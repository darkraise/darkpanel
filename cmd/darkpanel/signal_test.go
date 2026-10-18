package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/cli"
	"github.com/darkraise/darkpanel/internal/controller/server"
)

const hungServeEnv = "DARKPANEL_TEST_HUNG_SERVE"

// TestHungServe is the child of TestSecondInterruptKillsServe: a serve whose
// shutdown never finishes.
func TestHungServe(t *testing.T) {
	if os.Getenv(hungServeEnv) != "1" {
		t.Skip("runs only as the child of TestSecondInterruptKillsServe")
	}
	geteuid = func() int { return 1000 }
	serveRun = func(ctx context.Context, _ server.Options) error {
		fmt.Println("ready")
		<-ctx.Done()
		fmt.Println("stopping")
		time.Sleep(time.Hour)
		return nil
	}
	run([]string{"serve", "--dev"}, cli.Options{})
}

// TestSecondInterruptKillsServe shows that once the first interrupt has
// started the shutdown, another one ends the process by the default action.
func TestSecondInterruptKillsServe(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestHungServe$", "-test.count=1") //nolint:gosec // G204: the test binary runs itself
	child.Env = append(os.Environ(), hungServeEnv+"=1", "XDG_STATE_HOME="+t.TempDir(), "XDG_RUNTIME_DIR=")
	stdout, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	lines := bufio.NewScanner(stdout)
	waitFor := func(want string) {
		t.Helper()
		for lines.Scan() {
			if strings.TrimSpace(lines.Text()) == want {
				return
			}
		}
		t.Fatalf("the child ended before printing %q", want)
	}
	waitFor("ready")
	if err := child.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	waitFor("stopping")
	exited := make(chan error, 1)
	go func() { exited <- child.Wait() }()
	// The handler is released just after the context ends, so interrupt
	// until the default action applies.
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case err := <-exited:
			var exit *exec.ExitError
			if !errors.As(err, &exit) {
				t.Fatalf("child: %v, want it killed by SIGINT", err)
			}
			if ws, ok := exit.Sys().(syscall.WaitStatus); !ok || !ws.Signaled() || ws.Signal() != syscall.SIGINT {
				t.Fatalf("child ended with %v, want it killed by SIGINT", exit)
			}
			return
		case <-tick.C:
			_ = child.Process.Signal(os.Interrupt)
		case <-ctx.Done():
			t.Fatal("a second interrupt did not end the hung serve")
		}
	}
}
