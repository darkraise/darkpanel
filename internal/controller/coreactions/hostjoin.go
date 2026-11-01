package coreactions

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/netip"
	"strings"

	"github.com/darkraise/darkpanel/internal/controller/listen"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// controllerTailnetAddr is the agent listener address a joining host dials
// over the tailnet: the controller's first saved IPv4 Tailscale address, else
// its first IPv6 one, on listen.AgentPort.
func controllerTailnetAddr(ctx context.Context, db *store.DB) (string, error) {
	addrs, err := listen.LoadTailscaleAddrs(ctx, db)
	if err != nil {
		return "", err
	}
	var pick netip.Addr
	for _, a := range addrs {
		if a.Is4() {
			pick = a
			break
		}
		if !pick.IsValid() {
			pick = a
		}
	}
	if !pick.IsValid() {
		return "", joinRefusal("/tailnetJoin", "the controller is not on a tailnet")
	}
	return netip.AddrPortFrom(pick, listen.AgentPort).String(), nil
}

// joiner returns the first compiled-in module that implements
// core.EnrollJoiner and is enabled on the controller's own host. Without a
// local host or a state row the manifest's default decides, as it does when
// the dispatcher writes the row at the host's first hello.
func (m module) joiner(ctx context.Context, q querier) (core.EnrollJoiner, error) {
	for _, mod := range m.d.Modules {
		j, ok := mod.(core.EnrollJoiner)
		if !ok {
			continue
		}
		man := mod.Manifest()
		enabled := man.DefaultEnabled
		err := q.QueryRowContext(ctx, `SELECT s.enabled FROM core_module_state s JOIN core_hosts h ON h.id = s.host_id
			WHERE h.is_local = 1 AND s.module_id = ?`, man.ID).Scan(&enabled)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("coreactions: read module state: %w", err)
		}
		if enabled {
			return j, nil
		}
	}
	return nil, joinRefusal("/tailnetJoin", "tailnet join needs the tailnet module")
}

// joinRefusal is a request.invalid_input refusal naming one field, with the
// detail as the field's message.
func joinRefusal(path, detail string) error {
	return core.Definite(&errcode.Error{
		Code: errcode.InvalidInput, Detail: detail,
		Fields: []errcode.FieldError{{Path: path, Message: detail}},
	})
}

// joinTags renders tags for a diff line.
func joinTags(tags []string) string {
	if len(tags) == 0 {
		return "no tags"
	}
	return strings.Join(tags, ", ")
}
