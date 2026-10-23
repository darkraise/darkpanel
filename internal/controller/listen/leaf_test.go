package listen

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/netip"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core/coretest"
)

// newLeaf returns a leaf manager for host "panel" on a fresh authority, with
// the authority and the fake clock it reads.
func newLeaf(t *testing.T) (*Leaf, *Authority, *coretest.FakeClock) {
	t.Helper()
	clock := coretest.NewFakeClock(testStart)
	a, err := LoadOrCreateAuthority(context.Background(), openControllerDB(t), newSealer(t), clock)
	if err != nil {
		t.Fatal(err)
	}
	return NewLeaf(a, clock, "panel"), a, clock
}

func current(t *testing.T, l *Leaf) *tls.Certificate {
	t.Helper()
	c, err := l.GetCertificate(&tls.ClientHelloInfo{})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestLeafSANsAndChain(t *testing.T) {
	l, a, _ := newLeaf(t)
	if _, err := l.GetCertificate(&tls.ClientHelloInfo{}); err == nil {
		t.Fatal("GetCertificate before Ensure returned a certificate")
	}
	issued, err := l.Ensure(context.Background(), addrs("fd7a:115c:a1e0::1", "192.168.1.5", "100.64.0.1", "192.168.1.5"))
	if err != nil || !issued {
		t.Fatalf("Ensure = %v, %v; want true, nil", issued, err)
	}
	c := current(t, l)
	if len(c.Certificate) != 2 || !reflect.DeepEqual(c.Certificate[1], rootCert(t, a).Raw) {
		t.Fatal("the chain is not [leaf, root]")
	}
	leaf, err := x509.ParseCertificate(c.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(leaf.DNSNames, []string{"panel", "localhost"}) {
		t.Fatalf("DNS SANs = %v", leaf.DNSNames)
	}
	var ips []string
	for _, ip := range leaf.IPAddresses {
		ips = append(ips, ip.String())
	}
	if !reflect.DeepEqual(ips, []string{"100.64.0.1", "192.168.1.5", "fd7a:115c:a1e0::1"}) {
		t.Fatalf("IP SANs = %v", ips)
	}
	pub, ok := leaf.PublicKey.(*ecdsa.PublicKey)
	if !ok || pub.Curve != elliptic.P256() || leaf.IsCA {
		t.Fatalf("leaf key %T, IsCA %v; want an ECDSA P-256 end-entity certificate", leaf.PublicKey, leaf.IsCA)
	}
	if !leaf.NotBefore.Equal(testStart.Add(-time.Hour)) || !leaf.NotAfter.Equal(testStart.Add(90*24*time.Hour)) {
		t.Fatalf("leaf validity %v to %v", leaf.NotBefore, leaf.NotAfter)
	}
	if leaf.KeyUsage != x509.KeyUsageDigitalSignature || !reflect.DeepEqual(leaf.ExtKeyUsage, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}) {
		t.Fatalf("leaf usage %v %v", leaf.KeyUsage, leaf.ExtKeyUsage)
	}
	roots := x509.NewCertPool()
	roots.AddCert(rootCert(t, a))
	for _, name := range []string{"panel", "localhost", "100.64.0.1", "fd7a:115c:a1e0::1"} {
		if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots, DNSName: name, CurrentTime: testStart}); err != nil {
			t.Errorf("verify for %s: %v", name, err)
		}
	}
}

func TestLeafRenewal(t *testing.T) {
	l, _, clock := newLeaf(t)
	ips := addrs("100.64.0.1")
	if _, err := l.Ensure(context.Background(), ips); err != nil {
		t.Fatal(err)
	}
	first := current(t, l)
	clock.Advance(LeafRenewAfter - time.Second)
	if issued, err := l.Ensure(context.Background(), ips); err != nil || issued || current(t, l) != first {
		t.Fatalf("Ensure before 60 days = %v, %v; want the same leaf", issued, err)
	}
	clock.Advance(time.Second)
	if issued, err := l.Ensure(context.Background(), ips); err != nil || !issued || current(t, l) == first {
		t.Fatalf("Ensure at 60 days = %v, %v; want a new leaf", issued, err)
	}
	second := current(t, l)
	if second.Leaf.NotAfter != clock.Now().Add(LeafValidity) {
		t.Fatalf("renewed leaf expires %v", second.Leaf.NotAfter)
	}
	if issued, err := l.Ensure(context.Background(), addrs("100.64.0.2")); err != nil || !issued || current(t, l) == second {
		t.Fatalf("Ensure after an address change = %v, %v; want a new leaf", issued, err)
	}
	if issued, err := l.Ensure(context.Background(), addrs("100.64.0.2", "100.64.0.2")); err != nil || issued {
		t.Fatalf("Ensure with the same set = %v, %v; want no new leaf", issued, err)
	}
}

func TestLeafHandshake(t *testing.T) {
	l, a, clock := newLeaf(t)
	if _, err := l.Ensure(context.Background(), addrs("127.0.0.1")); err != nil {
		t.Fatal(err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", l.TLSConfig())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_ = conn.(*tls.Conn).Handshake()
			_ = conn.Close()
		}
	}()
	roots := x509.NewCertPool()
	roots.AddCert(rootCert(t, a))
	dial := func(cfg *tls.Config) (*tls.Conn, error) {
		cfg.RootCAs = roots
		cfg.Time = clock.Now
		d := &tls.Dialer{NetDialer: &net.Dialer{Timeout: 5 * time.Second}, Config: cfg}
		conn, err := d.DialContext(context.Background(), "tcp", ln.Addr().String())
		if err != nil {
			return nil, err
		}
		return conn.(*tls.Conn), nil
	}
	for _, name := range []string{"", "localhost", "panel"} {
		conn, err := dial(&tls.Config{ServerName: name})
		if err != nil {
			t.Fatalf("handshake with ServerName %q: %v", name, err)
		}
		if n := len(conn.ConnectionState().PeerCertificates); n != 2 {
			t.Fatalf("server sent %d certificates, want 2", n)
		}
		_ = conn.Close()
	}
	var unknown x509.HostnameError
	if _, err := dial(&tls.Config{ServerName: "other.example"}); !errors.As(err, &unknown) {
		t.Fatalf("handshake for a name outside the SANs: %v, want a hostname error", err)
	}
	if _, err := dial(&tls.Config{MinVersion: tls.VersionTLS10, MaxVersion: tls.VersionTLS11}); err == nil { //nolint:gosec // G402: an old client must be refused
		t.Fatal("a TLS 1.1 client completed the handshake")
	}
	if cfg := l.TLSConfig(); cfg.MinVersion != tls.VersionTLS12 {
		t.Fatalf("MinVersion = %x, want TLS 1.2", cfg.MinVersion)
	}
}

func TestLeafConcurrentReads(t *testing.T) {
	l, _, clock := newLeaf(t)
	if _, err := l.Ensure(context.Background(), addrs("100.64.0.1")); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for range 4 {
		wg.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
					if _, err := l.GetCertificate(&tls.ClientHelloInfo{}); err != nil {
						t.Error(err)
						return
					}
				}
			}
		})
	}
	for i := range 20 {
		clock.Advance(LeafRenewAfter)
		if _, err := l.Ensure(context.Background(), []netip.Addr{netip.AddrFrom4([4]byte{100, 64, 0, byte(i)})}); err != nil {
			t.Error(err)
		}
	}
	close(stop)
	wg.Wait()
}
