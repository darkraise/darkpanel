package tslocal

import (
	"errors"
	"io"
	"net/http"
	"net/netip"
	"reflect"
	"strings"
	"testing"
)

func TestDERPMap(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /localapi/v0/derpmap", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"Regions":{"1":{"RegionID":1,"RegionCode":"nyc","RegionName":"New York City","Nodes":[`+
			`{"Name":"1a","RegionID":1,"HostName":"derp1.tailscale.com","IPv4":"192.0.2.1","IPv6":"2001:db8::1"}]},`+
			`"900":{"RegionID":900,"RegionCode":"dp","RegionName":"Darkpanel","Nodes":[{"Name":"900a","RegionID":900,`+
			`"HostName":"203.0.113.7","CertName":"sha256-raw:abcd","IPv4":"203.0.113.7","STUNPort":3478,"DERPPort":443}]}},`+
			`"omitDefaultRegions":true}`)
	})
	m, err := newFakeTailscaled(t, mux).DERPMap(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	dp := m.Regions[900]
	if !m.OmitDefaultRegions || m.Regions[1].RegionCode != "nyc" || dp == nil || dp.Nodes[0].CertName != "sha256-raw:abcd" || dp.Nodes[0].STUNPort != 3478 {
		t.Fatalf("map = %+v", m)
	}
}

func TestDERPMapOfLoggedOutNode(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /localapi/v0/derpmap", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "null\n")
	})
	m, err := newFakeTailscaled(t, mux).DERPMap(t.Context())
	if err != nil || m == nil || m.Regions != nil {
		t.Fatalf("map = %+v, %v", m, err)
	}
}

func TestCertPair(t *testing.T) {
	const key = "-----BEGIN EC PRIVATE KEY-----\nAAAA\n-----END EC PRIVATE KEY-----\n" //nolint:gosec // G101: a placeholder PEM, not a key
	const chain = "-----BEGIN CERTIFICATE-----\nBBBB\n-----END CERTIFICATE-----\n-----BEGIN CERTIFICATE-----\nCCCC\n-----END CERTIFICATE-----\n"
	mux := http.NewServeMux()
	var path, query string
	mux.HandleFunc("GET /localapi/v0/cert/{domain}", func(w http.ResponseWriter, r *http.Request) {
		path, query = r.URL.Path, r.URL.RawQuery
		switch r.PathValue("domain") {
		case "vps.tail1234.ts.net":
			_, _ = io.WriteString(w, key+chain)
		case "broken.ts.net":
			_, _ = io.WriteString(w, chain)
		default:
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, "cert access denied\n")
		}
	})
	c := newFakeTailscaled(t, mux)
	cert, k, err := c.CertPair(t.Context(), "vps.tail1234.ts.net")
	if err != nil {
		t.Fatal(err)
	}
	if string(k) != key || string(cert) != chain || path != "/localapi/v0/cert/vps.tail1234.ts.net" || query != "type=pair" {
		t.Fatalf("key %q cert %q (path %s?%s)", k, cert, path, query)
	}
	grown := append(k, "appended"...) //nolint:gocritic // appendAssign: the append is the behaviour under test
	if string(cert) != chain || !strings.HasSuffix(string(grown), "appended") {
		t.Fatalf("appending to the key corrupted the certificate: %q", cert)
	}
	if _, _, err := c.CertPair(t.Context(), "broken.ts.net"); err == nil {
		t.Fatal("a chain without a key block was accepted as a pair")
	}
	var apiErr *APIError
	if _, _, err := c.CertPair(t.Context(), "other.ts.net"); !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusForbidden {
		t.Fatalf("denied err = %v", err)
	}
	for _, bad := range []string{"", "a/b", "../x", "a..b", "-a.net", "a.net?x=1", strings.Repeat("a", 64) + ".net"} {
		if _, _, err := c.CertPair(t.Context(), bad); err == nil || !strings.Contains(err.Error(), "invalid certificate domain") {
			t.Errorf("CertPair(%q) err = %v", bad, err)
		}
	}
}

func TestCertPairWithoutKey(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /localapi/v0/cert/{domain}", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "-----BEGIN CERTIFICATE-----\nBBBB\n-----END CERTIFICATE-----\n")
	})
	if _, _, err := newFakeTailscaled(t, mux).CertPair(t.Context(), "vps.ts.net"); err == nil || !strings.Contains(err.Error(), "no delimiter") {
		t.Fatalf("err = %v", err)
	}
}

func TestWhoIs(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /localapi/v0/whois", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("addr") != "100.64.0.2" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, "no match for IP:port\n")
			return
		}
		_, _ = io.WriteString(w, `{"Node":{"ID":42,"StableID":"nA","Name":"laptop.tail1234.ts.net.","Addresses":["100.64.0.2/32"],`+
			`"Tags":["tag:dev"]},"UserProfile":{"ID":7,"LoginName":"owner@example.com","DisplayName":"Owner"},"CapMap":{}}`)
	})
	c := newFakeTailscaled(t, mux)
	w, err := c.WhoIs(t.Context(), netip.MustParseAddr("100.64.0.2"))
	if err != nil {
		t.Fatal(err)
	}
	if w.Node.StableID != "nA" || w.Node.Addresses[0] != netip.MustParsePrefix("100.64.0.2/32") || w.UserProfile.LoginName != "owner@example.com" {
		t.Fatalf("whois = %+v %+v", w.Node, w.UserProfile)
	}
	if _, err := c.WhoIs(t.Context(), netip.MustParseAddr("100.64.0.9")); !errors.Is(err, ErrPeerNotFound) {
		t.Fatalf("unknown address err = %v", err)
	}
}

func TestDebugDERPRegion(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /localapi/v0/debug-derp-region", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("region") != "900" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, "no such region\n")
			return
		}
		_, _ = io.WriteString(w, `{"Info":["Region 900 == \"dp\""],"Warnings":["no IPv6"],"Errors":null}`)
	})
	c := newFakeTailscaled(t, mux)
	r, err := c.DebugDERPRegion(t.Context(), "900")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r, &DERPRegionReport{Info: []string{`Region 900 == "dp"`}, Warnings: []string{"no IPv6"}}) {
		t.Fatalf("report = %+v", r)
	}
	var apiErr *APIError
	if _, err := c.DebugDERPRegion(t.Context(), "x&y"); !errors.As(err, &apiErr) || apiErr.Message != "no such region" {
		t.Fatalf("bad region err = %v", err)
	}
}
