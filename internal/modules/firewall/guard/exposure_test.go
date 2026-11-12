package guard

import (
	"context"
	"net/netip"
	"reflect"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/platform/netstat"
)

// v1r19Path is an NPM exposure whose record names no public binding.
func v1r19Path() *core.ProxyPath {
	return &core.ProxyPath{ExposureID: "exp1", Kind: "npm", ProxyAddr: netip.MustParseAddr("172.30.0.2"),
		ListenerAddr: netip.MustParseAddrPort("172.30.0.1:8080")}
}

func TestV1r19NPMExposureWithoutPublicBindingIsUnknown(t *testing.T) {
	in := Input{Plan: &core.GuardContext{Via: v1r19Path(), Complete: true}}
	got := buildFlows(in, facts{ifaces: gIfaces(), controller: 4242})
	want := []flow{
		{label: "exposure exp1 proxy", pkt: flOn("dp-panel", flPkt("input", "172.30.0.2", 0, "172.30.0.1", 8080, "tcp"))},
		{label: "exposure exp1 public", unknown: "the exposure has no public bindings"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("flows\n%+v\nwant\n%+v", got, want)
	}
}

func TestV1r19CheckRefusesAnNPMExposureWithoutPublicBinding(t *testing.T) {
	env, _, _ := ckEnv()
	ckRunning(env)
	in := Input{Plan: &core.GuardContext{Via: v1r19Path(), Complete: true}, Eval: ckEval(t, nil)}
	g, _, err := Checker{Host: &gHost{ifaces: ckIfaces()}}.Check(context.Background(), env, ckState(), in)
	if err != nil {
		t.Fatal(err)
	}
	want := core.Guard{ID: GuardID, Message: "the change would cut exposure exp1 public: unknown (the exposure has no public bindings)"}
	if g != want {
		t.Errorf("guard = %+v, want %+v", g, want)
	}
}

const (
	w11b09Nat = "*nat\n:PREROUTING ACCEPT [0:0]\n:INPUT ACCEPT [0:0]\n:OUTPUT ACCEPT [0:0]\n:POSTROUTING ACCEPT [0:0]\n" +
		"-A PREROUTING -p tcp -m tcp --dport 443 -j DNAT --to-destination 172.30.0.2:443\nCOMMIT\n"
	w11b09Drop = "-A FORWARD -d 172.30.0.2/32 -p tcp -m tcp --dport 443 -j DROP"
)

func w11b09Plan(bind string) *core.GuardContext {
	path := v1r19Path()
	path.Public = []netip.AddrPort{netip.MustParseAddrPort(bind)}
	return &core.GuardContext{Via: path, Complete: true}
}

func TestW11b09BlockedPublicBindingIsRefused(t *testing.T) {
	const want = "exposure exp1 public from"
	for _, bind := range []string{"0.0.0.0:443", "[::]:443"} {
		t.Run(bind, func(t *testing.T) {
			check := func(rules ...string) core.Guard {
				t.Helper()
				h := v1r21Fib{&gHost{ifaces: ckIfaces(), table: v1r21HostRoutes()}}
				return v1r21CheckHost(t, h, v1r21State(t, w11b09Nat+v1r21Table(rules...), v1r21Table()), w11b09Plan(bind))
			}
			if g := check(w11b09Drop); g.OK || !strings.Contains(g.Message, want) {
				t.Errorf("guard = %+v, want a refusal containing %q", g, want)
			}
			if g := check(); !g.OK {
				t.Errorf("guard = %+v, want OK without the drop", g)
			}
		})
	}
}

func TestW11b09IPv6LegOfADualStackBinding(t *testing.T) {
	const v6Drop = "-A INPUT -p tcp -m tcp --dport 443 -j DROP"
	check := func(table []netstat.Route, v6 ...string) core.Guard {
		t.Helper()
		h := v1r21Fib{&gHost{ifaces: ckIfaces(), table: table}}
		return v1r21CheckHost(t, h, v1r21State(t, w11b09Nat+v1r21Table(), v1r21Table(v6...)), w11b09Plan("[::]:443"))
	}
	withRoute := append(v1r21HostRoutes(), v1r21Route("unicast", "::/0", "eth0", "2001:db8::1", 254))
	const want = "exposure exp1 public from 2000::-3fff:ffff:ffff:ffff:ffff:ffff:ffff:ffff to [2001:db8::1]:443 on eth0: drop"
	if g := check(withRoute, v6Drop); g.OK || !strings.Contains(g.Message, want) {
		t.Errorf("guard = %+v, want a refusal containing %q", g, want)
	}
	if g := check(withRoute); !g.OK {
		t.Errorf("guard = %+v, want OK without the IPv6 drop", g)
	}
	if g := check(v1r21HostRoutes(), v6Drop); !g.OK {
		t.Errorf("guard = %+v, want OK: a host without an IPv6 route has nothing to protect on that leg", g)
	}
}

// w12aClientPlan is w11b09Plan(bind) of a request whose NPM exposure
// named client in its client header.
func w12aClientPlan(bind, client string) *core.GuardContext {
	g := w11b09Plan(bind)
	g.Client = &core.Peer{Addr: netip.MustParseAddr(client)}
	return g
}

func TestW12aBlockedNPMClientOutsideTheInternetCellsIsRefused(t *testing.T) {
	routes := append(v1r21HostRoutes(), v1r21Route("unicast", "::/0", "eth0", "2001:db8::1", 254))
	for _, tc := range []struct {
		name, bind, client string
		v4, v6             []string // the drop that cuts the client, in each family's filter table
		want               string   // "" when the change must pass
	}{
		{"an RFC 1918 client", "0.0.0.0:443", "10.0.0.50",
			[]string{"-A FORWARD -s 10.0.0.50/32 -d 172.30.0.2/32 -p tcp -m tcp --dport 443 -j DROP"}, nil,
			"exposure exp1 client from 10.0.0.50 to 10.0.0.1:443 on eth1: drop"},
		{"a CGNAT client on the IPv4 leg of a dual-stack binding", "[::]:443", "100.101.102.103",
			[]string{"-A FORWARD -s 100.101.102.103/32 -d 172.30.0.2/32 -p tcp -m tcp --dport 443 -j DROP"}, nil,
			"exposure exp1 client from 100.101.102.103 to 192.0.2.1:443 on eth0: drop"},
		{"a ULA client on the IPv6 leg of a dual-stack binding", "[::]:443", "fd7a:115c:a1e0::5",
			nil, []string{"-A INPUT -s fd7a:115c:a1e0::5/128 -p tcp -m tcp --dport 443 -j DROP"},
			"exposure exp1 client from fd7a:115c:a1e0::5 to [2001:db8::1]:443 on eth0: drop"},
		{"an IPv6 client of an IPv4-only binding", "0.0.0.0:443", "fd7a:115c:a1e0::5",
			nil, []string{"-A INPUT -s fd7a:115c:a1e0::5/128 -p tcp -m tcp --dport 443 -j DROP"}, ""},
		// Docker's userland proxy names the bridge gateway, an address of this host.
		{"a client address of this host", "0.0.0.0:443", "172.30.0.1",
			[]string{"-A FORWARD -s 172.30.0.1/32 -d 172.30.0.2/32 -p tcp -m tcp --dport 443 -j DROP"}, nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			check := func(v4, v6 []string) core.Guard {
				t.Helper()
				h := v1r21Fib{&gHost{ifaces: ckIfaces(), table: routes}}
				return v1r21CheckHost(t, h, v1r21State(t, w11b09Nat+v1r21Table(v4...), v1r21Table(v6...)), w12aClientPlan(tc.bind, tc.client))
			}
			g := check(tc.v4, tc.v6)
			switch {
			case tc.want == "" && !g.OK:
				t.Errorf("guard = %+v, want OK: no client flow applies", g)
			case tc.want != "" && (g.OK || !strings.Contains(g.Message, tc.want)):
				t.Errorf("guard = %+v, want a refusal containing %q", g, tc.want)
			}
			if g := check(nil, nil); !g.OK {
				t.Errorf("guard = %+v, want OK without the drop", g)
			}
		})
	}
}
