package listen

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"log/slog"
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
// and kept in memory only. Its SANs are the hostname, localhost, the extra
// names of the certificate sources and the IP addresses passed to Ensure. A
// handshake asks the certificate sources first and falls back to it.
type Leaf struct {
	auth     *Authority
	clock    core.Clock
	hostname string
	sources  []CertSource
	names    func(ctx context.Context) ([]string, error) // nil: no extra names
	log      *slog.Logger
	mu       sync.Mutex // serialises Ensure
	cur      atomic.Pointer[issuedLeaf]
}

type issuedLeaf struct {
	cert  *tls.Certificate
	ips   []netip.Addr
	names []string // the extra names, sorted
	at    time.Time
}

// NewLeaf returns a leaf manager for hostname without certificate sources;
// no certificate exists until the first Ensure.
func NewLeaf(a *Authority, clock core.Clock, hostname string) *Leaf {
	return &Leaf{auth: a, clock: clock, hostname: hostname, log: slog.New(slog.DiscardHandler)}
}

// useSources makes the leaf ask d's certificate sources at every handshake
// and add their extra names at every issuance.
func (l *Leaf) useSources(d SourceDeps) {
	if d.Log != nil {
		l.log = d.Log
	}
	l.sources = certSources(d)
	l.names = func(ctx context.Context) ([]string, error) { return extraNames(ctx, d) }
}

// Ensure issues a new leaf when none exists, when ips or the sources' extra
// names differ from the current leaf's, or when LeafRenewAfter has passed
// since the current one was issued. It reports whether it issued one. When
// the extra names cannot be read, the current leaf's names are kept, so a
// failing source never takes the listeners down.
func (l *Leaf) Ensure(ctx context.Context, ips []netip.Addr) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	want := normalizeAddrs(ips)
	cur := l.cur.Load()
	names := l.extraNames(ctx, cur)
	now := l.clock.Now()
	if cur != nil && slices.Equal(cur.ips, want) && slices.Equal(cur.names, names) && now.Sub(cur.at) < LeafRenewAfter {
		return false, nil
	}
	cert, err := l.auth.issue(l.hostname, names, want, now)
	if err != nil {
		return false, err
	}
	l.cur.Store(&issuedLeaf{cert: cert, ips: want, names: names, at: now})
	return true, nil
}

// extraNames reads the sources' extra names, sorted, without duplicates and
// without the names every leaf carries. On an error it logs and returns the
// names of cur.
func (l *Leaf) extraNames(ctx context.Context, cur *issuedLeaf) []string {
	if l.names == nil {
		return nil
	}
	list, err := l.names(ctx)
	if err != nil {
		l.log.Warn("listen: the certificate sources' names are unavailable; keeping the current names", "err", err)
		if cur == nil {
			return nil
		}
		return cur.names
	}
	out := make([]string, 0, len(list))
	for _, n := range list {
		if n != "" && n != "localhost" && n != l.hostname {
			out = append(out, n)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// GetCertificate serves tls.Config.GetCertificate: the first certificate a
// source supplies, else the chain [leaf, root]. A source that fails is
// logged and skipped.
func (l *Leaf) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	for _, src := range l.sources {
		cert, err := src.Certificate(hello)
		if err != nil {
			l.log.Warn("listen: a certificate source failed; trying the next", "server_name", hello.ServerName, "err", err)
			continue
		}
		if cert != nil {
			return cert, nil
		}
	}
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

func (a *Authority) issue(hostname string, extra []string, ips []netip.Addr, now time.Time) (*tls.Certificate, error) {
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
	names = append(names, extra...)
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
