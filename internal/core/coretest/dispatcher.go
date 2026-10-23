package coretest

import (
	"context"
	"encoding/json"

	"github.com/darkraise/darkpanel/internal/core"
)

// FakeDispatcher is a core.Dispatcher whose methods call the matching
// function field; a method whose field is nil returns an error naming it.
type FakeDispatcher struct {
	ReadFunc     func(ctx context.Context, p core.Principal, hostID string, a core.ActionRef, input json.RawMessage) (json.RawMessage, error)
	ResourceFunc func(ctx context.Context, p core.Principal, hostID, name string) (json.RawMessage, error)
	PlanFunc     func(ctx context.Context, p core.Principal, hostID string, a core.ActionRef, input json.RawMessage, opts core.PlanOptions) (core.PlanView, error)
	ApplyFunc    func(ctx context.Context, p core.Principal, hostID string, a core.ActionRef, planID string, opts core.ApplyOptions) (core.ChangeView, error)
	ConfirmFunc  func(ctx context.Context, p core.Principal, changeID string) (core.ChangeView, error)
	RevertFunc   func(ctx context.Context, p core.Principal, changeID string) (core.ChangeView, error)
	ChangeFunc   func(ctx context.Context, p core.Principal, changeID string) (core.ChangeView, error)
	ChangesFunc  func(ctx context.Context, p core.Principal, f core.ChangeFilter) ([]core.ChangeView, error)
	UndoFunc     func(ctx context.Context, p core.Principal, changeID string) (core.ChangeView, error)
	RepairFunc   func(ctx context.Context, p core.Principal, changeID string, req core.RepairRequest) (core.RepairResult, error)
	ReleaseFunc  func(ctx context.Context, p core.Principal, changeID, reason string) (core.ReleaseResult, error)
	EffectsFunc  func(ctx context.Context, p core.Principal, changeID string) ([]core.EffectView, error)
	ObserveFunc  func(ctx context.Context, p core.Principal, changeID string, seq int) (core.ObserveResult, error)
	ResolveFunc  func(ctx context.Context, p core.Principal, changeID string, seq int, req core.ResolveRequest) (core.EffectView, error)
	StepsFunc    func(ctx context.Context, p core.Principal, changeID string) ([]core.StepView, error)
	RevealFunc   func(ctx context.Context, p core.Principal, hostID string, a core.ActionRef, input json.RawMessage) (json.RawMessage, error)
}

var _ core.Dispatcher = (*FakeDispatcher)(nil)

// Read calls ReadFunc.
func (f *FakeDispatcher) Read(ctx context.Context, p core.Principal, hostID string, a core.ActionRef, input json.RawMessage) (json.RawMessage, error) {
	if f.ReadFunc == nil {
		return nil, notConfigured("FakeDispatcher", "Read")
	}
	return f.ReadFunc(ctx, p, hostID, a, input)
}

// Resource calls ResourceFunc.
func (f *FakeDispatcher) Resource(ctx context.Context, p core.Principal, hostID, name string) (json.RawMessage, error) {
	if f.ResourceFunc == nil {
		return nil, notConfigured("FakeDispatcher", "Resource")
	}
	return f.ResourceFunc(ctx, p, hostID, name)
}

// Plan calls PlanFunc.
func (f *FakeDispatcher) Plan(ctx context.Context, p core.Principal, hostID string, a core.ActionRef, input json.RawMessage, opts core.PlanOptions) (core.PlanView, error) {
	if f.PlanFunc == nil {
		return core.PlanView{}, notConfigured("FakeDispatcher", "Plan")
	}
	return f.PlanFunc(ctx, p, hostID, a, input, opts)
}

// Apply calls ApplyFunc.
func (f *FakeDispatcher) Apply(ctx context.Context, p core.Principal, hostID string, a core.ActionRef, planID string, opts core.ApplyOptions) (core.ChangeView, error) {
	if f.ApplyFunc == nil {
		return core.ChangeView{}, notConfigured("FakeDispatcher", "Apply")
	}
	return f.ApplyFunc(ctx, p, hostID, a, planID, opts)
}

// Confirm calls ConfirmFunc.
func (f *FakeDispatcher) Confirm(ctx context.Context, p core.Principal, changeID string) (core.ChangeView, error) {
	if f.ConfirmFunc == nil {
		return core.ChangeView{}, notConfigured("FakeDispatcher", "Confirm")
	}
	return f.ConfirmFunc(ctx, p, changeID)
}

// Revert calls RevertFunc.
func (f *FakeDispatcher) Revert(ctx context.Context, p core.Principal, changeID string) (core.ChangeView, error) {
	if f.RevertFunc == nil {
		return core.ChangeView{}, notConfigured("FakeDispatcher", "Revert")
	}
	return f.RevertFunc(ctx, p, changeID)
}

// Change calls ChangeFunc.
func (f *FakeDispatcher) Change(ctx context.Context, p core.Principal, changeID string) (core.ChangeView, error) {
	if f.ChangeFunc == nil {
		return core.ChangeView{}, notConfigured("FakeDispatcher", "Change")
	}
	return f.ChangeFunc(ctx, p, changeID)
}

// Changes calls ChangesFunc.
func (f *FakeDispatcher) Changes(ctx context.Context, p core.Principal, filter core.ChangeFilter) ([]core.ChangeView, error) {
	if f.ChangesFunc == nil {
		return nil, notConfigured("FakeDispatcher", "Changes")
	}
	return f.ChangesFunc(ctx, p, filter)
}

// Undo calls UndoFunc.
func (f *FakeDispatcher) Undo(ctx context.Context, p core.Principal, changeID string) (core.ChangeView, error) {
	if f.UndoFunc == nil {
		return core.ChangeView{}, notConfigured("FakeDispatcher", "Undo")
	}
	return f.UndoFunc(ctx, p, changeID)
}

// Repair calls RepairFunc.
func (f *FakeDispatcher) Repair(ctx context.Context, p core.Principal, changeID string, req core.RepairRequest) (core.RepairResult, error) {
	if f.RepairFunc == nil {
		return core.RepairResult{}, notConfigured("FakeDispatcher", "Repair")
	}
	return f.RepairFunc(ctx, p, changeID, req)
}

// Release calls ReleaseFunc.
func (f *FakeDispatcher) Release(ctx context.Context, p core.Principal, changeID, reason string) (core.ReleaseResult, error) {
	if f.ReleaseFunc == nil {
		return core.ReleaseResult{}, notConfigured("FakeDispatcher", "Release")
	}
	return f.ReleaseFunc(ctx, p, changeID, reason)
}

// Effects calls EffectsFunc.
func (f *FakeDispatcher) Effects(ctx context.Context, p core.Principal, changeID string) ([]core.EffectView, error) {
	if f.EffectsFunc == nil {
		return nil, notConfigured("FakeDispatcher", "Effects")
	}
	return f.EffectsFunc(ctx, p, changeID)
}

// Observe calls ObserveFunc.
func (f *FakeDispatcher) Observe(ctx context.Context, p core.Principal, changeID string, seq int) (core.ObserveResult, error) {
	if f.ObserveFunc == nil {
		return core.ObserveResult{}, notConfigured("FakeDispatcher", "Observe")
	}
	return f.ObserveFunc(ctx, p, changeID, seq)
}

// Resolve calls ResolveFunc.
func (f *FakeDispatcher) Resolve(ctx context.Context, p core.Principal, changeID string, seq int, req core.ResolveRequest) (core.EffectView, error) {
	if f.ResolveFunc == nil {
		return core.EffectView{}, notConfigured("FakeDispatcher", "Resolve")
	}
	return f.ResolveFunc(ctx, p, changeID, seq, req)
}

// Reveal calls RevealFunc.
func (f *FakeDispatcher) Reveal(ctx context.Context, p core.Principal, hostID string, a core.ActionRef, input json.RawMessage) (json.RawMessage, error) {
	if f.RevealFunc == nil {
		return nil, notConfigured("FakeDispatcher", "Reveal")
	}
	return f.RevealFunc(ctx, p, hostID, a, input)
}

// Steps calls StepsFunc.
func (f *FakeDispatcher) Steps(ctx context.Context, p core.Principal, changeID string) ([]core.StepView, error) {
	if f.StepsFunc == nil {
		return nil, notConfigured("FakeDispatcher", "Steps")
	}
	return f.StepsFunc(ctx, p, changeID)
}
