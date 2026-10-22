package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

func (f *fixture) uninstalling() int {
	f.t.Helper()
	return f.count("SELECT count(*) FROM core_meta WHERE key = ?", MetaUninstalling)
}

func TestBeginUninstallRefusesWhileWorkIsLive(t *testing.T) {
	f := newTaskFixture(t)
	ctx := context.Background()
	v := f.taskApplyStart(refTskInstall, `{"name":"a","value":"1"}`)
	name := TaskUnitName(v.ID, core.TaskOpApply, 1)
	f.taskPoll(func() {
		f.units.set(name, true)
		f.taskRowTo(v.ID, core.TaskOpApply, 1, taskRowRunning, "", "", "")
	})
	err := BeginUninstall(ctx, f.db, false)
	wantCode(t, err, errcode.ChangeBusy)
	if msg := errcode.From(err).Detail; !strings.Contains(msg, "change "+v.ID+" is owned by engine") ||
		!strings.Contains(msg, "task unit "+name+" is running") {
		t.Errorf("refusal = %q, want it to name the owned change and the running unit", msg)
	}
	if n := f.uninstalling(); n != 0 {
		t.Errorf("a refused BeginUninstall left %d barrier rows", n)
	}
	for range 2 {
		if err := BeginUninstall(ctx, f.db, true); err != nil {
			t.Fatalf("BeginUninstall with stopTasks = %v, want nil", err)
		}
	}
	if n := f.uninstalling(); n != 1 {
		t.Errorf("barrier rows = %d, want 1", n)
	}
}

func TestUninstallingGatesTheEngine(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	input := json.RawMessage(`{"name":"c","value":"1"}`)
	spare := f.plan(alice, refAdd, `{"name":"b","value":"1"}`)
	done := f.apply(alice, f.plan(alice, refAdd, `{"name":"a","value":"1"}`).PlanID, core.ApplyOptions{})
	if err := BeginUninstall(ctx, f.db, false); err != nil {
		t.Fatalf("BeginUninstall with nothing running = %v, want nil", err)
	}
	gated := []struct {
		name string
		call func() error
	}{
		{"Plan", func() error { _, err := f.eng.Plan(ctx, alice, refAdd, input, core.PlanOptions{}); return err }},
		{"Apply", func() error { _, err := f.eng.Apply(ctx, alice, spare.PlanID, core.ApplyOptions{}); return err }},
		{"Undo", func() error { _, err := f.eng.Undo(ctx, alice, done.ID, nil); return err }},
		{"Repair", func() error { _, err := f.eng.Repair(ctx, alice, done.ID, core.RepairRequest{}); return err }},
		{"Resolve", func() error {
			_, err := f.eng.Resolve(ctx, alice, done.ID, 1, core.ResolveRequest{Resolution: core.ResolutionAccept})
			return err
		}},
	}
	for _, g := range gated {
		t.Run(g.name, func(t *testing.T) { wantCode(t, g.call(), errcode.Uninstalling) })
	}
	_, err := f.eng.Release(ctx, alice, done.ID, "the operator gave up", nil)
	var coded *errcode.Error
	if errors.As(err, &coded) && coded.Code == errcode.Uninstalling {
		t.Error("Release answered core.uninstalling; the barrier never refuses a release")
	}
	for range 2 {
		if err := AbortUninstall(ctx, f.db, "local:root@h1"); err != nil {
			t.Fatalf("AbortUninstall = %v", err)
		}
	}
	if _, err := f.eng.Plan(ctx, alice, refAdd, input, core.PlanOptions{}); err != nil {
		t.Errorf("Plan after the abort = %v, want it admitted", err)
	}
}

func TestRunTaskFenceRefusesWhileUninstalling(t *testing.T) {
	f := newTaskFixture(t)
	ctx := context.Background()
	v := f.taskApplyStart(refTskInstall, `{"name":"a","value":"1"}`)
	if err := BeginUninstall(ctx, f.db, true); err != nil {
		t.Fatal(err)
	}
	if err := f.eng.RunTask(ctx, v.ID, core.TaskOpApply, 1); err != nil {
		t.Fatalf("RunTask while uninstalling = %v, want nil", err)
	}
	f.tsk.mu.Lock()
	ran := len(f.tsk.envs)
	f.tsk.mu.Unlock()
	if got := f.taskRowState(v.ID, core.TaskOpApply, 1); got != taskRowStarting || ran != 0 {
		t.Errorf("row %s, Task ran %d times; want the row starting and no Task", got, ran)
	}
	if err := AbortUninstall(ctx, f.db, "local:root@h1"); err != nil {
		t.Fatal(err)
	}
	if err := f.eng.RunTask(ctx, v.ID, core.TaskOpApply, 1); err != nil {
		t.Fatal(err)
	}
	if got := f.taskRowState(v.ID, core.TaskOpApply, 1); got != taskRowSucceeded {
		t.Errorf("row after the abort = %s, want succeeded", got)
	}
}

func TestRecoverSkipsClaimsWhileUninstalling(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	v := f.apply(alice, f.plan(alice, refAdd, `{"name":"a","value":"1"}`).PlanID, core.ApplyOptions{})
	// What a process that died during this apply leaves.
	f.recoverExec("UPDATE core_changes SET state = ?, recovery_owner = ? WHERE id = ?",
		core.StateApplying, core.OwnerEngine, v.ID)
	wantCode(t, BeginUninstall(ctx, f.db, false), errcode.ChangeBusy)
	if err := BeginUninstall(ctx, f.db, true); err != nil {
		t.Fatal(err)
	}
	f.restart()
	log := f.logged()
	f.recoverAll()
	got, err := f.eng.Change(ctx, v.ID)
	if err != nil || got.State != core.StateApplying || got.Owner != core.OwnerEngine {
		t.Errorf("after recovery while uninstalling: %+v, %v; want the change left as it was", got, err)
	}
	if out := log.String(); !strings.Contains(out, "recovery claim skipped") || !strings.Contains(out, v.ID) {
		t.Errorf("log = %q, want the skipped claim of %s", out, v.ID)
	}
}

func TestAbortUninstallRecoversWhatStartupSkipped(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	v := f.apply(alice, f.plan(alice, refAdd, `{"name":"a","value":"1"}`).PlanID, core.ApplyOptions{})
	// What a process that died during this apply leaves.
	f.recoverExec("UPDATE core_changes SET state = ?, recovery_owner = ? WHERE id = ?",
		core.StateApplying, core.OwnerEngine, v.ID)
	if err := BeginUninstall(ctx, f.db, true); err != nil {
		t.Fatal(err)
	}
	f.restart()
	f.recoverAll()
	err := BeginUninstall(ctx, f.db, false)
	wantCode(t, err, errcode.ChangeBusy)
	if msg := errcode.From(err).Detail; !strings.Contains(msg, "change "+v.ID+" is owned by engine") ||
		!strings.Contains(msg, "a stopped process") {
		t.Errorf("refusal = %q, want it to name the stale owner of %s", msg, v.ID)
	}
	if err := f.eng.AbortUninstall(ctx); err != nil {
		t.Fatalf("AbortUninstall on the live engine = %v", err)
	}
	f.eng.recoveryPass.Wait()
	got, err := f.eng.Change(ctx, v.ID)
	if err != nil || got.State != core.StateInterrupted || got.Owner != "" {
		t.Errorf("after the abort: %+v, %v; want interrupted and unowned without another restart", got, err)
	}
	if n := f.uninstalling(); n != 0 {
		t.Errorf("barrier rows after the abort = %d, want 0", n)
	}
	if _, err := f.eng.Plan(ctx, alice, refAdd, json.RawMessage(`{"name":"b","value":"1"}`), core.PlanOptions{}); err != nil {
		t.Errorf("Plan after the abort = %v, want it admitted", err)
	}
	if err := f.eng.AbortUninstall(ctx); err != nil {
		t.Errorf("a second AbortUninstall = %v, want nil", err)
	}
}

// skippedAtStart leaves a change owned by a stopped process, sets the
// barrier as uninstall --stop-tasks does, and restarts, so start-up recovery
// skips the change. It returns the change's id.
func (f *fixture) skippedAtStart() string {
	f.t.Helper()
	ctx := context.Background()
	v := f.apply(alice, f.plan(alice, refAdd, `{"name":"a","value":"1"}`).PlanID, core.ApplyOptions{})
	f.recoverExec("UPDATE core_changes SET state = ?, recovery_owner = ? WHERE id = ?",
		core.StateApplying, core.OwnerEngine, v.ID)
	if err := BeginUninstall(ctx, f.db, true); err != nil {
		f.t.Fatal(err)
	}
	f.restart()
	f.recoverAll()
	got, err := f.eng.Change(ctx, v.ID)
	if err != nil || got.State != core.StateApplying || got.Owner != core.OwnerEngine {
		f.t.Fatalf("after start-up recovery under the barrier: %+v, %v; want the change left as it was", got, err)
	}
	return v.ID
}

func TestEngineNoticesTheBarrierClearedOnItsPoll(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	id := f.skippedAtStart()
	f.clock.Advance(uninstallPoll)
	if got, err := f.eng.Change(ctx, id); err != nil || got.State != core.StateApplying || got.Owner != core.OwnerEngine {
		t.Fatalf("after a poll with the barrier set: %+v, %v; want the change left as it was", got, err)
	}
	// darkpanel uninstall --abort clears the row with the package function,
	// which cannot reach the engine.
	if err := AbortUninstall(ctx, f.db, "local:root@h1"); err != nil {
		t.Fatal(err)
	}
	f.clock.Advance(uninstallPoll)
	f.eng.recoveryPass.Wait()
	got, err := f.eng.Change(ctx, id)
	if err != nil || got.State != core.StateInterrupted || got.Owner != "" {
		t.Errorf("a poll after the abort: %+v, %v; want interrupted and unowned without a restart", got, err)
	}
	f.eng.uninstallMu.Lock()
	left := len(f.eng.uninstallSkipped)
	f.eng.uninstallMu.Unlock()
	f.eng.timerMu.Lock()
	armed := f.eng.uninstallTimer != nil
	f.eng.timerMu.Unlock()
	if left != 0 || armed {
		t.Errorf("after the recovery: %d skipped ids recorded, poll armed %v; want none and no poll", left, armed)
	}
}

func TestAdmissionNoticesTheBarrierCleared(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	id := f.skippedAtStart()
	if err := AbortUninstall(ctx, f.db, "local:root@h1"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.eng.Plan(ctx, alice, refAdd, json.RawMessage(`{"name":"b","value":"1"}`), core.PlanOptions{}); err != nil {
		t.Fatalf("Plan after the abort = %v, want it admitted", err)
	}
	f.eng.recoveryPass.Wait()
	got, err := f.eng.Change(ctx, id)
	if err != nil || got.State != core.StateInterrupted || got.Owner != "" {
		t.Errorf("after the first admitted call: %+v, %v; want interrupted and unowned before any poll", got, err)
	}
}

func TestUninstallingRefusesAnIntentAdmittedBeforeTheBarrier(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	done := f.apply(alice, f.plan(alice, refAdd, `{"name":"a","value":"1"}`).PlanID, core.ApplyOptions{})
	spare := f.plan(alice, refAdd, `{"name":"b","value":"1"}`)
	// Both calls passed admit before uninstall set the barrier.
	if err := BeginUninstall(ctx, f.db, false); err != nil {
		t.Fatal(err)
	}
	_, _, err := f.eng.begin(ctx, alice, spare.PlanID, core.ApplyOptions{})
	wantCode(t, err, errcode.Uninstalling)
	_, _, _, err = f.eng.claimUndo(ctx, alice, done.ID, nil)
	wantCode(t, err, errcode.Uninstalling)
	if n := f.count("SELECT count(*) FROM core_changes WHERE recovery_owner <> ''"); n != 0 {
		t.Errorf("%d changes owned after their intents were refused under the barrier, want 0", n)
	}
}

func TestUninstallingRefusesAResolveAndARecoveryRepairAdmittedBeforeTheBarrier(t *testing.T) {
	ctx := context.Background()
	u := newUfx(t)
	c := u.unresolvedChange("a", u.clock.Now())
	// Both calls passed admit before uninstall set the barrier.
	if err := BeginUninstall(ctx, u.db, false); err != nil {
		t.Fatal(err)
	}
	_, _, _, err := u.eng.claimResolve(ctx, c.ID, 1)
	wantCode(t, err, errcode.Uninstalling)
	if v, err := u.eng.Change(ctx, c.ID); err != nil || v.State != core.StateNeedsRepair || v.Owner != "" {
		t.Errorf("after the refused resolve claim: %+v, %v; want needs_repair and unowned", v, err)
	}

	f, h := newRcvFixture(t)
	h.script(core.RecoveryOutcome{}, errcode.New(errcode.Unavailable, "the package database is locked"))
	v := f.rcvApply(refRcvFix, `{"name":"a","value":"1","failBefore":true}`)
	if retry, _, _ := f.rcvFlags(v.ID); v.State != core.StateNeedsRepair || retry != 1 {
		t.Fatalf("change = %s with recovery_retry %d, want needs_repair with it set", v.State, retry)
	}
	if err := BeginUninstall(ctx, f.db, false); err != nil {
		t.Fatal(err)
	}
	_, err = f.eng.recovererRepair(ctx, v.ID)
	wantCode(t, err, errcode.Uninstalling)
	if got, err := f.eng.Change(ctx, v.ID); err != nil || got.State != core.StateNeedsRepair || got.Owner != "" {
		t.Errorf("after the refused recovery repair: %+v, %v; want needs_repair and unowned", got, err)
	}
	if calls, _, _ := h.recovered(); calls != 1 {
		t.Errorf("Recover calls = %d, want only the live failure's", calls)
	}
}

func TestEngineNoticesTheBarrierClearedAfterAFailedClaim(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	id := f.skippedAtStart()
	f.recoverExec(`CREATE TRIGGER fail_claim BEFORE UPDATE OF recovery_owner ON core_changes
		BEGIN SELECT RAISE(ABORT, 'injected claim failure'); END`)
	// logged replaces the engine; recovering again under the barrier gives
	// the logging engine the skipped id and the poll.
	log := f.logged()
	f.recoverAll()
	if err := AbortUninstall(ctx, f.db, "local:root@h1"); err != nil {
		t.Fatal(err)
	}
	f.clock.Advance(uninstallPoll)
	if got, err := f.eng.Change(ctx, id); err != nil || got.State != core.StateApplying || got.Owner != core.OwnerEngine {
		t.Fatalf("after a failed claim: %+v, %v; want the change left as it was", got, err)
	}
	if out := log.String(); !strings.Contains(out, "injected claim failure") {
		t.Errorf("log = %q, want the failed claim", out)
	}
	f.eng.uninstallMu.Lock()
	kept := false
	for _, s := range f.eng.uninstallSkipped {
		kept = kept || s == id
	}
	f.eng.uninstallMu.Unlock()
	f.eng.timerMu.Lock()
	armed := f.eng.uninstallTimer != nil
	f.eng.timerMu.Unlock()
	if !kept || !armed {
		t.Fatalf("after a failed claim: id kept %v, poll armed %v; want both", kept, armed)
	}
	f.recoverExec("DROP TRIGGER fail_claim")
	f.clock.Advance(uninstallPoll)
	f.eng.recoveryPass.Wait()
	got, err := f.eng.Change(ctx, id)
	if err != nil || got.State != core.StateInterrupted || got.Owner != "" {
		t.Errorf("a poll after the failed claim: %+v, %v; want interrupted and unowned without a restart", got, err)
	}
}

func TestAbortUninstallAudited(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if err := BeginUninstall(ctx, f.db, false); err != nil {
		t.Fatal(err)
	}
	// The second abort finds no barrier, succeeds and is audited too.
	for range 2 {
		if err := AbortUninstall(ctx, f.db, "local:root@h1"); err != nil {
			t.Fatalf("AbortUninstall = %v", err)
		}
	}
	if n := f.uninstalling(); n != 0 {
		t.Errorf("barrier rows after the abort = %d, want 0", n)
	}
	if err := f.eng.AbortUninstall(ctx); err != nil {
		t.Fatal(err)
	}
	entries, err := f.journal.Entries(ctx, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	var got []core.JournalEntry
	for _, e := range entries {
		if e.At.IsZero() {
			t.Errorf("entry %+v has no time", e)
		}
		e.Seq, e.At = 0, time.Time{}
		got = append(got, e)
	}
	entry := func(actor string) core.JournalEntry {
		return core.JournalEntry{Actor: actor, Action: "core.uninstall", Version: 1, Phase: "uninstall-abort",
			Input: json.RawMessage(`{}`), Result: "ok"}
	}
	want := []core.JournalEntry{entry("local:root@h1"), entry("local:root@h1"), entry(ActorRecovery)}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("journal\n got %+v\nwant %+v", got, want)
	}

	// The row and the entry share one transaction: an entry that cannot be
	// written keeps the barrier.
	if err := BeginUninstall(ctx, f.db, false); err != nil {
		t.Fatal(err)
	}
	f.recoverExec(`CREATE TRIGGER fail_journal BEFORE INSERT ON core_journal
		BEGIN SELECT RAISE(ABORT, 'injected journal failure'); END`)
	if err := AbortUninstall(ctx, f.db, "local:root@h1"); err == nil || !strings.Contains(err.Error(), "injected journal failure") {
		t.Fatalf("AbortUninstall with a failing journal = %v", err)
	}
	if n := f.uninstalling(); n != 1 {
		t.Errorf("barrier rows after a failed abort = %d, want 1", n)
	}
}
