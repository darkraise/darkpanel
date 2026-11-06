// Package server is the DERP server's pure logic (spec 4.3): the unit file, derper's flags, the certificate pin and the firewall rules. It does no I/O.
package server

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/base32"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/firewall/model"
	"github.com/darkraise/darkpanel/internal/modules/tailscale/link"
)

const (
	// ServiceUser is the system user derper runs as.
	ServiceUser = "darkpanel-derp"
	// BinaryPath is the installed derper binary.
	BinaryPath = "/usr/local/lib/darkpanel/derper"
	// BinaryNewPath is where an update stages the new binary.
	BinaryNewPath = BinaryPath + ".new"
	// BinaryPrevPath is where an update keeps the binary it replaces.
	BinaryPrevPath = BinaryPath + ".prev"
	// UnitName is derper's systemd unit.
	UnitName = "darkpanel-derper.service"
	// UnitPath is the unit file.
	UnitPath = "/etc/systemd/system/" + UnitName
	// StateDir is derper's service directory.
	StateDir = "/var/lib/darkpanel/derp"
	// CertSubdir is the certificate directory's name inside StateDir.
	CertSubdir = "certs"
	// CertDir is derper's -certdir.
	CertDir = StateDir + "/" + CertSubdir
	// KeyPath is derper's node key file (-c).
	KeyPath = StateDir + "/derper.key"
	// HTTPPort is derper's plain HTTP port (ACME and the port 80 handler).
	HTTPPort = 80
	// HTTPSPort is derper's DERP and TLS port.
	HTTPSPort = 443
	// STUNPort is derper's STUN port (UDP).
	STUNPort = 3478
	// ModeLetsEncrypt is the mode whose certificate derper obtains from Let's Encrypt for a DNS name.
	ModeLetsEncrypt = "letsencrypt"
	// ModeIP is the mode whose self-signed certificate derper makes for a public IP address.
	ModeIP = "ip"
	// RuleLabel is the firewall label of the DERP port rules.
	RuleLabel = "derp"
	// PinPrefix starts a DERP map node's CertName pin.
	PinPrefix = "sha256-raw:"
)

// Config is one server's configuration.
type Config struct {
	Mode          string // ModeLetsEncrypt | ModeIP
	HostName      string // letsencrypt: a DNS name; ip: the public IP literal in canonical form
	VerifyClients bool
}

const (
	invalidDetail   = "the DERP server configuration is invalid"
	modeMessage     = "mode must be letsencrypt or ip"
	dnsNameMessage  = "the host name must be a lower-case DNS name of at most 253 bytes with at least two labels, not an IP address"
	ipAddrMessage   = "the host name must be a public IP address in canonical form, not private, loopback, link-local, multicast, unspecified, IPv4-mapped IPv6 or a Tailscale address"
	maxDNSNameBytes = 253
	maxLabelBytes   = 63
)

// CheckConfig refuses, as request.invalid_input with a field error: a Mode other than the two (/mode); for
// letsencrypt, a HostName that is not a lower-case DNS name of at most 253 bytes with at least two labels, or is an
// IP literal (/hostName); for ip, a HostName that is not a canonical IP literal, or is not global unicast, or is
// private, loopback, link-local, unspecified, IPv4-mapped IPv6 (::ffff:0:0/96) or a Tailscale address
// (100.64.0.0/10, fd7a:115c:a1e0::/48) (/hostName).
func CheckConfig(c Config) error {
	var field errcode.FieldError
	switch c.Mode {
	case ModeLetsEncrypt:
		if validDNSName(c.HostName) {
			return nil
		}
		field = errcode.FieldError{Path: "/hostName", Message: dnsNameMessage}
	case ModeIP:
		if validPublicIP(c.HostName) {
			return nil
		}
		field = errcode.FieldError{Path: "/hostName", Message: ipAddrMessage}
	default:
		field = errcode.FieldError{Path: "/mode", Message: modeMessage}
	}
	return &errcode.Error{Code: errcode.InvalidInput, Detail: invalidDetail, Fields: []errcode.FieldError{field}}
}

func validDNSName(s string) bool {
	if _, err := netip.ParseAddr(s); err == nil {
		return false
	}
	if len(s) > maxDNSNameBytes {
		return false
	}
	labels := strings.Split(s, ".")
	if len(labels) < 2 {
		return false
	}
	for _, l := range labels {
		if !validLabel(l) {
			return false
		}
	}
	return true
}

func validLabel(l string) bool {
	if len(l) < 1 || len(l) > maxLabelBytes || l[0] == '-' || l[len(l)-1] == '-' {
		return false
	}
	for i := range len(l) {
		b := l[i]
		if (b < 'a' || b > 'z') && (b < '0' || b > '9') && b != '-' {
			return false
		}
	}
	return true
}

func validPublicIP(s string) bool {
	a, err := netip.ParseAddr(s)
	if err != nil {
		return false
	}
	// The class checks see a mapped address as its IPv4 address, but derper and the DERP map would carry the mapped
	// form: an IPv6 node address and certificate file names derper strips to "ffff<IPv4>".
	return a.Zone() == "" && !a.Is4In6() && a.String() == s && a.IsGlobalUnicast() && !a.IsPrivate() && !link.IsTailscaleAddr(a)
}

// Flags is derper's argument vector after the program path, in this order:
// -c KeyPath -a :443 -http-port 80 -stun -stun-port 3478 -hostname <HostName> -certmode <letsencrypt|manual>
// -certdir CertDir, then -verify-clients when VerifyClients. The -certmode is letsencrypt for ModeLetsEncrypt and
// manual for ModeIP; any other Mode has no flags, and Flags returns nil.
func Flags(c Config) []string {
	var certMode string
	switch c.Mode {
	case ModeLetsEncrypt:
		certMode = "letsencrypt"
	case ModeIP:
		certMode = "manual"
	default:
		return nil
	}
	flags := []string{
		"-c", KeyPath,
		"-a", ":" + strconv.Itoa(HTTPSPort),
		"-http-port", strconv.Itoa(HTTPPort),
		"-stun",
		"-stun-port", strconv.Itoa(STUNPort),
		"-hostname", c.HostName,
		"-certmode", certMode,
		"-certdir", CertDir,
	}
	if c.VerifyClients {
		flags = append(flags, "-verify-clients")
	}
	return flags
}

// UnitFile is the unit text (Contracts "DERP unit text"), or nil when Flags has none for c's Mode, so no unit with a
// half-built ExecStart is written.
func UnitFile(c Config) []byte {
	flags := Flags(c)
	if flags == nil {
		return nil
	}
	lines := []string{
		"[Unit]",
		"Description=Darkpanel DERP server",
		"After=network-online.target tailscaled.service",
		"Wants=network-online.target",
		"",
		"[Service]",
		"User=" + ServiceUser,
		"ExecStart=" + BinaryPath + " " + strings.Join(flags, " "),
		"AmbientCapabilities=CAP_NET_BIND_SERVICE",
		"CapabilityBoundingSet=CAP_NET_BIND_SERVICE",
		"NoNewPrivileges=yes",
		"Restart=on-failure",
		"RestartSec=5",
		"",
		"[Install]",
		"WantedBy=multi-user.target",
	}
	return []byte(strings.Join(lines, "\n") + "\n")
}

// CertFiles is the ip mode's certificate and key file names inside CertDir, as derper names them: "<name>.crt",
// "<name>.key", <name> being HostName with every character outside [a-zA-Z0-9-.] removed (an IPv4 literal is
// unchanged; 2001:db8::1 gives 2001db81). HostName itself is not changed. ok is false for letsencrypt (derper's own
// cache, never pinned or renamed).
func CertFiles(c Config) (crt, key string, ok bool) {
	if c.Mode != ModeIP {
		return "", "", false
	}
	// derper strips these characters from -hostname when it names its manual certificate files.
	name := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '.' {
			return r
		}
		return -1
	}, c.HostName)
	return name + ".crt", name + ".key", true
}

// Pin is PinPrefix + the lower-case hex SHA-256 of der.
func Pin(der []byte) string {
	sum := sha256.Sum256(der)
	return PinPrefix + hex.EncodeToString(sum[:])
}

// ParseCert decodes the first CERTIFICATE block of pemBytes: its DER and NotAfter.
func ParseCert(pemBytes []byte) (der []byte, notAfter time.Time, err error) {
	rest := pemBytes
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			return nil, time.Time{}, errors.New("server: no CERTIFICATE block in the PEM data")
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			// The parser's message can quote the certificate's contents, so it is not passed on.
			return nil, time.Time{}, errors.New("server: the CERTIFICATE block is not a valid X.509 certificate")
		}
		return block.Bytes, cert.NotAfter.UTC(), nil
	}
}

var crockford = base32.NewEncoding("0123456789ABCDEFGHJKMNPQRSTVWXYZ").WithPadding(base32.NoPadding)

// RuleID is the firewall rule id of one DERP port of host hostID: "0" + the first 25 characters of the Crockford
// base32 (alphabet 0123456789ABCDEFGHJKMNPQRSTVWXYZ, no padding) of SHA-256("darkpanel/derp/" + hostID + "/" + port),
// port one of "80/tcp", "443/tcp", "3478/udp". It matches the firewall's ULID id pattern.
func RuleID(hostID, port string) string {
	sum := sha256.Sum256([]byte("darkpanel/derp/" + hostID + "/" + port))
	return "0" + crockford.EncodeToString(sum[:])[:25]
}

// PortRules are the three rules derp.server.deploy adds, in port order 80/tcp, 443/tcp, 3478/udp: each
// model.Rule{ID: RuleID(hostID, port), Action: allow, Direction: in, Proto: tcp|udp, From: "any", To: "any",
// ToPort: "80"|"443"|"3478", Family: both}, with no comment (firewall.rules.apply's label adds it).
func PortRules(hostID string) []model.Rule {
	ports := []struct {
		port  int
		proto model.Proto
	}{
		{HTTPPort, model.ProtoTCP},
		{HTTPSPort, model.ProtoTCP},
		{STUNPort, model.ProtoUDP},
	}
	rules := make([]model.Rule, 0, len(ports))
	for _, p := range ports {
		port := strconv.Itoa(p.port)
		rules = append(rules, model.Rule{
			ID:        RuleID(hostID, port+"/"+string(p.proto)),
			Action:    model.ActionAllow,
			Direction: model.DirIn,
			Proto:     p.proto,
			From:      model.Any,
			To:        model.Any,
			ToPort:    port,
			Family:    model.FamilyBoth,
		})
	}
	return rules
}
