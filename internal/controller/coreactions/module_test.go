package coreactions

import (
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)

func TestManifest(t *testing.T) {
	m := Module(Deps{}).Manifest()
	if m.ID != core.ModuleIDCore || m.Version != 1 || !m.DefaultEnabled {
		t.Errorf("Manifest = %+v, want id core, version 1, enabled by default", m)
	}
	if len(m.Actions) != 0 || len(m.Resources) != 0 || m.Migrations != nil {
		t.Errorf("Manifest declares actions, resources or migrations: %+v", m)
	}
}
