package docker

import (
	"context"
	"io/fs"
	"os"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

const (
	// lockKey is the lock every mutating docker action takes.
	lockKey = "docker"
	// commitWait is how long the Engine may still commit a call whose answer was lost.
	commitWait = 2 * time.Minute
	// stopTimeout is how long the Engine waits for a container to stop before it kills it.
	stopTimeout = 10 * time.Second
)

// dk carries the module's host dependencies; tests replace them.
type dk struct{ d deps }

// deps are the host functions the module reaches through.
type deps struct {
	docker   dockerAPI
	lstat    func(name string) (fs.FileInfo, error)                                        // os.Lstat; Sys() is *syscall.Stat_t in production
	readFile func(name string) ([]byte, error)                                             // os.ReadFile
	secrets  func(ctx context.Context, env core.Env, containerID string) ([][]byte, error) // production: none, nil error
}

// dockerAPI is the part of the Engine API client the module uses.
type dockerAPI interface {
	Version(ctx context.Context) (dockerapi.VersionInfo, error)
	Info(ctx context.Context) (dockerapi.SystemInfo, error)
	ListContainers(ctx context.Context, o dockerapi.ListContainersOptions) ([]dockerapi.ContainerSummary, error)
	InspectContainer(ctx context.Context, id string) (dockerapi.ContainerJSON, error)
	StartContainer(ctx context.Context, id string) (bool, error)
	StopContainer(ctx context.Context, id string, timeout time.Duration) (bool, error)
	RestartContainer(ctx context.Context, id string, timeout time.Duration) error
	RemoveContainer(ctx context.Context, id string, o dockerapi.RemoveContainerOptions) error
	ListImages(ctx context.Context, o dockerapi.ListImagesOptions) ([]dockerapi.ImageSummary, error)
	InspectImage(ctx context.Context, ref string) (dockerapi.ImageInspect, error)
	PullImage(ctx context.Context, ref string) (pullStream, error)
	RemoveImage(ctx context.Context, ref string, o dockerapi.RemoveImageOptions) ([]dockerapi.ImageDeleteItem, error)
	ListNetworks(ctx context.Context, f dockerapi.Filters) ([]dockerapi.Network, error)
	InspectNetwork(ctx context.Context, id string) (dockerapi.Network, error)
	CreateNetwork(ctx context.Context, n dockerapi.NetworkCreate) (string, error)
	RemoveNetwork(ctx context.Context, id string) error
	ConnectNetwork(ctx context.Context, network, container string, ep *dockerapi.EndpointSettings) error
	DisconnectNetwork(ctx context.Context, network, container string, force bool) error
	ListVolumes(ctx context.Context, f dockerapi.Filters) ([]dockerapi.Volume, error)
	InspectVolume(ctx context.Context, name string) (dockerapi.Volume, error)
	CreateVolume(ctx context.Context, v dockerapi.VolumeCreate) (dockerapi.Volume, error)
	RemoveVolume(ctx context.Context, name string, force bool) error
	Logs(ctx context.Context, id string, o dockerapi.LogsOptions) (logStream, error)
	Stats(ctx context.Context, id string, stream bool) (statsStream, error)
}

// pullStream yields the progress messages of an image pull.
type pullStream interface {
	Next() (dockerapi.PullMessage, error)
	Close() error
}

// logStream yields the frames of a container's log output.
type logStream interface {
	Next() (dockerapi.LogFrame, error)
	Close() error
}

// statsStream yields the resource-usage samples of a container.
type statsStream interface {
	Next() (dockerapi.Stats, error)
	Close() error
}

// dockerClient adapts the Engine client to dockerAPI.
type dockerClient struct{ *dockerapi.Client }

var _ dockerAPI = dockerClient{}

// PullImage starts a pull; a failure returns a nil interface, not the client's nil pointer inside one.
func (c dockerClient) PullImage(ctx context.Context, ref string) (pullStream, error) {
	s, err := c.Client.PullImage(ctx, ref)
	if err != nil {
		return nil, err
	}
	return s, nil
}

// Logs opens a container's log output; a failure returns a nil interface, not the client's nil pointer inside one.
func (c dockerClient) Logs(ctx context.Context, id string, o dockerapi.LogsOptions) (logStream, error) {
	s, err := c.Client.Logs(ctx, id, o)
	if err != nil {
		return nil, err
	}
	return s, nil
}

// Stats opens a container's usage samples; a failure returns a nil interface, not the client's nil pointer inside one.
func (c dockerClient) Stats(ctx context.Context, id string, stream bool) (statsStream, error) {
	s, err := c.Client.Stats(ctx, id, stream)
	if err != nil {
		return nil, err
	}
	return s, nil
}

// newDockerClient returns the Engine client for the socket; it opens no connection until a call.
func newDockerClient(socket string) dockerAPI { return dockerClient{dockerapi.New(socket)} }

// absentSocket is the socket a test binary dials instead of the host's. Test
// binaries that link the module, such as the controller's server tests, open a
// runtime that runs this module's preflight, and they must never reach the
// host's Docker daemon. A path that never exists makes every call fail as a
// missing socket.
const absentSocket = "/nonexistent/darkpanel/docker.sock"

// productionDK wires the module to the host's Docker daemon and file system.
func productionDK() dk {
	socket := dockerapi.DefaultSocket
	if testing.Testing() {
		socket = absentSocket
	}
	return dk{d: deps{
		docker:   newDockerClient(socket),
		lstat:    os.Lstat,
		readFile: os.ReadFile,
		secrets: func(context.Context, core.Env, string) ([][]byte, error) {
			return nil, nil
		},
	}}
}
