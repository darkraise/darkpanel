package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	agentapi "github.com/darkraise/darkpanel/internal/agent/api"
	"github.com/darkraise/darkpanel/internal/agent/daemon"
	"github.com/darkraise/darkpanel/internal/agent/runtime"
	"github.com/darkraise/darkpanel/internal/buildinfo"
	"github.com/darkraise/darkpanel/internal/cli"
	"github.com/darkraise/darkpanel/internal/controller/api"
	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/controller/coreactions"
	"github.com/darkraise/darkpanel/internal/controller/events"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/install"
	"github.com/darkraise/darkpanel/internal/paths"
	"github.com/darkraise/darkpanel/internal/platform"
	"github.com/darkraise/darkpanel/internal/registry"
	"github.com/darkraise/darkpanel/internal/transport"
	"github.com/darkraise/darkpanel/internal/update"
)

// eventually polls cond every 50 ms for at most 30 s, running tick between
// polls when it is set, and fails the test when cond never holds.
func eventually(t *testing.T, what string, tick func(), cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("%s: not within 30 s", what)
		}
		if tick != nil {
			tick()
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// noRecovery is the daemon's update recovery when no update ever ran.
type noRecovery struct{}

func (noRecovery) Recover(context.Context, update.RecoverMode, string) error { return nil }

// fleetAgent is a remote host's agent daemon, run in this process as
// darkpanel agent run builds it, over its own layout, agent.db and clock,
// reporting version as its binary's.
type fleetAgent struct {
	t       *testing.T
	version string
	paths   paths.Paths
	db      *store.DB
	clock   *coretest.FakeClock
	logs    *lockedBuffer
	stop    func()
}

func newFleetAgent(t *testing.T, version string) *fleetAgent {
	t.Helper()
	p := paths.Under(t.TempDir())
	sockDir, err := os.MkdirTemp("", "dp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	p.AgentSocket = filepath.Join(sockDir, "agent.sock")
	if err := os.MkdirAll(p.AgentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(context.Background(), p.AgentDB())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return &fleetAgent{t: t, version: version, paths: p, db: db, clock: coretest.NewFakeClock(time.Now().UTC()), logs: &lockedBuffer{}}
}

// enroll consumes token into the agent's credential directory.
func (a *fleetAgent) enroll(token string) {
	a.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), daemon.EnrollTimeout)
	defer cancel()
	if _, err := daemon.Enroll(ctx, a.paths.AgentTLSDir, token); err != nil {
		a.t.Fatalf("enroll: %v", err)
	}
}

// run starts the daemon until a.stop or the end of the test.
func (a *fleetAgent) run() {
	a.t.Helper()
	reg, err := registry.BuildWith(coreactions.Module(coreactions.Deps{
		Extra: registry.CoreActions(nil, ""), OwnedPaths: install.CoreOwnedPaths(),
	}))
	if err != nil {
		a.t.Fatal(err)
	}
	sealer, err := loadSealer(a.paths.AgentKey)
	if err != nil {
		a.t.Fatal(err)
	}
	uid := uint32(os.Getuid()) //nolint:gosec // G115: a uid fits in 32 bits
	cfg := daemon.Config{
		Paths: a.paths,
		Runtime: runtime.Config{
			DB: a.db, Sealer: sealer, Clock: a.clock, Log: slog.New(slog.NewTextHandler(a.logs, nil)),
			Registry: reg, BinaryVersion: a.version, BackupDir: a.paths.AgentBackups(),
			Exec: platform.NewRunner(platform.ProductionRules()),
		},
		Updater:   noRecovery{},
		AllowPeer: func(peer uint32) bool { return peer == uid },
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- daemon.Run(ctx, cfg) }()
	var once sync.Once
	a.stop = func() {
		once.Do(func() {
			cancel()
			if err := <-done; err != nil {
				a.t.Errorf("daemon.Run: %v", err)
			}
		})
	}
	a.t.Cleanup(a.stop)
}

// hello asks the agent socket for Hello.
func (a *fleetAgent) hello() (core.AgentHello, error) {
	c := agentapi.NewClient(agentapi.ClientConfig{Dial: func(ctx context.Context) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", a.paths.AgentSocket)
	}})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return c.Hello(ctx)
}

// skipWaits advances the agent's clock past its longest redial backoff and
// credential poll, so its dial loop never sleeps for long.
func (a *fleetAgent) skipWaits() { a.clock.Advance(transport.MaxBackoff) }

// loginAdmin creates the first admin through the controller socket with the
// CLI, as TestEndToEnd does, and logs it in over HTTP.
func (e *e2e) loginAdmin() {
	e.t.Helper()
	sock := e.s.SocketPath()
	out, errOut, code := e.cli(nil, adminPassword+"\n", "user", "create", "--username", "admin", "--socket", sock, "--yes")
	m := secretLine.FindStringSubmatch(out)
	if code != cli.ExitUsage || m == nil {
		e.t.Fatalf("user create: exit %d\n%s\n%s", code, out, errOut)
	}
	totp, err := auth.TOTPCode(m[1], e.clock.Now())
	if err != nil {
		e.t.Fatal(err)
	}
	if out, errOut, code := e.cli(nil, totp+"\n", "user", "activate", "--username", "admin", "--socket", sock, "--yes"); code != cli.ExitOK {
		e.t.Fatalf("user activate: exit %d\n%s\n%s", code, out, errOut)
	}
	e.clock.Advance(auth.TOTPPeriod)
	totp, _ = auth.TOTPCode(m[1], e.clock.Now())
	r := e.do("POST", "/api/v1/auth/login", map[string]string{"username": "admin", "password": adminPassword, "code": totp}, credentials{})
	var login struct {
		CSRFToken string `json:"csrfToken"`
	}
	e.want(r, 200, &login)
	for _, c := range (&http.Response{Header: r.header}).Cookies() {
		if c.Name == api.SessionCookiePlain {
			e.session = c.Value
		}
	}
	e.csrf = login.CSRFToken
}

// applyPlanned plans and applies a controller-side action with the admin
// session, answering the plan's confirmation phrase when it has one.
func (e *e2e) applyPlanned(name string, input any) change {
	e.t.Helper()
	var plan struct {
		PlanID       string  `json:"planId"`
		Confirmation *string `json:"confirmation"`
	}
	e.want(e.do("POST", "/api/v1/actions/"+name+"/v1/plan", map[string]any{"input": input}, e.admin()), 200, &plan)
	body := map[string]any{"planId": plan.PlanID}
	if plan.Confirmation != nil {
		body["confirmation"] = *plan.Confirmation
	}
	var ch change
	e.want(e.do("POST", "/api/v1/actions/"+name+"/v1/apply", body, e.admin()), 200, &ch)
	if ch.State != "completed" {
		e.t.Fatalf("%s: change %s is %s", name, ch.ID, ch.State)
	}
	return ch
}

// host reads one host of core.host.list over the network API.
func (e *e2e) host(id string) core.Host {
	e.t.Helper()
	var list struct {
		Output core.HostList `json:"output"`
	}
	e.want(e.do("POST", "/api/v1/actions/core.host.list/v1/read", map[string]any{"input": map[string]any{}}, e.admin()), 200, &list)
	for _, h := range list.Output.Hosts {
		if h.ID == id {
			return h
		}
	}
	e.t.Fatalf("core.host.list has no host %s", id)
	return core.Host{}
}

// TestFleetEndToEnd drives a controller-only server and real agent daemons
// over the agent listener's mutual TLS: enrollment, the host list, a
// resource through the agent API, the support window, offline answers,
// reconnection, revocation, re-enrollment and the agent socket without a
// controller.
func TestFleetEndToEnd(t *testing.T) {
	began := time.Now()
	opts := testOptions(t)
	// Certificates are checked against the real time, so the controller's
	// clock starts there; the authority backdates what it issues by an hour,
	// which covers the TOTP step the login advances.
	opts.Clock = coretest.NewFakeClock(time.Now().UTC().Truncate(time.Second))
	e := &e2e{t: t, clock: opts.Clock.(*coretest.FakeClock), home: t.TempDir()}
	e.s = start(t, opts)
	e.url = baseURL(t, e.s)
	e.loginAdmin()

	agentAddr := ""
	for _, b := range e.s.AgentListeners() {
		if b.Endpoint.Addr == netip.MustParseAddrPort("127.0.0.1:7443") {
			agentAddr = b.Addr.String()
		}
	}
	if agentAddr == "" {
		t.Fatalf("no loopback agent listener in %v", e.s.AgentListeners())
	}
	createHost := func(name string) core.EnrollmentGrant {
		t.Helper()
		var grant core.EnrollmentGrant
		created := e.applyPlanned("core.host.create", map[string]any{"name": name, "controllerAddress": agentAddr})
		if err := json.Unmarshal(created.Output, &grant); err != nil || grant.Token == "" {
			t.Fatalf("core.host.create output %s (%v), want a token", created.Output, err)
		}
		return grant
	}
	grant := createHost("agent1")
	hostID := grant.Host.ID
	online := func() bool { return e.host(hostID).Online }
	offline := func() bool { return !e.host(hostID).Online }

	// Enrollment and the first session (R22, R23, R27).
	hostEvents, _ := e.s.events.Subscribe(t.Context())
	agent := newFleetAgent(t, buildinfo.Version)
	agent.enroll(grant.Token)
	agent.run()
	eventually(t, "the enrolled agent online", agent.skipWaits, online)
	pubWant(t, hostEvents, events.TypeHost, events.HostEvent{HostID: hostID, Online: true})
	h := e.host(hostID)
	if h.State != core.HostEnrolled || h.AgentVersion == nil || *h.AgentVersion != buildinfo.Version ||
		h.ProtocolVersion == nil || *h.ProtocolVersion != runtime.ProtocolVersion || h.OS == nil || *h.OS == "" || h.Arch == nil || *h.Arch == "" ||
		!h.Supported {
		t.Errorf("host after its agent connected: %+v, want enrolled and supported with its versions and platform", h)
	}

	// A resource through the controller, mTLS and the agent API (R22, R26).
	var summary struct {
		Hostname         string `json:"hostname"`
		DarkpanelVersion string `json:"darkpanelVersion"`
	}
	e.want(e.do("GET", "/api/v1/hosts/"+hostID+"/resources/hostinfo/summary", nil, e.admin()), 200, &summary)
	if summary.Hostname == "" || summary.DarkpanelVersion != buildinfo.Version {
		t.Errorf("hostinfo.summary over the agent transport = %+v", summary)
	}

	// An agent this controller cannot compare its version with is outside
	// the support window: it is listed unsupported and gets only the update
	// actions (R24, R25).
	oldGrant := createHost("old1")
	oldID := oldGrant.Host.ID
	old := newFleetAgent(t, "0.1.0")
	old.enroll(oldGrant.Token)
	old.run()
	eventually(t, "the old agent online", old.skipWaits, func() bool { return e.host(oldID).Online })
	if h := e.host(oldID); h.Supported {
		t.Errorf("host of agent 0.1.0 under a %s controller: %+v, want unsupported", buildinfo.Version, h)
	}
	e.wantProblem(e.do("GET", "/api/v1/hosts/"+oldID+"/resources/hostinfo/summary", nil, e.admin()), 409, errcode.HostVersionUnsupported)
	// The test's agents run no updater, so the agent itself answers
	// unavailable: the call passed the support window.
	r := e.do("POST", "/api/v1/hosts/"+oldID+"/actions/core.update.status/v1/read", map[string]any{"input": map[string]any{}}, e.admin())
	e.wantProblem(r, 503, errcode.Unavailable)
	old.stop()

	// A stopped agent is offline, and its actions fail at once (R27).
	agent.stop()
	eventually(t, "the stopped agent offline", nil, offline)
	asked := time.Now()
	r = e.do("POST", "/api/v1/hosts/"+hostID+"/actions/core.update.status/v1/read", map[string]any{"input": map[string]any{}}, e.admin())
	if took := time.Since(asked); took > 5*time.Second {
		t.Errorf("an action on the offline host took %v", took)
	}
	e.wantProblem(r, 503, errcode.HostOffline)

	// A restarted agent reconnects.
	agent.run()
	eventually(t, "the restarted agent online", agent.skipWaits, online)

	// Revocation closes the session and the controller refuses the redial
	// (R23).
	e.applyPlanned("core.host.revoke", map[string]any{"hostId": hostID})
	eventually(t, "the revoked agent offline", nil, offline)
	eventually(t, "the revoked agent's redial refused", agent.skipWaits, func() bool {
		return strings.Contains(agent.logs.String(), daemon.ErrRefused.Error())
	})
	if h := e.host(hostID); h.State != core.HostRevoked || h.Online {
		t.Errorf("host after its redial was refused: %+v, want revoked and offline", h)
	}

	// A new token re-enrolls the host; the running daemon takes up the new
	// credentials (R23).
	reissued := e.applyPlanned("core.host.token", map[string]any{"hostId": hostID, "controllerAddress": agentAddr})
	if err := json.Unmarshal(reissued.Output, &grant); err != nil || grant.Token == "" {
		t.Fatalf("core.host.token output %s (%v), want a token", reissued.Output, err)
	}
	agent.enroll(grant.Token)
	eventually(t, "the re-enrolled agent online", agent.skipWaits, online)
	if h := e.host(hostID); h.State != core.HostEnrolled {
		t.Errorf("host after re-enrollment: %+v, want enrolled", h)
	}

	// The agent keeps serving its socket while the controller is down (R28).
	if err := e.s.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	eventually(t, "the agent socket reporting no controller", nil, func() bool {
		hello, err := agent.hello()
		return err == nil && hello.ControllerConnected != nil && !*hello.ControllerConnected
	})
	t.Logf("the fleet end-to-end test took %v", time.Since(began))
}
