package predict

import (
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/modules/firewall/model"
)

// predictShellSplit splits a commands.txt line as the shell does for the
// quoting the golden scenarios use: words separated by spaces, single
// quotes grouping.
func predictShellSplit(line string) []string {
	var out []string
	var b strings.Builder
	inWord, quoted := false, false
	for _, r := range line {
		switch {
		case r == '\'':
			quoted, inWord = !quoted, true
		case r == ' ' && !quoted:
			if inWord {
				out = append(out, b.String())
				b.Reset()
			}
			inWord = false
		default:
			b.WriteRune(r)
			inWord = true
		}
	}
	if inWord {
		out = append(out, b.String())
	}
	return out
}

// vectorTestRule is a model rule whose argument vectors the tests parse.
var vectorTestRule = model.Rule{ID: "01J00000000000000000000000", Action: model.ActionDeny, Direction: model.DirRoute,
	Proto: model.ProtoUDP, From: "10.0.0.0/8", FromPort: "53", To: model.Any, ToPort: "1000:2000,80",
	InIface: "eth0", OutIface: "docker0", Family: model.FamilyV4, Log: model.LogAll, Comment: "dns"}

// TestParseVectorRules checks the tuple lines a vector gives in each
// family against the lines ufw wrote for the same command (the golden
// "rules" scenario and the captures of TestFilesRuleLines), and against
// the model's own vectors.
func TestParseVectorRules(t *testing.T) {
	for _, tc := range []struct {
		args     []string
		remove   bool
		position int
		want4    string
		want6    string
	}{
		{args: predictShellSplit("allow in proto tcp to any port 22 comment 'darkpanel:01J00000000000000000000000 ssh'"),
			want4: "### tuple ### allow tcp 22 0.0.0.0/0 any 0.0.0.0/0 in comment=6461726b70616e656c3a30314a303030303030303030303030303030303030303030303020737368",
			want6: "### tuple ### allow tcp 22 ::/0 any ::/0 in comment=6461726b70616e656c3a30314a303030303030303030303030303030303030303030303020737368"},
		{args: predictShellSplit("deny in proto udp from 2001:db8::/32 to any port 5000:5100"),
			want6: "### tuple ### deny udp 5000:5100 ::/0 any 2001:db8::/32 in"},
		{args: predictShellSplit("route allow out on eth0 proto tcp to 172.17.0.2 port 80"),
			want4: "### tuple ### route:allow tcp 80 172.17.0.2 any 0.0.0.0/0 out_eth0"},
		{args: predictShellSplit("limit in proto tcp to any port 2222"),
			want4: "### tuple ### limit tcp 2222 0.0.0.0/0 any 0.0.0.0/0 in", want6: "### tuple ### limit tcp 2222 ::/0 any ::/0 in"},
		{args: predictShellSplit("reject in to any port 3000"),
			want4: "### tuple ### reject any 3000 0.0.0.0/0 any 0.0.0.0/0 in", want6: "### tuple ### reject any 3000 ::/0 any ::/0 in"},
		{args: predictShellSplit("allow in log proto tcp from 10.0.0.0/8 to any port 443,8000:8100"),
			want4: "### tuple ### allow_log tcp 443,8000:8100 0.0.0.0/0 any 10.0.0.0/8 in"},
		{args: predictShellSplit("allow out on eth0 to any port 53"),
			want4: "### tuple ### allow any 53 0.0.0.0/0 any 0.0.0.0/0 out_eth0", want6: "### tuple ### allow any 53 ::/0 any ::/0 out_eth0"},
		{args: predictShellSplit("allow in on eth0 proto esp"),
			want4: "### tuple ### allow esp any 0.0.0.0/0 any 0.0.0.0/0 in_eth0", want6: "### tuple ### allow esp any ::/0 any ::/0 in_eth0"},
		{args: predictShellSplit("allow proto tcp from 10.0.0.0/8 port 1000:2000 to any port 80,443"),
			want4: "### tuple ### allow tcp 80,443 0.0.0.0/0 1000:2000 10.0.0.0/8 in"},
		{args: predictShellSplit("deny out proto udp from any port 53"),
			want4: "### tuple ### deny udp any 0.0.0.0/0 53 0.0.0.0/0 out", want6: "### tuple ### deny udp any ::/0 53 ::/0 out"},
		{args: predictShellSplit("reject in log-all to 192.0.2.1 port 25"),
			want4: "### tuple ### reject_log-all any 25 192.0.2.1 any 0.0.0.0/0 in"},
		{args: predictShellSplit("limit in log proto tcp to any port 22"),
			want4: "### tuple ### limit_log tcp 22 0.0.0.0/0 any 0.0.0.0/0 in", want6: "### tuple ### limit_log tcp 22 ::/0 any ::/0 in"},
		{args: predictShellSplit("route deny in on eth0 out on eth1"),
			want4: "### tuple ### route:deny any any 0.0.0.0/0 any 0.0.0.0/0 in_eth0!out_eth1", want6: "### tuple ### route:deny any any ::/0 any ::/0 in_eth0!out_eth1"},
		{args: predictShellSplit("allow in on eth0 log-all from 2001:db8::/32 to any port 8080:8090 proto udp"),
			want6: "### tuple ### allow_log-all udp 8080:8090 ::/0 any 2001:db8::/32 in_eth0"},
		{args: predictShellSplit("deny from 2001:db8::1/32"),
			want6: "### tuple ### deny any any ::/0 any 2001:db8::1/32 in"},
		{args: model.AddArgs(vectorTestRule),
			want4: "### tuple ### route:deny_log-all udp 80,1000:2000 0.0.0.0/0 53 10.0.0.0/8 in_eth0!out_docker0 comment=6461726b70616e656c3a30314a303030303030303030303030303030303030303030303020646e73"},
		{args: model.DeleteArgs(vectorTestRule), remove: true,
			want4: "### tuple ### route:deny_log-all udp 80,1000:2000 0.0.0.0/0 53 10.0.0.0/8 in_eth0!out_docker0"},
		{args: model.InsertArgs(3, vectorTestRule), position: 3,
			want4: "### tuple ### route:deny_log-all udp 80,1000:2000 0.0.0.0/0 53 10.0.0.0/8 in_eth0!out_docker0 comment=6461726b70616e656c3a30314a303030303030303030303030303030303030303030303020646e73"},
		{args: []string{"allow", "in", "proto", "tcp", "from", "10.1.2.3/8", "to", "192.0.2.7/32", "port", "22"},
			want4: "### tuple ### allow tcp 22 192.0.2.7 any 10.0.0.0/8 in"},
	} {
		v, err := parseVector(tc.args)
		if err != nil {
			t.Errorf("%q: %v", tc.args, err)
			continue
		}
		if v.kind != vecRule || v.remove != tc.remove || v.position != tc.position {
			t.Errorf("%q: kind %s remove %v position %d", tc.args, v.kind, v.remove, v.position)
		}
		for _, f := range []struct {
			fam  model.Family
			want string
		}{{model.FamilyV4, tc.want4}, {model.FamilyV6, tc.want6}} {
			in := v.iptype == model.FamilyBoth || v.iptype == f.fam
			if in != (f.want != "") {
				t.Errorf("%q: iptype %s, want %s line %q", tc.args, v.iptype, f.fam, f.want)
				continue
			}
			if !in {
				continue
			}
			got, err := forFamily(v.rule, f.fam)
			if err != nil {
				t.Errorf("%q %s: %v", tc.args, f.fam, err)
				continue
			}
			if got.Raw != f.want {
				t.Errorf("%q %s:\n got %s\nwant %s", tc.args, f.fam, got.Raw, f.want)
			}
			parsed, err := model.ParseTuple(got.Raw, f.fam == model.FamilyV6)
			if err != nil || parsed != got {
				t.Errorf("%q %s: tuple %+v does not read back as %+v (%v)", tc.args, f.fam, got, parsed, err)
			}
		}
	}
}

func TestParseVectorCommands(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want vector
	}{
		{[]string{"--force", "enable"}, vector{kind: vecEnable}},
		{[]string{"disable"}, vector{kind: vecDisable}},
		{[]string{"reload"}, vector{kind: vecReload}},
		{[]string{"default", "allow", "outgoing"}, vector{kind: vecDefault, policy: "allow", chain: "OUTPUT"}},
		{[]string{"default", "reject", "incoming"}, vector{kind: vecDefault, policy: "reject", chain: "INPUT"}},
		{[]string{"default", "deny", "routed"}, vector{kind: vecDefault, policy: "deny", chain: "FORWARD"}},
		{[]string{"logging", "on"}, vector{kind: vecLogging, level: "on"}},
		{[]string{"logging", "high"}, vector{kind: vecLogging, level: "high"}},
	} {
		got, err := parseVector(tc.args)
		if err != nil || got != tc.want {
			t.Errorf("%q = %+v, %v; want %+v", tc.args, got, err, tc.want)
		}
	}
}

func TestParseVectorRefuses(t *testing.T) {
	for _, line := range []string{
		"",
		"enable",
		"status",
		"allow 22/tcp",
		"allow 'DP Web'",
		"allow in proto tcp to any app 'DP Web'",
		"allow in proto tcp to any port ssh",
		"prepend allow in proto tcp to any port 22",
		"delete 3",
		"insert 0 allow in proto tcp to any port 22",
		"insert x allow in proto tcp to any port 22",
		"default allow input",
		"default permit incoming",
		"logging loud",
		"allow log in proto tcp to any port 22",
		"route allow in proto tcp to any port 80",
		"allow in proto tcp from 10.0.0.1 to 2001:db8::1 port 22",
		"allow in proto ipv6 from 2001:db8::1",
		"allow in proto esp to any port 22",
		"allow in to any port 80,443",
		"allow in proto vrrp from any",
		"allow in from 10.0.0.0/255.0.0.0",
		"allow in",
		"allow in on eth0!x proto esp",
		"route allow in on 00 out on %0",
		"allow in on eth0.verylongname proto esp",
		"allow in proto tcp to any to 10.0.0.1",
		"allow in proto tcp to any port 0",
		"allow in proto tcp to any port 22:22",
		"allow in proto tcp port 22",
		"allow in proto tcp to fe80::1%eth0",
		"allow in on eth0 out on eth1 proto esp",
		"allow in proto tcp to any port 22 extra",
	} {
		vectorTestRefused(t, predictShellSplit(line))
	}
	vectorTestRefused(t, []string{"allow", "in", "proto", "tcp", "to", "any", "port", "22", "comment", "it's"})
	vectorTestRefused(t, []string{"deny", "to", "any", "comment", "\xe5"})
}

func vectorTestRefused(t *testing.T, args []string) {
	t.Helper()
	if v, err := parseVector(args); err == nil {
		t.Errorf("%q parsed as %+v, want a refusal", args, v)
	} else if !strings.HasPrefix(err.Error(), "predict: ufw ") {
		t.Errorf("%q: error %q lacks the prefix", args, err)
	}
}

func TestNormalize(t *testing.T) {
	for _, tc := range []struct {
		in   string
		v6   bool
		want string
	}{
		{"10.0.0.1/8", false, "10.0.0.0/8"},
		{"10.0.0.1/32", false, "10.0.0.1"},
		{"10.1.2.3/255.255.0.0", false, "10.1.0.0/16"},
		{"10.1.2.3/255.0.255.0", false, "10.0.2.0/255.0.255.0"},
		{"0.0.0.0/0", false, "0.0.0.0/0"},
		{"2001:db8::1/32", true, "2001:db8::1/32"},
		{"2001:0db8::1/128", true, "2001:db8::1"},
		{"::/0", true, "::/0"},
	} {
		if got, err := normalizeAddr(tc.in, tc.v6); err != nil || got != tc.want {
			t.Errorf("normalizeAddr(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
		}
	}
	if _, err := normalizeAddr("10.0.0.1", true); err == nil {
		t.Error("an IPv4 address normalized as IPv6")
	}
	if got := sortPorts("8000:8100,443,22:30,22,1000:1100,1000:1050"); got != "22,22:30,443,1000:1050,1000:1100,8000:8100" {
		t.Errorf("sortPorts = %q", got)
	}
}
