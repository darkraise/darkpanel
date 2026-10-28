package dockerfw

import (
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

const dtfNat = `*nat
:PREROUTING ACCEPT [0:0]
:INPUT ACCEPT [0:0]
:OUTPUT ACCEPT [0:0]
:POSTROUTING ACCEPT [0:0]
:DOCKER - [0:0]
:lan-fwd - [0:0]
:loopy - [0:0]
:stale - [0:0]
-A PREROUTING -m addrtype --dst-type LOCAL -j DOCKER
-A PREROUTING -d 192.0.2.10/32 -i eth0 -p tcp -m tcp --dport 80 -j DNAT --to-destination 10.9.0.5:8080
-A PREROUTING -p tcp -m tcp --dport 8000:8100 -j DNAT --to-destination 10.9.0.6
-A PREROUTING -d 192.0.2.11/32 -p udp -m udp --dport 53 -j DNAT --to-destination 10.9.0.7:5353 --persistent
-A PREROUTING -p tcp -m tcp --dport 22 -j REDIRECT --to-ports 2222
-A PREROUTING -j lan-fwd
-A PREROUTING -j loopy
-A PREROUTING -p udp -j lan-fwd
-A PREROUTING ! -i eth1 -d 192.0.2.12/32 -p tcp -m multiport --dports 80,443 -j DNAT --to-destination 10.9.0.8
-A OUTPUT -d 192.0.2.50/32 -p tcp -m tcp --dport 99 -j DNAT --to-destination 10.9.0.50:99
-A DOCKER ! -i docker0 -p tcp -m tcp --dport 8080 -j DNAT --to-destination 172.17.0.2:80
-A lan-fwd -d 192.0.2.13/32 -p tcp -m tcp --dport 25 -m comment --comment "mail" -j DNAT --to-destination 10.9.0.9:25
-A loopy -j loopy
-A stale -d 192.0.2.14/32 -p tcp -m tcp --dport 26 -j DNAT --to-destination 10.9.0.10:26
COMMIT
`

// dtfTable parses save text and returns the named table.
func dtfTable(t *testing.T, save, name string) *netfilter.Table {
	t.Helper()
	rs, err := netfilter.Parse(strings.NewReader(save))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	tb := rs.Table(name)
	if tb == nil {
		t.Fatalf("no table %s in %q", name, save)
	}
	return tb
}

func TestDtfForeignDNAT(t *testing.T) {
	got := ForeignDNAT(dtfTable(t, dtfNat, "nat"))
	var texts []string
	for _, r := range got {
		texts = append(texts, r.Text)
	}
	want := []string{
		"-A PREROUTING -d 192.0.2.10/32 -i eth0 -p tcp -m tcp --dport 80 -j DNAT --to-destination 10.9.0.5:8080",
		"-A PREROUTING -p tcp -m tcp --dport 8000:8100 -j DNAT --to-destination 10.9.0.6",
		"-A PREROUTING -d 192.0.2.11/32 -p udp -m udp --dport 53 -j DNAT --to-destination 10.9.0.7:5353 --persistent",
		`-A lan-fwd -d 192.0.2.13/32 -p tcp -m tcp --dport 25 -m comment --comment "mail" -j DNAT --to-destination 10.9.0.9:25`,
		"-A PREROUTING ! -i eth1 -d 192.0.2.12/32 -p tcp -m multiport --dports 80,443 -j DNAT --to-destination 10.9.0.8",
	}
	if !slices.Equal(texts, want) {
		t.Errorf("ForeignDNAT =\n%s\nwant\n%s", strings.Join(texts, "\n"), strings.Join(want, "\n"))
	}
	t.Run("no nat table", func(t *testing.T) {
		if got := ForeignDNAT(nil); len(got) != 0 {
			t.Errorf("ForeignDNAT(nil) = %v", got)
		}
	})
	t.Run("no PREROUTING chain", func(t *testing.T) {
		tb := dtfTable(t, "*nat\n:OUTPUT ACCEPT [0:0]\n-A OUTPUT -p tcp -j DNAT --to-destination 10.9.0.1\nCOMMIT\n", "nat")
		if got := ForeignDNAT(tb); len(got) != 0 {
			t.Errorf("ForeignDNAT = %v", got)
		}
	})
	t.Run("Docker's prefixed chains are Docker's", func(t *testing.T) {
		tb := dtfTable(t, "*nat\n:PREROUTING ACCEPT [0:0]\n:DOCKER-INGRESS - [0:0]\n-A PREROUTING -j DOCKER-INGRESS\n"+
			"-A DOCKER-INGRESS -p tcp -m tcp --dport 80 -j DNAT --to-destination 172.18.0.2:80\nCOMMIT\n", "nat")
		if got := ForeignDNAT(tb); len(got) != 0 {
			t.Errorf("ForeignDNAT = %v", got)
		}
	})
}

// dtfRule parses one rule line of nat PREROUTING.
func dtfRule(t *testing.T, line string) netfilter.Rule {
	t.Helper()
	tb := dtfTable(t, "*nat\n:PREROUTING ACCEPT [0:0]\n-A PREROUTING "+line+"\nCOMMIT\n", "nat")
	return tb.Chain("PREROUTING").Rules[0]
}

func dtfExemption(inIface, origDst string, origPort uint16, dst, proto string, port uint16) Exemption {
	return Exemption{InInterface: inIface, OrigDestination: netip.MustParseAddr(origDst), OrigPort: origPort,
		Destination: netip.MustParseAddr(dst), Proto: proto, Port: port}
}

func TestDtfPropose(t *testing.T) {
	tests := []struct {
		name string
		fam  fwstate.Family
		rule string
		want Exemption
		ok   bool
	}{
		{"a port forward", fwstate.V4,
			"-d 192.0.2.10/32 -i eth0 -p tcp -m tcp --dport 80 -j DNAT --to-destination 10.9.0.5:8080",
			dtfExemption("eth0", "192.0.2.10", 80, "10.9.0.5", "tcp", 8080), true},
		{"the translated port is the original one when the target names none", fwstate.V4,
			`-d 192.0.2.13/32 -p tcp -m tcp --dport 25 -m comment --comment "mail" -j DNAT --to-destination 10.9.0.9`,
			dtfExemption("", "192.0.2.13", 25, "10.9.0.9", "tcp", 25), true},
		{"a UDP forward of every port to one port", fwstate.V4,
			"-d 192.0.2.11/32 -p udp -j DNAT --to-destination 10.9.0.7:5353",
			dtfExemption("", "192.0.2.11", 0, "10.9.0.7", "udp", 5353), true},
		{"a forward of every port to every port", fwstate.V4,
			"-d 192.0.2.11/32 -p tcp -j DNAT --to-destination 10.9.0.7",
			dtfExemption("", "192.0.2.11", 0, "10.9.0.7", "tcp", 0), true},
		{"a source restriction does not change what the rule translates", fwstate.V4,
			"-s 203.0.113.0/24 -d 192.0.2.10/32 -p tcp -m tcp --dport 80 -j DNAT --to-destination 10.9.0.5:80",
			dtfExemption("", "192.0.2.10", 80, "10.9.0.5", "tcp", 80), true},
		{"an interface wildcard is kept as written", fwstate.V4,
			"-d 192.0.2.10/32 -i eth+ -p tcp -m tcp --dport 80 -j DNAT --to-destination 10.9.0.5",
			dtfExemption("eth+", "192.0.2.10", 80, "10.9.0.5", "tcp", 80), true},
		{"an IPv6 forward", fwstate.V6,
			"-d 2001:db8::10/128 -p tcp -m tcp --dport 443 -j DNAT --to-destination [fd00::5]:8443",
			dtfExemption("", "2001:db8::10", 443, "fd00::5", "tcp", 8443), true},
		{"an IPv6 forward without a target port", fwstate.V6,
			"-d 2001:db8::10/128 -p tcp -m tcp --dport 443 -j DNAT --to-destination fd00::5",
			dtfExemption("", "2001:db8::10", 443, "fd00::5", "tcp", 443), true},
		{"no destination address", fwstate.V4, "-p tcp -m tcp --dport 80 -j DNAT --to-destination 10.9.0.5:80", Exemption{}, false},
		{"a destination network", fwstate.V4, "-d 192.0.2.0/24 -p tcp -m tcp --dport 80 -j DNAT --to-destination 10.9.0.5:80", Exemption{}, false},
		{"a negated destination", fwstate.V4, "! -d 192.0.2.10/32 -p tcp -m tcp --dport 80 -j DNAT --to-destination 10.9.0.5:80", Exemption{}, false},
		{"a negated in-interface", fwstate.V4, "! -i eth1 -d 192.0.2.10/32 -p tcp -m tcp --dport 80 -j DNAT --to-destination 10.9.0.5:80", Exemption{}, false},
		{"a port range", fwstate.V4, "-d 192.0.2.10/32 -p tcp -m tcp --dport 8000:8100 -j DNAT --to-destination 10.9.0.5", Exemption{}, false},
		{"a port list", fwstate.V4, "-d 192.0.2.10/32 -p tcp -m multiport --dports 80,443 -j DNAT --to-destination 10.9.0.5", Exemption{}, false},
		{"a source port", fwstate.V4, "-d 192.0.2.10/32 -p tcp -m tcp --sport 1024 --dport 80 -j DNAT --to-destination 10.9.0.5", Exemption{}, false},
		{"an address type match", fwstate.V4, "-d 192.0.2.10/32 -p tcp -m tcp --dport 80 -m addrtype --src-type LOCAL -j DNAT --to-destination 10.9.0.5", Exemption{}, false},
		{"a rule the parser keeps opaque", fwstate.V4, "-d 192.0.2.10/32 -p tcp -m tcp --dport 80 -j DNAT --to-destination 10.9.0.5 --persistent", Exemption{}, false},
		{"no protocol", fwstate.V4, "-d 192.0.2.10/32 -j DNAT --to-destination 10.9.0.5", Exemption{}, false},
		{"a protocol that has no ports", fwstate.V4, "-d 192.0.2.10/32 -p icmp -j DNAT --to-destination 10.9.0.5", Exemption{}, false},
		{"a translated address range", fwstate.V4, "-d 192.0.2.10/32 -p tcp -m tcp --dport 80 -j DNAT --to-destination 10.9.0.5-10.9.0.9", Exemption{}, false},
		{"a target of the other family", fwstate.V6, "-d 2001:db8::10/128 -p tcp -m tcp --dport 80 -j DNAT --to-destination 10.9.0.5", Exemption{}, false},
		{"a rule of the other family", fwstate.V6, "-d 192.0.2.10/32 -p tcp -m tcp --dport 80 -j DNAT --to-destination 10.9.0.5", Exemption{}, false},
		{"not a DNAT rule", fwstate.V4, "-d 192.0.2.10/32 -p tcp -m tcp --dport 80 -j ACCEPT", Exemption{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := Propose(dtfRule(t, tt.rule), tt.fam)
			if ok != tt.ok || got != tt.want {
				t.Errorf("Propose = %+v, %v; want %+v, %v", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestDtfCovered(t *testing.T) {
	const line = "-d 192.0.2.10/32 -i eth0 -p tcp -m tcp --dport 80 -j DNAT --to-destination 10.9.0.5:8080"
	exact := dtfExemption("eth0", "192.0.2.10", 80, "10.9.0.5", "tcp", 8080)
	with := func(edit func(e *Exemption)) Exemption {
		e := exact
		edit(&e)
		return e
	}
	tests := []struct {
		name string
		rule string
		ex   []Exemption
		want bool
	}{
		{"an exemption equal to the proposal", line, []Exemption{exact}, true},
		{"one matching exemption among others", line, []Exemption{with(func(e *Exemption) { e.Proto = "udp" }), exact}, true},
		{"an exemption for any interface", line, []Exemption{with(func(e *Exemption) { e.InInterface = "" })}, true},
		{"an exemption for any original port", line, []Exemption{with(func(e *Exemption) { e.OrigPort = 0 })}, true},
		{"an exemption for any translated port", line, []Exemption{with(func(e *Exemption) { e.Port = 0 })}, true},
		{"an exemption for another interface", line, []Exemption{with(func(e *Exemption) { e.InInterface = "eth1" })}, false},
		{"an exemption for another original port", line, []Exemption{with(func(e *Exemption) { e.OrigPort = 81 })}, false},
		{"an exemption for another translated port", line, []Exemption{with(func(e *Exemption) { e.Port = 8081 })}, false},
		{"an exemption for another original address", line, []Exemption{with(func(e *Exemption) { e.OrigDestination = netip.MustParseAddr("192.0.2.11") })}, false},
		{"an exemption for another destination", line, []Exemption{with(func(e *Exemption) { e.Destination = netip.MustParseAddr("10.9.0.6") })}, false},
		{"an exemption for another protocol", line, []Exemption{with(func(e *Exemption) { e.Proto = "udp" })}, false},
		{"no exemptions", line, nil, false},
		{"a rule with no destination address is never covered", "-p tcp -m tcp --dport 80 -j DNAT --to-destination 10.9.0.5:8080",
			[]Exemption{with(func(e *Exemption) { e.InInterface = "" })}, false},
		{"an IPv6 rule", "-d 2001:db8::10/128 -p tcp -m tcp --dport 443 -j DNAT --to-destination [fd00::5]:8443",
			[]Exemption{dtfExemption("", "2001:db8::10", 443, "fd00::5", "tcp", 8443)}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Covered(dtfRule(t, tt.rule), tt.ex); got != tt.want {
				t.Errorf("Covered = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDtfExemptionConflicts(t *testing.T) {
	p := netip.MustParsePrefix
	nets := []Network{
		{Name: "bridge", Subnets: []netip.Prefix{p("172.17.0.0/16")}},
		{Name: "app", Subnets: []netip.Prefix{p("172.21.0.0/16"), p("fd00:dead::/64")}},
		{Name: "narrow", Subnets: []netip.Prefix{p("172.21.0.0/24")}},
	}
	ex := []Exemption{
		dtfExemption("", "192.0.2.10", 80, "10.9.0.5", "tcp", 80),
		dtfExemption("", "192.0.2.11", 80, "172.21.0.9", "tcp", 80),
		dtfExemption("", "2001:db8::10", 80, "fd00:dead::7", "tcp", 80),
		dtfExemption("", "192.0.2.12", 80, "172.17.0.3", "udp", 53),
	}
	want := []string{
		"exemption 1: destination 172.21.0.9 lies in Docker network app (172.21.0.0/16)",
		"exemption 1: destination 172.21.0.9 lies in Docker network narrow (172.21.0.0/24)",
		"exemption 2: destination fd00:dead::7 lies in Docker network app (fd00:dead::/64)",
		"exemption 3: destination 172.17.0.3 lies in Docker network bridge (172.17.0.0/16)",
	}
	if got := ExemptionConflicts(ex, nets); !slices.Equal(got, want) {
		t.Errorf("ExemptionConflicts =\n%q\nwant\n%q", got, want)
	}
	if got := ExemptionConflicts(ex[:1], nets); len(got) != 0 {
		t.Errorf("ExemptionConflicts(no conflict) = %q", got)
	}
	if got := ExemptionConflicts(ex, nil); len(got) != 0 {
		t.Errorf("ExemptionConflicts(no networks) = %q", got)
	}
}

// TestDtfCaptures holds ForeignDNAT to what Docker 29.9.1 left in the nat table
// of the four images.
func TestDtfCaptures(t *testing.T) {
	for _, image := range capImages {
		for _, scenario := range capScenarios {
			t.Run(image+"/"+scenario, func(t *testing.T) {
				v4 := capLoad(t, image, scenario)[fwstate.V4]
				if v4 == nil {
					t.Fatal("the capture has no IPv4 tables")
				}
				nat := v4.Table("nat")
				if nat == nil {
					t.Fatal("no nat table in the capture")
				}
				if got := ForeignDNAT(nat); len(got) != 0 {
					t.Errorf("ForeignDNAT = %d rules on a host with only Docker's forwards: %q", len(got), got[0].Text)
				}
				if scenario != "base" {
					return
				}
				// The control: the captured nat table does hold Docker's DNAT
				// rules, so the check above proves something.
				dnat := 0
				for _, c := range nat.Chains {
					for _, r := range c.Rules {
						if r.Target.Name == "DNAT" {
							dnat++
						}
					}
				}
				if dnat == 0 {
					t.Error("the base capture's nat table holds no DNAT rule, so the ForeignDNAT check proves nothing")
				}
			})
		}
	}
}
