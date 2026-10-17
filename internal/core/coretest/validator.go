package coretest

import (
	"bytes"
	"encoding/json"
	"reflect"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// Validator is a core.Validator that decodes the input strictly into the
// action's input type: empty input, unknown fields, wrong types and trailing
// data fail with request.invalid_input. It checks no patterns, enums or
// ranges; the schema package's validator does.
type Validator struct{}

// ValidateInput checks raw against def's input type.
func (Validator) ValidateInput(def core.ActionDef, raw json.RawMessage) error {
	invalid := func(msg string) error {
		return &errcode.Error{
			Code:   errcode.InvalidInput,
			Detail: "input does not match the action's schema",
			Fields: []errcode.FieldError{{Path: "", Message: msg}},
		}
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return invalid("input is empty")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	// The decoder's error text can quote input bytes, so it is not reported.
	if err := dec.Decode(reflect.New(def.InputType).Interface()); err != nil {
		return invalid("input does not match the action's input type")
	}
	if dec.More() {
		return invalid("trailing data after the JSON value")
	}
	return nil
}
