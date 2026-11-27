package platform

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/sys/unix"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// The file manager's limits (M11 spec section 5).
const (
	MaxTextFile   = 1 << 20   // read text and write text
	MaxUpload     = 512 << 20 // one upload
	UploadReserve = 64 << 20  // the free space an upload leaves
	DirPageSize   = 2000      // entries per listing page
	TransferChunk = 32 << 10  // the unit of every copy
)

// TempPrefix starts the name of a write's temporary file.
const TempPrefix = ".darkpanel-tmp-"

var tempName = regexp.MustCompile(`^\.darkpanel-tmp-[0-9a-f]{16}$`)

// The directories whose content the file manager never changes, and the
// ones it never reads (M11 spec 1.3).
var (
	writeProtected = []string{"/proc", "/sys", "/dev", "/run", "/boot/efi", "/etc/darkpanel", "/var/lib/darkpanel"}
	readProtected  = []string{"/proc", "/sys", "/dev", "/etc/darkpanel", "/var/lib/darkpanel"}
)

// ConsoleFilesConfig configures NewConsoleFiles.
type ConsoleFilesConfig struct {
	// Root is the directory the file manager shows as /: "/" in production,
	// a temporary directory in tests.
	Root string
	// Binary is the Darkpanel binary's path, paths.Binary(): it is never
	// changed.
	Binary string
	// FreeSpace returns the bytes free in the directory dirfd; nil reads
	// statfs.
	FreeSpace func(dirfd int) (int64, error)
}

// ConsoleFiles is the core.ConsoleFiles of the agent: root's access to the
// whole host through the console's file manager (ADR 0020), refusing the
// protected paths. Every path is resolved from the root with openat2 and
// RESOLVE_NO_SYMLINKS, so a symbolic link in any component, the last
// included, is refused: a link never reaches a protected path, and the user
// opens the link's target instead.
type ConsoleFiles struct {
	root      string
	binary    string
	freeSpace func(dirfd int) (int64, error)
	writeMu   sync.Mutex // one text write at a time, from its hash check to its rename
}

var _ core.ConsoleFiles = (*ConsoleFiles)(nil)

// NewConsoleFiles returns the file manager cfg describes.
func NewConsoleFiles(cfg ConsoleFilesConfig) *ConsoleFiles {
	f := &ConsoleFiles{root: cfg.Root, binary: cfg.Binary, freeSpace: cfg.FreeSpace}
	if f.root == "" {
		f.root = "/"
	}
	if f.freeSpace == nil {
		f.freeSpace = statfsFree
	}
	return f
}

func statfsFree(dirfd int) (int64, error) {
	var st unix.Statfs_t
	if err := unix.Fstatfs(dirfd, &st); err != nil {
		return 0, err
	}
	return int64(st.Bavail) * st.Bsize, nil //nolint:gosec // G115: free bytes fit in an int64
}

func invalid(format string, args ...any) error {
	return errcode.New(errcode.InvalidInput, fmt.Sprintf(format, args...))
}

// cleanPath returns p cleaned; p must be absolute and hold no NUL byte.
func cleanPath(p string) (string, error) {
	if !strings.HasPrefix(p, "/") || strings.IndexByte(p, 0) >= 0 {
		return "", invalid("the path %q is not absolute", p)
	}
	return path.Clean(p), nil
}

func under(p, dir string) bool { return p == dir || strings.HasPrefix(p, dir+"/") }

// writeRefusal is why the file manager never changes p, "" when it may.
func (f *ConsoleFiles) writeRefusal(p string) string {
	for _, d := range writeProtected {
		if under(p, d) {
			return fmt.Sprintf("Darkpanel never changes files under %s", d)
		}
	}
	if f.binary != "" && p == f.binary {
		return "Darkpanel never changes its own binary"
	}
	return ""
}

// ancestorRefusal is why p, an ancestor of a protected path, is never
// renamed or deleted, "" when it is not one.
func (f *ConsoleFiles) ancestorRefusal(p string) string {
	for _, d := range append(slices.Clone(writeProtected), f.binary) {
		if d != "" && d != p && (p == "/" || strings.HasPrefix(d, p+"/")) {
			return fmt.Sprintf("%s holds %s, which Darkpanel never changes", p, d)
		}
	}
	return ""
}

func readRefusal(p string) string {
	for _, d := range readProtected {
		if under(p, d) {
			return fmt.Sprintf("Darkpanel never reads files under %s", d)
		}
	}
	return ""
}

func refuse(reason string) error { return invalid("%s", reason) }

// fileError turns a system error on p into the error its user reads.
func fileError(p string, err error) error {
	switch {
	case errors.Is(err, unix.ENOENT):
		return errcode.New(errcode.NotFound, p+" does not exist")
	case errors.Is(err, unix.ELOOP):
		return invalid("%s has a symbolic link in its path; open the link's target instead", p)
	case errors.Is(err, unix.ENOTDIR):
		return invalid("a part of %s is not a directory", p)
	case errors.Is(err, unix.EEXIST):
		return invalid("%s already exists", p)
	case errors.Is(err, unix.ENOTEMPTY):
		return invalid("%s is not empty", p)
	case errors.Is(err, unix.EXDEV):
		return invalid("%s is on another filesystem; a rename stays on one", p)
	case errors.Is(err, unix.EROFS):
		return invalid("%s is on a read-only filesystem", p)
	case errors.Is(err, unix.ENOSPC):
		return invalid("no space left for %s", p)
	case errors.Is(err, unix.EPERM), errors.Is(err, unix.EACCES):
		return invalid("the host refused the change to %s: %v", p, err)
	case errors.Is(err, unix.EINVAL):
		return invalid("the host refused %s: %v", p, err)
	}
	return fmt.Errorf("platform: %s: %w", p, err)
}

// rel is p below the root, "." for the root itself.
func rel(p string) string {
	if p == "/" {
		return "."
	}
	return p[1:]
}

func (f *ConsoleFiles) openRoot() (int, error) {
	fd, err := unix.Open(f.root, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, fmt.Errorf("platform: open the file manager's root %s: %w", f.root, err)
	}
	return fd, nil
}

// open opens p from the root, refusing a symbolic link in any component.
func (f *ConsoleFiles) open(p string, flags uint64) (int, error) {
	rootFd, err := f.openRoot()
	if err != nil {
		return -1, err
	}
	defer func() { _ = unix.Close(rootFd) }()
	fd, err := openBeneath(rootFd, rel(p), flags, 0)
	if err != nil {
		return -1, fileError(p, err)
	}
	return fd, nil
}

// openParent opens p's directory and returns it with p's last component;
// the root has none.
func (f *ConsoleFiles) openParent(p string) (int, string, error) {
	if p == "/" {
		return -1, "", invalid("/ has no parent directory")
	}
	fd, err := f.open(path.Dir(p), unix.O_RDONLY|unix.O_DIRECTORY)
	return fd, path.Base(p), err
}

// escapeName returns name with each byte that is not UTF-8 escaped as \xNN,
// and whether it escaped any.
func escapeName(name string) (string, bool) {
	if utf8.ValidString(name) {
		return name, false
	}
	var b strings.Builder
	for i := 0; i < len(name); {
		r, size := utf8.DecodeRuneInString(name[i:])
		if r == utf8.RuneError && size == 1 {
			fmt.Fprintf(&b, `\x%02x`, name[i])
		} else {
			b.WriteString(name[i : i+size])
		}
		i += size
	}
	return b.String(), true
}

// owners maps uids and gids to the names /etc/passwd and /etc/group give.
type owners struct{ users, groups map[uint32]string }

func (f *ConsoleFiles) readOwners() owners {
	read := func(name string) map[uint32]string {
		out := map[uint32]string{}
		data, err := ReadRegularFile(filepath.Join(f.root, "etc", name), 16<<20)
		if err != nil {
			return out
		}
		for line := range bytes.Lines(data) {
			fields := strings.Split(strings.TrimRight(string(line), "\r\n"), ":")
			if len(fields) < 3 {
				continue
			}
			if id, err := strconv.ParseUint(fields[2], 10, 32); err == nil {
				if _, seen := out[uint32(id)]; !seen {
					out[uint32(id)] = fields[0]
				}
			}
		}
		return out
	}
	return owners{users: read("passwd"), groups: read("group")}
}

func (o owners) name(names map[uint32]string, id uint32) string {
	if n, ok := names[id]; ok {
		return n
	}
	return strconv.FormatUint(uint64(id), 10)
}

func fileType(mode uint32) string {
	switch mode & unix.S_IFMT {
	case unix.S_IFREG:
		return core.FileTypeFile
	case unix.S_IFDIR:
		return core.FileTypeDir
	case unix.S_IFLNK:
		return core.FileTypeSymlink
	}
	return core.FileTypeOther
}

// info describes the entry name of dirfd, whose path is p.
func (f *ConsoleFiles) info(dirfd int, name, p string, st unix.Stat_t, o owners) core.FileInfo {
	shown, escaped := escapeName(name)
	fi := core.FileInfo{
		Name: shown, Escaped: escaped, Type: fileType(st.Mode), Size: st.Size,
		Mode: fmt.Sprintf("%04o", st.Mode&0o7777), UID: int(st.Uid), GID: int(st.Gid),
		Owner: o.name(o.users, st.Uid), Group: o.name(o.groups, st.Gid),
		Modified: time.Unix(st.Mtim.Sec, st.Mtim.Nsec).UTC(),
	}
	if escaped {
		fi.Raw = base64.StdEncoding.EncodeToString([]byte(name))
	}
	if fi.Type == core.FileTypeSymlink {
		buf := make([]byte, unix.PathMax)
		if n, err := unix.Readlinkat(dirfd, name, buf); err == nil {
			fi.Target, _ = escapeName(string(buf[:n]))
		}
	}
	fi.Protected = f.writeRefusal(p)
	if fi.Protected == "" {
		fi.Protected = f.ancestorRefusal(p)
	}
	return fi
}

type listEntry struct {
	name string
	dir  bool
}

func (e listEntry) less(o listEntry) bool {
	if e.dir != o.dir {
		return e.dir
	}
	return e.name < o.name
}

func encodeCursor(e listEntry) string {
	kind := "f"
	if e.dir {
		kind = "d"
	}
	return base64.RawURLEncoding.EncodeToString([]byte(kind + e.name))
}

func decodeCursor(cursor string) (listEntry, error) {
	b, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || len(b) < 2 || (b[0] != 'd' && b[0] != 'f') {
		return listEntry{}, invalid("the cursor %q is not one a listing gave", cursor)
	}
	return listEntry{name: string(b[1:]), dir: b[0] == 'd'}, nil
}

// List returns the page of directory p after cursor ("" for the first):
// at most DirPageSize entries, directories first, then by name.
func (f *ConsoleFiles) List(ctx context.Context, p, cursor string) (core.DirPage, error) {
	if err := ctx.Err(); err != nil {
		return core.DirPage{}, err
	}
	p, err := cleanPath(p)
	if err != nil {
		return core.DirPage{}, err
	}
	if r := readRefusal(p); r != "" {
		return core.DirPage{}, refuse(r)
	}
	var after *listEntry
	if cursor != "" {
		c, err := decodeCursor(cursor)
		if err != nil {
			return core.DirPage{}, err
		}
		after = &c
	}
	fd, err := f.open(p, unix.O_RDONLY|unix.O_DIRECTORY)
	if err != nil {
		return core.DirPage{}, err
	}
	dir := os.NewFile(uintptr(fd), p)
	defer func() { _ = dir.Close() }()
	des, err := dir.ReadDir(-1)
	if err != nil {
		return core.DirPage{}, fileError(p, err)
	}
	all := make([]listEntry, 0, len(des))
	for _, de := range des {
		all = append(all, listEntry{name: de.Name(), dir: de.Type().IsDir()})
	}
	slices.SortFunc(all, func(a, b listEntry) int {
		switch {
		case a.less(b):
			return -1
		case b.less(a):
			return 1
		}
		return 0
	})
	start := 0
	if after != nil {
		start, _ = slices.BinarySearchFunc(all, *after, func(e, target listEntry) int {
			if e.less(target) || e == target {
				return -1
			}
			return 1
		})
	}
	end := min(start+DirPageSize, len(all))
	page := core.DirPage{Path: p, Entries: []core.FileInfo{}, Protected: f.writeRefusal(p)}
	if end < len(all) {
		page.Next = encodeCursor(all[end-1])
	}
	o := f.readOwners()
	for _, e := range all[start:end] {
		var st unix.Stat_t
		if err := unix.Fstatat(fd, e.name, &st, unix.AT_SYMLINK_NOFOLLOW); errors.Is(err, unix.ENOENT) {
			continue // removed since the directory was read
		} else if err != nil {
			return core.DirPage{}, fmt.Errorf("read %s: %w", path.Join(p, e.name), err)
		}
		page.Entries = append(page.Entries, f.info(fd, e.name, path.Join(p, e.name), st, o))
	}
	return page, nil
}

// Stat describes p, never following a symbolic link.
func (f *ConsoleFiles) Stat(ctx context.Context, p string) (core.FileInfo, error) {
	if err := ctx.Err(); err != nil {
		return core.FileInfo{}, err
	}
	p, err := cleanPath(p)
	if err != nil {
		return core.FileInfo{}, err
	}
	if r := readRefusal(p); r != "" {
		return core.FileInfo{}, refuse(r)
	}
	var st unix.Stat_t
	if p == "/" {
		fd, err := f.open(p, unix.O_RDONLY|unix.O_DIRECTORY)
		if err != nil {
			return core.FileInfo{}, err
		}
		defer func() { _ = unix.Close(fd) }()
		if err := unix.Fstat(fd, &st); err != nil {
			return core.FileInfo{}, fileError(p, err)
		}
		fi := f.info(fd, ".", p, st, f.readOwners())
		fi.Name = "/"
		return fi, nil
	}
	dirfd, name, err := f.openParent(p)
	if err != nil {
		return core.FileInfo{}, err
	}
	defer func() { _ = unix.Close(dirfd) }()
	if err := unix.Fstatat(dirfd, name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return core.FileInfo{}, fileError(p, err)
	}
	return f.info(dirfd, name, p, st, f.readOwners()), nil
}

// openRegular opens the regular file p for reading.
func (f *ConsoleFiles) openRegular(p string) (*os.File, int64, error) {
	if r := readRefusal(p); r != "" {
		return nil, 0, refuse(r)
	}
	// O_NONBLOCK keeps a FIFO from blocking the open.
	fd, err := f.open(p, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOCTTY)
	if err != nil {
		return nil, 0, err
	}
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		_ = unix.Close(fd)
		return nil, 0, fileError(p, err)
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG {
		_ = unix.Close(fd)
		return nil, 0, invalid("%s is not a regular file", p)
	}
	return os.NewFile(uintptr(fd), p), st.Size, nil
}

// ReadText returns the UTF-8 text of p, at most MaxTextFile bytes, and its
// SHA-256; anything else is files.not_text.
func (f *ConsoleFiles) ReadText(ctx context.Context, p string) ([]byte, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	p, err := cleanPath(p)
	if err != nil {
		return nil, "", err
	}
	file, size, err := f.openRegular(p)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = file.Close() }()
	tooLarge := errcode.New(errcode.FilesNotText, p+" is larger than 1 MiB; download it instead")
	if size > MaxTextFile {
		return nil, "", tooLarge
	}
	data, err := io.ReadAll(io.LimitReader(file, MaxTextFile+1))
	switch {
	case err != nil:
		return nil, "", fileError(p, err)
	case len(data) > MaxTextFile:
		return nil, "", tooLarge
	case !utf8.Valid(data):
		return nil, "", errcode.New(errcode.FilesNotText, p+" is not UTF-8 text; download it instead")
	}
	sum := sha256.Sum256(data)
	return data, hex.EncodeToString(sum[:]), nil
}

// Open opens the regular file p for a download and returns its size.
func (f *ConsoleFiles) Open(ctx context.Context, p string) (io.ReadCloser, int64, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	p, err := cleanPath(p)
	if err != nil {
		return nil, 0, err
	}
	return f.openRegular(p)
}

// writeTarget opens p's directory for a write and returns the status of
// the regular file p, nil when there is none.
func (f *ConsoleFiles) writeTarget(p string) (int, string, *unix.Stat_t, error) {
	if r := f.writeRefusal(p); r != "" {
		return -1, "", nil, refuse(r)
	}
	dirfd, name, err := f.openParent(p)
	if err != nil {
		return -1, "", nil, err
	}
	var st unix.Stat_t
	err = unix.Fstatat(dirfd, name, &st, unix.AT_SYMLINK_NOFOLLOW)
	switch {
	case errors.Is(err, unix.ENOENT):
		return dirfd, name, nil, nil
	case err != nil:
		err = fileError(p, err)
	case st.Mode&unix.S_IFMT == unix.S_IFLNK:
		err = invalid("%s is a symbolic link; write to the path of its target instead", p)
	case st.Mode&unix.S_IFMT != unix.S_IFREG:
		err = invalid("%s is not a regular file", p)
	default:
		return dirfd, name, &st, nil
	}
	_ = unix.Close(dirfd)
	return -1, "", nil, err
}

// WriteText replaces p with data while p's SHA-256 is still expectSHA (""
// for a p that must not exist), and returns the new SHA-256.
func (f *ConsoleFiles) WriteText(ctx context.Context, p string, data []byte, expectSHA string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if len(data) > MaxTextFile {
		return "", errcode.New(errcode.TooLarge, "a text write is at most 1 MiB; upload the file instead")
	}
	p, err := cleanPath(p)
	if err != nil {
		return "", err
	}
	f.writeMu.Lock()
	defer f.writeMu.Unlock()
	dirfd, name, existing, err := f.writeTarget(p)
	if err != nil {
		return "", err
	}
	defer func() { _ = unix.Close(dirfd) }()
	switch {
	case existing == nil && expectSHA != "":
		return "", errcode.New(errcode.FilesConflict, p+" was removed on the host since you opened it")
	case existing != nil && expectSHA == "":
		return "", errcode.New(errcode.FilesConflict, p+" already exists")
	case existing != nil:
		current, err := hashAt(dirfd, name, p)
		if err != nil {
			return "", err
		}
		if current != expectSHA {
			return "", errcode.New(errcode.FilesConflict, p+" changed on the host since you opened it")
		}
	}
	return f.writeTemp(ctx, dirfd, name, p, existing, bytes.NewReader(data), int64(len(data)))
}

func hashAt(dirfd int, name, p string) (string, error) {
	fd, err := openBeneath(dirfd, name, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOCTTY, 0)
	if err != nil {
		return "", fileError(p, err)
	}
	file := os.NewFile(uintptr(fd), p)
	defer func() { _ = file.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, file); err != nil {
		return "", fileError(p, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Upload writes exactly size bytes of r to p, replacing a regular file
// there, and returns their SHA-256. It is refused before any byte is
// written when the free space is below size plus UploadReserve.
func (f *ConsoleFiles) Upload(ctx context.Context, p string, r io.Reader, size int64) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	switch {
	case size < 0:
		return "", invalid("an upload needs its size")
	case size > MaxUpload:
		return "", errcode.New(errcode.TooLarge, "an upload is at most 512 MiB")
	}
	p, err := cleanPath(p)
	if err != nil {
		return "", err
	}
	dirfd, name, existing, err := f.writeTarget(p)
	if err != nil {
		return "", err
	}
	defer func() { _ = unix.Close(dirfd) }()
	free, err := f.freeSpace(dirfd)
	if err != nil {
		return "", fileError(path.Dir(p), err)
	}
	if free < size+UploadReserve {
		return "", invalid("%s has %d MiB free; this upload needs %d MiB, its size plus 64 MiB",
			path.Dir(p), free>>20, (size+UploadReserve+(1<<20)-1)>>20)
	}
	return f.writeTemp(ctx, dirfd, name, p, existing, r, size)
}

// writeTemp writes size bytes of src to a temporary file in dirfd,
// recorded with core.RecordTemp before its first byte, then renames it over
// name. An existing file's mode and owner are kept; a new one is 0644. Any
// failure, a cancellation included, removes the temporary file.
func (f *ConsoleFiles) writeTemp(ctx context.Context, dirfd int, name, p string, existing *unix.Stat_t, src io.Reader, size int64) (string, error) {
	var rnd [8]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return "", fmt.Errorf("platform: temporary name: %w", err)
	}
	tmp := TempPrefix + hex.EncodeToString(rnd[:])
	tmpPath := path.Join(path.Dir(p), tmp)
	fd, err := unix.Openat(dirfd, tmp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return "", fileError(p, err)
	}
	file := os.NewFile(uintptr(fd), tmpPath)
	sum, err := func() (string, error) {
		if err := core.RecordTemp(ctx, tmpPath); err != nil {
			return "", err
		}
		h := sha256.New()
		n, err := copyChunks(ctx, io.MultiWriter(file, h), io.LimitReader(src, size))
		switch {
		case err != nil:
			return "", err
		case n != size:
			return "", invalid("the body of %s ended after %d of %d bytes", p, n, size)
		}
		mode := uint32(0o644)
		if existing != nil {
			if err := unix.Fchown(fd, int(existing.Uid), int(existing.Gid)); err != nil {
				return "", fileError(p, err)
			}
			mode = existing.Mode & 0o7777
		}
		// After Fchown, which clears set-user-ID and set-group-ID bits.
		if err := unix.Fchmod(fd, mode); err != nil {
			return "", fileError(p, err)
		}
		if err := file.Sync(); err != nil {
			return "", fileError(p, err)
		}
		return hex.EncodeToString(h.Sum(nil)), nil
	}()
	err = errors.Join(err, file.Close())
	if err == nil {
		if rerr := unix.Renameat(dirfd, tmp, dirfd, name); rerr != nil {
			err = fileError(p, rerr)
		}
	}
	if err != nil {
		_ = unix.Unlinkat(dirfd, tmp, 0)
		return "", err
	}
	if err := unix.Fsync(dirfd); err != nil {
		return "", fmt.Errorf("platform: sync the directory of %s: %w", p, err)
	}
	return sum, nil
}

// copyChunks copies src to dst TransferChunk bytes at a time, stopping
// when ctx ends.
func copyChunks(ctx context.Context, dst io.Writer, src io.Reader) (int64, error) {
	buf := make([]byte, TransferChunk)
	var n int64
	for {
		if err := ctx.Err(); err != nil {
			return n, err
		}
		m, rerr := src.Read(buf)
		if m > 0 {
			if _, err := dst.Write(buf[:m]); err != nil {
				return n, err
			}
			n += int64(m)
		}
		if errors.Is(rerr, io.EOF) {
			return n, nil
		}
		if rerr != nil {
			return n, rerr
		}
	}
}

// Mkdir creates the directory p, mode 0755.
func (f *ConsoleFiles) Mkdir(ctx context.Context, p string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p, err := cleanPath(p)
	if err != nil {
		return err
	}
	if r := f.writeRefusal(p); r != "" {
		return refuse(r)
	}
	dirfd, name, err := f.openParent(p)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(dirfd) }()
	if err := unix.Mkdirat(dirfd, name, 0o755); err != nil {
		return fileError(p, err)
	}
	return nil
}

// Rename moves from to to on the same filesystem; to must not exist.
func (f *ConsoleFiles) Rename(ctx context.Context, from, to string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	from, err := cleanPath(from)
	if err != nil {
		return err
	}
	if to, err = cleanPath(to); err != nil {
		return err
	}
	for _, p := range []string{from, to} {
		if r := f.writeRefusal(p); r != "" {
			return refuse(r)
		}
	}
	if r := f.ancestorRefusal(from); r != "" {
		return refuse(r)
	}
	fromFd, fromName, err := f.openParent(from)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(fromFd) }()
	toFd, toName, err := f.openParent(to)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(toFd) }()
	if err := unix.Renameat2(fromFd, fromName, toFd, toName, unix.RENAME_NOREPLACE); err != nil {
		if errors.Is(err, unix.EEXIST) {
			return fileError(to, err)
		}
		return fileError(from, err)
	}
	return nil
}

// Delete removes p; a directory, with everything in it, only when confirm
// is p. It never crosses into another filesystem mounted below p.
func (f *ConsoleFiles) Delete(ctx context.Context, p, confirm string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p, err := cleanPath(p)
	if err != nil {
		return err
	}
	if r := f.writeRefusal(p); r != "" {
		return refuse(r)
	}
	if r := f.ancestorRefusal(p); r != "" {
		return refuse(r)
	}
	dirfd, name, err := f.openParent(p)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(dirfd) }()
	var st unix.Stat_t
	if err := unix.Fstatat(dirfd, name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return fileError(p, err)
	}
	if st.Mode&unix.S_IFMT != unix.S_IFDIR {
		if err := unix.Unlinkat(dirfd, name, 0); err != nil {
			return fileError(p, err)
		}
		return nil
	}
	if confirm != p {
		return invalid("%s is a directory: confirm with its path to delete it and everything in it", p)
	}
	return removeAllAt(ctx, dirfd, name, p, st.Dev)
}

// removeAllAt removes the directory name of dirfd, whose path is p, and
// everything in it, never following a symbolic link and refusing a mount
// point, on dev or another device.
func removeAllAt(ctx context.Context, dirfd int, name, p string, dev uint64) error {
	// RESOLVE_NO_XDEV also stops at a bind mount of the same filesystem,
	// whose device the check below cannot tell apart.
	fd, err := unix.Openat2(dirfd, name, &unix.OpenHow{
		Flags:   unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC,
		Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_XDEV,
	})
	if errors.Is(err, unix.EXDEV) {
		return invalid("%s is a mount point; unmount it first", p)
	}
	if err != nil {
		return fileError(p, err)
	}
	dir := os.NewFile(uintptr(fd), p)
	err = func() error {
		var st unix.Stat_t
		if err := unix.Fstat(fd, &st); err != nil {
			return fileError(p, err)
		}
		if st.Dev != dev {
			return invalid("%s is another filesystem's mount point; unmount it first", p)
		}
		entries, err := dir.ReadDir(-1)
		if err != nil {
			return fileError(p, err)
		}
		for _, e := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			child := path.Join(p, e.Name())
			if e.Type().IsDir() {
				if err := removeAllAt(ctx, fd, e.Name(), child, dev); err != nil {
					return err
				}
				continue
			}
			if err := unix.Unlinkat(fd, e.Name(), 0); err != nil {
				return fileError(child, err)
			}
		}
		return nil
	}()
	if err = errors.Join(err, dir.Close()); err != nil {
		return err
	}
	if err := unix.Unlinkat(dirfd, name, unix.AT_REMOVEDIR); err != nil {
		return fileError(p, err)
	}
	return nil
}

// openChange opens p, which is not a symbolic link, to change its mode or
// owner.
func (f *ConsoleFiles) openChange(ctx context.Context, p string) (int, string, error) {
	if err := ctx.Err(); err != nil {
		return -1, "", err
	}
	p, err := cleanPath(p)
	if err != nil {
		return -1, "", err
	}
	if r := f.writeRefusal(p); r != "" {
		return -1, "", refuse(r)
	}
	// O_NONBLOCK keeps a FIFO from blocking the open.
	fd, err := f.open(p, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOCTTY)
	return fd, p, err
}

// Chmod sets p's permission bits, set-id and sticky bits included.
func (f *ConsoleFiles) Chmod(ctx context.Context, p string, mode fs.FileMode) error {
	if mode&^0o7777 != 0 {
		return invalid("the mode %o has bits beyond 7777", mode)
	}
	fd, p, err := f.openChange(ctx, p)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(fd) }()
	if err := unix.Fchmod(fd, uint32(mode)); err != nil {
		return fileError(p, err)
	}
	return nil
}

// Chown sets p's owner and group; -1 keeps either.
func (f *ConsoleFiles) Chown(ctx context.Context, p string, uid, gid int) error {
	for _, id := range []int{uid, gid} {
		if id < -1 || id > 1<<31-1 {
			return invalid("the id %d is not a user or group id", id)
		}
	}
	fd, p, err := f.openChange(ctx, p)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(fd) }()
	if err := unix.Fchown(fd, uid, gid); err != nil {
		return fileError(p, err)
	}
	return nil
}

// RemoveTemp removes p, a temporary file a write named with RecordTemp; a
// p that is gone is no error, and any other name is refused.
func (f *ConsoleFiles) RemoveTemp(ctx context.Context, p string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p, err := cleanPath(p)
	if err != nil {
		return err
	}
	if !tempName.MatchString(path.Base(p)) {
		return invalid("%s is not a temporary file of the file manager", p)
	}
	dirfd, name, err := f.openParent(p)
	if err != nil {
		if errcode.From(err).Code == errcode.NotFound {
			return nil
		}
		return err
	}
	defer func() { _ = unix.Close(dirfd) }()
	var st unix.Stat_t
	err = unix.Fstatat(dirfd, name, &st, unix.AT_SYMLINK_NOFOLLOW)
	switch {
	case errors.Is(err, unix.ENOENT):
		return nil
	case err != nil:
		return fileError(p, err)
	case st.Mode&unix.S_IFMT != unix.S_IFREG:
		return invalid("%s is not a regular file", p)
	}
	if err := unix.Unlinkat(dirfd, name, 0); err != nil && !errors.Is(err, unix.ENOENT) {
		return fileError(p, err)
	}
	return nil
}
