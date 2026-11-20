package platform

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/sys/unix"
)

// The refusals of a purge; each is returned wrapped with the path.
var (
	ErrPurgeSymlink = errors.New("platform: a symbolic link is on the path to purge")
	ErrPurgeMount   = errors.New("platform: the path to purge is or holds a mount point")
	ErrPurgePath    = errors.New("platform: only /var/lib/docker and /var/lib/containerd can be purged")
)

var purgeablePaths = []string{"/var/lib/docker", "/var/lib/containerd"}

// Purge deletes Docker's data directories, and nothing else.
type Purge interface {
	// CheckPurgeable refuses a path PurgeDir would refuse; an absent path
	// is purgeable.
	CheckPurgeable(path string) error
	// PurgeDir removes path and everything beneath it, never following a
	// symlink and never crossing into another mount.
	PurgeDir(path string) error
}

// DirPurge is the Purge of a host.
type DirPurge struct {
	root   string                   // "/" on a host; the purgeable paths are beneath it
	mounts func() ([]string, error) // the mount points of the host, as absolute paths
}

var _ Purge = (*DirPurge)(nil)

// NewPurge returns the host's Purge, reading mounts from
// /proc/self/mountinfo.
func NewPurge() *DirPurge { return newPurge("/", procMounts) }

func newPurge(root string, mounts func() ([]string, error)) *DirPurge {
	return &DirPurge{root: root, mounts: mounts}
}

// CheckPurgeable implements Purge. Beyond the path checks it walks the
// tree, so a directory on another device (a btrfs subvolume, which no mount
// table lists) refuses the plan before anything is removed, instead of
// stopping a purge halfway.
func (p *DirPurge) CheckPurgeable(path string) error {
	pfd, err := p.open(path)
	if err != nil || pfd < 0 {
		return err
	}
	defer func() { _ = unix.Close(pfd) }()
	fd, dev, err := openTop(pfd, path)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(fd) }()
	return checkDevices(fd, path, dev)
}

// openTop opens path, a child of the directory pfd, refusing a symlink and
// a directory on another device than its parent; it returns the
// descriptor and the device.
func openTop(pfd int, path string) (int, uint64, error) {
	fd, err := unix.Openat(pfd, filepath.Base(path), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if errors.Is(err, unix.ELOOP) {
		return -1, 0, fmt.Errorf("%w: %s", ErrPurgeSymlink, path)
	}
	if err != nil {
		return -1, 0, fmt.Errorf("platform: open %s: %w", path, err)
	}
	var parent, top unix.Stat_t
	if err := unix.Fstat(pfd, &parent); err != nil {
		_ = unix.Close(fd)
		return -1, 0, fmt.Errorf("platform: stat the directory of %s: %w", path, err)
	}
	if err := unix.Fstat(fd, &top); err != nil {
		_ = unix.Close(fd)
		return -1, 0, fmt.Errorf("platform: stat %s: %w", path, err)
	}
	if top.Dev != parent.Dev {
		_ = unix.Close(fd)
		return -1, 0, fmt.Errorf("%w: %s", ErrPurgeMount, path)
	}
	return fd, top.Dev, nil
}

// checkDevices refuses when any directory beneath fd, which is path on
// device dev, is on another device.
func checkDevices(fd int, path string, dev uint64) error {
	names, err := purgeListing(fd, path)
	if err != nil {
		return err
	}
	for _, n := range names {
		child := filepath.Join(path, n)
		var st unix.Stat_t
		if err := unix.Fstatat(fd, n, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			if errors.Is(err, unix.ENOENT) {
				continue
			}
			return fmt.Errorf("platform: stat %s: %w", child, err)
		}
		if st.Mode&unix.S_IFMT != unix.S_IFDIR {
			continue
		}
		if st.Dev != dev {
			return fmt.Errorf("%w: %s is on another device (a mount or a btrfs subvolume); delete it by hand", ErrPurgeMount, child)
		}
		cfd, err := unix.Openat(fd, n, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if errors.Is(err, unix.ELOOP) || errors.Is(err, unix.ENOENT) {
			continue
		}
		if err != nil {
			return fmt.Errorf("platform: open %s: %w", child, err)
		}
		err = checkDevices(cfd, child, dev)
		_ = unix.Close(cfd)
		if err != nil {
			return err
		}
	}
	return nil
}

// PurgeDir implements Purge. It runs CheckPurgeable's checks again, then
// deletes through directory descriptors: a symlink is unlinked, never
// followed, and a directory on another device than the path's is refused
// before anything beneath it is touched. A bind mount of the same device
// is caught only by the mount table, read just before the deletion.
func (p *DirPurge) PurgeDir(path string) error {
	pfd, err := p.open(path)
	if err != nil || pfd < 0 {
		return err
	}
	defer func() { _ = unix.Close(pfd) }()
	base := filepath.Base(path)
	fd, dev, err := openTop(pfd, path)
	if err != nil {
		return err
	}
	// Refuse before deleting anything, so a tree on two devices is never
	// left half purged.
	if err := checkDevices(fd, path, dev); err != nil {
		_ = unix.Close(fd)
		return err
	}
	err = purgeContents(fd, path, dev)
	_ = unix.Close(fd)
	if err != nil {
		return err
	}
	if err := unix.Unlinkat(pfd, base, unix.AT_REMOVEDIR); err != nil {
		return fmt.Errorf("platform: remove %s: %w", path, err)
	}
	return nil
}

// open checks path and returns a descriptor of its parent directory, or -1
// with a nil error when path does not exist.
func (p *DirPurge) open(path string) (int, error) {
	if !slices.Contains(purgeablePaths, path) {
		return -1, fmt.Errorf("%w: %s", ErrPurgePath, path)
	}
	rootfd, err := unix.Open(p.root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, fmt.Errorf("platform: open %s: %w", p.root, err)
	}
	defer func() { _ = unix.Close(rootfd) }()
	rel := strings.TrimPrefix(path, "/")
	pfd, err := openBeneath(rootfd, filepath.Dir(rel), unix.O_RDONLY|unix.O_DIRECTORY, 0)
	switch {
	case errors.Is(err, unix.ENOENT):
		return -1, nil
	case errors.Is(err, unix.ELOOP), errors.Is(err, unix.EXDEV):
		return -1, fmt.Errorf("%w: %s", ErrPurgeSymlink, path)
	case err != nil:
		return -1, fmt.Errorf("platform: open the directory of %s: %w", path, err)
	}
	fail := func(err error) (int, error) {
		_ = unix.Close(pfd)
		return -1, err
	}
	var st unix.Stat_t
	err = unix.Fstatat(pfd, filepath.Base(rel), &st, unix.AT_SYMLINK_NOFOLLOW)
	switch {
	case errors.Is(err, unix.ENOENT):
		return fail(nil)
	case err != nil:
		return fail(fmt.Errorf("platform: stat %s: %w", path, err))
	case st.Mode&unix.S_IFMT == unix.S_IFLNK:
		return fail(fmt.Errorf("%w: %s", ErrPurgeSymlink, path))
	case st.Mode&unix.S_IFMT != unix.S_IFDIR:
		return fail(fmt.Errorf("platform: %s is not a directory", path))
	}
	mounts, err := p.mounts()
	if err != nil {
		return fail(fmt.Errorf("platform: read the mount table: %w", err))
	}
	for _, m := range mounts {
		if m == path || strings.HasPrefix(m, path+"/") {
			return fail(fmt.Errorf("%w: %s holds the mount point %s", ErrPurgeMount, path, m))
		}
	}
	return pfd, nil
}

// purgeContents removes everything in the directory fd, which is path on
// device dev.
func purgeContents(fd int, path string, dev uint64) error {
	names, err := purgeListing(fd, path)
	if err != nil {
		return err
	}
	for _, n := range names {
		child := filepath.Join(path, n)
		var st unix.Stat_t
		if err := unix.Fstatat(fd, n, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			if errors.Is(err, unix.ENOENT) {
				continue
			}
			return fmt.Errorf("platform: stat %s: %w", child, err)
		}
		if st.Mode&unix.S_IFMT != unix.S_IFDIR {
			if err := unix.Unlinkat(fd, n, 0); err != nil && !errors.Is(err, unix.ENOENT) {
				return fmt.Errorf("platform: remove %s: %w", child, err)
			}
			continue
		}
		if err := purgeSubdir(fd, n, child, dev); err != nil {
			return err
		}
	}
	return nil
}

// purgeSubdir empties and removes the directory name in fd.
func purgeSubdir(fd int, name, path string, dev uint64) error {
	cfd, err := unix.Openat(fd, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	switch {
	case errors.Is(err, unix.ELOOP):
		return fmt.Errorf("%w: %s became a link while it was purged", ErrPurgeSymlink, path)
	case err != nil:
		return fmt.Errorf("platform: open %s: %w", path, err)
	}
	var st unix.Stat_t
	if err := unix.Fstat(cfd, &st); err != nil {
		_ = unix.Close(cfd)
		return fmt.Errorf("platform: stat %s: %w", path, err)
	}
	if st.Dev != dev {
		_ = unix.Close(cfd)
		return fmt.Errorf("%w: %s is on another device", ErrPurgeMount, path)
	}
	err = purgeContents(cfd, path, dev)
	_ = unix.Close(cfd)
	if err != nil {
		return err
	}
	if err := unix.Unlinkat(fd, name, unix.AT_REMOVEDIR); err != nil && !errors.Is(err, unix.ENOENT) {
		return fmt.Errorf("platform: remove %s: %w", path, err)
	}
	return nil
}

// purgeListing lists the directory fd through a descriptor of its own, so fd's
// offset does not move.
func purgeListing(fd int, path string) ([]string, error) {
	lfd, err := unix.Openat(fd, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("platform: open %s: %w", path, err)
	}
	list := os.NewFile(uintptr(lfd), path)
	names, err := list.Readdirnames(-1)
	_ = list.Close()
	if err != nil {
		return nil, fmt.Errorf("platform: list %s: %w", path, err)
	}
	return names, nil
}

func procMounts() ([]string, error) {
	b, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return nil, err
	}
	return parseMountInfo(b)
}

// parseMountInfo returns the mount points of a mountinfo table (proc(5)):
// the fifth field of each line, with the kernel's octal escapes decoded.
func parseMountInfo(b []byte) ([]string, error) {
	var points []string
	for line := range strings.Lines(string(b)) {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) < 5 {
			return nil, fmt.Errorf("platform: mountinfo line %q has %d fields", strings.TrimSpace(line), len(fields))
		}
		points = append(points, unescapeMountField(fields[4]))
	}
	return points, nil
}

// unescapeMountField decodes the \ooo escapes the kernel writes for a space,
// tab, newline and backslash in a mountinfo field.
func unescapeMountField(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) && isOctal(s[i+1]) && isOctal(s[i+2]) && isOctal(s[i+3]) {
			b.WriteByte((s[i+1]-'0')<<6 | (s[i+2]-'0')<<3 | (s[i+3] - '0'))
			i += 3
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func isOctal(c byte) bool { return c >= '0' && c <= '7' }
