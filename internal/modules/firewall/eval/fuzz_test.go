package eval

import (
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

// fz7Packets is a fixed set of packets, both families, every hook, and the
// protocols and connection states the walk tells apart.
func fz7Packets() []fwstate.Packet {
	var out []fwstate.Packet
	for _, fam := range []struct {
		family   fwstate.Family
		src, dst string
	}{
		{fwstate.V4, "203.0.113.7", "192.0.2.1"},
		{fwstate.V6, "2001:db8::7", "2001:db8::1"},
	} {
		base := fwstate.Packet{
			Family: fam.family, Src: netip.MustParseAddr(fam.src), Dst: netip.MustParseAddr(fam.dst),
			SrcPort: 40000, DstPort: 22, Proto: "tcp", CtState: "NEW",
		}
		input, forward, output := base, base, base
		input.Path, input.InIface = "input", "eth0"
		forward.Path, forward.InIface, forward.OutIface, forward.DstPort = "forward", "eth0", "eth1", 80
		output.Path, output.OutIface = "output", "eth0"
		udp, established, marked, icmp := input, input, forward, input
		udp.Proto, udp.DstPort = "udp", 53
		established.CtState = "ESTABLISHED"
		marked.Mark = 0x1
		icmp.Proto, icmp.SrcPort, icmp.DstPort = "icmp", 0, 0
		out = append(out, input, forward, output, udp, established, marked, icmp)
	}
	return out
}

// FuzzEvaluate feeds arbitrary save output to netfilter.Parse and, when it is
// accepted, to Evaluate for a fixed set of packets, without and with a router,
// and to Unmodelled: none may panic, every verdict is one of the four, the
// same ruleset gives the same results twice, and Unmodelled lists only rules
// of the mangle and security tables (R74).
func FuzzEvaluate(f *testing.F) {
	for _, pattern := range []string{
		filepath.Join("testdata", "*", "*.save"),
		filepath.Join("..", "..", "..", "platform", "netfilter", "testdata", "*.save"),
		filepath.Join("..", "..", "..", "platform", "netfilter", "testdata", "ufw", "*", "*.save"),
	} {
		names, err := filepath.Glob(pattern)
		if err != nil {
			f.Fatal(err)
		}
		if len(names) == 0 {
			f.Fatalf("no seed matches %s", pattern)
		}
		for _, name := range names {
			b, err := os.ReadFile(name) //nolint:gosec // G304: a fixture under a testdata/ directory
			if err != nil {
				f.Fatal(err)
			}
			f.Add(string(b))
		}
	}
	f.Add(walkFilter)
	route := func(fwstate.Packet) (string, string, error) { return "forward", "eth1", nil }
	f.Fuzz(func(t *testing.T, in string) {
		rs, err := netfilter.Parse(strings.NewReader(in))
		if err != nil {
			return
		}
		for _, p := range fz7Packets() {
			for _, r := range []fwstate.Router{nil, route} {
				got := Evaluate(rs, p, r)
				switch got.Verdict {
				case fwstate.Accept, fwstate.Drop, fwstate.Reject, fwstate.Unknown:
				default:
					t.Fatalf("verdict %q for %q", got.Verdict, in[:min(len(in), 64)])
				}
				if again := Evaluate(rs, p, r); !reflect.DeepEqual(got, again) {
					t.Fatalf("Evaluate gave %+v, then %+v for %q", got, again, in[:min(len(in), 64)])
				}
			}
		}
		gaps := Unmodelled(rs)
		for _, g := range gaps {
			if !strings.HasPrefix(g, "mangle/") && !strings.HasPrefix(g, "security/") {
				t.Fatalf("Unmodelled lists %q for %q", g, in[:min(len(in), 64)])
			}
		}
		if again := Unmodelled(rs); !reflect.DeepEqual(gaps, again) {
			t.Fatalf("Unmodelled gave %q, then %q for %q", gaps, again, in[:min(len(in), 64)])
		}
	})
}
