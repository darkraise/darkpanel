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

// Rechecker rechecks the availability of modules on a host; it is
// satisfied by *dispatch.Dispatcher.
type Rechecker interface {
	RecheckModules(ctx context.Context, hostID string, modules []string) error
}

// Option configures StartWatchers and AgentChanges.
type Option func(*options)

type options struct {
	reg       *core.Registry
	rechecker Rechecker
}

// WithRegistry gives the watchers the actions' specs, whose Rechecks name
// the modules to recheck when a change ends.
func WithRegistry(reg *core.Registry) Option { return func(o *options) { o.reg = reg } }

// WithRechecker rechecks a change's Rechecks modules when it ends.
func WithRechecker(r Rechecker) Option { return func(o *options) { o.rechecker = r } }

func newOptions(opts []Option) options {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

// recheckStates are the change states after which the modules an action
// rechecks may have changed availability; awaiting_confirmation is not one,
// since the change can still revert.
var recheckStates = map[string]bool{
	core.StateCompleted: true, core.StateConfirmed: true, core.StateReverted: true,
	core.StateFailed: true, core.StateNeedsRepair: true,
}

// rechecks returns the modules to recheck after a change of action/version
// reached state, or nil.
func (o options) rechecks(action string, version int, state string) []string {
	if o.reg == nil || o.rechecker == nil || !recheckStates[state] {
		return nil
	}
	def, ok := o.reg.Action(core.ActionRef{Name: action, Version: version})
	if !ok {
		return nil
	}
	return def.Spec.Rechecks
}

// recheck is one change that ended with modules to recheck on host.
type recheck struct {
	changeID, host string
	modules        []string
}

// watcher polls the controller database for change and module events.
type watcher struct {
	db      *store.DB
	clock   core.Clock
	bus     *Bus
	visible func(core.ChangeView) bool
	log     *slog.Logger
	opts    options

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
func StartWatchers(db *store.DB, clock core.Clock, bus *Bus, visible func(core.ChangeView) bool, log *slog.Logger, opts ...Option) (stop func()) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	w := &watcher{db: db, clock: clock, bus: bus, visible: visible, log: log, opts: newOptions(opts), last: clock.Now(),
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
	var rechecks []recheck
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
		// The recheck does not depend on the visible filter: a hidden change
		// still changes what the host has installed.
		if mods := w.opts.rechecks(ev.Action, version, ev.State); publish && len(mods) > 0 {
			rechecks = append(rechecks, recheck{changeID: ev.ChangeID, host: ev.HostID, modules: mods})
		}
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
	if len(rechecks) > 0 {
		w.startRechecks(rechecks)
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

// startRechecks rechecks each ended change's modules in the background, so
// an agent slow to answer never holds up the events. A controller-side
// composite has no host of its own; its steps' hosts are rechecked.
func (w *watcher) startRechecks(rechecks []recheck) {
	go func() {
		for _, r := range rechecks {
			hosts := []string{r.host}
			if r.host == "" {
				var err error
				if hosts, err = w.stepHosts(r.changeID); err != nil {
					w.log.Warn("events: read a composite's step hosts; its modules are not rechecked", "change", r.changeID, "err", err)
					continue
				}
			}
			for _, host := range hosts {
				ctx, cancel := context.WithTimeout(context.Background(), agentReadTimeout)
				if err := w.opts.rechecker.RecheckModules(ctx, host, r.modules); err != nil {
					w.log.Warn("events: recheck modules", "change", r.changeID, "host", host, "err", err)
				}
				cancel()
			}
		}
	}()
}

func (w *watcher) stepHosts(changeID string) ([]string, error) {
	rows, err := w.db.Read().QueryContext(context.Background(),
		"SELECT DISTINCT host_id FROM core_composite_steps WHERE change_id = ? AND host_id != '' ORDER BY host_id", changeID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var hosts []string
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, err
		}
		hosts = append(hosts, h)
	}
	return hosts, rows.Err()
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
	visible func(core.ChangeView) bool, log *slog.Logger, opts ...Option) func(hostID string, entries []core.JournalEntry) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	o := newOptions(opts)
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
				if mods := o.rechecks(v.Action, v.Version, v.State); len(mods) > 0 {
					if err := o.rechecker.RecheckModules(ctx, hostID, mods); err != nil {
						log.Warn("events: recheck modules", "host", hostID, "change", id, "err", err)
					}
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
