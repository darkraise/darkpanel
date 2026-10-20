package fleet

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/agent/wire"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/transport"
)

// enrollRequest sends one request over a fresh enrollment connection and
// returns the answer. It fails the test unless the server closes the
// connection after answering.
func enrollRequest(t *testing.T, f *fleetServer, method, path, contentType string, body []byte) (int, []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := transport.Dial(ctx, f.addr, transport.EnrollConfig(f.a.Fingerprint(), f.a.ControllerSAN()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	req, err := http.NewRequestWithContext(ctx, method, "https://controller"+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if err := req.Write(conn); err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Close {
		t.Error("the answer does not announce Connection: close")
	}
	if _, err := br.ReadByte(); !errors.Is(err, io.EOF) {
		t.Errorf("after the answer the connection gave %v, want EOF", err)
	}
	return resp.StatusCode, b
}

func enrollBody(t *testing.T, token string, csr []byte) []byte {
	t.Helper()
	b, err := json.Marshal(transport.EnrollRequest{Token: token, CSR: string(csr)})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestServerEnrolls(t *testing.T) {
	f := startServer(t, nil)
	addHost(t, f.db, "H1", core.HostPending)
	token, _ := issueToken(t, f.a, f.db, "H1")
	csr, key := newCSR(t)

	status, b := enrollRequest(t, f, http.MethodPost, transport.EnrollPath, "application/json", enrollBody(t, token, csr))
	if status != http.StatusOK {
		t.Fatalf("enrollment answered %d %s", status, b)
	}
	var resp transport.EnrollResponse
	if err := json.Unmarshal(b, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.CA != string(f.a.RootPEM()) {
		t.Fatal("the answer's ca is not the agent root")
	}
	leaf := parseCert(t, []byte(resp.Certificate))
	if id, err := transport.HostIDOf(leaf); err != nil || id != "H1" {
		t.Fatalf("the certificate names %q, %v", id, err)
	}
	if got := hostState(t, f.db, "H1"); got != core.HostEnrolled {
		t.Fatalf("host state = %s", got)
	}

	// The new credentials open an agent session.
	cert := tls.Certificate{Certificate: [][]byte{leaf.Raw}, PrivateKey: key, Leaf: leaf}
	connectAgent(t, f, cert, "H1", helloAgent("H1", "v"))
	if h, _ := hostHello(t, f.reg, "H1"); h.HostID != "H1" {
		t.Fatalf("hello = %+v", h)
	}
}

func TestServerEnrollRefusals(t *testing.T) {
	limits := coretest.NewFakeClock(testStart)
	f := startServer(t, func(c *ServerConfig) { c.Limiter = NewEnrollLimiter(limits) })
	addHost(t, f.db, "H1", core.HostPending)
	addHost(t, f.db, "H2", core.HostPending)
	used, _ := issueToken(t, f.a, f.db, "H1")
	csr, _ := newCSR(t)
	if status, b := enrollRequest(t, f, http.MethodPost, transport.EnrollPath, "application/json", enrollBody(t, used, csr)); status != http.StatusOK {
		t.Fatalf("the first enrollment answered %d %s", status, b)
	}
	fresh, _ := issueToken(t, f.a, f.db, "H2")
	big := append([]byte(`{"token":"`), bytes.Repeat([]byte("a"), wire.MaxBody)...)
	big = append(big, `"}`...)

	const invalid = "the enrollment token is not valid"
	cases := []struct {
		name, method, path, contentType string
		body                            []byte
		status                          int
		code                            errcode.Code
		detail                          string
	}{
		{"reused token", http.MethodPost, transport.EnrollPath, "application/json", enrollBody(t, used, csr),
			http.StatusForbidden, errcode.EnrollTokenInvalid, invalid},
		{"malformed token", http.MethodPost, transport.EnrollPath, "application/json", enrollBody(t, "dpe1.junk", csr),
			http.StatusForbidden, errcode.EnrollTokenInvalid, invalid},
		{"bad CSR", http.MethodPost, transport.EnrollPath, "application/json", enrollBody(t, fresh, []byte("junk")),
			http.StatusForbidden, errcode.EnrollTokenInvalid, invalid},
		{"wrong path", http.MethodPost, transport.RenewPath, "application/json", enrollBody(t, fresh, csr),
			http.StatusNotFound, errcode.NotFound, "the enrollment connection serves only " + transport.EnrollPath},
		{"wrong method", http.MethodGet, transport.EnrollPath, "", nil,
			http.StatusMethodNotAllowed, errcode.MethodNotAllowed, transport.EnrollPath + " takes POST"},
		{"not JSON", http.MethodPost, transport.EnrollPath, "text/plain", enrollBody(t, fresh, csr),
			http.StatusUnsupportedMediaType, errcode.UnsupportedMediaType, "send the request body with Content-Type: application/json"},
		{"bad JSON", http.MethodPost, transport.EnrollPath, "application/json", []byte(`{"token":`),
			http.StatusBadRequest, errcode.InvalidJSON, "the request body must be one JSON object"},
		{"too large", http.MethodPost, transport.EnrollPath, "application/json", big,
			http.StatusRequestEntityTooLarge, errcode.TooLarge, "the request body exceeds 4194304 bytes"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			limits.Advance(time.Minute)
			status, b := enrollRequest(t, f, tc.method, tc.path, tc.contentType, tc.body)
			wantProblemBody(t, status, b, tc.status, tc.code, tc.detail)
			if strings.Contains(string(b), "fleet:") || strings.Contains(string(b), "transport:") {
				t.Fatalf("the answer carries an error's text: %s", b)
			}
		})
	}

	// The refused CSR did not consume H2's token.
	limits.Advance(time.Minute)
	if status, b := enrollRequest(t, f, http.MethodPost, transport.EnrollPath, "application/json", enrollBody(t, fresh, csr)); status != http.StatusOK {
		t.Fatalf("H2's token after the refusals answered %d %s", status, b)
	}

	// An expired token reads like every other refusal.
	addHost(t, f.db, "H3", core.HostPending)
	late, _ := issueToken(t, f.a, f.db, "H3")
	f.clock.Advance(TokenTTL)
	limits.Advance(time.Minute)
	status, b := enrollRequest(t, f, http.MethodPost, transport.EnrollPath, "application/json", enrollBody(t, late, csr))
	wantProblemBody(t, status, b, http.StatusForbidden, errcode.EnrollTokenInvalid, invalid)
}

func TestServerEnrollRateLimited(t *testing.T) {
	f := startServer(t, nil)
	csr, _ := newCSR(t)
	body := enrollBody(t, "dpe1.junk", csr)
	for i := range EnrollPerSource {
		status, b := enrollRequest(t, f, http.MethodPost, transport.EnrollPath, "application/json", body)
		if status != http.StatusForbidden {
			t.Fatalf("request %d answered %d %s", i, status, b)
		}
	}
	status, b := enrollRequest(t, f, http.MethodPost, transport.EnrollPath, "application/json", body)
	wantProblemBody(t, status, b, http.StatusTooManyRequests, errcode.EnrollRateLimited,
		"too many enrollment requests; wait a minute and try again")

	f.clock.Advance(time.Minute)
	if status, b := enrollRequest(t, f, http.MethodPost, transport.EnrollPath, "application/json", body); status != http.StatusForbidden {
		t.Fatalf("a minute later the request answered %d %s", status, b)
	}
}

func TestServerBoundsTheEnrollment(t *testing.T) {
	f := startServer(t, func(c *ServerConfig) { c.EnrollTimeout = 50 * time.Millisecond })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := transport.Dial(ctx, f.addr, transport.EnrollConfig(f.a.Fingerprint(), f.a.ControllerSAN()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, err = conn.Read(make([]byte, 1))
	var netErr net.Error
	if err == nil || errors.As(err, &netErr) && netErr.Timeout() {
		t.Fatalf("a silent enrollment client was not cut off: %v", err)
	}
}

func TestEnrollSource(t *testing.T) {
	for addr, want := range map[string]string{
		"127.0.0.1:41000":                "127.0.0.1",
		"[::ffff:198.51.100.7]:41000":    "198.51.100.7",
		"[2001:db8:1:2:3:4:5:6]:41000":   "2001:db8:1:2::/64",
		"[2001:db8:1:2:ffff::1]:1":       "2001:db8:1:2::/64",
		"[fe80::1%eth0]:41000":           "fe80::/64",
		"[2001:db8:1:3::1]:41000":        "2001:db8:1:3::/64",
		"@/run/darkpanel/odd-socket":     "@/run/darkpanel/odd-socket",
		"[2001:db8:1:2::1%tailscale0]:1": "2001:db8:1:2::/64",
	} {
		if got := enrollSource(fakeAddr(addr)); got != want {
			t.Errorf("enrollSource(%s) = %s, want %s", addr, got, want)
		}
	}
}

type fakeAddr string

func (a fakeAddr) Network() string { return "tcp" }

func (a fakeAddr) String() string { return string(a) }
