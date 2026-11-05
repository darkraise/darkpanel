package cloudflare

import (
	"net/netip"
	"testing"
)

// fz8Head returns at most the first 64 bytes of s, for a failure message.
func fz8Head(s string) string { return s[:min(len(s), 64)] }

// FuzzServiceHost feeds an arbitrary tunnel service URL to serviceHost and
// serviceProblem, and the URL, and the host read from it, to legacyIPv4: none
// panics; a literal host has a valid address that is not IPv4-mapped; a
// service that serviceProblem accepts has no host in a legacy numeric form,
// so every literal host of it is one netip.ParseAddr reads; and a canonical
// dotted quad gives the same address through legacyIPv4 as through
// netip.ParseAddr (R74).
func FuzzServiceHost(f *testing.F) {
	for _, s := range []string{
		"http://127.0.0.1:80", "http://0x7f.1:80", "https://[::ffff:172.31.255.1]:443", "tcp://example.com:22",
		"http_status:404", "http://172.31.255.1.:80", "ssh://010.0.0.1:22", "http://2887778049:80", "",
		"http://:80", "://", "http://user@a:1/x?y#z", "http://[fe80::1%25eth0]:80", "1.2.3.4", "http://1.2.3.4:8080",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, service string) {
		host, addr, literal := serviceHost(service)
		switch {
		case literal && (host == "" || !addr.IsValid() || addr.Is4In6()):
			t.Fatalf("serviceHost(%q) = %q, %v, literal", fz8Head(service), fz8Head(host), addr)
		case !literal && addr.IsValid():
			t.Fatalf("serviceHost(%q) = %q, %v, not literal", fz8Head(service), fz8Head(host), addr)
		}
		if serviceProblem(service) == "" && literal {
			if _, err := netip.ParseAddr(host); err != nil {
				t.Fatalf("serviceProblem accepts %q, whose host %q is a legacy numeric form", fz8Head(service), fz8Head(host))
			}
		}
		for _, h := range []string{host, service} {
			want, err := netip.ParseAddr(h)
			if err != nil || !want.Is4() {
				continue
			}
			if got, ok := legacyIPv4(h); !ok || got != want {
				t.Fatalf("legacyIPv4(%q) = %v, %v; netip.ParseAddr reads %v", fz8Head(h), got, ok, want)
			}
		}
	})
}
