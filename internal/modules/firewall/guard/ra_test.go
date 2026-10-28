package guard

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"syscall"
	"testing"

	"github.com/darkraise/darkpanel/internal/platform/netstat"
)

// gxDefault6 is a default IPv6 route of the given protocol out of eth0.
func gxDefault6(proto uint8) netstat.Route {
	return netstat.Route{Type: "unicast", Table: 254, Protocol: proto, Dst: netip.MustParsePrefix("::/0"),
		Gateway: netip.MustParseAddr("fe80::1"), OutIface: "eth0"}
}

// gxPrefix6 is a connected IPv6 network route of eth0.
func gxPrefix6() netstat.Route {
	return netstat.Route{Type: "unicast", Table: 254, Protocol: netstat.RouteProtoKernel, Dst: netip.MustParsePrefix("2001:db8::/64"), OutIface: "eth0"}
}

func TestGxCheckIPv6Forwarding(t *testing.T) {
	const all, eth0 = "net/ipv6/conf/all/forwarding", "net/ipv6/conf/eth0/forwarding"
	ra := []netstat.Route{gxPrefix6(), gxDefault6(netstat.RouteProtoRA)}
	failed := func(key, from, to string) string {
		return "the change would cut IPv6 router advertisements: unknown (" + key + " changes from " + from + " to " + to +
			" and the default IPv6 route was learned from router advertisements, which an interface stops accepting while it forwards)"
	}
	for _, tc := range []struct {
		name      string
		routes    []netstat.Route
		live, new map[string]string
		message   string // "": the guard passes
		dumps     int
	}{
		{"forwarding switched on", ra, map[string]string{all: "0"}, map[string]string{all: "1"}, failed(all, "0", "1"), 1},
		{"forwarding switched off", ra, map[string]string{all: "1"}, map[string]string{all: "0"}, failed(all, "1", "0"), 1},
		{"one interface's forwarding", ra, map[string]string{all: "0", eth0: "0"}, map[string]string{all: "0", eth0: "1"}, failed(eth0, "0", "1"), 1},
		{"no change", ra, map[string]string{all: "0", eth0: "0"}, map[string]string{all: "0", eth0: "0"}, "", 0},
		{"a static default route", []netstat.Route{gxPrefix6(), gxDefault6(netstat.RouteProtoStatic)}, map[string]string{all: "0"}, map[string]string{all: "1"}, "", 1},
		{"no default route", []netstat.Route{gxPrefix6()}, map[string]string{all: "0"}, map[string]string{all: "1"}, "", 1},
		{"a router advertisement route that is not the default", []netstat.Route{
			{Type: "unicast", Table: 254, Protocol: netstat.RouteProtoRA, Dst: netip.MustParsePrefix("2001:db8:1::/64"), OutIface: "eth0"},
		}, map[string]string{all: "0"}, map[string]string{all: "1"}, "", 1},
		{"another supported key changes", ra, map[string]string{"net/ipv6/conf/all/accept_ra": "1"}, map[string]string{"net/ipv6/conf/all/accept_ra": "2"}, "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, files, _ := ckEnv()
			for k, v := range tc.live {
				files.sysctl[k] = v
			}
			st := ckState()
			st.Sysctl = tc.new
			h := &gxDumpHost{gHost: &gHost{ifaces: gIfaces()}, dump: map[netstat.Family][]netstat.Route{netstat.IPv6: tc.routes}}
			g, results, err := Checker{Host: h}.Check(context.Background(), env, st, Input{Eval: ckEval(t, nil)})
			if err != nil {
				t.Fatal(err)
			}
			if tc.message == "" && (!g.OK || len(results) != 0) {
				t.Errorf("guard = %+v with %d results, want it to pass with none", g, len(results))
			}
			if tc.message != "" && (g.OK || g.Message != tc.message) {
				t.Errorf("guard = %+v\nwant it failed with %q", g, tc.message)
			}
			if len(h.dumps) != tc.dumps {
				t.Errorf("route dumps = %v, want %d", h.dumps, tc.dumps)
			}
		})
	}
}

func TestGxCheckIPv6ForwardingFacts(t *testing.T) {
	const key = "net/ipv6/conf/all/forwarding"
	boom := errors.New("boom")
	ra := map[netstat.Family][]netstat.Route{netstat.IPv6: {gxDefault6(netstat.RouteProtoRA)}}
	for _, tc := range []struct {
		name    string
		err     error // the live read of key
		dumpErr error
		noDump  bool // the host cannot list routes
		wantErr string
	}{
		{name: "the live value cannot be read", err: syscall.EACCES, wantErr: "firewall: read " + key},
		{name: "IPv6 is off", err: fmt.Errorf("platform: open sysctl %s: %w", key, syscall.ENOENT)},
		{name: "the routes cannot be read", dumpErr: boom, wantErr: "firewall: read the IPv6 routes: boom"},
		{name: "the host cannot list routes", noDump: true, wantErr: "firewall: the host cannot list routes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, files, _ := ckEnv()
			files.sysctl[key] = "0"
			if tc.err != nil {
				files.errs = map[string]error{key: tc.err}
			}
			st := ckState()
			st.Sysctl = map[string]string{key: "1"}
			var h Host = &gxDumpHost{gHost: &gHost{ifaces: gIfaces()}, dump: ra, err: tc.dumpErr}
			if tc.noDump {
				h = &gHost{ifaces: gIfaces()}
			}
			g, results, err := Checker{Host: h}.Check(context.Background(), env, st, Input{Eval: ckEval(t, nil)})
			if tc.wantErr != "" {
				if err == nil || !strings.HasPrefix(err.Error(), tc.wantErr) {
					t.Fatalf("Check error = %v, want one starting %q", err, tc.wantErr)
				}
				if g.ID != "" || results != nil {
					t.Errorf("a failed Check returned guard %+v and results %+v", g, results)
				}
				return
			}
			if err != nil || !g.OK {
				t.Errorf("Check = %+v, %v; want a passing guard", g, err)
			}
		})
	}
}
