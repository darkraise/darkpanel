package cloudflare

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"maps"
	"reflect"
	"regexp"
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

// regDefs returns the manifest's actions by name.
func regDefs(t *testing.T) map[string]core.ActionDef {
	t.Helper()
	defs := map[string]core.ActionDef{}
	for _, a := range Module().Manifest().Actions {
		if _, dup := defs[a.Spec.Name]; dup {
			t.Errorf("%s is declared twice", a.Spec.Name)
		}
		defs[a.Spec.Name] = a
	}
	return defs
}

// TestRegActions: the manifest declares exactly the nineteen actions of the
// account, tunnel, hostname, connector and exposure surface at version 1, each
// on its side with the class, lock key, deadline, unwind policy and lifecycle
// handlers of the table.
func TestRegActions(t *testing.T) {
	tests := []struct {
		name          string
		class         core.Class
		agent         bool
		compensatedBy string
		converges     bool
		internal      bool
		undo, recover bool
		apply         time.Duration // the ApplyTimeout of a long-running action; 0 for none
		unwind        core.Unwind
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
		{name: "cloudflare.connector.run", class: core.ClassCompensable, agent: true, undo: true, apply: 10 * time.Minute},
		{name: "cloudflare.connector.stop", class: core.ClassCompensable, agent: true, compensatedBy: "cloudflare.connector.run", converges: true},
		{name: "cloudflare.connector.list", class: core.ClassRead, agent: true},
		{name: "cloudflare.network.attach", class: core.ClassCompensable, agent: true, undo: true},
		{name: "cloudflare.network.detach", class: core.ClassCompensable, agent: true, undo: true, converges: true},
		{name: "cloudflare.connector.deploy", class: core.ClassComposite, unwind: core.UnwindReverse},
		{name: "cloudflare.expose.container", class: core.ClassComposite, unwind: core.UnwindReverse},
		{name: "cloudflare.expose.remove", class: core.ClassComposite, unwind: core.UnwindForward},
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
		side := core.SideController
		if tt.agent {
			side = core.SideAgent
		}
		if s.Version != 1 || s.Side != side || s.Class != tt.class || s.Unwind != tt.unwind {
			t.Errorf("%s: version %d side %s class %s unwind %q, want 1 %s %s %q", tt.name, s.Version, s.Side, s.Class, s.Unwind, side, tt.class, tt.unwind)
		}
		wantLocks := []string{"cloudflare"}
		switch tt.class {
		case core.ClassRead:
			wantLocks = nil
		case core.ClassComposite:
			wantLocks = []string{"composite:cloudflare"}
		}
		if !slices.Equal(s.LockKeys, wantLocks) {
			t.Errorf("%s: lock keys %v, want %v", tt.name, s.LockKeys, wantLocks)
		}
		if s.LongRunning != (tt.apply > 0) || s.ApplyTimeout != tt.apply {
			t.Errorf("%s: long-running %t apply timeout %s, want %t %s", tt.name, s.LongRunning, s.ApplyTimeout, tt.apply > 0, tt.apply)
		}
		if s.CompensatedBy != tt.compensatedBy || s.Converges != tt.converges || s.Internal != tt.internal {
			t.Errorf("%s: compensatedBy %q converges %t internal %t, want %q %t %t",
				tt.name, s.CompensatedBy, s.Converges, s.Internal, tt.compensatedBy, tt.converges, tt.internal)
		}
		if (h.Inverse != nil) != tt.undo || (h.Recover != nil) != tt.recover {
			t.Errorf("%s: undo handler %t recovery handler %t, want %t %t", tt.name, h.Inverse != nil, h.Recover != nil, tt.undo, tt.recover)
		}
		// account.disconnect and connector.stop journal no effect (the stop is compensated by connector.run, not
		// undone), and an Observer is required only of an action that journals; a composite is run by the engine and
		// has none.
		journals := tt.class != core.ClassComposite && tt.name != "cloudflare.account.disconnect" &&
			tt.name != "cloudflare.connector.stop"
		if tt.class != core.ClassRead && (h.Observe != nil) != journals {
			t.Errorf("%s: Observe handler %t, want %t", tt.name, h.Observe != nil, journals)
		}
		if tt.class == core.ClassComposite && (h.Steps == nil || h.Output == nil || h.Plan != nil || h.Apply != nil) {
			t.Errorf("%s: a composite has Steps and Output handlers and no Plan or Apply", tt.name)
		}
	}
	slices.Sort(want)
	if got := slices.Sorted(maps.Keys(defs)); !slices.Equal(got, want) {
		t.Errorf("declared actions %v, want %v", got, want)
	}
}

// regScopes are the scopes of the connector surface: the read needs read, the
// agent mutations operate and every composite admin.
var regScopes = map[string]core.Scope{
	"cloudflare.connector.run":    core.ScopeOperate,
	"cloudflare.connector.stop":   core.ScopeOperate,
	"cloudflare.connector.list":   core.ScopeRead,
	"cloudflare.network.attach":   core.ScopeOperate,
	"cloudflare.network.detach":   core.ScopeOperate,
	"cloudflare.connector.deploy": core.ScopeAdmin,
	"cloudflare.expose.container": core.ScopeAdmin,
	"cloudflare.expose.remove":    core.ScopeAdmin,
}

// TestRegScopes: no connector action is registered with a scope other than the
// one its class and side give it.
func TestRegScopes(t *testing.T) {
	defs := regDefs(t)
	for _, name := range slices.Sorted(maps.Keys(regScopes)) {
		def, ok := defs[name]
		if !ok {
			t.Errorf("%s is not declared", name)
			continue
		}
		if got := core.RequiredScope(def.Spec); got != regScopes[name] {
			t.Errorf("%s needs scope %s, want %s", name, got, regScopes[name])
		}
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
		{"cloudflare.connector.run", `{"tunnel":"` + regTunnelID + `","token":"x"}`, ""},
		{"cloudflare.connector.run", `{"tunnel":"` + regTunnelID + `"}`, "/token"},
		{"cloudflare.connector.run", `{"token":"x"}`, "/tunnel"},
		{"cloudflare.connector.stop", `{"tunnel":"` + regTunnelID + `"}`, ""},
		{"cloudflare.connector.stop", `{}`, "/tunnel"},
		{"cloudflare.connector.list", `{}`, ""},
		{"cloudflare.connector.list", `{"x":1}`, "/x"},
		{"cloudflare.network.attach", `{"tunnel":"t","network":"appnet","claimant":"expose:app.example.com","target":"web","address":"10.9.0.2"}`, ""},
		{"cloudflare.network.attach", `{"tunnel":"t","network":"appnet"}`, "/claimant"},
		{"cloudflare.network.attach", `{"tunnel":"t","network":1,"claimant":"c"}`, "/network"},
		{"cloudflare.network.detach", `{"tunnel":"t","network":"","claimant":"c"}`, ""},
		{"cloudflare.network.detach", `{"tunnel":"t","claimant":"c"}`, "/network"},
		{"cloudflare.connector.deploy", `{"hostId":"h1"}`, ""},
		{"cloudflare.connector.deploy", `{"hostId":"h1","tunnel":"edge"}`, ""},
		{"cloudflare.connector.deploy", `{}`, "/hostId"},
		{"cloudflare.connector.deploy", `{"hostId":"h1","tunnel":1}`, "/tunnel"},
		{"cloudflare.expose.container", `{"hostId":"h1","tunnel":"edge","container":"web","port":8080,"network":"appnet","hostname":"app.example.com"}`, ""},
		{"cloudflare.expose.container", `{"hostId":"h1","tunnel":"edge","container":"web","port":"8080","network":"appnet","hostname":"app.example.com"}`, "/port"},
		{"cloudflare.expose.container", `{"hostId":"h1","tunnel":"edge","container":"web","port":0,"network":"appnet","hostname":"app.example.com"}`, "/port"},
		{"cloudflare.expose.container", `{"tunnel":"edge","container":"web","port":8080,"network":"appnet","hostname":"app.example.com"}`, "/hostId"},
		{"cloudflare.expose.remove", `{"hostId":"h1","hostname":"app.example.com"}`, ""},
		{"cloudflare.expose.remove", `{"hostId":"h1"}`, "/hostname"},
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

// TestRegSecretFields: the API token a connect takes, the tunnel token a read
// returns and the tunnel token a connector run takes are marked as secrets in
// their schemas, so the snapshots, the audit log and the CLI never show them.
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
	run, ok := defs["cloudflare.connector.run"]
	if !ok {
		t.Fatal("cloudflare.connector.run is not declared")
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
	in, err = schema.Input(run.InputType)
	if err != nil {
		t.Fatal(err)
	}
	if !regSecretProperty(t, in, "token") || regSecretProperty(t, in, "tunnel") {
		t.Errorf("cloudflare.connector.run input: want token alone marked secret: %s", in)
	}
}

// TestRegNoOtherConnectorSecrets: of the connector surface only the input of
// connector.run holds a secret; no output and no composite input does.
func TestRegNoOtherConnectorSecrets(t *testing.T) {
	defs := regDefs(t)
	for _, name := range slices.Sorted(maps.Keys(regScopes)) {
		def, ok := defs[name]
		if !ok {
			t.Errorf("%s is not declared", name)
			continue
		}
		in, err := schema.Input(def.InputType)
		if err != nil {
			t.Errorf("%s input: %v", name, err)
		}
		out, err := schema.Output(def.OutputType)
		if err != nil {
			t.Errorf("%s output: %v", name, err)
		}
		if secret := bytes.Contains(in, []byte("x-secret")); secret != (name == "cloudflare.connector.run") {
			t.Errorf("%s: its input schema marks a field secret: %t", name, secret)
		}
		if bytes.Contains(out, []byte("x-secret")) {
			t.Errorf("%s: its output schema marks a field secret", name)
		}
	}
}

// TestRegManifest: the module is enabled by default, owns its controller
// tables and its agent tables through its migrations, owns the one managed
// directory of the tunnel token files, declares no resource, and the registry
// accepts the whole manifest.
func TestRegManifest(t *testing.T) {
	m := Module().Manifest()
	if m.ID != ID || m.Version != 1 || !m.DefaultEnabled {
		t.Errorf("manifest id %q version %d default-enabled %t, want %q 1 true", m.ID, m.Version, m.DefaultEnabled, ID)
	}
	if len(m.Resources) != 0 || len(m.Preflight) != 0 {
		t.Errorf("manifest declares %d resources and %d preflight checks, want none", len(m.Resources), len(m.Preflight))
	}
	if len(m.OwnedPaths) != 1 {
		t.Fatalf("manifest owns %d paths %+v, want the one managed directory of the token files", len(m.OwnedPaths), m.OwnedPaths)
	}
	owned := m.OwnedPaths[0]
	if owned.Path != "/var/lib/darkpanel/agent/cloudflare-tokens" || owned.Kind != core.OwnedManagedDir {
		t.Errorf("owned path %s of kind %s, want /var/lib/darkpanel/agent/cloudflare-tokens of kind %s", owned.Path, owned.Kind, core.OwnedManagedDir)
	}
	pattern, err := regexp.Compile(owned.Pattern)
	if err != nil {
		t.Fatalf("the owned path's file-name pattern %q: %v", owned.Pattern, err)
	}
	for name, accepted := range map[string]bool{
		regTunnelID + ".token":                  true,
		strings.ToUpper(regTunnelID) + ".token": false,
		regTunnelID + ".token.bak":              false,
		"../" + regTunnelID + ".token":          false,
		"notes.txt":                             false,
	} {
		if pattern.MatchString(name) != accepted {
			t.Errorf("file name %q: pattern match %t, want %t", name, !accepted, accepted)
		}
	}
	if len(m.Migrations) != 2 || m.Migrations[core.SideController] == nil || m.Migrations[core.SideAgent] == nil {
		t.Fatalf("migrations %v, want the controller's and the agent's", m.Migrations)
	}
	if b, err := fs.ReadFile(m.Migrations[core.SideController], "0001_cloudflare.sql"); err != nil || !strings.Contains(string(b), "cloudflare_hostnames") {
		t.Errorf("controller 0001_cloudflare.sql = %q, %v, want the migration that creates cloudflare_hostnames", b, err)
	}
	b, err := fs.ReadFile(m.Migrations[core.SideAgent], "0001_cloudflare.sql")
	if err != nil || !strings.Contains(string(b), "cloudflare_connectors") || !strings.Contains(string(b), "cloudflare_attachments") {
		t.Errorf("agent 0001_cloudflare.sql = %q, %v, want the migration that creates cloudflare_connectors and cloudflare_attachments", b, err)
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
	// Cancel only once the first pass has run and scheduled the next, so the
	// test stops a running service rather than one never started.
	for deadline := time.Now().Add(10 * time.Second); h.clock.Pending() == 0; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the service did not schedule its next pass")
		}
	}
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

// TestRegConnectorTypes: each connector action is registered with its own
// input and output type, and its schema snapshot can be built.
func TestRegConnectorTypes(t *testing.T) {
	defs := regDefs(t)
	for _, tt := range []struct {
		name    string
		in, out reflect.Type
	}{
		{"cloudflare.connector.run", reflect.TypeFor[ConnectorRunInput](), reflect.TypeFor[Connector]()},
		{"cloudflare.connector.stop", reflect.TypeFor[ConnectorStopInput](), reflect.TypeFor[ConnectorStopped]()},
		{"cloudflare.connector.list", reflect.TypeFor[ConnectorListInput](), reflect.TypeFor[ConnectorList]()},
		{"cloudflare.network.attach", reflect.TypeFor[NetworkAttachInput](), reflect.TypeFor[NetworkAttachment]()},
		{"cloudflare.network.detach", reflect.TypeFor[NetworkDetachInput](), reflect.TypeFor[NetworkAttachment]()},
		{"cloudflare.connector.deploy", reflect.TypeFor[ConnectorDeployInput](), reflect.TypeFor[ConnectorDeployOutput]()},
		{"cloudflare.expose.container", reflect.TypeFor[ExposeInput](), reflect.TypeFor[ExposeOutput]()},
		{"cloudflare.expose.remove", reflect.TypeFor[ExposeRemoveInput](), reflect.TypeFor[ExposeRemoveOutput]()},
	} {
		def, ok := defs[tt.name]
		if !ok {
			t.Errorf("%s is not declared", tt.name)
			continue
		}
		if def.InputType != tt.in || def.OutputType != tt.out {
			t.Errorf("%s: input %v output %v, want %v %v", tt.name, def.InputType, def.OutputType, tt.in, tt.out)
		}
		if _, err := schema.ActionSnapshot(def); err != nil {
			t.Errorf("%s: %v", def.Ref(), err)
		}
	}
}

// regTunnelID is the id of the tunnel row the composite tests seed.
const regTunnelID = "0b6c1f8e-3d52-4a7b-9c14-5e8d2a1f7b30"

// regGeneric decodes the JSON form of v into maps, slices and scalars.
func regGeneric(t *testing.T, v any) any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// regSample is the output of def's output type with every member at its zero
// value and the members of set replaced. A member of set that the type does
// not have fails the test, which is how a reference to a missing output
// member shows.
func regSample(t *testing.T, def core.ActionDef, set map[string]any) map[string]any {
	t.Helper()
	m, ok := regGeneric(t, reflect.New(def.OutputType).Elem().Interface()).(map[string]any)
	if !ok {
		t.Fatalf("the output of %s is not a JSON object", def.Spec.Name)
	}
	for k, v := range set {
		if _, has := m[k]; !has {
			t.Errorf("the output of %s has no member %q", def.Spec.Name, k)
		}
		m[k] = v
	}
	return m
}

// TestRegCompositeSteps: every step a connector or exposure composite returns
// names a registered action, and obeys the rules the engine checks at plan
// time: a controller action takes no host and an agent action the composite's
// host, no step is irreversible or a composite, every compensable step of a
// reverse composite has an undo handler, every step of a forward composite
// that is not a read converges, each step follows only steps before it and
// references only steps it follows, and each step's input, once its
// references are resolved, is the one the Contracts give and is valid for its
// action's schema.
func TestRegCompositeSteps(t *testing.T) {
	ctx := context.Background()
	h := newCFHost(t)
	now := h.clock.Now()
	err := h.db.Write(ctx, func(tx *sql.Tx) error {
		if err := insertTunnel(ctx, tx, tunnelRow{ID: regTunnelID, Name: "edge", ChangeID: "c0", CreatedAt: now}); err != nil {
			return err
		}
		return insertHostname(ctx, tx, hostnameRow{Hostname: "app.example.com", TunnelID: regTunnelID, Service: "http://web:8080",
			ZoneID: "z1", RecordID: "r1", Phase: "active", ChangeID: "c0", UpdatedAt: now})
	})
	if err != nil {
		t.Fatal(err)
	}
	reg, err := core.NewRegistry(Module())
	if err != nil {
		t.Fatal(err)
	}
	validator := schema.NewValidator()
	samples := map[string]map[string]any{
		"cloudflare.tunnel.create": {"id": regTunnelID},
		"cloudflare.tunnel.token":  {"token": "sample-token"},
	}
	id := regTunnelID
	tests := []struct {
		name, action string
		input        any
		forward      bool
		steps        []string
		inputs       map[string]string // the resolved input of each step
	}{
		{"deploy creating a tunnel", "cloudflare.connector.deploy", ConnectorDeployInput{HostID: "h1"}, false,
			[]string{"tunnel", "token", "run"},
			map[string]string{"tunnel": `{}`, "token": `{"tunnel":"` + id + `"}`, "run": `{"tunnel":"` + id + `","token":"sample-token"}`}},
		{"deploy on a named tunnel", "cloudflare.connector.deploy", ConnectorDeployInput{HostID: "h1", Tunnel: "edge"}, false,
			[]string{"token", "run"},
			map[string]string{"token": `{"tunnel":"` + id + `"}`, "run": `{"tunnel":"` + id + `","token":"sample-token"}`}},
		{"expose a container", "cloudflare.expose.container",
			ExposeInput{HostID: "h1", Tunnel: "edge", Container: "web", Port: 8080, Network: "appnet", Hostname: "app.example.com"}, false,
			[]string{"network", "hostname"},
			map[string]string{
				"network":  `{"tunnel":"` + id + `","network":"appnet","claimant":"expose:app.example.com","target":"web"}`,
				"hostname": `{"tunnel":"` + id + `","hostname":"app.example.com","service":"http://web:8080"}`,
			}},
		{"remove an exposure", "cloudflare.expose.remove", ExposeRemoveInput{HostID: "h1", Hostname: "app.example.com"}, true,
			[]string{"hostname", "network"},
			map[string]string{
				"hostname": `{"hostname":"app.example.com"}`,
				"network":  `{"tunnel":"` + id + `","network":"","claimant":"expose:app.example.com"}`,
			}},
	}
	for _, c := range tests {
		t.Run(c.name, func(t *testing.T) {
			def, ok := reg.Action(core.ActionRef{Name: c.action, Version: 1})
			if !ok {
				t.Fatalf("%s is not registered", c.action)
			}
			if (def.Spec.Unwind == core.UnwindForward) != c.forward {
				t.Errorf("%s unwinds %q, want forward %t", c.action, def.Spec.Unwind, c.forward)
			}
			raw, err := json.Marshal(c.input)
			if err != nil {
				t.Fatal(err)
			}
			steps, err := def.Handler.Steps(ctx, h.env(""), raw)
			if err != nil {
				t.Fatal(err)
			}
			ids := map[string]int{}
			follows := map[string]map[string]bool{}
			outputs := map[string]any{}
			var got []string
			for i, s := range steps {
				got = append(got, s.ID)
				if _, dup := ids[s.ID]; dup {
					t.Errorf("step id %s appears twice", s.ID)
				}
				ids[s.ID] = i
				child, ok := reg.Action(s.Action)
				if !ok {
					t.Errorf("step %s: %s is not registered", s.ID, s.Action)
					continue
				}
				outputs[s.ID] = regSample(t, child, samples[s.Action.Name])
				if child.Spec.Side == core.SideController && s.Host != "" || child.Spec.Side == core.SideAgent && s.Host != "h1" {
					t.Errorf("step %s: %s is %s-side but the step names host %q", s.ID, s.Action, child.Spec.Side, s.Host)
				}
				switch child.Spec.Class {
				case core.ClassIrreversible, core.ClassComposite:
					t.Errorf("step %s: %s is %s, which a step never is", s.ID, s.Action, child.Spec.Class)
				case core.ClassCompensable:
					if !c.forward && child.Handler.Inverse == nil {
						t.Errorf("step %s: %s has no undo handler, which a reverse composite needs", s.ID, s.Action)
					}
				}
				if c.forward && child.Spec.Class != core.ClassRead && !child.Spec.Converges {
					t.Errorf("step %s: %s does not converge, which a forward composite needs of every step that is not a read", s.ID, s.Action)
				}
				for _, a := range s.After {
					if before, ok := ids[a]; !ok || before >= i {
						t.Errorf("step %s follows %s, which is not an earlier step", s.ID, a)
					}
				}
				if i > 0 && len(s.After) == 0 {
					t.Errorf("step %s follows no step", s.ID)
				}
				// A step follows the steps of its After list and, as the engine reckons it, every step those follow. Only
				// the outputs of followed steps are offered, so a reference to any other step fails in cdResolve.
				set := map[string]bool{}
				for _, a := range s.After {
					set[a] = true
					for f := range follows[a] {
						set[f] = true
					}
				}
				follows[s.ID] = set
				followed := map[string]any{}
				for a := range set {
					if o, ok := outputs[a]; ok {
						followed[a] = o
					}
				}
				input := cdResolve(t, s.Input, c.input, followed)
				if strings.Contains(string(input), "${") {
					t.Errorf("step %s: input %s holds a reference left unresolved", s.ID, s.Input)
					continue
				}
				if want, ok := c.inputs[s.ID]; !ok {
					t.Errorf("step %s has no expected input in the test table", s.ID)
				} else if !reflect.DeepEqual(regGeneric(t, input), regGeneric(t, json.RawMessage(want))) {
					t.Errorf("step %s: resolved input %s, want %s", s.ID, input, want)
				}
				if err := validator.ValidateInput(child, input); err != nil {
					t.Errorf("step %s: input %s is invalid for %s: %v", s.ID, input, s.Action, err)
				}
			}
			if !slices.Equal(got, c.steps) {
				t.Errorf("steps %v, want %v", got, c.steps)
			}
		})
	}
}
