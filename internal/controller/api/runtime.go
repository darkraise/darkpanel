package api

import (
	"context"
	"net/http"
	"net/netip"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// forwardedProtoHeader is the forwarded protocol a proxy Darkpanel
// configured sets (spec 1.10); NPM proxy hosts force SSL and tunnels are
// HTTPS at the edge, so anything but https means a misconfigured hop.
const forwardedProtoHeader = "X-Forwarded-Proto"

// RuntimeHandler serves one runtime listener: it refuses a request on a
// revoked listener, a forwarded protocol other than https and a missing or
// malformed client header, then serves s.Handler() with core.Proxied in
// the request's context. revoked is closed once the listener is revoked;
// isRevoked re-reads the durable state for every request. The listener
// reads exactly the one client header its proxy sets and overwrites;
// X-Forwarded-For and X-Real-IP come from the client and are never read.
func (s *Server) RuntimeHandler(info core.ListenerInfo, revoked <-chan struct{}, isRevoked func(ctx context.Context) (bool, error)) http.Handler {
	kind := "npm"
	if info.Spec.ClientHeader == core.ClientHeaderTunnel {
		kind = "tunnel"
	}
	path := core.ProxyPath{ExposureID: info.Spec.ChangeID, Kind: kind, ProxyAddr: info.Spec.AllowedPeer, ListenerAddr: info.Spec.Addr}
	return securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := runtimeRevoked(r.Context(), revoked, isRevoked); err != nil {
			writeProblem(w, s.log, err)
			return
		}
		if v := r.Header.Values(forwardedProtoHeader); len(v) != 1 || v[0] != "https" {
			writeProblem(w, s.log, errcode.New(errcode.Forbidden, "a request through an exposure must arrive over https"))
			return
		}
		client, ok := runtimeClient(r.Header.Values(info.Spec.ClientHeader))
		if !ok {
			writeProblem(w, s.log, errcode.New(errcode.Forbidden, "the proxy's "+info.Spec.ClientHeader+" header is missing or malformed"))
			return
		}
		transport, err := netip.ParseAddrPort(r.RemoteAddr)
		if err != nil {
			writeProblem(w, s.log, errcode.New(errcode.Forbidden, "the request has no TCP peer"))
			return
		}
		ctx := core.WithProxied(r.Context(), core.Proxied{
			ListenerID: info.ID,
			Client:     client,
			Transport:  netip.AddrPortFrom(transport.Addr().Unmap(), transport.Port()),
			PublicHost: info.Spec.PublicHost,
			Path:       path,
			Revoked:    revoked,
		})
		s.handler.ServeHTTP(w, r.WithContext(ctx))
	}))
}

// runtimeRevoked refuses a request on a revoked listener. The durable state
// decides: revocation commits it before anything else (spec 1.10), so a
// request racing the in-memory close still finds it. A state that cannot
// be read refuses the request too.
func runtimeRevoked(ctx context.Context, revoked <-chan struct{}, isRevoked func(ctx context.Context) (bool, error)) error {
	select {
	case <-revoked:
		return errcode.New(errcode.NotFound, "this exposure was removed")
	default:
	}
	gone, err := isRevoked(ctx)
	if err != nil {
		return errcode.Wrap(errcode.Unavailable, err, "the exposure's listener state cannot be read")
	}
	if gone {
		return errcode.New(errcode.NotFound, "this exposure was removed")
	}
	return nil
}

// runtimeClient parses the one value of the client header: a bare IP
// address, IPv4-mapped addresses unmapped. Several values, a port or a zone
// make it malformed.
func runtimeClient(values []string) (netip.Addr, bool) {
	if len(values) != 1 {
		return netip.Addr{}, false
	}
	a, err := netip.ParseAddr(strings.TrimSpace(values[0]))
	if err != nil || a.Zone() != "" {
		return netip.Addr{}, false
	}
	return a.Unmap(), true
}
