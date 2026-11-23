package auth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
	"github.com/darkraise/darkpanel/internal/core/schema"
)

var settingsAdmin = core.Principal{Actor: "user:u1", UserID: "u1", Scope: core.ScopeAdmin, Source: "203.0.113.7"}

const testClientSecret = "client-secret-value-42"

type settingsEnv struct {
	t   *testing.T
	s   *Service
	reg *core.Registry
	eng *lifecycle.Engine
}

func newSettingsEnv(t *testing.T) *settingsEnv {
	t.Helper()
	s, clock, _ := newTestService(t)
	origins := func(context.Context) ([]string, error) {
		return []string{"https://100.64.0.1:8443", "https://panel.example.com"}, nil
	}
	reg, err := core.NewRegistry(Module(origins))
	if err != nil {
		t.Fatal(err)
	}
	eng, err := lifecycle.New(lifecycle.Config{
		Side: core.SideController, Registry: reg, Validator: schema.NewValidator(), Journal: &coretest.MemJournal{}, Redact: schema.Redact,
		Base: core.Env{DB: s.db, Sealer: coretest.Sealer{}, Clock: clock, Log: slog.New(slog.NewTextHandler(io.Discard, nil))},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(eng.Close)
	return &settingsEnv{t: t, s: s, reg: reg, eng: eng}
}

// run plans and applies name with input as an admin.
func (e *settingsEnv) run(name, input string) (core.ChangeView, error) {
	ctx := context.Background()
	pv, err := e.eng.Plan(ctx, settingsAdmin, core.ActionRef{Name: name, Version: 1}, json.RawMessage(input), core.PlanOptions{})
	if err != nil {
		return core.ChangeView{}, err
	}
	return e.eng.Apply(ctx, settingsAdmin, pv.PlanID, core.ApplyOptions{})
}

func (e *settingsEnv) mustRun(name, input string) core.ChangeView {
	e.t.Helper()
	cv, err := e.run(name, input)
	if err != nil || cv.State != core.StateCompleted {
		e.t.Fatalf("%s: %+v, %v; want a completed change", name, cv, err)
	}
	return cv
}

func (e *settingsEnv) get() (SignInSettings, string) {
	e.t.Helper()
	raw, err := e.eng.Read(context.Background(), settingsAdmin, core.ActionRef{Name: "auth.settings.get", Version: 1}, json.RawMessage(`{}`))
	if err != nil {
		e.t.Fatal(err)
	}
	var out SignInSettings
	if err := json.Unmarshal(raw, &out); err != nil {
		e.t.Fatal(err)
	}
	return out, string(raw)
}

// storedSecret opens the stored OIDC client secret.
func (e *settingsEnv) storedSecret() string {
	e.t.Helper()
	var ct []byte
	if err := e.s.db.Read().QueryRow(`SELECT oidc_client_secret_ct FROM auth_settings WHERE id = 1`).Scan(&ct); err != nil {
		e.t.Fatal(err)
	}
	if len(ct) == 0 {
		return ""
	}
	if strings.Contains(string(ct), testClientSecret) {
		e.t.Error("the client secret is stored in plain text")
	}
	plain, err := coretest.Sealer{}.Open(oidcSecretAD, ct)
	if err != nil {
		e.t.Fatal(err)
	}
	return string(plain)
}

func (e *settingsEnv) undo(id string) {
	e.t.Helper()
	ctx := context.Background()
	if _, err := e.eng.Undo(ctx, settingsAdmin, id, nil); err != nil {
		e.t.Fatalf("undo %s: %v", id, err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		v, err := e.eng.Change(ctx, id)
		if err != nil {
			e.t.Fatal(err)
		}
		if v.State != core.StateUndoing {
			return
		}
		if time.Now().After(deadline) {
			e.t.Fatalf("change %s is still undoing", id)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func oidcInput(issuer, name, base, secret string) string {
	in := map[string]any{"issuer": issuer, "clientId": "darkpanel", "displayName": name, "redirectBase": base}
	if secret != "" {
		in["clientSecret"] = secret
	}
	raw, _ := json.Marshal(in)
	return string(raw)
}

func TestOIDCSettings(t *testing.T) {
	e := newSettingsEnv(t)
	for name, c := range map[string]struct{ input, path string }{
		"http issuer":          {oidcInput("http://idp.example", "Sign in with IdP", "https://panel.example.com", testClientSecret), "/issuer"},
		"long display name":    {oidcInput("https://idp.example", strings.Repeat("n", 33), "https://panel.example.com", testClientSecret), "/displayName"},
		"unknown origin":       {oidcInput("https://idp.example", "Sign in", "https://elsewhere.example", testClientSecret), "/redirectBase"},
		"redirect with path":   {oidcInput("https://idp.example", "Sign in", "https://panel.example.com/login", testClientSecret), "/redirectBase"},
		"no secret stored":     {oidcInput("https://idp.example", "Sign in", "https://panel.example.com", ""), "/clientSecret"},
		"http listener origin": {oidcInput("https://idp.example", "Sign in", "http://100.64.0.1:8443", testClientSecret), "/redirectBase"},
	} {
		_, err := e.run("auth.oidc.set", c.input)
		var ce *errcode.Error
		if err == nil || !errors.As(err, &ce) || ce.Code != errcode.InvalidInput {
			t.Errorf("%s: %v, want an invalid input", name, err)
			continue
		}
		if len(ce.Fields) == 0 || ce.Fields[0].Path != c.path {
			t.Errorf("%s: fields %+v, want %s", name, ce.Fields, c.path)
		}
		if strings.Contains(err.Error(), testClientSecret) {
			t.Errorf("%s: the error %v quotes the client secret", name, err)
		}
	}
	if got, _ := e.get(); got.OIDC != nil {
		t.Fatalf("OIDC after refused sets = %+v, want none", got.OIDC)
	}

	cv := e.mustRun("auth.oidc.set", oidcInput("https://idp.example", "Sign in with IdP", "https://PANEL.example.com", testClientSecret))
	if strings.Contains(string(cv.Output), testClientSecret) {
		t.Errorf("the change output %s carries the client secret", cv.Output)
	}
	got, raw := e.get()
	if got.OIDC == nil || got.OIDC.Issuer != "https://idp.example" || !got.OIDC.ClientSecretSet || strings.Contains(raw, testClientSecret) {
		t.Fatalf("auth.settings.get = %s, want the provider with clientSecretSet and no secret", raw)
	}
	if secret := e.storedSecret(); secret != testClientSecret {
		t.Fatalf("stored secret opens to %q, want the input's", secret)
	}

	// A user pinned to the first issuer keeps the pin when the issuer
	// changes: sign-in refuses it until an admin changes the user's email.
	u, _ := createActive(t, e.s, coretest.NewFakeClock(t0), "pinned")
	exec(t, e.s, `UPDATE core_users SET email = 'pinned@example.com', oidc_issuer = 'https://idp.example', oidc_subject = 'sub-1' WHERE id = ?`, u.ID)
	pin := func() string {
		var s string
		if err := e.s.db.Read().QueryRow(`SELECT oidc_issuer || '|' || oidc_subject FROM core_users WHERE id = ?`, u.ID).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	e.mustRun("auth.oidc.set", oidcInput("https://idp2.example", "Sign in", "https://100.64.0.1:8443", ""))
	if got, _ := e.get(); got.OIDC == nil || got.OIDC.Issuer != "https://idp2.example" || !got.OIDC.ClientSecretSet {
		t.Fatalf("OIDC after the issuer change = %+v, want idp2 with the stored secret", got.OIDC)
	}
	if secret := e.storedSecret(); secret != testClientSecret {
		t.Errorf("an omitted secret left %q stored, want the earlier one kept", secret)
	}
	if p := pin(); p != "https://idp.example|sub-1" {
		t.Errorf("pin after the issuer change = %q, want the old issuer's kept", p)
	}

	cleared := e.mustRun("auth.oidc.clear", `{}`)
	if got, _ := e.get(); got.OIDC != nil {
		t.Fatalf("OIDC after clear = %+v, want off", got.OIDC)
	}
	if secret := e.storedSecret(); secret != "" {
		t.Error("clear kept the client secret")
	}
	if p := pin(); p != "https://idp.example|sub-1" {
		t.Errorf("pin after clear = %q, want it kept", p)
	}
	if _, err := e.run("auth.oidc.clear", `{}`); err == nil {
		t.Error("clearing OIDC twice succeeded")
	}

	// Undoing the clear restores the provider and its secret from the journal.
	e.undo(cleared.ID)
	if got, _ := e.get(); got.OIDC == nil || got.OIDC.Issuer != "https://idp2.example" || !got.OIDC.ClientSecretSet {
		t.Fatalf("OIDC after undoing clear = %+v, want idp2 back", got.OIDC)
	}
	if secret := e.storedSecret(); secret != testClientSecret {
		t.Errorf("secret after undoing clear = %q, want the earlier one", secret)
	}
}

func TestTailscaleSettings(t *testing.T) {
	e := newSettingsEnv(t)
	if def, ok := e.reg.Action(core.ActionRef{Name: "auth.settings.get", Version: 1}); !ok || def.Spec.HostData != core.HostDataFleet {
		t.Fatalf("auth.settings.get: registered %v, HostData %q; want fleet", ok, def.Spec.HostData)
	}
	for _, name := range []string{"auth.oidc.set", "auth.oidc.clear", "auth.tailscale.set"} {
		if def, _ := e.reg.Action(core.ActionRef{Name: name, Version: 1}); core.RequiredScope(def.Spec) != core.ScopeAdmin {
			t.Errorf("%s needs %q, want admin", name, core.RequiredScope(def.Spec))
		}
	}
	if got, _ := e.get(); got.Tailscale != (TailscaleSettings{}) || got.OIDC != nil {
		t.Fatalf("defaults = %+v, want everything off", got)
	}
	e.mustRun("auth.tailscale.set", `{"enabled": true, "requireTotp": true}`)
	if got, _ := e.get(); got.Tailscale != (TailscaleSettings{Enabled: true, RequireTOTP: true}) {
		t.Fatalf("after set = %+v, want enabled with the code", got.Tailscale)
	}
	cv := e.mustRun("auth.tailscale.set", `{"enabled": true}`)
	if got, _ := e.get(); got.Tailscale != (TailscaleSettings{Enabled: true}) {
		t.Fatalf("requireTotp omitted = %+v, want it false", got.Tailscale)
	}
	e.undo(cv.ID)
	if got, _ := e.get(); got.Tailscale != (TailscaleSettings{Enabled: true, RequireTOTP: true}) {
		t.Errorf("after undoing the second set = %+v, want the first set's settings", got.Tailscale)
	}
}
