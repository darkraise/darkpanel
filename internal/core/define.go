package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// decode parses raw into a T, refusing unknown fields and trailing data. An
// empty body decodes as the zero value; callers validate the input against
// its schema first, and validators refuse an empty body.
func decode[T any](raw json.RawMessage) (T, error) {
	var v T
	if len(bytes.TrimSpace(raw)) == 0 {
		return v, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		// A JSON syntax error quotes input bytes, which may belong to a secret
		// field, so the cause stays out of the client-visible detail.
		return v, errcode.Wrap(errcode.InvalidInput, err, "input does not match the action's input type")
	}
	if dec.More() {
		return v, errcode.New(errcode.InvalidInput, "trailing data after the JSON value")
	}
	return v, nil
}

func encode(v any) (json.RawMessage, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("encode output: %w", err)
	}
	return b, nil
}

func typeOf[T any]() reflect.Type { return reflect.TypeFor[T]() }

// DefineRead declares a read action.
func DefineRead[In, Out any](spec ActionSpec, h Reader[In, Out]) ActionDef {
	spec.Class = ClassRead
	return ActionDef{
		Spec: spec, InputType: typeOf[In](), OutputType: typeOf[Out](),
		Handler: ErasedHandler{
			Read: func(ctx context.Context, env Env, raw json.RawMessage) (json.RawMessage, error) {
				in, err := decode[In](raw)
				if err != nil {
					return nil, err
				}
				out, err := h.Read(ctx, env, in)
				if err != nil {
					return nil, err
				}
				return encode(out)
			},
		},
	}
}

func mutatorHandler[In, Out any](h Mutator[In, Out]) ErasedHandler {
	return ErasedHandler{
		Plan: func(ctx context.Context, env Env, raw json.RawMessage) (PlanOutcome, error) {
			in, err := decode[In](raw)
			if err != nil {
				return PlanOutcome{}, err
			}
			return h.Plan(ctx, env, in)
		},
		Apply: func(ctx context.Context, env Env, raw json.RawMessage) (json.RawMessage, error) {
			in, err := decode[In](raw)
			if err != nil {
				return nil, err
			}
			out, err := h.Apply(ctx, env, in)
			if err != nil {
				return nil, err
			}
			return encode(out)
		},
		Verify: func(ctx context.Context, env Env, rawIn, rawOut json.RawMessage) error {
			in, err := decode[In](rawIn)
			if err != nil {
				return err
			}
			var out Out
			if err := json.Unmarshal(rawOut, &out); err != nil {
				return fmt.Errorf("decode output for verify: %w", err)
			}
			return h.Verify(ctx, env, in, out)
		},
	}
}

// DefineMutating declares a compensable or irreversible action; spec.Class
// must be one of the two.
func DefineMutating[In, Out any](spec ActionSpec, h Mutator[In, Out]) ActionDef {
	return ActionDef{Spec: spec, InputType: typeOf[In](), OutputType: typeOf[Out](), Handler: mutatorHandler(h)}
}

// DefineRevertible declares a revertible action.
func DefineRevertible[In, Out any](spec ActionSpec, h Reverter[In, Out]) ActionDef {
	spec.Class = ClassRevertible
	eh := mutatorHandler[In, Out](h)
	eh.Snapshot = func(ctx context.Context, env Env, raw json.RawMessage) ([]byte, error) {
		in, err := decode[In](raw)
		if err != nil {
			return nil, err
		}
		return h.Snapshot(ctx, env, in)
	}
	eh.Revert = h.Revert
	return ActionDef{Spec: spec, InputType: typeOf[In](), OutputType: typeOf[Out](), Handler: eh}
}

// DefineResource declares a resource named "<module>.<resource>".
func DefineResource[Out any](name string, read func(ctx context.Context, env Env) (Out, error)) ResourceDef {
	return ResourceDef{
		Name: name, OutputType: typeOf[Out](),
		Read: func(ctx context.Context, env Env) (json.RawMessage, error) {
			out, err := read(ctx, env)
			if err != nil {
				return nil, err
			}
			return encode(out)
		},
	}
}
