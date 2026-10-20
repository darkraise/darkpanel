//go:build harnessbroken

package main

import (
	"testing"

	"github.com/darkraise/darkpanel/internal/cli"
)

func TestHarnessBroken(t *testing.T) {
	status, never := healthStatus, neverConnected
	if status != "degraded" || !never {
		t.Fatalf("a harnessbroken build has health status %q and NeverConnected %v, want degraded and true", status, never)
	}
	served := fakeServe(t, 0, nil)
	s := fakeAgent(t, 0)
	healthStatus, neverConnected = status, never
	if out, code := runArgs("serve", "--dev"); code != cli.ExitOK || len(*served) != 1 || (*served)[0].HealthStatus != "degraded" {
		t.Errorf("serve --dev: exit %d, %s; want it to pass HealthStatus degraded", code, out)
	}
	if out, code := runArgs("agent", "run", "--dev"); code != cli.ExitOK || len(s.runs) != 1 || !s.runs[0].NeverConnected {
		t.Errorf("agent run --dev: exit %d, %s; want it to pass NeverConnected", code, out)
	}
}
