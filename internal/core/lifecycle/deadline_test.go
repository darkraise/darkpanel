package lifecycle

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// applyInBackground applies planID as alice on a goroutine and returns the
// channel its view arrives on.
func (f *jfixture) applyInBackground(planID string) <-chan core.ChangeView {
	out := make(chan core.ChangeView, 1)
	go func() {
		v, err := f.eng.Apply(context.Background(), alice, planID, core.ApplyOptions{})
		if err != nil {
			f.t.Error(err)
		}
		out <- v
	}()
	return out
}

func TestApplyPastItsDeadlineFails(t *testing.T) {
	f := newJFixture(t, jfxOptions{applyTimeout: 100 * time.Millisecond})
	v := f.apply(alice, f.plan(alice, refJModify, `{"name":"a","value":"1","local":true,"hangApply":true}`).PlanID, core.ApplyOptions{})
	if v.State != core.StateFailed || v.Error == nil || v.Error.Code != string(errcode.DeadlineExceeded) ||
		!strings.Contains(v.Error.Message, "jfx.item.modify/v1 did not finish within 100ms") {
		t.Errorf("change = %+v (error %+v), want failed with core.deadline_exceeded", v, v.Error)
	}
	if f.store.cancelled != 1 {
		t.Errorf("cancelled applies = %d, want the handler's context cancelled", f.store.cancelled)
	}
	if got := f.phases(); len(got) != 1 || got[0] != "apply/error/core.deadline_exceeded" {
		t.Errorf("journal = %v", got)
	}
}

func TestLongRunningApplyAnswersAtOnce(t *testing.T) {
	f := newJFixture(t, jfxOptions{})
	v := f.apply(alice, f.plan(alice, refJSlow, `{"name":"a","value":"1","local":true,"hangApply":true}`).PlanID, core.ApplyOptions{})
	if v.State != core.StateApplying || v.Output != nil {
		t.Fatalf("answer = %+v, want the applying change", v)
	}
	f.store.waitEntered(t, "a")
	f.store.open("a")
	if done := f.waitState(v.ID, core.StateCompleted); string(done.Output) != `{"resource":"jfx.item:a","token":"[redacted]"}` {
		t.Errorf("output = %s, want the redacted output", done.Output)
	}
}

func TestSynchronousApplyAnswersAtSyncAnswer(t *testing.T) {
	f := newJFixture(t, jfxOptions{})
	if f.eng.cfg.SyncAnswer != SyncAnswer || SyncAnswer != 50*time.Second {
		t.Fatalf("SyncAnswer = %v (engine %v), want 50s", SyncAnswer, f.eng.cfg.SyncAnswer)
	}
	answer := f.applyInBackground(f.plan(alice, refJModify, `{"name":"a","value":"1","local":true,"hangApply":true}`).PlanID)
	f.store.waitEntered(t, "a")
	f.clock.Advance(SyncAnswer - time.Second)
	select {
	case v := <-answer:
		t.Fatalf("answered %+v before SyncAnswer", v)
	case <-time.After(50 * time.Millisecond):
	}
	f.clock.Advance(time.Second)
	v := <-answer
	if v.State != core.StateApplying {
		t.Fatalf("answer = %+v, want the applying change", v)
	}
	f.store.open("a")
	f.waitState(v.ID, core.StateCompleted)
}

func TestSynchronousApplyRunErrorAfterTheAnswerIsLogged(t *testing.T) {
	f := newJFixture(t, jfxOptions{})
	f.eng.Close()
	log := &taskLog{}
	cfg := f.config()
	cfg.Base.Log = slog.New(slog.NewTextHandler(log, nil))
	eng, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	f.eng = eng
	t.Cleanup(eng.Close)
	answer := f.applyInBackground(f.plan(alice, refJModify, `{"name":"a","value":"1","local":true,"hangApply":true}`).PlanID)
	f.store.waitEntered(t, "a")
	f.clock.Advance(SyncAnswer)
	v := <-answer
	if v.State != core.StateApplying {
		t.Fatalf("answer = %+v, want the applying change", v)
	}
	f.recoverExec(`CREATE TRIGGER fail_record BEFORE UPDATE OF state ON core_changes
		BEGIN SELECT RAISE(ABORT, 'injected record failure'); END`)
	f.store.open("a")
	deadline := time.Now().Add(10 * time.Second)
	for {
		text := log.String()
		if strings.Contains(text, "lifecycle: apply run") && strings.Contains(text, v.ID) &&
			strings.Contains(text, "injected record failure") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no apply run error was logged for %s:\n%s", v.ID, text)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestSealedInputIsKeptUntilTheChangeIsFinished(t *testing.T) {
	f := newFixture(t)
	pl := f.plan(alice, refSet, `{"name":"a","value":"1"}`)
	v := f.apply(alice, pl.PlanID, core.ApplyOptions{})
	if v.State != core.StatePending || f.planInputCleared(pl.PlanID) {
		t.Fatalf("pending change %s: input cleared = %v, want kept", v.State, f.planInputCleared(pl.PlanID))
	}
	if _, err := f.eng.Confirm(context.Background(), alice, v.ID); err != nil {
		t.Fatal(err)
	}
	if !f.planInputCleared(pl.PlanID) {
		t.Error("the confirmed change's plan kept its sealed input")
	}
}

func TestCloseCancelsARunningApply(t *testing.T) {
	f := newJFixture(t, jfxOptions{})
	answer := f.applyInBackground(f.plan(alice, refJModify, `{"name":"a","value":"1","local":true,"hangApply":true}`).PlanID)
	f.store.waitEntered(t, "a")
	f.eng.Close()
	v := <-answer
	if v.State != core.StateFailed || v.Error == nil || v.Error.Code != string(errcode.Internal) {
		t.Errorf("change = %+v (error %+v), want failed with internal", v, v.Error)
	}
	if f.store.cancelled != 1 || f.count("SELECT count(*) FROM core_locks") != 0 {
		t.Errorf("cancelled = %d, locks = %d; want the handler cancelled and the lock released",
			f.store.cancelled, f.count("SELECT count(*) FROM core_locks"))
	}
}
