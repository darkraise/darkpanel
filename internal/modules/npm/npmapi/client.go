// Package npmapi is a hand-written client of the Nginx Proxy Manager API: the status, user, token, proxy host,
// access list and certificate endpoints the npm module calls.
package npmapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"syscall"
)

const (
	hostsPath = "/api/nginx/proxy-hosts"
	listsPath = "/api/nginx/access-lists"
	certsPath = "/api/nginx/certificates"

	// errorBodyLimit bounds how much of a refusal is read.
	errorBodyLimit = 64 << 10
	// answerBodyLimit is the most bytes of a successful answer the client reads. It equals the agent API's body
	// limit because a larger answer could not be returned to the controller.
	answerBodyLimit = 4 << 20
)

// Client talks to one NPM instance. It is safe for concurrent use.
type Client struct {
	base  string
	hc    *http.Client
	token string
}

// New returns a client of the NPM API at baseURL (for example http://127.0.0.1:81, no trailing slash) that sends its
// requests through hc, or through a client without a timeout when hc is nil: a certificate request waits for the ACME
// order, so the caller's context bounds every call.
func New(baseURL string, hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{}
	}
	return &Client{base: baseURL, hc: hc}
}

// WithToken returns a copy of the client that sends token as a bearer token.
func (c *Client) WithToken(token string) *Client {
	cp := *c
	cp.token = token
	return &cp
}

// Health reads NPM's status. The path keeps its trailing slash because the admin port answers a bare /api with a
// redirect, which the module's client does not follow.
func (c *Client) Health(ctx context.Context) (Health, error) {
	var h Health
	err := c.do(ctx, http.MethodGet, "/api/", "", nil, &h)
	return h, err
}

// CreateAdmin creates the first user, an administrator; NPM accepts it without credentials only while no user exists.
func (c *Client) CreateAdmin(ctx context.Context, email, password string) error {
	return c.do(ctx, http.MethodPost, "/api/users", "", map[string]any{
		"name":        "Darkpanel",
		"nickname":    "darkpanel",
		"email":       email,
		"roles":       []string{"admin"},
		"is_disabled": false,
		"auth":        map[string]string{"type": "password", "secret": password},
	}, nil)
}

// Token logs in and returns a bearer token.
func (c *Client) Token(ctx context.Context, email, password string) (string, error) {
	var out struct {
		Token string `json:"token"`
	}
	err := c.do(ctx, http.MethodPost, "/api/tokens", "", map[string]string{"identity": email, "secret": password}, &out)
	if err != nil {
		return "", err
	}
	if out.Token == "" {
		return "", errors.New("npmapi: the token answer carries no token")
	}
	return out.Token, nil
}

// ProxyHosts lists the proxy hosts; the slice is never nil.
func (c *Client) ProxyHosts(ctx context.Context) ([]ProxyHost, error) {
	var out []ProxyHost
	if err := c.do(ctx, http.MethodGet, hostsPath, "", nil, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []ProxyHost{}
	}
	return out, nil
}

// ProxyHost reads one proxy host.
func (c *Client) ProxyHost(ctx context.Context, id int) (ProxyHost, error) {
	var out ProxyHost
	err := c.do(ctx, http.MethodGet, hostsPath+"/"+strconv.Itoa(id), "", nil, &out)
	return out, err
}

// CreateProxyHost creates a proxy host.
func (c *Client) CreateProxyHost(ctx context.Context, b ProxyHostBody) (ProxyHost, error) {
	var out ProxyHost
	err := c.do(ctx, http.MethodPost, hostsPath, "", b, &out)
	return out, err
}

// UpdateProxyHost replaces the members of a proxy host.
func (c *Client) UpdateProxyHost(ctx context.Context, id int, b ProxyHostBody) (ProxyHost, error) {
	var out ProxyHost
	err := c.do(ctx, http.MethodPut, hostsPath+"/"+strconv.Itoa(id), "", b, &out)
	return out, err
}

// DeleteProxyHost deletes a proxy host.
func (c *Client) DeleteProxyHost(ctx context.Context, id int) error {
	return c.do(ctx, http.MethodDelete, hostsPath+"/"+strconv.Itoa(id), "", nil, nil)
}

// AccessLists lists the access lists with their clients; the slice is never nil.
func (c *Client) AccessLists(ctx context.Context) ([]AccessList, error) {
	var out []AccessList
	if err := c.do(ctx, http.MethodGet, listsPath, "expand=clients", nil, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []AccessList{}
	}
	return out, nil
}

// AccessList reads one access list with its clients.
func (c *Client) AccessList(ctx context.Context, id int) (AccessList, error) {
	var out AccessList
	err := c.do(ctx, http.MethodGet, listsPath+"/"+strconv.Itoa(id), "expand=clients", nil, &out)
	return out, err
}

// CreateAccessList creates an access list.
func (c *Client) CreateAccessList(ctx context.Context, b AccessListBody) (AccessList, error) {
	var out AccessList
	err := c.do(ctx, http.MethodPost, listsPath, "", b, &out)
	return out, err
}

// DeleteAccessList deletes an access list.
func (c *Client) DeleteAccessList(ctx context.Context, id int) error {
	return c.do(ctx, http.MethodDelete, listsPath+"/"+strconv.Itoa(id), "", nil, nil)
}

// Certificates lists the certificates; the slice is never nil.
func (c *Client) Certificates(ctx context.Context) ([]Certificate, error) {
	var out []Certificate
	if err := c.do(ctx, http.MethodGet, certsPath, "", nil, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []Certificate{}
	}
	return out, nil
}

// Certificate reads one certificate.
func (c *Client) Certificate(ctx context.Context, id int) (Certificate, error) {
	var out Certificate
	err := c.do(ctx, http.MethodGet, certsPath+"/"+strconv.Itoa(id), "", nil, &out)
	return out, err
}

// CreateCertificate requests a Let's Encrypt certificate and returns when NPM has finished the ACME order.
func (c *Client) CreateCertificate(ctx context.Context, b CertificateBody) (Certificate, error) {
	var out Certificate
	err := c.do(ctx, http.MethodPost, certsPath, "", b, &out)
	return out, err
}

// DeleteCertificate deletes a certificate.
func (c *Client) DeleteCertificate(ctx context.Context, id int) error {
	return c.do(ctx, http.MethodDelete, certsPath+"/"+strconv.Itoa(id), "", nil, nil)
}

// do sends one request. A 2xx answer is decoded into out when out is not nil and otherwise dropped; any other status
// becomes an *APIError, which never keeps the answer's text. A request without a usable answer, or a 2xx answer that
// does not decode, fails with the method, the path and a fixed reason, because Go's transport and JSON errors quote
// bytes of the answer.
func (c *Client) do(ctx context.Context, method, path, query string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("npmapi: encode %s request: %w", path, err)
		}
		rd = bytes.NewReader(b)
	}
	u := c.base + path
	if query != "" {
		u += "?" + query
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return fmt.Errorf("npmapi: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	res, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("npmapi: %s %s: %w", method, path, unanswered(err))
	}
	// A short remainder is drained so the connection can be reused; a longer one is abandoned and the connection
	// closed, so a hostile NPM cannot keep the client reading.
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, errorBodyLimit))
		_ = res.Body.Close()
	}()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return apiError(res)
	}
	if out == nil {
		return nil
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, answerBodyLimit+1))
	if err != nil {
		return fmt.Errorf("npmapi: read %s %s answer: %w", method, path, unanswered(err))
	}
	if len(b) > answerBodyLimit {
		return fmt.Errorf("npmapi: %s %s answer exceeds %d bytes", method, path, answerBodyLimit)
	}
	if err := json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("npmapi: decode %s %s answer: %w", method, path, ErrUndecodable)
	}
	return nil
}

// apiError builds the error of a refusal from its status and NPM's error.code when that is a number from 400 to 599.
// The answer's text is never decoded or kept.
func apiError(res *http.Response) *APIError {
	ae := &APIError{Status: res.StatusCode}
	var envelope struct {
		Error struct {
			Code json.RawMessage `json:"code"`
		} `json:"error"`
	}
	if b, err := io.ReadAll(io.LimitReader(res.Body, errorBodyLimit)); err == nil && json.Unmarshal(b, &envelope) == nil {
		if code, err := strconv.Atoi(string(bytes.TrimSpace(envelope.Error.Code))); err == nil && code >= 400 && code <= 599 {
			ae.Code = code
		}
	}
	return ae
}

// ErrUndecodable marks a 2xx answer whose body is not the JSON the call expects.
var ErrUndecodable = errors.New("not the expected JSON")

// errNoAnswer and errRefused are the fixed reasons of a request that got no usable answer.
var (
	errNoAnswer = errors.New("no usable answer")
	errRefused  = errors.New("connection refused")
)

// unanswered classifies a failed exchange with a fixed reason. It never wraps or formats err, whose text can quote
// bytes NPM sent; the context sentinels are returned as they are, so errors.Is still finds them.
func unanswered(err error) error {
	switch {
	case errors.Is(err, context.Canceled):
		return context.Canceled
	case errors.Is(err, context.DeadlineExceeded):
		return context.DeadlineExceeded
	case errors.Is(err, syscall.ECONNREFUSED):
		return errRefused
	default:
		return errNoAnswer
	}
}
