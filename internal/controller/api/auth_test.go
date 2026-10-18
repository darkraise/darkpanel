package api

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

func loginBody(username, password, code string) string {
	b, _ := json.Marshal(map[string]string{"username": username, "password": password, "code": code})
	return string(b)
}

// sessionCookieOf returns the session cookie rec set.
func sessionCookieOf(t *testing.T, rec interface{ Result() *http.Response }) *http.Cookie {
	t.Helper()
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("Set-Cookie: %v, want one cookie", cookies)
	}
	return cookies[0]
}

// tokenSecret is the secret part of a dpt_<id>_<secret> token.
func tokenSecret(token string) string {
	return token[strings.LastIndex(token, "_")+1:]
}

type sessionBody struct {
	User      *auth.User      `json:"user"`
	CSRFToken *string         `json:"csrfToken"`
	ExpiresAt *string         `json:"expiresAt"`
	Token     *auth.TokenInfo `json:"token"`
}

func TestLogin(t *testing.T) {
	for _, secure := range []bool{false, true} {
		h := newHarness(t)
		r := request("POST", "/api/v1/auth/login", loginBody("admin", testPassword, h.code()))
		r.Header.Set("User-Agent", "test-agent")
		if secure {
			r.TLS = &tls.ConnectionState{}
		}
		rec := h.serve(r)
		if rec.Code != 200 {
			t.Fatalf("secure %v: status %d %s", secure, rec.Code, rec.Body)
		}
		ck := sessionCookieOf(t, rec)
		wantName := SessionCookiePlain
		if secure {
			wantName = SessionCookieSecure
		}
		if ck.Name != wantName || ck.Secure != secure || !ck.HttpOnly || ck.SameSite != http.SameSiteStrictMode ||
			ck.Path != "/" || ck.Domain != "" || ck.MaxAge != 0 || !ck.Expires.IsZero() || len(ck.Value) != 43 {
			t.Errorf("secure %v: cookie %+v", secure, ck)
		}
		var body sessionBody
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.User == nil || body.User.Username != "admin" || body.CSRFToken == nil ||
			*body.CSRFToken != h.auth.CSRFToken(ck.Value) || body.ExpiresAt == nil || body.Token != nil {
			t.Errorf("secure %v: body %s", secure, rec.Body)
		}
		if !strings.Contains(rec.Body.String(), `"token":null`) {
			t.Errorf("secure %v: body lacks token null: %s", secure, rec.Body)
		}
		entries, _, err := h.audit.List(context.Background(), core.AuditQuery{Limit: 10})
		if err != nil || len(entries) != 1 || entries[0].Action != auth.AuditLoginAction || entries[0].Result != "ok" ||
			entries[0].Source != "192.0.2.10" {
			t.Errorf("secure %v: audit %+v, %v", secure, entries, err)
		}
		noCredentials(t, h, []*httptest.ResponseRecorder{rec}, map[string]string{"password": testPassword, "session id": ck.Value})
	}
}

func TestLoginRefusals(t *testing.T) {
	h := newHarness(t)
	wrong := loginBody("admin", testPassword, "000000")
	var recs []*httptest.ResponseRecorder
	for _, tc := range []struct {
		name        string
		r           *http.Request
		status      int
		code        errcode.Code
		contentType string
	}{
		{"wrong code", request("POST", "/api/v1/auth/login", wrong), 401, errcode.InvalidCredentials, ""},
		{"unknown user", request("POST", "/api/v1/auth/login", loginBody("nobody", testPassword, "123456")), 401, errcode.InvalidCredentials, ""},
		{"text body", request("POST", "/api/v1/auth/login", wrong), 415, errcode.UnsupportedMediaType, "text/plain"},
		{"unknown field", request("POST", "/api/v1/auth/login", `{"username":"admin","password":"`+testPassword+`","code":"1","remember":true}`),
			400, errcode.InvalidJSON, ""},
	} {
		if tc.contentType != "" {
			tc.r.Header.Set("Content-Type", tc.contentType)
		}
		rec := h.serve(tc.r)
		recs = append(recs, rec)
		decodeProblem(t, rec, tc.status, tc.code)
		if strings.Contains(rec.Body.String(), testPassword) || len(rec.Result().Cookies()) != 0 {
			t.Errorf("%s: response leaks the password or sets a cookie: %v %s", tc.name, rec.Result().Cookies(), rec.Body)
		}
	}
	noCredentials(t, h, recs, map[string]string{"password": testPassword})
}

func TestLoginRateLimited(t *testing.T) {
	h := newHarness(t)
	var recs []*httptest.ResponseRecorder
	for range auth.UsernameFailureLimit {
		rec := h.serve(request("POST", "/api/v1/auth/login", loginBody("admin", "wrong password!", "000000")))
		recs = append(recs, rec)
		decodeProblem(t, rec, 401, errcode.InvalidCredentials)
	}
	rec := h.serve(request("POST", "/api/v1/auth/login", loginBody("admin", testPassword, h.code())))
	recs = append(recs, rec)
	decodeProblem(t, rec, 429, errcode.RateLimited)
	if n, err := strconv.Atoi(rec.Header().Get("Retry-After")); err != nil || n < 1 || n > int(auth.LockDuration.Seconds()) {
		t.Errorf("Retry-After = %q", rec.Header().Get("Retry-After"))
	}
	noCredentials(t, h, recs, map[string]string{"password": testPassword, "wrong password": "wrong password!"})
}

func TestLogout(t *testing.T) {
	h := newHarness(t)
	session, csrf := h.login()
	var recs []*httptest.ResponseRecorder

	rec := h.serve(withSession(request("POST", "/api/v1/auth/logout", ""), session, ""))
	recs = append(recs, rec)
	decodeProblem(t, rec, 403, errcode.CSRF)
	// The session route returns the CSRF token, so this response is scanned
	// without it.
	sessionRec := h.serve(withSession(request("GET", "/api/v1/auth/session", ""), session, ""))
	if sessionRec.Code != 200 {
		t.Fatalf("a refused logout ended the session: %d", sessionRec.Code)
	}
	noCredentials(t, h, []*httptest.ResponseRecorder{sessionRec}, map[string]string{"password": testPassword, "session id": session})

	rec = h.serve(withSession(request("POST", "/api/v1/auth/logout", ""), session, csrf))
	recs = append(recs, rec)
	if rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
		t.Fatalf("logout: %d %s", rec.Code, rec.Body)
	}
	ck := sessionCookieOf(t, rec)
	if ck.Name != SessionCookiePlain || ck.Value != "" || ck.MaxAge != -1 || !ck.HttpOnly || ck.SameSite != http.SameSiteStrictMode ||
		ck.Path != "/" || ck.Domain != "" || ck.Secure || !ck.Expires.IsZero() {
		t.Errorf("cleared cookie %+v", ck)
	}
	rec = h.serve(withSession(request("GET", "/api/v1/auth/session", ""), session, ""))
	recs = append(recs, rec)
	decodeProblem(t, rec, 401, errcode.AuthRequired)
	entries, _, err := h.audit.List(context.Background(), core.AuditQuery{Limit: 10})
	if err != nil || len(entries) != 2 || entries[0].Action != auth.AuditLogoutAction || entries[0].Actor != "user:"+h.user.ID {
		t.Errorf("audit %+v, %v", entries, err)
	}

	token := h.token(core.ScopeRead)
	r := withToken(request("POST", "/api/v1/auth/logout", ""), token)
	r.TLS = &tls.ConnectionState{}
	rec = h.serve(r)
	recs = append(recs, rec)
	if rec.Code != http.StatusNoContent {
		t.Errorf("token logout: %d %s", rec.Code, rec.Body)
	}
	ck = sessionCookieOf(t, rec)
	if ck.Name != SessionCookieSecure || ck.Value != "" || ck.MaxAge != -1 || !ck.Secure || !ck.HttpOnly ||
		ck.SameSite != http.SameSiteStrictMode || ck.Path != "/" || ck.Domain != "" || !ck.Expires.IsZero() {
		t.Errorf("cleared HTTPS cookie %+v", ck)
	}
	rec = h.serve(withToken(request("GET", "/api/v1/auth/session", ""), token))
	recs = append(recs, rec)
	if rec.Code != 200 {
		t.Errorf("logout ended a token: %d", rec.Code)
	}
	noCredentials(t, h, recs, map[string]string{"password": testPassword, "session id": session, "CSRF token": csrf,
		"token secret": tokenSecret(token)})
}

func TestSessionRoute(t *testing.T) {
	h := newHarness(t)
	session, csrf := h.login()
	rec := h.serve(withSession(request("GET", "/api/v1/auth/session", ""), session, ""))
	recs := []*httptest.ResponseRecorder{rec}
	var body sessionBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || rec.Code != 200 {
		t.Fatalf("%d %s %v", rec.Code, rec.Body, err)
	}
	if body.User == nil || body.User.ID != h.user.ID || body.CSRFToken == nil || *body.CSRFToken != csrf ||
		body.ExpiresAt == nil || body.Token != nil {
		t.Errorf("session body %s", rec.Body)
	}

	token := h.token(core.ScopeOperate)
	rec = h.serve(withToken(request("GET", "/api/v1/auth/session", ""), token))
	recs = append(recs, rec)
	body = sessionBody{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || rec.Code != 200 {
		t.Fatalf("%d %s %v", rec.Code, rec.Body, err)
	}
	if body.Token == nil || body.Token.Scope != core.ScopeOperate || !strings.HasPrefix(token, "dpt_"+body.Token.ID+"_") ||
		body.CSRFToken != nil || body.User == nil || body.User.ID != h.user.ID || body.ExpiresAt == nil {
		t.Errorf("token body %s", rec.Body)
	}
	if strings.Contains(rec.Body.String(), strings.TrimPrefix(token, "dpt_"+body.Token.ID+"_")) {
		t.Error("the session route echoed the token secret")
	}

	rec = h.serve(request("GET", "/api/v1/auth/session", ""))
	recs = append(recs, rec)
	decodeProblem(t, rec, 401, errcode.AuthRequired)
	noCredentials(t, h, recs, map[string]string{"session id": session, "token secret": tokenSecret(token)})
}

func TestAuthRouteMethods(t *testing.T) {
	h := newHarness(t)
	token := h.token(core.ScopeRead)
	rec := h.serve(request("GET", "/api/v1/auth/login", ""))
	recs := []*httptest.ResponseRecorder{rec}
	decodeProblem(t, rec, 401, errcode.AuthRequired)
	rec = h.serve(withToken(request("GET", "/api/v1/auth/login", ""), token))
	recs = append(recs, rec)
	decodeProblem(t, rec, 405, errcode.MethodNotAllowed)
	if got := rec.Header().Get("Allow"); got != "POST" {
		t.Errorf("Allow = %q, want POST", got)
	}
	rec = h.serve(withToken(request("DELETE", "/api/v1/auth/session", ""), token))
	recs = append(recs, rec)
	decodeProblem(t, rec, 405, errcode.MethodNotAllowed)
	if got := rec.Header().Get("Allow"); got != "GET, HEAD" {
		t.Errorf("Allow = %q, want GET, HEAD", got)
	}
	noCredentials(t, h, recs, map[string]string{"password": testPassword, "token secret": tokenSecret(token)})
}
