package coretest

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

func notConfigured(fake, method string) error {
	return fmt.Errorf("coretest.%s: %s is not configured", fake, method)
}

// FakeAgent is a core.AgentClient whose methods call the matching function
// field; a method whose field is nil returns an error naming it.
type FakeAgent struct {
	HelloFunc      func(ctx context.Context) (core.AgentHello, error)
	ReadFunc       func(ctx context.Context, p core.Principal, a core.ActionRef, input json.RawMessage) (json.RawMessage, error)
	ResourceFunc   func(ctx context.Context, p core.Principal, name string) (json.RawMessage, error)
	PlanFunc       func(ctx context.Context, p core.Principal, a core.ActionRef, input json.RawMessage, opts core.PlanOptions) (core.PlanView, error)
	ApplyFunc      func(ctx context.Context, p core.Principal, planID string, opts core.ApplyOptions) (core.ChangeView, error)
	ConfirmFunc    func(ctx context.Context, p core.Principal, changeID string) (core.ChangeView, error)
	RevertFunc     func(ctx context.Context, p core.Principal, changeID string) (core.ChangeView, error)
	ChangeFunc     func(ctx context.Context, changeID string) (core.ChangeView, error)
	ChangesFunc    func(ctx context.Context, f core.ChangeFilter) ([]core.ChangeView, error)
	PreflightFunc  func(ctx context.Context, moduleID string) (core.ModuleStatus, error)
	JournalFunc    func(ctx context.Context, afterSeq int64, limit int) ([]core.JournalEntry, error)
	AckFunc        func(ctx context.Context, seq int64) error
	UndoFunc       func(ctx context.Context, p core.Principal, changeID string, call *core.CallContext) (core.ChangeView, error)
	RepairFunc     func(ctx context.Context, p core.Principal, changeID string, req core.RepairRequest) (core.RepairResult, error)
	ReleaseFunc    func(ctx context.Context, p core.Principal, changeID, reason string, call *core.CallContext) (core.ReleaseResult, error)
	EffectsFunc    func(ctx context.Context, p core.Principal, changeID string) ([]core.EffectView, error)
	ObserveFunc    func(ctx context.Context, p core.Principal, changeID string, seq int) (core.ObserveResult, error)
	ResolveFunc    func(ctx context.Context, p core.Principal, changeID string, seq int, req core.ResolveRequest) (core.EffectView, error)
	LookupFunc     func(ctx context.Context, planID string, private bool) (core.PlanLookup, error)
	RetireFunc     func(ctx context.Context, planID string) (core.PlanLookup, error)
	AckPrivateFunc func(ctx context.Context, changeID string, final bool) error
}

var _ core.AgentClient = (*FakeAgent)(nil)

// Hello calls HelloFunc.
func (f *FakeAgent) Hello(ctx context.Context) (core.AgentHello, error) {
	if f.HelloFunc == nil {
		return core.AgentHello{}, notConfigured("FakeAgent", "Hello")
	}
	return f.HelloFunc(ctx)
}

// Read calls ReadFunc.
func (f *FakeAgent) Read(ctx context.Context, p core.Principal, a core.ActionRef, input json.RawMessage) (json.RawMessage, error) {
	if f.ReadFunc == nil {
		return nil, notConfigured("FakeAgent", "Read")
	}
	return f.ReadFunc(ctx, p, a, input)
}

// Resource calls ResourceFunc.
func (f *FakeAgent) Resource(ctx context.Context, p core.Principal, name string) (json.RawMessage, error) {
	if f.ResourceFunc == nil {
		return nil, notConfigured("FakeAgent", "Resource")
	}
	return f.ResourceFunc(ctx, p, name)
}

// Plan calls PlanFunc.
func (f *FakeAgent) Plan(ctx context.Context, p core.Principal, a core.ActionRef, input json.RawMessage, opts core.PlanOptions) (core.PlanView, error) {
	if f.PlanFunc == nil {
		return core.PlanView{}, notConfigured("FakeAgent", "Plan")
	}
	return f.PlanFunc(ctx, p, a, input, opts)
}

// Apply calls ApplyFunc.
func (f *FakeAgent) Apply(ctx context.Context, p core.Principal, planID string, opts core.ApplyOptions) (core.ChangeView, error) {
	if f.ApplyFunc == nil {
		return core.ChangeView{}, notConfigured("FakeAgent", "Apply")
	}
	return f.ApplyFunc(ctx, p, planID, opts)
}

// Confirm calls ConfirmFunc.
func (f *FakeAgent) Confirm(ctx context.Context, p core.Principal, changeID string) (core.ChangeView, error) {
	if f.ConfirmFunc == nil {
		return core.ChangeView{}, notConfigured("FakeAgent", "Confirm")
	}
	return f.ConfirmFunc(ctx, p, changeID)
}

// Revert calls RevertFunc.
func (f *FakeAgent) Revert(ctx context.Context, p core.Principal, changeID string) (core.ChangeView, error) {
	if f.RevertFunc == nil {
		return core.ChangeView{}, notConfigured("FakeAgent", "Revert")
	}
	return f.RevertFunc(ctx, p, changeID)
}

// Change calls ChangeFunc.
func (f *FakeAgent) Change(ctx context.Context, changeID string) (core.ChangeView, error) {
	if f.ChangeFunc == nil {
		return core.ChangeView{}, notConfigured("FakeAgent", "Change")
	}
	return f.ChangeFunc(ctx, changeID)
}

// Changes calls ChangesFunc.
func (f *FakeAgent) Changes(ctx context.Context, filter core.ChangeFilter) ([]core.ChangeView, error) {
	if f.ChangesFunc == nil {
		return nil, notConfigured("FakeAgent", "Changes")
	}
	return f.ChangesFunc(ctx, filter)
}

// Preflight calls PreflightFunc.
func (f *FakeAgent) Preflight(ctx context.Context, moduleID string) (core.ModuleStatus, error) {
	if f.PreflightFunc == nil {
		return core.ModuleStatus{}, notConfigured("FakeAgent", "Preflight")
	}
	return f.PreflightFunc(ctx, moduleID)
}

// Journal calls JournalFunc.
func (f *FakeAgent) Journal(ctx context.Context, afterSeq int64, limit int) ([]core.JournalEntry, error) {
	if f.JournalFunc == nil {
		return nil, notConfigured("FakeAgent", "Journal")
	}
	return f.JournalFunc(ctx, afterSeq, limit)
}

// Ack calls AckFunc.
func (f *FakeAgent) Ack(ctx context.Context, seq int64) error {
	if f.AckFunc == nil {
		return notConfigured("FakeAgent", "Ack")
	}
	return f.AckFunc(ctx, seq)
}

// Undo calls UndoFunc.
func (f *FakeAgent) Undo(ctx context.Context, p core.Principal, changeID string, call *core.CallContext) (core.ChangeView, error) {
	if f.UndoFunc == nil {
		return core.ChangeView{}, notConfigured("FakeAgent", "Undo")
	}
	return f.UndoFunc(ctx, p, changeID, call)
}

// Repair calls RepairFunc.
func (f *FakeAgent) Repair(ctx context.Context, p core.Principal, changeID string, req core.RepairRequest) (core.RepairResult, error) {
	if f.RepairFunc == nil {
		return core.RepairResult{}, notConfigured("FakeAgent", "Repair")
	}
	return f.RepairFunc(ctx, p, changeID, req)
}

// Release calls ReleaseFunc.
func (f *FakeAgent) Release(ctx context.Context, p core.Principal, changeID, reason string, call *core.CallContext) (core.ReleaseResult, error) {
	if f.ReleaseFunc == nil {
		return core.ReleaseResult{}, notConfigured("FakeAgent", "Release")
	}
	return f.ReleaseFunc(ctx, p, changeID, reason, call)
}

// Effects calls EffectsFunc.
func (f *FakeAgent) Effects(ctx context.Context, p core.Principal, changeID string) ([]core.EffectView, error) {
	if f.EffectsFunc == nil {
		return nil, notConfigured("FakeAgent", "Effects")
	}
	return f.EffectsFunc(ctx, p, changeID)
}

// Observe calls ObserveFunc.
func (f *FakeAgent) Observe(ctx context.Context, p core.Principal, changeID string, seq int) (core.ObserveResult, error) {
	if f.ObserveFunc == nil {
		return core.ObserveResult{}, notConfigured("FakeAgent", "Observe")
	}
	return f.ObserveFunc(ctx, p, changeID, seq)
}

// Resolve calls ResolveFunc.
func (f *FakeAgent) Resolve(ctx context.Context, p core.Principal, changeID string, seq int, req core.ResolveRequest) (core.EffectView, error) {
	if f.ResolveFunc == nil {
		return core.EffectView{}, notConfigured("FakeAgent", "Resolve")
	}
	return f.ResolveFunc(ctx, p, changeID, seq, req)
}

// Lookup calls LookupFunc.
func (f *FakeAgent) Lookup(ctx context.Context, planID string, private bool) (core.PlanLookup, error) {
	if f.LookupFunc == nil {
		return core.PlanLookup{}, notConfigured("FakeAgent", "Lookup")
	}
	return f.LookupFunc(ctx, planID, private)
}

// Retire calls RetireFunc.
func (f *FakeAgent) Retire(ctx context.Context, planID string) (core.PlanLookup, error) {
	if f.RetireFunc == nil {
		return core.PlanLookup{}, notConfigured("FakeAgent", "Retire")
	}
	return f.RetireFunc(ctx, planID)
}

// AckPrivate calls AckPrivateFunc.
func (f *FakeAgent) AckPrivate(ctx context.Context, changeID string, final bool) error {
	if f.AckPrivateFunc == nil {
		return notConfigured("FakeAgent", "AckPrivate")
	}
	return f.AckPrivateFunc(ctx, changeID, final)
}

// MapHosts is a core.HostDirectory over a map from host id to client.
type MapHosts map[string]core.AgentClient

// Agent returns the host's client, or not_found.
func (m MapHosts) Agent(_ context.Context, hostID string) (core.AgentClient, error) {
	if c, ok := m[hostID]; ok {
		return c, nil
	}
	return nil, errcode.New(errcode.NotFound, fmt.Sprintf("host %s not found", hostID))
}
