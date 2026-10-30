// Package policy edits a tailnet policy file held as HuJSON text. Edits keep
// the comments and layout of everything they do not touch, and each kind
// converges: applying the same edit to its own result changes nothing.
package policy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/tailscale/hujson"
)

// Edit is one change to a policy document.
type Edit struct {
	Op    string          `json:"op"`              // set | delete | addItem | removeItem
	Path  string          `json:"path"`            // JSON Pointer to an object member (set, delete) or an array (addItem, removeItem)
	Value json.RawMessage `json:"value,omitempty"` // set, addItem, removeItem
}

// refusal is the client-safe reason an edit cannot be applied.
type refusal string

func (r refusal) Error() string { return string(r) }

// Apply applies edits in order to HuJSON text, keeping comments and unrelated
// content; each kind converges as spec 4.2 says. A path through an array index,
// an unknown op, a missing parent or a value of the wrong kind is
// *errcode.Error request.invalid_input with the field "/edits/<i>". On any
// error the returned text is nil.
func Apply(text []byte, edits []Edit) ([]byte, error) {
	doc, err := hujson.Parse(bytes.Clone(text))
	if err != nil {
		return nil, fmt.Errorf("policy: parse: %w", err)
	}
	for i, e := range edits {
		if err := applyEdit(&doc, e); err != nil {
			var r refusal
			if !errors.As(err, &r) {
				return nil, err
			}
			msg := string(r)
			return nil, &errcode.Error{
				Code:   errcode.InvalidInput,
				Detail: "edit " + strconv.Itoa(i) + ": " + msg,
				Fields: []errcode.FieldError{{Path: "/edits/" + strconv.Itoa(i), Message: msg}},
			}
		}
	}
	return doc.Pack(), nil
}

func applyEdit(doc *hujson.Value, e Edit) error {
	switch e.Op {
	case "set", "delete", "addItem", "removeItem":
	default:
		return refusal(fmt.Sprintf("unknown op %q", e.Op))
	}
	tokens, err := pointerTokens(e.Path)
	if err != nil {
		return err
	}

	var canon string
	var raw json.RawMessage
	if e.Op != "delete" {
		if canon, raw, err = editValue(e.Value); err != nil {
			return err
		}
	}

	parent, err := walkToParent(doc, tokens)
	if err != nil {
		return err
	}
	target := member(parent, tokens[len(tokens)-1])

	switch e.Op {
	case "set":
		if target == nil {
			return patch(doc, "add", e.Path, raw)
		}
		cur, err := canonicalOf(target)
		if err != nil {
			return err
		}
		if cur == canon {
			return nil
		}
		return patch(doc, "replace", e.Path, raw)
	case "delete":
		if target == nil {
			return nil
		}
		return patch(doc, "remove", e.Path, nil)
	case "addItem":
		if target == nil {
			return patch(doc, "add", e.Path, append(append(json.RawMessage{'['}, raw...), ']'))
		}
		idx, err := equalElements(target, canon)
		if err != nil {
			return err
		}
		if len(idx) > 0 {
			return nil
		}
		return patch(doc, "add", e.Path+"/-", raw)
	default: // removeItem
		if target == nil {
			return nil
		}
		idx, err := equalElements(target, canon)
		if err != nil {
			return err
		}
		for k := len(idx) - 1; k >= 0; k-- {
			if err := patch(doc, "remove", e.Path+"/"+strconv.Itoa(idx[k]), nil); err != nil {
				return err
			}
		}
		return nil
	}
}

// pointerTokens splits a JSON Pointer with at least one token into its decoded
// tokens.
func pointerTokens(path string) ([]string, error) {
	if !strings.HasPrefix(path, "/") {
		return nil, refusal(`path must be a JSON Pointer starting with "/"`)
	}
	tokens := strings.Split(path[1:], "/")
	for i, t := range tokens {
		for j := 0; j < len(t); j++ {
			if t[j] == '~' && (j+1 == len(t) || (t[j+1] != '0' && t[j+1] != '1')) {
				return nil, refusal(fmt.Sprintf("path %q has an invalid escape", path))
			}
		}
		tokens[i] = strings.ReplaceAll(strings.ReplaceAll(t, "~1", "/"), "~0", "~")
	}
	return tokens, nil
}

// editValue checks an edit's value and returns its canonical form and its
// minimized JSON.
func editValue(v json.RawMessage) (canon string, raw json.RawMessage, err error) {
	if len(bytes.TrimSpace(v)) == 0 {
		return "", nil, refusal("value is required")
	}
	c, err := Canonical(v)
	if err != nil {
		return "", nil, refusal("value is not valid JSON")
	}
	mini, err := hujson.Minimize(bytes.Clone(v))
	if err != nil {
		return "", nil, refusal("value is not valid JSON")
	}
	return string(c), mini, nil
}

// walkToParent follows all tokens but the last from the document root and
// returns the object the last token names a member of.
func walkToParent(doc *hujson.Value, tokens []string) (*hujson.Object, error) {
	cur := doc
	for _, t := range tokens[:len(tokens)-1] {
		obj, err := asObject(cur)
		if err != nil {
			return nil, err
		}
		next := member(obj, t)
		if next == nil {
			return nil, refusal("parent does not exist")
		}
		cur = next
	}
	return asObject(cur)
}

func asObject(v *hujson.Value) (*hujson.Object, error) {
	switch c := v.Value.(type) {
	case *hujson.Object:
		return c, nil
	case *hujson.Array:
		return nil, refusal("path goes through an array index")
	default:
		return nil, refusal("parent is not an object")
	}
}

// member is the value of the first member of obj with the given name, nil when
// there is none.
func member(obj *hujson.Object, name string) *hujson.Value {
	for i := range obj.Members {
		if lit, ok := obj.Members[i].Name.Value.(hujson.Literal); ok && lit.String() == name {
			return &obj.Members[i].Value
		}
	}
	return nil
}

func canonicalOf(v *hujson.Value) (string, error) {
	c, err := Canonical(v.Pack())
	return string(c), err
}

// equalElements returns the indexes of the elements of the array v whose
// canonical form is canon.
func equalElements(v *hujson.Value, canon string) ([]int, error) {
	arr, ok := v.Value.(*hujson.Array)
	if !ok {
		return nil, refusal("target is not an array")
	}
	var idx []int
	for i := range arr.Elements {
		c, err := canonicalOf(&arr.Elements[i])
		if err != nil {
			return nil, err
		}
		if c == canon {
			idx = append(idx, i)
		}
	}
	return idx, nil
}

type patchOp struct {
	Op    string          `json:"op"`
	Path  string          `json:"path"`
	Value json.RawMessage `json:"value,omitempty"`
}

func patch(doc *hujson.Value, op, path string, value json.RawMessage) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode([]patchOp{{Op: op, Path: path, Value: value}}); err != nil {
		return fmt.Errorf("policy: encode: %w", err)
	}
	if err := doc.Patch(buf.Bytes()); err != nil {
		return fmt.Errorf("policy: patch: %w", err)
	}
	return nil
}
