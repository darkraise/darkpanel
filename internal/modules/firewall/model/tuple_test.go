package model

import (
	"reflect"
	"strings"
	"testing"
)

// The tuple lines are UFW 0.36's format (backend_iptables.py _write_rules);
// tupleTestSSH is a real line written by ufw 0.36.2 on ubuntu:24.04.
const (
	tupleTestSSH     = "### tuple ### allow tcp 22 0.0.0.0/0 any 0.0.0.0/0 in comment=6461726b70616e656c3a30314a303030303030303030303030303030303030303030303020737368"
	tupleTestSSH6    = "### tuple ### allow tcp 22 ::/0 any ::/0 in comment=6461726b70616e656c3a30314a303030303030303030303030303030303030303030303020737368"
	tupleTestRoute   = "### tuple ### route:allow tcp 80 172.17.0.2 any 0.0.0.0/0 out_eth0"
	tupleTestLimit   = "### tuple ### limit tcp 2222 0.0.0.0/0 any 0.0.0.0/0 in"
	tupleTestLimit6  = "### tuple ### limit tcp 2222 ::/0 any ::/0 in"
	tupleTestLog     = "### tuple ### allow_log tcp 443,8000:8100 0.0.0.0/0 any 10.0.0.0/8 in"
	tupleTestApp     = "### tuple ### allow tcp 80,443 0.0.0.0/0 any 0.0.0.0/0 WWW%20Full - in"
	tupleTestApp6    = "### tuple ### allow tcp 80,443 ::/0 any ::/0 WWW%20Full - in"
	tupleTestFwd     = "### tuple ### route:deny_log-all any any 0.0.0.0/0 any 0.0.0.0/0 in_eth0!out_docker0 comment=68616e64"
	tupleTestFwd6    = "### tuple ### route:deny_log-all any any ::/0 any ::/0 in_eth0!out_docker0 comment=68616e64"
	tupleTestUDP6    = "### tuple ### deny udp 5000:5100 ::/0 any 2001:db8::/32 in"
	tupleTestHostBit = "### tuple ### deny any any ::/0 any 2001:db8::1/32 in"
)

func TestParseTuple(t *testing.T) {
	cases := []struct {
		line string
		v6   bool
		want Tuple
	}{
		{tupleTestSSH, false, Tuple{Action: ActionAllow, Log: LogOff, Proto: ProtoTCP, DPort: "22", Dst: "0.0.0.0/0", SPort: "any",
			Src: "0.0.0.0/0", Direction: DirIn, Comment: "darkpanel:" + ruleTestID + " ssh", Family: FamilyV4, Raw: tupleTestSSH}},
		{tupleTestRoute, false, Tuple{Action: ActionAllow, Log: LogOff, Route: true, Proto: ProtoTCP, DPort: "80", Dst: "172.17.0.2",
			SPort: "any", Src: "0.0.0.0/0", Direction: DirOut, OutIface: "eth0", Family: FamilyV4, Raw: tupleTestRoute}},
		{tupleTestApp6 + " \n", true, Tuple{Action: ActionAllow, Log: LogOff, Proto: ProtoTCP, DPort: "80,443", Dst: "::/0", SPort: "any",
			Src: "::/0", DApp: "WWW Full", Direction: DirIn, Family: FamilyV6, Raw: tupleTestApp6}},
		{tupleTestFwd, false, Tuple{Action: ActionDeny, Log: LogAll, Route: true, Proto: ProtoAny, DPort: "any", Dst: "0.0.0.0/0",
			SPort: "any", Src: "0.0.0.0/0", Direction: DirIn, InIface: "eth0", OutIface: "docker0", Comment: "hand", Family: FamilyV4, Raw: tupleTestFwd}},
		{"### tuple ### reject udp any any 53 any in_br_lan", true, Tuple{Action: ActionReject, Log: LogOff, Proto: ProtoUDP, DPort: "any",
			Dst: "::/0", SPort: "53", Src: "::/0", Direction: DirIn, InIface: "br_lan", Family: FamilyV6,
			Raw: "### tuple ### reject udp any any 53 any in_br_lan"}},
		{"### tuple ### deny tcp 22 10.0.0.0/255.0.255.0 any ::/0", false, Tuple{Action: ActionDeny, Log: LogOff, Proto: ProtoTCP,
			DPort: "22", Dst: "10.0.0.0/255.0.255.0", SPort: "any", Src: "0.0.0.0/0", Direction: DirIn, Family: FamilyV4,
			Raw: "### tuple ### deny tcp 22 10.0.0.0/255.0.255.0 any ::/0"}},
		{"### tuple ### allow tcp 22 0.0.0.0/0 any 0.0.0.0/0 - - out_eth0", false, Tuple{Action: ActionAllow, Log: LogOff,
			Proto: ProtoTCP, DPort: "22", Dst: "0.0.0.0/0", SPort: "any", Src: "0.0.0.0/0", Direction: DirOut, OutIface: "eth0",
			Family: FamilyV4, Raw: "### tuple ### allow tcp 22 0.0.0.0/0 any 0.0.0.0/0 - - out_eth0"}},
	}
	for _, c := range cases {
		got, err := ParseTuple(c.line, c.v6)
		if err != nil {
			t.Errorf("ParseTuple(%q) error: %v", c.line, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("ParseTuple(%q)\n got %+v\nwant %+v", c.line, got, c.want)
		}
	}
}

func TestParseTupleErrors(t *testing.T) {
	cases := []struct {
		line string
		v6   bool
	}{
		{"-A ufw-user-input -p tcp --dport 22 -j ACCEPT", false},
		{"### tuple ### allow tcp 22 0.0.0.0/0 any", false},
		{"### tuple ### allow tcp 22 0.0.0.0/0 any 0.0.0.0/0 a b c in", false},
		{"### tuple ### allow tcp 22 0.0.0.0/0 any 0.0.0.0/0 sideways", false},
		{"### tuple ### allow tcp 22 0.0.0.0/0 any 0.0.0.0/0 on_eth0", false},
		{"### tuple ### allow tcp 22 0.0.0.0/0 any 0.0.0.0/0 in_eth0:1", false},
		{"### tuple ### allow tcp 22 0.0.0.0/0 any 0.0.0.0/0 in_abcdefghijklmnop", false},
		{"### tuple ### accept tcp 22 0.0.0.0/0 any 0.0.0.0/0 in", false},
		{"### tuple ### allow_loud tcp 22 0.0.0.0/0 any 0.0.0.0/0 in", false},
		{"### tuple ### fwd:allow tcp 22 0.0.0.0/0 any 0.0.0.0/0 in", false},
		{"### tuple ### allow icmp any 0.0.0.0/0 any 0.0.0.0/0 in", false},
		{"### tuple ### allow tcp 0 0.0.0.0/0 any 0.0.0.0/0 in", false},
		{"### tuple ### allow tcp 22, 0.0.0.0/0 any 0.0.0.0/0 in", false},
		{"### tuple ### allow tcp 30:20 0.0.0.0/0 any 0.0.0.0/0 in", false},
		{"### tuple ### allow tcp ssh 0.0.0.0/0 any 0.0.0.0/0 in", false},
		{"### tuple ### allow tcp 1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16 0.0.0.0/0 any 0.0.0.0/0 in", false},
		{"### tuple ### allow tcp 22 2001:db8::1 any 0.0.0.0/0 in", false},
		{"### tuple ### allow tcp 22 ::/0 any 10.0.0.1 in", true},
		{"### tuple ### allow tcp 22 10.0.0.300 any 0.0.0.0/0 in", false},
		{"### tuple ### allow tcp 22 0.0.0.0/0 any 0.0.0.0/0 in comment=zz", false},
		{"### tuple ### allow tcp 22 0.0.0.0/0 any 0.0.0.0/0 in comment=ff", false},
		{"### tuple ### allow tcp 22 0.0.0.0/0 any 0.0.0.0/0 in comment=61 comment=62", false},
	}
	for _, c := range cases {
		if got, err := ParseTuple(c.line, c.v6); err == nil {
			t.Errorf("ParseTuple(%q) = %+v, want an error", c.line, got)
		} else if !strings.HasPrefix(err.Error(), "model: ") {
			t.Errorf("ParseTuple(%q) error %q lacks the package prefix", c.line, err)
		}
	}
}

func TestTupleRule(t *testing.T) {
	cases := []struct {
		line string
		v6   bool
		want Rule
		ok   bool
	}{
		{tupleTestSSH, false, Rule{ID: ruleTestID, Action: ActionAllow, Direction: DirIn, Proto: ProtoTCP, From: Any, To: Any,
			ToPort: "22", Family: FamilyV4, Log: LogOff, Comment: "ssh", Origin: OriginDarkpanel}, true},
		{tupleTestRoute, false, Rule{ID: "manual-ef4bffdeac1f78d0", Action: ActionAllow, Direction: DirRoute, Proto: ProtoTCP,
			From: Any, To: "172.17.0.2", ToPort: "80", OutIface: "eth0", Family: FamilyV4, Log: LogOff, Origin: OriginManual}, true},
		{tupleTestUDP6, true, Rule{ID: "manual-10082ef169e6cca9", Action: ActionDeny, Direction: DirIn, Proto: ProtoUDP,
			From: "2001:db8::/32", To: Any, ToPort: "5000:5100", Family: FamilyV6, Log: LogOff, Origin: OriginManual}, true},
		{tupleTestHostBit, true, Rule{ID: "manual-886f40953635d33b", Action: ActionDeny, Direction: DirIn, Family: FamilyV6,
			Log: LogOff, Origin: OriginManual, Raw: tupleTestHostBit}, false},
		{tupleTestApp, false, Rule{ID: "manual-8b3b1140904e6d3a", Action: ActionAllow, Direction: DirIn, Family: FamilyV4,
			Log: LogOff, Origin: OriginManual, Raw: tupleTestApp}, false},
		{"### tuple ### route:allow any any 0.0.0.0/0 any 0.0.0.0/0 out", false, Rule{ID: "manual-3a542fa66efb5ba5",
			Action: ActionAllow, Direction: DirRoute, Family: FamilyV4, Log: LogOff, Origin: OriginManual,
			Raw: "### tuple ### route:allow any any 0.0.0.0/0 any 0.0.0.0/0 out"}, false},
		{"### tuple ### limit tcp 22 0.0.0.0/0 any 0.0.0.0/0 out", false, Rule{ID: "manual-9e40d8ff027bb66d",
			Action: ActionLimit, Direction: DirOut, Family: FamilyV4, Log: LogOff, Origin: OriginManual,
			Raw: "### tuple ### limit tcp 22 0.0.0.0/0 any 0.0.0.0/0 out"}, false},
		{"### tuple ### allow tcp 022 0.0.0.0/0 any 0.0.0.0/0 in comment=6e6f7465", false, Rule{ID: "manual-66ea23bbb0f175bf",
			Action: ActionAllow, Direction: DirIn, Family: FamilyV4, Log: LogOff, Comment: "note", Origin: OriginManual,
			Raw: "### tuple ### allow tcp 022 0.0.0.0/0 any 0.0.0.0/0 in comment=6e6f7465"}, false},
	}
	for _, c := range cases {
		tu, err := ParseTuple(c.line, c.v6)
		if err != nil {
			t.Fatalf("ParseTuple(%q): %v", c.line, err)
		}
		got, ok := tu.Rule()
		if ok != c.ok || !reflect.DeepEqual(got, c.want) {
			t.Errorf("Rule() of %q\n got %+v, %v\nwant %+v, %v", c.line, got, ok, c.want, c.ok)
		}
	}
}

func TestMerge(t *testing.T) {
	parse := func(lines []string, v6 bool) []Tuple {
		var out []Tuple
		for _, l := range lines {
			tu, err := ParseTuple(l, v6)
			if err != nil {
				t.Fatalf("ParseTuple(%q): %v", l, err)
			}
			out = append(out, tu)
		}
		return out
	}
	v4 := parse([]string{tupleTestSSH, tupleTestRoute, tupleTestLimit, tupleTestLog, tupleTestApp, tupleTestFwd}, false)
	v6 := parse([]string{tupleTestSSH6, tupleTestUDP6, tupleTestLimit6, tupleTestApp6, tupleTestFwd6, tupleTestHostBit}, true)
	want := []Rule{
		{ID: ruleTestID, Action: ActionAllow, Direction: DirIn, Proto: ProtoTCP, From: Any, To: Any, ToPort: "22",
			Family: FamilyBoth, Log: LogOff, Comment: "ssh", Origin: OriginDarkpanel},
		{ID: "manual-ef4bffdeac1f78d0", Action: ActionAllow, Direction: DirRoute, Proto: ProtoTCP, From: Any, To: "172.17.0.2",
			ToPort: "80", OutIface: "eth0", Family: FamilyV4, Log: LogOff, Origin: OriginManual},
		{ID: "manual-366f31a1f1903fdf", Action: ActionLimit, Direction: DirIn, Proto: ProtoTCP, From: Any, To: Any, ToPort: "2222",
			Family: FamilyBoth, Log: LogOff, Origin: OriginManual},
		{ID: "manual-fbec77a7f9553c86", Action: ActionAllow, Direction: DirIn, Proto: ProtoTCP, From: "10.0.0.0/8", To: Any,
			ToPort: "443,8000:8100", Family: FamilyV4, Log: LogOn, Origin: OriginManual},
		{ID: "manual-d1b2d8ed16e39ee8", Action: ActionAllow, Direction: DirIn, Family: FamilyBoth, Log: LogOff,
			Origin: OriginManual, Raw: tupleTestApp},
		{ID: "manual-83c8e87200530bb0", Action: ActionDeny, Direction: DirRoute, Proto: ProtoAny, From: Any, To: Any,
			InIface: "eth0", OutIface: "docker0", Family: FamilyBoth, Log: LogAll, Comment: "hand", Origin: OriginManual},
		{ID: "manual-10082ef169e6cca9", Action: ActionDeny, Direction: DirIn, Proto: ProtoUDP, From: "2001:db8::/32", To: Any,
			ToPort: "5000:5100", Family: FamilyV6, Log: LogOff, Origin: OriginManual},
		{ID: "manual-886f40953635d33b", Action: ActionDeny, Direction: DirIn, Family: FamilyV6, Log: LogOff,
			Origin: OriginManual, Raw: tupleTestHostBit},
	}
	got := Merge(v4, v6)
	if len(got) != len(want) {
		t.Fatalf("Merge returned %d rules, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Errorf("rule %d\n got %+v\nwant %+v", i, got[i], want[i])
		}
	}
	if again := Merge(v4, v6); !reflect.DeepEqual(again, got) {
		t.Error("Merge is not stable across calls")
	}
	// Equal tuples whose comments differ stay apart, and a v6 tuple pairs
	// with at most one v4 tuple.
	split := Merge(parse([]string{tupleTestLimit, tupleTestLimit}, false), parse([]string{
		strings.Replace(tupleTestLimit6, " in", " in comment=78", 1), tupleTestLimit6}, true))
	fams := []Family{}
	for _, r := range split {
		fams = append(fams, r.Family)
	}
	if !reflect.DeepEqual(fams, []Family{FamilyBoth, FamilyV4, FamilyV6}) {
		t.Errorf("families = %v, want [both v4 v6]", fams)
	}
}

// FuzzParseTuple checks that no line panics the parser and that every rule
// it calls representable passes the grammar.
func FuzzParseTuple(f *testing.F) {
	for _, l := range []string{tupleTestSSH, tupleTestRoute, tupleTestLog, tupleTestApp, tupleTestFwd, tupleTestUDP6, tupleTestHostBit} {
		f.Add(l, false)
		f.Add(l, true)
	}
	f.Fuzz(func(t *testing.T, line string, v6 bool) {
		tu, err := ParseTuple(line, v6)
		if err != nil {
			return
		}
		r, ok := tu.Rule()
		if ok && len(r.problems(false)) > 0 {
			t.Fatalf("representable rule %+v has problems %v", r, r.problems(false))
		}
		if !ok && r.Raw == "" {
			t.Fatalf("unrepresentable rule %+v has no Raw", r)
		}
	})
}
