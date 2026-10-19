package lifecycle

import (
	"reflect"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)

func TestApplyAndVerifySeeTheChangeID(t *testing.T) {
	f := newFixture(t)
	pl := f.plan(alice, refAdd, `{"name":"a","value":"1"}`)
	v := f.apply(alice, pl.PlanID, core.ApplyOptions{})
	// Plan runs once for the plan and once more as apply's recheck, both
	// before the change exists.
	want := []string{"Plan=", "Plan=", "Apply=" + v.ID, "Verify=" + v.ID}
	if !reflect.DeepEqual(f.host.changeIDs, want) {
		t.Errorf("change ids seen = %v, want %v", f.host.changeIDs, want)
	}
}

func TestRecoveryVerifySeesTheChangeID(t *testing.T) {
	f := newFixture(t)
	_, id := f.crash(refAdd, `{"name":"a","value":"1","crashApply":true}`, core.ApplyOptions{})
	f.restart()
	f.host.changeIDs = nil
	f.recoverAll()
	if want := []string{"Verify=" + id}; !reflect.DeepEqual(f.host.changeIDs, want) {
		t.Errorf("change ids seen during recovery = %v, want %v", f.host.changeIDs, want)
	}
}
