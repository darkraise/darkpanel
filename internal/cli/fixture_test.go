package cli

import (
	"context"
	"testing"

	"github.com/darkraise/darkpanel/internal/controller/coreactions"
	"github.com/darkraise/darkpanel/internal/core"
)

// fixtureHandler satisfies every handler interface. The CLI never runs a
// handler; it only reads the declarations.
type fixtureHandler[In, Out any] struct{}

func (fixtureHandler[In, Out]) Read(context.Context, core.Env, In) (Out, error) {
	var out Out
	return out, nil
}

func (fixtureHandler[In, Out]) Plan(context.Context, core.Env, In) (core.PlanOutcome, error) {
	return core.PlanOutcome{}, nil
}

func (fixtureHandler[In, Out]) Apply(context.Context, core.Env, In) (Out, error) {
	var out Out
	return out, nil
}

func (fixtureHandler[In, Out]) Verify(context.Context, core.Env, In, Out) error { return nil }

func (fixtureHandler[In, Out]) Snapshot(context.Context, core.Env, In) ([]byte, error) {
	return nil, nil
}

func (fixtureHandler[In, Out]) Revert(context.Context, core.Env, []byte) (core.RevertOutcome, error) {
	return core.RevertOutcome{}, nil
}

type demoNoInput struct{}

type demoItemQuery struct {
	ID      string `json:"id" jsonschema_description:"Item id."`
	Verbose bool   `json:"verbose,omitempty" jsonschema_description:"Include every detail."`
}

type demoItem struct {
	ID   string   `json:"id"`
	Name string   `json:"name"`
	Tags []string `json:"tags"`
}

type demoItems struct {
	Items []demoItem `json:"items"`
}

type demoAddInput struct {
	Name       string `json:"name" jsonschema_description:"Item name."`
	APIKey     string `json:"apiKey" secret:"true" jsonschema_description:"The upstream API key."`
	Passphrase string `json:"passphrase" secret:"true" jsonschema_description:"The key's passphrase."`
}

type demoAdded struct {
	ID    string `json:"id"`
	Token string `json:"token" secret:"true"`
}

type demoIDInput struct {
	ID string `json:"id" jsonschema_description:"Item id."`
}

type demoLimits struct {
	Max   int `json:"max"`
	Burst int `json:"burst,omitempty"`
}

type demoRuleInput struct {
	Name    string     `json:"name" jsonschema_description:"Rule name."`
	Port    int        `json:"port" jsonschema_description:"Port number."`
	Enabled bool       `json:"enabled,omitempty" jsonschema_description:"Whether the rule is on."`
	Weight  float64    `json:"weight,omitempty" jsonschema_description:"Relative weight."`
	Mode    string     `json:"mode,omitempty" jsonschema:"enum=allow,enum=deny" jsonschema_description:"allow or deny."`
	Ports   []int      `json:"ports,omitempty" jsonschema_description:"Extra ports."`
	Tags    []string   `json:"tags,omitempty" jsonschema_description:"Labels."`
	Limits  demoLimits `json:"limits" jsonschema_description:"Rate limits."`
}

type demoRule struct {
	Name string `json:"name"`
	Port int    `json:"port"`
}

type demoStatus struct {
	Healthy bool `json:"healthy"`
	Rules   int  `json:"rules"`
}

// demoModule is module "demo": agent-side read, compensable, irreversible
// and revertible actions, two versions of demo.item.get, a nested input
// (demo.rule.set's limits), secret inputs and a secret output
// (demo.item.add), and resources sharing a noun with actions (demo.rule).
type demoModule struct{}

func (demoModule) Manifest() core.Manifest {
	agent := func(name string, version int, class core.Class) core.ActionSpec {
		return core.ActionSpec{Name: name, Version: version, Class: class, Side: core.SideAgent, Summary: "Demo " + name + "."}
	}
	add := agent("demo.item.add", 1, core.ClassCompensable)
	add.CompensatedBy = "demo.item.remove"
	rule := agent("demo.rule.set", 1, core.ClassRevertible)
	rule.LockKeys = []string{"demo.rule"}
	return core.Manifest{
		ID: "demo", Version: 1, DefaultEnabled: true,
		Actions: []core.ActionDef{
			core.DefineRead(agent("demo.item.get", 1, core.ClassRead), fixtureHandler[demoItemQuery, demoItem]{}),
			core.DefineRead(agent("demo.item.get", 2, core.ClassRead), fixtureHandler[demoItemQuery, demoItem]{}),
			core.DefineRead(agent("demo.item.list", 1, core.ClassRead), fixtureHandler[demoNoInput, demoItems]{}),
			core.DefineMutating(add, fixtureHandler[demoAddInput, demoAdded]{}),
			core.DefineMutating(agent("demo.item.remove", 1, core.ClassIrreversible), fixtureHandler[demoIDInput, demoNoInput]{}),
			core.DefineRevertible(rule, fixtureHandler[demoRuleInput, demoRule]{}),
		},
		Resources: []core.ResourceDef{
			core.DefineResource("demo.rule", func(context.Context, core.Env) (demoRule, error) { return demoRule{}, nil }),
			core.DefineResource("demo.status", func(context.Context, core.Env) (demoStatus, error) { return demoStatus{}, nil }),
		},
	}
}

// testRegistry is the real core module, built as registry.Build builds it,
// plus demoModule.
func testRegistry(t *testing.T) *core.Registry {
	t.Helper()
	reg, err := core.NewRegistry(coreactions.Module(coreactions.Deps{}), demoModule{})
	if err != nil {
		t.Fatal(err)
	}
	return reg
}
