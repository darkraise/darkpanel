// Package cli is the darkpanel command line (spec section 15): commands
// generated from the module registry, the hand-written change, controller
// and user enrollment commands, and the HTTP and controller-socket clients
// they share.
package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode"

	"github.com/spf13/cobra"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// Exit codes (spec section 15).
const (
	ExitOK      = 0
	ExitError   = 1
	ExitUsage   = 2
	ExitRefused = 3 // a guard failed or a prompt was declined
	ExitAuth    = 4
)

// usageError is a command line the CLI cannot act on: a missing or invalid
// flag, argument or input.
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

func usagef(format string, args ...any) error {
	return &usageError{msg: fmt.Sprintf(format, args...)}
}

// refusedError is a change the CLI did not apply: a failing guard, a
// declined prompt or a confirmation phrase that does not match.
type refusedError struct{ msg string }

func (e *refusedError) Error() string { return e.msg }

func refusedf(format string, args ...any) error {
	return &refusedError{msg: fmt.Sprintf(format, args...)}
}

// problemError is a refusal the API answered as RFC 9457 problem details
// (spec section 12).
type problemError struct {
	Status int
	Code   errcode.Code
	Detail string
	Fields []errcode.FieldError
}

func (e *problemError) Error() string { return fmt.Sprintf("%s (%s)", e.Detail, e.Code) }

// runError marks an error that a command's run returned. Cobra's own flag,
// argument and unknown-command errors are never marked, which is how
// ExitCode tells them apart as usage errors.
type runError struct{ err error }

func (e *runError) Error() string { return e.err.Error() }

func (e *runError) Unwrap() error { return e.err }

func ranError(err error) error {
	if err == nil {
		return nil
	}
	return &runError{err: err}
}

// markRuns wraps the RunE of root and of every command below it, so their
// errors are marked as run errors.
func markRuns(root *cobra.Command) {
	if run := root.RunE; run != nil {
		root.RunE = func(cmd *cobra.Command, args []string) error { return ranError(run(cmd, args)) }
	}
	for _, c := range root.Commands() {
		markRuns(c)
	}
}

// ExitCode is the process exit code for err, the error of a command tree
// that NewRoot built: 0 for nil, 2 for a usage error (including every error
// cobra raises while parsing the command line), 3 for a refused change or an
// API refusal with plan.guard_failed, 4 when the API refused with an auth.*
// code, and 1 for any other error.
func ExitCode(err error) int {
	var (
		usage   *usageError
		refused *refusedError
		problem *problemError
		run     *runError
	)
	switch {
	case err == nil:
		return ExitOK
	case errors.As(err, &usage):
		return ExitUsage
	case errors.As(err, &refused):
		return ExitRefused
	case errors.As(err, &problem) && problem.Code == errcode.PlanGuardFailed:
		return ExitRefused
	case errors.As(err, &problem) && strings.HasPrefix(string(problem.Code), "auth."):
		return ExitAuth
	case errors.As(err, &run):
		return ExitError
	default:
		return ExitUsage
	}
}

// PrintError writes err as "darkpanel: <message>", followed by one indented
// line per field error of an API refusal, all sanitized. A nil err prints
// nothing.
func PrintError(w io.Writer, err error) {
	if err == nil {
		return
	}
	_, _ = fmt.Fprintf(w, "darkpanel: %s\n", sanitize(err.Error()))
	var problem *problemError
	if errors.As(err, &problem) {
		for _, f := range problem.Fields {
			_, _ = fmt.Fprintf(w, "  %s: %s\n", sanitize(f.Path), sanitize(f.Message))
		}
	}
}

// sanitize makes server-supplied text safe to write to a terminal: every
// control character (C0 including ESC, DEL, C1), the line and paragraph
// separators and the bidirectional controls become a visible escape, \xNN
// below U+0080 and \uNNNN above, so no answer can move the cursor, recolour
// the screen or reorder what the operator reads. Every string the CLI
// writes for people passes through it; --json output does not.
func sanitize(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r < 0x80 && unicode.IsControl(r):
			fmt.Fprintf(&b, "\\x%02x", r)
		case unicode.IsControl(r) || isFormatHazard(r):
			fmt.Fprintf(&b, "\\u%04x", r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func isFormatHazard(r rune) bool {
	return r == 0x2028 || r == 0x2029 || r == 0x200e || r == 0x200f ||
		(r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069)
}
