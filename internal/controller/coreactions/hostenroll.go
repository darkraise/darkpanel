package coreactions

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/darkraise/darkpanel/internal/controller/fleet"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// LocalAgentAddress is the address the controller's own agent dials: the
// agent listener's loopback bind, which is always present (spec section 4).
const LocalAgentAddress = "127.0.0.1:7443"

func (m module) hostEnrollActions() []core.ActionDef {
	return []core.ActionDef{
		core.DefineMutating[core.HostCreateInput, core.EnrollmentGrant](core.ActionSpec{
			Name: "core.host.create", Version: 1, Class: core.ClassCompensable, Side: core.SideController,
			CompensatedBy: "core.host.delete",
			Summary:       "Create a pending host and return its one-hour enrollment token, shown only once.",
		}, hostCreate{m}),
		core.DefineMutating[core.HostTokenInput, core.EnrollmentGrant](core.ActionSpec{
			Name: "core.host.token", Version: 1, Class: core.ClassIrreversible, Side: core.SideController,
			Summary: "Issue a pending or revoked host other than the local one a new one-hour enrollment token, replacing any earlier one; shown only once. The confirmation phrase is the host name.",
		}, hostToken{m}),
		core.DefineMutating[NoInput, core.EnrollmentGrant](core.ActionSpec{
			Name: "core.host.enroll-local", Version: 1, Class: core.ClassIrreversible, Side: core.SideController, BreakGlass: true,
			Summary: "Revoke the local host's agent credentials and issue it a new enrollment token for 127.0.0.1:7443; served only on the controller socket. The confirmation phrase is the host name.",
		}, hostEnrollLocal{m}),
	}
}

func (m module) authority() (*fleet.Authority, error) {
	if m.d.Authority == nil {
		return nil, errUnset("Deps.Authority")
	}
	return m.d.Authority, nil
}

// address is the controller address a token carries: the requested one,
// else serve's --agent-advertise. It must be host:port with a port from 1 to
// 65535; the input's pattern admits ports up to 99999, and nothing checks the
// advertise flag before a token carries it to an agent.
func (m module) address(requested string) (string, error) {
	addr, source := requested, "controllerAddress"
	if addr == "" {
		addr, source = m.d.AgentAdvertise, "the --agent-advertise address"
	}
	if addr == "" {
		return "", badAddress("name the address the agent dials: the controller has no --agent-advertise address",
			"required when the controller has no --agent-advertise address")
	}
	host, port, err := net.SplitHostPort(addr)
	n, perr := strconv.Atoi(port)
	if err != nil || host == "" || perr != nil || strconv.Itoa(n) != port || n < 1 || n > 65535 {
		return "", badAddress(fmt.Sprintf("%s %q is not host:port with a port from 1 to 65535", source, addr),
			"want host:port with a port from 1 to 65535")
	}
	return addr, nil
}

func badAddress(detail, message string) error {
	return &errcode.Error{
		Code: errcode.InvalidInput, Detail: detail,
		Fields: []errcode.FieldError{{Path: "/controllerAddress", Message: message}},
	}
}

// grant is hostID as core.host.list shows it, with the token an apply just
// issued it.
func (m module) grant(ctx context.Context, env core.Env, hostID, token string, expires time.Time) (core.EnrollmentGrant, error) {
	host, err := m.hostView(ctx, env, hostID)
	if err != nil {
		return core.EnrollmentGrant{}, err
	}
	return core.EnrollmentGrant{Host: host, Token: token, ExpiresAt: expires.UTC()}, nil
}

// issued is Verify of the token actions: hostID holds a token that no agent
// has consumed, which only the apply's own grant can have reached.
func issued(ctx context.Context, env core.Env, hostID string) error {
	var n int
	if err := env.DB.Read().QueryRowContext(ctx,
		`SELECT count(*) FROM core_enroll_tokens WHERE host_id = ? AND consumed_at IS NULL`, hostID).Scan(&n); err != nil {
		return fmt.Errorf("coreactions: read enrollment token: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("coreactions: host %s has no enrollment token", hostID)
	}
	return nil
}

func nameTaken(ctx context.Context, q querier, name string) (bool, error) {
	var n int
	if err := q.QueryRowContext(ctx, `SELECT count(*) FROM core_hosts WHERE name = ?`, name).Scan(&n); err != nil {
		return false, fmt.Errorf("coreactions: read hosts: %w", err)
	}
	return n > 0, nil
}

type hostCreate struct{ m module }

func (h hostCreate) Plan(ctx context.Context, env core.Env, in core.HostCreateInput) (core.PlanOutcome, error) {
	if _, err := h.m.authority(); err != nil {
		return core.PlanOutcome{}, err
	}
	addr, err := h.m.address(in.ControllerAddress)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	taken, err := nameTaken(ctx, env.DB.Read(), in.Name)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	guard := core.Guard{ID: "name-free", OK: !taken, Message: "no host is named " + in.Name}
	if taken {
		guard.Message = "a host named " + in.Name + " already exists"
	}
	return core.PlanOutcome{
		Diff: []core.DiffLine{
			{Op: "+", Text: fmt.Sprintf("host %s: pending, with an enrollment token for %s, shown once and valid for one hour", in.Name, addr)},
			{Op: " ", Text: "compensated by core.host.delete"},
		},
		Guards:    []core.Guard{guard},
		StateHash: stateHash(in.Name, strconv.FormatBool(taken)),
	}, nil
}

func (h hostCreate) Apply(ctx context.Context, env core.Env, in core.HostCreateInput) (core.EnrollmentGrant, error) {
	a, err := h.m.authority()
	if err != nil {
		return core.EnrollmentGrant{}, err
	}
	addr, err := h.m.address(in.ControllerAddress)
	if err != nil {
		return core.EnrollmentGrant{}, err
	}
	now := env.Clock.Now()
	id, err := ulid.New(ulid.Timestamp(now), rand.Reader)
	if err != nil {
		return core.EnrollmentGrant{}, fmt.Errorf("coreactions: host id: %w", err)
	}
	var token string
	var expires time.Time
	err = env.DB.Write(ctx, func(tx *sql.Tx) error {
		taken, err := nameTaken(ctx, tx, in.Name)
		if err != nil {
			return err
		}
		if taken {
			return errcode.New(errcode.PlanStateChanged, fmt.Sprintf("a host named %s was created since the plan; plan again", in.Name))
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO core_hosts (id, name, is_local, state, created_at) VALUES (?, ?, 0, ?, ?)`,
			id.String(), in.Name, core.HostPending, formatTime(now)); err != nil {
			return fmt.Errorf("coreactions: create host: %w", err)
		}
		token, expires, err = a.IssueToken(ctx, tx, id.String(), addr)
		return err
	})
	if err != nil {
		return core.EnrollmentGrant{}, err
	}
	return h.m.grant(ctx, env, id.String(), token, expires)
}

// Verify checks the name alone: recovery passes no output, so the id the
// apply chose is unknown.
func (h hostCreate) Verify(ctx context.Context, env core.Env, in core.HostCreateInput, _ core.EnrollmentGrant) error {
	taken, err := nameTaken(ctx, env.DB.Read(), in.Name)
	if err != nil {
		return err
	}
	if !taken {
		return fmt.Errorf("coreactions: no host is named %s", in.Name)
	}
	return nil
}

// localOnlyDetail is why core.host.token refuses the local host: a token
// issued over the network would let another machine enroll under the
// controller's own host id, which only the break-glass path may re-enroll.
const localOnlyDetail = " is the local host; enroll it with core.host.enroll-local over the controller socket"

// tokenGuards refuse a token for the local host and for an enrolled host,
// whose agent still holds valid credentials: core.host.revoke comes first.
func tokenGuards(r hostRecord) []core.Guard {
	local := core.Guard{ID: "not-local", OK: !r.local, Message: r.name + " is not the local host"}
	if r.local {
		local.Message = r.name + localOnlyDetail
	}
	enrolled := core.Guard{ID: "not-enrolled", OK: r.state != core.HostEnrolled, Message: r.name + " is " + r.state}
	if !enrolled.OK {
		enrolled.Message = r.name + " is enrolled; revoke it before issuing a new token"
	}
	return []core.Guard{local, enrolled}
}

type hostToken struct{ m module }

func (h hostToken) Plan(ctx context.Context, env core.Env, in core.HostTokenInput) (core.PlanOutcome, error) {
	if _, err := h.m.authority(); err != nil {
		return core.PlanOutcome{}, err
	}
	addr, err := h.m.address(in.ControllerAddress)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	r, err := readHost(ctx, env.DB.Read(), in.HostID)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	return core.PlanOutcome{
		Diff: []core.DiffLine{
			{Op: "+", Text: fmt.Sprintf("host %s (%s): a new enrollment token for %s, shown once and valid for one hour; an earlier token stops working",
				r.name, in.HostID, addr)},
		},
		Guards:       tokenGuards(r),
		StateHash:    stateHash(in.HostID, r.state),
		Confirmation: r.name,
	}, nil
}

func (h hostToken) Apply(ctx context.Context, env core.Env, in core.HostTokenInput) (core.EnrollmentGrant, error) {
	a, err := h.m.authority()
	if err != nil {
		return core.EnrollmentGrant{}, err
	}
	addr, err := h.m.address(in.ControllerAddress)
	if err != nil {
		return core.EnrollmentGrant{}, err
	}
	var token string
	var expires time.Time
	err = env.DB.Write(ctx, func(tx *sql.Tx) error {
		r, err := readHost(ctx, tx, in.HostID)
		if err != nil {
			return err
		}
		switch {
		case r.local:
			return errcode.New(errcode.PlanGuardFailed, r.name+localOnlyDetail)
		case r.state == core.HostEnrolled:
			return errcode.New(errcode.PlanStateChanged, fmt.Sprintf("host %s was enrolled since the plan; plan again", in.HostID))
		}
		token, expires, err = a.IssueToken(ctx, tx, in.HostID, addr)
		return err
	})
	if err != nil {
		return core.EnrollmentGrant{}, err
	}
	return h.m.grant(ctx, env, in.HostID, token, expires)
}

func (h hostToken) Verify(ctx context.Context, env core.Env, in core.HostTokenInput, _ core.EnrollmentGrant) error {
	return issued(ctx, env, in.HostID)
}

// localHost returns the id and record of the controller's own host.
func localHost(ctx context.Context, q querier) (string, hostRecord, error) {
	var id string
	r := hostRecord{local: true}
	err := q.QueryRowContext(ctx, `SELECT id, name, state FROM core_hosts WHERE is_local = 1`).Scan(&id, &r.name, &r.state)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", hostRecord{}, errcode.New(errcode.NotFound, "the controller has no local host")
	case err != nil:
		return "", hostRecord{}, fmt.Errorf("coreactions: read the local host: %w", err)
	}
	return id, r, nil
}

type hostEnrollLocal struct{ m module }

func (h hostEnrollLocal) Plan(ctx context.Context, env core.Env, _ NoInput) (core.PlanOutcome, error) {
	if _, err := h.m.authority(); err != nil {
		return core.PlanOutcome{}, err
	}
	id, r, err := localHost(ctx, env.DB.Read())
	if err != nil {
		return core.PlanOutcome{}, err
	}
	subject := fmt.Sprintf("host %s (%s)", r.name, id)
	return core.PlanOutcome{
		Diff: []core.DiffLine{
			{Op: "-", Text: subject + ": " + r.state + ", with its agent certificates and enrollment token"},
			{Op: "+", Text: subject + ": revoked, with a new enrollment token for " + LocalAgentAddress +
				", shown once and valid for one hour; its live session closes"},
		},
		Guards:       []core.Guard{},
		StateHash:    stateHash(id, r.state),
		Confirmation: r.name,
	}, nil
}

// Apply revokes and issues in one write transaction, so a crash leaves the
// local host either as it was or revoked with a fresh token.
func (h hostEnrollLocal) Apply(ctx context.Context, env core.Env, _ NoInput) (core.EnrollmentGrant, error) {
	a, err := h.m.authority()
	if err != nil {
		return core.EnrollmentGrant{}, err
	}
	var id, token string
	var expires time.Time
	err = env.DB.Write(ctx, func(tx *sql.Tx) error {
		var err error
		if id, _, err = localHost(ctx, tx); err != nil {
			return err
		}
		if err := fleet.RevokeHost(ctx, tx, id, env.Clock.Now()); err != nil {
			return err
		}
		token, expires, err = a.IssueToken(ctx, tx, id, LocalAgentAddress)
		return err
	})
	if err != nil {
		return core.EnrollmentGrant{}, err
	}
	h.m.disconnect(id)
	return h.m.grant(ctx, env, id, token, expires)
}

func (h hostEnrollLocal) Verify(ctx context.Context, env core.Env, _ NoInput, _ core.EnrollmentGrant) error {
	id, _, err := localHost(ctx, env.DB.Read())
	if err != nil {
		return err
	}
	return issued(ctx, env, id)
}
