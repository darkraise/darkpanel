package loaded

import (
	"context"
	"io/fs"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)

// fz7Runner is a core.Runner that answers every command alike.
type fz7Runner struct {
	res core.CmdResult
	err error
}

func (r fz7Runner) Run(context.Context, core.Cmd) (core.CmdResult, error) { return r.res, r.err }

// FuzzToolVariant feeds arbitrary output of `iptables --version`, as a
// successful run, as a failed one and as a tool that is not installed, to
// toolVariant: it never panics, a variant it reports is nft or legacy, and it
// reads the output as its first line, after white space is trimmed from the
// whole output, ending in the variant's name. Only a tool that is not
// installed is neither a variant nor an error (R74).
func FuzzToolVariant(f *testing.F) {
	for _, s := range []string{
		"iptables v1.8.10 (nf_tables)\n", "iptables v1.8.7 (legacy)", "\niptables v1.8.10 (nf_tables)\n",
		"iptables v1.8.10", "iptables v1.8.10 (nf_tables)\nsecond line (legacy)", "",
	} {
		f.Add(s, uint8(0))
	}
	f.Add("iptables: not found", uint8(1))
	f.Add("", uint8(2))
	f.Fuzz(func(t *testing.T, out string, mode uint8) {
		x := fz7Runner{res: core.CmdResult{Stdout: []byte(out)}}
		switch mode % 3 {
		case 1:
			x.res = core.CmdResult{Stderr: []byte(out), ExitCode: 1}
		case 2:
			x.err = fs.ErrNotExist
		}
		variant, ok, err := toolVariant(context.Background(), x, "iptables")
		switch {
		case ok:
			if err != nil || (variant != "nft" && variant != "legacy") {
				t.Fatalf("variant %q, error %v for %q", variant, err, out[:min(len(out), 64)])
			}
		case err != nil:
			if variant != "" {
				t.Fatalf("variant %q with error %v for %q", variant, err, out[:min(len(out), 64)])
			}
		case mode%3 != 2:
			t.Fatalf("no variant and no error for %q although the tool ran", out[:min(len(out), 64)])
		}
		if mode%3 == 0 {
			line, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
			if want := strings.HasSuffix(line, " (nf_tables)") || strings.HasSuffix(line, " (legacy)"); ok != want {
				t.Fatalf("ok is %v for the first line %q", ok, line[:min(len(line), 64)])
			}
		}
	})
}
