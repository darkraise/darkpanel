package listen

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// PurposeWeb is the core_ca purpose of the web certificate root (ADR 0010).
const PurposeWeb = "web"

// backdate is how far before its issue time a certificate becomes valid, so
// a client whose clock runs a little behind still accepts it.
const backdate = time.Hour

// WebCA is the read-only view of the web root that the controller socket's
// web-ca route serves.
type WebCA interface {
	// RootPEM is the root certificate as one PEM CERTIFICATE block.
	RootPEM() []byte
	// Fingerprint is the lowercase hex SHA-256 of the root's DER encoding.
	Fingerprint() string
}

// Authority is the controller's web certificate root: ECDSA P-256, valid 10
// years, used only to issue the HTTPS listeners' leaf (ADR 0010).
type Authority struct {
	cert        *x509.Certificate
	key         *ecdsa.PrivateKey
	pem         []byte
	fingerprint string
}

var _ WebCA = (*Authority)(nil)

// RootPEM returns the root certificate in PEM.
func (a *Authority) RootPEM() []byte { return bytes.Clone(a.pem) }

// Fingerprint returns the lowercase hex SHA-256 of the root's DER encoding.
func (a *Authority) Fingerprint() string { return a.fingerprint }

// LoadOrCreateAuthority loads the web root from core_ca, creating it on the
// first start. Its private key is stored sealed with associated data
// core_ca.key_ct.<row id>. Starts that overlap agree on one root: the row is
// inserted only inside a write transaction that finds none.
func LoadOrCreateAuthority(ctx context.Context, db *store.DB, sealer core.Sealer, clock core.Clock) (*Authority, error) {
	row, found, err := selectRoot(ctx, db.Read())
	if err != nil {
		return nil, err
	}
	if !found {
		fresh, err := newRoot(sealer, clock.Now())
		if err != nil {
			return nil, err
		}
		err = db.Write(ctx, func(tx *sql.Tx) error {
			var existing bool
			row, existing, err = selectRoot(ctx, tx)
			if err != nil || existing {
				return err
			}
			row = fresh
			_, err = tx.ExecContext(ctx,
				`INSERT INTO core_ca (id, purpose, cert_pem, key_ct, created_at) VALUES (?, ?, ?, ?, ?)`,
				row.id, PurposeWeb, row.certPEM, row.keyCT, row.createdAt)
			if err != nil {
				return fmt.Errorf("listen: web authority: store root: %w", err)
			}
			return nil
		})
		if err != nil {
			if !strings.HasPrefix(err.Error(), "listen: web authority") {
				err = fmt.Errorf("listen: web authority: %w", err)
			}
			return nil, err
		}
	}
	return openRoot(row, sealer)
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
		PurposeWeb).Scan(&r.id, &r.certPEM, &r.keyCT, &r.createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return rootRow{}, false, nil
	}
	if err != nil {
		return rootRow{}, false, fmt.Errorf("listen: web authority: read core_ca: %w", err)
	}
	return r, true, nil
}

func keyAD(id string) string { return "core_ca.key_ct." + id }

func newRoot(sealer core.Sealer, now time.Time) (rootRow, error) {
	id := ulid.MustNew(ulid.Timestamp(now), rand.Reader).String()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return rootRow{}, fmt.Errorf("listen: web authority: generate key: %w", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return rootRow{}, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "darkpanel web root " + id},
		NotBefore:             now.Add(-backdate),
		NotAfter:              now.AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return rootRow{}, fmt.Errorf("listen: web authority: create root: %w", err)
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return rootRow{}, fmt.Errorf("listen: web authority: encode key: %w", err)
	}
	defer clear(pkcs8)
	ct, err := sealer.Seal(keyAD(id), pkcs8)
	if err != nil {
		return rootRow{}, fmt.Errorf("listen: web authority: seal key: %w", err)
	}
	return rootRow{
		id:        id,
		certPEM:   string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		keyCT:     ct,
		createdAt: now.UTC().Format(time.RFC3339),
	}, nil
}

func openRoot(r rootRow, sealer core.Sealer) (*Authority, error) {
	block, _ := pem.Decode([]byte(r.certPEM))
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("listen: web authority: root %s: cert_pem holds no certificate", r.id)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("listen: web authority: root %s: %w", r.id, err)
	}
	if !cert.IsCA {
		return nil, fmt.Errorf("listen: web authority: root %s is not a CA certificate", r.id)
	}
	pkcs8, err := sealer.Open(keyAD(r.id), r.keyCT)
	if err != nil {
		return nil, fmt.Errorf("listen: web authority: root %s: open key: %w", r.id, err)
	}
	defer clear(pkcs8)
	parsed, err := x509.ParsePKCS8PrivateKey(pkcs8)
	if err != nil {
		return nil, fmt.Errorf("listen: web authority: root %s: parse key: %w", r.id, err)
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok || key.Curve != elliptic.P256() || !key.PublicKey.Equal(cert.PublicKey) {
		return nil, fmt.Errorf("listen: web authority: root %s: the key does not match the certificate", r.id)
	}
	sum := sha256.Sum256(cert.Raw)
	return &Authority{
		cert:        cert,
		key:         key,
		pem:         pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}),
		fingerprint: hex.EncodeToString(sum[:]),
	}, nil
}

func randomSerial() (*big.Int, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("listen: web authority: serial: %w", err)
	}
	return serial, nil
}
