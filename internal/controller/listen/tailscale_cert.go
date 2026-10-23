package listen

import (
	"context"
	"crypto/tls"
)

// tailscaleCertSource serves the host's Tailscale certificate for its
// MagicDNS name. Until the tailscale module provides one it falls through to
// the Darkpanel CA leaf.
func tailscaleCertSource(SourceDeps) CertSource { return fallThrough{} }

// tailscaleNames are the MagicDNS names the CA leaf also carries; none until
// the tailscale module provides them.
func tailscaleNames(context.Context, SourceDeps) ([]string, error) { return nil, nil }

// fallThrough is a source that never supplies a certificate.
type fallThrough struct{}

func (fallThrough) Certificate(*tls.ClientHelloInfo) (*tls.Certificate, error) { return nil, nil }
