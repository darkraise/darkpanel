package dockerfw

import (
	"net/netip"
	"reflect"
	"testing"

	"github.com/darkraise/darkpanel/internal/modules/firewall/model"
)

const (
	rzID1 = "01J00000000000000000000001"
	rzID2 = "01J00000000000000000000002"
	rzID3 = "01J00000000000000000000003"
	rzID4 = "01J00000000000000000000004"
)

// rzPub is a published port: host address (empty for 0.0.0.0), host port,
// container port, protocol.
func rzPub(host string, hostPort, containerPort uint16, proto string) Published {
	if host == "" {
		host = "0.0.0.0"
	}
	return Published{HostAddr: netip.MustParseAddr(host), HostPort: hostPort, ContainerPort: containerPort, Proto: proto}
}

// rzEp is a container's endpoint on a network; an empty address is absent.
func rzEp(network, bridge, v4, v6 string) Endpoint {
	e := Endpoint{Network: network, Bridge: bridge}
	if v4 != "" {
		e.Addr4 = netip.MustParseAddr(v4)
	}
	if v6 != "" {
		e.Addr6 = netip.MustParseAddr(v6)
	}
	return e
}

func rzContainer(name string, ports []Published, eps ...Endpoint) Container {
	return Container{ID: "id-" + name, Name: name, Ports: ports, Endpoints: eps}
}

func rzNetwork(name, bridge string, subnets ...string) Network {
	n := Network{ID: "id-" + name, Name: name, Driver: "bridge", Bridge: bridge}
	for _, s := range subnets {
		n.Subnets = append(n.Subnets, netip.MustParsePrefix(s))
	}
	return n
}

func rzFacts(containers []Container, networks ...Network) Facts {
	return Facts{Containers: containers, Networks: networks}
}

// rzRow is a container rule: an empty host is every address, an empty from
// is any source.
func rzRow(id, selector, host string, port uint16, kind Kind, from string) ContainerRule {
	r := ContainerRule{ID: id, Binding: Binding{Selector: selector, HostPort: port, Proto: "tcp"}, Kind: kind}
	if host != "" {
		r.Binding.HostAddr = netip.MustParseAddr(host)
	}
	if from != "" {
		r.From = netip.MustParsePrefix(from)
	}
	return r
}

func rzRoute(action model.Action, id, from, bridge, to, port string, fam model.Family) model.Rule {
	return model.Rule{Action: action, Direction: model.DirRoute, Proto: model.ProtoTCP, From: from, To: to, ToPort: port,
		OutIface: bridge, Family: fam, Log: model.LogOff, Comment: LabelPrefix + id, Origin: model.OriginDarkpanel}
}

func rzIn(action model.Action, id, from, to, port string, fam model.Family) model.Rule {
	return model.Rule{Action: action, Direction: model.DirIn, Proto: model.ProtoTCP, From: from, To: to, ToPort: port,
		Family: fam, Log: model.LogOff, Comment: LabelPrefix + id, Origin: model.OriginDarkpanel}
}

func rzLine(b Binding, addr string, containerPort uint16) BindingLine {
	return BindingLine{Proto: b.Proto, ContainerAddr: netip.MustParseAddr(addr), ContainerPort: containerPort,
		HostAddr: b.HostAddr, HostPort: b.HostPort, Chain: b.Chain()}
}

func rzPrefixes(s ...string) []netip.Prefix {
	var out []netip.Prefix
	for _, p := range s {
		out = append(out, netip.MustParsePrefix(p))
	}
	return out
}

// rzCheck compares the parts of a Realized separately for readable output.
func rzCheck(t *testing.T, got Realized, lines []BindingLine, chains []BindingChain, rules []model.Rule) {
	t.Helper()
	if !reflect.DeepEqual(got.Block.Lines, lines) {
		t.Errorf("Block.Lines =\n%+v\nwant\n%+v", got.Block.Lines, lines)
	}
	if !reflect.DeepEqual(got.Block.Chains, chains) {
		t.Errorf("Block.Chains =\n%+v\nwant\n%+v", got.Block.Chains, chains)
	}
	if !reflect.DeepEqual(got.Rules, rules) {
		t.Errorf("Rules =\n%+v\nwant\n%+v", got.Rules, rules)
	}
}

func TestRzRealizeRouteAllow(t *testing.T) {
	facts := rzFacts(
		[]Container{rzContainer("web", []Published{rzPub("", 8080, 80, "tcp")}, rzEp("bridge", "docker0", "172.17.0.2", ""))},
		rzNetwork("bridge", "docker0", "172.17.0.0/16"))
	row := rzRow(rzID1, "web", "", 8080, Allow, "")
	got := Realize([]ContainerRule{row}, facts, DefaultSettings())
	if !reflect.DeepEqual(got.Block.Settings, DefaultSettings()) || got.Block.CustomBridges != nil {
		t.Errorf("Block settings %+v and custom bridges %v, want the settings passed in and no custom bridge", got.Block.Settings, got.Block.CustomBridges)
	}
	rzCheck(t, got,
		[]BindingLine{rzLine(row.Binding, "172.17.0.2", 80)},
		[]BindingChain{{Name: row.Binding.Chain()}},
		[]model.Rule{rzRoute(model.ActionAllow, rzID1, "any", "docker0", "172.17.0.2", "80", model.FamilyV4)})

	dns := rzRow(rzID2, "dns", "", 53, Allow, "")
	dns.Binding.Proto = "udp"
	facts = rzFacts([]Container{rzContainer("dns", []Published{rzPub("", 53, 5353, "udp")}, rzEp("bridge", "docker0", "172.17.0.3", ""))})
	udpRule := rzRoute(model.ActionAllow, rzID2, "any", "docker0", "172.17.0.3", "5353", model.FamilyV4)
	udpRule.Proto = model.ProtoUDP
	got = Realize([]ContainerRule{dns}, facts, DefaultSettings())
	rzCheck(t, got,
		[]BindingLine{rzLine(dns.Binding, "172.17.0.3", 5353)},
		[]BindingChain{{Name: dns.Binding.Chain()}},
		[]model.Rule{udpRule})
}

func TestRzRealizeProxiedFamily(t *testing.T) {
	facts := rzFacts([]Container{rzContainer("web",
		[]Published{rzPub("", 8080, 80, "tcp"), rzPub("::", 8080, 80, "tcp")},
		rzEp("bridge", "docker0", "172.17.0.2", ""))})
	allow := rzRow(rzID1, "web", "", 8080, Allow, "")
	deny := rzRow(rzID2, "web", "", 8080, Deny, "2001:db8:bad::/48")
	got := Realize([]ContainerRule{allow, deny}, facts, DefaultSettings())
	rzCheck(t, got,
		[]BindingLine{rzLine(allow.Binding, "172.17.0.2", 80)},
		[]BindingChain{{Name: allow.Binding.Chain(), Deny: rzPrefixes("2001:db8:bad::/48")}},
		[]model.Rule{
			rzIn(model.ActionDeny, rzID2, "2001:db8:bad::/48", "any", "8080", model.FamilyV6),
			rzIn(model.ActionAllow, rzID1, "any", "any", "8080", model.FamilyV6),
			rzRoute(model.ActionAllow, rzID1, "any", "docker0", "172.17.0.2", "80", model.FamilyV4),
		})
}

func TestRzRealizeProxiedBothFamiliesMergeIntoOneRule(t *testing.T) {
	facts := rzFacts([]Container{rzContainer("web", []Published{rzPub("", 8080, 80, "tcp"), rzPub("::", 8080, 80, "tcp")})})
	row := rzRow(rzID1, "web", "", 8080, Allow, "")
	got := Realize([]ContainerRule{row}, facts, DefaultSettings())
	rzCheck(t, got, nil, nil, []model.Rule{rzIn(model.ActionAllow, rzID1, "any", "any", "8080", model.FamilyBoth)})

	pinned := rzRow(rzID2, "web", "192.0.2.10", 8080, Allow, "")
	facts = rzFacts([]Container{rzContainer("web", []Published{rzPub("192.0.2.10", 8080, 80, "tcp")})})
	got = Realize([]ContainerRule{pinned}, facts, DefaultSettings())
	rzCheck(t, got, nil, nil, []model.Rule{rzIn(model.ActionAllow, rzID2, "any", "192.0.2.10", "8080", model.FamilyV4)})
}

func TestRzRealizeAllowAndDenyWithSources(t *testing.T) {
	facts := rzFacts([]Container{rzContainer("web", []Published{rzPub("", 8080, 80, "tcp")}, rzEp("bridge", "docker0", "172.17.0.2", ""))})
	deny := rzRow(rzID1, "web", "", 8080, Deny, "203.0.113.0/24")
	allowNet := rzRow(rzID2, "web", "", 8080, Allow, "198.51.100.0/24")
	allowHost := rzRow(rzID4, "web", "", 8080, Allow, "198.51.100.7/32")
	got := Realize([]ContainerRule{deny, allowHost, allowNet}, facts, DefaultSettings())
	rzCheck(t, got,
		[]BindingLine{rzLine(allowNet.Binding, "172.17.0.2", 80)},
		[]BindingChain{{Name: allowNet.Binding.Chain(), Deny: rzPrefixes("203.0.113.0/24"), Allow: rzPrefixes("198.51.100.0/24", "198.51.100.7/32")}},
		[]model.Rule{
			rzRoute(model.ActionAllow, rzID2, "198.51.100.0/24", "docker0", "172.17.0.2", "80", model.FamilyV4),
			rzRoute(model.ActionAllow, rzID4, "198.51.100.7", "docker0", "172.17.0.2", "80", model.FamilyV4),
			rzRoute(model.ActionDeny, rzID1, "203.0.113.0/24", "docker0", "172.17.0.2", "80", model.FamilyV4),
		})
}

func TestRzRealizeAnyAndSpecificAllow(t *testing.T) {
	facts := rzFacts([]Container{rzContainer("web", []Published{rzPub("", 8080, 80, "tcp")}, rzEp("bridge", "docker0", "172.17.0.2", ""))})
	any1 := rzRow(rzID1, "web", "", 8080, Allow, "")
	net2 := rzRow(rzID2, "web", "", 8080, Allow, "198.51.100.0/24")
	got := Realize([]ContainerRule{net2, any1}, facts, DefaultSettings())
	rzCheck(t, got,
		[]BindingLine{rzLine(any1.Binding, "172.17.0.2", 80)},
		[]BindingChain{{Name: any1.Binding.Chain()}},
		[]model.Rule{
			rzRoute(model.ActionAllow, rzID1, "any", "docker0", "172.17.0.2", "80", model.FamilyV4),
			rzRoute(model.ActionAllow, rzID2, "198.51.100.0/24", "docker0", "172.17.0.2", "80", model.FamilyV4),
		})
}

func TestRzRealizeBindingsShareAContainerPort(t *testing.T) {
	facts := rzFacts([]Container{rzContainer("web",
		[]Published{rzPub("", 8080, 80, "tcp"), rzPub("", 8081, 80, "tcp")},
		rzEp("bridge", "docker0", "172.17.0.2", ""))})
	deny8080 := rzRow(rzID1, "web", "", 8080, Deny, "203.0.113.0/24")
	allow8080 := rzRow(rzID3, "web", "", 8080, Allow, "")
	allow8081 := rzRow(rzID4, "web", "", 8081, Allow, "")
	got := Realize([]ContainerRule{allow8081, allow8080, deny8080}, facts, DefaultSettings())
	rzCheck(t, got,
		[]BindingLine{rzLine(allow8080.Binding, "172.17.0.2", 80), rzLine(allow8081.Binding, "172.17.0.2", 80)},
		[]BindingChain{
			{Name: allow8080.Binding.Chain(), Deny: rzPrefixes("203.0.113.0/24")},
			{Name: allow8081.Binding.Chain()},
		},
		[]model.Rule{
			rzRoute(model.ActionAllow, rzID3, "any", "docker0", "172.17.0.2", "80", model.FamilyV4),
			rzRoute(model.ActionDeny, rzID1, "203.0.113.0/24", "docker0", "172.17.0.2", "80", model.FamilyV4),
		})
}

func TestRzRealizeDenyWithoutAllowRealizesNothing(t *testing.T) {
	facts := rzFacts([]Container{rzContainer("web", []Published{rzPub("", 8080, 80, "tcp")}, rzEp("bridge", "docker0", "172.17.0.2", ""))})
	got := Realize([]ContainerRule{rzRow(rzID1, "web", "", 8080, Deny, "203.0.113.0/24")}, facts, DefaultSettings())
	rzCheck(t, got, nil, nil, nil)
}

func TestRzRealizeEveryNetworkAndFamily(t *testing.T) {
	facts := rzFacts(
		[]Container{rzContainer("web",
			[]Published{rzPub("", 8080, 80, "tcp"), rzPub("::", 8080, 80, "tcp")},
			rzEp("bridge", "docker0", "172.17.0.2", ""),
			rzEp("custom", "dpcustom", "172.30.0.2", "fd00:dead::2"))},
		rzNetwork("bridge", "docker0", "172.17.0.0/16"),
		rzNetwork("custom", "dpcustom", "172.30.0.0/24", "fd00:dead::/64"))
	row := rzRow(rzID1, "web", "", 8080, Allow, "")
	got := Realize([]ContainerRule{row}, facts, DefaultSettings())
	if want := []string{"dpcustom"}; !reflect.DeepEqual(got.Block.CustomBridges, want) {
		t.Errorf("CustomBridges = %v, want %v", got.Block.CustomBridges, want)
	}
	rzCheck(t, got,
		[]BindingLine{
			rzLine(row.Binding, "172.17.0.2", 80),
			rzLine(row.Binding, "172.30.0.2", 80),
			rzLine(row.Binding, "fd00:dead::2", 80),
		},
		[]BindingChain{{Name: row.Binding.Chain()}},
		[]model.Rule{
			rzRoute(model.ActionAllow, rzID1, "any", "docker0", "172.17.0.2", "80", model.FamilyV4),
			rzRoute(model.ActionAllow, rzID1, "any", "dpcustom", "172.30.0.2", "80", model.FamilyV4),
			rzRoute(model.ActionAllow, rzID1, "any", "dpcustom", "fd00:dead::2", "80", model.FamilyV6),
		})
}

func TestRzRealizeOnlyTheFamiliesASourceCanReach(t *testing.T) {
	facts := rzFacts(
		[]Container{rzContainer("web",
			[]Published{rzPub("", 8080, 80, "tcp"), rzPub("::", 8080, 80, "tcp")},
			rzEp("custom", "dpcustom", "172.30.0.2", "fd00:dead::2"))},
		rzNetwork("custom", "dpcustom", "172.30.0.0/24", "fd00:dead::/64"))
	row := rzRow(rzID1, "web", "", 8080, Allow, "2001:db8::/32")
	got := Realize([]ContainerRule{row}, facts, DefaultSettings())
	rzCheck(t, got,
		[]BindingLine{rzLine(row.Binding, "fd00:dead::2", 80)},
		[]BindingChain{{Name: row.Binding.Chain(), Allow: rzPrefixes("2001:db8::/32")}},
		[]model.Rule{rzRoute(model.ActionAllow, rzID1, "2001:db8::/32", "dpcustom", "fd00:dead::2", "80", model.FamilyV6)})
}

func TestRzRealizeHostAddress(t *testing.T) {
	for _, c := range []struct {
		name      string
		bind, pub string
		wantAddr  string // the container address of the one line; empty for none
	}{
		{"every address, published on every IPv4 address", "", "0.0.0.0", "172.17.0.2"},
		{"every address, published on one address", "", "192.0.2.10", "172.17.0.2"},
		{"every address, published on every IPv6 address", "", "::", "fd00::2"},
		{"one address, published on that address", "192.0.2.10", "192.0.2.10", "172.17.0.2"},
		{"one address, published on every address of its family", "192.0.2.10", "0.0.0.0", "172.17.0.2"},
		{"one address, published on another address", "192.0.2.11", "192.0.2.10", ""},
		{"one address, published on the other family's wildcard", "192.0.2.10", "::", ""},
		{"one IPv6 address, published on every IPv6 address", "2001:db8::10", "::", "fd00::2"},
	} {
		facts := rzFacts([]Container{rzContainer("web", []Published{rzPub(c.pub, 8080, 80, "tcp")}, rzEp("bridge", "docker0", "172.17.0.2", "fd00::2"))})
		row := rzRow(rzID1, "web", c.bind, 8080, Allow, "")
		got := Realize([]ContainerRule{row}, facts, DefaultSettings())
		if c.wantAddr == "" {
			if len(got.Block.Lines) != 0 || len(got.Block.Chains) != 0 || len(got.Rules) != 0 {
				t.Errorf("%s: realized %+v, want nothing", c.name, got)
			}
			continue
		}
		fam := model.FamilyV4
		if netip.MustParseAddr(c.wantAddr).Is6() {
			fam = model.FamilyV6
		}
		rzCheck(t, got,
			[]BindingLine{rzLine(row.Binding, c.wantAddr, 80)},
			[]BindingChain{{Name: row.Binding.Chain()}},
			[]model.Rule{rzRoute(model.ActionAllow, rzID1, "any", "docker0", c.wantAddr, "80", fam)})
		if t.Failed() {
			t.Fatalf("case %q failed", c.name)
		}
	}
}

func TestRzRealizeSelectsComposeServices(t *testing.T) {
	web := rzContainer("shop-web-1", []Published{rzPub("", 8080, 80, "tcp")}, rzEp("shop", "br-0123456789ab", "172.20.0.2", ""))
	web.Project, web.Service = "shop", "web"
	db := rzContainer("shop-db-1", []Published{rzPub("", 8080, 5432, "tcp")}, rzEp("shop", "br-0123456789ab", "172.20.0.3", ""))
	db.Project, db.Service = "shop", "db"
	facts := rzFacts([]Container{db, web}, rzNetwork("shop", "br-0123456789ab", "172.20.0.0/16"))
	row := rzRow(rzID1, "shop/web", "", 8080, Allow, "")
	got := Realize([]ContainerRule{row}, facts, DefaultSettings())
	if got.Block.CustomBridges != nil {
		t.Errorf("CustomBridges = %v, want none for a br-<id> bridge", got.Block.CustomBridges)
	}
	rzCheck(t, got,
		[]BindingLine{rzLine(row.Binding, "172.20.0.2", 80)},
		[]BindingChain{{Name: row.Binding.Chain()}},
		[]model.Rule{rzRoute(model.ActionAllow, rzID1, "any", "br-0123456789ab", "172.20.0.2", "80", model.FamilyV4)})
}

func TestRzRealizeNothingToRealize(t *testing.T) {
	running := rzContainer("web", []Published{rzPub("", 8080, 80, "tcp")}, rzEp("bridge", "docker0", "172.17.0.2", ""))
	for _, c := range []struct {
		name  string
		facts Facts
		row   ContainerRule
	}{
		{"the container is not running", rzFacts(nil), rzRow(rzID1, "web", "", 8080, Allow, "")},
		{"another container's name", rzFacts([]Container{running}), rzRow(rzID1, "api", "", 8080, Allow, "")},
		{"another host port", rzFacts([]Container{running}), rzRow(rzID1, "web", "", 9090, Allow, "")},
		{"another protocol", rzFacts([]Container{running}), func() ContainerRule {
			r := rzRow(rzID1, "web", "", 8080, Allow, "")
			r.Binding.Proto = "udp"
			return r
		}()},
	} {
		settings := Settings{PrivateSources: rzPrefixes("10.0.0.0/8")}
		got := Realize([]ContainerRule{c.row}, c.facts, settings)
		if len(got.Block.Lines) != 0 || len(got.Block.Chains) != 0 || len(got.Rules) != 0 {
			t.Errorf("%s: realized %+v, want no line, chain or rule", c.name, got)
		}
		if !reflect.DeepEqual(got.Block.Settings, settings) {
			t.Errorf("%s: Block.Settings = %+v, want the settings passed in", c.name, got.Block.Settings)
		}
	}
	if got := Realize(nil, rzFacts([]Container{running}), DefaultSettings()); len(got.Rules) != 0 || len(got.Block.Lines) != 0 {
		t.Errorf("no rows realized %+v", got)
	}
}

func TestRzRealizeCustomBridges(t *testing.T) {
	macvlan := rzNetwork("mv", "enp3s0")
	macvlan.Driver = "macvlan"
	host := Network{ID: "id-host", Name: "host", Driver: "host"}
	facts := rzFacts(nil,
		rzNetwork("bridge", "docker0", "172.17.0.0/16"),
		rzNetwork("shop_default", "br-0123456789ab", "172.20.0.0/16"),
		rzNetwork("zeta", "zeta0", "172.31.0.0/24"),
		rzNetwork("alpha", "alpha0", "172.30.0.0/24"),
		rzNetwork("alpha-again", "alpha0", "172.32.0.0/24"),
		rzNetwork("lan", "br-lan", "172.33.0.0/24"),
		macvlan, host)
	settings := Settings{PrivateSources: rzPrefixes("10.0.0.0/8", "fc00::/7")}
	got := Realize(nil, facts, settings)
	if want := []string{"alpha0", "zeta0"}; !reflect.DeepEqual(got.Block.CustomBridges, want) {
		t.Errorf("CustomBridges = %v, want %v", got.Block.CustomBridges, want)
	}
	if !reflect.DeepEqual(got.Block.Settings, settings) {
		t.Errorf("Block.Settings = %+v, want %+v", got.Block.Settings, settings)
	}
}

func TestRzRealizeIsIndependentOfRowOrder(t *testing.T) {
	facts := rzFacts([]Container{rzContainer("web",
		[]Published{rzPub("", 8080, 80, "tcp"), rzPub("::", 8080, 80, "tcp"), rzPub("", 8081, 80, "tcp")},
		rzEp("bridge", "docker0", "172.17.0.2", ""))})
	rows := []ContainerRule{
		rzRow(rzID1, "web", "", 8080, Deny, "203.0.113.0/24"),
		rzRow(rzID2, "web", "", 8080, Allow, "198.51.100.0/24"),
		rzRow(rzID3, "web", "", 8080, Allow, ""),
		rzRow(rzID4, "web", "", 8081, Allow, ""),
	}
	want := Realize(rows, facts, DefaultSettings())
	if len(want.Rules) == 0 || len(want.Block.Lines) == 0 {
		t.Fatalf("the fixture realized nothing: %+v", want)
	}
	for name, order := range map[string][]int{"reversed": {3, 2, 1, 0}, "shuffled": {2, 0, 3, 1}} {
		var shuffled []ContainerRule
		for _, i := range order {
			shuffled = append(shuffled, rows[i])
		}
		if got := Realize(shuffled, facts, DefaultSettings()); !reflect.DeepEqual(got, want) {
			t.Errorf("%s rows: %+v, want the result of the sorted rows %+v", name, got, want)
		}
	}
}
