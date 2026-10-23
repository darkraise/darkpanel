package listen

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"
)

// csSource is a certificate source that answers cert and err.
type csSource struct {
	cert  *tls.Certificate
	err   error
	calls int
}

func (s *csSource) Certificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	s.calls++
	return s.cert, s.err
}

// csDNSNames are the DNS SANs of l's current CA leaf.
func csDNSNames(t *testing.T, l *Leaf) []string {
	t.Helper()
	cur := l.cur.Load()
	if cur == nil {
		t.Fatal("no leaf issued")
	}
	leaf, err := x509.ParseCertificate(cur.cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	return leaf.DNSNames
}

func TestLeafAsksSourcesInOrder(t *testing.T) {
	l, _, _ := newLeaf(t)
	log := &logBuffer{}
	l.log = slog.New(slog.NewTextHandler(log, nil))
	if _, err := l.Ensure(context.Background(), addrs("127.0.0.1")); err != nil {
		t.Fatal(err)
	}
	ca := current(t, l)
	failing := &csSource{err: errors.New("tailscaled is down")}
	silent := &csSource{}
	own := &csSource{cert: &tls.Certificate{Certificate: [][]byte{{1}}}}
	later := &csSource{cert: &tls.Certificate{Certificate: [][]byte{{2}}}}

	l.sources = []CertSource{failing, silent, own, later}
	got, err := l.GetCertificate(&tls.ClientHelloInfo{ServerName: "panel.example.ts.net"})
	if err != nil || got != own.cert {
		t.Fatalf("GetCertificate = %v, %v; want the first certificate a source supplies", got, err)
	}
	if failing.calls != 1 || silent.calls != 1 || later.calls != 0 {
		t.Errorf("source calls %d, %d, %d; want 1, 1, 0", failing.calls, silent.calls, later.calls)
	}
	if !strings.Contains(log.String(), "a certificate source failed") {
		t.Errorf("the failing source was not logged: %s", log.String())
	}

	l.sources = []CertSource{failing, silent}
	if got, err := l.GetCertificate(&tls.ClientHelloInfo{}); err != nil || got != ca {
		t.Fatalf("GetCertificate = %v, %v; want the CA leaf when every source falls through", got, err)
	}
}

func TestLeafExtraNames(t *testing.T) {
	ctx := context.Background()
	l, _, _ := newLeaf(t)
	l.log = slog.New(slog.DiscardHandler)
	var names []string
	var namesErr error
	l.names = func(context.Context) ([]string, error) { return names, namesErr }

	// The names fail before any leaf exists: the leaf is issued without them.
	namesErr = errors.New("tailscaled is down")
	if issued, err := l.Ensure(ctx, addrs("100.64.0.1")); err != nil || !issued {
		t.Fatalf("Ensure = %v, %v; want a leaf despite the failing names", issued, err)
	}
	if got := csDNSNames(t, l); !slices.Equal(got, []string{"panel", "localhost"}) {
		t.Fatalf("DNS SANs = %v", got)
	}

	namesErr = nil
	names = []string{"panel.tail1234.ts.net", "localhost", "panel", "", "alias.tail1234.ts.net", "panel.tail1234.ts.net"}
	if issued, err := l.Ensure(ctx, addrs("100.64.0.1")); err != nil || !issued {
		t.Fatalf("Ensure with new names = %v, %v; want a new leaf", issued, err)
	}
	want := []string{"panel", "localhost", "alias.tail1234.ts.net", "panel.tail1234.ts.net"}
	if got := csDNSNames(t, l); !slices.Equal(got, want) {
		t.Fatalf("DNS SANs = %v, want %v", got, want)
	}
	if issued, err := l.Ensure(ctx, addrs("100.64.0.1")); err != nil || issued {
		t.Fatalf("Ensure with the same names in another order = %v, %v; want no new leaf", issued, err)
	}

	// A failure keeps the current names and issues nothing.
	namesErr = errors.New("tailscaled is down")
	if issued, err := l.Ensure(ctx, addrs("100.64.0.1")); err != nil || issued {
		t.Fatalf("Ensure with failing names = %v, %v; want the current leaf kept", issued, err)
	}
	if got := csDNSNames(t, l); !slices.Equal(got, want) {
		t.Fatalf("DNS SANs after a failure = %v, want %v", got, want)
	}

	namesErr = nil
	names = []string{"panel.tail1234.ts.net"}
	if issued, err := l.Ensure(ctx, addrs("100.64.0.1")); err != nil || !issued {
		t.Fatalf("Ensure with a name gone = %v, %v; want a new leaf", issued, err)
	}
	if got := csDNSNames(t, l); !slices.Equal(got, []string{"panel", "localhost", "panel.tail1234.ts.net"}) {
		t.Fatalf("DNS SANs = %v", got)
	}
}

func TestLeafWithStubSources(t *testing.T) {
	l, _, _ := newLeaf(t)
	l.useSources(SourceDeps{LocalHostID: "H1"})
	if _, err := l.Ensure(context.Background(), addrs("127.0.0.1")); err != nil {
		t.Fatal(err)
	}
	if got := csDNSNames(t, l); !slices.Equal(got, []string{"panel", "localhost"}) {
		t.Fatalf("DNS SANs = %v; the stub sources add no name", got)
	}
	if got, err := l.GetCertificate(&tls.ClientHelloInfo{ServerName: "panel"}); err != nil || got != l.cur.Load().cert {
		t.Fatalf("GetCertificate = %v, %v; want the CA leaf through the stub source", got, err)
	}
}
