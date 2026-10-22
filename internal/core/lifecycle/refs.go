package lifecycle

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// A step template's strings may hold references (spec 1.3):
// ${input.<path>} and ${steps.<id>.output.<path>}, where <path> is
// dot-separated object keys and array indexes, and $${ is a literal ${.
// Object keys are never templates.

const (
	refsInput = "input"
	refsSteps = "steps"
)

// refsPart is one piece of a template string: literal text, with escapes
// decoded, when kind is empty, else one reference.
type refsPart struct {
	text string
	ref  string   // the reference as written, "${...}"
	kind string   // refsInput or refsSteps
	step string   // a steps reference's step id
	path []string // the path below the input or the step's output
}

// refsParse splits s into literal text and references. A ${ without a
// closing }, or a reference of neither form, is an error: templates are the
// module's, so it is a module bug.
func refsParse(s string) ([]refsPart, error) {
	var parts []refsPart
	var lit strings.Builder
	flush := func() {
		if lit.Len() > 0 {
			parts = append(parts, refsPart{text: lit.String()})
			lit.Reset()
		}
	}
	for i := 0; i < len(s); {
		switch {
		case strings.HasPrefix(s[i:], "$${"):
			lit.WriteString("${")
			i += 3
		case strings.HasPrefix(s[i:], "${"):
			end := strings.IndexByte(s[i:], '}')
			if end < 0 {
				return nil, fmt.Errorf("lifecycle: template string %q: unterminated reference at byte %d", s, i)
			}
			p, err := refsReference(s[i : i+end+1])
			if err != nil {
				return nil, err
			}
			flush()
			parts = append(parts, p)
			i += end + 1
		default:
			lit.WriteByte(s[i])
			i++
		}
	}
	flush()
	return parts, nil
}

// refsReference parses one "${...}".
func refsReference(ref string) (refsPart, error) {
	segs := strings.Split(ref[2:len(ref)-1], ".")
	bad := fmt.Errorf("lifecycle: reference %s: want ${input.<path>} or ${steps.<id>.output.<path>}", ref)
	if slices.Contains(segs, "") {
		return refsPart{}, bad
	}
	switch {
	case len(segs) >= 2 && segs[0] == refsInput:
		return refsPart{ref: ref, kind: refsInput, path: segs[1:]}, nil
	case len(segs) >= 4 && segs[0] == refsSteps && segs[2] == "output":
		return refsPart{ref: ref, kind: refsSteps, step: segs[1], path: segs[3:]}, nil
	}
	return refsPart{}, bad
}

// refsDecode decodes one JSON value, keeping numbers as json.Number so they
// keep their text.
func refsDecode(raw json.RawMessage) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("more than one JSON value")
	}
	return v, nil
}

// refsEncode encodes v without HTML escaping, because templates are shown
// in clear in the preview.
func refsEncode(v any) (json.RawMessage, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("lifecycle: encode a step template: %w", err)
	}
	return json.RawMessage(bytes.TrimSuffix(buf.Bytes(), []byte("\n"))), nil
}

// refsWalk replaces every string of v, through objects (in key order) and
// arrays, with what str returns for it.
func refsWalk(v any, str func(string) (any, error)) (any, error) {
	switch x := v.(type) {
	case string:
		return str(x)
	case map[string]any:
		out := make(map[string]any, len(x))
		for _, k := range slices.Sorted(maps.Keys(x)) {
			r, err := refsWalk(x[k], str)
			if err != nil {
				return nil, err
			}
			out[k] = r
		}
		return out, nil
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			r, err := refsWalk(e, str)
			if err != nil {
				return nil, err
			}
			out[i] = r
		}
		return out, nil
	}
	return v, nil
}

// refsLookup follows path into v; ok is false when a key or an index is
// missing. An index is a decimal number without a leading zero.
func refsLookup(v any, path []string) (any, bool) {
	for _, seg := range path {
		switch node := v.(type) {
		case map[string]any:
			next, ok := node[seg]
			if !ok {
				return nil, false
			}
			v = next
		case []any:
			i, err := strconv.Atoi(seg)
			if err != nil || i < 0 || i >= len(node) || strconv.Itoa(i) != seg {
				return nil, false
			}
			v = node[i]
		default:
			return nil, false
		}
	}
	return v, true
}

// refsText is the text a reference inside a longer string stands for: a
// string's own text or a number's JSON text.
func refsText(v any) (string, bool) {
	switch x := v.(type) {
	case string:
		return x, true
	case json.Number:
		return x.String(), true
	}
	return "", false
}

// refsEscape escapes every ${ in the strings of v, so that a substituted
// value stays literal in the step pass.
func refsEscape(v any) any {
	out, _ := refsWalk(v, func(s string) (any, error) { return strings.ReplaceAll(s, "${", "$${"), nil })
	return out
}

// resolveInputRefs resolves the ${input...} references of tmpl against the
// composite's input at plan time. The result is still a template:
// ${steps...} references stay, $${ stays escaped, and every ${ a
// substituted value holds is escaped, so a value never becomes a reference.
// A missing input path is request.invalid_input.
func resolveInputRefs(tmpl, input json.RawMessage) (json.RawMessage, error) {
	t, err := refsDecode(tmpl)
	if err != nil {
		return nil, fmt.Errorf("lifecycle: step template: %w", err)
	}
	in, err := refsDecode(input)
	if err != nil {
		return nil, fmt.Errorf("lifecycle: composite input: %w", err)
	}
	out, err := refsWalk(t, func(s string) (any, error) { return refsInputString(s, in) })
	if err != nil {
		return nil, err
	}
	return refsEncode(out)
}

func refsInputString(s string, input any) (any, error) {
	parts, err := refsParse(s)
	if err != nil {
		return nil, err
	}
	if len(parts) == 1 && parts[0].kind == refsInput {
		v, err := refsInputValue(parts[0], input)
		if err != nil {
			return nil, err
		}
		return refsEscape(v), nil
	}
	var out, lit strings.Builder
	flush := func() {
		out.WriteString(strings.ReplaceAll(lit.String(), "${", "$${"))
		lit.Reset()
	}
	for _, p := range parts {
		switch p.kind {
		case "":
			lit.WriteString(p.text)
		case refsInput:
			v, err := refsInputValue(p, input)
			if err != nil {
				return nil, err
			}
			text, ok := refsText(v)
			if !ok {
				return nil, fmt.Errorf("lifecycle: reference %s inside a longer string is not a string or a number", p.ref)
			}
			lit.WriteString(text)
		case refsSteps:
			if strings.HasSuffix(lit.String(), "$") {
				return nil, fmt.Errorf("lifecycle: template string %q: a $ right before %s cannot be told from an escape", s, p.ref)
			}
			flush()
			out.WriteString(p.ref)
		}
	}
	flush()
	return out.String(), nil
}

func refsInputValue(p refsPart, input any) (any, error) {
	v, ok := refsLookup(input, p.path)
	if !ok {
		return nil, errcode.New(errcode.InvalidInput,
			fmt.Sprintf("the input has no %s, which the step reference %s needs", strings.Join(p.path, "."), p.ref))
	}
	return v, nil
}

// resolveStepRefs resolves the ${steps...} references of tmpl, a template
// resolveInputRefs returned, against the unredacted outputs of the steps it
// follows, keyed by step id, and decodes $${ to ${. A missing step output or
// path, or a value that cannot stand inside a longer string, fails the step
// with core.child_failed.
func resolveStepRefs(tmpl json.RawMessage, outputs map[string]json.RawMessage) (json.RawMessage, error) {
	t, err := refsDecode(tmpl)
	if err != nil {
		return nil, fmt.Errorf("lifecycle: step template: %w", err)
	}
	decoded := map[string]any{}
	out, err := refsWalk(t, func(s string) (any, error) { return refsStepString(s, outputs, decoded) })
	if err != nil {
		return nil, err
	}
	return refsEncode(out)
}

func refsStepString(s string, outputs map[string]json.RawMessage, decoded map[string]any) (any, error) {
	parts, err := refsParse(s)
	if err != nil {
		return nil, err
	}
	if len(parts) == 1 && parts[0].kind == refsSteps {
		return refsStepValue(parts[0], outputs, decoded)
	}
	var out strings.Builder
	for _, p := range parts {
		switch p.kind {
		case "":
			out.WriteString(p.text)
		case refsSteps:
			v, err := refsStepValue(p, outputs, decoded)
			if err != nil {
				return nil, err
			}
			text, ok := refsText(v)
			if !ok {
				return nil, errcode.New(errcode.ChildFailed,
					fmt.Sprintf("step %s: %s inside a longer string is not a string or a number", p.step, p.ref))
			}
			out.WriteString(text)
		case refsInput:
			return nil, fmt.Errorf("lifecycle: reference %s was not resolved at plan", p.ref)
		}
	}
	return out.String(), nil
}

func refsStepValue(p refsPart, outputs map[string]json.RawMessage, decoded map[string]any) (any, error) {
	out, ok := decoded[p.step]
	if !ok {
		raw, found := outputs[p.step]
		if !found {
			return nil, errcode.New(errcode.ChildFailed, fmt.Sprintf("step %s has no output for %s", p.step, p.ref))
		}
		var err error
		if out, err = refsDecode(raw); err != nil {
			return nil, fmt.Errorf("lifecycle: output of step %s: %w", p.step, err)
		}
		decoded[p.step] = out
	}
	v, ok := refsLookup(out, p.path)
	if !ok {
		return nil, errcode.New(errcode.ChildFailed,
			fmt.Sprintf("the output of step %s has no %s, which %s needs", p.step, strings.Join(p.path, "."), p.ref))
	}
	return v, nil
}

// stepRefs lists the step ids tmpl references, each once, in the order they
// first appear (objects in key order). Escaped text is not a reference.
func stepRefs(tmpl json.RawMessage) ([]string, error) {
	t, err := refsDecode(tmpl)
	if err != nil {
		return nil, fmt.Errorf("lifecycle: step template: %w", err)
	}
	var ids []string
	_, err = refsWalk(t, func(s string) (any, error) {
		parts, err := refsParse(s)
		if err != nil {
			return nil, err
		}
		for _, p := range parts {
			if p.kind == refsSteps && !slices.Contains(ids, p.step) {
				ids = append(ids, p.step)
			}
		}
		return s, nil
	})
	if err != nil {
		return nil, err
	}
	return ids, nil
}
