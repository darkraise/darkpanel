package schema

import (
	"encoding/json"
	"errors"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

var _ core.Validator = (*Validator)(nil)

const validInput = `{"name":"web","mode":"fast","password":"hunter2hunter2",` +
	`"target":{"host":"example.org","port":443},"backups":[{"host":"b.example.org","port":22}]}`

func TestValidateAccepts(t *testing.T) {
	v := NewValidator()
	def := fixtureAction(t, "demo.item.add")
	for _, raw := range []string{validInput, ` ` + validInput + "\n"} {
		if err := v.ValidateInput(def, json.RawMessage(raw)); err != nil {
			t.Errorf("ValidateInput(%s) = %v, want nil", raw, err)
		}
	}
}

func TestValidateRefuses(t *testing.T) {
	v := NewValidator()
	def := fixtureAction(t, "demo.item.add")
	tests := []struct {
		name, raw, path, message string
	}{
		{"empty", ``, "", "input is empty"},
		{"not json", `{"name":`, "", "input is not a single JSON value"},
		{"trailing value", validInput + ` {}`, "", "input is not a single JSON value"},
		{"trailing bracket", validInput + `}`, "", "input is not a single JSON value"},
		{"missing field", `{"mode":"fast","password":"p","target":{"host":"a","port":1}}`, "/name", "required"},
		{"unknown field", strings.Replace(validInput, `"mode"`, `"extra":1,"mode"`, 1), "/extra", "unknown property"},
		{"wrong type", strings.Replace(validInput, `"name":"web"`, `"name":7`, 1), "/name", "got number, want string"},
		{"pattern", strings.Replace(validInput, `"name":"web"`, `"name":"Web Server"`, 1), "/name", `value fails the "pattern" constraint`},
		{"enum", strings.Replace(validInput, `"fast"`, `"slow"`, 1), "/mode", `value fails the "enum" constraint`},
		{"nested range", strings.Replace(validInput, `"port":443`, `"port":0`, 1), "/target/port", `value fails the "minimum" constraint`},
		{"array element", strings.Replace(validInput, `"port":22`, `"port":22,"zone":"x"`, 1), "/backups/0/zone", "unknown property"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := v.ValidateInput(def, json.RawMessage(tt.raw))
			var coded *errcode.Error
			if !errors.As(err, &coded) || coded.Code != errcode.InvalidInput {
				t.Fatalf("err = %v, want %s", err, errcode.InvalidInput)
			}
			found := false
			for _, f := range coded.Fields {
				if f.Path == tt.path && f.Message == tt.message {
					found = true
				}
			}
			if !found {
				t.Errorf("fields = %+v, want {%s %s}", coded.Fields, tt.path, tt.message)
			}
		})
	}
}

func TestValidateNeverQuotesValues(t *testing.T) {
	v := NewValidator()
	def := fixtureAction(t, "demo.item.add")
	raw := strings.Replace(validInput, `"name":"web"`, `"name":"S3cr3t Value"`, 1)
	raw = strings.Replace(raw, `"password":"hunter2hunter2"`, `"password":["hunter2hunter2"]`, 1)
	err := v.ValidateInput(def, json.RawMessage(raw))
	var coded *errcode.Error
	if !errors.As(err, &coded) || len(coded.Fields) < 2 {
		t.Fatalf("err = %v, want two field errors", err)
	}
	for _, text := range append([]string{coded.Error()}, fieldTexts(coded.Fields)...) {
		if strings.Contains(text, "S3cr3t") || strings.Contains(text, "hunter2") {
			t.Errorf("error text quotes the submitted value: %s", text)
		}
	}
}

type formatInput struct {
	Addr string `json:"addr" jsonschema:"format=ipv4"`
	At   string `json:"at" jsonschema:"format=date-time"`
}

func TestValidateAssertsFormat(t *testing.T) {
	v := NewValidator()
	def := core.DefineRead(core.ActionSpec{Name: "demo.format.get", Version: 1, Side: core.SideAgent},
		fixtureHandler[formatInput, fixtureStatus]{})
	if err := v.ValidateInput(def, json.RawMessage(`{"addr":"192.0.2.1","at":"2026-10-17T12:00:00Z"}`)); err != nil {
		t.Fatalf("valid formats: %v", err)
	}
	err := v.ValidateInput(def, json.RawMessage(`{"addr":"192.0.2.300","at":"yesterday"}`))
	var coded *errcode.Error
	if !errors.As(err, &coded) || coded.Code != errcode.InvalidInput {
		t.Fatalf("err = %v, want %s", err, errcode.InvalidInput)
	}
	want := []errcode.FieldError{
		{Path: "/addr", Message: `value fails the "format" constraint`},
		{Path: "/at", Message: `value fails the "format" constraint`},
	}
	for _, w := range want {
		if !slices.Contains(coded.Fields, w) {
			t.Errorf("fields = %+v, want %+v", coded.Fields, w)
		}
	}
}

func fieldTexts(fields []errcode.FieldError) []string {
	var out []string
	for _, f := range fields {
		out = append(out, f.Path+" "+f.Message)
	}
	return out
}

func TestValidateCompilesOnce(t *testing.T) {
	v := NewValidator()
	def := fixtureAction(t, "demo.item.add")
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if err := v.ValidateInput(def, json.RawMessage(validInput)); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if err := v.ValidateInput(fixtureAction(t, "demo.secret.reset"), json.RawMessage(`{}`)); err != nil {
		t.Errorf("empty object input: %v", err)
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if len(v.schemas) != 2 {
		t.Errorf("compiled schemas = %d, want 2", len(v.schemas))
	}
}

func TestValidatorChecksAddressFormats(t *testing.T) {
	v := NewValidator()
	def := core.DefineRead(core.ActionSpec{Name: "demo.addr.get", Version: 1, Side: core.SideAgent},
		fixtureHandler[addrFields, fixtureStatus]{})
	const formatFails = `value fails the "format" constraint`
	tests := []struct {
		name, raw     string
		path, message string // "" when the input is valid
	}{
		{"IPv4 address", `{"a":"192.0.2.1"}`, "", ""},
		{"IPv6 address", `{"a":"2001:db8::1"}`, "", ""},
		{"IPv6 address and port", `{"p":"[2001:db8::1]:443"}`, "", ""},
		{"IPv4 address and port", `{"p":"192.0.2.1:22"}`, "", ""},
		{"IPv4 prefix", `{"n":"10.0.0.0/8"}`, "", ""},
		{"IPv6 prefix", `{"n":"2001:db8::/32"}`, "", ""},
		{"list of addresses and ports", `{"l":["192.0.2.1:22","[::1]:80"]}`, "", ""},
		{"address out of range", `{"a":"300.1.1.1"}`, "/a", formatFails},
		{"address without a port", `{"p":"192.0.2.1"}`, "/p", formatFails},
		{"prefix too long", `{"n":"10.0.0.0/33"}`, "/n", formatFails},
		{"empty address", `{"a":""}`, "/a", formatFails},
		{"bad list element", `{"l":["192.0.2.1:22","x"]}`, "/l/1", formatFails},
		{"number for an address", `{"a":7}`, "/a", "got number, want string"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := v.ValidateInput(def, json.RawMessage(tt.raw))
			if tt.path == "" {
				if err != nil {
					t.Fatalf("ValidateInput(%s) = %v, want nil", tt.raw, err)
				}
				return
			}
			var coded *errcode.Error
			if !errors.As(err, &coded) || coded.Code != errcode.InvalidInput {
				t.Fatalf("err = %v, want %s", err, errcode.InvalidInput)
			}
			if want := (errcode.FieldError{Path: tt.path, Message: tt.message}); !slices.Contains(coded.Fields, want) {
				t.Errorf("fields = %+v, want %+v", coded.Fields, want)
			}
		})
	}
}

func TestValidatorAcceptsUnsetAddressFields(t *testing.T) {
	v := NewValidator()
	def := core.DefineRead(core.ActionSpec{Name: "demo.addr.get", Version: 1, Side: core.SideAgent},
		fixtureHandler[addrFields, fixtureStatus]{})
	raw, err := json.Marshal(addrFields{A: netip.MustParseAddr("192.0.2.1")})
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"a":"192.0.2.1"}` {
		t.Errorf("Marshal = %s, want only a", raw)
	}
	if err := v.ValidateInput(def, raw); err != nil {
		t.Errorf("ValidateInput(%s) = %v, want nil", raw, err)
	}
}
