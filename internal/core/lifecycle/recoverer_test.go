package lifecycle

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// The fixture module "rcv" journals one modify effect per apply on a map of
// named items. rcv.item.fix has a module recovery handler whose outcome the
// test scripts on the host; rcv.item.put has an undo handler and no
// recovery handler. Input flags make Apply fail before or after its write,
// and remote makes the effect's calls not Local.

var (
	refRcvFix = core.ActionRef{Name: "rcv.item.fix", Version: 1}
	refRcvPut = core.ActionRef{Name: "rcv.item.put", Version: 1}
)

// rcvTimeout is both actions' ApplyTimeout. It is short, so a recovery
// handler that ignores its context is waited on in real time.
const rcvTimeout = 300 * time.Millisecond

// errRcvStore is the error a failing Apply returns.
var errRcvStore = errcode.New(errcode.Unavailable, "the item store is unavailable")

type rcvHost struct {
	mu      sync.Mutex
	items   map[string]string
	outcome core.RecoveryOutcome
	err     error
	// leave makes Recover intend a remote effect on item z and return
	// without recording it made.
	leave bool
	// hold, when set, makes Recover wait for its context to end, close
	// ctxEnded, and then wait for hold to close: a handler that ignores
	// its context.
	hold     chan struct{}
	ctxEnded chan struct{}
	calls    int
	during   []string // "<state>/<owner>" of the change, read in each Recover call
	changes  []string // env.ChangeID of each Recover call
}

func (h *rcvHost) get(name string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.items[name]
}

func (h *rcvHost) set(name, value string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.items[name] = value
}

// script sets what the next Recover calls return.
func (h *rcvHost) script(out core.RecoveryOutcome, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.outcome, h.err = out, err
}

func (h *rcvHost) recovered() (calls int, during, changes []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.calls, append([]string(nil), h.during...), append([]string(nil), h.changes...)
}

type rcvIn struct {
	Name       string `json:"name"`
	Value      string `json:"value"`
	Remote     bool   `json:"remote,omitempty"`
	FailBefore bool   `json:"failBefore,omitempty"`
	FailAfter  bool   `json:"failAfter,omitempty"`
}

type rcvOut struct {
	Value string `json:"value"`
}

// rcvDoc is the StateDoc of an item holding value; an absent item holds "".
func rcvDoc(value string) core.StateDoc {
	b, _ := json.Marshal(map[string]string{"value": value})
	return core.StateDoc{State: b}
}

type rcvBase struct{ h *rcvHost }

func (x rcvBase) Plan(_ context.Context, _ core.Env, in rcvIn) (core.PlanOutcome, error) {
	return core.PlanOutcome{
		Diff:      []core.DiffLine{{Op: "+", Text: in.Name + "=" + in.Value}},
		Guards:    []core.Guard{},
		StateHash: in.Name + "=" + x.h.get(in.Name),
	}, nil
}

func (x rcvBase) Apply(ctx context.Context, env core.Env, in rcvIn) (rcvOut, error) {
	after := rcvDoc(in.Value)
	e := core.Effect{
		Kind: core.EffectModify, Resource: "rcv.item:" + in.Name, Local: !in.Remote, InverseLocal: !in.Remote,
		Before: rcvDoc(x.h.get(in.Name)), After: &after,
	}
	if in.Remote {
		e.CommitWait = 2 * time.Minute
	}
	seq, err := env.Effects.Intend(ctx, e, nil)
	if err != nil {
		return rcvOut{}, err
	}
	if in.FailBefore {
		return rcvOut{}, errRcvStore
	}
	x.h.set(in.Name, in.Value)
	if in.FailAfter {
		return rcvOut{}, errRcvStore
	}
	if err := env.Effects.Made(ctx, seq, e.Resource, after); err != nil {
		return rcvOut{}, err
	}
	return rcvOut{Value: in.Value}, nil
}

func (x rcvBase) Verify(_ context.Context, _ core.Env, in rcvIn, _ rcvOut) error {
	if got := x.h.get(in.Name); got != in.Value {
		return errcode.New(errcode.Internal, "item "+in.Name+" is "+got)
	}
	return nil
}

func (x rcvBase) Observe(_ context.Context, _ core.Env, e core.Effect) (core.Observation, error) {
	name := strings.TrimPrefix(e.Resource, "rcv.item:")
	return core.Observation{Current: rcvDoc(x.h.get(name)), Resource: e.Resource, Matches: 1, Exclusive: true}, nil
}

type rcvFixHandler struct{ rcvBase }

func (x rcvFixHandler) Recover(ctx context.Context, env core.Env) (core.RecoveryOutcome, error) {
	var state, owner string
	if err := env.DB.Read().QueryRowContext(ctx,
		"SELECT state, recovery_owner FROM core_changes WHERE id = ?", env.ChangeID).Scan(&state, &owner); err != nil {
		return core.RecoveryOutcome{}, err
	}
	x.h.mu.Lock()
	x.h.calls++
	x.h.during = append(x.h.during, state+"/"+owner)
	x.h.changes = append(x.h.changes, env.ChangeID)
	leave, hold, ended, out, err := x.h.leave, x.h.hold, x.h.ctxEnded, x.h.outcome, x.h.err
	x.h.mu.Unlock()
	if leave {
		e := core.Effect{Kind: core.EffectModify, Resource: "rcv.item:z", CommitWait: 2 * time.Minute, Before: rcvDoc("")}
		if _, err := env.Effects.Intend(ctx, e, nil); err != nil {
			return core.RecoveryOutcome{}, err
		}
	}
	if hold != nil {
		<-ctx.Done()
		close(ended)
		<-hold
	}
	return out, err
}

type rcvPutHandler struct{ rcvBase }

func (x rcvPutHandler) Inverse(_ context.Context, _ core.Env, e core.Effect) (core.InverseResult, error) {
	var before struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(e.Before.State, &before); err != nil {
		return core.InverseResult{}, err
	}
	x.h.set(strings.TrimPrefix(e.Resource, "rcv.item:"), before.Value)
	return core.InverseResult{}, nil
}

type rcvModule struct{ host *rcvHost }

func (m rcvModule) Manifest() core.Manifest {
	b := rcvBase{m.host}
	return core.Manifest{ID: "rcv", Version: 1, Actions: []core.ActionDef{
		core.DefineMutating(core.ActionSpec{
			Name: refRcvFix.Name, Version: 1, Class: core.ClassCompensable, Side: core.SideAgent,
			LockKeys: []string{"rcv"}, ForwardOnly: true, Converges: true, ApplyTimeout: rcvTimeout,
		}, rcvFixHandler{b}),
		core.DefineMutating(core.ActionSpec{
			Name: refRcvPut.Name, Version: 1, Class: core.ClassCompensable, Side: core.SideAgent,
			LockKeys: []string{"rcv"}, ApplyTimeout: rcvTimeout,
		}, rcvPutHandler{b}),
	}}
}

// newRecoveryFixture is newFixture with mods registered beside the fix
// module.
func newRecoveryFixture(t *testing.T, mods ...core.Module) *fixture {
	t.Helper()
	h := &fixHost{items: map[string]string{}}
	reg, err := core.NewRegistry(append([]core.Module{fixModule{h}}, mods...)...)
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{
		t: t, host: h, reg: reg, path: filepath.Join(t.TempDir(), "agent.db"),
		clock: coretest.NewFakeClock(time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)),
	}
	f.open()
	return f
}

func newRcvFixture(t *testing.T) (*fixture, *rcvHost) {
	t.Helper()
	h := &rcvHost{items: map[string]string{}}
	return newRecoveryFixture(t, rcvModule{h}), h
}

// rcvApply plans and applies ref with input as alice and returns the change
// in whatever state the apply left it.
func (f *fixture) rcvApply(ref core.ActionRef, input string) core.ChangeView {
	f.t.Helper()
	return f.apply(alice, f.plan(alice, ref, input).PlanID, core.ApplyOptions{})
}

// rcvFlags reads a change's recovery_retry, the locks it holds and the
// epoch of lock key rcv.
func (f *fixture) rcvFlags(changeID string) (retry, locks, epoch int) {
	f.t.Helper()
	return f.count("SELECT recovery_retry FROM core_changes WHERE id = ?", changeID),
		f.count("SELECT count(*) FROM core_locks WHERE owner = ?", changeID),
		f.count("SELECT coalesce(max(epoch), 0) FROM core_lock_epochs WHERE key = 'rcv'")
}

// recoveryAwait polls until change id is in state want and returns it.
func (f *fixture) recoveryAwait(changeID, want string) core.ChangeView {
	f.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		v, err := f.eng.Change(context.Background(), changeID)
		if err != nil {
			f.t.Fatal(err)
		}
		if v.State == want {
			return v
		}
		if time.Now().After(deadline) {
			f.t.Fatalf("change %s is %s (owner %q), want %s", changeID, v.State, v.Owner, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestRecovererOutcomes(t *testing.T) {
	cases := []struct {
		name    string
		outcome core.RecoveryOutcome
		err     error
		leave   bool
		state   string
		code    string // the stored error code; "" for none
		cause   string // checked when set
		message string // part of the stored error message
		locks   int
		retry   int
		epoch   int
		last    string // the outcome's journal entry
	}{
		{
			name: "completed", outcome: core.RecoveryOutcome{State: core.RecoveryCompleted, Output: json.RawMessage(`{"value":"1"}`)},
			state: core.StateCompleted, epoch: 1, last: "recover/ok",
		},
		{
			name: "failed", outcome: core.RecoveryOutcome{State: core.RecoveryFailed, Reason: "the upgrade did not finish"},
			state: core.StateFailed, code: string(errcode.Unavailable), message: "the upgrade did not finish",
			last: "recover/error/unavailable",
		},
		{
			name: "rolled back", outcome: core.RecoveryOutcome{State: core.RecoveryRolledBack},
			state: core.StateReverted, last: "recover/ok",
		},
		{
			name: "needs repair", outcome: core.RecoveryOutcome{State: core.RecoveryNeedsRepair, Reason: "the old binary is gone"},
			state: core.StateNeedsRepair, code: string(errcode.NeedsRepair), cause: string(errcode.Unavailable),
			message: "the old binary is gone", locks: 1, epoch: 1, last: "recover/error/core.needs_repair",
		},
		{
			name: "an effect left unresolved", outcome: core.RecoveryOutcome{State: core.RecoveryCompleted}, leave: true,
			state: core.StateNeedsRepair, code: string(errcode.NeedsRepair), cause: string(errcode.Unavailable),
			message: "rcv.item:z", locks: 1, epoch: 1, last: "recover/error/core.needs_repair",
		},
		{
			name: "an error", err: errcode.New(errcode.Unavailable, "the package database is locked"),
			state: core.StateNeedsRepair, code: string(errcode.NeedsRepair), cause: string(errcode.Unavailable),
			message: "the package database is locked", locks: 1, retry: 1, epoch: 1, last: "recover/error/core.needs_repair",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, h := newRcvFixture(t)
			h.script(c.outcome, c.err)
			h.leave = c.leave
			v := f.rcvApply(refRcvFix, `{"name":"a","value":"1","failBefore":true}`)
			if v.State != c.state || v.Owner != "" {
				t.Fatalf("change = %s owned by %q, want %s and unowned", v.State, v.Owner, c.state)
			}
			calls, during, changes := h.recovered()
			if calls != 1 || !reflect.DeepEqual(during, []string{"recovering/module"}) || changes[0] != v.ID {
				t.Errorf("Recover ran %d times while the change was %v, for %v; want once, recovering/module, for %s",
					calls, during, changes, v.ID)
			}
			switch {
			case c.code == "" && v.Error != nil:
				t.Errorf("error = %+v, want none", v.Error)
			case c.code != "" && (v.Error == nil || v.Error.Code != c.code || !strings.Contains(v.Error.Message, c.message)):
				t.Errorf("error = %+v, want code %s naming %q", v.Error, c.code, c.message)
			case c.cause != "" && v.Error.Cause != c.cause:
				t.Errorf("error cause = %q, want %q", v.Error.Cause, c.cause)
			}
			if c.state == core.StateCompleted && string(v.Output) != `{"value":"1"}` {
				t.Errorf("output = %s, want the handler's", v.Output)
			}
			if retry, locks, epoch := f.rcvFlags(v.ID); retry != c.retry || locks != c.locks || epoch != c.epoch {
				t.Errorf("recovery_retry %d, locks %d, epoch %d; want %d, %d, %d", retry, locks, epoch, c.retry, c.locks, c.epoch)
			}
			got := f.phases()
			if len(got) < 2 || !reflect.DeepEqual(got[len(got)-2:], []string{"apply/error/unavailable", c.last}) {
				t.Errorf("journal = %v, want it to end with the apply's failure and %s", got, c.last)
			}
		})
	}
}

func TestRecovererLiveFailureWithoutTheHandler(t *testing.T) {
	cases := []struct {
		name  string
		ref   core.ActionRef
		input string
		state string
		code  string
		retry int
		locks int
	}{
		{"an unresolved effect", refRcvFix, `{"name":"a","value":"1","remote":true,"failAfter":true}`,
			core.StateNeedsRepair, string(errcode.NeedsRepair), 1, 1},
		{"no recovery handler", refRcvPut, `{"name":"a","value":"1","failBefore":true}`,
			core.StateFailed, string(errcode.Unavailable), 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, h := newRcvFixture(t)
			v := f.rcvApply(c.ref, c.input)
			if v.State != c.state || v.Owner != "" || v.Error == nil || v.Error.Code != c.code {
				t.Fatalf("change = %s owned by %q with error %+v, want %s with code %s", v.State, v.Owner, v.Error, c.state, c.code)
			}
			if calls, _, _ := h.recovered(); calls != 0 {
				t.Errorf("Recover ran %d times, want never", calls)
			}
			if retry, locks, _ := f.rcvFlags(v.ID); retry != c.retry || locks != c.locks {
				t.Errorf("recovery_retry %d, locks %d; want %d, %d", retry, locks, c.retry, c.locks)
			}
		})
	}
}

func TestRecovererRepairRunsItAgain(t *testing.T) {
	f, h := newRcvFixture(t)
	ctx := context.Background()
	h.script(core.RecoveryOutcome{}, errcode.New(errcode.Unavailable, "the package database is locked"))
	v := f.rcvApply(refRcvFix, `{"name":"a","value":"1","failBefore":true}`)
	if retry, _, _ := f.rcvFlags(v.ID); v.State != core.StateNeedsRepair || retry != 1 {
		t.Fatalf("change = %s with recovery_retry %d, want needs_repair with it set", v.State, retry)
	}
	h.script(core.RecoveryOutcome{State: core.RecoveryCompleted, Output: json.RawMessage(`{"value":"1"}`)}, nil)
	res, err := f.eng.Repair(ctx, alice, v.ID, core.RepairRequest{})
	if err != nil || res.Plan != nil || res.Change == nil ||
		res.Change.State != core.StateRecovering || res.Change.Owner != core.OwnerModule {
		t.Fatalf("Repair = %+v, %v; want the change recovering, owned by module", res, err)
	}
	done := f.recoveryAwait(v.ID, core.StateCompleted)
	retry, locks, _ := f.rcvFlags(v.ID)
	calls, during, _ := h.recovered()
	if done.Owner != "" || retry != 0 || locks != 0 || calls != 2 || during[1] != "recovering/module" {
		t.Errorf("after repair: owner %q, recovery_retry %d, locks %d, Recover calls %d (%v); want unowned, 0, 0, 2",
			done.Owner, retry, locks, calls, during)
	}
}

func TestRecovererRepairRefusesAnInput(t *testing.T) {
	f, h := newRcvFixture(t)
	ctx := context.Background()
	h.script(core.RecoveryOutcome{}, errcode.New(errcode.Unavailable, "the package database is locked"))
	v := f.rcvApply(refRcvFix, `{"name":"a","value":"1","failBefore":true}`)
	if retry, _, _ := f.rcvFlags(v.ID); v.State != core.StateNeedsRepair || retry != 1 {
		t.Fatalf("change = %s with recovery_retry %d, want needs_repair with it set", v.State, retry)
	}
	_, err := f.eng.Repair(ctx, alice, v.ID, core.RepairRequest{Input: json.RawMessage(`{"outcome":"keep"}`)})
	if err == nil || errcode.From(err).Code != errcode.InvalidInput {
		t.Fatalf("Repair with an input = %v, want %s", err, errcode.InvalidInput)
	}
	after, err := f.eng.Change(ctx, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	retry, _, _ := f.rcvFlags(v.ID)
	calls, _, _ := h.recovered()
	if after.State != core.StateNeedsRepair || after.Owner != "" || retry != 1 || calls != 1 {
		t.Errorf("after the refusal: %s owned by %q, recovery_retry %d, Recover calls %d; want needs_repair, unowned, 1, 1",
			after.State, after.Owner, retry, calls)
	}
}

func TestRecovererIgnoringItsContextKeepsTheChangeOwned(t *testing.T) {
	f, h := newRcvFixture(t)
	ctx := context.Background()
	hold, ended := make(chan struct{}), make(chan struct{})
	h.script(core.RecoveryOutcome{State: core.RecoveryCompleted}, nil)
	h.hold, h.ctxEnded = hold, ended
	pl := f.plan(alice, refRcvFix, `{"name":"a","value":"1","failBefore":true}`)
	type result struct {
		v   core.ChangeView
		err error
	}
	applied := make(chan result, 1)
	go func() {
		v, err := f.eng.Apply(ctx, alice, pl.PlanID, core.ApplyOptions{})
		applied <- result{v, err}
	}()
	select {
	case <-ended:
	case <-time.After(10 * time.Second):
		close(hold)
		t.Fatal("the recovery handler's context did not end at the action's ApplyTimeout")
	}
	var id string
	if err := f.db.Read().QueryRow("SELECT id FROM core_changes WHERE plan_id = ?", pl.PlanID).Scan(&id); err != nil {
		close(hold)
		t.Fatal(err)
	}
	v, err := f.eng.Change(ctx, id)
	_, locks, _ := f.rcvFlags(id)
	if err != nil || v.State != core.StateRecovering || v.Owner != core.OwnerModule || locks != 1 {
		t.Errorf("while the handler runs past its deadline: %+v, %v, locks %d; want recovering, owned by module, locked",
			v, err, locks)
	}
	select {
	case r := <-applied:
		t.Errorf("Apply returned %+v, %v while the handler still ran", r.v, r.err)
	case <-time.After(50 * time.Millisecond):
	}
	close(hold)
	select {
	case r := <-applied:
		if r.err != nil || r.v.State != core.StateCompleted || r.v.Owner != "" {
			t.Errorf("Apply = %+v, %v; want completed and unowned once the handler returned", r.v, r.err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Apply did not return after the handler did")
	}
}
