package api

import (
	"net/http"

	"github.com/darkraise/darkpanel/internal/controller/auth"
)

// The enrollment link's routes (M10 spec section 1.5): public, under the
// login's per-source budget.
const (
	enrollBeginPath  = "/api/v1/enroll/begin"
	enrollFinishPath = "/api/v1/enroll/finish"
)

type enrollBeginRequest struct {
	Token string `json:"token"`
}

type enrollFinishRequest struct {
	Token    string `json:"token"`
	Password string `json:"password"`
	Code     string `json:"code"`
}

// handleEnrollBegin gives the link's user a new TOTP secret and returns it
// with its otpauth URI for the QR code.
func (s *Server) handleEnrollBegin(w http.ResponseWriter, r *http.Request) {
	var in enrollBeginRequest
	if err := decodeBody(w, r, &in); err != nil {
		writeProblem(w, s.log, err)
		return
	}
	out, err := s.cfg.Auth.BeginEnrollment(r.Context(), in.Token, clientAddr(r), s.TrustedListener(r))
	if err != nil {
		writeProblem(w, s.log, err)
		return
	}
	writeJSON(w, s.log, http.StatusOK, out)
}

// handleEnrollFinish sets the password, checks the code, activates the user
// and signs it in, as a login does.
func (s *Server) handleEnrollFinish(w http.ResponseWriter, r *http.Request) {
	var in enrollFinishRequest
	if err := decodeBody(w, r, &in); err != nil {
		writeProblem(w, s.log, err)
		return
	}
	res, err := s.cfg.Auth.FinishEnrollment(r.Context(), auth.EnrollRequest{
		Token: in.Token, Password: in.Password, Code: in.Code,
		Source: clientAddr(r), UserAgent: r.UserAgent(), Trusted: s.TrustedListener(r),
	})
	if err != nil {
		writeProblem(w, s.log, err)
		return
	}
	http.SetCookie(w, sessionCookie(r, res.SessionID, 0))
	writeJSON(w, s.log, http.StatusOK, sessionResponse{User: &sessionUser{User: res.User, AllHosts: res.Access.Everywhere()},
		CSRFToken: &res.CSRFToken, ExpiresAt: &res.ExpiresAt})
}
