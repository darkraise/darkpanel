package api

import (
	"net/http"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/auth"
)

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Code     string `json:"code"`
}

// sessionResponse is the body of a login and of GET /api/v1/auth/session
// (spec section 12). A field that does not apply is null: csrfToken for a
// token, token for a session.
type sessionResponse struct {
	User      *auth.User      `json:"user"`
	CSRFToken *string         `json:"csrfToken"`
	ExpiresAt *time.Time      `json:"expiresAt"`
	Token     *auth.TokenInfo `json:"token"`
}

// sessionCookie is the session cookie of r's listener (spec section 9):
// HttpOnly, SameSite=Strict, Path=/, no Domain, and Secure exactly on HTTPS.
// It has no expiry; the server ends the session. A negative maxAge clears
// it.
func sessionCookie(r *http.Request, value string, maxAge int) *http.Cookie {
	return &http.Cookie{ //nolint:gosec // G124: Secure is set on HTTPS; the plain-HTTP loopback cookie cannot be Secure (spec section 9)
		Name:     sessionCookieName(r),
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		Secure:   r.TLS != nil,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	}
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var in loginRequest
	if err := decodeBody(w, r, &in); err != nil {
		writeProblem(w, s.log, err)
		return
	}
	res, err := s.cfg.Auth.Login(r.Context(), auth.LoginRequest{
		Username:  in.Username,
		Password:  in.Password,
		Code:      in.Code,
		Source:    clientAddr(r),
		UserAgent: r.UserAgent(),
	})
	if err != nil {
		writeProblem(w, s.log, err)
		return
	}
	http.SetCookie(w, sessionCookie(r, res.SessionID, 0))
	writeJSON(w, s.log, http.StatusOK, sessionResponse{User: &res.User, CSRFToken: &res.CSRFToken, ExpiresAt: &res.ExpiresAt})
}

// handleLogout ends the caller's session, when it is one, and clears the
// cookie either way.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	c, _ := callerFrom(r.Context())
	if c.identity.FromSession {
		if err := s.cfg.Auth.Logout(r.Context(), c.session, clientAddr(r)); err != nil {
			writeProblem(w, s.log, err)
			return
		}
	}
	http.SetCookie(w, sessionCookie(r, "", -1))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	c, _ := callerFrom(r.Context())
	id := c.identity
	resp := sessionResponse{User: &id.User, ExpiresAt: &id.ExpiresAt, Token: id.Token}
	if id.FromSession {
		csrf := s.cfg.Auth.CSRFToken(c.session)
		resp.CSRFToken = &csrf
	}
	writeJSON(w, s.log, http.StatusOK, resp)
}
