package lifecycle

import (
	"context"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

func (c *inprocChildren) setUnreachable(v bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.unreachable = v
}

// reopenCtrl closes the controller engine and opens a new one over the same
// database, as a controller restart does; RecoverComposites is the caller's.
func (f *cfxFixture) reopenCtrl() {
	f.t.Helper()
	f.ctrl.Close()
	f.openCtrl()
}

// cutAt stops the runner right after it commits step stepID in state (with
// a child recorded when withChild), as if the controller died there. The
// returned channel is closed once the runner is gone.
func cutAt(f *cfxFixture, stepID, state string, withChild bool) <-chan struct{} {
	cut := make(chan struct{})
	var once sync.Once
	f.events.setOnEvent(func(ev StepEvent) {
		if ev.StepID != stepID || ev.State != state || (ev.ChildChangeID != "") != withChild {
			return
		}
		fired := false
		once.Do(func() {
			fired = true
			close(cut)
		})
		if fired {
			runtime.Goexit()
		}
	})
	return cut
}

// restartAfter waits for cut, restarts the controller and runs composite
// recovery.
func restartAfter(t *testing.T, f *cfxFixture, cut <-chan struct{}) {
	t.Helper()
	select {
	case <-cut:
	case <-time.After(10 * time.Second):
		t.Fatal("the runner never reached the cut-off point")
	}
	f.events.setOnEvent(nil)
	f.reopenCtrl()
	if err := f.ctrl.RecoverComposites(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func (f *cfxFixture) agentPlans() int {
	f.t.Helper()
	var n int
	if err := f.agentDB.Read().QueryRow("SELECT count(*) FROM core_plans").Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}

func TestCompositeRecoveryAfterACutOff(t *testing.T) {
	cases := []struct {
		name      string
		ref       core.ActionRef
		failItem  string // an item whose apply fails, so the composite unwinds
		step      string
		state     string
		withChild bool
		want      string
		undone    []string
		retiredB  bool // step b's plan is retired, never applied
	}{
		{"before b's plan request", refCfxAdd, "", "b", core.StepPlanning, false, core.StateReverted, []string{"a"}, false},
		{"after b's plan answered", refCfxAdd, "", "b", core.StepPlanned, false, core.StateReverted, []string{"a"}, true},
		{"before b's apply request", refCfxAdd, "", "b", core.StepApplying, false, core.StateReverted, []string{"a"}, true},
		{"after b's apply answered", refCfxAdd, "", "b", core.StepApplying, true, core.StateReverted, []string{"a", "b"}, false},
		{"while undoing b", refCfxAdd, "web-c", "b", core.StepUndoing, true, core.StateReverted, []string{"a", "b"}, false},
		{"forward, before y's plan request", refCfxRemove, "", "y", core.StepPlanning, false, core.StateNeedsRepair, nil, false},
		{"forward, after y's apply answered", refCfxRemove, "", "y", core.StepApplying, true, core.StateCompleted, nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newCfxFixture(t)
			if c.ref == refCfxRemove {
				f.host.setItem("web-a", "1")
				f.host.setItem("web-b", "2")
			}
			if c.failItem != "" {
				f.host.failNext(c.failItem, 1)
			}
			cut := cutAt(f, c.step, c.state, c.withChild)
			v := f.applyC(c.ref, `{"name":"web"}`, core.ApplyOptions{})
			restartAfter(t, f, cut)
			plans := f.agentPlans()
			done := f.await(v.ID, core.StateReverted, core.StateFailed, core.StateNeedsRepair, core.StateCompleted)
			if done.State != c.want {
				t.Fatalf("composite = %s (%+v), want %s", done.State, done.Error, c.want)
			}
			if n := f.agentPlans(); n != plans {
				t.Errorf("recovery made %d plans; it never plans a step", n-plans)
			}
			steps := f.stepsOf(v.ID)
			for _, id := range c.undone {
				if steps[id].State != core.StepUndone {
					t.Errorf("step %s = %s, want undone", id, steps[id].State)
				}
			}
			if c.retiredB {
				lk, err := f.agent.Lookup(context.Background(), steps["b"].PlanID, false)
				if err != nil || lk.Status != core.LookupRetired {
					t.Errorf("b's plan lookup = %+v, %v; want retired", lk, err)
				}
			}
			if c.ref == refCfxAdd && c.want == core.StateReverted {
				for _, name := range []string{"web-a", "web-b", "web-c"} {
					if _, ok := f.host.item(name); ok {
						t.Errorf("item %s is still there", name)
					}
				}
			}
		})
	}
}

func TestCompositeRecoveryAroundConfirmation(t *testing.T) {
	for _, sent := range []bool{false, true} {
		t.Run(map[bool]string{false: "before the confirm was sent", true: "after the confirm was sent"}[sent], func(t *testing.T) {
			f := newCfxFixture(t)
			v, g := awaitingGate(t, f)
			f.reopenCtrl()
			if _, err := f.ctrlDB.Writer().Exec(
				"UPDATE core_composite_steps SET state = ? WHERE change_id = ? AND step_id = 'g'", core.StepConfirming, v.ID); err != nil {
				t.Fatal(err)
			}
			if sent {
				if _, err := f.agent.Confirm(context.Background(), cfxRoot, g.ChildID); err != nil {
					t.Fatal(err)
				}
			}
			if err := f.ctrl.RecoverComposites(context.Background()); err != nil {
				t.Fatal(err)
			}
			if done := f.await(v.ID, core.StateCompleted, core.StateReverted); done.State != core.StateCompleted {
				t.Fatalf("composite = %s (%+v), want completed", done.State, done.Error)
			}
			if child := f.agentChange(g.ChildID); child.State != core.StateConfirmed {
				t.Errorf("child of g = %s, want confirmed", child.State)
			}
		})
	}
}

func TestCompositeAgentUnreachableDuringUnwind(t *testing.T) {
	f := newCfxFixture(t)
	f.host.failNext("web-c", 1)
	f.events.setOnEvent(func(ev StepEvent) {
		if ev.StepID == "b" && ev.State == core.StepUndoing {
			f.kids.setUnreachable(true)
		}
	})
	v := f.applyC(refCfxAdd, `{"name":"web"}`, core.ApplyOptions{})
	f.await(v.ID, core.StateRecovering)
	f.events.setOnEvent(nil)
	f.kids.setUnreachable(false)
	done := f.await(v.ID, core.StateReverted, core.StateNeedsRepair, core.StateFailed)
	if done.State != core.StateReverted {
		t.Fatalf("composite = %s (%+v), want reverted once the agent is back", done.State, done.Error)
	}
	for _, name := range []string{"web-a", "web-b"} {
		if _, ok := f.host.item(name); ok {
			t.Errorf("item %s is still there", name)
		}
	}
}

func TestCompositeAgentUnreachableDuringConfirmation(t *testing.T) {
	f := newCfxFixture(t)
	v := f.applyC(refCfxGate, `{"name":"web"}`, core.ApplyOptions{ConfirmTimeout: MinConfirmTimeout})
	f.await(v.ID, core.StateAwaitingConfirmation)
	f.kids.setUnreachable(true)
	got, err := f.ctrl.Confirm(context.Background(), cfxRoot, v.ID)
	if err != nil || got.State != core.StateAwaitingConfirmation {
		t.Fatalf("Confirm = %s, %v; want still awaiting confirmation", got.State, err)
	}
	f.await(v.ID, core.StateRecovering)
	f.kids.setUnreachable(false)
	if done := f.await(v.ID, core.StateCompleted, core.StateReverted); done.State != core.StateCompleted {
		t.Fatalf("composite = %s, want completed once the agent is back", done.State)
	}
}

func TestCompositeWake(t *testing.T) {
	f := newCfxFixture(t)
	v, g := awaitingGate(t, f)
	if _, err := f.agent.Confirm(context.Background(), cfxRoot, g.ChildID); err != nil {
		t.Fatal(err)
	}
	f.ctrl.Wake("01UNKNOWN", g.ChildID)
	deadline := time.Now().Add(5 * time.Second)
	for {
		got, err := f.ctrl.Change(context.Background(), v.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.State == core.StateCompleted {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("composite = %s after Wake, want completed without a poll", got.State)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestUserAndTokenMutationsWaitForCompositeRecovery(t *testing.T) {
	f := newCfxFixture(t)
	gated := []core.ActionRef{{Name: "core.user.create", Version: 1}, {Name: "core.token.create", Version: 1}}
	for _, ref := range gated {
		wantCode(t, f.ctrl.compositeGate(ref), errcode.Recovering)
	}
	if err := f.ctrl.compositeGate(core.ActionRef{Name: "core.host.list", Version: 1}); err != nil {
		t.Errorf("core.host.list is gated: %v", err)
	}
	cut := cutAt(f, "b", core.StepApplying, true)
	v := f.applyC(refCfxAdd, `{"name":"web"}`, core.ApplyOptions{})
	select {
	case <-cut:
	case <-time.After(10 * time.Second):
		t.Fatal("the runner never reached the cut-off point")
	}
	f.events.setOnEvent(nil)
	f.reopenCtrl()
	f.kids.setUnreachable(true)
	if err := f.ctrl.RecoverComposites(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.ctrl.CompositesRecovered() {
		t.Fatal("CompositesRecovered while a recovered composite is still applying")
	}
	wantCode(t, f.ctrl.compositeGate(gated[0]), errcode.Recovering)
	f.await(v.ID, core.StateRecovering)
	if !f.ctrl.CompositesRecovered() {
		t.Fatal("not CompositesRecovered with only a recovering composite left")
	}
	for _, ref := range gated {
		if err := f.ctrl.compositeGate(ref); err != nil {
			t.Errorf("%s is still gated: %v", ref, err)
		}
	}
	f.kids.setUnreachable(false)
	f.await(v.ID, core.StateReverted)
}

func TestRecoverCompositesClaimsOnlyUnfinishedComposites(t *testing.T) {
	f := newCfxFixture(t)
	v := f.applyC(refCfxAdd, `{"name":"web"}`, core.ApplyOptions{})
	f.await(v.ID, core.StateCompleted)
	f.reopenCtrl()
	if err := f.ctrl.RecoverComposites(context.Background()); err != nil {
		t.Fatal(err)
	}
	if v, _ := f.ctrl.Change(context.Background(), v.ID); v.Owner != "" || v.State != core.StateCompleted {
		t.Errorf("completed composite = %s owned by %q after recovery, want it untouched", v.State, v.Owner)
	}
	if !f.ctrl.CompositesRecovered() {
		t.Error("not CompositesRecovered with no unfinished composite")
	}
}
