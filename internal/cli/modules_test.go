package cli

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// mhSwap replaces moduleCommands and controllerCommands for one test.
func mhSwap(t *testing.T, mods map[string]func(a *app) []*cobra.Command, ctrl []func(a *app) []*cobra.Command) {
	t.Helper()
	savedMods, savedCtrl := moduleCommands, controllerCommands
	t.Cleanup(func() { moduleCommands, controllerCommands = savedMods, savedCtrl })
	moduleCommands, controllerCommands = mods, ctrl
}

// mhCommand is a hand-written command that prints its command path.
func mhCommand(name string) func(a *app) []*cobra.Command {
	return func(*app) []*cobra.Command {
		return []*cobra.Command{{
			Use:  name,
			Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s ran\n", cmd.CommandPath())
				return err
			},
		}}
	}
}

func TestModuleCommandsCoverEveryModule(t *testing.T) {
	want := []string{"cloudflare", "derp", "docker", "firewall", "npm", "panel", "tailnet", "tailscale"}
	if got := slices.Sorted(maps.Keys(moduleCommands)); !slices.Equal(got, want) {
		t.Fatalf("moduleCommands has %v, want %v", got, want)
	}
	for _, id := range want {
		if cmds := moduleCommands[id](&app{}); len(cmds) != 0 {
			t.Errorf("the %s stub returns %d commands, want none", id, len(cmds))
		}
	}
	if len(controllerCommands) != 1 {
		t.Fatalf("controllerCommands has %d entries, want 1 (backupCommands)", len(controllerCommands))
	}
	if cmds := controllerCommands[0](&app{}); len(cmds) != 0 {
		t.Errorf("the backup stub returns %d commands, want none", len(cmds))
	}
	root, err := NewRoot(Options{Registry: testRegistry(t)})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range root.Commands() {
		if slices.Contains(want, c.Name()) {
			t.Errorf("a module without hand-written commands got the group %q", c.CommandPath())
		}
	}
}

func TestModuleCommandsMountUnderTheirGroup(t *testing.T) {
	mhSwap(t, map[string]func(a *app) []*cobra.Command{"demo": mhCommand("extra"), "firewall": mhCommand("break-glass")},
		[]func(a *app) []*cobra.Command{mhCommand("backup")})
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"demo", "extra"}, "darkpanel demo extra ran\n"},
		{[]string{"firewall", "break-glass"}, "darkpanel firewall break-glass ran\n"},
		{[]string{"controller", "backup"}, "darkpanel controller backup ran\n"},
	} {
		stdout, stderr, code := cliEnv{}.run(t, tc.args...)
		if code != ExitOK || stdout != tc.want {
			t.Errorf("darkpanel %s: exit %d, stdout %q, stderr %q; want %q", strings.Join(tc.args, " "), code, stdout, stderr, tc.want)
		}
	}
	root, err := NewRoot(Options{Registry: testRegistry(t)})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := FindCommand(root, "demo.item.get"); !ok {
		t.Error("the generated demo item get is gone once demo has a hand-written command")
	}
}

func TestModuleCommandsRefuseClashes(t *testing.T) {
	for _, tc := range []struct {
		name string
		mods map[string]func(a *app) []*cobra.Command
		ctrl []func(a *app) []*cobra.Command
		want string
	}{
		{"a generated group", map[string]func(a *app) []*cobra.Command{"demo": mhCommand("item")}, nil, `"darkpanel demo item"`},
		{"the controller's web-ca", nil, []func(a *app) []*cobra.Command{mhCommand("web-ca")}, `"darkpanel controller web-ca"`},
		{"another hand-written command", nil,
			[]func(a *app) []*cobra.Command{mhCommand("backup"), mhCommand("backup")}, `"darkpanel controller backup"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mhSwap(t, tc.mods, tc.ctrl)
			_, err := NewRoot(Options{Registry: testRegistry(t)})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("NewRoot: error %v, want one naming %s", err, tc.want)
			}
		})
	}
}
