package coretest

import (
	"errors"
	"reflect"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

type validatorInput struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

func TestValidator(t *testing.T) {
	def := core.ActionDef{InputType: reflect.TypeFor[validatorInput]()}
	if err := (Validator{}).ValidateInput(def, []byte(`{"name":"a","count":1}`)); err != nil {
		t.Errorf("valid input: %v", err)
	}
	for _, raw := range []string{``, `{"name":"a","extra":1}`, `{"count":"one"}`, `{} {}`, `{}}`, `{} ]`} {
		err := (Validator{}).ValidateInput(def, []byte(raw))
		var coded *errcode.Error
		if !errors.As(err, &coded) || coded.Code != errcode.InvalidInput || len(coded.Fields) != 1 {
			t.Errorf("ValidateInput(%q) = %v, want %s with one field error", raw, err, errcode.InvalidInput)
		}
	}
}
