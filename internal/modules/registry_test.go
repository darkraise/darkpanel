package modules_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/spf13/cobra"

	"github.com/darkraise/darkpanel/internal/cli"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/schema"
)

// The registration rules of spec section 2, written out here rather than
// taken from internal/core, so that a rule weakened there fails this test.
var (
	moduleIDRule   = regexp.MustCompile(`^[a-z][a-z0-9]*$`)
	actionNameRule = regexp.MustCompile(`^[a-z][a-z0-9]*\.[a-z][a-z0-9]*\.[a-z][a-z0-9-]*$`)
	resourceRule   = regexp.MustCompile(`^[a-z][a-z0-9]*\.[a-z][a-z0-9-]*$`)
	reservedCLI    = []string{
		"serve", "agent", "update", "user", "controller", "change", "uninstall", "version",
		"host", "module", "token", "audit", "help", "completion", "system",
	}
)

// TestRegistrationRules checks every rule of spec section 2 over the real
// registry: module ids, action and resource names, unique versions,
// compensating actions, and the sides of break-glass and revertible actions.
func TestRegistrationRules(t *testing.T) {
	reg := built(t)
	names := map[string]bool{}
	for _, def := range reg.Actions() {
		names[def.Spec.Name] = true
	}
	ids := map[string]bool{}
	for _, m := range reg.Modules() {
		switch {
		case !moduleIDRule.MatchString(m.ID):
			t.Errorf("module id %q does not match %s", m.ID, moduleIDRule)
		case slices.Contains(reservedCLI, m.ID):
			t.Errorf("module id %q is a reserved CLI name", m.ID)
		case ids[m.ID]:
			t.Errorf("module id %q is registered twice", m.ID)
		}
		ids[m.ID] = true
		refs := map[core.ActionRef]bool{}
		for _, a := range m.Actions {
			s := a.Spec
			if !actionNameRule.MatchString(s.Name) || !strings.HasPrefix(s.Name, m.ID+".") {
				t.Errorf("action %s: name must match %s and start with %s.", s.Name, actionNameRule, m.ID)
			}
			if refs[a.Ref()] {
				t.Errorf("action %s is registered twice", a.Ref())
			}
			refs[a.Ref()] = true
			if s.Class == core.ClassCompensable && !names[s.CompensatedBy] {
				t.Errorf("action %s: compensating action %q is not registered", a.Ref(), s.CompensatedBy)
			}
			if s.BreakGlass && s.Side != core.SideController {
				t.Errorf("action %s: break-glass but %s-side", a.Ref(), s.Side)
			}
			if s.Class == core.ClassRevertible && s.Side != core.SideAgent {
				t.Errorf("action %s: revertible but %s-side", a.Ref(), s.Side)
			}
			if def, ok := reg.Action(a.Ref()); !ok || def.Spec.Name != s.Name {
				t.Errorf("action %s of module %s is not in the registry", a.Ref(), m.ID)
			}
		}
		for i, res := range m.Resources {
			if !resourceRule.MatchString(res.Name) || !strings.HasPrefix(res.Name, m.ID+".") {
				t.Errorf("resource %s: name must match %s and start with %s.", res.Name, resourceRule, m.ID)
			}
			if slices.ContainsFunc(m.Resources[:i], func(r core.ResourceDef) bool { return r.Name == res.Name }) {
				t.Errorf("resource %s is registered twice", res.Name)
			}
			if _, ok := reg.Resource(res.Name); !ok {
				t.Errorf("resource %s of module %s is not in the registry", res.Name, m.ID)
			}
		}
	}
	if !ids[core.ModuleIDCore] || reg.Modules()[0].ID != core.ModuleIDCore {
		t.Errorf("the core module is not registered first")
	}
}

// TestEveryActionHasACLICommand is spec section 10's parity test (register
// row 3): the CLI built from the real registry, as cmd/darkpanel builds it,
// has a generated command for every action, mutating or read, and for every
// resource.
func TestEveryActionHasACLICommand(t *testing.T) {
	reg := built(t)
	root, err := cli.NewRoot(cli.Options{Registry: reg, Commands: []*cobra.Command{{Use: "serve"}, {Use: "version"}}})
	if err != nil {
		t.Fatalf("cli.NewRoot: %v", err)
	}
	mutating := 0
	for _, def := range reg.Actions() {
		if def.Spec.Class != core.ClassRead {
			mutating++
		}
		if _, ok := cli.FindCommand(root, def.Spec.Name); !ok {
			t.Errorf("action %s has no CLI command darkpanel %s", def.Ref(), strings.Join(cli.CommandPath(def.Spec.Name), " "))
		}
	}
	for _, res := range reg.Resources() {
		if _, ok := cli.FindCommand(root, res.Name); !ok {
			t.Errorf("resource %s has no CLI command darkpanel %s", res.Name, strings.Join(cli.CommandPath(res.Name), " "))
		}
	}
	if mutating == 0 {
		t.Error("the registry has no mutating action, so parity proved nothing")
	}
}

// TestPointerOutputsAreNullable is register row 70's guard: a pointer field
// encodes null, so every pointer field of a registered output type must be
// published with a schema that accepts null (the jsonschema:"nullable"
// tag).
func TestPointerOutputsAreNullable(t *testing.T) {
	reg := built(t)
	var problems []string
	for _, def := range reg.Actions() {
		problems = append(problems, nullableProblems(t, def.Ref().String(), def.OutputType)...)
	}
	for _, res := range reg.Resources() {
		problems = append(problems, nullableProblems(t, res.Name, res.OutputType)...)
	}
	for _, p := range problems {
		t.Error(p)
	}
}

// TestNullableGuardFindsUntaggedPointers proves the guard on a type with an
// untagged pointer at the top and inside a list.
func TestNullableGuardFindsUntaggedPointers(t *testing.T) {
	type item struct {
		Note *int `json:"note"`
	}
	type probe struct {
		Bare   *string  `json:"bare"`
		Tagged *string  `json:"tagged" jsonschema:"nullable"`
		Items  []item   `json:"items"`
		Nested *[]item  `json:"nested" jsonschema:"nullable"`
		Plain  []string `json:"plain"`
	}
	got := nullableProblems(t, "probe", reflect.TypeFor[probe]())
	want := []string{
		"probe: output field bare is a pointer but its schema does not accept null; tag it jsonschema:\"nullable\"",
		"probe: output field items[].note is a pointer but its schema does not accept null; tag it jsonschema:\"nullable\"",
		"probe: output field nested[].note is a pointer but its schema does not accept null; tag it jsonschema:\"nullable\"",
	}
	if !slices.Equal(got, want) {
		t.Errorf("problems:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// nullableProblems walks output type typ together with its published schema
// and describes every pointer field whose property does not accept null.
func nullableProblems(t *testing.T, owner string, typ reflect.Type) []string {
	t.Helper()
	raw, err := schema.Output(typ)
	if err != nil {
		t.Fatalf("%s: schema.Output: %v", owner, err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("%s: %v", owner, err)
	}
	defs, _ := doc["$defs"].(map[string]any)
	w := &nullWalker{t: t, owner: owner, defs: defs}
	w.walk(typ, doc, "")
	return w.problems
}

type nullWalker struct {
	t        *testing.T
	owner    string
	defs     map[string]any
	problems []string
}

// resolve follows "#/$defs/<name>" references.
func (w *nullWalker) resolve(s map[string]any) map[string]any {
	for range 32 {
		name, ok := strings.CutPrefix(fmt.Sprint(s["$ref"]), "#/$defs/")
		if !ok {
			return s
		}
		s, _ = w.defs[name].(map[string]any)
	}
	return s
}

// branches returns the alternatives of s that are not the null type: the
// members of its oneOf or anyOf, or s itself.
func (w *nullWalker) branches(s map[string]any) []map[string]any {
	s = w.resolve(s)
	var out []map[string]any
	for _, key := range []string{"oneOf", "anyOf"} {
		list, _ := s[key].([]any)
		for _, b := range list {
			if m, ok := b.(map[string]any); ok && m["type"] != "null" {
				out = append(out, w.branches(m)...)
			}
		}
	}
	if len(out) == 0 && s != nil {
		out = append(out, s)
	}
	return out
}

// acceptsNull compiles the property schema s with the document's $defs, as
// the production validator compiles schemas, and validates null.
func (w *nullWalker) acceptsNull(s map[string]any) bool {
	doc := map[string]any{"$defs": w.defs}
	for k, v := range s {
		doc[k] = v
	}
	b, err := json.Marshal(doc)
	if err != nil {
		w.t.Fatal(err)
	}
	parsed, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil {
		w.t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("urn:darkpanel:property", parsed); err != nil {
		w.t.Fatal(err)
	}
	sch, err := c.Compile("urn:darkpanel:property")
	if err != nil {
		w.t.Fatalf("%s: compile a property schema: %v", w.owner, err)
	}
	return sch.Validate(nil) == nil
}

func (w *nullWalker) walk(typ reflect.Type, s map[string]any, path string) {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	for _, b := range w.branches(s) {
		switch typ.Kind() {
		case reflect.Slice, reflect.Array:
			if items, ok := b["items"].(map[string]any); ok {
				w.walk(typ.Elem(), items, strings.TrimSuffix(path, ".")+"[].")
			}
		case reflect.Map:
			if values, ok := b["additionalProperties"].(map[string]any); ok {
				w.walk(typ.Elem(), values, strings.TrimSuffix(path, ".")+"{}.")
			}
			patterns, _ := b["patternProperties"].(map[string]any) // integer-keyed maps
			for _, p := range patterns {
				if values, ok := p.(map[string]any); ok {
					w.walk(typ.Elem(), values, strings.TrimSuffix(path, ".")+"{}.")
				}
			}
		case reflect.Struct:
			if props, ok := b["properties"].(map[string]any); ok {
				w.fields(typ, props, path)
			} // otherwise a type with its own representation, such as time.Time
		}
	}
}

func (w *nullWalker) fields(typ reflect.Type, props map[string]any, path string) {
	for f := range typ.Fields() {
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		ft := f.Type
		for ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}
		switch {
		case name == "-":
			continue
		case f.Anonymous && name == "" && ft.Kind() == reflect.Struct:
			w.fields(ft, props, path) // embedded fields are inlined
			continue
		case !f.IsExported():
			continue
		case name == "":
			name = f.Name
		}
		p, ok := props[name].(map[string]any)
		if !ok {
			continue
		}
		if f.Type.Kind() == reflect.Pointer && !w.acceptsNull(p) {
			w.problems = append(w.problems, fmt.Sprintf(
				"%s: output field %s%s is a pointer but its schema does not accept null; tag it jsonschema:\"nullable\"",
				w.owner, path, name))
		}
		w.walk(f.Type, p, path+name+".")
	}
}
