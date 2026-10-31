package cloudflare

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/controller/cloud"
	"github.com/darkraise/darkpanel/internal/controller/cloud/cloudfake"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/cloudflare/fake"
)

// tuRig is a connected account, a fake Cloudflare account and the module under test.
type tuRig struct {
	h   *cfHost
	srv *fake.Server
	c   cf
}

func tuNew(t *testing.T) tuRig {
	t.Helper()
	h := newCFHost(t)
	srv := fake.New(t)
	acConnect(t, h, srv)
	return tuRig{h: h, srv: srv, c: cfWith(srv)}
}

// tuTunnel puts a tunnel into the fake and its row into the database, as a finished tunnel.create leaves them.
func tuTunnel(t *testing.T, h *cfHost, srv *fake.Server) tunnelRow {
	t.Helper()
	const name = "darkpanel-seed"
	row := tunnelRow{ID: srv.AddTunnel(name), Name: name, ChangeID: "seed", CreatedAt: h.clock.Now()}
	err := h.db.Write(context.Background(), func(tx *sql.Tx) error { return insertTunnel(context.Background(), tx, row) })
	if err != nil {
		t.Fatal(err)
	}
	return row
}

// tuCollection is the request path of the tunnel collection, taken from the fake's request log so the test does
// not depend on the API prefix the client uses.
func tuCollection(t *testing.T, r tuRig) string {
	t.Helper()
	a, _, err := r.c.connected(context.Background(), r.h.db, r.h.env("").Sealer, r.h.clock)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Tunnels(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	reqs := r.srv.Cloud().Requests()
	return reqs[len(reqs)-1].Path
}

func tuAddHostname(t *testing.T, h *cfHost, tunnelID, hostname string) {
	t.Helper()
	row := hostnameRow{
		Hostname: hostname, TunnelID: tunnelID, Service: "http://127.0.0.1:8080", ZoneID: "z1", RecordID: "r1",
		Phase: "active", ChangeID: "tu-host", Health: "unknown", UpdatedAt: h.clock.Now(),
	}
	err := h.db.Write(context.Background(), func(tx *sql.Tx) error { return insertHostname(context.Background(), tx, row) })
	if err != nil {
		t.Fatal(err)
	}
}

func tuRemoveHostname(t *testing.T, h *cfHost, hostname string) {
	t.Helper()
	err := h.db.Write(context.Background(), func(tx *sql.Tx) error { return deleteHostname(context.Background(), tx, hostname) })
	if err != nil {
		t.Fatal(err)
	}
}

func tuInput(tunnel string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"tunnel":%q}`, tunnel))
}

func (r tuRig) apply(def core.ActionDef, changeID string, in json.RawMessage) (TunnelRef, error) {
	raw, err := def.Handler.Apply(context.Background(), r.h.env(changeID), in)
	var ref TunnelRef
	if err == nil {
		if err := json.Unmarshal(raw, &ref); err != nil {
			return TunnelRef{}, err
		}
	}
	return ref, err
}

func tuSameJSON(t *testing.T, what string, got json.RawMessage, want string) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatalf("%s: %q is not JSON: %v", what, got, err)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(g, w) {
		t.Errorf("%s = %s, want %s", what, got, want)
	}
}

func tuCode(err error) errcode.Code {
	if err == nil {
		return ""
	}
	return errcode.From(err).Code
}

func tuDoc(id, name string) string { return fmt.Sprintf(`{"id":%q,"name":%q}`, id, name) }

// tuUndo runs the engine's undo protocol on one made effect: the observation must equal the recorded post-state
// before the inverse and the undo target after it.
func tuUndo(t *testing.T, r tuRig, def core.ActionDef, e core.Effect) error {
	t.Helper()
	ctx, env := context.Background(), r.h.env("chg-undo")
	obs, err := def.Handler.Observe(ctx, env, e)
	if err != nil {
		t.Fatal(err)
	}
	tuSameJSON(t, "state before the inverse", obs.Current.State, string(e.Post.State))
	e.State = core.EffectUndoing
	if _, err := def.Handler.Inverse(ctx, env, e); err != nil {
		return err
	}
	obs, err = def.Handler.Observe(ctx, env, e)
	if err != nil {
		t.Fatal(err)
	}
	tuSameJSON(t, "state after the inverse", obs.Current.State, "null")
	return nil
}

func TestTuDeclarations(t *testing.T) {
	r := tuNew(t)
	for _, tc := range []struct {
		def     core.ActionDef
		name    string
		class   core.Class
		inverse bool
		in      reflect.Type
	}{
		{r.c.tunnelCreate(), "cloudflare.tunnel.create", core.ClassCompensable, true, reflect.TypeFor[TunnelCreateInput]()},
		{r.c.tunnelDelete(), "cloudflare.tunnel.delete", core.ClassIrreversible, false, reflect.TypeFor[TunnelInput]()},
	} {
		s := tc.def.Spec
		if s.Name != tc.name || s.Version != 1 || s.Class != tc.class || s.Side != core.SideController ||
			!slices.Equal(s.LockKeys, []string{"cloudflare"}) || s.CompensatedBy != "" || s.ForwardOnly {
			t.Errorf("%s: spec %+v", tc.name, s)
		}
		if tc.def.InputType != tc.in || tc.def.OutputType != reflect.TypeFor[TunnelRef]() {
			t.Errorf("%s: input %v, output %v", tc.name, tc.def.InputType, tc.def.OutputType)
		}
		if tc.def.Handler.Observe == nil || (tc.def.Handler.Inverse != nil) != tc.inverse {
			t.Errorf("%s: Observe %v, Inverse %v, want an Observe handler and Inverse %v",
				tc.name, tc.def.Handler.Observe != nil, tc.def.Handler.Inverse != nil, tc.inverse)
		}
	}
}

func TestTuCreate(t *testing.T) {
	r := tuNew(t)
	def := r.c.tunnelCreate()
	out, err := def.Handler.Plan(context.Background(), r.h.env(""), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Diff) != 1 || out.Diff[0].Op != "+" || !strings.Contains(out.Diff[0].Text, "darkpanel-") || out.StateHash == "" {
		t.Errorf("plan %+v", out)
	}
	ref, err := r.apply(def, "chg-create", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	tunnels := r.srv.Tunnels()
	if len(tunnels) != 1 || tunnels[0].Name != "darkpanel-chg-create" {
		t.Fatalf("provider tunnels %+v, want one named darkpanel-chg-create", tunnels)
	}
	if ref != (TunnelRef{ID: tunnels[0].ID, Name: "darkpanel-chg-create"}) {
		t.Errorf("output %+v", ref)
	}
	row, found, err := loadTunnel(context.Background(), r.h.db.Read(), ref.ID)
	if err != nil || !found || row.Name != ref.Name || row.ChangeID != "chg-create" {
		t.Errorf("row %+v (found %v, err %v)", row, found, err)
	}
	effs := r.h.journal.effects()
	if len(effs) != 2 {
		t.Fatalf("%d effects, want 2", len(effs))
	}
	remote, local := effs[0], effs[1]
	if remote.Kind != core.EffectCreate || remote.Resource != "cloudflare.tunnel:"+ref.ID || remote.State != core.EffectMade ||
		remote.Local || remote.CommitWait != cloud.CommitWait ||
		remote.Recovery == nil || *remote.Recovery != (core.RecoveryID{Kind: "name", Value: "darkpanel-chg-create"}) {
		t.Errorf("provider effect %+v", remote)
	}
	tuSameJSON(t, "provider before", remote.Before.State, "null")
	if local.Kind != core.EffectCreate || local.Resource != "cloudflare.tunnelrow:"+ref.ID || local.State != core.EffectMade ||
		!local.Local || !local.InverseLocal {
		t.Errorf("row effect %+v", local)
	}
	for _, e := range effs {
		if e.Post == nil {
			t.Fatalf("effect %s has no post-state", e.Resource)
		}
		tuSameJSON(t, e.Resource+" post", e.Post.State, tuDoc(ref.ID, ref.Name))
	}
}

func TestTuCreateRefused(t *testing.T) {
	r := tuNew(t)
	r.srv.Cloud().Fail(http.MethodPost, tuCollection(t, r), cloudfake.Fault{Status: http.StatusForbidden})
	_, err := r.apply(r.c.tunnelCreate(), "chg-refused", json.RawMessage(`{}`))
	if tuCode(err) != errcode.CloudPermissionMissing || !core.IsDefinite(err) {
		t.Fatalf("error %v, want a definite cloud.permission_missing", err)
	}
	if effs := r.h.journal.effects(); len(effs) != 1 || effs[0].State != core.EffectSkipped {
		t.Errorf("effects %+v, want one skipped effect", effs)
	}
	if n := len(r.srv.Tunnels()); n != 0 {
		t.Errorf("%d provider tunnels, want none", n)
	}
	if rows, err := listTunnels(context.Background(), r.h.db.Read()); err != nil || len(rows) != 0 {
		t.Errorf("rows %+v (err %v), want none", rows, err)
	}
}

func TestTuCreateLostResponse(t *testing.T) {
	r := tuNew(t)
	def := r.c.tunnelCreate()
	r.srv.Cloud().Fail(http.MethodPost, tuCollection(t, r), cloudfake.Fault{LoseResponse: true})
	_, err := r.apply(def, "chg-lost", json.RawMessage(`{}`))
	if tuCode(err) != errcode.CloudUncertainOutcome || core.IsDefinite(err) {
		t.Fatalf("error %v, want an indefinite cloud.uncertain_outcome", err)
	}
	effs := r.h.journal.effects()
	if len(effs) != 1 || effs[0].State != core.EffectIntended || effs[0].Resource != "cloudflare.tunnel:?darkpanel-chg-lost" {
		t.Fatalf("effects %+v, want one intended creation named by its recovery value", effs)
	}
	obs, err := def.Handler.Observe(context.Background(), r.h.env("chg-lost"), effs[0])
	if err != nil {
		t.Fatal(err)
	}
	id := r.srv.Tunnels()[0].ID
	if obs.Matches != 1 || obs.Resource != "cloudflare.tunnel:"+id {
		t.Errorf("observation %+v, want one match, the committed tunnel", obs)
	}
	tuSameJSON(t, "observed", obs.Current.State, tuDoc(id, "darkpanel-chg-lost"))

	missing := effs[0]
	missing.Resource, missing.Recovery = "cloudflare.tunnel:?darkpanel-none", &core.RecoveryID{Kind: "name", Value: "darkpanel-none"}
	obs, err = def.Handler.Observe(context.Background(), r.h.env("chg-lost"), missing)
	if err != nil || obs.Matches != 0 || obs.Resource != "" {
		t.Errorf("observation of an absent creation %+v (err %v), want no match", obs, err)
	}
	tuSameJSON(t, "absent", obs.Current.State, "null")
}

func TestTuUndoCreate(t *testing.T) {
	r := tuNew(t)
	def := r.c.tunnelCreate()
	ref, err := r.apply(def, "chg-create", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	effs := r.h.journal.effects()
	if err := tuUndo(t, r, def, effs[1]); err != nil {
		t.Fatalf("undo of the row: %v", err)
	}
	if _, found, _ := loadTunnel(context.Background(), r.h.db.Read(), ref.ID); found {
		t.Error("the row survived its undo")
	}
	if len(r.srv.Tunnels()) != 1 {
		t.Fatal("the provider tunnel must outlive the row's undo")
	}
	if err := tuUndo(t, r, def, effs[0]); err != nil {
		t.Fatalf("undo of the tunnel: %v", err)
	}
	if n := len(r.srv.Tunnels()); n != 0 {
		t.Errorf("%d provider tunnels after the undo, want none", n)
	}
}

func TestTuUndoCreateRefusals(t *testing.T) {
	r := tuNew(t)
	def := r.c.tunnelCreate()
	ref, err := r.apply(def, "chg-create", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	effs := r.h.journal.effects()
	env := r.h.env("chg-undo")
	inverse := func(e core.Effect) error {
		e.State = core.EffectUndoing
		_, err := def.Handler.Inverse(context.Background(), env, e)
		return err
	}

	tuAddHostname(t, r.h, ref.ID, "app.example.com")
	if err := inverse(effs[1]); tuCode(err) != errcode.CloudConflict || !core.IsDefinite(err) {
		t.Errorf("row undo with a hostname: error %v, want a definite cloud.conflict", err)
	}
	if _, found, _ := loadTunnel(context.Background(), r.h.db.Read(), ref.ID); !found {
		t.Error("a refused undo removed the row")
	}
	tuRemoveHostname(t, r.h, "app.example.com")
	if err := inverse(effs[1]); err != nil {
		t.Fatalf("row undo: %v", err)
	}

	r.srv.SetConnections(ref.ID, 1)
	if err := inverse(effs[0]); tuCode(err) != errcode.CloudConflict || !core.IsDefinite(err) {
		t.Errorf("tunnel undo with a connector: error %v, want a definite cloud.conflict", err)
	}
	if len(r.srv.Tunnels()) != 1 {
		t.Error("a refused undo deleted the tunnel")
	}
	r.srv.SetConnections(ref.ID, 0)
	if err := inverse(effs[0]); err != nil {
		t.Fatalf("tunnel undo: %v", err)
	}
	if len(r.srv.Tunnels()) != 0 {
		t.Error("the tunnel survived its undo")
	}
}

func TestTuDeletePlan(t *testing.T) {
	r := tuNew(t)
	row := tuTunnel(t, r.h, r.srv)
	def := r.c.tunnelDelete()
	for _, ref := range []string{row.ID, row.Name} {
		out, err := def.Handler.Plan(context.Background(), r.h.env(""), tuInput(ref))
		if err != nil {
			t.Fatalf("plan by %q: %v", ref, err)
		}
		if out.Confirmation != row.Name || out.StateHash == "" || len(out.Diff) != 1 || out.Diff[0].Op != "-" ||
			!strings.Contains(out.Diff[0].Text, row.Name) || !strings.Contains(out.Diff[0].Text, row.ID) {
			t.Errorf("plan by %q: %+v", ref, out)
		}
	}
}

func TestTuDeleteRefusals(t *testing.T) {
	r := tuNew(t)
	row := tuTunnel(t, r.h, r.srv)
	def := r.c.tunnelDelete()
	plan := func(env core.Env, tunnel string) error {
		_, err := def.Handler.Plan(context.Background(), env, tuInput(tunnel))
		return err
	}
	if err := plan(r.h.env(""), ""); tuCode(err) != errcode.InvalidInput {
		t.Errorf("empty tunnel: error %v, want request.invalid_input", err)
	}
	if err := plan(r.h.env(""), "darkpanel-other"); tuCode(err) != errcode.NotFound || !core.IsDefinite(err) {
		t.Errorf("unmanaged tunnel: error %v, want a definite not_found", err)
	}
	if err := plan(newCFHost(t).env(""), row.ID); tuCode(err) != errcode.CloudNotConnected {
		t.Errorf("no account: error %v, want cloud.not_connected", err)
	}
	tuAddHostname(t, r.h, row.ID, "app.example.com")
	if err := plan(r.h.env(""), row.ID); tuCode(err) != errcode.CloudConflict || !core.IsDefinite(err) ||
		!strings.Contains(err.Error(), "1 hostnames") {
		t.Errorf("tunnel with a hostname: error %v, want a definite cloud.conflict naming the count", err)
	}
	tuRemoveHostname(t, r.h, "app.example.com")
	r.srv.SetConnections(row.ID, 2)
	if err := plan(r.h.env(""), row.ID); tuCode(err) != errcode.CloudConflict || !core.IsDefinite(err) ||
		!strings.Contains(err.Error(), "2 connectors") {
		t.Errorf("tunnel with connectors: error %v, want a definite cloud.conflict naming the count", err)
	}
	if _, err := r.apply(def, "chg-del", tuInput(row.ID)); tuCode(err) != errcode.CloudConflict {
		t.Errorf("apply with connectors: error %v, want cloud.conflict", err)
	}
	if len(r.srv.Tunnels()) != 1 || len(r.h.journal.effects()) != 0 {
		t.Error("a refused delete changed the provider or journaled an effect")
	}
}

func TestTuDelete(t *testing.T) {
	r := tuNew(t)
	row := tuTunnel(t, r.h, r.srv)
	def := r.c.tunnelDelete()
	ref, err := r.apply(def, "chg-del", tuInput(row.Name))
	if err != nil {
		t.Fatal(err)
	}
	if ref != (TunnelRef{ID: row.ID, Name: row.Name}) {
		t.Errorf("output %+v", ref)
	}
	if n := len(r.srv.Tunnels()); n != 0 {
		t.Errorf("%d provider tunnels, want none", n)
	}
	if _, found, _ := loadTunnel(context.Background(), r.h.db.Read(), row.ID); found {
		t.Error("the row survived")
	}
	effs := r.h.journal.effects()
	if len(effs) != 2 {
		t.Fatalf("%d effects, want 2", len(effs))
	}
	remote, local := effs[0], effs[1]
	if remote.Kind != core.EffectDelete || remote.Resource != "cloudflare.tunnel:"+row.ID || remote.State != core.EffectMade ||
		remote.Local || remote.CommitWait != cloud.CommitWait {
		t.Errorf("provider effect %+v", remote)
	}
	if local.Kind != core.EffectDelete || local.Resource != "cloudflare.tunnelrow:"+row.ID || local.State != core.EffectMade || !local.Local {
		t.Errorf("row effect %+v", local)
	}
	for _, e := range effs {
		tuSameJSON(t, e.Resource+" before", e.Before.State, tuDoc(row.ID, row.Name))
		if e.After == nil || e.Post == nil {
			t.Fatalf("effect %s: after %v, post %v", e.Resource, e.After, e.Post)
		}
		tuSameJSON(t, e.Resource+" after", e.After.State, "null")
		tuSameJSON(t, e.Resource+" post", e.Post.State, "null")
	}
	if err := def.Handler.Verify(context.Background(), r.h.env("chg-del"), tuInput(row.Name), json.RawMessage(`{}`)); err != nil {
		t.Errorf("verify: %v", err)
	}
}

func TestTuDeleteGoneFromProvider(t *testing.T) {
	r := tuNew(t)
	row := tuTunnel(t, r.h, r.srv)
	a, _, err := r.c.connected(context.Background(), r.h.db, r.h.env("").Sealer, r.h.clock)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.DeleteTunnel(context.Background(), row.ID); err != nil {
		t.Fatal(err)
	}
	def := r.c.tunnelDelete()
	out, err := def.Handler.Plan(context.Background(), r.h.env(""), tuInput(row.ID))
	if err != nil || len(out.Diff) != 1 || !strings.Contains(out.Diff[0].Text, "already gone") {
		t.Fatalf("plan %+v (err %v), want a line saying the tunnel is already gone", out, err)
	}
	if _, err := r.apply(def, "chg-del", tuInput(row.ID)); err != nil {
		t.Fatal(err)
	}
	effs := r.h.journal.effects()
	if len(effs) != 1 || effs[0].Resource != "cloudflare.tunnelrow:"+row.ID || effs[0].State != core.EffectMade {
		t.Errorf("effects %+v, want only the made row deletion", effs)
	}
	if _, found, _ := loadTunnel(context.Background(), r.h.db.Read(), row.ID); found {
		t.Error("the row survived")
	}
}

func TestTuDeleteRefusedAndLost(t *testing.T) {
	t.Run("refused", func(t *testing.T) {
		r := tuNew(t)
		row := tuTunnel(t, r.h, r.srv)
		r.srv.Cloud().Fail(http.MethodDelete, tuCollection(t, r)+"/"+row.ID, cloudfake.Fault{Status: http.StatusForbidden})
		_, err := r.apply(r.c.tunnelDelete(), "chg-del", tuInput(row.ID))
		if tuCode(err) != errcode.CloudPermissionMissing || !core.IsDefinite(err) {
			t.Fatalf("error %v, want a definite cloud.permission_missing", err)
		}
		if effs := r.h.journal.effects(); len(effs) != 1 || effs[0].State != core.EffectSkipped {
			t.Errorf("effects %+v, want one skipped effect", effs)
		}
		if _, found, _ := loadTunnel(context.Background(), r.h.db.Read(), row.ID); !found || len(r.srv.Tunnels()) != 1 {
			t.Error("a refused delete removed the row or the tunnel")
		}
	})
	t.Run("lost response converges", func(t *testing.T) {
		r := tuNew(t)
		row := tuTunnel(t, r.h, r.srv)
		r.srv.Cloud().Fail(http.MethodDelete, tuCollection(t, r)+"/"+row.ID, cloudfake.Fault{LoseResponse: true})
		if _, err := r.apply(r.c.tunnelDelete(), "chg-del", tuInput(row.ID)); err != nil {
			t.Fatalf("the retry that finds the tunnel gone must count as done: %v", err)
		}
		if _, found, _ := loadTunnel(context.Background(), r.h.db.Read(), row.ID); found {
			t.Error("the row survived")
		}
	})
}

func TestTuObserve(t *testing.T) {
	r := tuNew(t)
	row := tuTunnel(t, r.h, r.srv)
	remote := core.Effect{Kind: core.EffectDelete, Before: core.StateDoc{State: json.RawMessage(tuDoc(row.ID, row.Name))}}
	for _, tc := range []struct {
		name     string
		resource string
		want     string
	}{
		{"tunnel present", "cloudflare.tunnel:" + row.ID, tuDoc(row.ID, row.Name)},
		{"tunnel absent", "cloudflare.tunnel:tun-gone", "null"},
		{"row present", "cloudflare.tunnelrow:" + row.ID, tuDoc(row.ID, row.Name)},
		{"row absent", "cloudflare.tunnelrow:tun-gone", "null"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := remote
			e.Resource = tc.resource
			if strings.HasSuffix(tc.resource, "tun-gone") {
				e.Before = core.StateDoc{State: json.RawMessage(tuDoc("tun-gone", "darkpanel-gone"))}
			}
			obs, err := r.c.tunnelDelete().Handler.Observe(context.Background(), r.h.env("chg-o"), e)
			if err != nil {
				t.Fatal(err)
			}
			tuSameJSON(t, "state", obs.Current.State, tc.want)
		})
	}
	e := remote
	e.Resource = "cloudflare.tunnel:" + row.ID
	if _, err := r.c.tunnelDelete().Handler.Observe(context.Background(), newCFHost(t).env("chg-o"), e); tuCode(err) != errcode.CloudNotConnected {
		t.Errorf("without an account: error %v, want cloud.not_connected", err)
	}
}

func TestTuVerifyNeedsTheCallMade(t *testing.T) {
	r := tuNew(t)
	row := tuTunnel(t, r.h, r.srv)
	def := r.c.tunnelDelete()
	ctx, env := context.Background(), r.h.env("chg-v")
	if err := def.Handler.Verify(ctx, env, tuInput(row.ID), json.RawMessage(`{}`)); err == nil {
		t.Error("verify passed with no recorded call")
	}
	e := core.Effect{
		Kind: core.EffectDelete, Resource: "cloudflare.tunnelrow:" + row.ID, Local: true,
		Before: core.StateDoc{State: json.RawMessage(tuDoc(row.ID, row.Name))},
	}
	seq, err := env.Effects.Intend(ctx, e, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := env.Effects.Made(ctx, seq, e.Resource, core.StateDoc{State: json.RawMessage(`null`)}); err != nil {
		t.Fatal(err)
	}
	if err := def.Handler.Verify(ctx, env, tuInput(row.ID), json.RawMessage(`{}`)); err == nil {
		t.Error("verify passed although the row still exists")
	}
}
