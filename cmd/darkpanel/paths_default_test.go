//go:build !harness

package main

import (
	"testing"

	"github.com/darkraise/darkpanel/internal/cli"
)

// TestSdNoStateDirOutsideHarnessBuilds holds --state-dir, which moves every
// path the binary uses, to harness builds: a release build has no such flag.
func TestSdNoStateDirOutsideHarnessBuilds(t *testing.T) {
	root, err := newRoot(cli.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if f := root.PersistentFlags().Lookup("state-dir"); f != nil {
		t.Errorf("a build without the harness tag has a --state-dir flag: %q", f.Usage)
	}
	for _, path := range [][]string{{"serve"}, {"agent", "run"}, {"update", "recover"}} {
		if cmd, _, err := root.Find(path); err != nil || cmd.Flag("state-dir") != nil {
			t.Errorf("%v: %v, %v; want the command, without --state-dir", path, cmd, err)
		}
	}
}
