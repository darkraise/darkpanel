package guard

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

// GuardID is the id of the firewall's lockout guard.
const GuardID = "firewall.lockout"

// FlowResult is one protected flow on one in-interface and its verdict.
type FlowResult struct {
	Flow   string // human-readable: "ssh from 203.0.113.7 to 192.0.2.1:22 on eth0"
	Result fwstate.Result
}

// Checker runs the lockout guard against a host.
type Checker struct {
	Host Host // nil: LiveHost{}
}

// Check runs the lockout guard against this host; see Checker.Check.
func Check(ctx context.Context, env core.Env, st fwstate.Loaded, in Input) (core.Guard, []FlowResult, error) {
	return Checker{}.Check(ctx, env, st, in)
}

// Check gathers the host's live facts, builds every protected flow from
// them and from in, evaluates each flow with in.Eval against st on every
// eligible in-interface, and returns the firewall.lockout guard with one
// FlowResult per flow and interface. The guard fails when any flow is not
// accepted. Check never decides overridability or hard refusals; a fact it
// cannot read is an error.
func (c Checker) Check(ctx context.Context, env core.Env, st fwstate.Loaded, in Input) (core.Guard, []FlowResult, error) {
	if in.Eval == nil {
		return core.Guard{}, nil, errors.New("firewall: the guard has no evaluator")
	}
	h := c.Host
	if h == nil {
		h = LiveHost{}
	}
	f, err := gatherFacts(ctx, env, h)
	if err != nil {
		return core.Guard{}, nil, err
	}
	r := newRouter(h, f, st)
	var results []FlowResult
	for _, fl := range buildFlows(in, f) {
		for _, p := range r.probes(fl) {
			results = append(results, FlowResult{Flow: p.desc, Result: evaluate(r, p, in.Eval)})
		}
	}
	return verdict(results), results, nil
}

// evaluate walks one probe through the ruleset of its family.
func evaluate(r *router, p probe, eval func(*netfilter.Ruleset, fwstate.Packet, fwstate.Router) fwstate.Result) fwstate.Result {
	if p.unknown != "" {
		return fwstate.Result{Verdict: fwstate.Unknown, Reason: p.unknown}
	}
	rs := r.st.V4
	if p.pkt.Family == fwstate.V6 {
		rs = r.st.V6
	}
	if rs == nil {
		return fwstate.Result{Verdict: fwstate.Unknown, Reason: "no " + string(p.pkt.Family) + " ruleset was read"}
	}
	return eval(rs, p.pkt, r.route)
}

// verdict is the guard for the results: OK only when every flow is
// accepted, so a drop, a reject, an unknown and any verdict the guard does
// not know all fail it.
func verdict(results []FlowResult) core.Guard {
	var failed []string
	for _, fr := range results {
		if fr.Result.Verdict == fwstate.Accept {
			continue
		}
		v := string(fr.Result.Verdict)
		if v == "" {
			v = "no verdict"
		}
		s := fr.Flow + ": " + v
		if fr.Result.Reason != "" {
			s += " (" + fr.Result.Reason + ")"
		}
		failed = append(failed, s)
	}
	switch {
	case len(failed) > 0:
		return core.Guard{ID: GuardID, Message: "the change would cut " + strings.Join(failed, "; ")}
	case len(results) == 0:
		return core.Guard{ID: GuardID, OK: true, Message: "no protected flow to check"}
	}
	return core.Guard{ID: GuardID, OK: true, Message: fmt.Sprintf("every protected flow passes (%d checked)", len(results))}
}
