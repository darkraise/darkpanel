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
	"tailscaled.service": "tailscale", "docker.service": "docker", "docker.socket": "docker",
	"containerd.service": "docker", "ufw.service": "firewall", "fail2ban.service": "fail2ban",
	"nftables.service": "firewall", "netfilter-persistent.service": "firewall", "firewalld.service": "firewall",
}

const (
	journaldUnit   = "systemd-journald.service"
	tailscaledUnit = "tailscaled.service"
)

// dbusUnits carry the agent's connection to systemd.
var dbusUnits = []string{"dbus.service", "dbus.socket"}

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
	if n, ok := named(u, dbusUnits); ok && (o == opStop || o == opRestart || o == opDisable) {
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
	admin   bool     // the caller needs admin on the host
	phrase  bool     // apply repeats the unit name
	modules []string // the modules whose pending changes refuse the action
	pulled  []pulledUnit
}

// pulledUnit is a unit that an op on the target also changes, one level
// deep, with what the op does to it.
type pulledUnit struct {
	u    core.UnitInfo
	op   op
	need demands // what op on the unit asks of the caller
	text string  // "starting foo.service also starts docker.service"
}

var (
	gerunds = map[op]string{opStart: "starting", opStop: "stopping", opRestart: "restarting"}
	verbs   = map[op]string{opStart: "starts", opStop: "stops", opRestart: "restarts"}
)

// pulledNames are the units o on u also changes, with what o does to them.
// Starting u starts the units it requires, binds to or wants; stopping it
// stops the units that require it, are bound to it or are part of it.
// Restarting it restarts the second and starts the first: systemd
// propagates a restart only to the units that depend on u. A unit in both
// lists comes first as restarted.
func pulledNames(u core.UnitInfo, o op) [][2]string {
	var out [][2]string
	add := func(o op, lists ...[]string) {
		for _, l := range lists {
			for _, n := range l {
				out = append(out, [2]string{n, string(o)})
			}
		}
	}
	if o == opStop || o == opRestart {
		add(o, u.RequiredBy, u.BoundBy, u.ConsistsOf)
	}
	if o.starts() {
		add(opStart, u.Requires, u.BindsTo, u.Wants)
	}
	return out
}

// pulledUnits reads the units o on u also changes, skipping u itself, a unit
// named twice and a unit systemd does not know.
func pulledUnits(ctx context.Context, admin core.UnitAdmin, u core.UnitInfo, o op) ([]pulledUnit, error) {
	seen := map[string]bool{u.ID: true}
	var out []pulledUnit
	for _, p := range pulledNames(u, o) {
		if seen[p[0]] {
			continue
		}
		seen[p[0]] = true
		info, err := admin.Inspect(ctx, p[0])
		if err != nil {
			return nil, err
		}
		if info.LoadState == "not-found" || seen[info.ID] && info.ID != p[0] {
			continue
		}
		seen[info.ID] = true
		po := op(p[1])
		out = append(out, pulledUnit{u: info, op: po, need: demandsOf(info, po),
			text: fmt.Sprintf("%s %s also %s %s", gerunds[o], u.ID, verbs[po], info.ID)})
	}
	return out, nil
}

// pulledNamed returns the first pulled unit o changes that has a name in set.
func pulledNamed(pulled []pulledUnit, set []string, ops ...op) (pulledUnit, string, bool) {
	for _, p := range pulled {
		if n, ok := named(p.u, set); ok && slices.Contains(ops, p.op) {
			return p, n, true
		}
	}
	return pulledUnit{}, "", false
}

// pulledRefusal is the services.protected error for a pulled unit that is
// Darkpanel's own or that the agent cannot lose, or nil. A job's units are
// started only through the jobs module, whose guard keeps them off while
// jobs is off.
func pulledRefusal(pulled []pulledUnit) error {
	for _, p := range pulled {
		for _, n := range unitNames(p.u) {
			if strings.HasPrefix(n, "darkpanel") {
				return protected(fmt.Sprintf("%s; Darkpanel manages its own units", p.text))
			}
		}
	}
	if p, n, ok := pulledNamed(pulled, dbusUnits, opStop, opRestart); ok {
		return protected(fmt.Sprintf("%s; the agent talks to systemd through %s", p.text, n))
	}
	return nil
}

// merge adds what e asks to d, keeping each module once, in order.
func (d *demands) merge(e demands) {
	d.admin, d.phrase = d.admin || e.admin, d.phrase || e.phrase
	for _, m := range e.modules {
		if !slices.Contains(d.modules, m) {
			d.modules = append(d.modules, m)
		}
	}
}

func demandsOf(u core.UnitInfo, o op) demands {
	var d demands
	if m, ok := managedBy(u); ok {
		d = demands{admin: true, phrase: true, modules: []string{m}}
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
	} else if p, n, ok := pulledNamed(d.pulled, networkUnits, opStop); ok {
		guards = append(guards, core.Guard{ID: GuardNetworkStack, Message: fmt.Sprintf(
			"The agent reaches the controller through the network stack; %s stays running, and %s", n, p.text)})
	}
	if len(d.modules) > 0 {
		var busy []string
		for _, mod := range d.modules {
			b, err := moduleBusy(ctx, env, mod)
			if err != nil {
				return nil, nil, err
			}
			if b {
				busy = append(busy, mod)
			}
		}
		g := core.Guard{ID: GuardModuleChange, OK: len(busy) == 0,
			Message: fmt.Sprintf("module %s has no change in flight", strings.Join(d.modules, ", "))}
		if len(busy) > 0 {
			g.Message = fmt.Sprintf("module %s has a change applying or awaiting confirmation; let it finish first", strings.Join(busy, ", "))
		}
		guards = append(guards, g)
	}
	_, target := named(u, []string{tailscaledUnit})
	p, _, viaPulled := pulledNamed(d.pulled, []string{tailscaledUnit}, opStop, opRestart)
	if (target && (o == opStop || o == opRestart || o == opDisable)) || (!target && viaPulled) {
		on, err := m.agentOnTailnet(ctx)
		if err != nil {
			return nil, nil, err
		}
		switch {
		case target && o == opDisable && on:
			notes = append(notes, core.DiffLine{Op: " ", Text: "The agent reaches the controller over Tailscale; disabling tailscaled takes effect at the next boot"})
		case target && o == opDisable:
		case on && !target:
			guards = append(guards, core.Guard{ID: GuardTailnetLink,
				Message: fmt.Sprintf("The agent reaches the controller over Tailscale, and %s, which would cut it off", p.text)})
		case on:
			guards = append(guards, core.Guard{ID: GuardTailnetLink,
				Message: "The agent reaches the controller over Tailscale, so stopping tailscaled would cut it off"})
		default:
			guards = append(guards, core.Guard{ID: GuardTailnetLink, OK: true, Message: "the agent's controller connection does not use Tailscale"})
		}
	}
	return guards, notes, nil
}
