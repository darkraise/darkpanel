package schema

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
)

// snapshotFile is the committed form of an action or resource (written by
// ActionSnapshot and ResourceSnapshot); a resource has only name and output.
type snapshotFile struct {
	Name       string         `json:"name"`
	Version    int            `json:"version"`
	Class      string         `json:"class"`
	Side       string         `json:"side"`
	BreakGlass bool           `json:"breakGlass"`
	Input      map[string]any `json:"input"`
	Output     map[string]any `json:"output"`
}

// constraintKeywords are compared for equality: any change to one of them in
// a published version is a breaking change (ADR 0002).
var constraintKeywords = []string{
	"type", "format", "pattern", "default", "const", "minimum", "maximum",
	"exclusiveMinimum", "exclusiveMaximum", "minLength", "maxLength", "minItems",
	"maxItems", "uniqueItems", "minProperties", "maxProperties", "contentEncoding",
	secretKeyword, "writeOnly",
}

// Compare reports every change from the snapshot prev to the snapshot next of
// the same action version that ADR 0002 forbids: a removed or renamed field, a
// changed type, default or constraint, a new required input field, a removed
// enum value (or any enum change in an input), or a changed class, side or
// break-glass flag. New optional input fields, new output fields and new
// output enum values are allowed. It fails only when the files cannot be
// compared.
func Compare(prev, next []byte) ([]string, error) {
	var p, n snapshotFile
	if err := json.Unmarshal(prev, &p); err != nil {
		return nil, fmt.Errorf("schema: old snapshot: %w", err)
	}
	if err := json.Unmarshal(next, &n); err != nil {
		return nil, fmt.Errorf("schema: new snapshot: %w", err)
	}
	if p.Name != n.Name || p.Version != n.Version {
		return nil, fmt.Errorf("schema: comparing %s/v%d with %s/v%d", p.Name, p.Version, n.Name, n.Version)
	}
	var out []string
	for _, f := range []struct {
		name       string
		prev, next any
	}{{"class", p.Class, n.Class}, {"side", p.Side, n.Side}, {"breakGlass", p.BreakGlass, n.BreakGlass}} {
		if f.prev != f.next {
			out = append(out, fmt.Sprintf("%s changed from %s to %s", f.name, render(f.prev), render(f.next)))
		}
	}
	for _, dir := range []struct {
		name       string
		prev, next map[string]any
		input      bool
	}{{"input", p.Input, n.Input, true}, {"output", p.Output, n.Output, false}} {
		c := comparer{prevDefs: defsOf(dir.prev), nextDefs: defsOf(dir.next), input: dir.input, active: map[[2]string]bool{}}
		c.compare(dir.name, dir.prev, dir.next)
		out = append(out, c.out...)
	}
	return out, nil
}

// CheckReleased compares every released snapshot, releasedDir/<tag>/<path>,
// with current[<path>] and returns every violation joined. A released file
// with no current counterpart is a removed version. A missing releasedDir has
// nothing to check.
func CheckReleased(releasedDir string, current map[string][]byte) error {
	if _, err := os.Stat(releasedDir); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	var errs []error
	err := filepath.WalkDir(releasedDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".json" {
			return err
		}
		rel, err := filepath.Rel(releasedDir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		_, key, ok := strings.Cut(rel, "/") // drop the tag
		if !ok {
			return nil
		}
		now, ok := current[key]
		if !ok {
			errs = append(errs, fmt.Errorf("%s: released version removed", rel))
			return nil
		}
		old, err := os.ReadFile(path) //nolint:gosec // G304: a path found under the released snapshots directory
		if err != nil {
			return err
		}
		violations, err := Compare(old, now)
		if err != nil {
			return fmt.Errorf("%s: %w", rel, err)
		}
		for _, v := range violations {
			errs = append(errs, fmt.Errorf("%s: %s", rel, v))
		}
		return nil
	})
	if err != nil {
		errs = append(errs, fmt.Errorf("schema: released snapshots: %w", err))
	}
	return errors.Join(errs...)
}

func defsOf(s map[string]any) map[string]any {
	d, _ := s["$defs"].(map[string]any)
	return d
}

func render(v any) string {
	if v == nil {
		return "none"
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

type comparer struct {
	prevDefs, nextDefs map[string]any
	input              bool
	active             map[[2]string]bool // definition pairs being compared, against recursion
	out                []string
}

func (c *comparer) fail(path, format string, args ...any) {
	c.out = append(c.out, path+": "+fmt.Sprintf(format, args...))
}

// resolveJSON replaces a "#/$defs/<name>" reference with the definition, keeping
// keywords written beside the reference (such as x-secret on a field).
func resolveJSON(s, defs map[string]any) (map[string]any, string) {
	ref, _ := s["$ref"].(string)
	name, ok := strings.CutPrefix(ref, "#/$defs/")
	if !ok {
		return s, ""
	}
	d, _ := defs[name].(map[string]any)
	if d == nil {
		return nil, name
	}
	merged := maps.Clone(d)
	for k, v := range s {
		if k != "$ref" {
			merged[k] = v
		}
	}
	return merged, name
}

func (c *comparer) compare(path string, prev, next map[string]any) {
	prev, pname := resolveJSON(prev, c.prevDefs)
	next, nname := resolveJSON(next, c.nextDefs)
	if prev == nil || next == nil {
		if prev != nil || next != nil {
			c.fail(path, "reference %q cannot be resolved", pname+nname)
		}
		return
	}
	for _, kw := range constraintKeywords {
		if !reflect.DeepEqual(prev[kw], next[kw]) {
			c.fail(path, "changed %s from %s to %s", kw, render(prev[kw]), render(next[kw]))
		}
	}
	c.compareEnum(path, prev["enum"], next["enum"])
	key := [2]string{pname, nname}
	if pname != "" || nname != "" {
		if c.active[key] {
			return
		}
		c.active[key] = true
		defer delete(c.active, key)
	}
	c.compareProperties(path, prev, next)
	for _, kw := range []string{"items", "additionalProperties"} {
		ps, pok := prev[kw].(map[string]any)
		ns, nok := next[kw].(map[string]any)
		switch {
		case pok && nok:
			c.compare(path+"/"+kw, ps, ns)
		case !reflect.DeepEqual(prev[kw], next[kw]):
			c.fail(path, "changed %s from %s to %s", kw, render(prev[kw]), render(next[kw]))
		}
	}
	for _, kw := range []string{"oneOf", "anyOf", "allOf"} {
		ps, _ := prev[kw].([]any)
		ns, _ := next[kw].([]any)
		if len(ps) != len(ns) {
			c.fail(path, "changed %s from %d to %d branches", kw, len(ps), len(ns))
			continue
		}
		for i := range ps {
			pm, _ := ps[i].(map[string]any)
			nm, _ := ns[i].(map[string]any)
			c.compare(fmt.Sprintf("%s/%s/%d", path, kw, i), pm, nm)
		}
	}
}

func (c *comparer) compareEnum(path string, prev, next any) {
	ps, _ := prev.([]any)
	ns, _ := next.([]any)
	switch {
	case prev == nil && next == nil:
		return
	case prev == nil || next == nil:
		c.fail(path, "changed enum from %s to %s", render(prev), render(next))
		return
	}
	for _, v := range ps {
		if !slices.ContainsFunc(ns, func(w any) bool { return reflect.DeepEqual(v, w) }) {
			c.fail(path, "removed enum value %s", render(v))
		}
	}
	if c.input {
		for _, v := range ns {
			if !slices.ContainsFunc(ps, func(w any) bool { return reflect.DeepEqual(v, w) }) {
				c.fail(path, "added enum value %s to an input", render(v))
			}
		}
	}
}

func (c *comparer) compareProperties(path string, prev, next map[string]any) {
	pp, _ := prev["properties"].(map[string]any)
	np, _ := next["properties"].(map[string]any)
	preq, nreq := stringSet(prev["required"]), stringSet(next["required"])
	for _, name := range slices.Sorted(maps.Keys(pp)) {
		at := path + "/" + name
		ns, ok := np[name].(map[string]any)
		if !ok {
			c.fail(at, "field removed")
			continue
		}
		if preq[name] != nreq[name] {
			c.fail(at, "required changed from %t to %t", preq[name], nreq[name])
		}
		ps, _ := pp[name].(map[string]any)
		c.compare(at, ps, ns)
	}
	for _, name := range slices.Sorted(maps.Keys(np)) {
		if _, ok := pp[name]; !ok && c.input && nreq[name] {
			c.fail(path+"/"+name, "new required input field")
		}
	}
}

func stringSet(v any) map[string]bool {
	out := map[string]bool{}
	list, _ := v.([]any)
	for _, s := range list {
		if name, ok := s.(string); ok {
			out[name] = true
		}
	}
	return out
}
