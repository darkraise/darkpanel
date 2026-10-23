package events

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// PollInterval is how often StartWatchers reads core_changes and
// core_module_state.
const PollInterval = time.Second

// pollOverlap is how far before the previous poll a poll reads
// core_changes again: a writer stamps updated_at before its transaction
// commits, so a row can become visible with a time a poll has already
// passed. Rows seen in the overlap are recognised by their time and state.
const pollOverlap = 5 * time.Second

// agentReadTimeout bounds AgentChanges' reads of one page's changes.
const agentReadTimeout = 30 * time.Second

// timeFormat is the stored time format of internal/core/lifecycle.
const timeFormat = "2006-01-02T15:04:05.000000000Z"

type changeSeen struct{ at, state string }

type moduleKey struct{ host, module string }

type moduleSeen struct{ enabled, available bool }

// watcher polls the controller database for change and module events.
type watcher struct {
	db      *store.DB
	clock   core.Clock
	bus     *Bus
	visible func(core.ChangeView) bool
	log     *slog.Logger

	mu      sync.Mutex
	stopped bool
	timer   core.Timer
	last    time.Time // the clock at the previous poll
	changes map[string]changeSeen
	modules map[moduleKey]moduleSeen
}

// StartWatchers publishes ChangeEvent for every core_changes row whose
// updated_at passed the last poll (rows older than the start publish
// nothing) and that visible accepts, and ModuleEvent for every
// core_module_state row whose enabled or available differs from the last
// poll (the first poll only records), every PollInterval on clock; stop
// waits for a running poll.
func StartWatchers(db *store.DB, clock core.Clock, bus *Bus, visible func(core.ChangeView) bool, log *slog.Logger) (stop func()) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	w := &watcher{db: db, clock: clock, bus: bus, visible: visible, log: log, last: clock.Now(),
		changes: map[string]changeSeen{}, modules: map[moduleKey]moduleSeen{}}
	w.mu.Lock()
	w.poll(false)
	w.timer = clock.AfterFunc(PollInterval, w.tick)
	w.mu.Unlock()
	return func() {
		w.mu.Lock()
		defer w.mu.Unlock()
		w.stopped = true
		w.timer.Stop()
	}
}

func (w *watcher) tick() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopped {
		return
	}
	w.poll(true)
	w.timer = w.clock.AfterFunc(PollInterval, w.tick)
}

// poll reads both tables, publishing what changed when publish is set. A
// failed read is logged; the next poll reads again.
func (w *watcher) poll(publish bool) {
	now := w.clock.Now()
	from := w.last
	if now.Before(from) {
		from = now
	}
	from = from.Add(-pollOverlap)
	if err := w.pollChanges(from, publish); err != nil {
		w.log.Warn("events: read the changes", "err", err)
	} else {
		w.last = now
	}
	if err := w.pollModules(publish); err != nil {
		w.log.Warn("events: read the module states", "err", err)
	}
}

func (w *watcher) pollChanges(from time.Time, publish bool) error {
	cutoff := from.UTC().Format(timeFormat)
	rows, err := w.db.Read().QueryContext(context.Background(),
		"SELECT id, host_id, action, version, state, updated_at FROM core_changes WHERE updated_at >= ? ORDER BY updated_at, id", cutoff)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	var out []ChangeEvent
	lost := false
	read := map[string]changeSeen{}
	for rows.Next() {
		var ev ChangeEvent
		var version int
		var at string
		if err := rows.Scan(&ev.ChangeID, &ev.HostID, &ev.Action, &version, &ev.State, &at); err != nil {
			return err
		}
		seen := changeSeen{at: at, state: ev.State}
		if w.changes[ev.ChangeID] == seen {
			continue
		}
		read[ev.ChangeID] = seen
		if !w.visible(core.ChangeView{ID: ev.ChangeID, HostID: ev.HostID, Action: ev.Action, Version: version, State: ev.State}) {
			continue
		}
		if ev.UpdatedAt, err = time.Parse(timeFormat, at); err != nil {
			w.log.Warn("events: a change's updated_at does not parse; its event is skipped", "change", ev.ChangeID, "updatedAt", at, "err", err)
			lost = true
			continue
		}
		out = append(out, ev)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for id, seen := range read {
		w.changes[id] = seen
	}
	for id, seen := range w.changes {
		if seen.at < cutoff {
			delete(w.changes, id)
		}
	}
	if publish {
		for _, ev := range out {
			w.bus.Publish(TypeChange, ev)
		}
		if lost {
			w.bus.Publish(TypeReset, resetData)
		}
	}
	return nil
}

func (w *watcher) pollModules(publish bool) error {
	rows, err := w.db.Read().QueryContext(context.Background(),
		"SELECT host_id, module_id, enabled, available FROM core_module_state ORDER BY host_id, module_id")
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	current := map[moduleKey]moduleSeen{}
	var out []ModuleEvent
	for rows.Next() {
		var ev ModuleEvent
		if err := rows.Scan(&ev.HostID, &ev.ModuleID, &ev.Enabled, &ev.Available); err != nil {
			return err
		}
		k, s := moduleKey{ev.HostID, ev.ModuleID}, moduleSeen{ev.Enabled, ev.Available}
		current[k] = s
		if prev, ok := w.modules[k]; !ok || prev != s {
			out = append(out, ev)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	w.modules = current
	if publish {
		for _, ev := range out {
			w.bus.Publish(TypeModule, ev)
		}
	}
	return nil
}

// AgentChanges is an audit OnEntries piece: for each distinct change id of a
// page it reads the change through change and publishes its ChangeEvent
// when visible accepts it; a failed read is logged and skipped, and one
// reset after the page's events tells the subscribers to refetch. OnEntries
// runs under the ingestion lock and a read is a request to the agent, so the
// reads run on their own goroutine.
func AgentChanges(bus *Bus, change func(ctx context.Context, hostID, changeID string) (core.ChangeView, error),
	visible func(core.ChangeView) bool, log *slog.Logger) func(hostID string, entries []core.JournalEntry) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return func(hostID string, entries []core.JournalEntry) {
		var ids []string
		seen := map[string]bool{}
		for _, e := range entries {
			if e.ChangeID != "" && !seen[e.ChangeID] {
				seen[e.ChangeID] = true
				ids = append(ids, e.ChangeID)
			}
		}
		if len(ids) == 0 {
			return
		}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), agentReadTimeout)
			defer cancel()
			lost := false
			for _, id := range ids {
				v, err := change(ctx, hostID, id)
				if err != nil {
					log.Warn("events: read an ingested change; its event is skipped", "host", hostID, "change", id, "err", err)
					lost = true
					continue
				}
				if !visible(v) {
					continue
				}
				bus.Publish(TypeChange, ChangeEvent{HostID: hostID, ChangeID: id, Action: v.Action, State: v.State, UpdatedAt: v.UpdatedAt})
			}
			if lost {
				bus.Publish(TypeReset, resetData)
			}
		}()
	}
}
