package core

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"strings"
	"testing"

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

func (echo) Revert(context.Context, Env, []byte) error { return nil }

type testModule Manifest

func (m testModule) Manifest() Manifest { return Manifest(m) }

func readAction(name string) ActionDef {
	return DefineRead[echoIn, echoOut](ActionSpec{Name: name, Version: 1, Side: SideAgent}, echo{})
}

func mutating(name string, class Class, side Side, compensatedBy string) ActionDef {
	return DefineMutating[echoIn, echoOut](ActionSpec{Name: name, Version: 1, Class: class, Side: side, CompensatedBy: compensatedBy}, echo{})
}

func revertible(name string, side Side) ActionDef {
	return DefineRevertible[echoIn, echoOut](ActionSpec{Name: name, Version: 1, Side: side, LockKeys: []string{name}}, echo{})
}

func resource(name string) ResourceDef {
	return DefineResource(name, func(context.Context, Env) (echoOut, error) { return echoOut{"hi"}, nil })
}

// A resource <m>.<r> is `darkpanel <m> <r> get`, so it may share its noun
// with actions whose verbs differ (spec section 15).
func TestNewRegistryAcceptsResourceBesideActionNoun(t *testing.T) {
	mod := testModule{
		ID: "demo", Version: 1,
		Actions:   []ActionDef{revertible("demo.rule.set", SideAgent)},
		Resources: []ResourceDef{resource("demo.rule")},
	}
	if _, err := NewRegistry(mod); err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
}

func TestNewRegistryAccepts(t *testing.T) {
	mod := testModule{
		ID: "demo", Version: 1,
		Actions: []ActionDef{
			readAction("demo.item.get"),
			mutating("demo.item.add", ClassCompensable, SideAgent, "demo.item.remove"),
			mutating("demo.item.remove", ClassIrreversible, SideAgent, ""),
			revertible("demo.rule.set", SideAgent),
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
	if got := len(r.Actions()); got != 4 {
		t.Errorf("len(Actions()) = %d, want 4", got)
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
		{"compensable without compensation", []Module{testModule{ID: "demo", Version: 1, Actions: []ActionDef{mutating("demo.item.add", ClassCompensable, SideAgent, "")}}}, "name a compensating action"},
		{"unknown compensation", []Module{testModule{ID: "demo", Version: 1, Actions: []ActionDef{mutating("demo.item.add", ClassCompensable, SideAgent, "demo.item.gone")}}}, "not registered"},
		{"compensation on irreversible", []Module{testModule{ID: "demo", Version: 1, Actions: []ActionDef{mutating("demo.item.add", ClassIrreversible, SideAgent, "demo.item.add")}}}, "only compensable"},
		{"read with locks", []Module{testModule{ID: "demo", Version: 1, Actions: []ActionDef{locked}}}, "take no locks"},
		{"missing handler", []Module{testModule{ID: "demo", Version: 1, Actions: []ActionDef{noVerify}}}, "Plan, Apply and Verify"},
		{"unknown class", []Module{testModule{ID: "demo", Version: 1, Actions: []ActionDef{mutating("demo.item.add", Class("magic"), SideAgent, "")}}}, "unknown class"},
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
