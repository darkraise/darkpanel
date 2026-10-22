package lifecycle

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// passCutOff rewrites change id as a process that died during its apply
// leaves it: applying and owned by engine, with no error recorded and every
// unresolved effect intended again, its calls Local as given.
func (f *fixture) passCutOff(id string, local bool) {
	f.t.Helper()
	f.recoverExec(`UPDATE core_changes SET state = ?, recovery_owner = ?, recovery_retry = 0,
		error_code = NULL, error_message = NULL, error_cause = NULL WHERE id = ?`,
		core.StateApplying, core.OwnerEngine, id)
	l := 0
	if local {
		l = 1
	}
	f.recoverExec(`UPDATE core_effects SET state = ?, reason = '', local = ?, inverse_local = ?
		WHERE change_id = ? AND state = ?`,
		string(core.EffectIntended), l, l, id, string(core.EffectUnresolved))
}

func TestRecoverPassSettlesCutOffApplies(t *testing.T) {
	cases := []struct {
		name    string
		local   bool
		item    string // item a when the agent restarts
		state   string
		code    string
		effect  core.EffectState
		locks   int
		epoch   int
		message string
	}{
		{"a remote call", false, "1", core.StateNeedsRepair, string(errcode.NeedsRepair),
			core.EffectUnresolved, 1, 1, "rcv.item:a"},
		{"a local call that took effect", true, "1", core.StateInterrupted, string(errcode.Internal),
			core.EffectMade, 0, 1, "interrupted by a restart during apply; verify passed"},
		{"a local call that never ran", true, "", core.StateInterrupted, string(errcode.Internal),
			core.EffectSkipped, 0, 0, "interrupted by a restart during apply; verify failed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, h := newRcvFixture(t)
			v := f.rcvApply(refRcvPut, `{"name":"a","value":"1","remote":true,"failAfter":true}`)
			if v.State != core.StateNeedsRepair {
				t.Fatalf("seed change = %s, want needs_repair", v.State)
			}
			f.passCutOff(v.ID, c.local)
			h.set("a", c.item)
			f.restart()
			f.recoverAll()
			got, err := f.eng.Change(context.Background(), v.ID)
			if err != nil || got.State != c.state || got.Owner != "" || got.Error == nil || got.Error.Code != c.code ||
				!strings.Contains(got.Error.Message, c.message) {
				t.Fatalf("after recovery: %+v (error %+v), %v; want %s, unowned, code %s naming %q",
					got, got.Error, err, c.state, c.code, c.message)
			}
			if c.state == core.StateNeedsRepair && got.Error.Cause != string(errcode.Internal) {
				t.Errorf("error cause = %q, want internal", got.Error.Cause)
			}
			if n := f.count("SELECT count(*) FROM core_effects WHERE change_id = ? AND state = ?", v.ID, string(c.effect)); n != 1 {
				t.Errorf("effects in state %s = %d, want 1", c.effect, n)
			}
			if retry, locks, epoch := f.rcvFlags(v.ID); retry != 0 || locks != c.locks || epoch != c.epoch {
				t.Errorf("recovery_retry %d, locks %d, epoch %d; want 0, %d, %d", retry, locks, epoch, c.locks, c.epoch)
			}
		})
	}
}

func TestRecoverPassRunsTheRecoveryHandler(t *testing.T) {
	locked := errcode.New(errcode.Unavailable, "the package database is locked")
	cases := []struct {
		name  string
		seed  func(f *fixture, h *rcvHost) string
		state string
		calls int // Recover calls after the restart
		retry int
	}{
		{"an apply cut off", func(f *fixture, _ *rcvHost) string {
			v := f.rcvApply(refRcvFix, `{"name":"a","value":"1","remote":true,"failAfter":true}`)
			f.passCutOff(v.ID, true)
			return v.ID
		}, core.StateCompleted, 1, 0},
		{"an apply cut off over a remote call", func(f *fixture, _ *rcvHost) string {
			v := f.rcvApply(refRcvFix, `{"name":"a","value":"1","remote":true,"failAfter":true}`)
			f.passCutOff(v.ID, false)
			return v.ID
		}, core.StateNeedsRepair, 0, 1},
		{"a recovery handler cut off", func(f *fixture, h *rcvHost) string {
			h.script(core.RecoveryOutcome{}, locked)
			v := f.rcvApply(refRcvFix, `{"name":"a","value":"1","failBefore":true}`)
			f.recoverExec("UPDATE core_changes SET state = ?, recovery_owner = ?, recovery_retry = 0 WHERE id = ?",
				core.StateRecovering, core.OwnerModule, v.ID)
			return v.ID
		}, core.StateCompleted, 1, 0},
		{"a recovery handler that has not finished", func(f *fixture, h *rcvHost) string {
			h.script(core.RecoveryOutcome{}, locked)
			return f.rcvApply(refRcvFix, `{"name":"a","value":"1","failBefore":true}`).ID
		}, core.StateCompleted, 1, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, h := newRcvFixture(t)
			id := c.seed(f, h)
			before, _, _ := h.recovered()
			h.script(core.RecoveryOutcome{State: core.RecoveryCompleted, Output: json.RawMessage(`{"value":"1"}`)}, nil)
			f.restart()
			f.recoverAll()
			v, err := f.eng.Change(context.Background(), id)
			if err != nil || v.State != c.state || v.Owner != "" {
				t.Fatalf("after recovery: %+v, %v; want %s and unowned", v, err, c.state)
			}
			calls, during, _ := h.recovered()
			if calls-before != c.calls || (c.calls > 0 && during[len(during)-1] != "recovering/module") {
				t.Errorf("Recover ran %d times after the restart (%v), want %d, on a change recovering by module",
					calls-before, during, c.calls)
			}
			if retry, _, _ := f.rcvFlags(id); retry != c.retry {
				t.Errorf("recovery_retry = %d, want %d", retry, c.retry)
			}
		})
	}
}

func TestRecoverPassResumesAnUndo(t *testing.T) {
	for _, c := range []struct {
		name string
		item string // item a when the agent restarts
	}{
		{"the inverse finished", ""},
		{"the inverse never ran", "1"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f, h := newRcvFixture(t)
			ctx := context.Background()
			v := f.rcvApply(refRcvPut, `{"name":"a","value":"1"}`)
			if v.State != core.StateCompleted {
				t.Fatalf("seed change = %s, want completed", v.State)
			}
			effs, err := loadEffects(ctx, f.db.Read(), v.ID)
			if err != nil || len(effs) != 1 {
				t.Fatalf("effects = %v, %v; want one", effs, err)
			}
			to := undoTo(effs[0])
			if err := f.db.Write(ctx, func(tx *sql.Tx) error {
				return setEffect(ctx, tx, f.clock.Now(), v.ID, effs[0].Seq, []core.EffectState{core.EffectMade},
					effectUpdate{State: core.EffectUndoing, UndoTo: &to})
			}); err != nil {
				t.Fatal(err)
			}
			f.recoverExec("UPDATE core_changes SET state = ?, recovery_owner = ? WHERE id = ?",
				core.StateUndoing, core.OwnerUndo, v.ID)
			f.recoverExec("INSERT INTO core_locks (key, owner) VALUES ('rcv', ?)", v.ID)
			h.set("a", c.item)
			f.restart()
			f.recoverAll()
			got, err := f.eng.Change(ctx, v.ID)
			if err != nil || got.State != core.StateReverted || got.Owner != "" || h.get("a") != "" {
				t.Errorf("after recovery: %+v, %v, item a %q; want reverted, unowned, item a empty", got, err, h.get("a"))
			}
			if n := f.count("SELECT count(*) FROM core_effects WHERE change_id = ? AND state = ?",
				v.ID, string(core.EffectUndone)); n != 1 {
				t.Errorf("undone effects = %d, want 1", n)
			}
			if _, locks, epoch := f.rcvFlags(v.ID); locks != 0 || epoch != 0 {
				t.Errorf("locks %d, epoch %d; want 0, 0", locks, epoch)
			}
		})
	}
}

func TestRecoverPassRecordsACutOffRepairChange(t *testing.T) {
	f, _ := newRcvFixture(t)
	ctx := context.Background()
	r := f.pending(`{"name":"a","value":"1","failRevert":true}`)
	if _, err := f.eng.Revert(ctx, alice, r.ID); err != nil {
		t.Fatal(err)
	}
	if s := f.state(r.ID); s != core.StateNeedsRepair {
		t.Fatalf("target = %s, want needs_repair", s)
	}
	res, err := f.eng.Repair(ctx, alice, r.ID, core.RepairRequest{})
	if err != nil || res.Plan == nil {
		t.Fatalf("Repair = %+v, %v; want a repair plan", res, err)
	}
	def, ok := f.reg.Action(core.ActionRef{Name: res.Plan.Action, Version: res.Plan.Version})
	if !ok {
		t.Fatalf("repair action %s/v%d is not registered", res.Plan.Action, res.Plan.Version)
	}
	// What the repair change's intent transaction leaves (1.3, repair
	// change P of R), cut off before the change recorded anything else.
	pID := f.eng.newID(f.clock.Now())
	ts := formatTime(f.clock.Now())
	f.recoverExec("UPDATE core_lock_epochs SET epoch = epoch + 1 WHERE key = 'fix'")
	f.recoverExec(`INSERT INTO core_changes (id, plan_id, host_id, action, version, class, compensated_by, actor,
		state, input_json, recovery_owner, lock_epochs, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, '{}', ?, (SELECT json_object('fix', epoch) FROM core_lock_epochs WHERE key = 'fix'), ?, ?)`,
		pID, res.Plan.PlanID, testHost, def.Spec.Name, def.Spec.Version, string(def.Spec.Class), def.Spec.CompensatedBy,
		alice.Actor, core.StateApplying, core.OwnerEngine, ts, ts)
	f.recoverExec("UPDATE core_locks SET owner = ? WHERE owner = ?", pID, r.ID)
	f.recoverExec("UPDATE core_changes SET recovery_owner = ?, repaired_by = ? WHERE id = ?", core.OwnerRepair, pID, r.ID)
	f.restart()
	f.recoverAll()
	p, err := f.eng.Change(ctx, pID)
	if err != nil || p.State != core.StateFailed || p.Owner != "" {
		t.Errorf("repair change after recovery = %+v, %v; want failed and unowned", p, err)
	}
	target, err := f.eng.Change(ctx, r.ID)
	if err != nil || target.State != core.StateNeedsRepair || target.Owner != "" {
		t.Errorf("target after recovery = %+v, %v; want needs_repair and unowned", target, err)
	}
	if mine, theirs := f.count("SELECT count(*) FROM core_locks WHERE owner = ?", r.ID),
		f.count("SELECT count(*) FROM core_locks WHERE owner = ?", pID); mine == 0 || theirs != 0 {
		t.Errorf("locks: target %d, repair change %d; want them all moved back to the target", mine, theirs)
	}
}

func TestRecoverPassKeepsAHangingHandlerOwned(t *testing.T) {
	f, h := newRcvFixture(t)
	ctx := context.Background()
	h.script(core.RecoveryOutcome{}, errcode.New(errcode.Unavailable, "the package database is locked"))
	v := f.rcvApply(refRcvFix, `{"name":"a","value":"1","failBefore":true}`)
	if v.State != core.StateNeedsRepair {
		t.Fatalf("seed change = %s, want needs_repair", v.State)
	}
	hold, ended := make(chan struct{}), make(chan struct{})
	h.script(core.RecoveryOutcome{State: core.RecoveryCompleted}, nil)
	h.mu.Lock()
	h.hold, h.ctxEnded = hold, ended
	h.mu.Unlock()
	f.restart()
	released := false
	release := func() {
		if !released {
			released = true
			close(hold)
		}
	}
	t.Cleanup(release)
	if err := f.eng.Recover(ctx); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	select {
	case <-ended:
	case <-time.After(10 * time.Second):
		t.Fatal("the recovery handler did not start, or its context did not end")
	}
	got, err := f.eng.Change(ctx, v.ID)
	if err != nil || got.State != core.StateRecovering || got.Owner != core.OwnerModule {
		t.Errorf("while the handler hangs: %+v, %v; want recovering, owned by module", got, err)
	}
	if other := f.apply(alice, f.plan(alice, refAdd, `{"name":"b","value":"1"}`).PlanID, core.ApplyOptions{}); other.State != core.StateCompleted {
		t.Errorf("another change while the handler hangs = %s, want completed", other.State)
	}
	release()
	f.eng.recoveryPass.Wait()
	if s := f.state(v.ID); s != core.StateCompleted {
		t.Errorf("after the handler returned: %s, want completed", s)
	}
}
