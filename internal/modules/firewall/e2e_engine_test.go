package firewall

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/modules/firewall/loaded"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

var eeUser = core.Principal{Actor: "user:u1", UserID: "u1", Scope: core.ScopeOperate, Source: "203.0.113.7"}

// eeEngine is the agent's lifecycle engine over the host, serving the
// firewall module.
func eeEngine(t *testing.T, h *e2eHost) *lifecycle.Engine {
	t.Helper()
	reg, err := core.NewRegistry(module{f: h.fw()})
	if err != nil {
		t.Fatal(err)
	}
	eng, err := lifecycle.New(lifecycle.Config{
		Side: core.SideAgent, Registry: reg, Validator: schema.NewValidator(), Journal: &coretest.MemJournal{}, Redact: schema.Redact,
		Base: core.Env{HostID: "h1", DB: h.db, Exec: h, Files: h, Sealer: coretest.Sealer{}, Clock: h.clock,
			Log: slog.New(slog.NewTextHandler(io.Discard, nil))},
		ModuleUnits: func(string) core.Units { return e2eUnits{} },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(eng.Close)
	return eng
}

// eePlanApply plans and applies action name with input raw.
func eePlanApply(t *testing.T, eng *lifecycle.Engine, name, raw string) (core.PlanView, core.ChangeView, error) {
	t.Helper()
	ctx := context.Background()
	pv, err := eng.Plan(ctx, eeUser, core.ActionRef{Name: name, Version: 1}, json.RawMessage(raw), core.PlanOptions{})
	if err != nil {
		t.Fatalf("plan %s: %v", name, err)
	}
	cv, err := eng.Apply(ctx, eeUser, pv.PlanID, core.ApplyOptions{})
	return pv, cv, err
}

// eeUserChain returns the keys of the kernel's IPv4 ufw-user-input chain.
func eeUserChain(h *e2eHost) []string {
	var keys []string
	if c := chainOf(h.loaded.V4, "filter", "ufw-user-input"); c != nil {
		for _, r := range c.Rules {
			keys = append(keys, netfilter.Key(r, false))
		}
	}
	return keys
}

// TestE2EApplyVerifyRevert: through the engine, a rule is added as
// journaled effects with UFW reloaded, verify finds the kernel as the
// prediction said, and the revert restores the snapshot whole: UFW's files
// and the kernel's tables are as before.
func TestE2EApplyVerifyRevert(t *testing.T) {
	ctx := context.Background()
	h := newE2EHost(t, "rules", "rules")
	eng := eeEngine(t, h)
	before, beforeLoaded := h.config(), h.loaded

	pv, cv, err := eePlanApply(t, eng, "firewall.rule.add",
		`{"rule":{"action":"allow","direction":"in","proto":"tcp","from":"any","to":"any","toPort":"8080","family":"both","comment":"web"}}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(pv.Diff) != 1 || !strings.HasPrefix(pv.Diff[0].Text, "rule new: allow in proto tcp from any to any port 8080") || !pv.Guards[0].OK {
		t.Errorf("plan %+v, want the new rule and a passing guard", pv)
	}
	if cv.State != core.StatePending || cv.Error != nil {
		t.Fatalf("change %+v, want pending after verify", cv)
	}
	want := []string{"allow in proto tcp from any to any port 8080 comment darkpanel:" + cv.ID + " web", "reload"}
	if !reflect.DeepEqual(h.ran, want) {
		t.Errorf("ufw ran %q, want %q", h.ran, want)
	}
	var out ChangeOutput
	if err := json.Unmarshal(cv.Output, &out); err != nil || !reflect.DeepEqual(out.Rules, []string{cv.ID}) {
		t.Errorf("output %s (%v), want the rule tagged with the change id", cv.Output, err)
	}
	if keys := eeUserChain(h); !strings.Contains(strings.Join(keys, "\n"), "--dport 8080") {
		t.Errorf("the kernel's user chain %q lacks the new rule", keys)
	}

	cv, err = eng.Revert(ctx, eeUser, cv.ID)
	if err != nil || cv.State != core.StateReverted {
		t.Fatalf("revert %+v (%v), want reverted", cv, err)
	}
	after := h.config()
	for _, p := range []string{"/etc/ufw/user.rules", "/etc/ufw/user6.rules", "/etc/ufw/ufw.conf"} {
		if !bytes.Equal(after.Files[p].Data, before.Files[p].Data) {
			t.Errorf("%s differs from before the change", p)
		}
	}
	if diffs := loaded.Compare(beforeLoaded, h.loaded, restoreKeep); len(diffs) != 0 {
		t.Errorf("the kernel's tables differ from before the change: %+v", diffs)
	}
	if h.ran[len(h.ran)-1] != "reload" {
		t.Errorf("ufw ran %q, want the restore's reload last", h.ran)
	}
}

// TestE2EApplyGuard: through the engine, a change that would cut the SSH
// session is planned with the failing guard and refused at apply; planned
// with the guard overridden it applies, and confirming keeps it.
func TestE2EApplyGuard(t *testing.T) {
	ctx := context.Background()
	h := newE2EHost(t, "rules", "rules")
	eng := eeEngine(t, h)
	pv, _, err := eePlanApply(t, eng, "firewall.rule.delete", `{"id":"`+e2eSSHRule+`"}`)
	if e := errcode.From(err); e.Code != errcode.PlanGuardFailed || !strings.Contains(e.Detail, "ssh from 203.0.113.7") {
		t.Errorf("apply = %v, want the guard's refusal naming the session", err)
	}
	if pv.Guards[0].OK || len(h.ran) != 0 {
		t.Errorf("plan guards %+v, ufw ran %q; want a failing guard and nothing run", pv.Guards, h.ran)
	}

	pv, cv, err := eePlanApply(t, eng, "firewall.rule.delete", `{"id":"`+e2eSSHRule+`","overrides":["firewall.lockout"]}`)
	if err != nil || cv.State != core.StatePending {
		t.Fatalf("apply overridden = %+v, %v; want pending", cv, err)
	}
	if !pv.Guards[0].Overridden || !strings.Contains(pv.Diff[len(pv.Diff)-1].Text, "guard firewall.lockout overridden") {
		t.Errorf("plan %+v, want the override recorded in the diff", pv)
	}
	if cv, err = eng.Confirm(ctx, eeUser, cv.ID); err != nil || cv.State != core.StateConfirmed {
		t.Fatalf("confirm = %+v, %v", cv, err)
	}
	if len(findRules(h.config(), e2eSSHRule)) != 0 || strings.Contains(strings.Join(eeUserChain(h), "\n"), "--dport 22 ") {
		t.Error("the SSH rule is still there")
	}
	list, err := listSnapshots(ctx, h.db, snapshotFilter{})
	if err != nil || len(list) != 1 || !list[0].Baseline || list[0].ChangeID != cv.ID {
		t.Errorf("snapshots %+v (%v), want one, the confirmed change's, the host's baseline (the refused apply took none)", list, err)
	}
}

// TestE2ETimerReverts: a change nobody confirms reverts on the agent when
// its confirm timeout (the apply request's, owner ruling R2) passes.
func TestE2ETimerReverts(t *testing.T) {
	ctx := context.Background()
	h := newE2EHost(t, "rules", "rules")
	eng := eeEngine(t, h)
	before := h.config()
	pv, err := eng.Plan(ctx, eeUser, core.ActionRef{Name: "firewall.logging.set", Version: 1}, json.RawMessage(`{"level":"high"}`), core.PlanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	cv, err := eng.Apply(ctx, eeUser, pv.PlanID, core.ApplyOptions{ConfirmTimeout: 10 * time.Second})
	if err != nil || cv.State != core.StatePending || cv.RevertDeadline == nil || !cv.RevertDeadline.Equal(h.clock.Now().Add(10*time.Second)) {
		t.Fatalf("apply = %+v, %v; want pending with a 10 s deadline", cv, err)
	}
	if h.config().LogLevel != "high" {
		t.Fatalf("logging %s after apply, want high", h.config().LogLevel)
	}
	h.clock.Advance(11 * time.Second)
	if cv, err = eng.Change(ctx, cv.ID); err != nil || cv.State != core.StateReverted {
		t.Fatalf("change after the deadline = %+v, %v; want reverted", cv, err)
	}
	if got := h.config(); got.LogLevel != before.LogLevel || !bytes.Equal(got.Files["/etc/ufw/user.rules"].Data, before.Files["/etc/ufw/user.rules"].Data) {
		t.Errorf("logging %s after the timer, want %s and the files as before", got.LogLevel, before.LogLevel)
	}
}
