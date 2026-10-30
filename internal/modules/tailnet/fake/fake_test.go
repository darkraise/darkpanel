package fake

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/controller/cloud/cloudfake"
)

// fkResult is one answer of the server.
type fkResult struct {
	Status int
	Header http.Header
	Body   string
}

// fkReq sends one request; token "" sends no Authorization header.
func fkReq(t *testing.T, s *Server, method, path, token string, header map[string]string, body string) fkResult {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, s.URL()+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	res, err := s.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return fkResult{res.StatusCode, res.Header, string(b)}
}

// fkToken obtains an access token the way the module's client does.
func fkToken(t *testing.T, s *Server) string {
	t.Helper()
	r := fkTokenRequest(t, s, url.Values{"client_id": {ClientID}, "client_secret": {ClientSecret}, "grant_type": {"client_credentials"}})
	if r.Status != http.StatusOK {
		t.Fatalf("token request answered %d: %s", r.Status, r.Body)
	}
	return fkDecode(t, r.Body)["access_token"].(string)
}

func fkTokenRequest(t *testing.T, s *Server, form url.Values) fkResult {
	t.Helper()
	return fkReq(t, s, http.MethodPost, "/api/v2/oauth/token", "", map[string]string{"Content-Type": "application/x-www-form-urlencoded"}, form.Encode())
}

func fkDecode(t *testing.T, body string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("body %q is not a JSON object: %v", body, err)
	}
	return m
}

func TestFkOAuthToken(t *testing.T) {
	s := New(t)
	for name, form := range map[string]url.Values{
		"wrong secret": {"client_id": {ClientID}, "client_secret": {"nope"}, "grant_type": {"client_credentials"}},
		"wrong client": {"client_id": {"other"}, "client_secret": {ClientSecret}, "grant_type": {"client_credentials"}},
	} {
		if r := fkTokenRequest(t, s, form); r.Status != http.StatusUnauthorized {
			t.Errorf("%s: status %d, want 401", name, r.Status)
		}
	}
	if r := fkTokenRequest(t, s, url.Values{"client_id": {ClientID}, "client_secret": {ClientSecret}, "grant_type": {"password"}}); r.Status != http.StatusBadRequest {
		t.Errorf("wrong grant type: status %d, want 400", r.Status)
	}
	if s.Tokens() != 0 {
		t.Fatalf("Tokens = %d after refused requests, want 0", s.Tokens())
	}
	r := fkTokenRequest(t, s, url.Values{"client_id": {ClientID}, "client_secret": {ClientSecret}, "grant_type": {"client_credentials"}})
	doc := fkDecode(t, r.Body)
	if r.Status != http.StatusOK || doc["token_type"] != "Bearer" || doc["expires_in"] != float64(3600) || doc["access_token"] == "" {
		t.Fatalf("token answer %d %s", r.Status, r.Body)
	}
	if !strings.Contains(doc["scope"].(string), "devices:core") {
		t.Errorf("scope %q does not list the granted scopes", doc["scope"])
	}
	second := fkToken(t, s)
	if second == doc["access_token"] || s.Tokens() != 2 {
		t.Errorf("second token %q, Tokens = %d; want a distinct token and 2", second, s.Tokens())
	}
}

func TestFkAPIRequiresAnIssuedTokenAndTheTailnet(t *testing.T) {
	s := New(t)
	tok := fkToken(t, s)
	for _, tc := range []struct {
		name, path, token string
		want              int
	}{
		{"no token", "/api/v2/tailnet/example.com/devices", "", http.StatusUnauthorized},
		{"unknown token", "/api/v2/tailnet/example.com/devices", "tskey-api-made-up", http.StatusUnauthorized},
		{"issued token", "/api/v2/tailnet/example.com/devices", tok, http.StatusOK},
		{"default tailnet", "/api/v2/tailnet/-/devices", tok, http.StatusOK},
		{"other tailnet", "/api/v2/tailnet/other.org/devices", tok, http.StatusNotFound},
	} {
		if r := fkReq(t, s, http.MethodGet, tc.path, tc.token, nil, ""); r.Status != tc.want {
			t.Errorf("%s: status %d, want %d (%s)", tc.name, r.Status, tc.want, r.Body)
		}
	}
	if doc := fkDecode(t, fkReq(t, s, http.MethodGet, "/api/v2/tailnet/example.com/devices", "", nil, "").Body); doc["message"] == "" {
		t.Errorf("a refusal carries the provider's message member")
	}
}

func TestFkScopes(t *testing.T) {
	s := New(t)
	tok := fkToken(t, s)
	d := s.AddDevice(Device{Name: "web"})
	policy := `{"acls": []}`
	type call struct{ method, path, body string }
	var (
		listDevices  = call{http.MethodGet, "/api/v2/tailnet/example.com/devices", ""}
		authorize    = call{http.MethodPost, "/api/v2/device/" + d + "/authorized", `{"authorized":true}`}
		getRoutes    = call{http.MethodGet, "/api/v2/device/" + d + "/routes", ""}
		setRoutes    = call{http.MethodPost, "/api/v2/device/" + d + "/routes", `{"routes":[]}`}
		listKeys     = call{http.MethodGet, "/api/v2/tailnet/example.com/keys", ""}
		createKey    = call{http.MethodPost, "/api/v2/tailnet/example.com/keys", `{"capabilities":{"devices":{"create":{"tags":["tag:a"]}}}}`}
		getPolicy    = call{http.MethodGet, "/api/v2/tailnet/example.com/acl", ""}
		setPolicy    = call{http.MethodPost, "/api/v2/tailnet/example.com/acl", policy}
		validatePol  = call{http.MethodPost, "/api/v2/tailnet/example.com/acl/validate", policy}
		allEndpoints = []call{listDevices, authorize, getRoutes, setRoutes, listKeys, createKey, getPolicy, setPolicy, validatePol}
	)
	for _, tc := range []struct {
		scopes  []string
		allowed []call
	}{
		{[]string{"devices:core"}, []call{listDevices, authorize}},
		{[]string{"devices:core:read"}, []call{listDevices}},
		{[]string{"devices:routes"}, []call{getRoutes, setRoutes}},
		{[]string{"auth_keys"}, []call{listKeys, createKey}},
		{[]string{"policy_file:read"}, []call{getPolicy, validatePol}},
		{[]string{"policy_file"}, []call{getPolicy, setPolicy, validatePol}},
		{nil, nil},
	} {
		s.SetScopes(tc.scopes...)
		for _, c := range allEndpoints {
			want := http.StatusForbidden
			for _, a := range tc.allowed {
				if a == c {
					want = http.StatusOK
				}
			}
			if r := fkReq(t, s, c.method, c.path, tok, nil, c.body); r.Status != want {
				t.Errorf("scopes %v: %s %s answered %d, want %d (%s)", tc.scopes, c.method, c.path, r.Status, want, r.Body)
			}
		}
	}
}

func TestFkFaultsComeFromTheSharedScaffolding(t *testing.T) {
	s := New(t)
	tok := fkToken(t, s)
	s.Cloud().Fail(http.MethodGet, "/api/v2/tailnet/example.com/devices", cloudfake.Fault{Status: http.StatusTooManyRequests})
	if r := fkReq(t, s, http.MethodGet, "/api/v2/tailnet/example.com/devices", tok, nil, ""); r.Status != http.StatusTooManyRequests {
		t.Fatalf("faulted request answered %d, want 429", r.Status)
	}
	if r := fkReq(t, s, http.MethodGet, "/api/v2/tailnet/example.com/devices", tok, nil, ""); r.Status != http.StatusOK {
		t.Fatalf("request after the fault answered %d, want 200", r.Status)
	}
	reqs := s.Cloud().Requests()
	last := reqs[len(reqs)-1]
	if last.Method != http.MethodGet || last.Header.Get("Authorization") != "Bearer "+tok {
		t.Errorf("request log last entry %+v does not show the bearer token", last)
	}
}
