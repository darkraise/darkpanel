package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/schema"
)

var pathParam = regexp.MustCompile(`\{[A-Za-z]+\}`)

// CheckOpenAPI verifies the route-coverage invariant of spec section 7 for
// reg: every route of the network mux appears in the OpenAPI document (a
// hand-written route as itself, a per-action route once for each
// non-break-glass action or resource of reg it serves), and every operation
// of the document is served by a network route and names no break-glass or
// Internal action. Tests call it; plan 4's assembly test calls it with the
// real registry.
func CheckOpenAPI(reg *core.Registry) error {
	doc, err := schema.OpenAPI(reg, Routes())
	if err != nil {
		return err
	}
	return checkOpenAPI(reg, (&Server{}).networkRoutes(), doc)
}

func checkOpenAPI(reg *core.Registry, routes []route, doc []byte) error {
	var d struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(doc, &d); err != nil {
		return fmt.Errorf("api: openapi: %w", err)
	}
	var errs []error
	mux := http.NewServeMux()
	for _, rt := range routes {
		mux.Handle(rt.method+" "+rt.path, rt.handler)
		paths := []string{rt.path}
		if rt.perAction {
			paths = instances(reg, rt)
		}
		for _, p := range paths {
			if _, ok := d.Paths[p][strings.ToLower(rt.method)]; !ok {
				errs = append(errs, fmt.Errorf("route %s %s: the document lacks %s %s", rt.method, rt.path, rt.method, p))
			}
		}
	}
	for p, ops := range d.Paths {
		for _, def := range reg.Actions() {
			if !strings.Contains(p, "/"+def.Spec.Name+"/") {
				continue
			}
			if def.Spec.BreakGlass {
				errs = append(errs, fmt.Errorf("document path %s: break-glass action %s is listed", p, def.Spec.Name))
			}
			if def.Spec.Internal {
				errs = append(errs, fmt.Errorf("document path %s: internal action %s is listed", p, def.Spec.Name))
			}
		}
		for m := range ops {
			r, err := http.NewRequest(strings.ToUpper(m), "http://controller"+pathParam.ReplaceAllString(p, "x"), nil)
			if err != nil {
				errs = append(errs, fmt.Errorf("document operation %s %s: %w", strings.ToUpper(m), p, err))
				continue
			}
			if _, pattern := mux.Handler(r); pattern == "" {
				errs = append(errs, fmt.Errorf("document operation %s %s: no network route serves it", strings.ToUpper(m), p))
			}
		}
	}
	return errors.Join(errs...)
}

// instances lists the document paths of a per-action route: one per
// resource for the resource route, and for an action route one per
// non-break-glass action of its side that the route's operation serves.
func instances(reg *core.Registry, rt route) []string {
	if strings.Contains(rt.path, "/resources/") {
		var out []string
		for _, res := range reg.Resources() {
			module, name, _ := strings.Cut(res.Name, ".")
			out = append(out, strings.NewReplacer("{module}", module, "{resource}", name).Replace(rt.path))
		}
		return out
	}
	side := core.SideController
	if strings.Contains(rt.path, "{host}") {
		side = core.SideAgent
	}
	op := rt.path[strings.LastIndex(rt.path, "/")+1:]
	var out []string
	for _, def := range reg.Actions() {
		s := def.Spec
		if s.BreakGlass || s.Side != side || !servesOp(s, op) {
			continue
		}
		out = append(out, strings.NewReplacer("{name}", s.Name, "{version}", fmt.Sprintf("v%d", s.Version)).Replace(rt.path))
	}
	return out
}
