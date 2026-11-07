package guard

import (
	"github.com/darkraise/darkpanel/internal/modules/firewall/eval"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

// cellFlows is fl once per cell of the internet that the checked ruleset
// of its family partitions the public space into, with the wild end set
// to the cell's representative address. A flow without a wild end, or one
// that is not evaluated, is returned as it is.
func (r *router) cellFlows(fl flow) []flow {
	if fl.wild == wildNone || fl.unknown != "" {
		return []flow{fl}
	}
	v6 := fl.pkt.Family == fwstate.V6
	rs := r.st.V4
	if v6 {
		rs = r.st.V6
	}
	cells := eval.InternetCells(rs, v6)
	out := make([]flow, 0, len(cells))
	for _, c := range cells {
		cf := fl
		if fl.wild == wildSrc {
			cf.pkt.Src = c.Rep
		} else {
			cf.pkt.Dst = c.Rep
		}
		cf.span = c.From.String() + "-" + c.To.String()
		out = append(out, cf)
	}
	return out
}

// checkFlow evaluates fl in every cell of its wild end. Every cell must let
// the flow through, so the results of the first blocked cell are returned
// and later cells are not evaluated. When no cell is blocked, the results
// of the first cell that produced any stand for all of them; a cell without
// a route of its family produces none.
func (r *router) checkFlow(fl flow, ev func(*netfilter.Ruleset, fwstate.Packet, fwstate.Router) fwstate.Result) []FlowResult {
	var first []FlowResult
	for _, cf := range r.cellFlows(fl) {
		var results []FlowResult
		blocked := false
		for _, p := range r.probes(cf) {
			res := evaluate(r, p, ev)
			blocked = blocked || res.Verdict != fwstate.Accept
			results = append(results, FlowResult{Flow: p.desc, Result: res})
		}
		if blocked {
			return results
		}
		if first == nil {
			first = results
		}
	}
	return first
}
