package firewall

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/firewall/dockerfw"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

// dockerUserInUse refuses, while the Docker integration is on (flag), a
// loaded DOCKER-USER chain holding a rule that is neither Docker's own
// -j RETURN nor one of the block's rules in the family's after file (spec
// 2.6, "Adoption of DOCKER-USER"). A family whose DOCKER-USER chain is not
// loaded has nothing to refuse. A damaged block fails with a plain error:
// it cannot say which rules are Darkpanel's, and no override must clear
// that.
func dockerUserInUse(cur fwstate.Loaded, cfg fwstate.Config, flag bool) error {
	if !flag {
		return nil
	}
	var listed []string
	for _, fam := range []struct {
		family fwstate.Family
		loaded *netfilter.Ruleset
		path   string
	}{
		{fwstate.V4, cur.V4, "/etc/ufw/after.rules"},
		{fwstate.V6, cur.V6, "/etc/ufw/after6.rules"},
	} {
		if fam.loaded == nil {
			continue
		}
		filter := fam.loaded.Table("filter")
		if filter == nil {
			continue
		}
		chain := filter.Chain(dockerfw.ChainDockerUser)
		if chain == nil {
			continue
		}
		known, err := drefKnownRules(cfg.Files[fam.path])
		if err != nil {
			return fmt.Errorf("firewall: %s: %w", fam.path, err)
		}
		for _, r := range dockerfw.Foreign(chain, known, fam.family) {
			listed = append(listed, string(fam.family)+" "+r.Text)
		}
	}
	if len(listed) == 0 {
		return nil
	}
	return errcode.New(errcode.FirewallDockerUserInUse,
		"DOCKER-USER holds "+strconv.Itoa(len(listed))+" rule(s) the Docker integration does not own: "+
			strings.Join(listed, "; ")+
			"; remove them, or run firewall.docker.repair with discardForeign: true and manual: true")
}

// drefKnownRules returns the DOCKER-USER rules of the block in the after
// file e; an absent file, a file without the block or a block without the
// chain gives none.
func drefKnownRules(e core.RestoreEntry) ([]netfilter.Rule, error) {
	if !e.Present {
		return nil, nil
	}
	block, ok, err := dockerfw.BlockOf(e.Data)
	if err != nil || !ok {
		return nil, err
	}
	rs, err := dockerfw.BlockRules(block)
	if err != nil {
		return nil, err
	}
	filter := rs.Table("filter")
	if filter == nil {
		return nil, nil
	}
	chain := filter.Chain(dockerfw.ChainDockerUser)
	if chain == nil {
		return nil, nil
	}
	return chain.Rules, nil
}

// refuseOpenIncoming refuses a change after which the Docker integration is
// on (flagAfter) and the incoming policy of the final configuration is
// ACCEPT: docker-proxy serves a proxied family's published ports through
// INPUT, so the policy must keep them closed (spec 2.6, "Proxied
// families").
func refuseOpenIncoming(flagAfter bool, final fwstate.Config) error {
	if !flagAfter || final.DefaultInput != "ACCEPT" {
		return nil
	}
	return errcode.New(errcode.FirewallUnsupportedIntegration,
		"the Docker integration needs DEFAULT_INPUT_POLICY DROP or REJECT: an ACCEPT policy leaves every proxied published port open")
}
