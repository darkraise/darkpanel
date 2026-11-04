package panel

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/modules/cloudflare"
	"github.com/darkraise/darkpanel/internal/modules/firewall"
	"github.com/darkraise/darkpanel/internal/modules/npm"
)

const (
	rgHostID   = "h1"
	rgHostname = "panel.example.com"
	rgTunnel   = "0b6c1f8e-3d52-4a7b-9c14-5e8d2a1f7b30"
)

// rgAction is one row of the table of the actions spec section 8 and the plan rulings give the module.
type rgAction struct {
	name          string
	class         core.Class
	side          core.Side
	locks         []string
	in, out       reflect.Type
	undoer        bool // has an Inverse (and so an Observe) handler
	internal      bool
	converges     bool
	forwardOnly   bool
	breakGlass    bool
	compensatedBy string
	unwind        core.Unwind
}

func rgType[T any]() reflect.Type { return reflect.TypeFor[T]() }

var rgActions = []rgAction{
	{name: ActionNetCreate, class: core.ClassCompensable, side: core.SideAgent, locks: []string{"panel"}, undoer: true, in: rgType[NetworkCreateInput](), out: rgType[NetworkView]()},
	{name: ActionNetRemove, class: core.ClassCompensable, side: core.SideAgent, locks: []string{"panel"}, converges: true, compensatedBy: ActionNetCreate, in: rgType[NetworkRemoveInput](), out: rgType[NetworkRemoveOutput]()},
	{name: ActionConnCheck, class: core.ClassRead, side: core.SideAgent, internal: true, in: rgType[ConnectorCheckInput](), out: rgType[ConnectorCheckOutput]()},
	{name: ActionOpen, class: core.ClassCompensable, side: core.SideController, locks: []string{"panel"}, undoer: true, in: rgType[ListenerOpenInput](), out: rgType[ListenerOpenOutput]()},
	{name: ActionRevoke, class: core.ClassCompensable, side: core.SideController, locks: []string{"panel"}, converges: true, forwardOnly: true, breakGlass: true, in: rgType[ListenerRevokeInput](), out: rgType[ListenerRevokeOutput]()},
	{name: ActionStatus, class: core.ClassRead, side: core.SideController, in: rgType[StatusInput](), out: rgType[StatusOutput]()},
	{name: ActionAdd, class: core.ClassComposite, side: core.SideController, locks: []string{"composite:panel"}, unwind: core.UnwindReverse, in: rgType[ExposeInput](), out: rgType[ExposeOutput]()},
	{name: ActionRemove, class: core.ClassComposite, side: core.SideController, locks: []string{"composite:panel"}, unwind: core.UnwindForward, in: rgType[RemoveInput](), out: rgType[RemoveOutput]()},
}

func rgDefs(t *testing.T, m core.Module) map[string]core.ActionDef {
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

// TestRgManifestDeclaresTheActions: the module declares the eight actions of the table, all version 1 and for
// admins, each on its side under the panel lock (the composites under composite:panel, the reads under none), with the
// class, handlers, flags and unwind policy of the table.
func TestRgManifestDeclaresTheActions(t *testing.T) {
	m := Module()
	man := m.Manifest()
	if man.ID != ID || man.Version != 1 || !man.DefaultEnabled {
		t.Errorf("manifest %q v%d default-enabled %v, want panel v1 enabled by default", man.ID, man.Version, man.DefaultEnabled)
	}
	got := rgDefs(t, m)
	for _, want := range rgActions {
		a, ok := got[want.name]
		if !ok {
			t.Errorf("%s is not declared", want.name)
			continue
		}
		delete(got, want.name)
		s := a.Spec
		if s.Version != 1 || s.Summary == "" || s.Class != want.class || s.Side != want.side || !slices.Equal(s.LockKeys, want.locks) ||
			s.MinScope != core.ScopeAdmin || core.RequiredScope(s) != core.ScopeAdmin || s.Internal != want.internal ||
			s.Converges != want.converges || s.ForwardOnly != want.forwardOnly || s.BreakGlass != want.breakGlass ||
			s.CompensatedBy != want.compensatedBy || s.Unwind != want.unwind {
			t.Errorf("%s: spec %+v; want class %s on the %s, locks %v, scope admin, internal %v, converges %v, forward-only %v, break-glass %v, compensated by %q, unwind %q",
				want.name, s, want.class, want.side, want.locks, want.internal, want.converges, want.forwardOnly, want.breakGlass, want.compensatedBy, want.unwind)
		}
		h := a.Handler
		if (h.Inverse != nil) != want.undoer || (want.undoer && h.Observe == nil) {
			t.Errorf("%s: undo handler %v, observer %v; want undo %v", want.name, h.Inverse != nil, h.Observe != nil, want.undoer)
		}
		var served bool
		switch {
		case want.internal:
			served = h.Reveal != nil && h.Read == nil
		case want.class == core.ClassRead:
			served = h.Read != nil && h.Reveal == nil
		case want.class == core.ClassComposite:
			served = h.Steps != nil && h.Output != nil && h.Plan == nil && h.Apply == nil
		default:
			served = h.Plan != nil && h.Verify != nil && h.Apply != nil
		}
		if !served {
			t.Errorf("%s: handlers do not match its class %s (internal %v)", want.name, want.class, want.internal)
		}
		if a.InputType != want.in || a.OutputType != want.out {
			t.Errorf("%s: types %v and %v, want %v and %v", want.name, a.InputType, a.OutputType, want.in, want.out)
		}
		if _, err := schema.ActionSnapshot(a); err != nil {
			t.Errorf("%s: %v", a.Ref(), err)
		}
	}
	for name := range got {
		t.Errorf("%s is declared but is not in the action table", name)
	}
}

// TestRgManifestDeclaresTheRest: no resource, preflight check, external unit or owned path, and the controller's
// migrations only.
func TestRgManifestDeclaresTheRest(t *testing.T) {
	man := Module().Manifest()
	if len(man.Resources) != 0 || len(man.Preflight) != 0 || len(man.ExternalUnits) != 0 || len(man.OwnedPaths) != 0 {
		t.Errorf("manifest declares %d resources, %d preflight checks, %d external units and %d owned paths, want none",
			len(man.Resources), len(man.Preflight), len(man.ExternalUnits), len(man.OwnedPaths))
	}
	migrations, ok := man.Migrations[core.SideController]
	if !ok || len(man.Migrations) != 1 {
		t.Fatalf("migrations %v, want the controller side only", man.Migrations)
	}
	found := false
	err := fs.WalkDir(migrations, ".", func(p string, d fs.DirEntry, err error) error {
		found = found || err == nil && !d.IsDir() && filepath.Base(p) == "0001_panel.sql"
		return err
	})
	if err != nil || !found {
		t.Errorf("controller migrations hold no 0001_panel.sql (walk error %v)", err)
	}
}

var (
	rgSubnet  = netip.MustParsePrefix("172.31.255.248/29")
	rgGateway = netip.MustParseAddr("172.31.255.249")
	rgProxy   = netip.MustParseAddr("172.31.255.250")
)

// rgSamples are valid inputs of the actions, written with the types' own JSON names.
var rgSamples = map[string]any{
	ActionNetCreate: NetworkCreateInput{},
	ActionNetRemove: NetworkRemoveInput{},
	ActionConnCheck: ConnectorCheckInput{Tunnel: rgTunnel},
	ActionOpen: ListenerOpenInput{Via: ViaNPM, Hostname: rgHostname, HostID: rgHostID, Subnet: rgSubnet, Gateway: rgGateway, Proxy: rgProxy,
		Port: DefaultPort},
	ActionRevoke: ListenerRevokeInput{},
	ActionStatus: StatusInput{},
	ActionAdd:    ExposeInput{Via: ViaTunnel, Hostname: rgHostname, HostID: rgHostID, Tunnel: rgTunnel, Port: 9444},
	ActionRemove: RemoveInput{},
}

func rgMembers(t *testing.T, v any) map[string]any {
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

// rgRefusal requires err to be request.invalid_input naming field.
func rgRefusal(err error, field string) bool {
	var coded *errcode.Error
	return errors.As(err, &coded) && coded.Code == errcode.InvalidInput &&
		slices.ContainsFunc(coded.Fields, func(f errcode.FieldError) bool { return f.Path == field })
}

// TestRgInputsAcceptTheirSampleAndRefuseUnknownMembers: each action is registered with its own input type, so its
// schema accepts the input it takes and refuses a member it does not have.
func TestRgInputsAcceptTheirSampleAndRefuseUnknownMembers(t *testing.T) {
	defs := rgDefs(t, Module())
	v := schema.NewValidator()
	for _, want := range rgActions {
		def, ok := defs[want.name]
		if !ok {
			t.Errorf("%s is not declared", want.name)
			continue
		}
		members := rgMembers(t, rgSamples[want.name])
		raw, _ := json.Marshal(members)
		if err := v.ValidateInput(def, raw); err != nil {
			t.Errorf("%s %s: %v, want it accepted", want.name, raw, err)
		}
		members["x"] = 1
		raw, _ = json.Marshal(members)
		if err := v.ValidateInput(def, raw); !rgRefusal(err, "/x") {
			t.Errorf("%s %s: %v, want request.invalid_input naming /x", want.name, raw, err)
		}
	}
}

// TestRgInputsRefuseWhatTheWireTypesRequire: the members the wire types make required or typed.
func TestRgInputsRefuseWhatTheWireTypesRequire(t *testing.T) {
	defs := rgDefs(t, Module())
	v := schema.NewValidator()
	for _, tc := range []struct {
		name, action, field string
		mutate              func(map[string]any)
	}{
		{"expose without the route", ActionAdd, "/via", func(m map[string]any) { delete(m, "via") }},
		{"expose without the hostname", ActionAdd, "/hostname", func(m map[string]any) { delete(m, "hostname") }},
		{"expose without the host", ActionAdd, "/hostId", func(m map[string]any) { delete(m, "hostId") }},
		{"expose with a text port", ActionAdd, "/port", func(m map[string]any) { m["port"] = "9443" }},
		{"listener open without the proxy address", ActionOpen, "/proxy", func(m map[string]any) { delete(m, "proxy") }},
		{"connector check without the tunnel", ActionConnCheck, "/tunnel", func(m map[string]any) { delete(m, "tunnel") }},
	} {
		def, ok := defs[tc.action]
		if !ok {
			t.Errorf("%s: %s is not declared", tc.name, tc.action)
			continue
		}
		members := rgMembers(t, rgSamples[tc.action])
		tc.mutate(members)
		raw, _ := json.Marshal(members)
		if err := v.ValidateInput(def, raw); !rgRefusal(err, tc.field) {
			t.Errorf("%s: %s %s: %v, want request.invalid_input naming %s", tc.name, tc.action, raw, err, tc.field)
		}
	}
}

// TestRgNoSecretIsDeclared: the module handles no secret, so no input and no output schema marks a field secret.
func TestRgNoSecretIsDeclared(t *testing.T) {
	for _, def := range Module().Manifest().Actions {
		in, err := schema.Input(def.InputType)
		if err != nil {
			t.Errorf("%s input: %v", def.Ref(), err)
		}
		out, err := schema.Output(def.OutputType)
		if err != nil {
			t.Errorf("%s output: %v", def.Ref(), err)
		}
		if strings.Contains(string(in), "x-secret") || strings.Contains(string(out), "x-secret") {
			t.Errorf("%s: a schema marks a field secret", def.Ref())
		}
	}
}

// TestRgSnapshotsAreCommitted: each action has its committed snapshot, and no panel snapshot exists for anything else
// (modules.TestSnapshotsCurrent checks that each file is current).
func TestRgSnapshotsAreCommitted(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "schemas")
	want := map[string]bool{}
	for _, a := range rgActions {
		want[a.name] = true
		if _, err := os.Stat(filepath.Join(dir, a.name, "v1.json")); err != nil {
			t.Errorf("schemas/%s/v1.json: %v; run make schemas", a.name, err)
		}
	}
	dirs, err := filepath.Glob(filepath.Join(dir, "panel.*"))
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range dirs {
		if name := filepath.Base(d); !want[name] {
			t.Errorf("schemas/%s belongs to no panel action; run make schemas", name)
		}
	}
}

// rgRegistry is the registry of the panel module with the modules its composites call.
func rgRegistry(t *testing.T) *core.Registry {
	t.Helper()
	reg, err := core.NewRegistry(Module(), npm.Module(), cloudflare.Module(), firewall.Module())
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

func TestRgRegistryAcceptsTheModule(t *testing.T) {
	reg := rgRegistry(t)
	for _, a := range rgActions {
		if _, ok := reg.Action(core.ActionRef{Name: a.name, Version: 1}); !ok {
			t.Errorf("the registry has no %s", a.name)
		}
	}
}

var rgStepRef = regexp.MustCompile(`\$\{steps\.([a-z][a-z0-9]*)\.output`)

// rgOutputs gives each step action whose output a composite's steps reference a sample output of its output type.
var rgOutputs = map[string]any{
	ActionNetCreate:           NetworkView{Network: NetworkName, ID: "n1", Subnet: rgSubnet, Gateway: rgGateway, Proxy: rgProxy},
	"npm.certificate.request": npm.CertificateView{ID: 3, Domains: []string{rgHostname}},
	ActionOpen:                ListenerOpenOutput{ExposureID: "c1", ListenerID: "l1", RuleID: RuleID("c1"), Address: netip.AddrPortFrom(rgGateway, DefaultPort)},
	ActionRevoke:              ListenerRevokeOutput{ExposureID: "c1", ListenerID: "l1", RuleID: RuleID("c1"), Revoked: true},
}

// rgComposites are the two composites with the step ids each returns; the removal reads row, the live exposure.
var rgComposites = []struct {
	name, action string
	input        any
	forward      bool
	steps        []string
	row          *exposureRow
	ruleRef      string // the reference the ports step holds to the rule id
}{
	{"expose through NPM", ActionAdd, ExposeInput{Via: ViaNPM, Hostname: rgHostname, HostID: rgHostID}, false,
		[]string{"public", "network", "attach", "listen", "cert", "proxy", "ports"}, nil, "${steps.listen.output.ruleId}"},
	{"expose through a tunnel", ActionAdd, ExposeInput{Via: ViaTunnel, Hostname: rgHostname, HostID: rgHostID, Tunnel: rgTunnel}, false,
		[]string{"connector", "network", "attach", "listen", "proxy", "ports"}, nil, "${steps.listen.output.ruleId}"},
	{"remove an NPM exposure", ActionRemove, RemoveInput{}, true,
		[]string{"revoke", "proxy", "attach", "network", "ports"}, rgRow(ViaNPM), "${steps.revoke.output.ruleId}"},
	{"remove a tunnel exposure", ActionRemove, RemoveInput{}, true,
		[]string{"revoke", "proxy", "attach", "network", "ports"}, rgRow(ViaTunnel), "${steps.revoke.output.ruleId}"},
}

// rgRow is the live exposure of the controller's host over via.
func rgRow(via string) *exposureRow {
	id := "01JD0000000000000000000010"
	r := &exposureRow{
		ID: id, Via: via, Hostname: rgHostname, HostID: rgHostID, Subnet: rgSubnet, Gateway: rgGateway, Proxy: rgProxy,
		Port: DefaultPort, ListenerID: "l1", RuleID: RuleID(id), State: StateOpen, CreatedAt: fdNow, UpdatedAt: fdNow,
	}
	if via == ViaTunnel {
		r.Tunnel = rgTunnel
	}
	return r
}

// TestRgStepsMatchTheRegistry: every step a composite returns names a registered action and obeys the rules the engine
// checks at plan time: a controller action takes no host and an agent action the controller's host, no step is
// irreversible or a composite, at most one step is revertible and it is last, every compensable step of a reverse
// composite has an undo handler, every step of a forward composite that is not a read converges, each step follows
// only steps before it and references only steps it follows, only a non-read step of a forward composite retires an
// exposure, and each step's input is valid for its action's schema once its references are resolved (eaResolve)
// against the composite's input and sample outputs of the steps it follows.
func TestRgStepsMatchTheRegistry(t *testing.T) {
	validator := schema.NewValidator()
	reg := rgRegistry(t)
	for _, c := range rgComposites {
		t.Run(c.name, func(t *testing.T) {
			def, ok := reg.Action(core.ActionRef{Name: c.action, Version: 1})
			if !ok {
				t.Fatalf("%s is not registered", c.action)
			}
			if (def.Spec.Unwind == core.UnwindForward) != c.forward {
				t.Errorf("%s unwinds %q, want forward %v", c.action, def.Spec.Unwind, c.forward)
			}
			db := fdDB(t)
			fdInsertHost(t, db, rgHostID, true)
			fdInsertUser(t, db, "ann", "active")
			if c.row != nil {
				fdInsertExposure(t, db, *c.row)
			}
			raw, _ := json.Marshal(c.input)
			steps, err := def.Handler.Steps(context.Background(), fdEnv(t, db, fdNewListeners(), core.CallContext{}), raw)
			if err != nil {
				t.Fatal(err)
			}
			index := map[string]int{}
			follows := map[string]map[string]bool{}
			outputs := map[string]any{}
			revertible := -1
			var got []string
			for i, s := range steps {
				got = append(got, s.ID)
				if _, dup := index[s.ID]; dup {
					t.Errorf("step id %s appears twice", s.ID)
				}
				index[s.ID] = i
				follows[s.ID] = map[string]bool{}
				for _, a := range s.After {
					if before, ok := index[a]; !ok || before >= i {
						t.Errorf("step %s follows %s, which is not an earlier step", s.ID, a)
					}
					follows[s.ID][a] = true
					for f := range follows[a] {
						follows[s.ID][f] = true
					}
				}
				if o, ok := rgOutputs[s.Action.Name]; ok {
					outputs[s.ID] = o
				}
				child, ok := reg.Action(s.Action)
				if !ok {
					t.Errorf("step %s: %s is not registered", s.ID, s.Action)
					continue
				}
				if child.Spec.Side == core.SideController && s.Host != "" || child.Spec.Side == core.SideAgent && s.Host != rgHostID {
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
				if s.Retires != "" && (!c.forward || child.Spec.Class == core.ClassRead) {
					t.Errorf("step %s retires %q, which only a non-read step of a forward composite may", s.ID, s.Retires)
				}
				for _, m := range rgStepRef.FindAllStringSubmatch(string(s.Input), -1) {
					if !follows[s.ID][m[1]] {
						t.Errorf("step %s references step %s but does not follow it", s.ID, m[1])
					}
				}
				followed := map[string]any{}
				for id := range follows[s.ID] {
					if o, ok := outputs[id]; ok {
						followed[id] = o
					}
				}
				input := eaResolve(t, s.Input, c.input, followed)
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

// TestRgPortsStepWaitsForTheRuleID: the firewall step of each composite is the last, names the rule by the output of
// the step that opens or revokes the listener, and so is planned only after that step has applied.
func TestRgPortsStepWaitsForTheRuleID(t *testing.T) {
	reg := rgRegistry(t)
	for _, c := range rgComposites {
		t.Run(c.name, func(t *testing.T) {
			def, _ := reg.Action(core.ActionRef{Name: c.action, Version: 1})
			db := fdDB(t)
			fdInsertHost(t, db, rgHostID, true)
			if c.row != nil {
				fdInsertExposure(t, db, *c.row)
			}
			raw, _ := json.Marshal(c.input)
			steps, err := def.Handler.Steps(context.Background(), fdEnv(t, db, fdNewListeners(), core.CallContext{}), raw)
			if err != nil {
				t.Fatal(err)
			}
			if len(steps) == 0 {
				t.Fatal("the composite returned no steps")
			}
			last := steps[len(steps)-1]
			if last.ID != "ports" || last.Action.Name != "firewall.rules.apply" || !strings.Contains(string(last.Input), c.ruleRef) {
				t.Errorf("last step %s (%s) holds %s, want the ports step with %s", last.ID, last.Action, last.Input, c.ruleRef)
			}
		})
	}
}
