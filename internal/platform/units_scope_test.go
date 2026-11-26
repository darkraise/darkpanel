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
	"sync"
	"syscall"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)

// scpUnits is a core.Units that records "<Method> <args>" for every call
// and answers success.
type scpUnits struct {
	mu    sync.Mutex
	calls []string
}

func (u *scpUnits) record(method string, args ...string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.calls = append(u.calls, strings.TrimSpace(method+" "+strings.Join(args, " ")))
}

func (u *scpUnits) recorded() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return slices.Clone(u.calls)
}

func (u *scpUnits) StartTransientService(_ context.Context, name string, _ core.TransientService) error {
	u.record("StartTransientService", name)
	return nil
}

func (u *scpUnits) StartTransientTimer(_ context.Context, name string, _ core.TransientTimer) error {
	u.record("StartTransientTimer", name)
	return nil
}

func (u *scpUnits) Start(_ context.Context, unit string) error { u.record("Start", unit); return nil }
func (u *scpUnits) Stop(_ context.Context, unit string) error  { u.record("Stop", unit); return nil }
func (u *scpUnits) Restart(_ context.Context, unit string) error {
	u.record("Restart", unit)
	return nil
}

func (u *scpUnits) Kill(_ context.Context, unit string, sig syscall.Signal) error {
	u.record("Kill", unit, fmt.Sprint(int(sig)))
	return nil
}

func (u *scpUnits) ResetFailed(_ context.Context, unit string) error {
	u.record("ResetFailed", unit)
	return nil
}

func (u *scpUnits) Enable(_ context.Context, unitFiles ...string) error {
	u.record("Enable", unitFiles...)
	return nil
}

func (u *scpUnits) Disable(_ context.Context, unitFiles ...string) error {
	u.record("Disable", unitFiles...)
	return nil
}

func (u *scpUnits) Reload(context.Context) error { u.record("Reload"); return nil }

func (u *scpUnits) ReloadUnit(_ context.Context, unit string) error {
	u.record("ReloadUnit", unit)
	return nil
}

func (u *scpUnits) State(_ context.Context, unit string) (core.UnitState, error) {
	u.record("State", unit)
	return core.UnitState{LoadState: "loaded", ActiveState: "active"}, nil
}

func (u *scpUnits) List(_ context.Context, pattern string) ([]core.UnitStatus, error) {
	u.record("List", pattern)
	return nil, nil
}

const scpUnitPath = "/etc/systemd/system/darkpanel-derp.service"

// scpDerp is module derp with its own unit and, optionally, external units.
func scpDerp(ownsUnit bool, external ...core.ExternalUnit) core.Manifest {
	m := core.Manifest{ID: "derp", Version: 1, ExternalUnits: external}
	if ownsUnit {
		m.OwnedPaths = []core.OwnedPath{{Path: scpUnitPath, Kind: core.OwnedUnit}}
	}
	return m
}

// scpCall is one call of a scoped client and the call its fake must see,
// "" when the call is refused.
type scpCall struct {
	name string
	run  func(ctx context.Context, u core.Units) error
	want string
}

func scpRun(t *testing.T, m core.Manifest, calls []scpCall, refusal func(c scpCall) string) {
	t.Helper()
	for _, c := range calls {
		t.Run(c.name, func(t *testing.T) {
			fake := &scpUnits{}
			err := c.run(context.Background(), ScopeUnits(fake, m))
			if c.want != "" {
				if got := fake.recorded(); err != nil || !slices.Equal(got, []string{c.want}) {
					t.Errorf("err %v, calls %q; want the call %q", err, got, c.want)
				}
				return
			}
			if want := refusal(c); err == nil || err.Error() != want {
				t.Errorf("err %v, want %q", err, want)
			}
			if got := fake.recorded(); len(got) != 0 {
				t.Errorf("a refused call reached the client: %q", got)
			}
		})
	}
}

func scpState(unit string) func(context.Context, core.Units) error {
	return func(ctx context.Context, u core.Units) error {
		_, err := u.State(ctx, unit)
		return err
	}
}

func TestScopeOwnedUnits(t *testing.T) {
	const unit = "darkpanel-derp.service"
	scpRun(t, scpDerp(true), []scpCall{
		{"start", func(ctx context.Context, u core.Units) error { return u.Start(ctx, unit) }, "Start " + unit},
		{"stop", func(ctx context.Context, u core.Units) error { return u.Stop(ctx, unit) }, "Stop " + unit},
		{"restart", func(ctx context.Context, u core.Units) error { return u.Restart(ctx, unit) }, "Restart " + unit},
		{"state", scpState(unit), "State " + unit},
		{"kill", func(ctx context.Context, u core.Units) error { return u.Kill(ctx, unit, syscall.SIGTERM) }, "Kill " + unit + " 15"},
		{"reset failed", func(ctx context.Context, u core.Units) error { return u.ResetFailed(ctx, unit) }, "ResetFailed " + unit},
		{"enable by name", func(ctx context.Context, u core.Units) error { return u.Enable(ctx, unit) }, "Enable " + unit},
		{"enable by path", func(ctx context.Context, u core.Units) error { return u.Enable(ctx, scpUnitPath) }, "Enable " + scpUnitPath},
		{"disable by name", func(ctx context.Context, u core.Units) error { return u.Disable(ctx, unit) }, "Disable " + unit},
		{"disable by path", func(ctx context.Context, u core.Units) error { return u.Disable(ctx, scpUnitPath) }, "Disable " + scpUnitPath},
		{"reload", func(ctx context.Context, u core.Units) error { return u.Reload(ctx) }, "Reload"},
	}, nil)
}

func TestScopeTaskUnits(t *testing.T) {
	const unit = "darkpanel-task-01ABC-apply-1.service"
	scpRun(t, scpDerp(false), []scpCall{
		{"state", scpState(unit), "State " + unit},
		{"stop", func(ctx context.Context, u core.Units) error { return u.Stop(ctx, unit) }, ""},
	}, func(scpCall) string { return "platform: module derp may not Stop unit " + unit })
}

func TestScopeExternalUnits(t *testing.T) {
	const unit = "tailscaled.service"
	m := scpDerp(false, core.ExternalUnit{Name: unit, Ops: []string{core.UnitOpState, core.UnitOpRestart}})
	methods := map[string]string{"stop": "Stop", "start": "Start", "kill": "Kill"}
	scpRun(t, m, []scpCall{
		{"state", scpState(unit), "State " + unit},
		{"restart", func(ctx context.Context, u core.Units) error { return u.Restart(ctx, unit) }, "Restart " + unit},
		{"stop", func(ctx context.Context, u core.Units) error { return u.Stop(ctx, unit) }, ""},
		{"start", func(ctx context.Context, u core.Units) error { return u.Start(ctx, unit) }, ""},
		{"kill", func(ctx context.Context, u core.Units) error { return u.Kill(ctx, unit, syscall.SIGTERM) }, ""},
	}, func(c scpCall) string { return "platform: module derp may not " + methods[c.name] + " unit " + unit })
}

// TestScopeReloadUnit admits ReloadUnit on an external unit that declares
// reload and refuses it on one that does not, and on every other unit.
func TestScopeReloadUnit(t *testing.T) {
	const unit, other = "ssh.service", "tailscaled.service"
	m := scpDerp(false, core.ExternalUnit{Name: unit, Ops: []string{core.UnitOpState, core.UnitOpReload}},
		core.ExternalUnit{Name: other, Ops: []string{core.UnitOpState, core.UnitOpRestart}})
	reload := func(name string) func(context.Context, core.Units) error {
		return func(ctx context.Context, u core.Units) error { return u.(core.UnitReloader).ReloadUnit(ctx, name) }
	}
	scpRun(t, m, []scpCall{
		{"declared", reload(unit), "ReloadUnit " + unit},
		{"undeclared op", reload(other), ""},
		{"unknown unit", reload("nginx.service"), ""},
	}, func(c scpCall) string {
		return map[string]string{
			"undeclared op": "platform: module derp may not ReloadUnit unit " + other,
			"unknown unit":  "platform: module derp may not ReloadUnit unit nginx.service",
		}[c.name]
	})
}

func TestScopeRefusals(t *testing.T) {
	const other = "nginx.service"
	want := map[string]string{
		"list":            "platform: module derp may not List unit darkpanel-*",
		"transient":       "platform: module derp may not StartTransientService unit darkpanel-derp-x.service",
		"timer":           "platform: module derp may not StartTransientTimer unit darkpanel-derp-x.timer",
		"start":           "platform: module derp may not Start unit " + other,
		"stop":            "platform: module derp may not Stop unit " + other,
		"restart":         "platform: module derp may not Restart unit " + other,
		"state":           "platform: module derp may not State unit " + other,
		"kill":            "platform: module derp may not Kill unit " + other,
		"reset failed":    "platform: module derp may not ResetFailed unit " + other,
		"enable":          "platform: module derp may not Enable unit " + other,
		"disable a path":  "platform: module derp may not Disable unit /etc/systemd/system/" + other,
		"enable two":      "platform: module derp may not Enable unit " + other,
		"reload unowned":  "platform: module derp may not Reload units: it declares no unit",
		"a lookalike":     "platform: module derp may not State unit darkpanel-task-01ABC-apply-1.timer",
		"a renamed owned": "platform: module derp may not Start unit darkpanel-derp.service.d",
	}
	refusal := func(c scpCall) string { return want[c.name] }
	scpRun(t, scpDerp(true), []scpCall{
		{"list", func(ctx context.Context, u core.Units) error {
			_, err := u.List(ctx, "darkpanel-*")
			return err
		}, ""},
		{"transient", func(ctx context.Context, u core.Units) error {
			return u.StartTransientService(ctx, "darkpanel-derp-x.service", core.TransientService{ExecStart: []string{"/bin/true"}})
		}, ""},
		{"timer", func(ctx context.Context, u core.Units) error {
			return u.StartTransientTimer(ctx, "darkpanel-derp-x.timer", core.TransientTimer{})
		}, ""},
		{"start", func(ctx context.Context, u core.Units) error { return u.Start(ctx, other) }, ""},
		{"stop", func(ctx context.Context, u core.Units) error { return u.Stop(ctx, other) }, ""},
		{"restart", func(ctx context.Context, u core.Units) error { return u.Restart(ctx, other) }, ""},
		{"state", scpState(other), ""},
		{"kill", func(ctx context.Context, u core.Units) error { return u.Kill(ctx, other, syscall.SIGKILL) }, ""},
		{"reset failed", func(ctx context.Context, u core.Units) error { return u.ResetFailed(ctx, other) }, ""},
		{"enable", func(ctx context.Context, u core.Units) error { return u.Enable(ctx, other) }, ""},
		{"disable a path", func(ctx context.Context, u core.Units) error {
			return u.Disable(ctx, "/etc/systemd/system/"+other)
		}, ""},
		{"enable two", func(ctx context.Context, u core.Units) error {
			return u.Enable(ctx, "darkpanel-derp.service", other)
		}, ""},
		{"a lookalike", scpState("darkpanel-task-01ABC-apply-1.timer"), ""},
		{"a renamed owned", func(ctx context.Context, u core.Units) error { return u.Start(ctx, "darkpanel-derp.service.d") }, ""},
	}, refusal)
	scpRun(t, scpDerp(false), []scpCall{
		{"reload unowned", func(ctx context.Context, u core.Units) error { return u.Reload(ctx) }, ""},
	}, refusal)
}

// scpModule registers a manifest as it is.
type scpModule core.Manifest

func (m scpModule) Manifest() core.Manifest { return core.Manifest(m) }

func TestModuleUnits(t *testing.T) {
	reg, err := core.NewRegistry(scpModule(scpDerp(true)))
	if err != nil {
		t.Fatal(err)
	}
	fake := &scpUnits{}
	units := ModuleUnits(fake, reg)
	if got := units(core.ModuleIDCore); got != core.Units(fake) {
		t.Errorf("the core module got %T, want the client itself", got)
	}
	derp := units("derp")
	if _, ok := derp.(*scopedUnits); !ok {
		t.Fatalf("module derp got %T, want a scoped client", derp)
	}
	if err := derp.Start(context.Background(), "nginx.service"); err == nil {
		t.Error("module derp started nginx.service")
	}
	if got := units("nope"); got != nil {
		t.Errorf("an unknown module got %T, want nil", got)
	}
	if got := ModuleUnits(nil, reg)(core.ModuleIDCore); got != nil {
		t.Errorf("without a client the core module got %T, want nil", got)
	}
}

// jobUnitPattern is the jobs module's unit set (M11 spec section 6).
const jobUnitPattern = `^darkpanel-job-[a-z0-9][a-z0-9-]{0,39}\.(service|timer)$`

// usJobs is module jobs, whose unit set lives in unitDir and whose scripts
// directory is scripts.
func usJobs(unitDir, scripts string) core.Manifest {
	return core.Manifest{ID: "jobs", Version: 1, OwnedPaths: []core.OwnedPath{
		{Path: unitDir, Kind: core.OwnedUnitSet, Pattern: jobUnitPattern},
		{Path: scripts, Kind: core.OwnedDir, Pattern: `^[a-z0-9][a-z0-9-]{0,39}\.sh$`},
	}}
}

// usFiles returns a writer for usJobs with its unit set and scripts
// directory in a new temporary directory, and that directory.
func usFiles(t *testing.T) (*Files, *fakeOwnership, string) {
	t.Helper()
	root := t.TempDir()
	m := usJobs(filepath.Join(root, "units"), filepath.Join(root, "jobs"))
	if err := os.Mkdir(filepath.Join(root, "units"), 0o750); err != nil {
		t.Fatal(err)
	}
	f, own := fpFiles(t, FilesConfig{Owned: OwnedPaths([]core.Manifest{m})})
	return f, own, root
}

// TestUnitSetAdmits is the unit-set kind of M11 spec section 6: a module
// writes, starts and stops the units its pattern names, and nothing else.
func TestUnitSetAdmits(t *testing.T) {
	ctx := context.Background()
	f, _, root := usFiles(t)
	units := filepath.Join(root, "units")
	for _, name := range []string{"darkpanel-job-backup.service", "darkpanel-job-backup.timer"} {
		if err := f.WriteUnitInSet(ctx, "jobs", name, []byte("[Unit]\n")); err != nil {
			t.Errorf("WriteUnitInSet %s: %v", name, err)
		}
		if got := readFile(t, filepath.Join(units, name)); got != "[Unit]\n" {
			t.Errorf("%s holds %q", name, got)
		}
	}
	for _, name := range []string{"darkpanel-job-Backup.service", "darkpanel-job-x.socket", "darkpanel-agent.service",
		"../darkpanel-job-backup.service", "sub/darkpanel-job-backup.service", "darkpanel-job-backup.service.d"} {
		if err := f.WriteUnitInSet(ctx, "jobs", name, []byte("[Unit]\n")); err == nil {
			t.Errorf("WriteUnitInSet %s succeeded", name)
		}
		if err := f.RemoveUnitInSet(ctx, "jobs", name); err == nil {
			t.Errorf("RemoveUnitInSet %s succeeded", name)
		}
	}
	if err := f.WriteUnitInSet(ctx, "other", "darkpanel-job-backup.service", []byte("[Unit]\n")); err == nil {
		t.Error("a module without the unit set wrote into it")
	}
	if names := dirNames(t, units); !slices.Equal(names, []string{"darkpanel-job-backup.service", "darkpanel-job-backup.timer"}) {
		t.Errorf("the unit directory holds %v", names)
	}

	const svc, timer = "darkpanel-job-backup.service", "darkpanel-job-backup.timer"
	m := usJobs("/etc/systemd/system", "/var/lib/darkpanel/jobs")
	scpRun(t, m, []scpCall{
		{"start the service", func(ctx context.Context, u core.Units) error { return u.Start(ctx, svc) }, "Start " + svc},
		{"stop the service", func(ctx context.Context, u core.Units) error { return u.Stop(ctx, svc) }, "Stop " + svc},
		{"start the timer", func(ctx context.Context, u core.Units) error { return u.Start(ctx, timer) }, "Start " + timer},
		{"stop the timer", func(ctx context.Context, u core.Units) error { return u.Stop(ctx, timer) }, "Stop " + timer},
		{"state", scpState(timer), "State " + timer},
		{"enable by path", func(ctx context.Context, u core.Units) error {
			return u.Enable(ctx, "/etc/systemd/system/"+timer)
		}, "Enable /etc/systemd/system/" + timer},
		{"disable by name", func(ctx context.Context, u core.Units) error { return u.Disable(ctx, timer) }, "Disable " + timer},
		{"reload", func(ctx context.Context, u core.Units) error { return u.Reload(ctx) }, "Reload"},
		{"upper case", func(ctx context.Context, u core.Units) error { return u.Start(ctx, "darkpanel-job-Backup.service") }, ""},
		{"a socket", func(ctx context.Context, u core.Units) error { return u.Start(ctx, "darkpanel-job-x.socket") }, ""},
		{"the agent", func(ctx context.Context, u core.Units) error { return u.Stop(ctx, "darkpanel-agent.service") }, ""},
		{"another directory", func(ctx context.Context, u core.Units) error {
			return u.Enable(ctx, "/run/systemd/system/"+timer)
		}, ""},
		{"a longer name", func(ctx context.Context, u core.Units) error { return u.Stop(ctx, "x-"+svc) }, ""},
	}, func(c scpCall) string {
		return map[string]string{
			"upper case":        "platform: module jobs may not Start unit darkpanel-job-Backup.service",
			"a socket":          "platform: module jobs may not Start unit darkpanel-job-x.socket",
			"the agent":         "platform: module jobs may not Stop unit darkpanel-agent.service",
			"another directory": "platform: module jobs may not Enable unit /run/systemd/system/" + timer,
			"a longer name":     "platform: module jobs may not Stop unit x-" + svc,
		}[c.name]
	})
}

// TestUnitSetOwnership records each unit of a set as it is written and
// forgets it as it is removed, and never takes over or removes a unit file
// Darkpanel did not write.
func TestUnitSetOwnership(t *testing.T) {
	ctx := context.Background()
	f, own, root := usFiles(t)
	const name = "darkpanel-job-backup.timer"
	path := filepath.Join(root, "units", name)
	own.onRecord = func(e core.OwnedEntry) {
		if _, err := os.Lstat(e.Path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s exists when it is recorded (%v); it must be recorded first", e.Path, err)
		}
	}
	if err := f.WriteUnitInSet(ctx, "jobs", name, []byte("[Timer]\n")); err != nil {
		t.Fatal(err)
	}
	own.onRecord = nil
	want := []core.OwnedEntry{{Path: path, Module: "jobs", Kind: core.OwnedUnit, CreatedAt: filesNow}}
	if got, _ := own.List(ctx); !reflect.DeepEqual(got, want) {
		t.Errorf("ownership = %+v, want %+v", got, want)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o644 {
		t.Errorf("unit file %v, %v; want mode 0644", info, err)
	}
	if err := f.WriteUnitInSet(ctx, "jobs", name, []byte("[Timer]\nOnCalendar=daily\n")); err != nil {
		t.Errorf("rewriting a recorded unit: %v", err)
	}
	if err := f.RemoveUnitInSet(ctx, "jobs", name); err != nil {
		t.Fatal(err)
	}
	if exists := fileExists(t, path); exists {
		t.Error("the removed unit file is still there")
	}
	if got, _ := own.List(ctx); len(got) != 0 {
		t.Errorf("ownership = %+v after the removal, want nothing", got)
	}
	if err := f.RemoveUnitInSet(ctx, "jobs", name); err != nil {
		t.Errorf("removing a missing unit: %v", err)
	}

	foreign := filepath.Join(root, "units", "darkpanel-job-theirs.service")
	if err := os.WriteFile(foreign, []byte("[Service]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := f.WriteUnitInSet(ctx, "jobs", "darkpanel-job-theirs.service", []byte("x")); err == nil ||
		!strings.Contains(err.Error(), "exists and is not Darkpanel's") {
		t.Errorf("writing over a unit Darkpanel did not write: %v", err)
	}
	if err := f.RemoveUnitInSet(ctx, "jobs", "darkpanel-job-theirs.service"); err == nil ||
		!strings.Contains(err.Error(), "is not Darkpanel's") {
		t.Errorf("removing a unit Darkpanel did not write: %v", err)
	}
	if got := readFile(t, foreign); got != "[Service]\n" {
		t.Errorf("the foreign unit holds %q", got)
	}
	if got, _ := own.List(ctx); len(got) != 0 {
		t.Errorf("ownership = %+v, want the foreign unit unrecorded", got)
	}
}

// TestDirPattern is a dir declaration with a file-name Pattern: its files
// are written root-owned in a 0755 directory and recorded one by one.
func TestDirPattern(t *testing.T) {
	ctx := context.Background()
	f, own, root := usFiles(t)
	dir := filepath.Join(root, "jobs")
	gid := os.Getegid()
	if os.Geteuid() == 0 {
		gid = 4242 // a group other than the agent's, which only root may give
	}
	if err := f.WriteDirFile(ctx, "jobs", dir, "backup.sh", []byte("tar c /srv\n"), 0o750, gid); err != nil {
		t.Fatalf("WriteDirFile: %v", err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() || info.Mode().Perm() != 0o755 || int(info.Sys().(*syscall.Stat_t).Uid) != os.Geteuid() {
		t.Errorf("directory %v, want a 0755 directory owned by %d", info.Mode(), os.Geteuid())
	}
	script := filepath.Join(dir, "backup.sh")
	sinfo, err := os.Lstat(script)
	if err != nil {
		t.Fatal(err)
	}
	if st := sinfo.Sys().(*syscall.Stat_t); sinfo.Mode().Perm() != 0o750 || int(st.Gid) != gid || int(st.Uid) != os.Geteuid() {
		t.Errorf("script mode %o owner %d:%d, want 750 and %d:%d", sinfo.Mode().Perm(), st.Uid, st.Gid, os.Geteuid(), gid)
	}
	if got := readFile(t, script); got != "tar c /srv\n" {
		t.Errorf("script holds %q", got)
	}
	want := []core.OwnedEntry{
		{Path: dir, Module: "jobs", Kind: core.OwnedDir, CreatedAt: filesNow},
		{Path: script, Module: "jobs", Kind: core.OwnedFile, CreatedAt: filesNow},
	}
	if got, _ := own.List(ctx); !reflect.DeepEqual(got, want) {
		t.Errorf("ownership = %+v, want %+v", got, want)
	}

	other := filepath.Join(root, "elsewhere")
	for _, c := range []struct{ dir, name string }{{dir, "../x.sh"}, {dir, "x.txt"}, {dir, "sub/x.sh"}, {dir, "X.sh"}, {other, "x.sh"}} {
		if err := f.WriteDirFile(ctx, "jobs", c.dir, c.name, []byte("x"), 0o750, gid); err == nil {
			t.Errorf("WriteDirFile %s %s succeeded", c.dir, c.name)
		}
		if err := f.RemoveDirFile(ctx, "jobs", c.dir, c.name); err == nil {
			t.Errorf("RemoveDirFile %s %s succeeded", c.dir, c.name)
		}
	}
	if fileExists(t, filepath.Join(root, "x.sh")) || fileExists(t, other) {
		t.Error("a refused write left a file")
	}

	if err := f.RemoveDirFile(ctx, "jobs", dir, "backup.sh"); err != nil {
		t.Fatalf("RemoveDirFile: %v", err)
	}
	if fileExists(t, script) {
		t.Error("the removed script is still there")
	}
	if got, _ := own.List(ctx); !reflect.DeepEqual(got, want[:1]) {
		t.Errorf("ownership = %+v after the removal, want only the directory", got)
	}
	if err := f.RemoveDirFile(ctx, "jobs", dir, "backup.sh"); err != nil {
		t.Errorf("removing a missing script: %v", err)
	}

	// A directory that exists but is not recorded is not Darkpanel's.
	f2, _, root2 := usFiles(t)
	if err := os.Mkdir(filepath.Join(root2, "jobs"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := f2.WriteDirFile(ctx, "jobs", filepath.Join(root2, "jobs"), "backup.sh", []byte("x"), 0o750, gid); err == nil ||
		!strings.Contains(err.Error(), "is not Darkpanel's") {
		t.Errorf("writing into an unrecorded directory: %v", err)
	}
	// A directory others may write is refused.
	if err := os.Chmod(dir, 0o775); err != nil { //nolint:gosec // G302: group-writable on purpose, to be refused
		t.Fatal(err)
	}
	if err := f.WriteDirFile(ctx, "jobs", dir, "backup.sh", []byte("x"), 0o750, gid); err == nil ||
		!strings.Contains(err.Error(), "has mode 775") {
		t.Errorf("writing into a group-writable directory: %v", err)
	}
	// Whatever the Pattern admits, a name is one entry of the directory, and
	// a Pattern matches the whole name even when it is not anchored.
	anyDir := filepath.Join(root, "any")
	f4, _ := fpFiles(t, FilesConfig{Owned: map[string][]core.OwnedPath{"jobs": {
		{Path: anyDir, Kind: core.OwnedDir, Pattern: `^.*$`},
		{Path: filepath.Join(root, "loose"), Kind: core.OwnedDir, Pattern: `[a-z]+\.sh`},
	}}})
	if err := f4.WriteDirFile(ctx, "jobs", anyDir, "ok.sh", []byte("x"), 0o750, gid); err != nil {
		t.Fatal(err)
	}
	// A directory named ... would hold the temporary file of ../x.sh.
	if err := os.Mkdir(filepath.Join(anyDir, "..."), 0o750); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"../x.sh", "sub/x.sh", ".", "..", ""} {
		if err := f4.WriteDirFile(ctx, "jobs", anyDir, name, []byte("x"), 0o750, gid); err == nil {
			t.Errorf("WriteDirFile %q under a pattern that admits it succeeded", name)
		}
	}
	if fileExists(t, filepath.Join(root, "x.sh")) {
		t.Error("a name with .. wrote outside the directory")
	}
	if err := f4.WriteDirFile(ctx, "jobs", filepath.Join(root, "loose"), "x.sh.txt", []byte("x"), 0o750, gid); err == nil {
		t.Error("an unanchored Pattern admitted a name it matches only part of")
	}
	// A dir without a Pattern names no files.
	f3, _ := fpFiles(t, FilesConfig{Owned: map[string][]core.OwnedPath{"jobs": {{Path: dir, Kind: core.OwnedDir}}}})
	if err := f3.WriteDirFile(ctx, "jobs", dir, "backup.sh", []byte("x"), 0o750, gid); err == nil {
		t.Error("WriteDirFile into a dir without a Pattern succeeded")
	}
}

func fileExists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Lstat(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	return err == nil
}

// timerUnits is scpUnits with canned timers and last runs.
type timerUnits struct{ scpUnits }

func (u *timerUnits) Timers(context.Context) ([]core.TimerInfo, error) {
	u.record("Timers")
	return []core.TimerInfo{{Name: "apt-daily.timer"}}, nil
}

func (u *timerUnits) LastRun(_ context.Context, unit string) (core.ServiceRun, error) {
	u.record("LastRun", unit)
	return core.ServiceRun{Result: "success"}, nil
}

// TestScopeTimers lets a module with a unit set read every timer and the
// last runs of its own services, and nothing else.
func TestScopeTimers(t *testing.T) {
	ctx := context.Background()
	fake := &timerUnits{}
	jobs := ScopeUnits(fake, usJobs("/etc/systemd/system", "/var/lib/darkpanel/jobs")).(core.TimerReader)
	if got, err := jobs.Timers(ctx); err != nil || len(got) != 1 {
		t.Errorf("Timers: %v, %v", got, err)
	}
	if run, err := jobs.LastRun(ctx, "darkpanel-job-backup.service"); err != nil || run.Result != "success" {
		t.Errorf("LastRun of a job: %+v, %v", run, err)
	}
	if _, err := jobs.LastRun(ctx, "ssh.service"); err == nil || err.Error() != "platform: module jobs may not LastRun unit ssh.service" {
		t.Errorf("LastRun of ssh.service: %v", err)
	}
	derp := ScopeUnits(fake, scpDerp(true)).(core.TimerReader)
	if _, err := derp.Timers(ctx); err == nil {
		t.Error("a module without a unit set listed timers")
	}
	if got := fake.recorded(); !slices.Equal(got, []string{"Timers", "LastRun darkpanel-job-backup.service"}) {
		t.Errorf("calls %q", got)
	}
}
