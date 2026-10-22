package lifecycle

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

func TestReverseCompositeCompletesInTheBackground(t *testing.T) {
	f := newCfxFixture(t)
	v := f.applyC(refCfxAdd, `{"name":"web"}`, core.ApplyOptions{})
	if v.State != core.StateApplying || v.Owner != core.OwnerComposite || f.lockOwner("composite:cfx") != v.ID {
		t.Fatalf("Apply = %s owned by %q, want applying owned by composite with its lock", v.State, v.Owner)
	}
	done := f.await(v.ID, core.StateCompleted, core.StateReverted, core.StateFailed, core.StateNeedsRepair)
	if done.State != core.StateCompleted || done.Owner != "" || done.Error != nil {
		t.Fatalf("composite = %s owner %q error %+v, want completed and unowned", done.State, done.Owner, done.Error)
	}
	for name, want := range map[string]string{"web-a": "1", "web-b": "2", "web-c": "web-a"} {
		if got, ok := f.host.item(name); !ok || got != want {
			t.Errorf("item %s = %q (%v), want %q", name, got, ok, want)
		}
	}
	var out cfxOut
	if err := json.Unmarshal(done.Output, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Steps) != 3 || !strings.Contains(string(out.Steps["a"]), `"token":"[redacted]"`) {
		t.Errorf("output = %s, want the three steps' redacted outputs", done.Output)
	}
	for id, s := range f.stepsOf(v.ID) {
		if s.State != core.StepApplied || s.Attempt != 1 || s.ChildID == "" {
			t.Errorf("step %s = %s attempt %d child %q, want applied, attempt 1, with a child", id, s.State, s.Attempt, s.ChildID)
			continue
		}
		if child := f.agentChange(s.ChildID); child.ParentChangeID != v.ID || child.Actor != cfxRoot.Actor {
			t.Errorf("child of %s has parent %q actor %q, want %s and %s", id, child.ParentChangeID, child.Actor, v.ID, cfxRoot.Actor)
		}
	}
	if owner := f.lockOwner("composite:cfx"); owner != "" {
		t.Errorf("composite:cfx is still held by %s", owner)
	}
}

func TestForwardCompositeCompletes(t *testing.T) {
	f := newCfxFixture(t)
	f.host.setItem("web-a", "1")
	f.host.setItem("web-b", "2")
	v := f.applyC(refCfxRemove, `{"name":"web"}`, core.ApplyOptions{})
	if done := f.await(v.ID, core.StateCompleted, core.StateNeedsRepair); done.State != core.StateCompleted {
		t.Fatalf("composite = %s (%+v), want completed", done.State, done.Error)
	}
	for _, name := range []string{"web-a", "web-b"} {
		if _, ok := f.host.item(name); ok {
			t.Errorf("item %s is still there", name)
		}
	}
}

func TestReverseCompositeUnwindsAfterAStepFails(t *testing.T) {
	add := func(id, name string, after ...string) core.Step {
		return cstStep(id, refCstAdd, `{"name":"`+name+`","value":"1"}`, after...)
	}
	cases := []struct {
		name     string
		ref      core.ActionRef
		steps    []core.Step
		fail     string // the item whose apply fails
		failStep string
		undone   []string // the steps whose children are undone
	}{
		{"after a sibling that failed with nothing made", refCfxAdd, nil, "web-c", "c", []string{"a", "b"}},
		{"three compensable steps on one key", refCfxBad, []core.Step{
			add("a", "web-a"), add("b", "web-b", "a"), add("c", "web-c", "b"), add("d", "web-d", "c"),
		}, "web-d", "d", []string{"a", "b", "c"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newCfxFixture(t)
			f.bad.set(c.steps...)
			f.host.failNext(c.fail, 1)
			v := f.applyC(c.ref, `{"name":"web"}`, core.ApplyOptions{})
			done := f.await(v.ID, core.StateReverted, core.StateFailed, core.StateNeedsRepair, core.StateCompleted)
			if done.State != core.StateReverted || done.Error == nil || done.Error.Code != string(errcode.ChildFailed) ||
				!strings.Contains(done.Error.Message, "step "+c.failStep+" ") || done.Owner != "" {
				t.Fatalf("composite = %s owner %q error %+v, want reverted with core.child_failed naming step %s",
					done.State, done.Owner, done.Error, c.failStep)
			}
			steps := f.stepsOf(v.ID)
			for _, id := range c.undone {
				s := steps[id]
				if s.State != core.StepUndone {
					t.Errorf("step %s = %s, want undone", id, s.State)
				}
				if child := f.agentChange(s.ChildID); child.State != core.StateReverted {
					t.Errorf("child of step %s = %s, want reverted", id, child.State)
				}
			}
			if s := steps[c.failStep]; s.State != core.StepUndone {
				t.Errorf("failed step %s = %s, want undone (nothing made)", c.failStep, s.State)
			}
			for _, name := range []string{"web-a", "web-b", "web-c", "web-d"} {
				if _, ok := f.host.item(name); ok {
					t.Errorf("item %s is still there after the unwind", name)
				}
			}
		})
	}
}

func TestReverseCompositeWhoseFirstStepFailsIsFailed(t *testing.T) {
	f := newCfxFixture(t)
	f.host.failNext("web-a", 1)
	v := f.applyC(refCfxAdd, `{"name":"web"}`, core.ApplyOptions{})
	done := f.await(v.ID, core.StateFailed, core.StateReverted, core.StateNeedsRepair)
	if done.State != core.StateFailed || done.Error == nil || done.Error.Code != string(errcode.ChildFailed) ||
		!strings.Contains(done.Error.Message, "step a ") {
		t.Fatalf("composite = %s (%+v), want failed with core.child_failed naming step a", done.State, done.Error)
	}
	if slices.Contains(f.kids.callLog(), "Undo "+testHost) {
		t.Errorf("calls %v include an undo; nothing was made", f.kids.callLog())
	}
	if owner := f.lockOwner("composite:cfx"); owner != "" {
		t.Errorf("composite:cfx is still held by %s", owner)
	}
}

func TestForwardCompositeFailureNeedsRepair(t *testing.T) {
	f := newCfxFixture(t)
	f.host.setItem("web-a", "1")
	f.host.setItem("web-b", "2")
	f.host.failNext("web-b", 1)
	v := f.applyC(refCfxRemove, `{"name":"web"}`, core.ApplyOptions{})
	done := f.await(v.ID, core.StateNeedsRepair, core.StateCompleted, core.StateReverted)
	if done.State != core.StateNeedsRepair || done.Owner != "" || done.Error == nil ||
		done.Error.Code != string(errcode.NeedsRepair) || done.Error.Cause != string(errcode.ChildFailed) ||
		!strings.Contains(done.Error.Message, "step y ") {
		t.Fatalf("composite = %s owner %q error %+v, want needs_repair, unowned, caused by step y", done.State, done.Owner, done.Error)
	}
	if owner := f.lockOwner("composite:cfx"); owner != v.ID {
		t.Errorf("composite:cfx is held by %q, want the composite to keep it", owner)
	}
	steps := f.stepsOf(v.ID)
	if steps["x"].State != core.StepApplied || steps["y"].State != core.StepApplying || steps["y"].Err == nil {
		t.Errorf("steps x %s, y %s (%+v), want x applied and y applying with its error", steps["x"].State, steps["y"].State, steps["y"].Err)
	}
}

func TestCompositeStepsDeadline(t *testing.T) {
	f := newCfxFixture(t)
	f.host.setHooks(nil, func(name string) {
		if name == "web-b" {
			f.ctrlClock.Advance(3*DefaultApplyTimeout + StepsGrace + time.Second)
		}
	})
	v := f.applyC(refCfxAdd, `{"name":"web"}`, core.ApplyOptions{})
	done := f.await(v.ID, core.StateReverted, core.StateFailed, core.StateCompleted, core.StateNeedsRepair)
	if done.State != core.StateReverted || done.Error == nil || done.Error.Code != string(errcode.DeadlineExceeded) ||
		!strings.Contains(done.Error.Message, "step c ") {
		t.Fatalf("composite = %s (%+v), want reverted with core.deadline_exceeded naming step c", done.State, done.Error)
	}
	steps := f.stepsOf(v.ID)
	if steps["a"].State != core.StepUndone || steps["b"].State != core.StepUndone || steps["c"].State != core.StepPending {
		t.Errorf("steps a %s, b %s, c %s, want a and b undone and c never started",
			steps["a"].State, steps["b"].State, steps["c"].State)
	}
}

func TestCompositePublishesEveryStepState(t *testing.T) {
	f := newCfxFixture(t)
	f.host.setItem("web-a", "1")
	f.host.setItem("web-b", "2")
	v := f.applyC(refCfxRemove, `{"name":"web"}`, core.ApplyOptions{})
	f.await(v.ID, core.StateCompleted)
	var got []string
	for _, ev := range f.events.of(v.ID) {
		got = append(got, ev.StepID+" "+ev.State)
	}
	want := []string{
		"x planning", "x planned", "x applying", "x applying", "x applied",
		"y planning", "y planned", "y applying", "y applying", "y applied",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	evs := f.events.of(v.ID)
	if evs[2].ChildChangeID != "" || evs[3].ChildChangeID == "" || evs[4].ChildChangeID != evs[3].ChildChangeID {
		t.Errorf("x's events carry children %q %q %q, want none before the apply answered, then the child",
			evs[2].ChildChangeID, evs[3].ChildChangeID, evs[4].ChildChangeID)
	}
}

func TestCompositeStepReferenceToAMissingPath(t *testing.T) {
	f := newCfxFixture(t)
	f.bad.set(
		cstStep("a", refCstAdd, `{"name":"x-a","value":"1"}`),
		cstStep("b", refCstAdd, `{"name":"x-b","value":"${steps.a.output.nope}"}`, "a"),
	)
	v := f.applyC(refCfxBad, `{"name":"x"}`, core.ApplyOptions{})
	done := f.await(v.ID, core.StateReverted, core.StateFailed, core.StateNeedsRepair, core.StateCompleted)
	if done.State != core.StateReverted || done.Error == nil || done.Error.Code != string(errcode.ChildFailed) ||
		!strings.Contains(done.Error.Message, "step b ") {
		t.Fatalf("composite = %s (%+v), want reverted with core.child_failed naming step b", done.State, done.Error)
	}
	b := f.stepsOf(v.ID)["b"]
	if b.State != core.StepPending || b.Err == nil || b.PlanID != "" {
		t.Errorf("step b = %s plan %q error %+v, want pending, never planned, with its error", b.State, b.PlanID, b.Err)
	}
	if _, ok := f.host.item("x-a"); ok {
		t.Error("item x-a is still there after the unwind")
	}
}

func TestReverseCompositeResumesAnUnwindWhoseStartCheckWaited(t *testing.T) {
	f := newCfxFixture(t)
	f.events.setOnEvent(func(ev StepEvent) {
		if ev.StepID == "b" && ev.State == core.StepPlanning {
			f.kids.mu.Lock()
			f.kids.unreachable = true
			f.kids.mu.Unlock()
		}
	})
	v := f.applyC(refCfxAdd, `{"name":"web"}`, core.ApplyOptions{})
	var plansB atomic.Int32
	f.host.setHooks(func(name string) {
		if name == "web-b" {
			plansB.Add(1)
		}
	}, nil)
	deadline := time.Now().Add(10 * time.Second)
	for {
		c, err := f.ctrl.Change(context.Background(), v.ID)
		if err != nil {
			t.Fatal(err)
		}
		if c.Error != nil && f.ctrlClock.Pending() > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("composite = %s (%+v), want its cause recorded and its runner waiting", c.State, c.Error)
		}
		time.Sleep(time.Millisecond)
	}
	f.events.setOnEvent(nil)
	f.kids.mu.Lock()
	f.kids.unreachable = false
	f.kids.mu.Unlock()
	done := f.await(v.ID, core.StateReverted, core.StateFailed, core.StateNeedsRepair, core.StateCompleted)
	if done.State != core.StateReverted || done.Error == nil || done.Error.Code != string(errcode.ChildFailed) ||
		!strings.Contains(done.Error.Message, "step b ") {
		t.Fatalf("composite = %s (%+v), want reverted with core.child_failed naming step b", done.State, done.Error)
	}
	if a := f.stepsOf(v.ID)["a"]; a.State != core.StepUndone {
		t.Errorf("step a = %s, want undone", a.State)
	}
	if _, ok := f.host.item("web-a"); ok {
		t.Error("item web-a is still there after the unwind")
	}
	if n := plansB.Load(); n != 0 {
		t.Errorf("step b was planned %d times after it failed, want 0", n)
	}
}

func TestCompositeRecheckDoesNotHoldApplyMu(t *testing.T) {
	f := newCfxFixture(t)
	pl, err := f.planC(refCfxAdd, `{"name":"web"}`)
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	f.host.setHooks(func(string) {
		once.Do(func() {
			close(entered)
			<-release
		})
	}, nil)
	type result struct {
		v   core.ChangeView
		err error
	}
	applied := make(chan result, 1)
	go func() {
		v, err := f.ctrl.Apply(context.Background(), cfxRoot, pl.PlanID, core.ApplyOptions{})
		applied <- result{v, err}
	}()
	select {
	case <-entered:
	case r := <-applied:
		t.Fatalf("Apply = %s, %v before its recheck planned a step", r.v.State, r.err)
	}
	retired := make(chan struct{})
	go func() {
		_, _ = f.ctrl.Retire(context.Background(), "01UNKNOWNPLAN")
		close(retired)
	}()
	select {
	case <-retired:
	case <-time.After(2 * time.Second):
		t.Error("Retire waited for the composite apply's recheck")
	}
	close(release)
	f.host.setHooks(nil, nil)
	r := <-applied
	if r.err != nil {
		t.Fatalf("Apply: %v", r.err)
	}
	done := f.await(r.v.ID, core.StateCompleted, core.StateReverted, core.StateFailed, core.StateNeedsRepair)
	if done.State != core.StateCompleted {
		t.Fatalf("composite = %s (%+v), want completed", done.State, done.Error)
	}
}
