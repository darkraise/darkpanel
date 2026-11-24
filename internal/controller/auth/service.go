package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/oklog/ulid/v2"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/secret"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// The two user roles, the two grant roles (M10 spec section 1.1) and the two
// user states (spec section 8).
const (
	RoleAdmin     = "admin"
	RoleMember    = "member"
	GrantViewer   = "viewer"
	GrantOperator = "operator"
	StatePending  = "pending"
	StateActive   = "active"
)

// User is a user as the API and the CLI show it (spec section 12).
type User struct {
	ID        string    `json:"id"`
	Username  string    `json:"username"`
	Role      string    `json:"role"`
	State     string    `json:"state"`
	CreatedAt time.Time `json:"createdAt"`
	Disabled  bool      `json:"disabled,omitempty" jsonschema_description:"True while core.user.disable keeps the user from signing in."`
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
	// OIDCTransport carries the requests to the OIDC provider; nil means
	// http.DefaultTransport.
	OIDCTransport http.RoundTripper
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
	oidc    *oidcFlow

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
		oidc:    newOIDCFlow(opts.OIDCTransport),
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

// usable reports whether the user may sign in: active and not disabled.
func (u User) usable() bool { return u.State == StateActive && !u.Disabled }

const userColumns = `id, username, role, state, created_at, password_hash, totp_secret_ct, totp_last_step,
	disabled_at IS NOT NULL`

// loadUser reads username's row; an absent user is errcode.NotFound.
func loadUser(ctx context.Context, q querier, username string) (userRecord, error) {
	return loadUserBy(ctx, q, `username = ?`, username)
}

// loadUserBy reads the one row matching where; none is errcode.NotFound.
func loadUserBy(ctx context.Context, q querier, where string, args ...any) (userRecord, error) {
	var r userRecord
	var created string
	err := q.QueryRowContext(ctx, `SELECT `+userColumns+` FROM core_users WHERE `+where, args...).
		Scan(&r.ID, &r.Username, &r.Role, &r.State, &created, &r.passwordHash, &r.totpCT, &r.lastStep, &r.Disabled)
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
	return s.CreateUserWithRole(ctx, username, password, RoleAdmin)
}

// CreateUserWithRole is CreateUser for a user of role admin or member; a
// member starts with no grants, so it sees no host.
func (s *Service) CreateUserWithRole(ctx context.Context, username, password, role string) (User, string, error) {
	if err := checkUsername(username); err != nil {
		return User{}, "", err
	}
	if role != RoleAdmin && role != RoleMember {
		return User{}, "", invalidField("/role", "must be admin or member")
	}
	if err := checkPassword(password); err != nil {
		return User{}, "", err
	}
	hash, err := s.hasher.hash(ctx, password)
	if err != nil {
		return User{}, "", err
	}
	key := newTOTPSecret()
	u := User{ID: s.newID(), Username: username, Role: role, State: StatePending, CreatedAt: s.now()}
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
		if err := endEnrollmentLinks(ctx, tx, r.ID); err != nil {
			return err
		}
		r.State = StateActive
		u = r.User
		return nil
	})
	return u, err
}

// endEnrollmentLinks ends userID's unused enrollment link for good: no token
// hashes to "". Every path that activates a user other than the link itself,
// or resets its credentials, calls it, so a link an admin handed out earlier
// cannot take the account over once the user is pending again.
func endEnrollmentLinks(ctx context.Context, tx *sql.Tx, userID string) error {
	if _, err := tx.ExecContext(ctx, `UPDATE core_user_enrollments SET token_hash = '' WHERE user_id = ? AND used_at IS NULL`,
		userID); err != nil {
		return fmt.Errorf("auth: end enrollment link: %w", err)
	}
	return nil
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
		if err := endEnrollmentLinks(ctx, tx, r.ID); err != nil {
			return err
		}
		return endSessions(ctx, tx, r.ID)
	})
	if err != nil {
		return User{}, "", err
	}
	return u, encodeTOTPSecret(key), nil
}

// ResetPassword sets username's password, ends its sessions and revokes its
// API tokens, since a token may have leaked with the password (owner
// decision). A token already revoked keeps its first revocation time. An
// invite never enrolled stops being one: its link is removed, since the user
// now has a password of its own. An invalid password is
// request.invalid_input.
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
		now := formatTime(s.now())
		if _, err := tx.ExecContext(ctx, `UPDATE core_users SET password_hash = ?, updated_at = ? WHERE id = ?`,
			hash, now, r.ID); err != nil {
			return fmt.Errorf("auth: reset password: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE core_tokens SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL`,
			now, r.ID); err != nil {
			return fmt.Errorf("auth: revoke tokens: %w", err)
		}
		u = r.User
		if _, err := tx.ExecContext(ctx, `DELETE FROM core_user_enrollments WHERE user_id = ? AND used_at IS NULL`,
			r.ID); err != nil {
			return fmt.Errorf("auth: end enrollment link: %w", err)
		}
		return endSessions(ctx, tx, r.ID)
	})
	return u, err
}

// DeleteUser deletes username with its sessions and tokens. Deleting the last
// active admin is user.last_admin.
func (s *Service) DeleteUser(ctx context.Context, username string) error {
	return s.db.Write(ctx, func(tx *sql.Tx) error {
		r, err := loadUser(ctx, tx, username)
		if err != nil {
			return err
		}
		if err := refuseLastAdmin(ctx, tx, r.User); err != nil {
			return err
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
	rows, err := s.db.Read().QueryContext(ctx, `SELECT id, username, role, state, created_at, disabled_at IS NOT NULL
		FROM core_users ORDER BY username`)
	if err != nil {
		return nil, fmt.Errorf("auth: list users: %w", err)
	}
	defer func() { _ = rows.Close() }()
	users := []User{}
	for rows.Next() {
		var u User
		var created string
		if err := rows.Scan(&u.ID, &u.Username, &u.Role, &u.State, &created, &u.Disabled); err != nil {
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

// refuseLastAdmin fails with user.last_admin when u is the last active
// admin that is not disabled, the one user that must keep signing in. It
// counts inside the caller's write transaction, so two changes cannot each
// remove one of the last two admins.
func refuseLastAdmin(ctx context.Context, tx *sql.Tx, u User) error {
	if !u.usable() || u.Role != RoleAdmin {
		return nil
	}
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM core_users WHERE state = ? AND role = ? AND disabled_at IS NULL`,
		StateActive, RoleAdmin).Scan(&n); err != nil {
		return fmt.Errorf("auth: count active admins: %w", err)
	}
	if n <= 1 {
		return LastAdminError(u.Username)
	}
	return nil
}

// LastAdminError is the user.last_admin refusal of a change that would leave
// no active admin.
func LastAdminError(username string) error {
	return errcode.New(errcode.UserLastAdmin, username+" is the last active admin; activate another admin first")
}

// Invitations and enrollment links (M10 spec section 1.5).
const (
	EnrollmentTTL    = 72 * time.Hour
	MethodEnrollment = "enrollment"
	enrollTokenSize  = 32
	maxIdentityLen   = 254
)

// Grant is one of a member's grants: target "*" (every host) or a host group
// id, role viewer or operator.
type Grant struct {
	Target string `json:"target" jsonschema:"minLength=1" jsonschema_description:"A host group id, or * for every host, including hosts enrolled later."`
	Role   string `json:"role" jsonschema:"enum=viewer,enum=operator" jsonschema_description:"viewer gives read, operator gives operate."`
}

// InviteRequest is a new invited user. Email and TailnetLogin are optional
// sign-in identities; "" leaves them unset.
type InviteRequest struct {
	Username, Role, Email, TailnetLogin string
	Grants                              []Grant
}

// Invitation is a new enrollment link's user, token and expiry; only the
// call that made the token returns it.
type Invitation struct {
	User      User
	Token     string
	ExpiresAt time.Time
}

func errEnrollmentExpired() *errcode.Error {
	return errcode.New(errcode.EnrollmentExpired, "This link has expired. Ask an admin for a new one.")
}

// newEnrollToken returns a token of 32 random bytes in unpadded base64url
// and the hash it is stored as.
func newEnrollToken() (string, string) {
	raw := make([]byte, enrollTokenSize)
	_, _ = rand.Read(raw)
	return b64url.EncodeToString(raw), secretHash(raw)
}

// enrollTokenHash returns the stored hash of token; ok is false for a token
// that is not the canonical encoding of 32 bytes.
func enrollTokenHash(token string) (string, bool) {
	raw, err := b64url.DecodeString(token)
	if err != nil || len(raw) != enrollTokenSize || b64url.EncodeToString(raw) != token {
		return "", false
	}
	return secretHash(raw), true
}

// checkIdentity checks an optional sign-in identity: at most 254 characters
// with no spaces or control characters, and for an email one "@" with text
// on both sides.
func checkIdentity(path, v string, email bool) error {
	if v == "" {
		return nil
	}
	if len(v) > maxIdentityLen || strings.IndexFunc(v, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return invalidField(path, fmt.Sprintf("must be at most %d characters with no spaces", maxIdentityLen))
	}
	if local, domain, ok := strings.Cut(v, "@"); email && (!ok || local == "" || domain == "" || strings.Contains(domain, "@")) {
		return invalidField(path, "must be an email address")
	}
	return nil
}

func nullable(v string) any {
	if v == "" {
		return nil
	}
	return v
}

// identityTaken fails when a user other than userID already has value in
// column, which compares without case.
func identityTaken(ctx context.Context, q querier, column, path, value, userID string) error {
	if value == "" {
		return nil
	}
	var n int
	if err := q.QueryRowContext(ctx, `SELECT count(*) FROM core_users WHERE `+column+` = ? COLLATE NOCASE AND id <> ?`,
		value, userID).Scan(&n); err != nil {
		return fmt.Errorf("auth: check %s: %w", column, err)
	}
	if n > 0 {
		return invalidField(path, "another user already has it")
	}
	return nil
}

// Invite adds a pending user that cannot sign in by password: its password
// hash is the argon2id hash of a random password nobody sees, and its TOTP
// secret a sealed random one, until its enrollment link sets both. A taken
// username or identity, an unknown role, a grant CheckGrants refuses, or
// grants for an admin is request.invalid_input.
func (s *Service) Invite(ctx context.Context, r InviteRequest) (Invitation, error) {
	if err := checkUsername(r.Username); err != nil {
		return Invitation{}, err
	}
	switch {
	case r.Role != RoleAdmin && r.Role != RoleMember:
		return Invitation{}, invalidField("/role", "must be admin or member")
	case r.Role == RoleAdmin && len(r.Grants) > 0:
		return Invitation{}, invalidField("/grants", "an admin has every host; grants are for members")
	}
	if err := checkIdentity("/email", r.Email, true); err != nil {
		return Invitation{}, err
	}
	if err := checkIdentity("/tailnetLogin", r.TailnetLogin, false); err != nil {
		return Invitation{}, err
	}
	placeholder := make([]byte, 32)
	_, _ = rand.Read(placeholder)
	hash, err := s.hasher.hash(ctx, b64url.EncodeToString(placeholder))
	if err != nil {
		return Invitation{}, err
	}
	u := User{ID: s.newID(), Username: r.Username, Role: r.Role, State: StatePending, CreatedAt: s.now()}
	ct, err := s.sealer.Seal(totpAD(u.ID), newTOTPSecret())
	if err != nil {
		return Invitation{}, fmt.Errorf("auth: seal TOTP secret: %w", err)
	}
	token, tokenHash := newEnrollToken()
	inv := Invitation{User: u, Token: token, ExpiresAt: u.CreatedAt.Add(EnrollmentTTL)}
	err = s.db.Write(ctx, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM core_users WHERE username = ?`, r.Username).Scan(&n); err != nil {
			return fmt.Errorf("auth: invite: %w", err)
		}
		if n > 0 {
			return invalidField("/username", "a user with this name already exists")
		}
		if err := identityTaken(ctx, tx, "email", "/email", r.Email, u.ID); err != nil {
			return err
		}
		if err := identityTaken(ctx, tx, "tailnet_login", "/tailnetLogin", r.TailnetLogin, u.ID); err != nil {
			return err
		}
		if _, err := CheckGrants(ctx, tx, r.Grants); err != nil {
			return err
		}
		at := formatTime(u.CreatedAt)
		if _, err := tx.ExecContext(ctx, `INSERT INTO core_users
			(id, username, role, state, password_hash, totp_secret_ct, totp_last_step, created_at, updated_at, email, tailnet_login)
			VALUES (?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?)`, u.ID, u.Username, u.Role, u.State, hash, ct, at, at,
			nullable(r.Email), nullable(r.TailnetLogin)); err != nil {
			return fmt.Errorf("auth: invite: %w", err)
		}
		for _, g := range r.Grants {
			if _, err := tx.ExecContext(ctx, `INSERT INTO core_user_grants (user_id, target, role) VALUES (?, ?, ?)`,
				u.ID, g.Target, g.Role); err != nil {
				return fmt.Errorf("auth: invite: store grant: %w", err)
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO core_user_enrollments (user_id, token_hash, expires_at) VALUES (?, ?, ?)`,
			u.ID, tokenHash, formatTime(inv.ExpiresAt)); err != nil {
			return fmt.Errorf("auth: invite: store enrollment: %w", err)
		}
		return nil
	})
	if err != nil {
		return Invitation{}, err
	}
	return inv, nil
}

// loadInvited reads username for renew and cancel, which apply only to a user
// that never enrolled: pending, with an enrollment link never used. With
// active, an active user whose link is unused qualifies too: an OIDC or
// Tailscale sign-in activated its invite, the link still sets its password,
// and renewing the link is how an admin ends one that leaked.
func loadInvited(ctx context.Context, tx querier, username string, active bool) (userRecord, error) {
	r, err := loadUser(ctx, tx, username)
	if err != nil {
		return userRecord{}, err
	}
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM core_user_enrollments WHERE user_id = ? AND used_at IS NULL`,
		r.ID).Scan(&n); err != nil {
		return userRecord{}, fmt.Errorf("auth: load enrollment: %w", err)
	}
	if (r.State != StatePending && (!active || r.State != StateActive)) || n == 0 {
		return userRecord{}, invalidField("/username", "the user is not an invite that was never used")
	}
	return r, nil
}

// Invited returns username when it is an invite that was never used, the
// users core.user.renew-invite and core.user.cancel-invite apply to; any
// other user is request.invalid_input.
func (s *Service) Invited(ctx context.Context, username string) (User, error) {
	r, err := loadInvited(ctx, s.db.Read(), username, false)
	return r.User, err
}

// LinkHolder returns username when it holds an unused enrollment link: an
// invite that never enrolled, or one whose invite an OIDC or Tailscale
// sign-in activated. core.user.renew-invite applies to these users.
func (s *Service) LinkHolder(ctx context.Context, username string) (User, error) {
	r, err := loadInvited(ctx, s.db.Read(), username, true)
	return r.User, err
}

// RenewInvite gives a user holding an unused enrollment link (LinkHolder) a
// new link valid 72 hours; the previous link stops working.
func (s *Service) RenewInvite(ctx context.Context, username string) (Invitation, error) {
	token, tokenHash := newEnrollToken()
	var inv Invitation
	err := s.db.Write(ctx, func(tx *sql.Tx) error {
		r, err := loadInvited(ctx, tx, username, true)
		if err != nil {
			return err
		}
		now := s.now()
		inv = Invitation{User: r.User, Token: token, ExpiresAt: now.Add(EnrollmentTTL)}
		if _, err := tx.ExecContext(ctx, `UPDATE core_user_enrollments SET token_hash = ?, expires_at = ? WHERE user_id = ?`,
			tokenHash, formatTime(inv.ExpiresAt), r.ID); err != nil {
			return fmt.Errorf("auth: renew invite: %w", err)
		}
		return nil
	})
	if err != nil {
		return Invitation{}, err
	}
	return inv, nil
}

// CancelInvite removes an invited user that never enrolled, with its grants
// and its link, and returns the user as it was.
func (s *Service) CancelInvite(ctx context.Context, username string) (User, error) {
	var u User
	err := s.db.Write(ctx, func(tx *sql.Tx) error {
		r, err := loadInvited(ctx, tx, username, false)
		if err != nil {
			return err
		}
		u = r.User
		for _, q := range []string{
			`DELETE FROM core_user_enrollments WHERE user_id = ?`,
			`DELETE FROM core_user_grants WHERE user_id = ?`,
			`DELETE FROM core_tokens WHERE user_id = ?`,
			`DELETE FROM core_sessions WHERE user_id = ?`,
			`DELETE FROM core_users WHERE id = ?`,
		} {
			if _, err := tx.ExecContext(ctx, q, r.ID); err != nil {
				return fmt.Errorf("auth: cancel invite: %w", err)
			}
		}
		return nil
	})
	return u, err
}

// SetDisabled disables or enables username. A disabled user's sessions and
// tokens are refused at their next request and kept, so enabling the user
// restores them; disabling also ends an unused enrollment link. Disabling the
// last active admin is request.invalid_input.
func (s *Service) SetDisabled(ctx context.Context, username string, disabled bool) (User, error) {
	var u User
	err := s.db.Write(ctx, func(tx *sql.Tx) error {
		r, err := loadUser(ctx, tx, username)
		if err != nil {
			return err
		}
		if disabled && !r.Disabled {
			if err := refuseLastAdmin(ctx, tx, r.User); err != nil {
				return err
			}
		}
		now := formatTime(s.now())
		q, args := `UPDATE core_users SET disabled_at = NULL, updated_at = ? WHERE id = ?`, []any{now, r.ID}
		if disabled {
			q, args = `UPDATE core_users SET disabled_at = coalesce(disabled_at, ?), updated_at = ? WHERE id = ?`, []any{now, now, r.ID}
		}
		if _, err := tx.ExecContext(ctx, q, args...); err != nil {
			return fmt.Errorf("auth: set disabled: %w", err)
		}
		if disabled {
			if err := endEnrollmentLinks(ctx, tx, r.ID); err != nil {
				return err
			}
		}
		r.Disabled = disabled
		u = r.User
		return nil
	})
	return u, err
}

// CheckGrants checks a member's grants: each target is "*" or an existing
// host group id, named once, and each role viewer or operator. It returns
// the name of each group a grant targets. Every write that stores grants
// runs it inside its transaction, so a group deleted meanwhile is refused.
func CheckGrants(ctx context.Context, q querier, grants []Grant) (map[string]string, error) {
	names := map[string]string{}
	seen := map[string]bool{}
	for i, g := range grants {
		at := fmt.Sprintf("/grants/%d", i)
		if _, ok := grantScope[g.Role]; !ok {
			return nil, invalidField(at+"/role", "must be viewer or operator")
		}
		if seen[g.Target] {
			return nil, invalidField(at+"/target", "names a target another grant names")
		}
		seen[g.Target] = true
		if g.Target == "*" {
			continue
		}
		var name string
		err := q.QueryRowContext(ctx, `SELECT name FROM core_host_groups WHERE id = ?`, g.Target).Scan(&name)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, invalidField(at+"/target", "is not * or an existing host group")
		}
		if err != nil {
			return nil, fmt.Errorf("auth: check grants: %w", err)
		}
		names[g.Target] = name
	}
	return names, nil
}

// Sign-in methods a directory entry lists.
const (
	SignInPassword  = "password"
	SignInOIDC      = "oidc"
	SignInTailscale = "tailscale"
)

// DirectoryUser is a user as the web Users page shows it: its role, grants,
// sign-in identities and the sign-in methods it has.
type DirectoryUser struct {
	User
	Grants          []Grant    `json:"grants" jsonschema_description:"The user's grants, sorted by target; they apply only while the user is a member."`
	Email           string     `json:"email" jsonschema_description:"The email an OIDC sign-in matches; empty when unset."`
	TailnetLogin    string     `json:"tailnetLogin" jsonschema_description:"The tailnet login a Tailscale sign-in matches; empty when unset."`
	SignIn          []string   `json:"signIn" jsonschema:"enum=password,enum=oidc,enum=tailscale" jsonschema_description:"The sign-in methods the user has: password once it has a password of its own, oidc and tailscale once a sign-in by that method linked the account."`
	InviteExpiresAt *time.Time `json:"inviteExpiresAt" jsonschema:"nullable" jsonschema_description:"When the user's enrollment link expires; null when it has no unused link or the link was ended."`
}

// directory reads every user, or only username when it is not "".
func directory(ctx context.Context, q accessQuerier, username string) ([]DirectoryUser, error) {
	query := `SELECT u.id, u.username, u.role, u.state, u.created_at, u.disabled_at IS NOT NULL,
		coalesce(u.email, ''), coalesce(u.tailnet_login, ''),
		NOT EXISTS (SELECT 1 FROM core_user_enrollments e WHERE e.user_id = u.id AND e.used_at IS NULL),
		u.oidc_subject IS NOT NULL, u.tailnet_user_id IS NOT NULL,
		(SELECT e.expires_at FROM core_user_enrollments e WHERE e.user_id = u.id AND e.used_at IS NULL AND e.token_hash != '')
		FROM core_users u`
	args := []any{}
	if username != "" {
		query += ` WHERE u.username = ?`
		args = append(args, username)
	}
	rows, err := q.QueryContext(ctx, query+` ORDER BY u.username`, args...)
	if err != nil {
		return nil, fmt.Errorf("auth: read the directory: %w", err)
	}
	defer func() { _ = rows.Close() }()
	users := []DirectoryUser{}
	byID := map[string]int{}
	for rows.Next() {
		var d DirectoryUser
		var created string
		var password, oidc, tailscale bool
		var expires sql.NullString
		if err := rows.Scan(&d.ID, &d.Username, &d.Role, &d.State, &created, &d.Disabled, &d.Email, &d.TailnetLogin,
			&password, &oidc, &tailscale, &expires); err != nil {
			return nil, fmt.Errorf("auth: read the directory: %w", err)
		}
		if d.CreatedAt, err = parseTime(created); err != nil {
			return nil, err
		}
		if expires.Valid {
			at, err := parseTime(expires.String)
			if err != nil {
				return nil, err
			}
			d.InviteExpiresAt = &at
		}
		d.Grants, d.SignIn = []Grant{}, []string{}
		for _, m := range []struct {
			has  bool
			name string
		}{{password, SignInPassword}, {oidc, SignInOIDC}, {tailscale, SignInTailscale}} {
			if m.has {
				d.SignIn = append(d.SignIn, m.name)
			}
		}
		byID[d.ID] = len(users)
		users = append(users, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("auth: read the directory: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("auth: read the directory: %w", err)
	}
	grants, err := q.QueryContext(ctx, `SELECT user_id, target, role FROM core_user_grants ORDER BY user_id, target`)
	if err != nil {
		return nil, fmt.Errorf("auth: read the directory's grants: %w", err)
	}
	defer func() { _ = grants.Close() }()
	for grants.Next() {
		var userID string
		var g Grant
		if err := grants.Scan(&userID, &g.Target, &g.Role); err != nil {
			return nil, fmt.Errorf("auth: read the directory's grants: %w", err)
		}
		if i, ok := byID[userID]; ok {
			users[i].Grants = append(users[i].Grants, g)
		}
	}
	if err := grants.Err(); err != nil {
		return nil, fmt.Errorf("auth: read the directory's grants: %w", err)
	}
	return users, nil
}

// Directory returns every user as the web Users page shows it, sorted by
// username.
func (s *Service) Directory(ctx context.Context) ([]DirectoryUser, error) {
	return directory(ctx, s.db.Read(), "")
}

// Profile returns username's directory entry; an unknown user is
// errcode.NotFound.
func (s *Service) Profile(ctx context.Context, username string) (DirectoryUser, error) {
	return profile(ctx, s.db.Read(), username)
}

func profile(ctx context.Context, q accessQuerier, username string) (DirectoryUser, error) {
	users, err := directory(ctx, q, username)
	if err != nil {
		return DirectoryUser{}, err
	}
	if len(users) == 0 {
		return DirectoryUser{}, errUserNotFound()
	}
	return users[0], nil
}

// SetRole makes username an admin or a member. A member's stored grants
// give its access; an admin keeps its grants unused, so making it a member
// again restores them. Demoting the last active admin is user.last_admin. A
// change of role ends an unused enrollment link: the link was handed out for
// the old role.
func (s *Service) SetRole(ctx context.Context, username, role string) (DirectoryUser, error) {
	if role != RoleAdmin && role != RoleMember {
		return DirectoryUser{}, invalidField("/role", "must be admin or member")
	}
	var d DirectoryUser
	err := s.db.Write(ctx, func(tx *sql.Tx) error {
		r, err := loadUser(ctx, tx, username)
		if err != nil {
			return err
		}
		if r.Role != role {
			if err := refuseLastAdmin(ctx, tx, r.User); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE core_users SET role = ?, updated_at = ? WHERE id = ?`,
				role, formatTime(s.now()), r.ID); err != nil {
				return fmt.Errorf("auth: set role: %w", err)
			}
			if err := endEnrollmentLinks(ctx, tx, r.ID); err != nil {
				return err
			}
		}
		d, err = profile(ctx, tx, username)
		return err
	})
	return d, err
}

// SetGrants replaces a member's grants, checked by CheckGrants. An admin
// has every host, so setting its grants is request.invalid_input.
func (s *Service) SetGrants(ctx context.Context, username string, grants []Grant) (DirectoryUser, error) {
	var d DirectoryUser
	err := s.db.Write(ctx, func(tx *sql.Tx) error {
		r, err := loadUser(ctx, tx, username)
		if err != nil {
			return err
		}
		if r.Role != RoleMember {
			return invalidField("/grants", "an admin has every host; grants are for members")
		}
		if _, err := CheckGrants(ctx, tx, grants); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM core_user_grants WHERE user_id = ?`, r.ID); err != nil {
			return fmt.Errorf("auth: set grants: %w", err)
		}
		for _, g := range grants {
			if _, err := tx.ExecContext(ctx, `INSERT INTO core_user_grants (user_id, target, role) VALUES (?, ?, ?)`,
				r.ID, g.Target, g.Role); err != nil {
				return fmt.Errorf("auth: set grants: %w", err)
			}
		}
		d, err = profile(ctx, tx, username)
		return err
	})
	return d, err
}

// CheckIdentity checks email and tailnetLogin as SetIdentity would set them
// on username: their form, and that no other user has either in any case.
func (s *Service) CheckIdentity(ctx context.Context, username, email, tailnetLogin string) error {
	r, err := loadUser(ctx, s.db.Read(), username)
	if err != nil {
		return err
	}
	return checkIdentities(ctx, s.db.Read(), r.ID, email, tailnetLogin)
}

func checkIdentities(ctx context.Context, q querier, userID, email, tailnetLogin string) error {
	if err := checkIdentity("/email", email, true); err != nil {
		return err
	}
	if err := checkIdentity("/tailnetLogin", tailnetLogin, false); err != nil {
		return err
	}
	if err := identityTaken(ctx, q, "email", "/email", email, userID); err != nil {
		return err
	}
	return identityTaken(ctx, q, "tailnet_login", "/tailnetLogin", tailnetLogin, userID)
}

// SetIdentity sets username's email and tailnet login, "" clearing one. An
// email that changes other than in case clears the OIDC pin (issuer and
// subject), and a tailnet login that does clears the Tailscale pin, so the
// next sign-in by that method links the account again by the new identity.
// A malformed identity, or one another user has in any case, is
// request.invalid_input.
func (s *Service) SetIdentity(ctx context.Context, username, email, tailnetLogin string) (DirectoryUser, error) {
	var d DirectoryUser
	err := s.db.Write(ctx, func(tx *sql.Tx) error {
		cur, err := profile(ctx, tx, username)
		if err != nil {
			return err
		}
		if err := checkIdentities(ctx, tx, cur.ID, email, tailnetLogin); err != nil {
			return err
		}
		q := `UPDATE core_users SET email = ?, tailnet_login = ?, updated_at = ?`
		if !strings.EqualFold(cur.Email, email) {
			q += `, oidc_issuer = NULL, oidc_subject = NULL`
		}
		if !strings.EqualFold(cur.TailnetLogin, tailnetLogin) {
			q += `, tailnet_user_id = NULL`
		}
		if _, err := tx.ExecContext(ctx, q+` WHERE id = ?`, nullable(email), nullable(tailnetLogin),
			formatTime(s.now()), cur.ID); err != nil {
			return fmt.Errorf("auth: set identity: %w", err)
		}
		d, err = profile(ctx, tx, username)
		return err
	})
	return d, err
}

// EnrollmentStart is what POST /api/v1/enroll/begin returns: the user's new
// TOTP secret, shown once, as text and as its otpauth URI for a QR code.
type EnrollmentStart struct {
	Username  string    `json:"username"`
	Secret    string    `json:"secret"`
	URI       string    `json:"uri"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// EnrollRequest is one use of an enrollment link. Source, UserAgent and
// Trusted are as in LoginRequest.
type EnrollRequest struct {
	Token, Password, Code, Source, UserAgent string
	Trusted                                  bool
}

// enrollment is a live link's row and its user.
type enrollment struct {
	user    userRecord
	expires time.Time
}

// loadEnrollment finds the live link whose hash is tokenHash: unused, not
// expired, and its user not disabled, pending or activated by an OIDC or
// Tailscale sign-in (spec 1.5). Anything else is auth.enrollment_expired.
func (s *Service) loadEnrollment(ctx context.Context, q querier, tokenHash string) (enrollment, error) {
	var userID, expText string
	var used sql.NullString
	err := q.QueryRowContext(ctx, `SELECT user_id, expires_at, used_at FROM core_user_enrollments WHERE token_hash = ?`,
		tokenHash).Scan(&userID, &expText, &used)
	if errors.Is(err, sql.ErrNoRows) {
		return enrollment{}, errEnrollmentExpired()
	}
	if err != nil {
		return enrollment{}, fmt.Errorf("auth: load enrollment: %w", err)
	}
	exp, err := parseTime(expText)
	if err != nil {
		return enrollment{}, err
	}
	var username string
	if err := q.QueryRowContext(ctx, `SELECT username FROM core_users WHERE id = ?`, userID).Scan(&username); err != nil {
		return enrollment{}, fmt.Errorf("auth: load enrollment user: %w", err)
	}
	r, err := loadUser(ctx, q, username)
	if err != nil {
		return enrollment{}, err
	}
	// An active user keeps an unused link only when an OIDC or Tailscale
	// sign-in activated its invite; the link still sets its password.
	if used.Valid || !s.now().Before(exp) || (r.State != StatePending && r.State != StateActive) || r.Disabled {
		return enrollment{}, errEnrollmentExpired()
	}
	return enrollment{user: r, expires: exp}, nil
}

// enrollAllowed checks the per-source budget the login shares (spec section
// 9): an attempt from a locked source is auth.rate_limited.
func (s *Service) enrollAllowed(ctx context.Context, source string, trusted bool) error {
	if d := s.limiter.sourceBlockedFor(limitSource(ctx, source), trusted && !proxied(ctx)); d > 0 {
		return errRateLimited(d)
	}
	return nil
}

func (s *Service) enrollFailed(ctx context.Context, source string, trusted bool) {
	s.limiter.failSourceFor(limitSource(ctx, source), trusted && !proxied(ctx))
}

func proxied(ctx context.Context) bool {
	_, ok := core.ProxiedFrom(ctx)
	return ok
}

// BeginEnrollment gives the user of a live enrollment link a new TOTP secret,
// replacing any an earlier begin made. An unknown, used or expired link is
// auth.enrollment_expired and counts against the source's budget.
func (s *Service) BeginEnrollment(ctx context.Context, token, source string, trusted bool) (EnrollmentStart, error) {
	if err := s.enrollAllowed(ctx, source, trusted); err != nil {
		return EnrollmentStart{}, err
	}
	tokenHash, ok := enrollTokenHash(token)
	if !ok {
		s.enrollFailed(ctx, source, trusted)
		return EnrollmentStart{}, errEnrollmentExpired()
	}
	key := newTOTPSecret()
	var out EnrollmentStart
	err := s.db.Write(ctx, func(tx *sql.Tx) error {
		e, err := s.loadEnrollment(ctx, tx, tokenHash)
		if err != nil {
			return err
		}
		ct, err := s.sealer.Seal(totpAD(e.user.ID), key)
		if err != nil {
			return fmt.Errorf("auth: seal TOTP secret: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE core_users SET totp_secret_ct = ?, totp_last_step = 0, updated_at = ? WHERE id = ?`,
			ct, formatTime(s.now()), e.user.ID); err != nil {
			return fmt.Errorf("auth: begin enrollment: %w", err)
		}
		secret := encodeTOTPSecret(key)
		out = EnrollmentStart{Username: e.user.Username, Secret: secret, URI: totpURI(e.user.Username, secret), ExpiresAt: e.expires}
		return nil
	})
	if errcode.From(err).Code == errcode.EnrollmentExpired {
		s.enrollFailed(ctx, source, trusted)
	}
	return out, err
}

// totpURI is the otpauth URI the CLI's enrollment shows too.
func totpURI(username, secret string) string {
	return "otpauth://totp/Darkpanel:" + url.PathEscape(username) + "?secret=" + secret +
		"&issuer=Darkpanel&algorithm=SHA1&digits=6&period=30"
}

// FinishEnrollment uses a live enrollment link: in one transaction it sets
// the password, checks the code against the secret BeginEnrollment made,
// activates the user, marks the link used and opens a session. An unknown,
// used or expired link is auth.enrollment_expired and a wrong code
// request.invalid_input; both count against the source's budget. Every
// evaluated attempt is audited as a login with method enrollment.
func (s *Service) FinishEnrollment(ctx context.Context, r EnrollRequest) (LoginResult, error) {
	if err := s.enrollAllowed(ctx, r.Source, r.Trusted); err != nil {
		return LoginResult{}, err
	}
	if err := checkPassword(r.Password); err != nil {
		return LoginResult{}, err
	}
	res, username, userID, err := s.finishEnrollment(ctx, r)
	code := errcode.Code("")
	if err != nil {
		code = errcode.From(err).Code
		if code == errcode.EnrollmentExpired || code == errcode.InvalidInput {
			s.enrollFailed(ctx, r.Source, r.Trusted)
		}
	}
	if aerr := s.recordSignIn(ctx, MethodEnrollment, username, r.Source, userID, code); aerr != nil && err == nil {
		return LoginResult{}, aerr
	}
	if err != nil {
		return LoginResult{}, err
	}
	return res, nil
}

func (s *Service) finishEnrollment(ctx context.Context, r EnrollRequest) (LoginResult, string, string, error) {
	tokenHash, ok := enrollTokenHash(r.Token)
	if !ok {
		return LoginResult{}, "", "", errEnrollmentExpired()
	}
	e, err := s.loadEnrollment(ctx, s.db.Read(), tokenHash)
	if err != nil {
		return LoginResult{}, "", "", err
	}
	hash, err := s.hasher.hash(ctx, r.Password)
	if err != nil {
		return LoginResult{}, e.user.Username, e.user.ID, err
	}
	var res LoginResult
	err = s.db.Write(ctx, func(tx *sql.Tx) error {
		cur, err := s.loadEnrollment(ctx, tx, tokenHash)
		if err != nil {
			return err
		}
		if cur.user.ID != e.user.ID {
			return errEnrollmentExpired()
		}
		key, err := s.sealer.Open(totpAD(cur.user.ID), cur.user.totpCT)
		if err != nil {
			return fmt.Errorf("auth: open TOTP secret: %w", err)
		}
		step, ok := matchTOTP(key, r.Code, s.clock.Now(), cur.user.lastStep)
		if !ok {
			return invalidField("/code", "does not match the authenticator")
		}
		now := formatTime(s.now())
		if _, err := tx.ExecContext(ctx, `UPDATE core_users SET password_hash = ?, state = ?, totp_last_step = ?, updated_at = ?
			WHERE id = ?`, hash, StateActive, step, now, cur.user.ID); err != nil {
			return fmt.Errorf("auth: finish enrollment: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE core_user_enrollments SET used_at = ? WHERE user_id = ?`, now, cur.user.ID); err != nil {
			return fmt.Errorf("auth: finish enrollment: %w", err)
		}
		id, exp, err := s.createSession(ctx, tx, cur.user.ID, r.Source, r.UserAgent)
		if err != nil {
			return err
		}
		cur.user.State = StateActive
		access, err := resolveAccess(ctx, tx, cur.user.User, "")
		if err != nil {
			return err
		}
		res = LoginResult{SessionID: id, CSRFToken: s.CSRFToken(id), ExpiresAt: exp, User: cur.user.User, Access: access}
		return nil
	})
	return res, e.user.Username, e.user.ID, err
}
