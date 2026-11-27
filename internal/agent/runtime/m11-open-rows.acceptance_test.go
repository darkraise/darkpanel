package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

type qaParams struct{}

type qaNoop struct{}

func (qaNoop) Read(context.Context, core.Env, itemIn) (itemOut, error) { return itemOut{}, nil }

// qaGate holds the first check that reaches it until release closes.
type qaGate struct {
	mu      sync.Mutex
	armed   bool
	entered chan struct{}
	release chan struct{}
	result  error
}

func (g *qaGate) arm(result error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.armed, g.result = true, result
	g.entered, g.release = make(chan struct{}), make(chan struct{})
}

func (g *qaGate) pass() error {
	g.mu.Lock()
	armed, entered, release, result := g.armed, g.entered, g.release, g.result
	g.armed = false
	g.mu.Unlock()
	if !armed {
		return nil
	}
	close(entered)
	<-release
	return result
}

// qaUnits records the calls of the jobs module's unit client; Start waits for
// hold, when set, after it entered.
type qaUnits struct {
	core.Units
	mu      sync.Mutex
	log     []string
	entered chan struct{}
	hold    chan struct{}
}

func (u *qaUnits) rec(s string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.log = append(u.log, s)
}

func (u *qaUnits) calls() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return slices.Clone(u.log)
}

func (u *qaUnits) reset() {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.log = nil
}

func (u *qaUnits) Start(_ context.Context, unit string) error {
	u.mu.Lock()
	entered, hold := u.entered, u.hold
	u.mu.Unlock()
	if hold != nil {
		close(entered)
		<-hold
	}
	u.rec("Start " + unit)
	return nil
}
func (u *qaUnits) Stop(_ context.Context, unit string) error    { u.rec("Stop " + unit); return nil }
func (u *qaUnits) Restart(_ context.Context, unit string) error { u.rec("Restart " + unit); return nil }
func (u *qaUnits) Enable(_ context.Context, f ...string) error {
	u.rec("Enable " + strings.Join(f, " "))
	return nil
}
func (u *qaUnits) Disable(_ context.Context, f ...string) error {
	u.rec("Disable " + strings.Join(f, " "))
	return nil
}
func (u *qaUnits) Timers(context.Context) ([]core.TimerInfo, error) {
	u.rec("Timers")
	return []core.TimerInfo{{Name: "darkpanel-job-qa.timer"}}, nil
}
func (u *qaUnits) LastRun(context.Context, string) (core.ServiceRun, error) {
	u.rec("LastRun")
	return core.ServiceRun{}, nil
}

const qaTimer = "darkpanel-job-qa.timer"

// qaJobsModule is a module named jobs that owns the job unit set; its off hook
// stops and disables its timer through its unit client.
type qaJobsModule struct {
	gate  *qaGate
	units *qaUnits
}

func (m qaJobsModule) Manifest() core.Manifest {
	mf := core.Manifest{
		ID: "jobs", Version: 1,
		OwnedPaths: []core.OwnedPath{{Path: core.UnitSetDir, Kind: core.OwnedUnitSet, Pattern: `^darkpanel-job-[a-z]+\.(service|timer)$`}},
		Actions:    []core.ActionDef{core.DefineRead(core.ActionSpec{Name: "jobs.item.get", Version: 1, Side: core.SideAgent}, qaNoop{})},
	}
	if m.gate != nil {
		mf.Preflight = []core.PreflightCheck{{ID: "gate", Run: func(context.Context, core.Env) error { return m.gate.pass() }}}
	}
	return mf
}

func (m qaJobsModule) OnEnabled(ctx context.Context, env core.Env, enabled bool) error {
	if enabled {
		return nil
	}
	return errors.Join(env.Units.Stop(ctx, qaTimer), env.Units.Disable(ctx, qaTimer))
}

// qaConsole is a module named console with a terminal channel that signals
// when it opened and waits for its context to end.
type qaConsole struct{ opened chan struct{} }

func (m qaConsole) Manifest() core.Manifest {
	return core.Manifest{ID: "console", Version: 1, DefaultEnabled: true,
		Actions: []core.ActionDef{core.DefineRead(core.ActionSpec{Name: "console.item.get", Version: 1, Side: core.SideAgent}, qaNoop{})}}
}

func (m qaConsole) Channels() []core.ChannelDef {
	return []core.ChannelDef{core.DefineChannel("terminal", func(ctx context.Context, _ core.Env, _ qaParams, c core.ChannelConn) error {
		m.opened <- struct{}{}
		for ctx.Err() == nil {
			if _, _, err := c.ReadFrame(); err != nil {
				return nil
			}
		}
		return nil
	})}
}

func qaFixture(t *testing.T, gate *qaGate, policyText string) (*fixture, *qaUnits, qaConsole) {
	t.Helper()
	units := &qaUnits{}
	con := qaConsole{opened: make(chan struct{}, 4)}
	reg, err := core.NewRegistry(qaJobsModule{gate: gate, units: units}, con)
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, host: &fixHost{}, clock: coretest.NewFakeClock(t0), reg: reg, dir: t.TempDir(), units: units}
	f.policy = filepath.Join(f.dir, "agent-policy.json")
	if policyText != "" {
		writePolicy(t, f.dir, policyText, 0o644)
	}
	f.open()
	return f, units, con
}

func qaJobsOff(err error) bool {
	var e *errcode.Error
	return errors.As(err, &e) && e.Code == errcode.ModuleUnavailable && strings.Contains(e.Detail+err.Error(), "jobs is off on this host")
}

func TestA26_RuntimeRefusesJobStartsWhileJobsIsOffOrDenied(t *testing.T) {
	ctx := context.Background()
	f, units, _ := qaFixture(t, nil, "")
	if err := f.rt.SetModuleEnabled(ctx, "jobs", true); err != nil {
		t.Fatal(err)
	}
	u := f.rt.moduleEnv("jobs").Units

	assertRefused := func(when string) {
		t.Helper()
		units.reset()
		for name, err := range map[string]error{
			"Start":   u.Start(ctx, qaTimer),
			"Restart": u.Restart(ctx, "darkpanel-job-qa.service"),
			"Enable":  u.Enable(ctx, qaTimer),
		} {
			if !qaJobsOff(err) {
				t.Errorf("%s of a job unit %s: %v, want module.unavailable: jobs is off on this host", name, when, err)
			}
		}
		if got := units.calls(); len(got) != 0 {
			t.Errorf("the unit client got %q %s, want no call", got, when)
		}
		if err := u.Stop(ctx, qaTimer); err != nil {
			t.Errorf("Stop %s: %v", when, err)
		}
		if err := u.Disable(ctx, qaTimer); err != nil {
			t.Errorf("Disable %s: %v", when, err)
		}
		tr, ok := u.(core.TimerReader)
		if !ok {
			t.Fatal("the jobs unit client is no TimerReader")
		}
		if _, err := tr.Timers(ctx); err != nil {
			t.Errorf("Timers %s: %v", when, err)
		}
		if _, err := tr.LastRun(ctx, "darkpanel-job-qa.service"); err != nil {
			t.Errorf("LastRun %s: %v", when, err)
		}
	}

	if err := f.rt.SetModuleEnabled(ctx, "jobs", false); err != nil {
		t.Fatal(err)
	}
	assertRefused("while jobs is turned off")

	// With the flag back on, only the policy can refuse.
	if err := f.rt.SetModuleEnabled(ctx, "jobs", true); err != nil {
		t.Fatal(err)
	}
	writePolicy(t, f.dir, `{"denyModules":["jobs"]}`, 0o644)
	if err := f.rt.ReloadPolicy(ctx); err != nil {
		t.Fatal(err)
	}
	assertRefused("while the policy denies jobs")

	writePolicy(t, f.dir, `{"denyModules":[]}`, 0o644)
	if err := f.rt.ReloadPolicy(ctx); err != nil {
		t.Fatal(err)
	}
	units.reset()
	for name, err := range map[string]error{"Start": u.Start(ctx, qaTimer), "Enable": u.Enable(ctx, qaTimer)} {
		if err != nil {
			t.Errorf("%s with jobs on and allowed: %v", name, err)
		}
	}
	if got := units.calls(); len(got) != 2 {
		t.Errorf("the unit client got %q with jobs on, want a Start and an Enable", got)
	}
}

// A start that passed the guard before jobs turned off is stopped by the off
// hook: the hook waits for the start to return.
func TestA26_OffHookStopsATimerWhoseStartPassedTheGuard(t *testing.T) {
	ctx := context.Background()
	f, units, _ := qaFixture(t, nil, "")
	if err := f.rt.SetModuleEnabled(ctx, "jobs", true); err != nil {
		t.Fatal(err)
	}
	units.reset()
	entered, hold := make(chan struct{}), make(chan struct{})
	units.mu.Lock()
	units.entered, units.hold = entered, hold
	units.mu.Unlock()
	u := f.rt.moduleEnv("jobs").Units
	started := make(chan error, 1)
	go func() { started <- u.Start(ctx, qaTimer) }()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the guarded Start never reached the unit client")
	}
	units.mu.Lock()
	units.hold = nil
	units.mu.Unlock()
	off := make(chan error, 1)
	go func() { off <- f.rt.SetModuleEnabled(ctx, "jobs", false) }()
	// Give the off call every chance to run its hook while the start is held.
	time.Sleep(200 * time.Millisecond)
	if got := units.calls(); len(got) != 0 {
		t.Errorf("the unit client got %q while the start was in flight, want nothing yet", got)
	}
	close(hold)
	for name, ch := range map[string]chan error{"Start": started, "SetModuleEnabled(false)": off} {
		select {
		case err := <-ch:
			if err != nil {
				t.Errorf("%s: %v", name, err)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("%s never returned", name)
		}
	}
	want := []string{"Start " + qaTimer, "Stop " + qaTimer, "Disable " + qaTimer}
	if got := units.calls(); !slices.Equal(got, want) {
		t.Errorf("unit calls %q, want %q: the hook must stop the timer the start started", got, want)
	}
}

func TestA28_PreflightRacingAReloadThatAllowsJobsEndsAvailable(t *testing.T) {
	ctx := context.Background()
	gate := &qaGate{}
	f, _, con := qaFixture(t, gate, "")
	// A check begun before the reloads and finishing after them has a stale
	// verdict: here, a failure.
	gate.arm(errors.New("jobs: the host is not ready"))
	done := make(chan core.ModuleStatus, 1)
	go func() {
		st, err := f.rt.Preflight(ctx, "jobs")
		if err != nil {
			t.Error(err)
		}
		done <- st
	}()
	<-gate.entered
	for _, text := range []string{`{"denyModules":["jobs"]}`, `{"denyModules":[]}`} {
		writePolicy(t, f.dir, text, 0o644)
		if err := f.rt.ReloadPolicy(ctx); err != nil {
			t.Fatal(err)
		}
	}
	close(gate.release)
	select {
	case st := <-done:
		if !st.Available {
			t.Errorf("Preflight raced by a reload that allowed jobs returned %+v, want available", st)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Preflight never returned")
	}
	if st := f.helloStatus("jobs"); !st.Available {
		t.Errorf("Hello reports jobs as %+v, want available", st)
	}

	// A check that takes long delays no console open.
	gate.arm(nil)
	held := make(chan error, 1)
	go func() { _, err := f.rt.Preflight(ctx, "jobs"); held <- err }()
	<-gate.entered
	a, b := net.Pipe()
	defer func() { _ = a.Close(); _ = b.Close() }()
	opened := make(chan error, 1)
	go func() {
		opened <- f.rt.OpenChannel(ctx, alice, "console", "terminal", url.Values{}, a)
	}()
	select {
	case <-con.opened:
	case <-time.After(time.Second):
		t.Error("a console open waited for a held jobs check")
	}
	_ = b.Close()
	<-opened
	close(gate.release)
	if err := <-held; err != nil {
		t.Error(err)
	}
}

func TestA32_ReloadJournalsTheModulesWhoseAvailabilityChanged(t *testing.T) {
	ctx := context.Background()
	f, _, _ := qaFixture(t, nil, "")
	reloads := func() []core.JournalEntry {
		t.Helper()
		es, err := f.rt.Journal(ctx, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		var out []core.JournalEntry
		for _, e := range es {
			if e.Action == "agent.policy.reload" {
				out = append(out, e)
			}
		}
		return out
	}
	reload := func(text string) {
		t.Helper()
		writePolicy(t, f.dir, text, 0o644)
		if err := f.rt.ReloadPolicy(ctx); err != nil {
			t.Fatal(err)
		}
	}
	reload(`{"denyModules":["console","jobs"]}`)
	es := reloads()
	if len(es) != 1 {
		t.Fatalf("journal holds %d policy reload entries after denying console and jobs, want 1", len(es))
	}
	var in struct {
		Modules []string `json:"modules"`
	}
	if err := json.Unmarshal(es[0].Input, &in); err != nil || !slices.Equal(in.Modules, []string{"console", "jobs"}) {
		t.Errorf("entry input %s (%v), want the modules console and jobs", es[0].Input, err)
	}
	reload(`{"denyModules":["console","jobs"]}`)
	if n := len(reloads()); n != 1 {
		t.Errorf("a reload that changed nothing journaled an entry (%d entries)", n)
	}
	reload(`{"denyModules":["console"]}`)
	es = reloads()
	if len(es) != 2 {
		t.Fatalf("journal holds %d reload entries after allowing jobs, want 2", len(es))
	}
	if err := json.Unmarshal(es[1].Input, &in); err != nil || !slices.Equal(in.Modules, []string{"jobs"}) {
		t.Errorf("second entry input %s (%v), want the module jobs only", es[1].Input, err)
	}
}
