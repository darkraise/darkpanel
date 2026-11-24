package notify

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/darkraise/darkpanel/internal/core"
)

// controllerName is how a template names a host-less change's host.
const controllerName = "the controller"

// templateSeverity is each templated kind's severity when the caller names
// none.
var templateSeverity = map[string]string{
	core.NotifyChangeFailed:       core.SeverityWarning,
	core.NotifyChangeAutoReverted: core.SeverityWarning,
	core.NotifyRolloutFailed:      core.SeverityCritical,
	core.NotifyUpdateRolledBack:   core.SeverityCritical,
}

// render writes e's title and body from its kind's fixed template, its
// facts and its host's name, and its severity when e names none. A body
// carries only the host name, the action name, the change or rollout id,
// the state or version and the error code: never an error's message or
// cause, an action's input or an output, since bodies leave for third
// parties.
func render(ctx context.Context, tx *sql.Tx, e core.NotifyEvent) (core.NotifyEvent, error) {
	f := *e.Facts
	host, err := hostName(ctx, tx, e.HostID)
	if err != nil {
		return e, err
	}
	withCode := func(s, code string) string {
		if code == "" {
			return s + "."
		}
		return s + " with error " + code + "."
	}
	switch e.Kind {
	case core.NotifyChangeFailed:
		e.Title = "Change failed on " + host
		e.Body = withCode(fmt.Sprintf("Change %s (%s) on %s ended %s", f.ChangeID, f.Action, host, f.State), f.ErrorCode)
	case core.NotifyChangeAutoReverted:
		e.Title = "Change reverted automatically on " + host
		e.Body = fmt.Sprintf("Change %s (%s) on %s was reverted automatically and ended %s.", f.ChangeID, f.Action, host, f.State)
	case core.NotifyRolloutFailed:
		e.Title = "Update rollout failed on " + host
		e.Body = withCode(fmt.Sprintf("Rollout %s of version %s failed on %s", f.RolloutID, f.Version, host), f.ErrorCode)
	case core.NotifyUpdateRolledBack:
		e.Title = "Update rolled back on " + host
		e.Body = fmt.Sprintf("The update of %s to version %s in rollout %s was rolled back.", host, f.Version, f.RolloutID)
	default:
		return e, fmt.Errorf("notify: the %s kind has no template", e.Kind)
	}
	if e.Severity == "" {
		e.Severity = templateSeverity[e.Kind]
	}
	return e, nil
}

// hostName is the name of host id, "the controller" for "", or the id when
// no host has it.
func hostName(ctx context.Context, tx *sql.Tx, id string) (string, error) {
	if id == "" {
		return controllerName, nil
	}
	var name string
	err := tx.QueryRowContext(ctx, `SELECT name FROM core_hosts WHERE id = ?`, id).Scan(&name)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return id, nil
	case err != nil:
		return "", fmt.Errorf("notify: read the name of host %s: %w", id, err)
	}
	return name, nil
}
