package lifecycle

import (
	"context"
	"errors"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// susFlag is a Config.Suspended whose answer a test changes while the
// engine runs.
type susFlag struct {
	held atomic.Bool
	err  atomic.Pointer[error]
}

func (s *susFlag) suspended(context.Context) (bool, error) {
	if err := s.err.Load(); err != nil {
		return false, *err
	}
	return s.held.Load(), nil
}

// susReopenCtrl is reopenCtrl with Config.Suspended set to suspended.
func susReopenCtrl(f *cfxFixture, suspended func(context.Context) (bool, error)) {
	f.t.Helper()
	f.ctrl.Close()
	f.ctrlDB = openDBAt(f.t, f.ctrlPath, store.SideController)
	cfg := f.ctrlConfig()
	cfg.Suspended = suspended
	eng, err := New(cfg)
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(eng.Close)
	f.kids.setEngines(eng, nil)
	eng.SetChildren(f.kids)
	if err := eng.Recover(context.Background()); err != nil {
		f.t.Fatal(err)
	}
	f.ctrl = eng
}

// susStill checks for a while that composite id stays in state while the
// controller clock moves past several child polls, and that the agent gets
// no call meanwhile.
func susStill(t *testing.T, f *cfxFixture, id, state string) {
	t.Helper()
	calls := len(f.kids.callLog())
	for range 5 {
		f.ctrlClock.Advance(ChildPoll)
		time.Sleep(20 * time.Millisecond)
	}
	if v := f.ctrlChange(id); v.State != state {
		t.Fatalf("composite = %s, want it held in %s", v.State, state)
	}
	if got := f.kids.callLog(); len(got) != calls {
		t.Errorf("the agent got calls %v while the engine was suspended", got[calls:])
	}
}

// ctrlChange reads change id from the controller engine.
func (f *cfxFixture) ctrlChange(id string) core.ChangeView {
	f.t.Helper()
	v, err := f.ctrl.Change(context.Background(), id)
	if err != nil {
		f.t.Fatal(err)
	}
	return v
}

func TestSuspendedHoldsCompositeRecovery(t *testing.T) {
	failed := errors.New("controller.db is locked")
	for _, c := range []struct {
		name string
		set  func(*susFlag)
	}{
		{"the flag is set", func(s *susFlag) { s.held.Store(true) }},
		{"the flag cannot be read", func(s *susFlag) { s.err.Store(&failed) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newCfxFixture(t)
			cut := cutAt(f, "b", core.StepApplying, true)
			v := f.applyC(refCfxAdd, `{"name":"web"}`, core.ApplyOptions{})
			select {
			case <-cut:
			case <-time.After(10 * time.Second):
				t.Fatal("the runner never reached the cut-off point")
			}
			f.events.setOnEvent(nil)
			flag := &susFlag{}
			c.set(flag)
			susReopenCtrl(f, flag.suspended)
			if err := f.ctrl.RecoverComposites(context.Background()); err != nil {
				t.Fatal(err)
			}
			if !f.ctrl.CompositesRecovered() {
				t.Error("CompositesRecovered = false; a suspended recovery has nothing to wait for")
			}
			susStill(t, f, v.ID, core.StateApplying)

			// Held back, not lost: a later start without the flag recovers it.
			susReopenCtrl(f, nil)
			if err := f.ctrl.RecoverComposites(context.Background()); err != nil {
				t.Fatal(err)
			}
			if done := f.await(v.ID, core.StateReverted, core.StateCompleted, core.StateNeedsRepair); done.State != core.StateReverted {
				t.Fatalf("composite = %s (%+v), want reverted by the later recovery", done.State, done.Error)
			}
		})
	}
}

func TestSuspendedHoldsTheChildWatcher(t *testing.T) {
	f := newCfxFixture(t)
	flag := &susFlag{}
	susReopenCtrl(f, flag.suspended)
	v, g := awaitingGate(t, f)
	flag.held.Store(true)
	if _, err := f.agent.Confirm(context.Background(), cfxRoot, g.ChildID); err != nil {
		t.Fatal(err)
	}
	f.ctrl.Wake(g.ChildID)
	susStill(t, f, v.ID, core.StateAwaitingConfirmation)

	flag.held.Store(false)
	if done := f.await(v.ID, core.StateCompleted, core.StateReverted); done.State != core.StateCompleted {
		t.Fatalf("composite = %s, want completed once the runner resumes", done.State)
	}
}

func TestSuspendedHoldsModuleRecoveryHandlers(t *testing.T) {
	f, h := newRcvFixture(t)
	f.eng.Close()
	flag := &susFlag{}
	flag.held.Store(true)
	cfg := f.config()
	cfg.Suspended = flag.suspended
	eng, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(eng.Close)
	f.eng = eng
	if err := f.eng.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}

	pl := f.plan(alice, refRcvFix, `{"name":"a","value":"1","failBefore":true}`)
	if _, err := f.eng.Apply(context.Background(), alice, pl.PlanID, core.ApplyOptions{}); !errors.Is(err, errRecovererSuspended) {
		t.Fatalf("Apply = %v, want the suspended recovery handler's error", err)
	}
	if calls, _, _ := h.recovered(); calls != 0 {
		t.Fatalf("the recovery handler ran %d times while suspended", calls)
	}
	ids := f.count("SELECT count(*) FROM core_changes WHERE state = ? AND recovery_owner = ?",
		core.StateRecovering, core.OwnerModule)
	if ids != 1 {
		t.Fatalf("%d changes recovering and owned by module, want the failed one held there", ids)
	}

	// Held back, not lost: the next start without the flag runs the handler.
	h.script(core.RecoveryOutcome{State: core.RecoveryCompleted}, nil)
	f.eng.Close()
	f.open()
	if err := f.eng.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	var id string
	if err := f.db.Read().QueryRow("SELECT id FROM core_changes WHERE action = ?", refRcvFix.Name).Scan(&id); err != nil {
		t.Fatal(err)
	}
	f.recoveryAwait(id, core.StateCompleted)
	if calls, _, changes := h.recovered(); calls != 1 || !slices.Equal(changes, []string{id}) {
		t.Errorf("the recovery handler ran %d times for %v, want once for %s", calls, changes, id)
	}
}

func TestUnsuspendedEngineIgnoresTheHook(t *testing.T) {
	// The agent passes no Suspended; a nil hook must never hold anything.
	f := newCfxFixture(t)
	if f.ctrl.suspended(context.Background(), "nothing") {
		t.Fatal("an engine without Config.Suspended reports suspended")
	}
}
