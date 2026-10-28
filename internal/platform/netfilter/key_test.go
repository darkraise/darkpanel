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

// kxIcmp4 and kxIcmp6 are the icmp type spellings that iptables and ip6tables
// 1.8.11 (extensions/libxt_icmp.h, in iptables-1.8.11.tar.xz from
// netfilter.org) accepted in a container, with the type iptables-save then
// printed: every name of the tables, a type number, a code, a code range
// and a case or prefix variation.
var kxIcmp4 = []struct{ in, saved string }{
	{"any", "any"},
	{"echo-reply", "0"},
	{"pong", "0"},
	{"destination-unreachable", "3"},
	{"network-unreachable", "3/0"},
	{"host-unreachable", "3/1"},
	{"protocol-unreachable", "3/2"},
	{"port-unreachable", "3/3"},
	{"fragmentation-needed", "3/4"},
	{"source-route-failed", "3/5"},
	{"network-unknown", "3/6"},
	{"host-unknown", "3/7"},
	{"network-prohibited", "3/9"},
	{"host-prohibited", "3/10"},
	{"TOS-network-unreachable", "3/11"},
	{"TOS-host-unreachable", "3/12"},
	{"communication-prohibited", "3/13"},
	{"host-precedence-violation", "3/14"},
	{"precedence-cutoff", "3/15"},
	{"source-quench", "4"},
	{"redirect", "5"},
	{"network-redirect", "5/0"},
	{"host-redirect", "5/1"},
	{"TOS-network-redirect", "5/2"},
	{"TOS-host-redirect", "5/3"},
	{"echo-request", "8"},
	{"ping", "8"},
	{"router-advertisement", "9"},
	{"router-solicitation", "10"},
	{"time-exceeded", "11"},
	{"ttl-exceeded", "11"},
	{"ttl-zero-during-transit", "11/0"},
	{"ttl-zero-during-reassembly", "11/1"},
	{"parameter-problem", "12"},
	{"ip-header-bad", "12/0"},
	{"required-option-missing", "12/1"},
	{"timestamp-request", "13"},
	{"timestamp-reply", "14"},
	{"address-mask-request", "17"},
	{"address-mask-reply", "18"},
	{"any", "any"},
	{"255", "any"},
	{"3/0", "3/0"},
	{"3/0:255", "3"},
	{"3/1:2", "3/1"},
	{"3/4", "3/4"},
	{"8/0", "8/0"},
	{"Port-Unreachable", "3/3"},
	{"TOS-host-redirect", "5/3"},
	{"tos-host-redirect", "5/3"},
	{"dest", "3"},
	{"pa", "12"},
	{"0x3", "3"},
	{"03", "3"},
	{"3/0x1", "3/1"},
}

var kxIcmp6 = []struct{ in, saved string }{
	{"destination-unreachable", "1"},
	{"no-route", "1/0"},
	{"communication-prohibited", "1/1"},
	{"beyond-scope", "1/2"},
	{"address-unreachable", "1/3"},
	{"port-unreachable", "1/4"},
	{"failed-policy", "1/5"},
	{"reject-route", "1/6"},
	{"packet-too-big", "2"},
	{"time-exceeded", "3"},
	{"ttl-exceeded", "3"},
	{"ttl-zero-during-transit", "3/0"},
	{"ttl-zero-during-reassembly", "3/1"},
	{"parameter-problem", "4"},
	{"bad-header", "4/0"},
	{"unknown-header-type", "4/1"},
	{"unknown-option", "4/2"},
	{"echo-request", "128"},
	{"ping", "128"},
	{"echo-reply", "129"},
	{"pong", "129"},
	{"mld-listener-query", "130"},
	{"mld-listener-report", "131"},
	{"mld-listener-done", "132"},
	{"mld-listener-reduction", "132"},
	{"router-solicitation", "133"},
	{"router-advertisement", "134"},
	{"neighbour-solicitation", "135"},
	{"neighbor-solicitation", "135"},
	{"neighbour-advertisement", "136"},
	{"neighbor-advertisement", "136"},
	{"redirect", "137"},
	{"255", "255"},
	{"1/0", "1/0"},
	{"1/1:2", "1/1"},
	{"128", "128"},
	{"dest", "1"},
	{"ECHO-REQUEST", "128"},
	{"mld-listener-d", "132"},
	{"0x80", "128"},
	{"1/0:255", "1"},
	{"1/3:3", "1/3"},
}

// kxIcmpRefused are spellings that iptables and ip6tables refused: an
// ambiguous prefix, a type range, and "any" for ICMPv6. The key keeps them
// as written, so a rule that cannot be loaded never equals one that can.
var kxIcmpRefused = []struct {
	ipv6 bool
	in   string
}{
	{false, "echo"},
	{false, "echo-r"},
	{false, "3:4"},
	{false, "a"},
	{true, "mld"},
	{true, "neighbo"},
	{true, "any"},
}

func TestKxIcmpTypes(t *testing.T) {
	for _, ipv6 := range []bool{false, true} {
		proto, module, opt, rows := "icmp", "icmp", "icmp-type", kxIcmp4
		if ipv6 {
			proto, module, opt, rows = "ipv6-icmp", "icmp6", "icmpv6-type", kxIcmp6
		}
		for _, tc := range rows {
			got := Key(kyRule(t, "-A X -p "+proto+" --"+opt+" "+tc.in+" -j ACCEPT", true), ipv6)
			want := "-p " + proto + " -m " + module + " --" + opt + " " + tc.saved + " -j ACCEPT"
			if got != want {
				t.Errorf("ipv6 %v: --%s %s has the key %q, want %q", ipv6, opt, tc.in, got, want)
			}
		}
	}
	for _, tc := range kxIcmpRefused {
		proto, opt := "icmp", "icmp-type"
		if tc.ipv6 {
			proto, opt = "ipv6-icmp", "icmpv6-type"
		}
		got := Key(kyRule(t, "-A X -p "+proto+" --"+opt+" "+tc.in+" -j ACCEPT", true), tc.ipv6)
		if want := "--" + opt + " " + tc.in + " -j ACCEPT"; !strings.HasSuffix(got, want) {
			t.Errorf("ipv6 %v: the refused --%s %s has the key %q, want it kept as written", tc.ipv6, opt, tc.in, got)
		}
	}
}

// kxReject are the --reject-with spellings that iptables and ip6tables 1.8.11
// (extensions/libipt_REJECT.c and libip6t_REJECT.c) accepted in a container,
// with the name iptables-save then printed: the names and aliases, a name in
// capitals and a prefix, which matches the first entry of the table in its
// order.
var kxReject = []struct {
	ipv6      bool
	in, saved string
}{
	{false, "icmp-net-unreachable", "icmp-net-unreachable"},
	{false, "net-unreach", "icmp-net-unreachable"},
	{false, "NET-UNREACH", "icmp-net-unreachable"},
	{false, "icmp-port", "icmp-port-unreachable"},
	{false, "port", "icmp-port-unreachable"},
	{false, "p", "icmp-proto-unreachable"},
	{false, "icmp-p", "icmp-proto-unreachable"},
	{false, "icmp-h", "icmp-host-unreachable"},
	{false, "h", "icmp-host-unreachable"},
	{false, "host", "icmp-host-unreachable"},
	{false, "net", "icmp-net-unreachable"},
	{false, "tcp", "tcp-reset"},
	{false, "tcp-rst", "tcp-reset"},
	{false, "tcp-reset", "tcp-reset"},
	{false, "admin", "icmp-admin-prohibited"},
	{false, "admin-prohib", "icmp-admin-prohibited"},
	{false, "icmp-admin", "icmp-admin-prohibited"},
	{true, "icmp6-no-route", "icmp6-no-route"},
	{true, "no-route", "icmp6-no-route"},
	{true, "NO-ROUTE", "icmp6-no-route"},
	{true, "adm-prohibited", "icmp6-adm-prohibited"},
	{true, "icmp6-adm", "icmp6-adm-prohibited"},
	{true, "addr-unreach", "icmp6-addr-unreachable"},
	{true, "port-unreach", "icmp6-port-unreachable"},
	{true, "port", "icmp6-port-unreachable"},
	{true, "p", "icmp6-port-unreachable"},
	{true, "icmp6", "icmp6-no-route"},
	{true, "icmp6-p", "icmp6-port-unreachable"},
	{true, "tcp", "tcp-reset"},
	{true, "tcp-reset", "tcp-reset"},
	{true, "policy-fail", "icmp6-policy-fail"},
	{true, "policy", "icmp6-policy-fail"},
	{true, "reject-route", "icmp6-reject-route"},
	{true, "reject", "icmp6-reject-route"},
	{true, "r", "icmp6-reject-route"},
}

// kxRejectRefused are spellings that the tools refused. The key keeps them
// as written, so a rule that cannot be loaded never equals one that can.
var kxRejectRefused = []struct {
	ipv6 bool
	in   string
}{
	{false, "echo-reply"},
	{false, "nope"},
	{true, "tcp-rst"},
	{true, "icmp-port-unreachable"},
	{true, "nope"},
}

// kxLogLevels are the --log-level spellings that iptables 1.8.11 accepted,
// with the level iptables-save then printed (no level for the default 4), and
// kxLogRefused those it refused: it takes the syslog names alert, crit,
// debug, emerg, error, info, notice, panic and warning in lower case, and
// the numbers 0 to 7.
var kxLogLevels = []struct{ in, saved string }{
	{"emerg", "0"},
	{"alert", "1"},
	{"crit", "2"},
	{"error", "3"},
	{"warning", "4"},
	{"notice", "5"},
	{"info", "6"},
	{"debug", "7"},
	{"panic", "0"},
	{"0", "0"},
	{"1", "1"},
	{"4", "4"},
	{"7", "7"},
}

var kxLogRefused = []string{
	"err",
	"warn",
	"none",
	"EMERG",
	"Warning",
	"8",
	"warni",
}

func TestKxRejectNames(t *testing.T) {
	for _, tc := range kxReject {
		got := Key(kyRule(t, "-A X -p tcp -j REJECT --reject-with "+tc.in, true), tc.ipv6)
		if want := "-j REJECT --reject-with " + tc.saved; !strings.HasSuffix(got, want) {
			t.Errorf("ipv6 %v: --reject-with %s has the key %q, want it to end with %q", tc.ipv6, tc.in, got, want)
		}
	}
	for _, tc := range kxRejectRefused {
		got := Key(kyRule(t, "-A X -p tcp -j REJECT --reject-with "+tc.in, true), tc.ipv6)
		if want := "-j REJECT --reject-with " + tc.in; !strings.HasSuffix(got, want) {
			t.Errorf("ipv6 %v: the refused --reject-with %s has the key %q, want it kept as written", tc.ipv6, tc.in, got)
		}
	}
}

func TestKxLogLevels(t *testing.T) {
	for _, tc := range kxLogLevels {
		got := Key(kyRule(t, "-A X -j LOG --log-level "+tc.in, true), false)
		want := "-j LOG --log-level " + tc.saved
		if tc.saved == "4" {
			want = "-j LOG"
		}
		if got != want {
			t.Errorf("--log-level %s has the key %q, want %q", tc.in, got, want)
		}
	}
	for _, in := range kxLogRefused {
		got := Key(kyRule(t, "-A X -j LOG --log-level "+in, true), false)
		if want := "-j LOG --log-level " + in; got != want {
			t.Errorf("the refused --log-level %s has the key %q, want %q", in, got, want)
		}
	}
}
