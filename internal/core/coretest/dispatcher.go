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
	PlanFunc     func(ctx context.Context, p core.Principal, hostID string, a core.ActionRef, input json.RawMessage) (core.PlanView, error)
	ApplyFunc    func(ctx context.Context, p core.Principal, hostID string, a core.ActionRef, planID string, opts core.ApplyOptions) (core.ChangeView, error)
	ConfirmFunc  func(ctx context.Context, p core.Principal, changeID string) (core.ChangeView, error)
	RevertFunc   func(ctx context.Context, p core.Principal, changeID string) (core.ChangeView, error)
	ChangeFunc   func(ctx context.Context, p core.Principal, changeID string) (core.ChangeView, error)
	ChangesFunc  func(ctx context.Context, p core.Principal, f core.ChangeFilter) ([]core.ChangeView, error)
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
func (f *FakeDispatcher) Plan(ctx context.Context, p core.Principal, hostID string, a core.ActionRef, input json.RawMessage) (core.PlanView, error) {
	if f.PlanFunc == nil {
		return core.PlanView{}, notConfigured("FakeDispatcher", "Plan")
	}
	return f.PlanFunc(ctx, p, hostID, a, input)
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
