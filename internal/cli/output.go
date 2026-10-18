package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/darkraise/darkpanel/internal/core/schema"
)

// render writes an output for people (spec section 15): a list of objects
// is a table, any other value is "key: value" lines with nested keys joined
// by dots. An object holding only a list is the list's table. Null prints
// nothing. Every string it writes is sanitized.
func render(w io.Writer, raw json.RawMessage) error {
	if !json.Valid(raw) {
		return fmt.Errorf("render: the output is not valid JSON")
	}
	raw = bytes.TrimSpace(raw)
	switch raw[0] {
	case '[':
		return renderList(w, raw)
	case '{':
		ms, err := members(raw)
		if err != nil {
			return err
		}
		if len(ms) == 1 && isList(ms[0].Value) {
			return renderList(w, ms[0].Value)
		}
		var lines []string
		for _, m := range ms {
			if rows := objectRows(m.Value); len(rows) > 0 {
				if err := writeLines(w, lines); err != nil {
					return err
				}
				lines = nil
				if _, err := fmt.Fprintf(w, "%s:\n", sanitize(m.Key)); err != nil {
					return err
				}
				if err := renderList(w, m.Value); err != nil {
					return err
				}
				continue
			}
			lines = flatten(lines, m.Key, m.Value)
		}
		return writeLines(w, lines)
	case 'n':
		return nil
	}
	_, err := fmt.Fprintln(w, sanitize(scalarText(raw)))
	return err
}

func writeLines(w io.Writer, lines []string) error {
	for _, l := range lines {
		if _, err := fmt.Fprintln(w, sanitize(l)); err != nil {
			return err
		}
	}
	return nil
}

func isList(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) > 0 && raw[0] == '['
}

// objectRows returns the elements of raw when it is a non-empty list of
// objects only.
func objectRows(raw json.RawMessage) []json.RawMessage {
	var items []json.RawMessage
	if !isList(raw) || json.Unmarshal(raw, &items) != nil || len(items) == 0 {
		return nil
	}
	for _, it := range items {
		if t := bytes.TrimSpace(it); len(t) == 0 || t[0] != '{' {
			return nil
		}
	}
	return items
}

// renderList writes a list of objects as a table, an empty list as
// "(none)", and anything else one element per line.
func renderList(w io.Writer, raw json.RawMessage) error {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return err
	}
	if len(items) == 0 {
		_, err := fmt.Fprintln(w, "(none)")
		return err
	}
	rows := objectRows(raw)
	if rows == nil {
		for _, it := range items {
			if _, err := fmt.Fprintln(w, sanitize(cellText(it))); err != nil {
				return err
			}
		}
		return nil
	}
	var header []string
	cells := make([]map[string]string, 0, len(rows))
	for _, r := range rows {
		ms, err := members(r)
		if err != nil {
			return err
		}
		row := map[string]string{}
		for _, m := range ms {
			if !slices.Contains(header, m.Key) {
				header = append(header, m.Key)
			}
			row[m.Key] = cellText(m.Value)
		}
		cells = append(cells, row)
	}
	table := make([][]string, 0, len(cells))
	for _, row := range cells {
		line := make([]string, len(header))
		for i, h := range header {
			line[i] = row[h]
		}
		table = append(table, line)
	}
	return writeTable(w, header, table)
}

// writeTable writes header and rows, sanitized, as columns separated by
// two spaces. Sanitizing also escapes tabs and newlines, which would break
// the columns.
func writeTable(w io.Writer, header []string, rows [][]string) error {
	tw := tabwriter.NewWriter(w, 0, 8, 2, ' ', 0)
	for _, r := range append([][]string{header}, rows...) {
		cells := make([]string, len(r))
		for i, c := range r {
			cells[i] = sanitize(c)
		}
		if _, err := fmt.Fprintln(tw, strings.Join(cells, "\t")); err != nil {
			return err
		}
	}
	return tw.Flush()
}

// flatten appends "key: value" lines for raw under key: objects recurse
// with dotted keys, lists of scalars are comma-separated, and anything
// else nested is compact JSON.
func flatten(lines []string, key string, raw json.RawMessage) []string {
	raw = bytes.TrimSpace(raw)
	if len(raw) > 0 && raw[0] == '{' {
		ms, err := members(raw)
		if err == nil && len(ms) > 0 {
			for _, m := range ms {
				lines = flatten(lines, key+"."+m.Key, m.Value)
			}
			return lines
		}
	}
	return append(lines, key+": "+cellText(raw))
}

// cellText is a value as one line: a scalar's text, "-" for null, a list
// of scalars comma-separated, anything else compact JSON.
func cellText(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	var items []json.RawMessage
	if isList(raw) && json.Unmarshal(raw, &items) == nil {
		texts := make([]string, 0, len(items))
		for _, it := range items {
			if t := bytes.TrimSpace(it); len(t) > 0 && (t[0] == '{' || t[0] == '[') {
				return compact(raw)
			}
			texts = append(texts, scalarText(it))
		}
		if len(items) > 0 {
			return strings.Join(texts, ", ")
		}
	}
	if len(raw) > 0 && (raw[0] == '{' || raw[0] == '[') {
		return compact(raw)
	}
	return scalarText(raw)
}

func scalarText(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	var s string
	switch {
	case string(raw) == "null": // checked first: null unmarshals into a string as ""
		return "-"
	case json.Unmarshal(raw, &s) == nil:
		return s
	}
	return string(raw)
}

func compact(raw json.RawMessage) string {
	var b bytes.Buffer
	if json.Compact(&b, raw) != nil {
		return string(raw)
	}
	return b.String()
}

// secretOutputs are the top-level properties of t's output schema
// (schema.Output) marked secret, in schema order.
func secretOutputs(t reflect.Type) ([]string, error) {
	raw, err := schema.Output(t)
	if err != nil {
		return nil, err
	}
	var doc struct {
		propSchema
		Defs map[string]*propSchema `json:"$defs"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("output schema of %s: %w", t, err)
	}
	root := &doc.propSchema
	if name, ok := strings.CutPrefix(root.Ref, "#/$defs/"); ok && doc.Defs[name] != nil {
		root = doc.Defs[name]
	}
	props, err := members(root.Properties)
	if err != nil {
		return nil, fmt.Errorf("output schema of %s: %w", t, err)
	}
	var names []string
	for _, m := range props {
		var p propSchema
		if json.Unmarshal(m.Value, &p) == nil && p.Secret {
			names = append(names, m.Key)
		}
	}
	return names, nil
}

// noteSecrets tells the operator that the secret outputs shown are shown
// only once (spec section 5): the API never returns them again.
func noteSecrets(w io.Writer, names []string, raw json.RawMessage) {
	ms, _ := members(raw)
	var shown []string
	for _, m := range ms {
		if slices.Contains(names, m.Key) && scalarText(m.Value) != "-" && scalarText(m.Value) != "" {
			shown = append(shown, m.Key)
		}
	}
	switch len(shown) {
	case 0:
	case 1:
		_, _ = fmt.Fprintf(w, "\nThe %s above will not be shown again; store it now.\n", shown[0])
	default:
		_, _ = fmt.Fprintf(w, "\nThe %s above will not be shown again; store them now.\n", strings.Join(shown, " and "))
	}
}
