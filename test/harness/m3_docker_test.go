//go:build harness

package harness

import (
	"context"
	"testing"
	"time"
)

// m3dDockDeadline bounds TestM3Docker, a go test run of its own that shares
// TestM3Firewall's -m3 flag.
const m3dDockDeadline = 60 * time.Minute

// TestM3Docker is the Docker half of M3 (spec, Verification): the integration's
// lifecycle and refusals on the M2 fleet, whose controller and agent run the
// harness build of this commit. The workflow selects it with -run.
func TestM3Docker(t *testing.T) {
	if *osFlag == "" || *m2Flag == "" || !*m3Flag {
		t.Skip("no -os, -m2 or -m3 given: the VM tests run in .github/workflows/harness.yml")
	}
	ctx, cancel := context.WithTimeout(context.Background(), m3dDockDeadline)
	defer cancel()
	f := bootFleet(ctx, t)
	serveReleases(ctx, t, f)
	steps := []struct {
		name string
		gate bool // later steps need this one
		run  func(t *testing.T)
	}{
		{"install", true, func(t *testing.T) { scenarioInstall(ctx, t, f) }},
		{"firewall-ready", true, func(t *testing.T) { m3Ready(ctx, t, f) }},
		{"docker-detected", true, func(t *testing.T) { m3dDockDetect(ctx, t, f) }},
		{"refuse-foreign-docker-user", false, func(t *testing.T) { m3dDockRefuseUser(ctx, t, f) }},
		{"foreign-after-enable", false, func(t *testing.T) { m3dDockForeignAfterEnable(ctx, t, f) }},
		{"foreign-forward", false, func(t *testing.T) { m3dDockForeignForward(ctx, t, f) }},
		{"refuse-exemption-overlap", false, func(t *testing.T) { m3dDockExemptionOverlap(ctx, t, f) }},
		{"refuse-open-incoming", false, func(t *testing.T) { m3dDockOpenIncoming(ctx, t, f) }},
		{"disable-cleanup", false, func(t *testing.T) { m3dDockDisableCleanup(ctx, t, f) }},
		{"routes-survive", false, func(t *testing.T) { m3dDockRoutesSurvive(ctx, t, f) }},
		{"foreign-in-confirmation-window", false, func(t *testing.T) { m3dDockForeignWindow(ctx, t, f) }},
		{"foreign-in-interrupted-cleanup", false, func(t *testing.T) { m3dDockForeignCleanup(ctx, t, f) }},
		{"refuse-ipv6no-docker-rules", false, func(t *testing.T) { m3dDockIPv6NoRules(ctx, t, f) }},
		{"traffic-unallowed-closed", false, func(t *testing.T) { m3dTrafficUnallowed(ctx, t, f) }},
		{"traffic-new-network", false, func(t *testing.T) { m3dTrafficNewNetwork(ctx, t, f) }},
		{"traffic-container-to-container", false, func(t *testing.T) { m3dTrafficContainers(ctx, t, f) }},
		{"traffic-disjoint-bindings", false, func(t *testing.T) { m3dTrafficDisjoint(ctx, t, f) }},
		{"traffic-deny-keeps-allow", false, func(t *testing.T) { m3dTrafficDenyKeeps(ctx, t, f) }},
		{"traffic-proxied-ipv6", false, func(t *testing.T) { m3dTrafficProxied(ctx, t, f) }},
		{"traffic-recreate-under-scan", false, func(t *testing.T) { m3dTrafficRecreate(ctx, t, f) }},
	}
	for _, s := range steps {
		if !t.Run(s.name, s.run) && s.gate {
			return
		}
	}
}
