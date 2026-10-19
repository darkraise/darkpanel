package platform

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

const (
	helperArg     = "darkpanel-exec-helper"
	helperForkArg = "darkpanel-exec-helper-fork"
)

// TestExecHelperProcess is not a test: Run starts the test binary with
// helperArg so a program can write more than 4 MiB to stderr, which no
// standard program does without a shell, or with helperForkArg so a program
// leaves a descendant running and prints its PID.
func TestExecHelperProcess(*testing.T) {
	if os.Args[len(os.Args)-1] == helperForkArg {
		child := exec.Command("/usr/bin/sleep", "30")
		child.Stdout = os.Stdout
		if err := child.Start(); err != nil {
			os.Exit(2)
		}
		_, _ = os.Stdout.WriteString(strconv.Itoa(child.Process.Pid) + "\n")
		time.Sleep(30 * time.Second)
		os.Exit(0)
	}
	if os.Args[len(os.Args)-1] != helperArg {
		return
	}
	_, _ = os.Stderr.Write(bytes.Repeat([]byte("e"), maxOutput+1))
	os.Exit(0)
}

func TestRunRefusesUnlistedPrograms(t *testing.T) {
	r := NewRunner([]string{"/usr/bin/printf"})
	for _, path := range []string{"/usr/bin/env", "printf", "/usr/bin/../bin/printf"} {
		if _, err := r.Run(context.Background(), core.Cmd{Path: path}); err == nil {
			t.Errorf("Run(%q) succeeded, want a refusal", path)
		}
	}
}

func TestNewRunnerPanicsOnRelativePath(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("NewRunner accepted a relative path")
		}
	}()
	NewRunner([]string{"printf"})
}

func TestProductionAllowlist(t *testing.T) {
	want := []string{"/usr/sbin/useradd", "/usr/sbin/userdel"}
	if got := ProductionAllowlist(); !slices.Equal(got, want) {
		t.Errorf("ProductionAllowlist() = %v, want %v", got, want)
	}
}

func TestRunUsesFixedEnvironment(t *testing.T) {
	t.Setenv("DARKPANEL_LEAK", "1")
	res, err := NewRunner([]string{"/usr/bin/env"}).Run(context.Background(), core.Cmd{Path: "/usr/bin/env"})
	if err != nil {
		t.Fatal(err)
	}
	want := "PATH=/usr/sbin:/usr/bin:/sbin:/bin\nLANG=C.UTF-8\nLC_ALL=C.UTF-8\n"
	if string(res.Stdout) != want {
		t.Errorf("environment = %q, want %q", res.Stdout, want)
	}
}

func TestRunPassesArgumentsAndStdin(t *testing.T) {
	r := NewRunner([]string{"/usr/bin/printf", "/usr/bin/cat"})
	res, err := r.Run(context.Background(), core.Cmd{Path: "/usr/bin/printf", Args: []string{"%s|", "a b", "--x", "$(id)"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(res.Stdout); got != "a b|--x|$(id)|" {
		t.Errorf("printf output = %q", got)
	}
	res, err = r.Run(context.Background(), core.Cmd{Path: "/usr/bin/cat", Stdin: []byte("hello")})
	if err != nil || string(res.Stdout) != "hello" {
		t.Errorf("cat = %q, %v; want hello", res.Stdout, err)
	}
}

func TestRunReportsExitCodeAndStderr(t *testing.T) {
	r := NewRunner([]string{"/usr/bin/false", "/usr/bin/cat"})
	res, err := r.Run(context.Background(), core.Cmd{Path: "/usr/bin/false"})
	if err != nil || res.ExitCode != 1 {
		t.Errorf("false = exit %d, %v; want exit 1 and no error", res.ExitCode, err)
	}
	res, err = r.Run(context.Background(), core.Cmd{Path: "/usr/bin/cat", Args: []string{"--", "/nonexistent-darkpanel"}})
	if err != nil || res.ExitCode == 0 || !strings.Contains(string(res.Stderr), "nonexistent-darkpanel") {
		t.Errorf("cat missing file = exit %d, stderr %q, %v", res.ExitCode, res.Stderr, err)
	}
}

func TestRunTimesOut(t *testing.T) {
	r := NewRunner([]string{"/usr/bin/sleep"})
	r.defaultTimeout = 100 * time.Millisecond
	start := time.Now()
	_, err := r.Run(context.Background(), core.Cmd{Path: "/usr/bin/sleep", Args: []string{"10"}})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("no deadline: err = %v, want the default timeout", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	r.defaultTimeout = time.Hour
	_, err = r.Run(ctx, core.Cmd{Path: "/usr/bin/sleep", Args: []string{"10"}})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("caller deadline: err = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("timeouts took %v", elapsed)
	}
}

func TestRunStopsDescendants(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	r := NewRunner([]string{self})
	r.defaultTimeout = 500 * time.Millisecond
	start := time.Now()
	res, err := r.Run(context.Background(), core.Cmd{Path: self, Args: []string{"-test.run=^TestExecHelperProcess$", "--", helperForkArg}})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want the default timeout", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("Run took %v", elapsed)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(res.Stdout)))
	if err != nil {
		t.Fatalf("helper printed %q, want the descendant's PID", res.Stdout)
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if descendantGone(pid) {
			return
		}
	}
	t.Errorf("descendant %d still runs after Run returned", pid)
}

// descendantGone reports whether pid no longer runs. A killed descendant
// whose parent died first may stay a zombie when nothing reaps orphans.
func descendantGone(pid int) bool {
	if errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
		return true
	}
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	s := string(stat)
	return strings.HasPrefix(s[strings.LastIndex(s, ")")+1:], " Z")
}

func TestRunCapsOutput(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	r := NewRunner([]string{"/usr/bin/cat", "/usr/bin/yes", self})
	ctx := context.Background()
	res, err := r.Run(ctx, core.Cmd{Path: "/usr/bin/cat", Stdin: bytes.Repeat([]byte("x"), maxOutput)})
	if err != nil || len(res.Stdout) != maxOutput {
		t.Errorf("exactly 4 MiB: %d bytes, %v; want it accepted", len(res.Stdout), err)
	}
	cmds := map[string]core.Cmd{
		"stdout one byte over": {Path: "/usr/bin/cat", Stdin: bytes.Repeat([]byte("x"), maxOutput+1)},
		"stdout endless":       {Path: "/usr/bin/yes"},
		"stderr":               {Path: self, Args: []string{"-test.run=^TestExecHelperProcess$", "--", helperArg}},
	}
	for name, cmd := range cmds {
		_, err := r.Run(ctx, cmd)
		var coded *errcode.Error
		if !errors.As(err, &coded) || coded.Code != errcode.ExecOutputTooLarge {
			t.Errorf("%s: err = %v, want %s", name, err, errcode.ExecOutputTooLarge)
		}
	}
}
