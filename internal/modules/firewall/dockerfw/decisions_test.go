package dockerfw

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/modules/firewall/conf"
	"github.com/darkraise/darkpanel/internal/modules/firewall/eval"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/modules/firewall/loaded"
	"github.com/darkraise/darkpanel/internal/modules/firewall/model"
	"github.com/darkraise/darkpanel/internal/modules/firewall/predict"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

// The container rules of Task 1's block scenario: the ids its two UFW rules
// carry after "ctr ".
const (
	dcRowAllow = "01J00000000000000000000001"
	dcRowDeny  = "01J00000000000000000000003"
)

// dcDenied is the part of a reason that names a drop by the block's deny
// chain.
const dcDenied = "filter/darkpanel-docker-deny#"

// dcInspect is the part of a container's inspect output that Facts needs.
type dcInspect struct {
	ID     string `json:"Id"`
	Name   string
	State  struct{ Running bool }
	Config struct{ Labels map[string]string }

	NetworkSettings struct {
		Ports map[string][]struct {
			HostIP   string `json:"HostIp"`
			HostPort string
		}
		Networks map[string]struct{ IPAddress, GlobalIPv6Address string }
	}
}

// dcNet is the part of a network's inspect output that Facts needs.
type dcNet struct {
	ID         string `json:"Id"`
	Name       string
	Driver     string
	EnableIPv6 bool
	IPAM       struct{ Config []struct{ Subnet string } }
	Options    map[string]string
}

func dcAddr(s string) netip.Addr {
	if s == "" {
		return netip.IPv4Unspecified()
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Addr{}
	}
	return a
}

// dcFacts reads what the module root would read from Docker for one capture
// scenario: its bridge networks and running containers.
func dcFacts(t *testing.T, image, scenario string) Facts {
	t.Helper()
	var nets []dcNet
	if err := json.Unmarshal(capFile(t, image, scenario, "networks.json"), &nets); err != nil {
		t.Fatalf("%s/%s networks.json: %v", image, scenario, err)
	}
	var cts []dcInspect
	if err := json.Unmarshal(capFile(t, image, scenario, "containers.json"), &cts); err != nil {
		t.Fatalf("%s/%s containers.json: %v", image, scenario, err)
	}
	var f Facts
	bridges := map[string]string{}
	for _, n := range nets {
		if n.Driver != "bridge" {
			continue
		}
		bridge := n.Options["com.docker.network.bridge.name"]
		switch {
		case bridge != "":
		case n.Name == "bridge":
			bridge = "docker0"
		default:
			bridge = "br-" + n.ID[:12]
		}
		net := Network{
			ID: n.ID, Name: n.Name, Driver: n.Driver, Bridge: bridge, IPv6: n.EnableIPv6,
			GatewayModeV4: n.Options["com.docker.network.bridge.gateway_mode_ipv4"],
			GatewayModeV6: n.Options["com.docker.network.bridge.gateway_mode_ipv6"],
		}
		for _, c := range n.IPAM.Config {
			if p, err := netip.ParsePrefix(c.Subnet); err == nil {
				net.Subnets = append(net.Subnets, p)
			}
		}
		f.Networks = append(f.Networks, net)
		bridges[n.Name] = bridge
	}
	sort.Slice(f.Networks, func(i, j int) bool { return f.Networks[i].Name < f.Networks[j].Name })
	for _, c := range cts {
		if !c.State.Running {
			continue
		}
		ct := Container{
			ID: c.ID, Name: strings.TrimPrefix(c.Name, "/"),
			Project: c.Config.Labels["com.docker.compose.project"], Service: c.Config.Labels["com.docker.compose.service"],
		}
		for key, bs := range c.NetworkSettings.Ports {
			port, proto, _ := strings.Cut(key, "/")
			cport, err := strconv.ParseUint(port, 10, 16)
			if err != nil {
				t.Fatalf("%s: published port key %q", ct.Name, key)
			}
			for _, b := range bs {
				hport, err := strconv.ParseUint(b.HostPort, 10, 16)
				if err != nil {
					t.Fatalf("%s: host port %q", ct.Name, b.HostPort)
				}
				ct.Ports = append(ct.Ports, Published{HostAddr: dcAddr(b.HostIP), HostPort: uint16(hport), ContainerPort: uint16(cport), Proto: proto})
			}
		}
		sort.Slice(ct.Ports, func(i, j int) bool {
			a, b := ct.Ports[i], ct.Ports[j]
			if a.HostPort != b.HostPort {
				return a.HostPort < b.HostPort
			}
			return a.HostAddr.Less(b.HostAddr)
		})
		for name, e := range c.NetworkSettings.Networks {
			if bridge, ok := bridges[name]; ok {
				// An endpoint without an address in a family has the zero
				// address there, unlike a host address, where empty means all.
				a4, _ := netip.ParseAddr(e.IPAddress)
				a6, _ := netip.ParseAddr(e.GlobalIPv6Address)
				ct.Endpoints = append(ct.Endpoints, Endpoint{Network: name, Bridge: bridge, Addr4: a4, Addr6: a6})
			}
		}
		sort.Slice(ct.Endpoints, func(i, j int) bool { return ct.Endpoints[i].Network < ct.Endpoints[j].Network })
		f.Containers = append(f.Containers, ct)
	}
	sort.Slice(f.Containers, func(i, j int) bool { return f.Containers[i].Name < f.Containers[j].Name })
	return f
}

// dcWorld is one capture scenario: its facts, the name of the interface
// that faces the internet and the loaded rules of each family.
type dcWorld struct {
	facts  Facts
	uplink string
	rs     map[fwstate.Family]*netfilter.Ruleset
}

func dcLoad(t *testing.T, image, scenario string) dcWorld {
	t.Helper()
	w := dcWorld{facts: dcFacts(t, image, scenario), rs: capLoad(t, image, scenario)}
	have := map[string]bool{}
	var names []string
	for _, line := range strings.Fields(string(capFile(t, image, scenario, "interfaces.txt"))) {
		name, _, _ := strings.Cut(line, "@")
		have[name] = true
		names = append(names, name)
	}
	for _, n := range w.facts.Networks {
		if !have[n.Bridge] {
			t.Errorf("%s/%s: network %s has bridge %s, which interfaces.txt does not list", image, scenario, n.Name, n.Bridge)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		isBridge := slices.ContainsFunc(w.facts.Networks, func(n Network) bool { return n.Bridge == name })
		if name != "lo" && !isBridge && !strings.HasPrefix(name, "veth") && !strings.HasPrefix(name, "br-") {
			w.uplink = name
			break
		}
	}
	if w.uplink == "" {
		t.Fatalf("%s/%s: interfaces.txt lists no uplink interface: %q", image, scenario, names)
	}
	return w
}

// endpoint returns the container's address in the family and its bridge.
func (w dcWorld) endpoint(t *testing.T, name string, fam fwstate.Family) (netip.Addr, string) {
	t.Helper()
	for _, c := range w.facts.Containers {
		if c.Name != name {
			continue
		}
		for _, e := range c.Endpoints {
			if a := map[fwstate.Family]netip.Addr{fwstate.V4: e.Addr4, fwstate.V6: e.Addr6}[fam]; a.IsValid() {
				return a, e.Bridge
			}
		}
	}
	t.Fatalf("no running container %s with a %s address", name, fam)
	return netip.Addr{}, ""
}

// route is the router the evaluator asks once DNAT has rewritten a
// destination: a container's address is forwarded out of its bridge.
func (w dcWorld) route(p fwstate.Packet) (string, string, error) {
	for _, c := range w.facts.Containers {
		for _, e := range c.Endpoints {
			if p.Dst == e.Addr4 || p.Dst == e.Addr6 {
				return "forward", e.Bridge, nil
			}
		}
	}
	return "", "", fmt.Errorf("no route to %s", p.Dst)
}

// arrive is the first packet of a connection from src to a port of this
// host, arriving on the uplink.
func (w dcWorld) arrive(fam fwstate.Family, src string, port uint16) fwstate.Packet {
	host := map[fwstate.Family]string{fwstate.V4: "192.0.2.10", fwstate.V6: "2001:db8::10"}[fam]
	return fwstate.Packet{
		Family: fam, Path: "input", InIface: w.uplink, Src: netip.MustParseAddr(src), Dst: netip.MustParseAddr(host),
		SrcPort: 40000, DstPort: port, Proto: "tcp", CtState: "NEW",
	}
}

// leave is the first packet of a connection from a container to the
// internet.
func (w dcWorld) leave(t *testing.T, name string, fam fwstate.Family, dst string) fwstate.Packet {
	t.Helper()
	a, bridge := w.endpoint(t, name, fam)
	return fwstate.Packet{
		Family: fam, Path: "forward", InIface: bridge, OutIface: w.uplink, Src: a, Dst: netip.MustParseAddr(dst),
		SrcPort: 50000, DstPort: 443, Proto: "tcp", CtState: "NEW",
	}
}

// between is the first packet of a connection from a container to another
// address of its own bridge.
func (w dcWorld) between(t *testing.T, name string, fam fwstate.Family) fwstate.Packet {
	t.Helper()
	a, bridge := w.endpoint(t, name, fam)
	dst := a
	for range 40 {
		dst = dst.Next()
	}
	return fwstate.Packet{
		Family: fam, Path: "forward", InIface: bridge, OutIface: bridge, Src: a, Dst: dst,
		SrcPort: 50000, DstPort: 80, Proto: "tcp", CtState: "NEW",
	}
}

// into is a packet forwarded from the uplink straight to a container's port
// 80, with no DNAT.
func (w dcWorld) into(t *testing.T, name string, fam fwstate.Family, src, state string) fwstate.Packet {
	t.Helper()
	a, bridge := w.endpoint(t, name, fam)
	return fwstate.Packet{
		Family: fam, Path: "forward", InIface: w.uplink, OutIface: bridge, Src: netip.MustParseAddr(src), Dst: a,
		SrcPort: 40000, DstPort: 80, Proto: "tcp", CtState: state,
	}
}

// dcWithout returns the ruleset without one table.
func dcWithout(rs *netfilter.Ruleset, table string) *netfilter.Ruleset {
	out := &netfilter.Ruleset{LegacyPresent: rs.LegacyPresent}
	for _, tb := range rs.Tables {
		if tb.Name != table {
			out.Tables = append(out.Tables, tb)
		}
	}
	return out
}

// dcCase is one packet and what the capture decides for it with the block
// loaded and without it.
type dcCase struct {
	name string
	pkt  func(t *testing.T, w dcWorld) fwstate.Packet
	// noRaw evaluates without the raw table, whose protection of container
	// addresses would drop the packet before the block is reached. The
	// kernel reaches the container address of a reply only after raw has
	// run, so a reply is evaluated this way too.
	noRaw bool
	block fwstate.Verdict
	// base is the verdict in the base and cleanup captures, which hold no
	// block; "" skips them.
	base fwstate.Verdict
}

func (c dcCase) decide(t *testing.T, w dcWorld, want fwstate.Verdict, reason string) {
	t.Helper()
	p := c.pkt(t, w)
	rs := w.rs[p.Family]
	if c.noRaw {
		rs = dcWithout(rs, "raw")
	}
	got := eval.Evaluate(rs, p, w.route)
	if got.Verdict != want {
		t.Errorf("verdict %q (reason %q, trace %q), want %q", got.Verdict, got.Reason, got.Trace, want)
	}
	if want == fwstate.Drop && !strings.Contains(got.Reason, reason) {
		t.Errorf("reason %q does not contain %q", got.Reason, reason)
	}
}

// dcRequireBlock fails unless the capture holds the block's chains loaded.
func dcRequireBlock(t *testing.T, w dcWorld) {
	t.Helper()
	for _, fam := range []fwstate.Family{fwstate.V4, fwstate.V6} {
		f := w.rs[fam].Table("filter")
		if f == nil || f.Chain("darkpanel-docker-deny") == nil || f.Chain("DOCKER-USER") == nil || len(f.Chain("DOCKER-USER").Rules) < 2 {
			t.Fatalf("the %s filter table of the block capture does not hold the block", fam)
		}
	}
}

func TestDcDecisions(t *testing.T) {
	v4, v6 := fwstate.V4, fwstate.V6
	cases := []dcCase{
		{
			name:  "a published port is open to a source its binding allows",
			pkt:   func(_ *testing.T, w dcWorld) fwstate.Packet { return w.arrive(v4, "198.51.100.7", 8080) },
			block: fwstate.Accept, base: fwstate.Accept,
		},
		{
			name:  "the binding's explicit deny closes it to its range",
			pkt:   func(_ *testing.T, w dcWorld) fwstate.Packet { return w.arrive(v4, "203.0.113.9", 8080) },
			block: fwstate.Drop, base: fwstate.Accept,
		},
		{
			name:  "a published port without a binding is closed to the internet",
			pkt:   func(_ *testing.T, w dcWorld) fwstate.Packet { return w.arrive(v4, "198.51.100.7", 9090) },
			block: fwstate.Drop, base: fwstate.Accept,
		},
		{
			name:  "a private source reaches a published port without a binding, 10/8",
			pkt:   func(_ *testing.T, w dcWorld) fwstate.Packet { return w.arrive(v4, "10.9.9.9", 9090) },
			block: fwstate.Accept, base: fwstate.Accept,
		},
		{
			name:  "a private source reaches a published port without a binding, 192.168/16",
			pkt:   func(_ *testing.T, w dcWorld) fwstate.Packet { return w.arrive(v4, "192.168.7.7", 9090) },
			block: fwstate.Accept, base: fwstate.Accept,
		},
		{
			name:  "a tailnet source reaches a published port without a binding",
			pkt:   func(_ *testing.T, w dcWorld) fwstate.Packet { return w.arrive(v4, "100.64.5.5", 9090) },
			block: fwstate.Accept, base: fwstate.Accept,
		},
		{
			name:  "a container reaches the internet from the default bridge",
			pkt:   func(t *testing.T, w dcWorld) fwstate.Packet { return w.leave(t, "web", v4, "198.51.100.2") },
			block: fwstate.Accept, base: fwstate.Accept,
		},
		{
			name:  "a container reaches the internet from a custom-named bridge",
			pkt:   func(t *testing.T, w dcWorld) fwstate.Packet { return w.leave(t, "app", v4, "198.51.100.2") },
			block: fwstate.Accept, base: fwstate.Accept,
		},
		{
			name:  "two containers talk on the default bridge",
			pkt:   func(t *testing.T, w dcWorld) fwstate.Packet { return w.between(t, "web", v4) },
			block: fwstate.Accept, base: fwstate.Accept,
		},
		{
			name:  "two containers talk on a custom-named bridge",
			pkt:   func(t *testing.T, w dcWorld) fwstate.Packet { return w.between(t, "app", v4) },
			block: fwstate.Accept, base: fwstate.Accept,
		},
		{
			name: "replies to a container's connection return first",
			pkt: func(t *testing.T, w dcWorld) fwstate.Packet {
				return w.into(t, "web", v4, "198.51.100.7", "ESTABLISHED")
			},
			noRaw: true, block: fwstate.Accept, base: fwstate.Accept,
		},
		{
			name:  "a new connection forwarded without DNAT to the default bridge is denied",
			pkt:   func(t *testing.T, w dcWorld) fwstate.Packet { return w.into(t, "web", v4, "198.51.100.7", "NEW") },
			noRaw: true, block: fwstate.Drop, base: fwstate.Accept,
		},
		{
			name:  "a new connection forwarded without DNAT to a custom-named bridge is denied",
			pkt:   func(t *testing.T, w dcWorld) fwstate.Packet { return w.into(t, "app", v4, "198.51.100.7", "NEW") },
			noRaw: true, block: fwstate.Drop, base: fwstate.Accept,
		},
		{
			name:  "an IPv6 published port without a binding is closed to the internet",
			pkt:   func(_ *testing.T, w dcWorld) fwstate.Packet { return w.arrive(v6, "2001:db8:1::7", 8443) },
			block: fwstate.Drop, base: fwstate.Accept,
		},
		{
			name:  "a private IPv6 source reaches a published port without a binding",
			pkt:   func(_ *testing.T, w dcWorld) fwstate.Packet { return w.arrive(v6, "fc00::5", 8443) },
			block: fwstate.Accept, base: fwstate.Accept,
		},
		{
			name:  "a container reaches the internet over IPv6",
			pkt:   func(t *testing.T, w dcWorld) fwstate.Packet { return w.leave(t, "six", v6, "2001:db8:2::2") },
			block: fwstate.Accept, base: fwstate.Accept,
		},
	}
	for _, image := range capImages {
		block := dcLoad(t, image, "block")
		dcRequireBlock(t, block)
		bases := map[string]dcWorld{"base": dcLoad(t, image, "base"), "cleanup": dcLoad(t, image, "cleanup")}
		for _, c := range cases {
			t.Run(image+"/"+c.name, func(t *testing.T) {
				c.decide(t, block, c.block, dcDenied)
				for name, w := range bases {
					if c.base != "" {
						t.Run(name, func(t *testing.T) { c.decide(t, w, c.base, "") })
					}
				}
			})
		}
	}
}

// dcConfig reads the configured state of a capture scenario's UFW: its
// rule files and defaults, with a minimal before.rules and ufw.conf, which
// the chains compared below do not depend on.
func dcConfig(t *testing.T, image, scenario string) fwstate.Config {
	t.Helper()
	root := t.TempDir()
	put := func(path string, data []byte) {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{"etc/ufw/user.rules", "etc/ufw/user6.rules", "etc/ufw/after.rules", "etc/ufw/after6.rules", "etc/default/ufw"} {
		put(p, capFile(t, image, scenario, "files/"+p))
	}
	for _, p := range []string{"etc/ufw/before.rules", "etc/ufw/before6.rules"} {
		put(p, []byte("*filter\nCOMMIT\n"))
	}
	put("etc/ufw/ufw.conf", []byte("ENABLED=yes\nLOGLEVEL=low\n"))
	cfg, err := conf.Read(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Unsupported) > 0 {
		t.Fatalf("%s/%s: the configuration is unsupported: %q", image, scenario, cfg.Unsupported)
	}
	return cfg
}

func TestDcPrediction(t *testing.T) {
	keep := func(_ fwstate.Family, table, chain string) bool {
		return table == "filter" && (Owned(chain) || chain == "ufw-user-forward" || chain == "ufw6-user-forward")
	}
	for _, image := range capImages {
		t.Run(image, func(t *testing.T) {
			block := dcLoad(t, image, "block")
			dcRequireBlock(t, block)
			base := dcLoad(t, image, "base")
			cur := fwstate.Loaded{Variant: "capture", V4: base.rs[fwstate.V4], V6: base.rs[fwstate.V6]}
			got, err := predict.Predict(dcConfig(t, image, "block"), cur, fwstate.TransReload)
			if err != nil {
				t.Fatal(err)
			}
			want := fwstate.Loaded{Variant: "capture", V4: block.rs[fwstate.V4], V6: block.rs[fwstate.V6]}
			for _, d := range loaded.Compare(got, want, keep) {
				t.Errorf("predicted and captured %s %s/%s differ: only predicted %q, only captured %q, order differs %v, policy %q/%q",
					d.Family, d.Table, d.Chain, d.OnlyA, d.OnlyB, d.OrderDiffers, d.PolicyA, d.PolicyB)
			}
		})
	}
}

// dcConfigured returns the rules of the capture's user.rules and
// user6.rules, merged as wave 1's status lists them.
func dcConfigured(t *testing.T, image, scenario string) []model.Rule {
	t.Helper()
	var v4, v6 []model.Tuple
	for _, f := range []struct {
		path string
		v6   bool
		dst  *[]model.Tuple
	}{{"files/etc/ufw/user.rules", false, &v4}, {"files/etc/ufw/user6.rules", true, &v6}} {
		for _, line := range strings.Split(string(capFile(t, image, scenario, f.path)), "\n") {
			if !strings.HasPrefix(line, model.TuplePrefix) {
				continue
			}
			tp, err := model.ParseTuple(line, f.v6)
			if err != nil {
				t.Fatalf("%s: %v", f.path, err)
			}
			*f.dst = append(*f.dst, tp)
		}
	}
	return model.Merge(v4, v6)
}

// dcTuple is a rule without what UFW and the caller assign: its id and raw
// text.
func dcTuple(r model.Rule) model.Rule {
	r = r.Normalize()
	r.ID, r.Raw = "", ""
	return r
}

// dcChains returns the rule keys of every chain of a block's filter table.
func dcChains(t *testing.T, block []byte, fam fwstate.Family) map[string][]string {
	t.Helper()
	rs, err := BlockRules(block)
	if err != nil {
		t.Fatal(err)
	}
	f := rs.Table("filter")
	if f == nil {
		t.Fatal("the block has no filter table")
	}
	out := map[string][]string{}
	for _, c := range f.Chains {
		keys := []string{}
		for _, r := range c.Rules {
			keys = append(keys, netfilter.Key(r, fam == fwstate.V6))
		}
		out[c.Name] = keys
	}
	return out
}

func TestDcRealize(t *testing.T) {
	web := Binding{Selector: "web", HostPort: 8080, Proto: "tcp"}
	rows := []ContainerRule{
		{ID: dcRowAllow, Binding: web, Kind: Allow},
		{ID: dcRowDeny, Binding: web, Kind: Deny, From: netip.MustParsePrefix("203.0.113.0/24")},
	}
	for _, image := range capImages {
		t.Run(image, func(t *testing.T) {
			got := Realize(rows, dcFacts(t, image, "block"), DefaultSettings())

			var captured []model.Rule
			for _, r := range dcConfigured(t, image, "block") {
				if _, ok := RuleOf(r.Comment); ok && r.Origin == model.OriginDarkpanel {
					captured = append(captured, r)
				}
			}
			if len(captured) != 2 {
				t.Fatalf("the capture's user rules hold %d container rules, want 2", len(captured))
			}
			index := func(c model.Rule) int {
				return slices.IndexFunc(got.Rules, func(r model.Rule) bool { return dcTuple(r) == dcTuple(c) })
			}
			for _, c := range captured {
				if index(c) < 0 {
					t.Errorf("Realize does not make the rule UFW stored in the capture: %+v\nit makes %+v", dcTuple(c), got.Rules)
				}
			}
			if a, d := index(captured[0]), index(captured[1]); a >= 0 && d >= 0 && (captured[0].Action != model.ActionAllow || captured[1].Action != model.ActionDeny || a > d) {
				t.Errorf("the route deny must follow the route allow: captured %s then %s, Realize puts them at %d and %d",
					captured[0].Action, captured[1].Action, a, d)
			}
			for _, r := range got.Rules {
				if !slices.ContainsFunc(captured, func(c model.Rule) bool { return dcTuple(c) == dcTuple(r) }) && (r.Direction != model.DirIn || r.Family != model.FamilyV6) {
					t.Errorf("Realize makes %+v, which is neither in the capture nor an IPv6 rule for a proxied family", r)
				}
			}

			for _, fam := range []fwstate.Family{fwstate.V4, fwstate.V6} {
				file := map[fwstate.Family]string{fwstate.V4: "after.rules", fwstate.V6: "after6.rules"}[fam]
				text, ok, err := BlockOf(capFile(t, image, "block", "files/etc/ufw/"+file))
				if err != nil || !ok {
					t.Fatalf("%s holds no block: %v", file, err)
				}
				want, have := dcChains(t, text, fam), dcChains(t, Render(got.Block, fam), fam)
				names := []string{ChainDockerUser, ChainBindings, ChainDeny}
				if fam == fwstate.V4 {
					names = append(names, web.Chain())
				}
				for _, name := range names {
					if len(want[name]) == 0 {
						t.Errorf("%s: the captured block has no rules in %s", fam, name)
					}
					if !slices.Equal(have[name], want[name]) {
						t.Errorf("%s %s: Render gives\n%s\nthe captured block holds\n%s", fam, name, strings.Join(have[name], "\n"), strings.Join(want[name], "\n"))
					}
				}
			}
		})
	}
}
