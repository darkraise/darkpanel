package model

import (
	"slices"
	"strings"
	"testing"
)

// argsTestRules covers the grammar of spec 2.2; every rule is valid and
// normalized, and each was run through real UFW (see TestArgsRoundTrip).
var argsTestRules = []struct {
	rule   Rule
	add    string // AddArgs joined by "|"
	delete string
	insert string // InsertArgs(2, rule)
}{
	{Rule{ID: "01J00000000000000000000001", Action: ActionAllow, Direction: DirIn, Proto: ProtoTCP, From: Any, To: Any,
		ToPort: "22", Family: FamilyBoth, Log: LogOff, Comment: "ssh", Origin: OriginDarkpanel},
		"allow|in|proto|tcp|from|any|to|any|port|22|comment|darkpanel:01J00000000000000000000001 ssh",
		"delete|allow|in|proto|tcp|from|any|to|any|port|22",
		"insert|2|allow|in|proto|tcp|from|any|to|any|port|22|comment|darkpanel:01J00000000000000000000001 ssh"},
	{Rule{ID: "01J00000000000000000000002", Action: ActionDeny, Direction: DirIn, Proto: ProtoUDP, From: "2001:db8::/32",
		To: Any, ToPort: "5000:5100", Family: FamilyV6, Log: LogOff, Origin: OriginDarkpanel},
		"deny|in|proto|udp|from|2001:db8::/32|to|::/0|port|5000:5100|comment|darkpanel:01J00000000000000000000002",
		"delete|deny|in|proto|udp|from|2001:db8::/32|to|::/0|port|5000:5100",
		"insert|2|deny|in|proto|udp|from|2001:db8::/32|to|::/0|port|5000:5100|comment|darkpanel:01J00000000000000000000002"},
	{Rule{ID: "01J00000000000000000000003", Action: ActionAllow, Direction: DirRoute, Proto: ProtoTCP, From: Any,
		To: "172.17.0.2", ToPort: "80", OutIface: "docker0", Family: FamilyV4, Log: LogOff, Origin: OriginDarkpanel},
		"route|allow|out|on|docker0|proto|tcp|from|0.0.0.0/0|to|172.17.0.2|port|80|comment|darkpanel:01J00000000000000000000003",
		"route|delete|allow|out|on|docker0|proto|tcp|from|0.0.0.0/0|to|172.17.0.2|port|80",
		"route|insert|2|allow|out|on|docker0|proto|tcp|from|0.0.0.0/0|to|172.17.0.2|port|80|comment|darkpanel:01J00000000000000000000003"},
	{Rule{ID: "01J00000000000000000000004", Action: ActionLimit, Direction: DirIn, Proto: ProtoTCP, From: Any, To: Any,
		ToPort: "2222", Family: FamilyBoth, Log: LogOn, Comment: "ssh alt", Origin: OriginDarkpanel},
		"limit|in|log|proto|tcp|from|any|to|any|port|2222|comment|darkpanel:01J00000000000000000000004 ssh alt",
		"delete|limit|in|log|proto|tcp|from|any|to|any|port|2222",
		"insert|2|limit|in|log|proto|tcp|from|any|to|any|port|2222|comment|darkpanel:01J00000000000000000000004 ssh alt"},
	{Rule{ID: "01J00000000000000000000005", Action: ActionReject, Direction: DirIn, Proto: ProtoAny, From: "10.0.0.0/8",
		To: Any, ToPort: "3000", Family: FamilyV4, Log: LogOff, Origin: OriginDarkpanel},
		"reject|in|from|10.0.0.0/8|to|0.0.0.0/0|port|3000|comment|darkpanel:01J00000000000000000000005",
		"delete|reject|in|from|10.0.0.0/8|to|0.0.0.0/0|port|3000",
		"insert|2|reject|in|from|10.0.0.0/8|to|0.0.0.0/0|port|3000|comment|darkpanel:01J00000000000000000000005"},
	{Rule{ID: "01J00000000000000000000006", Action: ActionAllow, Direction: DirIn, Proto: ProtoTCP, From: "10.0.0.0/8",
		FromPort: "1024:2048", To: "192.168.1.10", ToPort: "443,8000:8100", Family: FamilyV4, Log: LogAll, Origin: OriginDarkpanel},
		"allow|in|log-all|proto|tcp|from|10.0.0.0/8|port|1024:2048|to|192.168.1.10|port|443,8000:8100|comment|darkpanel:01J00000000000000000000006",
		"delete|allow|in|log-all|proto|tcp|from|10.0.0.0/8|port|1024:2048|to|192.168.1.10|port|443,8000:8100",
		"insert|2|allow|in|log-all|proto|tcp|from|10.0.0.0/8|port|1024:2048|to|192.168.1.10|port|443,8000:8100|comment|darkpanel:01J00000000000000000000006"},
	{Rule{ID: "01J00000000000000000000007", Action: ActionAllow, Direction: DirOut, Proto: ProtoUDP, From: Any, To: Any,
		ToPort: "53", OutIface: "eth0", Family: FamilyV6, Log: LogOff, Comment: "dns", Origin: OriginDarkpanel},
		"allow|out|on|eth0|proto|udp|from|::/0|to|::/0|port|53|comment|darkpanel:01J00000000000000000000007 dns",
		"delete|allow|out|on|eth0|proto|udp|from|::/0|to|::/0|port|53",
		"insert|2|allow|out|on|eth0|proto|udp|from|::/0|to|::/0|port|53|comment|darkpanel:01J00000000000000000000007 dns"},
	{Rule{ID: "01J00000000000000000000008", Action: ActionAllow, Direction: DirIn, Proto: ProtoESP, From: Any, To: Any,
		InIface: "eth0", Family: FamilyBoth, Log: LogOff, Origin: OriginDarkpanel},
		"allow|in|on|eth0|proto|esp|from|any|to|any|comment|darkpanel:01J00000000000000000000008",
		"delete|allow|in|on|eth0|proto|esp|from|any|to|any",
		"insert|2|allow|in|on|eth0|proto|esp|from|any|to|any|comment|darkpanel:01J00000000000000000000008"},
	{Rule{ID: "01J00000000000000000000009", Action: ActionDeny, Direction: DirRoute, Proto: ProtoTCP, From: Any,
		To: "172.17.0.0/16", ToPort: "80", InIface: "eth0", OutIface: "docker0", Family: FamilyV4, Log: LogOn,
		Comment: "web", Origin: OriginDarkpanel},
		"route|deny|in|on|eth0|out|on|docker0|log|proto|tcp|from|0.0.0.0/0|to|172.17.0.0/16|port|80|comment|darkpanel:01J00000000000000000000009 web",
		"route|delete|deny|in|on|eth0|out|on|docker0|log|proto|tcp|from|0.0.0.0/0|to|172.17.0.0/16|port|80",
		"route|insert|2|deny|in|on|eth0|out|on|docker0|log|proto|tcp|from|0.0.0.0/0|to|172.17.0.0/16|port|80|comment|darkpanel:01J00000000000000000000009 web"},
	{Rule{ID: "01J0000000000000000000000A", Action: ActionAllow, Direction: DirIn, Proto: ProtoIPv6, From: Any, To: Any,
		Family: FamilyV4, Log: LogOff, Origin: OriginDarkpanel},
		"allow|in|proto|ipv6|from|0.0.0.0/0|to|0.0.0.0/0|comment|darkpanel:01J0000000000000000000000A",
		"delete|allow|in|proto|ipv6|from|0.0.0.0/0|to|0.0.0.0/0",
		"insert|2|allow|in|proto|ipv6|from|0.0.0.0/0|to|0.0.0.0/0|comment|darkpanel:01J0000000000000000000000A"},
	{Rule{ID: "01J0000000000000000000000B", Action: ActionDeny, Direction: DirIn, Proto: ProtoIGMP, From: "192.168.0.0/16",
		To: Any, Family: FamilyV4, Log: LogOff, Origin: OriginDarkpanel},
		"deny|in|proto|igmp|from|192.168.0.0/16|to|0.0.0.0/0|comment|darkpanel:01J0000000000000000000000B",
		"delete|deny|in|proto|igmp|from|192.168.0.0/16|to|0.0.0.0/0",
		"insert|2|deny|in|proto|igmp|from|192.168.0.0/16|to|0.0.0.0/0|comment|darkpanel:01J0000000000000000000000B"},
	{Rule{ID: "01J0000000000000000000000C", Action: ActionAllow, Direction: DirRoute, Proto: ProtoGRE, From: Any, To: Any,
		Family: FamilyBoth, Log: LogOff, Origin: OriginDarkpanel},
		"route|allow|proto|gre|from|any|to|any|comment|darkpanel:01J0000000000000000000000C",
		"route|delete|allow|proto|gre|from|any|to|any",
		"route|insert|2|allow|proto|gre|from|any|to|any|comment|darkpanel:01J0000000000000000000000C"},
	{Rule{ID: "01J0000000000000000000000D", Action: ActionDeny, Direction: DirOut, Proto: ProtoAny, From: Any,
		To: "2001:db8::1", Family: FamilyV6, Log: LogOff, Origin: OriginDarkpanel},
		"deny|out|from|::/0|to|2001:db8::1|comment|darkpanel:01J0000000000000000000000D",
		"delete|deny|out|from|::/0|to|2001:db8::1",
		"insert|2|deny|out|from|::/0|to|2001:db8::1|comment|darkpanel:01J0000000000000000000000D"},
	{Rule{ID: "01J0000000000000000000000E", Action: ActionAllow, Direction: DirIn, Proto: ProtoAH, From: "2001:db8::/48",
		To: Any, Family: FamilyV6, Log: LogOff, Origin: OriginDarkpanel},
		"allow|in|proto|ah|from|2001:db8::/48|to|::/0|comment|darkpanel:01J0000000000000000000000E",
		"delete|allow|in|proto|ah|from|2001:db8::/48|to|::/0",
		"insert|2|allow|in|proto|ah|from|2001:db8::/48|to|::/0|comment|darkpanel:01J0000000000000000000000E"},
	{Rule{Action: ActionAllow, Direction: DirIn, Proto: ProtoTCP, From: Any, To: Any, ToPort: "8080", Family: FamilyBoth,
		Log: LogOff, Comment: "hand rule", Origin: OriginManual},
		"allow|in|proto|tcp|from|any|to|any|port|8080|comment|hand rule",
		"delete|allow|in|proto|tcp|from|any|to|any|port|8080",
		"insert|2|allow|in|proto|tcp|from|any|to|any|port|8080|comment|hand rule"},
	{Rule{Action: ActionDeny, Direction: DirIn, Proto: ProtoAny, From: "192.168.0.0/16", To: Any, Family: FamilyV4,
		Log: LogOff, Origin: OriginManual},
		"deny|in|from|192.168.0.0/16|to|0.0.0.0/0",
		"delete|deny|in|from|192.168.0.0/16|to|0.0.0.0/0",
		"insert|2|deny|in|from|192.168.0.0/16|to|0.0.0.0/0"},
}

func TestArgs(t *testing.T) {
	for _, c := range argsTestRules {
		if err := c.rule.Validate(); err != nil {
			t.Fatalf("test rule %+v is invalid: %v", c.rule, err)
		}
		if got := strings.Join(AddArgs(c.rule), "|"); got != c.add {
			t.Errorf("AddArgs(%+v)\n got %s\nwant %s", c.rule, got, c.add)
		}
		if got := strings.Join(DeleteArgs(c.rule), "|"); got != c.delete {
			t.Errorf("DeleteArgs(%+v)\n got %s\nwant %s", c.rule, got, c.delete)
		}
		if got := strings.Join(InsertArgs(2, c.rule), "|"); got != c.insert {
			t.Errorf("InsertArgs(2, %+v)\n got %s\nwant %s", c.rule, got, c.insert)
		}
	}
}

func TestArgsNoOptionLikeValue(t *testing.T) {
	for _, c := range argsTestRules {
		for _, args := range [][]string{AddArgs(c.rule), DeleteArgs(c.rule), InsertArgs(1, c.rule)} {
			if i := slices.IndexFunc(args, func(a string) bool { return strings.HasPrefix(a, "-") || a == "" }); i >= 0 {
				t.Errorf("argument %d of %q is empty or starts with a dash", i, args)
			}
		}
	}
}

// argsTestUserRules and argsTestUser6Rules are the tuple lines ufw 0.36.2
// (ubuntu:24.04, iptables 1.8.10 nf_tables) wrote after running AddArgs of
// every rule of argsTestRules in order on an enabled firewall.
var argsTestUserRules = []string{
	"### tuple ### allow tcp 22 0.0.0.0/0 any 0.0.0.0/0 in comment=6461726b70616e656c3a30314a303030303030303030303030303030303030303030303120737368",
	"### tuple ### route:allow tcp 80 172.17.0.2 any 0.0.0.0/0 out_docker0 comment=6461726b70616e656c3a30314a3030303030303030303030303030303030303030303033",
	"### tuple ### limit_log tcp 2222 0.0.0.0/0 any 0.0.0.0/0 in comment=6461726b70616e656c3a30314a30303030303030303030303030303030303030303030342073736820616c74",
	"### tuple ### reject any 3000 0.0.0.0/0 any 10.0.0.0/8 in comment=6461726b70616e656c3a30314a3030303030303030303030303030303030303030303035",
	"### tuple ### allow_log-all tcp 443,8000:8100 192.168.1.10 1024:2048 10.0.0.0/8 in comment=6461726b70616e656c3a30314a3030303030303030303030303030303030303030303036",
	"### tuple ### allow esp any 0.0.0.0/0 any 0.0.0.0/0 in_eth0 comment=6461726b70616e656c3a30314a3030303030303030303030303030303030303030303038",
	"### tuple ### route:deny_log tcp 80 172.17.0.0/16 any 0.0.0.0/0 in_eth0!out_docker0 comment=6461726b70616e656c3a30314a303030303030303030303030303030303030303030303920776562",
	"### tuple ### allow ipv6 any 0.0.0.0/0 any 0.0.0.0/0 in comment=6461726b70616e656c3a30314a3030303030303030303030303030303030303030303041",
	"### tuple ### deny igmp any 0.0.0.0/0 any 192.168.0.0/16 in comment=6461726b70616e656c3a30314a3030303030303030303030303030303030303030303042",
	"### tuple ### route:allow gre any 0.0.0.0/0 any 0.0.0.0/0 in comment=6461726b70616e656c3a30314a3030303030303030303030303030303030303030303043",
	"### tuple ### allow tcp 8080 0.0.0.0/0 any 0.0.0.0/0 in comment=68616e642072756c65",
	"### tuple ### deny any any 0.0.0.0/0 any 192.168.0.0/16 in",
}

var argsTestUser6Rules = []string{
	"### tuple ### allow tcp 22 ::/0 any ::/0 in comment=6461726b70616e656c3a30314a303030303030303030303030303030303030303030303120737368",
	"### tuple ### deny udp 5000:5100 ::/0 any 2001:db8::/32 in comment=6461726b70616e656c3a30314a3030303030303030303030303030303030303030303032",
	"### tuple ### limit_log tcp 2222 ::/0 any ::/0 in comment=6461726b70616e656c3a30314a30303030303030303030303030303030303030303030342073736820616c74",
	"### tuple ### allow udp 53 ::/0 any ::/0 out_eth0 comment=6461726b70616e656c3a30314a303030303030303030303030303030303030303030303720646e73",
	"### tuple ### allow esp any ::/0 any ::/0 in_eth0 comment=6461726b70616e656c3a30314a3030303030303030303030303030303030303030303038",
	"### tuple ### route:allow gre any ::/0 any ::/0 in comment=6461726b70616e656c3a30314a3030303030303030303030303030303030303030303043",
	"### tuple ### deny any any 2001:db8::1 any ::/0 out comment=6461726b70616e656c3a30314a3030303030303030303030303030303030303030303044",
	"### tuple ### allow ah any ::/0 any 2001:db8::/48 in comment=6461726b70616e656c3a30314a3030303030303030303030303030303030303030303045",
	"### tuple ### allow tcp 8080 ::/0 any ::/0 in comment=68616e642072756c65",
}

// TestArgsRoundTrip checks that the tuples ufw wrote for AddArgs parse back
// to the same rules: the v4 and both-family rules in order, then the
// v6-only ones. A manual rule's id is derived from its tuple.
func TestArgsRoundTrip(t *testing.T) {
	var v4, v6 []Tuple
	for _, l := range argsTestUserRules {
		tu, err := ParseTuple(l, false)
		if err != nil {
			t.Fatal(err)
		}
		v4 = append(v4, tu)
	}
	for _, l := range argsTestUser6Rules {
		tu, err := ParseTuple(l, true)
		if err != nil {
			t.Fatal(err)
		}
		v6 = append(v6, tu)
	}
	var want []Rule
	for _, only6 := range []bool{false, true} {
		for _, c := range argsTestRules {
			if (c.rule.Family == FamilyV6) == only6 {
				want = append(want, c.rule)
			}
		}
	}
	got := Merge(v4, v6)
	if len(got) != len(want) {
		t.Fatalf("Merge returned %d rules, want %d", len(got), len(want))
	}
	for i, w := range want {
		if w.Origin == OriginManual {
			if !strings.HasPrefix(got[i].ID, "manual-") {
				t.Errorf("rule %d id = %q, want a manual id", i, got[i].ID)
			}
			w.ID = got[i].ID
		}
		if got[i] != w {
			t.Errorf("rule %d\n got %+v\nwant %+v", i, got[i], w)
		}
	}
}
