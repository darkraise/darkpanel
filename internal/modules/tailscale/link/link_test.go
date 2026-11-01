package link

import (
	"net/netip"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)

const lkOK = "no protected flow depends on what this change removes"

const lkSSHOn = "Tailscale SSH is on and a session may use it"

// lkFlow is a protected flow between two addresses, routed through iface.
func lkFlow(label string, inbound bool, local, remote, iface string) Flow {
	f := Flow{Label: label, Inbound: inbound, OutIface: iface}
	if local != "" {
		f.Local = netip.MustParseAddr(local)
	}
	if remote != "" {
		f.Remote = netip.MustParseAddr(remote)
	}
	return f
}

// lkRequest marks f as the flow that carries the request being planned.
func lkRequest(f Flow) Flow {
	f.Request = true
	return f
}

// lkPrefs changes to the node's preferences: a running node plus whatever set applies.
func lkPrefs(set func(*Prefs)) Prefs {
	p := Prefs{WantRunning: true}
	if set != nil {
		set(&p)
	}
	return p
}

type lkCase struct {
	name  string
	cur   Prefs
	chg   Change
	flows []Flow
	fail  string // the guard's message; empty when the guard passes
}

func lkPrefsChange(after Prefs) Change { return Change{Kind: "prefs", After: after} }

func lkCases() []lkCase {
	tailnetSSH := lkFlow("ssh", true, "100.64.0.5", "100.64.0.9", "tailscale0")
	lanSSH := lkFlow("ssh", true, "192.0.2.10", "198.51.100.7", "eth0")
	panel := lkFlow("panel", true, "100.64.0.5", "100.64.0.20", "tailscale0")
	subnet := lkFlow("controller link", false, "100.64.0.5", "10.8.0.3", "tailscale0")
	viaExit := lkFlow("controller session", false, "100.64.0.5", "203.0.113.9", "tailscale0")
	viaExit.Default = true
	route := netip.MustParsePrefix("192.168.7.0/24")

	return []lkCase{
		// logout, relogin and wantRunning false
		{"logout cuts a tailnet ssh session", lkPrefs(nil), Change{Kind: "logout"}, []Flow{tailnetSSH}, "ssh flow with 100.64.0.9 depends on Tailscale"},
		{"logout leaves flows that do not use tailscale", lkPrefs(nil), Change{Kind: "logout"}, []Flow{lanSSH}, ""},
		{"logout with no flows", lkPrefs(nil), Change{Kind: "logout"}, nil, ""},
		{"logout while Tailscale SSH is on", lkPrefs(func(p *Prefs) { p.RunSSH = true }), Change{Kind: "logout"}, []Flow{lanSSH}, lkSSHOn},
		{
			"logout names the flows and then Tailscale SSH", lkPrefs(func(p *Prefs) { p.RunSSH = true }), Change{Kind: "logout"},
			[]Flow{tailnetSSH}, "ssh flow with 100.64.0.9 depends on Tailscale; " + lkSSHOn,
		},
		{
			"relogin cuts a flow whose local address is on the tailnet", lkPrefs(nil), Change{Kind: "relogin"},
			[]Flow{lkFlow("agent link", false, "100.64.0.5", "203.0.113.9", "")}, "agent link flow with 203.0.113.9 depends on Tailscale",
		},
		{
			"an IPv6 tailnet address depends on tailscale", lkPrefs(nil), Change{Kind: "logout"},
			[]Flow{lkFlow("ssh", true, "fd7a:115c:a1e0::5", "fd7a:115c:a1e0::9", "")}, "ssh flow with fd7a:115c:a1e0::9 depends on Tailscale",
		},
		{
			"an IPv4-mapped tailnet address depends on tailscale", lkPrefs(nil), Change{Kind: "logout"},
			[]Flow{lkFlow("ssh", true, "::ffff:100.64.0.5", "198.51.100.7", "")}, "ssh flow with 198.51.100.7 depends on Tailscale",
		},
		{
			"wantRunning false cuts the panel", lkPrefs(nil), lkPrefsChange(Prefs{}),
			[]Flow{panel}, "panel flow with 100.64.0.20 depends on Tailscale",
		},
		{"wantRunning true leaves tailnet flows alone", lkPrefs(nil), lkPrefsChange(lkPrefs(nil)), []Flow{tailnetSSH}, ""},
		{
			"every dependent flow is named once, in order", lkPrefs(nil), Change{Kind: "logout"},
			[]Flow{tailnetSSH, lanSSH, panel, tailnetSSH},
			"ssh flow with 100.64.0.9 depends on Tailscale; panel flow with 100.64.0.20 depends on Tailscale",
		},
		{
			"logout does not apply the preferences of After", lkPrefs(func(p *Prefs) { p.AcceptRoutes = true }), Change{Kind: "logout"},
			[]Flow{subnet}, "controller link flow with 10.8.0.3 depends on Tailscale",
		},

		// shields-up
		{
			"shields-up blocks an inbound flow over tailscale", lkPrefs(nil), lkPrefsChange(lkPrefs(func(p *Prefs) { p.ShieldsUp = true })),
			[]Flow{tailnetSSH}, "ssh flow with 100.64.0.9 arrives over Tailscale and shields-up would block it",
		},
		{
			"shields-up leaves an outbound flow over tailscale", lkPrefs(nil), lkPrefsChange(lkPrefs(func(p *Prefs) { p.ShieldsUp = true })),
			[]Flow{subnet, lanSSH}, "",
		},
		{
			"shields-up while Tailscale SSH is on", lkPrefs(func(p *Prefs) { p.RunSSH = true }),
			lkPrefsChange(lkPrefs(func(p *Prefs) { p.RunSSH, p.ShieldsUp = true, true })), []Flow{lanSSH}, lkSSHOn,
		},
		{
			"shields-up already on", lkPrefs(func(p *Prefs) { p.ShieldsUp = true }),
			lkPrefsChange(lkPrefs(func(p *Prefs) { p.ShieldsUp = true })), []Flow{tailnetSSH}, "",
		},

		// runSSH off
		{
			"runSSH off with a request that arrives another way", lkPrefs(func(p *Prefs) { p.RunSSH = true }), lkPrefsChange(lkPrefs(nil)),
			[]Flow{lkRequest(tailnetSSH), lkRequest(lanSSH)}, "",
		},
		{
			"runSSH off with a request over tailscale", lkPrefs(func(p *Prefs) { p.RunSSH = true }), lkPrefsChange(lkPrefs(nil)),
			[]Flow{lkRequest(tailnetSSH), lanSSH}, "Tailscale SSH is on and no request flow is known to arrive another way",
		},
		{
			"runSSH off with no request flow", lkPrefs(func(p *Prefs) { p.RunSSH = true }), lkPrefsChange(lkPrefs(nil)),
			[]Flow{lanSSH}, "Tailscale SSH is on and no request flow is known to arrive another way",
		},
		{
			"runSSH off with a request flow that cannot be classified", lkPrefs(func(p *Prefs) { p.RunSSH = true }), lkPrefsChange(lkPrefs(nil)),
			[]Flow{lkRequest(Flow{Label: "ssh", Inbound: true, Unknown: "the socket has no owner"})},
			"Tailscale SSH is on and no request flow is known to arrive another way; ssh flow with an unknown address cannot be classified: the socket has no owner",
		},
		{"runSSH already off", lkPrefs(nil), lkPrefsChange(lkPrefs(nil)), []Flow{lkRequest(tailnetSSH)}, ""},

		// acceptRoutes off
		{
			"acceptRoutes off cuts a flow that uses an accepted route", lkPrefs(func(p *Prefs) { p.AcceptRoutes = true }), lkPrefsChange(lkPrefs(nil)),
			[]Flow{subnet}, "controller link flow with 10.8.0.3 uses a route accepted from the tailnet",
		},
		{
			"acceptRoutes off keeps a flow to a tailnet peer", lkPrefs(func(p *Prefs) { p.AcceptRoutes = true }), lkPrefsChange(lkPrefs(nil)),
			[]Flow{lkFlow("agent peer", false, "100.64.0.5", "100.64.0.9", "tailscale0")}, "",
		},
		{
			"acceptRoutes off leaves the exit node path to the exit node rule", lkPrefs(func(p *Prefs) { p.AcceptRoutes = true }), lkPrefsChange(lkPrefs(nil)),
			[]Flow{viaExit}, "",
		},
		{"acceptRoutes already off", lkPrefs(nil), lkPrefsChange(lkPrefs(nil)), []Flow{subnet}, ""},

		// exit node changes
		{
			"changing the exit node cuts a flow through it", lkPrefs(func(p *Prefs) { p.ExitNode = "nA" }),
			lkPrefsChange(lkPrefs(func(p *Prefs) { p.ExitNode = "nB" })), []Flow{viaExit},
			"controller session flow with 203.0.113.9 leaves through the exit node",
		},
		{
			"clearing the exit node cuts a flow through it", lkPrefs(func(p *Prefs) { p.ExitNode = "nA" }), lkPrefsChange(lkPrefs(nil)),
			[]Flow{viaExit}, "controller session flow with 203.0.113.9 leaves through the exit node",
		},
		{"choosing a first exit node", lkPrefs(nil), lkPrefsChange(lkPrefs(func(p *Prefs) { p.ExitNode = "nB" })), []Flow{viaExit}, ""},
		{
			"keeping the exit node", lkPrefs(func(p *Prefs) { p.ExitNode = "nA" }),
			lkPrefsChange(lkPrefs(func(p *Prefs) { p.ExitNode = "nA" })), []Flow{viaExit}, "",
		},
		{
			"changing the exit node leaves a subnet route alone", lkPrefs(func(p *Prefs) { p.ExitNode = "nA" }), lkPrefsChange(lkPrefs(nil)),
			[]Flow{subnet}, "",
		},

		// advertised routes removed
		{
			"removing an advertised route under an inbound flow", lkPrefs(func(p *Prefs) { p.AdvertiseRoutes = []netip.Prefix{route} }), lkPrefsChange(lkPrefs(nil)),
			[]Flow{lkFlow("ssh", true, "192.168.7.20", "100.64.0.9", "tailscale0")}, "ssh flow with 100.64.0.9 arrives through the advertised route 192.168.7.0/24",
		},
		{
			"keeping the advertised route", lkPrefs(func(p *Prefs) { p.AdvertiseRoutes = []netip.Prefix{route} }),
			lkPrefsChange(lkPrefs(func(p *Prefs) { p.AdvertiseRoutes = []netip.Prefix{route} })),
			[]Flow{lkFlow("ssh", true, "192.168.7.20", "100.64.0.9", "tailscale0")}, "",
		},
		{
			"keeping the advertised route written unmasked", lkPrefs(func(p *Prefs) { p.AdvertiseRoutes = []netip.Prefix{route} }),
			lkPrefsChange(lkPrefs(func(p *Prefs) { p.AdvertiseRoutes = []netip.Prefix{netip.MustParsePrefix("192.168.7.5/24")} })),
			[]Flow{lkFlow("ssh", true, "192.168.7.20", "100.64.0.9", "tailscale0")}, "",
		},
		{
			"removing an advertised route outside every flow", lkPrefs(func(p *Prefs) { p.AdvertiseRoutes = []netip.Prefix{route} }), lkPrefsChange(lkPrefs(nil)),
			[]Flow{lkFlow("ssh", true, "192.168.8.20", "100.64.0.9", "tailscale0")}, "",
		},
		{
			"removing an advertised route under an outbound flow", lkPrefs(func(p *Prefs) { p.AdvertiseRoutes = []netip.Prefix{route} }), lkPrefsChange(lkPrefs(nil)),
			[]Flow{lkFlow("ssh", false, "192.168.7.20", "100.64.0.9", "tailscale0")}, "",
		},
		{
			"removing an advertised route under a flow on another interface", lkPrefs(func(p *Prefs) { p.AdvertiseRoutes = []netip.Prefix{route} }), lkPrefsChange(lkPrefs(nil)),
			[]Flow{lkFlow("ssh", true, "192.168.7.20", "198.51.100.7", "eth0")}, "",
		},

		// exit node advertisement removed
		{
			"dropping the exit node advertisement under a public address", lkPrefs(func(p *Prefs) { p.AdvertiseExitNode = true }), lkPrefsChange(lkPrefs(nil)),
			[]Flow{lkFlow("ssh", true, "203.0.113.50", "100.64.0.9", "tailscale0")}, "ssh flow with 100.64.0.9 arrives through the advertised exit node",
		},
		{
			"dropping the exit node advertisement under a tailnet address", lkPrefs(func(p *Prefs) { p.AdvertiseExitNode = true }), lkPrefsChange(lkPrefs(nil)),
			[]Flow{tailnetSSH}, "",
		},
		{
			"dropping the exit node advertisement under an advertised route",
			lkPrefs(func(p *Prefs) {
				p.AdvertiseExitNode = true
				p.AdvertiseRoutes = []netip.Prefix{netip.MustParsePrefix("203.0.113.0/24")}
			}),
			lkPrefsChange(lkPrefs(func(p *Prefs) { p.AdvertiseRoutes = []netip.Prefix{netip.MustParsePrefix("203.0.113.0/24")} })),
			[]Flow{lkFlow("ssh", true, "203.0.113.50", "100.64.0.9", "tailscale0")}, "",
		},
		{
			"no exit node advertisement to drop", lkPrefs(nil), lkPrefsChange(lkPrefs(nil)),
			[]Flow{lkFlow("ssh", true, "203.0.113.50", "100.64.0.9", "tailscale0")}, "",
		},

		// flows that cannot be classified
		{
			"an unknown flow fails whatever the change", lkPrefs(nil), lkPrefsChange(lkPrefs(nil)),
			[]Flow{{Label: "panel", Remote: netip.MustParseAddr("198.51.100.7"), Unknown: "the socket has no owner"}},
			"panel flow with 198.51.100.7 cannot be classified: the socket has no owner",
		},
		{
			"an unknown flow without an address", lkPrefs(nil), lkPrefsChange(lkPrefs(nil)),
			[]Flow{{Label: "agent link", Unknown: "no route to the controller"}},
			"agent link flow with an unknown address cannot be classified: no route to the controller",
		},
		{
			"an unknown flow is named after the dependent ones", lkPrefs(nil), Change{Kind: "logout"},
			[]Flow{{Label: "agent link", Unknown: "no route to the controller"}, tailnetSSH},
			"ssh flow with 100.64.0.9 depends on Tailscale; agent link flow with an unknown address cannot be classified: no route to the controller",
		},

		// an unknown change kind
		{
			"an empty change kind fails", lkPrefs(nil), Change{}, []Flow{lanSSH},
			`the change kind "" is not prefs, logout or relogin, so no rule could check it`,
		},
		{
			"a misspelled change kind fails and still names unknown flows", lkPrefs(nil), Change{Kind: "pref", After: lkPrefs(nil)},
			[]Flow{{Label: "agent link", Unknown: "no route to the controller"}},
			`the change kind "pref" is not prefs, logout or relogin, so no rule could check it; agent link flow with an unknown address cannot be classified: no route to the controller`,
		},

		// several rules
		{
			"one flow can break two rules", lkPrefs(func(p *Prefs) { p.AcceptRoutes = true }), lkPrefsChange(Prefs{}),
			[]Flow{lkFlow("panel", false, "100.64.0.5", "10.2.0.9", "tailscale0")},
			"panel flow with 10.2.0.9 depends on Tailscale; panel flow with 10.2.0.9 uses a route accepted from the tailnet",
		},
		{
			"nothing removed", lkPrefs(func(p *Prefs) {
				p.AcceptRoutes, p.RunSSH, p.ShieldsUp, p.AdvertiseExitNode = true, true, true, true
				p.ExitNode = "nA"
				p.AdvertiseRoutes = []netip.Prefix{route}
			}),
			lkPrefsChange(lkPrefs(func(p *Prefs) {
				p.AcceptRoutes, p.RunSSH, p.ShieldsUp, p.AdvertiseExitNode = true, true, true, true
				p.ExitNode = "nA"
				p.AdvertiseRoutes = []netip.Prefix{route}
			})),
			[]Flow{tailnetSSH, subnet, viaExit, lkFlow("ssh", true, "192.168.7.20", "100.64.0.9", "tailscale0")}, "",
		},
	}
}

func TestLkCheck(t *testing.T) {
	for _, tt := range lkCases() {
		t.Run(tt.name, func(t *testing.T) {
			want := core.Guard{ID: "tailscale.link", OK: tt.fail == "", Message: tt.fail, Overridable: true}
			if want.OK {
				want.Message = lkOK
			}
			if got := Check(tt.cur, tt.chg, tt.flows); got != want {
				t.Fatalf("Check = %+v, want %+v", got, want)
			}
		})
	}
}

func TestLkIsTailscaleAddr(t *testing.T) {
	tests := []struct {
		addr string
		want bool
	}{
		{"100.64.0.0", true},
		{"100.100.100.100", true},
		{"100.127.255.255", true},
		{"100.63.255.255", false},
		{"100.128.0.0", false},
		{"192.0.2.1", false},
		{"127.0.0.1", false},
		{"fd7a:115c:a1e0::1", true},
		{"fd7a:115c:a1e0:ffff:ffff:ffff:ffff:ffff", true},
		{"fd7a:115c:a1e1::1", false},
		{"fd7a:115c:a1df::1", false},
		{"::1", false},
		{"::ffff:100.64.0.1", true},
		{"::ffff:192.0.2.1", false},
	}
	for _, tt := range tests {
		if got := IsTailscaleAddr(netip.MustParseAddr(tt.addr)); got != tt.want {
			t.Errorf("IsTailscaleAddr(%s) = %v, want %v", tt.addr, got, tt.want)
		}
	}
	if IsTailscaleAddr(netip.Addr{}) {
		t.Error("IsTailscaleAddr(zero Addr) = true")
	}
}

func TestLkConstants(t *testing.T) {
	if GuardID != "tailscale.link" || Interface != "tailscale0" {
		t.Fatalf("GuardID = %q, Interface = %q", GuardID, Interface)
	}
}
