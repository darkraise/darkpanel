package derp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/derp/server"
)

const (
	rcResource = "derp.binary:derper"
	rcFrom     = "v1.103.0"
	rcTo       = "v1.104.1"
	rcRestart  = "restart " + server.UnitName
)

var (
	// rcOld and rcNew are the contents of the binary before and after the update.
	rcOld = []byte("derper v1.103.0")
	rcNew = []byte("derper v1.104.1")

	rcActive   = core.UnitState{LoadState: "loaded", ActiveState: "active", SubState: "running"}
	rcInactive = core.UnitState{LoadState: "loaded", ActiveState: "inactive", SubState: "dead"}
)

func rcSum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// rcSetup is a host with a DERP server installed at rcFrom, whose unit is active.
func rcSetup(t *testing.T) (*dpHost, dp) {
	t.Helper()
	h := newDPHost(t)
	h.units.States[server.UnitName] = rcActive
	err := insertLocalServer(context.Background(), h.db, localServer{
		ChangeID: "c0", Mode: server.ModeIP, HostName: "203.0.113.9", DerperVersion: rcFrom, BinarySHA256: rcSum(rcOld),
		InstalledAt: h.clock.Now(), UpdatedAt: h.clock.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return h, h.dp()
}

// rcDef is the update's handler; it fails the test when the undo, the observer or the recovery handler is missing.
func rcDef(t *testing.T, d dp) core.ErasedHandler {
	t.Helper()
	h := d.binaryUpdate().Handler
	if h.Observe == nil || h.Inverse == nil || h.Recover == nil {
		t.Fatalf("handler has Observe %v, Inverse %v, Recover %v; want all three", h.Observe != nil, h.Inverse != nil, h.Recover != nil)
	}
	return h
}

// rcAdd records the update change id in phase.
func rcAdd(t *testing.T, h *dpHost, id, phase, result string) {
	t.Helper()
	err := insertUpdate(context.Background(), h.db, updateRow{
		ChangeID: id, FromVersion: rcFrom, FromSHA256: rcSum(rcOld), ToVersion: rcTo, ToSHA256: rcSum(rcNew),
		Phase: phase, Result: result, UpdatedAt: h.clock.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
}

// rcLayout puts the three binary files where an update that reached phase leaves them.
func rcLayout(h *dpHost, phase string) {
	delete(h.files, server.BinaryPath)
	delete(h.files, server.BinaryPrevPath)
	delete(h.files, server.BinaryNewPath)
	switch phase {
	case "staged":
		h.files[server.BinaryPath], h.files[server.BinaryNewPath] = rcOld, rcNew
	case "kept":
		h.files[server.BinaryPrevPath], h.files[server.BinaryNewPath] = rcOld, rcNew
	case "swapped", "restarted", "rolling_back", "done":
		h.files[server.BinaryPath], h.files[server.BinaryPrevPath] = rcNew, rcOld
	default:
		h.files[server.BinaryPath] = rcOld
	}
}

// rcDrive runs call, advancing the clock by a second whenever it waits on it.
func rcDrive(t *testing.T, h *dpHost, call func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		call()
	}()
	for deadline := time.Now().Add(20 * time.Second); ; {
		select {
		case <-done:
			return
		default:
		}
		if h.clock.Pending() > 0 {
			h.clock.Advance(time.Second)
		} else {
			time.Sleep(time.Millisecond)
		}
		if time.Now().After(deadline) {
			t.Fatal("the call did not finish")
		}
	}
}

func rcSame(t *testing.T, a, b []byte) bool {
	t.Helper()
	var va, vb any
	if err := json.Unmarshal(a, &va); err != nil {
		t.Fatalf("document %s: %v", a, err)
	}
	if err := json.Unmarshal(b, &vb); err != nil {
		t.Fatalf("document %s: %v", b, err)
	}
	return reflect.DeepEqual(va, vb)
}

// rcRestored fails the test unless the host runs the old binary again: it is at BinaryPath, neither derper.prev nor
// derper.new is left, and the unit was restarted.
func rcRestored(t *testing.T, h *dpHost) {
	t.Helper()
	if !bytes.Equal(h.files[server.BinaryPath], rcOld) {
		t.Errorf("binary %q, want the old one %q", h.files[server.BinaryPath], rcOld)
	}
	for _, p := range []string{server.BinaryPrevPath, server.BinaryNewPath} {
		if _, ok := h.files[p]; ok {
			t.Errorf("%s is still there after the rollback", p)
		}
	}
	if !slices.Contains(h.units.Calls, rcRestart) {
		t.Errorf("unit calls %v, want %q", h.units.Calls, rcRestart)
	}
}

// rcPhase is the phase of update id.
func rcPhase(t *testing.T, h *dpHost, id string) string {
	t.Helper()
	u, ok, err := loadUpdate(context.Background(), h.db.Read(), id)
	if err != nil || !ok {
		t.Fatalf("update %s: %v, %v", id, ok, err)
	}
	return u.Phase
}

func TestRCDefinition(t *testing.T) {
	_, d := rcSetup(t)
	h := rcDef(t, d)
	if h.Plan == nil || h.Task == nil || h.Verify == nil || h.Apply != nil {
		t.Error("handler lacks Plan, Task or Verify, or has an Apply")
	}
}

func TestRCObserve(t *testing.T) {
	ctx := context.Background()
	e := core.Effect{ChangeID: "c1", Resource: rcResource}
	t.Run("the version of the record and the hash of the file", func(t *testing.T) {
		h, d := rcSetup(t)
		h.files[server.BinaryPath] = rcNew
		if err := setLocalBinary(ctx, h.db, rcTo, rcSum(rcNew), h.clock.Now()); err != nil {
			t.Fatal(err)
		}
		obs, err := rcDef(t, d).Observe(ctx, h.env("c1"), e)
		want := `{"version":"` + rcTo + `","sha256":"` + rcSum(rcNew) + `"}`
		if err != nil || !rcSame(t, obs.Current.State, []byte(want)) || obs.Matches != 1 || !obs.Exclusive {
			t.Errorf("observation %+v (%v), want %s, one exclusive match", obs, err, want)
		}
	})
	t.Run("the hash is the file's, not the record's", func(t *testing.T) {
		h, d := rcSetup(t)
		h.files[server.BinaryPath] = rcNew
		obs, err := rcDef(t, d).Observe(ctx, h.env("c1"), e)
		want := `{"version":"` + rcFrom + `","sha256":"` + rcSum(rcNew) + `"}`
		if err != nil || !rcSame(t, obs.Current.State, []byte(want)) {
			t.Errorf("observation %+v (%v), want %s", obs, err, want)
		}
	})
	t.Run("no record", func(t *testing.T) {
		h := newDPHost(t)
		h.files[server.BinaryPath] = rcNew
		obs, err := rcDef(t, h.dp()).Observe(ctx, h.env("c1"), e)
		if err != nil || string(obs.Current.State) != "null" || obs.Matches != 1 || !obs.Exclusive {
			t.Errorf("observation %+v (%v), want the state null, one exclusive match", obs, err)
		}
	})
	t.Run("no binary", func(t *testing.T) {
		h, d := rcSetup(t)
		obs, err := rcDef(t, d).Observe(ctx, h.env("c1"), e)
		if err != nil || string(obs.Current.State) != "null" {
			t.Errorf("observation %+v (%v), want the state null", obs, err)
		}
	})
	t.Run("a binary that cannot be read", func(t *testing.T) {
		h, d := rcSetup(t)
		d.d.readFile = func(string) ([]byte, error) { return nil, errors.New("input/output error") }
		if _, err := rcDef(t, d).Observe(ctx, h.env("c1"), e); err == nil {
			t.Error("observed with an unreadable binary, want an error")
		}
	})
	t.Run("another resource", func(t *testing.T) {
		h, d := rcSetup(t)
		if _, err := rcDef(t, d).Observe(ctx, h.env("c1"), core.Effect{Resource: "derp.cert:203.0.113.9"}); err == nil {
			t.Error("observed another resource, want an error")
		}
	})
}

func TestRCRecoverWithoutARollback(t *testing.T) {
	tests := []struct {
		name   string
		phase  string // "" for no update row
		result string
		layout string
		want   core.RecoveryOutcome
	}{
		{"no update row: only a staged binary can be left", "", "", "staged", core.RecoveryOutcome{State: core.RecoveryRolledBack}},
		{"staged: derper.new is removed", "staged", "", "staged", core.RecoveryOutcome{State: core.RecoveryRolledBack}},
		{"staged with nothing written yet", "staged", "", "", core.RecoveryOutcome{State: core.RecoveryRolledBack}},
		{"already rolled back", "rolled_back", "", "", core.RecoveryOutcome{State: core.RecoveryRolledBack}},
		{"done", "done", "", "done", core.RecoveryOutcome{State: core.RecoveryCompleted, Output: json.RawMessage(`{"from":"` + rcFrom + `","to":"` + rcTo + `"}`)}},
		{"failed", "failed", "the unit is not active after the rollback", "swapped", core.RecoveryOutcome{State: core.RecoveryNeedsRepair}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			h, d := rcSetup(t)
			rcLayout(h, tc.layout)
			before := maps.Clone(h.files)
			if tc.phase != "" {
				rcAdd(t, h, "c1", tc.phase, tc.result)
			}
			out, err := rcDef(t, d).Recover(ctx, h.env("c1"))
			if err != nil {
				t.Fatal(err)
			}
			if out.State != tc.want.State || (tc.want.Output != nil && !rcSame(t, out.Output, tc.want.Output)) {
				t.Errorf("outcome %+v, want %+v", out, tc.want)
			}
			if tc.want.State == core.RecoveryNeedsRepair && (!strings.Contains(out.Reason, rcTo) || !strings.Contains(out.Reason, tc.result)) {
				t.Errorf("reason %q, want the target version and the update's result", out.Reason)
			}
			if len(h.units.Calls) != 0 || len(h.ran) != 0 {
				t.Errorf("recover restarted %v and ran %v, want nothing", h.units.Calls, h.ran)
			}
			wantFiles := before
			if tc.layout == "staged" && (tc.phase == "" || tc.phase == "staged") {
				wantFiles = map[string][]byte{server.BinaryPath: rcOld}
			}
			if !reflect.DeepEqual(h.files, wantFiles) {
				t.Errorf("files %v, want %v", slices.Sorted(maps.Keys(h.files)), slices.Sorted(maps.Keys(wantFiles)))
			}
		})
	}
}

func TestRCRecoverRollsAnInterruptedUpdateBack(t *testing.T) {
	for _, phase := range []string{"kept", "swapped", "restarted", "rolling_back"} {
		t.Run(phase, func(t *testing.T) {
			ctx := context.Background()
			h, d := rcSetup(t)
			rcLayout(h, phase)
			rcAdd(t, h, "c1", phase, "")
			var out core.RecoveryOutcome
			var err error
			rcDrive(t, h, func() { out, err = rcDef(t, d).Recover(ctx, h.env("c1")) })
			if err != nil || out.State != core.RecoveryRolledBack {
				t.Fatalf("outcome %+v, %v; want %s", out, err, core.RecoveryRolledBack)
			}
			rcRestored(t, h)
			if got := rcPhase(t, h, "c1"); got != "rolled_back" {
				t.Errorf("update phase %q, want rolled_back", got)
			}
			if row, _, err := loadLocalServer(ctx, h.db.Read()); err != nil || row.DerperVersion != rcFrom {
				t.Errorf("record %+v, %v; want the version %s it still holds", row, err, rcFrom)
			}
		})
	}
}

func TestRCRecoverNeedsRepairWhenTheRollbackFails(t *testing.T) {
	ctx := context.Background()
	h, d := rcSetup(t)
	h.units.States[server.UnitName] = rcInactive
	rcLayout(h, "swapped")
	rcAdd(t, h, "c1", "swapped", "")
	var out core.RecoveryOutcome
	var err error
	rcDrive(t, h, func() { out, err = rcDef(t, d).Recover(ctx, h.env("c1")) })
	if err != nil {
		t.Fatal(err)
	}
	if out.State != core.RecoveryNeedsRepair || !strings.HasPrefix(out.Reason, "the rollback of the update to "+rcTo+" failed: ") {
		t.Errorf("outcome %+v, want %s with the rollback's error as the reason", out, core.RecoveryNeedsRepair)
	}
	if got := rcPhase(t, h, "c1"); got != "failed" {
		t.Errorf("update phase %q, want failed", got)
	}
}

func TestRCInverse(t *testing.T) {
	ctx := context.Background()
	h, d := rcSetup(t)
	rcLayout(h, "done")
	rcAdd(t, h, "c1", "done", "")
	if err := setLocalBinary(ctx, h.db, rcTo, rcSum(rcNew), h.clock.Now()); err != nil {
		t.Fatal(err)
	}
	var res core.InverseResult
	var err error
	rcDrive(t, h, func() {
		res, err = rcDef(t, d).Inverse(ctx, h.env("c1"), core.Effect{ChangeID: "c1", Resource: rcResource, State: core.EffectUndoing})
	})
	if err != nil || res != (core.InverseResult{}) {
		t.Fatalf("inverse = %+v, %v; want an empty result", res, err)
	}
	rcRestored(t, h)
	if got := rcPhase(t, h, "c1"); got != "rolled_back" {
		t.Errorf("update phase %q, want rolled_back", got)
	}
	if row, _, err := loadLocalServer(ctx, h.db.Read()); err != nil || row.DerperVersion != rcFrom || row.BinarySHA256 != rcSum(rcOld) {
		t.Errorf("record %+v, %v; want the old version %s and its hash back", row, err, rcFrom)
	}
}

func TestRCInverseOfARolledBackUpdateChangesNothing(t *testing.T) {
	ctx := context.Background()
	h, d := rcSetup(t)
	rcLayout(h, "")
	rcAdd(t, h, "c1", "rolled_back", "")
	if _, err := rcDef(t, d).Inverse(ctx, h.env("c1"), core.Effect{ChangeID: "c1", Resource: rcResource, State: core.EffectUndoing}); err != nil {
		t.Fatal(err)
	}
	if len(h.units.Calls) != 0 || len(h.ran) != 0 || !reflect.DeepEqual(h.files, map[string][]byte{server.BinaryPath: rcOld}) {
		t.Errorf("inverse restarted %v, ran %v and left files %v, want nothing changed", h.units.Calls, h.ran, slices.Sorted(maps.Keys(h.files)))
	}
}

func TestRCInverseFailures(t *testing.T) {
	ctx := context.Background()
	h, d := rcSetup(t)
	def := rcDef(t, d)
	rcLayout(h, "done")
	if err := setLocalBinary(ctx, h.db, rcTo, rcSum(rcNew), h.clock.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := def.Inverse(ctx, h.env("c5"), core.Effect{ChangeID: "c5", Resource: rcResource}); err == nil {
		t.Error("inverse of a change with no update row succeeded, want an error")
	}
	rcAdd(t, h, "c1", "done", "")
	if _, err := def.Inverse(ctx, h.env("c1"), core.Effect{ChangeID: "c1", Resource: "derp.cert:203.0.113.9"}); err == nil {
		t.Error("inverse of another resource succeeded, want an error")
	}
	rcAdd(t, h, "c2", "staged", "")
	if _, err := def.Inverse(ctx, h.env("c2"), core.Effect{ChangeID: "c2", Resource: rcResource}); err == nil {
		t.Error("inverse of an update that only staged a binary succeeded, want an error")
	}
	if len(h.units.Calls) != 0 || len(h.ran) != 0 || !bytes.Equal(h.files[server.BinaryPath], rcNew) {
		t.Errorf("the refused inverses restarted %v, ran %v and left binary %q, want nothing changed", h.units.Calls, h.ran, h.files[server.BinaryPath])
	}
	h.units.States[server.UnitName] = rcInactive
	var err error
	rcDrive(t, h, func() {
		_, err = def.Inverse(ctx, h.env("c1"), core.Effect{ChangeID: "c1", Resource: rcResource, State: core.EffectUndoing})
	})
	if err == nil {
		t.Error("inverse with a unit that stays inactive succeeded, want the rollback's error")
	}
	if got := rcPhase(t, h, "c1"); got != "failed" {
		t.Errorf("update phase %q after a failed rollback, want failed", got)
	}
	if row, _, err := loadLocalServer(ctx, h.db.Read()); err != nil || row.DerperVersion != rcFrom || row.BinarySHA256 != rcSum(rcOld) {
		t.Errorf("record %+v, %v; a rollback that put the old binary back must record %s and its hash, even when the unit stays inactive", row, err, rcFrom)
	}
	if !bytes.Equal(h.files[server.BinaryPath], rcOld) {
		t.Errorf("binary %q after the failed rollback, want the old one %q", h.files[server.BinaryPath], rcOld)
	}
}
