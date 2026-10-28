package firewall

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/netip"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/firewall/dockerfw"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
	"github.com/darkraise/darkpanel/internal/platform/netstat"
)

var dfT0 = time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)

// dfState is a dockerState for tests that do not read a host: Docker 29.9.1
// available with the iptables driver, its chains in UFW's nft variant and
// the raw protection loaded, the integration off with the default settings
// and no rows or containers. Each opt edits it; realized is computed from
// the result's rows, facts and settings.
func dfState(t *testing.T, opts ...func(*dockerState)) dockerState {
	t.Helper()
	st := dockerState{
		available: true,
		det: dockerfw.Detection{Version: "29.9.1", Driver: "iptables", DockerVariant: "nft", UFWVariant: "nft",
			RawProtection: true},
		cfg: dockerConfig{Settings: dockerfw.DefaultSettings()},
	}
	for _, o := range opts {
		o(&st)
	}
	st.realized = dockerfw.Realize(st.rows, st.facts, st.cfg.Settings)
	return st
}

func dfID(c byte) string { return strings.Repeat(string(c), 64) }

func dfNetwork(name string, id byte, driver string, ipv6 bool, opts map[string]string, subnets ...string) dockerapi.Network {
	n := dockerapi.Network{Name: name, ID: dfID(id), Driver: driver, EnableIPv6: ipv6, Options: opts}
	for _, s := range subnets {
		n.IPAM.Config = append(n.IPAM.Config, dockerapi.IPAMConfig{Subnet: netip.MustParsePrefix(s)})
	}
	return n
}

func dfContainer(name string, labels map[string]string, ports map[string][]dockerapi.PortBinding,
	nets map[string]*dockerapi.EndpointSettings) dockerapi.ContainerJSON {
	return fdContainer(name, func(c *dockerapi.ContainerJSON) {
		c.ID, c.Name = strings.Repeat(name[:1], 64), "/"+name
		c.State = dockerapi.ContainerState{Status: "running", Running: true}
		c.Config.Labels = labels
		c.NetworkSettings = dockerapi.NetworkSettings{Ports: ports, Networks: nets}
	})
}

func dfEndpoint(v4, v6 string) *dockerapi.EndpointSettings {
	e := &dockerapi.EndpointSettings{}
	if v4 != "" {
		e.IPAddress = netip.MustParseAddr(v4)
	}
	if v6 != "" {
		e.GlobalIPv6Address = netip.MustParseAddr(v6)
	}
	return e
}

// dfVariants scripts deps.readVariant: the save text of each variant's
// filter and raw tables, and the calls made. A variant with no text cannot
// be read safely and gives a nil ruleset.
type dfVariants struct {
	t      *testing.T
	filter map[string]string
	raw    map[string]string
	err    error
	calls  []string
}

const (
	dfFilterDocker = "*filter\n:INPUT ACCEPT [0:0]\n:FORWARD DROP [0:0]\n:OUTPUT ACCEPT [0:0]\n:DOCKER-USER - [0:0]\n" +
		":DOCKER-FORWARD - [0:0]\n-A FORWARD -j DOCKER-USER\n-A DOCKER-USER -j RETURN\nCOMMIT\n"
	dfFilterPlain = "*filter\n:INPUT ACCEPT [0:0]\n:FORWARD ACCEPT [0:0]\n:OUTPUT ACCEPT [0:0]\nCOMMIT\n"
	dfRawDocker   = "*raw\n:PREROUTING ACCEPT [0:0]\n:OUTPUT ACCEPT [0:0]\n" +
		"-A PREROUTING -d 172.17.0.2/32 ! -i docker0 -p tcp -m tcp --dport 80 -j DROP\nCOMMIT\n"
)

func (v *dfVariants) read(_ context.Context, _ core.Env, f fwstate.Family, variant string, tables []string) (*netfilter.Ruleset, error) {
	v.calls = append(v.calls, fmt.Sprintf("%s %s %s", f, variant, strings.Join(tables, ",")))
	if v.err != nil {
		return nil, v.err
	}
	text := v.filter[variant]
	if slices.Equal(tables, []string{"raw"}) {
		text = v.raw[variant]
	}
	if text == "" {
		return nil, nil
	}
	return fhParse(v.t, text), nil
}

var dfInterfaces = []netstat.Interface{
	{Index: 1, Name: "lo", Up: true, Running: true, Loopback: true, Addrs: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/8")}},
	{Index: 2, Name: "eth0", Up: true, Running: true, Addrs: []netip.Prefix{netip.MustParsePrefix("192.0.2.10/24")}},
}

// dfHost is the Docker daemon, the variant reads and the interfaces a
// dockerState reads, with the fw over them and an env holding an empty
// Docker store.
type dfHost struct {
	d    *fakeDocker
	v    *dfVariants
	f    fw
	env  core.Env
	ifs  []netstat.Interface
	ifEr error
}

func dfNewHost(t *testing.T) *dfHost {
	t.Helper()
	h := &dfHost{d: newFakeDocker(), ifs: dfInterfaces,
		v:   &dfVariants{t: t, filter: map[string]string{"nft": dfFilterDocker}, raw: map[string]string{"nft": dfRawDocker}},
		env: core.Env{HostID: "h1", DB: dsDB(t)}}
	h.f = fw{d: deps{
		docker:      h.d,
		readVariant: h.v.read,
		interfaces:  func() ([]netstat.Interface, error) { return h.ifs, h.ifEr },
	}}
	return h
}

func (h *dfHost) seed(t *testing.T, cfg dockerConfig, rows ...dockerfw.ContainerRule) {
	t.Helper()
	ctx := context.Background()
	err := h.env.DB.Write(ctx, func(tx *sql.Tx) error {
		if err := writeDockerConfig(ctx, tx, cfg); err != nil {
			return err
		}
		for _, r := range rows {
			if err := insertContainerRule(ctx, tx, r, dfT0); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

var dfCur = fwstate.Loaded{Variant: "nft"}

// TestDfDockerStateFacts: the state is the daemon's detection, the running
// containers and the networks as dockerfw's facts, the stored flag, settings
// and rows, the realized block and the host's interfaces. Containers and
// networks are sorted by name, a container's ports by container port,
// protocol, host port and host address, and its endpoints by network; the
// endpoints are those on bridge networks of the list; the bridge is the
// network's own name, docker0 for the default network, else br-<id>.
func TestDfDockerStateFacts(t *testing.T) {
	h := dfNewHost(t)
	settings := dockerfw.DefaultSettings()
	settings.PrivateSources = settings.PrivateSources[:3]
	settings.ForwardExemptions = []dockerfw.Exemption{{OrigDestination: netip.MustParseAddr("203.0.113.5"), OrigPort: 8443,
		Destination: netip.MustParseAddr("10.9.0.2"), Proto: "tcp", Port: 443}}
	allow := dockerfw.ContainerRule{ID: "01J00000000000000000000001", Kind: dockerfw.Allow,
		Binding: dockerfw.Binding{Selector: "web", HostPort: 8080, Proto: "tcp"}}
	deny := dockerfw.ContainerRule{ID: "01J00000000000000000000002", Kind: dockerfw.Deny,
		Binding: dockerfw.Binding{Selector: "web", HostPort: 8080, Proto: "tcp"}, From: netip.MustParsePrefix("203.0.113.0/24")}
	cfg := dockerConfig{Enabled: true, Settings: settings}
	h.seed(t, cfg, deny, allow)

	h.d.networks = []dockerapi.Network{
		dfNetwork("v6net", 'd', "bridge", true, nil, "fd00:dead::/64"),
		dfNetwork("host", 'f', "host", false, nil),
		dfNetwork("bridge", 'b', "bridge", false, nil, "172.17.0.0/16"),
		dfNetwork("routednet", 'e', "bridge", false, map[string]string{"com.docker.network.bridge.gateway_mode_ipv4": "routed"}, "198.51.100.0/24"),
		dfNetwork("custom", 'c', "bridge", false, map[string]string{"com.docker.network.bridge.name": "dpcustom"}, "172.30.0.0/24"),
	}
	pb := func(ip, port string) dockerapi.PortBinding { return dockerapi.PortBinding{HostIP: ip, HostPort: port} }
	old := dfContainer("old", nil, nil, nil)
	old.State = dockerapi.ContainerState{Status: "exited"}
	h.d.containers = []dockerapi.ContainerJSON{
		dfContainer("web", nil, map[string][]dockerapi.PortBinding{
			"80/tcp":  {pb("127.0.0.1", "8081"), pb("::", "8080"), pb("0.0.0.0", "8080")},
			"53/udp":  {pb("127.0.0.1", "8053")},
			"443/tcp": nil,
			"9/sctp":  {pb("0.0.0.0", "9")},
		}, map[string]*dockerapi.EndpointSettings{"bridge": dfEndpoint("172.17.0.2", "")}),
		dfContainer("six", nil, map[string][]dockerapi.PortBinding{"80/tcp": {pb("::", "8443")}},
			map[string]*dockerapi.EndpointSettings{"v6net": dfEndpoint("", "fd00:dead::2")}),
		dfContainer("app", map[string]string{"com.docker.compose.project": "shop", "com.docker.compose.service": "api"},
			map[string][]dockerapi.PortBinding{"80/tcp": {pb("0.0.0.0", "9090")}},
			map[string]*dockerapi.EndpointSettings{"custom": dfEndpoint("172.30.0.2", "")}),
		dfContainer("edge", nil, nil, map[string]*dockerapi.EndpointSettings{
			"host": {}, "gone": dfEndpoint("10.9.9.9", ""), "routednet": dfEndpoint("198.51.100.2", "")}),
		old,
	}

	st, err := h.f.dockerState(context.Background(), h.env, dfCur)
	if err != nil {
		t.Fatal(err)
	}
	wantFacts := dockerfw.Facts{
		Containers: []dockerfw.Container{
			{ID: dfID('a'), Name: "app", Project: "shop", Service: "api",
				Ports:     []dockerfw.Published{{HostAddr: netip.MustParseAddr("0.0.0.0"), HostPort: 9090, ContainerPort: 80, Proto: "tcp"}},
				Endpoints: []dockerfw.Endpoint{{Network: "custom", Bridge: "dpcustom", Addr4: netip.MustParseAddr("172.30.0.2")}}},
			{ID: dfID('e'), Name: "edge",
				Endpoints: []dockerfw.Endpoint{{Network: "routednet", Bridge: "br-" + strings.Repeat("e", 12), Addr4: netip.MustParseAddr("198.51.100.2")}}},
			{ID: dfID('s'), Name: "six",
				Ports:     []dockerfw.Published{{HostAddr: netip.MustParseAddr("::"), HostPort: 8443, ContainerPort: 80, Proto: "tcp"}},
				Endpoints: []dockerfw.Endpoint{{Network: "v6net", Bridge: "br-" + strings.Repeat("d", 12), Addr6: netip.MustParseAddr("fd00:dead::2")}}},
			{ID: dfID('w'), Name: "web",
				Ports: []dockerfw.Published{
					{HostAddr: netip.MustParseAddr("127.0.0.1"), HostPort: 8053, ContainerPort: 53, Proto: "udp"},
					{HostAddr: netip.MustParseAddr("0.0.0.0"), HostPort: 8080, ContainerPort: 80, Proto: "tcp"},
					{HostAddr: netip.MustParseAddr("::"), HostPort: 8080, ContainerPort: 80, Proto: "tcp"},
					{HostAddr: netip.MustParseAddr("127.0.0.1"), HostPort: 8081, ContainerPort: 80, Proto: "tcp"},
				},
				Endpoints: []dockerfw.Endpoint{{Network: "bridge", Bridge: "docker0", Addr4: netip.MustParseAddr("172.17.0.2")}}},
		},
		Networks: []dockerfw.Network{
			{ID: dfID('b'), Name: "bridge", Driver: "bridge", Bridge: "docker0", Subnets: []netip.Prefix{netip.MustParsePrefix("172.17.0.0/16")}},
			{ID: dfID('c'), Name: "custom", Driver: "bridge", Bridge: "dpcustom", Subnets: []netip.Prefix{netip.MustParsePrefix("172.30.0.0/24")}},
			{ID: dfID('f'), Name: "host", Driver: "host"},
			{ID: dfID('e'), Name: "routednet", Driver: "bridge", Bridge: "br-" + strings.Repeat("e", 12),
				Subnets: []netip.Prefix{netip.MustParsePrefix("198.51.100.0/24")}, GatewayModeV4: "routed"},
			{ID: dfID('d'), Name: "v6net", Driver: "bridge", Bridge: "br-" + strings.Repeat("d", 12), IPv6: true,
				Subnets: []netip.Prefix{netip.MustParsePrefix("fd00:dead::/64")}},
		},
	}
	if !st.available || st.reason != "" {
		t.Errorf("available %v reason %q, want available", st.available, st.reason)
	}
	if !reflect.DeepEqual(st.facts, wantFacts) {
		t.Errorf("facts\n%+v\nwant\n%+v", st.facts, wantFacts)
	}
	rawTable := fhParse(t, dfRawDocker).Table("raw")
	wantDet := dockerfw.Detection{Version: "29.9.1", Driver: "iptables", DockerVariant: "nft", UFWVariant: "nft",
		RawProtection: dockerfw.RawProtected(rawTable, wantFacts)}
	if st.det != wantDet {
		t.Errorf("detection %+v, want %+v", st.det, wantDet)
	}
	if !reflect.DeepEqual(st.cfg, cfg) {
		t.Errorf("config %+v, want %+v", st.cfg, cfg)
	}
	if want := []dockerfw.ContainerRule{allow, deny}; !reflect.DeepEqual(st.rows, want) {
		t.Errorf("rows %+v, want %+v (ordered by id)", st.rows, want)
	}
	if want := dockerfw.Realize(st.rows, wantFacts, settings); !reflect.DeepEqual(st.realized, want) {
		t.Errorf("realized %+v, want %+v", st.realized, want)
	}
	if !reflect.DeepEqual(st.interfaces, dfInterfaces) {
		t.Errorf("interfaces %+v, want %+v", st.interfaces, dfInterfaces)
	}
}

// TestDfDockerStateDetection: the driver is iptables when the daemon does
// not report one; the variant holding Docker's chains is found by reading
// both variants' filter tables in IPv4; the raw table is read from that
// variant only when exactly one holds the chains.
func TestDfDockerStateDetection(t *testing.T) {
	nftFW := &dockerapi.FirewallBackend{Driver: "nftables"}
	for _, tc := range []struct {
		name            string
		nft, legacy     string
		backend         *dockerapi.FirewallBackend
		wantVariant     string
		wantDriver      string
		wantRawReadFrom string
	}{
		{"nft chains", dfFilterDocker, dfFilterPlain, &dockerapi.FirewallBackend{Driver: "iptables"}, "nft", "iptables", "nft"},
		{"legacy chains", dfFilterPlain, dfFilterDocker, nil, "legacy", "iptables", "legacy"},
		{"both hold them", dfFilterDocker, dfFilterDocker, &dockerapi.FirewallBackend{}, "both", "iptables", ""},
		{"neither holds them", dfFilterPlain, dfFilterPlain, nil, "", "iptables", ""},
		{"legacy unreadable", dfFilterDocker, "", nil, "nft", "iptables", "nft"},
		{"nftables driver", dfFilterDocker, "", nftFW, "nft", "nftables", "nft"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := dfNewHost(t)
			h.v.filter = map[string]string{"nft": tc.nft, "legacy": tc.legacy}
			h.v.raw = map[string]string{"nft": dfRawDocker, "legacy": dfRawDocker}
			h.d.info.ServerVersion, h.d.info.FirewallBackend = "28.0.2", tc.backend
			st, err := h.f.dockerState(context.Background(), h.env, fwstate.Loaded{Variant: "legacy"})
			if err != nil {
				t.Fatal(err)
			}
			if st.det.Version != "28.0.2" || st.det.Driver != tc.wantDriver || st.det.DockerVariant != tc.wantVariant || st.det.UFWVariant != "legacy" {
				t.Errorf("detection %+v, want version 28.0.2, driver %s, Docker variant %q, UFW variant legacy", st.det, tc.wantDriver, tc.wantVariant)
			}
			want := []string{"v4 legacy filter", "v4 nft filter"}
			if tc.wantRawReadFrom != "" {
				want = append(want, "v4 "+tc.wantRawReadFrom+" raw")
			} else if st.det.RawProtection {
				t.Error("raw protection reported without a raw table read")
			}
			slices.Sort(want)
			if got := slices.Sorted(slices.Values(h.v.calls)); !slices.Equal(got, want) {
				t.Errorf("variant reads %q, want %q", got, want)
			}
		})
	}
}

// TestDfRawProtectionInternal: Docker loads no raw drop rule for an
// endpoint on an internal network, so the raw protection does not expect
// one; the facts still list that endpoint.
func TestDfRawProtectionInternal(t *testing.T) {
	const raw = "*raw\n:PREROUTING ACCEPT [0:0]\n:OUTPUT ACCEPT [0:0]\n" +
		"-A PREROUTING -d 172.17.0.2/32 ! -i docker0 -j DROP\nCOMMIT\n"
	for _, internal := range []bool{false, true} {
		h := dfNewHost(t)
		h.v.raw = map[string]string{"nft": raw}
		back := dfNetwork("back", 'i', "bridge", false, nil, "172.31.0.0/24")
		back.Internal = internal
		h.d.networks = []dockerapi.Network{dfNetwork("bridge", 'b', "bridge", false, nil, "172.17.0.0/16"), back}
		h.d.containers = []dockerapi.ContainerJSON{dfContainer("web", nil, nil, map[string]*dockerapi.EndpointSettings{
			"bridge": dfEndpoint("172.17.0.2", ""), "back": dfEndpoint("172.31.0.2", "")})}
		st, err := h.f.dockerState(context.Background(), h.env, dfCur)
		if err != nil {
			t.Fatal(err)
		}
		if st.det.RawProtection != internal {
			t.Errorf("internal %v: RawProtection %v, want %v", internal, st.det.RawProtection, internal)
		}
		if c := st.facts.Containers; len(c) != 1 || len(c[0].Endpoints) != 2 {
			t.Errorf("internal %v: containers %+v, want web with both endpoints", internal, c)
		}
	}
}

// TestDfDockerStateUnavailable: an unreachable daemon is not an error: the
// state is unavailable with a reason and no facts or detection, yet it
// holds the stored configuration, rows and the block those realize, so the
// integration can still be turned off. Another daemon failure, a variant
// read and the interface list fail the call.
func TestDfDockerStateUnavailable(t *testing.T) {
	ctx := context.Background()
	h := dfNewHost(t)
	cfg := dockerConfig{Enabled: true, Settings: dockerfw.DefaultSettings()}
	row := dockerfw.ContainerRule{ID: "01J00000000000000000000001", Kind: dockerfw.Allow,
		Binding: dockerfw.Binding{Selector: "web", HostPort: 8080, Proto: "tcp"}}
	h.seed(t, cfg, row)
	h.d.err = fmt.Errorf("dial unix /var/run/docker.sock: %w", dockerapi.ErrUnavailable)
	st, err := h.f.dockerState(ctx, h.env, dfCur)
	if err != nil {
		t.Fatal(err)
	}
	if st.available || st.reason != "the Docker daemon is not reachable" {
		t.Errorf("available %v reason %q, want unavailable with its reason", st.available, st.reason)
	}
	if st.det != (dockerfw.Detection{}) || !reflect.DeepEqual(st.facts, dockerfw.Facts{}) {
		t.Errorf("detection %+v, facts %+v, want none", st.det, st.facts)
	}
	if !reflect.DeepEqual(st.cfg, cfg) || !reflect.DeepEqual(st.rows, []dockerfw.ContainerRule{row}) {
		t.Errorf("config %+v, rows %+v, want the stored ones", st.cfg, st.rows)
	}
	if want := dockerfw.Realize(st.rows, dockerfw.Facts{}, cfg.Settings); !reflect.DeepEqual(st.realized, want) {
		t.Errorf("realized %+v, want %+v", st.realized, want)
	}
	if !reflect.DeepEqual(st.interfaces, dfInterfaces) || len(h.v.calls) != 0 {
		t.Errorf("interfaces %+v, variant reads %q, want the interfaces and no read", st.interfaces, h.v.calls)
	}

	boom := errors.New("boom")
	for name, fail := range map[string]func(h *dfHost){
		"the daemon answers an error": func(h *dfHost) { h.d.err = boom },
		"a variant read fails":        func(h *dfHost) { h.v.err = boom },
		"the interfaces fail":         func(h *dfHost) { h.ifEr = boom },
	} {
		h := dfNewHost(t)
		fail(h)
		if _, err := h.f.dockerState(ctx, h.env, dfCur); !errors.Is(err, boom) || !strings.HasPrefix(err.Error(), "firewall:") {
			t.Errorf("%s: dockerState = %v, want a firewall error wrapping %v", name, err, boom)
		}
	}
}

func dfAfterFiles() fwstate.Config {
	return fwstate.Config{Files: map[string]core.RestoreEntry{
		"/etc/ufw/after.rules":  {Path: "/etc/ufw/after.rules", Present: true, Mode: 0o640, Data: []byte("*filter\n# after\nCOMMIT\n")},
		"/etc/ufw/after6.rules": {Path: "/etc/ufw/after6.rules", Present: true, Mode: 0o640, Data: []byte("*filter\n# after6\nCOMMIT\n")},
	}}
}

// TestDfBlockFiles: enabled, both after files carry the realized block of
// their family between the markers after the file's own content; disabled,
// they carry no block and keep that content; a missing file is an error.
func TestDfBlockFiles(t *testing.T) {
	binding := dockerfw.Binding{Selector: "web", HostPort: 8080, Proto: "tcp"}
	st := dfState(t, func(s *dockerState) {
		s.cfg.Enabled = true
		s.rows = []dockerfw.ContainerRule{{ID: "01J00000000000000000000001", Binding: binding, Kind: dockerfw.Allow}}
		s.facts = dockerfw.Facts{
			Containers: []dockerfw.Container{{ID: dfID('w'), Name: "web",
				Ports:     []dockerfw.Published{{HostAddr: netip.MustParseAddr("0.0.0.0"), HostPort: 8080, ContainerPort: 80, Proto: "tcp"}},
				Endpoints: []dockerfw.Endpoint{{Network: "bridge", Bridge: "docker0", Addr4: netip.MustParseAddr("172.17.0.2")}}}},
			Networks: []dockerfw.Network{{ID: dfID('b'), Name: "bridge", Driver: "bridge", Bridge: "docker0",
				Subnets: []netip.Prefix{netip.MustParsePrefix("172.17.0.0/16")}}},
		}
	})
	cfg := dfAfterFiles()
	on, err := blockFiles(cfg, st, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(on) != 2 {
		t.Fatalf("blockFiles wrote %d files, want after.rules and after6.rules", len(on))
	}
	for path, fam := range map[string]fwstate.Family{"/etc/ufw/after.rules": fwstate.V4, "/etc/ufw/after6.rules": fwstate.V6} {
		got := string(on[path])
		for _, want := range []string{"# BEGIN DARKPANEL docker\n", "# END DARKPANEL docker\n", string(dockerfw.Render(st.realized.Block, fam)),
			string(cfg.Files[path].Data[:len("*filter\n# after")])} {
			if !strings.Contains(got, want) {
				t.Errorf("%s with the block lacks %q:\n%s", path, want, got)
			}
		}
	}
	v4, v6 := string(on["/etc/ufw/after.rules"]), string(on["/etc/ufw/after6.rules"])
	if !strings.Contains(v4, ":ufw-user-forward - [0:0]") || strings.Contains(v4, "ufw6-user-forward") ||
		!strings.Contains(v6, ":ufw6-user-forward - [0:0]") || strings.Contains(v6, ":ufw-user-forward") {
		t.Errorf("the families' blocks are mixed up:\n%s\n%s", v4, v6)
	}
	if !strings.Contains(v4, binding.Chain()) {
		t.Errorf("after.rules lacks the binding chain %s:\n%s", binding.Chain(), v4)
	}

	withBlock := dfAfterFiles()
	for p, data := range on {
		withBlock.Files[p] = core.RestoreEntry{Path: p, Present: true, Mode: 0o640, Data: data}
	}
	off, err := blockFiles(withBlock, st, false)
	if err != nil {
		t.Fatal(err)
	}
	for path, orig := range map[string]string{"/etc/ufw/after.rules": "# after\n", "/etc/ufw/after6.rules": "# after6\n"} {
		got := string(off[path])
		if strings.Contains(got, "DARKPANEL") || strings.Contains(got, "DOCKER-USER") || !strings.Contains(got, orig) {
			t.Errorf("%s without the block:\n%s", path, got)
		}
	}
	plain, err := blockFiles(cfg, st, false)
	if err != nil {
		t.Fatal(err)
	}
	for path, e := range cfg.Files {
		if !bytes.Equal(plain[path], e.Data) {
			t.Errorf("%s without a block to remove = %q, want it unchanged", path, plain[path])
		}
	}

	delete(cfg.Files, "/etc/ufw/after6.rules")
	if _, err := blockFiles(cfg, st, true); err == nil || !strings.HasPrefix(err.Error(), "firewall:") || !strings.Contains(err.Error(), "after6.rules") {
		t.Errorf("blockFiles without after6.rules = %v, want an error naming it", err)
	}
	broken := dfAfterFiles()
	e := broken.Files["/etc/ufw/after.rules"]
	e.Data = []byte("*filter\n# BEGIN DARKPANEL docker\nCOMMIT\n")
	broken.Files["/etc/ufw/after.rules"] = e
	if _, err := blockFiles(broken, st, true); err == nil || !strings.Contains(err.Error(), "after.rules") {
		t.Errorf("blockFiles with an unterminated block = %v, want an error naming the file", err)
	}
}
