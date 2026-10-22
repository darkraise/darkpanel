package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

func TestChildrenPlanApplyAndReadGoThroughTheDispatcher(t *testing.T) {
	h, d := newDispatcher(t)
	ctx := context.Background()
	var planned core.PlanOptions
	var applied core.ApplyOptions
	h.clients.Add(testHost, &coretest.FakeAgent{
		HelloFunc:   h.rt.Hello,
		ReadFunc:    h.rt.Read,
		ConfirmFunc: h.rt.Confirm,
		RevertFunc:  h.rt.Revert,
		ChangeFunc:  h.rt.Change,
		PlanFunc: func(ctx context.Context, p core.Principal, a core.ActionRef, input json.RawMessage, opts core.PlanOptions) (core.PlanView, error) {
			planned = opts
			return h.rt.Plan(ctx, p, a, input, opts)
		},
		ApplyFunc: func(ctx context.Context, p core.Principal, planID string, opts core.ApplyOptions) (core.ChangeView, error) {
			applied = opts
			return h.rt.Apply(ctx, p, planID, opts)
		},
	})
	kids := d.Children()
	call := &core.CallContext{ParentChangeID: "01COMPOSITE", ParentAction: "cfx.deploy.add", StepID: "a"}
	apply := func(name string) core.ChangeView {
		t.Helper()
		pl, err := kids.Plan(ctx, alice, testHost, refSet, json.RawMessage(`{"name":"`+name+`","value":"1"}`), core.PlanOptions{Call: call})
		if err != nil {
			t.Fatal(err)
		}
		if planned.Call == nil || !reflect.DeepEqual(*planned.Call, *call) {
			t.Errorf("the agent's plan got call %+v, want %+v", planned.Call, call)
		}
		if n := h.count("SELECT count(*) FROM core_plan_index WHERE plan_id = ?", pl.PlanID); n != 1 {
			t.Errorf("plan index rows = %d, want the child plan indexed", n)
		}
		v, err := kids.Apply(ctx, alice, testHost, refSet, pl.PlanID, core.ApplyOptions{Call: call})
		if err != nil {
			t.Fatal(err)
		}
		if applied.Call == nil || !reflect.DeepEqual(*applied.Call, *call) {
			t.Errorf("the agent's apply got call %+v, want %+v", applied.Call, call)
		}
		if n := h.count("SELECT count(*) FROM core_change_index WHERE change_id = ?", v.ID); n != 1 {
			t.Errorf("change index rows = %d, want the child change indexed", n)
		}
		return v
	}
	// kv.item.set is revertible: a pending change holds key kv until it is
	// confirmed, so the second apply follows the confirm.
	first := apply("a")
	if v, err := kids.Confirm(ctx, alice, first.ID); err != nil || v.State != core.StateConfirmed {
		t.Errorf("Confirm = %s, %v; want confirmed", v.State, err)
	}
	second := apply("b")
	if v, err := kids.Revert(ctx, alice, second.ID); err != nil || v.State != core.StateReverted {
		t.Errorf("Revert = %s, %v; want reverted", v.State, err)
	}
	out, err := kids.Read(ctx, alice, testHost, refGet, json.RawMessage(`{"name":"a"}`))
	if err != nil || !strings.Contains(string(out), `"value":"1"`) {
		t.Errorf("Read = %s, %v; want item a's value", out, err)
	}
}

func TestChildrenRouteAgentChangeOperations(t *testing.T) {
	h, d := newDispatcher(t)
	ctx := context.Background()
	var got []string
	h.clients.Add(testHost, &coretest.FakeAgent{
		HelloFunc: h.rt.Hello,
		UndoFunc: func(_ context.Context, p core.Principal, id string, c *core.CallContext) (core.ChangeView, error) {
			got = append(got, fmt.Sprintf("undo %s as %s parent %s step %s", id, p.Actor, c.ParentChangeID, c.StepID))
			return core.ChangeView{ID: id, HostID: testHost, State: core.StateUndoing}, nil
		},
		ReleaseFunc: func(_ context.Context, _ core.Principal, id, reason string, c *core.CallContext) (core.ReleaseResult, error) {
			got = append(got, fmt.Sprintf("release %s %q parent %s", id, reason, c.ParentChangeID))
			return core.ReleaseResult{Change: core.ChangeView{ID: id, HostID: testHost, State: core.StateAbandoned}}, nil
		},
		LookupFunc: func(_ context.Context, planID string, private bool) (core.PlanLookup, error) {
			got = append(got, fmt.Sprintf("lookup %s private %v", planID, private))
			return core.PlanLookup{Status: core.LookupAbsent}, nil
		},
		RetireFunc: func(_ context.Context, planID string) (core.PlanLookup, error) {
			got = append(got, "retire "+planID)
			return core.PlanLookup{Status: core.LookupRetired}, nil
		},
		AckPrivateFunc: func(_ context.Context, id string, final bool) error {
			got = append(got, fmt.Sprintf("ack %s final %t", id, final))
			return nil
		},
	})
	kids := d.Children()
	call := &core.CallContext{ParentChangeID: "01COMPOSITE", StepID: "a"}
	sys := core.Principal{Actor: "system:recovery", Scope: core.ScopeAdmin}
	if v, err := kids.Undo(ctx, sys, testHost, "01CHILD", call); err != nil || v.State != core.StateUndoing {
		t.Errorf("Undo = %+v, %v", v, err)
	}
	if r, err := kids.Release(ctx, sys, testHost, "01CHILD", "done", call); err != nil || r.Change.State != core.StateAbandoned {
		t.Errorf("Release = %+v, %v", r, err)
	}
	if lk, err := kids.Lookup(ctx, testHost, "01PLAN", true); err != nil || lk.Status != core.LookupAbsent {
		t.Errorf("Lookup = %+v, %v", lk, err)
	}
	if lk, err := kids.Retire(ctx, testHost, "01PLAN"); err != nil || lk.Status != core.LookupRetired {
		t.Errorf("Retire = %+v, %v", lk, err)
	}
	if err := kids.AckPrivate(ctx, testHost, "01CHILD", true); err != nil {
		t.Errorf("AckPrivate = %v", err)
	}
	want := []string{
		"undo 01CHILD as system:recovery parent 01COMPOSITE step a",
		`release 01CHILD "done" parent 01COMPOSITE`,
		"lookup 01PLAN private true",
		"retire 01PLAN",
		"ack 01CHILD final true",
	}
	if !slices.Equal(got, want) {
		t.Errorf("agent calls = %q, want %q", got, want)
	}
}

func TestChildrenRefuseAViewOfAnotherChange(t *testing.T) {
	h, d := newDispatcher(t)
	h.clients.Add(testHost, &coretest.FakeAgent{
		HelloFunc: h.rt.Hello,
		UndoFunc: func(context.Context, core.Principal, string, *core.CallContext) (core.ChangeView, error) {
			return core.ChangeView{ID: "01OTHER", HostID: testHost, State: core.StateUndoing}, nil
		},
	})
	_, err := d.Children().Undo(context.Background(), alice, testHost, "01CHILD", nil)
	if err == nil || !strings.Contains(err.Error(), "answered as change 01OTHER") {
		t.Errorf("Undo = %v, want a refusal of the other change's view", err)
	}
}

func TestChildrenRouteControllerChangeOperations(t *testing.T) {
	h, d := newDispatcher(t)
	ctx := context.Background()
	kids := d.Children()
	pl := h.plan(d, "", refCtlSet, `{"name":"n","value":"x"}`)
	if lk, err := kids.Lookup(ctx, "", pl.PlanID, false); err != nil || lk.Status != core.LookupAbsent {
		t.Fatalf("Lookup = %+v, %v; want absent", lk, err)
	}
	if lk, err := kids.Retire(ctx, "", pl.PlanID); err != nil || lk.Status != core.LookupRetired {
		t.Fatalf("Retire = %+v, %v; want retired", lk, err)
	}
	_, err := d.Apply(ctx, alice, "", refCtlSet, pl.PlanID, core.ApplyOptions{})
	_ = wantCode(t, err, errcode.PlanNotFound)
	_, err = kids.Undo(ctx, alice, "", "01MISSING", nil)
	_ = wantCode(t, err, errcode.ChangeNotFound)
	_, err = kids.Release(ctx, alice, "", "01MISSING", "done", nil)
	_ = wantCode(t, err, errcode.ChangeNotFound)
	_ = wantCode(t, kids.AckPrivate(ctx, "", "01MISSING", true), errcode.ChangeNotFound)
}
