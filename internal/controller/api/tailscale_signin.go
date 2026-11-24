package api

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/netip"
	"sync"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// tailscalePath is Tailscale identity sign-in (M10 spec section 3, C8).
const tailscalePath = "/api/v1/auth/tailscale"

// The whois throttle of spec section 3: answers cached per remote IP, and
// a budget of lookups per IP and minute.
const (
	whoisMatchTTL      = 10 * time.Second
	whoisNoneTTL       = 60 * time.Second
	whoisLookupsPerMin = 6
)

// Tailscale's address ranges; a request from any other address never uses
// the method.
var (
	tailnetV4 = netip.MustParsePrefix("100.64.0.0/10")
	tailnetV6 = netip.MustParsePrefix("fd7a:115c:a1e0::/48")
)

// whoisThrottle holds the cached answers and the lookup times per remote IP.
type whoisThrottle struct {
	mu      sync.Mutex
	cache   map[netip.Addr]whoisCached
	lookups map[netip.Addr][]time.Time
}

type whoisCached struct {
	match auth.TailscaleMatch
	err   error
	until time.Time
}

// cached is addr's answer while it lasts.
func (t *whoisThrottle) cached(addr netip.Addr, now time.Time) (whoisCached, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	c, ok := t.cache[addr]
	if !ok || !now.Before(c.until) {
		delete(t.cache, addr)
		return whoisCached{}, false
	}
	return c, true
}

func (t *whoisThrottle) store(addr netip.Addr, c whoisCached) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cache == nil {
		t.cache = map[netip.Addr]whoisCached{}
	}
	t.cache[addr] = c
}

// spend takes one lookup from addr's budget; false when the minute's
// budget is gone.
func (t *whoisThrottle) spend(addr netip.Addr, now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.lookups == nil {
		t.lookups = map[netip.Addr][]time.Time{}
	}
	recent := t.lookups[addr][:0]
	for _, at := range t.lookups[addr] {
		if now.Sub(at) < time.Minute {
			recent = append(recent, at)
		}
	}
	if len(recent) >= whoisLookupsPerMin {
		t.lookups[addr] = recent
		return false
	}
	t.lookups[addr] = append(recent, now)
	return true
}

// tailscaleRemote is the request's tailnet address once the request may use
// the method: over TLS on a Tailscale listener, not proxied, with the method on, and
// from an address inside Tailscale's ranges. No agent call happens before.
func (s *Server) tailscaleRemote(r *http.Request) (netip.Addr, error) {
	// TLS binds the request's Host, and so the Origin the POST is checked
	// against, to a name the listener's certificate holds: a plain-HTTP
	// listener on a tailnet address never serves this method.
	if r.TLS == nil || !s.onTailscaleListener(r) {
		return netip.Addr{}, errcode.New(errcode.MethodOff, "Tailscale sign-in is offered only on the Tailscale listener")
	}
	set, err := s.cfg.Auth.SignInMethods(r.Context())
	if err != nil {
		return netip.Addr{}, err
	}
	if !set.Tailscale.Enabled {
		return netip.Addr{}, errcode.New(errcode.MethodOff, "this sign-in method is off")
	}
	addr, ok := tailnetAddr(r)
	if !ok {
		return netip.Addr{}, errcode.New(errcode.InvalidCredentials, "the sign-in was refused")
	}
	return addr, nil
}

// tailnetPeer is the request's unmapped tailnet address when it arrived over
// TLS on a Tailscale listener and was not proxied; false otherwise. It asks
// no agent.
func (s *Server) tailnetPeer(r *http.Request) (netip.Addr, bool) {
	if r.TLS == nil || !s.onTailscaleListener(r) {
		return netip.Addr{}, false
	}
	return tailnetAddr(r)
}

// tailnetAddr is the request's remote address, unmapped, when it lies in
// Tailscale's ranges and is not loopback.
func tailnetAddr(r *http.Request) (netip.Addr, bool) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return netip.Addr{}, false
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, false
	}
	addr = addr.Unmap()
	if addr.IsLoopback() || !tailnetV4.Contains(addr) && !tailnetV6.Contains(addr) {
		return netip.Addr{}, false
	}
	return addr, true
}

// whois asks the controller host's agent who holds addr, after spending one
// lookup of addr's budget. A failed call or an answer that does not decode
// refuses; the cause is logged, never answered.
func (s *Server) whois(ctx context.Context, addr netip.Addr) (auth.TailnetWhois, error) {
	if !s.whoisLimit.spend(addr, s.cfg.Clock.Now()) {
		return auth.TailnetWhois{}, errcode.New(errcode.RateLimited, "too many Tailscale sign-in lookups; wait a minute")
	}
	refused := errcode.New(errcode.InvalidCredentials, "the sign-in was refused")
	if s.cfg.Whois == nil {
		return auth.TailnetWhois{}, refused
	}
	raw, err := s.cfg.Whois(ctx, addr)
	if err != nil {
		s.log.Warn("api: Tailscale whois failed", "code", errcode.From(err).Code, "err", err)
		return auth.TailnetWhois{}, refused
	}
	var w auth.TailnetWhois
	if err := json.Unmarshal(raw, &w); err != nil {
		s.log.Warn("api: Tailscale whois answer does not decode", "err", err)
		return auth.TailnetWhois{}, refused
	}
	return w, nil
}

// handleTailscaleMatch answers the user the requesting node may continue
// as. Answers are cached per remote IP: a match for 10 s, anything else for
// 60 s.
func (s *Server) handleTailscaleMatch(w http.ResponseWriter, r *http.Request) {
	addr, err := s.tailscaleRemote(r)
	if err != nil {
		writeProblem(w, s.log, err)
		return
	}
	now := s.cfg.Clock.Now()
	c, ok := s.whoisLimit.cached(addr, now)
	if !ok {
		var who auth.TailnetWhois
		if who, err = s.whois(r.Context(), addr); err != nil {
			if errcode.From(err).Code != errcode.RateLimited {
				s.whoisLimit.store(addr, whoisCached{err: err, until: now.Add(whoisNoneTTL)})
			}
			writeProblem(w, s.log, err)
			return
		}
		c.match, c.err = s.cfg.Auth.TailscaleMatchFor(r.Context(), auth.TailscaleRequest{Remote: addr, Whois: who})
		c.until = now.Add(whoisMatchTTL)
		if c.err != nil {
			c.until = now.Add(whoisNoneTTL)
		}
		s.whoisLimit.store(addr, c)
	}
	if c.err != nil {
		writeProblem(w, s.log, c.err)
		return
	}
	writeJSON(w, s.log, http.StatusOK, c.match)
}

type tailscaleStepResponse struct {
	TOTP bool `json:"totp"`
	// Remember reports that a right code will remember the node.
	Remember bool `json:"remember"`
}

// handleTailscaleSignIn signs the requesting node in as its linked user. It
// runs a fresh whois, past the cache but within the budget, and needs the
// request's own Origin and a JSON body.
func (s *Server) handleTailscaleSignIn(w http.ResponseWriter, r *http.Request) {
	addr, err := s.tailscaleRemote(r)
	if err != nil {
		writeProblem(w, s.log, err)
		return
	}
	if o := r.Header.Get("Origin"); o == "" || o != ownOrigin(r) {
		writeProblem(w, s.log, errcode.New(errcode.Forbidden, "the request's Origin is not this panel's"))
		return
	}
	var in struct{}
	if err := decodeBody(w, r, &in); err != nil {
		writeProblem(w, s.log, err)
		return
	}
	who, err := s.whois(r.Context(), addr)
	if err != nil {
		if errcode.From(err).Code != errcode.RateLimited {
			err = s.cfg.Auth.RefuseTailscale(r.Context(), clientAddr(r), err)
		}
		writeProblem(w, s.log, err)
		return
	}
	res, err := s.cfg.Auth.SignInTailscale(r.Context(), auth.TailscaleRequest{Remote: addr, Whois: who,
		Source: clientAddr(r), UserAgent: r.UserAgent(), Trusted: s.TrustedListener(r)})
	if err != nil {
		writeProblem(w, s.log, err)
		return
	}
	if res.Ticket != "" {
		http.SetCookie(w, flowCookie(signInStepCookie, res.Ticket, totpStepPath, auth.SignInTicketTTL, false))
		writeJSON(w, s.log, http.StatusOK, tailscaleStepResponse{TOTP: true, Remember: res.Remember})
		return
	}
	http.SetCookie(w, sessionCookie(r, res.SessionID, 0))
	writeJSON(w, s.log, http.StatusOK, sessionResponse{User: &sessionUser{User: res.User, AllHosts: res.Access.Everywhere()},
		CSRFToken: &res.CSRFToken, ExpiresAt: &res.ExpiresAt})
}

// ownOrigin is the origin r arrived on; tailscaleRemote has refused any
// request without TLS.
func ownOrigin(r *http.Request) string { return "https://" + r.Host }
