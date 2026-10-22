package core

import (
	"errors"
	"fmt"
	"path"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
)

// ReservedNames are top-level CLI names no module id may take (ADR 0006 and
// ADR 0011).
var ReservedNames = []string{
	"serve", "agent", "update", "user", "controller", "change", "uninstall", "version",
	"host", "module", "token", "audit", "help", "completion", "system",
}

var (
	moduleIDPattern     = regexp.MustCompile(`^[a-z][a-z0-9]*$`)
	actionNamePattern   = regexp.MustCompile(`^[a-z][a-z0-9]*\.[a-z][a-z0-9]*\.[a-z][a-z0-9-]*$`)
	resourceNamePattern = regexp.MustCompile(`^[a-z][a-z0-9]*\.[a-z][a-z0-9]*$`)
	moduleUnitPattern   = regexp.MustCompile(`^/etc/systemd/system/darkpanel-[a-z0-9-]+\.service$`)
	sysctlSegment       = regexp.MustCompile(`^[a-z0-9_.-]+$`)
	subdirPattern       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	externalUnitPattern = regexp.MustCompile(`^[A-Za-z0-9@._-]+\.service$`)
	streamNamePattern   = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
)

// sysctlIface is the one segment of a sysctl key that stands for a network
// interface name.
const sysctlIface = "<iface>"

// Registry holds every module, action, resource and stream, validated once.
type Registry struct {
	modules   []Manifest
	actions   map[ActionRef]ActionDef
	resources map[string]ResourceDef
	streams   map[string]ModuleStream // by "<module>.<name>"
	names     map[string]bool         // action names, any version
}

// ModuleStream is one stream and the module that serves it.
type ModuleStream struct {
	Module string
	Def    StreamDef
}

// NewRegistry validates the modules against the registration rules (spec
// section 2) and returns the registry, or every violation joined.
func NewRegistry(mods ...Module) (*Registry, error) {
	r := &Registry{
		actions:   map[ActionRef]ActionDef{},
		resources: map[string]ResourceDef{},
		streams:   map[string]ModuleStream{},
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
		errs = append(errs, checkOwnedPaths(m)...)
		errs = append(errs, checkExternalUnits(m)...)
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
		if st, ok := mod.(Streamer); ok {
			errs = append(errs, r.addStreams(m.ID, st.Streams())...)
		}
		r.modules = append(r.modules, m)
	}
	for _, a := range r.actions {
		if a.Spec.CompensatedBy != "" && !r.names[a.Spec.CompensatedBy] {
			fail("action %s: compensating action %q is not registered", a.Ref(), a.Spec.CompensatedBy)
		}
		if a.Spec.Class == ClassRevertible {
			errs = append(errs, r.checkRepairedBy(a)...)
		}
	}
	errs = append(errs, r.checkCLINames(ids)...)
	errs = append(errs, checkOwnership(r.modules)...)
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return r, nil
}

// addStreams checks and indexes module's streams.
func (r *Registry) addStreams(module string, defs []StreamDef) []error {
	var errs []error
	for _, d := range defs {
		key := module + "." + d.Name
		switch {
		case !streamNamePattern.MatchString(d.Name):
			errs = append(errs, fmt.Errorf("module %s: stream %q must match %s", module, d.Name, streamNamePattern))
		case d.Run == nil || d.ParamsType == nil || d.EventType == nil:
			errs = append(errs, fmt.Errorf("stream %s needs Run, ParamsType and EventType", key))
		case r.streams[key].Module != "":
			errs = append(errs, fmt.Errorf("stream %s registered twice", key))
		default:
			r.streams[key] = ModuleStream{Module: module, Def: d}
		}
	}
	return errs
}

// checkCLINames refuses names whose CLI commands (spec section 15) would
// clash: an action <m>.<noun>.<verb> is `<m> <noun> <verb>`, a resource
// <m>.<r> is `<m> <r> get`, and the core drops its module segment, so a core
// noun or resource must not be a module id. A stream <m>.<n> is the command
// `<m> <n> --follow`, which can be neither another command nor the group of
// one.
func (r *Registry) checkCLINames(moduleIDs map[string]bool) []error {
	var errs []error
	owners := map[string]string{}
	claim := func(owner string, words []string) {
		if words[0] == ModuleIDCore {
			words = words[1:]
			if moduleIDs[words[0]] {
				errs = append(errs, fmt.Errorf("%s takes the top-level CLI name %q of module %s", owner, words[0], words[0]))
			}
		}
		cmd := strings.Join(words, " ")
		if other, ok := owners[cmd]; ok && other != owner {
			errs = append(errs, fmt.Errorf("%s and %s map to the same CLI command", other, owner))
		}
		owners[cmd] = owner
	}
	for _, a := range r.Actions() {
		claim(a.Spec.Name, strings.Split(a.Spec.Name, "."))
	}
	for _, res := range r.Resources() {
		claim(res.Name, append(strings.Split(res.Name, "."), "get"))
	}
	for _, st := range r.Streams() {
		cmd := st.Module + " " + st.Def.Name
		var clashes []string
		for other, owner := range owners {
			if other == cmd || strings.HasPrefix(other, cmd+" ") {
				clashes = append(clashes, owner)
			}
		}
		slices.Sort(clashes)
		for _, owner := range slices.Compact(clashes) {
			errs = append(errs, fmt.Errorf("stream %s.%s: the CLI command %q clashes with %s", st.Module, st.Def.Name, cmd, owner))
		}
	}
	return errs
}

// checkOwnership refuses two modules, the core included, that declare the
// same owned path, marked block or user: the file writer and uninstall could
// not tell which module a path or block belongs to. Blocks of one file with
// distinct ids are how modules share a file, so they are allowed.
func checkOwnership(mods []Manifest) []error {
	type declaration struct {
		module string
		path   OwnedPath
	}
	var decls []declaration
	for _, m := range mods {
		for _, op := range m.OwnedPaths {
			decls = append(decls, declaration{m.ID, op})
		}
	}
	var errs []error
	for i, a := range decls {
		for _, b := range decls[i+1:] {
			if a.module == b.module || a.path.Path != b.path.Path {
				continue
			}
			aUser, bUser := a.path.Kind == OwnedUser, b.path.Kind == OwnedUser
			switch {
			case aUser != bUser:
				// A user name and a file path never name the same thing.
			case aUser:
				errs = append(errs, fmt.Errorf("user %s is declared by modules %s and %s", a.path.Path, a.module, b.module))
			case a.path.Kind == OwnedBlock && b.path.Kind == OwnedBlock:
				for _, id := range a.path.BlockIDs {
					if slices.Contains(b.path.BlockIDs, id) {
						errs = append(errs, fmt.Errorf("marked block %s of %s is declared by modules %s and %s",
							id, a.path.Path, a.module, b.module))
					}
				}
			default:
				errs = append(errs, fmt.Errorf("owned path %s is declared by modules %s and %s", a.path.Path, a.module, b.module))
			}
		}
	}
	return errs
}

// checkOwnedPaths applies the rules of each owned-path kind to one module's
// declarations (spec 1.8). Only the core writes unit files outside
// /etc/systemd/system/darkpanel-*.service, and a sysctl key never leaves
// /proc/sys.
func checkOwnedPaths(m Manifest) []error {
	var errs []error
	fail := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }
	users := map[string]bool{}
	for _, op := range m.OwnedPaths {
		if op.Kind == OwnedUser {
			users[op.Path] = true
		}
	}
	for _, op := range m.OwnedPaths {
		p := op.Path
		switch op.Kind {
		case OwnedFile, OwnedBlock, OwnedDir, OwnedUser:
		case OwnedUnit:
			if m.ID != ModuleIDCore && !moduleUnitPattern.MatchString(p) {
				fail("module %s: unit path %s must match %s", m.ID, p, moduleUnitPattern)
			}
		case OwnedRestore, OwnedManagedDir, OwnedServiceDir:
			if !path.IsAbs(p) || path.Clean(p) != p {
				fail("module %s: %s path %q is not absolute and clean", m.ID, op.Kind, p)
			}
			if p == "/" {
				fail("module %s: %s path %q is the root directory", m.ID, op.Kind, p)
			}
		case OwnedSysctl:
			if problem := sysctlKeyProblem(p); problem != "" {
				fail("module %s: sysctl key %q %s", m.ID, p, problem)
			}
			if op.Pattern == "" {
				fail("module %s: sysctl key %q has no value Pattern", m.ID, p)
			}
		default:
			fail("module %s: owned path %q has unknown kind %q", m.ID, p, op.Kind)
		}
		if op.Pattern != "" {
			if _, err := regexp.Compile(op.Pattern); err != nil {
				fail("module %s: owned path %s: Pattern does not compile: %v", m.ID, p, err)
			}
			if op.Kind != OwnedSysctl && op.Kind != OwnedManagedDir && op.Kind != OwnedServiceDir {
				fail("module %s: owned path %s: Pattern is for kinds sysctl, managed-dir and service-dir only", m.ID, p)
			}
		}
		switch {
		case op.Kind == OwnedServiceDir && (op.User == "" || !users[op.User]):
			fail("module %s: service-dir %s: User %q is not an owned user of the module", m.ID, p, op.User)
		case op.Kind != OwnedServiceDir && op.User != "":
			fail("module %s: owned path %s: User is for kind service-dir only", m.ID, p)
		}
		if len(op.Subdirs) > 0 && op.Kind != OwnedServiceDir {
			fail("module %s: owned path %s: Subdirs is for kind service-dir only", m.ID, p)
		}
		seen := map[string]bool{}
		for _, d := range op.Subdirs {
			switch {
			case !subdirPattern.MatchString(d):
				fail("module %s: service-dir %s: subdirectory %q must match %s", m.ID, p, d, subdirPattern)
			case seen[d]:
				fail("module %s: service-dir %s: subdirectory %s listed twice", m.ID, p, d)
			}
			seen[d] = true
		}
	}
	return errs
}

// sysctlKeyProblem says what is wrong with a sysctl key, or "" when nothing
// is: a key is relative to /proc/sys, its segments match sysctlSegment and
// are not . or .., and at most one is sysctlIface.
func sysctlKeyProblem(key string) string {
	if strings.HasPrefix(key, "/") {
		return "is absolute; want the key relative to /proc/sys"
	}
	ifaces := 0
	for _, seg := range strings.Split(key, "/") {
		switch {
		case seg == "":
			return "has an empty segment"
		case seg == sysctlIface:
			ifaces++
		case seg == "." || seg == ".." || !sysctlSegment.MatchString(seg):
			return fmt.Sprintf("has a segment %q outside %s", seg, sysctlSegment)
		}
	}
	if ifaces > 1 {
		return "has more than one " + sysctlIface + " segment"
	}
	return ""
}

// checkExternalUnits checks the units a module drives without owning them.
func checkExternalUnits(m Manifest) []error {
	var errs []error
	fail := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }
	known := []string{UnitOpState, UnitOpStart, UnitOpStop, UnitOpRestart}
	names := map[string]bool{}
	for _, u := range m.ExternalUnits {
		if !externalUnitPattern.MatchString(u.Name) {
			fail("module %s: external unit %q must match %s", m.ID, u.Name, externalUnitPattern)
		}
		if names[u.Name] {
			fail("module %s: external unit %s listed twice", m.ID, u.Name)
		}
		names[u.Name] = true
		if len(u.Ops) == 0 {
			fail("module %s: external unit %s has no Ops", m.ID, u.Name)
		}
		ops := map[string]bool{}
		for _, op := range u.Ops {
			switch {
			case !slices.Contains(known, op):
				fail("module %s: external unit %s: op %q is not one of %s", m.ID, u.Name, op, strings.Join(known, ", "))
			case ops[op]:
				fail("module %s: external unit %s: op %s listed twice", m.ID, u.Name, op)
			}
			ops[op] = true
		}
	}
	return errs
}

// maxSyncApplyTimeout is the longest ApplyTimeout an action may declare
// without LongRunning: a synchronous apply must answer before the API
// servers' 60 s write timeout.
const maxSyncApplyTimeout = 45 * time.Second

// checkRepairedBy checks a revertible action's repair action (spec 1.3): every
// registered version of it belongs to the same module and side, is a mutating
// action that is neither a task action nor has a recovery handler, and locks
// no key the revertible action does not hold, because its repair change runs
// under the revertible change's locks.
func (r *Registry) checkRepairedBy(a ActionDef) []error {
	ref, name := a.Ref(), a.Spec.RepairedBy
	module, _, _ := strings.Cut(a.Spec.Name, ".")
	if !r.names[name] {
		return []error{fmt.Errorf("action %s: repair action %q is not registered", ref, name)}
	}
	if !strings.HasPrefix(name, module+".") {
		return []error{fmt.Errorf("action %s: repair action %s is not of module %s", ref, name, module)}
	}
	var errs []error
	for _, rep := range r.actions {
		if rep.Spec.Name != name {
			continue
		}
		switch {
		case rep.Spec.Side != a.Spec.Side:
			errs = append(errs, fmt.Errorf("action %s: repair action %s runs on side %s, not %s", ref, rep.Ref(), rep.Spec.Side, a.Spec.Side))
		case rep.Spec.Class != ClassCompensable && rep.Spec.Class != ClassIrreversible:
			errs = append(errs, fmt.Errorf("action %s: repair action %s is %s, want compensable or irreversible", ref, rep.Ref(), rep.Spec.Class))
		case rep.Spec.Task:
			errs = append(errs, fmt.Errorf("action %s: repair action %s is a task action", ref, rep.Ref()))
		case rep.Handler.Recover != nil:
			errs = append(errs, fmt.Errorf("action %s: repair action %s has a recovery handler", ref, rep.Ref()))
		}
		for _, key := range rep.Spec.LockKeys {
			if !slices.Contains(a.Spec.LockKeys, key) {
				errs = append(errs, fmt.Errorf("action %s: repair action %s takes lock %s, which %s does not hold", ref, rep.Ref(), key, ref))
			}
		}
	}
	return errs
}

// hasSecretField reports whether t, or a type it contains, has a field
// tagged secret:"true".
func hasSecretField(t reflect.Type, seen map[reflect.Type]bool) bool {
	if seen[t] {
		return false
	}
	seen[t] = true
	switch t.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Array, reflect.Map:
		return hasSecretField(t.Elem(), seen)
	case reflect.Struct:
		for i := range t.NumField() {
			f := t.Field(i)
			if f.Tag.Get("secret") == "true" || hasSecretField(f.Type, seen) {
				return true
			}
		}
	}
	return false
}

// checkLifecycle applies the lifecycle declaration rules of spec 1.3 that
// one action can be checked against alone.
func checkLifecycle(moduleID string, a ActionDef) error {
	s, h := a.Spec, a.Handler
	ref := a.Ref()
	compositeKey := "composite:" + moduleID
	undoes := h.Inverse != nil
	compensations := 0
	for _, set := range []bool{undoes, s.CompensatedBy != "", s.ForwardOnly} {
		if set {
			compensations++
		}
	}
	switch {
	case s.Class == ClassComposite && s.Side != SideController:
		return fmt.Errorf("action %s: composite actions are controller-side", ref)
	case s.Class == ClassComposite && (h.Steps == nil || h.Output == nil):
		return fmt.Errorf("action %s: a composite action has Steps and Output handlers", ref)
	case s.Class == ClassComposite && s.Unwind != UnwindReverse && s.Unwind != UnwindForward:
		return fmt.Errorf("action %s: a composite action declares Unwind reverse or forward", ref)
	case s.Class == ClassComposite && !slices.Equal(s.LockKeys, []string{compositeKey}):
		return fmt.Errorf("action %s: a composite action holds exactly the lock key %s", ref, compositeKey)
	case s.Class == ClassComposite && hasSecretField(a.InputType, map[reflect.Type]bool{}):
		return fmt.Errorf("action %s: a composite action's input has no secret field", ref)
	case s.Class != ClassComposite && slices.ContainsFunc(s.LockKeys, func(k string) bool { return strings.HasPrefix(k, "composite:") }):
		return fmt.Errorf("action %s: only composite actions declare a composite:* lock key", ref)
	case s.Class == ClassCompensable && compensations != 1:
		return fmt.Errorf("action %s: a compensable action declares exactly one of an undo handler, CompensatedBy or ForwardOnly", ref)
	case s.ForwardOnly && !s.Converges:
		return fmt.Errorf("action %s: a ForwardOnly action must declare Converges", ref)
	case undoes && (s.Class != ClassCompensable || len(s.LockKeys) == 0):
		return fmt.Errorf("action %s: an undo handler needs a compensable action with at least one lock key", ref)
	case s.ApplyTimeout > maxSyncApplyTimeout && !s.LongRunning:
		return fmt.Errorf("action %s: an ApplyTimeout above %s requires LongRunning", ref, maxSyncApplyTimeout)
	case s.Task && s.Class != ClassCompensable && s.Class != ClassIrreversible:
		return fmt.Errorf("action %s: a task action is compensable or irreversible", ref)
	case s.Class == ClassRevertible && s.RepairedBy == "":
		return fmt.Errorf("action %s: a revertible action names its repair action in RepairedBy", ref)
	case s.Class != ClassRevertible && s.RepairedBy != "":
		return fmt.Errorf("action %s: only revertible actions name a repair action", ref)
	case s.Task && (s.Side != SideAgent || len(s.LockKeys) == 0 || s.ApplyTimeout <= 0):
		return fmt.Errorf("action %s: a task action is agent-side with lock keys and an ApplyTimeout", ref)
	case s.Task && (h.Apply != nil || h.Task == nil):
		return fmt.Errorf("action %s: a task action has a Task handler and no Apply", ref)
	case !s.Task && h.Task != nil:
		return fmt.Errorf("action %s: only task actions have a Task handler", ref)
	case s.Class != ClassComposite && (h.Steps != nil || h.Output != nil):
		return fmt.Errorf("action %s: only composite actions have Steps and Output handlers", ref)
	}
	return nil
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
	case s.Class == ClassRevertible && s.Side == SideAgent && len(s.LockKeys) == 0:
		return fmt.Errorf("action %s: a revertible agent action declares at least one lock key, so an update's reservation holds it off", ref)
	case s.Class != ClassCompensable && s.CompensatedBy != "":
		return fmt.Errorf("action %s: only compensable actions name a compensating action", ref)
	case s.Class == ClassRead && len(s.LockKeys) > 0:
		return fmt.Errorf("action %s: read actions take no locks", ref)
	}
	if err := checkLifecycle(moduleID, a); err != nil {
		return err
	}
	if err := checkGuardsAndScope(a); err != nil {
		return err
	}
	if err := checkReveal(a); err != nil {
		return err
	}
	switch s.Class {
	case ClassRead:
		// checkReveal let exactly one of Read and Reveal through.
		if (h.Read == nil && h.Reveal == nil) || h.Plan != nil || h.Apply != nil {
			return fmt.Errorf("action %s: a read action has only a Read handler, or a Reveal handler when it reveals", ref)
		}
	case ClassCompensable, ClassIrreversible:
		// checkLifecycle already required Task instead of Apply for a task action.
		if h.Read != nil || h.Plan == nil || (h.Apply == nil && h.Task == nil) || h.Verify == nil || h.Snapshot != nil || h.Revert != nil {
			return fmt.Errorf("action %s: a %s action has Plan, Apply and Verify handlers only", ref, s.Class)
		}
	case ClassRevertible:
		if h.Read != nil || h.Plan == nil || h.Apply == nil || h.Verify == nil || h.Snapshot == nil || h.Revert == nil {
			return fmt.Errorf("action %s: a revertible action has Plan, Apply, Verify, Snapshot and Revert handlers", ref)
		}
	case ClassComposite:
		if h.Read != nil || h.Plan != nil || h.Apply != nil || h.Verify != nil || h.Snapshot != nil || h.Revert != nil {
			return fmt.Errorf("action %s: a composite action has Steps and Output handlers only", ref)
		}
	default:
		return fmt.Errorf("action %s: unknown class %q", ref, s.Class)
	}
	return nil
}

// guardedType is the type an action with Guards embeds in its input.
var guardedType = reflect.TypeFor[Guarded]()

// embedsGuarded reports whether t is a struct with an anonymous field of type
// Guarded.
func embedsGuarded(t reflect.Type) bool {
	if t == nil || t.Kind() != reflect.Struct {
		return false
	}
	for i := range t.NumField() {
		if f := t.Field(i); f.Anonymous && f.Type == guardedType {
			return true
		}
	}
	return false
}

// checkGuardsAndScope applies the guard and scope declaration rules (spec
// 1.5, 1.7): Guards and an embedded Guarded go together, so the schema
// carries guard and overrides exactly when a plan may report a guard, and
// MinScope only raises the scope the class and side require.
func checkGuardsAndScope(a ActionDef) error {
	s := a.Spec
	ref := a.Ref()
	guarded := embedsGuarded(a.InputType)
	switch {
	case len(s.Guards) > 0 && !guarded:
		return fmt.Errorf("action %s: an action with Guards has an input type that embeds core.Guarded", ref)
	case len(s.Guards) == 0 && guarded:
		return fmt.Errorf("action %s: an input type that embeds core.Guarded belongs to an action with Guards", ref)
	}
	ids := map[string]bool{}
	for _, g := range s.Guards {
		switch {
		case g.ID == "":
			return fmt.Errorf("action %s: a guard has an empty ID", ref)
		case ids[g.ID]:
			return fmt.Errorf("action %s: guard %s is declared twice", ref, g.ID)
		}
		ids[g.ID] = true
	}
	switch need := DefaultScope(s.Class, s.Side); {
	case s.MinScope == "":
	case s.MinScope != ScopeRead && s.MinScope != ScopeOperate && s.MinScope != ScopeAdmin:
		return fmt.Errorf("action %s: unknown MinScope %q", ref, s.MinScope)
	case !s.MinScope.Allows(need):
		return fmt.Errorf("action %s: MinScope %s is below the %s scope the action needs; MinScope only raises", ref, s.MinScope, need)
	}
	return nil
}

// checkReveal applies the rules of reveal actions and Internal reads (spec
// 1.7, 1.14): both are read actions whose only handler is Reveal, never both
// at once, and a reveal action needs admin. Every other action has no Reveal
// handler.
func checkReveal(a ActionDef) error {
	s, h := a.Spec, a.Handler
	ref := a.Ref()
	flagged := s.Reveal || s.Internal
	switch {
	case s.Reveal && s.Internal:
		return fmt.Errorf("action %s: an action is a reveal action or an internal read, not both", ref)
	case flagged && s.Class != ClassRead:
		return fmt.Errorf("action %s: reveal actions and internal reads are read actions", ref)
	case flagged && (h.Reveal == nil || h.Read != nil):
		return fmt.Errorf("action %s: a reveal action or internal read has a Reveal handler and no Read handler", ref)
	case !flagged && h.Reveal != nil:
		return fmt.Errorf("action %s: only reveal actions and internal reads have a Reveal handler", ref)
	case s.Reveal && s.MinScope != ScopeAdmin:
		return fmt.Errorf("action %s: a reveal action declares MinScope admin", ref)
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

// Stream returns one stream of module.
func (r *Registry) Stream(module, name string) (StreamDef, bool) {
	st, ok := r.streams[module+"."+name]
	return st.Def, ok
}

// Streams returns every stream, sorted by module and then name.
func (r *Registry) Streams() []ModuleStream {
	out := make([]ModuleStream, 0, len(r.streams))
	for _, st := range r.streams {
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Module != out[j].Module {
			return out[i].Module < out[j].Module
		}
		return out[i].Def.Name < out[j].Def.Name
	})
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
