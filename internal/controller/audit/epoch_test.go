package audit

import (
	"context"
	"fmt"
	"log/slog"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/dispatch"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// The controller's host directory is what reports epochs in production.
var _ Epochs = (*dispatch.Hosts)(nil)

// epochHosts is a directory that reports each host's journal epoch, as
// dispatch.Hosts does from Hello; a host without a client is offline.
type epochHosts struct {
	mu      sync.Mutex
	clients map[string]core.AgentClient
	epochs  map[string]string
}

func newEpochHosts() *epochHosts {
	return &epochHosts{clients: map[string]core.AgentClient{}, epochs: map[string]string{}}
}

func (e *epochHosts) Agent(_ context.Context, hostID string) (core.AgentClient, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if c, ok := e.clients[hostID]; ok {
		return c, nil
	}
	return nil, errcode.New(errcode.HostOffline, fmt.Sprintf("host %s is offline", hostID))
}

func (e *epochHosts) JournalEpoch(hostID string) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.epochs[hostID]
}

// connect gives hostID a session whose agent has journal m in epoch.
func (e *epochHosts) connect(hostID, epoch string, m *memJournal) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.clients[hostID], e.epochs[hostID] = m.agent(), epoch
}

// logRecords is a slog.Handler that keeps each record as
// "<level> <message> host=<host attribute>".
type logRecords struct {
	mu  sync.Mutex
	got []string
}

func (l *logRecords) Enabled(context.Context, slog.Level) bool { return true }

func (l *logRecords) Handle(_ context.Context, r slog.Record) error {
	host := ""
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "host" {
			host = a.Value.String()
		}
		return true
	})
	l.mu.Lock()
	defer l.mu.Unlock()
	l.got = append(l.got, fmt.Sprintf("%s %s host=%s", r.Level, r.Message, host))
	return nil
}

func (l *logRecords) WithAttrs([]slog.Attr) slog.Handler { return l }

func (l *logRecords) WithGroup(string) slog.Handler { return l }

func (l *logRecords) lines() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.got)
}

func newEpochIngester(e *ingestEnv, hosts *epochHosts, log *slog.Logger) *Ingester {
	return NewIngester(IngesterConfig{Store: e.store, Agents: hosts, Clock: e.clock, Log: log})
}

func epochCursorOf(t *testing.T, db *store.DB, hostID string) (string, int64) {
	t.Helper()
	var epoch string
	var seq int64
	if err := db.Read().QueryRow("SELECT epoch, acked_seq FROM core_journal_cursor WHERE host_id = ?", hostID).
		Scan(&epoch, &seq); err != nil {
		t.Fatalf("cursor of %s: %v", hostID, err)
	}
	return epoch, seq
}

func TestJournalEpochTables(t *testing.T) {
	db := openControllerDB(t)
	insert := `INSERT INTO core_audit (id, seq, epoch, at, actor, host_id, action, version, phase, result)
		VALUES (?, 1, ?, '2026-10-17T12:00:00.000000000Z', 'user:u1', 'h1', 'a.b.c', 1, 'apply', 'ok')`
	for _, row := range [][]any{{"01A", "E1"}, {"01B", "E2"}} {
		if _, err := db.Writer().Exec(insert, row...); err != nil {
			t.Fatalf("insert %v: %v", row, err)
		}
	}
	if _, err := db.Writer().Exec(insert, "01C", "E1"); err == nil {
		t.Error("a second row for host h1, epoch E1 and seq 1 was accepted")
	}
	if _, err := db.Writer().Exec(`INSERT INTO core_journal_cursor (host_id, acked_seq) VALUES ('h1', 4)`); err != nil {
		t.Fatal(err)
	}
	if epoch, seq := epochCursorOf(t, db, "h1"); epoch != "" || seq != 4 {
		t.Errorf("cursor = (%q, %d), want the empty epoch by default", epoch, seq)
	}
}

func TestIngestDeduplicatesByHostEpochAndSeq(t *testing.T) {
	s, db, _ := newTestStore(t)
	ctx := context.Background()
	for _, epoch := range []string{"E1", "E1", "E2"} {
		if err := s.IngestEpoch(ctx, "h1", epoch, []core.JournalEntry{journalEntry(1, t0), journalEntry(2, t0)}); err != nil {
			t.Fatal(err)
		}
	}
	for epoch, want := range map[string]int{"E1": 2, "E2": 2} {
		if n := count(t, db, "SELECT count(*) FROM core_audit WHERE host_id = 'h1' AND epoch = ?", epoch); n != want {
			t.Errorf("rows of epoch %s = %d, want %d", epoch, n, want)
		}
	}
}

// TestIngestRestartsAtANewEpoch: an agent whose journal was recreated
// restarts its sequence at 1 under a new epoch; the ingester starts that
// host's cursor at (epoch, 0) and acknowledges the new journal although its
// sequence is below the old epoch's acknowledgement.
func TestIngestRestartsAtANewEpoch(t *testing.T) {
	e := newIngestEnv(t)
	ctx := context.Background()
	addHost(t, e.db, "h1")
	hosts := newEpochHosts()
	in := newEpochIngester(e, hosts, slog.New(slog.DiscardHandler))
	old := &memJournal{}
	old.add(5)
	hosts.connect("h1", "E1", old)
	if err := in.IngestHost(ctx, "h1"); err != nil {
		t.Fatal(err)
	}
	if epoch, seq := epochCursorOf(t, e.db, "h1"); epoch != "E1" || seq != 5 {
		t.Fatalf("cursor = (%s, %d), want (E1, 5)", epoch, seq)
	}

	recreated := &memJournal{}
	recreated.add(3)
	hosts.connect("h1", "E2", recreated)
	if err := in.IngestHost(ctx, "h1"); err != nil {
		t.Fatal(err)
	}
	acks, reads := recreated.state()
	if !reflect.DeepEqual(reads, []int64{0}) || !reflect.DeepEqual(acks, []int64{3}) {
		t.Errorf("new journal: reads %v acks %v, want reads [0] and acks [3]", reads, acks)
	}
	if epoch, seq := epochCursorOf(t, e.db, "h1"); epoch != "E2" || seq != 3 {
		t.Errorf("cursor = (%s, %d), want (E2, 3)", epoch, seq)
	}
	for epoch, want := range map[string]int{"E1": 5, "E2": 3} {
		if n := count(t, e.db, "SELECT count(*) FROM core_audit WHERE host_id = 'h1' AND epoch = ?", epoch); n != want {
			t.Errorf("rows of epoch %s = %d, want %d", epoch, n, want)
		}
	}

	// A new epoch whose journal is still empty moves the cursor to (epoch, 0).
	hosts.connect("h1", "E3", &memJournal{})
	if err := in.IngestHost(ctx, "h1"); err != nil {
		t.Fatal(err)
	}
	if epoch, seq := epochCursorOf(t, e.db, "h1"); epoch != "E3" || seq != 0 {
		t.Errorf("cursor = (%s, %d), want (E3, 0)", epoch, seq)
	}
}

func TestIngestSkipsOfflineHosts(t *testing.T) {
	e := newIngestEnv(t)
	addHost(t, e.db, "h1")
	addHost(t, e.db, "h2")
	hosts := newEpochHosts()
	m := &memJournal{}
	m.add(2)
	hosts.connect("h2", "E1", m)
	logs := &logRecords{}
	if err := newEpochIngester(e, hosts, slog.New(logs)).IngestAll(context.Background()); err != nil {
		t.Fatalf("IngestAll = %v, want the offline host skipped", err)
	}
	if got, want := logs.lines(), []string{"DEBUG audit: offline host skipped host=h1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("logs = %q, want %q", got, want)
	}
	if n := count(t, e.db, "SELECT count(*) FROM core_audit WHERE host_id = 'h2'"); n != 2 {
		t.Errorf("h2 rows = %d, want 2", n)
	}
}

// TestIngestDeadline: a host whose agent stops answering is given up after
// IngestTimeout on the ingester's clock, and the next host is still read.
func TestIngestDeadline(t *testing.T) {
	e := newIngestEnv(t)
	addHost(t, e.db, "h1")
	addHost(t, e.db, "h2")
	hosts := newEpochHosts()
	entered := make(chan struct{})
	hosts.clients["h1"] = &coretest.FakeAgent{
		JournalFunc: func(ctx context.Context, _ int64, _ int) ([]core.JournalEntry, error) {
			close(entered)
			<-ctx.Done()
			return nil, context.Cause(ctx)
		},
	}
	m := &memJournal{}
	m.add(1)
	hosts.connect("h2", "E1", m)
	in := newEpochIngester(e, hosts, slog.New(slog.DiscardHandler))
	done := make(chan error, 1)
	go func() { done <- in.IngestAll(context.Background()) }()
	<-entered
	e.clock.Advance(IngestTimeout - time.Second)
	select {
	case err := <-done:
		t.Fatalf("IngestAll returned %v before the deadline", err)
	case <-time.After(50 * time.Millisecond):
	}
	e.clock.Advance(time.Second)
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("IngestAll = %v, want h1's deadline reported, not returned", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("IngestAll did not return after the deadline")
	}
	if got, want := ingestStatus(t, e.db, "h1"), "2026-10-17T12:00:30.000000000Z audit: ingestion exceeded 30s"; got != want {
		t.Errorf("h1 status = %q, want %q", got, want)
	}
	if n := count(t, e.db, "SELECT count(*) FROM core_audit WHERE host_id = 'h2'"); n != 1 {
		t.Errorf("h2 rows = %d, want 1", n)
	}
}
