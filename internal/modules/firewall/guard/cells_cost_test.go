package guard

import (
	"fmt"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

func TestW11c10FlowRulesCountTheBeforeStateOfAWildSource(t *testing.T) {
	parse := func(s string) *netfilter.Ruleset {
		t.Helper()
		rs, err := netfilter.ParseRestore(strings.NewReader(s))
		if err != nil {
			t.Fatal(err)
		}
		return rs
	}
	now := parse("*filter\n:INPUT DROP [0:0]\n-A INPUT -p tcp -j ACCEPT\nCOMMIT\n")
	before := parse("*filter\n:INPUT DROP [0:0]\n-A INPUT -p tcp -j ACCEPT\n-A INPUT -p udp -j ACCEPT\nCOMMIT\n")
	if got := flowRules(flow{wild: wildSrc}, now, before); got != 3 {
		t.Errorf("a wild-source flow counts %d rules, want 3", got)
	}
	if got := flowRules(flow{}, now, before); got != 1 {
		t.Errorf("a flow without a wild source counts %d rules, want 1", got)
	}
	if got := flowRules(flow{wild: wildSrc}, now, nil); got != 1 {
		t.Errorf("a wild-source flow without a before-state counts %d rules, want 1", got)
	}
}

func TestW11c10CellFlowsCountTheBeforeState(t *testing.T) {
	now := v1r21State(t, v1r21Table("-A INPUT -p tcp -j ACCEPT", "-A INPUT -p udp -j ACCEPT"), v1r21Table())
	rules := make([]string, 2300)
	for i := range rules {
		rules[i] = fmt.Sprintf("-A INPUT -s 11.%d.%d.1/32 -j DROP", i>>8, i&255)
	}
	before := v1r21State(t, v1r21Table(rules...), v1r21Table())
	r := newRouter(&gHost{}, facts{ifaces: ckIfaces()}, now)
	fl := flow{label: "x", pkt: flPkt("input", "0.0.0.0", 0, "192.0.2.1", 443, "tcp"), wild: wildSrc}
	if got := r.cellFlows(fl, &before); len(got) != 1 || got[0].unknown == "" {
		t.Errorf("with a before-state: cellFlows = %+v, want one unknown flow", got)
	}
	if got := r.cellFlows(fl, nil); len(got) == 0 || got[0].unknown != "" {
		t.Errorf("without a before-state: cellFlows = %+v, want known flows", got)
	}
}
