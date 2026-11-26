package fail2ban

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform"
)

// rig is one host: files and directories, unit states, the answers of
// allowlisted commands, and one ordered log of what the module did.
type rig struct {
	t       *testing.T
	events  []string
	answers map[string]core.CmdResult
	files   map[string][]byte
	dirs    map[string]bool
	units   map[string]core.UnitState
	effects *effects
	addrs   []string
}

func newRig(t *testing.T) *rig {
	r := &rig{
		t: t, answers: map[string]core.CmdResult{}, files: map[string][]byte{},
		dirs: map[string]bool{"/etc": true, etcDir: true, jailDir: true},
		units: map[string]core.UnitState{
			fail2banUnit: {LoadState: "loaded", ActiveState: "active"},
		},
		effects: &effects{}, addrs: []string{"198.51.100.7"},
	}
	r.answer(platform.Fail2banClientPath, platform.Fail2banVersion(), testdata(t, "version"))
	r.answer(platform.Fail2banClientPath, platform.Fail2banStatus(""), testdata(t, "status"))
	r.answer(platform.Fail2banClientPath, platform.Fail2banStatus("sshd"), testdata(t, "status-sshd"))
	r.answer(platform.Fail2banClientPath, platform.Fail2banBanTimes("sshd"), testdata(t, "banip-sshd"))
	r.answer(platform.SshdPath, platform.SshdEffective(), testdata(t, "sshd-T"))
	for _, v := range [][]string{platform.Fail2banTest(), platform.Fail2banReload()} {
		r.answer(platform.Fail2banClientPath, v, "")
	}
	return r
}

func testdata(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name)) //nolint:gosec // G304: a fixed name under testdata
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func key(path string, args []string) string {
	return strings.Join(append([]string{path}, args...), " ")
}

func (r *rig) answer(path string, args []string, stdout string) {
	r.answers[key(path, args)] = core.CmdResult{Stdout: []byte(stdout)}
}

func (r *rig) fail(path string, args []string, code int, stderr string) {
	r.answers[key(path, args)] = core.CmdResult{ExitCode: code, Stderr: []byte(stderr)}
}

func (r *rig) module() module {
	return module{d: deps{readFile: func(name string) ([]byte, error) {
		b, ok := r.files[name]
		if !ok {
			return nil, fs.ErrNotExist
		}
		return slices.Clone(b), nil
	}}}
}

func (r *rig) env(p core.Principal) core.Env {
	return core.Env{
		HostID: "h1", Principal: p, Exec: runner{r}, Files: files{r}, Units: units{r: r},
		Effects: r.effects, ControllerAddrs: r.addrs,
	}
}

var (
	adminP    = core.Principal{Actor: "user:admin", UserID: "admin", Scope: core.ScopeAdmin, Source: "192.0.2.1"}
	operatorP = core.Principal{Actor: "user:op", UserID: "op", Source: "192.0.2.2", Access: core.Access{All: core.ScopeOperate}}
)

// runner answers the commands the rig knows, refusing any vector the
// agent's exec allowlist would refuse.
type runner struct{ r *rig }

func (x runner) Run(_ context.Context, cmd core.Cmd) (core.CmdResult, error) {
	k := key(cmd.Path, cmd.Args)
	allowed := false
	for _, rule := range platform.ProductionRules() {
		if rule.Path == cmd.Path && rule.Match(cmd.Args) == nil {
			allowed = true
		}
	}
	if !allowed {
		x.r.t.Errorf("%q is not in the exec allowlist", k)
		return core.CmdResult{}, fmt.Errorf("refused %q", k)
	}
	x.r.events = append(x.r.events, "run "+k)
	res, ok := x.r.answers[k]
	if !ok {
		x.r.t.Errorf("unexpected command %q", k)
		return core.CmdResult{ExitCode: 1}, nil
	}
	return res, nil
}

func (runner) Stream(context.Context, string, []string, func([]byte) error) error {
	return fmt.Errorf("no streams")
}

// files is the rig's FileWriter: a write needs its directory.
type files struct{ r *rig }

var _ core.DirMaker = files{}

func (f files) WriteFile(_ context.Context, module, path string, data []byte, mode fs.FileMode) error {
	if module != ID || path != SettingsFile || mode != 0o644 {
		return fmt.Errorf("write %s %s %v: not the settings file", module, path, mode)
	}
	if !f.r.dirs[filepath.Dir(path)] {
		return fmt.Errorf("write %s: no directory", path)
	}
	f.r.events = append(f.r.events, "write "+path)
	f.r.files[path] = slices.Clone(data)
	return nil
}

func (f files) Remove(_ context.Context, _, path string) error {
	f.r.events = append(f.r.events, "remove "+path)
	delete(f.r.files, path)
	return nil
}

func (f files) MakeDir(_ context.Context, module, dir string) error {
	if module != ID || !f.r.dirs[filepath.Dir(dir)] {
		return fmt.Errorf("mkdir %s: no parent", dir)
	}
	if !f.r.dirs[dir] {
		f.r.events = append(f.r.events, "mkdir "+dir+" 0755 root")
		f.r.dirs[dir] = true
	}
	return nil
}

func (files) ReplaceBlock(context.Context, string, string, string, []byte) error { return errUnused }
func (files) RemoveBlock(context.Context, string, string, string) error          { return errUnused }
func (files) Restore(context.Context, string, core.RestoreEntry) error           { return errUnused }
func (files) ReadSysctl(context.Context, string, string) (string, error)         { return "", errUnused }
func (files) WriteSysctl(context.Context, string, string, string) error          { return errUnused }
func (files) WriteManaged(context.Context, string, string, string, []byte, fs.FileMode, int, int) error {
	return errUnused
}
func (files) RemoveManaged(context.Context, string, string, string) error    { return errUnused }
func (files) Rename(context.Context, string, string, string) error           { return errUnused }
func (files) EnsureServiceDir(context.Context, string, string) error         { return errUnused }
func (files) RenameIn(context.Context, string, string, string, string) error { return errUnused }
func (files) RemoveIn(context.Context, string, string, string) error         { return errUnused }
func (files) RemoveServiceDir(context.Context, string, string) error         { return errUnused }
func (files) Owns(context.Context, string, string) (bool, error)             { return false, errUnused }

var errUnused = fmt.Errorf("not used by the fail2ban module")

// units reads the rig's unit states; a unit it does not list is not found.
type units struct {
	core.Units
	r *rig
}

func (u units) State(_ context.Context, name string) (core.UnitState, error) {
	if st, ok := u.r.units[name]; ok {
		return st, nil
	}
	return core.UnitState{LoadState: "not-found", ActiveState: "inactive"}, nil
}

// effects records the journal calls of one change.
type effects struct {
	intended []core.Effect
	made     []int
	skipped  []int
}

func (e *effects) Intend(_ context.Context, eff core.Effect, _ []byte) (int, error) {
	e.intended = append(e.intended, eff)
	return len(e.intended), nil
}
func (e *effects) Made(_ context.Context, seq int, _ string, _ core.StateDoc) error {
	e.made = append(e.made, seq)
	return nil
}
func (e *effects) Skipped(_ context.Context, seq int) error {
	e.skipped = append(e.skipped, seq)
	return nil
}
func (e *effects) List(context.Context) ([]core.Effect, error) { return nil, nil }
func (e *effects) Secret(context.Context, int) ([]byte, error) { return nil, nil }

func wantCode(t *testing.T, what string, err error, code errcode.Code) {
	t.Helper()
	if got := errcode.From(err).Code; err == nil || got != code {
		t.Errorf("%s: err = %v (%s), want %s", what, err, got, code)
	}
}

// uninstalled is the rig of a host without fail2ban or its directories.
func uninstalled(t *testing.T) *rig {
	r := newRig(t)
	delete(r.units, fail2banUnit)
	delete(r.dirs, etcDir)
	delete(r.dirs, jailDir)
	r.answer("/usr/bin/apt-get", platform.SystemAptCheck(), "")
	r.answer("/usr/bin/apt-get", platform.SystemAptCheckWait(), "")
	r.answer("/usr/bin/apt-get", platform.SystemAptRun("install", platform.Fail2banPackages), "Setting up fail2ban (1.0.2-2) ...\n")
	return r
}

func TestInstallWritesSettingsFirst(t *testing.T) {
	r := uninstalled(t)
	h := installHandler{m: r.module()}
	ctx := context.Background()
	plan, err := h.Plan(ctx, r.env(adminP), struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Diff) != 2 || !strings.Contains(plan.Diff[0].Text, SettingsFile) {
		t.Errorf("plan diff %+v", plan.Diff)
	}
	r.events = nil
	out, err := h.Task(ctx, r.env(adminP), struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	install := "run " + key("/usr/bin/apt-get", platform.SystemAptRun("install", platform.Fail2banPackages))
	want := []string{
		"mkdir " + etcDir + " 0755 root",
		"mkdir " + jailDir + " 0755 root",
		"run " + key(platform.SshdPath, platform.SshdEffective()),
		"write " + SettingsFile,
		"run " + key("/usr/bin/apt-get", platform.SystemAptCheckWait()),
		install,
	}
	if !slices.Equal(r.events, want) {
		t.Errorf("events\n%s\nwant\n%s", strings.Join(r.events, "\n"), strings.Join(want, "\n"))
	}
	got := string(r.files[SettingsFile])
	for _, line := range []string{"banaction = nftables-multiport", "banaction_allports = nftables-allports", "backend = systemd",
		"ignoreip = 127.0.0.1/8 ::1 198.51.100.7 100.64.0.0/10 fd7a:115c:a1e0::/48", "[sshd]\nenabled = true\nport = 22"} {
		if !strings.Contains(got, line) {
			t.Errorf("settings file lacks %q:\n%s", line, got)
		}
	}
	if !slices.Equal(out.Packages, []string{"fail2ban", "python3-systemd"}) {
		t.Errorf("output %+v", out)
	}

	// A settings file of an earlier install is kept as it is.
	r = uninstalled(t)
	r.dirs[etcDir], r.dirs[jailDir] = true, true
	r.files[SettingsFile] = []byte("kept")
	if _, err := (installHandler{m: r.module()}).Task(ctx, r.env(adminP), struct{}{}); err != nil {
		t.Fatal(err)
	}
	if string(r.files[SettingsFile]) != "kept" || slices.ContainsFunc(r.events, func(e string) bool { return strings.HasPrefix(e, "write") }) {
		t.Errorf("an existing settings file was rewritten: %v", r.events)
	}

	// Installing over an installed fail2ban is refused.
	r = newRig(t)
	_, err = (installHandler{m: r.module()}).Plan(ctx, r.env(adminP), struct{}{})
	wantCode(t, "install over an installed fail2ban", err, errcode.InvalidInput)
}

func TestCrowdSecPresent(t *testing.T) {
	ctx := context.Background()
	r := uninstalled(t)
	r.units[crowdsecUnit] = core.UnitState{LoadState: "loaded", ActiveState: "active"}
	h := installHandler{m: r.module()}
	for name, call := range map[string]func() error{
		"plan": func() error { _, err := h.Plan(ctx, r.env(adminP), struct{}{}); return err },
		"task": func() error { _, err := h.Task(ctx, r.env(adminP), struct{}{}); return err },
	} {
		err := call()
		wantCode(t, name, err, errcode.ModuleUnavailable)
		if err == nil || !strings.Contains(errcode.From(err).Detail, "CrowdSec is installed on this host") {
			t.Errorf("%s: %v, want the CrowdSec reason", name, err)
		}
	}
	if len(r.events) != 0 {
		t.Errorf("a refused install ran %v", r.events)
	}

	// An existing fail2ban stays manageable, and its settings plan warns.
	r = newRig(t)
	r.units[crowdsecUnit] = core.UnitState{LoadState: "loaded", ActiveState: "active"}
	plan, err := settingsHandler{m: r.module()}.Plan(ctx, r.env(adminP), SettingsInput{Settings: settings()})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(plan.Diff, func(l core.DiffLine) bool { return strings.Contains(l.Text, "CrowdSec") }) {
		t.Errorf("no CrowdSec warning in %+v", plan.Diff)
	}
	st, err := statusReader{m: r.module()}.Read(ctx, r.env(adminP), struct{}{})
	if err != nil || !st.CrowdSec || !st.Installed {
		t.Errorf("status %+v, %v; want CrowdSec and fail2ban reported", st, err)
	}
}

func TestRemoveRefusesOtherRemovals(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	r.answer("/usr/bin/apt-get", platform.SystemAptCheck(), "")
	sim := platform.SystemAptPlan("remove", platform.Fail2banPackages)
	r.answer("/usr/bin/apt-get", sim, "Remv fail2ban [1.0.2-2]\nRemv python3-systemd [235-1]\n")
	if _, err := (removeHandler{}).Plan(ctx, r.env(adminP), struct{}{}); err != nil {
		t.Fatal(err)
	}
	r.answer("/usr/bin/apt-get", sim, "Remv fail2ban [1.0.2-2]\nRemv python3-systemd [235-1]\nRemv borgmatic [1.7-1]\n")
	_, err := (removeHandler{}).Plan(ctx, r.env(adminP), struct{}{})
	wantCode(t, "a removal taking borgmatic along", err, errcode.SystemRemovalRefused)
}

// settings are test settings: 10-minute bans after 5 failures, with
// 203.0.113.0/24 as the admin's own ignore entry.
func settings() Settings {
	return Settings{BantimeMinutes: 10, FindtimeMinutes: 10, MaxRetry: 5,
		IgnoreIP: []string{"127.0.0.1/8", "::1", "198.51.100.7", "100.64.0.0/10", "fd7a:115c:a1e0::/48", "203.0.113.0/24"}}
}

// decode reads a JSON string state.
func decode(t *testing.T, doc core.StateDoc) *string {
	t.Helper()
	var s *string
	if err := json.Unmarshal(doc.State, &s); err != nil {
		t.Fatal(err)
	}
	return s
}
