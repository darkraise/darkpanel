package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// Session limits (spec section 9).
const (
	SessionIdle      = time.Hour
	SessionLifetime  = 12 * time.Hour
	LastSeenInterval = time.Minute
	sessionIDSize    = 32
	maxUserAgent     = 512
)

var b64url = base64.RawURLEncoding

func errAuthRequired() *errcode.Error {
	return errcode.New(errcode.AuthRequired, "authentication required")
}

// newSessionID returns a new session id, 32 random bytes in unpadded
// base64url, and those bytes.
func newSessionID() (string, []byte) {
	raw := make([]byte, sessionIDSize)
	_, _ = rand.Read(raw)
	return b64url.EncodeToString(raw), raw
}

func decodeSessionID(id string) ([]byte, bool) {
	raw, err := b64url.DecodeString(id)
	if err != nil || len(raw) != sessionIDSize {
		return nil, false
	}
	return raw, true
}

// sessionHash is the core_sessions key: the hex SHA-256 of the id's bytes.
func sessionHash(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// truncate shortens s to at most n bytes without splitting a UTF-8 sequence.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && cut > n-utf8.UTFMax && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// createSession inserts a session for userID inside tx and returns its id and
// its expiry, the idle limit from now. The user agent is kept to 512 bytes.
func (s *Service) createSession(ctx context.Context, tx *sql.Tx, userID, source, userAgent string) (string, time.Time, error) {
	id, raw := newSessionID()
	now := s.now()
	at := formatTime(now)
	_, err := tx.ExecContext(ctx, `INSERT INTO core_sessions
		(id_hash, user_id, created_at, last_seen_at, expires_at, source_ip, user_agent) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		sessionHash(raw), userID, at, at, formatTime(now.Add(SessionLifetime)), source, truncate(userAgent, maxUserAgent))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("auth: create session: %w", err)
	}
	return id, now.Add(min(SessionIdle, SessionLifetime)), nil
}

// roleScope is the scope a session of a user with role carries (spec section
// 10).
func roleScope(role string) (core.Scope, bool) {
	if role == RoleAdmin {
		return core.ScopeAdmin, true
	}
	return "", false
}

// authenticateSession resolves a session cookie value. An unknown, malformed
// or expired id, or a user that is not active, is auth.required. It moves
// last_seen_at forward when it is a minute or more old.
func (s *Service) authenticateSession(ctx context.Context, id, source string) (Identity, error) {
	raw, ok := decodeSessionID(id)
	if !ok {
		return Identity{}, errAuthRequired()
	}
	key := sessionHash(raw)
	var u User
	var seenText, expText, createdText string
	err := s.db.Read().QueryRowContext(ctx, `SELECT s.last_seen_at, s.expires_at, u.id, u.username, u.role, u.state, u.created_at
		FROM core_sessions s JOIN core_users u ON u.id = s.user_id WHERE s.id_hash = ?`, key).
		Scan(&seenText, &expText, &u.ID, &u.Username, &u.Role, &u.State, &createdText)
	if errors.Is(err, sql.ErrNoRows) {
		return Identity{}, errAuthRequired()
	}
	if err != nil {
		return Identity{}, fmt.Errorf("auth: load session: %w", err)
	}
	seen, err := parseTime(seenText)
	if err != nil {
		return Identity{}, err
	}
	exp, err := parseTime(expText)
	if err != nil {
		return Identity{}, err
	}
	if u.CreatedAt, err = parseTime(createdText); err != nil {
		return Identity{}, err
	}
	now := s.now()
	scope, ok := roleScope(u.Role)
	if !now.Before(exp) || !now.Before(seen.Add(SessionIdle)) || u.State != StateActive || !ok {
		return Identity{}, errAuthRequired()
	}
	if now.Sub(seen) >= LastSeenInterval {
		err := s.db.Write(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `UPDATE core_sessions SET last_seen_at = ? WHERE id_hash = ? AND last_seen_at < ?`,
				formatTime(now), key, formatTime(now))
			return err
		})
		if err != nil {
			return Identity{}, fmt.Errorf("auth: touch session: %w", err)
		}
		seen = now
	}
	if idle := seen.Add(SessionIdle); idle.Before(exp) {
		exp = idle
	}
	return Identity{
		Principal:   core.Principal{Actor: "user:" + u.ID, UserID: u.ID, Scope: scope, Source: source},
		FromSession: true, User: u, ExpiresAt: exp,
	}, nil
}

// endSession deletes the session id and returns its user; ok is false when
// no such session exists.
func (s *Service) endSession(ctx context.Context, id string) (string, bool, error) {
	raw, ok := decodeSessionID(id)
	if !ok {
		return "", false, nil
	}
	var userID string
	err := s.db.Write(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `DELETE FROM core_sessions WHERE id_hash = ? RETURNING user_id`, sessionHash(raw)).Scan(&userID)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("auth: end session: %w", err)
	}
	return userID, true, nil
}

// CSRFToken returns the CSRF token of a session: HMAC-SHA256 of the session
// id's bytes under the derived CSRF key, in unpadded base64url. A malformed id
// has no token ("").
func (s *Service) CSRFToken(sessionID string) string {
	raw, ok := decodeSessionID(sessionID)
	if !ok {
		return ""
	}
	m := hmac.New(sha256.New, s.csrfKey)
	m.Write(raw)
	return b64url.EncodeToString(m.Sum(nil))
}

// VerifyCSRF reports, in constant time, whether token is the session's CSRF
// token.
func (s *Service) VerifyCSRF(sessionID, token string) bool {
	want := s.CSRFToken(sessionID)
	return want != "" && subtle.ConstantTimeCompare([]byte(want), []byte(token)) == 1
}
