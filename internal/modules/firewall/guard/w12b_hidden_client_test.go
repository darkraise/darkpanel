package guard

import (
	"context"
	"net/netip"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/firewall/eval"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/netstat"
)

// w12bRoutes are v1r21HostRoutes with an IPv6 default route on eth0.
func w12bRoutes() []netstat.Route {
	return append(v1r21HostRoutes(), v1r21Route("unicast", "::/0", "eth0", "2001:db8::1", 254))
}

// w12bCheck runs the guard of a request whose NPM exposure on bind named
// client, against st, with before as the state the change starts from (nil
// for a revert's guard) and rpFilter as net/ipv4/conf/all/rp_filter.
func w12bCheck(t *testing.T, h Host, before *fwstate.Loaded, st fwstate.Loaded, bind, client, rpFilter string) core.Guard {
	t.Helper()
	env, files, _ := ckEnv()
	value := rpFilter
	if value == "" {
		value = "0"
	}
	for _, name := range []string{"docker0", "tailscale0"} {
		files.sysctl["net/ipv4/conf/"+name+"/rp_filter"] = value
	}
	if rpFilter != "" {
		files.sysctl["net/ipv4/conf/all/rp_filter"] = rpFilter
		st.Sysctl = map[string]string{"net/ipv4/conf/all/rp_filter": rpFilter}
		if before != nil {
			b := *before
			b.Sysctl = st.Sysctl
			before = &b
		}
	}
	ckRunning(env)
	g, _, err := Checker{Host: h}.Check(context.Background(), env, st, Input{Plan: w12aClientPlan(bind, client), Before: before, Eval: eval.Evaluate})
	if err != nil {
		t.Fatal(err)
	}
	return g
}

const (
	w12bDropPrivate4 = "-A FORWARD -s 10.44.0.0/16 -d 172.30.0.2/32 -p tcp -m tcp --dport 443 -j DROP"
	w12bDropULA      = "-A INPUT -s fd00:d0::/64 -p tcp -m tcp --dport 443 -j DROP"
	w12bOtherPort    = "-A INPUT -p tcp -m tcp --dport 2222 -j ACCEPT"
)

func TestW12bHiddenNPMClient(t *testing.T) {
	h := v1r21Fib{&gHost{ifaces: ckIfaces(), table: w12bRoutes()}}
	for _, tc := range []struct {
		name, bind, client string
		before, after      [2][]string // IPv4 and IPv6 filter rules
		noBefore           bool        // a revert's guard: no before-state
		want               string      // "" when the change must pass
	}{
		{"a new drop of a ULA range on the IPv6 leg", "[::]:443", "172.30.0.1",
			[2][]string{nil, nil}, [2][]string{nil, {w12bDropULA}}, false, "exposure exp1 hidden client from fd00:d0::-"},
		{"a new drop of a private range on an IPv4 binding", "0.0.0.0:443", "172.30.0.1",
			[2][]string{nil, nil}, [2][]string{{w12bDropPrivate4}, nil}, false, "exposure exp1 hidden client from 10.44.0.0-10.44.255.255"},
		{"a new drop of a private range on a concrete host-address binding", "172.30.0.1:443", "172.30.0.1",
			[2][]string{nil, nil}, [2][]string{{w12bDropPrivate4}, nil}, false, "exposure exp1 hidden client from 10.44.0.0-10.44.255.255"},
		{"a change that blocks nothing on a concrete host-address binding", "172.30.0.1:443", "172.30.0.1",
			[2][]string{nil, nil}, [2][]string{{w12bOtherPort}, nil}, false, ""},
		{"a loopback client", "0.0.0.0:443", "127.0.0.1",
			[2][]string{nil, nil}, [2][]string{{w12bDropPrivate4}, nil}, false, "exposure exp1 hidden client from 10.44.0.0-10.44.255.255"},
		{"a range the rules already dropped", "0.0.0.0:443", "172.30.0.1",
			[2][]string{{w12bDropPrivate4}, nil}, [2][]string{{w12bDropPrivate4, w12bOtherPort}, nil}, false, ""},
		{"a revert's guard with a range the host already dropped", "0.0.0.0:443", "172.30.0.1",
			[2][]string{nil, nil}, [2][]string{{w12bDropPrivate4}, nil}, true, "exposure exp1 hidden client from 10.44.0.0-10.44.255.255"},
		{"a new drop of a public range on the DNAT path", "0.0.0.0:443", "172.30.0.1",
			[2][]string{nil, nil}, [2][]string{{"-A FORWARD -s 45.0.0.0/16 -d 172.30.0.2/32 -p tcp -m tcp --dport 443 -j DROP"}, nil}, false, "exposure exp1 public from 45.0.0.0-45.0.255.255"},
		// The hidden-client flow judges a public source as the public leg does:
		// a public range the rules already dropped refuses neither.
		{"a public range the rules already dropped on the DNAT path", "0.0.0.0:443", "172.30.0.1",
			[2][]string{{"-A FORWARD -s 45.0.0.0/16 -d 172.30.0.2/32 -p tcp -m tcp --dport 443 -j DROP"}, nil},
			[2][]string{{"-A FORWARD -s 45.0.0.0/16 -d 172.30.0.2/32 -p tcp -m tcp --dport 443 -j DROP", w12bOtherPort}, nil}, false, ""},
		{"a client that is not a host address gets no hidden-client flow", "0.0.0.0:443", "10.0.0.50",
			[2][]string{nil, nil}, [2][]string{{w12bDropPrivate4}, nil}, false, ""},
		{"a change that blocks nothing on the binding", "[::]:443", "172.30.0.1",
			[2][]string{nil, nil}, [2][]string{{w12bOtherPort}, {w12bOtherPort}}, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var before *fwstate.Loaded
			if !tc.noBefore {
				b := v1r21State(t, w11b09Nat+v1r21Table(tc.before[0]...), v1r21Table(tc.before[1]...))
				before = &b
			}
			st := v1r21State(t, w11b09Nat+v1r21Table(tc.after[0]...), v1r21Table(tc.after[1]...))
			g := w12bCheck(t, h, before, st, tc.bind, tc.client, "")
			switch {
			case tc.want == "" && !g.OK:
				t.Errorf("guard = %+v, want OK", g)
			case tc.want != "" && (g.OK || !strings.Contains(g.Message, tc.want)):
				t.Errorf("guard = %+v, want a refusal containing %q", g, tc.want)
			}
		})
	}
}

// TestW12bHiddenClientOnABusyHost: Docker bridges, a tailnet interface, ULA
// and link-local addresses and strict reverse-path filtering, with kept rules
// that name the host's own tailnet addresses, leave a change that blocks
// nothing on the binding alone in both families, while a new drop of a ULA
// range is still refused.
func TestW12bHiddenClientOnABusyHost(t *testing.T) {
	ifaces := append(ckIfaces(),
		netstat.Interface{Index: 7, Name: "docker0", Up: true, Running: true, Addrs: []netip.Prefix{netip.MustParsePrefix("172.17.0.1/16")}},
		netstat.Interface{Index: 8, Name: "tailscale0", Up: true, Running: true, Addrs: []netip.Prefix{
			netip.MustParsePrefix("100.101.102.103/32"), netip.MustParsePrefix("fd7a:115c:a1e0::5/128")}},
	)
	ifaces[1].Addrs = append(ifaces[1].Addrs, netip.MustParsePrefix("fd00:d0::10/64"), netip.MustParsePrefix("fe80::1/64"))
	table := append(w12bRoutes(),
		v1r21Route("unicast", "172.17.0.0/16", "docker0", "172.17.0.1", 254),
		v1r21Route("unicast", "100.64.0.0/10", "tailscale0", "100.101.102.103", 254),
		v1r21Route("local", "172.17.0.1/32", "docker0", "172.17.0.1", 255),
		v1r21Route("local", "100.101.102.103/32", "tailscale0", "100.101.102.103", 255),
	)
	h := v1r21Fib{&gHost{ifaces: ifaces, table: table}}
	kept4 := "-A INPUT -s 100.101.102.103/32 -j ACCEPT"
	kept6 := "-A INPUT -s fd7a:115c:a1e0::5/128 -j ACCEPT"
	before := v1r21State(t, w11b09Nat+v1r21Table(kept4), v1r21Table(kept6))
	pass := v1r21State(t, w11b09Nat+v1r21Table(kept4, w12bOtherPort), v1r21Table(kept6, w12bOtherPort))
	if g := w12bCheck(t, h, &before, pass, "[::]:443", "172.30.0.1", "1"); !g.OK {
		t.Errorf("guard = %+v, want OK: the change blocks no source on the binding", g)
	}
	cut := v1r21State(t, w11b09Nat+v1r21Table(kept4), v1r21Table(kept6, w12bDropULA))
	if g := w12bCheck(t, h, &before, cut, "[::]:443", "172.30.0.1", "1"); g.OK || !strings.Contains(g.Message, "exposure exp1 hidden client from fd00:d0::") {
		t.Errorf("guard = %+v, want a refusal of the hidden client from the ULA range", g)
	}
}

// 2300 bans exceed the evaluation limit (cells times rules), the limit a
// hidden-client flow over many rules meets first.
func TestW12bHiddenClientOverTheCellLimitIsUnknown(t *testing.T) {
	h := v1r21Fib{&gHost{ifaces: ckIfaces(), table: w12bRoutes()}}
	st := v1r21State(t, w11b09Nat+v1r21Table(v1r21Bans(2300, "FORWARD", " -d 172.30.0.2/32 -p tcp -m tcp --dport 443")...), v1r21Table())
	before := st
	g := w12bCheck(t, h, &before, st, "0.0.0.0:443", "172.30.0.1", "")
	if g.OK || !strings.Contains(g.Message, "exposure exp1 hidden client: unknown (the rules and routes split the unicast address space into ") {
		t.Errorf("guard = %+v, want the hidden-client flow unknown over the cell limit", g)
	}
}
