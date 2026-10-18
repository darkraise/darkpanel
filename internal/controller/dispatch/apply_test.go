package dispatch

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/audit"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
)

// newAudited returns a dispatcher whose controller engine journals to a real
// audit store and which ingests the agent journal after each change.
func newAudited(t *testing.T) (*harness, *Dispatcher, *audit.Store) {
	t.Helper()
	h := newHarness(t)
	store := audit.New(h.db, h.clock)
	cfg := h.config(store)
	cfg.Ingest = audit.NewIngester(audit.IngesterConfig{Store: store, Agents: h.hosts, Clock: h.clock, Log: slog.New(slog.DiscardHandler)})
	d, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return h, d, store
}

func (h *harness) plan(d *Dispatcher, hostID string, ref core.ActionRef, input string) core.PlanView {
	h.t.Helper()
	v, err := d.Plan(context.Background(), alice, hostID, ref, json.RawMessage(input))
	if err != nil {
		h.t.Fatalf("Plan %s: %v", ref, err)
	}
	return v
}

func TestApplyRefusesAPlanOfAnotherHostOrAction(t *testing.T) {
	h, d := newDispatcher(t)
	ctx := context.Background()
	agentPlan := h.plan(d, testHost, refSet, `{"name":"a","value":"1"}`)
	ctlPlan := h.plan(d, "", refCtlSet, `{"name":"n","value":"x"}`)
	cases := []struct {
		name, host string
		ref        core.ActionRef
		planID     string
	}{
		{"agent plan as a controller action", "", refCtlSet, agentPlan.PlanID},
		{"agent plan as another action", testHost, refGet, agentPlan.PlanID},
		{"agent plan on another host", "h2", refSet, agentPlan.PlanID},
		{"controller plan as an agent action", testHost, refSet, ctlPlan.PlanID},
		{"unknown plan", testHost, refSet, "01UNKNOWN"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := d.Apply(ctx, alice, c.host, c.ref, c.planID, core.ApplyOptions{})
			_ = wantCode(t, err, errcode.PlanNotFound)
		})
	}
	if n := h.count("SELECT count(*) FROM core_change_index"); n != 0 {
		t.Errorf("index rows after refusals = %d, want 0", n)
	}
	if n := h.count("SELECT count(*) FROM core_plan_index WHERE applied = 1"); n != 0 {
		t.Errorf("plans marked applied after refusals = %d, want 0", n)
	}
	_, err := d.Apply(ctx, alice, testHost, refSet, "01UNKNOWN", core.ApplyOptions{})
	e := wantCode(t, err, errcode.PlanNotFound)
	if e.Detail != "no plan 01UNKNOWN for kv.item.set/v1 on host h1" {
		t.Errorf("detail = %q", e.Detail)
	}
	for _, c := range []struct {
		host string
		ref  core.ActionRef
		id   string
	}{{testHost, refSet, agentPlan.PlanID}, {"", refCtlSet, ctlPlan.PlanID}} {
		if _, err := d.Apply(ctx, alice, c.host, c.ref, c.id, core.ApplyOptions{}); err != nil {
			t.Errorf("Apply of %s: %v", c.ref, err)
		}
	}
}

func TestApplyRecordsTheChangeAndIngests(t *testing.T) {
	h, d, store := newAudited(t)
	ctx := context.Background()
	pl := h.plan(d, testHost, refSet, `{"name":"a","value":"1"}`)
	v, err := d.Apply(ctx, alice, testHost, refSet, pl.PlanID, core.ApplyOptions{})
	if err != nil || v.State != core.StatePending || v.HostID != testHost {
		t.Fatalf("Apply = %+v, %v; want pending on h1", v, err)
	}
	var host, action, created string
	var version int
	if err := h.db.Read().QueryRow("SELECT host_id, action, version, created_at FROM core_change_index WHERE change_id = ?", v.ID).
		Scan(&host, &action, &version, &created); err != nil {
		t.Fatalf("index row: %v", err)
	}
	if host != testHost || action != refSet.Name || version != 1 || created != "2026-10-17T12:00:00.000000000Z" {
		t.Errorf("index row = %s %s %d %s", host, action, version, created)
	}
	entries, _, err := store.List(ctx, core.AuditQuery{HostID: testHost})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Phase != "apply" || entries[0].ChangeID != v.ID ||
		entries[0].Actor != alice.Actor || entries[0].Source != alice.Source {
		t.Errorf("audit = %+v, want the apply of the change with alice's source", entries)
	}
	if left, err := h.rt.Journal(ctx, 0, 10); err != nil || len(left) != 0 {
		t.Errorf("agent journal after ingestion = %+v, %v; want acknowledged", left, err)
	}
	again, err := d.Apply(ctx, alice, testHost, refSet, pl.PlanID, core.ApplyOptions{})
	if err != nil || again.ID != v.ID {
		t.Errorf("repeated apply = %+v, %v; want the same change", again, err)
	}
	if n := h.count("SELECT count(*) FROM core_change_index"); n != 1 {
		t.Errorf("index rows = %d, want 1", n)
	}

	ctl := h.plan(d, "", refCtlSet, `{"name":"n","value":"x"}`)
	cv, err := d.Apply(ctx, alice, "", refCtlSet, ctl.PlanID, core.ApplyOptions{})
	if err != nil || cv.State != core.StateCompleted || cv.HostID != "" {
		t.Fatalf("controller apply = %+v, %v", cv, err)
	}
	if n := h.count("SELECT count(*) FROM core_change_index WHERE change_id = ?", cv.ID); n != 0 {
		t.Error("a controller-side change was indexed")
	}
	entries, _, err = store.List(ctx, core.AuditQuery{Action: refCtlSet.Name})
	if err != nil || len(entries) != 1 || entries[0].HostID != "" || entries[0].Source != alice.Source {
		t.Errorf("controller audit = %+v, %v; want one row with alice's source", entries, err)
	}
}

func TestApplyRefusesAModuleDisabledAfterThePlan(t *testing.T) {
	h, d := newDispatcher(t)
	ctx := context.Background()
	pl := h.plan(d, testHost, refSet, `{"name":"a","value":"1"}`)
	if _, err := h.db.Writer().Exec("UPDATE core_module_state SET enabled = 0 WHERE module_id = 'kv'"); err != nil {
		t.Fatal(err)
	}
	_, err := d.Apply(ctx, alice, testHost, refSet, pl.PlanID, core.ApplyOptions{})
	_ = wantCode(t, err, errcode.ModuleUnavailable)
	if n := h.count("SELECT applied FROM core_plan_index WHERE plan_id = ?", pl.PlanID); n != 0 {
		t.Errorf("applied after the module refusal = %d, want 0", n)
	}
	if _, err := h.db.Writer().Exec("UPDATE core_module_state SET enabled = 1 WHERE module_id = 'kv'"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Apply(ctx, alice, testHost, refSet, pl.PlanID, core.ApplyOptions{}); err != nil {
		t.Errorf("Apply once enabled again: %v", err)
	}
}

func TestApplyOfAnExpiredPlanIsExpired(t *testing.T) {
	h, d := newDispatcher(t)
	pl := h.plan(d, "", refCtlSet, `{"name":"n","value":"x"}`)
	h.clock.Advance(lifecycle.PlanTTL)
	_, err := d.Apply(context.Background(), alice, "", refCtlSet, pl.PlanID, core.ApplyOptions{})
	_ = wantCode(t, err, errcode.PlanExpired)
	if n := h.count("SELECT applied FROM core_plan_index WHERE plan_id = ?", pl.PlanID); n != 0 {
		t.Errorf("applied after a failed apply = %d, want 0", n)
	}
}

func TestApplyRepeatsAfterARestartAndPastTheRetention(t *testing.T) {
	h, d := newDispatcher(t)
	ctx := context.Background()
	type applied struct {
		host             string
		ref              core.ActionRef
		planID, changeID string
	}
	done := []applied{
		{host: testHost, ref: refSet, planID: h.plan(d, testHost, refSet, `{"name":"a","value":"1"}`).PlanID},
		{host: "", ref: refCtlSet, planID: h.plan(d, "", refCtlSet, `{"name":"n","value":"x"}`).PlanID},
	}
	for i, a := range done {
		v, err := d.Apply(ctx, alice, a.host, a.ref, a.planID, core.ApplyOptions{})
		if err != nil {
			t.Fatalf("Apply of %s: %v", a.ref, err)
		}
		done[i].changeID = v.ID
	}
	if n := h.count("SELECT count(*) FROM core_plan_index WHERE applied = 1"); n != 2 {
		t.Errorf("plans marked applied = %d, want 2", n)
	}
	// A dispatcher started later over the same database and agent.
	restarted, err := New(d.cfg)
	if err != nil {
		t.Fatal(err)
	}
	again := func(when string) {
		t.Helper()
		for _, a := range done {
			v, err := restarted.Apply(ctx, alice, a.host, a.ref, a.planID, core.ApplyOptions{})
			if err != nil || v.ID != a.changeID {
				t.Errorf("%s: repeated apply of %s = %+v, %v; want change %s", when, a.ref, v, err, a.changeID)
			}
		}
	}
	again("after a restart")
	// A refused repeat must not clear the flag, or the purge below would
	// drop the plans and alice's next repeat would be plan.not_found.
	mallory := core.Principal{Actor: "user:mallory", UserID: "mallory", Scope: core.ScopeOperate, Source: "192.0.2.9"}
	for _, a := range done {
		_, err := restarted.Apply(ctx, mallory, a.host, a.ref, a.planID, core.ApplyOptions{})
		_ = wantCode(t, err, errcode.PlanPrincipalMismatch)
	}
	h.clock.Advance(lifecycle.PlanTTL + PlanIndexRetention + time.Second)
	_ = h.plan(restarted, "", refCtlSet, `{"name":"n","value":"y"}`)
	again("past the retention")
}

func TestApplyOfAnUnappliedPlanPastItsRetentionIsNotFound(t *testing.T) {
	h, d := newDispatcher(t)
	pl := h.plan(d, testHost, refSet, `{"name":"a","value":"1"}`)
	h.clock.Advance(lifecycle.PlanTTL + PlanIndexRetention)
	_ = h.plan(d, "", refCtlSet, `{"name":"n","value":"x"}`)
	if n := h.count("SELECT count(*) FROM core_plan_index WHERE plan_id = ?", pl.PlanID); n != 0 {
		t.Errorf("index rows of the old plan = %d, want 0", n)
	}
	_, err := d.Apply(context.Background(), alice, testHost, refSet, pl.PlanID, core.ApplyOptions{})
	e := wantCode(t, err, errcode.PlanNotFound)
	if e.Detail != "no plan "+pl.PlanID+" for kv.item.set/v1 on host h1" {
		t.Errorf("detail = %q", e.Detail)
	}
}
