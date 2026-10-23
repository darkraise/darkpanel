package schema

import (
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/invopop/jsonschema"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// Route is a hand-written API route (spec section 12) that the API package
// passes to OpenAPI. Path uses {name} for path parameters.
type Route struct {
	Method, Path, Summary string
	Public                bool // served without a session or token
}

// The wire shapes of spec section 12 that every action operation shares.
// They mirror the API package's response types, which this package cannot
// import.
type (
	planWire struct {
		PlanID       string          `json:"planId"`
		HostID       *string         `json:"hostId" jsonschema:"nullable"`
		Action       string          `json:"action"`
		Version      int             `json:"version"`
		InputHash    string          `json:"inputHash"`
		Diff         []core.DiffLine `json:"diff"`
		Guards       []core.Guard    `json:"guards"`
		ExpiresAt    time.Time       `json:"expiresAt"`
		Confirmation *string         `json:"confirmation" jsonschema:"nullable"`
	}
	changeErrorWire struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Cause   string `json:"cause,omitempty"`
	}
	changeWire struct {
		ID             string           `json:"id"`
		HostID         *string          `json:"hostId" jsonschema:"nullable"`
		Action         string           `json:"action"`
		Version        int              `json:"version"`
		Class          string           `json:"class" jsonschema:"enum=revertible,enum=compensable,enum=irreversible,enum=composite"`
		State          string           `json:"state" jsonschema:"enum=applying,enum=pending,enum=confirmed,enum=reverted,enum=completed,enum=failed,enum=interrupted,enum=awaiting_confirmation,enum=undoing,enum=needs_repair,enum=recovering,enum=repaired,enum=kept,enum=abandoned,enum=quarantined,enum=releasing"`
		Actor          string           `json:"actor"`
		CreatedAt      time.Time        `json:"createdAt"`
		UpdatedAt      time.Time        `json:"updatedAt"`
		RevertDeadline *time.Time       `json:"revertDeadline" jsonschema:"nullable"`
		Output         json.RawMessage  `json:"output"`
		Error          *changeErrorWire `json:"error" jsonschema:"nullable"`
		CompensatedBy  *string          `json:"compensatedBy" jsonschema:"nullable"`
		ParentChangeID *string          `json:"parentChangeId" jsonschema:"nullable"`
		Owner          *string          `json:"owner" jsonschema:"nullable"`
		RepairedBy     *string          `json:"repairedBy" jsonschema:"nullable"`
		RepairOutcome  *string          `json:"repairOutcome" jsonschema:"nullable"`
		Override       json.RawMessage  `json:"override"`
	}
	applyWire struct {
		PlanID                string `json:"planId"`
		ConfirmTimeoutSeconds int    `json:"confirmTimeoutSeconds,omitempty" jsonschema:"minimum=10,maximum=900"`
		Confirmation          string `json:"confirmation,omitempty"`
		ConfirmNow            bool   `json:"confirmNow,omitempty"`
	}
	problemWire struct {
		Type   string               `json:"type"`
		Title  string               `json:"title"`
		Status int                  `json:"status"`
		Detail string               `json:"detail"`
		Code   string               `json:"code"`
		Errors []errcode.FieldError `json:"errors,omitempty"`
	}
)

var (
	pathParam     = regexp.MustCompile(`\{([A-Za-z][A-Za-z0-9]*)\}`)
	componentChar = regexp.MustCompile(`[^A-Za-z0-9._-]`)
)

const componentRef = "#/components/schemas/"

// OpenAPI returns the OpenAPI 3.1 document served at /api/v1/openapi.json:
// the hand-written routes, one reveal operation per reveal action, one read
// operation per other read action and one plan and one apply operation per
// mutating action, and one get operation per resource. Break-glass actions
// are served only on the controller socket (spec section 10) and Internal
// actions only to the controller itself (spec 1.7); both are left out.
func OpenAPI(reg *core.Registry, routes []Route) ([]byte, error) {
	d := &document{paths: map[string]map[string]any{}, schemas: map[string]any{}}
	shared := []struct {
		name  string
		t     reflect.Type
		input bool
	}{
		{"Plan", reflect.TypeFor[planWire](), false},
		{"Change", reflect.TypeFor[changeWire](), false},
		{"ApplyRequest", reflect.TypeFor[applyWire](), true},
		{"Problem", reflect.TypeFor[problemWire](), false},
	}
	for _, s := range shared {
		d.schemas[s.name] = inline(s.t, s.input)
	}
	for _, r := range routes {
		if err := d.add(r.Method, r.Path, operation(r.Summary, "", r.Public, nil, nil)); err != nil {
			return nil, err
		}
	}
	for _, def := range reg.Actions() {
		if err := d.addAction(def); err != nil {
			return nil, err
		}
	}
	for _, res := range reg.Resources() {
		out, err := Output(res.OutputType)
		if err != nil {
			return nil, err
		}
		ref, err := d.lift(res.Name+".output", out)
		if err != nil {
			return nil, err
		}
		module, name, _ := strings.Cut(res.Name, ".")
		op := operation("Read "+res.Name, res.Name+".get", false, nil, map[string]any{"$ref": ref})
		if err := d.add("GET", "/api/v1/hosts/{host}/resources/"+module+"/"+name, op); err != nil {
			return nil, err
		}
	}
	b, err := json.Marshal(map[string]any{
		"openapi":           "3.1.0",
		"info":              map[string]any{"title": "Darkpanel API", "version": "v1"},
		"jsonSchemaDialect": "https://json-schema.org/draft/2020-12/schema",
		"paths":             d.paths,
		"components": map[string]any{
			"schemas": d.schemas,
			"securitySchemes": map[string]any{
				"session": map[string]any{"type": "apiKey", "in": "cookie", "name": "__Host-darkpanel_session",
					"description": "Session cookie; darkpanel_session on the plain-HTTP loopback listener."},
				"token": map[string]any{"type": "http", "scheme": "bearer", "description": "API token dpt_<id>_<secret>."},
			},
			"responses": map[string]any{
				"Problem": map[string]any{
					"description": "RFC 9457 problem details",
					"content":     map[string]any{"application/problem+json": map[string]any{"schema": map[string]any{"$ref": componentRef + "Problem"}}},
				},
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("schema: openapi: %w", err)
	}
	return b, nil
}

type document struct {
	paths   map[string]map[string]any
	schemas map[string]any
}

func (d *document) add(method, path string, op map[string]any) error {
	m := strings.ToLower(method)
	item := d.paths[path]
	if item == nil {
		item = map[string]any{}
		d.paths[path] = item
	}
	if _, dup := item[m]; dup {
		return fmt.Errorf("schema: openapi: %s %s listed twice", method, path)
	}
	var params []any
	for _, p := range pathParam.FindAllStringSubmatch(path, -1) {
		params = append(params, map[string]any{"name": p[1], "in": "path", "required": true, "schema": map[string]any{"type": "string"}})
	}
	if params != nil {
		op["parameters"] = params
	}
	item[m] = op
	return nil
}

func (d *document) addAction(def core.ActionDef) error {
	s := def.Spec
	if s.BreakGlass || s.Internal {
		return nil
	}
	id := fmt.Sprintf("%s.v%d", s.Name, s.Version)
	in, err := Input(def.InputType)
	if err != nil {
		return err
	}
	out, err := Output(def.OutputType)
	if err != nil {
		return err
	}
	inRef, err := d.lift(id+".input", in)
	if err != nil {
		return err
	}
	outRef, err := d.lift(id+".output", out)
	if err != nil {
		return err
	}
	base := fmt.Sprintf("/api/v1/actions/%s/v%d/", s.Name, s.Version)
	if s.Side == core.SideAgent {
		base = fmt.Sprintf("/api/v1/hosts/{host}/actions/%s/v%d/", s.Name, s.Version)
	}
	inputBody := wrapper("input", inRef, true)
	if s.Reveal {
		return d.add("POST", base+"reveal", operation(s.Summary, id+".reveal", false, inputBody, wrapper("value", outRef, false)))
	}
	if s.Class == core.ClassRead {
		return d.add("POST", base+"read", operation(s.Summary, id+".read", false, inputBody, wrapper("output", outRef, false)))
	}
	if err := d.add("POST", base+"plan", operation(s.Summary, id+".plan", false, inputBody, map[string]any{"$ref": componentRef + "Plan"})); err != nil {
		return err
	}
	return d.add("POST", base+"apply", operation(s.Summary, id+".apply", false,
		map[string]any{"$ref": componentRef + "ApplyRequest"}, map[string]any{"$ref": componentRef + "Change"}))
}

// lift moves a schema from Input or Output into the components under prefix:
// its $defs become "<prefix>.<name>" and its references are rewritten, so the
// document holds no "#/$defs/" reference, which would resolve against the
// document root. It returns the reference to the lifted root.
func (d *document) lift(prefix string, raw json.RawMessage) (string, error) {
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return "", fmt.Errorf("schema: openapi: %w", err)
	}
	name := func(def string) string { return componentChar.ReplaceAllString(prefix+"."+def, "_") }
	defs, _ := root["$defs"].(map[string]any)
	delete(root, "$defs")
	delete(root, "$schema")
	for def, s := range defs {
		d.schemas[name(def)] = rewriteRefs(s, name)
	}
	d.schemas[prefix] = rewriteRefs(root, name)
	return componentRef + prefix, nil
}

func rewriteRefs(v any, name func(string) string) any {
	switch v := v.(type) {
	case map[string]any:
		for k, x := range v {
			if s, ok := x.(string); ok && k == "$ref" {
				if def, ok := strings.CutPrefix(s, "#/$defs/"); ok {
					v[k] = componentRef + name(def)
				}
				continue
			}
			v[k] = rewriteRefs(x, name)
		}
	case []any:
		for i, x := range v {
			v[i] = rewriteRefs(x, name)
		}
	}
	return v
}

// inline reflects a shared wire type into one self-contained schema.
func inline(t reflect.Type, input bool) map[string]any {
	r := &jsonschema.Reflector{Anonymous: true, DoNotReference: true, AllowAdditionalProperties: !input}
	s := r.ReflectFromType(t)
	s.Version = ""
	b, err := json.Marshal(s)
	if err != nil {
		panic(fmt.Sprintf("schema: openapi: %s: %v", t, err)) // fixed types: a failure is a programming error
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		panic(fmt.Sprintf("schema: openapi: %s: %v", t, err))
	}
	return m
}

// wrapper is the {input} request or {output} response body of spec section 12.
func wrapper(field, ref string, request bool) map[string]any {
	w := map[string]any{
		"type":       "object",
		"properties": map[string]any{field: map[string]any{"$ref": ref}},
		"required":   []string{field},
	}
	if request {
		w["additionalProperties"] = false
	}
	return w
}

func operation(summary, id string, public bool, body, ok map[string]any) map[string]any {
	success := map[string]any{"description": "Success"}
	if ok != nil {
		success["content"] = map[string]any{"application/json": map[string]any{"schema": ok}}
	}
	op := map[string]any{
		"responses": map[string]any{"2XX": success, "default": map[string]any{"$ref": "#/components/responses/Problem"}},
		"security":  []any{},
	}
	if !public {
		op["security"] = []any{map[string]any{"session": []any{}}, map[string]any{"token": []any{}}}
	}
	if summary != "" {
		op["summary"] = summary
	}
	if id != "" {
		op["operationId"] = id
	}
	if body != nil {
		op["requestBody"] = map[string]any{
			"required": true,
			"content":  map[string]any{"application/json": map[string]any{"schema": body}},
		}
	}
	return op
}
