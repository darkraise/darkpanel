package tailscale

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
)

const (
	upAptMark  = "/usr/bin/apt-mark"
	upResource = "tailscale.package:tailscale"
)

// upRunner runs vectors on the host and keeps the hold state of the tailscale package in *held, as apt-mark would.
type upRunner struct {
	h    *tsHost
	held *bool
}

func (r upRunner) Run(ctx context.Context, cmd core.Cmd) (core.CmdResult, error) {
	res, err := r.h.Run(ctx, cmd)
	if err != nil || res.ExitCode != 0 || cmd.Path != upAptMark {
		return res, err
	}
	switch strings.Join(cmd.Args, " ") {
	case "hold tailscale":
		*r.held = true
	case "unhold tailscale":
		*r.held = false
	case "showhold":
		if *r.held {
			res.Stdout = []byte("tailscale\n")
		}
	}
	return res, nil
}

// upSetup is a host with tailscale installed at version and held or not, behind a ts.
func upSetup(t *testing.T, version string, held bool) (*tsHost, ts, *bool) {
	t.Helper()
	h := newTSHost(t)
	h.reads[thDpkgPath] = thDpkgStatus(version)
	return h, h.ts(), &held
}

// upEnv is the environment of a call of change changeID (empty: a plan) whose apt-mark acts on *held.
func upEnv(h *tsHost, held *bool, changeID string) core.Env {
	env := h.env(changeID)
	env.Exec = upRunner{h: h, held: held}
	return env
}

// upPin is a pin of 1.102.0 over 1.104.1 that reached phase done.
func upPin(id string, priorHeld bool, at time.Time) pinRow {
	return pinRow{
		ChangeID: id, Version: "1.102.0", Arch: "amd64", PriorVersion: "1.104.1", PriorHeld: priorHeld, PriorBackend: "Running",
		Phase: "done", UpdatedAt: at,
	}
}

func upAdd(t *testing.T, h *tsHost, p pinRow) {
	t.Helper()
	ctx := context.Background()
	if err := h.db.Write(ctx, func(tx *sql.Tx) error { return insertPin(ctx, tx, p) }); err != nil {
		t.Fatal(err)
	}
}

func upLoad(t *testing.T, h *tsHost, id string) pinRow {
	t.Helper()
	p, ok, err := loadPin(context.Background(), h.db.Read(), id)
	if err != nil || !ok {
		t.Fatalf("pin %s: found %v, err %v", id, ok, err)
	}
	return p
}

// upHolds lists the apt-mark vectors, other than showhold, run from index from on, as "hold tailscale" or
// "unhold tailscale".
func upHolds(h *tsHost, from int) []string {
	var out []string
	for _, v := range h.ran[from:] {
		if v[0] == upAptMark && v[1] != "showhold" {
			out = append(out, strings.Join(v[1:], " "))
		}
	}
	return out
}

// upSame compares two JSON documents as values.
func upSame(t *testing.T, a, b []byte) bool {
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

func TestUpActionDefinition(t *testing.T) {
	_, tt, _ := upSetup(t, "1.102.0", true)
	def := tt.packageUnpin()
	s, h := def.Spec, def.Handler
	if s.Name != "tailscale.package.unpin" || s.Version != 1 || s.Class != core.ClassCompensable || s.Side != core.SideAgent ||
		!slices.Equal(s.LockKeys, []string{lockKey}) || !s.Converges || s.ApplyTimeout != 5*time.Minute || !s.Task || !s.LongRunning ||
		s.Available == nil || len(s.Guards) != 0 {
		t.Errorf("spec %+v, want a converging compensable agent task locking %q, with a 5 minute timeout and an availability check", s, lockKey)
	}
	if def.InputType != reflect.TypeFor[UnpinInput]() || def.OutputType != reflect.TypeFor[Unpinned]() {
		t.Errorf("input %v and output %v, want UnpinInput and Unpinned", def.InputType, def.OutputType)
	}
	if h.Plan == nil || h.Task == nil || h.Verify == nil || h.Observe == nil || h.Inverse == nil || h.Apply != nil || h.Recover != nil {
		t.Error("handler lacks Plan, Task, Verify, Observe or Inverse, or has an Apply or a Recover")
	}
}

func TestUpPlan(t *testing.T) {
	line := func(op, text string) core.DiffLine { return core.DiffLine{Op: op, Text: text} }
	release := line(" ", "release the pin of change p1: tailscale 1.102.0 stays installed")
	tests := []struct {
		name      string
		priorHeld bool
		held      bool
		want      []core.DiffLine
	}{
		{"the pin took the hold", false, true, []core.DiffLine{release, line("-", "tailscale is held"), line("+", "tailscale is not held")}},
		{"the hold was there before the pin", true, true, []core.DiffLine{release, line(" ", "tailscale is held (unchanged)")}},
		{"the hold the prior state needs was released", true, false, []core.DiffLine{release, line("-", "tailscale is not held"), line("+", "tailscale is held")}},
		{"the hold is already gone", false, false, []core.DiffLine{release, line(" ", "tailscale is not held (unchanged)")}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, tt, held := upSetup(t, "1.102.0", tc.held)
			upAdd(t, h, upPin("p1", tc.priorHeld, h.clock.Now()))
			out, err := tt.packageUnpin().Handler.Plan(context.Background(), upEnv(h, held, ""), json.RawMessage(`{}`))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(out.Diff, tc.want) || out.Confirmation != "" || len(out.Guards) != 0 || len(out.StateHash) != 64 {
				t.Errorf("plan %+v, want diff %v, no confirmation, no guards and a 64-digit hash", out, tc.want)
			}
			if got := upHolds(h, 0); len(got) != 0 {
				t.Errorf("plan changed the hold: %v", got)
			}
		})
	}
}

func TestUpPlanWithNoPin(t *testing.T) {
	h, tt, held := upSetup(t, "1.104.1", true)
	upAdd(t, h, pinRow{ChangeID: "p0", Version: "1.102.0", Arch: "amd64", PriorVersion: "1.104.1", PriorBackend: "Running", Phase: "rolled_back", UpdatedAt: h.clock.Now()})
	upAdd(t, h, func() pinRow { p := upPin("p1", false, h.clock.Now()); p.UnpinnedBy = "c0"; return p }())
	out, err := tt.packageUnpin().Handler.Plan(context.Background(), upEnv(h, held, ""), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Diff) != 0 || len(out.Guards) != 0 || out.Confirmation != "" || len(out.StateHash) != 64 {
		t.Errorf("plan %+v, want an empty diff, no guards and a 64-digit hash when no pin is active", out)
	}
	if len(h.ran) != 0 {
		t.Errorf("plan ran %v, want nothing when no pin is active", h.ran)
	}
}

func TestUpPlanStateHash(t *testing.T) {
	hash := func(pinID string, held bool, version string) string {
		t.Helper()
		h, tt, heldPtr := upSetup(t, version, held)
		upAdd(t, h, upPin(pinID, false, h.clock.Now()))
		out, err := tt.packageUnpin().Handler.Plan(context.Background(), upEnv(h, heldPtr, ""), json.RawMessage(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		return out.StateHash
	}
	base := hash("p1", true, "1.102.0")
	if again := hash("p1", true, "1.102.0"); again != base {
		t.Error("the same pin, hold and version have different state hashes")
	}
	for name, other := range map[string]string{
		"another pin":     hash("p2", true, "1.102.0"),
		"another hold":    hash("p1", false, "1.102.0"),
		"another version": hash("p1", true, "1.103.0"),
	} {
		if other == base {
			t.Errorf("%s has the same state hash", name)
		}
	}
}

func TestUpTask(t *testing.T) {
	tests := []struct {
		name      string
		priorHeld bool
		held      bool
		wantRan   []string
	}{
		{"release the hold the pin took", false, true, []string{"unhold tailscale"}},
		{"keep the hold that was there before the pin", true, true, nil},
		{"restore a hold that someone released", true, false, []string{"hold tailscale"}},
		{"the hold is already gone", false, false, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			h, tt, held := upSetup(t, "1.102.0", tc.held)
			upAdd(t, h, upPin("p1", tc.priorHeld, h.clock.Now()))
			raw, err := tt.packageUnpin().Handler.Task(ctx, upEnv(h, held, "c1"), json.RawMessage(`{}`))
			if err != nil {
				t.Fatal(err)
			}
			if want := fmt.Sprintf(`{"version":"1.102.0","held":%v}`, tc.priorHeld); string(raw) != want {
				t.Errorf("output %s, want %s", raw, want)
			}
			if got := upHolds(h, 0); !slices.Equal(got, tc.wantRan) {
				t.Errorf("hold changes %v, want %v", got, tc.wantRan)
			}
			for _, v := range h.ran {
				if v[0] != upAptMark {
					t.Errorf("ran %v, want only apt-mark: unpin installs and removes nothing", v)
				}
			}
			if *held != tc.priorHeld {
				t.Errorf("hold state %v after the task, want the pin's prior state %v", *held, tc.priorHeld)
			}
			if p := upLoad(t, h, "p1"); p.UnpinnedBy != "c1" {
				t.Errorf("pin unpinned by %q, want c1", p.UnpinnedBy)
			}
			effects := h.journal.effects()
			if len(effects) != 1 {
				t.Fatalf("%d effects journaled, want 1", len(effects))
			}
			e, want := effects[0], fmt.Sprintf(`{"held":%v}`, tc.priorHeld)
			if e.Kind != core.EffectModify || e.Resource != upResource || !e.Local || !e.InverseLocal || e.State != core.EffectMade {
				t.Errorf("effect %+v, want a made modify of %s that is local both ways", e, upResource)
			}
			if !upSame(t, e.Before.State, []byte(`{"held":true}`)) {
				t.Errorf("before %s, want the hold the pin set", e.Before.State)
			}
			if e.After == nil || !upSame(t, e.After.State, []byte(want)) || e.Post == nil || !upSame(t, e.Post.State, []byte(want)) {
				t.Errorf("after %+v and post %+v, want %s", e.After, e.Post, want)
			}
		})
	}
}

func TestUpTaskWithNoPin(t *testing.T) {
	h, tt, held := upSetup(t, "1.104.1", true)
	raw, err := tt.packageUnpin().Handler.Task(context.Background(), upEnv(h, held, "c1"), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"version":"1.104.1","held":true}` {
		t.Errorf("output %s, want the installed version and the hold as they are", raw)
	}
	if got := upHolds(h, 0); len(got) != 0 || len(h.journal.effects()) != 0 || !*held {
		t.Errorf("hold changes %v, effects %v, held %v; want nothing changed and nothing journaled", got, h.journal.effects(), *held)
	}
}

func TestUpTaskTakesTheNewestPinFirst(t *testing.T) {
	ctx := context.Background()
	h, tt, held := upSetup(t, "1.102.0", true)
	upAdd(t, h, upPin("p1", false, h.clock.Now()))
	upAdd(t, h, upPin("p2", false, h.clock.Now().Add(time.Hour)))
	def, in := tt.packageUnpin(), json.RawMessage(`{}`)
	if _, err := def.Handler.Task(ctx, upEnv(h, held, "c1"), in); err != nil {
		t.Fatal(err)
	}
	if a, b := upLoad(t, h, "p1").UnpinnedBy, upLoad(t, h, "p2").UnpinnedBy; a != "" || b != "c1" {
		t.Errorf("unpinned by %q and %q, want only the newer pin taken", a, b)
	}
	if _, err := def.Handler.Task(ctx, upEnv(h, held, "c2"), in); err != nil {
		t.Fatal(err)
	}
	if a := upLoad(t, h, "p1").UnpinnedBy; a != "c2" {
		t.Errorf("older pin unpinned by %q, want c2", a)
	}
}

func TestUpTaskFailsWhenAptMarkFails(t *testing.T) {
	h, tt, held := upSetup(t, "1.102.0", true)
	upAdd(t, h, upPin("p1", false, h.clock.Now()))
	h.script(upAptMark+" unhold tailscale", tsRun{res: core.CmdResult{ExitCode: 100, Stderr: []byte("E: cannot unhold")}})
	if _, err := tt.packageUnpin().Handler.Task(context.Background(), upEnv(h, held, "c1"), json.RawMessage(`{}`)); err == nil {
		t.Fatal("task succeeded, want the apt-mark failure")
	}
	if p := upLoad(t, h, "p1"); p.UnpinnedBy != "" {
		t.Errorf("pin unpinned by %q after a failed unhold, want it still active", p.UnpinnedBy)
	}
	if effects := h.journal.effects(); len(effects) != 1 || effects[0].State != core.EffectIntended {
		t.Errorf("effects %+v, want one intended: it is journaled before the call", effects)
	}
}

func TestUpConverges(t *testing.T) {
	ctx := context.Background()
	h, tt, held := upSetup(t, "1.102.0", true)
	upAdd(t, h, upPin("p1", false, h.clock.Now()))
	def, in := tt.packageUnpin(), json.RawMessage(`{}`)
	if _, err := def.Handler.Task(ctx, upEnv(h, held, "c1"), in); err != nil {
		t.Fatal(err)
	}
	out, err := def.Handler.Plan(ctx, upEnv(h, held, ""), in)
	if err != nil || len(out.Diff) != 0 {
		t.Errorf("plan after the task = %+v, %v; want an empty diff", out, err)
	}
}

func TestUpVerify(t *testing.T) {
	ctx := context.Background()
	h, tt, held := upSetup(t, "1.102.0", true)
	upAdd(t, h, upPin("p1", false, h.clock.Now()))
	def, in := tt.packageUnpin(), json.RawMessage(`{}`)
	if err := def.Handler.Verify(ctx, upEnv(h, held, "c2"), in, json.RawMessage(`{}`)); err != nil {
		t.Errorf("verify of a change that unpinned nothing: %v", err)
	}
	out, err := def.Handler.Task(ctx, upEnv(h, held, "c1"), in)
	if err != nil {
		t.Fatal(err)
	}
	if err := def.Handler.Verify(ctx, upEnv(h, held, "c1"), in, out); err != nil {
		t.Errorf("verify after the task: %v", err)
	}
	if err := def.Handler.Verify(ctx, upEnv(h, held, "c1"), in, json.RawMessage(`{}`)); err != nil {
		t.Errorf("verify without the output, as recovery calls it: %v", err)
	}
	*held = true
	if err := def.Handler.Verify(ctx, upEnv(h, held, "c1"), in, out); err == nil || !strings.Contains(err.Error(), "held") {
		t.Errorf("verify with the hold back = %v, want an error naming the hold", err)
	}
}

func TestUpObserve(t *testing.T) {
	ctx := context.Background()
	h, tt, held := upSetup(t, "1.102.0", true)
	def := tt.packageUnpin()
	for _, want := range []bool{true, false} {
		*held = want
		obs, err := def.Handler.Observe(ctx, upEnv(h, held, "c1"), core.Effect{Resource: upResource})
		if err != nil || !upSame(t, obs.Current.State, []byte(fmt.Sprintf(`{"held":%v}`, want))) || obs.Matches != 1 || !obs.Exclusive {
			t.Errorf("observation %+v (%v), want the hold state %v, one exclusive match", obs, err, want)
		}
	}
	if _, err := def.Handler.Observe(ctx, upEnv(h, held, "c1"), core.Effect{Resource: "tailscale.file:/etc/apt/sources.list.d/tailscale.list"}); err == nil {
		t.Error("observed another resource, want an error")
	}
	h.script(upAptMark+" showhold", tsRun{res: core.CmdResult{ExitCode: 100}})
	if _, err := def.Handler.Observe(ctx, upEnv(h, held, "c1"), core.Effect{Resource: upResource}); err == nil {
		t.Error("observed with showhold failing, want an error")
	}
}

func TestUpInverse(t *testing.T) {
	ctx := context.Background()
	e := core.Effect{ChangeID: "c1", Resource: upResource, State: core.EffectMade}
	tests := []struct {
		name      string
		priorHeld bool
		heldAfter bool // the hold when the undo starts
		wantRan   []string
	}{
		{"the pin took the hold, so the undo takes it back", false, false, []string{"hold tailscale"}},
		{"the hold stayed through the unpin", true, true, nil},
		{"someone already held it again", false, true, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, tt, held := upSetup(t, "1.102.0", true)
			upAdd(t, h, upPin("p1", tc.priorHeld, h.clock.Now()))
			def, in := tt.packageUnpin(), json.RawMessage(`{}`)
			if _, err := def.Handler.Task(ctx, upEnv(h, held, "c1"), in); err != nil {
				t.Fatal(err)
			}
			*held = tc.heldAfter
			from := len(h.ran)
			res, err := def.Handler.Inverse(ctx, upEnv(h, held, "c1"), e)
			if err != nil || res != (core.InverseResult{}) {
				t.Fatalf("inverse = %+v, %v; want an empty result", res, err)
			}
			if got := upHolds(h, from); !slices.Equal(got, tc.wantRan) {
				t.Errorf("hold changes %v, want %v", got, tc.wantRan)
			}
			if !*held {
				t.Error("the package is not held after the undo")
			}
			if p := upLoad(t, h, "p1"); p.UnpinnedBy != "" {
				t.Errorf("pin unpinned by %q after the undo, want it active again", p.UnpinnedBy)
			}
			if p, ok, err := activePin(ctx, h.db.Read()); err != nil || !ok || p.ChangeID != "p1" {
				t.Errorf("active pin = %+v, %v, %v; want p1", p, ok, err)
			}
		})
	}
}

func TestUpInverseFailures(t *testing.T) {
	ctx := context.Background()
	h, tt, held := upSetup(t, "1.102.0", true)
	upAdd(t, h, upPin("p1", false, h.clock.Now()))
	def, in := tt.packageUnpin(), json.RawMessage(`{}`)
	if _, err := def.Handler.Task(ctx, upEnv(h, held, "c1"), in); err != nil {
		t.Fatal(err)
	}
	from := len(h.ran)
	if _, err := def.Handler.Inverse(ctx, upEnv(h, held, "c1"), core.Effect{ChangeID: "c1", Resource: "tailscale.file:/x"}); err == nil || len(h.ran) != from {
		t.Errorf("inverse of another resource = %v after running %v, want an error and no command", err, h.ran[from:])
	}
	h.script(upAptMark+" hold tailscale", tsRun{res: core.CmdResult{ExitCode: 100, Stderr: []byte("E: cannot hold")}})
	if _, err := def.Handler.Inverse(ctx, upEnv(h, held, "c1"), core.Effect{ChangeID: "c1", Resource: upResource}); err == nil {
		t.Fatal("inverse succeeded, want the apt-mark failure")
	}
	if p := upLoad(t, h, "p1"); p.UnpinnedBy != "c1" {
		t.Errorf("pin unpinned by %q after a failed re-hold, want c1: the pin is active again only once the hold is back", p.UnpinnedBy)
	}
}
