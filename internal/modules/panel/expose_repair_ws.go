package panel

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// repairWSProxyHost is the action of another module that panel.websockets.repair runs as its first step.
const repairWSProxyHost = "npm.proxyhost.websockets"

// websocketsRepairAction declares panel.websockets.repair: it turns WebSocket upgrades on for the proxy host of an NPM
// exposure made before exposures asked for them, then records that the exposure passes them.
func websocketsRepairAction() core.ActionDef {
	return core.DefineComposite[WebsocketsRepairInput, ExposureView](core.ActionSpec{
		Name: ActionWSRepair, Version: 1, Unwind: core.UnwindReverse, MinScope: core.ScopeAdmin,
		Summary: "Turn WebSocket upgrades on for the exposed panel's proxy host in Nginx Proxy Manager.",
	}, wsRepairComposer{})
}

// wsRepairComposer repairs the live exposure's WebSocket passthrough.
type wsRepairComposer struct{}

// wsRepairInvalid is the refusal of a live exposure the repair cannot take.
func wsRepairInvalid(msg string) error {
	return core.Definite(errcode.New(errcode.InvalidInput, msg))
}

// Steps sets the flag on the proxy host serving the exposure's hostname, then records it. The hostname is the stored
// one, which ValidateExpose accepted, so it holds no "${" the engine would read as a reference.
func (wsRepairComposer) Steps(ctx context.Context, env core.Env, _ WebsocketsRepairInput) ([]core.Step, error) {
	row, ok, err := liveExposure(ctx, env.DB.Read())
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, core.Definite(errcode.New(errcode.PanelNotExposed, "the panel is not exposed"))
	}
	if row.Via != ViaNPM {
		return nil, wsRepairInvalid("the panel is exposed through a Cloudflare tunnel, which passes WebSocket upgrades")
	}
	if row.State != StateOpen {
		return nil, wsRepairInvalid(fmt.Sprintf("the exposure at %s is %s, not open", row.Hostname, row.State))
	}
	proxy, err := addStepInput(map[string]any{"domain": row.Hostname, "websockets": true})
	if err != nil {
		return nil, err
	}
	return []core.Step{
		{ID: "proxy", Host: row.HostID, Action: core.ActionRef{Name: repairWSProxyHost, Version: 1}, Input: proxy},
		{ID: "record", Action: core.ActionRef{Name: ActionWSRecord, Version: 1}, Input: json.RawMessage(`{}`), After: []string{"proxy"}},
	}, nil
}

// Output is the exposure as the repair left it.
func (wsRepairComposer) Output(ctx context.Context, env core.Env, _ WebsocketsRepairInput, _ map[string]json.RawMessage) (ExposureView, error) {
	row, ok, err := liveExposure(ctx, env.DB.Read())
	if err != nil {
		return ExposureView{}, err
	}
	if !ok {
		return ExposureView{}, fmt.Errorf("panel: the exposure the repair changed is no longer live")
	}
	return row.view(), nil
}

// websocketsRecordAction declares panel.websockets.record, the repair's last step: it records that the live exposure
// passes WebSocket upgrades.
func websocketsRecordAction() core.ActionDef {
	return core.DefineMutating[WebsocketsRecordInput, ExposureView](core.ActionSpec{
		Name:        ActionWSRecord,
		Summary:     "Record that the exposed panel passes WebSocket upgrades.",
		Version:     1,
		Class:       core.ClassCompensable,
		Side:        core.SideController,
		LockKeys:    []string{"panel"},
		ForwardOnly: true,
		Converges:   true,
		MinScope:    core.ScopeAdmin,
	}, wsRecordHandler{})
}

// wsRecordHandler is the handler of panel.websockets.record. It journals no effect: a run that is cut off is run again,
// and every run ends in the same state.
type wsRecordHandler struct{}

// live returns the live exposure. The record runs only under the repair, after the proxy host was changed, so on its
// own it cannot mark an exposure whose proxy host still refuses upgrades.
func (wsRecordHandler) live(ctx context.Context, env core.Env) (exposureRow, error) {
	if env.Call.ParentAction != ActionWSRepair {
		return exposureRow{}, wsRepairInvalid(ActionWSRecord + " runs only as a step of " + ActionWSRepair)
	}
	row, ok, err := liveExposure(ctx, env.DB.Read())
	if err != nil {
		return exposureRow{}, err
	}
	if !ok {
		return exposureRow{}, core.Definite(errcode.New(errcode.PanelNotExposed, "the panel is not exposed"))
	}
	return row, nil
}

// Plan shows the recorded flag's change, none when it is already set.
func (h wsRecordHandler) Plan(ctx context.Context, env core.Env, _ WebsocketsRecordInput) (core.PlanOutcome, error) {
	row, err := h.live(ctx, env)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	diff := []core.DiffLine{}
	if !row.Websockets {
		diff = append(diff,
			core.DiffLine{Op: "-", Text: "exposure " + row.Hostname + ": WebSocket passthrough off"},
			core.DiffLine{Op: "+", Text: "exposure " + row.Hostname + ": WebSocket passthrough on"})
	}
	return core.PlanOutcome{Diff: diff, StateHash: listenerHash(row.ID + "|websockets|" + strconv.FormatBool(row.Websockets))}, nil
}

// Apply records the flag on the live exposure.
func (h wsRecordHandler) Apply(ctx context.Context, env core.Env, _ WebsocketsRecordInput) (ExposureView, error) {
	row, err := h.live(ctx, env)
	if err != nil {
		return ExposureView{}, err
	}
	now := env.Clock.Now()
	if err := env.DB.Write(ctx, func(tx *sql.Tx) error { return setExposureWebsockets(ctx, tx, row.ID, now) }); err != nil {
		return ExposureView{}, err
	}
	row.Websockets, row.UpdatedAt = true, now
	return row.view(), nil
}

// Verify passes once the live exposure records the flag. It ignores the output.
func (wsRecordHandler) Verify(ctx context.Context, env core.Env, _ WebsocketsRecordInput, _ ExposureView) error {
	row, ok, err := liveExposure(ctx, env.DB.Read())
	if err != nil {
		return err
	}
	if !ok || !row.Websockets {
		return fmt.Errorf("panel: the live exposure does not record WebSocket passthrough")
	}
	return nil
}
