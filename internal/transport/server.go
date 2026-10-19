package transport

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"slices"
)

// PeerCheck decides whether a client certificate that verified against the
// agent root may connect: nil when hostID names an enrolled host and serial
// is one of its unrevoked, unexpired certificates. The transport has no
// database, so the controller supplies the lookup.
type PeerCheck func(ctx context.Context, hostID, serial string) error

// ServerConfig is the controller's side of the agent listener.
type ServerConfig struct {
	// Certificate returns the listener's chain [leaf, root].
	Certificate func(*tls.ClientHelloInfo) (*tls.Certificate, error)
	// AgentRoots holds the agent root alone; system roots never apply.
	AgentRoots *x509.CertPool
	CheckPeer  PeerCheck
}

// Peer is one accepted connection. For ALPNAgent the host id comes from the
// verified certificate and nothing else identifies the peer.
type Peer struct {
	Conn     *tls.Conn
	Protocol string // ALPNAgent or ALPNEnroll
	HostID   string // empty for ALPNEnroll
	Serial   string // SerialText of the client leaf; empty for ALPNEnroll
}

// Handshake runs the controller side of the TLS handshake on raw within ctx
// and closes raw on any failure.
func Handshake(ctx context.Context, raw net.Conn, cfg ServerConfig) (*Peer, error) {
	conn := tls.Server(raw, &tls.Config{
		MinVersion:             tls.VersionTLS13,
		SessionTicketsDisabled: true,
		GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
			return policy(hello, cfg)
		},
	})
	if err := conn.HandshakeContext(ctx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("transport: handshake with %s: %w", raw.RemoteAddr(), err)
	}
	state := conn.ConnectionState()
	peer := &Peer{Conn: conn, Protocol: state.NegotiatedProtocol}
	if peer.Protocol == ALPNAgent {
		leaf := state.PeerCertificates[0]
		// verifyAgent accepted this leaf, so its identity parses.
		peer.HostID, _ = HostIDOf(leaf)
		peer.Serial = SerialText(leaf.SerialNumber)
	}
	return peer, nil
}

// policy picks the configuration for the protocol the client offers. A
// client offering both could be steered to the weaker one, so it is refused.
func policy(hello *tls.ClientHelloInfo, cfg ServerConfig) (*tls.Config, error) {
	agent := slices.Contains(hello.SupportedProtos, ALPNAgent)
	enroll := slices.Contains(hello.SupportedProtos, ALPNEnroll)
	c := &tls.Config{
		MinVersion:     tls.VersionTLS13,
		GetCertificate: cfg.Certificate,
		// A resumed session skips VerifyPeerCertificate, so a revoked
		// certificate could come back on a ticket.
		SessionTicketsDisabled: true,
	}
	switch {
	case agent && enroll:
		return nil, errors.New("transport: the client offers both the agent and the enrollment protocol")
	case agent:
		ctx := hello.Context()
		c.NextProtos = []string{ALPNAgent}
		c.ClientAuth = tls.RequireAnyClientCert
		c.VerifyPeerCertificate = func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			return verifyAgent(ctx, rawCerts, cfg)
		}
	case enroll:
		c.NextProtos = []string{ALPNEnroll}
	default:
		return nil, errors.New("transport: the client offers neither the agent nor the enrollment protocol")
	}
	return c, nil
}

// verifyAgent checks an agent's certificate against the agent root alone.
func verifyAgent(ctx context.Context, rawCerts [][]byte, cfg ServerConfig) error {
	if len(rawCerts) == 0 {
		return errors.New("transport: the agent presented no certificate")
	}
	// Only the leaf counts: the root issues agent certificates itself, so a
	// presented intermediate could only widen what verifies.
	leaf, err := x509.ParseCertificate(rawCerts[0])
	if err != nil {
		return fmt.Errorf("transport: agent certificate: %w", err)
	}
	opts := x509.VerifyOptions{Roots: cfg.AgentRoots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	if _, err := leaf.Verify(opts); err != nil {
		return fmt.Errorf("transport: agent certificate: %w", err)
	}
	// Verify lets a certificate without extended key usages through for any
	// usage.
	if !slices.Contains(leaf.ExtKeyUsage, x509.ExtKeyUsageClientAuth) {
		return errors.New("transport: the agent certificate lacks the clientAuth usage")
	}
	hostID, err := HostIDOf(leaf)
	if err != nil {
		return err
	}
	if err := cfg.CheckPeer(ctx, hostID, SerialText(leaf.SerialNumber)); err != nil {
		return fmt.Errorf("transport: host %s: %w", hostID, err)
	}
	return nil
}
