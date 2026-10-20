//go:build harness

package harness

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
)

func TestActionPath(t *testing.T) {
	cases := []struct {
		host, action, op, want string
	}{
		{"", "core.host.create/v1", "plan", "/api/v1/actions/core.host.create/v1/plan"},
		{"", "core.host.list/v1", "read", "/api/v1/actions/core.host.list/v1/read"},
		{"01HZY", "core.update.status/v2", "read", "/api/v1/hosts/01HZY/actions/core.update.status/v2/read"},
		{"a b", "core.update.apply/v1", "apply", "/api/v1/hosts/a%20b/actions/core.update.apply/v1/apply"},
	}
	for _, c := range cases {
		got, err := actionPath(c.host, c.action, c.op)
		if err != nil || got != c.want {
			t.Errorf("actionPath(%q, %q, %q) = %q, %v; want %q", c.host, c.action, c.op, got, err, c.want)
		}
	}
	for _, bad := range []string{"core.host.create", "core.host.create/", "/v1", "core.host.create/1"} {
		if got, err := actionPath("", bad, "plan"); err == nil {
			t.Errorf("actionPath of %q = %q, want an error", bad, got)
		}
	}
	if got, err := resourcePath("01HZY", "hostinfo.summary"); err != nil || got != "/api/v1/hosts/01HZY/resources/hostinfo/summary" {
		t.Errorf("resourcePath = %q, %v", got, err)
	}
	if got, err := resourcePath("01HZY", "summary"); err == nil {
		t.Errorf("resourcePath of a name without a module = %q, want an error", got)
	}
}

// fakeAPI is a controller API with one user: it checks the session cookie
// and, on every POST but the login, the CSRF token.
func fakeAPI(t *testing.T) *httptest.Server {
	t.Helper()
	const session, csrf = "s3ss10n", "csrf-t0ken"
	type planBody struct {
		Input json.RawMessage `json:"input"`
	}
	type applyBody struct {
		PlanID       string `json:"planId"`
		Confirmation string `json:"confirmation"`
	}
	problem := func(w http.ResponseWriter, status int, code, detail string) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{"type": "about:blank", "title": "x", "status": status, "detail": detail, "code": code})
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/auth/login", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Username, Password, Code string }
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Username != "admin" || in.Password != "pw" || in.Code != "123456" {
			problem(w, http.StatusUnauthorized, "auth.invalid_credentials", "invalid username, password or code")
			return
		}
		//nolint:gosec // G124: a cookie of plain HTTP cannot be Secure, as the controller's loopback one cannot
		http.SetCookie(w, &http.Cookie{Name: "darkpanel_session", Value: session, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
		_ = json.NewEncoder(w).Encode(map[string]any{"csrfToken": csrf, "user": map[string]any{"username": "admin"}})
	})
	authed := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if c, err := r.Cookie("darkpanel_session"); err != nil || c.Value != session {
				problem(w, http.StatusUnauthorized, "auth.unauthenticated", "no session")
				return
			}
			if r.Method == http.MethodPost && r.Header.Get("X-CSRF-Token") != csrf {
				problem(w, http.StatusForbidden, "auth.csrf", "missing or invalid X-CSRF-Token header")
				return
			}
			h(w, r)
		}
	}
	mux.HandleFunc("POST /api/v1/actions/core.host.revoke/v1/plan", authed(func(w http.ResponseWriter, r *http.Request) {
		var in planBody
		_ = json.NewDecoder(r.Body).Decode(&in)
		if string(in.Input) != `{"hostId":"h1"}` {
			problem(w, http.StatusBadRequest, "request.invalid_input", "input "+string(in.Input))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"planId": "p1", "confirmation": "agent"})
	}))
	mux.HandleFunc("POST /api/v1/actions/core.host.revoke/v1/apply", authed(func(w http.ResponseWriter, r *http.Request) {
		var in applyBody
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in.PlanID != "p1" || in.Confirmation != "agent" {
			problem(w, http.StatusBadRequest, "plan.confirmation_mismatch", "plan "+in.PlanID+" confirmed with "+in.Confirmation)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "c1", "state": "completed", "output": map[string]any{"id": "h1", "state": "revoked"}})
	}))
	mux.HandleFunc("POST /api/v1/actions/core.host.create/v1/plan", authed(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"planId": "p2", "confirmation": nil})
	}))
	mux.HandleFunc("POST /api/v1/actions/core.host.create/v1/apply", authed(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "c2", "state": "failed", "output": nil,
			"error": map[string]any{"code": "host.name_taken", "message": "a host named agent exists"}})
	}))
	mux.HandleFunc("POST /api/v1/actions/core.host.list/v1/read", authed(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"output": map[string]any{"hosts": []map[string]any{
			{"id": "h0", "name": "controller", "isLocal": true, "state": "enrolled", "online": true, "agentVersion": "0.2.0"},
			{"id": "h1", "name": "agent", "isLocal": false, "state": "enrolled", "online": false, "agentVersion": nil},
		}}})
	}))
	mux.HandleFunc("POST /api/v1/hosts/h1/actions/core.update.status/v1/read", authed(func(w http.ResponseWriter, _ *http.Request) {
		problem(w, http.StatusServiceUnavailable, "host.offline", "host h1 is offline")
	}))
	mux.HandleFunc("GET /api/v1/hosts/h0/resources/hostinfo/summary", authed(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"hostname":"controller"}`)
	}))
	mux.HandleFunc("GET /api/v1/hosts/h2/resources/hostinfo/summary", authed(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, "<html>bad gateway</html>")
	}))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestAPI(t *testing.T) {
	srv := fakeAPI(t)
	var d net.Dialer
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	a := NewAPI(srv.URL, d.DialContext)

	var apiErr *APIError
	if _, err := a.Hosts(ctx); !errors.As(err, &apiErr) || apiErr.Status != http.StatusUnauthorized || apiErr.Code != "auth.unauthenticated" {
		t.Errorf("Hosts before the login = %v, want the API's auth.unauthenticated", err)
	}
	if err := a.Login(ctx, "admin", "wrong", "123456"); !errors.As(err, &apiErr) || apiErr.Code != "auth.invalid_credentials" {
		t.Errorf("Login with a wrong password = %v, want auth.invalid_credentials", err)
	}
	if err := a.Login(ctx, "admin", "pw", "123456"); err != nil {
		t.Fatalf("Login: %v", err)
	}

	hosts, err := a.Hosts(ctx)
	if err != nil || len(hosts) != 2 || !hosts[0].IsLocal || hosts[0].AgentVersion == nil || *hosts[0].AgentVersion != "0.2.0" ||
		hosts[1].Name != "agent" || hosts[1].Online || hosts[1].AgentVersion != nil {
		t.Errorf("Hosts = %+v, %v", hosts, err)
	}
	if h, ok := findHost(hosts, "h1"); !ok || h.Name != "agent" {
		t.Errorf("findHost(h1) = %+v, %v", h, ok)
	}
	if _, ok := findHost(hosts, "h9"); ok {
		t.Error("findHost found a host that is not listed")
	}
	if h, ok := localHost(hosts); !ok || h.ID != "h0" {
		t.Errorf("localHost = %+v, %v; want h0", h, ok)
	}

	var out core.Host
	ch, err := a.Apply(ctx, "", "core.host.revoke/v1", core.HostIDInput{HostID: "h1"}, &out)
	if err != nil || ch.ID != "c1" || out.State != "revoked" {
		t.Errorf("Apply of an irreversible action = %+v, %+v, %v; want the change with the plan's confirmation applied", ch, out, err)
	}
	if ch, err := a.Apply(ctx, "", "core.host.create/v1", core.HostCreateInput{Name: "agent"}, nil); err == nil ||
		!strings.Contains(err.Error(), "host.name_taken") || ch.State != core.StateFailed {
		t.Errorf("Apply of a change that failed = %+v, %v; want an error naming its code", ch, err)
	}

	err = a.Read(ctx, "h1", "core.update.status/v1", core.UpdateStatusInput{}, nil)
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusServiceUnavailable || apiErr.Code != "host.offline" || apiErr.Detail != "host h1 is offline" {
		t.Errorf("Read on an offline host = %v, want host.offline with its detail", err)
	}

	var sum struct {
		Hostname string `json:"hostname"`
	}
	if err := a.Resource(ctx, "h0", "hostinfo.summary", &sum); err != nil || sum.Hostname != "controller" {
		t.Errorf("Resource = %+v, %v", sum, err)
	}
	if err := a.Resource(ctx, "h2", "hostinfo.summary", &sum); !errors.As(err, &apiErr) || apiErr.Status != http.StatusBadGateway ||
		apiErr.Code != "" || !strings.Contains(apiErr.Detail, "bad gateway") {
		t.Errorf("Resource answered without problem details = %v, want an APIError quoting the body", err)
	}
}
