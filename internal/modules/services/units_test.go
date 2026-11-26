package services

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// fakeUnits is a core.UnitAdmin over a fixed set of units. Methods the
// module must not call are left to the nil embedded core.Units.
type fakeUnits struct {
	core.Units
	units map[string]*core.UnitInfo
	calls []string
}

func newFakeUnits(units ...core.UnitInfo) *fakeUnits {
	f := &fakeUnits{units: map[string]*core.UnitInfo{}}
	for _, u := range units {
		f.units[u.ID] = &u
	}
	return f
}

// find looks a unit up by its ID or any of its names, as LoadUnit does.
func (f *fakeUnits) find(name string) *core.UnitInfo {
	for _, u := range f.units {
		if u.ID == name || slices.Contains(u.Names, name) {
			return u
		}
	}
	return nil
}

func (f *fakeUnits) Inspect(_ context.Context, name string) (core.UnitInfo, error) {
	if u := f.find(name); u != nil {
		return *u, nil
	}
	return core.UnitInfo{ID: name, LoadState: "not-found", ActiveState: "inactive"}, nil
}

func (f *fakeUnits) ListAll(context.Context) ([]core.UnitInfo, error) {
	out := []core.UnitInfo{}
	for _, u := range f.units {
		out = append(out, *u)
	}
	slices.SortFunc(out, func(a, b core.UnitInfo) int { return strings.Compare(a.ID, b.ID) })
	return out, nil
}

func (f *fakeUnits) State(_ context.Context, name string) (core.UnitState, error) {
	u := f.find(name)
	if u == nil {
		return core.UnitState{LoadState: "not-found", ActiveState: "inactive"}, nil
	}
	return core.UnitState{LoadState: u.LoadState, ActiveState: u.ActiveState, SubState: u.SubState}, nil
}

func (f *fakeUnits) set(call, name, active, file string) error {
	f.calls = append(f.calls, call+" "+name)
	if u := f.find(name); u != nil {
		if active != "" {
			u.ActiveState = active
		}
		if file != "" {
			u.UnitFileState = file
		}
	}
	return nil
}

func (f *fakeUnits) Start(_ context.Context, u string) error { return f.set("start", u, "active", "") }
func (f *fakeUnits) Stop(_ context.Context, u string) error  { return f.set("stop", u, "inactive", "") }
func (f *fakeUnits) Restart(_ context.Context, u string) error {
	return f.set("restart", u, "active", "")
}
func (f *fakeUnits) ReloadUnit(_ context.Context, u string) error {
	return f.set("reload", u, "", "")
}

func (f *fakeUnits) Enable(_ context.Context, files ...string) error {
	return f.set("enable", strings.Join(files, " "), "", "enabled")
}

func (f *fakeUnits) Disable(_ context.Context, files ...string) error {
	return f.set("disable", strings.Join(files, " "), "", "disabled")
}

func (f *fakeUnits) Reload(context.Context) error {
	f.calls = append(f.calls, "daemon-reload")
	return nil
}

// unit is a loaded, active, enabled unit with no action properties; edit
// changes it.
func unit(name string, edit ...func(*core.UnitInfo)) core.UnitInfo {
	u := core.UnitInfo{ID: name, Names: []string{name}, Description: "test unit " + name, LoadState: "loaded",
		ActiveState: "active", SubState: "running", UnitFileState: "enabled", SuccessAction: "none",
		FailureAction: "none", JobTimeoutAction: "none", StartLimitAction: "none"}
	for _, e := range edit {
		e(&u)
	}
	return u
}

const testHost = "h1"

var (
	adminP    = core.Principal{Actor: "user:admin", UserID: "admin", Scope: core.ScopeAdmin, Source: "192.0.2.1"}
	operatorP = core.Principal{Actor: "user:op", UserID: "op", Source: "192.0.2.2", Access: core.Access{All: core.ScopeOperate}}
)

// rig runs the module's handlers against fake units and a migrated agent
// database.
type rig struct {
	t         *testing.T
	units     *fakeUnits
	db        *store.DB
	onTailnet bool
}

func newRig(t *testing.T, units ...core.UnitInfo) *rig {
	t.Helper()
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
	if _, err := store.Migrate(ctx, db, store.MigrateOptions{Side: store.SideAgent, BinaryVersion: "test",
		BackupRoot: filepath.Join(t.TempDir(), "backups"), Keep: 5, Now: time.Now()}, set, nil); err != nil {
		t.Fatal(err)
	}
	return &rig{t: t, units: newFakeUnits(units...), db: db}
}

func (r *rig) module() module {
	return module{agentOnTailnet: func(context.Context) (bool, error) { return r.onTailnet, nil }}
}

func (r *rig) env(p core.Principal) core.Env {
	return core.Env{HostID: testHost, Principal: p, DB: r.db, Units: r.units}
}

func (r *rig) plan(o op, p core.Principal, name string) (core.PlanOutcome, error) {
	return unitHandler{m: r.module(), op: o}.Plan(context.Background(), r.env(p), UnitInput{Unit: name})
}

// pending records a change of action in state, as the lifecycle would.
func (r *rig) pending(action, state string) {
	r.t.Helper()
	at := time.Now().UTC().Format(time.RFC3339Nano)
	id := "c-" + action + "-" + state
	if _, err := r.db.Writer().Exec(`INSERT INTO core_plans (id, host_id, action, version, actor, input_hash, state_hash,
		diff_json, guards_json, confirmation, created_at, expires_at) VALUES (?, ?, ?, 1, 'user:admin', '', '', '[]', '[]', '', ?, ?)`,
		"p-"+id, testHost, action, at, at); err != nil {
		r.t.Fatal(err)
	}
	if _, err := r.db.Writer().Exec(`INSERT INTO core_changes (id, plan_id, host_id, action, version, class, compensated_by,
		actor, state, input_json, created_at, updated_at) VALUES (?, ?, ?, ?, 1, 'compensable', '', 'user:admin', ?, '{}', ?, ?)`,
		id, "p-"+id, testHost, action, state, at, at); err != nil {
		r.t.Fatal(err)
	}
}

func wantCode(t *testing.T, what string, err error, code errcode.Code) {
	t.Helper()
	if got := errcode.From(err).Code; err == nil || got != code {
		t.Errorf("%s: err = %v (%s), want %s", what, err, got, code)
	}
}

func TestUnitList(t *testing.T) {
	r := newRig(t,
		unit("nginx.service", func(u *core.UnitInfo) { u.MainPID = 42 }),
		unit("ssh.socket", func(u *core.UnitInfo) { u.SubState = "listening" }),
		unit("apt-daily.timer", func(u *core.UnitInfo) { u.ActiveState, u.SubState = "failed", "failed" }),
		unit("home.mount", func(u *core.UnitInfo) { u.SubState, u.UnitFileState = "mounted", "generated" }),
		unit("cups.path", func(u *core.UnitInfo) { u.SubState = "waiting" }),
		unit("multi-user.target", func(u *core.UnitInfo) { u.SubState, u.UnitFileState = "active", "static" }),
		unit("swapfile.swap"),
	)
	list := func(in ListInput) []Unit {
		t.Helper()
		out, err := listHandler{}.Read(context.Background(), r.env(operatorP), in)
		if err != nil {
			t.Fatal(err)
		}
		return out.Units
	}
	all := list(ListInput{})
	var types []string
	for _, u := range all {
		types = append(types, u.Type)
	}
	slices.Sort(types)
	if want := []string{"mount", "path", "service", "socket", "target", "timer"}; !slices.Equal(types, want) {
		t.Errorf("types = %v, want %v", types, want)
	}
	nginx := Unit{Name: "nginx.service", Type: "service", Description: "test unit nginx.service", Load: "loaded",
		Active: "active", Sub: "running", Enabled: "enabled", MainPID: 42}
	if i := slices.IndexFunc(all, func(u Unit) bool { return u.Name == "nginx.service" }); i < 0 || all[i] != nginx {
		t.Errorf("nginx.service listed as %+v, want %+v", all, nginx)
	}
	if got := list(ListInput{Type: "service"}); len(got) != 1 || got[0].Name != "nginx.service" {
		t.Errorf("type=service lists %+v", got)
	}
	if got := list(ListInput{State: "failed"}); len(got) != 1 || got[0].Name != "apt-daily.timer" || got[0].Sub != "failed" {
		t.Errorf("state=failed lists %+v", got)
	}
	for _, in := range []ListInput{{Type: "swap"}, {State: "broken"}} {
		_, err := listHandler{}.Read(context.Background(), r.env(operatorP), in)
		wantCode(t, "list "+in.Type+in.State, err, errcode.InvalidInput)
	}
}

func TestUnitGet(t *testing.T) {
	mem, tasks := uint64(12<<20), uint64(3)
	started := time.Date(2026, 11, 26, 8, 0, 0, 0, time.UTC)
	text := strings.Repeat("x", 64<<10)
	r := newRig(t, unit("nginx.service", func(u *core.UnitInfo) {
		u.MainPID, u.MemoryCurrent, u.TasksCurrent, u.Restarts, u.StartedAt = 42, &mem, &tasks, 2, started
		u.FragmentPath, u.FileText, u.FileTruncated = "/lib/systemd/system/nginx.service", text, true
		u.DropInPaths = []string{"/etc/systemd/system/nginx.service.d/override.conf"}
	}))
	got, err := getHandler{}.Read(context.Background(), r.env(operatorP), GetInput{Unit: "nginx.service"})
	if err != nil {
		t.Fatal(err)
	}
	if got.MemoryCurrent == nil || *got.MemoryCurrent != mem || got.Tasks == nil || *got.Tasks != tasks || got.Restarts != 2 ||
		got.StartedAt == nil || !got.StartedAt.Equal(started) || got.UnitFile != "/lib/systemd/system/nginx.service" ||
		got.UnitFileText != text || !got.Truncated || !slices.Equal(got.DropIns, []string{"/etc/systemd/system/nginx.service.d/override.conf"}) ||
		got.Active != "active" || got.MainPID != 42 {
		t.Errorf("get = %+v", got)
	}
	for name, code := range map[string]errcode.Code{"nginx": errcode.InvalidInput, "missing.service": errcode.NotFound, "a b.service": errcode.InvalidInput} {
		_, err := getHandler{}.Read(context.Background(), r.env(operatorP), GetInput{Unit: name})
		wantCode(t, "get "+name, err, code)
	}
}

func TestUnitApplyAndVerify(t *testing.T) {
	ctx := context.Background()
	r := newRig(t, unit("nginx.service", func(u *core.UnitInfo) {
		u.Names, u.ActiveState, u.UnitFileState = []string{"nginx.service", "web.service"}, "inactive", "disabled"
	}))
	env := r.env(adminP)
	for _, o := range []op{opStart, opEnable, opDisable, opStop} {
		h := unitHandler{m: r.module(), op: o}
		in := UnitInput{Unit: "web.service"}
		out, err := h.Apply(ctx, env, in)
		if err != nil || out.Unit != "nginx.service" {
			t.Fatalf("%s web.service: %+v, %v", o, out, err)
		}
		if err := h.Verify(ctx, env, in, out); err != nil {
			t.Errorf("verify %s: %v", o, err)
		}
	}
	want := []string{"start nginx.service", "enable nginx.service", "daemon-reload", "disable nginx.service", "daemon-reload", "stop nginx.service"}
	if !slices.Equal(r.units.calls, want) {
		t.Errorf("calls %v, want %v", r.units.calls, want)
	}
	r.units.units["nginx.service"].ActiveState = "failed"
	err := unitHandler{m: r.module(), op: opRestart}.Verify(ctx, env, UnitInput{Unit: "nginx.service"}, UnitChanged{})
	if err == nil || !strings.Contains(err.Error(), "failed after the restart") {
		t.Errorf("verify of a failed restart: %v", err)
	}
}
