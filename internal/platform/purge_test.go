package platform

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestPurgeDir(t *testing.T) {
	root := t.TempDir()
	mk := func(p string) string { full := filepath.Join(root, p); _ = os.MkdirAll(full, 0o750); return full }
	outside := mk("outside")
	_ = os.WriteFile(filepath.Join(outside, "keep"), []byte("x"), 0o600)

	cases := []struct {
		name    string
		setup   func() string
		mounts  []string
		wantErr error
		keep    string
	}{
		{"plain tree is removed", func() string {
			d := mk("var/lib/docker")
			_ = os.WriteFile(filepath.Join(d, "f"), nil, 0o600)
			return "/var/lib/docker"
		}, nil, nil, ""},
		{"symlink at the path", func() string {
			_ = os.MkdirAll(filepath.Join(root, "var/lib"), 0o750)
			_ = os.Symlink(outside, filepath.Join(root, "var/lib/containerd"))
			return "/var/lib/containerd"
		}, nil, ErrPurgeSymlink, "outside/keep"},
		{"symlink inside is unlinked, not followed", func() string {
			d := mk("var/lib/docker")
			_ = os.Symlink(outside, filepath.Join(d, "link"))
			return "/var/lib/docker"
		}, nil, nil, "outside/keep"},
		{"path is a mount point", func() string { mk("var/lib/docker"); return "/var/lib/docker" }, []string{"/var/lib/docker"}, ErrPurgeMount, ""},
		{"mount inside the path", func() string { mk("var/lib/docker/volumes/v1"); return "/var/lib/docker" }, []string{"/var/lib/docker/volumes/v1"}, ErrPurgeMount, ""},
		{"other path", func() string { mk("etc"); return "/etc" }, nil, ErrPurgePath, ""},
		// Additions to the plan's cases.
		{"deep tree with links is removed", func() string {
			d := mk("var/lib/docker/overlay2/a/diff/usr/bin")
			_ = os.WriteFile(filepath.Join(d, "sh"), []byte("x"), 0o600)
			_ = os.Symlink(filepath.Join(outside, "keep"), filepath.Join(d, "keep-link"))
			_ = os.Symlink(outside, filepath.Join(root, "var/lib/docker/overlay2/dirlink"))
			return "/var/lib/docker"
		}, nil, nil, "outside/keep"},
		{"symlink above the path", func() string {
			_ = os.MkdirAll(filepath.Join(root, "var"), 0o750)
			_ = os.MkdirAll(filepath.Join(outside, "lib/docker"), 0o750)
			_ = os.Symlink(outside, filepath.Join(root, "var/lib"))
			return "/var/lib/docker"
		}, nil, ErrPurgeSymlink, "outside/lib/docker"},
		{"absent path is nothing to do", func() string { mk("var/lib"); return "/var/lib/containerd" }, nil, nil, ""},
		{"a sibling mount is no refusal", func() string { mk("var/lib/docker"); return "/var/lib/docker" }, []string{"/var/lib/docker-other", "/var/lib"}, nil, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_ = os.RemoveAll(filepath.Join(root, "var"))
			path := c.setup()
			p := newPurge(root, func() ([]string, error) { return c.mounts, nil })
			if err := p.CheckPurgeable(path); !errors.Is(err, c.wantErr) {
				t.Fatalf("CheckPurgeable = %v, want %v", err, c.wantErr)
			}
			err := p.PurgeDir(path)
			if !errors.Is(err, c.wantErr) {
				t.Fatalf("PurgeDir = %v, want %v", err, c.wantErr)
			}
			if c.wantErr == nil {
				if _, err := os.Lstat(filepath.Join(root, path)); !os.IsNotExist(err) {
					t.Fatal("path still exists")
				}
			}
			if c.keep != "" {
				if _, err := os.Stat(filepath.Join(root, c.keep)); err != nil {
					t.Fatalf("%s was deleted", c.keep)
				}
			}
		})
	}
}

func TestPurgeMountTableError(t *testing.T) {
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "var/lib/docker"), 0o750)
	cause := errors.New("no mount table")
	p := newPurge(root, func() ([]string, error) { return nil, cause })
	if err := p.PurgeDir("/var/lib/docker"); !errors.Is(err, cause) {
		t.Fatalf("PurgeDir = %v, want the mount table's error", err)
	}
	if _, err := os.Stat(filepath.Join(root, "var/lib/docker")); err != nil {
		t.Fatal("the path was removed without a mount table")
	}
}

func TestParseMountInfo(t *testing.T) {
	info := "22 1 8:1 / / rw,relatime shared:1 - ext4 /dev/sda1 rw\n" +
		"36 22 0:32 / /var/lib/docker/overlay2/x\\040y/merged rw - overlay overlay rw\n" +
		"37 22 0:33 / /mnt/tab\\011and\\134slash rw - tmpfs tmpfs rw\n"
	got, err := parseMountInfo([]byte(info))
	want := []string{"/", "/var/lib/docker/overlay2/x y/merged", "/mnt/tab\tand\\slash"}
	if err != nil || len(got) != len(want) {
		t.Fatalf("parseMountInfo = %q, %v; want %q", got, err, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("mount %d = %q, want %q", i, got[i], want[i])
		}
	}
	if _, err := parseMountInfo([]byte("22 1 8:1 /\n")); err == nil {
		t.Error("a short mountinfo line was accepted")
	}
}

// A btrfs subvolume cannot be made in a test: checkDevices is given a device
// the tree is not on, which every directory beneath then differs from.
func TestCheckDevicesRefusesADirectoryOnAnotherDevice(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "overlay2", "l1", "diff"), 0o750); err != nil {
		t.Fatal(err)
	}
	fd, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unix.Close(fd) }()
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		t.Fatal(err)
	}
	if err := checkDevices(fd, root, st.Dev); err != nil {
		t.Fatalf("one device: %v", err)
	}
	if err := checkDevices(fd, root, st.Dev+1); !errors.Is(err, ErrPurgeMount) {
		t.Fatalf("another device: err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "overlay2", "l1", "diff")); err != nil {
		t.Fatalf("the check removed something: %v", err)
	}
}
