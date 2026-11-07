package derp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
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
	"github.com/darkraise/darkpanel/internal/modules/firewall"
	"github.com/darkraise/darkpanel/internal/modules/tailnet"
	"github.com/darkraise/darkpanel/internal/modules/tailscale"
)

type regAction struct {
	name      string
	class     core.Class
	side      core.Side
	task      bool          // declared with DefineTask
	apply     time.Duration // 0 keeps the 30 s default
	available bool          // declares its own availability check
	observer  bool          // has an Observe handler
	undoer    bool          // has an Inverse handler
	recoverer bool          // has a Recover handler
	converges bool
	internal  bool
	unwind    core.Unwind
	comp      string // CompensatedBy
}

var regActions = []regAction{
	{name: "derp.server.install", class: core.ClassCompensable, side: core.SideAgent, task: true, apply: 15 * time.Minute, available: true, observer: true, undoer: true},
	{name: "derp.binary.update", class: core.ClassCompensable, side: core.SideAgent, task: true, apply: 15 * time.Minute, available: true, observer: true, undoer: true, recoverer: true},
	{name: "derp.server.status", class: core.ClassRead, side: core.SideAgent},
	{name: "derp.node.probe", class: core.ClassRead, side: core.SideAgent},
	{name: "derp.server.uninstall", class: core.ClassCompensable, side: core.SideAgent, apply: 45 * time.Second, observer: true, converges: true, comp: "derp.server.install"},
	{name: "derp.cert.renew", class: core.ClassCompensable, side: core.SideAgent, apply: 45 * time.Second, available: true, observer: true, undoer: true},
	{name: "derp.server.probe", class: core.ClassRead, side: core.SideController},
	{name: "derp.server.list", class: core.ClassRead, side: core.SideController},
	{name: "derp.region.edits", class: core.ClassRead, side: core.SideController, internal: true},
	{name: "derp.fallback.check", class: core.ClassRead, side: core.SideController, internal: true},
	{name: "derp.server.deploy", class: core.ClassComposite, side: core.SideController, unwind: core.UnwindReverse},
	{name: "derp.server.upgrade", class: core.ClassComposite, side: core.SideController, unwind: core.UnwindReverse},
	{name: "derp.cert.rotate", class: core.ClassComposite, side: core.SideController, unwind: core.UnwindReverse},
	{name: "derp.region.set", class: core.ClassComposite, side: core.SideController, unwind: core.UnwindReverse},
	{name: "derp.region.remove", class: core.ClassComposite, side: core.SideController, unwind: core.UnwindReverse},
	{name: "derp.server.remove", class: core.ClassComposite, side: core.SideController, unwind: core.UnwindForward},
}

func regModule(t *testing.T) module {
	t.Helper()
	return module{d: newDPHost(t).dp()}
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

// TestRegManifestDeclaresTheActions: the module declares section 5's derp rows and the read actions the plan adds,
// all version 1, each on its side, the agent mutations under the derp lock, the composites under composite:derp and
// the reads under none, with the class, handlers, availability check, deadline and unwind policy of the action table.
func TestRegManifestDeclaresTheActions(t *testing.T) {
	m := regModule(t)
	man := m.Manifest()
	if man.ID != ID || man.Version != 1 || !man.DefaultEnabled {
		t.Errorf("manifest %q v%d default-enabled %v, want derp v1 enabled by default", man.ID, man.Version, man.DefaultEnabled)
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
		wantTimeout := want.apply
		if wantTimeout == 0 {
			wantTimeout = 30 * time.Second
		}
		timeout := s.ApplyTimeout
		if timeout == 0 {
			timeout = 30 * time.Second
		}
		wantLocks := []string{"derp"}
		switch want.class {
		case core.ClassRead:
			wantLocks = nil
		case core.ClassComposite:
			wantLocks = []string{"composite:derp"}
		}
		wantMin := core.Scope("")
		if want.name == "derp.node.probe" {
			wantMin = core.ScopeOperate
		}
		if s.Version != 1 || s.Class != want.class || s.Side != want.side || !slices.Equal(s.LockKeys, wantLocks) ||
			s.LongRunning != want.task || s.Task != want.task || timeout != wantTimeout || s.MinScope != wantMin ||
			s.Internal != want.internal || s.Reveal || s.Converges != want.converges || s.RepairedBy != "" ||
			s.CompensatedBy != want.comp || s.ForwardOnly || s.BreakGlass || s.Unwind != want.unwind || len(s.Guards) != 0 {
			t.Errorf("%s: spec %+v; want class %s on the %s, locks %v, task %v, timeout %s, internal %v, converges %v, compensated by %q, unwind %q, no guard",
				want.name, s, want.class, want.side, wantLocks, want.task, wantTimeout, want.internal, want.converges, want.comp, want.unwind)
		}
		if (s.Available != nil) != want.available {
			t.Errorf("%s: availability check %v, want %v", want.name, s.Available != nil, want.available)
		}
		if (a.Handler.Observe != nil) != want.observer || (a.Handler.Inverse != nil) != want.undoer || (a.Handler.Recover != nil) != want.recoverer {
			t.Errorf("%s: observer %v, undo handler %v, recovery handler %v, want %v, %v, %v",
				want.name, a.Handler.Observe != nil, a.Handler.Inverse != nil, a.Handler.Recover != nil, want.observer, want.undoer, want.recoverer)
		}
		var served bool
		switch {
		case want.internal:
			served = a.Handler.Reveal != nil && a.Handler.Read == nil
		case want.class == core.ClassRead:
			served = a.Handler.Read != nil && a.Handler.Reveal == nil
		case want.class == core.ClassComposite:
			served = a.Handler.Steps != nil && a.Handler.Output != nil && a.Handler.Plan == nil
		case want.task:
			served = a.Handler.Plan != nil && a.Handler.Task != nil && a.Handler.Verify != nil && a.Handler.Apply == nil
		default:
			served = a.Handler.Plan != nil && a.Handler.Apply != nil && a.Handler.Verify != nil
		}
		if !served {
			t.Errorf("%s: handlers do not match its class %s (internal %v, task %v)", want.name, want.class, want.internal, want.task)
		}
		if _, err := schema.ActionSnapshot(a); err != nil {
			t.Errorf("%s: %v", a.Ref(), err)
		}
	}
	for name := range got {
		t.Errorf("%s is declared but is not in the action table", name)
	}
}

// TestRegManifestDeclaresTheRest: no resource, no preflight check and no external unit, the paths the module owns,
// and both sides' migrations.
func TestRegManifestDeclaresTheRest(t *testing.T) {
	man := regModule(t).Manifest()

	if len(man.Resources) != 0 || len(man.Preflight) != 0 || len(man.ExternalUnits) != 0 {
		t.Errorf("manifest declares %d resources, %d preflight checks and %d external units, want none: the controller's list is the read derp.server.list and the module drives only its own unit",
			len(man.Resources), len(man.Preflight), len(man.ExternalUnits))
	}

	owned := map[string]core.OwnedPath{}
	for _, p := range man.OwnedPaths {
		owned[p.Path] = p
	}
	wantOwned := []core.OwnedPath{
		{Path: "darkpanel-derp", Kind: core.OwnedUser},
		{Path: "/usr/local/lib/darkpanel/derper", Kind: core.OwnedFile},
		{Path: "/usr/local/lib/darkpanel/derper.new", Kind: core.OwnedFile},
		{Path: "/usr/local/lib/darkpanel/derper.prev", Kind: core.OwnedFile},
		{Path: "/etc/systemd/system/darkpanel-derper.service", Kind: core.OwnedUnit},
		{Path: "/var/lib/darkpanel/derp", Kind: core.OwnedServiceDir, User: "darkpanel-derp", Subdirs: []string{"certs"}},
	}
	if len(man.OwnedPaths) != len(wantOwned) {
		t.Errorf("owned paths %+v, want %d", man.OwnedPaths, len(wantOwned))
	}
	for _, w := range wantOwned {
		g := owned[w.Path]
		if g.Kind != w.Kind || g.Pattern != w.Pattern || g.User != w.User || !slices.Equal(g.Subdirs, w.Subdirs) {
			t.Errorf("owned path %s = %+v, want %+v", w.Path, g, w)
		}
	}

	if len(man.Migrations) != 2 {
		t.Fatalf("migrations %v, want the agent's and the controller's", man.Migrations)
	}
	for _, side := range []core.Side{core.SideAgent, core.SideController} {
		migrations, ok := man.Migrations[side]
		if !ok {
			t.Errorf("no %s migrations", side)
			continue
		}
		if _, err := fs.Stat(migrations, "0001_derp.sql"); err != nil {
			t.Errorf("%s migrations: %v", side, err)
		}
	}
}

// TestRegInputs: each action is registered with its own input type, so its schema accepts the input the action
// takes and names the field of the one it refuses.
func TestRegInputs(t *testing.T) {
	defs := regDefs(t, regModule(t))
	v := schema.NewValidator()
	whois := func(n int) string {
		return `{"whois":[` + strings.TrimSuffix(strings.Repeat(`"100.64.0.1",`, n), ",") + `]}`
	}
	agents := func(n int) string {
		return `{"hostId":"h1","agents":[` + strings.TrimSuffix(strings.Repeat(`"h2",`, n), ",") + `]}`
	}
	tests := []struct {
		action, input string
		field         string // "" when the input is accepted
	}{
		{"derp.server.install", `{"mode":"letsencrypt","hostName":"derp.example.com"}`, ""},
		{"derp.server.install", `{"mode":"ip","hostName":"203.0.113.10","verifyClients":false}`, ""},
		{"derp.server.install", `{"mode":"other","hostName":"derp.example.com"}`, "/mode"},
		{"derp.server.install", `{"mode":"ip"}`, "/hostName"},
		{"derp.server.install", `{"mode":"ip","hostName":"` + strings.Repeat("a", 254) + `"}`, "/hostName"},
		{"derp.server.install", `{"mode":"ip","hostName":"203.0.113.10","verifyClients":"no"}`, "/verifyClients"},
		{"derp.server.uninstall", `{}`, ""},
		{"derp.server.uninstall", `{"x":1}`, "/x"},
		{"derp.binary.update", `{"version":"v1.104.1"}`, ""},
		{"derp.binary.update", `{"version":"1.104.1"}`, "/version"},
		{"derp.binary.update", `{}`, "/version"},
		{"derp.cert.renew", `{}`, ""},
		{"derp.cert.renew", `{"x":1}`, "/x"},
		{"derp.server.status", `{}`, ""},
		{"derp.server.status", whois(256), ""},
		{"derp.server.status", whois(257), "/whois"},
		{"derp.server.status", `{"whois":"100.64.0.1"}`, "/whois"},
		{"derp.node.probe", `{"hostName":"203.0.113.10"}`, ""},
		{"derp.node.probe", `{"hostName":"203.0.113.10","address":"203.0.113.10","certName":"sha256-raw:00","regionId":900}`, ""},
		{"derp.node.probe", `{}`, "/hostName"},
		{"derp.node.probe", `{"hostName":"203.0.113.10","regionId":"900"}`, "/regionId"},
		{"derp.server.probe", `{"hostId":"h1"}`, ""},
		{"derp.server.probe", agents(8), ""},
		{"derp.server.probe", agents(9), "/agents"},
		{"derp.server.probe", `{}`, "/hostId"},
		{"derp.server.list", `{}`, ""},
		{"derp.server.list", `{"x":1}`, "/x"},
		{"derp.region.edits", `{"op":"set","hostId":"h1","regionId":900,"regionCode":"nyc","regionName":"New York","omitDefaultRegions":true,"acknowledge":"nodes will relay only through nyc"}`, ""},
		{"derp.region.edits", `{"op":"remove","regionId":900}`, ""},
		{"derp.region.edits", `{"op":"explode","regionId":900}`, "/op"},
		{"derp.region.edits", `{"op":"remove","regionId":899}`, "/regionId"},
		{"derp.region.edits", `{"op":"set","hostId":"h1","regionId":900,"regionCode":"NYC"}`, "/regionCode"},
		{"derp.fallback.check", `{"hostId":"h1","regionId":900}`, ""},
		{"derp.fallback.check", `{"hostId":"h1","regionId":1000}`, "/regionId"},
		{"derp.fallback.check", `{"hostId":"h1"}`, "/regionId"},
		{"derp.server.deploy", `{"hostId":"h1","mode":"ip","hostName":"203.0.113.10"}`, ""},
		{"derp.server.deploy", `{"hostId":"h1","mode":"letsencrypt","hostName":"derp.example.com","verifyClients":false}`, ""},
		{"derp.server.deploy", `{"mode":"ip","hostName":"203.0.113.10"}`, "/hostId"},
		{"derp.server.deploy", `{"hostId":"h1","mode":"ip"}`, "/hostName"},
		{"derp.server.upgrade", `{"hostId":"h1"}`, ""},
		{"derp.server.upgrade", `{"hostId":""}`, "/hostId"},
		{"derp.cert.rotate", `{"hostId":"h1"}`, ""},
		{"derp.cert.rotate", `{}`, "/hostId"},
		{"derp.region.set", `{"hostId":"h1","regionId":900,"regionCode":"nyc","regionName":"New York"}`, ""},
		{"derp.region.set", `{"hostId":"h1","regionId":900,"regionCode":"nyc","regionName":"New York","omitDefaultRegions":true,"acknowledge":"nodes will relay only through nyc"}`, ""},
		{"derp.region.set", `{"hostId":"h1","regionId":1000,"regionCode":"nyc","regionName":"New York"}`, "/regionId"},
		{"derp.region.set", `{"hostId":"h1","regionId":900,"regionName":"New York"}`, "/regionCode"},
		{"derp.region.remove", `{"regionId":900}`, ""},
		{"derp.region.remove", `{"regionId":10}`, "/regionId"},
		{"derp.server.remove", `{"hostId":"h1"}`, ""},
		{"derp.server.remove", `{}`, "/hostId"},
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
				t.Errorf("%s %.80s: %v, want it accepted", tt.action, tt.input, err)
			}
			continue
		}
		var coded *errcode.Error
		if !errors.As(err, &coded) || coded.Code != errcode.InvalidInput ||
			!slices.ContainsFunc(coded.Fields, func(f errcode.FieldError) bool { return f.Path == tt.field }) {
			t.Errorf("%s %.80s: %v, want %s naming %s", tt.action, tt.input, err, errcode.InvalidInput, tt.field)
		}
	}
}

// TestRegSchemasHoldNoSecret: no input or output of a derp action is marked secret, because the module handles none
// (derper's node key and a certificate's private key never enter a detail, output or log).
func TestRegSchemasHoldNoSecret(t *testing.T) {
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
		if bytes.Contains(in, []byte("x-secret")) || bytes.Contains(out, []byte("x-secret")) {
			t.Errorf("%s: a schema marks a field secret, which no derp type does", want.name)
		}
	}
}

// TestRegSnapshotsAreCommitted: each action has its committed snapshot, and no snapshot exists for a resource the
// module does not declare (modules.TestSnapshotsCurrent checks that each file is current).
func TestRegSnapshotsAreCommitted(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "schemas")
	for _, a := range regActions {
		if _, err := os.Stat(filepath.Join(dir, a.name, "v1.json")); err != nil {
			t.Errorf("schemas/%s/v1.json: %v; run make schemas", a.name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "derp.servers")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("schemas/derp.servers: %v, want it absent: derp.servers is a service and declares no resource", err)
	}
}

func TestRegRegistryAcceptsTheModule(t *testing.T) {
	for _, m := range []core.Module{regModule(t), Module()} {
		reg, err := core.NewRegistry(m, tailscale.Module(), tailnet.Module(), firewall.Module())
		if err != nil {
			t.Fatal(err)
		}
		for _, a := range regActions {
			if _, ok := reg.Action(core.ActionRef{Name: a.name, Version: 1}); !ok {
				t.Errorf("the registry has no %s", a.name)
			}
		}
		if _, ok := reg.Resource("derp.servers"); ok {
			t.Error("the registry has a derp.servers resource, want none")
		}
	}
}

// TestRegServersService: the module, built for production and built over the test host, is an ext.Controller that
// contributes no route and the one service derp.servers; the service runs a pass, arms its next one and stops when
// its context ends.
func TestRegServersService(t *testing.T) {
	h := newDPHost(t)
	deps := ext.Deps{
		DB:     h.ctlDB,
		Sealer: coretest.Sealer{},
		Clock:  h.clock,
		Log:    slog.New(slog.DiscardHandler),
		Hosts:  h.ctlEnv("").Agents,
		Audit:  &coretest.MemAudit{},
	}
	var services []ext.Service
	for _, m := range []core.Module{Module(), module{d: h.dp()}} {
		c, ok := m.(ext.Controller)
		if !ok {
			t.Fatalf("%T does not implement ext.Controller", m)
		}
		var routes []ext.Route
		var err error
		routes, services, err = c.Controller(deps)
		if err != nil {
			t.Fatal(err)
		}
		if len(routes) != 0 || len(services) != 1 || services[0].Name != "derp.servers" || services[0].Run == nil {
			t.Fatalf("%T contributes %d routes and services %+v, want no route and the service derp.servers with a Run", m, len(routes), services)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- services[0].Run(ctx) }()
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

var (
	regStepRef  = regexp.MustCompile(`\$\{steps\.([a-z][a-z0-9]*)\.output`)
	regInputRef = regexp.MustCompile(`"\$\{input\.([A-Za-z][A-Za-z0-9]*)\}"`)
	regCertName = "sha256-raw:" + strings.Repeat("ab", 32)
	// regSamples fills the whole-string step references of the composites' step inputs with values of the right type.
	regSamples = strings.NewReplacer(`"${steps.edits.output.edits}"`, `[]`, `"${steps.cert.output.certName}"`, `"`+regCertName+`"`)
)

// regComposites are the composites over the DERP host h1, whose derp_servers row holds the region given, with the
// step ids each returns.
var regComposites = []struct {
	name      string
	action    string
	input     string
	region    int  // the row's region id; 0 for none
	connected bool // the tailnet account exists
	forward   bool
	steps     []string
}{
	{"deploy", "derp.server.deploy", `{"hostId":"h1","mode":"ip","hostName":"203.0.113.10"}`, 0, true, false, []string{"tailscaled", "install", "ports"}},
	{"deploy with client verification off", "derp.server.deploy", `{"hostId":"h1","mode":"letsencrypt","hostName":"derp.example.com","verifyClients":false}`, 0, true, false, []string{"tailscaled", "install", "ports"}},
	{"upgrade with a region", "derp.server.upgrade", `{"hostId":"h1"}`, 900, true, false, []string{"fallback", "tailscaled", "binary"}},
	{"upgrade without a region", "derp.server.upgrade", `{"hostId":"h1"}`, 0, true, false, []string{"tailscaled", "binary"}},
	{"rotate with a region", "derp.cert.rotate", `{"hostId":"h1"}`, 900, true, false, []string{"fallback", "cert", "edits", "region"}},
	{"rotate without a region", "derp.cert.rotate", `{"hostId":"h1"}`, 0, true, false, []string{"cert"}},
	{"region set", "derp.region.set", `{"hostId":"h1","regionId":900,"regionCode":"nyc","regionName":"New York"}`, 0, true, false, []string{"edits", "region"}},
	{"region set with omitted default regions", "derp.region.set", `{"hostId":"h1","regionId":900,"regionCode":"nyc","regionName":"New York","omitDefaultRegions":true,"acknowledge":"nodes will relay only through nyc"}`, 0, true, false, []string{"edits", "region"}},
	{"region remove", "derp.region.remove", `{"regionId":900}`, 900, true, false, []string{"edits", "region"}},
	{"remove with a region", "derp.server.remove", `{"hostId":"h1"}`, 900, true, true, []string{"edits", "region", "uninstall", "unpin", "ports"}},
	{"remove with a region, tailnet not connected", "derp.server.remove", `{"hostId":"h1"}`, 900, false, true, []string{"uninstall", "unpin", "ports"}},
	{"remove without a region", "derp.server.remove", `{"hostId":"h1"}`, 0, true, true, []string{"uninstall", "unpin", "ports"}},
}

// TestRegStepsMatchTheRegistry: every step a composite returns names a registered action, and obeys the rules the
// engine checks at plan time: a controller action takes no host and an agent action the DERP host, no step is
// irreversible or a composite, at most one step is revertible and it is last, every compensable step of a reverse
// composite has an undo handler, every step of the forward composite that is not a read converges, each step follows
// the one before it and only references steps it follows, and each step's input is valid for its action's schema once
// the references are filled in.
func TestRegStepsMatchTheRegistry(t *testing.T) {
	validator := schema.NewValidator()
	for _, c := range regComposites {
		t.Run(c.name, func(t *testing.T) {
			h := newDPHost(t)
			h.addHost("h1", "derp-a", false)
			h.tailnet = c.connected
			rec := serverRecord{
				HostID: "h1", Mode: "ip", HostName: "203.0.113.10", RegionID: c.region, DerperVersion: "v1.104.1",
				CertName: regCertName, UnitActive: true, CheckedAt: h.clock.Now(),
			}
			if c.region != 0 {
				rec.RegionCode = "nyc"
			}
			if err := upsertServerRecord(context.Background(), h.ctlDB, rec); err != nil {
				t.Fatal(err)
			}
			reg, err := core.NewRegistry(module{d: h.dp()}, tailscale.Module(), tailnet.Module(), firewall.Module())
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
			var members map[string]json.RawMessage
			if err := json.Unmarshal([]byte(c.input), &members); err != nil {
				t.Fatal(err)
			}
			steps, err := def.Handler.Steps(context.Background(), h.ctlEnv(""), json.RawMessage(c.input))
			if err != nil {
				t.Fatal(err)
			}
			ids := map[string]int{}
			revertible := -1
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
				if i > 0 && !slices.Contains(s.After, steps[i-1].ID) {
					t.Errorf("step %s follows %v, want it after %s", s.ID, s.After, steps[i-1].ID)
				}
				for _, a := range s.After {
					if before, ok := ids[a]; !ok || before >= i {
						t.Errorf("step %s follows %s, which is not an earlier step", s.ID, a)
					}
				}
				input := regInputRef.ReplaceAllStringFunc(string(s.Input), func(ref string) string {
					member := regInputRef.FindStringSubmatch(ref)[1]
					v, ok := members[member]
					if !ok {
						t.Errorf("step %s: %s names a member the composite's input does not hold", s.ID, ref)
						return ref
					}
					return string(v)
				})
				for _, m := range regStepRef.FindAllStringSubmatch(input, -1) {
					if !slices.Contains(s.After, m[1]) {
						t.Errorf("step %s references step %s but does not follow it", s.ID, m[1])
					}
				}
				input = regSamples.Replace(input)
				if strings.Contains(input, "${") {
					t.Errorf("step %s: input %s holds a reference the test does not fill", s.ID, s.Input)
					continue
				}
				if err := validator.ValidateInput(child, json.RawMessage(input)); err != nil {
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
