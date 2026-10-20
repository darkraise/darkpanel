//go:build harnesscrash

package main

import (
	"os"
	"testing"

	"github.com/darkraise/darkpanel/internal/cli"
	"github.com/darkraise/darkpanel/internal/update/trustedkeys"
)

func TestHarnessCrash(t *testing.T) {
	if !crashAtStart {
		t.Fatal("a harnesscrash build does not crash at start")
	}
	served := fakeServe(t, 0, nil)
	s := fakeAgent(t, 0)
	crashAtStart = true
	for _, args := range [][]string{{"serve", "--dev"}, {"agent", "run", "--dev"}} {
		if out, code := runArgs(args...); code != cli.ExitError || out != "darkpanel: harnesscrash: exiting at start\n" {
			t.Errorf("darkpanel %v: exit %d, output %q; want exit 1 and the harnesscrash error", args, code, out)
		}
	}
	if len(*served) != 0 || len(s.runs) != 0 || len(s.updates) != 0 {
		t.Errorf("the crashing build served %d times, ran %d agents and opened %d updaters", len(*served), len(s.runs), len(s.updates))
	}
	if _, err := os.Stat(s.dev.AgentDir); !os.IsNotExist(err) {
		t.Errorf("agent run created %s before crashing: %v", s.dev.AgentDir, err)
	}
	withTrustedKeys(t, []trustedkeys.Key{{ID: "BCB10AB3E9AF694E", PublicKey: "RWROaa/pswqxvLcQvIucxc5teWzMb2E2rinAmlfu9fYXug3dfuI81MvD"}})
	if out, code := runArgs("selftest"); code != cli.ExitOK || out != "selftest: ok\n" {
		t.Errorf("selftest: exit %d, output %q; want it to pass in a harnesscrash build", code, out)
	}
}
