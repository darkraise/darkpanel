package api

import (
	"fmt"
	"net"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// The sign-in method routes (M10 spec sections 2 and 3, C8).
const (
	oidcStartPath    = "/api/v1/auth/oidc/start"
	oidcCallbackPath = auth.OIDCCallbackPath
	totpStepPath     = "/api/v1/auth/totp"
	methodsPath      = "/api/v1/auth/methods"

	oidcStateCookie  = "darkpanel_oidc_state"
	signInStepCookie = "darkpanel_signin_step"
)

// Outcomes of the callback page's navigation, /?signin=<outcome> (C8).
const (
	outcomeOK           = "ok"
	outcomeTOTP         = "totp"
	outcomeNoLinkedUser = "no_linked_user"
	outcomeMethodOff    = "method_off"
	outcomeRefused      = "refused"
)

// requestOrigin is the https origin r arrived on: a runtime listener's
// public host, or the Host of a TLS request; "" otherwise.
func requestOrigin(r *http.Request) string {
	if p, ok := core.ProxiedFrom(r.Context()); ok && p.PublicHost != "" {
		return "https://" + p.PublicHost
	}
	if r.TLS != nil && r.Host != "" {
		return "https://" + r.Host
	}
	return ""
}

// onTailscaleListener reports whether r arrived on a served Tailscale
// address; a proxied request never did.
func (s *Server) onTailscaleListener(r *http.Request) bool {
	if _, proxied := core.ProxiedFrom(r.Context()); proxied {
		return false
	}
	local, ok := r.Context().Value(http.LocalAddrContextKey).(*net.TCPAddr)
	if !ok {
		return false
	}
	addrs := s.tailscale.Load()
	return addrs != nil && slices.Contains((*addrs)(), local.AddrPort().Addr().Unmap())
}

type oidcButton struct {
	DisplayName string `json:"displayName"`
}

type methodsResponse struct {
	OIDC      *oidcButton `json:"oidc"`
	Tailscale bool        `json:"tailscale"`
}

// handleMethods answers which sign-in buttons the login page shows here:
// OIDC on its redirect base's origin, Tailscale on a Tailscale listener.
func (s *Server) handleMethods(w http.ResponseWriter, r *http.Request) {
	set, err := s.cfg.Auth.SignInMethods(r.Context())
	if err != nil {
		writeProblem(w, s.log, err)
		return
	}
	out := methodsResponse{Tailscale: set.Tailscale.Enabled && s.onTailscaleListener(r)}
	if set.OIDC != nil && auth.OriginMatches(requestOrigin(r), set.OIDC.RedirectBase) {
		out.OIDC = &oidcButton{DisplayName: set.OIDC.DisplayName}
	}
	writeJSON(w, s.log, http.StatusOK, out)
}

// flowCookie is a short-lived cookie of the sign-in flow, scoped to path.
// The state cookie is Lax, since the provider's redirect back is a
// cross-site navigation; the code step's cookie is Strict.
func flowCookie(name, value, path string, ttl time.Duration, lax bool) *http.Cookie {
	maxAge := int(ttl / time.Second)
	if value == "" {
		maxAge = -1
	}
	site := http.SameSiteStrictMode
	if lax {
		site = http.SameSiteLaxMode
	}
	return &http.Cookie{Name: name, Value: value, Path: path, MaxAge: maxAge, Secure: true, HttpOnly: true, SameSite: site} //nolint:gosec // G124: Secure and HttpOnly are set; SameSite is Lax or Strict
}

// handleOIDCStart sends the browser to the provider with a new state
// cookie. A refusal answers the sign-in page, as the callback does.
func (s *Server) handleOIDCStart(w http.ResponseWriter, r *http.Request) {
	start, err := s.cfg.Auth.StartOIDC(r.Context(), requestOrigin(r))
	if err != nil {
		writeSignInPage(w, s, err)
		return
	}
	http.SetCookie(w, flowCookie(oidcStateCookie, start.Cookie, oidcCallbackPath, auth.OIDCStateTTL, true))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.Redirect(w, r, start.URL, http.StatusFound)
}

// handleOIDCCallback finishes the provider's redirect back. It answers 200
// with a page that navigates to /, never a redirect, so the Strict session
// cookie it sets is sent on the next request.
func (s *Server) handleOIDCCallback(w http.ResponseWriter, r *http.Request) {
	var state string
	if c, err := r.Cookie(oidcStateCookie); err == nil {
		state = c.Value
	}
	http.SetCookie(w, flowCookie(oidcStateCookie, "", oidcCallbackPath, 0, true))
	q := r.URL.Query()
	res, err := s.cfg.Auth.FinishOIDC(r.Context(), auth.OIDCCallback{Cookie: state, State: q.Get("state"), Code: q.Get("code"),
		Source: clientAddr(r), UserAgent: r.UserAgent(), Trusted: s.TrustedListener(r)})
	if err != nil {
		writeSignInPage(w, s, err)
		return
	}
	if res.Ticket != "" {
		http.SetCookie(w, flowCookie(signInStepCookie, res.Ticket, totpStepPath, auth.SignInTicketTTL, false))
		writeOutcomePage(w, http.StatusOK, outcomeTOTP)
		return
	}
	http.SetCookie(w, sessionCookie(r, res.SessionID, 0))
	writeOutcomePage(w, http.StatusOK, outcomeOK)
}

// handleTOTPStep checks the code a sign-in method's ticket waits for and
// answers as a login does.
func (s *Server) handleTOTPStep(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Code string `json:"code"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		writeProblem(w, s.log, err)
		return
	}
	var ticket string
	if c, err := r.Cookie(signInStepCookie); err == nil {
		ticket = c.Value
	}
	step := auth.TOTPStepRequest{Ticket: ticket, Code: in.Code,
		Source: clientAddr(r), UserAgent: r.UserAgent(), Trusted: s.TrustedListener(r)}
	if addr, ok := s.tailnetPeer(r); ok {
		step.Remote = addr
	}
	res, err := s.cfg.Auth.FinishTOTPStep(r.Context(), step)
	if err != nil {
		writeProblem(w, s.log, err)
		return
	}
	http.SetCookie(w, flowCookie(signInStepCookie, "", totpStepPath, 0, false))
	http.SetCookie(w, sessionCookie(r, res.SessionID, 0))
	writeJSON(w, s.log, http.StatusOK, sessionResponse{User: &sessionUser{User: res.User, AllHosts: res.Access.Everywhere()},
		CSRFToken: &res.CSRFToken, ExpiresAt: &res.ExpiresAt})
}

// writeSignInPage answers err as the sign-in page's outcome: too many
// attempts with 429, anything else with 200. An internal fault is logged.
func writeSignInPage(w http.ResponseWriter, s *Server, err error) {
	status, outcome := http.StatusOK, outcomeRefused
	switch errcode.From(err).Code {
	case errcode.NoLinkedUser:
		outcome = outcomeNoLinkedUser
	case errcode.MethodOff:
		outcome = outcomeMethodOff
	case errcode.RateLimited:
		status = http.StatusTooManyRequests
	case errcode.InvalidCredentials:
	default:
		s.log.Error("api: OIDC sign-in failed", "code", errcode.From(err).Code, "err", err)
	}
	writeOutcomePage(w, status, outcome)
}

// writeOutcomePage answers a same-origin page whose one navigation is a
// meta refresh to /?signin=<outcome>; it runs no script.
func writeOutcomePage(w http.ResponseWriter, status int, outcome string) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("Referrer-Policy", "no-referrer")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `<!doctype html><html lang="en"><head><meta charset="utf-8">`+
		`<meta http-equiv="refresh" content="0;url=/?signin=%s"><title>Darkpanel</title></head><body></body></html>`,
		strings.ReplaceAll(outcome, `"`, ""))
}
