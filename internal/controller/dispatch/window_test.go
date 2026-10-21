package dispatch

import (
	"context"
	"encoding/json"
	"maps"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/controller/coreactions"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/schemas"
)

var refUpdateStatus = core.ActionRef{Name: "core.update.status", Version: 1}

// newWindowHarness is a dispatcher of a controller at version whose registry
// also has an agent-side core.update.status, as the assembled controller's
// does.
func newWindowHarness(t *testing.T, version string) (*harness, *Dispatcher) {
	t.Helper()
	h := newHarness(t)
	status := core.DefineRead(core.ActionSpec{Name: refUpdateStatus.Name, Version: 1, Side: core.SideAgent}, echo{})
	var err error
	if h.reg, err = core.NewRegistry(append(modules(), coreactions.Module(coreactions.Deps{Extra: []core.ActionDef{status}}))...); err != nil {
		t.Fatal(err)
	}
	h.hosts = NewHosts(HostsConfig{DB: h.db, Registry: h.reg, Clients: h.clients, Clock: h.clock, Log: h.hosts.cfg.Log})
	cfg := h.config(&coretest.MemAudit{})
	cfg.Version = version
	d, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return h, d
}

// agentAt gives h1 a new session whose agent is the runtime reporting
// version and protocol; it answers core.update.status itself, which the
// fixture runtime does not have, and reports that action's schema hash.
func (h *harness) agentAt(version string, protocol int) {
	h.t.Helper()
	def, _ := h.reg.Action(refUpdateStatus)
	hash, err := schemas.ActionHash(def)
	if err != nil {
		h.t.Fatal(err)
	}
	h.clients.Add(testHost, &coretest.FakeAgent{
		HelloFunc: func(ctx context.Context) (core.AgentHello, error) {
			hello, err := h.rt.Hello(ctx)
			hello.BinaryVersion, hello.ProtocolVersion = version, protocol
			hello.SchemaHashes = maps.Clone(hello.SchemaHashes)
			hello.SchemaHashes[schemas.HashKey(refUpdateStatus)] = hash
			return hello, err
		},
		ReadFunc: func(ctx context.Context, p core.Principal, a core.ActionRef, input json.RawMessage) (json.RawMessage, error) {
			if a == refUpdateStatus {
				return json.RawMessage(`{"transactions":[]}`), nil
			}
			return h.rt.Read(ctx, p, a, input)
		},
		ResourceFunc: h.rt.Resource,
		PlanFunc:     h.rt.Plan,
		ApplyFunc:    h.rt.Apply,
	})
}

func TestVersionWindow(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		controller, agent string
		protocol          int
		supported         bool
	}{
		{"1.3.0", "1.3.0", 1, true},
		{"1.3.0", "1.3.4", 1, true},
		{"1.3.0", "1.2.9", 1, true},
		{"1.3.0", "1.1.0", 1, false},
		{"1.3.0", "1.4.0", 1, false},
		{"1.3.0", "2.3.0", 1, false},
		{"1.3.0", "1.3.0", 2, false},
		{"1.3.0", "dev", 1, false},
		{"dev", "dev", 1, true},
		{"", "0.1.0", 1, true},
	} {
		h, d := newWindowHarness(t, tc.controller)
		h.agentAt(tc.agent, tc.protocol)
		name := tc.agent + " under " + tc.controller
		calls := map[string]func() error{
			"read": func() error {
				_, err := d.Read(ctx, alice, testHost, refGet, json.RawMessage(`{"name":"a"}`))
				return err
			},
			"resource": func() error { _, err := d.Resource(ctx, alice, testHost, "kv.status"); return err },
			"plan": func() error {
				_, err := d.Plan(ctx, alice, testHost, refSet, json.RawMessage(`{"name":"a","value":"1"}`), core.PlanOptions{})
				return err
			},
		}
		for call, run := range calls {
			err := run()
			switch {
			case tc.supported && err != nil:
				t.Errorf("%s: %s = %v, want it dispatched", name, call, err)
			case !tc.supported:
				e := wantCode(t, err, errcode.HostVersionUnsupported)
				if !strings.Contains(e.Detail, "host h1 runs agent "+tc.agent) {
					t.Errorf("%s: %s detail %q does not name the agent's version", name, call, e.Detail)
				}
			}
		}
		if out, err := d.Read(ctx, alice, testHost, refUpdateStatus, json.RawMessage(`{"name":"x"}`)); err != nil ||
			string(out) != `{"transactions":[]}` {
			t.Errorf("%s: core.update.status = %s, %v; want it dispatched in every case", name, out, err)
		}
	}
}

// TestApplyChecksTheWindowAgain: an agent replaced by one outside the window
// after the plan cannot apply it.
func TestApplyChecksTheWindowAgain(t *testing.T) {
	ctx := context.Background()
	h, d := newWindowHarness(t, "1.3.0")
	h.agentAt("1.3.0", 1)
	pv := h.plan(d, testHost, refSet, `{"name":"a","value":"1"}`)
	h.agentAt("1.4.0", 1)
	_, err := d.Apply(ctx, alice, testHost, refSet, pv.PlanID, core.ApplyOptions{})
	_ = wantCode(t, err, errcode.HostVersionUnsupported)
	h.agentAt("1.2.0", 1)
	if v, err := d.Apply(ctx, alice, testHost, refSet, pv.PlanID, core.ApplyOptions{}); err != nil || v.State != core.StatePending {
		t.Errorf("Apply from a previous-minor agent = %+v, %v; want the pending change", v, err)
	}
}
