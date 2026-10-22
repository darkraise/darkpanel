package lifecycle

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// restart closes the engine and the database and opens both again on the
// same file, as a restarted agent does.
func (f *fixture) restart() {
	f.t.Helper()
	f.eng.Close()
	if err := f.db.Close(); err != nil {
		f.t.Fatal(err)
	}
	f.open()
}

// crash plans and applies an input whose handler stops the run's goroutine in
// Apply, which stands for the process dying after the intent is persisted and
// before the result is recorded: Apply answers the change still applying. It
// returns the plan and change ids.
func (f *fixture) crash(ref core.ActionRef, input string, opts core.ApplyOptions) (planID, changeID string) {
	f.t.Helper()
	planID = f.plan(alice, ref, input).PlanID
	v, err := f.eng.Apply(context.Background(), alice, planID, opts)
	if err != nil || v.State != core.StateApplying {
		f.t.Fatalf("Apply = %+v, %v; want the change left applying", v, err)
	}
	return planID, v.ID
}

// recoverAll runs Recover and waits until its background pass has
// finished every claimed change.
func (f *fixture) recoverAll() {
	f.t.Helper()
	if err := f.eng.Recover(context.Background()); err != nil {
		f.t.Fatalf("Recover: %v", err)
	}
	f.eng.recoveryPass.Wait()
}

func TestRecoverRevertsAnInterruptedRevertibleApply(t *testing.T) {
	f := newFixture(t)
	f.host.items["a"] = "old"
	planID, id := f.crash(refSet, `{"name":"a","value":"1","crashApply":true}`, core.ApplyOptions{})
	if s := f.state(id); s != core.StateApplying || f.count("SELECT count(*) FROM core_locks WHERE owner = ?", id) != 1 {
		t.Fatalf("after the crash: state %s, want applying with its lock", s)
	}
	f.host.items["a"] = "half" // the mutation had begun
	f.restart()
	f.recoverAll()

	if s := f.state(id); s != core.StateReverted || f.host.items["a"] != "old" {
		t.Errorf("state %s, item a %q; want reverted to old", s, f.host.items["a"])
	}
	if got := f.phases(); !reflect.DeepEqual(got, []string{"recover/ok"}) {
		t.Errorf("journal = %v", got)
	}
	entries, _ := f.journal.Entries(context.Background(), 0, 10)
	if entries[0].Actor != ActorRecovery {
		t.Errorf("recovery actor = %s, want %s", entries[0].Actor, ActorRecovery)
	}
	if f.count("SELECT count(*) FROM core_locks") != 0 || !f.planInputCleared(planID) {
		t.Error("recovery kept the lock or the plan's sealed input")
	}
}

func TestRecoverRevertsPendingChangesWhateverTheDeadline(t *testing.T) {
	f := newFixture(t)
	v := f.pending(`{"name":"a","value":"1"}`)
	f.restart()
	f.recoverAll()
	if _, ok := f.host.items["a"]; f.state(v.ID) != core.StateReverted || ok {
		t.Errorf("state %s, item present %v; want reverted", f.state(v.ID), ok)
	}
	if got := f.phases(); !reflect.DeepEqual(got, []string{"apply/ok", "recover/ok"}) {
		t.Errorf("journal = %v", got)
	}
	if f.clock.Pending() != 0 {
		t.Errorf("armed timers = %d, want 0", f.clock.Pending())
	}
}

func TestRecoverRecordsAFailedRevertAsNeedingRepair(t *testing.T) {
	f := newFixture(t)
	v := f.pending(`{"name":"a","value":"1","failRevert":true}`)
	f.restart()
	f.recoverAll()
	c, err := f.eng.Change(context.Background(), v.ID)
	if err != nil {
		t.Fatal(err)
	}
	if c.State != core.StateNeedsRepair || c.Error == nil || c.Error.Code != string(errcode.NeedsRepair) ||
		c.Error.Cause != string(errcode.ChangeRevertFailed) || c.Owner != "" {
		t.Errorf("change = %+v (error %+v), want unowned needs_repair caused by change.revert_failed", c, c.Error)
	}
	if got := f.phases(); !reflect.DeepEqual(got, []string{"apply/ok", "recover/error/core.needs_repair"}) {
		t.Errorf("journal = %v", got)
	}
	if f.count("SELECT count(*) FROM core_locks WHERE owner = ?", v.ID) != 1 {
		t.Error("recovery released the lock of a change that needs repair")
	}
}

func TestRecoverRecordsARevertWhoseContextEnded(t *testing.T) {
	f := newFixture(t)
	f.host.items["a"] = "old"
	v := f.pending(`{"name":"a","value":"1"}`)
	f.restart()
	f.host.onRevert = f.eng.stopRun
	// The pass runs on the engine's run context; cancelling it mid-revert
	// must still record the outcome.
	_ = f.eng.Recover(context.Background())
	f.eng.recoveryPass.Wait()
	if f.host.revertCtxErr != nil {
		t.Errorf("the revert handler saw its context end: %v", f.host.revertCtxErr)
	}
	if s := f.state(v.ID); s != core.StateReverted || f.host.items["a"] != "old" {
		t.Errorf("state %s, item a %q; want the revert recorded and item a old", s, f.host.items["a"])
	}
	if f.count("SELECT count(*) FROM core_locks WHERE owner = ?", v.ID) != 0 {
		t.Error("the recorded revert kept its lock")
	}
}

func TestRecoverInterruptsCompensableAndIrreversible(t *testing.T) {
	cases := []struct {
		name    string
		ref     core.ActionRef
		opts    core.ApplyOptions
		mutated bool // whether the host shows the change at restart
		verify  string
		compBy  string
	}{
		{"compensable, not applied", refAdd, core.ApplyOptions{}, false, "verify failed", refRemove.Name},
		{"irreversible, applied", refRemove, core.ApplyOptions{Confirmation: "a"}, true, "verify passed", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			ctx := context.Background()
			planID, id := f.crash(c.ref, `{"name":"a","value":"1","crashApply":true}`, c.opts)
			if c.mutated {
				f.host.items["a"] = "1"
			}
			f.restart()
			f.recoverAll()

			v, err := f.eng.Change(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if v.State != core.StateInterrupted || v.CompensatedBy != c.compBy || v.Error == nil ||
				v.Error.Code != string(errcode.Internal) || !strings.Contains(v.Error.Message, c.verify) {
				t.Errorf("change = %+v (error %+v), want interrupted reporting %q", v, v.Error, c.verify)
			}
			if got := f.phases(); !reflect.DeepEqual(got, []string{"interrupt/error/internal"}) {
				t.Errorf("journal = %v", got)
			}
			if f.count("SELECT count(*) FROM core_locks") != 0 || !f.planInputCleared(planID) {
				t.Error("recovery kept the lock or the plan's sealed input")
			}
			again, err := f.eng.Apply(ctx, alice, planID, c.opts)
			if err != nil || again.ID != id || again.State != core.StateInterrupted {
				t.Errorf("repeated apply = %+v, %v; want the interrupted change", again, err)
			}
			if f.host.applies != 0 {
				t.Errorf("handler applied %d times, want 0: recovery never retries", f.host.applies)
			}
		})
	}
}

func TestRecoverReleasesLocksOfFinishedChanges(t *testing.T) {
	f := newFixture(t)
	done := f.apply(alice, f.plan(alice, refAdd, `{"name":"a","value":"1"}`).PlanID, core.ApplyOptions{})
	for key, owner := range map[string]string{"stray": done.ID, "update": "update-txn-1"} {
		if _, err := f.db.Writer().Exec("INSERT INTO core_locks (key, owner) VALUES (?, ?)", key, owner); err != nil {
			t.Fatal(err)
		}
	}
	f.restart()
	f.recoverAll()
	var keys []string
	rows, err := f.db.Read().Query("SELECT key FROM core_locks ORDER BY key")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			t.Fatal(err)
		}
		keys = append(keys, k)
	}
	if !reflect.DeepEqual(keys, []string{"update"}) {
		t.Errorf("locks after recovery = %v, want only the update transaction's", keys)
	}
}

func TestRecoverOnACleanStartDoesNothing(t *testing.T) {
	f := newFixture(t)
	f.recoverAll()
	if got := f.phases(); len(got) != 0 {
		t.Errorf("journal = %v, want empty", got)
	}
}

func TestPurgeDeletesExpiredPlansWithoutAChange(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	unused := f.plan(alice, refAdd, `{"name":"a","value":"1"}`)
	applied := f.plan(alice, refAdd, `{"name":"b","value":"1"}`)
	f.apply(alice, applied.PlanID, core.ApplyOptions{})
	f.clock.Advance(PlanTTL)
	fresh := f.plan(alice, refAdd, `{"name":"c","value":"1"}`)
	if err := f.eng.Purge(ctx); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]int{unused.PlanID: 0, applied.PlanID: 1, fresh.PlanID: 1} {
		if n := f.count("SELECT count(*) FROM core_plans WHERE id = ?", id); n != want {
			t.Errorf("plan %s rows = %d, want %d", id, n, want)
		}
	}
	_, err := f.eng.Apply(ctx, alice, unused.PlanID, core.ApplyOptions{})
	wantCode(t, err, errcode.PlanNotFound)
}

// A plan that expires while its apply runs the handlers must survive a purge
// in that window, or the intent insert fails its foreign key.
func TestPurgeWaitsForARunningApply(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	pl := f.plan(alice, refSet, `{"name":"a","value":"1"}`)
	f.clock.Advance(PlanTTL - time.Second)
	purged := make(chan error, 1)
	f.host.onSnapshot = func() {
		f.clock.Advance(time.Second)
		go func() { purged <- f.eng.Purge(ctx) }()
		// Purge must wait for the apply; give one that does not the time to
		// delete the plan before the intent is inserted.
		select {
		case err := <-purged:
			purged <- err
		case <-time.After(100 * time.Millisecond):
		}
	}
	if _, err := f.eng.Apply(ctx, alice, pl.PlanID, core.ApplyOptions{}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	select {
	case err := <-purged:
		if err != nil {
			t.Fatalf("Purge: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Purge did not return after the apply finished")
	}
	if n := f.count("SELECT count(*) FROM core_plans WHERE id = ?", pl.PlanID); n != 1 {
		t.Errorf("plan rows = %d, want 1", n)
	}
}

// recoverExec runs one statement on the fixture's database.
func (f *fixture) recoverExec(query string, args ...any) {
	f.t.Helper()
	if _, err := f.db.Writer().Exec(query, args...); err != nil {
		f.t.Fatalf("%s: %v", query, err)
	}
}

// recoverSeed inserts a change of ref in state, owned by owner, as a
// previous process left it, and returns its id. planID names an existing
// plan; "" inserts a plan without input.
func (f *fixture) recoverSeed(planID string, ref core.ActionRef, class core.Class, state, owner string) string {
	f.t.Helper()
	now := f.clock.Now()
	ts := formatTime(now)
	if planID == "" {
		planID = f.eng.newID(now)
		f.recoverExec(`INSERT INTO core_plans (id, host_id, action, version, actor, input_hash, state_hash,
			diff_json, guards_json, confirmation, created_at, expires_at)
			VALUES (?, ?, ?, ?, ?, '', '', '[]', '[]', '', ?, ?)`,
			planID, testHost, ref.Name, ref.Version, alice.Actor, ts, formatTime(now.Add(PlanTTL)))
	}
	id := f.eng.newID(now)
	f.recoverExec(`INSERT INTO core_changes (id, plan_id, host_id, action, version, class, compensated_by, actor,
		state, input_json, recovery_owner, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, '', ?, ?, '{}', ?, ?, ?)`,
		id, planID, testHost, ref.Name, ref.Version, string(class), alice.Actor, state, owner, ts, ts)
	return id
}

// The fixture module "rcl" has one task action; claims read only its
// declaration, so its handlers do nothing.
var refRclRun = core.ActionRef{Name: "rcl.job.run", Version: 1}

type rclJob struct{}

func (rclJob) Plan(context.Context, core.Env, struct{}) (core.PlanOutcome, error) {
	return core.PlanOutcome{}, nil
}

func (rclJob) Task(context.Context, core.Env, struct{}) (struct{}, error) { return struct{}{}, nil }

func (rclJob) Verify(context.Context, core.Env, struct{}, struct{}) error { return nil }

type rclModule struct{}

func (rclModule) Manifest() core.Manifest {
	return core.Manifest{ID: "rcl", Version: 1, Actions: []core.ActionDef{
		core.DefineTask(core.ActionSpec{
			Name: refRclRun.Name, Version: 1, Class: core.ClassIrreversible, Side: core.SideAgent,
			LockKeys: []string{"rcl"}, ApplyTimeout: time.Minute,
		}, rclJob{}),
	}}
}

func TestRecoverClaims(t *testing.T) {
	f := newRecoveryFixture(t, rcvModule{&rcvHost{items: map[string]string{}}}, rclModule{})
	ctx := context.Background()
	cases := []struct {
		name         string
		ref          core.ActionRef
		class        core.Class
		state, owner string
		retry        bool
		repairs      bool
		unresolved   bool
		kind         recoverKind // "" when nothing claims the change
		wantState    string
		wantOwner    string
	}{
		{name: "a composite", ref: core.ActionRef{Name: "cfx.deploy.add", Version: 1}, class: core.ClassComposite,
			state: core.StateApplying, owner: core.OwnerComposite, wantState: core.StateApplying, wantOwner: core.OwnerComposite},
		{name: "a task change applying", ref: refRclRun, class: core.ClassIrreversible, state: core.StateApplying,
			owner: core.OwnerEngine, kind: recoverTask, wantState: core.StateRecovering, wantOwner: core.OwnerModule},
		{name: "a task change recovering", ref: refRclRun, class: core.ClassIrreversible, state: core.StateRecovering,
			owner: core.OwnerModule, kind: recoverTask, wantState: core.StateRecovering, wantOwner: core.OwnerModule},
		{name: "a revertible change applying", ref: refSet, class: core.ClassRevertible, state: core.StateApplying,
			owner: core.OwnerEngine, kind: recoverRevert, wantState: core.StateApplying, wantOwner: core.OwnerEngine},
		{name: "a revertible change pending", ref: refSet, class: core.ClassRevertible, state: core.StatePending,
			kind: recoverRevert, wantState: core.StateApplying, wantOwner: core.OwnerEngine},
		{name: "a repair change applying", ref: refAdd, class: core.ClassCompensable, state: core.StateApplying,
			owner: core.OwnerEngine, repairs: true, kind: recoverSettle, wantState: core.StateApplying, wantOwner: core.OwnerEngine},
		{name: "an irreversible change applying", ref: refRemove, class: core.ClassIrreversible, state: core.StateApplying,
			kind: recoverSettle, wantState: core.StateApplying, wantOwner: core.OwnerEngine},
		{name: "a recovery handler cut off", ref: refRcvFix, class: core.ClassCompensable, state: core.StateRecovering,
			owner: core.OwnerModule, kind: recoverSettle, wantState: core.StateRecovering, wantOwner: core.OwnerEngine},
		{name: "an undo cut off", ref: refAdd, class: core.ClassCompensable, state: core.StateUndoing,
			owner: core.OwnerUndo, kind: recoverUndo, wantState: core.StateUndoing, wantOwner: core.OwnerUndo},
		{name: "a recovery handler to run again", ref: refRcvFix, class: core.ClassCompensable, state: core.StateNeedsRepair,
			retry: true, kind: recoverRetry, wantState: core.StateRecovering, wantOwner: core.OwnerModule},
		{name: "a retry over an unresolved effect", ref: refRcvFix, class: core.ClassCompensable, state: core.StateNeedsRepair,
			retry: true, unresolved: true, wantState: core.StateNeedsRepair},
		{name: "a resolution cut off", ref: refAdd, class: core.ClassCompensable, state: core.StateNeedsRepair,
			owner: core.OwnerResolve, wantState: core.StateNeedsRepair},
		{name: "a target under repair", ref: refSet, class: core.ClassRevertible, state: core.StateNeedsRepair,
			owner: core.OwnerRepair, wantState: core.StateNeedsRepair, wantOwner: core.OwnerRepair},
		{name: "quarantined", ref: refAdd, class: core.ClassCompensable, state: core.StateQuarantined,
			wantState: core.StateQuarantined},
		{name: "completed", ref: refAdd, class: core.ClassCompensable, state: core.StateCompleted,
			wantState: core.StateCompleted},
	}
	ids := make([]string, len(cases))
	for i, c := range cases {
		ids[i] = f.recoverSeed("", c.ref, c.class, c.state, c.owner)
		if c.retry {
			f.recoverExec("UPDATE core_changes SET recovery_retry = 1 WHERE id = ?", ids[i])
		}
		if c.repairs {
			f.recoverExec("UPDATE core_plans SET repairs = '01TARGET' WHERE id = (SELECT plan_id FROM core_changes WHERE id = ?)", ids[i])
		}
		if c.unresolved {
			ts := formatTime(f.clock.Now())
			f.recoverExec(`INSERT INTO core_effects (change_id, seq, kind, resource, local, inverse_local, before_json,
				state, reason, created_at, updated_at)
				VALUES (?, 1, 'modify', 'rcv.item:a', 1, 1, '{"state":{"value":""}}', 'unresolved', 'uncertain', ?, ?)`,
				ids[i], ts, ts)
		}
	}
	claims, err := f.eng.recoverClaims(ctx)
	if err != nil {
		t.Fatalf("recoverClaims: %v", err)
	}
	kinds := map[string]recoverClaim{}
	for _, c := range claims {
		kinds[c.id] = c
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var state, owner string
			if err := f.db.Read().QueryRow("SELECT state, recovery_owner FROM core_changes WHERE id = ?", ids[i]).
				Scan(&state, &owner); err != nil {
				t.Fatal(err)
			}
			if state != c.wantState || owner != c.wantOwner {
				t.Errorf("after the claims: %s owned by %q, want %s owned by %q", state, owner, c.wantState, c.wantOwner)
			}
			got, ok := kinds[ids[i]]
			switch {
			case c.kind == "" && ok:
				t.Errorf("claimed as %s, want no claim", got.kind)
			case c.kind != "" && (!ok || got.kind != c.kind || got.state != c.wantState || got.repair != c.repairs):
				t.Errorf("claim = %+v (found %v), want kind %s in %s with repair %v", got, ok, c.kind, c.wantState, c.repairs)
			}
		})
	}
}

func TestRecoverSweepKeepsLocksOfUnfinishedChanges(t *testing.T) {
	f := newFixture(t)
	kept := []string{
		core.StateApplying, core.StatePending, core.StateAwaitingConfirmation, core.StateUndoing,
		core.StateNeedsRepair, core.StateRecovering, core.StateReleasing, core.StateQuarantined,
	}
	var want []string
	for _, state := range kept {
		id := f.recoverSeed("", refAdd, core.ClassCompensable, state, "")
		f.recoverExec("INSERT INTO core_locks (key, owner) VALUES (?, ?)", "k-"+state, id)
		want = append(want, "k-"+state)
	}
	done := f.recoverSeed("", refAdd, core.ClassCompensable, core.StateCompleted, "")
	f.recoverExec("INSERT INTO core_locks (key, owner) VALUES ('k-done', ?)", done)
	owned := f.recoverSeed("", refAdd, core.ClassCompensable, core.StateFailed, core.OwnerEngine)
	f.recoverExec("INSERT INTO core_locks (key, owner) VALUES ('k-owned', ?)", owned)
	f.recoverExec("INSERT INTO core_locks (key, owner) VALUES ('k-update', ?)", ReservationOwner("01OPA"))
	want = append(want, "k-owned", "k-update")
	if err := f.eng.recoverSweep(context.Background()); err != nil {
		t.Fatalf("recoverSweep: %v", err)
	}
	rows, err := f.db.Read().Query("SELECT key FROM core_locks")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var got []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			t.Fatal(err)
		}
		got = append(got, k)
	}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("locks after the sweep = %v, want %v", got, want)
	}
}

func TestRecoverLeavesAQuarantinedChange(t *testing.T) {
	f := newFixture(t)
	id := f.recoverSeed("", refAdd, core.ClassCompensable, core.StateQuarantined, "")
	f.recoverExec("INSERT INTO core_locks (key, owner) VALUES ('fix', ?)", id)
	f.restart()
	f.recoverAll()
	v, err := f.eng.Change(context.Background(), id)
	if err != nil || v.State != core.StateQuarantined || v.Owner != "" {
		t.Errorf("quarantined change after restart = %+v, %v; want it untouched", v, err)
	}
	if n := f.count("SELECT count(*) FROM core_locks WHERE owner = ?", id); n != 1 {
		t.Errorf("quarantined change holds %d locks after restart, want 1", n)
	}
	if got := f.phases(); len(got) != 0 {
		t.Errorf("journal = %v, want empty", got)
	}
}

func TestRecoverRefusesMutationsUntilThePassStarts(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	input := json.RawMessage(`{"name":"c","value":"1"}`)
	spare := f.plan(alice, refAdd, `{"name":"b","value":"1"}`)
	id := f.recoverSeed("", refAdd, core.ClassCompensable, core.StateNeedsRepair, "")
	// Recover sets this from its start until its pass has started.
	f.eng.recovering.Store(true)
	calls := []struct {
		name string
		call func() error
	}{
		{"Plan", func() error { _, err := f.eng.Plan(ctx, alice, refAdd, input, core.PlanOptions{}); return err }},
		{"Apply", func() error { _, err := f.eng.Apply(ctx, alice, spare.PlanID, core.ApplyOptions{}); return err }},
		{"Undo", func() error { _, err := f.eng.Undo(ctx, alice, id, nil); return err }},
		{"Repair", func() error { _, err := f.eng.Repair(ctx, alice, id, core.RepairRequest{}); return err }},
		{"Release", func() error { _, err := f.eng.Release(ctx, alice, id, "the operator gave up", nil); return err }},
		{"Resolve", func() error {
			_, err := f.eng.Resolve(ctx, alice, id, 1, core.ResolveRequest{Resolution: core.ResolutionAccept})
			return err
		}},
	}
	for _, c := range calls {
		t.Run(c.name, func(t *testing.T) { wantCode(t, c.call(), errcode.Recovering) })
	}
	if _, err := f.eng.Change(ctx, id); err != nil {
		t.Errorf("Change while recovering = %v, want reads admitted", err)
	}
	f.recoverAll()
	if _, err := f.eng.Plan(ctx, alice, refAdd, input, core.PlanOptions{}); err != nil {
		t.Errorf("Plan after Recover = %v, want it admitted", err)
	}
}

func TestRecoverInterruptsAChangeOfAnUnregisteredAction(t *testing.T) {
	f := newFixture(t)
	gone := core.ActionRef{Name: "gone.item.put", Version: 1}
	applying := f.recoverSeed("", gone, core.ClassCompensable, core.StateApplying, core.OwnerEngine)
	f.recoverExec("INSERT INTO core_locks (key, owner) VALUES ('gone', ?)", applying)
	undoing := f.recoverSeed("", gone, core.ClassCompensable, core.StateUndoing, core.OwnerUndo)
	f.recoverExec("INSERT INTO core_locks (key, owner) VALUES ('gone-undo', ?)", undoing)
	pending := f.recoverSeed("", gone, core.ClassRevertible, core.StatePending, "")
	f.recoverExec("INSERT INTO core_locks (key, owner) VALUES ('gone-pending', ?)", pending)
	revertible := f.recoverSeed("", gone, core.ClassRevertible, core.StateApplying, core.OwnerEngine)
	recovering := f.recoverSeed("", gone, core.ClassCompensable, core.StateRecovering, core.OwnerModule)
	f.recoverAll()
	row := func(id string) (state, owner, msg string) {
		t.Helper()
		var m *string
		if err := f.db.Read().QueryRow("SELECT state, recovery_owner, error_message FROM core_changes WHERE id = ?", id).
			Scan(&state, &owner, &m); err != nil {
			t.Fatal(err)
		}
		if m != nil {
			msg = *m
		}
		return state, owner, msg
	}
	state, owner, msg := row(applying)
	if state != core.StateInterrupted || owner != "" ||
		!strings.Contains(msg, "verify not run: the action is not registered") {
		t.Errorf("applying change after Recover = %s owned by %q with %q, want interrupted, unowned, verify not run",
			state, owner, msg)
	}
	if n := f.count("SELECT count(*) FROM core_locks WHERE owner = ?", applying); n != 0 {
		t.Errorf("interrupted change holds %d locks, want 0", n)
	}
	for name, id := range map[string]string{
		"undoing": undoing, "pending": pending, "applying revertible": revertible, "recovering": recovering,
	} {
		if state, owner, msg := row(id); state != core.StateNeedsRepair || owner != "" || !strings.Contains(msg, gone.String()) {
			t.Errorf("%s change after Recover = %s owned by %q with %q, want needs_repair, unowned, naming %s",
				name, state, owner, msg, gone)
		}
	}
	for id, key := range map[string]string{undoing: "gone-undo", pending: "gone-pending"} {
		if n := f.count("SELECT count(*) FROM core_locks WHERE owner = ? AND key = ?", id, key); n != 1 {
			t.Errorf("change %s holds %d locks %s, want it kept", id, n, key)
		}
	}
	if _, err := f.eng.Release(context.Background(), alice, pending, "the module is gone", nil); err != nil {
		t.Errorf("Release of a change of an unregistered action: %v", err)
	}
}
