package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
)

// The fixture module "jfx" journals its effects. Its host is jstore, a map of
// item names to StateDocs; Version is a revision every write bumps. Its
// compensable actions create, modify and delete one item and have an undo
// handler; jfx.item.set is revertible; jfx.item.slow is LongRunning.
// Input flags choose locality and make a call fail, hang or misbehave.

var (
	refJCreate = core.ActionRef{Name: "jfx.item.create", Version: 1}
	refJModify = core.ActionRef{Name: "jfx.item.modify", Version: 1}
	refJDelete = core.ActionRef{Name: "jfx.item.delete", Version: 1}
	refJSet    = core.ActionRef{Name: "jfx.item.set", Version: 1}    // revertible
	refJSlow   = core.ActionRef{Name: "jfx.item.slow", Version: 1}   // compensable, LongRunning
	refJRepair = core.ActionRef{Name: "jfx.item.repair", Version: 1} // jfx.item.set's RepairedBy
)

// jfxCommitWait is the CommitWait of every remote jfx call.
const jfxCommitWait = 2 * time.Minute

type jIn struct {
	Name   string `json:"name"`
	Value  string `json:"value,omitempty"`
	Secret string `json:"secret,omitempty" secret:"true"`
	// Local and InverseLocal declare the forward and inverse calls local.
	Local        bool `json:"local,omitempty"`
	InverseLocal bool `json:"inverseLocal,omitempty"`
	// FailBeforeCall fails after Intend without making the call; with
	// Definite the handler records Skipped and fails with a Definite error.
	FailBeforeCall bool `json:"failBeforeCall,omitempty"`
	Definite       bool `json:"definite,omitempty"`
	// FailAfterCall makes the call, then fails before Made.
	FailAfterCall bool `json:"failAfterCall,omitempty"`
	// HangApply blocks after Intend, before the call, until ctx is done or
	// the test opens the item's gate; HangVerify blocks Verify until the gate
	// opens, ignoring ctx.
	HangApply  bool `json:"hangApply,omitempty"`
	HangVerify bool `json:"hangVerify,omitempty"`
	// ObserveErr makes every observation of the item fail.
	ObserveErr bool `json:"observeErr,omitempty"`
	// InverseFail and InverseDefinite make the inverse fail without writing,
	// the second with a Definite error. InverseCreates makes the undo of a
	// deletion recreate the item under the new identity "<name>~r".
	InverseFail     bool `json:"inverseFail,omitempty"`
	InverseDefinite bool `json:"inverseDefinite,omitempty"`
	InverseCreates  bool `json:"inverseCreates,omitempty"`
	// Allowed is the item's Observation.Allowed.
	Allowed []string `json:"allowed,omitempty"`
	// Candidates makes a creation's call create the item twice, as a
	// retried provider call can, so its recovery identity matches two.
	Candidates bool `json:"candidates,omitempty"`
}

type jOut struct {
	Resource string `json:"resource"`
	Token    string `json:"token" secret:"true"`
}

type jRepairIn struct {
	Name    string `json:"name,omitempty"`
	Value   string `json:"value,omitempty"`
	Fail    bool   `json:"fail,omitempty"`
	Outcome string `json:"outcome,omitempty"`
}

type jRepairOut struct {
	RepairOutcome string `json:"repairOutcome"`
}

// jSaw is one handler call as jstore recorded it.
type jSaw struct {
	Call, ChangeID string
	CallCtx        core.CallContext
	Journal        bool // Env.Effects was set
}

type jstore struct {
	mu   sync.Mutex
	docs map[string]core.StateDoc
	rev  int
	// flags are the jIn flags of the last apply of each item, which Observe
	// and Inverse read: neither gets the input.
	flags map[string]jIn
	gates map[string]chan struct{}
	// entered receives an item name each time a hang starts.
	entered chan string
	saw     []jSaw
	// guardFails makes jfx.item.set's lockout guard fail on the state its
	// change left (spec 1.2's connectivity exception).
	guardFails bool
	// cancelled counts hung applies whose ctx ended.
	cancelled int
}

func newJStore() *jstore {
	return &jstore{docs: map[string]core.StateDoc{}, flags: map[string]jIn{}, gates: map[string]chan struct{}{},
		entered: make(chan string, 16)}
}

func (s *jstore) record(call string, env core.Env) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saw = append(s.saw, jSaw{Call: call, ChangeID: env.ChangeID, CallCtx: env.Call, Journal: env.Effects != nil})
}

// calls returns the recorded calls named call.
func (s *jstore) calls(call string) []jSaw {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []jSaw
	for _, c := range s.saw {
		if c.Call == call {
			out = append(out, c)
		}
	}
	return out
}

// doc is the state of a stored value: State {"value": v}, Version the
// revision of the write.
func (s *jstore) doc(value string) core.StateDoc {
	s.rev++
	return core.StateDoc{
		State:   json.RawMessage(fmt.Sprintf(`{"value":%q}`, value)),
		Version: json.RawMessage(fmt.Sprintf(`{"rev":%d}`, s.rev)),
	}
}

// put stores value under name, as a write outside Darkpanel does.
func (s *jstore) put(name, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.docs[name] = s.doc(value)
}

// get returns the stored doc of name, or {"state": null}.
func (s *jstore) get(name string) core.StateDoc {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getLocked(name)
}

func (s *jstore) getLocked(name string) core.StateDoc {
	if d, ok := s.docs[name]; ok {
		return d
	}
	return core.StateDoc{State: json.RawMessage("null")}
}

func (s *jstore) setFlags(in jIn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.flags[in.Name] = in
}

func (s *jstore) flagsOf(name string) jIn {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.flags[strings.TrimSuffix(name, "~r")]
}

func (s *jstore) gate(name string) chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.gates[name] == nil {
		s.gates[name] = make(chan struct{})
	}
	return s.gates[name]
}

// open releases every hang on name.
func (s *jstore) open(name string) { close(s.gate(name)) }

// waitEntered waits until a hang on name started.
func (s *jstore) waitEntered(t *testing.T, name string) {
	t.Helper()
	select {
	case got := <-s.entered:
		if got != name {
			t.Fatalf("hang started on %s, want %s", got, name)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("no hang started on %s", name)
	}
}

func jIdentity(name string) string { return "jfx.item:" + name }

// jName is the item name of a jfx resource; a creation's "?<name>" too.
func jName(resource string) string {
	return strings.TrimPrefix(strings.TrimPrefix(resource, "jfx.item:"), "?")
}

// jObserver observes jstore items for every jfx action.
type jObserver struct{ s *jstore }

func (o jObserver) Observe(_ context.Context, env core.Env, e core.Effect) (core.Observation, error) {
	o.s.record("Observe", env)
	name := jName(e.Resource)
	if e.State == core.EffectUndoing || e.State == core.EffectUndone {
		switch {
		case e.UndoResource != "":
			name = jName(e.UndoResource)
		case e.InverseRecovery != nil:
			name = e.InverseRecovery.Value
		}
	}
	flags := o.s.flagsOf(name)
	if flags.ObserveErr {
		return core.Observation{}, errors.New("observe failed")
	}
	o.s.mu.Lock()
	defer o.s.mu.Unlock()
	obs := core.Observation{Matches: 1, Resource: jIdentity(name), Exclusive: true, Allowed: flags.Allowed}
	if e.Kind != core.EffectCreate || !strings.Contains(e.Resource, ":?") {
		obs.Current = o.s.getLocked(name)
		return obs, nil
	}
	var keys []string
	for k := range o.s.docs {
		if k == name || strings.HasPrefix(k, name+"#") {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	obs.Matches, obs.Resource, obs.Current = len(keys), "", core.StateDoc{State: json.RawMessage("null")}
	for _, k := range keys {
		obs.Candidates = append(obs.Candidates, core.Candidate{Resource: jIdentity(k), State: o.s.docs[k]})
	}
	if len(keys) == 1 {
		obs.Resource, obs.Current = jIdentity(keys[0]), o.s.docs[keys[0]]
	}
	return obs, nil
}

// jMutator is the forward half of every journaling jfx action; kind is the
// effect each apply journals.
type jMutator struct {
	jObserver
	kind core.EffectKind
}

func (m jMutator) Plan(_ context.Context, env core.Env, in jIn) (core.PlanOutcome, error) {
	m.s.record("Plan", env)
	return core.PlanOutcome{Diff: []core.DiffLine{{Op: "+", Text: in.Name + "=" + in.Value}}, StateHash: "jfx"}, nil
}

func (m jMutator) Apply(ctx context.Context, env core.Env, in jIn) (jOut, error) {
	m.s.record("Apply", env)
	m.s.setFlags(in)
	eff := core.Effect{
		Kind: m.kind, Resource: jIdentity(in.Name), Local: in.Local, InverseLocal: in.InverseLocal,
		Before: m.s.get(in.Name), After: &core.StateDoc{State: json.RawMessage(fmt.Sprintf(`{"value":%q}`, in.Value))},
	}
	if !in.Local {
		eff.CommitWait = jfxCommitWait
	}
	switch m.kind {
	case core.EffectCreate:
		eff.Resource = "jfx.item:?" + in.Name
		eff.Recovery = &core.RecoveryID{Kind: "name", Value: in.Name}
		eff.Before = core.StateDoc{State: json.RawMessage("null")}
	case core.EffectDelete:
		eff.After = &core.StateDoc{State: json.RawMessage("null")}
		eff.InverseRecovery = &core.RecoveryID{Kind: "name", Value: in.Name}
	}
	var secret []byte
	if in.Secret != "" {
		secret = []byte(in.Secret)
	}
	seq, err := env.Effects.Intend(ctx, eff, secret)
	if err != nil {
		return jOut{}, err
	}
	switch {
	case in.FailBeforeCall && in.Definite:
		if err := env.Effects.Skipped(ctx, seq); err != nil {
			return jOut{}, err
		}
		return jOut{}, core.Definite(errors.New("refused before the call"))
	case in.FailBeforeCall:
		return jOut{}, errors.New("failed before the call")
	case in.HangApply:
		m.s.entered <- in.Name
		select {
		case <-ctx.Done():
			m.s.mu.Lock()
			m.s.cancelled++
			m.s.mu.Unlock()
			return jOut{}, ctx.Err()
		case <-m.s.gate(in.Name):
		}
	}
	m.s.mu.Lock()
	switch m.kind {
	case core.EffectDelete:
		delete(m.s.docs, in.Name)
	default:
		m.s.docs[in.Name] = m.s.doc(in.Value)
		if in.Candidates {
			m.s.docs[in.Name+"#2"] = m.s.doc(in.Value)
		}
	}
	post := m.s.getLocked(in.Name)
	m.s.mu.Unlock()
	if in.FailAfterCall {
		return jOut{}, errors.New("failed after the call")
	}
	if err := env.Effects.Made(ctx, seq, jIdentity(in.Name), post); err != nil {
		return jOut{}, err
	}
	return jOut{Resource: jIdentity(in.Name), Token: "tok-" + in.Name}, nil
}

func (m jMutator) Verify(_ context.Context, env core.Env, in jIn, _ jOut) error {
	m.s.record("Verify", env)
	if in.HangVerify {
		m.s.entered <- in.Name
		<-m.s.gate(in.Name)
	}
	want := core.StateDoc{State: json.RawMessage("null")}
	if m.kind != core.EffectDelete {
		want.State = json.RawMessage(fmt.Sprintf(`{"value":%q}`, in.Value))
	}
	if got := m.s.get(in.Name); !stateEqual(got, want) {
		return fmt.Errorf("item %s is %s, want %s", in.Name, got.State, want.State)
	}
	return nil
}

// jUndoer is a compensable jfx action with an undo handler.
type jUndoer struct{ jMutator }

func (u jUndoer) Inverse(_ context.Context, env core.Env, e core.Effect) (core.InverseResult, error) {
	u.s.record("Inverse", env)
	name := jName(e.Resource)
	flags := u.s.flagsOf(name)
	switch {
	case flags.InverseDefinite:
		return core.InverseResult{}, core.Definite(errors.New("inverse refused"))
	case flags.InverseFail:
		return core.InverseResult{}, errors.New("inverse failed")
	}
	u.s.mu.Lock()
	defer u.s.mu.Unlock()
	if e.UndoTo == nil || string(e.UndoTo.State) == "null" {
		delete(u.s.docs, name)
		return core.InverseResult{}, nil
	}
	var st struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(e.UndoTo.State, &st); err != nil {
		return core.InverseResult{}, err
	}
	if e.Kind == core.EffectDelete && flags.InverseCreates {
		u.s.docs[name+"~r"] = u.s.doc(st.Value)
		return core.InverseResult{Resource: jIdentity(name + "~r")}, nil
	}
	u.s.docs[name] = u.s.doc(st.Value)
	return core.InverseResult{}, nil
}

// jSnap is jfx.item.set's snapshot: the item before the apply.
type jSnap struct {
	Name    string        `json:"name"`
	Doc     core.StateDoc `json:"doc"`
	Present bool          `json:"present"`
}

// jReverter is jfx.item.set: a revertible modify of one item whose Revert
// follows spec 1.2's precedence rule, with jstore.guardFails as its lockout
// guard.
type jReverter struct{ jMutator }

func (r jReverter) Snapshot(_ context.Context, _ core.Env, in jIn) ([]byte, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	d, ok := r.s.docs[in.Name]
	return json.Marshal(jSnap{Name: in.Name, Doc: d, Present: ok})
}

func (r jReverter) Revert(ctx context.Context, env core.Env, snapshot []byte) (core.RevertOutcome, error) {
	r.s.record("Revert", env)
	var snap jSnap
	if err := json.Unmarshal(snapshot, &snap); err != nil {
		return core.RevertOutcome{}, err
	}
	effects, err := env.Effects.List(ctx)
	if err != nil {
		return core.RevertOutcome{}, err
	}
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	restore := func() {
		if snap.Present {
			r.s.docs[snap.Name] = r.s.doc(jValue(snap.Doc))
		} else {
			delete(r.s.docs, snap.Name)
		}
	}
	override := func() core.RevertOutcome {
		out := core.RevertOutcome{Override: true, Detail: json.RawMessage(fmt.Sprintf(`{"overwritten":%q}`, snap.Name))}
		for _, eff := range effects {
			cur := r.s.getLocked(snap.Name)
			out.Effects = append(out.Effects, core.EffectOutcome{Seq: eff.Seq, State: core.EffectUnresolved,
				Reason: core.ReasonConflict, Current: &cur})
		}
		restore()
		return out
	}
	for _, eff := range effects {
		if eff.State != core.EffectUnresolved {
			continue
		}
		if r.s.guardFails {
			return override(), nil
		}
		return core.RevertOutcome{}, fmt.Errorf("effect %d is unresolved", eff.Seq)
	}
	var out core.RevertOutcome
	left := false
	for _, eff := range effects {
		if eff.State != core.EffectMade {
			continue
		}
		cur := r.s.getLocked(snap.Name)
		if !docEqual(cur, *eff.Post) {
			left = true
			out.Effects = append(out.Effects, core.EffectOutcome{Seq: eff.Seq, State: core.EffectUnresolved,
				Reason: core.ReasonConflict, Current: &cur})
			continue
		}
		restore()
		out.Effects = append(out.Effects, core.EffectOutcome{Seq: eff.Seq, State: core.EffectUndone})
	}
	if left && r.s.guardFails {
		return override(), nil
	}
	return out, nil
}

// jValue is the value of a jfx StateDoc.
func jValue(d core.StateDoc) string {
	var st struct {
		Value string `json:"value"`
	}
	_ = json.Unmarshal(d.State, &st)
	return st.Value
}

// jRepairer is jfx.item.repair: it sets an item when Name is given and
// reports Outcome ("restored" when empty) as its repairOutcome.
type jRepairer struct{ s *jstore }

func (r jRepairer) Plan(_ context.Context, env core.Env, in jRepairIn) (core.PlanOutcome, error) {
	r.s.record("Plan", env)
	return core.PlanOutcome{Diff: []core.DiffLine{{Op: "+", Text: "repair " + in.Name}}, StateHash: "jfx-repair"}, nil
}

func (r jRepairer) Apply(_ context.Context, env core.Env, in jRepairIn) (jRepairOut, error) {
	r.s.record("Apply", env)
	if in.Fail {
		return jRepairOut{}, errors.New("repair failed")
	}
	if in.Name != "" {
		r.s.put(in.Name, in.Value)
	}
	if in.Outcome == "" {
		in.Outcome = core.RepairRestored
	}
	return jRepairOut{RepairOutcome: in.Outcome}, nil
}

func (r jRepairer) Verify(context.Context, core.Env, jRepairIn, jRepairOut) error { return nil }

// jfxOptions configure the jfx module of one fixture.
type jfxOptions struct {
	applyTimeout time.Duration // every jfx action's ApplyTimeout; 0 keeps the default
}

type jfxModule struct {
	s    *jstore
	opts jfxOptions
}

func (m jfxModule) Manifest() core.Manifest {
	spec := func(ref core.ActionRef, class core.Class) core.ActionSpec {
		return core.ActionSpec{Name: ref.Name, Version: 1, Class: class, Side: core.SideAgent,
			LockKeys: []string{"jfx"}, ApplyTimeout: m.opts.applyTimeout}
	}
	mut := func(kind core.EffectKind) jMutator { return jMutator{jObserver{m.s}, kind} }
	set := spec(refJSet, core.ClassRevertible)
	set.RepairedBy = refJRepair.Name
	slow := spec(refJSlow, core.ClassCompensable)
	slow.LongRunning = true
	repair := spec(refJRepair, core.ClassCompensable)
	repair.ForwardOnly, repair.Converges = true, true
	return core.Manifest{ID: "jfx", Version: 1, Actions: []core.ActionDef{
		core.DefineMutating(spec(refJCreate, core.ClassCompensable), jUndoer{mut(core.EffectCreate)}),
		core.DefineMutating(spec(refJModify, core.ClassCompensable), jUndoer{mut(core.EffectModify)}),
		core.DefineMutating(spec(refJDelete, core.ClassCompensable), jUndoer{mut(core.EffectDelete)}),
		core.DefineMutating(slow, jUndoer{mut(core.EffectModify)}),
		core.DefineRevertible(set, jReverter{mut(core.EffectModify)}),
		core.DefineMutating(repair, jRepairer{m.s}),
	}}
}

// jfixture is a fixture whose registry holds fix and jfx.
type jfixture struct {
	*fixture
	store *jstore
}

func newJFixture(t *testing.T, opts jfxOptions) *jfixture {
	t.Helper()
	h, s := &fixHost{items: map[string]string{}}, newJStore()
	reg, err := core.NewRegistry(fixModule{h}, jfxModule{s, opts})
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{
		t: t, host: h, reg: reg, path: filepath.Join(t.TempDir(), "agent.db"),
		clock: coretest.NewFakeClock(time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)),
	}
	f.open()
	return &jfixture{fixture: f, store: s}
}

// waitState polls a change until it is in state, for up to ten seconds, and
// returns its view.
func (f *fixture) waitState(changeID, state string) core.ChangeView {
	f.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		v, err := f.eng.Change(context.Background(), changeID)
		if err != nil {
			f.t.Fatal(err)
		}
		if v.State == state {
			return v
		}
		if time.Now().After(deadline) {
			f.t.Fatalf("change %s is %s, want %s", changeID, v.State, state)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// changeOf returns the id of the change of plan planID.
func (f *fixture) changeOf(planID string) string {
	f.t.Helper()
	var id string
	if err := f.db.Read().QueryRow("SELECT id FROM core_changes WHERE plan_id = ?", planID).Scan(&id); err != nil {
		f.t.Fatal(err)
	}
	return id
}

// effects lists a change's effects from the database.
func (f *jfixture) effects(changeID string) []core.Effect {
	f.t.Helper()
	effs, err := loadEffects(context.Background(), f.db.Read(), changeID)
	if err != nil {
		f.t.Fatal(err)
	}
	return effs
}

// effectStates lists a change's effects as "<seq>:<state>[/<reason>]".
func (f *jfixture) effectStates(changeID string) []string {
	f.t.Helper()
	var out []string
	for _, e := range f.effects(changeID) {
		s := fmt.Sprintf("%d:%s", e.Seq, e.State)
		if e.Reason != "" {
			s += "/" + e.Reason
		}
		out = append(out, s)
	}
	return out
}
