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
