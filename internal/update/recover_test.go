package update

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
)

// crashedRunner leaves e as a runner killed in state leaves it, for an
// update from 1.2.0 to 1.3.1 with the reservation held, and returns the
// record. staged writes darkpanel.new-<tx>, kept keeps the previous binary
// as darkpanel.prev and darkpanel.recovery, renamed swaps.
func crashedRunner(t *testing.T, e *updaterEnv, state string, staged, kept, renamed bool) Record {
	t.Helper()
	p := e.p
	r := testRecord("01TX", "01TX", 0)
	r.ToVersion, r.TargetVersion = "1.3.1", "1.3.1"
	r.Steps = []Release{{Version: "1.3.1", Channel: core.ChannelStable}}
	r.State = state
	if staged {
		writeBinaryFile(t, p.NewBinary(r.ID), binaryOf("1.3.1"))
		r.NewSHA256 = sumOf(binaryOf("1.3.1"))
	}
	if kept {
		writeBinaryFile(t, p.PrevBinary(), binaryOf("1.2.0"))
		if err := os.Link(p.PrevBinary(), p.RecoveryBinary()); err != nil {
			t.Fatal(err)
		}
		r.PrevSHA256 = sumOf(binaryOf("1.2.0"))
	}
	if renamed {
		if err := os.Rename(p.NewBinary(r.ID), p.Binary()); err != nil {
			t.Fatal(err)
		}
	}
	if err := WriteRecord(p, r); err != nil {
		t.Fatal(err)
	}
	if err := lifecycle.Reserve(context.Background(), e.db, lifecycle.ReserveAll, lifecycle.ReservationOwner(r.ID)); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRecoverAfterACrashAtEachRunnerStep(t *testing.T) {
	tests := []struct {
		name                  string
		state                 string
		staged, kept, renamed bool
		download              bool
		want                  string
	}{
		{"pending", core.UpdatePending, false, false, false, false, core.UpdateFailed},
		{"downloading", core.UpdateDownloading, false, false, false, true, core.UpdateFailed},
		{"binary staged", core.UpdateDownloading, true, false, false, false, core.UpdateFailed},
		{"staged", core.UpdateStaged, true, false, false, false, core.UpdateFailed},
		{"previous binary kept", core.UpdateStaged, true, true, false, false, core.UpdateFailed},
		{"swapped, not renamed", core.UpdateSwapped, true, true, false, false, core.UpdateRolledBack},
		{"swapped and renamed", core.UpdateSwapped, true, true, true, false, core.UpdateRolledBack},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newUpdaterEnv(t, "1.2.0")
			u := e.updater(t, "1.2.0")
			r := crashedRunner(t, e, tt.state, tt.staged, tt.kept, tt.renamed)
			if tt.download {
				if err := os.MkdirAll(u.downloadDir(r.ID), 0o700); err != nil {
					t.Fatal(err)
				}
				writeBinaryFile(t, filepath.Join(u.downloadDir(r.ID), "partial"), "half a tarball")
			}
			e.sd.setState(RunnerUnit(r.ID), "failed")
			if err := u.Recover(context.Background(), RecoverFailure, ""); err != nil {
				t.Fatal(err)
			}
			got := mustRecord(t, e.p, r.ID)
			if got.State != tt.want || got.Error == nil || got.Error.Code != string(errcode.UpdateInterrupted) {
				t.Errorf("record = %s %+v, want %s with update.interrupted", got.State, got.Error, tt.want)
			}
			p := e.p
			for path, want := range map[string]string{
				p.Binary(): binaryOf("1.2.0"), p.NewBinary(r.ID): "<missing>", p.RecoveryBinary(): "<missing>",
			} {
				if c := binaryContent(t, path); c != want {
					t.Errorf("%s = %q, want %q", path, c, want)
				}
			}
			if _, err := os.Stat(u.downloadDir(r.ID)); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("download directory left behind: %v", err)
			}
			wantNoLocks(t, e.db)
		})
	}
}

func TestRecoverSparesALiveRunner(t *testing.T) {
	for _, active := range []string{"active", "activating", "deactivating"} {
		e := newUpdaterEnv(t, "1.2.0")
		r := crashedRunner(t, e, core.UpdateDownloading, true, false, false)
		e.sd.setState(RunnerUnit(r.ID), active)
		if err := e.updater(t, "1.2.0").Recover(context.Background(), RecoverStart, ""); err != nil {
			t.Fatal(err)
		}
		if got := mustRecord(t, e.p, r.ID).State; got != core.UpdateDownloading {
			t.Errorf("%s runner: state = %s, want it left downloading", active, got)
		}
		if got := binaryContent(t, e.p.NewBinary(r.ID)); got != binaryOf("1.3.1") {
			t.Errorf("%s runner: staged binary = %q", active, got)
		}
		if got := lockRows(t, e.db); len(got) != 1 {
			t.Errorf("%s runner: core_locks = %v, want the reservation kept", active, got)
		}
	}
}

func TestRecoverStartLeavesASwappedUpdate(t *testing.T) {
	e := newUpdaterEnv(t, "1.2.0")
	swappedUpdate(t, e, "01TX", "1.2.0", "1.3.0")
	if err := e.updater(t, "1.3.0").Recover(context.Background(), RecoverStart, ""); err != nil {
		t.Fatal(err)
	}
	if got := mustRecord(t, e.p, "01TX").State; got != core.UpdateSwapped {
		t.Errorf("state = %s, want swapped: the runner is health-checking", got)
	}
	if got := binaryContent(t, e.p.Binary()); got != binaryOf("1.3.0") {
		t.Errorf("darkpanel = %q", got)
	}
	if got := lockRows(t, e.db); len(got) != 1 {
		t.Errorf("core_locks = %v, want the reservation kept", got)
	}
}

func TestRecoverRollsBack(t *testing.T) {
	tests := []struct {
		mode     RecoverMode
		restarts bool
		message  string
	}{
		{RecoverBoot, false, "host restarted"},
		{RecoverFailure, true, "failed before"},
		{RecoverDeadline, true, "deadline"},
	}
	for _, tt := range tests {
		e := newUpdaterEnv(t, "1.2.0")
		swappedUpdate(t, e, "01TX", "1.2.0", "1.3.0")
		// A crash-looping agent has hit its start limit.
		e.sd.setState(AgentUnit, "failed")
		if err := e.updater(t, "1.2.0").Recover(context.Background(), tt.mode, "01TX"); err != nil {
			t.Fatalf("mode %d: %v", tt.mode, err)
		}
		wantRolledBack(t, e, "01TX", "1.2.0", "1.3.0")
		if r := mustRecord(t, e.p, "01TX"); r.Error == nil || !strings.Contains(r.Error.Message, tt.message) {
			t.Errorf("mode %d: record error = %+v, want %q", tt.mode, r.Error, tt.message)
		}
		restarts := e.sd.unitsOf("Restart")
		if tt.restarts != (len(restarts) > 0) {
			t.Errorf("mode %d: restarts = %v", tt.mode, restarts)
		}
		if tt.restarts {
			e.sd.mu.Lock()
			calls := slices.Clone(e.sd.calls)
			e.sd.mu.Unlock()
			reset := slices.IndexFunc(calls, func(c systemdCall) bool { return c.Method == "ResetFailed" && c.Unit == AgentUnit })
			restart := slices.IndexFunc(calls, func(c systemdCall) bool { return c.Method == "Restart" && c.Unit == AgentUnit })
			if reset < 0 || restart < reset {
				t.Errorf("mode %d: the agent was restarted without its failed state reset first", tt.mode)
			}
		}
	}
}

func TestRecoverBootResumesAfterARename(t *testing.T) {
	e := newUpdaterEnv(t, "1.2.0")
	r := swappedUpdate(t, e, "01TX", "1.2.0", "1.3.0")
	r.State = core.UpdateRollingBack
	if err := WriteRecord(e.p, r); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(e.p.Binary(), e.p.FailedBinary()); err != nil {
		t.Fatal(err)
	}
	if err := e.updater(t, "1.2.0").Recover(context.Background(), RecoverBoot, ""); err != nil {
		t.Fatal(err)
	}
	wantRolledBack(t, e, "01TX", "1.2.0", "1.3.0")
}

func TestRecoverDeadlineTouchesOnlyItsTransaction(t *testing.T) {
	e := newUpdaterEnv(t, "1.2.0")
	swappedUpdate(t, e, "01TX", "1.2.0", "1.3.0")
	if err := e.updater(t, "1.3.0").Recover(context.Background(), RecoverDeadline, "01OTHER"); err != nil {
		t.Fatal(err)
	}
	if got := mustRecord(t, e.p, "01TX").State; got != core.UpdateSwapped {
		t.Errorf("state = %s, want swapped", got)
	}
	if err := e.updater(t, "1.3.0").Recover(context.Background(), RecoverDeadline, ""); err == nil {
		t.Error("RecoverDeadline accepted an empty transaction id")
	}
}

func TestRecoverDeadlineStopsAFrozenRunner(t *testing.T) {
	e := newUpdaterEnv(t, "1.2.0")
	swappedUpdate(t, e, "01TX", "1.2.0", "1.3.0")
	e.sd.setState(RunnerUnit("01TX"), "active")
	frozen, err := AcquireLock(context.Background(), e.p.UpdateLock(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	e.sd.onKill = func(unit string) {
		if unit == RunnerUnit("01TX") {
			_ = frozen.Release()
		}
	}
	u := e.updater(t, "1.2.0")
	u.lockTimeout = 100 * time.Millisecond
	if err := u.Recover(context.Background(), RecoverDeadline, "01TX"); err != nil {
		t.Fatal(err)
	}
	wantRolledBack(t, e, "01TX", "1.2.0", "1.3.0")
	if kills := e.sd.unitsOf("Kill"); !slices.Equal(kills, []string{RunnerUnit("01TX")}) {
		t.Errorf("kills = %v", kills)
	}
}

func TestRecoverStartWaitsForTheLock(t *testing.T) {
	e := newUpdaterEnv(t, "1.2.0")
	swappedUpdate(t, e, "01TX", "1.2.0", "1.3.0")
	held, err := AcquireLock(context.Background(), e.p.UpdateLock(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Release() }()
	u := e.updater(t, "1.3.0")
	u.lockTimeout = 50 * time.Millisecond
	err = u.Recover(context.Background(), RecoverStart, "")
	wantCode(t, err, errcode.UpdateInProgress)
	if kills := e.sd.unitsOf("Kill"); len(kills) != 0 {
		t.Errorf("agent start killed %v", kills)
	}
}

func TestRecoverContinuesAStep(t *testing.T) {
	ctx := context.Background()
	e := newUpdaterEnv(t, "1.2.0")
	// The runner committed 1.3.1 of a 1.2.0 -> 1.4.0 update and crashed
	// before it recorded the next step.
	r := testRecord("01TX", "01TX", 0)
	r.ToVersion, r.TargetVersion, r.State = "1.3.1", "1.4.0", core.UpdateCommitted
	r.Steps = []Release{{Version: "1.3.1", Channel: core.ChannelStable}, {Version: "1.4.0", Channel: core.ChannelStable}}
	if err := WriteRecord(e.p, r); err != nil {
		t.Fatal(err)
	}
	writeBinaryFile(t, e.p.Binary(), binaryOf("1.3.1"))
	if err := lifecycle.Reserve(ctx, e.db, lifecycle.ReserveAll, lifecycle.ReservationOwner("01TX")); err != nil {
		t.Fatal(err)
	}
	u := e.updater(t, "1.3.1")
	if err := u.Recover(ctx, RecoverStart, ""); err != nil {
		t.Fatal(err)
	}
	all, err := ListRecords(e.p)
	if err != nil || len(all) != 2 {
		t.Fatalf("records = %+v, %v", all, err)
	}
	child := all[1]
	if child.State != core.UpdatePending || child.ParentID == nil || *child.ParentID != "01TX" ||
		child.FromVersion != "1.3.1" || child.ToVersion != "1.4.0" || child.OperationID != "01TX" {
		t.Errorf("next step = %+v", child)
	}
	starts := e.sd.callsOf("StartTransientService")
	if len(starts) != 1 || starts[0].Unit != RunnerUnit(child.ID) {
		t.Fatalf("started = %+v", starts)
	}
	if got := lockRows(t, e.db); !slices.Equal(got, []string{"* " + lifecycle.ReservationOwner("01TX")}) {
		t.Errorf("core_locks = %v, want the reservation kept for the next step", got)
	}
	// The started step runs to the target and releases the reservation.
	if err := e.updater(t, "1.3.1").Run(ctx, child.ID); err != nil {
		t.Fatal(err)
	}
	if got := binaryContent(t, e.p.Binary()); got != binaryOf("1.4.0") {
		t.Errorf("darkpanel = %q", got)
	}
	wantNoLocks(t, e.db)
}

func TestRecoverSweep(t *testing.T) {
	ctx := context.Background()
	e := newUpdaterEnv(t, "1.2.0")
	write := func(id, state, to, target string, offset time.Duration) {
		r := testRecord(id, id, offset)
		r.State, r.ToVersion, r.TargetVersion = state, to, target
		if err := WriteRecord(e.p, r); err != nil {
			t.Fatal(err)
		}
	}
	write("01DONE", core.UpdateCommitted, "1.3.0", "1.3.0", 0)
	write("01FAIL", core.UpdateFailed, "1.3.0", "1.3.0", time.Minute)
	write("01LIVE", core.UpdateSwapped, "1.3.0", "1.3.0", 2*time.Minute)
	for _, row := range [][2]string{
		{"*", "op:01NONE"}, {"a", "op:01DONE"}, {"b", "op:01FAIL"}, {"c", "op:01LIVE"}, {"ufw", "01CHANGE"},
	} {
		if _, err := e.db.Writer().Exec("INSERT INTO core_locks (key, owner) VALUES (?, ?)", row[0], row[1]); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.updater(t, "1.2.0").Recover(ctx, RecoverStart, ""); err != nil {
		t.Fatal(err)
	}
	want := []string{"c op:01LIVE", "ufw 01CHANGE"}
	if got := lockRows(t, e.db); !slices.Equal(got, want) {
		t.Errorf("core_locks = %v, want %v: only finished operations' reservations go", got, want)
	}
}
