package lifecycle

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// The test module "ufx" journals one effect per operation over ufxStore, a
// map of named objects, and implements Observer, Undoer and Resolver over
// it. Knobs on the store make an inverse or an observation misbehave and
// set an observation's candidates and allowed resolutions. The tests of
// undo, resolution, release, repair and secret purges use it.

const ufxKey = "ufx"

var (
	refUfx      = core.ActionRef{Name: "ufx.item.change", Version: 1} // compensable with an undo handler
	refUfxReset = core.ActionRef{Name: "ufx.item.reset", Version: 1}  // compensable and forward only: no undo handler
)

type ufxObject struct {
	value string
	rev   int
}

type ufxValue struct {
	Value string `json:"value"`
}

type ufxStore struct {
	mu      sync.Mutex
	objects map[string]ufxObject
	revs    int
	// inverse sets how Inverse treats an object: "definite" refuses with a
	// Definite error, "fail" fails before writing, "fail-after-write"
	// restores and then fails, "noop" returns without writing, and ""
	// restores.
	inverse     map[string]string
	observeErr  map[string]bool
	candidates  map[string][]core.Candidate
	allowed     map[string][]string
	onObserve   func() // runs at the start of every Observe, without mu held
	resolvedErr error
	resolved    []string // "<change id>/<seq>=<resolution> <state>" for every Resolved call
}

func newUfxStore() *ufxStore {
	return &ufxStore{
		objects: map[string]ufxObject{}, inverse: map[string]string{}, observeErr: map[string]bool{},
		candidates: map[string][]core.Candidate{}, allowed: map[string][]string{},
	}
}

// set writes value to the object id, or deletes it when value is empty.
// Every write gives the object a new revision.
func (s *ufxStore) set(id, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.setLocked(id, value)
}

func (s *ufxStore) setLocked(id, value string) {
	if value == "" {
		delete(s.objects, id)
		return
	}
	s.revs++
	s.objects[id] = ufxObject{value: value, rev: s.revs}
}

func (s *ufxStore) get(id string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.objects[id]
	return o.value, ok
}

func ufxState(value string) json.RawMessage {
	b, err := json.Marshal(ufxValue{Value: value})
	if err != nil {
		panic(err)
	}
	return b
}

// docLocked is the object's StateDoc: the value is State, the revision is
// Version, and an absent object is {"state": null}.
func (s *ufxStore) docLocked(id string) core.StateDoc {
	o, ok := s.objects[id]
	if !ok {
		return core.StateDoc{State: json.RawMessage("null")}
	}
	return core.StateDoc{State: ufxState(o.value), Version: json.RawMessage(fmt.Sprintf(`{"rev":%d}`, o.rev))}
}

func (s *ufxStore) hash() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := map[string]string{}
	for id, o := range s.objects {
		m[id] = fmt.Sprintf("%s@%d", o.value, o.rev)
	}
	b, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// ufxIdentity is the object an effect names: the object the inverse left
// once the effect is undoing or undone, else the effect's resource.
func ufxIdentity(e core.Effect) string {
	r := e.Resource
	if (e.State == core.EffectUndoing || e.State == core.EffectUndone) && e.UndoResource != "" {
		r = e.UndoResource
	}
	return strings.TrimPrefix(strings.TrimPrefix(r, "ufx.item:"), "?")
}

type ufxOp struct {
	ID            string `json:"id"`
	Value         string `json:"value,omitempty"`         // "" deletes the object
	Remote        bool   `json:"remote,omitempty"`        // the forward call is not Local
	InverseRemote bool   `json:"inverseRemote,omitempty"` // the inverse is not InverseLocal
}

type ufxIn struct {
	Ops []ufxOp `json:"ops"`
}

type ufxOut struct {
	Seqs []int `json:"seqs"`
}

// effect is the journal entry of op, taken before the call.
func (s *ufxStore) effect(op ufxOp) core.Effect {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, exists := s.objects[op.ID]
	after := core.StateDoc{State: json.RawMessage("null")}
	if op.Value != "" {
		after.State = ufxState(op.Value)
	}
	eff := core.Effect{
		Kind: core.EffectModify, Resource: "ufx.item:" + op.ID, Local: !op.Remote, InverseLocal: !op.InverseRemote,
		Before: s.docLocked(op.ID), After: &after,
	}
	switch {
	case !exists:
		eff.Kind, eff.Resource = core.EffectCreate, "ufx.item:?"+op.ID
		eff.Recovery = &core.RecoveryID{Kind: "name", Value: op.ID}
	case op.Value == "":
		eff.Kind = core.EffectDelete
		eff.InverseRecovery = &core.RecoveryID{Kind: "name", Value: op.ID}
	}
	if op.Remote || op.InverseRemote {
		eff.CommitWait = 2 * time.Minute
	}
	return eff
}

type ufxHandler struct{ s *ufxStore }

func (h ufxHandler) Plan(_ context.Context, _ core.Env, in ufxIn) (core.PlanOutcome, error) {
	diff := make([]core.DiffLine, 0, len(in.Ops))
	for _, op := range in.Ops {
		diff = append(diff, core.DiffLine{Op: "+", Text: op.ID + "=" + op.Value})
	}
	return core.PlanOutcome{Diff: diff, StateHash: h.s.hash()}, nil
}

func (h ufxHandler) Apply(ctx context.Context, env core.Env, in ufxIn) (ufxOut, error) {
	var out ufxOut
	for _, op := range in.Ops {
		seq, err := env.Effects.Intend(ctx, h.s.effect(op), nil)
		if err != nil {
			return out, err
		}
		h.s.mu.Lock()
		h.s.setLocked(op.ID, op.Value)
		post := h.s.docLocked(op.ID)
		h.s.mu.Unlock()
		if err := env.Effects.Made(ctx, seq, "ufx.item:"+op.ID, post); err != nil {
			return out, err
		}
		out.Seqs = append(out.Seqs, seq)
	}
	return out, nil
}

func (h ufxHandler) Verify(context.Context, core.Env, ufxIn, ufxOut) error { return nil }

func (h ufxHandler) Observe(_ context.Context, _ core.Env, e core.Effect) (core.Observation, error) {
	h.s.mu.Lock()
	hook := h.s.onObserve
	h.s.mu.Unlock()
	if hook != nil {
		hook()
	}
	id := ufxIdentity(e)
	h.s.mu.Lock()
	defer h.s.mu.Unlock()
	if h.s.observeErr[id] {
		return core.Observation{}, fmt.Errorf("ufx: cannot observe %s", id)
	}
	obs := core.Observation{Current: h.s.docLocked(id), Matches: 1, Exclusive: true, Allowed: h.s.allowed[id]}
	if _, ok := h.s.objects[id]; ok {
		obs.Resource = "ufx.item:" + id
	} else if e.Kind == core.EffectCreate {
		obs.Matches = 0
	}
	if c := h.s.candidates[id]; len(c) > 0 {
		obs.Matches, obs.Candidates, obs.Resource = len(c), c, ""
	}
	return obs, nil
}

func (h ufxHandler) Inverse(_ context.Context, _ core.Env, e core.Effect) (core.InverseResult, error) {
	id := ufxIdentity(e)
	h.s.mu.Lock()
	defer h.s.mu.Unlock()
	mode := h.s.inverse[id]
	switch mode {
	case "definite":
		return core.InverseResult{}, core.Definite(fmt.Errorf("ufx: the inverse of %s was refused", id))
	case "fail":
		return core.InverseResult{}, fmt.Errorf("ufx: the inverse of %s failed", id)
	case "noop":
		return core.InverseResult{}, nil
	}
	if e.UndoTo == nil {
		return core.InverseResult{}, fmt.Errorf("ufx: effect %d has no UndoTo", e.Seq)
	}
	var v ufxValue
	if err := json.Unmarshal(e.UndoTo.State, &v); err != nil {
		return core.InverseResult{}, err
	}
	h.s.setLocked(id, v.Value)
	if mode == "fail-after-write" {
		return core.InverseResult{}, fmt.Errorf("ufx: the inverse of %s failed after its write", id)
	}
	var res core.InverseResult
	if e.Kind == core.EffectDelete {
		res.Resource = "ufx.item:" + id
	}
	return res, nil
}

func (h ufxHandler) Resolved(_ context.Context, _ *sql.Tx, e core.Effect, resolution string) error {
	h.s.mu.Lock()
	defer h.s.mu.Unlock()
	h.s.resolved = append(h.s.resolved, fmt.Sprintf("%s/%d=%s %s", e.ChangeID, e.Seq, resolution, e.State))
	return h.s.resolvedErr
}

type ufxResetIn struct {
	ID string `json:"id"`
}

type ufxResetOut struct{}

type ufxResetHandler struct{ s *ufxStore }

func (h ufxResetHandler) Plan(context.Context, core.Env, ufxResetIn) (core.PlanOutcome, error) {
	return core.PlanOutcome{StateHash: h.s.hash()}, nil
}

func (h ufxResetHandler) Apply(_ context.Context, _ core.Env, in ufxResetIn) (ufxResetOut, error) {
	h.s.set(in.ID, "reset")
	return ufxResetOut{}, nil
}

func (h ufxResetHandler) Verify(context.Context, core.Env, ufxResetIn, ufxResetOut) error { return nil }

type ufxModule struct{ s *ufxStore }

func (m ufxModule) Manifest() core.Manifest {
	return core.Manifest{ID: "ufx", Version: 1, Actions: []core.ActionDef{
		core.DefineMutating(core.ActionSpec{
			Name: refUfx.Name, Version: 1, Class: core.ClassCompensable, Side: core.SideAgent, LockKeys: []string{ufxKey},
		}, ufxHandler(m)),
		core.DefineMutating(core.ActionSpec{
			Name: refUfxReset.Name, Version: 1, Class: core.ClassCompensable, Side: core.SideAgent,
			LockKeys: []string{ufxKey}, ForwardOnly: true, Converges: true,
		}, ufxResetHandler(m)),
	}}
}

type ufxFixture struct {
	*fixture
	store *ufxStore
}

// newUfx returns an agent engine whose registry holds only the ufx module.
func newUfx(t *testing.T) *ufxFixture {
	t.Helper()
	s := newUfxStore()
	reg, err := core.NewRegistry(ufxModule{s})
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{
		t: t, reg: reg, path: filepath.Join(t.TempDir(), "agent.db"),
		clock: coretest.NewFakeClock(time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)),
	}
	f.open()
	return &ufxFixture{fixture: f, store: s}
}

// change plans and applies ops as alice and returns the completed change.
func (u *ufxFixture) change(ops ...ufxOp) core.ChangeView {
	u.t.Helper()
	in, err := json.Marshal(ufxIn{Ops: ops})
	if err != nil {
		u.t.Fatal(err)
	}
	v := u.apply(alice, u.plan(alice, refUfx, string(in)).PlanID, core.ApplyOptions{})
	if v.State != core.StateCompleted {
		u.t.Fatalf("change = %+v, want completed", v)
	}
	return v
}

// sqlExec runs one statement, for tests that shape a change the way a crash
// or another mechanism would have left it.
func (f *fixture) sqlExec(query string, args ...any) {
	f.t.Helper()
	err := f.db.Write(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.Exec(query, args...)
		return err
	})
	if err != nil {
		f.t.Fatalf("%s: %v", query, err)
	}
}

// setNeedsRepair puts a change in needs_repair, unowned and holding the
// locks of keys, as a failed change that kept its locks is.
func (f *fixture) setNeedsRepair(changeID string, keys ...string) {
	f.t.Helper()
	f.sqlExec(`UPDATE core_changes SET state = ?, recovery_owner = '', error_code = ?, error_message = 'set by the test'
		WHERE id = ?`, core.StateNeedsRepair, string(errcode.NeedsRepair), changeID)
	for _, k := range keys {
		f.sqlExec(`INSERT INTO core_locks (key, owner) VALUES (?, ?)
			ON CONFLICT (key) DO UPDATE SET owner = excluded.owner`, k, changeID)
	}
}

// setEffectState moves effect seq of a change to state with reason,
// bypassing the engine's transition rules.
func (f *fixture) setEffectState(changeID string, seq int, state core.EffectState, reason string) {
	f.t.Helper()
	f.sqlExec("UPDATE core_effects SET state = ?, reason = ? WHERE change_id = ? AND seq = ?",
		string(state), reason, changeID, seq)
}

// putEffect inserts effect seq of a change in state with reason, for a
// journal shape no handler made.
func (f *fixture) putEffect(changeID string, seq int, state core.EffectState, reason string) { //nolint:unused // shared helper for repair and lookup tests in this package
	f.t.Helper()
	now := formatTime(f.clock.Now())
	f.sqlExec(`INSERT INTO core_effects (change_id, seq, kind, resource, local, inverse_local, before_json, state,
		reason, created_at, updated_at) VALUES (?, ?, 'modify', ?, 1, 1, '{"state":null}', ?, ?, ?, ?)`,
		changeID, seq, fmt.Sprintf("test.item:%d", seq), string(state), reason, now, now)
}

// journalEffect reads effect seq of a change.
func (f *fixture) journalEffect(changeID string, seq int) core.Effect {
	f.t.Helper()
	effs, err := loadEffects(context.Background(), f.db.Read(), changeID)
	if err != nil {
		f.t.Fatal(err)
	}
	for _, e := range effs {
		if e.Seq == seq {
			return e
		}
	}
	f.t.Fatalf("change %s has no effect %d", changeID, seq)
	return core.Effect{}
}

// settled waits until a change has left undoing: an undo records its
// outcome on its own goroutine.
func (f *fixture) settled(changeID string) core.ChangeView {
	f.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		v, err := f.eng.Change(context.Background(), changeID)
		if err != nil {
			f.t.Fatal(err)
		}
		if v.State != core.StateUndoing {
			return v
		}
		if time.Now().After(deadline) {
			f.t.Fatalf("change %s is still undoing", changeID)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// wantRefusal fails unless err carries code and its detail contains part.
func wantRefusal(t *testing.T, err error, code errcode.Code, part string) {
	t.Helper()
	wantCode(t, err, code)
	if d := errcode.From(err).Detail; !strings.Contains(d, part) {
		t.Fatalf("detail = %q, want it to contain %q", d, part)
	}
}
