package system

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform"
)

const (
	procUptime = "/proc/uptime"
	// rebootDelay leaves the agent time to report the change before the
	// host goes down.
	rebootDelay = 15 * time.Second
)

// RebootInput is the input of system.host.reboot.
type RebootInput struct{}

// RebootScheduled is the output of system.host.reboot; the web client waits
// for a boot time after BootTimeBefore.
type RebootScheduled struct {
	ScheduledAt    string `json:"scheduledAt" jsonschema_description:"When the host reboots, RFC 3339."`
	BootTimeBefore string `json:"bootTimeBefore" jsonschema_description:"The host's boot time before the reboot, RFC 3339."`
}

func (m module) rebootAction() core.ActionDef {
	return core.DefineTask[RebootInput, RebootScheduled](core.ActionSpec{
		Name: ID + ".host.reboot", Version: 1,
		Summary:      "Reboot the host 15 seconds after apply.",
		Class:        core.ClassIrreversible,
		Side:         core.SideAgent,
		LockKeys:     []string{aptLockKey},
		ApplyTimeout: time.Minute,
	}, rebootHandler{m: m})
}

type rebootHandler struct{ m module }

// pendingChanges refuses while another change on the host is applying or
// awaiting confirmation: a reboot would cut it off or revert it.
func pendingChanges(ctx context.Context, env core.Env) error {
	rows, err := env.DB.Read().QueryContext(ctx,
		`SELECT id, action, state FROM core_changes WHERE state IN (?, ?) AND id != ? ORDER BY id`,
		core.StateApplying, core.StateAwaitingConfirmation, env.ChangeID)
	if err != nil {
		return fmt.Errorf("system: read the pending changes: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var pending []string
	for rows.Next() {
		var id, action, state string
		if err := rows.Scan(&id, &action, &state); err != nil {
			return fmt.Errorf("system: read the pending changes: %w", err)
		}
		pending = append(pending, fmt.Sprintf("%s (%s, %s)", id, action, state))
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("system: read the pending changes: %w", err)
	}
	if len(pending) > 0 {
		return errcode.New(errcode.SystemChangePending, "finish or settle these changes before rebooting: "+strings.Join(pending, ", "))
	}
	return nil
}

func (h rebootHandler) Plan(ctx context.Context, env core.Env, _ RebootInput) (core.PlanOutcome, error) {
	if err := pendingChanges(ctx, env); err != nil {
		return core.PlanOutcome{}, err
	}
	host, err := h.m.hostname()
	if err != nil {
		return core.PlanOutcome{}, err
	}
	sum := sha256.Sum256([]byte(ID + ".host.reboot"))
	return core.PlanOutcome{
		Diff:         []core.DiffLine{{Op: "+", Text: "Reboot the host 15 seconds after apply; the agent is unreachable until the host is back"}},
		StateHash:    hex.EncodeToString(sum[:]),
		Confirmation: host,
	}, nil
}

func (h rebootHandler) Task(ctx context.Context, env core.Env, _ RebootInput) (RebootScheduled, error) {
	if err := pendingChanges(ctx, env); err != nil {
		return RebootScheduled{}, core.Definite(err)
	}
	now := env.Clock.Now().UTC()
	boot, err := h.m.bootTime(now)
	if err != nil {
		return RebootScheduled{}, core.Definite(err)
	}
	at := now.Add(rebootDelay)
	if err := h.m.d.reboot.ScheduleReboot(ctx, at); err != nil {
		var le *platform.LogindError
		if errors.As(err, &le) {
			return RebootScheduled{}, core.Definite(errcode.Wrap(errcode.SystemLogindRefused, err, "logind refused the reboot: "+le.Reason))
		}
		return RebootScheduled{}, err
	}
	return RebootScheduled{ScheduledAt: at.Format(time.RFC3339), BootTimeBefore: boot.Format(time.RFC3339)}, nil
}

// Verify has nothing to check: the host is still up until the scheduled time.
func (rebootHandler) Verify(context.Context, core.Env, RebootInput, RebootScheduled) error {
	return nil
}

// bootTime is now minus the uptime, truncated to the second, as the hostinfo
// module computes it.
func (m module) bootTime(now time.Time) (time.Time, error) {
	b, err := m.d.readFile(procUptime)
	if err != nil {
		return time.Time{}, fmt.Errorf("system: read %s: %w", procUptime, err)
	}
	s, err := parseUptime(b)
	if err != nil {
		return time.Time{}, err
	}
	return now.Add(-time.Duration(s) * time.Second).Truncate(time.Second), nil
}

// parseUptime reads the whole seconds of /proc/uptime's first field.
func parseUptime(b []byte) (int64, error) {
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return 0, errors.New("system: /proc/uptime is empty")
	}
	s, err := strconv.ParseFloat(f[0], 64)
	if err != nil || math.IsNaN(s) || s < 0 || s >= 1<<53 {
		return 0, errors.New("system: /proc/uptime's first field is not a non-negative number of seconds")
	}
	return int64(s), nil
}
