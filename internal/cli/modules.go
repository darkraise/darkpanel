package cli

import "github.com/spf13/cobra"

// moduleCommands are the hand-written commands of each module; each
// function lives in internal/cli/<module>.go.
var moduleCommands = map[string]func(a *app) []*cobra.Command{
	"firewall": firewallCommands, "docker": dockerCommands, "tailscale": tailscaleCommands,
	"tailnet": tailnetCommands, "derp": derpCommands, "npm": npmCommands,
	"cloudflare": cloudflareCommands, "panel": panelCommands,
}

// controllerCommands are added under "darkpanel controller".
var controllerCommands = []func(a *app) []*cobra.Command{backupCommands}
