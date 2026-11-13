package panel

import (
	"context"
	"errors"
	"testing"

	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

// TestFdProductionAgentDialsNoDaemon: in a test binary the production dependencies dial a socket that never exists, so
// a test that reaches them cannot touch the host's Docker daemon.
func TestFdProductionAgentDialsNoDaemon(t *testing.T) {
	d := productionAgent()
	if d.routes == nil || d.interfaces == nil {
		t.Fatal("productionAgent does not read the host's routes and interface addresses")
	}
	docker, err := d.docker()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := docker.InspectNetwork(context.Background(), NetworkName); !errors.Is(err, dockerapi.ErrUnavailable) {
		t.Errorf("InspectNetwork error = %v, want dockerapi.ErrUnavailable", err)
	}
}
