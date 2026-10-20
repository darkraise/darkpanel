package coreactions

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"

	"github.com/darkraise/darkpanel/internal/controller/fleet"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

func (m module) hostRevokeActions() []core.ActionDef {
	return []core.ActionDef{
		core.DefineMutating[core.HostIDInput, core.Host](core.ActionSpec{
			Name: "core.host.revoke", Version: 1, Class: core.ClassIrreversible, Side: core.SideController,
			Summary: "Revoke a host's agent certificates and enrollment token and close its live session; the confirmation phrase is the host name.",
		}, hostRevoke{m}),
		core.DefineMutating[core.HostIDInput, core.Host](core.ActionSpec{
			Name: "core.host.delete", Version: 1, Class: core.ClassIrreversible, Side: core.SideController,
			Summary: "Delete a pending or revoked host other than the local one; the confirmation phrase is the host name.",
		}, hostDelete{m}),
	}
}

// hostRecord is the part of a core_hosts row the host state rules read.
type hostRecord struct {
	name  string
	state string
	local bool
}

// readHost returns hostID's record, or not_found.
func readHost(ctx context.Context, q querier, hostID string) (hostRecord, error) {
	var r hostRecord
	err := q.QueryRowContext(ctx, `SELECT name, state, is_local FROM core_hosts WHERE id = ?`, hostID).
		Scan(&r.name, &r.state, &r.local)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return hostRecord{}, errcode.New(errcode.NotFound, fmt.Sprintf("no host %s", hostID))
	case err != nil:
		return hostRecord{}, fmt.Errorf("coreactions: read host: %w", err)
	}
	return r, nil
}

// hostView returns hostID as core.host.list shows it, or not_found.
func (m module) hostView(ctx context.Context, env core.Env, hostID string) (core.Host, error) {
	q := env.DB.Read()
	h, err := scanHost(q.QueryRowContext(ctx, `SELECT `+hostColumns+` FROM core_hosts WHERE id = ?`, hostID))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return core.Host{}, errcode.New(errcode.NotFound, fmt.Sprintf("no host %s", hostID))
	case err != nil:
		return core.Host{}, fmt.Errorf("coreactions: read host: %w", err)
	}
	if err := m.complete(ctx, env, q, &h); err != nil {
		return core.Host{}, err
	}
	return h, nil
}

// disconnect closes hostID's live session once its revocation or deletion
// has committed, so the agent's next dial meets the new state.
func (m module) disconnect(hostID string) {
	if m.d.Sessions != nil {
		m.d.Sessions.Disconnect(hostID)
	}
}

type hostRevoke struct{ m module }

func (h hostRevoke) Plan(ctx context.Context, env core.Env, in core.HostIDInput) (core.PlanOutcome, error) {
	r, err := readHost(ctx, env.DB.Read(), in.HostID)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	subject := fmt.Sprintf("host %s (%s)", r.name, in.HostID)
	diff := []core.DiffLine{
		{Op: "-", Text: subject + ": " + r.state},
		{Op: "+", Text: subject + ": revoked; its certificates and enrollment token are revoked and its live session closes"},
	}
	if r.state == core.HostRevoked {
		diff = []core.DiffLine{{Op: " ", Text: subject + ": already revoked; any live session closes"}}
	}
	diff = append(diff, core.DiffLine{Op: " ", Text: "core.host.token issues the host a new enrollment token"})
	return core.PlanOutcome{
		Diff: diff, Guards: []core.Guard{},
		StateHash:    stateHash(in.HostID, r.state),
		Confirmation: r.name,
	}, nil
}

func (h hostRevoke) Apply(ctx context.Context, env core.Env, in core.HostIDInput) (core.Host, error) {
	err := env.DB.Write(ctx, func(tx *sql.Tx) error {
		return fleet.RevokeHost(ctx, tx, in.HostID, env.Clock.Now())
	})
	if err != nil {
		return core.Host{}, err
	}
	h.m.disconnect(in.HostID)
	return h.m.hostView(ctx, env, in.HostID)
}

func (h hostRevoke) Verify(ctx context.Context, env core.Env, in core.HostIDInput, _ core.Host) error {
	r, err := readHost(ctx, env.DB.Read(), in.HostID)
	if err != nil {
		return err
	}
	if r.state != core.HostRevoked {
		return fmt.Errorf("coreactions: host %s is %s, want revoked", in.HostID, r.state)
	}
	return nil
}

type hostDelete struct{ m module }

// deletable refuses the local host, which the controller always manages,
// and an enrolled host, whose agent still holds valid credentials.
func deletable(r hostRecord) (local, enrolled core.Guard) {
	local = core.Guard{ID: "not-local", OK: !r.local, Message: r.name + " is not the local host"}
	if r.local {
		local.Message = r.name + " is the local host, which the controller always manages"
	}
	enrolled = core.Guard{ID: "not-enrolled", OK: r.state != core.HostEnrolled, Message: r.name + " is " + r.state}
	if r.state == core.HostEnrolled {
		enrolled.Message = r.name + " is enrolled; revoke it first"
	}
	return local, enrolled
}

func (h hostDelete) Plan(ctx context.Context, env core.Env, in core.HostIDInput) (core.PlanOutcome, error) {
	r, err := readHost(ctx, env.DB.Read(), in.HostID)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	local, enrolled := deletable(r)
	return core.PlanOutcome{
		Diff: []core.DiffLine{
			{Op: "-", Text: fmt.Sprintf("host %s (%s, %s), with its certificates, enrollment token, module state and update policy",
				r.name, in.HostID, r.state)},
			{Op: " ", Text: "its audit entries are kept"},
		},
		Guards:       []core.Guard{local, enrolled},
		StateHash:    stateHash(in.HostID, r.state, strconv.FormatBool(r.local)),
		Confirmation: r.name,
	}, nil
}

func (h hostDelete) Apply(ctx context.Context, env core.Env, in core.HostIDInput) (core.Host, error) {
	view, err := h.m.hostView(ctx, env, in.HostID)
	if err != nil {
		return core.Host{}, err
	}
	err = env.DB.Write(ctx, func(tx *sql.Tx) error {
		r, err := readHost(ctx, tx, in.HostID)
		if err != nil {
			return err
		}
		if local, enrolled := deletable(r); !local.OK || !enrolled.OK {
			return errcode.New(errcode.PlanStateChanged, fmt.Sprintf("host %s changed since the plan; plan again", in.HostID))
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM core_hosts WHERE id = ?`, in.HostID)
		return err
	})
	var coded *errcode.Error
	switch {
	case errors.As(err, &coded):
		return core.Host{}, err
	case err != nil:
		return core.Host{}, fmt.Errorf("coreactions: delete host: %w", err)
	}
	h.m.disconnect(in.HostID)
	view.Online = false
	return view, nil
}

func (h hostDelete) Verify(ctx context.Context, env core.Env, in core.HostIDInput, _ core.Host) error {
	_, err := readHost(ctx, env.DB.Read(), in.HostID)
	var coded *errcode.Error
	switch {
	case errors.As(err, &coded) && coded.Code == errcode.NotFound:
		return nil
	case err != nil:
		return err
	}
	return fmt.Errorf("coreactions: host %s still exists", in.HostID)
}
