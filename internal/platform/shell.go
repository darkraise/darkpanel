package platform

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/godbus/dbus/v5"
	"golang.org/x/sys/unix"

	"github.com/darkraise/darkpanel/internal/core"
)

// ScopeStarter moves a process into a new transient scope; DBusSystemd
// implements it.
type ScopeStarter interface {
	StartScope(ctx context.Context, name string, pid int) error
}

// scopeWait bounds how long StartScope waits for systemd to move the
// process into its scope.
const scopeWait = 5 * time.Second

// StartScope starts the transient scope name, which must end in .scope,
// holding pid, and returns once pid is in it. The "fail" mode refuses a name
// already loaded.
func (s *DBusSystemd) StartScope(ctx context.Context, name string, pid int) error {
	what := "start " + name
	if !strings.HasSuffix(name, ".scope") || pid <= 0 {
		return fmt.Errorf("platform: %s: want a .scope name and a process id", what)
	}
	props := []property{
		{"Description", dbus.MakeVariant("Darkpanel console terminal")},
		{"PIDs", dbus.MakeVariant([]uint32{uint32(pid)})}, //nolint:gosec // G115: a positive pid, checked above
		{"CollectMode", dbus.MakeVariant("inactive-or-failed")},
	}
	if _, err := s.manager(ctx, what, "StartTransientUnit", name, "fail", props, []auxUnit{}); err != nil || s.proc == "" {
		return err
	}
	return waitInCgroup(ctx, s.proc, pid, name, scopeWait)
}

// waitInCgroup waits until proc/<pid>/cgroup places pid in unit. systemd
// answers StartTransientUnit once the job is queued; a child the process
// forks before the move stays in the agent's cgroup, and the agent's stop
// kills it.
func waitInCgroup(ctx context.Context, proc string, pid int, unit string, limit time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	path := filepath.Join(proc, strconv.Itoa(pid), "cgroup")
	for {
		b, err := os.ReadFile(path) //nolint:gosec // G304: a process's cgroup file under proc
		if err != nil {
			return fmt.Errorf("platform: read %s: %w", path, err)
		}
		if inCgroup(b, unit) {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("platform: process %d is not in %s after %s", pid, unit, limit)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// inCgroup reports whether a /proc/<pid>/cgroup text has a line ending in unit.
func inCgroup(cgroup []byte, unit string) bool {
	for line := range bytes.SplitSeq(cgroup, []byte("\n")) {
		if bytes.HasSuffix(line, []byte("/"+unit)) {
			return true
		}
	}
	return false
}

// Shell is core.ShellStarter: root's login shell on a new PTY, in a new
// session with the PTY as its controlling terminal, moved into a transient
// scope of its own so the agent unit's stop never kills what an admin
// started (ADR 0020). It is the one code path that runs a program outside
// the exec allowlist.
type Shell struct {
	scopes    ScopeStarter
	passwd    string        // where root's login shell is read
	locale    string        // where LANG is read
	home      string        // the shell's working directory, "/" when missing
	killAfter time.Duration // from the hang-up to SIGKILL
}

var _ core.ShellStarter = (*Shell)(nil)

// NewShell returns the host's Shell, its scopes started through scopes.
func NewShell(scopes ScopeStarter) *Shell {
	return &Shell{scopes: scopes, passwd: "/etc/passwd", locale: "/etc/default/locale", home: "/root", killAfter: 5 * time.Second}
}

// shellGate is the script the shell starts as: it waits for "ok" on fd 3,
// which the agent writes once the process is in its scope, then closes fd
// 3 and execs the login shell, $0, with -l. Anything the login shell's
// profile starts is in the scope too; a gate closed without "ok" exits.
const shellGate = `IFS= read -r go <&3 && [ "$go" = ok ] || exit 1; exec "$0" -l 3<&-`

// Start starts root's login shell with -l on a terminal of cols by rows,
// once its process is in a scope of its own. A shell that cannot be moved
// into its scope is killed before the login shell runs.
func (s *Shell) Start(ctx context.Context, cols, rows uint16) (core.ShellSession, error) {
	program := loginShell(s.passwd)
	master, tty, err := openPTY()
	if err != nil {
		return nil, err
	}
	if err := setWinsize(master, cols, rows); err != nil {
		_, _ = master.Close(), tty.Close()
		return nil, err
	}
	gate, open, err := os.Pipe()
	if err != nil {
		_, _ = master.Close(), tty.Close()
		return nil, fmt.Errorf("platform: start the shell %s: %w", program, err)
	}
	defer func() { _ = open.Close() }()
	cmd := exec.Command("/bin/sh", "-c", shellGate, program) //nolint:gosec // G204: root's login shell (ADR 0020)
	cmd.Env = s.environ(program)
	cmd.Dir = "/"
	if st, err := os.Stat(s.home); err == nil && st.IsDir() {
		cmd.Dir = s.home
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = tty, tty, tty
	cmd.ExtraFiles = []*os.File{gate}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	err = cmd.Start()
	_, _ = tty.Close(), gate.Close()
	if err != nil {
		_ = master.Close()
		return nil, fmt.Errorf("platform: start the shell %s: %w", program, err)
	}
	sess := &shellSession{master: master, cmd: cmd, pid: cmd.Process.Pid, killAfter: s.killAfter, done: make(chan struct{})}
	go sess.wait()
	name, err := scopeName()
	if err == nil {
		err = s.scopes.StartScope(ctx, name, sess.pid)
	}
	if err != nil {
		sess.kill()
		return nil, fmt.Errorf("platform: move the shell into its scope: %w", err)
	}
	if _, err := open.Write([]byte("ok\n")); err != nil {
		sess.kill()
		return nil, fmt.Errorf("platform: start the shell %s in its scope: %w", program, err)
	}
	return sess, nil
}

func scopeName() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "darkpanel-console-" + hex.EncodeToString(b[:]) + ".scope", nil
}

// loginShell is root's login shell in passwd, else /bin/bash, else /bin/sh.
func loginShell(passwd string) string {
	var candidates []string
	if data, err := os.ReadFile(passwd); err == nil { //nolint:gosec // G304: the host's passwd file
		for line := range strings.Lines(string(data)) {
			f := strings.Split(strings.TrimRight(line, "\n"), ":")
			if len(f) == 7 && f[0] == "root" && f[2] == "0" {
				candidates = append(candidates, f[6])
				break
			}
		}
	}
	for _, p := range append(candidates, "/bin/bash") {
		if !strings.HasPrefix(p, "/") {
			continue
		}
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() && st.Mode()&0o111 != 0 {
			return p
		}
	}
	return "/bin/sh"
}

// environ is the shell's fixed environment; LANG comes from the host's
// locale file when it sets one.
func (s *Shell) environ(program string) []string {
	env := []string{"TERM=xterm-256color", "HOME=/root", "USER=root", "LOGNAME=root", "SHELL=" + program, "PATH=" + fixedPath}
	data, err := os.ReadFile(s.locale)
	if err != nil {
		return env
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		v, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), "LANG=")
		if v = strings.Trim(v, `"'`); ok && v != "" {
			return append(env, "LANG="+v)
		}
	}
	return env
}

// openPTY opens a new PTY pair: the master non-blocking, so a Close ends a
// pending Read, and the terminal for the shell.
func openPTY() (master, tty *os.File, err error) {
	fd, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("platform: open /dev/ptmx: %w", err)
	}
	n, err := ptyNumber(fd)
	if err == nil {
		err = unix.SetNonblock(fd, true)
	}
	if err != nil {
		_ = unix.Close(fd)
		return nil, nil, fmt.Errorf("platform: set the PTY up: %w", err)
	}
	path := fmt.Sprintf("/dev/pts/%d", n)
	sfd, err := unix.Open(path, unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		_ = unix.Close(fd)
		return nil, nil, fmt.Errorf("platform: open %s: %w", path, err)
	}
	return os.NewFile(uintptr(fd), "/dev/ptmx"), os.NewFile(uintptr(sfd), path), nil
}

// ptyNumber unlocks the PTY master fd and returns its terminal's number.
func ptyNumber(fd int) (uint32, error) {
	if err := unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0); err != nil {
		return 0, err
	}
	return unix.IoctlGetUint32(fd, unix.TIOCGPTN)
}

func setWinsize(master *os.File, cols, rows uint16) error {
	return control(master, func(fd int) error {
		return unix.IoctlSetWinsize(fd, unix.TIOCSWINSZ, &unix.Winsize{Row: rows, Col: cols})
	})
}

func control(f *os.File, op func(fd int) error) error {
	rc, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var opErr error
	if err := rc.Control(func(fd uintptr) { opErr = op(int(fd)) }); err != nil { //nolint:gosec // G115: a file descriptor fits in an int
		return err
	}
	return opErr
}

// shellSession is a running shell; the shell leads its own session and
// process group, both numbered pid.
type shellSession struct {
	master    *os.File
	cmd       *exec.Cmd
	pid       int
	killAfter time.Duration

	done chan struct{} // closed once the shell was reaped
	code int
	err  error

	closeOnce sync.Once
	closeErr  error
}

func (s *shellSession) wait() {
	err := s.cmd.Wait()
	s.code, s.err = -1, err
	if ps := s.cmd.ProcessState; ps != nil {
		s.err = nil
		if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			s.code = 128 + int(ws.Signal())
		} else {
			s.code = ps.ExitCode()
		}
	}
	close(s.done)
}

func (s *shellSession) exited() bool {
	select {
	case <-s.done:
		return true
	default:
		return false
	}
}

// Read reads the terminal's output; a hung-up or closed terminal reads
// io.EOF.
func (s *shellSession) Read(p []byte) (int, error) {
	n, err := s.master.Read(p)
	if errors.Is(err, syscall.EIO) || errors.Is(err, os.ErrClosed) {
		err = io.EOF
	}
	return n, err
}

func (s *shellSession) Write(p []byte) (int, error) { return s.master.Write(p) }

func (s *shellSession) Resize(cols, rows uint16) error { return setWinsize(s.master, cols, rows) }

func (s *shellSession) Wait() (int, error) {
	<-s.done
	return s.code, s.err
}

// Close sends SIGHUP to the shell's process group and closes the master, a
// hang-up as a dropped SSH connection is: the kernel sends the terminal's
// foreground group SIGHUP too, and nohup, tmux and screen survive it. A
// shell still running killAfter later is killed with its process group.
func (s *shellSession) Close() error {
	s.closeOnce.Do(func() {
		if s.exited() {
			s.closeErr = s.master.Close()
			return
		}
		_ = syscall.Kill(-s.pid, syscall.SIGHUP)
		s.closeErr = s.master.Close()
		t := time.NewTimer(s.killAfter)
		defer t.Stop()
		select {
		case <-s.done:
		case <-t.C:
			_ = syscall.Kill(-s.pid, syscall.SIGKILL)
			<-s.done
		}
	})
	return s.closeErr
}

// kill ends a shell that never reached its scope.
func (s *shellSession) kill() {
	_ = syscall.Kill(-s.pid, syscall.SIGKILL)
	<-s.done
	_ = s.master.Close()
}
