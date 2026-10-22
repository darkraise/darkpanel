package dispatch

import (
	"context"
	"encoding/json"
	"net/netip"
	"sync"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
)

// The fixture module "dg" has a guarded agent-side action and a guarded
// controller-side action; their handlers record the guard context of every
// plan and the call context of every apply.

var (
	refDgAgent = core.ActionRef{Name: "dg.rule.set", Version: 1}
	refDgCtl   = core.ActionRef{Name: "dg.ctl.set", Version: 1}
)

type dgInput struct {
	core.Guarded
	Name string `json:"name"`
}

type dgOut struct {
	Name string `json:"name"`
}

// dgSeen holds, per side, the guard context of the last plan and the call
// context of the last apply.
type dgSeen struct {
	mu     sync.Mutex
	guards map[core.Side]*core.GuardContext
	calls  map[core.Side]core.CallContext
}

func (s *dgSeen) guard(side core.Side) *core.GuardContext {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.guards[side]
}

func (s *dgSeen) call(side core.Side) core.CallContext {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[side]
}

type dgHandler struct {
	side core.Side
	seen *dgSeen
}

func (h dgHandler) Plan(_ context.Context, _ core.Env, in dgInput) (core.PlanOutcome, error) {
	h.seen.mu.Lock()
	defer h.seen.mu.Unlock()
	h.seen.guards[h.side] = in.Guard
	return core.PlanOutcome{Diff: []core.DiffLine{{Op: "+", Text: in.Name}}, StateHash: "dg"}, nil
}

func (h dgHandler) Apply(_ context.Context, env core.Env, in dgInput) (dgOut, error) {
	h.seen.mu.Lock()
	defer h.seen.mu.Unlock()
	h.seen.calls[h.side] = env.Call
	return dgOut{Name: in.Name}, nil
}

func (dgHandler) Verify(context.Context, core.Env, dgInput, dgOut) error { return nil }

type dgModule struct{ seen *dgSeen }

func (m dgModule) Manifest() core.Manifest {
	guards := []core.GuardSpec{{ID: "dg.lockout", Overridable: true}}
	return core.Manifest{ID: "dg", Version: 1, DefaultEnabled: true, Actions: []core.ActionDef{
		core.DefineMutating(core.ActionSpec{Name: refDgAgent.Name, Version: 1, Class: core.ClassCompensable,
			Side: core.SideAgent, CompensatedBy: refDgAgent.Name, Guards: guards}, dgHandler{core.SideAgent, m.seen}),
		core.DefineMutating(core.ActionSpec{Name: refDgCtl.Name, Version: 1, Class: core.ClassCompensable,
			Side: core.SideController, CompensatedBy: refDgCtl.Name, Guards: guards}, dgHandler{core.SideController, m.seen}),
	}}
}

// dgAgent is h1's agent: the harness runtime, with the raw input of every
// plan and the options of every apply recorded.
type dgAgent struct {
	core.AgentClient
	mu     sync.Mutex
	inputs []string
	opts   []core.ApplyOptions
}

func (a *dgAgent) Plan(ctx context.Context, p core.Principal, ref core.ActionRef, input json.RawMessage, opts core.PlanOptions) (core.PlanView, error) {
	a.mu.Lock()
	a.inputs = append(a.inputs, string(input))
	a.mu.Unlock()
	return a.AgentClient.Plan(ctx, p, ref, input, opts)
}

func (a *dgAgent) Apply(ctx context.Context, p core.Principal, planID string, opts core.ApplyOptions) (core.ChangeView, error) {
	a.mu.Lock()
	a.opts = append(a.opts, opts)
	a.mu.Unlock()
	return a.AgentClient.Apply(ctx, p, planID, opts)
}

func (a *dgAgent) lastInput() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.inputs[len(a.inputs)-1]
}

func (a *dgAgent) lastOpts() core.ApplyOptions {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.opts[len(a.opts)-1]
}

var (
	dgTopology = Topology{
		PanelListen: []netip.AddrPort{netip.MustParseAddrPort("127.0.0.1:8443")},
		AgentListen: []netip.AddrPort{netip.MustParseAddrPort("[::]:7443")},
		AgentPeers:  []netip.Addr{netip.MustParseAddr("192.0.2.30")},
	}
	dgPeer = core.Peer{Addr: netip.MustParseAddr("192.0.2.7"), Port: 51234}
	// dgFull is the guard context of dgTopology and dgPeer.
	dgFull = &core.GuardContext{Client: &dgPeer, PanelListen: dgTopology.PanelListen,
		AgentListen: dgTopology.AgentListen, AgentPeers: dgTopology.AgentPeers}
)

// newDg is a dispatcher with module dg and topology topo over a harness whose
// agent is a dgAgent.
func newDg(t *testing.T, topo func() Topology) (*Dispatcher, *dgSeen, *dgAgent) {
	t.Helper()
	seen := &dgSeen{guards: map[core.Side]*core.GuardContext{}, calls: map[core.Side]core.CallContext{}}
	h := newHarnessWith(t, dgModule{seen})
	agent := &dgAgent{AgentClient: h.rt}
	h.clients.Add(testHost, agent)
	cfg := h.config(&coretest.MemAudit{})
	cfg.Topology = topo
	d, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return d, seen, agent
}

// dgJSON is v as JSON, so guard contexts compare by their wire form.
func dgJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestPlanFillsGuard(t *testing.T) {
	d, seen, agent := newDg(t, func() Topology { return dgTopology })
	ctx := core.WithClientPeer(context.Background(), dgPeer)
	want := dgJSON(t, dgFull)
	for _, tc := range []struct {
		name, host string
		ref        core.ActionRef
		side       core.Side
		input      string
	}{
		{"an agent action", testHost, refDgAgent, core.SideAgent, `{"name":"a"}`},
		{"an agent action given a guard", testHost, refDgAgent, core.SideAgent,
			`{"name":"a","guard":{"agentPeers":["203.0.113.9"]},"GUARD":{}}`},
		{"a controller action given a guard", "", refDgCtl, core.SideController, `{"name":"a","guard":{}}`},
	} {
		if _, err := d.Plan(ctx, alice, tc.host, tc.ref, json.RawMessage(tc.input), core.PlanOptions{}); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got := dgJSON(t, seen.guard(tc.side)); got != want {
			t.Errorf("%s: the handler saw guard %s, want %s", tc.name, got, want)
		}
	}
	const unguarded = `{ "value": "1", "name": "a" }`
	if _, err := d.Plan(ctx, alice, testHost, refSet, json.RawMessage(unguarded), core.PlanOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := agent.lastInput(); got != unguarded {
		t.Errorf("the agent got input %s for an unguarded action, want %s byte for byte", got, unguarded)
	}
}

func TestPlanGuardWithoutPeer(t *testing.T) {
	for _, tc := range []struct {
		name string
		topo func() Topology
		want *core.GuardContext
	}{
		{"no client peer", func() Topology { return dgTopology }, &core.GuardContext{PanelListen: dgTopology.PanelListen,
			AgentListen: dgTopology.AgentListen, AgentPeers: dgTopology.AgentPeers}},
		{"no topology", nil, &core.GuardContext{}},
	} {
		d, seen, _ := newDg(t, tc.topo)
		if _, err := d.Plan(context.Background(), alice, testHost, refDgAgent, json.RawMessage(`{"name":"a"}`), core.PlanOptions{}); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got, want := dgJSON(t, seen.guard(core.SideAgent)), dgJSON(t, tc.want); got != want {
			t.Errorf("%s: guard %s, want %s", tc.name, got, want)
		}
	}
}

func TestGuardContextDropsZone(t *testing.T) {
	d, _, _ := newDg(t, func() Topology { return dgTopology })
	peer := core.Peer{Addr: netip.MustParseAddr("fe80::7%eth0"), Port: 51234}
	g := d.guardContext(core.WithClientPeer(context.Background(), peer))
	if g.Client == nil || g.Client.Addr != netip.MustParseAddr("fe80::7") || g.Client.Port != peer.Port {
		t.Errorf("client %+v, want fe80::7 port %d without a zone", g.Client, peer.Port)
	}
}

func TestApplySendsGuardRefresh(t *testing.T) {
	d, seen, agent := newDg(t, func() Topology { return dgTopology })
	ctx := core.WithClientPeer(context.Background(), dgPeer)
	want := dgJSON(t, dgFull)
	plan := func(hostID string, ref core.ActionRef, input string, call *core.CallContext) string {
		t.Helper()
		v, err := d.Plan(ctx, alice, hostID, ref, json.RawMessage(input), core.PlanOptions{Call: call})
		if err != nil {
			t.Fatal(err)
		}
		return v.PlanID
	}

	// A top-level agent apply gains a call context with the guard refresh.
	planID := plan(testHost, refSet, `{"name":"a","value":"1"}`, nil)
	if _, err := d.Apply(ctx, alice, testHost, refSet, planID, core.ApplyOptions{ConfirmNow: true}); err != nil {
		t.Fatal(err)
	}
	got := agent.lastOpts()
	if got.Call == nil || dgJSON(t, got.Call.GuardRefresh) != want || got.Call.ParentChangeID != "" || !got.ConfirmNow {
		t.Errorf("top-level apply options %+v, call %+v; want only the guard refresh %s added", got, got.Call, want)
	}

	// A composite step's apply keeps its call context, and the caller's is
	// not changed.
	call := &core.CallContext{ParentChangeID: "01COMPOSITE", ParentAction: "cfx.deploy.add", StepID: "a"}
	planID = plan(testHost, refDgAgent, `{"name":"b"}`, call)
	if _, err := d.Apply(ctx, alice, testHost, refDgAgent, planID, core.ApplyOptions{Call: call}); err != nil {
		t.Fatal(err)
	}
	got = agent.lastOpts()
	if got.Call == nil || got.Call == call || got.Call.ParentChangeID != call.ParentChangeID ||
		got.Call.StepID != call.StepID || dgJSON(t, got.Call.GuardRefresh) != want {
		t.Errorf("step apply call %+v, want a copy of %+v with the guard refresh", got.Call, call)
	}
	if call.GuardRefresh != nil {
		t.Error("the caller's call context was changed")
	}
	if seenRefresh := seen.call(core.SideAgent).GuardRefresh; dgJSON(t, seenRefresh) != want {
		t.Errorf("the agent handler's apply saw guard refresh %s, want %s", dgJSON(t, seenRefresh), want)
	}

	// A controller-side apply runs with the options as given.
	planID = plan("", refDgCtl, `{"name":"c"}`, nil)
	if _, err := d.Apply(ctx, alice, "", refDgCtl, planID, core.ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	if c := seen.call(core.SideController); c.GuardRefresh != nil {
		t.Errorf("the controller handler's apply saw guard refresh %s, want none", dgJSON(t, c.GuardRefresh))
	}
}
