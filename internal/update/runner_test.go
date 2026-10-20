package update

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// setChangeState moves change id to state.
func setChangeState(t *testing.T, db *store.DB, id, state string) {
	t.Helper()
	if _, err := db.Writer().Exec("UPDATE core_changes SET state = ? WHERE id = ?", state, id); err != nil {
		t.Fatal(err)
	}
}

// driveClock runs fn and advances clock by step every millisecond of real
// time until fn returns, for code that waits on the clock. When first is not
// nil it runs once fn first waits on the clock, before any advance.
func driveClock(clock *coretest.FakeClock, step time.Duration, first func(), fn func() error) error {
	done := make(chan error, 1)
	go func() { done <- fn() }()
	if first != nil {
		for clock.Pending() == 0 {
			select {
			case err := <-done:
				return err
			case <-time.After(time.Millisecond):
			}
		}
		first()
	}
	for {
		select {
		case err := <-done:
			return err
		case <-time.After(time.Millisecond):
			clock.Advance(step)
		}
	}
}

// startUpdate starts an update to version from a completed change, as the
// apply action does, and returns its first transaction id.
func startUpdate(t *testing.T, e *updaterEnv, installed, version string) string {
	t.Helper()
	insertChange(t, e.db, "01CHANGE", core.ClassCompensable, core.StateCompleted)
	tr, err := e.updater(t, installed).StartUpdate(context.Background(), "01CHANGE",
		core.UpdateApplyInput{Version: version, Channel: core.ChannelStable})
	if err != nil {
		t.Fatal(err)
	}
	return tr.ID
}

func TestRunUpdate(t *testing.T) {
	for _, controller := range []bool{false, true} {
		e := newUpdaterEnv(t, "1.2.0")
		if controller {
			e.sd.setState(ControllerUnit, "active")
		}
		tx := startUpdate(t, e, "1.2.0", "1.3.1")
		start := e.clock.Now()
		if err := e.updater(t, "1.2.0").Run(context.Background(), tx); err != nil {
			t.Fatal(err)
		}
		r := mustRecord(t, e.p, tx)
		if r.State != core.UpdateCommitted || r.Error != nil {
			t.Fatalf("record = %s %+v", r.State, r.Error)
		}
		if r.NewSHA256 != sumOf(binaryOf("1.3.1")) || r.PrevSHA256 != sumOf(binaryOf("1.2.0")) ||
			r.Deadline == nil || !r.Deadline.Equal(start.Add(HealthDeadline)) {
			t.Errorf("record = %+v", r)
		}
		p := e.p
		for path, want := range map[string]string{
			p.Binary(): binaryOf("1.3.1"), p.PrevBinary(): binaryOf("1.2.0"),
			p.RecoveryBinary(): "<missing>", p.NewBinary(tx): "<missing>",
		} {
			if got := binaryContent(t, path); got != want {
				t.Errorf("%s = %q, want %q", path, got, want)
			}
		}
		if _, err := os.Stat(e.updater(t, "1.2.0").downloadDir(tx)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("download directory left behind: %v", err)
		}
		if !slices.Equal(e.selfTests, []string{p.NewBinary(tx)}) {
			t.Errorf("self-tests = %v", e.selfTests)
		}
		timers := e.sd.callsOf("StartTransientTimer")
		if len(timers) != 1 || timers[0].Unit != DeadlineTimer(tx) {
			t.Fatalf("timers = %+v", timers)
		}
		tm := timers[0].Timer
		if tm.OnActive != HealthDeadline || tm.Accuracy != time.Second ||
			!slices.Equal(tm.Service.ExecStart, []string{p.RecoveryBinary(), "update", "recover", "--deadline", tx}) {
			t.Errorf("timer = %+v", tm)
		}
		if !slices.Contains(e.sd.unitsOf("Stop"), DeadlineTimer(tx)) {
			t.Error("the deadline timer was not stopped after the commit")
		}
		want := []string{AgentUnit}
		if controller {
			want = []string{ControllerUnit, AgentUnit}
		}
		if got := e.sd.unitsOf("Restart"); !slices.Equal(got, want) {
			t.Errorf("restarts = %v, want %v", got, want)
		}
		wantNoLocks(t, e.db)
	}
}

func TestRunSteps(t *testing.T) {
	ctx := context.Background()
	e := newUpdaterEnv(t, "1.2.0")
	tx := startUpdate(t, e, "1.2.0", "1.5.0")
	owner := "* " + lifecycle.ReservationOwner(tx)
	installed := "1.2.0"
	for _, want := range []string{"1.3.1", "1.4.0", "1.5.0"} {
		if err := e.updater(t, installed).Run(ctx, tx); err != nil {
			t.Fatalf("step to %s: %v", want, err)
		}
		r := mustRecord(t, e.p, tx)
		if r.State != core.UpdateCommitted || r.ToVersion != want || r.OperationID != lockOwnerOp(owner) {
			t.Fatalf("step record = %+v", r)
		}
		if got := binaryContent(t, e.p.Binary()); got != binaryOf(want) {
			t.Fatalf("darkpanel = %q after the step to %s", got, want)
		}
		installed = want
		if want == "1.5.0" {
			break
		}
		// The reservation is held between steps, and the next step's runner
		// was started with the binary just installed.
		if got := lockRows(t, e.db); !slices.Equal(got, []string{owner}) {
			t.Fatalf("core_locks between steps = %v", got)
		}
		starts := e.sd.callsOf("StartTransientService")
		next := starts[len(starts)-1]
		all, err := ListRecords(e.p)
		if err != nil {
			t.Fatal(err)
		}
		child := all[len(all)-1]
		if child.ParentID == nil || *child.ParentID != tx || child.State != core.UpdatePending ||
			child.FromVersion != want || child.ChangeID != "" || child.TargetVersion != "1.5.0" {
			t.Fatalf("next step record = %+v", child)
		}
		if next.Unit != RunnerUnit(child.ID) || !slices.Equal(next.Service.ExecStart, []string{e.p.Binary(), "update", "run", child.ID}) {
			t.Fatalf("next runner = %+v", next)
		}
		tx = child.ID
	}
	wantNoLocks(t, e.db)
	if got := binaryContent(t, e.p.PrevBinary()); got != binaryOf("1.4.0") {
		t.Errorf("darkpanel.prev = %q", got)
	}
	if got := binaryContent(t, e.p.RecoveryBinary()); got != "<missing>" {
		t.Errorf("darkpanel.recovery = %q after the last step", got)
	}
	st, err := e.updater(t, "1.5.0").Status(ctx, core.UpdateStatusInput{ChangeID: "01CHANGE"})
	if err != nil || len(st.Transactions) != 3 {
		t.Errorf("status = %+v, %v", st, err)
	}
}

// lockOwnerOp is the operation id of a "* op:<id>" lock row.
func lockOwnerOp(row string) string { return strings.TrimPrefix(row, "* op:") }

func TestRunWaitsForItsChange(t *testing.T) {
	tests := []struct {
		name  string
		state string
		later func(t *testing.T, e *updaterEnv)
		ok    bool
	}{
		{"completed", core.StateCompleted, nil, true},
		{"completes after the runner started", core.StateApplying, func(t *testing.T, e *updaterEnv) {
			setChangeState(t, e.db, "01CHANGE", core.StateCompleted)
		}, true},
		{"failed", core.StateFailed, nil, false},
		{"interrupted", core.StateApplying, func(t *testing.T, e *updaterEnv) {
			setChangeState(t, e.db, "01CHANGE", core.StateInterrupted)
		}, false},
		{"never completes", core.StateApplying, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			e := newUpdaterEnv(t, "1.2.0")
			insertChange(t, e.db, "01CHANGE", core.ClassCompensable, core.StateApplying)
			tr, err := e.updater(t, "1.2.0").StartUpdate(ctx, "01CHANGE",
				core.UpdateApplyInput{Version: "1.3.1", Channel: core.ChannelStable})
			if err != nil {
				t.Fatal(err)
			}
			setChangeState(t, e.db, "01CHANGE", tt.state)
			u := e.updater(t, "1.2.0")
			var first func()
			if tt.later != nil {
				first = func() { tt.later(t, e) }
			}
			err = driveClock(e.clock, changePoll, first, func() error { return u.Run(ctx, tr.ID) })
			r := mustRecord(t, e.p, tr.ID)
			if tt.ok {
				if err != nil || r.State != core.UpdateCommitted {
					t.Fatalf("Run = %v, state %s", err, r.State)
				}
				return
			}
			wantCode(t, err, errcode.UpdateInterrupted)
			if r.State != core.UpdateFailed || r.Error == nil || r.Error.Code != string(errcode.UpdateInterrupted) {
				t.Errorf("record = %s %+v", r.State, r.Error)
			}
			if got := binaryContent(t, e.p.Binary()); got != binaryOf("1.2.0") {
				t.Errorf("darkpanel = %q", got)
			}
			if len(e.selfTests) != 0 {
				t.Error("the release was staged although the change did not complete")
			}
			wantNoLocks(t, e.db)
		})
	}
}

func TestRunWithoutAChangeDoesNotWait(t *testing.T) {
	e := newUpdaterEnv(t, "1.2.0")
	tx := startUpdate(t, e, "1.2.0", "1.3.1")
	r := mustRecord(t, e.p, tx)
	r.ChangeID = ""
	if err := WriteRecord(e.p, r); err != nil {
		t.Fatal(err)
	}
	if err := e.updater(t, "1.2.0").Run(context.Background(), tx); err != nil {
		t.Fatal(err)
	}
	if got := mustRecord(t, e.p, tx).State; got != core.UpdateCommitted {
		t.Errorf("state = %s", got)
	}
}

func TestRunHealthFailureRollsBack(t *testing.T) {
	tests := []struct {
		name   string
		setup  func(e *updaterEnv, u *Updater)
		detail string
	}{
		{"agent never reports the new version", func(e *updaterEnv, _ *Updater) {
			e.agentHello = func(string) (core.AgentHello, error) { return core.AgentHello{BinaryVersion: "1.2.0"}, nil }
		}, "did not report"},
		{"agent not connected to the controller", func(e *updaterEnv, _ *Updater) {
			disconnected := false
			e.agentHello = func(v string) (core.AgentHello, error) {
				return core.AgentHello{BinaryVersion: v, ControllerConnected: &disconnected}, nil
			}
		}, "did not report"},
		{"controller degraded", func(e *updaterEnv, u *Updater) {
			e.sd.setState(ControllerUnit, "active")
			u.cfg.Probes.Controller = func(context.Context) (ControllerHealth, error) {
				return ControllerHealth{Status: "degraded", Version: installedVersion(e.p.Binary())}, nil
			}
		}, "degraded"},
		{"restart refused", func(e *updaterEnv, _ *Updater) {
			e.sd.failCall("Restart", AgentUnit, errors.New("unit masked"))
		}, "unit masked"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newUpdaterEnv(t, "1.2.0")
			tx := startUpdate(t, e, "1.2.0", "1.3.1")
			u := e.updater(t, "1.2.0")
			tt.setup(e, u)
			err := driveClock(e.clock, HealthInterval, nil, func() error { return u.Run(context.Background(), tx) })
			if err == nil {
				t.Fatal("Run succeeded although the health check failed")
			}
			wantRolledBack(t, e, tx, "1.2.0", "1.3.1")
			r := mustRecord(t, e.p, tx)
			if r.Error == nil || r.Error.Code != string(errcode.UpdateInterrupted) || !strings.Contains(r.Error.Message, tt.detail) {
				t.Errorf("record error = %+v, want %q", r.Error, tt.detail)
			}
		})
	}
}

func TestRunPreSwapFailures(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, e *updaterEnv)
		code  errcode.Code
	}{
		{"signed by an untrusted key", func(t *testing.T, e *updaterEnv) {
			e.rel.publish(t, "1.3.1", core.ChannelStable, "amd64", []byte(binaryOf("1.3.1")), newTestSigner(t))
			e.rel.remove("v1.3.1/checksums.txt." + e.signer.Key.ID + ".minisig")
		}, errcode.UpdateSignatureInvalid},
		{"tarball removed", func(_ *testing.T, e *updaterEnv) {
			e.rel.remove("v1.3.1/" + TarballName("1.3.1", "amd64"))
		}, errcode.UpdateNotFound},
		{"self-test fails", func(_ *testing.T, e *updaterEnv) {
			e.selfErr = errcode.New(errcode.UpdateInterrupted, "the new binary's self-test failed")
		}, errcode.UpdateInterrupted},
		{"deadline timer refused", func(_ *testing.T, e *updaterEnv) {
			e.sd.failCall("StartTransientTimer", "", errors.New("no bus"))
		}, errcode.Internal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newUpdaterEnv(t, "1.2.0")
			tx := startUpdate(t, e, "1.2.0", "1.3.1")
			tt.setup(t, e)
			err := e.updater(t, "1.2.0").Run(context.Background(), tx)
			if err == nil {
				t.Fatal("Run succeeded")
			}
			r := mustRecord(t, e.p, tx)
			if r.State != core.UpdateFailed || r.Error == nil || r.Error.Code != string(tt.code) {
				t.Errorf("record = %s %+v, want failed with %s", r.State, r.Error, tt.code)
			}
			p := e.p
			for path, want := range map[string]string{
				p.Binary(): binaryOf("1.2.0"), p.NewBinary(tx): "<missing>", p.RecoveryBinary(): "<missing>",
			} {
				if got := binaryContent(t, path); got != want {
					t.Errorf("%s = %q, want %q", path, got, want)
				}
			}
			if got := e.sd.unitsOf("Restart"); len(got) != 0 {
				t.Errorf("restarted %v before a swap", got)
			}
			wantNoLocks(t, e.db)
		})
	}
}

func TestRunLeavesAMovedRecordAlone(t *testing.T) {
	ctx := context.Background()
	e := newUpdaterEnv(t, "1.2.0")
	tx := startUpdate(t, e, "1.2.0", "1.3.1")
	// The deadline rollback runs while the runner health-checks; the
	// runner must then not commit.
	rolled := false
	e.agentHello = func(string) (core.AgentHello, error) {
		if !rolled {
			rolled = true
			if err := e.updater(t, "1.2.0").RollBack(ctx, tx, false); err != nil {
				t.Error(err)
			}
		}
		return core.AgentHello{BinaryVersion: "1.3.1"}, nil
	}
	if err := e.updater(t, "1.2.0").Run(ctx, tx); err != nil {
		t.Fatal(err)
	}
	wantRolledBack(t, e, tx, "1.2.0", "1.3.1")
	// A record past pending is not run again.
	if err := e.updater(t, "1.2.0").Run(ctx, tx); err != nil {
		t.Errorf("second Run: %v", err)
	}
}

func TestRunNextStepStartFailure(t *testing.T) {
	e := newUpdaterEnv(t, "1.2.0")
	tx := startUpdate(t, e, "1.2.0", "1.4.0")
	e.sd.failCall("StartTransientService", "", errors.New("no bus"))
	if err := e.updater(t, "1.2.0").Run(context.Background(), tx); err == nil {
		t.Fatal("Run succeeded although the next step did not start")
	}
	all, err := ListRecords(e.p)
	if err != nil || len(all) != 2 {
		t.Fatalf("records = %+v, %v", all, err)
	}
	if all[0].State != core.UpdateCommitted || all[1].State != core.UpdateFailed {
		t.Errorf("states = %s, %s; want committed, failed", all[0].State, all[1].State)
	}
	// The host stays on the last committed minor.
	if got := binaryContent(t, e.p.Binary()); got != binaryOf("1.3.1") {
		t.Errorf("darkpanel = %q", got)
	}
	wantNoLocks(t, e.db)
}
