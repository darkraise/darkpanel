package audit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
)

// IngestInterval is how often a started Ingester reads every host's journal
// (spec section 20).
const IngestInterval = 5 * time.Second

// journalPage is how many journal entries one Journal call asks for.
const journalPage = 500

// IngesterConfig wires an Ingester.
type IngesterConfig struct {
	Store  *Store             // the controller's audit log, whose database lists the hosts
	Agents core.HostDirectory // the agents whose journals are read
	Clock  core.Clock
	Log    *slog.Logger
}

// Ingester copies agent journals into core_audit (ADR 0003, spec section
// 11): it reads the entries after the host's core_journal_cursor, stores them
// with the cursor in one transaction, and then acknowledges the cursor so the
// agent deletes them. Its methods are safe for concurrent use; ingestion runs
// one at a time.
type Ingester struct {
	cfg IngesterConfig

	// mu is held for a whole ingestion run, so runs never overlap and Stop
	// waits for a running one.
	mu      sync.Mutex
	acked   map[string]int64 // the last sequence acknowledged per host by this process
	timer   core.Timer
	started bool
	stopped bool
}

// NewIngester returns an ingester for cfg. Call Start to ingest every
// IngestInterval.
func NewIngester(cfg IngesterConfig) *Ingester {
	return &Ingester{cfg: cfg, acked: map[string]int64{}}
}

// IngestHost ingests hostID's journal now; the dispatcher calls it after each
// agent apply, confirm and revert. After Stop it does nothing.
func (in *Ingester) IngestHost(ctx context.Context, hostID string) error {
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.stopped {
		return nil
	}
	return in.ingestHost(ctx, hostID)
}

// IngestAll ingests the journal of every host in core_hosts. A host that
// fails does not stop the others; their errors are joined. After Stop it
// does nothing.
func (in *Ingester) IngestAll(ctx context.Context) error {
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.stopped {
		return nil
	}
	return in.ingestAll(ctx)
}

// Start ingests every host each IngestInterval until Stop. A second Start
// does nothing.
func (in *Ingester) Start() {
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.started || in.stopped {
		return
	}
	in.started = true
	in.timer = in.cfg.Clock.AfterFunc(IngestInterval, in.tick)
}

// Stop ends the loop and waits for a running ingestion to finish. Later
// calls ingest nothing.
func (in *Ingester) Stop() {
	in.mu.Lock()
	defer in.mu.Unlock()
	in.stopped = true
	if in.timer != nil {
		in.timer.Stop()
	}
}

func (in *Ingester) tick() {
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.stopped {
		return
	}
	if err := in.ingestAll(context.Background()); err != nil {
		in.cfg.Log.Warn("audit: journal ingestion", "error", err)
	}
	in.timer = in.cfg.Clock.AfterFunc(IngestInterval, in.tick)
}

func (in *Ingester) ingestAll(ctx context.Context) error {
	rows, err := in.cfg.Store.db.Read().QueryContext(ctx, "SELECT id FROM core_hosts ORDER BY id")
	if err != nil {
		return fmt.Errorf("audit: list hosts: %w", err)
	}
	var hosts []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return fmt.Errorf("audit: list hosts: %w", err)
		}
		hosts = append(hosts, id)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return fmt.Errorf("audit: list hosts: %w", err)
	}
	var errs []error
	for _, id := range hosts {
		if err := in.ingestHost(ctx, id); err != nil {
			errs = append(errs, fmt.Errorf("audit: ingest host %s: %w", id, err))
		}
	}
	return errors.Join(errs...)
}

// ingestHost runs under mu.
func (in *Ingester) ingestHost(ctx context.Context, hostID string) error {
	agent, err := in.cfg.Agents.Agent(ctx, hostID)
	if err != nil {
		return err
	}
	cursor, err := in.cfg.Store.cursor(ctx, hostID)
	if err != nil {
		return err
	}
	for {
		entries, err := agent.Journal(ctx, cursor, journalPage)
		if err != nil {
			return fmt.Errorf("audit: read the journal of %s: %w", hostID, err)
		}
		if len(entries) == 0 {
			break
		}
		prev := cursor
		for _, e := range entries {
			if e.Seq <= prev {
				return fmt.Errorf("audit: the journal of %s returned sequence %d after %d", hostID, e.Seq, prev)
			}
			prev = e.Seq
		}
		if err := in.cfg.Store.advance(ctx, hostID, entries, prev); err != nil {
			return err
		}
		cursor = prev
		if len(entries) < journalPage {
			break
		}
	}
	// A previous process may have stored entries and stopped before its Ack,
	// so the first run of this process acknowledges the stored cursor too.
	if cursor > in.acked[hostID] {
		if err := agent.Ack(ctx, cursor); err != nil {
			return fmt.Errorf("audit: acknowledge the journal of %s: %w", hostID, err)
		}
		in.acked[hostID] = cursor
	}
	return nil
}

// cursor is the highest journal sequence of hostID stored in core_audit, or
// 0 before the first ingestion.
func (s *Store) cursor(ctx context.Context, hostID string) (int64, error) {
	var seq int64
	err := s.db.Read().QueryRowContext(ctx,
		"SELECT acked_seq FROM core_journal_cursor WHERE host_id = ?", hostID).Scan(&seq)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return 0, nil
	case err != nil:
		return 0, fmt.Errorf("audit: read the journal cursor of %s: %w", hostID, err)
	}
	return seq, nil
}

// advance stores entries of hostID and moves its cursor to seq in one
// transaction, so a crash between the two can neither lose nor repeat rows.
func (s *Store) advance(ctx context.Context, hostID string, entries []core.JournalEntry, seq int64) error {
	return s.db.Write(ctx, func(tx *sql.Tx) error {
		if err := s.ingest(ctx, tx, hostID, entries); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx,
			`INSERT INTO core_journal_cursor (host_id, acked_seq) VALUES (?, ?)
			 ON CONFLICT (host_id) DO UPDATE SET acked_seq = max(acked_seq, excluded.acked_seq)`, hostID, seq)
		if err != nil {
			return fmt.Errorf("audit: advance the journal cursor of %s: %w", hostID, err)
		}
		return nil
	})
}
