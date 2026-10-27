package eval

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

// ayTables wraps rules in a mangle table and a security table whose
// built-in chains accept.
func ayTables(mangle, security string) string {
	return "*mangle\n:PREROUTING ACCEPT [0:0]\n:INPUT ACCEPT [0:0]\n:FORWARD ACCEPT [0:0]\n:OUTPUT ACCEPT [0:0]\n:POSTROUTING ACCEPT [0:0]\n" +
		mangle + "COMMIT\n*security\n:INPUT ACCEPT [0:0]\n:FORWARD ACCEPT [0:0]\n:OUTPUT ACCEPT [0:0]\n" + security + "COMMIT\n"
}

// TestUnmodelled: a rule of the mangle or security table counts when the
// walk cannot model it whatever the packet: a match or target outside what
// it decides, where the rule's target would change the verdict, or a mark
// target with options it does not know. A rule whose undecidable match
// guards a target that decides nothing does not count.
func TestUnmodelled(t *testing.T) {
	cases := []struct {
		name, mangle, security string
		want                   []string
	}{
		{"empty tables", "", "", nil},
		{"tailscale's connmark rules",
			"-A PREROUTING -m conntrack --ctstate RELATED,ESTABLISHED -m connmark ! --mark 0x0/0xff0000 -j CONNMARK --restore-mark --nfmask 0xff0000 --ctmask 0xff0000\n" +
				"-A OUTPUT -m conntrack --ctstate NEW -m mark ! --mark 0x0/0xff0000 -j CONNMARK --save-mark --nfmask 0xff0000 --ctmask 0xff0000\n", "", nil},
		{"a rate match before a target that decides", "-A INPUT -p tcp -m tcp --dport 22 -j ACCEPT\n-A INPUT -m recent --name scan --rcheck -j DROP\n", "",
			[]string{"mangle/INPUT#2: match recent"}},
		{"icmp types before a mark", "-A PREROUTING -p icmp -m icmp --icmp-type 8 -j MARK --set-xmark 0x1/0xff\n", "",
			[]string{"mangle/PREROUTING#1: match icmp"}},
		{"a rate match before a jump", ":mg-user - [0:0]\n-A FORWARD -m limit --limit 5/second -j mg-user\n", "",
			[]string{"mangle/FORWARD#1: match limit"}},
		{"an undecidable match before a target that decides nothing",
			"-A PREROUTING -m limit --limit 1/second -j LOG\n-A PREROUTING -m statistic --mode random --probability 0.5 -j CONNMARK --save-mark\n", "", nil},
		{"a target outside the contract", "-A PREROUTING -p tcp -j TPROXY --on-port 3128\n", "-A INPUT -j SECMARK --selctx system_u:object_r:ssh_server_packet_t:s0\n",
			[]string{"mangle/PREROUTING#1: target TPROXY", "security/INPUT#1: target SECMARK"}},
		{"a mark target with an option it does not know", "-A PREROUTING -j MARK --set-mark 1\n", "",
			[]string{"mangle/PREROUTING#1: target MARK option --set-mark"}},
		{"a mark target with two modes", "-A OUTPUT -j CONNMARK --save-mark --restore-mark\n", "",
			[]string{"mangle/OUTPUT#1: target CONNMARK with these options"}},
		{"an address type the walk does not decide", "-A PREROUTING -m addrtype --dst-type BLACKHOLE -j DROP\n", "",
			[]string{"mangle/PREROUTING#1: match addrtype type BLACKHOLE"}},
		{"a nat target outside the nat table", "-A POSTROUTING -j MASQUERADE\n", "",
			[]string{"mangle/POSTROUTING#1: target MASQUERADE outside the nat table"}},
	}
	for _, c := range cases {
		got := Unmodelled(evalRuleset(t, ayTables(c.mangle, c.security)))
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: Unmodelled = %q, want %q", c.name, got, c.want)
		}
	}
	if got := Unmodelled(nil); got != nil {
		t.Errorf("Unmodelled(nil) = %q, want none", got)
	}
	nat := evalRuleset(t, "*nat\n:PREROUTING ACCEPT [0:0]\n-A PREROUTING -m recent --name x --rcheck -j DNAT --to-destination 192.0.2.9\nCOMMIT\n")
	if got := Unmodelled(nat); got != nil {
		t.Errorf("Unmodelled of a nat table = %q, want none: only mangle and security are listed", got)
	}
}

// TestUnmodelledFixtures: the captures of Docker 28, tailscaled and UFW
// hold nothing the walk cannot model, so none of those hosts becomes
// unsupported.
func TestUnmodelledFixtures(t *testing.T) {
	var files []string
	for _, pattern := range []string{"testdata/docker28/*.save", "testdata/tailscale/*.save", "../testdata/golden/*/*/save/*.txt"} {
		m, err := filepath.Glob(pattern)
		if err != nil || len(m) == 0 {
			t.Fatalf("glob %s = %v, %v", pattern, m, err)
		}
		files = append(files, m...)
	}
	for _, f := range files {
		data, err := os.ReadFile(f) //nolint:gosec // G304: a capture under testdata
		if err != nil {
			t.Fatal(err)
		}
		rs, err := netfilter.Parse(strings.NewReader(string(data)))
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if got := Unmodelled(rs); got != nil {
			t.Errorf("%s: Unmodelled = %q, want none", f, got)
		}
	}
}

// TestUnmodelledAgreesWithEvaluate: a packet that reaches a rule Unmodelled
// lists gets an unknown verdict, and one that reaches a rule it leaves out
// gets a decided one.
func TestUnmodelledAgreesWithEvaluate(t *testing.T) {
	for _, c := range []struct {
		rule, proto string
		listed      bool
	}{
		{"-A INPUT -m recent --name scan --rcheck -j DROP", "tcp", true},
		{"-A INPUT -p icmp -m icmp --icmp-type 8 -j MARK --set-xmark 0x1/0xff", "icmp", true},
		{"-A INPUT -j MARK --set-mark 1", "tcp", true},
		{"-A INPUT -j CONNMARK --save-mark --restore-mark", "tcp", true},
		{"-A INPUT -j MASQUERADE", "tcp", true},
		{"-A INPUT -m addrtype --dst-type BLACKHOLE -j DROP", "tcp", true},
		{"-A INPUT -m addrtype --dst-type LOCAL,BLACKHOLE -j DROP", "tcp", false},
		{"-A INPUT -p tcp -m tcp --tcp-flags SYN,ECE SYN -j DROP", "tcp", true},
		{"-A INPUT -m mark --mark zz -j DROP", "tcp", true},
		{"-A INPUT -m limit --limit 1/second -j LOG", "tcp", false},
		{"-A INPUT -m statistic --mode random --probability 0.5 -j CONNMARK --save-mark", "tcp", false},
	} {
		rs := evalRuleset(t, ayTables(c.rule+"\n", ""))
		p := evalPacket()
		p.Proto = c.proto
		res := Evaluate(rs, p, nil)
		if listed := len(Unmodelled(rs)) > 0; listed != c.listed || (res.Verdict == fwstate.Unknown) != c.listed {
			t.Errorf("%s: listed %v, verdict %s (%s); want listed %v and a verdict that is unknown only when listed", c.rule, listed, res.Verdict, res.Reason, c.listed)
		}
	}
}
