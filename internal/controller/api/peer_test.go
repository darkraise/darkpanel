package api

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)

// peerProbe records the client peer the next handler saw.
type peerProbe struct {
	peer  core.Peer
	found bool
}

func (p *peerProbe) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.peer, p.found = core.ClientPeer(r.Context())
	w.WriteHeader(http.StatusNoContent)
}

func TestAPIRecordsClientPeer(t *testing.T) {
	h := newHarness(t)
	token := h.token(core.ScopeRead)
	for _, tc := range []struct {
		remote string
		want   *core.Peer // nil: no peer
	}{
		{"192.0.2.7:51234", &core.Peer{Addr: netip.MustParseAddr("192.0.2.7"), Port: 51234}},
		{"[2001:db8::7]:443", &core.Peer{Addr: netip.MustParseAddr("2001:db8::7"), Port: 443}},
		{"[::ffff:192.0.2.8]:80", &core.Peer{Addr: netip.MustParseAddr("192.0.2.8"), Port: 80}},
		{"@", nil},
	} {
		// An authenticated route and a public one.
		for _, path := range []string{"/api/v1/hosts", "/"} {
			r := withToken(request("GET", path, ""), token)
			r.RemoteAddr = tc.remote
			p := &peerProbe{}
			rec := httptest.NewRecorder()
			h.srv.authenticate(p).ServeHTTP(rec, r)
			switch {
			case rec.Code != http.StatusNoContent:
				t.Errorf("%s %s: status %d, body %s", tc.remote, path, rec.Code, rec.Body)
			case tc.want == nil && p.found:
				t.Errorf("%s %s: peer %+v, want none", tc.remote, path, p.peer)
			case tc.want != nil && (!p.found || p.peer != *tc.want):
				t.Errorf("%s %s: peer %+v (%v), want %+v", tc.remote, path, p.peer, p.found, *tc.want)
			}
		}
	}
}
