package panel

import "github.com/darkraise/darkpanel/internal/core"

// regActions declares the module's actions (spec section 8): the agent's
// network and connector actions, the controller's listener and status
// actions, and the composites that add and remove the exposure.
func regActions(d agentDeps) []core.ActionDef {
	return []core.ActionDef{
		networkCreateAction(d), networkRemoveAction(d), connectorCheckAction(d),
		listenerOpenAction(), listenerRevokeAction(), statusAction(),
		exposureAddAction(), exposureRemoveAction(),
	}
}
