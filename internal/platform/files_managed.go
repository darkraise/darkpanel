package platform

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"

	"golang.org/x/sys/unix"

	"github.com/darkraise/darkpanel/internal/core"
)

// managedDirMode is the mode of a managed directory: its files may be
// secrets (tunnel tokens), so only root reads them.
const managedDirMode = 0o700

// managedDefaultName is a managed directory's file-name pattern when the
// declaration sets none.
var managedDefaultName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// WriteManaged writes the regular file name directly inside dir, which module
// declares as kind managed-dir, with data, mode's permission bits and the
// owner uid:gid. The directory is recorded, then created root-owned with mode
// 0700, on the first write; an existing directory the ownership store does not
// record as module's is refused.
func (f *Files) WriteManaged(ctx context.Context, module, dir, name string, data []byte, mode fs.FileMode, uid, gid int) error {
	op, err := f.declaredKind(module, dir, core.OwnedManagedDir)
	if err != nil {
		return err
	}
	if err := managedName(op, name); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	defer f.lock(dir)()
	fd, err := f.openManaged(ctx, module, dir, true)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(fd) }()
	return writeFileAt(fd, name, filepath.Join(dir, name), data, mode, uid, gid)
}

// RemoveManaged removes the file name from dir, which module declares as kind
// managed-dir. A missing directory or name is not an error, a symlink is
// unlinked without being followed, and a subdirectory or a directory the
// ownership store does not record as module's is refused.
func (f *Files) RemoveManaged(ctx context.Context, module, dir, name string) error {
	op, err := f.declaredKind(module, dir, core.OwnedManagedDir)
	if err != nil {
		return err
	}
	if err := managedName(op, name); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	defer f.lock(dir)()
	fd, err := f.openManaged(ctx, module, dir, false)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(fd) }()
	path := filepath.Join(dir, name)
	var st unix.Stat_t
	err = unix.Fstatat(fd, name, &st, unix.AT_SYMLINK_NOFOLLOW)
	switch {
	case errors.Is(err, unix.ENOENT):
		return nil
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
	return nil
}

// managedName refuses a name that is not one flat entry matching the
// declaration's Pattern, or the default pattern without one.
func managedName(op core.OwnedPath, name string) error {
	pattern := managedDefaultName
	if op.Pattern != "" {
		var err error
		if pattern, err = regexp.Compile(op.Pattern); err != nil {
			return fmt.Errorf("platform: managed directory %s: pattern: %w", op.Path, err)
		}
	}
	if name == "." || name == ".." || filepath.Base(name) != name || !pattern.MatchString(name) {
		return fmt.Errorf("platform: %q is not a valid file name in %s", name, op.Path)
	}
	return nil
}

// openManaged opens dir, through its parent opened as openDir opens one, and
// checks that it is module's recorded directory, owned like that parent with
// no group or other access. With create, a missing dir is recorded, then made
// with mode 0700; without, a missing dir fails with an error wrapping
// fs.ErrNotExist.
func (f *Files) openManaged(ctx context.Context, module, dir string, create bool) (int, error) {
	parentDir, name := filepath.Split(dir)
	pfd, err := f.openDir(parentDir)
	if err != nil {
		return -1, err
	}
	defer func() { _ = unix.Close(pfd) }()
	fd, err := openBeneath(pfd, name, unix.O_RDONLY|unix.O_DIRECTORY, 0)
	switch {
	case errors.Is(err, unix.ENOENT) && create:
		if fd, err = f.createManaged(ctx, module, dir, pfd, name); err != nil {
			return -1, err
		}
	case err != nil:
		return -1, fmt.Errorf("platform: open directory %s: %w", dir, err)
	default:
		owned, err := f.cfg.Ownership.Owns(ctx, module, dir)
		if err == nil && !owned {
			err = fmt.Errorf("platform: directory %s exists and is not Darkpanel's", dir)
		}
		if err != nil {
			_ = unix.Close(fd)
			return -1, err
		}
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
	case st.Mode&0o077 != 0:
		_ = unix.Close(fd)
		return -1, fmt.Errorf("platform: directory %s has mode %o, want %o", dir, st.Mode&0o7777, managedDirMode)
	}
	return fd, nil
}

// createManaged records dir as module's, makes it in pfd as name and opens it.
func (f *Files) createManaged(ctx context.Context, module, dir string, pfd int, name string) (int, error) {
	// Recorded before the directory exists, so a crash after the mkdir still
	// leaves it for uninstall to find.
	entry := core.OwnedEntry{Path: dir, Module: module, Kind: core.OwnedManagedDir, CreatedAt: f.cfg.Clock.Now().UTC()}
	if err := f.cfg.Ownership.Record(ctx, entry); err != nil {
		return -1, err
	}
	if err := unix.Mkdirat(pfd, name, managedDirMode); err != nil {
		return -1, fmt.Errorf("platform: create directory %s: %w", dir, err)
	}
	if err := unix.Fsync(pfd); err != nil {
		return -1, fmt.Errorf("platform: sync directory of %s: %w", dir, err)
	}
	fd, err := openBeneath(pfd, name, unix.O_RDONLY|unix.O_DIRECTORY, 0)
	if err != nil {
		return -1, fmt.Errorf("platform: open directory %s: %w", dir, err)
	}
	// Mkdirat applies the umask; the mode must not depend on it.
	if err := unix.Fchmod(fd, managedDirMode); err != nil {
		_ = unix.Close(fd)
		return -1, fmt.Errorf("platform: chmod directory %s: %w", dir, err)
	}
	return fd, nil
}
