package api

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/darkraise/darkpanel/internal/agent/wire"
	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

const termHost = "h1"

// termUser is a signed-in browser of the rig.
type termUser struct {
	name, secret, session, csrf string
}

// termRig serves the API on a real listener, so terminals upgrade to
// WebSockets; the fake dispatcher hands the agent's end of each channel to
// agents.
type termRig struct {
	t       *testing.T
	h       *harness
	ts      *httptest.Server
	admin   termUser
	agents  chan net.Conn
	opened  atomic.Int32
	proxied bool
	revoke  chan struct{}
}

func newTermRig(t *testing.T, proxied bool, timeouts time.Duration) *termRig {
	t.Helper()
	h := newHarness(t)
	h.cfg.Clock = h.clock
	srv, err := New(h.cfg)
	if err != nil {
		t.Fatal(err)
	}
	h.srv = srv
	r := &termRig{t: t, h: h, agents: make(chan net.Conn, 16), proxied: proxied, revoke: make(chan struct{})}
	h.disp.OpenChannelFunc = func(_ context.Context, _ core.Principal, host, module, name string, params url.Values) (io.ReadWriteCloser, error) {
		if host != termHost || module != "console" || name != "terminal" || params.Get("cols") != "80" || params.Get("rows") != "24" {
			return nil, errcode.New(errcode.InvalidInput, "unexpected channel request")
		}
		r.opened.Add(1)
		a, b := net.Pipe()
		r.agents <- b
		return a, nil
	}
	r.ts = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if r.proxied {
			req = req.WithContext(core.WithProxied(req.Context(), core.Proxied{ListenerID: "l1", PublicHost: "panel.example", Revoked: r.revoke}))
		}
		h.srv.Handler().ServeHTTP(w, req)
	}))
	if timeouts > 0 {
		r.ts.Config.ReadTimeout, r.ts.Config.WriteTimeout = timeouts, timeouts
	}
	r.ts.Start()
	t.Cleanup(r.ts.Close)
	session, csrf := h.login()
	r.admin = termUser{name: "admin", secret: h.secret, session: session, csrf: csrf}
	return r
}

// addUser creates, activates and signs in another user with role.
func (r *termRig) addUser(name, role string) termUser {
	r.t.Helper()
	ctx := context.Background()
	_, secret, err := r.h.auth.CreateUser(ctx, name, testPassword)
	if err != nil {
		r.t.Fatal(err)
	}
	u := termUser{name: name, secret: secret}
	if _, err := r.h.auth.ActivateUser(ctx, name, r.code(u)); err != nil {
		r.t.Fatal(err)
	}
	if _, err := r.h.auth.SetRole(ctx, name, role); err != nil {
		r.t.Fatal(err)
	}
	res, err := r.h.auth.Login(ctx, auth.LoginRequest{Username: name, Password: testPassword, Code: r.code(u), Source: "192.0.2.10"})
	if err != nil {
		r.t.Fatal(err)
	}
	u.session, u.csrf = res.SessionID, res.CSRFToken
	return u
}

// code advances the clock a TOTP period and returns u's code.
func (r *termRig) code(u termUser) string {
	r.t.Helper()
	r.h.clock.Advance(auth.TOTPPeriod)
	c, err := auth.TOTPCode(u.secret, r.h.clock.Now())
	if err != nil {
		r.t.Fatal(err)
	}
	return c
}

func ticketPath(host string) string {
	return "/api/v1/hosts/" + host + "/channels/console/terminal/ticket"
}

func (r *termRig) postTicket(u termUser, body string) *httptest.ResponseRecorder {
	return r.h.serve(withSession(request(http.MethodPost, ticketPath(termHost), body), u.session, u.csrf))
}

// ticket gets a ticket for u with a fresh code.
func (r *termRig) ticket(u termUser) string {
	r.t.Helper()
	rec := r.postTicket(u, `{"code":"`+r.code(u)+`"}`)
	if rec.Code != http.StatusOK {
		r.t.Fatalf("ticket: %d %s", rec.Code, rec.Body)
	}
	var b struct{ Ticket string }
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		r.t.Fatal(err)
	}
	if raw, err := base64.RawURLEncoding.DecodeString(b.Ticket); err != nil || len(raw) != 32 {
		r.t.Fatalf("ticket %q is not 32 bytes of base64url", b.Ticket)
	}
	return b.Ticket
}

func (r *termRig) origin() string {
	if r.proxied {
		return "https://panel.example"
	}
	return "http://" + strings.TrimPrefix(r.ts.URL, "http://")
}

// dial opens a terminal as u with tk; on a refusal it returns the problem
// code. opts may set the dial options.
func (r *termRig) dial(u termUser, host, tk string, opts *websocket.DialOptions) (*websocket.Conn, net.Conn, string) {
	r.t.Helper()
	u2 := "ws" + strings.TrimPrefix(r.ts.URL, "http") + "/api/v1/hosts/" + host + "/channels/console/terminal?ticket=" + url.QueryEscape(tk) + "&cols=80&rows=24"
	if opts == nil {
		opts = &websocket.DialOptions{}
	}
	opts.HTTPHeader = http.Header{"Cookie": {SessionCookiePlain + "=" + u.session}, "Origin": {r.origin()}}
	base := r.h.clock.Pending()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ws, res, err := websocket.Dial(ctx, u2, opts)
	if err != nil {
		var p struct{ Code string }
		if res != nil {
			_ = json.NewDecoder(res.Body).Decode(&p)
		}
		return nil, nil, p.Code
	}
	r.t.Cleanup(func() { _ = ws.CloseNow() })
	var agent net.Conn
	select {
	case agent = <-r.agents:
	case <-time.After(5 * time.Second):
		r.t.Fatal("the bridge opened no agent channel")
	}
	r.t.Cleanup(func() { _ = agent.Close() })
	// The bridge arms its recheck and heartbeat once it runs.
	deadline := time.Now().Add(5 * time.Second)
	for r.h.clock.Pending() < base+2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	return ws, agent, ""
}

// keep reads ws in the background, so its pings are answered.
func keep(t *testing.T, ws *websocket.Conn) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		for {
			if _, _, err := ws.Read(ctx); err != nil {
				return
			}
		}
	}()
}

// closeReason reads ws until it closes and returns the close reason.
func closeReason(t *testing.T, ws *websocket.Conn) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		_, _, err := ws.Read(ctx)
		if err == nil {
			continue
		}
		var ce websocket.CloseError
		if errors.As(err, &ce) {
			return ce.Reason
		}
		t.Fatalf("the terminal ended without a close: %v", err)
		return ""
	}
}

// agentClose reads the agent's end until the bridge's close frame.
func agentClose(t *testing.T, agent net.Conn) string {
	t.Helper()
	_ = agent.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		typ, p, err := wire.ReadFrame(agent)
		if err != nil {
			t.Fatalf("the agent got no close frame: %v", err)
		}
		if typ == wire.FrameClose {
			var c struct{ Reason string }
			_ = json.Unmarshal(p, &c)
			return c.Reason
		}
	}
}

func TestTicket(t *testing.T) {
	r := newTermRig(t, false, 0)
	tk := r.ticket(r.admin)
	if _, _, code := r.dial(r.admin, termHost, tk, nil); code != "" {
		t.Fatalf("a fresh ticket was refused: %s", code)
	}
	if _, _, code := r.dial(r.admin, termHost, tk, nil); code != string(errcode.StepUpRequired) {
		t.Errorf("a spent ticket: %q, want %s", code, errcode.StepUpRequired)
	}
	late := r.ticket(r.admin)
	r.h.clock.Advance(TicketTTL)
	if _, _, code := r.dial(r.admin, termHost, late, nil); code != string(errcode.StepUpRequired) {
		t.Errorf("a ticket 30 s old: %q, want %s", code, errcode.StepUpRequired)
	}
	other := r.addUser("bob", auth.RoleAdmin)
	if _, _, code := r.dial(other, termHost, r.ticket(r.admin), nil); code != string(errcode.StepUpRequired) {
		t.Errorf("another session's ticket: %q, want %s", code, errcode.StepUpRequired)
	}
	if _, _, code := r.dial(r.admin, "h2", r.ticket(r.admin), nil); code != string(errcode.StepUpRequired) {
		t.Errorf("another host's ticket: %q, want %s", code, errcode.StepUpRequired)
	}
	wantProblem(t, "no code", r.postTicket(r.admin, `{}`), http.StatusForbidden, errcode.StepUpRequired)
	r.h.clock.Advance(auth.TOTPPeriod)
	wantProblem(t, "wrong code", r.postTicket(r.admin, `{"code":"`+r.h.wrongCode()+`"}`), http.StatusUnauthorized, errcode.InvalidCredentials)
	tok := r.h.token(core.ScopeAdmin)
	wantProblem(t, "a token", r.h.serve(withToken(request(http.MethodPost, ticketPath(termHost), `{"code":"123456"}`), tok)),
		http.StatusForbidden, errcode.Forbidden)
	if n := r.opened.Load(); n != 1 {
		t.Errorf("%d agent channels opened, want 1", n)
	}
}

func TestOrigin(t *testing.T) {
	r := newTermRig(t, false, 0)
	for _, tc := range []struct {
		name, origin string
		tls, proxy   bool
		accepted     bool
	}{
		{"no origin", "", false, false, false},
		{"another site", "https://evil.example", false, false, false},
		{"http on a TLS listener", "http://example.com", true, false, false},
		{"https on a TLS listener", "https://example.com", true, false, true},
		{"http on a plain listener", "http://example.com", false, false, true},
		{"the proxied public origin", "https://panel.example", false, true, true},
		{"the listener's own on a proxied one", "http://example.com", false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tk := r.ticket(r.admin)
			req := request(http.MethodGet, "/api/v1/hosts/"+termHost+"/channels/console/terminal?ticket="+tk+"&cols=80&rows=24", "")
			req.Header.Set("Connection", "Upgrade")
			req.Header.Set("Upgrade", "websocket")
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if tc.tls {
				req.TLS = &tls.ConnectionState{}
				req = withSecureSession(req, r.admin.session)
			} else {
				req = withSession(req, r.admin.session, "")
			}
			if tc.proxy {
				req = req.WithContext(core.WithProxied(req.Context(), core.Proxied{ListenerID: "l1", PublicHost: "panel.example"}))
			}
			before := r.opened.Load()
			rec := r.h.serve(req)
			if accepted := r.opened.Load() > before; accepted != tc.accepted {
				t.Errorf("Origin %q: accepted %v (%d %s), want %v", tc.origin, accepted, rec.Code, rec.Body, tc.accepted)
			}
			for len(r.agents) > 0 {
				_ = (<-r.agents).Close()
			}
		})
	}
}

func TestRevocationEnds(t *testing.T) {
	for _, tc := range []struct {
		name    string
		proxied bool
		revoke  func(r *termRig, u termUser)
		want    string
	}{
		{"sign-out", false, func(r *termRig, u termUser) {
			r.h.serve(withSession(request(http.MethodPost, "/api/v1/auth/logout", ""), u.session, u.csrf))
		}, "revoked"},
		{"user disabled", false, func(r *termRig, u termUser) {
			if _, err := r.h.auth.SetDisabled(context.Background(), u.name, true); err != nil {
				r.t.Fatal(err)
			}
		}, "revoked"},
		{"demoted to member", false, func(r *termRig, u termUser) {
			if _, err := r.h.auth.SetRole(context.Background(), u.name, auth.RoleMember); err != nil {
				r.t.Fatal(err)
			}
		}, "revoked"},
		{"console disabled", false, func(r *termRig, _ termUser) {
			r.h.disp.ModuleUsableFunc = func(context.Context, string, string) error {
				return errcode.New(errcode.ModuleUnavailable, "module console is disabled")
			}
		}, "revoked"},
		{"listener revoked", true, func(r *termRig, _ termUser) { close(r.revoke) }, "revoked"},
		{"CloseStreams", false, func(r *termRig, _ termUser) { r.h.srv.CloseStreams() }, "agent restarted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newTermRig(t, tc.proxied, 0)
			u := r.addUser("bob", auth.RoleAdmin)
			ws, agent, code := r.dial(u, termHost, r.ticket(u), nil)
			if code != "" {
				t.Fatal(code)
			}
			go func() { _, _ = io.Copy(io.Discard, agent) }()
			tc.revoke(r, u)
			r.h.clock.Advance(SSERecheck)
			if got := closeReason(t, ws); got != tc.want {
				t.Errorf("closed with %q, want %q", got, tc.want)
			}
		})
	}
}

// bridgeSteps are the steps a rig's terminal bridge took (Server.bridgeStep).
type bridgeSteps chan bridgeStep

type bridgeStep struct {
	step string
	next time.Time
}

// watchBridge records the steps of the bridges r serves from now on.
func watchBridge(r *termRig) bridgeSteps {
	c := make(bridgeSteps, 4096)
	r.h.srv.bridgeStep = func(step string, next time.Time) { c <- bridgeStep{step, next} }
	return c
}

// await waits for step; for a step with a deadline, for one due after now:
// an "idle" step due after now has handled every timer the clock fired, with
// no ping out, so the next Advance reaches its next timer. A value on done
// ends the wait instead.
func (c bridgeSteps) await(t *testing.T, step string, now time.Time, done <-chan string) (string, bool) {
	t.Helper()
	timeout := time.After(10 * time.Second)
	for {
		select {
		case ev := <-c:
			if ev.step == step && (now.IsZero() || ev.next.After(now)) {
				return "", false
			}
		case got := <-done:
			return got, true
		case <-timeout:
			t.Fatalf("the bridge never took the step %s", step)
		}
	}
}

func TestKeepaliveAndActivity(t *testing.T) {
	t.Run("ping", func(t *testing.T) {
		r := newTermRig(t, false, 0)
		steps := watchBridge(r)
		var pings atomic.Int32
		ws, agent, _ := r.dial(r.admin, termHost, r.ticket(r.admin), &websocket.DialOptions{
			OnPingReceived: func(context.Context, []byte) bool { pings.Add(1); return true },
		})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() {
			for {
				if _, _, err := ws.Read(ctx); err != nil {
					return
				}
			}
		}()
		for range 2 {
			r.h.clock.Advance(SSEHeartbeat)
			// The next heartbeat is armed once the pong came back.
			steps.await(t, "idle", r.h.clock.Now(), nil)
		}
		if n := pings.Load(); n != 2 {
			t.Errorf("%d pings in two heartbeats, want 2", n)
		}
		_ = agent
	})
	t.Run("no pong", func(t *testing.T) {
		r := newTermRig(t, false, 0)
		steps := watchBridge(r)
		_, agent, _ := r.dial(r.admin, termHost, r.ticket(r.admin), nil)
		// The browser reads nothing, so no pong answers.
		r.h.clock.Advance(SSEHeartbeat)
		steps.await(t, "ping", r.h.clock.Now(), nil)
		r.h.clock.Advance(PongTimeout)
		if got := agentClose(t, agent); got != "browser gone" {
			t.Errorf("closed with %q, want browser gone", got)
		}
	})
	lastSeen := func(r *termRig, u termUser) string {
		r.t.Helper()
		var s string
		if err := r.h.db.Read().QueryRow("SELECT max(last_seen_at) FROM core_sessions s JOIN core_users u ON u.id = s.user_id WHERE u.username = ?",
			u.name).Scan(&s); err != nil {
			r.t.Fatal(err)
		}
		return s
	}
	t.Run("activity", func(t *testing.T) {
		r := newTermRig(t, false, 0)
		steps := watchBridge(r)
		ws, agent, _ := r.dial(r.admin, termHost, r.ticket(r.admin), nil)
		keep(t, ws)
		got := make(chan []byte, 64)
		go func() {
			for {
				typ, p, err := wire.ReadFrame(agent)
				if err != nil {
					return
				}
				if typ == wire.FrameData {
					got <- p
				}
			}
		}()
		ctx := context.Background()
		for i := range 13 {
			if err := ws.Write(ctx, websocket.MessageBinary, []byte{'a' + byte(i)}); err != nil {
				t.Fatalf("after %d minutes: %v", 5*i, err)
			}
			select {
			case p := <-got:
				if !bytes.Equal(p, []byte{'a' + byte(i)}) {
					t.Fatalf("the agent got %q", p)
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("after %d minutes the data frame never reached the agent", 5*i)
			}
			steps.await(t, "data", time.Time{}, nil)
			r.h.clock.Advance(5 * time.Minute)
			steps.await(t, "idle", r.h.clock.Now(), nil)
		}
		if err := ws.Write(ctx, websocket.MessageBinary, []byte("z")); err != nil {
			t.Fatalf("data every 5 minutes did not keep the session past 61 minutes: %v", err)
		}
		select {
		case <-got:
		case <-time.After(5 * time.Second):
			t.Fatal("the terminal ended although data kept its session alive")
		}

		steps.await(t, "data", time.Time{}, nil) // the last frame's touch
		before := lastSeen(r, r.admin)
		for range 58 {
			r.h.clock.Advance(SSERecheck)
			steps.await(t, "idle", r.h.clock.Now(), nil)
		}
		if after := lastSeen(r, r.admin); after != before {
			t.Errorf("29 minutes without data moved last_seen_at from %s to %s", before, after)
		}
	})
	t.Run("session expired", func(t *testing.T) {
		r := newTermRig(t, false, 0)
		steps := watchBridge(r)
		ws, agent, _ := r.dial(r.admin, termHost, r.ticket(r.admin), nil)
		go func() { _, _ = io.Copy(io.Discard, agent) }()
		reason := make(chan string, 1)
		go func() { reason <- closeReason(t, ws) }()
		// Rechecks are passive: without data the session's idle hour runs out.
		for i := 1; i <= 2*61+2; i++ {
			r.h.clock.Advance(SSERecheck)
			got, closed := steps.await(t, "idle", r.h.clock.Now(), reason)
			if !closed {
				continue
			}
			if got != "expired" || i < 2*59 {
				t.Errorf("closed with %q after %d rechecks, want expired after the idle hour", got, i)
			}
			return
		}
		select {
		case got := <-reason:
			if got != "expired" {
				t.Errorf("closed with %q, want expired", got)
			}
		case <-time.After(5 * time.Second):
			t.Error("the terminal outlived its session")
		}
	})
}

func TestBadFrameFromBrowser(t *testing.T) {
	for _, tc := range []struct {
		name string
		typ  websocket.MessageType
		p    []byte
	}{
		{"binary over 32 KiB", websocket.MessageBinary, make([]byte, wire.MaxFramePayload+1)},
		{"text that is no resize", websocket.MessageText, []byte(`{"type":"paste","cols":80,"rows":24}`)},
		{"resize out of bounds", websocket.MessageText, []byte(`{"type":"resize","cols":10,"rows":24}`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newTermRig(t, false, 0)
			ws, agent, _ := r.dial(r.admin, termHost, r.ticket(r.admin), nil)
			if err := ws.Write(context.Background(), tc.typ, tc.p); err != nil {
				t.Fatal(err)
			}
			if got := agentClose(t, agent); got != "bad frame" {
				t.Errorf("the agent's channel closed with %q, want bad frame", got)
			}
			if got := closeReason(t, ws); got != "bad frame" {
				t.Errorf("the terminal closed with %q, want bad frame", got)
			}
		})
	}
	t.Run("a resize passes", func(t *testing.T) {
		r := newTermRig(t, false, 0)
		ws, agent, _ := r.dial(r.admin, termHost, r.ticket(r.admin), nil)
		if err := ws.Write(context.Background(), websocket.MessageText, []byte(`{"type":"resize","cols":120,"rows":40}`)); err != nil {
			t.Fatal(err)
		}
		_ = agent.SetReadDeadline(time.Now().Add(5 * time.Second))
		typ, p, err := wire.ReadFrame(agent)
		if err != nil || typ != wire.FrameResize || string(p) != `{"cols":120,"rows":40}` {
			t.Errorf("the agent got %d %s %v, want a resize frame", typ, p, err)
		}
	})
}

func TestDeadlines(t *testing.T) {
	r := newTermRig(t, false, 200*time.Millisecond)
	ws, agent, code := r.dial(r.admin, termHost, r.ticket(r.admin), nil)
	if code != "" {
		t.Fatal(code)
	}
	time.Sleep(time.Second) // five listener timeouts
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := ws.Write(ctx, websocket.MessageBinary, []byte("ls\n")); err != nil {
		t.Fatal(err)
	}
	_ = agent.SetDeadline(time.Now().Add(5 * time.Second))
	if typ, p, err := wire.ReadFrame(agent); err != nil || typ != wire.FrameData || string(p) != "ls\n" {
		t.Fatalf("after an idle second the agent got %d %q %v", typ, p, err)
	}
	if err := wire.WriteFrame(agent, wire.FrameData, []byte("out")); err != nil {
		t.Fatal(err)
	}
	if typ, p, err := ws.Read(ctx); err != nil || typ != websocket.MessageBinary || string(p) != "out" {
		t.Fatalf("after an idle second the browser got %v %q %v", typ, p, err)
	}
	if err := wire.WriteFrame(agent, wire.FrameExit, []byte(`{"code":0}`)); err != nil {
		t.Fatal(err)
	}
	if typ, p, err := ws.Read(ctx); err != nil || typ != websocket.MessageText || string(p) != `{"type":"exit","code":0}` {
		t.Errorf("the exit frame reached the browser as %v %q %v", typ, p, err)
	}
	if err := wire.WriteFrame(agent, wire.FrameClose, []byte(`{"reason":"idle"}`)); err != nil {
		t.Fatal(err)
	}
	if got := closeReason(t, ws); got != "idle" {
		t.Errorf("the agent's close reached the browser as %q, want idle", got)
	}
}

func TestUserLimitAndOldAgent(t *testing.T) {
	r := newTermRig(t, false, 0)
	for i := range MaxUserTerminals {
		ws, agent, code := r.dial(r.admin, termHost, r.ticket(r.admin), nil)
		if code != "" {
			t.Fatalf("terminal %d: %s", i+1, code)
		}
		keep(t, ws)
		go func() { _, _ = io.Copy(io.Discard, agent) }()
	}
	if _, _, code := r.dial(r.admin, termHost, r.ticket(r.admin), nil); code != string(errcode.ConsoleLimit) {
		t.Errorf("a ninth terminal: %q, want %s", code, errcode.ConsoleLimit)
	}

	old := newTermRig(t, false, 0)
	old.h.disp.OpenChannelFunc = func(context.Context, core.Principal, string, string, string, url.Values) (io.ReadWriteCloser, error) {
		return nil, errcode.New(errcode.ModuleUnavailable, "Update the agent to use the terminal")
	}
	if _, _, code := old.dial(old.admin, termHost, old.ticket(old.admin), nil); code != string(errcode.ModuleUnavailable) {
		t.Errorf("an old agent: %q, want %s", code, errcode.ModuleUnavailable)
	}

	member := r.addUser("mia", auth.RoleMember)
	if _, err := r.h.auth.SetGrants(context.Background(), "mia", []auth.Grant{{Target: "*", Role: "operator"}}); err != nil {
		t.Fatal(err)
	}
	wantProblem(t, "a member's ticket", r.postTicket(member, `{"code":"`+r.code(member)+`"}`), http.StatusForbidden, errcode.Forbidden)
}

// stuckAgent is an agent channel without deadlines that nobody reads.
type stuckAgent struct{ io.ReadWriteCloser }

func TestStuckAgentReleases(t *testing.T) {
	r := newTermRig(t, false, 0)
	r.h.disp.OpenChannelFunc = func(context.Context, core.Principal, string, string, string, url.Values) (io.ReadWriteCloser, error) {
		a, b := net.Pipe()
		t.Cleanup(func() { _ = b.Close() })
		r.agents <- b
		return stuckAgent{a}, nil
	}
	ws, _, code := r.dial(r.admin, termHost, r.ticket(r.admin), nil)
	if code != "" {
		t.Fatal(code)
	}
	// The data write blocks on the agent that never reads.
	if err := ws.Write(context.Background(), websocket.MessageBinary, []byte("x")); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	r.h.srv.CloseStreams()
	if got := closeReason(t, ws); got != "agent restarted" {
		t.Errorf("closed with %q, want agent restarted", got)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("the close took %v with a stuck agent", d)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		r.h.srv.longLived.mu.Lock()
		n := len(r.h.srv.longLived.terminals)
		r.h.srv.longLived.mu.Unlock()
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the stuck terminal kept its slot")
		}
		time.Sleep(time.Millisecond)
	}
}

// TestOversizeFrame closes the terminal with "bad frame" for a binary
// message over wire.MaxFramePayload, one past the WebSocket's own read
// limit included.
func TestOversizeFrame(t *testing.T) {
	for _, size := range []int{40 << 10, 2 << 20} {
		t.Run(fmt.Sprintf("%d bytes", size), func(t *testing.T) {
			r := newTermRig(t, false, 0)
			ws, agent, _ := r.dial(r.admin, termHost, r.ticket(r.admin), nil)
			go func() { _ = ws.Write(context.Background(), websocket.MessageBinary, make([]byte, size)) }()
			if got := agentClose(t, agent); got != "bad frame" {
				t.Errorf("the agent's channel closed with %q, want bad frame", got)
			}
			if got := closeReason(t, ws); got != "bad frame" {
				t.Errorf("the terminal closed with %q, want bad frame", got)
			}
		})
	}
}

// TestRecheckErrorReason closes a terminal whose recheck could not be made
// with "error", and one whose console was turned off with "revoked".
func TestRecheckErrorReason(t *testing.T) {
	for _, tc := range []struct {
		name  string
		cause func(r *termRig)
		want  string
	}{
		{"identity read error", func(r *termRig) { _ = r.h.db.Close() }, "error"},
		{"database error in ModuleUsable", func(r *termRig) {
			r.h.disp.ModuleUsableFunc = func(context.Context, string, string) error {
				return errors.New("dispatch: read the host's modules: database is locked")
			}
		}, "error"},
		{"console disabled", func(r *termRig) {
			r.h.disp.ModuleUsableFunc = func(context.Context, string, string) error {
				return errcode.New(errcode.ModuleUnavailable, "module console is disabled")
			}
		}, "revoked"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newTermRig(t, false, 0)
			ws, agent, _ := r.dial(r.admin, termHost, r.ticket(r.admin), nil)
			tc.cause(r)
			r.h.clock.Advance(SSERecheck)
			if got := agentClose(t, agent); got != tc.want {
				t.Errorf("the agent's channel closed with %q, want %q", got, tc.want)
			}
			if got := closeReason(t, ws); got != tc.want {
				t.Errorf("the terminal closed with %q, want %q", got, tc.want)
			}
		})
	}
}
