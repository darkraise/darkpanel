package lifecycle

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// item is the name every panic test uses; the fixture's panic value quotes
// it, and the log must not.
const item = "zz-item"

// logged replaces the fixture's engine with one that logs to the returned
// buffer.
func (f *fixture) logged() *bytes.Buffer {
	f.t.Helper()
	f.eng.Close()
	buf := &bytes.Buffer{}
	cfg := f.config()
	cfg.Base.Log = slog.New(slog.NewTextHandler(buf, nil))
	eng, err := New(cfg)
	if err != nil {
		f.t.Fatal(err)
	}
	f.eng = eng
	f.t.Cleanup(eng.Close)
	return buf
}

func (f *fixture) panicOn(call string) {
	f.host.mu.Lock()
	defer f.host.mu.Unlock()
	f.host.panicOn = call
}

// wantPanicLogged checks that log holds exactly one handler panic record for
// call, with a stack, and nothing of the input.
func wantPanicLogged(t *testing.T, log *bytes.Buffer, call string) {
	t.Helper()
	text := log.String()
	if n := strings.Count(text, `msg="lifecycle: handler panic"`); n != 1 {
		t.Errorf("handler panic logged %d times, want once:\n%s", n, text)
	}
	if !strings.Contains(text, "call="+call) || !strings.Contains(text, "handler=fix.item.") ||
		!strings.Contains(text, "panic=string") || !strings.Contains(text, "stack=") {
		t.Errorf("log lacks the call, handler, panic type or stack:\n%s", text)
	}
	if strings.Contains(text, item) {
		t.Errorf("log quotes the input:\n%s", text)
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, `msg="lifecycle: action failed"`) && strings.Contains(line, "panicked") {
			t.Errorf("the panic was logged a second time as a failed action:\n%s", text)
		}
	}
}

// wantPlain checks that err is a plain error, which the API answers as
// internal.
func wantPlain(t *testing.T, err error) {
	t.Helper()
	var coded *errcode.Error
	if err == nil || errors.As(err, &coded) {
		t.Fatalf("err = %v, want a plain error", err)
	}
}

func TestPanicsBeforeAChangeAreErrors(t *testing.T) {
	input := `{"name":"` + item + `","value":"1"}`
	cases := []struct {
		call string
		run  func(f *fixture) error
	}{
		{"Read", func(f *fixture) error {
			_, err := f.eng.Read(context.Background(), alice, refGet, json.RawMessage(`{"name":"`+item+`"}`))
			return err
		}},
		{"Plan", func(f *fixture) error {
			_, err := f.eng.Plan(context.Background(), alice, refSet, json.RawMessage(input))
			return err
		}},
	}
	for _, c := range cases {
		t.Run(c.call, func(t *testing.T) {
			f := newFixture(t)
			log := f.logged()
			f.panicOn(c.call)
			wantPlain(t, c.run(f))
			wantPanicLogged(t, log, c.call)
			if n := f.count("SELECT count(*) FROM core_plans"); n != 0 {
				t.Errorf("plans = %d, want 0", n)
			}
		})
	}
}

func TestPanicsDuringApplyChecksAreErrors(t *testing.T) {
	// Plan panics in apply's recheck; Snapshot before the intent is stored.
	for _, call := range []string{"Plan", "Snapshot"} {
		t.Run(call, func(t *testing.T) {
			f := newFixture(t)
			ctx := context.Background()
			pl := f.plan(alice, refSet, `{"name":"`+item+`","value":"1"}`)
			log := f.logged()
			f.panicOn(call)
			_, err := f.eng.Apply(ctx, alice, pl.PlanID, core.ApplyOptions{})
			wantPlain(t, err)
			wantPanicLogged(t, log, call)
			if n := f.count("SELECT count(*) FROM core_changes"); n != 0 {
				t.Errorf("changes = %d, want 0", n)
			}
			// The apply mutex was released and the plan is still usable.
			f.panicOn("")
			if v := f.apply(alice, pl.PlanID, core.ApplyOptions{}); v.State != core.StatePending {
				t.Errorf("apply after the panic: state %s, want pending", v.State)
			}
		})
	}
}

func TestPanicsAfterTheIntentFailTheChange(t *testing.T) {
	cases := []struct {
		name, call, input string
		ref               core.ActionRef
		opts              core.ApplyOptions
		state, code       string
	}{
		{"revertible apply", "Apply", `"value":"1"`, refSet, core.ApplyOptions{}, core.StateReverted, string(errcode.Internal)},
		{"compensable apply", "Apply", `"value":"1"`, refAdd, core.ApplyOptions{}, core.StateFailed, string(errcode.Internal)},
		{"irreversible verify", "Verify", `"value":"1"`, refRemove, core.ApplyOptions{Confirmation: item}, core.StateFailed, string(errcode.Internal)},
		{"revertible verify", "Verify", `"value":"1"`, refSet, core.ApplyOptions{}, core.StateReverted, string(errcode.Internal)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			pl := f.plan(alice, c.ref, `{"name":"`+item+`",`+c.input+`}`)
			log := f.logged()
			f.panicOn(c.call)
			v := f.apply(alice, pl.PlanID, c.opts)
			if v.State != c.state || v.Error == nil || v.Error.Code != c.code || v.Error.Message != errcode.Internal.Title() {
				t.Errorf("change = %+v (error %+v), want %s with %s and only its title", v, v.Error, c.state, c.code)
			}
			wantPanicLogged(t, log, c.call)
			if n := f.count("SELECT count(*) FROM core_locks"); n != 0 {
				t.Errorf("locks = %d, want 0", n)
			}
		})
	}
}

func TestPanickingRevertFailsTheChange(t *testing.T) {
	t.Run("after a failed apply", func(t *testing.T) {
		f := newFixture(t)
		pl := f.plan(alice, refSet, `{"name":"`+item+`","value":"1","failApply":true}`)
		log := f.logged()
		f.panicOn("Revert")
		v := f.apply(alice, pl.PlanID, core.ApplyOptions{})
		if v.State != core.StateFailed || v.Error == nil || v.Error.Code != string(errcode.ChangeRevertFailed) {
			t.Errorf("change = %+v (error %+v), want failed with change.revert_failed", v, v.Error)
		}
		wantPanicLogged(t, log, "Revert")
	})
	t.Run("by the timer", func(t *testing.T) {
		f := newFixture(t)
		log := f.logged()
		v := f.pending(`{"name":"` + item + `","value":"1"}`)
		f.panicOn("Revert")
		f.clock.Advance(DefaultConfirmTimeout)
		if s := f.state(v.ID); s != core.StateFailed {
			t.Errorf("state %s, want failed", s)
		}
		wantPanicLogged(t, log, "Revert")
	})
}

func TestRecoveryReportsAPanickingVerify(t *testing.T) {
	f := newFixture(t)
	_, id := f.crash(refAdd, `{"name":"`+item+`","value":"1","crashApply":true}`, core.ApplyOptions{})
	f.restart()
	log := f.logged()
	f.panicOn("Verify")
	f.recoverAll()
	v, err := f.eng.Change(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if v.State != core.StateInterrupted || v.Error == nil ||
		!strings.HasSuffix(v.Error.Message, "verify failed: "+errcode.Internal.Title()) {
		t.Errorf("change = %+v (error %+v), want interrupted reporting the failed verify", v, v.Error)
	}
	wantPanicLogged(t, log, "Verify")
}

func TestProtect(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	boom := errors.New("boom")
	if err := Protect(log, "demo.item.get/v1", "Read", func() error { return boom }); !errors.Is(err, boom) {
		t.Errorf("Protect passed %v through, want boom", err)
	}
	var items []string
	err := Protect(log, "demo.status", "Resource", func() error {
		_ = items[len(item)] // a runtime error: index out of range
		return nil
	})
	if err == nil || err.Error() != "Resource of demo.status panicked" {
		t.Errorf("Protect = %v, want the panic as an error", err)
	}
	if text := buf.String(); !strings.Contains(text, "panic=\"runtime error: index out of range [7] with length 0\"") ||
		!strings.Contains(text, "handler=demo.status") {
		t.Errorf("log does not show the runtime error:\n%s", text)
	}
}
