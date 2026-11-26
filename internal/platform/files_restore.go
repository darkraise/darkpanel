package platform

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"golang.org/x/sys/unix"

	"github.com/darkraise/darkpanel/internal/core"
)

// Restore writes e.Path with e's data, mode and owner, or removes it when e is
// not present; module must declare the path as kind restore. Only a regular
// file is written or removed. Nothing is recorded: a restore path is a file of
// the host that uninstall never removes.
func (f *Files) Restore(ctx context.Context, module string, e core.RestoreEntry) error {
	if _, err := f.declaredKind(module, e.Path, core.OwnedRestore); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	defer f.lock(e.Path)()
	dir, name := filepath.Split(e.Path)
	fd, err := f.openDir(dir)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(fd) }()
	if e.Present {
		return writeFileAt(fd, name, e.Path, e.Data, e.Mode, e.UID, e.GID)
	}
	var st unix.Stat_t
	err = unix.Fstatat(fd, name, &st, unix.AT_SYMLINK_NOFOLLOW)
	switch {
	case errors.Is(err, unix.ENOENT):
		return nil
	case err != nil:
		return fmt.Errorf("platform: stat %s: %w", e.Path, err)
	case st.Mode&unix.S_IFMT != unix.S_IFREG:
		return fmt.Errorf("platform: %s is not a regular file", e.Path)
	}
	if err := unix.Unlinkat(fd, name, 0); err != nil && !errors.Is(err, unix.ENOENT) {
		return fmt.Errorf("platform: remove %s: %w", e.Path, err)
	}
	if err := unix.Fsync(fd); err != nil {
		return fmt.Errorf("platform: sync directory of %s: %w", e.Path, err)
	}
	return nil
}

// MakeDir creates dir, which module declares as kind dir, root-owned with
// mode 0755 when it is absent; its parent must exist. Nothing is recorded:
// the directory is the package's that later installs into it, so uninstall
// leaves it.
func (f *Files) MakeDir(ctx context.Context, module, dir string) error {
	if _, err := f.declaredKind(module, dir, core.OwnedDir); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	parentDir, name := filepath.Split(dir)
	pfd, err := f.openDir(parentDir)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(pfd) }()
	err = unix.Mkdirat(pfd, name, 0o755)
	if errors.Is(err, unix.EEXIST) {
		fd, err := openBeneath(pfd, name, unix.O_RDONLY|unix.O_DIRECTORY, 0)
		if err != nil {
			return fmt.Errorf("platform: open directory %s: %w", dir, err)
		}
		return unix.Close(fd)
	}
	if err != nil {
		return fmt.Errorf("platform: create directory %s: %w", dir, err)
	}
	// The umask may have cleared bits of the requested mode.
	if err := unix.Fchmodat(pfd, name, 0o755, 0); err != nil {
		return fmt.Errorf("platform: set the mode of %s: %w", dir, err)
	}
	if err := unix.Fsync(pfd); err != nil {
		return fmt.Errorf("platform: sync directory of %s: %w", dir, err)
	}
	return nil
}
