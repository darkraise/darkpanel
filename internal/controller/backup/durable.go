package backup

import (
	"errors"
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
	// Chown changes the owner of path without following a symbolic link (lchown).
	Chown(path string, uid, gid int) error
	// Chmod changes the mode of path.
	Chmod(path string, perm fs.FileMode) error
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

func (osOps) Chown(path string, uid, gid int) error {
	return os.Lchown(path, uid, gid)
}

func (osOps) Chmod(path string, perm fs.FileMode) error {
	return os.Chmod(path, perm)
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
