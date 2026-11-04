package panel

import (
	"context"
	"testing"

	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

// depsAbsentSocket is the socket a test binary dials instead of the host's. Test binaries that link the module open a
// runtime that may call it, and they must never reach the host's Docker daemon; a path that never exists makes every
// call fail as a missing socket.
const depsAbsentSocket = "/nonexistent/darkpanel/docker.sock"

// panelDocker is the Engine API surface the agent actions use; *dockerapi.Client implements it.
type panelDocker interface {
	ListNetworks(ctx context.Context, filters dockerapi.Filters) ([]dockerapi.Network, error)
	InspectNetwork(ctx context.Context, id string) (dockerapi.Network, error)
	CreateNetwork(ctx context.Context, n dockerapi.NetworkCreate) (string, error)
	RemoveNetwork(ctx context.Context, id string) error
	InspectContainer(ctx context.Context, id string) (dockerapi.ContainerJSON, error)
}

// agentDeps are the host dependencies of the agent actions; tests replace them.
type agentDeps struct {
	docker func() (panelDocker, error) // a client over the Engine socket; tests return the fake
}

// productionAgent wires the agent actions to the host's Docker daemon.
func productionAgent() agentDeps {
	socket := dockerapi.DefaultSocket
	if testing.Testing() {
		socket = depsAbsentSocket
	}
	client := dockerapi.New(socket)
	return agentDeps{docker: func() (panelDocker, error) { return client, nil }}
}
