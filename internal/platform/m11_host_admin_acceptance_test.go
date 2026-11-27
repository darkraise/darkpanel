package platform_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform"
)

const acceptBinary = "/usr/local/bin/darkpanel"

func acceptRoot(t *testing.T, free int64) (string, *platform.ConsoleFiles) {
	t.Helper()
	root := t.TempDir()
	for _, d := range []string{"proc", "sys", "dev", "run", "boot/efi", "etc/darkpanel", "var/lib/darkpanel", "usr/local/bin", "srv", "etc/ssh"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, d, "x"), []byte("keep "+d), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "usr/local/bin/darkpanel"), []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "srv/target.txt"), []byte("target"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../etc/darkpanel", filepath.Join(root, "srv/link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target.txt", filepath.Join(root, "srv/file-link")); err != nil {
		t.Fatal(err)
	}
	return root, platform.NewConsoleFiles(platform.ConsoleFilesConfig{Root: root, Binary: acceptBinary,
		FreeSpace: func(int) (int64, error) { return free, nil }})
}

func sum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func codeOf(err error) errcode.Code {
	if err == nil {
		return ""
	}
	return errcode.From(err).Code
}

func tmpFiles(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && strings.HasPrefix(d.Name(), ".darkpanel-tmp-") {
			out = append(out, p)
		}
		return nil
	})
	return out
}

func TestA19_ListsTwoThousandEntriesInOnePageThenTheNext(t *testing.T) {
	root, f := acceptRoot(t, 1<<40)
	dir := filepath.Join(root, "big")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 1990; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("a%04d", i)), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 10; i++ {
		if err := os.Mkdir(filepath.Join(dir, fmt.Sprintf("z%02d", i)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	page, err := f.List(context.Background(), "/big", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 2000 || page.Next != "" {
		t.Fatalf("2000 entries: got %d entries, next %q; want 2000 and no next page", len(page.Entries), page.Next)
	}
	for i := 0; i < 10; i++ {
		if page.Entries[i].Type != core.FileTypeDir {
			t.Fatalf("entry %d is %s %s; directories come first", i, page.Entries[i].Type, page.Entries[i].Name)
		}
	}
	for i := 0; i < 5; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("b%04d", i)), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p1, err := f.List(context.Background(), "/big", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(p1.Entries) != 2000 || p1.Next == "" {
		t.Fatalf("2005 entries: first page %d entries, next %q; want 2000 and a cursor", len(p1.Entries), p1.Next)
	}
	p2, err := f.List(context.Background(), "/big", p1.Next)
	if err != nil {
		t.Fatal(err)
	}
	if len(p2.Entries) != 5 || p2.Next != "" {
		t.Fatalf("second page %d entries, next %q; want 5 and none", len(p2.Entries), p2.Next)
	}
}

func TestA20_TextRoundTripAndConflict(t *testing.T) {
	root, f := acceptRoot(t, 1<<40)
	ctx := context.Background()
	data := bytes.Repeat([]byte("0123456789abcdef"), 1<<16) // 1 MiB
	if _, err := f.WriteText(ctx, "/srv/big.txt", data, ""); err != nil {
		t.Fatalf("write 1 MiB: %v", err)
	}
	got, hash, err := f.ReadText(ctx, "/srv/big.txt")
	if err != nil || !bytes.Equal(got, data) || hash != sum(data) {
		t.Fatalf("read back: err %v, equal %v, hash %s", err, bytes.Equal(got, data), hash)
	}
	stale := sum([]byte("an older content"))
	_, err = f.WriteText(ctx, "/srv/big.txt", []byte("new"), stale)
	if codeOf(err) != errcode.FilesConflict {
		t.Fatalf("write with an outdated hash: %v; want files.conflict", err)
	}
	after, _ := os.ReadFile(filepath.Join(root, "srv/big.txt"))
	if !bytes.Equal(after, data) {
		t.Fatal("a conflicting write changed the file")
	}
	if _, err := f.WriteText(ctx, "/srv/big.txt", []byte("new"), hash); err != nil {
		t.Fatalf("write with the current hash: %v", err)
	}
}

type patternReader struct{ left int64 }

func (p *patternReader) Read(b []byte) (int, error) {
	if p.left == 0 {
		return 0, io.EOF
	}
	n := int64(len(b))
	if n > p.left {
		n = p.left
	}
	for i := int64(0); i < n; i++ {
		b[i] = byte((p.left - i) * 31)
	}
	p.left -= n
	return int(n), nil
}

func TestA21_UploadDownloadByteIdenticalAndFreeSpaceRefusal(t *testing.T) {
	if testing.Short() {
		t.Skip("512 MiB")
	}
	const size = 512 << 20
	root, f := acceptRoot(t, 1<<40)
	ctx := context.Background()
	want := sha256.New()
	if _, err := io.Copy(want, &patternReader{left: size}); err != nil {
		t.Fatal(err)
	}
	gotHash, err := f.Upload(ctx, "/srv/up.bin", &patternReader{left: size}, size)
	if err != nil {
		t.Fatalf("upload 512 MiB: %v", err)
	}
	if gotHash != hex.EncodeToString(want.Sum(nil)) {
		t.Errorf("upload hash %s", gotHash)
	}
	rc, n, err := f.Open(ctx, "/srv/up.bin")
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	dl := sha256.New()
	if _, err := io.Copy(dl, rc); err != nil || n != size || !bytes.Equal(dl.Sum(nil), want.Sum(nil)) {
		t.Fatalf("download: err %v, size %d, identical %v", err, n, bytes.Equal(dl.Sum(nil), want.Sum(nil)))
	}
	if err := os.Remove(filepath.Join(root, "srv/up.bin")); err != nil {
		t.Fatal(err)
	}
	_, tight := acceptRoot(t, 100<<20+63<<20) // 100 MiB upload needs 164 MiB free
	troot := t.TempDir()
	_ = troot
	_, err = tight.Upload(ctx, "/srv/up.bin", &patternReader{left: 100 << 20}, 100<<20)
	if err == nil {
		t.Error("an upload larger than free space minus 64 MiB was accepted")
	}
}

func TestA21_RefusesBeforeWriting(t *testing.T) {
	root, f := acceptRoot(t, 100<<20+63<<20)
	_, err := f.Upload(context.Background(), "/srv/up.bin", &patternReader{left: 100 << 20}, 100<<20)
	if err == nil {
		t.Fatal("an upload larger than free space minus 64 MiB was accepted")
	}
	if _, serr := os.Stat(filepath.Join(root, "srv/up.bin")); serr == nil {
		t.Error("the refused upload left a file")
	}
	if left := tmpFiles(t, root); len(left) != 0 {
		t.Errorf("the refused upload left %v", left)
	}
	_, f2 := acceptRoot(t, 100<<20+64<<20)
	if _, err := f2.Upload(context.Background(), "/srv/ok.bin", &patternReader{left: 100 << 20}, 100<<20); err != nil {
		t.Errorf("an upload of exactly free space minus 64 MiB: %v", err)
	}
}

func TestA63_UploadLimitIs512MiB(t *testing.T) {
	_, f := acceptRoot(t, 1<<50)
	_, err := f.Upload(context.Background(), "/srv/huge.bin", &patternReader{left: 512<<20 + 1}, 512<<20+1)
	if err == nil {
		t.Fatal("an upload of 512 MiB + 1 byte was accepted")
	}
}

func TestA22_ProtectedPathsRefuseWritesRenamesDeletesReads(t *testing.T) {
	root, f := acceptRoot(t, 1<<40)
	ctx := context.Background()
	before := map[string]string{}
	snap := func() map[string]string {
		m := map[string]string{}
		_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				b, _ := os.ReadFile(p)
				m[p] = string(b)
			}
			return nil
		})
		return m
	}
	before = snap()
	for _, dir := range []string{"/proc", "/sys", "/dev", "/run", "/boot/efi", "/etc/darkpanel", "/var/lib/darkpanel"} {
		if _, err := f.WriteText(ctx, dir+"/x", []byte("changed"), sum([]byte("keep "+strings.TrimPrefix(dir, "/")))); err == nil {
			t.Errorf("write under %s accepted", dir)
		}
		if _, err := f.WriteText(ctx, dir+"/new", []byte("changed"), ""); err == nil {
			t.Errorf("create under %s accepted", dir)
		}
		if _, err := f.Upload(ctx, dir+"/up", strings.NewReader("abc"), 3); err == nil {
			t.Errorf("upload under %s accepted", dir)
		}
		if err := f.Rename(ctx, dir+"/x", dir+"/y"); err == nil {
			t.Errorf("rename under %s accepted", dir)
		}
		if err := f.Delete(ctx, dir+"/x", ""); err == nil {
			t.Errorf("delete under %s accepted", dir)
		}
		if err := f.Mkdir(ctx, dir+"/d"); err == nil {
			t.Errorf("mkdir under %s accepted", dir)
		}
	}
	if _, err := f.WriteText(ctx, acceptBinary, []byte("evil"), sum([]byte("binary"))); err == nil {
		t.Error("write over the Darkpanel binary accepted")
	}
	if err := f.Delete(ctx, acceptBinary, ""); err == nil {
		t.Error("delete of the Darkpanel binary accepted")
	}
	if err := f.Rename(ctx, acceptBinary, "/usr/local/bin/other"); err == nil {
		t.Error("rename of the Darkpanel binary accepted")
	}
	for _, anc := range []string{"/etc", "/var/lib", "/var", "/usr/local/bin", "/usr/local", "/usr", "/boot"} {
		if err := f.Rename(ctx, anc, anc+"-moved"); err == nil {
			t.Errorf("rename of ancestor %s accepted", anc)
		}
		if err := f.Delete(ctx, anc, anc); err == nil {
			t.Errorf("recursive delete of ancestor %s accepted", anc)
		}
	}
	if err := f.Delete(ctx, "/", "/"); err == nil {
		t.Error("recursive delete of / accepted")
	}
	for _, p := range []string{"/proc/x", "/sys/x", "/dev/x", "/etc/darkpanel/x", "/var/lib/darkpanel/x"} {
		if _, _, err := f.ReadText(ctx, p); err == nil {
			t.Errorf("read of %s accepted", p)
		}
		if rc, _, err := f.Open(ctx, p); err == nil {
			rc.Close()
			t.Errorf("download of %s accepted", p)
		}
	}
	after := snap()
	if len(after) != len(before) {
		t.Errorf("file count %d, was %d", len(after), len(before))
	}
	for p, c := range before {
		if after[p] != c {
			t.Errorf("%s changed", p)
		}
	}
}

func TestA23_SymlinksAreNeverWrittenThrough(t *testing.T) {
	root, f := acceptRoot(t, 1<<40)
	ctx := context.Background()
	if _, err := f.WriteText(ctx, "/srv/file-link", []byte("changed"), sum([]byte("target"))); err == nil {
		t.Error("write to a symlink accepted")
	}
	if b, _ := os.ReadFile(filepath.Join(root, "srv/target.txt")); string(b) != "target" {
		t.Errorf("target behind the symlink is %q", b)
	}
	if _, err := f.WriteText(ctx, "/srv/link/x", []byte("changed"), sum([]byte("keep etc/darkpanel"))); err == nil {
		t.Error("write through a symlinked directory accepted")
	}
	if _, err := f.WriteText(ctx, "/srv/link/new", []byte("changed"), ""); err == nil {
		t.Error("create through a symlinked directory accepted")
	}
	if b, _ := os.ReadFile(filepath.Join(root, "etc/darkpanel/x")); string(b) != "keep etc/darkpanel" {
		t.Errorf("file behind the symlinked directory is %q", b)
	}
	if _, err := os.Stat(filepath.Join(root, "etc/darkpanel/new")); err == nil {
		t.Error("a file was created behind the symlinked directory")
	}
}

func TestA24_OverwriteKeepsModeAndOwner(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("needs root to chown")
	}
	root, f := acceptRoot(t, 1<<40)
	p := filepath.Join(root, "srv/owned.txt")
	if err := os.WriteFile(p, []byte("old"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(p, 4321, 4322); err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteText(context.Background(), "/srv/owned.txt", []byte("new"), sum([]byte("old"))); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	sys := st.Sys().(*syscall.Stat_t)
	if st.Mode().Perm() != 0o640 || sys.Uid != 4321 || sys.Gid != 4322 {
		t.Errorf("mode %v owner %d:%d; want 0640 4321:4322", st.Mode().Perm(), sys.Uid, sys.Gid)
	}
	if b, _ := os.ReadFile(p); string(b) != "new" {
		t.Errorf("content %q", b)
	}
}

func TestA25_DeleteDirectoryNeedsConfirm(t *testing.T) {
	root, f := acceptRoot(t, 1<<40)
	ctx := context.Background()
	if err := os.MkdirAll(filepath.Join(root, "srv/d/sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "srv/d/sub/f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := f.Delete(ctx, "/srv/d", ""); err == nil {
		t.Error("delete of a directory without confirm accepted")
	}
	if err := f.Delete(ctx, "/srv/d", "/srv/other"); err == nil {
		t.Error("delete of a directory with another path as confirm accepted")
	}
	if _, err := os.Stat(filepath.Join(root, "srv/d/sub/f")); err != nil {
		t.Fatal("a refused delete removed content")
	}
	if err := f.Delete(ctx, "/srv/d", "/srv/d"); err != nil {
		t.Fatalf("delete with confirm: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "srv/d")); err == nil {
		t.Error("the directory is still there")
	}
}

type failAfter struct {
	n   int
	err error
}

func (r *failAfter) Read(b []byte) (int, error) {
	if r.n <= 0 {
		return 0, r.err
	}
	if len(b) > r.n {
		b = b[:r.n]
	}
	r.n -= len(b)
	return len(b), nil
}

func TestA85_FailedAndCancelledUploadsLeaveNoTemporaryFile(t *testing.T) {
	root, f := acceptRoot(t, 1<<40)
	if _, err := f.Upload(context.Background(), "/srv/cut.bin", &failAfter{n: 1 << 20, err: errors.New("link dropped")}, 4<<20); err == nil {
		t.Error("a cut upload succeeded")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _ = f.Upload(ctx, "/srv/cancel.bin", &patternReader{left: 1 << 20}, 1<<20)
	if left := tmpFiles(t, root); len(left) != 0 {
		t.Errorf("temporary files left: %v", left)
	}
	for _, n := range []string{"cut.bin"} {
		if _, err := os.Stat(filepath.Join(root, "srv", n)); err == nil {
			t.Errorf("%s exists after a failed upload", n)
		}
	}
}

type noScopes struct{}

func (noScopes) StartScope(context.Context, string, int) error { return nil }

type watcher struct {
	ch  chan string
	acc string
}

func watch(s core.ShellSession) *watcher {
	w := &watcher{ch: make(chan string, 4096)}
	go func() {
		b := make([]byte, 4096)
		for {
			n, err := s.Read(b)
			if n > 0 {
				w.ch <- string(b[:n])
			}
			if err != nil {
				close(w.ch)
				return
			}
		}
	}()
	return w
}

func (w *watcher) until(t *testing.T, re string, d time.Duration) {
	t.Helper()
	rx := regexp.MustCompile(re)
	deadline := time.After(d)
	for {
		if rx.MatchString(w.acc) {
			return
		}
		select {
		case c, ok := <-w.ch:
			if !ok {
				t.Fatalf("shell output ended before %q; got %q", re, w.acc)
			}
			w.acc += c
		case <-deadline:
			t.Fatalf("no output matching %q within %v; got %q", re, d, w.acc)
		}
	}
}

func TestA12_TypedBytesReachTheShellAndResizeChangesTheSize(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("needs root")
	}
	s, err := platform.NewShell(noScopes{}).Start(context.Background(), 90, 33)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	w := watch(s)
	if _, err := s.Write([]byte("echo \"hel\"\"lo-$((6*7))\"; stty size\n")); err != nil {
		t.Fatal(err)
	}
	w.until(t, `hello-42\r?\n33 90`, 10*time.Second)
	if err := s.Resize(132, 41); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Write([]byte("echo si\"\"ze; stty size\n")); err != nil {
		t.Fatal(err)
	}
	w.until(t, `size\r?\n41 132`, 10*time.Second)
}

func TestA13_CloseSendsSIGHUPThenSIGKILLAfterFiveSeconds(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("needs root")
	}
	s, err := platform.NewShell(noScopes{}).Start(context.Background(), 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	w := watch(s)
	if _, err := s.Write([]byte("trap '' HUP; echo rea\"\"dy; while :; do sleep 1; done\n")); err != nil {
		t.Fatal(err)
	}
	w.until(t, `ready\r?\n`, 10*time.Second)
	start := time.Now()
	done := make(chan struct{})
	go func() { _ = s.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("Close did not return: nothing killed the shell that ignores SIGHUP")
	}
	el := time.Since(start)
	if el < 4*time.Second || el > 8*time.Second {
		t.Errorf("Close took %v; a shell ignoring SIGHUP is killed after 5 s", el)
	}
	code, _ := s.Wait()
	if code != 128+int(syscall.SIGKILL) {
		t.Errorf("exit code %d; want 137 (SIGKILL)", code)
	}
}

func TestA13_ShellHonouringSIGHUPEndsAtOnce(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("needs root")
	}
	s, err := platform.NewShell(noScopes{}).Start(context.Background(), 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	w := watch(s)
	if _, err := s.Write([]byte("echo rea\"\"dy\n")); err != nil {
		t.Fatal(err)
	}
	w.until(t, `ready`, 10*time.Second)
	start := time.Now()
	_ = s.Close()
	if el := time.Since(start); el > 3*time.Second {
		t.Errorf("Close took %v for a shell that honours SIGHUP", el)
	}
	if code, _ := s.Wait(); code != 128+int(syscall.SIGHUP) && code != 0 && code != 129 {
		t.Logf("exit code %d", code)
	}
}

type homeRig struct {
	h    *platform.HomeFiles
	home string
	uid  int
}

func newHome(t *testing.T) homeRig {
	t.Helper()
	if os.Getuid() != 0 {
		t.Skip("needs root to chown")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "home", "u")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(home, 4242, 4243); err != nil {
		t.Fatal(err)
	}
	passwd := filepath.Join(root, "passwd")
	line := fmt.Sprintf("root:x:0:0:root:/root:/bin/bash\nu:x:4242:4243::%s:/bin/bash\n", home)
	if err := os.WriteFile(passwd, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	return homeRig{h: platform.NewHomeFiles(passwd), home: home, uid: 4242}
}

func (r homeRig) mkssh(t *testing.T, content string) string {
	t.Helper()
	d := filepath.Join(r.home, ".ssh")
	if err := os.MkdirAll(d, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(d, r.uid, 4243); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(d, "authorized_keys")
	if err := os.WriteFile(f, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(f, r.uid, 4243); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestA39_KeyFileAccessRefusals(t *testing.T) {
	ctx := context.Background()
	t.Run("a regular file is read and written", func(t *testing.T) {
		r := newHome(t)
		r.mkssh(t, "ssh-ed25519 AAAA one\n")
		b, err := r.h.ReadAuthorizedKeys(ctx, "u")
		if err != nil || string(b) != "ssh-ed25519 AAAA one\n" {
			t.Fatalf("read %q, %v", b, err)
		}
		if err := r.h.WriteAuthorizedKeys(ctx, "u", []byte("ssh-ed25519 AAAA two\n")); err != nil {
			t.Fatalf("write: %v", err)
		}
	})
	t.Run(".ssh is a symlink", func(t *testing.T) {
		r := newHome(t)
		other := filepath.Join(filepath.Dir(r.home), "elsewhere")
		_ = os.MkdirAll(other, 0o700)
		_ = os.WriteFile(filepath.Join(other, "authorized_keys"), []byte("secret\n"), 0o600)
		if err := os.Symlink(other, filepath.Join(r.home, ".ssh")); err != nil {
			t.Fatal(err)
		}
		if _, err := r.h.ReadAuthorizedKeys(ctx, "u"); err == nil {
			t.Error("read through a symlinked .ssh accepted")
		}
		if err := r.h.WriteAuthorizedKeys(ctx, "u", []byte("ssh-ed25519 AAAA x\n")); err == nil {
			t.Error("write through a symlinked .ssh accepted")
		}
		if b, _ := os.ReadFile(filepath.Join(other, "authorized_keys")); string(b) != "secret\n" {
			t.Errorf("the file behind the symlink changed: %q", b)
		}
	})
	t.Run("authorized_keys is a symlink", func(t *testing.T) {
		r := newHome(t)
		f := r.mkssh(t, "x\n")
		target := filepath.Join(r.home, "real")
		_ = os.WriteFile(target, []byte("real\n"), 0o600)
		_ = os.Remove(f)
		if err := os.Symlink(target, f); err != nil {
			t.Fatal(err)
		}
		if err := r.h.WriteAuthorizedKeys(ctx, "u", []byte("ssh-ed25519 AAAA x\n")); err == nil {
			t.Error("write over a symlinked authorized_keys accepted")
		}
		if _, err := r.h.ReadAuthorizedKeys(ctx, "u"); err == nil {
			t.Error("read of a symlinked authorized_keys accepted")
		}
		if b, _ := os.ReadFile(target); string(b) != "real\n" {
			t.Errorf("target changed: %q", b)
		}
	})
	t.Run("authorized_keys is not a regular file", func(t *testing.T) {
		r := newHome(t)
		f := r.mkssh(t, "x\n")
		_ = os.Remove(f)
		if err := os.Mkdir(f, 0o700); err != nil {
			t.Fatal(err)
		}
		if _, err := r.h.ReadAuthorizedKeys(ctx, "u"); err == nil {
			t.Error("a directory as authorized_keys accepted")
		}
	})
	t.Run("authorized_keys is over 1 MiB", func(t *testing.T) {
		r := newHome(t)
		r.mkssh(t, strings.Repeat("a", 1<<20+1))
		if _, err := r.h.ReadAuthorizedKeys(ctx, "u"); err == nil {
			t.Error("a 1 MiB + 1 byte authorized_keys accepted")
		}
	})
	t.Run("authorized_keys owned by another user", func(t *testing.T) {
		r := newHome(t)
		f := r.mkssh(t, "x\n")
		if err := os.Chown(f, 5555, 4243); err != nil {
			t.Fatal(err)
		}
		if _, err := r.h.ReadAuthorizedKeys(ctx, "u"); err == nil {
			t.Error("authorized_keys owned by a third user accepted")
		}
	})
	for _, mode := range []os.FileMode{0o775, 0o757} {
		t.Run(fmt.Sprintf("home mode %o", mode), func(t *testing.T) {
			r := newHome(t)
			r.mkssh(t, "x\n")
			if err := os.Chmod(r.home, mode); err != nil {
				t.Fatal(err)
			}
			if err := r.h.WriteAuthorizedKeys(ctx, "u", []byte("ssh-ed25519 AAAA x\n")); err == nil {
				t.Error("write in a group- or other-writable home accepted")
			}
		})
	}
}
