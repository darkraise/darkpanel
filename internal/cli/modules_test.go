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

// mhAliased is mhCommand with aliases.
func mhAliased(name string, aliases ...string) func(a *app) []*cobra.Command {
	return func(a *app) []*cobra.Command {
		cmds := mhCommand(name)(a)
		cmds[0].Aliases = aliases
		return cmds
	}
}

func TestModuleCommandsCoverEveryModule(t *testing.T) {
	want := []string{"cloudflare", "derp", "docker", "firewall", "npm", "panel", "tailnet", "tailscale"}
	if got := slices.Sorted(maps.Keys(moduleCommands)); !slices.Equal(got, want) {
		t.Fatalf("moduleCommands has %v, want %v", got, want)
	}
	if len(controllerCommands) != 1 {
		t.Fatalf("controllerCommands has %d entries, want 1 (backupCommands)", len(controllerCommands))
	}
	root, err := NewRoot(Options{Registry: testRegistry(t)})
	if err != nil {
		t.Fatal(err)
	}
	// testRegistry generates no command for these modules, so a group exists
	// exactly when the module's function returns hand-written commands.
	for _, id := range want {
		grouped := slices.ContainsFunc(root.Commands(), func(c *cobra.Command) bool { return c.Name() == id })
		if has := len(moduleCommands[id](&app{})) > 0; grouped != has {
			t.Errorf("module %s: group present %t, hand-written commands %t", id, grouped, has)
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
		{"an alias on an existing name", nil,
			[]func(a *app) []*cobra.Command{mhAliased("bkp", "web-ca")}, `"darkpanel controller bkp" clashes with an existing command on "web-ca"`},
		{"an alias on another hand-written alias", nil,
			[]func(a *app) []*cobra.Command{mhAliased("backup", "b"), mhAliased("bundle", "b")}, `"darkpanel controller bundle" clashes with an existing command on "b"`},
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
