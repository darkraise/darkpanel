package netfilter

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fz5Seeds adds the content of every file matching a pattern as a seed. A
// pattern that matches nothing fails the target, so a fixture directory that
// moved is noticed instead of silently shrinking the corpus.
func fz5Seeds(f *testing.F, patterns ...string) {
	f.Helper()
	for _, pattern := range patterns {
		names, err := filepath.Glob(pattern)
		if err != nil {
			f.Fatal(err)
		}
		if len(names) == 0 {
			f.Fatalf("no seed matches %s", pattern)
		}
		for _, name := range names {
			b, err := os.ReadFile(name) //nolint:gosec // G304: a fixture under a testdata/ directory
			if err != nil {
				f.Fatal(err)
			}
			f.Add(string(b))
		}
	}
}

// FuzzParse feeds arbitrary input to Parse: it must never panic, a ruleset it
// accepts holds at least one table, each known and named once, each rule
// jumps to a declared chain or a contract target or is opaque, and Key reads
// every rule of it, for both families, without panicking and with the same
// answer twice (spec 1.8, R74).
func FuzzParse(f *testing.F) {
	fz5Seeds(f,
		filepath.Join("testdata", "*.save"),
		filepath.Join("testdata", "ufw", "*", "*.save"),
		filepath.Join("..", "..", "modules", "firewall", "dockerfw", "testdata", "capture", "*", "*", "save", "*.txt"),
		filepath.Join("..", "..", "modules", "firewall", "eval", "testdata", "*", "*.save"),
	)
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
					for _, v6 := range []bool{false, true} {
						first := Key(r, v6)
						if second := Key(r, v6); first != second {
							t.Fatalf("Key of a rule of %q differs between two calls", in[:min(len(in), 64)])
						}
					}
				}
			}
		}
	})
}
