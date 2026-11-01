package tailscale

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"slices"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/schema"
)

type regAction struct {
	name       string
	class      core.Class
	task       bool
	apply      time.Duration // 0 keeps the 30 s default
	available  bool          // declares its own availability check
	guarded    bool          // declares the tailscale.link guard
	undoer     bool
	recoverer  bool
	converges  bool
	repairedBy string
}

var regActions = []regAction{
	{name: "tailscale.prefs.set", class: core.ClassRevertible, available: true, guarded: true, repairedBy: "tailscale.prefs.repair"},
	{name: "tailscale.prefs.repair", class: core.ClassRevertible, available: true, guarded: true, repairedBy: "tailscale.prefs.repair"},
	{name: "tailscale.node.login", class: core.ClassCompensable, available: true, guarded: true, undoer: true},
	{name: "tailscale.node.logout", class: core.ClassIrreversible, available: true, guarded: true},
	{name: "tailscale.package.install", class: core.ClassCompensable, task: true, apply: 15 * time.Minute, undoer: true},
	{name: "tailscale.package.update", class: core.ClassIrreversible, task: true, apply: 15 * time.Minute, available: true},
	{name: "tailscale.package.pin", class: core.ClassCompensable, task: true, apply: 20 * time.Minute, available: true, undoer: true, recoverer: true},
	{name: "tailscale.package.unpin", class: core.ClassCompensable, task: true, apply: 5 * time.Minute, available: true, undoer: true, converges: true},
	{name: "tailscale.cert.get", class: core.ClassRead},
}

func regModule(t *testing.T) module {
	t.Helper()
	return module{t: newTSHost(t).ts()}
}

// TestRegManifestDeclaresTheActions: the module declares section 5's tailscale
// rows, all version 1 and agent-side, each mutation under the tailscale lock
// and the read under none, with the class, handlers, guard and deadlines of
// the action table.
func TestRegManifestDeclaresTheActions(t *testing.T) {
	man := regModule(t).Manifest()
	if man.ID != ID || man.Version != 1 || !man.DefaultEnabled {
		t.Errorf("manifest %q v%d default-enabled %v, want tailscale v1 enabled by default", man.ID, man.Version, man.DefaultEnabled)
	}
	got := map[string]core.ActionDef{}
	for _, a := range man.Actions {
		if _, dup := got[a.Spec.Name]; dup {
			t.Errorf("%s is declared twice", a.Spec.Name)
		}
		got[a.Spec.Name] = a
	}
	for _, want := range regActions {
		a, ok := got[want.name]
		if !ok {
			t.Errorf("%s is not declared", want.name)
			continue
		}
		delete(got, want.name)
		s := a.Spec
		wantTimeout := want.apply
		if wantTimeout == 0 {
			wantTimeout = 30 * time.Second
		}
		timeout := s.ApplyTimeout
		if timeout == 0 {
			timeout = 30 * time.Second
		}
		wantLocks := []string{"tailscale"}
		if want.class == core.ClassRead {
			wantLocks = nil
		}
		internal := want.name == "tailscale.cert.get"
		if s.Version != 1 || s.Class != want.class || s.Side != core.SideAgent || !slices.Equal(s.LockKeys, wantLocks) ||
			s.MinScope != "" || s.LongRunning != want.task || s.Task != want.task || timeout != wantTimeout ||
			s.Internal != internal || s.Reveal || s.Converges != want.converges || s.RepairedBy != want.repairedBy ||
			s.CompensatedBy != "" || s.ForwardOnly {
			t.Errorf("%s: spec %+v; want class %s on the agent, locks %v, task %v, timeout %s, internal %v, converges %v, repaired by %q",
				want.name, s, want.class, wantLocks, want.task, wantTimeout, internal, want.converges, want.repairedBy)
		}
		// cert.get answers tailscale.unavailable from its handler, so the table does not pin its check.
		if want.name != "tailscale.cert.get" && (s.Available != nil) != want.available {
			t.Errorf("%s: availability check %v, want %v", want.name, s.Available != nil, want.available)
		}
		wantGuards := []core.GuardSpec(nil)
		if want.guarded {
			wantGuards = []core.GuardSpec{{ID: "tailscale.link", Overridable: true}}
		}
		if !slices.Equal(s.Guards, wantGuards) {
			t.Errorf("%s: guards %v, want %v", want.name, s.Guards, wantGuards)
		}
		if (a.Handler.Inverse != nil) != want.undoer || (want.undoer && a.Handler.Observe == nil) {
			t.Errorf("%s: undo handler %v, observer %v, want an undo handler %v with an observer", want.name, a.Handler.Inverse != nil, a.Handler.Observe != nil, want.undoer)
		}
		if (a.Handler.Recover != nil) != want.recoverer {
			t.Errorf("%s: recovery handler %v, want %v", want.name, a.Handler.Recover != nil, want.recoverer)
		}
		if internal != (a.Handler.Reveal != nil) {
			t.Errorf("%s: reveal handler %v, want %v", want.name, a.Handler.Reveal != nil, internal)
		}
		if _, err := schema.ActionSnapshot(a); err != nil {
			t.Errorf("%s: %v", a.Ref(), err)
		}
	}
	for name := range got {
		t.Errorf("%s is declared but is not in the action table", name)
	}
}

// TestRegManifestDeclaresTheRest: the resources, the OS preflight, the owned
// paths, the units the module drives and the agent migrations.
func TestRegManifestDeclaresTheRest(t *testing.T) {
	man := regModule(t).Manifest()

	var resources []string
	for _, r := range man.Resources {
		resources = append(resources, r.Name)
		if _, err := schema.ResourceSnapshot(r); err != nil {
			t.Errorf("%s: %v", r.Name, err)
		}
	}
	slices.Sort(resources)
	if want := []string{"tailscale.latency", "tailscale.status"}; !slices.Equal(resources, want) {
		t.Errorf("resources %v, want %v", resources, want)
	}

	if len(man.Preflight) == 0 {
		t.Error("the module declares no preflight check, want the OS check")
	}

	owned := map[string]core.OwnedPath{}
	for _, p := range man.OwnedPaths {
		owned[p.Path] = p
	}
	wantOwned := []core.OwnedPath{
		{Path: "/usr/share/keyrings/tailscale-archive-keyring.gpg", Kind: core.OwnedFile},
		{Path: "/etc/apt/sources.list.d/tailscale.list", Kind: core.OwnedFile},
		{Path: "/var/lib/darkpanel/agent/tailscale-debs", Kind: core.OwnedManagedDir, Pattern: `^tailscale_[0-9][0-9A-Za-z.+~-]{0,63}_(amd64|arm64)\.deb$`},
	}
	if len(man.OwnedPaths) != len(wantOwned) {
		t.Errorf("owned paths %+v, want %d", man.OwnedPaths, len(wantOwned))
	}
	for _, w := range wantOwned {
		if g := owned[w.Path]; g.Kind != w.Kind || g.Pattern != w.Pattern {
			t.Errorf("owned path %s = %+v, want kind %s with pattern %q", w.Path, g, w.Kind, w.Pattern)
		}
	}

	units := map[string][]string{}
	for _, u := range man.ExternalUnits {
		units[u.Name] = slices.Sorted(slices.Values(u.Ops))
	}
	wantUnits := map[string][]string{
		"tailscaled.service":           {core.UnitOpRestart, core.UnitOpState},
		"darkpanel-controller.service": {core.UnitOpState},
	}
	if len(units) != len(wantUnits) {
		t.Errorf("external units %v, want %v", units, wantUnits)
	}
	for name, ops := range wantUnits {
		if !slices.Equal(units[name], ops) {
			t.Errorf("external unit %s operations %v, want %v", name, units[name], ops)
		}
	}

	migrations, ok := man.Migrations[core.SideAgent]
	if !ok || len(man.Migrations) != 1 {
		t.Fatalf("migrations %v, want the agent side only", man.Migrations)
	}
	if _, err := fs.Stat(migrations, "0001_tailscale.sql"); err != nil {
		t.Errorf("agent migrations: %v", err)
	}
}

// TestRegInputs: each action is registered with its own input type, so its
// schema accepts the input the action takes and names the field of the one
// it refuses.
func TestRegInputs(t *testing.T) {
	defs := map[string]core.ActionDef{}
	for _, a := range regModule(t).Manifest().Actions {
		defs[a.Spec.Name] = a
	}
	v := schema.NewValidator()
	tests := []struct {
		action, input string
		field         string // "" when the input is accepted
	}{
		{"tailscale.prefs.set", `{"wantRunning":true}`, ""},
		{"tailscale.prefs.set", `{"acceptRoutes":false,"shieldsUp":true,"runSSH":false,"exitNode":"","advertiseExitNode":true}`, ""},
		{"tailscale.prefs.set", `{"advertiseRoutes":["10.0.0.0/24"],"overrides":["tailscale.link"]}`, ""},
		{"tailscale.prefs.set", `{"wantRunning":"yes"}`, "/wantRunning"},
		{"tailscale.prefs.set", `{"advertiseRoutes":"10.0.0.0/24"}`, "/advertiseRoutes"},
		{"tailscale.prefs.set", `{"unknown":1}`, "/unknown"},
		{"tailscale.prefs.repair", `{}`, ""},
		{"tailscale.prefs.repair", `{"outcome":"keep"}`, ""},
		{"tailscale.prefs.repair", `{"outcome":5}`, "/outcome"},
		{"tailscale.node.login", `{"authKey":"tskey-auth-x"}`, ""},
		{"tailscale.node.login", `{"authKey":"k","hostname":"h","advertiseTags":["tag:a"],"relogin":true}`, ""},
		{"tailscale.node.login", `{}`, "/authKey"},
		{"tailscale.node.login", `{"authKey":"k","advertiseTags":"tag:a"}`, "/advertiseTags"},
		{"tailscale.node.logout", `{}`, ""},
		{"tailscale.node.logout", `{"x":1}`, "/x"},
		{"tailscale.package.install", `{}`, ""},
		{"tailscale.package.install", `{"x":1}`, "/x"},
		{"tailscale.package.update", `{}`, ""},
		{"tailscale.package.update", `{"x":1}`, "/x"},
		{"tailscale.package.pin", `{"version":"1.104.1"}`, ""},
		{"tailscale.package.pin", `{}`, "/version"},
		{"tailscale.package.pin", `{"version":1}`, "/version"},
		{"tailscale.package.unpin", `{}`, ""},
		{"tailscale.package.unpin", `{"x":1}`, "/x"},
		{"tailscale.cert.get", `{}`, ""},
		{"tailscale.cert.get", `{"x":1}`, "/x"},
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

// TestRegSecretsAreRedacted: the auth key of node.login and the private key of
// cert.get carry the secret tag, so no log, diff or stored output shows them.
func TestRegSecretsAreRedacted(t *testing.T) {
	defs := map[string]core.ActionDef{}
	for _, a := range regModule(t).Manifest().Actions {
		defs[a.Spec.Name] = a
	}
	login, ok := defs["tailscale.node.login"]
	if !ok {
		t.Fatal("tailscale.node.login is not declared")
	}
	cert, ok := defs["tailscale.cert.get"]
	if !ok {
		t.Fatal("tailscale.cert.get is not declared")
	}
	in := schema.Redact(login.InputType, json.RawMessage(`{"authKey":"tskey-auth-SECRET","hostname":"web1"}`))
	if bytes.Contains(in, []byte("SECRET")) || !bytes.Contains(in, []byte("web1")) {
		t.Errorf("redacted login input %s, want the auth key hidden and the hostname kept", in)
	}
	out := schema.Redact(cert.OutputType, json.RawMessage(`{"name":"web1.example.ts.net","certPem":"CERT","keyPem":"PRIVATE","notAfter":"2026-12-01T00:00:00Z"}`))
	if bytes.Contains(out, []byte("PRIVATE")) || !bytes.Contains(out, []byte("CERT")) {
		t.Errorf("redacted certificate output %s, want the private key hidden and the certificate kept", out)
	}
}

func TestRegRegistryAcceptsTheModule(t *testing.T) {
	for _, m := range []core.Module{regModule(t), Module()} {
		reg, err := core.NewRegistry(m)
		if err != nil {
			t.Fatal(err)
		}
		for _, a := range regActions {
			if _, ok := reg.Action(core.ActionRef{Name: a.name, Version: 1}); !ok {
				t.Errorf("the registry has no %s", a.name)
			}
		}
		for _, r := range []string{"tailscale.status", "tailscale.latency"} {
			if _, ok := reg.Resource(r); !ok {
				t.Errorf("the registry has no %s resource", r)
			}
		}
	}
}
