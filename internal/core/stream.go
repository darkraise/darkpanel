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

// ChannelDef is a duplex channel of a module, "<module>.<name>" (spec 1.6):
// frames both ways over one connection, such as a terminal. Channels are not
// listed in an agent's hello, and the controller decides who may open one.
type ChannelDef struct {
	Name       string       // "terminal"; the channel is "<module>.<name>"
	ParamsType reflect.Type // validated like action input (query parameters decoded to JSON)
	// Open serves the channel until it returns; the runtime then closes c.
	// ctx ends when the channel is closed from either side or the runtime
	// closes it.
	Open func(ctx context.Context, env Env, params json.RawMessage, c ChannelConn) error
}

// Channeler is implemented by a module with channels, found with a type
// assertion on the Module.
type Channeler interface{ Channels() []ChannelDef }

// ChannelConn is one open channel. ReadFrame returns the peer's next frame;
// a frame the codec refuses closes the channel with reason "bad frame".
// WriteFrame is safe for concurrent use. Close sends a close frame with
// reason and closes the connection; later calls do nothing.
type ChannelConn interface {
	ReadFrame() (byte, []byte, error)
	WriteFrame(t byte, payload []byte) error
	Close(reason string) error
}

// DefineChannel builds a ChannelDef from typed parameters. Open decodes the
// parameters like an action input, refusing unknown members.
func DefineChannel[P any](name string, open func(ctx context.Context, env Env, p P, c ChannelConn) error) ChannelDef {
	return ChannelDef{
		Name: name, ParamsType: typeOf[P](),
		Open: func(ctx context.Context, env Env, params json.RawMessage, c ChannelConn) error {
			p, err := decode[P](params)
			if err != nil {
				return err
			}
			return open(ctx, env, p, c)
		},
	}
}
