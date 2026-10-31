package tailnet

import (
	"context"
	"sync"
	"time"
)

// renewAfter is the age at which a cached access token is replaced, well
// before the hour Tailscale grants one.
const renewAfter = 50 * time.Minute

// tokenCache holds the access tokens of OAuth clients in memory.
type tokenCache struct {
	// mu is held across a fetch, so that concurrent callers of one key wait
	// for its token instead of each minting another.
	mu      sync.Mutex
	entries map[string]cachedToken
}

type cachedToken struct {
	token   string
	renewAt time.Time
}

func newTokenCache() *tokenCache {
	return &tokenCache{entries: map[string]cachedToken{}}
}

// token answers the cached token of key while now is before its renewal time,
// the fetch time plus the shorter of its lifetime and renewAfter, and otherwise
// fetches and caches a new one. A failed fetch caches nothing.
func (c *tokenCache) token(ctx context.Context, key string, fetch func(ctx context.Context) (string, time.Duration, error), now time.Time) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.entries[key]; ok && now.Before(e.renewAt) {
		return e.token, nil
	}
	tok, ttl, err := fetch(ctx)
	if err != nil {
		return "", err
	}
	c.entries[key] = cachedToken{token: tok, renewAt: now.Add(min(ttl, renewAfter))}
	return tok, nil
}
