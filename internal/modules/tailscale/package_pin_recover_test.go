package tailscale

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/platform"
)

const (
	rbAptGet       = "/usr/bin/apt-get"
	rbAptMark      = "/usr/bin/apt-mark"
	rbDpkg         = "/usr/bin/dpkg"
	rbResource     = "tailscale.package:tailscale"
	rbPriorVersion = "1.104.1"
	rbTarget       = "1.102.0"
)

// rbDeb is the content of the staged prior package.
var rbDeb = []byte("staged tailscale 1.104.1 package")

// rbStaged is the staged_deb and staged_sha256 a pin that staged rbDeb holds, and the bytes of that file.
func rbStaged() (path, sum string, data []byte) {
	h := sha256.Sum256(rbDeb)
	return debDir + "/tailscale_" + rbPriorVersion + "_amd64.deb", hex.EncodeToString(h[:]), rbDeb
}

// rbRunner runs vectors on the host and keeps the hold state of the tailscale package in *held; when the install of
// the staged package succeeds it leaves the prior version installed, as apt-get would.
type rbRunner struct {
	h    *tsHost
	held *bool
}

func (r rbRunner) Run(ctx context.Context, cmd core.Cmd) (core.CmdResult, error) {
	res, err := r.h.Run(ctx, cmd)
	if err != nil || res.ExitCode != 0 {
		return res, err
	}
	deb, _, _ := rbStaged()
	switch {
	case cmd.Path == rbAptMark && slices.Equal(cmd.Args, []string{"hold", "tailscale"}):
		*r.held = true
	case cmd.Path == rbAptMark && slices.Equal(cmd.Args, []string{"unhold", "tailscale"}):
		*r.held = false
	case cmd.Path == rbAptMark && slices.Equal(cmd.Args, []string{"showhold"}):
		if *r.held {
			res.Stdout = []byte("tailscale\n")
		}
	case cmd.Path == rbAptGet && slices.Equal(cmd.Args, platform.AptInstallDeb(deb)):
		r.h.reads[thDpkgPath] = thDpkgStatus(rbPriorVersion)
	}
	return res, nil
}

// rbSetup is a host on which the pin to rbTarget over rbPriorVersion has installed rbTarget, held or not, with the
// staged prior package on disk.
func rbSetup(t *testing.T, held bool) (*tsHost, ts, *bool) {
	t.Helper()
	h := newTSHost(t)
	deb, _, data := rbStaged()
	h.reads[thDpkgPath] = thDpkgStatus(rbTarget)
	h.reads[deb] = data
	return h, h.ts(), &held
}

func rbEnv(h *tsHost, held *bool, changeID string) core.Env {
	env := h.env(changeID)
	env.Exec = rbRunner{h: h, held: held}
	return env
}

// rbPin is the pin change id in phase, with its prior package staged.
func rbPin(id, phase string, at time.Time) pinRow {
	deb, sum, _ := rbStaged()
	return pinRow{
		ChangeID: id, Version: rbTarget, Arch: "amd64", PriorVersion: rbPriorVersion, PriorBackend: "Running",
		Phase: phase, StagedDeb: deb, StagedSHA256: sum, UpdatedAt: at,
	}
}

func rbAdd(t *testing.T, h *tsHost, p pinRow) {
	t.Helper()
	ctx := context.Background()
	if err := h.db.Write(ctx, func(tx *sql.Tx) error { return insertPin(ctx, tx, p) }); err != nil {
		t.Fatal(err)
	}
}

// rbDef is the pin action's handler; it fails the test when the undo or the recovery handler is missing.
func rbDef(t *testing.T, tt ts) core.ErasedHandler {
	t.Helper()
	h := tt.packagePin().Handler
	if h.Observe == nil || h.Inverse == nil || h.Recover == nil {
		t.Fatalf("handler has Observe %v, Inverse %v, Recover %v; want all three", h.Observe != nil, h.Inverse != nil, h.Recover != nil)
	}
	return h
}

// rbSteps lists "<step> <result>" of the pin's journal, in order.
func rbSteps(t *testing.T, h *tsHost, id string) []string {
	t.Helper()
	rows, err := h.db.Read().Query(`SELECT step, result FROM tailscale_pin_steps WHERE change_id = ? ORDER BY seq`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var step, result string
		if err := rows.Scan(&step, &result); err != nil {
			t.Fatal(err)
		}
		out = append(out, step+" "+result)
	}
	return out
}

func rbSame(t *testing.T, a, b []byte) bool {
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

// rbRolledBack fails the test unless the host is back at the prior version, not held (the pin's prior hold state),
// with the staged package installed through APT-5 and tailscaled restarted.
func rbRolledBack(t *testing.T, h *tsHost, tt ts, held *bool, wantConfigure bool) {
	t.Helper()
	deb, _, _ := rbStaged()
	if v, err := tt.installedVersion(context.Background(), h.env("")); err != nil || v != rbPriorVersion {
		t.Errorf("installed version %q, %v; want %s", v, err, rbPriorVersion)
	}
	if *held {
		t.Error("the package is held after the rollback, want the pin's prior state: not held")
	}
	install := append([]string{rbAptGet}, platform.AptInstallDeb(deb)...)
	if !slices.ContainsFunc(h.ran, func(v []string) bool { return slices.Equal(v, install) }) {
		t.Errorf("ran %v, want the install of the staged package %v", h.ran, install)
	}
	configure := slices.ContainsFunc(h.ran, func(v []string) bool { return slices.Equal(v, []string{rbDpkg, "--configure", "-a"}) })
	if configure != wantConfigure {
		t.Errorf("dpkg --configure -a ran %v, want %v", configure, wantConfigure)
	}
	if !slices.Contains(h.units.Restarts, tailscaledUnit) {
		t.Errorf("restarts %v, want %s", h.units.Restarts, tailscaledUnit)
	}
}

func TestRbDefinition(t *testing.T) {
	_, tt, _ := rbSetup(t, false)
	h := rbDef(t, tt)
	if h.Plan == nil || h.Task == nil || h.Verify == nil || h.Apply != nil {
		t.Error("handler lacks Plan, Task or Verify, or has an Apply")
	}
}

func TestRbObserve(t *testing.T) {
	ctx := context.Background()
	h, tt, held := rbSetup(t, true)
	def := rbDef(t, tt)
	obs, err := def.Observe(ctx, rbEnv(h, held, "c1"), core.Effect{Resource: rbResource})
	if err != nil || !rbSame(t, obs.Current.State, []byte(`{"version":"1.102.0","held":true}`)) || obs.Matches != 1 || !obs.Exclusive {
		t.Errorf("observation %+v (%v), want the installed version and the hold, one exclusive match", obs, err)
	}
	*held = false
	h.reads[thDpkgPath] = thDpkgStatus("")
	obs, err = def.Observe(ctx, rbEnv(h, held, "c1"), core.Effect{Resource: rbResource})
	if err != nil || !rbSame(t, obs.Current.State, []byte(`{"version":"","held":false}`)) {
		t.Errorf("observation %+v (%v), want an empty version when the package is not installed", obs, err)
	}
	if _, err := def.Observe(ctx, rbEnv(h, held, "c1"), core.Effect{Resource: "tailscale.prefs:node"}); err == nil {
		t.Error("observed another resource, want an error")
	}
	h.script(rbAptMark+" showhold", tsRun{res: core.CmdResult{ExitCode: 100}})
	if _, err := def.Observe(ctx, rbEnv(h, held, "c1"), core.Effect{Resource: rbResource}); err == nil {
		t.Error("observed with showhold failing, want an error")
	}
}

func TestRbRecoverWithoutARollback(t *testing.T) {
	tests := []struct {
		name  string
		phase string // "" for no pin row
		step  [2]string
		want  core.RecoveryOutcome
	}{
		{"no pin row", "", [2]string{}, core.RecoveryOutcome{State: core.RecoveryRolledBack}},
		{"recorded: nothing was changed", "recorded", [2]string{}, core.RecoveryOutcome{State: core.RecoveryRolledBack}},
		{"already rolled back", "rolled_back", [2]string{}, core.RecoveryOutcome{State: core.RecoveryRolledBack}},
		{"done", "done", [2]string{}, core.RecoveryOutcome{State: core.RecoveryCompleted, Output: json.RawMessage(`{"version":"1.102.0","priorVersion":"1.104.1","priorHeld":false}`)}},
		{"failed", "failed", [2]string{"rollback-install", "failed: sha256 mismatch"}, core.RecoveryOutcome{State: core.RecoveryNeedsRepair}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			h, tt, held := rbSetup(t, true)
			if tc.phase != "" {
				rbAdd(t, h, rbPin("c1", tc.phase, h.clock.Now()))
			}
			if tc.step[0] != "" {
				if err := addPinStep(ctx, h.db, "c1", tc.step[0], tc.step[1], h.clock.Now()); err != nil {
					t.Fatal(err)
				}
			}
			out, err := rbDef(t, tt).Recover(ctx, rbEnv(h, held, "c1"))
			if err != nil {
				t.Fatal(err)
			}
			if out.State != tc.want.State || (tc.want.Output != nil && !rbSame(t, out.Output, tc.want.Output)) {
				t.Errorf("outcome %+v, want %+v", out, tc.want)
			}
			if tc.want.State == core.RecoveryNeedsRepair && (!strings.Contains(out.Reason, "rollback-install") || !strings.Contains(out.Reason, "failed: sha256 mismatch")) {
				t.Errorf("reason %q, want the last step and its result", out.Reason)
			}
			if len(h.ran) != 0 || len(h.units.Restarts) != 0 {
				t.Errorf("recover ran %v and restarted %v, want nothing", h.ran, h.units.Restarts)
			}
		})
	}
}

func TestRbRecoverNamesAFailedPinWithoutSteps(t *testing.T) {
	ctx := context.Background()
	h, tt, held := rbSetup(t, true)
	rbAdd(t, h, rbPin("c1", "failed", h.clock.Now()))
	out, err := rbDef(t, tt).Recover(ctx, rbEnv(h, held, "c1"))
	if err != nil {
		t.Fatal(err)
	}
	if want := (core.RecoveryOutcome{State: core.RecoveryNeedsRepair, Reason: "the pin of 1.102.0 failed"}); out.State != want.State || out.Reason != want.Reason {
		t.Errorf("outcome %+v, want %+v", out, want)
	}
	if len(h.ran) != 0 || len(h.units.Restarts) != 0 {
		t.Errorf("recover ran %v and restarted %v, want nothing", h.ran, h.units.Restarts)
	}
}

func TestRbRecoverRollsAnInterruptedPinBack(t *testing.T) {
	tests := []struct {
		phase         string
		held          bool
		wantConfigure bool
	}{
		{"staged", false, false},
		{"installed", false, true},
		{"held", true, false},
		{"checking", true, false},
		{"rolling_back", false, true},
	}
	for _, tc := range tests {
		t.Run(tc.phase, func(t *testing.T) {
			ctx := context.Background()
			h, tt, held := rbSetup(t, tc.held)
			if tc.phase == "staged" {
				// A staged pin has only downloaded the target package, so the host still runs the prior version.
				h.reads[thDpkgPath] = thDpkgStatus(rbPriorVersion)
			}
			rbAdd(t, h, rbPin("c1", tc.phase, h.clock.Now()))
			out, err := rbDef(t, tt).Recover(ctx, rbEnv(h, held, "c1"))
			if err != nil || out.State != core.RecoveryRolledBack {
				t.Fatalf("outcome %+v, %v; want %s", out, err, core.RecoveryRolledBack)
			}
			if tc.phase == "staged" {
				if *held {
					t.Error("the package is held after the rollback, want the pin's prior state: not held")
				}
				for _, v := range h.ran {
					if !slices.Equal(v, []string{rbAptMark, "showhold"}) {
						t.Errorf("ran %v, want only reads of the hold: a staged pin installed nothing", h.ran)
						break
					}
				}
				if len(h.units.Restarts) != 0 {
					t.Errorf("restarts %v, want none: a staged pin installed nothing", h.units.Restarts)
				}
			} else {
				rbRolledBack(t, h, tt, held, tc.wantConfigure)
			}
			if p, ok, err := loadPin(ctx, h.db.Read(), "c1"); err != nil || !ok || p.Phase != "rolled_back" {
				t.Errorf("pin %+v, %v, %v; want phase rolled_back", p, ok, err)
			}
			if steps := rbSteps(t, h, "c1"); len(steps) == 0 {
				t.Error("the rollback journaled no step")
			}
		})
	}
}

func TestRbRecoverNeedsRepairWhenTheRollbackFails(t *testing.T) {
	ctx := context.Background()
	h, tt, held := rbSetup(t, true)
	p := rbPin("c1", "checking", h.clock.Now())
	p.StagedSHA256 = strings.Repeat("0", 64)
	rbAdd(t, h, p)
	out, err := rbDef(t, tt).Recover(ctx, rbEnv(h, held, "c1"))
	if err != nil {
		t.Fatal(err)
	}
	if out.State != core.RecoveryNeedsRepair || !strings.HasPrefix(out.Reason, "the rollback of the pin of 1.102.0 failed: ") {
		t.Errorf("outcome %+v, want %s with the rollback's error as the reason", out, core.RecoveryNeedsRepair)
	}
	deb, _, _ := rbStaged()
	if slices.ContainsFunc(h.ran, func(v []string) bool {
		return slices.Equal(v, append([]string{rbAptGet}, platform.AptInstallDeb(deb)...))
	}) {
		t.Errorf("ran %v, want no install of a package whose SHA-256 does not match", h.ran)
	}
	if got, _, _ := loadPin(ctx, h.db.Read(), "c1"); got.Phase != "failed" {
		t.Errorf("pin phase %q, want failed", got.Phase)
	}
}

func TestRbRecoverSettlesTheRowOfAPinWithNothingStaged(t *testing.T) {
	ctx := context.Background()
	h, tt, held := rbSetup(t, false)
	// An unheld pin has installed nothing, so the host still runs the prior version.
	h.reads[thDpkgPath] = thDpkgStatus(rbPriorVersion)
	p := rbPin("c1", "unheld", h.clock.Now())
	p.StagedDeb, p.StagedSHA256 = "", ""
	rbAdd(t, h, p)
	out, err := rbDef(t, tt).Recover(ctx, rbEnv(h, held, "c1"))
	if err != nil {
		t.Fatal(err)
	}
	// Neither held before nor after, and nothing installed: the rollback only verifies the host is as before.
	if out.State != core.RecoveryRolledBack || out.Reason != "" {
		t.Errorf("outcome %+v, want %s with no reason", out, core.RecoveryRolledBack)
	}
	if got, ok, err := loadPin(ctx, h.db.Read(), "c1"); err != nil || !ok || got.Phase != "rolled_back" {
		t.Errorf("pin %+v, %v, %v; want phase rolled_back", got, ok, err)
	}
	for _, v := range h.ran {
		if !slices.Equal(v, []string{rbAptMark, "showhold"}) {
			t.Errorf("ran %v, want only reads of the hold: an unheld pin installed nothing", h.ran)
			break
		}
	}
	if len(h.units.Restarts) != 0 {
		t.Errorf("restarts %v, want none: an unheld pin installed nothing", h.units.Restarts)
	}
	if *held {
		t.Error("the package is held after the rollback, want the pin's prior state: not held")
	}
	if steps := rbSteps(t, h, "c1"); !slices.Equal(steps, []string{"rollback-verify ok"}) {
		t.Errorf("steps %v, want only rollback-verify ok", steps)
	}
}

func TestRbInverse(t *testing.T) {
	ctx := context.Background()
	h, tt, held := rbSetup(t, true)
	rbAdd(t, h, rbPin("c1", "done", h.clock.Now()))
	def := rbDef(t, tt)
	res, err := def.Inverse(ctx, rbEnv(h, held, "c1"), core.Effect{ChangeID: "c1", Resource: rbResource, State: core.EffectUndoing})
	if err != nil || res != (core.InverseResult{}) {
		t.Fatalf("inverse = %+v, %v; want an empty result", res, err)
	}
	rbRolledBack(t, h, tt, held, false)
	if p, ok, err := loadPin(ctx, h.db.Read(), "c1"); err != nil || !ok || p.Phase != "rolled_back" {
		t.Errorf("pin %+v, %v, %v; want phase rolled_back", p, ok, err)
	}
	if _, ok, err := activePin(ctx, h.db.Read()); err != nil || ok {
		t.Errorf("active pin after the undo: found %v, %v; want none", ok, err)
	}
}

func TestRbInverseFailures(t *testing.T) {
	ctx := context.Background()
	h, tt, held := rbSetup(t, true)
	def := rbDef(t, tt)
	if _, err := def.Inverse(ctx, rbEnv(h, held, "c5"), core.Effect{ChangeID: "c5", Resource: rbResource}); err == nil {
		t.Error("inverse of a change with no pin row succeeded, want an error")
	}
	rbAdd(t, h, rbPin("c1", "done", h.clock.Now()))
	if _, err := def.Inverse(ctx, rbEnv(h, held, "c1"), core.Effect{ChangeID: "c1", Resource: "tailscale.prefs:node"}); err == nil {
		t.Error("inverse of another resource succeeded, want an error")
	}
	if len(h.ran) != 0 {
		t.Errorf("ran %v before any valid inverse, want nothing", h.ran)
	}
	p := rbPin("c2", "done", h.clock.Now().Add(time.Hour))
	p.StagedSHA256 = strings.Repeat("0", 64)
	rbAdd(t, h, p)
	if _, err := def.Inverse(ctx, rbEnv(h, held, "c2"), core.Effect{ChangeID: "c2", Resource: rbResource}); err == nil {
		t.Error("inverse with a staged package whose SHA-256 does not match succeeded, want the rollback's error")
	}
	if got, _, _ := loadPin(ctx, h.db.Read(), "c2"); got.Phase != "failed" {
		t.Errorf("pin phase %q after a failed rollback, want failed", got.Phase)
	}
}
