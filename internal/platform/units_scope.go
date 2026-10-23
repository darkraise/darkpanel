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
	external map[string][]string // ExternalUnits: name → ops
}

// ScopeUnits returns u limited to module m (1.8): every method on the units
// m declares as owned paths of kind unit, State on task units, the declared
// ops on m's external units, and Reload when m declares a unit. Everything
// else fails with an error naming the module, the method and the unit.
func ScopeUnits(u core.Units, m core.Manifest) core.Units {
	s := &scopedUnits{u: u, module: m.ID, owned: map[string]bool{}, paths: map[string]bool{}, external: map[string][]string{}}
	for _, p := range m.OwnedPaths {
		if p.Kind == core.OwnedUnit {
			s.owned[filepath.Base(p.Path)] = true
			s.paths[p.Path] = true
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

// allow refuses method on unit unless the module owns unit or declares it
// external with op; op "" admits owned units only.
func (s *scopedUnits) allow(method, op, unit string) error {
	if s.owned[unit] || (op != "" && slices.Contains(s.external[unit], op)) {
		return nil
	}
	return s.refuse(method, unit)
}

// allowFiles refuses method unless every unit file is an owned unit, by
// name or by its declared path.
func (s *scopedUnits) allowFiles(method string, unitFiles []string) error {
	for _, f := range unitFiles {
		if !s.owned[f] && !s.paths[f] {
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

// Reload reloads every unit file, so only a module with unit files of its
// own needs it.
func (s *scopedUnits) Reload(ctx context.Context) error {
	if len(s.owned) == 0 {
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

func (s *scopedUnits) List(_ context.Context, pattern string) ([]core.UnitStatus, error) {
	return nil, s.refuse("List", pattern)
}
