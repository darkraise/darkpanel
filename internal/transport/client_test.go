package transport

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"testing"
	"time"
)

// agentDialer dials with AgentConfig.
func agentDialer(cert tls.Certificate, ca *x509.Certificate, san string) dialer {
	return func(ctx context.Context, addr string) (*tls.Conn, error) {
		return Dial(ctx, addr, AgentConfig(cert, ca, san))
	}
}

// enrollDialer dials with EnrollConfig.
func enrollDialer(fingerprint, san string) dialer {
	return func(ctx context.Context, addr string) (*tls.Conn, error) {
		return Dial(ctx, addr, EnrollConfig(fingerprint, san))
	}
}

func TestAgentConfigAccepts(t *testing.T) {
	ca := newTestCA(t)
	cfg := serverConfig(ca, ca.controllerCert(t, "01JCTRL"))
	r := handshake(t, cfg, agentDialer(ca.agentCert(t, "01JHOST"), ca.cert, ControllerURI("01JCTRL")))
	if r.serverErr != nil || r.clientErr != nil {
		t.Fatalf("handshake: server %v, client %v", r.serverErr, r.clientErr)
	}
	if r.peer.HostID != "01JHOST" || r.client.ConnectionState().NegotiatedProtocol != ALPNAgent {
		t.Errorf("peer %+v, protocol %q", r.peer, r.client.ConnectionState().NegotiatedProtocol)
	}
}

func TestAgentConfigRefuses(t *testing.T) {
	ca := newTestCA(t)
	other := newTestCA(t)
	serverAuth := []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	tests := []struct {
		name string
		cert tls.Certificate
	}{
		{"another authority", other.controllerCert(t, "01JCTRL")},
		{"missing SAN", ca.issue(t, certSpec{eku: serverAuth})},
		{"different SAN", ca.controllerCert(t, "01JOTHER")},
		{"two SANs", ca.issue(t, certSpec{uris: []string{ControllerURI("01JCTRL"), ControllerURI("01JOTHER")}, eku: serverAuth})},
		{"clientAuth only", ca.issue(t, certSpec{uris: []string{ControllerURI("01JCTRL")}, eku: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}})},
		{"no extended key usage", ca.issue(t, certSpec{uris: []string{ControllerURI("01JCTRL")}})},
		{"expired", ca.issue(t, certSpec{uris: []string{ControllerURI("01JCTRL")}, eku: serverAuth, notAfter: time.Now().Add(-time.Minute)})},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := handshake(t, serverConfig(ca, tt.cert), agentDialer(ca.agentCert(t, "01JHOST"), ca.cert, ControllerURI("01JCTRL")))
			if r.clientErr == nil {
				t.Fatal("the agent accepted the controller")
			}
			t.Logf("refused: %v", r.clientErr)
		})
	}
}

// serveRaw accepts one connection with a plain TLS server using cfg and
// returns the server's connection state once its handshake ends.
func serveRaw(t *testing.T, cfg *tls.Config, dial dialer) (tls.ConnectionState, error, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	type served struct {
		state tls.ConnectionState
		err   error
	}
	done := make(chan served, 1)
	go func() {
		raw, err := ln.Accept()
		if err != nil {
			done <- served{err: err}
			return
		}
		conn := tls.Server(raw, cfg)
		t.Cleanup(func() { _ = conn.Close() })
		err = conn.HandshakeContext(ctx)
		done <- served{conn.ConnectionState(), err}
	}()
	client, clientErr := dial(ctx, ln.Addr().String())
	if client != nil {
		t.Cleanup(func() { _ = client.Close() })
	}
	s := <-done
	return s.state, s.err, clientErr
}

func TestAgentConfigRequiresProtocol(t *testing.T) {
	ca := newTestCA(t)
	cfg := &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{ca.controllerCert(t, "01JCTRL")},
		ClientAuth:   tls.RequireAnyClientCert,
	}
	_, _, clientErr := serveRaw(t, cfg, agentDialer(ca.agentCert(t, "01JHOST"), ca.cert, ControllerURI("01JCTRL")))
	if clientErr == nil {
		t.Fatal("the agent accepted a controller that negotiated no protocol")
	}
}

func TestEnrollConfig(t *testing.T) {
	ca := newTestCA(t)
	other := newTestCA(t)
	san := ControllerURI("01JCTRL")
	good := ca.controllerCert(t, "01JCTRL")
	// A leaf from another authority, presented with the pinned root.
	foreign := other.controllerCert(t, "01JCTRL")
	foreign.Certificate = [][]byte{foreign.Certificate[0], ca.cert.Raw}
	tests := []struct {
		name        string
		cert        tls.Certificate
		fingerprint string
		san         string
		ok          bool
	}{
		{"pinned root", good, Fingerprint(ca.cert), san, true},
		{"wrong fingerprint", good, Fingerprint(other.cert), san, false},
		{"different SAN", good, Fingerprint(ca.cert), ControllerURI("01JOTHER"), false},
		{"leaf of another authority", foreign, Fingerprint(ca.cert), san, false},
		{"clientAuth only", ca.issue(t, certSpec{uris: []string{san}, eku: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}), Fingerprint(ca.cert), san, false},
		{"root alone", tls.Certificate{Certificate: [][]byte{ca.cert.Raw}, PrivateKey: ca.key}, Fingerprint(ca.cert), san, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := handshake(t, serverConfig(ca, tt.cert), enrollDialer(tt.fingerprint, tt.san))
			if tt.ok != (r.clientErr == nil) {
				t.Fatalf("client error %v, want ok=%v", r.clientErr, tt.ok)
			}
			if tt.ok && (r.serverErr != nil || r.peer.Protocol != ALPNEnroll) {
				t.Fatalf("server %v, peer %+v", r.serverErr, r.peer)
			}
			if !tt.ok {
				t.Logf("refused: %v", r.clientErr)
			}
		})
	}
}

func TestEnrollConfigSendsNoCertificate(t *testing.T) {
	ca := newTestCA(t)
	cfg := &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{ca.controllerCert(t, "01JCTRL")},
		NextProtos:   []string{ALPNEnroll},
		ClientAuth:   tls.RequestClientCert,
	}
	state, serverErr, clientErr := serveRaw(t, cfg, enrollDialer(Fingerprint(ca.cert), ControllerURI("01JCTRL")))
	if serverErr != nil || clientErr != nil {
		t.Fatalf("handshake: server %v, client %v", serverErr, clientErr)
	}
	if len(state.PeerCertificates) != 0 {
		t.Fatalf("the enrolling client sent %d certificates", len(state.PeerCertificates))
	}
}

func TestDialFails(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := Dial(ctx, addr, EnrollConfig(Fingerprint(newTestCA(t).cert), ControllerURI("C1"))); err == nil {
		t.Fatal("Dial to a closed port succeeded")
	}
}

func TestBackoffCeilings(t *testing.T) {
	b := NewBackoff()
	b.rand = func(n int64) int64 { return n - 1 }
	want := []time.Duration{1, 2, 4, 8, 16, 32, 60, 60, 60}
	for i, w := range want {
		if got := b.Next(); got != w*time.Second {
			t.Fatalf("call %d: Next = %v, want %v", i, got, w*time.Second)
		}
	}
	b.Reset()
	if got := b.Next(); got != time.Second {
		t.Fatalf("after Reset: Next = %v, want 1s", got)
	}
}

func TestBackoffJitter(t *testing.T) {
	b := NewBackoff()
	var low bool
	for i := range 200 {
		ceiling := min(BackoffMax, BackoffMin<<min(i, 6))
		d := b.Next()
		if d < 0 || d > ceiling {
			t.Fatalf("call %d: Next = %v, outside [0, %v]", i, d, ceiling)
		}
		low = low || d < ceiling/2
	}
	if !low {
		t.Error("no delay fell in the lower half: the jitter is not full")
	}
}
