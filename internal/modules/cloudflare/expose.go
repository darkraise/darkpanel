package cloudflare

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// exposeClaimantPrefix starts the claimant of an exposure's network claim; the claimant ends with its hostname.
const exposeClaimantPrefix = "expose:"

// exposeContainer declares cloudflare.expose.container: a container served on a hostname through a tunnel's
// connector. A failed step undoes the earlier ones in reverse.
func (c cf) exposeContainer() core.ActionDef {
	return core.DefineComposite[ExposeInput, ExposeOutput](core.ActionSpec{
		Name: ID + ".expose.container", Version: 1, Unwind: core.UnwindReverse,
		Summary: "Attach a tunnel's connector to a container's network and route a hostname of the tunnel to the container.",
	}, exposeContainerComposer{})
}

// exposeRemove declares cloudflare.expose.remove: the removal of an exposed container's hostname and network claims.
// The engine repeats the steps on repair instead of undoing them.
func (c cf) exposeRemove() core.ActionDef {
	return core.DefineComposite[ExposeRemoveInput, ExposeRemoveOutput](core.ActionSpec{
		Name: ID + ".expose.remove", Version: 1, Unwind: core.UnwindForward,
		Summary: "Remove an exposed container's hostname and the network claims of its exposure.",
	}, exposeRemoveComposer{})
}

// exposeStepInput is a step template built from members. The engine reads every "${" of a template as a reference, so
// what an operator typed reaches a template only as a reference to the composite's input, never as its text.
func exposeStepInput(members map[string]any) (json.RawMessage, error) {
	raw, err := json.Marshal(members)
	if err != nil {
		return nil, fmt.Errorf("cloudflare: %w", err)
	}
	return raw, nil
}

// exposeStepOutput decodes the output of step id into into.
func exposeStepOutput(steps map[string]json.RawMessage, id string, into any) error {
	raw, ok := steps[id]
	if !ok {
		return errors.New("cloudflare: the exposure has no output of its " + id + " step")
	}
	if err := json.Unmarshal(raw, into); err != nil {
		return fmt.Errorf("cloudflare: the exposure's %s output: %w", id, err)
	}
	return nil
}

// exposeContainerComposer exposes a container: it attaches the tunnel's connector to the container's network, then
// adds the hostname that routes to the container.
type exposeContainerComposer struct{}

// Steps returns the exposure's steps for in.
func (exposeContainerComposer) Steps(ctx context.Context, env core.Env, in ExposeInput) ([]core.Step, error) {
	// The container name is written into the service literally, which is safe only because the pattern keeps "$",
	// white space and "|" out of it.
	switch {
	case !networkNamePattern.MatchString(in.Container):
		return nil, hostnameInvalid("/container", "the container must be a container name of 1 to 128 letters, digits, '_', '.' or '-' that starts with a letter or digit")
	case !networkNamePattern.MatchString(in.Network):
		return nil, hostnameInvalid("/network", "the network must be a network name of 1 to 128 letters, digits, '_', '.' or '-' that starts with a letter or digit")
	case in.Port < 1 || in.Port > 65535:
		return nil, hostnameInvalid("/port", "the port must be from 1 to 65535")
	case in.Hostname == "":
		return nil, hostnameInvalid("/hostname", "the hostname is required")
	}
	tunnel, found, err := loadTunnel(ctx, env.DB.Read(), in.Tunnel)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, core.Definite(errcode.New(errcode.NotFound, "no managed tunnel "+in.Tunnel))
	}
	network, err := exposeStepInput(map[string]any{
		"tunnel":   tunnel.ID,
		"network":  "${input.network}",
		"claimant": exposeClaimantPrefix + "${input.hostname}",
		"target":   "${input.container}",
	})
	if err != nil {
		return nil, err
	}
	hostname, err := exposeStepInput(map[string]any{
		"tunnel":   tunnel.ID,
		"hostname": "${input.hostname}",
		"service":  "http://" + in.Container + ":" + strconv.Itoa(in.Port),
	})
	if err != nil {
		return nil, err
	}
	return []core.Step{
		{ID: "network", Host: in.HostID, Action: core.ActionRef{Name: ID + ".network.attach", Version: 1}, Input: network},
		{
			ID: "hostname", Action: core.ActionRef{Name: ID + ".hostname.add", Version: 1},
			Input: hostname, After: []string{"network"},
		},
	}, nil
}

// Output is the added hostname, its service and the network the connector was attached to.
func (exposeContainerComposer) Output(_ context.Context, _ core.Env, _ ExposeInput, steps map[string]json.RawMessage) (ExposeOutput, error) {
	var attachment NetworkAttachment
	if err := exposeStepOutput(steps, "network", &attachment); err != nil {
		return ExposeOutput{}, err
	}
	var hostname HostnameView
	if err := exposeStepOutput(steps, "hostname", &hostname); err != nil {
		return ExposeOutput{}, err
	}
	return ExposeOutput{Hostname: hostname.Hostname, Service: hostname.Service, Network: attachment.Network}, nil
}

// exposeRemoveComposer removes an exposure: its hostname, then every network claim of its claimant on the hostname's
// tunnel.
type exposeRemoveComposer struct{}

// Steps returns the removal's steps for in.
func (exposeRemoveComposer) Steps(ctx context.Context, env core.Env, in ExposeRemoveInput) ([]core.Step, error) {
	row, found, err := loadHostname(ctx, env.DB.Read(), in.Hostname)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, core.Definite(errcode.New(errcode.NotFound, "no managed hostname "+in.Hostname))
	}
	hostname, err := exposeStepInput(map[string]any{"hostname": "${input.hostname}"})
	if err != nil {
		return nil, err
	}
	network, err := exposeStepInput(map[string]any{
		"tunnel":   row.TunnelID,
		"network":  "",
		"claimant": exposeClaimantPrefix + "${input.hostname}",
	})
	if err != nil {
		return nil, err
	}
	return []core.Step{
		{ID: "hostname", Action: core.ActionRef{Name: ID + ".hostname.remove", Version: 1}, Input: hostname},
		{
			ID: "network", Host: in.HostID, Action: core.ActionRef{Name: ID + ".network.detach", Version: 1},
			Input: network, After: []string{"hostname"},
		},
	}, nil
}

// Output is the removed exposure's hostname.
func (exposeRemoveComposer) Output(_ context.Context, _ core.Env, in ExposeRemoveInput, steps map[string]json.RawMessage) (ExposeRemoveOutput, error) {
	var hostname HostnameView
	if err := exposeStepOutput(steps, "hostname", &hostname); err != nil {
		return ExposeRemoveOutput{}, err
	}
	var attachment NetworkAttachment
	if err := exposeStepOutput(steps, "network", &attachment); err != nil {
		return ExposeRemoveOutput{}, err
	}
	return ExposeRemoveOutput{Hostname: in.Hostname}, nil
}
