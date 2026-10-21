package lifecycle

import (
	"context"
	"reflect"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// pendingJSet applies jfx.item.set of item a from "0" to "1" locally and
// returns the pending change.
func (f *jfixture) pendingJSet() core.ChangeView {
	f.t.Helper()
	f.store.put("a", "0")
	v := f.apply(alice, f.plan(alice, refJSet, `{"name":"a","value":"1","local":true}`).PlanID, core.ApplyOptions{})
	if v.State != core.StatePending {
		f.t.Fatalf("state = %s, want pending", v.State)
	}
	return v
}

// markJUnresolved sets effect 1 of changeID unresolved, as a failed
// resolution or an earlier settle leaves it.
func (f *jfixture) markJUnresolved(changeID string) {
	f.t.Helper()
	if _, err := f.db.Writer().Exec(
		"UPDATE core_effects SET state = 'unresolved', reason = 'uncertain' WHERE change_id = ? AND seq = 1", changeID); err != nil {
		f.t.Fatal(err)
	}
}

func TestRevertOutcomes(t *testing.T) {
	operator := func(f *jfixture, id string) {
		if _, err := f.eng.Revert(context.Background(), alice, id); err != nil {
			f.t.Fatal(err)
		}
	}
	timer := func(f *jfixture, _ string) { f.clock.Advance(DefaultConfirmTimeout) }
	restart := func(f *jfixture, _ string) {
		f.restart()
		f.recoverAll()
	}
	conflict := func(f *jfixture, _ string) { f.store.put("a", "other") }
	unresolved := func(f *jfixture, id string) { f.markJUnresolved(id) }
	cases := []struct {
		name       string
		guardFails bool
		prepare    func(f *jfixture, id string)
		trigger    func(f *jfixture, id string)
		state      string
		cause      string
		journal    string // the revert's entry
		effects    []string
		value      string // item a afterwards
		override   string
	}{
		{"every effect restored", false, nil, operator,
			core.StateReverted, "", "revert/ok", []string{"1:undone"}, "0", ""},
		{"an unresolved effect blocks the revert", false, unresolved, operator,
			core.StateNeedsRepair, "change.revert_failed", "revert/error/core.needs_repair", []string{"1:unresolved/uncertain"}, "1", ""},
		{"an unresolved effect and a failing guard", true, unresolved, operator,
			core.StateNeedsRepair, "", "revert/override/core.needs_repair", []string{"1:unresolved/uncertain"}, "0", `{"overwritten":"a"}`},
		{"a conflict is left in place", false, conflict, operator,
			core.StateNeedsRepair, "change.revert_failed", "revert/error/core.needs_repair", []string{"1:unresolved/conflict"}, "other", ""},
		{"a conflict and a failing guard", true, conflict, operator,
			core.StateNeedsRepair, "", "revert/override/core.needs_repair", []string{"1:unresolved/conflict"}, "0", `{"overwritten":"a"}`},
		{"a conflict found by the timer", false, conflict, timer,
			core.StateNeedsRepair, "change.revert_failed", "revert/error/core.needs_repair", []string{"1:unresolved/conflict"}, "other", ""},
		{"a conflict found at the next start", false, conflict, restart,
			core.StateNeedsRepair, "change.revert_failed", "recover/error/core.needs_repair", []string{"1:unresolved/conflict"}, "other", ""},
		{"restored at the next start", false, nil, restart,
			core.StateReverted, "", "recover/ok", []string{"1:undone"}, "0", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newJFixture(t, jfxOptions{})
			v := f.pendingJSet()
			f.store.guardFails = c.guardFails
			if c.prepare != nil {
				c.prepare(f, v.ID)
			}
			c.trigger(f, v.ID)
			got, err := f.eng.Change(context.Background(), v.ID)
			if err != nil {
				t.Fatal(err)
			}
			cause := ""
			if got.Error != nil {
				cause = got.Error.Cause
			}
			if got.State != c.state || got.Owner != "" || cause != c.cause || string(got.Override) != c.override {
				t.Errorf("change = %s owned by %q, error %+v, override %s; want %s caused by %q with override %q",
					got.State, got.Owner, got.Error, got.Override, c.state, c.cause, c.override)
			}
			if phases := f.phases(); phases[len(phases)-1] != c.journal {
				t.Errorf("journal = %v, want it to end with %s", phases, c.journal)
			}
			if effs := f.effectStates(v.ID); !reflect.DeepEqual(effs, c.effects) {
				t.Errorf("effects = %v, want %v", effs, c.effects)
			}
			if a := jValue(f.store.get("a")); a != c.value {
				t.Errorf("item a = %q, want %q", a, c.value)
			}
			locks := f.count("SELECT count(*) FROM core_locks WHERE owner = ?", v.ID)
			if (locks == 1) != (c.state == core.StateNeedsRepair) {
				t.Errorf("locks held = %d with the change %s", locks, c.state)
			}
			if wantEpoch := map[bool]int64{true: 0, false: 1}[c.state == core.StateReverted]; f.lockEpoch("jfx") != wantEpoch {
				t.Errorf("jfx epoch = %d, want %d", f.lockEpoch("jfx"), wantEpoch)
			}
		})
	}
}

func TestARestoredEffectRecordsWhatItWasRestoredTo(t *testing.T) {
	f := newJFixture(t, jfxOptions{})
	v := f.pendingJSet()
	if _, err := f.eng.Revert(context.Background(), alice, v.ID); err != nil {
		t.Fatal(err)
	}
	eff := f.effects(v.ID)[0]
	if eff.State != core.EffectUndone || eff.UndoTo == nil || jValue(*eff.UndoTo) != "0" {
		t.Errorf("effect = %+v, want undone to the value before", eff)
	}
}

func TestAnOverrideRecordsWhatItOverwrote(t *testing.T) {
	f := newJFixture(t, jfxOptions{})
	v := f.pendingJSet()
	f.store.guardFails = true
	f.store.put("a", "other")
	if _, err := f.eng.Revert(context.Background(), alice, v.ID); err != nil {
		t.Fatal(err)
	}
	eff := f.effects(v.ID)[0]
	if eff.Current == nil || jValue(*eff.Current) != "other" {
		t.Errorf("effect Current = %+v, want the overwritten value", eff.Current)
	}
}

func TestAFailedApplySettlesThenReverts(t *testing.T) {
	f := newJFixture(t, jfxOptions{})
	f.store.put("a", "0")
	v := f.apply(alice, f.plan(alice, refJSet, `{"name":"a","value":"1","local":true,"failAfterCall":true}`).PlanID, core.ApplyOptions{})
	if v.State != core.StateReverted || v.Error == nil || v.Error.Code != string(errcode.Internal) {
		t.Errorf("change = %s, error %+v; want reverted with the apply's error", v.State, v.Error)
	}
	if got := f.effectStates(v.ID); !reflect.DeepEqual(got, []string{"1:undone"}) {
		t.Errorf("effects = %v, want the settled effect undone", got)
	}
	if a := jValue(f.store.get("a")); a != "0" || f.clock.Pending() != 0 {
		t.Errorf("item a = %q, armed timers = %d; want 0 and none", a, f.clock.Pending())
	}
}
