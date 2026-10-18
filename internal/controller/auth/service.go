package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/secret"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// The only role (R19) and the two user states (spec section 8).
const (
	RoleAdmin    = "admin"
	StatePending = "pending"
	StateActive  = "active"
)

// User is a user as the API and the CLI show it (spec section 12).
type User struct {
	ID        string    `json:"id"`
	Username  string    `json:"username"`
	Role      string    `json:"role"`
	State     string    `json:"state"`
	CreatedAt time.Time `json:"createdAt"`
}

// TokenInfo describes an API token without its secret (spec section 12).
type TokenInfo struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Scope      core.Scope `json:"scope"`
	CreatedAt  time.Time  `json:"createdAt"`
	ExpiresAt  time.Time  `json:"expiresAt"`
	LastUsedAt *time.Time `json:"lastUsedAt" jsonschema:"nullable"`
	RevokedAt  *time.Time `json:"revokedAt" jsonschema:"nullable"`
}

// Identity is the authenticated caller of a request.
type Identity struct {
	Principal   core.Principal
	FromSession bool       // true for a session cookie, false for a bearer token
	User        User       // the principal's user
	ExpiresAt   time.Time  // when the session or token ends, as of this request
	Token       *TokenInfo // the bearer token; nil for a session
}

// Options configure a Service.
type Options struct {
	// Params are the argon2id parameters of new hashes; the zero value means
	// DefaultParams.
	Params Params
}

// Service is the controller's identity over the core_users, core_sessions
// and core_tokens tables. Its methods are safe for concurrent use.
type Service struct {
	db      *store.DB
	sealer  core.Sealer
	clock   core.Clock
	audit   core.Audit
	hasher  *hasher
	limiter *limiter
	csrfKey []byte

	idMu    sync.Mutex
	entropy *ulid.MonotonicEntropy
}

// New returns the identity service of the controller database db. It hashes
// once to prepare the unknown-user dummy hash.
func New(db *store.DB, sealer core.Sealer, clock core.Clock, audit core.Audit, opts Options) *Service {
	p := opts.Params
	if p == (Params{}) {
		p = DefaultParams
	}
	return &Service{
		db: db, sealer: sealer, clock: clock, audit: audit,
		hasher: newHasher(p), limiter: newLimiter(clock), csrfKey: sealer.Derive(secret.CSRFLabel),
		entropy: ulid.Monotonic(rand.Reader, 0),
	}
}

// newID returns a ULID stamped with the service's clock; ids it makes sort in
// creation order.
func (s *Service) newID() string {
	s.idMu.Lock()
	defer s.idMu.Unlock()
	return ulid.MustNew(ulid.Timestamp(s.clock.Now()), s.entropy).String()
}

// now is the clock's time in UTC, truncated to what the database keeps.
func (s *Service) now() time.Time { return s.clock.Now().UTC().Round(0) }

// timeFormat is RFC 3339 in UTC with a fixed width, so stored times compare
// as text.
const timeFormat = "2006-01-02T15:04:05.000000000Z"

func formatTime(t time.Time) string { return t.UTC().Format(timeFormat) }

func parseTime(s string) (time.Time, error) {
	t, err := time.Parse(timeFormat, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("auth: stored time %q: %w", s, err)
	}
	return t, nil
}

// totpAD is the associated data sealing a user's TOTP secret (ADR 0005).
func totpAD(userID string) string { return "core_users.totp_secret_ct." + userID }

func invalidField(path, message string) *errcode.Error {
	return &errcode.Error{Code: errcode.InvalidInput, Detail: "invalid input",
		Fields: []errcode.FieldError{{Path: path, Message: message}}}
}

func errUserNotFound() *errcode.Error { return errcode.New(errcode.NotFound, "user not found") }

// querier is a transaction or the reader pool.
type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// userRecord is a core_users row.
type userRecord struct {
	User
	passwordHash string
	totpCT       []byte
	lastStep     int64
}

const userColumns = `id, username, role, state, created_at, password_hash, totp_secret_ct, totp_last_step`

// loadUser reads username's row; an absent user is errcode.NotFound.
func loadUser(ctx context.Context, q querier, username string) (userRecord, error) {
	var r userRecord
	var created string
	err := q.QueryRowContext(ctx, `SELECT `+userColumns+` FROM core_users WHERE username = ?`, username).
		Scan(&r.ID, &r.Username, &r.Role, &r.State, &created, &r.passwordHash, &r.totpCT, &r.lastStep)
	if errors.Is(err, sql.ErrNoRows) {
		return userRecord{}, errUserNotFound()
	}
	if err != nil {
		return userRecord{}, fmt.Errorf("auth: load user: %w", err)
	}
	if r.CreatedAt, err = parseTime(created); err != nil {
		return userRecord{}, err
	}
	return r, nil
}

// CreateUser adds a pending admin with password and a new TOTP secret, and
// returns the user and the secret in unpadded base32, the only time it is
// shown. A taken or invalid username or an invalid password is
// request.invalid_input.
func (s *Service) CreateUser(ctx context.Context, username, password string) (User, string, error) {
	if err := checkUsername(username); err != nil {
		return User{}, "", err
	}
	if err := checkPassword(password); err != nil {
		return User{}, "", err
	}
	hash, err := s.hasher.hash(ctx, password)
	if err != nil {
		return User{}, "", err
	}
	key := newTOTPSecret()
	u := User{ID: s.newID(), Username: username, Role: RoleAdmin, State: StatePending, CreatedAt: s.now()}
	ct, err := s.sealer.Seal(totpAD(u.ID), key)
	if err != nil {
		return User{}, "", fmt.Errorf("auth: seal TOTP secret: %w", err)
	}
	err = s.db.Write(ctx, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM core_users WHERE username = ?`, username).Scan(&n); err != nil {
			return fmt.Errorf("auth: create user: %w", err)
		}
		if n > 0 {
			return invalidField("/username", "a user with this name already exists")
		}
		at := formatTime(u.CreatedAt)
		_, err := tx.ExecContext(ctx, `INSERT INTO core_users
			(id, username, role, state, password_hash, totp_secret_ct, totp_last_step, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, 0, ?, ?)`, u.ID, u.Username, u.Role, u.State, hash, ct, at, at)
		if err != nil {
			return fmt.Errorf("auth: create user: %w", err)
		}
		return nil
	})
	if err != nil {
		return User{}, "", err
	}
	return u, encodeTOTPSecret(key), nil
}

// ActivateUser confirms a pending user's enrollment with a TOTP code and makes
// the user active. The code's step is stored, so it cannot also log in. A
// wrong code or a user that is not pending is request.invalid_input.
func (s *Service) ActivateUser(ctx context.Context, username, code string) (User, error) {
	var u User
	err := s.db.Write(ctx, func(tx *sql.Tx) error {
		r, err := loadUser(ctx, tx, username)
		if err != nil {
			return err
		}
		if r.State != StatePending {
			return invalidField("/username", "the user is not pending")
		}
		key, err := s.sealer.Open(totpAD(r.ID), r.totpCT)
		if err != nil {
			return fmt.Errorf("auth: open TOTP secret: %w", err)
		}
		step, ok := matchTOTP(key, code, s.clock.Now(), r.lastStep)
		if !ok {
			return invalidField("/code", "does not match the authenticator")
		}
		if _, err := tx.ExecContext(ctx, `UPDATE core_users SET state = ?, totp_last_step = ?, updated_at = ? WHERE id = ?`,
			StateActive, step, formatTime(s.now()), r.ID); err != nil {
			return fmt.Errorf("auth: activate user: %w", err)
		}
		r.State = StateActive
		u = r.User
		return nil
	})
	return u, err
}

// endSessions deletes every session of userID inside tx.
func endSessions(ctx context.Context, tx *sql.Tx, userID string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM core_sessions WHERE user_id = ?`, userID); err != nil {
		return fmt.Errorf("auth: end sessions: %w", err)
	}
	return nil
}

// ResetTOTP gives username a new TOTP secret, returns the user to pending and
// ends its sessions; it returns the user and the new secret in unpadded
// base32. This is the only recovery for a lost device (owner decision).
func (s *Service) ResetTOTP(ctx context.Context, username string) (User, string, error) {
	key := newTOTPSecret()
	var u User
	err := s.db.Write(ctx, func(tx *sql.Tx) error {
		r, err := loadUser(ctx, tx, username)
		if err != nil {
			return err
		}
		ct, err := s.sealer.Seal(totpAD(r.ID), key)
		if err != nil {
			return fmt.Errorf("auth: seal TOTP secret: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE core_users SET state = ?, totp_secret_ct = ?, totp_last_step = 0,
			updated_at = ? WHERE id = ?`, StatePending, ct, formatTime(s.now()), r.ID); err != nil {
			return fmt.Errorf("auth: reset TOTP: %w", err)
		}
		r.State = StatePending
		u = r.User
		return endSessions(ctx, tx, r.ID)
	})
	if err != nil {
		return User{}, "", err
	}
	return u, encodeTOTPSecret(key), nil
}

// ResetPassword sets username's password and ends its sessions. An invalid
// password is request.invalid_input.
func (s *Service) ResetPassword(ctx context.Context, username, password string) (User, error) {
	if err := checkPassword(password); err != nil {
		return User{}, err
	}
	if _, err := s.GetUser(ctx, username); err != nil {
		return User{}, err
	}
	hash, err := s.hasher.hash(ctx, password)
	if err != nil {
		return User{}, err
	}
	var u User
	err = s.db.Write(ctx, func(tx *sql.Tx) error {
		r, err := loadUser(ctx, tx, username)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE core_users SET password_hash = ?, updated_at = ? WHERE id = ?`,
			hash, formatTime(s.now()), r.ID); err != nil {
			return fmt.Errorf("auth: reset password: %w", err)
		}
		u = r.User
		return endSessions(ctx, tx, r.ID)
	})
	return u, err
}

// DeleteUser deletes username with its sessions and tokens. Deleting the last
// active admin is request.invalid_input.
func (s *Service) DeleteUser(ctx context.Context, username string) error {
	return s.db.Write(ctx, func(tx *sql.Tx) error {
		r, err := loadUser(ctx, tx, username)
		if err != nil {
			return err
		}
		if r.State == StateActive && r.Role == RoleAdmin {
			var n int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM core_users WHERE state = ? AND role = ?`,
				StateActive, RoleAdmin).Scan(&n); err != nil {
				return fmt.Errorf("auth: delete user: %w", err)
			}
			if n <= 1 {
				return invalidField("/username", "the user is the last active admin")
			}
		}
		for _, q := range []string{
			`DELETE FROM core_tokens WHERE user_id = ?`,
			`DELETE FROM core_sessions WHERE user_id = ?`,
			`DELETE FROM core_users WHERE id = ?`,
		} {
			if _, err := tx.ExecContext(ctx, q, r.ID); err != nil {
				return fmt.Errorf("auth: delete user: %w", err)
			}
		}
		return nil
	})
}

// ListUsers returns every user sorted by username; no users is an empty,
// non-nil slice.
func (s *Service) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.db.Read().QueryContext(ctx, `SELECT id, username, role, state, created_at FROM core_users ORDER BY username`)
	if err != nil {
		return nil, fmt.Errorf("auth: list users: %w", err)
	}
	defer func() { _ = rows.Close() }()
	users := []User{}
	for rows.Next() {
		var u User
		var created string
		if err := rows.Scan(&u.ID, &u.Username, &u.Role, &u.State, &created); err != nil {
			return nil, fmt.Errorf("auth: list users: %w", err)
		}
		if u.CreatedAt, err = parseTime(created); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("auth: list users: %w", err)
	}
	return users, nil
}

// GetUser returns username; an unknown user is errcode.NotFound.
func (s *Service) GetUser(ctx context.Context, username string) (User, error) {
	r, err := loadUser(ctx, s.db.Read(), username)
	return r.User, err
}
