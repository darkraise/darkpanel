package lifecycle

import (
	"context"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
)

func TestVerifySharesTheApplyDeadline(t *testing.T) {
	if DefaultApplyTimeout != 30*time.Second {
		t.Fatalf("DefaultApplyTimeout = %v, want 30s", DefaultApplyTimeout)
	}
	f := newFixture(t)
	var deadline time.Time
	var ok bool
	f.host.onVerify = func(ctx context.Context) { deadline, ok = ctx.Deadline() }
	before := time.Now()
	f.apply(alice, f.plan(alice, refAdd, `{"name":"a","value":"1"}`).PlanID, core.ApplyOptions{})
	if !ok || deadline.Before(before.Add(DefaultApplyTimeout)) || deadline.After(time.Now().Add(DefaultApplyTimeout)) {
		t.Errorf("Verify deadline = %v (set %v), want DefaultApplyTimeout from the apply", deadline, ok)
	}
}

func TestAVerifyIgnoringItsDeadlineKeepsTheChange(t *testing.T) {
	f := newJFixture(t, jfxOptions{applyTimeout: 100 * time.Millisecond})
	pl := f.plan(alice, refJModify, `{"name":"a","value":"1","local":true,"hangVerify":true}`)
	done := make(chan core.ChangeView, 1)
	go func() {
		v, err := f.eng.Apply(context.Background(), alice, pl.PlanID, core.ApplyOptions{})
		if err != nil {
			t.Error(err)
		}
		done <- v
	}()
	f.store.waitEntered(t, "a")
	time.Sleep(300 * time.Millisecond) // three times the deadline
	id := f.changeOf(pl.PlanID)
	if s := f.state(id); s != core.StateApplying || f.count("SELECT count(*) FROM core_locks WHERE owner = ?", id) != 1 {
		t.Fatalf("past the deadline: state %s, want applying with its lock", s)
	}
	f.store.open("a")
	if v := <-done; v.State != core.StateCompleted {
		t.Errorf("state = %s, want completed once Verify returned", v.State)
	}
}

func TestRecoveryBoundsVerify(t *testing.T) {
	f := newFixture(t)
	_, id := f.crash(refAdd, `{"name":"a","value":"1","crashApply":true}`, core.ApplyOptions{})
	f.restart()
	entered, release := make(chan struct{}), make(chan struct{})
	f.host.onVerify = func(context.Context) {
		close(entered)
		<-release
	}
	t.Cleanup(func() { close(release) })
	recovered := make(chan error, 1)
	go func() { recovered <- f.eng.Recover(context.Background()) }()
	<-entered
	f.clock.Advance(RecoveryVerifyBound)
	if err := <-recovered; err != nil {
		t.Fatal(err)
	}
	v, err := f.eng.Change(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if v.State != core.StateInterrupted || v.Error == nil || v.Error.Message != "interrupted by a restart during apply; verify did not finish" {
		t.Errorf("change = %+v (error %+v), want interrupted reporting the unfinished verify", v, v.Error)
	}
}
