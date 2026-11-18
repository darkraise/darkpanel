package auth

import (
	"context"
	"fmt"
	"net/netip"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// TestA19AFullLimiterTableStillEvaluatesLoopbackAndTailscale covers the auth
// half of A19: with the key cap lowered and every key locked, an attempt that
// arrived on a loopback or Tailscale listener and needs a new key is
// evaluated, while the same attempt on the public listener, or one a runtime
// listener proxied, is refused with auth.rate_limited.
func TestA19AFullLimiterTableStillEvaluatesLoopbackAndTailscale(t *testing.T) {
	s, clock, _ := newTestService(t)
	_, secret := createActive(t, s, clock, "alice")
	s.limiter.max = 2
	ctx := context.Background()

	// Five failures of one name lock the pair key; failures of other names
	// then count toward the source key until it locks too, which leaves the
	// two-key table full of locked keys.
	for range UsernameFailureLimit {
		_, err := s.Login(ctx, LoginRequest{Username: "mallory", Password: "wrong horse battery", Code: "000000", Source: "198.51.100.7"})
		wantCode(t, err, errcode.InvalidCredentials)
	}
	locked := false
	for i := range 100 {
		_, err := s.Login(ctx, LoginRequest{Username: fmt.Sprintf("probe%d", i), Password: "wrong horse battery", Code: "000000", Source: "198.51.100.7"})
		if errcode.From(err).Code == errcode.RateLimited {
			locked = true
			break
		}
	}
	if !locked {
		t.Fatal("A19: 100 failed attempts from one source never locked it")
	}

	good := func(source string, trusted bool) LoginRequest {
		return LoginRequest{Username: "alice", Password: testPassword, Code: code(t, secret, clock), Source: source, Trusted: trusted}
	}
	t.Run("A19: the public listener is refused", func(t *testing.T) {
		_, err := s.Login(ctx, good("198.51.100.8", false))
		wantCode(t, err, errcode.RateLimited)
	})
	t.Run("A19: a runtime listener is refused, whatever the flag says", func(t *testing.T) {
		pctx := core.WithProxied(ctx, core.Proxied{
			ListenerID: "L1",
			Client:     netip.MustParseAddr("2001:db8:9::1"),
			Transport:  netip.MustParseAddrPort("172.31.255.2:40000"),
		})
		_, err := s.Login(pctx, good("2001:db8:9::1 via 172.31.255.2", true))
		wantCode(t, err, errcode.RateLimited)
	})
	t.Run("A19: a trusted listener is evaluated, wrong password", func(t *testing.T) {
		_, err := s.Login(ctx, LoginRequest{Username: "alice", Password: "wrong horse battery", Code: "000000", Source: "127.0.0.1", Trusted: true})
		wantCode(t, err, errcode.InvalidCredentials)
	})
	t.Run("A19: a trusted listener is evaluated, right password", func(t *testing.T) {
		res, err := s.Login(ctx, good("100.64.0.9", true))
		if err != nil {
			t.Fatalf("A19: trusted sign-in with a full table: %v", err)
		}
		if res.SessionID == "" {
			t.Error("A19: the trusted sign-in returned no session")
		}
	})
}
