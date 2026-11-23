package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// The sign-in method settings (M10 spec sections 2 and 3) live in the one
// auth_settings row; each method is one effect resource, so an undo restores
// that method's previous settings and leaves the other's.
const (
	oidcSecretAD      = "auth_settings.oidc_client_secret_ct.1" //nolint:gosec // G101: the secret's associated data, not a credential
	oidcResource      = "auth.settings:oidc"
	tailscaleResource = "auth.settings:tailscale"
	maxDisplayName    = 32
)

// ModuleID is the id of the built-in module of the sign-in settings
// actions, which every registry holds right after the core module.
const ModuleID = "auth"

// Module is the auth module: the sign-in settings actions alone. Their
// tables are core migrations (0018), so it has no migrations of its own.
func Module(origins Origins) core.Module { return authModule{origins: origins} }

type authModule struct{ origins Origins }

func (m authModule) Manifest() core.Manifest {
	return core.Manifest{ID: ModuleID, Version: 1, DefaultEnabled: true, Actions: SettingsActions(m.origins)}
}

// Origins lists the https origins the panel is served at: its configured
// HTTPS listeners and the public hosts of its open runtime listeners.
type Origins func(ctx context.Context) ([]string, error)

// OIDCSetInput is the input of auth.oidc.set/v1.
type OIDCSetInput struct {
	Issuer       string `json:"issuer" jsonschema:"minLength=1" jsonschema_description:"The provider's issuer, an https URL; discovery reads <issuer>/.well-known/openid-configuration."`
	ClientID     string `json:"clientId" jsonschema:"minLength=1" jsonschema_description:"The client id the provider issued to Darkpanel."`
	ClientSecret string `json:"clientSecret,omitempty" secret:"true" jsonschema_description:"The client secret; write-only. Omit it to keep the stored one."`
	DisplayName  string `json:"displayName" jsonschema:"minLength=1,maxLength=32" jsonschema_description:"The sign-in button's text, 1 to 32 characters."`
	RedirectBase string `json:"redirectBase" jsonschema:"minLength=1" jsonschema_description:"The https origin the provider redirects back to: one of the panel's HTTPS listeners or runtime listener public hosts."`
	RequireTOTP  bool   `json:"requireTotp,omitempty" jsonschema_description:"Ask for the user's authenticator code after the provider; false when omitted."`
}

// TailscaleSetInput is the input of auth.tailscale.set/v1.
type TailscaleSetInput struct {
	Enabled     bool `json:"enabled" jsonschema_description:"Offer Tailscale sign-in on the Tailscale listener."`
	RequireTOTP bool `json:"requireTotp,omitempty" jsonschema_description:"Ask for the user's authenticator code after the whois match; false when omitted."`
}

// NoSettingsInput is the empty input of auth.oidc.clear/v1 and auth.settings.get/v1.
type NoSettingsInput struct{}

// OIDCSettings is the stored OIDC provider, without its secret.
type OIDCSettings struct {
	Issuer          string `json:"issuer" jsonschema_description:"The provider's issuer."`
	ClientID        string `json:"clientId" jsonschema_description:"The client id."`
	DisplayName     string `json:"displayName" jsonschema_description:"The sign-in button's text."`
	RedirectBase    string `json:"redirectBase" jsonschema_description:"The origin the provider redirects back to."`
	RequireTOTP     bool   `json:"requireTotp" jsonschema_description:"Whether the authenticator code is asked after the provider."`
	ClientSecretSet bool   `json:"clientSecretSet" jsonschema_description:"Whether a client secret is stored; the secret itself is never returned."`
}

// TailscaleSettings is the stored Tailscale sign-in setting.
type TailscaleSettings struct {
	Enabled     bool `json:"enabled" jsonschema_description:"Whether Tailscale sign-in is offered."`
	RequireTOTP bool `json:"requireTotp" jsonschema_description:"Whether the authenticator code is asked after the whois match."`
}

// SignInSettings is the output of auth.settings.get/v1 and of each settings change.
type SignInSettings struct {
	OIDC      *OIDCSettings     `json:"oidc" jsonschema:"nullable" jsonschema_description:"The OIDC provider; null when OIDC sign-in is off."`
	Tailscale TailscaleSettings `json:"tailscale" jsonschema_description:"Tailscale sign-in."`
}

// SettingsActions are the sign-in settings actions; origins is the source
// of the allowed redirect bases, and nil allows none.
func SettingsActions(origins Origins) []core.ActionDef {
	return []core.ActionDef{
		core.DefineMutating[OIDCSetInput, SignInSettings](core.ActionSpec{
			Name: "auth.oidc.set", Version: 1, Class: core.ClassCompensable, Side: core.SideController,
			MinScope: core.ScopeAdmin, LockKeys: []string{"auth.settings"},
			Summary: "Set up OIDC sign-in with one provider; changing the issuer keeps the users' links to the old one.",
		}, oidcSet{origins: origins}),
		core.DefineMutating[NoSettingsInput, SignInSettings](core.ActionSpec{
			Name: "auth.oidc.clear", Version: 1, Class: core.ClassCompensable, Side: core.SideController,
			MinScope: core.ScopeAdmin, LockKeys: []string{"auth.settings"},
			Summary: "Turn OIDC sign-in off; the users' links to the provider are kept.",
		}, oidcClear{}),
		core.DefineMutating[TailscaleSetInput, SignInSettings](core.ActionSpec{
			Name: "auth.tailscale.set", Version: 1, Class: core.ClassCompensable, Side: core.SideController,
			MinScope: core.ScopeAdmin, LockKeys: []string{"auth.settings"},
			Summary: "Turn Tailscale sign-in on or off.",
		}, tailscaleSet{}),
		core.DefineRead[NoSettingsInput, SignInSettings](core.ActionSpec{
			Name: "auth.settings.get", Version: 1, Side: core.SideController, HostData: core.HostDataFleet,
			Summary: "Read the sign-in method settings; the OIDC client secret is never returned.",
		}, settingsGet{}),
	}
}

type rowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// storedSettings is the auth_settings row; the zero value when there is none.
type storedSettings struct {
	SignInSettings
	SecretCT []byte
}

func loadSettings(ctx context.Context, q rowQuerier) (storedSettings, error) {
	var issuer, clientID, name, base sql.NullString
	var otp bool
	var s storedSettings
	err := q.QueryRowContext(ctx, `SELECT oidc_issuer, oidc_client_id, oidc_client_secret_ct, oidc_display_name,
		oidc_redirect_base, oidc_require_totp, tailscale_enabled, tailscale_require_totp FROM auth_settings WHERE id = 1`).
		Scan(&issuer, &clientID, &s.SecretCT, &name, &base, &otp, &s.Tailscale.Enabled, &s.Tailscale.RequireTOTP)
	if errors.Is(err, sql.ErrNoRows) {
		return storedSettings{}, nil
	}
	if err != nil {
		return storedSettings{}, fmt.Errorf("auth: read sign-in settings: %w", err)
	}
	if issuer.Valid {
		s.OIDC = &OIDCSettings{Issuer: issuer.String, ClientID: clientID.String, DisplayName: name.String,
			RedirectBase: base.String, RequireTOTP: otp, ClientSecretSet: len(s.SecretCT) > 0}
	}
	return s, nil
}

// storeOIDC writes the OIDC columns: o with the sealed secret ct, or all
// null for nil.
func storeOIDC(ctx context.Context, tx *sql.Tx, o *OIDCSettings, ct []byte, now string) error {
	var issuer, clientID, name, base any
	otp := false
	if o != nil {
		issuer, clientID, name, base, otp = o.Issuer, o.ClientID, o.DisplayName, o.RedirectBase, o.RequireTOTP
	} else {
		ct = nil
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO auth_settings (id, oidc_issuer, oidc_client_id, oidc_client_secret_ct,
		oidc_display_name, oidc_redirect_base, oidc_require_totp, updated_at) VALUES (1, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET oidc_issuer = excluded.oidc_issuer, oidc_client_id = excluded.oidc_client_id,
		oidc_client_secret_ct = excluded.oidc_client_secret_ct, oidc_display_name = excluded.oidc_display_name,
		oidc_redirect_base = excluded.oidc_redirect_base, oidc_require_totp = excluded.oidc_require_totp,
		updated_at = excluded.updated_at`, issuer, clientID, ct, name, base, otp, now)
	if err != nil {
		return fmt.Errorf("auth: store OIDC settings: %w", err)
	}
	return nil
}

func storeTailscale(ctx context.Context, tx *sql.Tx, t TailscaleSettings, now string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO auth_settings (id, tailscale_enabled, tailscale_require_totp, updated_at)
		VALUES (1, ?, ?, ?) ON CONFLICT (id) DO UPDATE SET tailscale_enabled = excluded.tailscale_enabled,
		tailscale_require_totp = excluded.tailscale_require_totp, updated_at = excluded.updated_at`,
		t.Enabled, t.RequireTOTP, now)
	if err != nil {
		return fmt.Errorf("auth: store Tailscale settings: %w", err)
	}
	return nil
}

func stateDoc(v any) core.StateDoc {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err) // the settings types always marshal
	}
	return core.StateDoc{State: raw}
}

func settingsNow(env core.Env) string { return formatTime(env.Clock.Now()) }

// checkOIDC refuses an invalid input with one field error per problem; no
// message quotes the secret.
func checkOIDC(ctx context.Context, in OIDCSetInput, origins Origins) error {
	var fields []errcode.FieldError
	if u, err := url.Parse(in.Issuer); err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		fields = append(fields, errcode.FieldError{Path: "/issuer", Message: "the issuer is an https URL"})
	}
	if strings.TrimSpace(in.ClientID) == "" {
		fields = append(fields, errcode.FieldError{Path: "/clientId", Message: "the client id is required"})
	}
	if n := utf8.RuneCountInString(strings.TrimSpace(in.DisplayName)); n == 0 || utf8.RuneCountInString(in.DisplayName) > maxDisplayName {
		fields = append(fields, errcode.FieldError{Path: "/displayName", Message: fmt.Sprintf("the display name is 1 to %d characters", maxDisplayName)})
	}
	base, ok := redirectOrigin(in.RedirectBase)
	if ok {
		allowed, err := allowedOrigins(ctx, origins)
		if err != nil {
			return err
		}
		ok = slices.Contains(allowed, base)
	}
	if !ok {
		fields = append(fields, errcode.FieldError{Path: "/redirectBase",
			Message: "the redirect base is the https origin of one of the panel's HTTPS listeners or public hosts"})
	}
	if len(fields) > 0 {
		return core.Definite(&errcode.Error{Code: errcode.InvalidInput, Detail: "the OIDC settings are invalid", Fields: fields})
	}
	return nil
}

// redirectOrigin is s as a lower-case https origin, when it is one: no
// path beyond "/", no query, fragment or user.
func redirectOrigin(s string) (string, bool) {
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		(u.Path != "" && u.Path != "/") {
		return "", false
	}
	return "https://" + strings.ToLower(u.Host), true
}

func allowedOrigins(ctx context.Context, origins Origins) ([]string, error) {
	if origins == nil {
		return nil, nil
	}
	list, err := origins(ctx)
	if err != nil {
		return nil, fmt.Errorf("auth: list the panel's origins: %w", err)
	}
	out := make([]string, 0, len(list))
	for _, o := range list {
		if n, ok := redirectOrigin(o); ok {
			out = append(out, n)
		}
	}
	return out, nil
}

// settingsChange is the one effect of a settings action over resource:
// from the stored state to after, with secret (the client secret the
// undo restores) sealed in the journal.
func settingsChange(ctx context.Context, env core.Env, resource string, before, after core.StateDoc, secret []byte,
	write func(tx *sql.Tx) error) error {
	seq, err := env.Effects.Intend(ctx, core.Effect{
		Kind: core.EffectModify, Resource: resource, Local: true, InverseLocal: true, Before: before, After: &after,
	}, secret)
	if err != nil {
		return err
	}
	if err := env.DB.Write(ctx, write); err != nil {
		if serr := env.Effects.Skipped(ctx, seq); serr != nil {
			return errors.Join(err, serr)
		}
		return err
	}
	return env.Effects.Made(ctx, seq, resource, after)
}

// openSecret is the stored client secret, nil when none is stored.
func openSecret(env core.Env, ct []byte) ([]byte, error) {
	if len(ct) == 0 {
		return nil, nil
	}
	secret, err := env.Sealer.Open(oidcSecretAD, ct)
	if err != nil {
		return nil, fmt.Errorf("auth: open the OIDC client secret: %w", err)
	}
	return secret, nil
}

// sectionState is the observed state of a settings resource.
func sectionState(s storedSettings, resource string) core.StateDoc {
	if resource == oidcResource {
		return stateDoc(s.OIDC)
	}
	return stateDoc(s.Tailscale)
}

// settingsEffects is the observe and undo half the three settings actions share.
type settingsEffects struct{}

func (settingsEffects) Observe(ctx context.Context, env core.Env, e core.Effect) (core.Observation, error) {
	if e.Resource != oidcResource && e.Resource != tailscaleResource {
		return core.Observation{}, fmt.Errorf("auth: %q is not a sign-in settings resource", e.Resource)
	}
	s, err := loadSettings(ctx, env.DB.Read())
	if err != nil {
		return core.Observation{}, err
	}
	return core.Observation{Current: sectionState(s, e.Resource), Resource: e.Resource, Matches: 1, Exclusive: true}, nil
}

// Inverse writes back the settings the effect found, the OIDC client secret
// from the journal.
func (settingsEffects) Inverse(ctx context.Context, env core.Env, e core.Effect) (core.InverseResult, error) {
	to := e.Before
	if e.UndoTo != nil {
		to = *e.UndoTo
	}
	now := settingsNow(env)
	switch e.Resource {
	case oidcResource:
		var o *OIDCSettings
		if err := json.Unmarshal(to.State, &o); err != nil {
			return core.InverseResult{}, fmt.Errorf("auth: read the OIDC settings to restore: %w", err)
		}
		var ct []byte
		if o != nil && o.ClientSecretSet {
			secret, err := env.Effects.Secret(ctx, e.Seq)
			if err != nil {
				return core.InverseResult{}, err
			}
			if len(secret) == 0 {
				return core.InverseResult{}, errors.New("auth: the journal holds no OIDC client secret to restore")
			}
			if ct, err = env.Sealer.Seal(oidcSecretAD, secret); err != nil {
				return core.InverseResult{}, fmt.Errorf("auth: seal the OIDC client secret: %w", err)
			}
		}
		return core.InverseResult{}, env.DB.Write(ctx, func(tx *sql.Tx) error { return storeOIDC(ctx, tx, o, ct, now) })
	case tailscaleResource:
		var t TailscaleSettings
		if err := json.Unmarshal(to.State, &t); err != nil {
			return core.InverseResult{}, fmt.Errorf("auth: read the Tailscale settings to restore: %w", err)
		}
		return core.InverseResult{}, env.DB.Write(ctx, func(tx *sql.Tx) error { return storeTailscale(ctx, tx, t, now) })
	}
	return core.InverseResult{}, fmt.Errorf("auth: %q is not a sign-in settings resource", e.Resource)
}

func planDiff(op, text string, hashOf core.StateDoc) core.PlanOutcome {
	return core.PlanOutcome{Diff: []core.DiffLine{{Op: op, Text: text}}, StateHash: string(hashOf.State)}
}

type oidcSet struct {
	settingsEffects
	origins Origins
}

func (h oidcSet) next(in OIDCSetInput, cur storedSettings) (*OIDCSettings, error) {
	if in.ClientSecret == "" && len(cur.SecretCT) == 0 {
		return nil, core.Definite(&errcode.Error{Code: errcode.InvalidInput, Detail: "the OIDC settings are invalid",
			Fields: []errcode.FieldError{{Path: "/clientSecret", Message: "the client secret is required: none is stored"}}})
	}
	base, _ := redirectOrigin(in.RedirectBase)
	return &OIDCSettings{Issuer: in.Issuer, ClientID: in.ClientID, DisplayName: in.DisplayName,
		RedirectBase: base, RequireTOTP: in.RequireTOTP, ClientSecretSet: true}, nil
}

func (h oidcSet) Plan(ctx context.Context, env core.Env, in OIDCSetInput) (core.PlanOutcome, error) {
	if err := checkOIDC(ctx, in, h.origins); err != nil {
		return core.PlanOutcome{}, err
	}
	cur, err := loadSettings(ctx, env.DB.Read())
	if err != nil {
		return core.PlanOutcome{}, err
	}
	if _, err := h.next(in, cur); err != nil {
		return core.PlanOutcome{}, err
	}
	return planDiff("~", "OIDC sign-in with "+in.Issuer+" as "+in.DisplayName, stateDoc(cur.OIDC)), nil
}

func (h oidcSet) Apply(ctx context.Context, env core.Env, in OIDCSetInput) (SignInSettings, error) {
	if err := checkOIDC(ctx, in, h.origins); err != nil {
		return SignInSettings{}, err
	}
	cur, err := loadSettings(ctx, env.DB.Read())
	if err != nil {
		return SignInSettings{}, err
	}
	next, err := h.next(in, cur)
	if err != nil {
		return SignInSettings{}, err
	}
	prev, err := openSecret(env, cur.SecretCT)
	if err != nil {
		return SignInSettings{}, err
	}
	secret := []byte(in.ClientSecret)
	if len(secret) == 0 {
		secret = prev
	}
	ct, err := env.Sealer.Seal(oidcSecretAD, secret)
	if err != nil {
		return SignInSettings{}, fmt.Errorf("auth: seal the OIDC client secret: %w", err)
	}
	now := settingsNow(env)
	err = settingsChange(ctx, env, oidcResource, stateDoc(cur.OIDC), stateDoc(next), prev,
		func(tx *sql.Tx) error { return storeOIDC(ctx, tx, next, ct, now) })
	if err != nil {
		return SignInSettings{}, err
	}
	return SignInSettings{OIDC: next, Tailscale: cur.Tailscale}, nil
}

func (h oidcSet) Verify(ctx context.Context, env core.Env, in OIDCSetInput, _ SignInSettings) error {
	s, err := loadSettings(ctx, env.DB.Read())
	if err != nil {
		return err
	}
	if s.OIDC == nil || s.OIDC.Issuer != in.Issuer || s.OIDC.ClientID != in.ClientID || !s.OIDC.ClientSecretSet {
		return errors.New("auth: the OIDC settings are not stored")
	}
	return nil
}

type oidcClear struct{ settingsEffects }

func (oidcClear) Plan(ctx context.Context, env core.Env, _ NoSettingsInput) (core.PlanOutcome, error) {
	cur, err := loadSettings(ctx, env.DB.Read())
	if err != nil {
		return core.PlanOutcome{}, err
	}
	if cur.OIDC == nil {
		return core.PlanOutcome{}, core.Definite(errcode.New(errcode.InvalidInput, "OIDC sign-in is already off"))
	}
	return planDiff("-", "OIDC sign-in with "+cur.OIDC.Issuer, stateDoc(cur.OIDC)), nil
}

func (oidcClear) Apply(ctx context.Context, env core.Env, _ NoSettingsInput) (SignInSettings, error) {
	cur, err := loadSettings(ctx, env.DB.Read())
	if err != nil {
		return SignInSettings{}, err
	}
	if cur.OIDC == nil {
		return SignInSettings{}, core.Definite(errcode.New(errcode.InvalidInput, "OIDC sign-in is already off"))
	}
	prev, err := openSecret(env, cur.SecretCT)
	if err != nil {
		return SignInSettings{}, err
	}
	now := settingsNow(env)
	var none *OIDCSettings
	err = settingsChange(ctx, env, oidcResource, stateDoc(cur.OIDC), stateDoc(none), prev,
		func(tx *sql.Tx) error { return storeOIDC(ctx, tx, nil, nil, now) })
	if err != nil {
		return SignInSettings{}, err
	}
	return SignInSettings{Tailscale: cur.Tailscale}, nil
}

func (oidcClear) Verify(ctx context.Context, env core.Env, _ NoSettingsInput, _ SignInSettings) error {
	s, err := loadSettings(ctx, env.DB.Read())
	if err != nil {
		return err
	}
	if s.OIDC != nil {
		return errors.New("auth: OIDC sign-in is still set")
	}
	return nil
}

type tailscaleSet struct{ settingsEffects }

func (tailscaleSet) Plan(ctx context.Context, env core.Env, in TailscaleSetInput) (core.PlanOutcome, error) {
	cur, err := loadSettings(ctx, env.DB.Read())
	if err != nil {
		return core.PlanOutcome{}, err
	}
	text := "Tailscale sign-in off"
	if in.Enabled {
		text = "Tailscale sign-in on"
	}
	return planDiff("~", text, stateDoc(cur.Tailscale)), nil
}

func (tailscaleSet) Apply(ctx context.Context, env core.Env, in TailscaleSetInput) (SignInSettings, error) {
	cur, err := loadSettings(ctx, env.DB.Read())
	if err != nil {
		return SignInSettings{}, err
	}
	next := TailscaleSettings(in)
	now := settingsNow(env)
	err = settingsChange(ctx, env, tailscaleResource, stateDoc(cur.Tailscale), stateDoc(next), nil,
		func(tx *sql.Tx) error { return storeTailscale(ctx, tx, next, now) })
	if err != nil {
		return SignInSettings{}, err
	}
	return SignInSettings{OIDC: cur.OIDC, Tailscale: next}, nil
}

func (tailscaleSet) Verify(ctx context.Context, env core.Env, in TailscaleSetInput, _ SignInSettings) error {
	s, err := loadSettings(ctx, env.DB.Read())
	if err != nil {
		return err
	}
	if s.Tailscale != TailscaleSettings(in) {
		return errors.New("auth: the Tailscale settings are not stored")
	}
	return nil
}

type settingsGet struct{}

func (settingsGet) Read(ctx context.Context, env core.Env, _ NoSettingsInput) (SignInSettings, error) {
	s, err := loadSettings(ctx, env.DB.Read())
	if err != nil {
		return SignInSettings{}, err
	}
	return s.SignInSettings, nil
}
