package backup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/paths"
	"github.com/darkraise/darkpanel/internal/update"
)

var rgNow = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

// rgUnits answers State from a table and records which units were asked
// about. Every other method panics through the nil embedded interface, so a
// check that touched a unit in any other way fails the test.
type rgUnits struct {
	core.Units
	states map[string]core.UnitState
	errs   map[string]error
	asked  []string
}

func (u *rgUnits) State(_ context.Context, unit string) (core.UnitState, error) {
	u.asked = append(u.asked, unit)
	if err := u.errs[unit]; err != nil {
		return core.UnitState{}, err
	}
	return u.states[unit], nil
}

// rgWrite creates path with content, and its directory when missing.
func rgWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// rgJournal is the path of the journal of transaction tx in dir.
func rgJournal(dir, tx string) string { return filepath.Join(dir, "restore-"+tx+".journal") }

// rgHost is a Host over a temporary installation that passes every check:
// root, the unit file, the controller directory and key present, the
// controller inactive, the agent failed, no journal, the lock free.
func rgHost(t *testing.T) (Host, *rgUnits) {
	t.Helper()
	root := t.TempDir()
	p := paths.Under(root)
	rgWrite(t, p.ControllerKey, "key")
	if err := os.MkdirAll(p.ControllerDir, 0o700); err != nil {
		t.Fatal(err)
	}
	unitFile := filepath.Join(root, "units", update.ControllerUnit)
	rgWrite(t, unitFile, "[Unit]\n")
	units := &rgUnits{
		states: map[string]core.UnitState{
			update.ControllerUnit: {LoadState: "loaded", ActiveState: "inactive"},
			update.AgentUnit:      {LoadState: "loaded", ActiveState: "failed"},
		},
		errs: map[string]error{},
	}
	return Host{
		Paths: p, UnitFile: unitFile, LockFile: filepath.Join(root, "darkpanel-restore.lock"),
		Units: units, Euid: func() int { return 0 },
		Owner: func() (int, int, error) { return os.Getuid(), os.Getgid(), nil },
		Ops:   OSOps(), Clock: coretest.NewFakeClock(rgNow),
		NewTx: func() string { return "01RGTXAAAAAAAAAAAAAAAAAAAA" },
		Log:   slog.New(slog.DiscardHandler),
	}, units
}

func TestRgCheckRestore(t *testing.T) {
	const (
		notRoot      = "backup: a restore must run as root"
		notInstalled = "backup: no controller is installed here; run darkpanel system install --role controller first"
	)
	stop := func(unit, state string) func(Host) string {
		return func(Host) string {
			return fmt.Sprintf("backup: stop darkpanel-controller.service and darkpanel-agent.service before a restore (%s is %s)", unit, state)
		}
	}
	text := func(s string) func(Host) string { return func(Host) string { return s } }
	journal := func(name string) func(Host) string {
		return func(h Host) string {
			return fmt.Sprintf("backup: an unfinished restore exists (%s); start the controller or run `darkpanel controller restore --recover` first",
				filepath.Join(h.Paths.ControllerDir, name))
		}
	}
	held := func(h Host) string { return "backup: another restore or recovery holds " + h.LockFile }
	takeLock := func(t *testing.T, h *Host) {
		release, err := h.lock()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(release)
	}

	tests := []struct {
		name  string
		setup func(t *testing.T, h *Host, u *rgUnits)
		want  func(Host) string // nil: no refusal
	}{
		{"installed and stopped", func(*testing.T, *Host, *rgUnits) {}, nil},
		{"not root", func(_ *testing.T, h *Host, _ *rgUnits) { h.Euid = func() int { return 1000 } }, text(notRoot)},
		{"not root before not installed", func(t *testing.T, h *Host, _ *rgUnits) {
			h.Euid = func() int { return 1000 }
			if err := os.Remove(h.UnitFile); err != nil {
				t.Fatal(err)
			}
		}, text(notRoot)},
		{"no unit file", func(t *testing.T, h *Host, _ *rgUnits) {
			if err := os.Remove(h.UnitFile); err != nil {
				t.Fatal(err)
			}
		}, text(notInstalled)},
		{"no controller directory", func(t *testing.T, h *Host, _ *rgUnits) {
			if err := os.RemoveAll(h.Paths.ControllerDir); err != nil {
				t.Fatal(err)
			}
		}, text(notInstalled)},
		{"controller directory is a file", func(t *testing.T, h *Host, _ *rgUnits) {
			if err := os.RemoveAll(h.Paths.ControllerDir); err != nil {
				t.Fatal(err)
			}
			rgWrite(t, h.Paths.ControllerDir, "not a directory")
		}, text(notInstalled)},
		{"no controller key", func(t *testing.T, h *Host, _ *rgUnits) {
			if err := os.Remove(h.Paths.ControllerKey); err != nil {
				t.Fatal(err)
			}
		}, text(notInstalled)},
		{"not installed before a running service", func(t *testing.T, h *Host, u *rgUnits) {
			if err := os.Remove(h.UnitFile); err != nil {
				t.Fatal(err)
			}
			u.states[update.ControllerUnit] = core.UnitState{LoadState: "loaded", ActiveState: "active"}
		}, text(notInstalled)},
		{"controller running", func(_ *testing.T, _ *Host, u *rgUnits) {
			u.states[update.ControllerUnit] = core.UnitState{LoadState: "loaded", ActiveState: "active"}
		}, stop(update.ControllerUnit, "active")},
		{"agent activating", func(_ *testing.T, _ *Host, u *rgUnits) {
			u.states[update.AgentUnit] = core.UnitState{LoadState: "loaded", ActiveState: "activating"}
		}, stop(update.AgentUnit, "activating")},
		{"both running names the controller", func(_ *testing.T, _ *Host, u *rgUnits) {
			u.states[update.ControllerUnit] = core.UnitState{LoadState: "loaded", ActiveState: "active"}
			u.states[update.AgentUnit] = core.UnitState{LoadState: "loaded", ActiveState: "active"}
		}, stop(update.ControllerUnit, "active")},
		{"units that are not found count as stopped", func(_ *testing.T, _ *Host, u *rgUnits) {
			u.states[update.ControllerUnit] = core.UnitState{LoadState: "not-found", ActiveState: "active"}
			u.states[update.AgentUnit] = core.UnitState{LoadState: "not-found", ActiveState: "active"}
		}, nil},
		{"journal", func(t *testing.T, h *Host, _ *rgUnits) {
			rgWrite(t, rgJournal(h.Paths.ControllerDir, "01A"), "{}")
		}, journal("restore-01A.journal")},
		{"two journals name the oldest", func(t *testing.T, h *Host, _ *rgUnits) {
			rgWrite(t, rgJournal(h.Paths.ControllerDir, "01B"), "{}")
			rgWrite(t, rgJournal(h.Paths.ControllerDir, "01A"), "{}")
		}, journal("restore-01A.journal")},
		{"a running service before a journal", func(t *testing.T, h *Host, u *rgUnits) {
			rgWrite(t, rgJournal(h.Paths.ControllerDir, "01A"), "{}")
			u.states[update.AgentUnit] = core.UnitState{LoadState: "loaded", ActiveState: "active"}
		}, stop(update.AgentUnit, "active")},
		{"a journal's temporary file alone is no journal", func(t *testing.T, h *Host, _ *rgUnits) {
			rgWrite(t, rgJournal(h.Paths.ControllerDir, "01A")+".new", "{")
		}, nil},
		{"lock held", func(t *testing.T, h *Host, _ *rgUnits) { takeLock(t, h) }, held},
		{"a journal before a held lock", func(t *testing.T, h *Host, _ *rgUnits) {
			takeLock(t, h)
			rgWrite(t, rgJournal(h.Paths.ControllerDir, "01A"), "{}")
		}, journal("restore-01A.journal")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, units := rgHost(t)
			tt.setup(t, &h, units)
			err := h.checkRestore(context.Background())
			if tt.want == nil {
				if err != nil {
					t.Fatalf("checkRestore = %v, want no refusal", err)
				}
				return
			}
			if want := tt.want(h); err == nil || err.Error() != want {
				t.Fatalf("checkRestore = %v, want %q", err, want)
			}
		})
	}
}

// TestRgCheckRestoreAsksControllerThenAgent: the units are read through
// State alone, the controller first, and the first refusal ends the check.
func TestRgCheckRestoreAsksControllerThenAgent(t *testing.T) {
	h, units := rgHost(t)
	if err := h.checkRestore(context.Background()); err != nil {
		t.Fatal(err)
	}
	if want := []string{update.ControllerUnit, update.AgentUnit}; strings.Join(units.asked, ",") != strings.Join(want, ",") {
		t.Errorf("units asked = %v, want %v", units.asked, want)
	}

	h, units = rgHost(t)
	units.states[update.ControllerUnit] = core.UnitState{LoadState: "loaded", ActiveState: "active"}
	if err := h.checkRestore(context.Background()); err == nil {
		t.Fatal("checkRestore accepted a running controller")
	}
	if want := []string{update.ControllerUnit}; strings.Join(units.asked, ",") != strings.Join(want, ",") {
		t.Errorf("units asked after a refusal = %v, want %v", units.asked, want)
	}
}

func TestRgCheckRestoreReportsAStateError(t *testing.T) {
	h, units := rgHost(t)
	boom := errors.New("dbus: connection closed")
	units.errs[update.AgentUnit] = boom
	err := h.checkRestore(context.Background())
	if !errors.Is(err, boom) || !strings.Contains(err.Error(), update.AgentUnit) {
		t.Errorf("checkRestore = %v, want an error wrapping the bus error and naming %s", err, update.AgentUnit)
	}
}

// TestRgCheckRestoreLeavesTheLockFree: the lock check takes and releases the
// lock, so the restore that follows can take it.
func TestRgCheckRestoreLeavesTheLockFree(t *testing.T) {
	h, _ := rgHost(t)
	if err := h.checkRestore(context.Background()); err != nil {
		t.Fatal(err)
	}
	release, err := h.lock()
	if err != nil {
		t.Fatalf("lock after a passed check: %v", err)
	}
	release()
}

func TestRgLockIsExclusiveUntilReleased(t *testing.T) {
	h, _ := rgHost(t)
	release, err := h.lock()
	if err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(h.LockFile); err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("lock file = %v, %v; want mode 0600", st, err)
	}
	_, err = h.lock()
	if want := "backup: another restore or recovery holds " + h.LockFile; err == nil || err.Error() != want {
		t.Fatalf("second lock = %v, want %q", err, want)
	}
	release()
	release()
	again, err := h.lock()
	if err != nil {
		t.Fatalf("lock after release: %v", err)
	}
	if _, err := h.lock(); err == nil {
		t.Error("a repeated release freed the lock a later caller holds")
	}
	again()
}

func TestRgLockRefusesWhatItCannotOpen(t *testing.T) {
	h, _ := rgHost(t)
	h.LockFile = filepath.Join(filepath.Dir(h.LockFile), "missing", "restore.lock")
	if _, err := h.lock(); err == nil || strings.Contains(err.Error(), "another restore") {
		t.Errorf("lock in a missing directory = %v, want an open error", err)
	}

	target := filepath.Join(t.TempDir(), "target")
	rgWrite(t, target, "")
	link := filepath.Join(t.TempDir(), "restore.lock")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	h.LockFile = link
	if _, err := h.lock(); err == nil {
		t.Error("lock followed a symbolic link")
	}
}

type rgUsers struct {
	uid, gid int
	found    bool
	err      error
	asked    string
}

func (u *rgUsers) Lookup(name string) (int, int, bool, error) {
	u.asked = name
	return u.uid, u.gid, u.found, u.err
}

func TestRgServiceOwner(t *testing.T) {
	users := &rgUsers{uid: 996, gid: 995, found: true}
	uid, gid, err := serviceOwner(users)()
	if err != nil || uid != 996 || gid != 995 || users.asked != "darkpanel" {
		t.Errorf("serviceOwner = %d, %d, %v after asking for %q; want 996, 995 and the darkpanel user", uid, gid, err, users.asked)
	}
	if _, _, err := serviceOwner(&rgUsers{})(); err == nil || !strings.Contains(err.Error(), "darkpanel") {
		t.Errorf("serviceOwner for a missing user = %v, want an error naming the user", err)
	}
	boom := errors.New("nss: unavailable")
	if _, _, err := serviceOwner(&rgUsers{err: boom})(); !errors.Is(err, boom) {
		t.Errorf("serviceOwner = %v, want the lookup error wrapped", err)
	}
}

func TestRgUlidTx(t *testing.T) {
	clock := coretest.NewFakeClock(rgNow)
	newTx := ulidTx(clock)
	first, second := newTx(), newTx()
	id, err := ulid.ParseStrict(first)
	if err != nil || first != strings.ToUpper(first) || len(first) != 26 {
		t.Fatalf("transaction id %q: %v; want 26 upper-case ULID characters", first, err)
	}
	if id.Time() != uint64(rgNow.UnixMilli()) {
		t.Errorf("transaction id time = %d, want the clock's %d", id.Time(), rgNow.UnixMilli())
	}
	if first == second {
		t.Error("two transaction ids are equal")
	}
	clock.Advance(time.Second)
	if later := newTx(); later <= first {
		t.Errorf("a later transaction id %s sorts before %s", later, first)
	}
}

func TestRgRestoreLockPath(t *testing.T) {
	if RestoreLockPath != "/run/darkpanel-restore.lock" {
		t.Errorf("RestoreLockPath = %q", RestoreLockPath)
	}
}
