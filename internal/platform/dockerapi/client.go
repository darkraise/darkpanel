// Package dockerapi is a hand-written client for the Docker Engine API over
// the daemon's Unix socket (spec 1.8). It models only the endpoints and
// fields Darkpanel's modules use, and it never shells out to the docker CLI
// (R54).
package dockerapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultSocket is the Docker daemon's API socket.
const DefaultSocket = "/var/run/docker.sock"

// The client speaks the newest API version both sides support within this
// range. 1.44 is Docker 25, the oldest daemon the docker module supports.
// 1.49 (Docker 28.1) is the first version whose /info reports
// FirewallBackend; newer versions change shapes this client does not model
// (1.54 adds a JSON log format), so the client never asks for more.
const (
	MinAPIVersion = "1.44"
	MaxAPIVersion = "1.49"
)

// maxBody bounds a JSON response read into memory; maxErrorBody bounds the
// text kept from an error answer.
const (
	maxBody      = 64 << 20
	maxErrorBody = 64 << 10
)

var (
	// ErrUnavailable wraps a failure to reach the daemon's socket.
	ErrUnavailable = errors.New("dockerapi: daemon unavailable")
	// ErrUnsupportedVersion is returned when the daemon's API is older than
	// MinAPIVersion.
	ErrUnsupportedVersion = errors.New("dockerapi: daemon API version unsupported")
	// ErrEmptyID is wrapped by the error of a call given an empty object id,
	// name or reference.
	ErrEmptyID = errors.New("dockerapi: empty id")
)

// requireID refuses an empty id before any request: an empty path segment
// would address the collection's list route (for example /volumes/), and the
// daemon would answer something decodable instead of an error.
func requireID(what, id string) error {
	if id == "" {
		return fmt.Errorf("%w: %s", ErrEmptyID, what)
	}
	return nil
}

// APIError is an answer outside 2xx and 304 from the daemon.
type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("dockerapi: daemon answered %d: %s", e.StatusCode, e.Message)
}

// IsNotFound reports whether err is the daemon's 404 answer.
func IsNotFound(err error) bool { return statusIs(err, http.StatusNotFound) }

// IsConflict reports whether err is the daemon's 409 answer (an object in
// use, a name taken).
func IsConflict(err error) bool { return statusIs(err, http.StatusConflict) }

func statusIs(err error, code int) bool {
	var e *APIError
	return errors.As(err, &e) && e.StatusCode == code
}

// Filters is the Engine API's filters argument: each key with its accepted
// values, for example {"label": {"darkpanel.module=npm"}}.
type Filters map[string][]string

func (f Filters) set(q url.Values) error {
	if len(f) == 0 {
		return nil
	}
	b, err := json.Marshal(map[string][]string(f))
	if err != nil {
		return fmt.Errorf("dockerapi: encode filters: %w", err)
	}
	q.Set("filters", string(b))
	return nil
}

// Client talks to one daemon. It negotiates the API version on first use
// and is safe for concurrent use.
type Client struct {
	hc *http.Client

	// negotiating has one slot, held while a /version call is in flight. A
	// channel rather than a mutex lets a queued caller give up when its own
	// context ends instead of waiting behind a hung daemon.
	negotiating chan struct{}

	mu      sync.Mutex
	version string
}

// New returns a client for the daemon listening on socket.
func New(socket string) *Client {
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			conn, err := d.DialContext(ctx, "unix", socket)
			if err != nil {
				return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
			}
			return conn, nil
		},
		DisableCompression: true,
		MaxIdleConns:       4,
		IdleConnTimeout:    30 * time.Second,
	}
	return &Client{hc: &http.Client{Transport: tr}, negotiating: make(chan struct{}, 1)}
}

// Close releases idle connections.
func (c *Client) Close() { c.hc.CloseIdleConnections() }

// APIVersion returns the negotiated API version: the daemon's, capped at
// MaxAPIVersion. A failed negotiation is retried on the next call, and so is
// negotiation after the daemon became unreachable, since it may have been
// replaced by another version.
func (c *Client) APIVersion(ctx context.Context) (string, error) {
	if v := c.cachedVersion(); v != "" {
		return v, nil
	}
	select {
	case c.negotiating <- struct{}{}:
	case <-ctx.Done():
		return "", fmt.Errorf("dockerapi: negotiate API version: %w", ctx.Err())
	}
	defer func() { <-c.negotiating }()
	if v := c.cachedVersion(); v != "" {
		return v, nil
	}
	v, err := c.Version(ctx)
	if err != nil {
		return "", err
	}
	cmpMin, err := compareVersions(v.APIVersion, MinAPIVersion)
	if err != nil {
		return "", err
	}
	if cmpMin < 0 {
		return "", fmt.Errorf("%w: daemon API %s is older than %s", ErrUnsupportedVersion, v.APIVersion, MinAPIVersion)
	}
	negotiated := MaxAPIVersion
	if cmpMax, _ := compareVersions(v.APIVersion, MaxAPIVersion); cmpMax < 0 {
		negotiated = v.APIVersion
	}
	c.mu.Lock()
	c.version = negotiated
	c.mu.Unlock()
	return negotiated, nil
}

func (c *Client) cachedVersion() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.version
}

func (c *Client) forgetVersion() {
	c.mu.Lock()
	c.version = ""
	c.mu.Unlock()
}

// compareVersions compares two "major.minor" API versions.
func compareVersions(a, b string) (int, error) {
	pa, err := parseVersion(a)
	if err != nil {
		return 0, err
	}
	pb, err := parseVersion(b)
	if err != nil {
		return 0, err
	}
	for i := range pa {
		if pa[i] != pb[i] {
			if pa[i] < pb[i] {
				return -1, nil
			}
			return 1, nil
		}
	}
	return 0, nil
}

func parseVersion(v string) ([2]int, error) {
	major, minor, ok := strings.Cut(v, ".")
	maj, err1 := strconv.Atoi(major)
	mnr, err2 := strconv.Atoi(minor)
	if !ok || err1 != nil || err2 != nil || maj < 0 || mnr < 0 {
		return [2]int{}, fmt.Errorf("dockerapi: malformed API version %q", v)
	}
	return [2]int{maj, mnr}, nil
}

// request is one call; body is nil, an io.Reader sent as-is with
// contentType, or a value sent as JSON.
type request struct {
	method      string
	path        string
	query       url.Values
	body        any
	contentType string
}

// do sends r under the negotiated version and returns the response when its
// status is 2xx or 304; the caller closes its body.
func (c *Client) do(ctx context.Context, r request) (*http.Response, error) {
	v, err := c.APIVersion(ctx)
	if err != nil {
		return nil, err
	}
	r.path = "/v" + v + r.path
	return c.send(ctx, r)
}

func (c *Client) send(ctx context.Context, r request) (*http.Response, error) {
	var body io.Reader
	contentType := r.contentType
	switch b := r.body.(type) {
	case nil:
	case io.Reader:
		body = b
	default:
		buf, err := json.Marshal(b)
		if err != nil {
			return nil, fmt.Errorf("dockerapi: encode %s %s: %w", r.method, r.path, err)
		}
		body = bytes.NewReader(buf)
		contentType = "application/json"
	}
	u := "http://docker" + r.path
	if len(r.query) > 0 {
		u += "?" + r.query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, r.method, u, body)
	if err != nil {
		return nil, fmt.Errorf("dockerapi: %s %s: %w", r.method, r.path, err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	res, err := c.hc.Do(req)
	if err != nil {
		if errors.Is(err, ErrUnavailable) {
			c.forgetVersion()
		}
		return nil, fmt.Errorf("dockerapi: %s %s: %w", r.method, r.path, err)
	}
	if res.StatusCode/100 != 2 && res.StatusCode != http.StatusNotModified {
		defer func() { _ = res.Body.Close() }()
		return nil, readAPIError(res)
	}
	return res, nil
}

func readAPIError(res *http.Response) error {
	b, _ := io.ReadAll(io.LimitReader(res.Body, maxErrorBody))
	msg := strings.TrimSpace(string(b))
	var j struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(b, &j) == nil && j.Message != "" {
		msg = j.Message
	}
	return &APIError{StatusCode: res.StatusCode, Message: msg}
}

// call sends r and decodes a JSON answer into out, or discards the body
// when out is nil. It returns the status code.
func (c *Client) call(ctx context.Context, r request, out any) (int, error) {
	res, err := c.do(ctx, r)
	if err != nil {
		return 0, err
	}
	defer func() { _ = res.Body.Close() }()
	if out == nil || res.StatusCode == http.StatusNotModified || res.StatusCode == http.StatusNoContent {
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, maxBody))
		return res.StatusCode, nil
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, maxBody)).Decode(out); err != nil {
		return res.StatusCode, fmt.Errorf("dockerapi: decode %s %s: %w", r.method, r.path, err)
	}
	return res.StatusCode, nil
}

// maxStreamValue bounds the bytes Next reads from the network while it
// decodes one value; events, progress messages and stats samples are far
// smaller.
const maxStreamValue = 1 << 20

var errStreamValueTooLarge = fmt.Errorf("dockerapi: stream value exceeds %d bytes", maxStreamValue)

// valueReader fails once more than its budget is read; the stream resets the
// budget before each value, so a stream may run indefinitely while no single
// value grows without bound.
type valueReader struct {
	r    io.Reader
	left int64
}

func (v *valueReader) Read(p []byte) (int, error) {
	if v.left <= 0 {
		return 0, errStreamValueTooLarge
	}
	if int64(len(p)) > v.left {
		p = p[:v.left]
	}
	n, err := v.r.Read(p)
	v.left -= int64(n)
	return n, err
}

// Stream decodes a stream of JSON values: events, pull progress, stats.
type Stream[T any] struct {
	body io.ReadCloser
	src  *valueReader
	dec  *json.Decoder
}

func newStream[T any](res *http.Response) *Stream[T] {
	src := &valueReader{r: res.Body}
	return &Stream[T]{body: res.Body, src: src, dec: json.NewDecoder(src)}
}

// Next returns the next value, or io.EOF once the daemon ends the stream.
// Cancelling the request's context ends a stream that would block.
func (s *Stream[T]) Next() (T, error) {
	var v T
	s.src.left = maxStreamValue
	err := s.dec.Decode(&v)
	return v, err
}

// Close ends the stream.
func (s *Stream[T]) Close() error { return s.body.Close() }

// VersionInfo is the daemon's /version answer.
type VersionInfo struct {
	Version       string
	APIVersion    string `json:"ApiVersion"`
	MinAPIVersion string
	Os            string
	Arch          string
	KernelVersion string
}

// Version reads /version without a version prefix, which every daemon
// serves; negotiation uses it.
func (c *Client) Version(ctx context.Context) (VersionInfo, error) {
	res, err := c.send(ctx, request{method: http.MethodGet, path: "/version"})
	if err != nil {
		return VersionInfo{}, err
	}
	defer func() { _ = res.Body.Close() }()
	var v VersionInfo
	if err := json.NewDecoder(io.LimitReader(res.Body, maxBody)).Decode(&v); err != nil {
		return VersionInfo{}, fmt.Errorf("dockerapi: decode /version: %w", err)
	}
	return v, nil
}

// SystemInfo is the part of /info the modules read.
type SystemInfo struct {
	ID                string
	ServerVersion     string
	Containers        int
	ContainersRunning int
	Images            int
	Driver            string
	DockerRootDir     string
	OperatingSystem   string
	// FirewallBackend is nil when the daemon does not report it (Docker
	// 28.0, or a negotiated version below 1.49): such a daemon uses its
	// iptables backend.
	FirewallBackend *FirewallBackend
}

// FirewallBackend is /info's FirewallBackend; its Info list is not stable
// and is not read.
type FirewallBackend struct {
	Driver string
}

// Info reads /info.
func (c *Client) Info(ctx context.Context) (SystemInfo, error) {
	var info SystemInfo
	_, err := c.call(ctx, request{method: http.MethodGet, path: "/info"}, &info)
	return info, err
}
