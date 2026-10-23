package schema

import (
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// typeNames maps each bare type ValidateType has seen to its key in the
// schema cache.
type typeNames map[reflect.Type]string

// ValidateType is ValidateInput for a bare type, compiled once per type.
func (v *Validator) ValidateType(t reflect.Type, raw json.RawMessage) error {
	return v.ValidateInput(core.ActionDef{Spec: core.ActionSpec{Name: v.typeName(t)}, InputType: t}, raw)
}

// typeName is t's key in the schema cache, "type-<n>". No action can take
// it: an action name has three dotted segments.
func (v *Validator) typeName(t reflect.Type) string {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.types == nil {
		v.types = typeNames{}
	}
	name, ok := v.types[t]
	if !ok {
		name = fmt.Sprintf("type-%d", len(v.types)+1)
		v.types[t] = name
	}
	return name
}

// paramsNode is the part of an input schema ParamsJSON reads.
type paramsNode struct {
	Ref        string                 `json:"$ref"`
	Type       json.RawMessage        `json:"type"`
	Items      *paramsNode            `json:"items"`
	Properties map[string]*paramsNode `json:"properties"`
}

// typeName is the node's JSON type: its type keyword, or the one type other
// than null a list of types names; "" when it names none.
func (n *paramsNode) typeName() string {
	var one string
	if json.Unmarshal(n.Type, &one) == nil {
		return one
	}
	var many []string
	_ = json.Unmarshal(n.Type, &many)
	var types []string
	for _, t := range many {
		if t != "null" {
			types = append(types, t)
		}
	}
	if len(types) == 1 {
		return types[0]
	}
	return ""
}

// ParamsJSON turns query parameters into the JSON object t's input schema
// describes: a string, integer, number or boolean property from one value,
// an array property from every value; an unknown key or a value of the
// wrong type is request.invalid_input. Required members and value
// constraints are left to the input schema's validation (ValidateType).
func ParamsJSON(t reflect.Type, q url.Values) (json.RawMessage, error) {
	raw, err := Input(t)
	if err != nil {
		return nil, err
	}
	var doc struct {
		paramsNode
		Defs map[string]*paramsNode `json:"$defs"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("schema: parameters of %s: %w", t, err)
	}
	resolve := func(n *paramsNode) *paramsNode {
		if name, ok := strings.CutPrefix(n.Ref, "#/$defs/"); ok && doc.Defs[name] != nil {
			return doc.Defs[name]
		}
		return n
	}
	root := resolve(&doc.paramsNode)
	obj := make(map[string]json.RawMessage, len(q))
	for _, key := range slices.Sorted(maps.Keys(q)) {
		prop, ok := root.Properties[key]
		if !ok {
			return nil, invalidParam(key, "unknown parameter")
		}
		prop = resolve(prop)
		values := q[key]
		var v json.RawMessage
		if prop.typeName() == "array" && prop.Items != nil {
			items := make([]json.RawMessage, len(values))
			for i, s := range values {
				if items[i], err = paramValue(resolve(prop.Items).typeName(), s); err != nil {
					return nil, invalidParam(key, err.Error())
				}
			}
			if v, err = json.Marshal(items); err != nil {
				return nil, fmt.Errorf("schema: parameter %s: %w", key, err)
			}
		} else {
			if len(values) != 1 {
				return nil, invalidParam(key, "give it once")
			}
			if v, err = paramValue(prop.typeName(), values[0]); err != nil {
				return nil, invalidParam(key, err.Error())
			}
		}
		obj[key] = v
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return nil, fmt.Errorf("schema: parameters of %s: %w", t, err)
	}
	return out, nil
}

// paramValue is the JSON of one query value s for a property of JSON type
// typ. Its errors never quote s, which may be a secret.
func paramValue(typ, s string) (json.RawMessage, error) {
	switch typ {
	case "string":
		return json.Marshal(s)
	case "integer":
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("want an integer")
		}
		return strconv.AppendInt(nil, n, 10), nil
	case "number":
		f, err := strconv.ParseFloat(s, 64)
		if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
			return nil, fmt.Errorf("want a number")
		}
		return json.Marshal(f)
	case "boolean":
		if s != "true" && s != "false" {
			return nil, fmt.Errorf("want true or false")
		}
		return json.RawMessage(s), nil
	}
	return nil, fmt.Errorf("cannot be given as a query parameter")
}

func invalidParam(key, msg string) error {
	return &errcode.Error{Code: errcode.InvalidInput, Detail: fmt.Sprintf("query parameter %s: %s", key, msg),
		Fields: []errcode.FieldError{{Path: "/" + escape(key), Message: msg}}}
}
