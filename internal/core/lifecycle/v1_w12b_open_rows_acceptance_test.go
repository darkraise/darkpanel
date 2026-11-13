package lifecycle

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"strings"
	"sync"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// a5Call is one call a composite made to a child on behalf of a step.
type a5Call struct {
	method, step string
	client       string // "" when the context carried no client peer
	exposure     string // "" when the context carried no proxied value
	proxiedFrom  string // the client address the proxied value names
}

// a5Kids records the requester the context of each step call carries, and
// refuses the plan of the steps refuse names while the context carries an
// exposure.
type a5Kids struct {
	Children
	mu     sync.Mutex
	calls  []a5Call
	refuse map[string]string // step id -> refusal detail
}

func (k *a5Kids) note(ctx context.Context, method string, call *core.CallContext) (a5Call, bool) {
	if call == nil || call.ParentChangeID == "" {
		return a5Call{}, false
	}
	c := a5Call{method: method, step: call.StepID}
	if p, ok := core.ClientPeer(ctx); ok {
		c.client = p.Addr.String()
	}
	if px, ok := core.ProxiedFrom(ctx); ok {
		c.exposure, c.proxiedFrom = px.Path.ExposureID, px.Client.String()
	}
	k.mu.Lock()
	k.calls = append(k.calls, c)
	k.mu.Unlock()
	return c, true
}

func (k *a5Kids) Plan(ctx context.Context, p core.Principal, hostID string, a core.ActionRef, input json.RawMessage, opts core.PlanOptions) (core.PlanView, error) {
	if c, ok := k.note(ctx, "Plan", opts.Call); ok && c.exposure != "" {
		if detail, refused := k.refuse[c.step]; refused {
			return core.PlanView{}, core.Definite(errcode.New(errcode.PlanGuardFailed, detail))
		}
	}
	return k.Children.Plan(ctx, p, hostID, a, input, opts)
}

func (k *a5Kids) Apply(ctx context.Context, p core.Principal, hostID string, a core.ActionRef, planID string, opts core.ApplyOptions) (core.ChangeView, error) {
	k.note(ctx, "Apply", opts.Call)
	return k.Children.Apply(ctx, p, hostID, a, planID, opts)
}

func (k *a5Kids) stepCalls() []a5Call {
	k.mu.Lock()
	defer k.mu.Unlock()
	return append([]a5Call(nil), k.calls...)
}

func a5Via(client, exposure string) context.Context {
	addr := netip.MustParseAddr(client)
	ctx := core.WithClientPeer(context.Background(), core.Peer{Addr: addr})
	return core.WithProxied(ctx, core.Proxied{Client: addr, Path: core.ProxyPath{ExposureID: exposure, Kind: "npm"}})
}

func a5Fixture(t *testing.T) (*cfxFixture, *a5Kids) {
	t.Helper()
	f := newCfxFixture(t)
	k := &a5Kids{Children: f.kids, refuse: map[string]string{}}
	f.ctrl.SetChildren(k)
	return f, k
}

func a5Apply(t *testing.T, f *cfxFixture, ctx context.Context, ref core.ActionRef) core.ChangeView {
	t.Helper()
	pl, err := f.planC(ref, `{"name":"web"}`)
	if err != nil {
		t.Fatal(err)
	}
	v, err := f.ctrl.Apply(ctx, cfxRoot, pl.PlanID, core.ApplyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func a5Want(t *testing.T, calls []a5Call, client, exposure string, steps ...string) {
	t.Helper()
	for _, step := range steps {
		for _, method := range []string{"Plan", "Apply"} {
			n := 0
			for _, c := range calls {
				if c.step != step || c.method != method {
					continue
				}
				n++
				if c.client != client || c.exposure != exposure {
					t.Errorf("step %s %s ran with client %q exposure %q, want client %q exposure %q", step, method, c.client, c.exposure, client, exposure)
				}
				if exposure != "" && c.proxiedFrom != client {
					t.Errorf("step %s %s ran with proxied client %q, want %q", step, method, c.proxiedFrom, client)
				}
			}
			if n == 0 {
				t.Errorf("step %s made no %s call", step, method)
			}
		}
	}
}

func TestA5_ACompositeAppliedThroughAnExposureGuardsEachStepWithTheRequester(t *testing.T) {
	f, k := a5Fixture(t)
	v := a5Apply(t, f, a5Via("198.51.100.7", "exp9"), refCfxAdd)
	if done := f.await(v.ID, core.StateCompleted, core.StateReverted, core.StateFailed, core.StateNeedsRepair); done.State != core.StateCompleted {
		t.Fatalf("composite = %s, want completed", done.State)
	}
	a5Want(t, k.stepCalls(), "198.51.100.7", "exp9", "a", "b", "c")
}

func TestA5_ARepairThatStartsARunCarriesTheRepairer(t *testing.T) {
	f, k := a5Fixture(t)
	f.host.setItem("web-a", "1")
	f.host.setItem("web-b", "2")
	f.host.failNext("web-b", 1)
	v := a5Apply(t, f, a5Via("198.51.100.7", "exp9"), refCfxRemove)
	if done := f.await(v.ID, core.StateNeedsRepair, core.StateCompleted, core.StateFailed); done.State != core.StateNeedsRepair {
		t.Fatalf("composite = %s, want needs_repair", done.State)
	}
	before := len(k.stepCalls())
	if _, err := f.ctrl.Repair(a5Via("203.0.113.9", "exp10"), cfxRoot, v.ID, core.RepairRequest{}); err != nil {
		t.Fatal(err)
	}
	if done := f.await(v.ID, core.StateCompleted, core.StateFailed, core.StateNeedsRepair); done.State != core.StateCompleted {
		t.Fatalf("composite after the repair = %s, want completed", done.State)
	}
	after := k.stepCalls()[before:]
	if len(after) == 0 {
		t.Fatal("the repair made no step call")
	}
	for _, c := range after {
		if c.client != "203.0.113.9" || c.exposure != "exp10" {
			t.Errorf("step %s %s after the repair ran with client %q exposure %q, want the repairer 203.0.113.9 through exp10", c.step, c.method, c.client, c.exposure)
		}
	}
}

func TestA5_ADirectPanelClientIsCarriedWithoutAnExposure(t *testing.T) {
	f, k := a5Fixture(t)
	ctx := core.WithClientPeer(context.Background(), core.Peer{Addr: netip.MustParseAddr("192.0.2.44")})
	v := a5Apply(t, f, ctx, refCfxAdd)
	if done := f.await(v.ID, core.StateCompleted, core.StateReverted, core.StateFailed, core.StateNeedsRepair); done.State != core.StateCompleted {
		t.Fatalf("composite = %s, want completed", done.State)
	}
	a5Want(t, k.stepCalls(), "192.0.2.44", "", "a", "b", "c")
}

func TestA5_AStepRefusedByItsGuardFailsTheCompositeNamingTheStepAndTheFlow(t *testing.T) {
	const cut = "the change would cut exposure exp9 client from 198.51.100.7 to 192.0.2.1:443 on eth0: drop"
	f, k := a5Fixture(t)
	k.refuse["b"] = cut
	v := a5Apply(t, f, a5Via("198.51.100.7", "exp9"), refCfxAdd)
	done := f.await(v.ID, core.StateReverted, core.StateFailed, core.StateNeedsRepair, core.StateCompleted)
	if done.State != core.StateReverted {
		t.Fatalf("a reverse composite whose step b was refused is %s, want reverted", done.State)
	}
	if done.Error == nil || !strings.Contains(done.Error.Message, cut) || !strings.Contains(done.Error.Message, "b") {
		t.Fatalf("composite error %+v, want it to name step b and %q", done.Error, cut)
	}
	if _, ok := f.host.item("web-a"); ok {
		t.Error("step a was not undone")
	}
	// The same step through a request that reaches no exposure is not refused.
	f2, _ := a5Fixture(t)
	v2 := a5Apply(t, f2, context.Background(), refCfxAdd)
	if done := f2.await(v2.ID, core.StateCompleted, core.StateReverted, core.StateFailed, core.StateNeedsRepair); done.State != core.StateCompleted {
		t.Errorf("composite without an exposure = %s, want completed", done.State)
	}
}

func a6Holds(t *testing.T, f *cfxFixture, table, s string) bool {
	t.Helper()
	rows, err := f.ctrlDB.Read().Query("SELECT * FROM " + table)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		for _, v := range vals {
			if strings.Contains(fmt.Sprintf("%s", v), s) {
				return true
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return false
}

func TestA6_TheCompositesRowsHoldNeitherTheClientNorTheExposurePath(t *testing.T) {
	f, _ := a5Fixture(t)
	v := a5Apply(t, f, a5Via("198.51.100.7", "exp9"), refCfxAdd)
	if done := f.await(v.ID, core.StateCompleted, core.StateReverted, core.StateFailed, core.StateNeedsRepair); done.State != core.StateCompleted {
		t.Fatalf("composite = %s, want completed", done.State)
	}
	// Close waits for the run goroutine, which may still be recording when the change turns completed.
	f.ctrl.Close()
	for _, table := range []string{"core_composites", "core_composite_steps"} {
		for _, s := range []string{"198.51.100.7", "exp9", "listener", "proxy_addr", "ProxyAddr"} {
			if a6Holds(t, f, table, s) {
				t.Errorf("%s holds %q", table, s)
			}
		}
	}
}

func TestA6_ACompositeResumedAfterARestartStartsNoStep(t *testing.T) {
	f := newCfxFixture(t)
	cut := cutAt(f, "b", core.StepPlanning, false)
	a5Apply(t, f, a5Via("198.51.100.7", "exp9"), refCfxAdd)
	restartAfter(t, f, cut)
	plans := f.agentPlans()
	calls := len(f.kids.callLog())
	var id string
	if err := f.ctrlDB.Read().QueryRow("SELECT change_id FROM core_composites LIMIT 1").Scan(&id); err != nil {
		t.Fatal(err)
	}
	done := f.await(id, core.StateReverted, core.StateFailed, core.StateNeedsRepair, core.StateCompleted)
	if done.State == core.StateCompleted {
		t.Errorf("composite resumed after a restart = %s, want it not to run its remaining steps", done.State)
	}
	if n := f.agentPlans(); n != plans {
		t.Errorf("recovery planned %d steps, want none", n-plans)
	}
	for _, c := range f.kids.callLog()[calls:] {
		if strings.HasPrefix(c, "Plan ") {
			t.Errorf("recovery called %q, want no step planned", c)
		}
	}
	steps := f.stepsOf(id)
	if s := steps["b"]; s.PlanID != "" && s.State == core.StepApplied {
		t.Errorf("step b = %s after the restart, want it never applied", s.State)
	}
	for _, table := range []string{"core_composites", "core_composite_steps"} {
		for _, s := range []string{"198.51.100.7", "exp9"} {
			if a6Holds(t, f, table, s) {
				t.Errorf("%s holds %q after the restart", table, s)
			}
		}
	}
}
