package guard

import (
	"context"
	"errors"
	"net/netip"
	"reflect"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
	"github.com/darkraise/darkpanel/internal/platform/netstat"
)

// ckV4 and ckV6 are distinct rulesets, so a test can see which one the
// evaluator was given.
var ckV4, ckV6 = &netfilter.Ruleset{}, &netfilter.Ruleset{}

// ckState is a checked state holding ckV4 and ckV6.
func ckState() fwstate.Loaded { return fwstate.Loaded{Variant: "nft", V4: ckV4, V6: ckV6} }

// ckEval drops a packet deny matches and accepts the rest, and fails the
// test when it gets the ruleset of the wrong family.
func ckEval(t *testing.T, deny func(p fwstate.Packet) bool) func(*netfilter.Ruleset, fwstate.Packet, fwstate.Router) fwstate.Result {
	return func(rs *netfilter.Ruleset, p fwstate.Packet, _ fwstate.Router) fwstate.Result {
		if (p.Family == fwstate.V4) != (rs == ckV4) {
			t.Errorf("packet %+v evaluated against the other family's ruleset", p)
		}
		if deny != nil && deny(p) {
			return fwstate.Result{Verdict: fwstate.Drop, Reason: "filter/ufw-user-input#1", Trace: []string{"filter/INPUT#0", "filter/ufw-user-input#1"}}
		}
		return fwstate.Result{Verdict: fwstate.Accept}
	}
}

// ckEnv is gEnv with rp_filter 0 for the dp-panel bridge of ckIfaces too.
func ckEnv() (core.Env, *gFiles, *gUnits) {
	env, files, units := gEnv()
	files.sysctl["net/ipv4/conf/dp-panel/rp_filter"] = "0"
	return env, files, units
}

// ckIfaces are gIfaces with the controller's up dp-panel bridge.
func ckIfaces() []netstat.Interface {
	return append(gIfaces(), netstat.Interface{Index: 6, Name: "dp-panel", Up: true, Running: true,
		Addrs: []netip.Prefix{netip.MustParsePrefix("172.30.0.1/16")}})
}

// ckHost is a host with an SSH session, a running local controller, the
// agent's own link to a controller, and tailscaled.
func ckHost() *gHost {
	return &gHost{
		ifaces: ckIfaces(),
		pid:    99,
		sockets: []netstat.Socket{
			flSock(netstat.ProtoTCP, netstat.StateListen, "0.0.0.0:22", "", "sshd", 10),
			flSock(netstat.ProtoTCP, netstat.StateEstablished, "192.0.2.1:22", "203.0.113.7:50000", "sshd-session", 11),
			flSock(netstat.ProtoTCP, netstat.StateEstablished, "192.0.2.1:40100", "198.51.100.9:7443", "darkpanel-agent", 99),
			flSock(netstat.ProtoUDP, netstat.StateClose, "0.0.0.0:41641", "", "tailscaled", 300),
		},
		routes: map[netstat.RouteQuery]netstat.Route{
			rtQuery("198.51.100.9", "192.0.2.1", ""):          rtVia("eth0", "192.0.2.1"),
			rtQuery("198.51.100.1", "", ""):                   rtVia("eth0", "192.0.2.1"),
			rtQuery("198.51.100.1", "172.30.0.3", "dp-panel"): rtVia("eth0", ""),
		},
	}
}

// ckInput is a plan from a direct client and an apply refresh that came
// through a tunnel exposure.
func ckInput(t *testing.T, deny func(p fwstate.Packet) bool) Input {
	return Input{
		Plan: &core.GuardContext{
			Client:      &core.Peer{Addr: netip.MustParseAddr("203.0.113.7"), Port: 51000},
			PanelListen: flAddrPorts("192.0.2.1:443"),
			AgentListen: flAddrPorts("192.0.2.1:7443"),
			AgentPeers:  []netip.Addr{netip.MustParseAddr("198.51.100.7")},
			Complete:    true,
		},
		Refresh: &core.GuardContext{
			Via: &core.ProxyPath{ExposureID: "exp2", Kind: "tunnel", ProxyAddr: netip.MustParseAddr("172.30.0.3"),
				ListenerAddr: netip.MustParseAddrPort("172.30.0.1:8080")},
			Complete: true,
		},
		Eval: ckEval(t, deny),
	}
}

// ckAllFlows are the flows of ckHost and ckInput, in order.
var ckAllFlows = []string{
	"ssh from 203.0.113.7 to 192.0.2.1:22 on eth0",
	"ssh from 203.0.113.7 to 192.0.2.1:22 on eth1",
	"ssh from 203.0.113.7 to 192.0.2.1:22 on dp-panel",
	"panel from 203.0.113.7 to 192.0.2.1:443 on eth0",
	"panel from 203.0.113.7 to 192.0.2.1:443 on eth1",
	"panel from 203.0.113.7 to 192.0.2.1:443 on dp-panel",
	"exposure exp2 proxy from 172.30.0.3 to 172.30.0.1:8080 on dp-panel",
	"exposure exp2 tunnel udp from 172.30.0.3 to 198.51.100.1:7844 on dp-panel out eth0",
	"exposure exp2 tunnel from 172.30.0.3 to 198.51.100.1:7844 on dp-panel out eth0",
	"exposure exp2 tunnel from 172.30.0.3 to 198.51.100.1:443 on dp-panel out eth0",
	"agent link from 198.51.100.7 to 192.0.2.1:7443 on eth0",
	"agent link from 198.51.100.7 to 192.0.2.1:7443 on eth1",
	"agent link from 198.51.100.7 to 192.0.2.1:7443 on dp-panel",
	"controller link from 192.0.2.1 to 198.51.100.9:7443 out eth0",
	"tailscale udp from 192.0.2.1 to 198.51.100.1:41641 out eth0",
	"tailscale from 192.0.2.1 to 198.51.100.1:443 out eth0",
}

// ckRunning makes the controller unit of env run with main PID 4242.
func ckRunning(env core.Env) {
	env.Units.(*gUnits).state = core.UnitState{LoadState: "loaded", ActiveState: "active", SubState: "running", MainPID: 4242}
}

// ckFailing lists the flows of results that were not accepted.
func ckFailing(results []FlowResult) []string {
	var out []string
	for _, r := range results {
		if r.Result.Verdict != fwstate.Accept {
			out = append(out, r.Flow)
		}
	}
	return out
}

func TestCheckAllPass(t *testing.T) {
	env, _, _ := ckEnv()
	ckRunning(env)
	g, results, err := Checker{Host: ckHost()}.Check(context.Background(), env, ckState(), ckInput(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	var flows []string
	for _, r := range results {
		flows = append(flows, r.Flow)
	}
	if !reflect.DeepEqual(flows, ckAllFlows) {
		t.Errorf("flows =\n%q\nwant\n%q", flows, ckAllFlows)
	}
	want := core.Guard{ID: "firewall.lockout", OK: true, Message: "every protected flow passes (16 checked)"}
	if g != want {
		t.Errorf("guard = %+v, want %+v", g, want)
	}
}

func TestCheckEachFlowRefusingAlone(t *testing.T) {
	for _, tc := range []struct {
		name string
		deny func(p fwstate.Packet) bool
		want []string
	}{
		{"ssh", func(p fwstate.Packet) bool { return p.DstPort == 22 }, ckAllFlows[0:3]},
		{"panel on one interface", func(p fwstate.Packet) bool { return p.DstPort == 443 && p.InIface == "eth1" }, ckAllFlows[4:5]},
		{"exposure proxy", func(p fwstate.Packet) bool { return p.DstPort == 8080 }, ckAllFlows[6:7]},
		{"tunnel over udp", func(p fwstate.Packet) bool { return p.DstPort == 7844 && p.Proto == "udp" }, ckAllFlows[7:8]},
		{"agent link", func(p fwstate.Packet) bool { return p.DstPort == 7443 && p.Path == "input" }, ckAllFlows[10:13]},
		{"controller link", func(p fwstate.Packet) bool { return p.DstPort == 7443 && p.Path == "output" }, ckAllFlows[13:14]},
		{"tailscale", func(p fwstate.Packet) bool { return p.DstPort == 41641 }, ckAllFlows[14:15]},
		{"a new connection has no source port", func(p fwstate.Packet) bool {
			observed := p.Path == "output" && (p.Proto == "udp" || p.DstPort == 7443)
			return p.SrcPort != 0 && !observed
		}, nil},
		{"an observed source port is kept", func(p fwstate.Packet) bool {
			return p.Path == "output" && (p.Proto == "udp" && p.SrcPort != 41641 || p.DstPort == 7443 && p.SrcPort != 40100)
		}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, _, _ := ckEnv()
			ckRunning(env)
			g, results, err := Checker{Host: ckHost()}.Check(context.Background(), env, ckState(), ckInput(t, tc.deny))
			if err != nil {
				t.Fatal(err)
			}
			if got := ckFailing(results); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("failing flows = %q, want %q", got, tc.want)
			}
			parts := make([]string, len(tc.want))
			for i, f := range tc.want {
				parts[i] = f + ": drop (filter/ufw-user-input#1)"
			}
			want := core.Guard{ID: "firewall.lockout", Message: "the change would cut " + strings.Join(parts, "; ")}
			if len(tc.want) == 0 {
				want = core.Guard{ID: "firewall.lockout", OK: true, Message: "every protected flow passes (16 checked)"}
			}
			if g != want {
				t.Errorf("guard = %+v\nwant %+v", g, want)
			}
		})
	}
}

func TestCheckStrictRPFilter(t *testing.T) {
	denyEth1 := func(p fwstate.Packet) bool { return p.InIface == "eth1" }
	host := func() *gHost {
		return &gHost{ifaces: gIfaces(), sockets: ckHost().sockets[:2], routes: map[netstat.RouteQuery]netstat.Route{
			rtQuery("203.0.113.7", "192.0.2.1", ""): rtVia("eth0", "192.0.2.1"),
		}}
	}
	env, files, _ := ckEnv()
	g, _, err := Checker{Host: host()}.Check(context.Background(), env, ckState(), Input{Eval: ckEval(t, denyEth1)})
	if err != nil {
		t.Fatal(err)
	}
	if g.OK {
		t.Fatalf("with rp_filter 0 the flow on eth1 counts: %+v", g)
	}
	files.sysctl["net/ipv4/conf/all/rp_filter"] = "1"
	g, results, err := Checker{Host: host()}.Check(context.Background(), env, ckState(), Input{Eval: ckEval(t, denyEth1)})
	if err != nil {
		t.Fatal(err)
	}
	if !g.OK || len(results) != 1 || results[0].Flow != "ssh from 203.0.113.7 to 192.0.2.1:22 on eth0" {
		t.Errorf("strict rp_filter: guard %+v, results %+v; want only eth0, passing", g, results)
	}
}

func TestCheckUnresolvableRoute(t *testing.T) {
	env, _, _ := ckEnv()
	ckRunning(env)
	h := ckHost()
	delete(h.routes, rtQuery("198.51.100.9", "192.0.2.1", ""))
	g, results, err := Checker{Host: h}.Check(context.Background(), env, ckState(), ckInput(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	want := "controller link from 192.0.2.1 to 198.51.100.9:7443: unknown (the route to 198.51.100.9 cannot be resolved: netstat: no route)"
	if g.OK || g.Message != "the change would cut "+want {
		t.Errorf("guard = %+v, want it failed with %q", g, want)
	}
	if got := ckFailing(results); len(got) != 1 {
		t.Errorf("failing flows = %q, want one", got)
	}
}

func TestCheckIncompleteTopology(t *testing.T) {
	env, _, _ := ckEnv()
	ckRunning(env)
	in := ckInput(t, nil)
	in.Plan.Complete = false
	g, results, err := Checker{Host: ckHost()}.Check(context.Background(), env, ckState(), in)
	if err != nil {
		t.Fatal(err)
	}
	want := core.Guard{ID: "firewall.lockout", Message: "the change would cut controller topology: unknown (the controller's topology is incomplete)"}
	if g != want {
		t.Errorf("guard = %+v, want %+v", g, want)
	}
	if len(results) != len(ckAllFlows)+1 {
		t.Errorf("%d results, want every flow still evaluated besides the topology", len(results))
	}
}

func TestCheckBreakGlass(t *testing.T) {
	env, _, _ := ckEnv()
	g, results, err := Checker{Host: ckHost()}.Check(context.Background(), env, ckState(), Input{Eval: ckEval(t, nil)})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{ckAllFlows[0], ckAllFlows[1], ckAllFlows[2], ckAllFlows[13], ckAllFlows[14], ckAllFlows[15]}
	var got []string
	for _, r := range results {
		got = append(got, r.Flow)
	}
	if !g.OK || !reflect.DeepEqual(got, want) {
		t.Errorf("break-glass: guard %+v, flows %q; want live flows only %q", g, got, want)
	}
}

func TestCheckRetiredVia(t *testing.T) {
	env, _, _ := ckEnv()
	ckRunning(env)
	in := ckInput(t, func(p fwstate.Packet) bool { return p.DstPort == 8080 || p.DstPort == 7844 })
	in.Retired = []string{"exp2"}
	g, results, err := Checker{Host: ckHost()}.Check(context.Background(), env, ckState(), in)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if strings.HasPrefix(r.Flow, "exposure") {
			t.Errorf("a retired exposure was evaluated: %s", r.Flow)
		}
	}
	if !g.OK {
		t.Errorf("guard = %+v, want OK", g)
	}
}

func TestCheckNoIPv6Ruleset(t *testing.T) {
	env, _, _ := ckEnv()
	h := &gHost{ifaces: gIfaces(), sockets: []netstat.Socket{
		flSock(netstat.ProtoTCP, netstat.StateListen, "[::]:22", "", "sshd", 10),
		flSock(netstat.ProtoTCP, netstat.StateEstablished, "[2001:db8::1]:22", "[2001:db8:9::9]:50000", "sshd", 11),
	}}
	st := ckState()
	st.V6 = nil
	g, _, err := Checker{Host: h}.Check(context.Background(), env, st, Input{Eval: ckEval(t, nil)})
	if err != nil {
		t.Fatal(err)
	}
	if g.OK || !strings.Contains(g.Message, "ssh from 2001:db8:9::9 to [2001:db8::1]:22 on eth0: unknown (no v6 ruleset was read)") {
		t.Errorf("guard = %+v, want the IPv6 flow unknown", g)
	}
}

func TestCheckRouterGiven(t *testing.T) {
	env, _, _ := ckEnv()
	ckRunning(env)
	h := &gHost{ifaces: ckIfaces(), routes: map[netstat.RouteQuery]netstat.Route{
		rtQuery("172.17.0.5", "198.51.100.1", "eth0"):     rtVia("docker0", ""),
		rtQuery("172.17.0.5", "198.51.100.1", "dp-panel"): rtVia("docker0", ""),
	}}
	in := Input{
		Plan: &core.GuardContext{Via: &core.ProxyPath{ExposureID: "exp1", Kind: "npm", ProxyAddr: netip.MustParseAddr("172.30.0.2"),
			ListenerAddr: netip.MustParseAddrPort("172.30.0.1:8080"), Public: flAddrPorts("192.0.2.1:80")}, Complete: true},
		Eval: func(_ *netfilter.Ruleset, p fwstate.Packet, route fwstate.Router) fwstate.Result {
			if p.DstPort != 80 {
				return fwstate.Result{Verdict: fwstate.Accept}
			}
			p.Dst = netip.MustParseAddr("172.17.0.5")
			path, oif, err := route(p)
			switch {
			case err != nil:
				return fwstate.Result{Verdict: fwstate.Unknown, Reason: err.Error()}
			case path == "forward" && oif == "docker0":
				return fwstate.Result{Verdict: fwstate.Accept}
			}
			return fwstate.Result{Verdict: fwstate.Drop}
		},
	}
	g, results, err := Checker{Host: h}.Check(context.Background(), env, ckState(), in)
	if err != nil {
		t.Fatal(err)
	}
	want := "exposure exp1 public from 198.51.100.1 to 192.0.2.1:80 on eth1: unknown (firewall: route to 172.17.0.5 from 198.51.100.1 on eth1: netstat: no route)"
	if g.OK || g.Message != "the change would cut "+want {
		t.Errorf("guard = %+v, want only eth1 failing with %q", g, want)
	}
	if len(results) != 4 {
		t.Errorf("%d results, want the proxy flow and the public flow on three interfaces", len(results))
	}
}

func TestCheckErrors(t *testing.T) {
	env, _, _ := ckEnv()
	if _, _, err := (Checker{Host: ckHost()}).Check(context.Background(), env, ckState(), Input{}); err == nil {
		t.Error("Check without an evaluator succeeded")
	}
	h := ckHost()
	h.socketsErr = netstat.ErrNoSocketTables
	g, results, err := Checker{Host: h}.Check(context.Background(), env, ckState(), Input{Eval: ckEval(t, nil)})
	if !errors.Is(err, netstat.ErrNoSocketTables) {
		t.Errorf("Check on a host without socket tables: %v, want ErrNoSocketTables", err)
	}
	if g != (core.Guard{}) || results != nil {
		t.Errorf("a failed Check returned a guard %+v and results %+v", g, results)
	}
}

func TestVerdict(t *testing.T) {
	if g := verdict(nil); g != (core.Guard{ID: "firewall.lockout", OK: true, Message: "no protected flow to check"}) {
		t.Errorf("no flows: %+v", g)
	}
	g := verdict([]FlowResult{
		{Flow: "a", Result: fwstate.Result{Verdict: fwstate.Accept}},
		{Flow: "b", Result: fwstate.Result{Verdict: fwstate.Reject, Reason: "filter/ufw-user-input#2"}},
		{Flow: "c", Result: fwstate.Result{Verdict: fwstate.Unknown, Reason: "limit on a deciding rule"}},
		{Flow: "d", Result: fwstate.Result{}},
	})
	want := core.Guard{ID: "firewall.lockout",
		Message: "the change would cut b: reject (filter/ufw-user-input#2); c: unknown (limit on a deciding rule); d: no verdict"}
	if g != want {
		t.Errorf("verdict = %+v\nwant %+v", g, want)
	}
}
