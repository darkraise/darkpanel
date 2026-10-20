package install

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/platform"
)

// manifest lists agent.db's ownership manifest as "kind path" lines, with
// " (module <id>)" after an entry of a module other than the core.
func manifest(t *testing.T, l Layout) []string {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, l.Paths.AgentDB())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	entries, err := platform.NewOwnership(db).List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		line := e.Kind + " " + e.Path
		if e.Module != core.ModuleIDCore {
			line += " (module " + e.Module + ")"
		}
		out = append(out, line)
	}
	return out
}

// wantManifest is OwnedPaths(l, role) as manifest lists it.
func wantManifest(l Layout, role Role) []string {
	var out []string
	for _, op := range OwnedPaths(l, role) {
		out = append(out, op.Kind+" "+op.Path)
	}
	slices.SortFunc(out, func(a, b string) int {
		return strings.Compare(a[strings.IndexByte(a, ' ')+1:], b[strings.IndexByte(b, ' ')+1:])
	})
	return out
}

func wantMode(t *testing.T, path string, mode fs.FileMode) {
	t.Helper()
	st, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != mode {
		t.Errorf("%s has mode %o, want %o", path, st.Mode().Perm(), mode)
	}
}

func readBytes(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // G304: a test's temporary path
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestInstallController(t *testing.T) {
	h := newTestHost(t)
	cfg := h.config(RoleController)
	cfg.AgentListen = []string{"10.77.0.10:7443"}
	if err := Install(context.Background(), cfg); err != nil {
		t.Fatalf("Install: %v\n%s", err, h.out)
	}
	l, p := h.layout, h.layout.Paths
	for _, d := range []struct {
		path string
		mode fs.FileMode
	}{
		{l.DataDir, 0o755}, {l.ConfigDir, 0o755}, {p.AgentDir, 0o700}, {p.UpdateDir, 0o700},
		{p.AgentTLSDir, 0o700}, {p.BinaryDir, 0o755}, {p.ControllerDir, 0o700},
	} {
		wantMode(t, d.path, d.mode)
	}
	for _, path := range []string{l.DataDir, l.ConfigDir, p.AgentDir, p.UpdateDir, p.AgentTLSDir, p.BinaryDir, p.AgentKey} {
		if ids, ok := h.chowns.of(path); !ok || ids != [2]int{0, 0} {
			t.Errorf("%s owned by %v (recorded %v), want root", path, ids, ok)
		}
	}
	for _, path := range []string{p.ControllerDir, p.ControllerKey} {
		if ids, _ := h.chowns.of(path); ids != [2]int{990, 990} {
			t.Errorf("%s owned by %v, want the darkpanel user", path, ids)
		}
	}
	wantMode(t, p.AgentKey, 0o600)
	wantMode(t, p.ControllerKey, 0o600)
	if got, want := h.runner.commands(), []string{"/usr/sbin/useradd --system --no-create-home --shell /usr/sbin/nologin darkpanel"}; !reflect.DeepEqual(got, want) {
		t.Errorf("commands %v, want %v", got, want)
	}
	if got, want := manifest(t, l), wantManifest(l, RoleController); !reflect.DeepEqual(got, want) {
		t.Errorf("manifest\n got %v\nwant %v", got, want)
	}
	files, err := Units(l, RoleController, cfg.AgentListen, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if got := readBytes(t, f.Path); string(got) != string(f.Content) {
			t.Errorf("%s holds %q", f.Path, got)
		}
	}
	want := []string{
		"Reload", "Enable darkpanel-controller.service darkpanel-agent.service darkpanel-update-recover.service",
		"Restart darkpanel-controller.service", "Restart darkpanel-agent.service",
	}
	if got := h.systemd.recorded(); !reflect.DeepEqual(got, want) {
		t.Errorf("systemd calls\n got %v\nwant %v", got, want)
	}
	if h.controller.enrolls != 1 || !reflect.DeepEqual(h.agent.tokens, []string{"dpe1.local"}) {
		t.Errorf("enroll-local ran %d times, agent enrolled with %v; want once with dpe1.local", h.controller.enrolls, h.agent.tokens)
	}
	if !strings.Contains(h.out.String(), "sudo darkpanel user create --username admin") {
		t.Errorf("output does not say how to create the first admin:\n%s", h.out)
	}
}

func TestInstallRerunChangesNothing(t *testing.T) {
	for _, role := range []Role{RoleController, RoleAgent} {
		t.Run(string(role), func(t *testing.T) {
			h := newTestHost(t)
			cfg := h.config(role)
			if role == RoleAgent {
				cfg.Token = "dpe1.agent"
			}
			ctx := context.Background()
			if err := Install(ctx, cfg); err != nil {
				t.Fatalf("Install: %v", err)
			}
			key := readBytes(t, h.layout.Paths.AgentKey)
			before := manifest(t, h.layout)
			h.out.Reset()
			cfg.Token = ""
			if err := Install(ctx, cfg); err != nil {
				t.Fatalf("second Install: %v\n%s", err, h.out)
			}
			if string(readBytes(t, h.layout.Paths.AgentKey)) != string(key) {
				t.Error("the second install replaced the agent key")
			}
			if after := manifest(t, h.layout); !reflect.DeepEqual(after, before) {
				t.Errorf("manifest changed\nbefore %v\n after %v", before, after)
			}
			if len(h.agent.tokens) != 1 {
				t.Errorf("the agent enrolled with %v, want once", h.agent.tokens)
			}
			for _, line := range []string{"keys: 0 created", "units: 0 written", "enrollment: the saved credentials are valid"} {
				if !strings.Contains(h.out.String(), line) {
					t.Errorf("second run output lacks %q:\n%s", line, h.out)
				}
			}
			if role == RoleController && len(h.runner.commands()) != 1 {
				t.Errorf("useradd ran %d times, want once", len(h.runner.commands()))
			}
		})
	}
}

// TestInstallRerunAfterEachCheckpoint fails Install at each step, reruns it
// without the failure, and expects the state of an install that never
// failed.
func TestInstallRerunAfterEachCheckpoint(t *testing.T) {
	boom := errors.New("boom")
	const agentRestart = "Restart darkpanel-agent.service"
	tests := []struct {
		name   string
		step   string // the step the injected failure stops
		inject func(h *testHost)
		fix    func(h *testHost)
	}{
		{"useradd fails", "directories", func(h *testHost) { h.runner.exit = 1 }, func(h *testHost) { h.runner.exit = 0 }},
		{"chown fails", "directories", nil, nil},
		{"reload fails", "units", func(h *testHost) { h.systemd.failOn("Reload", boom) }, func(h *testHost) { h.systemd.failOn("Reload", nil) }},
		{"controller start fails", "controller", func(h *testHost) { h.systemd.failOn("Restart", boom) }, func(h *testHost) { h.systemd.failOn("Restart", nil) }},
		{"controller never healthy", "controller", func(h *testHost) { h.controller.unhealthy = 1 << 30 }, func(h *testHost) { h.controller.unhealthy = 0 }},
		{"enrollment fails", "enrollment", func(h *testHost) { h.agent.enrollErr = boom }, func(h *testHost) { h.agent.enrollErr = nil }},
		{"agent start fails", "agent", func(h *testHost) { h.systemd.failOn(agentRestart, boom) }, func(h *testHost) { h.systemd.failOn(agentRestart, nil) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newTestHost(t)
			cfg := h.config(RoleController)
			cfg.HealthTimeout = 20 * time.Millisecond
			ctx := context.Background()
			brk, fix := tt.inject, tt.fix
			if brk == nil {
				calls := 0
				cfg.Chown = func(path string, uid, gid int) error {
					if calls++; calls == 4 {
						return boom
					}
					return h.chowns.chown(path, uid, gid)
				}
				brk, fix = func(*testHost) {}, func(*testHost) {}
			}
			brk(h)
			err := Install(ctx, cfg)
			if err == nil {
				t.Fatal("Install succeeded with a failure injected")
			}
			if !strings.HasPrefix(err.Error(), "install: "+tt.step+": ") {
				t.Errorf("Install = %v, want a failure of the %s step", err, tt.step)
			}
			fix(h)
			cfg.Chown = h.chowns.chown
			if err := Install(ctx, cfg); err != nil {
				t.Fatalf("rerun: %v\n%s", err, h.out)
			}
			if got, want := manifest(t, h.layout), wantManifest(h.layout, RoleController); !reflect.DeepEqual(got, want) {
				t.Errorf("manifest\n got %v\nwant %v", got, want)
			}
			if _, err := os.Lstat(h.layout.Paths.AgentTLSCurrent()); err != nil {
				t.Errorf("no credentials after the rerun: %v", err)
			}
			if ids, _ := h.chowns.of(h.layout.Paths.ControllerKey); ids != [2]int{990, 990} {
				t.Errorf("controller key owned by %v", ids)
			}
			last := h.systemd.recorded()
			if last[len(last)-1] != "Restart darkpanel-agent.service" {
				t.Errorf("the rerun ended with %q, want the agent started", last[len(last)-1])
			}
		})
	}
}

func TestInstallAgentEnrollment(t *testing.T) {
	tokenInvalid := errcode.New(errcode.EnrollTokenInvalid, "invalid enrollment token")
	tests := []struct {
		name      string
		saved     bool
		check     CheckResult
		token     string
		enrollErr error
		wantErr   string
		wantOut   string
		tokens    []string
	}{
		{name: "fresh with a token", token: "dpe1.t", wantOut: "enrollment: enrolled", tokens: []string{"dpe1.t"}},
		{name: "fresh without a token", wantErr: "no saved credentials and no enrollment token"},
		{name: "valid credentials", saved: true, check: CheckAccepted, wantOut: "enrollment: the saved credentials are valid"},
		{name: "valid credentials and a token", saved: true, check: CheckAccepted, token: "dpe1.t", wantOut: "the enrollment token was not used"},
		{name: "refused without a token", saved: true, check: CheckRefused, wantErr: "rerun install.sh with --token-file"},
		{name: "refused with a token", saved: true, check: CheckRefused, token: "dpe1.t", wantOut: "enrollment: enrolled", tokens: []string{"dpe1.t"}},
		{name: "controller unreachable", saved: true, check: CheckUnreachable, token: "dpe1.t", wantOut: "the saved credentials are kept"},
		{name: "token refused", token: "dpe1.t", enrollErr: tokenInvalid, wantErr: "revoke the host and create a new token", tokens: []string{"dpe1.t"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newTestHost(t)
			if tt.saved {
				if err := h.agent.Enroll(context.Background(), "earlier"); err != nil {
					t.Fatal(err)
				}
				h.agent.tokens = nil
			}
			h.agent.check, h.agent.enrollErr = tt.check, tt.enrollErr
			cfg := h.config(RoleAgent)
			cfg.Token = tt.token
			err := Install(context.Background(), cfg)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Install = %v, want an error containing %q", err, tt.wantErr)
				}
			} else if err != nil {
				t.Fatalf("Install: %v", err)
			}
			if tt.wantOut != "" && !strings.Contains(h.out.String(), tt.wantOut) {
				t.Errorf("output lacks %q:\n%s", tt.wantOut, h.out)
			}
			if !reflect.DeepEqual(h.agent.tokens, tt.tokens) {
				t.Errorf("enrolled with %v, want %v", h.agent.tokens, tt.tokens)
			}
			if h.controller.enrolls != 0 || len(h.runner.commands()) != 0 {
				t.Error("the agent role used the controller or created a user")
			}
			if _, err := os.Lstat(h.layout.Paths.ControllerDir); !errors.Is(err, fs.ErrNotExist) {
				t.Error("the agent role created the controller directory")
			}
		})
	}
}

func TestInstallControllerReenrollsRefusedCredentials(t *testing.T) {
	for _, check := range []CheckResult{CheckRefused, CheckUnreachable} {
		h := newTestHost(t)
		if err := h.agent.Enroll(context.Background(), "earlier"); err != nil {
			t.Fatal(err)
		}
		h.agent.tokens, h.agent.check = nil, check
		if err := Install(context.Background(), h.config(RoleController)); err != nil {
			t.Fatal(err)
		}
		if h.controller.enrolls != 1 || !reflect.DeepEqual(h.agent.tokens, []string{"dpe1.local"}) {
			t.Errorf("check %d: enroll-local ran %d times, tokens %v", check, h.controller.enrolls, h.agent.tokens)
		}
	}
}

func TestInstallWaitsForTheController(t *testing.T) {
	h := newTestHost(t)
	h.controller.unhealthy = 3
	if err := Install(context.Background(), h.config(RoleController)); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if h.controller.health != 4 {
		t.Errorf("Healthy asked %d times, want 4", h.controller.health)
	}

	h = newTestHost(t)
	h.controller.unhealthy = 1 << 30
	cfg := h.config(RoleController)
	cfg.HealthTimeout = 30 * time.Millisecond
	err := Install(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "did not answer /healthz") {
		t.Fatalf("Install = %v, want the health wait to fail", err)
	}
	if len(h.agent.tokens) != 0 {
		t.Error("the agent enrolled although the controller never answered")
	}
}

func TestInstallResetsAFailedUnit(t *testing.T) {
	h := newTestHost(t)
	h.systemd.setState("darkpanel-agent.service", "failed")
	cfg := h.config(RoleAgent)
	cfg.Token = "dpe1.t"
	if err := Install(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	got := h.systemd.recorded()
	if want := []string{"ResetFailed darkpanel-agent.service", "Restart darkpanel-agent.service"}; !reflect.DeepEqual(got[len(got)-2:], want) {
		t.Errorf("systemd calls end %v, want %v", got[len(got)-2:], want)
	}
}

func TestInstallRepairsAKeyMode(t *testing.T) {
	h := newTestHost(t)
	ctx := context.Background()
	if err := Install(ctx, h.config(RoleController)); err != nil {
		t.Fatal(err)
	}
	p := h.layout.Paths
	keys := map[string]string{}
	for _, k := range []string{p.AgentKey, p.ControllerKey} {
		keys[k] = string(readBytes(t, k))
		if err := os.Chmod(k, 0o644); err != nil { //nolint:gosec // G302: the test loosens the key on purpose
			t.Fatal(err)
		}
	}
	h.out.Reset()
	if err := Install(ctx, h.config(RoleController)); err != nil {
		t.Fatalf("rerun: %v\n%s", err, h.out)
	}
	for k, content := range keys {
		wantMode(t, k, 0o600)
		if string(readBytes(t, k)) != content {
			t.Errorf("the rerun replaced %s", k)
		}
	}
	if !strings.Contains(h.out.String(), "keys: 0 created, 2 kept") {
		t.Errorf("rerun output lacks the kept keys:\n%s", h.out)
	}
}

func TestInstallRefusals(t *testing.T) {
	h := newTestHost(t)
	tests := map[string]func(c *Config){
		"unknown role":             func(c *Config) { c.Role = "both" },
		"token for the controller": func(c *Config) { c.Token = "dpe1.t" },
		"listen for an agent":      func(c *Config) { c.Role = RoleAgent; c.AgentListen = []string{"10.0.0.1:7443"} },
		"bad listen address":       func(c *Config) { c.AgentListen = []string{"10.0.0.1"} },
		"bad advertise address":    func(c *Config) { c.AgentAdvertise = "h:1 --dev" },
		"no controller":            func(c *Config) { c.Controller = nil },
		"no systemd":               func(c *Config) { c.Systemd = nil },
	}
	for name, change := range tests {
		cfg := h.config(RoleController)
		change(&cfg)
		if err := Install(context.Background(), cfg); err == nil {
			t.Errorf("%s: Install succeeded", name)
		}
	}
	if _, err := os.Lstat(h.layout.DataDir); !errors.Is(err, fs.ErrNotExist) {
		t.Error("a refused install created directories")
	}

	// A directory that is a symlink is refused rather than followed.
	if err := os.MkdirAll(filepath.Dir(h.layout.Paths.AgentDir), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), h.layout.Paths.AgentDir); err != nil {
		t.Fatal(err)
	}
	if err := Install(context.Background(), h.config(RoleController)); err == nil || !strings.Contains(err.Error(), "is not a directory") {
		t.Errorf("Install over a symlinked directory = %v", err)
	}
}
