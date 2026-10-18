package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/oklog/ulid/v2"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// API token rules (spec section 9).
const (
	TokenPrefix      = "dpt_"
	DefaultTokenDays = 90
	MaxTokenDays     = 365
	MaxTokenName     = 64
	LastUsedInterval = time.Minute
	TokenRetention   = 30 * 24 * time.Hour
	tokenSecretSize  = 32
)

func errTokenNotFound() *errcode.Error { return errcode.New(errcode.NotFound, "token not found") }

func secretHash(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// parseToken splits dpt_<ULID>_<base32 secret> into the id and the secret's
// bytes. Only the canonical encoding is accepted: the decoder ignores the
// last character's four spare bits, which would otherwise give one secret
// several spellings.
func parseToken(token string) (string, []byte, bool) {
	rest, ok := strings.CutPrefix(token, TokenPrefix)
	if !ok {
		return "", nil, false
	}
	id, sec, ok := strings.Cut(rest, "_")
	if !ok || len(id) != ulid.EncodedSize {
		return "", nil, false
	}
	if _, err := ulid.ParseStrict(id); err != nil {
		return "", nil, false
	}
	raw, err := b32.DecodeString(sec)
	if err != nil || len(raw) != tokenSecretSize || b32.EncodeToString(raw) != sec {
		return "", nil, false
	}
	return id, raw, true
}

const tokenColumns = `t.id, t.name, t.scope, t.created_at, t.expires_at, t.last_used_at, t.revoked_at` //nolint:gosec // G101: column names, not a credential

// scanToken reads tokenColumns, then extra destinations, from row.
func scanToken(row interface{ Scan(...any) error }, extra ...any) (TokenInfo, error) {
	var info TokenInfo
	var created, expires string
	var used, revoked sql.NullString
	dest := append([]any{&info.ID, &info.Name, &info.Scope, &created, &expires, &used, &revoked}, extra...)
	if err := row.Scan(dest...); err != nil {
		return TokenInfo{}, err
	}
	var err error
	if info.CreatedAt, err = parseTime(created); err != nil {
		return TokenInfo{}, err
	}
	if info.ExpiresAt, err = parseTime(expires); err != nil {
		return TokenInfo{}, err
	}
	if info.LastUsedAt, err = parseNullTime(used); err != nil {
		return TokenInfo{}, err
	}
	if info.RevokedAt, err = parseNullTime(revoked); err != nil {
		return TokenInfo{}, err
	}
	return info, nil
}

func parseNullTime(s sql.NullString) (*time.Time, error) {
	if !s.Valid {
		return nil, nil
	}
	t, err := parseTime(s.String)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// CreateToken creates an API token of userID and returns its description and
// the token string dpt_<id>_<secret>, the only time the secret is shown. days
// is the lifetime, 1 to 365, and 0 means 90. An invalid name, scope or
// lifetime is request.invalid_input; an unknown or inactive user is
// errcode.NotFound.
func (s *Service) CreateToken(ctx context.Context, userID, name string, scope core.Scope, days int) (TokenInfo, string, error) {
	if n := utf8.RuneCountInString(name); n < 1 || n > MaxTokenName {
		return TokenInfo{}, "", invalidField("/name", fmt.Sprintf("must be 1 to %d characters", MaxTokenName))
	}
	if scope != core.ScopeRead && scope != core.ScopeOperate && scope != core.ScopeAdmin {
		return TokenInfo{}, "", invalidField("/scope", "must be read, operate or admin")
	}
	if days == 0 {
		days = DefaultTokenDays
	}
	if days < 1 || days > MaxTokenDays {
		return TokenInfo{}, "", invalidField("/expiresInDays", fmt.Sprintf("must be 1 to %d", MaxTokenDays))
	}
	raw := make([]byte, tokenSecretSize)
	_, _ = rand.Read(raw)
	now := s.now()
	info := TokenInfo{ID: s.newID(), Name: name, Scope: scope, CreatedAt: now,
		ExpiresAt: now.Add(time.Duration(days) * 24 * time.Hour)}
	err := s.db.Write(ctx, func(tx *sql.Tx) error {
		var state string
		err := tx.QueryRowContext(ctx, `SELECT state FROM core_users WHERE id = ?`, userID).Scan(&state)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && state != StateActive) {
			return errUserNotFound()
		}
		if err != nil {
			return fmt.Errorf("auth: create token: %w", err)
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO core_tokens (id, user_id, name, scope, secret_hash, created_at, expires_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, info.ID, userID, info.Name, string(info.Scope), secretHash(raw),
			formatTime(info.CreatedAt), formatTime(info.ExpiresAt))
		if err != nil {
			return fmt.Errorf("auth: create token: %w", err)
		}
		return nil
	})
	if err != nil {
		return TokenInfo{}, "", err
	}
	return info, TokenPrefix + info.ID + "_" + b32.EncodeToString(raw), nil
}

// ListTokens returns the tokens of userID, newest first, revoked and expired
// ones included until they are purged; none is an empty, non-nil slice.
func (s *Service) ListTokens(ctx context.Context, userID string) ([]TokenInfo, error) {
	rows, err := s.db.Read().QueryContext(ctx, `SELECT `+tokenColumns+` FROM core_tokens t WHERE t.user_id = ? ORDER BY t.id DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("auth: list tokens: %w", err)
	}
	defer func() { _ = rows.Close() }()
	tokens := []TokenInfo{}
	for rows.Next() {
		info, err := scanToken(rows)
		if err != nil {
			return nil, fmt.Errorf("auth: list tokens: %w", err)
		}
		tokens = append(tokens, info)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("auth: list tokens: %w", err)
	}
	return tokens, nil
}

func getToken(ctx context.Context, q querier, userID, id string) (TokenInfo, error) {
	info, err := scanToken(q.QueryRowContext(ctx, `SELECT `+tokenColumns+` FROM core_tokens t WHERE t.id = ? AND t.user_id = ?`, id, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return TokenInfo{}, errTokenNotFound()
	}
	if err != nil {
		return TokenInfo{}, fmt.Errorf("auth: get token: %w", err)
	}
	return info, nil
}

// GetToken returns the token id of userID; a token of another user, or none,
// is errcode.NotFound.
func (s *Service) GetToken(ctx context.Context, userID, id string) (TokenInfo, error) {
	return getToken(ctx, s.db.Read(), userID, id)
}

// RevokeToken revokes the token id of userID and returns it. Revoking a
// revoked token keeps its first revocation time; a token of another user, or
// none, is errcode.NotFound.
func (s *Service) RevokeToken(ctx context.Context, userID, id string) (TokenInfo, error) {
	var info TokenInfo
	err := s.db.Write(ctx, func(tx *sql.Tx) error {
		var err error
		if info, err = getToken(ctx, tx, userID, id); err != nil {
			return err
		}
		if info.RevokedAt != nil {
			return nil
		}
		now := s.now()
		if _, err := tx.ExecContext(ctx, `UPDATE core_tokens SET revoked_at = ? WHERE id = ?`, formatTime(now), id); err != nil {
			return fmt.Errorf("auth: revoke token: %w", err)
		}
		info.RevokedAt = &now
		return nil
	})
	return info, err
}

// authenticateToken resolves a bearer token. A malformed, unknown, revoked or
// expired token, a wrong secret, or a user that is not active is
// auth.required. It moves last_used_at forward when it is unset or a minute
// or more old.
func (s *Service) authenticateToken(ctx context.Context, token, source string) (Identity, error) {
	id, raw, ok := parseToken(token)
	if !ok {
		return Identity{}, errAuthRequired()
	}
	var hash, createdText string
	var u User
	info, err := scanToken(s.db.Read().QueryRowContext(ctx, `SELECT `+tokenColumns+`, t.secret_hash,
		u.id, u.username, u.role, u.state, u.created_at FROM core_tokens t JOIN core_users u ON u.id = t.user_id
		WHERE t.id = ?`, id), &hash, &u.ID, &u.Username, &u.Role, &u.State, &createdText)
	if errors.Is(err, sql.ErrNoRows) {
		return Identity{}, errAuthRequired()
	}
	if err != nil {
		return Identity{}, fmt.Errorf("auth: load token: %w", err)
	}
	if u.CreatedAt, err = parseTime(createdText); err != nil {
		return Identity{}, err
	}
	now := s.now()
	if subtle.ConstantTimeCompare([]byte(secretHash(raw)), []byte(hash)) != 1 ||
		info.RevokedAt != nil || !now.Before(info.ExpiresAt) || u.State != StateActive {
		return Identity{}, errAuthRequired()
	}
	if info.LastUsedAt == nil || now.Sub(*info.LastUsedAt) >= LastUsedInterval {
		var touched int64
		err := s.db.Write(ctx, func(tx *sql.Tx) error {
			res, err := tx.ExecContext(ctx, `UPDATE core_tokens SET last_used_at = ? WHERE id = ? AND (last_used_at IS NULL OR last_used_at <= ?)`,
				formatTime(now), id, formatTime(now.Add(-LastUsedInterval)))
			if err != nil {
				return err
			}
			touched, err = res.RowsAffected()
			return err
		})
		if err != nil {
			return Identity{}, fmt.Errorf("auth: touch token: %w", err)
		}
		if touched == 1 {
			info.LastUsedAt = &now
		}
	}
	return Identity{
		Principal: core.Principal{Actor: "token:" + id, UserID: u.ID, TokenID: id, Scope: info.Scope, Source: source},
		User:      u, ExpiresAt: info.ExpiresAt, Token: &info,
	}, nil
}

// Purge deletes sessions past their idle or absolute limit and tokens 30
// days after they expired or were revoked (spec section 9). The server runs
// it at start and every 10 minutes.
func (s *Service) Purge(ctx context.Context) error {
	now := s.now()
	return s.db.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM core_sessions WHERE expires_at <= ? OR last_seen_at <= ?`,
			formatTime(now), formatTime(now.Add(-SessionIdle))); err != nil {
			return fmt.Errorf("auth: purge sessions: %w", err)
		}
		cutoff := formatTime(now.Add(-TokenRetention))
		if _, err := tx.ExecContext(ctx, `DELETE FROM core_tokens WHERE expires_at <= ? OR revoked_at <= ?`,
			cutoff, cutoff); err != nil {
			return fmt.Errorf("auth: purge tokens: %w", err)
		}
		return nil
	})
}
