// Package rollout updates hosts one at a time (spec section 8, Fleet rollout
// and Channels and schedule): the rollout tables, the controller-side
// rollout actions, the driver that applies core.update.apply on each host in
// turn, and the scheduler that starts a one-host rollout in a host's update
// window.
package rollout

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/update"
)

// timeLayout is the fixed-width RFC 3339 UTC layout of every controller
// table, so stored times compare as text.
const timeLayout = "2006-01-02T15:04:05.000000000Z"

// querier reads from the database's reader pool or inside a write
// transaction.
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// CreateParams describe a new rollout.
type CreateParams struct {
	Input             core.RolloutInput
	ControllerVersion string // the controller's buildinfo.Version
	ChangeID          string // the core.update.rollout change; empty for a scheduled rollout
	Now               time.Time
}

// Create checks p and inserts a running rollout with its hosts inside tx:
// the controller's own host first when it is listed, the others in the order
// given, all waiting. Every host must exist and be enrolled. A target newer
// than the controller's version is refused unless the controller's own host
// is listed, so the controller is never older than an agent it manages. A
// second active rollout is rollout.in_progress.
func Create(ctx context.Context, tx *sql.Tx, p CreateParams) (core.Rollout, error) {
	hosts, err := check(ctx, tx, p.Input, p.ControllerVersion)
	if err != nil {
		return core.Rollout{}, err
	}
	now := p.Now.UTC()
	id := ulid.MustNew(ulid.Timestamp(now), rand.Reader).String()
	at := now.Format(timeLayout)
	var change any
	if p.ChangeID != "" {
		change = p.ChangeID
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO core_rollouts (id, version, channel, state, change_id, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, p.Input.Version, p.Input.Channel, core.RolloutRunning, change, at, at); err != nil {
		return core.Rollout{}, fmt.Errorf("rollout: insert rollout: %w", err)
	}
	for i, h := range hosts {
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO core_rollout_hosts (rollout_id, host_id, position, state) VALUES (?, ?, ?, ?)",
			id, h, i, core.RolloutHostWaiting); err != nil {
			return core.Rollout{}, fmt.Errorf("rollout: insert host %s: %w", h, err)
		}
	}
	r, err := load(ctx, tx, id)
	if err != nil {
		return core.Rollout{}, err
	}
	return r.view(), nil
}

// check validates a rollout of in and returns its hosts in update order.
func check(ctx context.Context, q querier, in core.RolloutInput, controllerVersion string) ([]string, error) {
	if _, err := update.ParseVersion(in.Version); err != nil {
		return nil, errcode.New(errcode.InvalidInput, fmt.Sprintf("version %q is not a semantic version", in.Version))
	}
	if in.Channel != core.ChannelStable && in.Channel != core.ChannelBeta {
		return nil, errcode.New(errcode.InvalidInput, fmt.Sprintf("unknown channel %q", in.Channel))
	}
	if len(in.Hosts) == 0 {
		return nil, errcode.New(errcode.InvalidInput, "a rollout needs at least one host")
	}
	var own string
	err := q.QueryRowContext(ctx, "SELECT id FROM core_hosts WHERE is_local = 1").Scan(&own)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("rollout: read the controller's host: %w", err)
	}
	seen := map[string]bool{}
	var ordered []string
	for _, h := range in.Hosts {
		if seen[h] {
			return nil, errcode.New(errcode.InvalidInput, fmt.Sprintf("host %s is listed twice", h))
		}
		seen[h] = true
		var state string
		err := q.QueryRowContext(ctx, "SELECT state FROM core_hosts WHERE id = ?", h).Scan(&state)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return nil, errcode.New(errcode.NotFound, "no host "+h)
		case err != nil:
			return nil, fmt.Errorf("rollout: read host %s: %w", h, err)
		case state != core.HostEnrolled:
			return nil, errcode.New(errcode.InvalidInput, fmt.Sprintf("host %s is %s; only an enrolled host can be updated", h, state))
		}
		if h == own {
			ordered = append([]string{h}, ordered...)
		} else {
			ordered = append(ordered, h)
		}
	}
	if (own == "" || !seen[own]) && olderThan(controllerVersion, in.Version) {
		return nil, errcode.New(errcode.InvalidInput, fmt.Sprintf(
			"the controller runs %s, older than %s; list the controller's own host, which updates first",
			controllerVersion, in.Version))
	}
	var activeID, state string
	err = q.QueryRowContext(ctx, "SELECT id, state FROM core_rollouts WHERE state IN (?, ?)",
		core.RolloutRunning, core.RolloutDraining).Scan(&activeID, &state)
	switch {
	case err == nil:
		return nil, errcode.New(errcode.RolloutInProgress, fmt.Sprintf("rollout %s is still %s", activeID, state))
	case !errors.Is(err, sql.ErrNoRows):
		return nil, fmt.Errorf("rollout: read the active rollout: %w", err)
	}
	return ordered, nil
}

// olderThan reports whether version current is older than target. A version
// that does not parse, such as a development build's "dev", equals only the
// same string and otherwise counts as older, so a controller whose version
// is unknown never lets another host get ahead of it.
func olderThan(current, target string) bool {
	if current == target {
		return false
	}
	c, err := update.ParseVersion(current)
	if err != nil {
		return true
	}
	t, err := update.ParseVersion(target)
	if err != nil {
		return false
	}
	return c.Compare(t) < 0
}

// hostRecord is a rollout host with the columns the API does not show.
type hostRecord struct {
	core.RolloutHost
	ApplySentAt time.Time // when the driver committed to applying the plan; zero before
	FollowedAt  time.Time // when the host's update operation was stored; zero before
}

// record is a rollout with the columns the API does not show.
type record struct {
	ID, Version, Channel, State string
	Outcome                     string // the state a draining rollout ends in
	ChangeID                    string // the creating change; empty for a scheduled rollout
	Error                       *core.ErrorDetail
	CreatedAt, UpdatedAt        time.Time
	Hosts                       []hostRecord
}

// view is the rollout as the actions return it.
func (r record) view() core.Rollout {
	v := core.Rollout{
		ID: r.ID, Version: r.Version, Channel: r.Channel, State: r.State, Error: r.Error,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, Hosts: make([]core.RolloutHost, 0, len(r.Hosts)),
	}
	for _, h := range r.Hosts {
		v.Hosts = append(v.Hosts, h.RolloutHost)
	}
	return v
}

const rolloutColumns = `id, version, channel, state, coalesce(outcome, ''), error_code, error_message,
	coalesce(change_id, ''), created_at, updated_at`

type scanner interface {
	Scan(dest ...any) error
}

func scanRollout(s scanner) (record, error) {
	var r record
	var code, message sql.NullString
	var created, updated string
	if err := s.Scan(&r.ID, &r.Version, &r.Channel, &r.State, &r.Outcome, &code, &message, &r.ChangeID,
		&created, &updated); err != nil {
		return r, err
	}
	r.Error = errorDetail(code, message)
	var err error
	if r.CreatedAt, err = time.Parse(timeLayout, created); err != nil {
		return r, fmt.Errorf("rollout: rollout %s: %w", r.ID, err)
	}
	if r.UpdatedAt, err = time.Parse(timeLayout, updated); err != nil {
		return r, fmt.Errorf("rollout: rollout %s: %w", r.ID, err)
	}
	return r, nil
}

// errorDetail is the error stored in a code and a message column.
func errorDetail(code, message sql.NullString) *core.ErrorDetail {
	if !code.Valid {
		return nil
	}
	return &core.ErrorDetail{Code: code.String, Message: message.String}
}

// optionalTime parses a nullable time column; NULL is the zero time.
func optionalTime(s sql.NullString) (time.Time, error) {
	if !s.Valid {
		return time.Time{}, nil
	}
	return time.Parse(timeLayout, s.String)
}

// load reads rollout id with its hosts; a missing rollout is not_found.
func load(ctx context.Context, q querier, id string) (record, error) {
	r, err := scanRollout(q.QueryRowContext(ctx, "SELECT "+rolloutColumns+" FROM core_rollouts WHERE id = ?", id))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return r, errcode.New(errcode.NotFound, "no rollout "+id)
	case err != nil:
		return r, fmt.Errorf("rollout: read rollout %s: %w", id, err)
	}
	return r, loadHosts(ctx, q, &r)
}

// active reads the running or draining rollout, if there is one.
func active(ctx context.Context, q querier) (record, bool, error) {
	r, err := scanRollout(q.QueryRowContext(ctx, "SELECT "+rolloutColumns+" FROM core_rollouts WHERE state IN (?, ?)",
		core.RolloutRunning, core.RolloutDraining))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return r, false, nil
	case err != nil:
		return r, false, fmt.Errorf("rollout: read the active rollout: %w", err)
	}
	return r, true, loadHosts(ctx, q, &r)
}

// loadHosts reads r's hosts in position order.
func loadHosts(ctx context.Context, q querier, r *record) error {
	rows, err := q.QueryContext(ctx,
		`SELECT host_id, position, state, plan_id, apply_sent_at, operation_id, error_code, error_message, followed_at
		 FROM core_rollout_hosts WHERE rollout_id = ? ORDER BY position`, r.ID)
	if err != nil {
		return fmt.Errorf("rollout: read the hosts of %s: %w", r.ID, err)
	}
	r.Hosts = nil
	for rows.Next() {
		var h hostRecord
		var plan, sent, op, code, message, followed sql.NullString
		if err := rows.Scan(&h.HostID, &h.Position, &h.State, &plan, &sent, &op, &code, &message, &followed); err != nil {
			_ = rows.Close()
			return fmt.Errorf("rollout: read the hosts of %s: %w", r.ID, err)
		}
		if plan.Valid {
			h.PlanID = &plan.String
		}
		if op.Valid {
			h.OperationID = &op.String
		}
		h.Error = errorDetail(code, message)
		if h.ApplySentAt, err = optionalTime(sent); err == nil {
			h.FollowedAt, err = optionalTime(followed)
		}
		if err != nil {
			_ = rows.Close()
			return fmt.Errorf("rollout: host %s of %s: %w", h.HostID, r.ID, err)
		}
		r.Hosts = append(r.Hosts, h)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return fmt.Errorf("rollout: read the hosts of %s: %w", r.ID, err)
	}
	return nil
}
