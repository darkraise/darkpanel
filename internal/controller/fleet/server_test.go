package fleet

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/agent/wire"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/transport"
)

// fleetServer is a Server on 127.0.0.1 over a fresh authority and registry.
type fleetServer struct {
	a     *Authority
	db    *store.DB
	clock *coretest.FakeClock
	reg   *Registry
	srv   *Server
	addr  string
}

// startServer serves a fresh authority's agent connections on
// 127.0.0.1:0 until the end of the test; change adjusts the configuration
// first.
func startServer(t *testing.T, change func(*ServerConfig)) *fleetServer {
	t.Helper()
	a, db, clock := newAuthority(t)
	reg, err := NewRegistry(RegistryConfig{
		DB: db, Clock: clock, CheckPeer: a.CheckPeer, Version: "1.2.3", ControllerID: a.ControllerID(),
		Heartbeat: transport.HeartbeatConfig{Interval: time.Hour}, Log: quietLog,
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg := ServerConfig{Authority: a, Registry: reg, Limiter: NewEnrollLimiter(clock), Log: quietLog}
	if change != nil {
		change(&cfg)
	}
	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ln) }()
	t.Cleanup(func() {
		_ = ln.Close()
		if err := <-served; err != nil {
			t.Errorf("Serve = %v after the listener closed, want nil", err)
		}
		srv.Close()
		reg.Close()
	})
	return &fleetServer{a: a, db: db, clock: clock, reg: reg, srv: srv, addr: ln.Addr().String()}
}

// enrolledCert issues hostID a certificate for a fresh key, as enrollment
// does, and returns it with its key.
func enrolledCert(t *testing.T, a *Authority, db *store.DB, hostID string) tls.Certificate {
	t.Helper()
	csr, key := newCSR(t)
	var out []byte
	err := db.Write(context.Background(), func(tx *sql.Tx) error {
		var err error
		out, err = a.IssueAgentCert(context.Background(), tx, hostID, csr)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	leaf := parseCert(t, out)
	return tls.Certificate{Certificate: [][]byte{leaf.Raw}, PrivateKey: key, Leaf: leaf}
}

// dialAgent dials the server as an agent with cert. Under TLS 1.3 the
// server checks the client certificate after the client's side of the
// handshake ends, so a refusal may surface only at the first read.
func dialAgent(t *testing.T, f *fleetServer, cert tls.Certificate) (*tls.Conn, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return transport.Dial(ctx, f.addr, transport.AgentConfig(cert, f.a.Root(), f.a.ControllerSAN()))
}

// connectAgent connects as hostID's agent with cert, serves agent over the
// session and waits until the registry holds it.
func connectAgent(t *testing.T, f *fleetServer, cert tls.Certificate, hostID string, agent core.AgentClient) *transport.Session {
	t.Helper()
	conn, err := dialAgent(t, f, cert)
	if err != nil {
		t.Fatal(err)
	}
	s, err := transport.AgentSession(conn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	serveAgent(t, s, agent)
	eventually(t, "the registry to hold "+hostID, func() bool {
		_, _, err := f.reg.Client(context.Background(), hostID)
		return err == nil
	})
	return s
}

// wantRefused fails unless the handshake refuses cert with a TLS alert,
// when the dial or the first read fails, and the host stays offline. A
// session closed after the handshake would fail the read without an alert.
func wantRefused(t *testing.T, f *fleetServer, cert tls.Certificate, hostID string) {
	t.Helper()
	conn, err := dialAgent(t, f, cert)
	if err == nil {
		defer func() { _ = conn.Close() }()
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, err = conn.Read(make([]byte, 1))
	}
	if err == nil || !strings.Contains(err.Error(), "remote error: tls: ") {
		t.Fatalf("the handshake did not refuse the agent with an alert: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	wantOffline(t, f.reg, hostID)
}

// sessionHTTP is an HTTP client whose requests open streams on the agent's
// side of s, as the agent's renewal does.
func sessionHTTP(s *transport.Session) *http.Client {
	return &http.Client{Transport: &http.Transport{
		DialContext:       func(ctx context.Context, _, _ string) (net.Conn, error) { return s.Open(ctx) },
		DisableKeepAlives: true,
	}}
}

// postJSON sends body to path with contentType and returns the status and
// the answer.
func postJSON(t *testing.T, c *http.Client, method, path, contentType string, body []byte) (int, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, "http://controller"+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, b
}

// wantProblemBody fails unless b is problem details with status and code
// and, when detail is not empty, exactly that detail.
func wantProblemBody(t *testing.T, gotStatus int, b []byte, status int, code errcode.Code, detail string) {
	t.Helper()
	var p problem
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatalf("answer %d %s is not problem details: %v", gotStatus, b, err)
	}
	if gotStatus != status || p.Status != status || p.Code != code || p.Title != code.Title() || p.Type != "about:blank" {
		t.Fatalf("answer %d %s, want status %d and code %s", gotStatus, b, status, code)
	}
	if detail != "" && p.Detail != detail {
		t.Fatalf("detail = %q, want %q", p.Detail, detail)
	}
}

func renewBody(t *testing.T, csr []byte) []byte {
	t.Helper()
	b, err := json.Marshal(transport.RenewRequest{CSR: string(csr)})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestServerRegistersAnAgent(t *testing.T) {
	f := startServer(t, nil)
	addHost(t, f.db, "H1", core.HostEnrolled)
	cert := enrolledCert(t, f.a, f.db, "H1")
	connectAgent(t, f, cert, "H1", helloAgent("H1", "1.2.0"))

	h, gen := hostHello(t, f.reg, "H1")
	if h.HostID != "H1" || gen == 0 {
		t.Fatalf("hello through the registry = %+v, generation %d", h, gen)
	}
}

func TestServerHandshakeRevokesEarlierSerials(t *testing.T) {
	f := startServer(t, nil)
	ctx := context.Background()
	addHost(t, f.db, "H1", core.HostEnrolled)
	old := enrolledCert(t, f.a, f.db, "H1")
	f.clock.Advance(time.Second)
	renewed := enrolledCert(t, f.a, f.db, "H1")
	connectAgent(t, f, renewed, "H1", helloAgent("H1", "v"))

	if err := f.a.CheckPeer(ctx, "H1", transport.SerialText(old.Leaf.SerialNumber)); err == nil {
		t.Fatal("the earlier serial is still valid after a handshake with the newer one")
	}
	if err := f.a.CheckPeer(ctx, "H1", transport.SerialText(renewed.Leaf.SerialNumber)); err != nil {
		t.Fatalf("the serial that connected was refused: %v", err)
	}
}

func TestServerRenews(t *testing.T) {
	f := startServer(t, nil)
	addHost(t, f.db, "H1", core.HostEnrolled)
	s := connectAgent(t, f, enrolledCert(t, f.a, f.db, "H1"), "H1", helloAgent("H1", "v"))
	c := sessionHTTP(s)

	csr, _ := newCSR(t)
	status, b := postJSON(t, c, http.MethodPost, transport.RenewPath, "application/json", renewBody(t, csr))
	if status != http.StatusOK {
		t.Fatalf("renew answered %d %s", status, b)
	}
	var resp transport.RenewResponse
	if err := json.Unmarshal(b, &resp); err != nil {
		t.Fatal(err)
	}
	cert := parseCert(t, []byte(resp.Certificate))
	if id, err := transport.HostIDOf(cert); err != nil || id != "H1" {
		t.Fatalf("the renewed certificate names %q, %v", id, err)
	}
	if _, err := cert.Verify(x509.VerifyOptions{Roots: f.a.Pool(), KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		CurrentTime: f.clock.Now()}); err != nil {
		t.Fatalf("the renewed certificate does not verify: %v", err)
	}

	big := append([]byte(`{"csr":"`), bytes.Repeat([]byte("a"), wire.MaxBody)...)
	big = append(big, `"}`...)
	cases := []struct {
		name, method, path, contentType string
		body                            []byte
		status                          int
		code                            errcode.Code
	}{
		{"wrong method", http.MethodGet, transport.RenewPath, "", nil, http.StatusMethodNotAllowed, errcode.MethodNotAllowed},
		{"wrong path", http.MethodPost, "/controller/v1/other", "application/json", renewBody(t, csr), http.StatusNotFound, errcode.NotFound},
		{"enrollment path", http.MethodPost, transport.EnrollPath, "application/json", renewBody(t, csr), http.StatusNotFound, errcode.NotFound},
		{"not JSON", http.MethodPost, transport.RenewPath, "text/plain", renewBody(t, csr), http.StatusUnsupportedMediaType, errcode.UnsupportedMediaType},
		{"bad JSON", http.MethodPost, transport.RenewPath, "application/json", []byte(`{"csr":`), http.StatusBadRequest, errcode.InvalidJSON},
		{"two objects", http.MethodPost, transport.RenewPath, "application/json", []byte(`{} {}`), http.StatusBadRequest, errcode.InvalidJSON},
		{"bad CSR", http.MethodPost, transport.RenewPath, "application/json", renewBody(t, []byte("junk")), http.StatusUnprocessableEntity, errcode.InvalidInput},
		{"too large", http.MethodPost, transport.RenewPath, "application/json", big, http.StatusRequestEntityTooLarge, errcode.TooLarge},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, b := postJSON(t, c, tc.method, tc.path, tc.contentType, tc.body)
			wantProblemBody(t, status, b, tc.status, tc.code, "")
		})
	}
}

func TestServerRenewRefusesARevokedHost(t *testing.T) {
	f := startServer(t, nil)
	addHost(t, f.db, "H1", core.HostEnrolled)
	s := connectAgent(t, f, enrolledCert(t, f.a, f.db, "H1"), "H1", helloAgent("H1", "v"))
	// The session stays up because nothing calls Disconnect, as when a
	// revocation's transaction commits just before the renewal.
	if _, err := f.db.Writer().Exec(`UPDATE core_hosts SET state = 'revoked' WHERE id = 'H1'`); err != nil {
		t.Fatal(err)
	}
	csr, _ := newCSR(t)
	status, b := postJSON(t, sessionHTTP(s), http.MethodPost, transport.RenewPath, "application/json", renewBody(t, csr))
	wantProblemBody(t, status, b, http.StatusForbidden, errcode.Forbidden, "host H1 is not enrolled")
}

func TestRevokedHostIsDisconnectedAndRefused(t *testing.T) {
	f := startServer(t, nil)
	ctx := context.Background()
	addHost(t, f.db, "H1", core.HostEnrolled)
	cert := enrolledCert(t, f.a, f.db, "H1")
	s := connectAgent(t, f, cert, "H1", helloAgent("H1", "v"))

	if err := f.db.Write(ctx, func(tx *sql.Tx) error { return RevokeHost(ctx, tx, "H1", f.clock.Now()) }); err != nil {
		t.Fatal(err)
	}
	f.reg.Disconnect("H1")
	wantOffline(t, f.reg, "H1")
	eventually(t, "the agent to see its session closed", func() bool { return ended(s) })

	wantRefused(t, f, cert, "H1")
}

func TestServerRefusesForeignAndRevokedCertificates(t *testing.T) {
	f := startServer(t, nil)
	addHost(t, f.db, "H1", core.HostEnrolled)

	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	san, err := url.Parse(transport.HostURI("H1"))
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(7), Subject: pkix.Name{CommonName: "self-signed"}, URIs: []*url.URL{san},
		NotBefore: testStart.Add(-time.Hour), NotAfter: testStart.Add(time.Hour),
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, BasicConstraintsValid: true, IsCA: true,
		KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, pub, key)
	if err != nil {
		t.Fatal(err)
	}
	foreign := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	wantRefused(t, f, foreign, "H1")

	revoked := enrolledCert(t, f.a, f.db, "H1")
	if _, err := f.db.Writer().Exec(`UPDATE core_agent_certs SET revoked_at = ?`, formatTime(testStart)); err != nil {
		t.Fatal(err)
	}
	wantRefused(t, f, revoked, "H1")
}

func TestServerBoundsTheHandshake(t *testing.T) {
	f := startServer(t, func(c *ServerConfig) { c.HandshakeTimeout = 50 * time.Millisecond })
	var d net.Dialer
	conn, err := d.DialContext(context.Background(), "tcp4", f.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, err = conn.Read(make([]byte, 1))
	var netErr net.Error
	if err == nil || errors.As(err, &netErr) && netErr.Timeout() {
		t.Fatalf("a silent client was not cut off: %v", err)
	}
}

func TestServerClose(t *testing.T) {
	f := startServer(t, nil)
	addHost(t, f.db, "H1", core.HostEnrolled)
	cert := enrolledCert(t, f.a, f.db, "H1")
	s := connectAgent(t, f, cert, "H1", helloAgent("H1", "v"))

	f.srv.Close()
	eventually(t, "Close to end the agent's session", func() bool { return ended(s) })
	eventually(t, "the registry to drop the host", func() bool {
		_, _, err := f.reg.Client(context.Background(), "H1")
		return err != nil
	})
	if conn, err := dialAgent(t, f, cert); err == nil {
		_ = conn.Close()
		t.Fatal("a closed server completed a handshake")
	}
}

func TestServerCloseWaitsForHandlers(t *testing.T) {
	f := startServer(t, nil)
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	h := f.srv.counted(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		close(started)
		<-release
	}))
	go h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, transport.RenewPath, nil))
	<-started

	go func() {
		f.srv.Close()
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("Close returned while a handler was still running")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return after the handler ended")
	}

	called := false
	late := f.srv.counted(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	rec := httptest.NewRecorder()
	late.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, transport.RenewPath, nil))
	if called {
		t.Fatal("a handler ran after Close")
	}
	wantProblemBody(t, rec.Code, rec.Body.Bytes(), http.StatusServiceUnavailable, errcode.Unavailable, "Unavailable")
}

func TestNewServerRefusesBadConfig(t *testing.T) {
	e := newRegistry(t, transport.HeartbeatConfig{})
	a, r, l := e.a, e.r, NewEnrollLimiter(e.clock)
	for name, cfg := range map[string]ServerConfig{
		"no authority":        {Registry: r, Limiter: l},
		"no registry":         {Authority: a, Limiter: l},
		"no limiter":          {Authority: a, Registry: r},
		"negative handshake":  {Authority: a, Registry: r, Limiter: l, HandshakeTimeout: -time.Second},
		"negative enrollment": {Authority: a, Registry: r, Limiter: l, EnrollTimeout: -time.Second},
	} {
		if _, err := NewServer(cfg); err == nil {
			t.Errorf("%s: NewServer accepted the configuration", name)
		}
	}
}

func TestProblemHidesInternalCauses(t *testing.T) {
	rec := &recordedAnswer{header: http.Header{}}
	writeProblem(rec, quietLog, errors.New("fleet: secret path /var/lib/darkpanel"))
	if strings.Contains(rec.body.String(), "secret") || rec.status != http.StatusInternalServerError {
		t.Fatalf("internal answer %d %s", rec.status, rec.body.String())
	}
	wantProblemBody(t, rec.status, rec.body.Bytes(), http.StatusInternalServerError, errcode.Internal, errcode.Internal.Title())

	rec = &recordedAnswer{header: http.Header{}}
	writeProblem(rec, quietLog, errcode.Wrap(errcode.Forbidden, errors.New("hidden cause"), "visible detail"))
	if strings.Contains(rec.body.String(), "hidden") {
		t.Fatalf("the cause leaked: %s", rec.body.String())
	}
	wantProblemBody(t, rec.status, rec.body.Bytes(), http.StatusForbidden, errcode.Forbidden, "visible detail")
	if got := rec.header.Get("Content-Type"); got != problemContentType {
		t.Fatalf("Content-Type = %q", got)
	}
}

// recordedAnswer is a minimal http.ResponseWriter for the writer tests.
type recordedAnswer struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (r *recordedAnswer) Header() http.Header { return r.header }

func (r *recordedAnswer) WriteHeader(status int) { r.status = status }

func (r *recordedAnswer) Write(p []byte) (int, error) { return r.body.Write(p) }
