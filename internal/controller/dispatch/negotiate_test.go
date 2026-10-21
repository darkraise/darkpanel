package dispatch

import (
	"context"
	"encoding/json"
	"maps"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/schemas"
)

// olderSnapshot is ref's snapshot as a previous minor released it: the
// input of every kv item action had only its name.
func (h *harness) olderSnapshot(ref core.ActionRef) ([]byte, core.SchemaHash) {
	h.t.Helper()
	def, ok := h.reg.Action(ref)
	if !ok {
		h.t.Fatalf("%s is not registered", ref)
	}
	raw, err := schema.ActionSnapshot(def)
	if err != nil {
		h.t.Fatal(err)
	}
	var snap map[string]any
	if err := json.Unmarshal(raw, &snap); err != nil {
		h.t.Fatal(err)
	}
	for _, d := range snap["input"].(map[string]any)["$defs"].(map[string]any) {
		delete(d.(map[string]any)["properties"].(map[string]any), "value")
	}
	older, err := json.Marshal(snap)
	if err != nil {
		h.t.Fatal(err)
	}
	hash, err := schemas.SnapshotHashes(older)
	if err != nil {
		h.t.Fatal(err)
	}
	return older, hash
}

// newNegotiationHarness is a dispatcher whose released snapshots hold the
// v1.2.0 kv.item.get and kv.item.set, with the hashes of those snapshots.
func newNegotiationHarness(t *testing.T) (*harness, *Dispatcher, map[string]core.SchemaHash) {
	t.Helper()
	h := newHarness(t)
	released := fstest.MapFS{}
	older := map[string]core.SchemaHash{}
	for _, ref := range []core.ActionRef{refGet, refSet} {
		snap, hash := h.olderSnapshot(ref)
		released["released/v1.2.0/"+ref.Name+"/v1.json"] = &fstest.MapFile{Data: snap}
		older[schemas.HashKey(ref)] = hash
	}
	cfg := h.config(&coretest.MemAudit{})
	cfg.Snapshots = released
	d, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return h, d, older
}

// agentWith gives h1 a new session whose agent is the runtime reporting the
// schema hashes edit leaves of its own.
func (h *harness) agentWith(edit func(map[string]core.SchemaHash)) {
	h.clients.Add(testHost, &coretest.FakeAgent{
		HelloFunc: func(ctx context.Context) (core.AgentHello, error) {
			hello, err := h.rt.Hello(ctx)
			hello.SchemaHashes = maps.Clone(hello.SchemaHashes)
			edit(hello.SchemaHashes)
			return hello, err
		},
		ReadFunc: h.rt.Read, ResourceFunc: h.rt.Resource, PlanFunc: h.rt.Plan, ApplyFunc: h.rt.Apply,
	})
}

func TestSchemaNegotiationWithAPreviousMinor(t *testing.T) {
	ctx := context.Background()
	h, d, older := newNegotiationHarness(t)
	withValue := json.RawMessage(`{"name":"a","value":"1"}`)
	if _, err := d.Read(ctx, alice, testHost, refGet, withValue); err != nil {
		t.Fatalf("Read from an agent of this binary: %v", err)
	}

	h.agentWith(func(m map[string]core.SchemaHash) { maps.Copy(m, older) })
	if out, err := d.Read(ctx, alice, testHost, refGet, json.RawMessage(`{"name":"a"}`)); err != nil {
		t.Errorf("Read within the older schema = %s, %v; want it dispatched", out, err)
	}
	if _, err := d.Plan(ctx, alice, testHost, refSet, json.RawMessage(`{"name":"a"}`), core.PlanOptions{}); err != nil {
		t.Errorf("Plan within the older schema: %v; want it dispatched", err)
	}
	for name, call := range map[string]func() error{
		"read": func() error { _, err := d.Read(ctx, alice, testHost, refGet, withValue); return err },
		"plan": func() error {
			_, err := d.Plan(ctx, alice, testHost, refSet, withValue, core.PlanOptions{})
			return err
		},
	} {
		e := wantCode(t, call(), errcode.InvalidInput)
		if len(e.Fields) != 1 || e.Fields[0].Path != "/value" || !strings.Contains(e.Detail, "no field /value") {
			t.Errorf("%s: %+v, want the field /value the agent's schema lacks", name, e)
		}
	}
}

func TestSchemaNegotiationRefusesAnUnknownSchema(t *testing.T) {
	ctx := context.Background()
	h, d, _ := newNegotiationHarness(t)
	for name, edit := range map[string]func(map[string]core.SchemaHash){
		"unknown hash": func(m map[string]core.SchemaHash) {
			m[schemas.HashKey(refGet)] = core.SchemaHash{Input: strings.Repeat("0", 64), Output: strings.Repeat("0", 64)}
		},
		"no hash": func(m map[string]core.SchemaHash) { delete(m, schemas.HashKey(refGet)) },
	} {
		h.agentWith(edit)
		_, err := d.Read(ctx, alice, testHost, refGet, json.RawMessage(`{"name":"a"}`))
		e := wantCode(t, err, errcode.ModuleUnavailable)
		if want := "action kv.item.get/v1 is unavailable on host h1: " + ReasonSchemaUnknown; e.Detail != want {
			t.Errorf("%s: detail %q, want %q", name, e.Detail, want)
		}
		if _, err := d.Resource(ctx, alice, testHost, "kv.status"); err != nil {
			t.Errorf("%s: resources carry no schema hash and must still dispatch: %v", name, err)
		}
	}
}

func TestApplyChecksTheSchemaAgain(t *testing.T) {
	ctx := context.Background()
	h, d, _ := newNegotiationHarness(t)
	pv := h.plan(d, testHost, refSet, `{"name":"a","value":"1"}`)
	h.agentWith(func(m map[string]core.SchemaHash) { delete(m, schemas.HashKey(refSet)) })
	_, err := d.Apply(ctx, alice, testHost, refSet, pv.PlanID, core.ApplyOptions{})
	_ = wantCode(t, err, errcode.ModuleUnavailable)
}

func TestNewRefusesABrokenReleasedSnapshot(t *testing.T) {
	h := newHarness(t)
	cfg := h.config(&coretest.MemAudit{})
	cfg.Snapshots = fstest.MapFS{"released/v1.2.0/kv.item.get/v1.json": &fstest.MapFile{Data: []byte(`{"input":`)}}
	if _, err := New(cfg); err == nil || !strings.Contains(err.Error(), "released/v1.2.0/kv.item.get/v1.json") {
		t.Errorf("New = %v, want an error naming the broken snapshot", err)
	}
}
