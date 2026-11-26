package panel

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// repairWSProxyHost is the action of another module that panel.websockets.repair runs as its first step.
const repairWSProxyHost = "npm.proxyhost.websockets"

// wsNoun is the effect noun of the recorded WebSocket passthrough of an exposure.
const wsNoun = "panel.websockets"

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
		Name:      ActionWSRecord,
		Summary:   "Record that the exposed panel passes WebSocket upgrades.",
		Version:   1,
		Class:     core.ClassCompensable,
		Side:      core.SideController,
		LockKeys:  []string{"panel"},
		Converges: true,
		MinScope:  core.ScopeAdmin,
	}, wsRecordHandler{})
}

// wsRecordHandler is the handler of panel.websockets.record. It journals one local effect when it sets the flag, so an
// undo or unwind of the repair, which turns NPM's flag off, clears the record too.
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

// wsState is the effect document of a recorded flag.
func wsState(on bool) json.RawMessage {
	return json.RawMessage(`{"websockets":` + strconv.FormatBool(on) + `}`)
}

// Apply records the flag on the live exposure; an exposure that already records it is left alone.
func (h wsRecordHandler) Apply(ctx context.Context, env core.Env, _ WebsocketsRecordInput) (ExposureView, error) {
	row, err := h.live(ctx, env)
	if err != nil {
		return ExposureView{}, err
	}
	if row.Websockets {
		return row.view(), nil
	}
	resource := wsNoun + ":" + row.ID
	seq, err := env.Effects.Intend(ctx, core.Effect{
		Kind: core.EffectModify, Resource: resource, Local: true, InverseLocal: true,
		Before: core.StateDoc{State: wsState(false)}, After: &core.StateDoc{State: wsState(true)},
	}, nil)
	if err != nil {
		return ExposureView{}, err
	}
	now := env.Clock.Now()
	if err := env.DB.Write(ctx, func(tx *sql.Tx) error { return setExposureWebsockets(ctx, tx, row.ID, true, now) }); err != nil {
		return ExposureView{}, errors.Join(err, env.Effects.Skipped(ctx, seq))
	}
	if err := env.Effects.Made(ctx, seq, resource, core.StateDoc{State: wsState(true)}); err != nil {
		return ExposureView{}, err
	}
	row.Websockets, row.UpdatedAt = true, now
	return row.view(), nil
}

// wsExposureID is the exposure id an effect of the record names.
func wsExposureID(e core.Effect) (string, error) {
	id, ok := strings.CutPrefix(e.Resource, wsNoun+":")
	if !ok || id == "" {
		return "", fmt.Errorf("panel: %q is not a recorded WebSocket passthrough", e.Resource)
	}
	return id, nil
}

// Observe reads the recorded flag of the exposure the effect names; an exposure that is no longer live has none.
func (wsRecordHandler) Observe(ctx context.Context, env core.Env, e core.Effect) (core.Observation, error) {
	id, err := wsExposureID(e)
	if err != nil {
		return core.Observation{}, err
	}
	row, ok, err := liveExposure(ctx, env.DB.Read())
	if err != nil {
		return core.Observation{}, err
	}
	if !ok || row.ID != id {
		return core.Observation{Current: core.StateDoc{State: json.RawMessage("null")}, Exclusive: true}, nil
	}
	return core.Observation{Current: core.StateDoc{State: wsState(row.Websockets)}, Resource: e.Resource, Matches: 1, Exclusive: true}, nil
}

// Inverse clears the record on the exposure the effect names while it is live, and changes nothing otherwise, so it may
// run again. It does not check the parent: clearing the record only makes the panel offer the repair again.
func (wsRecordHandler) Inverse(ctx context.Context, env core.Env, e core.Effect) (core.InverseResult, error) {
	id, err := wsExposureID(e)
	if err != nil {
		return core.InverseResult{}, err
	}
	row, ok, err := liveExposure(ctx, env.DB.Read())
	if err != nil {
		return core.InverseResult{}, err
	}
	if !ok || row.ID != id || !row.Websockets {
		return core.InverseResult{}, nil
	}
	now := env.Clock.Now()
	if err := env.DB.Write(ctx, func(tx *sql.Tx) error { return setExposureWebsockets(ctx, tx, id, false, now) }); err != nil {
		return core.InverseResult{}, err
	}
	return core.InverseResult{}, nil
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
