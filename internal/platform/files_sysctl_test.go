package platform

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/darkraise/darkpanel/internal/core"
)

const (
	sysForward   = "net/ipv4/ip_forward"
	sysForward6  = "net/ipv6/conf/<iface>/forwarding"
	sysOtherKey  = "net/ipv4/tcp_syncookies"
	sysOtherMod  = "net/core/somaxconn"
	sysTreeValue = "0\n"
)

// sysFiles returns a writer whose sysctl root is a temporary tree holding
// net/ipv4/ip_forward, net/ipv4/tcp_syncookies, net/core/somaxconn and
// net/ipv6/conf/{all,default,eth0}/forwarding, each "0\n". Module "sys"
// declares ip_forward and the <iface> forwarding key with Pattern ^[01]$;
// module "other" declares somaxconn.
func sysFiles(t *testing.T) (*Files, *fakeOwnership, string) {
	t.Helper()
	root := t.TempDir()
	keys := []string{sysForward, sysOtherKey, sysOtherMod}
	for _, iface := range []string{"all", "default", "eth0"} {
		keys = append(keys, "net/ipv6/conf/"+iface+"/forwarding")
	}
	for _, k := range keys {
		path := filepath.Join(root, k)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(sysTreeValue), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	f, own := fpFiles(t, FilesConfig{ProcSys: root, Owned: map[string][]core.OwnedPath{
		"sys": {
			{Path: sysForward, Kind: core.OwnedSysctl, Pattern: `^[01]$`},
			{Path: sysForward6, Kind: core.OwnedSysctl, Pattern: `^[01]$`},
		},
		"other": {{Path: sysOtherMod, Kind: core.OwnedSysctl, Pattern: `^[0-9]+$`}},
	}})
	return f, own, root
}

func TestWriteSysctl(t *testing.T) {
	f, own, root := sysFiles(t)
	ctx := context.Background()
	if err := f.WriteSysctl(ctx, "sys", sysForward, "1"); err != nil {
		t.Fatalf("WriteSysctl: %v", err)
	}
	if got := readFile(t, filepath.Join(root, sysForward)); got != "1\n" {
		t.Errorf("key holds %q, want 1 written over 0 at offset 0", got)
	}
	if got, err := f.ReadSysctl(ctx, "sys", sysForward); err != nil || got != "1" {
		t.Errorf("ReadSysctl = %q, %v; want 1", got, err)
	}
	if got, _ := own.List(ctx); len(got) != 0 {
		t.Errorf("ownership = %+v, want nothing recorded", got)
	}
}

func TestSysctlPattern(t *testing.T) {
	f, _, root := sysFiles(t)
	if err := f.WriteSysctl(context.Background(), "sys", sysForward, "2"); err == nil {
		t.Fatal("WriteSysctl of a value outside the pattern succeeded")
	}
	if got := readFile(t, filepath.Join(root, sysForward)); got != sysTreeValue {
		t.Errorf("key holds %q, want it untouched", got)
	}
}

func TestSysctlInterfaces(t *testing.T) {
	cases := []struct {
		iface    string
		ok       bool
		notExist bool
	}{
		{iface: "all", ok: true},
		{iface: "default", ok: true},
		{iface: "eth0", ok: true},
		{iface: "eth9", notExist: true},
		{iface: ".."},
		{iface: "."},
		{iface: "a/b"},
		{iface: "abcdefghijklmnop"},
	}
	for _, c := range cases {
		t.Run(c.iface, func(t *testing.T) {
			f, _, root := sysFiles(t)
			key := "net/ipv6/conf/" + c.iface + "/forwarding"
			err := f.WriteSysctl(context.Background(), "sys", key, "1")
			switch {
			case c.ok && err != nil:
				t.Fatalf("WriteSysctl(%s): %v", key, err)
			case c.ok:
				if got := readFile(t, filepath.Join(root, key)); got != "1\n" {
					t.Errorf("key holds %q", got)
				}
			case err == nil:
				t.Fatalf("WriteSysctl(%s) succeeded, want a refusal", key)
			case c.notExist != errors.Is(err, fs.ErrNotExist):
				t.Errorf("WriteSysctl(%s) = %v; wraps fs.ErrNotExist: %v, want %v", key, err, errors.Is(err, fs.ErrNotExist), c.notExist)
			}
			if _, err := f.ReadSysctl(context.Background(), "sys", key); c.ok != (err == nil) {
				t.Errorf("ReadSysctl(%s) = %v", key, err)
			}
			if !c.ok {
				sysUntouched(t, root)
			}
		})
	}
}

func TestSysctlReadBackMismatch(t *testing.T) {
	f, _, root := sysFiles(t)
	path := filepath.Join(root, sysForward)
	if err := os.WriteFile(path, []byte("0x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := f.WriteSysctl(context.Background(), "sys", sysForward, "1")
	if err == nil || !strings.Contains(err.Error(), `reads back "1x"`) {
		t.Fatalf("WriteSysctl = %v, want the read-back mismatch", err)
	}
	if got := readFile(t, path); got != "1x\n" {
		t.Errorf("key holds %q, want one write at offset 0 without truncation", got)
	}
}

func TestSysctlRefuses(t *testing.T) {
	cases := []struct {
		name, module, key string
		setup             func(t *testing.T, root string)
	}{
		{name: "undeclared key", module: "sys", key: sysOtherKey},
		{name: "key of another module", module: "sys", key: sysOtherMod},
		{name: "absolute key", module: "sys", key: "/" + sysForward},
		{name: "symlinked directory", module: "sys", key: "net/ipv6/conf/lnk/forwarding", setup: func(t *testing.T, root string) {
			if err := os.Symlink("eth0", filepath.Join(root, "net/ipv6/conf/lnk")); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "symlinked key", module: "sys", key: sysForward, setup: func(t *testing.T, root string) {
			path := filepath.Join(root, sysForward)
			if err := os.Rename(path, path+".real"); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("ip_forward.real", path); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, _, root := sysFiles(t)
			if c.setup != nil {
				c.setup(t, root)
			}
			if err := f.WriteSysctl(context.Background(), c.module, c.key, "1"); err == nil {
				t.Fatal("WriteSysctl succeeded, want a refusal")
			}
			if _, err := f.ReadSysctl(context.Background(), c.module, c.key); err == nil {
				t.Error("ReadSysctl succeeded, want a refusal")
			}
			sysUntouched(t, root)
		})
	}
}

func TestIsProcfs(t *testing.T) {
	fd, err := unix.Open(t.TempDir(), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unix.Close(fd) }()
	if ok, err := isProcfs(fd); err != nil || ok {
		t.Errorf("isProcfs(temporary directory) = %v, %v; want false", ok, err)
	}
	proc, err := unix.Open(procSys, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unix.Close(proc) }()
	if ok, err := isProcfs(proc); err != nil || !ok {
		t.Errorf("isProcfs(%s) = %v, %v; want true", procSys, ok, err)
	}
}

// TestOpenProcSysReal opens the real /proc/sys, which the procfs check must
// accept.
func TestOpenProcSysReal(t *testing.T) {
	root, err := (&Files{}).openProcSys()
	if err != nil {
		t.Fatalf("openProcSys of the real %s: %v", procSys, err)
	}
	defer func() { _ = unix.Close(root) }()
	if got, err := readSysctl(root, "kernel/ostype"); err != nil || got != "Linux" {
		t.Errorf("kernel/ostype = %q, %v; want Linux", got, err)
	}
}

// sysUntouched checks that every regular file of the tree still holds the
// initial value.
func sysUntouched(t *testing.T, root string) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return err
		}
		if got := readFile(t, path); got != sysTreeValue {
			t.Errorf("%s holds %q, want it untouched", path, got)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
