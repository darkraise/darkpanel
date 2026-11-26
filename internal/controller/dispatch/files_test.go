package dispatch

import (
	"context"
	"net/url"
	"reflect"
	"slices"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// dfConsole stands in for the console module.
type dfConsole struct{}

func (dfConsole) Manifest() core.Manifest {
	return core.Manifest{ID: FilesModule, Version: 1, DefaultEnabled: true}
}

// dfAgent is an agent client that sends file requests.
type dfAgent struct {
	coretest.FakeAgent
	got []core.FileRequest
	as  []core.Principal
}

func (a *dfAgent) File(_ context.Context, p core.Principal, req core.FileRequest) (core.FileAnswer, error) {
	a.got, a.as = append(a.got, req), append(a.as, p)
	return core.FileAnswer{Status: 204}, nil
}

func dfDispatcher(t *testing.T, client func(h *harness) core.AgentClient) (*harness, *Dispatcher) {
	t.Helper()
	h := newHarnessWith(t, dfConsole{})
	h.clients.Add(testHost, client(h))
	d, err := New(h.config(&coretest.MemAudit{}))
	if err != nil {
		t.Fatal(err)
	}
	return h, d
}

func TestFile(t *testing.T) {
	ctx := context.Background()
	var a *dfAgent
	h, d := dfDispatcher(t, func(h *harness) core.AgentClient {
		a = &dfAgent{}
		a.HelloFunc = h.rt.Hello
		return a
	})
	req := core.FileRequest{Op: "list", Query: url.Values{"path": {"/etc"}}}
	ans, err := d.File(ctx, alice, testHost, req)
	if err != nil || ans.Status != 204 {
		t.Fatalf("File: %+v, %v", ans, err)
	}
	if !reflect.DeepEqual(a.got, []core.FileRequest{req}) || !reflect.DeepEqual(a.as, []core.Principal{alice}) {
		t.Errorf("the agent got %+v as %+v", a.got, a.as)
	}

	if _, err := h.db.Writer().Exec("UPDATE core_module_state SET enabled = 0 WHERE module_id = ?", FilesModule); err != nil {
		t.Fatal(err)
	}
	_, err = d.File(ctx, alice, testHost, req)
	_ = wantCode(t, err, errcode.ModuleUnavailable)
	_ = wantCode(t, d.ModuleUsable(ctx, testHost, FilesModule), errcode.ModuleUnavailable)
	if len(a.got) != 1 {
		t.Errorf("a refused file request reached the agent: %+v", a.got)
	}
}

func TestFileOldAgent(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		client func(h *harness) core.AgentClient
	}{
		{"hello without the console", func(h *harness) core.AgentClient {
			a := &dfAgent{}
			a.HelloFunc = func(ctx context.Context) (core.AgentHello, error) {
				hello, err := h.rt.Hello(ctx)
				hello.Modules = slices.DeleteFunc(hello.Modules, func(m core.ModuleStatus) bool { return m.ID == FilesModule })
				return hello, err
			}
			return a
		}},
		{"a client without file requests", func(h *harness) core.AgentClient {
			a := &coretest.FakeAgent{}
			a.HelloFunc = h.rt.Hello
			return a
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, d := dfDispatcher(t, tc.client)
			_, err := d.File(ctx, alice, testHost, core.FileRequest{Op: "stat"})
			if e := wantCode(t, err, errcode.ModuleUnavailable); e.Detail != ReasonFilesUnserved {
				t.Errorf("detail %q, want %q", e.Detail, ReasonFilesUnserved)
			}
		})
	}
}
