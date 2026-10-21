package lifecycle

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// planCalled plans ref with input as alice under call.
func (f *jfixture) planCalled(ref core.ActionRef, input string, call *core.CallContext) core.PlanView {
	f.t.Helper()
	v, err := f.eng.Plan(context.Background(), alice, ref, json.RawMessage(input), core.PlanOptions{Call: call})
	if err != nil {
		f.t.Fatalf("Plan %s: %v", ref, err)
	}
	return v
}

func TestTheCallReachesEveryHandlerCall(t *testing.T) {
	f := newJFixture(t, jfxOptions{})
	ctx := context.Background()
	sent := &core.CallContext{ParentChangeID: "comp1", ParentAction: "cfx.deploy.add", StepID: "s1",
		RepairOf: "ignored", Retired: []string{"e1"}}
	want := core.CallContext{ParentChangeID: "comp1", ParentAction: "cfx.deploy.add", StepID: "s1", Retired: []string{"e1"}}
	pl := f.planCalled(refJSet, `{"name":"a","value":"1","local":true}`, sent)
	var stored string
	if err := f.db.Read().QueryRow("SELECT call FROM core_plans WHERE id = ?", pl.PlanID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != `{"parentChangeId":"comp1","parentAction":"cfx.deploy.add","stepId":"s1","retired":["e1"]}` {
		t.Errorf("stored call = %s", stored)
	}
	v, err := f.eng.Apply(ctx, alice, pl.PlanID, core.ApplyOptions{Call: &core.CallContext{ParentChangeID: "comp1", StepID: "s1"}})
	if err != nil {
		t.Fatal(err)
	}
	if v.ParentChangeID != "comp1" {
		t.Errorf("parent change = %q, want comp1", v.ParentChangeID)
	}
	if _, err := f.eng.Revert(ctx, alice, v.ID); err != nil {
		t.Fatal(err)
	}
	for _, call := range []string{"Plan", "Apply", "Verify", "Revert"} {
		got := f.store.calls(call)
		if len(got) == 0 || !reflect.DeepEqual(got[len(got)-1].CallCtx, want) {
			t.Errorf("%s saw %+v, want the call %+v", call, got, want)
		}
	}
}

func TestAnApplyMustNameItsPlansParentAndStep(t *testing.T) {
	step := &core.CallContext{ParentChangeID: "comp1", ParentAction: "cfx.deploy.add", StepID: "s1"}
	cases := []struct {
		name       string
		plan, call *core.CallContext
	}{
		{"another step", step, &core.CallContext{ParentChangeID: "comp1", StepID: "s2"}},
		{"another parent", step, &core.CallContext{ParentChangeID: "comp2", StepID: "s1"}},
		{"no call for a step's plan", step, nil},
		{"a call for a top-level plan", nil, &core.CallContext{ParentChangeID: "comp1", StepID: "s1"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newJFixture(t, jfxOptions{})
			pl := f.planCalled(refJModify, `{"name":"a","value":"1"}`, c.plan)
			_, err := f.eng.Apply(context.Background(), alice, pl.PlanID, core.ApplyOptions{Call: c.call})
			wantCode(t, err, errcode.PlanStateChanged)
			if n := f.count("SELECT count(*) FROM core_changes"); n != 0 {
				t.Errorf("changes = %d, want none", n)
			}
		})
	}
}

func TestATopLevelChangeHasNoCall(t *testing.T) {
	f := newJFixture(t, jfxOptions{})
	pl := f.planCalled(refJModify, `{"name":"a","value":"1","local":true}`, nil)
	v := f.apply(alice, pl.PlanID, core.ApplyOptions{})
	if v.State != core.StateCompleted || v.ParentChangeID != "" {
		t.Errorf("change = %+v, want completed without a parent", v)
	}
	if n := f.count("SELECT count(*) FROM core_plans WHERE id = ? AND call IS NULL", pl.PlanID); n != 1 {
		t.Error("a plan without a call stored one")
	}
	for _, call := range []string{"Plan", "Apply", "Verify"} {
		got := f.store.calls(call)
		if len(got) == 0 {
			t.Errorf("%s was not called", call)
		}
		for _, saw := range got {
			if !reflect.DeepEqual(saw.CallCtx, core.CallContext{}) {
				t.Errorf("%s saw %+v, want the zero call", call, saw.CallCtx)
			}
		}
	}
}

func TestRecoveryVerifySeesTheStoredCall(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	pl, err := f.eng.Plan(ctx, alice, refAdd, json.RawMessage(`{"name":"a","value":"1","crashApply":true}`), core.PlanOptions{
		Call: &core.CallContext{ParentChangeID: "comp1", ParentAction: "cfx.deploy.add", StepID: "s1", Retired: []string{"e1"}}})
	if err != nil {
		t.Fatal(err)
	}
	v, err := f.eng.Apply(ctx, alice, pl.PlanID, core.ApplyOptions{Call: &core.CallContext{ParentChangeID: "comp1", StepID: "s1"}})
	if err != nil || v.State != core.StateApplying {
		t.Fatalf("Apply = %+v, %v; want the change left applying", v, err)
	}
	f.restart()
	f.host.verifyCalls = nil
	f.recoverAll()
	want := []core.CallContext{{ParentChangeID: "comp1", ParentAction: "cfx.deploy.add", StepID: "s1", Retired: []string{"e1"}}}
	if !reflect.DeepEqual(f.host.verifyCalls, want) {
		t.Errorf("recovery's Verify saw %+v, want %+v", f.host.verifyCalls, want)
	}
}
