package netfilter

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// ufwImages are the OS images whose UFW files and filter tables
// testdata/ufw holds (testdata/ufw/capture.sh).
var ufwImages = []string{"debian-12", "debian-13", "ubuntu-22-04", "ubuntu-24-04"}

// ufwLoad is the restore input UFW's first start loads for one family of
// an image, in the order of ufw_start in ufw-init-functions: the primary
// chains with the built-in chains' jumps into them, the reject and
// tracking rules of the default policies, the logging and skip-to-policy
// chains, before*.rules, after*.rules, the user chains, user*.rules, the
// hooks into the user chains and the built-in policies.
func ufwLoad(t *testing.T, image string, ipv6 bool) string {
	t.Helper()
	dir := filepath.Join("testdata", "ufw", image)
	read := func(name string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(dir, name)) //nolint:gosec // G304: a fixture under testdata/
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	policy := map[string]string{}
	for _, line := range strings.Split(read("default"), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok && strings.HasPrefix(k, "DEFAULT_") && strings.HasSuffix(k, "_POLICY") {
			policy[strings.TrimSuffix(strings.TrimPrefix(k, "DEFAULT_"), "_POLICY")] = strings.Trim(v, `"`)
		}
	}
	typ := ""
	if ipv6 {
		typ = "6"
	}
	u := "ufw" + typ
	dirs := []string{"input", "output", "forward"}
	var b strings.Builder
	w := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }
	w("*filter")
	for _, c := range []string{"before-logging-input", "before-logging-output", "before-logging-forward",
		"before-input", "before-output", "before-forward", "after-input", "after-output", "after-forward",
		"after-logging-input", "after-logging-output", "after-logging-forward",
		"reject-input", "reject-output", "reject-forward", "track-input", "track-output", "track-forward"} {
		w(":%s-%s - [0:0]", u, c)
	}
	for _, d := range dirs {
		for _, c := range []string{"before-logging", "before", "after", "after-logging", "reject", "track"} {
			w("-A %s -j %s-%s-%s", strings.ToUpper(d), u, c, d)
		}
	}
	w("COMMIT")
	for _, d := range dirs {
		if policy[strings.ToUpper(d)] == "REJECT" {
			w("*filter\n-A %s-reject-%s -j REJECT\nCOMMIT", u, d)
		}
	}
	for _, d := range dirs {
		if policy[strings.ToUpper(d)] == "ACCEPT" {
			w("*filter\n-A %[1]s-track-%[2]s -p tcp -m conntrack --ctstate NEW -j ACCEPT\n"+
				"-A %[1]s-track-%[2]s -p udp -m conntrack --ctstate NEW -j ACCEPT\nCOMMIT", u, d)
		}
	}
	w("*filter\n:%[1]s-logging-deny - [0:0]\n:%[1]s-logging-allow - [0:0]\nCOMMIT", u)
	w("*filter")
	for _, d := range dirs {
		w(":%s-skip-to-policy-%s - [0:0]", u, d)
	}
	for _, d := range dirs {
		w("-A %s-skip-to-policy-%s -j %s", u, d, policy[strings.ToUpper(d)])
	}
	w("COMMIT")
	b.WriteString(read("before" + typ + ".rules"))
	b.WriteString(read("after" + typ + ".rules"))
	w("*filter")
	for _, c := range []string{"user-input", "user-output", "user-forward", "user-logging-input", "user-logging-output",
		"user-logging-forward", "user-limit", "user-limit-accept"} {
		w(":%s-%s - [0:0]", u, c)
	}
	w("COMMIT")
	b.WriteString(read("user" + typ + ".rules"))
	w("*filter\n-A %[1]s-before-input -j %[1]s-user-input\n-A %[1]s-before-output -j %[1]s-user-output\n"+
		"-A %[1]s-before-forward -j %[1]s-user-forward\nCOMMIT", u)
	builtin := func(d string) string {
		if p := policy[d]; p != "REJECT" {
			return p
		}
		return "DROP"
	}
	w("*filter\n:INPUT %s [0:0]\n:FORWARD %s [0:0]\n:OUTPUT %s [0:0]\nCOMMIT", builtin("INPUT"), builtin("FORWARD"), builtin("OUTPUT"))
	return b.String()
}

const rsSample = `# a comment
*filter
:ufw-a - [0:0]
:ufw-b - [0:0]
-A ufw-a -p tcp --dport 22 -m conntrack --ctstate NEW -m recent --set
-A ufw-a -p udp -s 10.0.0.1 --sport 67 --dport 68 -j ACCEPT
-A ufw-a -p icmp --icmp-type echo-request -d 192.0.2.7/24 -j ufw-b
-A ufw-a -j LOG --log-prefix "[UFW BLOCK] " -m limit --limit 3/min --limit-burst 10
-I ufw-a -m conntrack --ctstate INVALID -j RETURN
-A ufw-a -p 6 -m multiport --dports 80,443 -j ACCEPT
-A INPUT -j ufw-a
COMMIT
*filter
:ufw-b - [0:0]
:INPUT DROP [0:0]
-A ufw-b -p icmpv6 --icmpv6-type 135 -s fe80::1 -j ufw-late
-A ufw-b -p all -j ACCEPT
-I ufw-b 2 -p tcp -m tcp --dport 80 -j ACCEPT
:ufw-late - [0:0]
COMMIT
`

func TestParseRestoreSyntax(t *testing.T) {
	rs, err := ParseRestore(strings.NewReader(rsSample))
	if err != nil {
		t.Fatal(err)
	}
	if len(rs.Tables) != 1 || rs.Tables[0].Name != "filter" {
		t.Fatalf("tables = %+v, want filter once", rs.Tables)
	}
	f := rs.Table("filter")
	if names := chainNames(f); !slices.Equal(names, []string{"ufw-a", "ufw-b", "INPUT", "ufw-late"}) {
		t.Fatalf("chains = %v", names)
	}
	a := f.Chain("ufw-a").Rules
	if len(a) != 6 || a[0].Line != 9 || a[0].Target.Name != "RETURN" {
		t.Fatalf("ufw-a = %+v, want the -I rule first", a)
	}
	want := Rule{
		Line: 5, Text: "-A ufw-a -p tcp --dport 22 -m conntrack --ctstate NEW -m recent --set",
		Proto: NameMatch{Name: "tcp"},
		Matches: []Match{
			{Module: "tcp", Options: []Option{{Name: "dport", Values: []string{"22"}}}},
			{Module: "conntrack", Options: []Option{{Name: "ctstate", Values: []string{"NEW"}}}},
			{Module: "recent", Options: []Option{{Name: "set"}}},
		},
	}
	if !reflect.DeepEqual(a[1], want) {
		t.Errorf("implicit tcp rule:\n got %+v\nwant %+v", a[1], want)
	}
	udp := a[2]
	if udp.Source.Prefix != netip.MustParsePrefix("10.0.0.1/32") || len(udp.Matches) != 1 ||
		!reflect.DeepEqual(udp.Matches[0], Match{Module: "udp", Options: []Option{
			{Name: "sport", Values: []string{"67"}}, {Name: "dport", Values: []string{"68"}}}}) {
		t.Errorf("udp rule = %+v, want a host source and one implicit udp match", udp)
	}
	icmp := a[3]
	if icmp.Dest.Prefix != netip.MustParsePrefix("192.0.2.0/24") || icmp.Matches[0].Module != "icmp" ||
		!icmp.Target.Chain || icmp.Target.Name != "ufw-b" {
		t.Errorf("icmp rule = %+v, want a masked destination, an implicit icmp match and a jump", icmp)
	}
	logRule := a[4]
	if logRule.Target.Name != "LOG" || len(logRule.Target.Options) != 1 || logRule.Matches[0].Module != "limit" ||
		len(logRule.Matches[0].Options) != 2 {
		t.Errorf("LOG rule = %+v, want the target's option and the limit match after it", logRule)
	}
	if a[5].Proto.Name != "tcp" || a[5].Matches[0].Module != "multiport" {
		t.Errorf("protocol 6 = %+v, want tcp", a[5])
	}
	b := f.Chain("ufw-b").Rules
	if len(b) != 3 || b[1].Line != 18 {
		t.Fatalf("ufw-b = %+v, want the redeclared chain's three rules with the -I at position 2", b)
	}
	if b[0].Proto.Name != "ipv6-icmp" || b[0].Matches[0].Module != "icmp6" || b[0].Source.Prefix != netip.MustParsePrefix("fe80::1/128") ||
		!b[0].Target.Chain {
		t.Errorf("icmpv6 rule = %+v, want ipv6-icmp, an implicit icmp6 match, a host source and a jump to a later chain", b[0])
	}
	if b[2].Proto != (NameMatch{}) {
		t.Errorf("-p all = %+v, want no protocol", b[2].Proto)
	}
	in := f.Chain("INPUT")
	if !in.Builtin || in.Policy != "DROP" || len(in.Rules) != 1 || !in.Rules[0].Target.Chain {
		t.Errorf("INPUT = %+v, want the built-in chain with its policy and the rule appended before it was declared", in)
	}
	for _, c := range f.Chains {
		for _, r := range c.Rules {
			if r.Opaque() {
				t.Errorf("line %d opaque: %q", r.Line, r.Unsupported)
			}
		}
	}
}

func chainNames(t *Table) []string {
	var out []string
	for _, c := range t.Chains {
		out = append(out, c.Name)
	}
	return out
}

func TestParseRestoreChains(t *testing.T) {
	in := "*nat\n-A POSTROUTING -j MASQUERADE\n:POSTROUTING ACCEPT [0:0]\n-A OUTPUT -j DOCKER\nCOMMIT\n"
	rs, err := ParseRestore(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	nat := rs.Table("nat")
	if c := nat.Chain("POSTROUTING"); !c.Builtin || c.Policy != "ACCEPT" || len(c.Rules) != 1 {
		t.Errorf("POSTROUTING = %+v, want built-in with its policy, keeping its rule", c)
	}
	if c := nat.Chain("OUTPUT"); !c.Builtin || c.Policy != "" {
		t.Errorf("OUTPUT = %+v, want built-in without a policy", c)
	}
	if r := nat.Chain("OUTPUT").Rules[0]; r.Target.Chain || !r.Opaque() {
		t.Errorf("a jump to a chain the input never names = %+v, want an opaque target", r)
	}
	flushed, err := ParseRestore(strings.NewReader("*filter\n:X - [0:0]\n-A X -j ACCEPT\n:X - [0:0]\n-A X -j DROP\nCOMMIT\n"))
	if err != nil {
		t.Fatal(err)
	}
	if r := flushed.Table("filter").Chain("X").Rules; len(r) != 1 || r[0].Target.Name != "DROP" {
		t.Errorf("a redeclared chain = %+v, want it flushed", r)
	}
}

func TestParseRestoreErrors(t *testing.T) {
	for name, tc := range map[string]struct{ in, want string }{
		"unknown table":       {"*broute\nCOMMIT\n", `line 1: unknown table "broute"`},
		"nested table":        {"*nat\n*raw\n", "line 2: table raw opened inside table nat"},
		"no commit":           {"*nat\n:X - [0:0]\n", "table nat has no COMMIT"},
		"stray commit":        {"COMMIT\n", "line 1: COMMIT outside a table"},
		"chain outside":       {":X - [0:0]\n", "line 1: chain declared outside a table"},
		"rule outside":        {"-A X -j ACCEPT\n", "line 1: rule outside a table"},
		"bad declaration":     {"*nat\n:X\n", `line 2: malformed chain declaration "X"`},
		"bad policy":          {"*filter\n:INPUT REJECT [0:0]\n", `line 2: chain INPUT has policy "REJECT"`},
		"user chain policy":   {"*filter\n:X DROP [0:0]\n", "line 2: chain X is not built-in, so it has no policy"},
		"no chain":            {"*filter\n-A\n", "line 2: -A without a chain"},
		"insert beyond":       {"*filter\n:X - [0:0]\n-I X 2 -j ACCEPT\n", "line 3: insert at 2 into chain X of 0 rules"},
		"insert at zero":      {"*filter\n:X - [0:0]\n-I X 0 -j ACCEPT\n", "line 3: insert at 0 into chain X of 0 rules"},
		"other command":       {"*filter\n-D X 1\n", `line 2: unexpected line "-D X 1"`},
		"unterminated":        {"*filter\n-A X -m comment --comment \"open\n", "line 2: unterminated quote"},
		"loose value":         {"*filter\n-A X extra\n", `line 2: value "extra" without an option`},
		"empty":               {"", "no tables in the input"},
		"comments only":       {"# nothing\n\n", "no tables in the input"},
		"incompatible note":   {"# Table `raw' is incompatible, use 'nft' tool.\n", "no tables in the input"},
		"undeclared one rule": {"*filter\n-A X -j\n", "line 2: -j needs one name"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ParseRestore(strings.NewReader(tc.in))
			if err == nil || err.Error() != "netfilter: "+tc.want {
				t.Fatalf("err = %v, want netfilter: %s", err, tc.want)
			}
			if strings.HasSuffix(tc.want, "no tables in the input") && !errors.Is(err, ErrNoTables) {
				t.Fatalf("err = %v, want ErrNoTables", err)
			}
		})
	}
}

// TestParseRestoreUFW parses every rules file UFW loads on each image, alone
// and as the whole load of its first start, which leaves no rule opaque.
func TestParseRestoreUFW(t *testing.T) {
	for _, image := range ufwImages {
		for _, ipv6 := range []bool{false, true} {
			typ := ""
			if ipv6 {
				typ = "6"
			}
			t.Run(image+"/v4"+typ, func(t *testing.T) {
				for _, file := range []string{"before", "after", "user"} {
					b, err := os.ReadFile(filepath.Join("testdata", "ufw", image, file+typ+".rules")) //nolint:gosec // G304: a fixture under testdata/
					if err != nil {
						t.Fatal(err)
					}
					if _, err := ParseRestore(strings.NewReader(string(b))); err != nil {
						t.Errorf("%s%s.rules: %v", file, typ, err)
					}
				}
				rs, err := ParseRestore(strings.NewReader(ufwLoad(t, image, ipv6)))
				if err != nil {
					t.Fatal(err)
				}
				u := "ufw" + typ
				f := rs.Table("filter")
				for _, c := range f.Chains {
					for _, r := range c.Rules {
						if r.Opaque() {
							t.Errorf("chain %s line %d opaque: %q", c.Name, r.Line, r.Unsupported)
						}
					}
				}
				if in := f.Chain("INPUT"); in.Policy != "DROP" || len(in.Rules) != 6 {
					t.Errorf("INPUT = %s with %d rules, want DROP with UFW's six jumps", in.Policy, len(in.Rules))
				}
				deny := f.Chain(u + "-logging-deny").Rules
				if len(deny) != 2 || deny[0].Target.Name != "RETURN" || deny[1].Target.Name != "LOG" {
					t.Errorf("%s-logging-deny = %+v, want the inserted RETURN before the LOG", u, deny)
				}
				before := f.Chain(u + "-before-input").Rules
				if last := before[len(before)-1]; last.Target.Name != u+"-user-input" || !last.Target.Chain {
					t.Errorf("%s-before-input ends with %+v, want the jump to %s-user-input", u, last, u)
				}
				first := f.Chain(u + "-user-input").Rules[0]
				if first.Proto.Name != "tcp" || len(first.Matches) != 1 || first.Matches[0].Module != "tcp" {
					t.Errorf("%s-user-input's first rule = %+v, want an implicit tcp match", u, first)
				}
			})
		}
	}
}

// FuzzParseRestore feeds arbitrary input to ParseRestore: it must never
// panic, and a ruleset it accepts holds known tables once each, every jump
// to a chain names a chain of its table, and every other unknown target is
// opaque (spec 1.8, R74).
func FuzzParseRestore(f *testing.F) {
	seeds, err := filepath.Glob(filepath.Join("testdata", "ufw", "*", "*.rules"))
	if err != nil {
		f.Fatal(err)
	}
	for _, name := range seeds {
		b, err := os.ReadFile(name) //nolint:gosec // G304: a fixture under testdata/
		if err != nil {
			f.Fatal(err)
		}
		f.Add(string(b))
	}
	f.Add(rsSample)
	f.Fuzz(func(t *testing.T, in string) {
		rs, err := ParseRestore(strings.NewReader(in))
		if err != nil {
			return
		}
		seen := map[string]bool{}
		for _, tb := range rs.Tables {
			if !slices.Contains(Tables(), tb.Name) || seen[tb.Name] {
				t.Fatalf("table %q unknown or repeated", tb.Name)
			}
			seen[tb.Name] = true
			for _, c := range tb.Chains {
				for _, r := range c.Rules {
					if r.Target.Chain && tb.Chain(r.Target.Name) == nil {
						t.Fatalf("jump to undeclared chain %q", r.Target.Name)
					}
					if _, ok := targets[r.Target.Name]; r.Target.Name != "" && !r.Target.Chain && !ok && !r.Opaque() {
						t.Fatalf("unknown target %q not opaque", r.Target.Name)
					}
				}
			}
		}
	})
}
