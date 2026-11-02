package server

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/firewall/model"
)

const (
	svHostA = "01JD0000000000000000000011"
	svHostB = "01JD0000000000000000000012"
)

// svPaths is the field paths of a request.invalid_input refusal; it fails the test for any other error.
func svPaths(t *testing.T, err error) []string {
	t.Helper()
	var e *errcode.Error
	if !errors.As(err, &e) || e.Code != errcode.InvalidInput {
		t.Fatalf("error %v, want an *errcode.Error with code request.invalid_input", err)
	}
	var paths []string
	for _, f := range e.Fields {
		paths = append(paths, f.Path)
	}
	return paths
}

func TestSvConstants(t *testing.T) {
	for _, c := range []struct{ name, got, want string }{
		{"ServiceUser", ServiceUser, "darkpanel-derp"},
		{"BinaryPath", BinaryPath, "/usr/local/lib/darkpanel/derper"},
		{"BinaryNewPath", BinaryNewPath, "/usr/local/lib/darkpanel/derper.new"},
		{"BinaryPrevPath", BinaryPrevPath, "/usr/local/lib/darkpanel/derper.prev"},
		{"UnitName", UnitName, "darkpanel-derper.service"},
		{"UnitPath", UnitPath, "/etc/systemd/system/darkpanel-derper.service"},
		{"StateDir", StateDir, "/var/lib/darkpanel/derp"},
		{"CertSubdir", CertSubdir, "certs"},
		{"CertDir", CertDir, "/var/lib/darkpanel/derp/certs"},
		{"KeyPath", KeyPath, "/var/lib/darkpanel/derp/derper.key"},
		{"ModeLetsEncrypt", ModeLetsEncrypt, "letsencrypt"},
		{"ModeIP", ModeIP, "ip"},
		{"RuleLabel", RuleLabel, "derp"},
		{"PinPrefix", PinPrefix, "sha256-raw:"},
	} {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
	if HTTPPort != 80 || HTTPSPort != 443 || STUNPort != 3478 {
		t.Errorf("ports %d, %d, %d, want 80, 443, 3478", HTTPPort, HTTPSPort, STUNPort)
	}
}

func TestSvCheckConfig(t *testing.T) {
	label63 := strings.Repeat("a", 63)
	name253 := label63 + "." + label63 + "." + label63 + "." + strings.Repeat("a", 61)
	tests := []struct {
		name string
		cfg  Config
		want []string // the refused field paths; nil means accepted
	}{
		{"letsencrypt with a DNS name", Config{Mode: ModeLetsEncrypt, HostName: "derp.example.com"}, nil},
		{"letsencrypt with a punycode name and digits", Config{Mode: ModeLetsEncrypt, HostName: "xn--bcher-kva-1.example"}, nil},
		{"letsencrypt with a name of 253 bytes", Config{Mode: ModeLetsEncrypt, HostName: name253}, nil},
		{"letsencrypt, verification off", Config{Mode: ModeLetsEncrypt, HostName: "derp.example.com", VerifyClients: false}, nil},
		{"letsencrypt with a name of 254 bytes", Config{Mode: ModeLetsEncrypt, HostName: name253 + "a"}, []string{"/hostName"}},
		{"letsencrypt with a label of 64 bytes", Config{Mode: ModeLetsEncrypt, HostName: strings.Repeat("a", 64) + ".com"}, []string{"/hostName"}},
		{"letsencrypt with one label", Config{Mode: ModeLetsEncrypt, HostName: "localhost"}, []string{"/hostName"}},
		{"letsencrypt with an upper-case name", Config{Mode: ModeLetsEncrypt, HostName: "Derp.example.com"}, []string{"/hostName"}},
		{"letsencrypt with an empty label", Config{Mode: ModeLetsEncrypt, HostName: "derp..example.com"}, []string{"/hostName"}},
		{"letsencrypt with a trailing dot", Config{Mode: ModeLetsEncrypt, HostName: "derp.example.com."}, []string{"/hostName"}},
		{"letsencrypt with an underscore", Config{Mode: ModeLetsEncrypt, HostName: "derp_1.example.com"}, []string{"/hostName"}},
		{"letsencrypt with a leading hyphen", Config{Mode: ModeLetsEncrypt, HostName: "-derp.example.com"}, []string{"/hostName"}},
		{"letsencrypt with a trailing hyphen in a label", Config{Mode: ModeLetsEncrypt, HostName: "derp-.example.com"}, []string{"/hostName"}},
		{"letsencrypt with an IPv4 literal", Config{Mode: ModeLetsEncrypt, HostName: "203.0.113.7"}, []string{"/hostName"}},
		{"letsencrypt with an IPv6 literal", Config{Mode: ModeLetsEncrypt, HostName: "2001:db8::1"}, []string{"/hostName"}},
		{"letsencrypt with an empty name", Config{Mode: ModeLetsEncrypt}, []string{"/hostName"}},
		{"ip with a public IPv4 address", Config{Mode: ModeIP, HostName: "203.0.113.7"}, nil},
		{"ip with a public IPv6 address", Config{Mode: ModeIP, HostName: "2001:db8::1"}, nil},
		{"ip with a private address", Config{Mode: ModeIP, HostName: "10.0.0.5"}, []string{"/hostName"}},
		{"ip with a private 192.168 address", Config{Mode: ModeIP, HostName: "192.168.1.1"}, []string{"/hostName"}},
		{"ip with a loopback address", Config{Mode: ModeIP, HostName: "127.0.0.1"}, []string{"/hostName"}},
		{"ip with the IPv6 loopback", Config{Mode: ModeIP, HostName: "::1"}, []string{"/hostName"}},
		{"ip with a link-local address", Config{Mode: ModeIP, HostName: "169.254.1.1"}, []string{"/hostName"}},
		{"ip with an IPv6 link-local address", Config{Mode: ModeIP, HostName: "fe80::1"}, []string{"/hostName"}},
		{"ip with the unspecified address", Config{Mode: ModeIP, HostName: "0.0.0.0"}, []string{"/hostName"}},
		{"ip with a multicast address", Config{Mode: ModeIP, HostName: "224.0.0.1"}, []string{"/hostName"}},
		{"ip with a Tailscale IPv4 address", Config{Mode: ModeIP, HostName: "100.64.0.9"}, []string{"/hostName"}},
		{"ip with a Tailscale IPv6 address", Config{Mode: ModeIP, HostName: "fd7a:115c:a1e0::1"}, []string{"/hostName"}},
		{"ip with an IPv4-mapped IPv6 address", Config{Mode: ModeIP, HostName: "::ffff:203.0.113.7"}, []string{"/hostName"}},
		{"ip with an upper-case IPv6 literal",Config{Mode: ModeIP, HostName: "2001:DB8::1"}, []string{"/hostName"}},
		{"ip with an IPv6 literal that is not shortened", Config{Mode: ModeIP, HostName: "2001:db8:0:0:0:0:0:1"}, []string{"/hostName"}},
		{"ip with a prefix length", Config{Mode: ModeIP, HostName: "203.0.113.7/32"}, []string{"/hostName"}},
		{"ip with a space", Config{Mode: ModeIP, HostName: "203.0.113.7 "}, []string{"/hostName"}},
		{"ip with a DNS name", Config{Mode: ModeIP, HostName: "derp.example.com"}, []string{"/hostName"}},
		{"ip with an empty address", Config{Mode: ModeIP}, []string{"/hostName"}},
		{"an unknown mode", Config{Mode: "manual", HostName: "derp.example.com"}, []string{"/mode"}},
		{"an empty mode", Config{HostName: "derp.example.com"}, []string{"/mode"}},
	}
	for _, tt := range tests {
		err := CheckConfig(tt.cfg)
		if tt.want == nil {
			if err != nil {
				t.Errorf("%s: CheckConfig = %v, want nil", tt.name, err)
			}
			continue
		}
		if err == nil {
			t.Errorf("%s: CheckConfig = nil, want a refusal of %v", tt.name, tt.want)
			continue
		}
		if got := svPaths(t, err); !slices.Equal(got, tt.want) {
			t.Errorf("%s: refused fields %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestSvFlags(t *testing.T) {
	common := []string{"-c", "/var/lib/darkpanel/derp/derper.key", "-a", ":443", "-http-port", "80", "-stun", "-stun-port", "3478"}
	tests := []struct {
		name string
		cfg  Config
		want []string
	}{
		{"ip mode verifies clients", Config{Mode: ModeIP, HostName: "203.0.113.7", VerifyClients: true},
			append(slices.Clone(common), "-hostname", "203.0.113.7", "-certmode", "manual", "-certdir", "/var/lib/darkpanel/derp/certs", "-verify-clients")},
		{"letsencrypt without verification", Config{Mode: ModeLetsEncrypt, HostName: "derp.example.com"},
			append(slices.Clone(common), "-hostname", "derp.example.com", "-certmode", "letsencrypt", "-certdir", "/var/lib/darkpanel/derp/certs")},
	}
	for _, tt := range tests {
		if got := Flags(tt.cfg); !slices.Equal(got, tt.want) {
			t.Errorf("%s: Flags =\n%q\nwant\n%q", tt.name, got, tt.want)
		}
	}
}

func TestSvUnitFile(t *testing.T) {
	const head = "[Unit]\n" +
		"Description=Darkpanel DERP server\n" +
		"After=network-online.target tailscaled.service\n" +
		"Wants=network-online.target\n" +
		"\n" +
		"[Service]\n" +
		"User=darkpanel-derp\n" +
		"ExecStart=/usr/local/lib/darkpanel/derper -c /var/lib/darkpanel/derp/derper.key -a :443 -http-port 80 -stun -stun-port 3478 "
	const tail = "\n" +
		"AmbientCapabilities=CAP_NET_BIND_SERVICE\n" +
		"CapabilityBoundingSet=CAP_NET_BIND_SERVICE\n" +
		"NoNewPrivileges=yes\n" +
		"Restart=on-failure\n" +
		"RestartSec=5\n" +
		"\n" +
		"[Install]\n" +
		"WantedBy=multi-user.target\n"
	tests := []struct {
		name string
		cfg  Config
		want string
	}{
		{"ip mode with client verification", Config{Mode: ModeIP, HostName: "203.0.113.7", VerifyClients: true},
			head + "-hostname 203.0.113.7 -certmode manual -certdir /var/lib/darkpanel/derp/certs -verify-clients" + tail},
		{"letsencrypt without client verification", Config{Mode: ModeLetsEncrypt, HostName: "derp.example.com"},
			head + "-hostname derp.example.com -certmode letsencrypt -certdir /var/lib/darkpanel/derp/certs" + tail},
	}
	for _, tt := range tests {
		if got := string(UnitFile(tt.cfg)); got != tt.want {
			t.Errorf("%s: UnitFile =\n%s\nwant\n%s", tt.name, got, tt.want)
		}
	}
}

func TestSvCertFiles(t *testing.T) {
	crt, key, ok := CertFiles(Config{Mode: ModeIP, HostName: "203.0.113.7"})
	if crt != "203.0.113.7.crt" || key != "203.0.113.7.key" || !ok {
		t.Errorf("CertFiles(ip) = %q, %q, %v; want 203.0.113.7.crt, 203.0.113.7.key, true", crt, key, ok)
	}
	crt, key, ok = CertFiles(Config{Mode: ModeIP, HostName: "2001:db8::1"})
	if crt != "2001db81.crt" || key != "2001db81.key" || !ok {
		t.Errorf("CertFiles(ip6) = %q, %q, %v; want derper's names 2001db81.crt, 2001db81.key, true", crt, key, ok)
	}
	crt, key, ok = CertFiles(Config{Mode: ModeIP, HostName: "2001:db8::ab"})
	if crt != "2001db8ab.crt" || key != "2001db8ab.key" || !ok {
		t.Errorf("CertFiles(ip6 with letters) = %q, %q, %v; want 2001db8ab.crt, 2001db8ab.key, true", crt, key, ok)
	}
	crt, key, ok = CertFiles(Config{Mode: ModeLetsEncrypt, HostName: "derp.example.com"})
	if crt != "" || key != "" || ok {
		t.Errorf("CertFiles(letsencrypt) = %q, %q, %v; want empty names and false", crt, key, ok)
	}
}

func TestSvPin(t *testing.T) {
	// SHA-256 of "abc", the FIPS 180 test vector.
	if got, want := Pin([]byte("abc")), "sha256-raw:ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"; got != want {
		t.Errorf("Pin(abc) = %q, want %q", got, want)
	}
	if a, b := Pin([]byte("a")), Pin([]byte("b")); a == b {
		t.Errorf("Pin of different inputs is %q for both", a)
	}
}

func svCert(t *testing.T, notAfter time.Time) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "derpkey"},
		NotBefore:    notAfter.Add(-365 * 24 * time.Hour),
		NotAfter:     notAfter,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return der
}

func TestSvParseCert(t *testing.T) {
	notAfter := time.Date(2027, 10, 20, 9, 0, 0, 0, time.UTC)
	der := svCert(t, notAfter)
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	other := svCert(t, notAfter.Add(time.Hour))
	other = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: other})
	params := pem.EncodeToMemory(&pem.Block{Type: "EC PARAMETERS", Bytes: []byte{1, 2, 3}})

	for name, in := range map[string][]byte{
		"a certificate": certPEM,
		"a certificate after a block of another type": append(slices.Clone(params), certPEM...),
		"the first of two certificates":               append(slices.Clone(certPEM), other...),
	} {
		gotDER, gotAfter, err := ParseCert(in)
		if err != nil {
			t.Errorf("%s: ParseCert: %v", name, err)
			continue
		}
		if !reflect.DeepEqual(gotDER, der) {
			t.Errorf("%s: DER differs from the first certificate's", name)
		}
		if !gotAfter.Equal(notAfter) {
			t.Errorf("%s: NotAfter = %v, want %v", name, gotAfter, notAfter)
		}
	}

	for name, in := range map[string][]byte{
		"empty input":             nil,
		"text":                    []byte("not a pem file"),
		"only another block type": params,
		"a certificate block that is not a certificate": pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("nope")}),
	} {
		_, _, err := ParseCert(in)
		if err == nil {
			t.Errorf("%s: ParseCert = nil, want an error", name)
			continue
		}
		if !strings.HasPrefix(err.Error(), "server:") {
			t.Errorf("%s: error %q does not start with the package name", name, err)
		}
	}
}

func TestSvRuleID(t *testing.T) {
	golden := map[[2]string]string{
		{svHostA, "80/tcp"}:   "052TM0KFCBMRCRHPQNF8ENDQCB",
		{svHostA, "443/tcp"}:  "0WKF7EEXYG37R9V2YAMNJ3FT3E",
		{svHostA, "3478/udp"}: "0PDBWS9M0AGNGD1BH1SB6APK0D",
		{svHostB, "80/tcp"}:   "0G9DBB9VCFZ8CXS7N9QCXBTR36",
		{svHostB, "443/tcp"}:  "0PF3SB818V8X9DV0J1V0HRF6PQ",
		{svHostB, "3478/udp"}: "0XY1KXY2CSQ55CBR3SKJ1QPDBT",
	}
	ulid := regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{26}$`)
	seen := map[string]bool{}
	for k, want := range golden {
		got := RuleID(k[0], k[1])
		if got != want {
			t.Errorf("RuleID(%s, %s) = %s, want %s", k[0], k[1], got, want)
		}
		if !ulid.MatchString(got) {
			t.Errorf("RuleID(%s, %s) = %s does not match the firewall's ULID pattern", k[0], k[1], got)
		}
		seen[got] = true
	}
	if len(seen) != len(golden) {
		t.Errorf("%d distinct ids for %d host and port pairs", len(seen), len(golden))
	}
}

func TestSvPortRules(t *testing.T) {
	rule := func(id string, proto model.Proto, port string) model.Rule {
		return model.Rule{
			ID: id, Action: model.ActionAllow, Direction: model.DirIn, Proto: proto,
			From: model.Any, To: model.Any, ToPort: port, Family: model.FamilyBoth,
		}
	}
	want := []model.Rule{
		rule("052TM0KFCBMRCRHPQNF8ENDQCB", model.ProtoTCP, "80"),
		rule("0WKF7EEXYG37R9V2YAMNJ3FT3E", model.ProtoTCP, "443"),
		rule("0PDBWS9M0AGNGD1BH1SB6APK0D", model.ProtoUDP, "3478"),
	}
	got := PortRules(svHostA)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("PortRules =\n%+v\nwant\n%+v", got, want)
	}
	for _, r := range got {
		if err := r.Validate(); err != nil {
			t.Errorf("rule %s is outside the firewall grammar: %v", r.ID, err)
		}
	}
}
