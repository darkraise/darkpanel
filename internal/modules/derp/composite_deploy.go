package derp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/derp/artifact"
	"github.com/darkraise/darkpanel/internal/modules/derp/server"
	"github.com/darkraise/darkpanel/internal/modules/firewall/model"
)

// serverDeploy is derp.server.deploy: it pins tailscaled, installs derper and opens its ports on one host, and undoes
// the steps in reverse when one fails.
func (d dp) serverDeploy() core.ActionDef {
	return core.DefineComposite[DeployInput, Deployed](core.ActionSpec{
		Name: ID + ".server.deploy", Summary: "Pin tailscaled, install derper and open its ports on a host.", Version: 1,
		Unwind: core.UnwindReverse,
	}, serverDeployComposer{})
}

// serverUpgrade is derp.server.upgrade: it moves a server the controller knows to this release's derper, first
// checking, when the server publishes a region, that the host does not depend on that region alone.
func (d dp) serverUpgrade() core.ActionDef {
	return core.DefineComposite[UpgradeInput, Upgraded](core.ActionSpec{
		Name: ID + ".server.upgrade", Version: 1, Unwind: core.UnwindReverse,
		Summary: "Pin tailscaled, then replace derper; between the two steps the versions differ and the region may refuse clients.",
	}, serverUpgradeComposer{})
}

// serverDeployComposer deploys a DERP server in three steps: pin tailscaled to
// the derper version, install derper, and open its ports.
type serverDeployComposer struct{}

// serverDeployPin is the tailscale.package.pin input holding tailscaled at the
// version derper is built from.
func serverDeployPin() (json.RawMessage, error) {
	raw, err := json.Marshal(map[string]string{"version": artifact.PinVersion()})
	if err != nil {
		return nil, fmt.Errorf("derp: %w", err)
	}
	return raw, nil
}

// Steps returns the deploy's steps for in.
func (serverDeployComposer) Steps(_ context.Context, _ core.Env, in DeployInput) ([]core.Step, error) {
	pin, err := serverDeployPin()
	if err != nil {
		return nil, err
	}
	// The engine reads every "${" of a template as a reference, so the typed
	// values reach the install only as references to the input, and an absent
	// verifyClients is left out because a reference to it would fail the plan.
	install := map[string]string{"mode": "${input.mode}", "hostName": "${input.hostName}"}
	if in.VerifyClients != nil {
		install["verifyClients"] = "${input.verifyClients}"
	}
	installRaw, err := json.Marshal(install)
	if err != nil {
		return nil, fmt.Errorf("derp: %w", err)
	}
	ports, err := json.Marshal(struct {
		Label string       `json:"label"`
		Add   []model.Rule `json:"add"`
	}{server.RuleLabel, server.PortRules(in.HostID)})
	if err != nil {
		return nil, fmt.Errorf("derp: %w", err)
	}
	return []core.Step{
		{ID: "tailscaled", Host: in.HostID, Action: core.ActionRef{Name: "tailscale.package.pin", Version: 1}, Input: pin},
		{
			ID: "install", Host: in.HostID, Action: core.ActionRef{Name: ID + ".server.install", Version: 1},
			Input: installRaw, After: []string{"tailscaled"},
		},
		{
			ID: "ports", Host: in.HostID, Action: core.ActionRef{Name: "firewall.rules.apply", Version: 1},
			Input: ports, After: []string{"install"},
		},
	}, nil
}

// Output is the install step's derper version and certificate.
func (serverDeployComposer) Output(_ context.Context, _ core.Env, _ DeployInput, steps map[string]json.RawMessage) (Deployed, error) {
	raw, ok := steps["install"]
	if !ok {
		return Deployed{}, errors.New("derp: the deploy has no output of its install step")
	}
	var out ServerInstalled
	if err := json.Unmarshal(raw, &out); err != nil {
		return Deployed{}, fmt.Errorf("derp: the deploy's install output: %w", err)
	}
	return Deployed(out), nil
}

// serverUpgradeComposer upgrades a DERP server: check that the host keeps a
// path to the controller without its own region, pin tailscaled to the new
// derper version, and replace derper.
type serverUpgradeComposer struct{}

// Steps returns the upgrade's steps for the server the controller knows on in.HostID.
func (serverUpgradeComposer) Steps(ctx context.Context, env core.Env, in UpgradeInput) ([]core.Step, error) {
	rec, ok, err := loadServerRecord(ctx, env.DB.Read(), in.HostID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errcode.New(errcode.DERPNotReady, "no DERP server is known on "+in.HostID)
	}
	if err := compositeRegionKnown(rec); err != nil {
		return nil, err
	}
	pin, err := serverDeployPin()
	if err != nil {
		return nil, err
	}
	binary, err := json.Marshal(BinaryUpdateInput{Version: artifact.DerperVersion})
	if err != nil {
		return nil, fmt.Errorf("derp: %w", err)
	}
	var steps []core.Step
	var pinAfter []string
	if rec.RegionID != 0 {
		fallback, err := json.Marshal(FallbackCheckInput{HostID: "${input.hostId}", RegionID: rec.RegionID})
		if err != nil {
			return nil, fmt.Errorf("derp: %w", err)
		}
		steps = append(steps, core.Step{ID: "fallback", Action: core.ActionRef{Name: ID + ".fallback.check", Version: 1}, Input: fallback})
		pinAfter = []string{"fallback"}
	}
	return append(steps,
		core.Step{
			ID: "tailscaled", Host: in.HostID, Action: core.ActionRef{Name: "tailscale.package.pin", Version: 1},
			Input: pin, After: pinAfter,
		},
		core.Step{
			ID: "binary", Host: in.HostID, Action: core.ActionRef{Name: ID + ".binary.update", Version: 1},
			Input: binary, After: []string{"tailscaled"},
		},
	), nil
}

// Output is the binary step's versions.
func (serverUpgradeComposer) Output(_ context.Context, _ core.Env, _ UpgradeInput, steps map[string]json.RawMessage) (Upgraded, error) {
	raw, ok := steps["binary"]
	if !ok {
		return Upgraded{}, errors.New("derp: the upgrade has no output of its binary step")
	}
	var out BinaryUpdated
	if err := json.Unmarshal(raw, &out); err != nil {
		return Upgraded{}, fmt.Errorf("derp: the upgrade's binary output: %w", err)
	}
	return Upgraded(out), nil
}
