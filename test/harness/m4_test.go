//go:build harness

package harness

import (
	"context"
	"flag"
	"testing"
	"time"
)

var m4Flag = flag.Bool("m4", false, "run the M4 Docker module scenarios")

// m4Deadline bounds TestM4, a go test run of its own that needs -os, -m2 and -m4.
const m4Deadline = 60 * time.Minute

// m4Step is one scenario of TestM4; a failed gate step ends the run.
type m4Step struct {
	name string
	gate bool // later steps need this one
	run  func(t *testing.T)
}

// TestM4 is the Docker half of M4 (spec, Verification) on the enrolled agent
// of the M2 fleet, whose controller and agent run the harness build of this
// commit. The workflow selects it with -run and -m4.
func TestM4(t *testing.T) {
	if *osFlag == "" || *m2Flag == "" || !*m4Flag {
		t.Skip("no -os, -m2 or -m4 given: the VM tests run in .github/workflows/harness.yml")
	}
	ctx, cancel := context.WithTimeout(context.Background(), m4Deadline)
	defer cancel()
	f := bootFleet(ctx, t)
	serveReleases(ctx, t, f)
	steps := []m4Step{
		{"install", true, func(t *testing.T) { scenarioInstall(ctx, t, f) }},
		{"docker-ready", true, func(t *testing.T) { m4dReady(ctx, t, f) }},
		{"resources", false, func(t *testing.T) { m4dResources(ctx, t, f) }},
		{"container-lifecycle", false, func(t *testing.T) { m4dLifecycle(ctx, t, f) }},
		{"undo-conflict", false, func(t *testing.T) { m4dUndoConflict(ctx, t, f) }},
		{"inspect-and-streams", false, func(t *testing.T) { m4dInspect(ctx, t, f) }},
		{"container-remove", false, func(t *testing.T) { m4dRemove(ctx, t, f) }},
		{"images", false, func(t *testing.T) { m4dImages(ctx, t, f) }},
		{"networks-and-volumes", false, func(t *testing.T) { m4dNetworksVolumes(ctx, t, f) }},
		{"owned-refused", false, func(t *testing.T) { m4dOwned(ctx, t, f) }},
	}
	for _, s := range steps {
		if !t.Run(s.name, s.run) && s.gate {
			return
		}
	}
}
