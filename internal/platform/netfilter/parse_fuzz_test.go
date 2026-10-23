package netfilter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// FuzzParse feeds arbitrary input to Parse: it must never panic, and a
// ruleset it accepts holds only known tables and declared chains.
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
		for _, tb := range rs.Tables {
			if tb.Name == "" || strings.ContainsAny(tb.Name, " \t") {
				t.Fatalf("table name %q", tb.Name)
			}
			for _, c := range tb.Chains {
				for _, r := range c.Rules {
					if r.Target.Chain && tb.Chain(r.Target.Name) == nil {
						t.Fatalf("jump to undeclared chain %q", r.Target.Name)
					}
				}
			}
		}
	})
}
