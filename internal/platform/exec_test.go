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

// execAny allows every argument vector of each path.
func execAny(paths ...string) []ExecRule {
	rules := make([]ExecRule, 0, len(paths))
	for _, p := range paths {
		rules = append(rules, ExecRule{Path: p, Match: func([]string) error { return nil }})
	}
	return rules
}

func TestRunRefusesUnlistedPrograms(t *testing.T) {
	r := NewRunner(execAny("/usr/bin/printf"))
	for _, path := range []string{"/usr/bin/env", "printf", "/usr/bin/../bin/printf"} {
		if _, err := r.Run(context.Background(), core.Cmd{Path: path}); err == nil {
			t.Errorf("Run(%q) succeeded, want a refusal", path)
		}
	}
}

func TestNewRunnerPanicsOnBadRules(t *testing.T) {
	for name, rule := range map[string]ExecRule{
		"relative path": {Path: "printf", Match: func([]string) error { return nil }},
		"unclean path":  {Path: "/usr/bin/../bin/printf", Match: func([]string) error { return nil }},
		"no Match":      {Path: "/usr/bin/printf"},
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Error("NewRunner accepted the rule")
				}
			}()
			NewRunner([]ExecRule{rule})
		})
	}
}

func TestProductionAllowlist(t *testing.T) {
	var got []string
	for _, rule := range ProductionAllowlist() {
		got = append(got, rule.Path)
	}
	if want := []string{"/usr/sbin/useradd", "/usr/sbin/userdel"}; !slices.Equal(got, want) {
		t.Errorf("ProductionAllowlist() paths = %v, want %v", got, want)
	}
}

func TestRunnerAppliesRules(t *testing.T) {
	matched := 0
	r := NewRunner([]ExecRule{
		{Path: "/usr/bin/printf", Match: func(args []string) error {
			matched++
			if len(args) == 2 && args[0] == "%s" {
				return nil
			}
			return errors.New("not a %s vector")
		}},
		{Path: "/usr/bin/printf", Match: func(args []string) error {
			if slices.Equal(args, []string{"two"}) {
				return nil
			}
			return errors.New("not the second rule's vector")
		}},
	})
	tests := []struct {
		name    string
		args    []string
		out     string
		wantErr string
		matches bool // Match ran
	}{
		{"an allowed vector", []string{"%s", "hello"}, "hello", "", true},
		{"an argument of 4096 bytes", []string{"%s", strings.Repeat("a", 4096)}, strings.Repeat("a", 4096), "", true},
		{"a second rule of the path", []string{"two"}, "two", "", true},
		{"a refused vector", []string{"%s|", "s3cret-arg"}, "", "platform: /usr/bin/printf: argument vector not allowed", true},
		{"an argument of 4097 bytes", []string{"%s", strings.Repeat("a", 4097)}, "",
			"platform: /usr/bin/printf: argument 1 is longer than 4096 bytes", false},
		{"a NUL", []string{"%s", "s3cret\x00arg"}, "", "platform: /usr/bin/printf: argument 1 contains a control character", false},
		{"a newline", []string{"%s", "s3cret\narg"}, "", "platform: /usr/bin/printf: argument 1 contains a control character", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matched = 0
			res, err := r.Run(context.Background(), core.Cmd{Path: "/usr/bin/printf", Args: tt.args})
			if tt.wantErr == "" {
				if err != nil || string(res.Stdout) != tt.out {
					t.Fatalf("Run = %q, %v; want %q", res.Stdout, err, tt.out)
				}
			} else if err == nil || err.Error() != tt.wantErr {
				t.Fatalf("Run error %v, want %q", err, tt.wantErr)
			}
			if (matched > 0) != tt.matches {
				t.Errorf("Match ran %d times, want it to run: %v", matched, tt.matches)
			}
		})
	}
}

func TestRunnerEnvironment(t *testing.T) {
	t.Setenv("DARKPANEL_LEAK", "1")
	t.Setenv("HOME", "/root/darkpanel-leak")
	base := "PATH=/usr/sbin:/usr/bin:/sbin:/bin\nLANG=C.UTF-8\nLC_ALL=C.UTF-8\n"
	tests := []struct {
		name string
		env  []string
		want string
	}{
		{"the base environment", nil, base},
		{"a rule's environment", []string{"A=1"}, base + "A=1\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewRunner([]ExecRule{{Path: "/usr/bin/env", Match: func([]string) error { return nil }, Env: tt.env}})
			res, err := r.Run(context.Background(), core.Cmd{Path: "/usr/bin/env"})
			if err != nil || string(res.Stdout) != tt.want {
				t.Errorf("environment = %q, %v; want %q", res.Stdout, err, tt.want)
			}
		})
	}
}

func TestRunPassesArgumentsAndStdin(t *testing.T) {
	r := NewRunner(execAny("/usr/bin/printf", "/usr/bin/cat"))
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
	r := NewRunner(execAny("/usr/bin/false", "/usr/bin/cat"))
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
	r := NewRunner(execAny("/usr/bin/sleep"))
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
	r := NewRunner(execAny(self))
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
	r := NewRunner(execAny("/usr/bin/cat", "/usr/bin/yes", self))
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
