package fleet

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/transport"
)

// TokenTTL is how long an enrollment token works (spec section 3).
const TokenTTL = time.Hour

// errTokenInvalid ends an enrollment transaction; Enroll answers every
// refusal with the same code, so a caller learns nothing about which check
// failed.
var errTokenInvalid = errors.New("fleet: enrollment token refused")

// IssueToken creates or replaces hostID's enrollment token inside tx and
// returns it with its expiry. address is the host:port the agent dials.
// Only the SHA-256 of the token's 128-bit secret is stored; a secret that
// random needs no slow hash. It checks no host state: the host actions apply
// the state rules. An unknown host is not_found.
func (a *Authority) IssueToken(ctx context.Context, tx *sql.Tx, hostID, address string) (string, time.Time, error) {
	if address == "" {
		return "", time.Time{}, errcode.New(errcode.InvalidInput, "an enrollment token needs the controller address the agent dials")
	}
	var one int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM core_hosts WHERE id = ?`, hostID).Scan(&one)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", time.Time{}, errcode.New(errcode.NotFound, "no host "+hostID)
	case err != nil:
		return "", time.Time{}, fmt.Errorf("fleet: issue token: read host: %w", err)
	}
	secret := make([]byte, 16)
	if _, err := rand.Read(secret); err != nil {
		return "", time.Time{}, fmt.Errorf("fleet: issue token: secret: %w", err)
	}
	token := transport.Token{
		Address:       address,
		Fingerprint:   a.fingerprint,
		ControllerSAN: a.ControllerSAN(),
		HostID:        hostID,
		Secret:        base64.RawURLEncoding.EncodeToString(secret),
	}
	clear(secret)
	hash, err := token.SecretHash()
	if err != nil {
		return "", time.Time{}, fmt.Errorf("fleet: issue token: %w", err)
	}
	now := a.clock.Now()
	expires := now.Add(TokenTTL)
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO core_enroll_tokens (host_id, hash, expires_at, consumed_at, created_at) VALUES (?, ?, ?, NULL, ?)
		 ON CONFLICT (host_id) DO UPDATE SET hash = excluded.hash, expires_at = excluded.expires_at,
		   consumed_at = NULL, created_at = excluded.created_at`,
		hostID, hash, formatTime(expires), formatTime(now)); err != nil {
		return "", time.Time{}, fmt.Errorf("fleet: issue token: store: %w", err)
	}
	return token.String(), expires, nil
}

// Enrollment is what a consumed token yields: the agent's certificate and
// the root it chains to, both PEM.
type Enrollment struct {
	HostID      string
	Certificate []byte
	CA          []byte
}

// Enroll consumes token and issues the host's certificate in one write
// transaction (spec section 3, Consumption): the token must be the host's
// current one, unconsumed and unexpired, and the host pending or revoked;
// the host becomes enrolled. A concurrent IssueToken or RevokeHost
// serialises on the same writer, so a replaced or revoked token can never
// enroll. Every failure of the token or of the CSR is enroll.token_invalid.
func (a *Authority) Enroll(ctx context.Context, token string, csrPEM []byte) (Enrollment, error) {
	refused := func(cause error) (Enrollment, error) {
		return Enrollment{}, errcode.Wrap(errcode.EnrollTokenInvalid, cause, "the enrollment token is not valid")
	}
	t, err := transport.ParseToken(token)
	if err != nil {
		return refused(err)
	}
	if t.ControllerSAN != a.ControllerSAN() || t.Fingerprint != a.fingerprint {
		return refused(errors.New("fleet: the token names another controller"))
	}
	hash, err := t.SecretHash()
	if err != nil {
		return refused(err)
	}
	if _, err := csrKey(csrPEM); err != nil {
		return refused(err)
	}
	var cert []byte
	err = a.db.Write(ctx, func(tx *sql.Tx) error {
		var stored []byte
		var expires, state string
		var consumed sql.NullString
		err := tx.QueryRowContext(ctx,
			`SELECT t.hash, t.expires_at, t.consumed_at, h.state
			 FROM core_enroll_tokens t JOIN core_hosts h ON h.id = t.host_id WHERE t.host_id = ?`,
			t.HostID).Scan(&stored, &expires, &consumed, &state)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return errTokenInvalid
		case err != nil:
			return fmt.Errorf("fleet: enroll: read token: %w", err)
		}
		now := a.clock.Now()
		if subtle.ConstantTimeCompare(stored, hash) != 1 || consumed.Valid || expires <= formatTime(now) ||
			(state != core.HostPending && state != core.HostRevoked) {
			return errTokenInvalid
		}
		res, err := tx.ExecContext(ctx,
			`UPDATE core_enroll_tokens SET consumed_at = ? WHERE host_id = ? AND hash = ? AND consumed_at IS NULL`,
			formatTime(now), t.HostID, hash)
		if err != nil {
			return fmt.Errorf("fleet: enroll: consume token: %w", err)
		}
		if n, err := res.RowsAffected(); err != nil {
			return fmt.Errorf("fleet: enroll: consume token: %w", err)
		} else if n != 1 {
			return errTokenInvalid
		}
		if cert, err = a.IssueAgentCert(ctx, tx, t.HostID, csrPEM); err != nil {
			var coded *errcode.Error
			if errors.As(err, &coded) && coded.Code == errcode.InvalidInput {
				return fmt.Errorf("%w: %w", errTokenInvalid, err)
			}
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE core_hosts SET state = ?, enrolled_at = ? WHERE id = ?`,
			core.HostEnrolled, formatTime(now), t.HostID); err != nil {
			return fmt.Errorf("fleet: enroll: mark host enrolled: %w", err)
		}
		return nil
	})
	switch {
	case errors.Is(err, errTokenInvalid):
		return refused(err)
	case err != nil:
		return Enrollment{}, err
	}
	return Enrollment{HostID: t.HostID, Certificate: cert, CA: a.RootPEM()}, nil
}
