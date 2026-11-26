package loaded

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
)

// f2bRecording is one of the harness recordings of fail2ban's table (Task 1
// of M11): "absent" when fail2ban had created none.
func f2bRecording(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "f2b", name)) //nolint:gosec // G304: a fixed name under testdata
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// f2bRead reads a host whose nftables holds UFW's tables and, unless table
// is "absent", fail2ban's table as nft prints table.
func f2bRead(t *testing.T, table string) fwstate.Loaded {
	t.Helper()
	x := ldGolden(t, "debian-12", "rules")
	x.answers["/usr/sbin/nft list tables"] = ldResult{out: "table ip filter\ntable ip6 filter\n"}
	if strings.TrimSpace(table) != "absent" {
		x.answers["/usr/sbin/nft list tables"] = ldResult{out: "table ip filter\ntable ip6 filter\ntable inet f2b-table\n"}
		x.answers["/usr/sbin/nft list table inet f2b-table"] = ldResult{out: table}
	}
	st, err := fhReader(true).read(context.Background(), ldEnv(x, nil), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestF2BRecordingsAccepted(t *testing.T) {
	names, err := filepath.Glob(filepath.Join("testdata", "f2b", "*.nft"))
	if err != nil || len(names) != 15 {
		t.Fatalf("recordings %v, %v; want 15", names, err)
	}
	want := map[string]int{"sshd": 1, "recidive": 2}
	for _, path := range names {
		name := filepath.Base(path)
		t.Run(name, func(t *testing.T) {
			rec := f2bRecording(t, name)
			st := f2bRead(t, rec)
			if len(st.Unsupported) != 0 {
				t.Errorf("unsupported %q, want none", st.Unsupported)
			}
			kase := strings.TrimSuffix(name[strings.LastIndexByte(name, '-')+1:], ".nft")
			if strings.TrimSpace(rec) == "absent" {
				if len(st.Neighbours) != 0 {
					t.Errorf("neighbours %+v without a table", st.Neighbours)
				}
				return
			}
			if wantN := []fwstate.Neighbour{{Kind: "fail2ban", Banned: want[kase]}}; !slices.Equal(st.Neighbours, wantN) {
				t.Errorf("neighbours %+v, want %+v", st.Neighbours, wantN)
			}
		})
	}
}

// f2bShape is fail2ban's table on the five OS with every form spec 4
// allows: IPv4 and IPv6 sets, elements over several lines, port lists and
// ranges, protocol lists, counters and each verdict.
const f2bShape = `table inet f2b-table {
	set addr-set-sshd {
		type ipv4_addr
		elements = { 192.0.2.10, 192.0.2.11,
			     198.51.100.7 }
	}

	set addr6-set-sshd {
		type ipv6_addr
		elements = { 2001:db8::1 }
	}

	set addr-set-recidive {
		type ipv4_addr
		elements = { 192.0.2.10 }
	}

	set addr-set-nginx-http-auth {
		type ipv4_addr
	}

	chain f2b-chain {
		type filter hook input priority filter - 1; policy accept;
		tcp dport 22 ip saddr @addr-set-sshd reject with icmp port-unreachable
		tcp dport { 22, 2222 } ip6 saddr @addr6-set-sshd counter packets 3 bytes 180 reject with icmpv6 port-unreachable
		meta l4proto tcp ip saddr @addr-set-recidive reject with icmp port-unreachable
		meta l4proto { tcp, udp } ip saddr @addr-set-recidive drop
		tcp dport { 80, 443, 8000-8100 } ip saddr @addr-set-nginx-http-auth reject with tcp reset
		udp dport 51820 ip saddr @addr-set-nginx-http-auth reject
		tcp dport 22 ip saddr @addr-set-sshd reject with icmpx admin-prohibited
	}
}
`

func TestF2BShapes(t *testing.T) {
	st := f2bRead(t, f2bShape)
	if len(st.Unsupported) != 0 || !slices.Equal(st.Neighbours, []fwstate.Neighbour{{Kind: "fail2ban", Banned: 4}}) {
		t.Errorf("unsupported %q, neighbours %+v; want none and 4 banned", st.Unsupported, st.Neighbours)
	}
	// After the last unban fail2ban keeps the table with empty sets.
	empty := strings.Replace(f2bRecording(t, "ubuntu-24.04-sshd.nft"), "\t\telements = { 192.0.2.10 }\n", "", 1)
	if st := f2bRead(t, empty); len(st.Unsupported) != 0 || !slices.Equal(st.Neighbours, []fwstate.Neighbour{{Kind: "fail2ban", Banned: 0}}) {
		t.Errorf("empty set: unsupported %q, neighbours %+v; want none and 0 banned", st.Unsupported, st.Neighbours)
	}
}

func TestF2BOtherShapesRefused(t *testing.T) {
	sshd := f2bRecording(t, "debian-13-sshd.nft")
	const rule = "\t\ttcp dport 22 ip saddr @addr-set-sshd reject with icmp port-unreachable\n"
	const header = "type filter hook input priority filter - 1; policy accept;"
	const set = "\tset addr-set-sshd {\n\t\ttype ipv4_addr\n\t\telements = { 192.0.2.10 }\n\t}\n"
	replace := func(old, repl string) string {
		if !strings.Contains(sshd, old) {
			t.Fatalf("the recording lacks %q", old)
		}
		return strings.Replace(sshd, old, repl, 1)
	}
	cases := map[string]string{
		"an extra accept rule":            replace(rule, rule+"\t\ttcp dport 80 accept\n"),
		"a second base chain":             replace("\t}\n}\n", "\t}\n\n\tchain other {\n\t\ttype filter hook forward priority filter; policy accept;\n\t}\n}\n"),
		"a second chain of the same name": replace("\t}\n}\n", "\t}\n\n\tchain f2b-chain {\n\t}\n}\n"),
		"policy drop":                     replace(header, "type filter hook input priority filter - 1; policy drop;"),
		"another priority":                replace(header, "type filter hook input priority filter; policy accept;"),
		"another hook":                    replace(header, "type filter hook forward priority filter - 1; policy accept;"),
		"a set of another name":           strings.ReplaceAll(sshd, "addr-set-sshd", "blocked-sshd"),
		"an IPv6 match on an IPv4 set":    replace(rule, "\t\ttcp dport 22 ip6 saddr @addr-set-sshd reject with icmp port-unreachable\n"),
		"an IPv6 address in an IPv4 set":  replace("{ 192.0.2.10 }", "{ 2001:db8::1 }"),
		"an IPv4 set typed ipv6_addr":     replace("\t\ttype ipv4_addr\n", "\t\ttype ipv6_addr\n"),
		"a set of another type":           replace("\t\ttype ipv4_addr\n", "\t\ttype ether_addr\n"),
		"a set with flags":                replace("\t\ttype ipv4_addr\n", "\t\ttype ipv4_addr\n\t\tflags interval\n"),
		"a jump":                          replace(rule, "\t\ttcp dport 22 ip saddr @addr-set-sshd jump elsewhere\n"),
		"an accept verdict":               replace(rule, "\t\ttcp dport 22 ip saddr @addr-set-sshd accept\n"),
		"a destination match":             replace(rule, "\t\ttcp dport 22 ip daddr @addr-set-sshd reject with icmp port-unreachable\n"),
		"an undeclared set":               replace(rule, "\t\ttcp dport 22 ip saddr @addr-set-other reject with icmp port-unreachable\n"),
		"a second table":                  sshd + "table inet other {\n}\n",
		"no chain":                        strings.Replace(sshd, sshd[strings.Index(sshd, "\tchain"):], "}\n", 1),
		"a set after the chain":           replace("\t}\n}\n", "\t}\n\n"+set+"}\n"),
	}
	for name, table := range cases {
		t.Run(name, func(t *testing.T) {
			st := f2bRead(t, table)
			// A table without a base chain filters nothing, so it is no
			// neighbour and no reason either.
			if len(st.Neighbours) != 0 || (len(st.Unsupported) == 0) != (name == "no chain") {
				t.Errorf("neighbours %+v, unsupported %q; want the table foreign", st.Neighbours, st.Unsupported)
			}
			if _, ok := parseF2B([]byte(table)); ok {
				t.Error("parseF2B accepted it")
			}
		})
	}
	t.Run("the same content in table f2b-other", func(t *testing.T) {
		x := ldGolden(t, "debian-12", "rules")
		other := strings.Replace(sshd, "table inet f2b-table", "table inet f2b-other", 1)
		x.answers["/usr/sbin/nft list tables"] = ldResult{out: "table ip filter\ntable ip6 filter\ntable inet f2b-other\n"}
		x.answers["/usr/sbin/nft list table inet f2b-other"] = ldResult{out: other}
		st, err := fhReader(true).read(context.Background(), ldEnv(x, nil), t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		fhWant(t, st.Unsupported, []string{`nft table inet f2b-other: base chain "f2b-chain" on hook "input" has 1 rule(s) and policy accept`})
		if len(st.Neighbours) != 0 {
			t.Errorf("neighbours %+v", st.Neighbours)
		}
	})
}
