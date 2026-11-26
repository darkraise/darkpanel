package platform

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"syscall"

	"github.com/darkraise/darkpanel/internal/core"
)

// scopeTaskUnit matches the task units of 1.3c, which the task runtime
// starts and stops and a module may only read.
var scopeTaskUnit = regexp.MustCompile(`^darkpanel-task-[0-9A-Za-z]+-[a-z]+-[0-9]+\.service$`)

// scopedUnits is a core.Units limited to the units one module may drive.
type scopedUnits struct {
	u        core.Units
	module   string
	owned    map[string]bool     // the unit names of the module's unit owned paths
	paths    map[string]bool     // those paths, which Enable and Disable also take
	sets     []core.OwnedPath    // the module's unit sets
	external map[string][]string // ExternalUnits: name → ops
}

// ScopeUnits returns u limited to module m (1.8): every method on the units
// m declares as owned paths of kind unit or whose names its unit sets match,
// State on task units, the declared ops on m's external units (ReloadUnit for
// UnitOpReload), and Reload when m declares a unit or a unit set. Everything
// else fails with an error naming the module, the method and the unit. The
// services module gets adminUnits instead (ADR 0020).
func ScopeUnits(u core.Units, m core.Manifest) core.Units {
	if m.ID == ServicesModule {
		return adminUnits{u: u}
	}
	s := &scopedUnits{u: u, module: m.ID, owned: map[string]bool{}, paths: map[string]bool{}, external: map[string][]string{}}
	for _, p := range m.OwnedPaths {
		switch p.Kind {
		case core.OwnedUnit:
			s.owned[filepath.Base(p.Path)] = true
			s.paths[p.Path] = true
		case core.OwnedUnitSet:
			s.sets = append(s.sets, p)
		}
	}
	for _, e := range m.ExternalUnits {
		s.external[e.Name] = slices.Clone(e.Ops)
	}
	return s
}

// ModuleUnits is lifecycle.Config.ModuleUnits over reg: the core module gets
// u itself, another registered module ScopeUnits(u, its manifest), an
// unknown module nil; a nil u gives nil.
func ModuleUnits(u core.Units, reg *core.Registry) func(module string) core.Units {
	scoped := map[string]core.Units{}
	if u != nil {
		for _, m := range reg.Modules() {
			scoped[m.ID] = ScopeUnits(u, m)
		}
		scoped[core.ModuleIDCore] = u
	}
	return func(module string) core.Units { return scoped[module] }
}

func (s *scopedUnits) refuse(method, unit string) error {
	return fmt.Errorf("platform: module %s may not %s unit %s", s.module, method, unit)
}

// inSet reports whether one of the module's unit sets holds the unit name,
// or, when dir is not "", holds it in dir.
func (s *scopedUnits) inSet(dir, name string) bool {
	return slices.ContainsFunc(s.sets, func(p core.OwnedPath) bool {
		return (dir == "" || dir == p.Path) && fullMatch(p.Pattern, name)
	})
}

// allow refuses method on unit unless the module owns unit, one of its unit
// sets holds it, or it declares unit external with op; op "" admits owned
// units and units of the sets only.
func (s *scopedUnits) allow(method, op, unit string) error {
	if s.owned[unit] || s.inSet("", unit) || (op != "" && slices.Contains(s.external[unit], op)) {
		return nil
	}
	return s.refuse(method, unit)
}

// allowFiles refuses method unless every unit file is an owned unit, by
// name or by its declared path, or a unit of one of the module's sets, by
// name or by its path in the set's directory.
func (s *scopedUnits) allowFiles(method string, unitFiles []string) error {
	for _, f := range unitFiles {
		inSet := s.inSet("", f) || (filepath.IsAbs(f) && s.inSet(filepath.Dir(f), filepath.Base(f)))
		if !s.owned[f] && !s.paths[f] && !inSet {
			return s.refuse(method, f)
		}
	}
	return nil
}

func (s *scopedUnits) StartTransientService(_ context.Context, name string, _ core.TransientService) error {
	return s.refuse("StartTransientService", name)
}

func (s *scopedUnits) StartTransientTimer(_ context.Context, name string, _ core.TransientTimer) error {
	return s.refuse("StartTransientTimer", name)
}

func (s *scopedUnits) Start(ctx context.Context, unit string) error {
	if err := s.allow("Start", core.UnitOpStart, unit); err != nil {
		return err
	}
	return s.u.Start(ctx, unit)
}

func (s *scopedUnits) Stop(ctx context.Context, unit string) error {
	if err := s.allow("Stop", core.UnitOpStop, unit); err != nil {
		return err
	}
	return s.u.Stop(ctx, unit)
}

func (s *scopedUnits) Restart(ctx context.Context, unit string) error {
	if err := s.allow("Restart", core.UnitOpRestart, unit); err != nil {
		return err
	}
	return s.u.Restart(ctx, unit)
}

func (s *scopedUnits) Kill(ctx context.Context, unit string, sig syscall.Signal) error {
	if err := s.allow("Kill", "", unit); err != nil {
		return err
	}
	return s.u.Kill(ctx, unit, sig)
}

func (s *scopedUnits) ResetFailed(ctx context.Context, unit string) error {
	if err := s.allow("ResetFailed", "", unit); err != nil {
		return err
	}
	return s.u.ResetFailed(ctx, unit)
}

func (s *scopedUnits) Enable(ctx context.Context, unitFiles ...string) error {
	if err := s.allowFiles("Enable", unitFiles); err != nil {
		return err
	}
	return s.u.Enable(ctx, unitFiles...)
}

func (s *scopedUnits) Disable(ctx context.Context, unitFiles ...string) error {
	if err := s.allowFiles("Disable", unitFiles); err != nil {
		return err
	}
	return s.u.Disable(ctx, unitFiles...)
}

// ReloadUnit asks an external unit that declares UnitOpReload to reload its
// configuration.
func (s *scopedUnits) ReloadUnit(ctx context.Context, unit string) error {
	if err := s.allow("ReloadUnit", core.UnitOpReload, unit); err != nil {
		return err
	}
	r, ok := s.u.(core.UnitReloader)
	if !ok {
		return fmt.Errorf("platform: the unit client of module %s cannot ReloadUnit", s.module)
	}
	return r.ReloadUnit(ctx, unit)
}

// Reload reloads every unit file, so only a module with unit files of its
// own needs it.
func (s *scopedUnits) Reload(ctx context.Context) error {
	if len(s.owned) == 0 && len(s.sets) == 0 {
		return fmt.Errorf("platform: module %s may not Reload units: it declares no unit", s.module)
	}
	return s.u.Reload(ctx)
}

func (s *scopedUnits) State(ctx context.Context, unit string) (core.UnitState, error) {
	if !scopeTaskUnit.MatchString(unit) {
		if err := s.allow("State", core.UnitOpState, unit); err != nil {
			return core.UnitState{}, err
		}
	}
	return s.u.State(ctx, unit)
}

var _ core.TimerReader = (*scopedUnits)(nil)

// Timers lists every timer, read-only, for a module with a unit set: its
// jobs list shows the host's other timers.
func (s *scopedUnits) Timers(ctx context.Context) ([]core.TimerInfo, error) {
	tr, ok := s.u.(core.TimerReader)
	switch {
	case len(s.sets) == 0:
		return nil, fmt.Errorf("platform: module %s may not list timers: it declares no unit set", s.module)
	case !ok:
		return nil, fmt.Errorf("platform: the unit client of module %s cannot list timers", s.module)
	}
	return tr.Timers(ctx)
}

// LastRun reads the last run of a service of one of the module's sets.
func (s *scopedUnits) LastRun(ctx context.Context, unit string) (core.ServiceRun, error) {
	if !s.inSet("", unit) {
		return core.ServiceRun{}, s.refuse("LastRun", unit)
	}
	tr, ok := s.u.(core.TimerReader)
	if !ok {
		return core.ServiceRun{}, fmt.Errorf("platform: the unit client of module %s cannot read last runs", s.module)
	}
	return tr.LastRun(ctx, unit)
}

func (s *scopedUnits) List(_ context.Context, pattern string) ([]core.UnitStatus, error) {
	return nil, s.refuse("List", pattern)
}
