package core_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/schema"
)

var fleetInputs = []any{
	core.HostCreateInput{}, core.HostTokenInput{}, core.HostIDInput{},
	core.UpdateApplyInput{}, core.UpdateRollbackInput{}, core.UpdateStatusInput{}, core.UpdatePolicy{},
	core.RolloutInput{}, core.RolloutCancelInput{}, core.RolloutsInput{},
}

var fleetOutputs = []any{
	core.HostList{}, core.Host{}, core.EnrollmentGrant{},
	core.UpdateTransaction{}, core.UpdateStatus{}, core.UpdatePolicy{},
	core.Rollout{}, core.RolloutList{},
}

func TestFleetTypesHaveSchemas(t *testing.T) {
	for _, v := range fleetInputs {
		if _, err := schema.Input(reflect.TypeOf(v)); err != nil {
			t.Errorf("schema.Input(%T): %v", v, err)
		}
	}
	for _, v := range fleetOutputs {
		if _, err := schema.Output(reflect.TypeOf(v)); err != nil {
			t.Errorf("schema.Output(%T): %v", v, err)
		}
	}
}

var camelCase = regexp.MustCompile(`^[a-z][a-zA-Z0-9]*$`)

// checkJSONNames fails for every exported field reachable from typ whose
// JSON name is missing or not camelCase.
func checkJSONNames(t *testing.T, typ reflect.Type, seen map[reflect.Type]bool) {
	t.Helper()
	for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice || typ.Kind() == reflect.Map {
		typ = typ.Elem()
	}
	if typ.Kind() != reflect.Struct || typ == reflect.TypeFor[time.Time]() || seen[typ] {
		return
	}
	seen[typ] = true
	for i := range typ.NumField() {
		f := typ.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if !camelCase.MatchString(name) {
			t.Errorf("%s.%s: JSON name %q is not camelCase", typ.Name(), f.Name, name)
		}
		checkJSONNames(t, f.Type, seen)
	}
}

func TestFleetJSONNamesAreCamelCase(t *testing.T) {
	seen := map[reflect.Type]bool{}
	for _, v := range append(append([]any{}, fleetInputs...), fleetOutputs...) {
		checkJSONNames(t, reflect.TypeOf(v), seen)
	}
}

func TestEnrollmentTokenIsSecret(t *testing.T) {
	f, ok := reflect.TypeFor[core.EnrollmentGrant]().FieldByName("Token")
	if !ok || f.Tag.Get("secret") != "true" {
		t.Fatal(`EnrollmentGrant.Token is not tagged secret:"true"`)
	}
	out, err := schema.Output(reflect.TypeFor[core.EnrollmentGrant]())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out, []byte(`"x-secret":true`)) {
		t.Errorf("EnrollmentGrant schema has no x-secret property: %s", out)
	}
}

func TestFleetInputPatterns(t *testing.T) {
	v := schema.NewValidator()
	tests := []struct {
		in    any
		raw   string
		valid bool
	}{
		{core.HostCreateInput{}, `{"name":"web-1"}`, true},
		{core.HostCreateInput{}, `{"name":"web-1","controllerAddress":"panel.example.com:7443"}`, true},
		{core.HostCreateInput{}, `{"name":"web-1","controllerAddress":"[fd7a:115c::1]:7443"}`, true},
		{core.HostCreateInput{}, `{"name":"web-1","controllerAddress":"panel.example.com"}`, false},
		{core.HostCreateInput{}, `{"name":"Web"}`, false},
		{core.HostCreateInput{}, `{"name":"-web"}`, false},
		{core.HostCreateInput{}, `{"name":"` + strings.Repeat("a", 63) + `"}`, true},
		{core.HostCreateInput{}, `{"name":"` + strings.Repeat("a", 64) + `"}`, false},
		{core.HostIDInput{}, `{"hostId":""}`, false},
		{core.UpdateApplyInput{}, `{"version":"1.3.0","channel":"stable","force":false}`, true},
		{core.UpdateApplyInput{}, `{"version":"1.3.0-beta.1","channel":"beta","force":true}`, true},
		{core.UpdateApplyInput{}, `{"version":"01.3.0","channel":"stable","force":false}`, false},
		{core.UpdateApplyInput{}, `{"version":"1.3","channel":"stable","force":false}`, false},
		{core.UpdateApplyInput{}, `{"version":"v1.3.0","channel":"stable","force":false}`, false},
		{core.UpdateApplyInput{}, `{"version":"1.3.0","channel":"nightly","force":false}`, false},
		{core.UpdatePolicy{}, `{"hostId":"h1","channel":"beta","schedule":null}`, true},
		{core.UpdatePolicy{}, `{"hostId":"h1","channel":"stable","schedule":{"days":["mon","fri"],"time":"03:30"}}`, true},
		{core.UpdatePolicy{}, `{"hostId":"h1","channel":"stable","schedule":{"days":[],"time":"03:30"}}`, false},
		{core.UpdatePolicy{}, `{"hostId":"h1","channel":"stable","schedule":{"days":["monday"],"time":"03:30"}}`, false},
		{core.UpdatePolicy{}, `{"hostId":"h1","channel":"stable","schedule":{"days":["mon"],"time":"24:00"}}`, false},
		{core.RolloutInput{}, `{"hosts":[],"version":"1.0.0","channel":"stable"}`, false},
		{core.RolloutsInput{}, `{}`, true},
		{core.RolloutsInput{}, `{"limit":0}`, false},
		{core.RolloutsInput{}, `{"limit":101}`, false},
	}
	for _, tt := range tests {
		// The validator caches a compiled schema per action, so each type
		// gets its own name.
		typ := reflect.TypeOf(tt.in)
		def := core.ActionDef{Spec: core.ActionSpec{Name: "core.fleet." + strings.ToLower(typ.Name()), Version: 1}, InputType: typ}
		err := v.ValidateInput(def, json.RawMessage(tt.raw))
		var coded *errcode.Error
		switch {
		case tt.valid && err != nil:
			t.Errorf("%T %s: %v, want valid", tt.in, tt.raw, err)
		case !tt.valid && (!errors.As(err, &coded) || coded.Code != errcode.InvalidInput):
			t.Errorf("%T %s: %v, want %s", tt.in, tt.raw, err, errcode.InvalidInput)
		}
	}
}
