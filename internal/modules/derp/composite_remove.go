package derp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/derp/server"
)

// serverRemove is derp.server.remove: it withdraws a server's region from the tailnet policy file, uninstalls derper,
// unpins tailscaled and closes the server's ports. The engine repeats the steps on repair instead of undoing them.
func (d dp) serverRemove() core.ActionDef {
	return core.DefineComposite[ServerRemoveInput, ServerRemoved](core.ActionSpec{
		Name: ID + ".server.remove", Version: 1, Unwind: core.UnwindForward,
		Summary: "Withdraw a DERP server's region, uninstall derper, unpin tailscaled and close the server's ports on a host.",
	}, serverRemoveComposer{d: d})
}

// serverRemovePatch is the input of the region step: the edits step's edits as a whole-string reference, which keeps
// their JSON type, an array.
const serverRemovePatch = `{"edits":"${steps.edits.output.edits}"}`

// serverRemoveComposer removes a DERP server: its region first, when it publishes one on a connected tailnet, then
// derper, the tailscaled pin and the firewall rules.
type serverRemoveComposer struct{ d dp }

// Steps returns the removal's steps for the server on in.HostID.
func (c serverRemoveComposer) Steps(ctx context.Context, env core.Env, in ServerRemoveInput) ([]core.Step, error) {
	rec, known, err := loadServerRecord(ctx, env.DB.Read(), in.HostID)
	if err != nil {
		return nil, err
	}
	var steps []core.Step
	var uninstallAfter []string
	regionID, connected := rec.RegionID, false
	switch {
	case !known:
		if connected, err = c.d.d.connected(ctx, env.DB); err != nil {
			return nil, err
		}
		if connected {
			if regionID, err = c.unrecordedRegion(ctx, env, in.HostID); err != nil {
				return nil, err
			}
		}
	case regionID != 0:
		if connected, err = c.d.d.connected(ctx, env.DB); err != nil {
			return nil, err
		}
	}
	if regionID != 0 {
		if connected {
			edits, err := json.Marshal(RegionEditsInput{Op: "remove", RegionID: regionID})
			if err != nil {
				return nil, fmt.Errorf("derp: %w", err)
			}
			steps = append(steps,
				core.Step{ID: "edits", Action: core.ActionRef{Name: ID + ".region.edits", Version: 1}, Input: edits},
				core.Step{
					ID: "region", Action: core.ActionRef{Name: "tailnet.policy.patch", Version: 1},
					Input: json.RawMessage(serverRemovePatch), After: []string{"edits"},
				},
			)
			uninstallAfter = []string{"region"}
		}
	}
	var ids []string
	for _, r := range server.PortRules(in.HostID) {
		ids = append(ids, r.ID)
	}
	ports, err := json.Marshal(struct {
		Delete []string `json:"delete"`
	}{ids})
	if err != nil {
		return nil, fmt.Errorf("derp: %w", err)
	}
	return append(steps,
		core.Step{
			ID: "uninstall", Host: in.HostID, Action: core.ActionRef{Name: ID + ".server.uninstall", Version: 1},
			Input: json.RawMessage(`{}`), After: uninstallAfter,
		},
		core.Step{
			ID: "unpin", Host: in.HostID, Action: core.ActionRef{Name: "tailscale.package.unpin", Version: 1},
			Input: json.RawMessage(`{}`), After: []string{"uninstall"},
		},
		// firewall.rules.apply is revertible, and the engine accepts a
		// revertible step only as a composite's last.
		core.Step{
			ID: "ports", Host: in.HostID, Action: core.ActionRef{Name: "firewall.rules.apply", Version: 1},
			Input: ports, After: []string{"unpin"},
		},
	), nil
}

// unrecordedRegion is the region to withdraw for a host the derp.servers service has not recorded yet: the policy's
// region whose node is the server the host's agent reports, else 0. A host whose status cannot be read is refused,
// since removing its server could leave its region published.
func (c serverRemoveComposer) unrecordedRegion(ctx context.Context, env core.Env, hostID string) (int, error) {
	st, err := readStatus(ctx, env, hostID, nil)
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return 0, cerr
		}
		return 0, core.Definite(errcode.New(errcode.DERPNotReady, "the controller has no record of the DERP server on "+hostID+
			" and cannot read its status, so whether it publishes a region is not known"))
	}
	if !st.Installed {
		return 0, nil
	}
	p, err := c.d.d.policy(ctx, env)
	if err != nil {
		return 0, err
	}
	m, err := parseDERPMap(p.Text)
	if err != nil {
		return 0, err
	}
	region, _ := m.regionOf(st.HostName)
	return region.RegionID, nil
}

// Output is what the uninstall step removed.
func (serverRemoveComposer) Output(_ context.Context, _ core.Env, _ ServerRemoveInput, steps map[string]json.RawMessage) (ServerRemoved, error) {
	raw, ok := steps["uninstall"]
	if !ok {
		return ServerRemoved{}, errors.New("derp: the removal has no output of its uninstall step")
	}
	var out ServerUninstalled
	if err := json.Unmarshal(raw, &out); err != nil {
		return ServerRemoved{}, fmt.Errorf("derp: the removal's uninstall output: %w", err)
	}
	if out.Removed == nil {
		out.Removed = []string{}
	}
	return ServerRemoved(out), nil
}
