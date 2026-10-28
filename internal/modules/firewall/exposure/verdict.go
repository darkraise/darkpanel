package exposure

import (
	"cmp"
	"net/netip"
	"slices"
	"strconv"
	"strings"

	"github.com/darkraise/darkpanel/internal/modules/firewall/eval"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

// DefaultMaxProbes is the number of probes a class may need before its
// verdict is unknown, when Input.MaxProbes is zero.
const DefaultMaxProbes = 4096

const (
	evOpen     = "open"
	evClosed   = "closed"
	evFiltered = "filtered"
	evPartial  = "partial"
	evUnknown  = "unknown"
)

// Input is everything one exposure evaluation reads: one family's ruleset and
// the host's interfaces. It holds no host state of its own.
type Input struct {
	Rules     *netfilter.Ruleset
	Family    fwstate.Family
	Route     fwstate.Router
	Ifaces    []Iface
	Private   []netip.Prefix
	ShieldsUp bool
	Describe  func(ref string) string // nil: the ref itself
	MaxProbes int                     // 0: DefaultMaxProbes
}

// Target is the port whose exposure is evaluated.
type Target struct {
	Proto string
	Addr  netip.Addr // unspecified: every address
	Port  uint16
}

// Allowed is one set of packets a partially open class lets through.
type Allowed struct {
	Sources   []string `json:"sources"`
	Ifaces    []string `json:"ifaces,omitempty"`
	SrcPorts  []string `json:"srcPorts,omitempty"`
	DecidedBy string   `json:"decidedBy"`
}

// Verdict is the exposure of the target to one source class.
type Verdict struct {
	Class     string    `json:"class"`
	Range     string    `json:"range,omitempty"`
	Verdict   string    `json:"verdict"` // open | closed | filtered | partial | unknown
	DecidedBy string    `json:"decidedBy,omitempty"`
	Allowed   []Allowed `json:"allowed,omitempty"`
	Note      string    `json:"note,omitempty"`
}

type evRun struct {
	in        Input
	t         Target
	srcs      []netip.Prefix
	ports     []PortRange
	maxProbes int
}

// evPoint is where a class's packets enter: a probe interface with the
// destination its packets carry, or the loopback output path.
type evPoint struct {
	iface string
	dst   netip.Addr
	local bool
}

type evProbe struct {
	atom  Range
	point evPoint
	port  PortRange
	res   fwstate.Result
}

// Evaluate returns one verdict per source class of in.Family that applies to
// the host, in the order Classes lists them.
func Evaluate(in Input, t Target) []Verdict {
	r := evRun{in: in, t: t, ports: PortAtoms(nil), maxProbes: cmp.Or(in.MaxProbes, DefaultMaxProbes)}
	if in.Rules != nil {
		var sports []PortRange
		r.srcs, _, sports = Cuts(in.Rules)
		r.ports = PortAtoms(sports)
	}
	var out []Verdict
	for _, c := range Classes(in.Private, in.Family) {
		if v, ok := r.class(c); ok {
			out = append(out, v)
		}
	}
	return out
}

func (r *evRun) class(c Class) (Verdict, bool) {
	v := Verdict{Class: c.Name}
	switch c.Name {
	case "private":
		v.Range = c.Range.String()
	case "internet", "tailnet", "localhost":
	default:
		return v, false
	}
	if c.Name == "tailnet" && !slices.ContainsFunc(r.in.Ifaces, func(i Iface) bool { return i.Up && i.Tailnet }) {
		return v, false
	}
	switch {
	case r.t.Addr.IsLoopback() && c.Name != "localhost":
		v.Verdict, v.DecidedBy = evClosed, "bound to loopback"
		return v, true
	case c.Name == "tailnet" && r.in.ShieldsUp:
		v.Verdict, v.DecidedBy = evClosed, "Tailscale shields-up"
		return v, true
	case r.in.Rules == nil:
		v.Verdict, v.Note = evUnknown, "no ruleset for this family"
		return v, true
	}
	r.decide(c, &v)
	if c.Name == "tailnet" && v.Verdict != evUnknown {
		v.Note = "host firewall; tailnet policy not evaluated"
	}
	return v, true
}

func (r *evRun) decide(c Class, v *Verdict) {
	atoms, points := r.atoms(c), r.points(c)
	count := len(atoms) * len(points) * len(r.ports)
	if count > r.maxProbes {
		v.Verdict, v.Note = evUnknown, "too many partitions"
		return
	}
	if count == 0 {
		v.Verdict, v.Note = evUnknown, "no interface to probe"
		return
	}
	proto := strings.ToLower(r.t.Proto)
	probes := make([]evProbe, 0, count)
	for _, a := range atoms {
		for _, pt := range points {
			for _, pr := range r.ports {
				pkt := fwstate.Packet{
					Family: r.in.Family, Path: "input", InIface: pt.iface,
					Src: a.From, Dst: pt.dst, SrcPort: pr.From, DstPort: r.t.Port,
					Proto: proto, CtState: "NEW",
				}
				if pt.local {
					pkt.Path, pkt.InIface, pkt.OutIface = "output", "", "lo"
				}
				probes = append(probes, evProbe{atom: a, point: pt, port: pr, res: eval.Evaluate(r.in.Rules, pkt, r.in.Route)})
			}
		}
	}

	accepts, rejects := 0, 0
	unknown := -1
	for i, p := range probes {
		switch p.res.Verdict {
		case fwstate.Accept:
			accepts++
		case fwstate.Reject:
			rejects++
		case fwstate.Unknown:
			if unknown < 0 {
				unknown = i
			}
		}
	}
	switch {
	case unknown >= 0:
		v.Verdict, v.Note = evUnknown, probes[unknown].res.Reason
	case accepts == len(probes):
		v.Verdict, v.DecidedBy = evOpen, r.decidedBy(probes)
	case rejects == len(probes):
		v.Verdict, v.DecidedBy = evClosed, r.decidedBy(probes)
	case accepts == 0:
		v.Verdict, v.DecidedBy = evFiltered, r.decidedBy(probes)
	default:
		v.Verdict, v.Allowed = evPartial, r.allowed(probes, points)
	}
}

func (r *evRun) describe(ref string) string {
	if r.in.Describe == nil {
		return ref
	}
	return r.in.Describe(ref)
}

func (r *evRun) decidedBy(probes []evProbe) string {
	var refs []string
	for _, p := range probes {
		if p.res.Decided != "" {
			refs = append(refs, r.describe(p.res.Decided))
		}
	}
	slices.Sort(refs)
	return strings.Join(slices.Compact(refs), "; ")
}

type evGroup struct {
	sources, ifaces, ports []string
}

func (r *evRun) allowed(probes []evProbe, points []evPoint) []Allowed {
	groups := map[string]*evGroup{}
	for _, p := range probes {
		if p.res.Verdict != fwstate.Accept {
			continue
		}
		key := r.describe(p.res.Decided)
		g := groups[key]
		if g == nil {
			g = &evGroup{}
			groups[key] = g
		}
		for _, pre := range p.atom.Prefixes() {
			g.sources = evAdd(g.sources, pre.String())
		}
		if p.point.iface != "" {
			g.ifaces = evAdd(g.ifaces, p.point.iface)
		}
		g.ports = evAdd(g.ports, evPortString(p.port))
	}
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	var probedIfaces []string
	for _, pt := range points {
		if !pt.local {
			probedIfaces = evAdd(probedIfaces, pt.iface)
		}
	}
	probed := len(probedIfaces)
	out := make([]Allowed, 0, len(keys))
	for _, k := range keys {
		g := groups[k]
		a := Allowed{Sources: g.sources, DecidedBy: k}
		if len(g.ifaces) != probed {
			slices.Sort(g.ifaces)
			a.Ifaces = g.ifaces
		}
		if len(g.ports) != len(r.ports) {
			a.SrcPorts = g.ports
		}
		out = append(out, a)
	}
	return out
}

func evAdd(list []string, s string) []string {
	if slices.Contains(list, s) {
		return list
	}
	return append(list, s)
}

func evPortString(p PortRange) string {
	if p.From == p.To {
		return strconv.Itoa(int(p.From))
	}
	return strconv.Itoa(int(p.From)) + "-" + strconv.Itoa(int(p.To))
}

func evIs4(f fwstate.Family) bool { return f == fwstate.V4 }

func evTailscale(is4 bool) netip.Prefix {
	if is4 {
		return netip.MustParsePrefix("100.64.0.0/10")
	}
	return netip.MustParsePrefix("fd7a:115c:a1e0::/48")
}

// localAddr is the address a connection from the host to itself uses.
func (r *evRun) localAddr() netip.Addr {
	switch {
	case r.t.Addr.IsValid() && !r.t.Addr.IsUnspecified():
		return r.t.Addr
	case evIs4(r.in.Family):
		return netip.MustParseAddr("127.0.0.1")
	}
	return netip.IPv6Loopback()
}

func (r *evRun) atoms(c Class) []Range {
	is4 := evIs4(r.in.Family)
	switch c.Name {
	case "internet":
		whole, others := netip.MustParsePrefix("::/0"), []string{"::1/128", "ff00::/8", "::/128"}
		if is4 {
			whole = netip.MustParsePrefix("0.0.0.0/0")
			others = []string{"127.0.0.0/8", "224.0.0.0/4", "0.0.0.0/32"}
		}
		exclude := slices.DeleteFunc(slices.Clone(r.in.Private), func(p netip.Prefix) bool { return p.Addr().Is4() != is4 })
		for _, s := range others {
			exclude = append(exclude, netip.MustParsePrefix(s))
		}
		return Atoms([]netip.Prefix{whole}, exclude, r.srcs)
	case "private":
		return Atoms([]netip.Prefix{c.Range}, nil, r.srcs)
	case "tailnet":
		return Atoms([]netip.Prefix{evTailscale(is4)}, nil, r.srcs)
	}
	a := r.localAddr()
	return []Range{{From: a, To: a}}
}

func (r *evRun) points(c Class) []evPoint {
	if c.Name == "localhost" {
		return []evPoint{{dst: r.localAddr(), local: true}}
	}
	concrete := r.t.Addr.IsValid() && !r.t.Addr.IsUnspecified()
	var out []evPoint
	for _, i := range r.in.Ifaces {
		if !i.Up {
			continue
		}
		if c.Name == "tailnet" {
			if !i.Tailnet {
				continue
			}
		} else if i.Loopback || i.Tailnet || i.Bridge {
			continue
		}
		if concrete {
			out = append(out, evPoint{iface: i.Name, dst: r.t.Addr})
			continue
		}
		var seen []netip.Addr
		for _, a := range i.Addrs {
			if evIs4(r.in.Family) != a.Is4() || a.Is4In6() || slices.Contains(seen, a) {
				continue
			}
			seen = append(seen, a)
			out = append(out, evPoint{iface: i.Name, dst: a})
		}
	}
	return out
}
