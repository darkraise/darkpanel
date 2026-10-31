package cloudflare

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/ext"
	"github.com/darkraise/darkpanel/internal/core/schema"
)

// regOtherWave are the name prefixes of the connector and expose actions,
// which a later wave registers in this same manifest.
var regOtherWave = []string{"cloudflare.connector.", "cloudflare.network.", "cloudflare.expose."}

// regDefs returns the manifest's actions of this wave by name.
func regDefs(t *testing.T) map[string]core.ActionDef {
	t.Helper()
	defs := map[string]core.ActionDef{}
	for _, a := range Module().Manifest().Actions {
		if slices.ContainsFunc(regOtherWave, func(p string) bool { return strings.HasPrefix(a.Spec.Name, p) }) {
			continue
		}
		defs[a.Spec.Name] = a
	}
	return defs
}

// TestRegActions: the manifest declares exactly the eleven actions of the
// account, tunnel and hostname surface, controller-side at version 1, with
// the class, lock key and lifecycle handlers of the wire-shape table.
func TestRegActions(t *testing.T) {
	tests := []struct {
		name          string
		class         core.Class
		compensatedBy string
		converges     bool
		internal      bool
		undo, recover bool
	}{
		{name: "cloudflare.account.connect", class: core.ClassCompensable, undo: true},
		{name: "cloudflare.account.disconnect", class: core.ClassIrreversible},
		{name: "cloudflare.account.get", class: core.ClassRead},
		{name: "cloudflare.tunnel.create", class: core.ClassCompensable, undo: true},
		{name: "cloudflare.tunnel.delete", class: core.ClassIrreversible},
		{name: "cloudflare.tunnel.list", class: core.ClassRead},
		{name: "cloudflare.tunnel.token", class: core.ClassRead, internal: true},
		{name: "cloudflare.tunnel.adopt", class: core.ClassCompensable, undo: true},
		{name: "cloudflare.hostname.add", class: core.ClassCompensable, undo: true, recover: true},
		{name: "cloudflare.hostname.remove", class: core.ClassCompensable, compensatedBy: "cloudflare.hostname.add", converges: true, recover: true},
		{name: "cloudflare.hostname.list", class: core.ClassRead},
	}
	defs := regDefs(t)
	var want []string
	for _, tt := range tests {
		want = append(want, tt.name)
		def, ok := defs[tt.name]
		if !ok {
			t.Errorf("%s is not declared", tt.name)
			continue
		}
		s, h := def.Spec, def.Handler
		if s.Version != 1 || s.Side != core.SideController || s.Class != tt.class {
			t.Errorf("%s: version %d side %s class %s, want 1 controller %s", tt.name, s.Version, s.Side, s.Class, tt.class)
		}
		wantLocks := []string{"cloudflare"}
		if tt.class == core.ClassRead {
			wantLocks = nil
		}
		if !slices.Equal(s.LockKeys, wantLocks) {
			t.Errorf("%s: lock keys %v, want %v", tt.name, s.LockKeys, wantLocks)
		}
		if s.CompensatedBy != tt.compensatedBy || s.Converges != tt.converges || s.Internal != tt.internal {
			t.Errorf("%s: compensatedBy %q converges %t internal %t, want %q %t %t",
				tt.name, s.CompensatedBy, s.Converges, s.Internal, tt.compensatedBy, tt.converges, tt.internal)
		}
		if (h.Inverse != nil) != tt.undo || (h.Recover != nil) != tt.recover {
			t.Errorf("%s: undo handler %t recovery handler %t, want %t %t", tt.name, h.Inverse != nil, h.Recover != nil, tt.undo, tt.recover)
		}
		// account.disconnect journals no effect, and an Observer is required only of an action that journals.
		journals := tt.name != "cloudflare.account.disconnect"
		if tt.class != core.ClassRead && (h.Observe != nil) != journals {
			t.Errorf("%s: Observe handler %t, want %t", tt.name, h.Observe != nil, journals)
		}
	}
	slices.Sort(want)
	if got := slices.Sorted(maps.Keys(defs)); !slices.Equal(got, want) {
		t.Errorf("declared actions %v, want %v", got, want)
	}
}

// TestRegInputs: each action is registered with its own input type, so its
// schema accepts the input the action takes and names the field of the one
// it refuses.
func TestRegInputs(t *testing.T) {
	const add = `{"tunnel":"t","hostname":"app.example.com","service":"http://10.0.0.5:8080"}`
	defs := regDefs(t)
	v := schema.NewValidator()
	tests := []struct {
		action, input string
		field         string // "" when the input is accepted
	}{
		{"cloudflare.account.connect", `{"accountId":"0123456789abcdef0123456789abcdef","apiToken":"x"}`, ""},
		{"cloudflare.account.connect", `{"accountId":"0123456789abcdef0123456789abcdef"}`, "/apiToken"},
		{"cloudflare.account.connect", `{"apiToken":"x"}`, "/accountId"},
		{"cloudflare.account.disconnect", `{}`, ""},
		{"cloudflare.account.disconnect", `{"abandonCleanup":true}`, ""},
		{"cloudflare.account.disconnect", `{"abandonCleanup":"yes"}`, "/abandonCleanup"},
		{"cloudflare.account.get", `{}`, ""},
		{"cloudflare.account.get", `{"x":1}`, "/x"},
		{"cloudflare.tunnel.create", `{}`, ""},
		{"cloudflare.tunnel.create", `{"x":1}`, "/x"},
		{"cloudflare.tunnel.delete", `{"tunnel":"darkpanel-01J0"}`, ""},
		{"cloudflare.tunnel.delete", `{}`, "/tunnel"},
		{"cloudflare.tunnel.list", `{}`, ""},
		{"cloudflare.tunnel.list", `{"x":1}`, "/x"},
		{"cloudflare.tunnel.token", `{"tunnel":"t"}`, ""},
		{"cloudflare.tunnel.token", `{}`, "/tunnel"},
		{"cloudflare.tunnel.adopt", `{"tunnel":"t"}`, ""},
		{"cloudflare.tunnel.adopt", `{"tunnel":1}`, "/tunnel"},
		{"cloudflare.hostname.add", add, ""},
		{"cloudflare.hostname.add", `{"tunnel":"t","hostname":"app.example.com","service":"https://10.0.0.5:8443","noTLSVerify":true}`, ""},
		{"cloudflare.hostname.add", `{"tunnel":"t","hostname":"app.example.com"}`, "/service"},
		{"cloudflare.hostname.add", `{"tunnel":"t","hostname":"app.example.com","service":"http://x","noTLSVerify":"yes"}`, "/noTLSVerify"},
		{"cloudflare.hostname.remove", `{"hostname":"app.example.com"}`, ""},
		{"cloudflare.hostname.remove", `{}`, "/hostname"},
		{"cloudflare.hostname.list", `{}`, ""},
		{"cloudflare.hostname.list", `{"x":1}`, "/x"},
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

// regSecretProperty reports whether the schema doc of a type marks property
// as a secret. A schema is a reference to the one definition it holds.
func regSecretProperty(t *testing.T, doc json.RawMessage, property string) bool {
	t.Helper()
	var s struct {
		Ref  string `json:"$ref"`
		Defs map[string]struct {
			Properties map[string]map[string]any `json:"properties"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(doc, &s); err != nil {
		t.Fatal(err)
	}
	def, ok := s.Defs[strings.TrimPrefix(s.Ref, "#/$defs/")]
	if !ok {
		t.Fatalf("the schema holds no definition for %q: %s", s.Ref, doc)
	}
	p, ok := def.Properties[property]
	if !ok {
		t.Fatalf("the schema has no property %q: %s", property, doc)
	}
	return p["x-secret"] == true
}

// TestRegSecretFields: the API token a connect takes and the tunnel token a
// read returns are marked as secrets in their schemas, so the snapshots, the
// audit log and the CLI never show them.
func TestRegSecretFields(t *testing.T) {
	defs := regDefs(t)
	connect, ok := defs["cloudflare.account.connect"]
	if !ok {
		t.Fatal("cloudflare.account.connect is not declared")
	}
	token, ok := defs["cloudflare.tunnel.token"]
	if !ok {
		t.Fatal("cloudflare.tunnel.token is not declared")
	}
	in, err := schema.Input(connect.InputType)
	if err != nil {
		t.Fatal(err)
	}
	if !regSecretProperty(t, in, "apiToken") || regSecretProperty(t, in, "accountId") {
		t.Errorf("cloudflare.account.connect input: want apiToken alone marked secret: %s", in)
	}
	out, err := schema.Output(token.OutputType)
	if err != nil {
		t.Fatal(err)
	}
	if !regSecretProperty(t, out, "token") {
		t.Errorf("cloudflare.tunnel.token output: want token marked secret: %s", out)
	}
}

// TestRegManifest: the module is enabled by default, owns its controller
// tables through its migrations and no agent state, declares no resource
// (controller modules have none), and the registry accepts the whole
// manifest.
func TestRegManifest(t *testing.T) {
	m := Module().Manifest()
	if m.ID != ID || m.Version != 1 || !m.DefaultEnabled {
		t.Errorf("manifest id %q version %d default-enabled %t, want %q 1 true", m.ID, m.Version, m.DefaultEnabled, ID)
	}
	if len(m.Resources) != 0 || len(m.Preflight) != 0 || len(m.OwnedPaths) != 0 {
		t.Errorf("manifest declares %d resources, %d preflight checks and %d owned paths, want none", len(m.Resources), len(m.Preflight), len(m.OwnedPaths))
	}
	mig := m.Migrations[core.SideController]
	if len(m.Migrations) != 1 || mig == nil {
		t.Fatalf("migrations %v, want the controller's only", m.Migrations)
	}
	if b, err := fs.ReadFile(mig, "0001_cloudflare.sql"); err != nil || !strings.Contains(string(b), "cloudflare_hostnames") {
		t.Errorf("0001_cloudflare.sql = %q, %v, want the migration that creates cloudflare_hostnames", b, err)
	}
	if _, err := core.NewRegistry(Module()); err != nil {
		t.Errorf("core.NewRegistry: %v", err)
	}
}

// TestRegHostnamesService: the module contributes no route and exactly the
// cloudflare.hostnames service, which stops when its context ends.
func TestRegHostnamesService(t *testing.T) {
	c, ok := Module().(ext.Controller)
	if !ok {
		t.Fatal("the module does not implement ext.Controller")
	}
	h := newCFHost(t)
	routes, services, err := c.Controller(ext.Deps{
		DB: h.db, Sealer: coretest.Sealer{}, Clock: h.clock, Log: slog.New(slog.DiscardHandler), Audit: &coretest.MemAudit{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 0 || len(services) != 1 || services[0].Name != "cloudflare.hostnames" || services[0].Run == nil {
		t.Fatalf("contributed %d routes and services %+v, want no route and the one service cloudflare.hostnames", len(routes), services)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- services[0].Run(ctx) }()
	cancel()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("the service stopped with %v, want nil or context.Canceled", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the service did not stop when its context ended")
	}
}
