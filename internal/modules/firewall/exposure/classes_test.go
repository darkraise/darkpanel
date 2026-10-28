package exposure

import (
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

// ptPrivate is the default privateSources list in spec order.
func ptPrivate() []netip.Prefix {
	var out []netip.Prefix
	for _, s := range []string{
		"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "100.64.0.0/10", "fc00::/7", "fe80::/10",
	} {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}

func ptClassNames(cs []Class) []string {
	var out []string
	for _, c := range cs {
		s := c.Name
		if c.Range.IsValid() {
			s += " " + c.Range.String()
		}
		out = append(out, s)
	}
	return out
}

func TestPtClasses(t *testing.T) {
	tests := []struct {
		name    string
		private []netip.Prefix
		family  fwstate.Family
		want    []string
	}{
		{
			"v4 keeps the IPv4 ranges in order", ptPrivate(), fwstate.V4,
			[]string{"internet", "private 10.0.0.0/8", "private 172.16.0.0/12", "private 192.168.0.0/16", "private 100.64.0.0/10", "tailnet", "localhost"},
		},
		{
			"v6 keeps the IPv6 ranges in order", ptPrivate(), fwstate.V6,
			[]string{"internet", "private fc00::/7", "private fe80::/10", "tailnet", "localhost"},
		},
		{"no private ranges", nil, fwstate.V4, []string{"internet", "tailnet", "localhost"}},
		{
			"an invalid prefix is skipped", []netip.Prefix{{}, netip.MustParsePrefix("10.0.0.0/8")}, fwstate.V4,
			[]string{"internet", "private 10.0.0.0/8", "tailnet", "localhost"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ptClassNames(Classes(tt.private, tt.family)); !slices.Equal(got, tt.want) {
				t.Errorf("Classes = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestPtIfaceFields pins the field names Iface offers its consumers.
func TestPtIfaceFields(t *testing.T) {
	i := Iface{Name: "tailscale0", Up: true, Loopback: false, Tailnet: true, Bridge: false, Addrs: []netip.Addr{netip.MustParseAddr("100.64.0.1")}}
	if !i.Tailnet || i.Name != "tailscale0" || len(i.Addrs) != 1 {
		t.Errorf("Iface = %+v", i)
	}
}

const ptCutsSave = `*raw
:PREROUTING ACCEPT [0:0]
-A PREROUTING -s 203.0.113.0/24 -j DROP
COMMIT
*filter
:INPUT DROP [0:0]
:ufw-user-input - [0:0]
-A INPUT -i br-+ -s 10.0.0.0/8 -j ACCEPT
-A INPUT ! -i eth0 ! -s 192.168.1.0/24 -p tcp -m tcp --sport 1000:2000 -j ACCEPT
-A INPUT -p udp -m multiport --sports 53,5000:5100,:20 -j ACCEPT
-A INPUT -p tcp -m tcp --dport 22 -j ACCEPT
-A INPUT -p udp -m udp --sport 53 -j ACCEPT
-A INPUT -j ufw-user-input
-A ufw-user-input -i eth0 -s 203.0.113.0/24 -j ACCEPT
-A ufw-user-input -s 198.51.100.7/32 -m hashlimit --hashlimit-upto 1/sec -j ACCEPT
COMMIT
`

func TestPtCuts(t *testing.T) {
	rs, err := netfilter.Parse(strings.NewReader(ptCutsSave))
	if err != nil {
		t.Fatal(err)
	}
	srcs, ifaces, sports := Cuts(rs)
	var gotSrcs []string
	for _, p := range srcs {
		gotSrcs = append(gotSrcs, p.String())
	}
	wantSrcs := []string{"10.0.0.0/8", "192.168.1.0/24", "198.51.100.7/32", "203.0.113.0/24"}
	if !slices.Equal(gotSrcs, wantSrcs) {
		t.Errorf("sources = %q, want %q", gotSrcs, wantSrcs)
	}
	if want := []string{"br-+", "eth0"}; !slices.Equal(ifaces, want) {
		t.Errorf("interfaces = %q, want %q", ifaces, want)
	}
	wantPorts := []PortRange{{0, 20}, {53, 53}, {1000, 2000}, {5000, 5100}}
	if !slices.Equal(sports, wantPorts) {
		t.Errorf("source ports = %v, want %v", sports, wantPorts)
	}
}

func TestPtCutsEmpty(t *testing.T) {
	srcs, ifaces, sports := Cuts(nil)
	if srcs != nil || ifaces != nil || sports != nil {
		t.Errorf("Cuts(nil) = %v %v %v, want all nil", srcs, ifaces, sports)
	}
	rs, err := netfilter.Parse(strings.NewReader("*filter\n:INPUT ACCEPT [0:0]\nCOMMIT\n"))
	if err != nil {
		t.Fatal(err)
	}
	srcs, ifaces, sports = Cuts(rs)
	if len(srcs) != 0 || len(ifaces) != 0 || len(sports) != 0 {
		t.Errorf("Cuts(no rules) = %v %v %v, want all empty", srcs, ifaces, sports)
	}
}

func TestPtCutsV6(t *testing.T) {
	rs, err := netfilter.Parse(strings.NewReader("*filter\n:INPUT DROP [0:0]\n-A INPUT -s fd00::/8 -i eth0 -j ACCEPT\n-A INPUT -s fe80::/10 -j ACCEPT\nCOMMIT\n"))
	if err != nil {
		t.Fatal(err)
	}
	srcs, _, _ := Cuts(rs)
	want := []netip.Prefix{netip.MustParsePrefix("fd00::/8"), netip.MustParsePrefix("fe80::/10")}
	if !slices.Equal(srcs, want) {
		t.Errorf("sources = %v, want %v", srcs, want)
	}
}
