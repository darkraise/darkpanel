package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/darkraise/darkpanel/internal/cli"
	"github.com/darkraise/darkpanel/internal/paths"
)

// psSwap replaces selectPaths for one test, recording the name of every
// command it resolves for ("" for none).
func psSwap(t *testing.T, p paths.Paths) *[]string {
	t.Helper()
	saved := selectPaths
	t.Cleanup(func() { selectPaths = saved })
	var calls []string
	selectPaths = func(cmd *cobra.Command, _ bool) (paths.Paths, error) {
		name := ""
		if cmd != nil {
			name = cmd.Name()
		}
		calls = append(calls, name)
		return p, nil
	}
	return &calls
}

func TestSelectPathsResolves(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	t.Setenv("XDG_RUNTIME_DIR", "")
	if p, err := selectPaths(nil, false); err != nil || p != paths.Production() {
		t.Errorf("selectPaths(nil, false) = %+v, %v; want the production paths", p, err)
	}
	if p, err := selectPaths(&cobra.Command{Use: "serve"}, true); err != nil || p != paths.Under(filepath.Join(state, "darkpanel-dev")) {
		t.Errorf("selectPaths(serve, true) = %+v, %v; want the development paths", p, err)
	}
}

func TestServeResolvesThroughSelectPaths(t *testing.T) {
	served := fakeServe(t, 0, nil)
	want := paths.Under(t.TempDir())
	calls := psSwap(t, want)
	if out, code := runArgs("serve", "--dev"); code != cli.ExitOK {
		t.Fatalf("serve --dev: exit %d: %s", code, out)
	}
	if len(*served) != 1 || (*served)[0].Paths != want || strings.Join(*calls, ",") != "serve" {
		t.Errorf("serve --dev: served %+v after selectPaths calls %v; want the selected paths, resolved once for serve", *served, *calls)
	}
}

func TestNewRootAddsRootFlags(t *testing.T) {
	saved := addRootFlags
	t.Cleanup(func() { addRootFlags = saved })
	addRootFlags = func(root *cobra.Command) { root.PersistentFlags().String("probe-dir", "", "a test flag") }
	root, err := newRoot(cli.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if root.PersistentFlags().Lookup("probe-dir") == nil {
		t.Error("newRoot did not call addRootFlags")
	}
	if cmd, _, err := root.Find([]string{"agent", "run"}); err != nil || cmd.Flag("probe-dir") == nil {
		t.Errorf("agent run does not inherit the root flag: %v, %v", cmd, err)
	}
}
