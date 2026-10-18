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

// crash plans and applies an input whose handler stops its goroutine in
// Apply, which stands for the process dying after the intent is persisted and
// before the result is recorded. It returns the plan and change ids.
func (f *fixture) crash(ref core.ActionRef, input string, opts core.ApplyOptions) (planID, changeID string) {
	f.t.Helper()
	planID = f.plan(alice, ref, input).PlanID
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		_, _ = f.eng.Apply(context.Background(), alice, planID, opts)
		f.t.Error("Apply returned; want its goroutine stopped inside the handler")
	}()
	<-stopped
	if err := f.db.Read().QueryRow("SELECT id FROM core_changes WHERE plan_id = ?", planID).Scan(&changeID); err != nil {
		f.t.Fatal(err)
	}
	return planID, changeID
}

func (f *fixture) recoverAll() {
	f.t.Helper()
	if err := f.eng.Recover(context.Background()); err != nil {
		f.t.Fatalf("Recover: %v", err)
	}
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

func TestRecoverRecordsAFailedRevert(t *testing.T) {
	f := newFixture(t)
	v := f.pending(`{"name":"a","value":"1","failRevert":true}`)
	f.restart()
	f.recoverAll()
	c, err := f.eng.Change(context.Background(), v.ID)
	if err != nil {
		t.Fatal(err)
	}
	if c.State != core.StateFailed || c.Error == nil || c.Error.Code != string(errcode.ChangeRevertFailed) {
		t.Errorf("change = %+v (error %+v), want failed with change.revert_failed", c, c.Error)
	}
	if got := f.phases(); !reflect.DeepEqual(got, []string{"apply/ok", "recover/error/change.revert_failed"}) {
		t.Errorf("journal = %v", got)
	}
	if f.count("SELECT count(*) FROM core_locks") != 0 {
		t.Error("recovery kept the lock of a failed change")
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
	if err := <-purged; err != nil {
		t.Fatalf("Purge: %v", err)
	}
	if n := f.count("SELECT count(*) FROM core_plans WHERE id = ?", pl.PlanID); n != 1 {
		t.Errorf("plan rows = %d, want 1", n)
	}
}
