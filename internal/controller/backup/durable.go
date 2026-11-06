package backup

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"syscall"
)

// File is a file being written: Sync is its fsync.
type File interface {
	io.Writer
	Sync() error
	Close() error
}

// Ops are the file operations of a restore and its recovery, so tests can crash between any two.
type Ops interface {
	// Create creates path with perm, opened O_WRONLY|O_CREATE|O_EXCL|O_NOFOLLOW|O_CLOEXEC.
	Create(path string, perm fs.FileMode) (File, error)
	// Rename renames from to to, replacing an existing to.
	Rename(from, to string) error
	// Remove removes path; a missing path is not an error.
	Remove(path string) error
	// SyncDir fsyncs the directory dir.
	SyncDir(dir string) error
	// Secure opens path read-only without following a symbolic link, requires the open
	// descriptor to be a regular file with one link, and then sets owner and mode through that
	// descriptor. Anything else is refused with an error saying the path is not a single
	// regular file.
	Secure(path string, uid, gid int, perm fs.FileMode) error
	// Exists reports whether path exists, by Lstat: a symbolic link exists.
	Exists(path string) (bool, error)
}

// OSOps returns the Ops of the operating system.
func OSOps() Ops {
	return osOps{}
}

type osOps struct{}

func (osOps) Create(path string, perm fs.FileMode) (File, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, perm) //nolint:gosec // G304: restore paths come from paths.Paths, never from a request
	if err != nil {
		// A nil *os.File in the File interface would not compare equal to nil.
		return nil, err
	}
	return f, nil
}

func (osOps) Rename(from, to string) error {
	return os.Rename(from, to)
}

func (osOps) Remove(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func (osOps) SyncDir(dir string) error {
	d, err := os.Open(dir) //nolint:gosec // G304: restore directories come from paths.Paths, never from a request
	if err != nil {
		return err
	}
	errSync := d.Sync()
	errClose := d.Close()
	if errSync != nil {
		return errSync
	}
	return errClose
}

func (osOps) Secure(path string, uid, gid int, perm fs.FileMode) error {
	// The check and both changes use this one descriptor, so nothing can be swapped in under
	// path between them. O_NONBLOCK keeps a named pipe from blocking the open until the Stat
	// below refuses it.
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0) //nolint:gosec // G304: restore paths come from paths.Paths, never from a request
	if errors.Is(err, syscall.ELOOP) {
		return fmt.Errorf("backup: %s is not a single regular file", path)
	}
	if err != nil {
		return fmt.Errorf("backup: secure %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		return fmt.Errorf("backup: secure %s: %w", path, err)
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); !fi.Mode().IsRegular() || !ok || st.Nlink != 1 {
		return fmt.Errorf("backup: %s is not a single regular file", path)
	}
	if err := f.Chown(uid, gid); err != nil {
		return fmt.Errorf("backup: secure %s: %w", path, err)
	}
	if err := f.Chmod(perm); err != nil {
		return fmt.Errorf("backup: secure %s: %w", path, err)
	}
	return nil
}

func (osOps) Exists(path string) (bool, error) {
	_, err := os.Lstat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return false, err
}
