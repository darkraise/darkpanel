package cloudflare

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/cloud"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/modules/cloudflare/fake"
)

const acAccountChange = "01J00000000000000000000001"

var acAdmin = core.Principal{Actor: "user:u1", UserID: "u1", Scope: core.ScopeAdmin, Source: "203.0.113.7"}

// acConnect inserts the connected account the fake server accepts.
func acConnect(t *testing.T, h *cfHost, _ *fake.Server) account {
	t.Helper()
	ct, err := cloud.SealCredential(coretest.Sealer{}, "cloudflare_accounts", "token_ct", acAccountChange, []byte(fake.Token))
	if err != nil {
		t.Fatal(err)
	}
	a := account{
		ID: acAccountChange, AccountID: fake.AccountID, TokenCT: ct, ConnectedAt: h.clock.Now(),
		Capabilities: []cloud.Capability{{Name: "tunnels.read", State: cloud.CapabilityOK}},
	}
	ctx := context.Background()
	if err := h.db.Write(ctx, func(tx *sql.Tx) error { return insertAccount(ctx, tx, a) }); err != nil {
		t.Fatal(err)
	}
	return a
}

type acModule struct{ actions []core.ActionDef }

func (m acModule) Manifest() core.Manifest {
	return core.Manifest{ID: ID, Version: 1, Actions: m.actions}
}

func acEngine(t *testing.T, h *cfHost, c cf) *lifecycle.Engine {
	t.Helper()
	reg, err := core.NewRegistry(acModule{[]core.ActionDef{c.accountConnect(), c.accountDisconnect(), c.accountGet()}})
	if err != nil {
		t.Fatal(err)
	}
	eng, err := lifecycle.New(lifecycle.Config{
		Side: core.SideController, Registry: reg, Validator: schema.NewValidator(), Journal: &coretest.MemJournal{}, Redact: schema.Redact,
		Base: core.Env{DB: h.db, Sealer: coretest.Sealer{}, Clock: h.clock, Log: slog.New(slog.NewTextHandler(io.Discard, nil))},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(eng.Close)
	return eng
}

// acRun plans action and applies the plan with the plan's confirmation phrase.
func acRun(t *testing.T, eng *lifecycle.Engine, action, input string) (core.PlanView, core.ChangeView, error) {
	t.Helper()
	ctx := context.Background()
	pv, err := eng.Plan(ctx, acAdmin, core.ActionRef{Name: action, Version: 1}, json.RawMessage(input), core.PlanOptions{})
	if err != nil {
		return pv, core.ChangeView{}, err
	}
	cv, err := eng.Apply(ctx, acAdmin, pv.PlanID, core.ApplyOptions{Confirmation: pv.Confirmation})
	return pv, cv, err
}

// acUndo undoes a change and waits until the undo has settled.
func acUndo(t *testing.T, eng *lifecycle.Engine, changeID string) core.ChangeView {
	t.Helper()
	ctx := context.Background()
	if _, err := eng.Undo(ctx, acAdmin, changeID, nil); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		v, err := eng.Change(ctx, changeID)
		if err != nil {
			t.Fatal(err)
		}
		if v.State != core.StateUndoing || time.Now().After(deadline) {
			return v
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func acEffects(t *testing.T, h *cfHost, changeID string) []string {
	t.Helper()
	rows, err := h.db.Read().QueryContext(context.Background(),
		`SELECT kind, resource, state, local FROM core_effects WHERE change_id = ? ORDER BY seq`, changeID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var kind, resource, state string
		var local int
		if err := rows.Scan(&kind, &resource, &state, &local); err != nil {
			t.Fatal(err)
		}
		out = append(out, fmt.Sprintf("%s %s %s local=%d", kind, resource, state, local))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// acPending inserts a hostname row that is not active, which blocks deleting the account.
func acPending(t *testing.T, h *cfHost) {
	t.Helper()
	ctx := context.Background()
	now := h.clock.Now().UTC().Format(time.RFC3339Nano)
	err := h.db.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO cloudflare_tunnels (id, name, change_id, created_at) VALUES ('t1', 'darkpanel-x', 'c1', ?)`, now); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO cloudflare_hostnames (hostname, tunnel_id, service, phase, change_id, updated_at)
			VALUES ('app.example.com', 't1', 'http://10.0.0.5:80', 'adding_rule', 'c1', ?)`, now)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func acActivate(t *testing.T, h *cfHost) {
	t.Helper()
	ctx := context.Background()
	err := h.db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE cloudflare_hostnames SET phase = 'active' WHERE hostname = 'app.example.com'`)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func acConnectInput(token string) string {
	return `{"accountId":"` + fake.AccountID + `","apiToken":"` + token + `"}`
}

func acDiff(pv core.PlanView) string {
	var b strings.Builder
	for _, l := range pv.Diff {
		b.WriteString(l.Op + " " + l.Text + "\n")
	}
	return b.String()
}

func TestAcConnectStoresTheAccount(t *testing.T) {
	srv := fake.New(t)
	h := newCFHost(t)
	eng := acEngine(t, h, cfWith(srv))

	pv, cv, err := acRun(t, eng, "cloudflare.account.connect", acConnectInput(fake.Token))
	if err != nil {
		t.Fatal(err)
	}
	if cv.State != core.StateCompleted {
		t.Fatalf("change is %s (%+v), want completed", cv.State, cv.Error)
	}
	diff := acDiff(pv)
	for _, want := range []string{
		"cloudflare account " + fake.AccountID,
		"capability tunnels.read: ok", "capability zones.read: ok",
		"capability tunnels.write: unverified", "capability dns.write: unverified",
	} {
		if !strings.Contains(diff, want) {
			t.Errorf("plan diff lacks %q:\n%s", want, diff)
		}
	}
	if strings.Contains(diff, fake.Token) {
		t.Error("the plan diff carries the API token")
	}

	ctx := context.Background()
	acct, ok, err := loadAccount(ctx, h.db.Read())
	if err != nil || !ok {
		t.Fatalf("loadAccount = %v, %v; want the connected account", ok, err)
	}
	wantCaps := []cloud.Capability{
		{Name: "tunnels.read", State: cloud.CapabilityOK}, {Name: "zones.read", State: cloud.CapabilityOK},
		{Name: "tunnels.write", State: cloud.CapabilityUnverified}, {Name: "dns.write", State: cloud.CapabilityUnverified},
	}
	if acct.ID != cv.ID || acct.AccountID != fake.AccountID || !slices.Equal(acct.Capabilities, wantCaps) {
		t.Errorf("stored account %+v, want id %s, account %s and capabilities %+v", acct, cv.ID, fake.AccountID, wantCaps)
	}
	token, err := cloud.OpenCredential(coretest.Sealer{}, "cloudflare_accounts", "token_ct", acct.ID, acct.TokenCT)
	if err != nil || string(token) != fake.Token {
		t.Errorf("sealed token opens to %q (%v), want the connected token", token, err)
	}
	if strings.Contains(string(cv.Output), fake.Token) {
		t.Errorf("change output %s carries the API token", cv.Output)
	}
	var view AccountView
	if err := json.Unmarshal(cv.Output, &view); err != nil || view.AccountID != fake.AccountID || !slices.Equal(view.Capabilities, wantCaps) {
		t.Errorf("output %s (%v), want the account view", cv.Output, err)
	}
	if got, want := acEffects(t, h, cv.ID), []string{"create cloudflare.account:" + cv.ID + " made local=1"}; !slices.Equal(got, want) {
		t.Errorf("effects %q, want %q", got, want)
	}

	var tunnelsRead, zonesRead bool
	for _, r := range srv.Cloud().Requests() {
		if r.Method != http.MethodGet {
			t.Errorf("connect sent %s %s; it may only read from Cloudflare", r.Method, r.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+fake.Token {
			t.Errorf("%s %s sent Authorization %q", r.Method, r.Path, got)
		}
		tunnelsRead = tunnelsRead || strings.HasSuffix(r.Path, "/cfd_tunnel")
		zonesRead = zonesRead || strings.HasSuffix(r.Path, "/zones")
	}
	if !tunnelsRead || !zonesRead {
		t.Errorf("tunnels listed %v, zones listed %v; connect probes both", tunnelsRead, zonesRead)
	}
}

func TestAcConnectReportsAMissingPermission(t *testing.T) {
	srv := fake.New(t)
	srv.SetPermissions(fake.PermTunnelWrite, fake.PermDNSWrite)
	h := newCFHost(t)
	eng := acEngine(t, h, cfWith(srv))

	pv, cv, err := acRun(t, eng, "cloudflare.account.connect", acConnectInput(fake.Token))
	if err != nil || cv.State != core.StateCompleted {
		t.Fatalf("connect = %v, %v (%+v); a missing read permission is reported, not refused", cv.State, err, cv.Error)
	}
	if !strings.Contains(acDiff(pv), "capability zones.read: missing (cloud.permission_missing)") {
		t.Errorf("plan diff does not report the missing Zone Read permission:\n%s", acDiff(pv))
	}
	acct, _, err := loadAccount(context.Background(), h.db.Read())
	if err != nil {
		t.Fatal(err)
	}
	if got := acct.Capabilities[:2]; !slices.Equal(got, []cloud.Capability{
		{Name: "tunnels.read", State: cloud.CapabilityOK},
		{Name: "zones.read", State: cloud.CapabilityMissing, Detail: "cloud.permission_missing"},
	}) {
		t.Errorf("stored read capabilities %+v", got)
	}
}

func TestAcConnectRefusals(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		connected bool
		code      errcode.Code
		field     string
	}{
		{"account id of the wrong shape", `{"accountId":"nope","apiToken":"` + fake.Token + `"}`, false, errcode.InvalidInput, "/accountId"},
		{"blank token", acConnectInput("  "), false, errcode.InvalidInput, "/apiToken"},
		{"token with a space", acConnectInput("fake token"), false, errcode.InvalidInput, "/apiToken"},
		{"token the provider rejects", acConnectInput("other-token"), false, errcode.CloudAuthFailed, ""},
		{"an account is already connected", acConnectInput(fake.Token), true, errcode.InvalidInput, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := fake.New(t)
			h := newCFHost(t)
			if tt.connected {
				acConnect(t, h, srv)
			}
			_, _, err := acRun(t, acEngine(t, h, cfWith(srv)), "cloudflare.account.connect", tt.input)
			e := errcode.From(err)
			if e.Code != tt.code {
				t.Fatalf("error %v, want code %s", err, tt.code)
			}
			if tt.field != "" && !slices.ContainsFunc(e.Fields, func(f errcode.FieldError) bool { return f.Path == tt.field }) {
				t.Errorf("error fields %+v, want one naming %s", e.Fields, tt.field)
			}
			if strings.Contains(e.Detail, "other-token") || strings.Contains(e.Detail, fake.Token) {
				t.Errorf("detail %q carries the token", e.Detail)
			}
			if _, ok, _ := loadAccount(context.Background(), h.db.Read()); ok != tt.connected {
				t.Errorf("an account is stored = %v after the refusal, want %v", ok, tt.connected)
			}
		})
	}
}

func TestAcConnectUndo(t *testing.T) {
	srv := fake.New(t)
	h := newCFHost(t)
	eng := acEngine(t, h, cfWith(srv))
	_, cv, err := acRun(t, eng, "cloudflare.account.connect", acConnectInput(fake.Token))
	if err != nil || cv.State != core.StateCompleted {
		t.Fatalf("connect = %v, %v", cv.State, err)
	}
	acPending(t, h)

	v := acUndo(t, eng, cv.ID)
	if v.State != core.StateNeedsRepair {
		t.Fatalf("undo with a hostname mid-phase left the change %s, want needs_repair", v.State)
	}
	if _, ok, _ := loadAccount(context.Background(), h.db.Read()); !ok {
		t.Error("the undo deleted the account while cleanup was pending")
	}
	if got, want := acEffects(t, h, cv.ID), []string{"create cloudflare.account:" + cv.ID + " made local=1"}; !slices.Equal(got, want) {
		t.Errorf("effects %q after the refused undo, want %q", got, want)
	}

	acActivate(t, h)
	v = acUndo(t, eng, cv.ID)
	if v.State != core.StateReverted {
		t.Fatalf("undo once cleanup finished left the change %s, want reverted", v.State)
	}
	if _, ok, _ := loadAccount(context.Background(), h.db.Read()); ok {
		t.Error("the account row is still there after the undo")
	}
	if got, want := acEffects(t, h, cv.ID), []string{"create cloudflare.account:" + cv.ID + " undone local=1"}; !slices.Equal(got, want) {
		t.Errorf("effects %q after the undo, want %q", got, want)
	}
}

func TestAcDisconnect(t *testing.T) {
	srv := fake.New(t)
	h := newCFHost(t)
	acConnect(t, h, srv)
	eng := acEngine(t, h, cfWith(srv))
	ctx := context.Background()
	ref := core.ActionRef{Name: "cloudflare.account.disconnect", Version: 1}

	pv, err := eng.Plan(ctx, acAdmin, ref, json.RawMessage(`{}`), core.PlanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if pv.Confirmation != fake.AccountID {
		t.Errorf("confirmation %q, want the account id %s", pv.Confirmation, fake.AccountID)
	}
	if !strings.Contains(acDiff(pv), "- cloudflare account "+fake.AccountID) {
		t.Errorf("plan diff:\n%s", acDiff(pv))
	}
	_, err = eng.Apply(ctx, acAdmin, pv.PlanID, core.ApplyOptions{})
	if e := errcode.From(err); e.Code != errcode.PlanConfirmationNeeded {
		t.Fatalf("apply without the confirmation = %v, want %s", err, errcode.PlanConfirmationNeeded)
	}
	cv, err := eng.Apply(ctx, acAdmin, pv.PlanID, core.ApplyOptions{Confirmation: pv.Confirmation})
	if err != nil || cv.State != core.StateCompleted {
		t.Fatalf("apply = %v, %v (%+v)", cv.State, err, cv.Error)
	}
	if string(cv.Output) != `{"abandoned":[]}` {
		t.Errorf("output %s, want an empty abandoned list", cv.Output)
	}
	if _, ok, _ := loadAccount(ctx, h.db.Read()); ok {
		t.Error("the account row is still there")
	}
	_, err = eng.Plan(ctx, acAdmin, ref, json.RawMessage(`{}`), core.PlanOptions{})
	if e := errcode.From(err); e.Code != errcode.CloudNotConnected {
		t.Errorf("plan with no account = %v, want %s", err, errcode.CloudNotConnected)
	}
}

func TestAcDisconnectWithCleanupPending(t *testing.T) {
	srv := fake.New(t)
	h := newCFHost(t)
	acConnect(t, h, srv)
	acPending(t, h)
	eng := acEngine(t, h, cfWith(srv))
	ctx := context.Background()
	ref := core.ActionRef{Name: "cloudflare.account.disconnect", Version: 1}

	_, err := eng.Plan(ctx, acAdmin, ref, json.RawMessage(`{}`), core.PlanOptions{})
	e := errcode.From(err)
	if e.Code != errcode.CloudCleanupPending || !strings.Contains(e.Detail, "app.example.com") {
		t.Fatalf("plan = %v, want %s naming app.example.com", err, errcode.CloudCleanupPending)
	}

	pv, cv, err := acRun(t, eng, "cloudflare.account.disconnect", `{"abandonCleanup":true}`)
	if err != nil || cv.State != core.StateCompleted {
		t.Fatalf("disconnect with abandonCleanup = %v, %v (%+v)", cv.State, err, cv.Error)
	}
	if !strings.Contains(acDiff(pv), "abandon: ") || !strings.Contains(acDiff(pv), "app.example.com") {
		t.Errorf("plan diff does not list what is abandoned:\n%s", acDiff(pv))
	}
	var out Disconnected
	if err := json.Unmarshal(cv.Output, &out); err != nil || len(out.Abandoned) != 1 || !strings.Contains(out.Abandoned[0], "app.example.com") {
		t.Errorf("output %s (%v), want the hostname abandoned", cv.Output, err)
	}
	if _, ok, _ := loadAccount(ctx, h.db.Read()); ok {
		t.Error("the account row is still there")
	}
}

func TestAcGet(t *testing.T) {
	srv := fake.New(t)
	h := newCFHost(t)
	eng := acEngine(t, h, cfWith(srv))
	ctx := context.Background()
	ref := core.ActionRef{Name: "cloudflare.account.get", Version: 1}

	raw, err := eng.Read(ctx, acAdmin, ref, json.RawMessage(`{}`))
	if err != nil || string(raw) != `{"connected":false}` {
		t.Fatalf("read with no account = %s, %v; want {\"connected\":false}", raw, err)
	}
	acConnect(t, h, srv)
	raw, err = eng.Read(ctx, acAdmin, ref, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var st AccountStatus
	if err := json.Unmarshal(raw, &st); err != nil || !st.Connected || st.Account == nil || st.Account.AccountID != fake.AccountID ||
		len(st.Account.Capabilities) != 1 {
		t.Errorf("read = %s (%v), want the connected account with its stored capabilities", raw, err)
	}
	if strings.Contains(string(raw), fake.Token) || strings.Contains(string(raw), "token_ct") {
		t.Errorf("read %s carries credentials", raw)
	}
}

func TestAcConnected(t *testing.T) {
	srv := fake.New(t)
	h := newCFHost(t)
	c := cfWith(srv)
	ctx := context.Background()

	_, _, err := c.connected(ctx, h.db, coretest.Sealer{}, h.clock)
	if e := errcode.From(err); e.Code != errcode.CloudNotConnected {
		t.Fatalf("connected with no account = %v, want %s", err, errcode.CloudNotConnected)
	}
	want := acConnect(t, h, srv)
	a, acct, err := c.connected(ctx, h.db, coretest.Sealer{}, h.clock)
	if err != nil {
		t.Fatal(err)
	}
	if acct.ID != want.ID || acct.AccountID != fake.AccountID {
		t.Errorf("account %+v, want %+v", acct, want)
	}
	if err := a.VerifyToken(ctx); err != nil {
		t.Errorf("the api built from the stored credentials: VerifyToken = %v", err)
	}
}

func TestAcCloudEffect(t *testing.T) {
	create := cloudEffect(core.EffectCreate, "cloudflare.tunnel:?darkpanel-x", nil)
	if create.Kind != core.EffectCreate || create.Resource != "cloudflare.tunnel:?darkpanel-x" || create.Local || create.InverseLocal ||
		create.CommitWait != cloud.CommitWait || create.Recovery != nil || string(create.Before.State) != "null" {
		t.Errorf("creation effect %+v, want a non-local effect with CommitWait %s, no recovery and a null before-state", create, cloud.CommitWait)
	}
	modify := cloudEffect(core.EffectModify, "cloudflare.tunnel:t1", json.RawMessage(`{"a":1}`))
	if string(modify.Before.State) != `{"a":1}` || modify.Local || modify.CommitWait != cloud.CommitWait {
		t.Errorf("modify effect %+v, want the given before-state", modify)
	}
}

func TestAcDefinitions(t *testing.T) {
	c := cfWith(fake.New(t))
	tests := []struct {
		def    core.ActionDef
		name   string
		class  core.Class
		locks  []string
		undoer bool
	}{
		{c.accountConnect(), "cloudflare.account.connect", core.ClassCompensable, []string{"cloudflare"}, true},
		{c.accountDisconnect(), "cloudflare.account.disconnect", core.ClassIrreversible, []string{"cloudflare"}, false},
		{c.accountGet(), "cloudflare.account.get", core.ClassRead, nil, false},
	}
	for _, tt := range tests {
		s := tt.def.Spec
		if s.Name != tt.name || s.Version != 1 || s.Class != tt.class || s.Side != core.SideController || !slices.Equal(s.LockKeys, tt.locks) ||
			(tt.def.Handler.Inverse != nil) != tt.undoer {
			t.Errorf("%s: spec %+v, undo handler %v; want class %s, locks %v, undo handler %v", tt.name, s, tt.def.Handler.Inverse != nil, tt.class, tt.locks, tt.undoer)
		}
	}
}
