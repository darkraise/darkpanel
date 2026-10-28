package dockerfw

import (
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

const (
	hlChainName   = "dp-b-0123456789ab"
	hlForeignDNAT = "-A PREROUTING -d 198.51.100.7/32 -p tcp -m tcp --dport 2222 -j DNAT --to-destination 192.168.1.5:22\n"
	hlForeignRule = "-A DOCKER-USER -s 198.51.100.0/24 -j DROP"
)

func hlNetworks() []Network {
	return []Network{
		{ID: "aaaaaaaaaaaa1111", Name: "bridge", Driver: "bridge", Bridge: "docker0", Subnets: []netip.Prefix{netip.MustParsePrefix("172.17.0.0/16")}},
		{ID: "bbbbbbbbbbbb2222", Name: "app", Driver: "bridge", Bridge: "br-bbbbbbbbbbbb", Subnets: []netip.Prefix{netip.MustParsePrefix("172.18.0.0/16")}},
		{ID: "cccccccccccc3333", Name: "custom", Driver: "bridge", Bridge: "dpcustom", Subnets: []netip.Prefix{netip.MustParsePrefix("172.30.0.0/24")}},
	}
}

func hlV6Network() Network {
	return Network{ID: "dddddddddddd4444", Name: "v6net", Driver: "bridge", Bridge: "br-dddddddddddd", IPv6: true, Subnets: []netip.Prefix{netip.MustParsePrefix("fd00:dead::/64")}}
}

func hlBlockInput(s Settings) BlockInput {
	return BlockInput{
		CustomBridges: []string{"dpcustom"},
		Settings:      s,
		Lines: []BindingLine{
			{Proto: "tcp", ContainerAddr: netip.MustParseAddr("172.17.0.2"), ContainerPort: 80, HostPort: 8080, Chain: hlChainName},
			{Proto: "tcp", ContainerAddr: netip.MustParseAddr("fd00:dead::2"), ContainerPort: 80, HostPort: 8080, Chain: hlChainName},
		},
		Chains: []BindingChain{{Name: hlChainName, Deny: []netip.Prefix{netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("2001:db8::/32")}}},
	}
}

// hlLoaded parses what the kernel holds for one family: Docker's FORWARD
// jumps, the block's chains and a nat table with Docker's PREROUTING jump
// followed by nat.
func hlLoaded(t *testing.T, block []byte, nat string) *netfilter.Ruleset {
	t.Helper()
	text := "*filter\n:INPUT DROP [0:0]\n:FORWARD DROP [0:0]\n:DOCKER-FORWARD - [0:0]\n" +
		"-A FORWARD -j DOCKER-USER\n-A FORWARD -j DOCKER-FORWARD\nCOMMIT\n" + string(block) +
		"*nat\n:PREROUTING ACCEPT [0:0]\n:DOCKER - [0:0]\n-A PREROUTING -m addrtype --dst-type LOCAL -j DOCKER\n" + nat + "COMMIT\n"
	rs, err := netfilter.ParseRestore(strings.NewReader(text))
	if err != nil {
		t.Fatalf("parse loaded rules: %v", err)
	}
	return rs
}

// hlDockerDefault is Docker's own DOCKER-USER before the integration is on.
var hlDockerDefault = []byte("*filter\n:DOCKER-USER - [0:0]\n-A DOCKER-USER -j RETURN\nCOMMIT\n")

// hlInput is a healthy host with the integration on: the block in both
// after files, loaded as rendered, UFW's incoming policy DROP, and nat4 as
// the IPv4 nat rules after Docker's.
func hlInput(t *testing.T, s Settings, nat4 string) HealthInput {
	t.Helper()
	in := HealthInput{
		Want:         true,
		Files:        map[fwstate.Family][]byte{},
		Expected:     map[fwstate.Family][]byte{},
		Loaded:       map[fwstate.Family]*netfilter.Ruleset{},
		IPv6:         true,
		DefaultInput: "DROP",
		Facts:        Facts{Networks: hlNetworks()},
		Settings:     s,
		Interfaces:   []string{"lo", "eth0", "docker0", "br-bbbbbbbbbbbb", "dpcustom"},
	}
	for _, f := range []fwstate.Family{fwstate.V4, fwstate.V6} {
		block := Render(hlBlockInput(s), f)
		file, err := WithBlock([]byte("# after.rules\n"), block)
		if err != nil {
			t.Fatalf("write the block: %v", err)
		}
		nat := ""
		if f == fwstate.V4 {
			nat = nat4
		}
		in.Files[f], in.Expected[f], in.Loaded[f] = file, block, hlLoaded(t, block, nat)
	}
	return in
}

func hlChain(t *testing.T, in HealthInput, f fwstate.Family, name string) *netfilter.Chain {
	t.Helper()
	c := in.Loaded[f].Table("filter").Chain(name)
	if c == nil {
		t.Fatalf("loaded %s rules have no chain %s", f, name)
	}
	return c
}

func hlRule(t *testing.T, line string) netfilter.Rule {
	t.Helper()
	rs, err := netfilter.ParseRestore(strings.NewReader("*filter\n" + line + "\nCOMMIT\n"))
	if err != nil {
		t.Fatalf("parse %q: %v", line, err)
	}
	return rs.Table("filter").Chains[0].Rules[0]
}

func hlExemption(t *testing.T) Exemption {
	t.Helper()
	rs, err := netfilter.ParseRestore(strings.NewReader("*nat\n" + hlForeignDNAT + "COMMIT\n"))
	if err != nil {
		t.Fatal(err)
	}
	ex, ok := Propose(rs.Table("nat").Chain("PREROUTING").Rules[0], fwstate.V4)
	if !ok {
		t.Fatal("Propose refused the foreign DNAT rule")
	}
	return ex
}

type hlWant struct {
	level  Level
	reason string
}

func TestHlHealth(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, in *HealthInput)
		want   []hlWant
	}{
		{name: "a healthy host", mutate: func(*testing.T, *HealthInput) {}},
		{name: "REJECT is a valid incoming policy", mutate: func(_ *testing.T, in *HealthInput) { in.DefaultInput = "REJECT" }},
		{
			name: "block missing from after.rules",
			mutate: func(_ *testing.T, in *HealthInput) {
				in.Files[fwstate.V4] = []byte("# after.rules\n")
			},
			want: []hlWant{{Broken, "after.rules holds no Darkpanel Docker block"}},
		},
		{
			name: "block missing from after6.rules",
			mutate: func(_ *testing.T, in *HealthInput) {
				delete(in.Files, fwstate.V6)
			},
			want: []hlWant{{Broken, "after6.rules holds no Darkpanel Docker block"}},
		},
		{
			name: "duplicated block markers",
			mutate: func(_ *testing.T, in *HealthInput) {
				in.Files[fwstate.V4] = slices.Concat(in.Files[fwstate.V4], in.Files[fwstate.V4])
			},
			want: []hlWant{{Broken, "after.rules: "}},
		},
		{
			name: "IPV6=no leaves after6.rules and the IPv6 chains alone",
			mutate: func(_ *testing.T, in *HealthInput) {
				in.IPv6 = false
				delete(in.Files, fwstate.V6)
				delete(in.Loaded, fwstate.V6)
			},
		},
		{
			name: "a loaded rule is missing",
			mutate: func(t *testing.T, in *HealthInput) {
				c := hlChain(t, *in, fwstate.V4, "DOCKER-USER")
				c.Rules = slices.Delete(c.Rules, 0, 1)
			},
			want: []hlWant{{Broken, "loaded DOCKER-USER (IPv4) does not match the Docker block"}},
		},
		{
			name: "a loaded rule is duplicated",
			mutate: func(t *testing.T, in *HealthInput) {
				c := hlChain(t, *in, fwstate.V6, ChainDeny)
				c.Rules = slices.Insert(c.Rules, 0, c.Rules[0])
			},
			want: []hlWant{{Broken, "loaded " + ChainDeny + " (IPv6) does not match the Docker block"}},
		},
		{
			name: "loaded rules are out of order",
			mutate: func(t *testing.T, in *HealthInput) {
				c := hlChain(t, *in, fwstate.V4, hlChainName)
				c.Rules[0], c.Rules[1] = c.Rules[1], c.Rules[0]
			},
			want: []hlWant{{Broken, "loaded " + hlChainName + " (IPv4) does not match the Docker block"}},
		},
		{
			name: "a binding chain is not loaded",
			mutate: func(_ *testing.T, in *HealthInput) {
				tbl := in.Loaded[fwstate.V4].Table("filter")
				tbl.Chains = slices.DeleteFunc(tbl.Chains, func(c netfilter.Chain) bool { return c.Name == hlChainName })
			},
			want: []hlWant{{Broken, "loaded " + hlChainName + " (IPv4) does not match the Docker block: chain is not loaded"}},
		},
		{
			name: "a stale binding chain is loaded",
			mutate: func(t *testing.T, in *HealthInput) {
				tbl := in.Loaded[fwstate.V4].Table("filter")
				tbl.Chains = append(tbl.Chains, netfilter.Chain{Name: "dp-b-ffffffffffff", Rules: []netfilter.Rule{hlRule(t, "-A dp-b-ffffffffffff -j RETURN")}})
			},
			want: []hlWant{{Broken, "loaded dp-b-ffffffffffff (IPv4) does not match the Docker block: chain is not in the block"}},
		},
		{
			name: "a foreign DOCKER-USER rule is reported once and not as a mismatch",
			mutate: func(t *testing.T, in *HealthInput) {
				c := hlChain(t, *in, fwstate.V4, "DOCKER-USER")
				c.Rules = slices.Insert(c.Rules, 0, hlRule(t, hlForeignRule))
			},
			want: []hlWant{{Broken, "DOCKER-USER (IPv4) holds a foreign rule: " + hlForeignRule}},
		},
		{
			name: "a rule of a stale block on disk is Darkpanel's, not foreign",
			mutate: func(t *testing.T, in *HealthInput) {
				stale := DefaultSettings()
				stale.PrivateSources = append(stale.PrivateSources, netip.MustParsePrefix("192.0.2.0/24"))
				block := Render(hlBlockInput(stale), fwstate.V4)
				file, err := WithBlock([]byte("# after.rules\n"), block)
				if err != nil {
					t.Fatal(err)
				}
				in.Files[fwstate.V4], in.Loaded[fwstate.V4] = file, hlLoaded(t, block, "")
			},
			want: []hlWant{{Broken, "loaded DOCKER-USER (IPv4) does not match the Docker block"}},
		},
		{
			name: "FORWARD does not jump to DOCKER-USER",
			mutate: func(t *testing.T, in *HealthInput) {
				c := hlChain(t, *in, fwstate.V4, "FORWARD")
				c.Rules = slices.Delete(c.Rules, 0, 1)
			},
			want: []hlWant{{Broken, "FORWARD (IPv4) does not jump to DOCKER-USER"}},
		},
		{
			name: "Docker's rule comes before the jump to DOCKER-USER",
			mutate: func(t *testing.T, in *HealthInput) {
				c := hlChain(t, *in, fwstate.V4, "FORWARD")
				c.Rules[0], c.Rules[1] = c.Rules[1], c.Rules[0]
			},
			want: []hlWant{{Broken, "FORWARD (IPv4) reaches Docker's rule -A FORWARD -j DOCKER-FORWARD before DOCKER-USER"}},
		},
		{
			name: "no IPv6 network, so ip6tables FORWARD need not jump",
			mutate: func(t *testing.T, in *HealthInput) {
				c := hlChain(t, *in, fwstate.V6, "FORWARD")
				c.Rules = slices.Delete(c.Rules, 0, 1)
			},
		},
		{
			name: "an IPv6 network needs the ip6tables FORWARD jump",
			mutate: func(t *testing.T, in *HealthInput) {
				in.Facts.Networks = append(in.Facts.Networks, hlV6Network())
				c := hlChain(t, *in, fwstate.V6, "FORWARD")
				c.Rules = slices.Delete(c.Rules, 0, 1)
			},
			want: []hlWant{{Broken, "FORWARD (IPv6) does not jump to DOCKER-USER"}},
		},
		{
			name: "an incoming policy of ACCEPT in the configuration",
			mutate: func(_ *testing.T, in *HealthInput) {
				in.DefaultInput = "ACCEPT"
			},
			want: []hlWant{{Broken, "DEFAULT_INPUT_POLICY is ACCEPT, not DROP or REJECT"}},
		},
		{
			name: "an incoming policy of ACCEPT loaded",
			mutate: func(_ *testing.T, in *HealthInput) {
				in.Loaded[fwstate.V4].Table("filter").Chain("INPUT").Policy = "ACCEPT"
			},
			want: []hlWant{{Broken, "loaded INPUT policy (IPv4) is ACCEPT, not DROP"}},
		},
		{
			name: "a non-Docker br-* interface",
			mutate: func(_ *testing.T, in *HealthInput) {
				in.Interfaces = append(in.Interfaces, "br-lan")
			},
			want: []hlWant{{Degraded, "interface br-lan is a br-* bridge that is not a Docker network's bridge"}},
		},
		{
			name: "a network in routed gateway mode",
			mutate: func(_ *testing.T, in *HealthInput) {
				in.Facts.Networks[1].GatewayModeV4 = "routed"
			},
			want: []hlWant{{Degraded, "network app is unprotected: gateway mode routed bypasses the DNAT gate"}},
		},
		{
			name: "a custom bridge name with a public subnet",
			mutate: func(_ *testing.T, in *HealthInput) {
				in.Facts.Networks[2].Subnets = []netip.Prefix{netip.MustParsePrefix("203.0.113.0/24")}
			},
			want: []hlWant{{Degraded, "network custom is unprotected: custom bridge name dpcustom with non-private subnet 203.0.113.0/24"}},
		},
		{
			name: "IPV6=no with an IPv6 Docker network",
			mutate: func(_ *testing.T, in *HealthInput) {
				in.IPv6 = false
				delete(in.Files, fwstate.V6)
				delete(in.Loaded, fwstate.V6)
				in.Facts.Networks = append(in.Facts.Networks, hlV6Network())
			},
			want: []hlWant{{Degraded, "IPV6=no in /etc/default/ufw while Docker network v6net has IPv6 enabled"}},
		},
		{
			name: "issues come in the order of the specification",
			mutate: func(t *testing.T, in *HealthInput) {
				in.Files[fwstate.V4] = []byte("# after.rules\n")
				c := hlChain(t, *in, fwstate.V4, "DOCKER-USER")
				c.Rules = slices.Insert(c.Rules, 0, hlRule(t, hlForeignRule))
				in.DefaultInput = "ACCEPT"
				in.Interfaces = append(in.Interfaces, "br-lan")
			},
			want: []hlWant{
				{Broken, "after.rules holds no Darkpanel Docker block"},
				{Broken, "DOCKER-USER (IPv4) holds a foreign rule"},
				{Broken, "DEFAULT_INPUT_POLICY is ACCEPT"},
				{Degraded, "interface br-lan"},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := hlInput(t, DefaultSettings(), "")
			tc.mutate(t, &in)
			hlCheck(t, Health(in), tc.want)
		})
	}
}

func hlCheck(t *testing.T, got []Issue, want []hlWant) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("issues = %+v, want %d", got, len(want))
	}
	for i, w := range want {
		if got[i].Level != w.level || !strings.Contains(got[i].Reason, w.reason) {
			t.Errorf("issue %d = %+v, want level %s containing %q", i, got[i], w.level, w.reason)
		}
	}
}

func TestHlHealthForeignForwards(t *testing.T) {
	covered := DefaultSettings()
	covered.ForwardExemptions = []Exemption{hlExemption(t)}
	inDocker := DefaultSettings()
	inDocker.ForwardExemptions = []Exemption{{
		OrigDestination: netip.MustParseAddr("198.51.100.7"),
		Destination:     netip.MustParseAddr("172.17.0.9"),
		Proto:           "tcp",
	}}
	tests := []struct {
		name string
		s    Settings
		nat  string
		want []hlWant
	}{
		{name: "no foreign DNAT rule", s: DefaultSettings()},
		{
			name: "a foreign DNAT rule without an exemption",
			s:    DefaultSettings(),
			nat:  hlForeignDNAT,
			want: []hlWant{{Degraded, "foreign DNAT forward has no exemption (IPv4): " + strings.TrimSuffix(hlForeignDNAT, "\n")}},
		},
		{name: "a foreign DNAT rule its exemption covers", s: covered, nat: hlForeignDNAT},
		{
			name: "an exemption whose destination lies in a Docker subnet",
			s:    inDocker,
			want: []hlWant{{Broken, "exemption 0: destination 172.17.0.9 lies in Docker network bridge (172.17.0.0/16)"}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			hlCheck(t, Health(hlInput(t, tc.s, tc.nat)), tc.want)
		})
	}
}

func TestHlHealthWithoutTheBlock(t *testing.T) {
	// flag on while UFW is disabled: Want is false and Docker's own
	// DOCKER-USER is loaded.
	base := func(t *testing.T) HealthInput {
		in := hlInput(t, DefaultSettings(), hlForeignDNAT)
		in.Want = false
		in.Files, in.Expected = nil, nil
		for _, f := range []fwstate.Family{fwstate.V4, fwstate.V6} {
			in.Loaded[f] = hlLoaded(t, hlDockerDefault, hlForeignDNAT)
			in.Loaded[f].Table("filter").Chain("INPUT").Policy = "ACCEPT"
		}
		return in
	}
	tests := []struct {
		name   string
		mutate func(t *testing.T, in *HealthInput)
		want   []hlWant
	}{
		{name: "block, chains, FORWARD, DNAT and loaded policy are not checked", mutate: func(*testing.T, *HealthInput) {}},
		{
			name: "a foreign DOCKER-USER rule still counts",
			mutate: func(t *testing.T, in *HealthInput) {
				c := hlChain(t, *in, fwstate.V4, "DOCKER-USER")
				c.Rules = slices.Insert(c.Rules, 0, hlRule(t, hlForeignRule))
			},
			want: []hlWant{{Broken, "DOCKER-USER (IPv4) holds a foreign rule: " + hlForeignRule}},
		},
		{
			name:   "the configured incoming policy still counts",
			mutate: func(_ *testing.T, in *HealthInput) { in.DefaultInput = "ACCEPT" },
			want:   []hlWant{{Broken, "DEFAULT_INPUT_POLICY is ACCEPT, not DROP or REJECT"}},
		},
		{
			name:   "a non-Docker br-* interface still counts",
			mutate: func(_ *testing.T, in *HealthInput) { in.Interfaces = append(in.Interfaces, "br-lan") },
			want:   []hlWant{{Degraded, "interface br-lan"}},
		},
		{
			name:   "an unprotected network still counts",
			mutate: func(_ *testing.T, in *HealthInput) { in.Facts.Networks[1].GatewayModeV4 = "nat-unprotected" },
			want:   []hlWant{{Degraded, "network app is unprotected: gateway mode nat-unprotected bypasses the DNAT gate"}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := base(t)
			tc.mutate(t, &in)
			hlCheck(t, Health(in), tc.want)
		})
	}
}

func TestHlUnprotected(t *testing.T) {
	pfx := func(ps ...string) []netip.Prefix {
		var out []netip.Prefix
		for _, p := range ps {
			out = append(out, netip.MustParsePrefix(p))
		}
		return out
	}
	br := func(over func(*Network)) Network {
		n := Network{ID: "eeeeeeeeeeee5555", Name: "n", Driver: "bridge", Bridge: "br-eeeeeeeeeeee", Subnets: pfx("172.20.0.0/16")}
		over(&n)
		return n
	}
	only := Settings{PrivateSources: pfx("203.0.113.0/24")}
	tests := []struct {
		name   string
		n      Network
		s      Settings
		reason string
	}{
		{name: "the default bridge", n: br(func(n *Network) { n.Name, n.Bridge = "bridge", "docker0" }), s: DefaultSettings()},
		{name: "nat mode", n: br(func(n *Network) { n.GatewayModeV4 = "nat" }), s: DefaultSettings()},
		{name: "routed IPv4 mode", n: br(func(n *Network) { n.GatewayModeV4 = "routed" }), s: DefaultSettings(), reason: "gateway mode routed bypasses the DNAT gate"},
		{name: "nat-unprotected IPv4 mode", n: br(func(n *Network) { n.GatewayModeV4 = "nat-unprotected" }), s: DefaultSettings(), reason: "gateway mode nat-unprotected bypasses the DNAT gate"},
		{name: "routed IPv6 mode", n: br(func(n *Network) { n.GatewayModeV6 = "routed" }), s: DefaultSettings(), reason: "gateway mode routed bypasses the DNAT gate"},
		{name: "a network that is not a bridge", n: br(func(n *Network) { n.Driver, n.Bridge, n.GatewayModeV4 = "macvlan", "", "routed" }), s: DefaultSettings()},
		{name: "a custom bridge name with a private subnet", n: br(func(n *Network) { n.Bridge = "dpcustom" }), s: DefaultSettings()},
		{
			name:   "a custom bridge name with a public subnet",
			n:      br(func(n *Network) { n.Bridge, n.Subnets = "dpcustom", pfx("172.20.0.0/16", "203.0.113.0/24") }),
			s:      DefaultSettings(),
			reason: "custom bridge name dpcustom with non-private subnet 203.0.113.0/24",
		},
		{name: "a default bridge name with a public subnet", n: br(func(n *Network) { n.Subnets = pfx("203.0.113.0/24") }), s: DefaultSettings()},
		{name: "a custom bridge name whose subnet is a private source", n: br(func(n *Network) { n.Bridge, n.Subnets = "dpcustom", pfx("203.0.113.0/24") }), s: only},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reason, got := Unprotected(tc.n, tc.s)
			if got != (tc.reason != "") || reason != tc.reason {
				t.Errorf("Unprotected = %q, %v, want %q", reason, got, tc.reason)
			}
		})
	}
}
