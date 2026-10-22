package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"

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
	// More reports false before a stray '}' or ']', so only io.EOF proves the input ended.
	if !errors.Is(dec.Decode(new(json.RawMessage)), io.EOF) {
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

func erasedPlan[In any](plan func(ctx context.Context, env Env, in In) (PlanOutcome, error)) func(context.Context, Env, json.RawMessage) (PlanOutcome, error) {
	return func(ctx context.Context, env Env, raw json.RawMessage) (PlanOutcome, error) {
		in, err := decode[In](raw)
		if err != nil {
			return PlanOutcome{}, err
		}
		return plan(ctx, env, in)
	}
}

func erasedRun[In, Out any](run func(ctx context.Context, env Env, in In) (Out, error)) func(context.Context, Env, json.RawMessage) (json.RawMessage, error) {
	return func(ctx context.Context, env Env, raw json.RawMessage) (json.RawMessage, error) {
		in, err := decode[In](raw)
		if err != nil {
			return nil, err
		}
		out, err := run(ctx, env, in)
		if err != nil {
			return nil, err
		}
		return encode(out)
	}
}

func erasedVerify[In, Out any](verify func(ctx context.Context, env Env, in In, out Out) error) func(context.Context, Env, json.RawMessage, json.RawMessage) error {
	return func(ctx context.Context, env Env, rawIn, rawOut json.RawMessage) error {
		in, err := decode[In](rawIn)
		if err != nil {
			return err
		}
		var out Out
		if err := json.Unmarshal(rawOut, &out); err != nil {
			return fmt.Errorf("decode output for verify: %w", err)
		}
		return verify(ctx, env, in, out)
	}
}

// findHandlers fills the optional lifecycle handlers h implements, found by
// type assertion as Uninstaller is on a Module. Effects and observations
// travel unchanged, so the methods are used as they are.
func findHandlers(eh *ErasedHandler, h any) {
	if o, ok := h.(Observer); ok {
		eh.Observe = o.Observe
	}
	if r, ok := h.(Resolver); ok {
		eh.Resolved = r.Resolved
	}
	if u, ok := h.(Undoer); ok {
		eh.Inverse = u.Inverse
	}
	if r, ok := h.(Recoverer); ok {
		eh.Recover = r.Recover
	}
}

// DefineRead declares a read action.
func DefineRead[In, Out any](spec ActionSpec, h Reader[In, Out]) ActionDef {
	spec.Class = ClassRead
	return ActionDef{
		Spec: spec, InputType: typeOf[In](), OutputType: typeOf[Out](),
		Handler: ErasedHandler{Read: erasedRun(h.Read)},
	}
}

func mutatorHandler[In, Out any](h Mutator[In, Out]) ErasedHandler {
	eh := ErasedHandler{Plan: erasedPlan(h.Plan), Apply: erasedRun(h.Apply), Verify: erasedVerify(h.Verify)}
	findHandlers(&eh, h)
	return eh
}

// DefineMutating declares a compensable or irreversible action; spec.Class
// must be one of the two. A handler that also implements Observer, Resolver,
// Undoer or Recoverer gets the matching handler.
func DefineMutating[In, Out any](spec ActionSpec, h Mutator[In, Out]) ActionDef {
	return ActionDef{Spec: spec, InputType: typeOf[In](), OutputType: typeOf[Out](), Handler: mutatorHandler(h)}
}

// DefineRevertible declares a revertible action. A handler that also
// implements Observer, Resolver, Undoer or Recoverer gets the matching
// handler.
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

// DefineComposite declares a composite: Class composite, Side controller,
// LockKeys ["composite:<module>"], spec.Unwind kept. It has no Plan, Apply or
// Verify handler; the engine plans, applies and recovers it from Steps.
func DefineComposite[In, Out any](spec ActionSpec, h Composer[In, Out]) ActionDef {
	module, _, _ := strings.Cut(spec.Name, ".")
	spec.Class, spec.Side = ClassComposite, SideController
	spec.LockKeys = []string{"composite:" + module}
	return ActionDef{
		Spec: spec, InputType: typeOf[In](), OutputType: typeOf[Out](),
		Handler: ErasedHandler{
			Steps: func(ctx context.Context, env Env, raw json.RawMessage) ([]Step, error) {
				in, err := decode[In](raw)
				if err != nil {
					return nil, err
				}
				return h.Steps(ctx, env, in)
			},
			Output: func(ctx context.Context, env Env, raw json.RawMessage, steps map[string]json.RawMessage) (json.RawMessage, error) {
				in, err := decode[In](raw)
				if err != nil {
					return nil, err
				}
				out, err := h.Output(ctx, env, in, steps)
				if err != nil {
					return nil, err
				}
				return encode(out)
			},
		},
	}
}

// DefineTask declares a task action, whose host sequences run in transient
// units, and sets Task and LongRunning. spec.Class stays the caller's:
// compensable or irreversible. It has a Task handler in place of Apply. A
// handler that also implements Observer, Resolver, Undoer or Recoverer gets
// the matching handler; without a Recoverer the engine's task sequence is
// all the recovery there is.
func DefineTask[In, Out any](spec ActionSpec, h TaskMutator[In, Out]) ActionDef {
	spec.Task, spec.LongRunning = true, true
	eh := ErasedHandler{Plan: erasedPlan(h.Plan), Task: erasedRun(h.Task), Verify: erasedVerify(h.Verify)}
	findHandlers(&eh, h)
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
