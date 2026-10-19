package platform

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"syscall"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// maxOutput is the most a program may write to either stream.
const maxOutput = 4 << 20

// environment is the whole environment of every program; nothing is inherited
// from the agent's own (ADR 0009).
var environment = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C.UTF-8", "LC_ALL=C.UTF-8"}

var errOutputTooLarge = errors.New("output too large")

// Runner is the core.Runner of the agent: it runs only allowlisted programs,
// by absolute path, with an argument vector and never through a shell.
type Runner struct {
	allowed        map[string]bool
	defaultTimeout time.Duration // applied when the caller's context has no deadline
}

var _ core.Runner = (*Runner)(nil)

// NewRunner returns a runner for the given absolute program paths. A relative
// or unclean path is a programming error and panics.
func NewRunner(allowlist []string) *Runner {
	r := &Runner{allowed: map[string]bool{}, defaultTimeout: 30 * time.Second}
	for _, p := range allowlist {
		if !filepath.IsAbs(p) || filepath.Clean(p) != p {
			panic(fmt.Sprintf("platform: allowlist entry %q is not a clean absolute path", p))
		}
		r.allowed[p] = true
	}
	return r
}

// ProductionAllowlist is the agent's allowlist. The installer runs
// useradd and userdel for the controller's system user; every systemd
// operation goes through D-Bus instead (Systemd).
func ProductionAllowlist() []string { return []string{"/usr/sbin/useradd", "/usr/sbin/userdel"} }

// cappedBuffer keeps at most limit bytes and cancels the run when a program
// writes more.
type cappedBuffer struct {
	buf    bytes.Buffer
	limit  int
	over   bool
	cancel context.CancelCauseFunc
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	if c.buf.Len()+len(p) > c.limit {
		c.over = true
		c.cancel(errOutputTooLarge)
		return 0, errOutputTooLarge
	}
	return c.buf.Write(p)
}

// Run runs cmd. A non-zero exit is reported in the result's ExitCode, not as
// an error; a refused path, a timeout or a failure to start is an error, and
// more than 4 MiB on either stream fails with exec.output_too_large.
func (r *Runner) Run(ctx context.Context, cmd core.Cmd) (core.CmdResult, error) {
	if !r.allowed[cmd.Path] {
		return core.CmdResult{}, fmt.Errorf("platform: %s is not in the exec allowlist", cmd.Path)
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.defaultTimeout)
		defer cancel()
	}
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	c := exec.CommandContext(ctx, cmd.Path, cmd.Args...) //nolint:gosec // G204: an allowlisted absolute path and an argument vector, never a shell
	c.Env = slices.Clone(environment)
	c.Dir = "/"
	if len(cmd.Stdin) > 0 {
		c.Stdin = bytes.NewReader(cmd.Stdin)
	}
	stdout := &cappedBuffer{limit: maxOutput, cancel: cancel}
	stderr := &cappedBuffer{limit: maxOutput, cancel: cancel}
	c.Stdout, c.Stderr = stdout, stderr
	// The program leads its own process group so that the timeout and the
	// output cap stop every process it started, not only the program itself.
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error { return syscall.Kill(-c.Process.Pid, syscall.SIGKILL) }
	// A descendant that left the group and still holds the output pipes would
	// otherwise keep Run waiting after the group was killed.
	c.WaitDelay = time.Second

	err := c.Run()
	if stdout.over || stderr.over {
		return core.CmdResult{}, errcode.New(errcode.ExecOutputTooLarge, cmd.Path+" wrote more than 4 MiB")
	}
	res := core.CmdResult{Stdout: stdout.buf.Bytes(), Stderr: stderr.buf.Bytes()}
	if c.ProcessState != nil {
		res.ExitCode = c.ProcessState.ExitCode()
	}
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return res, nil
	// A program killed by the context also ends in an ExitError; the context
	// must win so the timeout is not hidden behind exit code -1.
	case ctx.Err() != nil:
		return res, fmt.Errorf("platform: %s: %w", cmd.Path, context.Cause(ctx))
	case errors.As(err, &exitErr):
		return res, nil
	}
	return res, fmt.Errorf("platform: run %s: %w", cmd.Path, err)
}
