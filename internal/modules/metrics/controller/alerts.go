package controller

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/darkraise/darkpanel/internal/core"
)

// recentResolved is how long metrics.alert.list keeps showing a resolved
// alert.
const recentResolved = 24 * time.Hour

const (
	stateFiring   = "firing"
	stateResolved = "resolved"
)

// Alert is one alert's state.
type Alert struct {
	RuleID   string    `json:"ruleId"`
	RuleName string    `json:"ruleName"`
	Metric   string    `json:"metric"`
	Severity string    `json:"severity"`
	HostID   string    `json:"hostId"`
	HostName string    `json:"hostName"`
	Subject  string    `json:"subject" jsonschema_description:"The mount point of a disk alert; empty otherwise."`
	State    string    `json:"state" jsonschema:"enum=firing,enum=resolved"`
	Since    time.Time `json:"since" jsonschema_description:"When the alert fired, or when it resolved."`
	Value    float64   `json:"value" jsonschema_description:"The figure at that moment: a percent, load per logical CPU, or minutes offline."`
}

// AlertList is the output of metrics.alert.list/v1.
type AlertList struct {
	Alerts []Alert `json:"alerts" jsonschema_description:"Firing alerts, then those resolved in the last 24 hours, newest first; only hosts the caller may read."`
}

func alertListAction() core.ActionDef {
	return core.DefineRead[NoInput, AlertList](core.ActionSpec{
		Name: ActionAlertList, Version: 1, Side: core.SideController, HostData: core.HostDataFiltered,
		Summary: "List the firing alerts and those resolved in the last 24 hours.",
	}, alertListRead{})
}

type alertListRead struct{}

func (alertListRead) Read(ctx context.Context, env core.Env, _ NoInput) (AlertList, error) {
	from := env.Clock.Now().UTC().Add(-recentResolved).Format(tsFormat)
	rows, err := env.DB.Read().QueryContext(ctx, `SELECT a.rule_id, r.name, r.metric, r.severity, a.host_id, h.name, a.subject,
		a.state, a.since, a.value FROM metrics_alerts a JOIN metrics_alert_rules r ON r.id = a.rule_id
		JOIN core_hosts h ON h.id = a.host_id WHERE a.state = 'firing' OR a.since >= ?
		ORDER BY a.state = 'firing' DESC, a.since DESC, h.name, r.name, a.subject`, from)
	if err != nil {
		return AlertList{}, fmt.Errorf("metrics: read the alerts: %w", err)
	}
	defer func() { _ = rows.Close() }()
	access := env.Principal.HostAccess()
	out := AlertList{Alerts: []Alert{}}
	for rows.Next() {
		var a Alert
		var since string
		if err := rows.Scan(&a.RuleID, &a.RuleName, &a.Metric, &a.Severity, &a.HostID, &a.HostName, &a.Subject,
			&a.State, &since, &a.Value); err != nil {
			return AlertList{}, fmt.Errorf("metrics: read the alerts: %w", err)
		}
		if !access.Sees(a.HostID) {
			continue
		}
		if a.Since, err = time.Parse(tsFormat, since); err != nil {
			return AlertList{}, fmt.Errorf("metrics: the stored time %q: %w", since, err)
		}
		out.Alerts = append(out.Alerts, a)
	}
	if err := rows.Err(); err != nil {
		return AlertList{}, fmt.Errorf("metrics: read the alerts: %w", err)
	}
	return out, nil
}

// alertKey is one alert: a rule on a host, for a subject (a disk rule's
// mount point, "" otherwise).
type alertKey struct{ rule, host, subject string }

func loadAlerts(ctx context.Context, q querier) (map[alertKey]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT rule_id, host_id, subject, state FROM metrics_alerts`)
	if err != nil {
		return nil, fmt.Errorf("metrics: read the alert states: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[alertKey]string{}
	for rows.Next() {
		var k alertKey
		var state string
		if err := rows.Scan(&k.rule, &k.host, &k.subject, &state); err != nil {
			return nil, fmt.Errorf("metrics: read the alert states: %w", err)
		}
		out[k] = state
	}
	return out, rows.Err()
}

// transition is one alert firing or resolving.
type transition struct {
	rule   Rule
	key    alertKey
	firing bool
	value  float64
}

// record stores t at now and emits its event in tx; an alert already in
// t's state is left alone and emits nothing.
func record(ctx context.Context, tx *sql.Tx, n core.Notifier, t transition, now time.Time) error {
	at := now.UTC().Format(tsFormat)
	var res sql.Result
	var err error
	if t.firing {
		res, err = tx.ExecContext(ctx, `INSERT INTO metrics_alerts (rule_id, host_id, subject, state, since, value)
			VALUES (?, ?, ?, 'firing', ?, ?) ON CONFLICT (rule_id, host_id, subject) DO UPDATE SET state = 'firing',
			since = excluded.since, value = excluded.value WHERE metrics_alerts.state <> 'firing'`,
			t.key.rule, t.key.host, t.key.subject, at, t.value)
	} else {
		res, err = tx.ExecContext(ctx, `UPDATE metrics_alerts SET state = 'resolved', since = ?, value = ?
			WHERE rule_id = ? AND host_id = ? AND subject = ? AND state = 'firing'`, at, t.value, t.key.rule, t.key.host, t.key.subject)
	}
	if err != nil {
		return fmt.Errorf("metrics: store the alert of rule %s on host %s: %w", t.key.rule, t.key.host, err)
	}
	if changed, err := res.RowsAffected(); err != nil || changed == 0 {
		return err
	}
	e, err := alertEvent(ctx, tx, t, now)
	if err != nil {
		return err
	}
	return n.Emit(ctx, tx, e)
}

// alertEvent is t's notification. Its key ends in the transition time in
// Unix seconds, which holds no colon, so a fire after a resolve is always
// delivered and the notify module keeps one alert's events in order.
func alertEvent(ctx context.Context, tx *sql.Tx, t transition, now time.Time) (core.NotifyEvent, error) {
	var host string
	err := tx.QueryRowContext(ctx, `SELECT name FROM core_hosts WHERE id = ?`, t.key.host).Scan(&host)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		host = t.key.host
	case err != nil:
		return core.NotifyEvent{}, fmt.Errorf("metrics: read the name of host %s: %w", t.key.host, err)
	}
	host = plain(host, maxTextHost)
	kind := core.NotifyAlertFiring
	if !t.firing {
		kind = core.NotifyAlertResolved
	}
	r := t.rule
	e := core.NotifyEvent{Kind: kind, Severity: r.Severity, HostID: t.key.host,
		DedupeKey: fmt.Sprintf("%s:%s:%s:%s:%d", kind, r.ID, t.key.host, t.key.subject, now.Unix())}
	name := plain(r.Name, maxTextHost)
	if t.firing {
		e.Title = fmt.Sprintf("%s on %s", name, host)
	} else {
		e.Title = fmt.Sprintf("Resolved: %s on %s", name, host)
	}
	if r.Metric == metricOffline {
		if t.firing {
			e.Body = fmt.Sprintf("%s has had no connection to the controller for %d minutes.", host, int(t.value))
		} else {
			e.Body = fmt.Sprintf("%s is connected to the controller again.", host)
		}
		return e, nil
	}
	label, unit := metricLabel(r.Metric, t.key.subject), "%"
	if r.Metric == metricLoad {
		unit = " per CPU"
	}
	side := map[string]string{">": "above", "<": "below"}[r.Comparator]
	if t.firing {
		e.Body = fmt.Sprintf("%s on %s has been %s %g%s for %d minutes; it is %.1f%s.", label, host, side, r.Threshold, unit,
			r.DurationMin, t.value, unit)
	} else {
		e.Body = fmt.Sprintf("%s on %s is no longer %s %g%s; it is %.1f%s.", label, host, side, r.Threshold, unit, t.value, unit)
	}
	return e, nil
}

// maxTextHost bounds a host or rule name in a notification.
const maxTextHost = 64

// mountText is what a mount point may be in a notification: agents supply
// mount points, and bodies leave for third parties.
var mountText = sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile(`^/[A-Za-z0-9/._@+-]{0,127}$`) })

func metricLabel(metric, subject string) string {
	switch metric {
	case metricCPU:
		return "CPU"
	case metricMemory:
		return "Memory"
	case metricSwap:
		return "Swap"
	case metricLoad:
		return "Load"
	}
	if mountText().MatchString(subject) {
		return "Disk " + subject
	}
	return "A disk"
}

// plain is s on one line, cut to at most n runes.
func plain(s string, n int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// sortTransitions orders ts so one pass emits in a stable order.
func sortTransitions(ts []transition) {
	slices.SortFunc(ts, func(a, b transition) int {
		return strings.Compare(a.key.rule+"\x00"+a.key.host+"\x00"+a.key.subject, b.key.rule+"\x00"+b.key.host+"\x00"+b.key.subject)
	})
}
