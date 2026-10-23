package cli

import (
	"fmt"
	"maps"
	"slices"

	"github.com/spf13/cobra"
)

// moduleCommands are the hand-written commands of each module; each
// function lives in internal/cli/<module>.go.
var moduleCommands = map[string]func(a *app) []*cobra.Command{
	"firewall": firewallCommands, "docker": dockerCommands, "tailscale": tailscaleCommands,
	"tailnet": tailnetCommands, "derp": derpCommands, "npm": npmCommands,
	"cloudflare": cloudflareCommands, "panel": panelCommands,
}

// controllerCommands are added under "darkpanel controller".
var controllerCommands = []func(a *app) []*cobra.Command{backupCommands}

// mountHandWritten adds each module's hand-written commands under the
// module's group, in module id order, and each controller command under
// controller. A module with no hand-written command gets no group of its
// own. A name that a generated command or another hand-written one already
// takes fails NewRoot, as a clash between generated commands does.
func (a *app) mountHandWritten(root, controller *cobra.Command) error {
	for _, id := range slices.Sorted(maps.Keys(moduleCommands)) {
		cmds := moduleCommands[id](a)
		if len(cmds) == 0 {
			continue
		}
		if err := addHandWritten(group(root, []string{id}), cmds); err != nil {
			return err
		}
	}
	for _, commands := range controllerCommands {
		if err := addHandWritten(controller, commands(a)); err != nil {
			return err
		}
	}
	return nil
}

// addHandWritten adds cmds under parent, refusing a name or alias that a
// command of parent already takes.
func addHandWritten(parent *cobra.Command, cmds []*cobra.Command) error {
	for _, c := range cmds {
		for _, have := range parent.Commands() {
			if have.Name() == c.Name() || have.HasAlias(c.Name()) {
				return fmt.Errorf("the hand-written command \"%s %s\" clashes with an existing command", parent.CommandPath(), c.Name())
			}
		}
		parent.AddCommand(c)
	}
	return nil
}
