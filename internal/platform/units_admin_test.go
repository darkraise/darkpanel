package platform

import (
	"context"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)

// adminFake is scpUnits with the reads and reload of core.UnitAdmin.
type adminFake struct{ scpUnits }

func (u *adminFake) ReloadUnit(_ context.Context, unit string) error {
	u.record("ReloadUnit", unit)
	return nil
}

func (u *adminFake) ListAll(context.Context) ([]core.UnitInfo, error) {
	u.record("ListAll")
	return nil, nil
}

func (u *adminFake) Inspect(_ context.Context, unit string) (core.UnitInfo, error) {
	u.record("Inspect", unit)
	return core.UnitInfo{ID: unit}, nil
}

func TestUnitScopeOfServices(t *testing.T) {
	ctx := context.Background()
	fake := &adminFake{}
	s, ok := ScopeUnits(fake, core.Manifest{ID: ServicesModule}).(core.UnitAdmin)
	if !ok {
		t.Fatal("the services module's unit client is not a core.UnitAdmin")
	}
	for _, unit := range []string{"nginx.service", "ssh.socket", "apt-daily.timer"} {
		for name, call := range map[string]func() error{
			"Start": func() error { return s.Start(ctx, unit) }, "Stop": func() error { return s.Stop(ctx, unit) },
			"Restart": func() error { return s.Restart(ctx, unit) }, "ReloadUnit": func() error { return s.ReloadUnit(ctx, unit) },
			"Enable": func() error { return s.Enable(ctx, unit) }, "Disable": func() error { return s.Disable(ctx, unit) },
		} {
			if err := call(); err != nil {
				t.Errorf("%s %s: %v", name, unit, err)
			}
		}
	}
	for _, unit := range []string{"darkpanel-agent.service", "darkpanel-job-x.timer", "/lib/systemd/system/darkpanel-agent.service", "home.mount", "multi-user.target", "nginx"} {
		for name, call := range map[string]func() error{
			"Start": func() error { return s.Start(ctx, unit) }, "Stop": func() error { return s.Stop(ctx, unit) },
			"Restart": func() error { return s.Restart(ctx, unit) }, "ReloadUnit": func() error { return s.ReloadUnit(ctx, unit) },
			"Enable": func() error { return s.Enable(ctx, unit) }, "Disable": func() error { return s.Disable(ctx, unit) },
			"Kill":                  func() error { return s.Kill(ctx, unit, syscall.SIGKILL) },
			"ResetFailed":           func() error { return s.ResetFailed(ctx, unit) },
			"StartTransientService": func() error { return s.StartTransientService(ctx, unit, core.TransientService{}) },
		} {
			if err := call(); err == nil || !strings.Contains(err.Error(), "module services may not "+name+" unit "+unit) {
				t.Errorf("%s %s: %v, want a refusal", name, unit, err)
			}
		}
	}
	before := len(fake.recorded())
	if _, err := s.Inspect(ctx, "darkpanel-agent.service"); err != nil {
		t.Errorf("Inspect of the agent's unit: %v", err)
	}
	if err := s.Reload(ctx); err != nil {
		t.Errorf("daemon-reload: %v", err)
	}
	if got := fake.recorded()[before:]; !slices.Equal(got, []string{"Inspect darkpanel-agent.service", "Reload"}) {
		t.Errorf("reads reached %v", got)
	}
	if _, err := ScopeUnits(&fake.scpUnits, core.Manifest{ID: ServicesModule}).(core.UnitAdmin).Inspect(ctx, "nginx.service"); err == nil {
		t.Error("Inspect over a client without reads succeeded")
	}
}
