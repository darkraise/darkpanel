package lifecycle

import (
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

var (
	refCsuAdd = core.ActionRef{Name: "csu.item.add", Version: 1}
	refCsuDel = core.ActionRef{Name: "csu.item.del", Version: 1}
)

// csuModule declares a StepUp action and an UndoStepUp action, which no
// composite may take as a step: a composite's undo runs its steps' undos
// without the operator's step-up.
type csuModule struct{}

func (csuModule) Manifest() core.Manifest {
	return core.Manifest{ID: "csu", Version: 1, Actions: []core.ActionDef{
		core.DefineMutating(core.ActionSpec{
			Name: refCsuAdd.Name, Version: 1, Class: core.ClassCompensable, Side: core.SideAgent,
			LockKeys: []string{"csu"}, ForwardOnly: true, Converges: true, StepUp: true,
		}, cstDel(cstModule{newCstHost()})),
		core.DefineMutating(core.ActionSpec{
			Name: refCsuDel.Name, Version: 1, Class: core.ClassCompensable, Side: core.SideAgent,
			LockKeys: []string{"csu"}, ForwardOnly: true, Converges: true, UndoStepUp: true,
		}, cstDel(cstModule{newCstHost()})),
	}}
}

func TestCompositeRefusesStepUpStep(t *testing.T) {
	for _, c := range []struct {
		ref  core.ActionRef
		flag string
	}{{refCsuAdd, "StepUp"}, {refCsuDel, "UndoStepUp"}} {
		f := newCfxFixture(t, csuModule{})
		f.bad.set(cstStep("a", c.ref, `{"name":"x"}`))
		_, err := f.planC(refCfxBad, `{"name":"x"}`)
		if got := errcode.From(err).Code; err == nil || got != errcode.Internal ||
			!strings.Contains(err.Error(), c.ref.String()) || !strings.Contains(err.Error(), c.flag) {
			t.Errorf("Plan with a step of %s = %v (code %s), want an internal error naming it and %s", c.ref, err, got, c.flag)
		}
	}
}
