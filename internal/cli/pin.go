package cli

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// requestTimeout bounds one API request, so a hung server cannot hang the
// CLI; it exceeds the server's 60-second write timeout.
const requestTimeout = 90 * time.Second

// newHTTPClient returns the client for the server at base. Plain HTTP and
// HTTPS without a fingerprint use Go's defaults, so HTTPS verifies against
// the system roots. With a fingerprint, HTTPS trusts only the presented CA
// certificate whose SHA-256 is that fingerprint (spec section 15). Neither
// follows a redirect.
func newHTTPClient(base *url.URL, fingerprint string) (*http.Client, error) {
	if fingerprint != "" {
		if b, err := hex.DecodeString(fingerprint); err != nil || len(b) != sha256.Size {
			return nil, errors.New("caFingerprint must be 64 hexadecimal digits, the SHA-256 that darkpanel controller web-ca prints")
		}
	}
	if base.Scheme != "https" || fingerprint == "" {
		return &http.Client{Timeout: requestTimeout, CheckRedirect: refuseRedirect}, nil
	}
	tr, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, errors.New("http.DefaultTransport is not an *http.Transport")
	}
	tr = tr.Clone()
	tr.TLSClientConfig = pinnedTLS(strings.ToLower(fingerprint), base.Hostname())
	return &http.Client{Transport: tr, Timeout: requestTimeout, CheckRedirect: refuseRedirect}, nil
}

// refuseRedirect stops the client at a redirect: the pinned TLS check is
// bound to the configured host, and a redirect could carry the token to
// another host or downgrade it to plain HTTP.
func refuseRedirect(req *http.Request, _ []*http.Request) error {
	return fmt.Errorf("the server redirected to %s; darkpanel does not follow redirects, so set the server's own URL", req.URL.Redacted())
}

// pinnedTLS replaces Go's verification, which would require a system root,
// with one rooted at the pinned CA: the leaf must chain to the presented
// certificate whose SHA-256 is fingerprint, that certificate must be a CA,
// and the leaf must be valid for host. Leaf renewals need no new pin. The
// check runs in VerifyConnection rather than VerifyPeerCertificate because
// only VerifyConnection also runs on resumed sessions.
func pinnedTLS(fingerprint, host string) *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		//nolint:gosec // G402: VerifyConnection below verifies the chain against the pinned root and the host
		InsecureSkipVerify: true,
		VerifyConnection: func(cs tls.ConnectionState) error {
			certs := cs.PeerCertificates
			if len(certs) == 0 {
				return errors.New("the server presented no certificate")
			}
			var root *x509.Certificate
			intermediates := x509.NewCertPool()
			for _, c := range certs[1:] {
				intermediates.AddCert(c)
			}
			for _, c := range certs {
				sum := sha256.Sum256(c.Raw)
				if hex.EncodeToString(sum[:]) == fingerprint {
					root = c
				}
			}
			if root == nil {
				return fmt.Errorf("no certificate the server presented matches caFingerprint %s", fingerprint)
			}
			if !root.BasicConstraintsValid || !root.IsCA {
				return fmt.Errorf("the certificate matching caFingerprint %s is not a CA", fingerprint)
			}
			roots := x509.NewCertPool()
			roots.AddCert(root)
			if _, err := certs[0].Verify(x509.VerifyOptions{Roots: roots, Intermediates: intermediates, DNSName: host}); err != nil {
				return fmt.Errorf("server certificate: %w", err)
			}
			return nil
		},
	}
}
