package daemon

import (
	"context"
	"crypto/rand"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/transport"
)

func wantCode(t *testing.T, err error, code errcode.Code) {
	t.Helper()
	var e *errcode.Error
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("err = %v, want code %s", err, code)
	}
}

// TestEnrollEndToEnd enrolls against the real authority behind
// transport.Handshake, then opens an mTLS session with what it saved.
func TestEnrollEndToEnd(t *testing.T) {
	ctrl := newTestController(t, time.Now().UTC().Truncate(time.Second))
	ctrl.addHost("H1", "pending")
	token := ctrl.token("H1")
	dir := filepath.Join(t.TempDir(), "agent")
	ctx := context.Background()
	creds, err := Enroll(ctx, dir, "  "+token+"\n")
	if err != nil {
		t.Fatalf("Enroll: %v", err)
	}
	saved, err := LoadCredentials(dir)
	if err != nil || !sameCredentials(saved, creds) || saved.Generation != 1 {
		t.Fatalf("LoadCredentials = %+v, %v", saved, err)
	}
	if saved.HostID() != "H1" || !saved.CA.Equal(ctrl.auth.Root()) ||
		saved.Controller != (Endpoint{Address: ctrl.addr, SAN: ctrl.auth.ControllerSAN()}) {
		t.Errorf("saved %s, root %v, controller %+v", saved.HostID(), saved.CA.Equal(ctrl.auth.Root()), saved.Controller)
	}
	if st := ctrl.hostState("H1"); st != "enrolled" {
		t.Errorf("host state %q, want enrolled", st)
	}
	conn, err := transport.Dial(ctx, saved.Controller.Address,
		transport.AgentConfig(saved.TLSCertificate(), saved.CA, saved.Controller.SAN))
	if err != nil {
		t.Fatal(err)
	}
	sess, err := transport.AgentSession(conn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()
	if _, err := sess.Ping(); err != nil {
		t.Fatalf("the controller did not accept the saved credentials: %v", err)
	}
	if s := ctrl.agentSessions(); len(s) != 1 || s[0].hostID != "H1" {
		t.Errorf("controller sessions = %+v", s)
	}

	// A token is used once; a refused enrollment writes nothing.
	_, err = Enroll(ctx, dir, token)
	wantCode(t, err, errcode.EnrollTokenInvalid)
	if gens := generations(t, dir); len(gens) != 1 {
		t.Errorf("generations after the refused enrollment = %v", gens)
	}
}

func TestEnrollRefusesAMalformedToken(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "agent")
	for _, token := range []string{"", "dpe1.", "dpe1.e30", "nope"} {
		_, err := Enroll(context.Background(), dir, token)
		wantCode(t, err, errcode.EnrollTokenInvalid)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("a malformed token created %s: %v", dir, err)
	}
}

// tamper rewrites one member of token's JSON.
func tamper(t *testing.T, token, member, value string) string {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(token, transport.TokenPrefix))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	m[member] = value
	if raw, err = json.Marshal(m); err != nil {
		t.Fatal(err)
	}
	return transport.TokenPrefix + base64.RawURLEncoding.EncodeToString(raw)
}

func TestEnrollRefusesAnotherController(t *testing.T) {
	ctrl := newTestController(t, time.Now().UTC().Truncate(time.Second))
	ctrl.addHost("H1", "pending")
	token := ctrl.token("H1")
	dir := filepath.Join(t.TempDir(), "agent")
	ctx := context.Background()
	other := newTestAuthority(t)
	for name, bad := range map[string]string{
		"root fingerprint": tamper(t, token, "f", transport.Fingerprint(other.cert)),
		"controller SAN":   tamper(t, token, "s", transport.ControllerURI("SOMEONEELSE")),
	} {
		if _, err := Enroll(ctx, dir, bad); err == nil || !strings.Contains(err.Error(), "transport:") {
			t.Errorf("%s: Enroll = %v, want the handshake refused", name, err)
		}
	}
	// Nothing reached the controller, so the token still enrolls.
	if _, err := Enroll(ctx, dir, token); err != nil {
		t.Fatalf("Enroll with the real token: %v", err)
	}
}

// signCSR signs the CSR's key with a's root for uri, as a controller of
// another authority would.
func (a *testAuthority) signCSR(t *testing.T, csrPEM, uri string) string {
	t.Helper()
	block, _ := pem.Decode([]byte(csrPEM))
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(uri)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(7), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, URIs: []*url.URL{u},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, a.cert, csr.PublicKey, a.key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

// Spec Verification "enrollment": a returned ca with the wrong fingerprint,
// and certificates the agent must not accept.
func TestEnrollChecksTheAnswer(t *testing.T) {
	ctrl := newTestController(t, time.Now().UTC().Truncate(time.Second))
	ctrl.addHost("H2", "pending")
	other := newTestAuthority(t)
	otherRoot := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: other.cert.Raw}))
	cases := []struct {
		name, host string
		rewrite    func(host string, req transport.EnrollRequest, r *transport.EnrollResponse)
	}{
		// A consistent pair from another authority: only the fingerprint
		// check refuses it.
		{"another ca", "HA", func(host string, req transport.EnrollRequest, r *transport.EnrollResponse) {
			r.CA, r.Certificate = otherRoot, other.signCSR(t, req.CSR, transport.HostURI(host))
		}},
		{"another authority's certificate", "HB", func(host string, req transport.EnrollRequest, r *transport.EnrollResponse) {
			r.Certificate = other.signCSR(t, req.CSR, transport.HostURI(host))
		}},
		{"another host's certificate", "HC", func(_ string, req transport.EnrollRequest, r *transport.EnrollResponse) {
			err := ctrl.db.Write(context.Background(), func(tx *sql.Tx) error {
				cert, err := ctrl.auth.IssueAgentCert(context.Background(), tx, "H2", []byte(req.CSR))
				r.Certificate = string(cert)
				return err
			})
			if err != nil {
				t.Error(err)
			}
		}},
		{"no certificate", "HD", func(_ string, _ transport.EnrollRequest, r *transport.EnrollResponse) { r.Certificate = "" }},
	}
	for _, c := range cases {
		ctrl.addHost(c.host, "pending")
		token := ctrl.token(c.host)
		ctrl.mu.Lock()
		ctrl.rewrite = func(req transport.EnrollRequest, r *transport.EnrollResponse) { c.rewrite(c.host, req, r) }
		ctrl.mu.Unlock()
		dir := filepath.Join(t.TempDir(), "agent")
		if _, err := Enroll(context.Background(), dir, token); err == nil {
			t.Errorf("%s: Enroll accepted the answer", c.name)
		}
		if _, err := LoadCredentials(dir); !errors.Is(err, ErrNotEnrolled) {
			t.Errorf("%s: credentials were saved: %v", c.name, err)
		}
	}
}

func TestReadTokenFile(t *testing.T) {
	dir := t.TempDir()
	uid := os.Getuid()
	write := func(name, content string, mode os.FileMode) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
		return p
	}
	for _, mode := range []os.FileMode{0o600, 0o400} {
		p := write("ok"+mode.String(), "  dpe1.token\n", mode)
		if got, err := ReadTokenFile(p, uid); err != nil || got != "dpe1.token" {
			t.Errorf("mode %v: ReadTokenFile = %q, %v", mode, got, err)
		}
	}
	fifo := filepath.Join(dir, "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	refused := map[string]struct {
		path string
		uid  int
	}{
		"group readable": {write("group", "dpe1.token", 0o640), uid},
		"world readable": {write("world", "dpe1.token", 0o604), uid},
		"another owner":  {write("owner", "dpe1.token", 0o600), uid + 1},
		"empty":          {write("empty", " \n", 0o600), uid},
		"directory":      {dir, uid},
		"fifo":           {fifo, uid},
		"missing":        {filepath.Join(dir, "missing"), uid},
	}
	for name, c := range refused {
		if got, err := ReadTokenFile(c.path, c.uid); err == nil || !strings.HasPrefix(err.Error(), "daemon: ") {
			t.Errorf("%s: ReadTokenFile = %q, %v; want a refusal", name, got, err)
		}
	}
}
