package notify

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"unicode"

	"github.com/darkraise/darkpanel/internal/core"
)

// controllerName is how a template names a host-less change's host.
const controllerName = "the controller"

// maxHostName bounds a host's name in a template.
const maxHostName = 64

// factPattern is what an id, action name, state, version or error code
// in a template may be. Agents supply some facts, and bodies leave for
// third parties, so anything else is written as "unknown".
var factPattern = sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:+-]{0,127}$`) })

func fact(s string) string {
	if !factPattern().MatchString(s) {
		return "unknown"
	}
	return s
}

// oneLine is s with every control character replaced by a space.
func oneLine(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
}

// truncate is s cut to at most n runes.
func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

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
	for _, v := range []*string{&f.ChangeID, &f.Action, &f.State, &f.RolloutID, &f.Version} {
		*v = fact(*v)
	}
	if f.ErrorCode != "" {
		f.ErrorCode = fact(f.ErrorCode)
	}
	host, err := hostName(ctx, tx, e.HostID)
	if err != nil {
		return e, err
	}
	host = truncate(oneLine(host), maxHostName)
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
