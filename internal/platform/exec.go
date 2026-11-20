package platform

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"
	"unicode"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// maxOutput is the most a program may write to either stream.
const maxOutput = 4 << 20

// maxExecArg is the longest argument the runner passes on.
const maxExecArg = 4096

// environment is the base environment of every program; a rule adds its Env
// and nothing is inherited from the agent's own (ADR 0009).
var environment = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C.UTF-8", "LC_ALL=C.UTF-8"}

var errOutputTooLarge = errors.New("output too large")

// ExecRule allows a program to run with the argument vectors Match accepts.
type ExecRule struct {
	Path  string                    // absolute, clean
	Match func(args []string) error // nil error: the vector is allowed
	Env   []string                  // KEY=VALUE pairs appended to the base environment; nothing else is inherited
	// KeepTail keeps the last bytes of each stream, up to the rule's cap,
	// instead of cancelling the program: a mutating apt or dpkg run killed
	// for its output would leave dpkg interrupted.
	KeepTail bool
	// MaxOutput is the rule's cap on each stream; 0 means maxOutput. A rule
	// raises it only for a read whose full output it needs.
	MaxOutput int
}

// Runner is the core.Runner of the agent: it runs only programs a rule
// allows, by absolute path, with an argument vector a rule of that path
// accepts, and never through a shell.
type Runner struct {
	rules          map[string][]ExecRule
	defaultTimeout time.Duration // applied when the caller's context has no deadline
}

var _ core.Runner = (*Runner)(nil)

// NewRunner returns a runner for rules; several rules may share a path. A
// relative or unclean path, or a rule without Match, is a programming error
// and panics.
func NewRunner(rules []ExecRule) *Runner {
	r := &Runner{rules: map[string][]ExecRule{}, defaultTimeout: 30 * time.Second}
	for _, rule := range rules {
		if !filepath.IsAbs(rule.Path) || filepath.Clean(rule.Path) != rule.Path {
			panic(fmt.Sprintf("platform: exec rule path %q is not a clean absolute path", rule.Path))
		}
		if rule.Match == nil {
			panic(fmt.Sprintf("platform: exec rule for %s has no Match", rule.Path))
		}
		r.rules[rule.Path] = append(r.rules[rule.Path], rule)
	}
	return r
}

// match returns the environment of the first rule of path that allows args.
func (r *Runner) match(path string, args []string) ([]string, error) {
	rule, err := r.matchRule(path, args)
	if err != nil {
		return nil, err
	}
	return append(slices.Clone(environment), rule.Env...), nil
}

// matchRule returns the first rule of path that allows args. Its errors name
// the path and an argument's position, never an argument, which may carry a
// secret.
func (r *Runner) matchRule(path string, args []string) (ExecRule, error) {
	rules, ok := r.rules[path]
	if !ok {
		return ExecRule{}, fmt.Errorf("platform: %s is not in the exec allowlist", path)
	}
	for i, a := range args {
		if len(a) > maxExecArg {
			return ExecRule{}, fmt.Errorf("platform: %s: argument %d is longer than %d bytes", path, i, maxExecArg)
		}
		if strings.ContainsFunc(a, unicode.IsControl) {
			return ExecRule{}, fmt.Errorf("platform: %s: argument %d contains a control character", path, i)
		}
	}
	for _, rule := range rules {
		if rule.Match(args) == nil {
			return rule, nil
		}
	}
	return ExecRule{}, fmt.Errorf("platform: %s: argument vector not allowed", path)
}

// cappedBuffer keeps at most limit bytes. When a program writes more, it
// cancels the run, or with keepTail drops the oldest bytes.
type cappedBuffer struct {
	buf      bytes.Buffer
	limit    int
	keepTail bool
	over     bool
	cancel   context.CancelCauseFunc
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	if c.keepTail {
		n := len(p)
		if n >= c.limit {
			c.buf.Reset()
			p = p[n-c.limit:]
		} else if drop := c.buf.Len() + n - c.limit; drop > 0 {
			c.buf.Next(drop)
		}
		c.buf.Write(p)
		return n, nil
	}
	if c.buf.Len()+len(p) > c.limit {
		c.over = true
		c.cancel(errOutputTooLarge)
		return 0, errOutputTooLarge
	}
	return c.buf.Write(p)
}

// Run runs cmd. A non-zero exit is reported in the result's ExitCode, not as
// an error; a refused path, a timeout or a failure to start is an error, and
// more than the rule's cap (4 MiB by default) on either stream fails with
// exec.output_too_large, unless the rule keeps the tail.
func (r *Runner) Run(ctx context.Context, cmd core.Cmd) (core.CmdResult, error) {
	rule, err := r.matchRule(cmd.Path, cmd.Args)
	if err != nil {
		return core.CmdResult{}, err
	}
	env := append(slices.Clone(environment), rule.Env...)
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.defaultTimeout)
		defer cancel()
	}
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	c := exec.CommandContext(ctx, cmd.Path, cmd.Args...) //nolint:gosec // G204: an allowlisted absolute path and an argument vector, never a shell
	c.Env = env
	c.Dir = "/"
	if len(cmd.Stdin) > 0 {
		c.Stdin = bytes.NewReader(cmd.Stdin)
	}
	limit := maxOutput
	if rule.MaxOutput > 0 {
		limit = rule.MaxOutput
	}
	stdout := &cappedBuffer{limit: limit, keepTail: rule.KeepTail, cancel: cancel}
	stderr := &cappedBuffer{limit: limit, keepTail: rule.KeepTail, cancel: cancel}
	c.Stdout, c.Stderr = stdout, stderr
	// The program leads its own process group so that the timeout and the
	// output cap stop every process it started, not only the program itself.
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error { return syscall.Kill(-c.Process.Pid, syscall.SIGKILL) }
	// A descendant that left the group and still holds the output pipes would
	// otherwise keep Run waiting after the group was killed.
	c.WaitDelay = time.Second

	err = c.Run()
	if stdout.over || stderr.over {
		return core.CmdResult{}, errcode.New(errcode.ExecOutputTooLarge, fmt.Sprintf("%s wrote more than %d MiB", cmd.Path, limit>>20))
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
