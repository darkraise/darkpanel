package firewall

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/firewall/dockerfw"
	"github.com/darkraise/darkpanel/internal/modules/firewall/exposure"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/modules/firewall/model"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
	"github.com/darkraise/darkpanel/internal/platform/netstat"
)

func xvAddr(s string) netip.Addr { return netip.MustParseAddr(s) }

func xvSocket(proto netstat.Proto, local string, comm string) netstat.Socket {
	state := netstat.StateListen
	if proto == netstat.ProtoUDP {
		state = netstat.StateClose
	}
	s := netstat.Socket{Proto: proto, Local: netip.MustParseAddrPort(local), State: state}
	if comm != "" {
		s.Processes = []netstat.Process{{PID: 100, Comm: comm}}
	}
	return s
}

// xvIfaces are the host's interfaces: loopback, eth0 with one address of
// each family and a link-local IPv6 address, and docker0.
func xvIfaces() []netstat.Interface {
	return []netstat.Interface{
		{Index: 1, Name: "lo", Up: true, Running: true, Loopback: true, Addrs: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/8"), netip.MustParsePrefix("::1/128")}},
		{Index: 2, Name: "eth0", Up: true, Running: true, Addrs: []netip.Prefix{
			netip.MustParsePrefix("fe80::1/64"), netip.MustParsePrefix("192.0.2.10/24"), netip.MustParsePrefix("2001:db8::10/64")}},
		{Index: 3, Name: "docker0", Up: true, Running: true, Addrs: []netip.Prefix{netip.MustParsePrefix("172.17.0.1/16")}},
	}
}

const (
	xvSaveV4 = `*nat
:PREROUTING ACCEPT [0:0]
-A PREROUTING -p tcp -m tcp --dport 8080 -j DNAT --to-destination 172.17.0.2:80
COMMIT
*filter
:INPUT DROP [0:0]
:FORWARD DROP [0:0]
:OUTPUT ACCEPT [0:0]
-A INPUT -i lo -j ACCEPT
-A INPUT -p tcp -m tcp --dport 22 -j ACCEPT
-A FORWARD -d 172.17.0.2/32 -p tcp -m tcp --dport 80 -j ACCEPT
COMMIT
`
	xvSaveV6 = `*filter
:INPUT DROP [0:0]
:FORWARD DROP [0:0]
:OUTPUT ACCEPT [0:0]
-A INPUT -i lo -j ACCEPT
-A INPUT -p tcp -m tcp --dport 22 -j ACCEPT
COMMIT
`
)

func xvLoaded(t *testing.T) fwstate.Loaded {
	t.Helper()
	return fwstate.Loaded{Variant: "nft", V4: fhParse(t, xvSaveV4), V6: fhParse(t, xvSaveV6),
		Sysctl: map[string]string{"net/ipv4/ip_forward": "1", "net/ipv6/conf/all/forwarding": "1"}}
}

// xvDocker is Docker with web (8080 on both families, 127.0.0.1:8081, one
// IPv4 address) and api (9090 on IPv4 only, no proxy socket).
func xvDocker() dockerState {
	web := dockerfw.Container{ID: "c1", Name: "web",
		Ports: []dockerfw.Published{
			{HostAddr: xvAddr("0.0.0.0"), HostPort: 8080, ContainerPort: 80, Proto: "tcp"},
			{HostAddr: xvAddr("::"), HostPort: 8080, ContainerPort: 80, Proto: "tcp"},
			{HostAddr: xvAddr("127.0.0.1"), HostPort: 8081, ContainerPort: 80, Proto: "tcp"},
		},
		Endpoints: []dockerfw.Endpoint{{Network: "bridge", Bridge: "docker0", Addr4: xvAddr("172.17.0.2")}}}
	api := dockerfw.Container{ID: "c2", Name: "api",
		Ports:     []dockerfw.Published{{HostAddr: xvAddr("0.0.0.0"), HostPort: 9090, ContainerPort: 80, Proto: "tcp"}},
		Endpoints: []dockerfw.Endpoint{{Network: "bridge", Bridge: "docker0", Addr4: xvAddr("172.17.0.3")}}}
	return dockerState{
		available: true,
		cfg:       dockerConfig{Settings: dockerfw.DefaultSettings()},
		facts: dockerfw.Facts{
			Containers: []dockerfw.Container{api, web},
			Networks: []dockerfw.Network{{ID: "b1", Name: "bridge", Driver: "bridge", Bridge: "docker0",
				Subnets: []netip.Prefix{netip.MustParsePrefix("172.17.0.0/16")}}},
		},
	}
}

func xvSockets() []netstat.Socket {
	established := xvSocket(netstat.ProtoTCP, "192.0.2.10:22", "sshd")
	established.State = netstat.StateEstablished
	return []netstat.Socket{
		xvSocket(netstat.ProtoTCP, "0.0.0.0:22", "sshd"),
		xvSocket(netstat.ProtoTCP, "[::]:22", "sshd"),
		xvSocket(netstat.ProtoUDP, "0.0.0.0:123", "chronyd"),
		xvSocket(netstat.ProtoTCP, "[::]:3000", "node"),
		xvSocket(netstat.ProtoTCP, "[::ffff:192.0.2.10]:3306", "mysqld"),
		xvSocket(netstat.ProtoTCP, "127.0.0.1:5432", "postgres"),
		xvSocket(netstat.ProtoTCP, "0.0.0.0:7777", "docker-proxy"),
		xvSocket(netstat.ProtoTCP, "0.0.0.0:8080", "docker-proxy"),
		xvSocket(netstat.ProtoTCP, "[::]:8080", "docker-proxy"),
		established,
		xvSocket(netstat.ProtoTCP, "0.0.0.0:0", "sshd"),
	}
}

func xvInput(t *testing.T) exposureInput {
	t.Helper()
	return exposureInput{
		cfg: fwstate.Config{}, cur: xvLoaded(t), st: xvDocker(), sockets: xvSockets(), ifaces: xvIfaces(),
		routeGet: func(q netstat.RouteQuery) (netstat.Route, error) {
			return netstat.Route{}, fmt.Errorf("route to %s asked for", q.Dst)
		},
	}
}

func xvClass(r ExposureRow, name string) exposure.Verdict {
	for _, c := range r.Classes {
		if c.Class == name {
			return c
		}
	}
	return exposure.Verdict{Class: "(missing)"}
}

// xvLine summarizes a row: the key, the process, the merged container and
// the internet and localhost verdicts.
func xvLine(r ExposureRow) string {
	ctr := "-"
	if c := r.Container; c != nil {
		host := "all"
		if c.Binding.HostAddr.IsValid() {
			host = c.Binding.HostAddr.String()
		}
		ctr = fmt.Sprintf("%s>%d host=%s proxied=%t", c.Name, c.ContainerPort, host, c.Proxied)
	}
	return fmt.Sprintf("%s %s:%d %s proc=%q ctr=%s internet=%s localhost=%s",
		r.Proto, r.Addr, r.Port, r.Family, r.Process, ctr, xvClass(r, "internet").Verdict, xvClass(r, "localhost").Verdict)
}

// TestXvRows: listening sockets and published ports become one row each in
// port order; a docker-proxy socket merges into its container's binding,
// an unmatched one stays a process, a published port without a socket is
// listed, IPv4-mapped addresses are unmapped and sockets that do not listen
// are skipped. Each row carries the evaluator's verdicts for the loaded rules.
func TestXvRows(t *testing.T) {
	rows := exposureRows(xvInput(t))
	got := make([]string, len(rows))
	for i, r := range rows {
		got[i] = xvLine(r)
	}
	want := []string{
		`tcp 0.0.0.0:22 v4 proc="sshd" ctr=- internet=open localhost=open`,
		`tcp :::22 v6 proc="sshd" ctr=- internet=open localhost=open`,
		`udp 0.0.0.0:123 v4 proc="chronyd" ctr=- internet=filtered localhost=open`,
		`tcp 0.0.0.0:3000 v4 proc="node" ctr=- internet=filtered localhost=open`,
		`tcp :::3000 v6 proc="node" ctr=- internet=filtered localhost=open`,
		`tcp 192.0.2.10:3306 v4 proc="mysqld" ctr=- internet=filtered localhost=open`,
		`tcp 127.0.0.1:5432 v4 proc="postgres" ctr=- internet=closed localhost=open`,
		`tcp 0.0.0.0:7777 v4 proc="docker-proxy" ctr=- internet=filtered localhost=open`,
		`tcp 0.0.0.0:8080 v4 proc="" ctr=web>80 host=all proxied=false internet=open localhost=open`,
		`tcp :::8080 v6 proc="" ctr=web>80 host=all proxied=true internet=filtered localhost=open`,
		`tcp 127.0.0.1:8081 v4 proc="" ctr=web>80 host=127.0.0.1 proxied=false internet=closed localhost=open`,
		`tcp 0.0.0.0:9090 v4 proc="" ctr=api>80 host=all proxied=false internet=filtered localhost=open`,
	}
	if !slices.Equal(got, want) {
		t.Errorf("rows\n got %q\nwant %q", got, want)
	}
}

func TestXvRowDetails(t *testing.T) {
	rows := exposureRows(xvInput(t))
	byKey := map[string]ExposureRow{}
	for _, r := range rows {
		byKey[fmt.Sprintf("%s/%s/%d", r.Proto, r.Family, r.Port)] = r
	}
	ssh := byKey["tcp/v4/22"]
	if got, want := xvClass(ssh, "internet").DecidedBy, "filter/INPUT#2: -A INPUT -p tcp -m tcp --dport 22 -j ACCEPT"; got != want {
		t.Errorf("internet decidedBy %q, want %q", got, want)
	}
	if lo := xvClass(byKey["tcp/v4/5432"], "internet"); lo.DecidedBy != "bound to loopback" {
		t.Errorf("loopback row decidedBy %q, want bound to loopback", lo.DecidedBy)
	}
	web := byKey["tcp/v4/8080"].Container
	want := dockerfw.Binding{Selector: "web", HostPort: 8080, Proto: "tcp"}
	if web == nil || web.Binding != want {
		t.Errorf("web binding %+v, want %+v: a wildcard publication has no host address", web, want)
	}
	for _, r := range rows {
		if r.Classes == nil {
			t.Errorf("row %s/%d has nil classes", r.Proto, r.Port)
		}
	}
}

// TestXvShieldsUp: with a tailscale0 interface up, the tailnet class is the
// host firewall's verdict, and closed by shields-up when tailscaled has them on.
func TestXvShieldsUp(t *testing.T) {
	in := xvInput(t)
	in.sockets = []netstat.Socket{xvSocket(netstat.ProtoTCP, "0.0.0.0:22", "sshd")}
	in.ifaces = append(in.ifaces, netstat.Interface{Index: 9, Name: "tailscale0", Up: true, Running: true,
		Addrs: []netip.Prefix{netip.MustParsePrefix("100.64.0.5/32")}})
	open := xvClass(exposureRows(in)[0], "tailnet")
	if open.Verdict != "open" || open.Note != "host firewall; tailnet policy not evaluated" {
		t.Errorf("tailnet %+v, want open with the host firewall note", open)
	}
	in.shieldsUp = true
	closed := xvClass(exposureRows(in)[0], "tailnet")
	if closed.Verdict != "closed" || closed.DecidedBy != "Tailscale shields-up" {
		t.Errorf("tailnet %+v, want closed by shields-up", closed)
	}
}

func TestXvRoute(t *testing.T) {
	var asked []netstat.RouteQuery
	routeGet := func(q netstat.RouteQuery) (netstat.Route, error) {
		asked = append(asked, q)
		switch q.Dst.String() {
		case "198.51.100.4":
			return netstat.Route{Type: "unicast", OutIface: "eth1"}, nil
		case "198.51.100.5":
			return netstat.Route{Type: "unicast"}, nil
		case "198.51.100.6":
			return netstat.Route{Type: "local"}, nil
		}
		return netstat.Route{}, errors.New("no route")
	}
	nets := []dockerfw.Network{{Name: "bridge", Bridge: "docker0", Subnets: []netip.Prefix{netip.MustParsePrefix("172.17.0.0/16")}}}
	sysctl := map[string]string{"net/ipv4/ip_forward": "1", "net/ipv6/conf/all/forwarding": "0"}
	route := exposureRoute(routeGet, xvIfaces(), nets, sysctl)
	for _, tt := range []struct {
		name      string
		p         fwstate.Packet
		path, out string
		err       string
		asks      int
	}{
		{"a host address", fwstate.Packet{Path: "forward", Dst: xvAddr("192.0.2.10")}, "input", "", "", 0},
		{"an IPv4-mapped host address", fwstate.Packet{Path: "forward", Dst: xvAddr("::ffff:192.0.2.10")}, "input", "", "", 0},
		{"loopback from output", fwstate.Packet{Path: "output", Dst: xvAddr("127.0.0.1")}, "output", "lo", "", 0},
		{"a Docker subnet needs no lookup", fwstate.Packet{Path: "forward", Dst: xvAddr("172.17.0.2")}, "forward", "docker0", "", 0},
		{"another address looks the route up", fwstate.Packet{Path: "forward", Src: xvAddr("203.0.113.7"), InIface: "eth0", Dst: xvAddr("198.51.100.4")}, "forward", "eth1", "", 1},
		{"a local route", fwstate.Packet{Path: "forward", Dst: xvAddr("198.51.100.6")}, "input", "", "", 1},
		{"a route with no single interface", fwstate.Packet{Path: "forward", Dst: xvAddr("198.51.100.5")}, "", "", "no single out-interface", 1},
		{"a lookup that fails", fwstate.Packet{Path: "forward", Dst: xvAddr("198.51.100.9")}, "", "", "no route", 1},
		{"an output route", fwstate.Packet{Path: "output", Src: xvAddr("192.0.2.10"), Dst: xvAddr("198.51.100.4")}, "output", "eth1", "", 1},
		{"forwarding off for IPv6", fwstate.Packet{Path: "forward", Dst: xvAddr("2001:db8:1::5")}, "", "", "forwarding is off", 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			asked = nil
			path, out, err := route(tt.p)
			if tt.err != "" {
				if err == nil || !strings.Contains(err.Error(), tt.err) {
					t.Fatalf("err = %v, want one containing %q", err, tt.err)
				}
			} else if err != nil || path != tt.path || out != tt.out {
				t.Fatalf("route = %q, %q, %v; want %q, %q", path, out, err, tt.path, tt.out)
			}
			if len(asked) != tt.asks {
				t.Errorf("asked the kernel %d times, want %d", len(asked), tt.asks)
			}
		})
	}
	asked = nil
	if _, _, err := route(fwstate.Packet{Path: "forward", Src: xvAddr("203.0.113.7"), InIface: "eth0", Mark: 5, Dst: xvAddr("198.51.100.4")}); err != nil {
		t.Fatal(err)
	}
	want := netstat.RouteQuery{Dst: xvAddr("198.51.100.4"), Src: xvAddr("203.0.113.7"), IIF: "eth0", Mark: 5}
	if len(asked) != 1 || asked[0] != want {
		t.Errorf("query %+v, want %+v: the lookup carries the source, the in-interface and the mark", asked, want)
	}
}

func TestXvDescribe(t *testing.T) {
	rs := fhParse(t, `*raw
:PREROUTING ACCEPT [0:0]
-A PREROUTING -d 172.17.0.2/32 ! -i docker0 -j DROP
COMMIT
*filter
:INPUT DROP [0:0]
:ufw-before-input - [0:0]
:ufw-user-input - [0:0]
:ufw-user-forward - [0:0]
:DOCKER - [0:0]
:DOCKER-USER - [0:0]
:dp-b-0123456789ab - [0:0]
:ts-input - [0:0]
-A INPUT -p tcp -m tcp --dport 22 -j ACCEPT
-A ufw-before-input -i lo -j ACCEPT
-A ufw-user-input -p tcp -m tcp --dport 80 -j ACCEPT
-A ufw-user-input -p tcp -m tcp --dport 443 -j ACCEPT
-A ufw-user-forward -p tcp -m tcp --dport 80 -j ACCEPT
-A ufw-user-forward -p tcp -m tcp --dport 81 -j ACCEPT
-A ufw-user-forward -p tcp -m tcp --dport 82 -j ACCEPT
-A DOCKER -d 172.17.0.2/32 -j ACCEPT
-A DOCKER-USER -i docker0 -j RETURN
-A dp-b-0123456789ab -j DROP
-A ts-input -i tailscale0 -j ACCEPT
COMMIT
`)
	in1 := model.Rule{ID: "01J00000000000000000000001", Action: model.ActionAllow, Direction: model.DirIn, Proto: model.ProtoTCP, From: model.Any, To: model.Any, ToPort: "80", Family: model.FamilyV4}
	in2 := model.Rule{ID: "01J00000000000000000000002", Action: model.ActionAllow, Direction: model.DirIn, Proto: model.ProtoTCP, From: model.Any, To: model.Any, ToPort: "443", Family: model.FamilyV4, Comment: "web"}
	route1 := model.Rule{ID: "manual-0123456789abcdef", Action: model.ActionAllow, Direction: model.DirRoute, Proto: model.ProtoTCP, From: model.Any, To: model.Any, ToPort: "80", Family: model.FamilyV4, Origin: model.OriginManual}
	describe := exposureDescribe(rs, []model.Rule{in1, route1, in2})
	for _, tt := range []struct{ ref, want string }{
		{"filter/INPUT policy DROP", "filter/INPUT policy DROP"},
		{"filter/INPUT#1", "filter/INPUT#1: -A INPUT -p tcp -m tcp --dport 22 -j ACCEPT"},
		{"filter/ufw-user-input#2", "filter/ufw-user-input#2: -A ufw-user-input -p tcp -m tcp --dport 443 -j ACCEPT [ufw " + ruleText(in2) + "]"},
		{"filter/ufw-user-forward#1", "filter/ufw-user-forward#1: -A ufw-user-forward -p tcp -m tcp --dport 80 -j ACCEPT"},
		{"filter/ufw-before-input#1", "filter/ufw-before-input#1: -A ufw-before-input -i lo -j ACCEPT [UFW framework]"},
		{"filter/DOCKER#1", "filter/DOCKER#1: -A DOCKER -d 172.17.0.2/32 -j ACCEPT [Docker]"},
		{"filter/DOCKER-USER#1", "filter/DOCKER-USER#1: -A DOCKER-USER -i docker0 -j RETURN [Darkpanel Docker block]"},
		{"filter/dp-b-0123456789ab#1", "filter/dp-b-0123456789ab#1: -A dp-b-0123456789ab -j DROP [Darkpanel Docker block]"},
		{"filter/ts-input#1", "filter/ts-input#1: -A ts-input -i tailscale0 -j ACCEPT [tailscaled]"},
		{"raw/PREROUTING#1", "raw/PREROUTING#1: -A PREROUTING -d 172.17.0.2/32 ! -i docker0 -j DROP [Docker raw-table protection]"},
		{"filter/INPUT#9", "filter/INPUT#9"},
		{"filter/nope#1", "filter/nope#1"},
		{"mangle/INPUT#1", "mangle/INPUT#1"},
		{"garbage", "garbage"},
	} {
		if got := describe(tt.ref); got != tt.want {
			t.Errorf("describe(%q) = %q, want %q", tt.ref, got, tt.want)
		}
	}
	if got := exposureDescribe(nil, nil)("filter/INPUT#1"); got != "filter/INPUT#1" {
		t.Errorf("describe with no ruleset = %q, want the ref", got)
	}
}

func TestXvManualNotes(t *testing.T) {
	tuple := func(line string) model.Tuple {
		tp, err := model.ParseTuple(line, false)
		if err != nil {
			t.Fatal(err)
		}
		return tp
	}
	cfg := fwstate.Config{Rules4: []model.Tuple{
		tuple("### tuple ### route:allow tcp 80 172.17.0.2 any 0.0.0.0/0 in_eth0!out_docker0"),
		tuple("### tuple ### route:allow tcp 25 203.0.113.7 any 0.0.0.0/0 in_eth0!out_eth1"),
		tuple("### tuple ### allow tcp 80 172.17.0.2 any 0.0.0.0/0 in"),
	}}
	facts := dockerfw.Facts{Networks: []dockerfw.Network{{Name: "bridge", Driver: "bridge", Bridge: "docker0",
		Subnets: []netip.Prefix{netip.MustParsePrefix("172.17.0.0/16")}}}}
	notes := exposureManualNotes(cfg, facts)
	if len(notes) != 1 || !strings.Contains(notes[0], "172.17.0.2") || !strings.HasSuffix(notes[0], "no effect without a Darkpanel binding") {
		t.Errorf("notes %q, want one for the manual route rule to a container", notes)
	}
}

// xvDeps is a deps over the fake host with Docker, the sockets, the
// interfaces and the tailscaled state the test scripts.
func xvDeps(h *fakeHost, d *fakeDocker, socks []netstat.Socket) deps {
	out := h.deps()
	out.docker = d
	out.readVariant = func(context.Context, core.Env, fwstate.Family, string, []string) (*netfilter.Ruleset, error) {
		return nil, nil
	}
	out.sockets = func() ([]netstat.Socket, error) { return socks, nil }
	out.interfaces = func() ([]netstat.Interface, error) { return xvIfaces(), nil }
	out.routeGet = func(q netstat.RouteQuery) (netstat.Route, error) {
		return netstat.Route{}, fmt.Errorf("route to %s asked for", q.Dst)
	}
	out.shieldsUp = func(context.Context) (bool, bool, error) { return false, false, nil }
	return out
}

// xvHost is a fake host whose IPv4 UFW chain holds the rules of its two
// IPv4 tuples, so a deciding rule can be named after the tuple.
func xvHost(t *testing.T) *fakeHost {
	t.Helper()
	h := newFakeHost(t)
	h.loaded.V4 = fhParse(t, "*filter\n:INPUT DROP [0:0]\n:FORWARD DROP [0:0]\n:OUTPUT ACCEPT [0:0]\n:ufw-user-input - [0:0]\n"+
		"-A INPUT -j ufw-user-input\n-A ufw-user-input -p tcp -m tcp --dport 22 -j ACCEPT\n"+
		"-A ufw-user-input -s 10.0.0.0/8 -p tcp -m tcp --dport 3000 -j ACCEPT\nCOMMIT\n")
	return h
}

func xvView(t *testing.T, h *fakeHost, d deps) ExposureView {
	t.Helper()
	raw, err := fw{d: d}.exposureResource().Read(context.Background(), h.env())
	if err != nil {
		t.Fatal(err)
	}
	var v ExposureView
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// TestXvResource: the resource reads the host and the Docker facts, names
// the deciding UFW rule after its tuple, and is registered as
// firewall.exposure.
func TestXvResource(t *testing.T) {
	h := xvHost(t)
	d := xvDeps(h, newFakeDocker(), []netstat.Socket{xvSocket(netstat.ProtoTCP, "0.0.0.0:22", "sshd")})
	if name := (fw{d: d}).exposureResource().Name; name != "firewall.exposure" {
		t.Fatalf("resource %q, want firewall.exposure", name)
	}
	v := xvView(t, h, d)
	if len(v.Rows) != 1 || len(v.Notes) != 0 {
		t.Fatalf("view %+v, want one row and no notes", v)
	}
	r := v.Rows[0]
	if r.Proto != "tcp" || r.Addr != "0.0.0.0" || r.Port != 22 || r.Family != "v4" || r.Process != "sshd" || r.Container != nil {
		t.Errorf("row %+v", r)
	}
	internet := xvClass(r, "internet")
	rule22 := model.Merge(h.config().Rules4, nil)[0]
	want := "filter/ufw-user-input#1: -A ufw-user-input -p tcp -m tcp --dport 22 -j ACCEPT [ufw " + ruleText(rule22) + "]"
	if internet.Verdict != "open" || internet.DecidedBy != want {
		t.Errorf("internet %+v, want open decided by %q", internet, want)
	}
}

func TestXvResourceShieldsUp(t *testing.T) {
	h := xvHost(t)
	d := xvDeps(h, newFakeDocker(), []netstat.Socket{xvSocket(netstat.ProtoTCP, "0.0.0.0:22", "sshd")})
	d.interfaces = func() ([]netstat.Interface, error) {
		return append(xvIfaces(), netstat.Interface{Index: 9, Name: "tailscale0", Up: true, Running: true,
			Addrs: []netip.Prefix{netip.MustParsePrefix("100.64.0.5/32")}}), nil
	}
	d.shieldsUp = func(context.Context) (bool, bool, error) { return true, true, nil }
	if tn := xvClass(xvView(t, h, d).Rows[0], "tailnet"); tn.Verdict != "closed" || tn.DecidedBy != "Tailscale shields-up" {
		t.Errorf("tailnet %+v, want closed by shields-up", tn)
	}
	d.shieldsUp = func(context.Context) (bool, bool, error) { return true, false, nil }
	if tn := xvClass(xvView(t, h, d).Rows[0], "tailnet"); tn.Verdict == "closed" && tn.DecidedBy == "Tailscale shields-up" {
		t.Errorf("tailnet %+v: shields-up counts only when tailscaled is known", tn)
	}
}

func TestXvResourceNotes(t *testing.T) {
	socks := []netstat.Socket{xvSocket(netstat.ProtoTCP, "0.0.0.0:22", "sshd")}
	t.Run("an unsupported host has no rows", func(t *testing.T) {
		h := xvHost(t)
		h.loaded.Unsupported = []string{"a foreign nftables hook"}
		v := xvView(t, h, xvDeps(h, newFakeDocker(), socks))
		if len(v.Rows) != 0 || len(v.Notes) != 1 || !strings.Contains(v.Notes[0], "unsupported configuration") || !strings.Contains(v.Notes[0], "a foreign nftables hook") {
			t.Errorf("view %+v", v)
		}
	})
	t.Run("an out-of-sync host keeps its rows", func(t *testing.T) {
		h := xvHost(t)
		h.diffs = []fwstate.ChainDiff{{Family: fwstate.V4, Table: "filter", Chain: "ufw-user-input", OnlyB: []string{"k"}}}
		v := xvView(t, h, xvDeps(h, newFakeDocker(), socks))
		if len(v.Rows) != 1 || len(v.Notes) != 1 || !strings.Contains(v.Notes[0], "out of sync") || !strings.Contains(v.Notes[0], "v4 filter/ufw-user-input") {
			t.Errorf("view %+v", v)
		}
	})
	t.Run("Docker unavailable is a note", func(t *testing.T) {
		h := xvHost(t)
		d := newFakeDocker()
		d.err = dockerapi.ErrUnavailable
		v := xvView(t, h, xvDeps(h, d, socks))
		if len(v.Rows) != 1 || len(v.Notes) != 1 || !strings.Contains(v.Notes[0], "Docker is unavailable") {
			t.Errorf("view %+v", v)
		}
	})
	t.Run("unreadable shields-up is a note", func(t *testing.T) {
		h := xvHost(t)
		d := xvDeps(h, newFakeDocker(), socks)
		d.shieldsUp = func(context.Context) (bool, bool, error) { return false, false, errors.New("tailscaled said no") }
		v := xvView(t, h, d)
		if len(v.Rows) != 1 || len(v.Notes) != 1 || !strings.Contains(v.Notes[0], "shields-up") || !strings.Contains(v.Notes[0], "tailscaled said no") {
			t.Errorf("view %+v", v)
		}
	})
	t.Run("a socket table that cannot be read fails", func(t *testing.T) {
		h := xvHost(t)
		d := xvDeps(h, newFakeDocker(), nil)
		d.sockets = func() ([]netstat.Socket, error) { return nil, errors.New("no /proc") }
		_, err := fw{d: d}.exposureView(context.Background(), h.env())
		if err == nil || !strings.Contains(err.Error(), "no /proc") {
			t.Errorf("err = %v, want the read failure", err)
		}
	})
}

// TestXvViewJSON: rows and classes are never null, and a view with nothing
// to say omits notes.
func TestXvViewJSON(t *testing.T) {
	raw, err := json.Marshal(ExposureView{Rows: []ExposureRow{}})
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"rows":[]}` {
		t.Errorf("json %s", raw)
	}
	h := xvHost(t)
	d := xvDeps(h, newFakeDocker(), []netstat.Socket{xvSocket(netstat.ProtoTCP, "0.0.0.0:22", "sshd")})
	v, err := fw{d: d}.exposureView(context.Background(), h.env())
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Rows) != 1 || len(v.Rows[0].Classes) == 0 {
		t.Errorf("view %+v, want one row with classes", v)
	}
}
