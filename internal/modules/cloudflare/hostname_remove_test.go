package cloudflare

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/controller/cloud"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/cloudflare/fake"
	"github.com/darkraise/darkpanel/internal/modules/cloudflare/ingress"
)

const (
	hrHostname  = "app.example.com"
	hrOther     = "other.example.com"
	hrService   = "http://127.0.0.1:8080"
	hrAddChange = "01J00000000000000000000001"
)

var hrIn = json.RawMessage(`{"hostname":"app.example.com"}`)

// hrOpts varies the state hrNew builds; an empty field keeps what a clean
// hostname.add leaves.
type hrOpts struct {
	service, content, comment, phase, hash string
	noAccount                              bool
}

// hrWorld is an active hostname: its tunnel, its ingress rule between
// another rule and the catch-all, its proxied CNAME record, and the rows.
type hrWorld struct {
	h                    *cfHost
	c                    cf
	srv                  *fake.Server
	pa                   *api
	def                  core.ActionDef
	zone, tunnel, record string
	base                 int // requests the fixture itself made
}

func hrNew(t *testing.T, mods ...func(*hrOpts)) *hrWorld {
	t.Helper()
	ctx := context.Background()
	o := hrOpts{service: hrService, phase: "active"}
	for _, m := range mods {
		m(&o)
	}
	srv := fake.New(t)
	h := newCFHost(t)
	if !o.noAccount {
		acConnect(t, h, srv)
	}
	c := cfWith(srv)
	pa, err := c.newAPI(fake.AccountID, []byte(fake.Token), h.clock)
	if err != nil {
		t.Fatal(err)
	}
	zone := srv.AddZone("example.com")
	tun, err := pa.CreateTunnel(ctx, "darkpanel-"+hrAddChange)
	if err != nil {
		t.Fatal(err)
	}
	cfg := ingress.Config{Ingress: []ingress.Rule{
		{Hostname: hrOther, Service: "http://127.0.0.1:9000"},
		{Hostname: hrHostname, Service: o.service},
		{Service: ingress.CatchAll},
	}}
	if err := pa.PutConfiguration(ctx, tun.ID, cfg); err != nil {
		t.Fatal(err)
	}
	live, err := pa.Configuration(ctx, tun.ID)
	if err != nil {
		t.Fatal(err)
	}
	content, comment, hash := o.content, o.comment, o.hash
	if content == "" {
		content = tun.ID + ".cfargotunnel.com"
	}
	if comment == "" {
		comment = "darkpanel:" + hrAddChange
	}
	if hash == "" {
		hash = ingress.Hash(live)
	}
	rec, err := pa.CreateDNSRecord(ctx, zone, DNSRecord{Type: "CNAME", Name: hrHostname, Content: content, Proxied: true, Comment: comment})
	if err != nil {
		t.Fatal(err)
	}
	now := h.clock.Now()
	err = h.db.Write(ctx, func(tx *sql.Tx) error {
		if err := insertTunnel(ctx, tx, tunnelRow{ID: tun.ID, Name: tun.Name, ChangeID: hrAddChange, ConfigHash: hash, CreatedAt: now}); err != nil {
			return err
		}
		return insertHostname(ctx, tx, hostnameRow{
			Hostname: hrHostname, TunnelID: tun.ID, Service: o.service, ZoneID: zone, RecordID: rec.ID,
			Phase: o.phase, ChangeID: hrAddChange, Health: "unknown", UpdatedAt: now,
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	return &hrWorld{h: h, c: c, srv: srv, pa: pa, def: c.hostnameRemove(), zone: zone, tunnel: tun.ID, record: rec.ID, base: len(srv.Cloud().Requests())}
}

// writes are the non-GET requests since the fixture was built, as
// "<method> <path>".
func (w *hrWorld) writes() []string {
	var out []string
	for _, r := range w.srv.Cloud().Requests()[w.base:] {
		if r.Method != http.MethodGet {
			out = append(out, r.Method+" "+r.Path)
		}
	}
	return out
}

func (w *hrWorld) row(t *testing.T) (hostnameRow, bool) {
	t.Helper()
	r, ok, err := loadHostname(context.Background(), w.h.db.Read(), hrHostname)
	if err != nil {
		t.Fatal(err)
	}
	return r, ok
}

func (w *hrWorld) records(t *testing.T) []DNSRecord {
	t.Helper()
	recs, err := w.pa.DNSRecords(context.Background(), w.zone, hrHostname, "")
	if err != nil {
		t.Fatal(err)
	}
	return recs
}

func (w *hrWorld) config(t *testing.T) ingress.Config {
	t.Helper()
	cfg, err := w.pa.Configuration(context.Background(), w.tunnel)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func (w *hrWorld) setPhase(t *testing.T, phase string) {
	t.Helper()
	err := w.h.db.Write(context.Background(), func(tx *sql.Tx) error {
		return setHostnamePhase(context.Background(), tx, hrHostname, phase, w.zone, w.record, w.h.clock.Now())
	})
	if err != nil {
		t.Fatal(err)
	}
	w.base = len(w.srv.Cloud().Requests())
}

func hrSame(a, b json.RawMessage) bool {
	var x, y any
	return json.Unmarshal(a, &x) == nil && json.Unmarshal(b, &y) == nil && reflect.DeepEqual(x, y)
}

// hrLose forwards a request, so the provider commits it, and then fails the
// call as a dropped connection.
type hrLose struct {
	next             http.RoundTripper
	method, contains string
}

func (l *hrLose) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := l.next.RoundTrip(r)
	if err != nil || r.Method != l.method || !strings.Contains(r.URL.Path, l.contains) {
		return resp, err
	}
	_ = resp.Body.Close()
	return nil, errors.New("hr: connection dropped after the server answered")
}

// TestHRSpec: the action is compensable by hostname.add alone (no undo
// handler), controller side, locked on cloudflare, converges, and has an
// observer and a recovery handler.
func TestHRSpec(t *testing.T) {
	def := cfWith(fake.New(t)).hostnameRemove()
	s := def.Spec
	if s.Name != "cloudflare.hostname.remove" || s.Version != 1 || s.Class != core.ClassCompensable || s.Side != core.SideController ||
		!slices.Equal(s.LockKeys, []string{"cloudflare"}) || s.CompensatedBy != "cloudflare.hostname.add" || !s.Converges || s.ForwardOnly {
		t.Errorf("spec %+v", s)
	}
	if def.Handler.Inverse != nil || def.Handler.Observe == nil || def.Handler.Recover == nil {
		t.Errorf("inverse %v observe %v recover %v, want no inverse, an observer and a recovery handler",
			def.Handler.Inverse != nil, def.Handler.Observe != nil, def.Handler.Recover != nil)
	}
}

// TestHRPlanShowsTheRemoval: the plan names the record and the rule it will
// delete and says it is a read-then-write check, and writes nothing.
func TestHRPlanShowsTheRemoval(t *testing.T) {
	w := hrNew(t)
	out, err := w.def.Handler.Plan(context.Background(), w.h.env(""), hrIn)
	if err != nil {
		t.Fatal(err)
	}
	want := []core.DiffLine{
		{Op: "-", Text: "dns record " + w.record + ": CNAME " + hrHostname + " -> " + w.tunnel + ".cfargotunnel.com"},
		{Op: "-", Text: "ingress rule: " + hrHostname + " -> " + hrService},
		{Op: " ", Text: "read-then-write: a change another client makes between the read and the delete is not detected"},
	}
	if !reflect.DeepEqual(out.Diff, want) {
		t.Errorf("diff %+v\nwant %+v", out.Diff, want)
	}
	if out.StateHash == "" || out.Confirmation != "" {
		t.Errorf("state hash %q confirmation %q, want a hash and no confirmation", out.StateHash, out.Confirmation)
	}
	again, err := w.def.Handler.Plan(context.Background(), w.h.env(""), hrIn)
	if err != nil || again.StateHash != out.StateHash {
		t.Errorf("second plan hash %q err %v, want %q", again.StateHash, err, out.StateHash)
	}
	if got := w.writes(); len(got) != 0 {
		t.Errorf("plan wrote %v", got)
	}
}

// TestHRRemove: apply deletes the record, then the rule, keeps the other
// rules and the catch-all, stores the new configuration hash, deletes the
// row and journals a delete of the record and a modify of the rule.
func TestHRRemove(t *testing.T) {
	ctx := context.Background()
	w := hrNew(t)
	env := w.h.env("01J00000000000000000000002")
	raw, err := w.def.Handler.Apply(ctx, env, hrIn)
	if err != nil {
		t.Fatal(err)
	}
	var view HostnameView
	if err := json.Unmarshal(raw, &view); err != nil {
		t.Fatal(err)
	}
	if view.Hostname != hrHostname || view.Tunnel != w.tunnel || view.Phase != "removed" || view.RecordID != w.record || view.Service != hrService {
		t.Errorf("view %+v, want the removed hostname with its tunnel, record and service", view)
	}
	if recs := w.records(t); len(recs) != 0 {
		t.Errorf("records %+v remain", recs)
	}
	cfg := w.config(t)
	if _, ok := ingress.Lookup(cfg, hrHostname); ok {
		t.Error("the rule remains")
	}
	if r, ok := ingress.Lookup(cfg, hrOther); !ok || r.Service != "http://127.0.0.1:9000" {
		t.Errorf("the unrelated rule is %+v, present %v, want it unchanged", r, ok)
	}
	if last := cfg.Ingress[len(cfg.Ingress)-1]; last.Hostname != "" || last.Service != ingress.CatchAll {
		t.Errorf("last rule %+v, want the catch-all", last)
	}
	tr, ok, err := loadTunnel(ctx, w.h.db.Read(), w.tunnel)
	if err != nil || !ok || tr.ConfigHash != ingress.Hash(cfg) {
		t.Errorf("tunnel row %+v ok %v err %v, want the hash of the live configuration", tr, ok, err)
	}
	if _, ok := w.row(t); ok {
		t.Error("the hostname row remains")
	}
	got := w.writes()
	if len(got) != 2 || !strings.HasPrefix(got[0], "DELETE ") || !strings.Contains(got[0], "/dns_records/"+w.record) ||
		!strings.HasPrefix(got[1], "PUT ") || !strings.Contains(got[1], "/configurations") {
		t.Errorf("writes %v, want the record delete and then the configuration put", got)
	}
	effs := w.h.journal.effects()
	if len(effs) != 2 {
		t.Fatalf("%d effects, want 2", len(effs))
	}
	wantEff := []struct {
		kind     core.EffectKind
		resource string
		post     string
	}{
		{core.EffectDelete, "cloudflare.dnsrecord:" + w.zone + "/" + w.record, `null`},
		{core.EffectModify, "cloudflare.ingress:" + w.tunnel + "/" + hrHostname, `{"rule":null}`},
	}
	for i, we := range wantEff {
		e := effs[i]
		if e.Kind != we.kind || e.Resource != we.resource || e.State != core.EffectMade || e.Local || e.CommitWait != cloud.CommitWait ||
			e.Post == nil || !hrSame(e.Post.State, json.RawMessage(we.post)) {
			t.Errorf("effect %d %+v, want a made non-local %s of %s with post %s", i, e, we.kind, we.resource, we.post)
		}
		obs, err := w.def.Handler.Observe(ctx, env, e)
		if err != nil || e.Post == nil || !hrSame(obs.Current.State, e.Post.State) {
			t.Errorf("observation of effect %d is %s (err %v), want its post state", i, obs.Current.State, err)
		}
	}
	var before struct {
		Content, Comment string
	}
	if err := json.Unmarshal(effs[0].Before.State, &before); err != nil ||
		before.Content != w.tunnel+".cfargotunnel.com" || before.Comment != "darkpanel:"+hrAddChange {
		t.Errorf("record before state %s (err %v), want its content and comment", effs[0].Before.State, err)
	}
	if err := w.def.Handler.Verify(ctx, env, hrIn, raw); err != nil {
		t.Errorf("verify after apply: %v", err)
	}
}

// TestHRConvergesAfterARemoval: run again after a complete removal, the
// action plans an empty diff, writes nothing, journals nothing and answers
// the removed hostname.
func TestHRConvergesAfterARemoval(t *testing.T) {
	ctx := context.Background()
	w := hrNew(t)
	if _, err := w.def.Handler.Apply(ctx, w.h.env("01J00000000000000000000002"), hrIn); err != nil {
		t.Fatal(err)
	}
	w.base = len(w.srv.Cloud().Requests())
	out, err := w.def.Handler.Plan(ctx, w.h.env(""), hrIn)
	if err != nil || len(out.Diff) != 0 {
		t.Fatalf("second plan diff %+v err %v, want it empty", out.Diff, err)
	}
	raw, err := w.def.Handler.Apply(ctx, w.h.env("01J00000000000000000000003"), hrIn)
	if err != nil {
		t.Fatal(err)
	}
	var view HostnameView
	if err := json.Unmarshal(raw, &view); err != nil || view.Hostname != hrHostname || view.Phase != "removed" {
		t.Errorf("second apply answered %s (err %v), want the removed hostname", raw, err)
	}
	if got := w.writes(); len(got) != 0 {
		t.Errorf("second apply wrote %v", got)
	}
	if n := len(w.h.journal.effects()); n != 2 {
		t.Errorf("%d effects after the second apply, want 2", n)
	}
}

// TestHRFinishesAPartialRemoval: a record that is already gone counts as
// removed; the plan shows only the rule and apply deletes only the rule.
func TestHRFinishesAPartialRemoval(t *testing.T) {
	ctx := context.Background()
	w := hrNew(t)
	if err := w.pa.DeleteDNSRecord(ctx, w.zone, w.record); err != nil {
		t.Fatal(err)
	}
	w.setPhase(t, "removing_record")
	out, err := w.def.Handler.Plan(ctx, w.h.env(""), hrIn)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Diff) != 2 || out.Diff[0].Text != "ingress rule: "+hrHostname+" -> "+hrService {
		t.Errorf("diff %+v, want the rule line and the read-then-write note", out.Diff)
	}
	if _, err := w.def.Handler.Apply(ctx, w.h.env("01J00000000000000000000002"), hrIn); err != nil {
		t.Fatal(err)
	}
	if got := w.writes(); len(got) != 1 || !strings.HasPrefix(got[0], "PUT ") {
		t.Errorf("writes %v, want the configuration put only", got)
	}
	effs := w.h.journal.effects()
	if len(effs) != 1 || !strings.HasPrefix(effs[0].Resource, "cloudflare.ingress:") {
		t.Errorf("effects %+v, want the rule's only", effs)
	}
	if _, ok := w.row(t); ok {
		t.Error("the hostname row remains")
	}
}

// TestHRRefusals: a changed record, a drifted configuration, a hostname
// still being added and a missing account each refuse the plan and the
// apply with a definite error, before any write, and leave the row as it
// was.
func TestHRRefusals(t *testing.T) {
	tests := []struct {
		name  string
		mod   func(*hrOpts)
		code  errcode.Code
		phase string
	}{
		{"record content changed", func(o *hrOpts) { o.content = "other.cfargotunnel.com" }, errcode.CloudConflict, "active"},
		{"record comment changed", func(o *hrOpts) { o.comment = "darkpanel:someone-else" }, errcode.CloudConflict, "active"},
		{"configuration drift", func(o *hrOpts) { o.hash = "deadbeef" }, errcode.CloudConflict, "active"},
		{"added mid-phase", func(o *hrOpts) { o.phase = "adding_record" }, errcode.CloudConflict, "adding_record"},
		{"no account", func(o *hrOpts) { o.noAccount = true }, errcode.CloudNotConnected, "active"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			w := hrNew(t, tt.mod)
			if _, err := w.def.Handler.Plan(ctx, w.h.env(""), hrIn); errcode.From(err).Code != tt.code {
				t.Errorf("plan error %v, want %s", err, tt.code)
			}
			_, err := w.def.Handler.Apply(ctx, w.h.env("01J00000000000000000000002"), hrIn)
			if errcode.From(err).Code != tt.code || !core.IsDefinite(err) {
				t.Errorf("apply error %v (definite %v), want a definite %s", err, core.IsDefinite(err), tt.code)
			}
			if got := w.writes(); len(got) != 0 {
				t.Errorf("writes %v, want none", got)
			}
			if n := len(w.h.journal.effects()); n != 0 {
				t.Errorf("%d effects, want none", n)
			}
			if row, ok := w.row(t); !ok || row.Phase != tt.phase {
				t.Errorf("row %+v present %v, want it kept in phase %s", row, ok, tt.phase)
			}
		})
	}
}

// TestHRReserved: a hostname whose service is in the panel network is
// removed only as a step of panel.exposure.remove.
func TestHRReserved(t *testing.T) {
	ctx := context.Background()
	w := hrNew(t, func(o *hrOpts) { o.service = "http://172.31.255.1:8080" })
	for _, parent := range []string{"", "panel.exposure.add", "npm.expose.remove"} {
		env := w.h.env("")
		env.Call.ParentAction = parent
		if _, err := w.def.Handler.Plan(ctx, env, hrIn); errcode.From(err).Code != errcode.CloudflareReserved {
			t.Errorf("parent %q: plan error %v, want %s", parent, err, errcode.CloudflareReserved)
		}
		env = w.h.env("01J00000000000000000000002")
		env.Call.ParentAction = parent
		if _, err := w.def.Handler.Apply(ctx, env, hrIn); errcode.From(err).Code != errcode.CloudflareReserved || !core.IsDefinite(err) {
			t.Errorf("parent %q: apply error %v, want a definite %s", parent, err, errcode.CloudflareReserved)
		}
	}
	if got := w.writes(); len(got) != 0 {
		t.Fatalf("writes %v after the refusals, want none", got)
	}
	env := w.h.env("01J00000000000000000000003")
	env.Call.ParentAction = "panel.exposure.remove"
	if _, err := w.def.Handler.Apply(ctx, env, hrIn); err != nil {
		t.Fatalf("apply as a step of panel.exposure.remove: %v", err)
	}
	if _, ok := w.row(t); ok {
		t.Error("the hostname row remains")
	}
}

// TestHRUncertainWriteThenRecover: a configuration write whose answer is
// lost leaves its effect intended and the row in removing_rule; observation
// shows the rule gone, and once the operator has resolved the effect the
// recovery handler finishes the removal without writing again.
func TestHRUncertainWriteThenRecover(t *testing.T) {
	ctx := context.Background()
	w := hrNew(t)
	w.c.d.http = &http.Client{Transport: &hrLose{next: w.srv.Client().Transport, method: http.MethodPut, contains: "/configurations"}}
	w.def = w.c.hostnameRemove()
	change := "01J00000000000000000000002"
	env := w.h.env(change)
	_, err := w.def.Handler.Apply(ctx, env, hrIn)
	if errcode.From(err).Code != errcode.CloudUncertainOutcome || core.IsDefinite(err) {
		t.Fatalf("apply error %v (definite %v), want an indefinite %s", err, core.IsDefinite(err), errcode.CloudUncertainOutcome)
	}
	effs := w.h.journal.effects()
	if len(effs) != 2 || effs[0].State != core.EffectMade || effs[1].State != core.EffectIntended {
		t.Fatalf("effects %+v, want the record delete made and the rule modify intended", effs)
	}
	if row, ok := w.row(t); !ok || row.Phase != "removing_rule" {
		t.Fatalf("row %+v present %v, want phase removing_rule", row, ok)
	}
	obs, err := w.def.Handler.Observe(ctx, env, effs[1])
	if err != nil || !hrSame(obs.Current.State, json.RawMessage(`{"rule":null}`)) {
		t.Fatalf("observation %s err %v, want the rule gone", obs.Current.State, err)
	}
	if err := w.h.journal.Made(ctx, effs[1].Seq, effs[1].Resource, obs.Current); err != nil {
		t.Fatal(err)
	}
	w.base = len(w.srv.Cloud().Requests())
	out, err := w.def.Handler.Recover(ctx, env)
	if err != nil || out.State != core.RecoveryCompleted {
		t.Fatalf("recover: %+v err %v, want completed", out, err)
	}
	var view HostnameView
	if err := json.Unmarshal(out.Output, &view); err != nil || view.Hostname != hrHostname || view.Phase != "removed" {
		t.Errorf("recovery output %s (err %v), want the removed hostname", out.Output, err)
	}
	if got := w.writes(); len(got) != 0 {
		t.Errorf("recovery wrote %v, want nothing", got)
	}
	if _, ok := w.row(t); ok {
		t.Error("the hostname row remains")
	}
	tr, _, err := loadTunnel(ctx, w.h.db.Read(), w.tunnel)
	if err != nil || tr.ConfigHash != ingress.Hash(w.config(t)) {
		t.Errorf("tunnel hash %q err %v, want the hash of the live configuration", tr.ConfigHash, err)
	}
}

// TestHRRecoverFinishes: a row left in removing_record by a crash before any
// effect was journaled is finished forward.
func TestHRRecoverFinishes(t *testing.T) {
	ctx := context.Background()
	w := hrNew(t)
	w.setPhase(t, "removing_record")
	out, err := w.def.Handler.Recover(ctx, w.h.env("01J00000000000000000000002"))
	if err != nil || out.State != core.RecoveryCompleted {
		t.Fatalf("recover: %+v err %v, want completed", out, err)
	}
	if recs := w.records(t); len(recs) != 0 {
		t.Errorf("records %+v remain", recs)
	}
	if _, ok := ingress.Lookup(w.config(t), hrHostname); ok {
		t.Error("the rule remains")
	}
	if _, ok := w.row(t); ok {
		t.Error("the hostname row remains")
	}
	if n := len(w.h.journal.effects()); n != 2 {
		t.Errorf("%d effects, want the record delete and the rule modify", n)
	}
}

// TestHRRecoverNothingStarted: an active row means the removal never began,
// so the recovery handler fails the change and touches nothing.
func TestHRRecoverNothingStarted(t *testing.T) {
	w := hrNew(t)
	out, err := w.def.Handler.Recover(context.Background(), w.h.env("01J00000000000000000000002"))
	if err != nil || out.State != core.RecoveryFailed || out.Reason == "" {
		t.Errorf("recover: %+v err %v, want failed with a reason", out, err)
	}
	if got := w.writes(); len(got) != 0 {
		t.Errorf("writes %v, want none", got)
	}
	if recs := w.records(t); len(recs) != 1 {
		t.Errorf("%d records, want the record kept", len(recs))
	}
}

// TestHRRecoverRefusesAChangedRecord: a record someone changed is not
// deleted by recovery either; the handler returns the conflict.
func TestHRRecoverRefusesAChangedRecord(t *testing.T) {
	w := hrNew(t, func(o *hrOpts) { o.content = "other.cfargotunnel.com"; o.phase = "removing_record" })
	_, err := w.def.Handler.Recover(context.Background(), w.h.env("01J00000000000000000000002"))
	if errcode.From(err).Code != errcode.CloudConflict {
		t.Errorf("recover error %v, want %s", err, errcode.CloudConflict)
	}
	if got := w.writes(); len(got) != 0 {
		t.Errorf("writes %v, want none", got)
	}
}

// TestHRRecoverAfterTheRowIsGone: a crash after the row was deleted but
// before the outcome was recorded completes from the journal.
func TestHRRecoverAfterTheRowIsGone(t *testing.T) {
	ctx := context.Background()
	w := hrNew(t)
	env := w.h.env("01J00000000000000000000002")
	if _, err := w.def.Handler.Apply(ctx, env, hrIn); err != nil {
		t.Fatal(err)
	}
	w.base = len(w.srv.Cloud().Requests())
	out, err := w.def.Handler.Recover(ctx, env)
	if err != nil || out.State != core.RecoveryCompleted {
		t.Fatalf("recover: %+v err %v, want completed", out, err)
	}
	var view HostnameView
	if err := json.Unmarshal(out.Output, &view); err != nil || view.Hostname != hrHostname || view.Tunnel != w.tunnel || view.Phase != "removed" {
		t.Errorf("recovery output %s (err %v), want the removed hostname and its tunnel", out.Output, err)
	}
	if got := w.writes(); len(got) != 0 {
		t.Errorf("writes %v, want none", got)
	}
}
