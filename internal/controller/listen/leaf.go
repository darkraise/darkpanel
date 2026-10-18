package listen

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
)

// Leaf lifetimes (ADR 0010).
const (
	LeafValidity   = 90 * 24 * time.Hour
	LeafRenewAfter = 60 * 24 * time.Hour
)

// Leaf holds the HTTPS listeners' certificate, issued by the web authority
// and kept in memory only. Its SANs are the hostname, localhost and the IP
// addresses passed to Ensure.
type Leaf struct {
	auth     *Authority
	clock    core.Clock
	hostname string
	mu       sync.Mutex // serialises Ensure
	cur      atomic.Pointer[issuedLeaf]
}

type issuedLeaf struct {
	cert *tls.Certificate
	ips  []netip.Addr
	at   time.Time
}

// NewLeaf returns a leaf manager for hostname; no certificate exists until
// the first Ensure.
func NewLeaf(a *Authority, clock core.Clock, hostname string) *Leaf {
	return &Leaf{auth: a, clock: clock, hostname: hostname}
}

// Ensure issues a new leaf when none exists, when ips differ from the current
// leaf's IP SANs, or when LeafRenewAfter has passed since the current one was
// issued. It reports whether it issued one.
func (l *Leaf) Ensure(ips []netip.Addr) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	want := normalizeAddrs(ips)
	now := l.clock.Now()
	if cur := l.cur.Load(); cur != nil && slices.Equal(cur.ips, want) && now.Sub(cur.at) < LeafRenewAfter {
		return false, nil
	}
	cert, err := l.auth.issue(l.hostname, want, now)
	if err != nil {
		return false, err
	}
	l.cur.Store(&issuedLeaf{cert: cert, ips: want, at: now})
	return true, nil
}

// GetCertificate serves tls.Config.GetCertificate with the chain
// [leaf, root].
func (l *Leaf) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	cur := l.cur.Load()
	if cur == nil {
		return nil, errors.New("listen: no web certificate issued yet")
	}
	return cur.cert, nil
}

// TLSConfig returns the HTTPS listeners' configuration: TLS 1.2 or later and
// the current leaf.
func (l *Leaf) TLSConfig() *tls.Config {
	return &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: l.GetCertificate}
}

func normalizeAddrs(list []netip.Addr) []netip.Addr {
	out := make([]netip.Addr, 0, len(list))
	for _, a := range list {
		out = append(out, a.Unmap())
	}
	slices.SortFunc(out, netip.Addr.Compare)
	return slices.Compact(out)
}

func (a *Authority) issue(hostname string, ips []netip.Addr, now time.Time) (*tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("listen: web leaf: generate key: %w", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, err
	}
	names := []string{"localhost"}
	if hostname != "" && hostname != "localhost" {
		names = []string{hostname, "localhost"}
	}
	ipSANs := make([]net.IP, 0, len(ips))
	for _, ip := range ips {
		ipSANs = append(ipSANs, ip.AsSlice())
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: names[0]},
		DNSNames:              names,
		IPAddresses:           ipSANs,
		NotBefore:             now.Add(-backdate),
		NotAfter:              now.Add(LeafValidity),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, a.cert, &key.PublicKey, a.key)
	if err != nil {
		return nil, fmt.Errorf("listen: web leaf: %w", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("listen: web leaf: %w", err)
	}
	return &tls.Certificate{Certificate: [][]byte{der, a.cert.Raw}, PrivateKey: key, Leaf: leaf}, nil
}
