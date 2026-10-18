package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/darkraise/darkpanel/internal/core"
)

// Annotations on generated commands, naming the action or resource each
// one serves; FindCommand reads them.
const (
	AnnotationAction   = "darkpanel.action"
	AnnotationResource = "darkpanel.resource"
)

// Options configure NewRoot. Registry is required; production passes
// registry.Build(). A nil field takes the process's own: os.Stdin,
// os.Stdout, os.Stderr, os.Getenv, os.UserHomeDir, a terminal check of
// standard input and a no-echo read from it.
type Options struct {
	Registry *core.Registry
	// Commands are hand-written commands the assembly adds, such as serve
	// and version; no generated command may take their names.
	Commands    []*cobra.Command
	Stdin       io.Reader
	Stdout      io.Writer
	Stderr      io.Writer
	Getenv      func(string) string
	UserHomeDir func() (string, error)
	// IsTerminal reports whether standard input is an interactive terminal.
	IsTerminal func() bool
	// ReadSecret reads one line from the terminal without echo.
	ReadSecret func() (string, error)
}

// app is the state the generated commands share.
type app struct {
	opts Options
	term *terminal
}

// NewRoot returns the darkpanel command tree: one command per action and
// resource of opts.Registry (spec section 15) plus the hand-written ones.
// It fails when two commands would clash. Errors of the returned tree go
// to PrintError and ExitCode.
func NewRoot(opts Options) (*cobra.Command, error) {
	if opts.Registry == nil {
		return nil, errors.New("cli: Options.Registry is required")
	}
	opts = withDefaults(opts)
	a := &app{opts: opts, term: &terminal{in: bufio.NewReader(opts.Stdin), out: opts.Stderr,
		isTerm: opts.IsTerminal(), readSecret: opts.ReadSecret}}
	root := &cobra.Command{
		Use:           "darkpanel",
		Short:         "Single-binary fleet manager for unmanaged Linux servers",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetIn(opts.Stdin)
	root.SetOut(opts.Stdout)
	root.SetErr(opts.Stderr)
	reserved := slices.Clone(handWritten)
	for _, c := range opts.Commands {
		reserved = append(reserved, c.Name())
	}
	root.AddCommand(opts.Commands...)
	es, err := entries(opts.Registry, reserved)
	if err != nil {
		return nil, fmt.Errorf("cli: %w", err)
	}
	for _, e := range es {
		var leaf *cobra.Command
		if e.Resource != nil {
			leaf, err = a.resourceCommand(e)
		} else {
			leaf, err = a.actionCommand(e)
		}
		if err != nil {
			return nil, fmt.Errorf("cli: %w", err)
		}
		if leaf != nil {
			group(root, e.Path[:len(e.Path)-1]).AddCommand(leaf)
		}
	}
	markRuns(root)
	return root, nil
}

func withDefaults(o Options) Options {
	if o.Stdin == nil {
		o.Stdin = os.Stdin
	}
	if o.Stdout == nil {
		o.Stdout = os.Stdout
	}
	if o.Stderr == nil {
		o.Stderr = os.Stderr
	}
	if o.Getenv == nil {
		o.Getenv = os.Getenv
	}
	if o.UserHomeDir == nil {
		o.UserHomeDir = os.UserHomeDir
	}
	stdin, isFile := o.Stdin.(*os.File)
	if o.IsTerminal == nil {
		o.IsTerminal = func() bool { return isFile && term.IsTerminal(int(stdin.Fd())) } //nolint:gosec // G115: a file descriptor fits in an int
	}
	if o.ReadSecret == nil {
		o.ReadSecret = func() (string, error) {
			if !isFile {
				return "", errors.New("standard input is not a terminal")
			}
			b, err := term.ReadPassword(int(stdin.Fd())) //nolint:gosec // G115: a file descriptor fits in an int
			return string(b), err
		}
	}
	return o
}

// group returns the command at path below root, creating the missing
// command groups. A group run with an unknown subcommand is a usage error;
// run alone it prints its help.
func group(root *cobra.Command, path []string) *cobra.Command {
	cur := root
	for i, word := range path {
		var next *cobra.Command
		for _, c := range cur.Commands() {
			if c.Name() == word {
				next = c
			}
		}
		if next == nil {
			next = &cobra.Command{
				Use:   word,
				Short: fmt.Sprintf("%s commands", strings.Join(path[:i+1], " ")),
				RunE: func(cmd *cobra.Command, args []string) error {
					if len(args) > 0 {
						return usagef("unknown command %q for %q", args[0], cmd.CommandPath())
					}
					return cmd.Help()
				},
			}
			cur.AddCommand(next)
		}
		cur = next
	}
	return cur
}

// FindCommand returns the command of root generated for an action or
// resource name, the way the assembly's CLI parity test looks one up.
func FindCommand(root *cobra.Command, name string) (*cobra.Command, bool) {
	cmd, rest, err := root.Find(CommandPath(name))
	if err != nil || len(rest) > 0 {
		return nil, false
	}
	if cmd.Annotations[AnnotationAction] != name && cmd.Annotations[AnnotationResource] != name {
		return nil, false
	}
	return cmd, true
}

// actionCommand builds the command of an action: its input flags, the
// connection flags, --host for an agent-side action, --json, and for a
// mutating action the flags of the mutating flow.
func (a *app) actionCommand(e entry) (*cobra.Command, error) {
	def := *e.Action
	fields, err := inputFields(def.InputType)
	if err != nil {
		return nil, err
	}
	secrets, err := secretOutputs(def.OutputType)
	if err != nil {
		return nil, err
	}
	cmd := &cobra.Command{
		Use:         e.Path[len(e.Path)-1],
		Short:       def.Spec.Summary,
		Args:        cobra.NoArgs,
		Annotations: map[string]string{AnnotationAction: e.Name},
	}
	addInputFlags(cmd, fields)
	addConnFlags(cmd, def.Spec.BreakGlass, def.Spec.Side == core.SideAgent)
	cmd.Flags().Bool("json", false, "print the raw JSON output")
	if def.Spec.Class == core.ClassRead {
		cmd.RunE = func(cmd *cobra.Command, _ []string) error { return a.runRead(cmd, def, fields, secrets) }
		return cmd, nil
	}
	addMutatingFlags(cmd, def.Spec.Class)
	cmd.RunE = func(cmd *cobra.Command, _ []string) error { return a.runMutating(cmd, def, fields, secrets) }
	return cmd, nil
}
