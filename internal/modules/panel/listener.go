package panel

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// listenerNoun is the resource noun of an exposure in the effect journal: panel.exposure:<exposure id>, and
// panel.exposure:?<exposure id> before the creation is named.
const listenerNoun = "panel.exposure"

// npmHTTPSPort is NPM's HTTPS port, which it publishes as 443/tcp on every address of the host (spec section 6,
// Deploy).
const npmHTTPSPort = 443

// listenerOpenAction declares panel.listener.open: a step of panel.exposure.add that opens the runtime listener and
// records the exposure. Its undo revokes and closes the listener and deletes the exposure.
func listenerOpenAction() core.ActionDef {
	return core.DefineMutating[ListenerOpenInput, ListenerOpenOutput](core.ActionSpec{
		Name:     ActionOpen,
		Summary:  "Open the runtime listener that serves the exposed panel.",
		Version:  1,
		Class:    core.ClassCompensable,
		Side:     core.SideController,
		LockKeys: []string{"panel"},
		MinScope: core.ScopeAdmin,
	}, listenerOpenHandler{})
}

// listenerOpenHandler is the handler of panel.listener.open. Its one effect is the exposure row, which carries the
// listener with it.
type listenerOpenHandler struct{}

// listenerDoc is the state document of an exposure. It leaves out the listener id and the row state, which change
// after the creation, so an undo still finds the state the creation left.
type listenerDoc struct {
	ID       string       `json:"id"`
	Via      string       `json:"via"`
	Hostname string       `json:"hostname"`
	HostID   string       `json:"hostId"`
	Tunnel   string       `json:"tunnel"`
	Subnet   netip.Prefix `json:"subnet"`
	Gateway  netip.Addr   `json:"gateway"`
	Proxy    netip.Addr   `json:"proxy"`
	Port     int          `json:"port"`
}

// listenerState is the state document of exposure row.
func listenerState(row exposureRow) (json.RawMessage, error) {
	b, err := json.Marshal(listenerDoc{
		ID: row.ID, Via: row.Via, Hostname: row.Hostname, HostID: row.HostID, Tunnel: row.Tunnel,
		Subnet: row.Subnet, Gateway: row.Gateway, Proxy: row.Proxy, Port: row.Port,
	})
	if err != nil {
		return nil, fmt.Errorf("panel: render exposure %s: %w", row.ID, err)
	}
	return b, nil
}

// listenerHash is the hex SHA-256 of a plan's state text.
func listenerHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// listenerBad is the refusal of a listener input with one bad field.
func listenerBad(path, message string) error {
	return &errcode.Error{
		Code: errcode.InvalidInput, Detail: "the listener input is invalid",
		Fields: []errcode.FieldError{{Path: path, Message: message}},
	}
}

// listenerExposed is the refusal while exposure row is live.
func listenerExposed(row exposureRow) error {
	return errcode.New(errcode.PanelAlreadyExposed, fmt.Sprintf("the panel is already exposed at %s", row.Hostname))
}

// listenerCheck refuses a call that is not a step of panel.exposure.add, and an input the composite would never send.
func listenerCheck(env core.Env, in ListenerOpenInput) error {
	if env.Call.ParentAction != ActionAdd || env.Call.ParentChangeID == "" {
		return core.Definite(errcode.New(errcode.InvalidInput, ActionOpen+" runs only as a step of "+ActionAdd))
	}
	if err := ValidateExpose(ExposeInput{Via: in.Via, Hostname: in.Hostname, HostID: in.HostID, Tunnel: in.Tunnel, Port: in.Port}); err != nil {
		return core.Definite(err)
	}
	if in.Port == 0 {
		return core.Definite(listenerBad("/port", "port must be from 1024 to 65535"))
	}
	if !in.Subnet.IsValid() || in.Subnet.Bits() != 29 || in.Subnet != in.Subnet.Masked() || !PanelRange.Contains(in.Subnet.Addr()) {
		return core.Definite(listenerBad("/subnet", "subnet must be a masked /29 in "+PanelRange.String()))
	}
	if in.Gateway != in.Subnet.Addr().Next() {
		return core.Definite(listenerBad("/gateway", "gateway must be the first host address of the subnet"))
	}
	if in.Proxy != in.Gateway.Next() {
		return core.Definite(listenerBad("/proxy", "proxy must be the second host address of the subnet"))
	}
	return nil
}

// listenerAddr is the address the listener binds.
func listenerAddr(in ListenerOpenInput) netip.AddrPort {
	return netip.AddrPortFrom(in.Gateway, uint16(in.Port)) //nolint:gosec // G115: the port was validated to 1024-65535
}

// Plan shows the listener the apply opens; it refuses while an exposure is live.
func (listenerOpenHandler) Plan(ctx context.Context, env core.Env, in ListenerOpenInput) (core.PlanOutcome, error) {
	if err := listenerCheck(env, in); err != nil {
		return core.PlanOutcome{}, err
	}
	row, ok, err := liveExposure(ctx, env.DB.Read())
	if err != nil {
		return core.PlanOutcome{}, err
	}
	if ok {
		return core.PlanOutcome{}, core.Definite(listenerExposed(row))
	}
	diff := []core.DiffLine{
		{Op: "+", Text: fmt.Sprintf("runtime listener %s for %s via %s", listenerAddr(in), in.Hostname, in.Via)},
		{Op: "+", Text: "allowed peer " + in.Proxy.String()},
	}
	return core.PlanOutcome{Diff: diff, StateHash: listenerHash("no live exposure")}, nil
}

// Apply journals the exposure, opens its listener and records the exposure under the parent's change id. An exposure
// that appeared while the listener opened wins: the listener this apply opened is revoked and closed again.
func (listenerOpenHandler) Apply(ctx context.Context, env core.Env, in ListenerOpenInput) (ListenerOpenOutput, error) {
	if err := listenerCheck(env, in); err != nil {
		return ListenerOpenOutput{}, err
	}
	live, ok, err := liveExposure(ctx, env.DB.Read())
	if err != nil {
		return ListenerOpenOutput{}, err
	}
	if ok {
		return ListenerOpenOutput{}, core.Definite(listenerExposed(live))
	}
	id := env.Call.ParentChangeID
	now := env.Clock.Now().UTC()
	row := exposureRow{
		ID: id, Via: in.Via, Hostname: in.Hostname, HostID: in.HostID, Tunnel: in.Tunnel, Subnet: in.Subnet,
		Gateway: in.Gateway, Proxy: in.Proxy, Port: in.Port, RuleID: RuleID(id), State: StateOpen, CreatedAt: now, UpdatedAt: now,
	}
	state, err := listenerState(row)
	if err != nil {
		return ListenerOpenOutput{}, err
	}
	seq, err := env.Effects.Intend(ctx, core.Effect{
		Kind: core.EffectCreate, Resource: listenerNoun + ":?" + id,
		Recovery: &core.RecoveryID{Kind: "name", Value: id}, Local: true, InverseLocal: true,
		Before: core.StateDoc{State: json.RawMessage("null")}, After: &core.StateDoc{State: state},
	}, nil)
	if err != nil {
		return ListenerOpenOutput{}, err
	}
	header := core.ClientHeaderNPM
	// IPv4 only: whether Docker also publishes [::]:443 depends on the daemon's ip6tables setting, which the
	// controller cannot see, and an IPv6 public flow on a host without IPv6 routes would be unknown and refuse every
	// change made through the exposure.
	public := netip.AddrPortFrom(netip.IPv4Unspecified(), npmHTTPSPort)
	if in.Via == ViaTunnel {
		header = core.ClientHeaderTunnel
		public = netip.AddrPort{}
	}
	addr := listenerAddr(in)
	listenerID, err := env.Listeners.Open(ctx, core.ListenerSpec{
		Addr: addr, AllowedPeer: in.Proxy, ClientHeader: header, PublicHost: in.Hostname, ChangeID: id,
		PublicBinding: public,
	})
	if err != nil {
		err = fmt.Errorf("panel: open the listener on %s: %w", addr, err)
		return ListenerOpenOutput{}, errors.Join(err, env.Effects.Skipped(ctx, seq))
	}
	row.ListenerID = listenerID
	err = env.DB.Write(ctx, func(tx *sql.Tx) error {
		live, ok, err := liveExposure(ctx, tx)
		if err != nil {
			return err
		}
		if ok {
			return listenerExposed(live)
		}
		return insertExposure(ctx, tx, row)
	})
	if err != nil {
		cleanup := context.WithoutCancel(ctx)
		op, cerr := "revoke", env.Listeners.Revoke(cleanup, listenerID)
		if cerr == nil {
			op, cerr = "close", env.Listeners.Close(cleanup, listenerID)
		}
		if cerr != nil {
			// The listener may still be open, so the call committed something: the effect stays intended for the
			// engine's settle rather than skipped.
			return ListenerOpenOutput{}, errors.Join(err, fmt.Errorf("panel: %s the listener %s: %w", op, listenerID, cerr))
		}
		return ListenerOpenOutput{}, errors.Join(err, env.Effects.Skipped(ctx, seq))
	}
	if err := env.Effects.Made(ctx, seq, listenerNoun+":"+id, core.StateDoc{State: state}); err != nil {
		return ListenerOpenOutput{}, err
	}
	return ListenerOpenOutput{ExposureID: id, ListenerID: listenerID, RuleID: row.RuleID, Address: addr}, nil
}

// Verify reads the exposure of the parent's change id rather than the output, which recovery passes as zero.
func (listenerOpenHandler) Verify(ctx context.Context, env core.Env, _ ListenerOpenInput, _ ListenerOpenOutput) error {
	id := env.Call.ParentChangeID
	row, ok, err := liveExposure(ctx, env.DB.Read())
	if err != nil {
		return err
	}
	if !ok || row.ID != id || row.State != StateOpen {
		return fmt.Errorf("panel: exposure %s is not recorded open", id)
	}
	info, err := env.Listeners.Get(ctx, row.ListenerID)
	if err != nil {
		return fmt.Errorf("panel: read the listener %s: %w", row.ListenerID, err)
	}
	if info.State != core.ListenerOpen {
		return fmt.Errorf("panel: the listener %s of exposure %s is %s, not open", row.ListenerID, id, info.State)
	}
	return nil
}

// listenerExposureID is the exposure id an effect names, whether or not the creation was named yet.
func listenerExposureID(e core.Effect) (string, error) {
	id, ok := strings.CutPrefix(e.Resource, listenerNoun+":")
	id = strings.TrimPrefix(id, "?")
	if !ok || id == "" {
		return "", fmt.Errorf("panel: %q is not a panel exposure", e.Resource)
	}
	return id, nil
}

// listenerOrphans answers the ids of the runtime listeners still open that change id created. core.Listeners has no
// lookup by change, so it reads core_listeners directly.
func listenerOrphans(ctx context.Context, q storeQueryer, id string) ([]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT id FROM core_listeners WHERE change_id = ? AND state = ? ORDER BY created_at, id`,
		id, core.ListenerOpen)
	if err != nil {
		return nil, fmt.Errorf("panel: read the open listeners of change %s: %w", id, err)
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var l string
		if err := rows.Scan(&l); err != nil {
			return nil, fmt.Errorf("panel: read the open listeners of change %s: %w", id, err)
		}
		ids = append(ids, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("panel: read the open listeners of change %s: %w", id, err)
	}
	return ids, nil
}

// listenerOrphanDoc is the state of an exposure that was never recorded while a listener its change opened is still
// open.
type listenerOrphanDoc struct {
	ID            string   `json:"id"`
	OpenListeners []string `json:"openListeners"`
}

// Observe reads the exposure the effect names. Its id tells it apart from any other, so the observation is exclusive;
// an exposure that is gone, or was undone, reads as the null state. With no exposure of that id, a listener its change
// left open reads as a state that is neither null nor the creation's, so the change ends needs_repair.
func (listenerOpenHandler) Observe(ctx context.Context, env core.Env, e core.Effect) (core.Observation, error) {
	id, err := listenerExposureID(e)
	if err != nil {
		return core.Observation{}, err
	}
	row, ok, err := liveExposure(ctx, env.DB.Read())
	if err != nil {
		return core.Observation{}, err
	}
	if !ok || row.ID != id {
		orphans, err := listenerOrphans(ctx, env.DB.Read(), id)
		if err != nil {
			return core.Observation{}, err
		}
		if len(orphans) == 0 {
			return core.Observation{Current: core.StateDoc{State: json.RawMessage("null")}, Exclusive: true}, nil
		}
		state, err := json.Marshal(listenerOrphanDoc{ID: id, OpenListeners: orphans})
		if err != nil {
			return core.Observation{}, fmt.Errorf("panel: render the open listeners of exposure %s: %w", id, err)
		}
		return core.Observation{Current: core.StateDoc{State: state}, Resource: listenerNoun + ":" + id, Matches: 1, Exclusive: true}, nil
	}
	state, err := listenerState(row)
	if err != nil {
		return core.Observation{}, err
	}
	return core.Observation{Current: core.StateDoc{State: state}, Resource: listenerNoun + ":" + id, Matches: 1, Exclusive: true}, nil
}

// Inverse revokes and closes the listener of the exposure the effect names and deletes the exposure, then revokes and
// closes any listener its change left open. With no live exposure and no open listener of that id it changes nothing,
// so it may run again.
func (listenerOpenHandler) Inverse(ctx context.Context, env core.Env, e core.Effect) (core.InverseResult, error) {
	id, err := listenerExposureID(e)
	if err != nil {
		return core.InverseResult{}, err
	}
	row, ok, err := liveExposure(ctx, env.DB.Read())
	if err != nil {
		return core.InverseResult{}, err
	}
	if ok && row.ID == id {
		if err := listenerEnd(ctx, env, row.ListenerID); err != nil {
			return core.InverseResult{}, err
		}
		if err := env.DB.Write(ctx, func(tx *sql.Tx) error { return deleteExposure(ctx, tx, id) }); err != nil {
			return core.InverseResult{}, err
		}
	}
	orphans, err := listenerOrphans(ctx, env.DB.Read(), id)
	if err != nil {
		return core.InverseResult{}, err
	}
	for _, l := range orphans {
		if err := listenerEnd(ctx, env, l); err != nil {
			return core.InverseResult{}, err
		}
	}
	return core.InverseResult{}, nil
}

// listenerEnd revokes and then closes listener id; a revoked or closed listener is fine.
func listenerEnd(ctx context.Context, env core.Env, id string) error {
	if err := env.Listeners.Revoke(ctx, id); err != nil {
		return fmt.Errorf("panel: revoke the listener %s: %w", id, err)
	}
	if err := env.Listeners.Close(ctx, id); err != nil {
		return fmt.Errorf("panel: close the listener %s: %w", id, err)
	}
	return nil
}
