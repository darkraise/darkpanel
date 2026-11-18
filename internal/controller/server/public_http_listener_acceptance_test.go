package server

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/cli"
	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/controller/listen"
	"github.com/darkraise/darkpanel/internal/core/coretest"
)

// acceptanceHostAddr returns a non-loopback, non-link-local IPv4 address one
// of this host's interfaces holds.
func acceptanceHostAddr(t *testing.T) netip.Addr {
	t.Helper()
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range addrs {
		n, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		ip, ok := netip.AddrFromSlice(n.IP)
		if !ok {
			continue
		}
		if ip = ip.Unmap(); ip.Is4() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() {
			return ip
		}
	}
	t.Skip("this host has no non-loopback IPv4 address")
	return netip.Addr{}
}

// TestA3ServeBindsAPlainHTTPAddressOfTheHost covers the server half of A3: a
// real socket on a non-loopback address of the host answers, signs in with
// the plain session cookie, and loopback keeps answering beside it.
func TestA3ServeBindsAPlainHTTPAddressOfTheHost(t *testing.T) {
	host := acceptanceHostAddr(t)
	wantPublic := netip.AddrPortFrom(host, 8080)
	specs, err := listen.ParseSpecs([]string{"loopback", "http://" + wantPublic.String()})
	if err != nil {
		t.Fatalf("A3: ParseSpecs: %v", err)
	}
	opts := embeddedOptions(t)
	opts.Listen = specs
	// Port 8080 may be taken on the machine running the tests: the endpoint
	// keeps its address and port, and the socket binds the host address on a
	// free port, through the production Bind.
	opts.Bind = func(ctx context.Context, e listen.Endpoint) (net.Listener, error) {
		if e.Addr == wantPublic {
			e.Addr = netip.AddrPortFrom(host, 0)
			return listen.Bind(ctx, e)
		}
		return localBind(ctx, e)
	}
	e := &e2e{t: t, clock: opts.Clock.(*coretest.FakeClock), home: t.TempDir()}
	e.s = start(t, opts)

	var publicURL string
	var loopbackServed bool
	for _, b := range e.s.Listeners() {
		switch {
		case b.Endpoint.Addr == wantPublic:
			if b.Endpoint.TLS || !strings.HasPrefix(b.Addr.String(), host.String()+":") {
				t.Fatalf("A3: public listener %+v is not plain HTTP on %s", b, host)
			}
			publicURL = "http://" + b.Addr.String()
		case b.Endpoint.Addr.Addr().IsLoopback() && b.Endpoint.Addr.Addr().Is4():
			loopbackServed = true
		}
	}
	if publicURL == "" || !loopbackServed {
		t.Fatalf("A3: listeners %+v, want 127.0.0.1:8080 and %s", e.s.Listeners(), wantPublic)
	}

	out, errOut, code := e.cli(nil, adminPassword+"\n", "user", "create", "--username", "admin", "--socket", e.s.SocketPath(), "--yes")
	m := secretLine.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("A3: user create (exit %d) printed no secret:\n%s\n%s", code, out, errOut)
	}
	secret := m[1]
	totp, err := auth.TOTPCode(secret, e.clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	if out, errOut, code := e.cli(nil, totp+"\n", "user", "activate", "--username", "admin", "--socket", e.s.SocketPath(), "--yes"); code != cli.ExitOK {
		t.Fatalf("A3: user activate: exit %d\n%s\n%s", code, out, errOut)
	}

	for _, base := range []string{publicURL, baseURL(t, e.s)} {
		e.url = base
		e.clock.Advance(auth.TOTPPeriod)
		totp, _ = auth.TOTPCode(secret, e.clock.Now())
		r := e.do("POST", "/api/v1/auth/login", map[string]string{"username": "admin", "password": adminPassword, "code": totp}, credentials{})
		if r.status != http.StatusOK {
			t.Fatalf("A3: login on %s: status %d: %s", base, r.status, r.body)
		}
		var cookie *http.Cookie
		for _, c := range (&http.Response{Header: r.header}).Cookies() {
			if strings.Contains(c.Name, "darkpanel_session") {
				cookie = c
			}
		}
		if cookie == nil {
			t.Fatalf("A3: login on %s set no session cookie: %v", base, r.header)
		}
		if cookie.Name != "darkpanel_session" || cookie.Secure {
			t.Errorf("A3: login on %s set cookie %q with Secure=%v, want darkpanel_session without Secure", base, cookie.Name, cookie.Secure)
		}
		if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
			t.Errorf("A3: cookie on %s is not HttpOnly and SameSite=Strict: %+v", base, cookie)
		}
	}
}

// TestA14TheFirewallGuardSeesAPublicPlainListener covers A14.
func TestA14TheFirewallGuardSeesAPublicPlainListener(t *testing.T) {
	public := listen.Bound{
		Endpoint: listen.Endpoint{Addr: netip.MustParseAddrPort("203.0.113.7:8080"), Retry: true},
		Addr:     net.TCPAddrFromAddrPort(netip.MustParseAddrPort("203.0.113.7:8080")),
	}
	loop := listen.Bound{
		Endpoint: listen.Endpoint{Addr: netip.MustParseAddrPort("127.0.0.1:8080")},
		Addr:     net.TCPAddrFromAddrPort(netip.MustParseAddrPort("127.0.0.1:8080")),
	}
	got := topologyOf([]listen.Bound{loop, public}, nil, nil, nil)
	want := map[netip.AddrPort]bool{
		netip.MustParseAddrPort("203.0.113.7:8080"): false,
		netip.MustParseAddrPort("127.0.0.1:8080"):   false,
	}
	for _, ap := range got.PanelListen {
		if _, ok := want[ap]; ok {
			want[ap] = true
		}
	}
	for ap, seen := range want {
		if !seen {
			t.Errorf("A14: PanelListen %v lacks %v", got.PanelListen, ap)
		}
	}
}
