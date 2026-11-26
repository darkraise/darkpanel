//go:build harness

package harness

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	mrand "math/rand/v2"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"golang.org/x/crypto/ssh"

	"github.com/darkraise/darkpanel/internal/agent/wire"
	"github.com/darkraise/darkpanel/internal/controller/api"
	"github.com/darkraise/darkpanel/internal/controller/coreactions"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/jobs"
	m11svc "github.com/darkraise/darkpanel/internal/modules/services"
	"github.com/darkraise/darkpanel/test/harness/vm"
)

const (
	// m11TermWait bounds how long a terminal command takes to print what it should.
	m11TermWait = time.Minute
	// m11SSHWait bounds one SSH sign-in.
	m11SSHWait = 30 * time.Second
	// m11TermCols and m11TermRows are the size a terminal opens at.
	m11TermCols, m11TermRows = "120", "40"
)

// m11StepUp elevates a's session for 15 minutes with the next TOTP code, as
// the step-up dialog does before an elevated action.
func m11StepUp(ctx context.Context, t *testing.T, f *fleet, a *API) {
	t.Helper()
	if err := a.do(ctx, http.MethodPost, "/api/v1/auth/step-up", map[string]string{"code": nextCode(ctx, t, f)}, nil); err != nil {
		t.Fatalf("step-up: %v", err)
	}
}

// m11Raw is a's client without its per-request timeout, for transfers and
// WebSockets that outlast apiTimeout: their context bounds them.
func (a *API) m11Raw() *http.Client {
	return &http.Client{Transport: a.http.Transport, Jar: a.http.Jar}
}

// m11Problem reads an answer outside 2xx as an *APIError.
func m11Problem(resp *http.Response) error {
	data, _ := io.ReadAll(io.LimitReader(resp.Body, maxProblemBody))
	e := &APIError{Status: resp.StatusCode}
	var p struct {
		Code   string `json:"code"`
		Detail string `json:"detail"`
	}
	if json.Unmarshal(data, &p) == nil && p.Code != "" {
		e.Code, e.Detail = p.Code, p.Detail
	} else {
		e.Detail = string(data)
	}
	return e
}

// m11FilesURL is the controller's file route op on host for path.
func m11FilesURL(a *API, host, op, path string) string {
	return a.base + "/api/v1/hosts/" + url.PathEscape(host) + "/files/" + op + "?" + url.Values{"path": {path}}.Encode()
}

// m11Upload uploads size bytes of the ChaCha8 stream of seed to path on host
// through the controller and returns the agent's answer and the SHA-256 of
// what was sent.
func m11Upload(ctx context.Context, a *API, host, path string, size int64, seed [32]byte) (wire.FileWritten, string, error) {
	h := sha256.New()
	body := io.TeeReader(io.LimitReader(mrand.NewChaCha8(seed), size), h)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m11FilesURL(a, host, "upload", path), body)
	if err != nil {
		return wire.FileWritten{}, "", err
	}
	req.ContentLength = size
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set(api.CSRFHeader, a.csrf)
	resp, err := a.m11Raw().Do(req)
	if err != nil {
		return wire.FileWritten{}, "", fmt.Errorf("harness: upload %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return wire.FileWritten{}, "", fmt.Errorf("harness: upload %s: %w", path, m11Problem(resp))
	}
	var w wire.FileWritten
	if err := json.NewDecoder(resp.Body).Decode(&w); err != nil {
		return wire.FileWritten{}, "", fmt.Errorf("harness: upload %s: %w", path, err)
	}
	return w, hex.EncodeToString(h.Sum(nil)), nil
}

// m11Download downloads path on host through the controller and returns its
// size and SHA-256.
func m11Download(ctx context.Context, a *API, host, path string) (int64, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m11FilesURL(a, host, "download", path), nil)
	if err != nil {
		return 0, "", err
	}
	resp, err := a.m11Raw().Do(req)
	if err != nil {
		return 0, "", fmt.Errorf("harness: download %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return 0, "", fmt.Errorf("harness: download %s: %w", path, m11Problem(resp))
	}
	h := sha256.New()
	n, err := io.Copy(h, resp.Body)
	if err != nil {
		return n, "", fmt.Errorf("harness: download %s after %d bytes: %w", path, n, err)
	}
	return n, hex.EncodeToString(h.Sum(nil)), nil
}

// m11Term is one terminal WebSocket: a reader keeps the output, and answers
// the controller's pings, until the socket closes.
type m11Term struct {
	c    *websocket.Conn
	mu   sync.Mutex
	out  []byte
	err  error // why the socket closed; nil while it is open
	done chan struct{}
}

// m11Ticket answers code with a terminal ticket for host.
func m11Ticket(ctx context.Context, a *API, host, code string) (string, error) {
	var tk struct {
		Ticket string `json:"ticket"`
	}
	path := "/api/v1/hosts/" + url.PathEscape(host) + "/channels/console/terminal/ticket"
	if err := a.do(ctx, http.MethodPost, path, map[string]string{"code": code}, &tk); err != nil {
		return "", fmt.Errorf("terminal ticket: %w", err)
	}
	return tk.Ticket, nil
}

// m11TermURL is the terminal WebSocket of host on a's base with ticket.
func m11TermURL(base, host, ticket string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	default:
		return "", fmt.Errorf("harness: base %q is not http or https", base)
	}
	u.Path = "/api/v1/hosts/" + url.PathEscape(host) + "/channels/console/terminal"
	u.RawQuery = url.Values{"ticket": {ticket}, "cols": {m11TermCols}, "rows": {m11TermRows}}.Encode()
	return u.String(), nil
}

// m11OpenTerm takes a ticket with the next TOTP code and opens host's
// terminal through a, with the Origin of a's own pages. The reader runs on
// ctx, so it must outlive the terminal. A refusal before the upgrade is an
// *APIError.
func m11OpenTerm(ctx context.Context, t *testing.T, f *fleet, a *API, host string) (*m11Term, error) {
	t.Helper()
	tk, err := m11Ticket(ctx, a, host, nextCode(ctx, t, f))
	if err != nil {
		return nil, err
	}
	u, err := m11TermURL(a.base, host, tk)
	if err != nil {
		return nil, err
	}
	c, resp, err := websocket.Dial(ctx, u, &websocket.DialOptions{
		HTTPClient: a.m11Raw(),
		HTTPHeader: http.Header{"Origin": {a.base}},
	})
	if err != nil {
		if resp != nil && resp.StatusCode != http.StatusSwitchingProtocols {
			return nil, m11Problem(resp)
		}
		return nil, fmt.Errorf("harness: open the terminal of %s: %w", host, err)
	}
	c.SetReadLimit(1 << 20)
	tm := &m11Term{c: c, done: make(chan struct{})}
	go tm.read(ctx)
	return tm, nil
}

func (tm *m11Term) read(ctx context.Context) {
	defer close(tm.done)
	for {
		typ, p, err := tm.c.Read(ctx)
		tm.mu.Lock()
		if err != nil {
			tm.err = err
			tm.mu.Unlock()
			return
		}
		if typ == websocket.MessageBinary {
			tm.out = append(tm.out, p...)
		}
		tm.mu.Unlock()
	}
}

// expect types line and waits until the plain output after it matches want,
// and returns the match.
func (tm *m11Term) expect(ctx context.Context, line string, want *regexp.Regexp) ([]string, error) {
	tm.mu.Lock()
	mark := len(tm.out)
	tm.mu.Unlock()
	if err := tm.c.Write(ctx, websocket.MessageBinary, []byte(line+"\n")); err != nil {
		return nil, fmt.Errorf("harness: type %q: %w", line, err)
	}
	deadline := time.NewTimer(m11TermWait)
	defer deadline.Stop()
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		tm.mu.Lock()
		out, closed := string(tm.out[mark:]), tm.err
		tm.mu.Unlock()
		if m := want.FindStringSubmatch(m11Plain(out)); m != nil {
			return m, nil
		}
		if closed != nil {
			return nil, fmt.Errorf("harness: the terminal closed (%s) before %q printed %s; output %q", m11CloseReason(closed), line, want, m11Tail(out))
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			return nil, fmt.Errorf("harness: %q printed nothing matching %s within %s; output %q", line, want, m11TermWait, m11Tail(out))
		case <-tick.C:
		}
	}
}

// closed returns why the socket closed, or nil while it is open.
func (tm *m11Term) closed() error {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	return tm.err
}

// close closes the socket and waits for the reader.
func (tm *m11Term) close() {
	_ = tm.c.Close(websocket.StatusNormalClosure, "")
	<-tm.done
}

// m11CloseReason is the close frame's reason of err, or err itself.
func m11CloseReason(err error) string {
	var ce websocket.CloseError
	if errors.As(err, &ce) {
		return fmt.Sprintf("status %d, reason %q", ce.Code, ce.Reason)
	}
	return err.Error()
}

// m11Tail is the last 400 bytes of out.
func m11Tail(out string) string {
	return out[max(0, len(out)-400):]
}

// m11EchoLine matches the plain output line of echo m11, not the echoed
// command.
var m11EchoLine = regexp.MustCompile(`(?m)^m11$`)

// m11Escape matches terminal escape sequences: OSC (titles, systemd's
// context markers), CSI (bracketed paste, colours) and two-byte ones.
var m11Escape = regexp.MustCompile("\x1b\\][^\x07\x1b]*(?:\x07|\x1b\\\\)|\x1b\\[[0-9;?]*[ -/]*[@-~]|\x1b[@-Z\\\\-_]")

// m11Plain is terminal output without escape sequences and carriage
// returns, so lines read as printed.
func m11Plain(out string) string {
	return strings.ReplaceAll(m11Escape.ReplaceAllString(out, ""), "\r", "")
}

// m11NewKey returns a new ed25519 signer and its authorized_keys line.
func m11NewKey(comment string) (ssh.Signer, string, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, "", err
	}
	s, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		return nil, "", err
	}
	return s, strings.TrimSpace(string(ssh.MarshalAuthorizedKey(s.PublicKey()))) + " " + comment, nil
}

// m11SSH signs in as user to sshd on addr, dialled from inside from, with
// auth, and returns what id -un prints there.
func m11SSH(ctx context.Context, from *vm.VM, addr, user string, auth ssh.AuthMethod) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, m11SSHWait)
	defer cancel()
	conn, err := from.DialContext(ctx, "tcp", addr)
	if err != nil {
		return "", fmt.Errorf("harness: dial %s: %w", addr, err)
	}
	// The channel's connection has no deadlines; closing it ends a hung
	// handshake.
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	defer func() { _ = conn.Close() }()
	cfg := &ssh.ClientConfig{User: user, Auth: []ssh.AuthMethod{auth},
		HostKeyCallback: ssh.InsecureIgnoreHostKey()} //nolint:gosec // G106: a lab VM's freshly made host key
	c, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		return "", fmt.Errorf("harness: sign in as %s: %w", user, err)
	}
	client := ssh.NewClient(c, chans, reqs)
	defer func() { _ = client.Close() }()
	s, err := client.NewSession()
	if err != nil {
		return "", fmt.Errorf("harness: session as %s: %w", user, err)
	}
	defer func() { _ = s.Close() }()
	out, err := s.Output("id -un")
	if err != nil {
		return "", fmt.Errorf("harness: id -un as %s: %w", user, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// m11Follow opens host's services.journal-follow stream for unit and calls
// ready once its headers arrived; it returns the first entry match accepts.
func m11Follow(ctx context.Context, a *API, host, unit string, ready func(), match func(m11svc.Entry) bool) (m11svc.Entry, error) {
	q := url.Values{"unit": {unit}, "lines": {"1"}}
	path := "/api/v1/hosts/" + url.PathEscape(host) + "/streams/services/journal-follow?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.base+path, nil)
	if err != nil {
		return m11svc.Entry{}, err
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := a.m11Raw().Do(req)
	if err != nil {
		return m11svc.Entry{}, fmt.Errorf("harness: GET %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return m11svc.Entry{}, m11Problem(resp)
	}
	ready()
	return m11xFollowed(resp.Body, match)
}

// m11xFollowed reads server-sent events from r and returns the first data
// event's entry that match accepts. An end event or a closed stream before it
// is an error naming the messages seen.
func m11xFollowed(r io.Reader, match func(m11svc.Entry) bool) (m11svc.Entry, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), m4MaxFrameLine)
	var (
		event string
		data  []string
		seen  []string
	)
	for sc.Scan() {
		line := sc.Text()
		if line != "" {
			switch {
			case strings.HasPrefix(line, "event:"):
				event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			case strings.HasPrefix(line, "data:"):
				data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
			}
			continue
		}
		payload, name := strings.Join(data, "\n"), event
		event, data = "", nil
		switch name {
		case "end":
			return m11svc.Entry{}, fmt.Errorf("harness: the journal stream ended (%s); messages %q", payload, seen)
		case "data":
			var e m11svc.Entry
			if err := json.Unmarshal([]byte(payload), &e); err != nil {
				return m11svc.Entry{}, fmt.Errorf("harness: journal entry %q: %w", payload, err)
			}
			if match(e) {
				return e, nil
			}
			seen = append(seen, e.Message)
		}
	}
	if err := sc.Err(); err != nil {
		return m11svc.Entry{}, fmt.Errorf("harness: reading the journal stream: %w; messages %q", err, seen)
	}
	return m11svc.Entry{}, fmt.Errorf("harness: the journal stream closed; messages %q", seen)
}

// m11xStopLine accepts systemd's line for a finished stop of a unit.
func m11xStopLine(e m11svc.Entry) bool { return strings.HasPrefix(e.Message, "Stopped ") }

// m11xRefused accepts an error that is an API answer with code.
func m11xRefused(err error, code string) error {
	var apiErr *APIError
	switch {
	case err == nil:
		return fmt.Errorf("harness: the request went through, want %s", code)
	case !errors.As(err, &apiErr):
		return fmt.Errorf("harness: %w, want the API's %s", err, code)
	case apiErr.Code != code:
		return fmt.Errorf("harness: the API answered %d %s (%s), want %s", apiErr.Status, apiErr.Code, apiErr.Detail, code)
	}
	return nil
}

// m11xRefusedWith accepts an API answer with code whose detail holds detail.
func m11xRefusedWith(err error, code, detail string) error {
	if e := m11xRefused(err, code); e != nil {
		return e
	}
	var apiErr *APIError
	errors.As(err, &apiErr)
	if !strings.Contains(apiErr.Detail, detail) {
		return fmt.Errorf("harness: the API's %s says %q, want it to hold %q", code, apiErr.Detail, detail)
	}
	return nil
}

// m11Firewall is the part of firewall.status the fail2ban scenario reads.
type m11Firewall struct {
	Supported   bool     `json:"supported"`
	Unsupported []string `json:"unsupported"`
	Neighbours  []struct {
		Kind   string `json:"kind"`
		Banned int    `json:"banned"`
	} `json:"neighbours"`
}

// m11xBanned accepts a supported firewall whose fail2ban neighbour holds banned addresses.
func m11xBanned(st m11Firewall, banned int) error {
	if !st.Supported {
		return fmt.Errorf("harness: the firewall is unsupported: %v", st.Unsupported)
	}
	for _, n := range st.Neighbours {
		if n.Kind == "fail2ban" {
			if n.Banned != banned {
				return fmt.Errorf("harness: fail2ban: %d banned addresses, want %d", n.Banned, banned)
			}
			return nil
		}
	}
	return fmt.Errorf("harness: the firewall lists no fail2ban neighbour: %+v", st.Neighbours)
}

// m11xModule returns the state of module on host in hosts.
func m11xModule(hosts []core.Host, host, module string) (core.ModuleState, error) {
	h, ok := findHost(hosts, host)
	if !ok {
		return core.ModuleState{}, fmt.Errorf("harness: host %s is not listed", host)
	}
	for _, m := range h.Modules {
		if m.ID == module {
			return m, nil
		}
	}
	return core.ModuleState{}, fmt.Errorf("harness: host %s lists no module %s", host, module)
}

// m11xUsable accepts module on host enabled and available.
func m11xUsable(hosts []core.Host, host, module string) error {
	m, err := m11xModule(hosts, host, module)
	if err != nil {
		return err
	}
	if !m.Enabled || !m.Available {
		reason := ""
		if m.Reason != nil {
			reason = *m.Reason
		}
		return fmt.Errorf("harness: module %s on %s is enabled %t, available %t (%s)", module, host, m.Enabled, m.Available, reason)
	}
	return nil
}

// m11xAudited accepts entries holding action in each of phases.
func m11xAudited(entries []coreactions.AuditEntry, action string, phases ...string) error {
	var got []string
	for _, e := range entries {
		if e.Action == action {
			got = append(got, e.Phase)
		}
	}
	for _, p := range phases {
		if !slices.Contains(got, p) {
			return fmt.Errorf("harness: the audit holds %s in phases %v, want %v", action, got, phases)
		}
	}
	return nil
}

// m11xCgroupScope accepts a /proc/<pid>/cgroup that places the process in a
// darkpanel-console-*.scope.
func m11xCgroupScope(cgroup string) error {
	for line := range strings.SplitSeq(cgroup, "\n") {
		for part := range strings.SplitSeq(line, "/") {
			if strings.HasPrefix(part, "darkpanel-console-") && strings.HasSuffix(part, ".scope") {
				return nil
			}
		}
	}
	return fmt.Errorf("harness: the process is in no darkpanel-console-*.scope: %q", cgroup)
}

// m11xJobRan accepts a job list whose job slug has run and exited status.
func m11xJobRan(list []jobs.Job, slug string, status int32) error {
	for _, j := range list {
		if j.Slug != slug {
			continue
		}
		if j.ExitStatus == nil || j.LastStart == "" {
			return fmt.Errorf("harness: job %s has not run yet (last start %q, result %q)", slug, j.LastStart, j.LastResult)
		}
		if *j.ExitStatus != status {
			return fmt.Errorf("harness: job %s exited %d (%s), want %d", slug, *j.ExitStatus, j.LastResult, status)
		}
		return nil
	}
	return fmt.Errorf("harness: the job list has no %s", slug)
}
