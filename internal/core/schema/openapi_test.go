package schema

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/darkraise/darkpanel/internal/core"
)

var testRoutes = []Route{
	{Method: "GET", Path: "/healthz", Summary: "Health", Public: true},
	{Method: "POST", Path: "/api/v1/auth/login", Summary: "Log in", Public: true},
	{Method: "GET", Path: "/api/v1/changes/{id}", Summary: "Show a change"},
	{Method: "POST", Path: "/api/v1/changes/{id}/confirm", Summary: "Confirm a change"},
}

func openAPIDoc(t *testing.T) (map[string]any, []byte) {
	t.Helper()
	b, err := OpenAPI(fixtureRegistry(t), testRoutes)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("document is not JSON: %v", err)
	}
	return doc, b
}

func findOperation(t *testing.T, doc map[string]any, method, path string) map[string]any {
	t.Helper()
	item, ok := doc["paths"].(map[string]any)[path].(map[string]any)
	if !ok {
		t.Fatalf("no path %s", path)
	}
	op, ok := item[method].(map[string]any)
	if !ok {
		t.Fatalf("no %s %s", method, path)
	}
	return op
}

func TestOpenAPIStructure(t *testing.T) {
	doc, _ := openAPIDoc(t)
	if doc["openapi"] != "3.1.0" {
		t.Errorf("openapi = %v", doc["openapi"])
	}
	if info, _ := doc["info"].(map[string]any); info["title"] != "Darkpanel API" || info["version"] != "v1" {
		t.Errorf("info = %v", doc["info"])
	}
	components, _ := doc["components"].(map[string]any)
	schemes, _ := components["securitySchemes"].(map[string]any)
	if _, ok := schemes["session"]; !ok {
		t.Error("no session security scheme")
	}
	if token, _ := schemes["token"].(map[string]any); token["scheme"] != "bearer" {
		t.Errorf("token scheme = %v", schemes["token"])
	}
	for _, name := range []string{"Plan", "Change", "ApplyRequest", "Problem"} {
		if _, ok := components["schemas"].(map[string]any)[name]; !ok {
			t.Errorf("no component schema %s", name)
		}
	}
}

func TestOpenAPIRoutes(t *testing.T) {
	doc, _ := openAPIDoc(t)
	if sec, ok := findOperation(t, doc, "get", "/healthz")["security"].([]any); !ok || len(sec) != 0 {
		t.Errorf("public route security = %v, want []", sec)
	}
	op := findOperation(t, doc, "get", "/api/v1/changes/{id}")
	if sec, _ := op["security"].([]any); len(sec) != 2 {
		t.Errorf("protected route security = %v, want session and token", sec)
	}
	params, _ := op["parameters"].([]any)
	if len(params) != 1 || params[0].(map[string]any)["name"] != "id" || params[0].(map[string]any)["in"] != "path" {
		t.Errorf("parameters = %v, want the path parameter id", params)
	}
	findOperation(t, doc, "post", "/api/v1/changes/{id}/confirm")
}

func TestOpenAPIActions(t *testing.T) {
	doc, raw := openAPIDoc(t)
	for _, want := range []struct{ method, path string }{
		{"post", "/api/v1/hosts/{host}/actions/demo.item.get/v1/read"},
		{"post", "/api/v1/hosts/{host}/actions/demo.item.add/v1/plan"},
		{"post", "/api/v1/hosts/{host}/actions/demo.item.add/v1/apply"},
		{"post", "/api/v1/hosts/{host}/actions/demo.item.purge/v1/plan"},
		{"post", "/api/v1/actions/demo.item.remove/v1/plan"},
		{"post", "/api/v1/actions/demo.item.remove/v1/apply"},
		{"get", "/api/v1/hosts/{host}/resources/demo/status"},
	} {
		op := findOperation(t, doc, want.method, want.path)
		if sec, _ := op["security"].([]any); len(sec) != 2 {
			t.Errorf("%s %s security = %v", want.method, want.path, sec)
		}
	}
	if _, ok := doc["paths"].(map[string]any)["/api/v1/hosts/{host}/actions/demo.item.get/v1/plan"]; ok {
		t.Error("a read action has a plan operation")
	}
	if bytes.Contains(raw, []byte("demo.secret.reset")) {
		t.Error("the break-glass action appears in the document")
	}
	if bytes.Contains(raw, []byte(`#/$defs/`)) {
		t.Error("a $defs reference was not rewritten")
	}
	in := doc["components"].(map[string]any)["schemas"].(map[string]any)["demo.item.add.v1.input.fixtureInput"].(map[string]any)
	pw := in["properties"].(map[string]any)["password"].(map[string]any)
	if pw["x-secret"] != true || pw["writeOnly"] != true {
		t.Errorf("password = %v, want x-secret and writeOnly", pw)
	}
}

// TestOpenAPIRefsResolve checks that every $ref names a component that exists.
func TestOpenAPIRefsResolve(t *testing.T) {
	doc, _ := openAPIDoc(t)
	components := doc["components"].(map[string]any)
	var walk func(v any)
	walk = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			if ref, ok := v["$ref"].(string); ok {
				parts := strings.Split(strings.TrimPrefix(ref, "#/components/"), "/")
				group, _ := components[parts[0]].(map[string]any)
				if !strings.HasPrefix(ref, "#/components/") || len(parts) != 2 || group[parts[1]] == nil {
					t.Errorf("unresolved $ref %s", ref)
				}
			}
			for _, x := range v {
				walk(x)
			}
		case []any:
			for _, x := range v {
				walk(x)
			}
		}
	}
	walk(doc)
}

// TestOpenAPISchemasCompile compiles every component schema as draft 2020-12
// inside the whole document, which also resolves every reference.
func TestOpenAPISchemasCompile(t *testing.T) {
	_, raw := openAPIDoc(t)
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	if err := c.AddResource("urn:darkpanel:openapi", doc); err != nil {
		t.Fatal(err)
	}
	schemas := doc.(map[string]any)["components"].(map[string]any)["schemas"].(map[string]any)
	for name := range schemas {
		if _, err := c.Compile("urn:darkpanel:openapi#/components/schemas/" + name); err != nil {
			t.Errorf("component %s: %v", name, err)
		}
	}
}

func TestOpenAPIDeterministic(t *testing.T) {
	_, a := openAPIDoc(t)
	_, b := openAPIDoc(t)
	if !bytes.Equal(a, b) {
		t.Error("two builds differ")
	}
}

func TestOpenAPIRefusesDuplicateRoutes(t *testing.T) {
	routes := append([]Route{{Method: "GET", Path: "/healthz"}}, testRoutes...)
	if _, err := OpenAPI(fixtureRegistry(t), routes); err == nil {
		t.Error("OpenAPI accepted a route listed twice")
	}
}

type orvIn struct {
	Name string `json:"name"`
}

type orvOut struct {
	Password string `json:"password" secret:"true"`
}

func orvValue(context.Context, core.Env, orvIn) (core.Revealed[orvOut], error) {
	return core.Revealed[orvOut]{}, nil
}

// orvModule has an agent reveal action and an Internal read on each side.
type orvModule struct{}

func (orvModule) Manifest() core.Manifest {
	return core.Manifest{ID: "orv", Version: 1, Actions: []core.ActionDef{
		core.DefineReveal(core.ActionSpec{Name: "orv.key.reveal", Version: 1, Side: core.SideAgent, MinScope: core.ScopeAdmin,
			Summary: "Reveal the key"}, orvValue),
		core.DefineInternalRead(core.ActionSpec{Name: "orv.cert.get", Version: 1, Side: core.SideAgent}, orvValue),
		core.DefineInternalRead(core.ActionSpec{Name: "orv.ctl.get", Version: 1, Side: core.SideController}, orvValue),
	}}
}

func TestOpenAPIReveal(t *testing.T) {
	reg, err := core.NewRegistry(orvModule{})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := OpenAPI(reg, testRoutes)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	op := findOperation(t, doc, "post", "/api/v1/hosts/{host}/actions/orv.key.reveal/v1/reveal")
	if op["operationId"] != "orv.key.reveal.v1.reveal" || op["summary"] != "Reveal the key" {
		t.Errorf("reveal operation = %v", op)
	}
	ok := op["responses"].(map[string]any)["2XX"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	value := ok["properties"].(map[string]any)["value"].(map[string]any)
	if value["$ref"] != "#/components/schemas/orv.key.reveal.v1.output" || len(ok["required"].([]any)) != 1 || ok["required"].([]any)[0] != "value" {
		t.Errorf("reveal answer schema = %v, want {value: the output}", ok)
	}
	if _, found := doc["paths"].(map[string]any)["/api/v1/hosts/{host}/actions/orv.key.reveal/v1/read"]; found {
		t.Error("a reveal action has a read operation")
	}
	for _, name := range []string{"orv.cert.get", "orv.ctl.get"} {
		if bytes.Contains(raw, []byte(name)) {
			t.Errorf("the internal action %s appears in the document", name)
		}
	}
}
