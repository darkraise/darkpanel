package update

import (
	"context"
	"encoding/json"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
	"github.com/darkraise/darkpanel/internal/core/schema"
)

// actionPrincipal is the rollout driver, the principal that applies update
// actions on a host.
var actionPrincipal = core.Principal{Actor: core.ActorRollout, Scope: core.ScopeAdmin}

// actionModule is a core module holding only the given actions.
type actionModule struct{ defs []core.ActionDef }

func (m actionModule) Manifest() core.Manifest {
	return core.Manifest{ID: core.ModuleIDCore, Version: 1, Actions: m.defs, DefaultEnabled: true}
}

// newActionEngine returns the agent engine of host h1 over e's agent
// database, serving the update actions of u.
func newActionEngine(t *testing.T, e *updaterEnv, u *Updater) *lifecycle.Engine {
	t.Helper()
	reg, err := core.NewRegistry(actionModule{Actions(u)})
	if err != nil {
		t.Fatal(err)
	}
	eng, err := lifecycle.New(lifecycle.Config{
		Side: core.SideAgent, Registry: reg, Validator: schema.NewValidator(), Journal: &coretest.MemJournal{},
		Redact: schema.Redact,
		Base: core.Env{HostID: "h1", DB: e.db, Sealer: coretest.Sealer{}, Clock: e.clock,
			Log: slog.New(slog.DiscardHandler)},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(eng.Close)
	return eng
}

// actionDef returns the action of defs named name.
func actionDef(t *testing.T, defs []core.ActionDef, name string) core.ActionDef {
	t.Helper()
	i := slices.IndexFunc(defs, func(d core.ActionDef) bool { return d.Spec.Name == name })
	if i < 0 {
		t.Fatalf("no action %s", name)
	}
	return defs[i]
}

// wantInternal fails unless err is one the API reports as internal: an
// error without a code.
func wantInternal(t *testing.T, err error) {
	t.Helper()
	if err == nil || errcode.From(err).Code != errcode.Internal {
		t.Fatalf("error = %v, want an internal error", err)
	}
}

// diffText joins a plan's diff lines.
func diffText(diff []core.DiffLine) string {
	var b strings.Builder
	for _, l := range diff {
		b.WriteString(l.Op + " " + l.Text + "\n")
	}
	return b.String()
}

func TestActionsRegisterWithoutAnUpdater(t *testing.T) {
	defs := Actions(nil)
	reg, err := core.NewRegistry(actionModule{defs})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]struct {
		class         core.Class
		compensatedBy string
	}{
		ActionApply:    {core.ClassCompensable, ActionRollback},
		ActionRollback: {core.ClassIrreversible, ""},
		ActionStatus:   {core.ClassRead, ""},
	}
	if len(reg.Actions()) != len(want) {
		t.Fatalf("registered %d actions, want %d", len(reg.Actions()), len(want))
	}
	for _, def := range reg.Actions() {
		w, ok := want[def.Spec.Name]
		switch {
		case !ok:
			t.Errorf("unexpected action %s", def.Ref())
		case def.Spec.Version != 1 || def.Spec.Side != core.SideAgent || def.Spec.Class != w.class ||
			def.Spec.CompensatedBy != w.compensatedBy || len(def.Spec.LockKeys) != 0 || def.Spec.BreakGlass:
			t.Errorf("%s spec = %+v", def.Ref(), def.Spec)
		}
		if _, err := schema.ActionSnapshot(def); err != nil {
			t.Errorf("%s snapshot: %v", def.Ref(), err)
		}
	}

	ctx := context.Background()
	apply := actionDef(t, defs, ActionApply)
	_, err = apply.Handler.Plan(ctx, core.Env{}, json.RawMessage(`{"version":"1.3.0","channel":"stable","force":false}`))
	wantCode(t, err, errcode.Unavailable)
	_, err = apply.Handler.Apply(ctx, core.Env{}, json.RawMessage(`{"version":"1.3.0","channel":"stable","force":false}`))
	wantCode(t, err, errcode.Unavailable)
	err = apply.Handler.Verify(ctx, core.Env{}, json.RawMessage(`{"version":"1.3.0","channel":"stable","force":false}`),
		json.RawMessage(`{}`))
	wantCode(t, err, errcode.Unavailable)
	rollback := actionDef(t, defs, ActionRollback)
	_, err = rollback.Handler.Plan(ctx, core.Env{}, json.RawMessage(`{"force":false}`))
	wantCode(t, err, errcode.Unavailable)
	_, err = actionDef(t, defs, ActionStatus).Handler.Read(ctx, core.Env{}, json.RawMessage(`{}`))
	wantCode(t, err, errcode.Unavailable)
}

func TestApplyAction(t *testing.T) {
	ctx := context.Background()
	e := newUpdaterEnv(t, "1.2.0")
	eng := newActionEngine(t, e, e.updater(t, "1.2.0"))
	plan, err := eng.Plan(ctx, actionPrincipal, core.ActionRef{Name: ActionApply, Version: 1},
		json.RawMessage(`{"version":"1.4.0","channel":"stable","force":false}`))
	if err != nil {
		t.Fatal(err)
	}
	diff := diffText(plan.Diff)
	for _, want := range []string{"- darkpanel 1.2.0", "+ darkpanel 1.4.0 (stable)", "installs 1.3.1, then 1.4.0"} {
		if !strings.Contains(diff, want) {
			t.Errorf("diff = %q, want it to contain %q", diff, want)
		}
	}
	v, err := eng.Apply(ctx, actionPrincipal, plan.PlanID, core.ApplyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if v.State != core.StateCompleted {
		t.Fatalf("change = %+v, want completed", v)
	}
	var tr core.UpdateTransaction
	if err := json.Unmarshal(v.Output, &tr); err != nil {
		t.Fatal(err)
	}
	if tr.State != core.UpdatePending || tr.FromVersion != "1.2.0" || tr.ToVersion != "1.3.1" ||
		tr.TargetVersion != "1.4.0" || tr.OperationID != tr.ID {
		t.Errorf("transaction = %+v", tr)
	}
	if r := mustRecord(t, e.p, tr.ID); r.ChangeID != v.ID {
		t.Errorf("record change id = %q, want the change %q", r.ChangeID, v.ID)
	}
	if units := e.sd.unitsOf("StartTransientService"); !slices.Equal(units, []string{RunnerUnit(tr.ID)}) {
		t.Errorf("transient units = %v", units)
	}
	if got := lockRows(t, e.db); !slices.Equal(got, []string{"* " + lifecycle.ReservationOwner(tr.ID)}) {
		t.Errorf("core_locks = %v", got)
	}
	again, err := eng.Apply(ctx, actionPrincipal, plan.PlanID, core.ApplyOptions{})
	if err != nil || again.ID != v.ID {
		t.Errorf("repeated apply = %+v, %v; want the same change", again, err)
	}
	if n := len(e.sd.unitsOf("StartTransientService")); n != 1 {
		t.Errorf("a repeated apply started %d runner units, want 1", n)
	}
}

func TestApplyActionRefusals(t *testing.T) {
	ctx := context.Background()
	e := newUpdaterEnv(t, "1.2.0")
	eng := newActionEngine(t, e, e.updater(t, "1.2.0"))
	ref := core.ActionRef{Name: ActionApply, Version: 1}
	tests := []struct {
		input string
		code  errcode.Code
	}{
		{`{"version":"1.1.0","channel":"stable","force":false}`, errcode.UpdateDowngradeRefused},
		{`{"version":"1.9.0","channel":"stable","force":false}`, errcode.UpdateNotFound},
		{`{"version":"1.6.0-beta.1","channel":"stable","force":false}`, errcode.UpdateNotFound},
		{`{"version":"1.2.0","channel":"stable","force":false}`, errcode.InvalidInput},
		{`{"version":"v1.3.0","channel":"stable","force":false}`, errcode.InvalidInput},
	}
	for _, tt := range tests {
		_, err := eng.Plan(ctx, actionPrincipal, ref, json.RawMessage(tt.input))
		wantCode(t, err, tt.code)
	}
	if n := len(e.sd.unitsOf("StartTransientService")); n != 0 {
		t.Errorf("refused plans started %d units", n)
	}
	wantNoLocks(t, e.db)
}

func TestApplyActionVerify(t *testing.T) {
	ctx := context.Background()
	e := newUpdaterEnv(t, "1.2.0")
	pending := testRecord("01A", "01A", 0)
	pending.ChangeID = "C1"
	failed := testRecord("01B", "01B", time.Second)
	failed.ChangeID, failed.State = "C2", core.UpdateFailed
	failed.Error = &core.ErrorDetail{Code: string(errcode.UpdateSignatureInvalid), Message: "no trusted signature"}
	for _, r := range []Record{pending, failed} {
		if err := WriteRecord(e.p, r); err != nil {
			t.Fatal(err)
		}
	}
	verify := actionDef(t, Actions(e.updater(t, "1.2.0")), ActionApply).Handler.Verify
	in := json.RawMessage(`{"version":"1.3.0","channel":"stable","force":false}`)
	tests := []struct {
		name, changeID, out string
		code                errcode.Code // "" for success, errcode.Internal for an uncoded error
	}{
		{"pending record", "C1", `{"id":"01A"}`, ""},
		{"failed record", "C2", `{"id":"01B"}`, errcode.UpdateSignatureInvalid},
		{"missing record", "C1", `{"id":"01MISSING"}`, errcode.NotFound},
		{"recovery finds the record by change", "C1", `{}`, ""},
		{"recovery finds the failed record", "C2", `{}`, errcode.UpdateSignatureInvalid},
		{"recovery without a record", "C3", `{}`, errcode.Internal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := verify(ctx, core.Env{ChangeID: tt.changeID}, in, json.RawMessage(tt.out))
			if tt.code == "" {
				if err != nil {
					t.Fatalf("Verify = %v, want nil", err)
				}
				return
			}
			if tt.code == errcode.Internal {
				wantInternal(t, err)
				return
			}
			wantCode(t, err, tt.code)
		})
	}
}

func TestRollbackAction(t *testing.T) {
	ctx := context.Background()
	e := newUpdaterEnv(t, "1.2.0")
	committedUpdate(t, e, "01UP", "1.2.0", "1.3.0")
	eng := newActionEngine(t, e, e.updater(t, "1.3.0"))
	plan, err := eng.Plan(ctx, actionPrincipal, core.ActionRef{Name: ActionRollback, Version: 1},
		json.RawMessage(`{"force":false}`))
	if err != nil {
		t.Fatal(err)
	}
	if plan.Confirmation != "1.2.0" {
		t.Errorf("confirmation = %q, want the version rolled back to", plan.Confirmation)
	}
	if diff := diffText(plan.Diff); !strings.Contains(diff, "- darkpanel 1.3.0") || !strings.Contains(diff, "+ darkpanel 1.2.0") {
		t.Errorf("diff = %q", diff)
	}
	_, err = eng.Apply(ctx, actionPrincipal, plan.PlanID, core.ApplyOptions{})
	wantCode(t, err, errcode.PlanConfirmationNeeded)
	v, err := eng.Apply(ctx, actionPrincipal, plan.PlanID, core.ApplyOptions{Confirmation: "1.2.0"})
	if err != nil {
		t.Fatal(err)
	}
	if v.State != core.StateCompleted {
		t.Fatalf("change = %+v, want completed", v)
	}
	var tr core.UpdateTransaction
	if err := json.Unmarshal(v.Output, &tr); err != nil {
		t.Fatal(err)
	}
	if tr.Kind != core.UpdateKindRollback || tr.State != core.UpdatePending || tr.ToVersion != "1.2.0" {
		t.Errorf("transaction = %+v", tr)
	}
	if r := mustRecord(t, e.p, tr.ID); r.ChangeID != v.ID {
		t.Errorf("record change id = %q, want the change %q", r.ChangeID, v.ID)
	}
	if units := e.sd.unitsOf("StartTransientService"); !slices.Equal(units, []string{RollbackRunnerUnit(tr.ID)}) {
		t.Errorf("transient units = %v", units)
	}
}

func TestRollbackActionRefusesWithoutAnUpdate(t *testing.T) {
	e := newUpdaterEnv(t, "1.2.0")
	eng := newActionEngine(t, e, e.updater(t, "1.2.0"))
	_, err := eng.Plan(context.Background(), actionPrincipal, core.ActionRef{Name: ActionRollback, Version: 1},
		json.RawMessage(`{"force":false}`))
	wantCode(t, err, errcode.UpdateNothingToRollBack)
}

func TestStatusAction(t *testing.T) {
	ctx := context.Background()
	e := newUpdaterEnv(t, "1.2.0")
	committedUpdate(t, e, "01UP", "1.2.0", "1.3.0")
	eng := newActionEngine(t, e, e.updater(t, "1.3.0"))
	ref := core.ActionRef{Name: ActionStatus, Version: 1}
	out, err := eng.Read(ctx, actionPrincipal, ref, json.RawMessage(`{"operationId":"01UP"}`))
	if err != nil {
		t.Fatal(err)
	}
	var st core.UpdateStatus
	if err := json.Unmarshal(out, &st); err != nil {
		t.Fatal(err)
	}
	if len(st.Transactions) != 1 || st.Transactions[0].ID != "01UP" || st.Transactions[0].State != core.UpdateCommitted {
		t.Errorf("status = %+v", st)
	}
	_, err = eng.Read(ctx, actionPrincipal, ref, json.RawMessage(`{"operationId":"01UP","changeId":"C1"}`))
	wantCode(t, err, errcode.InvalidInput)
}
