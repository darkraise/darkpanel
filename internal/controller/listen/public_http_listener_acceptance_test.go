package listen

import (
	"net/netip"
	"strings"
	"syscall"
	"testing"
)

func acceptanceServe(t *testing.T, values ...string) []Endpoint {
	t.Helper()
	specs, err := ParseSpecs(values)
	if err != nil {
		t.Fatalf("ParseSpecs(%q): %v", values, err)
	}
	return Endpoints(specs, nil)
}

// TestA3ParseAcceptsAPlainHTTPAddressOfTheHost covers the listen half of A3.
func TestA3ParseAcceptsAPlainHTTPAddressOfTheHost(t *testing.T) {
	eps := acceptanceServe(t, "loopback", "http://203.0.113.7:8080")
	var public, loopback *Endpoint
	for i := range eps {
		switch eps[i].Addr.String() {
		case "203.0.113.7:8080":
			public = &eps[i]
		case "127.0.0.1:8080":
			loopback = &eps[i]
		}
	}
	if public == nil || public.TLS || public.URL() != "http://203.0.113.7:8080" {
		t.Errorf("A3: endpoints %+v lack the plain HTTP endpoint 203.0.113.7:8080", eps)
	}
	if loopback == nil || loopback.TLS {
		t.Errorf("A3: endpoints %+v lack the plain loopback endpoint 127.0.0.1:8080", eps)
	}
}

// TestA12StatusMessageNamesAPlainPublicListener covers A12.
func TestA12StatusMessageNamesAPlainPublicListener(t *testing.T) {
	const suffix = "Plain HTTP, not encrypted: http://203.0.113.7:8080."
	t.Run("A12: a bound non-loopback plain endpoint", func(t *testing.T) {
		f := newServiceFixture(t)
		s, err := f.start(t, "loopback", "http://203.0.113.7:8080")
		if err != nil {
			t.Fatal(err)
		}
		if msg := s.State().Message; !strings.HasSuffix(msg, suffix) {
			t.Errorf("A12: message %q does not end with %q", msg, suffix)
		}
	})
	t.Run("A12: with tailscale listed too", func(t *testing.T) {
		f := newServiceFixture(t)
		f.ifs.set(true, "100.64.0.2")
		s, err := f.start(t, "loopback", "tailscale", "http://203.0.113.7:8080")
		if err != nil {
			t.Fatal(err)
		}
		if msg := s.State().Message; !strings.HasPrefix(msg, "Serving on Tailscale: https://100.64.0.2:8443.") || !strings.HasSuffix(msg, suffix) {
			t.Errorf("A12: message %q, want the Tailscale sentence then %q", msg, suffix)
		}
	})
	t.Run("A12: none gives today's message", func(t *testing.T) {
		for _, tt := range []struct {
			values []string
			want   string
		}{
			{[]string{"loopback"}, "Tailscale listener disabled by --listen."},
			{[]string{"loopback", "tailscale"}, "No Tailscale address found; serving loopback only."},
			{[]string{"loopback", "http://127.0.0.2:8080"}, "Tailscale listener disabled by --listen."},
		} {
			f := newServiceFixture(t)
			s, err := f.start(t, tt.values...)
			if err != nil {
				t.Fatal(err)
			}
			if msg := s.State().Message; msg != tt.want {
				t.Errorf("A12: values %v: message %q, want %q", tt.values, msg, tt.want)
			}
		}
	})
}

// TestA16AnUnboundPublicListenerIsRetried covers A16.
func TestA16AnUnboundPublicListenerIsRetried(t *testing.T) {
	missing := netip.MustParseAddrPort("203.0.113.7:8080")
	f := newServiceFixture(t)
	f.binder.fail = map[netip.AddrPort]error{missing: syscall.EADDRNOTAVAIL}
	s, err := f.start(t, "loopback", "http://203.0.113.7:8080")
	if err != nil {
		t.Fatalf("A16: Start failed with an address the host lacks: %v", err)
	}
	st := s.State()
	var loopbackAddr string
	for _, b := range st.Listeners {
		if b.Endpoint.Addr.String() == "127.0.0.1:8080" {
			loopbackAddr = b.Addr.String()
		}
		if b.Endpoint.Addr == missing {
			t.Errorf("A16: the missing address is reported as bound: %+v", b)
		}
	}
	if loopbackAddr == "" {
		t.Fatalf("A16: listeners %+v lack 127.0.0.1:8080", st.Listeners)
	}
	if got := get(t, rootClient(t, f.auth), "http://"+loopbackAddr+"/"); got != "tls=false" {
		t.Errorf("A16: the loopback listener answered %q", got)
	}
	if !strings.Contains(f.log.String(), "http://203.0.113.7:8080") {
		t.Errorf("A16: the log does not name the unbound URL:\n%s", f.log.String())
	}
	const unbound = "Not bound: http://203.0.113.7:8080; see the serve log."
	if !strings.HasSuffix(st.Message, unbound) {
		t.Errorf("A16: message %q does not end with %q", st.Message, unbound)
	}

	f.binder.mu.Lock()
	delete(f.binder.fail, missing)
	f.binder.mu.Unlock()
	f.clock.Advance(CheckInterval)
	st = s.State()
	var bound bool
	for _, b := range st.Listeners {
		bound = bound || b.Endpoint.Addr == missing
	}
	if !bound {
		t.Errorf("A16: the next check did not bind %v; listeners %+v", missing, st.Listeners)
	}
	if strings.Contains(st.Message, "Not bound") || !strings.HasSuffix(st.Message, "Plain HTTP, not encrypted: http://203.0.113.7:8080.") {
		t.Errorf("A16: message after the bind %q", st.Message)
	}
}

// TestA21EndpointsKeepTLSForATailscaleAddressListedPlain covers A21.
func TestA21EndpointsKeepTLSForATailscaleAddressListedPlain(t *testing.T) {
	ts := netip.MustParseAddr("100.64.0.5")
	for _, order := range [][]string{
		{"tailscale", "http://100.64.0.5:8443"},
		{"http://100.64.0.5:8443", "tailscale"},
	} {
		specs, err := ParseSpecs(order)
		if err != nil {
			t.Fatalf("A21: ParseSpecs(%q): %v", order, err)
		}
		eps := Endpoints(specs, []netip.Addr{ts})
		if len(eps) != 1 || eps[0].Addr != netip.MustParseAddrPort("100.64.0.5:8443") || !eps[0].TLS {
			t.Errorf("A21: Endpoints over %q = %+v, want one endpoint 100.64.0.5:8443 with TLS", order, eps)
		}
	}
}

// TestA22EndpointsMarkRetry covers A22.
func TestA22EndpointsMarkRetry(t *testing.T) {
	ts := []netip.Addr{netip.MustParseAddr("100.64.0.5")}
	tests := []struct {
		value string
		addr  string
		retry bool
	}{
		{"http://203.0.113.7:8080", "203.0.113.7:8080", true},
		{"https://203.0.113.7:9443", "203.0.113.7:9443", true},
		{"http://[2001:db8::7]:8080", "[2001:db8::7]:8080", true},
		{"http://127.0.0.1:9090", "127.0.0.1:9090", false},
		{"https://127.0.0.1:9443", "127.0.0.1:9443", false},
		{"http://[::1]:9090", "[::1]:9090", false},
		{"loopback", "127.0.0.1:8080", false},
		{"loopback", "[::1]:8080", false},
		{"tailscale", "100.64.0.5:8443", false},
	}
	for _, tt := range tests {
		specs, err := ParseSpecs([]string{tt.value})
		if err != nil {
			t.Fatalf("A22: ParseSpecs(%q): %v", tt.value, err)
		}
		var found bool
		for _, e := range Endpoints(specs, ts) {
			if e.Addr.String() != tt.addr {
				continue
			}
			found = true
			if e.Retry != tt.retry {
				t.Errorf("A22: %s at %s: Retry = %v, want %v", tt.value, tt.addr, e.Retry, tt.retry)
			}
		}
		if !found {
			t.Errorf("A22: %s yields no endpoint at %s", tt.value, tt.addr)
		}
	}
}
