package auth

import (
	"context"
	"fmt"
	"net/netip"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// v1r17Burst is count failed logins of one username from one source.
type v1r17Burst struct {
	source string
	count  int
}

func TestV1r17LimiterLocksTheUserAndSourcePair(t *testing.T) {
	fiveV6 := []v1r17Burst{{"2001:db8:1:2::1", 1}, {"2001:db8:1:2::2", 1}, {"2001:db8:1:2::3", 1}, {"2001:db8:1:2::4", 1}, {"2001:db8:1:2::5", 1}}
	tests := []struct {
		name        string
		failures    []v1r17Burst
		user        string
		source      string
		wantBlocked bool
	}{
		{"same user and source", []v1r17Burst{{"10.0.0.1", UsernameFailureLimit}}, "alice", "10.0.0.1", true},
		{"same user, another IPv4 source", []v1r17Burst{{"10.0.0.1", UsernameFailureLimit}}, "alice", "10.0.0.2", false},
		{"another user, same source", []v1r17Burst{{"10.0.0.1", UsernameFailureLimit}}, "bob", "10.0.0.1", false},
		{"same user, IPv4-mapped form of the source", []v1r17Burst{{"10.0.0.1", UsernameFailureLimit}}, "alice", "::ffff:10.0.0.1", true},
		{"failures split over two sources", []v1r17Burst{{"10.0.0.1", UsernameFailureLimit - 1}, {"10.0.0.2", UsernameFailureLimit - 1}}, "alice", "10.0.0.1", false},
		{"same user, another address of the /64", fiveV6, "alice", "2001:db8:1:2:ffff::9", true},
		{"same user, another /64", fiveV6, "alice", "2001:db8:1:3::1", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := newLimiter(coretest.NewFakeClock(t0))
			for _, b := range tt.failures {
				for range b.count {
					l.fail("alice", b.source)
				}
			}
			got := l.blocked(tt.user, tt.source)
			switch {
			case tt.wantBlocked && got != LockDuration:
				t.Errorf("%s from %s blocked for %s, want %s", tt.user, tt.source, got, LockDuration)
			case !tt.wantBlocked && got != 0:
				t.Errorf("%s from %s blocked for %s, want allowed", tt.user, tt.source, got)
			}
		})
	}
}

func TestV1r17LoginLockIsPerSource(t *testing.T) {
	s, clock, _ := newTestService(t)
	ctx := context.Background()
	_, secret := createActive(t, s, clock, "alice")
	for range UsernameFailureLimit {
		_, err := s.Login(ctx, LoginRequest{Username: "alice", Password: "wrong horse battery", Code: "000000", Source: "10.0.0.1"})
		wantCode(t, err, errcode.InvalidCredentials)
	}
	_, err := s.Login(ctx, LoginRequest{Username: "alice", Password: testPassword, Code: code(t, secret, clock), Source: "10.0.0.1"})
	wantCode(t, err, errcode.RateLimited)
	if _, err := s.Login(ctx, LoginRequest{Username: "alice", Password: testPassword, Code: code(t, secret, clock), Source: "10.0.0.2"}); err != nil {
		t.Errorf("alice from another source after five failures from the first: %v, want a session", err)
	}
}

// v1r17Via is an attempt that came through a runtime listener from client:
// the context carries the client the listener parsed and Source holds the
// audit text "<client> via <proxy>".
func v1r17Via(client, username string) (context.Context, LoginRequest) {
	ctx := core.WithProxied(context.Background(), core.Proxied{
		ListenerID: "L1",
		Client:     netip.MustParseAddr(client),
		Transport:  netip.MustParseAddrPort("172.31.255.2:40000"),
	})
	return ctx, LoginRequest{Username: username, Password: "wrong horse battery", Code: "000000", Source: client + " via 172.31.255.2"}
}

func TestV1r17LoginKeysTheProxiedClient(t *testing.T) {
	s, _, audit := newTestService(t)
	for i := range SourceFailureLimit {
		ctx, req := v1r17Via(fmt.Sprintf("2001:db8:1:2::%x", i+1), fmt.Sprintf("probe%d", i))
		_, err := s.Login(ctx, req)
		wantCode(t, err, errcode.InvalidCredentials)
	}
	ctx, req := v1r17Via("2001:db8:1:2:ffff::9", "probe99")
	_, err := s.Login(ctx, req)
	wantCode(t, err, errcode.RateLimited)

	ctx, req = v1r17Via("2001:db8:1:3::1", "probe98")
	_, err = s.Login(ctx, req)
	wantCode(t, err, errcode.InvalidCredentials)
	entries, _, _ := audit.List(context.Background(), core.AuditQuery{Limit: 1})
	if len(entries) != 1 || entries[0].Source != "2001:db8:1:3::1 via 172.31.255.2" {
		t.Errorf("audit entries %+v, want the source text %q", entries, "2001:db8:1:3::1 via 172.31.255.2")
	}
}
