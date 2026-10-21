package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	agentruntime "github.com/darkraise/darkpanel/internal/agent/runtime"
	"github.com/darkraise/darkpanel/internal/agent/wire"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// ClientConfig is where a client sends its requests.
type ClientConfig struct {
	// Dial opens one connection to the agent API: a transport.Session's
	// Open, or a dial of the agent socket.
	Dial func(ctx context.Context) (net.Conn, error)
	// Controller is sent in the headers of GET hello; nil sends none, as a
	// client of the agent socket does. Its Protocol is ignored: the client
	// always sends this binary's protocol.
	Controller *ControllerHello
}

// controllerPrincipal is what a controller's client sends as
// Darkpanel-Principal on the routes whose method names no caller: the
// protocol sends the header on every request (ADR 0012), and the agent reads
// it only where it acts for a caller, so this principal is never recorded.
var controllerPrincipal = core.Principal{Actor: "system:controller", Scope: core.ScopeAdmin}

// Client is a core.AgentClient over the agent API.
type Client struct {
	cfg  ClientConfig
	http *http.Client
}

var _ core.AgentClient = (*Client)(nil)

// NewClient returns a client for cfg. Requests end with their context; the
// client sets no timeout of its own.
func NewClient(cfg ClientConfig) *Client {
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return cfg.Dial(ctx) },
		// Each request opens a stream and closes it: an idle pooled stream
		// would keep one of the session's stream slots.
		DisableKeepAlives:      true,
		DisableCompression:     true,
		MaxResponseHeaderBytes: 64 << 10,
	}
	return &Client{cfg: cfg, http: &http.Client{Transport: tr}}
}

// request is one call of the agent API.
type request struct {
	method, path string
	query        url.Values
	principal    *core.Principal
	body         any // nil sends none; a json.RawMessage is sent as it is
	hello        bool
}

// do sends req and decodes a 2xx answer into out (nil discards it). An
// answer in problem details becomes an *errcode.Error with the agent's code,
// detail and field errors.
func (c *Client) do(ctx context.Context, req request, out any) error {
	what := req.method + " " + req.path
	var body io.Reader
	if req.body != nil {
		b, ok := req.body.(json.RawMessage)
		if !ok {
			var err error
			if b, err = json.Marshal(req.body); err != nil {
				return fmt.Errorf("api: %s: encode the request: %w", what, err)
			}
		}
		if len(b) == 0 {
			b = json.RawMessage("null")
		}
		body = bytes.NewReader(b)
	}
	u := "http://agent" + prefix + req.path
	if len(req.query) > 0 {
		u += "?" + req.query.Encode()
	}
	hr, err := http.NewRequestWithContext(ctx, req.method, u, body)
	if err != nil {
		return fmt.Errorf("api: %s: %w", what, err)
	}
	hr.Header.Set("Accept", "application/json")
	if body != nil {
		hr.Header.Set("Content-Type", "application/json")
	}
	principal := req.principal
	if principal == nil && c.cfg.Controller != nil {
		principal = &controllerPrincipal
	}
	if principal != nil {
		b, err := json.Marshal(wire.FromPrincipal(*principal))
		if err != nil {
			return fmt.Errorf("api: %s: encode the principal: %w", what, err)
		}
		hr.Header.Set(wire.HeaderPrincipal, string(b))
	}
	if req.hello && c.cfg.Controller != nil {
		hr.Header.Set(wire.HeaderProtocol, strconv.Itoa(agentruntime.ProtocolVersion))
		hr.Header.Set(wire.HeaderVersion, c.cfg.Controller.Version)
		hr.Header.Set(wire.HeaderControllerID, c.cfg.Controller.ControllerID)
	}
	resp, err := c.http.Do(hr)
	if err != nil {
		return fmt.Errorf("api: %s: %w", what, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, wire.MaxBody+1))
	if err != nil {
		return fmt.Errorf("api: %s: read the answer: %w", what, err)
	}
	if len(data) > wire.MaxBody {
		return fmt.Errorf("api: %s: the answer exceeds %d bytes", what, wire.MaxBody)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return answerError(what, resp, data)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("api: %s: decode the answer: %w", what, err)
	}
	return nil
}

// answerError turns a non-2xx answer into an *errcode.Error when it carries
// problem details with a code, else into a plain error naming the status.
// The code is kept even when this binary does not know it.
func answerError(what string, resp *http.Response, data []byte) error {
	if mt, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type")); err == nil && mt == problemContentType {
		var p problem
		if json.Unmarshal(data, &p) == nil && p.Code != "" {
			return &errcode.Error{Code: p.Code, Detail: p.Detail, Fields: p.Errors}
		}
	}
	return fmt.Errorf("api: %s: unexpected answer %s", what, resp.Status)
}

// actionPath is the route of op on action a.
func actionPath(a core.ActionRef, op string) string {
	return "actions/" + url.PathEscape(a.Name) + "/v" + strconv.Itoa(a.Version) + "/" + op
}

// rawOutput maps JSON null to nil, as the runtime returns no output.
func rawOutput(m json.RawMessage) json.RawMessage {
	if bytes.Equal(m, []byte("null")) {
		return nil
	}
	return m
}

// Hello asks the agent about itself, sending the controller's half when the
// client has one.
func (c *Client) Hello(ctx context.Context) (core.AgentHello, error) {
	var h wire.Hello
	if err := c.do(ctx, request{method: http.MethodGet, path: "hello", hello: true}, &h); err != nil {
		return core.AgentHello{}, err
	}
	return h.Core(), nil
}

// Read runs an agent read action.
func (c *Client) Read(ctx context.Context, p core.Principal, a core.ActionRef, input json.RawMessage) (json.RawMessage, error) {
	var out json.RawMessage
	err := c.do(ctx, request{method: http.MethodPost, path: actionPath(a, opRead), principal: &p, body: input}, &out)
	if err != nil {
		return nil, err
	}
	return rawOutput(out), nil
}

// Resource reads a resource named "<module>.<resource>".
func (c *Client) Resource(ctx context.Context, p core.Principal, name string) (json.RawMessage, error) {
	module, resource, ok := strings.Cut(name, ".")
	if !ok {
		return nil, errcode.New(errcode.NotFound, "no resource "+name)
	}
	var out json.RawMessage
	path := "resources/" + url.PathEscape(module) + "/" + url.PathEscape(resource)
	if err := c.do(ctx, request{method: http.MethodGet, path: path, principal: &p}, &out); err != nil {
		return nil, err
	}
	return rawOutput(out), nil
}

// Plan plans an agent action.
func (c *Client) Plan(ctx context.Context, p core.Principal, a core.ActionRef, input json.RawMessage, _ core.PlanOptions) (core.PlanView, error) {
	var v wire.PlanView
	if err := c.do(ctx, request{method: http.MethodPost, path: actionPath(a, opPlan), principal: &p, body: input}, &v); err != nil {
		return core.PlanView{}, err
	}
	return v.Core(), nil
}

// Apply applies a plan.
func (c *Client) Apply(ctx context.Context, p core.Principal, planID string, opts core.ApplyOptions) (core.ChangeView, error) {
	return c.change(ctx, request{method: http.MethodPost, path: "plans/" + url.PathEscape(planID) + "/apply", principal: &p,
		body: wire.FromApplyOptions(opts)})
}

// Confirm confirms a pending change.
func (c *Client) Confirm(ctx context.Context, p core.Principal, changeID string) (core.ChangeView, error) {
	return c.change(ctx, request{method: http.MethodPost, path: "changes/" + url.PathEscape(changeID) + "/confirm", principal: &p})
}

// Revert reverts a pending change.
func (c *Client) Revert(ctx context.Context, p core.Principal, changeID string) (core.ChangeView, error) {
	return c.change(ctx, request{method: http.MethodPost, path: "changes/" + url.PathEscape(changeID) + "/revert", principal: &p})
}

// Change returns one change.
func (c *Client) Change(ctx context.Context, changeID string) (core.ChangeView, error) {
	return c.change(ctx, request{method: http.MethodGet, path: "changes/" + url.PathEscape(changeID)})
}

func (c *Client) change(ctx context.Context, req request) (core.ChangeView, error) {
	var v wire.ChangeView
	if err := c.do(ctx, req, &v); err != nil {
		return core.ChangeView{}, err
	}
	return v.Core(), nil
}

// Changes lists changes matching f.
func (c *Client) Changes(ctx context.Context, f core.ChangeFilter) ([]core.ChangeView, error) {
	var vs []wire.ChangeView
	if err := c.do(ctx, request{method: http.MethodGet, path: "changes", query: wire.ChangeFilterQuery(f)}, &vs); err != nil {
		return nil, err
	}
	out := make([]core.ChangeView, len(vs))
	for i, v := range vs {
		out[i] = v.Core()
	}
	return out, nil
}

// Preflight runs a module's preflight checks again.
func (c *Client) Preflight(ctx context.Context, moduleID string) (core.ModuleStatus, error) {
	var st wire.ModuleStatus
	if err := c.do(ctx, request{method: http.MethodGet, path: "modules/" + url.PathEscape(moduleID) + "/preflight"}, &st); err != nil {
		return core.ModuleStatus{}, err
	}
	return st.Core(), nil
}

// Journal returns up to limit journal entries after afterSeq; a limit of 0
// leaves the count to the agent.
func (c *Client) Journal(ctx context.Context, afterSeq int64, limit int) ([]core.JournalEntry, error) {
	q := url.Values{"after": {strconv.FormatInt(afterSeq, 10)}}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	var es []wire.JournalEntry
	if err := c.do(ctx, request{method: http.MethodGet, path: "journal", query: q}, &es); err != nil {
		return nil, err
	}
	out := make([]core.JournalEntry, len(es))
	for i, e := range es {
		out[i] = e.Core()
	}
	return out, nil
}

// Ack tells the agent the controller stored the journal up to seq.
func (c *Client) Ack(ctx context.Context, seq int64) error {
	return c.do(ctx, request{method: http.MethodPost, path: "journal/ack", body: wire.Ack{Seq: seq}}, nil)
}
