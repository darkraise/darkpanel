package fleet

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/listen"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/transport"
)

// TestAgentListenerHandshake wires the agent listener as the controller
// does: listen.StartAgent serving Server.Serve. An agent completes the
// mutual-TLS handshake on the loopback endpoint, so the peer check is
// wired, and an enrollment answers on the same socket.
func TestAgentListenerHandshake(t *testing.T) {
	a, db, clock := newAuthority(t)
	reg, err := NewRegistry(RegistryConfig{
		DB: db, Clock: clock, CheckPeer: a.CheckPeer, Version: "1.2.3", ControllerID: a.ControllerID(),
		Heartbeat: transport.HeartbeatConfig{Interval: time.Hour}, Log: quietLog,
	})
	if err != nil {
		t.Fatal(err)
	}
	srv, err := NewServer(ServerConfig{Authority: a, Registry: reg, Limiter: NewEnrollLimiter(clock), Log: quietLog})
	if err != nil {
		t.Fatal(err)
	}
	// Every endpoint binds a free loopback port: the test host may lack the
	// addresses, and 7443 may be taken.
	bind := func(ctx context.Context, _ listen.Endpoint) (net.Listener, error) {
		var lc net.ListenConfig
		return lc.Listen(ctx, "tcp4", "127.0.0.1:0")
	}
	ls, err := listen.StartAgent(context.Background(), listen.AgentConfig{
		DB: db, Clock: clock, Serve: srv.Serve, Log: quietLog, Bind: bind,
		Interfaces: func(string) ([]netip.Addr, bool, error) { return nil, false, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := ls.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
		srv.Close()
		reg.Close()
	})
	bound := ls.Bound()
	if len(bound) != 1 || bound[0].Endpoint.Addr != netip.MustParseAddrPort("127.0.0.1:7443") {
		t.Fatalf("bound %v, want 127.0.0.1:7443 alone", bound)
	}
	f := &fleetServer{a: a, db: db, clock: clock, reg: reg, srv: srv, addr: bound[0].Addr.String()}

	addHost(t, db, "H1", core.HostEnrolled)
	connectAgent(t, f, enrolledCert(t, a, db, "H1"), "H1", helloAgent("H1", "v"))
	if h, _ := hostHello(t, reg, "H1"); h.HostID != "H1" {
		t.Fatalf("hello = %+v", h)
	}

	addHost(t, db, "H2", core.HostPending)
	token, _ := issueToken(t, a, db, "H2")
	csr, _ := newCSR(t)
	if status, b := enrollRequest(t, f, http.MethodPost, transport.EnrollPath, "application/json", enrollBody(t, token, csr)); status != http.StatusOK {
		t.Fatalf("enrollment through the listener answered %d %s", status, b)
	}
}
