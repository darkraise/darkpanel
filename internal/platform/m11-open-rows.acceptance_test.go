package platform

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestA14_HomeFileErrorKinds(t *testing.T) {
	ctx := context.Background()
	t.Run("a cancelled context is not a policy refusal", func(t *testing.T) {
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		_, err := newHomeRig(t).h.ReadAuthorizedKeys(cancelled, "u")
		if !errors.Is(err, context.Canceled) {
			t.Errorf("read with a cancelled context: %v, want context.Canceled", err)
		}
		if errors.Is(err, ErrHomePolicy) {
			t.Errorf("read with a cancelled context: %v is marked a home file policy refusal", err)
		}
	})
	t.Run("a symlinked authorized_keys is a policy refusal", func(t *testing.T) {
		r := newHomeRig(t)
		target := filepath.Join(filepath.Dir(r.home), "elsewhere")
		if err := os.WriteFile(target, []byte("ssh-ed25519 AAAA x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(r.path(".ssh"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, r.path(".ssh", "authorized_keys")); err != nil {
			t.Fatal(err)
		}
		if _, err := r.h.ReadAuthorizedKeys(ctx, "u"); !errors.Is(err, ErrHomePolicy) {
			t.Errorf("read of a symlinked authorized_keys: %v, want a home file policy refusal", err)
		}
	})
}

func TestA15_SSHDirReplacedWhileItIsCreated(t *testing.T) {
	r := newHomeRig(t)
	afterSSHMkdir = func() {
		for _, step := range []error{os.Remove(r.path(".ssh")), os.Mkdir(r.path(".ssh"), 0o755), os.Chmod(r.path(".ssh"), 0o755)} { //nolint:gosec // G301: the directory swapped in under test
			if step != nil {
				t.Error(step)
			}
		}
	}
	t.Cleanup(func() { afterSSHMkdir = nil })

	err := r.h.WriteAuthorizedKeys(context.Background(), "u", []byte("ssh-ed25519 AAAA one\n"))
	if err == nil || !strings.Contains(err.Error(), "changed while it was created") {
		t.Fatalf("write with .ssh replaced: %v, want an error saying it changed while it was created", err)
	}
	entries, rerr := os.ReadDir(r.path(".ssh"))
	if rerr != nil {
		t.Fatal(rerr)
	}
	if len(entries) != 0 {
		t.Errorf("the replacement .ssh holds %d entries after the failed write, want none", len(entries))
	}
}

// A33 (second half): the login shell waits on fd 3 for "ok" and runs the
// profile only after it; a gate closed without it exits. The script is the
// shell's own gate.
const a33Gate = shellGate

func a33Run(t *testing.T, send func(w *os.File)) (marker, fd3 bool, exit error) {
	t.Helper()
	dir := t.TempDir()
	mark := filepath.Join(dir, "profile-ran")
	login := filepath.Join(dir, "login")
	// A login shell that, as a profile script would, leaves a mark first and
	// reports whether fd 3 is open in it.
	body := "#!/bin/sh\necho ran > " + mark + "\nif [ -e /proc/$$/fd/3 ]; then echo open > " + mark + ".fd3; fi\nexit 0\n"
	if err := os.WriteFile(login, []byte(body), 0o700); err != nil { //nolint:gosec // G306: the test's login shell
		t.Fatal(err)
	}
	rd, wr, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rd.Close() }()
	cmd := exec.Command("/bin/sh", "-c", a33Gate, login) //nolint:gosec // G204: the spec's gate and the test's login shell
	cmd.ExtraFiles = []*os.File{rd}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	send(wr)
	_ = wr.Close()
	select {
	case exit = <-done:
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("the shell did not end")
	}
	_, err = os.Stat(mark)
	_, err3 := os.Stat(mark + ".fd3")
	return err == nil, err3 == nil, exit
}

func TestA33_ShellGateRunsTheProfileOnlyAfterOk(t *testing.T) {
	ran, fd3, exit := a33Run(t, func(w *os.File) { _, _ = w.Write([]byte("ok\n")) })
	if exit != nil || !ran {
		t.Errorf("after ok: exit %v, profile ran %v; want exit 0 and the profile run", exit, ran)
	}
	if fd3 {
		t.Error("the session has fd 3 open after the gate")
	}

	for name, send := range map[string]func(*os.File){
		"closed without a word": func(*os.File) {},
		"closed after not-ok":   func(w *os.File) { _, _ = w.Write([]byte("no\n")) },
		"closed on a partial ok": func(w *os.File) {
			_, _ = w.Write([]byte("ok"))
		},
	} {
		ran, _, exit := a33Run(t, send)
		var ee *exec.ExitError
		if !errors.As(exit, &ee) || ee.ExitCode() != 1 {
			t.Errorf("%s: exit %v, want status 1", name, exit)
		}
		if ran {
			t.Errorf("%s: the login profile ran", name)
		}
	}
}

// gateScopeStarter blocks StartScope until release closes; it records whether
// the profile mark existed when it was asked, and the pid it was given.
type a33Scopes struct {
	entered chan int
	release chan struct{}
	err     error
}

func (g *a33Scopes) StartScope(_ context.Context, _ string, pid int) error {
	g.entered <- pid
	<-g.release
	return g.err
}

func TestA33_ShellStartRunsTheProfileInsideTheScope(t *testing.T) {
	dir := t.TempDir()
	mark := filepath.Join(dir, "profile-ran")
	login := filepath.Join(dir, "login")
	// fd 3 is checked by the script itself, before cat, which may open its
	// own fd 3 (uutils cat opens a splice pipe there).
	if err := os.WriteFile(login, []byte("#!/bin/sh\nif [ -e /proc/$$/fd/3 ]; then echo open > "+mark+".fd3; fi\necho ran > "+mark+"\nexec cat\n"), 0o700); err != nil { //nolint:gosec // G306: the test's login shell
		t.Fatal(err)
	}
	passwd := filepath.Join(dir, "passwd")
	if err := os.WriteFile(passwd, []byte("root:x:0:0:root:/root:"+login+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	exists := func() bool { _, err := os.Stat(mark); return err == nil }

	for _, tc := range []struct {
		name     string
		scopeErr error
		wantRun  bool
	}{{"scope moved", nil, true}, {"scope refused", errors.New("scope refused"), false}} {
		t.Run(tc.name, func(t *testing.T) {
			_ = os.Remove(mark)
			g := &a33Scopes{entered: make(chan int, 1), release: make(chan struct{}), err: tc.scopeErr}
			sh := &Shell{scopes: g, passwd: passwd, locale: filepath.Join(dir, "none"), home: dir, killAfter: time.Second}
			type result struct {
				sess interface{ Close() error }
				err  error
			}
			done := make(chan result, 1)
			go func() {
				s, err := sh.Start(context.Background(), 80, 24)
				if err != nil {
					done <- result{nil, err}
					return
				}
				done <- result{s, nil}
			}()
			var pid int
			select {
			case pid = <-g.entered:
			case <-time.After(10 * time.Second):
				t.Fatal("Start never asked for a scope")
			}
			time.Sleep(300 * time.Millisecond) // the profile has had every chance to run
			if exists() {
				t.Error("the profile ran before the shell was inside its scope")
			}
			close(g.release)
			res := <-done
			if res.sess != nil {
				defer func() { _ = res.sess.Close() }()
			}
			if !tc.wantRun {
				if res.err == nil {
					t.Error("Start succeeded although the scope refused")
				}
				time.Sleep(200 * time.Millisecond)
				if exists() {
					t.Error("the profile ran although the shell was never in its scope")
				}
				if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
					t.Errorf("shell %d still alive after the scope refused (kill 0: %v)", pid, err)
				}
				return
			}
			if res.err != nil {
				t.Fatal(res.err)
			}
			for deadline := time.Now().Add(5 * time.Second); !exists() && time.Now().Before(deadline); {
				time.Sleep(10 * time.Millisecond)
			}
			if !exists() {
				t.Error("the profile did not run once the shell was in its scope")
			}
			if _, err := os.Stat(mark + ".fd3"); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("session %d had fd 3 open when its profile ran (%v)", pid, err)
			}
		})
	}
}
