package tailscale

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/tailscale/link"
)

// nodeLogout is tailscale.node.logout: it logs the node out of its tailnet. A logout discards the node key, so
// nothing undoes it and it journals nothing.
func (t ts) nodeLogout() core.ActionDef {
	return core.DefineMutating[LogoutInput, LogoutResult](core.ActionSpec{
		Name: ID + ".node.logout", Summary: "Log the node out of its tailnet.",
		Version: 1, Class: core.ClassIrreversible, Side: core.SideAgent, LockKeys: []string{lockKey},
		Guards:    []core.GuardSpec{{ID: link.GuardID, Overridable: true}},
		Available: t.available,
	}, nodeLogoutHandler{t: t})
}

// nodeLogoutHandler is node.logout's handler.
type nodeLogoutHandler struct{ t ts }

func (h nodeLogoutHandler) Plan(ctx context.Context, env core.Env, in LogoutInput) (core.PlanOutcome, error) {
	st, err := nodeLoginStatus(ctx, h.t.d.local())
	if err != nil {
		return core.PlanOutcome{}, err
	}
	loggedIn, known := nodeLoginLoggedIn(st)
	switch {
	case !known:
		return core.PlanOutcome{}, nodeLoginNotStarted()
	case !loggedIn:
		return core.PlanOutcome{}, core.Definite(errcode.New(errcode.InvalidInput, "the node is already logged out"))
	}
	host, err := h.t.d.hostname()
	if err != nil {
		return core.PlanOutcome{}, fmt.Errorf("tailscale: read the host name: %w", err)
	}
	lp, _, err := h.t.currentLinkPrefs(ctx)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	g, err := h.t.linkGuard(ctx, env, in.Guarded, lp, link.Change{Kind: "logout", After: lp})
	if err != nil {
		return core.PlanOutcome{}, err
	}
	var self string
	if st.Self != nil {
		self = st.Self.ID
	}
	sum := sha256.Sum256([]byte(st.BackendState + "|" + self))
	return core.PlanOutcome{
		Diff:         []core.DiffLine{{Op: "-", Text: "node: logged in as " + nodeLoginSelfName(st)}, {Op: "+", Text: "node: logged out"}},
		Guards:       []core.Guard{g},
		Confirmation: host,
		StateHash:    hex.EncodeToString(sum[:]),
	}, nil
}

func (h nodeLogoutHandler) Apply(ctx context.Context, _ core.Env, _ LogoutInput) (LogoutResult, error) {
	local := h.t.d.local()
	if err := local.Logout(ctx); err != nil {
		return LogoutResult{}, nodeLoginRefusal(err, "logout", "")
	}
	st, err := nodeLoginStatus(ctx, local)
	if err != nil {
		return LogoutResult{}, err
	}
	return LogoutResult{BackendState: st.BackendState}, nil
}

// Verify checks the host from the input alone, as recovery calls it without an output.
func (h nodeLogoutHandler) Verify(ctx context.Context, _ core.Env, _ LogoutInput, _ LogoutResult) error {
	st, err := nodeLoginStatus(ctx, h.t.d.local())
	if err != nil {
		return err
	}
	if st.BackendState != "NeedsLogin" {
		return fmt.Errorf("tailscale: the backend state is %s after the logout, want NeedsLogin", st.BackendState)
	}
	return nil
}
