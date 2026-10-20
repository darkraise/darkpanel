package dispatch

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// Changes returns DefaultChangeLimit changes unless asked for fewer, and
// never more than MaxChangeLimit, as lifecycle.Engine.Changes does.
const (
	DefaultChangeLimit = 100
	MaxChangeLimit     = 500
)

var _ core.Dispatcher = (*Dispatcher)(nil)

func isChangeNotFound(err error) bool {
	var e *errcode.Error
	return errors.As(err, &e) && e.Code == errcode.ChangeNotFound
}

func isHostOffline(err error) bool {
	var e *errcode.Error
	return errors.As(err, &e) && e.Code == errcode.HostOffline
}

// owner returns the client of the host core_change_index names for an agent
// change. A change it does not hold is looked for on every host, because the
// agent commits a change before the controller indexes it; a change no host
// holds is change.not_found.
func (d *Dispatcher) owner(ctx context.Context, changeID string) (string, core.AgentClient, error) {
	var hostID string
	err := d.cfg.DB.Read().QueryRowContext(ctx,
		"SELECT host_id FROM core_change_index WHERE change_id = ?", changeID).Scan(&hostID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return d.find(ctx, changeID)
	case err != nil:
		return "", nil, fmt.Errorf("dispatch: read change index: %w", err)
	}
	c, err := d.cfg.Hosts.Agent(ctx, hostID)
	return hostID, c, err
}

// find asks every online host for changeID and indexes the first that
// answers with a view of that change on its own host. A host's
// change.not_found means it does not hold the change, and an offline host
// cannot answer, so both are skipped; any other error fails the lookup.
func (d *Dispatcher) find(ctx context.Context, changeID string) (string, core.AgentClient, error) {
	hosts, err := d.hostIDs(ctx)
	if err != nil {
		return "", nil, err
	}
	for _, hostID := range hosts {
		c, err := d.cfg.Hosts.Agent(ctx, hostID)
		switch {
		case isHostOffline(err):
			d.cfg.Log.Debug("dispatch: offline host skipped", "host", hostID, "change", changeID)
			continue
		case err != nil:
			return "", nil, fmt.Errorf("dispatch: find change %s on host %s: %w", changeID, hostID, err)
		}
		v, err := c.Change(ctx, changeID)
		switch {
		case isChangeNotFound(err):
			continue
		case err != nil:
			return "", nil, fmt.Errorf("dispatch: find change %s on host %s: %w", changeID, hostID, err)
		case v.HostID != hostID || v.ID != changeID:
			continue
		}
		d.index(ctx, hostID, v)
		return hostID, c, nil
	}
	return "", nil, errcode.New(errcode.ChangeNotFound, fmt.Sprintf("no change %s", changeID))
}

// checkChange refuses a view of another change than changeID, or of another
// host (checkHost): the API authorizes from the view Change returns and then
// acts on the id it asked for (register row 69, obligation 2).
func checkChange(v core.ChangeView, hostID, changeID string) (core.ChangeView, error) {
	if v.ID != changeID {
		return core.ChangeView{}, fmt.Errorf("dispatch: change %s of host %s answered as change %s", changeID, hostID, v.ID)
	}
	return checkHost(v, hostID)
}

// checkHost refuses a view whose host is not the one asked, so the caller
// always authorizes and acts on the same change (register row 69,
// obligation 2).
func checkHost(v core.ChangeView, hostID string) (core.ChangeView, error) {
	if v.HostID != hostID {
		return core.ChangeView{}, fmt.Errorf("dispatch: change %s of host %s answered for host %s", v.ID, hostID, v.HostID)
	}
	return v, nil
}

// Change returns a change as its engine stores it, with its real action,
// version and host (empty for a controller-side change): the controller
// engine's first, else the agent's that core_change_index names or, without
// a row, the agent's that holds it.
func (d *Dispatcher) Change(ctx context.Context, _ core.Principal, changeID string) (core.ChangeView, error) {
	v, err := d.cfg.Engine.Change(ctx, changeID)
	if !isChangeNotFound(err) {
		return v, err
	}
	hostID, c, err := d.owner(ctx, changeID)
	if err != nil {
		return core.ChangeView{}, err
	}
	if v, err = c.Change(ctx, changeID); err != nil {
		return v, err
	}
	return checkChange(v, hostID, changeID)
}

// Confirm confirms the change Change returns for changeID, then ingests its
// host's journal.
func (d *Dispatcher) Confirm(ctx context.Context, p core.Principal, changeID string) (core.ChangeView, error) {
	return d.settle(ctx, changeID,
		func() (core.ChangeView, error) { return d.cfg.Engine.Confirm(ctx, p, changeID) },
		func(c core.AgentClient) (core.ChangeView, error) { return c.Confirm(ctx, p, changeID) })
}

// Revert reverts the change Change returns for changeID, then ingests its
// host's journal.
func (d *Dispatcher) Revert(ctx context.Context, p core.Principal, changeID string) (core.ChangeView, error) {
	return d.settle(ctx, changeID,
		func() (core.ChangeView, error) { return d.cfg.Engine.Revert(ctx, p, changeID) },
		func(c core.AgentClient) (core.ChangeView, error) { return c.Revert(ctx, p, changeID) })
}

// settle runs a confirm or revert on the controller engine, or, for a change
// it does not hold, on the owning agent. Neither refuses an unusable module:
// a pending change must stay confirmable and revertible.
func (d *Dispatcher) settle(ctx context.Context, changeID string, onController func() (core.ChangeView, error),
	onAgent func(core.AgentClient) (core.ChangeView, error)) (core.ChangeView, error) {
	v, err := onController()
	if !isChangeNotFound(err) {
		return v, err
	}
	hostID, c, err := d.owner(ctx, changeID)
	if err != nil {
		return core.ChangeView{}, err
	}
	v, err = onAgent(c)
	if err != nil {
		return v, err
	}
	d.ingest(ctx, hostID)
	return checkChange(v, hostID, changeID)
}

// Changes lists changes newest first and pages exactly like
// lifecycle.Engine.Changes: only changes with an id below f.Before, at most
// f.Limit (100 when 0 or less, 500 at most), and fewer than the limit only
// when nothing more matches. Without f.HostID it merges the controller
// engine's changes with those of every host in core_hosts. A host that
// answers host.offline is skipped and logged at debug level, so one stopped
// agent does not hide every other change; any other failure fails the call,
// because a silently short page would end a caller's paging early. With
// f.HostID only that host's agent is listed, and an offline host fails.
func (d *Dispatcher) Changes(ctx context.Context, _ core.Principal, f core.ChangeFilter) ([]core.ChangeView, error) {
	if f.Limit <= 0 {
		f.Limit = DefaultChangeLimit
	}
	f.Limit = min(f.Limit, MaxChangeLimit)
	all := []core.ChangeView{}
	hosts := []string{f.HostID}
	if f.HostID == "" {
		vs, err := d.cfg.Engine.Changes(ctx, f)
		if err != nil {
			return nil, err
		}
		all = append(all, vs...)
		if hosts, err = d.hostIDs(ctx); err != nil {
			return nil, err
		}
	}
	for _, hostID := range hosts {
		c, err := d.cfg.Hosts.Agent(ctx, hostID)
		switch {
		case isHostOffline(err) && f.HostID == "":
			d.cfg.Log.Debug("dispatch: offline host skipped", "host", hostID)
			continue
		case err != nil:
			return nil, err
		}
		hf := f
		hf.HostID = hostID
		vs, err := c.Changes(ctx, hf)
		if err != nil {
			return nil, err
		}
		for _, v := range vs {
			if _, err := checkHost(v, hostID); err != nil {
				return nil, err
			}
			if f.Before != "" && v.ID >= f.Before {
				return nil, fmt.Errorf("dispatch: host %s listed change %s at or above cursor %s", hostID, v.ID, f.Before)
			}
		}
		all = append(all, vs...)
	}
	// Every source returned its newest f.Limit matches, so the newest f.Limit
	// of their union are the newest f.Limit overall.
	sort.Slice(all, func(i, j int) bool { return all[i].ID > all[j].ID })
	if len(all) > f.Limit {
		all = all[:f.Limit]
	}
	return all, nil
}

func (d *Dispatcher) hostIDs(ctx context.Context) ([]string, error) {
	rows, err := d.cfg.DB.Read().QueryContext(ctx, "SELECT id FROM core_hosts ORDER BY id")
	if err != nil {
		return nil, fmt.Errorf("dispatch: list hosts: %w", err)
	}
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("dispatch: list hosts: %w", err)
		}
		out = append(out, id)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, fmt.Errorf("dispatch: list hosts: %w", err)
	}
	return out, nil
}
