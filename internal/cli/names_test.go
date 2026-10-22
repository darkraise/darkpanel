package cli

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)

func TestCommandPath(t *testing.T) {
	cases := map[string]string{
		"demo.item.get":        "demo item get",
		"demo.rule.set":        "demo rule set",
		"demo.rule":            "demo rule get",
		"core.host.list":       "host list",
		"core.user.reset-totp": "user reset-totp",
		"core.status":          "status get",
	}
	for name, want := range cases {
		if got := strings.Join(CommandPath(name), " "); got != want {
			t.Errorf("CommandPath(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestEntries(t *testing.T) {
	es, err := entries(testRegistry(t), handWritten, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]entry{}
	var paths []string
	for _, e := range es {
		p := strings.Join(e.Path, " ")
		got[p] = e
		paths = append(paths, p)
	}
	want := []string{
		"audit list",
		"cov rule repair", "cov rule set",
		"demo item add", "demo item get", "demo item list", "demo item remove",
		"demo rule get", "demo rule repair", "demo rule set", "demo status get",
		"host create", "host delete", "host enroll-local", "host list", "host revoke", "host token", "host update-policy",
		"module disable", "module enable", "module list",
		"token create", "token list", "token revoke",
		"user activate", "user create", "user delete", "user list", "user reset-password", "user reset-totp",
	}
	if !slices.Equal(paths, want) {
		t.Errorf("paths\n%q\nwant\n%q", paths, want)
	}
	if e := got["demo item get"]; e.Action == nil || e.Action.Spec.Version != 2 || e.Name != "demo.item.get" {
		t.Errorf("demo item get is %+v, want demo.item.get version 2", e)
	}
	if e := got["demo rule get"]; e.Resource == nil || e.Action != nil || e.Name != "demo.rule" {
		t.Errorf("demo rule get is %+v, want resource demo.rule", e)
	}
	if e := got["user create"]; e.Action == nil || e.Name != "core.user.create" || !e.Action.Spec.BreakGlass {
		t.Errorf("user create is %+v, want the break-glass core.user.create", e)
	}
}

// fakeCore is a core module with the given read actions, standing in for a
// future core noun.
type fakeCore []string

func (f fakeCore) Manifest() core.Manifest {
	m := core.Manifest{ID: core.ModuleIDCore, Version: 1}
	for _, name := range f {
		m.Actions = append(m.Actions, core.DefineRead(core.ActionSpec{Name: name, Version: 1, Side: core.SideController},
			fixtureHandler[demoNoInput, demoNoInput]{}))
	}
	return m
}

func TestEntriesRefuseHandWrittenNames(t *testing.T) {
	for _, noun := range []string{"change", "controller", "serve", "version", "help", "completion", "agent"} {
		reg, err := core.NewRegistry(fakeCore{"core." + noun + ".list"})
		if err != nil {
			t.Fatal(err)
		}
		_, err = entries(reg, append(slices.Clone(handWritten), "agent"), nil)
		if err == nil || !strings.Contains(err.Error(), `hand-written command "`+noun+`"`) {
			t.Errorf("core.%s.list: error %v, want a clash with the hand-written %q", noun, err, noun)
		}
	}
}

func TestCheckEntriesRefusesClashes(t *testing.T) {
	read := core.DefineRead(core.ActionSpec{Name: "demo.rule.get", Version: 1, Side: core.SideAgent},
		fixtureHandler[demoNoInput, demoNoInput]{})
	res := core.DefineResource("demo.rule", func(context.Context, core.Env) (demoRule, error) { return demoRule{}, nil })
	same := []entry{
		{Name: "demo.rule.get", Path: CommandPath("demo.rule.get"), Action: &read},
		{Name: "demo.rule", Path: CommandPath("demo.rule"), Resource: &res},
	}
	if err := checkEntries(same, handWritten, nil); err == nil || !strings.Contains(err.Error(), `"darkpanel demo rule get"`) {
		t.Errorf("same path: error %v", err)
	}
	prefix := []entry{
		{Name: "core.demo.rule", Path: []string{"demo", "rule"}, Action: &read},
		{Name: "demo.rule.get", Path: CommandPath("demo.rule.get"), Action: &read},
	}
	if err := checkEntries(prefix, handWritten, nil); err == nil || !strings.Contains(err.Error(), "is also a command group") {
		t.Errorf("prefix: error %v", err)
	}
}
