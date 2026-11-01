package listen

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/ext"
)

const (
	certReadTimeout = 10 * time.Second
	certRetryAfter  = time.Minute
	certRecheck     = 5 * time.Minute
)

var certReader = core.Principal{Actor: "system:listen.cert", Scope: core.ScopeAdmin}

// tailscaleCertSource serves the host's Tailscale certificate for its
// MagicDNS name and falls through for every other name.
func tailscaleCertSource(d SourceDeps) CertSource { return sharedPairs(d) }

// tailscaleNames are the MagicDNS names the CA leaf also carries: the name of
// the host's Tailscale certificate, or none while there is no certificate.
func tailscaleNames(ctx context.Context, d SourceDeps) ([]string, error) {
	return sharedPairs(d).names(ctx)
}

type pairKey struct {
	dispatch ext.Dispatcher
	host     string
}

// pairs holds one cache per dispatcher and host. The frozen sources.go hands
// the certificate source and the name hook only a SourceDeps, so this registry
// is how a handshake and the leaf's names share one pair and one read.
var pairs sync.Map // pairKey -> *pairCache

func sharedPairs(d SourceDeps) *pairCache {
	key := pairKey{dispatch: d.Dispatch, host: d.LocalHostID}
	if c, ok := pairs.Load(key); ok {
		return c.(*pairCache)
	}
	c, _ := pairs.LoadOrStore(key, newPairCache(d, core.SystemClock{}))
	return c.(*pairCache)
}

type pairCache struct {
	d       SourceDeps
	clock   core.Clock
	log     *slog.Logger
	timeout time.Duration
	wg      sync.WaitGroup

	mu      sync.Mutex
	pair    *certPair
	loading bool
	// nextTry gates the handshake path after a failed read, so a tailscaled
	// that does not answer is not asked again on every handshake.
	nextTry time.Time
	lastErr string
}

type certPair struct {
	name    string
	cert    *tls.Certificate
	expires time.Time
	renewAt time.Time
	readAt  time.Time
}

type certAnswer struct {
	Name    string `json:"name"`
	CertPEM string `json:"certPem"`
	KeyPEM  string `json:"keyPem"`
}

func newPairCache(d SourceDeps, clock core.Clock) *pairCache {
	log := d.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &pairCache{d: d, clock: clock, log: log, timeout: certReadTimeout}
}

func (c *pairCache) wait() { c.wg.Wait() }

// Certificate serves the cached pair when hello asks for its name. A pair
// that is due for renewal keeps being served while its refresh runs in the
// background; the handshake never waits for a read.
func (c *pairCache) Certificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	if c.d.Dispatch == nil {
		return nil, nil
	}
	now := c.clock.Now()
	c.mu.Lock()
	p := c.pair
	if !c.loading && (p == nil || !now.Before(p.renewAt)) && !now.Before(c.nextTry) {
		c.loading = true
		c.wg.Add(1)
		go c.refresh()
	}
	c.mu.Unlock()
	if p != nil && now.Before(p.expires) && normalizeName(hello.ServerName) == p.name {
		return p.cert, nil
	}
	return nil, nil
}

func (c *pairCache) refresh() {
	defer c.wg.Done()
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()
	c.read(ctx)
	c.mu.Lock()
	c.loading = false
	c.mu.Unlock()
}

// names returns the cached pair's name, reading the pair first when there is
// none, when it is due for renewal, or when it was read certRecheck ago: an
// address change can give the node a new MagicDNS name.
func (c *pairCache) names(ctx context.Context) ([]string, error) {
	if c.d.Dispatch == nil {
		return nil, nil
	}
	now := c.clock.Now()
	c.mu.Lock()
	p := c.pair
	c.mu.Unlock()
	if p == nil || !now.Before(p.renewAt) || now.Sub(p.readAt) >= certRecheck {
		rctx, cancel := context.WithTimeout(ctx, c.timeout)
		c.read(rctx)
		cancel()
		c.mu.Lock()
		p = c.pair
		c.mu.Unlock()
	}
	if p == nil {
		return nil, nil
	}
	return []string{p.name}, nil
}

func (c *pairCache) read(ctx context.Context) {
	p, err := c.fetch(ctx)
	now := c.clock.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	if err == nil {
		p.readAt = now
		c.pair = p
		c.nextTry = time.Time{}
		c.lastErr = ""
		return
	}
	c.nextTry = now.Add(certRetryAfter)
	if msg := err.Error(); msg != c.lastErr {
		c.lastErr = msg
		c.log.Debug("listen: the Tailscale certificate is unavailable; falling through", "err", err)
	}
}

func (c *pairCache) fetch(ctx context.Context) (*certPair, error) {
	body, err := c.d.Dispatch.ReadInternal(ctx, certReader, c.d.LocalHostID, core.ActionRef{Name: "tailscale.cert.get", Version: 1}, json.RawMessage("{}"))
	if err != nil {
		return nil, fmt.Errorf("listen: read the Tailscale certificate: %w", err)
	}
	var ans certAnswer
	if err := json.Unmarshal(body, &ans); err != nil {
		return nil, fmt.Errorf("listen: decode the Tailscale certificate: %w", err)
	}
	name := normalizeName(ans.Name)
	if err := validDNSName(name); err != nil {
		return nil, fmt.Errorf("listen: the Tailscale certificate's name: %w", err)
	}
	cert, err := tls.X509KeyPair([]byte(ans.CertPEM), []byte(ans.KeyPEM))
	if err != nil {
		return nil, fmt.Errorf("listen: the Tailscale certificate pair: %w", err)
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return nil, fmt.Errorf("listen: the Tailscale certificate: %w", err)
	}
	cert.Leaf = leaf
	life := leaf.NotAfter.Sub(leaf.NotBefore)
	return &certPair{
		name:    name,
		cert:    &cert,
		expires: leaf.NotAfter,
		renewAt: leaf.NotBefore.Add(life - life/3),
	}, nil
}

func normalizeName(s string) string {
	return strings.TrimSuffix(strings.ToLower(s), ".")
}

func validDNSName(name string) error {
	if len(name) < 1 || len(name) > 253 {
		return fmt.Errorf("%d bytes long, not 1 to 253", len(name))
	}
	if _, err := netip.ParseAddr(name); err == nil {
		return errors.New("an IP address, not a DNS name")
	}
	for label := range strings.SplitSeq(name, ".") {
		if len(label) < 1 || len(label) > 63 {
			return fmt.Errorf("a label of %d bytes, not 1 to 63", len(label))
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return errors.New("a label starts or ends with a hyphen")
		}
		for i := 0; i < len(label); i++ {
			if b := label[i]; (b < 'a' || b > 'z') && (b < '0' || b > '9') && b != '-' {
				return errors.New("a label holds a byte other than a-z, 0-9 or a hyphen")
			}
		}
	}
	return nil
}
