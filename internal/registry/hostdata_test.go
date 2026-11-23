package registry

import (
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)

func TestEveryControllerReadDeclaresHostData(t *testing.T) {
	r, err := Build()
	if err != nil {
		t.Fatal(err)
	}
	classes := map[core.HostData]bool{
		core.HostDataNone: true, core.HostDataFleet: true,
		core.HostDataInput: true, core.HostDataFiltered: true,
	}
	for _, def := range r.Actions() {
		s := def.Spec
		if s.Class != core.ClassRead || s.Side != core.SideController || s.Internal {
			continue
		}
		if !classes[s.HostData] {
			t.Errorf("%s: HostData %q, want one of none, fleet, input, filtered", def.Ref(), s.HostData)
			continue
		}
		if s.HostData == core.HostDataInput && len(s.HostPointers) == 0 {
			t.Errorf("%s: HostData input with no HostPointers", def.Ref())
		}
	}
}
