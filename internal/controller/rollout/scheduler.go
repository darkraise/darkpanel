package rollout

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/dispatch"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/update"
)

const (
	// ScheduleInterval is how often the scheduler looks for open update
	// windows.
	ScheduleInterval = time.Minute
	// WindowLength is how long an update window stays open after its time.
	WindowLength = 2 * time.Hour
)

// ReleaseSource lists the published releases; *update.IndexSource
// implements it.
type ReleaseSource interface {
	Releases(ctx context.Context) ([]update.Release, error)
}

// SchedulerConfig wires a Scheduler; every field but Log and Suspended is
// required.
type SchedulerConfig struct {
	DB *store.DB // the controller database
	// Clients tells which hosts are online: a host whose client answers an
	// error is offline. The controller passes its session registry.
	Clients dispatch.ClientSource
	Index   ReleaseSource
	// Audit is the controller's audit log; a claim's entry is written in
	// the claim's transaction.
	Audit   core.Journal
	Clock   core.Clock
	Log     *slog.Logger // nil discards
	Version string       // the controller's buildinfo.Version
	// Suspended reports whether automatic changes are suspended (restore
	// quarantine, spec 10): a pass that finds it true does nothing, and an
	// error counts as true. nil is never suspended.
	Suspended func(ctx context.Context) (bool, error)
}

// Scheduler starts a one-host rollout in a host's update window (spec
// section 8, Channels and schedule): once a minute, for a host inside an
// open window it has not claimed, when the index has a newer version in the
// host's channel and the host is online, it creates the rollout, records
// the window and writes the audit entry in one controller write
// transaction. A window is claimed once whatever the rollout's outcome, and
// a window that closed while the scheduler was not running is not caught
// up.
type Scheduler struct {
	cfg    SchedulerConfig
	ctx    context.Context
	cancel context.CancelFunc

	// mu is held for a whole pass, so passes never overlap and Stop waits
	// for a running one.
	mu      sync.Mutex
	timer   core.Timer
	started bool
	stopped bool
}

// NewScheduler returns a scheduler for cfg. Call Start to check the windows.
func NewScheduler(cfg SchedulerConfig) (*Scheduler, error) {
	switch {
	case cfg.DB == nil:
		return nil, errors.New("rollout: SchedulerConfig.DB is required")
	case cfg.Clients == nil:
		return nil, errors.New("rollout: SchedulerConfig.Clients is required")
	case cfg.Index == nil:
		return nil, errors.New("rollout: SchedulerConfig.Index is required")
	case cfg.Audit == nil:
		return nil, errors.New("rollout: SchedulerConfig.Audit is required")
	case cfg.Clock == nil:
		return nil, errors.New("rollout: SchedulerConfig.Clock is required")
	case cfg.Version == "":
		return nil, errors.New("rollout: SchedulerConfig.Version is required")
	}
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Scheduler{cfg: cfg, ctx: ctx, cancel: cancel}, nil
}

// Start checks the windows every ScheduleInterval until Stop. A second
// Start, or a Start after Stop, does nothing.
func (s *Scheduler) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started || s.stopped {
		return
	}
	s.started = true
	s.timer = s.cfg.Clock.AfterFunc(ScheduleInterval, s.tick)
}

// Stop ends the loop: it cancels a running pass and waits for it to end.
func (s *Scheduler) Stop() {
	s.cancel()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopped = true
	if s.timer != nil {
		s.timer.Stop()
	}
}

func (s *Scheduler) tick() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return
	}
	if !suspended(s.ctx, s.cfg.Suspended, s.cfg.Log, "scheduler") {
		if err := s.pass(s.ctx); err != nil {
			s.cfg.Log.Warn("rollout: scheduled updates", "error", err)
		}
	}
	s.timer = s.cfg.Clock.AfterFunc(ScheduleInterval, s.tick)
}

// openWindow returns the id <date>T<HH:MM> of the update window open at now
// for a schedule of days (lower-case three-letter names joined by ",") at
// hhmm UTC: a window opens at hhmm on each listed day and stays open
// WindowLength, so it may still be open after midnight. ok is false outside
// every window.
func openWindow(now time.Time, days, hhmm string) (string, bool) {
	at, err := time.Parse("15:04", hhmm)
	if err != nil {
		return "", false
	}
	now = now.UTC()
	listed := strings.Split(days, ",")
	for back := range 2 {
		day := now.AddDate(0, 0, -back)
		open := time.Date(day.Year(), day.Month(), day.Day(), at.Hour(), at.Minute(), 0, 0, time.UTC)
		name := strings.ToLower(open.Weekday().String()[:3])
		if slices.Contains(listed, name) && !now.Before(open) && now.Before(open.Add(WindowLength)) {
			return open.Format("2006-01-02") + "T" + hhmm, true
		}
	}
	return "", false
}

// scheduledHost is an enrolled host with an update schedule.
type scheduledHost struct {
	id, version, channel, days, time, lastWindow string
	local                                        bool
}

// scheduledHosts reads the enrolled hosts with a schedule, the controller's
// own host first.
func (s *Scheduler) scheduledHosts(ctx context.Context) ([]scheduledHost, error) {
	rows, err := s.cfg.DB.Read().QueryContext(ctx,
		`SELECT u.host_id, h.is_local, coalesce(h.agent_version, ''), u.channel, u.days, u.time_utc,
		 coalesce(u.last_window, '')
		 FROM core_host_update u JOIN core_hosts h ON h.id = u.host_id
		 WHERE u.days IS NOT NULL AND u.time_utc IS NOT NULL AND h.state = ?
		 ORDER BY h.is_local DESC, h.id`, core.HostEnrolled)
	if err != nil {
		return nil, fmt.Errorf("rollout: read the update schedules: %w", err)
	}
	var out []scheduledHost
	for rows.Next() {
		var h scheduledHost
		if err := rows.Scan(&h.id, &h.local, &h.version, &h.channel, &h.days, &h.time, &h.lastWindow); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("rollout: read the update schedules: %w", err)
		}
		out = append(out, h)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, fmt.Errorf("rollout: read the update schedules: %w", err)
	}
	return out, nil
}

// pass claims at most one open window: a claim starts a rollout, and no
// other can start until it ends.
func (s *Scheduler) pass(ctx context.Context) error {
	if _, ok, err := active(ctx, s.cfg.DB.Read()); err != nil || ok {
		return err
	}
	hosts, err := s.scheduledHosts(ctx)
	if err != nil {
		return err
	}
	now := s.cfg.Clock.Now().UTC()
	var releases []update.Release
	fetched := false
	for _, h := range hosts {
		window, open := openWindow(now, h.days, h.time)
		if !open || window == h.lastWindow {
			continue
		}
		// A host whose version is unknown or does not parse, such as a
		// development build, cannot be compared with a release.
		if _, err := update.ParseVersion(h.version); err != nil {
			continue
		}
		if !fetched {
			if releases, err = s.cfg.Index.Releases(ctx); err != nil {
				return fmt.Errorf("rollout: read the release index: %w", err)
			}
			fetched = true
		}
		candidates := update.InChannel(releases, h.channel)
		if len(candidates) == 0 {
			continue
		}
		target := candidates[len(candidates)-1].Version
		if !olderThan(h.version, target) {
			continue
		}
		// The controller's own host updates first; every other host waits
		// until the controller runs the target.
		if !h.local && olderThan(s.cfg.Version, target) {
			continue
		}
		if _, _, err := s.cfg.Clients.Client(ctx, h.id); err != nil {
			continue
		}
		claimed, err := s.claim(ctx, h, window, target, now)
		if err != nil || claimed {
			return err
		}
	}
	return nil
}

// errNotClaimed rolls a claim back when its window was claimed meanwhile.
var errNotClaimed = errors.New("rollout: the window was claimed already")

// claim creates the one-host rollout of h to target, records window as h's
// last and writes the audit entry, in one controller write transaction. It
// reports false without an error when another rollout is active or the
// window was claimed meanwhile.
func (s *Scheduler) claim(ctx context.Context, h scheduledHost, window, target string, now time.Time) (bool, error) {
	in := core.RolloutInput{Hosts: []string{h.id}, Version: target, Channel: h.channel}
	input, err := json.Marshal(in)
	if err != nil {
		return false, fmt.Errorf("rollout: encode the scheduled rollout: %w", err)
	}
	err = s.cfg.DB.Write(ctx, func(tx *sql.Tx) error {
		r, err := Create(ctx, tx, CreateParams{Input: in, ControllerVersion: s.cfg.Version, Now: now})
		if err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx,
			`UPDATE core_host_update SET last_window = ?, last_rollout_id = ?
			 WHERE host_id = ? AND (last_window IS NULL OR last_window <> ?)`, window, r.ID, h.id, window)
		if err != nil {
			return fmt.Errorf("rollout: record the window of host %s: %w", h.id, err)
		}
		if n, err := res.RowsAffected(); err != nil || n != 1 {
			return errors.Join(errNotClaimed, err)
		}
		return s.cfg.Audit.Append(ctx, tx, core.JournalEntry{
			At: now, Actor: core.ActorUpdateSchedule, Action: ActionRollout, Version: 1,
			Phase: "apply", Input: input, Result: "ok",
		})
	})
	switch {
	case err == nil:
		s.cfg.Log.Info("rollout: scheduled update", "host", h.id, "version", target, "window", window)
		return true, nil
	case errors.Is(err, errNotClaimed), errcode.From(err).Code == errcode.RolloutInProgress:
		return false, nil
	}
	return false, fmt.Errorf("rollout: claim window %s of host %s: %w", window, h.id, err)
}
