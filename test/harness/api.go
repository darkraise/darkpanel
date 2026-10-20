//go:build harness

package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/api"
	"github.com/darkraise/darkpanel/internal/core"
)

// apiTimeout bounds one API request; every call of the scenarios also has
// its own context.
const apiTimeout = 90 * time.Second

// API is a client of the controller's HTTP API logged in as one user: it
// keeps the session cookie and sends the session's CSRF token with every
// POST, as the web UI does.
type API struct {
	base string
	http *http.Client
	csrf string
}

// NewAPI returns a client of the API at base, such as http://127.0.0.1:8080,
// whose connections dial opens: vm.VM.DialContext reaches the controller's
// loopback listener inside its VM. Each request opens its own connection,
// so a connection the controller's restart closed is never reused.
func NewAPI(base string, dial func(ctx context.Context, network, addr string) (net.Conn, error)) *API {
	jar, err := cookiejar.New(nil)
	if err != nil {
		panic(err) // cookiejar.New fails only on options, and it gets none
	}
	tr := &http.Transport{DialContext: dial, DisableKeepAlives: true}
	return &API{base: strings.TrimSuffix(base, "/"), http: &http.Client{Transport: tr, Jar: jar, Timeout: apiTimeout}}
}

// APIError is an answer outside 2xx. Code and Detail come from its problem
// details; an answer without them leaves Code empty and quotes the body in
// Detail.
type APIError struct {
	Status int
	Code   string
	Detail string
}

func (e *APIError) Error() string {
	if e.Code == "" {
		return fmt.Sprintf("harness: the API answered %d: %s", e.Status, e.Detail)
	}
	return fmt.Sprintf("harness: the API answered %d %s: %s", e.Status, e.Code, e.Detail)
}

// Change is the part of a change the scenarios read.
type Change struct {
	ID     string            `json:"id"`
	State  string            `json:"state"`
	Output json.RawMessage   `json:"output"`
	Error  *core.ErrorDetail `json:"error"`
}

// Login opens a session with a password and a TOTP code.
func (a *API) Login(ctx context.Context, username, password, code string) error {
	var s struct {
		CSRFToken *string `json:"csrfToken"`
	}
	body := map[string]string{"username": username, "password": password, "code": code}
	if err := a.do(ctx, http.MethodPost, "/api/v1/auth/login", body, &s); err != nil {
		return err
	}
	if s.CSRFToken == nil || *s.CSRFToken == "" {
		return errors.New("harness: the login answered no CSRF token")
	}
	a.csrf = *s.CSRFToken
	return nil
}

// Apply plans action, written name/v<n>, with input on host, or on the
// controller when host is empty; applies the plan with the confirmation
// phrase it names, as an operator typing it would; and decodes the change's
// output into out unless out is nil. A change that did not complete is an
// error naming its code.
func (a *API) Apply(ctx context.Context, host, action string, input, out any) (Change, error) {
	planPath, err := actionPath(host, action, "plan")
	if err != nil {
		return Change{}, err
	}
	applyPath, _ := actionPath(host, action, "apply")
	var plan struct {
		PlanID       string  `json:"planId"`
		Confirmation *string `json:"confirmation"`
	}
	if err := a.do(ctx, http.MethodPost, planPath, map[string]any{"input": input}, &plan); err != nil {
		return Change{}, fmt.Errorf("plan %s: %w", action, err)
	}
	body := map[string]any{"planId": plan.PlanID}
	if plan.Confirmation != nil {
		body["confirmation"] = *plan.Confirmation
	}
	var ch Change
	if err := a.do(ctx, http.MethodPost, applyPath, body, &ch); err != nil {
		return Change{}, fmt.Errorf("apply %s: %w", action, err)
	}
	if ch.State != core.StateCompleted {
		if ch.Error != nil {
			return ch, fmt.Errorf("harness: change %s of %s is %s: %s: %s", ch.ID, action, ch.State, ch.Error.Code, ch.Error.Message)
		}
		return ch, fmt.Errorf("harness: change %s of %s is %s, want %s", ch.ID, action, ch.State, core.StateCompleted)
	}
	if out != nil {
		if err := json.Unmarshal(ch.Output, out); err != nil {
			return ch, fmt.Errorf("harness: output of %s: %w", action, err)
		}
	}
	return ch, nil
}

// Read runs the read action, written name/v<n>, with input on host, or on
// the controller when host is empty, and decodes its output into out unless
// out is nil.
func (a *API) Read(ctx context.Context, host, action string, input, out any) error {
	path, err := actionPath(host, action, "read")
	if err != nil {
		return err
	}
	var r struct {
		Output json.RawMessage `json:"output"`
	}
	if err := a.do(ctx, http.MethodPost, path, map[string]any{"input": input}, &r); err != nil {
		return fmt.Errorf("read %s: %w", action, err)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(r.Output, out); err != nil {
		return fmt.Errorf("harness: output of %s: %w", action, err)
	}
	return nil
}

// Resource reads the resource name, written module.resource, of host.
func (a *API) Resource(ctx context.Context, host, name string, out any) error {
	path, err := resourcePath(host, name)
	if err != nil {
		return err
	}
	if err := a.do(ctx, http.MethodGet, path, nil, out); err != nil {
		return fmt.Errorf("resource %s of %s: %w", name, host, err)
	}
	return nil
}

// Hosts returns the host list, the local host first.
func (a *API) Hosts(ctx context.Context) ([]core.Host, error) {
	var l core.HostList
	if err := a.Read(ctx, "", "core.host.list/v1", struct{}{}, &l); err != nil {
		return nil, err
	}
	return l.Hosts, nil
}

// findHost returns the host with id.
func findHost(hosts []core.Host, id string) (core.Host, bool) {
	for _, h := range hosts {
		if h.ID == id {
			return h, true
		}
	}
	return core.Host{}, false
}

// localHost returns the host that runs the controller.
func localHost(hosts []core.Host) (core.Host, bool) {
	for _, h := range hosts {
		if h.IsLocal {
			return h, true
		}
	}
	return core.Host{}, false
}

// actionPath is the route of op (plan, apply or read) of action, written
// name/v<n>: under /api/v1/hosts/<host> for an agent-side action, without a
// host for a controller-side one.
func actionPath(host, action, op string) (string, error) {
	name, version, ok := strings.Cut(action, "/")
	if !ok || name == "" {
		return "", fmt.Errorf("harness: action %q: want name/v<n>, such as core.host.list/v1", action)
	}
	if _, err := core.ParseVersion(version); err != nil {
		return "", fmt.Errorf("harness: action %q: %w", action, err)
	}
	path := "/api/v1/actions/" + name + "/" + version + "/" + op
	if host != "" {
		path = "/api/v1/hosts/" + url.PathEscape(host) + path[len("/api/v1"):]
	}
	return path, nil
}

// resourcePath is the route of the resource name, written module.resource,
// of host.
func resourcePath(host, name string) (string, error) {
	module, resource, ok := strings.Cut(name, ".")
	if !ok || module == "" || resource == "" {
		return "", fmt.Errorf("harness: resource %q: want module.resource, such as hostinfo.summary", name)
	}
	return "/api/v1/hosts/" + url.PathEscape(host) + "/resources/" + module + "/" + resource, nil
}

// maxProblemBody bounds how much of an answer without problem details an
// APIError quotes.
const maxProblemBody = 512

// do sends one request with body as JSON, unless body is nil, and decodes a
// 2xx answer into out, unless out is nil.
func (a *API) do(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("harness: %s %s: %w", method, path, err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.base+path, rd)
	if err != nil {
		return fmt.Errorf("harness: %s %s: %w", method, path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if method == http.MethodPost && a.csrf != "" {
		req.Header.Set(api.CSRFHeader, a.csrf)
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return fmt.Errorf("harness: %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("harness: %s %s: %w", method, path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		e := &APIError{Status: resp.StatusCode}
		var p struct {
			Code   string `json:"code"`
			Detail string `json:"detail"`
		}
		if json.Unmarshal(data, &p) == nil && p.Code != "" {
			e.Code, e.Detail = p.Code, p.Detail
		} else {
			e.Detail = string(data[:min(len(data), maxProblemBody)])
		}
		return e
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("harness: %s %s: %w", method, path, err)
	}
	return nil
}
