package tailnet

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/ext"
	"github.com/darkraise/darkpanel/internal/core/schema"
)

// TestRegManifestActions: the module declares section 5's tailnet rows plus
// the two read actions the plan adds, in the order of the wire-shape table,
// all controller-side at version 1. Mutating actions lock tailnet and reads
// take no lock; the compensable ones carry an undo handler (and so an
// observer), the irreversible ones none; only authkey.create resolves
// uncertain effects, only policy.patch converges, and no action names a
// compensating action or is forward-only. The module migrates its
// controller tables, has no resources, and the registry accepts it with a
// schema for every action.
func TestRegManifestActions(t *testing.T) {
	m := Module().Manifest()
	if m.ID != ID || m.Version != 1 || !m.DefaultEnabled || len(m.Resources) != 0 {
		t.Errorf("manifest %s v%d enabled %v with %d resources, want tailnet v1 enabled by default without resources",
			m.ID, m.Version, m.DefaultEnabled, len(m.Resources))
	}
	if mig := m.Migrations[core.SideController]; mig == nil || len(m.Migrations) != 1 {
		t.Errorf("migrations %v, want the controller's only", m.Migrations)
	} else if _, err := fs.Stat(mig, "0001_tailnet.sql"); err != nil {
		t.Errorf("controller migrations: %v", err)
	}
	want := []struct {
		name     string
		class    core.Class
		undo     bool
		resolves bool
		converge bool
	}{
		{"tailnet.account.connect", core.ClassCompensable, true, false, false},
		{"tailnet.account.disconnect", core.ClassIrreversible, false, false, false},
		{"tailnet.account.get", core.ClassRead, false, false, false},
		{"tailnet.device.list", core.ClassRead, false, false, false},
		{"tailnet.device.authorize", core.ClassCompensable, true, false, false},
		{"tailnet.device.set-routes", core.ClassCompensable, true, false, false},
		{"tailnet.device.expire", core.ClassIrreversible, false, false, false},
		{"tailnet.device.delete", core.ClassIrreversible, false, false, false},
		{"tailnet.authkey.create", core.ClassCompensable, true, true, false},
		{"tailnet.authkey.list", core.ClassRead, false, false, false},
		{"tailnet.policy.get", core.ClassRead, false, false, false},
		{"tailnet.policy.set", core.ClassCompensable, true, false, false},
		{"tailnet.policy.patch", core.ClassCompensable, true, false, true},
	}
	if len(m.Actions) != len(want) {
		var names []string
		for _, a := range m.Actions {
			names = append(names, a.Spec.Name)
		}
		t.Fatalf("actions %v, want %d", names, len(want))
	}
	for i, w := range want {
		a := m.Actions[i]
		s := a.Spec
		if s.Name != w.name || s.Version != 1 || s.Class != w.class || s.Side != core.SideController {
			t.Errorf("action %d: %s v%d %s on %s, want %s v1 %s on the controller", i, s.Name, s.Version, s.Class, s.Side, w.name, w.class)
		}
		wantLocks := []string{"tailnet"}
		if w.class == core.ClassRead {
			wantLocks = nil
		}
		if !slices.Equal(s.LockKeys, wantLocks) {
			t.Errorf("%s locks %v, want %v", s.Name, s.LockKeys, wantLocks)
		}
		if s.CompensatedBy != "" || s.ForwardOnly || s.Converges != w.converge {
			t.Errorf("%s: compensated by %q forward-only %v converges %v, want none, false, %v", s.Name, s.CompensatedBy, s.ForwardOnly, s.Converges, w.converge)
		}
		if (a.Handler.Inverse != nil) != w.undo || (w.undo && a.Handler.Observe == nil) {
			t.Errorf("%s: inverse %v observe %v, want an undo handler %v with an observer", s.Name, a.Handler.Inverse != nil, a.Handler.Observe != nil, w.undo)
		}
		if (a.Handler.Resolved != nil) != w.resolves {
			t.Errorf("%s: resolved hook %v, want %v", s.Name, a.Handler.Resolved != nil, w.resolves)
		}
		if _, err := schema.ActionSnapshot(a); err != nil {
			t.Errorf("%s: %v", a.Ref(), err)
		}
	}
	if _, err := core.NewRegistry(Module()); err != nil {
		t.Errorf("core.NewRegistry: %v", err)
	}
}

// TestRegInputs: each action is registered with its own input type, so its
// schema accepts the input the action takes and names the field of the one
// it refuses.
func TestRegInputs(t *testing.T) {
	defs := map[string]core.ActionDef{}
	for _, a := range Module().Manifest().Actions {
		defs[a.Spec.Name] = a
	}
	v := schema.NewValidator()
	tests := []struct {
		action, input string
		field         string // "" when the input is accepted
	}{
		{"tailnet.account.connect", `{"tailnet":"example.com","clientId":"k1","clientSecret":"s","tagsAllowed":["tag:darkpanel"]}`, ""},
		{"tailnet.account.connect", `{"tailnet":"example.com","clientId":"k1","tagsAllowed":["tag:darkpanel"]}`, "/clientSecret"},
		{"tailnet.account.disconnect", `{}`, ""},
		{"tailnet.account.disconnect", `{"abandonCleanup":true}`, ""},
		{"tailnet.account.disconnect", `{"abandonCleanup":"yes"}`, "/abandonCleanup"},
		{"tailnet.account.get", `{}`, ""},
		{"tailnet.account.get", `{"x":1}`, "/x"},
		{"tailnet.device.list", `{}`, ""},
		{"tailnet.device.list", `{"x":1}`, "/x"},
		{"tailnet.device.authorize", `{"device":"n1"}`, ""},
		{"tailnet.device.authorize", `{}`, "/device"},
		{"tailnet.device.set-routes", `{"device":"n1","routes":["10.0.0.0/24","0.0.0.0/0","::/0"]}`, ""},
		{"tailnet.device.set-routes", `{"device":"n1"}`, "/routes"},
		{"tailnet.device.expire", `{"device":"n1"}`, ""},
		{"tailnet.device.expire", `{}`, "/device"},
		{"tailnet.device.delete", `{"device":"n1"}`, ""},
		{"tailnet.device.delete", `{}`, "/device"},
		{"tailnet.authkey.create", `{"tags":["tag:a"]}`, ""},
		{"tailnet.authkey.create", `{"tags":["tag:a"],"reusable":true,"ephemeral":true,"preauthorized":true,"expirySeconds":3600}`, ""},
		{"tailnet.authkey.create", `{}`, "/tags"},
		{"tailnet.authkey.create", `{"tags":"tag:a"}`, "/tags"},
		{"tailnet.authkey.list", `{}`, ""},
		{"tailnet.authkey.list", `{"x":1}`, "/x"},
		{"tailnet.policy.get", `{}`, ""},
		{"tailnet.policy.get", `{"x":1}`, "/x"},
		{"tailnet.policy.set", `{"text":"{}"}`, ""},
		{"tailnet.policy.set", `{}`, "/text"},
		{"tailnet.policy.patch", `{"edits":[]}`, ""},
		{"tailnet.policy.patch", `{"edits":[{"op":"set","path":"/randomizeClientPort","value":true},{"op":"addItem","path":"/grants","value":{"src":["*"],"dst":["*"]}}]}`, ""},
		{"tailnet.policy.patch", `{}`, "/edits"},
		{"tailnet.policy.patch", `{"edits":[{"path":"/grants"}]}`, "/edits/0/op"},
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

// TestRegKeySweepService: the module is an ext.Controller that contributes
// no route and the one service tailnet.keysweep, which stops when its
// context ends.
func TestRegKeySweepService(t *testing.T) {
	c, ok := Module().(ext.Controller)
	if !ok {
		t.Fatal("the module does not implement ext.Controller")
	}
	deps := ext.Deps{
		DB:     tnDB(t),
		Sealer: coretest.Sealer{},
		Clock:  coretest.NewFakeClock(time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)),
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		Audit:  &coretest.MemAudit{},
	}
	routes, services, err := c.Controller(deps)
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 0 || len(services) != 1 || services[0].Name != "tailnet.keysweep" || services[0].Run == nil {
		t.Fatalf("contributions: %d routes, services %+v, want no route and the service tailnet.keysweep with a Run", len(routes), services)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- services[0].Run(ctx) }()
	cancel()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("Run after cancel: %v, want nil or context.Canceled", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after its context was cancelled")
	}
}
