package transport

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"slices"
)

// AgentConfig is the agent's ALPNAgent configuration: cert is its client
// certificate, and the controller's leaf must verify against ca alone with
// EKU serverAuth and carry the URI SAN controllerSAN.
func AgentConfig(cert tls.Certificate, ca *x509.Certificate, controllerSAN string) *tls.Config {
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	return &tls.Config{
		MinVersion: tls.VersionTLS13,
		NextProtos: []string{ALPNAgent},
		GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			return &cert, nil
		},
		// Go's hostname check cannot match a URI SAN, so VerifyConnection
		// does the whole check, chain included.
		InsecureSkipVerify: true, //nolint:gosec // G402: VerifyConnection verifies the chain and the pinned SAN
		VerifyConnection: func(cs tls.ConnectionState) error {
			return verifyController(cs, ALPNAgent, roots, controllerSAN)
		},
	}
}

// EnrollConfig is the ALPNEnroll configuration: no client certificate; the
// last certificate the controller presents must have the SHA-256
// fingerprint the enrollment token names, and the leaf must verify against
// that certificate alone with EKU serverAuth and carry the URI SAN
// controllerSAN. Nothing is sent before these checks pass.
func EnrollConfig(fingerprint, controllerSAN string) *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS13,
		NextProtos: []string{ALPNEnroll},
		// As AgentConfig: VerifyConnection does the whole check.
		InsecureSkipVerify: true, //nolint:gosec // G402: VerifyConnection pins the root and verifies the chain and SAN
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("transport: the controller presented no certificate")
			}
			root := cs.PeerCertificates[len(cs.PeerCertificates)-1]
			if Fingerprint(root) != fingerprint {
				return errors.New("transport: the controller's root is not the one the enrollment token names")
			}
			roots := x509.NewCertPool()
			roots.AddCert(root)
			return verifyController(cs, ALPNEnroll, roots, controllerSAN)
		},
	}
}

// verifyController checks the controller's leaf against roots alone.
func verifyController(cs tls.ConnectionState, protocol string, roots *x509.CertPool, san string) error {
	if cs.NegotiatedProtocol != protocol {
		return fmt.Errorf("transport: the controller negotiated %q, want %q", cs.NegotiatedProtocol, protocol)
	}
	if len(cs.PeerCertificates) == 0 {
		return errors.New("transport: the controller presented no certificate")
	}
	leaf := cs.PeerCertificates[0]
	opts := x509.VerifyOptions{Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	if _, err := leaf.Verify(opts); err != nil {
		return fmt.Errorf("transport: controller certificate: %w", err)
	}
	// Verify lets a certificate without extended key usages through for any
	// usage.
	if !slices.Contains(leaf.ExtKeyUsage, x509.ExtKeyUsageServerAuth) {
		return errors.New("transport: the controller certificate lacks the serverAuth usage")
	}
	if len(leaf.URIs) != 1 || leaf.URIs[0].String() != san {
		return fmt.Errorf("transport: the controller certificate does not name %s alone", san)
	}
	return nil
}

// Dial connects to address over TCP and completes the TLS handshake with
// cfg within ctx.
func Dial(ctx context.Context, address string, cfg *tls.Config) (*tls.Conn, error) {
	d := tls.Dialer{Config: cfg}
	conn, err := d.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, fmt.Errorf("transport: dial %s: %w", address, err)
	}
	return conn.(*tls.Conn), nil
}
