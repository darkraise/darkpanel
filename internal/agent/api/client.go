package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

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
	// DialStream opens the connection of one module stream: a
	// transport.Session's OpenModule, so streams never take the slots of
	// actions. nil uses Dial.
	DialStream func(ctx context.Context) (net.Conn, error)
	// Controller is sent in the headers of GET hello; nil sends none, as a
	// client of the agent socket does. Its Protocol is ignored: the client
	// always sends this binary's protocol.
	Controller *ControllerHello
	// HostID is the host the client talks to; when set, the principal header
	// carries the caller's scope there (wire.Principal.HostScope).
	HostID string
}

// controllerPrincipal is what a controller's client sends as
// Darkpanel-Principal on the routes whose method names no caller: the
// protocol sends the header on every request (ADR 0012), and the agent reads
// it only where it acts for a caller, so this principal is never recorded.
var controllerPrincipal = core.Principal{Actor: "system:controller", Scope: core.ScopeAdmin}

// Client is a core.AgentClient over the agent API.
type Client struct {
	cfg    ClientConfig
	http   *http.Client
	stream *http.Client // module streams, over DialStream
}

var (
	_ core.AgentClient       = (*Client)(nil)
	_ core.AgentChangeOps    = (*Client)(nil)
	_ core.AgentEnablement   = (*Client)(nil)
	_ core.AgentAvailability = (*Client)(nil)
	_ core.AgentReveal       = (*Client)(nil)
)

// NewClient returns a client for cfg. Requests end with their context; the
// client sets no timeout of its own.
func NewClient(cfg ClientConfig) *Client {
	dialStream := cfg.DialStream
	if dialStream == nil {
		dialStream = cfg.Dial
	}
	return &Client{cfg: cfg, http: &http.Client{Transport: transportOver(cfg.Dial)},
		stream: &http.Client{Transport: transportOver(dialStream)}}
}

// transportOver sends each request on its own connection from dial.
func transportOver(dial func(ctx context.Context) (net.Conn, error)) *http.Transport {
	return &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return dial(ctx) },
		// Each request opens a stream and closes it: an idle pooled stream
		// would keep one of the session's stream slots.
		DisableKeepAlives:      true,
		DisableCompression:     true,
		MaxResponseHeaderBytes: 64 << 10,
	}
}

// request is one call of the agent API.
type request struct {
	method, path string
	query        url.Values
	principal    *core.Principal
	call         *core.CallContext // sent as Darkpanel-Call when set
	body         any               // nil sends none; a json.RawMessage is sent as it is
	hello        bool
	internal     bool // sends Darkpanel-Internal: 1
}

// do sends req and decodes a 2xx answer into out (nil discards it); 202 is
// an answer like 200, and 204 has nothing to decode. An answer in problem
// details becomes an *errcode.Error with the agent's code, detail and field
// errors.
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
		b, err := json.Marshal(c.wirePrincipal(*principal))
		if err != nil {
			return fmt.Errorf("api: %s: encode the principal: %w", what, err)
		}
		hr.Header.Set(wire.HeaderPrincipal, string(b))
	}
	if req.call != nil {
		b, err := json.Marshal(wire.FromCall(*req.call))
		if err != nil {
			return fmt.Errorf("api: %s: encode the call: %w", what, err)
		}
		hr.Header.Set(wire.HeaderCall, base64.RawURLEncoding.EncodeToString(b))
	}
	if req.internal {
		hr.Header.Set(wire.HeaderInternal, "1")
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
	if out == nil || resp.StatusCode == http.StatusNoContent {
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

// Reveal runs a reveal action, or with internal an Internal read, which
// travels with Darkpanel-Internal: 1 (spec 1.7). The value comes back as
// the agent answered it.
func (c *Client) Reveal(ctx context.Context, p core.Principal, a core.ActionRef, input json.RawMessage, internal bool) (json.RawMessage, error) {
	var out json.RawMessage
	path := "reveal/" + url.PathEscape(a.Name) + "/v" + strconv.Itoa(a.Version)
	if err := c.do(ctx, request{method: http.MethodPost, path: path, principal: &p, body: input, internal: internal}, &out); err != nil {
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

// Plan plans an agent action; opts.Call, when set, travels as
// Darkpanel-Call.
func (c *Client) Plan(ctx context.Context, p core.Principal, a core.ActionRef, input json.RawMessage, opts core.PlanOptions) (core.PlanView, error) {
	var v wire.PlanView
	req := request{method: http.MethodPost, path: actionPath(a, opPlan), principal: &p, body: input, call: opts.Call}
	if err := c.do(ctx, req, &v); err != nil {
		return core.PlanView{}, err
	}
	return v.Core(), nil
}

// Apply applies a plan; opts.Call, when set, travels as Darkpanel-Call.
func (c *Client) Apply(ctx context.Context, p core.Principal, planID string, opts core.ApplyOptions) (core.ChangeView, error) {
	return c.change(ctx, request{method: http.MethodPost, path: "plans/" + url.PathEscape(planID) + "/apply", principal: &p,
		body: wire.FromApplyOptions(opts), call: opts.Call})
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

// SetModuleEnabled pushes the controller's enabled flag of a module.
func (c *Client) SetModuleEnabled(ctx context.Context, moduleID string, enabled bool) error {
	req := request{method: http.MethodPut, path: "modules/" + url.PathEscape(moduleID) + "/enabled",
		body: wire.ModuleEnabled{Enabled: enabled}}
	return c.do(ctx, req, nil)
}

// ReloadPolicy asks the agent to read its host-local policy file again; only
// the agent socket serves it.
func (c *Client) ReloadPolicy(ctx context.Context) error {
	return c.do(ctx, request{method: http.MethodPost, path: "policy/reload"}, nil)
}

// Availability asks which actions of a module are available. An agent
// without the route answers not_found, as for an unknown module.
func (c *Client) Availability(ctx context.Context, moduleID string) (core.ModuleAvailability, error) {
	var m wire.ModuleAvailability
	path := "modules/" + url.PathEscape(moduleID) + "/availability"
	if err := c.do(ctx, request{method: http.MethodGet, path: path}, &m); err != nil {
		return core.ModuleAvailability{}, err
	}
	return m.Core(), nil
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

// changeOpPath is the route of op on change changeID.
func changeOpPath(changeID, op string) string {
	return "changes/" + url.PathEscape(changeID) + "/" + op
}

// Undo undoes a change; call, when set, travels as Darkpanel-Call. The agent
// answers 202 with the change undoing, or 200 when it is already reverted.
func (c *Client) Undo(ctx context.Context, p core.Principal, changeID string, call *core.CallContext) (core.ChangeView, error) {
	return c.change(ctx, request{method: http.MethodPost, path: changeOpPath(changeID, "undo"), principal: &p, call: call})
}

// Repair repairs a change in needs_repair: the answer holds the change the
// repair started or the plan it made.
func (c *Client) Repair(ctx context.Context, p core.Principal, changeID string, req core.RepairRequest) (core.RepairResult, error) {
	var res wire.RepairResult
	if err := c.do(ctx, request{method: http.MethodPost, path: changeOpPath(changeID, "repair"), principal: &p, body: req}, &res); err != nil {
		return core.RepairResult{}, err
	}
	return res.Core(), nil
}

// Release gives up a change in needs_repair; call, when set, travels as
// Darkpanel-Call.
func (c *Client) Release(ctx context.Context, p core.Principal, changeID, reason string, call *core.CallContext) (core.ReleaseResult, error) {
	var res wire.ReleaseResult
	req := request{method: http.MethodPost, path: changeOpPath(changeID, "release"), principal: &p, call: call,
		body: wire.Release{Reason: reason}}
	if err := c.do(ctx, req, &res); err != nil {
		return core.ReleaseResult{}, err
	}
	return res.Core(), nil
}

// Effects lists a change's effects.
func (c *Client) Effects(ctx context.Context, p core.Principal, changeID string) ([]core.EffectView, error) {
	var res wire.Effects
	if err := c.do(ctx, request{method: http.MethodGet, path: changeOpPath(changeID, "effects"), principal: &p}, &res); err != nil {
		return nil, err
	}
	return res.Effects, nil
}

// Observe reads one effect's resource now.
func (c *Client) Observe(ctx context.Context, p core.Principal, changeID string, seq int) (core.ObserveResult, error) {
	var res core.ObserveResult
	path := changeOpPath(changeID, "effects/"+strconv.Itoa(seq)+"/observe")
	if err := c.do(ctx, request{method: http.MethodGet, path: path, principal: &p}, &res); err != nil {
		return core.ObserveResult{}, err
	}
	return res, nil
}

// Resolve resolves an unresolved effect.
func (c *Client) Resolve(ctx context.Context, p core.Principal, changeID string, seq int, req core.ResolveRequest) (core.EffectView, error) {
	var res core.EffectView
	path := changeOpPath(changeID, "effects/"+strconv.Itoa(seq)+"/resolve")
	if err := c.do(ctx, request{method: http.MethodPost, path: path, principal: &p, body: req}, &res); err != nil {
		return core.EffectView{}, err
	}
	return res, nil
}

// Lookup reports what became of a plan; private asks for a child change's
// unredacted output too.
func (c *Client) Lookup(ctx context.Context, planID string, private bool) (core.PlanLookup, error) {
	var q url.Values
	if private {
		q = url.Values{"private": {"1"}}
	}
	var res core.PlanLookup
	if err := c.do(ctx, request{method: http.MethodGet, path: "plans/" + url.PathEscape(planID), query: q}, &res); err != nil {
		return core.PlanLookup{}, err
	}
	return res, nil
}

// Retire marks a plan without a change as never to be applied.
func (c *Client) Retire(ctx context.Context, planID string) (core.PlanLookup, error) {
	var res core.PlanLookup
	if err := c.do(ctx, request{method: http.MethodPost, path: "plans/" + url.PathEscape(planID) + "/retire"}, &res); err != nil {
		return core.PlanLookup{}, err
	}
	return res, nil
}

// AckPrivate tells the agent the composite sealed a change's private output;
// final says the composite is terminal.
func (c *Client) AckPrivate(ctx context.Context, changeID string, final bool) error {
	req := request{method: http.MethodPost, path: changeOpPath(changeID, "private-output/ack")}
	if final {
		req.body = wire.AckPrivate{Final: true}
	}
	return c.do(ctx, req, nil)
}

var _ core.AgentStreams = (*Client)(nil)

// Streams lists the agent's module streams. An agent without the route
// answers not_found, which is an agent with no streams (spec 1.6).
func (c *Client) Streams(ctx context.Context) ([]core.StreamInfo, error) {
	var res wire.Streams
	err := c.do(ctx, request{method: http.MethodGet, path: "streams"}, &res)
	var e *errcode.Error
	if errors.As(err, &e) && e.Code == errcode.NotFound {
		return []core.StreamInfo{}, nil
	}
	if err != nil {
		return nil, err
	}
	if res.Streams == nil {
		res.Streams = []core.StreamInfo{}
	}
	return res.Streams, nil
}

// OpenStream opens module's stream name with params as p, on a connection
// of DialStream, and returns its body of JSON lines. The body is not
// limited to wire.MaxBody: a stream lasts until it ends or the body is
// closed, and closing it closes the connection, which ends the stream on
// the agent.
func (c *Client) OpenStream(ctx context.Context, p core.Principal, module, name string, params url.Values) (io.ReadCloser, error) {
	path := "streams/" + url.PathEscape(module) + "/" + url.PathEscape(name)
	what := http.MethodGet + " " + path
	u := "http://agent" + prefix + path
	if len(params) > 0 {
		u += "?" + params.Encode()
	}
	hr, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil) //nolint:gosec // G704: the host is the constant "agent"; DialStream chooses the connection
	if err != nil {
		return nil, fmt.Errorf("api: %s: %w", what, err)
	}
	b, err := json.Marshal(c.wirePrincipal(p))
	if err != nil {
		return nil, fmt.Errorf("api: %s: encode the principal: %w", what, err)
	}
	hr.Header.Set(wire.HeaderPrincipal, string(b))
	hr.Header.Set("Accept", "application/x-ndjson")
	resp, err := c.stream.Do(hr) //nolint:gosec // G704: as above, the request can only reach the agent DialStream connects to
	if err != nil {
		return nil, fmt.Errorf("api: %s: %w", what, err)
	}
	if resp.StatusCode == http.StatusOK {
		return resp.Body, nil
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, wire.MaxBody))
	if err != nil {
		return nil, fmt.Errorf("api: %s: read the answer: %w", what, err)
	}
	return nil, answerError(what, resp, data)
}

// wirePrincipal is p as the principal header carries it.
func (c *Client) wirePrincipal(p core.Principal) wire.Principal {
	if c.cfg.HostID == "" {
		return wire.FromPrincipal(p)
	}
	return wire.FromPrincipalOn(p, c.cfg.HostID)
}

// OpenChannel opens module's channel name with params as p, on a connection
// of DialStream, and returns it once the agent answered 101. The stream
// carries frames (wire.WriteFrame, wire.ReadFrame) and lasts until either
// side closes it. Its reads hand over whole frames only: a frame the codec
// refuses sends the agent a close frame with reason "bad frame", closes the
// connection and fails the read with wire.ErrFrame. A refusal is the
// agent's error.
func (c *Client) OpenChannel(ctx context.Context, p core.Principal, module, name string, params url.Values) (io.ReadWriteCloser, error) {
	path := "channels/" + url.PathEscape(module) + "/" + url.PathEscape(name)
	what := http.MethodGet + " " + path
	u := "http://agent" + prefix + path
	if len(params) > 0 {
		u += "?" + params.Encode()
	}
	hr, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("api: %s: %w", what, err)
	}
	b, err := json.Marshal(c.wirePrincipal(p))
	if err != nil {
		return nil, fmt.Errorf("api: %s: encode the principal: %w", what, err)
	}
	hr.Header.Set(wire.HeaderPrincipal, string(b))
	hr.Header.Set("Connection", "Upgrade")
	hr.Header.Set("Upgrade", wire.ChannelUpgrade)
	dial := c.cfg.DialStream
	if dial == nil {
		dial = c.cfg.Dial
	}
	conn, err := dial(ctx)
	if err != nil {
		return nil, fmt.Errorf("api: %s: %w", what, err)
	}
	// The handshake ends with ctx; the channel itself is ended by Close.
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	br := bufio.NewReader(conn)
	var resp *http.Response
	if err = hr.Write(conn); err == nil {
		resp, err = http.ReadResponse(br, hr)
	}
	if !stop() {
		_ = conn.Close()
		return nil, fmt.Errorf("api: %s: %w", what, ctx.Err())
	}
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("api: %s: %w", what, err)
	}
	if resp.StatusCode == http.StatusSwitchingProtocols && headerHas(resp.Header, "Upgrade", wire.ChannelUpgrade) {
		return &clientChannel{conn: conn, br: br}, nil
	}
	defer func() { _ = conn.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, wire.MaxBody))
	if err != nil {
		return nil, fmt.Errorf("api: %s: read the answer: %w", what, err)
	}
	if resp.StatusCode == http.StatusSwitchingProtocols {
		return nil, fmt.Errorf("api: %s: the agent switched to %q, not %s", what, resp.Header.Get("Upgrade"), wire.ChannelUpgrade)
	}
	return nil, answerError(what, resp, data)
}

// clientChannel is the controller's end of a channel.
type clientChannel struct {
	conn net.Conn
	br   *bufio.Reader

	rmu     sync.Mutex
	pending []byte // the rest of the frame being read
	err     error  // the read error that ended the channel

	wmu sync.Mutex
}

func (c *clientChannel) Read(p []byte) (int, error) {
	c.rmu.Lock()
	defer c.rmu.Unlock()
	if len(c.pending) == 0 {
		if c.err != nil {
			return 0, c.err
		}
		t, payload, err := wire.ReadFrame(c.br)
		if err != nil {
			if errors.Is(err, wire.ErrFrame) {
				c.closeBadFrame()
			}
			c.err = err
			return 0, err
		}
		var buf bytes.Buffer
		_ = wire.WriteFrame(&buf, t, payload) // ReadFrame accepted it, so it encodes
		c.pending = buf.Bytes()
	}
	n := copy(p, c.pending)
	c.pending = c.pending[n:]
	return n, nil
}

// closeBadFrame tells the agent why, waiting at most a second for a peer
// that stopped reading, and closes the connection.
func (c *clientChannel) closeBadFrame() {
	_ = c.conn.SetWriteDeadline(time.Now().Add(time.Second))
	c.wmu.Lock()
	_ = wire.WriteFrame(c.conn, wire.FrameClose, []byte(`{"reason":"bad frame"}`))
	c.wmu.Unlock()
	_ = c.conn.Close()
}

func (c *clientChannel) Write(p []byte) (int, error) {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return c.conn.Write(p)
}

func (c *clientChannel) Close() error { return c.conn.Close() }
