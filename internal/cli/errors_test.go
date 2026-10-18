package cli

import (
	"bytes"
	"errors"
	"fmt"
	"testing"

	"github.com/spf13/cobra"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

func TestExitCode(t *testing.T) {
	problem := func(code errcode.Code) error {
		return &problemError{Status: code.Status(), Code: code, Detail: code.Title()}
	}
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, ExitOK},
		{"usage", usagef("required flag --name not set"), ExitUsage},
		{"refused", refusedf("not applied"), ExitRefused},
		{"auth.required", ranError(problem(errcode.AuthRequired)), ExitAuth},
		{"auth.forbidden", ranError(problem(errcode.Forbidden)), ExitAuth},
		{"guard failed at apply", ranError(problem(errcode.PlanGuardFailed)), ExitRefused},
		{"other problem", ranError(problem(errcode.NotFound)), ExitError},
		{"run error", ranError(errors.New("dial tcp: connection refused")), ExitError},
		{"usage inside a run", ranError(fmt.Errorf("input: %w", usagef("bad --input"))), ExitUsage},
		{"refusal inside a run", ranError(refusedf("a guard failed")), ExitRefused},
		{"unmarked error from cobra", errors.New(`unknown flag: --bogus`), ExitUsage},
	}
	for _, c := range cases {
		if got := ExitCode(c.err); got != c.want {
			t.Errorf("%s: ExitCode = %d, want %d", c.name, got, c.want)
		}
	}
}

func TestExitCodeOfCobraErrors(t *testing.T) {
	newRoot := func() *cobra.Command {
		root := &cobra.Command{Use: "darkpanel", SilenceUsage: true, SilenceErrors: true}
		fail := &cobra.Command{Use: "fail", Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error {
			return errors.New("boom")
		}}
		fail.Flags().Int("count", 0, "a number")
		ok := &cobra.Command{Use: "ok", Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error { return nil }}
		root.AddCommand(fail, ok)
		markRuns(root)
		return root
	}
	cases := []struct {
		args []string
		want int
	}{
		{[]string{"ok"}, ExitOK},
		{[]string{"fail"}, ExitError},
		{[]string{"fail", "--bogus"}, ExitUsage},
		{[]string{"fail", "--count", "many"}, ExitUsage},
		{[]string{"fail", "extra"}, ExitUsage},
		{[]string{"nosuch"}, ExitUsage},
	}
	for _, c := range cases {
		root := newRoot()
		root.SetArgs(c.args)
		root.SetOut(new(bytes.Buffer))
		if got := ExitCode(root.Execute()); got != c.want {
			t.Errorf("darkpanel %v: exit %d, want %d", c.args, got, c.want)
		}
	}
}

func TestPrintError(t *testing.T) {
	var b bytes.Buffer
	PrintError(&b, ranError(&problemError{Status: 422, Code: errcode.InvalidInput, Detail: "invalid input",
		Fields: []errcode.FieldError{{Path: "/name", Message: "required"}, {Path: "/port", Message: "must be at least 1"}}}))
	want := "darkpanel: invalid input (request.invalid_input)\n  /name: required\n  /port: must be at least 1\n"
	if b.String() != want {
		t.Errorf("problem printed as\n%q\nwant\n%q", b.String(), want)
	}
	b.Reset()
	PrintError(&b, ranError(errors.New("dial tcp 127.0.0.1:8080: connection refused")))
	if want := "darkpanel: dial tcp 127.0.0.1:8080: connection refused\n"; b.String() != want {
		t.Errorf("plain error printed as %q, want %q", b.String(), want)
	}
	b.Reset()
	PrintError(&b, nil)
	if b.Len() != 0 {
		t.Errorf("nil error printed %q", b.String())
	}
}

func TestSanitize(t *testing.T) {
	cases := map[string]string{
		"plain text, ünïcode":            "plain text, ünïcode",
		"\x1b[31mred\x1b[0m":             `\x1b[31mred\x1b[0m`,
		"a\tb\nc\rd\x00e\x7f":            `a\x09b\x0ac\x0dd\x00e\x7f`,
		"c1 \u0085 \u009b":               `c1 \u0085 \u009b`,
		"evil\u202Etxt.exe":              `evil\u202etxt.exe`,
		"\u200e\u200f\u202a\u2066\u2069": `\u200e\u200f\u202a\u2066\u2069`,
		"line\u2028para\u2029":           `line\u2028para\u2029`,
		"alm\u061cx":                     `alm\u061cx`,
	}
	for in, want := range cases {
		if got := sanitize(in); got != want {
			t.Errorf("sanitize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPrintErrorSanitizes(t *testing.T) {
	var b bytes.Buffer
	PrintError(&b, ranError(&problemError{Status: 422, Code: errcode.InvalidInput, Detail: "bad \x1b[2J input\u202E",
		Fields: []errcode.FieldError{{Path: "/n\x1b]0;x\x07", Message: "no\u2066pe"}}}))
	want := `darkpanel: bad \x1b[2J input\u202e (request.invalid_input)` + "\n" + `  /n\x1b]0;x\x07: no\u2066pe` + "\n"
	if b.String() != want {
		t.Errorf("printed %q, want %q", b.String(), want)
	}
	b.Reset()
	PrintError(&b, ranError(errors.New("change c1 failed: disk \x1b[1mfull\u202e")))
	if want := `darkpanel: change c1 failed: disk \x1b[1mfull\u202e` + "\n"; b.String() != want {
		t.Errorf("printed %q, want %q", b.String(), want)
	}
}
