package lifecycle

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// hangVerify makes Verify ignore its context and block until the test ends,
// and shortens the engine's verify deadline so the test runs in real time.
func (f *fixture) hangVerify() {
	f.t.Helper()
	release := make(chan struct{})
	returned := make(chan struct{}, 10)
	f.host.onVerify = func(context.Context) {
		<-release
		returned <- struct{}{}
	}
	f.eng.verifyTimeout = 50 * time.Millisecond
	f.t.Cleanup(func() {
		close(release)
		select {
		case <-returned: // the abandoned Verify finishes before the database closes
		case <-time.After(10 * time.Second):
			f.t.Error("the abandoned Verify did not return")
		}
	})
}

func TestVerifyRunsUnderADeadline(t *testing.T) {
	if VerifyTimeout != 30*time.Second {
		t.Fatalf("VerifyTimeout = %v, want 30s", VerifyTimeout)
	}
	f := newFixture(t)
	if f.eng.verifyTimeout != VerifyTimeout {
		t.Fatalf("engine verify timeout = %v, want VerifyTimeout", f.eng.verifyTimeout)
	}
	var deadline time.Time
	var ok bool
	f.host.onVerify = func(ctx context.Context) { deadline, ok = ctx.Deadline() }
	before := time.Now()
	f.apply(alice, f.plan(alice, refAdd, `{"name":"a","value":"1"}`).PlanID, core.ApplyOptions{})
	if !ok || deadline.Before(before.Add(VerifyTimeout)) || deadline.After(time.Now().Add(VerifyTimeout)) {
		t.Errorf("Verify deadline = %v (set %v), want VerifyTimeout from the call", deadline, ok)
	}
}

func TestAHungVerifyFailsTheChange(t *testing.T) {
	f := newFixture(t)
	log := f.logged()
	f.hangVerify()
	start := time.Now()
	v := f.apply(alice, f.plan(alice, refSet, `{"name":"a","value":"1"}`).PlanID, core.ApplyOptions{})
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("Apply took %v, want about the 50ms verify deadline", elapsed)
	}
	if v.State != core.StateReverted || v.Error == nil || v.Error.Code != string(errcode.Internal) {
		t.Errorf("change = %+v (error %+v), want reverted with internal", v, v.Error)
	}
	if _, ok := f.host.items["a"]; ok {
		t.Error("the item was not reverted")
	}
	if !strings.Contains(log.String(), "Verify of fix.item.set/v1 did not finish within 50ms") {
		t.Errorf("log does not report the deadline:\n%s", log)
	}
}

func TestRecoveryBoundsVerify(t *testing.T) {
	f := newFixture(t)
	_, id := f.crash(refAdd, `{"name":"a","value":"1","crashApply":true}`, core.ApplyOptions{})
	f.restart()
	f.hangVerify()
	f.recoverAll()
	v, err := f.eng.Change(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if v.State != core.StateInterrupted || v.Error == nil ||
		!strings.HasSuffix(v.Error.Message, "verify failed: "+errcode.Internal.Title()) {
		t.Errorf("change = %+v (error %+v), want interrupted reporting the failed verify", v, v.Error)
	}
}
