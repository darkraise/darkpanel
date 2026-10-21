package core

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

type echoIn struct {
	Name string `json:"name"`
}

type echoOut struct {
	Greeting string `json:"greeting"`
}

type echo struct{}

func (echo) Read(_ context.Context, _ Env, in echoIn) (echoOut, error) {
	return echoOut{"hello " + in.Name}, nil
}

func (echo) Plan(_ context.Context, _ Env, in echoIn) (PlanOutcome, error) {
	return PlanOutcome{Diff: []DiffLine{{"+", in.Name}}, StateHash: "h"}, nil
}

func (echo) Apply(_ context.Context, _ Env, in echoIn) (echoOut, error) {
	return echoOut{"applied " + in.Name}, nil
}

func (echo) Verify(_ context.Context, _ Env, _ echoIn, out echoOut) error {
	if !strings.HasPrefix(out.Greeting, "applied ") {
		return errors.New("bad output")
	}
	return nil
}

func (echo) Snapshot(_ context.Context, _ Env, in echoIn) ([]byte, error) {
	return []byte(in.Name), nil
}

func (echo) Revert(context.Context, Env, []byte) (RevertOutcome, error) { return RevertOutcome{}, nil }

type testModule Manifest

func (m testModule) Manifest() Manifest { return Manifest(m) }

func readAction(name string) ActionDef {
	return DefineRead[echoIn, echoOut](ActionSpec{Name: name, Version: 1, Side: SideAgent}, echo{})
}

func mutating(name string, class Class, side Side, compensatedBy string) ActionDef {
	return DefineMutating[echoIn, echoOut](ActionSpec{Name: name, Version: 1, Class: class, Side: side, CompensatedBy: compensatedBy}, echo{})
}

// revertible declares name with the lock key name and the repair action
// regRepairName(name), which regRepairOf declares.
func revertible(name string, side Side) ActionDef {
	return DefineRevertible[echoIn, echoOut](ActionSpec{
		Name: name, Version: 1, Side: side, LockKeys: []string{name}, RepairedBy: regRepairName(name),
	}, echo{})
}

// regRepairName is "<module>.<noun>.repair" for the action "<module>.<noun>.<verb>".
func regRepairName(name string) string { return name[:strings.LastIndex(name, ".")] + ".repair" }

// regRepairOf is the irreversible repair action of the revertible action rev,
// on its side and with its lock keys.
func regRepairOf(rev ActionDef) ActionDef {
	return DefineMutating[echoIn, echoOut](ActionSpec{
		Name: rev.Spec.RepairedBy, Version: 1, Class: ClassIrreversible, Side: rev.Spec.Side, LockKeys: rev.Spec.LockKeys,
	}, echo{})
}

// regUndoEcho is echo with an undo handler.
type regUndoEcho struct{ echo }

func (regUndoEcho) Observe(context.Context, Env, Effect) (Observation, error) {
	return Observation{}, nil
}

func (regUndoEcho) Inverse(context.Context, Env, Effect) (InverseResult, error) {
	return InverseResult{}, nil
}

// regRecoverEcho is echo with a module recovery handler.
type regRecoverEcho struct{ echo }

func (regRecoverEcho) Recover(context.Context, Env) (RecoveryOutcome, error) {
	return RecoveryOutcome{}, nil
}

// regTaskEcho is the handler of a task action.
type regTaskEcho struct{ echo }

func (regTaskEcho) Task(_ context.Context, _ Env, in echoIn) (echoOut, error) {
	return echoOut{"applied " + in.Name}, nil
}

// regComposer is the handler of a composite action over input In.
type regComposer[In any] struct{}

func (regComposer[In]) Steps(context.Context, Env, In) ([]Step, error) { return nil, nil }

func (regComposer[In]) Output(context.Context, Env, In, map[string]json.RawMessage) (echoOut, error) {
	return echoOut{}, nil
}

type regSecretIn struct {
	Key string `json:"key" secret:"true"`
}

type regNestedSecretIn struct {
	Items []struct {
		Auth *regSecretIn `json:"auth"`
	} `json:"items"`
}

func regCompensable(name string, spec ActionSpec, h Mutator[echoIn, echoOut]) ActionDef {
	spec.Name, spec.Version, spec.Class, spec.Side = name, 1, ClassCompensable, SideAgent
	return DefineMutating(spec, h)
}

func regComposite(name string, unwind Unwind) ActionDef {
	return DefineComposite[echoIn, echoOut](ActionSpec{Name: name, Version: 1, Unwind: unwind}, regComposer[echoIn]{})
}

func regTask(name string, class Class) ActionDef {
	return DefineTask[echoIn, echoOut](ActionSpec{
		Name: name, Version: 1, Class: class, Side: SideAgent, LockKeys: []string{"demo"}, ApplyTimeout: 15 * time.Minute,
	}, regTaskEcho{})
}

func resource(name string) ResourceDef {
	return DefineResource(name, func(context.Context, Env) (echoOut, error) { return echoOut{"hi"}, nil })
}

// A resource <m>.<r> is `darkpanel <m> <r> get`, so it may share its noun
// with actions whose verbs differ (spec section 15).
func TestNewRegistryAcceptsResourceBesideActionNoun(t *testing.T) {
	set := revertible("demo.rule.set", SideAgent)
	mod := testModule{
		ID: "demo", Version: 1,
		Actions:   []ActionDef{set, regRepairOf(set)},
		Resources: []ResourceDef{resource("demo.rule")},
	}
	if _, err := NewRegistry(mod); err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
}

func TestNewRegistryAccepts(t *testing.T) {
	set := revertible("demo.rule.set", SideAgent)
	mod := testModule{
		ID: "demo", Version: 1,
		Actions: []ActionDef{
			readAction("demo.item.get"),
			mutating("demo.item.add", ClassCompensable, SideAgent, "demo.item.remove"),
			mutating("demo.item.remove", ClassIrreversible, SideAgent, ""),
			set, regRepairOf(set),
		},
		Resources: []ResourceDef{DefineResource("demo.summary", func(context.Context, Env) (echoOut, error) { return echoOut{"hi"}, nil })},
	}
	r, err := NewRegistry(mod)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	if _, ok := r.Action(ActionRef{"demo.item.add", 1}); !ok {
		t.Error("demo.item.add/v1 not found")
	}
	if got := len(r.Actions()); got != 5 {
		t.Errorf("len(Actions()) = %d, want 5", got)
	}
	if r.Actions()[0].Spec.Name != "demo.item.add" {
		t.Errorf("Actions() not sorted: first is %s", r.Actions()[0].Spec.Name)
	}
	if _, ok := r.Resource("demo.summary"); !ok {
		t.Error("demo.summary not found")
	}
	if m, ok := r.Module("demo"); !ok || m.ID != "demo" {
		t.Error("Module(demo) not found")
	}
}

func TestNewRegistryRules(t *testing.T) {
	bg := mutating("core.user.create", ClassCompensable, SideAgent, "core.user.create")
	bg.Spec.BreakGlass = true
	locked := readAction("demo.item.get")
	locked.Spec.LockKeys = []string{"demo"}
	noVerify := mutating("demo.item.add", ClassIrreversible, SideAgent, "")
	noVerify.Handler.Verify = nil
	keyless := revertible("demo.rule.set", SideAgent)
	keyless.Spec.LockKeys = nil
	tests := []struct {
		name string
		mods []Module
		want string
	}{
		{"bad module id", []Module{testModule{ID: "Demo", Version: 1}}, "must match"},
		{"reserved module id", []Module{testModule{ID: "token", Version: 1}}, "reserved"},
		{"reserved module id system", []Module{testModule{ID: "system", Version: 1}}, "reserved"},
		{"duplicate module", []Module{testModule{ID: "demo", Version: 1}, testModule{ID: "demo", Version: 1}}, "registered twice"},
		{"module version", []Module{testModule{ID: "demo"}}, "version 0"},
		{"bad action name", []Module{testModule{ID: "demo", Version: 1, Actions: []ActionDef{readAction("demo.get")}}}, "must match"},
		{"foreign action prefix", []Module{testModule{ID: "demo", Version: 1, Actions: []ActionDef{readAction("other.item.get")}}}, "start with"},
		{"duplicate action", []Module{testModule{ID: "demo", Version: 1, Actions: []ActionDef{readAction("demo.item.get"), readAction("demo.item.get")}}}, "registered twice"},
		{"break-glass on agent", []Module{testModule{ID: "core", Version: 1, Actions: []ActionDef{bg}}}, "break-glass"},
		{"revertible on controller", []Module{testModule{ID: "demo", Version: 1, Actions: []ActionDef{revertible("demo.rule.set", SideController)}}}, "revertible actions are agent-side"},
		{"revertible without a lock key", []Module{testModule{ID: "demo", Version: 1, Actions: []ActionDef{keyless}}}, "a revertible agent action declares at least one lock key"},
		{"compensable without compensation", []Module{testModule{ID: "demo", Version: 1, Actions: []ActionDef{mutating("demo.item.add", ClassCompensable, SideAgent, "")}}}, "exactly one of an undo handler, CompensatedBy or ForwardOnly"},
		{"unknown compensation", []Module{testModule{ID: "demo", Version: 1, Actions: []ActionDef{mutating("demo.item.add", ClassCompensable, SideAgent, "demo.item.gone")}}}, "not registered"},
		{"compensation on irreversible", []Module{testModule{ID: "demo", Version: 1, Actions: []ActionDef{mutating("demo.item.add", ClassIrreversible, SideAgent, "demo.item.add")}}}, "only compensable"},
		{"read with locks", []Module{testModule{ID: "demo", Version: 1, Actions: []ActionDef{locked}}}, "take no locks"},
		{"missing handler", []Module{testModule{ID: "demo", Version: 1, Actions: []ActionDef{noVerify}}}, "Plan, Apply and Verify"},
		{"unknown class", []Module{testModule{ID: "demo", Version: 1, Actions: []ActionDef{mutating("demo.item.add", Class("magic"), SideAgent, "")}}}, "unknown class"},
		{"resource noun with a dash", []Module{testModule{ID: "demo", Version: 1, Resources: []ResourceDef{resource("demo.sum-mary")}}}, "must match"},
		{"bad resource name", []Module{testModule{ID: "demo", Version: 1, Resources: []ResourceDef{{Name: "other.summary", Read: func(context.Context, Env) (json.RawMessage, error) { return nil, nil }}}}}, "start with"},
		{"migrations side", []Module{testModule{ID: "demo", Version: 1, Migrations: map[Side]fs.FS{"browser": nil}}}, "unknown side"},
		{"action and resource share a CLI command", []Module{testModule{ID: "demo", Version: 1,
			Actions: []ActionDef{readAction("demo.item.get")}, Resources: []ResourceDef{resource("demo.item")}}}, "same CLI command"},
		{"core action and resource share a CLI command", []Module{testModule{ID: "core", Version: 1,
			Actions: []ActionDef{readAction("core.thing.get")}, Resources: []ResourceDef{resource("core.thing")}}}, "same CLI command"},
		{"core action noun is a module id", []Module{testModule{ID: "core", Version: 1, Actions: []ActionDef{readAction("core.demo.list")}},
			testModule{ID: "demo", Version: 1}}, "top-level CLI name"},
		{"core resource is a module id", []Module{testModule{ID: "demo", Version: 1},
			testModule{ID: "core", Version: 1, Resources: []ResourceDef{resource("core.demo")}}}, "top-level CLI name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewRegistry(tt.mods...)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("NewRegistry error = %v, want one containing %q", err, tt.want)
			}
		})
	}
}

// TestLifecycleRegistryAccepts registers one declaration of each kind the
// lifecycle rules admit.
func TestLifecycleRegistryAccepts(t *testing.T) {
	set := revertible("demo.rule.set", SideAgent)
	undoable := regCompensable("demo.item.add", ActionSpec{LockKeys: []string{"demo"}}, regUndoEcho{})
	forward := regCompensable("demo.item.pin", ActionSpec{ForwardOnly: true, Converges: true}, echo{})
	long := mutating("demo.item.sync", ClassIrreversible, SideAgent, "")
	long.Spec.ApplyTimeout, long.Spec.LongRunning = 20*time.Minute, true
	mod := testModule{ID: "demo", Version: 1, Actions: []ActionDef{
		readAction("demo.item.get"),
		undoable, forward, long,
		mutating("demo.item.drop", ClassCompensable, SideAgent, "demo.item.add"),
		mutating("demo.item.purge", ClassIrreversible, SideAgent, ""),
		set, regRepairOf(set),
		regComposite("demo.stack.deploy", UnwindReverse),
		regComposite("demo.stack.remove", UnwindForward),
		regTask("demo.package.install", ClassCompensable),
		regTask("demo.package.update", ClassIrreversible),
	}}
	mod.Actions[len(mod.Actions)-2].Spec.CompensatedBy = "demo.package.update"
	r, err := NewRegistry(mod)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	if def, _ := r.Action(ActionRef{"demo.stack.deploy", 1}); !slices.Equal(def.Spec.LockKeys, []string{"composite:demo"}) {
		t.Errorf("composite lock keys = %v, want [composite:demo]", def.Spec.LockKeys)
	}
}

func TestLifecycleRegistryRules(t *testing.T) {
	module := func(actions ...ActionDef) []Module {
		return []Module{testModule{ID: "demo", Version: 1, Actions: actions}}
	}
	set := revertible("demo.rule.set", SideAgent)
	with := func(def ActionDef, edit func(*ActionDef)) ActionDef {
		edit(&def)
		return def
	}
	tests := []struct {
		name string
		mods []Module
		want string
	}{
		{"composite on the agent", module(with(regComposite("demo.stack.deploy", UnwindReverse),
			func(d *ActionDef) { d.Spec.Side = SideAgent })), "composite actions are controller-side"},
		{"composite without Steps", module(with(regComposite("demo.stack.deploy", UnwindReverse),
			func(d *ActionDef) { d.Handler.Steps = nil })), "Steps and Output handlers"},
		{"composite without Output", module(with(regComposite("demo.stack.deploy", UnwindReverse),
			func(d *ActionDef) { d.Handler.Output = nil })), "Steps and Output handlers"},
		{"composite without Unwind", module(regComposite("demo.stack.deploy", "")), "Unwind reverse or forward"},
		{"composite with an extra key", module(with(regComposite("demo.stack.deploy", UnwindReverse),
			func(d *ActionDef) { d.Spec.LockKeys = []string{"composite:demo", "demo"} })), "exactly the lock key composite:demo"},
		{"composite with another module's key", module(with(regComposite("demo.stack.deploy", UnwindReverse),
			func(d *ActionDef) { d.Spec.LockKeys = []string{"composite:other"} })), "exactly the lock key composite:demo"},
		{"composite with a secret input", module(DefineComposite[regSecretIn, echoOut](
			ActionSpec{Name: "demo.stack.deploy", Version: 1, Unwind: UnwindReverse}, regComposer[regSecretIn]{})), "input has no secret field"},
		{"composite with a nested secret input", module(DefineComposite[regNestedSecretIn, echoOut](
			ActionSpec{Name: "demo.stack.deploy", Version: 1, Unwind: UnwindReverse}, regComposer[regNestedSecretIn]{})), "input has no secret field"},
		{"composite with a Plan handler", module(with(regComposite("demo.stack.deploy", UnwindReverse),
			func(d *ActionDef) { d.Handler.Plan = regPlan })), "Steps and Output handlers only"},
		{"composite key on another class", module(with(mutating("demo.item.purge", ClassIrreversible, SideAgent, ""),
			func(d *ActionDef) { d.Spec.LockKeys = []string{"composite:demo"} })), "only composite actions declare a composite:* lock key"},
		{"compensable with two compensations", module(
			regCompensable("demo.item.add", ActionSpec{LockKeys: []string{"demo"}, CompensatedBy: "demo.item.add"}, regUndoEcho{})),
			"exactly one of an undo handler, CompensatedBy or ForwardOnly"},
		{"compensable forward-only and compensated", module(
			regCompensable("demo.item.add", ActionSpec{ForwardOnly: true, Converges: true, CompensatedBy: "demo.item.add"}, echo{})),
			"exactly one of an undo handler, CompensatedBy or ForwardOnly"},
		{"forward-only without Converges", module(regCompensable("demo.item.pin", ActionSpec{ForwardOnly: true}, echo{})),
			"ForwardOnly action must declare Converges"},
		{"undo handler on an irreversible action", module(DefineMutating[echoIn, echoOut](ActionSpec{
			Name: "demo.item.purge", Version: 1, Class: ClassIrreversible, Side: SideAgent, LockKeys: []string{"demo"}}, regUndoEcho{})),
			"undo handler needs a compensable action"},
		{"undo handler without a lock key", module(regCompensable("demo.item.add", ActionSpec{}, regUndoEcho{})),
			"undo handler needs a compensable action with at least one lock key"},
		{"long apply timeout without LongRunning", module(with(mutating("demo.item.purge", ClassIrreversible, SideAgent, ""),
			func(d *ActionDef) { d.Spec.ApplyTimeout = 46 * time.Second })), "requires LongRunning"},
		{"revertible without RepairedBy", module(with(set, func(d *ActionDef) { d.Spec.RepairedBy = "" })),
			"names its repair action in RepairedBy"},
		{"RepairedBy on a compensable action", module(with(mutating("demo.item.add", ClassCompensable, SideAgent, "demo.item.add"),
			func(d *ActionDef) { d.Spec.RepairedBy = "demo.item.add" })), "only revertible actions name a repair action"},
		{"repair action not registered", module(set), `repair action "demo.rule.repair" is not registered`},
		{"repair action of another module", []Module{
			testModule{ID: "demo", Version: 1, Actions: []ActionDef{with(set, func(d *ActionDef) { d.Spec.RepairedBy = "other.rule.repair" })}},
			testModule{ID: "other", Version: 1, Actions: []ActionDef{mutating("other.rule.repair", ClassIrreversible, SideAgent, "")}},
		}, "is not of module demo"},
		{"repair action with a foreign lock key", module(set, with(regRepairOf(set),
			func(d *ActionDef) { d.Spec.LockKeys = []string{"demo.rule.set", "demo.other"} })), "takes lock demo.other"},
		{"repair action that is a task action", module(set, with(regTask("demo.rule.repair", ClassIrreversible),
			func(d *ActionDef) { d.Spec.LockKeys = []string{"demo.rule.set"} })), "is a task action"},
		{"repair action with a recovery handler", module(set, DefineMutating[echoIn, echoOut](ActionSpec{
			Name: "demo.rule.repair", Version: 1, Class: ClassIrreversible, Side: SideAgent}, regRecoverEcho{})), "has a recovery handler"},
		{"repair action that reads", module(set, readAction("demo.rule.repair")), "want compensable or irreversible"},
		{"task action that is revertible", module(with(regTask("demo.package.install", ClassIrreversible),
			func(d *ActionDef) { d.Spec.Class = ClassRevertible })), "task action is compensable or irreversible"},
		{"task action on the controller", module(with(regTask("demo.package.install", ClassIrreversible),
			func(d *ActionDef) { d.Spec.Side = SideController })), "agent-side with lock keys and an ApplyTimeout"},
		{"task action without lock keys", module(with(regTask("demo.package.install", ClassIrreversible),
			func(d *ActionDef) { d.Spec.LockKeys = nil })), "agent-side with lock keys and an ApplyTimeout"},
		{"task action without ApplyTimeout", module(with(regTask("demo.package.install", ClassIrreversible),
			func(d *ActionDef) { d.Spec.ApplyTimeout = 0 })), "agent-side with lock keys and an ApplyTimeout"},
		{"task action with Apply", module(with(regTask("demo.package.install", ClassIrreversible),
			func(d *ActionDef) { d.Handler.Apply = regApply })), "Task handler and no Apply"},
		{"task action without Task", module(with(regTask("demo.package.install", ClassIrreversible),
			func(d *ActionDef) { d.Handler.Task = nil })), "Task handler and no Apply"},
		{"Task handler on an ordinary action", module(with(mutating("demo.item.purge", ClassIrreversible, SideAgent, ""),
			func(d *ActionDef) { d.Handler.Task = regApply })), "only task actions have a Task handler"},
		{"Steps handler on a read action", module(with(readAction("demo.item.get"),
			func(d *ActionDef) { d.Handler.Steps = regComposite("demo.stack.deploy", UnwindReverse).Handler.Steps })),
			"only composite actions have Steps and Output handlers"},
		{"Output handler on a revertible action", module(with(set,
			func(d *ActionDef) { d.Handler.Output = regComposite("demo.stack.deploy", UnwindReverse).Handler.Output }), regRepairOf(set)),
			"only composite actions have Steps and Output handlers"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewRegistry(tt.mods...)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("NewRegistry error = %v, want one containing %q", err, tt.want)
			}
		})
	}
}

var (
	regPlan  = func(context.Context, Env, json.RawMessage) (PlanOutcome, error) { return PlanOutcome{}, nil }
	regApply = func(context.Context, Env, json.RawMessage) (json.RawMessage, error) { return nil, nil }
)

func TestErasedHandlers(t *testing.T) {
	ctx := context.Background()
	read := readAction("demo.item.get")
	out, err := read.Handler.Read(ctx, Env{}, json.RawMessage(`{"name":"ada"}`))
	if err != nil || string(out) != `{"greeting":"hello ada"}` {
		t.Errorf("Read = %s, %v", out, err)
	}
	_, err = read.Handler.Read(ctx, Env{}, json.RawMessage(`{"name":"ada","extra":1}`))
	var coded *errcode.Error
	if !errors.As(err, &coded) || coded.Code != errcode.InvalidInput {
		t.Errorf("unknown field: err = %v, want %s", err, errcode.InvalidInput)
	}
	for _, raw := range []string{`{"name":"a"} {}`, `{"name":"a"}}`, `{"name":"a"}]`} {
		_, err = read.Handler.Read(ctx, Env{}, json.RawMessage(raw))
		if !errors.As(err, &coded) || coded.Code != errcode.InvalidInput {
			t.Errorf("trailing data %q: err = %v, want %s", raw, err, errcode.InvalidInput)
		}
	}

	rev := revertible("demo.rule.set", SideAgent)
	applied, err := rev.Handler.Apply(ctx, Env{}, json.RawMessage(`{"name":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := rev.Handler.Verify(ctx, Env{}, json.RawMessage(`{"name":"x"}`), applied); err != nil {
		t.Errorf("Verify: %v", err)
	}
	snap, err := rev.Handler.Snapshot(ctx, Env{}, json.RawMessage(`{"name":"x"}`))
	if err != nil || string(snap) != "x" {
		t.Errorf("Snapshot = %q, %v", snap, err)
	}
	if rev.Spec.Class != ClassRevertible {
		t.Errorf("DefineRevertible class = %s", rev.Spec.Class)
	}
}

func TestNewRegistryOwnership(t *testing.T) {
	file := func(p string) OwnedPath { return OwnedPath{Path: p, Kind: OwnedFile} }
	block := func(p string, ids ...string) OwnedPath { return OwnedPath{Path: p, Kind: OwnedBlock, BlockIDs: ids} }
	owner := func(id string, paths ...OwnedPath) Module {
		return testModule{ID: id, Version: 1, OwnedPaths: paths}
	}
	tests := []struct {
		name string
		mods []Module
		want string // "" when the registry is valid
	}{
		{"distinct paths", []Module{owner("core", file("/etc/a")), owner("demo", file("/etc/b"))}, ""},
		{"blocks with distinct ids", []Module{owner("core", block("/etc/ufw/after.rules", "core")),
			owner("demo", block("/etc/ufw/after.rules", "demo"))}, ""},
		{"one module declaring a path twice", []Module{owner("demo", file("/etc/a"), file("/etc/a"))}, ""},
		{"same file", []Module{owner("core", file("/etc/a")), owner("demo", file("/etc/a"))},
			"owned path /etc/a is declared by modules core and demo"},
		{"file and block", []Module{owner("core", file("/etc/a")), owner("demo", block("/etc/a", "x"))},
			"owned path /etc/a is declared by modules core and demo"},
		{"directory and unit", []Module{owner("core", OwnedPath{Path: "/etc/x", Kind: OwnedDir}),
			owner("demo", OwnedPath{Path: "/etc/x", Kind: OwnedUnit})}, "owned path /etc/x is declared by modules core and demo"},
		{"same block", []Module{owner("alpha", block("/etc/a", "x", "y")), owner("demo", block("/etc/a", "y"))},
			"marked block y of /etc/a is declared by modules alpha and demo"},
		{"same user", []Module{owner("core", OwnedPath{Path: "darkpanel", Kind: OwnedUser}),
			owner("demo", OwnedPath{Path: "darkpanel", Kind: OwnedUser})}, "user darkpanel is declared by modules core and demo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewRegistry(tt.mods...)
			switch {
			case tt.want == "" && err != nil:
				t.Fatalf("NewRegistry: %v", err)
			case tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)):
				t.Fatalf("NewRegistry error = %v, want one containing %q", err, tt.want)
			}
		})
	}
}
