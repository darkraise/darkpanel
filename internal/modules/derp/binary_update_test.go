package derp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"net/netip"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/derp/artifact"
	"github.com/darkraise/darkpanel/internal/modules/derp/server"
	"github.com/darkraise/darkpanel/internal/platform/tslocal"
)

const (
	buFrom   = "v1.99.0"
	buHostIP = "93.184.216.34"
	buPin    = "sha256-raw:0123456789abcdef"
)

var (
	buOld   = []byte("derper-old")
	buNew   = []byte("derper-new")
	buOther = []byte("derper-from-an-earlier-update")
)

func buSum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func buInput(version string) json.RawMessage {
	b, err := json.Marshal(BinaryUpdateInput{Version: version})
	if err != nil {
		panic(err)
	}
	return b
}

// buCall is one host change of the update and the phase of change c1's update row, then the state of its newest
// effect, when the change was made.
type buCall struct{ Call, State string }

type buTrace struct {
	h     *dpHost
	calls []buCall
}

func (tr *buTrace) note(ctx context.Context, call string) {
	state := "no row"
	if u, ok, _ := loadUpdate(ctx, tr.h.db.Read(), "c1"); ok {
		state = u.Phase
	}
	if effects, _ := tr.h.journal.List(ctx); len(effects) > 0 {
		state += "/" + string(effects[len(effects)-1].State)
	}
	tr.calls = append(tr.calls, buCall{call, state})
}

// buFiles traces WriteFile, Rename and Remove; a call named in fail is traced and then refused.
type buFiles struct {
	core.FileWriter
	tr   *buTrace
	fail map[string]error
}

func (f *buFiles) do(ctx context.Context, call string, run func() error) error {
	f.tr.note(ctx, call)
	if err := f.fail[call]; err != nil {
		return err
	}
	return run()
}

func (f *buFiles) WriteFile(ctx context.Context, module, path string, data []byte, mode fs.FileMode) error {
	return f.do(ctx, fmt.Sprintf("write %s %04o", path, uint32(mode.Perm())), func() error {
		return f.FileWriter.WriteFile(ctx, module, path, data, mode)
	})
}

func (f *buFiles) Rename(ctx context.Context, module, from, to string) error {
	return f.do(ctx, "rename "+from+" "+to, func() error { return f.FileWriter.Rename(ctx, module, from, to) })
}

func (f *buFiles) Remove(ctx context.Context, module, path string) error {
	return f.do(ctx, "remove "+path, func() error { return f.FileWriter.Remove(ctx, module, path) })
}

// buUnits traces Restart. The first failRestarts restarts are refused; onRestart, when set, runs after each
// successful one with the number of successful restarts so far.
type buUnits struct {
	core.Units
	tr           *buTrace
	failRestarts int
	restarts     int
	onRestart    func(n int)
}

func (u *buUnits) Restart(ctx context.Context, unit string) error {
	u.tr.note(ctx, "restart "+unit)
	if u.failRestarts > 0 {
		u.failRestarts--
		return errors.New("simulated failure of the restart")
	}
	if err := u.Units.Restart(ctx, unit); err != nil {
		return err
	}
	u.restarts++
	if u.onRestart != nil {
		u.onRestart(u.restarts)
	}
	return nil
}

// buSetup is a host whose derper is installed at buFrom, healthy, with change c1's environment; the fetch
// answers buNew.
type buSetup struct {
	h       *dpHost
	d       dp
	tr      *buTrace
	files   *buFiles
	units   *buUnits
	env     core.Env
	targets []probeTarget
	stuns   []netip.AddrPort
}

var buStunAddr = netip.MustParseAddrPort("127.0.0.1:3478")

func buNewSetup(t *testing.T) *buSetup {
	t.Helper()
	h := newDPHost(t)
	h.fetchBin = buNew
	now := h.clock.Now()
	err := insertLocalServer(context.Background(), h.db, localServer{
		ChangeID: "c0", Mode: "ip", HostName: buHostIP, DerperVersion: buFrom, BinarySHA256: buSum(buOld), CertName: buPin,
		CertNotAfter: now.Add(300 * 24 * time.Hour), InstalledAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	h.files[server.BinaryPath] = buOld
	h.owned[server.BinaryPath] = true
	h.units.States = map[string]core.UnitState{server.UnitName: {LoadState: "loaded", ActiveState: "active", SubState: "running", MainPID: 2000}}
	h.tls["127.0.0.1"] = dpProbe{d: time.Millisecond}
	h.stun[buStunAddr] = dpProbe{d: time.Millisecond}
	s := &buSetup{h: h, d: h.dp(), tr: &buTrace{h: h}}
	tlsProbe, stun := s.d.d.tlsProbe, s.d.d.stun
	s.d.d.tlsProbe = func(ctx context.Context, tg probeTarget) (time.Duration, error) {
		s.targets = append(s.targets, tg)
		return tlsProbe(ctx, tg)
	}
	s.d.d.stun = func(ctx context.Context, a netip.AddrPort) (time.Duration, error) {
		s.stuns = append(s.stuns, a)
		return stun(ctx, a)
	}
	s.env = h.env("c1")
	s.files = &buFiles{FileWriter: s.env.Files, tr: s.tr, fail: map[string]error{}}
	s.units = &buUnits{Units: s.env.Units, tr: s.tr}
	s.env.Files, s.env.Units = s.files, s.units
	return s
}

// buFilesAre reports whether the files the host holds are exactly want.
func buFilesAre(h *dpHost, want map[string][]byte) bool {
	return maps.EqualFunc(h.files, want, slices.Equal[[]byte])
}

func (s *buSetup) planEnv() core.Env { return s.h.env("") }

func (s *buSetup) effects(t *testing.T) []core.Effect {
	t.Helper()
	effects, err := s.h.journal.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return effects
}

func (s *buSetup) update(t *testing.T) updateRow {
	t.Helper()
	u, ok, err := loadUpdate(context.Background(), s.h.db.Read(), "c1")
	if err != nil || !ok {
		t.Fatalf("update row of c1: found %v, error %v", ok, err)
	}
	return u
}

func (s *buSetup) server(t *testing.T) localServer {
	t.Helper()
	row, ok, err := loadLocalServer(context.Background(), s.h.db.Read())
	if err != nil || !ok {
		t.Fatalf("derp_server row: found %v, error %v", ok, err)
	}
	return row
}

// run calls the update's task while advancing the host's clock by one poll whenever it waits.
func (s *buSetup) run(t *testing.T) (json.RawMessage, error) {
	t.Helper()
	var out json.RawMessage
	var err error
	buDrive(t, s.h, func() {
		out, err = s.d.binaryUpdate().Handler.Task(context.Background(), s.env, buInput(artifact.DerperVersion))
	})
	return out, err
}

func buDrive(t *testing.T, h *dpHost, call func()) {
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
			h.clock.Advance(binaryUpdatePoll)
		} else {
			time.Sleep(time.Millisecond)
		}
		if time.Now().After(deadline) {
			t.Fatal("the call did not finish")
		}
	}
}

func buCoded(t *testing.T, err error, code errcode.Code) *errcode.Error {
	t.Helper()
	var coded *errcode.Error
	if !errors.As(err, &coded) || coded.Code != code {
		t.Fatalf("error %v, want code %s", err, code)
	}
	return coded
}

func TestBuSpec(t *testing.T) {
	def := newDPHost(t).dp().binaryUpdate()
	sp, h := def.Spec, def.Handler
	if sp.Name != "derp.binary.update" || sp.Version != 1 || sp.Class != core.ClassCompensable || sp.Side != core.SideAgent ||
		!slices.Equal(sp.LockKeys, []string{"derp"}) || sp.ApplyTimeout != 15*time.Minute || !sp.Task || !sp.LongRunning ||
		sp.Available == nil || len(sp.Guards) != 0 || sp.Internal || sp.Converges {
		t.Errorf("spec %+v, want a long-running compensable agent task locking derp for 15 minutes, with an availability check", sp)
	}
	if def.InputType != reflect.TypeFor[BinaryUpdateInput]() || def.OutputType != reflect.TypeFor[BinaryUpdated]() {
		t.Errorf("input %v and output %v, want BinaryUpdateInput and BinaryUpdated", def.InputType, def.OutputType)
	}
	if h.Plan == nil || h.Task == nil || h.Verify == nil || h.Apply != nil {
		t.Errorf("handler has Plan %v Task %v Verify %v Apply %v, want a task handler", h.Plan != nil, h.Task != nil, h.Verify != nil, h.Apply != nil)
	}
}

func TestBuPlan(t *testing.T) {
	ctx := context.Background()
	s := buNewSetup(t)
	h := s.d.binaryUpdate().Handler
	out, err := h.Plan(ctx, s.planEnv(), buInput(artifact.DerperVersion))
	if err != nil {
		t.Fatal(err)
	}
	line := func(op, text string) core.DiffLine { return core.DiffLine{Op: op, Text: text} }
	want := []core.DiffLine{
		line("-", "derper "+buFrom),
		line("+", "derper "+artifact.DerperVersion),
		line(" ", "the previous binary is kept as "+server.BinaryPrevPath),
		line(" ", fmt.Sprintf("roll back to derper %s if the unit, the TLS probe, STUN and tailscaled are not healthy within %d s", buFrom, int(healthWindow/time.Second))),
	}
	if !slices.Equal(out.Diff, want) || out.Confirmation != "" || len(out.Guards) != 0 || len(out.StateHash) != 64 {
		t.Errorf("plan %+v, want diff %+v, no confirmation, no guard and a 64-digit hash", out, want)
	}
	if len(s.h.ran) != 0 || len(s.h.fetches) != 0 || len(s.h.units.Calls) != 0 || len(s.effects(t)) != 0 || !buFilesAre(s.h, map[string][]byte{server.BinaryPath: buOld}) {
		t.Errorf("plan ran %v, fetched %v, called units %v, journaled %v and left files %v, want nothing", s.h.ran, s.h.fetches, s.h.units.Calls, s.effects(t), s.h.files)
	}
	again, err := h.Plan(ctx, s.planEnv(), buInput(artifact.DerperVersion))
	if err != nil || again.StateHash != out.StateHash {
		t.Errorf("a second plan hash %q (error %v), want %q", again.StateHash, err, out.StateHash)
	}
	if err := setLocalBinary(ctx, s.h.db, "v1.98.0", buSum(buOld), s.h.clock.Now()); err != nil {
		t.Fatal(err)
	}
	if other, err := h.Plan(ctx, s.planEnv(), buInput(artifact.DerperVersion)); err != nil || other.StateHash == out.StateHash {
		t.Errorf("plan over another installed version: hash %q (error %v), want one different from %q", other.StateHash, err, out.StateHash)
	}
}

func TestBuRefusals(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name    string
		version string
		setup   func(t *testing.T, s *buSetup)
		code    errcode.Code
		field   string
		detail  string
	}{
		{"a version this agent does not carry", "v1.98.0", nil, errcode.InvalidInput, "/version",
			"this agent's darkpanel release carries derper " + artifact.DerperVersion + "; update the agent first"},
		{"something that is no version", "latest", nil, errcode.InvalidInput, "/version", "update the agent first"},
		{"the version already installed", artifact.DerperVersion, func(t *testing.T, s *buSetup) {
			if err := setLocalBinary(ctx, s.h.db, artifact.DerperVersion, buSum(buOld), s.h.clock.Now()); err != nil {
				t.Fatal(err)
			}
		}, errcode.InvalidInput, "/version", "already installed"},
		{"a host without a server", artifact.DerperVersion, func(t *testing.T, s *buSetup) {
			if err := deleteLocalServer(ctx, s.h.db); err != nil {
				t.Fatal(err)
			}
		}, errcode.DERPNotReady, "", "no DERP server"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := buNewSetup(t)
			if tc.setup != nil {
				tc.setup(t, s)
			}
			h := s.d.binaryUpdate().Handler
			_, planErr := h.Plan(ctx, s.planEnv(), buInput(tc.version))
			_, taskErr := h.Task(ctx, s.env, buInput(tc.version))
			for what, err := range map[string]error{"plan": planErr, "task": taskErr} {
				if !core.IsDefinite(err) {
					t.Errorf("%s: error %v is not definite", what, err)
				}
				coded := buCoded(t, err, tc.code)
				if tc.field != "" && (len(coded.Fields) != 1 || coded.Fields[0].Path != tc.field) {
					t.Errorf("%s: fields %+v, want one at %s", what, coded.Fields, tc.field)
				}
				if !strings.Contains(coded.Detail, tc.detail) {
					t.Errorf("%s: detail %q lacks %q", what, coded.Detail, tc.detail)
				}
			}
			if _, ok, _ := loadUpdate(ctx, s.h.db.Read(), "c1"); ok || len(s.effects(t)) != 0 || len(s.h.fetches) != 0 || len(s.h.ran) != 0 ||
				len(s.h.units.Calls) != 0 || !buFilesAre(s.h, map[string][]byte{server.BinaryPath: buOld}) {
				t.Errorf("a refusal left an update row (%v), effects %v, fetches %v, commands %v, unit calls %v or files %v",
					ok, s.effects(t), s.h.fetches, s.h.ran, s.h.units.Calls, s.h.files)
			}
		})
	}
}

func TestBuTaskUpdates(t *testing.T) {
	ctx := context.Background()
	s := buNewSetup(t)
	raw, err := s.run(t)
	if err != nil {
		t.Fatal(err)
	}
	var out BinaryUpdated
	if err := json.Unmarshal(raw, &out); err != nil || out != (BinaryUpdated{From: buFrom, To: artifact.DerperVersion}) {
		t.Errorf("output %s (error %v), want the old and the new version", raw, err)
	}
	if want := []string{"1.4.0 " + artifact.DerperVersion + " amd64"}; !slices.Equal(s.h.fetches, want) {
		t.Errorf("fetches %v, want %v", s.h.fetches, want)
	}
	if !buFilesAre(s.h, map[string][]byte{server.BinaryPath: buNew, server.BinaryPrevPath: buOld}) {
		t.Errorf("files %v, want the new binary in place and the old one kept as derper.prev", s.h.files)
	}
	// Each phase is recorded, and the effect journaled, before the change it covers.
	wantCalls := []buCall{
		{"write " + server.BinaryNewPath + " 0755", "staged/intended"},
		{"rename " + server.BinaryPath + " " + server.BinaryPrevPath, "kept/intended"},
		{"rename " + server.BinaryNewPath + " " + server.BinaryPath, "swapped/intended"},
		{"restart " + server.UnitName, "restarted/intended"},
	}
	if !slices.Equal(s.tr.calls, wantCalls) {
		t.Errorf("host changes with the update's phase and effect:\n%+v\nwant\n%+v", s.tr.calls, wantCalls)
	}
	wantUpdate := updateRow{
		ChangeID: "c1", FromVersion: buFrom, FromSHA256: buSum(buOld), ToVersion: artifact.DerperVersion, ToSHA256: buSum(buNew), Phase: "done",
	}
	got := s.update(t)
	if !got.UpdatedAt.Equal(s.h.clock.Now()) {
		t.Errorf("updated_at %v, want %v", got.UpdatedAt, s.h.clock.Now())
	}
	got.UpdatedAt = time.Time{}
	if got != wantUpdate {
		t.Errorf("update row %+v, want %+v", got, wantUpdate)
	}
	row := s.server(t)
	if row.DerperVersion != artifact.DerperVersion || row.BinarySHA256 != buSum(buNew) || row.CertName != buPin || row.Mode != "ip" || row.HostName != buHostIP {
		t.Errorf("derp_server row %+v, want the new version and hash and the rest as before", row)
	}
	wantTargets := []probeTarget{{HostName: buHostIP, Address: "127.0.0.1", Port: 443, CertName: buPin}}
	if !slices.Equal(s.targets, wantTargets) || !slices.Equal(s.stuns, []netip.AddrPort{buStunAddr}) {
		t.Errorf("probed %+v and %v, want one TLS probe of %+v and one STUN probe of %v", s.targets, s.stuns, wantTargets, buStunAddr)
	}
	if !slices.Equal(s.h.units.Calls, []string{"restart " + server.UnitName}) {
		t.Errorf("unit calls %v, want one restart", s.h.units.Calls)
	}
	effects := s.effects(t)
	if len(effects) != 1 {
		t.Fatalf("%d effects, want 1: %+v", len(effects), effects)
	}
	e := effects[0]
	doc := func(version string, bin []byte) string {
		return fmt.Sprintf(`{"version":%q,"sha256":%q}`, version, buSum(bin))
	}
	if e.Kind != core.EffectModify || e.Resource != "derp.binary:derper" || !e.Local || !e.InverseLocal || e.State != core.EffectMade ||
		string(e.Before.State) != doc(buFrom, buOld) || e.After == nil || string(e.After.State) != doc(artifact.DerperVersion, buNew) ||
		e.Post == nil || string(e.Post.State) != string(e.After.State) {
		t.Errorf("effect %+v, want a made, local modification of derp.binary:derper from %s to %s", e, doc(buFrom, buOld), doc(artifact.DerperVersion, buNew))
	}
	if err := s.d.binaryUpdate().Handler.Verify(ctx, s.env, buInput(artifact.DerperVersion), raw); err != nil {
		t.Errorf("verify after the update: %v", err)
	}
}

func TestBuTaskWaitsForTheHealthCheck(t *testing.T) {
	s := buNewSetup(t)
	tlsProbe := s.d.d.tlsProbe
	calls := 0
	s.d.d.tlsProbe = func(ctx context.Context, tg probeTarget) (time.Duration, error) {
		if calls++; calls <= 3 {
			return 0, errors.New("connection refused")
		}
		return tlsProbe(ctx, tg)
	}
	start := s.h.clock.Now()
	if _, err := s.run(t); err != nil {
		t.Fatal(err)
	}
	if waited := s.h.clock.Now().Sub(start); waited < 3*binaryUpdatePoll || waited >= healthWindow {
		t.Errorf("waited %v, want a few polls and less than %v", waited, healthWindow)
	}
	if s.update(t).Phase != "done" || !slices.Equal(s.h.units.Calls, []string{"restart " + server.UnitName}) {
		t.Errorf("phase %q and unit calls %v, want done after a single restart", s.update(t).Phase, s.h.units.Calls)
	}
}

func TestBuTaskFetchFailureChangesNothing(t *testing.T) {
	s := buNewSetup(t)
	s.h.fetchErr = errcode.New(errcode.UpdateNotFound, "darkpanel 1.4.0 is not a published release")
	_, err := s.run(t)
	_ = buCoded(t, err, errcode.UpdateNotFound)
	if !core.IsDefinite(err) {
		t.Errorf("error %v is not definite", err)
	}
	if _, ok, _ := loadUpdate(context.Background(), s.h.db.Read(), "c1"); ok || len(s.effects(t)) != 0 || len(s.h.units.Calls) != 0 ||
		!buFilesAre(s.h, map[string][]byte{server.BinaryPath: buOld}) {
		t.Errorf("a failed fetch left an update row (%v), effects %v, unit calls %v or files %v", ok, s.effects(t), s.h.units.Calls, s.h.files)
	}
}

// buRolledBack checks what every rolled-back update leaves: the old binary alone, the phase, the skipped effect and
// the server row as before.
func buRolledBack(t *testing.T, s *buSetup, err error, cause string) {
	t.Helper()
	coded := buCoded(t, err, errcode.DERPVerifyFailed)
	if core.IsDefinite(err) {
		t.Errorf("error %v is definite, but the update changed the host before it failed", err)
	}
	for _, want := range []string{cause, "derper is back at " + buFrom} {
		if !strings.Contains(coded.Detail, want) {
			t.Errorf("detail %q lacks %q", coded.Detail, want)
		}
	}
	if !buFilesAre(s.h, map[string][]byte{server.BinaryPath: buOld}) {
		t.Errorf("files %v, want only the old binary, back in place", s.h.files)
	}
	if u := s.update(t); u.Phase != "rolled_back" {
		t.Errorf("phase %q, want rolled_back", u.Phase)
	}
	if effects := s.effects(t); len(effects) != 1 || effects[0].State != core.EffectSkipped {
		t.Errorf("effects %+v, want the one effect skipped", effects)
	}
	if row := s.server(t); row.DerperVersion != buFrom || row.BinarySHA256 != buSum(buOld) {
		t.Errorf("derp_server row %+v, want the old version and hash", row)
	}
}

func TestBuTaskRollsBackAFailedStep(t *testing.T) {
	fail := func(call string) func(s *buSetup) {
		return func(s *buSetup) { s.files.fail[call] = errors.New("simulated failure at " + call) }
	}
	tests := []struct {
		name     string
		arm      func(s *buSetup)
		restarts int // the rollback's
	}{
		{"writing the new binary", fail(fmt.Sprintf("write %s 0755", server.BinaryNewPath)), 0},
		{"keeping the old binary", fail("rename " + server.BinaryPath + " " + server.BinaryPrevPath), 1},
		{"swapping the new binary in", fail("rename " + server.BinaryNewPath + " " + server.BinaryPath), 1},
		{"restarting derper", func(s *buSetup) { s.units.failRestarts = 1 }, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := buNewSetup(t)
			tc.arm(s)
			_, err := s.run(t)
			buRolledBack(t, s, err, "simulated failure")
			if s.units.restarts != tc.restarts {
				t.Errorf("%d restarts succeeded, want %d, the rollback's", s.units.restarts, tc.restarts)
			}
		})
	}
}

func TestBuTaskRollsBackAFailedHealthCheck(t *testing.T) {
	tests := []struct {
		name  string
		arm   func(s *buSetup)
		cause string
	}{
		{"the TLS probe", func(s *buSetup) { delete(s.h.tls, "127.0.0.1") }, "TLS probe"},
		{"the STUN probe", func(s *buSetup) { delete(s.h.stun, buStunAddr) }, "STUN probe"},
		{"tailscaled", func(s *buSetup) { s.h.local.Errs = map[string]error{"Status": errors.New("socket closed")} }, "tailscaled"},
		{"the unit", func(s *buSetup) {
			failed := core.UnitState{LoadState: "loaded", ActiveState: "failed", SubState: "failed"}
			active := s.h.units.States[server.UnitName]
			// The new binary leaves the unit failed; the restart of the rollback brings the old one up.
			s.units.onRestart = func(n int) {
				state := failed
				if n > 1 {
					state = active
				}
				s.h.units.States = map[string]core.UnitState{server.UnitName: state}
			}
		}, "not active"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := buNewSetup(t)
			tc.arm(s)
			start := s.h.clock.Now()
			_, err := s.run(t)
			buRolledBack(t, s, err, tc.cause)
			if waited := s.h.clock.Now().Sub(start); waited < healthWindow {
				t.Errorf("gave up after %v, want the whole %v", waited, healthWindow)
			}
			if want := []string{"restart " + server.UnitName, "restart " + server.UnitName}; !slices.Equal(s.h.units.Calls, want) {
				t.Errorf("unit calls %v, want the restart and the rollback's restart", s.h.units.Calls)
			}
		})
	}
}

// buHung is one Status call that hung: how long its context allowed it, and the error it ended with.
type buHung struct {
	bound time.Duration // 0 when the context had no deadline
	err   error
}

// buHungLocal is a local API whose Status fails at once until hang reports true, and then blocks until its context
// ends, sending each such call to hung.
type buHungLocal struct {
	localAPI
	hang func() bool
	hung chan buHung
}

func (l buHungLocal) Status(ctx context.Context) (*tslocal.Status, error) {
	if !l.hang() {
		return nil, errors.New("not answering yet")
	}
	var bound time.Duration
	if d, ok := ctx.Deadline(); ok {
		bound = time.Until(d)
	}
	<-ctx.Done()
	l.hung <- buHung{bound, ctx.Err()}
	return nil, ctx.Err()
}

func TestBuTaskEndsAHungPoll(t *testing.T) {
	hungLocal := func(s *buSetup, hang func() bool) chan buHung {
		hung := make(chan buHung, 16)
		local := s.d.d.local()
		s.d.d.local = func() localAPI { return buHungLocal{localAPI: local, hang: hang, hung: hung} }
		return hung
	}
	t.Run("at the end of the window", func(t *testing.T) {
		s := buNewSetup(t)
		start := s.h.clock.Now()
		hung := hungLocal(s, func() bool { return !s.h.clock.Now().Before(start.Add(healthWindow - binaryUpdatePoll)) })
		_, err := s.run(t)
		buRolledBack(t, s, err, "tailscaled")
		if waited := s.h.clock.Now().Sub(start); waited < healthWindow || waited > healthWindow+binaryUpdatePoll {
			t.Errorf("gave up after %v, want the %v window", waited, healthWindow)
		}
		if len(hung) == 0 {
			t.Fatal("no poll hung")
		}
		for len(hung) > 0 {
			// A poll started a poll or less before the window ends is allowed one poll.
			if c := <-hung; c.bound <= 0 || c.bound > binaryUpdatePoll || !errors.Is(c.err, context.DeadlineExceeded) {
				t.Errorf("a hung poll was bounded to %v and ended with %v, want at most %v and the deadline", c.bound, c.err, binaryUpdatePoll)
			}
		}
	})
	t.Run("when the task's context ends", func(t *testing.T) {
		s := buNewSetup(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		entered := make(chan struct{})
		hung := hungLocal(s, func() bool {
			close(entered)
			return true
		})
		go func() {
			<-entered
			cancel()
		}()
		var err error
		buDrive(t, s.h, func() {
			_, err = s.d.binaryUpdate().Handler.Task(ctx, s.env, buInput(artifact.DerperVersion))
		})
		buRolledBack(t, s, err, "tailscaled")
		if c := <-hung; !errors.Is(c.err, context.Canceled) {
			t.Errorf("the hung poll ended with %v, want the task's cancellation", c.err)
		}
		if want := []string{"restart " + server.UnitName, "restart " + server.UnitName}; !slices.Equal(s.h.units.Calls, want) {
			t.Errorf("unit calls %v, want the restart and the rollback's restart", s.h.units.Calls)
		}
	})
}

func TestBuTaskReportsAFailedRollback(t *testing.T) {
	s := buNewSetup(t)
	delete(s.h.tls, "127.0.0.1")
	s.files.fail["rename "+server.BinaryPrevPath+" "+server.BinaryPath] = errors.New("simulated failure of the restore")
	_, err := s.run(t)
	coded := buCoded(t, err, errcode.DERPVerifyFailed)
	for _, want := range []string{"the rollback to " + buFrom + " failed", "derper needs repair"} {
		if !strings.Contains(coded.Detail, want) {
			t.Errorf("detail %q lacks %q", coded.Detail, want)
		}
	}
	if u := s.update(t); u.Phase != "failed" || !strings.Contains(u.Result, "simulated failure of the restore") {
		t.Errorf("update row %+v, want phase failed with the restore's error as the result", u)
	}
	if effects := s.effects(t); len(effects) != 1 || effects[0].State != core.EffectIntended {
		t.Errorf("effects %+v, want the one effect still intended, for the operator", effects)
	}
	if !buFilesAre(s.h, map[string][]byte{server.BinaryPath: buNew, server.BinaryPrevPath: buOld}) {
		t.Errorf("files %v, want the swap left as it was", s.h.files)
	}
}

func TestBuRollback(t *testing.T) {
	ctx := context.Background()
	bin, prev, newer := server.BinaryPath, server.BinaryPrevPath, server.BinaryNewPath
	type files = map[string][]byte
	tests := []struct {
		name     string
		phase    string
		before   files
		after    files
		restarts int
		failure  string // "" when the rollback succeeds, else a substring of its error
	}{
		{"staged: nothing was moved", "staged", files{bin: buOld, newer: buNew}, files{bin: buOld}, 0, ""},
		{"staged, with a derper.prev left by an earlier update", "staged", files{bin: buOld, newer: buNew, prev: buOther}, files{bin: buOld, prev: buOther}, 0, ""},
		{"kept: the old binary was moved aside", "kept", files{prev: buOld, newer: buNew}, files{bin: buOld}, 1, ""},
		{"kept: the move never happened", "kept", files{bin: buOld, newer: buNew}, files{bin: buOld}, 1, ""},
		{"swapped", "swapped", files{bin: buNew, prev: buOld}, files{bin: buOld}, 1, ""},
		{"restarted", "restarted", files{bin: buNew, prev: buOld}, files{bin: buOld}, 1, ""},
		{"rolling_back after the restore", "rolling_back", files{bin: buOld}, files{bin: buOld}, 1, ""},
		{"rolling_back before the restore", "rolling_back", files{bin: buNew, prev: buOld}, files{bin: buOld}, 1, ""},
		{"derper.prev holds another binary", "swapped", files{bin: buNew, prev: buOther}, files{bin: buNew, prev: buOther}, 0, "derper.prev"},
		{"derper.prev is missing", "kept", files{newer: buNew}, files{newer: buNew}, 0, "derper.prev"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := buNewSetup(t)
			clear(s.h.files)
			for p, b := range tc.before {
				s.h.files[p] = b
			}
			row := updateRow{
				ChangeID: "c1", FromVersion: buFrom, FromSHA256: buSum(buOld), ToVersion: artifact.DerperVersion, ToSHA256: buSum(buNew),
				Phase: tc.phase, UpdatedAt: s.h.clock.Now(),
			}
			if err := insertUpdate(ctx, s.h.db, row); err != nil {
				t.Fatal(err)
			}
			after := core.StateDoc{State: json.RawMessage(`{"version":"` + artifact.DerperVersion + `"}`)}
			_, err := s.env.Effects.Intend(ctx, core.Effect{
				Kind: core.EffectModify, Resource: "derp.binary:derper", Local: true, InverseLocal: true,
				Before: core.StateDoc{State: json.RawMessage(`{"version":"` + buFrom + `"}`)}, After: &after,
			}, nil)
			if err != nil {
				t.Fatal(err)
			}
			err = binaryUpdateHandler{d: s.d}.rollback(ctx, s.env, row)
			got := s.update(t)
			effects := s.effects(t)
			if tc.failure == "" {
				if err != nil || got.Phase != "rolled_back" || effects[0].State != core.EffectSkipped {
					t.Errorf("rollback = %v, phase %q, effect %s, want success, rolled_back and a skipped effect", err, got.Phase, effects[0].State)
				}
			} else {
				if err == nil || !strings.Contains(err.Error(), tc.failure) || got.Phase != "failed" || !strings.Contains(got.Result, tc.failure) || effects[0].State != core.EffectIntended {
					t.Errorf("rollback = %v, phase %q, result %q, effect %s, want an error with %q, failed, the same result and the effect still intended",
						err, got.Phase, got.Result, effects[0].State, tc.failure)
				}
			}
			if !buFilesAre(s.h, tc.after) {
				t.Errorf("files %v, want %v", s.h.files, tc.after)
			}
			if s.units.restarts != tc.restarts {
				t.Errorf("%d restarts, want %d", s.units.restarts, tc.restarts)
			}
			if row := s.server(t); row.DerperVersion != buFrom || row.BinarySHA256 != buSum(buOld) {
				t.Errorf("derp_server row %+v, want the old version and hash", row)
			}
		})
	}
}

func TestBuRollbackWaitsForTheUnit(t *testing.T) {
	ctx := context.Background()
	s := buNewSetup(t)
	clear(s.h.files)
	s.h.files[server.BinaryPath], s.h.files[server.BinaryPrevPath] = buNew, buOld
	s.h.units.States = map[string]core.UnitState{server.UnitName: {LoadState: "loaded", ActiveState: "activating"}}
	row := updateRow{
		ChangeID: "c1", FromVersion: buFrom, FromSHA256: buSum(buOld), ToVersion: artifact.DerperVersion, ToSHA256: buSum(buNew),
		Phase: "restarted", UpdatedAt: s.h.clock.Now(),
	}
	if err := insertUpdate(ctx, s.h.db, row); err != nil {
		t.Fatal(err)
	}
	start := s.h.clock.Now()
	var err error
	buDrive(t, s.h, func() { err = binaryUpdateHandler{d: s.d}.rollback(ctx, s.env, row) })
	if err == nil || !strings.Contains(err.Error(), "not active") {
		t.Fatalf("rollback = %v, want an error saying the unit is not active", err)
	}
	if waited := s.h.clock.Now().Sub(start); waited < healthWindow {
		t.Errorf("gave up after %v, want the whole %v", waited, healthWindow)
	}
	if u := s.update(t); u.Phase != "failed" || !strings.Contains(u.Result, "not active") {
		t.Errorf("update row %+v, want phase failed with the unit's state as the result", u)
	}
	if string(s.h.files[server.BinaryPath]) != string(buOld) {
		t.Errorf("binary %q, want the old one, restored before the wait", s.h.files[server.BinaryPath])
	}
}

func TestBuVerify(t *testing.T) {
	ctx := context.Background()
	in := buInput(artifact.DerperVersion)
	updated := func(t *testing.T) *buSetup {
		t.Helper()
		s := buNewSetup(t)
		if _, err := s.run(t); err != nil {
			t.Fatal(err)
		}
		return s
	}
	t.Run("an updated server whose unit is active", func(t *testing.T) {
		s := updated(t)
		if err := s.d.binaryUpdate().Handler.Verify(ctx, s.env, in, json.RawMessage(`{}`)); err != nil {
			t.Errorf("verify without the output, as recovery calls it: %v", err)
		}
	})
	t.Run("an inactive unit", func(t *testing.T) {
		s := updated(t)
		s.h.units.States = map[string]core.UnitState{server.UnitName: {LoadState: "loaded", ActiveState: "inactive"}}
		if err := s.d.binaryUpdate().Handler.Verify(ctx, s.env, in, json.RawMessage(`{}`)); err == nil {
			t.Error("verify accepted an inactive unit")
		}
	})
	t.Run("a server still at the old version", func(t *testing.T) {
		s := updated(t)
		if err := setLocalBinary(ctx, s.h.db, buFrom, buSum(buOld), s.h.clock.Now()); err != nil {
			t.Fatal(err)
		}
		if err := s.d.binaryUpdate().Handler.Verify(ctx, s.env, in, json.RawMessage(`{}`)); err == nil {
			t.Error("verify accepted a server recorded at the old version")
		}
	})
}
