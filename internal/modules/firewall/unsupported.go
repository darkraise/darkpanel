package firewall

import (
	"fmt"

	"github.com/darkraise/darkpanel/internal/modules/firewall/eval"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

// hostUnsupported lists why the host is an unsupported configuration (spec
// 2.1): the reasons of the configured state, those of the loaded state and
// each mangle or security rule the evaluator cannot model. The host is
// supported when it is empty. The result is never nil.
func hostUnsupported(cfg fwstate.Config, cur fwstate.Loaded) []string {
	out := append(append([]string{}, cfg.Unsupported...), cur.Unsupported...)
	return append(out, unmodelledRules(cur)...)
}

// unmodelledRules names, for both families, the mangle and security rules
// of st that the evaluator cannot model: every protected flow would be
// unknown, so the host is unsupported rather than merely failing the guard.
func unmodelledRules(st fwstate.Loaded) []string {
	var out []string
	for _, f := range []struct {
		tool string
		rs   *netfilter.Ruleset
	}{{"iptables", st.V4}, {"ip6tables", st.V6}} {
		for _, r := range eval.Unmodelled(f.rs) {
			out = append(out, fmt.Sprintf("%s-%s %s, which the evaluator cannot model", f.tool, st.Variant, r))
		}
	}
	return out
}
