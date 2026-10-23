package dispatch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

var (
	refDrvReveal      = core.ActionRef{Name: "drv.key.reveal", Version: 1}
	refDrvInternal    = core.ActionRef{Name: "drv.cert.get", Version: 1}
	refDrvRead        = core.ActionRef{Name: "drv.item.get", Version: 1}
	refDrvCtlReveal   = core.ActionRef{Name: "drv.ctl.reveal", Version: 1}
	refDrvCtlInternal = core.ActionRef{Name: "drv.ctl.get", Version: 1}
)

type drvIn struct {
	Name string `json:"name"`
}

type drvOut struct {
	Secret string `json:"secret" secret:"true"`
}

func drvValue(_ context.Context, env core.Env, in drvIn) (core.Revealed[drvOut], error) {
	return core.Revealed[drvOut]{Identity: "drv secret " + in.Name, Value: drvOut{Secret: "s-" + in.Name + "-" + env.Principal.Actor}}, nil
}

// drvModule has a reveal action and an Internal read on each side, and a
// plain agent read.
type drvModule struct{}

func (drvModule) Manifest() core.Manifest {
	return core.Manifest{ID: "drv", Version: 1, DefaultEnabled: true, Actions: []core.ActionDef{
		core.DefineReveal(core.ActionSpec{Name: refDrvReveal.Name, Version: 1, Side: core.SideAgent, MinScope: core.ScopeAdmin}, drvValue),
		core.DefineInternalRead(core.ActionSpec{Name: refDrvInternal.Name, Version: 1, Side: core.SideAgent}, drvValue),
		core.DefineRead(core.ActionSpec{Name: refDrvRead.Name, Version: 1, Side: core.SideAgent}, echo{}),
		core.DefineReveal(core.ActionSpec{Name: refDrvCtlReveal.Name, Version: 1, Side: core.SideController, MinScope: core.ScopeAdmin},
			drvValue),
		core.DefineInternalRead(core.ActionSpec{Name: refDrvCtlInternal.Name, Version: 1, Side: core.SideController}, drvValue),
	}}
}

// drvAgent records the reveals h1's agent receives and reports the actions
// in unavailable as unavailable, with their reasons.
type drvAgent struct {
	mu          sync.Mutex
	calls       []string // "<action> internal=<bool> as <actor> <input>"
	unavailable map[string]string
}

func (a *drvAgent) recorded() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.calls)
}

func (a *drvAgent) availability(_ context.Context, moduleID string) (core.ModuleAvailability, error) {
	m := core.ModuleAvailability{Module: moduleID, Available: true}
	for _, name := range []string{refDrvReveal.Name, refDrvInternal.Name, refDrvRead.Name} {
		reason := a.unavailable[name]
		m.Actions = append(m.Actions, core.ActionAvailability{Name: name, Available: reason == "", Reason: reason})
	}
	return m, nil
}

// drvDispatcher is a dispatcher of a controller at version over a harness
// serving drvModule. Its h1 agent is the harness's runtime reporting
// agentVersion in Hello ("" keeps the runtime's) and the actions in
// unavailable as unavailable. The controller engine journals to the
// returned journal, and every dispatcher ingest is recorded.
func drvDispatcher(t *testing.T, version, agentVersion string, unavailable map[string]string) (*harness, *Dispatcher, *drvAgent, *coretest.MemJournal, *ingestCalls) {
	t.Helper()
	h := newHarnessWith(t, drvModule{})
	a := &drvAgent{unavailable: unavailable}
	h.clients.Add(testHost, &coretest.FakeAgent{
		HelloFunc: func(ctx context.Context) (core.AgentHello, error) {
			hello, err := h.rt.Hello(ctx)
			if agentVersion != "" {
				hello.BinaryVersion = agentVersion
			}
			return hello, err
		},
		ReadFunc:         h.rt.Read,
		AvailabilityFunc: a.availability,
		RevealFunc: func(ctx context.Context, p core.Principal, ref core.ActionRef, input json.RawMessage, internal bool) (json.RawMessage, error) {
			a.mu.Lock()
			a.calls = append(a.calls, fmt.Sprintf("%s internal=%t as %s %s", ref.Name, internal, p.Actor, input))
			a.mu.Unlock()
			return h.rt.Reveal(ctx, p, ref, input, internal)
		},
	})
	journal := &coretest.MemJournal{}
	cfg := h.config(journal)
	cfg.Version = version
	ingests := &ingestCalls{}
	cfg.Ingest = ingests
	d, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return h, d, a, journal, ingests
}

func TestDispatcherReveal(t *testing.T) {
	ctx := context.Background()
	h, d, a, journal, ingests := drvDispatcher(t, "", "", nil)
	in := json.RawMessage(`{"name":"a"}`)
	out, err := d.Reveal(ctx, alice, testHost, refDrvReveal, in)
	if err != nil || string(out) != `{"secret":"s-a-user:alice"}` {
		t.Fatalf("agent reveal = %s, %v; want the value unredacted", out, err)
	}
	if want := []string{`drv.key.reveal internal=false as user:alice {"name":"a"}`}; !slices.Equal(a.recorded(), want) {
		t.Errorf("the agent got %q, want %q", a.recorded(), want)
	}
	out, err = d.Reveal(ctx, alice, "", refDrvCtlReveal, in)
	if err != nil || string(out) != `{"secret":"s-a-user:alice"}` {
		t.Fatalf("controller reveal = %s, %v; want the value unredacted", out, err)
	}
	if e := journal.Entries(); len(e) != 1 || e[0].Phase != "reveal" || e[0].Action != refDrvCtlReveal.Name || e[0].Actor != alice.Actor {
		t.Errorf("controller journal %+v, want one reveal entry of %s", e, refDrvCtlReveal.Name)
	}
	for _, tc := range []struct {
		name, host string
		ref        core.ActionRef
	}{
		{"agent internal read", testHost, refDrvInternal},
		{"controller internal read", "", refDrvCtlInternal},
		{"plain read", testHost, refDrvRead},
		{"agent reveal without a host", "", refDrvReveal},
		{"controller reveal with a host", testHost, refDrvCtlReveal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := d.Reveal(ctx, alice, tc.host, tc.ref, in)
			_ = wantCode(t, err, errcode.ActionUnknown)
		})
	}
	if len(a.recorded()) != 1 || len(ingests.hosts) != 0 {
		t.Errorf("agent reveals %q, ingests %q; want the first reveal only and no ingest", a.recorded(), ingests.hosts)
	}
	if n := h.count("SELECT count(*) FROM core_plan_index") + h.count("SELECT count(*) FROM core_change_index"); n != 0 {
		t.Errorf("%d index rows after the reveals, want none", n)
	}
	if _, err := h.db.Writer().Exec("UPDATE core_module_state SET enabled = 0 WHERE module_id = 'drv'"); err != nil {
		t.Fatal(err)
	}
	_, err = d.Reveal(ctx, alice, testHost, refDrvReveal, in)
	_ = wantCode(t, err, errcode.ModuleUnavailable)
}

func TestDispatcherRevealRefusals(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name, version, agentVersion string
		unavailable                 map[string]string
		code                        errcode.Code
		detail                      string
	}{
		{"agent outside the support window", "1.3.0", "1.1.0", nil, errcode.HostVersionUnsupported, "outside the support window"},
		{"action unavailable", "", "", map[string]string{refDrvReveal.Name: "the vault is sealed"}, errcode.ModuleUnavailable,
			"the vault is sealed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, d, a, _, _ := drvDispatcher(t, tc.version, tc.agentVersion, tc.unavailable)
			_, err := d.Reveal(ctx, alice, testHost, refDrvReveal, json.RawMessage(`{"name":"a"}`))
			if e := wantCode(t, err, tc.code); !strings.Contains(e.Detail, tc.detail) {
				t.Errorf("detail %q, want %q", e.Detail, tc.detail)
			}
			if len(a.recorded()) != 0 {
				t.Errorf("a refused reveal reached the agent: %q", a.recorded())
			}
		})
	}
}

// The dispatcher logs when an agent's availability cannot be had, so this
// is where a revealed value could leak into its log; an agent's error must
// come back as the agent wrote it, never wrapped with the input or a value.
func TestDispatcherRevealKeepsTheValueOutOfLogsAndErrors(t *testing.T) {
	ctx := context.Background()
	h, d, _, _, _ := drvDispatcher(t, "", "", nil)
	var logs bytes.Buffer
	d.cfg.Log = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	refused := errors.New("drv: the vault refused")
	h.clients.Add(testHost, &coretest.FakeAgent{
		HelloFunc: h.rt.Hello,
		AvailabilityFunc: func(context.Context, string) (core.ModuleAvailability, error) {
			return core.ModuleAvailability{}, errors.New("drv: availability is down")
		},
		RevealFunc: func(ctx context.Context, p core.Principal, ref core.ActionRef, input json.RawMessage, internal bool) (json.RawMessage, error) {
			if string(input) == `{"name":"fail"}` {
				return nil, refused
			}
			return h.rt.Reveal(ctx, p, ref, input, internal)
		},
	})
	out, err := d.Reveal(ctx, alice, testHost, refDrvReveal, json.RawMessage(`{"name":"a"}`))
	if err != nil || string(out) != `{"secret":"s-a-user:alice"}` {
		t.Fatalf("agent reveal = %s, %v; want the value unredacted", out, err)
	}
	_, err = d.Reveal(ctx, alice, testHost, refDrvReveal, json.RawMessage(`{"name":"fail"}`))
	if !errors.Is(err, refused) || err.Error() != refused.Error() {
		t.Errorf("err = %v, want the agent's error unchanged", err)
	}
	if !strings.Contains(logs.String(), "availability unknown") {
		t.Fatalf("the availability warning was not logged: %q", logs.String())
	}
	if strings.Contains(logs.String(), "s-a-") {
		t.Errorf("the revealed value reached the log: %q", logs.String())
	}
}

func TestDispatcherReadInternal(t *testing.T) {
	ctx := context.Background()
	_, d, a, journal, _ := drvDispatcher(t, "", "", nil)
	sys := core.Principal{Actor: "system:listen.cert", Scope: core.ScopeAdmin}
	in := json.RawMessage(`{"name":"c"}`)
	out, err := d.ReadInternal(ctx, sys, testHost, refDrvInternal, in)
	if err != nil || string(out) != `{"secret":"s-c-system:listen.cert"}` {
		t.Fatalf("agent internal read = %s, %v", out, err)
	}
	if want := []string{`drv.cert.get internal=true as system:listen.cert {"name":"c"}`}; !slices.Equal(a.recorded(), want) {
		t.Errorf("the agent got %q, want %q", a.recorded(), want)
	}
	out, err = d.ReadInternal(ctx, sys, "", refDrvCtlInternal, in)
	if err != nil || string(out) != `{"secret":"s-c-system:listen.cert"}` {
		t.Fatalf("controller internal read = %s, %v", out, err)
	}
	if e := journal.Entries(); len(e) != 1 || e[0].Phase != "reveal" || e[0].Action != refDrvCtlInternal.Name || e[0].Actor != sys.Actor {
		t.Errorf("controller journal %+v, want one reveal entry of %s", e, refDrvCtlInternal.Name)
	}
	for _, ref := range []core.ActionRef{refDrvReveal, refDrvRead} {
		_, err := d.ReadInternal(ctx, sys, testHost, ref, in)
		_ = wantCode(t, err, errcode.ActionUnknown)
	}
	if len(a.recorded()) != 1 {
		t.Errorf("the agent got %q, want only the internal read", a.recorded())
	}
}
