package tailnet

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
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
	"github.com/darkraise/darkpanel/internal/modules/tailnet/fake"
)

// acModule registers the three account actions the way the module's
// manifest will, so the registry's own rules check their declarations and
// a controller engine can run them.
type acModule struct{ t tn }

func (m acModule) Manifest() core.Manifest {
	return core.Manifest{ID: ID, Version: 1, Actions: []core.ActionDef{
		m.t.accountConnect(), m.t.accountDisconnect(), m.t.accountGet(),
	}}
}

// acConnect inserts a connected account whose secret is the fake's client
// secret, sealed as the connect action seals it.
func acConnect(t *testing.T, h *tnHost, _ *fake.Server) account {
	t.Helper()
	ctx := context.Background()
	ct, err := cloud.SealCredential(coretest.Sealer{}, "tailnet_accounts", "client_secret_ct", "ACCT1", []byte(fake.ClientSecret))
	if err != nil {
		t.Fatal(err)
	}
	a := account{
		ID: "ACCT1", Tailnet: fake.Tailnet, ClientID: fake.ClientID, SecretCT: ct, TagsAllowed: []string{"tag:server"},
		Capabilities: []cloud.Capability{{Name: "devices:core:read", State: cloud.CapabilityOK}}, ConnectedAt: h.clock.Now(),
	}
	if err := h.db.Write(ctx, func(tx *sql.Tx) error { return insertAccount(ctx, tx, a) }); err != nil {
		t.Fatal(err)
	}
	return a
}

// acInput is a valid connect input for the fake's client; the tags are
// out of order and repeated on purpose.
func acInput(over func(*ConnectInput)) json.RawMessage {
	in := ConnectInput{
		Tailnet: fake.Tailnet, ClientID: fake.ClientID, ClientSecret: fake.ClientSecret,
		TagsAllowed: []string{"tag:web", "tag:server", "tag:web"},
	}
	if over != nil {
		over(&in)
	}
	b, err := json.Marshal(in) //nolint:gosec // G117: the connect input carries the fake's client secret on purpose
	if err != nil {
		panic(err)
	}
	return b
}

func acPending(t *testing.T, h *tnHost, description, state string) {
	t.Helper()
	ctx := context.Background()
	k := keyRow{
		Description: description, Kind: "change", OwnerID: "CHG", State: state, RequestedAt: h.clock.Now(), UpdatedAt: h.clock.Now(),
		ExpirySeconds: 3600, Tags: []string{"tag:server"},
	}
	if err := h.db.Write(ctx, func(tx *sql.Tx) error { return insertKey(ctx, tx, k) }); err != nil {
		t.Fatal(err)
	}
}

func acCode(t *testing.T, err error) *errcode.Error {
	t.Helper()
	var coded *errcode.Error
	if !errors.As(err, &coded) {
		t.Fatalf("error %v is not an *errcode.Error", err)
	}
	return coded
}

func acCapabilities(caps []cloud.Capability) []string {
	out := make([]string, len(caps))
	for i, c := range caps {
		out[i] = c.Name + "=" + c.State
	}
	return out
}

func TestACActionSpecs(t *testing.T) {
	m := acModule{t: tnWith(fake.New(t))}
	tests := []struct {
		def     core.ActionDef
		name    string
		class   core.Class
		locks   []string
		undo    bool
		minimum core.Scope
	}{
		{m.t.accountConnect(), "tailnet.account.connect", core.ClassCompensable, []string{"tailnet"}, true, core.ScopeAdmin},
		{m.t.accountDisconnect(), "tailnet.account.disconnect", core.ClassIrreversible, []string{"tailnet"}, false, core.ScopeAdmin},
		{m.t.accountGet(), "tailnet.account.get", core.ClassRead, nil, false, core.ScopeRead},
	}
	for _, tc := range tests {
		s := tc.def.Spec
		if s.Name != tc.name || s.Version != 1 || s.Class != tc.class || s.Side != core.SideController || !slices.Equal(s.LockKeys, tc.locks) {
			t.Errorf("%s: spec %+v, want version 1, class %s on the controller with locks %q", tc.name, s, tc.class, tc.locks)
		}
		if (tc.def.Handler.Inverse != nil) != tc.undo || (tc.undo && tc.def.Handler.Observe == nil) || s.CompensatedBy != "" {
			t.Errorf("%s: undo handler %v, want %v with an observer and no compensating action", tc.name, tc.def.Handler.Inverse != nil, tc.undo)
		}
		if got := core.RequiredScope(s); got != tc.minimum {
			t.Errorf("%s: scope %s, want %s", tc.name, got, tc.minimum)
		}
	}
	if _, err := core.NewRegistry(m); err != nil {
		t.Errorf("NewRegistry: %v", err)
	}
}

func TestACCloudEffect(t *testing.T) {
	e := cloudEffect(core.EffectModify, "tailnet.device:n1", json.RawMessage(`{"authorized":false}`))
	if e.Kind != core.EffectModify || e.Resource != "tailnet.device:n1" || e.Local || e.InverseLocal || e.CommitWait != cloud.CommitWait {
		t.Errorf("effect %+v, want a non-local modify of tailnet.device:n1 with the cloud commit wait", e)
	}
	if string(e.Before.State) != `{"authorized":false}` {
		t.Errorf("before %s, want the given state", e.Before.State)
	}
}

func TestACPlanRendersTheConnection(t *testing.T) {
	srv := fake.New(t)
	h := newTNHost(t)
	def := tnWith(srv).accountConnect()
	out, err := def.Handler.Plan(context.Background(), h.env(""), acInput(nil))
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	want := []core.DiffLine{
		{Op: "+", Text: "connect tailnet " + fake.Tailnet + " with OAuth client " + fake.ClientID},
		{Op: "+", Text: "tags the client may assign: tag:server, tag:web"},
		{Op: " ", Text: "capability devices:core:read: ok"},
		{Op: " ", Text: "capability auth_keys:read: ok"},
		{Op: " ", Text: "capability policy_file:read: ok"},
		{Op: " ", Text: "capability devices:core: unverified"},
		{Op: " ", Text: "capability devices:routes: unverified"},
		{Op: " ", Text: "capability auth_keys: unverified"},
		{Op: " ", Text: "capability policy_file: unverified"},
		{Op: " ", Text: "undo deletes the stored credentials once no provider cleanup is pending"},
	}
	if !slices.Equal(out.Diff, want) {
		t.Errorf("diff %+v, want %+v", out.Diff, want)
	}
	if out.StateHash == "" || out.Confirmation != "" {
		t.Errorf("state hash %q and confirmation %q, want a hash and no confirmation", out.StateHash, out.Confirmation)
	}
	if b, _ := json.Marshal(out); strings.Contains(string(b), fake.ClientSecret) {
		t.Errorf("the plan %s carries the client secret", b)
	}
	again, err := def.Handler.Plan(context.Background(), h.env(""), acInput(nil))
	if err != nil || again.StateHash != out.StateHash {
		t.Errorf("a second plan hashes %q (%v), want %q: capabilities must not move the hash", again.StateHash, err, out.StateHash)
	}
}

func TestACPlanReportsAMissingReadScope(t *testing.T) {
	srv := fake.New(t)
	srv.SetScopes("devices:core", "devices:routes", "auth_keys")
	h := newTNHost(t)
	out, err := tnWith(srv).accountConnect().Handler.Plan(context.Background(), h.env(""), acInput(nil))
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	for _, l := range []string{
		"capability devices:core:read: ok",
		"capability auth_keys:read: ok",
		"capability policy_file:read: missing (cloud.permission_missing)",
	} {
		if !slices.Contains(out.Diff, core.DiffLine{Op: " ", Text: l}) {
			t.Errorf("diff %+v lacks %q", out.Diff, l)
		}
	}
}

func TestACPlanRefusals(t *testing.T) {
	tests := []struct {
		name  string
		over  func(*ConnectInput)
		code  errcode.Code
		field string
	}{
		{"empty tailnet", func(in *ConnectInput) { in.Tailnet = "" }, errcode.InvalidInput, "/tailnet"},
		{"tailnet with a slash", func(in *ConnectInput) { in.Tailnet = "a/b" }, errcode.InvalidInput, "/tailnet"},
		{"empty client id", func(in *ConnectInput) { in.ClientID = "" }, errcode.InvalidInput, "/clientId"},
		{"empty client secret", func(in *ConnectInput) { in.ClientSecret = "" }, errcode.InvalidInput, "/clientSecret"},
		{"no tags", func(in *ConnectInput) { in.TagsAllowed = nil }, errcode.InvalidInput, "/tagsAllowed"},
		{"tag without the prefix", func(in *ConnectInput) { in.TagsAllowed = []string{"tag:ok", "server"} }, errcode.InvalidInput, "/tagsAllowed/1"},
		{"tag with a space", func(in *ConnectInput) { in.TagsAllowed = []string{"tag:a b"} }, errcode.InvalidInput, "/tagsAllowed/0"},
		{"the provider refuses the client", func(in *ConnectInput) { in.ClientSecret = "wrong" }, errcode.CloudAuthFailed, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newTNHost(t)
			_, err := tnWith(fake.New(t)).accountConnect().Handler.Plan(context.Background(), h.env(""), acInput(tc.over))
			coded := acCode(t, err)
			if coded.Code != tc.code || !core.IsDefinite(err) {
				t.Fatalf("Plan = %v (definite %v), want %s as a definite refusal", err, core.IsDefinite(err), tc.code)
			}
			if tc.field != "" && !slices.ContainsFunc(coded.Fields, func(f errcode.FieldError) bool { return f.Path == tc.field }) {
				t.Errorf("fields %+v, want one at %s", coded.Fields, tc.field)
			}
			if strings.Contains(coded.Detail, "wrong") {
				t.Errorf("detail %q carries the secret", coded.Detail)
			}
		})
	}
}

func TestACPlanRefusesASecondTailnet(t *testing.T) {
	srv := fake.New(t)
	h := newTNHost(t)
	acConnect(t, h, srv)
	_, err := tnWith(srv).accountConnect().Handler.Plan(context.Background(), h.env(""), acInput(nil))
	if coded := acCode(t, err); coded.Code != errcode.CloudConflict || !core.IsDefinite(err) {
		t.Errorf("Plan = %v, want a definite cloud.conflict", err)
	}
}

func TestACApplyStoresTheAccountAndJournalsIt(t *testing.T) {
	ctx := context.Background()
	srv := fake.New(t)
	h := newTNHost(t)
	def := tnWith(srv).accountConnect()
	env := h.env("CHG1")
	raw, err := def.Handler.Apply(ctx, env, acInput(nil))
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	var view AccountView
	if err := json.Unmarshal(raw, &view); err != nil {
		t.Fatal(err)
	}
	wantCaps := []string{
		"devices:core:read=ok", "auth_keys:read=ok", "policy_file:read=ok",
		"devices:core=unverified", "devices:routes=unverified", "auth_keys=unverified", "policy_file=unverified",
	}
	if view.Tailnet != fake.Tailnet || view.ClientID != fake.ClientID || !slices.Equal(view.TagsAllowed, []string{"tag:server", "tag:web"}) ||
		!slices.Equal(acCapabilities(view.Capabilities), wantCaps) || !view.ConnectedAt.Equal(h.clock.Now()) {
		t.Errorf("view %+v, want the tailnet, client, sorted tags, %q and the clock's time", view, wantCaps)
	}
	if strings.Contains(string(raw), fake.ClientSecret) {
		t.Errorf("the output %s carries the client secret", raw)
	}

	a, ok, err := loadAccount(ctx, h.db.Read())
	if err != nil || !ok {
		t.Fatalf("loadAccount = %v, %v, want the row", ok, err)
	}
	if a.ID != "CHG1" || a.Tailnet != fake.Tailnet || a.ClientID != fake.ClientID || !slices.Equal(acCapabilities(a.Capabilities), wantCaps) {
		t.Errorf("stored account %+v", a)
	}
	if strings.Contains(string(a.SecretCT), fake.ClientSecret) {
		t.Error("the stored secret is not sealed")
	}
	secret, err := cloud.OpenCredential(coretest.Sealer{}, "tailnet_accounts", "client_secret_ct", "CHG1", a.SecretCT)
	if err != nil || string(secret) != fake.ClientSecret {
		t.Errorf("the sealed secret opens to %q (%v), want the client secret", secret, err)
	}

	effs := h.journal.effects()
	if len(effs) != 1 {
		t.Fatalf("%d effects, want one", len(effs))
	}
	e := effs[0]
	if e.Kind != core.EffectCreate || e.Resource != "tailnet.account:CHG1" || !e.Local || !e.InverseLocal ||
		e.Recovery == nil || e.Recovery.Kind != "name" || e.Recovery.Value != "CHG1" {
		t.Errorf("effect %+v, want a local create of tailnet.account:CHG1 recovered by name CHG1", e)
	}
	if string(e.Before.State) != "null" || e.Post == nil {
		t.Errorf("effect before %s and post %+v, want null before and a post-state", e.Before.State, e.Post)
	}
	var post struct {
		ID          string   `json:"id"`
		Tailnet     string   `json:"tailnet"`
		ClientID    string   `json:"clientId"`
		TagsAllowed []string `json:"tagsAllowed"`
	}
	if err := json.Unmarshal(e.Post.State, &post); err != nil {
		t.Fatal(err)
	}
	if post.ID != "CHG1" || post.Tailnet != fake.Tailnet || post.ClientID != fake.ClientID || !slices.Equal(post.TagsAllowed, []string{"tag:server", "tag:web"}) {
		t.Errorf("post-state %s", e.Post.State)
	}
	if b, _ := json.Marshal(effs); strings.Contains(string(b), fake.ClientSecret) {
		t.Errorf("the journal %s carries the client secret", b)
	}

	if err := def.Handler.Verify(ctx, env, acInput(nil), json.RawMessage("{}")); err != nil {
		t.Errorf("Verify after Apply: %v", err)
	}
	if err := def.Handler.Verify(ctx, h.env("OTHER"), acInput(nil), json.RawMessage("{}")); err == nil {
		t.Error("Verify passed for a change that stored no account")
	}
}

func TestACApplyRefusalsJournalNothing(t *testing.T) {
	ctx := context.Background()
	t.Run("provider refuses the client", func(t *testing.T) {
		h := newTNHost(t)
		_, err := tnWith(fake.New(t)).accountConnect().Handler.Apply(ctx, h.env("CHG1"),
			acInput(func(in *ConnectInput) { in.ClientSecret = "wrong" }))
		if coded := acCode(t, err); coded.Code != errcode.CloudAuthFailed || !core.IsDefinite(err) {
			t.Errorf("Apply = %v, want a definite cloud.auth_failed", err)
		}
		acNothingStored(t, h)
	})
	t.Run("an account exists", func(t *testing.T) {
		srv := fake.New(t)
		h := newTNHost(t)
		acConnect(t, h, srv)
		_, err := tnWith(srv).accountConnect().Handler.Apply(ctx, h.env("CHG1"), acInput(nil))
		if coded := acCode(t, err); coded.Code != errcode.CloudConflict || !core.IsDefinite(err) {
			t.Errorf("Apply = %v, want a definite cloud.conflict", err)
		}
		if len(h.journal.effects()) != 0 {
			t.Error("an effect was journaled for a refused connect")
		}
	})
}

func acNothingStored(t *testing.T, h *tnHost) {
	t.Helper()
	if _, ok, err := loadAccount(context.Background(), h.db.Read()); err != nil || ok {
		t.Errorf("loadAccount = %v, %v, want no account", ok, err)
	}
	if len(h.journal.effects()) != 0 {
		t.Error("an effect was journaled for a refused connect")
	}
}

func TestACObserveFindsTheRowByTheChangeId(t *testing.T) {
	ctx := context.Background()
	h := newTNHost(t)
	def := tnWith(fake.New(t)).accountConnect()
	env := h.env("CHG1")
	unnamed := core.Effect{
		ChangeID: "CHG1", Seq: 1, Kind: core.EffectCreate, Resource: "tailnet.account:?CHG1",
		Recovery: &core.RecoveryID{Kind: "name", Value: "CHG1"}, Local: true, InverseLocal: true,
	}
	obs, err := def.Handler.Observe(ctx, env, unnamed)
	if err != nil || obs.Matches != 0 || string(obs.Current.State) != "null" {
		t.Errorf("Observe before Apply = %+v, %v, want no match and a null state", obs, err)
	}
	if _, err := def.Handler.Apply(ctx, env, acInput(nil)); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	obs, err = def.Handler.Observe(ctx, env, unnamed)
	if err != nil || obs.Matches != 1 || obs.Resource != "tailnet.account:CHG1" || !obs.Exclusive {
		t.Fatalf("Observe of the unnamed creation after Apply = %+v, %v, want one exclusive match", obs, err)
	}
	made := h.journal.effects()[0]
	obs, err = def.Handler.Observe(ctx, env, made)
	if err != nil || string(obs.Current.State) != string(made.Post.State) {
		t.Errorf("Observe of the made effect = %s (%v), want its post-state %s", obs.Current.State, err, made.Post.State)
	}
}

func TestACInverseDeletesTheRowUnlessCleanupIsPending(t *testing.T) {
	ctx := context.Background()
	h := newTNHost(t)
	def := tnWith(fake.New(t)).accountConnect()
	env := h.env("CHG1")
	if _, err := def.Handler.Apply(ctx, env, acInput(nil)); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	e := h.journal.effects()[0]
	acPending(t, h, "darkpanel-CHG0", "uncertain")

	_, err := def.Handler.Inverse(ctx, env, e)
	if coded := acCode(t, err); coded.Code != errcode.CloudCleanupPending || !core.IsDefinite(err) {
		t.Fatalf("Inverse = %v, want a definite cloud.cleanup_pending", err)
	}
	if _, ok, _ := loadAccount(ctx, h.db.Read()); !ok {
		t.Fatal("the account was deleted although cleanup is pending")
	}

	err = h.db.Write(ctx, func(tx *sql.Tx) error { return setKeyState(ctx, tx, "darkpanel-CHG0", "deleted", "", h.clock.Now()) })
	if err != nil {
		t.Fatal(err)
	}
	res, err := def.Handler.Inverse(ctx, env, e)
	if err != nil || res.Resource != "" {
		t.Fatalf("Inverse after cleanup = %+v, %v, want success", res, err)
	}
	if _, ok, _ := loadAccount(ctx, h.db.Read()); ok {
		t.Error("the account is still stored after the inverse")
	}
	if _, err := def.Handler.Inverse(ctx, env, e); err != nil {
		t.Errorf("a second Inverse = %v, want success: the row is already gone", err)
	}
}

func TestACDisconnectPlans(t *testing.T) {
	ctx := context.Background()
	srv := fake.New(t)
	h := newTNHost(t)
	def := tnWith(srv).accountDisconnect()

	_, err := def.Handler.Plan(ctx, h.env(""), json.RawMessage("{}"))
	if coded := acCode(t, err); coded.Code != errcode.CloudNotConnected {
		t.Errorf("Plan without an account = %v, want cloud.not_connected", err)
	}

	acConnect(t, h, srv)
	out, err := def.Handler.Plan(ctx, h.env(""), json.RawMessage("{}"))
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if out.Confirmation != fake.Tailnet {
		t.Errorf("confirmation %q, want the tailnet name %q", out.Confirmation, fake.Tailnet)
	}
	wantLine := core.DiffLine{Op: "-", Text: "disconnect tailnet " + fake.Tailnet + " (OAuth client " + fake.ClientID + "): the stored credentials are deleted"}
	if !slices.Contains(out.Diff, wantLine) || !slices.Contains(out.Diff, core.DiffLine{Op: " ", Text: "keys and devices in the tailnet stay as they are"}) {
		t.Errorf("diff %+v, want %+v and the line saying the tailnet is untouched", out.Diff, wantLine)
	}

	acPending(t, h, "darkpanel-CHG0", "retired")
	_, err = def.Handler.Plan(ctx, h.env(""), json.RawMessage("{}"))
	coded := acCode(t, err)
	if coded.Code != errcode.CloudCleanupPending || !core.IsDefinite(err) || !strings.Contains(coded.Detail, "darkpanel-CHG0") {
		t.Errorf("Plan with cleanup pending = %v, want a definite cloud.cleanup_pending naming the key", err)
	}

	out, err = def.Handler.Plan(ctx, h.env(""), json.RawMessage(`{"abandonCleanup":true}`))
	if err != nil {
		t.Fatalf("Plan with abandonCleanup: %v", err)
	}
	if !slices.ContainsFunc(out.Diff, func(l core.DiffLine) bool {
		return l.Op == "-" && strings.HasPrefix(l.Text, "abandons pending cleanup: ") && strings.Contains(l.Text, "darkpanel-CHG0")
	}) {
		t.Errorf("diff %+v lacks the abandoned item", out.Diff)
	}
}

func TestACDisconnectApply(t *testing.T) {
	ctx := context.Background()
	t.Run("nothing pending", func(t *testing.T) {
		srv := fake.New(t)
		h := newTNHost(t)
		acConnect(t, h, srv)
		def := tnWith(srv).accountDisconnect()
		raw, err := def.Handler.Apply(ctx, h.env("CHG2"), json.RawMessage("{}"))
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		if string(raw) != `{"abandoned":[]}` {
			t.Errorf("output %s, want {\"abandoned\":[]}", raw)
		}
		if _, ok, _ := loadAccount(ctx, h.db.Read()); ok {
			t.Error("the account is still stored")
		}
		if err := def.Handler.Verify(ctx, h.env("CHG2"), json.RawMessage("{}"), raw); err != nil {
			t.Errorf("Verify after Apply: %v", err)
		}
	})
	t.Run("cleanup pending", func(t *testing.T) {
		srv := fake.New(t)
		h := newTNHost(t)
		acConnect(t, h, srv)
		acPending(t, h, "darkpanel-CHG0", "uncertain")
		def := tnWith(srv).accountDisconnect()
		_, err := def.Handler.Apply(ctx, h.env("CHG2"), json.RawMessage("{}"))
		if coded := acCode(t, err); coded.Code != errcode.CloudCleanupPending {
			t.Fatalf("Apply = %v, want cloud.cleanup_pending", err)
		}
		if _, ok, _ := loadAccount(ctx, h.db.Read()); !ok {
			t.Fatal("the account was deleted although cleanup is pending")
		}
		if err := def.Handler.Verify(ctx, h.env("CHG2"), json.RawMessage("{}"), json.RawMessage("{}")); err == nil {
			t.Error("Verify passed with the account still stored")
		}

		var pending []string
		if err := h.db.Write(ctx, func(tx *sql.Tx) (err error) { pending, err = cleanupPending(ctx, tx); return err }); err != nil || len(pending) == 0 {
			t.Fatalf("cleanupPending = %v, %v, want the key", pending, err)
		}
		raw, err := def.Handler.Apply(ctx, h.env("CHG3"), json.RawMessage(`{"abandonCleanup":true}`))
		if err != nil {
			t.Fatalf("Apply with abandonCleanup: %v", err)
		}
		var out Disconnected
		if err := json.Unmarshal(raw, &out); err != nil || !slices.Equal(out.Abandoned, pending) {
			t.Errorf("abandoned %q (%v), want %q", out.Abandoned, err, pending)
		}
		if _, ok, _ := loadAccount(ctx, h.db.Read()); ok {
			t.Error("the account is still stored after abandoning the cleanup")
		}
		if _, ok, err := loadKey(ctx, h.db.Read(), "darkpanel-CHG0"); err != nil || !ok {
			t.Errorf("the key row is gone (%v, %v): disconnect must leave it for a later reconnect", ok, err)
		}
	})
}

func TestACAccountGet(t *testing.T) {
	ctx := context.Background()
	srv := fake.New(t)
	h := newTNHost(t)
	def := tnWith(srv).accountGet()
	read := func() AccountStatus {
		t.Helper()
		raw, err := def.Handler.Read(ctx, h.env(""), json.RawMessage("{}"))
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		var st AccountStatus
		if err := json.Unmarshal(raw, &st); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), fake.ClientSecret) || strings.Contains(string(raw), "secret") {
			t.Errorf("the status %s mentions a secret", raw)
		}
		return st
	}
	if st := read(); st.Connected || st.Account != nil {
		t.Errorf("status %+v without an account, want not connected", st)
	}
	a := acConnect(t, h, srv)
	st := read()
	if !st.Connected || st.Account == nil || st.Account.Tailnet != a.Tailnet || st.Account.ClientID != a.ClientID ||
		!slices.Equal(st.Account.TagsAllowed, a.TagsAllowed) || !slices.Equal(acCapabilities(st.Account.Capabilities), acCapabilities(a.Capabilities)) {
		t.Errorf("status %+v, want the stored account %+v", st, a)
	}
}

func TestACConnectedBuildsAnAPIFromTheStoredAccount(t *testing.T) {
	ctx := context.Background()
	srv := fake.New(t)
	h := newTNHost(t)
	t0 := tnWith(srv)
	_, _, err := t0.connected(ctx, h.db, coretest.Sealer{}, h.clock)
	if coded := acCode(t, err); coded.Code != errcode.CloudNotConnected {
		t.Fatalf("connected without an account = %v, want cloud.not_connected", err)
	}
	want := acConnect(t, h, srv)
	srv.AddDevice(fake.Device{Name: "web-1.example.ts.net", Hostname: "web-1", Authorized: true})
	a, got, err := t0.connected(ctx, h.db, coretest.Sealer{}, h.clock)
	if err != nil || got.ID != want.ID || got.Tailnet != want.Tailnet {
		t.Fatalf("connected = %+v, %v, want account %s", got, err, want.ID)
	}
	devices, err := a.Devices(ctx)
	if err != nil || len(devices) != 1 || devices[0].Hostname != "web-1" {
		t.Errorf("Devices = %+v, %v, want the fake's device: the stored secret must open to the fake's client secret", devices, err)
	}
}

// acEngine is a controller engine over the host's database serving the
// account actions.
func acEngine(t *testing.T, h *tnHost, srv *fake.Server) *lifecycle.Engine {
	t.Helper()
	reg, err := core.NewRegistry(acModule{t: tnWith(srv)})
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

var acAdmin = core.Principal{Actor: "user:u1", UserID: "u1", Scope: core.ScopeAdmin, Source: "203.0.113.7"}

func acWait(t *testing.T, eng *lifecycle.Engine, id string) core.ChangeView {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		v, err := eng.Change(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if v.State != core.StateUndoing {
			return v
		}
		if time.Now().After(deadline) {
			t.Fatalf("change %s is still undoing", id)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func acConnectThroughTheEngine(t *testing.T, eng *lifecycle.Engine) core.ChangeView {
	t.Helper()
	ctx := context.Background()
	pv, err := eng.Plan(ctx, acAdmin, core.ActionRef{Name: "tailnet.account.connect", Version: 1}, acInput(nil), core.PlanOptions{})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	cv, err := eng.Apply(ctx, acAdmin, pv.PlanID, core.ApplyOptions{})
	if err != nil || cv.State != core.StateCompleted {
		t.Fatalf("Apply = %+v, %v, want a completed change", cv, err)
	}
	return cv
}

func TestACEngineConnectsAndUndoes(t *testing.T) {
	ctx := context.Background()
	srv := fake.New(t)
	h := newTNHost(t)
	eng := acEngine(t, h, srv)
	cv := acConnectThroughTheEngine(t, eng)
	if strings.Contains(string(cv.Output), fake.ClientSecret) {
		t.Errorf("the change output %s carries the client secret", cv.Output)
	}
	if a, ok, err := loadAccount(ctx, h.db.Read()); err != nil || !ok || a.ID != cv.ID {
		t.Fatalf("loadAccount = %+v, %v, %v, want the row of change %s", a, ok, err, cv.ID)
	}

	if _, err := eng.Undo(ctx, acAdmin, cv.ID, nil); err != nil {
		t.Fatalf("Undo: %v", err)
	}
	if v := acWait(t, eng, cv.ID); v.State != core.StateReverted {
		t.Fatalf("change is %s after the undo, want reverted", v.State)
	}
	if _, ok, _ := loadAccount(ctx, h.db.Read()); ok {
		t.Error("the account is still stored after the undo")
	}
	effs, err := eng.Effects(ctx, cv.ID)
	if err != nil || len(effs) != 1 || effs[0].State != core.EffectUndone {
		t.Errorf("effects %+v (%v), want one undone effect", effs, err)
	}
}

func TestACEngineUndoWithCleanupPendingNeedsRepair(t *testing.T) {
	ctx := context.Background()
	srv := fake.New(t)
	h := newTNHost(t)
	eng := acEngine(t, h, srv)
	cv := acConnectThroughTheEngine(t, eng)
	acPending(t, h, "darkpanel-CHG0", "uncertain")

	if _, err := eng.Undo(ctx, acAdmin, cv.ID, nil); err != nil {
		t.Fatalf("Undo: %v", err)
	}
	if v := acWait(t, eng, cv.ID); v.State != core.StateNeedsRepair {
		t.Fatalf("change is %s, want needs_repair", v.State)
	}
	if _, ok, _ := loadAccount(ctx, h.db.Read()); !ok {
		t.Error("the account was deleted although cleanup is pending")
	}
	effs, err := eng.Effects(ctx, cv.ID)
	if err != nil || len(effs) != 1 || effs[0].State != core.EffectMade {
		t.Errorf("effects %+v (%v), want one effect still made", effs, err)
	}
}

func TestACEngineDisconnectNeedsTheTailnetName(t *testing.T) {
	ctx := context.Background()
	srv := fake.New(t)
	h := newTNHost(t)
	eng := acEngine(t, h, srv)
	acConnectThroughTheEngine(t, eng)

	pv, err := eng.Plan(ctx, acAdmin, core.ActionRef{Name: "tailnet.account.disconnect", Version: 1}, json.RawMessage("{}"), core.PlanOptions{})
	if err != nil || pv.Confirmation != fake.Tailnet {
		t.Fatalf("Plan = %+v, %v, want the confirmation %q", pv, err, fake.Tailnet)
	}
	if _, err := eng.Apply(ctx, acAdmin, pv.PlanID, core.ApplyOptions{}); acCode(t, err).Code != errcode.PlanConfirmationNeeded {
		t.Fatalf("Apply without the confirmation = %v, want plan.confirmation_required", err)
	}
	cv, err := eng.Apply(ctx, acAdmin, pv.PlanID, core.ApplyOptions{Confirmation: fake.Tailnet})
	if err != nil || cv.State != core.StateCompleted {
		t.Fatalf("Apply = %+v, %v, want completed", cv, err)
	}
	if _, ok, _ := loadAccount(ctx, h.db.Read()); ok {
		t.Error("the account is still stored")
	}
}
