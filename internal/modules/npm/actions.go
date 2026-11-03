package npm

import "github.com/darkraise/darkpanel/internal/core"

// actions declares the module's actions (spec section 6): the agent's
// instance, network, proxy-host, access-list, certificate and reveal actions,
// the controller's checks and fleet list, and the composites that deploy the
// instance and expose a container or the admin UI.
func (n nm) actions() []core.ActionDef {
	return []core.ActionDef{
		n.instanceDeploy(), n.instanceStatus(), n.instanceConfirmPublic(), n.instanceRemove(), n.instanceUpgrade(),
		n.networkAttach(), n.networkDetach(),
		n.proxyhostAdd(), n.proxyhostUpdate(), n.proxyhostRemove(), n.proxyhostList(),
		n.accesslistAdd(), n.accesslistRemove(), n.accesslistList(),
		n.certificateRequest(), n.certificateRemove(), n.certificateList(),
		n.adminRevealPassword(),
		n.instanceCheck(), n.publicCheck(), n.instanceList(),
		n.instanceDeployPublic(), n.exposeContainer(), n.adminExpose(), n.exposeRemove(), n.adminUnexpose(),
	}
}
