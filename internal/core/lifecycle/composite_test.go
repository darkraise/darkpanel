package lifecycle

import (
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

var refCsuAdd = core.ActionRef{Name: "csu.item.add", Version: 1}

// csuModule declares one StepUp action, which no composite may take as a step.
type csuModule struct{}

func (csuModule) Manifest() core.Manifest {
	return core.Manifest{ID: "csu", Version: 1, Actions: []core.ActionDef{
		core.DefineMutating(core.ActionSpec{
			Name: refCsuAdd.Name, Version: 1, Class: core.ClassCompensable, Side: core.SideAgent,
			LockKeys: []string{"csu"}, ForwardOnly: true, Converges: true, StepUp: true,
		}, cstDel(cstModule{newCstHost()})),
	}}
}

func TestCompositeRefusesStepUpStep(t *testing.T) {
	f := newCfxFixture(t, csuModule{})
	f.bad.set(cstStep("a", refCsuAdd, `{"name":"x"}`))
	_, err := f.planC(refCfxBad, `{"name":"x"}`)
	if got := errcode.From(err).Code; err == nil || got != errcode.Internal ||
		!strings.Contains(err.Error(), refCsuAdd.String()) || !strings.Contains(err.Error(), "StepUp") {
		t.Fatalf("Plan = %v (code %s), want an internal error naming %s and StepUp", err, got, refCsuAdd)
	}
}
