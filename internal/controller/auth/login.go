package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// Audit rows of logins and logouts (spec section 11, ADR 0011).
const (
	AuditLoginAction  = "core.auth.login"
	AuditLogoutAction = "core.auth.logout"
	ActorAnonymous    = "anonymous"
)

// LoginRequest is one login attempt. Source is the client address and
// UserAgent its User-Agent header; both are stored with the session.
type LoginRequest struct {
	Username, Password, Code, Source, UserAgent string
}

// LoginResult is a successful login. SessionID is the session cookie's value.
type LoginResult struct {
	SessionID string
	CSRFToken string
	ExpiresAt time.Time
	User      User
}

// Credentials are what a request presents. Authorization holds every value
// of the Authorization header and is nil when the header is absent; Session
// is the session cookie's value, "" when absent.
type Credentials struct {
	Authorization []string
	Session       string
	Source        string
}

func errInvalidCredentials() *errcode.Error {
	return errcode.New(errcode.InvalidCredentials, "invalid username, password or code")
}

// Login checks username, password and TOTP code and opens a session. Every
// failure is the same auth.invalid_credentials error. A username or source
// address locked by the rate limits is auth.rate_limited, with a
// *RetryAfterError cause, and is not evaluated. Evaluated attempts are
// audited; a username that fails the pattern is refused before anything
// else, unaudited.
func (s *Service) Login(ctx context.Context, r LoginRequest) (LoginResult, error) {
	if checkUsername(r.Username) != nil {
		return LoginResult{}, errInvalidCredentials()
	}
	if d := s.limiter.blocked(r.Username, r.Source); d > 0 {
		return LoginResult{}, errRateLimited(d)
	}
	if err := s.hasher.acquire(ctx); err != nil {
		return LoginResult{}, err
	}
	res, userID, err := s.evaluate(ctx, r)
	s.hasher.release()
	if err != nil {
		if errcode.From(err).Code != errcode.InvalidCredentials {
			return LoginResult{}, err
		}
		if aerr := s.recordLogin(ctx, r, userID, errcode.InvalidCredentials); aerr != nil {
			return LoginResult{}, aerr
		}
		return LoginResult{}, err
	}
	if err := s.recordLogin(ctx, r, userID, ""); err != nil {
		return LoginResult{}, err
	}
	return res, nil
}

func errRateLimited(d time.Duration) error {
	return errcode.Wrap(errcode.RateLimited, &RetryAfterError{After: d},
		"too many failed login attempts; try again later")
}

// evaluate runs attempt for a caller holding a hashing slot. The limits are
// checked again and a failure is counted under the slot: attempts that passed
// Login's first check while waiting for a slot would otherwise all be
// evaluated before any of their failures counted.
func (s *Service) evaluate(ctx context.Context, r LoginRequest) (LoginResult, string, error) {
	if d := s.limiter.blocked(r.Username, r.Source); d > 0 {
		return LoginResult{}, "", errRateLimited(d)
	}
	res, userID, err := s.attempt(ctx, r)
	if err != nil && errcode.From(err).Code == errcode.InvalidCredentials {
		s.limiter.fail(r.Username, r.Source)
	}
	return res, userID, err
}

// attempt evaluates a login and returns the matched user's id ("" when none
// matched). An unknown username still costs one argon2id hash. The caller
// holds a hashing slot.
func (s *Service) attempt(ctx context.Context, r LoginRequest) (LoginResult, string, error) {
	rec, err := loadUser(ctx, s.db.Read(), r.Username)
	if err != nil {
		if errcode.From(err).Code != errcode.NotFound {
			return LoginResult{}, "", err
		}
		if err := s.hasher.verifyDummyHeld(r.Password); err != nil {
			return LoginResult{}, "", err
		}
		return LoginResult{}, "", errInvalidCredentials()
	}
	ok, err := s.hasher.verifyHeld(rec.passwordHash, r.Password)
	if err != nil {
		return LoginResult{}, rec.ID, err
	}
	if !ok || rec.State != StateActive {
		return LoginResult{}, rec.ID, errInvalidCredentials()
	}
	var res LoginResult
	// The code's step is checked and stored in the same write transaction as
	// the session, so two logins racing with one code cannot both succeed.
	err = s.db.Write(ctx, func(tx *sql.Tx) error {
		cur, err := loadUser(ctx, tx, r.Username)
		if err != nil {
			if errcode.From(err).Code == errcode.NotFound {
				return errInvalidCredentials()
			}
			return err
		}
		if cur.ID != rec.ID || cur.passwordHash != rec.passwordHash || cur.State != StateActive {
			return errInvalidCredentials()
		}
		key, err := s.sealer.Open(totpAD(cur.ID), cur.totpCT)
		if err != nil {
			return fmt.Errorf("auth: open TOTP secret: %w", err)
		}
		step, ok := matchTOTP(key, r.Code, s.clock.Now(), cur.lastStep)
		if !ok {
			return errInvalidCredentials()
		}
		if _, err := tx.ExecContext(ctx, `UPDATE core_users SET totp_last_step = ? WHERE id = ?`, step, cur.ID); err != nil {
			return fmt.Errorf("auth: store TOTP step: %w", err)
		}
		id, exp, err := s.createSession(ctx, tx, cur.ID, r.Source, r.UserAgent)
		if err != nil {
			return err
		}
		res = LoginResult{SessionID: id, CSRFToken: s.CSRFToken(id), ExpiresAt: exp, User: cur.User}
		return nil
	})
	return res, rec.ID, err
}

// recordLogin audits a login; code is "" for a success. The row is written
// even if the request was cancelled, so a session never exists unaudited.
func (s *Service) recordLogin(ctx context.Context, r LoginRequest, userID string, code errcode.Code) error {
	actor := ActorAnonymous
	if userID != "" {
		actor = "user:" + userID
	}
	input, err := json.Marshal(map[string]string{"username": r.Username})
	if err != nil {
		return fmt.Errorf("auth: audit login: %w", err)
	}
	e := core.AuditEntry{ID: s.newID(), Actor: actor, Action: AuditLoginAction, Version: 1, Phase: "login",
		Result: "ok", Source: r.Source, At: s.now(), Input: input}
	if code != "" {
		e.Result, e.ErrorCode = "error", string(code)
	}
	if err := s.audit.Record(context.WithoutCancel(ctx), e); err != nil {
		return fmt.Errorf("auth: audit login: %w", err)
	}
	return nil
}

// Logout ends the session sessionID and audits it. Ending a session that does
// not exist does nothing.
func (s *Service) Logout(ctx context.Context, sessionID, source string) error {
	userID, ok, err := s.endSession(ctx, sessionID)
	if err != nil || !ok {
		return err
	}
	e := core.AuditEntry{ID: s.newID(), Actor: "user:" + userID, Action: AuditLogoutAction, Version: 1, Phase: "logout",
		Result: "ok", Source: source, At: s.now(), Input: json.RawMessage(`{}`)}
	if err := s.audit.Record(context.WithoutCancel(ctx), e); err != nil {
		return fmt.Errorf("auth: audit logout: %w", err)
	}
	return nil
}

// Authenticate resolves a request's caller. When an Authorization header is
// present only its bearer token is considered and the cookie never is (spec
// section 9); otherwise the session cookie is. Any failure is auth.required.
func (s *Service) Authenticate(ctx context.Context, c Credentials) (Identity, error) {
	if c.Authorization != nil {
		if len(c.Authorization) != 1 {
			return Identity{}, errAuthRequired()
		}
		scheme, token, ok := strings.Cut(c.Authorization[0], " ")
		if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" {
			return Identity{}, errAuthRequired()
		}
		return s.authenticateToken(ctx, token, c.Source)
	}
	if c.Session == "" {
		return Identity{}, errAuthRequired()
	}
	return s.authenticateSession(ctx, c.Session, c.Source)
}
