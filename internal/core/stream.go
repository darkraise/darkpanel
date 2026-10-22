package core

import (
	"context"
	"encoding/json"
	"reflect"
)

// StreamDef is a read-only event stream of a module, "<module>.<name>",
// served with scope read.
type StreamDef struct {
	Name       string       // "logs"; the stream is "<module>.<name>"
	ParamsType reflect.Type // validated like action input (query parameters decoded to JSON)
	EventType  reflect.Type // one event; its schema is part of the hash
	// Run emits events until it returns. emit fails once the reader is
	// gone or a limit ended the stream; Run must then return.
	Run func(ctx context.Context, env Env, params json.RawMessage, emit func(event any) error) error
}

// Streamer is implemented by a module with streams, found with a type
// assertion on the Module.
type Streamer interface{ Streams() []StreamDef }

// StreamInfo is one stream an agent serves, as GET /agent/v1/streams lists it.
type StreamInfo struct {
	Module     string `json:"module"`
	Name       string `json:"name"`
	SchemaHash string `json:"schemaHash"`
}

// DefineStream builds a StreamDef from typed parameters and events. Run
// decodes the parameters like an action input, refusing unknown members.
func DefineStream[P, E any](name string, run func(ctx context.Context, env Env, p P, emit func(E) error) error) StreamDef {
	return StreamDef{
		Name: name, ParamsType: typeOf[P](), EventType: typeOf[E](),
		Run: func(ctx context.Context, env Env, params json.RawMessage, emit func(event any) error) error {
			p, err := decode[P](params)
			if err != nil {
				return err
			}
			return run(ctx, env, p, func(e E) error { return emit(e) })
		},
	}
}
