package tailscale

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// certGet is tailscale.cert.get: the internal read that returns the node's TLS certificate pair for its
// MagicDNS name, which the controller serves its web listener with.
func (t ts) certGet() core.ActionDef {
	return core.DefineInternalRead[CertGetInput, CertPair](core.ActionSpec{
		Name: ID + ".cert.get", Summary: "Return the TLS certificate pair for the node's MagicDNS name, for the controller's own use.",
		Version: 1, Side: core.SideAgent,
	}, t.certRead)
}

// certRead asks tailscaled for the pair of the node's first certificate domain and checks it before
// returning it.
func (t ts) certRead(ctx context.Context, _ core.Env, _ CertGetInput) (core.Revealed[CertPair], error) {
	local := t.d.local()
	s, err := local.Status(ctx)
	if err != nil {
		return core.Revealed[CertPair]{}, certErr(ctx, err, "tailscaled does not answer on its socket")
	}
	var name string
	if len(s.CertDomains) > 0 {
		name = strings.TrimSuffix(s.CertDomains[0], ".")
	}
	if name == "" {
		return core.Revealed[CertPair]{}, errcode.New(errcode.TailscaleUnavailable,
			"tailscaled offers no certificate domain; enable HTTPS certificates for the tailnet and log the node in")
	}
	certPEM, keyPEM, err := local.CertPair(ctx, name)
	if err != nil {
		return core.Revealed[CertPair]{}, certErr(ctx, err, "tailscaled could not provide a certificate for the node's name")
	}

	const unusable = "tailscaled returned a certificate pair that is not usable"
	// The key-pair and parse errors are dropped, not wrapped: their text can quote the PEM input, the private
	// key included, and the cause of an errcode.Error is logged.
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return core.Revealed[CertPair]{}, certErr(ctx, errors.New("tailscale: the certificate and key do not form a pair"), unusable)
	}
	cert, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return core.Revealed[CertPair]{}, certErr(ctx, errors.New("tailscale: parse the leaf certificate"), unusable)
	}
	if err := cert.VerifyHostname(name); err != nil {
		return core.Revealed[CertPair]{}, certErr(ctx, err, unusable)
	}

	return core.Revealed[CertPair]{
		Identity: "tailscale certificate for " + name,
		Value:    CertPair{Name: name, CertPEM: string(certPEM), KeyPEM: string(keyPEM), NotAfter: cert.NotAfter.UTC()},
	}, nil
}

// certErr is the error of a failed step of certRead: the context's own error when the read was cut off, else
// tailscale.unavailable with detail and the cause kept.
func certErr(ctx context.Context, err error, detail string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return errcode.Wrap(errcode.TailscaleUnavailable, err, detail)
}
