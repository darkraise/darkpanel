package dockerfw

import (
	"bytes"
	"encoding/json"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

// dtTable parses save text and returns the named table.
func dtTable(t *testing.T, save, name string) *netfilter.Table {
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

func TestDtUnsupported(t *testing.T) {
	supported := Detection{Version: "29.9.1", Driver: "iptables", DockerVariant: "nft", UFWVariant: "nft", RawProtection: true}
	tests := []struct {
		name string
		edit func(d *Detection)
		want []string
	}{
		{"every condition holds", func(*Detection) {}, nil},
		{"28.0 is the floor", func(d *Detection) { d.Version = "28.0.0" }, nil},
		{"a pre-release of 28.0", func(d *Detection) { d.Version = "28.0.0-rc.1" }, nil},
		{"legacy on both sides", func(d *Detection) { d.DockerVariant, d.UFWVariant = "legacy", "legacy" }, nil},
		{"Docker 27", func(d *Detection) { d.Version = "27.5.1" }, []string{"Docker 27.5.1 is older than 28.0"}},
		{"Docker 9", func(d *Detection) { d.Version = "9.9.9" }, []string{"Docker 9.9.9 is older than 28.0"}},
		{"no version", func(d *Detection) { d.Version = "" }, []string{`Docker version "" is not recognised`}},
		{"a version that is not numbers", func(d *Detection) { d.Version = "dev" }, []string{`Docker version "dev" is not recognised`}},
		{"nftables backend", func(d *Detection) { d.Driver = "nftables" },
			[]string{`Docker's firewall backend is "nftables"; only "iptables" is supported`}},
		{"no driver", func(d *Detection) { d.Driver = "" },
			[]string{`Docker's firewall backend is ""; only "iptables" is supported`}},
		{"Docker's chains are nowhere", func(d *Detection) { d.DockerVariant = "" },
			[]string{"Docker's chains DOCKER-USER and DOCKER-FORWARD are in neither iptables variant"}},
		{"Docker's chains are in both variants", func(d *Detection) { d.DockerVariant = "both" },
			[]string{"Docker's chains are in both iptables variants"}},
		{"Docker and UFW use different variants", func(d *Detection) { d.DockerVariant, d.UFWVariant = "nft", "legacy" },
			[]string{"Docker's chains are in iptables-nft but UFW uses iptables-legacy"}},
		{"UFW's variant is unknown", func(d *Detection) { d.UFWVariant = "" },
			[]string{"UFW's iptables variant is not known"}},
		{"raw protection is not loaded", func(d *Detection) { d.RawProtection = false },
			[]string{"Docker's raw-table protection of published ports is not loaded"}},
		{"every reason, in order", func(d *Detection) {
			d.Version, d.Driver, d.DockerVariant, d.UFWVariant, d.RawProtection = "27.5.1", "nftables", "both", "legacy", false
		}, []string{
			"Docker 27.5.1 is older than 28.0",
			`Docker's firewall backend is "nftables"; only "iptables" is supported`,
			"Docker's chains are in both iptables variants",
			"Docker's raw-table protection of published ports is not loaded",
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := supported
			tt.edit(&d)
			if got := Unsupported(d); !slices.Equal(got, tt.want) {
				t.Errorf("Unsupported(%+v) = %q, want %q", d, got, tt.want)
			}
		})
	}
}

func TestDtDockerChains(t *testing.T) {
	const head = "*filter\n:INPUT ACCEPT [0:0]\n:FORWARD DROP [0:0]\n:OUTPUT ACCEPT [0:0]\n"
	tests := []struct {
		name string
		save string
		want bool
	}{
		{"both chains with rules", head + ":DOCKER-USER - [0:0]\n:DOCKER-FORWARD - [0:0]\n-A FORWARD -j DOCKER-USER\n-A FORWARD -j DOCKER-FORWARD\nCOMMIT\n", true},
		{"both chains empty", head + ":DOCKER-USER - [0:0]\n:DOCKER-FORWARD - [0:0]\nCOMMIT\n", true},
		{"only DOCKER-USER", head + ":DOCKER-USER - [0:0]\nCOMMIT\n", false},
		{"only DOCKER-FORWARD", head + ":DOCKER-FORWARD - [0:0]\nCOMMIT\n", false},
		{"neither", head + ":ufw-user-input - [0:0]\nCOMMIT\n", false},
		{"a table nft cannot show", "# Table `filter' is incompatible, use 'nft' tool.\n", false},
		{"a table with incompatible base-chains that still declares both", "# Table `filter' contains incompatible base-chains, use 'nft' tool to list them.\n" + head + ":DOCKER-USER - [0:0]\n:DOCKER-FORWARD - [0:0]\nCOMMIT\n", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DockerChains(dtTable(t, tt.save, "filter")); got != tt.want {
				t.Errorf("DockerChains = %v, want %v", got, tt.want)
			}
		})
	}
	if DockerChains(nil) {
		t.Error("DockerChains(nil) = true")
	}
}

const dtRaw = `*raw
:PREROUTING ACCEPT [0:0]
:OUTPUT ACCEPT [0:0]
-A PREROUTING -d 172.17.0.2/32 ! -i docker0 -j DROP
-A PREROUTING -d 172.21.0.2/32 ! -i br-a2871ddf38b3 -j DROP
-A PREROUTING -d 127.0.0.1/32 ! -i lo -p tcp -m tcp --dport 5432 -j DROP
-A PREROUTING -d 172.21.0.3/32 ! -i br-a2871ddf38b3 -j DROP
COMMIT
`

// dtFacts is the running containers behind dtRaw: a cache on the default
// bridge, a database published on 127.0.0.1 and a web server published on
// every address.
func dtFacts() Facts {
	a := netip.MustParseAddr
	app := []Endpoint{{Network: "app", Bridge: "br-a2871ddf38b3", Addr4: a("172.21.0.2")}}
	web := []Endpoint{{Network: "app", Bridge: "br-a2871ddf38b3", Addr4: a("172.21.0.3")}}
	return Facts{
		Containers: []Container{
			{ID: "c1", Name: "cache", Endpoints: []Endpoint{{Network: "bridge", Bridge: "docker0", Addr4: a("172.17.0.2")}}},
			{ID: "c2", Name: "db", Endpoints: app,
				Ports: []Published{{HostAddr: a("127.0.0.1"), HostPort: 5432, ContainerPort: 5432, Proto: "tcp"}}},
			{ID: "c3", Name: "web", Endpoints: web,
				Ports: []Published{{HostAddr: a("0.0.0.0"), HostPort: 8000, ContainerPort: 8000, Proto: "tcp"}}},
		},
		Networks: []Network{
			{ID: "0123456789abcdef", Name: "bridge", Bridge: "docker0", Subnets: []netip.Prefix{netip.MustParsePrefix("172.17.0.0/16")}},
			{ID: "a2871ddf38b3aaaa", Name: "app", Bridge: "br-a2871ddf38b3", Subnets: []netip.Prefix{netip.MustParsePrefix("172.21.0.0/16")}},
		},
	}
}

func TestDtRawProtected(t *testing.T) {
	a := netip.MustParseAddr
	without := func(line string) string { return strings.Replace(dtRaw, line+"\n", "", 1) }
	routedNet := func(f *Facts, mode string) {
		f.Containers = append(f.Containers, Container{ID: "c9", Name: "direct",
			Endpoints: []Endpoint{{Network: "direct", Bridge: "br-direct", Addr4: a("198.51.100.2")}}})
		f.Networks = append(f.Networks, Network{ID: "dddddddddddd", Name: "direct", Bridge: "br-direct", GatewayModeV4: mode})
	}
	tests := []struct {
		name string
		raw  string
		edit func(f *Facts)
		want bool
	}{
		{"Docker 29's own rules", dtRaw, func(*Facts) {}, true},
		{"the loopback port rule is missing", without("-A PREROUTING -d 127.0.0.1/32 ! -i lo -p tcp -m tcp --dport 5432 -j DROP"), func(*Facts) {}, false},
		{"a container address has no drop rule", without("-A PREROUTING -d 172.21.0.3/32 ! -i br-a2871ddf38b3 -j DROP"), func(*Facts) {}, false},
		{"the drop rule names another bridge", strings.Replace(dtRaw, "172.21.0.3/32 ! -i br-a2871ddf38b3", "172.21.0.3/32 ! -i docker0", 1), func(*Facts) {}, false},
		{"the rule is not negated", strings.Replace(dtRaw, "172.17.0.2/32 ! -i docker0", "172.17.0.2/32 -i docker0", 1), func(*Facts) {}, false},
		{"the drop matches fragments only", strings.Replace(dtRaw, "172.17.0.2/32 ! -i docker0 -j DROP", "172.17.0.2/32 ! -i docker0 -f -j DROP", 1), func(*Facts) {}, false},
		{"the drop is restricted by source", strings.Replace(dtRaw, "-A PREROUTING -d 172.17.0.2/32", "-A PREROUTING -s 10.0.0.0/8 -d 172.17.0.2/32", 1), func(*Facts) {}, false},
		{"a loopback UDP port needs a udp rule", dtRaw, func(f *Facts) {
			f.Containers[1].Ports = []Published{{HostAddr: a("127.0.0.1"), HostPort: 5432, ContainerPort: 5432, Proto: "udp"}}
		}, false},
		{"a loopback UDP port with its udp rule", strings.Replace(dtRaw, "-p tcp -m tcp --dport 5432", "-p udp -m udp --dport 5432", 1), func(f *Facts) {
			f.Containers[1].Ports = []Published{{HostAddr: a("127.0.0.1"), HostPort: 5432, ContainerPort: 5432, Proto: "udp"}}
		}, true},
		{"another loopback port is not covered by the 5432 rule", dtRaw, func(f *Facts) {
			f.Containers[1].Ports[0].HostPort = 5433
		}, false},
		{"a port on a specific public address needs no raw rule", dtRaw, func(f *Facts) {
			f.Containers[2].Ports = append(f.Containers[2].Ports, Published{HostAddr: a("192.0.2.10"), HostPort: 9000, ContainerPort: 9000, Proto: "tcp"})
		}, true},
		{"a port on the IPv6 loopback is not an IPv4 rule", dtRaw, func(f *Facts) {
			f.Containers[2].Ports = append(f.Containers[2].Ports, Published{HostAddr: a("::1"), HostPort: 9001, ContainerPort: 9001, Proto: "tcp"})
		}, true},
		{"an endpoint with no IPv4 address", dtRaw, func(f *Facts) {
			f.Containers = append(f.Containers, Container{ID: "c8", Name: "six",
				Endpoints: []Endpoint{{Network: "app", Bridge: "br-a2871ddf38b3", Addr6: a("fd00:dead::2")}}})
		}, true},
		{"a NAT network's container without its rule", dtRaw, func(f *Facts) { routedNet(f, "") }, false},
		{"a routed network needs no rule", dtRaw, func(f *Facts) { routedNet(f, "routed") }, true},
		{"a nat-unprotected network needs no rule", dtRaw, func(f *Facts) { routedNet(f, "nat-unprotected") }, true},
		{"an endpoint of an unknown network is held to the rule", dtRaw, func(f *Facts) {
			f.Containers = append(f.Containers, Container{ID: "c7", Name: "lost",
				Endpoints: []Endpoint{{Network: "gone", Bridge: "br-gone", Addr4: a("172.25.0.2")}}})
		}, false},
		{"nothing to protect", "*raw\n:PREROUTING ACCEPT [0:0]\n:OUTPUT ACCEPT [0:0]\nCOMMIT\n", func(f *Facts) { *f = Facts{} }, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := dtFacts()
			tt.edit(&f)
			if got := RawProtected(dtTable(t, tt.raw, "raw"), f); got != tt.want {
				t.Errorf("RawProtected = %v, want %v", got, tt.want)
			}
		})
	}
	t.Run("no raw table", func(t *testing.T) {
		if RawProtected(nil, Facts{}) {
			t.Error("RawProtected(nil, no containers) = true")
		}
	})
	t.Run("a raw table nft cannot show", func(t *testing.T) {
		tb := dtTable(t, "# Table `raw' is incompatible, use 'nft' tool.\n", "raw")
		if RawProtected(tb, Facts{}) {
			t.Error("RawProtected(incompatible table, no containers) = true")
		}
	})
}

// dtInspect and dtNet are the fields of the captured Docker inspect output
// that the facts need.
type dtInspect struct {
	ID              string
	Name            string
	State           struct{ Running bool }
	NetworkSettings struct {
		Ports    map[string][]struct{ HostIP, HostPort string }
		Networks map[string]struct{ IPAddress, GlobalIPv6Address string }
	}
}

type dtNet struct {
	Name, ID, Driver string
	EnableIPv6       bool
	Options          map[string]string
	IPAM             struct{ Config []struct{ Subnet string } }
}

// dtDecode reads a capture file holding a JSON array or a stream of objects.
func dtDecode[T any](t *testing.T, data []byte) []T {
	t.Helper()
	var list []T
	if err := json.Unmarshal(data, &list); err == nil {
		return list
	}
	list = nil
	dec := json.NewDecoder(bytes.NewReader(data))
	for dec.More() {
		var v T
		if err := dec.Decode(&v); err != nil {
			t.Fatalf("decode capture JSON: %v", err)
		}
		list = append(list, v)
	}
	return list
}

// dtCaptureFacts is the facts the capture's running containers and networks
// give.
func dtCaptureFacts(t *testing.T, image, scenario string) Facts {
	t.Helper()
	var f Facts
	bridge := map[string]string{}
	mode := map[string]string{}
	for _, n := range dtDecode[dtNet](t, capFile(t, image, scenario, "networks.json")) {
		b := n.Options["com.docker.network.bridge.name"]
		switch {
		case b != "":
		case n.Name == "bridge":
			b = "docker0"
		default:
			b = "br-" + n.ID[:12]
		}
		bridge[n.Name], mode[n.Name] = b, n.Options["com.docker.network.bridge.gateway_mode_ipv4"]
		nw := Network{ID: n.ID, Name: n.Name, Driver: n.Driver, Bridge: b, IPv6: n.EnableIPv6, GatewayModeV4: mode[n.Name]}
		for _, c := range n.IPAM.Config {
			nw.Subnets = append(nw.Subnets, netip.MustParsePrefix(c.Subnet))
		}
		f.Networks = append(f.Networks, nw)
	}
	for _, c := range dtDecode[dtInspect](t, capFile(t, image, scenario, "containers.json")) {
		if !c.State.Running {
			continue
		}
		ct := Container{ID: c.ID, Name: strings.TrimPrefix(c.Name, "/")}
		for key, bindings := range c.NetworkSettings.Ports {
			port, proto, _ := strings.Cut(key, "/")
			for _, b := range bindings {
				host := netip.IPv4Unspecified()
				if b.HostIP != "" {
					host = netip.MustParseAddr(b.HostIP)
				}
				cp, err := strconv.ParseUint(port, 10, 16)
				if err != nil {
					t.Fatal(err)
				}
				hp, err := strconv.ParseUint(b.HostPort, 10, 16)
				if err != nil {
					t.Fatal(err)
				}
				ct.Ports = append(ct.Ports, Published{HostAddr: host, HostPort: uint16(hp), ContainerPort: uint16(cp), Proto: proto})
			}
		}
		for name, ep := range c.NetworkSettings.Networks {
			e := Endpoint{Network: name, Bridge: bridge[name]}
			if ep.IPAddress != "" {
				e.Addr4 = netip.MustParseAddr(ep.IPAddress)
			}
			if ep.GlobalIPv6Address != "" {
				e.Addr6 = netip.MustParseAddr(ep.GlobalIPv6Address)
			}
			ct.Endpoints = append(ct.Endpoints, e)
		}
		f.Containers = append(f.Containers, ct)
	}
	return f
}

// TestDtCaptures holds this file's functions to what Docker 29.9.1 and UFW
// left on the four images.
func TestDtCaptures(t *testing.T) {
	for _, image := range capImages {
		for _, scenario := range capScenarios {
			t.Run(image+"/"+scenario, func(t *testing.T) {
				v4 := capLoad(t, image, scenario)[fwstate.V4]
				if v4 == nil {
					t.Fatal("the capture has no IPv4 tables")
				}
				if !DockerChains(v4.Table("filter")) {
					t.Error("DockerChains = false on the variant UFW uses")
				}
				facts := dtCaptureFacts(t, image, scenario)
				protected := RawProtected(v4.Table("raw"), facts)
				if !protected {
					t.Errorf("RawProtected = false for facts %+v", facts.Containers)
				}
				var info struct {
					ServerVersion   string
					FirewallBackend *struct{ Driver string }
				}
				if err := json.Unmarshal(capFile(t, image, scenario, "info.json"), &info); err != nil {
					t.Fatal(err)
				}
				driver := "iptables"
				if info.FirewallBackend != nil {
					driver = info.FirewallBackend.Driver
				}
				// capLoad reads only the variant UFW uses, so the variant fields
				// stand in for each other here.
				d := Detection{Version: info.ServerVersion, Driver: driver, DockerVariant: "nft", UFWVariant: "nft", RawProtection: protected}
				if got := Unsupported(d); len(got) != 0 {
					t.Errorf("Unsupported(%+v) = %q", d, got)
				}
				if scenario != "base" {
					return
				}
				// The control: the captured containers do need raw rules.
				if len(facts.Containers) == 0 {
					t.Fatal("the base capture has no running container")
				}
				if RawProtected(&netfilter.Table{Name: "raw"}, facts) {
					t.Error("RawProtected(empty raw table, the base capture's containers) = true")
				}
			})
		}
	}
}
