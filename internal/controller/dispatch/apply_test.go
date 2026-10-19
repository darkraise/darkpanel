package dispatch

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync/atomic"
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

func TestApplyRefusedForAnotherPrincipalKeepsThePlanMarked(t *testing.T) {
	h, d := newDispatcher(t)
	ctx := context.Background()
	pl := h.plan(d, testHost, refSet, `{"name":"a","value":"1"}`)
	mallory := core.Principal{Actor: "user:mallory", UserID: "mallory", Scope: core.ScopeOperate, Source: "192.0.2.9"}
	_, err := d.Apply(ctx, mallory, testHost, refSet, pl.PlanID, core.ApplyOptions{})
	_ = wantCode(t, err, errcode.PlanPrincipalMismatch)
	if n := h.count("SELECT applied FROM core_plan_index WHERE plan_id = ?", pl.PlanID); n != 1 {
		t.Errorf("applied after another principal's refusal = %d, want 1", n)
	}
	if _, err := d.Apply(ctx, alice, testHost, refSet, pl.PlanID, core.ApplyOptions{}); err != nil {
		t.Errorf("Apply by the planner: %v", err)
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

// lostReply is an agent whose apply commits the change and then loses the
// reply, as a dropped connection would.
type lostReply struct{ core.AgentClient }

func (c lostReply) Apply(ctx context.Context, p core.Principal, planID string, opts core.ApplyOptions) (core.ChangeView, error) {
	if _, err := c.AgentClient.Apply(ctx, p, planID, opts); err != nil {
		return core.ChangeView{}, err
	}
	return core.ChangeView{}, errors.New("connection reset")
}

func TestApplyKeepsThePlanWhenTheReplyIsLost(t *testing.T) {
	h, d := newDispatcher(t)
	ctx := context.Background()
	pl := h.plan(d, testHost, refSet, `{"name":"a","value":"1"}`)
	h.hosts.Add(testHost, lostReply{h.rt})
	if _, err := d.Apply(ctx, alice, testHost, refSet, pl.PlanID, core.ApplyOptions{}); err == nil {
		t.Fatal("Apply with a lost reply succeeded")
	}
	if n := h.count("SELECT applied FROM core_plan_index WHERE plan_id = ?", pl.PlanID); n != 1 {
		t.Errorf("applied after a lost reply = %d, want 1", n)
	}
	h.hosts.Add(testHost, h.rt)
	h.clock.Advance(lifecycle.PlanTTL + PlanIndexRetention + time.Second)
	_ = h.plan(d, "", refCtlSet, `{"name":"n","value":"x"}`)
	v, err := d.Apply(ctx, alice, testHost, refSet, pl.PlanID, core.ApplyOptions{})
	if err != nil || v.ID == "" {
		t.Fatalf("repeated apply = %+v, %v; want the committed change", v, err)
	}
	if n := h.count("SELECT count(*) FROM core_change_index WHERE change_id = ?", v.ID); n != 1 {
		t.Errorf("index rows of the change = %d, want 1", n)
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

// gatedApply is an agent whose first apply waits for release and then fails
// without a change, as a busy lock on the host would; every later apply
// passes through, and committed is closed after the first of them returns.
type gatedApply struct {
	core.AgentClient
	entered, release, committed chan struct{}
	calls                       atomic.Int32
}

func (c *gatedApply) Apply(ctx context.Context, p core.Principal, planID string, opts core.ApplyOptions) (core.ChangeView, error) {
	switch c.calls.Add(1) {
	case 1:
		close(c.entered)
		<-c.release
		return core.ChangeView{}, errcode.New(errcode.LockHeld, "kv is locked")
	case 2:
		defer close(c.committed)
	}
	return c.AgentClient.Apply(ctx, p, planID, opts)
}

// TestConcurrentAppliesOfOnePlanKeepItMarked is register row 90. The first
// apply is released only once the second has either committed (no per-plan
// lock) or queued on the plan's lock, so neither outcome depends on timing.
func TestConcurrentAppliesOfOnePlanKeepItMarked(t *testing.T) {
	h, d := newDispatcher(t)
	ctx := context.Background()
	pl := h.plan(d, testHost, refSet, `{"name":"a","value":"1"}`)
	g := &gatedApply{AgentClient: h.rt, entered: make(chan struct{}), release: make(chan struct{}), committed: make(chan struct{})}
	h.hosts.Add(testHost, g)

	first := make(chan error, 1)
	go func() {
		_, err := d.Apply(ctx, alice, testHost, refSet, pl.PlanID, core.ApplyOptions{})
		first <- err
	}()
	select {
	case <-g.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the first apply never reached the agent")
	}
	type result struct {
		v   core.ChangeView
		err error
	}
	second := make(chan result, 1)
	go func() {
		v, err := d.Apply(ctx, alice, testHost, refSet, pl.PlanID, core.ApplyOptions{})
		second <- result{v, err}
	}()
	queued := func() bool {
		d.plans.mu.Lock()
		defer d.plans.mu.Unlock()
		l := d.plans.held[pl.PlanID]
		return l != nil && l.users == 2
	}
	deadline := time.After(5 * time.Second)
wait:
	for {
		select {
		case <-g.committed:
			break wait
		case <-deadline:
			t.Fatal("the second apply neither committed nor queued on the plan lock")
		case <-time.After(time.Millisecond):
			if queued() {
				break wait
			}
		}
	}
	close(g.release)

	select {
	case err := <-first:
		_ = wantCode(t, err, errcode.LockHeld)
	case <-time.After(5 * time.Second):
		t.Fatal("the first apply did not return")
	}
	var r result
	select {
	case r = <-second:
	case <-time.After(5 * time.Second):
		t.Fatal("the second apply did not return")
	}
	if r.err != nil || r.v.ID == "" {
		t.Fatalf("second apply = %+v, %v; want a committed change", r.v, r.err)
	}
	if n := h.count("SELECT applied FROM core_plan_index WHERE plan_id = ?", pl.PlanID); n != 1 {
		t.Errorf("applied after a failed and a committed concurrent apply = %d, want 1", n)
	}
}

func TestPlanLocksWaitForTheHolderOrTheContext(t *testing.T) {
	var l planLocks
	unlock, err := l.lock(context.Background(), "p1")
	if err != nil {
		t.Fatal(err)
	}
	ended, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := l.lock(ended, "p1"); !errors.Is(err, context.Canceled) {
		t.Errorf("lock of a held plan with an ended context: %v, want context.Canceled", err)
	}
	other, err := l.lock(context.Background(), "p2")
	if err != nil {
		t.Fatalf("lock of another plan while p1 is held: %v", err)
	}
	other()
	unlock()
	again, err := l.lock(context.Background(), "p1")
	if err != nil {
		t.Fatalf("lock of p1 after its unlock: %v", err)
	}
	again()
	if len(l.held) != 0 {
		t.Errorf("locks kept after every unlock: %v", l.held)
	}
}

type panicApply struct{ core.AgentClient }

func (panicApply) Apply(context.Context, core.Principal, string, core.ApplyOptions) (core.ChangeView, error) {
	panic("agent apply panicked")
}

func TestApplyPanicUnlocksPlan(t *testing.T) {
	h, d := newDispatcher(t)
	ctx := context.Background()
	pl := h.plan(d, testHost, refSet, `{"name":"a","value":"1"}`)
	h.hosts.Add(testHost, panicApply{h.rt})
	func() {
		defer func() { _ = recover() }()
		_, _ = d.Apply(ctx, alice, testHost, refSet, pl.PlanID, core.ApplyOptions{})
	}()
	d.plans.mu.Lock()
	n := len(d.plans.held)
	d.plans.mu.Unlock()
	if n != 0 {
		t.Errorf("plan locks kept after a panicking apply: %d, want 0", n)
	}
	tctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	unlock, err := d.plans.lock(tctx, pl.PlanID)
	if err != nil {
		t.Fatalf("lock of the plan after a panicking apply: %v", err)
	}
	unlock()
}
