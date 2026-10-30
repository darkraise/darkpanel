package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/netip"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/modules/docker/dstate"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

var epUser = core.Principal{Actor: "user:u1", UserID: "u1", Scope: core.ScopeOperate, Source: "203.0.113.7"}

// epModule registers only the two actions under test.
type epModule struct{ k dk }

func (m epModule) Manifest() core.Manifest {
	return core.Manifest{ID: ID, Version: 1, DefaultEnabled: true,
		Actions: []core.ActionDef{m.k.networkConnect(), m.k.networkDisconnect()}}
}

type epConnectCall struct {
	Network, Container string
	Endpoint           *dockerapi.EndpointSettings
}

type epDisconnectCall struct {
	Network, Container string
	Force              bool
}

// epDocker records the arguments of the two calls under test and makes the
// stored endpoint carry the aliases and static addresses it was given, so
// the tests do not depend on how the fake copies endpoint settings.
type epDocker struct {
	*fakeDocker
	mu          sync.Mutex
	connects    []epConnectCall
	disconnects []epDisconnectCall
}

func (d *epDocker) ConnectNetwork(ctx context.Context, network, container string, ep *dockerapi.EndpointSettings) error {
	d.mu.Lock()
	d.connects = append(d.connects, epConnectCall{network, container, ep})
	d.mu.Unlock()
	if err := d.fakeDocker.ConnectNetwork(ctx, network, container, ep); err != nil {
		return err
	}
	if c, ok := d.container(container); ok && ep != nil {
		for _, e := range c.NetworkSettings.Networks {
			if e != nil && e.NetworkID == network {
				e.Aliases, e.IPAMConfig = ep.Aliases, ep.IPAMConfig
			}
		}
		d.put(c)
	}
	return nil
}

func (d *epDocker) DisconnectNetwork(ctx context.Context, network, container string, force bool) error {
	d.mu.Lock()
	d.disconnects = append(d.disconnects, epDisconnectCall{network, container, force})
	d.mu.Unlock()
	return d.fakeDocker.DisconnectNetwork(ctx, network, container, force)
}

// epSeed gives the container's endpoint on networkID the aliases, a static
// IPv4 address and an endpoint id.
func epSeed(t *testing.T, c dockerapi.ContainerJSON, networkID string, aliases []string, ipv4 string) dockerapi.ContainerJSON {
	t.Helper()
	found := false
	for _, ep := range c.NetworkSettings.Networks {
		if ep == nil || ep.NetworkID != networkID {
			continue
		}
		ep.EndpointID, ep.Aliases = "seeded-endpoint", aliases
		ep.IPAMConfig = &dockerapi.EndpointIPAMConfig{IPv4Address: netip.MustParseAddr(ipv4)}
		found = true
	}
	if !found {
		t.Fatalf("container %s has no endpoint on network %s", c.Name, networkID)
	}
	return c
}

func epDB(t *testing.T) *store.DB {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	set, err := store.CoreMigrations(store.SideAgent)
	if err != nil {
		t.Fatal(err)
	}
	res, err := store.Migrate(ctx, db, store.MigrateOptions{
		Side: store.SideAgent, BinaryVersion: "test", BackupRoot: filepath.Join(t.TempDir(), "backups"),
		Keep: 5, Now: time.Now(),
	}, set, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Failed) > 0 {
		t.Fatalf("core migrations failed: %v", res.Failed[0].Err)
	}
	return db
}

type epFixture struct {
	t   *testing.T
	rec *epDocker
	eng *lifecycle.Engine
}

// newEPFixture is an agent engine serving the two actions over a fake
// Engine that holds ctr and nw.
func newEPFixture(t *testing.T, ctr dockerapi.ContainerJSON, nw dockerapi.Network) *epFixture {
	t.Helper()
	h := newDKHost(t)
	h.docker.put(ctr)
	h.docker.putNetwork(nw)
	rec := &epDocker{fakeDocker: h.docker}
	k := h.dk()
	k.d.docker = rec
	reg, err := core.NewRegistry(epModule{k: k})
	if err != nil {
		t.Fatal(err)
	}
	eng, err := lifecycle.New(lifecycle.Config{
		Side: core.SideAgent, Registry: reg, Validator: schema.NewValidator(), Journal: &coretest.MemJournal{}, Redact: schema.Redact,
		Base: core.Env{HostID: "h1", DB: epDB(t), Sealer: coretest.Sealer{}, Clock: h.clock, Log: slog.New(slog.NewTextHandler(io.Discard, nil))},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(eng.Close)
	return &epFixture{t: t, rec: rec, eng: eng}
}

func (f *epFixture) plan(action, input string) (core.PlanView, error) {
	f.t.Helper()
	return f.eng.Plan(context.Background(), epUser, core.ActionRef{Name: action, Version: 1}, json.RawMessage(input), core.PlanOptions{})
}

// apply plans and applies action, failing the test when the plan is refused.
func (f *epFixture) apply(action, input string) (core.ChangeView, error) {
	f.t.Helper()
	pv, err := f.plan(action, input)
	if err != nil {
		f.t.Fatalf("plan %s: %v", action, err)
	}
	return f.eng.Apply(context.Background(), epUser, pv.PlanID, core.ApplyOptions{})
}

// undo undoes change id and waits until the undo has settled.
func (f *epFixture) undo(id string) core.ChangeView {
	f.t.Helper()
	ctx := context.Background()
	if _, err := f.eng.Undo(ctx, epUser, id, nil); err != nil {
		f.t.Fatalf("undo %s: %v", id, err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		v, err := f.eng.Change(ctx, id)
		if err != nil {
			f.t.Fatal(err)
		}
		if v.State != core.StateUndoing {
			return v
		}
		if time.Now().After(deadline) {
			f.t.Fatalf("change %s is still undoing", id)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (f *epFixture) effects(id string) []core.EffectView {
	f.t.Helper()
	effs, err := f.eng.Effects(context.Background(), id)
	if err != nil {
		f.t.Fatal(err)
	}
	return effs
}

// endpoint is the container's endpoint on the network, as the fake holds it.
func (f *epFixture) endpoint(containerID, networkID string) (dockerapi.EndpointSettings, bool) {
	f.t.Helper()
	c, ok := f.rec.container(containerID)
	if !ok {
		f.t.Fatalf("the fake has no container %s", containerID)
	}
	for _, ep := range c.NetworkSettings.Networks {
		if ep != nil && ep.NetworkID == networkID {
			return *ep, true
		}
	}
	return dockerapi.EndpointSettings{}, false
}

func epJSONEqual(t *testing.T, what string, got, want json.RawMessage) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatalf("%s: %s is not JSON: %v", what, got, err)
	}
	if err := json.Unmarshal(want, &w); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(g, w) {
		t.Errorf("%s = %s, want %s", what, got, want)
	}
}

func epWantRefusal(t *testing.T, err error, code errcode.Code, path, part string) {
	t.Helper()
	if err == nil {
		t.Fatalf("no error, want %s", code)
	}
	e := errcode.From(err)
	if e.Code != code {
		t.Fatalf("error = %v, want code %s", err, code)
	}
	if path != "" && (len(e.Fields) == 0 || e.Fields[0].Path != path) {
		t.Errorf("fields %+v, want the first at %s", e.Fields, path)
	}
	if part != "" && !strings.Contains(e.Detail, part) {
		t.Errorf("detail %q does not contain %q", e.Detail, part)
	}
}

func epInput(network, container, extra string) string {
	if extra != "" {
		extra = "," + extra
	}
	return fmt.Sprintf(`{"network":%q,"container":%q%s}`, network, container, extra)
}

func TestEPConnectAppliesAndUndoes(t *testing.T) {
	web, backend := dkContainer("web", dkRunning), dkNetwork("backend")
	f := newEPFixture(t, web, backend)
	in := epInput(backend.ID, "web", `"aliases":["api"],"ipv4":"172.20.0.9"`)

	pv, err := f.plan("docker.network.connect", in)
	if err != nil {
		t.Fatal(err)
	}
	wantDiff := []core.DiffLine{
		{Op: "+", Text: "container web joins network backend"},
		{Op: "+", Text: "alias api"},
		{Op: "+", Text: "ipv4 172.20.0.9"},
	}
	if !reflect.DeepEqual(pv.Diff, wantDiff) {
		t.Errorf("diff %+v, want %+v", pv.Diff, wantDiff)
	}
	if len(f.rec.connects) != 0 {
		t.Fatalf("planning connected the container: %+v", f.rec.connects)
	}

	cv, err := f.eng.Apply(context.Background(), epUser, pv.PlanID, core.ApplyOptions{})
	if err != nil || cv.State != core.StateCompleted {
		t.Fatalf("apply = %+v, %v; want a completed change", cv, err)
	}
	ep, on := f.endpoint(web.ID, backend.ID)
	if !on || ep.EndpointID == "" {
		t.Fatalf("endpoint %+v (on %v), want the container on the network", ep, on)
	}
	if len(f.rec.connects) != 1 {
		t.Fatalf("connect calls %+v, want one", f.rec.connects)
	}
	got := f.rec.connects[0]
	if got.Network != backend.ID || got.Container != web.ID || got.Endpoint == nil ||
		!reflect.DeepEqual(got.Endpoint.Aliases, []string{"api"}) || got.Endpoint.IPAMConfig == nil ||
		got.Endpoint.IPAMConfig.IPv4Address != netip.MustParseAddr("172.20.0.9") || got.Endpoint.IPAMConfig.IPv6Address.IsValid() {
		t.Errorf("connect call %+v (endpoint %+v), want the ids, alias api and static address 172.20.0.9", got, got.Endpoint)
	}
	var view EndpointView
	if err := json.Unmarshal(cv.Output, &view); err != nil {
		t.Fatal(err)
	}
	wantView := EndpointView{Network: "backend", NetworkID: backend.ID, Container: "web", EndpointID: ep.EndpointID, Aliases: []string{"api"}, IPv4: "172.20.0.9"}
	if !reflect.DeepEqual(view, wantView) {
		t.Errorf("output %+v, want %+v", view, wantView)
	}

	effs := f.effects(cv.ID)
	if len(effs) != 1 {
		t.Fatalf("effects %+v, want one", effs)
	}
	e := effs[0]
	if e.Resource != endpointResource(web.ID, backend.ID) || e.Kind != core.EffectModify || e.State != core.EffectMade || e.Local {
		t.Errorf("effect %+v, want a made, non-local modify of the endpoint", e)
	}
	wantBefore, err := dstate.Endpoint(web.ID, backend.ID, nil, dstate.Restored)
	if err != nil {
		t.Fatal(err)
	}
	wantPost, err := dstate.Endpoint(web.ID, backend.ID, &ep, dstate.Recorded)
	if err != nil {
		t.Fatal(err)
	}
	epJSONEqual(t, "before", e.Before.State, wantBefore)
	if e.Post == nil {
		t.Fatal("the effect has no post state")
	}
	epJSONEqual(t, "post", e.Post.State, wantPost)

	uv := f.undo(cv.ID)
	if uv.State != core.StateReverted {
		t.Fatalf("undo settled %s (%+v), want reverted", uv.State, uv.Error)
	}
	if _, on := f.endpoint(web.ID, backend.ID); on {
		t.Error("the container is still on the network after the undo")
	}
	if want := []epDisconnectCall{{backend.ID, web.ID, false}}; !reflect.DeepEqual(f.rec.disconnects, want) {
		t.Errorf("disconnect calls %+v, want %+v", f.rec.disconnects, want)
	}
	if s := f.effects(cv.ID)[0].State; s != core.EffectUndone {
		t.Errorf("effect is %s after the undo, want undone", s)
	}
}

func TestEPConnectRefusals(t *testing.T) {
	backend := dkNetwork("backend")
	ownedNet := dkNetwork("backend", func(n *dockerapi.Network) { n.Labels = map[string]string{core.LabelModule: "npm"} })
	web := dkContainer("web", dkRunning)
	tests := []struct {
		name  string
		ctr   dockerapi.ContainerJSON
		net   dockerapi.Network
		input string
		code  errcode.Code
		path  string
		part  string
	}{
		{"empty container", web, backend, epInput(backend.ID, "", ""), errcode.InvalidInput, "/container", ""},
		{"empty network", web, backend, epInput("", "web", ""), errcode.InvalidInput, "/network", ""},
		{"unparsable ipv4", web, backend, epInput(backend.ID, "web", `"ipv4":"nope"`), errcode.InvalidInput, "/ipv4", ""},
		{"ipv6 in ipv4", web, backend, epInput(backend.ID, "web", `"ipv4":"fd00::1"`), errcode.InvalidInput, "/ipv4", ""},
		{"ipv4 in ipv6", web, backend, epInput(backend.ID, "web", `"ipv6":"10.0.0.1"`), errcode.InvalidInput, "/ipv6", ""},
		{"bad alias", web, backend, epInput(backend.ID, "web", `"aliases":["ok","bad alias"]`), errcode.InvalidInput, "/aliases/1", ""},
		{"unknown container", web, backend, epInput(backend.ID, "ghost", ""), errcode.NotFound, "", "ghost"},
		{"unknown network", web, backend, epInput("ghost", "web", ""), errcode.NotFound, "", "ghost"},
		{"owned container", dkContainer("web", dkLabels(map[string]string{core.LabelModule: "npm"})), backend, epInput(backend.ID, "web", ""), errcode.DockerOwned, "", "container web belongs to module npm"},
		{"owned network", web, ownedNet, epInput(ownedNet.ID, "web", ""), errcode.DockerOwned, "", "network backend belongs to module npm"},
		{"already connected", dkContainer("web", dkRunning, dkOnNetwork(backend.ID)), backend, epInput(backend.ID, "web", ""), errcode.InvalidInput, "", "already connected"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newEPFixture(t, tc.ctr, tc.net)
			_, err := f.plan("docker.network.connect", tc.input)
			epWantRefusal(t, err, tc.code, tc.path, tc.part)
			if len(f.rec.connects) != 0 {
				t.Errorf("connect calls %+v, want none", f.rec.connects)
			}
		})
	}
}

func TestEPDisconnectAppliesAndUndoes(t *testing.T) {
	for _, force := range []bool{false, true} {
		t.Run("force="+strconv.FormatBool(force), func(t *testing.T) {
			backend := dkNetwork("backend")
			web := epSeed(t, dkContainer("web", dkRunning, dkOnNetwork(backend.ID)), backend.ID, []string{"web", "api"}, "172.20.0.9")
			f := newEPFixture(t, web, backend)
			seeded, _ := f.endpoint(web.ID, backend.ID)
			in := epInput(backend.ID, "web", "")
			if force {
				in = epInput(backend.ID, "web", `"force":true`)
			}

			pv, err := f.plan("docker.network.disconnect", in)
			if err != nil {
				t.Fatal(err)
			}
			wantDiff := []core.DiffLine{
				{Op: "-", Text: "container web leaves network backend"},
				{Op: "-", Text: "alias api"},
				{Op: "-", Text: "alias web"},
				{Op: "-", Text: "ipv4 172.20.0.9"},
			}
			if !reflect.DeepEqual(pv.Diff, wantDiff) {
				t.Errorf("diff %+v, want %+v", pv.Diff, wantDiff)
			}

			cv, err := f.eng.Apply(context.Background(), epUser, pv.PlanID, core.ApplyOptions{})
			if err != nil || cv.State != core.StateCompleted {
				t.Fatalf("apply = %+v, %v; want a completed change", cv, err)
			}
			if _, on := f.endpoint(web.ID, backend.ID); on {
				t.Fatal("the container is still on the network")
			}
			if want := []epDisconnectCall{{backend.ID, web.ID, force}}; !reflect.DeepEqual(f.rec.disconnects, want) {
				t.Errorf("disconnect calls %+v, want %+v", f.rec.disconnects, want)
			}
			var view EndpointView
			if err := json.Unmarshal(cv.Output, &view); err != nil {
				t.Fatal(err)
			}
			wantView := EndpointView{Network: "backend", NetworkID: backend.ID, Container: "web", EndpointID: "seeded-endpoint", Aliases: []string{"api", "web"}, IPv4: "172.20.0.9"}
			if !reflect.DeepEqual(view, wantView) {
				t.Errorf("output %+v, want the detached endpoint %+v", view, wantView)
			}

			e := f.effects(cv.ID)[0]
			wantBefore, err := dstate.Endpoint(web.ID, backend.ID, &seeded, dstate.Restored)
			if err != nil {
				t.Fatal(err)
			}
			wantPost, err := dstate.Endpoint(web.ID, backend.ID, nil, dstate.Recorded)
			if err != nil {
				t.Fatal(err)
			}
			epJSONEqual(t, "before", e.Before.State, wantBefore)
			if e.Post == nil {
				t.Fatal("the effect has no post state")
			}
			epJSONEqual(t, "post", e.Post.State, wantPost)

			uv := f.undo(cv.ID)
			if uv.State != core.StateReverted {
				t.Fatalf("undo settled %s (%+v), want reverted", uv.State, uv.Error)
			}
			if _, on := f.endpoint(web.ID, backend.ID); !on {
				t.Error("the container is not back on the network after the undo")
			}
			if len(f.rec.connects) != 1 {
				t.Fatalf("connect calls %+v, want the one reconnect", f.rec.connects)
			}
			re := f.rec.connects[0]
			if re.Network != backend.ID || re.Container != web.ID || re.Endpoint == nil ||
				!reflect.DeepEqual(re.Endpoint.Aliases, []string{"api", "web"}) || re.Endpoint.IPAMConfig == nil ||
				re.Endpoint.IPAMConfig.IPv4Address != netip.MustParseAddr("172.20.0.9") {
				t.Errorf("reconnect %+v (endpoint %+v), want the journaled aliases and static address", re, re.Endpoint)
			}
			if s := f.effects(cv.ID)[0].State; s != core.EffectUndone {
				t.Errorf("effect is %s after the undo, want undone", s)
			}
		})
	}
}

func TestEPDisconnectRefusals(t *testing.T) {
	backend := dkNetwork("backend")
	on := dkContainer("web", dkRunning, dkOnNetwork(backend.ID))
	tests := []struct {
		name  string
		ctr   dockerapi.ContainerJSON
		input string
		code  errcode.Code
		part  string
	}{
		{"not connected", dkContainer("web", dkRunning), epInput(backend.ID, "web", ""), errcode.InvalidInput, "not connected"},
		{"unknown container", on, epInput(backend.ID, "ghost", ""), errcode.NotFound, "ghost"},
		{"unknown network", on, epInput("ghost", "web", ""), errcode.NotFound, "ghost"},
		{"owned container", dkContainer("web", dkOnNetwork(backend.ID), dkLabels(map[string]string{core.LabelModule: "npm"})), epInput(backend.ID, "web", ""), errcode.DockerOwned, "container web belongs to module npm"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newEPFixture(t, tc.ctr, backend)
			_, err := f.plan("docker.network.disconnect", tc.input)
			epWantRefusal(t, err, tc.code, "", tc.part)
			if len(f.rec.disconnects) != 0 {
				t.Errorf("disconnect calls %+v, want none", f.rec.disconnects)
			}
		})
	}
}

func TestEPApplyRefusedByTheDaemon(t *testing.T) {
	backend := dkNetwork("backend")
	tests := []struct {
		name, action, method string
		ctr                  dockerapi.ContainerJSON
		connected            bool
	}{
		{"connect", "docker.network.connect", "ConnectNetwork", dkContainer("web", dkRunning), false},
		{"disconnect", "docker.network.disconnect", "DisconnectNetwork", dkContainer("web", dkRunning, dkOnNetwork(backend.ID)), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newEPFixture(t, tc.ctr, backend)
			f.rec.fail(tc.method, &dockerapi.APIError{StatusCode: 403, Message: "refused"})
			cv, err := f.apply(tc.action, epInput(backend.ID, "web", ""))
			if cv.State != core.StateFailed {
				t.Fatalf("apply = %+v, %v; want a failed change", cv, err)
			}
			if _, on := f.endpoint(tc.ctr.ID, backend.ID); on != tc.connected {
				t.Errorf("container on the network = %v, want it unchanged (%v)", on, tc.connected)
			}
			effs := f.effects(cv.ID)
			if len(effs) != 1 || effs[0].State != core.EffectSkipped {
				t.Errorf("effects %+v, want one skipped effect: the daemon refused before changing anything", effs)
			}
		})
	}
}

func TestEPApplyPlansAgain(t *testing.T) {
	ctx := context.Background()
	backend := dkNetwork("backend")

	t.Run("connect after an outside connect", func(t *testing.T) {
		web := dkContainer("web", dkRunning)
		f := newEPFixture(t, web, backend)
		pv, err := f.plan("docker.network.connect", epInput(backend.ID, "web", ""))
		if err != nil {
			t.Fatal(err)
		}
		if err := f.rec.fakeDocker.ConnectNetwork(ctx, backend.ID, web.ID, nil); err != nil {
			t.Fatal(err)
		}
		_, err = f.eng.Apply(ctx, epUser, pv.PlanID, core.ApplyOptions{})
		epWantRefusal(t, err, errcode.InvalidInput, "", "already connected")
		if len(f.rec.connects) != 0 {
			t.Errorf("connect calls %+v, want none", f.rec.connects)
		}
	})

	t.Run("disconnect after the endpoint was renewed", func(t *testing.T) {
		web := epSeed(t, dkContainer("web", dkRunning, dkOnNetwork(backend.ID)), backend.ID, []string{"web"}, "172.20.0.9")
		f := newEPFixture(t, web, backend)
		pv, err := f.plan("docker.network.disconnect", epInput(backend.ID, "web", ""))
		if err != nil {
			t.Fatal(err)
		}
		if err := f.rec.fakeDocker.DisconnectNetwork(ctx, backend.ID, web.ID, false); err != nil {
			t.Fatal(err)
		}
		if err := f.rec.fakeDocker.ConnectNetwork(ctx, backend.ID, web.ID, nil); err != nil {
			t.Fatal(err)
		}
		_, err = f.eng.Apply(ctx, epUser, pv.PlanID, core.ApplyOptions{})
		epWantRefusal(t, err, errcode.PlanStateChanged, "", "")
		if len(f.rec.disconnects) != 0 {
			t.Errorf("disconnect calls %+v, want none", f.rec.disconnects)
		}
	})
}

func TestEPUndoStopsAfterALaterChange(t *testing.T) {
	ctx := context.Background()
	web, backend := dkContainer("web", dkRunning), dkNetwork("backend")
	f := newEPFixture(t, web, backend)
	cv, err := f.apply("docker.network.connect", epInput(backend.ID, "web", ""))
	if err != nil || cv.State != core.StateCompleted {
		t.Fatalf("apply = %+v, %v", cv, err)
	}
	if err := f.rec.fakeDocker.DisconnectNetwork(ctx, backend.ID, web.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := f.rec.fakeDocker.ConnectNetwork(ctx, backend.ID, web.ID, nil); err != nil {
		t.Fatal(err)
	}

	uv := f.undo(cv.ID)
	if uv.State != core.StateNeedsRepair {
		t.Fatalf("undo settled %s, want needs_repair", uv.State)
	}
	e := f.effects(cv.ID)[0]
	if e.State != core.EffectUnresolved || e.Reason != core.ReasonConflict {
		t.Errorf("effect is %s (%s), want an unresolved conflict", e.State, e.Reason)
	}
	if _, on := f.endpoint(web.ID, backend.ID); !on {
		t.Error("the undo detached an endpoint a later operation made")
	}
	if len(f.rec.disconnects) != 0 {
		t.Errorf("disconnect calls %+v, want none", f.rec.disconnects)
	}
}
