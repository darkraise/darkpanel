package coreactions

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
)

// weekdays is the order core_host_update.days stores its days in.
var weekdays = []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}

func (m module) hostPolicyActions() []core.ActionDef {
	return []core.ActionDef{
		core.DefineMutating[core.UpdatePolicy, core.UpdatePolicy](core.ActionSpec{
			Name: "core.host.update-policy", Version: 1, Class: core.ClassCompensable, Side: core.SideController,
			CompensatedBy: "core.host.update-policy",
			Summary:       "Set the release channel a host follows and its weekly two-hour update window in UTC, or none for on-demand updates only.",
		}, hostUpdatePolicy{m}),
	}
}

// normalPolicy is p with its days in weekday order, as it is stored.
func normalPolicy(p core.UpdatePolicy) core.UpdatePolicy {
	if p.Schedule != nil {
		days := []string{}
		for _, d := range weekdays {
			if slices.Contains(p.Schedule.Days, d) {
				days = append(days, d)
			}
		}
		p.Schedule = &core.UpdateSchedule{Days: days, Time: p.Schedule.Time}
	}
	return p
}

// readPolicy returns hostID's stored policy. A host without one follows the
// stable channel and updates on demand only.
func readPolicy(ctx context.Context, q querier, hostID string) (core.UpdatePolicy, error) {
	p := core.UpdatePolicy{HostID: hostID, Channel: core.ChannelStable}
	var days, at sql.NullString
	err := q.QueryRowContext(ctx, `SELECT channel, days, time_utc FROM core_host_update WHERE host_id = ?`, hostID).
		Scan(&p.Channel, &days, &at)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return p, nil
	case err != nil:
		return core.UpdatePolicy{}, fmt.Errorf("coreactions: read update policy: %w", err)
	}
	if days.Valid && at.Valid {
		p.Schedule = &core.UpdateSchedule{Days: strings.Split(days.String, ","), Time: at.String}
	}
	return p, nil
}

func describePolicy(p core.UpdatePolicy) string {
	if p.Schedule == nil {
		return "channel " + p.Channel + ", updates on demand only"
	}
	return fmt.Sprintf("channel %s, a two-hour window opening at %s UTC on %s",
		p.Channel, p.Schedule.Time, strings.Join(p.Schedule.Days, ","))
}

type hostUpdatePolicy struct{ m module }

func (h hostUpdatePolicy) Plan(ctx context.Context, env core.Env, in core.UpdatePolicy) (core.PlanOutcome, error) {
	q := env.DB.Read()
	r, err := readHost(ctx, q, in.HostID)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	cur, err := readPolicy(ctx, q, in.HostID)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	subject := fmt.Sprintf("host %s (%s): ", r.name, in.HostID)
	next := normalPolicy(in)
	diff := []core.DiffLine{
		{Op: "-", Text: subject + describePolicy(cur)},
		{Op: "+", Text: subject + describePolicy(next)},
	}
	if reflect.DeepEqual(cur, next) {
		diff = []core.DiffLine{{Op: " ", Text: subject + describePolicy(cur) + " already"}}
	}
	diff = append(diff, core.DiffLine{Op: " ", Text: "compensated by core.host.update-policy with the previous policy"})
	return core.PlanOutcome{Diff: diff, Guards: []core.Guard{}, StateHash: stateHash(in.HostID, describePolicy(cur))}, nil
}

// Apply keeps last_window and last_rollout_id, so a changed policy never
// reopens a window the scheduler already used.
func (h hostUpdatePolicy) Apply(ctx context.Context, env core.Env, in core.UpdatePolicy) (core.UpdatePolicy, error) {
	p := normalPolicy(in)
	var days, at any
	if p.Schedule != nil {
		days, at = strings.Join(p.Schedule.Days, ","), p.Schedule.Time
	}
	err := env.DB.Write(ctx, func(tx *sql.Tx) error {
		if _, err := readHost(ctx, tx, in.HostID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO core_host_update (host_id, channel, days, time_utc) VALUES (?, ?, ?, ?)
			ON CONFLICT (host_id) DO UPDATE SET channel = excluded.channel, days = excluded.days, time_utc = excluded.time_utc`,
			in.HostID, p.Channel, days, at)
		return err
	})
	if err != nil {
		return core.UpdatePolicy{}, fmt.Errorf("coreactions: set update policy: %w", err)
	}
	return readPolicy(ctx, env.DB.Read(), in.HostID)
}

func (h hostUpdatePolicy) Verify(ctx context.Context, env core.Env, in core.UpdatePolicy, _ core.UpdatePolicy) error {
	got, err := readPolicy(ctx, env.DB.Read(), in.HostID)
	if err != nil {
		return err
	}
	if want := normalPolicy(in); !reflect.DeepEqual(got, want) {
		return fmt.Errorf("coreactions: host %s has %s, want %s", in.HostID, describePolicy(got), describePolicy(want))
	}
	return nil
}
