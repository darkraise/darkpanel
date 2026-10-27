package eval

import (
	"net/netip"
	"testing"

	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
)

const markIncoming = `*mangle
:PREROUTING ACCEPT [0:0]
:INPUT ACCEPT [0:0]
:FORWARD ACCEPT [0:0]
:OUTPUT ACCEPT [0:0]
:POSTROUTING ACCEPT [0:0]
-A PREROUTING -i wg0 -j MARK --set-xmark 0x100/0xff00
-A PREROUTING -m conntrack --ctstate RELATED,ESTABLISHED -m connmark ! --mark 0x0/0xff0000 -j CONNMARK --restore-mark --nfmask 0xff0000 --ctmask 0xff0000
-A PREROUTING -i wg0 -j CONNMARK --save-mark --nfmask 0xff00 --ctmask 0xff00
-A PREROUTING -i wg1 -j CONNMARK --set-xmark 0x7/0xff
-A PREROUTING -i wg2 -m limit --limit 1/sec -j MARK --set-xmark 0x1/0xff
COMMIT
*filter
:INPUT DROP [0:0]
:FORWARD DROP [0:0]
:OUTPUT ACCEPT [0:0]
-A INPUT -p tcp -m mark --mark 0x100/0xff00 -j ACCEPT
-A INPUT -p udp -m connmark --mark 0x100/0xff00 -j ACCEPT
-A INPUT -p tcp -m tcp --dport 22 -m mark --mark 0x0/0xff00 -j ACCEPT
-A INPUT -p tcp -m tcp --dport 2222 -m mark --mark 0x5/0xff -j ACCEPT
-A INPUT -p udp -m connmark --mark 0x7/0xff -j ACCEPT
-A INPUT -p tcp -m tcp --dport 2223 -m mark --mark zz -j ACCEPT
-A FORWARD -m mark --mark 0x40000/0xff0000 -j ACCEPT
-A FORWARD -j DROP
COMMIT
`

const markOutgoing = `*mangle
:PREROUTING ACCEPT [0:0]
:INPUT ACCEPT [0:0]
:FORWARD ACCEPT [0:0]
:OUTPUT ACCEPT [0:0]
:POSTROUTING ACCEPT [0:0]
-A OUTPUT -m conntrack --ctstate NEW -m mark ! --mark 0x0/0xff0000 -j CONNMARK --save-mark --nfmask 0xff0000 --ctmask 0xff0000
-A OUTPUT -p udp -m udp --dport 3478 -j MARK --set-xmark 0x80000/0xff0000
COMMIT
*filter
:INPUT ACCEPT [0:0]
:FORWARD ACCEPT [0:0]
:OUTPUT DROP [0:0]
-A OUTPUT -o lo -j ACCEPT
-A OUTPUT -p tcp -m tcp --dport 443 -m mark --mark 0x0/0xff0000 -j ACCEPT
-A OUTPUT -m mark --mark 0x80000/0xff0000 -j ACCEPT
COMMIT
`

// markRouter keeps the packet on the path and out-interface it was routed
// to before, and records what it was asked.
type markRouter struct{ asked []fwstate.Packet }

func (r *markRouter) route(p fwstate.Packet) (string, string, error) {
	r.asked = append(r.asked, p)
	return p.Path, p.OutIface, nil
}

func TestEvaluateMarks(t *testing.T) {
	in := evalRuleset(t, markIncoming)
	out := evalRuleset(t, markOutgoing)
	output := func(edit func(p *fwstate.Packet)) func(p *fwstate.Packet) {
		return func(p *fwstate.Packet) {
			p.Path, p.InIface, p.OutIface = "output", "", "eth0"
			p.Src, p.Dst, p.DstPort = netip.MustParseAddr("192.0.2.1"), netip.MustParseAddr("198.51.100.2"), 443
			edit(p)
		}
	}
	forward := func(p *fwstate.Packet) { p.Path, p.InIface, p.OutIface = "forward", "eth1", "eth2" }
	tests := []struct {
		name    string
		rs      string
		edit    func(p *fwstate.Packet)
		verdict fwstate.Verdict
		trace   []string
		reason  string
	}{
		{
			name:    "a MARK rule sets the bits a later match needs",
			edit:    func(p *fwstate.Packet) { p.InIface = "wg0" },
			verdict: fwstate.Accept,
			trace:   []string{"mangle/PREROUTING#1", "mangle/PREROUTING#3", "filter/INPUT#1"},
		},
		{
			name:    "save-mark copies the mark into the connection mark",
			edit:    func(p *fwstate.Packet) { p.InIface, p.Proto = "wg0", "udp" },
			verdict: fwstate.Accept,
			trace:   []string{"mangle/PREROUTING#1", "mangle/PREROUTING#3", "filter/INPUT#2"},
		},
		{
			name:    "an incoming packet's mark is zero, so a zero match holds",
			edit:    func(*fwstate.Packet) {},
			verdict: fwstate.Accept,
			trace:   []string{"filter/INPUT#3"},
		},
		{
			name:    "an incoming packet's mark is zero, so a non-zero match misses",
			edit:    func(p *fwstate.Packet) { p.DstPort = 2222 },
			verdict: fwstate.Drop,
			reason:  "filter/INPUT policy DROP",
		},
		{
			name:    "a new connection's mark is zero",
			edit:    func(p *fwstate.Packet) { p.Proto = "udp" },
			verdict: fwstate.Drop,
		},
		{
			name:    "CONNMARK --set-xmark sets the connection mark",
			edit:    func(p *fwstate.Packet) { p.InIface, p.Proto = "wg1", "udp" },
			verdict: fwstate.Accept,
			trace:   []string{"mangle/PREROUTING#4", "filter/INPUT#5"},
		},
		{
			name:    "an uncertain MARK rule is unknown",
			edit:    func(p *fwstate.Packet) { p.InIface = "wg2" },
			verdict: fwstate.Unknown,
			reason:  "mangle/PREROUTING#5: match limit",
		},
		{
			name:    "a mark value that is not a number is unknown",
			edit:    func(p *fwstate.Packet) { p.DstPort = 2223 },
			verdict: fwstate.Unknown,
			reason:  `filter/INPUT#6: mark "zz"`,
		},
		{
			name:    "a given incoming mark is known",
			edit:    func(p *fwstate.Packet) { forward(p); p.Mark = 0x40000 },
			verdict: fwstate.Accept,
			trace:   []string{"filter/FORWARD#1"},
		},
		{
			name:    "a forwarded new connection without the mark is dropped",
			edit:    forward,
			verdict: fwstate.Drop,
			trace:   []string{"filter/FORWARD#2"},
		},
		{
			name: "a mark restored from an earlier packet's connection mark is unknown",
			edit: func(p *fwstate.Packet) { forward(p); p.CtState = "ESTABLISHED" },
			// The restore rule may or may not run, and copies bits nothing on
			// this path set, so the FORWARD mark match cannot be decided.
			verdict: fwstate.Unknown,
			reason:  "filter/FORWARD#1: the match on mark 0x40000/0xff0000 needs bits only a non-netfilter source could set",
		},
		{
			name:    "a restored mark that no match reads is harmless",
			edit:    func(p *fwstate.Packet) { p.CtState = "ESTABLISHED" },
			verdict: fwstate.Accept,
			trace:   []string{"filter/INPUT#3"},
		},
		{
			name:    "output: lo is accepted before any mark match",
			rs:      "out",
			edit:    output(func(p *fwstate.Packet) { p.OutIface = "lo" }),
			verdict: fwstate.Accept,
			trace:   []string{"filter/OUTPUT#1"},
		},
		{
			name:    "output: a zero match on an unmarked local packet holds",
			rs:      "out",
			edit:    output(func(*fwstate.Packet) {}),
			verdict: fwstate.Accept,
			trace:   []string{"filter/OUTPUT#2"},
		},
		{
			name:    "output: a non-zero match on an unmarked local packet is unknown",
			rs:      "out",
			edit:    output(func(p *fwstate.Packet) { p.Proto, p.DstPort = "udp", 53 }),
			verdict: fwstate.Unknown,
			reason:  "filter/OUTPUT#3: the match on mark 0x80000/0xff0000 needs bits only a non-netfilter source could set",
		},
		{
			name:    "output: a socket's mark is known",
			rs:      "out",
			edit:    output(func(p *fwstate.Packet) { p.Proto, p.DstPort, p.Mark = "udp", 41641, 0x80000 }),
			verdict: fwstate.Accept,
			trace:   []string{"mangle/OUTPUT#1", "filter/OUTPUT#3"},
		},
		{
			name:    "output: a MARK rule in mangle OUTPUT sets the mark",
			rs:      "out",
			edit:    output(func(p *fwstate.Packet) { p.Proto, p.DstPort = "udp", 3478 }),
			verdict: fwstate.Accept,
			trace:   []string{"mangle/OUTPUT#2", "filter/OUTPUT#3"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := evalPacket()
			tt.edit(&p)
			rs := in
			if tt.rs == "out" {
				rs = out
			}
			r := &markRouter{}
			evalCheck(t, Evaluate(rs, p, r.route), tt.verdict, tt.trace, tt.reason)
		})
	}
}

func TestEvaluateMarkReroutes(t *testing.T) {
	r := &markRouter{}
	p := evalPacket()
	p.Path, p.InIface, p.OutIface = "output", "", "eth0"
	p.Src, p.Proto, p.DstPort = netip.MustParseAddr("192.0.2.1"), "udp", 3478
	Evaluate(evalRuleset(t, markOutgoing), p, r.route)
	if len(r.asked) != 1 || r.asked[0].Mark != 0x80000 {
		t.Fatalf("router asked %+v, want one lookup with mark 0x80000", r.asked)
	}

	r = &markRouter{}
	p.DstPort = 443
	Evaluate(evalRuleset(t, markOutgoing), p, r.route)
	if len(r.asked) != 0 {
		t.Fatalf("router asked %+v without a mark change, want none", r.asked)
	}

	p = evalPacket()
	p.InIface = "wg0"
	evalCheck(t, Evaluate(evalRuleset(t, markIncoming), p, nil), fwstate.Unknown, nil,
		"the mark was changed to 0x100 and no router was given")
}
