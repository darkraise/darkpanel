package api

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sync"
	"testing"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/core"
)

// acceptanceAuth records the login requests the login route hands to the
// authentication service.
type acceptanceAuth struct {
	Authenticator
	mu     sync.Mutex
	logins []auth.LoginRequest
}

func (a *acceptanceAuth) Login(ctx context.Context, r auth.LoginRequest) (auth.LoginResult, error) {
	a.mu.Lock()
	a.logins = append(a.logins, r)
	a.mu.Unlock()
	return a.Authenticator.Login(ctx, r)
}

func (a *acceptanceAuth) count() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.logins)
}

// serveLogin serves r and returns the login request the route passed on.
func (a *acceptanceAuth) serveLogin(t *testing.T, srv *Server, r *http.Request) auth.LoginRequest {
	t.Helper()
	before := a.count()
	srv.Handler().ServeHTTP(httptest.NewRecorder(), r)
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.logins) != before+1 {
		t.Fatalf("the login route called the authentication service %d times, want once", len(a.logins)-before)
	}
	return a.logins[len(a.logins)-1]
}

// TestA19TheLoginRequestFieldFollowsTheLocalAddress covers the API half of
// A19: the trusted field of the login request is true for a request whose
// local address is loopback or a served Tailscale address, and false for a
// public address, for no local address and for a proxied request.
func TestA19TheLoginRequestFieldFollowsTheLocalAddress(t *testing.T) {
	h := newHarness(t)
	rec := &acceptanceAuth{Authenticator: h.cfg.Auth}
	cfg := h.cfg
	cfg.Auth = rec
	srv, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	tailscale := []netip.Addr{netip.MustParseAddr("100.64.0.5")}
	srv.SetTailscaleAddrs(func() []netip.Addr { return tailscale })

	tests := []struct {
		name    string
		local   string
		proxied bool
		want    bool
	}{
		{"loopback", "127.0.0.1:8080", false, true},
		{"IPv6 loopback", "[::1]:8080", false, true},
		{"served Tailscale address", "100.64.0.5:8443", false, true},
		{"public address", "203.0.113.7:8080", false, false},
		{"a Tailscale address that is not served", "100.64.0.6:8443", false, false},
		{"no local address", "", false, false},
		{"a proxied request on loopback", "127.0.0.1:9443", true, false},
	}
	for _, tt := range tests {
		t.Run("A19: "+tt.name, func(t *testing.T) {
			r := request("POST", "/api/v1/auth/login", loginBody("admin", "wrong horse battery", "000000"))
			ctx := r.Context()
			if tt.local != "" {
				ctx = context.WithValue(ctx, http.LocalAddrContextKey, net.TCPAddrFromAddrPort(netip.MustParseAddrPort(tt.local)))
			}
			if tt.proxied {
				ctx = core.WithProxied(ctx, core.Proxied{ListenerID: "L1", Client: netip.MustParseAddr("192.0.2.9")})
			}
			if got := rec.serveLogin(t, srv, r.WithContext(ctx)).Trusted; got != tt.want {
				t.Errorf("A19: login on %q (proxied %v): Trusted = %v, want %v", tt.local, tt.proxied, got, tt.want)
			}
		})
	}

	t.Run("A19: no Tailscale source", func(t *testing.T) {
		srv.SetTailscaleAddrs(nil)
		r := request("POST", "/api/v1/auth/login", loginBody("admin", "wrong horse battery", "000000"))
		ctx := context.WithValue(r.Context(), http.LocalAddrContextKey, net.TCPAddrFromAddrPort(netip.MustParseAddrPort("100.64.0.5:8443")))
		if rec.serveLogin(t, srv, r.WithContext(ctx)).Trusted {
			t.Error("A19: a Tailscale address counted as trusted with no Tailscale source set")
		}
	})
}
