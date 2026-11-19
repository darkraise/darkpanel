package system

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/darkraise/darkpanel/internal/core"
)

// dockerInstallIn is the input of system.docker.install.
type dockerInstallIn struct {
	HostID              string `json:"hostId" jsonschema:"minLength=1"`
	FirewallIntegration bool   `json:"firewallIntegration" jsonschema_description:"Turn on the firewall's Docker integration after the install."`
}

func (m module) dockerInstallAction() core.ActionDef {
	return core.DefineComposite[dockerInstallIn, Applied](core.ActionSpec{
		Name: ID + ".docker.install", Version: 1, Unwind: core.UnwindReverse,
		Summary:  "Install Docker Engine on a host and, with the flag, turn on the firewall's Docker integration; a failed or reverted step undoes the install.",
		Rechecks: []string{"docker"},
	}, dockerInstallComposer{})
}

// dockerInstallComposer checks the flag, installs the engine, then turns the integration on. The firewall
// step plans only once the engine is installed: its plan asks the Docker daemon.
type dockerInstallComposer struct{}

func (dockerInstallComposer) Steps(_ context.Context, _ core.Env, in dockerInstallIn) ([]core.Step, error) {
	check, err := json.Marshal(map[string]any{"hostId": "${input.hostId}", "firewallIntegration": in.FirewallIntegration})
	if err != nil {
		return nil, fmt.Errorf("system: %w", err)
	}
	steps := []core.Step{
		{ID: "check", Action: core.ActionRef{Name: ID + ".docker.check", Version: 1}, Input: check},
		{
			ID: "engine", Host: in.HostID, Action: core.ActionRef{Name: ID + ".docker.engine-install", Version: 1},
			Input: json.RawMessage(`{}`), After: []string{"check"},
		},
	}
	if in.FirewallIntegration {
		// firewall.docker.enable is revertible, and the engine accepts a revertible step only as a composite's last.
		steps = append(steps, core.Step{
			ID: "firewall", Host: in.HostID, Action: core.ActionRef{Name: "firewall.docker.enable", Version: 1},
			Input: json.RawMessage(`{}`), After: []string{"engine"}, DeferPlan: true,
		})
	}
	return steps, nil
}

// Output is what the engine step installed.
func (dockerInstallComposer) Output(_ context.Context, _ core.Env, _ dockerInstallIn, steps map[string]json.RawMessage) (Applied, error) {
	raw, ok := steps["engine"]
	if !ok {
		return Applied{}, errors.New("system: the Docker install has no output of its engine step")
	}
	var out Applied
	if err := json.Unmarshal(raw, &out); err != nil {
		return Applied{}, fmt.Errorf("system: the Docker install's engine output: %w", err)
	}
	return out, nil
}
