package console

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/agent/wire"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
	"github.com/darkraise/darkpanel/internal/core/store"
)

var (
	t0     = time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	adminP = core.Principal{Actor: "user:admin", UserID: "admin", Scope: core.ScopeAdmin, Source: "192.0.2.7"}
)

// fakeSession is a shell that echoes what it is sent, as a terminal does.
type fakeSession struct {
	r *io.PipeReader
	w *io.PipeWriter

	mu      sync.Mutex
	input   bytes.Buffer
	sizes   [][2]uint16
	done    chan struct{}
	code    int
	closed  bool
	exitErr error
}

func newFakeSession() *fakeSession {
	r, w := io.Pipe()
	return &fakeSession{r: r, w: w, done: make(chan struct{})}
}

func (s *fakeSession) Read(p []byte) (int, error) { return s.r.Read(p) }

func (s *fakeSession) Write(p []byte) (int, error) {
	s.mu.Lock()
	s.input.Write(p)
	s.mu.Unlock()
	return s.w.Write(p)
}

func (s *fakeSession) Resize(cols, rows uint16) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sizes = append(s.sizes, [2]uint16{cols, rows})
	return nil
}

// exit ends the shell with code, as typing exit does.
func (s *fakeSession) exit(code int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed, s.code = true, code
		_ = s.w.Close()
		close(s.done)
	}
}

func (s *fakeSession) Close() error {
	s.exit(129)
	return nil
}

func (s *fakeSession) Wait() (int, error) {
	<-s.done
	return s.code, s.exitErr
}

func (s *fakeSession) received() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.input.String()
}

// fakeShell is Env.Shell; started holds every session it started.
type fakeShell struct {
	mu      sync.Mutex
	started []*fakeSession
}

func (f *fakeShell) Start(context.Context, uint16, uint16) (core.ShellSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := newFakeSession()
	f.started = append(f.started, s)
	return s, nil
}

func (f *fakeShell) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.started)
}

func (f *fakeShell) session(i int) *fakeSession {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.started[i]
}

type frame struct {
	typ byte
	p   []byte
}

// fakeConn is the agent's end of a channel; the test is the peer.
type fakeConn struct {
	in     chan frame
	closed chan struct{}

	mu     sync.Mutex
	out    []frame
	reason string
}

func newFakeConn() *fakeConn { return &fakeConn{in: make(chan frame), closed: make(chan struct{})} }

func (c *fakeConn) ReadFrame() (byte, []byte, error) {
	select {
	case f := <-c.in:
		return f.typ, f.p, nil
	case <-c.closed:
		return 0, nil, errors.New("closed")
	}
}

func (c *fakeConn) WriteFrame(t byte, p []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	select {
	case <-c.closed:
		return errors.New("closed")
	default:
	}
	c.out = append(c.out, frame{t, bytes.Clone(p)})
	return nil
}

func (c *fakeConn) Close(reason string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.reason == "" {
		c.reason = reason
		close(c.closed)
	}
	return nil
}

func (c *fakeConn) send(t *testing.T, typ byte, p []byte) {
	t.Helper()
	select {
	case c.in <- frame{typ, p}:
	case <-c.closed:
		t.Fatalf("the channel closed (%s) before the frame was read", c.closeReason())
	case <-time.After(5 * time.Second):
		t.Fatal("the terminal reads no frame")
	}
}

func (c *fakeConn) closeReason() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reason
}

func (c *fakeConn) output() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	var b []byte
	for _, f := range c.out {
		if f.typ == wire.FrameData {
			b = append(b, f.p...)
		}
	}
	return b
}

// dbJournal is the runtime's module journal over the test database.
type dbJournal struct {
	db *store.DB
	j  *lifecycle.AgentJournal
}

func (d dbJournal) Record(ctx context.Context, e core.JournalEntry) error {
	return d.db.Write(ctx, func(tx *sql.Tx) error { return d.j.Append(ctx, tx, e) })
}

type rig struct {
	t       *testing.T
	m       *module
	dir     string
	db      *store.DB
	journal *lifecycle.AgentJournal
	clock   *coretest.FakeClock
	shell   *fakeShell
	log     bytes.Buffer
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{t: t, m: Module().(*module), dir: t.TempDir(), clock: coretest.NewFakeClock(t0), shell: &fakeShell{}}
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(r.dir, "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	set, err := store.CoreMigrations(store.SideAgent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Migrate(ctx, db, store.MigrateOptions{
		Side: store.SideAgent, BinaryVersion: "test", BackupRoot: filepath.Join(t.TempDir(), "backups"), Keep: 5, Now: t0,
	}, set, []store.MigrationSet{{Module: ID, FS: r.m.Manifest().Migrations[core.SideAgent]}}); err != nil {
		t.Fatal(err)
	}
	r.db, r.journal = db, lifecycle.NewAgentJournal(db)
	return r
}

func (r *rig) env(p core.Principal) core.Env {
	return core.Env{
		HostID: "h1", Principal: p, DB: r.db, Clock: r.clock, Shell: r.shell,
		Journal: dbJournal{r.db, r.journal}, Log: slog.New(slog.NewTextHandler(&r.log, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
}

// open starts a terminal; its handler's error arrives on the channel.
func (r *rig) open(p core.Principal) (*fakeConn, chan error) {
	return r.openCtx(context.Background(), p)
}

func (r *rig) openCtx(ctx context.Context, p core.Principal) (*fakeConn, chan error) {
	c := newFakeConn()
	done := make(chan error, 1)
	def := r.m.Channels()[0]
	go func() { done <- def.Open(ctx, r.env(p), json.RawMessage(`{"cols":80,"rows":24}`), c) }()
	return c, done
}

// waitTimers waits until n idle timers are armed.
func (r *rig) waitTimers(n int) {
	r.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for r.clock.Pending() < n {
		if time.Now().After(deadline) {
			r.t.Fatalf("%d idle timers armed, want %d", r.clock.Pending(), n)
		}
		time.Sleep(time.Millisecond)
	}
}

func waitDone(t *testing.T, done chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("the terminal failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the terminal never ended")
	}
}

func (r *rig) entries() []core.JournalEntry {
	r.t.Helper()
	es, err := r.journal.Entries(context.Background(), 0, 0)
	if err != nil {
		r.t.Fatal(err)
	}
	return es
}

func TestTerminalLimits(t *testing.T) {
	r := newRig(t)
	conns := make([]*fakeConn, MaxHostTerminals)
	dones := make([]chan error, MaxHostTerminals)
	for i := range conns {
		conns[i], dones[i] = r.open(adminP)
		r.waitTimers(i + 1)
	}
	fifth, done5 := r.open(adminP)
	waitDone(t, done5)
	if got := fifth.closeReason(); got != "console.limit" {
		t.Errorf("the fifth terminal closed with %q, want console.limit", got)
	}
	if n := r.shell.count(); n != MaxHostTerminals {
		t.Errorf("%d shells started, want %d", n, MaxHostTerminals)
	}

	r.clock.Advance(20 * time.Minute)
	conns[0].send(t, wire.FrameResize, []byte(`{"cols":100,"rows":30}`))
	conns[1].send(t, wire.FrameData, []byte("x"))
	for r.shell.session(1).received() != "x" {
		time.Sleep(time.Millisecond)
	}
	r.clock.Advance(10 * time.Minute)
	for _, i := range []int{0, 2, 3} {
		waitDone(t, dones[i])
		if got := conns[i].closeReason(); got != "idle" {
			t.Errorf("terminal %d closed with %q, want idle", i, got)
		}
	}
	if got := r.shell.session(0).sizes; len(got) != 1 || got[0] != [2]uint16{100, 30} {
		t.Errorf("terminal 0 sizes = %v, want one resize to 100x30", got)
	}
	if got := conns[1].closeReason(); got != "" {
		t.Fatalf("terminal 1, sent data 10 minutes ago, closed with %q", got)
	}

	// The places the idle terminals held are free again.
	again, doneAgain := r.open(adminP)
	r.waitTimers(2)
	r.clock.Advance(20 * time.Minute)
	waitDone(t, dones[1])
	if got := conns[1].closeReason(); got != "idle" {
		t.Errorf("terminal 1 closed with %q, want idle 30 minutes after its data", got)
	}
	again.send(t, wire.FrameClose, []byte(`{"reason":"browser gone"}`))
	waitDone(t, doneAgain)
	if got := again.closeReason(); got != "browser gone" {
		t.Errorf("a peer close frame closed with %q, want its reason", got)
	}
}

func TestTerminalJournal(t *testing.T) {
	r := newRig(t)
	const marker = "m4rk3r-typed-secret"
	conn, done := r.open(adminP)
	r.waitTimers(1)
	conn.send(t, wire.FrameData, []byte("echo "+marker+"\n"))
	for !bytes.Contains(conn.output(), []byte(marker)) {
		time.Sleep(time.Millisecond)
	}
	r.clock.Advance(90 * time.Second)
	r.shell.session(0).exit(3)
	waitDone(t, done)
	if got := conn.closeReason(); got != "ended" {
		t.Errorf("a shell that exited closed the channel with %q, want ended", got)
	}
	var exit []byte
	for _, f := range conn.out {
		if f.typ == wire.FrameExit {
			exit = f.p
		}
	}
	if string(exit) != `{"code":3}` {
		t.Errorf("exit frame %q, want {\"code\":3}", exit)
	}

	n := int64(len("echo " + marker + "\n"))
	es := r.entries()
	want := []struct {
		action, phase, input string
	}{
		{ActionTerminalOpen, "open", `{"source":"192.0.2.7"}`},
		{ActionTerminalClose, "close", `{"source":"192.0.2.7","reason":"ended","durationMs":90000,"bytesIn":` +
			itoa(n) + `,"bytesOut":` + itoa(n) + `}`},
	}
	if len(es) != len(want) {
		t.Fatalf("journal = %+v, want %d entries", es, len(want))
	}
	for i, w := range want {
		e := es[i]
		if e.Action != w.action || e.Phase != w.phase || e.Actor != adminP.Actor || e.Source != adminP.Source || string(e.Input) != w.input {
			t.Errorf("entry %d = %s %s %s %s %s, want %s %s %s %s %s", i, e.Action, e.Phase, e.Actor, e.Source, e.Input,
				w.action, w.phase, adminP.Actor, adminP.Source, w.input)
		}
	}

	t.Run("agent restarted", func(t *testing.T) {
		conn, done := r.open(adminP)
		r.waitTimers(1)
		// The running process's own terminal is not a leftover.
		if err := r.m.OnEnabled(context.Background(), r.env(core.Principal{}), true); err != nil {
			t.Fatal(err)
		}
		if got := len(r.entries()); got != 3 {
			t.Fatalf("%d entries after the hook, want 3: a running terminal was closed", got)
		}
		// A new process over the same database finds the terminal left open.
		restarted := Module().(*module)
		if err := restarted.OnEnabled(context.Background(), r.env(core.Principal{}), false); err != nil {
			t.Fatal(err)
		}
		es := r.entries()
		last := es[len(es)-1]
		if len(es) != 4 || last.Action != ActionTerminalClose || last.Phase != "close" || last.Actor != adminP.Actor ||
			string(last.Input) != `{"source":"192.0.2.7","reason":"agent restarted"}` {
			t.Errorf("entries = %+v, want a fourth: the close with reason agent restarted and no duration", es)
		}
		if err := restarted.OnEnabled(context.Background(), r.env(core.Principal{}), false); err != nil || len(r.entries()) != 4 {
			t.Errorf("a second start journaled again: %d entries, %v", len(r.entries()), err)
		}
		conn.send(t, wire.FrameClose, []byte(`{"reason":"revoked"}`))
		waitDone(t, done)
	})

	t.Run("closed by the agent", func(t *testing.T) {
		ctx, cancel := context.WithCancelCause(context.Background())
		conn, done := r.openCtx(ctx, adminP)
		r.waitTimers(1)
		cancel(&core.ChannelClosed{Reason: "module turned off"})
		waitDone(t, done)
		es := r.entries()
		if last := es[len(es)-1]; last.Action != ActionTerminalClose ||
			!bytes.Contains(last.Input, []byte(`"reason":"module turned off"`)) || conn.closeReason() != "module turned off" {
			t.Errorf("the close is %s %s with the channel closed %q, want the agent's reason", last.Action, last.Input, conn.closeReason())
		}
	})

	// No byte typed in the terminal is in the log or the database.
	if bytes.Contains(r.log.Bytes(), []byte(marker)) {
		t.Errorf("the log holds the terminal's content: %s", r.log.String())
	}
	if err := r.db.Close(); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(r.dir, "*"))
	for _, f := range files {
		data, err := os.ReadFile(f) //nolint:gosec // G304: the test's own files
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte(marker)) {
			t.Errorf("%s holds the terminal's content", filepath.Base(f))
		}
	}
}

func TestTerminalNeedsAdmin(t *testing.T) {
	r := newRig(t)
	conn, done := r.open(core.Principal{Actor: "user:op", UserID: "op", Scope: core.ScopeOperate, Source: "192.0.2.8"})
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("an operator opened a terminal")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the refusal never came")
	}
	if r.shell.count() != 0 || len(r.entries()) != 0 || conn.closeReason() != "" {
		t.Errorf("a refused open started %d shells and journaled %d entries", r.shell.count(), len(r.entries()))
	}
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }
