package cloudflare

import (
	"context"
	"fmt"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

// connectorList declares cloudflare.connector.list, which lists the
// connectors of this host with the state of their containers.
func (c cf) connectorList() core.ActionDef {
	return core.DefineRead[ConnectorListInput, ConnectorList](core.ActionSpec{
		Name: ID + ".connector.list", Summary: "List the Cloudflare tunnel connectors of this host and the state of their containers.",
		Version: 1, Side: core.SideAgent,
	}, connectorReader{c: c})
}

// connectorReader is the handler of cloudflare.connector.list.
type connectorReader struct{ c cf }

// Read answers every connector row, ordered by tunnel, with the state of the
// container the row records. Only a container the Engine reports absent reads
// as missing: an Engine that cannot answer fails the read, so a connector is
// never shown as gone while it may still run.
func (r connectorReader) Read(ctx context.Context, env core.Env, _ ConnectorListInput) (ConnectorList, error) {
	rows, err := listConnectors(ctx, env.DB.Read())
	if err != nil {
		return ConnectorList{}, err
	}
	out := make([]Connector, 0, len(rows))
	for _, row := range rows {
		name := connectorName(row.TunnelID)
		state := "missing"
		c, err := r.c.a.docker.InspectContainer(ctx, row.ContainerID)
		switch {
		case err == nil:
			state = c.State.Status
		case !dockerapi.IsNotFound(err):
			return ConnectorList{}, fmt.Errorf("cloudflare: inspect container %s: %w", name, engineCoded(err))
		}
		out = append(out, Connector{Tunnel: row.TunnelID, Container: name, ContainerID: row.ContainerID, State: state})
	}
	return ConnectorList{Connectors: out}, nil
}
