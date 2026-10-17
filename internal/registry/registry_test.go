package registry

import (
	"context"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules"
)

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
	if len(mods) != 1+len(modules.All()) || mods[0].ID != core.ModuleIDCore {
		t.Errorf("modules = %d, first %q; want core first, then modules.All()", len(mods), mods[0].ID)
	}
}

func TestBuildWith(t *testing.T) {
	read := core.DefineRead[struct{}, hostListOut](core.ActionSpec{Name: "core.host.list", Version: 1, Side: core.SideController}, hostList{})
	r, err := BuildWith(fakeCore{ID: core.ModuleIDCore, Version: 1, Actions: []core.ActionDef{read}})
	if err != nil {
		t.Fatalf("BuildWith: %v", err)
	}
	if _, ok := r.Action(core.ActionRef{Name: "core.host.list", Version: 1}); !ok {
		t.Error("core.host.list/v1 not registered")
	}

	if _, err := BuildWith(fakeCore{ID: "demo", Version: 1}); err == nil || !strings.Contains(err.Error(), "want \"core\"") {
		t.Errorf("non-core module: err = %v", err)
	}
	foreign := core.DefineRead[struct{}, hostListOut](core.ActionSpec{Name: "demo.host.list", Version: 1, Side: core.SideController}, hostList{})
	if _, err := BuildWith(fakeCore{ID: core.ModuleIDCore, Version: 1, Actions: []core.ActionDef{foreign}}); err == nil {
		t.Error("BuildWith accepted a core module with a foreign action name")
	}
}
