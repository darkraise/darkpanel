package update

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
)

// swappedUpdate leaves e as a runner leaves a host right after the swap of
// a from -> to update: to installed, from kept as darkpanel.prev and
// darkpanel.recovery, the record swapped and the reservation held. It
// returns the record.
func swappedUpdate(t *testing.T, e *updaterEnv, tx, from, to string) Record {
	t.Helper()
	p := e.p
	writeBinaryFile(t, p.Binary(), binaryOf(to))
	writeBinaryFile(t, p.PrevBinary(), binaryOf(from))
	if err := os.Link(p.PrevBinary(), p.RecoveryBinary()); err != nil {
		t.Fatal(err)
	}
	deadline := e.clock.Now().Add(HealthDeadline)
	r := testRecord(tx, tx, 0)
	r.FromVersion, r.ToVersion, r.TargetVersion = from, to, to
	r.Steps = []Release{{Version: to, Channel: core.ChannelStable}}
	r.State, r.Deadline = core.UpdateSwapped, &deadline
	r.PrevSHA256, r.NewSHA256 = sumOf(binaryOf(from)), sumOf(binaryOf(to))
	if err := WriteRecord(p, r); err != nil {
		t.Fatal(err)
	}
	if err := lifecycle.Reserve(context.Background(), e.db, lifecycle.ReserveAll, lifecycle.ReservationOwner(tx)); err != nil {
		t.Fatal(err)
	}
	return r
}

// committedUpdate leaves e as a finished from -> to update leaves it: to
// installed, from kept as darkpanel.prev and the record committed.
func committedUpdate(t *testing.T, e *updaterEnv, tx, from, to string) Record {
	t.Helper()
	writeBinaryFile(t, e.p.Binary(), binaryOf(to))
	writeBinaryFile(t, e.p.PrevBinary(), binaryOf(from))
	r := testRecord(tx, tx, 0)
	r.FromVersion, r.ToVersion, r.TargetVersion = from, to, to
	r.Steps = []Release{{Version: to, Channel: core.ChannelStable}}
	r.State = core.UpdateCommitted
	r.PrevSHA256, r.NewSHA256 = sumOf(binaryOf(from)), sumOf(binaryOf(to))
	if err := WriteRecord(e.p, r); err != nil {
		t.Fatal(err)
	}
	return r
}

// wantRolledBack fails unless from is installed again, to is set aside as
// darkpanel.failed, nothing else is left in BinaryDir and record tx is
// rolled-back with no reservation.
func wantRolledBack(t *testing.T, e *updaterEnv, tx, from, to string) {
	t.Helper()
	p := e.p
	got := map[string]string{
		"darkpanel": binaryContent(t, p.Binary()), "darkpanel.failed": binaryContent(t, p.FailedBinary()),
		"darkpanel.prev": binaryContent(t, p.PrevBinary()), "darkpanel.recovery": binaryContent(t, p.RecoveryBinary()),
		"darkpanel.new": binaryContent(t, p.NewBinary(tx)),
	}
	want := map[string]string{
		"darkpanel": binaryOf(from), "darkpanel.failed": binaryOf(to),
		"darkpanel.prev": "<missing>", "darkpanel.recovery": "<missing>", "darkpanel.new": "<missing>",
	}
	for name, w := range want {
		if got[name] != w {
			t.Errorf("%s = %q, want %q", name, got[name], w)
		}
	}
	if r := mustRecord(t, p, tx); r.State != core.UpdateRolledBack {
		t.Errorf("record state = %s, want rolled-back", r.State)
	}
	wantNoLocks(t, e.db)
}

func TestRollBackSwapped(t *testing.T) {
	ctx := context.Background()
	for _, controller := range []bool{false, true} {
		e := newUpdaterEnv(t, "1.2.0")
		if controller {
			e.sd.setState(ControllerUnit, "active")
		}
		swappedUpdate(t, e, "01TX", "1.2.0", "1.3.0")
		e.sd.setState(AgentUnit, "failed")
		if err := e.updater(t, "1.2.0").RollBack(ctx, "01TX", false); err != nil {
			t.Fatal(err)
		}
		wantRolledBack(t, e, "01TX", "1.2.0", "1.3.0")
		r := mustRecord(t, e.p, "01TX")
		if r.Error == nil || r.Error.Code != string(errcode.UpdateInterrupted) {
			t.Errorf("record error = %+v", r.Error)
		}
		if stops := e.sd.unitsOf("Stop"); !slices.Contains(stops, DeadlineTimer("01TX")) {
			t.Errorf("stopped units = %v, want the deadline timer", stops)
		}
		want := []string{AgentUnit}
		if controller {
			want = []string{ControllerUnit, AgentUnit}
		}
		if got := e.sd.unitsOf("Restart"); !slices.Equal(got, want) {
			t.Errorf("restarted = %v, want %v", got, want)
		}
		if got := e.sd.unitsOf("ResetFailed"); !slices.Equal(got, want) {
			t.Errorf("reset = %v, want %v before the restarts", got, want)
		}
	}
}

func TestRollBackAtBootRestartsNothing(t *testing.T) {
	e := newUpdaterEnv(t, "1.2.0")
	swappedUpdate(t, e, "01TX", "1.2.0", "1.3.0")
	if err := e.updater(t, "1.2.0").RollBack(context.Background(), "01TX", true); err != nil {
		t.Fatal(err)
	}
	wantRolledBack(t, e, "01TX", "1.2.0", "1.3.0")
	if got := e.sd.unitsOf("Restart"); len(got) != 0 {
		t.Errorf("restarted %v at boot", got)
	}
}

func TestRollBackResumesAfterEachStep(t *testing.T) {
	tests := []struct {
		name  string
		crash func(t *testing.T, e *updaterEnv)
	}{
		{"claimed, nothing renamed", func(*testing.T, *updaterEnv) {}},
		{"darkpanel set aside", func(t *testing.T, e *updaterEnv) {
			if err := os.Rename(e.p.Binary(), e.p.FailedBinary()); err != nil {
				t.Fatal(err)
			}
		}},
		{"both renamed", func(t *testing.T, e *updaterEnv) {
			if err := os.Rename(e.p.Binary(), e.p.FailedBinary()); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(e.p.PrevBinary(), e.p.Binary()); err != nil {
				t.Fatal(err)
			}
		}},
		{"both renamed, recovery deleted", func(t *testing.T, e *updaterEnv) {
			if err := os.Rename(e.p.Binary(), e.p.FailedBinary()); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(e.p.PrevBinary(), e.p.Binary()); err != nil {
				t.Fatal(err)
			}
			_ = os.Remove(e.p.RecoveryBinary())
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newUpdaterEnv(t, "1.2.0")
			r := swappedUpdate(t, e, "01TX", "1.2.0", "1.3.0")
			r.State = core.UpdateRollingBack
			if err := WriteRecord(e.p, r); err != nil {
				t.Fatal(err)
			}
			tt.crash(t, e)
			if err := e.updater(t, "1.2.0").RollBack(context.Background(), "01TX", true); err != nil {
				t.Fatal(err)
			}
			wantRolledBack(t, e, "01TX", "1.2.0", "1.3.0")
		})
	}
}

func TestRollBackBeforeTheSwap(t *testing.T) {
	// The runner recorded swapped and crashed before renaming the staged
	// binary over darkpanel.
	e := newUpdaterEnv(t, "1.2.0")
	swappedUpdate(t, e, "01TX", "1.2.0", "1.3.0")
	writeBinaryFile(t, e.p.Binary(), binaryOf("1.2.0"))
	writeBinaryFile(t, e.p.NewBinary("01TX"), binaryOf("1.3.0"))
	if err := e.updater(t, "1.2.0").RollBack(context.Background(), "01TX", false); err != nil {
		t.Fatal(err)
	}
	if got := binaryContent(t, e.p.Binary()); got != binaryOf("1.2.0") {
		t.Errorf("darkpanel = %q", got)
	}
	if got := binaryContent(t, e.p.NewBinary("01TX")); got != "<missing>" {
		t.Errorf("staged binary = %q, want it deleted", got)
	}
	if r := mustRecord(t, e.p, "01TX"); r.State != core.UpdateRolledBack {
		t.Errorf("state = %s", r.State)
	}
	wantNoLocks(t, e.db)
}

func TestRollBackPrevMismatch(t *testing.T) {
	e := newUpdaterEnv(t, "1.2.0")
	swappedUpdate(t, e, "01TX", "1.2.0", "1.3.0")
	// darkpanel.prev replaced by another file, not rewritten in place, so the
	// darkpanel.recovery link still holds the binary prevSha256 names.
	if err := os.Remove(e.p.PrevBinary()); err != nil {
		t.Fatal(err)
	}
	writeBinaryFile(t, e.p.PrevBinary(), "something else")
	err := e.updater(t, "1.2.0").RollBack(context.Background(), "01TX", false)
	wantCode(t, err, errcode.UpdatePrevMismatch)
	r := mustRecord(t, e.p, "01TX")
	if r.State != core.UpdateFailed || r.Error == nil || r.Error.Code != string(errcode.UpdatePrevMismatch) {
		t.Errorf("record = %s %+v", r.State, r.Error)
	}
	if got := binaryContent(t, e.p.Binary()); got != binaryOf("1.3.0") {
		t.Errorf("darkpanel = %q, want it left alone", got)
	}
	if got := binaryContent(t, e.p.PrevBinary()); got != "something else" {
		t.Errorf("darkpanel.prev = %q, want it left alone", got)
	}
	if got := binaryContent(t, e.p.RecoveryBinary()); got != binaryOf("1.2.0") {
		t.Errorf("darkpanel.recovery = %q, want the binary prevSha256 names kept", got)
	}
	wantNoLocks(t, e.db)
	if got := e.sd.unitsOf("Restart"); len(got) != 0 {
		t.Errorf("restarted %v after a mismatch", got)
	}
}

func TestRollBackIgnoresOtherStates(t *testing.T) {
	for _, state := range []string{core.UpdateCommitted, core.UpdateRolledBack, core.UpdateFailed, core.UpdateStaged} {
		e := newUpdaterEnv(t, "1.2.0")
		r := swappedUpdate(t, e, "01TX", "1.2.0", "1.3.0")
		r.State = state
		if err := WriteRecord(e.p, r); err != nil {
			t.Fatal(err)
		}
		if err := e.updater(t, "1.2.0").RollBack(context.Background(), "01TX", false); err != nil {
			t.Errorf("%s: %v", state, err)
		}
		if got := binaryContent(t, e.p.Binary()); got != binaryOf("1.3.0") {
			t.Errorf("%s: darkpanel = %q, want it untouched", state, got)
		}
		if got := mustRecord(t, e.p, "01TX").State; got != state {
			t.Errorf("state = %s, want %s", got, state)
		}
	}
}

func TestRollbacksRace(t *testing.T) {
	e := newUpdaterEnv(t, "1.2.0")
	swappedUpdate(t, e, "01TX", "1.2.0", "1.3.0")
	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range errs {
		wg.Go(func() { errs[i] = e.updater(t, "1.2.0").RollBack(context.Background(), "01TX", false) })
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("rollback %d: %v", i, err)
		}
	}
	wantRolledBack(t, e, "01TX", "1.2.0", "1.3.0")
	if got := e.sd.unitsOf("Restart"); !slices.Equal(got, []string{AgentUnit}) {
		t.Errorf("restarts = %v, want one claimant to restart the agent once", got)
	}
}

func TestRecoveryLockStopsAFrozenRunner(t *testing.T) {
	e := newUpdaterEnv(t, "1.2.0")
	swappedUpdate(t, e, "01TX", "1.2.0", "1.3.0")
	frozen, err := AcquireLock(context.Background(), e.p.UpdateLock(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	// Killing the runner ends its process and with it the flock it holds.
	e.sd.onKill = func(unit string) {
		if unit == RunnerUnit("01TX") {
			_ = frozen.Release()
		}
	}
	u := e.updater(t, "1.2.0")
	u.lockTimeout = 100 * time.Millisecond
	if err := u.RollBack(context.Background(), "01TX", false); err != nil {
		t.Fatal(err)
	}
	wantRolledBack(t, e, "01TX", "1.2.0", "1.3.0")
	kills := e.sd.callsOf("Kill")
	if len(kills) != 1 || kills[0].Unit != RunnerUnit("01TX") || kills[0].Signal != syscall.SIGKILL {
		t.Errorf("kills = %+v", kills)
	}
	if stops := e.sd.unitsOf("Stop"); len(stops) == 0 || stops[0] != RunnerUnit("01TX") {
		t.Errorf("stops = %v, want the runner unit first", stops)
	}
}

func TestStopTimerWarnsWhenTheTimerStaysLoaded(t *testing.T) {
	for _, tc := range []struct {
		name         string
		loaded, warn bool
	}{
		{"timer still loaded", true, true},
		{"timer never started", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newUpdaterEnv(t, "1.2.0")
			var buf bytes.Buffer
			cfg := e.config("1.2.0")
			cfg.Log = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
			u, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if tc.loaded {
				e.sd.setState(DeadlineTimer("01TX"), "active")
			}
			e.sd.failCall("Stop", DeadlineTimer("01TX"), errors.New("no bus"))
			u.stopTimer(context.Background(), "01TX")
			if got := strings.Contains(buf.String(), "level=WARN"); got != tc.warn {
				t.Errorf("log = %q, want a warning: %v", buf.String(), tc.warn)
			}
		})
	}
}

func TestStartRollback(t *testing.T) {
	ctx := context.Background()
	e := newUpdaterEnv(t, "1.2.0")
	committedUpdate(t, e, "01UP", "1.2.0", "1.3.0")
	u := e.updater(t, "1.3.0")
	if err := u.CheckRollback(ctx, core.UpdateRollbackInput{}); err != nil {
		t.Fatal(err)
	}
	tr, err := u.StartRollback(ctx, "01CHANGE", core.UpdateRollbackInput{})
	if err != nil {
		t.Fatal(err)
	}
	if tr.Kind != core.UpdateKindRollback || tr.State != core.UpdatePending || tr.FromVersion != "1.3.0" ||
		tr.ToVersion != "1.2.0" || tr.TargetVersion != "1.2.0" || tr.OperationID != tr.ID {
		t.Errorf("transaction = %+v", tr)
	}
	r := mustRecord(t, e.p, tr.ID)
	if r.ChangeID != "01CHANGE" || r.PrevSHA256 != sumOf(binaryOf("1.2.0")) || r.NewSHA256 != sumOf(binaryOf("1.3.0")) {
		t.Errorf("record = %+v", r)
	}
	starts := e.sd.callsOf("StartTransientService")
	want := []string{e.p.Binary(), "update", "rollback", "--direct", "--transaction", tr.ID}
	if len(starts) != 1 || starts[0].Unit != RollbackRunnerUnit(tr.ID) || !slices.Equal(starts[0].Service.ExecStart, want) {
		t.Fatalf("transient units = %+v", starts)
	}
	if got := lockRows(t, e.db); !slices.Equal(got, []string{"* " + lifecycle.ReservationOwner(tr.ID)}) {
		t.Errorf("core_locks = %v", got)
	}
}

func TestRollbackGuards(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, e *updaterEnv)
		force bool
		code  errcode.Code
	}{
		{"no update", func(*testing.T, *updaterEnv) {}, false, errcode.UpdateNothingToRollBack},
		{"no previous binary", func(t *testing.T, e *updaterEnv) {
			committedUpdate(t, e, "01UP", "1.2.0", "1.3.0")
			_ = os.Remove(e.p.PrevBinary())
		}, false, errcode.UpdateNothingToRollBack},
		{"previous binary replaced", func(t *testing.T, e *updaterEnv) {
			committedUpdate(t, e, "01UP", "1.2.0", "1.3.0")
			writeBinaryFile(t, e.p.PrevBinary(), "other")
		}, false, errcode.UpdatePrevMismatch},
		{"update not finished", func(t *testing.T, e *updaterEnv) {
			swappedUpdate(t, e, "01UP", "1.2.0", "1.3.0")
		}, false, errcode.UpdateInProgress},
		{"update rolled back", func(t *testing.T, e *updaterEnv) {
			r := committedUpdate(t, e, "01UP", "1.2.0", "1.3.0")
			r.State = core.UpdateRolledBack
			if err := WriteRecord(e.p, r); err != nil {
				t.Fatal(err)
			}
		}, false, errcode.UpdateNothingToRollBack},
		{"update failed", func(t *testing.T, e *updaterEnv) {
			r := committedUpdate(t, e, "01UP", "1.2.0", "1.3.0")
			r.State = core.UpdateFailed
			if err := WriteRecord(e.p, r); err != nil {
				t.Fatal(err)
			}
		}, false, errcode.UpdateNothingToRollBack},
		{"newer agents", func(t *testing.T, e *updaterEnv) {
			committedUpdate(t, e, "01UP", "1.2.0", "1.3.0")
			e.newer = []string{"01HOSTB"}
		}, false, errcode.UpdateDowngradeRefused},
		{"newer agents with force", func(t *testing.T, e *updaterEnv) {
			committedUpdate(t, e, "01UP", "1.2.0", "1.3.0")
			e.newer = []string{"01HOSTB"}
		}, true, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newUpdaterEnv(t, "1.2.0")
			tt.setup(t, e)
			err := e.updater(t, "1.3.0").CheckRollback(context.Background(), core.UpdateRollbackInput{Force: tt.force})
			if tt.code == "" {
				if err != nil {
					t.Fatalf("CheckRollback: %v", err)
				}
				return
			}
			wantCode(t, err, tt.code)
		})
	}
}

func TestRunRollback(t *testing.T) {
	ctx := context.Background()
	e := newUpdaterEnv(t, "1.2.0")
	committedUpdate(t, e, "01UP", "1.2.0", "1.3.0")
	tr, err := e.updater(t, "1.3.0").StartRollback(ctx, "01CHANGE", core.UpdateRollbackInput{})
	if err != nil {
		t.Fatal(err)
	}
	// The transient unit runs the rollback in a process of its own.
	if err := e.updater(t, "1.3.0").RunRollback(ctx, tr.ID); err != nil {
		t.Fatal(err)
	}
	wantRolledBack(t, e, tr.ID, "1.2.0", "1.3.0")
	if r := mustRecord(t, e.p, tr.ID); r.Error != nil {
		t.Errorf("record error = %+v, want none", r.Error)
	}
	if got := e.sd.unitsOf("Restart"); !slices.Equal(got, []string{AgentUnit}) {
		t.Errorf("restarts = %v", got)
	}
	// A second run of the same record has nothing left to do.
	if err := e.updater(t, "1.2.0").RunRollback(ctx, tr.ID); err != nil {
		t.Errorf("second RunRollback: %v", err)
	}
}

func TestRunRollbackPrevChanged(t *testing.T) {
	ctx := context.Background()
	e := newUpdaterEnv(t, "1.2.0")
	committedUpdate(t, e, "01UP", "1.2.0", "1.3.0")
	tr, err := e.updater(t, "1.3.0").StartRollback(ctx, "01CHANGE", core.UpdateRollbackInput{})
	if err != nil {
		t.Fatal(err)
	}
	writeBinaryFile(t, e.p.PrevBinary(), "other")
	err = e.updater(t, "1.3.0").RunRollback(ctx, tr.ID)
	wantCode(t, err, errcode.UpdatePrevMismatch)
	if r := mustRecord(t, e.p, tr.ID); r.State != core.UpdateFailed {
		t.Errorf("state = %s, want failed", r.State)
	}
	if got := binaryContent(t, e.p.Binary()); got != binaryOf("1.3.0") {
		t.Errorf("darkpanel = %q, want it untouched", got)
	}
	wantNoLocks(t, e.db)
}

func TestRunRollbackResumesRollingBack(t *testing.T) {
	ctx := context.Background()
	e := newUpdaterEnv(t, "1.2.0")
	committedUpdate(t, e, "01UP", "1.2.0", "1.3.0")
	tr, err := e.updater(t, "1.3.0").StartRollback(ctx, "", core.UpdateRollbackInput{})
	if err != nil {
		t.Fatal(err)
	}
	r := mustRecord(t, e.p, tr.ID)
	r.State = core.UpdateRollingBack
	if err := WriteRecord(e.p, r); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(e.p.Binary(), e.p.FailedBinary()); err != nil {
		t.Fatal(err)
	}
	if err := e.updater(t, "1.3.0").RunRollback(ctx, tr.ID); err != nil {
		t.Fatal(err)
	}
	wantRolledBack(t, e, tr.ID, "1.2.0", "1.3.0")
}

func TestDirectRollback(t *testing.T) {
	ctx := context.Background()
	e := newUpdaterEnv(t, "1.2.0")
	committedUpdate(t, e, "01UP", "1.2.0", "1.3.0")
	tr, err := e.updater(t, "1.3.0").DirectRollback(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if tr.State != core.UpdateRolledBack || tr.Kind != core.UpdateKindRollback || tr.ToVersion != "1.2.0" {
		t.Errorf("transaction = %+v", tr)
	}
	wantRolledBack(t, e, tr.ID, "1.2.0", "1.3.0")
	if r := mustRecord(t, e.p, tr.ID); r.ChangeID != "" {
		t.Errorf("change id = %q, want none", r.ChangeID)
	}
	_, err = e.updater(t, "1.2.0").DirectRollback(ctx, false)
	wantCode(t, err, errcode.UpdateNothingToRollBack)
}
