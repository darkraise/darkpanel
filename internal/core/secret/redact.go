package secret

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"
)

// Redacted replaces every secret value in audit, journal, logs, diffs and
// errors.
const Redacted = "[redacted]"

var redactedJSON = json.RawMessage(`"` + Redacted + `"`)

// maxRefDepth bounds the $ref and combinator nesting followed for one value,
// so a reference cycle redacts instead of looping.
const maxRefDepth = 32

// RedactJSON returns raw with every value whose schema carries
// "x-secret": true replaced by "[redacted]". It follows $ref into $defs,
// object properties, patternProperties and additionalProperties, array items
// and prefixItems, and every oneOf, anyOf and allOf branch; a branch that marks
// a value secret wins. It fails closed: a value whose schema cannot be
// resolved, an object property or array element no schema describes, invalid
// JSON in raw or in schema, all come back as "[redacted]". The result is
// compact JSON; raw is not modified.
func RedactJSON(raw, schema json.RawMessage) json.RawMessage {
	value, err := decodeJSON(raw)
	if err != nil {
		return redactedJSON
	}
	root, err := decodeJSON(schema)
	if err != nil {
		return redactedJSON
	}
	r := redactor{root: root}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(r.redact(value, []any{root})); err != nil {
		return redactedJSON
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
}

// decodeJSON parses exactly one JSON value, keeping numbers as written.
func decodeJSON(raw []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if !errors.Is(dec.Decode(new(json.RawMessage)), io.EOF) {
		return nil, errors.New("trailing data after the JSON value")
	}
	return v, nil
}

type redactor struct {
	root any
}

// redact returns v redacted under every schema in schemas, which all apply to
// v at once (the branches of oneOf, anyOf and allOf are unioned).
func (r redactor) redact(v any, schemas []any) any {
	leaves, ok := r.expand(schemas)
	if !ok {
		return Redacted
	}
	for _, s := range leaves {
		if s["x-secret"] == true {
			return Redacted
		}
	}
	switch val := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(val))
		for key, child := range val {
			out[key] = r.redactChild(child, propertySchemas(leaves, key))
		}
		return out
	case []any:
		out := make([]any, len(val))
		for i, child := range val {
			out[i] = r.redactChild(child, itemSchemas(leaves, i))
		}
		return out
	default:
		return v
	}
}

// redactChild redacts a property or element; one that no schema describes is
// redacted whole.
func (r redactor) redactChild(v any, schemas []any) any {
	if len(schemas) == 0 {
		return Redacted
	}
	return r.redact(v, schemas)
}

// expand resolves $ref and flattens oneOf, anyOf and allOf into the object
// schemas that apply to a value. Boolean schemas describe no structure and
// are dropped. ok is false when a reference cannot be resolved or the nesting
// exceeds maxRefDepth.
func (r redactor) expand(schemas []any) (leaves []map[string]any, ok bool) {
	var walk func(s any, depth int) bool
	walk = func(s any, depth int) bool {
		if depth > maxRefDepth {
			return false
		}
		obj, isObj := s.(map[string]any)
		if !isObj {
			_, isBool := s.(bool)
			return isBool
		}
		leaves = append(leaves, obj)
		if ref, has := obj["$ref"]; has {
			target, found := r.resolve(ref)
			if !found || !walk(target, depth+1) {
				return false
			}
		}
		for _, kw := range []string{"oneOf", "anyOf", "allOf"} {
			branches, has := obj[kw]
			if !has {
				continue
			}
			list, isList := branches.([]any)
			if !isList {
				return false
			}
			for _, b := range list {
				if !walk(b, depth+1) {
					return false
				}
			}
		}
		return true
	}
	for _, s := range schemas {
		if !walk(s, 0) {
			return nil, false
		}
	}
	return leaves, true
}

// resolve finds a "#/$defs/<name>" reference in the root schema.
func (r redactor) resolve(ref any) (any, bool) {
	s, ok := ref.(string)
	name, cut := strings.CutPrefix(s, "#/$defs/")
	if !ok || !cut {
		return nil, false
	}
	root, ok := r.root.(map[string]any)
	if !ok {
		return nil, false
	}
	defs, ok := root["$defs"].(map[string]any)
	if !ok {
		return nil, false
	}
	target, ok := defs[name]
	return target, ok
}

// propertySchemas returns the schemas of an object's property key: its
// properties entry and matching patternProperties, or else
// additionalProperties when that is a schema object (JSON Schema 2020-12).
func propertySchemas(leaves []map[string]any, key string) []any {
	var out []any
	for _, s := range leaves {
		matched := false
		if props, ok := s["properties"].(map[string]any); ok {
			if p, ok := props[key]; ok {
				out = append(out, p)
				matched = true
			}
		}
		if patterns, ok := s["patternProperties"].(map[string]any); ok {
			for pattern, p := range patterns {
				if re, err := regexp.Compile(pattern); err == nil && re.MatchString(key) {
					out = append(out, p)
					matched = true
				}
			}
		}
		if extra, ok := s["additionalProperties"].(map[string]any); ok && !matched {
			out = append(out, extra)
		}
	}
	return out
}

// itemSchemas returns the schemas of array element i: its prefixItems entry,
// or else items when that is a schema object.
func itemSchemas(leaves []map[string]any, i int) []any {
	var out []any
	for _, s := range leaves {
		if prefix, ok := s["prefixItems"].([]any); ok && i < len(prefix) {
			out = append(out, prefix[i])
			continue
		}
		if items, ok := s["items"].(map[string]any); ok {
			out = append(out, items)
		}
	}
	return out
}
