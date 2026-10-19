package fleet

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/transport"
)

// PurposeAgent is the core_ca purpose of the agent certificate root (spec
// section 2). It is separate from the web root (ADR 0010).
const PurposeAgent = "agent"

// Certificate lifetimes (spec section 2).
const (
	CertValidity   = 365 * 24 * time.Hour // controller listener and agent certificates
	CertRenewAfter = CertValidity * 2 / 3
)

// backdate is how far before its issue time a certificate becomes valid, so
// a peer whose clock runs a little behind still accepts it.
const backdate = time.Hour

// timeFormat is the fixed-width RFC 3339 UTC layout every core table uses,
// so times compare as text.
const timeFormat = "2006-01-02T15:04:05.000000000Z"

func formatTime(t time.Time) string { return t.UTC().Format(timeFormat) }

// Authority is the controller's agent certificate root: Ed25519, valid 10
// years, used to issue the agent listener's certificate and every agent's
// client certificate. It also holds the controller id that both kinds of
// certificate name.
type Authority struct {
	db           *store.DB
	clock        core.Clock
	controllerID string
	root         *x509.Certificate
	key          ed25519.PrivateKey
	pem          []byte
	pool         *x509.CertPool
	fingerprint  string

	mu       sync.Mutex // serialises listener certificate issuance
	listener *tls.Certificate
	issuedAt time.Time
}

// LoadOrCreateAuthority loads the agent root from core_ca and the controller
// id from core_meta (key controller_id), creating either on the first start
// inside a write transaction that finds none, so starts that overlap agree.
// The root's private key is stored sealed with associated data
// core_ca.key_ct.<row id>.
func LoadOrCreateAuthority(ctx context.Context, db *store.DB, sealer core.Sealer, clock core.Clock) (*Authority, error) {
	row, foundRoot, err := selectRoot(ctx, db.Read())
	if err != nil {
		return nil, err
	}
	id, foundID, err := selectControllerID(ctx, db.Read())
	if err != nil {
		return nil, err
	}
	if !foundRoot || !foundID {
		now := clock.Now()
		fresh, err := newRoot(sealer, now)
		if err != nil {
			return nil, err
		}
		freshID := ulid.MustNew(ulid.Timestamp(now), rand.Reader).String()
		err = db.Write(ctx, func(tx *sql.Tx) error {
			if row, foundRoot, err = selectRoot(ctx, tx); err != nil {
				return err
			}
			if !foundRoot {
				row = fresh
				if _, err := tx.ExecContext(ctx,
					`INSERT INTO core_ca (id, purpose, cert_pem, key_ct, created_at) VALUES (?, ?, ?, ?, ?)`,
					row.id, PurposeAgent, row.certPEM, row.keyCT, row.createdAt); err != nil {
					return fmt.Errorf("fleet: agent authority: store root: %w", err)
				}
			}
			if id, foundID, err = selectControllerID(ctx, tx); err != nil {
				return err
			}
			if !foundID {
				id = freshID
				if _, err := tx.ExecContext(ctx,
					`INSERT INTO core_meta (key, value) VALUES ('controller_id', ?)`, id); err != nil {
					return fmt.Errorf("fleet: agent authority: store controller id: %w", err)
				}
			}
			return nil
		})
		if err != nil {
			if !strings.HasPrefix(err.Error(), "fleet: agent authority") {
				err = fmt.Errorf("fleet: agent authority: %w", err)
			}
			return nil, err
		}
	}
	a, err := openRoot(row, sealer)
	if err != nil {
		return nil, err
	}
	a.db, a.clock, a.controllerID = db, clock, id
	return a, nil
}

// ControllerID is the controller's ULID, named by its listener certificate.
func (a *Authority) ControllerID() string { return a.controllerID }

// ControllerSAN is the URI SAN of the controller's listener certificate,
// which agents pin.
func (a *Authority) ControllerSAN() string { return transport.ControllerURI(a.controllerID) }

// Root returns the agent root certificate.
func (a *Authority) Root() *x509.Certificate { return a.root }

// RootPEM returns the root certificate as one PEM CERTIFICATE block.
func (a *Authority) RootPEM() []byte { return append([]byte(nil), a.pem...) }

// Pool returns a pool holding the agent root alone.
func (a *Authority) Pool() *x509.CertPool { return a.pool.Clone() }

// Fingerprint returns the lowercase hex SHA-256 of the root's DER encoding.
func (a *Authority) Fingerprint() string { return a.fingerprint }

// GetCertificate serves tls.Config.GetCertificate for the agent listener with
// the chain [leaf, root]. The leaf is kept in memory only: it is issued on
// the first call and reissued once CertRenewAfter has passed since then, so
// a long-running controller never presents an expired one.
func (a *Authority) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.clock.Now()
	if a.listener != nil && now.Sub(a.issuedAt) < CertRenewAfter {
		return a.listener, nil
	}
	cert, err := a.issueListener(now)
	if err != nil {
		return nil, err
	}
	a.listener, a.issuedAt = cert, now
	return cert, nil
}

func (a *Authority) issueListener(now time.Time) (*tls.Certificate, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("fleet: listener certificate: generate key: %w", err)
	}
	san, err := url.Parse(a.ControllerSAN())
	if err != nil {
		return nil, fmt.Errorf("fleet: listener certificate: %w", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "darkpanel controller " + a.controllerID},
		URIs:                  []*url.URL{san},
		NotBefore:             now.Add(-backdate),
		NotAfter:              now.Add(CertValidity),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, a.root, pub, a.key)
	if err != nil {
		return nil, fmt.Errorf("fleet: listener certificate: %w", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("fleet: listener certificate: %w", err)
	}
	return &tls.Certificate{Certificate: [][]byte{der, a.root.Raw}, PrivateKey: priv, Leaf: leaf}, nil
}

type rootRow struct {
	id, certPEM, createdAt string
	keyCT                  []byte
}

type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func selectRoot(ctx context.Context, q querier) (rootRow, bool, error) {
	var r rootRow
	err := q.QueryRowContext(ctx,
		`SELECT id, cert_pem, key_ct, created_at FROM core_ca WHERE purpose = ? ORDER BY created_at, id LIMIT 1`,
		PurposeAgent).Scan(&r.id, &r.certPEM, &r.keyCT, &r.createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return rootRow{}, false, nil
	}
	if err != nil {
		return rootRow{}, false, fmt.Errorf("fleet: agent authority: read core_ca: %w", err)
	}
	return r, true, nil
}

func selectControllerID(ctx context.Context, q querier) (string, bool, error) {
	var id string
	err := q.QueryRowContext(ctx, `SELECT value FROM core_meta WHERE key = 'controller_id'`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("fleet: agent authority: read controller id: %w", err)
	}
	return id, true, nil
}

func keyAD(id string) string { return "core_ca.key_ct." + id }

func newRoot(sealer core.Sealer, now time.Time) (rootRow, error) {
	id := ulid.MustNew(ulid.Timestamp(now), rand.Reader).String()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return rootRow{}, fmt.Errorf("fleet: agent authority: generate key: %w", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return rootRow{}, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "darkpanel agent root " + id},
		NotBefore:             now.Add(-backdate),
		NotAfter:              now.AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, pub, priv)
	if err != nil {
		return rootRow{}, fmt.Errorf("fleet: agent authority: create root: %w", err)
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return rootRow{}, fmt.Errorf("fleet: agent authority: encode key: %w", err)
	}
	defer clear(pkcs8)
	ct, err := sealer.Seal(keyAD(id), pkcs8)
	if err != nil {
		return rootRow{}, fmt.Errorf("fleet: agent authority: seal key: %w", err)
	}
	return rootRow{
		id:        id,
		certPEM:   string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		keyCT:     ct,
		createdAt: formatTime(now),
	}, nil
}

func openRoot(r rootRow, sealer core.Sealer) (*Authority, error) {
	block, _ := pem.Decode([]byte(r.certPEM))
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("fleet: agent authority: root %s: cert_pem holds no certificate", r.id)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("fleet: agent authority: root %s: %w", r.id, err)
	}
	if !cert.IsCA {
		return nil, fmt.Errorf("fleet: agent authority: root %s is not a CA certificate", r.id)
	}
	pkcs8, err := sealer.Open(keyAD(r.id), r.keyCT)
	if err != nil {
		return nil, fmt.Errorf("fleet: agent authority: root %s: open key: %w", r.id, err)
	}
	defer clear(pkcs8)
	parsed, err := x509.ParsePKCS8PrivateKey(pkcs8)
	if err != nil {
		return nil, fmt.Errorf("fleet: agent authority: root %s: parse key: %w", r.id, err)
	}
	key, ok := parsed.(ed25519.PrivateKey)
	if !ok || !key.Public().(ed25519.PublicKey).Equal(cert.PublicKey) {
		return nil, fmt.Errorf("fleet: agent authority: root %s: the key does not match the certificate", r.id)
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return &Authority{
		root:        cert,
		key:         key,
		pem:         pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}),
		pool:        pool,
		fingerprint: transport.Fingerprint(cert),
	}, nil
}

func randomSerial() (*big.Int, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("fleet: serial: %w", err)
	}
	return serial, nil
}
