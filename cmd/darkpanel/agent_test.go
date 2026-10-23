package main

import (
	"bytes"
	"context"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/agent/daemon"
	"github.com/darkraise/darkpanel/internal/cli"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/install"
	"github.com/darkraise/darkpanel/internal/paths"
	"github.com/darkraise/darkpanel/internal/platform"
	"github.com/darkraise/darkpanel/internal/transport"
	"github.com/darkraise/darkpanel/internal/update"
)

// fakeSystemd is a systemd provider that fails the test when the code under
// test manages a unit; it only counts Close.
type fakeSystemd struct {
	platform.Systemd
	closed int
}

func (f *fakeSystemd) Close() error {
	f.closed++
	return nil
}

// agentSeams replaces the agent commands' seams for one test: geteuid
// answers euid, the production paths are a rooted layout under a temporary
// directory, and the daemon calls are recorded instead of run.
type agentSeams struct {
	layout  install.Layout
	dev     paths.Paths
	systemd *fakeSystemd
	// systemdCalls counts the connections to the systemd provider.
	systemdCalls int
	runs         []daemon.Config
	updates      []update.Config
	// statusErr is core.update.status run on the registry the daemon got.
	statusErr error
	enrolls   []string // "<dir> <token>"
	checkRes  install.CheckResult
	checkErr  error
}

func fakeAgent(t *testing.T, euid int) *agentSeams {
	t.Helper()
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	t.Setenv("XDG_RUNTIME_DIR", "")
	s := &agentSeams{
		layout:  install.Rooted(t.TempDir()),
		dev:     paths.Under(filepath.Join(state, "darkpanel-dev")),
		systemd: &fakeSystemd{},
	}
	oldEUID, oldRun, oldEnroll, oldCheck := geteuid, daemonRun, daemonEnroll, daemonCheck
	oldSystemd, oldUpdater, oldResolve, oldLayout := newSystemd, newUpdater, resolvePaths, installLayout
	t.Cleanup(func() {
		geteuid, daemonRun, daemonEnroll, daemonCheck = oldEUID, oldRun, oldEnroll, oldCheck
		newSystemd, newUpdater, resolvePaths, installLayout = oldSystemd, oldUpdater, oldResolve, oldLayout
	})
	withoutHarness(t)
	geteuid = func() int { return euid }
	installLayout = func() install.Layout { return s.layout }
	resolvePaths = func(dev bool) (paths.Paths, error) {
		if dev {
			return paths.Resolve(true)
		}
		return s.layout.Paths, nil
	}
	newSystemd = func(context.Context) (systemdConn, error) {
		s.systemdCalls++
		return s.systemd, nil
	}
	newUpdater = func(cfg update.Config) (*update.Updater, error) {
		s.updates = append(s.updates, cfg)
		return update.New(cfg)
	}
	daemonRun = func(ctx context.Context, cfg daemon.Config) error {
		s.runs = append(s.runs, cfg)
		def, ok := cfg.Runtime.Registry.Action(core.ActionRef{Name: update.ActionStatus, Version: 1})
		if !ok {
			t.Errorf("the agent registry has no %s", update.ActionStatus)
			return nil
		}
		_, s.statusErr = def.Handler.Read(ctx, core.Env{}, json.RawMessage(`{}`))
		return nil
	}
	daemonEnroll = func(_ context.Context, dir, token string) (*daemon.Credentials, error) {
		s.enrolls = append(s.enrolls, dir+" "+token)
		uri, err := url.Parse(transport.HostURI("01HZY5S4ZK8V3J1M2N3P4Q5R6S"))
		if err != nil {
			t.Fatal(err)
		}
		return &daemon.Credentials{Cert: &x509.Certificate{URIs: []*url.URL{uri}}}, nil
	}
	daemonCheck = func(context.Context, string) (install.CheckResult, error) { return s.checkRes, s.checkErr }
	return s
}

func runStdin(stdin io.Reader, args ...string) (stdout, stderr string, code int) {
	var out, errOut bytes.Buffer
	code = run(args, cli.Options{Stdin: stdin, Stdout: &out, Stderr: &errOut})
	return out.String(), errOut.String(), code
}

func TestAgentRefusals(t *testing.T) {
	s := fakeAgent(t, 1000)
	tests := []struct {
		args []string
		code int
		out  string
	}{
		{[]string{"agent", "run"}, cli.ExitUsage, "agent run runs as root in production mode; run it with sudo, or add --dev"},
		{[]string{"agent", "enroll"}, cli.ExitUsage, "agent enroll runs as root in production mode; run it with sudo, or add --dev"},
		{[]string{"agent", "run", "extra"}, cli.ExitUsage, `unknown command "extra"`},
		{[]string{"agent", "nope"}, cli.ExitUsage, `unknown command "nope"`},
		{[]string{"agent", "task", "01J", "apply", "1"}, cli.ExitUsage, "agent task runs as root in production mode; run it with sudo, or add --dev"},
		{[]string{"agent", "task", "--dev", "01J", "fix", "1"}, cli.ExitUsage, `op "fix"`},
		{[]string{"agent", "task", "--dev", "01J", "apply", "0"}, cli.ExitUsage, `attempt "0"`},
		{[]string{"agent", "task", "--dev", "01J", "apply", "x"}, cli.ExitUsage, `attempt "x"`},
		{[]string{"agent", "task", "--dev", "01J", "apply"}, cli.ExitUsage, "accepts 3 arg(s), received 2"},
	}
	for _, tt := range tests {
		out, code := runArgs(tt.args...)
		if code != tt.code || !strings.Contains(out, tt.out) {
			t.Errorf("darkpanel %s: exit %d, output %q; want exit %d and %q", strings.Join(tt.args, " "), code, out, tt.code, tt.out)
		}
	}
	if len(s.runs) != 0 || len(s.enrolls) != 0 {
		t.Errorf("refused commands ran the daemon %d times and enrolled %d times", len(s.runs), len(s.enrolls))
	}
}

func TestAgentRunDev(t *testing.T) {
	s := fakeAgent(t, 1000)
	if out, code := runArgs("agent", "run", "--dev"); code != cli.ExitOK {
		t.Fatalf("agent run --dev: exit %d: %s", code, out)
	}
	if len(s.runs) != 1 || len(s.updates) != 1 {
		t.Fatalf("agent run --dev ran the daemon %d times over %d updaters, want once", len(s.runs), len(s.updates))
	}
	cfg, upd := s.runs[0], s.updates[0]
	if cfg.Paths != s.dev || cfg.Runtime.DB == nil || cfg.Runtime.Sealer == nil || cfg.Runtime.Registry == nil ||
		cfg.Runtime.BackupDir != s.dev.AgentBackups() || cfg.Runtime.Exec == nil || cfg.Updater == nil {
		t.Errorf("agent run --dev passed %+v, want the dev paths and an opened runtime", cfg)
	}
	if cfg.NeverConnected {
		t.Error("agent run set NeverConnected outside a harnessbroken build")
	}
	if cfg.AllowPeer == nil || !cfg.AllowPeer(uint32(os.Getuid())) || !cfg.AllowPeer(0) || cfg.AllowPeer(uint32(os.Getuid())+4242) { //nolint:gosec // G115: a uid fits in 32 bits
		t.Error("agent run --dev must admit root and the invoking user on the agent socket, and no one else")
	}
	if s.statusErr != nil {
		t.Errorf("core.update.status on the agent registry: %v, want it to run on the agent's updater", s.statusErr)
	}
	if cfg.Runtime.Units != nil {
		t.Errorf("agent run --dev passed units %v, want none, so the engine refuses task actions", cfg.Runtime.Units)
	}
	if upd.Paths != s.dev || upd.HTTP != nil || upd.Index == nil ||
		upd.Probes.Agent == nil || upd.Probes.Controller != nil || upd.NewerAgents != nil {
		t.Errorf("the dev updater config is %+v, want the dev paths, no HTTP client, an index, only the agent probe", upd)
	}
	// A development agent never reaches the host's systemd: its updater's
	// provider refuses every unit operation.
	if s.systemdCalls != 0 {
		t.Errorf("agent run --dev connected to systemd %d times, want never", s.systemdCalls)
	}
	ctx := context.Background()
	if err := upd.Systemd.Restart(ctx, update.AgentUnit); !errors.Is(err, errDevSystemd) {
		t.Errorf("the dev updater's Restart = %v, want %v", err, errDevSystemd)
	}
	if _, err := upd.Systemd.State(ctx, update.ControllerUnit); !errors.Is(err, errDevSystemd) {
		t.Errorf("the dev updater's State = %v, want %v", err, errDevSystemd)
	}
	for _, d := range []string{s.dev.AgentDir, s.dev.AgentTLSDir, s.dev.UpdateDir, filepath.Dir(s.dev.AgentSocket)} {
		if st, err := os.Stat(d); err != nil || !st.IsDir() || st.Mode().Perm() != 0o700 {
			t.Errorf("agent run --dev left %s as %v, %v; want a directory with mode 0700", d, st, err)
		}
	}
	if s.systemd.closed != 0 {
		t.Errorf("the systemd seam closed %d times, want never", s.systemd.closed)
	}
}

// taskAgentDB migrates the development agent's database and seeds change
// 01JTASK of action tsk.pkg.install, which this binary does not register,
// with apply attempt 1 starting and attempt 2 fenced.
func taskAgentDB(t *testing.T, p paths.Paths) {
	t.Helper()
	ctx := context.Background()
	if err := os.MkdirAll(filepath.Dir(p.AgentDB()), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(ctx, p.AgentDB())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	set, err := store.CoreMigrations(store.SideAgent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Migrate(ctx, db, store.MigrateOptions{
		Side: store.SideAgent, BinaryVersion: "0.3.0", BackupRoot: p.AgentBackups(), Keep: 1, Now: time.Now(),
	}, set, nil); err != nil {
		t.Fatal(err)
	}
	const at = "2026-10-22T12:00:00.000000000Z"
	for _, q := range []string{
		`INSERT INTO core_plans (id, host_id, action, version, actor, input_hash, state_hash, diff_json, guards_json,
			confirmation, created_at, expires_at)
			VALUES ('01JPLAN', 'h1', 'tsk.pkg.install', 1, 'user:alice', '', '', '[]', '[]', '', '` + at + `', '` + at + `')`,
		`INSERT INTO core_changes (id, plan_id, host_id, action, version, class, compensated_by, actor, state,
			input_json, recovery_owner, created_at, updated_at)
			VALUES ('01JTASK', '01JPLAN', 'h1', 'tsk.pkg.install', 1, 'compensable', '', 'user:alice', 'applying',
			'{}', 'engine', '` + at + `', '` + at + `')`,
		`INSERT INTO core_task_runs (change_id, op, attempt, state, started_at)
			VALUES ('01JTASK', 'apply', 1, 'starting', '` + at + `'), ('01JTASK', 'apply', 2, 'cancelled', '` + at + `')`,
	} {
		if _, err := db.Writer().ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAgentTask(t *testing.T) {
	s := fakeAgent(t, 1000)
	taskAgentDB(t, s.dev)
	row := func(attempt int) (state, code string, pid sql.NullInt64) {
		t.Helper()
		db, err := store.Open(context.Background(), s.dev.AgentDB())
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = db.Close() }()
		if err := db.Read().QueryRow(
			"SELECT state, error_code, pid FROM core_task_runs WHERE change_id = '01JTASK' AND op = 'apply' AND attempt = ?",
			attempt).Scan(&state, &code, &pid); err != nil {
			t.Fatal(err)
		}
		return state, code, pid
	}

	if out, code := runArgs("agent", "task", "--dev", "01JTASK", "apply", "1"); code != cli.ExitOK {
		t.Fatalf("agent task on a starting row: exit %d: %s", code, out)
	}
	if state, code, pid := row(1); state != "failed" || code != string(errcode.ActionUnknown) || !pid.Valid {
		t.Errorf("attempt 1 = %s with code %q and pid %v, want claimed (a pid) and failed with %s",
			state, code, pid, errcode.ActionUnknown)
	}

	if out, code := runArgs("agent", "task", "--dev", "01JTASK", "apply", "2"); code != cli.ExitOK {
		t.Fatalf("agent task on a fenced row: exit %d: %s", code, out)
	}
	if state, code, pid := row(2); state != "cancelled" || code != "" || pid.Valid {
		t.Errorf("fenced attempt 2 = %s with code %q and pid %v, want it left cancelled", state, code, pid)
	}
	if len(s.runs) != 0 {
		t.Errorf("agent task ran the daemon %d times, want never", len(s.runs))
	}
}

func TestAgentTaskScopesUnits(t *testing.T) {
	s := fakeAgent(t, 0)
	taskAgentDB(t, s.layout.Paths)
	var cfgs []lifecycle.Config
	old := newTaskEngine
	t.Cleanup(func() { newTaskEngine = old })
	newTaskEngine = func(cfg lifecycle.Config) (*lifecycle.Engine, error) {
		cfgs = append(cfgs, cfg)
		return lifecycle.New(cfg)
	}
	if out, code := runArgs("agent", "task", "01JTASK", "apply", "1"); code != cli.ExitOK {
		t.Fatalf("agent task: exit %d: %s", code, out)
	}
	if len(cfgs) != 1 || cfgs[0].ModuleUnits == nil {
		t.Fatalf("agent task built %d engines, want one with ModuleUnits", len(cfgs))
	}
	units := cfgs[0].ModuleUnits
	if got := units(core.ModuleIDCore); got != core.Units(s.systemd) {
		t.Errorf("the core module's units are %T, want the systemd connection", got)
	}
	// The systemd seam embeds a nil client, so a call that reached it would
	// panic; the hostinfo module's client refuses before that.
	err := units("hostinfo").Start(context.Background(), "nginx.service")
	if err == nil || err.Error() != "platform: module hostinfo may not Start unit nginx.service" {
		t.Errorf("hostinfo's Start of nginx.service = %v, want the scoped client's refusal", err)
	}
}

func TestAgentRunProduction(t *testing.T) {
	for _, withController := range []bool{false, true} {
		s := fakeAgent(t, 0)
		if withController {
			unit := s.layout.UnitPath(update.ControllerUnit)
			if err := os.MkdirAll(filepath.Dir(unit), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(unit, []byte("[Unit]\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if out, code := runArgs("agent", "run"); code != cli.ExitOK {
			t.Fatalf("agent run as root: exit %d: %s", code, out)
		}
		cfg, upd := s.runs[0], s.updates[0]
		if s.systemdCalls != 1 || upd.Systemd != s.systemd || s.systemd.closed != 1 {
			t.Errorf("agent run connected to systemd %d times and closed it %d times, want the systemd seam once", s.systemdCalls, s.systemd.closed)
		}
		if cfg.Paths != s.layout.Paths || cfg.AllowPeer != nil {
			t.Errorf("agent run passed paths %+v and AllowPeer set %v; want the installed paths and root alone", cfg.Paths, cfg.AllowPeer != nil)
		}
		if got := upd.Probes.Controller != nil; got != withController {
			t.Errorf("controller unit installed %v: the updater probes the controller %v", withController, got)
		}
		if cfg.Runtime.Units != core.Units(s.systemd) || cfg.Runtime.TaskBinary != s.layout.Paths.Binary() {
			t.Errorf("agent run passed units %v and task binary %q, want the systemd connection and %s",
				cfg.Runtime.Units, cfg.Runtime.TaskBinary, s.layout.Paths.Binary())
		}
	}
}

func TestAgentEnrollToken(t *testing.T) {
	dir := t.TempDir()
	writeToken := func(name string, mode os.FileMode) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("  file-token\n"), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
		return path
	}
	private, shared := writeToken("private", 0o600), writeToken("shared", 0o644)
	tests := []struct {
		name     string
		args     []string
		stdin    string
		terminal bool
		code     int
		token    string // "" when the command must not enroll
		stderr   string
	}{
		{"token file", []string{"--token-file", private}, "", false, cli.ExitOK, "file-token", ""},
		{"token file readable by others", []string{"--token-file", shared}, "", false, cli.ExitError, "", "has mode 0644; want 0600 or stricter"},
		{"standard input", nil, "\n  piped-token \n", false, cli.ExitOK, "piped-token", ""},
		{"empty standard input", nil, " \n", false, cli.ExitError, "", "no enrollment token on standard input"},
		{"standard input too large", nil, strings.Repeat("x", maxStdinToken+1), false, cli.ExitError, "", "larger than an enrollment token"},
		{"terminal prompt", nil, "", true, cli.ExitOK, "typed-token", "Enrollment token: \n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := fakeAgent(t, 1000)
			oldTerm, oldRead := stdinTerminal, readHidden
			t.Cleanup(func() { stdinTerminal, readHidden = oldTerm, oldRead })
			stdinTerminal = func(io.Reader) bool { return tt.terminal }
			readHidden = func(io.Reader) (string, error) { return " typed-token ", nil }
			args := append([]string{"agent", "enroll", "--dev"}, tt.args...)
			stdout, stderr, code := runStdin(strings.NewReader(tt.stdin), args...)
			if code != tt.code || !strings.Contains(stderr, tt.stderr) {
				t.Fatalf("exit %d, stderr %q; want exit %d and %q", code, stderr, tt.code, tt.stderr)
			}
			if tt.token == "" {
				if len(s.enrolls) != 0 {
					t.Errorf("enrolled %v, want no enrollment", s.enrolls)
				}
				return
			}
			if want := []string{s.dev.AgentTLSDir + " " + tt.token}; len(s.enrolls) != 1 || s.enrolls[0] != want[0] {
				t.Errorf("enrolled %q, want %q", s.enrolls, want)
			}
			want := "enrolled as host 01HZY5S4ZK8V3J1M2N3P4Q5R6S\n" +
				"A running agent uses the new credentials from its next connection attempt; it needs no restart.\n"
			if stdout != want {
				t.Errorf("stdout %q, want %q", stdout, want)
			}
		})
	}
}

func TestAgentCheckExit(t *testing.T) {
	tests := []struct {
		res    install.CheckResult
		err    error
		code   int
		stdout string
		stderr string
	}{
		{install.CheckAccepted, nil, 0, "accepted: the controller accepted this agent's credentials\n", ""},
		{install.CheckRefused, errors.New("daemon: the controller refused this agent's credentials"), 1,
			"refused: the controller refused this agent's credentials, or there are none; enroll again with a new token\n",
			"darkpanel: daemon: the controller refused this agent's credentials\n"},
		{install.CheckUnreachable, errors.New("dial tcp 192.0.2.1:7443: i/o timeout"), 2,
			"unreachable: no handshake with the controller completed\n", "darkpanel: dial tcp 192.0.2.1:7443: i/o timeout\n"},
	}
	for _, tt := range tests {
		s := fakeAgent(t, 1000)
		s.checkRes, s.checkErr = tt.res, tt.err
		stdout, stderr, code := runStdin(strings.NewReader(""), "agent", "check", "--dev")
		if code != tt.code || stdout != tt.stdout || stderr != tt.stderr {
			t.Errorf("check result %d: exit %d, stdout %q, stderr %q; want exit %d, %q and %q",
				tt.res, code, stdout, stderr, tt.code, tt.stdout, tt.stderr)
		}
	}
	out, code := runArgs("agent", "check", "--help")
	if code != cli.ExitOK || !strings.Contains(out, "replaces a running agent's session for a moment") {
		t.Errorf("agent check --help: exit %d, output %q; want the warning about the running agent's session", code, out)
	}
	refusals := []struct {
		euid int
		args []string
		out  string
	}{
		{1000, []string{"agent", "check"}, "agent check runs as root in production mode; run it with sudo, or add --dev"},
		{1000, []string{"agent", "check", "--dev", "extra"}, `unknown command "extra"`},
		{0, []string{"agent", "check", "--nope"}, "unknown flag: --nope"},
		{0, []string{"agent", "check", "--dev", "--local"}, "--local reaches only this host's agent"},
	}
	for _, tt := range refusals {
		s := fakeAgent(t, tt.euid)
		s.checkRes = install.CheckAccepted
		stdout, stderr, code := runStdin(strings.NewReader(""), tt.args...)
		if code != int(install.CheckFailed) || stdout != "" || !strings.Contains(stderr, tt.out) {
			t.Errorf("darkpanel %s: exit %d, stdout %q, stderr %q; want exit %d, no outcome and %q",
				strings.Join(tt.args, " "), code, stdout, stderr, install.CheckFailed, tt.out)
		}
	}
	s := fakeAgent(t, 0)
	s.checkRes = install.CheckAccepted
	resolvePaths = func(bool) (paths.Paths, error) { return paths.Paths{}, errors.New("no state directory") }
	if stdout, stderr, code := runStdin(strings.NewReader(""), "agent", "check"); code != int(install.CheckFailed) ||
		stdout != "" || !strings.Contains(stderr, "no state directory") {
		t.Errorf("agent check with unresolvable paths: exit %d, stdout %q, stderr %q; want exit %d and the error",
			code, stdout, stderr, install.CheckFailed)
	}
	if out, _ := runArgs("agent", "check", "--help"); !strings.Contains(out, "3 the check could not run") {
		t.Errorf("agent check --help = %q, want exit status 3 documented", out)
	}
}
