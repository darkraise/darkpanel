package netfilter

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// FuzzParse feeds arbitrary input to Parse: it must never panic, and a
// ruleset it accepts holds at least one table, each known and named once, and
// each rule jumps to a declared chain or a contract target or is opaque
// (spec 1.8, R74).
func FuzzParse(f *testing.F) {
	seeds, err := filepath.Glob(filepath.Join("testdata", "*.save"))
	if err != nil {
		f.Fatal(err)
	}
	for _, name := range seeds {
		b, err := os.ReadFile(name) //nolint:gosec // G304: a fixture under testdata/
		if err != nil {
			f.Fatal(err)
		}
		f.Add(string(b))
	}
	f.Add(psSample)
	f.Fuzz(func(t *testing.T, in string) {
		rs, err := Parse(strings.NewReader(in))
		if err != nil {
			return
		}
		if len(rs.Tables) == 0 {
			t.Fatal("accepted input produced no tables")
		}
		seen := map[string]bool{}
		for _, tb := range rs.Tables {
			if !slices.Contains(Tables(), tb.Name) {
				t.Fatalf("table name %q", tb.Name)
			}
			if seen[tb.Name] {
				t.Fatalf("table %q appears twice", tb.Name)
			}
			seen[tb.Name] = true
			for _, c := range tb.Chains {
				for _, r := range c.Rules {
					if r.Target.Chain && tb.Chain(r.Target.Name) == nil {
						t.Fatalf("jump to undeclared chain %q", r.Target.Name)
					}
					if _, ok := targets[r.Target.Name]; r.Target.Name != "" && !r.Target.Chain && !ok && !r.Opaque() {
						t.Fatalf("unknown target %q not opaque", r.Target.Name)
					}
				}
			}
		}
	})
}
