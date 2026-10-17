package schema

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// Validator is the production core.Validator: it checks an input against the
// action's input schema, compiled once per action version.
type Validator struct {
	mu      sync.Mutex
	schemas map[core.ActionRef]*jsonschema.Schema
}

// NewValidator returns an empty validator; schemas compile on first use.
func NewValidator() *Validator {
	return &Validator{schemas: map[core.ActionRef]*jsonschema.Schema{}}
}

// ValidateInput checks raw against def's input schema. A refusal is an
// *errcode.Error with code request.invalid_input and one field error per
// failing value; no message quotes a submitted value, which may be a secret.
func (v *Validator) ValidateInput(def core.ActionDef, raw json.RawMessage) error {
	sch, err := v.compiled(def)
	if err != nil {
		return errcode.Wrap(errcode.Internal, err, "")
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return invalid(errcode.FieldError{Path: "", Message: "input is empty"})
	}
	// UnmarshalJSON refuses trailing data; its error text can quote input
	// bytes, so it is not reported.
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return invalid(errcode.FieldError{Path: "", Message: "input is not a single JSON value"})
	}
	err = sch.Validate(doc)
	var verr *jsonschema.ValidationError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &verr):
		return invalid(leaves(verr, nil)...)
	default:
		return errcode.Wrap(errcode.Internal, err, "")
	}
}

func invalid(fields ...errcode.FieldError) error {
	return &errcode.Error{Code: errcode.InvalidInput, Detail: "input does not match the action's schema", Fields: fields}
}

func (v *Validator) compiled(def core.ActionDef) (*jsonschema.Schema, error) {
	ref := def.Ref()
	v.mu.Lock()
	sch, ok := v.schemas[ref]
	v.mu.Unlock()
	if ok {
		return sch, nil
	}
	raw, err := Input(def.InputType)
	if err != nil {
		return nil, err
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("schema: %s: %w", ref, err)
	}
	url := "urn:darkpanel:input:" + ref.String()
	c := jsonschema.NewCompiler()
	if err := c.AddResource(url, doc); err != nil {
		return nil, fmt.Errorf("schema: %s: %w", ref, err)
	}
	sch, err = c.Compile(url)
	if err != nil {
		return nil, fmt.Errorf("schema: compile %s: %w", ref, err)
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.schemas[ref] = sch
	return sch, nil
}

// leaves turns the failing leaves of a validation error tree into field
// errors.
func leaves(e *jsonschema.ValidationError, out []errcode.FieldError) []errcode.FieldError {
	if len(e.Causes) > 0 {
		for _, c := range e.Causes {
			out = leaves(c, out)
		}
		return out
	}
	at := pointer(e.InstanceLocation)
	switch k := e.ErrorKind.(type) {
	case *kind.Required:
		for _, name := range k.Missing {
			out = append(out, errcode.FieldError{Path: at + "/" + escape(name), Message: "required"})
		}
	case *kind.AdditionalProperties:
		for _, name := range k.Properties {
			out = append(out, errcode.FieldError{Path: at + "/" + escape(name), Message: "unknown property"})
		}
	case *kind.Type:
		out = append(out, errcode.FieldError{Path: at, Message: fmt.Sprintf("got %s, want %s", k.Got, strings.Join(k.Want, " or "))})
	default:
		// The library's own messages quote the failing value, so only the
		// keyword is named.
		out = append(out, errcode.FieldError{Path: at, Message: fmt.Sprintf("value fails the %q constraint", strings.Join(k.KeywordPath(), "/"))})
	}
	return out
}

// pointer renders an instance location as a JSON pointer ("" for the root).
func pointer(tokens []string) string {
	var sb strings.Builder
	for _, tok := range tokens {
		sb.WriteString("/" + escape(tok))
	}
	return sb.String()
}

func escape(tok string) string {
	return strings.ReplaceAll(strings.ReplaceAll(tok, "~", "~0"), "/", "~1")
}
