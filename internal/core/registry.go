package core

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// ReservedNames are top-level CLI names no module id may take (ADR 0006 and
// ADR 0011).
var ReservedNames = []string{
	"serve", "agent", "update", "user", "controller", "change", "uninstall", "version",
	"host", "module", "token", "audit", "help", "completion",
}

var (
	moduleIDPattern     = regexp.MustCompile(`^[a-z][a-z0-9]*$`)
	actionNamePattern   = regexp.MustCompile(`^[a-z][a-z0-9]*\.[a-z][a-z0-9]*\.[a-z][a-z0-9-]*$`)
	resourceNamePattern = regexp.MustCompile(`^[a-z][a-z0-9]*\.[a-z][a-z0-9-]*$`)
)

// Registry holds every module, action and resource, validated once.
type Registry struct {
	modules   []Manifest
	actions   map[ActionRef]ActionDef
	resources map[string]ResourceDef
	names     map[string]bool // action names, any version
}

// NewRegistry validates the modules against the registration rules (spec
// section 2) and returns the registry, or every violation joined.
func NewRegistry(mods ...Module) (*Registry, error) {
	r := &Registry{
		actions:   map[ActionRef]ActionDef{},
		resources: map[string]ResourceDef{},
		names:     map[string]bool{},
	}
	var errs []error
	fail := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	ids := map[string]bool{}
	for _, mod := range mods {
		m := mod.Manifest()
		switch {
		case !moduleIDPattern.MatchString(m.ID):
			fail("module id %q must match %s", m.ID, moduleIDPattern)
		case slices.Contains(ReservedNames, m.ID):
			fail("module id %q is a reserved CLI name", m.ID)
		case ids[m.ID]:
			fail("module id %q registered twice", m.ID)
		}
		ids[m.ID] = true
		if m.Version < 1 {
			fail("module %s: version %d, want at least 1", m.ID, m.Version)
		}
		for side := range m.Migrations {
			if side != SideController && side != SideAgent {
				fail("module %s: migrations for unknown side %q", m.ID, side)
			}
		}
		for _, a := range m.Actions {
			if err := checkAction(m.ID, a); err != nil {
				errs = append(errs, err)
				continue
			}
			if _, dup := r.actions[a.Ref()]; dup {
				fail("action %s registered twice", a.Ref())
				continue
			}
			r.actions[a.Ref()] = a
			r.names[a.Spec.Name] = true
		}
		for _, res := range m.Resources {
			switch {
			case !resourceNamePattern.MatchString(res.Name) || !strings.HasPrefix(res.Name, m.ID+"."):
				fail("resource %q must match %s and start with %q", res.Name, resourceNamePattern, m.ID+".")
			case res.Read == nil:
				fail("resource %s has no Read", res.Name)
			case r.resources[res.Name].Name != "":
				fail("resource %s registered twice", res.Name)
			default:
				r.resources[res.Name] = res
			}
		}
		r.modules = append(r.modules, m)
	}
	for _, a := range r.actions {
		if a.Spec.Class == ClassCompensable && !r.names[a.Spec.CompensatedBy] {
			fail("action %s: compensating action %q is not registered", a.Ref(), a.Spec.CompensatedBy)
		}
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return r, nil
}

func checkAction(moduleID string, a ActionDef) error {
	s, h := a.Spec, a.Handler
	ref := a.Ref()
	switch {
	case !actionNamePattern.MatchString(s.Name) || !strings.HasPrefix(s.Name, moduleID+"."):
		return fmt.Errorf("action %q must match %s and start with %q", s.Name, actionNamePattern, moduleID+".")
	case s.Version < 1:
		return fmt.Errorf("action %s: version must be at least 1", s.Name)
	case s.Side != SideController && s.Side != SideAgent:
		return fmt.Errorf("action %s: unknown side %q", ref, s.Side)
	case s.BreakGlass && s.Side != SideController:
		return fmt.Errorf("action %s: break-glass actions are controller-side", ref)
	case s.Class == ClassRevertible && s.Side != SideAgent:
		return fmt.Errorf("action %s: revertible actions are agent-side; controller-side actions have no revert timer", ref)
	case s.Class == ClassCompensable && s.CompensatedBy == "":
		return fmt.Errorf("action %s: compensable actions name a compensating action", ref)
	case s.Class != ClassCompensable && s.CompensatedBy != "":
		return fmt.Errorf("action %s: only compensable actions name a compensating action", ref)
	case s.Class == ClassRead && len(s.LockKeys) > 0:
		return fmt.Errorf("action %s: read actions take no locks", ref)
	}
	switch s.Class {
	case ClassRead:
		if h.Read == nil || h.Plan != nil || h.Apply != nil {
			return fmt.Errorf("action %s: a read action has only a Read handler", ref)
		}
	case ClassCompensable, ClassIrreversible:
		if h.Read != nil || h.Plan == nil || h.Apply == nil || h.Verify == nil || h.Snapshot != nil || h.Revert != nil {
			return fmt.Errorf("action %s: a %s action has Plan, Apply and Verify handlers only", ref, s.Class)
		}
	case ClassRevertible:
		if h.Read != nil || h.Plan == nil || h.Apply == nil || h.Verify == nil || h.Snapshot == nil || h.Revert == nil {
			return fmt.Errorf("action %s: a revertible action has Plan, Apply, Verify, Snapshot and Revert handlers", ref)
		}
	default:
		return fmt.Errorf("action %s: unknown class %q", ref, s.Class)
	}
	return nil
}

// Action returns one action version.
func (r *Registry) Action(ref ActionRef) (ActionDef, bool) {
	a, ok := r.actions[ref]
	return a, ok
}

// Actions returns every action, sorted by name and version.
func (r *Registry) Actions() []ActionDef {
	out := make([]ActionDef, 0, len(r.actions))
	for _, a := range r.actions {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Spec.Name != out[j].Spec.Name {
			return out[i].Spec.Name < out[j].Spec.Name
		}
		return out[i].Spec.Version < out[j].Spec.Version
	})
	return out
}

// Resource returns one resource.
func (r *Registry) Resource(name string) (ResourceDef, bool) {
	res, ok := r.resources[name]
	return res, ok
}

// Resources returns every resource, sorted by name.
func (r *Registry) Resources() []ResourceDef {
	out := make([]ResourceDef, 0, len(r.resources))
	for _, res := range r.resources {
		out = append(out, res)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Modules returns the manifests in registration order.
func (r *Registry) Modules() []Manifest { return slices.Clone(r.modules) }

// Module returns one module's manifest.
func (r *Registry) Module(id string) (Manifest, bool) {
	for _, m := range r.modules {
		if m.ID == id {
			return m, true
		}
	}
	return Manifest{}, false
}
