package daemon

import (
	"context"
	"database/sql"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/fleet"
	"github.com/darkraise/darkpanel/internal/install"
	"github.com/darkraise/darkpanel/internal/transport"
)

// revoke revokes every certificate of hostID.
func (c *testController) revoke(hostID string) {
	c.t.Helper()
	err := c.db.Write(context.Background(), func(tx *sql.Tx) error {
		return fleet.RevokeHost(context.Background(), tx, hostID, c.clock.Now())
	})
	if err != nil {
		c.t.Fatal(err)
	}
}

// refusals is the number of failed handshakes.
func (c *testController) refusals() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.refused
}

// waitFor polls cond every 10 ms for up to 10 s.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// enrolled returns a controller with host H1 enrolled through Agent, and
// the agent's credentials directory.
func enrolled(t *testing.T) (*testController, string) {
	t.Helper()
	ctrl := newTestController(t, time.Now().UTC().Truncate(time.Second))
	ctrl.addHost("H1", "pending")
	dir := filepath.Join(t.TempDir(), "agent")
	if err := (Agent{TLSDir: dir}).Enroll(context.Background(), ctrl.token("H1")); err != nil {
		t.Fatalf("Enroll: %v", err)
	}
	return ctrl, dir
}

// resave saves the current credentials again with another endpoint.
func resave(t *testing.T, dir string, e Endpoint) {
	t.Helper()
	c, err := LoadCredentials(dir)
	if err != nil {
		t.Fatal(err)
	}
	c.Controller = e
	if _, err := SaveGeneration(dir, c); err != nil {
		t.Fatal(err)
	}
}

// TestCheck: darkpanel agent check exits 0 accepted, 1 refused, 2
// unreachable.
func TestCheck(t *testing.T) {
	ctx := context.Background()
	if res, err := Check(ctx, filepath.Join(t.TempDir(), "agent")); res != install.CheckRefused || err != nil {
		t.Errorf("Check without credentials = %d, %v; want refused", res, err)
	}
	ctrl, dir := enrolled(t)
	if res, err := (Agent{TLSDir: dir}).Check(ctx); res != install.CheckAccepted || err != nil {
		t.Fatalf("Check = %d, %v; want accepted", res, err)
	}
	if s := ctrl.agentSessions(); len(s) != 1 || s[0].hostID != "H1" {
		t.Errorf("controller sessions = %+v, want one of H1", s)
	}

	ctrl.revoke("H1")
	before := ctrl.refusals()
	if res, err := Check(ctx, dir); res != install.CheckRefused || err != nil {
		t.Errorf("Check of a revoked host = %d, %v; want refused", res, err)
	}
	// The agent reads the alert before the controller's goroutine counts it.
	waitFor(t, "the controller to count the refusal", func() bool { return ctrl.refusals() == before+1 })

	// A controller that is not the pinned one never completes a handshake.
	resave(t, dir, Endpoint{Address: ctrl.addr, SAN: transport.ControllerURI("SOMEONEELSE")})
	if res, err := Check(ctx, dir); res != install.CheckUnreachable || err != nil {
		t.Errorf("Check against another controller = %d, %v; want unreachable", res, err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := ln.Addr().String()
	_ = ln.Close()
	resave(t, dir, Endpoint{Address: closed, SAN: ctrl.auth.ControllerSAN()})
	if res, err := Check(ctx, dir); res != install.CheckUnreachable || err != nil {
		t.Errorf("Check against a closed port = %d, %v; want unreachable", res, err)
	}
}

func TestCheckReportsUnreadableCredentials(t *testing.T) {
	_, dir := enrolled(t)
	if err := os.WriteFile(filepath.Join(dir, "gen-1", certFile), []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	if res, err := Check(context.Background(), dir); res != install.CheckRefused || err == nil {
		t.Errorf("Check = %d, %v; want refused with an error", res, err)
	}
}
