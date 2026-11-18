package api

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/core"
)

// TestTrustedListener: only a loopback or served Tailscale local address
// counts, never a proxied request or one without a local address (spec A19).
func TestTrustedListener(t *testing.T) {
	h := newHarness(t)
	req := func(local string, proxied bool) *http.Request {
		r := httptest.NewRequest("POST", "/api/v1/auth/login", nil)
		ctx := r.Context()
		if local != "" {
			ctx = context.WithValue(ctx, http.LocalAddrContextKey, net.TCPAddrFromAddrPort(netip.MustParseAddrPort(local)))
		}
		if proxied {
			ctx = core.WithProxied(ctx, core.Proxied{ListenerID: "L1", Client: netip.MustParseAddr("192.0.2.9")})
		}
		return r.WithContext(ctx)
	}
	if h.srv.TrustedListener(req("100.64.0.5:8443", false)) {
		t.Error("a Tailscale address counted before SetTailscaleAddrs")
	}
	h.srv.SetTailscaleAddrs(func() []netip.Addr { return []netip.Addr{netip.MustParseAddr("100.64.0.5")} })
	for _, tt := range []struct {
		name    string
		local   string
		proxied bool
		want    bool
	}{
		{"loopback", "127.0.0.1:8080", false, true},
		{"IPv6 loopback", "[::1]:8080", false, true},
		{"mapped loopback", "[::ffff:127.0.0.1]:8080", false, true},
		{"tailscale", "100.64.0.5:8443", false, true},
		{"public", "203.0.113.7:8080", false, false},
		{"another tailnet address", "100.64.0.6:8443", false, false},
		{"no local address", "", false, false},
		{"proxied", "127.0.0.1:9443", true, false},
	} {
		if got := h.srv.TrustedListener(req(tt.local, tt.proxied)); got != tt.want {
			t.Errorf("%s: TrustedListener = %v, want %v", tt.name, got, tt.want)
		}
	}
	h.srv.SetTailscaleAddrs(nil)
	if h.srv.TrustedListener(req("100.64.0.5:8443", false)) {
		t.Error("a Tailscale address counted after SetTailscaleAddrs(nil)")
	}
}

// recordingAuth passes every call to the harness's auth service and keeps
// the login requests.
type recordingAuth struct {
	Authenticator
	logins []auth.LoginRequest
}

func (a *recordingAuth) Login(ctx context.Context, r auth.LoginRequest) (auth.LoginResult, error) {
	a.logins = append(a.logins, r)
	return a.Authenticator.Login(ctx, r)
}

func TestLoginPassesTheListenerTrust(t *testing.T) {
	h := newHarness(t)
	rec := &recordingAuth{Authenticator: h.srv.cfg.Auth}
	h.srv.cfg.Auth = rec
	for _, tt := range []struct {
		local string
		want  bool
	}{
		{"127.0.0.1:8080", true},
		{"203.0.113.7:8080", false},
	} {
		r := request("POST", "/api/v1/auth/login", loginBody("admin", "wrong horse battery", "000000"))
		r = r.WithContext(context.WithValue(r.Context(), http.LocalAddrContextKey, net.TCPAddrFromAddrPort(netip.MustParseAddrPort(tt.local))))
		h.serve(r)
		if n := len(rec.logins); n == 0 || rec.logins[n-1].Trusted != tt.want {
			t.Errorf("login on %s: Trusted = %v, want %v", tt.local, rec.logins, tt.want)
		}
	}
}
