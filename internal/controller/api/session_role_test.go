package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func TestSessionCarriesRole(t *testing.T) {
	h := accHarness(t)
	_, vstar := accMember(t, h, "vstar", "*=viewer")
	_, opg := accMember(t, h, "opg", "G=operator")
	cases := []struct{ name, secret, role, allHosts string }{
		{"admin", h.secret, "admin", "admin"},
		{"vstar", vstar, "member", "read"},
		{"opg", opg, "member", ""},
	}
	check := func(what string, code int, body []byte, role, allHosts string) {
		t.Helper()
		var resp struct {
			User *struct {
				Role     *string `json:"role"`
				AllHosts *string `json:"allHosts"`
			} `json:"user"`
		}
		if code != http.StatusOK || json.Unmarshal(body, &resp) != nil || resp.User == nil ||
			resp.User.Role == nil || resp.User.AllHosts == nil {
			t.Fatalf("%s: %d %s, want 200 with user.role and user.allHosts", what, code, body)
		}
		if *resp.User.Role != role || *resp.User.AllHosts != allHosts {
			t.Errorf("%s: role %q, allHosts %q; want %q, %q", what, *resp.User.Role, *resp.User.AllHosts, role, allHosts)
		}
	}
	for _, c := range cases {
		body := fmt.Sprintf(`{"username": %q, "password": %q, "code": %q}`, c.name, testPassword, accCode(t, h, c.secret))
		rec := h.serve(request("POST", "/api/v1/auth/login", body))
		check(c.name+" login", rec.Code, rec.Body.Bytes(), c.role, c.allHosts)
		rec = h.serve(accSession(t, h, c.name, c.secret)(request("GET", "/api/v1/auth/session", "")))
		check(c.name+" session", rec.Code, rec.Body.Bytes(), c.role, c.allHosts)
	}
}
