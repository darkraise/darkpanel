package coretest

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"sync"

	"github.com/darkraise/darkpanel/internal/core"
)

// MemJournal is a core.Journal that keeps entries in memory. It ignores the
// transaction, so a rolled-back change keeps its entry. Set Err to make
// Append fail.
type MemJournal struct {
	mu      sync.Mutex
	entries []core.JournalEntry
	Err     error
}

// Append records e, or returns Err when it is set.
func (j *MemJournal) Append(_ context.Context, _ *sql.Tx, e core.JournalEntry) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.Err != nil {
		return j.Err
	}
	j.entries = append(j.entries, e)
	return nil
}

// Entries returns the recorded entries in order.
func (j *MemJournal) Entries() []core.JournalEntry {
	j.mu.Lock()
	defer j.mu.Unlock()
	return slices.Clone(j.entries)
}

// MemAudit is a core.Audit in memory. Ids are 26-digit counters, so they sort
// in insertion order like ULIDs; List returns the newest first.
type MemAudit struct {
	mu      sync.Mutex
	entries []core.AuditEntry
	seen    map[string]bool // "<host id>/<seq>" already ingested
	next    int
}

func (a *MemAudit) add(e core.AuditEntry) {
	a.next++
	if e.ID == "" {
		e.ID = fmt.Sprintf("%026d", a.next)
	}
	a.entries = append(a.entries, e)
}

func fromJournal(hostID string, e core.JournalEntry) core.AuditEntry {
	return core.AuditEntry{
		Actor: e.Actor, HostID: hostID, Action: e.Action, Phase: e.Phase, ChangeID: e.ChangeID,
		Result: e.Result, ErrorCode: e.ErrorCode, At: e.At, Version: e.Version, Seq: e.Seq, Input: e.Input,
	}
}

// Append records a controller-side change event.
func (a *MemAudit) Append(_ context.Context, _ *sql.Tx, e core.JournalEntry) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.add(fromJournal("", e))
	return nil
}

// Record stores e, assigning an id when it has none.
func (a *MemAudit) Record(_ context.Context, e core.AuditEntry) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.add(e)
	return nil
}

// Ingest stores agent journal entries, skipping any host and seq pair seen
// before.
func (a *MemAudit) Ingest(_ context.Context, hostID string, entries []core.JournalEntry) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.seen == nil {
		a.seen = map[string]bool{}
	}
	for _, e := range entries {
		key := fmt.Sprintf("%s/%d", hostID, e.Seq)
		if a.seen[key] {
			continue
		}
		a.seen[key] = true
		a.add(fromJournal(hostID, e))
	}
	return nil
}

// List returns entries newest first, filtered by the query's exact-match
// fields and by Before (ids below it). A zero Limit means 100. nextBefore is
// the last returned id when older entries remain, else empty.
func (a *MemAudit) List(_ context.Context, q core.AuditQuery) ([]core.AuditEntry, string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	limit := q.Limit
	if limit == 0 {
		limit = 100
	}
	var out []core.AuditEntry
	for i := len(a.entries) - 1; i >= 0; i-- {
		e := a.entries[i]
		switch {
		case q.Before != "" && e.ID >= q.Before,
			q.Actor != "" && e.Actor != q.Actor,
			q.HostID != "" && e.HostID != q.HostID,
			q.Action != "" && e.Action != q.Action:
			continue
		}
		if len(out) == limit {
			return out, out[len(out)-1].ID, nil
		}
		out = append(out, e)
	}
	return out, "", nil
}
