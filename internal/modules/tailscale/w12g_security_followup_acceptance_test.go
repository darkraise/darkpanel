package tailscale

import (
	"context"
	"net/netip"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)

func TestA18_NPMExposureWithNoPublicBindingCannotBeShownSafe(t *testing.T) {
	routes := map[string]string{"100.64.0.9": "tailscale0", "172.30.0.2": "dp-panel", "203.0.113.50": "eth0"}
	via := func(public ...string) core.Guarded {
		return core.Guarded{Guard: &core.GuardContext{Complete: true,
			Client: &core.Peer{Addr: netip.MustParseAddr("203.0.113.50"), Port: 51000},
			Via: &core.ProxyPath{ExposureID: "e1", Kind: core.ProxyKindNPM, ProxyAddr: netip.MustParseAddr("172.30.0.2"),
				ListenerAddr: netip.MustParseAddrPort("172.30.0.1:8080"), Public: lgAddrs(public...)}}}
	}
	for _, tc := range []struct {
		name   string
		in     core.Guarded
		wantOK bool
	}{
		{"a client off the tailnet with a public binding passes", via("0.0.0.0:443"), true},
		{"a client off the tailnet with no public binding is refused", via(), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, tsv := lgHost(t, lgWorld{controller: true, routes: routes})
			g, err := tsv.linkGuard(context.Background(), h.env(""), tc.in, lgPrefs(nil), lgLogout)
			if err != nil {
				t.Fatal(err)
			}
			if g.OK != tc.wantOK {
				t.Errorf("guard OK %v (%s), want %v", g.OK, g.Message, tc.wantOK)
			}
		})
	}
}
