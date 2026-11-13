package lifecycle

import (
	"context"
	"fmt"
	"net/netip"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)

func w12bRequester(addr, exposure string) context.Context {
	ctx := core.WithClientPeer(context.Background(), core.Peer{Addr: netip.MustParseAddr(addr)})
	return core.WithProxied(ctx, core.Proxied{Client: netip.MustParseAddr(addr), Path: core.ProxyPath{ExposureID: exposure, Kind: "npm"}})
}

// w12bRowsHold reports whether any column of any row of table holds s.
func w12bRowsHold(t *testing.T, f *cfxFixture, table, s string) bool {
	t.Helper()
	rows, err := f.ctrlDB.Read().Query("SELECT * FROM " + table)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		for _, v := range vals {
			if strings.Contains(fmt.Sprintf("%s", v), s) {
				return true
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return false
}

func TestW12bCompositeStepsCarryTheRequester(t *testing.T) {
	f := newCfxFixture(t)
	pl, err := f.planC(refCfxAdd, `{"name":"web"}`)
	if err != nil {
		t.Fatal(err)
	}
	v, err := f.ctrl.Apply(w12bRequester("198.51.100.7", "exp9"), cfxRoot, pl.PlanID, core.ApplyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if done := f.await(v.ID, core.StateCompleted, core.StateReverted, core.StateFailed, core.StateNeedsRepair); done.State != core.StateCompleted {
		t.Fatalf("composite = %s, want completed", done.State)
	}
	got := f.kids.stepRequests()
	if len(got) == 0 {
		t.Fatal("no step call was recorded")
	}
	for _, r := range got {
		if !strings.HasSuffix(r, " 198.51.100.7 exp9") {
			t.Errorf("step call %q, want the requester 198.51.100.7 through exp9", r)
		}
	}
	for _, table := range []string{"core_composites", "core_composite_steps"} {
		for _, s := range []string{"198.51.100.7", "exp9"} {
			if w12bRowsHold(t, f, table, s) {
				t.Errorf("%s holds %q, want neither the client nor the exposure stored", table, s)
			}
		}
	}
}

func TestW12bARepairCarriesTheRepairer(t *testing.T) {
	f := newCfxFixture(t)
	f.host.setItem("web-a", "1")
	f.host.setItem("web-b", "2")
	f.host.failNext("web-b", 1)
	pl, err := f.planC(refCfxRemove, `{"name":"web"}`)
	if err != nil {
		t.Fatal(err)
	}
	v, err := f.ctrl.Apply(w12bRequester("198.51.100.7", "exp9"), cfxRoot, pl.PlanID, core.ApplyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if done := f.await(v.ID, core.StateNeedsRepair, core.StateCompleted, core.StateFailed); done.State != core.StateNeedsRepair {
		t.Fatalf("composite = %s, want needs_repair after step y failed", done.State)
	}
	before := len(f.kids.stepRequests())
	if _, err := f.ctrl.Repair(w12bRequester("203.0.113.5", "exp10"), cfxRoot, v.ID, core.RepairRequest{}); err != nil {
		t.Fatal(err)
	}
	if done := f.await(v.ID, core.StateCompleted, core.StateFailed, core.StateNeedsRepair); done.State != core.StateCompleted {
		t.Fatalf("composite after the repair = %s, want completed", done.State)
	}
	after := f.kids.stepRequests()[before:]
	if len(after) == 0 {
		t.Fatal("the repair made no step call")
	}
	for _, r := range after {
		if !strings.HasSuffix(r, " 203.0.113.5 exp10") {
			t.Errorf("step call after the repair %q, want the repairer 203.0.113.5 through exp10", r)
		}
	}
}

func TestW12bCompositeStepsCarryADirectClient(t *testing.T) {
	f := newCfxFixture(t)
	pl, err := f.planC(refCfxAdd, `{"name":"web"}`)
	if err != nil {
		t.Fatal(err)
	}
	ctx := core.WithClientPeer(context.Background(), core.Peer{Addr: netip.MustParseAddr("192.0.2.44")})
	v, err := f.ctrl.Apply(ctx, cfxRoot, pl.PlanID, core.ApplyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if done := f.await(v.ID, core.StateCompleted, core.StateReverted, core.StateFailed, core.StateNeedsRepair); done.State != core.StateCompleted {
		t.Fatalf("composite = %s, want completed", done.State)
	}
	got := f.kids.stepRequests()
	if len(got) == 0 {
		t.Fatal("no step call was recorded")
	}
	for _, r := range got {
		if !strings.HasSuffix(r, " 192.0.2.44 -") {
			t.Errorf("step call %q, want the direct client 192.0.2.44 and no exposure", r)
		}
	}
}
