package platform

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/darkraise/darkpanel/internal/core"
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
	exec := dbus.MakeVariant([]execCommand{{
		Path: "/usr/local/lib/darkpanel/darkpanel",
		Args: []string{"/usr/local/lib/darkpanel/darkpanel", "update", "run", "01J"},
	}})
	tests := []struct {
		name string
		svc  TransientService
		want []property
	}{
		{"with a failure hook", TransientService{
			Description: "Darkpanel update 01J",
			ExecStart:   []string{"/usr/local/lib/darkpanel/darkpanel", "update", "run", "01J"},
			OnFailure:   []string{"darkpanel-rollback.service"},
		}, []property{
			{"Description", dbus.MakeVariant("Darkpanel update 01J")},
			{"ExecStart", exec},
			{"OnFailure", dbus.MakeVariant([]string{"darkpanel-rollback.service"})},
		}},
		{"with a runtime bound and a collect mode", TransientService{
			Description: "Darkpanel update 01J",
			ExecStart:   []string{"/usr/local/lib/darkpanel/darkpanel", "update", "run", "01J"},
			RuntimeMax:  15 * time.Minute,
			CollectMode: "inactive-or-failed",
		}, []property{
			{"Description", dbus.MakeVariant("Darkpanel update 01J")},
			{"ExecStart", exec},
			{"RuntimeMaxUSec", dbus.MakeVariant(uint64(900_000_000))},
			{"CollectMode", dbus.MakeVariant("inactive-or-failed")},
		}},
		{"with a runtime bound below a microsecond", TransientService{
			Description: "Darkpanel update 01J",
			ExecStart:   []string{"/usr/local/lib/darkpanel/darkpanel", "update", "run", "01J"},
			RuntimeMax:  1500 * time.Nanosecond,
		}, []property{
			{"Description", dbus.MakeVariant("Darkpanel update 01J")},
			{"ExecStart", exec},
			{"RuntimeMaxUSec", dbus.MakeVariant(uint64(2))},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bus := &fakeBus{reply: func(busCall) ([]any, error) { return []any{dbus.ObjectPath("/job/2")}, nil }}
			if err := newSystemd(bus).StartTransientService(context.Background(), "darkpanel-update-01J.service", tt.svc); err != nil {
				t.Fatal(err)
			}
			want := busCall{path: "/org/freedesktop/systemd1", method: "org.freedesktop.systemd1.Manager.StartTransientUnit",
				args: []any{"darkpanel-update-01J.service", "fail", tt.want, []auxUnit{}}}
			if len(bus.calls) != 1 || !reflect.DeepEqual(bus.calls[0], want) {
				t.Fatalf("calls = %#v\nwant [%#v]", bus.calls, want)
			}
			if got := sig(bus.calls[0].args); got != "ssa(sv)a(sa(sv))" {
				t.Errorf("signature %q, want ssa(sv)a(sa(sv))", got)
			}
			wantSigs := map[string]string{"Description": "s", "ExecStart": "a(sasb)", "OnFailure": "as", "RuntimeMaxUSec": "t", "CollectMode": "s"}
			for _, p := range bus.calls[0].args[2].([]property) {
				if got := p.Value.Signature().String(); got != wantSigs[p.Name] {
					t.Errorf("%s signature %q, want %q", p.Name, got, wantSigs[p.Name])
				}
			}
		})
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
	bus := &fakeBus{}
	s := newSystemd(bus)
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
		{"negative runtime bound", s.StartTransientService(ctx, "a.service", TransientService{ExecStart: []string{"/bin/true"}, RuntimeMax: -time.Second})},
	}
	for _, tt := range tests {
		if tt.err == nil || !strings.HasPrefix(tt.err.Error(), "platform: start a.") {
			t.Errorf("%s: error %v, want one naming the start of the unit", tt.name, tt.err)
		}
	}
	if len(bus.calls) != 0 {
		t.Errorf("refused starts reached the bus: %#v", bus.calls)
	}
}

func TestState(t *testing.T) {
	unitPath := dbus.ObjectPath("/org/freedesktop/systemd1/unit/darkpanel_2dtask_2eservice")
	jobPath := dbus.ObjectPath("/org/freedesktop/systemd1/job/7")
	// A (uo) arrives from the bus as a []any, which is what the fake answers.
	noJob := []any{uint32(0), dbus.ObjectPath("/")}
	startJob := []any{uint32(7), jobPath}
	unitReads := []string{"LoadState", "ActiveState", "SubState", "Job", "MainPID"}
	tests := []struct {
		name    string
		unit    map[string]any
		jobType func() ([]any, error) // the answer to the job's JobType
		want    UnitState
		reads   []string
		wantErr string
	}{
		{"no job", map[string]any{"LoadState": "loaded", "ActiveState": "active", "SubState": "running", "Job": noJob}, nil,
			UnitState{LoadState: "loaded", ActiveState: "active", SubState: "running"}, unitReads, ""},
		{"a queued start job", map[string]any{"LoadState": "loaded", "ActiveState": "inactive", "SubState": "dead", "Job": startJob},
			func() ([]any, error) { return []any{dbus.MakeVariant("start")}, nil },
			UnitState{LoadState: "loaded", ActiveState: "inactive", SubState: "dead", PendingJob: "start"},
			append(slices.Clone(unitReads), "JobType"), ""},
		{"a unit that does not exist", map[string]any{"LoadState": "not-found", "ActiveState": "inactive", "SubState": "dead", "Job": noJob}, nil,
			UnitState{LoadState: "not-found", ActiveState: "inactive", SubState: "dead"}, unitReads[:4], ""},
		{"a job that finished between the reads", map[string]any{"LoadState": "loaded", "ActiveState": "active", "SubState": "running", "Job": startJob},
			func() ([]any, error) {
				return nil, dbus.Error{Name: "org.freedesktop.DBus.Error.UnknownObject", Body: []any{"Unknown object."}}
			},
			UnitState{LoadState: "loaded", ActiveState: "active", SubState: "running"}, append(slices.Clone(unitReads), "JobType"), ""},
		{"a job whose type cannot be read", map[string]any{"LoadState": "loaded", "ActiveState": "inactive", "SubState": "dead", "Job": startJob},
			func() ([]any, error) { return nil, errors.New("bus closed") },
			UnitState{}, append(slices.Clone(unitReads), "JobType"), "read the type of job 7: bus closed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bus := &fakeBus{reply: func(c busCall) ([]any, error) {
				switch {
				case c.method == "org.freedesktop.systemd1.Manager.LoadUnit":
					return []any{unitPath}, nil
				case c.method == "org.freedesktop.DBus.Properties.Get" && c.path == unitPath && c.args[0] == "org.freedesktop.systemd1.Unit":
					return []any{dbus.MakeVariant(tt.unit[c.args[1].(string)])}, nil
				case c.method == "org.freedesktop.DBus.Properties.Get" && c.path == unitPath &&
					c.args[0] == "org.freedesktop.systemd1.Service" && c.args[1] == "MainPID":
					return []any{dbus.MakeVariant(uint32(0))}, nil
				case c.method == "org.freedesktop.DBus.Properties.Get" && c.path == jobPath &&
					c.args[0] == "org.freedesktop.systemd1.Job" && c.args[1] == "JobType" && tt.jobType != nil:
					return tt.jobType()
				}
				return nil, fmt.Errorf("unexpected %s %v on %s", c.method, c.args, c.path)
			}}
			st, err := newSystemd(bus).State(context.Background(), "darkpanel-task.service")
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("State error %v, want one containing %q", err, tt.wantErr)
				}
			} else if err != nil || st != tt.want {
				t.Fatalf("State = %+v, %v; want %+v", st, err, tt.want)
			}
			if bus.calls[0].method != "org.freedesktop.systemd1.Manager.LoadUnit" ||
				!reflect.DeepEqual(bus.calls[0].args, []any{"darkpanel-task.service"}) {
				t.Errorf("first call %#v, want LoadUnit of the unit", bus.calls[0])
			}
			var reads []string
			for _, c := range bus.calls[1:] {
				reads = append(reads, c.args[1].(string))
			}
			if !reflect.DeepEqual(reads, tt.reads) {
				t.Errorf("properties read %v, want %v", reads, tt.reads)
			}
		})
	}
}

func TestSystemdStateMainPID(t *testing.T) {
	unitPath := dbus.ObjectPath("/org/freedesktop/systemd1/unit/x")
	noJob := []any{uint32(0), dbus.ObjectPath("/")}
	tests := []struct {
		unit      string
		loadState string
		want      uint32
		readsPID  bool
	}{
		{"x.service", "loaded", 1234, true},
		{"x.timer", "loaded", 0, false},
		{"x.service", "not-found", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.unit+" "+tt.loadState, func(t *testing.T) {
			unit := map[string]any{"LoadState": tt.loadState, "ActiveState": "active", "SubState": "running", "Job": noJob}
			readPID := false
			bus := &fakeBus{reply: func(c busCall) ([]any, error) {
				switch {
				case c.method == "org.freedesktop.systemd1.Manager.LoadUnit":
					return []any{unitPath}, nil
				case c.args[0] == "org.freedesktop.systemd1.Service" && c.args[1] == "MainPID":
					readPID = true
					return []any{dbus.MakeVariant(uint32(1234))}, nil
				case c.args[0] == "org.freedesktop.systemd1.Unit":
					return []any{dbus.MakeVariant(unit[c.args[1].(string)])}, nil
				}
				return nil, fmt.Errorf("unexpected %s %v on %s", c.method, c.args, c.path)
			}}
			st, err := newSystemd(bus).State(context.Background(), tt.unit)
			if err != nil || st.MainPID != tt.want || readPID != tt.readsPID {
				t.Errorf("State = %+v, %v, MainPID read %v; want MainPID %d, read %v", st, err, readPID, tt.want, tt.readsPID)
			}
		})
	}
}

func TestSystemdList(t *testing.T) {
	svcPath := dbus.ObjectPath("/org/freedesktop/systemd1/unit/darkpanel_2dtask_2da_2eservice")
	timerPath := dbus.ObjectPath("/org/freedesktop/systemd1/unit/darkpanel_2dtask_2db_2etimer")
	bus := &fakeBus{reply: func(c busCall) ([]any, error) {
		switch {
		case c.method == "org.freedesktop.systemd1.Manager.ListUnitsByPatterns":
			// An a(ssssssouso) arrives from the bus as a [][]any.
			return []any{[][]any{
				{"darkpanel-task-a.service", "Task a", "loaded", "active", "running", "", svcPath,
					uint32(9), "stop", dbus.ObjectPath("/org/freedesktop/systemd1/job/9")},
				{"darkpanel-task-b.timer", "Task b", "loaded", "active", "waiting", "", timerPath,
					uint32(0), "", dbus.ObjectPath("/")},
			}}, nil
		case c.method == "org.freedesktop.DBus.Properties.Get" && c.path == svcPath &&
			c.args[0] == "org.freedesktop.systemd1.Service" && c.args[1] == "MainPID":
			return []any{dbus.MakeVariant(uint32(1234))}, nil
		}
		return nil, fmt.Errorf("unexpected %s %v on %s", c.method, c.args, c.path)
	}}
	got, err := newSystemd(bus).List(context.Background(), "darkpanel-task-*")
	if err != nil {
		t.Fatal(err)
	}
	want := []UnitStatus{
		{Name: "darkpanel-task-a.service", UnitState: UnitState{LoadState: "loaded", ActiveState: "active", SubState: "running",
			MainPID: 1234, PendingJob: "stop"}},
		{Name: "darkpanel-task-b.timer", UnitState: UnitState{LoadState: "loaded", ActiveState: "active", SubState: "waiting"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("List = %+v\nwant %+v", got, want)
	}
	first := busCall{path: "/org/freedesktop/systemd1", method: "org.freedesktop.systemd1.Manager.ListUnitsByPatterns",
		args: []any{[]string{}, []string{"darkpanel-task-*"}}}
	if len(bus.calls) != 2 || !reflect.DeepEqual(bus.calls[0], first) {
		t.Fatalf("calls = %#v\nwant [%#v] and one MainPID read", bus.calls, first)
	}
	if got := sig(bus.calls[0].args); got != "asas" {
		t.Errorf("signature %q, want asas", got)
	}

	failing := &fakeBus{reply: func(busCall) ([]any, error) { return nil, errors.New("bus closed") }}
	if _, err := newSystemd(failing).List(context.Background(), "darkpanel-task-*"); err == nil ||
		!strings.Contains(err.Error(), "platform: list units darkpanel-task-*: bus closed") {
		t.Errorf("List error %v, want one naming the pattern", err)
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
	bounded := sleep
	bounded.RuntimeMax, bounded.CollectMode = time.Minute, "inactive-or-failed"
	if err := s.StartTransientService(ctx, stopped, bounded); err != nil {
		t.Fatal(err)
	}
	if st := waitFor(stopped, func(st UnitState) bool { return st.ActiveState == "active" }); st.PendingJob != "" || st.MainPID == 0 {
		t.Errorf("an active unit reports the pending job %q and the main PID %d, want none and a PID", st.PendingJob, st.MainPID)
	}
	listed, err := s.List(ctx, prefix+"-*")
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(listed, func(u UnitStatus) bool { return u.Name == stopped })
	if i < 0 || listed[i].ActiveState != "active" || listed[i].MainPID == 0 {
		t.Errorf("List(%s-*) = %+v; want %s active with a main PID", prefix, listed, stopped)
	}
	body, err := s.manager(ctx, "load "+stopped, "LoadUnit", stopped)
	if err != nil {
		t.Fatal(err)
	}
	var unitPath dbus.ObjectPath
	if err := dbus.Store(body, &unitPath); err != nil {
		t.Fatal(err)
	}
	var runtimeMax uint64
	if err := s.property(ctx, unitPath, "org.freedesktop.systemd1.Service", "RuntimeMaxUSec", &runtimeMax); err != nil || runtimeMax != 60_000_000 {
		t.Errorf("RuntimeMaxUSec = %d, %v; want 60000000", runtimeMax, err)
	}
	var collect string
	if err := s.property(ctx, unitPath, unitInterface, "CollectMode", &collect); err != nil || collect != "inactive-or-failed" {
		t.Errorf("CollectMode = %q, %v; want inactive-or-failed", collect, err)
	}
	var jobType string
	err = s.property(ctx, "/org/freedesktop/systemd1/job/4294967295", jobInterface, "JobType", &jobType)
	var dbusErr dbus.Error
	if !errors.As(err, &dbusErr) || dbusErr.Name != unknownObject {
		t.Errorf("JobType of a job that does not exist: %#v; want a D-Bus error named %s", err, unknownObject)
	}
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

func TestUnitInspect(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "nginx.service")
	if err := os.WriteFile(file, []byte(strings.Repeat("x", 70<<10)), 0o644); err != nil { //nolint:gosec // G306: a unit file every user may read
		t.Fatal(err)
	}
	secret := filepath.Join(dir, "secret.service")
	if err := os.WriteFile(secret, []byte("[Service]\nEnvironment=TOKEN=s3cret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	masked := filepath.Join(dir, "masked.service")
	if err := os.Symlink("/dev/null", masked); err != nil {
		t.Fatal(err)
	}
	frag := file
	path := dbus.ObjectPath("/org/freedesktop/systemd1/unit/nginx_2eservice")
	started := time.Date(2026, 11, 26, 8, 0, 0, 0, time.UTC)
	bus := &fakeBus{reply: func(c busCall) ([]any, error) {
		switch {
		case c.method == "org.freedesktop.systemd1.Manager.LoadUnit" && c.args[0] == "nginx":
			return []any{path}, nil
		case c.method == "org.freedesktop.systemd1.Manager.LoadUnit":
			return []any{dbus.ObjectPath("/org/freedesktop/systemd1/unit/gone")}, nil
		case c.method == "org.freedesktop.DBus.Properties.GetAll" && c.path == path && c.args[0] == unitInterface:
			return []any{map[string]dbus.Variant{
				"Id": dbus.MakeVariant("nginx.service"), "Names": dbus.MakeVariant([]string{"nginx.service", "nginx"}),
				"Description": dbus.MakeVariant("A web server"), "LoadState": dbus.MakeVariant("loaded"),
				"ActiveState": dbus.MakeVariant("active"), "SubState": dbus.MakeVariant("running"),
				"UnitFileState": dbus.MakeVariant("enabled"), "Requires": dbus.MakeVariant([]string{"system.slice"}),
				"Wants": dbus.MakeVariant([]string{}), "Triggers": dbus.MakeVariant([]string{}),
				"RefuseManualStart": dbus.MakeVariant(false), "AllowIsolate": dbus.MakeVariant(false),
				"SuccessAction": dbus.MakeVariant("none"), "FailureAction": dbus.MakeVariant("reboot"),
				"JobTimeoutAction": dbus.MakeVariant("none"), "StartLimitAction": dbus.MakeVariant("none"),
				"FragmentPath": dbus.MakeVariant(frag), "DropInPaths": dbus.MakeVariant([]string{"/etc/x.conf"}),
				"ActiveEnterTimestamp": dbus.MakeVariant(uint64(started.UnixMicro())),
			}}, nil
		case c.method == "org.freedesktop.DBus.Properties.GetAll" && c.path == path && c.args[0] == serviceInterface:
			return []any{map[string]dbus.Variant{"MainPID": dbus.MakeVariant(uint32(42)), "NRestarts": dbus.MakeVariant(uint32(2)),
				"MemoryCurrent": dbus.MakeVariant(uint64(12 << 20)), "TasksCurrent": dbus.MakeVariant(^uint64(0))}}, nil
		case c.method == "org.freedesktop.DBus.Properties.GetAll":
			return []any{map[string]dbus.Variant{"Id": dbus.MakeVariant("gone.service"), "LoadState": dbus.MakeVariant("not-found")}}, nil
		}
		return nil, fmt.Errorf("unexpected %s %v on %s", c.method, c.args, c.path)
	}}
	got, err := newSystemd(bus).Inspect(context.Background(), "nginx")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "nginx.service" || !slices.Equal(got.Names, []string{"nginx.service", "nginx"}) || got.MainPID != 42 ||
		got.Restarts != 2 || got.MemoryCurrent == nil || *got.MemoryCurrent != 12<<20 || got.TasksCurrent != nil ||
		got.FailureAction != "reboot" || !got.StartedAt.Equal(started) || got.FragmentPath != file ||
		len(got.FileText) != 64<<10 || !got.FileTruncated || !slices.Equal(got.DropInPaths, []string{"/etc/x.conf"}) {
		t.Errorf("Inspect = %+v", got)
	}
	for _, frag = range []string{secret, masked, filepath.Join(dir, "deleted.service")} {
		got, err := newSystemd(bus).Inspect(context.Background(), "nginx")
		if err != nil || got.FileText != "" || got.FileTruncated || got.FragmentPath != frag || got.MainPID != 42 {
			t.Errorf("Inspect with unit file %s: text %q, %+v, %v; want no text and the other fields", filepath.Base(frag), got.FileText, got.FragmentPath, err)
		}
	}
	gone, err := newSystemd(bus).Inspect(context.Background(), "gone.service")
	if err != nil || gone.LoadState != "not-found" {
		t.Errorf("Inspect of a missing unit = %+v, %v", gone, err)
	}
}

func TestUnitListAll(t *testing.T) {
	svc := dbus.ObjectPath("/org/freedesktop/systemd1/unit/nginx_2eservice")
	bus := &fakeBus{reply: func(c busCall) ([]any, error) {
		switch {
		case c.method == "org.freedesktop.systemd1.Manager.ListUnitsByPatterns":
			return []any{[][]any{{"nginx.service", "A web server", "loaded", "active", "running", "", svc, uint32(0), "", dbus.ObjectPath("/")}}}, nil
		case c.method == "org.freedesktop.systemd1.Manager.ListUnitFilesByPatterns":
			return []any{[][]any{{"/lib/systemd/system/nginx.service", "enabled"}, {"/lib/systemd/system/apache2.service", "disabled"},
				{"/lib/systemd/system/getty@.service", "enabled"}}}, nil
		case c.method == "org.freedesktop.DBus.Properties.Get" && c.path == svc:
			return []any{dbus.MakeVariant(uint32(42))}, nil
		}
		return nil, fmt.Errorf("unexpected %s %v on %s", c.method, c.args, c.path)
	}}
	got, err := newSystemd(bus).ListAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []core.UnitInfo{
		{ID: "apache2.service", LoadState: "not-loaded", ActiveState: "inactive", SubState: "dead", UnitFileState: "disabled"},
		{ID: "nginx.service", Description: "A web server", LoadState: "loaded", ActiveState: "active", SubState: "running",
			UnitFileState: "enabled", MainPID: 42},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ListAll = %+v\nwant %+v", got, want)
	}
	if got := sig(bus.calls[0].args); got != "asas" {
		t.Errorf("signature %q, want asas", got)
	}
}

// TestSystemdTimers reads each timer's unit, next elapse, last trigger and
// file, and a service's last run; systemd's zero and maximum stamps are none.
func TestSystemdTimers(t *testing.T) {
	timerPath := dbus.ObjectPath("/org/freedesktop/systemd1/unit/apt_2ddaily_2etimer")
	svcPath := dbus.ObjectPath("/org/freedesktop/systemd1/unit/darkpanel_2djob_2da_2eservice")
	next, started := time.Date(2026, 11, 27, 6, 0, 0, 0, time.UTC), time.Date(2026, 11, 26, 3, 0, 4, 0, time.UTC)
	bus := &fakeBus{reply: func(c busCall) ([]any, error) {
		switch {
		case c.method == "org.freedesktop.systemd1.Manager.ListUnitsByPatterns" && slices.Equal(c.args[1].([]string), []string{"*.timer"}):
			return []any{[][]any{{"apt-daily.timer", "Daily apt", "loaded", "active", "waiting", "", timerPath, uint32(0), "", dbus.ObjectPath("/")}}}, nil
		case c.method == "org.freedesktop.DBus.Properties.GetAll" && c.path == timerPath && c.args[0] == timerInterface:
			return []any{map[string]dbus.Variant{"Unit": dbus.MakeVariant("apt-daily.service"),
				"NextElapseUSecRealtime": dbus.MakeVariant(uint64(next.UnixMicro())), "LastTriggerUSec": dbus.MakeVariant(uint64(0))}}, nil
		case c.method == "org.freedesktop.DBus.Properties.Get" && c.path == timerPath && c.args[1] == "FragmentPath":
			return []any{dbus.MakeVariant("/usr/lib/systemd/system/apt-daily.timer")}, nil
		case c.method == "org.freedesktop.systemd1.Manager.LoadUnit" && c.args[0] == "darkpanel-job-a.service":
			return []any{svcPath}, nil
		case c.method == "org.freedesktop.DBus.Properties.Get" && c.path == svcPath && c.args[1] == "ActiveState":
			return []any{dbus.MakeVariant("inactive")}, nil
		case c.method == "org.freedesktop.DBus.Properties.GetAll" && c.path == svcPath && c.args[0] == serviceInterface:
			return []any{map[string]dbus.Variant{"Result": dbus.MakeVariant("exit-code"), "ExecMainStatus": dbus.MakeVariant(int32(2)),
				"ExecMainStartTimestamp": dbus.MakeVariant(uint64(started.UnixMicro()))}}, nil
		}
		return nil, fmt.Errorf("unexpected %s %v on %s", c.method, c.args, c.path)
	}}
	s := newSystemd(bus)
	got, err := s.Timers(context.Background())
	want := []core.TimerInfo{{Name: "apt-daily.timer", Unit: "apt-daily.service", ActiveState: "active",
		FragmentPath: "/usr/lib/systemd/system/apt-daily.timer", Next: next}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("Timers = %+v, %v; want %+v", got, err, want)
	}
	run, err := s.LastRun(context.Background(), "darkpanel-job-a.service")
	if want := (core.ServiceRun{ActiveState: "inactive", Started: started, Result: "exit-code", ExitStatus: 2}); err != nil || run != want {
		t.Errorf("LastRun = %+v, %v; want %+v", run, err, want)
	}
	if _, err := s.LastRun(context.Background(), "apt-daily.timer"); err == nil {
		t.Error("LastRun of a timer succeeded")
	}
	if !usecTime(^uint64(0)).IsZero() || !usecTime(0).IsZero() {
		t.Error("systemd's none stamps are not zero times")
	}
}

func TestUnitInfoDependencies(t *testing.T) {
	path := dbus.ObjectPath("/org/freedesktop/systemd1/unit/app_2etarget")
	list := func(s ...string) dbus.Variant { return dbus.MakeVariant(s) }
	bus := &fakeBus{reply: func(c busCall) ([]any, error) {
		switch {
		case c.method == "org.freedesktop.systemd1.Manager.LoadUnit":
			return []any{path}, nil
		case c.method == "org.freedesktop.DBus.Properties.GetAll" && c.args[0] == unitInterface:
			return []any{map[string]dbus.Variant{
				"Id": dbus.MakeVariant("app.target"), "LoadState": dbus.MakeVariant("loaded"),
				"Requires": list("a.service"), "Wants": list("b.service"), "BindsTo": list("c.service"),
				"RequiredBy": list("d.service", "e.service"), "BoundBy": list("f.service"), "ConsistsOf": list("g.service"),
			}}, nil
		}
		return nil, fmt.Errorf("unexpected %s %v on %s", c.method, c.args, c.path)
	}}
	got, err := newSystemd(bus).Inspect(context.Background(), "app.target")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.Requires, []string{"a.service"}) || !slices.Equal(got.Wants, []string{"b.service"}) ||
		!slices.Equal(got.BindsTo, []string{"c.service"}) || !slices.Equal(got.RequiredBy, []string{"d.service", "e.service"}) ||
		!slices.Equal(got.BoundBy, []string{"f.service"}) || !slices.Equal(got.ConsistsOf, []string{"g.service"}) {
		t.Errorf("Inspect = %+v; want every dependency list", got)
	}
}
