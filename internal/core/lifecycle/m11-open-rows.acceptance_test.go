package lifecycle

import (
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

type qaStepUpModule struct{}

func (qaStepUpModule) Manifest() core.Manifest {
	return core.Manifest{ID: "qsu", Version: 1, Actions: []core.ActionDef{
		core.DefineMutating(core.ActionSpec{
			Name: "qsu.item.plain", Version: 1, Class: core.ClassCompensable, Side: core.SideAgent,
			LockKeys: []string{"qsu"}, Converges: true,
		}, cstAdd(cstModule{newCstHost()})),
		core.DefineMutating(core.ActionSpec{
			Name: "qsu.item.guarded", Version: 1, Class: core.ClassCompensable, Side: core.SideAgent,
			LockKeys: []string{"qsu"}, Converges: true, StepUp: true,
		}, cstAdd(cstModule{newCstHost()})),
	}}
}

func TestA4_CompositeWithAStepUpStepFailsAsAModuleBug(t *testing.T) {
	plain := core.ActionRef{Name: "qsu.item.plain", Version: 1}
	guarded := core.ActionRef{Name: "qsu.item.guarded", Version: 1}

	f := newCfxFixture(t, qaStepUpModule{})
	f.bad.set(cstStep("a", plain, `{"name":"x"}`))
	if _, err := f.planC(refCfxBad, `{"name":"x"}`); err != nil {
		t.Fatalf("control: planning a composite whose step is not StepUp failed: %v", err)
	}

	f = newCfxFixture(t, qaStepUpModule{})
	f.bad.set(cstStep("a", guarded, `{"name":"x"}`))
	_, err := f.planC(refCfxBad, `{"name":"x"}`)
	if err == nil {
		t.Fatal("planning a composite whose step is a StepUp action succeeded")
	}
	if got := errcode.From(err).Code; got != errcode.Internal {
		t.Errorf("error code = %q, want %q (a module bug); error: %v", got, errcode.Internal, err)
	}
}
