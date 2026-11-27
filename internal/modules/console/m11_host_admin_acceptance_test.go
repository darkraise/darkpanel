package console_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/agent/wire"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/modules/console"
	"github.com/darkraise/darkpanel/internal/platform"
)

type journal struct {
	mu      sync.Mutex
	entries []core.JournalEntry
	onEntry func(core.JournalEntry)
}

func (j *journal) Record(_ context.Context, e core.JournalEntry) error {
	if j.onEntry != nil {
		j.onEntry(e)
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.entries = append(j.entries, e)
	return nil
}

func (j *journal) all() []core.JournalEntry {
	j.mu.Lock()
	defer j.mu.Unlock()
	return append([]core.JournalEntry(nil), j.entries...)
}

type conn struct {
	in     chan frame
	mu     sync.Mutex
	out    [][]byte
	reason string
	closed chan struct{}
	once   sync.Once
}

type frame struct {
	t byte
	p []byte
}

func newConn() *conn { return &conn{in: make(chan frame, 16), closed: make(chan struct{})} }

func (c *conn) ReadFrame() (byte, []byte, error) {
	select {
	case f := <-c.in:
		return f.t, f.p, nil
	case <-c.closed:
		return 0, nil, io.EOF
	}
}

func (c *conn) WriteFrame(_ byte, p []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.out = append(c.out, append([]byte(nil), p...))
	return nil
}

func (c *conn) Close(reason string) error {
	c.once.Do(func() {
		c.mu.Lock()
		c.reason = reason
		c.mu.Unlock()
		close(c.closed)
	})
	return nil
}

func (c *conn) closeReason() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reason
}

type shell struct {
	out    chan []byte
	closed chan struct{}
	once   sync.Once
	mu     sync.Mutex
	typed  bytes.Buffer
}

func newShell() *shell { return &shell{out: make(chan []byte, 8), closed: make(chan struct{})} }

func (s *shell) Read(p []byte) (int, error) {
	select {
	case b := <-s.out:
		return copy(p, b), nil
	case <-s.closed:
		return 0, io.EOF
	}
}
func (s *shell) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.typed.Write(p)
}
func (s *shell) Resize(uint16, uint16) error { return nil }
func (s *shell) Close() error                { s.once.Do(func() { close(s.closed) }); return nil }
func (s *shell) Wait() (int, error)          { <-s.closed; return 0, nil }

type starter struct {
	mu     sync.Mutex
	shells []*shell
}

func (s *starter) Start(context.Context, uint16, uint16) (core.ShellSession, error) {
	sh := newShell()
	s.mu.Lock()
	s.shells = append(s.shells, sh)
	s.mu.Unlock()
	return sh, nil
}

func (s *starter) count() int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.shells) }

var adminP = core.Principal{Actor: "user:ada", UserID: "ada", Scope: core.ScopeAdmin, Source: "198.51.100.7"}

type rig struct {
	m       core.Module
	env     core.Env
	clock   *coretest.FakeClock
	j       *journal
	shells  *starter
	root    string
	t       *testing.T
	channel core.ChannelDef
}

func newRig(t *testing.T) *rig {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	m := console.Module()
	set, err := store.CoreMigrations(store.SideAgent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Migrate(ctx, db, store.MigrateOptions{Side: store.SideAgent, BinaryVersion: "test",
		BackupRoot: filepath.Join(t.TempDir(), "backups"), Keep: 5, Now: time.Now()}, set,
		[]store.MigrationSet{{Module: "console", FS: m.Manifest().Migrations[core.SideAgent]}}); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "srv"), 0o755); err != nil {
		t.Fatal(err)
	}
	clk := coretest.NewFakeClock(time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC))
	j := &journal{}
	st := &starter{}
	fsys := platform.NewConsoleFiles(platform.ConsoleFilesConfig{Root: root, Binary: "/usr/local/bin/darkpanel", FreeSpace: func(int) (int64, error) { return 1 << 40, nil }})
	r := &rig{m: m, clock: clk, j: j, shells: st, root: root, t: t}
	r.env = core.Env{HostID: "h1", Principal: adminP, DB: db, Clock: clk, Journal: j, Shell: st, ConsoleFS: fsys, Log: nil}
	for _, c := range m.(core.Channeler).Channels() {
		if c.Name == "terminal" {
			r.channel = c
		}
	}
	return r
}

// open runs a terminal in the background and returns its conn and a channel that yields its end.
func (r *rig) open() (*conn, chan error) {
	c := newConn()
	done := make(chan error, 1)
	go func() {
		done <- r.channel.Open(context.Background(), r.env, json.RawMessage(`{"cols":80,"rows":24}`), c)
	}()
	return c, done
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestA15_AFifthTerminalOnOneHostIsRefusedWithConsoleLimit(t *testing.T) {
	r := newRig(t)
	var conns []*conn
	for i := 0; i < 4; i++ {
		c, _ := r.open()
		conns = append(conns, c)
	}
	waitFor(t, "four shells", func() bool { return r.shells.count() == 4 })
	c5, done := r.open()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the fifth terminal stayed open")
	}
	if got := c5.closeReason(); got != "console.limit" {
		t.Errorf("fifth terminal closed with %q; want console.limit", got)
	}
	if r.shells.count() != 4 {
		t.Errorf("%d shells started; want 4", r.shells.count())
	}
	for _, c := range conns {
		_ = c.Close("test over")
	}
}

func TestA16_ATerminalWithoutInputForThirtyMinutesClosesIdle(t *testing.T) {
	r := newRig(t)
	c, done := r.open()
	waitFor(t, "the shell", func() bool { return r.shells.count() == 1 && r.clock.Pending() > 0 })
	r.clock.Advance(29 * time.Minute)
	select {
	case <-done:
		t.Fatal("the terminal closed after 29 minutes")
	case <-time.After(100 * time.Millisecond):
	}
	r.clock.Advance(time.Minute + time.Second)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the terminal stayed open after 30 minutes")
	}
	if got := c.closeReason(); got != "idle" {
		t.Errorf("close reason %q; want idle", got)
	}
}

func TestA17_A18_JournalHoldsOpenAndCloseAndNoTerminalContent(t *testing.T) {
	r := newRig(t)
	c, done := r.open()
	waitFor(t, "the shell", func() bool { return r.shells.count() == 1 })
	const typed, shown = "TYPED-SECRET-4f2a", "SHOWN-SECRET-9c1d"
	c.in <- frame{wire.FrameData, []byte(typed)}
	r.shells.shells[0].out <- []byte(shown)
	waitFor(t, "the bytes to pass", func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return len(c.out) > 0
	})
	r.clock.Advance(90 * time.Second)
	c.in <- frame{wire.FrameClose, []byte(`{"reason":"closed by user"}`)}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var open, closed *core.JournalEntry
	for _, e := range r.j.all() {
		e := e
		switch e.Action {
		case "console.terminal.open":
			open = &e
		case "console.terminal.close":
			closed = &e
		}
	}
	if open == nil || closed == nil {
		t.Fatalf("journal entries %+v; want console.terminal.open and console.terminal.close", r.j.all())
	}
	if open.Actor != "user:ada" || open.Source != "198.51.100.7" {
		t.Errorf("open entry actor %q source %q", open.Actor, open.Source)
	}
	var rec struct {
		Source     string `json:"source"`
		Reason     string `json:"reason"`
		DurationMs int64  `json:"durationMs"`
		BytesIn    int64  `json:"bytesIn"`
		BytesOut   int64  `json:"bytesOut"`
	}
	if err := json.Unmarshal(closed.Input, &rec); err != nil {
		t.Fatal(err)
	}
	if closed.Actor != "user:ada" || rec.Source != "198.51.100.7" || rec.Reason != "closed by user" ||
		rec.DurationMs < 90_000 || rec.BytesIn != int64(len(typed)) || rec.BytesOut != int64(len(shown)) {
		t.Errorf("close entry actor %q, input %s", closed.Actor, closed.Input)
	}
	for _, e := range r.j.all() {
		blob, _ := json.Marshal(e)
		if strings.Contains(string(blob), typed) || strings.Contains(string(blob), shown) {
			t.Errorf("a journal entry holds terminal content: %s", blob)
		}
	}
}

func TestA17_AnOpenLeftByARestartGetsACloseWithReasonAgentRestarted(t *testing.T) {
	r := newRig(t)
	c, done := r.open()
	waitFor(t, "the open entry", func() bool {
		for _, e := range r.j.all() {
			if e.Action == "console.terminal.open" {
				return true
			}
		}
		return false
	})
	// A second module instance is the agent after a restart: it runs none of the terminals the database still holds.
	r2 := console.Module()
	_ = c
	if err := r2.(core.EnablementHook).OnEnabled(context.Background(), r.env, true); err != nil {
		t.Fatal(err)
	}
	var reasons []string
	for _, e := range r.j.all() {
		if e.Action == "console.terminal.close" {
			var rec struct {
				Reason string `json:"reason"`
			}
			_ = json.Unmarshal(e.Input, &rec)
			reasons = append(reasons, rec.Reason)
		}
	}
	if len(reasons) != 1 || reasons[0] != "agent restarted" {
		t.Errorf("close reasons %v; want [agent restarted]", reasons)
	}
	_ = c.Close("test over")
	<-done
}

func TestA26_EveryFileMutationIsJournaledBeforeItAnswers(t *testing.T) {
	r := newRig(t)
	files := r.m.(core.FileManager).Files(r.env)
	target := filepath.Join(r.root, "srv", "note.txt")
	r.j.onEntry = func(e core.JournalEntry) {
		if e.Action == "console.file.write" && e.Phase == "intent" {
			if _, err := os.Stat(target); err == nil {
				t.Error("the intent entry was journaled after the file existed")
			}
		}
	}
	data := []byte("hello journal")
	sum, err := files.WriteText(context.Background(), "/srv/note.txt", data, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := files.Rename(context.Background(), "/srv/note.txt", "/srv/note2.txt"); err != nil {
		t.Fatal(err)
	}
	if err := files.Delete(context.Background(), "/srv/note2.txt", ""); err != nil {
		t.Fatal(err)
	}
	type rec struct {
		Source string `json:"source"`
		Path   string `json:"path"`
		Size   *int64 `json:"size"`
		SHA256 string `json:"sha256"`
	}
	var phases = map[string][]string{}
	for _, e := range r.j.all() {
		if !strings.HasPrefix(e.Action, "console.file.") {
			continue
		}
		phases[e.Action] = append(phases[e.Action], e.Phase)
		if e.Actor != "user:ada" || e.Source != "198.51.100.7" {
			t.Errorf("%s: actor %q source %q", e.Action, e.Actor, e.Source)
		}
		var x rec
		_ = json.Unmarshal(e.Input, &x)
		if x.Path == "" {
			t.Errorf("%s %s has no path: %s", e.Action, e.Phase, e.Input)
		}
		if e.Action == "console.file.write" {
			if x.Size == nil || *x.Size != int64(len(data)) {
				t.Errorf("write %s: size %v", e.Phase, x.Size)
			}
			if e.Phase == "result" && x.SHA256 != sum {
				t.Errorf("write result sha %q; want %q", x.SHA256, sum)
			}
		}
	}
	for _, op := range []string{"console.file.write", "console.file.rename", "console.file.delete"} {
		if got := phases[op]; len(got) != 2 || got[0] != "intent" || got[1] != "result" {
			t.Errorf("%s phases %v; want [intent result]", op, got)
		}
	}
}

func TestA99_AnIntentWithoutAResultGetsInterruptedAtTheNextStart(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	if _, err := r.env.DB.Writer().Exec(`INSERT INTO console_file_ops (id, op, path, actor, source, started_at) VALUES ('crashed1', 'write', '/srv/x', 'user:ada', '198.51.100.7', ?)`,
		time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("console_file_ops has another shape: %v", err)
	}
	if err := console.Module().(core.EnablementHook).OnEnabled(ctx, r.env, true); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range r.j.all() {
		if e.Action == "console.file.write" && e.Phase == "result" && strings.Contains(string(e.Input), "interrupted") {
			found = true
		}
	}
	if !found {
		t.Errorf("no interrupted result entry in %+v", r.j.all())
	}
}
