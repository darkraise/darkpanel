package eval

import (
	"errors"
	"net/netip"
	"testing"

	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
)

const hooksRules = `*raw
:PREROUTING ACCEPT [0:0]
:OUTPUT ACCEPT [0:0]
-A PREROUTING -d 172.18.0.2/32 ! -i docker0 -j DROP
COMMIT
*mangle
:PREROUTING ACCEPT [0:0]
:INPUT ACCEPT [0:0]
:FORWARD ACCEPT [0:0]
:OUTPUT ACCEPT [0:0]
:POSTROUTING ACCEPT [0:0]
-A PREROUTING -p udp -m udp --dport 9999 -j DROP
-A FORWARD -o docker0 -p tcp -m tcp --tcp-flags SYN,RST SYN -j TCPMSS --clamp-mss-to-pmtu
-A POSTROUTING -o eth1 -j DROP
COMMIT
*nat
:PREROUTING ACCEPT [0:0]
:INPUT ACCEPT [0:0]
:OUTPUT ACCEPT [0:0]
:POSTROUTING ACCEPT [0:0]
:DOCKER - [0:0]
-A PREROUTING -m addrtype --dst-type LOCAL -j DOCKER
-A OUTPUT ! -d 127.0.0.0/8 -m addrtype --dst-type LOCAL -j DOCKER
-A POSTROUTING -s 172.18.0.0/16 ! -o docker0 -j MASQUERADE
-A DOCKER -i docker0 -j RETURN
-A DOCKER ! -i docker0 -p tcp -m tcp --dport 8080 -j DNAT --to-destination 172.18.0.2:80
-A DOCKER -p tcp -m tcp --dport 8081 -j DNAT --to-destination 172.18.0.3
-A DOCKER -p tcp -m tcp --dport 8082 -j DNAT --to-destination 172.18.0.2-172.18.0.9:80
-A DOCKER -p tcp -m tcp --dport 8084 -j DNAT --to-destination 192.0.2.1:2222
-A DOCKER -p tcp -m tcp --dport 8085 -j DNAT --to-destination 10.9.9.9:80
COMMIT
*filter
:INPUT DROP [0:0]
:FORWARD DROP [0:0]
:OUTPUT ACCEPT [0:0]
:DOCKER - [0:0]
-A INPUT -p tcp -m tcp --dport 22 -j ACCEPT
-A INPUT -p tcp -m tcp --dport 23 -j ACCEPT
-A INPUT -p tcp -m tcp --dport 2222 -m conntrack --ctstate DNAT --ctorigdstport 8084 -j ACCEPT
-A FORWARD -o docker0 -j DOCKER
-A FORWARD -i docker0 -j ACCEPT
-A DOCKER -d 172.18.0.2/32 ! -i docker0 -o docker0 -p tcp -m tcp --dport 80 -m conntrack --ctstate DNAT --ctorigdst 192.0.2.1 --ctorigdstport 8080 -j ACCEPT
-A DOCKER ! -i docker0 -o docker0 -j DROP
COMMIT
*security
:INPUT ACCEPT [0:0]
:FORWARD ACCEPT [0:0]
:OUTPUT ACCEPT [0:0]
-A INPUT -p tcp -m tcp --dport 23 -j DROP
COMMIT
`

// hooksRouter routes the containers' 172.18.0.0/16 out of docker0, this
// host's 192.0.2.1 locally, and fails for anything else. It records the
// packets it was asked about.
type hooksRouter struct{ asked []fwstate.Packet }

func (r *hooksRouter) route(p fwstate.Packet) (string, string, error) {
	r.asked = append(r.asked, p)
	switch {
	case netip.MustParsePrefix("172.18.0.0/16").Contains(p.Dst) && p.Path == "output":
		return "output", "docker0", nil
	case netip.MustParsePrefix("172.18.0.0/16").Contains(p.Dst):
		return "forward", "docker0", nil
	case p.Dst == netip.MustParseAddr("192.0.2.1") && p.Path == "output":
		return "output", "lo", nil
	case p.Dst == netip.MustParseAddr("192.0.2.1"):
		return "input", "", nil
	}
	return "", "", errors.New("no route to host")
}

func TestEvaluateHooks(t *testing.T) {
	rs := evalRuleset(t, hooksRules)
	port := func(n uint16) func(p *fwstate.Packet) { return func(p *fwstate.Packet) { p.DstPort = n } }
	tests := []struct {
		name    string
		edit    func(p *fwstate.Packet)
		verdict fwstate.Verdict
		trace   []string
		reason  string
	}{
		{
			name:    "a published port is DNATed, routed to the container and accepted",
			edit:    port(8080),
			verdict: fwstate.Accept,
			trace: []string{"nat/PREROUTING#1", "nat/DOCKER#2", "mangle/FORWARD#1",
				"filter/FORWARD#1", "filter/DOCKER#1"},
		},
		{
			name:    "DNAT without a port keeps the port",
			edit:    port(8081),
			verdict: fwstate.Drop,
			trace:   []string{"nat/PREROUTING#1", "nat/DOCKER#3", "mangle/FORWARD#1", "filter/FORWARD#1", "filter/DOCKER#2"},
			reason:  "filter/DOCKER#2 drops",
		},
		{
			name:    "DNAT to a range is unknown",
			edit:    port(8082),
			verdict: fwstate.Unknown,
			reason:  "nat/DOCKER#4: DNAT to a range",
		},
		{
			name:    "DNAT to a local port stays on the input path and sets the DNAT state",
			edit:    port(8084),
			verdict: fwstate.Accept,
			trace:   []string{"nat/PREROUTING#1", "nat/DOCKER#5", "filter/INPUT#3"},
		},
		{
			name:    "a route that cannot be resolved is unknown",
			edit:    port(8085),
			verdict: fwstate.Unknown,
			reason:  "route to 10.9.9.9: no route to host",
		},
		{
			name:    "a later packet skips the nat table",
			edit:    func(p *fwstate.Packet) { p.DstPort, p.CtState = 8080, "ESTABLISHED" },
			verdict: fwstate.Drop,
			trace:   []string{},
			reason:  "filter/INPUT policy DROP",
		},
		{
			name:    "a related connection facing nat rules is unknown",
			edit:    func(p *fwstate.Packet) { p.DstPort, p.CtState = 8080, "RELATED" },
			verdict: fwstate.Unknown,
			reason:  "nat/PREROUTING has rules a related connection may or may not pass",
		},
		{
			name: "raw PREROUTING drops direct access to a container",
			edit: func(p *fwstate.Packet) {
				p.Path, p.OutIface, p.Dst, p.DstPort = "forward", "docker0", netip.MustParseAddr("172.18.0.2"), 80
			},
			verdict: fwstate.Drop,
			trace:   []string{"raw/PREROUTING#1"},
			reason:  "raw/PREROUTING#1 drops",
		},
		{
			name:    "mangle PREROUTING drops",
			edit:    func(p *fwstate.Packet) { p.Proto, p.DstPort = "udp", 9999 },
			verdict: fwstate.Drop,
			reason:  "mangle/PREROUTING#1 drops",
		},
		{
			name:    "security INPUT runs after filter INPUT accepts",
			edit:    port(23),
			verdict: fwstate.Drop,
			trace:   []string{"nat/PREROUTING#1", "filter/INPUT#2", "security/INPUT#1"},
			reason:  "security/INPUT#1 drops",
		},
		{
			name: "mangle POSTROUTING drops a forwarded packet",
			edit: func(p *fwstate.Packet) {
				p.Path, p.InIface, p.OutIface = "forward", "docker0", "eth1"
				p.Src, p.Dst = netip.MustParseAddr("172.18.0.2"), netip.MustParseAddr("198.51.100.2")
			},
			verdict: fwstate.Drop,
			trace:   []string{"filter/FORWARD#2", "mangle/POSTROUTING#1"},
		},
		{
			name: "nat POSTROUTING masquerades a forwarded packet",
			edit: func(p *fwstate.Packet) {
				p.Path, p.InIface, p.OutIface = "forward", "docker0", "eth0"
				p.Src, p.Dst = netip.MustParseAddr("172.18.0.2"), netip.MustParseAddr("198.51.100.2")
			},
			verdict: fwstate.Accept,
			trace:   []string{"filter/FORWARD#2", "nat/POSTROUTING#1"},
		},
		{
			name: "nat OUTPUT DNATs a local connection to a published port",
			edit: func(p *fwstate.Packet) {
				p.Path, p.InIface, p.OutIface, p.DstPort = "output", "", "lo", 8080
				p.Src = netip.MustParseAddr("192.0.2.1")
			},
			verdict: fwstate.Accept,
			trace:   []string{"nat/OUTPUT#1", "nat/DOCKER#2"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := evalPacket()
			tt.edit(&p)
			r := &hooksRouter{}
			evalCheck(t, Evaluate(rs, p, r.route), tt.verdict, tt.trace, tt.reason)
		})
	}
}

func TestEvaluateRouterContract(t *testing.T) {
	rs := evalRuleset(t, hooksRules)
	p := evalPacket()
	p.DstPort = 8080
	r := &hooksRouter{}
	Evaluate(rs, p, r.route)
	if len(r.asked) != 1 {
		t.Fatalf("router asked %d times, want once", len(r.asked))
	}
	got := r.asked[0]
	if got.Dst != netip.MustParseAddr("172.18.0.2") || got.DstPort != 80 || got.Src != p.Src || got.InIface != "eth0" {
		t.Fatalf("router asked about %+v, want the rewritten packet", got)
	}

	p.DstPort = 22
	r = &hooksRouter{}
	Evaluate(rs, p, r.route)
	if len(r.asked) != 0 {
		t.Fatalf("router asked %d times without DNAT, want never", len(r.asked))
	}

	p.DstPort = 8080
	evalCheck(t, Evaluate(rs, p, nil), fwstate.Unknown, nil, "no router")

	wrong := func(fwstate.Packet) (string, string, error) { return "output", "eth0", nil }
	evalCheck(t, Evaluate(rs, p, wrong), fwstate.Unknown, nil, `gave path "output"`)
	noOut := func(fwstate.Packet) (string, string, error) { return "forward", "", nil }
	evalCheck(t, Evaluate(rs, p, noOut), fwstate.Unknown, nil, `gave path "forward"`)
}

func TestEvaluateUnmodelledHooks(t *testing.T) {
	p := evalPacket()
	natInput := "*nat\n:PREROUTING ACCEPT [0:0]\n:INPUT ACCEPT [0:0]\n:OUTPUT ACCEPT [0:0]\n:POSTROUTING ACCEPT [0:0]\n" +
		"-A INPUT -s 10.0.0.0/8 -j SNAT --to-source 192.0.2.1\nCOMMIT\n"
	evalCheck(t, Evaluate(evalRuleset(t, natInput), p, nil), fwstate.Unknown, nil, "nat/INPUT has rules")
	p.CtState = "RELATED"
	evalCheck(t, Evaluate(evalRuleset(t, natInput), p, nil), fwstate.Unknown, nil, "nat/INPUT has rules")
	p.CtState = "ESTABLISHED"
	evalCheck(t, Evaluate(evalRuleset(t, natInput), p, nil), fwstate.Accept, nil, "")

	p = evalPacket()
	dnatInFilter := "*filter\n:INPUT ACCEPT [0:0]\n:FORWARD ACCEPT [0:0]\n:OUTPUT ACCEPT [0:0]\n" +
		"-A INPUT -j DNAT --to-destination 10.0.0.1\nCOMMIT\n"
	evalCheck(t, Evaluate(evalRuleset(t, dnatInFilter), p, nil), fwstate.Unknown, nil, "outside the nat table")

	v6 := "*nat\n:PREROUTING ACCEPT [0:0]\n:INPUT ACCEPT [0:0]\n:OUTPUT ACCEPT [0:0]\n:POSTROUTING ACCEPT [0:0]\n" +
		"-A PREROUTING -p tcp -m tcp --dport 80 -j DNAT --to-destination [2001:db8::2]:8080\n" +
		"-A PREROUTING -p tcp -m tcp --dport 81 -j DNAT --to-destination 2001:db8::3\n" +
		"-A PREROUTING -p tcp -m tcp --dport 82 -j DNAT --to-destination 192.0.2.9\nCOMMIT\n"
	p.Family, p.Src, p.Dst = fwstate.V6, netip.MustParseAddr("2001:db8:1::7"), netip.MustParseAddr("2001:db8::1")
	var asked []fwstate.Packet
	route := func(q fwstate.Packet) (string, string, error) {
		asked = append(asked, q)
		return "forward", "eth1", nil
	}
	for _, port := range []uint16{80, 81} {
		p.DstPort = port
		evalCheck(t, Evaluate(evalRuleset(t, v6), p, route), fwstate.Accept, nil, "")
	}
	if len(asked) != 2 || asked[0].Dst != netip.MustParseAddr("2001:db8::2") || asked[0].DstPort != 8080 ||
		asked[1].Dst != netip.MustParseAddr("2001:db8::3") || asked[1].DstPort != 81 {
		t.Fatalf("v6 DNAT asked %+v", asked)
	}
	p.DstPort = 82
	evalCheck(t, Evaluate(evalRuleset(t, v6), p, route), fwstate.Unknown, nil, "not a v6 address")
}
