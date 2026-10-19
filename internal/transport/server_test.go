package transport

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// rawClient is a client that checks nothing about the controller, so these
// tests exercise the controller's policy alone.
func rawClient(protos []string, cert *tls.Certificate, maxVersion uint16) dialer {
	cfg := &tls.Config{
		InsecureSkipVerify: true, //nolint:gosec // G402: the controller's policy is under test
		NextProtos:         protos,
		MinVersion:         tls.VersionTLS12,
		MaxVersion:         maxVersion,
	}
	if cert != nil {
		c := *cert
		cfg.GetClientCertificate = func(*tls.CertificateRequestInfo) (*tls.Certificate, error) { return &c, nil }
	}
	return tlsDialer(cfg)
}

type ctxKey struct{}

func TestHandshakeAgent(t *testing.T) {
	ca := newTestCA(t)
	cert := ca.agentCert(t, "01JHOST")
	cfg := serverConfig(ca, ca.controllerCert(t, "01JCTRL"))
	var gotHost, gotSerial string
	cfg.CheckPeer = func(_ context.Context, hostID, serial string) error {
		gotHost, gotSerial = hostID, serial
		return nil
	}
	r := handshake(t, cfg, rawClient([]string{ALPNAgent}, &cert, 0))
	if r.serverErr != nil || r.clientErr != nil {
		t.Fatalf("handshake: server %v, client %v", r.serverErr, r.clientErr)
	}
	serial := SerialText(cert.Leaf.SerialNumber)
	if r.peer.Protocol != ALPNAgent || r.peer.HostID != "01JHOST" || r.peer.Serial != serial {
		t.Errorf("peer = %+v, want agent 01JHOST %s", r.peer, serial)
	}
	if gotHost != "01JHOST" || gotSerial != serial {
		t.Errorf("CheckPeer got %q %q", gotHost, gotSerial)
	}
	if v := r.peer.Conn.ConnectionState().Version; v != tls.VersionTLS13 {
		t.Errorf("version = %x, want TLS 1.3", v)
	}
}

func TestHandshakeCheckPeerContext(t *testing.T) {
	ca := newTestCA(t)
	cert := ca.agentCert(t, "01JHOST")
	cfg := serverConfig(ca, ca.controllerCert(t, "01JCTRL"))
	var got any
	cfg.CheckPeer = func(ctx context.Context, _, _ string) error {
		got = ctx.Value(ctxKey{})
		return nil
	}
	ctx := context.WithValue(context.Background(), ctxKey{}, "marker")
	r := handshakeCtx(ctx, t, cfg, rawClient([]string{ALPNAgent}, &cert, 0))
	if r.serverErr != nil {
		t.Fatal(r.serverErr)
	}
	if got != "marker" {
		t.Errorf("CheckPeer context value = %v, want the Handshake context's", got)
	}
}

func TestHandshakeNoResumption(t *testing.T) {
	ca := newTestCA(t)
	cert := ca.agentCert(t, "01JHOST")
	cfg := serverConfig(ca, ca.controllerCert(t, "01JCTRL"))
	checks := 0
	cfg.CheckPeer = func(context.Context, string, string) error {
		checks++
		return nil
	}
	client := &tls.Config{
		InsecureSkipVerify: true, //nolint:gosec // G402: the controller's policy is under test
		NextProtos:         []string{ALPNAgent},
		MinVersion:         tls.VersionTLS13,
		// A fixed server name keys the session cache across the two listeners.
		ServerName:           "controller",
		ClientSessionCache:   tls.NewLRUClientSessionCache(4),
		GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) { return &cert, nil },
	}
	for i := range 2 {
		r := handshake(t, cfg, tlsDialer(client))
		if r.serverErr != nil || r.clientErr != nil {
			t.Fatalf("handshake %d: server %v, client %v", i, r.serverErr, r.clientErr)
		}
		if r.client.ConnectionState().DidResume {
			t.Fatalf("handshake %d resumed a session, skipping the certificate checks", i)
		}
		// A read processes any session ticket the controller sent.
		if err := r.client.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
			t.Fatal(err)
		}
		_, _ = r.client.Read(make([]byte, 1))
	}
	if checks != 2 {
		t.Errorf("CheckPeer ran %d times, want 2", checks)
	}
}

func TestHandshakeEnroll(t *testing.T) {
	ca := newTestCA(t)
	cfg := serverConfig(ca, ca.controllerCert(t, "01JCTRL"))
	cfg.CheckPeer = func(context.Context, string, string) error {
		t.Error("CheckPeer called for an enrollment connection")
		return nil
	}
	asked := false
	client := &tls.Config{
		InsecureSkipVerify: true, //nolint:gosec // G402: the controller's policy is under test
		NextProtos:         []string{ALPNEnroll},
		MinVersion:         tls.VersionTLS13,
		GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			asked = true
			return &tls.Certificate{}, nil
		},
	}
	r := handshake(t, cfg, tlsDialer(client))
	if r.serverErr != nil || r.clientErr != nil {
		t.Fatalf("handshake: server %v, client %v", r.serverErr, r.clientErr)
	}
	if r.peer.Protocol != ALPNEnroll || r.peer.HostID != "" || r.peer.Serial != "" {
		t.Errorf("peer = %+v, want an anonymous enrollment peer", r.peer)
	}
	if asked {
		t.Error("the controller asked an enrolling client for a certificate")
	}
}

func TestHandshakeRefuses(t *testing.T) {
	ca := newTestCA(t)
	other := newTestCA(t)
	clientAuth := []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	valid := ca.agentCert(t, "01JHOST")
	tests := []struct {
		name       string
		protos     []string
		cert       *tls.Certificate
		maxVersion uint16
		refuse     bool // CheckPeer refuses
	}{
		{name: "both protocols", protos: []string{ALPNAgent, ALPNEnroll}, cert: &valid},
		{name: "no protocol", protos: nil, cert: &valid},
		{name: "another protocol", protos: []string{"h2"}, cert: &valid},
		{name: "no client certificate", protos: []string{ALPNAgent}},
		{name: "another authority", protos: []string{ALPNAgent}, cert: ptr(other.agentCert(t, "01JHOST"))},
		{name: "serverAuth only", protos: []string{ALPNAgent},
			cert: ptr(ca.issue(t, certSpec{uris: []string{HostURI("01JHOST")}, eku: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}))},
		{name: "no extended key usage", protos: []string{ALPNAgent},
			cert: ptr(ca.issue(t, certSpec{uris: []string{HostURI("01JHOST")}}))},
		{name: "no URI", protos: []string{ALPNAgent}, cert: ptr(ca.issue(t, certSpec{eku: clientAuth}))},
		{name: "two URIs", protos: []string{ALPNAgent},
			cert: ptr(ca.issue(t, certSpec{uris: []string{HostURI("A1"), HostURI("B2")}, eku: clientAuth}))},
		{name: "controller URI", protos: []string{ALPNAgent},
			cert: ptr(ca.issue(t, certSpec{uris: []string{ControllerURI("01JCTRL")}, eku: clientAuth}))},
		{name: "DNS name too", protos: []string{ALPNAgent},
			cert: ptr(ca.issue(t, certSpec{uris: []string{HostURI("01JHOST")}, dns: []string{"agent.example"}, eku: clientAuth}))},
		{name: "expired", protos: []string{ALPNAgent},
			cert: ptr(ca.issue(t, certSpec{uris: []string{HostURI("01JHOST")}, eku: clientAuth, notAfter: time.Now().Add(-time.Minute)}))},
		{name: "refused by the peer check", protos: []string{ALPNAgent}, cert: &valid, refuse: true},
		{name: "TLS 1.2", protos: []string{ALPNAgent}, cert: &valid, maxVersion: tls.VersionTLS12},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := serverConfig(ca, ca.controllerCert(t, "01JCTRL"))
			if tt.refuse {
				cfg.CheckPeer = func(context.Context, string, string) error {
					return errcode.New(errcode.Forbidden, "certificate revoked")
				}
			}
			r := handshake(t, cfg, rawClient(tt.protos, tt.cert, tt.maxVersion))
			if r.serverErr == nil {
				t.Fatalf("Handshake accepted the client: %+v", r.peer)
			}
			t.Logf("refused: %v", r.serverErr)
			if r.client == nil {
				return
			}
			// The controller closed the connection: a read ends at once.
			if err := r.client.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
				t.Fatal(err)
			}
			if _, err := r.client.Read(make([]byte, 1)); err == nil {
				t.Fatal("the refused connection is still open")
			}
		})
	}
}

func ptr[T any](v T) *T { return &v }
