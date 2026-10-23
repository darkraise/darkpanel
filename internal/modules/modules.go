// Package modules lists the compiled-in modules. Adding a module adds one
// line to All and changes nothing else in the core (R5).
package modules

import (
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/cloudflare"
	"github.com/darkraise/darkpanel/internal/modules/derp"
	"github.com/darkraise/darkpanel/internal/modules/docker"
	"github.com/darkraise/darkpanel/internal/modules/firewall"
	"github.com/darkraise/darkpanel/internal/modules/hostinfo"
	"github.com/darkraise/darkpanel/internal/modules/npm"
	"github.com/darkraise/darkpanel/internal/modules/panel"
	"github.com/darkraise/darkpanel/internal/modules/tailnet"
	"github.com/darkraise/darkpanel/internal/modules/tailscale"
)

// All returns every compiled-in module in registration order.
func All() []core.Module {
	return []core.Module{
		hostinfo.Module(),
		firewall.Module(),
		docker.Module(),
		tailscale.Module(),
		tailnet.Module(),
		derp.Module(),
		npm.Module(),
		cloudflare.Module(),
		panel.Module(),
	}
}
