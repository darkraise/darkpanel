package platform

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/darkraise/darkpanel/internal/core"
)

// serviceDirMode is the mode of a service directory and its subdirectories.
const serviceDirMode = 0o700

// serviceDefaultName is a service directory's file-name pattern when the
// declaration sets none; it admits a certificate named after an IPv6 address.
var serviceDefaultName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

// EnsureServiceDir creates dir, which module declares as kind service-dir, and
// its declared subdirectories with mode 0700, owned by the declaration's User
// looked up now; dir is recorded before it is created. An existing dir must be
// recorded as module's, and it and its subdirectories owned by User; one left
// owned by root by an interrupted creation is handed to User. Other entries
// are the service's and are not checked.
func (f *Files) EnsureServiceDir(ctx context.Context, module, dir string) error {
	op, err := f.declaredKind(module, dir, core.OwnedServiceDir)
	if err != nil {
		return err
	}
	uid, gid, err := f.lookupUser(op.User)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	defer f.lock(dir)()
	parentDir, name := filepath.Split(dir)
	pfd, err := f.openDir(parentDir)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(pfd) }()
	fd, err := openBeneath(pfd, name, unix.O_RDONLY|unix.O_DIRECTORY, 0)
	switch {
	case errors.Is(err, unix.ENOENT):
		// Recorded before the directory exists, so a crash after the mkdir
		// still leaves it for uninstall to find.
		entry := core.OwnedEntry{Path: dir, Module: module, Kind: core.OwnedServiceDir, CreatedAt: f.cfg.Clock.Now().UTC()}
		if err := f.cfg.Ownership.Record(ctx, entry); err != nil {
			return err
		}
		if fd, err = makeServiceDir(pfd, name, dir); err != nil {
			return err
		}
	case err != nil:
		return fmt.Errorf("platform: open directory %s: %w", dir, err)
	default:
		owned, err := f.cfg.Ownership.Owns(ctx, module, dir)
		if err == nil && !owned {
			err = fmt.Errorf("platform: directory %s exists and is not Darkpanel's", dir)
		}
		if err != nil {
			_ = unix.Close(fd)
			return err
		}
	}
	defer func() { _ = unix.Close(fd) }()
	if err := f.handOver(fd, dir, uid, gid); err != nil {
		return err
	}
	for _, sub := range op.Subdirs {
		path := filepath.Join(dir, sub)
		sfd, err := openBeneath(fd, sub, unix.O_RDONLY|unix.O_DIRECTORY, 0)
		if errors.Is(err, unix.ENOENT) {
			sfd, err = makeServiceDir(fd, sub, path)
		} else if err != nil {
			err = fmt.Errorf("platform: open directory %s: %w", path, err)
		}
		if err != nil {
			return err
		}
		err = f.handOver(sfd, path, uid, gid)
		_ = unix.Close(sfd)
		if err != nil {
			return err
		}
	}
	if err := unix.Fsync(fd); err != nil {
		return fmt.Errorf("platform: sync directory %s: %w", dir, err)
	}
	if err := unix.Fsync(pfd); err != nil {
		return fmt.Errorf("platform: sync directory of %s: %w", dir, err)
	}
	return nil
}

// RenameIn renames the regular file from to to, both names in dir, which
// module declares as kind service-dir, or both in the same declared
// subdirectory of it ("certs/a.crt"). The file must be owned by the
// declaration's User; a symlink is refused.
func (f *Files) RenameIn(ctx context.Context, module, dir, from, to string) error {
	op, err := f.declaredKind(module, dir, core.OwnedServiceDir)
	if err != nil {
		return err
	}
	fromSub, fromName, err := serviceEntry(op, from)
	if err != nil {
		return err
	}
	toSub, toName, err := serviceEntry(op, to)
	if err != nil {
		return err
	}
	if fromSub != toSub {
		return fmt.Errorf("platform: rename %s to %s in %s: not in the same directory", from, to, dir)
	}
	uid, _, err := f.lookupUser(op.User)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	defer f.lock(dir)()
	fd, err := f.openServiceLoc(dir, fromSub, uid)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(fd) }()
	path := filepath.Join(dir, from)
	if err := ownedRegular(fd, fromName, path, uid); err != nil {
		return err
	}
	if err := unix.Renameat(fd, fromName, fd, toName); err != nil {
		return fmt.Errorf("platform: rename %s to %s: %w", path, filepath.Join(dir, to), err)
	}
	if err := unix.Fsync(fd); err != nil {
		return fmt.Errorf("platform: sync directory of %s: %w", path, err)
	}
	return nil
}

// RemoveIn removes the file name from dir, which module declares as kind
// service-dir, or from a declared subdirectory of it ("certs/a.crt"). A
// regular file must be owned by the declaration's User; a symlink is unlinked
// without being followed; a missing directory or name is not an error.
func (f *Files) RemoveIn(ctx context.Context, module, dir, name string) error {
	op, err := f.declaredKind(module, dir, core.OwnedServiceDir)
	if err != nil {
		return err
	}
	sub, base, err := serviceEntry(op, name)
	if err != nil {
		return err
	}
	uid, _, err := f.lookupUser(op.User)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	defer f.lock(dir)()
	fd, err := f.openServiceLoc(dir, sub, uid)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(fd) }()
	path := filepath.Join(dir, name)
	var st unix.Stat_t
	err = unix.Fstatat(fd, base, &st, unix.AT_SYMLINK_NOFOLLOW)
	switch {
	case errors.Is(err, unix.ENOENT):
		return nil
	case err != nil:
		return fmt.Errorf("platform: stat %s: %w", path, err)
	case st.Mode&unix.S_IFMT == unix.S_IFLNK:
	case st.Mode&unix.S_IFMT != unix.S_IFREG:
		return fmt.Errorf("platform: %s is not a regular file", path)
	case int(st.Uid) != uid:
		return fmt.Errorf("platform: %s is owned by uid %d, want %d", path, st.Uid, uid)
	}
	if err := unix.Unlinkat(fd, base, 0); err != nil && !errors.Is(err, unix.ENOENT) {
		return fmt.Errorf("platform: remove %s: %w", path, err)
	}
	if err := unix.Fsync(fd); err != nil {
		return fmt.Errorf("platform: sync directory of %s: %w", path, err)
	}
	return nil
}

// RemoveServiceDir removes dir, which module declares as kind service-dir:
// every non-directory entry of each declared subdirectory and of dir (a
// symlink is unlinked, never followed), then the subdirectories and dir, and
// forgets the record. A directory it does not declare fails the call, naming
// it, before anything is removed; a missing dir is not an error. A dir that
// exists but is not recorded as module's is refused.
func (f *Files) RemoveServiceDir(ctx context.Context, module, dir string) error {
	op, err := f.declaredKind(module, dir, core.OwnedServiceDir)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	defer f.lock(dir)()
	parentDir, name := filepath.Split(dir)
	pfd, err := f.openDir(parentDir)
	if errors.Is(err, fs.ErrNotExist) {
		return f.cfg.Ownership.Forget(ctx, module, dir, "")
	}
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(pfd) }()
	fd, err := openBeneath(pfd, name, unix.O_RDONLY|unix.O_DIRECTORY, 0)
	switch {
	case errors.Is(err, unix.ENOENT):
		return f.cfg.Ownership.Forget(ctx, module, dir, "")
	case err != nil:
		return fmt.Errorf("platform: open directory %s: %w", dir, err)
	}
	defer func() { _ = unix.Close(fd) }()
	owned, err := f.cfg.Ownership.Owns(ctx, module, dir)
	if err != nil {
		return err
	}
	if !owned {
		return fmt.Errorf("platform: directory %s exists and is not Darkpanel's", dir)
	}
	files, subs, err := serviceListing(fd, dir)
	if err != nil {
		return err
	}
	var undeclared []string
	subFDs := map[string]int{}
	subFiles := map[string][]string{}
	defer func() {
		for _, sfd := range subFDs {
			_ = unix.Close(sfd)
		}
	}()
	for _, sub := range subs {
		if !slices.Contains(op.Subdirs, sub) {
			undeclared = append(undeclared, sub)
			continue
		}
		sfd, err := openBeneath(fd, sub, unix.O_RDONLY|unix.O_DIRECTORY, 0)
		if err != nil {
			return fmt.Errorf("platform: open directory %s: %w", filepath.Join(dir, sub), err)
		}
		subFDs[sub] = sfd
		inner, innerDirs, err := serviceListing(sfd, filepath.Join(dir, sub))
		if err != nil {
			return err
		}
		subFiles[sub] = inner
		for _, d := range innerDirs {
			undeclared = append(undeclared, sub+"/"+d)
		}
	}
	if len(undeclared) > 0 {
		return fmt.Errorf("platform: %s holds directories Darkpanel does not declare, so nothing was removed: %s",
			dir, strings.Join(undeclared, ", "))
	}
	for _, sub := range subs {
		if err := unlinkAll(subFDs[sub], filepath.Join(dir, sub), subFiles[sub]); err != nil {
			return err
		}
		if err := unix.Unlinkat(fd, sub, unix.AT_REMOVEDIR); err != nil {
			return fmt.Errorf("platform: remove directory %s: %w", filepath.Join(dir, sub), err)
		}
	}
	if err := unlinkAll(fd, dir, files); err != nil {
		return err
	}
	if err := unix.Unlinkat(pfd, name, unix.AT_REMOVEDIR); err != nil {
		return fmt.Errorf("platform: remove directory %s: %w", dir, err)
	}
	if err := unix.Fsync(pfd); err != nil {
		return fmt.Errorf("platform: sync directory of %s: %w", dir, err)
	}
	return f.cfg.Ownership.Forget(ctx, module, dir, "")
}

// lookupUser returns the ids of the system user name.
func (f *Files) lookupUser(name string) (uid, gid int, err error) {
	if f.cfg.LookupUser != nil {
		if uid, gid, err = f.cfg.LookupUser(name); err != nil {
			return 0, 0, fmt.Errorf("platform: look up user %s: %w", name, err)
		}
		return uid, gid, nil
	}
	u, err := user.Lookup(name)
	if err != nil {
		return 0, 0, fmt.Errorf("platform: look up user %s: %w", name, err)
	}
	if uid, err = strconv.Atoi(u.Uid); err == nil {
		gid, err = strconv.Atoi(u.Gid)
	}
	if err != nil {
		return 0, 0, fmt.Errorf("platform: user %s: %w", name, err)
	}
	return uid, gid, nil
}

// makeServiceDir makes name in the directory pfd with mode 0700 and opens it.
func makeServiceDir(pfd int, name, path string) (int, error) {
	if err := unix.Mkdirat(pfd, name, serviceDirMode); err != nil {
		return -1, fmt.Errorf("platform: create directory %s: %w", path, err)
	}
	fd, err := openBeneath(pfd, name, unix.O_RDONLY|unix.O_DIRECTORY, 0)
	if err != nil {
		return -1, fmt.Errorf("platform: open directory %s: %w", path, err)
	}
	// Mkdirat applies the umask; the mode must not depend on it.
	if err := unix.Fchmod(fd, serviceDirMode); err != nil {
		_ = unix.Close(fd)
		return -1, fmt.Errorf("platform: chmod directory %s: %w", path, err)
	}
	return fd, nil
}

// handOver accepts the directory fd when uid owns it, gives it to uid:gid
// with mode 0700 when root, its creator, still owns it, and refuses it
// otherwise. Only root can create a root-owned entry, so handing one over
// never takes a directory from anyone else.
func (f *Files) handOver(fd int, path string, uid, gid int) error {
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return fmt.Errorf("platform: stat directory %s: %w", path, err)
	}
	switch int(st.Uid) {
	case uid:
		return nil
	case f.parentUID:
		if err := unix.Fchown(fd, uid, gid); err != nil {
			return fmt.Errorf("platform: chown directory %s: %w", path, err)
		}
		// After Fchown, which clears set-group-ID bits.
		if err := unix.Fchmod(fd, serviceDirMode); err != nil {
			return fmt.Errorf("platform: chmod directory %s: %w", path, err)
		}
		return nil
	default:
		return fmt.Errorf("platform: directory %s is owned by uid %d, want %d", path, st.Uid, uid)
	}
}

// serviceEntry splits rel, "name" or "<subdir>/name", into the declared
// subdirectory ("" for dir itself) and a name matching the declaration's
// Pattern, or the default pattern without one.
func serviceEntry(op core.OwnedPath, rel string) (sub, name string, err error) {
	name = rel
	if i := strings.IndexByte(rel, '/'); i >= 0 {
		sub, name = rel[:i], rel[i+1:]
		if !slices.Contains(op.Subdirs, sub) {
			return "", "", fmt.Errorf("platform: %q: %s declares no subdirectory %q", rel, op.Path, sub)
		}
	}
	pattern := serviceDefaultName
	if op.Pattern != "" {
		if pattern, err = regexp.Compile(op.Pattern); err != nil {
			return "", "", fmt.Errorf("platform: service directory %s: pattern: %w", op.Path, err)
		}
	}
	if name == "." || name == ".." || strings.Contains(name, "/") || !pattern.MatchString(name) {
		return "", "", fmt.Errorf("platform: %q is not a valid file name in %s", rel, op.Path)
	}
	return sub, name, nil
}

// openServiceLoc opens dir, through its parent opened as openDir opens one,
// or its subdirectory sub, checking that each is owned by uid. A missing dir
// or sub fails with an error wrapping fs.ErrNotExist.
func (f *Files) openServiceLoc(dir, sub string, uid int) (int, error) {
	parentDir, name := filepath.Split(dir)
	pfd, err := f.openDir(parentDir)
	if err != nil {
		return -1, err
	}
	defer func() { _ = unix.Close(pfd) }()
	fd, err := openOwnedDir(pfd, name, dir, uid)
	if err != nil || sub == "" {
		return fd, err
	}
	defer func() { _ = unix.Close(fd) }()
	return openOwnedDir(fd, sub, filepath.Join(dir, sub), uid)
}

// openOwnedDir opens the directory name below dirfd and checks that uid owns
// it.
func openOwnedDir(dirfd int, name, path string, uid int) (int, error) {
	fd, err := openBeneath(dirfd, name, unix.O_RDONLY|unix.O_DIRECTORY, 0)
	if err != nil {
		return -1, fmt.Errorf("platform: open directory %s: %w", path, err)
	}
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		_ = unix.Close(fd)
		return -1, fmt.Errorf("platform: stat directory %s: %w", path, err)
	}
	if int(st.Uid) != uid {
		_ = unix.Close(fd)
		return -1, fmt.Errorf("platform: directory %s is owned by uid %d, want %d", path, st.Uid, uid)
	}
	return fd, nil
}

// ownedRegular checks that name in dirfd, opened without following a
// symlink, is a regular file owned by uid.
func ownedRegular(dirfd int, name, path string, uid int) error {
	fd, err := openBeneath(dirfd, name, unix.O_PATH, 0)
	if err != nil {
		return fmt.Errorf("platform: open %s: %w", path, err)
	}
	defer func() { _ = unix.Close(fd) }()
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return fmt.Errorf("platform: stat %s: %w", path, err)
	}
	switch {
	case st.Mode&unix.S_IFMT != unix.S_IFREG:
		return fmt.Errorf("platform: %s is not a regular file", path)
	case int(st.Uid) != uid:
		return fmt.Errorf("platform: %s is owned by uid %d, want %d", path, st.Uid, uid)
	}
	return nil
}

// serviceListing returns the names in the directory fd, split into
// non-directories and directories; a symlink is a non-directory.
func serviceListing(fd int, path string) (files, dirs []string, err error) {
	// A descriptor of its own, so the listing does not move fd's offset.
	lfd, err := openBeneath(fd, ".", unix.O_RDONLY|unix.O_DIRECTORY, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("platform: open directory %s: %w", path, err)
	}
	list := os.NewFile(uintptr(lfd), path)
	names, err := list.Readdirnames(-1)
	_ = list.Close()
	if err != nil {
		return nil, nil, fmt.Errorf("platform: list directory %s: %w", path, err)
	}
	slices.Sort(names)
	for _, n := range names {
		var st unix.Stat_t
		if err := unix.Fstatat(fd, n, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return nil, nil, fmt.Errorf("platform: stat %s: %w", filepath.Join(path, n), err)
		}
		if st.Mode&unix.S_IFMT == unix.S_IFDIR {
			dirs = append(dirs, n)
		} else {
			files = append(files, n)
		}
	}
	return files, dirs, nil
}

// unlinkAll unlinks names, none a directory, from the directory fd.
func unlinkAll(fd int, path string, names []string) error {
	for _, n := range names {
		if err := unix.Unlinkat(fd, n, 0); err != nil && !errors.Is(err, unix.ENOENT) {
			return fmt.Errorf("platform: remove %s: %w", filepath.Join(path, n), err)
		}
	}
	return nil
}
