package eval

import (
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

// evalRuleset parses save output for the tests of this package.
func evalRuleset(t *testing.T, text string) *netfilter.Ruleset {
	t.Helper()
	rs, err := netfilter.Parse(strings.NewReader(text))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return rs
}

// evalPacket is the first packet of a new TCP connection from
// 203.0.113.7:40000 to this host's 192.0.2.1:22, arriving on eth0.
func evalPacket() fwstate.Packet {
	return fwstate.Packet{
		Family:  fwstate.V4,
		Path:    "input",
		InIface: "eth0",
		Src:     netip.MustParseAddr("203.0.113.7"),
		Dst:     netip.MustParseAddr("192.0.2.1"),
		SrcPort: 40000,
		DstPort: 22,
		Proto:   "tcp",
		CtState: "NEW",
	}
}

// evalCheck compares a result with the wanted verdict and trace, and checks
// that the reason contains the wanted fragment ("" checks nothing). A nil
// trace is not compared.
func evalCheck(t *testing.T, got fwstate.Result, verdict fwstate.Verdict, trace []string, reason string) {
	t.Helper()
	if got.Verdict != verdict {
		t.Errorf("verdict = %q (reason %q, trace %q), want %q", got.Verdict, got.Reason, got.Trace, verdict)
	}
	if trace != nil && !slices.Equal(got.Trace, trace) {
		t.Errorf("trace = %q, want %q", got.Trace, trace)
	}
	if !strings.Contains(got.Reason, reason) {
		t.Errorf("reason = %q, want it to contain %q", got.Reason, reason)
	}
}

const walkFilter = `*filter
:INPUT DROP [0:0]
:FORWARD DROP [0:0]
:OUTPUT ACCEPT [0:0]
:walk-user - [0:0]
:walk-return - [0:0]
:walk-loop - [0:0]
-A INPUT -i lo -j ACCEPT
-A INPUT -j walk-return
-A INPUT -p tcp -m comment --comment "the user chain" -j walk-user
-A INPUT -p udp -j REJECT --reject-with icmp-port-unreachable
-A INPUT -p esp -m limit --limit 3/min -j LOG --log-prefix "[ESP] "
-A INPUT -p esp -j ACCEPT
-A INPUT -p gre -m limit --limit 3/min -j ACCEPT
-A INPUT -p ah -m hashlimit --hashlimit-upto 1/sec -j ACCEPT
-A INPUT -p 132 -j ACCEPT
-A INPUT -p icmp -j walk-loop
-A FORWARD -i eth+ ! -o eth0 -j ACCEPT
-A FORWARD -s 10.0.0.0/8 ! -d 10.1.0.0/16 -j ACCEPT
-A OUTPUT -i eth0 -j DROP
-A OUTPUT -o lo -j ACCEPT
-A OUTPUT -d 198.51.100.0/24 -j RETURN
-A OUTPUT -p tcp -j DROP
-A walk-return -s 203.0.113.0/24 -j RETURN
-A walk-return -s 198.51.100.0/24 -j DROP
-A walk-user -p tcp -m limit --limit 1/sec -j LOG --log-prefix "[SSH] "
-A walk-user ! -s 203.0.113.0/24 -p tcp -j ACCEPT
-A walk-loop -j walk-loop
COMMIT
`

func TestEvaluateWalk(t *testing.T) {
	rs := evalRuleset(t, walkFilter)
	tests := []struct {
		name    string
		edit    func(p *fwstate.Packet)
		verdict fwstate.Verdict
		trace   []string
		reason  string
	}{
		{
			name:    "loopback accepted by the first rule",
			edit:    func(p *fwstate.Packet) { p.InIface = "lo" },
			verdict: fwstate.Accept,
			trace:   []string{"filter/INPUT#1"},
		},
		{
			name: "a user chain returns and the built-in policy drops",
			edit: func(*fwstate.Packet) {},
			// walk-return returns for 203.0.113.0/24; walk-user's rate-limited
			// LOG rule may or may not match, which cannot change the verdict,
			// and its ACCEPT excludes the source.
			verdict: fwstate.Drop,
			trace:   []string{"filter/INPUT#2", "filter/walk-return#1", "filter/INPUT#3"},
			reason:  "filter/INPUT policy DROP",
		},
		{
			name:    "a user chain accepts",
			edit:    func(p *fwstate.Packet) { p.Src = netip.MustParseAddr("192.0.2.77") },
			verdict: fwstate.Accept,
			trace:   []string{"filter/INPUT#2", "filter/INPUT#3", "filter/walk-user#2"},
		},
		{
			name:    "a user chain drops",
			edit:    func(p *fwstate.Packet) { p.Src = netip.MustParseAddr("198.51.100.9") },
			verdict: fwstate.Drop,
			trace:   []string{"filter/INPUT#2", "filter/walk-return#2"},
			reason:  "filter/walk-return#2 drops",
		},
		{
			name:    "reject",
			edit:    func(p *fwstate.Packet) { p.Proto = "udp" },
			verdict: fwstate.Reject,
			trace:   []string{"filter/INPUT#2", "filter/walk-return#1", "filter/INPUT#4"},
			reason:  "filter/INPUT#4 rejects",
		},
		{
			name: "a rate-limited LOG continues and the next rule accepts",
			edit: func(p *fwstate.Packet) { p.Proto = "esp" },
			// The LOG rule's limit makes its match uncertain, so it is not
			// traced, but LOG never decides.
			verdict: fwstate.Accept,
			trace:   []string{"filter/INPUT#2", "filter/walk-return#1", "filter/INPUT#6"},
		},
		{
			name:    "a rate-limited ACCEPT is unknown",
			edit:    func(p *fwstate.Packet) { p.Proto = "gre" },
			verdict: fwstate.Unknown,
			reason:  "filter/INPUT#7: ",
		},
		{
			name:    "an opaque rule whose header matches is unknown",
			edit:    func(p *fwstate.Packet) { p.Proto = "ah" },
			verdict: fwstate.Unknown,
			reason:  "filter/INPUT#8: match hashlimit",
		},
		{
			name:    "a protocol number matches its name",
			edit:    func(p *fwstate.Packet) { p.Proto = "SCTP" },
			verdict: fwstate.Accept,
			trace:   []string{"filter/INPUT#2", "filter/walk-return#1", "filter/INPUT#9"},
		},
		{
			name:    "a jump loop is unknown",
			edit:    func(p *fwstate.Packet) { p.Proto = "icmp" },
			verdict: fwstate.Unknown,
			reason:  "jumps nest deeper than 64",
		},
		{
			name: "forward: wildcard in-interface and negated out-interface",
			edit: func(p *fwstate.Packet) {
				p.Path, p.InIface, p.OutIface = "forward", "eth1", "eth2"
			},
			verdict: fwstate.Accept,
			trace:   []string{"filter/FORWARD#1"},
		},
		{
			name: "forward: negated destination",
			edit: func(p *fwstate.Packet) {
				p.Path, p.InIface, p.OutIface = "forward", "wg0", "eth0"
				p.Src, p.Dst = netip.MustParseAddr("10.2.3.4"), netip.MustParseAddr("10.2.0.1")
			},
			verdict: fwstate.Accept,
			trace:   []string{"filter/FORWARD#2"},
		},
		{
			name: "forward: nothing matches and the policy drops",
			edit: func(p *fwstate.Packet) {
				p.Path, p.InIface, p.OutIface = "forward", "wg0", "eth0"
				p.Src, p.Dst = netip.MustParseAddr("10.2.3.4"), netip.MustParseAddr("10.1.0.1")
			},
			verdict: fwstate.Drop,
			trace:   []string{},
			reason:  "filter/FORWARD policy DROP",
		},
		{
			name: "output: no in-interface, RETURN in a built-in chain applies the policy",
			edit: func(p *fwstate.Packet) {
				p.Path, p.InIface, p.OutIface = "output", "", "eth0"
				p.Src, p.Dst = netip.MustParseAddr("192.0.2.1"), netip.MustParseAddr("198.51.100.4")
			},
			verdict: fwstate.Accept,
			trace:   []string{"filter/OUTPUT#3"},
		},
		{
			name: "output: drop",
			edit: func(p *fwstate.Packet) {
				p.Path, p.InIface, p.OutIface = "output", "", "eth0"
				p.Src, p.Dst = netip.MustParseAddr("192.0.2.1"), netip.MustParseAddr("192.0.2.200")
			},
			verdict: fwstate.Drop,
			trace:   []string{"filter/OUTPUT#4"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := evalPacket()
			tt.edit(&p)
			evalCheck(t, Evaluate(rs, p, nil), tt.verdict, tt.trace, tt.reason)
		})
	}
}

func TestEvaluateTables(t *testing.T) {
	p := evalPacket()
	t.Run("an absent table decides nothing", func(t *testing.T) {
		rs := evalRuleset(t, "*raw\n:PREROUTING ACCEPT [0:0]\n:OUTPUT ACCEPT [0:0]\nCOMMIT\n")
		evalCheck(t, Evaluate(rs, p, nil), fwstate.Accept, []string{}, "")
	})
	t.Run("an incompatible table is unknown", func(t *testing.T) {
		rs := evalRuleset(t, "# Table `filter' is incompatible, use 'nft' tool.\n")
		evalCheck(t, Evaluate(rs, p, nil), fwstate.Unknown, nil, "filter: Table `filter' is incompatible")
	})
	t.Run("no ruleset is unknown", func(t *testing.T) {
		evalCheck(t, Evaluate(nil, p, nil), fwstate.Unknown, nil, "eval: no ruleset")
	})
}

func TestEvaluateInvalidPacket(t *testing.T) {
	rs := evalRuleset(t, walkFilter)
	tests := []struct {
		name   string
		edit   func(p *fwstate.Packet)
		reason string
	}{
		{"family", func(p *fwstate.Packet) { p.Family = "v5" }, `eval: family "v5"`},
		{"address family", func(p *fwstate.Packet) { p.Src = netip.MustParseAddr("2001:db8::1") }, "eval: source 2001:db8::1 is not a v4 address"},
		{"v6 destination", func(p *fwstate.Packet) {
			p.Family, p.Src = fwstate.V6, netip.MustParseAddr("2001:db8::1")
		}, "eval: destination 192.0.2.1 is not a v6 address"},
		{"path", func(p *fwstate.Packet) { p.Path = "prerouting" }, `eval: path "prerouting"`},
		{"input without in-interface", func(p *fwstate.Packet) { p.InIface = "" }, "eval: path input needs an in-interface"},
		{"forward without out-interface", func(p *fwstate.Packet) { p.Path = "forward" }, "eval: path forward needs an out-interface"},
		{"output without out-interface", func(p *fwstate.Packet) { p.Path, p.InIface = "output", "" }, "eval: path output needs an out-interface"},
		{"conntrack state", func(p *fwstate.Packet) { p.CtState = "new" }, `eval: conntrack state "new"`},
		{"protocol", func(p *fwstate.Packet) { p.Proto = "" }, "eval: no protocol"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := evalPacket()
			tt.edit(&p)
			evalCheck(t, Evaluate(rs, p, nil), fwstate.Unknown, []string{}, tt.reason)
		})
	}
}
