package panel

import (
	"context"

	"github.com/darkraise/darkpanel/internal/core"
)

// statusAction declares panel.exposure.status: the live exposure, if any, with its state.
func statusAction() core.ActionDef {
	return core.DefineRead[StatusInput, StatusOutput](core.ActionSpec{
		Name:     ActionStatus,
		Summary:  "Show whether the panel is exposed and how.",
		Version:  1,
		Side:     core.SideController,
		MinScope: core.ScopeAdmin,
	}, statusHandler{})
}

// statusHandler is the handler of panel.exposure.status.
type statusHandler struct{}

// Read answers the live exposure. A revoked exposure whose removal has not completed, and one that needs repair, are
// still live, so the operator sees that the exposure is not fully gone.
func (statusHandler) Read(ctx context.Context, env core.Env, _ StatusInput) (StatusOutput, error) {
	row, ok, err := liveExposure(ctx, env.DB.Read())
	if err != nil || !ok {
		return StatusOutput{}, err
	}
	v := row.view()
	return StatusOutput{Exposed: true, Exposure: &v}, nil
}
