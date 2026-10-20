package coreactions

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/controller/fleet"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/transport"
)

// testAdvertise is the --agent-advertise address of newEnrollEnv.
const testAdvertise = "198.18.0.10:7443"

// enrollEnv is a test env whose core module has a real agent authority,
// recording sessions, and the local host h1 named alpha.
type enrollEnv struct {
	*testEnv
	authority *fleet.Authority
	sessions  *fakeSessions
}

func newEnrollEnv(t *testing.T, advertise string) *enrollEnv {
	t.Helper()
	out := &enrollEnv{sessions: &fakeSessions{}}
	out.testEnv = newTestEnvWith(t, func(e *testEnv, d *Deps) {
		a, err := fleet.LoadOrCreateAuthority(context.Background(), e.db, coretest.Sealer{}, e.clock)
		if err != nil {
			t.Fatal(err)
		}
		out.authority = a
		d.Authority, d.AgentAdvertise, d.Sessions = a, advertise, out.sessions
	}, fakeModule{"demo", 1})
	out.addHost("h1", "alpha", true)
	return out
}

// hostIDRule is the host id rule of agent certificates (transport.HostIDOf).
var hostIDRule = regexp.MustCompile(`^[0-9A-Za-z]{1,64}$`)

// checkGrant decodes a grant and checks that its token enrolls the grant's
// host at address against this controller's authority, expires an hour
// after t0, and is the host's one stored token.
func (e *enrollEnv) checkGrant(raw json.RawMessage, address string) core.EnrollmentGrant {
	e.t.Helper()
	var g core.EnrollmentGrant
	if err := json.Unmarshal(raw, &g); err != nil {
		e.t.Fatal(err)
	}
	tok, err := transport.ParseToken(g.Token)
	if err != nil {
		e.t.Fatalf("the granted token does not parse: %v", err)
	}
	if tok.HostID != g.Host.ID || tok.Address != address || tok.Fingerprint != e.authority.Fingerprint() ||
		tok.ControllerSAN != e.authority.ControllerSAN() {
		e.t.Errorf("token for host %s at %s, want host %s at %s of this controller", tok.HostID, tok.Address, g.Host.ID, address)
	}
	if !g.ExpiresAt.Equal(t0.Add(fleet.TokenTTL)) {
		e.t.Errorf("expiresAt = %v, want %v", g.ExpiresAt, t0.Add(fleet.TokenTTL))
	}
	hash, err := tok.SecretHash()
	if err != nil {
		e.t.Fatal(err)
	}
	var stored []byte
	if err := e.db.Read().QueryRow(`SELECT hash FROM core_enroll_tokens WHERE host_id = ? AND consumed_at IS NULL`,
		g.Host.ID).Scan(&stored); err != nil {
		e.t.Fatalf("stored token of %s: %v", g.Host.ID, err)
	}
	if !bytes.Equal(stored, hash) {
		e.t.Error("the stored token is not the granted one")
	}
	return g
}

func wantField(t *testing.T, err error, path string) {
	t.Helper()
	var e *errcode.Error
	if !errors.As(err, &e) || e.Code != errcode.InvalidInput || len(e.Fields) != 1 || e.Fields[0].Path != path {
		t.Fatalf("err = %#v, want request.invalid_input naming %s", err, path)
	}
}

func TestHostCreate(t *testing.T) {
	e := newEnrollEnv(t, testAdvertise)
	in := core.HostCreateInput{Name: "web1"}
	if err := e.verify("core.host.create", in); err == nil {
		t.Error("Verify passed before the host exists")
	}
	v := e.run(operator, "core.host.create", in, nil)
	g := e.checkGrant(v.Output, testAdvertise)
	h := g.Host
	if !hostIDRule.MatchString(h.ID) || h.Name != "web1" || h.State != core.HostPending || h.IsLocal || h.Online ||
		len(h.Modules) != 1 {
		t.Errorf("host = %+v, want a pending remote host web1 with the demo module", h)
	}
	noSecret(t, []string{g.Token}, e.change(v.ID), e.auditEntries())
	if err := e.verify("core.host.create", in); err != nil {
		t.Errorf("Verify: %v", err)
	}

	v = e.run(operator, "core.host.create", core.HostCreateInput{Name: "web2", ControllerAddress: "10.77.0.10:7443"}, nil)
	e.checkGrant(v.Output, "10.77.0.10:7443")
}

func TestHostCreateRefusesATakenName(t *testing.T) {
	e := newEnrollEnv(t, testAdvertise)
	pv, err := e.plan(operator, "core.host.create", core.HostCreateInput{Name: "alpha"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.apply(operator, pv.PlanID, "")
	wantCode(t, err, errcode.PlanGuardFailed)

	// A host of that name created between the engine's recheck and the apply.
	e.run(operator, "core.host.create", core.HostCreateInput{Name: "web1"}, nil)
	wantCode(t, e.applyDirect("core.host.create", core.HostCreateInput{Name: "web1"}), errcode.PlanStateChanged)
	if n := e.count(`SELECT count(*) FROM core_hosts WHERE name = 'web1'`); n != 1 {
		t.Errorf("hosts named web1 = %d, want 1", n)
	}
}

func TestHostEnrollmentNeedsAnAddress(t *testing.T) {
	e := newEnrollEnv(t, "")
	e.addHost("h2", "web1", false)
	_, err := e.plan(operator, "core.host.create", core.HostCreateInput{Name: "web2"})
	wantField(t, err, "/controllerAddress")
	_, err = e.plan(operator, "core.host.token", core.HostTokenInput{HostID: "h2"})
	wantField(t, err, "/controllerAddress")
	v := e.run(operator, "core.host.token", core.HostTokenInput{HostID: "h2", ControllerAddress: "[fd7a::1]:7443"}, nil)
	e.checkGrant(v.Output, "[fd7a::1]:7443")
}

// TestHostEnrollmentChecksTheAddress: a token is only as good as the address
// it carries, and no schema pattern covers --agent-advertise.
func TestHostEnrollmentChecksTheAddress(t *testing.T) {
	for _, advertise := range []string{"198.18.0.10", "198.18.0.10:0", "198.18.0.10:65536", "198.18.0.10:http", ":7443"} {
		e := newEnrollEnv(t, advertise)
		e.addHost("h2", "web1", false)
		_, err := e.plan(operator, "core.host.create", core.HostCreateInput{Name: "web2"})
		wantField(t, err, "/controllerAddress")
		_, err = e.plan(operator, "core.host.token", core.HostTokenInput{HostID: "h2"})
		wantField(t, err, "/controllerAddress")
		wantField(t, e.applyDirect("core.host.token", core.HostTokenInput{HostID: "h2"}), "/controllerAddress")
		if n := e.count(`SELECT count(*) FROM core_enroll_tokens`); n != 0 {
			t.Errorf("advertise %q: tokens = %d, want none", advertise, n)
		}
	}
	e := newEnrollEnv(t, testAdvertise)
	for _, addr := range []string{"web.example:0", "web.example:65536", "web.example:99999", "web.example:007443"} {
		_, err := e.plan(operator, "core.host.create", core.HostCreateInput{Name: "web2", ControllerAddress: addr})
		wantField(t, err, "/controllerAddress")
	}
	v := e.run(operator, "core.host.create", core.HostCreateInput{Name: "web2", ControllerAddress: "web.example:65535"}, nil)
	e.checkGrant(v.Output, "web.example:65535")
}

func TestHostToken(t *testing.T) {
	e := newEnrollEnv(t, testAdvertise)
	e.addHost("h2", "web1", false)
	in := core.HostTokenInput{HostID: "h2"}
	if err := e.verify("core.host.token", in); err == nil {
		t.Error("Verify passed before a token exists")
	}
	first := e.checkGrant(e.run(operator, "core.host.token", in, nil).Output, testAdvertise)
	second := e.checkGrant(e.run(operator, "core.host.token", in, nil).Output, testAdvertise)
	if first.Token == second.Token || second.Host.ID != "h2" {
		t.Errorf("the second grant = %+v, want a new token for h2", second.Host)
	}
	if n := e.count(`SELECT count(*) FROM core_enroll_tokens`); n != 1 {
		t.Errorf("tokens = %d, want the replacement only", n)
	}
	if err := e.verify("core.host.token", in); err != nil {
		t.Errorf("Verify: %v", err)
	}

	e.setHostState("h2", core.HostEnrolled)
	pv, err := e.plan(operator, "core.host.token", in)
	if err != nil {
		t.Fatal(err)
	}
	if pv.Confirmation != "web1" {
		t.Errorf("confirmation = %q, want the host name web1", pv.Confirmation)
	}
	_, err = e.apply(operator, pv.PlanID, pv.Confirmation)
	wantCode(t, err, errcode.PlanGuardFailed)
	wantCode(t, e.applyDirect("core.host.token", in), errcode.PlanStateChanged)

	e.setHostState("h2", core.HostRevoked)
	e.checkGrant(e.run(operator, "core.host.token", in, nil).Output, testAdvertise)

	_, err = e.plan(operator, "core.host.token", core.HostTokenInput{HostID: "nope"})
	wantCode(t, err, errcode.NotFound)
}

// TestHostTokenRefusesTheLocalHost: a network caller must not enroll another
// machine under the controller's own host id, in any state of that host.
func TestHostTokenRefusesTheLocalHost(t *testing.T) {
	e := newEnrollEnv(t, testAdvertise)
	local := core.HostTokenInput{HostID: "h1"}
	for _, state := range []string{core.HostPending, core.HostRevoked} {
		e.setHostState("h1", state)
		pv, err := e.plan(operator, "core.host.token", local)
		if err != nil {
			t.Fatal(err)
		}
		if g := pv.Guards[0]; g.ID != "not-local" || g.OK || !strings.Contains(g.Message, "core.host.enroll-local") {
			t.Errorf("%s local host: guard %+v, want not-local failing and naming core.host.enroll-local", state, g)
		}
		_, err = e.apply(operator, pv.PlanID, pv.Confirmation)
		wantCode(t, err, errcode.PlanGuardFailed)
		wantCode(t, e.applyDirect("core.host.token", local), errcode.PlanGuardFailed)
	}
	if n := e.count(`SELECT count(*) FROM core_enroll_tokens`); n != 0 {
		t.Errorf("tokens = %d, want none for the local host", n)
	}
}

func TestHostEnrollLocal(t *testing.T) {
	e := newEnrollEnv(t, "")
	e.setHostState("h1", core.HostEnrolled)
	e.addCert("h1", "b1")
	if err := e.verify("core.host.enroll-local", NoInput{}); err == nil {
		t.Error("Verify passed before a token exists")
	}
	v := e.run(root, "core.host.enroll-local", NoInput{}, nil)
	g := e.checkGrant(v.Output, LocalAgentAddress)
	if g.Host.ID != "h1" || g.Host.State != core.HostRevoked || !g.Host.IsLocal {
		t.Errorf("host = %+v, want the local host h1, revoked", g.Host)
	}
	if n := e.count(`SELECT count(*) FROM core_agent_certs WHERE revoked_at IS NULL`); n != 0 {
		t.Errorf("unrevoked certificates = %d, want none", n)
	}
	if got := e.sessions.closed(); !reflect.DeepEqual(got, []string{"h1"}) {
		t.Errorf("closed sessions = %v, want [h1]", got)
	}
	if err := e.verify("core.host.enroll-local", NoInput{}); err != nil {
		t.Errorf("Verify: %v", err)
	}

	// Every state is allowed, so a repeated repair works too.
	e.checkGrant(e.run(root, "core.host.enroll-local", NoInput{}, nil).Output, LocalAgentAddress)

	fresh := newEnrollEnv(t, testAdvertise)
	g = fresh.checkGrant(fresh.run(root, "core.host.enroll-local", NoInput{}, nil).Output, LocalAgentAddress)
	if g.Host.State != core.HostRevoked {
		t.Errorf("a fresh local host is %s after enroll-local, want revoked", g.Host.State)
	}

	none := newEnrollEnv(t, testAdvertise)
	none.exec(`DELETE FROM core_hosts`)
	_, err := none.plan(root, "core.host.enroll-local", NoInput{})
	wantCode(t, err, errcode.NotFound)
}
