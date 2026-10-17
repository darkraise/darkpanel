package platform

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"golang.org/x/sys/unix"

	"github.com/darkraise/darkpanel/internal/core"
)

// The owned-path kinds of core.OwnedPath and core.OwnedEntry.
const (
	kindFile  = "file"
	kindBlock = "block"
)

// newBlockFileMode is the mode of a file ReplaceBlock creates.
const newBlockFileMode = 0o644

// FilesConfig configures a Files writer.
type FilesConfig struct {
	Owned     map[string][]core.OwnedPath // the paths each module declares, by module id
	Ownership core.Ownership
	Clock     core.Clock
	TestMode  bool // parent directories must be owned by the effective uid instead of root
}

// Files is the core.FileWriter of the agent: it writes only the paths and
// marked blocks a module declares, never through a symlink (ADR 0009). Calls
// on one path are serialised, so the agent must build exactly one Files.
type Files struct {
	cfg       FilesConfig
	parentUID int
	locks     map[string]*sync.Mutex // one per declared path; read-only after NewFiles
}

var _ core.FileWriter = (*Files)(nil)

// NewFiles returns a writer for cfg.
func NewFiles(cfg FilesConfig) *Files {
	uid := 0
	if cfg.TestMode {
		uid = os.Geteuid()
	}
	locks := map[string]*sync.Mutex{}
	for _, paths := range cfg.Owned {
		for _, op := range paths {
			if locks[op.Path] == nil {
				locks[op.Path] = &sync.Mutex{}
			}
		}
	}
	return &Files{cfg: cfg, parentUID: uid, locks: locks}
}

// OwnedPaths collects the owned paths of the manifests by module id, for
// FilesConfig.Owned.
func OwnedPaths(mods []core.Manifest) map[string][]core.OwnedPath {
	out := map[string][]core.OwnedPath{}
	for _, m := range mods {
		if len(m.OwnedPaths) > 0 {
			out[m.ID] = slices.Clone(m.OwnedPaths)
		}
	}
	return out
}

// declared returns module's declaration of path as kind, which for a block
// must list blockID.
func (f *Files) declared(module, path, kind, blockID string) (core.OwnedPath, error) {
	if filepath.IsAbs(path) && filepath.Clean(path) == path && filepath.Base(path) != "/" {
		for _, op := range f.cfg.Owned[module] {
			if op.Path == path && op.Kind == kind && (kind == kindFile || slices.Contains(op.BlockIDs, blockID)) {
				return op, nil
			}
		}
	}
	if kind == kindBlock {
		return core.OwnedPath{}, fmt.Errorf("platform: module %s does not declare block %s of %s", module, blockID, path)
	}
	return core.OwnedPath{}, fmt.Errorf("platform: module %s does not declare file %s", module, path)
}

// parent is a target's parent directory, opened without following any
// symlink and checked for ownership and mode. It holds the target's path lock
// until close.
type parent struct {
	fd   int
	name string      // the target's name inside the directory
	path string      // the target's full path, for messages
	mu   *sync.Mutex // the target's lock in Files.locks
}

// openParent locks path, which the caller has already checked is declared,
// and opens its parent directory.
func (f *Files) openParent(path string) (*parent, error) {
	// Held from the read to the rename: modules editing blocks of one file
	// under different lock keys (ADR 0004) would otherwise drop each other's
	// block. A lock in process suffices because the agent is the only writer.
	mu := f.locks[path]
	mu.Lock()
	dir, name := filepath.Split(path)
	fd, err := f.openDir(dir)
	if err != nil {
		mu.Unlock()
		return nil, err
	}
	return &parent{fd: fd, name: name, path: path, mu: mu}, nil
}

func (f *Files) openDir(dir string) (int, error) {
	// RESOLVE_NO_SYMLINKS fails if any component of dir is a symlink, which
	// O_NOFOLLOW alone would not catch (ADR 0009).
	fd, err := unix.Openat2(unix.AT_FDCWD, dir, &unix.OpenHow{
		Flags:   unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC,
		Resolve: unix.RESOLVE_NO_SYMLINKS,
	})
	if err != nil {
		return -1, fmt.Errorf("platform: open directory %s: %w", dir, err)
	}
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		_ = unix.Close(fd)
		return -1, fmt.Errorf("platform: stat directory %s: %w", dir, err)
	}
	switch {
	case int(st.Uid) != f.parentUID:
		_ = unix.Close(fd)
		return -1, fmt.Errorf("platform: directory %s is owned by uid %d, want %d", dir, st.Uid, f.parentUID)
	case st.Mode&0o022 != 0:
		_ = unix.Close(fd)
		return -1, fmt.Errorf("platform: directory %s is writable by group or others", dir)
	}
	return fd, nil
}

func (p *parent) close() {
	_ = unix.Close(p.fd)
	p.mu.Unlock()
}

// read returns the target's content and status; exists is false when there
// is no target. A symlink or anything but a regular file is refused.
func (p *parent) read() (data []byte, st unix.Stat_t, exists bool, err error) {
	// O_NONBLOCK keeps a FIFO planted at the target from blocking the open.
	fd, err := unix.Openat(p.fd, p.name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	switch {
	case errors.Is(err, unix.ENOENT):
		return nil, st, false, nil
	case errors.Is(err, unix.ELOOP):
		return nil, st, false, fmt.Errorf("platform: %s is a symlink", p.path)
	case err != nil:
		return nil, st, false, fmt.Errorf("platform: open %s: %w", p.path, err)
	}
	file := os.NewFile(uintptr(fd), p.path)
	defer func() { _ = file.Close() }()
	if err := unix.Fstat(fd, &st); err != nil {
		return nil, st, false, fmt.Errorf("platform: stat %s: %w", p.path, err)
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG {
		return nil, st, false, fmt.Errorf("platform: %s is not a regular file", p.path)
	}
	if data, err = io.ReadAll(file); err != nil {
		return nil, st, false, fmt.Errorf("platform: read %s: %w", p.path, err)
	}
	return data, st, true, nil
}

// write replaces the target with data through a temporary file renamed over
// it, so readers see the old or the new content and never a mix. An existing
// target's mode and owner are kept; a new one gets mode.
func (p *parent) write(data []byte, mode uint32, existing *unix.Stat_t) error {
	var rnd [8]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return fmt.Errorf("platform: temporary name: %w", err)
	}
	tmp := fmt.Sprintf(".%s.darkpanel-%x", p.name, rnd)
	fd, err := unix.Openat(p.fd, tmp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return fmt.Errorf("platform: create temporary file for %s: %w", p.path, err)
	}
	file := os.NewFile(uintptr(fd), tmp)
	err = func() error {
		if _, err := file.Write(data); err != nil {
			return err
		}
		if existing != nil {
			mode = existing.Mode & 0o7777
			if err := unix.Fchown(fd, int(existing.Uid), int(existing.Gid)); err != nil {
				return err
			}
		}
		// After Fchown, which clears set-user-ID and set-group-ID bits.
		if err := unix.Fchmod(fd, mode); err != nil {
			return err
		}
		return file.Sync()
	}()
	err = errors.Join(err, file.Close())
	if err == nil {
		err = unix.Renameat(p.fd, tmp, p.fd, p.name)
	}
	if err != nil {
		_ = unix.Unlinkat(p.fd, tmp, 0)
		return fmt.Errorf("platform: write %s: %w", p.path, err)
	}
	if err := unix.Fsync(p.fd); err != nil {
		return fmt.Errorf("platform: sync directory of %s: %w", p.path, err)
	}
	return nil
}

// edit rewrites path with change applied to its current content (empty when
// it does not exist), recording ownership first.
func (f *Files) edit(ctx context.Context, module, path, blockID string, mode uint32, change func(old []byte) ([]byte, error)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p, err := f.openParent(path)
	if err != nil {
		return err
	}
	defer p.close()
	old, st, exists, err := p.read()
	if err != nil {
		return err
	}
	data, err := change(old)
	if err != nil {
		return err
	}
	kind := kindFile
	if blockID != "" {
		kind = kindBlock
	}
	// Recorded before the host is touched, so a crash after the write still
	// leaves the path for uninstall to find.
	entry := core.OwnedEntry{Path: path, Module: module, Kind: kind, BlockID: blockID, CreatedAt: f.cfg.Clock.Now().UTC()}
	if err := f.cfg.Ownership.Record(ctx, entry); err != nil {
		return err
	}
	var existing *unix.Stat_t
	if exists {
		existing = &st
	}
	return p.write(data, mode, existing)
}

// WriteFile replaces the whole file path, which module must declare as a
// file; a new file gets mode's permission bits.
func (f *Files) WriteFile(ctx context.Context, module, path string, data []byte, mode fs.FileMode) error {
	if _, err := f.declared(module, path, kindFile, ""); err != nil {
		return err
	}
	return f.edit(ctx, module, path, "", uint32(mode.Perm()), func([]byte) ([]byte, error) { return data, nil })
}

// ReplaceBlock sets the content of block blockID in path, appending the block
// when absent and creating the file (mode 0644) when missing.
func (f *Files) ReplaceBlock(ctx context.Context, module, path, blockID string, content []byte) error {
	op, err := f.declared(module, path, kindBlock, blockID)
	if err != nil {
		return err
	}
	prefix := commentPrefix(op)
	return f.edit(ctx, module, path, blockID, newBlockFileMode, func(old []byte) ([]byte, error) {
		return replaceBlock(old, prefix, module, blockID, content)
	})
}

// RemoveBlock deletes block blockID from path and forgets it; a missing file,
// block or parent directory is not an error.
func (f *Files) RemoveBlock(ctx context.Context, module, path, blockID string) error {
	op, err := f.declared(module, path, kindBlock, blockID)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	p, err := f.openParent(path)
	switch {
	case errors.Is(err, unix.ENOENT):
		// Without its directory the file is gone too; only the record is left.
		// A symlinked parent fails with ELOOP and stays an error.
		return f.cfg.Ownership.Forget(ctx, module, path, blockID)
	case err != nil:
		return err
	}
	defer p.close()
	old, st, exists, err := p.read()
	if err != nil {
		return err
	}
	if exists {
		data, err := removeBlock(old, commentPrefix(op), module, blockID)
		if err != nil {
			return err
		}
		if !bytes.Equal(data, old) {
			if err := p.write(data, 0, &st); err != nil {
				return err
			}
		}
	}
	return f.cfg.Ownership.Forget(ctx, module, path, blockID)
}

// Remove deletes the file path and forgets it; a missing file or parent
// directory is not an error. A symlink at path is removed itself, never
// followed.
func (f *Files) Remove(ctx context.Context, module, path string) error {
	if _, err := f.declared(module, path, kindFile, ""); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	p, err := f.openParent(path)
	switch {
	case errors.Is(err, unix.ENOENT):
		// Without its directory the file is gone too; only the record is left.
		// A symlinked parent fails with ELOOP and stays an error.
		return f.cfg.Ownership.Forget(ctx, module, path, "")
	case err != nil:
		return err
	}
	defer p.close()
	if err := unix.Unlinkat(p.fd, p.name, 0); err != nil && !errors.Is(err, unix.ENOENT) {
		return fmt.Errorf("platform: remove %s: %w", path, err)
	}
	if err := unix.Fsync(p.fd); err != nil {
		return fmt.Errorf("platform: sync directory of %s: %w", path, err)
	}
	return f.cfg.Ownership.Forget(ctx, module, path, "")
}

func commentPrefix(op core.OwnedPath) string {
	if op.CommentPrefix == "" {
		return "#"
	}
	return op.CommentPrefix
}
