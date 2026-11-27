package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
	"github.com/darkraise/darkpanel/internal/core/store"
)

func TestOpenRequiresItsConfig(t *testing.T) {
	f := newFixture(t)
	breaks := map[string]func(*Config){
		"DB":            func(c *Config) { c.DB = nil },
		"Sealer":        func(c *Config) { c.Sealer = nil },
		"Clock":         func(c *Config) { c.Clock = nil },
		"Log":           func(c *Config) { c.Log = nil },
		"Registry":      func(c *Config) { c.Registry = nil },
		"HostID":        func(c *Config) { c.HostID = "" },
		"BinaryVersion": func(c *Config) { c.BinaryVersion = "" },
		"BackupDir":     func(c *Config) { c.BackupDir = "" },
		"Exec":          func(c *Config) { c.Exec = nil },
	}
	for name, brk := range breaks {
		c := f.config()
		brk(&c)
		rt, err := Open(context.Background(), c)
		if err == nil {
			rt.Close()
		}
		if err == nil || !strings.Contains(err.Error(), "Config."+name) {
			t.Errorf("%s: Open = %v, want an error naming Config.%s", name, err, name)
		}
	}
}

func TestOpenMigratesAndChecksEveryModule(t *testing.T) {
	f := newFixture(t)
	if n := f.count("SELECT count(*) FROM sqlite_master WHERE name = 'kv_items'"); n != 1 {
		t.Error("kv's agent migration did not run")
	}
	if n := f.count("SELECT count(*) FROM pragma_table_info('core_journal') WHERE name = 'source'"); n != 1 {
		t.Error("the core agent migrations did not run")
	}
	want := []core.ModuleStatus{
		{ID: "kv", Version: 1, Available: true},
		{ID: "bad", Version: 1, Reason: ReasonMigrationFailed},
		{ID: "flaky", Version: 2, Reason: "flaky: the host is not ready"},
		{ID: "boom", Version: 1, Reason: "preflight check explode of boom panicked"},
	}
	got := f.rt.statuses()
	if len(got) != len(want) {
		t.Fatalf("statuses = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("status %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if f.host.badChecks != 0 {
		t.Errorf("bad's preflight ran %d times, want 0 after its migration failed", f.host.badChecks)
	}
}

func TestOpenLogsAFailedModuleMigration(t *testing.T) {
	f := newFixture(t)
	f.rt.Close()
	var buf bytes.Buffer
	c := f.config()
	c.Log = slog.New(slog.NewTextHandler(&buf, nil))
	rt, err := Open(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	// The failed migration is retried on every start and logged with its cause.
	if text := buf.String(); !strings.Contains(text, "runtime: module migrations failed") ||
		!strings.Contains(text, "module=bad") || !strings.Contains(text, "0001_bad") {
		t.Errorf("log does not report bad's migration:\n%s", text)
	}
}

func TestOpenWarnsWhenBackupsCannotBePruned(t *testing.T) {
	f := newFixture(t)
	f.rt.Close()
	notDir := filepath.Join(f.dir, "not-a-directory")
	if err := os.WriteFile(notDir, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	c := f.config()
	c.BackupDir = notDir
	c.Log = slog.New(slog.NewTextHandler(&buf, nil))
	rt, err := Open(context.Background(), c)
	if err != nil {
		t.Fatalf("Open: %v (a failed prune must not stop the runtime)", err)
	}
	defer rt.Close()
	if text := buf.String(); !strings.Contains(text, `level=WARN msg="runtime: old agent database backups could not be pruned"`) {
		t.Errorf("log lacks the prune warning:\n%s", text)
	}
}

func TestHandlersGetTheAgentEnv(t *testing.T) {
	f := newFixture(t)
	raw, err := f.rt.engine.Read(context.Background(), alice, refGet, json.RawMessage(`{"name":"a"}`))
	if err != nil {
		t.Fatal(err)
	}
	var out itemOut
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out.HostID != testHost || out.Actor != alice.Actor || !out.HasFiles || !out.HasExec {
		t.Errorf("handler saw %+v, want host h1, alice, files and exec", out)
	}
}

func TestOpenRecoversAndPurges(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	pl, err := f.rt.engine.Plan(ctx, alice, refSet, json.RawMessage(`{"name":"a","value":"1"}`), core.PlanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	v, err := f.rt.engine.Apply(ctx, alice, pl.PlanID, core.ApplyOptions{ConfirmTimeout: lifecycle.MaxConfirmTimeout})
	if err != nil || v.State != core.StatePending {
		t.Fatalf("Apply = %+v, %v; want pending", v, err)
	}
	unused, err := f.rt.engine.Plan(ctx, alice, refSet, json.RawMessage(`{"name":"b","value":"1"}`), core.PlanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	f.rt.Close() // stops the purge loop and the revert timer; the change stays pending
	f.clock.Advance(lifecycle.PlanTTL)
	f.restart()

	// Recovery settles the change on a background goroutine after Open returns.
	var got core.ChangeView
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if got, err = f.rt.engine.Change(ctx, v.ID); err != nil || got.State == core.StateReverted || time.Now().After(deadline) {
			break
		}
	}
	if err != nil || got.State != core.StateReverted {
		t.Errorf("change after restart = %+v, %v; want reverted by recovery", got, err)
	}
	if n := f.count("SELECT count(*) FROM kv_items"); n != 0 {
		t.Errorf("kv_items rows = %d, want 0 after the revert", n)
	}
	if n := f.count("SELECT count(*) FROM core_journal WHERE phase = 'recover' AND change_id = ?", v.ID); n != 1 {
		t.Errorf("recover entries = %d, want 1", n)
	}
	if n := f.count("SELECT count(*) FROM core_plans WHERE id = ?", unused.PlanID); n != 0 {
		t.Errorf("the expired unused plan was kept")
	}
}

func TestPurgeRunsEveryIntervalUntilClose(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if p := f.clock.Pending(); p != 1 {
		t.Fatalf("armed timers = %d, want the purge timer", p)
	}
	pl, err := f.rt.engine.Plan(ctx, alice, refSet, json.RawMessage(`{"name":"a","value":"1"}`), core.PlanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	f.clock.Advance(PurgeInterval)
	if n := f.count("SELECT count(*) FROM core_plans WHERE id = ?", pl.PlanID); n != 0 {
		t.Error("the purge loop kept an expired plan")
	}
	if p := f.clock.Pending(); p != 1 {
		t.Errorf("armed timers after a purge = %d, want 1", p)
	}
	f.rt.Close()
	if p := f.clock.Pending(); p != 0 {
		t.Errorf("armed timers after Close = %d, want 0", p)
	}
	f.rt.Close() // a second Close is harmless
}

// countingShell is Config.Shell; it counts the shells it was asked for.
type countingShell struct{ n atomic.Int32 }

func (s *countingShell) Start(context.Context, uint16, uint16) (core.ShellSession, error) {
	s.n.Add(1)
	return nil, nil
}

func TestShellWrapperRefuses(t *testing.T) {
	f, _, _ := policyFixture(t, "")
	sh := &countingShell{}
	f.shell = sh
	f.restart()
	ctx := context.Background()
	start := func() error {
		t.Helper()
		_, err := f.rt.moduleEnv("console").Shell.Start(ctx, 80, 24)
		return err
	}
	if env := f.rt.moduleEnv("jobs"); env.Shell != nil || env.Journal != nil {
		t.Error("a module other than console has a shell or the module journal")
	}
	wantRefused := func(why string, code errcode.Code, calls int32) {
		t.Helper()
		if err := start(); errcode.From(err).Code != code {
			t.Errorf("%s: Start = %v, want %s", why, err, code)
		}
		if n := sh.n.Load(); n != calls {
			t.Errorf("%s: the platform shell was asked %d times, want %d", why, n, calls)
		}
	}
	wantRefused("flag 0", errcode.ModuleUnavailable, 0)
	if err := f.rt.SetModuleEnabled(ctx, "console", true); err != nil {
		t.Fatal(err)
	}
	if err := start(); err != nil || sh.n.Load() != 1 {
		t.Fatalf("enabled: Start = %v after %d calls, want the platform shell", err, sh.n.Load())
	}
	writePolicy(t, f.dir, `{"denyModules":["console"]}`, 0o644)
	if err := f.rt.ReloadPolicy(ctx); err != nil {
		t.Fatal(err)
	}
	wantRefused("denied by the policy file", errcode.ModuleUnavailable, 1)
	writePolicy(t, f.dir, `{"denyModules":[]}`, 0o644)
	f.shell = nil
	f.restart()
	wantRefused("no platform shell", errcode.Unavailable, 1)
}

func TestModuleJournal(t *testing.T) {
	f, _, _ := policyFixture(t, "")
	ctx := context.Background()
	e := core.JournalEntry{
		Actor: "user:a", Action: "console.terminal.open", Version: 1, Phase: "open",
		Input: json.RawMessage(`{"source":"192.0.2.1"}`), Result: "ok", Source: "192.0.2.1",
	}
	if err := f.rt.moduleEnv("console").Journal.Record(ctx, e); err != nil {
		t.Fatal(err)
	}
	es, err := f.rt.Journal(ctx, 0, 0)
	if err != nil || len(es) == 0 {
		t.Fatalf("Journal = %v, %v", es, err)
	}
	got := es[len(es)-1]
	e.Seq, e.At = got.Seq, t0
	if !got.At.Equal(t0) || got.Seq == 0 || got.Actor != e.Actor || got.Action != e.Action || got.Phase != e.Phase ||
		string(got.Input) != string(e.Input) || got.Source != e.Source || got.Result != e.Result {
		t.Errorf("journal entry = %+v, want %+v at the runtime's clock", got, e)
	}
	// A phase the controller's audit refuses would stop the host's ingestion.
	e.Phase = "opened"
	if err := f.rt.moduleEnv("console").Journal.Record(ctx, e); err == nil {
		t.Error("a module journal entry in phase opened was recorded")
	}
	if es2, _ := f.rt.Journal(ctx, 0, 0); len(es2) != len(es) {
		t.Errorf("the refused entry reached the journal: %d entries, want %d", len(es2), len(es))
	}
}

// guardUnits is Config.Units of the jobs guard tests: it records each call
// it gets in log and, while block is set, holds Start until block is closed,
// closing entered as Start arrives.
type guardUnits struct {
	core.Units
	mu      sync.Mutex
	log     []string
	entered chan struct{}
	block   chan struct{}
}

func (u *guardUnits) record(call string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.log = append(u.log, call)
}

func (u *guardUnits) calls() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return slices.Clone(u.log)
}

func (u *guardUnits) reset() {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.log = nil
}

func (u *guardUnits) Start(_ context.Context, unit string) error {
	u.mu.Lock()
	entered, block := u.entered, u.block
	u.mu.Unlock()
	if block != nil {
		close(entered)
		<-block
	}
	u.record("Start " + unit)
	return nil
}

func (u *guardUnits) Stop(_ context.Context, unit string) error {
	u.record("Stop " + unit)
	return nil
}

func (u *guardUnits) Restart(_ context.Context, unit string) error {
	u.record("Restart " + unit)
	return nil
}

func (u *guardUnits) Enable(_ context.Context, files ...string) error {
	u.record("Enable " + strings.Join(files, " "))
	return nil
}

func (u *guardUnits) Disable(_ context.Context, files ...string) error {
	u.record("Disable " + strings.Join(files, " "))
	return nil
}

func (u *guardUnits) Timers(context.Context) ([]core.TimerInfo, error) {
	u.record("Timers")
	return []core.TimerInfo{{Name: guardTimer, Unit: "darkpanel-job-a.service"}}, nil
}

func (u *guardUnits) LastRun(context.Context, string) (core.ServiceRun, error) {
	return core.ServiceRun{}, nil
}

const guardTimer = "darkpanel-job-a.timer"

// guardHost is what the guard tests' jobs module reports: ran is closed and
// reverted gets Start's error when a revert of jobs.timer.set calls Start.
type guardHost struct {
	units    *guardUnits
	once     sync.Once
	ran      chan struct{}
	reverted chan error
}

// gjSet is jobs.timer.set: a revertible action whose revert starts the job
// timer, as the jobs module's undo of a delete does.
type gjSet struct{ h *guardHost }

func (gjSet) Plan(context.Context, core.Env, uexIn) (core.PlanOutcome, error) {
	return core.PlanOutcome{StateHash: "gj"}, nil
}
func (gjSet) Apply(_ context.Context, _ core.Env, in uexIn) (uexIn, error) { return in, nil }
func (gjSet) Verify(context.Context, core.Env, uexIn, uexIn) error         { return nil }
func (gjSet) Snapshot(context.Context, core.Env, uexIn) ([]byte, error)    { return []byte("{}"), nil }
func (x gjSet) Revert(ctx context.Context, env core.Env, _ []byte) (core.RevertOutcome, error) {
	err := env.Units.Start(ctx, guardTimer)
	x.h.reverted <- err
	x.h.once.Do(func() { close(x.h.ran) })
	return core.RevertOutcome{}, nil
}

// guardJobs is a module named jobs owning the darkpanel-job-* unit set, with
// jobs.timer.set and an off hook that stops and disables the job timer
// through its unit client, as the jobs module's does.
type guardJobs struct{ h *guardHost }

func (m guardJobs) Manifest() core.Manifest {
	return core.Manifest{
		ID: "jobs", Version: 1,
		OwnedPaths: []core.OwnedPath{{Path: core.UnitSetDir, Kind: core.OwnedUnitSet, Pattern: `^darkpanel-job-[a-z]+\.(service|timer)$`}},
		Actions: []core.ActionDef{
			core.DefineRevertible(core.ActionSpec{
				Name: "jobs.timer.set", Version: 1, Side: core.SideAgent, LockKeys: []string{"jobs"}, RepairedBy: "jobs.timer.repair",
			}, gjSet(m)),
			core.DefineMutating(core.ActionSpec{
				Name: "jobs.timer.repair", Version: 1, Class: core.ClassCompensable, Side: core.SideAgent,
				LockKeys: []string{"jobs"}, ForwardOnly: true, Converges: true,
			}, repairItem{}),
		},
	}
}

func (m guardJobs) OnEnabled(ctx context.Context, env core.Env, enabled bool) error {
	m.h.units.record(fmt.Sprintf("OnEnabled %t", enabled))
	if enabled {
		return nil
	}
	return errors.Join(env.Units.Stop(ctx, guardTimer), env.Units.Disable(ctx, guardTimer))
}

// guardFixture is a runtime with the jobs module guardJobs over a
// guardUnits client and a policy file path in its directory, absent until a
// test writes it.
func guardFixture(t *testing.T) (*fixture, *guardHost) {
	t.Helper()
	h := &guardHost{units: &guardUnits{}, ran: make(chan struct{}), reverted: make(chan error, 1)}
	reg, err := core.NewRegistry(kvModule{}, guardJobs{h})
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, host: &fixHost{}, clock: coretest.NewFakeClock(t0), reg: reg, dir: t.TempDir(), units: h.units}
	f.policy = filepath.Join(f.dir, "agent-policy.json")
	f.open()
	return f, h
}

func isJobsOff(err error) bool {
	var e *errcode.Error
	return errors.As(err, &e) && e.Code == errcode.ModuleUnavailable && strings.Contains(err.Error(), "jobs is off on this host")
}

// TestJobsUnitGuard refuses Start, Restart and Enable of a job unit while
// jobs is off or denied, and passes everything else through.
func TestJobsUnitGuard(t *testing.T) {
	f, h := guardFixture(t)
	ctx := context.Background()
	if err := f.rt.SetModuleEnabled(ctx, "jobs", false); err != nil {
		t.Fatal(err)
	}
	h.units.reset()
	u := f.rt.moduleEnv("jobs").Units
	if err := u.Start(ctx, guardTimer); !isJobsOff(err) {
		t.Errorf("Start with the flag 0: %v, want module.unavailable: jobs is off on this host", err)
	}
	if err := u.Restart(ctx, "darkpanel-job-a.service"); !isJobsOff(err) {
		t.Errorf("Restart with the flag 0: %v, want jobs is off on this host", err)
	}
	if err := u.Enable(ctx, guardTimer); !isJobsOff(err) {
		t.Errorf("Enable with the flag 0: %v, want jobs is off on this host", err)
	}
	if got := h.units.calls(); len(got) != 0 {
		t.Errorf("the unit client got %q while jobs is off, want no call", got)
	}
	if err := u.Stop(ctx, guardTimer); err != nil {
		t.Errorf("Stop with the flag 0: %v", err)
	}
	tr, ok := u.(core.TimerReader)
	if !ok {
		t.Fatal("the jobs unit client is no TimerReader")
	}
	if timers, err := tr.Timers(ctx); err != nil || len(timers) != 1 {
		t.Errorf("Timers with the flag 0 = %+v, %v; want the client's one timer", timers, err)
	}
	if want := []string{"Stop " + guardTimer, "Timers"}; !slices.Equal(h.units.calls(), want) {
		t.Errorf("the unit client got %q, want %q", h.units.calls(), want)
	}

	writePolicy(t, f.dir, `{"denyModules":["jobs"]}`, 0o644)
	if err := f.rt.ReloadPolicy(ctx); err != nil {
		t.Fatal(err)
	}
	if err := f.rt.SetModuleEnabled(ctx, "jobs", true); err == nil {
		t.Fatal("SetModuleEnabled(jobs, true) under a policy denying jobs succeeded")
	}
	if v, ok := f.enMeta("jobs"); !ok || v != "1" {
		t.Fatalf("jobs flag %q (%v), want 1: the case below checks the policy alone", v, ok)
	}
	h.units.reset()
	if err := u.Enable(ctx, guardTimer); !isJobsOff(err) {
		t.Errorf("Enable with the flag 1 and jobs denied: %v, want jobs is off on this host", err)
	}
	if got := h.units.calls(); len(got) != 0 {
		t.Errorf("the unit client got %q while jobs is denied, want no call", got)
	}

	writePolicy(t, f.dir, `{"denyModules":[]}`, 0o644)
	if err := f.rt.ReloadPolicy(ctx); err != nil {
		t.Fatal(err)
	}
	h.units.reset()
	if err := u.Enable(ctx, guardTimer); err != nil {
		t.Errorf("Enable with jobs on and allowed: %v", err)
	}
	if err := u.Start(ctx, guardTimer); err != nil {
		t.Errorf("Start with jobs on and allowed: %v", err)
	}
	if want := []string{"Enable " + guardTimer, "Start " + guardTimer}; !slices.Equal(h.units.calls(), want) {
		t.Errorf("the unit client got %q, want %q", h.units.calls(), want)
	}
}

// TestGuardHoldsAgainstOffHook runs the off hook only once a guarded Start
// that passed its check has returned, so the hook stops the timer it
// started.
func TestGuardHoldsAgainstOffHook(t *testing.T) {
	f, h := guardFixture(t)
	ctx := context.Background()
	if err := f.rt.SetModuleEnabled(ctx, "jobs", true); err != nil {
		t.Fatal(err)
	}
	h.units.reset()
	entered, block := make(chan struct{}), make(chan struct{})
	h.units.mu.Lock()
	h.units.entered, h.units.block = entered, block
	h.units.mu.Unlock()
	u := f.rt.moduleEnv("jobs").Units
	started := make(chan error, 1)
	go func() { started <- u.Start(ctx, guardTimer) }()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the guarded Start never reached the unit client")
	}
	h.units.mu.Lock()
	h.units.block = nil
	h.units.mu.Unlock()
	off := make(chan error, 1)
	go func() { off <- f.rt.SetModuleEnabled(ctx, "jobs", false) }()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		if v, _ := f.enMeta("jobs"); v == "0" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("SetModuleEnabled(jobs, false) never stored the flag")
		}
	}
	// Without the guard's lock the off hook runs now, while Start is held.
	time.Sleep(50 * time.Millisecond)
	close(block)
	for name, ch := range map[string]chan error{"Start": started, "SetModuleEnabled(jobs, false)": off} {
		select {
		case err := <-ch:
			if err != nil {
				t.Errorf("%s: %v", name, err)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("%s did not return: the off hook and the guard deadlock", name)
		}
	}
	want := []string{"Start " + guardTimer, "OnEnabled false", "Stop " + guardTimer, "Disable " + guardTimer}
	if got := h.units.calls(); !slices.Equal(got, want) {
		t.Errorf("calls %q, want %q: the off hook must wait for the Start", got, want)
	}
}

// policyGate is a log handler that holds the "policy file is invalid" error
// until the jobs revert has run, but only when recovery has claimed change
// already: with the policy read after Recover, the revert then runs before
// the policy is in force.
type policyGate struct {
	db     *store.DB
	change string
	ran    <-chan struct{}
}

func (g policyGate) Enabled(context.Context, slog.Level) bool { return true }
func (g policyGate) Handle(ctx context.Context, rec slog.Record) error {
	if !strings.HasPrefix(rec.Message, "runtime: the policy file is invalid") {
		return nil
	}
	var owner string
	if err := g.db.Read().QueryRowContext(ctx, "SELECT recovery_owner FROM core_changes WHERE id = ?", g.change).Scan(&owner); err != nil || owner == "" {
		return nil
	}
	select {
	case <-g.ran:
	case <-time.After(10 * time.Second):
	}
	return nil
}
func (g policyGate) WithAttrs([]slog.Attr) slog.Handler { return g }
func (g policyGate) WithGroup(string) slog.Handler      { return g }

// TestPolicyLoadedBeforeRecover reads the policy file before recovery runs,
// so a jobs revert that recovery resumes meets a guard that already denies
// jobs. The file is invalid, which denies jobs and logs before the policy
// is set, so the log handler can hold an agent that read it too late.
func TestPolicyLoadedBeforeRecover(t *testing.T) {
	f, h := guardFixture(t)
	ctx := context.Background()
	if err := f.rt.SetModuleEnabled(ctx, "jobs", true); err != nil {
		t.Fatal(err)
	}
	ref := core.ActionRef{Name: "jobs.timer.set", Version: 1}
	pl, err := f.rt.engine.Plan(ctx, alice, ref, json.RawMessage(`{"name":"a"}`), core.PlanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	v, err := f.rt.engine.Apply(ctx, alice, pl.PlanID, core.ApplyOptions{ConfirmTimeout: lifecycle.MaxConfirmTimeout})
	if err != nil || v.State != core.StatePending {
		t.Fatalf("Apply = %+v, %v; want pending", v, err)
	}
	f.rt.Close()
	writePolicy(t, f.dir, `{`, 0o644)
	c := f.config()
	c.Log = slog.New(policyGate{db: f.db, change: v.ID, ran: h.ran})
	rt, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(rt.Close)
	select {
	case err := <-h.reverted:
		if !isJobsOff(err) {
			t.Errorf("the recovered revert's Start: %v, want jobs is off on this host", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("recovery never reverted the pending jobs change")
	}
}
