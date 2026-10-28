package firewall

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"reflect"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/firewall/dockerfw"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/modules/firewall/model"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
	"github.com/darkraise/darkpanel/internal/platform/netstat"
)

const (
	dstAfter4   = "/etc/ufw/after.rules"
	dstAfter6   = "/etc/ufw/after6.rules"
	dstForeign4 = "-A DOCKER-USER -s 203.0.113.9/32 -j DROP\n"
)

func dstPrefix(s string) netip.Prefix { return netip.MustParsePrefix(s) }
func dstAddr(s string) netip.Addr     { return netip.MustParseAddr(s) }

// dstDet is a supported detection: Docker 29.9.1 and UFW both on nft, the
// iptables driver and the raw protection loaded.
var dstDet = dockerfw.Detection{Version: "29.9.1", Driver: "iptables", DockerVariant: "nft", UFWVariant: "nft", RawProtection: true}

// dstState is an available Docker with the default settings and no rows.
func dstState() dockerState {
	return dockerState{available: true, det: dstDet, cfg: dockerConfig{Settings: dockerfw.DefaultSettings()}}
}

// dstConfig is a UFW configuration with after files; withBlock puts the
// default block in both.
func dstConfig(t *testing.T, withBlock bool) fwstate.Config {
	t.Helper()
	files := map[string]core.RestoreEntry{}
	for path, f := range map[string]fwstate.Family{dstAfter4: fwstate.V4, dstAfter6: fwstate.V6} {
		data := []byte("*filter\n# after\nCOMMIT\n")
		if withBlock {
			var err error
			data, err = dockerfw.WithBlock(data, dockerfw.Render(dockerfw.BlockInput{Settings: dockerfw.DefaultSettings()}, f))
			if err != nil {
				t.Fatal(err)
			}
		}
		files[path] = core.RestoreEntry{Path: path, Present: true, Mode: 0o640, Data: data}
	}
	return fwstate.Config{Enabled: true, IPv6: true, DefaultInput: "DROP", Files: files}
}

func dstParse(t *testing.T, text string) *netfilter.Ruleset {
	t.Helper()
	return fhParse(t, text)
}

// dstLoaded is the loaded state: the block's rules in DOCKER-USER, the
// foreign IPv4 lines after them and, for IPv4 only, the extra tables' text.
func dstLoaded(t *testing.T, foreign4, extra4 string) fwstate.Loaded {
	t.Helper()
	text := func(f fwstate.Family, foreign, extra string) string {
		block := string(dockerfw.Render(dockerfw.BlockInput{Settings: dockerfw.DefaultSettings()}, f))
		return strings.Replace(block, "COMMIT\n", foreign+"COMMIT\n", 1) + extra
	}
	return fwstate.Loaded{Variant: "nft", V4: dstParse(t, text(fwstate.V4, foreign4, extra4)), V6: dstParse(t, text(fwstate.V6, "", ""))}
}

func TestDstUnavailable(t *testing.T) {
	st := dockerState{reason: "the Docker socket does not exist", cfg: dockerConfig{Settings: dockerfw.DefaultSettings()}}
	ds := dockerStatusOf(dstConfig(t, false), dstLoaded(t, "", ""), st)
	if ds.Available || ds.Reason != st.reason || ds.Supported || ds.Enabled || ds.Health != "unsupported" {
		t.Errorf("status %+v, want unavailable, unsupported and off with the reason", ds)
	}
	if ds.Version != "" || ds.Driver != "" || len(ds.Unsupported) != 0 {
		t.Errorf("status %+v carries detection of a daemon that is not there", ds)
	}
	raw, err := json.Marshal(ds)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"networks":[]`, `"rules":[]`, `"bindings":[]`, `"privateSources":[`, `"forwardExemptions":[]`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("json %s lacks %s", raw, want)
		}
	}
}

func TestDstUnsupported(t *testing.T) {
	st := dstState()
	st.det.Version = "27.5.1"
	st.cfg.Enabled = true
	ds := dockerStatusOf(dstConfig(t, true), dstLoaded(t, "", ""), st)
	want := dockerfw.Unsupported(st.det)
	if len(want) == 0 {
		t.Fatal("Docker 27.5.1 is supported")
	}
	if !ds.Available || ds.Supported || !reflect.DeepEqual(ds.Unsupported, want) || ds.Health != "unsupported" {
		t.Errorf("status %+v, want the reasons %q and health unsupported even with the flag on", ds, want)
	}
	if ds.Version != "27.5.1" || ds.Driver != "iptables" || ds.DockerVariant != "nft" || ds.UFWVariant != "nft" || !ds.RawProtection || !ds.Enabled {
		t.Errorf("status %+v does not carry the detection", ds)
	}
	if len(ds.Issues) != 0 {
		t.Errorf("issues %+v on an unsupported host", ds.Issues)
	}
}

func TestDstHealthLevels(t *testing.T) {
	broken := dockerfw.Issue{Level: dockerfw.Broken, Reason: "b"}
	degraded := dockerfw.Issue{Level: dockerfw.Degraded, Reason: "d"}
	for _, tt := range []struct {
		name               string
		supported, enabled bool
		issues             []dockerfw.Issue
		want               string
	}{
		{"unsupported wins", false, true, []dockerfw.Issue{broken}, "unsupported"},
		{"off wins over issues", true, false, []dockerfw.Issue{broken}, "off"},
		{"no issues", true, true, nil, "ok"},
		{"a degraded issue", true, true, []dockerfw.Issue{degraded}, "degraded"},
		{"degraded then broken", true, true, []dockerfw.Issue{degraded, broken}, "broken"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := dockerHealthLevel(tt.supported, tt.enabled, tt.issues); got != tt.want {
				t.Errorf("health %q, want %q", got, tt.want)
			}
		})
	}
}

// TestDstHealthBlockMissing: with the integration on and UFW enabled, a
// block missing from the files is health's first issue, and the status is
// broken with the issues listed; with the flag off the status is off.
func TestDstHealthBlockMissing(t *testing.T) {
	st := dstState()
	st.cfg.Enabled = true
	cfg := dstConfig(t, false)
	cur := dstLoaded(t, "", "")
	ds := dockerStatusOf(cfg, cur, st)
	if !ds.Supported || ds.Health != "broken" || len(ds.Issues) == 0 || ds.Issues[0].Level != dockerfw.Broken {
		t.Fatalf("status %+v, want supported and broken with a broken first issue", ds)
	}
	want := dockerfw.Health(dstHealthWant(cfg, cur, st))
	if !reflect.DeepEqual(ds.Issues, want) {
		t.Errorf("issues %+v, want what dockerfw.Health reports for the input of Contracts \"dockerfw health\": %+v", ds.Issues, want)
	}
	st.cfg.Enabled = false
	if off := dockerStatusOf(cfg, cur, st); off.Health != "off" || off.Enabled {
		t.Errorf("status %+v, want off", off)
	}
}

// dstHealthWant is the HealthInput of Contracts "dockerfw health" for the
// block-missing case, written out independently of the implementation.
func dstHealthWant(cfg fwstate.Config, cur fwstate.Loaded, st dockerState) dockerfw.HealthInput {
	in := dockerfw.HealthInput{
		Want:  true,
		Files: map[fwstate.Family][]byte{fwstate.V4: cfg.Files[dstAfter4].Data, fwstate.V6: cfg.Files[dstAfter6].Data},
		Expected: map[fwstate.Family][]byte{
			fwstate.V4: dockerfw.Render(st.realized.Block, fwstate.V4), fwstate.V6: dockerfw.Render(st.realized.Block, fwstate.V6),
		},
		Loaded: map[fwstate.Family]*netfilter.Ruleset{fwstate.V4: cur.V4, fwstate.V6: cur.V6},
		IPv6:   cfg.IPv6, DefaultInput: cfg.DefaultInput, Facts: st.facts, Settings: st.cfg.Settings,
	}
	return in
}

// TestDstHealthInterfaces: the host's interfaces and the loaded nat table
// reach health, so a br-* link that is no Docker network's bridge and a
// foreign DNAT forward without an exemption are both reported.
func TestDstHealthInterfaces(t *testing.T) {
	st := dstState()
	st.cfg.Enabled = true
	st.interfaces = []netstat.Interface{{Index: 1, Name: "lo"}, {Index: 7, Name: "br-lan"}}
	ds := dockerStatusOf(dstConfig(t, true), dstLoaded(t, "", dstNat), st)
	wantBridge := dockerfw.Issue{Level: dockerfw.Degraded, Reason: "interface br-lan is a br-* bridge that is not a Docker network's bridge"}
	var bridge, forward bool
	for _, is := range ds.Issues {
		bridge = bridge || is == wantBridge
		forward = forward || (is.Level == dockerfw.Degraded && strings.HasPrefix(is.Reason, "foreign DNAT forward has no exemption (IPv4)"))
	}
	if !bridge || !forward {
		t.Errorf("issues %+v, want the br-lan bridge issue (%t) and the foreign DNAT issue (%t)", ds.Issues, bridge, forward)
	}
}

func TestDstForeignRules(t *testing.T) {
	st := dstState()
	cfg := dstConfig(t, true)
	if ds := dockerStatusOf(cfg, dstLoaded(t, "", ""), st); len(ds.ForeignRules) != 0 {
		t.Errorf("foreign rules %q beside the block alone", ds.ForeignRules)
	}
	ds := dockerStatusOf(cfg, dstLoaded(t, dstForeign4, ""), st)
	if want := []string{"v4 -A DOCKER-USER -s 203.0.113.9/32 -j DROP"}; !reflect.DeepEqual(ds.ForeignRules, want) {
		t.Errorf("foreign rules %q, want %q", ds.ForeignRules, want)
	}
}

const dstNat = `*nat
:PREROUTING ACCEPT [0:0]
:DOCKER - [0:0]
-A PREROUTING -m addrtype --dst-type LOCAL -j DOCKER
-A PREROUTING -d 192.0.2.10/32 -p tcp -m tcp --dport 2222 -j DNAT --to-destination 10.9.0.5:22
-A DOCKER ! -i docker0 -p tcp -m tcp --dport 8080 -j DNAT --to-destination 172.17.0.2:80
COMMIT
`

// TestDstForeignForwards: a DNAT rule outside Docker's chains is proposed as
// an exemption until the settings cover it; Docker's own DNAT is not.
func TestDstForeignForwards(t *testing.T) {
	cfg := dstConfig(t, true)
	cur := dstLoaded(t, "", dstNat)
	rules := dockerfw.ForeignDNAT(cur.V4.Table("nat"))
	if len(rules) != 1 {
		t.Fatalf("ForeignDNAT lists %d rules, want the one outside Docker's chains", len(rules))
	}
	want, ok := dockerfw.Propose(rules[0], fwstate.V4)
	if !ok {
		t.Fatal("the test's foreign forward cannot be proposed as an exemption")
	}
	st := dstState()
	if ds := dockerStatusOf(cfg, cur, st); !reflect.DeepEqual(ds.ForeignForwards, []dockerfw.Exemption{want}) {
		t.Errorf("foreign forwards %+v, want %+v", ds.ForeignForwards, want)
	}
	st.cfg.Settings.ForwardExemptions = []dockerfw.Exemption{want}
	if ds := dockerStatusOf(cfg, cur, st); len(ds.ForeignForwards) != 0 {
		t.Errorf("foreign forwards %+v after the exemption covers it", ds.ForeignForwards)
	}
}

func TestDstNetworks(t *testing.T) {
	st := dstState()
	st.facts.Networks = []dockerfw.Network{
		{ID: "b1", Name: "bridge", Driver: "bridge", Bridge: "docker0", Subnets: []netip.Prefix{dstPrefix("172.17.0.0/16")}},
		{ID: "h1", Name: "host", Driver: "host"},
		{ID: "r1", Name: "routednet", Driver: "bridge", Bridge: "br-r1r1r1r1r1r1", GatewayModeV4: "routed",
			Subnets: []netip.Prefix{dstPrefix("198.51.100.0/24"), dstPrefix("fd00:dead::/64")}},
	}
	ds := dockerStatusOf(dstConfig(t, true), dstLoaded(t, "", ""), st)
	routedReason, unprotected := dockerfw.Unprotected(st.facts.Networks[2], st.cfg.Settings)
	if !unprotected || routedReason == "" {
		t.Fatal("a routed network is not unprotected")
	}
	want := []DockerNetwork{
		{Name: "bridge", ID: "b1", Bridge: "docker0", Subnets: []string{"172.17.0.0/16"}, Protected: true},
		{Name: "routednet", ID: "r1", Bridge: "br-r1r1r1r1r1r1", Subnets: []string{"198.51.100.0/24", "fd00:dead::/64"}, Protected: false, Reason: routedReason},
	}
	if !reflect.DeepEqual(ds.Networks, want) {
		t.Errorf("networks %+v, want %+v", ds.Networks, want)
	}
}

func TestDstManualRoutes(t *testing.T) {
	tuple := func(line string) model.Tuple {
		tp, err := model.ParseTuple(line, false)
		if err != nil {
			t.Fatal(err)
		}
		return tp
	}
	cfg := dstConfig(t, true)
	cfg.Rules4 = []model.Tuple{
		tuple("### tuple ### route:allow tcp 80 172.17.0.2 any 0.0.0.0/0 in_eth0!out_docker0"),
		tuple("### tuple ### route:allow tcp 25 203.0.113.7 any 0.0.0.0/0 in_eth0!out_eth1"),
		tuple("### tuple ### route:allow any any 0.0.0.0/0 any 0.0.0.0/0 in_eth0!out_eth1"),
		tuple("### tuple ### allow tcp 80 172.17.0.2 any 0.0.0.0/0 in"),
	}
	st := dstState()
	st.facts.Networks = []dockerfw.Network{{ID: "b1", Name: "bridge", Driver: "bridge", Bridge: "docker0", Subnets: []netip.Prefix{dstPrefix("172.17.0.0/16")}}}
	ds := dockerStatusOf(cfg, dstLoaded(t, "", ""), st)
	if len(ds.ManualRoutes) != 1 || !strings.Contains(ds.ManualRoutes[0], "172.17.0.2") || !strings.Contains(ds.ManualRoutes[0], "(manual)") {
		t.Errorf("manual routes %q, want only the manual route rule to 172.17.0.2", ds.ManualRoutes)
	}
}

func TestDstBindings(t *testing.T) {
	web := dockerfw.Container{ID: "c1", Name: "web",
		Ports: []dockerfw.Published{
			{HostAddr: dstAddr("0.0.0.0"), HostPort: 8080, ContainerPort: 80, Proto: "tcp"},
			{HostAddr: dstAddr("::"), HostPort: 8080, ContainerPort: 80, Proto: "tcp"},
		},
		Endpoints: []dockerfw.Endpoint{{Network: "bridge", Bridge: "docker0", Addr4: dstAddr("172.17.0.2")}}}
	api := dockerfw.Container{ID: "c2", Name: "shop-api-1", Project: "shop", Service: "api",
		Ports:     []dockerfw.Published{{HostAddr: dstAddr("0.0.0.0"), HostPort: 9090, ContainerPort: 8000, Proto: "tcp"}},
		Endpoints: []dockerfw.Endpoint{{Network: "shop", Bridge: "br-s", Addr4: dstAddr("172.30.0.2"), Addr6: dstAddr("fd00:dead::2")}}}
	bWeb := dockerfw.Binding{Selector: "web", HostPort: 8080, Proto: "tcp"}
	bAPI := dockerfw.Binding{Selector: "shop/api", HostPort: 9090, Proto: "tcp"}
	bGone := dockerfw.Binding{Selector: "gone", HostAddr: dstAddr("192.0.2.10"), HostPort: 5000, Proto: "tcp"}
	rows := []dockerfw.ContainerRule{
		{ID: "01J00000000000000000000001", Binding: bWeb, Kind: dockerfw.Allow},
		{ID: "01J00000000000000000000002", Binding: bWeb, Kind: dockerfw.Deny, From: dstPrefix("203.0.113.0/24")},
		{ID: "01J00000000000000000000003", Binding: bAPI, Kind: dockerfw.Allow, From: dstPrefix("198.51.100.0/24")},
		{ID: "01J00000000000000000000004", Binding: bAPI, Kind: dockerfw.Allow, From: dstPrefix("10.0.0.0/8")},
		{ID: "01J00000000000000000000005", Binding: bGone, Kind: dockerfw.Allow},
	}
	st := dstState()
	st.rows = rows
	st.facts.Containers = []dockerfw.Container{api, web}
	st.realized.Block.Lines = []dockerfw.BindingLine{
		{Proto: "tcp", ContainerAddr: dstAddr("172.17.0.2"), ContainerPort: 80, HostPort: 8080, Chain: bWeb.Chain()},
		{Proto: "tcp", ContainerAddr: dstAddr("172.30.0.2"), ContainerPort: 8000, HostPort: 9090, Chain: bAPI.Chain()},
		{Proto: "tcp", ContainerAddr: dstAddr("fd00:dead::2"), ContainerPort: 8000, HostPort: 9090, Chain: bAPI.Chain()},
	}
	st.realized.Rules = []model.Rule{{
		Action: model.ActionAllow, Direction: model.DirIn, Proto: model.ProtoTCP, From: model.Any, To: model.Any,
		ToPort: "8080", Family: model.FamilyV6, Comment: "ctr 01J00000000000000000000001",
	}}
	ds := dockerStatusOf(dstConfig(t, true), dstLoaded(t, "", ""), st)

	want := []DockerBinding{
		{Binding: bWeb, ID: bWeb.ID(), Containers: []string{"web"}, ContainerPort: 80,
			Allowed: []string{"any"}, Denied: []string{"203.0.113.0/24"}, Proxied: []string{"v6"}},
		{Binding: bAPI, ID: bAPI.ID(), Containers: []string{"shop-api-1"}, ContainerPort: 8000,
			Allowed: []string{"198.51.100.0/24", "10.0.0.0/8"}, Denied: []string{},
			PrivateReach: []string{"172.16.0.0/12", "192.168.0.0/16", "100.64.0.0/10", "fc00::/7", "fe80::/10"}},
		{Binding: bGone, ID: bGone.ID(), Containers: []string{}, Allowed: []string{"any"}, Denied: []string{}},
	}
	if !reflect.DeepEqual(ds.Bindings, want) {
		t.Errorf("bindings\n got %+v\nwant %+v", ds.Bindings, want)
	}
	if !reflect.DeepEqual(ds.Rules, rows) {
		t.Errorf("rules %+v, want the stored rows", ds.Rules)
	}
}

// dstDeps is a deps over the fake host with a Docker daemon the test
// scripts; the variant reads find no chains, the sockets and the interfaces
// hold only loopback, and routes are not scripted.
func dstDeps(h *fakeHost, d *fakeDocker) deps {
	out := h.deps()
	out.docker = d
	out.readVariant = func(context.Context, core.Env, fwstate.Family, string, []string) (*netfilter.Ruleset, error) {
		return nil, nil
	}
	out.sockets = func() ([]netstat.Socket, error) { return nil, nil }
	out.interfaces = func() ([]netstat.Interface, error) {
		return []netstat.Interface{{Index: 1, Name: "lo", Up: true, Running: true, Loopback: true}}, nil
	}
	out.routeGet = func(netstat.RouteQuery) (netstat.Route, error) {
		return netstat.Route{}, errors.New("routes are not scripted")
	}
	out.shieldsUp = func(context.Context) (bool, bool, error) { return false, false, nil }
	return out
}

// TestDstResource: the resource reads the host and the Docker facts into
// the status, and is registered as firewall.docker.
func TestDstResource(t *testing.T) {
	ctx := context.Background()
	h := newFakeHost(t)
	f := fw{d: dstDeps(h, newFakeDocker())}
	rd := f.dockerResource()
	if rd.Name != "firewall.docker" {
		t.Fatalf("resource %q, want firewall.docker", rd.Name)
	}
	raw, err := rd.Read(ctx, h.env())
	if err != nil {
		t.Fatal(err)
	}
	var ds DockerStatus
	if err := json.Unmarshal(raw, &ds); err != nil {
		t.Fatal(err)
	}
	if !ds.Available || ds.Version != "29.9.1" || ds.Driver != "iptables" || ds.Enabled {
		t.Errorf("status %+v, want Docker 29.9.1 on the iptables driver with the integration off", ds)
	}
	if ds.Supported || ds.Health != "unsupported" || len(ds.Unsupported) == 0 {
		t.Errorf("status %+v, want unsupported: no variant holds Docker's chains and no raw protection is loaded", ds)
	}
	if !reflect.DeepEqual(ds.Settings, dockerfw.DefaultSettings()) {
		t.Errorf("settings %+v, want the defaults", ds.Settings)
	}
	for _, want := range []string{`"networks":[{"name":"bridge"`, `"rules":[]`, `"bindings":[]`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("json %s lacks %s", raw, want)
		}
	}
}

func TestDstResourceDockerUnavailable(t *testing.T) {
	h := newFakeHost(t)
	d := newFakeDocker()
	d.err = dockerapi.ErrUnavailable
	ds, err := fw{d: dstDeps(h, d)}.dockerStatus(context.Background(), h.env())
	if err != nil {
		t.Fatal(err)
	}
	if ds.Available || ds.Reason == "" || ds.Health != "unsupported" || ds.Networks == nil || ds.Bindings == nil {
		t.Errorf("status %+v, want unavailable with a reason, not an error", ds)
	}
}
