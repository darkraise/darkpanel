package coretest

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

func TestFakeAgent(t *testing.T) {
	ctx := context.Background()
	agent := &FakeAgent{
		PreflightFunc: func(_ context.Context, id string) (core.ModuleStatus, error) {
			return core.ModuleStatus{ID: id, Available: true}, nil
		},
	}
	if st, err := agent.Preflight(ctx, "hostinfo"); err != nil || st.ID != "hostinfo" || !st.Available {
		t.Errorf("Preflight = %+v, %v", st, err)
	}
	if _, err := agent.Hello(ctx); err == nil || !strings.Contains(err.Error(), "Hello is not configured") {
		t.Errorf("unconfigured Hello error = %v", err)
	}
}

func TestFakeAgentChangeOps(t *testing.T) {
	ctx := context.Background()
	agent := &FakeAgent{
		UndoFunc: func(_ context.Context, _ core.Principal, id string, call *core.CallContext) (core.ChangeView, error) {
			return core.ChangeView{ID: id, State: core.StateUndoing, ParentChangeID: call.ParentChangeID}, nil
		},
	}
	var client core.AgentClient = agent
	v, err := client.Undo(ctx, core.Principal{}, "01C", &core.CallContext{ParentChangeID: "01P"})
	if err != nil || v.ID != "01C" || v.State != core.StateUndoing || v.ParentChangeID != "01P" {
		t.Errorf("Undo = %+v, %v", v, err)
	}
	if _, err := client.Lookup(ctx, "01PLAN", true); err == nil || !strings.Contains(err.Error(), "Lookup is not configured") {
		t.Errorf("unconfigured Lookup error = %v", err)
	}
	if err := client.AckPrivate(ctx, "01C", false); err == nil || !strings.Contains(err.Error(), "AckPrivate is not configured") {
		t.Errorf("unconfigured AckPrivate error = %v", err)
	}
}

func TestFakeDispatcher(t *testing.T) {
	ctx := context.Background()
	d := &FakeDispatcher{
		ChangeFunc: func(_ context.Context, _ core.Principal, id string) (core.ChangeView, error) {
			return core.ChangeView{ID: id}, nil
		},
	}
	if c, err := d.Change(ctx, core.Principal{}, "01C"); err != nil || c.ID != "01C" {
		t.Errorf("Change = %+v, %v", c, err)
	}
	if _, err := d.Apply(ctx, core.Principal{}, "", core.ActionRef{}, "", core.ApplyOptions{}); err == nil || !strings.Contains(err.Error(), "Apply is not configured") {
		t.Errorf("unconfigured Apply error = %v", err)
	}
}

func TestMapHosts(t *testing.T) {
	agent := &FakeAgent{}
	hosts := MapHosts{"H1": agent}
	var dir core.HostDirectory = hosts
	got, err := dir.Agent(context.Background(), "H1")
	if err != nil || got != agent {
		t.Errorf("Agent(H1) = %v, %v", got, err)
	}
	_, err = dir.Agent(context.Background(), "H2")
	var coded *errcode.Error
	if !errors.As(err, &coded) || coded.Code != errcode.NotFound {
		t.Errorf("Agent(H2) error = %v, want %s", err, errcode.NotFound)
	}
}
