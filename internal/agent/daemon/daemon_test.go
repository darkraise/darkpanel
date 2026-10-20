package daemon

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/darkraise/darkpanel/internal/agent/api"
	"github.com/darkraise/darkpanel/internal/agent/runtime"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/install"
	"github.com/darkraise/darkpanel/internal/platform"
	"github.com/darkraise/darkpanel/internal/transport"
	"github.com/darkraise/darkpanel/internal/update"
)

// probeModule has one agent read that reports who ran it where.
type probeModule struct{}

type probeIn struct {
	Name string `json:"name"`
}

type probeOut struct {
	HostID string `json:"hostId"`
	Actor  string `json:"actor"`
}

type probeRead struct{}

func (probeRead) Read(_ context.Context, env core.Env, _ probeIn) (probeOut, error) {
	return probeOut{HostID: env.HostID, Actor: env.Principal.Actor}, nil
}

var refProbe = core.ActionRef{Name: "probe.item.get", Version: 1}

func (probeModule) Manifest() core.Manifest {
	return core.Manifest{ID: "probe", Version: 1, DefaultEnabled: true, Actions: []core.ActionDef{
		core.DefineRead(core.ActionSpec{Name: refProbe.Name, Version: 1, Side: core.SideAgent}, probeRead{}),
	}}
}

// fakeRecoverer records the daemon's start recovery.
type fakeRecoverer struct {
	mu     sync.Mutex
	modes  []update.RecoverMode
	err    error
	during func() // runs inside Recover
}

func (f *fakeRecoverer) Recover(_ context.Context, mode update.RecoverMode, tx string) error {
	f.mu.Lock()
	f.modes = append(f.modes, mode)
	f.mu.Unlock()
	if tx != "" {
		return errors.New("fakeRecoverer: unexpected transaction " + tx)
	}
	if f.during != nil {
		f.during()
	}
	return f.err
}

func (f *fakeRecoverer) calls() []update.RecoverMode {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.modes)
}

// stubSystemd knows no unit and accepts every job.
type stubSystemd struct{}

func (stubSystemd) StartTransientService(context.Context, string, platform.TransientService) error {
	return nil
}
func (stubSystemd) StartTransientTimer(context.Context, string, platform.TransientTimer) error {
	return nil
}
func (stubSystemd) Start(context.Context, string) error                { return nil }
func (stubSystemd) Stop(context.Context, string) error                 { return nil }
func (stubSystemd) Restart(context.Context, string) error              { return nil }
func (stubSystemd) Kill(context.Context, string, syscall.Signal) error { return nil }
func (stubSystemd) ResetFailed(context.Context, string) error          { return nil }
func (stubSystemd) Enable(context.Context, ...string) error            { return nil }
func (stubSystemd) Disable(context.Context, ...string) error           { return nil }
func (stubSystemd) Reload(context.Context) error                       { return nil }
func (stubSystemd) State(context.Context, string) (platform.UnitState, error) {
	return platform.UnitState{LoadState: "not-found", ActiveState: "inactive", SubState: "dead"}, nil
}

// noUsers finds no user.
type noUsers struct{}

func (noUsers) Lookup(string) (int, int, bool, error) { return 0, 0, false, nil }

// syncBuffer is a log destination the daemon's goroutines share.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// agentHost is one host's installed layout, agent.db and clock.
type agentHost struct {
	t      *testing.T
	layout install.Layout
	db     *store.DB
	clock  *coretest.FakeClock
	logs   *syncBuffer
	rec    *fakeRecoverer
}

func newAgentHost(t *testing.T) *agentHost {
	t.Helper()
	l := install.Rooted(t.TempDir())
	sockDir, err := os.MkdirTemp("", "dp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	l.Paths.AgentSocket = filepath.Join(sockDir, "run", "agent.sock")
	if err := os.MkdirAll(l.Paths.AgentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(context.Background(), l.Paths.AgentDB())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return &agentHost{
		t: t, layout: l, db: db, clock: coretest.NewFakeClock(time.Now().UTC().Truncate(time.Second)),
		logs: &syncBuffer{}, rec: &fakeRecoverer{},
	}
}

func (h *agentHost) config() Config {
	h.t.Helper()
	reg, err := core.NewRegistry(probeModule{})
	if err != nil {
		h.t.Fatal(err)
	}
	return Config{
		Paths: h.layout.Paths,
		Runtime: runtime.Config{
			DB: h.db, Sealer: coretest.Sealer{}, Clock: h.clock, Log: slog.New(slog.NewTextHandler(h.logs, nil)),
			Registry: reg, BinaryVersion: "0.2.0", BackupDir: h.layout.Paths.AgentBackups(),
			Exec: platform.NewRunner(nil), FilesTestMode: true,
		},
		Updater:   h.rec,
		AllowPeer: func(uint32) bool { return true },
	}
}

// start runs the daemon with cfg until the test ends, and waits until its
// socket answers. The returned function stops it and returns Run's result.
func (h *agentHost) start(cfg Config) func() error {
	h.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, cfg) }()
	var once sync.Once
	var result error
	stop := func() error {
		once.Do(func() {
			cancel()
			result = <-done
		})
		return result
	}
	h.t.Cleanup(func() { _ = stop() })
	waitFor(h.t, "the agent socket", func() bool {
		select {
		case err := <-done:
			done <- err
			h.t.Fatalf("Run returned %v before serving", err)
		default:
		}
		_, err := h.socketClient().Hello(context.Background())
		return err == nil
	})
	return stop
}

func (h *agentHost) socketClient() *api.Client {
	return api.NewClient(api.ClientConfig{Dial: func(ctx context.Context) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", h.layout.Paths.AgentSocket)
	}})
}

// hello asks the agent socket for Hello.
func (h *agentHost) hello() core.AgentHello {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	hello, err := h.socketClient().Hello(ctx)
	if err != nil {
		h.t.Fatal(err)
	}
	return hello
}

// waitClock polls cond like waitFor, advancing the clock by the longest
// backoff between polls so the dial loop never sleeps for long.
func (h *agentHost) waitClock(what string, cond func() bool) {
	h.t.Helper()
	waitFor(h.t, what, func() bool {
		if cond() {
			return true
		}
		h.clock.Advance(transport.BackoffMax)
		return false
	})
}

// sessionClient is the controller's client over an accepted agent session.
func sessionClient(ctrl *testController, s *agentSession) *api.Client {
	return api.NewClient(api.ClientConfig{Dial: s.sess.Open, Controller: &api.ControllerHello{
		Protocol: 1, Version: "0.2.0", ControllerID: ctrl.auth.ControllerID(),
	}})
}

func connected(h core.AgentHello) string {
	if h.ControllerConnected == nil {
		return "null"
	}
	if *h.ControllerConnected {
		return "true"
	}
	return "false"
}

// TestRunHoldsTheAgentLock: uninstall refuses while the daemon runs, and a
// second daemon does not start.
func TestRunHoldsTheAgentLock(t *testing.T) {
	h := newAgentHost(t)
	stop := h.start(h.config())
	ctx := context.Background()
	u, err := install.PlanUninstall(ctx, install.UninstallConfig{
		Layout: h.layout, Systemd: stubSystemd{}, Exec: platform.NewRunner(nil), Users: noUsers{},
		Clock: h.clock, Log: slog.New(slog.DiscardHandler), FilesTestMode: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	err = u.Run(ctx, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "an agent still runs") {
		t.Fatalf("uninstall Run = %v, want the running agent refused", err)
	}
	if err := Run(ctx, h.config()); err == nil || !strings.Contains(err.Error(), "another agent runs") {
		t.Errorf("a second Run = %v, want it refused", err)
	}
	if err := stop(); err != nil {
		t.Fatalf("Run = %v after its context ended, want nil", err)
	}
	fd, err := unix.Open(filepath.Join(h.layout.Paths.AgentDir, install.AgentLockName), unix.O_RDWR|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unix.Close(fd) }()
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		t.Errorf("the stopped daemon still holds the lock: %v", err)
	}
}

// The start recovery runs before the socket serves, and update.in_progress
// is logged without stopping the start.
func TestRunRecoversBeforeServing(t *testing.T) {
	h := newAgentHost(t)
	h.rec.err = errcode.New(errcode.UpdateInProgress, "a runner holds the update lock")
	var socketDuring bool
	h.rec.during = func() {
		_, err := os.Lstat(h.layout.Paths.AgentSocket)
		socketDuring = err == nil
	}
	h.start(h.config())
	if got := h.rec.calls(); !slices.Equal(got, []update.RecoverMode{update.RecoverStart}) {
		t.Errorf("Recover modes = %v, want one RecoverStart", got)
	}
	if socketDuring {
		t.Error("the socket existed while the start recovery ran")
	}
	if logs := h.logs.String(); !strings.Contains(logs, "update.in_progress") {
		t.Errorf("the recovery error was not logged:\n%s", logs)
	}
}

// The real update library at start: a reservation whose operation has no
// record is swept.
func TestRunSweepsStaleReservations(t *testing.T) {
	h := newAgentHost(t)
	cfg := h.config()
	for _, dir := range []string{h.layout.Paths.UpdateDir, h.layout.Paths.BinaryDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	u, err := update.New(update.Config{
		Paths: h.layout.Paths, DB: h.db, Systemd: stubSystemd{}, Clock: h.clock, Version: "0.2.0",
		Log: cfg.Runtime.Log,
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg.Updater = u
	// The runtime creates core_locks; a first start and stop migrates.
	stop := h.start(cfg)
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	if _, err := h.db.Writer().Exec(`INSERT INTO core_locks (key, owner) VALUES ('*', 'op:01JAAAAAAAAAAAAAAAAAAAAAAA')`); err != nil {
		t.Fatal(err)
	}
	h.start(cfg)
	var n int
	if err := h.db.Read().QueryRow(`SELECT count(*) FROM core_locks`).Scan(&n); err != nil || n != 0 {
		t.Errorf("core_locks holds %d rows (%v) after the start, want none", n, err)
	}
}

func TestRunServesTheSocketBeforeEnrollment(t *testing.T) {
	h := newAgentHost(t)
	h.start(h.config())
	hello := h.hello()
	if hello.HostID != UnenrolledHostID || hello.ControllerConnected != nil || hello.JournalEpoch == "" {
		t.Errorf("Hello = %+v, want the unenrolled host with controllerConnected null", hello)
	}
	out, err := h.socketClient().Read(context.Background(), core.Principal{Actor: "user:mallory"}, refProbe, json.RawMessage(`{"name":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	var got probeOut
	if err := json.Unmarshal(out, &got); err != nil || got != (probeOut{HostID: UnenrolledHostID, Actor: core.ActorUnenrolled}) {
		t.Errorf("probe over the socket = %s, %v", out, err)
	}
}

func TestRunConnectsAndRedials(t *testing.T) {
	ctrl := newTestController(t, time.Now().UTC().Truncate(time.Second))
	ctrl.addHost("H1", "pending")
	h := newAgentHost(t)
	if err := (Agent{TLSDir: h.layout.Paths.AgentTLSDir}).Enroll(context.Background(), ctrl.token("H1")); err != nil {
		t.Fatal(err)
	}
	h.start(h.config())
	waitFor(t, "a session", func() bool { return len(ctrl.agentSessions()) == 1 })
	s := ctrl.agentSessions()[0]
	ctx := context.Background()
	hello, err := sessionClient(ctrl, s).Hello(ctx)
	if err != nil || hello.HostID != "H1" || connected(hello) != "true" {
		t.Fatalf("Hello over the session = %+v, %v", hello, err)
	}
	alice := core.Principal{Actor: "user:alice", Scope: core.ScopeOperate}
	out, err := sessionClient(ctrl, s).Read(ctx, alice, refProbe, json.RawMessage(`{"name":"x"}`))
	if err != nil || string(out) != `{"actor":"user:alice","hostId":"H1"}` {
		t.Errorf("probe over the session = %s, %v", out, err)
	}
	if hello := h.hello(); hello.HostID != "H1" || connected(hello) != "true" {
		t.Errorf("Hello over the socket = %+v", hello)
	}

	// The controller drops the session: the agent reports it and redials.
	_ = s.sess.Close()
	waitFor(t, "the session to end", func() bool { return connected(h.hello()) == "false" })
	h.waitClock("a second session", func() bool { return len(ctrl.agentSessions()) == 2 })
	waitFor(t, "the agent to report it", func() bool { return connected(h.hello()) == "true" })

	// A revoked agent keeps redialing and keeps serving its socket.
	ctrl.revoke("H1")
	refused := ctrl.refusals()
	_ = ctrl.agentSessions()[1].sess.Close()
	h.waitClock("refused redials", func() bool { return ctrl.refusals() >= refused+2 })
	if hello := h.hello(); connected(hello) != "false" {
		t.Errorf("a refused agent reports controllerConnected %s", connected(hello))
	}
}

func TestRunTakesUpAnEnrollment(t *testing.T) {
	ctrl := newTestController(t, time.Now().UTC().Truncate(time.Second))
	ctrl.addHost("H1", "pending")
	h := newAgentHost(t)
	h.start(h.config())
	before := h.hello()
	if before.HostID != UnenrolledHostID {
		t.Fatalf("Hello before the enrollment = %+v", before)
	}
	if err := (Agent{TLSDir: h.layout.Paths.AgentTLSDir}).Enroll(context.Background(), ctrl.token("H1")); err != nil {
		t.Fatal(err)
	}
	h.waitClock("the agent to dial as H1", func() bool { return len(ctrl.agentSessions()) == 1 })
	waitFor(t, "the socket to serve H1", func() bool {
		hello := h.hello()
		return hello.HostID == "H1" && connected(hello) == "true"
	})
	if after := h.hello(); after.JournalEpoch != before.JournalEpoch {
		t.Errorf("the enrollment changed the journal epoch from %s to %s", before.JournalEpoch, after.JournalEpoch)
	}
	out, err := h.socketClient().Read(context.Background(), core.Principal{Actor: "user:mallory"}, refProbe, json.RawMessage(`{"name":"x"}`))
	if err != nil || !strings.Contains(string(out), `"actor":"local:root@H1"`) {
		t.Errorf("probe over the socket = %s, %v", out, err)
	}
}

// Spec section 2: the previous generation is kept until the first
// successful handshake with the new one, then deleted.
func TestRunDeletesTheOldGenerationAfterAHandshake(t *testing.T) {
	ctrl := newTestController(t, time.Now().UTC().Truncate(time.Second))
	ctrl.addHost("H1", "pending")
	h := newAgentHost(t)
	dir := h.layout.Paths.AgentTLSDir
	old, err := Enroll(context.Background(), dir, ctrl.token("H1"))
	if err != nil {
		t.Fatal(err)
	}
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	csr, err := csrPEM(key)
	if err != nil {
		t.Fatal(err)
	}
	// The controller revokes serials created before the one a handshake uses.
	ctrl.clock.Advance(time.Second)
	certPEM, err := ctrl.auth.Renew(context.Background(), "H1", []byte(csr))
	if err != nil {
		t.Fatal(err)
	}
	cert, err := parseCertificate(certPEM)
	if err != nil {
		t.Fatal(err)
	}
	next := &Credentials{Key: key, Cert: cert, CA: old.CA, Controller: old.Controller}
	if _, err := SaveGeneration(dir, next, old.Generation); err != nil {
		t.Fatal(err)
	}
	if gens := generations(t, dir); !slices.Equal(gens, []string{"gen-1", "gen-2"}) {
		t.Fatalf("generations before the handshake = %v", gens)
	}
	h.start(h.config())
	waitFor(t, "a session", func() bool { return len(ctrl.agentSessions()) == 1 })
	if s := ctrl.agentSessions()[0]; s.serial != transport.SerialText(cert.SerialNumber) {
		t.Errorf("the agent connected with serial %s, want the new %s", s.serial, transport.SerialText(cert.SerialNumber))
	}
	waitFor(t, "the old generation to go", func() bool { return slices.Equal(generations(t, dir), []string{"gen-2"}) })
	var revoked bool
	if err := ctrl.db.Read().QueryRow(`SELECT revoked_at IS NOT NULL FROM core_agent_certs WHERE serial = ?`,
		transport.SerialText(old.Cert.SerialNumber)).Scan(&revoked); err != nil || !revoked {
		t.Errorf("the old serial is not revoked after the handshake: %v", err)
	}
}
