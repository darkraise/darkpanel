package system

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/platform"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

var shStart = time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)

// planVec, resimVec and runVec are the space-joined apt-get vectors of Task 10's builders.
func planVec(verb string, names []string) string {
	return aptGet + " " + strings.Join(platform.SystemAptPlan(verb, names), " ")
}

func resimVec(verb string, names []string) string {
	return aptGet + " " + strings.Join(platform.SystemAptResim(verb, names), " ")
}

func runVec(verb string, names []string) string {
	return aptGet + " " + strings.Join(platform.SystemAptRun(verb, names), " ")
}

func shJoin(path string, args []string) string {
	return strings.Join(append([]string{path}, args...), " ")
}

type shAnswer struct {
	out   string
	exit  int
	after []string // runChanges: the installed set once the vector ran; nil leaves it
	err   error    // the runner's error, such as a missing program
}

// shPkg is one package of the dpkg-query default.
type shPkg struct{ version, arch, status, essential, priority string }

// sysHost is a host for the system module's tests: a migrated agent DB, a
// fake runner keyed by the space-joined vector, a file map, fake Docker,
// reboot and purge providers, and a clock fixed at 2026-11-19T12:00:00Z.
type sysHost struct {
	t     *testing.T
	db    *store.DB
	clock *coretest.FakeClock
	mu    sync.Mutex

	answers  map[string]shAnswer
	ran      []string
	pkgs     map[string]*shPkg
	files    map[string][]byte // what reads see: file(path) and what WriteFile wrote
	mtimes   map[string]time.Time
	wrote    map[string][]byte // what the module wrote through core.Env.Files
	removed  []string
	restores []core.RestoreEntry // every Restore call, in order
	arch     string
	uname    string
	journal  *shJournal

	docker  *shDocker
	reboot  *shReboot
	purge   *shPurge
	keyring []byte
	fwOn    bool
	changes int               //nolint:unused // shared helper of the system module's action tests
	last    map[string]string // action → id of its last successful applyTask
}

func newSysHost(t *testing.T) *sysHost {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(dir, "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	coreSet, err := store.CoreMigrations(store.SideAgent)
	if err != nil {
		t.Fatal(err)
	}
	res, err := store.Migrate(ctx, db, store.MigrateOptions{
		Side: store.SideAgent, BinaryVersion: "test", BackupRoot: filepath.Join(dir, "backups"), Keep: 5, Now: shStart,
	}, coreSet, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Failed) > 0 {
		t.Fatalf("migrations failed: %+v", res.Failed)
	}
	h := &sysHost{
		t: t, db: db, clock: coretest.NewFakeClock(shStart),
		answers: map[string]shAnswer{}, pkgs: map[string]*shPkg{}, files: map[string][]byte{}, mtimes: map[string]time.Time{},
		wrote: map[string][]byte{}, uname: "6.8.0-45-generic", journal: &shJournal{},
		docker: &shDocker{}, reboot: &shReboot{}, purge: &shPurge{}, last: map[string]string{},
	}
	h.osRelease("ubuntu", "noble", "amd64")
	h.installed("bash")
	h.run(aptGet+" "+strings.Join(platform.SystemAptCheck(), " "), "", 0)
	h.run(aptGet+" "+strings.Join(platform.SystemAptCheckWait(), " "), "", 0)
	h.run(shJoin("/usr/bin/dpkg", platform.SystemDpkgAudit()), "", 0)
	h.run(shJoin("/usr/bin/apt-cache", platform.SystemAptCachePkgnames()), "", 0)
	h.run(shJoin("/usr/bin/apt-config", platform.SystemAptConfigDump()), "", 0)
	// needrestart is not installed: the runner cannot find the program.
	h.answers[shJoin(needrestartBin, platform.SystemNeedrestart())] = shAnswer{err: fmt.Errorf("exec %s: %w", needrestartBin, fs.ErrNotExist)}
	return h
}

// mod is the module over the host's fakes.
func (h *sysHost) mod() module {
	return module{d: deps{
		readFile: h.readFile,
		listDir:  h.listDir,
		modTime:  h.modTime,
		stat: func(name string) (fs.FileMode, int, int, error) {
			if _, err := h.readFile(name); err != nil {
				return 0, 0, 0, err
			}
			return 0o640, 0, 4, nil // a prior file: root:adm, mode 0640
		},
		kernelRelease: func() (string, error) { return h.uname, nil },
		hostname:      func() (string, error) { return h.hostName(), nil },
		arch:          h.arch,
		docker:        h.docker,
		reboot:        h.reboot,
		purge:         h.purge,
		keyring:       h.keyring,
		firewallIntegration: func(context.Context, core.Env) (bool, error) {
			h.mu.Lock()
			defer h.mu.Unlock()
			return h.fwOn, nil
		},
	}}
}

func (h *sysHost) env() core.Env { return h.envOf("") }

func (h *sysHost) envOf(changeID string) core.Env {
	env := core.Env{
		HostID: "h1", ChangeID: changeID,
		Principal: core.Principal{Actor: "user:u1", UserID: "u1", Scope: core.ScopeAdmin},
		DB:        h.db, Exec: h, Files: shFiles{h: h}, Sealer: coretest.Sealer{}, Clock: h.clock,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if changeID != "" {
		h.journal.begin(changeID)
		env.Effects = h.journal
	}
	return env
}

// run registers the answer of vector, overriding a default.
func (h *sysHost) run(vector, out string, exit int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.answers[vector] = shAnswer{out: out, exit: exit}
}

// runChanges is run, and once vector ran the installed set is installedAfter; "name=version" sets a version.
func (h *sysHost) runChanges(vector, out string, exit int, installedAfter ...string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.answers[vector] = shAnswer{out: out, exit: exit, after: append([]string{}, installedAfter...)}
}

// updatesReadable registers empty plain and phased dist-upgrade reads and apt-mark showhold.
func (h *sysHost) updatesReadable() {
	empty := "0 upgraded, 0 newly installed, 0 to remove and 0 not upgraded.\n"
	h.run(aptGet+" "+strings.Join(platform.SystemAptRead("dist-upgrade", nil, nil), " "), empty, 0)
	h.run(aptGet+" "+strings.Join(platform.SystemAptRead("dist-upgrade", []string{"-o", "APT::Get::Always-Include-Phased-Updates=true"}, nil), " "), empty, 0)
	h.run(shJoin("/usr/bin/apt-mark", platform.SystemAptMarkShowhold()), "", 0)
}

// Run implements core.Runner: only vectors the production rules allow, and only registered ones.
func (h *sysHost) Run(_ context.Context, cmd core.Cmd) (core.CmdResult, error) {
	key := shJoin(cmd.Path, cmd.Args)
	h.mu.Lock()
	defer h.mu.Unlock()
	h.ran = append(h.ran, key)
	if !shAllowed(cmd.Path, cmd.Args) {
		h.t.Errorf("sysHost: the production exec rules refuse %q", key)
		return core.CmdResult{}, errors.New("sysHost: vector not allowed")
	}
	if cmd.Path == dpkgQuery && slices.Equal(cmd.Args, platform.SystemDpkgQuery()) {
		if a, ok := h.answers[key]; ok {
			return core.CmdResult{Stdout: []byte(a.out), ExitCode: a.exit}, nil
		}
		return core.CmdResult{Stdout: h.dpkgOutput()}, nil
	}
	a, ok := h.answers[key]
	if !ok {
		h.t.Errorf("sysHost: no answer registered for %q", key)
		return core.CmdResult{}, fmt.Errorf("sysHost: no answer for %q", key)
	}
	if a.err != nil {
		return core.CmdResult{}, a.err
	}
	if a.after != nil {
		h.setInstalledLocked(a.after)
	}
	return core.CmdResult{Stdout: []byte(a.out), ExitCode: a.exit}, nil
}

func shAllowed(path string, args []string) bool {
	for _, rule := range platform.ProductionRules() {
		if rule.Path == path && rule.Match(args) == nil {
			return true
		}
	}
	return false
}

// calls returns every vector run, space-joined.
func (h *sysHost) calls() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.ran)
}

func (h *sysHost) dpkgOutput() []byte {
	var b bytes.Buffer
	for _, name := range slices.Sorted(maps.Keys(h.pkgs)) {
		p := h.pkgs[name]
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", name, p.version, p.arch, p.status, p.essential, p.priority, name+" package")
	}
	return b.Bytes()
}

func (h *sysHost) setInstalledLocked(names []string) {
	pkgs := map[string]*shPkg{}
	for _, n := range names {
		name, version, ok := strings.Cut(n, "=")
		if p, kept := h.pkgs[name]; kept && !ok {
			pkgs[name] = p
			continue
		}
		if !ok {
			version = "1.0-1"
		}
		pkgs[name] = &shPkg{version: version, arch: h.arch, status: "install ok installed", priority: "optional"}
	}
	h.pkgs = pkgs
}

// installed rewrites the dpkg-query default: names installed at 1.0-1 (bash is the default).
func (h *sysHost) installed(names ...string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.pkgs = map[string]*shPkg{}
	h.setInstalledLocked(names)
}

func (h *sysHost) pkg(name string) *shPkg { //nolint:unused // shared helper of the system module's action tests
	if p, ok := h.pkgs[name]; ok {
		return p
	}
	p := &shPkg{version: "1.0-1", arch: h.arch, status: "install ok installed", priority: "optional"}
	h.pkgs[name] = p
	return p
}

// dpkgStatus sets name's dpkg status, such as "install ok half-configured".
func (h *sysHost) dpkgStatus(name, status string) { //nolint:unused // shared helper of the system module's action tests
	h.mu.Lock()
	defer h.mu.Unlock()
	h.pkg(name).status = status
}

// dpkgFields sets control fields by package: "Essential: yes" or "Priority: required".
func (h *sysHost) dpkgFields(fields map[string]string) { //nolint:unused // shared helper of the system module's action tests
	h.mu.Lock()
	defer h.mu.Unlock()
	for name, field := range fields {
		key, value, _ := strings.Cut(field, ":")
		value = strings.TrimSpace(value)
		switch strings.TrimSpace(key) {
		case "Essential":
			h.pkg(name).essential = value
		case "Priority":
			h.pkg(name).priority = value
		default:
			h.t.Fatalf("sysHost: dpkgFields does not know the field %q", key)
		}
	}
}

// dpkgVersions sets installed versions by package.
func (h *sysHost) dpkgVersions(versions map[string]string) { //nolint:unused // shared helper of the system module's action tests
	h.mu.Lock()
	defer h.mu.Unlock()
	for name, v := range versions {
		h.pkg(name).version = v
	}
}

func (h *sysHost) osRelease(id, codename, arch string) {
	h.mu.Lock()
	h.arch = arch
	for _, p := range h.pkgs {
		p.arch = arch
	}
	h.mu.Unlock()
	h.file("/etc/os-release", fmt.Appendf(nil, "ID=%s\nVERSION_CODENAME=%s\n", id, codename))
}

func (h *sysHost) unameR(release string) { //nolint:unused // shared helper of the system module's action tests
	h.mu.Lock()
	defer h.mu.Unlock()
	h.uname = release
}

// file puts data at path for reads, with the clock's time as its mtime.
func (h *sysHost) file(path string, data []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.files[path] = bytes.Clone(data)
	h.mtimes[path] = h.clock.Now()
}

func (h *sysHost) readFile(name string) ([]byte, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if b, ok := h.files[name]; ok {
		return bytes.Clone(b), nil
	}
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
}

// listDir lists the files of the file map directly under dir.
func (h *sysHost) listDir(dir string) ([]string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var names []string
	for p := range h.files {
		if name, ok := strings.CutPrefix(p, dir+"/"); ok && !strings.Contains(name, "/") {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names, nil
}

func (h *sysHost) modTime(name string) (time.Time, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if t, ok := h.mtimes[name]; ok {
		return t, nil
	}
	return time.Time{}, &fs.PathError{Op: "stat", Path: name, Err: fs.ErrNotExist}
}

func (h *sysHost) written(path string) []byte { //nolint:unused // shared helper of the system module's action tests
	h.mu.Lock()
	defer h.mu.Unlock()
	return bytes.Clone(h.wrote[path])
}

func (h *sysHost) writtenPaths() []string { //nolint:unused // shared helper of the system module's action tests
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Sorted(maps.Keys(h.wrote))
}

func (h *sysHost) exists(path string) bool { //nolint:unused // shared helper of the system module's action tests
	h.mu.Lock()
	defer h.mu.Unlock()
	_, ok := h.files[path]
	return ok
}

func (h *sysHost) hostName() string         { return "web-1" }             //nolint:unused // shared helper of the system module's action tests
func (h *sysHost) now() time.Time           { return h.clock.Now() }       //nolint:unused // shared helper of the system module's action tests
func (h *sysHost) keyringOverride(b []byte) { h.keyring = bytes.Clone(b) } //nolint:unused // shared helper of the system module's action tests

func (h *sysHost) firewallDockerIntegration(on bool) { //nolint:unused // shared helper of the system module's action tests
	h.mu.Lock()
	defer h.mu.Unlock()
	h.fwOn = on
}

// firewallEnabled writes the agent DB's module_enabled.firewall.
func (h *sysHost) firewallEnabled(on bool) { //nolint:unused // shared helper of the system module's action tests
	v := "0"
	if on {
		v = "1"
	}
	h.exec(`INSERT INTO core_meta (key, value) VALUES ('module_enabled.firewall', ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value`, v)
}

func (h *sysHost) dockerDaemon(running bool) { //nolint:unused // shared helper of the system module's action tests
	h.docker.mu.Lock()
	defer h.docker.mu.Unlock()
	h.docker.running = running
}

// dockerCounts sets the daemon's counts; labelled maps a container name to its darkpanel module label.
func (h *sysHost) dockerCounts(containers, images, volumes int, labelled map[string]string) { //nolint:unused // shared helper of the system module's action tests
	h.docker.mu.Lock()
	defer h.docker.mu.Unlock()
	h.docker.containers, h.docker.images, h.docker.volumes = containers, images, volumes
	h.docker.labelled = maps.Clone(labelled)
}

func (h *sysHost) exec(query string, args ...any) { //nolint:unused // shared helper of the system module's action tests
	h.t.Helper()
	err := h.db.Write(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.Exec(query, args...)
		return err
	})
	if err != nil {
		h.t.Fatal(err)
	}
}

// pendingChange inserts a change of action in state into the agent DB's core_changes.
func (h *sysHost) pendingChange(id, action, state string) { h.change(id, action, state, "") } //nolint:unused // shared helper of the system module's action tests

func (h *sysHost) change(id, action, state, output string) { //nolint:unused // shared helper of the system module's action tests
	at := h.clock.Now().UTC().Format(time.RFC3339Nano)
	h.exec(`INSERT INTO core_plans (id, host_id, action, version, actor, input_hash, state_hash, diff_json, guards_json,
		confirmation, created_at, expires_at) VALUES (?, 'h1', ?, 1, 'user:u1', 'h', 's', '[]', '[]', '', ?, ?)`, "P"+id, action, at, at)
	var out any
	if output != "" {
		out = output
	}
	h.exec(`INSERT INTO core_changes (id, plan_id, host_id, action, version, class, compensated_by, actor, state, input_json,
		output_json, created_at, updated_at) VALUES (?, ?, 'h1', ?, 1, 'compensable', '', 'user:u1', ?, '{}', ?, ?, ?)`,
		id, "P"+id, action, state, out, at, at)
}

func (h *sysHost) clearChanges() { //nolint:unused // shared helper of the system module's action tests
	h.exec(`DELETE FROM core_changes`)
	h.exec(`DELETE FROM core_plans`)
}

// recordedInstall is a completed system.packages.install change whose output installed names, with its made
// effect journaled, so undo("system.packages.install") undoes it.
func (h *sysHost) recordedInstall(names []string) { //nolint:unused // shared helper of the system module's action tests
	out, _ := json.Marshal(Applied{Installed: names, Upgraded: []string{}, Removed: []string{}})
	id := fmt.Sprintf("rec-%d", len(h.calls()))
	h.change(id, "system.packages.install", core.StateCompleted, string(out))
	ctx := context.Background()
	h.journal.begin(id)
	seq, err := h.journal.Intend(ctx, installEffect(names), nil)
	if err == nil {
		err = h.journal.Made(ctx, seq, installResource(names), core.StateDoc{State: json.RawMessage(`{"present":true}`)})
	}
	if err != nil {
		h.t.Fatal(err)
	}
	h.mu.Lock()
	h.last["system.packages.install"] = id
	h.mu.Unlock()
}

// action returns the module's definition of name.
func (h *sysHost) action(name string) (core.ActionDef, error) { //nolint:unused // shared helper of the system module's action tests
	for _, a := range h.mod().Manifest().Actions {
		if a.Spec.Name == name {
			return a, nil
		}
	}
	return core.ActionDef{}, fmt.Errorf("sysHost: the system module has no action %s", name)
}

func (h *sysHost) readResource(t *testing.T, name string) string { //nolint:unused // shared helper of the system module's action tests
	t.Helper()
	for _, r := range h.mod().Manifest().Resources {
		if r.Name == ID+"."+name {
			out, err := r.Read(context.Background(), h.env())
			if err != nil {
				t.Fatalf("read %s: %v", name, err)
			}
			return string(out)
		}
	}
	t.Fatalf("the system module has no resource %s", name)
	return ""
}

func (h *sysHost) callRead(t *testing.T, action, input string) string { //nolint:unused // shared helper of the system module's action tests
	t.Helper()
	out, err := h.callReadErr(action, input)
	if err != nil {
		t.Fatalf("%s: %v", action, err)
	}
	return out
}

func (h *sysHost) callReadErr(action, input string) (string, error) { //nolint:unused // shared helper of the system module's action tests
	a, err := h.action(action)
	if err != nil {
		return "", err
	}
	out, err := a.Handler.Read(context.Background(), h.env(), json.RawMessage(input))
	return string(out), err
}

// PlanView is a plan of the module: its outcome and the input it was planned for.
type PlanView struct {
	core.PlanOutcome
	Action, Input string
	Phrase        string // the typed confirmation, "" when none
}

// Text is the plan's diff, one line per entry.
func (p PlanView) Text() string {
	var b strings.Builder
	for _, l := range p.Diff {
		b.WriteString(l.Op + " " + l.Text + "\n")
	}
	return b.String()
}

func (h *sysHost) plan(t *testing.T, action, input string) PlanView { //nolint:unused // shared helper of the system module's action tests
	t.Helper()
	p, err := h.planErr(action, input)
	if err != nil {
		t.Fatalf("plan %s: %v", action, err)
	}
	return p
}

func (h *sysHost) planErr(action, input string) (PlanView, error) { //nolint:unused // shared helper of the system module's action tests
	a, err := h.action(action)
	if err != nil {
		return PlanView{}, err
	}
	out, err := a.Handler.Plan(context.Background(), h.env(), json.RawMessage(input))
	return PlanView{PlanOutcome: out, Action: action, Input: input, Phrase: out.Confirmation}, err
}

// applyTask runs action's task in a new change, as the task unit would.
func (h *sysHost) applyTask(action, input string) (string, error) { //nolint:unused // shared helper of the system module's action tests
	a, err := h.action(action)
	if err != nil {
		return "", err
	}
	if a.Handler.Task == nil {
		return "", fmt.Errorf("sysHost: %s is not a task", action)
	}
	h.mu.Lock()
	h.changes++
	id := fmt.Sprintf("c%d", h.changes)
	h.mu.Unlock()
	out, err := a.Handler.Task(context.Background(), h.envOf(id), json.RawMessage(input))
	if err == nil {
		h.mu.Lock()
		h.last[action] = id
		h.mu.Unlock()
	}
	return string(out), err
}

// applyPlan applies p as the engine does: it plans again and refuses with plan.state_changed when the state
// hash differs, then runs the task.
func (h *sysHost) applyPlan(p PlanView) (string, error) { //nolint:unused // shared helper of the system module's action tests
	again, err := h.planErr(p.Action, p.Input)
	if err != nil {
		return "", err
	}
	if again.StateHash != p.StateHash {
		return "", errcode.New(errcode.PlanStateChanged, "the host changed since the plan")
	}
	return h.applyTask(p.Action, p.Input)
}

// undo inverts, last first, the effects of the last successful applyTask of action.
func (h *sysHost) undo(action string) error { //nolint:unused // shared helper of the system module's action tests
	h.mu.Lock()
	id, ok := h.last[action]
	h.mu.Unlock()
	if !ok {
		return fmt.Errorf("sysHost: no change of %s to undo", action)
	}
	a, err := h.action(action)
	if err != nil {
		return err
	}
	if a.Handler.Inverse == nil {
		return fmt.Errorf("sysHost: %s has no undo", action)
	}
	effects := h.journal.of(id)
	for i := len(effects) - 1; i >= 0; i-- {
		if effects[i].State != core.EffectMade {
			continue
		}
		if _, err := a.Handler.Inverse(context.Background(), h.envOf(id), effects[i]); err != nil {
			return err
		}
	}
	return nil
}

// shFiles is core.FileWriter over the host's file map; the methods the module does not use are left nil.
type shFiles struct {
	core.FileWriter
	h *sysHost
}

func (f shFiles) WriteFile(_ context.Context, module, path string, data []byte, _ fs.FileMode) error {
	if module != ID {
		return fmt.Errorf("sysHost: module %s wrote %s", module, path)
	}
	f.h.mu.Lock()
	defer f.h.mu.Unlock()
	f.h.wrote[path] = bytes.Clone(data)
	f.h.files[path] = bytes.Clone(data)
	f.h.mtimes[path] = f.h.clock.Now()
	return nil
}

// WriteManaged records the file at dir/name, as WriteFile does.
func (f shFiles) WriteManaged(ctx context.Context, module, dir, name string, data []byte, mode fs.FileMode, _, _ int) error {
	return f.WriteFile(ctx, module, dir+"/"+name, data, mode)
}

// Restore writes or removes e.Path as the platform's managed-file restore does and records e.
func (f shFiles) Restore(ctx context.Context, module string, e core.RestoreEntry) error {
	f.h.mu.Lock()
	f.h.restores = append(f.h.restores, e)
	f.h.mu.Unlock()
	if e.Present {
		return f.WriteFile(ctx, module, e.Path, e.Data, e.Mode)
	}
	return f.Remove(ctx, module, e.Path)
}

// Owns reports the paths the module wrote, as the ownership store records every write.
func (f shFiles) Owns(_ context.Context, module, path string) (bool, error) {
	f.h.mu.Lock()
	defer f.h.mu.Unlock()
	_, ok := f.h.wrote[path]
	return ok && module == ID, nil
}

func (f shFiles) Remove(_ context.Context, module, path string) error {
	if module != ID {
		return fmt.Errorf("sysHost: module %s removed %s", module, path)
	}
	f.h.mu.Lock()
	defer f.h.mu.Unlock()
	delete(f.h.wrote, path)
	delete(f.h.files, path)
	f.h.removed = append(f.h.removed, path)
	return nil
}

// shJournal is core.EffectJournal in memory, by change.
type shJournal struct {
	mu      sync.Mutex
	current string
	list    map[string][]core.Effect
}

func (j *shJournal) begin(change string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.current = change
	if j.list == nil {
		j.list = map[string][]core.Effect{}
	}
}

func (j *shJournal) of(change string) []core.Effect {
	j.mu.Lock()
	defer j.mu.Unlock()
	return slices.Clone(j.list[change])
}

func (j *shJournal) Intend(_ context.Context, e core.Effect, _ []byte) (int, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	e.ChangeID, e.Seq, e.State = j.current, len(j.list[j.current])+1, core.EffectIntended
	j.list[j.current] = append(j.list[j.current], e)
	return e.Seq, nil
}

func (j *shJournal) set(seq int, f func(*core.Effect)) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	l := j.list[j.current]
	if seq < 1 || seq > len(l) || l[seq-1].State != core.EffectIntended {
		return fmt.Errorf("sysHost: effect %d is not intended", seq)
	}
	f(&l[seq-1])
	return nil
}

func (j *shJournal) Made(_ context.Context, seq int, resource string, post core.StateDoc) error {
	return j.set(seq, func(e *core.Effect) { e.State, e.Resource, e.Post = core.EffectMade, resource, &post })
}

func (j *shJournal) Skipped(_ context.Context, seq int) error {
	return j.set(seq, func(e *core.Effect) { e.State = core.EffectSkipped })
}

func (j *shJournal) List(context.Context) ([]core.Effect, error) { return j.of(j.current), nil }

func (j *shJournal) Secret(context.Context, int) ([]byte, error) { return nil, nil }

// shDocker is the fake Docker Engine API: the daemon answers when running.
type shDocker struct {
	mu                          sync.Mutex
	running                     bool
	containers, images, volumes int
	labelled                    map[string]string
}

var errSHDockerDown = errors.New("sysHost: the Docker daemon does not answer")

func (d *shDocker) Info(context.Context) (dockerapi.SystemInfo, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.running {
		return dockerapi.SystemInfo{}, errSHDockerDown
	}
	return dockerapi.SystemInfo{Containers: d.containers, ContainersRunning: d.containers, Images: d.images}, nil
}

func (d *shDocker) ListContainers(context.Context, dockerapi.ListContainersOptions) ([]dockerapi.ContainerSummary, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.running {
		return nil, errSHDockerDown
	}
	var out []dockerapi.ContainerSummary
	for _, name := range slices.Sorted(maps.Keys(d.labelled)) {
		out = append(out, dockerapi.ContainerSummary{ID: name, Names: []string{"/" + name}, Labels: map[string]string{core.LabelModule: d.labelled[name]}})
	}
	return out, nil
}

func (d *shDocker) ListVolumes(context.Context, dockerapi.Filters) ([]dockerapi.Volume, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.running {
		return nil, errSHDockerDown
	}
	out := make([]dockerapi.Volume, d.volumes)
	for i := range out {
		out[i].Name = fmt.Sprintf("v%d", i+1)
	}
	return out, nil
}

// shReboot records the scheduled time, or answers err.
type shReboot struct {
	scheduledAt time.Time
	err         error
}

func (r *shReboot) ScheduleReboot(_ context.Context, at time.Time) error {
	if r.err != nil {
		return r.err
	}
	r.scheduledAt = at
	return nil
}

// shPurge answers checkErr from both methods and records each purged path.
type shPurge struct {
	checkErr error
	purgeErr error // a PurgeDir failure the plan-time check did not foresee
	purged   []string
}

func (p *shPurge) CheckPurgeable(string) error { return p.checkErr }

func (p *shPurge) PurgeDir(path string) error {
	if p.checkErr != nil {
		return p.checkErr
	}
	if p.purgeErr != nil {
		return p.purgeErr
	}
	p.purged = append(p.purged, path)
	return nil
}

func (*sysHost) Stream(context.Context, string, []string, func([]byte) error) error {
	return errors.New("sysHost: Stream is not faked")
}
