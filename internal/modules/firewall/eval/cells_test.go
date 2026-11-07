package eval

import (
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

func v1r20Src(prefix string, neg bool) netfilter.Rule {
	return netfilter.Rule{Source: netfilter.AddrMatch{Prefix: netip.MustParsePrefix(prefix), Neg: neg}}
}

func v1r20Dst(prefix string) netfilter.Rule {
	return netfilter.Rule{Dest: netfilter.AddrMatch{Prefix: netip.MustParsePrefix(prefix)}}
}

// v1r20Orig is a rule with a conntrack --ctorigdst match of value, a
// prefix or one address.
func v1r20Orig(value string) netfilter.Rule {
	return netfilter.Rule{Matches: []netfilter.Match{{Module: "conntrack",
		Options: []netfilter.Option{{Name: "ctorigdst", Values: []string{value}}}}}}
}

// v1r20Set puts the first half of rules in filter INPUT and the rest in nat
// PREROUTING, so a ruleset's rules are found in every table.
func v1r20Set(rules ...netfilter.Rule) *netfilter.Ruleset {
	k := (len(rules) + 1) / 2
	return &netfilter.Ruleset{Tables: []netfilter.Table{
		{Name: "filter", Chains: []netfilter.Chain{{Name: "INPUT", Builtin: true, Policy: "ACCEPT", Rules: rules[:k]}}},
		{Name: "nat", Chains: []netfilter.Chain{{Name: "PREROUTING", Builtin: true, Policy: "ACCEPT", Rules: rules[k:]}}},
	}}
}

// v1r20Cells builds the wanted cells from "<from>-<to>" texts; Rep is From.
func v1r20Cells(spans ...string) []Cell {
	out := make([]Cell, len(spans))
	for i, s := range spans {
		from, to, _ := strings.Cut(s, "-")
		out[i] = Cell{From: netip.MustParseAddr(from), To: netip.MustParseAddr(to), Rep: netip.MustParseAddr(from)}
	}
	return out
}

const (
	v1r20Whole4 = "1.0.0.0-223.255.255.255"
	v1r20Whole6 = "2000::-3fff:ffff:ffff:ffff:ffff:ffff:ffff:ffff"
)

func TestV1r20InternetCells(t *testing.T) {
	around8 := []string{"1.0.0.0-8.8.7.255", "8.8.8.0-8.8.8.255", "8.8.9.0-223.255.255.255"}
	tests := []struct {
		name string
		rs   *netfilter.Ruleset
		v6   bool
		want []string
	}{
		{"no ruleset", nil, false, []string{v1r20Whole4}},
		{"no rules, IPv6", &netfilter.Ruleset{}, true, []string{v1r20Whole6}},
		{"a destination prefix", v1r20Set(v1r20Dst("8.8.8.0/24")), false, around8},
		{"a source prefix with host bits set", v1r20Set(v1r20Src("8.8.8.9/24", false)), false, around8},
		{"a negated address", v1r20Set(v1r20Src("1.1.1.1/32", true)), false,
			[]string{"1.0.0.0-1.1.1.0", "1.1.1.1-1.1.1.1", "1.1.1.2-223.255.255.255"}},
		{"a conntrack original destination address", v1r20Set(v1r20Orig("9.9.9.9")), false,
			[]string{"1.0.0.0-9.9.9.8", "9.9.9.9-9.9.9.9", "9.9.9.10-223.255.255.255"}},
		{"a conntrack original destination prefix", v1r20Set(v1r20Orig("9.9.9.0/24")), false,
			[]string{"1.0.0.0-9.9.8.255", "9.9.9.0-9.9.9.255", "9.9.10.0-223.255.255.255"}},
		{"two prefixes in two tables", v1r20Set(v1r20Dst("198.41.192.0/24"), v1r20Dst("104.16.0.0/13")), false,
			[]string{"1.0.0.0-104.15.255.255", "104.16.0.0-104.23.255.255", "104.24.0.0-198.41.191.255",
				"198.41.192.0-198.41.192.255", "198.41.193.0-223.255.255.255"}},
		{"a prefix inside a private range", v1r20Set(v1r20Dst("10.1.0.0/16")), false,
			[]string{"1.0.0.0-9.255.255.255", "11.0.0.0-223.255.255.255"}},
		{"a prefix of the whole space", v1r20Set(v1r20Dst("0.0.0.0/0")), false, []string{v1r20Whole4}},
		{"a prefix at the top of the space", v1r20Set(v1r20Dst("255.255.255.254/31")), false, []string{v1r20Whole4}},
		{"a bogon prefix", v1r20Set(v1r20Src("240.0.0.0/4", false), v1r20Src("0.0.0.0/8", false)), false, []string{v1r20Whole4}},
		{"an IPv6 prefix in an IPv4 call", v1r20Set(v1r20Dst("2001:db8:1::/48")), false, []string{v1r20Whole4}},
		{"an IPv4 prefix in an IPv6 call", v1r20Set(v1r20Dst("8.8.8.0/24")), true, []string{v1r20Whole6}},
		{"an IPv6 prefix", v1r20Set(v1r20Dst("2606:4700::/32")), true,
			[]string{"2000::-2606:46ff:ffff:ffff:ffff:ffff:ffff:ffff", "2606:4700::-2606:4700:ffff:ffff:ffff:ffff:ffff:ffff",
				"2606:4701::-3fff:ffff:ffff:ffff:ffff:ffff:ffff:ffff"}},
		{"an IPv6 prefix inside the documentation range", v1r20Set(v1r20Dst("2001:db8:1::/48")), true,
			[]string{"2000::-2001:db7:ffff:ffff:ffff:ffff:ffff:ffff", "2001:db9::-3fff:ffff:ffff:ffff:ffff:ffff:ffff:ffff"}},
		{"an IPv6 prefix of the whole space", v1r20Set(v1r20Dst("::/0")), true, []string{v1r20Whole6}},
		{"an IPv6 prefix at the top of the space", v1r20Set(v1r20Dst("3fff::/16")), true,
			[]string{"2000::-3ffe:ffff:ffff:ffff:ffff:ffff:ffff:ffff", "3fff::-3fff:ffff:ffff:ffff:ffff:ffff:ffff:ffff"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := InternetCells(tt.rs, tt.v6)
			if want := v1r20Cells(tt.want...); !slices.Equal(got, want) {
				t.Errorf("InternetCells =\n%v\nwant\n%v", got, want)
			}
		})
	}
}

// v1r20Public is the Contracts definition of a public unicast address.
func v1r20Public(a netip.Addr) bool {
	for _, p := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24",
		"203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32"} {
		if netip.MustParsePrefix(p).Contains(a) {
			return false
		}
	}
	return a.IsGlobalUnicast() && !a.IsPrivate()
}

// v1r20Last is the last address of p.
func v1r20Last(p netip.Prefix) netip.Addr {
	b := p.Masked().Addr().AsSlice()
	for i := p.Bits(); i < len(b)*8; i++ {
		b[i/8] |= 0x80 >> (i % 8)
	}
	a, _ := netip.AddrFromSlice(b)
	return a
}

func TestV1r20CellsPartitionThePublicSpace(t *testing.T) {
	prefixes := map[bool][]string{
		false: {"8.8.8.0/24", "1.1.1.1/32", "104.16.0.0/13", "10.1.0.0/16", "198.41.192.0/24", "172.16.0.0/12", "224.0.0.0/4", "0.0.0.0/0"},
		true:  {"2606:4700::/32", "2001:db8:1::/48", "2a00::/12", "::/0"},
	}
	samples := map[bool][]string{
		false: {"1.0.0.0", "1.1.1.1", "8.8.8.8", "9.255.255.255", "11.0.0.0", "100.128.0.0", "126.255.255.255", "128.0.0.1",
			"169.253.255.255", "169.255.0.0", "172.15.255.255", "172.32.0.0", "192.0.1.255", "192.0.3.0", "198.17.255.255",
			"198.20.0.0", "198.51.99.255", "198.51.101.0", "203.0.112.255", "203.0.114.0", "223.255.255.255"},
		true: {"2000::", "2001:db7::1", "2001:db9::1", "2606:4700::1", "2a00::1", "3fff:ffff:ffff:ffff:ffff:ffff:ffff:ffff"},
	}
	for _, v6 := range []bool{false, true} {
		var rules []netfilter.Rule
		var pfx []netip.Prefix
		for _, p := range prefixes[v6] {
			rules = append(rules, v1r20Dst(p))
			pfx = append(pfx, netip.MustParsePrefix(p))
		}
		cells := InternetCells(v1r20Set(rules...), v6)
		if len(cells) == 0 {
			t.Fatalf("v6=%v: no cells", v6)
		}
		for i, c := range cells {
			if c.Rep != c.From || !v1r20Public(c.From) || !v1r20Public(c.To) || c.From.Compare(c.To) > 0 {
				t.Errorf("v6=%v: cell %v is not a range of public addresses with Rep at its lowest", v6, c)
			}
			if i > 0 && cells[i-1].To.Compare(c.From) >= 0 {
				t.Errorf("v6=%v: cells %v and %v are out of order or overlap", v6, cells[i-1], c)
			}
			for _, p := range pfx {
				p = p.Masked()
				if p.Contains(c.From) != p.Contains(c.To) ||
					(c.From.Compare(p.Addr()) < 0 && p.Addr().Compare(c.To) <= 0) ||
					(c.From.Compare(v1r20Last(p)) <= 0 && v1r20Last(p).Compare(c.To) < 0) {
					t.Errorf("v6=%v: cell %v is cut by the prefix %s", v6, c, p)
				}
			}
		}
		for _, s := range samples[v6] {
			a := netip.MustParseAddr(s)
			if !v1r20Public(a) {
				t.Fatalf("sample %s is not a public address", s)
			}
			n := 0
			for _, c := range cells {
				if c.From.Compare(a) <= 0 && a.Compare(c.To) <= 0 {
					n++
				}
			}
			if n != 1 {
				t.Errorf("v6=%v: public address %s is in %d cells, want 1", v6, s, n)
			}
		}
	}
}
