package cloudflare

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/cloudflare/ingress"
)

const trConfig = `{"ingress":[{"hostname":"app.example.com","service":"http://127.0.0.1:8080"},{"service":"http_status:404"}]}`

func trConfigHash(t *testing.T, raw string) string {
	t.Helper()
	var cfg ingress.Config
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatal(err)
	}
	return ingress.Hash(cfg)
}

func trRow(t *testing.T, r trRig, id string) tunnelRow {
	t.Helper()
	row, found, err := loadTunnel(context.Background(), r.h.db.Read(), id)
	if err != nil || !found {
		t.Fatalf("row %s: found %v, err %v", id, found, err)
	}
	return row
}

func trAdoptDoc(hash string, drift bool) string {
	return fmt.Sprintf(`{"hash":%q,"drift":%v}`, hash, drift)
}

func TestTrAdoptDeclarations(t *testing.T) {
	s := trNew(t).c.tunnelAdopt().Spec
	if s.Name != "cloudflare.tunnel.adopt" || s.Version != 1 || s.Class != core.ClassCompensable || s.Side != core.SideController ||
		!slices.Equal(s.LockKeys, []string{"cloudflare"}) || s.CompensatedBy != "" || s.ForwardOnly {
		t.Errorf("spec %+v", s)
	}
	h := trNew(t).c.tunnelAdopt().Handler
	if h.Observe == nil || h.Inverse == nil {
		t.Error("adopt needs Observe and Inverse handlers")
	}
}

func TestTrAdoptPlan(t *testing.T) {
	r := trNew(t)
	row := trTunnel(t, r.h, r.srv, "darkpanel-a")
	r.srv.SetConfiguration(row.ID, json.RawMessage(trConfig))
	want := trConfigHash(t, trConfig)
	def := r.c.tunnelAdopt()
	plan := func(ref string) core.PlanOutcome {
		t.Helper()
		out, err := def.Handler.Plan(context.Background(), r.h.env(""), trInput(ref))
		if err != nil {
			t.Fatalf("plan %q: %v", ref, err)
		}
		return out
	}

	out := plan(row.Name)
	if len(out.Diff) != 1 || out.Diff[0].Op != "+" || !strings.Contains(out.Diff[0].Text, want) || out.StateHash == "" {
		t.Errorf("first adoption: %+v, want one + line holding %s", out, want)
	}
	if got := trRow(t, r, row.ID); got.ConfigHash != "" {
		t.Errorf("planning stored hash %q", got.ConfigHash)
	}

	trWrite(t, r.h, func(ctx context.Context, tx *sql.Tx) error { return setTunnelHash(ctx, tx, row.ID, "stale-hash") })
	out = plan(row.ID)
	if len(out.Diff) != 2 || out.Diff[0].Op != "-" || !strings.Contains(out.Diff[0].Text, "stale-hash") ||
		out.Diff[1].Op != "+" || !strings.Contains(out.Diff[1].Text, want) {
		t.Errorf("changed hash: %+v, want a - line with the stored hash and a + line with %s", out.Diff, want)
	}
	changed := out.StateHash

	trWrite(t, r.h, func(ctx context.Context, tx *sql.Tx) error { return setTunnelHash(ctx, tx, row.ID, want) })
	out = plan(row.ID)
	if len(out.Diff) != 1 || out.Diff[0].Op != " " || !strings.Contains(out.Diff[0].Text, "already matches") {
		t.Errorf("matching hash: %+v, want one context line saying it already matches", out.Diff)
	}
	if out.StateHash == changed {
		t.Error("the state hash must change with the stored hash")
	}

	r.srv.SetConfiguration(row.ID, json.RawMessage(`{"ingress":[{"service":"http_status:404"}]}`))
	if again := plan(row.ID); again.StateHash == out.StateHash {
		t.Error("the state hash must change with the tunnel's configuration")
	}
}

func TestTrAdoptRefusals(t *testing.T) {
	r := trNew(t)
	row := trTunnel(t, r.h, r.srv, "darkpanel-a")
	def := r.c.tunnelAdopt()
	for _, tc := range []struct {
		name   string
		env    core.Env
		tunnel string
		want   errcode.Code
	}{
		{"empty", r.h.env(""), "", errcode.InvalidInput},
		{"unmanaged", r.h.env(""), "darkpanel-other", errcode.NotFound},
	} {
		_, err := def.Handler.Plan(context.Background(), tc.env, trInput(tc.tunnel))
		if trCode(err) != tc.want || !core.IsDefinite(err) {
			t.Errorf("%s: error %v, want a definite %s", tc.name, err, tc.want)
		}
	}
	bare := newCFHost(t)
	trInsertRow(t, bare, tunnelRow{ID: row.ID, Name: row.Name, ChangeID: "tr-seed", CreatedAt: bare.clock.Now()})
	if _, err := def.Handler.Plan(context.Background(), bare.env(""), trInput(row.ID)); trCode(err) != errcode.CloudNotConnected {
		t.Errorf("no account: error %v, want cloud.not_connected", err)
	}
}

func TestTrAdoptApplyAndUndo(t *testing.T) {
	r := trNew(t)
	row := trTunnel(t, r.h, r.srv, "darkpanel-a")
	trWrite(t, r.h, func(ctx context.Context, tx *sql.Tx) error {
		return setTunnelCheck(ctx, tx, row.ID, true, r.h.clock.Now())
	})
	r.srv.SetConfiguration(row.ID, json.RawMessage(trConfig))
	want := trConfigHash(t, trConfig)
	def := r.c.tunnelAdopt()
	ctx, env := context.Background(), r.h.env("chg-adopt")

	raw, err := def.Handler.Apply(ctx, env, trInput(row.ID))
	if err != nil {
		t.Fatal(err)
	}
	var ref TunnelRef
	if err := json.Unmarshal(raw, &ref); err != nil || ref != (TunnelRef{ID: row.ID, Name: row.Name}) {
		t.Errorf("output %s (err %v)", raw, err)
	}
	got := trRow(t, r, row.ID)
	if got.ConfigHash != want || got.Drift {
		t.Errorf("row after adopt: hash %q drift %v, want %q and no drift", got.ConfigHash, got.Drift, want)
	}
	effs := r.h.journal.effects()
	if len(effs) != 1 {
		t.Fatalf("%d effects, want 1", len(effs))
	}
	e := effs[0]
	if e.Kind != core.EffectModify || e.Resource != "cloudflare.tunnelhash:"+row.ID || e.State != core.EffectMade || !e.Local || !e.InverseLocal {
		t.Errorf("effect %+v", e)
	}
	trSameJSON(t, "before", e.Before.State, trAdoptDoc("", true))
	if e.After == nil || e.Post == nil {
		t.Fatalf("after %v, post %v", e.After, e.Post)
	}
	trSameJSON(t, "after", e.After.State, trAdoptDoc(want, false))
	trSameJSON(t, "post", e.Post.State, trAdoptDoc(want, false))
	if err := def.Handler.Verify(ctx, env, trInput(row.ID), raw); err != nil {
		t.Errorf("verify: %v", err)
	}

	obs, err := def.Handler.Observe(ctx, env, e)
	if err != nil {
		t.Fatal(err)
	}
	trSameJSON(t, "state before the inverse", obs.Current.State, string(e.Post.State))
	e.State = core.EffectUndoing
	if _, err := def.Handler.Inverse(ctx, env, e); err != nil {
		t.Fatal(err)
	}
	obs, err = def.Handler.Observe(ctx, env, e)
	if err != nil {
		t.Fatal(err)
	}
	trSameJSON(t, "state after the inverse", obs.Current.State, string(e.Before.State))
	if got := trRow(t, r, row.ID); got.ConfigHash != "" || !got.Drift {
		t.Errorf("row after undo: hash %q drift %v, want the previous empty hash and drift", got.ConfigHash, got.Drift)
	}
}

func TestTrAdoptObserveAndVerify(t *testing.T) {
	r := trNew(t)
	row := trTunnel(t, r.h, r.srv, "darkpanel-a")
	def := r.c.tunnelAdopt()
	ctx, env := context.Background(), r.h.env("chg-adopt")

	gone, err := def.Handler.Observe(ctx, env, core.Effect{Kind: core.EffectModify, Resource: "cloudflare.tunnelhash:tun-gone"})
	if err != nil {
		t.Fatal(err)
	}
	trSameJSON(t, "absent row", gone.Current.State, "null")
	if _, err := def.Handler.Inverse(ctx, env, core.Effect{
		Kind: core.EffectModify, Resource: "cloudflare.tunnelhash:tun-gone", Before: core.StateDoc{State: json.RawMessage(trAdoptDoc("", false))},
	}); trCode(err) != errcode.NotFound || !core.IsDefinite(err) {
		t.Errorf("inverse on an absent row: error %v, want a definite not_found", err)
	}

	if err := def.Handler.Verify(ctx, env, trInput(row.ID), json.RawMessage(`{}`)); err == nil {
		t.Error("verify passed with no recorded adoption")
	}
	e := core.Effect{
		Kind: core.EffectModify, Resource: "cloudflare.tunnelhash:" + row.ID, Local: true,
		Before: core.StateDoc{State: json.RawMessage(trAdoptDoc("", false))},
	}
	seq, err := env.Effects.Intend(ctx, e, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := env.Effects.Made(ctx, seq, e.Resource, core.StateDoc{State: json.RawMessage(trAdoptDoc("other-hash", false))}); err != nil {
		t.Fatal(err)
	}
	if err := def.Handler.Verify(ctx, env, trInput(row.ID), json.RawMessage(`{}`)); err == nil {
		t.Error("verify passed although the stored state differs from the recorded one")
	}
}
