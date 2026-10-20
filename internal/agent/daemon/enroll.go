package daemon

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/darkraise/darkpanel/internal/agent/wire"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/transport"
)

// EnrollTimeout bounds one enrollment: the dial, the handshake and the
// request.
const EnrollTimeout = 30 * time.Second

// maxTokenFile is more than any token: a token is about 250 bytes.
const maxTokenFile = 4096

// Enroll consumes token at the controller it names and saves the answer as a
// new credential generation of dir (Paths.AgentTLSDir), spec section 3: the
// key is generated here and never leaves the host; the controller must
// present a chain whose root has the token's fingerprint and a leaf naming
// the token's controller SAN before anything is sent; and the answer's root
// must be that root and its certificate must name the token's host and chain
// to it. A malformed token and every refusal by the controller keep the
// controller's code (enroll.token_invalid, enroll.rate_limited).
func Enroll(ctx context.Context, dir, token string) (*Credentials, error) {
	tok, err := transport.ParseToken(token)
	if err != nil {
		return nil, err
	}
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("daemon: generate the agent key: %w", err)
	}
	csr, err := csrPEM(key)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, EnrollTimeout)
	defer cancel()
	conn, err := transport.Dial(ctx, tok.Address, transport.EnrollConfig(tok.Fingerprint, tok.ControllerSAN))
	if err != nil {
		return nil, err
	}
	var used bool
	dial := func(context.Context) (net.Conn, error) {
		// The enrollment connection carries exactly one request.
		if used {
			return nil, errors.New("daemon: the enrollment connection is used")
		}
		used = true
		return conn, nil
	}
	var out transport.EnrollResponse
	err = post(ctx, dial, transport.EnrollPath, transport.EnrollRequest{Token: tok.String(), CSR: csr}, &out)
	_ = conn.Close()
	if err != nil {
		return nil, err
	}
	ca, err := parseCertificate([]byte(out.CA))
	if err != nil {
		return nil, fmt.Errorf("daemon: the controller's agent root: %w", err)
	}
	if transport.Fingerprint(ca) != tok.Fingerprint {
		return nil, errors.New("daemon: the controller answered with an agent root other than the one the token names")
	}
	cert, err := checkIssued(out.Certificate, ca, tok.HostID)
	if err != nil {
		return nil, err
	}
	c := &Credentials{Key: key, Cert: cert, CA: ca, Controller: Endpoint{Address: tok.Address, SAN: tok.ControllerSAN}}
	if _, err := SaveGeneration(dir, c); err != nil {
		return nil, err
	}
	return c, nil
}

// csrPEM is a certificate request signed by key. The controller ignores
// every identity field, so it carries none.
func csrPEM(key ed25519.PrivateKey) (string, error) {
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, key)
	if err != nil {
		return "", fmt.Errorf("daemon: create the certificate request: %w", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der})), nil
}

// checkIssued parses an issued agent certificate and requires it to name
// hostID and to verify against ca alone for client authentication.
func checkIssued(certPEM string, ca *x509.Certificate, hostID string) (*x509.Certificate, error) {
	cert, err := parseCertificate([]byte(certPEM))
	if err != nil {
		return nil, fmt.Errorf("daemon: the issued certificate: %w", err)
	}
	if id, err := transport.HostIDOf(cert); err != nil || id != hostID {
		return nil, fmt.Errorf("daemon: the issued certificate does not name host %s alone", hostID)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	if _, err := cert.Verify(x509.VerifyOptions{Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		return nil, fmt.Errorf("daemon: the issued certificate: %w", err)
	}
	return cert, nil
}

// post sends one JSON request over a connection from dial and decodes a 200
// answer into out. A refusal in problem details keeps the controller's code.
func post(ctx context.Context, dial func(context.Context) (net.Conn, error), path string, in, out any) error {
	body, err := json.Marshal(in)
	if err != nil {
		return fmt.Errorf("daemon: encode %s: %w", path, err)
	}
	tr := &http.Transport{
		DialContext:            func(ctx context.Context, _, _ string) (net.Conn, error) { return dial(ctx) },
		DisableKeepAlives:      true,
		DisableCompression:     true,
		MaxResponseHeaderBytes: 64 << 10,
	}
	defer tr.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://controller"+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("daemon: %s: %w", path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Transport: tr}).Do(req)
	if err != nil {
		return fmt.Errorf("daemon: %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, wire.MaxBody+1))
	if err != nil {
		return fmt.Errorf("daemon: %s: %w", path, err)
	}
	if len(data) > wire.MaxBody {
		return fmt.Errorf("daemon: %s: the answer exceeds %d bytes", path, wire.MaxBody)
	}
	if resp.StatusCode != http.StatusOK {
		if mt, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type")); err == nil && mt == "application/problem+json" {
			var p struct {
				Code   errcode.Code `json:"code"`
				Detail string       `json:"detail"`
			}
			if json.Unmarshal(data, &p) == nil && p.Code != "" {
				return &errcode.Error{Code: p.Code, Detail: p.Detail}
			}
		}
		return fmt.Errorf("daemon: %s: unexpected answer %s", path, resp.Status)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("daemon: %s: decode the answer: %w", path, err)
	}
	return nil
}

// ReadTokenFile reads an enrollment token from path for darkpanel agent
// enroll --token-file (spec section 3): path must be a regular file owned by
// uid (0 in production) with no permission bits for group or others, so
// mode 0600 or stricter. The checks run on the opened file, so a file
// swapped in after them is never read.
func ReadTokenFile(path string, uid int) (string, error) {
	// O_NONBLOCK keeps a FIFO at path from blocking the open.
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOCTTY, 0) //nolint:gosec // G304: the operator names the token file
	if err != nil {
		return "", fmt.Errorf("daemon: token file: %w", err)
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return "", fmt.Errorf("daemon: token file: %w", err)
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	switch {
	case !st.Mode().IsRegular():
		return "", fmt.Errorf("daemon: token file %s is not a regular file", path)
	case !ok || int(sys.Uid) != uid:
		return "", fmt.Errorf("daemon: token file %s must be owned by uid %d", path, uid)
	case st.Mode().Perm()&0o077 != 0:
		return "", fmt.Errorf("daemon: token file %s has mode %04o; want 0600 or stricter", path, st.Mode().Perm())
	}
	data, err := io.ReadAll(io.LimitReader(f, maxTokenFile+1))
	if err != nil {
		return "", fmt.Errorf("daemon: token file: %w", err)
	}
	token := strings.TrimSpace(string(data))
	switch {
	case len(data) > maxTokenFile:
		return "", fmt.Errorf("daemon: token file %s is larger than a token", path)
	case token == "":
		return "", fmt.Errorf("daemon: token file %s is empty", path)
	}
	return token, nil
}
