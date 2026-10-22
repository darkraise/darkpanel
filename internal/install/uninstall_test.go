package install

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/secret"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/platform"
)

// demoModule owns a file, a block of a shared file, a directory, a unit and
// a user, and has an uninstall step without options that records what it saw.
type demoModule struct {
	l     Layout
	calls []string
	opts  []map[string]string
}

func (m *demoModule) paths() (conf, hosts, data, unit string) {
	return filepath.Join(m.l.ConfigDir, "..", "demo.conf"), filepath.Join(m.l.ConfigDir, "..", "hosts"),
		filepath.Join(m.l.DataDir, "..", "demo-data"), m.l.UnitPath("demo.service")
}

func (m *demoModule) Manifest() core.Manifest {
	conf, hosts, data, unit := m.paths()
	return core.Manifest{ID: "demo", Version: 1, OwnedPaths: []core.OwnedPath{
		{Path: filepath.Clean(conf), Kind: core.OwnedFile},
		{Path: filepath.Clean(hosts), Kind: core.OwnedBlock, BlockIDs: []string{"names"}},
		{Path: filepath.Clean(data), Kind: core.OwnedDir},
		{Path: unit, Kind: core.OwnedUnit},
		{Path: "demo", Kind: core.OwnedUser},
	}}
}

func (m *demoModule) UninstallOptions() []core.UninstallOption { return nil }

func (m *demoModule) Uninstall(_ context.Context, env core.Env, keepData bool, opts map[string]string) error {
	m.opts = append(m.opts, opts)
	_, hosts, _, _ := m.paths()
	b, err := os.ReadFile(filepath.Clean(hosts))
	if err != nil {
		return err
	}
	m.calls = append(m.calls, "uninstall keepData="+map[bool]string{true: "true", false: "false"}[keepData]+
		" block present="+map[bool]string{true: "yes", false: "no"}[strings.Contains(string(b), "demo-name")]+
		" env="+map[bool]string{true: "complete", false: "incomplete"}[env.DB != nil && env.Files != nil && env.Sealer != nil])
	return nil
}

var _ core.Uninstaller = (*demoModule)(nil)

// installHost installs role on a fresh test host, puts the binary and its
// link where install.sh would, and lets the demo module write its paths.
func installHost(t *testing.T, role Role) (*testHost, *demoModule) {
	t.Helper()
	h := newTestHost(t)
	ctx := context.Background()
	cfg := h.config(role)
	if role == RoleAgent {
		cfg.Token = "dpe1.t"
	}
	if err := Install(ctx, cfg); err != nil {
		t.Fatalf("Install: %v", err)
	}
	p := h.layout.Paths
	if err := os.WriteFile(p.Binary(), []byte("binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.PrevBinary(), []byte("previous"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(p.Binary(), p.BinaryLink); err != nil {
		t.Fatal(err)
	}
	m := &demoModule{l: h.layout}
	conf, hosts, data, unit := m.paths()
	if err := os.WriteFile(filepath.Clean(hosts), []byte("127.0.0.1 localhost\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(ctx, p.AgentDB())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	own := platform.NewOwnership(db)
	files := platform.NewFiles(platform.FilesConfig{
		Owned: platform.OwnedPaths([]core.Manifest{m.Manifest()}), Ownership: own, Clock: h.clock, TestMode: true,
	})
	if err := files.WriteFile(ctx, "demo", filepath.Clean(conf), []byte("x=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := files.ReplaceBlock(ctx, "demo", filepath.Clean(hosts), "names", []byte("10.0.0.1 demo-name\n")); err != nil {
		t.Fatal(err)
	}
	for _, e := range []core.OwnedEntry{
		{Path: filepath.Clean(data), Module: "demo", Kind: core.OwnedDir, CreatedAt: installTime},
		{Path: unit, Module: "demo", Kind: core.OwnedUnit, CreatedAt: installTime},
		{Path: "demo", Module: "demo", Kind: core.OwnedUser, CreatedAt: installTime},
	} {
		if err := own.Record(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(filepath.Clean(data), "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unit, []byte("[Service]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.users.users["demo"] = [2]int{991, 991}
	h.systemd.calls = nil
	h.runner.cmds = nil
	return h, m
}

func (h *testHost) uninstallConfig(m *demoModule, keepData bool) UninstallConfig {
	return UninstallConfig{
		Layout: h.layout, KeepData: keepData, Modules: []core.Module{m}, Systemd: h.systemd, Exec: h.runner, Users: h.users,
		Clock: h.clock, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), FilesTestMode: true,
		PollInterval: time.Millisecond, StopTimeout: time.Second,
	}
}

func exists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Lstat(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	return err == nil
}

func runUninstall(t *testing.T, cfg UninstallConfig) (string, error) {
	t.Helper()
	u, err := PlanUninstall(context.Background(), cfg)
	if err != nil {
		t.Fatalf("PlanUninstall: %v", err)
	}
	var out bytes.Buffer
	err = u.Run(context.Background(), &out)
	return out.String(), err
}

func TestUninstallRemovesEverything(t *testing.T) {
	h, m := installHost(t, RoleController)
	out, err := runUninstall(t, h.uninstallConfig(m, false))
	if err != nil {
		t.Fatalf("Run: %v\n%s", err, out)
	}
	l, p := h.layout, h.layout.Paths
	conf, hosts, data, unit := m.paths()
	for _, path := range []string{
		l.DataDir, l.ConfigDir, p.BinaryDir, p.BinaryLink, filepath.Clean(conf), filepath.Clean(data), unit,
		filepath.Join(l.UnitDir, DropInDir), l.UnitPath("darkpanel-controller.service"), l.UnitPath("darkpanel-agent.service"),
		l.UnitPath("darkpanel-rollback.service"), l.UnitPath(RecoverUnit),
	} {
		if exists(t, path) {
			t.Errorf("%s is left", path)
		}
	}
	if got := string(readBytes(t, filepath.Clean(hosts))); got != "127.0.0.1 localhost\n" {
		t.Errorf("hosts holds %q, want only the line Darkpanel did not write", got)
	}
	if want := []string{"uninstall keepData=false block present=yes env=complete"}; !reflect.DeepEqual(m.calls, want) {
		t.Errorf("module steps %v, want %v", m.calls, want)
	}
	if got, want := h.runner.commands(), []string{"/usr/sbin/userdel darkpanel", "/usr/sbin/userdel demo"}; !reflect.DeepEqual(got, want) {
		t.Errorf("commands %v, want %v", got, want)
	}
	want := []string{
		"Stop darkpanel-controller.service", "Stop darkpanel-agent.service",
		"Disable darkpanel-controller.service darkpanel-agent.service darkpanel-update-recover.service",
		"Stop demo.service", "Disable demo.service", "Reload",
	}
	if got := h.systemd.recorded(); !reflect.DeepEqual(got, want) {
		t.Errorf("systemd calls\n got %v\nwant %v", got, want)
	}
	for _, step := range []string{"services:", "agent lock:", "modules:", "marked blocks:", "files:", "units:", "binary:", "data:"} {
		if !strings.Contains(out, "\n"+step) && !strings.HasPrefix(out, step) {
			t.Errorf("output lacks the step %s:\n%s", step, out)
		}
	}
}

func TestUninstallAgentRole(t *testing.T) {
	h, m := installHost(t, RoleAgent)
	h.systemd.missing["darkpanel-controller.service"] = true
	if out, err := runUninstall(t, h.uninstallConfig(m, false)); err != nil {
		t.Fatalf("Run: %v\n%s", err, out)
	}
	if exists(t, h.layout.DataDir) || exists(t, h.layout.Paths.BinaryDir) || exists(t, h.layout.UnitPath("darkpanel-agent.service")) {
		t.Error("the agent role's paths are left")
	}
	if got := h.systemd.recorded(); got[0] != "Stop darkpanel-agent.service" {
		t.Errorf("first call %q, want the agent stopped; the controller unit does not exist", got[0])
	}
	if got := h.runner.commands(); !reflect.DeepEqual(got, []string{"/usr/sbin/userdel demo"}) {
		t.Errorf("commands %v: the agent role has no darkpanel user to remove", got)
	}
}

func TestUninstallKeepDataThenReinstall(t *testing.T) {
	h, m := installHost(t, RoleController)
	p := h.layout.Paths
	key, err := secret.LoadOrCreateKey(p.AgentKey)
	if err != nil {
		t.Fatal(err)
	}
	sealer, err := secret.NewSealer(key)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := sealer.Seal("test", []byte("kept secret"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	db, err := store.Open(ctx, p.AgentDB())
	if err != nil {
		t.Fatal(err)
	}
	err = db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO core_meta (key, value) VALUES ('test_sealed', ?)`, hex.EncodeToString(sealed))
		return err
	})
	if cerr := db.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		t.Fatal(err)
	}
	controllerKey := readBytes(t, p.ControllerKey)

	if out, err := runUninstall(t, h.uninstallConfig(m, true)); err != nil {
		t.Fatalf("Run: %v\n%s", err, out)
	}
	for _, path := range []string{p.AgentDB(), p.AgentKey, p.ControllerKey, p.ControllerDir, p.UpdateDir, p.AgentTLSCurrent()} {
		if !exists(t, path) {
			t.Errorf("--keep-data removed %s", path)
		}
	}
	for _, path := range []string{p.BinaryDir, p.BinaryLink, h.layout.UnitPath("darkpanel-agent.service")} {
		if exists(t, path) {
			t.Errorf("--keep-data kept %s", path)
		}
	}
	if len(h.runner.commands()) != 0 {
		t.Errorf("--keep-data ran %v; every user is kept", h.runner.commands())
	}
	if want := []string{"uninstall keepData=true block present=yes env=complete"}; !reflect.DeepEqual(m.calls, want) {
		t.Errorf("module steps %v, want %v", m.calls, want)
	}

	if err := Install(ctx, h.config(RoleController)); err != nil {
		t.Fatalf("reinstall: %v", err)
	}
	if !bytes.Equal(readBytes(t, p.ControllerKey), controllerKey) {
		t.Error("the reinstall replaced the controller key")
	}
	key, err = secret.LoadOrCreateKey(p.AgentKey)
	if err != nil {
		t.Fatal(err)
	}
	if sealer, err = secret.NewSealer(key); err != nil {
		t.Fatal(err)
	}
	// The kept key opens what the kept database holds.
	if db, err = store.Open(ctx, p.AgentDB()); err != nil {
		t.Fatal(err)
	}
	var stored string
	err = db.Read().QueryRowContext(ctx, `SELECT value FROM core_meta WHERE key = 'test_sealed'`).Scan(&stored)
	if cerr := db.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		t.Fatalf("the kept agent.db lost its row: %v", err)
	}
	if sealed, err = hex.DecodeString(stored); err != nil {
		t.Fatal(err)
	}
	if plain, err := sealer.Open("test", sealed); err != nil || string(plain) != "kept secret" {
		t.Errorf("the kept agent key opens %q, %v", plain, err)
	}
	// --keep-data keeps every user, so the module's user stays recorded.
	want := append(wantManifest(h.layout, RoleController), "user demo (module demo)")
	if got := manifest(t, h.layout); !reflect.DeepEqual(got, want) {
		t.Errorf("manifest after the reinstall\n got %v\nwant %v", got, want)
	}
	if len(h.agent.tokens) != 1 {
		t.Errorf("the reinstall enrolled again (%v); the kept credentials were valid", h.agent.tokens)
	}
}

func TestUninstallKeepDataKeepsModuleData(t *testing.T) {
	h, m := installHost(t, RoleController)
	ctx := context.Background()
	db, err := store.Open(ctx, h.layout.Paths.AgentDB())
	if err != nil {
		t.Fatal(err)
	}
	err = platform.NewOwnership(db).Record(ctx, core.OwnedEntry{
		Path: filepath.Join(h.layout.DataDir, "demo"), Module: "demo", Kind: core.OwnedDir, CreatedAt: installTime,
	})
	if cerr := db.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		t.Fatal(err)
	}
	kept := filepath.Join(h.layout.DataDir, "demo", "sub")
	if err := os.MkdirAll(kept, 0o700); err != nil {
		t.Fatal(err)
	}
	if out, err := runUninstall(t, h.uninstallConfig(m, true)); err != nil {
		t.Fatalf("Run: %v\n%s", err, out)
	}
	if !exists(t, kept) {
		t.Errorf("--keep-data removed the module's %s", kept)
	}
	conf, _, _, _ := m.paths()
	if exists(t, filepath.Clean(conf)) {
		t.Errorf("--keep-data kept the module's %s, which is outside the data directory", filepath.Clean(conf))
	}
}

func TestUninstallRefusesARunningAgent(t *testing.T) {
	h, m := installHost(t, RoleController)
	lock := filepath.Join(h.layout.Paths.AgentDir, AgentLockName)
	fd, err := unix.Open(lock, unix.O_RDWR|unix.O_CREAT|unix.O_CLOEXEC, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unix.Close(fd) }()
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	_, err = runUninstall(t, h.uninstallConfig(m, false))
	if err == nil || !strings.Contains(err.Error(), "an agent still runs") ||
		!strings.Contains(err.Error(), "not run: modules, marked blocks, files, units, binary, data") {
		t.Fatalf("Run = %v, want a refusal naming the steps not run", err)
	}
	if !exists(t, h.layout.Paths.Binary()) || len(m.calls) != 0 {
		t.Error("a refused uninstall removed something")
	}
}

func TestUninstallStopsAtAFailedStepAndResumes(t *testing.T) {
	h, m := installHost(t, RoleController)
	h.systemd.failOn("Reload", errors.New("bus gone"))
	_, err := runUninstall(t, h.uninstallConfig(m, false))
	if err == nil || !strings.Contains(err.Error(), "units: bus gone; not run: binary, data") {
		t.Fatalf("Run = %v", err)
	}
	if !exists(t, h.layout.Paths.Binary()) || !exists(t, h.layout.Paths.AgentDB()) {
		t.Fatal("the steps after the failure ran")
	}
	if !exists(t, h.layout.Paths.AgentKey) {
		t.Fatal("the failed run removed the agent key, which the resumed module steps need")
	}
	h.systemd.failOn("Reload", nil)
	if out, err := runUninstall(t, h.uninstallConfig(m, false)); err != nil {
		t.Fatalf("second Run: %v\n%s", err, out)
	}
	if exists(t, h.layout.DataDir) || exists(t, h.layout.Paths.BinaryDir) {
		t.Error("the second run left data or the binary")
	}
	want := []string{"uninstall keepData=false block present=yes env=complete", "uninstall keepData=false block present=no env=complete"}
	if !reflect.DeepEqual(m.calls, want) {
		t.Errorf("module steps %v, want %v", m.calls, want)
	}
}

func TestUninstallPassesOptionsMap(t *testing.T) {
	h, m := installHost(t, RoleController)
	if out, err := runUninstall(t, h.uninstallConfig(m, false)); err != nil {
		t.Fatalf("Run: %v\n%s", err, out)
	}
	if len(m.opts) != 1 || m.opts[0] == nil || len(m.opts[0]) != 0 {
		t.Errorf("the module step got options %v, want one empty, non-nil map", m.opts)
	}
}

func TestUninstallResumeWithoutAgentKeySkipsModules(t *testing.T) {
	h, m := installHost(t, RoleController)
	h.systemd.failOn("Reload", errors.New("bus gone"))
	if _, err := runUninstall(t, h.uninstallConfig(m, false)); err == nil {
		t.Fatal("the first Run succeeded")
	}
	if err := os.Remove(h.layout.Paths.AgentKey); err != nil {
		t.Fatal(err)
	}
	h.systemd.failOn("Reload", nil)
	out, err := runUninstall(t, h.uninstallConfig(m, false))
	if err != nil {
		t.Fatalf("second Run: %v\n%s", err, out)
	}
	if want := []string{"uninstall keepData=false block present=yes env=complete"}; !reflect.DeepEqual(m.calls, want) {
		t.Errorf("module steps %v, want %v: without the agent key no module step runs", m.calls, want)
	}
	if !strings.Contains(out, "modules: no agent key") {
		t.Errorf("output %s does not say the module steps were skipped", out)
	}
}

func TestUninstallWaitsForServicesToStop(t *testing.T) {
	h, m := installHost(t, RoleController)
	h.systemd.stuck["darkpanel-agent.service"] = true
	cfg := h.uninstallConfig(m, false)
	cfg.StopTimeout = 20 * time.Millisecond
	_, err := runUninstall(t, cfg)
	if err == nil || !strings.Contains(err.Error(), "darkpanel-agent.service is still active") {
		t.Fatalf("Run = %v, want the stop wait to fail", err)
	}
	if !exists(t, h.layout.Paths.AgentDB()) {
		t.Error("the run went on after a service did not stop")
	}
}

func TestUninstallWithoutAgentDatabase(t *testing.T) {
	h := newTestHost(t)
	l := h.layout
	unit := l.UnitPath("darkpanel-agent.service")
	for _, path := range []string{l.Paths.Binary(), unit} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	out, err := runUninstall(t, h.uninstallConfig(&demoModule{l: l}, false))
	if err != nil {
		t.Fatalf("Run: %v\n%s", err, out)
	}
	if exists(t, l.Paths.BinaryDir) || exists(t, unit) {
		t.Error("the binary or the unit is left")
	}
	if !strings.Contains(out, "modules: no agent database") {
		t.Errorf("output %s does not say the module steps were skipped", out)
	}
}

func TestDescribeUninstall(t *testing.T) {
	h, m := installHost(t, RoleController)
	u, err := PlanUninstall(context.Background(), h.uninstallConfig(m, true))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = u.Close() }()
	var out bytes.Buffer
	u.Describe(&out)
	conf, hosts, _, _ := m.paths()
	for _, want := range []string{
		"1. services", "8. data", "keep " + h.layout.DataDir, "file " + filepath.Clean(conf) + ", of module demo",
		"block " + filepath.Clean(hosts) + " (block names), of module demo", "unit " + h.layout.DropInPath() + ", of module core",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("plan lacks %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "file "+h.layout.Paths.AgentKey) {
		t.Errorf("the --keep-data plan removes the agent key:\n%s", out.String())
	}
}

// kindsModule declares a managed directory outside the data directory and one
// inside it, a service directory with the subdirectory certs, a unit, a
// restore file and a kernel parameter.
type kindsModule struct{ l Layout }

func (m kindsModule) paths() (outside, inside, svc, unit, restore string) {
	etc := filepath.Dir(m.l.ConfigDir)
	return filepath.Join(etc, "kinds-debs"), filepath.Join(m.l.DataDir, "kinds-tokens"), filepath.Join(etc, "kinds-svc"),
		m.l.UnitPath("darkpanel-kinds.service"), filepath.Join(etc, "kinds.rules")
}

func (m kindsModule) Manifest() core.Manifest {
	outside, inside, svc, unit, restore := m.paths()
	return core.Manifest{ID: "kinds", Version: 1, OwnedPaths: []core.OwnedPath{
		{Path: outside, Kind: core.OwnedManagedDir},
		{Path: inside, Kind: core.OwnedManagedDir},
		{Path: svc, Kind: core.OwnedServiceDir, User: "kinds", Subdirs: []string{"certs"}},
		{Path: unit, Kind: core.OwnedUnit},
		{Path: restore, Kind: core.OwnedRestore},
		{Path: "net/ipv4/ip_forward", Kind: core.OwnedSysctl, Pattern: `^[01]$`},
	}}
}

// kindsInstall lets kindsModule write its paths on h, as its actions would:
// two files in each managed directory, the service directory with a
// certificate, the unit file and the restore file.
func kindsInstall(t *testing.T, h *testHost) kindsModule {
	t.Helper()
	ctx := context.Background()
	m := kindsModule{l: h.layout}
	outside, inside, svc, unit, restore := m.paths()
	db, err := store.Open(ctx, h.layout.Paths.AgentDB())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	uid, gid := os.Geteuid(), os.Getegid()
	files := platform.NewFiles(platform.FilesConfig{
		Owned: platform.OwnedPaths([]core.Manifest{m.Manifest()}), Ownership: platform.NewOwnership(db), Clock: h.clock,
		TestMode: true, LookupUser: func(string) (int, int, error) { return uid, gid, nil },
	})
	for _, dir := range []string{outside, inside} {
		for _, name := range []string{"a.token", "b.token"} {
			if err := files.WriteManaged(ctx, "kinds", dir, name, []byte(name), 0o600, uid, gid); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := files.EnsureServiceDir(ctx, "kinds", svc); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(svc, "certs", "a.crt"), []byte("cert"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := files.WriteFile(ctx, "kinds", unit, []byte("[Unit]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(restore, []byte("rules\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return m
}

// kindsConfig is h's uninstall with the demo module and kindsModule.
func kindsConfig(h *testHost, m *demoModule, km kindsModule, keepData bool) UninstallConfig {
	cfg := h.uninstallConfig(m, keepData)
	cfg.Modules = append(cfg.Modules, km)
	return cfg
}

func TestUninstallRemovesManagedDirs(t *testing.T) {
	h, m := installHost(t, RoleController)
	km := kindsInstall(t, h)
	outside, inside, _, _, _ := km.paths()
	u, err := PlanUninstall(context.Background(), kindsConfig(h, m, km, false))
	if err != nil {
		t.Fatal(err)
	}
	var plan bytes.Buffer
	u.Describe(&plan)
	_ = u.Close()
	for _, dir := range []string{outside, inside} {
		if want := "managed-dir " + dir + ", of module kinds"; !strings.Contains(plan.String(), want) {
			t.Errorf("plan lacks %q:\n%s", want, plan.String())
		}
	}
	if out, err := runUninstall(t, kindsConfig(h, m, km, false)); err != nil {
		t.Fatalf("Run: %v\n%s", err, out)
	}
	for _, dir := range []string{outside, inside} {
		if exists(t, dir) {
			t.Errorf("%s is left", dir)
		}
	}

	h, m = installHost(t, RoleController)
	km = kindsInstall(t, h)
	outside, inside, _, _, _ = km.paths()
	if out, err := runUninstall(t, kindsConfig(h, m, km, true)); err != nil {
		t.Fatalf("Run with --keep-data: %v\n%s", err, out)
	}
	if exists(t, outside) {
		t.Errorf("--keep-data kept %s, which is outside the data directory", outside)
	}
	for _, name := range []string{"a.token", "b.token"} {
		if !exists(t, filepath.Join(inside, name)) {
			t.Errorf("--keep-data removed %s from %s", name, inside)
		}
	}
	got := manifest(t, h.layout)
	if !slices.Contains(got, "managed-dir "+inside+" (module kinds)") || slices.Contains(got, "managed-dir "+outside+" (module kinds)") {
		t.Errorf("manifest %v: want the kept directory recorded and the removed one forgotten", got)
	}
}

func TestUninstallRefusesSymlinkedManagedDir(t *testing.T) {
	h, m := installHost(t, RoleController)
	km := kindsInstall(t, h)
	outside, _, _, _, _ := km.paths()
	if err := os.Rename(outside, outside+".real"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Base(outside)+".real", outside); err != nil {
		t.Fatal(err)
	}
	_, err := runUninstall(t, kindsConfig(h, m, km, false))
	if err == nil || !strings.Contains(err.Error(), "files: install: open managed directory "+outside) {
		t.Fatalf("Run = %v, want the files step to refuse the symlinked %s", err, outside)
	}
	if !exists(t, filepath.Join(outside+".real", "a.token")) {
		t.Error("the symlink's target directory lost its files")
	}
}

func TestUninstallRemovesServiceDirs(t *testing.T) {
	h, m := installHost(t, RoleController)
	km := kindsInstall(t, h)
	_, _, svc, _, _ := km.paths()
	extra := filepath.Join(svc, "extra")
	if err := os.Mkdir(extra, 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := runUninstall(t, kindsConfig(h, m, km, true))
	if err == nil || !strings.Contains(err.Error(), "files: ") || !strings.Contains(err.Error(), "extra") {
		t.Fatalf("Run = %v, want the files step to stop at the undeclared extra", err)
	}
	if !exists(t, filepath.Join(svc, "certs", "a.crt")) {
		t.Fatal("the refused removal removed part of the service directory")
	}
	if err := os.Remove(extra); err != nil {
		t.Fatal(err)
	}
	if out, err := runUninstall(t, kindsConfig(h, m, km, true)); err != nil {
		t.Fatalf("second Run: %v\n%s", err, out)
	}
	if exists(t, svc) {
		t.Errorf("%s is left", svc)
	}
	if got := manifest(t, h.layout); slices.Contains(got, "service-dir "+svc+" (module kinds)") {
		t.Errorf("manifest %v still records %s", got, svc)
	}
}

func TestUninstallStopsModuleUnits(t *testing.T) {
	h, m := installHost(t, RoleController)
	km := kindsInstall(t, h)
	_, _, _, unit, _ := km.paths()
	h.systemd.failOn("Disable darkpanel-kinds.service", errors.New("refused"))
	_, err := runUninstall(t, kindsConfig(h, m, km, false))
	if err == nil || !strings.Contains(err.Error(), "units: refused") {
		t.Fatalf("Run = %v, want the units step to fail at Disable", err)
	}
	if !exists(t, unit) {
		t.Fatal("the unit file was removed before the unit was disabled")
	}
	h.systemd.failOn("Disable darkpanel-kinds.service", nil)
	h.systemd.calls = nil
	if out, err := runUninstall(t, kindsConfig(h, m, km, false)); err != nil {
		t.Fatalf("second Run: %v\n%s", err, out)
	}
	if exists(t, unit) {
		t.Errorf("%s is left", unit)
	}
	want := []string{
		"Stop darkpanel-controller.service", "Stop darkpanel-agent.service",
		"Disable darkpanel-controller.service darkpanel-agent.service darkpanel-update-recover.service",
		"Stop darkpanel-kinds.service", "Disable darkpanel-kinds.service", "Stop demo.service", "Disable demo.service", "Reload",
	}
	if got := h.systemd.recorded(); !reflect.DeepEqual(got, want) {
		t.Errorf("systemd calls\n got %v\nwant %v", got, want)
	}
}

func TestUninstallLeavesRestoreAndSysctl(t *testing.T) {
	h, m := installHost(t, RoleController)
	km := kindsInstall(t, h)
	_, _, _, _, restore := km.paths()
	u, err := PlanUninstall(context.Background(), kindsConfig(h, m, km, false))
	if err != nil {
		t.Fatal(err)
	}
	var plan bytes.Buffer
	u.Describe(&plan)
	_ = u.Close()
	for _, s := range []string{restore, "ip_forward"} {
		if strings.Contains(plan.String(), s) {
			t.Errorf("the plan names %s:\n%s", s, plan.String())
		}
	}
	if out, err := runUninstall(t, kindsConfig(h, m, km, false)); err != nil {
		t.Fatalf("Run: %v\n%s", err, out)
	}
	if got := string(readBytes(t, restore)); got != "rules\n" {
		t.Errorf("the restore file holds %q, want it untouched", got)
	}
}
