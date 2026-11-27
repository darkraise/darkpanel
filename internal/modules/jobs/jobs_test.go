package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
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
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/platform"
)

var (
	adminP  = core.Principal{Actor: "user:admin", UserID: "admin", Scope: core.ScopeAdmin, Source: "192.0.2.1"}
	viewerP = core.Principal{Actor: "user:viewer", UserID: "viewer", Scope: core.ScopeRead, Source: "192.0.2.2"}
)

// units is a core.Units and core.TimerReader in memory: Start makes a unit
// active and Stop inactive at once.
type units struct {
	mu     sync.Mutex
	calls  []string
	active map[string]bool
	timers []core.TimerInfo
	runs   map[string]core.ServiceRun
	// stopFails names the units whose Stop fails.
	stopFails map[string]bool
}

func (u *units) record(call string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.calls = append(u.calls, call)
}

func (u *units) set(unit string, on bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.active[unit] = on
}

func (u *units) StartTransientService(context.Context, string, core.TransientService) error {
	return errors.New("not here")
}
func (u *units) StartTransientTimer(context.Context, string, core.TransientTimer) error {
	return errors.New("not here")
}
func (u *units) Start(_ context.Context, unit string) error {
	u.record("Start " + unit)
	u.set(unit, true)
	return nil
}
func (u *units) Stop(_ context.Context, unit string) error {
	u.record("Stop " + unit)
	if u.stopFails[unit] {
		return errors.New("stop failed")
	}
	u.set(unit, false)
	return nil
}
func (u *units) Restart(_ context.Context, unit string) error {
	u.record("Restart " + unit)
	u.set(unit, true)
	return nil
}
func (u *units) Kill(context.Context, string, syscall.Signal) error { return errors.New("not here") }
func (u *units) ResetFailed(context.Context, string) error          { return errors.New("not here") }
func (u *units) Enable(_ context.Context, f ...string) error {
	u.record("Enable " + strings.Join(f, " "))
	return nil
}
func (u *units) Disable(_ context.Context, f ...string) error {
	u.record("Disable " + strings.Join(f, " "))
	return nil
}
func (u *units) Reload(context.Context) error { u.record("Reload"); return nil }
func (u *units) List(context.Context, string) ([]core.UnitStatus, error) {
	return nil, errors.New("not here")
}
func (u *units) State(_ context.Context, unit string) (core.UnitState, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.active[unit] {
		return core.UnitState{LoadState: "loaded", ActiveState: "active"}, nil
	}
	return core.UnitState{LoadState: "loaded", ActiveState: "inactive"}, nil
}
func (u *units) Timers(context.Context) ([]core.TimerInfo, error) { return slices.Clone(u.timers), nil }
func (u *units) LastRun(_ context.Context, unit string) (core.ServiceRun, error) {
	return u.runs[unit], nil
}

// runner answers systemd-analyze calendar: "every tuesday" fails as
// systemd-analyze does, anything else elapses daily at 03:00 UTC.
type runner struct{}

func (runner) Run(_ context.Context, cmd core.Cmd) (core.CmdResult, error) {
	if cmd.Path != platform.SystemdAnalyzePath || len(cmd.Args) != 4 || !slices.Equal(cmd.Args[:3], platform.CalendarArgs("")[:3]) {
		return core.CmdResult{}, fmt.Errorf("unexpected command %s %q", cmd.Path, cmd.Args)
	}
	if cmd.Args[3] == "every tuesday" {
		return core.CmdResult{ExitCode: 1, Stderr: []byte("Failed to parse calendar specification 'every tuesday': Invalid argument\n")}, nil
	}
	return core.CmdResult{Stdout: []byte("Normalized form: " + cmd.Args[3] + "\n" +
		"    Next elapse: Fri 2026-11-27 10:00:00 +07\n       (in UTC): Fri 2026-11-27 03:00:00 UTC\n       From now: 10h left\n" +
		"   Iteration #2: Sat 2026-11-28 10:00:00 +07\n       (in UTC): Sat 2026-11-28 03:00:00 UTC\n" +
		"   Iteration #3: Sun 2026-11-29 10:00:00 +07\n       (in UTC): Sun 2026-11-29 03:00:00 UTC\n")}, nil
}

func (runner) Stream(context.Context, string, []string, func([]byte) error) error {
	return errors.New("not here")
}

// rig is a host for the module: its unit and scripts directories in a
// temporary tree, the real file writer over an agent database, the units
// fake and canned account and cron files.
type rig struct {
	t        *testing.T
	root     string
	m        module
	units    *units
	files    map[string]string // canned files, by path
	gid      int               // alice's primary group
	db       *store.DB
	engine   *lifecycle.Engine
	journal  *coretest.MemJournal
	writer   *platform.Files
	ownStore *platform.Ownership
}

func newRig(t *testing.T) *rig {
	t.Helper()
	root := t.TempDir()
	r := &rig{t: t, root: root, units: &units{active: map[string]bool{}, runs: map[string]core.ServiceRun{}}, files: map[string]string{}}
	r.gid = os.Getegid()
	// carol's group is one an unprivileged run can give a file, as alice's is.
	carolGid := r.gid
	if os.Geteuid() == 0 {
		r.gid, carolGid = 4242, 4243
	}
	r.files[passwdFile] = fmt.Sprintf("root:x:0:0:root:/root:/bin/bash\nalice:x:1000:%d:Alice:/home/alice:/bin/bash\n"+
		"daemon:x:1:1:daemon:/usr/sbin:/usr/sbin/nologin\nbob:x:1001:1001:Bob:/home/bob:/usr/sbin/nologin\n"+
		"carol:x:1002:%d:Carol:/home/carol:/bin/bash\n", r.gid, carolGid)
	r.files[shellsFile] = "/bin/sh\n/bin/bash\n"
	unitDir := filepath.Join(root, "units")
	if err := os.Mkdir(unitDir, 0o750); err != nil {
		t.Fatal(err)
	}
	r.m = module{d: deps{readFile: r.readFile, glob: r.glob}, unitDir: unitDir, scriptDir: filepath.Join(root, "jobs"), wait: time.Second}
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	set, err := store.CoreMigrations(store.SideAgent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Migrate(ctx, db, store.MigrateOptions{
		Side: store.SideAgent, BinaryVersion: "test", BackupRoot: filepath.Join(t.TempDir(), "backups"), Keep: 5, Now: time.Now(),
	}, set, []store.MigrationSet{{Module: ID, FS: r.m.Manifest().Migrations[core.SideAgent]}}); err != nil {
		t.Fatal(err)
	}
	r.db = db
	r.ownStore = platform.NewOwnership(db)
	r.writer = platform.NewFiles(platform.FilesConfig{
		Owned: map[string][]core.OwnedPath{ID: {
			{Path: unitDir, Kind: core.OwnedUnitSet, Pattern: UnitPattern},
			{Path: r.m.scriptDir, Kind: core.OwnedDir, Pattern: scriptPattern},
		}},
		Ownership: r.ownStore, Clock: coretest.NewFakeClock(time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)), TestMode: true,
	})
	r.journal = &coretest.MemJournal{}
	reg, err := core.NewRegistry(r.m)
	if err != nil {
		t.Fatal(err)
	}
	eng, err := lifecycle.New(lifecycle.Config{
		Side: core.SideAgent, Registry: reg, Validator: schema.NewValidator(), Journal: r.journal, Redact: schema.Redact,
		Base: r.env(adminP), ModuleUnits: func(string) core.Units { return r.units },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(eng.Close)
	r.engine = eng
	return r
}

// env is the Env a handler of the module gets as p.
func (r *rig) env(p core.Principal) core.Env {
	return core.Env{HostID: "h1", DB: r.db, Files: r.writer, Exec: runner{}, Units: r.units, Sealer: coretest.Sealer{}, Principal: p,
		Clock: coretest.NewFakeClock(time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)), Log: slog.New(slog.DiscardHandler)}
}

// readFile serves the canned files and reads the rest from disk.
func (r *rig) readFile(name string) ([]byte, error) {
	if s, ok := r.files[name]; ok {
		return []byte(s), nil
	}
	if !strings.HasPrefix(name, r.root) {
		return nil, fs.ErrNotExist
	}
	return os.ReadFile(name) //nolint:gosec // G304: a path in the test's temporary tree
}

// glob matches the canned files and the temporary tree.
func (r *rig) glob(pattern string) ([]string, error) {
	if strings.HasPrefix(pattern, r.root) {
		return filepath.Glob(pattern)
	}
	var out []string
	for name := range r.files {
		if ok, _ := filepath.Match(pattern, name); ok {
			out = append(out, name)
		}
	}
	return out, nil
}

func (r *rig) apply(action, input string) core.ChangeView {
	r.t.Helper()
	ctx := context.Background()
	pv, err := r.engine.Plan(ctx, adminP, core.ActionRef{Name: action, Version: 1}, json.RawMessage(input), core.PlanOptions{})
	if err != nil {
		r.t.Fatalf("plan %s %s: %v", action, input, err)
	}
	cv, err := r.engine.Apply(ctx, adminP, pv.PlanID, core.ApplyOptions{})
	if err != nil || cv.State != core.StateCompleted {
		r.t.Fatalf("apply %s: %+v %+v, %v", action, cv, cv.Error, err)
	}
	return cv
}

func (r *rig) planErr(action, input string) error {
	r.t.Helper()
	_, err := r.engine.Plan(context.Background(), adminP, core.ActionRef{Name: action, Version: 1}, json.RawMessage(input), core.PlanOptions{})
	return err
}

func (r *rig) read(p core.Principal, action, input string, out any) {
	r.t.Helper()
	raw, err := r.engine.Read(context.Background(), p, core.ActionRef{Name: action, Version: 1}, json.RawMessage(input))
	if err != nil {
		r.t.Fatalf("read %s: %v", action, err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) path(dir, name string) string { return filepath.Join(r.root, dir, name) }

func (r *rig) text(path string) string {
	r.t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // G304: a path in the test's temporary tree
	if err != nil {
		r.t.Fatal(err)
	}
	return string(b)
}

func golden(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name)) //nolint:gosec // G304: a golden file of the test
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

const backupInput = `{"slug":"backup","description":"Nightly 100% backup","schedule":"*-*-* 03:00:00","user":"alice",` +
	`"command":"tar czf /tmp/x.tgz /etc","timeoutSeconds":3600,"catchUp":true,"randomDelaySeconds":300}`

// TestCreateUnits is M11 spec section 6's job: a oneshot service, its timer
// and a 0750 script in a 0755 directory, group the run-as user's.
func TestCreateUnits(t *testing.T) {
	r := newRig(t)
	r.apply("jobs.job.create", backupInput)
	for _, name := range []string{"darkpanel-job-backup.service", "darkpanel-job-backup.timer"} {
		if got, want := r.text(r.path("units", name)), golden(t, name); got != want {
			t.Errorf("%s:\n%s\nwant:\n%s", name, got, want)
		}
	}
	script := r.path("jobs", "backup.sh")
	if got := r.text(script); got != "tar czf /tmp/x.tgz /etc\n" {
		t.Errorf("script %q", got)
	}
	info, err := os.Stat(script)
	if err != nil {
		t.Fatal(err)
	}
	st := info.Sys().(*syscall.Stat_t)
	if info.Mode().Perm() != 0o750 || int(st.Gid) != r.gid || int(st.Uid) != os.Geteuid() {
		t.Errorf("script mode %o owner %d:%d, want 750 %d:%d", info.Mode().Perm(), st.Uid, st.Gid, os.Geteuid(), r.gid)
	}
	if dir, err := os.Stat(filepath.Join(r.root, "jobs")); err != nil || dir.Mode().Perm() != 0o755 {
		t.Errorf("jobs directory %v, %v; want mode 0755", dir, err)
	}
	if want := []string{"Reload", "Enable darkpanel-job-backup.timer", "Start darkpanel-job-backup.timer"}; !slices.Equal(r.units.calls, want) {
		t.Errorf("unit calls %q, want %q", r.units.calls, want)
	}
	for _, name := range []string{"create", "update"} {
		def := findAction(t, r.m, "jobs.job."+name)
		if !def.Spec.StepUp || def.Spec.MinScope != core.ScopeAdmin {
			t.Errorf("jobs.job.%s: StepUp %t, MinScope %s; want step-up and admin", name, def.Spec.StepUp, def.Spec.MinScope)
		}
	}
	for _, name := range []string{"delete", "enable", "disable", "run"} {
		if def := findAction(t, r.m, "jobs.job."+name); def.Spec.StepUp || def.Spec.MinScope != core.ScopeAdmin {
			t.Errorf("jobs.job.%s: StepUp %t, MinScope %s; want admin without step-up", name, def.Spec.StepUp, def.Spec.MinScope)
		}
	}
	if err := r.planErr("jobs.job.create", backupInput); !isInvalid(err) {
		t.Errorf("creating backup twice: %v", err)
	}
	// The journal, which the controller's audit log copies for read-scope
	// principals too, never holds the command text (spec section 0).
	for _, e := range r.journal.Entries() {
		if strings.Contains(string(e.Input), "tar czf") {
			t.Errorf("journal entry %s %s holds the command: %s", e.Action, e.Phase, e.Input)
		}
	}
	planted := r.path("units", "darkpanel-job-planted.service")
	if err := os.WriteFile(planted, []byte("[Service]\nUser=root\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err = r.planErr("jobs.job.create", strings.Replace(backupInput, `"backup"`, `"planted"`, 1))
	if !isInvalid(err) || !strings.Contains(err.Error(), "Darkpanel did not write it") {
		t.Errorf("creating over a planted unit: %v", err)
	}
	for field, input := range map[string]string{
		"a nologin user":  strings.NewReplacer(`"alice"`, `"bob"`, `"backup"`, `"other"`).Replace(backupInput),
		"a system user":   strings.NewReplacer(`"alice"`, `"daemon"`, `"backup"`, `"other"`).Replace(backupInput),
		"/timeoutSeconds": `{"slug":"other","schedule":"daily","user":"root","command":"true","timeoutSeconds":30}`,
	} {
		if err := r.planErr("jobs.job.create", input); !isInvalid(err) {
			t.Errorf("create with a bad %s: %v", field, err)
		}
	}
}

func isInvalid(err error) bool {
	var e *errcode.Error
	return errors.As(err, &e) && e.Code == errcode.InvalidInput
}

func findAction(t *testing.T, m module, name string) core.ActionDef {
	t.Helper()
	for _, a := range m.Manifest().Actions {
		if a.Spec.Name == name {
			return a
		}
	}
	t.Fatalf("no action %s", name)
	return core.ActionDef{}
}

// TestScheduleInvalid refuses a schedule systemd-analyze cannot parse with
// its message and shows the next three runs of a valid one.
func TestScheduleInvalid(t *testing.T) {
	r := newRig(t)
	err := r.planErr("jobs.job.create", strings.Replace(backupInput, `*-*-* 03:00:00`, `every tuesday`, 1))
	if !isInvalid(err) || !strings.Contains(err.Error(), "Failed to parse calendar specification 'every tuesday': Invalid argument") {
		t.Errorf("plan with every tuesday: %v", err)
	}
	var check ScheduleCheck
	r.read(adminP, "jobs.schedule.check", `{"schedule":"*-*-* 03:00:00"}`, &check)
	if want := []string{"2026-11-27T03:00:00Z", "2026-11-28T03:00:00Z", "2026-11-29T03:00:00Z"}; !slices.Equal(check.Next, want) {
		t.Errorf("next runs %q, want %q", check.Next, want)
	}
	// A host in UTC prints no "(in UTC)" lines.
	next, err := parseCalendar("Normalized form: daily\n    Next elapse: Fri 2026-11-27 00:00:00 UTC\n" +
		"   Iteration #2: Sat 2026-11-28 00:00:00 UTC\n   Iteration #3: Sun 2026-11-29 00:00:00 UTC\n")
	if want := []string{"2026-11-27T00:00:00Z", "2026-11-28T00:00:00Z", "2026-11-29T00:00:00Z"}; err != nil || !slices.Equal(next, want) {
		t.Errorf("next runs in UTC %q, %v; want %q", next, err, want)
	}
}

// undoChange undoes a change and waits until the undo has settled.
func (r *rig) undoChange(changeID string) core.ChangeView {
	r.t.Helper()
	ctx := context.Background()
	if _, err := r.engine.Undo(ctx, adminP, changeID, nil); err != nil {
		r.t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		v, err := r.engine.Change(ctx, changeID)
		if err != nil {
			r.t.Fatal(err)
		}
		if v.State != core.StateUndoing || time.Now().After(deadline) {
			return v
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestUpdateUndo undoes an update to the previous units and script byte for
// byte, and runs a job now with no typed phrase.
func TestUpdateUndo(t *testing.T) {
	r := newRig(t)
	r.apply("jobs.job.create", backupInput)
	paths := []string{r.path("units", "darkpanel-job-backup.service"), r.path("units", "darkpanel-job-backup.timer"), r.path("jobs", "backup.sh")}
	var before []string
	for _, p := range paths {
		before = append(before, r.text(p))
	}
	updated := strings.NewReplacer(`*-*-* 03:00:00`, `daily`, `tar czf /tmp/x.tgz /etc`, `rsync -a /srv /backup`, `"alice"`, `"carol"`).Replace(backupInput)
	r.units.calls = nil
	cv := r.apply("jobs.job.update", updated)
	if want := []string{"Reload", "Restart darkpanel-job-backup.timer"}; !slices.Equal(r.units.calls, want) {
		t.Errorf("update calls %q, want %q: a running timer takes its new schedule on a restart", r.units.calls, want)
	}
	if got := r.text(paths[2]); got != "rsync -a /srv /backup\n" {
		t.Fatalf("updated script %q", got)
	}
	if got := r.text(paths[1]); !strings.Contains(got, "OnCalendar=daily\n") {
		t.Fatalf("updated timer:\n%s", got)
	}
	if def := findAction(t, r.m, "jobs.job.update"); def.Spec.Class != core.ClassCompensable || def.Spec.TypedConfirmation {
		t.Errorf("update class %s, typed confirmation %t", def.Spec.Class, def.Spec.TypedConfirmation)
	}
	// A viewer reads a change's effects, and the command text is admin-only
	// (spec section 0): the effect names each script by its digest.
	views, err := r.engine.Effects(context.Background(), cv.ID)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(views)
	if len(views) == 0 || strings.Contains(string(raw), "tar czf") || strings.Contains(string(raw), "rsync -a") {
		t.Errorf("the update's effects show a command: %s", raw)
	}
	if v := r.undoChange(cv.ID); v.State != core.StateReverted {
		t.Fatalf("undo: %+v", v)
	}
	for i, p := range paths {
		if got := r.text(p); got != before[i] {
			t.Errorf("%s after the undo:\n%s\nwant:\n%s", p, got, before[i])
		}
	}
	info, err := os.Stat(paths[2])
	if err != nil {
		t.Fatal(err)
	}
	if int(info.Sys().(*syscall.Stat_t).Gid) != r.gid {
		t.Errorf("the undone script's group is %d, want alice's %d", info.Sys().(*syscall.Stat_t).Gid, r.gid)
	}

	r.units.calls = nil
	r.apply("jobs.job.run", `{"slug":"backup"}`)
	if want := []string{"Start darkpanel-job-backup.service"}; !slices.Equal(r.units.calls, want) {
		t.Errorf("run calls %q, want %q", r.units.calls, want)
	}
	if def := findAction(t, r.m, "jobs.job.run"); !def.Spec.ForwardOnly || def.Spec.TypedConfirmation {
		t.Errorf("run: forward-only %t, typed confirmation %t", def.Spec.ForwardOnly, def.Spec.TypedConfirmation)
	}

	// Delete removes every file and the timer; its undo brings them back.
	r.units.calls = nil
	cv = r.apply("jobs.job.delete", `{"slug":"backup"}`)
	for _, p := range paths {
		if _, err := os.Lstat(p); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s after delete: %v", p, err)
		}
	}
	if want := []string{"Stop darkpanel-job-backup.timer", "Disable darkpanel-job-backup.timer", "Reload"}; !slices.Equal(r.units.calls, want) {
		t.Errorf("delete calls %q, want %q", r.units.calls, want)
	}
	if v := r.undoChange(cv.ID); v.State != core.StateReverted {
		t.Fatalf("undo the delete: %+v", v)
	}
	for i, p := range paths {
		if got := r.text(p); got != before[i] {
			t.Errorf("%s after undoing the delete differs", p)
		}
	}
	if !r.units.active["darkpanel-job-backup.timer"] {
		t.Error("undoing the delete left the timer stopped")
	}
}
