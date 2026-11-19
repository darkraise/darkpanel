package platform

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	logindDest = "org.freedesktop.login1"
	logindPath = "/org/freedesktop/login1"
)

// Reboot schedules a reboot of the host.
type Reboot interface {
	ScheduleReboot(ctx context.Context, at time.Time) error
}

// LogindError is logind's refusal to schedule a reboot, such as an
// inhibitor lock or a shutdown already scheduled; Reason is logind's text.
type LogindError struct {
	Reason string
}

func (e *LogindError) Error() string { return "logind refused the reboot: " + e.Reason }

// logindBus calls one method of a logind object, so tests can stand in for
// the system bus.
type logindBus interface {
	Call(ctx context.Context, dest, path, method string, args ...any) error
}

// LogindReboot is the Reboot of a host, through logind on the system bus.
type LogindReboot struct {
	bus logindBus
}

var _ Reboot = (*LogindReboot)(nil)

// NewReboot returns the host's Reboot. It connects to the system bus for
// each call: a reboot is rare, and the agent keeps no connection open for it.
func NewReboot() *LogindReboot { return newLogindReboot(systemBus{}) }

func newLogindReboot(bus logindBus) *LogindReboot { return &LogindReboot{bus: bus} }

// ScheduleReboot asks logind to reboot the host at at. A refusal is a
// *LogindError; a bus that cannot be reached is any other error.
func (r *LogindReboot) ScheduleReboot(ctx context.Context, at time.Time) error {
	err := r.bus.Call(ctx, logindDest, logindPath, logindDest+".Manager.ScheduleShutdown", "reboot", uint64(at.UnixMicro()))
	var dbusErr dbus.Error
	if errors.As(err, &dbusErr) {
		reason := dbusErr.Name
		if len(dbusErr.Body) > 0 {
			if s, ok := dbusErr.Body[0].(string); ok && s != "" {
				reason = s
			}
		}
		return &LogindError{Reason: reason}
	}
	if err != nil {
		return fmt.Errorf("platform: schedule a reboot: %w", err)
	}
	return nil
}

// systemBus is a logindBus over a fresh system bus connection per call.
type systemBus struct{}

func (systemBus) Call(ctx context.Context, dest, path, method string, args ...any) error {
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return fmt.Errorf("connect to the system bus: %w", err)
	}
	defer func() { _ = conn.Close() }()
	return conn.Object(dest, dbus.ObjectPath(path)).CallWithContext(ctx, method, 0, args...).Err
}
