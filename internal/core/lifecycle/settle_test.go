package lifecycle

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

func TestAFailedApplySettlesItsEffects(t *testing.T) {
	cases := []struct {
		name    string
		ref     core.ActionRef
		input   string
		present bool     // item a exists before the apply
		effects []string // after settle
		state   string
		code    errcode.Code
		cause   string
		locks   int
		epoch   int64 // of jfx after the change
	}{
		{"remote call failed", refJModify, `{"name":"a","value":"1","failBeforeCall":true}`, true,
			[]string{"1:unresolved/uncertain"}, core.StateNeedsRepair, errcode.NeedsRepair, "internal", 1, 1},
		{"local modify failed before the call", refJModify, `{"name":"a","value":"1","local":true,"failBeforeCall":true}`, true,
			[]string{"1:skipped"}, core.StateFailed, errcode.Internal, "", 0, 0},
		{"local modify failed after the call", refJModify, `{"name":"a","value":"1","local":true,"failAfterCall":true}`, true,
			[]string{"1:made"}, core.StateFailed, errcode.Internal, "", 0, 1},
		{"local creation failed before the call", refJCreate, `{"name":"a","value":"1","local":true,"failBeforeCall":true}`, false,
			[]string{"1:skipped"}, core.StateFailed, errcode.Internal, "", 0, 0},
		{"local creation failed after the call", refJCreate, `{"name":"a","value":"1","local":true,"failAfterCall":true}`, false,
			[]string{"1:made"}, core.StateFailed, errcode.Internal, "", 0, 1},
		{"local deletion failed before the call", refJDelete, `{"name":"a","local":true,"failBeforeCall":true}`, true,
			[]string{"1:skipped"}, core.StateFailed, errcode.Internal, "", 0, 0},
		{"local deletion failed after the call", refJDelete, `{"name":"a","local":true,"failAfterCall":true}`, true,
			[]string{"1:made"}, core.StateFailed, errcode.Internal, "", 0, 1},
		{"a definite refusal the handler recorded", refJModify, `{"name":"a","value":"1","failBeforeCall":true,"definite":true}`, true,
			[]string{"1:skipped"}, core.StateFailed, errcode.Internal, "", 0, 0},
		{"the observation failed", refJModify, `{"name":"a","value":"1","local":true,"failAfterCall":true,"observeErr":true}`, true,
			[]string{"1:unresolved/uncertain"}, core.StateNeedsRepair, errcode.NeedsRepair, "internal", 1, 1},
		{"a creation that matches two objects", refJCreate, `{"name":"a","value":"1","local":true,"failAfterCall":true,"candidates":true}`, false,
			[]string{"1:unresolved/ambiguous"}, core.StateNeedsRepair, errcode.NeedsRepair, "internal", 1, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newJFixture(t, jfxOptions{})
			if c.present {
				f.store.put("a", "0")
			}
			v := f.apply(alice, f.plan(alice, c.ref, c.input).PlanID, core.ApplyOptions{})
			if got := f.effectStates(v.ID); !reflect.DeepEqual(got, c.effects) {
				t.Errorf("effects = %v, want %v", got, c.effects)
			}
			if eff := f.effects(v.ID)[0]; eff.State == core.EffectMade && (eff.Resource != "jfx.item:a" || eff.Post == nil) {
				t.Errorf("made effect = %+v, want resource jfx.item:a with Post observed", eff)
			}
			if v.State != c.state || v.Owner != "" || v.Error == nil || v.Error.Code != string(c.code) || v.Error.Cause != c.cause {
				t.Errorf("change = %s owned by %q, error %+v; want %s unowned with %s (cause %q)", v.State, v.Owner, v.Error, c.state, c.code, c.cause)
			}
			if n := f.count("SELECT count(*) FROM core_locks WHERE owner = ?", v.ID); n != c.locks {
				t.Errorf("locks held = %d, want %d", n, c.locks)
			}
			if got := f.lockEpoch("jfx"); got != c.epoch {
				t.Errorf("jfx epoch = %d, want %d", got, c.epoch)
			}
			if got := f.phases(); !reflect.DeepEqual(got, []string{"apply/error/" + string(c.code)}) {
				t.Errorf("journal = %v", got)
			}
		})
	}
}

func TestTheNeedsRepairMessageNamesTheEffects(t *testing.T) {
	f := newJFixture(t, jfxOptions{})
	v := f.apply(alice, f.plan(alice, refJCreate, `{"name":"a","value":"1","local":true,"failAfterCall":true,"candidates":true}`).PlanID,
		core.ApplyOptions{})
	if v.Error == nil || v.Error.Message != "Internal error; unresolved effects: 1 jfx.item:?a (ambiguous)" {
		t.Errorf("error = %+v, want the unresolved effect named by seq, resource and reason", v.Error)
	}
	if got := f.effects(v.ID)[0]; got.Current == nil || string(got.Current.State) != "null" {
		t.Errorf("ambiguous effect Current = %+v, want the observation recorded", got.Current)
	}
}

func TestADeadlineSettlesLikeAnyFailure(t *testing.T) {
	f := newJFixture(t, jfxOptions{applyTimeout: 100 * time.Millisecond})
	f.store.put("a", "0")
	pl := f.plan(alice, refJModify, `{"name":"a","value":"1","local":true,"hangApply":true}`)
	answer := f.applyInBackground(pl.PlanID)
	f.store.waitEntered(t, "a")
	f.store.put("a", "other") // another writer changed the item while the call waited
	v := <-answer
	if v.State != core.StateNeedsRepair || v.Error == nil || v.Error.Cause != string(errcode.DeadlineExceeded) {
		t.Errorf("change = %s, error %+v; want needs_repair caused by core.deadline_exceeded", v.State, v.Error)
	}
	if got := f.effectStates(v.ID); !reflect.DeepEqual(got, []string{"1:unresolved/ambiguous"}) {
		t.Errorf("effects = %v, want the effect ambiguous", got)
	}
}

func TestSettleOnlyTouchesIntendedEffects(t *testing.T) {
	f := newJFixture(t, jfxOptions{})
	v := f.apply(alice, f.plan(alice, refJModify, `{"name":"a","value":"1","local":true}`).PlanID, core.ApplyOptions{})
	def, _ := f.reg.Action(refJModify)
	if err := f.eng.settle(context.Background(), f.eng.changeEnv(alice, def, v.ID, core.CallContext{}), def, v.ID); err != nil {
		t.Fatal(err)
	}
	if got := f.effectStates(v.ID); !reflect.DeepEqual(got, []string{"1:made"}) || len(f.store.calls("Observe")) != 0 {
		t.Errorf("effects = %v after %d observations, want 1:made untouched", got, len(f.store.calls("Observe")))
	}
}

func TestASlowObservationStillRecordsTheSettlement(t *testing.T) {
	f := newJFixture(t, jfxOptions{applyTimeout: 100 * time.Millisecond})
	f.store.put("a", "0")
	v := f.apply(alice, f.plan(alice, refJModify, `{"name":"a","value":"1","local":true,"failAfterCall":true,"hangObserve":true}`).PlanID,
		core.ApplyOptions{})
	if v.State != core.StateNeedsRepair || v.Error == nil || v.Error.Code != string(errcode.NeedsRepair) || v.Error.Cause != "internal" {
		t.Errorf("change = %s, error %+v; want needs_repair caused by internal", v.State, v.Error)
	}
	if got := f.effectStates(v.ID); !reflect.DeepEqual(got, []string{"1:unresolved/uncertain"}) {
		t.Errorf("effects = %v, want the effect uncertain", got)
	}
	if n := f.count("SELECT count(*) FROM core_locks WHERE owner = ?", v.ID); n != 1 {
		t.Errorf("locks held = %d, want 1", n)
	}
}
