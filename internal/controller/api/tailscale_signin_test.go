package api

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/controller/listen"
	"github.com/darkraise/darkpanel/internal/core"
)

// The Tailscale listener's address and a user-owned peer of the tailnet.
var (
	tsListener = netip.MustParseAddr("100.64.0.1")
	tsPeer     = "100.101.0.5"
	tsHost     = "https://panel.tail1234.ts.net"
)

// tsHarness is an API server whose Tailscale method is on and whose whois
// answers from answers (a JSON text or an error) per address.
type tsHarness struct {
	*harness
	mu      sync.Mutex
	answers map[string]any
	calls   int
}

func newTSHarness(t *testing.T, requireTOTP bool) *tsHarness {
	t.Helper()
	h := &tsHarness{harness: newHarness(t), answers: map[string]any{tsPeer: tsAnswer(nil)}}
	h.cfg.Whois = func(_ context.Context, addr netip.Addr) (json.RawMessage, error) {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.calls++
		switch a := h.answers[addr.String()].(type) {
		case string:
			return json.RawMessage(a), nil
		case error:
			return nil, a
		}
		return json.RawMessage(`{"loginName":"","userId":0,"nodeName":"","stableId":"","tags":[],"sharer":0,"addresses":[]}`), nil
	}
	h.cfg.Clock = h.clock
	srv, err := New(h.cfg)
	if err != nil {
		t.Fatal(err)
	}
	h.srv = srv
	srv.SetTailscaleAddrs(func() []netip.Addr { return []netip.Addr{tsListener} })
	h.exec(`INSERT INTO auth_settings (id, tailscale_enabled, tailscale_require_totp, updated_at) VALUES (1, 1, ?, '2026-11-24T00:00:00Z')`, requireTOTP)
	h.exec(`UPDATE core_users SET tailnet_login = 'Alice@Example.com' WHERE id = ?`, h.user.ID)
	return h
}

func (h *tsHarness) exec(q string, args ...any) {
	h.t.Helper()
	if _, err := h.db.Writer().Exec(q, args...); err != nil {
		h.t.Fatal(err)
	}
}

func (h *tsHarness) answer(addr string, a any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.answers[addr] = a
}

func (h *tsHarness) agentCalls() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.calls
}

func (h *tsHarness) sessions() int {
	h.t.Helper()
	var n int
	if err := h.db.Read().QueryRow(`SELECT count(*) FROM core_sessions`).Scan(&n); err != nil {
		h.t.Fatal(err)
	}
	return n
}

// tsAnswer is the whois answer of the user-owned peer, changed by mod.
func tsAnswer(mod func(w *auth.TailnetWhois)) string {
	w := auth.TailnetWhois{LoginName: "alice@example.com", UserID: 4242, NodeName: "laptop.tail1234.ts.net", StableID: "nLaptop",
		Tags: []string{}, Addresses: []string{tsPeer, "fd7a:115c:a1e0::5"}}
	if mod != nil {
		mod(&w)
	}
	b, _ := json.Marshal(w)
	return string(b)
}

// tsRequest is a request to the Tailscale route from remote, arriving on
// local (the Tailscale listener unless changed).
type tsRequest struct {
	method, remote, origin, contentType string
	local                               netip.Addr
	proxied                             bool
	plain                               bool // no TLS
}

func (h *tsHarness) do(q tsRequest) *httptest.ResponseRecorder {
	h.t.Helper()
	body := ""
	if q.method == http.MethodPost {
		body = "{}"
	}
	r := request(q.method, tsHost+tailscalePath, body)
	if q.contentType != "" {
		r.Header.Set("Content-Type", q.contentType)
	}
	if q.origin != "" {
		r.Header.Set("Origin", q.origin)
	}
	r.RemoteAddr = net.JoinHostPort(q.remote, "41000")
	r.TLS = &tls.ConnectionState{}
	if q.plain {
		r.TLS = nil
	}
	local := q.local
	if !local.IsValid() {
		local = tsListener
	}
	ctx := context.WithValue(r.Context(), http.LocalAddrContextKey, &net.TCPAddr{IP: local.AsSlice(), Port: 443})
	if q.proxied {
		ctx = core.WithProxied(ctx, core.Proxied{ListenerID: "L1", Client: netip.MustParseAddr(q.remote),
			Transport: netip.MustParseAddrPort("127.0.0.1:9000"), PublicHost: "panel.example"})
	}
	return h.serve(r.WithContext(ctx))
}

func get(remote string) tsRequest { return tsRequest{method: http.MethodGet, remote: remote} }
func post(remote string) tsRequest {
	return tsRequest{method: http.MethodPost, remote: remote, origin: tsHost}
}

func (h *tsHarness) tailnetUserID(userID string) string {
	h.t.Helper()
	var v *string
	if err := h.db.Read().QueryRow(`SELECT tailnet_user_id FROM core_users WHERE id = ?`, userID).Scan(&v); err != nil {
		h.t.Fatal(err)
	}
	if v == nil {
		return ""
	}
	return *v
}

func TestContinueAs(t *testing.T) {
	h := newTSHarness(t, false)
	rec := h.do(get(tsPeer))
	var match auth.TailscaleMatch
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &match) != nil {
		t.Fatalf("GET: %d %s", rec.Code, rec.Body)
	}
	if match != (auth.TailscaleMatch{Username: h.user.Username, LoginName: "alice@example.com", NodeName: "laptop.tail1234.ts.net", CodeReady: true}) {
		t.Errorf("GET answered %+v", match)
	}
	if h.tailnetUserID(h.user.ID) != "" {
		t.Fatal("the GET pinned the tailnet user")
	}

	// A node not yet remembered asks the code and pins nothing.
	rec = h.do(post(tsPeer))
	if rec.Code != http.StatusOK || cookieNamed(rec, signInStepCookie) == nil || h.sessions() != 0 || h.tailnetUserID(h.user.ID) != "" {
		t.Fatalf("first POST: %d %s; want the code step, no session and no pin", rec.Code, rec.Body)
	}

	// The code step pins and remembers the node (Task 4); a remembered node
	// signs in without a code.
	h.exec(`UPDATE core_users SET tailnet_user_id = '4242' WHERE id = ?`, h.user.ID)
	h.exec(`INSERT INTO core_user_tailnet_devices (user_id, stable_id, node_name, remembered_at, last_used_at)
		VALUES (?, 'nLaptop', 'laptop.tail1234.ts.net', '2026-11-24T00:00:00Z', '2026-11-24T00:00:00Z')`, h.user.ID)
	rec = h.do(post(tsPeer))
	if rec.Code != http.StatusOK || cookieNamed(rec, SessionCookiePlain) == nil && cookieNamed(rec, SessionCookieSecure) == nil || h.sessions() != 1 {
		t.Fatalf("POST: %d %s; want a session", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `"csrfToken"`) {
		t.Errorf("POST body %s; want the session answer", rec.Body)
	}

	// Later sign-ins match the pinned id; a node of another user with the
	// same login name is refused.
	if rec = h.do(post(tsPeer)); rec.Code != http.StatusOK || h.sessions() != 2 {
		t.Fatalf("second POST: %d %s", rec.Code, rec.Body)
	}
	h.answer(tsPeer, tsAnswer(func(w *auth.TailnetWhois) { w.UserID = 9999 }))
	if rec = h.do(post(tsPeer)); rec.Code != http.StatusUnauthorized || h.sessions() != 2 {
		t.Fatalf("POST with another tailnet user id: %d %s; want 401 and no session", rec.Code, rec.Body)
	}

	// The POST's own fresh whois decides: a GET's cached match does not.
	h.answer(tsPeer, tsAnswer(nil))
	h.clock.Advance(time.Minute)
	if rec = h.do(get(tsPeer)); rec.Code != http.StatusOK {
		t.Fatalf("GET: %d %s", rec.Code, rec.Body)
	}
	h.answer(tsPeer, tsAnswer(func(w *auth.TailnetWhois) { w.Tags = []string{"tag:server"} }))
	if rec = h.do(post(tsPeer)); rec.Code != http.StatusUnauthorized || h.sessions() != 2 {
		t.Fatalf("POST after the node was tagged: %d %s; want 401", rec.Code, rec.Body)
	}

	// An invited user is refused and stays invited.
	if _, err := h.auth.Invite(context.Background(), auth.InviteRequest{Username: "bob", Role: auth.RoleMember,
		TailnetLogin: "bob@example.com"}); err != nil {
		t.Fatal(err)
	}
	bob := "100.101.0.6"
	h.answer(bob, tsAnswer(func(w *auth.TailnetWhois) {
		w.LoginName, w.UserID, w.StableID, w.Addresses = "bob@example.com", 77, "nBob", []string{bob}
	}))
	if rec = h.do(post(bob)); rec.Code != http.StatusUnauthorized || h.sessions() != 2 {
		t.Fatalf("invited user's POST: %d %s; want 401 and no session", rec.Code, rec.Body)
	}
	var state, pinned string
	if err := h.db.Read().QueryRow(`SELECT state, coalesce(tailnet_user_id, '') FROM core_users WHERE username = 'bob'`).Scan(&state, &pinned); err != nil {
		t.Fatal(err)
	}
	if state != auth.StatePending || pinned != "" {
		t.Errorf("invited user after the POST: state %s, tailnet id %q; want pending and unpinned", state, pinned)
	}
}

func TestTailscaleRefusals(t *testing.T) {
	type setup func(h *tsHarness)
	cases := []struct {
		name   string
		setup  setup
		req    tsRequest
		status int
		code   string
		calls  int // agent calls
	}{
		{"method off", func(h *tsHarness) { h.exec(`UPDATE auth_settings SET tailscale_enabled = 0`) }, post(tsPeer), 403, "auth.method_off", 0},
		{"another listener", nil, tsRequest{method: http.MethodPost, remote: tsPeer, origin: tsHost, local: netip.MustParseAddr("127.0.0.1")},
			403, "auth.method_off", 0},
		{"plain HTTP on the Tailscale listener", nil, tsRequest{method: http.MethodPost, remote: tsPeer, origin: "http://panel.tail1234.ts.net",
			plain: true}, 403, "auth.method_off", 0},
		{"a proxied request", nil, tsRequest{method: http.MethodPost, remote: tsPeer, origin: "https://panel.example", proxied: true}, 403,
			"auth.method_off", 0},
		{"outside the tailnet ranges", nil, post("192.168.1.5"), 401, "auth.invalid_credentials", 0},
		{"just outside 100.64.0.0/10", nil, post("100.128.0.1"), 401, "auth.invalid_credentials", 0},
		{"loopback", nil, post("127.0.0.1"), 401, "auth.invalid_credentials", 0},
		{"no Origin", nil, tsRequest{method: http.MethodPost, remote: tsPeer}, 403, "auth.forbidden", 0},
		{"a foreign Origin", nil, tsRequest{method: http.MethodPost, remote: tsPeer, origin: "http://evil.example"}, 403, "auth.forbidden", 0},
		{"a form-encoded POST", nil, tsRequest{method: http.MethodPost, remote: tsPeer, origin: tsHost,
			contentType: "application/x-www-form-urlencoded"}, 415, "", 0},
		{"a tagged node", func(h *tsHarness) {
			h.answer(tsPeer, tsAnswer(func(w *auth.TailnetWhois) { w.Tags = []string{"tag:ci"} }))
		}, post(tsPeer), 401, "auth.invalid_credentials", 1},
		{"a shared-in node", func(h *tsHarness) {
			h.answer(tsPeer, tsAnswer(func(w *auth.TailnetWhois) { w.Sharer = 7 }))
		}, post(tsPeer), 401, "auth.invalid_credentials", 1},
		{"no node", func(h *tsHarness) { h.answer(tsPeer, nil) }, post(tsPeer), 401, "auth.invalid_credentials", 1},
		{"a user profile with no node", func(h *tsHarness) {
			h.answer(tsPeer, `{"loginName":"alice@example.com","userId":4242,"nodeName":"","stableId":"","tags":[],"sharer":0,"addresses":[]}`)
		}, post(tsPeer), 401, "auth.invalid_credentials", 1},
		{"a zero user id", func(h *tsHarness) {
			h.answer(tsPeer, tsAnswer(func(w *auth.TailnetWhois) { w.UserID = 0 }))
		}, post(tsPeer), 401, "auth.invalid_credentials", 1},
		{"an empty login name", func(h *tsHarness) {
			h.answer(tsPeer, tsAnswer(func(w *auth.TailnetWhois) { w.LoginName = "" }))
		}, post(tsPeer), 401, "auth.invalid_credentials", 1},
		{"the tagged-devices login", func(h *tsHarness) {
			h.exec(`UPDATE core_users SET tailnet_login = 'tagged-devices'`)
			h.answer(tsPeer, tsAnswer(func(w *auth.TailnetWhois) { w.LoginName = "tagged-devices" }))
		}, post(tsPeer), 401, "auth.invalid_credentials", 1},
		{"an answer that does not decode", func(h *tsHarness) { h.answer(tsPeer, "not json") }, post(tsPeer), 401, "auth.invalid_credentials", 1},
		{"a whois error", func(h *tsHarness) { h.answer(tsPeer, errors.New("tailscaled is down")) }, post(tsPeer), 401, "auth.invalid_credentials", 1},
		{"the request address is an offline host's", func(h *tsHarness) { h.managed(tsPeer, "nOther") }, post(tsPeer), 401,
			"auth.invalid_credentials", 1},
		{"the request address is an online host's", func(h *tsHarness) {
			h.managed(tsPeer, "nOther")
			h.exec(`UPDATE core_hosts SET last_seen_at = ? WHERE id = 'H9'`, h.clock.Now().UTC().Format(time.RFC3339Nano))
		}, post(tsPeer), 401, "auth.invalid_credentials", 1},
		{"the request address is a host's with tailscale disabled", func(h *tsHarness) {
			h.managed(tsPeer, "nOther")
			h.exec(`INSERT INTO core_module_state (host_id, module_id, enabled, available, reason, checked_at)
				VALUES ('H9', 'tailscale', 0, 0, 'disabled', '2026-11-24T00:00:00Z')`)
		}, post(tsPeer), 401, "auth.invalid_credentials", 1},
		{"another node address is a host's", func(h *tsHarness) { h.managed("fd7a:115c:a1e0::5", "nOther") }, post(tsPeer), 401,
			"auth.invalid_credentials", 1},
		{"the controller's own address", func(h *tsHarness) {
			if err := listen.SaveTailscaleAddrs(context.Background(), h.db, []netip.Addr{netip.MustParseAddr(tsPeer)}); err != nil {
				h.t.Fatal(err)
			}
		}, post(tsPeer), 401, "auth.invalid_credentials", 1},
		{"no linked user", func(h *tsHarness) { h.exec(`UPDATE core_users SET tailnet_login = NULL`) }, post(tsPeer), 403, "auth.no_linked_user", 1},
		{"a disabled user", func(h *tsHarness) { h.exec(`UPDATE core_users SET disabled_at = '2026-11-24T00:00:00Z'`) }, post(tsPeer), 401,
			"auth.invalid_credentials", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newTSHarness(t, false)
			if tc.setup != nil {
				tc.setup(h)
			}
			for _, q := range []tsRequest{tc.req, {method: http.MethodGet, remote: tc.req.remote, local: tc.req.local, proxied: tc.req.proxied, plain: tc.req.plain}} {
				if q.method == http.MethodGet && tc.status != 401 && tc.code != "auth.method_off" && tc.code != "auth.no_linked_user" {
					continue // Origin and body rules are the POST's own
				}
				before := h.agentCalls()
				rec := h.do(q)
				if rec.Code != tc.status || tc.code != "" && !strings.Contains(rec.Body.String(), `"`+tc.code+`"`) {
					t.Errorf("%s: %d %s; want %d %s", q.method, rec.Code, rec.Body, tc.status, tc.code)
				}
				if got := h.agentCalls() - before; got != tc.calls {
					t.Errorf("%s: %d agent calls; want %d", q.method, got, tc.calls)
				}
				if cookieNamed(rec, SessionCookiePlain) != nil || cookieNamed(rec, SessionCookieSecure) != nil || cookieNamed(rec, signInStepCookie) != nil {
					t.Errorf("%s: a session or step cookie was set", q.method)
				}
			}
			if h.sessions() != 0 || h.tailnetUserID(h.user.ID) != "" {
				t.Errorf("a refusal opened a session or pinned the tailnet user")
			}
		})
	}
}

// managed records addr and stableID as a Darkpanel host's tailnet node.
func (h *tsHarness) managed(addr, stableID string) {
	h.t.Helper()
	h.exec(`INSERT INTO core_hosts (id, name, created_at, state) VALUES ('H9', 'host-H9', '2026-10-19T00:00:00.000000000Z', ?)`, core.HostEnrolled)
	h.exec(`INSERT INTO core_host_tailnet (host_id, addr, stable_id, seen_at) VALUES ('H9', ?, ?, '2026-11-24T00:00:00Z')`, addr, stableID)
}

func TestWhoisThrottle(t *testing.T) {
	h := newTSHarness(t, false)
	// A match is cached for 10 s.
	for range 2 {
		if rec := h.do(get(tsPeer)); rec.Code != http.StatusOK {
			t.Fatalf("GET: %d %s", rec.Code, rec.Body)
		}
	}
	if h.agentCalls() != 1 {
		t.Fatalf("two GETs made %d agent calls; want 1", h.agentCalls())
	}
	h.clock.Advance(10 * time.Second)
	if h.do(get(tsPeer)); h.agentCalls() != 2 {
		t.Fatalf("a GET after 10 s made no new call")
	}

	// Anything else is cached for 60 s.
	other := "100.101.0.7"
	h.answer(other, tsAnswer(func(w *auth.TailnetWhois) {
		w.LoginName, w.StableID, w.Addresses = "carol@example.com", "nC", []string{other}
	}))
	if rec := h.do(get(other)); rec.Code != http.StatusForbidden {
		t.Fatalf("GET of an unlinked node: %d %s", rec.Code, rec.Body)
	}
	h.clock.Advance(59 * time.Second)
	if rec := h.do(get(other)); rec.Code != http.StatusForbidden || h.agentCalls() != 3 {
		t.Fatalf("a cached refusal: %d, %d calls; want 403 and 3 calls", rec.Code, h.agentCalls())
	}
	h.clock.Advance(time.Second)
	if h.do(get(other)); h.agentCalls() != 4 {
		t.Fatalf("a GET after 60 s made no new call")
	}

	// The POST passes the cache and spends the budget: six lookups a
	// minute per IP, then 429 with no agent call.
	h.clock.Advance(time.Minute)
	for i := range 6 {
		if rec := h.do(post(tsPeer)); rec.Code != http.StatusOK {
			t.Fatalf("POST %d: %d %s", i+1, rec.Code, rec.Body)
		}
	}
	calls := h.agentCalls()
	if rec := h.do(post(tsPeer)); rec.Code != http.StatusTooManyRequests || h.agentCalls() != calls {
		t.Fatalf("seventh POST in a minute: %d, %d new calls; want 429 and none", rec.Code, h.agentCalls()-calls)
	}
	if rec := h.do(get("100.101.0.8")); rec.Code == http.StatusTooManyRequests {
		t.Fatal("another IP was throttled")
	}
	h.clock.Advance(time.Minute)
	if rec := h.do(post(tsPeer)); rec.Code != http.StatusOK {
		t.Fatalf("POST a minute later: %d %s", rec.Code, rec.Body)
	}

	entries, _, err := h.audit.List(context.Background(), core.AuditQuery{Limit: 1000})
	if err != nil {
		t.Fatal(err)
	}
	var tailscale int
	for _, e := range entries {
		if strings.Contains(string(e.Input), `"method":"tailscale"`) {
			tailscale++
		}
	}
	// GETs and the 429 write none.
	if tailscale != 7 {
		t.Errorf("%d audit entries with method tailscale; want one per sign-in (7)", tailscale)
	}
}

// codeStep is the code-step POST from tsPeer over TLS on the Tailscale
// listener, the only place a Tailscale ticket's code is accepted from.
func (h *tsHarness) codeStep(step *http.Cookie, code string) *http.Request {
	r := request(http.MethodPost, tsHost+totpStepPath, `{"code":"`+code+`"}`)
	r.Header.Add("Cookie", step.Name+"="+step.Value)
	r.RemoteAddr = net.JoinHostPort(tsPeer, "41000")
	r.TLS = &tls.ConnectionState{}
	return r.WithContext(context.WithValue(r.Context(), http.LocalAddrContextKey, &net.TCPAddr{IP: tsListener.AsSlice(), Port: 443}))
}

func TestTailscaleRequireTotp(t *testing.T) {
	h := newTSHarness(t, true)
	rec := h.do(post(tsPeer))
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"totp":true}` || h.sessions() != 0 {
		t.Fatalf("POST with the code step: %d %s; want {\"totp\":true} and no session", rec.Code, rec.Body)
	}
	step := cookieNamed(rec, signInStepCookie)
	if step == nil || step.Path != totpStepPath || step.SameSite != http.SameSiteStrictMode {
		t.Fatalf("code step cookie %+v", step)
	}
	if h.tailnetUserID(h.user.ID) != "" {
		t.Fatal("the tailnet user was pinned before the code")
	}
	r := h.codeStep(step, h.code())
	if rec = h.serve(r); rec.Code != http.StatusOK || h.sessions() != 1 {
		t.Fatalf("code step: %d %s; want a session", rec.Code, rec.Body)
	}
	if got := h.tailnetUserID(h.user.ID); got != "4242" {
		t.Errorf("tailnet_user_id = %q after the code; want 4242", got)
	}

	// A ticket is refused once the method is off.
	h.clock.Advance(time.Minute)
	rec = h.do(post(tsPeer))
	if step = cookieNamed(rec, signInStepCookie); rec.Code != http.StatusOK || step == nil {
		t.Fatalf("second POST: %d %s", rec.Code, rec.Body)
	}
	h.exec(`UPDATE auth_settings SET tailscale_enabled = 0`)
	r = h.codeStep(step, h.code())
	if rec = h.serve(r); rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"auth.method_off"`) || h.sessions() != 1 {
		t.Errorf("code step with the method off: %d %s; want auth.method_off and no session", rec.Code, rec.Body)
	}

	// With the code step on, an invited user (no code yet) is refused.
	h.exec(`UPDATE auth_settings SET tailscale_enabled = 1`)
	if _, err := h.auth.Invite(context.Background(), auth.InviteRequest{Username: "bob", Role: auth.RoleMember,
		TailnetLogin: "bob@example.com"}); err != nil {
		t.Fatal(err)
	}
	bob := "100.101.0.6"
	h.answer(bob, tsAnswer(func(w *auth.TailnetWhois) {
		w.LoginName, w.UserID, w.StableID, w.Addresses = "bob@example.com", 77, "nBob", []string{bob}
	}))
	if rec = h.do(post(bob)); rec.Code != http.StatusUnauthorized || cookieNamed(rec, signInStepCookie) != nil {
		t.Errorf("invited user with the code step on: %d %s; want 401 and no ticket", rec.Code, rec.Body)
	}
}

// TestTailscaleWhoisRefusalAudited checks that a Continue-as POST refused
// at the whois is audited with method tailscale and the request's source,
// and that a POST refused by the lookup budget is not.
func TestTailscaleWhoisRefusalAudited(t *testing.T) {
	h := newTSHarness(t, false)
	audited := func() []core.AuditEntry {
		t.Helper()
		entries, _, err := h.audit.List(context.Background(), core.AuditQuery{Limit: 1000})
		if err != nil {
			t.Fatal(err)
		}
		var out []core.AuditEntry
		for _, e := range entries {
			if strings.Contains(string(e.Input), `"method":"tailscale"`) {
				out = append(out, e)
			}
		}
		return out
	}
	for i, a := range []any{errors.New("tailscaled is down"), "not json"} {
		h.answer(tsPeer, a)
		if rec := h.do(post(tsPeer)); rec.Code != http.StatusUnauthorized {
			t.Fatalf("POST %d: %d %s", i, rec.Code, rec.Body)
		}
		got := audited()
		if len(got) != i+1 {
			t.Fatalf("after POST %d: %d tailscale audit rows; want %d", i, len(got), i+1)
		}
		e := got[len(got)-1]
		for _, g := range got {
			if g.ID > e.ID {
				e = g
			}
		}
		if e.Source != tsPeer || e.ErrorCode != "auth.invalid_credentials" {
			t.Errorf("POST %d audit: source %q, code %q; want %s, auth.invalid_credentials", i, e.Source, e.ErrorCode, tsPeer)
		}
	}
	for range 4 {
		h.do(post(tsPeer))
	}
	before := len(audited())
	if rec := h.do(post(tsPeer)); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("seventh POST: %d %s; want 429", rec.Code, rec.Body)
	}
	if n := len(audited()); n != before {
		t.Errorf("a 429 added %d audit rows; want none", n-before)
	}
}
