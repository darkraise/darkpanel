package core_test

import (
	"context"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)

type qaIn struct {
	Name string `json:"name"`
}

type qaOut struct {
	Name string `json:"name"`
}

type qaMutator struct{}

func (qaMutator) Plan(_ context.Context, _ core.Env, in qaIn) (core.PlanOutcome, error) {
	return core.PlanOutcome{Diff: []core.DiffLine{{Op: "+", Text: in.Name}}, StateHash: "h"}, nil
}
func (qaMutator) Apply(_ context.Context, _ core.Env, in qaIn) (qaOut, error) {
	return qaOut(in), nil
}
func (qaMutator) Verify(context.Context, core.Env, qaIn, qaOut) error { return nil }

type qaModule core.Manifest

func (m qaModule) Manifest() core.Manifest { return core.Manifest(m) }

func qaCompensable(name string, edit func(*core.ActionSpec)) core.ActionDef {
	spec := core.ActionSpec{Name: name, Version: 1, Class: core.ClassCompensable, Side: core.SideAgent,
		LockKeys: []string{"qa"}, ForwardOnly: true, Converges: true}
	if edit != nil {
		edit(&spec)
	}
	return core.DefineMutating(spec, qaMutator{})
}

// qaBuild reports whether a registry of one module "qa" holding the actions builds.
func qaBuild(actions ...core.ActionDef) error {
	_, err := core.NewRegistry(qaModule{ID: "qa", Version: 1, Actions: actions})
	return err
}

func TestA4_RegistryRefusesStepUpCombinations(t *testing.T) {
	reveal := func(edit func(*core.ActionSpec)) core.ActionDef {
		spec := core.ActionSpec{Name: "qa.item.reveal", Version: 1, Side: core.SideAgent, MinScope: core.ScopeAdmin}
		if edit != nil {
			edit(&spec)
		}
		return core.DefineReveal(spec, func(context.Context, core.Env, qaIn) (core.Revealed[qaOut], error) {
			return core.Revealed[qaOut]{Identity: "qa"}, nil
		})
	}
	breakGlass := func(edit func(*core.ActionSpec)) core.ActionDef {
		return qaCompensable("qa.user.create", func(s *core.ActionSpec) {
			s.Side, s.LockKeys, s.BreakGlass = core.SideController, nil, true
			if edit != nil {
				edit(s)
			}
		})
	}
	irreversible := func(edit func(*core.ActionSpec)) core.ActionDef {
		spec := core.ActionSpec{Name: "qa.item.wipe", Version: 1, Class: core.ClassIrreversible, Side: core.SideAgent, LockKeys: []string{"qa"}}
		if edit != nil {
			edit(&spec)
		}
		return core.DefineMutating(spec, qaMutator{})
	}
	stepUp := func(s *core.ActionSpec) { s.StepUp = true }
	undoStepUp := func(s *core.ActionSpec) { s.UndoStepUp = true }

	for _, tt := range []struct {
		name     string
		control  core.ActionDef // the same action without the flag: must build
		violator core.ActionDef // with the flag: must be refused
	}{
		{"StepUp with Reveal", reveal(nil), reveal(stepUp)},
		{"UndoStepUp with Reveal", reveal(nil), reveal(undoStepUp)},
		{"StepUp with BreakGlass", breakGlass(nil), breakGlass(stepUp)},
		{"UndoStepUp with BreakGlass", breakGlass(nil), breakGlass(undoStepUp)},
		{"UndoStepUp on an irreversible action", irreversible(nil), irreversible(undoStepUp)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := qaBuild(tt.control); err != nil {
				t.Fatalf("control without the flag does not build: %v", err)
			}
			if err := qaBuild(tt.violator); err == nil {
				t.Errorf("the registry accepted %s", tt.name)
			}
		})
	}

	t.Run("StepUp and UndoStepUp on a compensable action build", func(t *testing.T) {
		if err := qaBuild(qaCompensable("qa.item.pin", func(s *core.ActionSpec) { s.StepUp, s.UndoStepUp = true, true })); err != nil {
			t.Errorf("a compensable action with StepUp and UndoStepUp was refused: %v", err)
		}
	})
}

func TestA4_RegistryRefusesExternalUnitMisuse(t *testing.T) {
	const jobsPattern = `^darkpanel-job-[a-z0-9][a-z0-9-]{0,39}\.(service|timer)$`
	jobs := qaModule{ID: "jobs", Version: 1, OwnedPaths: []core.OwnedPath{{Path: core.UnitSetDir, Kind: core.OwnedUnitSet, Pattern: jobsPattern}}}
	ext := func(name string, ops ...string) core.Module {
		return qaModule{ID: "other", Version: 1, ExternalUnits: []core.ExternalUnit{{Name: name, Ops: ops}}}
	}

	for _, tt := range []struct {
		name string
		mods []core.Module
		ok   bool
	}{
		{"state read of an unrelated external unit", []core.Module{jobs, ext("tailscaled.service", core.UnitOpState, core.UnitOpStart)}, true},
		{"another module's unit-set prefix", []core.Module{jobs, ext("darkpanel-job-backup.service", core.UnitOpState)}, false},
		{"another module's unit-set prefix, timer", []core.Module{jobs, ext("darkpanel-job-backup.timer", core.UnitOpState)}, false},
		{"state of darkpanel-controller.service", []core.Module{ext("darkpanel-controller.service", core.UnitOpState)}, true},
		{"start of a darkpanel- unit", []core.Module{ext("darkpanel-controller.service", core.UnitOpState, core.UnitOpStart)}, false},
		{"stop of a darkpanel- unit", []core.Module{ext("darkpanel-controller.service", core.UnitOpStop)}, false},
		{"restart of a darkpanel- unit", []core.Module{ext("darkpanel-controller.service", core.UnitOpRestart)}, false},
		{"reload of a darkpanel- unit", []core.Module{ext("darkpanel-controller.service", core.UnitOpReload)}, false},
		{"start of a non-darkpanel unit", []core.Module{ext("nginx.service", core.UnitOpStart)}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := core.NewRegistry(tt.mods...)
			if tt.ok && err != nil {
				t.Errorf("NewRegistry refused a valid registry: %v", err)
			}
			if !tt.ok && err == nil {
				t.Errorf("NewRegistry accepted %s", tt.name)
			}
		})
	}
}

func TestA5_RegistryRefusesAUnitInsideItsOwnUnitSet(t *testing.T) {
	const pattern = `^darkpanel-job-[a-z0-9][a-z0-9-]{0,39}\.(service|timer)$`
	set := core.OwnedPath{Path: core.UnitSetDir, Kind: core.OwnedUnitSet, Pattern: pattern}
	inside := core.OwnedPath{Path: core.UnitSetDir + "/darkpanel-job-extra.service", Kind: core.OwnedUnit}
	outside := core.OwnedPath{Path: core.UnitSetDir + "/darkpanel-other.service", Kind: core.OwnedUnit}

	if _, err := core.NewRegistry(qaModule{ID: "jobs", Version: 1, OwnedPaths: []core.OwnedPath{set, outside}}); err != nil {
		t.Fatalf("a module owning a unit outside its own unit set was refused: %v", err)
	}
	if _, err := core.NewRegistry(qaModule{ID: "jobs", Version: 1, OwnedPaths: []core.OwnedPath{set, inside}}); err == nil {
		t.Error("the registry accepted a module owning a unit inside its own unit set")
	}
}
