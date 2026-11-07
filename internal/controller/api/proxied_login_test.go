package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// v1r17Login posts the admin's login through the runtime listener of
// pxProxied, as the client at address.
func v1r17Login(h *harness, client, password, code string) *httptest.ResponseRecorder {
	px := pxProxied(nil)
	px.Client = netip.MustParseAddr(client)
	r := request("POST", loginPath, loginBody("admin", password, code))
	return h.serve(r.WithContext(core.WithProxied(r.Context(), px)))
}

func TestV1r17ProxiedLoginLockFollowsTheClientPrefix(t *testing.T) {
	h := newHarness(t)
	for i := range auth.UsernameFailureLimit {
		rec := v1r17Login(h, fmt.Sprintf("2001:db8:1:2::%x", i+1), "wrong password!", "000000")
		decodeProblem(t, rec, 401, errcode.InvalidCredentials)
	}
	decodeProblem(t, v1r17Login(h, "2001:db8:1:2:ffff::9", testPassword, h.code()), 429, errcode.RateLimited)
	if rec := v1r17Login(h, "2001:db8:1:3::1", testPassword, h.code()); rec.Code != http.StatusOK {
		t.Errorf("admin from another /64 got %d %s, want 200", rec.Code, rec.Body)
	}
}
