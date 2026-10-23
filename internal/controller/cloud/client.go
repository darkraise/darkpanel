// Package cloud is the controller's HTTP client for the providers modules
// manage through their APIs (spec 1.9): Tailscale, Cloudflare and Nginx
// Proxy Manager. It bounds every attempt, retries only reads and deletes,
// and turns every failed answer into a registered error code whose detail
// never carries the provider's response body.
package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// The client's bounds (spec 1.9).
const (
	Timeout    = 15 * time.Second // per request
	MaxRetries = 3                // GET and DELETE only
	CommitWait = 10 * time.Minute // Effect.CommitWait of every cloud write
)

// maxRetryAfter bounds how long a retry waits for a provider's Retry-After.
// A longer wait ends the retries at once: a request should not hold an API
// call open for minutes, and the caller can try again later.
const maxRetryAfter = 60 * time.Second

// maxBody bounds a provider's answer that the client reads.
const maxBody = 8 << 20

// Config configures New. Provider and BaseURL are required.
type Config struct {
	Provider  string                                           // "tailscale" | "cloudflare" | "npm": named in error details
	BaseURL   string                                           // scheme://host[:port][/prefix], no trailing slash
	Auth      func(r *http.Request) error                      // adds credentials to every attempt; nil adds none
	HTTP      *http.Client                                     // nil: a client with Timeout
	Clock     core.Clock                                       // nil: core.SystemClock{}
	Sleep     func(ctx context.Context, d time.Duration) error // nil: waits on Clock.AfterFunc or ctx
	ErrorCode func(body []byte) string                         // the provider's error code in an error body; nil reads none
}

// Request is one provider request.
type Request struct {
	Method string
	Path   string // joined to BaseURL; starts with "/"
	Query  url.Values
	Header http.Header
	Body   []byte // already encoded; nil sends none
}

// Response is a provider's 2xx answer.
type Response struct {
	Status int
	Header http.Header
	Body   []byte
}

// Client sends requests to one provider. It is safe for concurrent use.
type Client struct {
	cfg  Config
	base string
}

// New returns a client for cfg.
func New(cfg Config) (*Client, error) {
	if cfg.Provider == "" {
		return nil, errors.New("cloud: Config.Provider is required")
	}
	u, err := url.Parse(cfg.BaseURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" ||
		u.RawQuery != "" || u.Fragment != "" || strings.HasSuffix(cfg.BaseURL, "/") {
		return nil, fmt.Errorf("cloud: base URL %q must be scheme://host[:port][/prefix] without a trailing slash", cfg.BaseURL)
	}
	if cfg.HTTP == nil {
		cfg.HTTP = &http.Client{
			Timeout: Timeout,
			// A redirect would carry the credentials to another URL.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}
	}
	if cfg.Clock == nil {
		cfg.Clock = core.SystemClock{}
	}
	c := &Client{cfg: cfg, base: cfg.BaseURL}
	if c.cfg.Sleep == nil {
		c.cfg.Sleep = c.sleep
	}
	return c, nil
}

// retried reports whether a method may be sent again: a read changes
// nothing, and a delete that already committed answers the same way again.
func retried(method string) bool {
	return method == http.MethodGet || method == http.MethodDelete
}

// Do sends req. A 2xx answer returns its Response and a nil error. Every
// other outcome returns an *errcode.Error whose Detail names the provider,
// its HTTP status and its error code, never the body:
//
//   - 401 → CloudAuthFailed; 403 → CloudPermissionMissing; 409 and 412 →
//     CloudConflict; 429 → CloudRateLimited (GET/DELETE after their retries);
//   - 5xx or a transport failure → CloudUnavailable for GET/DELETE after
//     their retries, CloudUncertainOutcome for every other method (never
//     retried);
//   - any other status → errcode.InvalidInput for 4xx, CloudUnavailable
//     otherwise.
//
// Every 4xx answer to a method other than GET and DELETE is wrapped with
// core.Definite: the provider refused it, so nothing committed.
func (c *Client) Do(ctx context.Context, req Request) (Response, error) {
	if req.Path == "" || req.Path[0] != '/' {
		return Response{}, core.Definite(fmt.Errorf("cloud: request path %q must start with /", req.Path))
	}
	attempts := 1
	if retried(req.Method) {
		attempts += MaxRetries
	}
	var err error
	for i := range attempts {
		var res Response
		var wait time.Duration
		res, wait, err = c.attempt(ctx, req)
		if err == nil {
			return res, nil
		}
		if wait < 0 || i == attempts-1 {
			break
		}
		if wait == 0 {
			wait = time.Second << i
		}
		if wait > maxRetryAfter {
			break
		}
		if serr := c.cfg.Sleep(ctx, wait); serr != nil {
			return Response{}, fmt.Errorf("cloud: %s: waiting to retry: %w", c.cfg.Provider, serr)
		}
	}
	return Response{}, err
}

// attempt sends req once. On failure wait says what may follow: -1 means
// no retry, 0 the default backoff, and a positive value the provider's
// Retry-After.
func (c *Client) attempt(ctx context.Context, req Request) (Response, time.Duration, error) {
	actx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	hr, err := c.build(actx, req)
	if err != nil {
		return Response{}, -1, err
	}
	res, err := c.cfg.HTTP.Do(hr)
	if err != nil {
		return Response{}, 0, c.transportError(req.Method, err)
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(res.Body, maxBody+1))
	switch {
	case err != nil:
		return Response{}, 0, c.transportError(req.Method, err)
	case len(body) > maxBody:
		return Response{}, -1, errcode.New(errcode.CloudUnavailable, fmt.Sprintf("%s answered more than %d bytes", c.cfg.Provider, maxBody))
	}
	if res.StatusCode >= 200 && res.StatusCode < 300 {
		return Response{Status: res.StatusCode, Header: res.Header, Body: body}, 0, nil
	}
	wait := time.Duration(-1)
	if res.StatusCode == http.StatusTooManyRequests || res.StatusCode >= 500 {
		wait = c.retryAfter(res.Header.Get("Retry-After"))
	}
	return Response{}, wait, c.statusError(req.Method, res.StatusCode, body)
}

func (c *Client) build(ctx context.Context, req Request) (*http.Request, error) {
	target := c.base + req.Path
	if len(req.Query) > 0 {
		target += "?" + req.Query.Encode()
	}
	var body io.Reader
	if req.Body != nil {
		body = bytes.NewReader(req.Body)
	}
	hr, err := http.NewRequestWithContext(ctx, req.Method, target, body)
	if err != nil {
		return nil, core.Definite(fmt.Errorf("cloud: %s: build request: %w", c.cfg.Provider, err))
	}
	for k, vs := range req.Header {
		for _, v := range vs {
			hr.Header.Add(k, v)
		}
	}
	if c.cfg.Auth != nil {
		if err := c.cfg.Auth(hr); err != nil {
			return nil, core.Definite(fmt.Errorf("cloud: %s: credentials: %w", c.cfg.Provider, err))
		}
	}
	return hr, nil
}

// retryAfter reads a Retry-After value in seconds or as an HTTP date; 0
// means none was given.
func (c *Client) retryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}
	if s, err := strconv.Atoi(v); err == nil && s >= 0 {
		return max(time.Duration(s)*time.Second, time.Nanosecond)
	}
	if t, err := http.ParseTime(v); err == nil {
		return max(t.Sub(c.cfg.Clock.Now()), time.Nanosecond)
	}
	return 0
}

// statusErr carries the provider's answer inside an *errcode.Error, for
// Status.
type statusErr struct {
	status int
	code   string
}

func (e *statusErr) Error() string {
	if e.code == "" {
		return fmt.Sprintf("cloud: status %d", e.status)
	}
	return fmt.Sprintf("cloud: status %d (%s)", e.status, e.code)
}

func (c *Client) statusError(method string, status int, body []byte) error {
	se := &statusErr{status: status}
	if c.cfg.ErrorCode != nil {
		se.code = c.cfg.ErrorCode(body)
	}
	detail := fmt.Sprintf("%s answered %d", c.cfg.Provider, status)
	if se.code != "" {
		detail += " (" + se.code + ")"
	}
	var code errcode.Code
	switch {
	case status == http.StatusUnauthorized:
		code = errcode.CloudAuthFailed
	case status == http.StatusForbidden:
		code = errcode.CloudPermissionMissing
	case status == http.StatusConflict || status == http.StatusPreconditionFailed:
		code = errcode.CloudConflict
	case status == http.StatusTooManyRequests:
		code = errcode.CloudRateLimited
	case status >= 500 && retried(method):
		code = errcode.CloudUnavailable
	case status >= 500:
		code = errcode.CloudUncertainOutcome
		detail += "; the request may still have committed"
	case status >= 400:
		code = errcode.InvalidInput
	default:
		code = errcode.CloudUnavailable
	}
	err := error(errcode.Wrap(code, se, detail))
	if status >= 400 && status < 500 && !retried(method) {
		err = core.Definite(err)
	}
	return err
}

func (c *Client) transportError(method string, err error) error {
	if retried(method) {
		return errcode.Wrap(errcode.CloudUnavailable, err, c.cfg.Provider+" could not be reached")
	}
	return errcode.Wrap(errcode.CloudUncertainOutcome, err, c.cfg.Provider+" did not answer; the request may still have committed")
}

// sleep waits d on the client's clock, or until ctx ends.
func (c *Client) sleep(ctx context.Context, d time.Duration) error {
	done := make(chan struct{})
	t := c.cfg.Clock.AfterFunc(d, func() { close(done) })
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		t.Stop()
		return ctx.Err()
	}
}

// JSON encodes in (nil sends no body), calls Do with Content-Type
// application/json, and decodes a 2xx body into out (nil skips decoding).
func (c *Client) JSON(ctx context.Context, method, path string, in, out any) (Response, error) {
	req := Request{Method: method, Path: path, Header: http.Header{"Accept": {"application/json"}}}
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return Response{}, core.Definite(fmt.Errorf("cloud: %s: encode request: %w", c.cfg.Provider, err))
		}
		req.Body = b
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.Do(ctx, req)
	if err != nil || out == nil || len(res.Body) == 0 {
		return res, err
	}
	if err := json.Unmarshal(res.Body, out); err != nil {
		return res, errcode.Wrap(errcode.CloudUnavailable, err, c.cfg.Provider+" answered JSON that could not be read")
	}
	return res, nil
}

// Status returns the provider HTTP status that err carries, or 0.
func Status(err error) int {
	var se *statusErr
	if errors.As(err, &se) {
		return se.status
	}
	return 0
}
