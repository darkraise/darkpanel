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
	"github.com/darkraise/darkpanel/internal/modules/tailnet"
)

// cgHost is the DERP host of these tests; the agent-side test host's own id ("h1") is deliberately another.
const cgHost = "h7"

// cgPatch is the input of the region step: the edits the edits step computed, as one reference.
const cgPatch = `{"edits":"${steps.edits.output.edits}"}`

// cgSteps asks def for its steps the way the engine does, through the erased handler, on the controller's database.
func cgSteps(t *testing.T, h *dpHost, def core.ActionDef, in any) ([]core.Step, error) {
	t.Helper()
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	return def.Handler.Steps(context.Background(), h.ctlEnv(""), raw)
}

func cgJSON(t *testing.T, raw json.RawMessage) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("%s: %v", raw, err)
	}
	return v
}

// cgStrict decodes raw into v and refuses a member v does not have.
func cgStrict(t *testing.T, raw json.RawMessage, v any) {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		t.Fatalf("input %s: %v", raw, err)
	}
}

// cgInputRef is a template string that is exactly one reference to a member of the composite's input.
var cgInputRef = regexp.MustCompile(`^\$\{input\.([A-Za-z0-9]+)\}$`)

// cgResolve resolves the ${input.<member>} references of a step template against in as the engine does at plan time:
// a string that is exactly one reference becomes the member's value with its JSON type. A reference to a member the
// input does not hold fails the test, as it fails the plan. ${steps...} references stay.
func cgResolve(t *testing.T, tmpl json.RawMessage, in any) json.RawMessage {
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
			if m := cgInputRef.FindStringSubmatch(x); m != nil {
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

// cgSeed records, in the controller's database, a server on cgHost whose region is regionID (0: not published).
func cgSeed(t *testing.T, h *dpHost, regionID int) {
	t.Helper()
	err := upsertServerRecord(context.Background(), h.ctlDB, serverRecord{
		HostID: cgHost, Mode: "ip", HostName: "203.0.113.9", RegionID: regionID, RegionCode: "dp-eu",
		DerperVersion: "v1.104.1", UnitActive: true, CheckedAt: h.clock.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
}

// cgWantStep fails the test unless s is the step id on host running action after the steps after.
func cgWantStep(t *testing.T, s core.Step, id, host, action string, after []string) {
	t.Helper()
	if s.ID != id || s.Host != host || s.Action.String() != action || !slices.Equal(s.After, after) {
		t.Errorf("step %+v, want %s: %s on host %q after %v", s, id, action, host, after)
	}
}

// cgWantPatch fails the test unless s is the region step: tailnet.policy.patch applying the edits step's edits.
func cgWantPatch(t *testing.T, s core.Step, after []string) {
	t.Helper()
	cgWantStep(t, s, "region", "", "tailnet.policy.patch/v1", after)
	if got, want := cgJSON(t, s.Input), cgJSON(t, json.RawMessage(cgPatch)); !reflect.DeepEqual(got, want) {
		t.Errorf("region input %s, want %s", s.Input, cgPatch)
	}
}

// TestCGDeclaration: the three are controller-side composites that unwind in reverse and take the module's
// coordination lock; the rotation's summary states the window between its steps.
func TestCGDeclaration(t *testing.T) {
	d := newDPHost(t).dp()
	tests := []struct {
		name    string
		def     core.ActionDef
		summary string // "" accepts any non-empty summary
	}{
		{"derp.region.set", d.regionSet(), ""},
		{"derp.region.remove", d.regionRemove(), ""},
		{"derp.cert.rotate", d.certRotate(), "Renew the IP certificate, then publish its pin; between the two steps clients holding the old pin cannot use the region."},
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

func TestCGRegionSetSteps(t *testing.T) {
	tests := []struct {
		name string
		in   RegionSetInput
		want RegionEditsInput
	}{
		{"a region added to the default ones",
			RegionSetInput{HostID: cgHost, RegionID: 900, RegionCode: "dp-eu", RegionName: "Frankfurt"},
			RegionEditsInput{Op: "set", HostID: cgHost, RegionID: 900, RegionCode: "dp-eu", RegionName: "Frankfurt"}},
		{"the default regions omitted with the acknowledgement",
			RegionSetInput{HostID: cgHost, RegionID: 941, RegionCode: "dp-us", RegionName: "Ohio", OmitDefaultRegions: true, Acknowledge: "nodes will relay only through dp-us"},
			RegionEditsInput{Op: "set", HostID: cgHost, RegionID: 941, RegionCode: "dp-us", RegionName: "Ohio", OmitDefaultRegions: true, Acknowledge: "nodes will relay only through dp-us"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newDPHost(t)
			steps, err := cgSteps(t, h, h.dp().regionSet(), tc.in)
			if err != nil {
				t.Fatal(err)
			}
			if len(steps) != 2 {
				t.Fatalf("%d steps, want edits and region", len(steps))
			}
			cgWantStep(t, steps[0], "edits", "", "derp.region.edits/v1", nil)
			cgWantPatch(t, steps[1], []string{"edits"})
			var got RegionEditsInput
			cgStrict(t, cgResolve(t, steps[0].Input, tc.in), &got)
			if got != tc.want {
				t.Errorf("edits input %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestCGRegionSetKeepsInputOutOfTheTemplate: the engine reads a "${" in a step template as a reference, so a name the
// operator typed reaches the edits step only through a reference to the input, never spliced into the template.
func TestCGRegionSetKeepsInputOutOfTheTemplate(t *testing.T) {
	h := newDPHost(t)
	in := RegionSetInput{HostID: cgHost, RegionID: 900, RegionCode: "dp-eu", RegionName: "${steps.edits.output.edits}"}
	steps, err := cgSteps(t, h, h.dp().regionSet(), in)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(steps[0].Input), "steps.edits") {
		t.Errorf("edits input %s holds the typed name, want a reference to the input", steps[0].Input)
	}
	var got RegionEditsInput
	cgStrict(t, cgResolve(t, steps[0].Input, in), &got)
	if got.RegionName != in.RegionName {
		t.Errorf("resolved region name %q, want %q", got.RegionName, in.RegionName)
	}
}

func TestCGRegionRemoveSteps(t *testing.T) {
	h := newDPHost(t)
	in := RegionRemoveInput{RegionID: 907}
	steps, err := cgSteps(t, h, h.dp().regionRemove(), in)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 2 {
		t.Fatalf("%d steps, want edits and region", len(steps))
	}
	cgWantStep(t, steps[0], "edits", "", "derp.region.edits/v1", nil)
	cgWantPatch(t, steps[1], []string{"edits"})
	var got RegionEditsInput
	cgStrict(t, cgResolve(t, steps[0].Input, in), &got)
	if want := (RegionEditsInput{Op: "remove", RegionID: 907}); got != want {
		t.Errorf("edits input %+v, want %+v: the region id alone", got, want)
	}
}

func TestCGCertRotateRefusesAnUnknownServer(t *testing.T) {
	h := newDPHost(t)
	_, err := cgSteps(t, h, h.dp().certRotate(), CertRotateInput{HostID: cgHost})
	var coded *errcode.Error
	if !errors.As(err, &coded) || coded.Code != errcode.DERPNotReady || !strings.Contains(coded.Detail, cgHost) {
		t.Errorf("error %v, want %s naming host %s", err, errcode.DERPNotReady, cgHost)
	}
}

func TestCGCertRotateSteps(t *testing.T) {
	in := CertRotateInput{HostID: cgHost}
	t.Run("a published region", func(t *testing.T) {
		h := newDPHost(t)
		cgSeed(t, h, 905)
		steps, err := cgSteps(t, h, h.dp().certRotate(), in)
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, s := range steps {
			ids = append(ids, s.ID)
		}
		if !slices.Equal(ids, []string{"fallback", "cert", "edits", "region"}) {
			t.Fatalf("steps %v, want fallback, cert, edits, region", ids)
		}
		cgWantStep(t, steps[0], "fallback", "", "derp.fallback.check/v1", nil)
		cgWantStep(t, steps[1], "cert", cgHost, "derp.cert.renew/v1", []string{"fallback"})
		cgWantStep(t, steps[2], "edits", "", "derp.region.edits/v1", []string{"cert"})
		cgWantPatch(t, steps[3], []string{"edits"})
		var fb FallbackCheckInput
		cgStrict(t, cgResolve(t, steps[0].Input, in), &fb)
		if fb != (FallbackCheckInput{HostID: cgHost, RegionID: 905}) {
			t.Errorf("fallback input %+v, want host %s and the record's region 905", fb, cgHost)
		}
		if got := cgJSON(t, steps[1].Input); !reflect.DeepEqual(got, map[string]any{}) {
			t.Errorf("cert input %s, want {}", steps[1].Input)
		}
		var edits RegionEditsInput
		cgStrict(t, cgResolve(t, steps[2].Input, in), &edits)
		if want := (RegionEditsInput{Op: "rotate", HostID: cgHost, RegionID: 905, CertName: "${steps.cert.output.certName}"}); edits != want {
			t.Errorf("edits input %+v, want %+v: the new pin as a reference to the cert step's output", edits, want)
		}
	})
	t.Run("no region", func(t *testing.T) {
		h := newDPHost(t)
		cgSeed(t, h, 0)
		steps, err := cgSteps(t, h, h.dp().certRotate(), in)
		if err != nil {
			t.Fatal(err)
		}
		if len(steps) != 1 {
			t.Fatalf("%d steps, want only cert: nothing is published to check or edit", len(steps))
		}
		cgWantStep(t, steps[0], "cert", cgHost, "derp.cert.renew/v1", nil)
	})
}

// TestCGStepsMatchTheActions: tailnet.policy.patch is a controller-side compensable action with an undo handler, as a
// reverse composite needs, and takes the edits the edits step yields; this module's steps decode into its types.
func TestCGStepsMatchTheActions(t *testing.T) {
	h := newDPHost(t)
	cgSeed(t, h, 905)
	d := h.dp()
	patch := func() core.ActionDef {
		for _, a := range tailnet.Module().Manifest().Actions {
			if a.Spec.Name == "tailnet.policy.patch" && a.Spec.Version == 1 {
				return a
			}
		}
		t.Fatal("tailnet.policy.patch is not declared")
		return core.ActionDef{}
	}()
	if patch.Spec.Side != core.SideController || patch.Spec.Class != core.ClassCompensable || patch.Handler.Inverse == nil {
		t.Fatalf("tailnet.policy.patch is %s-side, %s, undo handler %v; want a controller-side compensable action with an undo handler",
			patch.Spec.Side, patch.Spec.Class, patch.Handler.Inverse != nil)
	}
	validator := schema.NewValidator()
	composites := []struct {
		def core.ActionDef
		in  any
	}{
		{d.regionSet(), RegionSetInput{HostID: cgHost, RegionID: 905, RegionCode: "dp-eu", RegionName: "Frankfurt"}},
		{d.regionRemove(), RegionRemoveInput{RegionID: 905}},
		{d.certRotate(), CertRotateInput{HostID: cgHost}},
	}
	for _, c := range composites {
		steps, err := cgSteps(t, h, c.def, c.in)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range steps {
			input := cgResolve(t, s.Input, c.in)
			switch s.Action.Name {
			case "tailnet.policy.patch":
				filled := json.RawMessage(strings.ReplaceAll(string(input), `"${steps.edits.output.edits}"`, `[]`))
				if err := validator.ValidateInput(patch, filled); err != nil {
					t.Errorf("%s step %s: input %s is invalid for %s: %v", c.def.Spec.Name, s.ID, filled, s.Action, err)
				}
			case "derp.region.edits":
				cgStrict(t, input, new(RegionEditsInput))
			case "derp.fallback.check":
				cgStrict(t, input, new(FallbackCheckInput))
			case "derp.cert.renew":
				cgStrict(t, input, new(struct{}))
			default:
				t.Errorf("%s step %s runs %s, which the composites do not declare", c.def.Spec.Name, s.ID, s.Action)
			}
		}
	}
}

func TestCGRegionOutput(t *testing.T) {
	h := newDPHost(t)
	region := json.RawMessage(`{"etag":"\"abc123\""}`)
	const grant = "the visibility grant of region 900's server stays in the policy: its tag could not be read"
	const setIn = `{"hostId":"h7","regionId":900,"regionCode":"dp-eu","regionName":"Frankfurt"}`
	const removeIn = `{"regionId":900}`
	const removeEdits = `[{"op":"delete","path":"/derpMap/Regions/900"}]`
	tests := []struct {
		name     string
		def      core.ActionDef
		in       string
		edits    string // the edits step's output; "" leaves the step out of the map
		warnings []any
	}{
		{"set without warnings", h.dp().regionSet(), setIn, `{"edits":[],"warnings":[]}`, []any{}},
		{"set with a warning", h.dp().regionSet(), setIn,
			`{"edits":[],"warnings":["the STUN probe of the server got no answer"]}`, []any{"the STUN probe of the server got no answer"}},
		{"remove without warnings", h.dp().regionRemove(), removeIn, `{"edits":` + removeEdits + `,"warnings":[]}`, []any{}},
		{"remove that leaves the grant", h.dp().regionRemove(), removeIn, `{"edits":` + removeEdits + `,"warnings":["` + grant + `"]}`, []any{grant}},
		{"remove whose edits step has a null output", h.dp().regionRemove(), removeIn, `null`, []any{}},
		{"remove without an edits step", h.dp().regionRemove(), removeIn, "", []any{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			steps := map[string]json.RawMessage{"region": region}
			if tc.edits != "" {
				steps["edits"] = json.RawMessage(tc.edits)
			}
			out, err := tc.def.Handler.Output(context.Background(), h.ctlEnv(""), json.RawMessage(tc.in), steps)
			if err != nil {
				t.Fatal(err)
			}
			if got, want := cgJSON(t, out), map[string]any{"etag": `"abc123"`, "warnings": tc.warnings}; !reflect.DeepEqual(got, want) {
				t.Errorf("output %s, want %v", out, want)
			}
			_, err = tc.def.Handler.Output(context.Background(), h.ctlEnv(""), json.RawMessage(tc.in), map[string]json.RawMessage{})
			if err == nil || !strings.Contains(err.Error(), "region") {
				t.Errorf("output without the region step: %v, want an error naming region", err)
			}
			steps["edits"] = json.RawMessage(`"not an object"`)
			_, err = tc.def.Handler.Output(context.Background(), h.ctlEnv(""), json.RawMessage(tc.in), steps)
			if err == nil || !strings.Contains(err.Error(), "edits") {
				t.Errorf("output with an unreadable edits step: %v, want an error naming edits", err)
			}
		})
	}
}

func TestCGCertRotateOutput(t *testing.T) {
	h := newDPHost(t)
	def := h.dp().certRotate()
	in := json.RawMessage(`{"hostId":"h7"}`)
	cert := json.RawMessage(`{"certName":"sha256-raw:aa","certNotAfter":"2027-11-01T12:00:00Z","priorCertName":"sha256-raw:bb"}`)
	out, err := def.Handler.Output(context.Background(), h.ctlEnv(""), in, map[string]json.RawMessage{"cert": cert, "region": json.RawMessage(`{"etag":"x"}`)})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"certName": "sha256-raw:aa", "certNotAfter": "2027-11-01T12:00:00Z"}
	if got := cgJSON(t, out); !reflect.DeepEqual(got, want) {
		t.Errorf("output %s, want %v: the new pin and expiry, not the prior pin", out, want)
	}
	if _, err := def.Handler.Output(context.Background(), h.ctlEnv(""), in, map[string]json.RawMessage{"region": json.RawMessage(`{"etag":"x"}`)}); err == nil || !strings.Contains(err.Error(), "cert") {
		t.Errorf("output without the cert step: %v, want an error naming cert", err)
	}
}
