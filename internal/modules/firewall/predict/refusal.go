package predict

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

// IPv6Refusal returns nil, or a firewall.unsupported_configuration error
// (a hard refusal) when tr starts UFW with IPV6=no (TransEnable, or
// TransReload of an enabled UFW) while cur's IPv6 filter table holds a
// chain or rule UFW did not create (spec 2.3). Such a start replaces the
// whole IPv6 filter table with UFW's plain one, which no snapshot can
// undo. UFW's own items are its ufw6- chains, bare jumps to them from
// INPUT, OUTPUT and FORWARD, and the plain table's loopback rules; the
// error's detail names every other chain and rule and suggests IPV6=yes.
func IPv6Refusal(cfg fwstate.Config, cur fwstate.Loaded, tr fwstate.Transition) error {
	starts := tr == fwstate.TransEnable || (tr == fwstate.TransReload && cfg.Enabled)
	if cfg.IPv6 || !starts || cur.V6 == nil {
		return nil
	}
	f := cur.V6.Table("filter")
	if f == nil {
		return nil
	}
	var foreign []string
	for _, c := range f.Chains {
		switch {
		case strings.HasPrefix(c.Name, "ufw6-"):
		case !slices.Contains(filterBuiltins, c.Name):
			foreign = append(foreign, "chain "+c.Name)
		default:
			for i, r := range c.Rules {
				if !ufwBuiltinRule(c.Name, r) {
					foreign = append(foreign, fmt.Sprintf("%s rule %d (%s)", c.Name, i+1, shorten(strings.Join(strings.Fields(r.Text), " "))))
				}
			}
		}
	}
	if len(foreign) == 0 {
		return nil
	}
	if n := len(foreign) - refusalItems; n > 0 {
		foreign = append(foreign[:refusalItems:refusalItems], fmt.Sprintf("and %d more", n))
	}
	return errcode.New(errcode.FirewallUnsupportedConfiguration, fmt.Sprintf(
		"starting UFW with IPV6=no would replace the IPv6 filter table and remove what UFW did not create, which no snapshot can rebuild: %s; set IPV6=yes in /etc/default/ufw",
		strings.Join(foreign, ", ")))
}

// The refusal names at most refusalItems chains and rules, each rule's
// text cut to refusalText bytes, so a large foreign table does not flood
// the API response, the logs and the change record.
const (
	refusalItems = 10
	refusalText  = 80
)

func shorten(s string) string {
	if len(s) <= refusalText {
		return s
	}
	cut := refusalText
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "..."
}

// ufwBuiltinRule reports whether a rule of a built-in IPv6 filter chain is
// one UFW creates: a bare jump to one of its chains, or the plain table's
// loopback rule.
func ufwBuiltinRule(chain string, r netfilter.Rule) bool {
	key := netfilter.Key(r, true)
	if r.Target.Chain && strings.HasPrefix(r.Target.Name, "ufw6-") && key == "-j "+r.Target.Name {
		return true
	}
	return (chain == "INPUT" && key == "-i lo -j ACCEPT") || (chain == "OUTPUT" && key == "-o lo -j ACCEPT")
}
