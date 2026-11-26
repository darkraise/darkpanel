package api

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/darkraise/darkpanel/internal/agent/wire"
	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// Terminal limits (M11 spec 1.2 and 5).
const (
	TicketTTL           = 30 * time.Second
	MaxUserTerminals    = 8
	PongTimeout         = 60 * time.Second
	ChannelWriteTimeout = 60 * time.Second
	sessionTouchEvery   = time.Minute
	closeWriteWait      = time.Second // the close frame to the agent
	// maxBrowserMessage bounds what the WebSocket reads; a binary message
	// above wire.MaxFramePayload closes the terminal with "bad frame".
	maxBrowserMessage = 1 << 20
)

// The close reasons the bridge sends (Contracts); an agent's close frame
// carries its own.
const (
	closeRevoked     = "revoked"
	closeExpired     = "expired"
	closeBrowserGone = "browser gone"
	closeBadFrame    = "bad frame"
	closeAgentGone   = "agent restarted"
)

// ticketChannel is the one channel a ticket opens.
const ticketModule, ticketChannel = "console", "terminal"

type ticket struct {
	session, host string
	expires       time.Time
}

// longLived tracks the controller's channels: the terminal tickets and the
// open terminals per user. CloseStreams ends the channels with the SSE
// streams.
type longLived struct {
	mu        sync.Mutex
	tickets   map[string]ticket
	terminals map[string]int // by Principal.Actor
}

func (l *longLived) addTicket(t string, tk ticket, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.tickets == nil {
		l.tickets = map[string]ticket{}
	}
	for k, v := range l.tickets {
		if !now.Before(v.expires) {
			delete(l.tickets, k)
		}
	}
	l.tickets[t] = tk
}

// takeTicket spends t once, for session and host before it expires.
func (l *longLived) takeTicket(t, session, host string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	tk, ok := l.tickets[t]
	if !ok {
		return false
	}
	delete(l.tickets, t)
	return tk.session == session && tk.host == host && now.Before(tk.expires)
}

func (l *longLived) takeTerminal(actor string) (release func(), err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.terminals == nil {
		l.terminals = map[string]int{}
	}
	if l.terminals[actor] >= MaxUserTerminals {
		return nil, errcode.New(errcode.ConsoleLimit, fmt.Sprintf("at most %d open terminals per user", MaxUserTerminals))
	}
	l.terminals[actor]++
	return func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		if l.terminals[actor]--; l.terminals[actor] <= 0 {
			delete(l.terminals, actor)
		}
	}, nil
}

func errNeedsSession() error {
	return errcode.New(errcode.Forbidden, "the terminal needs a signed-in browser session")
}

// handleTerminalTicket answers a fresh authenticator code with a ticket
// that opens one terminal on the host within TicketTTL (spec 1.1).
func (s *Server) handleTerminalTicket(w http.ResponseWriter, r *http.Request) {
	c, _ := callerFrom(r.Context())
	host := r.PathValue("host")
	if c.session == "" {
		writeProblem(w, s.log, errNeedsSession())
		return
	}
	if err := authorizeHost(c.principal, host, core.ScopeAdmin); err != nil {
		writeProblem(w, s.log, err)
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := decodeBody(w, r, &body); err != nil {
		writeProblem(w, s.log, err)
		return
	}
	if body.Code == "" {
		writeProblem(w, s.log, errcode.New(errcode.StepUpRequired, "opening a terminal needs a fresh authenticator code"))
		return
	}
	if err := s.cfg.Auth.StepUp(r.Context(), c.principal, c.session, body.Code); err != nil {
		writeProblem(w, s.log, err)
		return
	}
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		writeProblem(w, s.log, err)
		return
	}
	t := base64.RawURLEncoding.EncodeToString(b[:])
	s.long.addTicket(t, ticket{session: c.session, host: host, expires: s.clock().Now().Add(TicketTTL)}, s.clock().Now())
	writeJSON(w, s.log, http.StatusOK, struct {
		Ticket string `json:"ticket"`
	}{t})
}

// channelOrigin requires the browser's Origin to be the panel's own: the
// proxied public origin or https://<Host> on TLS, http://<Host> on a
// plain-HTTP listener.
func channelOrigin(r *http.Request) error {
	want := requestOrigin(r)
	if want == "" {
		want = "http://" + r.Host
	}
	if o := r.Header.Get("Origin"); o == "" || o != want {
		return errcode.New(errcode.Forbidden, "the terminal opens only from this panel's own pages")
	}
	return nil
}

// handleChannel bridges a module channel of a host to a WebSocket (spec
// 1.2). Every refusal comes before the upgrade, as problem details.
func (s *Server) handleChannel(w http.ResponseWriter, r *http.Request) {
	c, _ := callerFrom(r.Context())
	host, module, name := r.PathValue("host"), r.PathValue("module"), r.PathValue("name")
	channels, ok := s.cfg.Dispatcher.(core.DispatcherChannels)
	if err := s.channelChecks(r, c, host, module, name, ok); err != nil {
		writeProblem(w, s.log, err)
		return
	}
	release, err := s.long.takeTerminal(c.principal.Actor)
	if err != nil {
		writeProblem(w, s.log, err)
		return
	}
	defer release()
	q := r.URL.Query()
	params := url.Values{}
	for _, k := range []string{"cols", "rows"} {
		if v, ok := q[k]; ok {
			params[k] = v
		}
	}
	agent, err := channels.OpenChannel(r.Context(), c.principal, host, module, name, params)
	if err != nil {
		writeProblem(w, s.log, err)
		return
	}
	defer func() { _ = agent.Close() }()
	// The listener's deadlines stay on the connection after the hijack;
	// each write sets its own.
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Time{})
	_ = rc.SetWriteDeadline(time.Time{})
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true, // channelOrigin checked the origin
		CompressionMode:    websocket.CompressionDisabled,
	})
	if err != nil {
		return
	}
	ws.SetReadLimit(maxBrowserMessage)
	// Cancelling a WebSocket read closes the connection at once, so the
	// close frame goes out before the reads end.
	ctx, cancel := context.WithCancel(context.WithoutCancel(r.Context()))
	defer cancel()
	b := &bridge{s: s, r: r, ws: ws, agent: agent, host: host, module: module, session: c.session,
		expires: c.identity.ExpiresAt, end: make(chan string, 1)}
	reason := b.run(ctx)
	_ = ws.Close(websocket.StatusNormalClosure, reason)
}

func (s *Server) channelChecks(r *http.Request, c caller, host, module, name string, served bool) error {
	switch {
	case !served:
		return errcode.New(errcode.NotFound, "this controller opens no channels")
	case c.session == "":
		return errNeedsSession()
	}
	if err := channelOrigin(r); err != nil {
		return err
	}
	if module != ticketModule || name != ticketChannel ||
		!s.long.takeTicket(r.URL.Query().Get("ticket"), c.session, host, s.clock().Now()) {
		return errcode.New(errcode.StepUpRequired, "opening a terminal needs a fresh ticket")
	}
	return authorizeHost(c.principal, host, core.ScopeAdmin)
}

func (s *Server) clock() core.Clock { return s.cfg.Clock }

// bridge is one terminal between a browser's WebSocket and the agent's
// channel.
type bridge struct {
	s       *Server
	r       *http.Request
	ws      *websocket.Conn
	agent   io.ReadWriteCloser
	host    string
	module  string
	session string
	expires time.Time

	amu     sync.Mutex // one frame to the agent at a time
	end     chan string
	touched time.Time
}

func (b *bridge) finish(reason string) {
	select {
	case b.end <- reason:
	default:
	}
}

func (b *bridge) toAgent(t byte, p []byte) error {
	b.amu.Lock()
	defer b.amu.Unlock()
	return wire.WriteFrame(b.agent, t, p)
}

func (b *bridge) toBrowser(ctx context.Context, typ websocket.MessageType, p []byte) error {
	wctx, cancel := context.WithTimeout(ctx, ChannelWriteTimeout)
	defer cancel()
	return b.ws.Write(wctx, typ, p)
}

// run bridges until either side ends or a check fails, then sends the
// agent the close reason, and returns it.
func (b *bridge) run(ctx context.Context) string {
	fromAgent := make(chan struct{})
	go func() {
		defer close(fromAgent)
		b.pumpAgent(ctx)
	}()
	go b.pumpBrowser(ctx)

	clock := b.s.clock()
	check, beat := make(chan struct{}, 1), make(chan struct{}, 1)
	checkT := clock.AfterFunc(SSERecheck, sseSignal(check))
	beatT := clock.AfterFunc(SSEHeartbeat, sseSignal(beat))
	var pongT core.Timer
	defer func() {
		checkT.Stop()
		beatT.Stop()
		if pongT != nil {
			pongT.Stop()
		}
	}()
	var revoked <-chan struct{}
	if p, ok := core.ProxiedFrom(b.r.Context()); ok {
		revoked = p.Revoked
	}
	pinging := false
	pongs := make(chan error, 1)
	var reason string
	for reason == "" {
		select {
		case reason = <-b.end:
		case <-check:
			reason = b.recheck(ctx)
			checkT = clock.AfterFunc(SSERecheck, sseSignal(check))
		case <-beat:
			if !pinging {
				pinging = true
				pctx, pcancel := context.WithCancel(ctx)
				// A pong not back within PongTimeout fails the ping.
				pongT = clock.AfterFunc(PongTimeout, pcancel)
				go func() {
					defer pcancel()
					pongs <- b.ws.Ping(pctx)
				}()
			}
		case err := <-pongs:
			pinging = false
			pongT.Stop()
			if err != nil {
				reason = closeBrowserGone
				break
			}
			beatT = clock.AfterFunc(SSEHeartbeat, sseSignal(beat))
		case <-revoked:
			reason = closeRevoked
		case <-b.s.streamsDone:
			reason = closeAgentGone
		}
	}
	// An agent that stopped reading cannot hold the close.
	if d, ok := b.agent.(interface{ SetWriteDeadline(time.Time) error }); ok {
		_ = d.SetWriteDeadline(time.Now().Add(closeWriteWait))
	}
	payload, _ := json.Marshal(struct {
		Reason string `json:"reason"`
	}{reason})
	_ = b.toAgent(wire.FrameClose, payload)
	_ = b.agent.Close()
	<-fromAgent
	return reason
}

// recheck re-identifies the caller passively and re-checks admin on the
// host and the module's state there: "" while all hold.
func (b *bridge) recheck(ctx context.Context) string {
	p, reason := b.s.sseRecheck(b.r, &b.expires)
	if reason != "" {
		if reason == "error" {
			return closeRevoked
		}
		return reason
	}
	if authorizeHost(p, b.host, core.ScopeAdmin) != nil {
		return closeRevoked
	}
	if ch, ok := b.s.cfg.Dispatcher.(core.DispatcherChannels); ok && ch.ModuleUsable(ctx, b.host, b.module) != nil {
		return closeRevoked
	}
	return ""
}

// pumpAgent relays the agent's frames: data as binary messages, exit as a
// text message; the agent's close frame ends the bridge with its reason.
func (b *bridge) pumpAgent(ctx context.Context) {
	for {
		t, p, err := wire.ReadFrame(b.agent)
		switch {
		case errors.Is(err, wire.ErrFrame):
			b.finish(closeBadFrame)
			return
		case err != nil:
			b.finish(closeAgentGone)
			return
		}
		switch t {
		case wire.FrameData:
			err = b.toBrowser(ctx, websocket.MessageBinary, p)
		case wire.FrameExit:
			var exit struct {
				Code int `json:"code"`
			}
			if json.Unmarshal(p, &exit) == nil {
				msg, _ := json.Marshal(struct {
					Type string `json:"type"`
					Code int    `json:"code"`
				}{"exit", exit.Code})
				err = b.toBrowser(ctx, websocket.MessageText, msg)
			}
		case wire.FrameClose:
			var cl struct {
				Reason string `json:"reason"`
			}
			if json.Unmarshal(p, &cl) != nil || cl.Reason == "" {
				cl.Reason = closeAgentGone
			}
			b.finish(cl.Reason)
			return
		}
		if err != nil {
			b.finish(closeBrowserGone)
			return
		}
	}
}

// pumpBrowser relays the browser's messages: binary as data frames, a
// text resize message as a resize frame; anything else is a bad frame.
func (b *bridge) pumpBrowser(ctx context.Context) {
	for {
		typ, p, err := b.ws.Read(ctx)
		if err != nil {
			b.finish(closeBrowserGone)
			return
		}
		if typ == websocket.MessageBinary {
			if len(p) > wire.MaxFramePayload {
				b.finish(closeBadFrame)
				return
			}
			if err := b.toAgent(wire.FrameData, p); err != nil {
				b.finish(closeAgentGone)
				return
			}
			b.touch(ctx)
			continue
		}
		var m struct {
			Type string `json:"type"`
			Cols uint16 `json:"cols"`
			Rows uint16 `json:"rows"`
		}
		if json.Unmarshal(p, &m) != nil || m.Type != "resize" || m.Cols < 20 || m.Cols > 500 || m.Rows < 5 || m.Rows > 200 {
			b.finish(closeBadFrame)
			return
		}
		size, _ := json.Marshal(struct {
			Cols uint16 `json:"cols"`
			Rows uint16 `json:"rows"`
		}{m.Cols, m.Rows})
		if err := b.toAgent(wire.FrameResize, size); err != nil {
			b.finish(closeAgentGone)
			return
		}
	}
}

// touch counts a data frame as session activity at most once a minute.
func (b *bridge) touch(ctx context.Context) {
	now := b.s.clock().Now()
	if !b.touched.IsZero() && now.Sub(b.touched) < sessionTouchEvery {
		return
	}
	b.touched = now
	_, err := b.s.cfg.Auth.Authenticate(ctx, auth.Credentials{Session: b.session, Source: clientAddr(b.r)})
	if err != nil && errcode.From(err).Code != errcode.AuthRequired {
		b.s.log.Warn("api: touch a terminal's session", "err", err)
	}
}
