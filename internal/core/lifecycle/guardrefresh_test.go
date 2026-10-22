package lifecycle

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"slices"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

var gfxRefresh = &core.GuardContext{AgentPeers: []netip.Addr{netip.MustParseAddr("192.0.2.30")}}

func TestApplyHandsGuardRefresh(t *testing.T) {
	f, h := newGfx(t)
	ctx := context.Background()
	pl := f.plan(alice, refGfxSet, `{"name":"a"}`)
	v, err := f.eng.Apply(ctx, alice, pl.PlanID, core.ApplyOptions{Call: &core.CallContext{GuardRefresh: gfxRefresh}})
	if err != nil || v.State != core.StatePending {
		t.Fatalf("Apply = %s, %v; want pending", v.State, err)
	}
	if n := f.count("SELECT count(*) FROM core_plans WHERE id = ? AND call IS NULL", pl.PlanID); n != 1 {
		t.Error("the apply's guard refresh was stored with the plan")
	}
	if _, err := f.eng.Revert(ctx, alice, v.ID); err != nil {
		t.Fatal(err)
	}
	// A plan is never given a guard refresh, even when its call carries one.
	if _, err := f.eng.Plan(ctx, alice, refGfxSet, json.RawMessage(`{"name":"b"}`),
		core.PlanOptions{Call: &core.CallContext{GuardRefresh: gfxRefresh}}); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range h.seen() {
		got = append(got, fmt.Sprintf("%s refreshed=%v", c.Method, c.Call.GuardRefresh == gfxRefresh))
	}
	// The plan; the apply's re-plan, snapshot, apply and verify; the revert;
	// the second plan.
	want := []string{
		"Plan refreshed=false",
		"Plan refreshed=true", "Snapshot refreshed=true", "Apply refreshed=true", "Verify refreshed=true",
		"Revert refreshed=false",
		"Plan refreshed=false",
	}
	if !slices.Equal(got, want) {
		t.Errorf("handler calls\n got %q\nwant %q", got, want)
	}
}

func TestApplyCallMismatchStillRefused(t *testing.T) {
	f, _ := newGfx(t)
	ctx := context.Background()
	parent := &core.CallContext{ParentChangeID: "01PARENT", ParentAction: "gfx.deploy.add", StepID: "a"}
	pl, err := f.eng.Plan(ctx, alice, refGfxSet, json.RawMessage(`{"name":"a"}`), core.PlanOptions{Call: parent})
	if err != nil {
		t.Fatal(err)
	}
	for _, call := range []*core.CallContext{
		{ParentChangeID: "01OTHER", ParentAction: "gfx.deploy.add", StepID: "a", GuardRefresh: gfxRefresh},
		{GuardRefresh: gfxRefresh},
	} {
		_, err := f.eng.Apply(ctx, alice, pl.PlanID, core.ApplyOptions{Call: call})
		wantCode(t, err, errcode.PlanStateChanged)
	}
}
