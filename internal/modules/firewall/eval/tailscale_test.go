package eval

import (
	"net/netip"
	"os"
	"path/filepath"
	"testing"

	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

// tailscaleRuleset reads one hand-written fixture from testdata/tailscale.
func tailscaleRuleset(t *testing.T, name string) *netfilter.Ruleset {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "tailscale", name)) //nolint:gosec // G304: the path is built from constants of this test.
	if err != nil {
		t.Fatal(err)
	}
	return evalRuleset(t, string(data))
}

// tailscaleCase is one packet and the verdict a fixture must give it; a
// nil trace is not compared.
type tailscaleCase struct {
	name    string
	edit    func(p *fwstate.Packet)
	verdict fwstate.Verdict
	trace   []string
	reason  string
}

func tailscaleRun(t *testing.T, fixture string, cases []tailscaleCase) {
	t.Helper()
	rs := tailscaleRuleset(t, fixture)
	for _, c := range cases {
		t.Run(fixture+"/"+c.name, func(t *testing.T) {
			p := evalPacket()
			c.edit(&p)
			evalCheck(t, Evaluate(rs, p, nil), c.verdict, c.trace, c.reason)
		})
	}
}

func tailscaleAddr(s string) netip.Addr { return netip.MustParseAddr(s) }

// tailscaleV4Cases are the expectations both IPv4 fixtures share; inputRule
// is the position of ts-input's tailscale0 ACCEPT, cgnatRule that of its
// CGNAT DROP and portRule that of the WireGuard port rule.
func tailscaleV4Cases(inputRule, cgnatRule, portRule string) []tailscaleCase {
	fromTailnet := func(p *fwstate.Packet) {
		p.InIface, p.Src, p.Dst = "tailscale0", tailscaleAddr("100.100.1.2"), tailscaleAddr("100.101.102.103")
	}
	return []tailscaleCase{
		{
			name:    "ts-input accepts anything arriving on tailscale0, before the host's firewall",
			edit:    func(p *fwstate.Packet) { fromTailnet(p); p.DstPort = 3000 },
			verdict: fwstate.Accept,
			trace:   []string{"filter/INPUT#1", "filter/ts-input#" + inputRule},
		},
		{
			name:    "the host's firewall still governs eth0",
			edit:    func(p *fwstate.Packet) { p.DstPort = 3000 },
			verdict: fwstate.Reject,
			trace:   []string{"filter/INPUT#1", "filter/INPUT#5"},
		},
		{
			name:    "ssh on eth0",
			edit:    func(*fwstate.Packet) {},
			verdict: fwstate.Accept,
			trace:   []string{"filter/INPUT#1", "filter/INPUT#4"},
		},
		{
			name:    "a CGNAT source on another interface is dropped",
			edit:    func(p *fwstate.Packet) { p.Src = tailscaleAddr("100.100.1.2") },
			verdict: fwstate.Drop,
			reason:  "filter/ts-input#" + cgnatRule + " drops",
		},
		{
			name:    "ChromeOS's range falls through to the host's firewall",
			edit:    func(p *fwstate.Packet) { p.Src = tailscaleAddr("100.115.92.5") },
			verdict: fwstate.Accept,
		},
		{
			name: "the WireGuard port is open on every interface",
			edit: func(p *fwstate.Packet) {
				p.Proto, p.SrcPort, p.DstPort = "udp", 41641, 41641
			},
			verdict: fwstate.Accept,
			trace:   []string{"filter/INPUT#1", "filter/ts-input#" + portRule},
		},
		{
			name:    "the tailnet address on lo",
			edit:    func(p *fwstate.Packet) { p.InIface, p.Src = "lo", tailscaleAddr("100.101.102.103") },
			verdict: fwstate.Accept,
			trace:   []string{"filter/INPUT#1", "filter/ts-input#1"},
		},
		{
			name: "subnet routing forwards from tailscale0 and masquerades",
			edit: func(p *fwstate.Packet) {
				fromTailnet(p)
				p.Path, p.OutIface, p.Dst = "forward", "eth1", tailscaleAddr("192.168.1.20")
			},
			verdict: fwstate.Accept,
			trace: []string{"filter/FORWARD#1", "filter/ts-forward#1", "filter/ts-forward#2",
				"nat/POSTROUTING#1", "nat/ts-postrouting#1"},
		},
		{
			name: "an exit node forwards from tailscale0 to the internet",
			edit: func(p *fwstate.Packet) {
				fromTailnet(p)
				p.Path, p.OutIface, p.Dst, p.DstPort = "forward", "eth0", tailscaleAddr("198.51.100.2"), 443
			},
			verdict: fwstate.Accept,
		},
		{
			name: "stateful filtering drops a new connection forwarded into tailscale0",
			edit: func(p *fwstate.Packet) {
				p.Path, p.InIface, p.OutIface = "forward", "eth1", "tailscale0"
				p.Src, p.Dst = tailscaleAddr("192.168.1.20"), tailscaleAddr("100.100.1.2")
			},
			verdict: fwstate.Drop,
			trace:   []string{"filter/FORWARD#1", "filter/ts-forward#4"},
			reason:  "filter/ts-forward#4 drops",
		},
		{
			name: "a CGNAT source is never forwarded into tailscale0",
			edit: func(p *fwstate.Packet) {
				p.Path, p.InIface, p.OutIface = "forward", "eth1", "tailscale0"
				p.Src, p.Dst = tailscaleAddr("100.80.0.1"), tailscaleAddr("100.100.1.2")
			},
			verdict: fwstate.Drop,
			reason:  "filter/ts-forward#3 drops",
		},
		{
			name: "forwarding between other interfaces falls through to the policy",
			edit: func(p *fwstate.Packet) {
				p.Path, p.InIface, p.OutIface = "forward", "eth0", "eth1"
				p.Dst = tailscaleAddr("192.168.1.20")
			},
			verdict: fwstate.Drop,
			reason:  "filter/FORWARD policy DROP",
		},
		{
			name: "tailscaled's own marked traffic leaves",
			edit: func(p *fwstate.Packet) {
				p.Path, p.InIface, p.OutIface, p.Proto = "output", "", "eth0", "udp"
				p.Src, p.Dst, p.SrcPort, p.DstPort = tailscaleAddr("192.0.2.1"), tailscaleAddr("198.51.100.2"), 41641, 3478
				p.Mark = 0x80000
			},
			verdict: fwstate.Accept,
		},
		{
			name: "an unmarked local connection leaves",
			edit: func(p *fwstate.Packet) {
				p.Path, p.InIface, p.OutIface = "output", "", "eth0"
				p.Src, p.Dst, p.SrcPort, p.DstPort = tailscaleAddr("192.0.2.1"), tailscaleAddr("198.51.100.2"), 50000, 443
			},
			verdict: fwstate.Accept,
		},
	}
}

func TestTailscale182(t *testing.T) {
	tailscaleRun(t, "v1.82-v4.save", append(tailscaleV4Cases("4", "3", "5"),
		tailscaleCase{
			name: "an established connection forwarded into tailscale0",
			edit: func(p *fwstate.Packet) {
				p.Path, p.InIface, p.OutIface, p.CtState = "forward", "eth1", "tailscale0", "ESTABLISHED"
				p.Src, p.Dst = tailscaleAddr("192.168.1.20"), tailscaleAddr("100.100.1.2")
			},
			verdict: fwstate.Accept,
			trace:   []string{"filter/FORWARD#1", "filter/ts-forward#5"},
		},
	))
}

func TestTailscale1102(t *testing.T) {
	tailscaleRun(t, "v1.102-v4.save", append(tailscaleV4Cases("2", "5", "3"),
		tailscaleCase{
			// The CONNMARK restore may copy an earlier packet's connection
			// mark, so ts-forward's mark match cannot be decided; the guard
			// evaluates new connections only.
			name: "an established connection forwarded into tailscale0 is unknown",
			edit: func(p *fwstate.Packet) {
				p.Path, p.InIface, p.OutIface, p.CtState = "forward", "eth1", "tailscale0", "ESTABLISHED"
				p.Src, p.Dst = tailscaleAddr("192.168.1.20"), tailscaleAddr("100.100.1.2")
			},
			verdict: fwstate.Unknown,
			reason:  "filter/ts-forward#2: the match on mark 0x40000/0xff0000",
		},
		tailscaleCase{
			name: "tailscaled's mark is saved to the connection",
			edit: func(p *fwstate.Packet) {
				p.Path, p.InIface, p.OutIface, p.Proto = "output", "", "eth0", "udp"
				p.Src, p.Dst, p.SrcPort, p.DstPort = tailscaleAddr("192.0.2.1"), tailscaleAddr("198.51.100.2"), 41641, 3478
				p.Mark = 0x80000
			},
			verdict: fwstate.Accept,
			trace:   []string{"mangle/OUTPUT#1", "nat/POSTROUTING#1"},
		},
	))

	v6 := func(p *fwstate.Packet) {
		p.Family, p.Src, p.Dst = fwstate.V6, tailscaleAddr("2001:db8:1::7"), tailscaleAddr("2001:db8::1")
	}
	tailscaleRun(t, "v1.102-v6.save", []tailscaleCase{
		{
			name: "ts-input accepts anything arriving on tailscale0",
			edit: func(p *fwstate.Packet) {
				v6(p)
				p.InIface, p.Src, p.Dst, p.DstPort = "tailscale0", tailscaleAddr("fd7a:115c:a1e0::2"), tailscaleAddr("fd7a:115c:a1e0::1"), 3000
			},
			verdict: fwstate.Accept,
			trace:   []string{"filter/INPUT#1", "filter/ts-input#2"},
		},
		{
			name:    "the host's firewall governs eth0",
			edit:    func(p *fwstate.Packet) { v6(p); p.DstPort = 3000 },
			verdict: fwstate.Reject,
		},
		{
			name: "subnet routing forwards from tailscale0",
			edit: func(p *fwstate.Packet) {
				v6(p)
				p.Path, p.InIface, p.OutIface, p.Src = "forward", "tailscale0", "eth1", tailscaleAddr("fd7a:115c:a1e0::2")
			},
			verdict: fwstate.Accept,
			trace:   []string{"filter/FORWARD#1", "filter/ts-forward#1", "filter/ts-forward#2", "nat/POSTROUTING#1", "nat/ts-postrouting#1"},
		},
		{
			name: "stateful filtering drops a new connection forwarded into tailscale0",
			edit: func(p *fwstate.Packet) {
				v6(p)
				p.Path, p.InIface, p.OutIface, p.Dst = "forward", "eth1", "tailscale0", tailscaleAddr("fd7a:115c:a1e0::2")
			},
			verdict: fwstate.Drop,
			reason:  "filter/ts-forward#3 drops",
		},
	})
}
