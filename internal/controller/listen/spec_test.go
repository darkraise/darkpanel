package listen

import (
	"net/netip"
	"reflect"
	"strings"
	"testing"
)

func TestParseSpecsDefaults(t *testing.T) {
	got, err := ParseSpecs(nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []Spec{{Kind: KindLoopback}, {Kind: KindTailscale}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseSpecs(nil) = %v, want %v", got, want)
	}
}

func TestParseSpecsValues(t *testing.T) {
	got, err := ParseSpecs([]string{
		"tailscale",
		"https://192.168.1.5:9443",
		"https://[2001:db8::5]:443",
		"http://127.0.0.2:8081",
		"http://[::1]:9090",
		"https://[::ffff:10.0.0.1]:8443",
		"tailscale",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []Spec{
		{Kind: KindTailscale},
		{Kind: KindHTTPS, Addr: netip.MustParseAddrPort("192.168.1.5:9443")},
		{Kind: KindHTTPS, Addr: netip.MustParseAddrPort("[2001:db8::5]:443")},
		{Kind: KindHTTP, Addr: netip.MustParseAddrPort("127.0.0.2:8081")},
		{Kind: KindHTTP, Addr: netip.MustParseAddrPort("[::1]:9090")},
		{Kind: KindHTTPS, Addr: netip.MustParseAddrPort("10.0.0.1:8443")},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseSpecs = %v, want %v", got, want)
	}
	var s []string
	for _, sp := range got {
		s = append(s, sp.String())
	}
	if joined := strings.Join(s, " "); joined != "tailscale https://192.168.1.5:9443 https://[2001:db8::5]:443 http://127.0.0.2:8081 http://[::1]:9090 https://10.0.0.1:8443" {
		t.Fatalf("String() = %q", joined)
	}
}

func TestParseSpecsRefuses(t *testing.T) {
	cases := []struct {
		value, msg string
	}{
		{"http://192.168.1.5:8080", "plain HTTP is allowed only on a loopback address"},
		{"http://[2001:db8::1]:8080", "plain HTTP is allowed only on a loopback address"},
		{"http://localhost:8080", "is not loopback, tailscale, https://<ip>:<port> or http://<loopback ip>:<port>"},
		{"https://example.com:443", "is not loopback, tailscale, https://<ip>:<port> or http://<loopback ip>:<port>"},
		{"https://192.168.1.5", "is not loopback, tailscale, https://<ip>:<port> or http://<loopback ip>:<port>"},
		{"https://192.168.1.5:8443/", "is not loopback, tailscale, https://<ip>:<port> or http://<loopback ip>:<port>"},
		{"tcp://127.0.0.1:80", "is not loopback, tailscale, https://<ip>:<port> or http://<loopback ip>:<port>"},
		{"", "is not loopback, tailscale, https://<ip>:<port> or http://<loopback ip>:<port>"},
		{"https://192.168.1.5:0", "needs a port between 1 and 65535"},
		{"https://0.0.0.0:8443", "must be a specific address"},
		{"https://[::]:8443", "must be a specific address"},
		{"https://224.0.0.1:8443", "must be a specific address"},
		{"https://[fe80::1%eth0]:8443", "must not carry a zone"},
	}
	for _, c := range cases {
		_, err := ParseSpecs([]string{c.value})
		if err == nil || !strings.Contains(err.Error(), c.msg) {
			t.Errorf("ParseSpecs(%q) error = %v, want it to contain %q", c.value, err, c.msg)
		}
	}
}

func TestParseSpecsRefusesSchemeConflict(t *testing.T) {
	for _, values := range [][]string{
		{"loopback", "https://127.0.0.1:8080"},
		{"http://127.0.0.1:9000", "https://127.0.0.1:9000"},
	} {
		_, err := ParseSpecs(values)
		if err == nil || !strings.Contains(err.Error(), "is listed as both HTTP and HTTPS") {
			t.Errorf("ParseSpecs(%q) error = %v, want a scheme conflict", values, err)
		}
	}
}

func TestEndpoints(t *testing.T) {
	specs := []Spec{
		{Kind: KindTailscale},
		{Kind: KindLoopback},
		{Kind: KindHTTPS, Addr: netip.MustParseAddrPort("100.64.0.7:8443")},
		{Kind: KindHTTPS, Addr: netip.MustParseAddrPort("192.168.1.5:9443")},
		{Kind: KindHTTP, Addr: netip.MustParseAddrPort("127.0.0.1:8080")},
	}
	ts := []netip.Addr{netip.MustParseAddr("fd7a:115c:a1e0::7"), netip.MustParseAddr("100.64.0.7")}
	got := Endpoints(specs, ts)
	want := []Endpoint{
		{Addr: netip.MustParseAddrPort("100.64.0.7:8443"), TLS: true, FreeBind: true},
		{Addr: netip.MustParseAddrPort("127.0.0.1:8080")},
		{Addr: netip.MustParseAddrPort("192.168.1.5:9443"), TLS: true},
		{Addr: netip.MustParseAddrPort("[::1]:8080"), Optional: true},
		{Addr: netip.MustParseAddrPort("[fd7a:115c:a1e0::7]:8443"), TLS: true, FreeBind: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Endpoints = %v,\nwant %v", got, want)
	}
	if u := got[4].URL(); u != "https://[fd7a:115c:a1e0::7]:8443" {
		t.Fatalf("URL() = %q", u)
	}
	if u := got[1].URL(); u != "http://127.0.0.1:8080" {
		t.Fatalf("URL() = %q", u)
	}
}

func TestEndpointsWithoutTailscaleAddresses(t *testing.T) {
	got := Endpoints([]Spec{{Kind: KindLoopback}, {Kind: KindTailscale}}, nil)
	want := []Endpoint{
		{Addr: netip.MustParseAddrPort("127.0.0.1:8080")},
		{Addr: netip.MustParseAddrPort("[::1]:8080"), Optional: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Endpoints = %v, want %v", got, want)
	}
	if HasTailscale([]Spec{{Kind: KindLoopback}}) || !HasTailscale([]Spec{{Kind: KindTailscale}}) {
		t.Fatal("HasTailscale is wrong")
	}
}
