package secret

import (
	"encoding/json"
	"testing"
)

// inSchema has the shape internal/core/schema produces from
//
//	type Cred struct {
//		User     string `json:"user"`
//		Password string `json:"password" secret:"true"`
//	}
//	type In struct {
//		Name  string            `json:"name"`
//		Creds []Cred            `json:"creds,omitempty"`
//		Main  *Cred             `json:"main,omitempty"`
//		Tags  map[string]string `json:"tags,omitempty"`
//		Extra any               `json:"extra,omitempty"`
//	}
const inSchema = `{
	"$schema": "https://json-schema.org/draft/2020-12/schema",
	"$ref": "#/$defs/In",
	"$defs": {
		"Cred": {
			"type": "object",
			"properties": {
				"user": {"type": "string"},
				"password": {"type": "string", "x-secret": true, "writeOnly": true}
			},
			"additionalProperties": false,
			"required": ["user", "password"]
		},
		"In": {
			"type": "object",
			"properties": {
				"name": {"type": "string"},
				"creds": {"type": "array", "items": {"$ref": "#/$defs/Cred"}},
				"main": {"$ref": "#/$defs/Cred"},
				"tags": {"type": "object", "additionalProperties": {"type": "string"}},
				"extra": true
			},
			"additionalProperties": false,
			"required": ["name"]
		}
	}
}`

func TestRedactJSON(t *testing.T) {
	tests := []struct {
		name, raw, schema, want string
	}{
		{
			name:   "nested secrets through refs, arrays and pointers",
			raw:    `{"name":"web","creds":[{"user":"a","password":"p1"},{"user":"b","password":"p2"}],"main":{"user":"c","password":"p3"},"tags":{"env":"prod"}}`,
			schema: inSchema,
			want:   `{"creds":[{"password":"[redacted]","user":"a"},{"password":"[redacted]","user":"b"}],"main":{"password":"[redacted]","user":"c"},"name":"web","tags":{"env":"prod"}}`,
		},
		{
			name:   "a secret of any type is replaced whole",
			raw:    `{"main":{"user":"c","password":{"nested":["x"]}},"name":"web"}`,
			schema: inSchema,
			want:   `{"main":{"password":"[redacted]","user":"c"},"name":"web"}`,
		},
		{
			name:   "null and missing values",
			raw:    `{"name":"web","main":null}`,
			schema: inSchema,
			want:   `{"main":null,"name":"web"}`,
		},
		{
			name:   "an unknown property is redacted",
			raw:    `{"name":"web","token":"t0p"}`,
			schema: inSchema,
			want:   `{"name":"web","token":"[redacted]"}`,
		},
		{
			name:   "a free-form value keeps scalars and redacts what it contains",
			raw:    `{"name":"web","extra":{"apiKey":"k","list":[1]}}`,
			schema: inSchema,
			want:   `{"extra":{"apiKey":"[redacted]","list":"[redacted]"},"name":"web"}`,
		},
		{
			name:   "a free-form scalar is kept",
			raw:    `{"name":"web","extra":"plain"}`,
			schema: inSchema,
			want:   `{"extra":"plain","name":"web"}`,
		},
		{
			name:   "a secret root",
			raw:    `"hunter2"`,
			schema: `{"type": "string", "x-secret": true}`,
			want:   `"[redacted]"`,
		},
		{
			name:   "a secret field whose type is a referenced struct",
			raw:    `{"main":{"user":"c","password":"p3"}}`,
			schema: `{"type":"object","properties":{"main":{"$ref":"#/$defs/Cred","x-secret":true}},"$defs":{"Cred":{"type":"object","properties":{"user":{"type":"string"},"password":{"type":"string"}}}}}`,
			want:   `{"main":"[redacted]"}`,
		},
		{
			name:   "oneOf and anyOf branches",
			raw:    `{"a":{"user":"u","password":"p"},"b":{"user":"v","password":"q"},"c":"text"}`,
			schema: `{"type":"object","properties":{"a":{"oneOf":[{"type":"string"},{"$ref":"#/$defs/Cred"}]},"b":{"anyOf":[{"type":"null"},{"$ref":"#/$defs/Cred"}]},"c":{"oneOf":[{"type":"string"},{"$ref":"#/$defs/Cred"}]}},"$defs":{"Cred":{"type":"object","properties":{"user":{"type":"string"},"password":{"type":"string","x-secret":true}}}}}`,
			want:   `{"a":{"password":"[redacted]","user":"u"},"b":{"password":"[redacted]","user":"v"},"c":"text"}`,
		},
		{
			name:   "a branch that marks the value secret wins",
			raw:    `{"a":"s3cret"}`,
			schema: `{"type":"object","properties":{"a":{"anyOf":[{"type":"string"},{"type":"string","x-secret":true}]}}}`,
			want:   `{"a":"[redacted]"}`,
		},
		{
			name:   "an unresolvable reference redacts the value whole",
			raw:    `{"a":{"user":"u"},"b":1}`,
			schema: `{"type":"object","properties":{"a":{"$ref":"#/$defs/Missing"},"b":{"type":"integer"}}}`,
			want:   `{"a":"[redacted]","b":1}`,
		},
		{
			name:   "a reference cycle redacts instead of looping",
			raw:    `{"x":1}`,
			schema: `{"$ref":"#/$defs/A","$defs":{"A":{"$ref":"#/$defs/B"},"B":{"$ref":"#/$defs/A"}}}`,
			want:   `"[redacted]"`,
		},
		{
			name:   "a recursive type follows the data",
			raw:    `{"key":"k1","children":[{"key":"k2","children":[{"key":"k3","children":[]}]}]}`,
			schema: `{"$ref":"#/$defs/Node","$defs":{"Node":{"type":"object","properties":{"key":{"type":"string","x-secret":true},"children":{"type":"array","items":{"$ref":"#/$defs/Node"}}}}}}`,
			want:   `{"children":[{"children":[{"children":[],"key":"[redacted]"}],"key":"[redacted]"}],"key":"[redacted]"}`,
		},
		{
			name:   "prefixItems apply by position and an array without items redacts its elements",
			raw:    `{"pair":["user","pass","more"],"loose":["a"]}`,
			schema: `{"type":"object","properties":{"pair":{"type":"array","prefixItems":[{"type":"string"},{"type":"string","x-secret":true}]},"loose":{"type":"array"}}}`,
			want:   `{"loose":["[redacted]"],"pair":["user","[redacted]","[redacted]"]}`,
		},
		{
			name:   "patternProperties",
			raw:    `{"80":"web","x":"y"}`,
			schema: `{"type":"object","patternProperties":{"^[0-9]+$":{"type":"string"}},"additionalProperties":{"type":"string","x-secret":true}}`,
			want:   `{"80":"web","x":"[redacted]"}`,
		},
		{
			name:   "numbers keep their text and HTML is not escaped",
			raw:    `{"big":12345678901234567890,"dec":1.50,"html":"<a&b>"}`,
			schema: `{"type":"object","properties":{"big":{"type":"integer"},"dec":{"type":"number"},"html":{"type":"string"}}}`,
			want:   `{"big":12345678901234567890,"dec":1.50,"html":"<a&b>"}`,
		},
		{name: "invalid JSON", raw: `{"name":`, schema: inSchema, want: `"[redacted]"`},
		{name: "trailing data", raw: `{"name":"a"} {}`, schema: inSchema, want: `"[redacted]"`},
		{name: "empty input", raw: ``, schema: inSchema, want: `"[redacted]"`},
		{name: "invalid schema", raw: `{"name":"a"}`, schema: `{"type":`, want: `"[redacted]"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RedactJSON(json.RawMessage(tt.raw), json.RawMessage(tt.schema))
			if string(got) != tt.want {
				t.Errorf("RedactJSON =\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}

func TestRedactJSONLeavesInputUnchanged(t *testing.T) {
	raw := json.RawMessage(`{"name":"web","main":{"user":"c","password":"p3"}}`)
	before := string(raw)
	_ = RedactJSON(raw, json.RawMessage(inSchema))
	if string(raw) != before {
		t.Errorf("input changed to %s", raw)
	}
}
