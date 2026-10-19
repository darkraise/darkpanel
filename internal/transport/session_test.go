package transport

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// sessionPair returns the two ends of a session over an in-memory
// connection, closed when the test ends.
func sessionPair(t *testing.T) (ctrl, agent *Session) {
	t.Helper()
	c1, c2 := net.Pipe()
	ctrl, err := ControllerSession(c1)
	if err != nil {
		t.Fatal(err)
	}
	agent, err = AgentSession(c2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = ctrl.Close()
		_ = agent.Close()
	})
	return ctrl, agent
}

// sessionClient is an HTTP client whose every connection is a stream s
// opens.
func sessionClient(s *Session) *http.Client {
	return &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return s.Open(ctx) },
		},
	}
}

// serveOn serves h on the streams the peer of s opens until s closes, and
// reports Serve's result.
func serveOn(s *Session, h http.Handler) <-chan error {
	done := make(chan error, 1)
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second}
	go func() { done <- srv.Serve(s) }()
	return done
}

func get(t *testing.T, c *http.Client, method, url string) string {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader("body"))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("%s %s: %d %q %v", method, url, resp.StatusCode, body, err)
	}
	return string(body)
}

func TestSessionOverMutualTLS(t *testing.T) {
	ca := newTestCA(t)
	cfg := serverConfig(ca, ca.controllerCert(t, "01JCTRL"))
	r := handshake(t, cfg, agentDialer(ca.agentCert(t, "01JHOST"), ca.cert, ControllerURI("01JCTRL")))
	if r.serverErr != nil || r.clientErr != nil {
		t.Fatalf("handshake: server %v, client %v", r.serverErr, r.clientErr)
	}
	ctrl, err := ControllerSession(r.peer.Conn)
	if err != nil {
		t.Fatal(err)
	}
	agent, err := AgentSession(r.client)
	if err != nil {
		t.Fatal(err)
	}

	agentAPI := http.NewServeMux()
	agentAPI.HandleFunc("GET /agent/v1/hello", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "hello from the agent")
	})
	controllerAPI := http.NewServeMux()
	controllerAPI.HandleFunc("POST "+RenewPath, func(w http.ResponseWriter, req *http.Request) {
		b, _ := io.ReadAll(req.Body)
		_, _ = io.WriteString(w, "renewed "+string(b))
	})
	agentServed := serveOn(agent, agentAPI)
	ctrlServed := serveOn(ctrl, controllerAPI)

	toAgent, toController := sessionClient(ctrl), sessionClient(agent)
	for range 3 {
		if got := get(t, toAgent, "GET", "http://agent/agent/v1/hello"); got != "hello from the agent" {
			t.Fatalf("hello = %q", got)
		}
	}
	if got := get(t, toController, "POST", "http://controller"+RenewPath); got != "renewed body" {
		t.Fatalf("renew = %q", got)
	}
	toAgent.CloseIdleConnections()
	toController.CloseIdleConnections()

	if err := ctrl.Close(); err != nil {
		t.Fatal(err)
	}
	for name, served := range map[string]<-chan error{"agent": agentServed, "controller": ctrlServed} {
		select {
		case err := <-served:
			if !errors.Is(err, net.ErrClosed) {
				t.Errorf("%s Serve = %v, want net.ErrClosed", name, err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s Serve did not end after the session closed", name)
		}
	}
	select {
	case <-agent.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the agent end did not see the session close")
	}
}

func TestSessionOpenLimit(t *testing.T) {
	ctrl, agent := sessionPair(t)
	tests := []struct {
		name             string
		opener, acceptor *Session
		limit            int
	}{
		{"controller", ctrl, agent, MaxControllerStreams},
		{"agent", agent, ctrl, MaxAgentStreams},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			go func() {
				for {
					c, err := tt.acceptor.Accept()
					if err != nil {
						return
					}
					// Close each stream once the opener closes its end, so the
					// acceptor's own limit never holds the opener back.
					go func() {
						_, _ = io.Copy(io.Discard, c)
						_ = c.Close()
					}()
				}
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			streams := make([]net.Conn, 0, tt.limit)
			for range tt.limit {
				c, err := tt.opener.Open(ctx)
				if err != nil {
					t.Fatalf("Open %d: %v", len(streams), err)
				}
				streams = append(streams, c)
			}
			short, cancelShort := context.WithTimeout(ctx, 50*time.Millisecond)
			defer cancelShort()
			if c, err := tt.opener.Open(short); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("Open beyond the limit = %v, %v; want it to wait until the context ends", c, err)
			}
			if err := streams[0].Close(); err != nil {
				t.Fatal(err)
			}
			c, err := tt.opener.Open(ctx)
			if err != nil {
				t.Fatalf("Open after a stream closed: %v", err)
			}
			streams = append(streams[1:], c)
			for _, c := range streams {
				_ = c.Close()
			}
		})
	}
}

func TestSessionAcceptLimit(t *testing.T) {
	ctrl, agent := sessionPair(t)
	// Open raw streams so the agent's own Open limit does not hide the
	// controller's Accept limit.
	raw := map[uint32]net.Conn{}
	for range MaxAgentStreams + 1 {
		st, err := agent.mux.OpenStream()
		if err != nil {
			t.Fatal(err)
		}
		raw[st.StreamID()] = st
	}
	accepted := make([]net.Conn, 0, MaxAgentStreams)
	for range MaxAgentStreams {
		c, err := ctrl.Accept()
		if err != nil {
			t.Fatal(err)
		}
		accepted = append(accepted, c)
	}
	extra := make(chan net.Conn, 1)
	go func() {
		c, err := ctrl.Accept()
		if err == nil {
			extra <- c
		}
	}()
	select {
	case <-extra:
		t.Fatal("Accept returned a stream beyond the limit")
	case <-time.After(100 * time.Millisecond):
	}
	// A slot frees once both ends have closed the stream.
	first := accepted[0].(*stream)
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if err := raw[first.StreamID()].Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case c := <-extra:
		_ = c.Close()
	case <-time.After(5 * time.Second):
		t.Fatal("Accept did not return the waiting stream after a slot freed")
	}
}

func TestSessionHalfOpenStreamHoldsSlot(t *testing.T) {
	ctrl, agent := sessionPair(t)
	held := make(chan net.Conn, MaxAgentStreams)
	go func() {
		for {
			c, err := ctrl.Accept()
			if err != nil {
				return
			}
			held <- c // never closed until the test says so
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for range MaxAgentStreams {
		c, err := agent.Open(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
	}
	short, cancelShort := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancelShort()
	if c, err := agent.Open(short); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Open while the peer holds every stream half-open = %v, %v; want it to wait", c, err)
	}
	if err := (<-held).Close(); err != nil {
		t.Fatal(err)
	}
	c, err := agent.Open(ctx)
	if err != nil {
		t.Fatalf("Open after the peer closed a stream: %v", err)
	}
	_ = c.Close()
}

func TestSessionClosed(t *testing.T) {
	ctrl, agent := sessionPair(t)
	if err := ctrl.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctrl.Done():
	default:
		t.Fatal("Done is open after Close")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := ctrl.Open(ctx); err == nil {
		t.Fatal("Open on a closed session succeeded")
	}
	if _, err := ctrl.Accept(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Accept on a closed session = %v, want net.ErrClosed", err)
	}
	select {
	case <-agent.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the peer did not see the session close")
	}
}
