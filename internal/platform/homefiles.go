package platform

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/darkraise/darkpanel/internal/core"
)

// MaxAuthorizedKeys is the largest authorized_keys file HomeFiles reads or
// writes.
const MaxAuthorizedKeys = 1 << 20

const (
	sshDirName         = ".ssh"
	authorizedKeysName = "authorized_keys"
)

// ErrHomePolicy marks a refusal of HomeFiles under its file rules (a
// symbolic link, an owner, a mode, a size, a file of the wrong type), as
// opposed to an I/O error or a cancelled context.
var ErrHomePolicy = errors.New("home file policy")

// policyError is a refusal under the home file rules: its text alone, with
// ErrHomePolicy beneath it for errors.Is.
type policyError struct{ msg string }

func (e *policyError) Error() string { return e.msg }
func (e *policyError) Unwrap() error { return ErrHomePolicy }

func policyRefusal(format string, args ...any) error {
	return &policyError{msg: fmt.Sprintf(format, args...)}
}

// afterSSHMkdir, when set by a test, runs between the mkdir of .ssh and its
// open, where a user who owns the home could swap the name.
var afterSSHMkdir func()

// HomeFiles is the core.HomeFiles of the agent: the one file it touches is
// ~user/.ssh/authorized_keys, under sshd's StrictModes rules, never through a
// symbolic link (ADR 0020).
type HomeFiles struct {
	passwd string
}

var _ core.HomeFiles = (*HomeFiles)(nil)

// NewHomeFiles returns the HomeFiles that resolves homes from passwd, the
// path of /etc/passwd.
func NewHomeFiles(passwd string) *HomeFiles { return &HomeFiles{passwd: passwd} }

type homeUser struct {
	name     string
	uid, gid int
	home     string
}

func (h *HomeFiles) lookup(user string) (homeUser, error) {
	data, err := ReadRegularFile(h.passwd, 16<<20)
	if err != nil {
		return homeUser{}, err
	}
	for line := range bytes.Lines(data) {
		f := strings.Split(strings.TrimRight(string(line), "\r\n"), ":")
		if len(f) != 7 || f[0] != user {
			continue
		}
		uid, err1 := strconv.Atoi(f[2])
		gid, err2 := strconv.Atoi(f[3])
		if err1 != nil || err2 != nil {
			return homeUser{}, fmt.Errorf("platform: %s has an invalid passwd entry", user)
		}
		if !filepath.IsAbs(f[5]) || filepath.Clean(f[5]) != f[5] {
			return homeUser{}, fmt.Errorf("platform: the home of %s, %q, is not a clean absolute path", user, f[5])
		}
		return homeUser{name: user, uid: uid, gid: gid, home: f[5]}, nil
	}
	return homeUser{}, fmt.Errorf("platform: no account named %s", user)
}

// strictDir checks a directory as sshd's StrictModes does: owned by the user
// or root and not writable by group or others.
func strictDir(fd int, path string, u homeUser) error {
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return fmt.Errorf("platform: stat %s: %w", path, err)
	}
	switch {
	case int(st.Uid) != u.uid && st.Uid != 0:
		return policyRefusal("platform: %s is owned by uid %d, not by %s or root", path, st.Uid, u.name)
	case st.Mode&0o022 != 0:
		return policyRefusal("platform: %s is writable by group or others", path)
	}
	return nil
}

// openHome opens the user's home and, unless it is missing and create is
// false, its .ssh directory; sshFd is -1 when .ssh is missing.
func (h *HomeFiles) openHome(u homeUser, create bool) (homeFd, sshFd int, err error) {
	homeFd, err = unix.Openat2(unix.AT_FDCWD, u.home, &unix.OpenHow{
		Flags: unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC, Resolve: unix.RESOLVE_NO_SYMLINKS,
	})
	switch {
	case errors.Is(err, unix.ELOOP):
		return -1, -1, policyRefusal("platform: the home %s has a symbolic link in its path", u.home)
	case err != nil:
		return -1, -1, fmt.Errorf("platform: open the home %s: %w", u.home, err)
	}
	fail := func(err error) (int, int, error) {
		_ = unix.Close(homeFd)
		return -1, -1, err
	}
	if err := strictDir(homeFd, u.home, u); err != nil {
		return fail(err)
	}
	sshPath := filepath.Join(u.home, sshDirName)
	sshFd, err = openBeneath(homeFd, sshDirName, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if errors.Is(err, unix.ENOENT) && create {
		if sshFd, err = makeSSHDir(homeFd, sshPath, u); err != nil {
			return fail(err)
		}
	}
	switch {
	case errors.Is(err, unix.ENOENT):
		return homeFd, -1, nil
	case errors.Is(err, unix.ELOOP) || errors.Is(err, unix.ENOTDIR) && isSymlinkAt(homeFd, sshDirName):
		return fail(policyRefusal("platform: %s is a symbolic link", sshPath))
	case errors.Is(err, unix.ENOTDIR):
		return fail(policyRefusal("platform: %s is not a directory", sshPath))
	case err != nil:
		return fail(fmt.Errorf("platform: open %s: %w", sshPath, err))
	}
	if err := strictDir(sshFd, sshPath, u); err != nil {
		_ = unix.Close(sshFd)
		return fail(err)
	}
	return homeFd, sshFd, nil
}

// makeSSHDir creates .ssh in homeFd and gives it to the user through a
// descriptor of the directory it made: the user owns the home and could
// swap the name between the mkdir and a chown by name.
func makeSSHDir(homeFd int, sshPath string, u homeUser) (int, error) {
	if err := unix.Mkdirat(homeFd, sshDirName, 0o700); err != nil {
		return -1, fmt.Errorf("platform: create %s: %w", sshPath, err)
	}
	if afterSSHMkdir != nil {
		afterSSHMkdir()
	}
	fd, err := openBeneath(homeFd, sshDirName, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return -1, fmt.Errorf("platform: open %s: %w", sshPath, err)
	}
	var st unix.Stat_t
	err = unix.Fstat(fd, &st)
	switch {
	case err != nil:
		err = fmt.Errorf("platform: stat %s: %w", sshPath, err)
	case int(st.Uid) != unix.Geteuid() || st.Mode&0o777 != 0o700:
		err = policyRefusal("platform: %s changed while it was created", sshPath)
	default:
		if err = unix.Fchown(fd, u.uid, u.gid); err != nil {
			err = fmt.Errorf("platform: chown %s: %w", sshPath, err)
		}
	}
	if err != nil {
		_ = unix.Close(fd)
		return -1, err
	}
	return fd, nil
}

// isSymlinkAt reports whether name in dirfd is a symbolic link: O_NOFOLLOW
// with O_DIRECTORY reports one as ENOTDIR.
func isSymlinkAt(dirfd int, name string) bool {
	var st unix.Stat_t
	return unix.Fstatat(dirfd, name, &st, unix.AT_SYMLINK_NOFOLLOW) == nil && st.Mode&unix.S_IFMT == unix.S_IFLNK
}

// checkKeysFile checks an open authorized_keys: a regular file owned by the
// user or root of at most MaxAuthorizedKeys bytes.
func checkKeysFile(st unix.Stat_t, path string, u homeUser) error {
	switch {
	case st.Mode&unix.S_IFMT == unix.S_IFLNK:
		return policyRefusal("platform: %s is a symbolic link", path)
	case st.Mode&unix.S_IFMT != unix.S_IFREG:
		return policyRefusal("platform: %s is not a regular file", path)
	case int(st.Uid) != u.uid && st.Uid != 0:
		return policyRefusal("platform: %s is owned by uid %d, not by %s or root", path, st.Uid, u.name)
	case st.Size > MaxAuthorizedKeys:
		return policyRefusal("platform: %s is larger than 1 MiB", path)
	}
	return nil
}

// ReadAuthorizedKeys returns the user's authorized_keys, nil when it or
// .ssh does not exist.
func (h *HomeFiles) ReadAuthorizedKeys(ctx context.Context, user string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	u, err := h.lookup(user)
	if err != nil {
		return nil, err
	}
	homeFd, sshFd, err := h.openHome(u, false)
	if err != nil {
		return nil, err
	}
	defer func() { _ = unix.Close(homeFd) }()
	if sshFd < 0 {
		return nil, nil
	}
	defer func() { _ = unix.Close(sshFd) }()
	path := filepath.Join(u.home, sshDirName, authorizedKeysName)
	// O_NONBLOCK keeps a FIFO planted there from blocking the open.
	fd, err := openBeneath(sshFd, authorizedKeysName, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	switch {
	case errors.Is(err, unix.ENOENT):
		return nil, nil
	case errors.Is(err, unix.ELOOP):
		return nil, policyRefusal("platform: %s is a symbolic link", path)
	case err != nil:
		return nil, fmt.Errorf("platform: open %s: %w", path, err)
	}
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("platform: stat %s: %w", path, err)
	}
	if err := checkKeysFile(st, path, u); err != nil {
		_ = unix.Close(fd)
		return nil, err
	}
	return readOpenRegular(fd, path, MaxAuthorizedKeys)
}

// WriteAuthorizedKeys replaces the user's authorized_keys with data, owned
// by the user with mode 0600, creating .ssh with mode 0700 when it is
// missing.
func (h *HomeFiles) WriteAuthorizedKeys(ctx context.Context, user string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(data) > MaxAuthorizedKeys {
		return policyRefusal("platform: the authorized keys of %s would be larger than 1 MiB", user)
	}
	u, err := h.lookup(user)
	if err != nil {
		return err
	}
	homeFd, sshFd, err := h.openHome(u, true)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(homeFd) }()
	defer func() { _ = unix.Close(sshFd) }()
	path := filepath.Join(u.home, sshDirName, authorizedKeysName)
	var st unix.Stat_t
	switch err := unix.Fstatat(sshFd, authorizedKeysName, &st, unix.AT_SYMLINK_NOFOLLOW); {
	case errors.Is(err, unix.ENOENT):
	case err != nil:
		return fmt.Errorf("platform: stat %s: %w", path, err)
	default:
		if err := checkKeysFile(st, path, u); err != nil {
			return err
		}
	}
	return replaceAt(sshFd, authorizedKeysName, path, data, 0o600, u.uid, u.gid)
}
