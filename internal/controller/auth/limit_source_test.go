package auth

import (
	"context"
	"net/netip"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)

func TestW11c08MappedProxiedClientGroupsWithItsIPv4Form(t *testing.T) {
	ctx := core.WithProxied(context.Background(), core.Proxied{Client: netip.MustParseAddr("::ffff:192.0.2.7")})
	if got, want := sourceKey(limitSource(ctx, "172.31.255.1")), sourceKey("192.0.2.7"); got != want {
		t.Errorf("source key %q, want %q", got, want)
	}
}
