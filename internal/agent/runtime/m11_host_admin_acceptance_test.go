package runtime

import (
	"context"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

type acceptShell struct{ starts int }

func (c *acceptShell) Start(context.Context, uint16, uint16) (core.ShellSession, error) {
	c.starts++
	return nil, nil
}

const acceptPolicyReason = "Turned off on this host by its policy file"

func TestA14_NoShellWhileTheAgentsOwnFlagIsOff(t *testing.T) {
	ctx := context.Background()
	f, _, _ := policyFixture(t, "")
	sh := &acceptShell{}
	f.shell = sh
	f.restart()
	guard := shellGuard{r: f.rt, module: "console"}
	// A module is off until its flag is set; the controller's request to open a terminal reaches the guard whatever it says.
	if err := f.rt.SetModuleEnabled(ctx, "console", false); err != nil {
		t.Fatal(err)
	}
	if _, err := guard.Start(ctx, 80, 24); errcode.From(err).Code != errcode.ModuleUnavailable {
		t.Errorf("a shell with the flag off: %v; want module.unavailable", err)
	}
	if sh.starts != 0 {
		t.Errorf("the shell started %d times with the flag off", sh.starts)
	}
	if err := f.rt.SetModuleEnabled(ctx, "console", true); err != nil {
		t.Fatal(err)
	}
	if _, err := guard.Start(ctx, 80, 24); err != nil {
		t.Errorf("a shell with the flag on: %v", err)
	}
	if sh.starts != 1 {
		t.Errorf("the shell started %d times with the flag on; want 1", sh.starts)
	}
}

func TestA102_PolicyFileDeniesConsoleAndJobsWhateverTheControllerPushes(t *testing.T) {
	ctx := context.Background()
	f, console, jobs := policyFixture(t, `{"denyModules":["console","jobs"]}`)
	sh := &acceptShell{}
	f.shell = sh
	f.restart()
	for _, id := range []string{"console", "jobs"} {
		st := f.helloStatus(id)
		if st.Available || st.Reason != acceptPolicyReason {
			t.Errorf("%s: available %v, reason %q; want unavailable with %q", id, st.Available, st.Reason, acceptPolicyReason)
		}
		if err := f.rt.SetModuleEnabled(ctx, id, true); errcode.From(err).Code != errcode.ModuleUnavailable {
			t.Errorf("the controller pushed flag 1 for %s: %v; want it refused", id, err)
		}
	}
	if _, err := (shellGuard{r: f.rt, module: "console"}).Start(ctx, 80, 24); err == nil {
		t.Error("a shell started with console denied")
	}
	if sh.starts != 0 {
		t.Errorf("the shell started %d times", sh.starts)
	}
	for _, calls := range []*[]bool{console, jobs} {
		for _, on := range *calls {
			if on {
				t.Error("a denied module was told it is enabled")
			}
		}
	}
}
