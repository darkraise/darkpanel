package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	goruntime "runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/schemas"
)

func wantCode(t *testing.T, err error, code errcode.Code) *errcode.Error {
	t.Helper()
	var e *errcode.Error
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("err = %v, want code %s", err, code)
	}
	return e
}

func TestHelloReportsTheHostAndItsModules(t *testing.T) {
	f := newFixture(t)
	h, err := f.rt.Hello(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	hashes := map[string]core.SchemaHash{}
	for _, def := range f.reg.Actions() {
		if def.Spec.Side != core.SideAgent {
			continue
		}
		if hashes[schemas.HashKey(def.Ref())], err = schemas.ActionHash(def); err != nil {
			t.Fatal(err)
		}
	}
	want := core.AgentHello{
		ProtocolVersion: ProtocolVersion, BinaryVersion: "test", OS: goruntime.GOOS, Arch: goruntime.GOARCH, HostID: testHost,
		Modules: f.rt.statuses(), SchemaHashes: hashes, JournalEpoch: journalEpochOf(t, f),
	}
	if !reflect.DeepEqual(h, want) || len(h.Modules) != 4 || len(h.SchemaHashes) < 2 {
		t.Errorf("Hello = %+v, want %+v", h, want)
	}
	h.SchemaHashes["kv.item.get@1"] = core.SchemaHash{}
	if again, _ := f.rt.Hello(context.Background()); again.SchemaHashes["kv.item.get@1"] != hashes["kv.item.get@1"] {
		t.Error("changing one Hello's hashes changed the next Hello's")
	}
}

// journalEpochOf reads the journal epoch agent.db stores.
func journalEpochOf(t *testing.T, f *fixture) string {
	t.Helper()
	var v string
	if err := f.db.Read().QueryRow("SELECT value FROM core_meta WHERE key = 'journal_epoch'").Scan(&v); err != nil {
		t.Fatalf("journal_epoch: %v", err)
	}
	return v
}

func TestJournalEpoch(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	hello := func() string {
		t.Helper()
		h, err := f.rt.Hello(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return h.JournalEpoch
	}
	first := journalEpochOf(t, f)
	if _, err := ulid.ParseStrict(first); err != nil || hello() != first {
		t.Fatalf("epoch %q (%v), Hello %q; want one ULID", first, err, hello())
	}
	// A restart keeps the epoch, also under another host id, as after a
	// re-enrollment.
	f.restart()
	if got := hello(); got != first {
		t.Errorf("after a restart the epoch is %q, want %q", got, first)
	}
	f.rt.Close()
	c := f.config()
	c.HostID = "h2"
	rt, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(rt.Close)
	f.rt = rt
	if got := hello(); got != first {
		t.Errorf("under another host id the epoch is %q, want %q", got, first)
	}
	// A migration that recreates core_journal deletes the row, and the next
	// start creates a new epoch.
	if _, err := f.db.Writer().ExecContext(ctx, "DELETE FROM core_meta WHERE key = 'journal_epoch'"); err != nil {
		t.Fatal(err)
	}
	f.restart()
	renewed := hello()
	if _, err := ulid.ParseStrict(renewed); err != nil || renewed == first || journalEpochOf(t, f) != renewed {
		t.Errorf("after the row was deleted the epoch is %q, want a new ULID", renewed)
	}
	// Another agent.db has an epoch of its own.
	if other := newFixture(t); journalEpochOf(t, other) == renewed {
		t.Error("two agent databases share an epoch")
	}
}

func TestHelloReportsArchAndController(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	h, err := f.rt.Hello(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if h.Arch != goruntime.GOARCH || h.ControllerConnected != nil {
		t.Errorf("Hello without a callback: arch %q, controllerConnected %v; want %q and nil", h.Arch, h.ControllerConnected, goruntime.GOARCH)
	}
	var state *bool
	f.rt.Close()
	c := f.config()
	c.ControllerConnected = func() *bool { return state }
	rt, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(rt.Close)
	yes, no := true, false
	for _, want := range []*bool{nil, &yes, &no} {
		state = want
		h, err := rt.Hello(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if (h.ControllerConnected == nil) != (want == nil) || (want != nil && *h.ControllerConnected != *want) {
			t.Errorf("controllerConnected = %v, want %v", h.ControllerConnected, want)
		}
	}
}

// coreProbe is a core module with an agent-side action, as the update
// actions make it.
type coreProbe struct{}

func (coreProbe) Manifest() core.Manifest {
	return core.Manifest{
		ID: core.ModuleIDCore, Version: 1, DefaultEnabled: true,
		Actions: []core.ActionDef{core.DefineRead(core.ActionSpec{Name: "core.probe.get", Version: 1, Side: core.SideAgent}, echo{})},
	}
}

func TestCoreModuleIsAlwaysLoaded(t *testing.T) {
	f := newFixture(t)
	reg, err := core.NewRegistry(coreProbe{}, kvModule{})
	if err != nil {
		t.Fatal(err)
	}
	f.reg = reg
	f.restart()
	ctx := context.Background()
	coreStatus := core.ModuleStatus{ID: core.ModuleIDCore, Version: 1, Available: true}
	h, err := f.rt.Hello(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Modules) != 2 || h.Modules[0] != coreStatus {
		t.Errorf("Hello modules = %+v, want the core available first", h.Modules)
	}
	if st, err := f.rt.Preflight(ctx, core.ModuleIDCore); err != nil || st != coreStatus {
		t.Errorf("Preflight(core) = %+v, %v; want available", st, err)
	}
	out, err := f.rt.Read(ctx, alice, core.ActionRef{Name: "core.probe.get", Version: 1}, json.RawMessage(`{"name":"x"}`))
	if err != nil || !strings.Contains(string(out), `"value":"x"`) {
		t.Errorf("Read(core.probe.get) = %s, %v", out, err)
	}
}

func TestPreflightChecksAgain(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.host.mu.Lock()
	f.host.ready = true
	f.host.mu.Unlock()
	st, err := f.rt.Preflight(ctx, "flaky")
	if err != nil || st != (core.ModuleStatus{ID: "flaky", Version: 2, Available: true}) {
		t.Fatalf("Preflight(flaky) = %+v, %v; want available", st, err)
	}
	h, err := f.rt.Hello(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !h.Modules[2].Available {
		t.Errorf("Hello after the preflight = %+v, want flaky available", h.Modules[2])
	}
	st, err = f.rt.Preflight(ctx, "bad")
	if err != nil || st.Available || st.Reason != ReasonMigrationFailed || f.host.badChecks != 0 {
		t.Errorf("Preflight(bad) = %+v, %v (checks %d); want the migration failure without a check", st, err, f.host.badChecks)
	}
	_, err = f.rt.Preflight(ctx, "nope")
	_ = wantCode(t, err, errcode.NotFound)
}

func TestReadsRefuseABrokenModule(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	_, err := f.rt.Read(ctx, alice, core.ActionRef{Name: "bad.item.get", Version: 1}, json.RawMessage(`{"name":"a"}`))
	e := wantCode(t, err, errcode.ModuleUnavailable)
	if e.Detail != "module bad is unavailable: "+ReasonMigrationFailed {
		t.Errorf("detail = %q", e.Detail)
	}
	// A failed preflight is the dispatcher's to refuse; the agent serves it.
	if _, err := f.rt.Read(ctx, alice, core.ActionRef{Name: "flaky.item.get", Version: 1}, json.RawMessage(`{"name":"a"}`)); err != nil {
		t.Errorf("Read of flaky = %v, want served", err)
	}
	_, err = f.rt.Read(ctx, alice, core.ActionRef{Name: "kv.item.nope", Version: 1}, json.RawMessage(`{}`))
	_ = wantCode(t, err, errcode.ActionUnknown)
}

func TestResourceReadsWithTheAgentEnv(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	out, err := f.rt.Resource(ctx, alice, "kv.status")
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"count":0,"token":"[redacted]"}` {
		t.Errorf("kv.status = %s, want the token redacted", out)
	}
	_, err = f.rt.Resource(ctx, alice, "kv.nope")
	_ = wantCode(t, err, errcode.NotFound)
	_, err = f.rt.Resource(ctx, alice, "boom.status")
	var coded *errcode.Error
	if err == nil || errors.As(err, &coded) || !strings.Contains(err.Error(), "panicked") {
		t.Errorf("boom.status = %v, want a plain panic error", err)
	}
}

func TestChangesThroughTheClient(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	bob := core.Principal{Actor: "user:bob", UserID: "bob", Scope: core.ScopeOperate, Source: "192.0.2.8"}
	plan := func(input string) core.PlanView {
		t.Helper()
		v, err := f.rt.Plan(ctx, alice, refSet, json.RawMessage(input), core.PlanOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if v.HostID != testHost || v.Action != refSet.Name {
			t.Fatalf("plan = %+v", v)
		}
		return v
	}
	first, err := f.rt.Apply(ctx, alice, plan(`{"name":"a","value":"1"}`).PlanID, core.ApplyOptions{})
	if err != nil || first.State != core.StatePending {
		t.Fatalf("Apply = %+v, %v", first, err)
	}
	if v, err := f.rt.Confirm(ctx, bob, first.ID); err != nil || v.State != core.StateConfirmed {
		t.Fatalf("Confirm = %+v, %v", v, err)
	}
	second, err := f.rt.Apply(ctx, alice, plan(`{"name":"b","value":"1"}`).PlanID, core.ApplyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if v, err := f.rt.Revert(ctx, bob, second.ID); err != nil || v.State != core.StateReverted {
		t.Fatalf("Revert = %+v, %v", v, err)
	}
	if v, err := f.rt.Change(ctx, first.ID); err != nil || v.State != core.StateConfirmed || v.HostID != testHost {
		t.Errorf("Change = %+v, %v", v, err)
	}
	list, err := f.rt.Changes(ctx, core.ChangeFilter{})
	if err != nil || len(list) != 2 || list[0].ID != second.ID {
		t.Errorf("Changes = %+v, %v; want both, newest first", list, err)
	}

	entries, err := f.rt.Journal(ctx, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range entries {
		got = append(got, e.Phase+"/"+e.Source)
	}
	if want := []string{"apply/192.0.2.7", "confirm/192.0.2.8", "apply/192.0.2.7", "revert/192.0.2.8"}; !reflect.DeepEqual(got, want) {
		t.Errorf("journal = %v, want %v", got, want)
	}
	if err := f.rt.Ack(ctx, entries[1].Seq); err != nil {
		t.Fatal(err)
	}
	left, err := f.rt.Journal(ctx, 0, 10)
	if err != nil || len(left) != 2 || left[0].Seq != entries[2].Seq {
		t.Errorf("journal after Ack = %+v, %v; want the last two entries", left, err)
	}
}

// undoNoteStore is the host state of module note: the names of its entries.
type undoNoteStore struct {
	mu      sync.Mutex
	entries map[string]bool
}

func (s *undoNoteStore) has(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.entries[name]
}

func (s *undoNoteStore) set(name string, present bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[name] = present
}

type undoNoteIn struct {
	Name string `json:"name"`
}

type undoNoteOut struct {
	Name string `json:"name"`
}

func undoNoteDoc(name string) core.StateDoc {
	b, _ := json.Marshal(map[string]string{"name": name})
	return core.StateDoc{State: b}
}

// undoNoteAdd adds an entry and journals its creation. It is its own
// Observer and Undoer, so its changes can be undone.
type undoNoteAdd struct{ store *undoNoteStore }

func (h undoNoteAdd) Plan(_ context.Context, _ core.Env, in undoNoteIn) (core.PlanOutcome, error) {
	hash := "absent"
	if h.store.has(in.Name) {
		hash = "present"
	}
	return core.PlanOutcome{Diff: []core.DiffLine{{Op: "+", Text: "note " + in.Name}}, StateHash: hash}, nil
}

func (h undoNoteAdd) Apply(ctx context.Context, env core.Env, in undoNoteIn) (undoNoteOut, error) {
	after := undoNoteDoc(in.Name)
	seq, err := env.Effects.Intend(ctx, core.Effect{
		Kind: core.EffectCreate, Resource: "note.entry:?" + in.Name, Recovery: &core.RecoveryID{Kind: "name", Value: in.Name},
		Local: true, InverseLocal: true, Before: core.StateDoc{State: json.RawMessage("null")}, After: &after,
	}, nil)
	if err != nil {
		return undoNoteOut{}, err
	}
	h.store.set(in.Name, true)
	if err := env.Effects.Made(ctx, seq, "note.entry:"+in.Name, after); err != nil {
		return undoNoteOut{}, err
	}
	return undoNoteOut(in), nil
}

func (h undoNoteAdd) Verify(_ context.Context, _ core.Env, in undoNoteIn, _ undoNoteOut) error {
	if !h.store.has(in.Name) {
		return errors.New("note: entry " + in.Name + " is missing")
	}
	return nil
}

func (h undoNoteAdd) Observe(_ context.Context, _ core.Env, e core.Effect) (core.Observation, error) {
	name := strings.TrimPrefix(strings.TrimPrefix(e.Resource, "note.entry:"), "?")
	if !h.store.has(name) {
		return core.Observation{Current: core.StateDoc{State: json.RawMessage("null")}, Exclusive: true}, nil
	}
	return core.Observation{Current: undoNoteDoc(name), Resource: "note.entry:" + name, Matches: 1, Exclusive: true}, nil
}

func (h undoNoteAdd) Inverse(_ context.Context, _ core.Env, e core.Effect) (core.InverseResult, error) {
	h.store.set(strings.TrimPrefix(e.Resource, "note.entry:"), false)
	return core.InverseResult{}, nil
}

var undoNoteRef = core.ActionRef{Name: "note.entry.add", Version: 1}

// undoNoteModule is module note, whose one action note.entry.add is
// compensable with an undo handler.
type undoNoteModule struct{ store *undoNoteStore }

func (m undoNoteModule) Manifest() core.Manifest {
	return core.Manifest{ID: "note", Version: 1, DefaultEnabled: true, Actions: []core.ActionDef{
		core.DefineMutating(core.ActionSpec{Name: undoNoteRef.Name, Version: 1, Class: core.ClassCompensable,
			Side: core.SideAgent, LockKeys: []string{"note"}}, undoNoteAdd(m)),
	}}
}

// undoNoteFixture is the runtime fixture over modules kv, bad and note.
func undoNoteFixture(t *testing.T) (*fixture, *undoNoteStore) {
	t.Helper()
	f := newFixture(t)
	store := &undoNoteStore{entries: map[string]bool{}}
	reg, err := core.NewRegistry(kvModule{}, badModule{f.host}, undoNoteModule{store})
	if err != nil {
		t.Fatal(err)
	}
	f.reg = reg
	f.restart()
	return f, store
}

// undoNoteWait polls change id until the undo running on the engine's
// goroutine has left state undoing.
func undoNoteWait(t *testing.T, rt *Runtime, id string) core.ChangeView {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		v, err := rt.Change(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if v.State != core.StateUndoing {
			return v
		}
		if time.Now().After(deadline) {
			t.Fatalf("change %s is still undoing", id)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestChangeOpsThroughTheRuntime(t *testing.T) {
	f, store := undoNoteFixture(t)
	ctx := context.Background()
	pl, err := f.rt.Plan(ctx, alice, undoNoteRef, json.RawMessage(`{"name":"a"}`), core.PlanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := f.rt.Apply(ctx, alice, pl.PlanID, core.ApplyOptions{})
	if err != nil || ch.State != core.StateCompleted || !store.has("a") {
		t.Fatalf("Apply = %+v, %v; want completed with the entry made", ch, err)
	}
	v, err := f.rt.Undo(ctx, alice, ch.ID, nil)
	if err != nil || v.State != core.StateUndoing {
		t.Fatalf("Undo = %+v, %v; want undoing", v, err)
	}
	if v = undoNoteWait(t, f.rt, ch.ID); v.State != core.StateReverted || store.has("a") {
		t.Errorf("after the undo the change is %s and the entry present %v; want reverted and gone", v.State, store.has("a"))
	}
	effects, err := f.rt.Effects(ctx, alice, ch.ID)
	if err != nil || len(effects) != 1 || effects[0].State != core.EffectUndone || effects[0].Resource != "note.entry:a" {
		t.Errorf("Effects = %+v, %v; want the one creation undone", effects, err)
	}
	lk, err := f.rt.Lookup(ctx, pl.PlanID, false)
	if err != nil || lk.Status != core.LookupTerminal || lk.ChangeID != ch.ID || lk.State != core.StateReverted {
		t.Errorf("Lookup of the applied plan = %+v, %v; want terminal and reverted", lk, err)
	}
	_, err = f.rt.Release(ctx, alice, ch.ID, "the host was rebuilt", nil)
	_ = wantCode(t, err, errcode.ChangeNotPending)
	err = f.rt.AckPrivate(ctx, "01NOCHANGE", true)
	_ = wantCode(t, err, errcode.ChangeNotFound)
}

func TestLookupAndRetireThroughTheRuntime(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	pl, err := f.rt.Plan(ctx, alice, refSet, json.RawMessage(`{"name":"a","value":"1"}`), core.PlanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if lk, err := f.rt.Lookup(ctx, pl.PlanID, false); err != nil || lk.Status != core.LookupAbsent {
		t.Errorf("Lookup of an unapplied plan = %+v, %v; want absent", lk, err)
	}
	if lk, err := f.rt.Retire(ctx, pl.PlanID); err != nil || lk.Status != core.LookupRetired {
		t.Errorf("Retire = %+v, %v; want retired", lk, err)
	}
	_, err = f.rt.Apply(ctx, alice, pl.PlanID, core.ApplyOptions{})
	_ = wantCode(t, err, errcode.PlanNotFound)
	if lk, err := f.rt.Lookup(ctx, pl.PlanID, false); err != nil || lk.Status != core.LookupRetired {
		t.Errorf("Lookup after the retire = %+v, %v; want retired", lk, err)
	}
	_, err = f.rt.Lookup(ctx, "01NOPLAN", false)
	_ = wantCode(t, err, errcode.PlanNotFound)

	// kv.item.set journals no effects.
	other, err := f.rt.Plan(ctx, alice, refSet, json.RawMessage(`{"name":"b","value":"1"}`), core.PlanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := f.rt.Apply(ctx, alice, other.PlanID, core.ApplyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if effects, err := f.rt.Effects(ctx, alice, ch.ID); err != nil || len(effects) != 0 {
		t.Errorf("Effects of a change without effects = %+v, %v; want none", effects, err)
	}
}

func TestRepairNeedsAUsableModule(t *testing.T) {
	f, _ := undoNoteFixture(t)
	ctx := context.Background()
	const at = "2026-10-17T12:00:00.000000000Z"
	// A change of module bad, whose migration failed, as an older binary
	// would have left it.
	if _, err := f.db.Writer().ExecContext(ctx, `INSERT INTO core_plans (id, host_id, action, version, actor, input_hash,
		state_hash, diff_json, guards_json, confirmation, created_at, expires_at)
		VALUES ('01PBAD', 'h1', 'bad.item.set', 1, 'user:alice', '', '', '[]', '[]', '', ?, ?)`, at, at); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Writer().ExecContext(ctx, `INSERT INTO core_changes (id, plan_id, host_id, action, version, class,
		compensated_by, actor, state, input_json, created_at, updated_at)
		VALUES ('01CBAD', '01PBAD', 'h1', 'bad.item.set', 1, 'compensable', '', 'user:alice', 'needs_repair', '{}', ?, ?)`,
		at, at); err != nil {
		t.Fatal(err)
	}
	_, err := f.rt.Repair(ctx, alice, "01CBAD", core.RepairRequest{})
	_ = wantCode(t, err, errcode.ModuleUnavailable)
	_, err = f.rt.Repair(ctx, alice, "01NOCHANGE", core.RepairRequest{})
	_ = wantCode(t, err, errcode.ChangeNotFound)
}
