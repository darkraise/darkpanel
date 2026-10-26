package core_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/schema"
)

// gcIn is the input of a guarded action.
type gcIn struct {
	core.Guarded
	Name string `json:"name"`
}

type gcReader struct{}

func (gcReader) Read(context.Context, core.Env, gcIn) (struct{}, error) { return struct{}{}, nil }

func TestGuardContextComplete(t *testing.T) {
	for _, tc := range []struct {
		g    core.GuardContext
		want string
	}{
		{core.GuardContext{}, `{}`},
		{core.GuardContext{Complete: true}, `{"complete":true}`},
	} {
		b, err := json.Marshal(tc.g)
		if err != nil || string(b) != tc.want {
			t.Errorf("json.Marshal(%+v) = %s, %v; want %s", tc.g, b, err, tc.want)
		}
		var back core.GuardContext
		if err := json.Unmarshal(b, &back); err != nil || back.Complete != tc.g.Complete {
			t.Errorf("json.Unmarshal(%s) = %+v, %v", b, back, err)
		}
	}

	// The agent validates a guarded input against its schema, which refuses
	// members it does not declare, so the schema must carry complete.
	v := schema.NewValidator()
	def := core.DefineRead(core.ActionSpec{Name: "demo.guarded.get", Version: 1, Side: core.SideAgent}, gcReader{})
	if err := v.ValidateInput(def, json.RawMessage(`{"name":"x","guard":{"complete":true,"panelListen":["0.0.0.0:8443"]}}`)); err != nil {
		t.Errorf("a complete guard context: %v, want nil", err)
	}
	err := v.ValidateInput(def, json.RawMessage(`{"name":"x","guard":{"complete":"yes"}}`))
	var coded *errcode.Error
	if !errors.As(err, &coded) || coded.Code != errcode.InvalidInput {
		t.Errorf("complete as a string: %v, want %s", err, errcode.InvalidInput)
	}
}
