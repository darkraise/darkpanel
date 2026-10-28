package firewall

import (
	"bytes"
	"context"
	"fmt"
	"net/netip"
	"path"
	"slices"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/firewall/dockerfw"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
)

// dockerLines lists the plan lines of change c's Docker state, computed
// against the stored integration configuration and container rules. A change
// that holds no Docker state reads no store and gives no line.
func dockerLines(ctx context.Context, env core.Env, cfg0 fwstate.Config, c change) ([]core.DiffLine, error) {
	if c.flag == nil && c.sources == nil && len(c.rowsAdd) == 0 && len(c.rowsDel) == 0 &&
		len(c.files) == 0 && len(c.notes) == 0 && !c.cleanup {
		return nil, nil
	}
	before, err := readDockerConfig(ctx, env.DB)
	if err != nil {
		return nil, fmt.Errorf("firewall: read the Docker configuration: %w", err)
	}
	rows, err := listContainerRules(ctx, env.DB)
	if err != nil {
		return nil, fmt.Errorf("firewall: list the container rules: %w", err)
	}
	return dockerDiff(before, rows, cfg0, c)
}

// dockerDiff lists, in order, what change c does to the Docker state that
// before, rows and cfg0 hold: the integration flag, the private sources and
// the forward exemptions, each as an old and a new line when it changes; each
// container rule added, then each removed; each after file's block written
// or removed; the removal of the owned chains; and last c's notes as given.
// Removing a rule rows does not hold and malformed block markers are errors.
func dockerDiff(before dockerConfig, rows []dockerfw.ContainerRule, cfg0 fwstate.Config, c change) ([]core.DiffLine, error) {
	var out []core.DiffLine
	pair := func(old, now string) {
		out = append(out, core.DiffLine{Op: "-", Text: old}, core.DiffLine{Op: "+", Text: now})
	}
	if c.flag != nil && *c.flag != before.Enabled {
		pair(ddfIntegrationText(before.Enabled), ddfIntegrationText(*c.flag))
	}
	if c.sources != nil {
		if old, now := before.Settings.PrivateSources, c.sources.PrivateSources; !slices.Equal(old, now) {
			pair("private sources: "+ddfJoin(old, netip.Prefix.String), "private sources: "+ddfJoin(now, netip.Prefix.String))
		}
		if old, now := before.Settings.ForwardExemptions, c.sources.ForwardExemptions; !slices.Equal(old, now) {
			pair("forward exemptions: "+ddfJoin(old, ddfExemptionText), "forward exemptions: "+ddfJoin(now, ddfExemptionText))
		}
	}

	for _, r := range c.rowsAdd {
		out = append(out, core.DiffLine{Op: "+", Text: "container rule + " + ddfRuleText(r)})
	}
	for _, id := range c.rowsDel {
		i := slices.IndexFunc(rows, func(r dockerfw.ContainerRule) bool { return r.ID == id })
		if i < 0 {
			return nil, fmt.Errorf("firewall: container rule %s is not stored", id)
		}
		out = append(out, core.DiffLine{Op: "-", Text: "container rule - " + ddfRuleText(rows[i])})
	}

	for _, p := range []string{"/etc/ufw/after.rules", "/etc/ufw/after6.rules"} {
		data, ok := c.files[p]
		if !ok {
			continue
		}
		var cur []byte
		var curOK bool
		if e, ok := cfg0.Files[p]; ok && e.Present {
			var err error
			if cur, curOK, err = dockerfw.BlockOf(e.Data); err != nil {
				return nil, fmt.Errorf("firewall: %s: %w", p, err)
			}
		}
		next, nextOK, err := dockerfw.BlockOf(data)
		if err != nil {
			return nil, fmt.Errorf("firewall: %s: %w", p, err)
		}
		switch {
		case nextOK && (!curOK || !bytes.Equal(cur, next)):
			out = append(out, core.DiffLine{Op: "+", Text: path.Base(p) + ": Docker block written"})
		case curOK && !nextOK:
			out = append(out, core.DiffLine{Op: "-", Text: path.Base(p) + ": Docker block removed"})
		}
	}

	if c.cleanup {
		out = append(out, core.DiffLine{Op: "-", Text: "Docker chains removed"})
	}
	return append(out, c.notes...), nil
}

func ddfIntegrationText(on bool) string {
	if on {
		return "docker integration on"
	}
	return "docker integration off"
}

// ddfJoin writes items with text joined by ", ", or "none" when there is
// none.
func ddfJoin[T any](items []T, text func(T) string) string {
	if len(items) == 0 {
		return "none"
	}
	parts := make([]string, len(items))
	for i, it := range items {
		parts[i] = text(it)
	}
	return strings.Join(parts, ", ")
}

// ddfAddrPort writes an address with its port, IPv6 bracketed, or the
// address alone when the port is 0.
func ddfAddrPort(a netip.Addr, port uint16) string {
	if port == 0 {
		return a.String()
	}
	return netip.AddrPortFrom(a, port).String()
}

// ddfExemptionText writes an exemption as
// "[in <interface> ]<original destination> -> <destination>/<proto>", each
// address with its port when the port is not 0.
func ddfExemptionText(e dockerfw.Exemption) string {
	var b strings.Builder
	if e.InInterface != "" {
		b.WriteString("in " + e.InInterface + " ")
	}
	b.WriteString(ddfAddrPort(e.OrigDestination, e.OrigPort) + " -> " + ddfAddrPort(e.Destination, e.Port) + "/" + e.Proto)
	return b.String()
}

// ddfRuleText writes a container rule as
// "<selector> <host address|all>:<host port>/<proto> <kind> <source|any>".
func ddfRuleText(r dockerfw.ContainerRule) string {
	host := "all:" + fmt.Sprint(r.Binding.HostPort)
	if r.Binding.HostAddr.IsValid() {
		host = ddfAddrPort(r.Binding.HostAddr, r.Binding.HostPort)
	}
	from := "any"
	if r.From.IsValid() {
		from = r.From.String()
	}
	return r.Binding.Selector + " " + host + "/" + r.Binding.Proto + " " + string(r.Kind) + " " + from
}
