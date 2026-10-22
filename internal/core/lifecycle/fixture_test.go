package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// The fixture module "fix" is registered only in these tests. Its host is a
// map of named items; one handler serves every mutating class, input flags
// make it fail, stop or refuse to revert, and panicOn makes a handler call
// panic.

const testHost = "host-1"

var (
	alice = core.Principal{Actor: "user:alice", UserID: "alice", Scope: core.ScopeOperate, Source: "test"}

	refGet    = core.ActionRef{Name: "fix.item.get", Version: 1}
	refSet    = core.ActionRef{Name: "fix.item.set", Version: 1}    // revertible
	refAdd    = core.ActionRef{Name: "fix.item.add", Version: 1}    // compensable
	refRemove = core.ActionRef{Name: "fix.item.remove", Version: 1} // irreversible
	refCtl    = core.ActionRef{Name: "fix.ctl.get", Version: 1}     // controller-side read
)

type fixHost struct {
	mu      sync.Mutex
	items   map[string]string
	blocked bool // the not-blocked guard fails while set
	applies int
	// onSnapshot, when set, runs at the start of Snapshot without mu held.
	onSnapshot func()
	// onRevert, when set, runs at the start of Revert without mu held.
	onRevert func()
	// revertCtxErr is the context error the Revert handler saw right after
	// onRevert ran.
	revertCtxErr error
	// panicOn names the handler call ("Read", "Plan", "Apply", "Verify",
	// "Snapshot" or "Revert") that panics while set.
	panicOn string
	// onVerify, when set, runs at the start of Verify without mu held.
	onVerify func(ctx context.Context)
	// changeIDs records "<call>=<env.ChangeID>" for every Plan, Apply,
	// Verify and Revert call, in call order.
	changeIDs []string
	// verifyCalls records env.Call of every Verify call, in call order.
	verifyCalls []core.CallContext
}

func (h *fixHost) sawChangeID(call string, env core.Env) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.changeIDs = append(h.changeIDs, call+"="+env.ChangeID)
}

// panicIf panics when call is panicOn. The value quotes the item name, as a
// careless handler's panic might quote its input.
func (h *fixHost) panicIf(call, name string) {
	h.mu.Lock()
	on := h.panicOn
	h.mu.Unlock()
	if on == call {
		panic("fixture " + call + " panicked on item " + name)
	}
}

type itemIn struct {
	Name       string `json:"name"`
	Value      string `json:"value,omitempty"`
	Secret     string `json:"secret,omitempty" secret:"true"`
	FailApply  bool   `json:"failApply,omitempty"`
	FailVerify bool   `json:"failVerify,omitempty"`
	FailRevert bool   `json:"failRevert,omitempty"`
	CrashApply bool   `json:"crashApply,omitempty"`
}

type itemOut struct {
	Previous string `json:"previous"`
	Token    string `json:"token" secret:"true"`
}

type itemSnap struct {
	Name, Value         string
	Present, FailRevert bool
}

type getIn struct {
	Name string `json:"name"`
}

type getOut struct {
	Value  string `json:"value"`
	Actor  string `json:"actor"`
	HostID string `json:"hostId"`
	Token  string `json:"token" secret:"true"`
}

type itemHandler struct{ h *fixHost }

func (x itemHandler) Plan(_ context.Context, env core.Env, in itemIn) (core.PlanOutcome, error) {
	x.h.sawChangeID("Plan", env)
	x.h.panicIf("Plan", in.Name)
	x.h.mu.Lock()
	defer x.h.mu.Unlock()
	state, err := json.Marshal(x.h.items)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	return core.PlanOutcome{
		Diff:         []core.DiffLine{{Op: "-", Text: in.Name + "=" + x.h.items[in.Name]}, {Op: "+", Text: in.Name + "=" + in.Value}},
		Guards:       []core.Guard{{ID: "not-blocked", OK: !x.h.blocked, Message: "the host is blocked"}},
		StateHash:    string(state),
		Confirmation: in.Name,
	}, nil
}

func (x itemHandler) Apply(_ context.Context, env core.Env, in itemIn) (itemOut, error) {
	x.h.sawChangeID("Apply", env)
	// CrashApply stops the goroutine without a panic: it stands for the
	// process dying between persisting intent and recording the result.
	if in.CrashApply {
		runtime.Goexit()
	}
	x.h.panicIf("Apply", in.Name)
	if in.FailApply {
		return itemOut{}, errors.New("apply failed")
	}
	x.h.mu.Lock()
	defer x.h.mu.Unlock()
	x.h.applies++
	prev := x.h.items[in.Name]
	x.h.items[in.Name] = in.Value
	return itemOut{Previous: prev, Token: "tok-" + in.Name}, nil
}

func (x itemHandler) Verify(ctx context.Context, env core.Env, in itemIn, _ itemOut) error {
	if x.h.onVerify != nil {
		x.h.onVerify(ctx)
	}
	x.h.sawChangeID("Verify", env)
	x.h.mu.Lock()
	x.h.verifyCalls = append(x.h.verifyCalls, env.Call)
	x.h.mu.Unlock()
	x.h.panicIf("Verify", in.Name)
	if in.FailVerify {
		return errcode.New(errcode.Internal, "verify failed")
	}
	x.h.mu.Lock()
	defer x.h.mu.Unlock()
	if got := x.h.items[in.Name]; got != in.Value {
		return fmt.Errorf("item %s is %q, want %q", in.Name, got, in.Value)
	}
	return nil
}

func (x itemHandler) Snapshot(_ context.Context, _ core.Env, in itemIn) ([]byte, error) {
	if x.h.onSnapshot != nil {
		x.h.onSnapshot()
	}
	x.h.panicIf("Snapshot", in.Name)
	x.h.mu.Lock()
	defer x.h.mu.Unlock()
	v, ok := x.h.items[in.Name]
	return json.Marshal(itemSnap{Name: in.Name, Value: v, Present: ok, FailRevert: in.FailRevert})
}

func (x itemHandler) Revert(ctx context.Context, env core.Env, snapshot []byte) (core.RevertOutcome, error) {
	if x.h.onRevert != nil {
		x.h.onRevert()
	}
	x.h.revertCtxErr = ctx.Err()
	x.h.sawChangeID("Revert", env)
	var s itemSnap
	if err := json.Unmarshal(snapshot, &s); err != nil {
		return core.RevertOutcome{}, err
	}
	x.h.panicIf("Revert", s.Name)
	if s.FailRevert {
		return core.RevertOutcome{}, errors.New("revert failed")
	}
	x.h.mu.Lock()
	defer x.h.mu.Unlock()
	if s.Present {
		x.h.items[s.Name] = s.Value
	} else {
		delete(x.h.items, s.Name)
	}
	return core.RevertOutcome{}, nil
}

type getHandler struct{ h *fixHost }

func (g getHandler) Read(_ context.Context, env core.Env, in getIn) (getOut, error) {
	g.h.panicIf("Read", in.Name)
	g.h.mu.Lock()
	defer g.h.mu.Unlock()
	return getOut{Value: g.h.items[in.Name], Actor: env.Principal.Actor, HostID: env.HostID, Token: "tok-" + in.Name}, nil
}

type fixModule struct{ host *fixHost }

func (m fixModule) Manifest() core.Manifest {
	h := itemHandler{m.host}
	return core.Manifest{ID: "fix", Version: 1, Actions: []core.ActionDef{
		core.DefineRead(core.ActionSpec{Name: refGet.Name, Version: 1, Side: core.SideAgent}, getHandler{m.host}),
		core.DefineRevertible(core.ActionSpec{
			Name: refSet.Name, Version: 1, Side: core.SideAgent, LockKeys: []string{"fix"}, RepairedBy: "fix.item.repair",
		}, h),
		core.DefineMutating(core.ActionSpec{
			Name: "fix.item.repair", Version: 1, Class: core.ClassCompensable, Side: core.SideAgent,
			LockKeys: []string{"fix"}, CompensatedBy: refRemove.Name,
		}, h),
		core.DefineMutating(core.ActionSpec{
			Name: refAdd.Name, Version: 1, Class: core.ClassCompensable, Side: core.SideAgent,
			LockKeys: []string{"fix"}, CompensatedBy: refRemove.Name,
		}, h),
		core.DefineMutating(core.ActionSpec{
			Name: refRemove.Name, Version: 1, Class: core.ClassIrreversible, Side: core.SideAgent,
			LockKeys: []string{"fix", "fix"},
		}, h),
		core.DefineRead(core.ActionSpec{Name: refCtl.Name, Version: 1, Side: core.SideController}, getHandler{m.host}),
	}}
}

// testRedact replaces top-level fields tagged secret:"true"; production
// engines use secret.RedactJSON over the type's schema.
func testRedact(t reflect.Type, raw json.RawMessage) json.RawMessage {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return json.RawMessage(`"[redacted]"`)
	}
	for i := range t.NumField() {
		f := t.Field(i)
		name := strings.Split(f.Tag.Get("json"), ",")[0]
		if _, ok := m[name]; ok && f.Tag.Get("secret") == "true" {
			m[name] = json.RawMessage(`"[redacted]"`)
		}
	}
	b, err := json.Marshal(m)
	if err != nil {
		return json.RawMessage(`"[redacted]"`)
	}
	return b
}

type fixture struct {
	t       *testing.T
	host    *fixHost
	clock   *coretest.FakeClock
	reg     *core.Registry
	path    string
	db      *store.DB
	journal *AgentJournal
	eng     *Engine
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	h := &fixHost{items: map[string]string{}}
	reg, err := core.NewRegistry(fixModule{h})
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

func (f *fixture) config() Config {
	return Config{
		Side: core.SideAgent, Registry: f.reg, Validator: coretest.Validator{}, Journal: f.journal, Redact: testRedact,
		Base: core.Env{
			HostID: testHost, DB: f.db, Sealer: coretest.Sealer{}, Clock: f.clock,
			Log: slog.New(slog.DiscardHandler),
		},
	}
}

// open opens the database and a new engine over it, as a starting agent does.
func (f *fixture) open() {
	f.t.Helper()
	f.db = openDBAt(f.t, f.path, store.SideAgent)
	f.journal = NewAgentJournal(f.db)
	eng, err := New(f.config())
	if err != nil {
		f.t.Fatal(err)
	}
	f.eng = eng
	f.t.Cleanup(eng.Close)
}

func (f *fixture) plan(p core.Principal, ref core.ActionRef, input string) core.PlanView {
	f.t.Helper()
	v, err := f.eng.Plan(context.Background(), p, ref, json.RawMessage(input), core.PlanOptions{})
	if err != nil {
		f.t.Fatalf("Plan %s: %v", ref, err)
	}
	return v
}

func wantCode(t *testing.T, err error, code errcode.Code) {
	t.Helper()
	var e *errcode.Error
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("err = %v, want code %s", err, code)
	}
}
