package platform

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

// homeRig is a temporary root holding a passwd file and the home of user
// u, owned by uid; as root the test gives u its own uid.
type homeRig struct {
	h        *HomeFiles
	home     string
	uid, gid int
}

func newHomeRig(t *testing.T) homeRig {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	uid, gid := os.Getuid(), os.Getgid()
	if uid == 0 {
		uid, gid = 4242, 4243
	}
	home := filepath.Join(root, "home", "u")
	if err := os.MkdirAll(home, 0o755); err != nil { //nolint:gosec // G301: a home as useradd makes it
		t.Fatal(err)
	}
	passwd := filepath.Join(root, "passwd")
	entry := fmt.Sprintf("root:x:0:0:root:/root:/bin/bash\nu:x:%d:%d::%s:/bin/bash\n", uid, gid, home)
	if err := os.WriteFile(passwd, []byte(entry), 0o644); err != nil { //nolint:gosec // G306: a test passwd file
		t.Fatal(err)
	}
	return homeRig{h: NewHomeFiles(passwd), home: home, uid: uid, gid: gid}
}

func (r homeRig) path(parts ...string) string {
	return filepath.Join(append([]string{r.home}, parts...)...)
}

func TestHomeFilesRefusals(t *testing.T) {
	ctx := context.Background()
	keys := []byte("ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl a\n")
	cases := map[string]struct {
		setup     func(t *testing.T, r homeRig)
		want      string
		rootOnly  bool
		readsOnly bool
	}{
		"a symlinked .ssh": {func(t *testing.T, r homeRig) {
			other := filepath.Join(filepath.Dir(r.home), "elsewhere")
			mustDo(t, os.Mkdir(other, 0o700), os.Symlink(other, r.path(".ssh")))
		}, "symbolic link", false, false},
		"a symlinked authorized_keys": {func(t *testing.T, r homeRig) {
			target := filepath.Join(filepath.Dir(r.home), "keys")
			mustDo(t, os.Mkdir(r.path(".ssh"), 0o700), os.WriteFile(target, keys, 0o600), os.Symlink(target, r.path(".ssh", "authorized_keys")))
		}, "symbolic link", false, false},
		"an authorized_keys that is a FIFO": {func(t *testing.T, r homeRig) {
			mustDo(t, os.Mkdir(r.path(".ssh"), 0o700), syscall.Mkfifo(r.path(".ssh", "authorized_keys"), 0o600))
		}, "not a regular file", false, false},
		"an authorized_keys owned by another user": {func(t *testing.T, r homeRig) {
			mustDo(t, os.Mkdir(r.path(".ssh"), 0o700), os.WriteFile(r.path(".ssh", "authorized_keys"), keys, 0o600),
				os.Chown(r.path(".ssh", "authorized_keys"), 5151, 5151))
		}, "owned by uid 5151", true, false},
		"an authorized_keys of 1 MiB + 1 byte": {func(t *testing.T, r homeRig) {
			mustDo(t, os.Mkdir(r.path(".ssh"), 0o700), os.WriteFile(r.path(".ssh", "authorized_keys"), bytes.Repeat([]byte("a"), MaxAuthorizedKeys+1), 0o600))
		}, "larger than 1 MiB", false, false},
		"a group-writable home": {func(t *testing.T, r homeRig) {
			mustDo(t, os.Chmod(r.home, 0o775)) //nolint:gosec // G302: the group-writable home under test
		}, "writable by group or others", false, false},
		"a group-writable .ssh": {func(t *testing.T, r homeRig) {
			mustDo(t, os.Mkdir(r.path(".ssh"), 0o700), os.Chmod(r.path(".ssh"), 0o770)) //nolint:gosec // G302: the group-writable .ssh under test
		}, "writable by group or others", false, false},
		"a home owned by another user": {func(t *testing.T, r homeRig) {
			mustDo(t, os.Chown(r.home, 5151, 5151))
		}, "owned by uid 5151", true, false},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if c.rootOnly && os.Getuid() != 0 {
				t.Skip("needs root to give a file another owner")
			}
			r := newHomeRig(t)
			c.setup(t, r)
			if _, err := r.h.ReadAuthorizedKeys(ctx, "u"); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("read: %v, want an error naming %q", err, c.want)
			}
			if c.readsOnly {
				return
			}
			err := r.h.WriteAuthorizedKeys(ctx, "u", keys)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("write: %v, want an error naming %q", err, c.want)
			}
		})
	}
	r := newHomeRig(t)
	if err := r.h.WriteAuthorizedKeys(ctx, "u", bytes.Repeat([]byte("a"), MaxAuthorizedKeys+1)); err == nil {
		t.Error("a write of 1 MiB + 1 byte passed")
	}
	if _, err := r.h.ReadAuthorizedKeys(ctx, "nobody"); err == nil {
		t.Error("a read for an unknown user passed")
	}
}

func mustDo(t *testing.T, errs ...error) {
	t.Helper()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestHomeFilesCreates(t *testing.T) {
	ctx := context.Background()
	r := newHomeRig(t)
	if os.Getuid() == 0 {
		mustDo(t, os.Chown(r.home, r.uid, r.gid))
	}
	if got, err := r.h.ReadAuthorizedKeys(ctx, "u"); err != nil || got != nil {
		t.Fatalf("read without .ssh: %q, %v", got, err)
	}
	data := []byte("ssh-ed25519 AAAA one\n")
	if err := r.h.WriteAuthorizedKeys(ctx, "u", data); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		path string
		mode os.FileMode
	}{{r.path(".ssh"), os.ModeDir | 0o700}, {r.path(".ssh", "authorized_keys"), 0o600}} {
		var st unix.Stat_t
		if err := unix.Lstat(c.path, &st); err != nil {
			t.Fatal(err)
		}
		fi, _ := os.Lstat(c.path)
		if fi.Mode() != c.mode || int(st.Uid) != r.uid || int(st.Gid) != r.gid {
			t.Errorf("%s: mode %v owner %d:%d, want %v %d:%d", c.path, fi.Mode(), st.Uid, st.Gid, c.mode, r.uid, r.gid)
		}
	}
	if got, err := r.h.ReadAuthorizedKeys(ctx, "u"); err != nil || !bytes.Equal(got, data) {
		t.Errorf("read back %q, %v", got, err)
	}
	// A second write replaces the file, leaving no temporary file behind.
	if err := r.h.WriteAuthorizedKeys(ctx, "u", []byte("ssh-ed25519 AAAA two\n")); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(r.path(".ssh"))
	if len(entries) != 1 {
		t.Errorf(".ssh holds %v, want authorized_keys alone", entries)
	}
}
