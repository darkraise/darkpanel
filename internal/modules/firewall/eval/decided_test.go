package eval

import (
	"errors"
	"net/netip"
	"testing"

	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
)

// dcdDocker is a Docker-shaped ruleset: a DNAT in nat PREROUTING, a jump to
// DOCKER-USER first in FORWARD, a binding chain, and a built-in ACCEPT
// policy in every table the packet passes after the filter hook.
const dcdDocker = `*raw
:PREROUTING ACCEPT [0:0]
:OUTPUT ACCEPT [0:0]
COMMIT
*mangle
:PREROUTING ACCEPT [0:0]
:INPUT ACCEPT [0:0]
:FORWARD ACCEPT [0:0]
:OUTPUT ACCEPT [0:0]
:POSTROUTING ACCEPT [0:0]
COMMIT
*nat
:PREROUTING ACCEPT [0:0]
:INPUT ACCEPT [0:0]
:OUTPUT ACCEPT [0:0]
:POSTROUTING ACCEPT [0:0]
-A PREROUTING -p tcp -m tcp --dport 8080 -j DNAT --to-destination 172.17.0.2:80
COMMIT
*filter
:INPUT DROP [0:0]
:FORWARD DROP [0:0]
:OUTPUT ACCEPT [0:0]
:DOCKER-USER - [0:0]
:dcd-bindings - [0:0]
-A FORWARD -j DOCKER-USER
-A DOCKER-USER -s 203.0.113.0/24 -j dcd-bindings
-A DOCKER-USER -j RETURN
-A dcd-bindings -d 172.17.0.2/32 -p tcp -m tcp --dport 80 -j ACCEPT
-A dcd-bindings -j DROP
COMMIT
*security
:INPUT ACCEPT [0:0]
:FORWARD ACCEPT [0:0]
:OUTPUT ACCEPT [0:0]
COMMIT
`

var errDcdRoute = errors.New("no route")

// dcdCheck evaluates one packet and compares the verdict and Decided.
func dcdCheck(t *testing.T, rulesetText string, p fwstate.Packet, route fwstate.Router, verdict fwstate.Verdict, decided string) {
	t.Helper()
	got := Evaluate(evalRuleset(t, rulesetText), p, route)
	if got.Verdict != verdict {
		t.Fatalf("verdict = %q (reason %q), want %q", got.Verdict, got.Reason, verdict)
	}
	if got.Decided != decided {
		t.Errorf("Decided = %q, want %q (reason %q, trace %q)", got.Decided, decided, got.Reason, got.Trace)
	}
}

// dcdForward is a connection from 203.0.113.7 arriving on eth0 for the host's
// published port.
func dcdForward(port uint16) fwstate.Packet {
	p := evalPacket()
	p.Path, p.InIface, p.OutIface = "forward", "eth0", "docker0"
	p.DstPort = port
	return p
}

func dcdOutput(dst, proto string) fwstate.Packet {
	p := evalPacket()
	p.Path, p.InIface, p.OutIface, p.Proto = "output", "", "eth0", proto
	p.Src, p.Dst = netip.MustParseAddr("192.0.2.1"), netip.MustParseAddr(dst)
	return p
}

func TestDcdDecidedFilterHook(t *testing.T) {
	tests := []struct {
		name    string
		pkt     func() fwstate.Packet
		verdict fwstate.Verdict
		decided string
	}{
		{"accept by the first rule", func() fwstate.Packet {
			p := evalPacket()
			p.InIface = "lo"
			return p
		}, fwstate.Accept, "filter/INPUT#1"},
		{"accept by a rule of the jumped-to chain, not by the jump", func() fwstate.Packet {
			p := evalPacket()
			p.Src = netip.MustParseAddr("192.0.2.77")
			return p
		}, fwstate.Accept, "filter/walk-user#2"},
		{"accept: a LOG rule that may match is not the decider", func() fwstate.Packet {
			p := evalPacket()
			p.Proto = "esp"
			return p
		}, fwstate.Accept, "filter/INPUT#6"},
		{"accept by the policy after a RETURN", func() fwstate.Packet {
			return dcdOutput("198.51.100.4", "tcp")
		}, fwstate.Accept, "filter/OUTPUT policy ACCEPT"},
		{"accept by the policy when no rule matches", func() fwstate.Packet {
			return dcdOutput("192.0.2.9", "udp")
		}, fwstate.Accept, "filter/OUTPUT policy ACCEPT"},
		{"drop by a rule", func() fwstate.Packet {
			p := evalPacket()
			p.Src = netip.MustParseAddr("198.51.100.9")
			return p
		}, fwstate.Drop, "filter/walk-return#2"},
		{"drop by the policy", evalPacket, fwstate.Drop, "filter/INPUT policy DROP"},
		{"reject by a rule", func() fwstate.Packet {
			p := evalPacket()
			p.Proto = "udp"
			return p
		}, fwstate.Reject, "filter/INPUT#4"},
		{"unknown: a rate-limited ACCEPT", func() fwstate.Packet {
			p := evalPacket()
			p.Proto = "gre"
			return p
		}, fwstate.Unknown, "filter/INPUT#7"},
		{"unknown: an opaque rule", func() fwstate.Packet {
			p := evalPacket()
			p.Proto = "ah"
			return p
		}, fwstate.Unknown, "filter/INPUT#8"},
		{"unknown: the rule whose jump nests too deep", func() fwstate.Packet {
			p := evalPacket()
			p.Proto = "icmp"
			return p
		}, fwstate.Unknown, "filter/walk-loop#1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dcdCheck(t, walkFilter, tt.pkt(), nil, tt.verdict, tt.decided)
		})
	}
}

func TestDcdDecidedForward(t *testing.T) {
	route := func(fwstate.Packet) (string, string, error) { return "forward", "docker0", nil }
	t.Run("a binding chain's rule accepts, and later hooks do not replace it", func(t *testing.T) {
		dcdCheck(t, dcdDocker, dcdForward(8080), route, fwstate.Accept, "filter/dcd-bindings#1")
	})
	t.Run("a binding chain's last rule drops", func(t *testing.T) {
		p := dcdForward(9999)
		p.Dst = netip.MustParseAddr("172.17.0.9")
		dcdCheck(t, dcdDocker, p, nil, fwstate.Drop, "filter/dcd-bindings#2")
	})
	t.Run("a source outside the binding falls to the FORWARD policy", func(t *testing.T) {
		p := dcdForward(8080)
		p.Src = netip.MustParseAddr("198.51.100.5")
		dcdCheck(t, dcdDocker, p, route, fwstate.Drop, "filter/FORWARD policy DROP")
	})
}

func TestDcdDecidedLastHook(t *testing.T) {
	t.Run("with no filter table the last hook's policy decides", func(t *testing.T) {
		text := "*raw\n:PREROUTING ACCEPT [0:0]\nCOMMIT\n*mangle\n:INPUT ACCEPT [0:0]\nCOMMIT\n"
		dcdCheck(t, text, evalPacket(), nil, fwstate.Accept, "mangle/INPUT policy ACCEPT")
	})
	t.Run("with a filter table that has no INPUT chain the earlier hook decides", func(t *testing.T) {
		text := "*raw\n:PREROUTING ACCEPT [0:0]\nCOMMIT\n*filter\n:OUTPUT ACCEPT [0:0]\nCOMMIT\n"
		dcdCheck(t, text, evalPacket(), nil, fwstate.Accept, "raw/PREROUTING policy ACCEPT")
	})
	t.Run("no hook on the path leaves it empty", func(t *testing.T) {
		text := "*raw\n:OUTPUT ACCEPT [0:0]\nCOMMIT\n"
		dcdCheck(t, text, evalPacket(), nil, fwstate.Accept, "")
	})
}

func TestDcdDecidedHookLevelUnknown(t *testing.T) {
	t.Run("an incompatible table", func(t *testing.T) {
		dcdCheck(t, "# Table `filter' is incompatible, use 'nft' tool.\n", evalPacket(), nil, fwstate.Unknown, "")
	})
	t.Run("a built-in chain without a policy", func(t *testing.T) {
		dcdCheck(t, "*filter\n:INPUT - [0:0]\nCOMMIT\n", evalPacket(), nil, fwstate.Unknown, "")
	})
	t.Run("a rewritten destination and no router", func(t *testing.T) {
		dcdCheck(t, dcdDocker, dcdForward(8080), nil, fwstate.Unknown, "")
	})
	t.Run("a router that fails", func(t *testing.T) {
		fail := func(fwstate.Packet) (string, string, error) { return "", "", errDcdRoute }
		dcdCheck(t, dcdDocker, dcdForward(8080), fail, fwstate.Unknown, "")
	})
	t.Run("a packet the walk refuses", func(t *testing.T) {
		p := evalPacket()
		p.Proto = ""
		dcdCheck(t, walkFilter, p, nil, fwstate.Unknown, "")
	})
	t.Run("no ruleset", func(t *testing.T) {
		got := Evaluate(nil, evalPacket(), nil)
		if got.Verdict != fwstate.Unknown || got.Decided != "" {
			t.Errorf("got verdict %q Decided %q, want unknown and empty", got.Verdict, got.Decided)
		}
	})
}

func TestDcdDecidedNatRule(t *testing.T) {
	t.Run("a DNAT target that cannot be followed names its rule", func(t *testing.T) {
		text := "*nat\n:PREROUTING ACCEPT [0:0]\n-A PREROUTING -p tcp -j DNAT --to-destination 172.17.0.2-172.17.0.4\nCOMMIT\n"
		dcdCheck(t, text, evalPacket(), nil, fwstate.Unknown, "nat/PREROUTING#1")
	})
	t.Run("a mark target with two modes names its rule", func(t *testing.T) {
		text := "*mangle\n:PREROUTING ACCEPT [0:0]\n-A PREROUTING -j CONNMARK --save-mark --restore-mark\nCOMMIT\n"
		dcdCheck(t, text, evalPacket(), nil, fwstate.Unknown, "mangle/PREROUTING#1")
	})
	t.Run("a target the walk does not model names its rule", func(t *testing.T) {
		text := "*filter\n:INPUT ACCEPT [0:0]\n-A INPUT -p tcp -j TPROXY --on-port 3128\nCOMMIT\n"
		dcdCheck(t, text, evalPacket(), nil, fwstate.Unknown, "filter/INPUT#1")
	})
}
