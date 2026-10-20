package dispatch

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/schemas"
)

// ReasonSchemaUnknown is why an action version is unavailable on a host
// whose agent reports a schema hash this controller cannot map to a schema
// (spec section 5).
const ReasonSchemaUnknown = "agent schema unknown"

// negotiation is what the schema-hash rule compares an agent's Hello with.
type negotiation struct {
	current  map[string]core.SchemaHash                        // this binary's agent actions, by schemas.HashKey
	released map[string]map[core.SchemaHash]*jsonschema.Schema // released snapshots' compiled input schemas, by key and hashes
}

// newNegotiation hashes reg's agent actions and compiles the input schema of
// every released action snapshot in snapshots.
func newNegotiation(reg *core.Registry, snapshots fs.FS) (negotiation, error) {
	current, err := schemas.ActionHashes(reg, core.SideAgent)
	if err != nil {
		return negotiation{}, err
	}
	n := negotiation{current: current, released: map[string]map[core.SchemaHash]*jsonschema.Schema{}}
	err = fs.WalkDir(snapshots, "released", func(name string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// released/<tag>/<action>/v<n>.json
		parts := strings.Split(name, "/")
		if e.IsDir() || len(parts) != 4 || path.Ext(name) != ".json" {
			return nil
		}
		version, err := core.ParseVersion(strings.TrimSuffix(parts[3], ".json"))
		if err != nil {
			return nil
		}
		return n.add(snapshots, name, core.ActionRef{Name: parts[2], Version: version})
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return negotiation{}, err
	}
	return n, nil
}

// add compiles the input schema of the released snapshot at name. A resource
// snapshot has no input and names no action, so it is skipped.
func (n negotiation) add(snapshots fs.FS, name string, ref core.ActionRef) error {
	raw, err := fs.ReadFile(snapshots, name)
	if err != nil {
		return err
	}
	var snap struct {
		Input json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(raw, &snap); err != nil {
		return fmt.Errorf("dispatch: snapshot %s: %w", name, err)
	}
	if snap.Input == nil {
		return nil
	}
	hash, err := schemas.SnapshotHashes(raw)
	if err != nil {
		return fmt.Errorf("dispatch: snapshot %s: %w", name, err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(snap.Input))
	if err != nil {
		return fmt.Errorf("dispatch: snapshot %s: %w", name, err)
	}
	closeObjects(doc)
	c := jsonschema.NewCompiler()
	c.AssertFormat()
	url := "urn:darkpanel:" + name
	if err := c.AddResource(url, doc); err != nil {
		return fmt.Errorf("dispatch: snapshot %s: %w", name, err)
	}
	sch, err := c.Compile(url)
	if err != nil {
		return fmt.Errorf("dispatch: compile snapshot %s: %w", name, err)
	}
	key := schemas.HashKey(ref)
	if n.released[key] == nil {
		n.released[key] = map[core.SchemaHash]*jsonschema.Schema{}
	}
	n.released[key][hash] = sch
	return nil
}

// closeObjects refuses unknown properties in every object schema of doc that
// does not decide that itself, so validation against an older schema names
// every field the older agent lacks.
func closeObjects(doc any) {
	switch v := doc.(type) {
	case map[string]any:
		if _, ok := v["properties"]; ok {
			if _, set := v["additionalProperties"]; !set {
				v["additionalProperties"] = false
			}
		}
		for _, child := range v {
			closeObjects(child)
		}
	case []any:
		for _, child := range v {
			closeObjects(child)
		}
	}
}

// negotiate applies the schema-hash rule to def on hostID, as its last Hello
// describes the agent (spec section 5): an equal hash dispatches; a hash of a
// released snapshot of def dispatches unless input has a field that
// snapshot's schema lacks; any other hash, or none, leaves def unavailable
// there. A nil input, as an apply has, is not validated.
func (d *Dispatcher) negotiate(hostID string, def core.ActionDef, input json.RawMessage) error {
	hello, ok := d.cfg.Hosts.greetingOf(hostID)
	if !ok {
		return fmt.Errorf("dispatch: host %s has not said hello", hostID)
	}
	key := schemas.HashKey(def.Ref())
	got, reported := hello.SchemaHashes[key]
	if reported && got == d.neg.current[key] {
		return nil
	}
	if older, found := d.neg.released[key][got]; reported && found {
		if input == nil {
			return nil
		}
		return unknownField(hostID, def.Ref(), older, input)
	}
	return errcode.New(errcode.ModuleUnavailable,
		fmt.Sprintf("action %s is unavailable on host %s: %s", def.Ref(), hostID, ReasonSchemaUnknown))
}

// unknownField validates input against an older agent's input schema and
// answers request.invalid_input naming the first field, in JSON pointer
// order, that the schema lacks. Every other mismatch is left to the agent,
// whose own validation names each failing field.
func unknownField(hostID string, ref core.ActionRef, older *jsonschema.Schema, input json.RawMessage) error {
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(input))
	if err != nil {
		return nil
	}
	var verr *jsonschema.ValidationError
	if !errors.As(older.Validate(inst), &verr) {
		return nil
	}
	fields := unknownPaths(verr, nil)
	if len(fields) == 0 {
		return nil
	}
	slices.Sort(fields)
	return &errcode.Error{
		Code:   errcode.InvalidInput,
		Detail: fmt.Sprintf("host %s runs an older %s whose input has no field %s", hostID, ref, fields[0]),
		Fields: []errcode.FieldError{{Path: fields[0], Message: "unknown to the agent's version of the action"}},
	}
}

func unknownPaths(e *jsonschema.ValidationError, out []string) []string {
	for _, c := range e.Causes {
		out = unknownPaths(c, out)
	}
	if k, ok := e.ErrorKind.(*kind.AdditionalProperties); ok {
		at := jsonPointer(e.InstanceLocation)
		for _, p := range k.Properties {
			out = append(out, at+"/"+escapePointer(p))
		}
	}
	return out
}

func jsonPointer(tokens []string) string {
	var sb strings.Builder
	for _, tok := range tokens {
		sb.WriteString("/" + escapePointer(tok))
	}
	return sb.String()
}

func escapePointer(tok string) string {
	return strings.ReplaceAll(strings.ReplaceAll(tok, "~", "~0"), "/", "~1")
}
