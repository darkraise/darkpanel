package runtime_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/yamux"

	"github.com/darkraise/darkpanel/internal/agent/api"
	"github.com/darkraise/darkpanel/internal/agent/runtime"
	"github.com/darkraise/darkpanel/internal/agent/wire"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/platform"
)

type chParams struct {
	Tag string `json:"tag,omitempty" jsonschema:"maxLength=3"`
}

type chItem struct {
	Name string `json:"name"`
}

type chRead struct{}

func (chRead) Read(context.Context, core.Env, chItem) (chItem, error) { return chItem{Name: "ok"}, nil }

// chModule is a module named id with the channels "echoes", which echoes
// each data frame until the peer closes, and "holder", which waits for its
// context to end. ended gets each handler's return.
type chModule struct {
	id    string
	ended chan error
}

func (m chModule) Manifest() core.Manifest {
	return core.Manifest{ID: m.id, Version: 3, DefaultEnabled: true, Actions: []core.ActionDef{
		core.DefineRead(core.ActionSpec{Name: m.id + ".item.get", Version: 1, Side: core.SideAgent}, chRead{}),
	}}
}

func (m chModule) Channels() []core.ChannelDef {
	return []core.ChannelDef{
		core.DefineChannel("echoes", func(_ context.Context, _ core.Env, _ chParams, c core.ChannelConn) error {
			for {
				t, p, err := c.ReadFrame()
				if err != nil || t == wire.FrameClose {
					m.ended <- err
					return err
				}
				if err := c.WriteFrame(t, p); err != nil {
					m.ended <- err
					return err
				}
			}
		}),
		core.DefineChannel("holder", func(ctx context.Context, _ core.Env, _ chParams, _ core.ChannelConn) error {
			<-ctx.Done()
			m.ended <- ctx.Err()
			return nil
		}),
	}
}

// plainModule has chModule's manifest and no channels.
type plainModule struct{ m chModule }

func (p plainModule) Manifest() core.Manifest { return p.m.Manifest() }

type chFixture struct {
	t      *testing.T
	rt     *runtime.Runtime
	ended  chan error
	client *api.Client
	sess   *yamux.Session // the client's end
	policy string
}

var chUser = core.Principal{Actor: "user:alice", UserID: "alice", Scope: core.ScopeAdmin}

// newChFixture opens a runtime with mod over a fresh agent database and
// serves the agent API over a yamux pair.
func newChFixture(t *testing.T, mod core.Module, ended chan error) *chFixture {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(context.Background(), filepath.Join(dir, "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	reg, err := core.NewRegistry(mod)
	if err != nil {
		t.Fatal(err)
	}
	f := &chFixture{t: t, ended: ended, policy: filepath.Join(dir, "agent-policy.json")}
	rt, err := runtime.Open(context.Background(), runtime.Config{
		DB: db, Sealer: coretest.Sealer{}, Clock: coretest.NewFakeClock(time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)),
		Log: slog.New(slog.DiscardHandler), Registry: reg, HostID: "h1", BinaryVersion: "test",
		BackupDir: filepath.Join(dir, "backups"), Exec: platform.NewRunner(nil), FilesTestMode: true,
		PolicyFile: f.policy, PolicyOwner: uint32(os.Getuid()), //nolint:gosec // G115: a uid fits in 32 bits
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(rt.Close)
	f.rt = rt
	a, b := net.Pipe()
	srvSess, err := yamux.Server(a, nil)
	if err != nil {
		t.Fatal(err)
	}
	cliSess, err := yamux.Client(b, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: api.NewHandler(api.Config{Agent: rt, Log: slog.New(slog.DiscardHandler)}), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(srvSess) }()
	t.Cleanup(func() {
		_ = srv.Close()
		_ = cliSess.Close()
		_ = srvSess.Close()
	})
	f.sess = cliSess
	f.client = api.NewClient(api.ClientConfig{Dial: f.dial})
	return f
}

func newEchoFixture(t *testing.T, id string) *chFixture {
	t.Helper()
	ended := make(chan error, 4)
	return newChFixture(t, chModule{id: id, ended: ended}, ended)
}

func (f *chFixture) open(name string, params url.Values) io.ReadWriteCloser {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rw, err := f.client.OpenChannel(ctx, chUser, f.module(), name, params)
	if err != nil {
		f.t.Fatalf("OpenChannel(%s): %v", name, err)
	}
	f.t.Cleanup(func() { _ = rw.Close() })
	return rw
}

func (f *chFixture) module() string {
	h, err := f.rt.Hello(context.Background())
	if err != nil {
		f.t.Fatal(err)
	}
	for _, m := range h.Modules {
		if m.ID != core.ModuleIDCore {
			return m.ID
		}
	}
	f.t.Fatal("no module")
	return ""
}

// wantClose reads frames from rw until the agent's close frame, which must
// carry reason, within limit.
func wantClose(t *testing.T, rw io.Reader, reason string, limit time.Duration) {
	t.Helper()
	got := make(chan string, 1)
	go func() {
		for {
			typ, p, err := wire.ReadFrame(rw)
			if err != nil {
				got <- "error: " + err.Error()
				return
			}
			if typ == wire.FrameClose {
				var c struct {
					Reason string `json:"reason"`
				}
				_ = json.Unmarshal(p, &c)
				got <- c.Reason
				return
			}
		}
	}()
	select {
	case r := <-got:
		if r != reason {
			t.Errorf("close reason %q, want %q", r, reason)
		}
	case <-time.After(limit):
		t.Fatalf("no close frame within %s", limit)
	}
}

func (f *chFixture) wantEnded(limit time.Duration) {
	f.t.Helper()
	select {
	case err := <-f.ended:
		_ = err // the handler's own end: a read error or nil
	case <-time.After(limit):
		f.t.Fatalf("the module's handler did not end within %s", limit)
	}
}

func wantCode(t *testing.T, err error, code errcode.Code) {
	t.Helper()
	var e *errcode.Error
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("err = %v, want code %s", err, code)
	}
}

func TestUpgrade(t *testing.T) {
	f := newEchoFixture(t, "test")
	rw := f.open("echoes", url.Values{"tag": {"a"}})
	for _, msg := range []string{"hello", "", strings.Repeat("x", wire.MaxFramePayload)} {
		if err := wire.WriteFrame(rw, wire.FrameData, []byte(msg)); err != nil {
			t.Fatal(err)
		}
		typ, p, err := wire.ReadFrame(rw)
		if err != nil || typ != wire.FrameData || string(p) != msg {
			t.Fatalf("echo of %d bytes = type %d, %d bytes, %v", len(msg), typ, len(p), err)
		}
	}

	ctx := context.Background()
	_, err := f.client.OpenChannel(ctx, chUser, "test", "nope", nil)
	wantCode(t, err, errcode.NotFound)
	_, err = f.client.OpenChannel(ctx, chUser, "test", "echoes", url.Values{"tag": {"long"}})
	wantCode(t, err, errcode.InvalidInput)
	_, err = f.client.OpenChannel(ctx, chUser, "test", "echoes", url.Values{"other": {"x"}})
	wantCode(t, err, errcode.InvalidInput)

	// A plain GET without the upgrade is refused with request.invalid_input,
	// which the agent API answers with 422, and stays HTTP.
	hc := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return f.dial(ctx)
	}}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://agent/agent/v1/channels/test/echoes", nil)
	if err != nil {
		t.Fatal(err)
	}
	pb, err := json.Marshal(wire.FromPrincipal(chUser))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(wire.HeaderPrincipal, string(pb))
	resp, err := hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(string(body), "Upgrade: "+wire.ChannelUpgrade) {
		t.Errorf("GET without Upgrade = %d %s, want 422 request.invalid_input asking for the upgrade", resp.StatusCode, body)
	}
}

// dial opens a raw stream of the fixture's session.
func (f *chFixture) dial(context.Context) (net.Conn, error) { return f.sess.Open() }

// rawFrame is a frame header with any type and length, and n payload bytes.
func rawFrame(typ byte, length uint32, n int) []byte {
	b := make([]byte, 5+n)
	b[0] = typ
	binary.BigEndian.PutUint32(b[1:5], length)
	return b
}

func TestBadFrameCloses(t *testing.T) {
	for _, tc := range []struct {
		name  string
		frame []byte
	}{
		{"too long", rawFrame(wire.FrameData, wire.MaxFramePayload+1, 0)},
		{"unknown type", rawFrame(9, 1, 1)},
	} {
		t.Run("agent reads "+tc.name, func(t *testing.T) {
			f := newEchoFixture(t, "test")
			rw := f.open("echoes", nil)
			if _, err := rw.Write(tc.frame); err != nil {
				t.Fatal(err)
			}
			wantClose(t, rw, runtime.CloseBadFrame, 2*time.Second)
			f.wantEnded(2 * time.Second)
		})
		t.Run("client reads "+tc.name, func(t *testing.T) {
			got := make(chan []byte, 1)
			client := badAgent(t, tc.frame, got)
			rw, err := client.OpenChannel(context.Background(), chUser, "test", "echoes", nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = rw.Close() }()
			if _, _, err := wire.ReadFrame(rw); !errors.Is(err, wire.ErrFrame) {
				t.Errorf("reading the bad frame = %v, want wire.ErrFrame", err)
			}
			select {
			case b := <-got:
				var want bytes.Buffer
				_ = wire.WriteFrame(&want, wire.FrameClose, []byte(`{"reason":"bad frame"}`))
				if !bytes.Equal(b, want.Bytes()) {
					t.Errorf("the agent got %q after its bad frame, want the close frame %q", b, want.Bytes())
				}
			case <-time.After(2 * time.Second):
				t.Fatal("the client sent nothing after the bad frame")
			}
		})
	}
}

// badAgent serves one channel upgrade that answers 101, sends frame and
// hands what the client sends next, up to its closing the connection, to got.
func badAgent(t *testing.T, frame []byte, got chan<- []byte) *api.Client {
	t.Helper()
	a, b := net.Pipe()
	srvSess, err := yamux.Server(a, nil)
	if err != nil {
		t.Fatal(err)
	}
	cliSess, err := yamux.Client(b, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		conn, _, err := http.NewResponseController(w).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer func() { _ = conn.Close() }()
		_, _ = io.WriteString(conn, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: "+wire.ChannelUpgrade+"\r\n\r\n")
		_, _ = conn.Write(frame)
		rest, _ := io.ReadAll(conn)
		got <- rest
	})}
	go func() { _ = srv.Serve(srvSess) }()
	t.Cleanup(func() {
		_ = srv.Close()
		_ = cliSess.Close()
		_ = srvSess.Close()
	})
	return api.NewClient(api.ClientConfig{Dial: func(context.Context) (net.Conn, error) { return cliSess.Open() }})
}

func TestChannelNotGuarded(t *testing.T) {
	f := newEchoFixture(t, "test")
	rw := f.open("holder", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := f.client.Read(ctx, chUser, core.ActionRef{Name: "test.item.get", Version: 1}, json.RawMessage(`{"name":"a"}`))
	if err != nil || !strings.Contains(string(out), `"ok"`) {
		t.Fatalf("a read while a channel is open = %s, %v", out, err)
	}
	start := time.Now()
	closed := make(chan struct{})
	go func() {
		f.rt.Close()
		close(closed)
	}()
	wantClose(t, rw, runtime.CloseAgentRestarted, time.Second)
	select {
	case <-closed:
	case <-time.After(time.Second - time.Since(start)):
		t.Fatal("Runtime.Close did not return within 1 s of an open channel")
	}
	f.wantEnded(time.Second)
	_, err = f.client.OpenChannel(ctx, chUser, "test", "holder", nil)
	wantCode(t, err, errcode.Unavailable)
}

func TestModuleDisableClosesChannels(t *testing.T) {
	for _, tc := range []struct {
		name string
		off  func(t *testing.T, f *chFixture)
		code errcode.Code
	}{
		{"flag 0", func(t *testing.T, f *chFixture) {
			if err := f.rt.SetModuleEnabled(context.Background(), "console", false); err != nil {
				t.Fatal(err)
			}
		}, errcode.ModuleUnavailable},
		{"policy reload denies", func(t *testing.T, f *chFixture) {
			if err := os.WriteFile(f.policy, []byte(`{"denyModules":["console"]}`), 0o644); err != nil { //nolint:gosec // G306: the policy file is world-readable
				t.Fatal(err)
			}
			if err := f.rt.ReloadPolicy(context.Background()); err != nil {
				t.Fatal(err)
			}
		}, errcode.ModuleUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newEchoFixture(t, "console")
			if err := f.rt.SetModuleEnabled(context.Background(), "console", true); err != nil {
				t.Fatal(err)
			}
			rw := f.open("holder", nil)
			tc.off(t, f)
			wantClose(t, rw, runtime.CloseModuleOff, 2*time.Second)
			f.wantEnded(2 * time.Second)
			_, err := f.client.OpenChannel(context.Background(), chUser, "console", "holder", nil)
			wantCode(t, err, tc.code)
		})
	}
}

// helloShape is the hello JSON's keys at every level, its module ids and
// versions, and every string value in it.
func helloShape(t *testing.T, rt *runtime.Runtime) (keys, modules, strs []string) {
	t.Helper()
	h, err := rt.Hello(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(wire.FromHello(h))
	if err != nil {
		t.Fatal(err)
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	var walk func(path string, v any)
	walk = func(path string, v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, e := range x {
				keys = append(keys, path+"."+k)
				walk(path+"."+k, e)
			}
		case []any:
			for _, e := range x {
				walk(path+"[]", e)
			}
		case string:
			strs = append(strs, x)
		}
	}
	walk("", v)
	for _, m := range h.Modules {
		modules = append(modules, fmt.Sprintf("%s@%d", m.ID, m.Version))
	}
	sort.Strings(keys)
	keys = compact(keys)
	return keys, modules, strs
}

func compact(s []string) []string {
	var out []string
	for i, v := range s {
		if i == 0 || v != s[i-1] {
			out = append(out, v)
		}
	}
	return out
}

func TestHelloUnchanged(t *testing.T) {
	ended := make(chan error, 1)
	with := newChFixture(t, chModule{id: "test", ended: ended}, ended)
	without := newChFixture(t, plainModule{chModule{id: "test", ended: ended}}, ended)
	k1, m1, s1 := helloShape(t, with.rt)
	k2, m2, _ := helloShape(t, without.rt)
	if !reflect.DeepEqual(k1, k2) {
		t.Errorf("hello keys with channels %v, without %v", k1, k2)
	}
	if !reflect.DeepEqual(m1, m2) {
		t.Errorf("hello modules with channels %v, without %v", m1, m2)
	}
	for _, k := range k1 {
		if strings.Contains(k, "echoes") || strings.Contains(k, "holder") || strings.Contains(strings.ToLower(k), "channel") {
			t.Errorf("hello key %s names a channel", k)
		}
	}
	for _, s := range s1 {
		if strings.Contains(s, "echoes") || strings.Contains(s, "holder") {
			t.Errorf("hello value %q names a channel", s)
		}
	}
}
