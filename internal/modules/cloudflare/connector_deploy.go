package cloudflare

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// connectorDeploy declares cloudflare.connector.deploy: a tunnel's connector run on a host, the tunnel created first
// when none is named. A failed step undoes the earlier ones in reverse.
func (c cf) connectorDeploy() core.ActionDef {
	return core.DefineComposite[ConnectorDeployInput, ConnectorDeployOutput](core.ActionSpec{
		Name: ID + ".connector.deploy", Version: 1, Unwind: core.UnwindReverse,
		Summary: "Run a tunnel's connector on a host, creating the tunnel first when none is named.",
	}, connectorDeployComposer{})
}

// deployStepInput is a step template built from members.
func deployStepInput(members map[string]any) (json.RawMessage, error) {
	raw, err := json.Marshal(members)
	if err != nil {
		return nil, fmt.Errorf("cloudflare: %w", err)
	}
	return raw, nil
}

// connectorDeployComposer deploys a connector: it creates a tunnel when none is named, reads the tunnel's token and
// runs the connector with it on the host.
type connectorDeployComposer struct{}

// Steps returns the deployment's steps for in.
func (connectorDeployComposer) Steps(ctx context.Context, env core.Env, in ConnectorDeployInput) ([]core.Step, error) {
	// The engine refuses a literal in a secret field and stores a template in clear, so the token reaches the run only
	// as a reference to the token step's sealed output.
	token := "${steps.token.output.token}" //nolint:gosec // G101: a reference to the token step's output, not a credential
	tokenRef := core.ActionRef{Name: ID + ".tunnel.token", Version: 1}
	runRef := core.ActionRef{Name: ID + ".connector.run", Version: 1}
	if in.Tunnel == "" {
		create, err := deployStepInput(map[string]any{})
		if err != nil {
			return nil, err
		}
		read, err := deployStepInput(map[string]any{"tunnel": "${steps.tunnel.output.id}"})
		if err != nil {
			return nil, err
		}
		run, err := deployStepInput(map[string]any{"tunnel": "${steps.tunnel.output.id}", "token": token})
		if err != nil {
			return nil, err
		}
		return []core.Step{
			{ID: "tunnel", Action: core.ActionRef{Name: ID + ".tunnel.create", Version: 1}, Input: create},
			{ID: "token", Action: tokenRef, Input: read, After: []string{"tunnel"}},
			{ID: "run", Host: in.HostID, Action: runRef, Input: run, After: []string{"token"}},
		}, nil
	}
	// The templates hold the stored row's id, never what the operator typed, which the engine would read as a
	// reference wherever it holds "${".
	row, found, err := loadTunnel(ctx, env.DB.Read(), in.Tunnel)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, core.Definite(errcode.New(errcode.NotFound, "no managed tunnel "+in.Tunnel))
	}
	read, err := deployStepInput(map[string]any{"tunnel": row.ID})
	if err != nil {
		return nil, err
	}
	run, err := deployStepInput(map[string]any{"tunnel": row.ID, "token": token})
	if err != nil {
		return nil, err
	}
	return []core.Step{
		{ID: "token", Action: tokenRef, Input: read},
		{ID: "run", Host: in.HostID, Action: runRef, Input: run, After: []string{"token"}},
	}, nil
}

// Output is the tunnel's id, whether the deployment created it, and the connector the run step started.
func (connectorDeployComposer) Output(ctx context.Context, env core.Env, in ConnectorDeployInput, steps map[string]json.RawMessage) (ConnectorDeployOutput, error) {
	raw, ok := steps["run"]
	if !ok {
		return ConnectorDeployOutput{}, errors.New("cloudflare: the deployment has no output of its run step")
	}
	var conn Connector
	if err := json.Unmarshal(raw, &conn); err != nil {
		return ConnectorDeployOutput{}, fmt.Errorf("cloudflare: the deployment's run output: %w", err)
	}
	if in.Tunnel == "" {
		raw, ok := steps["tunnel"]
		if !ok {
			return ConnectorDeployOutput{}, errors.New("cloudflare: the deployment has no output of its tunnel step")
		}
		var ref TunnelRef
		if err := json.Unmarshal(raw, &ref); err != nil {
			return ConnectorDeployOutput{}, fmt.Errorf("cloudflare: the deployment's tunnel output: %w", err)
		}
		if ref.ID == "" {
			return ConnectorDeployOutput{}, errors.New("cloudflare: the deployment's tunnel output holds no id")
		}
		return ConnectorDeployOutput{Tunnel: ref.ID, Created: true, Connector: conn}, nil
	}
	row, found, err := loadTunnel(ctx, env.DB.Read(), in.Tunnel)
	if err != nil {
		return ConnectorDeployOutput{}, err
	}
	if !found {
		return ConnectorDeployOutput{}, errors.New("cloudflare: the deployment's tunnel " + in.Tunnel + " is no longer managed")
	}
	return ConnectorDeployOutput{Tunnel: row.ID, Connector: conn}, nil
}
