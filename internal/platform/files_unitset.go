package platform

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"slices"

	"golang.org/x/sys/unix"

	"github.com/darkraise/darkpanel/internal/core"
)

// unitFileMode is the mode of a unit file WriteUnitInSet creates.
const unitFileMode = 0o644

// fullMatch reports whether name is one flat directory entry that pattern
// matches whole. The registry holds patterns to ^...$, and the match is
// anchored again here, so a pattern never matches part of a name.
func fullMatch(pattern, name string) bool {
	if name == "" || name == "." || name == ".." || filepath.Base(name) != name {
		return false
	}
	re, err := regexp.Compile(`^(?:` + pattern + `)$`)
	return err == nil && re.MatchString(name)
}

// InUnitSet reports whether one of the declarations ops is a unit set that
// holds the unit file path.
func InUnitSet(ops []core.OwnedPath, path string) bool {
	return slices.ContainsFunc(ops, func(op core.OwnedPath) bool {
		return op.Kind == core.OwnedUnitSet && op.Pattern != "" && filepath.Dir(path) == op.Path && fullMatch(op.Pattern, filepath.Base(path))
	})
}

// unitSet returns the unit set of module whose pattern matches name.
func (f *Files) unitSet(module, name string) (core.OwnedPath, error) {
	for _, op := range f.cfg.Owned[module] {
		if op.Kind == core.OwnedUnitSet && op.Pattern != "" && fullMatch(op.Pattern, name) {
			return op, nil
		}
	}
	return core.OwnedPath{}, fmt.Errorf("platform: module %s declares no unit set that holds %q", module, name)
}

// openInSet locks the unit set dir and opens it as openDir opens a parent,
// for the file name in it.
func (f *Files) openInSet(dir, name string) (*parent, error) {
	mu := f.locks[dir]
	mu.Lock()
	fd, err := f.openDir(dir)
	if err != nil {
		mu.Unlock()
		return nil, err
	}
	return &parent{fd: fd, name: name, path: filepath.Join(dir, name), mu: mu}, nil
}

// WriteUnitInSet writes the unit file name into the unit set of module that
// holds it, recording it first as a unit file of module. An existing file the ownership store does not
// record as module's is refused, so Darkpanel never takes over a unit it did
// not write. A new file gets mode 0644; an existing one keeps its mode and
// owner.
func (f *Files) WriteUnitInSet(ctx context.Context, module, name string, data []byte) error {
	op, err := f.unitSet(module, name)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	p, err := f.openInSet(op.Path, name)
	if err != nil {
		return err
	}
	defer p.close()
	_, st, exists, err := p.read()
	if err != nil {
		return err
	}
	if exists {
		if err := f.ownedUnit(ctx, module, p.path); err != nil {
			return err
		}
	}
	entry := core.OwnedEntry{Path: p.path, Module: module, Kind: core.OwnedUnit, CreatedAt: f.cfg.Clock.Now().UTC()}
	if err := f.cfg.Ownership.Record(ctx, entry); err != nil {
		return err
	}
	var existing *unix.Stat_t
	if exists {
		existing = &st
	}
	return p.write(data, unitFileMode, existing)
}

// RemoveUnitInSet removes the unit file name from the unit set of module
// that holds it and forgets it. A missing file is not an error; a file the
// ownership store does not record as module's, or a directory, is refused.
func (f *Files) RemoveUnitInSet(ctx context.Context, module, name string) error {
	op, err := f.unitSet(module, name)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	p, err := f.openInSet(op.Path, name)
	if err != nil {
		return err
	}
	defer p.close()
	var st unix.Stat_t
	err = unix.Fstatat(p.fd, name, &st, unix.AT_SYMLINK_NOFOLLOW)
	switch {
	case errors.Is(err, unix.ENOENT):
		return f.cfg.Ownership.Forget(ctx, module, p.path, "")
	case err != nil:
		return fmt.Errorf("platform: stat %s: %w", p.path, err)
	case st.Mode&unix.S_IFMT == unix.S_IFDIR:
		return fmt.Errorf("platform: %s is a directory", p.path)
	}
	if err := f.ownedUnit(ctx, module, p.path); err != nil {
		return err
	}
	if err := unix.Unlinkat(p.fd, name, 0); err != nil && !errors.Is(err, unix.ENOENT) {
		return fmt.Errorf("platform: remove %s: %w", p.path, err)
	}
	if err := unix.Fsync(p.fd); err != nil {
		return fmt.Errorf("platform: sync directory of %s: %w", p.path, err)
	}
	return f.cfg.Ownership.Forget(ctx, module, p.path, "")
}

// ownedUnit refuses the existing unit file path unless the ownership store
// records it as module's.
func (f *Files) ownedUnit(ctx context.Context, module, path string) error {
	owned, err := f.cfg.Ownership.Owns(ctx, module, path)
	if err != nil {
		return err
	}
	if !owned {
		return fmt.Errorf("platform: unit file %s exists and is not Darkpanel's", path)
	}
	return nil
}

// patternDirDecl refuses name unless module declares dir as kind dir with a
// Pattern that matches it.
func (f *Files) patternDirDecl(module, dir, name string) error {
	op, err := f.declaredKind(module, dir, core.OwnedDir)
	switch {
	case err != nil:
		return err
	case op.Pattern == "":
		return fmt.Errorf("platform: module %s declares dir %s without a file Pattern", module, dir)
	case !fullMatch(op.Pattern, name):
		return fmt.Errorf("platform: %q is not a valid file name in %s", name, dir)
	}
	return nil
}

// WriteDirFile writes the regular file name directly inside dir, which module
// declares as kind dir with a Pattern name matches, with data, mode's
// permission bits and group gid (-1 keeps the agent's), owned by the agent's
// user. The directory is recorded, then created with mode 0700, on the first
// write, and a recorded directory readable or executable by group or other
// is set to 0700; one writable by group or other is refused, as is an
// existing directory the ownership store does not record as module's. The file is recorded before it is written.
func (f *Files) WriteDirFile(ctx context.Context, module, dir, name string, data []byte, mode fs.FileMode, gid int) error {
	if err := f.patternDirDecl(module, dir, name); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	defer f.lock(dir)()
	fd, err := f.openRecorded(ctx, module, dir, patternDir, true)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(fd) }()
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return fmt.Errorf("platform: stat directory %s: %w", dir, err)
	}
	if st.Mode&0o077 != 0 {
		if err := unix.Fchmod(fd, patternDir.mode); err != nil {
			return fmt.Errorf("platform: chmod directory %s: %w", dir, err)
		}
	}
	path := filepath.Join(dir, name)
	entry := core.OwnedEntry{Path: path, Module: module, Kind: core.OwnedFile, CreatedAt: f.cfg.Clock.Now().UTC()}
	if err := f.cfg.Ownership.Record(ctx, entry); err != nil {
		return err
	}
	return writeFileAt(fd, name, path, data, mode, -1, gid)
}

// RemoveDirFile removes the file name from dir, which module declares as kind
// dir with a Pattern name matches, and forgets it. A missing directory or
// file is not an error, a symlink is unlinked without being followed, and a
// subdirectory or a directory the ownership store does not record as
// module's is refused.
func (f *Files) RemoveDirFile(ctx context.Context, module, dir, name string) error {
	if err := f.patternDirDecl(module, dir, name); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	defer f.lock(dir)()
	path := filepath.Join(dir, name)
	fd, err := f.openRecorded(ctx, module, dir, patternDir, false)
	if errors.Is(err, unix.ENOENT) {
		return f.cfg.Ownership.Forget(ctx, module, path, "")
	}
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(fd) }()
	var st unix.Stat_t
	err = unix.Fstatat(fd, name, &st, unix.AT_SYMLINK_NOFOLLOW)
	switch {
	case errors.Is(err, unix.ENOENT):
		return f.cfg.Ownership.Forget(ctx, module, path, "")
	case err != nil:
		return fmt.Errorf("platform: stat %s: %w", path, err)
	case st.Mode&unix.S_IFMT == unix.S_IFDIR:
		return fmt.Errorf("platform: %s is a directory", path)
	}
	if err := unix.Unlinkat(fd, name, 0); err != nil && !errors.Is(err, unix.ENOENT) {
		return fmt.Errorf("platform: remove %s: %w", path, err)
	}
	if err := unix.Fsync(fd); err != nil {
		return fmt.Errorf("platform: sync directory %s: %w", dir, err)
	}
	return f.cfg.Ownership.Forget(ctx, module, path, "")
}
