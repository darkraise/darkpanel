package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"slices"
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

func TestSocketCommandsResolveThroughSelectPaths(t *testing.T) {
	// --dev and temporary XDG directories keep a command that ignores
	// selectPaths away from the controller socket of this host.
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	want := paths.Under(t.TempDir())
	calls := psSwap(t, want)
	out, code := runArgs("user", "list", "--dev", "--json")
	if code == cli.ExitOK || !strings.Contains(out, want.ControllerSocket) || strings.Join(*calls, ",") != "list" {
		t.Errorf("user list without a controller: exit %d, output %q, selectPaths calls %v; want a failure dialing %s",
			code, out, *calls, want.ControllerSocket)
	}
}

// sdRecord wraps the current selectPaths so that it records "<command path>
// dev=<dev>" for every call, "<nil>" in place of the command path for a nil
// command, and answers through the function it wrapped.
func sdRecord(t *testing.T) *[]string {
	t.Helper()
	inner := selectPaths
	t.Cleanup(func() { selectPaths = inner })
	var calls []string
	selectPaths = func(cmd *cobra.Command, dev bool) (paths.Paths, error) {
		name := "<nil>"
		if cmd != nil {
			name = cmd.CommandPath()
		}
		calls = append(calls, fmt.Sprintf("%s dev=%v", name, dev))
		return inner(cmd, dev)
	}
	return &calls
}

func TestSdCallSitesPassTheirCommand(t *testing.T) {
	agent := func(t *testing.T) { fakeAgent(t, 1000) }
	update := func(t *testing.T) { fakeUpdate(t, 0) }
	tests := []struct {
		name string
		seam func(t *testing.T)
		args []string
		want string
	}{
		{"serve", func(t *testing.T) {
			fakeServe(t, 1000, nil)
			t.Setenv("XDG_STATE_HOME", t.TempDir())
			t.Setenv("XDG_RUNTIME_DIR", "")
		}, []string{"serve", "--dev"}, "darkpanel serve dev=true"},
		{"agent run", agent, []string{"agent", "run", "--dev"}, "darkpanel agent run dev=true"},
		{"agent enroll", agent, []string{"agent", "enroll", "--dev"}, "darkpanel agent enroll dev=true"},
		{"agent check", agent, []string{"agent", "check", "--dev"}, "darkpanel agent check dev=true"},
		{"agent task", func(t *testing.T) { taskAgentDB(t, fakeAgent(t, 1000).dev) },
			[]string{"agent", "task", "--dev", "01JTASK", "apply", "1"}, "darkpanel agent task dev=true"},
		{"update run", update, []string{"update", "run", "01HZY6TX"}, "darkpanel update run dev=false"},
		{"update recover", update, []string{"update", "recover"}, "darkpanel update recover dev=false"},
		{"update rollback --direct", update, []string{"update", "rollback", "--direct"}, "darkpanel update rollback dev=false"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.seam(t)
			calls := sdRecord(t)
			stdout, stderr, code := runStdin(strings.NewReader("a-token\n"), tt.args...)
			if !slices.Equal(*calls, []string{tt.want}) {
				t.Errorf("darkpanel %s: selectPaths calls %q, want exactly %q (exit %d, stdout %q, stderr %q)",
					strings.Join(tt.args, " "), *calls, tt.want, code, stdout, stderr)
			}
		})
	}
}

func TestSdRollbackFallbackPassesNoCommand(t *testing.T) {
	root := paths.Under(t.TempDir())
	saved := selectPaths
	t.Cleanup(func() { selectPaths = saved })
	selectPaths = func(*cobra.Command, bool) (paths.Paths, error) { return root, nil }
	calls := sdRecord(t)
	// root holds no previous binary, so the fallback stops before it runs anything.
	_, err := rollbackFallback(io.Discard)(context.Background(), json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "no previous binary") {
		t.Errorf("the rollback fallback returned %v, want the refusal that there is no previous binary", err)
	}
	if want := []string{"<nil> dev=false"}; !slices.Equal(*calls, want) {
		t.Errorf("the rollback fallback called selectPaths %q, want %q", *calls, want)
	}
}
