package schema

import (
	"encoding/json"
	"errors"
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
