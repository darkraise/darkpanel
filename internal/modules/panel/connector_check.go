package panel

import (
	"context"
	"fmt"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

// connectorModule is the module whose label a tunnel's connector container carries.
const connectorModule = "cloudflare"

// connectorCheckAction declares panel.connector.check: the controller's own check that a tunnel's connector container
// runs on the agent's host. It changes nothing.
func connectorCheckAction(d agentDeps) core.ActionDef {
	return core.DefineInternalRead[ConnectorCheckInput, ConnectorCheckOutput](core.ActionSpec{
		Name:     ActionConnCheck,
		Summary:  "Check, for the controller's own use, that a tunnel's connector container runs on this host.",
		Version:  1,
		Side:     core.SideAgent,
		MinScope: core.ScopeAdmin,
	}, func(ctx context.Context, _ core.Env, in ConnectorCheckInput) (core.Revealed[ConnectorCheckOutput], error) {
		return connectorCheck(ctx, d, in)
	})
}

// connectorCheck refuses a malformed tunnel id before it reads the Engine, then refuses a connector that is missing,
// that another module made or that does not run.
func connectorCheck(ctx context.Context, d agentDeps, in ConnectorCheckInput) (core.Revealed[ConnectorCheckOutput], error) {
	if !validateTunnel.MatchString(in.Tunnel) {
		msg := "tunnel must be a lowercase UUID"
		return core.Revealed[ConnectorCheckOutput]{}, core.Definite(&errcode.Error{Code: errcode.InvalidInput, Detail: msg,
			Fields: []errcode.FieldError{{Path: "/tunnel", Message: msg}}})
	}
	docker, err := networkClient(d)
	if err != nil {
		return core.Revealed[ConnectorCheckOutput]{}, err
	}
	name := ConnectorPrefix + in.Tunnel
	missing := func(why string) (core.Revealed[ConnectorCheckOutput], error) {
		return core.Revealed[ConnectorCheckOutput]{}, core.Definite(errcode.New(errcode.CloudflareConnectorMissing,
			fmt.Sprintf("the connector %s %s", name, why)))
	}
	c, err := docker.InspectContainer(ctx, name)
	switch {
	case dockerapi.IsNotFound(err):
		return missing("does not exist on this host")
	case err != nil:
		return core.Revealed[ConnectorCheckOutput]{}, networkReadFailed("inspect container "+name, err)
	case c.Config.Labels[core.LabelModule] != connectorModule:
		return missing(fmt.Sprintf("is not labelled %s=%s", core.LabelModule, connectorModule))
	case !c.State.Running:
		return missing(fmt.Sprintf("is not running (state %s)", c.State.Status))
	}
	return core.Revealed[ConnectorCheckOutput]{
		Identity: "panel connector check for tunnel " + in.Tunnel,
		Value:    ConnectorCheckOutput{Container: name, State: c.State.Status},
	}, nil
}
