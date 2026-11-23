package registry

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/install"
	"github.com/darkraise/darkpanel/internal/modules"
)

// coreActionNames are the update and rollout actions every process's core
// manifest holds, in registration order.
var coreActionNames = []string{
	"core.update.apply", "core.update.rollback", "core.update.status",
	"core.update.rollout", "core.update.rollout-cancel", "core.update.rollouts",
}

type hostList struct{}

type hostListOut struct {
	Hosts []string `json:"hosts"`
}

func (hostList) Read(context.Context, core.Env, struct{}) (hostListOut, error) {
	return hostListOut{}, nil
}

type fakeCore core.Manifest

func (m fakeCore) Manifest() core.Manifest { return core.Manifest(m) }

func TestBuild(t *testing.T) {
	r, err := Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mods := r.Modules()
	if len(mods) != 2+len(modules.All()) || mods[0].ID != core.ModuleIDCore {
		t.Fatalf("modules = %d, first %q; want core first, then auth and modules.All()", len(mods), mods[0].ID)
	}
	var names []string
	for _, a := range mods[0].Actions {
		if slices.Contains(coreActionNames, a.Spec.Name) {
			names = append(names, a.Spec.Name)
		}
	}
	if !slices.Equal(names, coreActionNames) {
		t.Errorf("core update and rollout actions = %q, want %q", names, coreActionNames)
	}
	if !reflect.DeepEqual(mods[0].OwnedPaths, install.CoreOwnedPaths()) {
		t.Errorf("core owned paths = %v, want install.CoreOwnedPaths()", mods[0].OwnedPaths)
	}
}

func TestCoreActions(t *testing.T) {
	var names []string
	for _, a := range CoreActions(nil, "") {
		names = append(names, a.Spec.Name)
	}
	if !slices.Equal(names, coreActionNames) {
		t.Errorf("CoreActions = %q, want %q", names, coreActionNames)
	}
}

func TestBuildWith(t *testing.T) {
	read := core.DefineRead[struct{}, hostListOut](core.ActionSpec{Name: "core.host.list", Version: 1, Side: core.SideController}, hostList{})
	r, err := BuildWith(fakeCore{ID: core.ModuleIDCore, Version: 1, Actions: []core.ActionDef{read}}, nil)
	if err != nil {
		t.Fatalf("BuildWith: %v", err)
	}
	if _, ok := r.Action(core.ActionRef{Name: "core.host.list", Version: 1}); !ok {
		t.Error("core.host.list/v1 not registered")
	}

	if _, err := BuildWith(fakeCore{ID: "demo", Version: 1}, nil); err == nil || !strings.Contains(err.Error(), "want \"core\"") {
		t.Errorf("non-core module: err = %v", err)
	}
	foreign := core.DefineRead[struct{}, hostListOut](core.ActionSpec{Name: "demo.host.list", Version: 1, Side: core.SideController}, hostList{})
	if _, err := BuildWith(fakeCore{ID: core.ModuleIDCore, Version: 1, Actions: []core.ActionDef{foreign}}, nil); err == nil {
		t.Error("BuildWith accepted a core module with a foreign action name")
	}
}

func TestBuildRegistersAuthModule(t *testing.T) {
	r, err := Build()
	if err != nil {
		t.Fatal(err)
	}
	mods := r.Modules()
	if len(mods) < 2 || mods[0].ID != core.ModuleIDCore || mods[1].ID != "auth" {
		t.Fatalf("first modules %v, want core then auth", mods[:min(2, len(mods))])
	}
	for _, name := range []string{"auth.oidc.set", "auth.oidc.clear", "auth.tailscale.set", "auth.settings.get"} {
		def, ok := r.Action(core.ActionRef{Name: name, Version: 1})
		if !ok || def.Spec.Side != core.SideController {
			t.Errorf("%s/v1: registered %v, side %q; want a controller-side action", name, ok, def.Spec.Side)
		}
	}
	for _, m := range modules.All() {
		if m.Manifest().ID == "auth" {
			t.Error("modules.All() holds the auth module, which must not be listed per host")
		}
	}
}
