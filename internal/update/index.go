package update

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
)

// IndexTTL is how long a fetched release index is reused. The index only
// selects versions; each release's own signature makes it trusted, so a stale
// index can delay an update but never install an unsigned one.
const IndexTTL = time.Hour

// maxIndexBytes bounds the release index, which lists one short entry per
// published version.
const maxIndexBytes = 1 << 20

// Index is the release index file <base>/index/releases.json.
type Index struct {
	FormatVersion int       `json:"formatVersion"`
	Releases      []Release `json:"releases"`
}

// IndexSource reads the release index, at most once per IndexTTL per process.
type IndexSource struct {
	url    string
	client *http.Client
	clock  core.Clock

	mu      sync.Mutex // serialises fetches, so callers within the hour share one
	cached  []Release
	fetched time.Time
	valid   bool
}

// NewIndexSource reads the index under baseURL, the release base URL.
func NewIndexSource(baseURL string, client *http.Client, clock core.Clock) *IndexSource {
	return &IndexSource{
		url:    strings.TrimSuffix(baseURL, "/") + "/index/releases.json",
		client: client,
		clock:  clock,
	}
}

// Releases returns the index's entries. A failed fetch is not cached, so the
// next call tries again.
func (s *IndexSource) Releases(ctx context.Context) ([]Release, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock.Now()
	// A clock set back before the last fetch also refetches.
	if s.valid && !now.Before(s.fetched) && now.Sub(s.fetched) < IndexTTL {
		return slices.Clone(s.cached), nil
	}
	releases, err := s.fetch(ctx)
	if err != nil {
		return nil, err
	}
	s.cached, s.fetched, s.valid = releases, now, true
	return slices.Clone(releases), nil
}

func (s *IndexSource) fetch(ctx context.Context) ([]Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url, nil)
	if err != nil {
		return nil, fmt.Errorf("update: release index: %w", err)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("update: release index: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("update: release index %s answered %s", s.url, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxIndexBytes+1))
	if err != nil {
		return nil, fmt.Errorf("update: release index: %w", err)
	}
	if len(body) > maxIndexBytes {
		return nil, fmt.Errorf("update: release index %s is over %d bytes", s.url, maxIndexBytes)
	}
	var idx Index
	dec := json.NewDecoder(bytes.NewReader(body))
	if err := dec.Decode(&idx); err != nil {
		return nil, fmt.Errorf("update: release index: decode: %w", err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("update: release index: decode: data after the index object")
	}
	if idx.FormatVersion != 1 {
		return nil, fmt.Errorf("update: release index has format %d, want 1", idx.FormatVersion)
	}
	return idx.Releases, nil
}
