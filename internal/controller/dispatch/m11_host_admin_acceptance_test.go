package dispatch

import (
	"context"
	"slices"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/console"
)

func TestA9_AnAgentWithoutTheConsoleModuleAnswersModuleUnavailable(t *testing.T) {
	h := newHarnessWith(t, console.Module())
	a := &dchAgent{}
	a.HelloFunc = func(ctx context.Context) (core.AgentHello, error) {
		hello, err := h.rt.Hello(ctx)
		hello.Modules = slices.DeleteFunc(hello.Modules, func(m core.ModuleStatus) bool { return m.ID == "console" })
		return hello, err
	}
	h.clients.Add(testHost, a)
	d, err := New(h.config(&coretest.MemAudit{}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = d.OpenChannel(context.Background(), alice, testHost, "console", "terminal", nil)
	e := errcode.From(err)
	if e.Code != errcode.ModuleUnavailable || e.Detail != "Update the agent to use the terminal" {
		t.Errorf("OpenChannel = %v (%s, %q); want module.unavailable with %q", err, e.Code, e.Detail, "Update the agent to use the terminal")
	}
	if len(a.opens) != 0 {
		t.Errorf("the request reached the agent: %+v", a.opens)
	}
}
