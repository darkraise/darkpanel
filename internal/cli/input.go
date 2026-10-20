package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"slices"
	"strings"
	"unicode"

	"github.com/spf13/cobra"

	"github.com/darkraise/darkpanel/internal/core/schema"
)

// reservedFlags are the flags the CLI defines itself; no input field may
// take one (generation fails instead).
var reservedFlags = []string{"url", "socket", "dev", "help", "json", "host", "local", "input", "yes", "confirmation", "confirm"}

// terminal reads the operator's answers: from a terminal with prompts, or
// line by line from standard input otherwise.
type terminal struct {
	in         *bufio.Reader
	out        io.Writer // where prompts go
	isTerm     bool
	readSecret func() (string, error) // one line without echo; used only on a terminal
}

// line prompts on a terminal and reads one line without its line ending.
// It returns io.EOF when the input ended before any character.
func (t *terminal) line(prompt string) (string, error) {
	if t.isTerm {
		_, _ = io.WriteString(t.out, prompt)
	}
	s, err := t.in.ReadString('\n')
	if err != nil && (!errors.Is(err, io.EOF) || s == "") {
		return "", err
	}
	return strings.TrimRight(s, "\r\n"), nil
}

// secret reads one line: without echo after a prompt on a terminal, else
// as line does without a prompt.
func (t *terminal) secret(prompt string) (string, error) {
	if !t.isTerm {
		return t.line("")
	}
	_, _ = io.WriteString(t.out, prompt)
	s, err := t.readSecret()
	_, _ = io.WriteString(t.out, "\n")
	return s, err
}

// inputField is one property of an action's input schema.
type inputField struct {
	Name        string // the JSON name
	Flag        string // the kebab-case flag name
	Description string
	Type        string // string, integer, number, boolean or array; "" for a nested value
	Items       string // the scalar item type of an array
	Enum        []string
	Required    bool
	Secret      bool
}

// propSchema is the part of a JSON Schema the CLI reads.
type propSchema struct {
	Ref         string          `json:"$ref"`
	Type        json.RawMessage `json:"type"`
	Items       *propSchema     `json:"items"`
	Enum        []any           `json:"enum"`
	Description string          `json:"description"`
	Secret      bool            `json:"x-secret"`
	Required    []string        `json:"required"`
	Properties  json.RawMessage `json:"properties"`
}

var scalarTypes = []string{"string", "integer", "number", "boolean"}

// inputFields reads the properties of t's input schema (schema.Input) in
// schema order. Scalars and arrays of scalars get a flag; anything else is
// nested and comes from --input; secret fields get no flag and must be
// strings.
func inputFields(t reflect.Type) ([]inputField, error) {
	raw, err := schema.Input(t)
	if err != nil {
		return nil, err
	}
	var doc struct {
		propSchema
		Defs map[string]*propSchema `json:"$defs"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("input schema of %s: %w", t, err)
	}
	resolve := func(p *propSchema) *propSchema {
		if name, ok := strings.CutPrefix(p.Ref, "#/$defs/"); ok && doc.Defs[name] != nil {
			return doc.Defs[name]
		}
		return p
	}
	root := resolve(&doc.propSchema)
	props, err := members(root.Properties)
	if err != nil {
		return nil, fmt.Errorf("input schema of %s: %w", t, err)
	}
	var fields []inputField
	flags := map[string]string{}
	for _, m := range props {
		var p propSchema
		if err := json.Unmarshal(m.Value, &p); err != nil {
			return nil, fmt.Errorf("input schema of %s, property %s: %w", t, m.Key, err)
		}
		f := inputField{Name: m.Key, Flag: kebab(m.Key), Description: p.Description, Secret: p.Secret,
			Required: slices.Contains(root.Required, m.Key)}
		rp := resolve(&p)
		var typ string
		_ = json.Unmarshal(rp.Type, &typ)
		switch {
		case slices.Contains(scalarTypes, typ):
			f.Type = typ
		case typ == "array" && rp.Items != nil:
			var item string
			_ = json.Unmarshal(resolve(rp.Items).Type, &item)
			if slices.Contains(scalarTypes, item) {
				f.Type, f.Items = typ, item
			}
		}
		for _, e := range rp.Enum {
			if s, ok := e.(string); ok {
				f.Enum = append(f.Enum, s)
			}
		}
		switch {
		case f.Secret && f.Type != "string":
			return nil, fmt.Errorf("input of %s: secret field %s must be a string", t, f.Name)
		case f.Secret || f.Type == "":
		case slices.Contains(reservedFlags, f.Flag):
			return nil, fmt.Errorf("input of %s: field %s would take the CLI's own flag --%s", t, f.Name, f.Flag)
		case flags[f.Flag] != "":
			return nil, fmt.Errorf("input of %s: fields %s and %s both map to flag --%s", t, flags[f.Flag], f.Name, f.Flag)
		default:
			flags[f.Flag] = f.Name
		}
		fields = append(fields, f)
	}
	return fields, nil
}

// kebab turns a JSON name such as expiresInDays into expires-in-days.
func kebab(name string) string {
	var b strings.Builder
	runes := []rune(name)
	for i, r := range runes {
		switch {
		case r == '_':
			b.WriteRune('-')
		case unicode.IsUpper(r):
			if i > 0 && (unicode.IsLower(runes[i-1]) || unicode.IsDigit(runes[i-1])) {
				b.WriteRune('-')
			}
			b.WriteRune(unicode.ToLower(r))
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// addInputFlags defines a flag for every scalar field and, when the input
// has any field, --input.
func addInputFlags(cmd *cobra.Command, fields []inputField) {
	fs := cmd.Flags()
	for _, f := range fields {
		if f.Secret || f.Type == "" {
			continue
		}
		usage := f.Description
		if len(f.Enum) > 0 {
			usage += " One of: " + strings.Join(f.Enum, ", ") + "."
		}
		if f.Required && f.Type != "boolean" {
			usage += " (required)"
		}
		usage = strings.TrimSpace(usage)
		switch f.Type + "/" + f.Items {
		case "string/":
			fs.String(f.Flag, "", usage)
		case "integer/":
			fs.Int64(f.Flag, 0, usage)
		case "number/":
			fs.Float64(f.Flag, 0, usage)
		case "boolean/":
			fs.Bool(f.Flag, false, usage)
		case "array/string":
			fs.StringArray(f.Flag, nil, usage+" (repeatable)")
		case "array/integer":
			fs.Int64Slice(f.Flag, nil, usage+" (comma-separated or repeated)")
		case "array/number":
			fs.Float64Slice(f.Flag, nil, usage+" (comma-separated or repeated)")
		case "array/boolean":
			fs.BoolSlice(f.Flag, nil, usage+" (comma-separated or repeated)")
		}
	}
	if len(fields) > 0 {
		fs.String("input", "", "read the input as a JSON object from this file, or from standard input with -; flags override its fields")
	}
}

// flagValue is the value of f's flag.
func flagValue(cmd *cobra.Command, f inputField) (any, error) {
	fs := cmd.Flags()
	switch f.Type + "/" + f.Items {
	case "string/":
		return fs.GetString(f.Flag)
	case "integer/":
		return fs.GetInt64(f.Flag)
	case "number/":
		return fs.GetFloat64(f.Flag)
	case "boolean/":
		return fs.GetBool(f.Flag)
	case "array/string":
		return fs.GetStringArray(f.Flag)
	case "array/integer":
		return fs.GetInt64Slice(f.Flag)
	case "array/number":
		return fs.GetFloat64Slice(f.Flag)
	case "array/boolean":
		return fs.GetBoolSlice(f.Flag)
	}
	return nil, fmt.Errorf("field %s has no flag", f.Name)
}

// collectInput builds the input object: the --input document, overridden
// by the flags that were set, then every secret still missing, read in
// schema order from the terminal or standard input. A required boolean that
// is missing is false; any other missing required field is a usage error.
func collectInput(cmd *cobra.Command, fields []inputField, term *terminal) (json.RawMessage, error) {
	obj := map[string]json.RawMessage{}
	if fl := cmd.Flags().Lookup("input"); fl != nil && fl.Value.String() != "" {
		var err error
		if obj, err = readInputDoc(fl.Value.String(), term); err != nil {
			return nil, err
		}
	}
	for _, f := range fields {
		if f.Secret || f.Type == "" || !cmd.Flags().Changed(f.Flag) {
			continue
		}
		v, err := flagValue(cmd, f)
		if err != nil {
			return nil, err
		}
		if obj[f.Name], err = json.Marshal(v); err != nil {
			return nil, err
		}
	}
	for _, f := range fields {
		if _, ok := obj[f.Name]; ok || !f.Required || f.Secret {
			continue
		}
		switch f.Type {
		case "boolean":
			obj[f.Name] = json.RawMessage("false")
		case "":
			return nil, usagef("required input field %q missing; give it in --input", f.Name)
		default:
			return nil, usagef("required flag --%s not set", f.Flag)
		}
	}
	for _, f := range fields {
		if _, ok := obj[f.Name]; ok || !f.Secret {
			continue
		}
		s, err := term.secret(f.Name + ": ")
		if errors.Is(err, io.EOF) {
			return nil, usagef("no value for %s: run on a terminal, or give one line per secret on standard input", f.Name)
		}
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", f.Name, err)
		}
		if obj[f.Name], err = json.Marshal(s); err != nil {
			return nil, err
		}
	}
	return json.Marshal(obj)
}

// readInputDoc reads --input: a JSON object from path, or from standard
// input for "-".
func readInputDoc(path string, term *terminal) (map[string]json.RawMessage, error) {
	var data []byte
	var err error
	if path == "-" {
		data, err = io.ReadAll(term.in)
	} else {
		data, err = os.ReadFile(path) //nolint:gosec // G304: the operator names the file to read
	}
	if err != nil {
		return nil, usagef("--input %s: %v", path, err)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil || obj == nil {
		return nil, usagef("--input %s: want a JSON object", path)
	}
	return obj, nil
}

// member is one member of a JSON object, in document order.
type member struct {
	Key   string
	Value json.RawMessage
}

// members returns the members of the JSON object raw in document order; an
// empty raw has none.
func members(raw json.RawMessage) ([]member, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, errors.New("not a JSON object")
	}
	var out []member
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, _ := tok.(string)
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		out = append(out, member{Key: key, Value: v})
	}
	return out, nil
}
