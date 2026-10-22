// Package schema turns action and resource types into JSON Schemas (draft
// 2020-12), validates inputs against them, writes the committed snapshots,
// checks released snapshots for compatibility and builds the OpenAPI
// document (spec section 7, ADR 0002).
package schema

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"reflect"
	"strings"

	"github.com/invopop/jsonschema"
)

// secretKeyword marks a property whose value is a secret (shared contracts:
// the secret:"true" struct tag).
const secretKeyword = "x-secret"

// The formats of the netip types. Their JSON form is the string their
// MarshalText writes; the validator checks it with the matching parser.
const (
	formatAddr     = "ip-address"
	formatAddrPort = "ip-address-port"
	formatPrefix   = "ip-prefix"
)

// Input returns the schema of an action's input type: every field is required
// unless its JSON tag has omitempty, and every object refuses unknown
// properties, so a misspelt safety field is rejected rather than ignored
// (ADR 0002). Secret fields carry "x-secret" and "writeOnly".
func Input(t reflect.Type) (json.RawMessage, error) { return build(t, true) }

// Output returns the schema of an action's or resource's output type. Objects
// allow additional properties, so clients accept fields added later; secret
// fields carry "x-secret".
func Output(t reflect.Type) (json.RawMessage, error) { return build(t, false) }

func build(t reflect.Type, input bool) (json.RawMessage, error) {
	if t == nil {
		return nil, errors.New("schema: nil type")
	}
	r := &jsonschema.Reflector{Anonymous: true, AllowAdditionalProperties: !input, Mapper: addressSchema}
	s := r.ReflectFromType(t)
	markSecrets(t, s, s.Definitions, input, map[*jsonschema.Schema]bool{})
	b, err := json.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("schema: marshal %s: %w", t, err)
	}
	return b, nil
}

// addressSchema describes the netip types as strings of their format. Left
// to the reflector, their structs, which have no exported fields, would be
// empty objects, and an input schema would refuse their JSON form.
func addressSchema(t reflect.Type) *jsonschema.Schema {
	switch t {
	case reflect.TypeFor[netip.Addr]():
		return &jsonschema.Schema{Type: "string", Format: formatAddr}
	case reflect.TypeFor[netip.AddrPort]():
		return &jsonschema.Schema{Type: "string", Format: formatAddrPort}
	case reflect.TypeFor[netip.Prefix]():
		return &jsonschema.Schema{Type: "string", Format: formatPrefix}
	}
	return nil
}

// resolve follows a "#/$defs/<name>" reference.
func resolve(s *jsonschema.Schema, defs jsonschema.Definitions) *jsonschema.Schema {
	if name, ok := strings.CutPrefix(s.Ref, "#/$defs/"); ok {
		return defs[name]
	}
	return s
}

// markSecrets walks t and its schema s together and marks every property
// whose struct field is tagged secret:"true".
func markSecrets(t reflect.Type, s *jsonschema.Schema, defs jsonschema.Definitions, input bool, seen map[*jsonschema.Schema]bool) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	s = resolve(s, defs)
	if s == nil || seen[s] {
		return
	}
	seen[s] = true
	switch t.Kind() {
	case reflect.Slice, reflect.Array:
		if s.Items != nil {
			markSecrets(t.Elem(), s.Items, defs, input, seen)
		}
	case reflect.Map:
		if s.AdditionalProperties != nil {
			markSecrets(t.Elem(), s.AdditionalProperties, defs, input, seen)
		}
		for _, p := range s.PatternProperties {
			markSecrets(t.Elem(), p, defs, input, seen)
		}
	case reflect.Struct:
		markFields(t, s, defs, input, seen)
	}
}

func markFields(t reflect.Type, s *jsonschema.Schema, defs jsonschema.Definitions, input bool, seen map[*jsonschema.Schema]bool) {
	if s.Properties == nil {
		return // a type with its own representation, such as time.Time
	}
	for i := range t.NumField() {
		f := t.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "-" {
			continue
		}
		ft := f.Type
		for ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}
		if f.Anonymous && name == "" && ft.Kind() == reflect.Struct {
			markFields(ft, s, defs, input, seen) // embedded fields are inlined
			continue
		}
		if !f.IsExported() {
			continue
		}
		if name == "" {
			name = f.Name
		}
		p, ok := s.Properties.Get(name)
		if !ok {
			continue
		}
		if f.Tag.Get("secret") == "true" {
			if p.Extras == nil {
				p.Extras = map[string]any{}
			}
			p.Extras[secretKeyword] = true
			p.WriteOnly = input
		}
		markSecrets(f.Type, p, defs, input, seen)
	}
}
