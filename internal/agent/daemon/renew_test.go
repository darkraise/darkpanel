package daemon

import (
	"context"
	"crypto/x509"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/fleet"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/transport"
)

func TestRenewAt(t *testing.T) {
	start := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	cert := &x509.Certificate{NotBefore: start, NotAfter: start.Add(fleet.CertValidity)}
	if got, want := renewAt(cert), start.Add(fleet.CertRenewAfter); !got.Equal(want) {
		t.Errorf("renewAt = %v, want %v", got, want)
	}
}

// renewalHost enrolls H1 with a certificate issued 300 days ago, past two
// thirds of its life, then moves the controller's clock to now, so the
// certificates it issues next are fresh.
func renewalHost(t *testing.T) (*testController, *agentHost, *Credentials) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	ctrl := newTestController(t, now.Add(-300*24*time.Hour))
	ctrl.addHost("H1", "pending")
	h := newAgentHost(t)
	old, err := Enroll(context.Background(), h.layout.Paths.AgentTLSDir, ctrl.token("H1"))
	if err != nil {
		t.Fatal(err)
	}
	ctrl.clock.Advance(300 * 24 * time.Hour)
	return ctrl, h, old
}

// Spec section 2, Renewal: after two thirds of its life the agent renews
// over its live session, saves a new generation, reconnects with it, and the
// first handshake with it deletes the old generation.
func TestRunRenewsTheCertificate(t *testing.T) {
	ctrl, h, old := renewalHost(t)
	dir := h.layout.Paths.AgentTLSDir
	h.start(h.config())
	h.waitClock("a session with the renewed certificate", func() bool { return len(ctrl.agentSessions()) == 2 })
	sessions := ctrl.agentSessions()
	if sessions[0].serial != transport.SerialText(old.Cert.SerialNumber) {
		t.Errorf("the first session used serial %s, want the enrolled %s", sessions[0].serial, transport.SerialText(old.Cert.SerialNumber))
	}
	current, err := LoadCredentials(dir)
	if err != nil {
		t.Fatal(err)
	}
	if current.Generation != 2 || current.Key.Equal(old.Key) || sessions[1].serial != transport.SerialText(current.Cert.SerialNumber) {
		t.Errorf("after the renewal: generation %d, new key %v, second session serial %s",
			current.Generation, !current.Key.Equal(old.Key), sessions[1].serial)
	}
	if current.HostID() != "H1" || !current.CA.Equal(old.CA) || current.Controller != old.Controller {
		t.Errorf("the renewed generation changed the host, root or controller: %+v", current)
	}
	waitFor(t, "the old generation to go", func() bool { return slices.Equal(generations(t, dir), []string{"gen-2"}) })
	var revoked bool
	if err := ctrl.db.Read().QueryRow(`SELECT revoked_at IS NOT NULL FROM core_agent_certs WHERE serial = ?`,
		transport.SerialText(old.Cert.SerialNumber)).Scan(&revoked); err != nil || !revoked {
		t.Errorf("the enrolled serial is not revoked after the handshake with the renewed one: %v", err)
	}
	// The renewed certificate is fresh: no further renewal or reconnection.
	for range 20 {
		h.clock.Advance(transport.BackoffMax)
		time.Sleep(5 * time.Millisecond)
	}
	if n := len(ctrl.agentSessions()); n != 2 {
		t.Errorf("%d sessions after the renewal, want 2", n)
	}
}

func TestRunRetriesAFailedRenewal(t *testing.T) {
	ctrl, h, _ := renewalHost(t)
	ctrl.mu.Lock()
	ctrl.renewErr = errcode.New(errcode.Forbidden, "no renewal today")
	ctrl.mu.Unlock()
	renewals := func() int {
		ctrl.mu.Lock()
		defer ctrl.mu.Unlock()
		return ctrl.renewals
	}
	h.start(h.config())
	waitFor(t, "a renewal attempt", func() bool { return renewals() == 1 })
	waitFor(t, "the failure to be logged", func() bool {
		return strings.Contains(h.logs.String(), "daemon: renew the agent certificate")
	})
	// The session stays up and the next attempt waits RenewRetry.
	h.clock.Advance(RenewRetry - time.Minute)
	time.Sleep(50 * time.Millisecond)
	if renewals() != 1 || len(ctrl.agentSessions()) != 1 || connected(h.hello()) != "true" {
		t.Errorf("before RenewRetry: %d renewals, %d sessions, connected %s", renewals(), len(ctrl.agentSessions()), connected(h.hello()))
	}
	h.clock.Advance(time.Minute)
	waitFor(t, "a second attempt", func() bool { return renewals() == 2 })
	if gens := generations(t, h.layout.Paths.AgentTLSDir); !slices.Equal(gens, []string{"gen-1"}) {
		t.Errorf("generations after failed renewals = %v, want gen-1 alone", gens)
	}
}

// An enrollment written beside the running daemon wins over a renewal of the
// session's generation: the renewal saves nothing and the daemon reconnects
// with the enrolled generation.
func TestRunRenewalYieldsToANewerEnrollment(t *testing.T) {
	ctrl, h, old := renewalHost(t)
	dir := h.layout.Paths.AgentTLSDir
	ctrl.mu.Lock()
	ctrl.renewErr = errcode.New(errcode.Forbidden, "no renewal today")
	ctrl.mu.Unlock()
	renewals := func() int {
		ctrl.mu.Lock()
		defer ctrl.mu.Unlock()
		return ctrl.renewals
	}
	h.start(h.config())
	waitFor(t, "a renewal attempt", func() bool { return renewals() == 1 })

	ctrl.addHost("H2", "pending")
	next, err := Enroll(context.Background(), dir, ctrl.token("H2"))
	if err != nil {
		t.Fatal(err)
	}
	ctrl.mu.Lock()
	ctrl.renewErr = nil
	ctrl.mu.Unlock()
	h.clock.Advance(RenewRetry)
	waitFor(t, "a second attempt", func() bool { return renewals() == 2 })

	oldSerial, nextSerial := transport.SerialText(old.Cert.SerialNumber), transport.SerialText(next.Cert.SerialNumber)
	h.waitClock("a session with the enrolled certificate", func() bool {
		return slices.ContainsFunc(ctrl.agentSessions(), func(s *agentSession) bool { return s.serial == nextSerial })
	})
	current, err := LoadCredentials(dir)
	if err != nil {
		t.Fatal(err)
	}
	if current.Generation != 2 || current.HostID() != "H2" {
		t.Errorf("current is generation %d of %s, want generation 2 of H2", current.Generation, current.HostID())
	}
	waitFor(t, "the session's generation to go", func() bool { return slices.Equal(generations(t, dir), []string{"gen-2"}) })
	if !strings.Contains(h.logs.String(), "a newer credential generation is current") {
		t.Error("the superseded renewal is not logged")
	}
	for _, s := range ctrl.agentSessions() {
		if s.serial != oldSerial && s.serial != nextSerial {
			t.Errorf("a session used serial %s of host %s, neither the first nor the enrolled certificate", s.serial, s.hostID)
		}
	}
}
