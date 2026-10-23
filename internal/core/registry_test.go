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

// ownModule is a module id declaring paths.
func ownModule(id string, paths ...OwnedPath) Module {
	return testModule{ID: id, Version: 1, OwnedPaths: paths}
}

func TestRegistryOwnedPathKinds(t *testing.T) {
	user := OwnedPath{Path: "own", Kind: OwnedUser}
	sysctl := func(key, pattern string) OwnedPath { return OwnedPath{Path: key, Kind: OwnedSysctl, Pattern: pattern} }
	serviceDir := func(user string, subdirs ...string) OwnedPath {
		return OwnedPath{Path: "/var/lib/own", Kind: OwnedServiceDir, User: user, Subdirs: subdirs}
	}
	tokens := OwnedPath{Path: "/var/lib/darkpanel/agent/own-tokens", Kind: OwnedManagedDir}
	tests := []struct {
		name string
		mod  Module
		want string // "" when the registry is valid
	}{
		{"sysctl key", ownModule("own", sysctl("net/ipv4/ip_forward", "^[01]$")), ""},
		{"sysctl key with an interface", ownModule("own", sysctl("net/ipv6/conf/<iface>/forwarding", "^[01]$")), ""},
		{"managed-dir without a pattern", ownModule("own", tokens), ""},
		{"managed-dir with a pattern", ownModule("own",
			OwnedPath{Path: "/var/lib/darkpanel/agent/own-debs", Kind: OwnedManagedDir, Pattern: `^[a-z0-9._-]+\.deb$`}), ""},
		{"service-dir", ownModule("own", user, serviceDir("own", "certs")), ""},
		{"module unit file", ownModule("own", OwnedPath{Path: "/etc/systemd/system/darkpanel-own.service", Kind: OwnedUnit}), ""},
		{"restore file", ownModule("own", OwnedPath{Path: "/etc/ufw/user.rules", Kind: OwnedRestore}), ""},
		{"core unit files anywhere", ownModule(ModuleIDCore,
			OwnedPath{Path: "/etc/systemd/system/darkpanel-agent.service", Kind: OwnedUnit},
			OwnedPath{Path: "/etc/systemd/system/darkpanel-controller.service.d/darkpanel.conf", Kind: OwnedUnit},
			OwnedPath{Path: "/srv/test-root/units/demo.service", Kind: OwnedUnit}), ""},

		{"unknown kind", ownModule("own", OwnedPath{Path: "/run/own.sock", Kind: "socket"}),
			`module own: owned path "/run/own.sock" has unknown kind "socket"`},
		{"sysctl without a pattern", ownModule("own", sysctl("net/ipv4/ip_forward", "")),
			`module own: sysctl key "net/ipv4/ip_forward" has no value Pattern`},
		{"absolute sysctl key", ownModule("own", sysctl("/proc/sys/net/x", "^1$")),
			`module own: sysctl key "/proc/sys/net/x" is absolute`},
		{"two interface segments", ownModule("own", sysctl("net/<iface>/conf/<iface>/x", "^1$")),
			`sysctl key "net/<iface>/conf/<iface>/x" has more than one <iface> segment`},
		{"sysctl segment outside the pattern", ownModule("own", sysctl("net/IPv4/x", "^1$")), `has a segment "IPv4" outside`},
		{"sysctl dot-dot segment", ownModule("own", sysctl("net/../kernel/x", "^1$")), `has a segment ".." outside`},
		{"sysctl empty segment", ownModule("own", sysctl("net//x", "^1$")), `sysctl key "net//x" has an empty segment`},
		{"pattern that does not compile", ownModule("own", OwnedPath{Path: tokens.Path, Kind: OwnedManagedDir, Pattern: "("}),
			"module own: owned path /var/lib/darkpanel/agent/own-tokens: Pattern does not compile"},
		{"pattern on a file", ownModule("own", OwnedPath{Path: "/etc/own.conf", Kind: OwnedFile, Pattern: "^x$"}),
			"module own: owned path /etc/own.conf: Pattern is for kinds sysctl, managed-dir and service-dir only"},
		{"service-dir user not declared", ownModule("own", user, serviceDir("nobody")),
			`module own: service-dir /var/lib/own: User "nobody" is not an owned user of the module`},
		{"service-dir without a user", ownModule("own", user, serviceDir("")), `service-dir /var/lib/own: User "" is not an owned user`},
		{"user on a managed-dir", ownModule("own", user, OwnedPath{Path: tokens.Path, Kind: OwnedManagedDir, User: "own"}),
			"module own: owned path /var/lib/darkpanel/agent/own-tokens: User is for kind service-dir only"},
		{"nested subdirectory", ownModule("own", user, serviceDir("own", "a/b")), `module own: service-dir /var/lib/own: subdirectory "a/b" must match`},
		{"dot-dot subdirectory", ownModule("own", user, serviceDir("own", "..")), `subdirectory ".." must match`},
		{"repeated subdirectory", ownModule("own", user, serviceDir("own", "x", "x")), "service-dir /var/lib/own: subdirectory x listed twice"},
		{"subdirs on a dir", ownModule("own", OwnedPath{Path: "/var/lib/own", Kind: OwnedDir, Subdirs: []string{"certs"}}),
			"module own: owned path /var/lib/own: Subdirs is for kind service-dir only"},
		{"foreign unit file", ownModule("own", OwnedPath{Path: "/etc/systemd/system/nginx.service", Kind: OwnedUnit}),
			"module own: unit path /etc/systemd/system/nginx.service must match"},
		{"root as managed-dir", ownModule("own", OwnedPath{Path: "/", Kind: OwnedManagedDir}),
			`module own: managed-dir path "/" is the root directory`},
		{"relative managed-dir", ownModule("own", OwnedPath{Path: "relative/dir", Kind: OwnedManagedDir}),
			`module own: managed-dir path "relative/dir" is not absolute and clean`},
		{"unclean restore path", ownModule("own", OwnedPath{Path: "/etc/ufw/../ufw/user.rules", Kind: OwnedRestore}),
			`module own: restore path "/etc/ufw/../ufw/user.rules" is not absolute and clean`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewRegistry(tt.mod)
			switch {
			case tt.want == "" && err != nil:
				t.Fatalf("NewRegistry: %v", err)
			case tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)):
				t.Fatalf("NewRegistry error = %v, want one containing %q", err, tt.want)
			}
		})
	}
}

func TestRegistryExternalUnits(t *testing.T) {
	ext := func(units ...ExternalUnit) Module { return testModule{ID: "own", Version: 1, ExternalUnits: units} }
	tailscaled := func(ops ...string) ExternalUnit { return ExternalUnit{Name: "tailscaled.service", Ops: ops} }
	tests := []struct {
		name string
		mod  Module
		want string // "" when the registry is valid
	}{
		{"state and restart", ext(tailscaled(UnitOpState, UnitOpRestart)), ""},
		{"every op of a template instance", ext(ExternalUnit{Name: "getty@tty1.service",
			Ops: []string{UnitOpState, UnitOpStart, UnitOpStop, UnitOpRestart}}), ""},
		{"no ops", ext(tailscaled()), "module own: external unit tailscaled.service has no Ops"},
		{"unknown op", ext(tailscaled("reload")),
			`module own: external unit tailscaled.service: op "reload" is not one of state, start, stop, restart`},
		{"repeated op", ext(tailscaled(UnitOpState, UnitOpState)), "module own: external unit tailscaled.service: op state listed twice"},
		{"not a service", ext(ExternalUnit{Name: "tailscaled", Ops: []string{UnitOpState}}), `module own: external unit "tailscaled" must match`},
		{"listed twice", ext(tailscaled(UnitOpState), tailscaled(UnitOpRestart)), "module own: external unit tailscaled.service listed twice"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewRegistry(tt.mod)
			switch {
			case tt.want == "" && err != nil:
				t.Fatalf("NewRegistry: %v", err)
			case tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)):
				t.Fatalf("NewRegistry error = %v, want one containing %q", err, tt.want)
			}
		})
	}
}

// scpGuardedIn is an input that embeds Guarded.
type scpGuardedIn struct {
	Guarded
	Name string `json:"name"`
}

// scpMutator is a forward-only handler over input In.
type scpMutator[In any] struct{}

func (scpMutator[In]) Plan(context.Context, Env, In) (PlanOutcome, error) { return PlanOutcome{}, nil }

func (scpMutator[In]) Apply(context.Context, Env, In) (echoOut, error) { return echoOut{}, nil }

func (scpMutator[In]) Verify(context.Context, Env, In, echoOut) error { return nil }

// scpAction is the forward-only compensable agent action scp.rule.add over
// input In, with spec's Guards and MinScope.
func scpAction[In any](spec ActionSpec) ActionDef {
	spec.Name, spec.Version, spec.Class, spec.Side = "scp.rule.add", 1, ClassCompensable, SideAgent
	spec.ForwardOnly, spec.Converges = true, true
	return DefineMutating[In, echoOut](spec, scpMutator[In]{})
}

func TestRegistryGuards(t *testing.T) {
	lockout := GuardSpec{ID: "scp.lockout", Overridable: true}
	tests := []struct {
		name string
		def  ActionDef
		want string // "" when the registry is valid
	}{
		{"guards with Guarded", scpAction[scpGuardedIn](ActionSpec{Guards: []GuardSpec{lockout, {ID: "scp.quota"}}}), ""},
		{"guards without Guarded", scpAction[echoIn](ActionSpec{Guards: []GuardSpec{lockout}}),
			"action scp.rule.add/v1: an action with Guards has an input type that embeds core.Guarded"},
		{"Guarded without guards", scpAction[scpGuardedIn](ActionSpec{}),
			"action scp.rule.add/v1: an input type that embeds core.Guarded belongs to an action with Guards"},
		{"guard without an ID", scpAction[scpGuardedIn](ActionSpec{Guards: []GuardSpec{{Overridable: true}}}),
			"action scp.rule.add/v1: a guard has an empty ID"},
		{"guard declared twice", scpAction[scpGuardedIn](ActionSpec{Guards: []GuardSpec{lockout, {ID: "scp.lockout"}}}),
			"action scp.rule.add/v1: guard scp.lockout is declared twice"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewRegistry(testModule{ID: "scp", Version: 1, Actions: []ActionDef{tt.def}})
			switch {
			case tt.want == "" && err != nil:
				t.Fatalf("NewRegistry: %v", err)
			case tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)):
				t.Fatalf("NewRegistry error = %v, want one containing %q", err, tt.want)
			}
		})
	}
}

func TestRegistryMinScope(t *testing.T) {
	tests := []struct {
		name  string
		scope Scope
		want  string // "" when the registry is valid
	}{
		{"unknown scope", "root", `action scp.rule.add/v1: unknown MinScope "root"`},
		{"below the default", ScopeRead, "action scp.rule.add/v1: MinScope read is below the operate scope the action needs; MinScope only raises"},
		{"equal to the default", ScopeOperate, ""},
		{"raised", ScopeAdmin, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			def := scpAction[echoIn](ActionSpec{MinScope: tt.scope})
			_, err := NewRegistry(testModule{ID: "scp", Version: 1, Actions: []ActionDef{def}})
			switch {
			case tt.want == "" && err != nil:
				t.Fatalf("NewRegistry: %v", err)
			case tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)):
				t.Fatalf("NewRegistry error = %v, want one containing %q", err, tt.want)
			}
		})
	}
}

func TestRegistryRevealRules(t *testing.T) {
	admin := ActionSpec{Name: "demo.admin.reveal-password", Version: 1, Side: SideAgent, MinScope: ScopeAdmin}
	with := func(def ActionDef, edit func(*ActionDef)) ActionDef {
		edit(&def)
		return def
	}
	forward := regCompensable("demo.item.pin", ActionSpec{ForwardOnly: true, Converges: true}, echo{})
	tests := []struct {
		name string
		def  ActionDef
		want string // "" when the registry is valid
	}{
		{"reveal with MinScope admin", DefineReveal(admin, rvlReveal), ""},
		{"internal read without MinScope", DefineInternalRead(ActionSpec{Name: "demo.cert.get", Version: 1, Side: SideAgent}, rvlReveal), ""},
		{"internal read on the controller", DefineInternalRead(ActionSpec{Name: "demo.token.get", Version: 1, Side: SideController}, rvlReveal), ""},

		{"reveal without MinScope admin", DefineReveal(ActionSpec{Name: "demo.admin.reveal-password", Version: 1, Side: SideAgent}, rvlReveal),
			"action demo.admin.reveal-password/v1: a reveal action declares MinScope admin"},
		{"Reveal on a DefineRead action", with(readAction("demo.admin.get"), func(d *ActionDef) { d.Spec.Reveal, d.Spec.MinScope = true, ScopeAdmin }),
			"action demo.admin.get/v1: a reveal action or internal read has a Reveal handler and no Read handler"},
		{"Reveal handler without a flag", with(DefineReveal(admin, rvlReveal), func(d *ActionDef) { d.Spec.Reveal = false }),
			"action demo.admin.reveal-password/v1: only reveal actions and internal reads have a Reveal handler"},
		{"both flags", with(DefineReveal(admin, rvlReveal), func(d *ActionDef) { d.Spec.Internal = true }),
			"action demo.admin.reveal-password/v1: an action is a reveal action or an internal read, not both"},
		{"Internal on a compensable action", with(forward, func(d *ActionDef) { d.Spec.Internal = true }),
			"action demo.item.pin/v1: reveal actions and internal reads are read actions"},
		{"Reveal handler on a compensable action", with(forward, func(d *ActionDef) { d.Handler.Reveal = DefineReveal(admin, rvlReveal).Handler.Reveal }),
			"action demo.item.pin/v1: only reveal actions and internal reads have a Reveal handler"},
		{"reveal with a Read handler", with(DefineReveal(admin, rvlReveal), func(d *ActionDef) { d.Handler.Read = readAction("demo.admin.get").Handler.Read }),
			"action demo.admin.reveal-password/v1: a reveal action or internal read has a Reveal handler and no Read handler"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewRegistry(testModule{ID: "demo", Version: 1, Actions: []ActionDef{tt.def}})
			switch {
			case tt.want == "" && err != nil:
				t.Fatalf("NewRegistry: %v", err)
			case tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)):
				t.Fatalf("NewRegistry error = %v, want one containing %q", err, tt.want)
			}
		})
	}
}

// strmModule is a module id with streams and, for the CLI clash cases,
// actions and resources.
type strmModule struct {
	id        string
	streams   []StreamDef
	actions   []ActionDef
	resources []ResourceDef
}

func (m strmModule) Manifest() Manifest {
	return Manifest{ID: m.id, Version: 1, Actions: m.actions, Resources: m.resources}
}

func (m strmModule) Streams() []StreamDef { return m.streams }

func TestRegistryStreams(t *testing.T) {
	logs := DefineStream("logs", strmLogs)
	r, err := NewRegistry(
		strmModule{id: "strm", streams: []StreamDef{DefineStream("stats", strmLogs), logs}, actions: []ActionDef{readAction("strm.item.get")}},
		strmModule{id: "alpha", streams: []StreamDef{DefineStream("pull", strmLogs)}},
		testModule{ID: "plain", Version: 1},
	)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	var got []string
	for _, st := range r.Streams() {
		got = append(got, st.Module+"."+st.Def.Name)
	}
	if want := []string{"alpha.pull", "strm.logs", "strm.stats"}; !slices.Equal(got, want) {
		t.Errorf("Streams() = %v, want %v", got, want)
	}
	if def, ok := r.Stream("strm", "logs"); !ok || def.Name != "logs" || def.EventType != logs.EventType {
		t.Errorf("Stream(strm, logs) = %+v, %v", def, ok)
	}
	if _, ok := r.Stream("strm", "pull"); ok {
		t.Error("Stream(strm, pull) found alpha's stream")
	}

	noRun, noEvent := logs, logs
	noRun.Run, noEvent.EventType = nil, nil
	tests := []struct {
		name string
		mod  strmModule
		want string
	}{
		{"name with a capital", strmModule{id: "strm", streams: []StreamDef{DefineStream("Logs", strmLogs)}}, `module strm: stream "Logs" must match`},
		{"name repeated", strmModule{id: "strm", streams: []StreamDef{logs, logs}}, "stream strm.logs registered twice"},
		{"no Run", strmModule{id: "strm", streams: []StreamDef{noRun}}, "stream strm.logs needs Run, ParamsType and EventType"},
		{"no EventType", strmModule{id: "strm", streams: []StreamDef{noEvent}}, "stream strm.logs needs Run, ParamsType and EventType"},
		{"command of a resource", strmModule{id: "strm", streams: []StreamDef{DefineStream("list", strmLogs)}, resources: []ResourceDef{resource("strm.list")}},
			`stream strm.list: the CLI command "strm list" clashes with strm.list`},
		{"group of an action", strmModule{id: "strm", streams: []StreamDef{DefineStream("list", strmLogs)}, actions: []ActionDef{readAction("strm.list.get")}},
			`stream strm.list: the CLI command "strm list" clashes with strm.list.get`},
		{"core stream without the module word", strmModule{id: ModuleIDCore, streams: []StreamDef{DefineStream("item", strmLogs)}, actions: []ActionDef{readAction("core.item.get")}},
			`stream core.item: the CLI command "item" clashes with core.item.get`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewRegistry(tt.mod)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("NewRegistry error = %v, want one containing %q", err, tt.want)
			}
		})
	}
}

// bgRegModule is a testModule with background tasks.
type bgRegModule struct {
	testModule
	tasks []BackgroundTask
}

func (m bgRegModule) BackgroundTasks() []BackgroundTask { return m.tasks }

func TestRegistryBackgroundTasks(t *testing.T) {
	run := func(context.Context, TaskContext) error { return nil }
	syncTask := BackgroundTask{ID: "demo.sync", Every: time.Minute, LockKeys: []string{"demo"}, Run: run}
	watch := BackgroundTask{ID: "demo.re-watch", Run: run,
		Watch: func(context.Context, Env, func()) error { return nil }}
	reg, err := NewRegistry(
		bgRegModule{testModule{ID: "demo", Version: 1}, []BackgroundTask{syncTask, watch}},
		testModule{ID: "plain", Version: 1},
		bgRegModule{testModule{ID: "dem", Version: 1}, []BackgroundTask{{ID: "dem.sync", Run: run}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	ids := func(tasks []BackgroundTask) []string {
		var out []string
		for _, task := range tasks {
			out = append(out, task.ID)
		}
		return out
	}
	for _, tc := range []struct {
		module string
		want   []string
	}{{"demo", []string{"demo.sync", "demo.re-watch"}}, {"dem", []string{"dem.sync"}}, {"plain", nil}, {"nope", nil}} {
		if got := ids(reg.BackgroundTasks(tc.module)); !slices.Equal(got, tc.want) {
			t.Errorf("BackgroundTasks(%s) = %v, want %v", tc.module, got, tc.want)
		}
	}

	tests := []struct {
		name string
		task BackgroundTask
		want string
	}{
		{"id of another module", BackgroundTask{ID: "other.sync", Run: run}, `must match`},
		{"id without a name", BackgroundTask{ID: "demo", Run: run}, `must match`},
		{"upper-case id", BackgroundTask{ID: "demo.Sync", Run: run}, `must match`},
		{"repeated id", syncTask, "background task demo.sync registered twice"},
		{"no Run", BackgroundTask{ID: "demo.idle"}, "background task demo.idle has no Run"},
		{"negative Every", BackgroundTask{ID: "demo.back", Every: -time.Second, Run: run}, "background task demo.back: Every -1s is negative"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewRegistry(bgRegModule{testModule{ID: "demo", Version: 1}, []BackgroundTask{syncTask, tt.task}})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("NewRegistry error = %v, want one containing %q", err, tt.want)
			}
		})
	}
}
