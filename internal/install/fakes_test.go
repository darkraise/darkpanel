package install

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/platform"
)

// fakeSystemd is a platform.Systemd that records every call except State as
// "<Method> <units...>" and keeps unit states. A unit it was never told
// about is loaded and inactive; one in missing is not-found.
type fakeSystemd struct {
	mu      sync.Mutex
	calls   []string
	states  map[string]platform.UnitState
	missing map[string]bool
	stuck   map[string]bool  // units that stay active when stopped
	fail    map[string]error // by method name, or by "<Method> <unit>" for one unit
}

var _ platform.Systemd = (*fakeSystemd)(nil)

func newFakeSystemd() *fakeSystemd {
	return &fakeSystemd{states: map[string]platform.UnitState{}, missing: map[string]bool{}, stuck: map[string]bool{},
		fail: map[string]error{}}
}

func (f *fakeSystemd) record(method string, units []string, active string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, strings.TrimSpace(method+" "+strings.Join(units, " ")))
	if err := f.fail[method]; err != nil {
		return err
	}
	for _, u := range units {
		if err := f.fail[method+" "+u]; err != nil {
			return err
		}
	}
	if active != "" {
		for _, u := range units {
			if active != "inactive" || !f.stuck[u] {
				f.states[u] = platform.UnitState{LoadState: "loaded", ActiveState: active}
			}
		}
	}
	return nil
}

func (f *fakeSystemd) setState(unit, active string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.states[unit] = platform.UnitState{LoadState: "loaded", ActiveState: active}
}

func (f *fakeSystemd) failOn(method string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err == nil {
		delete(f.fail, method)
		return
	}
	f.fail[method] = err
}

func (f *fakeSystemd) recorded() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

func (f *fakeSystemd) StartTransientService(_ context.Context, name string, _ platform.TransientService) error {
	return f.record("StartTransientService", []string{name}, "active")
}

func (f *fakeSystemd) StartTransientTimer(_ context.Context, name string, _ platform.TransientTimer) error {
	return f.record("StartTransientTimer", []string{name}, "active")
}

func (f *fakeSystemd) Start(_ context.Context, unit string) error {
	return f.record("Start", []string{unit}, "active")
}

func (f *fakeSystemd) Stop(_ context.Context, unit string) error {
	return f.record("Stop", []string{unit}, "inactive")
}

func (f *fakeSystemd) Restart(_ context.Context, unit string) error {
	return f.record("Restart", []string{unit}, "active")
}

func (f *fakeSystemd) Kill(_ context.Context, unit string, _ syscall.Signal) error {
	return f.record("Kill", []string{unit}, "")
}

func (f *fakeSystemd) ResetFailed(_ context.Context, unit string) error {
	return f.record("ResetFailed", []string{unit}, "inactive")
}

func (f *fakeSystemd) Enable(_ context.Context, unitFiles ...string) error {
	return f.record("Enable", unitFiles, "")
}

func (f *fakeSystemd) Disable(_ context.Context, unitFiles ...string) error {
	return f.record("Disable", unitFiles, "")
}

func (f *fakeSystemd) Reload(context.Context) error { return f.record("Reload", nil, "") }

func (f *fakeSystemd) State(_ context.Context, unit string) (platform.UnitState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail["State"]; err != nil {
		return platform.UnitState{}, err
	}
	if f.missing[unit] {
		return platform.UnitState{LoadState: "not-found", ActiveState: "inactive", SubState: "dead"}, nil
	}
	if st, ok := f.states[unit]; ok {
		return st, nil
	}
	return platform.UnitState{LoadState: "loaded", ActiveState: "inactive", SubState: "dead"}, nil
}

// fakeUsers is a user database in memory.
type fakeUsers struct {
	mu    sync.Mutex
	users map[string][2]int
}

func (u *fakeUsers) Lookup(name string) (int, int, bool, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	ids, ok := u.users[name]
	return ids[0], ids[1], ok, nil
}

// fakeRunner records every command and runs useradd and userdel against
// users. exit makes every command exit with that status.
type fakeRunner struct {
	mu    sync.Mutex
	users *fakeUsers
	cmds  []string
	exit  int
}

func (r *fakeRunner) Run(_ context.Context, cmd core.Cmd) (core.CmdResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cmds = append(r.cmds, strings.Join(append([]string{cmd.Path}, cmd.Args...), " "))
	if r.exit != 0 {
		return core.CmdResult{ExitCode: r.exit, Stderr: []byte("refused\n")}, nil
	}
	name := cmd.Args[len(cmd.Args)-1]
	r.users.mu.Lock()
	defer r.users.mu.Unlock()
	switch cmd.Path {
	case "/usr/sbin/useradd":
		r.users.users[name] = [2]int{990, 990}
	case "/usr/sbin/userdel":
		delete(r.users.users, name)
	}
	return core.CmdResult{}, nil
}

func (r *fakeRunner) commands() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.cmds)
}

// fakeController is the local controller: healthy from its unhealthy-th
// call to Healthy on, and issuing token on EnrollLocal.
type fakeController struct {
	mu        sync.Mutex
	unhealthy int
	health    int
	enrolls   int
}

func (c *fakeController) Healthy(context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.health++
	if c.health <= c.unhealthy {
		return os.ErrDeadlineExceeded
	}
	return nil
}

func (c *fakeController) EnrollLocal(context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.enrolls++
	return "dpe1.local", nil
}

// fakeAgent answers Check with check and, on Enroll, saves a credential
// generation the way the agent does: gen-1 and the current symlink.
type fakeAgent struct {
	mu        sync.Mutex
	layout    Layout
	check     CheckResult
	enrollErr error
	tokens    []string
}

func (a *fakeAgent) Check(context.Context) (CheckResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.check, nil
}

func (a *fakeAgent) Enroll(_ context.Context, token string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.tokens = append(a.tokens, token)
	if a.enrollErr != nil {
		return a.enrollErr
	}
	dir := a.layout.Paths.AgentTLSDir
	if err := os.MkdirAll(filepath.Join(dir, "gen-1"), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "gen-1", "agent.crt"), []byte("cert\n"), 0o600); err != nil {
		return err
	}
	_ = os.Remove(a.layout.Paths.AgentTLSCurrent())
	return os.Symlink("gen-1", a.layout.Paths.AgentTLSCurrent())
}

// chowns records every Chown call; the tests run without root.
type chowns struct {
	mu     sync.Mutex
	owners map[string][2]int
}

func (c *chowns) chown(path string, uid, gid int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.owners[path] = [2]int{uid, gid}
	return nil
}

func (c *chowns) of(path string) ([2]int, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ids, ok := c.owners[path]
	return ids, ok
}

// testHost is one fake host with every dependency of Install and
// PlanUninstall.
type testHost struct {
	layout     Layout
	systemd    *fakeSystemd
	users      *fakeUsers
	runner     *fakeRunner
	controller *fakeController
	agent      *fakeAgent
	chowns     *chowns
	clock      *coretest.FakeClock
	out        *bytes.Buffer
}

var installTime = time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)

func newTestHost(t *testing.T) *testHost {
	t.Helper()
	l := Rooted(t.TempDir())
	users := &fakeUsers{users: map[string][2]int{}}
	return &testHost{
		layout:     l,
		systemd:    newFakeSystemd(),
		users:      users,
		runner:     &fakeRunner{users: users},
		controller: &fakeController{},
		agent:      &fakeAgent{layout: l, check: CheckAccepted},
		chowns:     &chowns{owners: map[string][2]int{}},
		clock:      coretest.NewFakeClock(installTime),
		out:        &bytes.Buffer{},
	}
}

// config is an install of role on h.
func (h *testHost) config(role Role) Config {
	return Config{
		Layout: h.layout, Role: role, BinaryVersion: "0.2.0", Systemd: h.systemd, Exec: h.runner, Users: h.users,
		Controller: h.controller, Agent: h.agent, Clock: h.clock, Out: h.out, Chown: h.chowns.chown,
		PollInterval: time.Millisecond, HealthTimeout: time.Second,
	}
}
