package schema

import (
	"encoding/json"
	"errors"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

type paramsLogs struct {
	Tail   int      `json:"tail"`
	Follow bool     `json:"follow"`
	Name   []string `json:"name"`
}

type paramsMixed struct {
	Ratio float64 `json:"ratio,omitempty"`
	Since string  `json:"since,omitempty"`
	Ports []int   `json:"ports,omitempty"`
	Inner struct {
		N int `json:"n"`
	} `json:"inner,omitzero"`
}

func TestParamsJSON(t *testing.T) {
	logs, mixed := reflect.TypeFor[paramsLogs](), reflect.TypeFor[paramsMixed]()
	tests := []struct {
		name  string
		typ   reflect.Type
		query string
		want  string // members sorted by key; "" wants request.invalid_input at path
		path  string
	}{
		{"every kind", logs, "tail=100&follow=true&name=a&name=b", `{"follow":true,"name":["a","b"],"tail":100}`, ""},
		{"one array value", logs, "name=a", `{"name":["a"]}`, ""},
		{"no parameters", logs, "", `{}`, ""},
		{"number and string", mixed, "ratio=0.5&since=2026-10-22T00:00:00Z", `{"ratio":0.5,"since":"2026-10-22T00:00:00Z"}`, ""},
		{"integer items", mixed, "ports=80&ports=443", `{"ports":[80,443]}`, ""},
		{"not an integer", logs, "tail=s3cr3t", "", "/tail"},
		{"not a boolean", logs, "follow=maybe", "", "/follow"},
		{"repeated scalar", logs, "tail=1&tail=2", "", "/tail"},
		{"unknown key", logs, "other=1", "", "/other"},
		{"not a number", mixed, "ratio=NaN", "", "/ratio"},
		{"bad item", mixed, "ports=80&ports=x", "", "/ports"},
		{"nested object", mixed, "inner=1", "", "/inner"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q, err := url.ParseQuery(tt.query)
			if err != nil {
				t.Fatal(err)
			}
			got, err := ParamsJSON(tt.typ, q)
			if tt.want != "" {
				if err != nil || !jsonEqual(t, got, []byte(tt.want)) {
					t.Errorf("ParamsJSON(%s) = %s, %v; want %s", tt.query, got, err, tt.want)
				}
				return
			}
			var coded *errcode.Error
			if !errors.As(err, &coded) || coded.Code != errcode.InvalidInput || len(coded.Fields) != 1 || coded.Fields[0].Path != tt.path {
				t.Fatalf("ParamsJSON(%s) = %s, %v; want %s at %s", tt.query, got, err, errcode.InvalidInput, tt.path)
			}
			if strings.Contains(err.Error(), "s3cr3t") {
				t.Errorf("the refusal %v quotes the value", err)
			}
		})
	}
}

func TestValidateType(t *testing.T) {
	v := NewValidator()
	typ := reflect.TypeFor[paramsLogs]()
	if err := v.ValidateType(typ, json.RawMessage(`{"tail":1,"follow":false,"name":[]}`)); err != nil {
		t.Errorf("ValidateType of a valid value = %v", err)
	}
	err := v.ValidateType(typ, json.RawMessage(`{"tail":1,"follow":false,"name":[],"extra":1}`))
	var coded *errcode.Error
	if !errors.As(err, &coded) || coded.Code != errcode.InvalidInput || len(coded.Fields) != 1 || coded.Fields[0].Path != "/extra" {
		t.Fatalf("ValidateType of an unknown member = %v, want %s at /extra", err, errcode.InvalidInput)
	}
	if err := v.ValidateType(reflect.TypeFor[paramsMixed](), json.RawMessage(`{}`)); err != nil {
		t.Errorf("ValidateType of a second type = %v", err)
	}
	if len(v.schemas) != 2 || len(v.types) != 2 {
		t.Errorf("compiled %d schemas for %d types, want 2 and 2", len(v.schemas), len(v.types))
	}
}
