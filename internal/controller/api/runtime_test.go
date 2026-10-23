package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"reflect"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)

// rtInfo is a runtime listener of kind header on the panel network.
func rtInfo(header string) core.ListenerInfo {
	return core.ListenerInfo{ID: "L1", State: core.ListenerOpen, Spec: core.ListenerSpec{
		Addr: netip.MustParseAddrPort("172.31.255.1:9443"), AllowedPeer: netip.MustParseAddr("172.31.255.2"),
		ClientHeader: header, PublicHost: "panel.example.com", ChangeID: "01CHANGE00000000000000000A",
	}}
}

// rtProbe records the Proxied of the one request that reached it.
type rtProbe struct {
	ran     bool
	proxied core.Proxied
	found   bool
}

func (p *rtProbe) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.ran = true
	p.proxied, p.found = core.ProxiedFrom(r.Context())
	w.WriteHeader(http.StatusNoContent)
}

// rtRequest is a GET from the proxy at 172.31.255.2 with headers.
func rtRequest(headers map[string][]string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "https://panel.example.com/api/v1/auth/session", nil)
	r.RemoteAddr = "172.31.255.2:40000"
	for k, vs := range headers {
		for _, v := range vs {
			r.Header.Add(k, v)
		}
	}
	return r
}

// rtServe serves r through RuntimeHandler of info over a probe in place of
// the API's handler and returns the answer, its problem code and the probe.
func rtServe(t *testing.T, info core.ListenerInfo, revoked chan struct{}, isRevoked func(context.Context) (bool, error),
	r *http.Request) (*httptest.ResponseRecorder, string, *rtProbe) {
	t.Helper()
	h := newHarness(t)
	p := &rtProbe{}
	h.srv.handler = p
	rec := httptest.NewRecorder()
	h.srv.RuntimeHandler(info, revoked, isRevoked).ServeHTTP(rec, r)
	var prob struct {
		Code string `json:"code"`
	}
	if rec.Code != http.StatusNoContent {
		if err := json.Unmarshal(rec.Body.Bytes(), &prob); err != nil {
			t.Fatalf("answer %d %q is not a problem: %v", rec.Code, rec.Body.String(), err)
		}
	}
	return rec, prob.Code, p
}

func rtNotRevoked(context.Context) (bool, error) { return false, nil }

func TestRuntimeHandlerProxied(t *testing.T) {
	tests := []struct {
		name, header, value string
		client              string
		kind                string
	}{
		{"npm", core.ClientHeaderNPM, "203.0.113.7", "203.0.113.7", "npm"},
		{"tunnel over IPv6", core.ClientHeaderTunnel, "2001:db8::7", "2001:db8::7", "tunnel"},
		{"IPv4-mapped value", core.ClientHeaderNPM, "::ffff:203.0.113.7", "203.0.113.7", "npm"},
		{"surrounding blanks", core.ClientHeaderNPM, " 203.0.113.7 ", "203.0.113.7", "npm"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := rtInfo(tt.header)
			revoked := make(chan struct{})
			rec, _, p := rtServe(t, info, revoked, rtNotRevoked, rtRequest(map[string][]string{
				tt.header: {tt.value}, "X-Forwarded-Proto": {"https"},
				"X-Forwarded-For": {"198.51.100.1"}, "X-Real-IP": {"198.51.100.2"},
			}))
			if rec.Code != http.StatusNoContent || !p.ran || !p.found {
				t.Fatalf("answer %d %s, probe ran %v with Proxied %v", rec.Code, rec.Body.String(), p.ran, p.found)
			}
			want := core.Proxied{
				ListenerID: "L1",
				Client:     netip.MustParseAddr(tt.client),
				Transport:  netip.MustParseAddrPort("172.31.255.2:40000"),
				PublicHost: "panel.example.com",
				Path: core.ProxyPath{ExposureID: "01CHANGE00000000000000000A", Kind: tt.kind,
					ProxyAddr: netip.MustParseAddr("172.31.255.2"), ListenerAddr: netip.MustParseAddrPort("172.31.255.1:9443")},
				Revoked: revoked,
			}
			if !reflect.DeepEqual(p.proxied, want) {
				t.Errorf("Proxied\n%+v\nwant\n%+v", p.proxied, want)
			}
			if rec.Header().Get("Content-Security-Policy") == "" {
				t.Error("the security headers are missing")
			}
		})
	}
}

func TestRuntimeHandlerRefusals(t *testing.T) {
	npm := core.ClientHeaderNPM
	ok := map[string][]string{npm: {"203.0.113.7"}, "X-Forwarded-Proto": {"https"}}
	with := func(k string, v ...string) map[string][]string {
		h := map[string][]string{}
		for key, vs := range ok {
			h[key] = vs
		}
		if v == nil {
			delete(h, k)
		} else {
			h[k] = v
		}
		return h
	}
	closed := make(chan struct{})
	close(closed)
	tests := []struct {
		name      string
		headers   map[string][]string
		revoked   chan struct{}
		isRevoked func(context.Context) (bool, error)
		status    int
		code      string
	}{
		{"no client header, only forwarded ones", map[string][]string{"X-Forwarded-Proto": {"https"},
			"X-Forwarded-For": {"203.0.113.7"}, "X-Real-IP": {"203.0.113.7"}}, nil, nil, http.StatusForbidden, "auth.forbidden"},
		{"the other kind's header", map[string][]string{core.ClientHeaderTunnel: {"203.0.113.7"}, "X-Forwarded-Proto": {"https"}},
			nil, nil, http.StatusForbidden, "auth.forbidden"},
		{"an address with a port", with(npm, "203.0.113.7:80"), nil, nil, http.StatusForbidden, "auth.forbidden"},
		{"a host name", with(npm, "client.example.com"), nil, nil, http.StatusForbidden, "auth.forbidden"},
		{"an empty value", with(npm, ""), nil, nil, http.StatusForbidden, "auth.forbidden"},
		{"two values", with(npm, "203.0.113.7", "198.51.100.1"), nil, nil, http.StatusForbidden, "auth.forbidden"},
		{"a zone", with(npm, "fe80::1%eth0"), nil, nil, http.StatusForbidden, "auth.forbidden"},
		{"plain http", with("X-Forwarded-Proto", "http"), nil, nil, http.StatusForbidden, "auth.forbidden"},
		{"no forwarded protocol", with("X-Forwarded-Proto"), nil, nil, http.StatusForbidden, "auth.forbidden"},
		{"revoked in memory", ok, closed, nil, http.StatusNotFound, "not_found"},
		{"revoked durably", ok, nil, func(context.Context) (bool, error) { return true, nil }, http.StatusNotFound, "not_found"},
		{"state unreadable", ok, nil, func(context.Context) (bool, error) { return false, errors.New("database is locked") },
			http.StatusServiceUnavailable, "unavailable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			revoked, isRevoked := tt.revoked, tt.isRevoked
			if revoked == nil {
				revoked = make(chan struct{})
			}
			if isRevoked == nil {
				isRevoked = rtNotRevoked
			}
			rec, code, p := rtServe(t, rtInfo(npm), revoked, isRevoked, rtRequest(tt.headers))
			if rec.Code != tt.status || code != tt.code || p.ran {
				t.Errorf("answer %d %s (probe ran %v), want %d %s", rec.Code, code, p.ran, tt.status, tt.code)
			}
		})
	}
}
