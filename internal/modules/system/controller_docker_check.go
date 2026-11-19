package system

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/firewall"
)

// dockerCheckIn is the input of system.docker.check.
type dockerCheckIn struct {
	HostID              string `json:"hostId" jsonschema:"minLength=1"`
	FirewallIntegration bool   `json:"firewallIntegration"`
}

// DockerCheckResult is the output of system.docker.check: the flag the host's state agreed with.
type DockerCheckResult struct {
	FirewallIntegration bool `json:"firewallIntegration"`
}

func (m module) dockerCheckAction() core.ActionDef {
	return core.DefineInternalRead[dockerCheckIn, DockerCheckResult](core.ActionSpec{
		Name:    ID + ".docker.check",
		Summary: "Check, for the controller's own use, that a Docker install's firewall flag still matches the host.",
		Version: 1,
		Side:    core.SideController,
	}, dockerCheck{}.Read)
}

// dockerCheck compares the firewall flag of a Docker install with the host: the integration is possible when
// the firewall module is enabled and available there and UFW is active.
type dockerCheck struct{}

func (dockerCheck) Read(ctx context.Context, env core.Env, in dockerCheckIn) (core.Revealed[DockerCheckResult], error) {
	on, err := firewallIntegrationPossible(ctx, env, in.HostID)
	if err != nil {
		return core.Revealed[DockerCheckResult]{}, err
	}
	if on != in.FirewallIntegration {
		return core.Revealed[DockerCheckResult]{}, core.Definite(errcode.New(errcode.SystemFirewallFlagMismatch, fmt.Sprintf(
			"the install's firewall integration flag is %t, but the host's firewall module and UFW say %t; reload the page and install again",
			in.FirewallIntegration, on)))
	}
	return core.Revealed[DockerCheckResult]{
		Identity: "Docker install check for host " + in.HostID,
		Value:    DockerCheckResult{FirewallIntegration: on},
	}, nil
}

// firewallIntegrationPossible reads the controller's core_module_state for the host's firewall module, the
// authority on enablement (the agent answers a disabled module's reads), and, only when it is enabled and
// available, whether UFW is active.
func firewallIntegrationPossible(ctx context.Context, env core.Env, hostID string) (bool, error) {
	var enabled, available bool
	err := env.DB.Read().QueryRowContext(ctx,
		"SELECT enabled, available FROM core_module_state WHERE host_id = ? AND module_id = ?", hostID, firewall.ID).
		Scan(&enabled, &available)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("system: read the firewall module state of host %s: %w", hostID, err)
	case !enabled || !available:
		return false, nil
	}
	agent, err := env.Agents.Agent(ctx, hostID)
	if err != nil {
		return false, fmt.Errorf("system: read the firewall status of host %s: %w", hostID, err)
	}
	raw, err := agent.Resource(ctx, env.Principal, firewall.ID+".status")
	if err != nil {
		return false, fmt.Errorf("system: read the firewall status of host %s: %w", hostID, err)
	}
	var st firewall.Status
	if err := json.Unmarshal(raw, &st); err != nil {
		return false, fmt.Errorf("system: read the firewall status of host %s: %w", hostID, err)
	}
	return st.Active, nil
}
