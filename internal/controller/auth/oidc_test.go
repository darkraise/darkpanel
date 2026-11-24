package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/auth/oidcfake"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

const (
	testClientID     = "darkpanel"
	testRedirectBase = "https://panel.example"
)

// oidcEnv is a service whose OIDC provider is a fakeIDP.
type oidcEnv struct {
	t     *testing.T
	s     *Service
	clock *coretest.FakeClock
	audit *coretest.MemAudit
	idp   *oidcfake.Provider
	n     int
}

func newOIDCEnv(t *testing.T, requireTOTP bool) *oidcEnv {
	t.Helper()
	s, clock, audit := newTestService(t)
	idp := oidcfake.New(t)
	s.oidc = newOIDCFlow(idp.Transport())
	ct, err := coretest.Sealer{}.Seal(oidcSecretAD, []byte("oidc-client-secret"))
	if err != nil {
		t.Fatal(err)
	}
	exec(t, s, `INSERT INTO auth_settings (id, oidc_issuer, oidc_client_id, oidc_client_secret_ct, oidc_display_name,
		oidc_redirect_base, oidc_require_totp, updated_at) VALUES (1, ?, ?, ?, 'Acme SSO', ?, ?, ?)`,
		idp.URL(), testClientID, ct, testRedirectBase, requireTOTP, formatTime(t0))
	return &oidcEnv{t: t, s: s, clock: clock, audit: audit, idp: idp}
}

// claims are an ID token's claims for sub and email from the provider,
// valid for ten minutes from the clock's time.
func (e *oidcEnv) claims(sub, email string) map[string]any {
	now := e.clock.Now()
	return map[string]any{"iss": e.idp.URL(), "aud": testClientID, "sub": sub, "email": email, "email_verified": true,
		"iat": now.Unix(), "exp": now.Add(10 * time.Minute).Unix()}
}

// authorization is a started sign-in and the provider's code for it.
type authorization struct {
	start OIDCStart
	state string
	code  string
	query url.Values
}

// authorize starts a sign-in and has the provider issue a code for claims,
// with the start's nonce unless claims set one; edit, when not nil, changes
// the code before it is registered.
func (e *oidcEnv) authorize(claims map[string]any, edit func(*oidcfake.Code)) authorization {
	e.t.Helper()
	start, err := e.s.StartOIDC(context.Background(), testRedirectBase)
	if err != nil {
		e.t.Fatal(err)
	}
	u, err := url.Parse(start.URL)
	if err != nil {
		e.t.Fatal(err)
	}
	q := u.Query()
	if _, ok := claims["nonce"]; !ok {
		claims["nonce"] = q.Get("nonce")
	}
	e.n++
	c := oidcfake.Code{Claims: claims, Challenge: q.Get("code_challenge")}
	if edit != nil {
		edit(&c)
	}
	code := "code-" + strconv.Itoa(e.n)
	e.idp.Register(code, c)
	return authorization{start: start, state: q.Get("state"), code: code, query: q}
}

func (e *oidcEnv) callback(a authorization) (SignInResult, error) {
	return e.s.FinishOIDC(context.Background(), OIDCCallback{Cookie: a.start.Cookie, State: a.state, Code: a.code,
		Source: "192.0.2.7", UserAgent: "test"})
}

func (e *oidcEnv) signIn(claims map[string]any) (SignInResult, error) {
	e.t.Helper()
	return e.callback(e.authorize(claims, nil))
}

// userWithEmail creates an active user and sets its email.
func (e *oidcEnv) userWithEmail(username, email string) (User, string) {
	e.t.Helper()
	u, secret := createActive(e.t, e.s, e.clock, username)
	exec(e.t, e.s, `UPDATE core_users SET email = ? WHERE id = ?`, email, u.ID)
	return u, secret
}

func (e *oidcEnv) pin(userID string) (string, string) {
	e.t.Helper()
	var iss, sub *string
	if err := e.s.db.Read().QueryRow(`SELECT oidc_issuer, oidc_subject FROM core_users WHERE id = ?`, userID).Scan(&iss, &sub); err != nil {
		e.t.Fatal(err)
	}
	if iss == nil || sub == nil {
		return "", ""
	}
	return *iss, *sub
}

// oidcAudits are the audited sign-ins with method oidc.
func (e *oidcEnv) oidcAudits() []map[string]string {
	var out []map[string]string
	entries, _, err := e.audit.List(context.Background(), core.AuditQuery{Limit: 1000})
	if err != nil {
		e.t.Fatal(err)
	}
	slices.Reverse(entries)
	for _, a := range entries {
		var in map[string]string
		if a.Action != AuditLoginAction || json.Unmarshal(a.Input, &in) != nil || in["method"] != MethodOIDC {
			continue
		}
		in["result"], in["code"] = a.Result, a.ErrorCode
		out = append(out, in)
	}
	return out
}

func TestOIDCLinkAndPin(t *testing.T) {
	e := newOIDCEnv(t, false)
	alice, _ := e.userWithEmail("alice", "Alice@Example.com")

	a := e.authorize(e.claims("s-1", "alice@example.com"), nil)
	if got := a.query.Get("code_challenge_method"); got != "S256" || a.query.Get("code_challenge") == "" {
		t.Errorf("authorization asks PKCE %q; want S256", got)
	}
	if got := a.query.Get("scope"); got != "openid email" {
		t.Errorf("scope %q; want openid email", got)
	}
	if got := a.query.Get("redirect_uri"); got != testRedirectBase+OIDCCallbackPath {
		t.Errorf("redirect_uri %q; want the stored base's callback", got)
	}
	res, err := e.callback(a)
	if err != nil {
		t.Fatal(err)
	}
	if res.User.ID != alice.ID || res.SessionID == "" || res.Ticket != "" {
		t.Fatalf("sign-in %+v; want a session for alice", res)
	}
	if iss, sub := e.pin(alice.ID); iss != e.idp.URL() || sub != "s-1" {
		t.Errorf("pin (%q, %q); want the issuer and s-1", iss, sub)
	}

	// The pin, not the email, matches later: a changed and unverified email
	// still signs alice in.
	later := e.claims("s-1", "someone@else.example")
	later["email_verified"] = false
	if res, err = e.signIn(later); err != nil || res.User.ID != alice.ID {
		t.Fatalf("pinned sign-in: %+v, %v; want alice", res, err)
	}
	if n := count(t, e.s, "core_sessions", "user_id = ?", alice.ID); n != 2 {
		t.Errorf("alice has %d sessions; want 2", n)
	}
	audits := e.oidcAudits()
	if len(audits) != 2 || audits[0]["result"] != "ok" || audits[0]["username"] != "alice" {
		t.Errorf("audits %v; want two ok oidc sign-ins of alice", audits)
	}
}

func TestOIDCRefusals(t *testing.T) {
	otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		setup func(e *oidcEnv, alice User)
		call  func(e *oidcEnv) (SignInResult, error)
		want  errcode.Code
	}{
		{"unverified email", nil, func(e *oidcEnv) (SignInResult, error) {
			c := e.claims("s-1", "alice@example.com")
			c["email_verified"] = false
			return e.signIn(c)
		}, errcode.InvalidCredentials},
		{"unknown email", nil, func(e *oidcEnv) (SignInResult, error) {
			return e.signIn(e.claims("s-1", "nobody@example.com"))
		}, errcode.NoLinkedUser},
		{"different subject for a pinned user", func(e *oidcEnv, alice User) {
			exec(e.t, e.s, `UPDATE core_users SET oidc_issuer = ?, oidc_subject = 's-1' WHERE id = ?`, e.idp.URL(), alice.ID)
		}, func(e *oidcEnv) (SignInResult, error) {
			return e.signIn(e.claims("s-2", "alice@example.com"))
		}, errcode.InvalidCredentials},
		{"colliding sub after the issuer changes", func(e *oidcEnv, alice User) {
			exec(e.t, e.s, `UPDATE core_users SET oidc_issuer = 'https://old.example', oidc_subject = 's-1' WHERE id = ?`, alice.ID)
		}, func(e *oidcEnv) (SignInResult, error) {
			return e.signIn(e.claims("s-1", "alice@example.com"))
		}, errcode.InvalidCredentials},
		{"wrong nonce", nil, func(e *oidcEnv) (SignInResult, error) {
			c := e.claims("s-1", "alice@example.com")
			c["nonce"] = "another"
			return e.signIn(c)
		}, errcode.InvalidCredentials},
		{"wrong audience", nil, func(e *oidcEnv) (SignInResult, error) {
			c := e.claims("s-1", "alice@example.com")
			c["aud"] = "someone-else"
			return e.signIn(c)
		}, errcode.InvalidCredentials},
		{"wrong issuer", nil, func(e *oidcEnv) (SignInResult, error) {
			c := e.claims("s-1", "alice@example.com")
			c["iss"] = "https://evil.example"
			return e.signIn(c)
		}, errcode.InvalidCredentials},
		{"bad signature", nil, func(e *oidcEnv) (SignInResult, error) {
			return e.callback(e.authorize(e.claims("s-1", "alice@example.com"), func(c *oidcfake.Code) { c.Key = otherKey }))
		}, errcode.InvalidCredentials},
		{"expired token", nil, func(e *oidcEnv) (SignInResult, error) {
			c := e.claims("s-1", "alice@example.com")
			c["exp"] = e.clock.Now().Add(-time.Minute).Unix()
			return e.signIn(c)
		}, errcode.InvalidCredentials},
		{"wrong PKCE verifier", nil, func(e *oidcEnv) (SignInResult, error) {
			return e.callback(e.authorize(e.claims("s-1", "alice@example.com"), func(c *oidcfake.Code) { c.Challenge = "other" }))
		}, errcode.InvalidCredentials},
		{"missing state", nil, func(e *oidcEnv) (SignInResult, error) {
			a := e.authorize(e.claims("s-1", "alice@example.com"), nil)
			a.state = ""
			return e.callback(a)
		}, errcode.InvalidCredentials},
		{"state of another cookie", nil, func(e *oidcEnv) (SignInResult, error) {
			a := e.authorize(e.claims("s-1", "alice@example.com"), nil)
			b := e.authorize(e.claims("s-1", "alice@example.com"), nil)
			a.state = b.state
			return e.callback(a)
		}, errcode.InvalidCredentials},
		{"expired state", nil, func(e *oidcEnv) (SignInResult, error) {
			a := e.authorize(e.claims("s-1", "alice@example.com"), nil)
			e.clock.Advance(OIDCStateTTL)
			return e.callback(a)
		}, errcode.InvalidCredentials},
		{"replayed state", nil, func(e *oidcEnv) (SignInResult, error) {
			a := e.authorize(e.claims("s-1", "nobody@example.com"), nil)
			if _, err := e.callback(a); errcode.From(err).Code != errcode.NoLinkedUser {
				e.t.Fatalf("first callback: %v; want no_linked_user", err)
			}
			// A code that would pass for a's verifier and nonce.
			c := e.claims("s-1", "alice@example.com")
			c["nonce"] = a.query.Get("nonce")
			e.idp.Register("replay", oidcfake.Code{Claims: c, Challenge: a.query.Get("code_challenge")})
			a.code = "replay"
			return e.callback(a)
		}, errcode.InvalidCredentials},
		{"disabled user", func(e *oidcEnv, alice User) {
			exec(e.t, e.s, `UPDATE core_users SET disabled_at = ? WHERE id = ?`, formatTime(t0), alice.ID)
		}, func(e *oidcEnv) (SignInResult, error) {
			return e.signIn(e.claims("s-1", "alice@example.com"))
		}, errcode.InvalidCredentials},
		{"method off", nil, func(e *oidcEnv) (SignInResult, error) {
			a := e.authorize(e.claims("s-1", "alice@example.com"), nil)
			exec(e.t, e.s, `UPDATE auth_settings SET oidc_issuer = NULL`)
			return e.callback(a)
		}, errcode.MethodOff},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newOIDCEnv(t, false)
			alice, _ := e.userWithEmail("alice", "alice@example.com")
			if c.setup != nil {
				c.setup(e, alice)
			}
			users := count(t, e.s, "core_users", "1 = 1")
			res, err := c.call(e)
			if code := errcode.From(err).Code; code != c.want {
				t.Fatalf("callback: %+v, %v; want %s", res, err, c.want)
			}
			if strings.Contains(err.Error(), oidcfake.LeakMarker) {
				t.Errorf("error %q carries the provider's body", err)
			}
			if n := count(t, e.s, "core_sessions", "1 = 1"); n != 0 {
				t.Errorf("%d sessions after a refused callback", n)
			}
			if n := count(t, e.s, "core_users", "1 = 1"); n != users {
				t.Errorf("users %d after a refused callback; want %d", n, users)
			}
			if c.name != "replayed state" {
				if got := failures(e.s, sourceKey("192.0.2.7")); got != 1 {
					t.Errorf("source failures %d; want the refusal counted once", got)
				}
			}
			if audits := e.oidcAudits(); len(audits) == 0 || audits[len(audits)-1]["code"] != string(c.want) {
				t.Errorf("audits %v; want the refusal audited with method oidc", audits)
			}
		})
	}

	t.Run("the source limit refuses the next callback", func(t *testing.T) {
		e := newOIDCEnv(t, false)
		for range SourceFailureLimit {
			if _, err := e.signIn(e.claims("s-1", "nobody@example.com")); errcode.From(err).Code != errcode.NoLinkedUser {
				t.Fatalf("callback: %v; want no_linked_user", err)
			}
		}
		if _, err := e.signIn(e.claims("s-1", "nobody@example.com")); errcode.From(err).Code != errcode.RateLimited {
			t.Errorf("callback past the limit: %v; want rate_limited", err)
		}
	})

	t.Run("discovery is cached for an hour", func(t *testing.T) {
		e := newOIDCEnv(t, false)
		e.userWithEmail("alice", "alice@example.com")
		for range 2 {
			if _, err := e.signIn(e.claims("s-1", "alice@example.com")); err != nil {
				t.Fatal(err)
			}
		}
		if n := e.idp.Discovery.Load(); n != 1 {
			t.Errorf("discovery fetched %d times for two sign-ins; want 1", n)
		}
		e.clock.Advance(61 * time.Minute)
		if _, err := e.signIn(e.claims("s-1", "alice@example.com")); err != nil {
			t.Fatal(err)
		}
		if n := e.idp.Discovery.Load(); n != 2 {
			t.Errorf("discovery fetched %d times after 61 minutes; want 2", n)
		}
	})
}

func TestOIDCStartNeedsTheRedirectBase(t *testing.T) {
	e := newOIDCEnv(t, false)
	if _, err := e.s.StartOIDC(context.Background(), "https://other.example"); errcode.From(err).Code != errcode.MethodOff {
		t.Errorf("start on another origin: %v; want method_off", err)
	}
	if _, err := e.s.StartOIDC(context.Background(), "https://PANEL.example:443"); err != nil {
		t.Errorf("start on the base with its default port: %v", err)
	}
}

func TestOIDCActivatesInvite(t *testing.T) {
	ctx := context.Background()
	e := newOIDCEnv(t, false)
	inv, err := e.s.Invite(ctx, InviteRequest{Username: "mia", Role: RoleMember, Email: "mia@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	res, err := e.signIn(e.claims("s-9", "MIA@example.com"))
	if err != nil {
		t.Fatal(err)
	}
	if res.User.ID != inv.User.ID || res.User.State != StateActive || res.SessionID == "" {
		t.Fatalf("invite sign-in %+v; want mia active with a session", res)
	}
	if _, err := e.s.Login(ctx, LoginRequest{Username: "mia", Password: testPassword, Code: "000000", Source: "192.0.2.8"}); errcode.From(err).Code != errcode.InvalidCredentials {
		t.Errorf("mia's password login after OIDC activation: %v; want invalid_credentials", err)
	}
	audits := e.oidcAudits()
	if len(audits) != 1 || audits[0]["activated"] != "true" || audits[0]["result"] != "ok" {
		t.Errorf("audits %v; want one ok oidc sign-in that activated mia", audits)
	}

	// An admin can renew the link, which ends the old one, but not cancel
	// an active user.
	if _, err := e.s.CancelInvite(ctx, "mia"); errcode.From(err).Code != errcode.InvalidInput {
		t.Errorf("cancel of an activated invite: %v; want invalid_input", err)
	}
	renewed, err := e.s.RenewInvite(ctx, "mia")
	if err != nil {
		t.Fatalf("renew of an activated invite's link: %v", err)
	}
	if _, err := e.s.BeginEnrollment(ctx, inv.Token, "192.0.2.8", false); errcode.From(err).Code != errcode.EnrollmentExpired {
		t.Errorf("the replaced link: %v; want enrollment_expired", err)
	}
	inv.Token = renewed.Token

	// The enrollment link still sets the password.
	begin, err := e.s.BeginEnrollment(ctx, inv.Token, "192.0.2.8", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.FinishEnrollment(ctx, EnrollRequest{Token: inv.Token, Password: testPassword,
		Code: code(t, begin.Secret, e.clock), Source: "192.0.2.8"}); err != nil {
		t.Fatalf("enrollment after OIDC activation: %v", err)
	}

	// With the code step on, an invite cannot sign in before it enrolls.
	t.Run("requireTotp", func(t *testing.T) {
		e := newOIDCEnv(t, true)
		inv, err := e.s.Invite(ctx, InviteRequest{Username: "zoe", Role: RoleMember, Email: "zoe@example.com"})
		if err != nil {
			t.Fatal(err)
		}
		res, err := e.signIn(e.claims("s-7", "zoe@example.com"))
		if errcode.From(err).Code != errcode.InvalidCredentials || res.Ticket != "" {
			t.Fatalf("invite sign-in with the code step on: %+v, %v; want refused", res, err)
		}
		var state string
		if err := e.s.db.Read().QueryRow(`SELECT state FROM core_users WHERE id = ?`, inv.User.ID).Scan(&state); err != nil || state != StatePending {
			t.Errorf("zoe's state %q, %v; want pending", state, err)
		}
		if iss, _ := e.pin(inv.User.ID); iss != "" {
			t.Errorf("zoe pinned to %q by a refused sign-in", iss)
		}
	})
}

func TestOIDCCodeStep(t *testing.T) {
	ctx := context.Background()
	e := newOIDCEnv(t, true)
	alice, secret := e.userWithEmail("alice", "alice@example.com")
	res, err := e.signIn(e.claims("s-1", "alice@example.com"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Ticket == "" || res.SessionID != "" || !res.TicketExpires.Equal(e.s.now().Add(SignInTicketTTL)) {
		t.Fatalf("sign-in with the code step: %+v; want a five-minute ticket and no session", res)
	}
	if n := count(t, e.s, "core_sessions", "1 = 1"); n != 0 {
		t.Errorf("%d sessions before the code", n)
	}
	if iss, _ := e.pin(alice.ID); iss != "" {
		t.Errorf("alice pinned before the code")
	}
	step := TOTPStepRequest{Ticket: res.Ticket, Code: wrongCode(t, secret, e.clock), Source: "192.0.2.7"}
	if _, err := e.s.FinishTOTPStep(ctx, step); errcode.From(err).Code != errcode.InvalidCredentials {
		t.Errorf("wrong code: %v; want invalid_credentials", err)
	}
	if got := failures(e.s, pairKey("alice", "192.0.2.7")); got != 1 {
		t.Errorf("pair failures %d; want the wrong code counted", got)
	}
	step.Code = code(t, secret, e.clock)
	got, err := e.s.FinishTOTPStep(ctx, step)
	if err != nil || got.User.ID != alice.ID || got.SessionID == "" {
		t.Fatalf("code step: %+v, %v; want alice's session", got, err)
	}
	if iss, sub := e.pin(alice.ID); iss != e.idp.URL() || sub != "s-1" {
		t.Errorf("pin after the code (%q, %q)", iss, sub)
	}
	if _, err := e.s.FinishTOTPStep(ctx, step); errcode.From(err).Code != errcode.InvalidCredentials {
		t.Errorf("the same ticket and code again: %v; want refused", err)
	}

	// Pinned now, alice's next ticket links nothing; it is still used once.
	res, err = e.signIn(e.claims("s-1", "alice@example.com"))
	if err != nil {
		t.Fatal(err)
	}
	e.clock.Advance(TOTPPeriod)
	pinned := TOTPStepRequest{Ticket: res.Ticket, Code: code(t, secret, e.clock), Source: "192.0.2.7"}
	if _, err := e.s.FinishTOTPStep(ctx, pinned); err != nil {
		t.Fatalf("pinned user's code step: %v", err)
	}
	e.clock.Advance(TOTPPeriod)
	if _, err := e.s.FinishTOTPStep(ctx, TOTPStepRequest{Ticket: res.Ticket, Code: code(t, secret, e.clock), Source: "192.0.2.7"}); errcode.From(err).Code != errcode.InvalidCredentials {
		t.Errorf("a used ticket with a fresh code: %v; want refused", err)
	}

	// Another ticket with a code already used is refused by the replay rule.
	res, err = e.signIn(e.claims("s-1", "alice@example.com"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.FinishTOTPStep(ctx, TOTPStepRequest{Ticket: res.Ticket, Code: pinned.Code, Source: "192.0.2.7"}); errcode.From(err).Code != errcode.InvalidCredentials {
		t.Errorf("a reused code: %v; want refused", err)
	}
	e.clock.Advance(SignInTicketTTL)
	if _, err := e.s.FinishTOTPStep(ctx, TOTPStepRequest{Ticket: res.Ticket, Code: code(t, secret, e.clock), Source: "192.0.2.7"}); errcode.From(err).Code != errcode.InvalidCredentials {
		t.Errorf("an expired ticket: %v; want refused", err)
	}
	if n := count(t, e.s, "core_sessions", "1 = 1"); n != 2 {
		t.Errorf("%d sessions; want the two code steps'", n)
	}

	// OIDC turned off between the callback and the code ends the sign-in.
	res, err = e.signIn(e.claims("s-1", "alice@example.com"))
	if err != nil {
		t.Fatal(err)
	}
	exec(t, e.s, `UPDATE auth_settings SET oidc_issuer = NULL`)
	e.clock.Advance(TOTPPeriod)
	if _, err := e.s.FinishTOTPStep(ctx, TOTPStepRequest{Ticket: res.Ticket, Code: code(t, secret, e.clock), Source: "192.0.2.7"}); errcode.From(err).Code != errcode.MethodOff {
		t.Errorf("code step after OIDC was turned off: %v; want method_off", err)
	}
}

func TestOIDCRefusalUnauditedAnswersInternal(t *testing.T) {
	e := newOIDCEnv(t, false)
	e.s.audit = &failingAudit{MemAudit: e.audit, fail: 1}
	_, err := e.signIn(e.claims("sub-nobody", "nobody@example.com"))
	if errcode.From(err).Code != errcode.Internal {
		t.Errorf("a refused sign-in whose audit write failed answered %v, want internal", err)
	}
}
