package lifecycle

import (
	"context"
	"sync"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)

// exitRecorder stands in for os.Exit as the engine's Config.Exit.
type exitRecorder struct {
	mu    sync.Mutex
	codes []int
}

func (r *exitRecorder) exit(code int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.codes = append(r.codes, code)
}

func (r *exitRecorder) got() []int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]int(nil), r.codes...)
}

// recordExits reopens the fixture's engine with Config.Exit recording.
func (f *jfixture) recordExits() *exitRecorder {
	f.t.Helper()
	r := &exitRecorder{}
	f.eng.Close()
	cfg := f.config()
	cfg.Exit = r.exit
	eng, err := New(cfg)
	if err != nil {
		f.t.Fatal(err)
	}
	f.eng = eng
	f.t.Cleanup(eng.Close)
	return r
}

func TestTheWatchdogExitsWhenARevertibleApplyHangs(t *testing.T) {
	for _, hang := range []string{"hangApply", "hangVerify"} {
		t.Run(hang, func(t *testing.T) {
			f := newJFixture(t, jfxOptions{})
			exits := f.recordExits()
			pl := f.plan(alice, refJSet, `{"name":"a","value":"1","local":true,"`+hang+`":true}`)
			answer := f.applyInBackground(pl.PlanID)
			f.store.waitEntered(t, "a")
			f.clock.Advance(DefaultConfirmTimeout)
			if got := exits.got(); len(got) != 1 || got[0] != WatchdogExitCode {
				t.Fatalf("exits = %v, want one with %d", got, WatchdogExitCode)
			}
			id := f.changeOf(pl.PlanID)
			if s := f.state(id); s != core.StateApplying {
				t.Errorf("state = %s, want applying: the watchdog changes nothing", s)
			}
			entries, err := f.journal.Entries(context.Background(), 0, 10)
			if err != nil || len(entries) != 1 || entries[0].Phase != "revert-watchdog" || entries[0].ChangeID != id ||
				entries[0].Actor != ActorRevertTimer || entries[0].ErrorCode != "core.deadline_exceeded" {
				t.Errorf("journal = %+v, %v; want one revert-watchdog entry for %s", entries, err, id)
			}
			if v := <-answer; v.State != core.StateApplying {
				t.Errorf("apply answered %s, want applying", v.State)
			}
			f.store.open("a")
			f.waitState(id, core.StatePending)
			if f.clock.Pending() != 0 {
				t.Errorf("armed timers = %d, want none left", f.clock.Pending())
			}
		})
	}
}

func TestATimerAtAPendingDeadlineRevertsWithoutExiting(t *testing.T) {
	f := newJFixture(t, jfxOptions{})
	exits := f.recordExits()
	v := f.pendingJSet()
	f.clock.Advance(DefaultConfirmTimeout)
	if s := f.state(v.ID); s != core.StateReverted || len(exits.got()) != 0 {
		t.Errorf("state = %s, exits = %v; want reverted without an exit", s, exits.got())
	}
}

func TestConfirmRacingTheTimer(t *testing.T) {
	for range 20 {
		f := newJFixture(t, jfxOptions{})
		exits := f.recordExits()
		v := f.pendingJSet()
		var wg sync.WaitGroup
		wg.Go(func() { _, _ = f.eng.Confirm(context.Background(), alice, v.ID) })
		wg.Go(func() { f.clock.Advance(DefaultConfirmTimeout) })
		wg.Wait()
		s := f.state(v.ID)
		if (s != core.StateConfirmed && s != core.StateReverted) || len(exits.got()) != 0 {
			t.Fatalf("state = %s, exits = %v; want confirmed or reverted and no exit", s, exits.got())
		}
		if f.count("SELECT count(*) FROM core_locks") != 0 || f.clock.Pending() != 0 {
			t.Fatalf("locks = %d, timers = %d; want none", f.count("SELECT count(*) FROM core_locks"), f.clock.Pending())
		}
	}
}
