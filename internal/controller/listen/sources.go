package listen

import (
	"context"
	"crypto/tls"
	"log/slog"

	"github.com/darkraise/darkpanel/internal/core/ext"
)

// CertSource supplies the web leaf for a handshake; nil, nil falls
// through to the next source and finally to the Darkpanel CA leaf.
type CertSource interface {
	Certificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error)
}

// SourceDeps is what the certificate sources may use. Their internal reads
// run as system:listen.cert with the admin scope.
type SourceDeps struct {
	Dispatch    ext.Dispatcher
	LocalHostID string
	Log         *slog.Logger
}

func certSources(d SourceDeps) []CertSource { return []CertSource{tailscaleCertSource(d)} }

func extraNames(ctx context.Context, d SourceDeps) ([]string, error) { return tailscaleNames(ctx, d) }
