package guard

import (
	"errors"
	"fmt"
	"net/netip"
	"strconv"

	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/netstat"
)

// probe is one packet the guard evaluates, or a flow it cannot evaluate.
type probe struct {
	desc    string         // FlowResult.Flow
	pkt     fwstate.Packet // with its in- and out-interface
	unknown string         // non-empty: not evaluated, and why
}

// tailnetIface is tailscaled's interface, and tailnetRanges are the
// address ranges it assigns. tailscaled drops these sources on other
// interfaces only for IPv4 and only when its netfilter rules are installed,
// and 100.64.0.0/10 is also carrier-grade NAT space, so fromTailnet also
// requires the route back to the source to leave through tailnetIface.
const tailnetIface = "tailscale0"

var tailnetRanges = []netip.Prefix{netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("fd7a:115c:a1e0::/48")}

// router places flows on this host's interfaces and routes. Its route
// method is the fwstate.Router the evaluator calls.
type router struct {
	host  Host
	f     facts
	st    fwstate.Loaded
	local map[netip.Addr]bool
}

func newRouter(h Host, f facts, st fwstate.Loaded) *router {
	return &router{host: h, f: f, st: st, local: localAddrs(f.ifaces)}
}

// probes turns a flow into the packets to evaluate: an incoming flow
// without a fixed in-interface on every eligible in-interface, any other
// flow once with the out-interface its route gives. A route that cannot be
// resolved makes the flow unknown, except that a stand-in destination
// without a route of its family has nothing to protect. Every lookup
// carries the packet's mark, which policy rules may match.
func (r *router) probes(fl flow) []probe {
	if fl.unknown != "" {
		return []probe{{desc: fl.label, unknown: fl.unknown}}
	}
	p := fl.pkt
	fail := func(reason string) []probe { return []probe{{desc: describe(fl.label, p), unknown: reason}} }
	lookup := func(q netstat.RouteQuery) (netstat.Route, bool, []probe) {
		rt, err := r.host.RouteGet(q)
		switch {
		case err != nil && fl.standIn && errors.Is(err, netstat.ErrNoRoute):
			return rt, false, nil
		case err != nil:
			return rt, false, fail(fmt.Sprintf("the route to %s cannot be resolved: %v", q.Dst, err))
		case rt.Type != "local" && rt.OutIface == "":
			return rt, false, fail(fmt.Sprintf("the route to %s has no single out-interface", q.Dst))
		}
		return rt, true, nil
	}
	switch p.Path {
	case "output":
		q := netstat.RouteQuery{Dst: p.Dst, Mark: p.Mark}
		if !p.Src.IsUnspecified() {
			q.Src = p.Src
		}
		rt, ok, out := lookup(q)
		if !ok {
			return out
		}
		if p.Src.IsUnspecified() {
			if !rt.PrefSrc.IsValid() {
				return fail(fmt.Sprintf("the route to %s has no source address", p.Dst))
			}
			p.Src = rt.PrefSrc.Unmap()
		}
		p.OutIface = rt.OutIface
		return []probe{{desc: describe(fl.label, p), pkt: p}}
	case "forward":
		if off := r.forwardingOff(p.Family); off != "" {
			return fail(off)
		}
		rt, ok, out := lookup(netstat.RouteQuery{Dst: p.Dst, Src: p.Src, IIF: p.InIface, Mark: p.Mark})
		if !ok {
			return out
		}
		if rt.Type == "local" {
			p.Path = "input"
		} else {
			p.OutIface = rt.OutIface
		}
		return []probe{{desc: describe(fl.label, p), pkt: p}}
	}
	if p.Dst.IsUnspecified() {
		rt, err := r.host.RouteGet(netstat.RouteQuery{Dst: p.Src, Mark: p.Mark})
		if err != nil {
			return fail(fmt.Sprintf("the route back to %s cannot be resolved: %v", p.Src, err))
		}
		if !rt.PrefSrc.IsValid() {
			return fail(fmt.Sprintf("the route back to %s has no source address", p.Src))
		}
		p.Dst = rt.PrefSrc.Unmap()
	}
	if !r.local[p.Dst] {
		return fail(p.Dst.String() + " is not an address of this host")
	}
	if p.InIface != "" {
		return []probe{{desc: describe(fl.label, p), pkt: p}}
	}
	ifaces, err := r.eligible(p)
	if err != nil {
		return fail(err.Error())
	}
	if len(ifaces) == 0 {
		return fail("no interface can receive the flow")
	}
	out := make([]probe, 0, len(ifaces))
	for _, name := range ifaces {
		q := p
		q.InIface = name
		out = append(out, probe{desc: describe(fl.label, q), pkt: q})
	}
	return out
}

// eligible lists the in-interfaces an incoming packet may arrive on: every
// up, non-loopback interface, except that for IPv4 an interface under
// strict reverse-path filtering takes only packets whose route back to the
// source leaves through it. A source in a tailnet range arrives only on
// tailscale0 while that interface is up and the route back to the source
// leaves through it.
func (r *router) eligible(p fwstate.Packet) ([]string, error) {
	if r.fromTailnet(p.Src) {
		return []string{tailnetIface}, nil
	}
	var out []string
	back := ""
	looked := false
	for _, i := range r.f.ifaces {
		if !i.Up || i.Loopback {
			continue
		}
		if p.Family == fwstate.V4 {
			rp, err := r.rpFilter(i.Name)
			if err != nil {
				return nil, err
			}
			if rp == 1 {
				if !looked {
					q := netstat.RouteQuery{Dst: p.Src, Mark: p.Mark}
					if r.local[p.Dst] {
						q.Src = p.Dst
					}
					rt, err := r.host.RouteGet(q)
					if err != nil {
						return nil, fmt.Errorf("the route back to %s cannot be resolved: %w", p.Src, err)
					}
					back, looked = rt.OutIface, true
				}
				if back != i.Name {
					continue
				}
			}
		}
		out = append(out, i.Name)
	}
	return out, nil
}

// fromTailnet reports whether src is a tailnet address, tailscale0 is up
// and the route back to src leaves through tailscale0, so a working session
// from src is one tailscaled carries. A failed lookup is not a tailnet
// source.
func (r *router) fromTailnet(src netip.Addr) bool {
	src = src.Unmap()
	in := false
	for _, p := range tailnetRanges {
		in = in || p.Contains(src)
	}
	if !in {
		return false
	}
	up := false
	for _, i := range r.f.ifaces {
		up = up || (i.Name == tailnetIface && i.Up)
	}
	if !up {
		return false
	}
	rt, err := r.host.RouteGet(netstat.RouteQuery{Dst: src})
	return err == nil && rt.OutIface == tailnetIface
}

// rpFilter is an interface's effective rp_filter: the larger of all's and
// the interface's value, each from the state being checked when it holds
// the key and from the live host otherwise.
func (r *router) rpFilter(name string) (int, error) {
	eff := 0
	for _, n := range []string{"all", name} {
		key := "net/ipv4/conf/" + n + "/rp_filter"
		v, ok := r.f.rpFilter[n]
		if s, has := r.st.Sysctl[key]; has {
			parsed, err := strconv.Atoi(s)
			if err != nil {
				return 0, fmt.Errorf("%s is %q in the checked state, not a number", key, s)
			}
			v, ok = parsed, true
		}
		if !ok {
			return 0, fmt.Errorf("%s was not read", key)
		}
		eff = max(eff, v)
	}
	return eff, nil
}

// forwardingOff says why the checked state forwards no packet of family
// fam, or "" when it does or does not say.
func (r *router) forwardingOff(fam fwstate.Family) string {
	key := "net/ipv4/ip_forward"
	if fam == fwstate.V6 {
		key = "net/ipv6/conf/all/forwarding"
	}
	if r.st.Sysctl[key] == "0" {
		return "forwarding is off (" + key + " = 0)"
	}
	return ""
}

// route is the evaluator's fwstate.Router: where a packet goes once nat
// PREROUTING has set its destination. Every lookup carries the packet's
// mark, which policy rules may match. A locally generated packet to an
// address of this host leaves through lo.
func (r *router) route(p fwstate.Packet) (string, string, error) {
	dst := p.Dst.Unmap()
	if dst.IsLoopback() || r.local[dst] {
		if p.Path == "output" {
			return "output", "lo", nil
		}
		return "input", "", nil
	}
	if p.Path == "output" {
		rt, err := r.host.RouteGet(netstat.RouteQuery{Dst: dst, Src: p.Src, Mark: p.Mark})
		switch {
		case err != nil:
			return "", "", fmt.Errorf("firewall: route to %s: %w", dst, err)
		case rt.OutIface == "":
			return "", "", fmt.Errorf("firewall: the route to %s has no single out-interface", dst)
		}
		return "output", rt.OutIface, nil
	}
	if off := r.forwardingOff(p.Family); off != "" {
		return "", "", errors.New("firewall: " + off)
	}
	rt, err := r.host.RouteGet(netstat.RouteQuery{Dst: dst, Src: p.Src, IIF: p.InIface, Mark: p.Mark})
	switch {
	case err != nil:
		return "", "", fmt.Errorf("firewall: route to %s from %s on %s: %w", dst, p.Src, p.InIface, err)
	case rt.Type == "local":
		return "input", "", nil
	case rt.OutIface == "":
		return "", "", fmt.Errorf("firewall: the route to %s has no single out-interface", dst)
	}
	return "forward", rt.OutIface, nil
}

// describe names a flow for the guard's message: label, protocol when not
// TCP, addresses and the interfaces known so far.
func describe(label string, p fwstate.Packet) string {
	s := label
	if p.Proto != "tcp" {
		s += " " + p.Proto
	}
	s += " from " + p.Src.String() + " to " + netip.AddrPortFrom(p.Dst, p.DstPort).String()
	if p.InIface != "" {
		s += " on " + p.InIface
	}
	if p.OutIface != "" {
		s += " out " + p.OutIface
	}
	return s
}
