package server

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/controller/listen"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/paths"
)

// t0 is the fake clock's start.
var t0 = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

// testHostname is the hostname the tests report.
const testHostname = "test-host"

// testParams are cheap argon2id parameters; production uses
// auth.DefaultParams.
var testParams = auth.Params{Memory: 8, Iterations: 1, Parallelism: 1, SaltLen: 16, KeyLen: 32}

// localBind maps every endpoint to 127.0.0.1 port 0, because listen values
// cannot name port 0.
func localBind(ctx context.Context, _ listen.Endpoint) (net.Listener, error) {
	var lc net.ListenConfig
	return lc.Listen(ctx, "tcp4", "127.0.0.1:0")
}

// noTailscale reports no Tailscale interface, so the agent listener binds
// only its loopback endpoint and the extra ones.
func noTailscale(string) ([]netip.Addr, bool, error) { return nil, false, nil }

// testOptions are options over temporary paths, with the socket in a short
// directory of its own (Unix socket paths are limited to 108 bytes). They
// start the controller alone, as production does.
func testOptions(t *testing.T) Options {
	t.Helper()
	run, err := os.MkdirTemp("", "dp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(run) })
	p := paths.Under(t.TempDir())
	p.ControllerSocket = filepath.Join(run, "controller.sock")
	specs, err := listen.ParseSpecs([]string{"loopback"})
	if err != nil {
		t.Fatal(err)
	}
	return Options{
		Paths:      p,
		Listen:     specs,
		Dev:        true,
		Clock:      coretest.NewFakeClock(t0),
		Hostname:   func() (string, error) { return testHostname, nil },
		Bind:       localBind,
		AgentBind:  localBind,
		Interfaces: noTailscale,
		AuthParams: testParams,
	}
}

// embeddedOptions are testOptions with the in-process agent of
// --embedded-agent, for the tests that need the local host's agent.
func embeddedOptions(t *testing.T) Options {
	t.Helper()
	opts := testOptions(t)
	opts.EmbeddedAgent = true
	return opts
}

// start starts a server and shuts it down when the test ends.
func start(t *testing.T, opts Options) *Server {
	t.Helper()
	s, err := Start(context.Background(), opts)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Shutdown(context.Background()); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
	})
	return s
}

// openDB opens a database file of a stopped server.
func openDB(t *testing.T, path string) *store.DB {
	t.Helper()
	db, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func wantMode(t *testing.T, path string, want fs.FileMode) {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Mode().Perm(); got != want {
		t.Errorf("%s has mode %v, want %v", path, got, want)
	}
}

func TestStartPreparesStorage(t *testing.T) {
	opts := embeddedOptions(t)
	p := opts.Paths
	s, err := Start(context.Background(), opts)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	id := s.LocalHostID()
	if got := s.AgentListeners(); len(got) != 0 {
		t.Errorf("the embedded agent's serve binds the agent listener %v, want none", got)
	}
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	for _, dir := range []string{p.ControllerDir, p.AgentDir, filepath.Dir(p.ControllerKey), filepath.Dir(p.ControllerSocket)} {
		wantMode(t, dir, 0o700)
	}
	wantMode(t, p.ControllerKey, 0o600)
	wantMode(t, p.AgentKey, 0o600)
	wantMode(t, filepath.Join(filepath.Dir(p.ControllerSocket), LockName), 0o600)
	backups, err := os.ReadDir(p.ControllerBackups())
	if err != nil || len(backups) != 1 || !strings.HasPrefix(backups[0].Name(), "none-dev-") {
		t.Errorf("controller backups = %v (%v), want one none-dev-<time> backup", backups, err)
	}

	db := openDB(t, p.ControllerDB())
	ctx := context.Background()
	var name, metaID string
	var local int
	if err := db.Read().QueryRowContext(ctx, `SELECT name, is_local FROM core_hosts WHERE id = ?`, id).Scan(&name, &local); err != nil {
		t.Fatalf("local host %q: %v", id, err)
	}
	if err := db.Read().QueryRowContext(ctx, `SELECT value FROM core_meta WHERE key = 'local_host_id'`).Scan(&metaID); err != nil {
		t.Fatal(err)
	}
	if name != testHostname || local != 1 || metaID != id || len(id) != 26 {
		t.Errorf("local host %q named %q, is_local %d, core_meta local_host_id %q; want a ULID named %s, local, in core_meta",
			id, name, local, metaID, testHostname)
	}
	if v, err := store.SchemaVersion(ctx, db, store.SideController); err != nil || v < 4 {
		t.Errorf("controller schema version %d (%v), want the core migrations applied", v, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	again := start(t, opts)
	if again.LocalHostID() != id {
		t.Errorf("restart: local host id %q, want %q", again.LocalHostID(), id)
	}
	var hosts int
	if err := again.controllerDB.Read().QueryRowContext(ctx, `SELECT count(*) FROM core_hosts`).Scan(&hosts); err != nil || hosts != 1 {
		t.Errorf("restart: %d hosts (%v), want 1", hosts, err)
	}
}

func TestLocalHostNameFallsBack(t *testing.T) {
	opts := testOptions(t)
	opts.Hostname = func() (string, error) { return "", errors.New("no hostname") }
	s := start(t, opts)
	var name string
	if err := s.controllerDB.Read().QueryRowContext(context.Background(),
		`SELECT name FROM core_hosts WHERE id = ?`, s.LocalHostID()).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "localhost" {
		t.Errorf("local host name %q, want localhost when the hostname is unknown", name)
	}
}

// TestOneServerAtATime is register row 76: a second serve on the same paths
// fails on the lock before it touches the databases or the socket.
func TestOneServerAtATime(t *testing.T) {
	opts := testOptions(t)
	first, err := Start(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Start(context.Background(), opts)
	if err == nil || !strings.Contains(err.Error(), "another darkpanel serve holds") {
		t.Fatalf("second Start: %v, want the lock refusal", err)
	}
	// Another socket directory over the same databases is refused too.
	other := opts
	other.Paths.ControllerSocket = testOptions(t).Paths.ControllerSocket
	_, err = Start(context.Background(), other)
	if err == nil || !strings.Contains(err.Error(), "another darkpanel serve holds") {
		t.Fatalf("Start with another socket directory: %v, want the lock refusal", err)
	}
	if err := first.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	start(t, opts)
}

// TestSocketDirectoryMustBePrivate is register row 69's obligation 4.
func TestSocketDirectoryMustBePrivate(t *testing.T) {
	for _, mode := range []fs.FileMode{0o720, 0o702, 0o777} {
		opts := testOptions(t)
		dir := filepath.Dir(opts.Paths.ControllerSocket)
		if err := os.Chmod(dir, mode); err != nil {
			t.Fatal(err)
		}
		_, err := Start(context.Background(), opts)
		if err == nil || !strings.Contains(err.Error(), "writable by group or other") {
			t.Errorf("socket directory mode %v: Start = %v, want a refusal", mode, err)
		}
		if _, err := os.Stat(filepath.Join(dir, LockName)); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("socket directory mode %v: the lock file was created (%v)", mode, err)
		}
	}
	if os.Geteuid() != 0 {
		t.Log("not root: the directory owned by another user is not checked")
		return
	}
	opts := testOptions(t)
	if err := os.Chown(filepath.Dir(opts.Paths.ControllerSocket), 65534, 65534); err != nil {
		t.Fatal(err)
	}
	if _, err := Start(context.Background(), opts); err == nil || !strings.Contains(err.Error(), "is owned by uid 65534") {
		t.Errorf("socket directory of another user: Start = %v, want a refusal", err)
	}
}

// TestFailedStartUndoesItself shows that a start that fails part-way closes
// what it opened and releases the lock.
func TestFailedStartUndoesItself(t *testing.T) {
	opts := testOptions(t)
	if err := os.MkdirAll(opts.Paths.ControllerDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(opts.Paths.ControllerDB(), []byte("not a database, not at all"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Start(context.Background(), opts); err == nil {
		t.Fatal("Start succeeded on a corrupt controller database")
	}
	if err := os.Remove(opts.Paths.ControllerDB()); err != nil {
		t.Fatal(err)
	}
	start(t, opts)
}

// TestShutdownOnce is register row 74: concurrent Shutdown calls run the
// shutdown once, and every caller gets its result after it finished.
func TestShutdownOnce(t *testing.T) {
	s, err := Start(context.Background(), testOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range errs {
		wg.Go(func() { errs[i] = s.Shutdown(context.Background()) })
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("Shutdown %d: %v", i, err)
		}
	}
	if err := s.controllerDB.Read().PingContext(context.Background()); err == nil {
		t.Error("the controller database is still open after Shutdown")
	}
}

// TestShutdownOutOfTimeKeepsTheLock: a step that outlives Shutdown's context
// may leave a request running, so no other serve may start in this process's
// lifetime.
func TestShutdownOutOfTimeKeepsTheLock(t *testing.T) {
	opts := testOptions(t)
	s, err := Start(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	s.onShutdown("stuck requests", func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := s.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown = %v, want the step's deadline error", err)
	}
	if err := s.controllerDB.Read().PingContext(context.Background()); err == nil {
		t.Error("the controller database is still open after Shutdown")
	}
	_, err = Start(context.Background(), opts)
	if err == nil || !strings.Contains(err.Error(), "another darkpanel serve holds") {
		t.Fatalf("Start after a Shutdown out of time: %v, want the lock refusal", err)
	}
}

func TestStartNeedsPaths(t *testing.T) {
	if _, err := Start(context.Background(), Options{}); err == nil || !strings.Contains(err.Error(), "Options.Paths") {
		t.Errorf("Start without paths: %v", err)
	}
}

// lockedBuffer is a log destination the server's goroutines may share.
type lockedBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// TestStartLogsTheControllerBackup is register row 87: the controller
// migration backup is logged as the agent runtime logs its own.
func TestStartLogsTheControllerBackup(t *testing.T) {
	opts := testOptions(t)
	var logs lockedBuffer
	opts.Log = slog.New(slog.NewTextHandler(&logs, nil))
	start(t, opts)
	out := logs.String()
	if !strings.Contains(out, `msg="server: controller database backed up before migrating"`) ||
		!strings.Contains(out, "dir="+opts.Paths.ControllerBackups()+string(filepath.Separator)) {
		t.Errorf("service log lacks the controller backup naming a directory under %s:\n%s", opts.Paths.ControllerBackups(), out)
	}
}

// TestControllerOnly is serve without the embedded agent: no agent state on
// disk, the agent listener bound and logged, every host offline until its
// agent connects, and nothing serving agents after Shutdown.
func TestControllerOnly(t *testing.T) {
	opts := testOptions(t)
	extra := netip.MustParseAddrPort("192.0.2.1:7444")
	opts.AgentListen = []netip.AddrPort{extra}
	var logs lockedBuffer
	opts.Log = slog.New(slog.NewTextHandler(&logs, nil))
	p := opts.Paths
	s, err := Start(context.Background(), opts)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	for _, path := range []string{p.AgentDir, p.AgentKey, p.AgentDB()} {
		if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s: %v, want no agent state from a controller-only serve", path, err)
		}
	}
	for _, dir := range []string{filepath.Dir(p.ControllerSocket), p.ControllerDir} {
		wantMode(t, filepath.Join(dir, LockName), 0o600)
	}

	bound := s.AgentListeners()
	var endpoints []netip.AddrPort
	for _, b := range bound {
		endpoints = append(endpoints, b.Endpoint.Addr)
		if !b.Endpoint.TLS || !strings.HasPrefix(b.Addr.String(), "127.0.0.1:") {
			t.Errorf("agent listener %+v, want TLS bound by AgentBind on 127.0.0.1", b)
		}
		if !strings.Contains(logs.String(), b.Addr.String()) {
			t.Errorf("the service log does not name the agent listener's address %s:\n%s", b.Addr, logs.String())
		}
	}
	if want := []netip.AddrPort{netip.MustParseAddrPort("127.0.0.1:7443"), extra}; !slices.Equal(endpoints, want) {
		t.Errorf("agent listener endpoints %v, want %v", endpoints, want)
	}

	for _, name := range []string{"core.update.apply", "core.update.rollout"} {
		if _, ok := s.Registry().Action(core.ActionRef{Name: name, Version: 1}); !ok {
			t.Errorf("the core module has no %s", name)
		}
	}
	id := s.LocalHostID()
	if _, err := s.dispatcher.Resource(context.Background(), root(id), id, "hostinfo.summary"); errcode.From(err).Code != errcode.HostOffline {
		t.Errorf("hostinfo.summary of the local host without its agent: %v, want host.offline", err)
	}

	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	for _, b := range bound {
		var d net.Dialer
		if c, err := d.DialContext(context.Background(), "tcp", b.Addr.String()); err == nil {
			_ = c.Close()
			t.Errorf("the agent listener %s still accepts connections after Shutdown", b.Addr)
		}
	}
	if got := s.AgentListeners(); len(got) != 0 {
		t.Errorf("agent listeners after Shutdown: %v, want none", got)
	}
}

// TestEnrollmentTokensCarryTheAdvertisedAddress shows the agent authority
// and --agent-advertise wired into the host actions in both modes: a host
// created without a controller address gets a token for the advertised one.
func TestEnrollmentTokensCarryTheAdvertisedAddress(t *testing.T) {
	for name, opts := range map[string]Options{"controller only": testOptions(t), "embedded agent": embeddedOptions(t)} {
		t.Run(name, func(t *testing.T) {
			opts.AgentAdvertise = "panel.example:7443"
			s := start(t, opts)
			v, err := s.dispatcher.Plan(context.Background(), root(s.LocalHostID()), "",
				core.ActionRef{Name: "core.host.create", Version: 1}, json.RawMessage(`{"name":"web1"}`))
			if err != nil {
				t.Fatalf("plan core.host.create: %v", err)
			}
			if !slices.ContainsFunc(v.Diff, func(l core.DiffLine) bool { return strings.Contains(l.Text, "token for panel.example:7443") }) {
				t.Errorf("plan diff %+v, want a token for the advertised address", v.Diff)
			}
		})
	}
}

// TestHealthStatusPassesThrough shows Options.HealthStatus reaching
// /healthz.
func TestHealthStatusPassesThrough(t *testing.T) {
	opts := testOptions(t)
	opts.HealthStatus = "degraded"
	s := start(t, opts)
	if code, body := get(t, http.DefaultClient, baseURL(t, s)+"/healthz", ""); code != 200 || strings.TrimSpace(body) != `{"status":"degraded"}` {
		t.Errorf("GET /healthz = %d %s, want 200 with status degraded", code, body)
	}
}
