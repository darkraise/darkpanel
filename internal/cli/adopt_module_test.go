package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/darkraise/darkpanel/internal/core"
)

// zzModule is module "zz": an action, a resource and a stream, plus extra
// actions a case adds, sharing its id with a hand-written zz group.
type zzModule struct{ extra []string }

func (m zzModule) Manifest() core.Manifest {
	man := core.Manifest{ID: "zz", Version: 1, DefaultEnabled: true,
		Resources: []core.ResourceDef{
			core.DefineResource("zz.status", func(context.Context, core.Env) (demoStatus, error) { return demoStatus{}, nil }),
		}}
	for _, name := range append([]string{"zz.thing.do"}, m.extra...) {
		man.Actions = append(man.Actions, core.DefineRead(core.ActionSpec{Name: name, Version: 1, Side: core.SideAgent, Summary: "Zz."},
			fixtureHandler[demoItemQuery, demoItem]{}))
	}
	return man
}

func (zzModule) Streams() []core.StreamDef {
	return []core.StreamDef{core.DefineStream("logs", func(context.Context, core.Env, cflLogParams, func(cflLogLine) error) error {
		return nil
	})}
}

// zzGroup is a hand-written zz command with a hand-written install
// subcommand.
func zzGroup() (group, install *cobra.Command) {
	group = &cobra.Command{Use: "zz", Short: "Zz commands"}
	install = &cobra.Command{Use: "install", RunE: func(*cobra.Command, []string) error { return nil }}
	group.AddCommand(install)
	return group, install
}

func TestAdoptModule(t *testing.T) {
	tests := []struct {
		name  string
		mod   zzModule
		adopt []string
		want  string // a substring of NewRoot's error; empty means it succeeds
	}{
		{name: "adopted", adopt: []string{"zz"}},
		{name: "not adopted keeps the clash", want: `clashes with the hand-written command "zz"`},
		{name: "group that is not hand-written", adopt: []string{"zz", "nope"}, want: `adopting group "nope" is not a hand-written command`},
		{name: "replacing a hand-written subcommand", mod: zzModule{extra: []string{"zz.install.run"}}, adopt: []string{"zz"},
			want: `zz.install.run would replace the hand-written command "darkpanel zz install"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg, err := core.NewRegistry(tt.mod)
			if err != nil {
				t.Fatal(err)
			}
			group, install := zzGroup()
			root, err := NewRoot(Options{Registry: reg, Commands: []*cobra.Command{group}, AdoptModules: tt.adopt})
			if tt.want != "" {
				if err == nil || !strings.Contains(err.Error(), tt.want) {
					t.Fatalf("error %v, want one containing %q", err, tt.want)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			for name, path := range map[string]string{"zz.thing.do": "darkpanel zz thing do", "zz.status": "darkpanel zz status get"} {
				if cmd, ok := FindCommand(root, name); !ok || cmd.CommandPath() != path {
					t.Errorf("FindCommand(%q) = %v, %v; want %q", name, cmd, ok, path)
				}
			}
			if c, _, err := root.Find([]string{"zz", "logs"}); err != nil || c.CommandPath() != "darkpanel zz logs" {
				t.Errorf("zz logs: %v, %v; want the stream command", c, err)
			}
			if c, _, err := root.Find([]string{"zz", "install"}); err != nil || c != install {
				t.Errorf("zz install: %v, %v; want the hand-written install command", c, err)
			}
		})
	}
}

func TestAdoptModuleRefusesAnUnregisteredModule(t *testing.T) {
	reg, err := core.NewRegistry(zzModule{})
	if err != nil {
		t.Fatal(err)
	}
	group, _ := zzGroup()
	nope := &cobra.Command{Use: "nope"}
	_, err = NewRoot(Options{Registry: reg, Commands: []*cobra.Command{group, nope}, AdoptModules: []string{"zz", "nope"}})
	if err == nil || !strings.Contains(err.Error(), "nope adopts module nope, which is not registered") {
		t.Fatalf("error %v, want the unregistered module refused", err)
	}
}
