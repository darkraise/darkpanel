package platform

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

// busCall is one method call the fake bus received.
type busCall struct {
	path   dbus.ObjectPath
	method string
	args   []any
}

// fakeBus records every call and answers with reply, which returns the
// call's body or an error.
type fakeBus struct {
	calls []busCall
	reply func(c busCall) ([]any, error)
}

func (f *fakeBus) call(_ context.Context, path dbus.ObjectPath, method string, args ...any) ([]any, error) {
	c := busCall{path: path, method: method, args: args}
	f.calls = append(f.calls, c)
	if f.reply == nil {
		return nil, nil
	}
	return f.reply(c)
}

// sig is the D-Bus signature of a call's arguments, which is what systemd
// checks a method call against.
func sig(args []any) string {
	return dbus.SignatureOf(args...).String()
}

func TestSystemdUnitMethods(t *testing.T) {
	job := []any{dbus.ObjectPath("/org/freedesktop/systemd1/job/1")}
	tests := []struct {
		name   string
		run    func(s *DBusSystemd) error
		method string
		args   []any
		sig    string
		body   []any
	}{
		{"start", func(s *DBusSystemd) error { return s.Start(context.Background(), "darkpanel-agent.service") },
			"StartUnit", []any{"darkpanel-agent.service", "replace"}, "ss", job},
		{"stop", func(s *DBusSystemd) error { return s.Stop(context.Background(), "darkpanel-agent.service") },
			"StopUnit", []any{"darkpanel-agent.service", "replace"}, "ss", job},
		{"restart", func(s *DBusSystemd) error { return s.Restart(context.Background(), "darkpanel-agent.service") },
			"RestartUnit", []any{"darkpanel-agent.service", "replace"}, "ss", job},
		{"kill", func(s *DBusSystemd) error {
			return s.Kill(context.Background(), "darkpanel-update-01J.service", syscall.SIGKILL)
		}, "KillUnit", []any{"darkpanel-update-01J.service", "all", int32(9)}, "ssi", nil},
		{"reset failed", func(s *DBusSystemd) error { return s.ResetFailed(context.Background(), "darkpanel-agent.service") },
			"ResetFailedUnit", []any{"darkpanel-agent.service"}, "s", nil},
		{"enable", func(s *DBusSystemd) error {
			return s.Enable(context.Background(), "darkpanel-agent.service", "darkpanel-update-recover.service")
		}, "EnableUnitFiles", []any{[]string{"darkpanel-agent.service", "darkpanel-update-recover.service"}, false, false},
			"asbb", []any{true, [][]any{}}},
		{"disable", func(s *DBusSystemd) error { return s.Disable(context.Background(), "darkpanel-agent.service") },
			"DisableUnitFiles", []any{[]string{"darkpanel-agent.service"}, false}, "asb", []any{[][]any{}}},
		{"reload", func(s *DBusSystemd) error { return s.Reload(context.Background()) }, "Reload", nil, "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bus := &fakeBus{reply: func(busCall) ([]any, error) { return tt.body, nil }}
			if err := tt.run(newSystemd(bus)); err != nil {
				t.Fatal(err)
			}
			want := busCall{path: "/org/freedesktop/systemd1", method: "org.freedesktop.systemd1.Manager." + tt.method, args: tt.args}
			if len(bus.calls) != 1 || !reflect.DeepEqual(bus.calls[0], want) {
				t.Fatalf("calls = %#v\nwant [%#v]", bus.calls, want)
			}
			if got := sig(bus.calls[0].args); got != tt.sig {
				t.Errorf("signature %q, want %q", got, tt.sig)
			}
		})
	}
}

func TestSystemdErrorsNameTheUnit(t *testing.T) {
	bus := &fakeBus{reply: func(busCall) ([]any, error) {
		return nil, dbus.Error{Name: "org.freedesktop.systemd1.NoSuchUnit", Body: []any{"Unit x.service not loaded."}}
	}}
	err := newSystemd(bus).Stop(context.Background(), "x.service")
	if err == nil || !strings.Contains(err.Error(), "platform: stop x.service") {
		t.Errorf("error %v, want one naming the operation and unit", err)
	}
}

func TestStartTransientService(t *testing.T) {
	bus := &fakeBus{reply: func(busCall) ([]any, error) { return []any{dbus.ObjectPath("/job/2")}, nil }}
	err := newSystemd(bus).StartTransientService(context.Background(), "darkpanel-update-01J.service", TransientService{
		Description: "Darkpanel update 01J",
		ExecStart:   []string{"/usr/local/lib/darkpanel/darkpanel", "update", "run", "01J"},
		OnFailure:   []string{"darkpanel-rollback.service"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := busCall{path: "/org/freedesktop/systemd1", method: "org.freedesktop.systemd1.Manager.StartTransientUnit", args: []any{
		"darkpanel-update-01J.service", "fail",
		[]property{
			{"Description", dbus.MakeVariant("Darkpanel update 01J")},
			{"ExecStart", dbus.MakeVariant([]execCommand{{
				Path: "/usr/local/lib/darkpanel/darkpanel",
				Args: []string{"/usr/local/lib/darkpanel/darkpanel", "update", "run", "01J"},
			}})},
			{"OnFailure", dbus.MakeVariant([]string{"darkpanel-rollback.service"})},
		},
		[]auxUnit{},
	}}
	if len(bus.calls) != 1 || !reflect.DeepEqual(bus.calls[0], want) {
		t.Fatalf("calls = %#v\nwant [%#v]", bus.calls, want)
	}
	if got := sig(bus.calls[0].args); got != "ssa(sv)a(sa(sv))" {
		t.Errorf("signature %q, want ssa(sv)a(sa(sv))", got)
	}
	if got := bus.calls[0].args[2].([]property)[1].Value.Signature().String(); got != "a(sasb)" {
		t.Errorf("ExecStart signature %q, want a(sasb)", got)
	}
}

func TestStartTransientTimer(t *testing.T) {
	bus := &fakeBus{reply: func(busCall) ([]any, error) { return []any{dbus.ObjectPath("/job/3")}, nil }}
	err := newSystemd(bus).StartTransientTimer(context.Background(), "darkpanel-update-deadline-01J.timer", TransientTimer{
		Description: "Darkpanel update deadline 01J",
		OnActive:    80 * time.Second,
		Accuracy:    time.Second,
		Service: TransientService{
			Description: "Darkpanel update deadline rollback 01J",
			ExecStart:   []string{"/usr/local/lib/darkpanel/darkpanel.recovery", "update", "recover", "--deadline", "01J"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := busCall{path: "/org/freedesktop/systemd1", method: "org.freedesktop.systemd1.Manager.StartTransientUnit", args: []any{
		"darkpanel-update-deadline-01J.timer", "fail",
		[]property{
			{"Description", dbus.MakeVariant("Darkpanel update deadline 01J")},
			{"OnActiveSec", dbus.MakeVariant(uint64(80_000_000))},
			{"AccuracyUSec", dbus.MakeVariant(uint64(1_000_000))},
		},
		[]auxUnit{{Name: "darkpanel-update-deadline-01J.service", Props: []property{
			{"Description", dbus.MakeVariant("Darkpanel update deadline rollback 01J")},
			{"ExecStart", dbus.MakeVariant([]execCommand{{
				Path: "/usr/local/lib/darkpanel/darkpanel.recovery",
				Args: []string{"/usr/local/lib/darkpanel/darkpanel.recovery", "update", "recover", "--deadline", "01J"},
			}})},
		}}},
	}}
	if len(bus.calls) != 1 || !reflect.DeepEqual(bus.calls[0], want) {
		t.Fatalf("calls = %#v\nwant [%#v]", bus.calls, want)
	}
	if got := sig(bus.calls[0].args); got != "ssa(sv)a(sa(sv))" {
		t.Errorf("signature %q, want ssa(sv)a(sa(sv))", got)
	}
}

func TestTransientUnitsRefuseBadInput(t *testing.T) {
	s := newSystemd(&fakeBus{})
	ctx := context.Background()
	good := TransientService{ExecStart: []string{"/bin/true"}}
	tests := []struct {
		name string
		err  error
	}{
		{"relative program", s.StartTransientService(ctx, "a.service", TransientService{ExecStart: []string{"true"}})},
		{"no program", s.StartTransientService(ctx, "a.service", TransientService{})},
		{"service name", s.StartTransientService(ctx, "a.timer", good)},
		{"timer name", s.StartTransientTimer(ctx, "a.service", TransientTimer{OnActive: time.Second, Service: good})},
		{"no delay", s.StartTransientTimer(ctx, "a.timer", TransientTimer{Service: good})},
		{"negative accuracy", s.StartTransientTimer(ctx, "a.timer", TransientTimer{OnActive: time.Second, Accuracy: -1, Service: good})},
		{"timer program", s.StartTransientTimer(ctx, "a.timer", TransientTimer{OnActive: time.Second})},
	}
	for _, tt := range tests {
		if tt.err == nil {
			t.Errorf("%s: no error", tt.name)
		}
	}
}

func TestState(t *testing.T) {
	unitPath := dbus.ObjectPath("/org/freedesktop/systemd1/unit/darkpanel_2dagent_2eservice")
	values := map[string]string{"LoadState": "loaded", "ActiveState": "active", "SubState": "running"}
	bus := &fakeBus{reply: func(c busCall) ([]any, error) {
		switch c.method {
		case "org.freedesktop.systemd1.Manager.LoadUnit":
			return []any{unitPath}, nil
		case "org.freedesktop.DBus.Properties.Get":
			if c.path != unitPath || c.args[0] != "org.freedesktop.systemd1.Unit" {
				return nil, fmt.Errorf("unexpected Get %v on %s", c.args, c.path)
			}
			return []any{dbus.MakeVariant(values[c.args[1].(string)])}, nil
		}
		return nil, errors.New("unexpected method " + c.method)
	}}
	st, err := newSystemd(bus).State(context.Background(), "darkpanel-agent.service")
	if err != nil {
		t.Fatal(err)
	}
	if st != (UnitState{LoadState: "loaded", ActiveState: "active", SubState: "running"}) {
		t.Errorf("State = %+v", st)
	}
	if bus.calls[0].method != "org.freedesktop.systemd1.Manager.LoadUnit" ||
		!reflect.DeepEqual(bus.calls[0].args, []any{"darkpanel-agent.service"}) {
		t.Errorf("first call %#v, want LoadUnit of the unit", bus.calls[0])
	}
	var props []string
	for _, c := range bus.calls[1:] {
		props = append(props, c.args[1].(string))
	}
	if !reflect.DeepEqual(props, []string{"LoadState", "ActiveState", "SubState"}) {
		t.Errorf("properties read %v", props)
	}
}

// TestSystemdLive drives the real systemd of the host, so it runs only when
// DARKPANEL_SYSTEMD_LIVE=1 and the test runs as root. It proves the D-Bus
// encodings the fake bus only records; every unit it starts is transient and
// stopped again.
func TestSystemdLive(t *testing.T) {
	if os.Getenv("DARKPANEL_SYSTEMD_LIVE") != "1" || os.Geteuid() != 0 {
		t.Skip("set DARKPANEL_SYSTEMD_LIVE=1 and run as root to drive the host's systemd")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	s, err := NewSystemd(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	prefix := fmt.Sprintf("darkpanel-live-%d-%d", os.Getpid(), time.Now().UnixNano())
	var started []string
	t.Cleanup(func() {
		for _, u := range started {
			_ = s.Stop(context.Background(), u)
			_ = s.ResetFailed(context.Background(), u)
		}
	})
	waitFor := func(unit string, ok func(UnitState) bool) UnitState {
		t.Helper()
		var st UnitState
		for range 100 {
			if st, err = s.State(ctx, unit); err != nil {
				t.Fatal(err)
			}
			if ok(st) {
				return st
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("%s stayed %+v", unit, st)
		return st
	}
	gone := func(st UnitState) bool { return st.LoadState == "not-found" || st.ActiveState == "inactive" }
	sleep := TransientService{Description: "Darkpanel live test", ExecStart: []string{"/bin/sleep", "30"}}

	killed := prefix + "-killed.service"
	svc := sleep
	svc.OnFailure = []string{prefix + "-hook.service"}
	started = append(started, killed)
	if err := s.StartTransientService(ctx, killed, svc); err != nil {
		t.Fatal(err)
	}
	waitFor(killed, func(st UnitState) bool { return st.ActiveState == "active" })
	if err := s.Kill(ctx, killed, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	waitFor(killed, func(st UnitState) bool { return st.ActiveState == "failed" })
	if err := s.ResetFailed(ctx, killed); err != nil {
		t.Fatal(err)
	}
	waitFor(killed, gone)

	stopped := prefix + "-stopped.service"
	started = append(started, stopped)
	if err := s.StartTransientService(ctx, stopped, sleep); err != nil {
		t.Fatal(err)
	}
	waitFor(stopped, func(st UnitState) bool { return st.ActiveState == "active" })
	if err := s.Stop(ctx, stopped); err != nil {
		t.Fatal(err)
	}
	waitFor(stopped, gone)

	timer := prefix + ".timer"
	started = append(started, timer)
	err = s.StartTransientTimer(ctx, timer, TransientTimer{
		Description: "Darkpanel live test timer", OnActive: time.Hour, Accuracy: time.Second,
		Service: TransientService{Description: "Darkpanel live test timer job", ExecStart: []string{"/bin/true"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(timer, func(st UnitState) bool { return st.ActiveState == "active" && st.SubState == "waiting" })
	if err := s.Stop(ctx, timer); err != nil {
		t.Fatal(err)
	}
	waitFor(timer, gone)

	if st, err := s.State(ctx, prefix+"-missing.service"); err != nil || st.LoadState != "not-found" {
		t.Errorf("a missing unit: %+v, %v; want LoadState not-found", st, err)
	}
}
