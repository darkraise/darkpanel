package panel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/modules/docker/dstate"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
	"github.com/darkraise/darkpanel/internal/platform/netstat"
)

const nwChange = "01JC0000000000000000000009"

// nwJournal is an in-memory core.EffectJournal. onIntend, when set, runs as each effect is recorded.
type nwJournal struct {
	list     []core.Effect
	onIntend func()
}

func (j *nwJournal) Intend(_ context.Context, e core.Effect, _ []byte) (int, error) {
	if j.onIntend != nil {
		j.onIntend()
	}
	e.Seq, e.State, e.ChangeID = len(j.list)+1, core.EffectIntended, nwChange
	j.list = append(j.list, e)
	return e.Seq, nil
}

func (j *nwJournal) intended(seq int) (*core.Effect, error) {
	if seq < 1 || seq > len(j.list) || j.list[seq-1].State != core.EffectIntended {
		return nil, fmt.Errorf("fake: effect %d is not intended", seq)
	}
	return &j.list[seq-1], nil
}

func (j *nwJournal) Made(_ context.Context, seq int, resource string, post core.StateDoc) error {
	e, err := j.intended(seq)
	if err != nil {
		return err
	}
	e.State, e.Resource, e.Post = core.EffectMade, resource, &post
	return nil
}

func (j *nwJournal) Skipped(_ context.Context, seq int) error {
	e, err := j.intended(seq)
	if err != nil {
		return err
	}
	e.State = core.EffectSkipped
	return nil
}

func (j *nwJournal) List(context.Context) ([]core.Effect, error) { return slices.Clone(j.list), nil }

func (j *nwJournal) Secret(context.Context, int) ([]byte, error) {
	return nil, errors.New("fake: no secret")
}

var _ core.EffectJournal = (*nwJournal)(nil)

// nwEnv is the agent-side environment of a call; the journal is set for a change and nil for a plan or a read.
func nwEnv(j *nwJournal) core.Env {
	env := core.Env{
		HostID:    "h1",
		Principal: core.Principal{Actor: "user:u1", Scope: core.ScopeAdmin},
		Clock:     coretest.NewFakeClock(fdNow),
	}
	if j != nil {
		env.ChangeID, env.Effects = nwChange, j
	}
	return env
}

// nwDeps points the agent actions at the fake Engine, on a host with no IPv4 route or address.
func nwDeps(d *fdDocker) agentDeps { return nwHostDeps(d, nil, nil) }

// nwHostDeps points the agent actions at the fake Engine, on a host whose IPv4 routes have the destinations routes and
// whose one interface has the addresses addrs. A read of the IPv6 routes fails, because the actions need only IPv4.
func nwHostDeps(d *fdDocker, routes, addrs []string) agentDeps {
	var rs []netstat.Route
	for _, r := range routes {
		rs = append(rs, netstat.Route{Type: "unicast", Table: 254, Dst: netip.MustParsePrefix(r), OutIface: "eth0", OutIndex: 2})
	}
	ifc := netstat.Interface{Index: 2, Name: "eth0", Up: true, Running: true}
	for _, a := range addrs {
		ifc.Addrs = append(ifc.Addrs, netip.MustParsePrefix(a))
	}
	return agentDeps{
		docker: func() (panelDocker, error) { return d, nil },
		routes: func(f netstat.Family) ([]netstat.Route, error) {
			if f != netstat.IPv4 {
				return nil, fmt.Errorf("fake: routes of family %d", f)
			}
			return rs, nil
		},
		interfaces: func() ([]netstat.Interface, error) { return []netstat.Interface{ifc}, nil },
	}
}

func nwJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// nwNet is a bridge network with one IPv4 or IPv6 subnet per argument.
func nwNet(name string, labels map[string]string, subnets ...string) dockerapi.Network {
	n := dockerapi.Network{Name: name, Created: "2026-10-01T00:00:00Z", Scope: "local", Driver: "bridge", Labels: labels}
	for _, s := range subnets {
		n.IPAM.Config = append(n.IPAM.Config, dockerapi.IPAMConfig{Subnet: netip.MustParsePrefix(s)})
	}
	return n
}

// nwPanelNet is the darkpanel-panel network as panel.network.create makes it.
func nwPanelNet(subnet string) dockerapi.Network {
	p := netip.MustParsePrefix(subnet)
	n := nwNet(NetworkName, map[string]string{core.LabelModule: ID})
	n.IPAM.Config = []dockerapi.IPAMConfig{{Subnet: p, Gateway: p.Addr().Next()}}
	n.Options = map[string]string{BridgeOption: BridgeName}
	return n
}

// nwAttach puts a container on the network.
func nwAttach(d *fdDocker, network, container string) {
	d.AddContainer(dockerapi.ContainerJSON{Name: "/" + container, NetworkSettings: dockerapi.NetworkSettings{
		Networks: map[string]*dockerapi.EndpointSettings{network: {EndpointID: "e-" + container}}}})
}

// nwRefusal asserts err is a definite refusal with the code and returns it.
func nwRefusal(t *testing.T, err error, code errcode.Code) *errcode.Error {
	t.Helper()
	var coded *errcode.Error
	if !errors.As(err, &coded) || coded.Code != code {
		t.Fatalf("error %v, want code %s", err, code)
	}
	if !core.IsDefinite(err) {
		t.Errorf("error %v is not marked definite", err)
	}
	return coded
}

// nwStates returns the states of the journaled effects, in order.
func nwStates(j *nwJournal) []core.EffectState {
	var out []core.EffectState
	for _, e := range j.list {
		out = append(out, e.State)
	}
	return out
}

func nwApplyCreate(t *testing.T, d *fdDocker, j *nwJournal) (NetworkView, error) {
	t.Helper()
	raw, err := networkCreateAction(nwDeps(d)).Handler.Apply(context.Background(), nwEnv(j), nwJSON(t, NetworkCreateInput{}))
	if err != nil {
		return NetworkView{}, err
	}
	var out NetworkView
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out, nil
}

// TestNwCreatePlanPicksSubnet: the plan takes the highest /29 of 172.31.255.0/24 that overlaps no IPv4 subnet of any
// network and no IPv4 route of /8 or longer or address of the host, gives the gateway the first host address and the
// proxy the second, and refuses when none is free, naming what takes the range.
func TestNwCreatePlanPicksSubnet(t *testing.T) {
	// takeFrom8 occupies every /29 above the lowest two: .8 up to .255.
	takeFrom8 := func(d *fdDocker) {
		for i, s := range []string{"172.31.255.128/25", "172.31.255.64/26", "172.31.255.32/27", "172.31.255.16/28", "172.31.255.8/29"} {
			d.AddNetwork(nwNet(fmt.Sprintf("taken%d", i), nil, s))
		}
	}
	const taken = "every one overlaps a Docker network's subnet or a host route or address"
	tests := []struct {
		name          string
		setup         func(d *fdDocker)
		routes, addrs []string // the host's IPv4 route destinations and interface addresses
		subnet        string   // "" when none is free
		why           string   // what the refusal names, when set
	}{
		{name: "empty host", subnet: "172.31.255.248/29"},
		{name: "top block taken", setup: func(d *fdDocker) { d.AddNetwork(nwNet("a", nil, "172.31.255.248/29")) }, subnet: "172.31.255.240/29"},
		{name: "a /28 over the two top blocks", setup: func(d *fdDocker) { d.AddNetwork(nwNet("a", nil, "172.31.255.240/28")) }, subnet: "172.31.255.232/29"},
		{name: "a /25 over the upper half", setup: func(d *fdDocker) { d.AddNetwork(nwNet("a", nil, "172.31.255.128/25")) }, subnet: "172.31.255.120/29"},
		{name: "a /30 inside the top block", setup: func(d *fdDocker) { d.AddNetwork(nwNet("a", nil, "172.31.255.252/30")) }, subnet: "172.31.255.240/29"},
		{name: "networks outside the range", setup: func(d *fdDocker) {
			d.AddNetwork(nwNet("a", nil, "172.31.254.0/24", "10.0.0.0/8"))
		}, subnet: "172.31.255.248/29"},
		{name: "an IPv6 subnet", setup: func(d *fdDocker) { d.AddNetwork(nwNet("a", nil, "fd00::/64")) }, subnet: "172.31.255.248/29"},
		{name: "a network without a subnet", setup: func(d *fdDocker) { d.AddNetwork(nwNet("a", nil)) }, subnet: "172.31.255.248/29"},
		{name: "a second subnet of one network", setup: func(d *fdDocker) {
			d.AddNetwork(nwNet("a", nil, "10.1.0.0/16", "172.31.255.248/29"))
		}, subnet: "172.31.255.240/29"},
		{name: "one network covering the range", setup: func(d *fdDocker) { d.AddNetwork(nwNet("a", nil, "172.16.0.0/12")) }},
		{name: "only the lowest block left", setup: takeFrom8, subnet: "172.31.255.0/29"},
		{name: "every block taken", setup: func(d *fdDocker) {
			takeFrom8(d)
			d.AddNetwork(nwNet("last", nil, "172.31.255.0/29"))
		}},
		{name: "a host route over the top block", routes: []string{"172.31.255.248/29"}, subnet: "172.31.255.240/29"},
		{name: "a VPN route over the upper half", routes: []string{"172.31.255.128/25"}, subnet: "172.31.255.120/29"},
		{name: "a LAN address inside the range", addrs: []string{"172.31.255.200/26"}, subnet: "172.31.255.184/29"},
		{name: "a host address of one address", addrs: []string{"172.31.255.250/32"}, subnet: "172.31.255.240/29"},
		{name: "the default route", routes: []string{"0.0.0.0/0"}, subnet: "172.31.255.248/29"},
		{name: "a default route split in halves", routes: []string{"0.0.0.0/1", "128.0.0.0/1"}, subnet: "172.31.255.248/29"},
		{name: "a /7 route over the range", routes: []string{"172.0.0.0/7"}, subnet: "172.31.255.248/29"},
		{name: "routes and addresses outside the range", routes: []string{"10.0.0.0/8", "192.168.1.0/24"},
			addrs: []string{"127.0.0.1/8", "192.168.1.10/24", "fd00::1/64"}, subnet: "172.31.255.248/29"},
		{name: "a /8 route over the range", routes: []string{"172.0.0.0/8"}, why: taken + " (172.0.0.0/8)"},
		{name: "a VPN route covering the range", routes: []string{"172.16.0.0/12"}, why: taken + " (172.16.0.0/12)"},
		{name: "Docker and the host together", setup: takeFrom8, routes: []string{"172.31.255.8/29"}, addrs: []string{"172.31.255.3/29"},
			why: taken + " (172.31.255.0/29, 172.31.255.8/29, 172.31.255.16/28, 172.31.255.32/27, 172.31.255.64/26, 172.31.255.128/25)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := fdNewDocker()
			if tt.setup != nil {
				tt.setup(d)
			}
			out, err := networkCreateAction(nwHostDeps(d, tt.routes, tt.addrs)).Handler.Plan(context.Background(), nwEnv(nil),
				nwJSON(t, NetworkCreateInput{}))
			if tt.subnet == "" {
				var coded *errcode.Error
				if err == nil || !strings.Contains(err.Error(), "panel: no free /29 is left in 172.31.255.0/24: ") ||
					!strings.Contains(err.Error(), tt.why) || errors.As(err, &coded) || !core.IsDefinite(err) {
					t.Fatalf("error %v, want a definite plain error naming that no /29 is free and %q", err, tt.why)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			p := netip.MustParsePrefix(tt.subnet)
			want := []core.DiffLine{
				{Op: "+", Text: "network darkpanel-panel " + tt.subnet},
				{Op: "+", Text: "gateway " + p.Addr().Next().String() + ", proxy " + p.Addr().Next().Next().String()},
			}
			if !slices.Equal(out.Diff, want) {
				t.Errorf("diff %v, want %v", out.Diff, want)
			}
			if len(d.Created) != 0 {
				t.Error("a plan created a network")
			}
		})
	}
}

// TestNwCreatePlanDiffOnEmptyHost pins the whole diff of the first case as literal text.
func TestNwCreatePlanDiffOnEmptyHost(t *testing.T) {
	out, err := networkCreateAction(nwDeps(fdNewDocker())).Handler.Plan(context.Background(), nwEnv(nil), nwJSON(t, NetworkCreateInput{}))
	if err != nil {
		t.Fatal(err)
	}
	want := []core.DiffLine{
		{Op: "+", Text: "network darkpanel-panel 172.31.255.248/29"},
		{Op: "+", Text: "gateway 172.31.255.249, proxy 172.31.255.250"},
	}
	if !slices.Equal(out.Diff, want) {
		t.Errorf("diff %v, want %v", out.Diff, want)
	}
}

// TestNwCreatePlanStateHash: the hash is stable, and it changes when another network moves the chosen subnet, which
// makes apply refuse a plan whose subnet was taken since.
func TestNwCreatePlanStateHash(t *testing.T) {
	d := fdNewDocker()
	def := networkCreateAction(nwDeps(d))
	plan := func() string {
		t.Helper()
		out, err := def.Handler.Plan(context.Background(), nwEnv(nil), nwJSON(t, NetworkCreateInput{}))
		if err != nil || out.StateHash == "" {
			t.Fatalf("plan: hash %q, error %v", out.StateHash, err)
		}
		return out.StateHash
	}
	first := plan()
	if again := plan(); again != first {
		t.Errorf("a second plan hashes %s, want %s", again, first)
	}
	d.AddNetwork(nwNet("elsewhere", nil, "10.9.0.0/24"))
	if unrelated := plan(); unrelated != first {
		t.Error("the hash changed when a network outside the range appeared")
	}
	d.AddNetwork(nwNet("a", nil, "172.31.255.248/29"))
	if moved := plan(); moved == first {
		t.Error("the hash did not change when the chosen subnet was taken")
	}
}

// TestNwCreatePlanRefusals: an existing darkpanel-panel network, whoever made it, an unreachable Engine and a client that
// cannot be built are refused before anything is created.
func TestNwCreatePlanRefusals(t *testing.T) {
	tests := []struct {
		name  string
		setup func(d *fdDocker)
		code  errcode.Code // "" for a plain definite error
	}{
		{name: "network exists", setup: func(d *fdDocker) { d.AddNetwork(nwPanelNet("172.31.255.248/29")) }, code: errcode.PanelAlreadyExposed},
		{name: "network of that name without the label", setup: func(d *fdDocker) {
			d.AddNetwork(nwNet(NetworkName, nil, "10.9.0.0/24"))
		}, code: errcode.PanelAlreadyExposed},
		{name: "engine unavailable", setup: func(d *fdDocker) { d.Fail("ListNetworks", dockerapi.ErrUnavailable) }, code: errcode.DockerEngineUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := fdNewDocker()
			tt.setup(d)
			_, err := networkCreateAction(nwDeps(d)).Handler.Plan(context.Background(), nwEnv(nil), nwJSON(t, NetworkCreateInput{}))
			_ = nwRefusal(t, err, tt.code)
			if len(d.Created) != 0 {
				t.Error("a refused plan created a network")
			}
		})
	}
	for _, tc := range []struct{ name, want string }{
		{"host routes cannot be read", "panel: read the host's IPv4 routes: fake: netlink refused"},
		{"host addresses cannot be read", "panel: read the host's interface addresses: fake: netlink refused"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := fdNewDocker()
			deps := nwDeps(d)
			if strings.Contains(tc.name, "routes") {
				deps.routes = func(netstat.Family) ([]netstat.Route, error) { return nil, errors.New("fake: netlink refused") }
			} else {
				deps.interfaces = func() ([]netstat.Interface, error) { return nil, errors.New("fake: netlink refused") }
			}
			_, err := networkCreateAction(deps).Handler.Plan(context.Background(), nwEnv(nil), nwJSON(t, NetworkCreateInput{}))
			if err == nil || err.Error() != tc.want || !core.IsDefinite(err) {
				t.Errorf("error %v, want the definite error %q", err, tc.want)
			}
			if len(d.Created) != 0 {
				t.Error("a refused plan created a network")
			}
		})
	}
	t.Run("client cannot be built", func(t *testing.T) {
		deps := agentDeps{docker: func() (panelDocker, error) { return nil, errors.New("no socket") }}
		_, err := networkCreateAction(deps).Handler.Plan(context.Background(), nwEnv(nil), nwJSON(t, NetworkCreateInput{}))
		if err == nil || !core.IsDefinite(err) || !strings.HasPrefix(err.Error(), "panel:") {
			t.Errorf("error %v, want a definite panel: error", err)
		}
	})
}

// TestNwCreateApply: apply journals the creation before the call, makes the network the spec describes (the label, the
// bridge name, IPv6 off, exactly one address configuration with the gateway), records it made with its state, and its
// output and verify agree with the Engine.
func TestNwCreateApply(t *testing.T) {
	ctx := context.Background()
	d := fdNewDocker()
	j := &nwJournal{}
	createdAtIntend := -1
	j.onIntend = func() { createdAtIntend = len(d.Created) }
	out, err := nwApplyCreate(t, d, j)
	if err != nil {
		t.Fatal(err)
	}
	if createdAtIntend != 0 {
		t.Errorf("%d networks existed when the effect was journaled, want 0", createdAtIntend)
	}
	ipv6 := false
	want := dockerapi.NetworkCreate{
		Name: "darkpanel-panel", Driver: "bridge", EnableIPv6: &ipv6,
		IPAM: &dockerapi.IPAM{Config: []dockerapi.IPAMConfig{{
			Subnet: netip.MustParsePrefix("172.31.255.248/29"), Gateway: netip.MustParseAddr("172.31.255.249"),
		}}},
		Options: map[string]string{"com.docker.network.bridge.name": "dp-panel"},
		Labels:  map[string]string{"darkpanel.module": "panel"},
	}
	if !reflect.DeepEqual(d.Created, []dockerapi.NetworkCreate{want}) {
		t.Errorf("created %+v, want %+v", d.Created, want)
	}
	wantOut := NetworkView{
		Network: "darkpanel-panel", ID: "fdnet01", Subnet: netip.MustParsePrefix("172.31.255.248/29"),
		Gateway: netip.MustParseAddr("172.31.255.249"), Proxy: netip.MustParseAddr("172.31.255.250"),
	}
	if out != wantOut {
		t.Errorf("output %+v, want %+v", out, wantOut)
	}
	if len(j.list) != 1 {
		t.Fatalf("%d effects journaled, want 1", len(j.list))
	}
	e := j.list[0]
	n, err := d.InspectNetwork(ctx, "fdnet01")
	if err != nil {
		t.Fatal(err)
	}
	post, err := dstate.NetworkCreated(&n)
	if err != nil {
		t.Fatal(err)
	}
	if e.State != core.EffectMade || e.Kind != core.EffectCreate || e.Resource != "panel.network:fdnet01" ||
		!reflect.DeepEqual(e.Recovery, &core.RecoveryID{Kind: "name", Value: "darkpanel-panel"}) || string(e.Before.State) != "null" ||
		e.CommitWait != 2*time.Minute || e.Local || e.Post == nil || !bytes.Equal(e.Post.State, post) {
		t.Errorf("effect %+v, want a made creation of panel.network:fdnet01 recovered by name, committing within 2 minutes, with post %s", e, post)
	}
	if err := networkCreateAction(nwDeps(d)).Handler.Verify(ctx, nwEnv(j), nwJSON(t, NetworkCreateInput{}), nwJSON(t, out)); err != nil {
		t.Errorf("verify after apply: %v", err)
	}
}

// TestNwCreateApplyFailures: a refusal of the Engine is definite and skips the effect, any other failure leaves it
// intended for the engine to settle, and a network that appeared since the plan is refused without an effect.
func TestNwCreateApplyFailures(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(d *fdDocker)
		definite bool
		code     errcode.Code
		states   []core.EffectState
	}{
		{name: "pool overlap refused", setup: func(d *fdDocker) {
			d.Fail("CreateNetwork", &dockerapi.APIError{StatusCode: http.StatusForbidden, Message: "Pool overlaps with other one on this address space"})
		}, definite: true, states: []core.EffectState{core.EffectSkipped}},
		{name: "server error", setup: func(d *fdDocker) {
			d.Fail("CreateNetwork", &dockerapi.APIError{StatusCode: http.StatusInternalServerError, Message: "boom"})
		}, states: []core.EffectState{core.EffectIntended}},
		{name: "engine lost during the call", setup: func(d *fdDocker) { d.Fail("CreateNetwork", dockerapi.ErrUnavailable) },
			states: []core.EffectState{core.EffectIntended}},
		{name: "network appeared since the plan", setup: func(d *fdDocker) { d.AddNetwork(nwPanelNet("172.31.255.248/29")) },
			definite: true, code: errcode.PanelAlreadyExposed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := fdNewDocker()
			tt.setup(d)
			j := &nwJournal{}
			_, err := nwApplyCreate(t, d, j)
			if err == nil {
				t.Fatal("apply succeeded")
			}
			if core.IsDefinite(err) != tt.definite {
				t.Errorf("definite = %v, want %v (%v)", core.IsDefinite(err), tt.definite, err)
			}
			if tt.code != "" {
				_ = nwRefusal(t, err, tt.code)
			}
			if got := nwStates(j); !slices.Equal(got, tt.states) {
				t.Errorf("effect states %v, want %v", got, tt.states)
			}
		})
	}
}

// TestNwCreateVerify: verify reads the network from the Engine alone and accepts only the network as it was created.
func TestNwCreateVerify(t *testing.T) {
	good := nwPanelNet("172.31.255.248/29")
	tests := []struct {
		name string
		net  *dockerapi.Network
		edit func(n *dockerapi.Network)
		has  string // "" when verify passes
	}{
		{name: "as created", net: &good},
		{name: "missing", has: "does not exist"},
		{name: "no label", net: &good, edit: func(n *dockerapi.Network) { n.Labels = nil }, has: core.LabelModule},
		{name: "label of another module", net: &good, edit: func(n *dockerapi.Network) { n.Labels = map[string]string{core.LabelModule: "npm"} }, has: core.LabelModule},
		{name: "another bridge", net: &good, edit: func(n *dockerapi.Network) { n.Options = nil }, has: "dp-panel"},
		{name: "IPv6 on", net: &good, edit: func(n *dockerapi.Network) { n.EnableIPv6 = true }, has: "IPv6"},
		{name: "two configurations", net: &good, edit: func(n *dockerapi.Network) {
			n.IPAM.Config = append(n.IPAM.Config, dockerapi.IPAMConfig{Subnet: netip.MustParsePrefix("fd00::/64")})
		}, has: "2 address configurations"},
		{name: "no configuration", net: &good, edit: func(n *dockerapi.Network) { n.IPAM.Config = nil }, has: "0 address configurations"},
		{name: "a /24", net: &good, edit: func(n *dockerapi.Network) { n.IPAM.Config[0].Subnet = netip.MustParsePrefix("172.31.255.0/24") }, has: "/29"},
		{name: "outside the range", net: &good, edit: func(n *dockerapi.Network) { n.IPAM.Config[0].Subnet = netip.MustParsePrefix("10.9.0.0/29") }, has: "/29"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := fdNewDocker()
			if tt.net != nil {
				n := *tt.net
				n.IPAM.Config = slices.Clone(n.IPAM.Config)
				if tt.edit != nil {
					tt.edit(&n)
				}
				d.AddNetwork(n)
			}
			err := networkCreateAction(nwDeps(d)).Handler.Verify(context.Background(), nwEnv(&nwJournal{}),
				nwJSON(t, NetworkCreateInput{}), nwJSON(t, NetworkView{}))
			if tt.has == "" {
				if err != nil {
					t.Fatalf("verify: %v", err)
				}
				return
			}
			if err == nil || !strings.HasPrefix(err.Error(), "panel:") || !strings.Contains(err.Error(), tt.has) {
				t.Errorf("verify error %v, want a panel: error holding %q", err, tt.has)
			}
		})
	}
}

// TestNwCreateObserve: a creation is found by the exact name, a made network by its id; the state is the network's
// created document, and a missing network is the null state.
func TestNwCreateObserve(t *testing.T) {
	ctx := context.Background()
	creation := core.Effect{Kind: core.EffectCreate, Resource: "panel.network:?darkpanel-panel"}
	made := core.Effect{Kind: core.EffectCreate, Resource: "panel.network:n1", State: core.EffectMade}
	doc := func(d *fdDocker, id string) json.RawMessage {
		t.Helper()
		n, err := d.InspectNetwork(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := dstate.NetworkCreated(&n)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	observe := func(d *fdDocker, e core.Effect) (core.Observation, error) {
		return networkCreateAction(nwDeps(d)).Handler.Observe(ctx, nwEnv(nil), e)
	}
	t.Run("creation not made", func(t *testing.T) {
		d := fdNewDocker()
		d.AddNetwork(nwNet("darkpanel-panel-old", nil, "10.1.0.0/24"))
		d.AddNetwork(nwNet("xdarkpanel-panel", nil, "10.2.0.0/24"))
		obs, err := observe(d, creation)
		if err != nil || obs.Matches != 0 || string(obs.Current.State) != "null" {
			t.Errorf("observation %+v, %v; want no match and the null state", obs, err)
		}
	})
	t.Run("creation made", func(t *testing.T) {
		d := fdNewDocker()
		n := nwPanelNet("172.31.255.248/29")
		n.ID = "n1"
		d.AddNetwork(n)
		obs, err := observe(d, creation)
		if err != nil || obs.Matches != 1 || obs.Resource != "panel.network:n1" || !obs.Exclusive || !bytes.Equal(obs.Current.State, doc(d, "n1")) {
			t.Errorf("observation %+v, %v; want panel.network:n1 with the network's document", obs, err)
		}
	})
	t.Run("creation matches two networks", func(t *testing.T) {
		d := fdNewDocker()
		for _, id := range []string{"n2", "n1"} {
			n := nwPanelNet("172.31.255.248/29")
			n.ID = id
			d.AddNetwork(n)
		}
		obs, err := observe(d, creation)
		if err != nil {
			t.Fatal(err)
		}
		if obs.Matches != 2 || obs.Resource != "" || obs.Exclusive || string(obs.Current.State) != "null" {
			t.Errorf("observation %+v; want two matches, no resource and the null state", obs)
		}
		if len(obs.Candidates) != 2 {
			t.Fatalf("candidates %+v, want two", obs.Candidates)
		}
		for i, id := range []string{"n1", "n2"} {
			c := obs.Candidates[i]
			if c.Resource != networkResource(id) || !bytes.Equal(c.State.State, doc(d, id)) {
				t.Errorf("candidate %d is %s with %s, want %s with its document", i, c.Resource, c.State.State, networkResource(id))
			}
		}
	})
	t.Run("made network by id", func(t *testing.T) {
		d := fdNewDocker()
		n := nwPanelNet("172.31.255.248/29")
		n.ID = "n1"
		d.AddNetwork(n)
		obs, err := observe(d, made)
		if err != nil || obs.Matches != 1 || !obs.Exclusive || !bytes.Equal(obs.Current.State, doc(d, "n1")) || !strings.Contains(string(obs.Current.State), `"unused":true`) {
			t.Errorf("observation %+v, %v", obs, err)
		}
		nwAttach(d, NetworkName, "web")
		obs, err = observe(d, made)
		if err != nil || !strings.Contains(string(obs.Current.State), `"unused":false`) {
			t.Errorf("observation with a container attached %s, %v; want unused false", obs.Current.State, err)
		}
	})
	t.Run("made network gone", func(t *testing.T) {
		obs, err := observe(fdNewDocker(), made)
		if err != nil || string(obs.Current.State) != "null" {
			t.Errorf("observation %+v, %v; want the null state", obs, err)
		}
	})
	t.Run("foreign resources", func(t *testing.T) {
		for _, r := range []string{"panel.network:", "panel.network:?", "npm.network:n1", "n1"} {
			if _, err := observe(fdNewDocker(), core.Effect{Kind: core.EffectCreate, Resource: r}); err == nil {
				t.Errorf("Observe(%q) succeeded", r)
			}
		}
	})
	t.Run("engine unavailable", func(t *testing.T) {
		d := fdNewDocker()
		d.Fail("ListNetworks", dockerapi.ErrUnavailable)
		if _, err := observe(d, creation); !errors.Is(err, dockerapi.ErrUnavailable) {
			t.Errorf("error %v, want the Engine's", err)
		}
	})
}

// TestNwCreateInverse: the undo removes the network the creation made while no container is attached, an absent network
// is undone, and a network in use or an Engine refusal is a definite failure that removed nothing.
func TestNwCreateInverse(t *testing.T) {
	ctx := context.Background()
	setup := func(t *testing.T) (*fdDocker, core.ActionDef, core.Effect) {
		t.Helper()
		d := fdNewDocker()
		j := &nwJournal{}
		if _, err := nwApplyCreate(t, d, j); err != nil {
			t.Fatal(err)
		}
		return d, networkCreateAction(nwDeps(d)), j.list[0]
	}
	t.Run("removes and converges", func(t *testing.T) {
		d, def, e := setup(t)
		for i := range 2 {
			if _, err := def.Handler.Inverse(ctx, nwEnv(nil), e); err != nil {
				t.Fatalf("inverse %d: %v", i+1, err)
			}
		}
		if !slices.Equal(d.Removed, []string{"darkpanel-panel"}) {
			t.Errorf("removed %v, want the network once", d.Removed)
		}
	})
	t.Run("container attached", func(t *testing.T) {
		d, def, e := setup(t)
		nwAttach(d, NetworkName, "web")
		_, err := def.Handler.Inverse(ctx, nwEnv(nil), e)
		if err == nil || !core.IsDefinite(err) || !strings.Contains(err.Error(), "attached") {
			t.Errorf("error %v, want a definite refusal naming the attached container", err)
		}
		if len(d.Removed) != 0 {
			t.Errorf("removed %v with a container attached", d.Removed)
		}
	})
	t.Run("refused by the Engine", func(t *testing.T) {
		d, def, e := setup(t)
		d.Fail("RemoveNetwork", &dockerapi.APIError{StatusCode: http.StatusForbidden, Message: "in use"})
		if _, err := def.Handler.Inverse(ctx, nwEnv(nil), e); err == nil || !core.IsDefinite(err) {
			t.Errorf("error %v, want a definite failure", err)
		}
	})
	t.Run("failed by the Engine", func(t *testing.T) {
		d, def, e := setup(t)
		d.Fail("RemoveNetwork", &dockerapi.APIError{StatusCode: http.StatusInternalServerError, Message: "boom"})
		if _, err := def.Handler.Inverse(ctx, nwEnv(nil), e); err == nil || core.IsDefinite(err) {
			t.Errorf("error %v, want a failure that is not definite", err)
		}
	})
	t.Run("creation resource", func(t *testing.T) {
		_, def, e := setup(t)
		e.Resource = "panel.network:?darkpanel-panel"
		if _, err := def.Handler.Inverse(ctx, nwEnv(nil), e); err == nil {
			t.Error("inverse of an unmade creation succeeded")
		}
	})
}

// TestNwCreateDeclaration: the action is a compensable, undoable agent action under the panel lock for admins, its
// schemas reflect, and the registry accepts it.
func TestNwCreateDeclaration(t *testing.T) {
	def := networkCreateAction(nwDeps(fdNewDocker()))
	s := def.Spec
	if s.Name != "panel.network.create" || s.Version != 1 || s.Class != core.ClassCompensable || s.Side != core.SideAgent ||
		!slices.Equal(s.LockKeys, []string{"panel"}) || s.MinScope != core.ScopeAdmin || s.CompensatedBy != "" || s.ForwardOnly {
		t.Errorf("spec %+v", s)
	}
	if def.Handler.Inverse == nil || def.Handler.Observe == nil {
		t.Error("the action has no undo or observer")
	}
	if _, err := schema.Input(def.InputType); err != nil {
		t.Error(err)
	}
	if _, err := schema.Output(def.OutputType); err != nil {
		t.Error(err)
	}
	if _, err := core.NewRegistry(nwModule{defs: []core.ActionDef{def}}); err != nil {
		t.Errorf("registry: %v", err)
	}
}

// nwModule registers actions, so the registry's own checks run on them.
type nwModule struct{ defs []core.ActionDef }

func (m nwModule) Manifest() core.Manifest { return core.Manifest{ID: ID, Version: 1, Actions: m.defs} }
