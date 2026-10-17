package lifecycle

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// pending applies fix.item.set as alice and returns the pending change.
func (f *fixture) pending(input string) core.ChangeView {
	f.t.Helper()
	v := f.apply(alice, f.plan(alice, refSet, input).PlanID, core.ApplyOptions{})
	if v.State != core.StatePending {
		f.t.Fatalf("state = %s, want pending", v.State)
	}
	return v
}

func (f *fixture) state(changeID string) string {
	f.t.Helper()
	v, err := f.eng.Change(context.Background(), changeID)
	if err != nil {
		f.t.Fatal(err)
	}
	return v.State
}

func TestConfirmKeepsTheChange(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.host.items["a"] = "old"
	v := f.pending(`{"name":"a","value":"1"}`)
	if f.clock.Pending() != 1 {
		t.Fatalf("armed timers = %d, want 1", f.clock.Pending())
	}
	c, err := f.eng.Confirm(ctx, bob, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	if c.State != core.StateConfirmed || c.Actor != alice.Actor {
		t.Errorf("confirmed change = %+v, want confirmed with the applier as actor", c)
	}
	if f.clock.Pending() != 0 || f.count("SELECT count(*) FROM core_locks") != 0 {
		t.Errorf("timers = %d, locks = %d; want none", f.clock.Pending(), f.count("SELECT count(*) FROM core_locks"))
	}
	if got := f.phases(); !reflect.DeepEqual(got, []string{"apply/ok", "confirm/ok"}) {
		t.Errorf("journal = %v", got)
	}
	if entries, _ := f.journal.Entries(ctx, 0, 10); entries[1].Actor != bob.Actor {
		t.Errorf("confirm actor = %s, want %s", entries[1].Actor, bob.Actor)
	}
	f.clock.Advance(2 * DefaultConfirmTimeout)
	if f.host.items["a"] != "1" {
		t.Errorf("item a = %q after the deadline, want the confirmed 1", f.host.items["a"])
	}
	_, err = f.eng.Confirm(ctx, alice, v.ID)
	wantCode(t, err, errcode.ChangeNotPending)
	_, err = f.eng.Revert(ctx, alice, v.ID)
	wantCode(t, err, errcode.ChangeNotPending)
}

func TestRevertRestoresTheSnapshot(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.host.items["a"] = "old"
	v := f.pending(`{"name":"a","value":"1"}`)
	r, err := f.eng.Revert(ctx, bob, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	if r.State != core.StateReverted || r.Error != nil || f.host.items["a"] != "old" {
		t.Errorf("reverted change = %+v, item a = %q; want reverted to old", r, f.host.items["a"])
	}
	if f.clock.Pending() != 0 || f.count("SELECT count(*) FROM core_locks") != 0 {
		t.Error("the revert left a timer or a lock")
	}
	if got := f.phases(); !reflect.DeepEqual(got, []string{"apply/ok", "revert/ok"}) {
		t.Errorf("journal = %v", got)
	}
	if entries, _ := f.journal.Entries(ctx, 0, 10); entries[1].Actor != bob.Actor {
		t.Errorf("revert actor = %s, want %s", entries[1].Actor, bob.Actor)
	}
}

func TestRevertFailureFailsTheChange(t *testing.T) {
	f := newFixture(t)
	v := f.pending(`{"name":"a","value":"1","failRevert":true}`)
	r, err := f.eng.Revert(context.Background(), alice, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	if r.State != core.StateFailed || r.Error == nil || r.Error.Code != string(errcode.ChangeRevertFailed) {
		t.Errorf("change = %+v (error %+v), want failed with change.revert_failed", r, r.Error)
	}
	if got := f.phases(); !reflect.DeepEqual(got, []string{"apply/ok", "revert/error/change.revert_failed"}) {
		t.Errorf("journal = %v", got)
	}
	if f.count("SELECT count(*) FROM core_locks") != 0 {
		t.Error("a failed revert kept its locks")
	}
}

func TestTimerRevertsAtTheDeadline(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	v := f.pending(`{"name":"a","value":"1"}`)
	f.clock.Advance(DefaultConfirmTimeout - time.Second)
	if s := f.state(v.ID); s != core.StatePending {
		t.Fatalf("state before the deadline = %s, want pending", s)
	}
	f.clock.Advance(time.Second)
	if s := f.state(v.ID); s != core.StateReverted {
		t.Fatalf("state at the deadline = %s, want reverted", s)
	}
	if _, ok := f.host.items["a"]; ok {
		t.Error("the timer revert left item a in place")
	}
	entries, err := f.journal.Entries(ctx, 0, 10)
	if err != nil || len(entries) != 2 || entries[1].Phase != "revert" || entries[1].Actor != ActorRevertTimer {
		t.Errorf("journal = %+v, %v; want a revert by %s", entries, err, ActorRevertTimer)
	}
}

func TestConfirmNowConfirmsInTheApply(t *testing.T) {
	f := newFixture(t)
	pl := f.plan(alice, refSet, `{"name":"a","value":"1"}`)
	v := f.apply(alice, pl.PlanID, core.ApplyOptions{ConfirmNow: true})
	if v.State != core.StateConfirmed || string(v.Output) != `{"previous":"","token":"tok-a"}` {
		t.Errorf("change = %+v, want confirmed with the raw output", v)
	}
	if got := f.phases(); !reflect.DeepEqual(got, []string{"apply/ok", "confirm/ok"}) {
		t.Errorf("journal = %v", got)
	}
	if f.clock.Pending() != 0 {
		t.Errorf("armed timers = %d, want 0", f.clock.Pending())
	}
}

func TestConfirmAndRevertRace(t *testing.T) {
	for range 20 {
		f := newFixture(t)
		ctx := context.Background()
		v := f.pending(`{"name":"a","value":"1"}`)
		var confirmErr, revertErr error
		var wg sync.WaitGroup
		wg.Go(func() { _, confirmErr = f.eng.Confirm(ctx, alice, v.ID) })
		wg.Go(func() { _, revertErr = f.eng.Revert(ctx, bob, v.ID) })
		wg.Wait()

		var coded *errcode.Error
		state := f.state(v.ID)
		switch {
		case confirmErr == nil && errors.As(revertErr, &coded) && coded.Code == errcode.ChangeNotPending:
			if state != core.StateConfirmed || f.host.items["a"] != "1" {
				t.Fatalf("confirm won but state = %s, item = %q", state, f.host.items["a"])
			}
		case revertErr == nil && errors.As(confirmErr, &coded) && coded.Code == errcode.ChangeNotPending:
			if _, ok := f.host.items["a"]; state != core.StateReverted || ok {
				t.Fatalf("revert won but state = %s, item present = %v", state, ok)
			}
		default:
			t.Fatalf("confirm = %v, revert = %v; want exactly one change.not_pending", confirmErr, revertErr)
		}
		if f.count("SELECT count(*) FROM core_locks") != 0 || len(f.phases()) != 2 {
			t.Fatalf("locks = %d, journal = %v; want no locks and two entries", f.count("SELECT count(*) FROM core_locks"), f.phases())
		}
	}
}

func TestConfirmAndRevertOnlyPending(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	v := f.apply(alice, f.plan(alice, refAdd, `{"name":"a","value":"1"}`).PlanID, core.ApplyOptions{})
	_, err := f.eng.Confirm(ctx, alice, v.ID)
	wantCode(t, err, errcode.ChangeNotPending)
	_, err = f.eng.Revert(ctx, alice, v.ID)
	wantCode(t, err, errcode.ChangeNotPending)
	_, err = f.eng.Confirm(ctx, alice, "01NOPE")
	wantCode(t, err, errcode.ChangeNotFound)
	_, err = f.eng.Revert(ctx, alice, "01NOPE")
	wantCode(t, err, errcode.ChangeNotFound)
}

func TestCloseStopsTimers(t *testing.T) {
	f := newFixture(t)
	v := f.pending(`{"name":"a","value":"1"}`)
	f.eng.Close()
	if f.clock.Pending() != 0 {
		t.Fatalf("armed timers after Close = %d, want 0", f.clock.Pending())
	}
	f.clock.Advance(2 * DefaultConfirmTimeout)
	if s := f.state(v.ID); s != core.StatePending {
		t.Errorf("state = %s, want pending until recovery", s)
	}
}
