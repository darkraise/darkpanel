package fake

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/controller/cloud/cloudfake"
)

// fkResult is one answer of the server.
type fkResult struct {
	Status int
	Body   string
}

// fkReq sends one request with the server's token; an empty token sends none.
func fkReq(t *testing.T, s *Server, method, path, token, body string) fkResult {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, s.URL()+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
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
	return fkResult{res.StatusCode, string(b)}
}

// fkEnvelope is Cloudflare's answer envelope.
type fkEnvelope struct {
	Success bool `json:"success"`
	Errors  []struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
	Messages   []any           `json:"messages"`
	Result     json.RawMessage `json:"result"`
	ResultInfo *struct {
		Count      int `json:"count"`
		TotalCount int `json:"total_count"`
	} `json:"result_info"`
}

func fkDecode(t *testing.T, r fkResult) fkEnvelope {
	t.Helper()
	var e fkEnvelope
	if err := json.Unmarshal([]byte(r.Body), &e); err != nil {
		t.Fatalf("body %q is not an envelope: %v", r.Body, err)
	}
	if e.Errors == nil || e.Messages == nil {
		t.Fatalf("envelope %q lacks errors or messages arrays", r.Body)
	}
	return e
}

func fkObject(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("result %s is not an object: %v", raw, err)
	}
	return m
}

func fkList(t *testing.T, e fkEnvelope) []map[string]any {
	t.Helper()
	var l []map[string]any
	if err := json.Unmarshal(e.Result, &l); err != nil || l == nil {
		t.Fatalf("result %s is not an array: %v", e.Result, err)
	}
	if e.ResultInfo == nil || e.ResultInfo.Count != len(l) || e.ResultInfo.TotalCount != len(l) {
		t.Fatalf("result_info %+v does not describe %d items", e.ResultInfo, len(l))
	}
	return l
}

func fkAccount(suffix string) string { return "/accounts/" + AccountID + suffix }

func TestFkAuthAndPermissions(t *testing.T) {
	s := New(t)
	zone := s.AddZone("example.com")
	tunnels, zones, records := fkAccount("/cfd_tunnel"), "/zones", "/zones/"+zone+"/dns_records"

	if r := fkReq(t, s, http.MethodGet, tunnels, "", ""); r.Status != http.StatusUnauthorized || fkDecode(t, r).Success {
		t.Errorf("no token: %d %s, want 401 with success false", r.Status, r.Body)
	}
	if r := fkReq(t, s, http.MethodGet, tunnels, "wrong", ""); r.Status != http.StatusUnauthorized {
		t.Errorf("wrong token: status %d, want 401", r.Status)
	}
	if r := fkReq(t, s, http.MethodGet, "/accounts/ffffffffffffffffffffffffffffffff/cfd_tunnel", Token, ""); r.Status != http.StatusForbidden {
		t.Errorf("another account: status %d, want 403", r.Status)
	}

	for _, tc := range []struct {
		perms                                 []string
		tunnelsOK, zonesOK, recordsOK, verify bool
	}{
		{[]string{PermTunnelWrite, PermZoneRead, PermDNSWrite}, true, true, true, true},
		{[]string{PermTunnelWrite}, true, false, false, true},
		{[]string{PermZoneRead}, false, true, false, true},
		{[]string{PermDNSWrite}, false, false, true, true},
		{nil, false, false, false, true},
	} {
		s.SetPermissions(tc.perms...)
		for path, want := range map[string]bool{tunnels: tc.tunnelsOK, zones: tc.zonesOK, records: tc.recordsOK, fkAccount("/tokens/verify"): tc.verify} {
			r := fkReq(t, s, http.MethodGet, path, Token, "")
			if (r.Status == http.StatusOK) != want {
				t.Errorf("permissions %v: GET %s answered %d, want ok=%t", tc.perms, path, r.Status, want)
			}
			if !want {
				e := fkDecode(t, r)
				if r.Status != http.StatusForbidden || e.Success || len(e.Errors) != 1 || e.Errors[0].Code != 10000 {
					t.Errorf("permissions %v: GET %s refusal %d %s, want 403 with error code 10000", tc.perms, path, r.Status, r.Body)
				}
			}
		}
	}
}

func TestFkVerifyToken(t *testing.T) {
	s := New(t)
	for _, path := range []string{fkAccount("/tokens/verify"), "/user/tokens/verify"} {
		r := fkReq(t, s, http.MethodGet, path, Token, "")
		e := fkDecode(t, r)
		if r.Status != http.StatusOK || !e.Success || fkObject(t, e.Result)["status"] != "active" {
			t.Errorf("verify %s: %d %s", path, r.Status, r.Body)
		}
	}
}

func TestFkBothBasePathsServeTheAPI(t *testing.T) {
	s := New(t)
	for _, prefix := range []string{"", "/client/v4"} {
		if r := fkReq(t, s, http.MethodGet, prefix+fkAccount("/cfd_tunnel"), Token, ""); r.Status != http.StatusOK {
			t.Errorf("prefix %q: status %d, want 200", prefix, r.Status)
		}
	}
}

func TestFkFaultsComeFromTheSharedScaffolding(t *testing.T) {
	s := New(t)
	path := fkAccount("/cfd_tunnel")
	s.Cloud().Fail(http.MethodPost, path, cloudfake.Fault{LoseResponse: true})
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, s.URL()+path, strings.NewReader(`{"name":"darkpanel-1","config_src":"cloudflare"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+Token)
	if res, err := s.Client().Do(req); err == nil {
		_ = res.Body.Close()
		t.Fatalf("a lost response reached the client")
	}
	if got := s.Tunnels(); len(got) != 1 || got[0].Name != "darkpanel-1" {
		t.Errorf("tunnels after a lost response = %+v, want the write committed", got)
	}
	reqs := s.Cloud().Requests()
	if len(reqs) != 1 || reqs[0].Header.Get("Authorization") != "Bearer "+Token {
		t.Errorf("request log %+v", reqs)
	}
}
