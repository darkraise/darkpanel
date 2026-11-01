package tailscale

import (
	"context"
	"errors"
	"net/netip"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/tailscale/link"
	"github.com/darkraise/darkpanel/internal/platform/netstat"
	"github.com/darkraise/darkpanel/internal/platform/tslocal"
)

const (
	lgAgentPID      = 99
	lgControllerPID = 4242
	lgInternet4     = "198.51.100.1" // the stand-in for any internet address
)

// lgSock is a TCP socket of the table held by one process.
func lgSock(state netstat.State, local, remote, comm string, pid int) netstat.Socket {
	s := netstat.Socket{Proto: netstat.ProtoTCP, Family: netstat.IPv4, State: state, Local: netip.MustParseAddrPort(local)}
	if remote != "" {
		s.Remote = netip.MustParseAddrPort(remote)
	}
	if s.Local.Addr().Is6() {
		s.Family = netstat.IPv6
	}
	s.Processes = []netstat.Process{{PID: pid, Comm: comm}}
	return s
}

// lgSSH is a listening sshd and one accepted session.
func lgSSH(local, remote string) []netstat.Socket {
	return []netstat.Socket{
		lgSock(netstat.StateListen, "0.0.0.0:22", "", "sshd", 7),
		lgSock(netstat.StateEstablished, local, remote, "sshd-session", 8),
	}
}

// lgControllerSession is the controller's listener and one session it accepted.
func lgControllerSession(local, remote string) []netstat.Socket {
	return []netstat.Socket{
		lgSock(netstat.StateListen, local, "", "darkpanel", lgControllerPID),
		lgSock(netstat.StateEstablished, local, remote, "darkpanel", lgControllerPID),
	}
}

func lgIfaces() []netstat.Interface {
	return []netstat.Interface{
		{Index: 1, Name: "lo", Up: true, Running: true, Loopback: true, Addrs: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/8")}},
		{Index: 2, Name: "eth0", Up: true, Running: true, Addrs: []netip.Prefix{netip.MustParsePrefix("192.0.2.1/24")}},
		{Index: 3, Name: "tailscale0", Up: true, Running: true, Addrs: []netip.Prefix{netip.MustParsePrefix("100.64.0.1/32")}},
		{Index: 4, Name: "dp-panel", Up: true, Running: true, Addrs: []netip.Prefix{netip.MustParsePrefix("172.30.0.1/24")}},
	}
}

// lgWorld is what the fake host reports.
type lgWorld struct {
	sockets    []netstat.Socket
	routes     map[string]string // destination to the interface the kernel routes it through
	controller bool              // darkpanel-controller.service runs with lgControllerPID
}

// lgHost builds a host and a ts over w. A lookup of an address w.routes does
// not name has no route.
func lgHost(t *testing.T, w lgWorld) (*tsHost, ts) {
	t.Helper()
	h := newTSHost(t)
	h.link.Sockets = w.sockets
	h.link.Ifaces = lgIfaces()
	h.link.Pid = lgAgentPID
	h.link.Routes = map[netip.Addr]netstat.Route{}
	for dst, iface := range w.routes {
		h.link.Routes[netip.MustParseAddr(dst)] = netstat.Route{Type: "unicast", OutIface: iface}
	}
	if h.units.States == nil {
		h.units.States = map[string]core.UnitState{}
	}
	h.units.States[controllerUnit] = core.UnitState{LoadState: "not-found", ActiveState: "inactive", SubState: "dead"}
	if w.controller {
		h.units.States[controllerUnit] = core.UnitState{LoadState: "loaded", ActiveState: "active", SubState: "running", MainPID: lgControllerPID}
	}
	return h, h.ts()
}

func lgPrefs(mut func(*link.Prefs)) link.Prefs {
	p := link.Prefs{WantRunning: true}
	if mut != nil {
		mut(&p)
	}
	return p
}

// lgPrefsChange is a prefs change from cur that applies mut.
func lgPrefsChange(cur link.Prefs, mut func(*link.Prefs)) link.Change {
	after := cur
	mut(&after)
	return link.Change{Kind: "prefs", After: after}
}

func lgAddrs(s ...string) []netip.AddrPort {
	out := make([]netip.AddrPort, len(s))
	for i, a := range s {
		out[i] = netip.MustParseAddrPort(a)
	}
	return out
}

var lgLogout = link.Change{Kind: "logout"}

func TestLgLinkGuard(t *testing.T) {
	sshOverTailnet := lgSSH("100.64.0.1:22", "100.64.0.9:50000")
	sshOverEth0 := lgSSH("192.0.2.1:22", "203.0.113.7:50000")
	panelCtx := func(client string) *core.GuardContext {
		return &core.GuardContext{Complete: true, Client: &core.Peer{Addr: netip.MustParseAddr(client), Port: 51000},
			PanelListen: lgAddrs("0.0.0.0:8443")}
	}
	cases := []struct {
		name    string
		w       lgWorld
		in      core.Guarded
		refresh *core.GuardContext
		cur     link.Prefs
		change  link.Change
		wantOK  bool
		wantIn  []string // the message of a failing guard names these
	}{
		{name: "no protected flow", cur: lgPrefs(nil), change: lgLogout, wantOK: true},
		{
			name: "an ssh session over eth0 does not depend on Tailscale", cur: lgPrefs(nil), change: lgLogout, wantOK: true,
			w: lgWorld{sockets: sshOverEth0, routes: map[string]string{"203.0.113.7": "eth0"}},
		},
		{
			name: "an ssh session over the tailnet fails a logout", cur: lgPrefs(nil), change: lgLogout,
			w:      lgWorld{sockets: sshOverTailnet, routes: map[string]string{"100.64.0.9": "tailscale0"}},
			wantIn: []string{"ssh", "100.64.0.9"},
		},
		{
			name: "Tailscale SSH on fails a logout even with every flow on eth0", cur: lgPrefs(func(p *link.Prefs) { p.RunSSH = true }), change: lgLogout,
			w: lgWorld{sockets: sshOverEth0, routes: map[string]string{"203.0.113.7": "eth0"}},
		},
		{
			name: "a loopback ssh session is not a flow", cur: lgPrefs(nil), change: lgLogout, wantOK: true,
			w: lgWorld{sockets: lgSSH("127.0.0.1:22", "127.0.0.1:50000")},
		},
		{
			name: "stopping the node fails for a flow over the tailnet", cur: lgPrefs(nil),
			change: lgPrefsChange(lgPrefs(nil), func(p *link.Prefs) { p.WantRunning = false }),
			w:      lgWorld{sockets: sshOverTailnet, routes: map[string]string{"100.64.0.9": "tailscale0"}},
			wantIn: []string{"ssh"},
		},
		{
			name: "shields-up fails for an inbound flow over the tailnet", cur: lgPrefs(nil),
			change: lgPrefsChange(lgPrefs(nil), func(p *link.Prefs) { p.ShieldsUp = true }),
			w:      lgWorld{sockets: sshOverTailnet, routes: map[string]string{"100.64.0.9": "tailscale0"}},
			wantIn: []string{"ssh"},
		},
		{
			name: "shields-up passes for an inbound flow over eth0", cur: lgPrefs(nil), wantOK: true,
			change: lgPrefsChange(lgPrefs(nil), func(p *link.Prefs) { p.ShieldsUp = true }),
			w:      lgWorld{sockets: sshOverEth0, routes: map[string]string{"203.0.113.7": "eth0"}},
		},
		{
			name: "a controller session over the tailnet fails a logout", cur: lgPrefs(nil), change: lgLogout,
			w: lgWorld{controller: true, sockets: lgControllerSession("100.64.0.1:7443", "100.64.0.5:41000"),
				routes: map[string]string{"100.64.0.5": "tailscale0"}},
			wantIn: []string{"controller session", "100.64.0.5"},
		},
		{
			name: "a controller listener on a tailnet address fails a logout", cur: lgPrefs(nil), change: lgLogout,
			w: lgWorld{controller: true, sockets: []netstat.Socket{
				lgSock(netstat.StateListen, "100.64.0.1:7443", "", "darkpanel", lgControllerPID)}},
			wantIn: []string{"controller link"},
		},
		{
			name: "a controller listener on every address reached over eth0 passes", cur: lgPrefs(nil), change: lgLogout, wantOK: true,
			w: lgWorld{controller: true, sockets: []netstat.Socket{lgSock(netstat.StateListen, "0.0.0.0:443", "", "darkpanel", lgControllerPID)},
				routes: map[string]string{lgInternet4: "eth0"}},
		},
		{
			name: "a loopback controller listener is not a flow", cur: lgPrefs(nil), change: lgLogout, wantOK: true,
			w: lgWorld{controller: true, sockets: []netstat.Socket{lgSock(netstat.StateListen, "127.0.0.1:8080", "", "darkpanel", lgControllerPID)}},
		},
		{
			name: "an exit node change fails for a dual-stack listener whose IPv4 reply path is the exit node", cur: lgPrefs(func(p *link.Prefs) { p.ExitNode = "n1" }),
			change: lgPrefsChange(lgPrefs(func(p *link.Prefs) { p.ExitNode = "n1" }), func(p *link.Prefs) { p.ExitNode = "" }),
			w: lgWorld{controller: true, sockets: []netstat.Socket{lgSock(netstat.StateListen, "[::]:443", "", "darkpanel", lgControllerPID)},
				routes: map[string]string{lgInternet4: "tailscale0"}},
			wantIn: []string{"controller link"},
		},
		{
			name: "the agent's session over the tailnet fails a logout", cur: lgPrefs(nil), change: lgLogout,
			w: lgWorld{sockets: []netstat.Socket{lgSock(netstat.StateEstablished, "100.64.0.1:50000", "100.64.0.7:7443", "darkpanel", lgAgentPID)},
				routes: map[string]string{"100.64.0.7": "tailscale0"}},
			wantIn: []string{"agent link", "100.64.0.7"},
		},
		{
			name: "the agent's session over eth0 passes", cur: lgPrefs(nil), change: lgLogout, wantOK: true,
			w: lgWorld{sockets: []netstat.Socket{lgSock(netstat.StateEstablished, "192.0.2.1:50000", "203.0.113.9:7443", "darkpanel", lgAgentPID)},
				routes: map[string]string{"203.0.113.9": "eth0"}},
		},
		{
			name: "the panel client over the tailnet fails a logout", cur: lgPrefs(nil), change: lgLogout,
			w:      lgWorld{controller: true, routes: map[string]string{"100.64.0.9": "tailscale0"}},
			in:     core.Guarded{Guard: panelCtx("100.64.0.9")},
			wantIn: []string{"panel", "100.64.0.9"},
		},
		{
			name: "the panel client over eth0 passes", cur: lgPrefs(nil), change: lgLogout, wantOK: true,
			w:  lgWorld{controller: true, routes: map[string]string{"203.0.113.7": "eth0"}},
			in: core.Guarded{Guard: panelCtx("203.0.113.7")},
		},
		{
			name: "a panel client of a controller elsewhere is not a flow", cur: lgPrefs(nil), change: lgLogout, wantOK: true,
			w: lgWorld{routes: map[string]string{"100.64.0.9": "tailscale0"}},
			in: core.Guarded{Guard: &core.GuardContext{Complete: true, Client: &core.Peer{Addr: netip.MustParseAddr("100.64.0.9")},
				PanelListen: lgAddrs("10.9.9.9:8443")}},
		},
		{
			name: "an agent peer over the tailnet fails a logout", cur: lgPrefs(nil), change: lgLogout,
			w: lgWorld{controller: true, routes: map[string]string{"100.64.0.7": "tailscale0"}},
			in: core.Guarded{Guard: &core.GuardContext{Complete: true, AgentPeers: []netip.Addr{netip.MustParseAddr("100.64.0.7")},
				AgentListen: lgAddrs("100.64.0.1:7443")}},
			wantIn: []string{"agent peer", "100.64.0.7"},
		},
		{
			name: "an agent peer over eth0 passes", cur: lgPrefs(nil), change: lgLogout, wantOK: true,
			w: lgWorld{controller: true, routes: map[string]string{"192.0.2.77": "eth0"}},
			in: core.Guarded{Guard: &core.GuardContext{Complete: true, AgentPeers: []netip.Addr{netip.MustParseAddr("192.0.2.77")},
				AgentListen: lgAddrs("192.0.2.1:7443")}},
		},
		{
			name: "an incomplete topology fails whatever the change", cur: lgPrefs(nil), change: lgLogout,
			in: core.Guarded{Guard: &core.GuardContext{Complete: false}},
		},
		{
			name: "the refreshed topology is read at apply too", cur: lgPrefs(nil), change: lgLogout,
			w:       lgWorld{controller: true, routes: map[string]string{"100.64.0.9": "tailscale0"}},
			in:      core.Guarded{Guard: &core.GuardContext{Complete: true}},
			refresh: panelCtx("100.64.0.9"),
			wantIn:  []string{"panel"},
		},
		{
			name: "a flow with no route fails whatever the change", cur: lgPrefs(nil),
			change: lgPrefsChange(lgPrefs(nil), func(*link.Prefs) {}),
			w:      lgWorld{sockets: sshOverEth0},
		},
		{
			name: "accept routes off fails for a subnet route flow", cur: lgPrefs(func(p *link.Prefs) { p.AcceptRoutes = true }),
			change: lgPrefsChange(lgPrefs(func(p *link.Prefs) { p.AcceptRoutes = true }), func(p *link.Prefs) { p.AcceptRoutes = false }),
			w: lgWorld{sockets: lgSSH("192.0.2.1:22", "10.20.0.5:50000"),
				routes: map[string]string{"10.20.0.5": "tailscale0", lgInternet4: "eth0"}},
			wantIn: []string{"ssh", "10.20.0.5"},
		},
		{
			name: "accept routes off passes when the exit node carries the flow anyway", cur: lgPrefs(func(p *link.Prefs) { p.AcceptRoutes = true }), wantOK: true,
			change: lgPrefsChange(lgPrefs(func(p *link.Prefs) { p.AcceptRoutes = true }), func(p *link.Prefs) { p.AcceptRoutes = false }),
			w: lgWorld{sockets: lgSSH("192.0.2.1:22", "10.20.0.5:50000"),
				routes: map[string]string{"10.20.0.5": "tailscale0", lgInternet4: "tailscale0"}},
		},
		{
			name: "an exit node change fails for a flow over the default route", cur: lgPrefs(func(p *link.Prefs) { p.ExitNode = "n1" }),
			change: lgPrefsChange(lgPrefs(func(p *link.Prefs) { p.ExitNode = "n1" }), func(p *link.Prefs) { p.ExitNode = "" }),
			w:      lgWorld{sockets: sshOverEth0, routes: map[string]string{"203.0.113.7": "tailscale0", lgInternet4: "tailscale0"}},
			wantIn: []string{"ssh", "203.0.113.7"},
		},
		{
			name: "an exit node change passes for a flow over a subnet route", cur: lgPrefs(func(p *link.Prefs) { p.ExitNode = "n1" }), wantOK: true,
			change: lgPrefsChange(lgPrefs(func(p *link.Prefs) { p.ExitNode = "n1" }), func(p *link.Prefs) { p.ExitNode = "" }),
			w:      lgWorld{sockets: sshOverEth0, routes: map[string]string{"203.0.113.7": "tailscale0", lgInternet4: "eth0"}},
		},
		{
			name: "turning Tailscale SSH off passes with no guard context and the ssh session over eth0", wantOK: true,
			cur:    lgPrefs(func(p *link.Prefs) { p.RunSSH = true }),
			change: lgPrefsChange(lgPrefs(func(p *link.Prefs) { p.RunSSH = true }), func(p *link.Prefs) { p.RunSSH = false }),
			w:      lgWorld{sockets: sshOverEth0, routes: map[string]string{"203.0.113.7": "eth0"}},
		},
		{
			name:   "turning Tailscale SSH off fails with no guard context and the ssh session over the tailnet",
			cur:    lgPrefs(func(p *link.Prefs) { p.RunSSH = true }),
			change: lgPrefsChange(lgPrefs(func(p *link.Prefs) { p.RunSSH = true }), func(p *link.Prefs) { p.RunSSH = false }),
			w:      lgWorld{sockets: sshOverTailnet, routes: map[string]string{"100.64.0.9": "tailscale0"}},
		},
		{
			name: "turning Tailscale SSH off passes when one of two ssh sessions is not over the tailnet", wantOK: true,
			cur:    lgPrefs(func(p *link.Prefs) { p.RunSSH = true }),
			change: lgPrefsChange(lgPrefs(func(p *link.Prefs) { p.RunSSH = true }), func(p *link.Prefs) { p.RunSSH = false }),
			w: lgWorld{sockets: slices.Concat(sshOverTailnet, lgSSH("192.0.2.1:22", "203.0.113.7:50000")[1:]),
				routes: map[string]string{"100.64.0.9": "tailscale0", "203.0.113.7": "eth0"}},
		},
		{
			name: "turning Tailscale SSH off passes when the panel request came over eth0", wantOK: true,
			cur:    lgPrefs(func(p *link.Prefs) { p.RunSSH = true }),
			change: lgPrefsChange(lgPrefs(func(p *link.Prefs) { p.RunSSH = true }), func(p *link.Prefs) { p.RunSSH = false }),
			w: lgWorld{controller: true, sockets: sshOverTailnet,
				routes: map[string]string{"100.64.0.9": "tailscale0", "203.0.113.7": "eth0"}},
			in: core.Guarded{Guard: panelCtx("203.0.113.7")},
		},
		{
			name:   "turning Tailscale SSH off fails when the panel request came over the tailnet",
			cur:    lgPrefs(func(p *link.Prefs) { p.RunSSH = true }),
			change: lgPrefsChange(lgPrefs(func(p *link.Prefs) { p.RunSSH = true }), func(p *link.Prefs) { p.RunSSH = false }),
			w: lgWorld{controller: true, sockets: sshOverEth0,
				routes: map[string]string{"100.64.0.9": "tailscale0", "203.0.113.7": "eth0"}},
			in: core.Guarded{Guard: panelCtx("100.64.0.9")},
		},
		{
			name:   "turning Tailscale SSH off fails when the guard context names no client",
			cur:    lgPrefs(func(p *link.Prefs) { p.RunSSH = true }),
			change: lgPrefsChange(lgPrefs(func(p *link.Prefs) { p.RunSSH = true }), func(p *link.Prefs) { p.RunSSH = false }),
			w:      lgWorld{controller: true, sockets: sshOverEth0, routes: map[string]string{"203.0.113.7": "eth0"}},
			in:     core.Guarded{Guard: &core.GuardContext{Complete: true}},
		},
		{
			name: "turning Tailscale SSH off passes when the request came through an exposure", wantOK: true,
			cur:    lgPrefs(func(p *link.Prefs) { p.RunSSH = true }),
			change: lgPrefsChange(lgPrefs(func(p *link.Prefs) { p.RunSSH = true }), func(p *link.Prefs) { p.RunSSH = false }),
			w: lgWorld{controller: true, sockets: sshOverTailnet,
				routes: map[string]string{"100.64.0.9": "tailscale0", "172.30.0.2": "dp-panel"}},
			in: core.Guarded{Guard: &core.GuardContext{Complete: true,
				Client: &core.Peer{Addr: netip.MustParseAddr("172.30.0.2")},
				Via: &core.ProxyPath{ExposureID: "e1", Kind: "npm", ProxyAddr: netip.MustParseAddr("172.30.0.2"),
					ListenerAddr: netip.MustParseAddrPort("172.30.0.1:8080")}}},
		},
		{
			name: "turning Tailscale SSH off passes when the agent link of a remote controller is over eth0", wantOK: true,
			cur:    lgPrefs(func(p *link.Prefs) { p.RunSSH = true }),
			change: lgPrefsChange(lgPrefs(func(p *link.Prefs) { p.RunSSH = true }), func(p *link.Prefs) { p.RunSSH = false }),
			w: lgWorld{sockets: []netstat.Socket{lgSock(netstat.StateEstablished, "192.0.2.1:50000", "203.0.113.9:7443", "darkpanel", lgAgentPID)},
				routes: map[string]string{"203.0.113.9": "eth0"}},
			in: core.Guarded{Guard: &core.GuardContext{Complete: true, PanelListen: lgAddrs("10.9.9.9:443")}},
		},
		{
			name:   "turning Tailscale SSH off fails when the agent link of a remote controller is over the tailnet",
			cur:    lgPrefs(func(p *link.Prefs) { p.RunSSH = true }),
			change: lgPrefsChange(lgPrefs(func(p *link.Prefs) { p.RunSSH = true }), func(p *link.Prefs) { p.RunSSH = false }),
			w: lgWorld{sockets: slices.Concat(sshOverEth0, []netstat.Socket{lgSock(netstat.StateEstablished, "100.64.0.1:50000", "100.64.0.7:7443", "darkpanel", lgAgentPID)}),
				routes: map[string]string{"100.64.0.7": "tailscale0", "203.0.113.7": "eth0"}},
			in: core.Guarded{Guard: &core.GuardContext{Complete: true, PanelListen: lgAddrs("10.9.9.9:443")}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, tsv := lgHost(t, tc.w)
			env := h.env("")
			env.Call.GuardRefresh = tc.refresh
			g, err := tsv.linkGuard(context.Background(), env, tc.in, tc.cur, tc.change)
			if err != nil {
				t.Fatal(err)
			}
			if g.ID != link.GuardID || !g.Overridable || g.Overridden {
				t.Errorf("guard %+v, want %s, overridable, not overridden", g, link.GuardID)
			}
			if g.OK != tc.wantOK {
				t.Fatalf("guard OK %v (%s), want %v", g.OK, g.Message, tc.wantOK)
			}
			for _, want := range tc.wantIn {
				if !strings.Contains(g.Message, want) {
					t.Errorf("message %q does not name %q", g.Message, want)
				}
			}
		})
	}
}

func TestLgLinkGuardHonoursOverrides(t *testing.T) {
	w := lgWorld{sockets: lgSSH("100.64.0.1:22", "100.64.0.9:50000"), routes: map[string]string{"100.64.0.9": "tailscale0"}}
	h, tsv := lgHost(t, w)
	g, err := tsv.linkGuard(context.Background(), h.env(""), core.Guarded{Overrides: []string{link.GuardID}}, lgPrefs(nil), lgLogout)
	if err != nil {
		t.Fatal(err)
	}
	if !g.OK || !g.Overridden || !g.Overridable || !strings.Contains(g.Message, "100.64.0.9") {
		t.Errorf("overridden guard %+v, want OK, overridden, overridable and the failing flow still named", g)
	}

	h, tsv = lgHost(t, lgWorld{sockets: lgSSH("192.0.2.1:22", "203.0.113.7:50000"), routes: map[string]string{"203.0.113.7": "eth0"}})
	g, err = tsv.linkGuard(context.Background(), h.env(""), core.Guarded{Overrides: []string{link.GuardID}}, lgPrefs(nil), lgLogout)
	if err != nil {
		t.Fatal(err)
	}
	if !g.OK || g.Overridden {
		t.Errorf("passing guard %+v, want OK and not overridden", g)
	}

	h, tsv = lgHost(t, w)
	g, err = tsv.linkGuard(context.Background(), h.env(""), core.Guarded{Overrides: []string{"firewall.lockout"}}, lgPrefs(nil), lgLogout)
	if err != nil {
		t.Fatal(err)
	}
	if g.OK || g.Overridden {
		t.Errorf("guard %+v with another guard overridden, want it failing", g)
	}
}

func TestLgLinkGuardReadsTheControllerUnit(t *testing.T) {
	w := lgWorld{sockets: lgControllerSession("100.64.0.1:7443", "100.64.0.5:41000"), routes: map[string]string{"100.64.0.5": "tailscale0"}}
	// With the controller unit absent its pid is not a controller, so its sockets are no flows.
	h, tsv := lgHost(t, w)
	g, err := tsv.linkGuard(context.Background(), h.env(""), core.Guarded{}, lgPrefs(nil), lgLogout)
	if err != nil || !g.OK {
		t.Errorf("guard %+v, error %v, want it passing without a controller", g, err)
	}
	w.controller = true
	h, tsv = lgHost(t, w)
	if g, err = tsv.linkGuard(context.Background(), h.env(""), core.Guarded{}, lgPrefs(nil), lgLogout); err != nil || g.OK {
		t.Errorf("guard %+v, error %v, want it failing with the controller running", g, err)
	}
	h, tsv = lgHost(t, w)
	h.units.States[controllerUnit] = core.UnitState{LoadState: "loaded", ActiveState: "inactive", SubState: "dead", MainPID: lgControllerPID}
	if g, err = tsv.linkGuard(context.Background(), h.env(""), core.Guarded{}, lgPrefs(nil), lgLogout); err != nil || !g.OK {
		t.Errorf("guard %+v, error %v, want it passing with the controller stopped", g, err)
	}
}

func TestLgLinkGuardRefusesFactsItCannotRead(t *testing.T) {
	h, tsv := lgHost(t, lgWorld{})
	env := h.env("")
	env.Units = nil
	if _, err := tsv.linkGuard(context.Background(), env, core.Guarded{}, lgPrefs(nil), lgLogout); err == nil {
		t.Error("the guard passed without the unit client")
	}
}

func TestLgCurrentLinkPrefs(t *testing.T) {
	cases := []struct {
		name string
		raw  tslocal.Prefs
		want link.Prefs
	}{
		{
			name: "every preference",
			raw: tslocal.Prefs{
				WantRunning: true, RouteAll: true, ShieldsUp: true, RunSSH: true, ExitNodeID: "n1",
				AdvertiseRoutes: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/24"), netip.MustParsePrefix("0.0.0.0/0"), netip.MustParsePrefix("::/0")},
			},
			want: link.Prefs{
				WantRunning: true, AcceptRoutes: true, ShieldsUp: true, RunSSH: true, ExitNode: "n1", AdvertiseExitNode: true,
				AdvertiseRoutes: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/24")},
			},
		},
		{
			name: "an exit node by address",
			raw:  tslocal.Prefs{ExitNodeIP: netip.MustParseAddr("100.64.0.9")},
			want: link.Prefs{ExitNode: "100.64.0.9"},
		},
		{
			name: "only the IPv6 exit route advertises an exit node",
			raw:  tslocal.Prefs{AdvertiseRoutes: []netip.Prefix{netip.MustParsePrefix("::/0")}},
			want: link.Prefs{AdvertiseExitNode: true},
		},
		{name: "nothing set", raw: tslocal.Prefs{}, want: link.Prefs{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newTSHost(t)
			raw := tc.raw
			h.local.Prefs = &raw
			got, back, err := h.ts().currentLinkPrefs(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if back == nil || !reflect.DeepEqual(*back, raw) {
				t.Errorf("the tailscaled preferences it returned are %+v, want %+v", back, raw)
			}
			if !slices.Equal(got.AdvertiseRoutes, tc.want.AdvertiseRoutes) {
				t.Errorf("advertised routes %v, want %v", got.AdvertiseRoutes, tc.want.AdvertiseRoutes)
			}
			got.AdvertiseRoutes, tc.want.AdvertiseRoutes = nil, nil
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("prefs %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestLgCurrentLinkPrefsReportsAFailedRead(t *testing.T) {
	h := newTSHost(t)
	h.local.Errs = map[string]error{"Prefs": errors.New("socket closed")}
	_, _, err := h.ts().currentLinkPrefs(context.Background())
	if err == nil || !strings.Contains(err.Error(), "socket closed") || !strings.HasPrefix(err.Error(), "tailscale:") {
		t.Errorf("error %v, want a tailscale: error wrapping the cause", err)
	}
}
