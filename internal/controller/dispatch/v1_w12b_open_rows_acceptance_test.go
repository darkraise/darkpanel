package dispatch

import (
	"context"
	"encoding/json"
	"net/netip"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)

func TestA5_AStepOfACompositeIsPlannedAndAppliedWithTheRequestersGuard(t *testing.T) {
	d, seen, agent := newDg(t, func() Topology { return dgTopology })
	addr := netip.MustParseAddr("203.0.113.5")
	ctx := core.WithProxied(core.WithClientPeer(context.Background(), core.Peer{Addr: addr}), core.Proxied{
		Client: addr,
		Path: core.ProxyPath{
			ExposureID: "exp-qa", Kind: "npm", ProxyAddr: netip.MustParseAddr("172.31.255.2"),
			ListenerAddr: netip.MustParseAddrPort("172.31.255.1:9443"),
			Public:       []netip.AddrPort{netip.MustParseAddrPort("198.51.100.4:443")},
		},
	})
	call := &core.CallContext{ParentChangeID: "01COMPOSITE", ParentAction: "cfx.deploy.add", StepID: "a"}
	kids := d.Children()
	v, err := kids.Plan(ctx, alice, testHost, refDgAgent, json.RawMessage(`{"name":"a"}`), core.PlanOptions{Call: call})
	if err != nil {
		t.Fatal(err)
	}
	check := func(what string, g *core.GuardContext) {
		t.Helper()
		if g == nil || g.Client == nil || g.Client.Addr != addr {
			t.Errorf("%s: guard client %+v, want %s", what, g, addr)
			return
		}
		if g.Via == nil || g.Via.ExposureID != "exp-qa" || g.Via.Kind != "npm" ||
			g.Via.ProxyAddr != netip.MustParseAddr("172.31.255.2") || len(g.Via.Public) != 1 ||
			g.Via.Public[0] != netip.MustParseAddrPort("198.51.100.4:443") {
			t.Errorf("%s: guard exposure path %+v, want exp-qa through 172.31.255.2 with the public binding 198.51.100.4:443", what, g.Via)
		}
	}
	check("plan", seen.guard(core.SideAgent))
	if _, err := kids.Apply(ctx, alice, testHost, refDgAgent, v.PlanID, core.ApplyOptions{Call: call}); err != nil {
		t.Fatal(err)
	}
	got := agent.lastOpts()
	if got.Call == nil {
		t.Fatal("the step's apply carried no call context")
	}
	check("apply", got.Call.GuardRefresh)

	direct := core.WithClientPeer(context.Background(), core.Peer{Addr: netip.MustParseAddr("192.0.2.77")})
	if _, err := kids.Plan(direct, alice, testHost, refDgAgent, json.RawMessage(`{"name":"b"}`), core.PlanOptions{Call: call}); err != nil {
		t.Fatal(err)
	}
	g := seen.guard(core.SideAgent)
	if g == nil || g.Client == nil || g.Client.Addr != netip.MustParseAddr("192.0.2.77") || g.Via != nil {
		t.Errorf("a direct panel client's step plan guard %+v, want that client and no exposure path", g)
	}
}
