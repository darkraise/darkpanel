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
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// IngestInterval is how often a started Ingester reads every host's journal
// (spec section 20).
const IngestInterval = 5 * time.Second

// IngestTimeout bounds one host's ingestion, so an agent that stops
// answering holds up neither the other hosts nor the loop.
const IngestTimeout = 30 * time.Second

// journalPage is how many journal entries one Journal call asks for.
const journalPage = 500

// errIngestTimeout ends a host's ingestion that IngestTimeout cut short.
var errIngestTimeout = fmt.Errorf("audit: ingestion exceeded %s", IngestTimeout)

// errOffline ends the ingestion of a host that answered host.offline: it is
// skipped, and its ingestion state stays as it was.
var errOffline = errors.New("audit: host offline")

// Epochs reports the journal epoch each host's agent said its last Hello
// with; *dispatch.Hosts implements it. With a directory that does not, every
// journal is in the empty epoch.
type Epochs interface {
	JournalEpoch(hostID string) string
}

// ackKey names one journal: a host's sequence starts again in a new epoch.
type ackKey struct{ host, epoch string }

// ingestState is the result of a host's last ingestion: failing with
// message, or succeeding.
type ingestState struct {
	failing bool
	message string
}

// IngesterConfig wires an Ingester.
type IngesterConfig struct {
	Store  *Store             // the controller's audit log, whose database lists the hosts
	Agents core.HostDirectory // the agents whose journals are read; see Epochs
	Clock  core.Clock
	Log    *slog.Logger
}

// Ingester copies agent journals into core_audit (ADR 0003, spec section
// 11): it reads the entries after the host's core_journal_cursor, stores them
// with the cursor in one transaction, and then acknowledges the cursor so the
// agent deletes them. When the agent reports a journal epoch other than the
// cursor's, the cursor starts again at (epoch, 0). A host that answers
// host.offline is skipped, and each host's ingestion ends after
// IngestTimeout on the configured clock. Each host's result is reported, in
// core_ingest_status and the log, whichever method ran it. Its methods are
// safe for concurrent use; ingestion runs one at a time.
type Ingester struct {
	cfg IngesterConfig

	// mu is held for a whole ingestion run, so runs never overlap and Stop
	// waits for a running one.
	mu      sync.Mutex
	acked   map[ackKey]int64       // the last sequence acknowledged per journal by this process
	states  map[string]ingestState // each host's last result stored by this process
	timer   core.Timer
	started bool
	stopped bool
}

// NewIngester returns an ingester for cfg. Call Start to ingest every
// IngestInterval.
func NewIngester(cfg IngesterConfig) *Ingester {
	return &Ingester{cfg: cfg, acked: map[ackKey]int64{}, states: map[string]ingestState{}}
}

// IngestHost ingests hostID's journal now; the dispatcher calls it after each
// agent apply, confirm and revert. It returns nil: a failure is reported as
// the loop reports it, and the loop retries it. After Stop it does nothing.
func (in *Ingester) IngestHost(ctx context.Context, hostID string) error {
	in.mu.Lock()
	defer in.mu.Unlock()
	if !in.stopped {
		in.ingestHost(ctx, hostID)
	}
	return nil
}

// IngestAll ingests the journal of every host in core_hosts. A host that
// fails does not stop the others, and its failure is reported, not returned;
// the error is a failure to list the hosts. After Stop it does nothing.
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
	for _, id := range hosts {
		in.ingestHost(ctx, id)
	}
	return nil
}

// ingestHost runs under mu and reports the result. The deadline runs on the
// configured clock, so tests reach it without waiting.
func (in *Ingester) ingestHost(ctx context.Context, hostID string) {
	run, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	timer := in.cfg.Clock.AfterFunc(IngestTimeout, func() { cancel(errIngestTimeout) })
	defer timer.Stop()
	err := in.ingest(run, hostID)
	if errors.Is(err, errOffline) {
		return
	}
	if err != nil {
		if cause := context.Cause(run); errors.Is(cause, errIngestTimeout) {
			err = cause
		}
		err = fmt.Errorf("audit: ingest host %s: %w", hostID, err)
	}
	in.report(context.WithoutCancel(ctx), hostID, err)
}

// report records hostID's ingestion result, err nil for a success. The log
// says when a host starts failing, fails differently or recovers; a repeated
// failure is logged at debug only, since the loop repeats it every
// IngestInterval. core_ingest_status is written only when the result differs
// from the last one this process stored; a write that fails is tried again
// with the next result.
func (in *Ingester) report(ctx context.Context, hostID string, err error) {
	next := ingestState{}
	if err != nil {
		next = ingestState{failing: true, message: err.Error()}
	}
	prev, seen := in.states[hostID]
	switch {
	case seen && next == prev:
		if next.failing {
			in.cfg.Log.Debug("audit: journal ingestion still failing", "host", hostID, "error", err)
		}
		return
	case next.failing:
		in.cfg.Log.Warn("audit: journal ingestion failing", "host", hostID, "error", err)
	case seen:
		in.cfg.Log.Info("audit: journal ingestion recovered", "host", hostID)
	}
	if werr := in.cfg.Store.recordIngestion(ctx, hostID, next, in.cfg.Clock.Now()); werr != nil {
		in.cfg.Log.Error("audit: record the journal ingestion state", "host", hostID, "error", werr)
		return
	}
	in.states[hostID] = next
}

func (in *Ingester) ingest(ctx context.Context, hostID string) error {
	agent, err := in.cfg.Agents.Agent(ctx, hostID)
	if errcode.From(err).Code == errcode.HostOffline {
		in.cfg.Log.Debug("audit: offline host skipped", "host", hostID)
		return errOffline
	}
	if err != nil {
		return err
	}
	epoch := ""
	if e, ok := in.cfg.Agents.(Epochs); ok {
		epoch = e.JournalEpoch(hostID)
	}
	stored, cursor, err := in.cfg.Store.cursor(ctx, hostID)
	if err != nil {
		return err
	}
	if stored != epoch {
		if err := in.cfg.Store.restart(ctx, hostID, epoch); err != nil {
			return err
		}
		cursor = 0
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
		if err := in.cfg.Store.advance(ctx, hostID, epoch, entries, prev); err != nil {
			return err
		}
		cursor = prev
		if len(entries) < journalPage {
			break
		}
	}
	// A previous process may have stored entries and stopped before its Ack,
	// so the first run of this process acknowledges the stored cursor too.
	key := ackKey{host: hostID, epoch: epoch}
	if cursor > in.acked[key] {
		if err := agent.Ack(ctx, cursor); err != nil {
			return fmt.Errorf("audit: acknowledge the journal of %s: %w", hostID, err)
		}
		in.acked[key] = cursor
	}
	return nil
}

// cursor is the journal epoch and highest sequence of hostID stored in
// core_audit, or the empty epoch and 0 before the first ingestion.
func (s *Store) cursor(ctx context.Context, hostID string) (string, int64, error) {
	var epoch string
	var seq int64
	err := s.db.Read().QueryRowContext(ctx,
		"SELECT epoch, acked_seq FROM core_journal_cursor WHERE host_id = ?", hostID).Scan(&epoch, &seq)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", 0, nil
	case err != nil:
		return "", 0, fmt.Errorf("audit: read the journal cursor of %s: %w", hostID, err)
	}
	return epoch, seq, nil
}

// recordIngestion stores hostID's ingestion state st as begun at since. A
// stored row with st's error already keeps its since, so a process that
// restarts during a failure does not move it.
func (s *Store) recordIngestion(ctx context.Context, hostID string, st ingestState, since time.Time) error {
	var msg any
	if st.failing {
		msg = st.message
	}
	err := s.db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO core_ingest_status (host_id, since, error) VALUES (?, ?, ?)
			 ON CONFLICT (host_id) DO UPDATE SET since = excluded.since, error = excluded.error
			 WHERE core_ingest_status.error IS NOT excluded.error`,
			hostID, since.UTC().Format(timeFormat), msg)
		return err
	})
	if err != nil {
		return fmt.Errorf("audit: record the ingestion state of %s: %w", hostID, err)
	}
	return nil
}

// restart moves hostID's cursor to the start of the journal epoch epoch.
func (s *Store) restart(ctx context.Context, hostID, epoch string) error {
	err := s.db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO core_journal_cursor (host_id, epoch, acked_seq) VALUES (?, ?, 0)
			 ON CONFLICT (host_id) DO UPDATE SET epoch = excluded.epoch, acked_seq = 0`, hostID, epoch)
		return err
	})
	if err != nil {
		return fmt.Errorf("audit: restart the journal cursor of %s: %w", hostID, err)
	}
	return nil
}

// advance stores entries of hostID's journal epoch epoch and moves its
// cursor to (epoch, seq) in one transaction, so a crash between the two can
// neither lose nor repeat rows.
func (s *Store) advance(ctx context.Context, hostID, epoch string, entries []core.JournalEntry, seq int64) error {
	return s.db.Write(ctx, func(tx *sql.Tx) error {
		if err := s.ingest(ctx, tx, hostID, epoch, entries); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx,
			`INSERT INTO core_journal_cursor (host_id, epoch, acked_seq) VALUES (?, ?, ?)
			 ON CONFLICT (host_id) DO UPDATE SET
			   acked_seq = CASE WHEN epoch = excluded.epoch THEN max(acked_seq, excluded.acked_seq) ELSE excluded.acked_seq END,
			   epoch = excluded.epoch`, hostID, epoch, seq)
		if err != nil {
			return fmt.Errorf("audit: advance the journal cursor of %s: %w", hostID, err)
		}
		return nil
	})
}
