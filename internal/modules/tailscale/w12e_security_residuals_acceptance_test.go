package tailscale

import (
	"context"
	"net/netip"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/tailscale/link"
)

// qaExposure is the guard context of a request that came through the NPM exposure e1, whose public binding is
// 0.0.0.0:443, made by client.
func qaExposure(client string) core.Guarded {
	return core.Guarded{Guard: &core.GuardContext{
		Complete: true,
		Client:   &core.Peer{Addr: netip.MustParseAddr(client), Port: 51000},
		Via: &core.ProxyPath{ExposureID: "e1", Kind: "npm", ProxyAddr: netip.MustParseAddr("172.30.0.2"),
			ListenerAddr: netip.MustParseAddrPort("172.30.0.1:8080"), Public: lgAddrs("0.0.0.0:443")},
	}}
}

// TestA10_ATailnetClientOfAnNPMExposureBlocksATailscaleLinkChange: for a request through an NPM exposure whose client
// is a tailnet address, every change that takes the Tailscale link down is refused by the link guard unless the
// caller overrides it; a client that is an address of this host adds no flow.
func TestA10_ATailnetClientOfAnNPMExposureBlocksATailscaleLinkChange(t *testing.T) {
	cur := lgPrefs(nil)
	changes := map[string]link.Change{
		"a logout":               lgLogout,
		"WantRunning turned off": lgPrefsChange(cur, func(p *link.Prefs) { p.WantRunning = false }),
	}
	for name, change := range changes {
		t.Run("a tailnet client: "+name, func(t *testing.T) {
			h, tsv := lgHost(t, lgWorld{controller: true, routes: map[string]string{"100.64.0.9": "tailscale0", "172.30.0.2": "dp-panel"}})
			g, err := tsv.linkGuard(context.Background(), h.env(""), qaExposure("100.64.0.9"), cur, change)
			if err != nil {
				t.Fatal(err)
			}
			if g.OK || g.Overridden || g.ID != link.GuardID {
				t.Fatalf("guard %+v, want a refusal by %s that is not overridden", g, link.GuardID)
			}

			over := qaExposure("100.64.0.9")
			over.Overrides = []string{link.GuardID}
			h, tsv = lgHost(t, lgWorld{controller: true, routes: map[string]string{"100.64.0.9": "tailscale0", "172.30.0.2": "dp-panel"}})
			g, err = tsv.linkGuard(context.Background(), h.env(""), over, cur, change)
			if err != nil {
				t.Fatal(err)
			}
			if !g.OK || !g.Overridden {
				t.Errorf("guard %+v with the override, want OK and overridden", g)
			}
		})

		t.Run("a host-address client: "+name, func(t *testing.T) {
			// The route table sends the host-address client through tailscale0, so a flow added for it would fail
			// the change; the client of Docker's userland proxy is the bridge gateway, an address of this host.
			h, tsv := lgHost(t, lgWorld{controller: true, routes: map[string]string{"172.30.0.1": "tailscale0", "172.30.0.2": "dp-panel"}})
			g, err := tsv.linkGuard(context.Background(), h.env(""), qaExposure("172.30.0.1"), cur, change)
			if err != nil {
				t.Fatal(err)
			}
			if !g.OK || g.Overridden {
				t.Errorf("guard %+v, want a plain pass: a host-address client adds no flow", g)
			}
		})
	}
}
