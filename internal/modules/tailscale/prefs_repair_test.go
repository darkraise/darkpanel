package tailscale

import (
	"context"
	"database/sql"
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
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/tailscale/link"
	"github.com/darkraise/darkpanel/internal/platform/tslocal"
)

const (
	prSnapshotAD   = "core_changes.snapshot_ct."
	prSetAction    = "tailscale.prefs.set"
	prRepairAction = "tailscale.prefs.repair"
	// prSetInput and prSetSnapshot are the change prSeedSet stores: it set two fields, and its snapshot holds their
	// previous values.
	prSetInput    = `{"acceptRoutes":true,"shieldsUp":true}`
	prSetSnapshot = `{"acceptRoutes":false,"shieldsUp":false}`
)

func prBool(b bool) *bool       { return &b }
func prString(s string) *string { return &s }

func prPrefixes(s ...string) []netip.Prefix {
	out := []netip.Prefix{}
	for _, p := range s {
		out = append(out, netip.MustParsePrefix(p))
	}
	return out
}

// prSetup is a running node with the given preferences behind a ts whose fakes are the host's.
func prSetup(t *testing.T, prefs tslocal.Prefs) (*tsHost, ts) {
	t.Helper()
	h := newTSHost(t)
	h.local.Status = &tslocal.Status{BackendState: "Running"}
	h.local.Prefs = &prefs
	h.local.Errs = map[string]error{}
	return h, h.ts()
}

// prSeedAD stores change id the way the engine leaves a change: its redacted input and its snapshot, sealed with ad.
func prSeedAD(t *testing.T, h *tsHost, id, action, input, snapshot, ad string) {
	t.Helper()
	ct, err := coretest.Sealer{}.Seal(ad, []byte(snapshot))
	if err != nil {
		t.Fatal(err)
	}
	at := h.clock.Now().UTC().Format(time.RFC3339)
	ctx := context.Background()
	err = h.db.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO core_plans (id, host_id, action, version, actor, input_hash, state_hash,
			diff_json, guards_json, confirmation, created_at, expires_at)
			VALUES (?, 'h1', ?, 1, 'user:u1', '', '', '[]', '[]', '', ?, ?)`, "p-"+id, action, at, at); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO core_changes (id, plan_id, host_id, action, version, class, compensated_by, actor,
			state, input_json, snapshot_ct, created_at, updated_at)
			VALUES (?, ?, 'h1', ?, 1, 'revertible', '', 'user:u1', 'needs_repair', ?, ?, ?, ?)`,
			id, "p-"+id, action, input, ct, at, at)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func prSeed(t *testing.T, h *tsHost, id, action, input, snapshot string) {
	t.Helper()
	prSeedAD(t, h, id, action, input, snapshot, prSnapshotAD+id)
}

// prSeedSet stores change c1, a tailscale.prefs.set that set acceptRoutes and shieldsUp.
func prSeedSet(t *testing.T, h *tsHost) {
	t.Helper()
	prSeed(t, h, "c1", prSetAction, prSetInput, prSetSnapshot)
}

// prEnv is the environment of the call of change changeID (empty: a plan) that repairs change repairs (empty: a
// call that repairs nothing).
func prEnv(h *tsHost, changeID, repairs string) core.Env {
	env := h.env(changeID)
	env.Call.RepairOf = repairs
	return env
}

func prEffects(h *tsHost) []core.Effect { return h.journal.effects() }

// prSame compares two JSON documents as values.
func prSame(t *testing.T, a, b []byte) bool {
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

// prStale is a local API whose EditPrefs answers preferences that differ from the ones it stored.
type prStale struct{ localAPI }

func (s prStale) EditPrefs(ctx context.Context, p tslocal.PrefsPatch) (*tslocal.Prefs, error) {
	got, err := s.localAPI.EditPrefs(ctx, p)
	if err != nil {
		return nil, err
	}
	got.ShieldsUp = !got.ShieldsUp
	return got, nil
}

func TestPrActionDefinition(t *testing.T) {
	_, tt := prSetup(t, tslocal.Prefs{})
	def := tt.prefsRepair()
	s, h := def.Spec, def.Handler
	if s.Name != prRepairAction || s.Version != 1 || s.Class != core.ClassRevertible || s.Side != core.SideAgent ||
		!slices.Equal(s.LockKeys, []string{lockKey}) || s.RepairedBy != prRepairAction || s.Available == nil || s.Task || s.LongRunning {
		t.Errorf("spec %+v, want a synchronous revertible agent action locking %q, repaired by itself, with an availability check", s, lockKey)
	}
	if want := []core.GuardSpec{{ID: link.GuardID, Overridable: true}}; !reflect.DeepEqual(s.Guards, want) {
		t.Errorf("guards %+v, want %+v", s.Guards, want)
	}
	if def.InputType != reflect.TypeFor[PrefsRepairInput]() || def.OutputType != reflect.TypeFor[PrefsRepaired]() {
		t.Errorf("input %v and output %v, want PrefsRepairInput and PrefsRepaired", def.InputType, def.OutputType)
	}
	if h.Plan == nil || h.Apply == nil || h.Verify == nil || h.Snapshot == nil || h.Revert == nil || h.Observe == nil || h.Inverse != nil {
		t.Error("handler lacks Plan, Apply, Verify, Snapshot, Revert or Observe, or has an Inverse")
	}
}

func TestPrTarget(t *testing.T) {
	ctx := context.Background()
	h, _ := prSetup(t, tslocal.Prefs{})
	prSeed(t, h, "c1", prSetAction,
		`{"overrides":["tailscale.link"],"advertiseRoutes":["192.168.5.0/24","10.2.0.0/16"],"shieldsUp":true}`,
		`{"advertiseRoutes":[],"shieldsUp":false}`)
	prSeed(t, h, "c2", prRepairAction, `{"outcome":"restore"}`, `{"shieldsUp":true}`)
	prSeedAD(t, h, "c3", prSetAction, `{"shieldsUp":true}`, `{"shieldsUp":false}`, prSnapshotAD+"c8")

	action, prior, set, err := prefsRepairTarget(ctx, prEnv(h, "c9", "c1"))
	if err != nil || action != prSetAction {
		t.Fatalf("target of c1 = %q, %v", action, err)
	}
	if got, _ := json.Marshal(prior); !prSame(t, got, []byte(`{"advertiseRoutes":[],"shieldsUp":false}`)) {
		t.Errorf("prior %s, want the snapshot's fields", got)
	}
	if got, _ := json.Marshal(set); !prSame(t, got, []byte(`{"advertiseRoutes":["10.2.0.0/16","192.168.5.0/24"],"shieldsUp":true}`)) {
		t.Errorf("set %s, want the input's fields, normalised and without the guard members", got)
	}

	action, prior, set, err = prefsRepairTarget(ctx, prEnv(h, "c9", "c2"))
	if err != nil || action != prRepairAction {
		t.Fatalf("target of c2 = %q, %v", action, err)
	}
	if got, _ := json.Marshal(prior); !prSame(t, got, []byte(`{"shieldsUp":true}`)) || !reflect.DeepEqual(set, PrefsFields{}) {
		t.Errorf("prior %s and set %+v of a repair, want the snapshot and no fields", got, set)
	}

	if _, _, _, err = prefsRepairTarget(ctx, prEnv(h, "c9", "c3")); err == nil {
		t.Error("a snapshot sealed for another change was opened, want an error")
	}
	if _, _, _, err = prefsRepairTarget(ctx, prEnv(h, "c9", "c7")); errcode.From(err).Code != errcode.NotFound {
		t.Errorf("target of a change that does not exist: %v, want %s", err, errcode.NotFound)
	}
}

func TestPrPlan(t *testing.T) {
	line := func(op, text string) core.DiffLine { return core.DiffLine{Op: op, Text: text} }
	set := [2]string{prSetInput, prSetSnapshot}
	routes := [2]string{`{"advertiseRoutes":["192.168.5.0/24","10.2.0.0/16"]}`, `{"advertiseRoutes":[]}`}
	restore := []core.DiffLine{line("-", "acceptRoutes: true"), line("+", "acceptRoutes: false"), line("-", "shieldsUp: true"), line("+", "shieldsUp: false")}
	tests := []struct {
		name   string
		action string
		change [2]string // the repaired change's input and snapshot
		prefs  tslocal.Prefs
		in     string
		want   []core.DiffLine
	}{
		{"restore is the default", prSetAction, set, tslocal.Prefs{WantRunning: true, RouteAll: true, ShieldsUp: true}, `{}`, restore},
		{"restore named", prSetAction, set, tslocal.Prefs{WantRunning: true, RouteAll: true, ShieldsUp: true}, `{"outcome":"restore"}`, restore},
		{"restore with a field already back", prSetAction, set, tslocal.Prefs{ShieldsUp: true}, `{"outcome":"restore"}`,
			[]core.DiffLine{line(" ", "acceptRoutes: false (unchanged)"), line("-", "shieldsUp: true"), line("+", "shieldsUp: false")}},
		{"keep", prSetAction, set, tslocal.Prefs{RouteAll: true}, `{"outcome":"keep"}`,
			[]core.DiffLine{line(" ", "acceptRoutes: true (unchanged)"), line("-", "shieldsUp: false"), line("+", "shieldsUp: true")}},
		{"keep shows the routes sorted", prSetAction, routes, tslocal.Prefs{}, `{"outcome":"keep"}`,
			[]core.DiffLine{line("-", "advertiseRoutes: none"), line("+", "advertiseRoutes: 10.2.0.0/16, 192.168.5.0/24")}},
		{"restore the routes", prSetAction, routes, tslocal.Prefs{AdvertiseRoutes: prPrefixes("10.2.0.0/16", "192.168.5.0/24")}, `{}`,
			[]core.DiffLine{line("-", "advertiseRoutes: 10.2.0.0/16, 192.168.5.0/24"), line("+", "advertiseRoutes: none")}},
		{"restore an exit node", prSetAction, [2]string{`{"exitNode":"100.64.0.9"}`, `{"exitNode":""}`},
			tslocal.Prefs{ExitNodeIP: netip.MustParseAddr("100.64.0.9")}, `{}`,
			[]core.DiffLine{line("-", "exitNode: 100.64.0.9"), line("+", "exitNode: none")}},
		{"fields in the order of the wire shape", prSetAction,
			[2]string{`{"runSSH":true,"acceptRoutes":true,"wantRunning":false}`, `{"runSSH":false,"acceptRoutes":false,"wantRunning":true}`},
			tslocal.Prefs{RunSSH: true, RouteAll: true}, `{}`,
			[]core.DiffLine{
				line("-", "wantRunning: false"), line("+", "wantRunning: true"),
				line("-", "acceptRoutes: true"), line("+", "acceptRoutes: false"),
				line("-", "runSSH: true"), line("+", "runSSH: false"),
			}},
		{"restore a repair", prRepairAction, [2]string{`{}`, `{"shieldsUp":true}`}, tslocal.Prefs{}, `{}`,
			[]core.DiffLine{line("-", "shieldsUp: false"), line("+", "shieldsUp: true")}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, tt := prSetup(t, tc.prefs)
			prSeed(t, h, "c1", tc.action, tc.change[0], tc.change[1])
			out, err := tt.prefsRepair().Handler.Plan(context.Background(), prEnv(h, "", "c1"), json.RawMessage(tc.in))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(out.Diff, tc.want) {
				t.Errorf("diff %+v, want %+v", out.Diff, tc.want)
			}
			if len(out.Guards) != 1 || out.Guards[0].ID != link.GuardID {
				t.Errorf("guards %+v, want only %s", out.Guards, link.GuardID)
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

func TestPrPlanGuard(t *testing.T) {
	plan := func(in string) core.Guard {
		t.Helper()
		h, tt := prSetup(t, tslocal.Prefs{WantRunning: true, RunSSH: true})
		prSeed(t, h, "c1", prSetAction, `{"wantRunning":false}`, `{"wantRunning":true}`)
		out, err := tt.prefsRepair().Handler.Plan(context.Background(), prEnv(h, "", "c1"), json.RawMessage(in))
		if err != nil || len(out.Guards) != 1 {
			t.Fatalf("plan %s: guards %+v, err %v", in, out.Guards, err)
		}
		return out.Guards[0]
	}
	if g := plan(`{"outcome":"keep"}`); g.ID != link.GuardID || g.OK || g.Message == "" {
		t.Errorf("keeping the node stopped while Tailscale SSH is on: guard %+v, want a failure with a message", g)
	}
	if g := plan(`{"outcome":"restore"}`); !g.OK {
		t.Errorf("restoring the node to running: guard %+v, want it to pass", g)
	}
	if g := plan(`{"outcome":"keep","overrides":["tailscale.link"]}`); !g.OK || !g.Overridden {
		t.Errorf("overridden guard %+v, want OK and Overridden", g)
	}
}

func TestPrPlanStateHash(t *testing.T) {
	hash := func(prefs tslocal.Prefs) string {
		t.Helper()
		h, tt := prSetup(t, prefs)
		prSeedSet(t, h)
		out, err := tt.prefsRepair().Handler.Plan(context.Background(), prEnv(h, "", "c1"), json.RawMessage(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		return out.StateHash
	}
	base := hash(tslocal.Prefs{RouteAll: true, ShieldsUp: true})
	if other := hash(tslocal.Prefs{RouteAll: true}); other == base {
		t.Error("another current value of a field the repair sets has the same state hash")
	}
	if other := hash(tslocal.Prefs{RouteAll: true, ShieldsUp: true, RunSSH: true}); other != base {
		t.Error("a preference the repair does not set changed the state hash")
	}
}

func TestPrRefusals(t *testing.T) {
	seedSet := func(t *testing.T, h *tsHost) { prSeedSet(t, h) }
	tests := []struct {
		name    string
		repairs string
		seed    func(t *testing.T, h *tsHost)
		in      string
		code    errcode.Code
		path    string // the one field error's path; "" when there is none
		detail  string // a part of the detail
	}{
		{"no repaired change", "", func(*testing.T, *tsHost) {}, `{}`, errcode.InvalidInput, "", "runs only as the repair of a change"},
		{"an unknown outcome", "c1", seedSet, `{"outcome":"merge"}`, errcode.InvalidInput, "/outcome", "merge"},
		{"keep of a repair", "c1", func(t *testing.T, h *tsHost) {
			prSeed(t, h, "c1", prRepairAction, `{}`, `{"shieldsUp":true}`)
		}, `{"outcome":"keep"}`, errcode.InvalidInput, "/outcome", "change c1 is a repair; repair it with outcome restore"},
		{"a change of another action", "c1", func(t *testing.T, h *tsHost) {
			prSeed(t, h, "c1", "tailscale.node.login", `{}`, `{}`)
		}, `{}`, errcode.InvalidInput, "", "tailscale.node.login"},
		{"a change that does not exist", "c7", func(*testing.T, *tsHost) {}, `{}`, errcode.NotFound, "", "c7"},
	}
	for _, tc := range tests {
		for _, phase := range []string{"plan", "snapshot", "apply"} {
			t.Run(tc.name+" at "+phase, func(t *testing.T) {
				h, tt := prSetup(t, tslocal.Prefs{RouteAll: true, ShieldsUp: true})
				tc.seed(t, h)
				def, ctx, raw := tt.prefsRepair(), context.Background(), json.RawMessage(tc.in)
				var err error
				switch phase {
				case "plan":
					_, err = def.Handler.Plan(ctx, prEnv(h, "", tc.repairs), raw)
				case "snapshot":
					_, err = def.Handler.Snapshot(ctx, prEnv(h, "c9", tc.repairs), raw)
				default:
					_, err = def.Handler.Apply(ctx, prEnv(h, "c9", tc.repairs), raw)
				}
				e := errcode.From(err)
				if e.Code != tc.code || !core.IsDefinite(err) {
					t.Fatalf("err = %v (definite %v), want %s marked definite", err, core.IsDefinite(err), tc.code)
				}
				if !strings.Contains(e.Detail, tc.detail) {
					t.Errorf("detail %q, want it to contain %q", e.Detail, tc.detail)
				}
				if tc.path == "" && len(e.Fields) != 0 || tc.path != "" && (len(e.Fields) != 1 || e.Fields[0].Path != tc.path) {
					t.Errorf("fields %+v, want one at %q", e.Fields, tc.path)
				}
				if len(prEffects(h)) != 0 || len(h.local.Edits) != 0 {
					t.Errorf("effects %v and edits %v, want none", prEffects(h), h.local.Edits)
				}
			})
		}
	}
}

func TestPrApply(t *testing.T) {
	addr := netip.MustParseAddr("100.64.0.9")
	set := [2]string{prSetInput, prSetSnapshot}
	tests := []struct {
		name          string
		action        string
		change        [2]string
		prefs         tslocal.Prefs
		in            string
		patch         tslocal.PrefsPatch
		out           string
		before, after string
	}{
		{"restore", prSetAction, set, tslocal.Prefs{WantRunning: true, RunSSH: true, RouteAll: true, ShieldsUp: true}, `{}`,
			tslocal.PrefsPatch{RouteAll: prBool(false), ShieldsUp: prBool(false)},
			`{"repairOutcome":"restored","prefs":{"acceptRoutes":false,"shieldsUp":false}}`,
			`{"acceptRoutes":true,"shieldsUp":true}`, `{"acceptRoutes":false,"shieldsUp":false}`},
		{"keep", prSetAction, set, tslocal.Prefs{WantRunning: true, RunSSH: true}, `{"outcome":"keep"}`,
			tslocal.PrefsPatch{RouteAll: prBool(true), ShieldsUp: prBool(true)},
			`{"repairOutcome":"kept","prefs":{"acceptRoutes":true,"shieldsUp":true}}`,
			`{"acceptRoutes":false,"shieldsUp":false}`, `{"acceptRoutes":true,"shieldsUp":true}`},
		{"keep an exit node", prSetAction, [2]string{`{"exitNode":"100.64.0.9"}`, `{"exitNode":""}`}, tslocal.Prefs{}, `{"outcome":"keep"}`,
			tslocal.PrefsPatch{ExitNodeID: prString(""), ExitNodeIP: &addr},
			`{"repairOutcome":"kept","prefs":{"exitNode":"100.64.0.9"}}`,
			`{"exitNode":""}`, `{"exitNode":"100.64.0.9"}`},
		{"restore a repair", prRepairAction, [2]string{`{}`, `{"shieldsUp":true}`}, tslocal.Prefs{}, `{}`,
			tslocal.PrefsPatch{ShieldsUp: prBool(true)},
			`{"repairOutcome":"restored","prefs":{"shieldsUp":true}}`,
			`{"shieldsUp":false}`, `{"shieldsUp":true}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, tt := prSetup(t, tc.prefs)
			prSeed(t, h, "c1", tc.action, tc.change[0], tc.change[1])
			raw, err := tt.prefsRepair().Handler.Apply(context.Background(), prEnv(h, "c9", "c1"), json.RawMessage(tc.in))
			if err != nil {
				t.Fatal(err)
			}
			if len(h.local.Edits) != 1 || !reflect.DeepEqual(h.local.Edits[0], tc.patch) {
				t.Errorf("edits %+v, want exactly %+v: the patch covers the target's fields and no others", h.local.Edits, tc.patch)
			}
			if !prSame(t, raw, []byte(tc.out)) {
				t.Errorf("output %s, want %s", raw, tc.out)
			}
			effects := prEffects(h)
			if len(effects) != 1 {
				t.Fatalf("%d effects journaled, want 1", len(effects))
			}
			e := effects[0]
			if e.Kind != core.EffectModify || e.Resource != "tailscale.prefs:node" || e.Local || e.CommitWait != 2*time.Minute || e.State != core.EffectMade {
				t.Errorf("effect %+v, want a made, non-local modify of tailscale.prefs:node with a 2 minute commit wait", e)
			}
			if !prSame(t, e.Before.State, []byte(tc.before)) {
				t.Errorf("before %s, want %s", e.Before.State, tc.before)
			}
			if e.After == nil || !prSame(t, e.After.State, []byte(tc.after)) || e.Post == nil || !prSame(t, e.Post.State, []byte(tc.after)) {
				t.Errorf("after %+v and post %+v, want %s", e.After, e.Post, tc.after)
			}
		})
	}
}

func TestPrApplyRefusals(t *testing.T) {
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
			h, tt := prSetup(t, tslocal.Prefs{RouteAll: true, ShieldsUp: true})
			prSeedSet(t, h)
			h.local.Errs["EditPrefs"] = tc.err
			_, err := tt.prefsRepair().Handler.Apply(context.Background(), prEnv(h, "c9", "c1"), json.RawMessage(`{}`))
			if err == nil || core.IsDefinite(err) != tc.definite {
				t.Fatalf("err = %v (definite %v), want an error with definite %v", err, core.IsDefinite(err), tc.definite)
			}
			var coded *errcode.Error
			if tc.code != "" && (!errors.As(err, &coded) || coded.Code != tc.code) {
				t.Errorf("err = %v, want code %s", err, tc.code)
			}
			if effects := prEffects(h); len(effects) != 1 || effects[0].State != tc.state {
				t.Errorf("effects %+v, want one %s: the effect is journaled before the call", effects, tc.state)
			}
		})
	}
}

func TestPrApplyChecksWhatTailscaledAnswers(t *testing.T) {
	h, tt := prSetup(t, tslocal.Prefs{RouteAll: true, ShieldsUp: true})
	prSeedSet(t, h)
	tt.d.local = func() localAPI { return prStale{thLocal{h.local}} }
	out, err := tt.prefsRepair().Handler.Apply(context.Background(), prEnv(h, "c9", "c1"), json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "shieldsUp") || core.IsDefinite(err) {
		t.Fatalf("apply = %s, %v; want an error naming shieldsUp that is not definite: the edit committed", out, err)
	}
	if strings.Contains(string(out), "repairOutcome") {
		t.Errorf("output %s names an outcome although the preferences do not hold", out)
	}
}

func TestPrExitNodeAddressResolvesToThePeer(t *testing.T) {
	ctx := context.Background()
	h, tt := prSetup(t, tslocal.Prefs{WantRunning: true})
	h.local.Status.Peer = map[string]*tslocal.PeerStatus{
		"nodekey:e9": {ID: "nExit9", TailscaleIPs: []netip.Addr{netip.MustParseAddr("100.64.0.9")}},
	}
	prSeed(t, h, "c1", prSetAction, `{"exitNode":"100.64.0.9"}`, `{"exitNode":""}`)
	def, in, env := tt.prefsRepair(), json.RawMessage(`{"outcome":"keep"}`), prEnv(h, "c9", "c1")
	out, err := def.Handler.Apply(ctx, env, in)
	if err != nil {
		t.Fatal(err)
	}
	zero := netip.Addr{}
	if want := (tslocal.PrefsPatch{ExitNodeID: prString("nExit9"), ExitNodeIP: &zero}); len(h.local.Edits) != 1 || !reflect.DeepEqual(h.local.Edits[0], want) {
		t.Errorf("edits %+v, want exactly %+v: the kept address is sent as the peer's stable ID", h.local.Edits, want)
	}
	if !prSame(t, out, []byte(`{"repairOutcome":"kept","prefs":{"exitNode":"nExit9"}}`)) {
		t.Errorf("output %s, want the peer's stable ID", out)
	}
	if err := def.Handler.Verify(ctx, env, in, out); err != nil {
		t.Errorf("verify after keeping an exit node given by address: %v", err)
	}
}

func TestPrVerify(t *testing.T) {
	ctx := context.Background()
	h, tt := prSetup(t, tslocal.Prefs{RouteAll: true, ShieldsUp: true, RunSSH: true})
	prSeedSet(t, h)
	def, in, env := tt.prefsRepair(), json.RawMessage(`{}`), prEnv(h, "c9", "c1")
	out, err := def.Handler.Apply(ctx, env, in)
	if err != nil {
		t.Fatal(err)
	}
	if err := def.Handler.Verify(ctx, env, in, out); err != nil {
		t.Errorf("verify after apply: %v", err)
	}
	if err := def.Handler.Verify(ctx, env, in, json.RawMessage(`{"repairOutcome":"restored","prefs":{}}`)); err != nil {
		t.Errorf("verify without the output, as recovery calls it: %v", err)
	}
	h.local.Prefs.ShieldsUp = true
	if err := def.Handler.Verify(ctx, env, in, out); err == nil || !strings.Contains(err.Error(), "shieldsUp") {
		t.Errorf("verify with shields up = %v, want an error naming shieldsUp", err)
	}
	h.local.Prefs.ShieldsUp, h.local.Prefs.RouteAll = false, true
	if err := def.Handler.Verify(ctx, env, in, out); err == nil || !strings.Contains(err.Error(), "acceptRoutes") {
		t.Errorf("verify with routes accepted = %v, want an error naming acceptRoutes", err)
	}
	h.local.Errs["Prefs"] = errors.New("boom")
	if err := def.Handler.Verify(ctx, env, in, out); err == nil {
		t.Error("verify passed with the preferences unreadable, want an error")
	}
}

func TestPrSnapshotAndRevert(t *testing.T) {
	ctx := context.Background()
	start := tslocal.Prefs{WantRunning: true, RouteAll: true, ShieldsUp: true}
	h, tt := prSetup(t, start)
	prSeedSet(t, h)
	def, in, env := tt.prefsRepair(), json.RawMessage(`{}`), prEnv(h, "c9", "c1")
	snap, err := def.Handler.Snapshot(ctx, env, in)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"acceptRoutes":true,"shieldsUp":true}`; !prSame(t, snap, []byte(want)) {
		t.Errorf("snapshot %s, want the current values of the fields the repair sets: %s", snap, want)
	}
	if _, err := def.Handler.Apply(ctx, env, in); err != nil {
		t.Fatal(err)
	}
	if h.local.Prefs.RouteAll || h.local.Prefs.ShieldsUp {
		t.Fatalf("apply left the preferences %+v", *h.local.Prefs)
	}
	out, err := def.Handler.Revert(ctx, env, snap)
	if err != nil {
		t.Fatal(err)
	}
	if want := []core.EffectOutcome{{Seq: 1, State: core.EffectUndone}}; !reflect.DeepEqual(out.Effects, want) || out.Override {
		t.Errorf("outcome %+v, want the effect undone and no override", out)
	}
	if !reflect.DeepEqual(*h.local.Prefs, start) {
		t.Errorf("preferences %+v after the revert, want %+v: an unconfirmed repair reverts like any prefs.set", *h.local.Prefs, start)
	}
}

func TestPrObserve(t *testing.T) {
	ctx := context.Background()
	h, tt := prSetup(t, tslocal.Prefs{WantRunning: true, ShieldsUp: true})
	e := core.Effect{Resource: "tailscale.prefs:node", Before: core.StateDoc{State: json.RawMessage(`{"shieldsUp":false}`)}}
	obs, err := tt.prefsRepair().Handler.Observe(ctx, h.env("c9"), e)
	if err != nil || !prSame(t, obs.Current.State, []byte(`{"shieldsUp":true}`)) || obs.Matches != 1 || !obs.Exclusive {
		t.Errorf("observation %+v (%v), want only the field the effect covers, one exclusive match", obs, err)
	}
	e.Resource = "tailscale.node:self"
	if _, err := tt.prefsRepair().Handler.Observe(ctx, h.env("c9"), e); err == nil {
		t.Error("observed another resource, want an error")
	}
}
