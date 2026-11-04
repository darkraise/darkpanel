package panel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/modules/cloudflare"
	"github.com/darkraise/darkpanel/internal/modules/firewall"
	"github.com/darkraise/darkpanel/internal/modules/firewall/model"
	"github.com/darkraise/darkpanel/internal/modules/npm"
)

const (
	eaHost   = "h1"
	eaName   = "panel.example.com"
	eaTunnel = "8e6f7a8b-9c0d-4e1f-a2b3-c4d5e6f70819"
	eaChange = "01JEXPOSURE0000000000000A"
)

var (
	eaSubnet  = netip.MustParsePrefix("172.31.255.248/29")
	eaGateway = netip.MustParseAddr("172.31.255.249")
	eaProxy   = netip.MustParseAddr("172.31.255.250")
)

// eaEnv is a controller env over a database that holds the controller's host h1, another host h2 and an
// administrator who has enrolled TOTP, so that no refusal applies until a test adds its cause.
func eaEnv(t *testing.T) (core.Env, *store.DB) {
	t.Helper()
	db := fdDB(t)
	fdInsertHost(t, db, eaHost, true)
	fdInsertHost(t, db, "h2", false)
	fdInsertUser(t, db, "admin", "active")
	return fdEnv(t, db, fdNewListeners(), core.CallContext{}), db
}

func eaNPMInput(port int) ExposeInput {
	return ExposeInput{Via: ViaNPM, Hostname: eaName, HostID: eaHost, Port: port}
}

func eaTunnelInput(port int) ExposeInput {
	return ExposeInput{Via: ViaTunnel, Hostname: eaName, HostID: eaHost, Tunnel: eaTunnel, Port: port}
}

func eaJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// eaSteps asks the composite for its steps the way the engine does, through the erased handler.
func eaSteps(t *testing.T, env core.Env, in ExposeInput) ([]core.Step, error) {
	t.Helper()
	return exposureAddAction().Handler.Steps(context.Background(), env, eaJSON(t, in))
}

// eaOK returns the steps of in, which must be accepted.
func eaOK(t *testing.T, in ExposeInput) []core.Step {
	t.Helper()
	env, _ := eaEnv(t)
	steps, err := eaSteps(t, env, in)
	if err != nil {
		t.Fatalf("steps: %v", err)
	}
	return steps
}

// eaGeneric is v as decoded JSON.
func eaGeneric(t *testing.T, v any) any {
	t.Helper()
	var out any
	if err := json.Unmarshal(eaJSON(t, v), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

var eaRef = regexp.MustCompile(`\$\{([^}]*)\}`)

// eaResolve resolves the references of a step template against the composite's input in and the outputs of earlier
// steps, as the engine does: a string that is exactly one reference becomes the referenced value with its JSON type,
// a reference inside a longer string is replaced by the value's text. A reference to a member that does not exist
// fails the test, as it fails the plan.
func eaResolve(t *testing.T, tmpl json.RawMessage, in any, outputs map[string]any) json.RawMessage {
	t.Helper()
	steps := map[string]any{}
	for id, v := range outputs {
		steps[id] = map[string]any{"output": eaGeneric(t, v)}
	}
	root := map[string]any{"input": eaGeneric(t, in), "steps": steps}
	lookup := func(ref string) any {
		var cur any = root
		for _, p := range strings.Split(ref, ".") {
			m, ok := cur.(map[string]any)
			if !ok {
				t.Errorf("reference ${%s}: %q is looked up in a value that has no members", ref, p)
				return nil
			}
			if cur, ok = m[p]; !ok {
				t.Errorf("reference ${%s}: there is no member %q", ref, p)
				return nil
			}
		}
		return cur
	}
	var walk func(v any) any
	walk = func(v any) any {
		switch x := v.(type) {
		case string:
			if m := eaRef.FindStringSubmatch(x); m != nil && m[0] == x {
				return lookup(m[1])
			}
			return eaRef.ReplaceAllStringFunc(x, func(s string) string { return fmt.Sprint(lookup(s[2 : len(s)-1])) })
		case map[string]any:
			for k, e := range x {
				x[k] = walk(e)
			}
		case []any:
			for i, e := range x {
				x[i] = walk(e)
			}
		}
		return v
	}
	var tv any
	if err := json.Unmarshal(tmpl, &tv); err != nil {
		t.Fatalf("template %s: %v", tmpl, err)
	}
	out, err := json.Marshal(walk(tv))
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// eaStrict decodes raw into a T and refuses a member T does not have.
func eaStrict[T any](t *testing.T, raw json.RawMessage) T {
	t.Helper()
	var v T
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("input %s: %v", raw, err)
	}
	return v
}

// eaOutputs are the outputs the earlier steps of an exposure on port answer.
func eaOutputs(port uint16) map[string]any {
	return map[string]any{
		"network": NetworkView{Network: NetworkName, ID: "n1", Subnet: eaSubnet, Gateway: eaGateway, Proxy: eaProxy},
		"listen": ListenerOpenOutput{
			ExposureID: eaChange, ListenerID: "l1", RuleID: RuleID(eaChange), Address: netip.AddrPortFrom(eaGateway, port),
		},
		"cert": npm.CertificateView{ID: 42},
	}
}

// eaDef returns the declaration of action name of the npm, cloudflare or firewall module.
func eaDef(t *testing.T, name string) (core.ActionDef, bool) {
	t.Helper()
	for _, m := range []core.Module{npm.Module(), cloudflare.Module(), firewall.Module()} {
		for _, a := range m.Manifest().Actions {
			if a.Spec.Name == name && a.Spec.Version == 1 {
				return a, true
			}
		}
	}
	return core.ActionDef{}, false
}

// eaWantStep fails the test unless s is the step id on host running action after the steps after.
func eaWantStep(t *testing.T, s core.Step, id, host, action string, after []string) {
	t.Helper()
	if s.ID != id || s.Host != host || s.Action.String() != action || !slices.Equal(s.After, after) {
		t.Errorf("step %+v, want %s: %s on host %q after %v", s, id, action, host, after)
	}
}

func eaIDs(steps []core.Step) []string {
	var ids []string
	for _, s := range steps {
		ids = append(ids, s.ID)
	}
	return ids
}

var eaStepRef = regexp.MustCompile(`\$\{steps\.([a-z][a-z0-9]*)\.`)

// eaCheckGraph applies the registry-time rules of the composite engine that a test can reach: unique ids, steps that
// follow earlier steps only, references only to steps the referencing step follows, host exactly for agent actions,
// a revertible step only last, and an undo handler for every compensable step of the module's neighbours. The
// resolved input of a step of another module must be valid for that action.
func eaCheckGraph(t *testing.T, steps []core.Step, in ExposeInput, port uint16) {
	t.Helper()
	pos := map[string]int{}
	follows := map[string]map[string]bool{}
	for i, s := range steps {
		if _, dup := pos[s.ID]; dup {
			t.Errorf("step id %s is used twice", s.ID)
		}
		set := map[string]bool{}
		for _, a := range s.After {
			if _, ok := pos[a]; !ok {
				t.Errorf("step %s follows %s, which is not an earlier step", s.ID, a)
			}
			set[a] = true
			for f := range follows[a] {
				set[f] = true
			}
		}
		pos[s.ID], follows[s.ID] = i, set
		for _, m := range eaStepRef.FindAllStringSubmatch(string(s.Input), -1) {
			if !set[m[1]] {
				t.Errorf("step %s references step %s, which it does not follow", s.ID, m[1])
			}
		}
		def, ok := eaDef(t, s.Action.Name)
		if !ok {
			continue
		}
		if (def.Spec.Side == core.SideController) != (s.Host == "") {
			t.Errorf("step %s: %s is a %s-side action and the step's host is %q", s.ID, s.Action, def.Spec.Side, s.Host)
		}
		if def.Spec.Class == core.ClassRevertible && i != len(steps)-1 {
			t.Errorf("step %s: %s is revertible and must be the last step", s.ID, s.Action)
		}
		if def.Spec.Class == core.ClassCompensable && def.Handler.Inverse == nil {
			t.Errorf("step %s: %s has no undo handler, which a reverse composite needs", s.ID, s.Action)
		}
		resolved := eaResolve(t, s.Input, in, eaOutputs(port))
		if err := schema.NewValidator().ValidateInput(def, resolved); err != nil {
			t.Errorf("step %s: input %s is invalid for %s: %v", s.ID, resolved, s.Action, err)
		}
	}
}

// eaPorts checks the final firewall step: one allow rule from the proxy to the gateway on the bridge, with the
// rule id the listener step answers, labelled with the hostname.
func eaPorts(t *testing.T, s core.Step, in ExposeInput, port uint16, after string) {
	t.Helper()
	eaWantStep(t, s, "ports", eaHost, "firewall.rules.apply/v1", []string{after})
	got := eaStrict[firewall.RulesApplyInput](t, eaResolve(t, s.Input, in, eaOutputs(port)))
	want := []model.Rule{{
		ID: RuleID(eaChange), Action: model.ActionAllow, Direction: model.DirIn, Proto: model.ProtoTCP,
		From: eaProxy.String(), To: eaGateway.String(), ToPort: strconv.Itoa(int(port)), InIface: BridgeName, Family: model.FamilyV4,
	}}
	if got.Label != "panel exposure "+eaName || !reflect.DeepEqual(got.Add, want) || len(got.Delete) != 0 || got.Guard != nil || len(got.Overrides) != 0 {
		t.Errorf("ports input %+v, want label %q and the rules %+v", got, "panel exposure "+eaName, want)
	}
	if !strings.Contains(string(s.Input), "${steps.listen.output.ruleId}") {
		t.Errorf("ports template %s does not take the rule id from the listen step", s.Input)
	}
}

// TestEaDeclaration: a controller-side composite that takes the module's coordination lock, unwinds in reverse and
// needs the admin scope, over an input the schema accepts.
func TestEaDeclaration(t *testing.T) {
	def := exposureAddAction()
	s := def.Spec
	if s.Name != ActionAdd || s.Version != 1 || s.Summary == "" || s.Class != core.ClassComposite || s.Side != core.SideController ||
		s.Unwind != core.UnwindReverse || s.MinScope != core.ScopeAdmin || !slices.Equal(s.LockKeys, []string{"composite:panel"}) {
		t.Errorf("spec %+v, want version 1, a summary, a controller-side composite, unwind reverse, admin scope, lock composite:panel", s)
	}
	if def.Handler.Steps == nil || def.Handler.Output == nil {
		t.Error("the handler lacks Steps or Output")
	}
	if _, err := schema.ActionSnapshot(def); err != nil {
		t.Errorf("schema: %v", err)
	}
	for _, in := range []ExposeInput{eaNPMInput(0), eaTunnelInput(0), eaNPMInput(9444)} {
		if err := schema.NewValidator().ValidateInput(def, eaJSON(t, in)); err != nil {
			t.Errorf("a valid input %+v is refused by the schema: %v", in, err)
		}
	}
}

// TestEaRefusals: Steps refuses in the order input, host, TOTP, existing exposure; each row also holds the causes
// that come after its own, to pin the order. An exposure that is gone refuses nothing.
func TestEaRefusals(t *testing.T) {
	pending := func(t *testing.T, db *store.DB) { fdInsertUser(t, db, "newcomer", "pending") }
	exposure := func(state, removal, change string) func(t *testing.T, db *store.DB) {
		return func(t *testing.T, db *store.DB) {
			if change != "" {
				fdInsertChange(t, db, removal, ActionRemove, change)
			}
			fdInsertExposure(t, db, eaExposure("01JLIVE000000000000000001", state, removal))
		}
	}
	with := func(fs ...func(t *testing.T, db *store.DB)) func(t *testing.T, db *store.DB) {
		return func(t *testing.T, db *store.DB) {
			for _, f := range fs {
				f(t, db)
			}
		}
	}
	edit := func(f func(in *ExposeInput)) ExposeInput {
		in := eaNPMInput(0)
		f(&in)
		return in
	}
	tests := []struct {
		name  string
		setup func(t *testing.T, db *store.DB)
		in    ExposeInput
		code  errcode.Code
		path  string // the field a request.invalid_input names
	}{
		{"an unknown via", with(pending, exposure(StateOpen, "", "")), edit(func(in *ExposeInput) { in.Via, in.HostID = "carrier", "h2" }), errcode.InvalidInput, "/via"},
		{"no hostname", nil, edit(func(in *ExposeInput) { in.Hostname = "" }), errcode.InvalidInput, "/hostname"},
		{"a hostname in capitals", nil, edit(func(in *ExposeInput) { in.Hostname = "Panel.Example.com" }), errcode.InvalidInput, "/hostname"},
		{"a hostname that is a reference", nil, edit(func(in *ExposeInput) { in.Hostname = "${steps.network.output.proxy}.example.com" }), errcode.InvalidInput, "/hostname"},
		{"a tunnel for npm", nil, edit(func(in *ExposeInput) { in.Tunnel = eaTunnel }), errcode.InvalidInput, "/tunnel"},
		{"a tunnel exposure without a tunnel", nil, ExposeInput{Via: ViaTunnel, Hostname: eaName, HostID: eaHost}, errcode.InvalidInput, "/tunnel"},
		{"a tunnel that is not a UUID", nil, ExposeInput{Via: ViaTunnel, Hostname: eaName, HostID: eaHost, Tunnel: "my-tunnel"}, errcode.InvalidInput, "/tunnel"},
		{"a port below 1024", nil, edit(func(in *ExposeInput) { in.Port = 80 }), errcode.InvalidInput, "/port"},
		{"a port above 65535", nil, edit(func(in *ExposeInput) { in.Port = 65536 }), errcode.InvalidInput, "/port"},
		{"another host", with(pending, exposure(StateOpen, "", "")), edit(func(in *ExposeInput) { in.HostID = "h2" }), errcode.PanelWrongHost, ""},
		{"an unknown host", nil, edit(func(in *ExposeInput) { in.HostID = "h9" }), errcode.PanelWrongHost, ""},
		{"a user still to enrol TOTP", pending, eaNPMInput(0), errcode.PanelTOTPIncomplete, ""},
		{"a user still to enrol TOTP and an exposure", with(pending, exposure(StateOpen, "", "")), eaNPMInput(0), errcode.PanelTOTPIncomplete, ""},
		{"an open exposure", exposure(StateOpen, "", ""), eaNPMInput(0), errcode.PanelAlreadyExposed, ""},
		{"a revoked exposure with no removal", exposure(StateRevoked, "", ""), eaNPMInput(0), errcode.PanelAlreadyExposed, ""},
		{"a revoked exposure whose removal failed", exposure(StateRevoked, "01JREMOVE00000000000000001", "failed"), eaNPMInput(0), errcode.PanelAlreadyExposed, ""},
		{"an exposure that needs repair", exposure(StateNeedsRepair, "", ""), eaNPMInput(0), errcode.PanelAlreadyExposed, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env, db := eaEnv(t)
			if tc.setup != nil {
				tc.setup(t, db)
			}
			steps, err := eaSteps(t, env, tc.in)
			e := errcode.From(err)
			if err == nil || e.Code != tc.code {
				t.Fatalf("steps %v, error %v; want code %s", eaIDs(steps), err, tc.code)
			}
			if tc.path != "" && !slices.ContainsFunc(e.Fields, func(f errcode.FieldError) bool { return f.Path == tc.path }) {
				t.Errorf("fields %+v, want one at %s", e.Fields, tc.path)
			}
		})
	}

	t.Run("a gone exposure", func(t *testing.T) {
		env, db := eaEnv(t)
		fdInsertChange(t, db, "01JREMOVE00000000000000001", ActionRemove, "completed")
		fdInsertExposure(t, db, eaExposure("01JGONE0000000000000000001", StateRevoked, "01JREMOVE00000000000000001"))
		if _, err := eaSteps(t, env, eaNPMInput(0)); err != nil {
			t.Errorf("steps beside a gone exposure: %v", err)
		}
	})
}

// eaExposure is an exposure through NPM with the given id, state and removal change.
func eaExposure(id, state, removal string) exposureRow {
	return exposureRow{
		ID: id, Via: ViaNPM, Hostname: eaName, HostID: eaHost, Subnet: eaSubnet, Gateway: eaGateway, Proxy: eaProxy,
		Port: DefaultPort, ListenerID: "l1", RuleID: RuleID(id), State: state, RemovalChangeID: removal,
		CreatedAt: fdNow, UpdatedAt: fdNow,
	}
}

// TestEaNPMSteps: the public instance is checked first, the bridge network is created and the proxy attached at its
// second address, the listener opens, the certificate and the proxy host follow, and the firewall rule is last.
func TestEaNPMSteps(t *testing.T) {
	for _, tc := range []struct {
		name string
		port int
		want uint16
	}{{"the default port", 0, DefaultPort}, {"an explicit port", 9444, 9444}} {
		t.Run(tc.name, func(t *testing.T) {
			in := eaNPMInput(tc.port)
			steps := eaOK(t, in)
			if want := []string{"public", "network", "attach", "listen", "cert", "proxy", "ports"}; !slices.Equal(eaIDs(steps), want) {
				t.Fatalf("steps %v, want %v", eaIDs(steps), want)
			}
			out := eaOutputs(tc.want)
			resolve := func(i int) json.RawMessage { return eaResolve(t, steps[i].Input, in, out) }

			eaWantStep(t, steps[0], "public", "", "npm.public.check/v1", nil)
			if got := eaStrict[npm.PublicInput](t, resolve(0)); got != (npm.PublicInput{HostID: eaHost}) {
				t.Errorf("public input %+v, want host %s", got, eaHost)
			}
			eaWantStep(t, steps[1], "network", eaHost, "panel.network.create/v1", []string{"public"})
			if got := eaStrict[NetworkCreateInput](t, resolve(1)); got != (NetworkCreateInput{}) {
				t.Errorf("network input %+v, want none", got)
			}
			eaWantStep(t, steps[2], "attach", eaHost, "npm.network.attach/v1", []string{"network"})
			wantAttach := npm.AttachInput{Network: NetworkName, Claimant: Claimant, Address: eaProxy}
			if got := eaStrict[npm.AttachInput](t, resolve(2)); got != wantAttach {
				t.Errorf("attach input %+v, want %+v", got, wantAttach)
			}
			eaWantStep(t, steps[3], "listen", "", "panel.listener.open/v1", []string{"attach"})
			wantListen := ListenerOpenInput{
				Via: ViaNPM, Hostname: eaName, HostID: eaHost, Subnet: eaSubnet, Gateway: eaGateway, Proxy: eaProxy, Port: int(tc.want),
			}
			if got := eaStrict[ListenerOpenInput](t, resolve(3)); got != wantListen {
				t.Errorf("listen input %+v, want %+v", got, wantListen)
			}
			eaWantStep(t, steps[4], "cert", eaHost, "npm.certificate.request/v1", []string{"listen"})
			if got := eaStrict[npm.CertificateInput](t, resolve(4)); !reflect.DeepEqual(got, npm.CertificateInput{Domains: []string{eaName}}) {
				t.Errorf("certificate input %+v, want the hostname", got)
			}
			eaWantStep(t, steps[5], "proxy", eaHost, "npm.proxyhost.add/v1", []string{"cert"})
			wantProxy := npm.ProxyHostInput{
				Domains: []string{eaName}, ForwardScheme: "https", ForwardHost: eaGateway.String(), ForwardPort: int(tc.want),
				CertificateID: 42, SSLForced: true, HSTS: true,
			}
			if got := eaStrict[npm.ProxyHostInput](t, resolve(5)); !reflect.DeepEqual(got, wantProxy) {
				t.Errorf("proxy input %+v, want %+v", got, wantProxy)
			}
			eaPorts(t, steps[6], in, tc.want, "proxy")
			eaCheckGraph(t, steps, in, tc.want)
		})
	}
}

// TestEaTunnelSteps: the connector is checked first, the proxy is the tunnel's connector, and the tunnel hostname
// routes to the gateway over HTTPS without verifying the internal certificate.
func TestEaTunnelSteps(t *testing.T) {
	for _, tc := range []struct {
		name string
		port int
		want uint16
	}{{"the default port", 0, DefaultPort}, {"an explicit port", 9444, 9444}} {
		t.Run(tc.name, func(t *testing.T) {
			in := eaTunnelInput(tc.port)
			steps := eaOK(t, in)
			if want := []string{"connector", "network", "attach", "listen", "proxy", "ports"}; !slices.Equal(eaIDs(steps), want) {
				t.Fatalf("steps %v, want %v", eaIDs(steps), want)
			}
			out := eaOutputs(tc.want)
			resolve := func(i int) json.RawMessage { return eaResolve(t, steps[i].Input, in, out) }

			eaWantStep(t, steps[0], "connector", eaHost, "panel.connector.check/v1", nil)
			if got := eaStrict[ConnectorCheckInput](t, resolve(0)); got != (ConnectorCheckInput{Tunnel: eaTunnel}) {
				t.Errorf("connector input %+v, want tunnel %s", got, eaTunnel)
			}
			eaWantStep(t, steps[1], "network", eaHost, "panel.network.create/v1", []string{"connector"})
			if got := eaStrict[NetworkCreateInput](t, resolve(1)); got != (NetworkCreateInput{}) {
				t.Errorf("network input %+v, want none", got)
			}
			eaWantStep(t, steps[2], "attach", eaHost, "cloudflare.network.attach/v1", []string{"network"})
			wantAttach := cloudflare.NetworkAttachInput{Tunnel: eaTunnel, Network: NetworkName, Claimant: Claimant, Address: eaProxy}
			if got := eaStrict[cloudflare.NetworkAttachInput](t, resolve(2)); got != wantAttach {
				t.Errorf("attach input %+v, want %+v", got, wantAttach)
			}
			eaWantStep(t, steps[3], "listen", "", "panel.listener.open/v1", []string{"attach"})
			wantListen := ListenerOpenInput{
				Via: ViaTunnel, Hostname: eaName, HostID: eaHost, Tunnel: eaTunnel, Subnet: eaSubnet, Gateway: eaGateway, Proxy: eaProxy, Port: int(tc.want),
			}
			if got := eaStrict[ListenerOpenInput](t, resolve(3)); got != wantListen {
				t.Errorf("listen input %+v, want %+v", got, wantListen)
			}
			eaWantStep(t, steps[4], "proxy", "", "cloudflare.hostname.add/v1", []string{"listen"})
			wantHostname := cloudflare.HostnameAddInput{
				Tunnel: eaTunnel, Hostname: eaName, Service: "https://" + eaGateway.String() + ":" + strconv.Itoa(int(tc.want)), NoTLSVerify: true,
			}
			if got := eaStrict[cloudflare.HostnameAddInput](t, resolve(4)); got != wantHostname {
				t.Errorf("hostname input %+v, want %+v", got, wantHostname)
			}
			eaPorts(t, steps[5], in, tc.want, "proxy")
			eaCheckGraph(t, steps, in, tc.want)
		})
	}
}

// TestEaLabelFitsTheFirewall: the firewall takes a label of at most 64 characters, and a hostname may be longer, so
// the label is cut and the step stays valid.
func TestEaLabelFitsTheFirewall(t *testing.T) {
	long := strings.Join([]string{strings.Repeat("a", 60), strings.Repeat("b", 60), strings.Repeat("c", 60), "example", "com"}, ".")
	for _, via := range []string{ViaNPM, ViaTunnel} {
		t.Run(via, func(t *testing.T) {
			in := eaNPMInput(0)
			if via == ViaTunnel {
				in = eaTunnelInput(0)
			}
			in.Hostname = long
			steps := eaOK(t, in)
			ports := steps[len(steps)-1]
			resolved := eaResolve(t, ports.Input, in, eaOutputs(DefaultPort))
			label := eaStrict[firewall.RulesApplyInput](t, resolved).Label
			if len(label) > 64 || !strings.HasPrefix(label, "panel exposure ") {
				t.Errorf("label %q (%d characters), want at most 64 characters and the prefix %q", label, len(label), "panel exposure ")
			}
			def, ok := eaDef(t, "firewall.rules.apply")
			if !ok {
				t.Fatal("firewall.rules.apply is not declared")
			}
			if err := schema.NewValidator().ValidateInput(def, resolved); err != nil {
				t.Errorf("ports input is invalid for firewall.rules.apply: %v", err)
			}
		})
	}
}

// TestEaOutput: the output is built from the network and listen steps, with the port the listener binds.
func TestEaOutput(t *testing.T) {
	def := exposureAddAction()
	steps := func(port uint16) map[string]json.RawMessage {
		out := map[string]json.RawMessage{}
		for id, v := range eaOutputs(port) {
			out[id] = eaJSON(t, v)
		}
		out["ports"] = json.RawMessage(`{}`)
		return out
	}
	tests := []struct {
		name string
		in   ExposeInput
		port uint16
		want ExposeOutput
	}{
		{"npm on the default port", eaNPMInput(0), DefaultPort, ExposeOutput{
			ExposureID: eaChange, Via: ViaNPM, Hostname: eaName, HostID: eaHost, Subnet: eaSubnet, Gateway: eaGateway, Proxy: eaProxy, Port: DefaultPort,
		}},
		{"a tunnel on another port", eaTunnelInput(9444), 9444, ExposeOutput{
			ExposureID: eaChange, Via: ViaTunnel, Hostname: eaName, HostID: eaHost, Tunnel: eaTunnel, Subnet: eaSubnet, Gateway: eaGateway, Proxy: eaProxy, Port: 9444,
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, err := def.Handler.Output(context.Background(), core.Env{}, eaJSON(t, tc.in), steps(tc.port))
			if err != nil {
				t.Fatal(err)
			}
			if got := eaStrict[ExposeOutput](t, out); got != tc.want {
				t.Errorf("output %+v, want %+v", got, tc.want)
			}
		})
	}
	in := eaJSON(t, eaNPMInput(0))
	for _, missing := range []string{"network", "listen"} {
		partial := steps(DefaultPort)
		delete(partial, missing)
		if _, err := def.Handler.Output(context.Background(), core.Env{}, in, partial); err == nil || !strings.Contains(err.Error(), missing) {
			t.Errorf("output without the %s step: %v, want an error naming it", missing, err)
		}
	}
}
