package apidocs

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/darkraise/darkpanel/internal/core"
)

const (
	componentRef = "#/components/schemas/"
	maxTypeDepth = 32
)

// operation is one operation of the OpenAPI document.
type operation struct {
	method, path string
	body         map[string]any
}

type renderer struct {
	schemas    map[string]any
	schemes    map[string]any
	operations []operation // ordered by path, then method
	byID       map[string]operation
}

func newRenderer(doc map[string]any) *renderer {
	components := asMap(doc["components"])
	r := &renderer{
		schemas: asMap(components["schemas"]),
		schemes: asMap(components["securitySchemes"]),
		byID:    map[string]operation{},
	}
	paths := asMap(doc["paths"])
	for _, p := range sortedKeys(paths) {
		item := asMap(paths[p])
		for _, m := range sortedKeys(item) {
			op := operation{method: strings.ToUpper(m), path: p, body: asMap(item[m])}
			r.operations = append(r.operations, op)
			if id, _ := op.body["operationId"].(string); id != "" {
				r.byID[id] = op
			}
		}
	}
	slices.SortFunc(r.operations, func(a, b operation) int {
		return cmp.Or(cmp.Compare(a.path, b.path), cmp.Compare(a.method, b.method))
	})
	return r
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// cell makes s safe for a table cell: whitespace runs become one space and
// the characters that Markdown would read as table or HTML syntax are
// escaped.
func cell(s string) string {
	s = strings.Join(strings.FieldsFunc(s, unicode.IsSpace), " ")
	return strings.NewReplacer("|", `\|`, "<", `\<`, ">", `\>`).Replace(s)
}

// resolve follows $ref references to the schema they name. It returns that
// schema and the name of the last component followed, "" when s holds no
// reference.
func (r *renderer) resolve(s any) (map[string]any, string, error) {
	cur, name := asMap(s), ""
	for seen := 0; ; seen++ {
		ref, _ := cur["$ref"].(string)
		next, ok := strings.CutPrefix(ref, componentRef)
		if !ok {
			return cur, name, nil
		}
		target, found := r.schemas[next]
		if !found {
			return nil, "", fmt.Errorf("apidocs: no component schema %s", next)
		}
		if seen > len(r.schemas) {
			return nil, "", fmt.Errorf("apidocs: component schema %s refers to itself", next)
		}
		cur, name = asMap(target), next
	}
}

func (r *renderer) typeName(s map[string]any, depth int) (string, error) {
	if depth > maxTypeDepth {
		return "", fmt.Errorf("apidocs: a schema nests deeper than %d levels", maxTypeDepth)
	}
	sub := func(v any) (string, error) {
		res, _, err := r.resolve(v)
		if err != nil {
			return "", err
		}
		return r.typeName(res, depth+1)
	}
	if members, ok := s["oneOf"].([]any); ok {
		names := make([]string, len(members))
		for i, m := range members {
			n, err := sub(m)
			if err != nil {
				return "", err
			}
			names[i] = n
		}
		return strings.Join(names, " or "), nil
	}
	switch t := s["type"].(type) {
	case string:
		switch t {
		case "array":
			items, ok := s["items"]
			if !ok {
				return "array of any", nil
			}
			n, err := sub(items)
			return "array of " + n, err
		case "object":
			if extra, ok := s["additionalProperties"].(map[string]any); ok && len(asMap(s["properties"])) == 0 {
				n, err := sub(extra)
				return "map of " + n, err
			}
		case "string":
			if f, _ := s["format"].(string); f != "" {
				return "string (" + f + ")", nil
			}
		}
		return t, nil
	case []any:
		names := make([]string, len(t))
		for i, e := range t {
			names[i] = fmt.Sprint(e)
		}
		return strings.Join(names, " or "), nil
	}
	return "any", nil
}

func description(prop, resolved map[string]any) string {
	desc, _ := prop["description"].(string)
	if desc == "" {
		desc, _ = resolved["description"].(string)
	}
	parts := []string{desc}
	enum, ok := prop["enum"].([]any)
	if !ok {
		enum, ok = resolved["enum"].([]any)
	}
	if ok {
		values := make([]string, len(enum))
		for i, v := range enum {
			values[i] = fmt.Sprint(v)
		}
		parts = append(parts, "Values: "+strings.Join(values, ", ")+".")
	}
	if prop["x-secret"] == true || resolved["x-secret"] == true {
		parts = append(parts, "Secret.")
	}
	parts = slices.DeleteFunc(parts, func(p string) bool { return p == "" })
	return cell(strings.Join(parts, " "))
}

// table renders the properties of the named component, nested objects
// included, or empty when it is an object that can hold no field. Any other
// schema without properties is an error: the table cannot describe it.
func (r *renderer) table(component, empty string) (string, error) {
	res, name, err := r.resolve(map[string]any{"$ref": componentRef + component})
	if err != nil {
		return "", err
	}
	if len(asMap(res["properties"])) == 0 {
		if !closedEmptyObject(res) {
			return "", fmt.Errorf("apidocs: component %s has no properties and is not an empty object, so no table describes it", component)
		}
		return empty, nil
	}
	lines := []string{"| Field | Type | Required | Description |", "|---|---|---|---|"}
	lines, err = r.rows(lines, res, "", []string{name})
	if err != nil {
		return "", err
	}
	return strings.Join(lines, "\n"), nil
}

// closedEmptyObject reports whether schema is an object that admits no member:
// no additional properties and no composition that could add some.
func closedEmptyObject(schema map[string]any) bool {
	if schema["type"] != "object" || schema["additionalProperties"] != false {
		return false
	}
	for _, key := range []string{"oneOf", "anyOf", "allOf", "patternProperties"} {
		if _, ok := schema[key]; ok {
			return false
		}
	}
	return true
}

// rows appends one row per property of parent in name order, each followed by
// the rows of the object it holds. stack names the components being expanded
// above, which are not expanded again.
func (r *renderer) rows(lines []string, parent map[string]any, prefix string, stack []string) ([]string, error) {
	props := asMap(parent["properties"])
	required := map[string]bool{}
	if list, ok := parent["required"].([]any); ok {
		for _, n := range list {
			if s, ok := n.(string); ok {
				required[s] = true
			}
		}
	}
	for _, name := range sortedKeys(props) {
		prop := asMap(props[name])
		res, resName, err := r.resolve(prop)
		if err != nil {
			return nil, err
		}
		typ, err := r.typeName(res, 0)
		if err != nil {
			return nil, err
		}
		path := prefix + name
		req := "no"
		if required[name] {
			req = "yes"
		}
		lines = append(lines, fmt.Sprintf("| `%s` | %s | %s | %s |", path, typ, req, description(prop, res)))

		inner, innerName, childPrefix := res, resName, path+"."
		if res["type"] == "array" {
			if items, ok := res["items"]; ok {
				if inner, innerName, err = r.resolve(items); err != nil {
					return nil, err
				}
				childPrefix = path + "[]."
			}
		}
		if len(asMap(inner["properties"])) == 0 || (innerName != "" && slices.Contains(stack, innerName)) {
			continue
		}
		if lines, err = r.rows(lines, inner, childPrefix, append(slices.Clone(stack), innerName)); err != nil {
			return nil, err
		}
	}
	return lines, nil
}

type opRef struct{ kind, id string }

// section renders one action or resource. inputComponent is "" for a
// resource, which has no input.
func (r *renderer) section(heading string, ops []opRef, scope core.Scope, inputComponent, outputComponent, note string) (string, error) {
	rows := make([]string, len(ops))
	var summary string
	for i, o := range ops {
		op, ok := r.byID[o.id]
		if !ok {
			return "", fmt.Errorf("apidocs: the OpenAPI document has no operation %s", o.id)
		}
		if i == 0 {
			summary, _ = op.body["summary"].(string)
		}
		rows[i] = fmt.Sprintf("| %s | `%s %s` |", o.kind, op.method, op.path)
	}
	blocks := []string{heading}
	if summary != "" {
		blocks = append(blocks, summary)
	}
	blocks = append(blocks,
		"| Operation | Request |\n|---|---|\n"+strings.Join(rows, "\n"),
		fmt.Sprintf("Minimum scope: `%s`.", scope),
	)
	if inputComponent != "" {
		in, err := r.table(inputComponent, "This action takes no input fields.")
		if err != nil {
			return "", err
		}
		blocks = append(blocks, "### Input", "Sent in the `input` field of the request body.", in)
	}
	out, err := r.table(outputComponent, "This action returns no fields.")
	if err != nil {
		return "", err
	}
	blocks = append(blocks, "### Output", note, out)
	return strings.Join(blocks, "\n\n"), nil
}

func (r *renderer) actionSection(spec core.ActionSpec) (string, error) {
	id := fmt.Sprintf("%s.v%d", spec.Name, spec.Version)
	var ops []opRef
	var note string
	switch {
	case spec.Reveal:
		ops, note = []opRef{{"reveal", id + ".reveal"}}, "Returned in the `value` field of the response."
	case spec.Class == core.ClassRead:
		ops, note = []opRef{{"read", id + ".read"}}, "Returned in the `output` field of the response."
	default:
		ops = []opRef{{"plan", id + ".plan"}, {"apply", id + ".apply"}}
		note = "Returned in the `output` field of the change that apply returns."
	}
	return r.section(fmt.Sprintf("## %s v%d", spec.Name, spec.Version), ops, core.RequiredScope(spec), id+".input", id+".output", note)
}

func (r *renderer) resourceSection(res core.ResourceDef) (string, error) {
	return r.section("## "+res.Name, []opRef{{"read", res.Name + ".get"}}, core.ScopeRead, "", res.Name+".output",
		"Returned in the `output` field of the response.")
}

// modulePage returns the page of m, or "" when m has no action or resource
// to document.
func (r *renderer) modulePage(m core.Manifest) (string, error) {
	type keyed struct{ key, text string }
	var secs []keyed
	for _, def := range m.Actions {
		if def.Spec.BreakGlass || def.Spec.Internal {
			continue
		}
		text, err := r.actionSection(def.Spec)
		if err != nil {
			return "", err
		}
		secs = append(secs, keyed{fmt.Sprintf("%s.v%d", def.Spec.Name, def.Spec.Version), text})
	}
	for _, res := range m.Resources {
		text, err := r.resourceSection(res)
		if err != nil {
			return "", err
		}
		secs = append(secs, keyed{res.Name, text})
	}
	if len(secs) == 0 {
		return "", nil
	}
	slices.SortFunc(secs, func(a, b keyed) int { return strings.Compare(a.key, b.key) })
	blocks := []string{"# " + m.ID + " API", "Back to the [API reference](README.md)."}
	for _, s := range secs {
		blocks = append(blocks, s.text)
	}
	return strings.Join(blocks, "\n\n") + "\n", nil
}

func (r *renderer) authentication() string {
	lines := []string{"| Scheme | Type | Credential | Description |", "|---|---|---|---|"}
	for _, name := range sortedKeys(r.schemes) {
		s := asMap(r.schemes[name])
		typ, _ := s["type"].(string)
		var credential string
		switch typ {
		case "apiKey":
			in, _ := s["in"].(string)
			n, _ := s["name"].(string)
			credential = fmt.Sprintf("%s `%s`", in, n)
		case "http":
			scheme, _ := s["scheme"].(string)
			if scheme != "" {
				scheme = strings.ToUpper(scheme[:1]) + scheme[1:]
			}
			credential = fmt.Sprintf("`Authorization: %s` header", scheme)
		}
		desc, _ := s["description"].(string)
		lines = append(lines, fmt.Sprintf("| `%s` | %s | %s | %s |", name, typ, credential, cell(desc)))
	}
	return strings.Join(lines, "\n")
}

func (r *renderer) routes() string {
	lines := []string{"| Method | Path | Authentication | Summary |", "|---|---|---|---|"}
	for _, op := range r.operations {
		if id, _ := op.body["operationId"].(string); id != "" {
			continue
		}
		auth := "none"
		if security, _ := op.body["security"].([]any); len(security) > 0 {
			auth = "session or token"
		}
		summary, _ := op.body["summary"].(string)
		lines = append(lines, fmt.Sprintf("| %s | `%s` | %s | %s |", op.method, op.path, auth, cell(summary)))
	}
	return strings.Join(lines, "\n")
}

// index returns the README page, which links the pages of modules.
func (r *renderer) index(modules []string) (string, error) {
	blocks := []string{
		"# Darkpanel API reference",
		"This reference is generated from the action schemas. `openapi.json` is the OpenAPI 3.1 document that the controller serves at `/api/v1/openapi.json`.",
		"## Authentication",
		r.authentication(),
		"## Common components",
	}
	for _, name := range []string{"Plan", "Change", "ApplyRequest", "Problem"} {
		t, err := r.table(name, "No fields.")
		if err != nil {
			return "", err
		}
		blocks = append(blocks, "### "+name, t)
	}
	blocks = append(blocks, "## Routes", r.routes(), "## Modules")
	if len(modules) > 0 {
		links := make([]string, len(modules))
		for i, id := range modules {
			links[i] = fmt.Sprintf("- [%s](%s.md)", id, id)
		}
		blocks = append(blocks, strings.Join(links, "\n"))
	}
	return strings.Join(blocks, "\n\n") + "\n", nil
}
