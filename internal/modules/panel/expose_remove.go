package panel

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// The actions of other modules that panel.exposure.remove runs as steps.
const (
	removeProxyHost     = "npm.proxyhost.remove"
	removeNPMDetach     = "npm.network.detach"
	removeTunnelHost    = "cloudflare.hostname.remove"
	removeTunnelDetach  = "cloudflare.network.detach"
	removeFirewallApply = "firewall.rules.apply"
)

// exposureRemoveAction declares panel.exposure.remove: the removal of the live exposure of the panel. The listener is
// revoked first, so a failed later step leaves the panel unreachable rather than undoing the revocation.
func exposureRemoveAction() core.ActionDef {
	return core.DefineComposite[RemoveInput, RemoveOutput](core.ActionSpec{
		Name: ActionRemove, Version: 1, Unwind: core.UnwindForward, MinScope: core.ScopeAdmin,
		Summary: "Revoke the panel's listener, then remove its proxy host or hostname, attachment, network and firewall rule.",
	}, removeComposer{})
}

// removeComposer removes the live exposure: it revokes the listener, removes the proxy host or the tunnel hostname,
// detaches the proxy from the panel network, removes the network and deletes the firewall rule.
type removeComposer struct{}

// removeInput is a step template built from members.
func removeInput(members map[string]any) (json.RawMessage, error) {
	raw, err := json.Marshal(members)
	if err != nil {
		return nil, fmt.Errorf("panel: %w", err)
	}
	return raw, nil
}

// Steps returns the removal's steps for the live exposure, or refuses when there is none.
func (removeComposer) Steps(ctx context.Context, env core.Env, _ RemoveInput) ([]core.Step, error) {
	e, live, err := liveExposure(ctx, env.DB.Read())
	if err != nil {
		return nil, err
	}
	if !live {
		return nil, core.Definite(errcode.New(errcode.PanelNotExposed, "the panel is not exposed"))
	}

	type template struct {
		id, host, action string
		members          map[string]any
	}
	tmpls := []template{{"revoke", "", ActionRevoke, map[string]any{}}}
	if e.Via == ViaTunnel {
		tmpls = append(tmpls,
			template{"proxy", "", removeTunnelHost, map[string]any{"hostname": e.Hostname}},
			template{"attach", e.HostID, removeTunnelDetach, map[string]any{"tunnel": e.Tunnel, "network": NetworkName, "claimant": Claimant}},
		)
	} else {
		tmpls = append(tmpls,
			template{"proxy", e.HostID, removeProxyHost, map[string]any{"domain": e.Hostname}},
			template{"attach", e.HostID, removeNPMDetach, map[string]any{"network": NetworkName, "claimant": Claimant}},
		)
	}
	tmpls = append(tmpls,
		template{"network", e.HostID, ActionNetRemove, map[string]any{}},
		template{"ports", e.HostID, removeFirewallApply, map[string]any{
			"label": exposureLabel(e.Hostname), "delete": []string{"${steps.revoke.output.ruleId}"},
		}},
	)

	steps := make([]core.Step, 0, len(tmpls))
	for i, t := range tmpls {
		input, err := removeInput(t.members)
		if err != nil {
			return nil, err
		}
		s := core.Step{ID: t.id, Host: t.host, Action: core.ActionRef{Name: t.action, Version: 1}, Input: input}
		if i == 0 {
			s.Retires = e.ID
		} else {
			s.After = []string{tmpls[i-1].id}
		}
		steps = append(steps, s)
	}
	return steps, nil
}

// Output names the exposure the revoke step revoked and its hostname. It reads the row by id, not as the live
// exposure, because a completed removal has made the exposure gone.
func (removeComposer) Output(ctx context.Context, env core.Env, _ RemoveInput, steps map[string]json.RawMessage) (RemoveOutput, error) {
	raw, ok := steps["revoke"]
	if !ok {
		return RemoveOutput{}, errors.New("panel: the removal has no output of its revoke step")
	}
	var revoke ListenerRevokeOutput
	if err := json.Unmarshal(raw, &revoke); err != nil {
		return RemoveOutput{}, fmt.Errorf("panel: the removal's revoke output: %w", err)
	}
	if revoke.ExposureID == "" {
		return RemoveOutput{}, errors.New("panel: the removal's revoke step names no exposure")
	}
	var hostname string
	err := env.DB.Read().QueryRowContext(ctx, `SELECT hostname FROM panel_exposures WHERE id = ?`, revoke.ExposureID).Scan(&hostname)
	if errors.Is(err, sql.ErrNoRows) {
		return RemoveOutput{}, fmt.Errorf("panel: exposure %s does not exist", revoke.ExposureID)
	}
	if err != nil {
		return RemoveOutput{}, fmt.Errorf("panel: read exposure %s: %w", revoke.ExposureID, err)
	}
	return RemoveOutput{ExposureID: revoke.ExposureID, Hostname: hostname}, nil
}
