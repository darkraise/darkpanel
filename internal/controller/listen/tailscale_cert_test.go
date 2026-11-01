package listen

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"log/slog"
	"math/big"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
)

const tcName = "panel.tail1234.ts.net"

// tcCall is one tailscale.cert.get read as the fake dispatcher saw it.
type tcCall struct {
	principal core.Principal
	host      string
	ref       core.ActionRef
	input     string
	left      time.Duration // time to the context's deadline when the read arrived; 0 without one
}

// tcReader is the dispatcher side of the certificate reads: it records every
// call and answers from answer, called with the call's 1-based number.
type tcReader struct {
	mu     sync.Mutex
	calls  []tcCall
	answer func(ctx context.Context, n int) (json.RawMessage, error)
}

func (r *tcReader) read(ctx context.Context, p core.Principal, host string, ref core.ActionRef, in json.RawMessage) (json.RawMessage, error) {
	var left time.Duration
	if dl, ok := ctx.Deadline(); ok {
		left = time.Until(dl)
	}
	r.mu.Lock()
	r.calls = append(r.calls, tcCall{principal: p, host: host, ref: ref, input: string(in), left: left})
	n := len(r.calls)
	answer := r.answer
	r.mu.Unlock()
	return answer(ctx, n)
}

func (r *tcReader) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func (r *tcReader) deps(log *slog.Logger) SourceDeps {
	return SourceDeps{Dispatch: &coretest.FakeDispatcher{ReadInternalFunc: r.read}, LocalHostID: "H1", Log: log}
}

// tcAlways answers every read with body.
func tcAlways(body json.RawMessage) func(context.Context, int) (json.RawMessage, error) {
	return func(context.Context, int) (json.RawMessage, error) { return body, nil }
}

// tcParts makes a self-signed certificate valid from notBefore to notAfter and
// returns its PEM, its private key's PEM and its DER.
func tcParts(t *testing.T, name string, notBefore, notAfter time.Time) (certPEM, keyPEM string, der []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: name}, DNSNames: []string{name},
		NotBefore: notBefore, NotAfter: notAfter, KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true,
	}
	der, err = x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	keyPEM = string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
	return certPEM, keyPEM, der
}

// tcBody is the JSON of a tailscale.cert.get answer.
func tcBody(t *testing.T, name, certPEM, keyPEM string, notAfter time.Time) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(map[string]any{"name": name, "certPem": certPEM, "keyPem": keyPEM, "notAfter": notAfter})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// tcPair is a fresh answer for name and the DER of its certificate.
func tcPair(t *testing.T, name string, notBefore, notAfter time.Time) (json.RawMessage, []byte) {
	t.Helper()
	certPEM, keyPEM, der := tcParts(t, name, notBefore, notAfter)
	return tcBody(t, name, certPEM, keyPEM, notAfter), der
}

func tcNewCache(r *tcReader, log *slog.Logger) (*pairCache, *coretest.FakeClock) {
	clock := coretest.NewFakeClock(testStart)
	return newPairCache(r.deps(log), clock), clock
}

func tcServed(t *testing.T, c *pairCache, serverName string) []byte {
	t.Helper()
	cert, err := c.Certificate(&tls.ClientHelloInfo{ServerName: serverName})
	if err != nil {
		t.Fatalf("Certificate(%q) error: %v", serverName, err)
	}
	if cert == nil {
		return nil
	}
	return cert.Certificate[0]
}

func TestTcNamesReadsThePairAsTheListenPrincipal(t *testing.T) {
	body, _ := tcPair(t, tcName, testStart, testStart.Add(90*time.Hour))
	r := &tcReader{answer: tcAlways(body)}
	c, _ := tcNewCache(r, nil)
	names, err := c.names(context.Background())
	if err != nil || !slices.Equal(names, []string{tcName}) {
		t.Fatalf("names = %v, %v; want [%s]", names, err, tcName)
	}
	if r.count() != 1 {
		t.Fatalf("%d reads; want 1", r.count())
	}
	call := r.calls[0]
	if call.principal != (core.Principal{Actor: "system:listen.cert", Scope: core.ScopeAdmin}) || call.host != "H1" ||
		call.ref != (core.ActionRef{Name: "tailscale.cert.get", Version: 1}) || call.input != "{}" {
		t.Errorf("read = %+v", call)
	}
	if call.left <= 9*time.Second || call.left > 10*time.Second {
		t.Errorf("the read had %v to its deadline; want the 10 s bound", call.left)
	}
	if names, _ := c.names(context.Background()); !slices.Equal(names, []string{tcName}) || r.count() != 1 {
		t.Errorf("a second names = %v after %d reads; want the cached pair, 1 read", names, r.count())
	}
}

func TestTcCertificateAnswersOnlyItsName(t *testing.T) {
	body, der := tcPair(t, tcName, testStart, testStart.Add(90*time.Hour))
	r := &tcReader{answer: tcAlways(body)}
	c, _ := tcNewCache(r, nil)
	if _, err := c.names(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		serverName string
		serves     bool
	}{
		{tcName, true},
		{"PANEL.Tail1234.TS.net", true},
		{tcName + ".", true},
		{"", false},
		{"panel", false},
		{"other.tail1234.ts.net", false},
		{"xpanel.tail1234.ts.net", false},
		{"100.64.0.1", false},
	} {
		got := tcServed(t, c, tt.serverName)
		if tt.serves != (got != nil) || (tt.serves && !bytes.Equal(got, der)) {
			t.Errorf("ServerName %q: served the pair = %v, want %v", tt.serverName, got != nil, tt.serves)
		}
	}
	c.wait()
	if r.count() != 1 {
		t.Errorf("%d reads; handshakes on a fresh pair must not read", r.count())
	}
}

func TestTcCertificateWithoutAPairFallsThroughAndLoadsOnce(t *testing.T) {
	body, der := tcPair(t, tcName, testStart, testStart.Add(90*time.Hour))
	release := make(chan struct{})
	r := &tcReader{answer: func(context.Context, int) (json.RawMessage, error) {
		<-release
		return body, nil
	}}
	c, _ := tcNewCache(r, nil)
	for range 3 {
		if got := tcServed(t, c, tcName); got != nil {
			t.Fatal("a handshake before any pair was loaded was served a certificate")
		}
	}
	close(release)
	c.wait()
	if r.count() != 1 {
		t.Fatalf("%d reads for three handshakes; want 1", r.count())
	}
	if got := tcServed(t, c, tcName); !bytes.Equal(got, der) {
		t.Error("the pair loaded in the background is not served")
	}
}

func TestTcRefreshesInTheBackgroundAtTwoThirdsOfLife(t *testing.T) {
	first, firstDER := tcPair(t, tcName, testStart, testStart.Add(90*time.Hour))
	second, secondDER := tcPair(t, tcName, testStart.Add(60*time.Hour), testStart.Add(150*time.Hour))
	r := &tcReader{answer: func(_ context.Context, n int) (json.RawMessage, error) {
		if n == 1 {
			return first, nil
		}
		return second, nil
	}}
	c, clock := tcNewCache(r, nil)
	if _, err := c.names(context.Background()); err != nil {
		t.Fatal(err)
	}
	clock.Advance(60*time.Hour - time.Second)
	if got := tcServed(t, c, tcName); !bytes.Equal(got, firstDER) {
		t.Fatal("the pair is not served just before two thirds of its life")
	}
	c.wait()
	if r.count() != 1 {
		t.Fatalf("%d reads before two thirds of the life; want 1", r.count())
	}
	clock.Advance(time.Second)
	if got := tcServed(t, c, tcName); !bytes.Equal(got, firstDER) {
		t.Fatal("the handshake that finds the pair due must still be served the old pair")
	}
	c.wait()
	if r.count() != 2 {
		t.Fatalf("%d reads at two thirds of the life; want 2", r.count())
	}
	if got := tcServed(t, c, tcName); !bytes.Equal(got, secondDER) {
		t.Error("the refreshed pair is not served")
	}
}

func TestTcExpiredPairIsNotServed(t *testing.T) {
	body, _ := tcPair(t, tcName, testStart, testStart.Add(90*time.Hour))
	r := &tcReader{answer: func(_ context.Context, n int) (json.RawMessage, error) {
		if n == 1 {
			return body, nil
		}
		return nil, errors.New("tailscaled does not answer")
	}}
	c, clock := tcNewCache(r, nil)
	if _, err := c.names(context.Background()); err != nil {
		t.Fatal(err)
	}
	clock.Advance(90 * time.Hour)
	if got := tcServed(t, c, tcName); got != nil {
		t.Error("a pair at its NotAfter is still served")
	}
	c.wait()
}

// TestTcNamesRechecksTheNodeEveryFiveMinutes: the leaf asks for its extra
// names on every listen check, and an address change of the node (a re-login
// as another node) can change its MagicDNS name, so a pair older than five
// minutes is read again on that path, and a new name replaces the old one.
func TestTcNamesRechecksTheNodeEveryFiveMinutes(t *testing.T) {
	first, _ := tcPair(t, tcName, testStart, testStart.Add(90*time.Hour))
	second, secondDER := tcPair(t, "other.tail1234.ts.net", testStart, testStart.Add(90*time.Hour))
	r := &tcReader{answer: func(_ context.Context, n int) (json.RawMessage, error) {
		if n == 1 {
			return first, nil
		}
		return second, nil
	}}
	c, clock := tcNewCache(r, nil)
	if names, err := c.names(context.Background()); err != nil || !slices.Equal(names, []string{tcName}) {
		t.Fatalf("names = %v, %v; want [%s]", names, err, tcName)
	}
	clock.Advance(5*time.Minute - time.Second)
	if names, _ := c.names(context.Background()); !slices.Equal(names, []string{tcName}) || r.count() != 1 {
		t.Fatalf("names = %v after %d reads; want the cached name and no second read before five minutes", names, r.count())
	}
	clock.Advance(time.Second)
	if names, _ := c.names(context.Background()); !slices.Equal(names, []string{"other.tail1234.ts.net"}) || r.count() != 2 {
		t.Fatalf("names = %v after %d reads; want the node's new name from a second read", names, r.count())
	}
	if got := tcServed(t, c, "other.tail1234.ts.net"); !bytes.Equal(got, secondDER) {
		t.Error("the pair of the new name is not served")
	}
	if got := tcServed(t, c, tcName); got != nil {
		t.Error("the old name is still served after the node's name changed")
	}
	c.wait()
}

func TestTcFailedRefreshIsNotRetriedForAMinute(t *testing.T) {
	r := &tcReader{answer: func(context.Context, int) (json.RawMessage, error) {
		return nil, errors.New("tailscaled does not answer")
	}}
	c, clock := tcNewCache(r, nil)
	tcServed(t, c, tcName)
	c.wait()
	tcServed(t, c, tcName)
	c.wait()
	if r.count() != 1 {
		t.Fatalf("%d reads right after a failure; want 1", r.count())
	}
	clock.Advance(time.Minute - time.Second)
	tcServed(t, c, tcName)
	c.wait()
	if r.count() != 1 {
		t.Fatalf("%d reads before the minute passed; want 1", r.count())
	}
	clock.Advance(time.Second)
	tcServed(t, c, tcName)
	c.wait()
	if r.count() != 2 {
		t.Fatalf("%d reads after the minute passed; want 2", r.count())
	}
}

// TestTcDuePairIsReadAgainOnlyAfterAMinute: tailscaled can hand back a pair
// already past two thirds of its life while its own renewal runs or fails,
// and every read writes an audit entry on the agent.
func TestTcDuePairIsReadAgainOnlyAfterAMinute(t *testing.T) {
	body, der := tcPair(t, tcName, testStart.Add(-60*time.Hour), testStart.Add(30*time.Hour))
	r := &tcReader{answer: tcAlways(body)}
	c, clock := tcNewCache(r, nil)
	if names, err := c.names(context.Background()); err != nil || !slices.Equal(names, []string{tcName}) {
		t.Fatalf("names = %v, %v; want [%s]", names, err, tcName)
	}
	for range 3 {
		if got := tcServed(t, c, tcName); !bytes.Equal(got, der) {
			t.Fatal("the due pair is not served")
		}
		c.wait()
		if names, _ := c.names(context.Background()); !slices.Equal(names, []string{tcName}) {
			t.Fatalf("names = %v; want the cached name", names)
		}
	}
	if r.count() != 1 {
		t.Fatalf("%d reads of a pair that came back due; want 1 until a minute passes", r.count())
	}
	clock.Advance(time.Minute - time.Second)
	tcServed(t, c, tcName)
	c.wait()
	_, _ = c.names(context.Background())
	if r.count() != 1 {
		t.Fatalf("%d reads before the minute passed; want 1", r.count())
	}
	clock.Advance(time.Second)
	tcServed(t, c, tcName)
	c.wait()
	if r.count() != 2 {
		t.Fatalf("%d reads after the minute passed; want 2", r.count())
	}
	if names, _ := c.names(context.Background()); !slices.Equal(names, []string{tcName}) || r.count() != 2 {
		t.Errorf("names = %v after %d reads; want the cached name and no third read within the next minute", names, r.count())
	}
}

func TestTcReadErrorsFallThroughAndLogOncePerDistinctError(t *testing.T) {
	body, _ := tcPair(t, tcName, testStart, testStart.Add(90*time.Hour))
	failures := []string{"tailscaled does not answer", "tailscaled does not answer", "the agent is offline", "the agent is offline", "tailscaled does not answer"}
	r := &tcReader{answer: func(_ context.Context, n int) (json.RawMessage, error) {
		if n <= len(failures) {
			return nil, errors.New(failures[n-1])
		}
		return body, nil
	}}
	buf := &logBuffer{}
	c, _ := tcNewCache(r, slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	for i := range failures {
		if names, err := c.names(context.Background()); names != nil || err != nil {
			t.Fatalf("names after failure %d = %v, %v; want none and no error", i+1, names, err)
		}
	}
	out := buf.String()
	if n := strings.Count(out, "tailscaled does not answer"); n != 2 {
		t.Errorf("tailscaled error logged %d times; want 2 (it changed and came back)", n)
	}
	if n := strings.Count(out, "the agent is offline"); n != 1 {
		t.Errorf("agent error logged %d times; want 1", n)
	}
	if !strings.Contains(out, "level=DEBUG") || strings.Contains(out, "level=WARN") || strings.Contains(out, "level=ERROR") {
		t.Errorf("read errors must be logged at debug level only: %s", out)
	}
	if names, err := c.names(context.Background()); err != nil || !slices.Equal(names, []string{tcName}) {
		t.Errorf("names after the read recovers = %v, %v", names, err)
	}
}

func TestTcNamesKeepsTheCachedNameWhenARefreshFails(t *testing.T) {
	body, der := tcPair(t, tcName, testStart, testStart.Add(90*time.Hour))
	r := &tcReader{answer: func(_ context.Context, n int) (json.RawMessage, error) {
		if n == 1 {
			return body, nil
		}
		return nil, errors.New("tailscaled does not answer")
	}}
	c, clock := tcNewCache(r, nil)
	if _, err := c.names(context.Background()); err != nil {
		t.Fatal(err)
	}
	clock.Advance(60 * time.Hour)
	names, err := c.names(context.Background())
	if err != nil || !slices.Equal(names, []string{tcName}) || r.count() != 2 {
		t.Fatalf("names = %v, %v after %d reads; want the cached name after a failed refresh", names, err, r.count())
	}
	if got := tcServed(t, c, tcName); !bytes.Equal(got, der) {
		t.Error("the cached pair stopped being served after a failed refresh inside its life")
	}
	c.wait()
}

func TestTcBadAnswersAreNeverCached(t *testing.T) {
	notBefore, notAfter := testStart, testStart.Add(90*time.Hour)
	certPEM, keyPEM, _ := tcParts(t, tcName, notBefore, notAfter)
	_, otherKeyPEM, _ := tcParts(t, tcName, notBefore, notAfter)
	tests := []struct {
		name string
		body json.RawMessage
	}{
		{"not JSON", json.RawMessage(`not json`)},
		{"an empty object", json.RawMessage(`{}`)},
		{"no certificate", tcBody(t, tcName, "", keyPEM, notAfter)},
		{"a certificate that is not PEM", tcBody(t, tcName, "certificate", keyPEM, notAfter)},
		{"a key that is not the certificate's", tcBody(t, tcName, certPEM, otherKeyPEM, notAfter)},
		{"an empty name", tcBody(t, "", certPEM, keyPEM, notAfter)},
		{"an IPv4 literal", tcBody(t, "100.64.0.1", certPEM, keyPEM, notAfter)},
		{"an IPv6 literal", tcBody(t, "fd7a:115c:a1e0::1", certPEM, keyPEM, notAfter)},
		{"a non-ASCII name", tcBody(t, "pänel.ts.net", certPEM, keyPEM, notAfter)},
		{"an empty label", tcBody(t, "panel..ts.net", certPEM, keyPEM, notAfter)},
		{"a label that starts with a hyphen", tcBody(t, "-panel.ts.net", certPEM, keyPEM, notAfter)},
		{"a label that ends with a hyphen", tcBody(t, "panel-.ts.net", certPEM, keyPEM, notAfter)},
		{"an underscore", tcBody(t, "pa_nel.ts.net", certPEM, keyPEM, notAfter)},
		{"a space", tcBody(t, "pa nel.ts.net", certPEM, keyPEM, notAfter)},
		{"a label of 64 characters", tcBody(t, strings.Repeat("a", 64)+".ts.net", certPEM, keyPEM, notAfter)},
		{"a name of 254 characters", tcBody(t, strings.Repeat(strings.Repeat("a", 61)+".", 4)+"abcdef", certPEM, keyPEM, notAfter)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &tcReader{answer: tcAlways(tt.body)}
			buf := &logBuffer{}
			c, _ := tcNewCache(r, slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
			if names, err := c.names(context.Background()); names != nil || err != nil {
				t.Errorf("names = %v, %v; want none and no error", names, err)
			}
			if got := tcServed(t, c, tcName); got != nil {
				t.Error("a certificate was served from a bad answer")
			}
			c.wait()
			if buf.String() == "" {
				t.Error("the bad answer was not logged")
			}
			if strings.Contains(buf.String(), "PRIVATE KEY") || strings.Contains(buf.String(), keyPEM) {
				t.Error("the log holds key material")
			}
		})
	}
}

func TestTcNamesNormalizesTheName(t *testing.T) {
	for _, tt := range []struct{ in, want string }{
		{"panel.tail1234.ts.net", "panel.tail1234.ts.net"},
		{"PANEL.Tail1234.TS.net.", "panel.tail1234.ts.net"},
		{"panel", "panel"},
		{strings.Repeat("a", 63) + ".ts.net", strings.Repeat("a", 63) + ".ts.net"},
	} {
		body, _ := tcPair(t, tt.in, testStart, testStart.Add(90*time.Hour))
		r := &tcReader{answer: tcAlways(body)}
		c, _ := tcNewCache(r, nil)
		names, err := c.names(context.Background())
		if err != nil || !slices.Equal(names, []string{tt.want}) {
			t.Errorf("names for %q = %v, %v; want [%s]", tt.in, names, err, tt.want)
		}
		if got := tcServed(t, c, tt.want); got == nil {
			t.Errorf("the pair named %q is not served for %q", tt.in, tt.want)
		}
	}
}

func TestTcReadIsBoundedByTheTimeout(t *testing.T) {
	r := &tcReader{answer: func(ctx context.Context, _ int) (json.RawMessage, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	c, _ := tcNewCache(r, nil)
	c.timeout = 20 * time.Millisecond
	start := time.Now()
	if names, err := c.names(context.Background()); names != nil || err != nil {
		t.Fatalf("names = %v, %v; want none and no error", names, err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("a read that never answers took %v", elapsed)
	}
}

func TestTcWithoutADispatcherEverythingFallsThrough(t *testing.T) {
	d := SourceDeps{LocalHostID: "H1"}
	c := newPairCache(d, coretest.NewFakeClock(testStart))
	if got := tcServed(t, c, tcName); got != nil {
		t.Error("a source without a dispatcher served a certificate")
	}
	c.wait()
	if names, err := c.names(context.Background()); names != nil || err != nil {
		t.Errorf("names = %v, %v; want none", names, err)
	}
	if cert, err := tailscaleCertSource(d).Certificate(&tls.ClientHelloInfo{ServerName: tcName}); cert != nil || err != nil {
		t.Errorf("tailscaleCertSource = %v, %v; want it to fall through", cert, err)
	}
	if names, err := tailscaleNames(context.Background(), d); names != nil || err != nil {
		t.Errorf("tailscaleNames = %v, %v; want none", names, err)
	}
}

func TestTcSourceAndNamesShareOnePair(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	body, der := tcPair(t, tcName, now.Add(-time.Hour), now.Add(90*24*time.Hour))
	r := &tcReader{answer: tcAlways(body)}
	d := r.deps(nil)
	names, err := tailscaleNames(context.Background(), d)
	if err != nil || !slices.Equal(names, []string{tcName}) {
		t.Fatalf("tailscaleNames = %v, %v", names, err)
	}
	cert, err := tailscaleCertSource(d).Certificate(&tls.ClientHelloInfo{ServerName: tcName})
	if err != nil || cert == nil || !bytes.Equal(cert.Certificate[0], der) {
		t.Fatalf("tailscaleCertSource did not serve the pair tailscaleNames loaded: %v, %v", cert, err)
	}
	if r.count() != 1 {
		t.Errorf("%d reads; want the one read shared by the source and the names", r.count())
	}
	other := &tcReader{answer: tcAlways(body)}
	if _, err := tailscaleNames(context.Background(), other.deps(nil)); err != nil || other.count() != 1 {
		t.Errorf("another dispatcher: %d reads, %v; want its own read", other.count(), err)
	}
	if r.count() != 1 {
		t.Errorf("the first dispatcher was read again: %d reads", r.count())
	}
}

func TestTcLeafCarriesTheNameAndServesThePair(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	body, der := tcPair(t, tcName, now.Add(-time.Hour), now.Add(90*24*time.Hour))
	r := &tcReader{answer: tcAlways(body)}
	l, _, _ := newLeaf(t)
	l.useSources(r.deps(slog.New(slog.DiscardHandler)))
	if issued, err := l.Ensure(context.Background(), addrs("100.64.0.1")); err != nil || !issued {
		t.Fatalf("Ensure = %v, %v", issued, err)
	}
	if got := csDNSNames(t, l); !slices.Equal(got, []string{"panel", "localhost", tcName}) {
		t.Fatalf("DNS SANs = %v; want the MagicDNS name added", got)
	}
	got, err := l.GetCertificate(&tls.ClientHelloInfo{ServerName: tcName})
	if err != nil || !bytes.Equal(got.Certificate[0], der) {
		t.Fatalf("GetCertificate(%s) = %v, %v; want the Tailscale pair", tcName, got, err)
	}
	if ca, err := l.GetCertificate(&tls.ClientHelloInfo{ServerName: "panel"}); err != nil || ca != l.cur.Load().cert {
		t.Errorf("GetCertificate(panel) = %v, %v; want the CA leaf", ca, err)
	}
	if r.count() != 1 {
		t.Errorf("%d reads; want 1", r.count())
	}
}
