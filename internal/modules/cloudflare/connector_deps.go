package cloudflare

import (
	"context"
	"errors"
	"os"
	"regexp"
	"testing"
	"time"
	"unicode"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

const (
	// tokenDir is the managed directory of the connectors' token files.
	tokenDir = "/var/lib/darkpanel/agent/cloudflare-tokens" //nolint:gosec // G101: a directory path, not a credential
	// tokenMount is where a connector's container reads its token file.
	tokenMount = "/run/darkpanel/token" //nolint:gosec // G101: a mount path, not a credential

	// absentSocket is the socket a test binary dials instead of the host's. Test binaries that link the module open a
	// runtime that may call the Engine, and they must never reach the host's Docker daemon; a path that never exists
	// makes every call fail as a missing socket.
	absentSocket = "/nonexistent/darkpanel/docker.sock"
)

var (
	// tunnelIDPattern is a tunnel id: a lower-case UUID.
	tunnelIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	// networkNamePattern is a Docker network or container name.
	networkNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
)

// connectorName is the name of the tunnel's connector container.
func connectorName(tunnel string) string { return "darkpanel-cloudflared-" + tunnel }

// tokenFileName is the name of the tunnel's token file in tokenDir.
func tokenFileName(tunnel string) string { return tunnel + ".token" }

// tunnelIDProblem is why id is not a tunnel id, or "" when it is one.
func tunnelIDProblem(id string) string {
	if tunnelIDPattern.MatchString(id) {
		return ""
	}
	return "tunnel must be a lower-case UUID"
}

// claimantProblem is why c is not a claimant, or "" when it is one.
func claimantProblem(c string) string {
	if c == "" || len(c) > 300 {
		return "claimant must be 1 to 300 bytes"
	}
	for _, r := range c {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return "claimant must not contain whitespace or control characters"
		}
	}
	return ""
}

// ownedPaths is what the module creates on the host: the managed directory of the connectors' token files, one per
// tunnel.
func ownedPaths() []core.OwnedPath {
	return []core.OwnedPath{{
		Path:    tokenDir,
		Kind:    core.OwnedManagedDir,
		Pattern: `^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\.token$`,
	}}
}

// agentDeps are the host functions the module's agent actions reach through.
type agentDeps struct {
	docker   connectorDocker
	readFile func(name string) ([]byte, error) // os.ReadFile
}

// connectorDocker is the part of the Engine API client the connector actions use.
type connectorDocker interface {
	InspectContainer(ctx context.Context, id string) (dockerapi.ContainerJSON, error)
	ListContainers(ctx context.Context, o dockerapi.ListContainersOptions) ([]dockerapi.ContainerSummary, error)
	CreateContainer(ctx context.Context, cfg dockerapi.ContainerCreate) (string, error)
	StartContainer(ctx context.Context, id string) (bool, error)
	StopContainer(ctx context.Context, id string, timeout time.Duration) (bool, error)
	RemoveContainer(ctx context.Context, id string, o dockerapi.RemoveContainerOptions) error
	InspectNetwork(ctx context.Context, id string) (dockerapi.Network, error)
	ConnectNetwork(ctx context.Context, network, container string, ep *dockerapi.EndpointSettings) error
	DisconnectNetwork(ctx context.Context, network, container string, force bool) error
	InspectImage(ctx context.Context, ref string) (dockerapi.ImageInspect, error)
	PullImage(ctx context.Context, ref string) (pullStream, error)
}

// pullStream yields the progress messages of an image pull.
type pullStream interface {
	Next() (dockerapi.PullMessage, error)
	Close() error
}

// engineClient adapts the Engine client to connectorDocker.
type engineClient struct{ *dockerapi.Client }

// PullImage starts a pull; a failure returns a nil interface, not the client's nil pointer inside one.
func (c engineClient) PullImage(ctx context.Context, ref string) (pullStream, error) {
	s, err := c.Client.PullImage(ctx, ref)
	if err != nil {
		return nil, err
	}
	return s, nil
}

// productionAgentDeps wires the agent actions to the host's Docker daemon.
func productionAgentDeps() agentDeps {
	socket := dockerapi.DefaultSocket
	if testing.Testing() {
		socket = absentSocket
	}
	return agentDeps{docker: engineClient{dockerapi.New(socket)}, readFile: os.ReadFile}
}

// engineCoded maps an unreachable Engine to docker.engine_unavailable, keeping err as the cause, and returns any other
// error unchanged.
func engineCoded(err error) error {
	if errors.Is(err, dockerapi.ErrUnavailable) {
		return errcode.Wrap(errcode.DockerEngineUnavailable, err, "the Docker Engine is not reachable")
	}
	return err
}
