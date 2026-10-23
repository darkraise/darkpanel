package netfilter

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestClassify(t *testing.T) {
	for _, tc := range []struct {
		rule string
		want []string
	}{
		{"-A X -p tcp -m multiport --dports 80,443 -m conntrack --ctstate NEW --ctorigdstport 8080 -j ACCEPT", nil},
		{"-A X -m connmark --mark 0x1/0x1 -m statistic --mode random --probability 0.5 -j RETURN", nil},
		{"-A X -m state --state NEW -m addrtype ! --src-type LOCAL -j REJECT --reject-with tcp-reset", nil},
		{"-A X -m recent --rcheck --seconds 60 --name ssh --rsource -j DROP", nil},
		{"-A X -j Y", nil},
		{"-A X -m set --match-set allow src -j ACCEPT", []string{"match set"}},
		{"-A X -m pkttype --pkt-type broadcast -j NFLOG", []string{"match pkttype", "target NFLOG"}},
		{"-A X -p tcp -m tcp --syn -j ACCEPT", []string{"match tcp option --syn"}},
		{"-A X -p tcp -m tcp --tcp-flags SYN -j ACCEPT", []string{"match tcp option --tcp-flags with 1 values"}},
		{"-A X -m comment ! --comment x -j ACCEPT", []string{"match comment option ! --comment"}},
		{"-A X -j DNAT --to-destination 10.0.0.1:80 --random", []string{"target DNAT option --random"}},
		{"-A X -j Y --log-prefix x", []string{"jump to Y option --log-prefix"}},
		{"-A X -j ACCEPT --to-ports 80", []string{"target ACCEPT option --to-ports"}},
		{"-A X -j MARK ! --set-xmark 0x1/0x1", []string{"target MARK option ! --set-xmark"}},
		{"-A X -m comment --comment -leading -j ACCEPT", []string{"match comment option --comment with 0 values", "match comment option --leading"}},
	} {
		rs, err := Parse(strings.NewReader("*filter\n:X - [0:0]\n:Y - [0:0]\n" + tc.rule + "\nCOMMIT\n"))
		if err != nil {
			t.Fatalf("%s: %v", tc.rule, err)
		}
		if got := rs.Tables[0].Chain("X").Rules[0].Unsupported; !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s:\n got %q\nwant %q", tc.rule, got, tc.want)
		}
	}
}

// TestFixtures parses real save output: Docker 29.5.3 (iptables-nft
// 1.8.11, captured 2026-10-23 and trimmed to three networks), UFW 0.36.2
// on Ubuntu 24.04 (captured 2026-10-23) and tailscaled's chains
// (reconstructed from tailscale's util/linuxfw at v1.104.2). None of it may be opaque.
func TestFixtures(t *testing.T) {
	for _, tc := range []struct {
		file          string
		tables, rules int
		check         func(*testing.T, *Ruleset)
	}{
		{"docker29-v4.save", 5, 28, func(t *testing.T, rs *Ruleset) {
			r := rs.Table("nat").Chain("DOCKER").Rules[0]
			if r.Dest.Prefix.String() != "127.0.0.1/32" || r.In != (NameMatch{Name: "br-a2871ddf38b3", Neg: true}) ||
				r.Target.Name != "DNAT" || r.Target.Options[0].Values[0] != "172.21.0.2:5432" {
				t.Fatalf("nat DOCKER rule = %+v", r)
			}
			if c := rs.Table("filter").Chain("DOCKER-USER"); len(c.Rules) != 0 {
				t.Fatalf("DOCKER-USER = %+v", c)
			}
		}},
		{"ufw-v4.save", 1, 71, func(t *testing.T, rs *Ruleset) {
			r := rs.Table("filter").Chain("ufw-user-input").Rules[2]
			if r.Matches[2].Module != "recent" || r.Target.Name != "ufw-user-limit" || !r.Target.Chain {
				t.Fatalf("limit rule = %+v", r)
			}
		}},
		{"ufw-v6.save", 1, 110, func(t *testing.T, rs *Ruleset) {
			r := rs.Table("filter").Chain("ufw6-before-input").Rules[1]
			if r.Matches[0].Module != "rt" || r.Target.Name != "DROP" {
				t.Fatalf("rt rule = %+v", r)
			}
		}},
		{"tailscaled-v4.save", 3, 20, func(t *testing.T, rs *Ruleset) {
			r := rs.Table("filter").Chain("ts-forward").Rules[2]
			if o := r.Matches[0].Options[0]; o.Name != "ctstate" || !o.Neg {
				t.Fatalf("stateful rule = %+v", r)
			}
		}},
	} {
		t.Run(tc.file, func(t *testing.T) {
			f, err := os.Open(filepath.Join("testdata", tc.file))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = f.Close() }()
			rs, err := Parse(f)
			if err != nil {
				t.Fatal(err)
			}
			rules := 0
			for _, tb := range rs.Tables {
				for _, c := range tb.Chains {
					for _, r := range c.Rules {
						rules++
						if r.Opaque() {
							t.Errorf("line %d opaque: %q", r.Line, r.Unsupported)
						}
					}
				}
			}
			if len(rs.Tables) != tc.tables || rules != tc.rules {
				t.Fatalf("%d tables, %d rules; want %d, %d", len(rs.Tables), rules, tc.tables, tc.rules)
			}
			tc.check(t, rs)
		})
	}
}
