package update

import (
	"context"
	"sync"
	"syscall"

	"github.com/darkraise/darkpanel/internal/platform"
)

// systemdCall is one call the fake systemd received.
type systemdCall struct {
	Method  string
	Unit    string
	Service platform.TransientService
	Timer   platform.TransientTimer
	Signal  syscall.Signal
}

// fakeSystemd is a platform.Systemd that records every call and keeps unit
// states in memory: started units are active, stopped ones inactive, killed
// ones failed, and a unit never touched reports LoadState not-found.
type fakeSystemd struct {
	mu     sync.Mutex
	calls  []systemdCall
	states map[string]platform.UnitState
	errs   map[string]error // keyed "<method> <unit>"; unit "" matches any unit
	// onKill runs, outside the lock, after Kill recorded its call; a test
	// stops a frozen runner's work from it.
	onKill func(unit string)
	// onState, when set, runs outside the lock before State answers; an
	// error it returns is State's answer.
	onState func(ctx context.Context, unit string) error
}

var _ platform.Systemd = (*fakeSystemd)(nil)

func newFakeSystemd() *fakeSystemd {
	return &fakeSystemd{states: map[string]platform.UnitState{}, errs: map[string]error{}}
}

// setState makes unit loaded in activeState.
func (f *fakeSystemd) setState(unit, activeState string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.states[unit] = platform.UnitState{LoadState: "loaded", ActiveState: activeState, SubState: activeState}
}

// failCall makes method on unit, or on every unit when unit is "", return
// err.
func (f *fakeSystemd) failCall(method, unit string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.errs[method+" "+unit] = err
}

// callsOf returns the calls of method, in order.
func (f *fakeSystemd) callsOf(method string) []systemdCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []systemdCall
	for _, c := range f.calls {
		if c.Method == method {
			out = append(out, c)
		}
	}
	return out
}

// unitsOf returns the units method was called on, in order.
func (f *fakeSystemd) unitsOf(method string) []string {
	var out []string
	for _, c := range f.callsOf(method) {
		out = append(out, c.Unit)
	}
	return out
}

// record stores c and returns the error set for it, applying state when the
// call succeeds and state is not empty.
func (f *fakeSystemd) record(c systemdCall, state string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, c)
	if err := f.errs[c.Method+" "+c.Unit]; err != nil {
		return err
	}
	if err := f.errs[c.Method+" "]; err != nil {
		return err
	}
	if state != "" {
		f.states[c.Unit] = platform.UnitState{LoadState: "loaded", ActiveState: state, SubState: state}
	}
	return nil
}

func (f *fakeSystemd) StartTransientService(_ context.Context, name string, svc platform.TransientService) error {
	return f.record(systemdCall{Method: "StartTransientService", Unit: name, Service: svc}, "active")
}

func (f *fakeSystemd) StartTransientTimer(_ context.Context, name string, t platform.TransientTimer) error {
	return f.record(systemdCall{Method: "StartTransientTimer", Unit: name, Timer: t}, "active")
}

func (f *fakeSystemd) Start(_ context.Context, unit string) error {
	return f.record(systemdCall{Method: "Start", Unit: unit}, "active")
}

func (f *fakeSystemd) Stop(_ context.Context, unit string) error {
	return f.record(systemdCall{Method: "Stop", Unit: unit}, "inactive")
}

func (f *fakeSystemd) Restart(_ context.Context, unit string) error {
	return f.record(systemdCall{Method: "Restart", Unit: unit}, "active")
}

func (f *fakeSystemd) Kill(_ context.Context, unit string, sig syscall.Signal) error {
	err := f.record(systemdCall{Method: "Kill", Unit: unit, Signal: sig}, "failed")
	f.mu.Lock()
	hook := f.onKill
	f.mu.Unlock()
	if err == nil && hook != nil {
		hook(unit)
	}
	return err
}

func (f *fakeSystemd) ResetFailed(_ context.Context, unit string) error {
	f.mu.Lock()
	st, ok := f.states[unit]
	f.mu.Unlock()
	state := ""
	if ok && st.ActiveState == "failed" {
		state = "inactive"
	}
	return f.record(systemdCall{Method: "ResetFailed", Unit: unit}, state)
}

func (f *fakeSystemd) Enable(_ context.Context, unitFiles ...string) error {
	for _, u := range unitFiles {
		if err := f.record(systemdCall{Method: "Enable", Unit: u}, ""); err != nil {
			return err
		}
	}
	return nil
}

func (f *fakeSystemd) Disable(_ context.Context, unitFiles ...string) error {
	for _, u := range unitFiles {
		if err := f.record(systemdCall{Method: "Disable", Unit: u}, ""); err != nil {
			return err
		}
	}
	return nil
}

func (f *fakeSystemd) Reload(context.Context) error {
	return f.record(systemdCall{Method: "Reload"}, "")
}

func (f *fakeSystemd) State(ctx context.Context, unit string) (platform.UnitState, error) {
	f.mu.Lock()
	hook := f.onState
	f.mu.Unlock()
	if hook != nil {
		if err := hook(ctx, unit); err != nil {
			return platform.UnitState{}, err
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.errs["State "+unit]; err != nil {
		return platform.UnitState{}, err
	}
	if st, ok := f.states[unit]; ok {
		return st, nil
	}
	return platform.UnitState{LoadState: "not-found", ActiveState: "inactive", SubState: "dead"}, nil
}
