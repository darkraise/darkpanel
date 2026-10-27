package netfilter

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// kyRule is the one rule of chain X in input, read by ParseRestore when
// restore is set and by Parse otherwise.
func kyRule(t *testing.T, line string, restore bool) Rule {
	t.Helper()
	in := "*filter\n:X - [0:0]\n:Y - [0:0]\n" + line + "\nCOMMIT\n"
	parse := Parse
	if restore {
		parse = ParseRestore
	}
	rs, err := parse(strings.NewReader(in))
	if err != nil {
		t.Fatalf("%s: %v", line, err)
	}
	return rs.Table("filter").Chain("X").Rules[0]
}

func TestKeyEqualAcrossSyntaxes(t *testing.T) {
	for _, tc := range []struct {
		restore, save string
		ipv6          bool
	}{
		{"-A X -p tcp --dport 22 -j ACCEPT", "-A X -p tcp -m tcp --dport 22 -j ACCEPT", false},
		{"-A X -o eth0 -p tcp -d 172.17.0.2 --dport 80 -j ACCEPT", "-A X -d 172.17.0.2/32 -o eth0 -p tcp -m tcp --dport 80 -j ACCEPT", false},
		{"-A X -p udp --dport 3000 -j REJECT", "-A X -p udp -m udp --dport 3000 -j REJECT --reject-with icmp-port-unreachable", false},
		{"-A X -p udp --dport 3000 -j REJECT", "-A X -p udp -m udp --dport 3000 -j REJECT --reject-with icmp6-port-unreachable", true},
		{"-A X -j REJECT --reject-with tcp-rst", "-A X -j REJECT --reject-with tcp-reset", false},
		{"-A X -p tcp --dport 2222 -m conntrack --ctstate NEW -m recent --set",
			"-A X -p tcp -m tcp --dport 2222 -m conntrack --ctstate NEW -m recent --set --name DEFAULT --mask 255.255.255.255 --rsource", false},
		{"-A X -p tcp --dport 2222 -m recent --update --seconds 30 --hitcount 6 -j Y",
			"-A X -p tcp -m tcp --dport 2222 -m recent --update --seconds 30 --hitcount 6 --name DEFAULT --mask ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff --rsource -j Y", true},
		{"-A X -m limit --limit 3/minute -j LOG --log-prefix \"[UFW LIMIT BLOCK] \"",
			"-A X -m limit --limit 3/min -j LOG --log-prefix \"[UFW LIMIT BLOCK] \"", false},
		{"-A X -m limit --limit-burst 5 -j LOG --log-level warning", "-A X -m limit --limit 3/hour -j LOG", false},
		{"-A X -m limit --limit 10/s -j LOG --log-level 4", "-A X -m limit --limit 10/sec -j LOG", false},
		{"-A X -m limit --limit 2 -j ACCEPT", "-A X -m limit --limit 2/sec -j ACCEPT", false},
		{"-A X -m limit --limit 1/d -j ACCEPT", "-A X -m limit --limit 1/day -j ACCEPT", false},
		{"-A X -j LOG --log-prefix \"[UFW BLOCK] \" -m limit --limit 3/min --limit-burst 10",
			"-A X -m limit --limit 3/min --limit-burst 10 -j LOG --log-prefix \"[UFW BLOCK] \"", false},
		{"-A X -p icmp --icmp-type destination-unreachable -j ACCEPT", "-A X -p icmp -m icmp --icmp-type 3 -j ACCEPT", false},
		{"-A X -p icmp --icmp-type port-unreachable -j ACCEPT", "-A X -p icmp -m icmp --icmp-type 3/3 -j ACCEPT", false},
		{"-A X -p icmpv6 --icmpv6-type neighbor-solicitation -m hl --hl-eq 255 -j ACCEPT",
			"-A X -p ipv6-icmp -m icmp6 --icmpv6-type 135 -m hl --hl-eq 255 -j ACCEPT", true},
		{"-A X -p udp -d ff02::fb --dport 5353 -j ACCEPT", "-A X -d ff02::fb/128 -p udp -m udp --dport 5353 -j ACCEPT", true},
		{"-A X -s 10.1.2.3/8 -j DROP", "-A X -s 10.0.0.0/8 -j DROP", false},
		{"-A X -s 0.0.0.0/0 -j DROP", "-A X -j DROP", false},
		{"-A X -s ::/0 -j DROP", "-A X -j DROP", true},
		{"-A X -d 0.0.0.0/0 -p all -j ACCEPT", "-A X -j ACCEPT", false},
		{"-A X -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT", "-A X -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT", false},
		{"-A X -m mark --mark 1 -j ACCEPT", "-A X -m mark --mark 0x1 -j ACCEPT", false},
		{"-A X -m connmark --mark 0x10/0xff -j ACCEPT", "-A X -m connmark --mark 0x10/0xff -j ACCEPT", false},
		{"-A X -p 6 -m multiport --dports 443,8000:8100 -s 10.0.0.0/8 -j ACCEPT",
			"-A X -s 10.0.0.0/8 -p tcp -m multiport --dports 443,8000:8100 -j ACCEPT", false},
		{"-A X -p udp --dport 68 --sport 67 -j ACCEPT", "-A X -p udp -m udp --sport 67 --dport 68 -j ACCEPT", false},
	} {
		r, s := kyRule(t, tc.restore, true), kyRule(t, tc.save, false)
		if kr, ks := Key(r, tc.ipv6), Key(s, tc.ipv6); kr != ks {
			t.Errorf("ipv6 %v:\n%s\n -> %s\n%s\n -> %s", tc.ipv6, tc.restore, kr, tc.save, ks)
		}
	}
}

func TestKeyDistinguishes(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		ipv6 bool
	}{
		{"-A X -p tcp --dport 22 -j ACCEPT", "-A X -p tcp --dport 23 -j ACCEPT", false},
		{"-A X -p tcp --dport 22 -j ACCEPT", "-A X -p tcp ! --dport 22 -j ACCEPT", false},
		{"-A X -s 10.0.0.0/8 -j ACCEPT", "-A X ! -s 10.0.0.0/8 -j ACCEPT", false},
		{"-A X -s 10.0.0.0/8 -j ACCEPT", "-A X -d 10.0.0.0/8 -j ACCEPT", false},
		{"-A X -i eth0 -j ACCEPT", "-A X -o eth0 -j ACCEPT", false},
		{"-A X -p tcp --dport 3000 -j REJECT", "-A X -p tcp --dport 3000 -j REJECT --reject-with tcp-reset", false},
		{"-A X -m recent --set", "-A X -m recent --set --rdest", false},
		{"-A X -m recent --set", "-A X -m recent --set --name other", false},
		{"-A X -m limit --limit 3/min -j LOG", "-A X -m limit --limit 3/hour -j LOG", false},
		{"-A X -m conntrack --ctstate NEW -m recent --set", "-A X -m recent --set -m conntrack --ctstate NEW", false},
		{"-A X -j Y", "-A X -j ACCEPT", false},
		{"-A X -m comment --comment \"a b\" -j ACCEPT", "-A X -m comment --comment a -m comment --comment b -j ACCEPT", false},
		{"-A X -m multiport --dports 80 --dports 90 -j ACCEPT", "-A X -m multiport --dports 90 -j ACCEPT", false},
	} {
		ka, kb := Key(kyRule(t, tc.a, true), tc.ipv6), Key(kyRule(t, tc.b, true), tc.ipv6)
		if ka == kb {
			t.Errorf("%s and %s share the key %s", tc.a, tc.b, ka)
		}
	}
	// A family default differs between the families.
	r := kyRule(t, "-A X -j REJECT", true)
	if Key(r, false) == Key(r, true) {
		t.Errorf("REJECT has the key %s in both families", Key(r, false))
	}
	// An opaque rule keys by its text, so it equals only a rule written the
	// same way.
	o1, o2 := kyRule(t, "-A X -m set --match-set a src -j ACCEPT", true), kyRule(t, "-A X -m set --match-set b src -j ACCEPT", true)
	if !o1.Opaque() || Key(o1, false) == Key(o2, false) {
		t.Errorf("opaque rules: %s and %s", Key(o1, false), Key(o2, false))
	}
	// A mark spelling iptables refuses keeps its text, so it never shares the
	// key of a mark iptables loads.
	for _, in := range []string{"0o1", "0O1", "1_0", "0x1_0", "0b1", "1/0o7"} {
		want := "-m mark --mark " + in + " -j ACCEPT"
		if got := Key(kyRule(t, "-A X -m mark --mark "+in+" -j ACCEPT", true), false); got != want {
			t.Errorf("mark %s has the key %s, want %s", in, got, want)
		}
	}
	if got, want := Key(kyRule(t, "-A X -m mark --mark 017 -j ACCEPT", true), false), "-m mark --mark 0xf -j ACCEPT"; got != want {
		t.Errorf("mark 017 has the key %s, want %s", got, want)
	}
}

// TestKeyMatchesUFW is the acceptance test of Key: on every captured image
// and family, each chain of the filter table iptables-save printed holds
// exactly the rules, by Key and in order, of the same chain of the files
// UFW loaded (ufwLoad), and the built-in chains have the same policies.
func TestKeyMatchesUFW(t *testing.T) {
	for _, image := range ufwImages {
		for _, ipv6 := range []bool{false, true} {
			save, family := "iptables.save", "v4"
			if ipv6 {
				save, family = "ip6tables.save", "v6"
			}
			t.Run(image+"/"+family, func(t *testing.T) {
				f, err := os.Open(filepath.Join("testdata", "ufw", image, save)) //nolint:gosec // G304: a fixture under testdata/
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = f.Close() }()
				saved, err := Parse(f)
				if err != nil {
					t.Fatal(err)
				}
				loaded, err := ParseRestore(strings.NewReader(ufwLoad(t, image, ipv6)))
				if err != nil {
					t.Fatal(err)
				}
				st, lt := saved.Table("filter"), loaded.Table("filter")
				names, loadedNames := chainNames(st), chainNames(lt)
				slices.Sort(names)
				slices.Sort(loadedNames)
				if !slices.Equal(names, loadedNames) {
					t.Fatalf("chains differ:\nsaved  %v\nloaded %v", names, loadedNames)
				}
				if len(names) < 30 {
					t.Fatalf("only %d chains: the capture is not UFW's", len(names))
				}
				rules := 0
				for _, name := range names {
					sc, lc := st.Chain(name), lt.Chain(name)
					if sc.Policy != lc.Policy {
						t.Errorf("chain %s: policy %q saved, %q loaded", name, sc.Policy, lc.Policy)
					}
					var sk, lk []string
					for _, r := range sc.Rules {
						sk = append(sk, Key(r, ipv6))
					}
					for _, r := range lc.Rules {
						lk = append(lk, Key(r, ipv6))
					}
					rules += len(sk)
					if !slices.Equal(sk, lk) {
						t.Errorf("chain %s:\nsaved\n  %s\nloaded\n  %s", name, strings.Join(sk, "\n  "), strings.Join(lk, "\n  "))
					}
				}
				if rules < 60 {
					t.Errorf("only %d rules compared", rules)
				}
			})
		}
	}
}
