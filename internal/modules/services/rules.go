package services

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// The guards of the mutating actions; none is overridable.
const (
	GuardNetworkStack = ID + ".network_stack"
	GuardTailnetLink  = ID + ".tailnet_link"
	GuardModuleChange = ID + ".module_change"
)

// op is what a mutating action does to a unit.
type op string

const (
	opStart   op = "start"
	opStop    op = "stop"
	opRestart op = "restart"
	opReload  op = "reload"
	opEnable  op = "enable"
	opDisable op = "disable"
)

// starts reports whether o starts the unit.
func (o op) starts() bool { return o == opStart || o == opRestart }

const useReboot = "Use Reboot on the System tab"

// powerUnits power the host off, reboot, suspend it or leave the running
// system; they are refused start and restart by name.
var powerUnits = []string{
	"systemd-poweroff.service", "systemd-reboot.service", "systemd-halt.service", "systemd-kexec.service",
	"systemd-soft-reboot.service", "systemd-suspend.service", "systemd-hibernate.service", "systemd-hybrid-sleep.service",
	"systemd-suspend-then-hibernate.service", "systemd-exit.service", "systemd-storagetm.service",
	"system-update-cleanup.service", "rescue.service", "emergency.service", "debug-shell.service",
}

// stopsReboot are the units whose stop reboots the host, refused stop by
// name as well as by their SuccessAction: systemd-storagetm sets
// SuccessAction=reboot.
var stopsReboot = []string{"systemd-storagetm.service"}

// finalTargets are the targets a unit that ends the running system pulls in.
var finalTargets = []string{"sleep.target", "shutdown.target", "umount.target", "final.target"}

// networkUnits carry the agent's connection to its controller.
var networkUnits = []string{"networking.service", "systemd-networkd.service", "NetworkManager.service", "systemd-resolved.service"}

// managedUnits are the units another Darkpanel module manages, with that
// module; the firewall module owns the firewall loaders.
var managedUnits = map[string]string{
	"ssh.service": "users", "ssh.socket": "users", "sshd.service": "users",
	"tailscaled.service": "tailscale", "docker.service": "docker", "ufw.service": "firewall",
	"fail2ban.service": "fail2ban", "nftables.service": "firewall", "netfilter-persistent.service": "firewall",
	"firewalld.service": "firewall",
}

const (
	journaldUnit   = "systemd-journald.service"
	tailscaledUnit = "tailscaled.service"
)

// mutableTypes are the unit types the mutating actions change.
var mutableTypes = []string{"service", "socket", "timer"}

// unitNames is every name of u: its ID and its Names.
func unitNames(u core.UnitInfo) []string {
	names := append([]string{u.ID}, u.Names...)
	slices.Sort(names)
	return slices.Compact(names)
}

// named returns the first name of u in set.
func named(u core.UnitInfo, set []string) (string, bool) {
	for _, n := range unitNames(u) {
		if slices.Contains(set, n) {
			return n, true
		}
	}
	return "", false
}

// managedBy returns the module that manages u, if any.
func managedBy(u core.UnitInfo) (string, bool) {
	for _, n := range unitNames(u) {
		if m, ok := managedUnits[n]; ok {
			return m, true
		}
	}
	return "", false
}

// acts reports whether an action property names something systemd does.
func acts(action string) bool { return action != "" && action != "none" }

func protected(msg string) error { return core.Definite(errcode.New(errcode.ServicesProtected, msg)) }

// startRefusal is why starting u is refused, or "".
func startRefusal(u core.UnitInfo) string {
	if n, ok := named(u, powerUnits); ok {
		return fmt.Sprintf("%s powers off, reboots or suspends the host. %s", n, useReboot)
	}
	for _, dep := range append(slices.Clone(u.Requires), u.Wants...) {
		if slices.Contains(finalTargets, dep) {
			return fmt.Sprintf("%s pulls in %s, which ends the running system. %s", u.ID, dep, useReboot)
		}
	}
	if acts(u.SuccessAction) {
		return fmt.Sprintf("%s runs %s when it finishes. %s", u.ID, u.SuccessAction, useReboot)
	}
	switch {
	case u.RefuseManualStart:
		return u.ID + " does not allow a manual start"
	case u.AllowIsolate:
		return u.ID + " can replace every running unit"
	}
	return ""
}

// refusal is the services.protected error for o on u, whoever asks, or nil.
// inspect reads a unit a timer or socket triggers.
func refusal(ctx context.Context, u core.UnitInfo, o op, inspect func(context.Context, string) (core.UnitInfo, error)) error {
	if t := unitType(u.ID); !slices.Contains(mutableTypes, t) {
		return protected(fmt.Sprintf("%s is a %s unit; only services, sockets and timers can be changed", u.ID, t))
	}
	for _, n := range unitNames(u) {
		if strings.HasPrefix(n, "darkpanel") {
			return protected("Darkpanel manages its own units: jobs through the Jobs tab, the agent and controller through updates")
		}
	}
	if n, ok := named(u, []string{"dbus.service", "dbus.socket"}); ok && (o == opStop || o == opRestart || o == opDisable) {
		return protected(fmt.Sprintf("The agent talks to systemd through %s, so it cannot %s it", n, o))
	}
	if o.starts() {
		if why := startRefusal(u); why != "" {
			return protected(why)
		}
	}
	if o == opStop && acts(u.SuccessAction) {
		return protected(fmt.Sprintf("Stopping %s runs %s. %s", u.ID, u.SuccessAction, useReboot))
	}
	if n, ok := named(u, stopsReboot); ok && o == opStop {
		return protected(fmt.Sprintf("Stopping %s reboots the host. %s", n, useReboot))
	}
	if o.starts() && (unitType(u.ID) == "timer" || unitType(u.ID) == "socket") {
		for _, name := range u.Triggers {
			t, err := inspect(ctx, name)
			if err != nil {
				return err
			}
			if why := startRefusal(t); why != "" {
				return protected(fmt.Sprintf("%s starts %s: %s", u.ID, name, why))
			}
		}
	}
	return nil
}

// demands is what o on u asks of the caller beyond the action's own scope.
type demands struct {
	admin  bool   // the caller needs admin on the host
	phrase bool   // apply repeats the unit name
	module string // the module whose pending changes refuse the action
}

func demandsOf(u core.UnitInfo, o op) demands {
	var d demands
	if m, ok := managedBy(u); ok {
		d = demands{admin: true, phrase: true, module: m}
	}
	if acts(u.FailureAction) || acts(u.JobTimeoutAction) || acts(u.StartLimitAction) {
		d.admin, d.phrase = true, true
	}
	if _, ok := named(u, networkUnits); ok && o == opRestart {
		d.phrase = true
	}
	if _, ok := named(u, []string{journaldUnit}); ok && (o == opStop || o == opRestart || o == opDisable) {
		d.phrase = true
	}
	return d
}

// pendingStates are the states of a change still in flight: applying, or
// applied and awaiting confirmation or undo.
var pendingStates = []string{
	core.StateApplying, core.StatePending, core.StateAwaitingConfirmation, core.StateUndoing, core.StateRecovering,
}

// moduleBusy reports whether module has a change in flight on this host.
func moduleBusy(ctx context.Context, env core.Env, module string) (bool, error) {
	q := `SELECT count(*) FROM core_changes WHERE substr(action, 1, ?) = ? AND state IN (?, ?, ?, ?, ?)`
	args := []any{len(module) + 1, module + "."}
	for _, s := range pendingStates {
		args = append(args, s)
	}
	var n int
	if err := env.DB.Read().QueryRowContext(ctx, q, args...).Scan(&n); err != nil {
		return false, fmt.Errorf("services: read the changes of module %s: %w", module, err)
	}
	return n > 0, nil
}

// guards are the guards o on u reports, and a warning line for the diff.
func (m module) guards(ctx context.Context, env core.Env, u core.UnitInfo, o op, d demands) ([]core.Guard, []core.DiffLine, error) {
	guards := []core.Guard{}
	var notes []core.DiffLine
	if n, ok := named(u, networkUnits); ok && (o == opStop || o == opDisable) {
		guards = append(guards, core.Guard{ID: GuardNetworkStack, Message: fmt.Sprintf(
			"The agent reaches the controller through the network stack; %s stays running", n)})
	}
	if d.module != "" {
		busy, err := moduleBusy(ctx, env, d.module)
		if err != nil {
			return nil, nil, err
		}
		g := core.Guard{ID: GuardModuleChange, OK: !busy, Message: fmt.Sprintf("module %s has no change in flight", d.module)}
		if busy {
			g.Message = fmt.Sprintf("module %s has a change applying or awaiting confirmation; let it finish first", d.module)
		}
		guards = append(guards, g)
	}
	if _, ok := named(u, []string{tailscaledUnit}); ok && (o == opStop || o == opRestart || o == opDisable) {
		on, err := m.agentOnTailnet(ctx)
		if err != nil {
			return nil, nil, err
		}
		switch {
		case o == opDisable && on:
			notes = append(notes, core.DiffLine{Op: " ", Text: "The agent reaches the controller over Tailscale; disabling tailscaled takes effect at the next boot"})
		case o == opDisable:
		case on:
			guards = append(guards, core.Guard{ID: GuardTailnetLink,
				Message: "The agent reaches the controller over Tailscale, so stopping tailscaled would cut it off"})
		default:
			guards = append(guards, core.Guard{ID: GuardTailnetLink, OK: true, Message: "the agent's controller connection does not use Tailscale"})
		}
	}
	return guards, notes, nil
}
