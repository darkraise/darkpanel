package daemon

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"fmt"
	"time"

	"github.com/darkraise/darkpanel/internal/transport"
)

// RenewRetry is how long the agent waits after a failed renewal before it
// asks again on the same session.
const RenewRetry = time.Hour

// RenewTimeout bounds one renewal request.
const RenewTimeout = 30 * time.Second

// renewAt is when a certificate is renewed: after two thirds of its life
// (spec section 2).
func renewAt(cert *x509.Certificate) time.Time {
	return cert.NotBefore.Add(cert.NotAfter.Sub(cert.NotBefore) * 2 / 3)
}

// renewLoop renews c's certificate over sess once it is due, retrying every
// RenewRetry, and closes renewed after the new generation is saved. It ends
// with ctx, which ends with the session.
func (d *daemon) renewLoop(ctx context.Context, sess *transport.Session, c *Credentials, renewed chan<- struct{}) {
	at := renewAt(c.Cert)
	for {
		// A fake clock fires a timer only when it advances, so a renewal
		// already due runs at once rather than through a timer.
		if delay := at.Sub(d.clock.Now()); delay > 0 {
			if err := sleep(ctx, d.clock, delay); err != nil {
				return
			}
		}
		rctx, cancel := context.WithTimeout(ctx, RenewTimeout)
		next, err := renew(rctx, d.cfg.Paths.AgentTLSDir, sess, c)
		cancel()
		if err == nil {
			d.log.Info("daemon: renewed the agent certificate", "generation", next.Generation, "notAfter", next.Cert.NotAfter)
			close(renewed)
			return
		}
		if ctx.Err() != nil {
			return
		}
		d.log.Warn("daemon: renew the agent certificate", "error", err)
		at = d.clock.Now().Add(RenewRetry)
	}
}

// renew sends a certificate request for a new key over an agent-opened
// stream of sess (POST transport.RenewPath), requires the answer to name the
// same host and chain to the same root, and saves it as a new generation.
func renew(ctx context.Context, dir string, sess *transport.Session, c *Credentials) (*Credentials, error) {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("daemon: generate the agent key: %w", err)
	}
	csr, err := csrPEM(key)
	if err != nil {
		return nil, err
	}
	var out transport.RenewResponse
	if err := post(ctx, sess.Open, transport.RenewPath, transport.RenewRequest{CSR: csr}, &out); err != nil {
		return nil, err
	}
	cert, err := checkIssued(out.Certificate, c.CA, c.HostID())
	if err != nil {
		return nil, err
	}
	next := &Credentials{Key: key, Cert: cert, CA: c.CA, Controller: c.Controller}
	if _, err := SaveGeneration(dir, next); err != nil {
		return nil, err
	}
	return next, nil
}
