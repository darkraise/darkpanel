package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
)

// qaStepCookie is the code-step cookie a Continue-as answer set.
func qaStepCookie(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == signInStepCookie && c.Value != "" {
			return c.Value
		}
	}
	t.Fatalf("Continue as = %d %s, want a code-step cookie", rec.Code, rec.Body)
	return ""
}

// qaCodeStep posts code with the step cookie on the listener at local from remote.
// A proxied request comes through an exposure, whose public host its Origin names.
func (q *qaTS) qaCodeStep(cookie, code, local, remote string, proxied bool) *httptest.ResponseRecorder {
	r := q.req("POST", local, remote, `{"code":"`+code+`"}`, false)
	r.URL.Path, r.RequestURI = totpStepPath, totpStepPath
	if proxied {
		r = r.WithContext(core.WithProxied(r.Context(), core.Proxied{ListenerID: "L1", Client: netip.MustParseAddr(remote),
			Transport: netip.MustParseAddrPort("127.0.0.1:9000"), PublicHost: "panel.example"}))
		r.Header.Set("Origin", "https://panel.example")
	}
	r.AddCookie(&http.Cookie{Name: signInStepCookie, Value: cookie})
	return q.serve(r)
}

func TestRemembered_A30_CodeStepBoundToNode(t *testing.T) {
	q := qaNewTS(t)
	qaExec(t, q.harness, `UPDATE core_users SET totp_last_step = 1 WHERE username = 'admin'`)
	cookie := qaStepCookie(t, q.serve(q.req("POST", qaTSController, qaTSNode, "{}", false)))
	for _, tc := range []struct {
		name          string
		local, remote string
		proxied       bool
	}{
		{"from another tailnet address", qaTSController, "100.64.0.10", false},
		{"from the ticket's address on another listener", "192.0.2.50", qaTSNode, false},
		{"from the ticket's address, proxied", qaTSController, qaTSNode, true},
	} {
		rec := q.qaCodeStep(cookie, q.code(), tc.local, tc.remote, tc.proxied)
		if rec.Code != 401 || !strings.Contains(rec.Body.String(), `"auth.invalid_credentials"`) {
			t.Errorf("%s: %d %s, want 401 auth.invalid_credentials", tc.name, rec.Code, rec.Body)
		}
		if n := qaCount(t, q.harness, `SELECT count(*) FROM core_sessions`); n != 0 {
			t.Errorf("%s: %d sessions, want none", tc.name, n)
		}
	}
	if rec := q.qaCodeStep(cookie, q.code(), qaTSController, qaTSNode, false); rec.Code != 200 ||
		qaCount(t, q.harness, `SELECT count(*) FROM core_sessions`) != 1 {
		t.Errorf("the code step from the ticket's node = %d %s, want a session", rec.Code, rec.Body)
	}
}

// qaFields decodes rec's JSON object, failing unless the answer is 200.
func qaFields(t *testing.T, what string, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil {
		t.Fatalf("%s = %d %s, want a 200 JSON object", what, rec.Code, rec.Body)
	}
	return out
}

func TestRemembered_A16_AnswerFields(t *testing.T) {
	q := qaNewTS(t)
	qaExec(t, q.harness, `UPDATE core_users SET totp_last_step = 1 WHERE username = 'admin'`)
	match := qaFields(t, "GET tailscale", q.serve(q.req("GET", qaTSController, qaTSNode, "", false)))
	if match["remembered"] != false || match["codeReady"] != true {
		t.Errorf("GET tailscale = %v, want remembered false and codeReady true", match)
	}
	rec := q.serve(q.req("POST", qaTSController, qaTSNode, "{}", false))
	cookie := qaStepCookie(t, rec)
	if out := qaFields(t, "Continue as", rec); len(out) != 2 || out["totp"] != true || out["remember"] != true {
		t.Errorf("Continue as = %v, want {totp: true, remember: true}", out)
	}
	if rec := q.qaCodeStep(cookie, q.code(), qaTSController, qaTSNode, false); rec.Code != 200 {
		t.Fatalf("the code step = %d %s, want a session", rec.Code, rec.Body)
	}
	// The match is cached for 10 s; past it the node is remembered.
	q.clock.Advance(whoisMatchTTL + time.Second)
	match = qaFields(t, "GET tailscale after the code", q.serve(q.req("GET", qaTSController, qaTSNode, "", false)))
	if match["remembered"] != true || match["codeReady"] != true {
		t.Errorf("GET tailscale after the code = %v, want remembered and codeReady", match)
	}
}
