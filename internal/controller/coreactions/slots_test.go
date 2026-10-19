package coreactions_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/darkraise/darkpanel/internal/controller/coreactions"
	"github.com/darkraise/darkpanel/internal/core"
)

type things struct{}

func (things) Read(context.Context, core.Env, coreactions.NoInput) (coreactions.Empty, error) {
	return coreactions.Empty{}, nil
}

func TestModuleCarriesExtraActionsAndOwnedPaths(t *testing.T) {
	extra := core.DefineRead[coreactions.NoInput, coreactions.Empty](core.ActionSpec{
		Name: "core.thing.list", Version: 1, Side: core.SideAgent, Summary: "List things.",
	}, things{})
	owned := []core.OwnedPath{{Path: "/etc/systemd/system/darkpanel-agent.service", Kind: "file"}}
	mod := coreactions.Module(coreactions.Deps{Extra: []core.ActionDef{extra}, OwnedPaths: owned})
	m := mod.Manifest()
	if last := m.Actions[len(m.Actions)-1]; last.Spec.Name != "core.thing.list" {
		t.Errorf("last core action = %s, want the extra core.thing.list", last.Spec.Name)
	}
	if !reflect.DeepEqual(m.OwnedPaths, owned) {
		t.Errorf("OwnedPaths = %+v, want %+v", m.OwnedPaths, owned)
	}
	reg, err := core.NewRegistry(mod)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	if _, ok := reg.Action(core.ActionRef{Name: "core.thing.list", Version: 1}); !ok {
		t.Error("the extra action is not in the registry")
	}
	if len(coreactions.Module(coreactions.Deps{}).Manifest().Actions) != len(m.Actions)-1 {
		t.Error("an empty Extra changed the built-in actions")
	}
}
