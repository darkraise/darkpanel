package platform

import (
	"context"
	"fmt"
	"strings"
	"syscall"

	"github.com/darkraise/darkpanel/internal/core"
)

// ServicesModule is the one module whose unit client drives units it does
// not declare (ADR 0020).
const ServicesModule = "services"

// adminUnits is the core.UnitAdmin of the services module: it reads every
// unit and drives only .service, .socket and .timer units whose names do not
// start with "darkpanel". The module's own rules refuse more; this is the
// bound that holds whatever they say.
type adminUnits struct{ u core.Units }

var _ core.UnitAdmin = adminUnits{}

// mutableUnit refuses method on unit unless the services module may drive it.
func mutableUnit(method, unit string) error {
	typ := unit[strings.LastIndexByte(unit, '.')+1:]
	if strings.HasPrefix(unit, "darkpanel") || (typ != "service" && typ != "socket" && typ != "timer") {
		return refuseAdmin(method, unit)
	}
	return nil
}

func (a adminUnits) admin(method string) (core.UnitAdmin, error) {
	if u, ok := a.u.(core.UnitAdmin); ok {
		return u, nil
	}
	return nil, fmt.Errorf("platform: the unit client of module %s cannot %s", ServicesModule, method)
}

func refuseAdmin(method, unit string) error {
	return fmt.Errorf("platform: module %s may not %s unit %s", ServicesModule, method, unit)
}

func (adminUnits) StartTransientService(_ context.Context, name string, _ core.TransientService) error {
	return refuseAdmin("StartTransientService", name)
}

func (adminUnits) StartTransientTimer(_ context.Context, name string, _ core.TransientTimer) error {
	return refuseAdmin("StartTransientTimer", name)
}

func (a adminUnits) Start(ctx context.Context, unit string) error {
	if err := mutableUnit("Start", unit); err != nil {
		return err
	}
	return a.u.Start(ctx, unit)
}

func (a adminUnits) Stop(ctx context.Context, unit string) error {
	if err := mutableUnit("Stop", unit); err != nil {
		return err
	}
	return a.u.Stop(ctx, unit)
}

func (a adminUnits) Restart(ctx context.Context, unit string) error {
	if err := mutableUnit("Restart", unit); err != nil {
		return err
	}
	return a.u.Restart(ctx, unit)
}

func (adminUnits) Kill(_ context.Context, unit string, _ syscall.Signal) error {
	return refuseAdmin("Kill", unit)
}

func (adminUnits) ResetFailed(_ context.Context, unit string) error {
	return refuseAdmin("ResetFailed", unit)
}

func (a adminUnits) Enable(ctx context.Context, unitFiles ...string) error {
	for _, f := range unitFiles {
		if err := mutableUnit("Enable", f); err != nil {
			return err
		}
	}
	return a.u.Enable(ctx, unitFiles...)
}

func (a adminUnits) Disable(ctx context.Context, unitFiles ...string) error {
	for _, f := range unitFiles {
		if err := mutableUnit("Disable", f); err != nil {
			return err
		}
	}
	return a.u.Disable(ctx, unitFiles...)
}

// Reload is the daemon-reload that systemctl runs after enabling or
// disabling a unit file.
func (a adminUnits) Reload(ctx context.Context) error { return a.u.Reload(ctx) }

func (a adminUnits) State(ctx context.Context, unit string) (core.UnitState, error) {
	return a.u.State(ctx, unit)
}

func (a adminUnits) List(ctx context.Context, pattern string) ([]core.UnitStatus, error) {
	return a.u.List(ctx, pattern)
}

func (a adminUnits) ReloadUnit(ctx context.Context, unit string) error {
	if err := mutableUnit("ReloadUnit", unit); err != nil {
		return err
	}
	u, err := a.admin("ReloadUnit")
	if err != nil {
		return err
	}
	return u.ReloadUnit(ctx, unit)
}

func (a adminUnits) ListAll(ctx context.Context) ([]core.UnitInfo, error) {
	u, err := a.admin("ListAll")
	if err != nil {
		return nil, err
	}
	return u.ListAll(ctx)
}

func (a adminUnits) Inspect(ctx context.Context, unit string) (core.UnitInfo, error) {
	u, err := a.admin("Inspect")
	if err != nil {
		return core.UnitInfo{}, err
	}
	return u.Inspect(ctx, unit)
}
