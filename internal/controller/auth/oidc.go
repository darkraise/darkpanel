package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// OIDC sign-in (M10 spec section 2).
const (
	MethodOIDC       = "oidc"
	OIDCCallbackPath = "/api/v1/auth/oidc/callback"
	OIDCStateTTL     = 10 * time.Minute
	SignInTicketTTL  = 5 * time.Minute
	discoveryTTL     = time.Hour
	oidcHTTPTimeout  = 15 * time.Second
	oidcStateAD      = "auth.oidc_state"
	signInTicketAD   = "auth.signin_ticket" //nolint:gosec // G101: associated data, not a credential
)

// oidcFlow holds what OIDC sign-in keeps between requests: the provider
// discovery cache and the state and ticket ids already spent.
type oidcFlow struct {
	client *http.Client

	mu        sync.Mutex
	providers map[string]cachedProvider
	spent     map[string]time.Time
}

type cachedProvider struct {
	p  *oidc.Provider
	at time.Time
}

// newOIDCFlow talks to providers through rt (nil: the default transport),
// with a 15-second timeout and no redirects followed.
func newOIDCFlow(rt http.RoundTripper) *oidcFlow {
	if rt == nil {
		rt = http.DefaultTransport
	}
	return &oidcFlow{
		client: &http.Client{Transport: rt, Timeout: oidcHTTPTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		providers: map[string]cachedProvider{}, spent: map[string]time.Time{},
	}
}

// spend marks id used until until and reports whether it was unused.
func (f *oidcFlow) spend(id string, now, until time.Time) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for k, exp := range f.spent {
		if !now.Before(exp) {
			delete(f.spent, k)
		}
	}
	if _, used := f.spent[id]; used {
		return false
	}
	f.spent[id] = until
	return true
}

// OIDCStart is the start of an OIDC sign-in: the provider URL the browser
// goes to, and the sealed state cookie the callback needs back.
type OIDCStart struct {
	URL     string
	Cookie  string
	Expires time.Time
}

// OIDCCallback is the provider's redirect back: the state cookie, the
// query's state and code, and the request's source.
type OIDCCallback struct {
	Cookie, State, Code, Source, UserAgent string
	Trusted                                bool
}

// SignInResult is a sign-in that either opened a session or, when the
// method asks for the authenticator code, holds a sealed ticket for
// FinishTOTPStep and no session.
type SignInResult struct {
	LoginResult
	Ticket        string
	TicketExpires time.Time
	// Remember reports that a right code remembers the Tailscale node for
	// the user, so its next Continue-as asks no code.
	Remember bool
}

// TOTPStepRequest is the code step after a sign-in method.
type TOTPStepRequest struct {
	Ticket, Code, Source, UserAgent string
	Trusted                         bool
}

// oidcState is the sealed content of the state cookie.
type oidcState struct {
	ID       string    `json:"id"`
	Nonce    string    `json:"nonce"`
	Verifier string    `json:"verifier"`
	Issuer   string    `json:"issuer"`
	Expires  time.Time `json:"exp"`
}

// signInTicket is the sealed content of the code step's cookie. Issuer and
// Subject are the OIDC identity to pin once the code is right, empty when
// the user is already pinned.
type signInTicket struct {
	ID      string `json:"id"`
	UserID  string `json:"user"`
	Method  string `json:"method"`
	Issuer  string `json:"iss,omitempty"`
	Subject string `json:"sub,omitempty"`
	// TailnetUserID is the Tailscale user id to pin once the code is right,
	// empty when the user is already pinned.
	TailnetUserID string `json:"tailnetUser,omitempty"`
	// StableID, NodeName, Addrs, Login and WhoisUserID are the Tailscale
	// node the ticket was issued to: the code step accepts the code only
	// from that node and remembers it. Addrs holds the request's address
	// first, then the whois addresses.
	StableID    string    `json:"stableId,omitempty"`
	NodeName    string    `json:"node,omitempty"`
	Addrs       []string  `json:"addrs,omitempty"`
	Login       string    `json:"login,omitempty"`
	WhoisUserID string    `json:"whoisUser,omitempty"`
	Expires     time.Time `json:"exp"`
}

func errMethodOff() error {
	return errcode.New(errcode.MethodOff, "this sign-in method is off")
}

func errNoLinkedUser() error {
	return errcode.New(errcode.NoLinkedUser, "No Darkpanel user is linked to this account. Ask an admin to add you.")
}

func errSignInRefused() error {
	return errcode.New(errcode.InvalidCredentials, "the sign-in was refused")
}

// refusalCause names the step of an OIDC callback that refused it. The audit
// row records it as a fixed word; the provider's own error text, which can
// carry its response body, is never recorded.
type refusalCause string

func (c refusalCause) Error() string { return "oidc: refused at " + string(c) }

func errRefusedAt(c refusalCause) error {
	return errcode.Wrap(errcode.InvalidCredentials, c, "the sign-in was refused")
}

func randomID() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return b64url.EncodeToString(b)
}

func (s *Service) sealJSON(ad string, v any) (string, error) {
	plain, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	ct, err := s.sealer.Seal(ad, plain)
	if err != nil {
		return "", fmt.Errorf("auth: seal %s: %w", ad, err)
	}
	return b64url.EncodeToString(ct), nil
}

// openJSON opens a value sealJSON made; any failure is false.
func (s *Service) openJSON(ad, value string, v any) bool {
	ct, err := b64url.DecodeString(value)
	if err != nil || value == "" {
		return false
	}
	plain, err := s.sealer.Open(ad, ct)
	return err == nil && json.Unmarshal(plain, v) == nil
}

// SignInMethods returns the stored sign-in settings, without the secret.
func (s *Service) SignInMethods(ctx context.Context) (SignInSettings, error) {
	st, err := loadSettings(ctx, s.db.Read())
	return st.SignInSettings, err
}

// OriginMatches reports whether origin, an https origin a request arrived
// on, is the redirect base base.
func OriginMatches(origin, base string) bool {
	o, ok := redirectOrigin(origin)
	b, okBase := redirectOrigin(base)
	return ok && okBase && o == b
}

// oidcSettings returns the OIDC settings and the opened client secret;
// auth.method_off when OIDC is not set.
func (s *Service) oidcSettings(ctx context.Context) (*OIDCSettings, []byte, error) {
	st, err := loadSettings(ctx, s.db.Read())
	if err != nil {
		return nil, nil, err
	}
	if st.OIDC == nil {
		return nil, nil, errMethodOff()
	}
	var secret []byte
	if len(st.SecretCT) > 0 {
		if secret, err = s.sealer.Open(oidcSecretAD, st.SecretCT); err != nil {
			return nil, nil, fmt.Errorf("auth: open the OIDC client secret: %w", err)
		}
	}
	return st.OIDC, secret, nil
}

// provider returns issuer's discovered provider, fetched again after an
// hour. A provider's answer never reaches the error.
func (s *Service) provider(ctx context.Context, issuer string) (*oidc.Provider, error) {
	now := s.clock.Now()
	f := s.oidc
	f.mu.Lock()
	c, ok := f.providers[issuer]
	f.mu.Unlock()
	if ok && now.Sub(c.at) < discoveryTTL {
		return c.p, nil
	}
	// The provider keeps this context for its key fetches, so it must not
	// end with the request.
	pctx := oidc.ClientContext(context.WithoutCancel(ctx), f.client)
	p, err := oidc.NewProvider(pctx, issuer)
	if err != nil {
		return nil, errcode.New(errcode.Unavailable, "the OIDC provider's discovery failed")
	}
	f.mu.Lock()
	f.providers[issuer] = cachedProvider{p: p, at: now}
	f.mu.Unlock()
	return p, nil
}

func oauthConfig(set *OIDCSettings, secret []byte, p *oidc.Provider) *oauth2.Config {
	return &oauth2.Config{ClientID: set.ClientID, ClientSecret: string(secret), Endpoint: p.Endpoint(),
		RedirectURL: set.RedirectBase + OIDCCallbackPath, Scopes: []string{oidc.ScopeOpenID, "email"}}
}

// StartOIDC begins an OIDC sign-in from origin, which must be the stored
// redirect base: the redirect_uri is always built from the stored value.
func (s *Service) StartOIDC(ctx context.Context, origin string) (OIDCStart, error) {
	set, secret, err := s.oidcSettings(ctx)
	if err != nil {
		return OIDCStart{}, err
	}
	if !OriginMatches(origin, set.RedirectBase) {
		return OIDCStart{}, errMethodOff()
	}
	p, err := s.provider(ctx, set.Issuer)
	if err != nil {
		return OIDCStart{}, err
	}
	st := oidcState{ID: randomID(), Nonce: randomID(), Verifier: oauth2.GenerateVerifier(), Issuer: set.Issuer,
		Expires: s.now().Add(OIDCStateTTL)}
	cookie, err := s.sealJSON(oidcStateAD, st)
	if err != nil {
		return OIDCStart{}, err
	}
	u := oauthConfig(set, secret, p).AuthCodeURL(st.ID, oidc.Nonce(st.Nonce), oauth2.S256ChallengeOption(st.Verifier))
	return OIDCStart{URL: u, Cookie: cookie, Expires: st.Expires}, nil
}

// FinishOIDC checks the provider's redirect back and signs the linked user
// in, or returns a ticket for the code step. Every failure counts on the
// login limiter's source key, and every evaluated callback is audited as a
// login with method oidc.
func (s *Service) FinishOIDC(ctx context.Context, c OIDCCallback) (SignInResult, error) {
	src, trusted := limitSource(ctx, c.Source), c.Trusted && !proxied(ctx)
	if d := s.limiter.sourceBlockedFor(src, trusted); d > 0 {
		return SignInResult{}, errRateLimited(d)
	}
	res, rec, activated, err := s.finishOIDC(ctx, c)
	code := errcode.Code("")
	if err != nil {
		code = errcode.From(err).Code
		s.limiter.failSourceFor(src, trusted)
	}
	extra := map[string]string{}
	if activated && err == nil {
		extra["activated"] = "true"
	}
	var rc refusalCause
	if errors.As(err, &rc) {
		extra["cause"] = string(rc)
	}
	if res.Ticket != "" {
		extra["next"] = "totp"
	}
	if aerr := s.recordSignInWith(ctx, MethodOIDC, rec.Username, c.Source, rec.ID, code, extra); aerr != nil {
		return SignInResult{}, aerr
	}
	if err != nil {
		return SignInResult{}, err
	}
	return res, nil
}

// idClaims are the ID token claims linking reads. email_verified is a JSON
// boolean, or the string "true" some providers send.
type idClaims struct {
	Email         string          `json:"email"`
	EmailVerified json.RawMessage `json:"email_verified"`
}

func (c idClaims) verified() bool {
	v := string(c.EmailVerified)
	return v == "true" || v == `"true"`
}

func (s *Service) finishOIDC(ctx context.Context, c OIDCCallback) (SignInResult, userRecord, bool, error) {
	var st oidcState
	now := s.now()
	if !s.openJSON(oidcStateAD, c.Cookie, &st) || c.State == "" || st.ID != c.State || !now.Before(st.Expires) {
		return SignInResult{}, userRecord{}, false, errRefusedAt("state")
	}
	if !s.oidc.spend("state:"+st.ID, now, st.Expires) {
		return SignInResult{}, userRecord{}, false, errRefusedAt("state")
	}
	set, secret, err := s.oidcSettings(ctx)
	if err != nil {
		return SignInResult{}, userRecord{}, false, err
	}
	if set.Issuer != st.Issuer || c.Code == "" {
		return SignInResult{}, userRecord{}, false, errRefusedAt("issuer")
	}
	p, err := s.provider(ctx, set.Issuer)
	if err != nil {
		return SignInResult{}, userRecord{}, false, err
	}
	hctx := oidc.ClientContext(ctx, s.oidc.client)
	tok, err := oauthConfig(set, secret, p).Exchange(hctx, c.Code, oauth2.VerifierOption(st.Verifier))
	if err != nil {
		return SignInResult{}, userRecord{}, false, errRefusedAt("exchange")
	}
	raw, _ := tok.Extra("id_token").(string)
	idt, err := p.Verifier(&oidc.Config{ClientID: set.ClientID, Now: s.clock.Now}).Verify(hctx, raw)
	if err != nil || idt.Nonce != st.Nonce {
		return SignInResult{}, userRecord{}, false, errRefusedAt("id_token")
	}
	var claims idClaims
	if err := idt.Claims(&claims); err != nil {
		return SignInResult{}, userRecord{}, false, errRefusedAt("claims")
	}
	return s.linkOIDC(ctx, c, set, idt.Issuer, idt.Subject, claims)
}

// linkOIDC signs in the user pinned to (iss, sub), or links the one user
// whose email matches a verified email: an active user, or an invited one,
// which this sign-in activates unless the code step is on. A user pinned
// to another identity is never linked.
func (s *Service) linkOIDC(ctx context.Context, c OIDCCallback, set *OIDCSettings, iss, sub string, claims idClaims) (SignInResult, userRecord, bool, error) {
	var res SignInResult
	var rec userRecord
	activated := false
	err := s.db.Write(ctx, func(tx *sql.Tx) error {
		var err error
		link := false
		rec, err = loadUserBy(ctx, tx, `oidc_issuer = ? AND oidc_subject = ?`, iss, sub)
		if errcode.From(err).Code == errcode.NotFound {
			if !claims.verified() || claims.Email == "" {
				return errSignInRefused()
			}
			if rec, err = loadUserBy(ctx, tx, `email = ? COLLATE NOCASE`, claims.Email); errcode.From(err).Code == errcode.NotFound {
				return errNoLinkedUser()
			}
			if err != nil {
				return err
			}
			var pinned bool
			if err := tx.QueryRowContext(ctx, `SELECT oidc_subject IS NOT NULL FROM core_users WHERE id = ?`, rec.ID).
				Scan(&pinned); err != nil {
				return fmt.Errorf("auth: read the OIDC link: %w", err)
			}
			if pinned {
				return errSignInRefused()
			}
			link = true
		} else if err != nil {
			return err
		}
		if rec.Disabled {
			return errSignInRefused()
		}
		switch rec.State {
		case StateActive:
		case StatePending:
			if set.RequireTOTP {
				return errSignInRefused()
			}
			activated = true
		default:
			return errSignInRefused()
		}
		if set.RequireTOTP {
			t := signInTicket{ID: randomID(), UserID: rec.ID, Method: MethodOIDC, Expires: s.now().Add(SignInTicketTTL)}
			if link {
				t.Issuer, t.Subject = iss, sub
			}
			res.Ticket, err = s.sealJSON(signInTicketAD, t)
			res.TicketExpires = t.Expires
			return err
		}
		now := formatTime(s.now())
		if link {
			if _, err := tx.ExecContext(ctx, `UPDATE core_users SET oidc_issuer = ?, oidc_subject = ?, updated_at = ? WHERE id = ?`,
				iss, sub, now, rec.ID); err != nil {
				return fmt.Errorf("auth: link OIDC: %w", err)
			}
		}
		if activated {
			// The enrollment link stays usable: the user's password is the
			// placeholder until it is used (spec section 1.5).
			if _, err := tx.ExecContext(ctx, `UPDATE core_users SET state = ?, updated_at = ? WHERE id = ?`,
				StateActive, now, rec.ID); err != nil {
				return fmt.Errorf("auth: activate on OIDC sign-in: %w", err)
			}
			rec.State = StateActive
		}
		res.LoginResult, err = s.openSession(ctx, tx, rec.User, c.Source, c.UserAgent)
		return err
	})
	return res, rec, activated, err
}

// openSession creates a session for u in tx and resolves its access.
func (s *Service) openSession(ctx context.Context, tx *sql.Tx, u User, source, userAgent string) (LoginResult, error) {
	id, exp, err := s.createSession(ctx, tx, u.ID, source, userAgent)
	if err != nil {
		return LoginResult{}, err
	}
	access, err := resolveAccess(ctx, tx, u, "")
	if err != nil {
		return LoginResult{}, err
	}
	return LoginResult{SessionID: id, CSRFToken: s.CSRFToken(id), ExpiresAt: exp, User: u, Access: access}, nil
}

// FinishTOTPStep checks the authenticator code a sign-in method's ticket
// waits for and opens the session. The code goes through the password
// login's rules: the replay rule of totp_last_step and the limiter's pair
// key. A ticket is used once.
func (s *Service) FinishTOTPStep(ctx context.Context, r TOTPStepRequest) (LoginResult, error) {
	var t signInTicket
	// A ticket that does not open names no method or user, so it is not a
	// sign-in and leaves no audit row; an orphaned or expired one does.
	if !s.openJSON(signInTicketAD, r.Ticket, &t) {
		return LoginResult{}, errSignInRefused()
	}
	step := map[string]string{"step": "totp"}
	rec, err := loadUserBy(ctx, s.db.Read(), `id = ?`, t.UserID)
	if err != nil {
		if errcode.From(err).Code == errcode.NotFound {
			if aerr := s.recordSignInWith(ctx, t.Method, "", r.Source, "", errcode.InvalidCredentials, step); aerr != nil {
				return LoginResult{}, aerr
			}
			return LoginResult{}, errSignInRefused()
		}
		return LoginResult{}, err
	}
	if !s.now().Before(t.Expires) {
		if aerr := s.recordSignInWith(ctx, t.Method, rec.Username, r.Source, rec.ID, errcode.InvalidCredentials, step); aerr != nil {
			return LoginResult{}, aerr
		}
		return LoginResult{}, errSignInRefused()
	}
	src, trusted := limitSource(ctx, r.Source), r.Trusted && !proxied(ctx)
	if d := s.limiter.blockedFor(rec.Username, src, trusted); d > 0 {
		return LoginResult{}, errRateLimited(d)
	}
	var res LoginResult
	err = s.db.Write(ctx, func(tx *sql.Tx) error {
		cur, err := loadUserBy(ctx, tx, `id = ?`, t.UserID)
		if err != nil {
			return err
		}
		if !cur.usable() {
			return errSignInRefused()
		}
		st, err := loadSettings(ctx, tx)
		if err != nil {
			return err
		}
		if t.Method == MethodOIDC && st.OIDC == nil || t.Method == MethodTailscale && !st.Tailscale.Enabled {
			return errMethodOff()
		}
		key, err := s.sealer.Open(totpAD(cur.ID), cur.totpCT)
		if err != nil {
			return fmt.Errorf("auth: open TOTP secret: %w", err)
		}
		step, ok := matchTOTP(key, r.Code, s.clock.Now(), cur.lastStep)
		if !ok {
			return errInvalidCredentials()
		}
		if !s.oidc.spend("ticket:"+t.ID, s.now(), t.Expires) {
			return errSignInRefused()
		}
		now := formatTime(s.now())
		if _, err := tx.ExecContext(ctx, `UPDATE core_users SET totp_last_step = ? WHERE id = ?`, step, cur.ID); err != nil {
			return fmt.Errorf("auth: store TOTP step: %w", err)
		}
		if t.Subject != "" {
			linked, err := tx.ExecContext(ctx, `UPDATE core_users SET oidc_issuer = ?, oidc_subject = ?, updated_at = ?
				WHERE id = ? AND oidc_subject IS NULL`, t.Issuer, t.Subject, now, cur.ID)
			if err != nil {
				return fmt.Errorf("auth: link OIDC: %w", err)
			}
			if n, err := linked.RowsAffected(); err != nil || n != 1 {
				return errSignInRefused()
			}
		}
		if t.TailnetUserID != "" {
			pinned, err := tx.ExecContext(ctx, `UPDATE core_users SET tailnet_user_id = ?, updated_at = ?
				WHERE id = ? AND tailnet_user_id IS NULL`, t.TailnetUserID, now, cur.ID)
			if err != nil {
				return fmt.Errorf("auth: pin the tailnet user: %w", err)
			}
			if n, err := pinned.RowsAffected(); err != nil || n != 1 {
				return errSignInRefused()
			}
		}
		res, err = s.openSession(ctx, tx, cur.User, r.Source, r.UserAgent)
		return err
	})
	code := errcode.Code("")
	if err != nil {
		code = errcode.From(err).Code
		s.limiter.failFor(rec.Username, src, trusted)
	}
	if aerr := s.recordSignInWith(ctx, t.Method, rec.Username, r.Source, rec.ID, code, map[string]string{"step": "totp"}); aerr != nil {
		return LoginResult{}, aerr
	}
	if err != nil {
		return LoginResult{}, err
	}
	return res, nil
}
