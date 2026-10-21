package audit

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// memJournal is an agent journal in memory behind a coretest.FakeAgent.
type memJournal struct {
	mu      sync.Mutex
	entries []core.JournalEntry
	acks    []int64
	reads   []int64 // the afterSeq of every Journal call
	// entered, when set, receives a value as Journal starts; Journal then
	// waits for release.
	entered chan struct{}
	release chan struct{}
}

func (m *memJournal) add(n int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	next := int64(1)
	if len(m.entries) > 0 {
		next = m.entries[len(m.entries)-1].Seq + 1
	}
	if len(m.acks) > 0 {
		next = max(next, m.acks[len(m.acks)-1]+1)
	}
	for i := range n {
		m.entries = append(m.entries, journalEntry(next+int64(i), t0.Add(time.Duration(next+int64(i))*time.Second)))
	}
}

func (m *memJournal) agent() *coretest.FakeAgent {
	return &coretest.FakeAgent{
		JournalFunc: func(_ context.Context, afterSeq int64, limit int) ([]core.JournalEntry, error) {
			if m.entered != nil {
				m.entered <- struct{}{}
				<-m.release
			}
			m.mu.Lock()
			defer m.mu.Unlock()
			m.reads = append(m.reads, afterSeq)
			var out []core.JournalEntry
			for _, e := range m.entries {
				if e.Seq > afterSeq && len(out) < limit {
					out = append(out, e)
				}
			}
			return out, nil
		},
		AckFunc: func(_ context.Context, seq int64) error {
			m.mu.Lock()
			defer m.mu.Unlock()
			m.acks = append(m.acks, seq)
			var kept []core.JournalEntry
			for _, e := range m.entries {
				if e.Seq > seq {
					kept = append(kept, e)
				}
			}
			m.entries = kept
			return nil
		},
	}
}

func (m *memJournal) state() (acks, reads []int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]int64(nil), m.acks...), append([]int64(nil), m.reads...)
}

func addHost(t *testing.T, db *store.DB, id string) {
	t.Helper()
	if _, err := db.Writer().Exec(
		"INSERT INTO core_hosts (id, name, is_local, created_at) VALUES (?, ?, 0, '2026-10-17T00:00:00Z')", id, id); err != nil {
		t.Fatal(err)
	}
}

type ingestEnv struct {
	store    *Store
	db       *store.DB
	clock    *coretest.FakeClock
	hosts    coretest.MapHosts
	ingester *Ingester
}

func newIngestEnv(t *testing.T) *ingestEnv {
	t.Helper()
	s, db, clock := newTestStore(t)
	e := &ingestEnv{store: s, db: db, clock: clock, hosts: coretest.MapHosts{}}
	e.ingester = e.newIngester()
	return e
}

func (e *ingestEnv) newIngester() *Ingester {
	return NewIngester(IngesterConfig{Store: e.store, Agents: e.hosts, Clock: e.clock, Log: slog.New(slog.DiscardHandler)})
}

func (e *ingestEnv) host(t *testing.T, id string) *memJournal {
	t.Helper()
	addHost(t, e.db, id)
	m := &memJournal{}
	e.hosts[id] = m.agent()
	return m
}

func cursorOf(t *testing.T, db *store.DB, hostID string) int64 {
	t.Helper()
	var seq int64
	if err := db.Read().QueryRow("SELECT acked_seq FROM core_journal_cursor WHERE host_id = ?", hostID).Scan(&seq); err != nil {
		t.Fatalf("cursor of %s: %v", hostID, err)
	}
	return seq
}

func TestIngestHostStoresAdvancesAndAcks(t *testing.T) {
	e := newIngestEnv(t)
	ctx := context.Background()
	m := e.host(t, "h1")
	m.add(3)
	if err := e.ingester.IngestHost(ctx, "h1"); err != nil {
		t.Fatal(err)
	}
	if n := count(t, e.db, "SELECT count(*) FROM core_audit WHERE host_id = 'h1'"); n != 3 {
		t.Errorf("audit rows = %d, want 3", n)
	}
	if c := cursorOf(t, e.db, "h1"); c != 3 {
		t.Errorf("cursor = %d, want 3", c)
	}
	if err := e.ingester.IngestHost(ctx, "h1"); err != nil {
		t.Fatal(err)
	}
	acks, reads := m.state()
	if !reflect.DeepEqual(acks, []int64{3}) || !reflect.DeepEqual(reads, []int64{0, 3}) {
		t.Errorf("acks %v reads %v, want acks [3] and reads [0 3]", acks, reads)
	}
	got, _ := list(t, e.store, core.AuditQuery{HostID: "h1", Limit: 1})
	if len(got) != 1 || got[0].Seq != 3 || got[0].Source != "192.0.2.1" || got[0].HostID != "h1" {
		t.Errorf("newest entry = %+v, want seq 3 of h1 with its source", got)
	}
}

func TestIngestHostReadsEveryPage(t *testing.T) {
	e := newIngestEnv(t)
	m := e.host(t, "h1")
	m.add(2*journalPage + 1)
	if err := e.ingester.IngestHost(context.Background(), "h1"); err != nil {
		t.Fatal(err)
	}
	if n := count(t, e.db, "SELECT count(*) FROM core_audit"); n != 2*journalPage+1 {
		t.Errorf("audit rows = %d, want %d", n, 2*journalPage+1)
	}
	acks, reads := m.state()
	if !reflect.DeepEqual(acks, []int64{2*journalPage + 1}) || !reflect.DeepEqual(reads, []int64{0, journalPage, 2 * journalPage}) {
		t.Errorf("acks %v reads %v, want one ack of the last entry after three pages", acks, reads)
	}
}

func TestIngestAcksTheStoredCursorAfterARestart(t *testing.T) {
	e := newIngestEnv(t)
	ctx := context.Background()
	m := e.host(t, "h1")
	m.add(5)
	// A run that stored the entries and stopped before its Ack.
	m.mu.Lock()
	entries := append([]core.JournalEntry(nil), m.entries...)
	m.mu.Unlock()
	if err := e.store.advance(ctx, "h1", "", entries, 5); err != nil {
		t.Fatal(err)
	}
	if err := e.newIngester().IngestHost(ctx, "h1"); err != nil {
		t.Fatal(err)
	}
	acks, reads := m.state()
	if !reflect.DeepEqual(acks, []int64{5}) || !reflect.DeepEqual(reads, []int64{5}) {
		t.Errorf("acks %v reads %v, want the stored cursor acknowledged once", acks, reads)
	}
	if n := count(t, e.db, "SELECT count(*) FROM core_audit"); n != 5 {
		t.Errorf("audit rows = %d, want 5", n)
	}
}

func TestIngestRefusesASequenceThatDoesNotAdvance(t *testing.T) {
	e := newIngestEnv(t)
	addHost(t, e.db, "h1")
	e.hosts["h1"] = &coretest.FakeAgent{
		JournalFunc: func(context.Context, int64, int) ([]core.JournalEntry, error) {
			return []core.JournalEntry{journalEntry(1, t0)}, nil
		},
		AckFunc: func(context.Context, int64) error { return nil },
	}
	ctx := context.Background()
	if err := e.ingester.IngestHost(ctx, "h1"); err != nil {
		t.Fatal(err)
	}
	if err := e.ingester.IngestHost(ctx, "h1"); err != nil {
		t.Fatalf("IngestHost = %v, want the failure reported, not returned", err)
	}
	if got := ingestStatus(t, e.db, "h1"); !strings.Contains(got, "sequence 1 after 1") {
		t.Errorf("ingestion status = %q, want a refused sequence", got)
	}
}

func TestIngestAllContinuesPastAFailingHost(t *testing.T) {
	e := newIngestEnv(t)
	addHost(t, e.db, "h1") // no agent: the directory answers not_found
	m := e.host(t, "h2")
	m.add(2)
	if err := e.ingester.IngestAll(context.Background()); err != nil {
		t.Fatalf("IngestAll = %v, want each host's failure reported, not returned", err)
	}
	if got, want := ingestStatus(t, e.db, "h1"), "2026-10-17T12:00:00.000000000Z audit: ingest host h1: not_found: host h1 not found"; got != want {
		t.Errorf("h1 status = %q, want %q", got, want)
	}
	if got, want := ingestStatus(t, e.db, "h2"), "2026-10-17T12:00:00.000000000Z <null>"; got != want {
		t.Errorf("h2 status = %q, want %q", got, want)
	}
	if n := count(t, e.db, "SELECT count(*) FROM core_audit WHERE host_id = 'h2'"); n != 2 {
		t.Errorf("h2 rows = %d, want 2", n)
	}
}

// ingestStatus is hostID's core_ingest_status row as "<since> <error>", with
// <null> for a NULL error, or "<none>" when the host has no row.
func ingestStatus(t *testing.T, db *store.DB, hostID string) string {
	t.Helper()
	var since string
	var msg sql.NullString
	err := db.Read().QueryRow("SELECT since, error FROM core_ingest_status WHERE host_id = ?", hostID).Scan(&since, &msg)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "<none>"
	case err != nil:
		t.Fatalf("ingestion status of %s: %v", hostID, err)
	}
	if !msg.Valid {
		return since + " <null>"
	}
	return since + " " + msg.String
}

// TestIngestReportsEachStateChange: a host's ingestion state is stored and
// logged when it changes, whether the loop or the dispatcher ran the
// ingestion; a repeated failure is logged at debug and keeps the row and its
// since.
func TestIngestReportsEachStateChange(t *testing.T) {
	e := newIngestEnv(t)
	addHost(t, e.db, "h1")
	var fail error
	e.hosts["h1"] = &coretest.FakeAgent{JournalFunc: func(context.Context, int64, int) ([]core.JournalEntry, error) {
		return nil, fail
	}}
	logs := &logRecords{}
	in := NewIngester(IngesterConfig{Store: e.store, Agents: e.hosts, Clock: e.clock, Log: slog.New(logs)})
	scheduled := func(ctx context.Context) error { return in.IngestAll(ctx) }
	dispatched := func(ctx context.Context) error { return in.IngestHost(ctx, "h1") }
	unreachable, refused := errors.New("agent unreachable"), errors.New("agent refused")
	const failed = "audit: ingest host h1: audit: read the journal of h1: "
	steps := []struct {
		name   string
		run    func(context.Context) error
		fail   error
		logs   []string
		status string
	}{
		{"first success", scheduled, nil, nil, "2026-10-17T12:00:01.000000000Z <null>"},
		{"first failure", dispatched, unreachable, []string{"WARN audit: journal ingestion failing host=h1"},
			"2026-10-17T12:00:02.000000000Z " + failed + "agent unreachable"},
		{"the same failure", scheduled, unreachable, []string{"DEBUG audit: journal ingestion still failing host=h1"},
			"2026-10-17T12:00:02.000000000Z " + failed + "agent unreachable"},
		{"another failure", dispatched, refused, []string{"WARN audit: journal ingestion failing host=h1"},
			"2026-10-17T12:00:04.000000000Z " + failed + "agent refused"},
		{"recovery", scheduled, nil, []string{"INFO audit: journal ingestion recovered host=h1"},
			"2026-10-17T12:00:05.000000000Z <null>"},
		{"another success", dispatched, nil, nil, "2026-10-17T12:00:05.000000000Z <null>"},
	}
	for _, s := range steps {
		e.clock.Advance(time.Second)
		fail = s.fail
		before := len(logs.lines())
		if err := s.run(context.Background()); err != nil {
			t.Fatalf("%s: %v, want the result reported, not returned", s.name, err)
		}
		if got := logs.lines()[before:]; !slices.Equal(got, s.logs) {
			t.Errorf("%s: logs = %q, want %q", s.name, got, s.logs)
		}
		if got := ingestStatus(t, e.db, "h1"); got != s.status {
			t.Errorf("%s: status = %q, want %q", s.name, got, s.status)
		}
	}
}

func TestIngesterRunsEveryIntervalUntilStopped(t *testing.T) {
	e := newIngestEnv(t)
	m := e.host(t, "h1")
	e.ingester.Start()
	e.ingester.Start() // a second Start arms nothing more
	if p := e.clock.Pending(); p != 1 {
		t.Fatalf("armed timers = %d, want 1", p)
	}
	m.add(1)
	e.clock.Advance(IngestInterval)
	m.add(1)
	e.clock.Advance(IngestInterval)
	if n := count(t, e.db, "SELECT count(*) FROM core_audit"); n != 2 {
		t.Errorf("audit rows after two intervals = %d, want 2", n)
	}
	e.ingester.Stop()
	m.add(1)
	e.clock.Advance(IngestInterval)
	if n := count(t, e.db, "SELECT count(*) FROM core_audit"); n != 2 {
		t.Errorf("audit rows after Stop = %d, want 2", n)
	}
	if p := e.clock.Pending(); p != 0 {
		t.Errorf("armed timers after Stop = %d, want 0", p)
	}
	if err := e.ingester.IngestHost(context.Background(), "h1"); err != nil {
		t.Errorf("IngestHost after Stop = %v, want nil", err)
	}
	if _, reads := m.state(); len(reads) != 2 {
		t.Errorf("journal reads = %v, want none after Stop", reads)
	}
}

func TestStopWaitsForARunningIngestion(t *testing.T) {
	e := newIngestEnv(t)
	m := e.host(t, "h1")
	m.entered, m.release = make(chan struct{}), make(chan struct{})
	e.ingester.Start()
	go e.clock.Advance(IngestInterval)
	<-m.entered
	stopped := make(chan struct{})
	go func() {
		e.ingester.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
		t.Fatal("Stop returned while an ingestion was running")
	case <-time.After(50 * time.Millisecond):
	}
	close(m.release)
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not return after the ingestion finished")
	}
}
