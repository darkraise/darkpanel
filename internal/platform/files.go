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
	// ProcSys is the sysctl root; "" means /proc/sys, and only then is the
	// procfs check made, so tests can use a temporary tree.
	ProcSys string
	// LookupUser returns a service-dir owner's ids; nil means os/user.Lookup.
	LookupUser func(name string) (uid, gid int, err error)
}

// Files is the core.FileWriter of the agent: it writes only the paths,
// marked blocks, kernel parameters and directories a module declares, never
// through a symlink (ADR 0009). Declarations of kind dir and user are
// uninstall's to remove and are ignored here. Calls on one path are
// serialised, so the agent must build exactly one Files.
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
			if op.Kind == core.OwnedDir || op.Kind == core.OwnedUser {
				continue
			}
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

// declaredKind returns module's declaration of the absolute, clean path as
// kind. A sysctl key is matched by sysctlDeclared instead.
func (f *Files) declaredKind(module, path, kind string) (core.OwnedPath, error) {
	if filepath.IsAbs(path) && filepath.Clean(path) == path && path != "/" {
		for _, op := range f.cfg.Owned[module] {
			if op.Path == path && op.Kind == kind {
				return op, nil
			}
		}
	}
	return core.OwnedPath{}, fmt.Errorf("platform: module %s does not declare %s %s", module, kind, path)
}

// declaredFile returns module's declaration of path as a unit file, or else
// as a file.
func (f *Files) declaredFile(module, path string) (core.OwnedPath, error) {
	if op, err := f.declaredKind(module, path, core.OwnedUnit); err == nil {
		return op, nil
	}
	return f.declared(module, path, kindFile, "")
}

// lock takes path's lock and returns its release. path must be a declared
// path of a kind NewFiles makes a lock for.
func (f *Files) lock(path string) func() {
	mu := f.locks[path]
	mu.Lock()
	return mu.Unlock
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

// openBeneath opens name below dirfd with openat2, refusing a symlink in any
// component, name included, and any path that leaves dirfd (ADR 0009). The
// error is the bare errno, for the caller to test and wrap.
func openBeneath(dirfd int, name string, flags uint64, mode uint64) (int, error) {
	how := &unix.OpenHow{Flags: flags | unix.O_CLOEXEC, Mode: mode, Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS}
	var err error
	for range 8 {
		var fd int
		// RESOLVE_BENEATH fails with EAGAIN when a rename elsewhere raced the
		// lookup; repeating the lookup is safe.
		if fd, err = unix.Openat2(dirfd, name, how); !errors.Is(err, unix.EAGAIN) {
			return fd, err
		}
	}
	return -1, err
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
	uid, gid := -1, -1
	if existing != nil {
		mode, uid, gid = existing.Mode&0o7777, int(existing.Uid), int(existing.Gid)
	}
	return replaceAt(p.fd, p.name, p.path, data, mode, uid, gid)
}

// writeFileAt replaces the regular file name in dirfd with data, mode's
// permission bits and the owner uid:gid, refusing a symlink or anything but a
// regular file at name. path names the file in messages.
func writeFileAt(dirfd int, name, path string, data []byte, mode fs.FileMode, uid, gid int) error {
	var st unix.Stat_t
	err := unix.Fstatat(dirfd, name, &st, unix.AT_SYMLINK_NOFOLLOW)
	switch {
	case errors.Is(err, unix.ENOENT):
	case err != nil:
		return fmt.Errorf("platform: stat %s: %w", path, err)
	case st.Mode&unix.S_IFMT == unix.S_IFLNK:
		return fmt.Errorf("platform: %s is a symlink", path)
	case st.Mode&unix.S_IFMT != unix.S_IFREG:
		return fmt.Errorf("platform: %s is not a regular file", path)
	}
	return replaceAt(dirfd, name, path, data, uint32(mode.Perm()), uid, gid)
}

// replaceAt writes data to a temporary file in dirfd with mode and, unless
// uid and gid are both -1, that owner, syncs it, renames it over name and
// syncs the directory. The rename replaces a symlink at name, never follows it.
func replaceAt(dirfd int, name, path string, data []byte, mode uint32, uid, gid int) error {
	var rnd [8]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return fmt.Errorf("platform: temporary name: %w", err)
	}
	tmp := fmt.Sprintf(".%s.darkpanel-%x", name, rnd)
	fd, err := unix.Openat(dirfd, tmp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return fmt.Errorf("platform: create temporary file for %s: %w", path, err)
	}
	file := os.NewFile(uintptr(fd), tmp)
	err = func() error {
		if _, err := file.Write(data); err != nil {
			return err
		}
		if uid != -1 || gid != -1 {
			if err := unix.Fchown(fd, uid, gid); err != nil {
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
		err = unix.Renameat(dirfd, tmp, dirfd, name)
	}
	if err != nil {
		_ = unix.Unlinkat(dirfd, tmp, 0)
		return fmt.Errorf("platform: write %s: %w", path, err)
	}
	if err := unix.Fsync(dirfd); err != nil {
		return fmt.Errorf("platform: sync directory of %s: %w", path, err)
	}
	return nil
}

// edit rewrites path with change applied to its current content (empty when
// it does not exist), recording ownership as kind first.
func (f *Files) edit(ctx context.Context, module, path, kind, blockID string, mode uint32, change func(old []byte) ([]byte, error)) error {
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
	if kind == core.OwnedUnit && exists {
		owned, err := f.cfg.Ownership.Owns(ctx, module, path)
		if err != nil {
			return err
		}
		if !owned {
			return fmt.Errorf("platform: unit file %s exists and is not Darkpanel's", path)
		}
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
// file or a unit file; a new file gets mode's permission bits. An existing
// unit file the ownership store does not record as module's is refused, so
// Darkpanel never takes over a unit it did not create.
func (f *Files) WriteFile(ctx context.Context, module, path string, data []byte, mode fs.FileMode) error {
	op, err := f.declaredFile(module, path)
	if err != nil {
		return err
	}
	return f.edit(ctx, module, path, op.Kind, "", uint32(mode.Perm()), func([]byte) ([]byte, error) { return data, nil })
}

// ReplaceBlock sets the content of block blockID in path, appending the block
// when absent and creating the file (mode 0644) when missing.
func (f *Files) ReplaceBlock(ctx context.Context, module, path, blockID string, content []byte) error {
	op, err := f.declared(module, path, kindBlock, blockID)
	if err != nil {
		return err
	}
	prefix := commentPrefix(op)
	return f.edit(ctx, module, path, kindBlock, blockID, newBlockFileMode, func(old []byte) ([]byte, error) {
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

// Remove deletes the file or unit file path and forgets it; a missing file
// or parent directory is not an error. A symlink at path is removed itself,
// never followed.
func (f *Files) Remove(ctx context.Context, module, path string) error {
	if _, err := f.declaredFile(module, path); err != nil {
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

// Rename renames the declared file from to the declared file to in the same
// directory, replacing to, so a binary swap is one atomic, durable step. It
// records to before the rename and forgets from after it.
func (f *Files) Rename(ctx context.Context, module, from, to string) error {
	if _, err := f.declared(module, from, kindFile, ""); err != nil {
		return err
	}
	if _, err := f.declared(module, to, kindFile, ""); err != nil {
		return err
	}
	dir, fromName := filepath.Split(from)
	toDir, toName := filepath.Split(to)
	switch {
	case dir != toDir:
		return fmt.Errorf("platform: rename %s to %s: not in the same directory", from, to)
	case from == to:
		return fmt.Errorf("platform: rename %s onto itself", from)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// In path order, so two renames of the same pair cannot deadlock.
	first, second := min(from, to), max(from, to)
	defer f.lock(first)()
	defer f.lock(second)()
	fd, err := f.openDir(dir)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(fd) }()
	var st unix.Stat_t
	if err := unix.Fstatat(fd, fromName, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return fmt.Errorf("platform: rename %s: %w", from, err)
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG {
		return fmt.Errorf("platform: rename %s: not a regular file", from)
	}
	entry := core.OwnedEntry{Path: to, Module: module, Kind: kindFile, CreatedAt: f.cfg.Clock.Now().UTC()}
	if err := f.cfg.Ownership.Record(ctx, entry); err != nil {
		return err
	}
	if err := unix.Renameat(fd, fromName, fd, toName); err != nil {
		return fmt.Errorf("platform: rename %s to %s: %w", from, to, err)
	}
	if err := unix.Fsync(fd); err != nil {
		return fmt.Errorf("platform: sync directory of %s: %w", to, err)
	}
	return f.cfg.Ownership.Forget(ctx, module, from, "")
}

// Owns reports whether the ownership store records path as module's, of any
// kind.
func (f *Files) Owns(ctx context.Context, module, path string) (bool, error) {
	return f.cfg.Ownership.Owns(ctx, module, path)
}

func commentPrefix(op core.OwnedPath) string {
	if op.CommentPrefix == "" {
		return "#"
	}
	return op.CommentPrefix
}
