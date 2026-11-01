package tailscale

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/tailscale/link"
	"github.com/darkraise/darkpanel/internal/platform/tslocal"
)

func pfBool(b bool) *bool       { return &b }
func pfString(s string) *string { return &s }

func pfPrefixes(s ...string) []netip.Prefix {
	out := []netip.Prefix{}
	for _, p := range s {
		out = append(out, netip.MustParsePrefix(p))
	}
	return out
}

// pfSetup is a running node with prefs behind a ts whose fakes are the host's.
func pfSetup(t *testing.T, prefs tslocal.Prefs) (*tsHost, ts) {
	t.Helper()
	h := newTSHost(t)
	h.local.Status = &tslocal.Status{BackendState: "Running"}
	h.local.Prefs = &prefs
	if h.local.Errs == nil {
		h.local.Errs = map[string]error{}
	}
	if h.units.States == nil {
		h.units.States = map[string]core.UnitState{}
	}
	if h.reads == nil {
		h.reads = map[string][]byte{}
	}
	h.units.States[tailscaledUnit] = core.UnitState{LoadState: "loaded", ActiveState: "active", SubState: "running"}
	h.units.States[controllerUnit] = core.UnitState{LoadState: "not-found", ActiveState: "inactive"}
	return h, h.ts()
}

func pfEffects(t *testing.T, h *tsHost) []core.Effect {
	t.Helper()
	effects, err := h.env("c1").Effects.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return effects
}

// pfSame compares two JSON documents as values.
func pfSame(t *testing.T, a, b []byte) bool {
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

func TestPfActionDefinition(t *testing.T) {
	_, tt := pfSetup(t, tslocal.Prefs{})
	def := tt.prefsSet()
	s, h := def.Spec, def.Handler
	if s.Name != "tailscale.prefs.set" || s.Version != 1 || s.Class != core.ClassRevertible || s.Side != core.SideAgent ||
		!slices.Equal(s.LockKeys, []string{lockKey}) || s.RepairedBy != "tailscale.prefs.repair" || s.Available == nil || s.Task || s.LongRunning {
		t.Errorf("spec %+v, want a synchronous revertible agent action locking %q, repaired by tailscale.prefs.repair, with an availability check", s, lockKey)
	}
	if want := []core.GuardSpec{{ID: link.GuardID, Overridable: true}}; !reflect.DeepEqual(s.Guards, want) {
		t.Errorf("guards %+v, want %+v", s.Guards, want)
	}
	if def.InputType != reflect.TypeFor[PrefsSetInput]() || def.OutputType != reflect.TypeFor[PrefsResult]() {
		t.Errorf("input %v and output %v, want PrefsSetInput and PrefsResult", def.InputType, def.OutputType)
	}
	if h.Plan == nil || h.Apply == nil || h.Verify == nil || h.Snapshot == nil || h.Revert == nil || h.Observe == nil || h.Inverse != nil {
		t.Error("handler lacks Plan, Apply, Verify, Snapshot, Revert or Observe, or has an Inverse")
	}
}

func TestPfPlan(t *testing.T) {
	line := func(op, text string) core.DiffLine { return core.DiffLine{Op: op, Text: text} }
	exitRoutes := pfPrefixes("10.1.0.0/24", "0.0.0.0/0", "::/0")
	tests := []struct {
		name  string
		prefs tslocal.Prefs
		in    string
		want  []core.DiffLine
	}{
		{"shields up", tslocal.Prefs{WantRunning: true}, `{"shieldsUp":true}`,
			[]core.DiffLine{line("-", "shieldsUp: false"), line("+", "shieldsUp: true")}},
		{"a value already set", tslocal.Prefs{WantRunning: true}, `{"wantRunning":true}`,
			[]core.DiffLine{line(" ", "wantRunning: true (unchanged)")}},
		{"routes are shown sorted", tslocal.Prefs{AdvertiseRoutes: exitRoutes}, `{"advertiseRoutes":["192.168.5.0/24","10.2.0.0/16"]}`,
			[]core.DiffLine{line("-", "advertiseRoutes: 10.1.0.0/24"), line("+", "advertiseRoutes: 10.2.0.0/16, 192.168.5.0/24")}},
		{"no routes", tslocal.Prefs{AdvertiseRoutes: exitRoutes}, `{"advertiseRoutes":[]}`,
			[]core.DiffLine{line("-", "advertiseRoutes: 10.1.0.0/24"), line("+", "advertiseRoutes: none")}},
		{"exit node cleared", tslocal.Prefs{ExitNodeID: "nABC123CNTRL"}, `{"exitNode":""}`,
			[]core.DiffLine{line("-", "exitNode: nABC123CNTRL"), line("+", "exitNode: none")}},
		{"exit node by address", tslocal.Prefs{}, `{"exitNode":"100.64.0.9"}`,
			[]core.DiffLine{line("-", "exitNode: none"), line("+", "exitNode: 100.64.0.9")}},
		{"advertise an exit node", tslocal.Prefs{}, `{"advertiseExitNode":true}`,
			[]core.DiffLine{line("-", "advertiseExitNode: false"), line("+", "advertiseExitNode: true")}},
		{"several fields in the order of the wire shape", tslocal.Prefs{WantRunning: true}, `{"runSSH":true,"acceptRoutes":true,"wantRunning":false}`,
			[]core.DiffLine{
				line("-", "wantRunning: true"), line("+", "wantRunning: false"),
				line("-", "acceptRoutes: false"), line("+", "acceptRoutes: true"),
				line("-", "runSSH: false"), line("+", "runSSH: true"),
			}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, tt := pfSetup(t, tc.prefs)
			out, err := tt.prefsSet().Handler.Plan(context.Background(), h.env(""), json.RawMessage(tc.in))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(out.Diff, tc.want) {
				t.Errorf("diff %+v, want %+v", out.Diff, tc.want)
			}
			if len(out.Guards) != 1 || out.Guards[0].ID != link.GuardID || !out.Guards[0].OK {
				t.Errorf("guards %+v, want %s passing", out.Guards, link.GuardID)
			}
			if len(out.StateHash) != 64 || out.Confirmation != "" {
				t.Errorf("state hash %q and confirmation %q, want a 64-digit hash and none", out.StateHash, out.Confirmation)
			}
			if len(h.local.Edits) != 0 || len(h.ran) != 0 {
				t.Errorf("plan edited the preferences %v or ran %v", h.local.Edits, h.ran)
			}
		})
	}
}

func TestPfPlanGuard(t *testing.T) {
	plan := func(in string) core.Guard {
		t.Helper()
		h, tt := pfSetup(t, tslocal.Prefs{WantRunning: true, RunSSH: true})
		out, err := tt.prefsSet().Handler.Plan(context.Background(), h.env(""), json.RawMessage(in))
		if err != nil || len(out.Guards) != 1 {
			t.Fatalf("plan %s: guards %+v, err %v", in, out.Guards, err)
		}
		return out.Guards[0]
	}
	if g := plan(`{"wantRunning":false}`); g.ID != link.GuardID || g.OK || g.Message == "" {
		t.Errorf("stopping the node while Tailscale SSH is on: guard %+v, want a failure with a message", g)
	}
	if g := plan(`{"wantRunning":true}`); !g.OK {
		t.Errorf("a change that removes nothing: guard %+v, want it to pass", g)
	}
	if g := plan(`{"wantRunning":false,"overrides":["tailscale.link"]}`); !g.OK || !g.Overridden {
		t.Errorf("overridden guard %+v, want OK and Overridden", g)
	}
}

func TestPfPlanStateHash(t *testing.T) {
	hash := func(prefs tslocal.Prefs) string {
		t.Helper()
		h, tt := pfSetup(t, prefs)
		out, err := tt.prefsSet().Handler.Plan(context.Background(), h.env(""), json.RawMessage(`{"shieldsUp":true}`))
		if err != nil {
			t.Fatal(err)
		}
		return out.StateHash
	}
	base := hash(tslocal.Prefs{WantRunning: true})
	if other := hash(tslocal.Prefs{WantRunning: true, ShieldsUp: true}); other == base {
		t.Error("another current value of a field the change sets has the same state hash")
	}
	if other := hash(tslocal.Prefs{WantRunning: true, RunSSH: true}); other != base {
		t.Error("a preference the change does not set changed the state hash")
	}
}

func TestPfRefusals(t *testing.T) {
	tests := []struct {
		name, in, path string
	}{
		{"nothing set", `{}`, ""},
		{"a default route", `{"advertiseRoutes":["0.0.0.0/0"]}`, "/advertiseRoutes/0"},
		{"the IPv6 default route", `{"advertiseRoutes":["10.0.0.0/24","::/0"]}`, "/advertiseRoutes/1"},
		{"bits past the mask", `{"advertiseRoutes":["10.0.0.5/24"]}`, "/advertiseRoutes/0"},
		{"not a prefix", `{"advertiseRoutes":["lan"]}`, "/advertiseRoutes/0"},
		{"a public exit node address", `{"exitNode":"8.8.8.8"}`, "/exitNode"},
		{"an exit node that is no id", `{"exitNode":"not a node!"}`, "/exitNode"},
	}
	for _, tc := range tests {
		for _, phase := range []string{"plan", "snapshot", "apply"} {
			t.Run(tc.name+" at "+phase, func(t *testing.T) {
				h, tt := pfSetup(t, tslocal.Prefs{WantRunning: true})
				def, ctx, raw := tt.prefsSet(), context.Background(), json.RawMessage(tc.in)
				var err error
				switch phase {
				case "plan":
					_, err = def.Handler.Plan(ctx, h.env(""), raw)
				case "snapshot":
					_, err = def.Handler.Snapshot(ctx, h.env("c1"), raw)
				default:
					_, err = def.Handler.Apply(ctx, h.env("c1"), raw)
				}
				e := errcode.From(err)
				if e.Code != errcode.InvalidInput || !core.IsDefinite(err) {
					t.Fatalf("err = %v (definite %v), want %s marked definite", err, core.IsDefinite(err), errcode.InvalidInput)
				}
				if tc.path == "" && len(e.Fields) != 0 || tc.path != "" && (len(e.Fields) != 1 || e.Fields[0].Path != tc.path) {
					t.Errorf("fields %+v, want one at %q", e.Fields, tc.path)
				}
				if len(pfEffects(t, h)) != 0 || len(h.local.Edits) != 0 {
					t.Errorf("effects %v and edits %v, want none", pfEffects(t, h), h.local.Edits)
				}
			})
		}
	}
}

func TestPfApplyPatch(t *testing.T) {
	zero := netip.Addr{}
	addr := netip.MustParseAddr("100.64.0.9")
	withExit := tslocal.Prefs{AdvertiseRoutes: pfPrefixes("10.1.0.0/24", "0.0.0.0/0", "::/0")}
	tests := []struct {
		name  string
		prefs tslocal.Prefs
		in    string
		want  tslocal.PrefsPatch
		out   string
	}{
		{"one boolean", tslocal.Prefs{WantRunning: true}, `{"shieldsUp":true}`,
			tslocal.PrefsPatch{ShieldsUp: pfBool(true)}, `{"shieldsUp":true}`},
		{"accept routes is the RouteAll preference", tslocal.Prefs{}, `{"acceptRoutes":true}`,
			tslocal.PrefsPatch{RouteAll: pfBool(true)}, `{"acceptRoutes":true}`},
		{"two fields and no other", tslocal.Prefs{WantRunning: true}, `{"wantRunning":false,"runSSH":true}`,
			tslocal.PrefsPatch{WantRunning: pfBool(false), RunSSH: pfBool(true)}, `{"wantRunning":false,"runSSH":true}`},
		{"exit node by id clears the address", tslocal.Prefs{ExitNodeIP: addr}, `{"exitNode":"nABC123CNTRL"}`,
			tslocal.PrefsPatch{ExitNodeID: pfString("nABC123CNTRL"), ExitNodeIP: &zero}, `{"exitNode":"nABC123CNTRL"}`},
		{"exit node by address clears the id", tslocal.Prefs{ExitNodeID: "nABC123CNTRL"}, `{"exitNode":"100.64.0.9"}`,
			tslocal.PrefsPatch{ExitNodeID: pfString(""), ExitNodeIP: &addr}, `{"exitNode":"100.64.0.9"}`},
		{"clear the exit node", tslocal.Prefs{ExitNodeID: "nABC123CNTRL"}, `{"exitNode":""}`,
			tslocal.PrefsPatch{ExitNodeID: pfString(""), ExitNodeIP: &zero}, `{"exitNode":""}`},
		{"routes keep the exit node advertisement", withExit, `{"advertiseRoutes":["192.168.5.0/24","10.2.0.0/16"]}`,
			tslocal.PrefsPatch{AdvertiseRoutes: &[]netip.Prefix{netip.MustParsePrefix("10.2.0.0/16"), netip.MustParsePrefix("192.168.5.0/24"), netip.MustParsePrefix("0.0.0.0/0"), netip.MustParsePrefix("::/0")}},
			`{"advertiseRoutes":["10.2.0.0/16","192.168.5.0/24"]}`},
		{"the exit node advertisement keeps the routes", tslocal.Prefs{AdvertiseRoutes: pfPrefixes("10.1.0.0/24")}, `{"advertiseExitNode":true}`,
			tslocal.PrefsPatch{AdvertiseRoutes: &[]netip.Prefix{netip.MustParsePrefix("10.1.0.0/24"), netip.MustParsePrefix("0.0.0.0/0"), netip.MustParsePrefix("::/0")}},
			`{"advertiseExitNode":true}`},
		{"stop advertising the exit node", withExit, `{"advertiseExitNode":false}`,
			tslocal.PrefsPatch{AdvertiseRoutes: &[]netip.Prefix{netip.MustParsePrefix("10.1.0.0/24")}}, `{"advertiseExitNode":false}`},
		{"no routes at all", withExit, `{"advertiseRoutes":[]}`,
			tslocal.PrefsPatch{AdvertiseRoutes: &[]netip.Prefix{netip.MustParsePrefix("0.0.0.0/0"), netip.MustParsePrefix("::/0")}}, `{"advertiseRoutes":[]}`},
		{"routes and exit node together", tslocal.Prefs{}, `{"advertiseRoutes":["10.3.0.0/24"],"advertiseExitNode":true}`,
			tslocal.PrefsPatch{AdvertiseRoutes: &[]netip.Prefix{netip.MustParsePrefix("10.3.0.0/24"), netip.MustParsePrefix("0.0.0.0/0"), netip.MustParsePrefix("::/0")}},
			`{"advertiseRoutes":["10.3.0.0/24"],"advertiseExitNode":true}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, tt := pfSetup(t, tc.prefs)
			raw, err := tt.prefsSet().Handler.Apply(context.Background(), h.env("c1"), json.RawMessage(tc.in))
			if err != nil {
				t.Fatal(err)
			}
			if len(h.local.Edits) != 1 || !reflect.DeepEqual(h.local.Edits[0], tc.want) {
				t.Errorf("edits %+v, want exactly %+v", h.local.Edits, tc.want)
			}
			var res PrefsResult
			if err := json.Unmarshal(raw, &res); err != nil {
				t.Fatal(err)
			}
			if got, _ := json.Marshal(res.Prefs); !pfSame(t, got, []byte(tc.out)) {
				t.Errorf("output prefs %s, want exactly the fields set: %s", got, tc.out)
			}
		})
	}
}

func TestPfApplyJournalsTheChange(t *testing.T) {
	h, tt := pfSetup(t, tslocal.Prefs{WantRunning: true})
	if _, err := tt.prefsSet().Handler.Apply(context.Background(), h.env("c1"), json.RawMessage(`{"shieldsUp":true,"runSSH":true}`)); err != nil {
		t.Fatal(err)
	}
	effects := pfEffects(t, h)
	if len(effects) != 1 {
		t.Fatalf("%d effects journaled, want 1", len(effects))
	}
	e := effects[0]
	if e.Kind != core.EffectModify || e.Resource != "tailscale.prefs:node" || e.Local || e.CommitWait != 2*time.Minute || e.State != core.EffectMade {
		t.Errorf("effect %+v, want a made, non-local modify of tailscale.prefs:node with a 2 minute commit wait", e)
	}
	if !pfSame(t, e.Before.State, []byte(`{"shieldsUp":false,"runSSH":false}`)) {
		t.Errorf("before %s, want the previous values of the two fields", e.Before.State)
	}
	if e.After == nil || !pfSame(t, e.After.State, []byte(`{"shieldsUp":true,"runSSH":true}`)) {
		t.Errorf("after %+v, want the two requested values", e.After)
	}
	if e.Post == nil || !pfSame(t, e.Post.State, []byte(`{"shieldsUp":true,"runSSH":true}`)) {
		t.Errorf("post %+v, want the two values now", e.Post)
	}
}

func TestPfApplyRefusals(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		code     errcode.Code // "" for an error that is not coded
		definite bool
		state    core.EffectState
	}{
		{"tailscaled refuses the edit", &tslocal.APIError{StatusCode: 400, Message: "bad route"}, errcode.InvalidInput, true, core.EffectSkipped},
		{"tailscaled is not reachable", fmt.Errorf("dial: %w", tslocal.ErrUnavailable), errcode.TailscaleUnavailable, true, core.EffectSkipped},
		{"tailscaled fails", &tslocal.APIError{StatusCode: 500, Message: "boom"}, "", false, core.EffectIntended},
		{"the call fails without an answer", errors.New("connection reset"), "", false, core.EffectIntended},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, tt := pfSetup(t, tslocal.Prefs{WantRunning: true})
			h.local.Errs["EditPrefs"] = tc.err
			_, err := tt.prefsSet().Handler.Apply(context.Background(), h.env("c1"), json.RawMessage(`{"shieldsUp":true}`))
			if err == nil || core.IsDefinite(err) != tc.definite {
				t.Fatalf("err = %v (definite %v), want an error with definite %v", err, core.IsDefinite(err), tc.definite)
			}
			var coded *errcode.Error
			if tc.code != "" && (!errors.As(err, &coded) || coded.Code != tc.code) {
				t.Errorf("err = %v, want code %s", err, tc.code)
			}
			if effects := pfEffects(t, h); len(effects) != 1 || effects[0].State != tc.state {
				t.Errorf("effects %+v, want one %s: the effect is journaled before the call", effects, tc.state)
			}
		})
	}
}

func TestPfVerify(t *testing.T) {
	ctx := context.Background()
	h, tt := pfSetup(t, tslocal.Prefs{WantRunning: true, AdvertiseRoutes: pfPrefixes("10.1.0.0/24")})
	def, in := tt.prefsSet(), json.RawMessage(`{"shieldsUp":true,"advertiseRoutes":["10.9.0.0/24"],"exitNode":"100.64.0.9"}`)
	out, err := def.Handler.Apply(ctx, h.env("c1"), in)
	if err != nil {
		t.Fatal(err)
	}
	if err := def.Handler.Verify(ctx, h.env("c1"), in, out); err != nil {
		t.Errorf("verify after apply: %v", err)
	}
	if err := def.Handler.Verify(ctx, h.env("c1"), in, json.RawMessage(`{"prefs":{}}`)); err != nil {
		t.Errorf("verify without the output, as recovery calls it: %v", err)
	}
	h.local.Prefs.ShieldsUp = false
	err = def.Handler.Verify(ctx, h.env("c1"), in, out)
	if err == nil || !strings.Contains(err.Error(), "shieldsUp") {
		t.Errorf("verify with shields down = %v, want an error naming shieldsUp", err)
	}
	h.local.Prefs.ShieldsUp = true
	h.local.Prefs.AdvertiseRoutes = nil
	if err := def.Handler.Verify(ctx, h.env("c1"), in, out); err == nil {
		t.Error("verify passed with the routes gone, want an error")
	}
	h.local.Errs["Prefs"] = errors.New("boom")
	if err := def.Handler.Verify(ctx, h.env("c1"), in, out); err == nil {
		t.Error("verify passed with the preferences unreadable, want an error")
	}
}

func TestPfRevert(t *testing.T) {
	ctx := context.Background()
	start := tslocal.Prefs{WantRunning: true, RouteAll: true, AdvertiseRoutes: pfPrefixes("10.1.0.0/24", "0.0.0.0/0", "::/0"), RunSSH: true}
	h, tt := pfSetup(t, start)
	def := tt.prefsSet()
	in := json.RawMessage(`{"wantRunning":false,"acceptRoutes":false,"advertiseExitNode":false,"exitNode":"100.64.0.9","shieldsUp":true}`)
	snap, err := def.Handler.Snapshot(ctx, h.env("c1"), in)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"wantRunning":true,"advertiseExitNode":true,"acceptRoutes":true,"exitNode":"","shieldsUp":false}`; !pfSame(t, snap, []byte(want)) {
		t.Errorf("snapshot %s, want the previous values of exactly the fields set: %s", snap, want)
	}
	if _, err := def.Handler.Apply(ctx, h.env("c1"), in); err != nil {
		t.Fatal(err)
	}
	if h.local.Prefs.WantRunning || !h.local.Prefs.ShieldsUp {
		t.Fatalf("apply left the preferences %+v", *h.local.Prefs)
	}
	out, err := def.Handler.Revert(ctx, h.env("c1"), snap)
	if err != nil {
		t.Fatal(err)
	}
	if want := []core.EffectOutcome{{Seq: 1, State: core.EffectUndone}}; !reflect.DeepEqual(out.Effects, want) || out.Override {
		t.Errorf("outcome %+v, want the effect undone and no override", out)
	}
	if !reflect.DeepEqual(*h.local.Prefs, start) {
		t.Errorf("preferences %+v after the revert, want %+v", *h.local.Prefs, start)
	}
	if len(h.local.Edits) != 2 {
		t.Errorf("%d edits, want the apply's and the revert's", len(h.local.Edits))
	}
}

func TestPfRevertLeavesAConflict(t *testing.T) {
	ctx := context.Background()
	h, tt := pfSetup(t, tslocal.Prefs{WantRunning: true})
	def, in := tt.prefsSet(), json.RawMessage(`{"shieldsUp":true}`)
	snap, err := def.Handler.Snapshot(ctx, h.env("c1"), in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := def.Handler.Apply(ctx, h.env("c1"), in); err != nil {
		t.Fatal(err)
	}
	h.local.Prefs.ShieldsUp = false // another operator's edit after the change
	out, err := def.Handler.Revert(ctx, h.env("c1"), snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Effects) != 1 || out.Effects[0].Seq != 1 || out.Effects[0].State != core.EffectUnresolved ||
		out.Effects[0].Reason != core.ReasonConflict || out.Effects[0].Current == nil ||
		!pfSame(t, out.Effects[0].Current.State, []byte(`{"shieldsUp":false}`)) {
		t.Errorf("outcome %+v, want effect 1 unresolved as a conflict, with the state it found", out)
	}
	if len(h.local.Edits) != 1 {
		t.Errorf("%d edits, want only the apply's: a conflict is left in place", len(h.local.Edits))
	}
}

func TestPfRevertRefusesAnEffectInDoubt(t *testing.T) {
	ctx := context.Background()
	h, tt := pfSetup(t, tslocal.Prefs{WantRunning: true})
	def, in := tt.prefsSet(), json.RawMessage(`{"shieldsUp":true}`)
	snap, err := def.Handler.Snapshot(ctx, h.env("c1"), in)
	if err != nil {
		t.Fatal(err)
	}
	h.local.Errs["EditPrefs"] = errors.New("connection reset")
	if _, err := def.Handler.Apply(ctx, h.env("c1"), in); err == nil {
		t.Fatal("apply succeeded, want the failed call")
	}
	delete(h.local.Errs, "EditPrefs")
	if _, err := def.Handler.Revert(ctx, h.env("c1"), snap); err == nil {
		t.Error("revert succeeded over an intended effect, want an error")
	}
	if len(h.local.Edits) != 1 {
		t.Errorf("%d edits, want only the failed apply's: an effect in doubt blocks every write", len(h.local.Edits))
	}
}

func TestPfRevertWithNothingMade(t *testing.T) {
	ctx := context.Background()
	h, tt := pfSetup(t, tslocal.Prefs{WantRunning: true})
	def, in := tt.prefsSet(), json.RawMessage(`{"shieldsUp":true}`)
	snap, err := def.Handler.Snapshot(ctx, h.env("c1"), in)
	if err != nil {
		t.Fatal(err)
	}
	h.local.Errs["EditPrefs"] = &tslocal.APIError{StatusCode: 400, Message: "no"}
	if _, err := def.Handler.Apply(ctx, h.env("c1"), in); !core.IsDefinite(err) {
		t.Fatalf("apply = %v, want a definite refusal", err)
	}
	delete(h.local.Errs, "EditPrefs")
	out, err := def.Handler.Revert(ctx, h.env("c1"), snap)
	if err != nil || len(out.Effects) != 0 || out.Override {
		t.Errorf("revert = %+v, %v, want an empty outcome", out, err)
	}
	if len(h.local.Edits) != 1 {
		t.Errorf("%d edits, want only the refused apply's", len(h.local.Edits))
	}
}

func TestPfObserve(t *testing.T) {
	ctx := context.Background()
	h, tt := pfSetup(t, tslocal.Prefs{WantRunning: true, ShieldsUp: true})
	def := tt.prefsSet()
	e := core.Effect{Resource: "tailscale.prefs:node", Before: core.StateDoc{State: json.RawMessage(`{"runSSH":false}`)}}
	obs, err := def.Handler.Observe(ctx, h.env("c1"), e)
	if err != nil || !pfSame(t, obs.Current.State, []byte(`{"runSSH":false}`)) || obs.Matches != 1 || !obs.Exclusive {
		t.Errorf("observation %+v (%v), want only the field the effect covers, one exclusive match", obs, err)
	}
	e.Resource = "tailscale.node:self"
	if _, err := def.Handler.Observe(ctx, h.env("c1"), e); err == nil {
		t.Error("observed another resource, want an error")
	}
	e.Resource = "tailscale.prefs:node"
	h.local.Errs["Prefs"] = errors.New("boom")
	if _, err := def.Handler.Observe(ctx, h.env("c1"), e); err == nil {
		t.Error("observed with the preferences unreadable, want an error")
	}
}
