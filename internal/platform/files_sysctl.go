package platform

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/darkraise/darkpanel/internal/core"
)

const (
	// procSys is the sysctl root of a host.
	procSys = "/proc/sys"
	// sysctlIfaceSegment is the key segment that stands for a network
	// interface (net/ipv6/conf/<iface>/forwarding).
	sysctlIfaceSegment = "<iface>"
	// maxSysctlValue bounds a read; no parameter Darkpanel declares comes near.
	maxSysctlValue = 64 << 10
)

// sysctlIface is an interface name as a key segment: all, default, or a
// kernel interface name (IFNAMSIZ is 16 with the NUL).
var sysctlIface = regexp.MustCompile(`^[A-Za-z0-9_.@-]{1,15}$`)

// ReadSysctl returns the value of the kernel parameter key, which module
// declares as kind sysctl, without one trailing newline.
func (f *Files) ReadSysctl(ctx context.Context, module, key string) (string, error) {
	if _, err := f.sysctlDeclared(module, key); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	root, err := f.openProcSys()
	if err != nil {
		return "", err
	}
	defer func() { _ = unix.Close(root) }()
	return readSysctl(root, key)
}

// WriteSysctl sets the kernel parameter key, which module declares as kind
// sysctl, to value, which must match the declaration's Pattern. procfs allows
// neither a temporary file nor a rename, so the value goes in one write at
// offset 0, and the key is read back to confirm the kernel took it. Nothing is
// recorded: uninstall never touches a kernel parameter.
func (f *Files) WriteSysctl(ctx context.Context, module, key, value string) error {
	op, err := f.sysctlDeclared(module, key)
	if err != nil {
		return err
	}
	pattern, err := regexp.Compile(op.Pattern)
	if err != nil {
		return fmt.Errorf("platform: sysctl %s: pattern: %w", op.Path, err)
	}
	if !pattern.MatchString(value) {
		return fmt.Errorf("platform: sysctl %s: value %q does not match %s", key, value, op.Pattern)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	defer f.lock(op.Path)()
	root, err := f.openProcSys()
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(root) }()
	fd, err := openSysctl(root, key, unix.O_WRONLY)
	if err != nil {
		return err
	}
	n, err := unix.Pwrite(fd, []byte(value), 0)
	if cerr := unix.Close(fd); err == nil {
		err = cerr
	}
	switch {
	case err != nil:
		return fmt.Errorf("platform: write sysctl %s: %w", key, err)
	case n != len(value):
		return fmt.Errorf("platform: write sysctl %s: wrote %d of %d bytes", key, n, len(value))
	}
	got, err := readSysctl(root, key)
	if err != nil {
		return err
	}
	if got != value {
		return fmt.Errorf("platform: sysctl %s reads back %q after writing %q", key, got, value)
	}
	return nil
}

// sysctlDeclared returns module's sysctl declaration that key instantiates:
// equal segment by segment, where an <iface> segment of the declaration takes
// any valid interface name.
func (f *Files) sysctlDeclared(module, key string) (core.OwnedPath, error) {
	segs := strings.Split(key, "/")
	for _, op := range f.cfg.Owned[module] {
		if op.Kind == core.OwnedSysctl && sysctlMatches(strings.Split(op.Path, "/"), segs) {
			return op, nil
		}
	}
	return core.OwnedPath{}, fmt.Errorf("platform: module %s does not declare sysctl %s", module, key)
}

func sysctlMatches(decl, segs []string) bool {
	if len(decl) != len(segs) {
		return false
	}
	for i, d := range decl {
		switch {
		case d == sysctlIfaceSegment:
			if segs[i] == "." || segs[i] == ".." || !sysctlIface.MatchString(segs[i]) {
				return false
			}
		case d != segs[i]:
			return false
		}
	}
	return true
}

// openProcSys opens the sysctl root and, for the real /proc/sys, checks that
// procfs is mounted there, so a directory planted in place of an unmounted
// /proc is never written.
func (f *Files) openProcSys() (int, error) {
	root := f.cfg.ProcSys
	if root == "" {
		root = procSys
	}
	fd, err := unix.Openat2(unix.AT_FDCWD, root, &unix.OpenHow{
		Flags:   unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC,
		Resolve: unix.RESOLVE_NO_SYMLINKS,
	})
	if err != nil {
		return -1, fmt.Errorf("platform: open %s: %v", root, err) //nolint:errorlint // a missing sysctl root must not read as a missing key (fs.ErrNotExist)
	}
	if f.cfg.ProcSys == "" {
		ok, err := isProcfs(fd)
		if err == nil && !ok {
			err = errors.New("not procfs")
		}
		if err != nil {
			_ = unix.Close(fd)
			return -1, fmt.Errorf("platform: %s: %w", root, err)
		}
	}
	return fd, nil
}

// isProcfs reports whether fd lies on a procfs mount.
func isProcfs(fd int) (bool, error) {
	var st unix.Statfs_t
	if err := unix.Fstatfs(fd, &st); err != nil {
		return false, fmt.Errorf("platform: fstatfs: %w", err)
	}
	return st.Type == unix.PROC_SUPER_MAGIC, nil
}

// openSysctl opens key below root, refusing a symlink anywhere in it, and
// checks that it is a regular file. A missing key, an interface that has gone
// among them, fails with an error wrapping fs.ErrNotExist.
func openSysctl(root int, key string, flags uint64) (int, error) {
	fd, err := openBeneath(root, key, flags, 0)
	if err != nil {
		return -1, fmt.Errorf("platform: open sysctl %s: %w", key, err)
	}
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		_ = unix.Close(fd)
		return -1, fmt.Errorf("platform: stat sysctl %s: %w", key, err)
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG {
		_ = unix.Close(fd)
		return -1, fmt.Errorf("platform: sysctl %s is not a regular file", key)
	}
	return fd, nil
}

func readSysctl(root int, key string) (string, error) {
	fd, err := openSysctl(root, key, unix.O_RDONLY)
	if err != nil {
		return "", err
	}
	file := os.NewFile(uintptr(fd), key)
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, maxSysctlValue))
	if err != nil {
		return "", fmt.Errorf("platform: read sysctl %s: %w", key, err)
	}
	return strings.TrimSuffix(string(data), "\n"), nil
}
