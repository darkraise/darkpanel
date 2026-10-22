package platform

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/godbus/dbus/v5"

	"github.com/darkraise/darkpanel/internal/core"
)

const (
	systemdDest      = "org.freedesktop.systemd1"
	systemdPath      = dbus.ObjectPath("/org/freedesktop/systemd1")
	managerInterface = "org.freedesktop.systemd1.Manager"
	unitInterface    = "org.freedesktop.systemd1.Unit"
	jobInterface     = "org.freedesktop.systemd1.Job"
	unknownObject    = "org.freedesktop.DBus.Error.UnknownObject"
)

// Systemd is core.Units, the contract the lifecycle engine starts task units
// through; DBusSystemd implements it.
type Systemd = core.Units

// TransientService is core.TransientService.
type TransientService = core.TransientService

// TransientTimer is core.TransientTimer.
type TransientTimer = core.TransientTimer

// UnitState is core.UnitState.
type UnitState = core.UnitState

// busCaller calls one method of a systemd object, so tests can stand in for
// the system bus.
type busCaller interface {
	call(ctx context.Context, path dbus.ObjectPath, method string, args ...any) ([]any, error)
}

type connCaller struct{ conn *dbus.Conn }

func (c connCaller) call(ctx context.Context, path dbus.ObjectPath, method string, args ...any) ([]any, error) {
	call := c.conn.Object(systemdDest, path).CallWithContext(ctx, method, 0, args...)
	return call.Body, call.Err
}

// property, auxUnit and execCommand encode as the D-Bus structures
// StartTransientUnit takes: a(sv), a(sa(sv)) and ExecStart's a(sasb).
type property struct {
	Name  string
	Value dbus.Variant
}

type auxUnit struct {
	Name  string
	Props []property
}

type execCommand struct {
	Path          string
	Args          []string
	IgnoreFailure bool
}

// DBusSystemd is the Systemd of a host, over the system bus.
type DBusSystemd struct {
	bus  busCaller
	conn *dbus.Conn
}

var _ Systemd = (*DBusSystemd)(nil)

// NewSystemd connects to the system bus and checks that systemd answers on
// it. Call Close when done.
func NewSystemd(ctx context.Context) (*DBusSystemd, error) {
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return nil, fmt.Errorf("platform: connect to the system bus: %w", err)
	}
	s := &DBusSystemd{bus: connCaller{conn}, conn: conn}
	if _, err := s.bus.call(ctx, systemdPath, "org.freedesktop.DBus.Peer.Ping"); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("platform: systemd does not answer on the system bus: %w", err)
	}
	return s, nil
}

func newSystemd(bus busCaller) *DBusSystemd { return &DBusSystemd{bus: bus} }

// Close closes the bus connection.
func (s *DBusSystemd) Close() error { return s.conn.Close() }

func (s *DBusSystemd) manager(ctx context.Context, what, method string, args ...any) ([]any, error) {
	body, err := s.bus.call(ctx, systemdPath, managerInterface+"."+method, args...)
	if err != nil {
		return nil, fmt.Errorf("platform: %s: %w", what, err)
	}
	return body, nil
}

// StartTransientService starts name, which must end in .service. The
// "fail" mode refuses the job when name is already loaded, so a stale unit
// of the same name is never reused.
func (s *DBusSystemd) StartTransientService(ctx context.Context, name string, svc TransientService) error {
	what := "start " + name
	props, err := serviceProps(svc)
	if err == nil && !strings.HasSuffix(name, ".service") {
		err = errors.New("the unit name must end in .service")
	}
	if err != nil {
		return fmt.Errorf("platform: %s: %w", what, err)
	}
	_, err = s.manager(ctx, what, "StartTransientUnit", name, "fail", props, []auxUnit{})
	return err
}

// StartTransientTimer starts name, which must end in .timer, together with
// the service it starts.
func (s *DBusSystemd) StartTransientTimer(ctx context.Context, name string, t TransientTimer) error {
	what := "start " + name
	base, ok := strings.CutSuffix(name, ".timer")
	svcProps, err := serviceProps(t.Service)
	switch {
	case err != nil:
	case !ok:
		err = errors.New("the unit name must end in .timer")
	case t.OnActive <= 0:
		err = errors.New("the timer needs a positive delay")
	case t.Accuracy < 0:
		err = errors.New("the timer's accuracy is negative")
	}
	if err != nil {
		return fmt.Errorf("platform: %s: %w", what, err)
	}
	props := []property{
		{"Description", dbus.MakeVariant(t.Description)},
		// systemd takes both in microseconds.
		{"OnActiveSec", dbus.MakeVariant(uint64(t.OnActive.Microseconds()))},  //nolint:gosec // G115: positive, checked above
		{"AccuracyUSec", dbus.MakeVariant(uint64(t.Accuracy.Microseconds()))}, //nolint:gosec // G115: non-negative, checked above
	}
	aux := []auxUnit{{Name: base + ".service", Props: svcProps}}
	_, err = s.manager(ctx, what, "StartTransientUnit", name, "fail", props, aux)
	return err
}

func serviceProps(svc TransientService) ([]property, error) {
	switch {
	case len(svc.ExecStart) == 0 || !filepath.IsAbs(svc.ExecStart[0]):
		return nil, errors.New("ExecStart must start with an absolute program path")
	case svc.RuntimeMax < 0:
		return nil, errors.New("the service's maximum runtime is negative")
	}
	props := []property{
		{"Description", dbus.MakeVariant(svc.Description)},
		{"ExecStart", dbus.MakeVariant([]execCommand{{Path: svc.ExecStart[0], Args: slices.Clone(svc.ExecStart)}})},
	}
	if len(svc.OnFailure) > 0 {
		props = append(props, property{"OnFailure", dbus.MakeVariant(slices.Clone(svc.OnFailure))})
	}
	if svc.RuntimeMax > 0 {
		props = append(props, property{"RuntimeMaxUSec", dbus.MakeVariant(uint64(svc.RuntimeMax.Microseconds()))}) //nolint:gosec // G115: positive, checked above
	}
	if svc.CollectMode != "" {
		props = append(props, property{"CollectMode", dbus.MakeVariant(svc.CollectMode)})
	}
	return props, nil
}

// Start queues a start job for unit.
func (s *DBusSystemd) Start(ctx context.Context, unit string) error {
	_, err := s.manager(ctx, "start "+unit, "StartUnit", unit, "replace")
	return err
}

// Stop queues a stop job for unit.
func (s *DBusSystemd) Stop(ctx context.Context, unit string) error {
	_, err := s.manager(ctx, "stop "+unit, "StopUnit", unit, "replace")
	return err
}

// Restart queues a restart job for unit.
func (s *DBusSystemd) Restart(ctx context.Context, unit string) error {
	_, err := s.manager(ctx, "restart "+unit, "RestartUnit", unit, "replace")
	return err
}

// Kill sends sig to every process of unit.
func (s *DBusSystemd) Kill(ctx context.Context, unit string, sig syscall.Signal) error {
	_, err := s.manager(ctx, "kill "+unit, "KillUnit", unit, "all", int32(sig)) //nolint:gosec // G115: signal numbers are below 65
	return err
}

// ResetFailed clears unit's failed state and start-limit counter, which a
// crash-looping unit needs before it starts again.
func (s *DBusSystemd) ResetFailed(ctx context.Context, unit string) error {
	_, err := s.manager(ctx, "reset-failed "+unit, "ResetFailedUnit", unit)
	return err
}

// Enable enables unit files in /etc, as systemctl enable does.
func (s *DBusSystemd) Enable(ctx context.Context, unitFiles ...string) error {
	_, err := s.manager(ctx, "enable "+strings.Join(unitFiles, " "), "EnableUnitFiles", slices.Clone(unitFiles), false, false)
	return err
}

// Disable disables unit files in /etc, as systemctl disable does.
func (s *DBusSystemd) Disable(ctx context.Context, unitFiles ...string) error {
	_, err := s.manager(ctx, "disable "+strings.Join(unitFiles, " "), "DisableUnitFiles", slices.Clone(unitFiles), false)
	return err
}

// Reload reloads every unit file, as systemctl daemon-reload does.
func (s *DBusSystemd) Reload(ctx context.Context) error {
	_, err := s.manager(ctx, "daemon-reload", "Reload")
	return err
}

// unitJob is a unit's Job property, D-Bus (uo); id 0 means no job is queued.
type unitJob struct {
	ID   uint32
	Path dbus.ObjectPath
}

// State reports unit's load, active and sub state and its queued job.
// LoadUnit, unlike GetUnit, answers for a unit that is not loaded, so a
// missing unit reports LoadState "not-found" instead of an error.
func (s *DBusSystemd) State(ctx context.Context, unit string) (UnitState, error) {
	what := "state of " + unit
	body, err := s.manager(ctx, what, "LoadUnit", unit)
	if err != nil {
		return UnitState{}, err
	}
	var path dbus.ObjectPath
	if err := dbus.Store(body, &path); err != nil {
		return UnitState{}, fmt.Errorf("platform: %s: %w", what, err)
	}
	var st UnitState
	var job unitJob
	for _, p := range []struct {
		name string
		dst  any
	}{{"LoadState", &st.LoadState}, {"ActiveState", &st.ActiveState}, {"SubState", &st.SubState}, {"Job", &job}} {
		if err := s.property(ctx, path, unitInterface, p.name, p.dst); err != nil {
			return UnitState{}, fmt.Errorf("platform: %s: read %s: %w", what, p.name, err)
		}
	}
	if job.ID == 0 {
		return st, nil
	}
	err = s.property(ctx, job.Path, jobInterface, "JobType", &st.PendingJob)
	var dbusErr dbus.Error
	if errors.As(err, &dbusErr) && dbusErr.Name == unknownObject {
		// The job finished between the two reads, so none is queued now.
		return st, nil
	}
	if err != nil {
		return UnitState{}, fmt.Errorf("platform: %s: read the type of job %d: %w", what, job.ID, err)
	}
	return st, nil
}

// property reads one D-Bus property of the object at path into dst.
func (s *DBusSystemd) property(ctx context.Context, path dbus.ObjectPath, iface, name string, dst any) error {
	body, err := s.bus.call(ctx, path, "org.freedesktop.DBus.Properties.Get", iface, name)
	if err != nil {
		return err
	}
	var v dbus.Variant
	if err := dbus.Store(body, &v); err != nil {
		return err
	}
	return v.Store(dst)
}
