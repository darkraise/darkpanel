package platform

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

type logindCall struct {
	dest, path, method string
	args               []any
}

// fakeLogindBus is a logindBus that records its calls and answers err.
type fakeLogindBus struct {
	calls []logindCall
	err   error
}

func (b *fakeLogindBus) Call(_ context.Context, dest, path, method string, args ...any) error {
	b.calls = append(b.calls, logindCall{dest, path, method, args})
	return b.err
}

func dbusError(name, body string) error { return dbus.Error{Name: name, Body: []any{body}} }

func TestScheduleRebootCallsLogind(t *testing.T) {
	bus := &fakeLogindBus{}
	r := newLogindReboot(bus)
	at := time.Date(2026, 11, 19, 12, 0, 15, 0, time.UTC)
	if err := r.ScheduleReboot(context.Background(), at); err != nil {
		t.Fatal(err)
	}
	c := bus.calls[0]
	if c.dest != "org.freedesktop.login1" || c.method != "org.freedesktop.login1.Manager.ScheduleShutdown" || c.args[0] != "reboot" || c.args[1] != uint64(at.UnixMicro()) {
		t.Fatalf("call = %+v", c)
	}
	if c.path != "/org/freedesktop/login1" || len(bus.calls) != 1 {
		t.Fatalf("calls = %+v", bus.calls)
	}
}

func TestScheduleRebootReturnsLogindReason(t *testing.T) {
	bus := &fakeLogindBus{err: dbusError("org.freedesktop.login1.OperationInProgress", "shutdown blocked by inhibitor")}
	err := newLogindReboot(bus).ScheduleReboot(context.Background(), time.Now())
	var le *LogindError
	if !errors.As(err, &le) || le.Reason != "shutdown blocked by inhibitor" {
		t.Fatalf("err = %v", err)
	}
}

func TestScheduleRebootKeepsTransportErrors(t *testing.T) {
	cause := errors.New("no system bus")
	err := newLogindReboot(&fakeLogindBus{err: cause}).ScheduleReboot(context.Background(), time.Now())
	var le *LogindError
	if errors.As(err, &le) || !errors.Is(err, cause) {
		t.Fatalf("err = %v; want the bus error, not a logind refusal", err)
	}
}
