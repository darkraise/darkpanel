package npm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/modules/firewall"
)

// regAction is one row of the table of the actions spec 6 gives the module.
type regAction struct {
	name      string
	class     core.Class
	side      core.Side
	long      bool          // LongRunning
	apply     time.Duration // the ApplyTimeout of a long-running action
	in, out   reflect.Type  // out nil: not checked; in nil: the input type has no name in the contracts
	available bool          // declares its own availability check
	undoer    bool          // has an Inverse (and so an Observe) handler
	recoverer bool          // has a Recover handler
	resolver  bool          // has a Resolved handler
	converges bool
	internal  bool
	reveal    bool
	unwind    core.Unwind
}

func regType[T any]() reflect.Type { return reflect.TypeFor[T]() }

var regActions = []regAction{
	{name: "npm.instance.deploy", class: core.ClassCompensable, side: core.SideAgent, long: true, apply: 20 * time.Minute, available: true, undoer: true, in: regType[DeployInput](), out: regType[Deployed]()},
	{name: "npm.instance.status", class: core.ClassRead, side: core.SideAgent, in: regType[StatusInput](), out: regType[InstanceStatus]()},
	{name: "npm.instance.confirm-public", class: core.ClassCompensable, side: core.SideAgent, undoer: true, in: regType[ConfirmPublicInput](), out: regType[PublicMark]()},
	{name: "npm.instance.remove", class: core.ClassIrreversible, side: core.SideAgent, in: regType[RemoveInput](), out: regType[Removed]()},
	{name: "npm.instance.upgrade", class: core.ClassCompensable, side: core.SideAgent, long: true, apply: 20 * time.Minute, undoer: true, recoverer: true, in: regType[UpgradeInput](), out: regType[Upgraded]()},
	{name: "npm.network.attach", class: core.ClassCompensable, side: core.SideAgent, undoer: true, in: regType[AttachInput](), out: regType[Attachment]()},
	{name: "npm.network.detach", class: core.ClassCompensable, side: core.SideAgent, undoer: true, converges: true, in: regType[DetachInput]()},
	{name: "npm.proxyhost.add", class: core.ClassCompensable, side: core.SideAgent, undoer: true, resolver: true, in: regType[ProxyHostInput](), out: regType[ProxyHostView]()},
	{name: "npm.proxyhost.update", class: core.ClassCompensable, side: core.SideAgent, undoer: true, in: regType[ProxyHostUpdateInput](), out: regType[ProxyHostView]()},
	{name: "npm.proxyhost.remove", class: core.ClassCompensable, side: core.SideAgent, undoer: true, converges: true, in: regType[ProxyHostRemoveInput](), out: regType[ProxyHostView]()},
	{name: "npm.proxyhost.list", class: core.ClassRead, side: core.SideAgent, in: regType[ProxyHostListInput](), out: regType[ProxyHostList]()},
	{name: "npm.accesslist.add", class: core.ClassCompensable, side: core.SideAgent, undoer: true, resolver: true, in: regType[AccessListInput](), out: regType[AccessListView]()},
	{name: "npm.accesslist.remove", class: core.ClassCompensable, side: core.SideAgent, undoer: true, in: regType[AccessListRemoveInput](), out: regType[AccessListView]()},
	{name: "npm.accesslist.list", class: core.ClassRead, side: core.SideAgent, in: regType[AccessListListInput](), out: regType[AccessListList]()},
	{name: "npm.certificate.request", class: core.ClassCompensable, side: core.SideAgent, long: true, apply: 10 * time.Minute, undoer: true, resolver: true, in: regType[CertificateInput](), out: regType[CertificateView]()},
	{name: "npm.certificate.remove", class: core.ClassIrreversible, side: core.SideAgent, in: regType[CertificateRemoveInput](), out: regType[CertificateView]()},
	{name: "npm.certificate.list", class: core.ClassRead, side: core.SideAgent, in: regType[CertificateListInput](), out: regType[CertificateList]()},
	{name: "npm.admin.reveal-password", class: core.ClassRead, side: core.SideAgent, reveal: true, in: regType[RevealInput](), out: regType[AdminPassword]()},
	{name: "npm.instance.check", class: core.ClassRead, side: core.SideController, internal: true, in: regType[CheckInput](), out: regType[CheckResult]()},
	{name: "npm.public.check", class: core.ClassRead, side: core.SideController, internal: true, in: regType[PublicInput](), out: regType[PublicResult]()},
	{name: "npm.instance.list", class: core.ClassRead, side: core.SideController, out: regType[InstancesOutput]()},
	{name: "npm.instance.deploy-public", class: core.ClassComposite, side: core.SideController, unwind: core.UnwindReverse, in: regType[DeployPublicInput](), out: regType[DeployPublicOutput]()},
	{name: "npm.expose.container", class: core.ClassComposite, side: core.SideController, unwind: core.UnwindReverse, in: regType[ExposeInput](), out: regType[ExposeOutput]()},
	{name: "npm.admin.expose", class: core.ClassComposite, side: core.SideController, unwind: core.UnwindReverse, in: regType[AdminExposeInput](), out: regType[AdminExposeOutput]()},
	{name: "npm.expose.remove", class: core.ClassComposite, side: core.SideController, unwind: core.UnwindForward, in: regType[ExposeRemoveInput](), out: regType[ExposeRemoveOutput]()},
	{name: "npm.admin.unexpose", class: core.ClassComposite, side: core.SideController, unwind: core.UnwindForward, in: regType[AdminUnexposeInput](), out: regType[AdminUnexposeOutput]()},
}

func regModule(t *testing.T) module {
	t.Helper()
	return module{n: newTHHost(t).nm()}
}

func regDefs(t *testing.T, m core.Module) map[string]core.ActionDef {
	t.Helper()
	defs := map[string]core.ActionDef{}
	for _, a := range m.Manifest().Actions {
		if _, dup := defs[a.Spec.Name]; dup {
			t.Errorf("%s is declared twice", a.Spec.Name)
		}
		defs[a.Spec.Name] = a
	}
	return defs
}

// regScope is the scope an action needs: reads need read, a reveal admin, a composite admin and every
// other agent mutation operate.
func regScope(a regAction) core.Scope {
	switch {
	case a.reveal, a.class == core.ClassComposite:
		return core.ScopeAdmin
	case a.class == core.ClassRead:
		return core.ScopeRead
	default:
		return core.ScopeOperate
	}
}

// TestRegManifestDeclaresTheActions: the module declares the 26 actions of the table, all version 1,
// each on its side, the agent mutations under the npm lock, the composites under composite:npm and the reads under
// none, with the class, scope, handlers, availability check, deadline and unwind policy of the table.
func TestRegManifestDeclaresTheActions(t *testing.T) {
	m := regModule(t)
	man := m.Manifest()
	if man.ID != ID || man.Version != 1 || !man.DefaultEnabled {
		t.Errorf("manifest %q v%d default-enabled %v, want npm v1 enabled by default", man.ID, man.Version, man.DefaultEnabled)
	}
	got := regDefs(t, m)
	for _, want := range regActions {
		a, ok := got[want.name]
		if !ok {
			t.Errorf("%s is not declared", want.name)
			continue
		}
		delete(got, want.name)
		s := a.Spec
		wantLocks := []string{"npm"}
		switch want.class {
		case core.ClassRead:
			wantLocks = nil
		case core.ClassComposite:
			wantLocks = []string{"composite:npm"}
		}
		if s.Version != 1 || s.Class != want.class || s.Side != want.side || !slices.Equal(s.LockKeys, wantLocks) ||
			core.RequiredScope(s) != regScope(want) || s.LongRunning != want.long || s.Internal != want.internal || s.Reveal != want.reveal ||
			s.Converges != want.converges || s.Unwind != want.unwind || s.RepairedBy != "" || s.BreakGlass || len(s.Guards) != 0 {
			t.Errorf("%s: spec %+v; want class %s on the %s, locks %v, scope %s, long-running %v, internal %v, reveal %v, converges %v, unwind %q, no guard",
				want.name, s, want.class, want.side, wantLocks, regScope(want), want.long, want.internal, want.reveal, want.converges, want.unwind)
		}
		if want.long && s.ApplyTimeout != want.apply {
			t.Errorf("%s: apply timeout %s, want %s", want.name, s.ApplyTimeout, want.apply)
		}
		if (s.Available != nil) != want.available {
			t.Errorf("%s: availability check %v, want %v", want.name, s.Available != nil, want.available)
		}
		h := a.Handler
		if (h.Inverse != nil) != want.undoer || (want.undoer && h.Observe == nil) || (h.Recover != nil) != want.recoverer || (want.resolver && h.Resolved == nil) {
			t.Errorf("%s: undo handler %v, observer %v, recovery handler %v, resolver %v; want undo %v, recovery %v, resolver %v",
				want.name, h.Inverse != nil, h.Observe != nil, h.Recover != nil, h.Resolved != nil, want.undoer, want.recoverer, want.resolver)
		}
		var served bool
		switch {
		case want.internal || want.reveal:
			served = h.Reveal != nil && h.Read == nil
		case want.class == core.ClassRead:
			served = h.Read != nil && h.Reveal == nil
		case want.class == core.ClassComposite:
			served = h.Steps != nil && h.Output != nil && h.Plan == nil && h.Apply == nil
		default:
			served = h.Plan != nil && h.Verify != nil && (h.Apply != nil || h.Task != nil)
		}
		if !served {
			t.Errorf("%s: handlers do not match its class %s (internal %v, reveal %v)", want.name, want.class, want.internal, want.reveal)
		}
		if want.in != nil && a.InputType != want.in {
			t.Errorf("%s: input type %v, want %v", want.name, a.InputType, want.in)
		}
		if want.out != nil && a.OutputType != want.out {
			t.Errorf("%s: output type %v, want %v", want.name, a.OutputType, want.out)
		}
		if _, err := schema.ActionSnapshot(a); err != nil {
			t.Errorf("%s: %v", a.Ref(), err)
		}
	}
	for name := range got {
		t.Errorf("%s is declared but is not in the action table", name)
	}
}

// TestRegManifestDeclaresTheRest: no resource, no preflight check and no external unit, the one owned directory and
// the agent's migrations.
func TestRegManifestDeclaresTheRest(t *testing.T) {
	man := regModule(t).Manifest()
	if len(man.Resources) != 0 || len(man.Preflight) != 0 || len(man.ExternalUnits) != 0 {
		t.Errorf("manifest declares %d resources, %d preflight checks and %d external units, want none: availability is per action and the fleet read is npm.instance.list",
			len(man.Resources), len(man.Preflight), len(man.ExternalUnits))
	}
	if len(man.OwnedPaths) != 1 || man.OwnedPaths[0].Path != backupDir || man.OwnedPaths[0].Kind != core.OwnedManagedDir {
		t.Errorf("owned paths %+v, want the one managed directory %s", man.OwnedPaths, backupDir)
	}
	migrations, ok := man.Migrations[core.SideAgent]
	if !ok || len(man.Migrations) != 1 {
		t.Fatalf("migrations %v, want the agent side only", man.Migrations)
	}
	found := false
	err := fs.WalkDir(migrations, ".", func(p string, d fs.DirEntry, err error) error {
		found = found || err == nil && !d.IsDir() && filepath.Base(p) == "0001_npm.sql"
		return err
	})
	if err != nil || !found {
		t.Errorf("agent migrations hold no 0001_npm.sql (walk error %v)", err)
	}
}

// regSample is a fully populated input value for the action, so that it is valid whichever of its members the
// schema requires.
var regSamples = map[string]any{
	"npm.instance.deploy":         DeployInput{AcmeEmail: "ops@example.com"},
	"npm.instance.status":         StatusInput{},
	"npm.instance.confirm-public": ConfirmPublicInput{},
	"npm.instance.remove":         RemoveInput{},
	"npm.instance.upgrade":        UpgradeInput{},
	"npm.network.attach":          AttachInput{Network: "appnet", Claimant: "expose:app.example.com", Target: "web"},
	"npm.network.detach":          DetachInput{Network: "appnet", Claimant: "expose:app.example.com"},
	"npm.proxyhost.add":           ProxyHostInput{Domains: []string{"app.example.com"}, ForwardScheme: "http", ForwardHost: "web", ForwardPort: 8080, CertificateID: 3, AccessListID: 2, SSLForced: true, HSTS: true, Websockets: true},
	"npm.proxyhost.update":        ProxyHostUpdateInput{ID: 5, ProxyHostInput: ProxyHostInput{Domains: []string{"app.example.com"}, ForwardScheme: "https", ForwardHost: "web", ForwardPort: 8443}},
	"npm.proxyhost.remove":        ProxyHostRemoveInput{Domain: "app.example.com"},
	"npm.proxyhost.list":          ProxyHostListInput{},
	"npm.accesslist.add":          AccessListInput{SatisfyAny: true, Clients: []AccessClientInput{{Address: "203.0.113.0/24", Directive: "allow"}}},
	"npm.accesslist.remove":       AccessListRemoveInput{ID: 2},
	"npm.accesslist.list":         AccessListListInput{},
	"npm.certificate.request":     CertificateInput{Domains: []string{"app.example.com"}},
	"npm.certificate.remove":      CertificateRemoveInput{ID: 3},
	"npm.certificate.list":        CertificateListInput{},
	"npm.admin.reveal-password":   RevealInput{},
	"npm.instance.check":          CheckInput{HostID: "h1", Integration: true},
	"npm.public.check":            PublicInput{HostID: "h1"},
	"npm.instance.list":           struct{}{},
	"npm.instance.deploy-public":  DeployPublicInput{HostID: "h1", AcmeEmail: "ops@example.com", Integration: true},
	"npm.expose.container":        ExposeInput{HostID: "h1", Container: "web", Port: 8080, Network: "appnet", Domains: []string{"app.example.com"}, Websockets: true, AccessListID: 2},
	"npm.admin.expose":            AdminExposeInput{HostID: "h1", Hostname: "npm.example.com", AccessListID: 2},
	"npm.expose.remove":           ExposeRemoveInput{HostID: "h1", Domain: "app.example.com"},
	"npm.admin.unexpose":          AdminUnexposeInput{HostID: "h1", Hostname: "npm.example.com"},
}

// regMembers marshals a sample into its JSON members.
func regMembers(t *testing.T, v any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// regRefusal requires err to be request.invalid_input naming field.
func regRefusal(err error, field string) bool {
	var coded *errcode.Error
	return errors.As(err, &coded) && coded.Code == errcode.InvalidInput &&
		slices.ContainsFunc(coded.Fields, func(f errcode.FieldError) bool { return f.Path == field })
}

// TestRegInputsAcceptTheirSampleAndRefuseUnknownMembers: each action is registered with its own input type, so its
// schema accepts the input it takes, written with the type's own JSON names, and refuses a member it does not have.
func TestRegInputsAcceptTheirSampleAndRefuseUnknownMembers(t *testing.T) {
	defs := regDefs(t, regModule(t))
	v := schema.NewValidator()
	for _, want := range regActions {
		def, ok := defs[want.name]
		if !ok {
			t.Errorf("%s is not declared", want.name)
			continue
		}
		sample, ok := regSamples[want.name]
		if !ok {
			t.Errorf("%s has no sample input", want.name)
			continue
		}
		members := regMembers(t, sample)
		raw, _ := json.Marshal(members)
		if err := v.ValidateInput(def, raw); err != nil {
			t.Errorf("%s %s: %v, want it accepted", want.name, raw, err)
		}
		members["x"] = 1
		raw, _ = json.Marshal(members)
		if err := v.ValidateInput(def, raw); !regRefusal(err, "/x") {
			t.Errorf("%s %s: %v, want request.invalid_input naming /x", want.name, raw, err)
		}
	}
}

// TestRegInputsRefuseWhatTheWireTypesRequire: the members that spec 6 and the wire types make required or typed.
func TestRegInputsRefuseWhatTheWireTypesRequire(t *testing.T) {
	defs := regDefs(t, regModule(t))
	v := schema.NewValidator()
	for _, tc := range []struct {
		name, action, field string
		mutate              func(map[string]any)
	}{
		{"deploy without the ACME email", "npm.instance.deploy", "/acmeEmail", func(m map[string]any) { delete(m, "acmeEmail") }},
		{"deploy-public without the host", "npm.instance.deploy-public", "/hostId", func(m map[string]any) { delete(m, "hostId") }},
		{"deploy-public with a text integration flag", "npm.instance.deploy-public", "/integration", func(m map[string]any) { m["integration"] = "yes" }},
		{"expose without the host", "npm.expose.container", "/hostId", func(m map[string]any) { delete(m, "hostId") }},
		{"check with an empty host", "npm.instance.check", "/hostId", func(m map[string]any) { m["hostId"] = "" }},
		{"public check with an empty host", "npm.public.check", "/hostId", func(m map[string]any) { m["hostId"] = "" }},
		{"deploy-public with an empty host", "npm.instance.deploy-public", "/hostId", func(m map[string]any) { m["hostId"] = "" }},
		{"expose with an empty host", "npm.expose.container", "/hostId", func(m map[string]any) { m["hostId"] = "" }},
		{"expose remove with an empty host", "npm.expose.remove", "/hostId", func(m map[string]any) { m["hostId"] = "" }},
		{"admin expose with an empty host", "npm.admin.expose", "/hostId", func(m map[string]any) { m["hostId"] = "" }},
		{"admin unexpose with an empty host", "npm.admin.unexpose", "/hostId", func(m map[string]any) { m["hostId"] = "" }},
		{"expose with a text port", "npm.expose.container", "/port", func(m map[string]any) { m["port"] = "8080" }},
		{"proxy host with one domain as text", "npm.proxyhost.add", "/domains", func(m map[string]any) { m["domains"] = "app.example.com" }},
		{"access list removed by a text id", "npm.accesslist.remove", "/id", func(m map[string]any) { m["id"] = "2" }},
		{"attach with a numeric network", "npm.network.attach", "/network", func(m map[string]any) { m["network"] = 1 }},
	} {
		def, ok := defs[tc.action]
		if !ok {
			t.Errorf("%s: %s is not declared", tc.name, tc.action)
			continue
		}
		members := regMembers(t, regSamples[tc.action])
		tc.mutate(members)
		raw, _ := json.Marshal(members)
		if err := v.ValidateInput(def, raw); !regRefusal(err, tc.field) {
			t.Errorf("%s: %s %s: %v, want request.invalid_input naming %s", tc.name, tc.action, raw, err, tc.field)
		}
	}
}

// TestRegOnlyTheRevealedPasswordIsSecret: the admin password is the one secret value the module declares, in the
// output of the reveal action; no input and no other output is marked secret, because the password, an NPM token and
// a private key never enter an action's input or output.
func TestRegOnlyTheRevealedPasswordIsSecret(t *testing.T) {
	defs := regDefs(t, regModule(t))
	for _, want := range regActions {
		def, ok := defs[want.name]
		if !ok {
			t.Errorf("%s is not declared", want.name)
			continue
		}
		in, err := schema.Input(def.InputType)
		if err != nil {
			t.Errorf("%s input: %v", want.name, err)
		}
		out, err := schema.Output(def.OutputType)
		if err != nil {
			t.Errorf("%s output: %v", want.name, err)
		}
		if bytes.Contains(in, []byte("x-secret")) {
			t.Errorf("%s: its input schema marks a field secret", want.name)
		}
		if secret := bytes.Contains(out, []byte("x-secret")); secret != want.reveal {
			t.Errorf("%s: its output schema marks a field secret: %v, want %v", want.name, secret, want.reveal)
		}
	}
}

// TestRegSnapshotsAreCommitted: each action has its committed snapshot, and no npm snapshot exists for anything else
// (modules.TestSnapshotsCurrent checks that each file is current).
func TestRegSnapshotsAreCommitted(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "schemas")
	for _, a := range regActions {
		if _, err := os.Stat(filepath.Join(dir, a.name, "v1.json")); err != nil {
			t.Errorf("schemas/%s/v1.json: %v; run make schemas", a.name, err)
		}
	}
	dirs, err := filepath.Glob(filepath.Join(dir, "npm.*"))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{}
	for _, a := range regActions {
		want[a.name] = true
	}
	for _, d := range dirs {
		if name := filepath.Base(d); !want[name] {
			t.Errorf("schemas/%s belongs to no npm action; run make schemas", name)
		}
	}
}

func TestRegRegistryAcceptsTheModule(t *testing.T) {
	for _, m := range []core.Module{regModule(t), Module()} {
		reg, err := core.NewRegistry(m, firewall.Module())
		if err != nil {
			t.Fatal(err)
		}
		for _, a := range regActions {
			if _, ok := reg.Action(core.ActionRef{Name: a.name, Version: 1}); !ok {
				t.Errorf("the registry has no %s", a.name)
			}
		}
	}
}

var regStepRef = regexp.MustCompile(`\$\{steps\.([a-z][a-z0-9]*)\.output`)

// regOutputs gives each step action whose output a composite's steps reference a sample output of its output type.
var regOutputs = map[string]any{
	"npm.certificate.request": CertificateView{ID: 3, Domains: []string{"app.example.com"}},
	"npm.proxyhost.remove":    ProxyHostView{ID: 5, Domains: []string{"app.example.com"}},
}

// regComposites are the composites over the NPM host h1 with the step ids each returns.
var regComposites = []struct {
	name    string
	action  string
	input   any
	forward bool
	steps   []string
}{
	{"deploy without the integration", "npm.instance.deploy-public", DeployPublicInput{HostID: "h1", AcmeEmail: "ops@example.com"}, false, []string{"check", "deploy"}},
	{"deploy with the integration", "npm.instance.deploy-public", DeployPublicInput{HostID: "h1", AcmeEmail: "ops@example.com", Integration: true}, false, []string{"check", "deploy", "ports"}},
	{"expose a container", "npm.expose.container", ExposeInput{HostID: "h1", Container: "web", Port: 8080, Network: "appnet", Domains: []string{"app.example.com"}, Websockets: true, AccessListID: 2}, false, []string{"public", "network", "cert", "proxy"}},
	{"remove an exposure", "npm.expose.remove", ExposeRemoveInput{HostID: "h1", Domain: "app.example.com"}, true, []string{"proxy", "network"}},
	{"expose the admin UI", "npm.admin.expose", AdminExposeInput{HostID: "h1", Hostname: "npm.example.com", AccessListID: 2}, false, []string{"public", "cert", "proxy"}},
	{"unexpose the admin UI", "npm.admin.unexpose", AdminUnexposeInput{HostID: "h1", Hostname: "npm.example.com"}, true, []string{"proxy"}},
}

// TestRegStepsMatchTheRegistry: every step a composite returns names a registered action, and obeys the rules the
// engine checks at plan time: a controller action takes no host and an agent action the NPM host, no step is
// irreversible or a composite, at most one step is revertible and it is last, every compensable step of a reverse
// composite has an undo handler, every step of a forward composite that is not a read converges, each step follows
// only steps before it and references only steps it follows, and each step's input is valid for its action's schema
// once its references are resolved as the engine resolves them (cxResolve), against the composite's input and sample
// outputs of the steps it follows.
func TestRegStepsMatchTheRegistry(t *testing.T) {
	validator := schema.NewValidator()
	for _, c := range regComposites {
		t.Run(c.name, func(t *testing.T) {
			h := newTHHost(t)
			reg, err := core.NewRegistry(module{n: h.nm()}, firewall.Module())
			if err != nil {
				t.Fatal(err)
			}
			def, ok := reg.Action(core.ActionRef{Name: c.action, Version: 1})
			if !ok {
				t.Fatalf("%s is not registered", c.action)
			}
			if (def.Spec.Unwind == core.UnwindForward) != c.forward {
				t.Errorf("%s unwinds %q, want forward %v", c.action, def.Spec.Unwind, c.forward)
			}
			raw, _ := json.Marshal(c.input)
			steps, err := def.Handler.Steps(context.Background(), h.ctlEnv(""), raw)
			if err != nil {
				t.Fatal(err)
			}
			ids := map[string]int{}
			outputs := map[string]any{}
			revertible := -1
			var got []string
			for i, s := range steps {
				got = append(got, s.ID)
				if _, dup := ids[s.ID]; dup {
					t.Errorf("step id %s appears twice", s.ID)
				}
				ids[s.ID] = i
				if o, ok := regOutputs[s.Action.Name]; ok {
					outputs[s.ID] = o
				}
				child, ok := reg.Action(s.Action)
				if !ok {
					t.Errorf("step %s: %s is not registered", s.ID, s.Action)
					continue
				}
				if child.Spec.Side == core.SideController && s.Host != "" || child.Spec.Side == core.SideAgent && s.Host != "h1" {
					t.Errorf("step %s: %s is %s-side but the step names host %q", s.ID, s.Action, child.Spec.Side, s.Host)
				}
				switch child.Spec.Class {
				case core.ClassIrreversible, core.ClassComposite:
					t.Errorf("step %s: %s is %s, which a step never is", s.ID, s.Action, child.Spec.Class)
				case core.ClassRevertible:
					if revertible >= 0 {
						t.Errorf("step %s: a second revertible step", s.ID)
					}
					revertible = i
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
				for _, m := range regStepRef.FindAllStringSubmatch(string(s.Input), -1) {
					if !slices.Contains(s.After, m[1]) {
						t.Errorf("step %s references step %s but does not follow it", s.ID, m[1])
					}
				}
				// Only the outputs of followed steps are offered, so a reference to any other step fails in cxResolve.
				followed := map[string]any{}
				for _, a := range s.After {
					if o, ok := outputs[a]; ok {
						followed[a] = o
					}
				}
				input := cxResolve(t, s.Input, c.input, followed)
				if strings.Contains(string(input), "${") {
					t.Errorf("step %s: input %s holds a reference left unresolved", s.ID, s.Input)
					continue
				}
				if err := validator.ValidateInput(child, input); err != nil {
					t.Errorf("step %s: input %s is invalid for %s: %v", s.ID, input, s.Action, err)
				}
			}
			if revertible >= 0 && revertible != len(steps)-1 {
				t.Errorf("the revertible step %s is not last", steps[revertible].ID)
			}
			if !slices.Equal(got, c.steps) {
				t.Errorf("steps %v, want %v", got, c.steps)
			}
		})
	}
}

// TestRegDeployPublicPortsStep: the ports step of deploy-public allows both published ports of darkpanel-npm from
// every source, in one firewall.containers.apply.
func TestRegDeployPublicPortsStep(t *testing.T) {
	h := newTHHost(t)
	def, ok := regDefs(t, module{n: h.nm()})["npm.instance.deploy-public"]
	if !ok {
		t.Fatal("npm.instance.deploy-public is not declared")
	}
	raw, _ := json.Marshal(DeployPublicInput{HostID: "h1", AcmeEmail: "ops@example.com", Integration: true})
	steps, err := def.Handler.Steps(context.Background(), h.ctlEnv(""), raw)
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(steps, func(s core.Step) bool { return s.ID == "ports" })
	if i < 0 {
		t.Fatalf("steps %+v have no ports step", steps)
	}
	if s := steps[i]; s.Action.Name != "firewall.containers.apply" || !slices.Equal(s.After, []string{"deploy"}) {
		t.Errorf("ports step is %s after %v, want firewall.containers.apply after [deploy]", s.Action.Name, s.After)
	}
	var in firewall.ContainersApplyInput
	if err := json.Unmarshal(steps[i].Input, &in); err != nil {
		t.Fatal(err)
	}
	var ports []uint16
	for _, op := range in.Ops {
		if op.Op != "allow" || op.Binding == nil || op.Binding.Selector != "darkpanel-npm" || op.Binding.Proto != "tcp" || op.From.IsValid() {
			t.Errorf("op %+v, want an allow of a tcp binding of darkpanel-npm from every source", op)
			continue
		}
		ports = append(ports, op.Binding.HostPort)
	}
	slices.Sort(ports)
	if !slices.Equal(ports, []uint16{80, 443}) {
		t.Errorf("ports %v, want 80 and 443", ports)
	}
}
