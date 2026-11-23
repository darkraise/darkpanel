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

// Manifest declares the module: its controller-side channel actions and
// the controller migration that holds the channels, events and deliveries.
func (module) Manifest() core.Manifest {
	return core.Manifest{
		ID:             ID,
		Version:        1,
		DefaultEnabled: true,
		Actions:        channelActions(),
		Migrations:     map[core.Side]fs.FS{core.SideController: controllerMigrations()},
	}
}

// Controller registers the channels' group referrer, so core.group.delete
// refuses a group a channel routes to. It runs only in the controller,
// whose database holds the module's tables.
func (module) Controller(ext.Deps) ([]ext.Route, []ext.Service, error) {
	coreactions.RegisterGroupReferrer(ID+".channels", channelReferrer)
	return nil, nil, nil
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
