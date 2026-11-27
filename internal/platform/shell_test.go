package platform

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/darkraise/darkpanel/internal/core"
)

// noScopes is a ScopeStarter that leaves the shell where it is.
type noScopes struct{}

func (noScopes) StartScope(context.Context, string, int) error { return nil }

func needRoot(t *testing.T) {
	t.Helper()
	if os.Getuid() != 0 {
		t.Skip("starts root's login shell: run as root")
	}
}

// screen collects a session's output.
type screen struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func watch(sess core.ShellSession) *screen {
	s := &screen{}
	go func() {
		p := make([]byte, 4096)
		for {
			n, err := sess.Read(p)
			s.mu.Lock()
			s.buf.Write(p[:n])
			s.mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	return s
}

// waitFor waits until the output matches re.
func (s *screen) waitFor(t *testing.T, re string) {
	t.Helper()
	r := regexp.MustCompile(re)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		ok := r.Match(s.buf.Bytes())
		s.mu.Unlock()
		if ok {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t.Fatalf("the terminal never showed %q; it shows %q", re, s.buf.String())
}

func typeLine(t *testing.T, sess core.ShellSession, line string) {
	t.Helper()
	if _, err := sess.Write([]byte(line + "\n")); err != nil {
		t.Fatal(err)
	}
}

func TestShellPTY(t *testing.T) {
	needRoot(t)
	sess, err := NewShell(noScopes{}).Start(context.Background(), 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()
	out := watch(sess)
	// The quotes keep the terminal's echo of the command from matching.
	typeLine(t, sess, `stty size; echo "si""ze-done"`)
	out.waitFor(t, `24 80\r?\nsize-done`)
	if err := sess.Resize(120, 40); err != nil {
		t.Fatal(err)
	}
	typeLine(t, sess, `stty size; echo "re""sized"`)
	out.waitFor(t, `40 120\r?\nresized`)
	typeLine(t, sess, `echo "[$TERM:$USER:$LOGNAME:$HOME:$(pwd)]"; tty`)
	out.waitFor(t, `\[xterm-256color:root:root:/root:/root\]\r?\n/dev/pts/`)
	typeLine(t, sess, "exit 7")
	if code, err := sess.Wait(); code != 7 || err != nil {
		t.Errorf("Wait after exit 7 = %d, %v; want 7", code, err)
	}
}

func TestShellCloseSignals(t *testing.T) {
	needRoot(t)
	t.Run("hang-up", func(t *testing.T) {
		sess, err := NewShell(noScopes{}).Start(context.Background(), 80, 24)
		if err != nil {
			t.Fatal(err)
		}
		out := watch(sess)
		typeLine(t, sess, `echo "rea""dy"`)
		out.waitFor(t, `[\r\n]ready\r\n`)
		start := time.Now()
		if err := sess.Close(); err != nil {
			t.Fatal(err)
		}
		if d := time.Since(start); d >= 4*time.Second {
			t.Errorf("a plain shell took %v to end on SIGHUP", d)
		}
		if code, _ := sess.Wait(); code != 128+int(syscall.SIGHUP) {
			t.Errorf("exit code %d, want %d (SIGHUP)", code, 128+int(syscall.SIGHUP))
		}
	})
	t.Run("ignores hang-up", func(t *testing.T) {
		sess, err := NewShell(noScopes{}).Start(context.Background(), 80, 24)
		if err != nil {
			t.Fatal(err)
		}
		out := watch(sess)
		typeLine(t, sess, `trap '' HUP; echo "arm""ed"; sleep 60`)
		out.waitFor(t, `[\r\n]armed\r\n`)
		// The sleep ignores SIGHUP too and outlives its shell, as it would
		// a dropped SSH connection.
		pid := sess.(*shellSession).pid
		children, err := os.ReadFile(fmt.Sprintf("/proc/%d/task/%d/children", pid, pid))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			for _, c := range strings.Fields(string(children)) {
				if n, err := strconv.Atoi(c); err == nil {
					_ = syscall.Kill(n, syscall.SIGKILL)
				}
			}
		})
		start := time.Now()
		if err := sess.Close(); err != nil {
			t.Fatal(err)
		}
		if d := time.Since(start); d < 5*time.Second || d > 8*time.Second {
			t.Errorf("Close returned after %v, want SIGKILL 5 s after the hang-up", d)
		}
		if code, _ := sess.Wait(); code != 128+int(syscall.SIGKILL) {
			t.Errorf("exit code %d, want %d (SIGKILL)", code, 128+int(syscall.SIGKILL))
		}
	})
}

func TestShellScope(t *testing.T) {
	dir := t.TempDir()
	passwd := filepath.Join(dir, "passwd")
	if err := os.WriteFile(passwd, []byte("daemon:x:1:1::/:/bin/false\nroot:x:0:0:root:/root:/bin/sh\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "locale"), []byte("# set by the installer\nLANG=\"en_GB.UTF-8\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	newShell := func(bus *fakeBus) *Shell {
		return &Shell{scopes: newSystemd(bus), passwd: passwd, locale: filepath.Join(dir, "locale"), home: dir, killAfter: 5 * time.Second}
	}
	pidsOf := func(t *testing.T, bus *fakeBus) (string, []uint32) {
		t.Helper()
		if len(bus.calls) != 1 || bus.calls[0].method != managerInterface+".StartTransientUnit" {
			t.Fatalf("bus calls = %+v, want one StartTransientUnit", bus.calls)
		}
		args := bus.calls[0].args
		name, _ := args[0].(string)
		if !regexp.MustCompile(`^darkpanel-console-[0-9a-f]{16}\.scope$`).MatchString(name) || args[1] != "fail" {
			t.Errorf("StartTransientUnit(%v, %v), want darkpanel-console-<id>.scope in mode fail", name, args[1])
		}
		for _, p := range args[2].([]property) {
			if p.Name == "PIDs" {
				pids, _ := p.Value.Value().([]uint32)
				return name, pids
			}
		}
		t.Fatal("the scope has no PIDs property")
		return "", nil
	}

	t.Run("moved", func(t *testing.T) {
		bus := &fakeBus{}
		sess, err := newShell(bus).Start(context.Background(), 80, 24)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = sess.Close() }()
		_, pids := pidsOf(t, bus)
		pid := sess.(*shellSession).pid
		if !slices.Equal(pids, []uint32{uint32(pid)}) { //nolint:gosec // G115: a pid
			t.Errorf("scope PIDs = %v, want the shell's %d", pids, pid)
		}
		// cmd.Start returns once exec closes the child's close-on-exec pipe,
		// which the kernel does before it lays out the new environment, so
		// environ reads empty for a moment.
		var environ []byte
		for deadline := time.Now().Add(5 * time.Second); len(environ) == 0 && time.Now().Before(deadline); time.Sleep(time.Millisecond) {
			if environ, err = os.ReadFile(fmt.Sprintf("/proc/%d/environ", pid)); err != nil {
				t.Fatal(err)
			}
		}
		want := "TERM=xterm-256color\x00HOME=/root\x00USER=root\x00LOGNAME=root\x00SHELL=/bin/sh\x00PATH=/usr/sbin:/usr/bin:/sbin:/bin\x00LANG=en_GB.UTF-8\x00"
		if string(environ) != want {
			t.Errorf("the shell's environment is %q, want %q", environ, want)
		}
	})
	t.Run("D-Bus fails", func(t *testing.T) {
		refused := dbus.Error{Name: "org.freedesktop.DBus.Error.AccessDenied"}
		bus := &fakeBus{reply: func(busCall) ([]any, error) { return nil, refused }}
		sess, err := newShell(bus).Start(context.Background(), 80, 24)
		var dErr dbus.Error
		if sess != nil || !errors.As(err, &dErr) || dErr.Name != refused.Name {
			t.Fatalf("Start = %v, %v; want the D-Bus error", sess, err)
		}
		_, pids := pidsOf(t, bus)
		if len(pids) != 1 {
			t.Fatalf("scope PIDs = %v", pids)
		}
		if err := syscall.Kill(int(pids[0]), 0); !errors.Is(err, syscall.ESRCH) {
			t.Errorf("the shell %d still runs after the scope failed (kill 0: %v)", pids[0], err)
		}
	})
}

func TestWaitInCgroup(t *testing.T) {
	const unit = "darkpanel-console-0123456789abcdef.scope"
	write := func(t *testing.T, proc, text string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(proc, "42"), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(proc, "42", "cgroup"), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("moved", func(t *testing.T) {
		proc := t.TempDir()
		write(t, proc, "0::/system.slice/"+unit+"\n")
		if err := waitInCgroup(context.Background(), proc, 42, unit, time.Second); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("moved later", func(t *testing.T) {
		proc := t.TempDir()
		write(t, proc, "0::/system.slice/darkpanel-agent.service\n")
		go func() {
			time.Sleep(50 * time.Millisecond)
			moved := "1:name=systemd:/system.slice/" + unit + "\n0::/system.slice/" + unit + "\n"
			_ = os.WriteFile(filepath.Join(proc, "42", "cgroup"), []byte(moved), 0o600)
		}()
		if err := waitInCgroup(context.Background(), proc, 42, unit, 5*time.Second); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("never moved", func(t *testing.T) {
		proc := t.TempDir()
		write(t, proc, "0::/system.slice/darkpanel-agent.service\n")
		err := waitInCgroup(context.Background(), proc, 42, unit, 100*time.Millisecond)
		if err == nil || !strings.Contains(err.Error(), "is not in "+unit) {
			t.Fatalf("waitInCgroup = %v, want it to say the process is not in %s", err, unit)
		}
	})
}

// gateScopes is a ScopeStarter that blocks until release is closed, then
// answers err.
type gateScopes struct {
	entered chan int
	release chan struct{}
	err     error
}

func (g *gateScopes) StartScope(_ context.Context, _ string, pid int) error {
	g.entered <- pid
	<-g.release
	return g.err
}

// TestShellWaitsForScope: the login shell, whose start writes a marker as a
// profile would, runs only once its scope holds it, and never when the move
// failed; fd 3, the gate, is closed in the shell it starts.
func TestShellWaitsForScope(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "marker")
	login := filepath.Join(dir, "login")
	// The script reports fd 3 itself, before it runs anything: a program it
	// starts may open fd 3 of its own (uutils cat opens a splice pipe there).
	script := "#!/bin/sh\nif [ -e /proc/$$/fd/3 ]; then echo open; else echo closed; fi > " + marker + ".fd3\necho started > " + marker + "\nexec cat\n"
	if err := os.WriteFile(login, []byte(script), 0o700); err != nil { //nolint:gosec // G306: the test's login shell
		t.Fatal(err)
	}
	passwd := filepath.Join(dir, "passwd")
	if err := os.WriteFile(passwd, []byte("root:x:0:0:root:/root:"+login+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	exists := func() bool {
		_, err := os.Stat(marker)
		return err == nil
	}
	start := func(t *testing.T, scopeErr error) (int, chan struct{}, chan error, *core.ShellSession) {
		t.Helper()
		_ = os.Remove(marker)
		g := &gateScopes{entered: make(chan int, 1), release: make(chan struct{}), err: scopeErr}
		sh := &Shell{scopes: g, passwd: passwd, locale: filepath.Join(dir, "none"), home: dir, killAfter: time.Second}
		var sess core.ShellSession
		done := make(chan error, 1)
		go func() {
			s, err := sh.Start(context.Background(), 80, 24)
			sess = s
			done <- err
		}()
		var pid int
		select {
		case pid = <-g.entered:
		case <-time.After(5 * time.Second):
			t.Fatal("Start never asked for the scope")
		}
		return pid, g.release, done, &sess
	}

	t.Run("moved", func(t *testing.T) {
		pid, release, done, sess := start(t, nil)
		time.Sleep(300 * time.Millisecond)
		if exists() {
			t.Error("the login shell ran before its scope held it")
		}
		close(release)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		defer func() { _ = (*sess).Close() }()
		for deadline := time.Now().Add(5 * time.Second); !exists() && time.Now().Before(deadline); {
			time.Sleep(10 * time.Millisecond)
		}
		if !exists() {
			t.Fatal("the login shell did not run after its scope held it")
		}
		if fd3, err := os.ReadFile(marker + ".fd3"); err != nil || string(fd3) != "closed\n" {
			t.Errorf("the login shell %d saw fd 3 %q (%v), want the gate closed", pid, fd3, err)
		}
	})
	t.Run("move failed", func(t *testing.T) {
		pid, release, done, _ := start(t, errors.New("scope refused"))
		close(release)
		if err := <-done; err == nil || !strings.Contains(err.Error(), "scope refused") {
			t.Fatalf("Start = %v, want the scope's error", err)
		}
		if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
			t.Errorf("the shell %d still runs after its move failed (kill 0: %v)", pid, err)
		}
		time.Sleep(200 * time.Millisecond)
		if exists() {
			t.Error("the login shell ran although its move failed")
		}
	})
}
