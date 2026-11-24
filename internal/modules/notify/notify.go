// Package notify is the notification module (M10 spec section 4): the
// channels notifications are sent on, and the events and deliveries of the
// outbox.
package notify

import (
	"context"
	"embed"
	"fmt"
	"io/fs"

	"github.com/darkraise/darkpanel/internal/controller/coreactions"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/ext"
)

var _ ext.NotifierProvider = module{}

// ID is the module id.
const ID = "notify"

//go:embed migrations
var migrationsFS embed.FS

func controllerMigrations() fs.FS {
	sub, err := fs.Sub(migrationsFS, "migrations/controller")
	if err != nil {
		// fs.Sub fails only for an invalid path, and this one is a constant.
		panic(err)
	}
	return sub
}

type module struct{}

// Module returns the notify module.
func Module() core.Module { return module{} }

// Manifest declares the module: its controller-side channel actions, the
// channel test, and the controller migration that holds the channels,
// events and deliveries.
func (module) Manifest() core.Manifest {
	return core.Manifest{
		ID:             ID,
		Version:        1,
		DefaultEnabled: true,
		Actions:        append(channelActions(), testAction()),
		Migrations:     map[core.Side]fs.FS{core.SideController: controllerMigrations()},
	}
}

// Notifier is the module's core.Notifier, which writes the outbox.
func (module) Notifier(deps ext.Deps) core.Notifier { return newNotifier(deps.Clock) }

// Controller registers the channels' group referrer, so core.group.delete
// refuses a group a channel routes to, hands notify.channel.test the audit
// log, marks the deliveries a restore left pending, and contributes the
// outbox's sender, woken by deps.Notifier when that is the module's, and the
// cursor that emits the controller's own failed and auto-reverted changes. It runs
// only in the controller, whose database holds the module's tables.
func (module) Controller(deps ext.Deps) ([]ext.Route, []ext.Service, error) {
	coreactions.RegisterGroupReferrer(ID+".channels", channelReferrer)
	setAudit(deps.Audit)
	if deps.DB == nil {
		// An assembly with no controller database has no outbox to send.
		return nil, nil, nil
	}
	n, ok := deps.Notifier.(*notifier)
	if !ok {
		n = newNotifier(deps.Clock)
	}
	o := newOutbox(outboxConfig{DB: deps.DB, Sealer: deps.Sealer, Clock: deps.Clock, Log: deps.Log, Send: sender(), Notifier: n})
	if err := o.restore(context.Background()); err != nil {
		return nil, nil, err
	}
	cursor := newChangeCursor(deps.DB, deps.Clock, deps.Log, n)
	return nil, []ext.Service{o.service(), cursor.service()}, nil
}

// channelReferrer names each channel whose routing names groupID.
func channelReferrer(ctx context.Context, q coreactions.GroupQuerier, groupID string) ([]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT c.name FROM notify_channels c, json_each(c.group_ids_json) g
		WHERE g.value = ? ORDER BY c.name COLLATE NOCASE`, groupID)
	if err != nil {
		return nil, fmt.Errorf("read channel routes: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("read channel routes: %w", err)
		}
		out = append(out, "channel "+name)
	}
	return out, rows.Err()
}
