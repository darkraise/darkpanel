package lifecycle

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

func TestUndoUndoesTheLastChangesNewestFirst(t *testing.T) {
	u := newUfx(t)
	ctx := context.Background()
	first := u.change(ufxOp{ID: "a", Value: "1"})
	second := u.change(ufxOp{ID: "a", Value: "2"}, ufxOp{ID: "b", Value: "x"})

	_, err := u.eng.Undo(ctx, bob, first.ID, nil)
	wantRefusal(t, err, errcode.UndoConflict, "lock ufx was taken by a later change")

	v, err := u.eng.Undo(ctx, bob, second.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if v.State != core.StateUndoing || v.Owner != core.OwnerUndo {
		t.Fatalf("undo answered %s owned by %q, want undoing owned by undo", v.State, v.Owner)
	}
	if v = u.settled(second.ID); v.State != core.StateReverted || v.Owner != "" {
		t.Fatalf("after the undo: %s owned by %q, want reverted and unowned", v.State, v.Owner)
	}
	for seq := 1; seq <= 2; seq++ {
		if s := u.journalEffect(second.ID, seq).State; s != core.EffectUndone {
			t.Errorf("effect %d is %s, want undone", seq, s)
		}
	}
	if got, ok := u.store.get("a"); !ok || got != "1" {
		t.Errorf("object a = %q (present %v), want 1", got, ok)
	}
	if _, ok := u.store.get("b"); ok {
		t.Error("object b is still there")
	}

	if _, err := u.eng.Undo(ctx, bob, first.ID, nil); err != nil {
		t.Fatalf("undo of the first change once the second was undone: %v", err)
	}
	if v = u.settled(first.ID); v.State != core.StateReverted {
		t.Fatalf("first change = %s, want reverted", v.State)
	}
	if _, ok := u.store.get("a"); ok {
		t.Error("object a is still there")
	}
	if n := u.count("SELECT count(*) FROM core_locks"); n != 0 {
		t.Errorf("locks = %d, want none", n)
	}
	if n := u.count("SELECT coalesce(max(epoch), 0) FROM core_lock_epochs WHERE key = ?", ufxKey); n != 0 {
		t.Errorf("epoch of %s = %d, want 0 once both changes gave theirs back", ufxKey, n)
	}
	want := []string{"apply/ok", "apply/ok", "undo/ok", "undo/ok", "undo/ok", "undo/ok"}
	if got := u.phases(); !reflect.DeepEqual(got, want) {
		t.Errorf("journal = %v, want %v", got, want)
	}

	v, err = u.eng.Undo(ctx, bob, second.ID, nil)
	if err != nil || v.State != core.StateReverted {
		t.Fatalf("a repeated undo = %s, %v; want the reverted change", v.State, err)
	}
	if n := len(u.phases()); n != len(want) {
		t.Errorf("a repeated undo journaled %d entries", n-len(want))
	}
}

func TestUndoStopsAtAConflict(t *testing.T) {
	u := newUfx(t)
	c := u.change(ufxOp{ID: "a", Value: "1"}, ufxOp{ID: "b", Value: "1"})
	u.store.set("b", "changed")
	if _, err := u.eng.Undo(context.Background(), bob, c.ID, nil); err != nil {
		t.Fatal(err)
	}
	v := u.settled(c.ID)
	if v.State != core.StateNeedsRepair || v.Error == nil || v.Error.Code != string(errcode.NeedsRepair) {
		t.Fatalf("change = %s with error %+v, want needs_repair with core.needs_repair", v.State, v.Error)
	}
	if !strings.Contains(v.Error.Message, "effect 2") {
		t.Errorf("message %q does not name effect 2", v.Error.Message)
	}
	b := u.journalEffect(c.ID, 2)
	if b.State != core.EffectUnresolved || b.Reason != core.ReasonConflict || b.Current == nil ||
		!strings.Contains(string(b.Current.State), "changed") {
		t.Errorf("effect 2 = %s (%s), current %v; want unresolved conflict observing the changed value", b.State, b.Reason, b.Current)
	}
	if s := u.journalEffect(c.ID, 1).State; s != core.EffectMade {
		t.Errorf("effect 1 = %s, want made: an earlier effect is never undone past a conflict", s)
	}
	if got, _ := u.store.get("a"); got != "1" {
		t.Errorf("object a = %q, want 1", got)
	}
	if n := u.count("SELECT count(*) FROM core_locks WHERE owner = ?", c.ID); n != 1 {
		t.Errorf("locks held by the change = %d, want 1: needs_repair keeps them", n)
	}
	if got := u.phases(); got[len(got)-1] != "undo/error/core.needs_repair" {
		t.Errorf("journal = %v, want it to end with undo/error/core.needs_repair", got)
	}
}

func TestUndoStopsAtAnAcceptedEffect(t *testing.T) {
	u := newUfx(t)
	c := u.change(ufxOp{ID: "a", Value: "1"}, ufxOp{ID: "b", Value: "1"}, ufxOp{ID: "d", Value: "1"})
	u.sqlExec(`UPDATE core_effects SET state = ?, resolution = ?, current_json = post_json
		WHERE change_id = ? AND seq = 2`, string(core.EffectAccepted), core.ResolutionAccept, c.ID)
	if _, err := u.eng.Undo(context.Background(), bob, c.ID, nil); err != nil {
		t.Fatal(err)
	}
	if v := u.settled(c.ID); v.State != core.StateKept {
		t.Fatalf("change = %s, want kept", v.State)
	}
	want := map[int]core.EffectState{1: core.EffectMade, 2: core.EffectAccepted, 3: core.EffectUndone}
	for seq, s := range want {
		if got := u.journalEffect(c.ID, seq).State; got != s {
			t.Errorf("effect %d = %s, want %s", seq, got, s)
		}
	}
	if _, ok := u.store.get("d"); ok {
		t.Error("object d, the newest effect, was not undone")
	}
	if got, _ := u.store.get("a"); got != "1" {
		t.Errorf("object a = %q, want 1: effects before the accepted one stay", got)
	}
	if n := u.count("SELECT count(*) FROM core_locks"); n != 0 {
		t.Errorf("locks = %d, want none: kept releases them", n)
	}
	if n := u.count("SELECT coalesce(max(epoch), 0) FROM core_lock_epochs WHERE key = ?", ufxKey); n != 1 {
		t.Errorf("epoch = %d, want 1: a kept change gives nothing back", n)
	}
}

func TestUndoInverseOutcomes(t *testing.T) {
	cases := []struct {
		name   string
		before string // the object's value before the change; "" when absent
		op     ufxOp
		mode   string
		state  core.EffectState
		reason string
		change string
		value  string // the object's value after the undo; "" when absent
	}{
		{"a local inverse", "1", ufxOp{ID: "a", Value: "2"}, "", core.EffectUndone, "", core.StateReverted, "1"},
		{"an inverse that recreates a deleted object", "1", ufxOp{ID: "a"}, "", core.EffectUndone, "", core.StateReverted, "1"},
		{"the undo of a creation", "", ufxOp{ID: "a", Value: "2"}, "", core.EffectUndone, "", core.StateReverted, ""},
		{"a definite refusal", "1", ufxOp{ID: "a", Value: "2"}, "definite", core.EffectMade, "", core.StateNeedsRepair, "2"},
		{"a local inverse failing before its write", "1", ufxOp{ID: "a", Value: "2"}, "fail", core.EffectMade, "", core.StateNeedsRepair, "2"},
		{"a local inverse failing after its write", "1", ufxOp{ID: "a", Value: "2"}, "fail-after-write",
			core.EffectUnresolved, core.ReasonAmbiguous, core.StateNeedsRepair, "1"},
		{"an inverse that changed nothing", "1", ufxOp{ID: "a", Value: "2"}, "noop",
			core.EffectUnresolved, core.ReasonConflict, core.StateNeedsRepair, "2"},
		{"a remote inverse failing", "1", ufxOp{ID: "a", Value: "2", InverseRemote: true}, "fail",
			core.EffectUnresolved, core.ReasonUncertain, core.StateNeedsRepair, "2"},
		{"a remote inverse refused definitely", "1", ufxOp{ID: "a", Value: "2", InverseRemote: true}, "definite",
			core.EffectMade, "", core.StateNeedsRepair, "2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := newUfx(t)
			if tc.before != "" {
				u.store.set("a", tc.before)
			}
			c := u.change(tc.op)
			u.store.inverse["a"] = tc.mode
			u.clock.Advance(time.Minute)
			if _, err := u.eng.Undo(context.Background(), bob, c.ID, nil); err != nil {
				t.Fatal(err)
			}
			v := u.settled(c.ID)
			eff := u.journalEffect(c.ID, 1)
			if eff.State != tc.state || eff.Reason != tc.reason || v.State != tc.change {
				t.Fatalf("effect %s (%q), change %s; want effect %s (%q), change %s",
					eff.State, eff.Reason, v.State, tc.state, tc.reason, tc.change)
			}
			got, ok := u.store.get("a")
			if got != tc.value || ok != (tc.value != "") {
				t.Errorf("object a = %q (present %v), want %q", got, ok, tc.value)
			}
			if tc.op.Value == "" && eff.UndoResource != "ufx.item:a" {
				t.Errorf("UndoResource = %q, want the recreated object ufx.item:a", eff.UndoResource)
			}
			if tc.op.InverseRemote && tc.state == core.EffectUnresolved &&
				!eff.CommitBy.Equal(u.clock.Now().Add(2*time.Minute)) {
				t.Errorf("CommitBy = %v, want the inverse's start plus CommitWait", eff.CommitBy)
			}
			wantLocks := 0
			if tc.change == core.StateNeedsRepair {
				wantLocks = 1
				if v.Error == nil || v.Error.Code != string(errcode.NeedsRepair) {
					t.Errorf("error = %+v, want core.needs_repair", v.Error)
				}
			}
			if n := u.count("SELECT count(*) FROM core_locks WHERE owner = ?", c.ID); n != wantLocks {
				t.Errorf("locks held = %d, want %d", n, wantLocks)
			}
		})
	}
}

func TestUndoResumesAnInterruptedInverse(t *testing.T) {
	cases := []struct {
		name          string
		inverseRemote bool
		found         string // the object's value at the restart; "" leaves it as the change made it
		state         core.EffectState
		reason        string
		change        string
		value         string
	}{
		{"the inverse had finished", false, "1", core.EffectUndone, "", core.StateReverted, "1"},
		{"the inverse had not run", false, "", core.EffectUndone, "", core.StateReverted, "1"},
		{"the object changed since", false, "9", core.EffectUnresolved, core.ReasonAmbiguous, core.StateNeedsRepair, "9"},
		{"a remote inverse", true, "1", core.EffectUnresolved, core.ReasonUncertain, core.StateNeedsRepair, "1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := newUfx(t)
			u.store.set("a", "1")
			c := u.change(ufxOp{ID: "a", Value: "2", InverseRemote: tc.inverseRemote})
			// The process died while the inverse ran: the effect is undoing
			// and the change kept its locks.
			u.sqlExec(`UPDATE core_effects SET state = ?, undo_to_json = '{"state":{"value":"1"}}'
				WHERE change_id = ? AND seq = 1`, string(core.EffectUndoing), c.ID)
			u.setNeedsRepair(c.ID, ufxKey)
			if tc.found != "" {
				u.store.set("a", tc.found)
			}
			u.restart()
			u.recoverAll()
			if _, err := u.eng.Undo(context.Background(), bob, c.ID, nil); err != nil {
				t.Fatal(err)
			}
			v := u.settled(c.ID)
			eff := u.journalEffect(c.ID, 1)
			if eff.State != tc.state || eff.Reason != tc.reason || v.State != tc.change {
				t.Fatalf("effect %s (%q), change %s; want effect %s (%q), change %s",
					eff.State, eff.Reason, v.State, tc.state, tc.reason, tc.change)
			}
			if got, _ := u.store.get("a"); got != tc.value {
				t.Errorf("object a = %q, want %q", got, tc.value)
			}
		})
	}
}

func TestUndoRefusals(t *testing.T) {
	cases := []struct {
		name  string
		setup func(u *ufxFixture, c core.ChangeView)
		code  errcode.Code
		part  string
	}{
		{"an unresolved effect", func(u *ufxFixture, c core.ChangeView) {
			u.setNeedsRepair(c.ID, ufxKey)
			u.setEffectState(c.ID, 1, core.EffectUnresolved, core.ReasonUncertain)
		}, errcode.NeedsRepair, "resolve its effects first"},
		{"a recovery handler that has not finished", func(u *ufxFixture, c core.ChangeView) {
			u.setNeedsRepair(c.ID, ufxKey)
			u.sqlExec("UPDATE core_changes SET recovery_retry = 1 WHERE id = ?", c.ID)
		}, errcode.NeedsRepair, "repair it: its recovery handler has not finished"},
		{"an owned change", func(u *ufxFixture, c core.ChangeView) {
			u.sqlExec("UPDATE core_changes SET recovery_owner = ? WHERE id = ?", core.OwnerResolve, c.ID)
		}, errcode.ChangeBusy, "owned by resolve"},
		{"a step of a composite", func(u *ufxFixture, c core.ChangeView) {
			u.sqlExec("UPDATE core_changes SET parent_change_id = 'COMPOSITE1' WHERE id = ?", c.ID)
		}, errcode.ChangeBusy, "COMPOSITE1"},
		{"a repair change", func(u *ufxFixture, c core.ChangeView) {
			u.sqlExec(`UPDATE core_plans SET repairs = 'TARGET1'
				WHERE id = (SELECT plan_id FROM core_changes WHERE id = ?)`, c.ID)
		}, errcode.ChangeBusy, "TARGET1"},
		{"an abandoned change", func(u *ufxFixture, c core.ChangeView) {
			u.sqlExec("UPDATE core_changes SET state = ? WHERE id = ?", core.StateAbandoned, c.ID)
		}, errcode.ChangeNotPending, "abandoned"},
		{"a lock another owner holds", func(u *ufxFixture, _ core.ChangeView) {
			u.sqlExec("INSERT INTO core_locks (key, owner) VALUES (?, 'someone')", ufxKey)
		}, errcode.LockHeld, "someone"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := newUfx(t)
			c := u.change(ufxOp{ID: "a", Value: "1"})
			tc.setup(u, c)
			before := u.state(c.ID)
			_, err := u.eng.Undo(context.Background(), bob, c.ID, nil)
			wantRefusal(t, err, tc.code, tc.part)
			if s := u.state(c.ID); s != before {
				t.Errorf("state = %s after the refusal, want %s", s, before)
			}
			if got, _ := u.store.get("a"); got != "1" {
				t.Errorf("object a = %q, want 1", got)
			}
		})
	}
}

func TestUndoRefusesWhatItCannotUndo(t *testing.T) {
	u := newUfx(t)
	ctx := context.Background()
	reset := u.apply(alice, u.plan(alice, refUfxReset, `{"id":"a"}`).PlanID, core.ApplyOptions{})
	_, err := u.eng.Undo(ctx, bob, reset.ID, nil)
	wantRefusal(t, err, errcode.ChangeNotPending, "no undo handler")
	_, err = u.eng.Undo(ctx, bob, "NOPE", nil)
	wantCode(t, err, errcode.ChangeNotFound)
}

func TestUndoOfAStep(t *testing.T) {
	ctx := context.Background()
	t.Run("by its composite", func(t *testing.T) {
		u := newUfx(t)
		c := u.change(ufxOp{ID: "a", Value: "1"})
		u.sqlExec("UPDATE core_changes SET parent_change_id = 'COMPOSITE1' WHERE id = ?", c.ID)
		call := &core.CallContext{ParentChangeID: "COMPOSITE1", ParentAction: "cfx.deploy.add", StepID: "one"}
		if _, err := u.eng.Undo(ctx, recoveryPrincipal, c.ID, call); err != nil {
			t.Fatal(err)
		}
		if v := u.settled(c.ID); v.State != core.StateReverted {
			t.Fatalf("step = %s, want reverted", v.State)
		}
	})
	t.Run("as its repair", func(t *testing.T) {
		u := newUfx(t)
		c := u.change(ufxOp{ID: "a", Value: "1"})
		u.sqlExec("UPDATE core_changes SET parent_change_id = 'COMPOSITE1' WHERE id = ?", c.ID)
		u.setNeedsRepair(c.ID, ufxKey)
		if _, err := u.eng.Undo(ctx, bob, c.ID, nil); err != nil {
			t.Fatal(err)
		}
		if v := u.settled(c.ID); v.State != core.StateReverted {
			t.Fatalf("step = %s, want reverted", v.State)
		}
	})
}
