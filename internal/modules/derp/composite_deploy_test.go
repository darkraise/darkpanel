package derp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/modules/derp/artifact"
	"github.com/darkraise/darkpanel/internal/modules/derp/server"
	"github.com/darkraise/darkpanel/internal/modules/firewall"
	"github.com/darkraise/darkpanel/internal/modules/firewall/model"
	"github.com/darkraise/darkpanel/internal/modules/tailscale"
)

// cdHost is the DERP host of these tests; the agent-side test host's own id ("h1") is deliberately another.
const cdHost = "h7"

func cdBool(b bool) *bool { return &b }

// cdSteps asks def for its steps the way the engine does, through the erased handler, on the controller's database.
func cdSteps(t *testing.T, h *dpHost, def core.ActionDef, in any) ([]core.Step, error) {
	t.Helper()
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	return def.Handler.Steps(context.Background(), h.ctlEnv(""), raw)
}

func cdJSON(t *testing.T, raw json.RawMessage) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("%s: %v", raw, err)
	}
	return v
}

// cdInputRef is a template string that is exactly one reference to a member of the composite's input.
var cdInputRef = regexp.MustCompile(`^\$\{input\.([A-Za-z0-9]+)\}$`)

// cdResolve resolves the ${input.<member>} references of a step template against in as the engine does at plan time:
// a string that is exactly one reference becomes the member's value with its JSON type. A reference to a member the
// input does not hold fails the test, as it fails the plan.
func cdResolve(t *testing.T, tmpl json.RawMessage, in any) json.RawMessage {
	t.Helper()
	rawIn, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var input map[string]any
	if err := json.Unmarshal(rawIn, &input); err != nil {
		t.Fatal(err)
	}
	var walk func(v any) any
	walk = func(v any) any {
		switch x := v.(type) {
		case string:
			if m := cdInputRef.FindStringSubmatch(x); m != nil {
				val, ok := input[m[1]]
				if !ok {
					t.Errorf("template %s references input.%s, which the input does not hold", tmpl, m[1])
				}
				return val
			}
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

// cdStrict decodes raw into v and refuses a member v does not have.
func cdStrict(t *testing.T, raw json.RawMessage, v any) {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		t.Fatalf("input %s: %v", raw, err)
	}
}

// cdAction returns the declaration of action name, version 1, of module m.
func cdAction(t *testing.T, m core.Module, name string) core.ActionDef {
	t.Helper()
	for _, a := range m.Manifest().Actions {
		if a.Spec.Name == name && a.Spec.Version == 1 {
			return a
		}
	}
	t.Fatalf("%s is not declared", name)
	return core.ActionDef{}
}

// cdSeed records, in the controller's database, a server on cdHost whose region is regionID (0: not published).
func cdSeed(t *testing.T, h *dpHost, regionID int) {
	t.Helper()
	err := upsertServerRecord(context.Background(), h.ctlDB, serverRecord{
		HostID: cdHost, Mode: server.ModeIP, HostName: "203.0.113.9", RegionID: regionID, RegionCode: "dp-eu",
		DerperVersion: "v1.103.0", UnitActive: true, CheckedAt: h.clock.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
}

// cdWantStep fails the test unless s is the step id on host running action after the steps after.
func cdWantStep(t *testing.T, s core.Step, id, host, action string, after []string) {
	t.Helper()
	if s.ID != id || s.Host != host || s.Action.String() != action || !slices.Equal(s.After, after) {
		t.Errorf("step %+v, want %s: %s on host %q after %v", s, id, action, host, after)
	}
}

// TestCDDeclaration: both are controller-side composites that unwind in reverse and take the module's coordination lock;
// the upgrade's summary states the window between its steps, because a composite plan has no diff line of its own.
func TestCDDeclaration(t *testing.T) {
	d := newDPHost(t).dp()
	tests := []struct {
		name    string
		def     core.ActionDef
		summary string // "" accepts any non-empty summary
	}{
		{"derp.server.deploy", d.serverDeploy(), ""},
		{"derp.server.upgrade", d.serverUpgrade(), "Pin tailscaled, then replace derper; between the two steps the versions differ and the region may refuse clients."},
	}
	for _, tc := range tests {
		s := tc.def.Spec
		if s.Name != tc.name || s.Version != 1 || s.Summary == "" || s.Class != core.ClassComposite || s.Side != core.SideController ||
			s.Unwind != core.UnwindReverse || !slices.Equal(s.LockKeys, []string{"composite:derp"}) {
			t.Errorf("%s: spec %+v, want version 1, a summary, a controller-side composite, unwind reverse, lock composite:derp", tc.name, s)
		}
		if tc.summary != "" && s.Summary != tc.summary {
			t.Errorf("%s: summary %q, want %q", tc.name, s.Summary, tc.summary)
		}
		if tc.def.Handler.Steps == nil || tc.def.Handler.Output == nil {
			t.Errorf("%s: the handler lacks Steps or Output", tc.name)
		}
	}
}

func TestCDDeploySteps(t *testing.T) {
	tests := []struct {
		name    string
		in      DeployInput
		install map[string]any
	}{
		{"letsencrypt, verification left to the default", DeployInput{HostID: cdHost, Mode: "letsencrypt", HostName: "derp.example.com"},
			map[string]any{"mode": "letsencrypt", "hostName": "derp.example.com"}},
		{"ip, verification off", DeployInput{HostID: cdHost, Mode: "ip", HostName: "203.0.113.9", VerifyClients: cdBool(false)},
			map[string]any{"mode": "ip", "hostName": "203.0.113.9", "verifyClients": false}},
		{"ip, verification asked for", DeployInput{HostID: cdHost, Mode: "ip", HostName: "203.0.113.9", VerifyClients: cdBool(true)},
			map[string]any{"mode": "ip", "hostName": "203.0.113.9", "verifyClients": true}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newDPHost(t)
			steps, err := cdSteps(t, h, h.dp().serverDeploy(), tc.in)
			if err != nil {
				t.Fatal(err)
			}
			if len(steps) != 3 {
				t.Fatalf("%d steps, want tailscaled, install and ports", len(steps))
			}
			cdWantStep(t, steps[0], "tailscaled", cdHost, "tailscale.package.pin/v1", nil)
			cdWantStep(t, steps[1], "install", cdHost, "derp.server.install/v1", []string{"tailscaled"})
			cdWantStep(t, steps[2], "ports", cdHost, "firewall.rules.apply/v1", []string{"install"})
			if got, want := cdJSON(t, steps[0].Input), map[string]any{"version": artifact.PinVersion()}; !reflect.DeepEqual(got, want) {
				t.Errorf("pin input %s, want %v: tailscaled held at the derper version without its leading v", steps[0].Input, want)
			}
			if got := cdJSON(t, cdResolve(t, steps[1].Input, tc.in)); !reflect.DeepEqual(got, tc.install) {
				t.Errorf("install input %s, want %v", steps[1].Input, tc.install)
			}
			var ports struct {
				Label string       `json:"label"`
				Add   []model.Rule `json:"add"`
			}
			cdStrict(t, steps[2].Input, &ports)
			if ports.Label != "derp" || !reflect.DeepEqual(ports.Add, server.PortRules(cdHost)) {
				t.Errorf("ports input %s, want label derp and the three rules of host %s, 80/tcp, 443/tcp and 3478/udp", steps[2].Input, cdHost)
			}
		})
	}
}

// TestCDDeployKeepsInputOutOfTheTemplate: the engine reads a "${" in a step template as a reference, so a value the
// operator typed reaches the install step only through a reference to the input, never spliced into the template.
func TestCDDeployKeepsInputOutOfTheTemplate(t *testing.T) {
	h := newDPHost(t)
	in := DeployInput{HostID: cdHost, Mode: "letsencrypt", HostName: "${steps.tailscaled.output.version}"}
	steps, err := cdSteps(t, h, h.dp().serverDeploy(), in)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(steps[1].Input), "steps.tailscaled") {
		t.Errorf("install input %s holds the typed host name, want a reference to the input", steps[1].Input)
	}
	var got ServerInstallInput
	cdStrict(t, cdResolve(t, steps[1].Input, in), &got)
	if got.HostName != in.HostName {
		t.Errorf("resolved host name %q, want %q", got.HostName, in.HostName)
	}
}

func TestCDUpgradeRefusesAnUnknownServer(t *testing.T) {
	h := newDPHost(t)
	_, err := cdSteps(t, h, h.dp().serverUpgrade(), UpgradeInput{HostID: cdHost})
	var coded *errcode.Error
	if !errors.As(err, &coded) || coded.Code != errcode.DERPNotReady || !strings.Contains(coded.Detail, cdHost) {
		t.Errorf("error %v, want %s naming host %s", err, errcode.DERPNotReady, cdHost)
	}
}

func TestCDUpgradeSteps(t *testing.T) {
	tests := []struct {
		name     string
		region   int
		ids      []string
		pinAfter []string
	}{
		{"a published region is checked for a fallback first", 900, []string{"fallback", "tailscaled", "binary"}, []string{"fallback"}},
		{"no region needs no fallback", 0, []string{"tailscaled", "binary"}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newDPHost(t)
			cdSeed(t, h, tc.region)
			steps, err := cdSteps(t, h, h.dp().serverUpgrade(), UpgradeInput{HostID: cdHost})
			if err != nil {
				t.Fatal(err)
			}
			var ids []string
			for _, s := range steps {
				ids = append(ids, s.ID)
			}
			if !slices.Equal(ids, tc.ids) {
				t.Fatalf("steps %v, want %v", ids, tc.ids)
			}
			if tc.region != 0 {
				cdWantStep(t, steps[0], "fallback", "", "derp.fallback.check/v1", nil)
				var fb FallbackCheckInput
				cdStrict(t, cdResolve(t, steps[0].Input, UpgradeInput{HostID: cdHost}), &fb)
				if fb != (FallbackCheckInput{HostID: cdHost, RegionID: tc.region}) {
					t.Errorf("fallback input %+v, want host %s and region %d", fb, cdHost, tc.region)
				}
				steps = steps[1:]
			}
			cdWantStep(t, steps[0], "tailscaled", cdHost, "tailscale.package.pin/v1", tc.pinAfter)
			cdWantStep(t, steps[1], "binary", cdHost, "derp.binary.update/v1", []string{"tailscaled"})
			if got, want := cdJSON(t, steps[0].Input), map[string]any{"version": artifact.PinVersion()}; !reflect.DeepEqual(got, want) {
				t.Errorf("pin input %s, want %v", steps[0].Input, want)
			}
			var bin BinaryUpdateInput
			cdStrict(t, steps[1].Input, &bin)
			if bin.Version != artifact.DerperVersion {
				t.Errorf("binary input %s, want version %s", steps[1].Input, artifact.DerperVersion)
			}
		})
	}
}

// TestCDStepsMatchTheActions: the steps obey the rules the engine checks at plan time. A step that runs another
// module's action is an agent action on a host, has an undo handler when it is compensable (the composites unwind in
// reverse), the one revertible step is the last, and its input, with the input references resolved, is valid for the
// action's schema; a step that runs one of this module's actions has an input that decodes into the action's type.
func TestCDStepsMatchTheActions(t *testing.T) {
	h := newDPHost(t)
	cdSeed(t, h, 900)
	d := h.dp()
	deployIn := DeployInput{HostID: cdHost, Mode: "ip", HostName: "203.0.113.9", VerifyClients: cdBool(false)}
	deploy, err := cdSteps(t, h, d.serverDeploy(), deployIn)
	if err != nil {
		t.Fatal(err)
	}
	upgradeIn := UpgradeInput{HostID: cdHost}
	upgrade, err := cdSteps(t, h, d.serverUpgrade(), upgradeIn)
	if err != nil {
		t.Fatal(err)
	}
	foreign := map[string]core.ActionDef{
		"tailscale.package.pin": cdAction(t, tailscale.Module(), "tailscale.package.pin"),
		"firewall.rules.apply":  cdAction(t, firewall.Module(), "firewall.rules.apply"),
	}
	validator := schema.NewValidator()
	check := func(s core.Step, input json.RawMessage) {
		if def, ok := foreign[s.Action.Name]; ok {
			switch {
			case def.Spec.Side != core.SideAgent || s.Host == "":
				t.Errorf("step %s: %s is %s-side and the step names host %q, want an agent action on a host", s.ID, s.Action, def.Spec.Side, s.Host)
			case def.Spec.Class == core.ClassCompensable && def.Handler.Inverse == nil:
				t.Errorf("step %s: %s has no undo handler, which a reverse composite needs", s.ID, s.Action)
			case def.Spec.Class != core.ClassCompensable && def.Spec.Class != core.ClassRevertible:
				t.Errorf("step %s: %s is %s, which a step of these composites never is", s.ID, s.Action, def.Spec.Class)
			}
			if err := validator.ValidateInput(def, input); err != nil {
				t.Errorf("step %s: input %s is invalid for %s: %v", s.ID, input, s.Action, err)
			}
			return
		}
		switch s.Action.Name {
		case "derp.server.install":
			cdStrict(t, input, new(ServerInstallInput))
		case "derp.fallback.check":
			cdStrict(t, input, new(FallbackCheckInput))
		case "derp.binary.update":
			var in BinaryUpdateInput
			cdStrict(t, input, &in)
			if !regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(in.Version) {
				t.Errorf("binary version %q does not match the action's pattern", in.Version)
			}
		default:
			t.Errorf("step %s runs %s, which the composites do not declare", s.ID, s.Action)
		}
	}
	for _, s := range deploy {
		check(s, cdResolve(t, s.Input, deployIn))
	}
	for _, s := range upgrade {
		check(s, cdResolve(t, s.Input, upgradeIn))
	}
	if last := deploy[len(deploy)-1]; last.Action.Name != "firewall.rules.apply" || foreign["firewall.rules.apply"].Spec.Class != core.ClassRevertible {
		t.Errorf("deploy ends with %s, want the revertible firewall.rules.apply, the one step that may be revertible, last", last.Action)
	}
}

func TestCDDeployOutput(t *testing.T) {
	h := newDPHost(t)
	def := h.dp().serverDeploy()
	in := json.RawMessage(`{"hostId":"h7","mode":"ip","hostName":"203.0.113.9"}`)
	tests := []struct {
		name    string
		install string
		want    map[string]any
	}{
		{"ip", `{"derperVersion":"v1.104.1","certName":"sha256-raw:ab","certNotAfter":"2027-11-01T12:00:00Z"}`,
			map[string]any{"derperVersion": "v1.104.1", "certName": "sha256-raw:ab", "certNotAfter": "2027-11-01T12:00:00Z"}},
		{"letsencrypt", `{"derperVersion":"v1.104.1","certName":"","certNotAfter":null}`,
			map[string]any{"derperVersion": "v1.104.1", "certName": "", "certNotAfter": nil}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			steps := map[string]json.RawMessage{"tailscaled": json.RawMessage(`{"version":"1.104.1"}`), "install": json.RawMessage(tc.install)}
			out, err := def.Handler.Output(context.Background(), h.ctlEnv(""), in, steps)
			if err != nil {
				t.Fatal(err)
			}
			if got := cdJSON(t, out); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("output %s, want %v", out, tc.want)
			}
		})
	}
	steps := map[string]json.RawMessage{"ports": json.RawMessage(`{}`)}
	if _, err := def.Handler.Output(context.Background(), h.ctlEnv(""), in, steps); err == nil || !strings.Contains(err.Error(), "install") {
		t.Errorf("output without the install step: %v, want an error naming install", err)
	}
}

func TestCDUpgradeOutput(t *testing.T) {
	h := newDPHost(t)
	def := h.dp().serverUpgrade()
	in := json.RawMessage(`{"hostId":"h7"}`)
	steps := map[string]json.RawMessage{"tailscaled": json.RawMessage(`{"version":"1.104.1"}`), "binary": json.RawMessage(`{"from":"v1.103.0","to":"v1.104.1"}`)}
	out, err := def.Handler.Output(context.Background(), h.ctlEnv(""), in, steps)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := cdJSON(t, out), map[string]any{"from": "v1.103.0", "to": "v1.104.1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("output %s, want %v", out, want)
	}
	if _, err := def.Handler.Output(context.Background(), h.ctlEnv(""), in, map[string]json.RawMessage{"tailscaled": steps["tailscaled"]}); err == nil || !strings.Contains(err.Error(), "binary") {
		t.Errorf("output without the binary step: %v, want an error naming binary", err)
	}
}
