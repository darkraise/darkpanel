package eval

import (
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

// ufwGoldenImages are the OS images the UFW golden captures cover.
var ufwGoldenImages = []string{"debian-12", "debian-13", "ubuntu-22.04", "ubuntu-24.04"}

// ufwGoldenRuleset joins one capture's five save files for a family.
func ufwGoldenRuleset(t *testing.T, image, scenario string, fam fwstate.Family) *netfilter.Ruleset {
	t.Helper()
	var b strings.Builder
	for _, table := range netfilter.Tables() {
		path := filepath.Join("..", "testdata", "golden", image, scenario, "save", "nft-"+string(fam)+"-"+table+".txt")
		data, err := os.ReadFile(path) //nolint:gosec // G304: the path is built from constants of this test.
		if err != nil {
			t.Fatal(err)
		}
		b.Write(data)
	}
	return evalRuleset(t, b.String())
}

// ufwGoldenCase is one packet and the verdict the capture must give it.
type ufwGoldenCase struct {
	name    string
	edit    func(p *fwstate.Packet)
	verdict fwstate.Verdict
	reason  string
}

// ufwGoldenPacket is evalPacket in the family, with the documentation
// prefix 2001:db8::/32 standing in for IPv4's documentation ranges.
func ufwGoldenPacket(fam fwstate.Family) fwstate.Packet {
	p := evalPacket()
	if fam == fwstate.V6 {
		p.Family = fwstate.V6
		p.Src, p.Dst = netip.MustParseAddr("2001:db8:1::7"), netip.MustParseAddr("2001:db8::1")
	}
	return p
}

func ufwGoldenRun(t *testing.T, scenario string, fam fwstate.Family, cases []ufwGoldenCase) {
	t.Helper()
	for _, image := range ufwGoldenImages {
		rs := ufwGoldenRuleset(t, image, scenario, fam)
		for _, c := range cases {
			t.Run(image+"/"+string(fam)+"/"+c.name, func(t *testing.T) {
				p := ufwGoldenPacket(fam)
				c.edit(&p)
				evalCheck(t, Evaluate(rs, p, nil), c.verdict, nil, c.reason)
			})
		}
	}
}

func TestUFWGoldenRules(t *testing.T) {
	none := func(*fwstate.Packet) {}
	port := func(proto string, n uint16) func(p *fwstate.Packet) {
		return func(p *fwstate.Packet) { p.Proto, p.DstPort = proto, n }
	}
	common := func(fam fwstate.Family) []ufwGoldenCase {
		limitRule, pfx := "filter/ufw-user-input#3", "ufw-"
		if fam == fwstate.V6 {
			limitRule, pfx = "filter/ufw6-user-input#4", "ufw6-"
		}
		return []ufwGoldenCase{
			{"ssh from any source", none, fwstate.Accept, ""},
			{"ssh from another source", func(p *fwstate.Packet) {
				p.Src = map[fwstate.Family]netip.Addr{
					fwstate.V4: netip.MustParseAddr("198.51.100.200"),
					fwstate.V6: netip.MustParseAddr("2001:db8:ffff::1"),
				}[fam]
			}, fwstate.Accept, ""},
			// ufw limit jumps to ufw-user-limit, which rejects, from a rule
			// whose recent match depends on earlier connections.
			{"limited port", port("tcp", 2222), fwstate.Unknown, limitRule + ": match recent depends on rates or earlier packets"},
			{"rejected tcp port", port("tcp", 3000), fwstate.Reject, "filter/" + pfx + "user-input#"},
			{"rejected udp port", port("udp", 3000), fwstate.Reject, "filter/" + pfx + "user-input#"},
			{"default incoming policy", port("tcp", 8080), fwstate.Drop, "filter/INPUT policy DROP"},
			{"esp on eth0", port("esp", 0), fwstate.Accept, ""},
			{"esp on eth1", func(p *fwstate.Packet) { p.Proto, p.DstPort, p.InIface = "esp", 0, "eth1" }, fwstate.Drop, "filter/INPUT policy DROP"},
			{"loopback", func(p *fwstate.Packet) { p.InIface, p.DstPort = "lo", 8080 }, fwstate.Accept, ""},
			{"established", func(p *fwstate.Packet) { p.DstPort, p.CtState = 8080, "ESTABLISHED" }, fwstate.Accept, ""},
			{"outgoing tcp", func(p *fwstate.Packet) {
				p.Path, p.InIface, p.OutIface, p.Src, p.Dst = "output", "", "eth0", p.Dst, p.Src
				p.SrcPort, p.DstPort = 50000, 443
			}, fwstate.Accept, ""},
			{"outgoing dns on eth0", func(p *fwstate.Packet) {
				p.Path, p.InIface, p.OutIface, p.Src, p.Dst = "output", "", "eth0", p.Dst, p.Src
				p.Proto, p.SrcPort, p.DstPort = "udp", 50000, 53
			}, fwstate.Accept, ""},
			{"forwarded elsewhere", func(p *fwstate.Packet) {
				p.Path, p.OutIface = "forward", "eth1"
			}, fwstate.Drop, "filter/FORWARD policy DROP"},
		}
	}
	ufwGoldenRun(t, "rules", fwstate.V4, append(common(fwstate.V4),
		ufwGoldenCase{"allowed list from 10.0.0.0/8", func(p *fwstate.Packet) {
			p.Src, p.DstPort = netip.MustParseAddr("10.1.2.3"), 8080
		}, fwstate.Accept, ""},
		ufwGoldenCase{"allowed list from elsewhere", port("tcp", 443), fwstate.Drop, "filter/INPUT policy DROP"},
		ufwGoldenCase{"route rule", func(p *fwstate.Packet) {
			p.Path, p.OutIface, p.Dst, p.DstPort = "forward", "eth0", netip.MustParseAddr("172.17.0.2"), 80
		}, fwstate.Accept, ""},
	))
	ufwGoldenRun(t, "rules", fwstate.V6, append(common(fwstate.V6),
		ufwGoldenCase{"denied udp range", func(p *fwstate.Packet) {
			p.Src, p.Proto, p.DstPort = netip.MustParseAddr("2001:db8:5::5"), "udp", 5050
		}, fwstate.Drop, "filter/ufw6-user-input#2 drops"},
	))
}

func TestUFWGoldenIPv6No(t *testing.T) {
	ssh := func(*fwstate.Packet) {}
	ufwGoldenRun(t, "ipv6no", fwstate.V4, []ufwGoldenCase{{"ssh", ssh, fwstate.Accept, ""}})
	ufwGoldenRun(t, "ipv6no", fwstate.V6, []ufwGoldenCase{
		{"ssh", ssh, fwstate.Drop, "filter/INPUT policy DROP"},
		{"loopback", func(p *fwstate.Packet) { p.InIface = "lo" }, fwstate.Accept, ""},
		{"outgoing", func(p *fwstate.Packet) {
			p.Path, p.InIface, p.OutIface, p.Src, p.Dst = "output", "", "eth0", p.Dst, p.Src
		}, fwstate.Drop, "filter/OUTPUT policy DROP"},
	})
}

func TestUFWGoldenDisabled(t *testing.T) {
	for _, fam := range []fwstate.Family{fwstate.V4, fwstate.V6} {
		ufwGoldenRun(t, "disabled", fam, []ufwGoldenCase{
			{"ssh", func(*fwstate.Packet) {}, fwstate.Accept, ""},
			{"formerly rejected port", func(p *fwstate.Packet) { p.DstPort = 3000 }, fwstate.Accept, ""},
			{"forwarded", func(p *fwstate.Packet) { p.Path, p.OutIface = "forward", "eth1" }, fwstate.Accept, ""},
		})
	}
}
