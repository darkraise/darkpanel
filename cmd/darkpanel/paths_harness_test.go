//go:build harness

package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/darkraise/darkpanel/internal/cli"
	"github.com/darkraise/darkpanel/internal/paths"
)

// sdStateDir sets harnessStateDir for one test and clears it afterwards.
func sdStateDir(t *testing.T, dir string) {
	t.Helper()
	old := harnessStateDir
	t.Cleanup(func() { harnessStateDir = old })
	harnessStateDir = dir
}

func TestSdSelectPathsUnderStateDir(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	t.Setenv("XDG_RUNTIME_DIR", "")
	dir := t.TempDir()
	cmd := &cobra.Command{Use: "run"}
	tests := []struct {
		name string
		dir  string
		cmd  *cobra.Command
		dev  bool
		want paths.Paths
		err  string
	}{
		{"state dir for a command", dir, cmd, false, paths.Under(dir), ""},
		{"state dir for the nil command of the rollback fallback", dir, nil, false, paths.Under(dir), ""},
		{"state dir with --dev", dir, cmd, true, paths.Paths{}, "--state-dir and --dev cannot be combined"},
		{"relative state dir", "state", cmd, false, paths.Paths{}, `--state-dir must be an absolute path, got "state"`},
		{"relative state dir for the nil command", "./state", nil, false, paths.Paths{}, `--state-dir must be an absolute path, got "./state"`},
		{"no state dir, production", "", cmd, false, paths.Production(), ""},
		{"no state dir, nil command", "", nil, false, paths.Production(), ""},
		{"no state dir, --dev", "", cmd, true, paths.Under(filepath.Join(state, "darkpanel-dev")), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sdStateDir(t, tt.dir)
			got, err := selectPaths(tt.cmd, tt.dev)
			if tt.err != "" {
				if err == nil || err.Error() != tt.err {
					t.Fatalf("selectPaths error = %v, want %q", err, tt.err)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Errorf("selectPaths = %+v, %v; want %+v", got, err, tt.want)
			}
		})
	}
}

func TestSdStateDirFlagIsPersistent(t *testing.T) {
	root, err := newRoot(cli.Options{})
	if err != nil {
		t.Fatal(err)
	}
	flag := root.PersistentFlags().Lookup("state-dir")
	if flag == nil {
		t.Fatal("the root command has no persistent --state-dir flag")
	}
	if want := "harness only: keep every path under this directory"; flag.Usage != want {
		t.Errorf("--state-dir usage = %q, want %q", flag.Usage, want)
	}
	for _, path := range [][]string{{"serve"}, {"agent", "run"}, {"update", "recover"}} {
		if cmd, _, err := root.Find(path); err != nil || cmd.Flag("state-dir") == nil {
			t.Errorf("%s does not inherit --state-dir: %v, %v", strings.Join(path, " "), cmd, err)
		}
	}
}

func TestSdStateDirReachesTheCommand(t *testing.T) {
	served := fakeServe(t, 1000, nil)
	dir := t.TempDir()
	t.Cleanup(func() { harnessStateDir = "" })
	if out, code := runArgs("serve", "--state-dir", dir); code != cli.ExitOK {
		t.Fatalf("serve --state-dir: exit %d: %s", code, out)
	}
	if len(*served) != 1 || (*served)[0].Paths != paths.Under(dir) {
		t.Errorf("serve --state-dir served %+v, want the paths under %s", *served, dir)
	}
	out, code := runArgs("serve", "--dev", "--state-dir", dir)
	if code != cli.ExitError || !strings.Contains(out, "--state-dir and --dev cannot be combined") || len(*served) != 1 {
		t.Errorf("serve --dev --state-dir: exit %d, output %q, served %d times; want exit %d, the refusal and no second run",
			code, out, len(*served), cli.ExitError)
	}
}
