package fleet

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/pem"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/transport"
)

// IssueAgentCert checks csrPEM and, inside tx, issues hostID a certificate
// and inserts its core_agent_certs row. The certificate is valid
// CertValidity, EKU clientAuth only, with the URI SAN transport.HostURI(hostID)
// and nothing else and a 128-bit random serial. Only the CSR's key is used:
// its subject and extensions are ignored, because the host a certificate
// names comes from the token or the session, never from the agent. A CSR that
// is not one PEM CERTIFICATE REQUEST block, whose signature does not verify
// or whose key is not Ed25519 fails with request.invalid_input. It returns
// the certificate in PEM.
func (a *Authority) IssueAgentCert(ctx context.Context, tx *sql.Tx, hostID string, csrPEM []byte) ([]byte, error) {
	pub, err := csrKey(csrPEM)
	if err != nil {
		return nil, err
	}
	san, err := url.Parse(transport.HostURI(hostID))
	if err != nil {
		return nil, fmt.Errorf("fleet: agent certificate: %w", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, err
	}
	now := a.clock.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "darkpanel host " + hostID},
		URIs:                  []*url.URL{san},
		NotBefore:             now.Add(-backdate),
		NotAfter:              now.Add(CertValidity),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, a.root, pub, a.key)
	if err != nil {
		return nil, fmt.Errorf("fleet: agent certificate: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("fleet: agent certificate: %w", err)
	}
	// The listener identifies a peer by HostIDOf alone, so a host id it
	// cannot read back must never be certified.
	if id, err := transport.HostIDOf(cert); err != nil || id != hostID {
		return nil, fmt.Errorf("fleet: agent certificate: host id %q cannot be named in a certificate", hostID)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO core_agent_certs (serial, host_id, not_before, not_after, created_at) VALUES (?, ?, ?, ?, ?)`,
		transport.SerialText(serial), hostID, formatTime(cert.NotBefore), formatTime(cert.NotAfter), formatTime(now)); err != nil {
		return nil, fmt.Errorf("fleet: agent certificate: store: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), nil
}

// csrKey returns the Ed25519 key of a CSR whose signature verifies.
func csrKey(csrPEM []byte) (ed25519.PublicKey, error) {
	block, rest := pem.Decode(csrPEM)
	if block == nil || block.Type != "CERTIFICATE REQUEST" || len(bytes.TrimSpace(rest)) != 0 {
		return nil, errcode.New(errcode.InvalidInput, "the certificate request must be one PEM CERTIFICATE REQUEST block")
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return nil, errcode.Wrap(errcode.InvalidInput, err, "the certificate request does not parse")
	}
	if err := csr.CheckSignature(); err != nil {
		return nil, errcode.Wrap(errcode.InvalidInput, err, "the certificate request's signature does not verify")
	}
	pub, ok := csr.PublicKey.(ed25519.PublicKey)
	if !ok {
		return nil, errcode.New(errcode.InvalidInput, "the certificate request's key must be Ed25519")
	}
	return pub, nil
}

// Renew issues an enrolled host a new certificate in a write transaction of
// its own. Earlier serials stay valid until Handshake sees the new one or
// they expire, so an agent that loses the answer keeps its old certificate.
// A host that is not enrolled fails with auth.forbidden.
func (a *Authority) Renew(ctx context.Context, hostID string, csrPEM []byte) ([]byte, error) {
	var out []byte
	err := a.db.Write(ctx, func(tx *sql.Tx) error {
		var state string
		err := tx.QueryRowContext(ctx, `SELECT state FROM core_hosts WHERE id = ?`, hostID).Scan(&state)
		switch {
		case errors.Is(err, sql.ErrNoRows) || err == nil && state != core.HostEnrolled:
			return errcode.New(errcode.Forbidden, fmt.Sprintf("host %s is not enrolled", hostID))
		case err != nil:
			return fmt.Errorf("fleet: renew: read host: %w", err)
		}
		out, err = a.IssueAgentCert(ctx, tx, hostID, csrPEM)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// CheckPeer is the agent listener's transport.PeerCheck: nil when hostID is
// enrolled and serial is its unrevoked certificate with the current time
// before not_after; otherwise auth.forbidden.
func (a *Authority) CheckPeer(ctx context.Context, hostID, serial string) error {
	var ok bool
	err := a.db.Read().QueryRowContext(ctx,
		`SELECT h.state = ? AND c.revoked_at IS NULL AND c.not_after > ?
		 FROM core_agent_certs c JOIN core_hosts h ON h.id = c.host_id
		 WHERE c.serial = ? AND c.host_id = ?`,
		core.HostEnrolled, formatTime(a.clock.Now()), serial, hostID).Scan(&ok)
	switch {
	case errors.Is(err, sql.ErrNoRows) || err == nil && !ok:
		return errcode.New(errcode.Forbidden, fmt.Sprintf("certificate %s of host %s is not valid", serial, hostID))
	case err != nil:
		return fmt.Errorf("fleet: check peer: %w", err)
	}
	return nil
}

// Handshake records a successful handshake with serial: every other
// unrevoked serial of hostID issued before it is revoked, because the agent
// has switched to the newer certificate. A serial issued after it, such as
// a renewal the agent has not saved yet, stays valid.
func (a *Authority) Handshake(ctx context.Context, hostID, serial string) error {
	err := a.db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`UPDATE core_agent_certs SET revoked_at = ?
			 WHERE host_id = ? AND serial <> ? AND revoked_at IS NULL
			   AND created_at < (SELECT created_at FROM core_agent_certs WHERE serial = ? AND host_id = ?)`,
			formatTime(a.clock.Now()), hostID, serial, serial, hostID)
		return err
	})
	if err != nil {
		return fmt.Errorf("fleet: handshake: %w", err)
	}
	return nil
}

// RevokeHost sets revoked_at on every unrevoked serial of hostID, the host's
// state to revoked and deletes its enrollment token, inside tx. A revoked
// host may enroll, so a token issued before the revocation must not outlive
// it. An unknown host is not_found.
func RevokeHost(ctx context.Context, tx *sql.Tx, hostID string, now time.Time) error {
	res, err := tx.ExecContext(ctx, `UPDATE core_hosts SET state = ? WHERE id = ?`, core.HostRevoked, hostID)
	if err != nil {
		return fmt.Errorf("fleet: revoke host: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("fleet: revoke host: %w", err)
	} else if n == 0 {
		return errcode.New(errcode.NotFound, "no host "+hostID)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE core_agent_certs SET revoked_at = ? WHERE host_id = ? AND revoked_at IS NULL`,
		formatTime(now), hostID); err != nil {
		return fmt.Errorf("fleet: revoke host: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM core_enroll_tokens WHERE host_id = ?`, hostID); err != nil {
		return fmt.Errorf("fleet: revoke host: %w", err)
	}
	return nil
}
