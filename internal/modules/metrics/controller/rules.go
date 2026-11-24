package controller

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/darkraise/darkpanel/internal/controller/coreactions"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// The alert actions (M10 spec section 5.3).
const (
	ActionAlertCreate    = moduleID + ".alert.create"
	ActionAlertUpdate    = moduleID + ".alert.update"
	ActionAlertDelete    = moduleID + ".alert.delete"
	ActionAlertListRules = moduleID + ".alert.list-rules"
	ActionAlertList      = moduleID + ".alert.list"
)

// The metrics a rule watches.
const (
	metricCPU     = "cpu"
	metricMemory  = "memory"
	metricSwap    = "swap"
	metricDisk    = "disk"
	metricLoad    = "load"
	metricOffline = "offline"
)

var ruleMetrics = []string{metricCPU, metricMemory, metricSwap, metricDisk, metricLoad, metricOffline}

// allHosts is the target of a rule over every host.
const allHosts = "*"

// maxLoadThreshold bounds a load rule's threshold, in load per logical CPU.
const maxLoadThreshold = 1000

var ruleNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]{0,63}$`)

// RuleInput is the input of metrics.alert.create/v1.
type RuleInput struct {
	Name        string  `json:"name" jsonschema:"pattern=^[A-Za-z0-9][A-Za-z0-9 ._-]{0\\,63}$" jsonschema_description:"The name: 1 to 64 letters, digits, spaces, dots, underscores or hyphens, starting with a letter or digit; unique without regard to case."`
	Metric      string  `json:"metric" jsonschema:"enum=cpu,enum=memory,enum=swap,enum=disk,enum=load,enum=offline" jsonschema_description:"cpu, memory, swap and disk compare a percent (disk per mounted filesystem), load the 1-minute load per logical CPU; offline fires on a host with no live session."`
	Comparator  string  `json:"comparator,omitempty" jsonschema:"enum=>,enum=<" jsonschema_description:"> or <; required except for offline, which ignores it."`
	Threshold   float64 `json:"threshold,omitempty" jsonschema_description:"0 to 100 for a percent, 0 to 1000 for load; offline ignores it."`
	DurationMin int     `json:"durationMin" jsonschema:"minimum=1,maximum=1440" jsonschema_description:"How many minutes the condition must hold before the rule fires."`
	Severity    string  `json:"severity" jsonschema:"enum=warning,enum=critical"`
	Target      string  `json:"target" jsonschema:"minLength=1" jsonschema_description:"* for every host, or the id of a host group."`
}

// RuleUpdateInput is the input of metrics.alert.update/v1.
type RuleUpdateInput struct {
	ID string `json:"id" jsonschema:"minLength=1"`
	RuleInput
	Enabled bool `json:"enabled" jsonschema_description:"Whether the rule is evaluated."`
}

// RuleIDInput is the input of metrics.alert.delete/v1.
type RuleIDInput struct {
	ID string `json:"id" jsonschema:"minLength=1"`
}

// NoInput is the input of a read that takes none.
type NoInput struct{}

// Rule is a stored alert rule.
type Rule struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Metric      string    `json:"metric"`
	Comparator  string    `json:"comparator" jsonschema_description:"> or <; > for offline, which ignores it."`
	Threshold   float64   `json:"threshold"`
	DurationMin int       `json:"durationMin"`
	Severity    string    `json:"severity"`
	Target      string    `json:"target" jsonschema_description:"* for every host, or a host group's id."`
	TargetName  string    `json:"targetName" jsonschema_description:"The target group's name; empty for every host."`
	Enabled     bool      `json:"enabled"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// RuleList is the output of metrics.alert.list-rules/v1.
type RuleList struct {
	Rules []Rule `json:"rules" jsonschema_description:"Every alert rule, sorted by name."`
}

func ruleActions() []core.ActionDef {
	return []core.ActionDef{
		core.DefineMutating[RuleInput, Rule](core.ActionSpec{
			Name: ActionAlertCreate, Version: 1, Class: core.ClassCompensable, Side: core.SideController,
			MinScope: core.ScopeAdmin, CompensatedBy: ActionAlertDelete,
			Summary: "Add an alert rule: a metric, a threshold held for a number of minutes, a severity and the hosts it covers.",
		}, ruleCreate{}),
		core.DefineMutating[RuleUpdateInput, Rule](core.ActionSpec{
			Name: ActionAlertUpdate, Version: 1, Class: core.ClassCompensable, Side: core.SideController,
			MinScope: core.ScopeAdmin, CompensatedBy: ActionAlertUpdate,
			Summary: "Change an alert rule; its alerts are cleared without notice and evaluated again.",
		}, ruleUpdate{}),
		core.DefineMutating[RuleIDInput, Rule](core.ActionSpec{
			Name: ActionAlertDelete, Version: 1, Class: core.ClassIrreversible, Side: core.SideController,
			MinScope: core.ScopeAdmin,
			Summary:  "Delete an alert rule with its alerts; the confirmation phrase is its name.",
		}, ruleDelete{}),
		core.DefineRead[NoInput, RuleList](core.ActionSpec{
			Name: ActionAlertListRules, Version: 1, Side: core.SideController, HostData: core.HostDataFleet,
			Summary: "List the alert rules.",
		}, ruleListRead{}),
	}
}

type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func invalid(msg string) error { return errcode.New(errcode.InvalidInput, msg) }

// normalize checks in as rule id's definition (id is "" for a new rule) and
// returns it as stored: an offline rule stores '>' and 0.
func normalize(ctx context.Context, q querier, id string, in RuleInput) (RuleInput, error) {
	if !ruleNamePattern.MatchString(in.Name) {
		return in, invalid("the name must be 1 to 64 letters, digits, spaces, dots, underscores or hyphens, starting with a letter or digit")
	}
	if !slices.Contains(ruleMetrics, in.Metric) {
		return in, invalid("metric must be cpu, memory, swap, disk, load or offline")
	}
	if in.Metric == metricOffline {
		in.Comparator, in.Threshold = ">", 0
	} else {
		if in.Comparator != ">" && in.Comparator != "<" {
			return in, invalid("comparator must be > or <")
		}
		limit := 100.0
		if in.Metric == metricLoad {
			limit = maxLoadThreshold
		}
		if math.IsNaN(in.Threshold) || in.Threshold < 0 || in.Threshold > limit {
			return in, invalid(fmt.Sprintf("threshold must be 0 to %g for %s", limit, in.Metric))
		}
	}
	if in.DurationMin < 1 || in.DurationMin > 1440 {
		return in, invalid("durationMin must be 1 to 1440")
	}
	if in.Severity != core.SeverityWarning && in.Severity != core.SeverityCritical {
		return in, invalid("severity must be warning or critical")
	}
	if in.Target != allHosts {
		var n int
		if err := q.QueryRowContext(ctx, `SELECT count(*) FROM core_host_groups WHERE id = ?`, in.Target).Scan(&n); err != nil {
			return in, fmt.Errorf("metrics: read host group %s: %w", in.Target, err)
		}
		if n == 0 {
			return in, errcode.New(errcode.NotFound, "no host group "+in.Target)
		}
	}
	var taken int
	if err := q.QueryRowContext(ctx, `SELECT count(*) FROM metrics_alert_rules WHERE name = ? COLLATE NOCASE AND id <> ?`,
		in.Name, id).Scan(&taken); err != nil {
		return in, fmt.Errorf("metrics: read the alert rules: %w", err)
	}
	if taken > 0 {
		return in, invalid("an alert rule is already named " + in.Name)
	}
	return in, nil
}

const ruleColumns = `r.id, r.name, r.metric, r.comparator, r.threshold, r.duration_min, r.severity, r.target,
	coalesce(g.name, ''), r.enabled, r.created_at, r.updated_at`

const ruleFrom = ` FROM metrics_alert_rules r LEFT JOIN core_host_groups g ON g.id = r.target`

func scanRule(row interface{ Scan(...any) error }) (Rule, error) {
	var r Rule
	var enabled int
	var created, updated string
	if err := row.Scan(&r.ID, &r.Name, &r.Metric, &r.Comparator, &r.Threshold, &r.DurationMin, &r.Severity, &r.Target,
		&r.TargetName, &enabled, &created, &updated); err != nil {
		return r, err
	}
	r.Enabled = enabled == 1
	var err error
	if r.CreatedAt, err = time.Parse(tsFormat, created); err != nil {
		return r, fmt.Errorf("the stored time %q: %w", created, err)
	}
	if r.UpdatedAt, err = time.Parse(tsFormat, updated); err != nil {
		return r, fmt.Errorf("the stored time %q: %w", updated, err)
	}
	return r, nil
}

func loadRule(ctx context.Context, q querier, id string) (Rule, error) {
	r, err := scanRule(q.QueryRowContext(ctx, `SELECT `+ruleColumns+ruleFrom+` WHERE r.id = ?`, id))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return r, errcode.New(errcode.NotFound, "no alert rule "+id)
	case err != nil:
		return r, fmt.Errorf("metrics: read alert rule %s: %w", id, err)
	}
	return r, nil
}

func loadRules(ctx context.Context, q querier) ([]Rule, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+ruleColumns+ruleFrom+` ORDER BY r.name COLLATE NOCASE`)
	if err != nil {
		return nil, fmt.Errorf("metrics: read the alert rules: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []Rule{}
	for rows.Next() {
		r, err := scanRule(rows)
		if err != nil {
			return nil, fmt.Errorf("metrics: read the alert rules: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("metrics: read the alert rules: %w", err)
	}
	return out, nil
}

// describe is a rule's definition as one line of a plan's diff.
func describe(in RuleInput, targetName string) string {
	cond := fmt.Sprintf("%s %s %g", in.Metric, in.Comparator, in.Threshold)
	if in.Metric == metricOffline {
		cond = metricOffline
	}
	target := "all hosts"
	if in.Target != allHosts {
		target = "group " + targetName
	}
	return fmt.Sprintf("alert rule %s: %s for %d minutes, %s, %s", in.Name, cond, in.DurationMin, in.Severity, target)
}

func groupName(ctx context.Context, q querier, target string) (string, error) {
	if target == allHosts {
		return "", nil
	}
	var name string
	if err := q.QueryRowContext(ctx, `SELECT name FROM core_host_groups WHERE id = ?`, target).Scan(&name); err != nil {
		return "", fmt.Errorf("metrics: read host group %s: %w", target, err)
	}
	return name, nil
}

func (r Rule) input() RuleInput {
	return RuleInput{Name: r.Name, Metric: r.Metric, Comparator: r.Comparator, Threshold: r.Threshold,
		DurationMin: r.DurationMin, Severity: r.Severity, Target: r.Target}
}

// ruleHash is a stored rule's state hash.
func ruleHash(r Rule) string {
	return fmt.Sprintf("%s|%s|%t|%s", r.ID, describe(r.input(), r.Target), r.Enabled, r.UpdatedAt.Format(tsFormat))
}

func writeRule(ctx context.Context, tx *sql.Tx, create bool, id string, in RuleInput, enabled bool, now time.Time) error {
	at := now.UTC().Format(tsFormat)
	on := 0
	if enabled {
		on = 1
	}
	var err error
	if create {
		_, err = tx.ExecContext(ctx, `INSERT INTO metrics_alert_rules (id, name, metric, comparator, threshold, duration_min,
			severity, target, enabled, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, in.Name, in.Metric, in.Comparator, in.Threshold, in.DurationMin, in.Severity, in.Target, on, at, at)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE metrics_alert_rules SET name = ?, metric = ?, comparator = ?, threshold = ?,
			duration_min = ?, severity = ?, target = ?, enabled = ?, updated_at = ? WHERE id = ?`,
			in.Name, in.Metric, in.Comparator, in.Threshold, in.DurationMin, in.Severity, in.Target, on, at, id)
	}
	if err != nil {
		return fmt.Errorf("metrics: store alert rule %s: %w", in.Name, err)
	}
	return nil
}

type ruleCreate struct{}

func (ruleCreate) Plan(ctx context.Context, env core.Env, in RuleInput) (core.PlanOutcome, error) {
	q := env.DB.Read()
	n, err := normalize(ctx, q, "", in)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	target, err := groupName(ctx, q, n.Target)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	return core.PlanOutcome{
		Diff: []core.DiffLine{
			{Op: "+", Text: describe(n, target)},
			{Op: " ", Text: "compensated by " + ActionAlertDelete},
		},
		Guards: []core.Guard{}, StateHash: "no alert rule " + strings.ToLower(n.Name),
	}, nil
}

func (ruleCreate) Apply(ctx context.Context, env core.Env, in RuleInput) (Rule, error) {
	now := env.Clock.Now()
	id, err := ulid.New(ulid.Timestamp(now), rand.Reader)
	if err != nil {
		return Rule{}, fmt.Errorf("metrics: alert rule id: %w", err)
	}
	var out Rule
	err = env.DB.Write(ctx, func(tx *sql.Tx) error {
		n, err := normalize(ctx, tx, "", in)
		if err != nil {
			return core.Definite(err)
		}
		if err := writeRule(ctx, tx, true, id.String(), n, true, now); err != nil {
			return err
		}
		out, err = loadRule(ctx, tx, id.String())
		return err
	})
	return out, err
}

// Verify finds the rule by name, the only key the input holds.
func (ruleCreate) Verify(ctx context.Context, env core.Env, in RuleInput, _ Rule) error {
	var n int
	if err := env.DB.Read().QueryRowContext(ctx, `SELECT count(*) FROM metrics_alert_rules WHERE name = ?`, in.Name).Scan(&n); err != nil {
		return fmt.Errorf("metrics: verify the alert rule: %w", err)
	}
	if n != 1 {
		return fmt.Errorf("metrics: no alert rule is named %q", in.Name)
	}
	return nil
}

type ruleUpdate struct{}

func (ruleUpdate) Plan(ctx context.Context, env core.Env, in RuleUpdateInput) (core.PlanOutcome, error) {
	q := env.DB.Read()
	r, err := loadRule(ctx, q, in.ID)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	n, err := normalize(ctx, q, r.ID, in.RuleInput)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	target, err := groupName(ctx, q, n.Target)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	state := map[bool]string{true: "on", false: "off"}
	return core.PlanOutcome{
		Diff: []core.DiffLine{
			{Op: "-", Text: describe(r.input(), r.TargetName) + " (" + state[r.Enabled] + ")"},
			{Op: "+", Text: describe(n, target) + " (" + state[in.Enabled] + ")"},
			{Op: " ", Text: "its alerts are cleared without notice; compensated by " + ActionAlertUpdate + " with the previous settings"},
		},
		Guards: []core.Guard{}, StateHash: ruleHash(r),
	}, nil
}

func (ruleUpdate) Apply(ctx context.Context, env core.Env, in RuleUpdateInput) (Rule, error) {
	var out Rule
	err := env.DB.Write(ctx, func(tx *sql.Tx) error {
		r, err := loadRule(ctx, tx, in.ID)
		if err != nil {
			return core.Definite(err)
		}
		n, err := normalize(ctx, tx, r.ID, in.RuleInput)
		if err != nil {
			return core.Definite(err)
		}
		if err := writeRule(ctx, tx, false, r.ID, n, in.Enabled, env.Clock.Now()); err != nil {
			return err
		}
		// A changed rule starts again from its new definition.
		if _, err := tx.ExecContext(ctx, `DELETE FROM metrics_alerts WHERE rule_id = ?`, r.ID); err != nil {
			return fmt.Errorf("metrics: clear the alerts of rule %s: %w", r.ID, err)
		}
		out, err = loadRule(ctx, tx, r.ID)
		return err
	})
	return out, err
}

func (ruleUpdate) Verify(ctx context.Context, env core.Env, in RuleUpdateInput, _ Rule) error {
	r, err := loadRule(ctx, env.DB.Read(), in.ID)
	if err != nil {
		return err
	}
	if r.Name != in.Name || r.Metric != in.Metric || r.DurationMin != in.DurationMin || r.Severity != in.Severity ||
		r.Target != in.Target || r.Enabled != in.Enabled {
		return fmt.Errorf("metrics: alert rule %s does not hold the updated settings", in.ID)
	}
	return nil
}

type ruleDelete struct{}

func (ruleDelete) Plan(ctx context.Context, env core.Env, in RuleIDInput) (core.PlanOutcome, error) {
	r, err := loadRule(ctx, env.DB.Read(), in.ID)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	return core.PlanOutcome{Diff: []core.DiffLine{{Op: "-", Text: describe(r.input(), r.TargetName)}},
		Guards: []core.Guard{}, StateHash: ruleHash(r), Confirmation: r.Name}, nil
}

func (ruleDelete) Apply(ctx context.Context, env core.Env, in RuleIDInput) (Rule, error) {
	var out Rule
	err := env.DB.Write(ctx, func(tx *sql.Tx) error {
		var err error
		if out, err = loadRule(ctx, tx, in.ID); err != nil {
			return core.Definite(err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM metrics_alert_rules WHERE id = ?`, in.ID); err != nil {
			return fmt.Errorf("metrics: delete alert rule %s: %w", in.ID, err)
		}
		return nil
	})
	return out, err
}

func (ruleDelete) Verify(ctx context.Context, env core.Env, in RuleIDInput, _ Rule) error {
	_, err := loadRule(ctx, env.DB.Read(), in.ID)
	switch {
	case errcode.From(err).Code == errcode.NotFound:
		return nil
	case err != nil:
		return err
	}
	return fmt.Errorf("metrics: alert rule %s still exists", in.ID)
}

type ruleListRead struct{}

func (ruleListRead) Read(ctx context.Context, env core.Env, _ NoInput) (RuleList, error) {
	rules, err := loadRules(ctx, env.DB.Read())
	if err != nil {
		return RuleList{}, err
	}
	return RuleList{Rules: rules}, nil
}

// ruleReferrer names each alert rule that targets groupID, so
// core.group.delete refuses the group while one does.
func ruleReferrer(ctx context.Context, q coreactions.GroupQuerier, groupID string) ([]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT name FROM metrics_alert_rules WHERE target = ? ORDER BY name COLLATE NOCASE`, groupID)
	if err != nil {
		return nil, fmt.Errorf("read alert rules: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("read alert rules: %w", err)
		}
		out = append(out, "alert rule "+name)
	}
	return out, rows.Err()
}
