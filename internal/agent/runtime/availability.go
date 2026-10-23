package runtime

import (
	"context"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
)

var _ core.AgentAvailability = (*Runtime)(nil)

// Availability runs moduleID's preflight checks again, as Preflight does, and
// answers which of the module's agent-side actions are available (spec 1.7).
// When the preflight fails, the module and every action are unavailable with
// its reason; otherwise each action name is asked through the engine's
// Available at its highest version. An unknown module is not_found.
func (r *Runtime) Availability(ctx context.Context, moduleID string) (core.ModuleAvailability, error) {
	st, err := r.Preflight(ctx, moduleID)
	if err != nil {
		return core.ModuleAvailability{}, err
	}
	out := core.ModuleAvailability{Module: moduleID, Available: st.Available, Reason: st.Reason,
		Actions: []core.ActionAvailability{}}
	for _, def := range r.latestActions(moduleID) {
		a := core.ActionAvailability{Name: def.Spec.Name, Available: st.Available, Reason: st.Reason}
		if st.Available {
			if err := r.engine.Available(ctx, def.Ref()); err != nil {
				a.Available, a.Reason = false, err.Error()
			}
		}
		out.Actions = append(out.Actions, a)
	}
	return out, nil
}

// latestActions returns the highest version of every agent-side action of
// moduleID, sorted by name.
func (r *Runtime) latestActions(moduleID string) []core.ActionDef {
	var out []core.ActionDef
	for _, def := range r.cfg.Registry.Actions() {
		if def.Spec.Side != core.SideAgent || !strings.HasPrefix(def.Spec.Name, moduleID+".") {
			continue
		}
		// Actions sorts by name, then version, so a later entry of the
		// same name is a higher version.
		if n := len(out); n > 0 && out[n-1].Spec.Name == def.Spec.Name {
			out[n-1] = def
			continue
		}
		out = append(out, def)
	}
	return out
}
