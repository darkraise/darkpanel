package transport

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"net/url"
	"testing"
	"time"
)

// testCA is a throwaway Ed25519 authority for the TLS tests.
type testCA struct {
	cert *x509.Certificate
	key  ed25519.PrivateKey
}

func newTestCA(t *testing.T) *testCA {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test root"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, pub, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return &testCA{cert: cert, key: key}
}

// pool holds the root alone.
func (ca *testCA) pool() *x509.CertPool {
	p := x509.NewCertPool()
	p.AddCert(ca.cert)
	return p
}

// certSpec describes a leaf the test authority issues.
type certSpec struct {
	uris     []string
	dns      []string
	eku      []x509.ExtKeyUsage
	notAfter time.Time // zero: an hour from now
}

// issue returns the chain [leaf, root] with the leaf's key.
func (ca *testCA) issue(t *testing.T, s certSpec) tls.Certificate {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatal(err)
	}
	notAfter := s.notAfter
	if notAfter.IsZero() {
		notAfter = time.Now().Add(time.Hour)
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		NotBefore:    time.Now().Add(-2 * time.Hour),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  s.eku,
		DNSNames:     s.dns,
	}
	for _, raw := range s.uris {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		tmpl.URIs = append(tmpl.URIs, u)
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, pub, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der, ca.cert.Raw}, PrivateKey: key, Leaf: leaf}
}

// agentCert is a valid agent certificate for hostID.
func (ca *testCA) agentCert(t *testing.T, hostID string) tls.Certificate {
	return ca.issue(t, certSpec{uris: []string{HostURI(hostID)}, eku: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}})
}

// controllerCert is a valid agent-listener certificate for controllerID.
func (ca *testCA) controllerCert(t *testing.T, controllerID string) tls.Certificate {
	return ca.issue(t, certSpec{uris: []string{ControllerURI(controllerID)}, eku: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}})
}

// serverConfig is a controller configuration presenting cert, trusting
// ca's agents and admitting every peer.
func serverConfig(ca *testCA, cert tls.Certificate) ServerConfig {
	return ServerConfig{
		Certificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) { return &cert, nil },
		AgentRoots:  ca.pool(),
		CheckPeer:   func(context.Context, string, string) error { return nil },
	}
}

// dialer opens a client connection to addr.
type dialer func(ctx context.Context, addr string) (*tls.Conn, error)

// tlsDialer dials with cfg.
func tlsDialer(cfg *tls.Config) dialer {
	return func(ctx context.Context, addr string) (*tls.Conn, error) {
		d := tls.Dialer{Config: cfg}
		c, err := d.DialContext(ctx, "tcp", addr)
		if err != nil {
			return nil, err
		}
		return c.(*tls.Conn), nil
	}
}

// handshakeResult is what each end of one test handshake saw.
type handshakeResult struct {
	peer      *Peer
	serverErr error
	client    *tls.Conn
	clientErr error
}

// handshake runs Handshake on one loopback connection that dial opens and
// closes both ends when the test ends.
func handshake(t *testing.T, cfg ServerConfig, dial dialer) handshakeResult {
	t.Helper()
	return handshakeCtx(context.Background(), t, cfg, dial)
}

// handshakeCtx is handshake with the server's Handshake run within ctx.
func handshakeCtx(ctx context.Context, t *testing.T, cfg ServerConfig, dial dialer) handshakeResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	type served struct {
		peer *Peer
		err  error
	}
	done := make(chan served, 1)
	go func() {
		raw, err := ln.Accept()
		if err != nil {
			done <- served{nil, err}
			return
		}
		p, err := Handshake(ctx, raw, cfg)
		done <- served{p, err}
	}()
	var r handshakeResult
	r.client, r.clientErr = dial(ctx, ln.Addr().String())
	s := <-done
	r.peer, r.serverErr = s.peer, s.err
	t.Cleanup(func() {
		if r.client != nil {
			_ = r.client.Close()
		}
		if r.peer != nil {
			_ = r.peer.Conn.Close()
		}
	})
	return r
}
