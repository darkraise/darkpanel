package controller

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/store"
)

const (
	// resolveMinutes is how many consecutive minutes a condition must not
	// hold before its alert resolves.
	resolveMinutes = 2
	// staleSlack is how much older than its duration a window's newest
	// minute may be and still be evaluated.
	staleSlack = 2 * time.Minute
	// sessionTimeout bounds one host's session check.
	sessionTimeout = 5 * time.Second
)

// evaluator evaluates every enabled rule after each poll. A metric rule
// reads only the window of its duration ending at the host's newest stored
// minute, so backfilled history never fires a condition that already ended.
// An offline rule reads the fleet's live sessions and counts a host offline
// from the first evaluation that found it so, which is never before the
// evaluator started: a controller restart raises no offline alert for a
// host that reconnects within the rule's duration.
type evaluator struct {
	db       *store.DB
	hosts    core.HostDirectory // nil: offline rules are not evaluated
	notifier core.Notifier
	clock    core.Clock
	log      *slog.Logger

	offlineSince map[string]time.Time // hosts with no live session, from when
	onlineSince  map[string]time.Time // hosts with one, from when
}

func newEvaluator(db *store.DB, hosts core.HostDirectory, n core.Notifier, clock core.Clock, log *slog.Logger) *evaluator {
	if n == nil {
		n = core.NopNotifier{}
	}
	if clock == nil {
		clock = core.SystemClock{}
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &evaluator{db: db, hosts: hosts, notifier: n, clock: clock, log: log,
		offlineSince: map[string]time.Time{}, onlineSince: map[string]time.Time{}}
}

// minute is one stored minute of a host: the figure of each host-wide
// metric that has one, and each mount's percent used.
type minute struct {
	values map[string]float64
	disks  map[string]float64
}

func (m minute) value(metric, subject string) (float64, bool) {
	if metric == metricDisk {
		v, ok := m.disks[subject]
		return v, ok
	}
	v, ok := m.values[metric]
	return v, ok
}

func holds(r Rule, v float64) bool {
	if r.Comparator == "<" {
		return v < r.Threshold
	}
	return v > r.Threshold
}

// evaluate runs one evaluation: it reads the rules, the hosts, the minutes
// and the sessions, then stores every transition with its event in one
// transaction, and deletes the alerts of disabled rules and of hosts a rule
// no longer covers.
func (e *evaluator) evaluate(ctx context.Context) error {
	now := e.clock.Now().UTC().Truncate(time.Second)
	q := e.db.Read()
	all, err := loadRules(ctx, q)
	if err != nil {
		return err
	}
	var rules []Rule
	for _, r := range all {
		if r.Enabled {
			rules = append(rules, r)
		}
	}
	hosts, members, err := targets(ctx, q)
	if err != nil {
		return err
	}
	alerts, err := loadAlerts(ctx, q)
	if err != nil {
		return err
	}
	covered := map[alertKey]bool{} // the rule and host pairs evaluated, subject ""
	need := map[string]int{}       // minutes each host's metric rules read
	offline := false
	for _, r := range rules {
		for _, h := range hostsOf(r, hosts, members) {
			covered[alertKey{r.ID, h, ""}] = true
			if r.Metric == metricOffline {
				offline = true
			} else {
				need[h] = max(need[h], r.DurationMin, resolveMinutes)
			}
		}
	}
	if offline && e.hosts != nil {
		e.sessions(ctx, hosts, now)
	}
	var ts []transition
	for h, n := range need {
		newest, mins, err := readMinutes(ctx, q, h, n)
		if err != nil {
			return err
		}
		if mins == nil {
			continue
		}
		for _, r := range rules {
			if r.Metric != metricOffline && covered[alertKey{r.ID, h, ""}] {
				ts = append(ts, evaluateMetric(r, h, newest, mins, alerts, now)...)
			}
		}
	}
	if e.hosts != nil {
		for _, r := range rules {
			if r.Metric != metricOffline {
				continue
			}
			for _, h := range hostsOf(r, hosts, members) {
				ts = append(ts, e.evaluateOffline(r, h, alerts, now)...)
			}
		}
	}
	var gone []alertKey
	for k := range alerts {
		if !covered[alertKey{k.rule, k.host, ""}] {
			gone = append(gone, k)
		}
	}
	if len(ts) == 0 && len(gone) == 0 {
		return nil
	}
	sortTransitions(ts)
	return e.db.Write(ctx, func(tx *sql.Tx) error {
		for _, k := range gone {
			if _, err := tx.ExecContext(ctx, `DELETE FROM metrics_alerts WHERE rule_id = ? AND host_id = ? AND subject = ?`,
				k.rule, k.host, k.subject); err != nil {
				return fmt.Errorf("metrics: delete the alert of rule %s on host %s: %w", k.rule, k.host, err)
			}
		}
		for _, t := range ts {
			if err := record(ctx, tx, e.notifier, t, now); err != nil {
				return err
			}
		}
		return nil
	})
}

// targets is the enrolled hosts, sorted, and each host group's members.
func targets(ctx context.Context, q querier) ([]string, map[string]map[string]bool, error) {
	rows, err := q.QueryContext(ctx, `SELECT id FROM core_hosts WHERE state = 'enrolled' ORDER BY id`)
	if err != nil {
		return nil, nil, fmt.Errorf("metrics: read the hosts: %w", err)
	}
	var hosts []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, nil, fmt.Errorf("metrics: read the hosts: %w", err)
		}
		hosts = append(hosts, id)
	}
	if err := rows.Close(); err != nil {
		return nil, nil, fmt.Errorf("metrics: read the hosts: %w", err)
	}
	rows, err = q.QueryContext(ctx, `SELECT group_id, host_id FROM core_host_group_members`)
	if err != nil {
		return nil, nil, fmt.Errorf("metrics: read the host groups: %w", err)
	}
	defer func() { _ = rows.Close() }()
	members := map[string]map[string]bool{}
	for rows.Next() {
		var g, h string
		if err := rows.Scan(&g, &h); err != nil {
			return nil, nil, fmt.Errorf("metrics: read the host groups: %w", err)
		}
		if members[g] == nil {
			members[g] = map[string]bool{}
		}
		members[g][h] = true
	}
	return hosts, members, rows.Err()
}

func hostsOf(r Rule, hosts []string, members map[string]map[string]bool) []string {
	if r.Target == allHosts {
		return hosts
	}
	var out []string
	for _, h := range hosts {
		if members[r.Target][h] {
			out = append(out, h)
		}
	}
	return out
}

// readMinutes is host's newest stored minute and the n minutes ending at
// it, by start time; nil when the host has none.
func readMinutes(ctx context.Context, q *sql.DB, host string, n int) (time.Time, map[time.Time]minute, error) {
	var last string
	var cpus sql.NullInt64
	if err := q.QueryRowContext(ctx, `SELECT coalesce(max(ts), ''),
		(SELECT cpus FROM metrics_poll_status WHERE host_id = ?) FROM metrics_minute WHERE host_id = ?`, host, host).
		Scan(&last, &cpus); err != nil {
		return time.Time{}, nil, fmt.Errorf("metrics: read the minutes of host %s: %w", host, err)
	}
	if last == "" {
		return time.Time{}, nil, nil
	}
	newest, err := time.Parse(tsFormat, last)
	if err != nil {
		return time.Time{}, nil, fmt.Errorf("metrics: the stored time %q of host %s: %w", last, host, err)
	}
	from := newest.Add(-time.Duration(n-1) * time.Minute).Format(tsFormat)
	mins := map[time.Time]minute{}
	err = eachRow(ctx, q, `SELECT ts, cpu, mem_used, mem_total, swap_used, swap_total, load1 FROM metrics_minute
		WHERE host_id = ? AND ts >= ?`, host, from, func(rows *sql.Rows) error {
		var ts string
		var cpu, load float64
		var mu, mt, su, st int64
		if err := rows.Scan(&ts, &cpu, &mu, &mt, &su, &st, &load); err != nil {
			return err
		}
		at, err := time.Parse(tsFormat, ts)
		if err != nil {
			return err
		}
		m := minute{values: map[string]float64{metricCPU: cpu}, disks: map[string]float64{}}
		if mt > 0 {
			m.values[metricMemory] = 100 * float64(mu) / float64(mt)
		}
		if st > 0 {
			m.values[metricSwap] = 100 * float64(su) / float64(st)
		}
		if cpus.Valid && cpus.Int64 > 0 {
			m.values[metricLoad] = load / float64(cpus.Int64)
		}
		mins[at] = m
		return nil
	})
	if err != nil {
		return time.Time{}, nil, err
	}
	err = eachRow(ctx, q, `SELECT ts, mount, used, total FROM metrics_minute_fs WHERE host_id = ? AND ts >= ?`,
		host, from, func(rows *sql.Rows) error {
			var ts, mount string
			var used, total int64
			if err := rows.Scan(&ts, &mount, &used, &total); err != nil {
				return err
			}
			at, err := time.Parse(tsFormat, ts)
			if err != nil {
				return err
			}
			if m, ok := mins[at]; ok && total > 0 {
				m.disks[mount] = 100 * float64(used) / float64(total)
			}
			return nil
		})
	if err != nil {
		return time.Time{}, nil, err
	}
	return newest, mins, nil
}

// evaluateMetric is rule r's transitions on host: it fires an alert whose
// condition held in every minute of the window of r's duration ending at
// the newest minute, and resolves a firing one whose condition held in
// neither of the newest two minutes. A window with a missing minute fires
// nothing, and one whose newest minute is older than the duration and
// staleSlack is not evaluated.
func evaluateMetric(r Rule, host string, newest time.Time, mins map[time.Time]minute, alerts map[alertKey]string, now time.Time) []transition {
	d := time.Duration(r.DurationMin) * time.Minute
	if newest.Before(now.Add(-d - staleSlack)) {
		return nil
	}
	subjects := []string{""}
	if r.Metric == metricDisk {
		subjects = subjects[:0]
		seen := map[string]bool{}
		for mount := range mins[newest].disks {
			seen[mount] = true
		}
		for k, state := range alerts {
			if k.rule == r.ID && k.host == host && state == stateFiring {
				seen[k.subject] = true
			}
		}
		for s := range seen {
			subjects = append(subjects, s)
		}
	}
	var out []transition
	for _, s := range subjects {
		k := alertKey{r.ID, host, s}
		cur, _ := mins[newest].value(r.Metric, s)
		if alerts[k] == stateFiring {
			resolved := true
			for i := range resolveMinutes {
				m, ok := mins[newest.Add(-time.Duration(i)*time.Minute)]
				if !ok {
					resolved = false
					break
				}
				if v, ok := m.value(r.Metric, s); ok && holds(r, v) {
					resolved = false
					break
				}
			}
			if resolved {
				out = append(out, transition{rule: r, key: k, value: cur})
			}
			continue
		}
		fire := true
		for i := range r.DurationMin {
			m, ok := mins[newest.Add(-time.Duration(i)*time.Minute)]
			if !ok {
				fire = false
				break
			}
			if v, ok := m.value(r.Metric, s); !ok || !holds(r, v) {
				fire = false
				break
			}
		}
		if fire {
			out = append(out, transition{rule: r, key: k, firing: true, value: cur})
		}
	}
	return out
}

// sessions records which of hosts have a live session at now. A host whose
// check fails another way than host.offline is left as it was.
func (e *evaluator) sessions(ctx context.Context, hosts []string, now time.Time) {
	listed := map[string]bool{}
	for _, h := range hosts {
		listed[h] = true
		cctx, cancel := context.WithTimeout(ctx, sessionTimeout)
		_, err := e.hosts.Agent(cctx, h)
		cancel()
		switch {
		case err == nil:
			delete(e.offlineSince, h)
			if _, ok := e.onlineSince[h]; !ok {
				e.onlineSince[h] = now
			}
		case errcode.From(err).Code == errcode.HostOffline:
			delete(e.onlineSince, h)
			if _, ok := e.offlineSince[h]; !ok {
				e.offlineSince[h] = now
			}
		case ctx.Err() == nil:
			e.log.Warn("metrics: check a host's session", "host", h, "error", err)
		}
	}
	for _, m := range []map[string]time.Time{e.offlineSince, e.onlineSince} {
		for h := range m {
			if !listed[h] {
				delete(m, h)
			}
		}
	}
}

// evaluateOffline is offline rule r's transition on host: it fires once
// the host has had no live session for r's duration, and resolves once it
// has had one for resolveMinutes.
func (e *evaluator) evaluateOffline(r Rule, host string, alerts map[alertKey]string, now time.Time) []transition {
	k := alertKey{r.ID, host, ""}
	if alerts[k] == stateFiring {
		if since, ok := e.onlineSince[host]; ok && now.Sub(since) >= resolveMinutes*time.Minute {
			return []transition{{rule: r, key: k}}
		}
		return nil
	}
	if since, ok := e.offlineSince[host]; ok && now.Sub(since) >= time.Duration(r.DurationMin)*time.Minute {
		return []transition{{rule: r, key: k, firing: true, value: float64(int(now.Sub(since).Minutes()))}}
	}
	return nil
}
