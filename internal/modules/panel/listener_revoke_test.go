package panel

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// lsEmptyInput is the input of the revoke and status actions.
var lsEmptyInput = json.RawMessage(`{}`)

// TestLsRevokePlan: a removal that finds no exposure is refused; an exposure that is not yet revoked, or whose
// listener is still open, shows the change; a revoked exposure behind a listener that is no longer open plans
// nothing, which is how a removal runs after the break-glass revoke.
func TestLsRevokePlan(t *testing.T) {
	ctx := context.Background()
	def := listenerRevokeAction()

	none := lsNewRig(t)
	_, err := def.Handler.Plan(ctx, none.env(core.CallContext{}, ""), lsEmptyInput)
	lsWantRefusal(t, "plan without an exposure", err, errcode.PanelNotExposed)

	tests := []struct {
		name     string
		row      string
		listener string
		empty    bool
	}{
		{"an open exposure behind an open listener", StateOpen, core.ListenerOpen, false},
		{"a revoked exposure behind a listener that is still open", StateRevoked, core.ListenerOpen, false},
		{"a revoked exposure behind a revoked listener", StateRevoked, core.ListenerRevoked, true},
		{"a revoked exposure behind a closed listener", StateRevoked, core.ListenerClosed, true},
		{"an exposure in needs_repair behind a closed listener", StateNeedsRepair, core.ListenerClosed, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := lsNewRig(t)
			row := r.expose(tc.row, "", tc.listener)
			plan, err := def.Handler.Plan(ctx, r.env(core.CallContext{}, ""), lsEmptyInput)
			if err != nil {
				t.Fatalf("plan: %v", err)
			}
			if tc.empty != (len(plan.Diff) == 0) {
				t.Errorf("diff %q, want empty: %t", lsDiff(plan.Diff), tc.empty)
			}
			if plan.StateHash == "" {
				t.Error("the plan has no state hash")
			}
			if got := r.l.Rows[row.ListenerID].State; got != tc.listener {
				t.Errorf("the plan changed the listener to %q", got)
			}
		})
	}
}

// TestLsRevokeApply: the apply revokes and closes the listener, then marks the exposure revoked with the removal
// that called it, when there is one. It is the same apply for the break-glass command (no parent) and for the
// removal, runs again to the same end, and answers whether this run revoked an open listener.
func TestLsRevokeApply(t *testing.T) {
	ctx := context.Background()
	def := listenerRevokeAction()
	tests := []struct {
		name        string
		row         string
		removal     string // the removal change the stored row names
		listener    string
		call        core.CallContext
		wantRevoked bool
		wantRemoval string
	}{
		{"the removal", StateOpen, "", core.ListenerOpen, lsRmCall, true, lsRemove},
		{"the break-glass command", StateOpen, "", core.ListenerOpen, core.CallContext{}, true, ""},
		{"the break-glass command again", StateRevoked, "", core.ListenerClosed, core.CallContext{}, false, ""},
		{"the removal after the break-glass command", StateRevoked, "", core.ListenerClosed, lsRmCall, false, lsRemove},
		{"a removal that replaces an earlier one", StateRevoked, lsOther, core.ListenerClosed, lsRmCall, false, lsRemove},
		{"the break-glass command after a removal", StateRevoked, lsOther, core.ListenerClosed, core.CallContext{}, false, lsOther},
		{"an exposure that needs repair", StateNeedsRepair, "", core.ListenerClosed, lsRmCall, false, lsRemove},
		{"a revoke that was cut off before the close", StateOpen, "", core.ListenerRevoked, lsRmCall, false, lsRemove},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := lsNewRig(t)
			row := r.expose(tc.row, tc.removal, tc.listener)
			env := r.env(tc.call, "")
			raw, err := def.Handler.Apply(ctx, env, lsEmptyInput)
			if err != nil {
				t.Fatalf("apply: %v", err)
			}
			want := ListenerRevokeOutput{ExposureID: lsAdd, ListenerID: row.ListenerID, RuleID: RuleID(lsAdd), Revoked: tc.wantRevoked}
			if got := lsDecode[ListenerRevokeOutput](t, raw); got != want {
				t.Errorf("output %+v, want %+v", got, want)
			}
			if got := r.l.Rows[row.ListenerID].State; got != core.ListenerClosed {
				t.Errorf("listener %s is %q, want closed", row.ListenerID, got)
			}
			live, ok := r.live()
			if !ok || live.State != StateRevoked || live.RemovalChangeID != tc.wantRemoval || !live.UpdatedAt.Equal(fdNow) {
				t.Errorf("exposure %+v (found %t), want revoked with removal %q", live, ok, tc.wantRemoval)
			}
			if err := def.Handler.Verify(ctx, env, lsEmptyInput, raw); err != nil {
				t.Errorf("verify: %v", err)
			}
		})
	}

	failing := []struct {
		name     string
		wrap     func(*fdListeners) core.Listeners
		want     error
		listener string // the listener's state after the failed apply
	}{
		{"the listener cannot be revoked", func(l *fdListeners) core.Listeners { return lsNoRevoke{fdListeners: l} }, errLsRevoke, core.ListenerOpen},
		{"the listener cannot be closed", func(l *fdListeners) core.Listeners { return lsNoClose{fdListeners: l} }, errLsClose, core.ListenerRevoked},
	}
	for _, tc := range failing {
		t.Run(tc.name, func(t *testing.T) {
			r := lsNewRig(t)
			row := r.expose(StateOpen, lsOther, core.ListenerOpen)
			env := r.env(lsRmCall, "")
			env.Listeners = tc.wrap(r.l)
			_, err := def.Handler.Apply(ctx, env, lsEmptyInput)
			if !errors.Is(err, tc.want) {
				t.Fatalf("apply error %v, want %v", err, tc.want)
			}
			if got := r.l.Rows[row.ListenerID].State; got != tc.listener {
				t.Errorf("listener %s is %q, want %q", row.ListenerID, got, tc.listener)
			}
			live, ok := r.live()
			if !ok || live.State != StateOpen || live.RemovalChangeID != lsOther {
				t.Errorf("exposure %+v (found %t), want it still open with removal %q", live, ok, lsOther)
			}
		})
	}

	none := lsNewRig(t)
	_, err := def.Handler.Apply(ctx, none.env(lsRmCall, ""), lsEmptyInput)
	lsWantRefusal(t, "apply without an exposure", err, errcode.PanelNotExposed)
}

var errLsClose = errors.New("close failed")

// lsNoClose is fdListeners whose Close always fails.
type lsNoClose struct {
	*fdListeners
}

func (lsNoClose) Close(context.Context, string) error { return errLsClose }

// TestLsRevokeVerify: the verification wants the exposure revoked and its listener no longer open.
func TestLsRevokeVerify(t *testing.T) {
	ctx := context.Background()
	def := listenerRevokeAction()
	tests := []struct {
		name     string
		row      string
		listener string
		ok       bool
	}{
		{"revoked and closed", StateRevoked, core.ListenerClosed, true},
		{"revoked behind an open listener", StateRevoked, core.ListenerOpen, false},
		{"still open behind a closed listener", StateOpen, core.ListenerClosed, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := lsNewRig(t)
			r.expose(tc.row, "", tc.listener)
			err := def.Handler.Verify(ctx, r.env(core.CallContext{}, ""), lsEmptyInput, lsEmptyInput)
			if (err == nil) != tc.ok {
				t.Errorf("verify error %v, want success: %t", err, tc.ok)
			}
		})
	}
	none := lsNewRig(t)
	if err := def.Handler.Verify(ctx, none.env(core.CallContext{}, ""), lsEmptyInput, lsEmptyInput); err == nil {
		t.Error("verify accepted a controller with no exposure")
	}
}
