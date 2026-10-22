package schema

import (
	"bytes"
	"encoding/json"
	"net/netip"
	"reflect"
	"slices"
	"testing"
)

// decodeSchema unmarshals a schema for inspection in tests.
func decodeSchema(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("schema is not JSON: %v\n%s", err, raw)
	}
	return m
}

// def returns the $defs entry name of a decoded schema.
func def(t *testing.T, s map[string]any, name string) map[string]any {
	t.Helper()
	defs, _ := s["$defs"].(map[string]any)
	d, ok := defs[name].(map[string]any)
	if !ok {
		t.Fatalf("$defs has no %s: %v", name, s["$defs"])
	}
	return d
}

func prop(t *testing.T, obj map[string]any, name string) map[string]any {
	t.Helper()
	props, _ := obj["properties"].(map[string]any)
	p, ok := props[name].(map[string]any)
	if !ok {
		t.Fatalf("no property %s in %v", name, obj["properties"])
	}
	return p
}

func required(obj map[string]any) []string {
	var out []string
	for _, r := range obj["required"].([]any) {
		out = append(out, r.(string))
	}
	slices.Sort(out)
	return out
}

func TestInputSchema(t *testing.T) {
	raw, err := Input(reflect.TypeFor[fixtureInput]())
	if err != nil {
		t.Fatal(err)
	}
	s := decodeSchema(t, raw)
	if s["$schema"] != "https://json-schema.org/draft/2020-12/schema" {
		t.Errorf("$schema = %v", s["$schema"])
	}
	if _, ok := s["$id"]; ok {
		t.Errorf("schema has an $id: %v", s["$id"])
	}
	if s["$ref"] != "#/$defs/fixtureInput" {
		t.Errorf("$ref = %v, want #/$defs/fixtureInput", s["$ref"])
	}
	in := def(t, s, "fixtureInput")
	if in["additionalProperties"] != false {
		t.Errorf("input additionalProperties = %v, want false", in["additionalProperties"])
	}
	if got, want := required(in), []string{"mode", "name", "password", "target"}; !slices.Equal(got, want) {
		t.Errorf("required = %v, want %v", got, want)
	}
	if p := prop(t, in, "name"); p["pattern"] != "^[a-z][a-z0-9-]*$" {
		t.Errorf("name pattern = %v", p["pattern"])
	}
	if p := prop(t, in, "mode"); !reflect.DeepEqual(p["enum"], []any{"fast", "safe"}) {
		t.Errorf("mode enum = %v", p["enum"])
	}
	if p := prop(t, in, "count"); p["minimum"] != 1.0 || p["maximum"] != 10.0 {
		t.Errorf("count range = %v..%v", p["minimum"], p["maximum"])
	}
	for _, name := range []string{"password", "apiKey"} {
		if p := prop(t, in, name); p["x-secret"] != true || p["writeOnly"] != true {
			t.Errorf("%s = %v, want x-secret and writeOnly", name, p)
		}
	}
	if p := prop(t, in, "name"); p["x-secret"] != nil || p["writeOnly"] != nil {
		t.Errorf("name is marked secret: %v", p)
	}
	if p := prop(t, in, "target"); p["$ref"] != "#/$defs/fixtureAddress" {
		t.Errorf("target = %v, want a $ref to fixtureAddress", p)
	}
	addr := def(t, s, "fixtureAddress")
	if addr["additionalProperties"] != false {
		t.Errorf("nested additionalProperties = %v, want false", addr["additionalProperties"])
	}
	if p := prop(t, addr, "port"); p["minimum"] != 1.0 || p["maximum"] != 65535.0 {
		t.Errorf("port range = %v..%v", p["minimum"], p["maximum"])
	}
	if p := prop(t, def(t, s, "fixtureCredential"), "key"); p["x-secret"] != true || p["writeOnly"] != true {
		t.Errorf("creds[].key = %v, want x-secret and writeOnly", p)
	}
}

func TestOutputSchema(t *testing.T) {
	raw, err := Output(reflect.TypeFor[fixtureOutput]())
	if err != nil {
		t.Fatal(err)
	}
	s := decodeSchema(t, raw)
	out := def(t, s, "fixtureOutput")
	if _, ok := out["additionalProperties"]; ok {
		t.Errorf("output additionalProperties = %v, want absent", out["additionalProperties"])
	}
	if _, ok := def(t, s, "fixtureAddress")["additionalProperties"]; ok {
		t.Error("nested output object restricts additional properties")
	}
	p := prop(t, out, "token")
	if p["x-secret"] != true {
		t.Errorf("token x-secret = %v, want true", p["x-secret"])
	}
	if _, ok := p["writeOnly"]; ok {
		t.Error("output field marked writeOnly")
	}
}

func TestSchemaDeterministic(t *testing.T) {
	a, err := Input(reflect.TypeFor[fixtureInput]())
	if err != nil {
		t.Fatal(err)
	}
	for range 5 {
		b, err := Input(reflect.TypeFor[fixtureInput]())
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(a, b) {
			t.Fatalf("schemas differ:\n%s\n%s", a, b)
		}
	}
}

func TestSchemaNilType(t *testing.T) {
	if _, err := Input(nil); err == nil {
		t.Error("Input(nil) succeeded")
	}
}

// addrFields has one field of each netip type, each optional so that a
// validation case can send one alone. A netip zero value marshals as "", which
// the formats refuse, and omitempty never omits a struct, so the optional
// netip fields are omitzero.
type addrFields struct {
	A netip.Addr       `json:"a,omitzero"`
	P netip.AddrPort   `json:"p,omitzero"`
	N netip.Prefix     `json:"n,omitzero"`
	L []netip.AddrPort `json:"l,omitempty"`
}

func TestAddressSchemas(t *testing.T) {
	for name, build := range map[string]func(reflect.Type) (json.RawMessage, error){"input": Input, "output": Output} {
		raw, err := build(reflect.TypeFor[addrFields]())
		if err != nil {
			t.Fatal(err)
		}
		obj := def(t, decodeSchema(t, raw), "addrFields")
		for field, format := range map[string]string{"a": "ip-address", "p": "ip-address-port", "n": "ip-prefix"} {
			if p := prop(t, obj, field); p["type"] != "string" || p["format"] != format || len(p) != 2 {
				t.Errorf("%s schema of %s = %v, want {type: string, format: %s}", name, field, p, format)
			}
		}
		items, _ := prop(t, obj, "l")["items"].(map[string]any)
		if items["type"] != "string" || items["format"] != "ip-address-port" {
			t.Errorf("%s schema of l's items = %v, want {type: string, format: ip-address-port}", name, items)
		}
	}
}
