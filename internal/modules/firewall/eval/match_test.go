package eval

import (
	"net/netip"
	"testing"

	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

// matchFilter is a filter table whose built-in chains drop by policy and
// accept what the one rule spec matches.
func matchFilter(spec string) string {
	return "*filter\n:INPUT DROP [0:0]\n:FORWARD DROP [0:0]\n:OUTPUT DROP [0:0]\n" +
		"-A INPUT " + spec + " -j ACCEPT\n" +
		"-A FORWARD " + spec + " -j ACCEPT\n" +
		"-A OUTPUT " + spec + " -j ACCEPT\n" +
		"COMMIT\n"
}

func matchForward(p *fwstate.Packet) {
	p.Path, p.OutIface, p.Dst = "forward", "eth1", netip.MustParseAddr("198.51.100.2")
}

func matchOutput(out string) func(p *fwstate.Packet) {
	return func(p *fwstate.Packet) {
		p.Path, p.InIface, p.OutIface = "output", "", out
		p.Src, p.Dst = netip.MustParseAddr("192.0.2.1"), netip.MustParseAddr("198.51.100.2")
	}
}

func matchV6(p *fwstate.Packet) {
	p.Family = fwstate.V6
	p.Src, p.Dst = netip.MustParseAddr("2001:db8:1::7"), netip.MustParseAddr("2001:db8::1")
}

func TestEvaluateMatches(t *testing.T) {
	none := func(*fwstate.Packet) {}
	established := func(p *fwstate.Packet) { p.CtState = "ESTABLISHED" }
	udp := func(p *fwstate.Packet) { p.Proto, p.SrcPort, p.DstPort = "udp", 67, 68 }
	port := func(n uint16) func(p *fwstate.Packet) { return func(p *fwstate.Packet) { p.DstPort = n } }
	dst := func(a string) func(p *fwstate.Packet) {
		return func(p *fwstate.Packet) { p.Dst = netip.MustParseAddr(a) }
	}
	tests := []struct {
		name    string
		spec    string
		edit    func(p *fwstate.Packet)
		verdict fwstate.Verdict
		reason  string
	}{
		{"tcp destination port", "-p tcp -m tcp --dport 22", none, fwstate.Accept, ""},
		{"tcp port range", "-p tcp -m tcp --dport 2000:3000", port(2222), fwstate.Accept, ""},
		{"tcp port outside the range", "-p tcp -m tcp --dport 2000:3000", none, fwstate.Drop, ""},
		{"tcp open-ended range", "-p tcp -m tcp --dport 1024:", port(8080), fwstate.Accept, ""},
		{"negated tcp port", "-p tcp -m tcp ! --dport 22", none, fwstate.Drop, ""},
		{"tcp source port", "-p tcp -m tcp --sport 40000", none, fwstate.Accept, ""},
		{"udp module on a tcp packet", "-m udp --dport 22", none, fwstate.Drop, ""},
		{"udp source and destination ports", "-p udp -m udp --sport 67 --dport 68", udp, fwstate.Accept, ""},
		{"a port iptables-save never prints", "-p tcp -m tcp --dport ssh", none, fwstate.Unknown, `port "ssh"`},
		{"SYN of a new connection", "-p tcp -m tcp --tcp-flags FIN,SYN,RST,ACK SYN", none, fwstate.Accept, ""},
		{"negated SYN of a new connection", "-p tcp -m tcp ! --tcp-flags SYN,RST SYN", none, fwstate.Drop, ""},
		{"ALL flags", "-p tcp -m tcp --tcp-flags ALL NONE", none, fwstate.Drop, ""},
		{"flags of a later packet", "-p tcp -m tcp --tcp-flags SYN,RST SYN", established, fwstate.Unknown, "first packet"},
		{"multiport list", "-p tcp -m multiport --dports 443,8000:8100", port(8080), fwstate.Accept, ""},
		{"multiport miss", "-p tcp -m multiport --dports 443,8000:8100", none, fwstate.Drop, ""},
		{"negated multiport", "-p tcp -m multiport ! --dports 443", none, fwstate.Accept, ""},
		{"multiport source ports", "-p tcp -m multiport --sports 1:1023,40000", none, fwstate.Accept, ""},
		{"multiport on icmp", "-m multiport --dports 22", func(p *fwstate.Packet) { p.Proto = "icmp" }, fwstate.Drop, ""},
		{"icmp type on tcp", "-p icmp -m icmp --icmp-type 8", none, fwstate.Drop, ""},
		{"icmp type on icmp", "-p icmp -m icmp --icmp-type 8", func(p *fwstate.Packet) { p.Proto = "icmp" }, fwstate.Unknown, "ICMP types"},
		{"comment", `-m comment --comment "darkpanel:01J00000000000000000000000 ssh"`, none, fwstate.Accept, ""},
		{"ctstate NEW", "-m conntrack --ctstate NEW", none, fwstate.Accept, ""},
		{"ctstate established on a new packet", "-m conntrack --ctstate RELATED,ESTABLISHED", none, fwstate.Drop, ""},
		{"ctstate established", "-m conntrack --ctstate RELATED,ESTABLISHED", established, fwstate.Accept, ""},
		{"negated ctstate", "-m conntrack ! --ctstate RELATED,ESTABLISHED", none, fwstate.Accept, ""},
		{"ctstate DNAT without DNAT", "-m conntrack --ctstate DNAT", none, fwstate.Drop, ""},
		{"ctstate DNAT of a later packet", "-m conntrack --ctstate DNAT", established, fwstate.Unknown, "NAT state"},
		{"ctstate decided without DNAT", "-m conntrack --ctstate ESTABLISHED,DNAT", established, fwstate.Accept, ""},
		{"original destination", "-m conntrack --ctorigdst 192.0.2.1 --ctorigdstport 22", none, fwstate.Accept, ""},
		{"original destination prefix", "-m conntrack --ctorigdst 192.0.2.0/24", none, fwstate.Accept, ""},
		{"negated original port", "-m conntrack ! --ctorigdstport 22", none, fwstate.Drop, ""},
		{"original port range", "-m conntrack --ctorigdstport 20:30", none, fwstate.Accept, ""},
		{"original destination of a later packet", "-m conntrack --ctorigdst 192.0.2.1", established, fwstate.Unknown, "original destination"},
		{"state module", "-m state --state NEW", none, fwstate.Accept, ""},
		{"local destination on input", "-m addrtype --dst-type LOCAL", none, fwstate.Accept, ""},
		{"local destination on forward", "-m addrtype --dst-type LOCAL", matchForward, fwstate.Drop, ""},
		{"negated local destination on forward", "-m addrtype ! --dst-type LOCAL", matchForward, fwstate.Accept, ""},
		{"unicast destination on forward", "-m addrtype --dst-type UNICAST", matchForward, fwstate.Accept, ""},
		{"broadcast destination", "-m addrtype --dst-type BROADCAST", dst("255.255.255.255"), fwstate.Accept, ""},
		{"local unicast is not broadcast", "-m addrtype --dst-type BROADCAST", none, fwstate.Drop, ""},
		{"multicast destination", "-m addrtype --dst-type MULTICAST", dst("224.0.0.251"), fwstate.Accept, ""},
		{"type list", "-m addrtype --dst-type MULTICAST,LOCAL", none, fwstate.Accept, ""},
		{"local destination on output through lo", "-m addrtype --dst-type LOCAL", matchOutput("lo"), fwstate.Accept, ""},
		{"remote destination on output", "-m addrtype --dst-type LOCAL", matchOutput("eth0"), fwstate.Drop, ""},
		{"source from another host", "-m addrtype --src-type LOCAL", none, fwstate.Drop, ""},
		{"source on lo", "-m addrtype --src-type LOCAL", func(p *fwstate.Packet) { p.InIface = "lo" }, fwstate.Accept, ""},
		{"source of a local packet", "-m addrtype --src-type LOCAL", matchOutput("eth0"), fwstate.Accept, ""},
		{"unicast source", "-m addrtype --src-type UNICAST", none, fwstate.Unknown, "route back"},
		{"unmodelled address type", "-m addrtype --dst-type BLACKHOLE", none, fwstate.Unknown, "BLACKHOLE"},
		{"no routing header", "-m rt --rt-type 0", matchV6, fwstate.Drop, ""},
		{"negated routing header type", "-m rt ! --rt-type 0", matchV6, fwstate.Drop, ""},
		{"hop limit", "-m hl --hl-eq 255", matchV6, fwstate.Unknown, "hop limit"},
		{"recent", "-m recent --rcheck --seconds 30 --name DEFAULT --mask 255.255.255.255 --rsource", none, fwstate.Unknown, "match recent"},
		{"statistic", "-m statistic --mode random --probability 0.50000000000", none, fwstate.Unknown, "match statistic"},
		{"limit", "-m limit --limit 3/min --limit-burst 10", none, fwstate.Unknown, "match limit"},
		{"a false match wins over an unknown one", "-m limit --limit 3/min -p tcp -m tcp --dport 80", none, fwstate.Drop, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := evalPacket()
			tt.edit(&p)
			evalCheck(t, Evaluate(evalRuleset(t, matchFilter(tt.spec)), p, nil), tt.verdict, nil, tt.reason)
		})
	}
}

func TestMatchConntrackInRaw(t *testing.T) {
	w, err := newWalker(evalRuleset(t, matchFilter("-p tcp")), evalPacket(), nil)
	if err != nil {
		t.Fatal(err)
	}
	r := &netfilter.Rule{Matches: []netfilter.Match{{
		Module:  "conntrack",
		Options: []netfilter.Option{{Name: "ctstate", Values: []string{"NEW"}}},
	}}}
	if got, why := w.modules(&netfilter.Table{Name: "raw"}, r); got != maybe || why == "" {
		t.Fatalf("conntrack in raw = %v %q, want maybe with a reason", got, why)
	}
	if got, _ := w.modules(&netfilter.Table{Name: "filter"}, r); got != yes {
		t.Fatalf("conntrack in filter = %v, want yes", got)
	}
}
