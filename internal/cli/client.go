package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// maxResponseBytes bounds the response body the CLI reads.
const maxResponseBytes = 16 << 20

// client talks to the network API with a bearer token, or to the controller
// socket without one.
type client struct {
	base  string // scheme and host, no trailing slash
	token string // "" sends no Authorization header
	http  *http.Client
}

// newSocketClient returns a client that reaches the controller socket at
// path (spec section 14); the peer credentials are the authentication.
func newSocketClient(path string) *client {
	tr := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		c, err := d.DialContext(ctx, "unix", path)
		if err != nil {
			return nil, fmt.Errorf("controller socket %s: %w", path, err)
		}
		return c, nil
	}}
	return &client{base: "http://controller", http: &http.Client{Transport: tr, Timeout: requestTimeout}}
}

// do sends a request with body as JSON (nil sends none) and decodes a 2xx
// answer into out (nil discards it). An answer in problem details is a
// *problemError; any other failure is a plain error.
func (c *client) do(ctx context.Context, method, path string, body, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode the request: %w", err)
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, r)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("%s %s: read the answer: %w", method, path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return answerError(method, path, resp, data)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("%s %s: decode the answer: %w", method, path, err)
	}
	return nil
}

// answerError turns a non-2xx answer into a *problemError when it carries
// problem details with a code, else into a plain error naming the status.
func answerError(method, path string, resp *http.Response, data []byte) error {
	if mt, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type")); err == nil && mt == "application/problem+json" {
		var p struct {
			Status int                  `json:"status"`
			Detail string               `json:"detail"`
			Code   errcode.Code         `json:"code"`
			Errors []errcode.FieldError `json:"errors"`
		}
		if json.Unmarshal(data, &p) == nil && p.Code != "" {
			return &problemError{Status: resp.StatusCode, Code: p.Code, Detail: p.Detail, Fields: p.Errors}
		}
	}
	return fmt.Errorf("%s %s: unexpected answer %s", method, path, resp.Status)
}
