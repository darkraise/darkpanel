package schema

import (
	"encoding/json"
	"reflect"

	"github.com/darkraise/darkpanel/internal/core/secret"
)

// Redact returns raw, a JSON value of type t, with every field tagged
// secret:"true" replaced by "[redacted]" (spec section 5). It is the
// lifecycle engine's Config.Redact on both sides, for inputs, change outputs
// and read outputs alike. When t has no output schema the whole value is
// redacted, never returned as is.
func Redact(t reflect.Type, raw json.RawMessage) json.RawMessage {
	s, err := Output(t)
	if err != nil {
		return json.RawMessage(`"` + secret.Redacted + `"`)
	}
	return secret.RedactJSON(raw, s)
}
