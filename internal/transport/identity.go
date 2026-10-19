// Package transport is the connection between an agent and its controller
// below the agent API (ADR 0003): the identities both ends check, the
// enrollment token, the TLS policies, the yamux session and the heartbeat.
// It knows nothing about actions or databases; the controller passes the
// lookups it needs as callbacks.
package transport

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"
)

// Application protocols of the agent listener. The controller picks its TLS
// policy by the one the client offers.
const (
	ALPNAgent  = "darkpanel-agent/1"
	ALPNEnroll = "darkpanel-enroll/1"
)

// Routes outside the agent API.
const (
	// EnrollPath is posted to over an ALPNEnroll connection.
	EnrollPath = "/enroll/v1"
	// RenewPath is posted to over a stream the agent opens on its session.
	RenewPath = "/controller/v1/renew"
)

const (
	hostURIPrefix       = "darkpanel://host/"
	controllerURIPrefix = "darkpanel://controller/"
)

// idPattern is a host or controller id as a URI SAN carries it: ULIDs in
// production, short ids in tests, never anything a URI would escape.
var idPattern = regexp.MustCompile(`^[0-9A-Za-z]{1,64}$`)

// HostURI is the URI SAN of an agent certificate.
func HostURI(hostID string) string { return hostURIPrefix + hostID }

// ControllerURI is the URI SAN of the controller's agent-listener
// certificate, which agents pin.
func ControllerURI(controllerID string) string { return controllerURIPrefix + controllerID }

// HostIDOf returns the host id an agent certificate names. The certificate
// must carry exactly one URI SAN, HostURI of an id of 1 to 64 letters and
// digits, and no DNS, IP or e-mail SAN, so nothing but the host id can
// identify the peer.
func HostIDOf(cert *x509.Certificate) (string, error) {
	if len(cert.DNSNames) > 0 || len(cert.IPAddresses) > 0 || len(cert.EmailAddresses) > 0 {
		return "", errors.New("transport: an agent certificate carries DNS, IP or e-mail names")
	}
	if len(cert.URIs) != 1 {
		return "", fmt.Errorf("transport: an agent certificate carries %d URI names, want 1", len(cert.URIs))
	}
	uri := cert.URIs[0].String()
	id, ok := strings.CutPrefix(uri, hostURIPrefix)
	// Comparing the whole text rejects a port, user info, query, fragment or
	// escaping that a field-by-field check could let through.
	if !ok || !idPattern.MatchString(id) || uri != HostURI(id) {
		return "", fmt.Errorf("transport: %q is not a host identity", uri)
	}
	return id, nil
}

// SerialText is a certificate serial as core_agent_certs stores it:
// lower-case hexadecimal without padding.
func SerialText(serial *big.Int) string { return serial.Text(16) }

// Fingerprint is the lower-case hex SHA-256 of a certificate's DER encoding.
func Fingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(sum[:])
}
