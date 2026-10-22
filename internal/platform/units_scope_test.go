package platform

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)

// scpUnits is a core.Units that records "<Method> <args>" for every call
// and answers success.
type scpUnits struct {
	mu    sync.Mutex
	calls []string
}

func (u *scpUnits) record(method string, args ...string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.calls = append(u.calls, strings.TrimSpace(method+" "+strings.Join(args, " ")))
}

func (u *scpUnits) recorded() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return slices.Clone(u.calls)
}

func (u *scpUnits) StartTransientService(_ context.Context, name string, _ core.TransientService) error {
	u.record("StartTransientService", name)
	return nil
}

func (u *scpUnits) StartTransientTimer(_ context.Context, name string, _ core.TransientTimer) error {
	u.record("StartTransientTimer", name)
	return nil
}

func (u *scpUnits) Start(_ context.Context, unit string) error { u.record("Start", unit); return nil }
func (u *scpUnits) Stop(_ context.Context, unit string) error  { u.record("Stop", unit); return nil }
func (u *scpUnits) Restart(_ context.Context, unit string) error {
	u.record("Restart", unit)
	return nil
}

func (u *scpUnits) Kill(_ context.Context, unit string, sig syscall.Signal) error {
	u.record("Kill", unit, fmt.Sprint(int(sig)))
	return nil
}

func (u *scpUnits) ResetFailed(_ context.Context, unit string) error {
	u.record("ResetFailed", unit)
	return nil
}

func (u *scpUnits) Enable(_ context.Context, unitFiles ...string) error {
	u.record("Enable", unitFiles...)
	return nil
}

func (u *scpUnits) Disable(_ context.Context, unitFiles ...string) error {
	u.record("Disable", unitFiles...)
	return nil
}

func (u *scpUnits) Reload(context.Context) error { u.record("Reload"); return nil }

func (u *scpUnits) State(_ context.Context, unit string) (core.UnitState, error) {
	u.record("State", unit)
	return core.UnitState{LoadState: "loaded", ActiveState: "active"}, nil
}

func (u *scpUnits) List(_ context.Context, pattern string) ([]core.UnitStatus, error) {
	u.record("List", pattern)
	return nil, nil
}

const scpUnitPath = "/etc/systemd/system/darkpanel-derp.service"

// scpDerp is module derp with its own unit and, optionally, external units.
func scpDerp(ownsUnit bool, external ...core.ExternalUnit) core.Manifest {
	m := core.Manifest{ID: "derp", Version: 1, ExternalUnits: external}
	if ownsUnit {
		m.OwnedPaths = []core.OwnedPath{{Path: scpUnitPath, Kind: core.OwnedUnit}}
	}
	return m
}

// scpCall is one call of a scoped client and the call its fake must see,
// "" when the call is refused.
type scpCall struct {
	name string
	run  func(ctx context.Context, u core.Units) error
	want string
}

func scpRun(t *testing.T, m core.Manifest, calls []scpCall, refusal func(c scpCall) string) {
	t.Helper()
	for _, c := range calls {
		t.Run(c.name, func(t *testing.T) {
			fake := &scpUnits{}
			err := c.run(context.Background(), ScopeUnits(fake, m))
			if c.want != "" {
				if got := fake.recorded(); err != nil || !slices.Equal(got, []string{c.want}) {
					t.Errorf("err %v, calls %q; want the call %q", err, got, c.want)
				}
				return
			}
			if want := refusal(c); err == nil || err.Error() != want {
				t.Errorf("err %v, want %q", err, want)
			}
			if got := fake.recorded(); len(got) != 0 {
				t.Errorf("a refused call reached the client: %q", got)
			}
		})
	}
}

func scpState(unit string) func(context.Context, core.Units) error {
	return func(ctx context.Context, u core.Units) error {
		_, err := u.State(ctx, unit)
		return err
	}
}

func TestScopeOwnedUnits(t *testing.T) {
	const unit = "darkpanel-derp.service"
	scpRun(t, scpDerp(true), []scpCall{
		{"start", func(ctx context.Context, u core.Units) error { return u.Start(ctx, unit) }, "Start " + unit},
		{"stop", func(ctx context.Context, u core.Units) error { return u.Stop(ctx, unit) }, "Stop " + unit},
		{"restart", func(ctx context.Context, u core.Units) error { return u.Restart(ctx, unit) }, "Restart " + unit},
		{"state", scpState(unit), "State " + unit},
		{"kill", func(ctx context.Context, u core.Units) error { return u.Kill(ctx, unit, syscall.SIGTERM) }, "Kill " + unit + " 15"},
		{"reset failed", func(ctx context.Context, u core.Units) error { return u.ResetFailed(ctx, unit) }, "ResetFailed " + unit},
		{"enable by name", func(ctx context.Context, u core.Units) error { return u.Enable(ctx, unit) }, "Enable " + unit},
		{"enable by path", func(ctx context.Context, u core.Units) error { return u.Enable(ctx, scpUnitPath) }, "Enable " + scpUnitPath},
		{"disable by name", func(ctx context.Context, u core.Units) error { return u.Disable(ctx, unit) }, "Disable " + unit},
		{"disable by path", func(ctx context.Context, u core.Units) error { return u.Disable(ctx, scpUnitPath) }, "Disable " + scpUnitPath},
		{"reload", func(ctx context.Context, u core.Units) error { return u.Reload(ctx) }, "Reload"},
	}, nil)
}

func TestScopeTaskUnits(t *testing.T) {
	const unit = "darkpanel-task-01ABC-apply-1.service"
	scpRun(t, scpDerp(false), []scpCall{
		{"state", scpState(unit), "State " + unit},
		{"stop", func(ctx context.Context, u core.Units) error { return u.Stop(ctx, unit) }, ""},
	}, func(scpCall) string { return "platform: module derp may not Stop unit " + unit })
}

func TestScopeExternalUnits(t *testing.T) {
	const unit = "tailscaled.service"
	m := scpDerp(false, core.ExternalUnit{Name: unit, Ops: []string{core.UnitOpState, core.UnitOpRestart}})
	methods := map[string]string{"stop": "Stop", "start": "Start", "kill": "Kill"}
	scpRun(t, m, []scpCall{
		{"state", scpState(unit), "State " + unit},
		{"restart", func(ctx context.Context, u core.Units) error { return u.Restart(ctx, unit) }, "Restart " + unit},
		{"stop", func(ctx context.Context, u core.Units) error { return u.Stop(ctx, unit) }, ""},
		{"start", func(ctx context.Context, u core.Units) error { return u.Start(ctx, unit) }, ""},
		{"kill", func(ctx context.Context, u core.Units) error { return u.Kill(ctx, unit, syscall.SIGTERM) }, ""},
	}, func(c scpCall) string { return "platform: module derp may not " + methods[c.name] + " unit " + unit })
}

func TestScopeRefusals(t *testing.T) {
	const other = "nginx.service"
	want := map[string]string{
		"list":            "platform: module derp may not List unit darkpanel-*",
		"transient":       "platform: module derp may not StartTransientService unit darkpanel-derp-x.service",
		"timer":           "platform: module derp may not StartTransientTimer unit darkpanel-derp-x.timer",
		"start":           "platform: module derp may not Start unit " + other,
		"stop":            "platform: module derp may not Stop unit " + other,
		"restart":         "platform: module derp may not Restart unit " + other,
		"state":           "platform: module derp may not State unit " + other,
		"kill":            "platform: module derp may not Kill unit " + other,
		"reset failed":    "platform: module derp may not ResetFailed unit " + other,
		"enable":          "platform: module derp may not Enable unit " + other,
		"disable a path":  "platform: module derp may not Disable unit /etc/systemd/system/" + other,
		"enable two":      "platform: module derp may not Enable unit " + other,
		"reload unowned":  "platform: module derp may not Reload units: it declares no unit",
		"a lookalike":     "platform: module derp may not State unit darkpanel-task-01ABC-apply-1.timer",
		"a renamed owned": "platform: module derp may not Start unit darkpanel-derp.service.d",
	}
	refusal := func(c scpCall) string { return want[c.name] }
	scpRun(t, scpDerp(true), []scpCall{
		{"list", func(ctx context.Context, u core.Units) error {
			_, err := u.List(ctx, "darkpanel-*")
			return err
		}, ""},
		{"transient", func(ctx context.Context, u core.Units) error {
			return u.StartTransientService(ctx, "darkpanel-derp-x.service", core.TransientService{ExecStart: []string{"/bin/true"}})
		}, ""},
		{"timer", func(ctx context.Context, u core.Units) error {
			return u.StartTransientTimer(ctx, "darkpanel-derp-x.timer", core.TransientTimer{})
		}, ""},
		{"start", func(ctx context.Context, u core.Units) error { return u.Start(ctx, other) }, ""},
		{"stop", func(ctx context.Context, u core.Units) error { return u.Stop(ctx, other) }, ""},
		{"restart", func(ctx context.Context, u core.Units) error { return u.Restart(ctx, other) }, ""},
		{"state", scpState(other), ""},
		{"kill", func(ctx context.Context, u core.Units) error { return u.Kill(ctx, other, syscall.SIGKILL) }, ""},
		{"reset failed", func(ctx context.Context, u core.Units) error { return u.ResetFailed(ctx, other) }, ""},
		{"enable", func(ctx context.Context, u core.Units) error { return u.Enable(ctx, other) }, ""},
		{"disable a path", func(ctx context.Context, u core.Units) error {
			return u.Disable(ctx, "/etc/systemd/system/"+other)
		}, ""},
		{"enable two", func(ctx context.Context, u core.Units) error {
			return u.Enable(ctx, "darkpanel-derp.service", other)
		}, ""},
		{"a lookalike", scpState("darkpanel-task-01ABC-apply-1.timer"), ""},
		{"a renamed owned", func(ctx context.Context, u core.Units) error { return u.Start(ctx, "darkpanel-derp.service.d") }, ""},
	}, refusal)
	scpRun(t, scpDerp(false), []scpCall{
		{"reload unowned", func(ctx context.Context, u core.Units) error { return u.Reload(ctx) }, ""},
	}, refusal)
}

// scpModule registers a manifest as it is.
type scpModule core.Manifest

func (m scpModule) Manifest() core.Manifest { return core.Manifest(m) }

func TestModuleUnits(t *testing.T) {
	reg, err := core.NewRegistry(scpModule(scpDerp(true)))
	if err != nil {
		t.Fatal(err)
	}
	fake := &scpUnits{}
	units := ModuleUnits(fake, reg)
	if got := units(core.ModuleIDCore); got != core.Units(fake) {
		t.Errorf("the core module got %T, want the client itself", got)
	}
	derp := units("derp")
	if _, ok := derp.(*scopedUnits); !ok {
		t.Fatalf("module derp got %T, want a scoped client", derp)
	}
	if err := derp.Start(context.Background(), "nginx.service"); err == nil {
		t.Error("module derp started nginx.service")
	}
	if got := units("nope"); got != nil {
		t.Errorf("an unknown module got %T, want nil", got)
	}
	if got := ModuleUnits(nil, reg)(core.ModuleIDCore); got != nil {
		t.Errorf("without a client the core module got %T, want nil", got)
	}
}
