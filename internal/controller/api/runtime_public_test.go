package api

import (
	"net/http"
	"net/netip"
	"slices"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)

func TestV1r19RuntimeHandlerCarriesThePublicBinding(t *testing.T) {
	binding := netip.MustParseAddrPort("0.0.0.0:443")
	for _, tt := range []struct {
		name    string
		header  string
		binding netip.AddrPort
		want    []netip.AddrPort
	}{
		{"npm with its published binding", core.ClientHeaderNPM, binding, []netip.AddrPort{binding}},
		{"npm recorded without one", core.ClientHeaderNPM, netip.AddrPort{}, nil},
		{"tunnel", core.ClientHeaderTunnel, netip.AddrPort{}, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			info := rtInfo(tt.header)
			info.Spec.PublicBinding = tt.binding
			rec, _, p := rtServe(t, info, make(chan struct{}), rtNotRevoked, rtRequest(map[string][]string{
				tt.header: {"203.0.113.7"}, "X-Forwarded-Proto": {"https"},
			}))
			if rec.Code != http.StatusNoContent || !p.found {
				t.Fatalf("answer %d %s, Proxied found %v", rec.Code, rec.Body.String(), p.found)
			}
			if !slices.Equal(p.proxied.Path.Public, tt.want) {
				t.Errorf("Path.Public = %v, want %v", p.proxied.Path.Public, tt.want)
			}
		})
	}
}
