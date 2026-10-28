package exposure

import (
	"net/netip"
	"slices"
	"testing"
)

func paPrefixes(ss ...string) []netip.Prefix {
	var out []netip.Prefix
	for _, s := range ss {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}

func paRange(from, to string) Range {
	return Range{From: netip.MustParseAddr(from), To: netip.MustParseAddr(to)}
}

func TestPaAtoms(t *testing.T) {
	tests := []struct {
		name                   string
		include, exclude, cuts []string
		want                   []Range
	}{
		{
			name:    "no cuts is one atom",
			include: []string{"0.0.0.0/0"},
			want:    []Range{paRange("0.0.0.0", "255.255.255.255")},
		},
		{
			name:    "an excluded range leaves a gap, and cuts split what remains, in order",
			include: []string{"0.0.0.0/0"},
			exclude: []string{"10.0.0.0/8"},
			cuts:    []string{"203.0.113.0/24", "172.16.0.0/12"},
			want: []Range{
				paRange("0.0.0.0", "9.255.255.255"),
				paRange("11.0.0.0", "172.15.255.255"),
				paRange("172.16.0.0", "172.31.255.255"),
				paRange("172.32.0.0", "203.0.112.255"),
				paRange("203.0.113.0", "203.0.113.255"),
				paRange("203.0.114.0", "255.255.255.255"),
			},
		},
		{
			name:    "nested and overlapping cuts",
			include: []string{"10.0.0.0/8"},
			cuts:    []string{"10.1.2.0/24", "10.1.0.0/16", "10.1.0.0/16"},
			want: []Range{
				paRange("10.0.0.0", "10.0.255.255"),
				paRange("10.1.0.0", "10.1.1.255"),
				paRange("10.1.2.0", "10.1.2.255"),
				paRange("10.1.3.0", "10.1.255.255"),
				paRange("10.2.0.0", "10.255.255.255"),
			},
		},
		{
			name:    "adjacent include prefixes make one atom",
			include: []string{"128.0.0.0/1", "0.0.0.0/1"},
			want:    []Range{paRange("0.0.0.0", "255.255.255.255")},
		},
		{
			name:    "a cut outside the covered set, inside an exclusion or covering everything changes nothing",
			include: []string{"10.0.0.0/8"},
			exclude: []string{"10.5.0.0/16"},
			cuts:    []string{"192.0.2.0/24", "10.5.1.0/24", "0.0.0.0/0", "10.0.0.0/8"},
			want: []Range{
				paRange("10.0.0.0", "10.4.255.255"),
				paRange("10.6.0.0", "10.255.255.255"),
			},
		},
		{
			name:    "the top of the address space",
			include: []string{"255.255.255.0/24"},
			cuts:    []string{"255.255.255.255/32"},
			want: []Range{
				paRange("255.255.255.0", "255.255.255.254"),
				paRange("255.255.255.255", "255.255.255.255"),
			},
		},
		{
			name:    "the bottom of the address space",
			include: []string{"0.0.0.0/8"},
			cuts:    []string{"0.0.0.0/32"},
			want: []Range{
				paRange("0.0.0.0", "0.0.0.0"),
				paRange("0.0.0.1", "0.255.255.255"),
			},
		},
		{
			name:    "IPv6",
			include: []string{"fc00::/7"},
			cuts:    []string{"fd00::/8"},
			want: []Range{
				paRange("fc00::", "fcff:ffff:ffff:ffff:ffff:ffff:ffff:ffff"),
				paRange("fd00::", "fdff:ffff:ffff:ffff:ffff:ffff:ffff:ffff"),
			},
		},
		{
			name:    "IPv6 across the whole space with the top address cut",
			include: []string{"::/0"},
			exclude: []string{"::1/128"},
			cuts:    []string{"ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff/128"},
			want: []Range{
				paRange("::", "::"),
				paRange("::2", "ffff:ffff:ffff:ffff:ffff:ffff:ffff:fffe"),
				paRange("ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff", "ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff"),
			},
		},
		{
			name:    "a cut of the other family is ignored",
			include: []string{"10.0.0.0/8"},
			cuts:    []string{"fd00::/8"},
			want:    []Range{paRange("10.0.0.0", "10.255.255.255")},
		},
		{
			name:    "an exclusion that covers everything leaves nothing",
			include: []string{"10.0.0.0/8"},
			exclude: []string{"10.0.0.0/7"},
			want:    nil,
		},
		{name: "no include leaves nothing", cuts: []string{"10.0.0.0/8"}, want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Atoms(paPrefixes(tt.include...), paPrefixes(tt.exclude...), paPrefixes(tt.cuts...))
			if !slices.Equal(got, tt.want) {
				t.Errorf("Atoms = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPaRangePrefixes(t *testing.T) {
	tests := []struct {
		name string
		r    Range
		want []string
	}{
		{"one aligned block", paRange("10.0.0.0", "10.255.255.255"), []string{"10.0.0.0/8"}},
		{"everything", paRange("0.0.0.0", "255.255.255.255"), []string{"0.0.0.0/0"}},
		{"one address", paRange("1.2.3.4", "1.2.3.4"), []string{"1.2.3.4/32"}},
		{"unaligned ends", paRange("10.0.0.1", "10.0.0.6"), []string{"10.0.0.1/32", "10.0.0.2/31", "10.0.0.4/31", "10.0.0.6/32"}},
		{"a range between two blocks", paRange("11.0.0.0", "172.15.255.255"), []string{
			"11.0.0.0/8", "12.0.0.0/6", "16.0.0.0/4", "32.0.0.0/3", "64.0.0.0/2",
			"128.0.0.0/3", "160.0.0.0/5", "168.0.0.0/6", "172.0.0.0/12",
		}},
		{"IPv6 aligned block", paRange("fd00::", "fdff:ffff:ffff:ffff:ffff:ffff:ffff:ffff"), []string{"fd00::/8"}},
		{"IPv6 one address", paRange("::", "::"), []string{"::/128"}},
		{"IPv6 everything", paRange("::", "ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff"), []string{"::/0"}},
		{"from after to", paRange("10.0.0.2", "10.0.0.1"), nil},
		{"mixed families", Range{From: netip.MustParseAddr("10.0.0.1"), To: netip.MustParseAddr("fd00::1")}, nil},
		{"zero range", Range{}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []string
			for _, p := range tt.r.Prefixes() {
				got = append(got, p.String())
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("Prefixes = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPaPortAtoms(t *testing.T) {
	tests := []struct {
		name string
		cuts []PortRange
		want []PortRange
	}{
		{"no cuts", nil, []PortRange{{1, 65535}}},
		{"one range", []PortRange{{1000, 2000}}, []PortRange{{1, 999}, {1000, 2000}, {2001, 65535}}},
		{"overlapping ranges, unsorted", []PortRange{{15, 30}, {10, 20}}, []PortRange{{1, 9}, {10, 14}, {15, 20}, {21, 30}, {31, 65535}}},
		{"adjacent ranges stay apart", []PortRange{{21, 30}, {10, 20}}, []PortRange{{1, 9}, {10, 20}, {21, 30}, {31, 65535}}},
		{"duplicates", []PortRange{{10, 20}, {10, 20}}, []PortRange{{1, 9}, {10, 20}, {21, 65535}}},
		{"port zero is not a source port", []PortRange{{0, 20}}, []PortRange{{1, 20}, {21, 65535}}},
		{"the first port", []PortRange{{1, 1}}, []PortRange{{1, 1}, {2, 65535}}},
		{"the last port", []PortRange{{65535, 65535}}, []PortRange{{1, 65534}, {65535, 65535}}},
		{"the whole range", []PortRange{{0, 65535}}, []PortRange{{1, 65535}}},
		{"an inverted range is ignored", []PortRange{{30, 10}}, []PortRange{{1, 65535}}},
		{"only port zero is ignored", []PortRange{{0, 0}}, []PortRange{{1, 65535}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PortAtoms(tt.cuts); !slices.Equal(got, tt.want) {
				t.Errorf("PortAtoms = %v, want %v", got, tt.want)
			}
		})
	}
}
