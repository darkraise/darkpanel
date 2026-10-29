package firewall

import (
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/schema"
)

// TestRegDockerInputs: each Docker action is registered with its own input
// type, so its schema accepts the input the action takes and names the
// field of the one it refuses.
func TestRegDockerInputs(t *testing.T) {
	const (
		binding = `{"selector":"web","hostPort":8080,"proto":"tcp"}`
		ruleID  = `"01J00000000000000000000001"`
	)
	defs := map[string]core.ActionDef{}
	for _, a := range Module().Manifest().Actions {
		defs[a.Spec.Name] = a
	}
	v := schema.NewValidator()
	tests := []struct {
		action, input string
		field         string // "" when the input is accepted
	}{
		{"firewall.docker.enable", `{}`, ""},
		{"firewall.docker.enable", `{"overrides":["firewall.lockout"]}`, ""},
		{"firewall.docker.enable", `{"x":1}`, "/x"},
		{"firewall.docker.disable", `{}`, ""},
		{"firewall.docker.disable", `{"x":1}`, "/x"},
		{"firewall.docker.repair", `{"discardForeign":true,"manual":true}`, ""},
		{"firewall.docker.repair", `{"discardForeign":"yes"}`, "/discardForeign"},
		{"firewall.docker.set-sources", `{}`, ""},
		{"firewall.docker.set-sources", `{"privateSources":["10.0.0.0/8","fc00::/7"],"forwardExemptions":[]}`, ""},
		{"firewall.docker.set-sources", `{"privateSources":"10.0.0.0/8"}`, "/privateSources"},
		{"firewall.container.allow", `{"binding":` + binding + `}`, ""},
		{"firewall.container.allow", `{"binding":` + binding + `,"from":"203.0.113.0/24"}`, ""},
		{"firewall.container.allow", `{}`, "/binding"},
		{"firewall.container.deny", `{"binding":` + binding + `,"from":"203.0.113.0/24"}`, ""},
		{"firewall.container.deny", `{"binding":` + binding + `}`, "/from"},
		{"firewall.container.remove", `{"id":` + ruleID + `}`, ""},
		{"firewall.container.remove", `{}`, "/id"},
		{"firewall.containers.apply", `{"ops":[]}`, ""},
		{"firewall.containers.apply", `{"ops":[{"op":"allow","binding":` + binding + `},{"op":"remove","id":` + ruleID + `}]}`, ""},
		{"firewall.containers.apply", `{}`, "/ops"},
	}
	for _, tt := range tests {
		def, ok := defs[tt.action]
		if !ok {
			t.Errorf("%s is not declared", tt.action)
			continue
		}
		err := v.ValidateInput(def, json.RawMessage(tt.input))
		if tt.field == "" {
			if err != nil {
				t.Errorf("%s %s: %v, want it accepted", tt.action, tt.input, err)
			}
			continue
		}
		var coded *errcode.Error
		if !errors.As(err, &coded) || coded.Code != errcode.InvalidInput ||
			!slices.ContainsFunc(coded.Fields, func(f errcode.FieldError) bool { return f.Path == tt.field }) {
			t.Errorf("%s %s: %v, want %s naming %s", tt.action, tt.input, err, errcode.InvalidInput, tt.field)
		}
	}
}

// TestRegReconcileTask: the module is a core.Background with the one task
// firewall.reconcile, woken every five minutes and by Docker events, which
// applies only actions of the firewall lock, and the registry accepts it.
func TestRegReconcileTask(t *testing.T) {
	bg, ok := Module().(core.Background)
	if !ok {
		t.Fatal("the module does not implement core.Background")
	}
	tasks := bg.BackgroundTasks()
	if len(tasks) != 1 {
		t.Fatalf("%d background tasks, want 1", len(tasks))
	}
	task := tasks[0]
	if task.ID != "firewall.reconcile" || task.Every != 5*time.Minute || !slices.Equal(task.LockKeys, []string{"firewall"}) ||
		task.Run == nil || task.Watch == nil {
		t.Errorf("task %q every %s locks %v run %v watch %v, want firewall.reconcile every 5m on the firewall lock with a pass and a watch",
			task.ID, task.Every, task.LockKeys, task.Run != nil, task.Watch != nil)
	}
	reg, err := core.NewRegistry(Module())
	if err != nil {
		t.Fatal(err)
	}
	if got := reg.BackgroundTasks(ID); len(got) != 1 || got[0].ID != "firewall.reconcile" {
		t.Errorf("registry background tasks of the firewall %v, want firewall.reconcile", got)
	}
}
