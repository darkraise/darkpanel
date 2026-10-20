package rollout

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/dispatch"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/update"
)

var _ Dispatcher = (*dispatch.Dispatcher)(nil)

// fakePlan is a core.update.apply plan the fake fleet made.
type fakePlan struct {
	host, version string
	expires       time.Time
}

// fakeFailure makes a host's next apply record a failed or interrupted
// change; with record the change still started an update operation.
type fakeFailure struct {
	state  string
	record bool
}

// fakeFleet is the controller's dispatcher with the agents behind it. It
// plans and applies core.update.apply as an agent engine does, keeping one
// change per plan whatever its state, and answers core.update.status from
// update records the test sets.
type fakeFleet struct {
	mu        sync.Mutex
	clock     core.Clock
	n         int
	offline   map[string]bool
	planErr   map[string]error       // every Plan of the host fails with it
	applyErr  map[string]error       // every Apply of the host fails with it
	statusErr map[string]error       // every status read of an operation of the host fails with it
	onApply   func()                 // runs at the start of every Apply, without the fleet's lock
	lose      map[string]bool        // the next Apply records its change and loses the answer
	failNext  map[string]fakeFailure // the next Apply records a failed change
	plans     map[string]fakePlan
	changes   map[string]core.ChangeView // by plan id
	changeOps map[string]string          // change id -> operation id
	lastOp    map[string]string          // host -> operation of its last change
	records   map[string][]core.UpdateTransaction
	calls     []string
}

func newFakeFleet(clock core.Clock) *fakeFleet {
	return &fakeFleet{
		clock: clock, offline: map[string]bool{}, planErr: map[string]error{}, applyErr: map[string]error{},
		statusErr: map[string]error{}, lose: map[string]bool{}, failNext: map[string]fakeFailure{},
		plans:   map[string]fakePlan{},
		changes: map[string]core.ChangeView{}, changeOps: map[string]string{}, lastOp: map[string]string{},
		records: map[string][]core.UpdateTransaction{},
	}
}

func (f *fakeFleet) call(format string, args ...any) {
	f.calls = append(f.calls, fmt.Sprintf(format, args...))
}

// callsOf lists the calls made to host.
func (f *fakeFleet) callsOf(host string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		if strings.Fields(c)[1] == host {
			out = append(out, c)
		}
	}
	return out
}

func offline(host string) error {
	return errcode.New(errcode.HostOffline, "host "+host+" is offline")
}

func (f *fakeFleet) Plan(_ context.Context, p core.Principal, hostID string, a core.ActionRef, input json.RawMessage) (core.PlanView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.call("plan %s", hostID)
	if p.Actor != core.ActorRollout || a != (core.ActionRef{Name: update.ActionApply, Version: 1}) {
		return core.PlanView{}, fmt.Errorf("fake: plan %s by %s", a, p.Actor)
	}
	if f.offline[hostID] {
		return core.PlanView{}, offline(hostID)
	}
	if err := f.planErr[hostID]; err != nil {
		return core.PlanView{}, err
	}
	var in core.UpdateApplyInput
	if err := json.Unmarshal(input, &in); err != nil || in.Force || in.Channel != core.ChannelStable {
		return core.PlanView{}, fmt.Errorf("fake: input %s", input)
	}
	f.n++
	id := fmt.Sprintf("P%d", f.n)
	expires := f.clock.Now().Add(lifecycle.PlanTTL)
	f.plans[id] = fakePlan{host: hostID, version: in.Version, expires: expires}
	return core.PlanView{PlanID: id, HostID: hostID, Action: a.Name, Version: a.Version, ExpiresAt: expires}, nil
}

func (f *fakeFleet) Apply(_ context.Context, p core.Principal, hostID string, a core.ActionRef, planID string, _ core.ApplyOptions) (core.ChangeView, error) {
	if f.onApply != nil {
		f.onApply()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.call("apply %s %s", hostID, planID)
	if p.Actor != core.ActorRollout {
		return core.ChangeView{}, fmt.Errorf("fake: apply by %s", p.Actor)
	}
	if f.offline[hostID] {
		return core.ChangeView{}, offline(hostID)
	}
	pl, ok := f.plans[planID]
	if !ok || pl.host != hostID {
		return core.ChangeView{}, errcode.New(errcode.PlanNotFound, "no plan "+planID)
	}
	if v, ok := f.changes[planID]; ok {
		return v, nil
	}
	if err := f.applyErr[hostID]; err != nil {
		return core.ChangeView{}, err
	}
	if !f.clock.Now().Before(pl.expires) {
		return core.ChangeView{}, errcode.New(errcode.PlanExpired, "the plan expired; plan again")
	}
	f.n++
	v := core.ChangeView{ID: fmt.Sprintf("C%d", f.n), HostID: hostID, Action: a.Name, Version: 1, State: core.StateCompleted}
	op := fmt.Sprintf("OP%d", f.n)
	tr := core.UpdateTransaction{ID: op, OperationID: op, Kind: core.UpdateKindUpdate, FromVersion: "1.2.0",
		ToVersion: pl.version, TargetVersion: pl.version, State: core.UpdatePending}
	failure, failed := f.failNext[hostID]
	delete(f.failNext, hostID)
	switch {
	case failed:
		v.State = failure.state
		v.Error = &core.ChangeError{Code: string(errcode.UpdateInProgress), Message: "update operation OP0 is not finished"}
		if failure.record {
			f.changeOps[v.ID], f.lastOp[hostID], f.records[op] = op, op, []core.UpdateTransaction{tr}
		}
	default:
		out, err := json.Marshal(tr)
		if err != nil {
			return core.ChangeView{}, err
		}
		v.Output = out
		f.changeOps[v.ID], f.lastOp[hostID], f.records[op] = op, op, []core.UpdateTransaction{tr}
	}
	f.changes[planID] = v
	if f.lose[hostID] {
		delete(f.lose, hostID)
		return core.ChangeView{}, errors.New("transport: stream reset")
	}
	return v, nil
}

func (f *fakeFleet) Read(_ context.Context, p core.Principal, hostID string, a core.ActionRef, input json.RawMessage) (json.RawMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var in core.UpdateStatusInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, err
	}
	f.call("status %s op=%s change=%s", hostID, in.OperationID, in.ChangeID)
	if p.Actor != core.ActorRollout || a != (core.ActionRef{Name: update.ActionStatus, Version: 1}) {
		return nil, fmt.Errorf("fake: read %s by %s", a, p.Actor)
	}
	if f.offline[hostID] {
		return nil, offline(hostID)
	}
	if err := f.statusErr[hostID]; err != nil && in.OperationID != "" {
		return nil, err
	}
	op := in.OperationID
	if in.ChangeID != "" {
		op = f.changeOps[in.ChangeID]
	}
	return json.Marshal(core.UpdateStatus{Transactions: append([]core.UpdateTransaction{}, f.records[op]...)})
}

// setOp replaces the records of host's last operation, newest first; each
// record's id becomes the operation id plus its index from the oldest.
func (f *fakeFleet) setOp(host string, trs ...core.UpdateTransaction) {
	f.mu.Lock()
	defer f.mu.Unlock()
	op := f.lastOp[host]
	for i := range trs {
		trs[i].ID = fmt.Sprintf("%s.%d", op, len(trs)-1-i)
		trs[i].OperationID = op
	}
	f.records[op] = trs
}

// purge forgets every plan that expired without a change, as the agent's
// purge and the dispatcher's plan index retention do.
func (f *fakeFleet) purge() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, pl := range f.plans {
		if _, applied := f.changes[id]; !applied && !f.clock.Now().Before(pl.expires) {
			delete(f.plans, id)
		}
	}
}

func (f *fakeFleet) setOffline(host string, off bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.offline[host] = off
}

// updateRecord is an update record of an operation.
func updateRecord(state, to, target string) core.UpdateTransaction {
	return core.UpdateTransaction{Kind: core.UpdateKindUpdate, FromVersion: "1.2.0", ToVersion: to, TargetVersion: target, State: state}
}

// driverEnv is a controller with hosts OWN (its own), A and B, all on 1.2.0,
// and a fake fleet.
type driverEnv struct {
	db    *store.DB
	clock *coretest.FakeClock
	fleet *fakeFleet
}

func newDriverEnv(t *testing.T) *driverEnv {
	t.Helper()
	e := &driverEnv{db: openDB(t), clock: coretest.NewFakeClock(testStart)}
	e.fleet = newFakeFleet(e.clock)
	addHost(t, e.db, "OWN", true, "1.2.0")
	addHost(t, e.db, "A", false, "1.2.0")
	addHost(t, e.db, "B", false, "1.2.0")
	return e
}

// start starts a driver, stopped when the test ends.
func (e *driverEnv) start(t *testing.T) *Driver {
	t.Helper()
	d, err := NewDriver(DriverConfig{DB: e.db, Dispatcher: e.fleet, Clock: e.clock})
	if err != nil {
		t.Fatal(err)
	}
	d.Start()
	t.Cleanup(d.Stop)
	return d
}

// passes runs n driver passes.
func (e *driverEnv) passes(n int) {
	for range n {
		e.clock.Advance(PollInterval)
	}
}

// want fails unless rollout id is in state with the given host states.
func (e *driverEnv) want(t *testing.T, id, state string, hosts ...string) record {
	t.Helper()
	r := mustLoad(t, e.db, id)
	if r.State != state || !slices.Equal(hostStates(r), hosts) {
		t.Fatalf("rollout = %s %v, want %s %v", r.State, hostStates(r), state, hosts)
	}
	return r
}

func TestNewDriverRequiresItsDependencies(t *testing.T) {
	e := newDriverEnv(t)
	for _, cfg := range []DriverConfig{
		{Dispatcher: e.fleet, Clock: e.clock},
		{DB: e.db, Clock: e.clock},
		{DB: e.db, Dispatcher: e.fleet},
	} {
		if _, err := NewDriver(cfg); err == nil {
			t.Errorf("NewDriver(%+v) succeeded", cfg)
		}
	}
}

func TestDriverUpdatesHostsInOrder(t *testing.T) {
	e := newDriverEnv(t)
	r := mustCreate(t, e.db, "1.3.0", "1.2.0", "A", "OWN")
	e.start(t)
	e.passes(1)
	got := e.want(t, r.ID, core.RolloutRunning, "OWN=updating", "A=waiting")
	if h := got.Hosts[0]; h.PlanID == nil || h.OperationID == nil || !h.FollowedAt.Equal(testStart.Add(PollInterval)) {
		t.Fatalf("host OWN = %+v, want its plan and operation stored", h)
	}
	if calls := e.fleet.callsOf("A"); len(calls) != 0 {
		t.Fatalf("host A was called before OWN committed: %v", calls)
	}
	e.passes(2)
	e.want(t, r.ID, core.RolloutRunning, "OWN=updating", "A=waiting")
	e.fleet.setOp("OWN", updateRecord(core.UpdateCommitted, "1.3.0", "1.3.0"))
	e.passes(1)
	e.want(t, r.ID, core.RolloutRunning, "OWN=committed", "A=updating")
	e.fleet.setOp("A", updateRecord(core.UpdateCommitted, "1.3.0", "1.3.0"))
	e.passes(1)
	done := e.want(t, r.ID, core.RolloutSucceeded, "OWN=committed", "A=committed")
	if done.Error != nil || !done.UpdatedAt.Equal(testStart.Add(5*PollInterval)) {
		t.Errorf("rollout = %+v", done)
	}
	want := []string{"plan OWN", "apply OWN P1", "status OWN op=OP2 change=", "status OWN op=OP2 change=",
		"status OWN op=OP2 change=", "status OWN op=OP2 change="}
	if calls := e.fleet.callsOf("OWN"); !slices.Equal(calls, want) {
		t.Errorf("calls to OWN = %v, want %v", calls, want)
	}
}

func TestDriverStopsAtTheFirstFailure(t *testing.T) {
	e := newDriverEnv(t)
	r := mustCreate(t, e.db, "1.3.0", "1.2.0", "OWN", "A", "B")
	e.start(t)
	e.passes(1)
	cause := updateRecord(core.UpdateRolledBack, "1.3.0", "1.3.0")
	cause.Error = &core.ErrorDetail{Code: string(errcode.UpdateInterrupted), Message: "the health check failed"}
	e.fleet.setOp("OWN", cause)
	e.passes(1)
	got := e.want(t, r.ID, core.RolloutFailed, "OWN=failed", "A=skipped", "B=skipped")
	if e := got.Hosts[0].Error; e == nil || e.Message != "the health check failed" {
		t.Errorf("host error = %+v", e)
	}
	if got.Error == nil || got.Error.Code != string(errcode.UpdateInterrupted) || !strings.Contains(got.Error.Message, "OWN") {
		t.Errorf("rollout error = %+v", got.Error)
	}
	e.passes(3)
	for _, h := range []string{"A", "B"} {
		if calls := e.fleet.callsOf(h); len(calls) != 0 {
			t.Errorf("calls to %s = %v, want none", h, calls)
		}
	}
}

func TestDriverRefusalsFailTheHost(t *testing.T) {
	agentInternal := errcode.New(errcode.Internal, "update: this process has no updater; update actions run on the agent")
	tests := []struct {
		name     string
		planErr  error
		applyErr error
		wantFail errcode.Code // "" when the host waits
	}{
		{"a refused plan", errcode.New(errcode.UpdateNotFound, "no release 1.3.0 in channel stable"), nil, errcode.UpdateNotFound},
		{"a plan answered internal", agentInternal, nil, errcode.Internal},
		{"an apply answered internal", nil, agentInternal, errcode.Internal},
		{"an uncoded plan error", errors.New("transport: connection reset"), nil, ""},
		{"an uncoded apply error", nil, errors.New("transport: connection reset"), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newDriverEnv(t)
			r := mustCreate(t, e.db, "1.3.0", "1.2.0", "OWN", "A")
			if tt.planErr != nil {
				e.fleet.planErr["OWN"] = tt.planErr
			}
			if tt.applyErr != nil {
				e.fleet.applyErr["OWN"] = tt.applyErr
			}
			e.start(t)
			e.passes(3)
			if tt.wantFail == "" {
				e.want(t, r.ID, core.RolloutRunning, "OWN=updating", "A=waiting")
				return
			}
			got := e.want(t, r.ID, core.RolloutFailed, "OWN=failed", "A=skipped")
			if got.Hosts[0].Error == nil || got.Hosts[0].Error.Code != string(tt.wantFail) {
				t.Errorf("host error = %+v, want %s", got.Hosts[0].Error, tt.wantFail)
			}
		})
	}
}

func TestDriverWaitsForAnOfflineHost(t *testing.T) {
	e := newDriverEnv(t)
	r := mustCreate(t, e.db, "1.3.0", "1.2.0", "OWN")
	e.fleet.setOffline("OWN", true)
	e.start(t)
	e.passes(200)
	e.want(t, r.ID, core.RolloutRunning, "OWN=updating")
	e.fleet.setOffline("OWN", false)
	e.passes(1)
	if h := mustLoad(t, e.db, r.ID).Hosts[0]; h.OperationID == nil {
		t.Fatalf("host = %+v, want its operation once it is back", h)
	}
	e.fleet.setOffline("OWN", true)
	e.passes(10)
	e.fleet.setOffline("OWN", false)
	e.fleet.setOp("OWN", updateRecord(core.UpdateCommitted, "1.3.0", "1.3.0"))
	e.passes(1)
	e.want(t, r.ID, core.RolloutSucceeded, "OWN=committed")
}

func TestDriverResumesAfterARestart(t *testing.T) {
	t.Run("before the stored plan was applied", func(t *testing.T) {
		e := newDriverEnv(t)
		r := mustCreate(t, e.db, "1.3.0", "1.2.0", "OWN")
		plan, err := e.fleet.Plan(context.Background(), core.Principal{Actor: core.ActorRollout}, "OWN",
			core.ActionRef{Name: update.ActionApply, Version: 1}, json.RawMessage(`{"version":"1.3.0","channel":"stable","force":false}`))
		if err != nil {
			t.Fatal(err)
		}
		exec(t, e.db, "UPDATE core_rollout_hosts SET state = 'updating', plan_id = ? WHERE rollout_id = ?", plan.PlanID, r.ID)
		e.start(t)
		e.passes(1)
		want := []string{"plan OWN", "apply OWN P1", "status OWN op=OP2 change="}
		if calls := e.fleet.callsOf("OWN"); !slices.Equal(calls, want) {
			t.Errorf("calls = %v, want the stored plan applied: %v", calls, want)
		}
	})
	t.Run("after the stored plan was applied", func(t *testing.T) {
		e := newDriverEnv(t)
		r := mustCreate(t, e.db, "1.3.0", "1.2.0", "OWN")
		e.fleet.lose["OWN"] = true
		d := e.start(t)
		e.passes(1)
		h := mustLoad(t, e.db, r.ID).Hosts[0]
		if h.PlanID == nil || h.OperationID != nil {
			t.Fatalf("host after a lost answer = %+v", h)
		}
		d.Stop()
		e.start(t)
		e.passes(1)
		h = mustLoad(t, e.db, r.ID).Hosts[0]
		if h.OperationID == nil || *h.OperationID != "OP2" {
			t.Fatalf("host after the restart = %+v, want the first change's operation", h)
		}
		if n := len(e.fleet.changes); n != 1 {
			t.Errorf("%d changes, want the one change of the plan", n)
		}
		e.fleet.setOp("OWN", updateRecord(core.UpdateCommitted, "1.3.0", "1.3.0"))
		e.passes(1)
		e.want(t, r.ID, core.RolloutSucceeded, "OWN=committed")
	})
	t.Run("while following the operation", func(t *testing.T) {
		e := newDriverEnv(t)
		r := mustCreate(t, e.db, "1.3.0", "1.2.0", "OWN", "A")
		d := e.start(t)
		e.passes(1)
		d.Stop()
		e.fleet.setOp("OWN", updateRecord(core.UpdateCommitted, "1.3.0", "1.3.0"))
		e.passes(3)
		e.want(t, r.ID, core.RolloutRunning, "OWN=updating", "A=waiting")
		e.start(t)
		e.passes(1)
		e.want(t, r.ID, core.RolloutRunning, "OWN=committed", "A=updating")
		want := []string{"plan OWN", "apply OWN P1", "status OWN op=OP2 change=", "status OWN op=OP2 change="}
		if calls := e.fleet.callsOf("OWN"); !slices.Equal(calls, want) {
			t.Errorf("calls to OWN = %v, want the stored operation polled without a new apply: %v", calls, want)
		}
	})
}

func TestDriverReplansAnExpiredPlan(t *testing.T) {
	for _, purged := range []bool{false, true} {
		t.Run(fmt.Sprintf("purged=%v", purged), func(t *testing.T) {
			e := newDriverEnv(t)
			r := mustCreate(t, e.db, "1.3.0", "1.2.0", "OWN")
			plan, err := e.fleet.Plan(context.Background(), core.Principal{Actor: core.ActorRollout}, "OWN",
				core.ActionRef{Name: update.ActionApply, Version: 1}, json.RawMessage(`{"version":"1.3.0","channel":"stable","force":false}`))
			if err != nil {
				t.Fatal(err)
			}
			exec(t, e.db, "UPDATE core_rollout_hosts SET state = 'updating', plan_id = ? WHERE rollout_id = ?", plan.PlanID, r.ID)
			e.clock.Advance(lifecycle.PlanTTL + time.Minute)
			if purged {
				e.fleet.purge()
			}
			e.start(t)
			e.passes(1)
			want := []string{"plan OWN", "apply OWN P1", "plan OWN", "apply OWN P2", "status OWN op=OP3 change="}
			if calls := e.fleet.callsOf("OWN"); !slices.Equal(calls, want) {
				t.Errorf("calls = %v, want %v", calls, want)
			}
			e.want(t, r.ID, core.RolloutRunning, "OWN=updating")
		})
	}
}

func TestDriverFailedApply(t *testing.T) {
	t.Run("without an update record", func(t *testing.T) {
		e := newDriverEnv(t)
		r := mustCreate(t, e.db, "1.3.0", "1.2.0", "OWN", "A")
		e.fleet.failNext["OWN"] = fakeFailure{state: core.StateFailed}
		e.start(t)
		e.passes(1)
		got := e.want(t, r.ID, core.RolloutFailed, "OWN=failed", "A=skipped")
		if h := got.Hosts[0]; h.Error == nil || h.Error.Code != string(errcode.UpdateInProgress) || h.OperationID != nil {
			t.Errorf("host = %+v", h)
		}
		if calls := e.fleet.callsOf("OWN"); !slices.Contains(calls, "status OWN op= change=C2") {
			t.Errorf("calls = %v, want the status of the failed change", calls)
		}
	})
	t.Run("with an update record", func(t *testing.T) {
		e := newDriverEnv(t)
		r := mustCreate(t, e.db, "1.3.0", "1.2.0", "OWN")
		e.fleet.failNext["OWN"] = fakeFailure{state: core.StateInterrupted, record: true}
		e.start(t)
		e.passes(1)
		got := e.want(t, r.ID, core.RolloutRunning, "OWN=updating")
		if h := got.Hosts[0]; h.OperationID == nil || *h.OperationID != "OP2" {
			t.Fatalf("host = %+v, want the operation the change started", h)
		}
		e.fleet.setOp("OWN", updateRecord(core.UpdateCommitted, "1.3.0", "1.3.0"))
		e.passes(1)
		e.want(t, r.ID, core.RolloutSucceeded, "OWN=committed")
	})
}

func TestDriverSteppedUpdateAdvancesOnlyOnTheTarget(t *testing.T) {
	e := newDriverEnv(t)
	r := mustCreate(t, e.db, "1.4.0", "1.2.0", "OWN", "A")
	e.start(t)
	e.passes(1)
	e.fleet.setOp("OWN", updateRecord(core.UpdateCommitted, "1.3.1", "1.4.0"))
	e.passes(2)
	e.want(t, r.ID, core.RolloutRunning, "OWN=updating", "A=waiting")
	e.fleet.setOp("OWN", updateRecord(core.UpdatePending, "1.4.0", "1.4.0"), updateRecord(core.UpdateCommitted, "1.3.1", "1.4.0"))
	e.passes(2)
	e.want(t, r.ID, core.RolloutRunning, "OWN=updating", "A=waiting")
	e.fleet.setOp("OWN", updateRecord(core.UpdateCommitted, "1.4.0", "1.4.0"), updateRecord(core.UpdateCommitted, "1.3.1", "1.4.0"))
	e.passes(1)
	e.want(t, r.ID, core.RolloutRunning, "OWN=committed", "A=updating")
}

func TestDriverTimesOutAHost(t *testing.T) {
	e := newDriverEnv(t)
	r := mustCreate(t, e.db, "1.3.0", "1.2.0", "OWN", "A")
	e.start(t)
	e.passes(1)
	e.passes(int(HostTimeout/PollInterval) - 1)
	e.want(t, r.ID, core.RolloutRunning, "OWN=updating", "A=waiting")
	e.passes(1)
	got := e.want(t, r.ID, core.RolloutDraining, "OWN=updating", "A=skipped")
	if got.Outcome != core.RolloutFailed || got.Error == nil || got.Error.Code != string(errcode.UpdateInterrupted) {
		t.Errorf("rollout = %+v", got)
	}
	_, err := createRollout(e.db, core.RolloutInput{Hosts: []string{"A"}, Version: "1.2.0", Channel: core.ChannelStable}, "1.2.0")
	wantCode(t, err, errcode.RolloutInProgress)
	e.passes(5)
	e.fleet.setOp("OWN", updateRecord(core.UpdateCommitted, "1.3.0", "1.3.0"))
	e.passes(1)
	e.want(t, r.ID, core.RolloutFailed, "OWN=committed", "A=skipped")
	if calls := e.fleet.callsOf("A"); len(calls) != 0 {
		t.Errorf("calls to A = %v, want none", calls)
	}
}

func TestDriverFollowWaitsThroughAStatusRefusal(t *testing.T) {
	for _, refusal := range []error{
		errcode.New(errcode.Internal, "update: read the update records"),
		errcode.New(errcode.NotFound, "no update operation OP2"),
	} {
		t.Run(string(errcode.From(refusal).Code), func(t *testing.T) {
			e := newDriverEnv(t)
			r := mustCreate(t, e.db, "1.3.0", "1.2.0", "OWN", "A")
			e.start(t)
			e.passes(1)
			e.fleet.mu.Lock()
			e.fleet.statusErr["OWN"] = refusal
			e.fleet.mu.Unlock()
			e.passes(3)
			e.want(t, r.ID, core.RolloutRunning, "OWN=updating", "A=waiting")
			e.passes(int(HostTimeout/PollInterval) - 3)
			got := e.want(t, r.ID, core.RolloutDraining, "OWN=updating", "A=skipped")
			if got.Outcome != core.RolloutFailed || got.Error == nil || got.Error.Code != string(errcode.UpdateInterrupted) {
				t.Errorf("rollout = %+v", got)
			}
			e.fleet.mu.Lock()
			delete(e.fleet.statusErr, "OWN")
			e.fleet.mu.Unlock()
			e.fleet.setOp("OWN", updateRecord(core.UpdateCommitted, "1.3.0", "1.3.0"))
			e.passes(1)
			e.want(t, r.ID, core.RolloutFailed, "OWN=committed", "A=skipped")
		})
	}
}

func TestDriverCancel(t *testing.T) {
	t.Run("while a host updates", func(t *testing.T) {
		e := newDriverEnv(t)
		r := mustCreate(t, e.db, "1.3.0", "1.2.0", "OWN", "A")
		e.start(t)
		e.passes(1)
		eng := newEngine(t, e.db, e.clock, "1.2.0")
		if _, err := planApply(eng, ActionRolloutCancel, `{"rolloutId":"`+r.ID+`","force":false}`, r.ID); err != nil {
			t.Fatal(err)
		}
		e.passes(3)
		e.want(t, r.ID, core.RolloutDraining, "OWN=updating", "A=skipped")
		e.fleet.setOp("OWN", updateRecord(core.UpdateCommitted, "1.3.0", "1.3.0"))
		e.passes(1)
		got := e.want(t, r.ID, core.RolloutCancelled, "OWN=committed", "A=skipped")
		if got.Error != nil {
			t.Errorf("rollout error = %+v", got.Error)
		}
		if calls := e.fleet.callsOf("A"); len(calls) != 0 {
			t.Errorf("calls to A = %v, want none", calls)
		}
	})
	t.Run("with force", func(t *testing.T) {
		e := newDriverEnv(t)
		r := mustCreate(t, e.db, "1.3.0", "1.2.0", "OWN", "A")
		e.start(t)
		e.passes(1)
		e.fleet.setOffline("OWN", true)
		e.passes(2)
		eng := newEngine(t, e.db, e.clock, "1.2.0")
		if _, err := planApply(eng, ActionRolloutCancel, `{"rolloutId":"`+r.ID+`","force":true}`, r.ID); err != nil {
			t.Fatal(err)
		}
		before := len(e.fleet.callsOf("OWN"))
		e.passes(3)
		e.want(t, r.ID, core.RolloutCancelled, "OWN=failed", "A=skipped")
		if n := len(e.fleet.callsOf("OWN")); n != before {
			t.Errorf("the driver called OWN %d more times after a forced cancel", n-before)
		}
		mustCreate(t, e.db, "1.3.0", "1.2.0", "OWN")
	})
}

func TestDriverYieldsToACancelDuringACall(t *testing.T) {
	e := newDriverEnv(t)
	r := mustCreate(t, e.db, "1.3.0", "1.2.0", "OWN", "A")
	eng := newEngine(t, e.db, e.clock, "1.2.0")
	e.fleet.applyErr["OWN"] = errcode.New(errcode.UpdateInProgress, "update operation OP0 is not finished")
	e.fleet.onApply = func() {
		e.fleet.onApply = nil
		if h := mustLoad(t, e.db, r.ID).Hosts[0]; h.ApplySentAt.IsZero() {
			t.Errorf("host = %+v at its first apply, want the apply marked as sent", h)
		}
		if _, err := planApply(eng, ActionRolloutCancel, `{"rolloutId":"`+r.ID+`","force":false}`, r.ID); err != nil {
			t.Error(err)
		}
	}
	e.start(t)
	e.passes(1)
	got := e.want(t, r.ID, core.RolloutCancelled, "OWN=failed", "A=skipped")
	if got.Error != nil || got.Hosts[0].Error == nil || got.Hosts[0].Error.Code != string(errcode.UpdateInProgress) {
		t.Errorf("rollout error %+v, host error %+v; want the cancel's outcome and the host's refusal", got.Error, got.Hosts[0].Error)
	}
}

// TestDriverNeverAppliesAfterACancel covers a host whose plan was stored but
// never sent: a cancel without force ends the rollout at once, and a
// draining rollout skips such a host, so no apply reaches it.
func TestDriverNeverAppliesAfterACancel(t *testing.T) {
	storePlan := func(t *testing.T, e *driverEnv, id string) {
		t.Helper()
		plan, err := e.fleet.Plan(context.Background(), core.Principal{Actor: core.ActorRollout}, "OWN",
			core.ActionRef{Name: update.ActionApply, Version: 1}, json.RawMessage(`{"version":"1.3.0","channel":"stable","force":false}`))
		if err != nil {
			t.Fatal(err)
		}
		exec(t, e.db, "UPDATE core_rollout_hosts SET state = 'updating', plan_id = ? WHERE rollout_id = ? AND host_id = 'OWN'",
			plan.PlanID, id)
	}
	t.Run("a cancel without force", func(t *testing.T) {
		e := newDriverEnv(t)
		r := mustCreate(t, e.db, "1.3.0", "1.2.0", "OWN", "A")
		storePlan(t, e, r.ID)
		eng := newEngine(t, e.db, e.clock, "1.2.0")
		if _, err := planApply(eng, ActionRolloutCancel, `{"rolloutId":"`+r.ID+`","force":false}`, r.ID); err != nil {
			t.Fatal(err)
		}
		e.want(t, r.ID, core.RolloutCancelled, "OWN=skipped", "A=skipped")
		e.start(t)
		e.passes(3)
		if calls := e.fleet.callsOf("OWN"); !slices.Equal(calls, []string{"plan OWN"}) {
			t.Errorf("calls to OWN = %v, want only the plan", calls)
		}
	})
	t.Run("a draining rollout", func(t *testing.T) {
		e := newDriverEnv(t)
		r := mustCreate(t, e.db, "1.3.0", "1.2.0", "OWN", "A")
		storePlan(t, e, r.ID)
		exec(t, e.db, "UPDATE core_rollout_hosts SET state = 'skipped' WHERE rollout_id = ? AND host_id = 'A'", r.ID)
		exec(t, e.db, "UPDATE core_rollouts SET state = 'draining', outcome = 'cancelled' WHERE id = ?", r.ID)
		e.start(t)
		e.passes(1)
		e.want(t, r.ID, core.RolloutCancelled, "OWN=skipped", "A=skipped")
		if calls := e.fleet.callsOf("OWN"); !slices.Equal(calls, []string{"plan OWN"}) {
			t.Errorf("calls to OWN = %v, want only the plan", calls)
		}
	})
}

func TestDriverFailsAHostNoLongerEnrolled(t *testing.T) {
	tests := []struct {
		name   string
		change string
		code   errcode.Code
	}{
		{"revoked", "UPDATE core_hosts SET state = 'revoked' WHERE id = 'A'", errcode.InvalidInput},
		{"deleted", "DELETE FROM core_hosts WHERE id = 'A'", errcode.NotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newDriverEnv(t)
			r := mustCreate(t, e.db, "1.2.0", "1.2.0", "A", "B")
			exec(t, e.db, tt.change)
			e.start(t)
			e.passes(1)
			got := e.want(t, r.ID, core.RolloutFailed, "A=failed", "B=skipped")
			if h := got.Hosts[0]; h.Error == nil || h.Error.Code != string(tt.code) {
				t.Errorf("host A error = %+v, want %s", h.Error, tt.code)
			}
			if calls := e.fleet.callsOf("A"); len(calls) != 0 {
				t.Errorf("calls to A = %v, want none", calls)
			}
		})
	}
}

// lockedBuffer is a log destination safe for the driver's timer goroutine.
type lockedBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func (l *lockedBuffer) count(s string) int { return strings.Count(l.String(), s) }

func TestDriverWarnsOfAStuckDrainingRollout(t *testing.T) {
	e := newDriverEnv(t)
	r := mustCreate(t, e.db, "1.3.0", "1.2.0", "OWN", "A")
	var logs lockedBuffer
	d, err := NewDriver(DriverConfig{DB: e.db, Dispatcher: e.fleet, Clock: e.clock,
		Log: slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn}))})
	if err != nil {
		t.Fatal(err)
	}
	d.Start()
	t.Cleanup(d.Stop)
	const warning = "a draining rollout is still waiting"
	e.passes(int(HostTimeout / PollInterval))
	e.want(t, r.ID, core.RolloutRunning, "OWN=updating", "A=waiting")
	if n := logs.count(warning); n != 0 {
		t.Fatalf("%d warnings while the rollout still runs, want none", n)
	}
	e.passes(1)
	e.want(t, r.ID, core.RolloutDraining, "OWN=updating", "A=skipped")
	e.passes(int(HostTimeout/PollInterval) - 1)
	if n := logs.count(warning); n != 1 {
		t.Fatalf("%d warnings in the first HostTimeout of draining, want 1", n)
	}
	e.passes(1)
	if n := logs.count(warning); n != 2 {
		t.Fatalf("%d warnings after another HostTimeout, want 2", n)
	}
	if n := logs.count("level=WARN"); n != 2 {
		t.Errorf("%d warnings in all, want 2:\n%s", n, logs.String())
	}
}

func TestDriverStop(t *testing.T) {
	e := newDriverEnv(t)
	d := e.start(t)
	d.Start()
	if n := e.clock.Pending(); n != 1 {
		t.Fatalf("pending timers = %d after two Starts, want 1", n)
	}
	d.Stop()
	if n := e.clock.Pending(); n != 0 {
		t.Fatalf("pending timers = %d after Stop, want 0", n)
	}
	r := mustCreate(t, e.db, "1.3.0", "1.2.0", "OWN")
	d.Start()
	e.passes(2)
	e.want(t, r.ID, core.RolloutRunning, "OWN=waiting")
}
