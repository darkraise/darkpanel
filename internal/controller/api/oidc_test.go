package api

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/controller/auth/oidcfake"
	"github.com/darkraise/darkpanel/internal/core/coretest"
)

const testOIDCBase = "https://panel.example"

// oidcHarness is a harness whose auth service reaches a fake provider set
// up as the OIDC method; the admin's email is admin@example.com.
type oidcHarness struct {
	*harness
	idp *oidcfake.Provider
	n   int
}

func newOIDCHarness(t *testing.T, requireTOTP bool) *oidcHarness {
	t.Helper()
	h := newHarness(t)
	idp := oidcfake.New(t)
	h.auth = auth.New(h.db, coretest.Sealer{}, h.clock, h.audit, auth.Options{Params: testParams, OIDCTransport: idp.Transport()})
	h.cfg.Auth = h.auth
	srv, err := New(h.cfg)
	if err != nil {
		t.Fatal(err)
	}
	h.srv = srv
	ct, err := coretest.Sealer{}.Seal("auth_settings.oidc_client_secret_ct.1", []byte("oidc-secret"))
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO auth_settings (id, oidc_issuer, oidc_client_id, oidc_client_secret_ct, oidc_display_name,
			oidc_redirect_base, oidc_require_totp, updated_at) VALUES (1, ?, 'darkpanel', ?, 'Acme SSO', ?, ?, '2026-11-24T00:00:00Z')`,
			[]any{idp.URL(), ct, testOIDCBase, requireTOTP}},
		{`UPDATE core_users SET email = 'admin@example.com' WHERE id = ?`, []any{h.user.ID}},
	} {
		if _, err := h.db.Writer().Exec(q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	return &oidcHarness{harness: h, idp: idp}
}

// get serves GET target with the cookie header cookie, when not empty.
func (h *oidcHarness) get(target, cookie string) *httptest.ResponseRecorder {
	r := request(http.MethodGet, target, "")
	if cookie != "" {
		r.Header.Add("Cookie", cookie)
	}
	return h.serve(r)
}

func cookieNamed(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// signIn starts a sign-in on host, has the provider issue a code for sub and email
// and follows the callback; it returns the start's and callback's answers.
func (h *oidcHarness) signIn(host, sub, email string) (start, callback *httptest.ResponseRecorder) {
	h.t.Helper()
	start = h.get("https://"+host+oidcStartPath, "")
	if start.Code != http.StatusFound {
		h.t.Fatalf("start: %d %s", start.Code, start.Body)
	}
	loc, err := url.Parse(start.Header().Get("Location"))
	if err != nil {
		h.t.Fatal(err)
	}
	q := loc.Query()
	now := h.clock.Now()
	h.n++
	code := "c" + strconv.Itoa(h.n)
	h.idp.Register(code, oidcfake.Code{Challenge: q.Get("code_challenge"), Claims: map[string]any{
		"iss": h.idp.URL(), "aud": "darkpanel", "sub": sub, "email": email, "email_verified": true, "nonce": q.Get("nonce"),
		"iat": now.Unix(), "exp": now.Add(5 * time.Minute).Unix()}})
	state := cookieNamed(start, oidcStateCookie)
	if state == nil {
		h.t.Fatal("start set no state cookie")
	}
	callback = h.get(testOIDCBase+oidcCallbackPath+"?state="+url.QueryEscape(q.Get("state"))+"&code="+code,
		state.Name+"="+state.Value)
	return start, callback
}

func (h *oidcHarness) sessions() int {
	h.t.Helper()
	var n int
	if err := h.db.Read().QueryRow(`SELECT count(*) FROM core_sessions`).Scan(&n); err != nil {
		h.t.Fatal(err)
	}
	return n
}

// wantPage checks a callback page: 200, no caching, no referrer, and one
// navigation, a meta refresh to /?signin=outcome.
func wantPage(t *testing.T, rec *httptest.ResponseRecorder, outcome string) {
	t.Helper()
	body := rec.Body.String()
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("Referrer-Policy") != "no-referrer" ||
		!strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") {
		t.Errorf("callback page %d %v; want 200 HTML, no-store, no-referrer", rec.Code, rec.Header())
	}
	want := `<meta http-equiv="refresh" content="0;url=/?signin=` + outcome + `">`
	if strings.Count(body, want) != 1 || strings.Count(body, "url=") != 1 || strings.Contains(body, "<script") ||
		strings.Contains(body, "<a ") || strings.Contains(body, "<form") {
		t.Errorf("callback page %q; want only the navigation %s", body, want)
	}
}

func TestCallbackCookiesAndPage(t *testing.T) {
	h := newOIDCHarness(t, false)
	start, callback := h.signIn("PANEL.example:443", "s-1", "Admin@Example.com")
	state := cookieNamed(start, oidcStateCookie)
	if state.SameSite != http.SameSiteLaxMode || !state.HttpOnly || !state.Secure || state.Path != oidcCallbackPath ||
		state.MaxAge != int(auth.OIDCStateTTL/time.Second) {
		t.Errorf("state cookie %+v; want Lax, HttpOnly, Secure, the callback path and ten minutes", state)
	}
	loc, _ := url.Parse(start.Header().Get("Location"))
	if got := loc.Query().Get("redirect_uri"); got != testOIDCBase+oidcCallbackPath {
		t.Errorf("redirect_uri %q; want the stored base's callback, not the request Host", got)
	}
	if got := h.idp.Redirects(); len(got) != 1 || got[0] != testOIDCBase+oidcCallbackPath {
		t.Errorf("token request redirect_uri %v; want the stored base's callback", got)
	}
	wantPage(t, callback, outcomeOK)
	session := cookieNamed(callback, SessionCookieSecure)
	if session == nil || session.Value == "" || session.SameSite != http.SameSiteStrictMode {
		t.Fatalf("callback session cookie %+v; want a Strict session", session)
	}
	if cleared := cookieNamed(callback, oidcStateCookie); cleared == nil || cleared.MaxAge >= 0 || cleared.Path != oidcCallbackPath {
		t.Errorf("state cookie after the callback %+v; want it cleared", cleared)
	}
	rec := h.serve(withSecureSession(request(http.MethodGet, "https://panel.example/api/v1/auth/session", ""), session.Value))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"username":"admin"`) {
		t.Errorf("session from the callback: %d %s", rec.Code, rec.Body)
	}

	_, callback = h.signIn("panel.example", "s-2", "nobody@example.com")
	wantPage(t, callback, outcomeNoLinkedUser)
	if cookieNamed(callback, SessionCookieSecure) != nil {
		t.Error("an unlinked sign-in set a session cookie")
	}
	if strings.Contains(callback.Body.String(), oidcfake.LeakMarker) || strings.Contains(h.log.String(), oidcfake.LeakMarker) {
		t.Error("the provider's body reached the page or the log")
	}

	if rec := h.get("https://other.example"+oidcStartPath, ""); rec.Code != http.StatusOK || cookieNamed(rec, oidcStateCookie) != nil {
		t.Errorf("start on another origin: %d; want the sign-in page and no state cookie", rec.Code)
	} else {
		wantPage(t, rec, outcomeMethodOff)
	}
	if rec := h.get(testOIDCBase+oidcCallbackPath+"?state=x&code=y", ""); rec.Code != http.StatusOK {
		t.Errorf("callback without a state cookie: %d", rec.Code)
	} else {
		wantPage(t, rec, outcomeRefused)
	}

	// Refused callbacks count on the source; past its limit the page answers
	// 429.
	for range auth.SourceFailureLimit {
		if rec = h.get(testOIDCBase+oidcCallbackPath+"?state=x&code=y", ""); rec.Code == http.StatusTooManyRequests {
			break
		}
	}
	if rec.Code != http.StatusTooManyRequests || !strings.Contains(rec.Body.String(), "url=/?signin="+outcomeRefused+`"`) {
		t.Errorf("callback past the source limit: %d %s; want 429 and the refused page", rec.Code, rec.Body)
	}
}

// withSecureSession adds the HTTPS listener's session cookie.
func withSecureSession(r *http.Request, session string) *http.Request {
	r.Header.Add("Cookie", SessionCookieSecure+"="+session)
	return r
}

func TestMethodsRoute(t *testing.T) {
	methods := func(h *harness, target string, local netip.Addr) map[string]any {
		t.Helper()
		r := request(http.MethodGet, target, "")
		if local.IsValid() {
			r = r.WithContext(context.WithValue(r.Context(), http.LocalAddrContextKey, &net.TCPAddr{IP: local.AsSlice(), Port: 443}))
		}
		rec := h.serve(r)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: %d %s", target, rec.Code, rec.Body)
		}
		var out map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	plain := newHarness(t)
	if got := methods(plain, "https://panel.example"+methodsPath, netip.Addr{}); got["oidc"] != nil || got["tailscale"] != false {
		t.Errorf("methods by default %v; want no OIDC and no Tailscale", got)
	}

	h := newOIDCHarness(t, false)
	if got := methods(h.harness, testOIDCBase+methodsPath, netip.Addr{}); !strings.Contains(jsonString(t, got["oidc"]), `"displayName":"Acme SSO"`) {
		t.Errorf("methods on the redirect base %v; want the Acme SSO button", got)
	}
	for _, target := range []string{"https://other.example" + methodsPath, "http://panel.example" + methodsPath} {
		if got := methods(h.harness, target, netip.Addr{}); got["oidc"] != nil {
			t.Errorf("methods at %s %v; want no OIDC button", target, got)
		}
	}

	ts := netip.MustParseAddr("100.64.0.1")
	h.srv.SetTailscaleAddrs(func() []netip.Addr { return []netip.Addr{ts} })
	if got := methods(h.harness, "http://100.64.0.1"+methodsPath, ts); got["tailscale"] != false {
		t.Errorf("Tailscale listener with the method off %v; want false", got)
	}
	if _, err := h.db.Writer().Exec(`UPDATE auth_settings SET tailscale_enabled = 1`); err != nil {
		t.Fatal(err)
	}
	if got := methods(h.harness, "http://100.64.0.1"+methodsPath, ts); got["tailscale"] != true {
		t.Errorf("Tailscale listener with the method on %v; want true", got)
	}
	for _, local := range []netip.Addr{netip.MustParseAddr("192.0.2.1"), netip.MustParseAddr("127.0.0.1")} {
		if got := methods(h.harness, "http://panel.example"+methodsPath, local); got["tailscale"] != false {
			t.Errorf("listener %s %v; want tailscale false", local, got)
		}
	}
}

func jsonString(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestOIDCRequireTotp(t *testing.T) {
	h := newOIDCHarness(t, true)
	_, callback := h.signIn("panel.example", "s-1", "admin@example.com")
	wantPage(t, callback, outcomeTOTP)
	if cookieNamed(callback, SessionCookieSecure) != nil || h.sessions() != 0 {
		t.Fatal("a session exists before the code")
	}
	step := cookieNamed(callback, signInStepCookie)
	if step == nil || step.Path != totpStepPath || step.MaxAge != int(auth.SignInTicketTTL/time.Second) || !step.HttpOnly ||
		!step.Secure || step.SameSite != http.SameSiteStrictMode {
		t.Fatalf("code step cookie %+v; want five minutes on the code route, HttpOnly, Secure, Strict", step)
	}
	post := func(c *http.Cookie, code string) *httptest.ResponseRecorder {
		r := request(http.MethodPost, testOIDCBase+totpStepPath, `{"code":"`+code+`"}`)
		r.Header.Add("Cookie", c.Name+"="+c.Value)
		return h.serve(r)
	}
	code := h.code()
	rec := post(step, code)
	if rec.Code != http.StatusOK || cookieNamed(rec, SessionCookieSecure) == nil || h.sessions() != 1 {
		t.Fatalf("code step: %d %s; want a session", rec.Code, rec.Body)
	}

	_, callback = h.signIn("panel.example", "s-1", "admin@example.com")
	rec = post(cookieNamed(callback, signInStepCookie), code)
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), `"auth.invalid_credentials"`) || h.sessions() != 1 {
		t.Errorf("a reused code: %d %s; want auth.invalid_credentials and no session", rec.Code, rec.Body)
	}
}
