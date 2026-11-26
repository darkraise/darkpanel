package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// StepUpWindow is how long a step-up keeps its session elevated (M11 spec
// section 1.1).
const StepUpWindow = 15 * time.Minute

// ErrStepUpUnavailable is the refusal of step-up to a caller that cannot give
// a code: an API token, or a user who never confirmed an authenticator.
func ErrStepUpUnavailable() *errcode.Error {
	return errcode.New(errcode.StepUpUnavailable, "Set up an authenticator to use the console and jobs")
}

func errStepUpWait() *errcode.Error {
	return errcode.New(errcode.StepUpWait, "Wait for the next code")
}

func errWrongCode() *errcode.Error {
	return errcode.New(errcode.InvalidCredentials, "wrong authenticator code")
}

// ErrStepUpRequired is the refusal of a step-up action or endpoint to a
// session that is not elevated.
func ErrStepUpRequired() error {
	return errcode.New(errcode.StepUpRequired, "enter an authenticator code to continue")
}

// codeConfirmed reports whether the user has confirmed an authenticator: an
// active user who has typed a code at least once. An active user who signed
// up through OIDC or Tailscale without a code holds a secret never confirmed.
func (r userRecord) codeConfirmed() bool { return r.usable() && r.lastStep > 0 }

// StepUp checks the TOTP code of p, a browser session's user, and elevates
// sessionID for StepUpWindow. A token principal is refused before any code
// is checked. A code of a step at or before the last one accepted, at
// sign-in or step-up, answers auth.step_up_wait and is not counted; any
// other wrong code counts against the sign-in limiter of the user and
// source. The accepted step is stored in the same transaction as the
// elevation, so two step-ups racing with one code cannot both succeed.
func (s *Service) StepUp(ctx context.Context, p core.Principal, sessionID, code string) error {
	if p.TokenID != "" || p.UserID == "" {
		return ErrStepUpUnavailable()
	}
	raw, ok := decodeSessionID(sessionID)
	if !ok {
		return errAuthRequired()
	}
	rec, err := loadUserBy(ctx, s.db.Read(), `id = ?`, p.UserID)
	if errcode.From(err).Code == errcode.NotFound {
		return errAuthRequired()
	}
	if err != nil {
		return err
	}
	if !rec.codeConfirmed() {
		return ErrStepUpUnavailable()
	}
	source := limitSource(ctx, p.Source)
	if d := s.limiter.blockedFor(rec.Username, source, false); d > 0 {
		return errRateLimited(d)
	}
	err = s.db.Write(ctx, func(tx *sql.Tx) error {
		cur, err := loadUserBy(ctx, tx, `id = ?`, p.UserID)
		if err != nil {
			return err
		}
		if !cur.codeConfirmed() {
			return ErrStepUpUnavailable()
		}
		key, err := s.sealer.Open(totpAD(cur.ID), cur.totpCT)
		if err != nil {
			return fmt.Errorf("auth: open TOTP secret: %w", err)
		}
		now := s.clock.Now()
		step, ok := matchTOTP(key, code, now, cur.lastStep)
		if !ok {
			if totpReplayed(key, code, now, cur.lastStep) {
				return errStepUpWait()
			}
			return errWrongCode()
		}
		if _, err := tx.ExecContext(ctx, `UPDATE core_users SET totp_last_step = ? WHERE id = ?`, step, cur.ID); err != nil {
			return fmt.Errorf("auth: store TOTP step: %w", err)
		}
		res, err := tx.ExecContext(ctx, `UPDATE core_sessions SET elevated_until = ? WHERE id_hash = ? AND user_id = ?`,
			formatTime(now.Add(StepUpWindow)), sessionHash(raw), cur.ID)
		if err != nil {
			return fmt.Errorf("auth: elevate session: %w", err)
		}
		if n, err := res.RowsAffected(); err != nil || n != 1 {
			return errAuthRequired()
		}
		return nil
	})
	if errcode.From(err).Code == errcode.InvalidCredentials {
		s.limiter.failFor(rec.Username, source, false)
	}
	return err
}

// Elevated reports whether sessionID is inside its step-up window. An
// unknown session is not elevated.
func (s *Service) Elevated(ctx context.Context, sessionID string) (bool, error) {
	raw, ok := decodeSessionID(sessionID)
	if !ok {
		return false, nil
	}
	var until sql.NullString
	err := s.db.Read().QueryRowContext(ctx, `SELECT elevated_until FROM core_sessions WHERE id_hash = ?`, sessionHash(raw)).Scan(&until)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !until.Valid) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("auth: load elevation: %w", err)
	}
	t, err := parseTime(until.String)
	if err != nil {
		return false, err
	}
	return s.clock.Now().Before(t), nil
}
