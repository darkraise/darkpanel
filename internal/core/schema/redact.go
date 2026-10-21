package schema

import (
	"encoding/json"
	"reflect"
	"sync"

	"github.com/darkraise/darkpanel/internal/core/secret"
)

// outputSchemas caches Output's result, schema or error, per type for Redact,
// which runs on every read, plan and change.
var outputSchemas sync.Map // reflect.Type -> cachedSchema

type cachedSchema struct {
	schema json.RawMessage
	err    error
}

// Redact returns raw, a JSON value of type t, with every field tagged
// secret:"true" replaced by "[redacted]" (spec section 5). It is the
// lifecycle engine's Config.Redact on both sides, for inputs, change outputs
// and read outputs alike. When t has no output schema the whole value is
// redacted, never returned as is.
func Redact(t reflect.Type, raw json.RawMessage) json.RawMessage {
	s, err := cachedOutput(t)
	if err != nil {
		return json.RawMessage(`"` + secret.Redacted + `"`)
	}
	return secret.RedactJSON(raw, s)
}

func cachedOutput(t reflect.Type) (json.RawMessage, error) {
	if c, ok := outputSchemas.Load(t); ok {
		cs := c.(cachedSchema)
		return cs.schema, cs.err
	}
	s, err := Output(t)
	outputSchemas.Store(t, cachedSchema{s, err})
	return s, err
}
