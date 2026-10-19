package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/darkraise/darkpanel/internal/core"
)

// updateCore is a core module whose agent-side actions share their first
// command word with a hand-written update group.
type updateCore struct{}

func (updateCore) Manifest() core.Manifest {
	m := core.Manifest{ID: core.ModuleIDCore, Version: 1}
	for _, name := range []string{"core.update.apply", "core.update.rollback", "core.update.status", "core.host.list"} {
		side := core.SideAgent
		if name == "core.host.list" {
			side = core.SideController
		}
		m.Actions = append(m.Actions, core.DefineRead(core.ActionSpec{Name: name, Version: 1, Side: side},
			fixtureHandler[demoNoInput, demoNoInput]{}))
	}
	return m
}

func updateRegistry(t *testing.T) *core.Registry {
	t.Helper()
	reg, err := core.NewRegistry(updateCore{})
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// updateGroup is a hand-written update command with a hand-written run
// subcommand, as the assembly passes it.
func updateGroup() (group, run *cobra.Command) {
	group = &cobra.Command{Use: "update", Short: "Update commands"}
	run = &cobra.Command{Use: "run", RunE: func(*cobra.Command, []string) error { return nil }}
	group.AddCommand(run)
	return group, run
}

var updateActions = []string{"core.update.apply", "core.update.rollback", "core.update.status"}

func noFallback(context.Context, json.RawMessage) (json.RawMessage, error) { return nil, nil }

func TestAdoptPutsGeneratedCommandsUnderAHandWrittenGroup(t *testing.T) {
	group, run := updateGroup()
	root, err := NewRoot(Options{
		Registry: updateRegistry(t), Commands: []*cobra.Command{group},
		Adopt:          map[string][]string{"update": updateActions},
		LocalFallbacks: map[string]LocalFallback{"core.update.rollback": noFallback},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range updateActions {
		cmd, ok := FindCommand(root, name)
		if !ok || cmd.Parent() != group {
			t.Errorf("FindCommand(%q) = %v, %v; want a subcommand of the hand-written update group", name, cmd, ok)
		}
	}
	if c, _, err := root.Find([]string{"update", "run"}); err != nil || c != run {
		t.Errorf("update run: %v, %v; want the hand-written run command", c, err)
	}
	if _, ok := FindCommand(root, "core.host.list"); !ok {
		t.Error("core.host.list is missing")
	}
}

func TestAdoptRefusals(t *testing.T) {
	tests := []struct {
		name      string
		adopt     map[string][]string
		fallbacks map[string]LocalFallback
		clash     bool // the group gets a hand-written apply subcommand
		want      string
	}{
		{name: "no adoption keeps the clash", want: `clashes with the hand-written command "update"`},
		{name: "unknown group", adopt: map[string][]string{"upgrade": updateActions},
			want: `adopting group "upgrade" is not a hand-written command`},
		{name: "unregistered action", adopt: map[string][]string{"update": append([]string{"core.update.freeze"}, updateActions...)},
			want: `update adopts core.update.freeze, which is not a registered action`},
		{name: "action outside the group", adopt: map[string][]string{"update": append([]string{"core.host.list"}, updateActions...)},
			want: `update adopts core.host.list, whose command "darkpanel host list" is not under it`},
		{name: "adopted name taken by a hand-written subcommand", adopt: map[string][]string{"update": updateActions}, clash: true,
			want: `core.update.apply would replace the hand-written command "darkpanel update apply"`},
		{name: "fallback for an unregistered action", adopt: map[string][]string{"update": updateActions},
			fallbacks: map[string]LocalFallback{"core.update.freeze": noFallback},
			want:      `local fallback core.update.freeze is not a registered agent-side action`},
		{name: "fallback for a controller-side action", adopt: map[string][]string{"update": updateActions},
			fallbacks: map[string]LocalFallback{"core.host.list": noFallback},
			want:      `local fallback core.host.list is not a registered agent-side action`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			group, _ := updateGroup()
			if tt.clash {
				group.AddCommand(&cobra.Command{Use: "apply"})
			}
			_, err := NewRoot(Options{
				Registry: updateRegistry(t), Commands: []*cobra.Command{group},
				Adopt: tt.adopt, LocalFallbacks: tt.fallbacks,
			})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %v, want one containing %q", err, tt.want)
			}
		})
	}
}
