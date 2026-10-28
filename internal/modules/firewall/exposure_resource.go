package firewall

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/firewall/dockerfw"
	"github.com/darkraise/darkpanel/internal/modules/firewall/exposure"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/modules/firewall/model"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
	"github.com/darkraise/darkpanel/internal/platform/netstat"
)

// ExposureView is the output of resource firewall.exposure (v1, spec 2.7):
// one row per listening socket or published port, each with the verdict of
// the loaded rules for every source class, and the notes that qualify them.
type ExposureView struct {
	Rows  []ExposureRow `json:"rows"`
	Notes []string      `json:"notes,omitempty"` // out of sync, unsupported, Docker unavailable
}

// ExposureRow is one port a source can try to reach: a listening socket, a
// published container port, or both when docker-proxy holds the socket.
type ExposureRow struct {
	Proto     string             `json:"proto"`
	Addr      string             `json:"addr"`
	Port      uint16             `json:"port"`
	Family    string             `json:"family"`
	Process   string             `json:"process,omitempty"`
	Container *ExposureContainer `json:"container,omitempty"`
	Classes   []exposure.Verdict `json:"classes"`
}

// ExposureContainer is the published port of a container that a row stands
// for.
type ExposureContainer struct {
	Name          string           `json:"name"`
	Binding       dockerfw.Binding `json:"binding"`
	ContainerPort uint16           `json:"containerPort"`
	Proxied       bool             `json:"proxied"`
}

// exposureInput is everything exposureRows reads.
type exposureInput struct {
	cfg       fwstate.Config
	cur       fwstate.Loaded
	st        dockerState
	sockets   []netstat.Socket
	ifaces    []netstat.Interface
	routeGet  func(netstat.RouteQuery) (netstat.Route, error)
	shieldsUp bool
}

// exposureResource is resource firewall.exposure.
func (f fw) exposureResource() core.ResourceDef {
	return core.DefineResource(ID+".exposure", f.exposureView)
}

// exposureView reads resource firewall.exposure. A host that is an
// unsupported configuration has no rows, since no verdict can be trusted;
// every other shortcoming of the inputs is a note beside the rows.
func (f fw) exposureView(ctx context.Context, env core.Env) (ExposureView, error) {
	cfg, err := f.d.readConfig(f.d.root)
	if err != nil {
		return ExposureView{}, fmt.Errorf("firewall: exposure: %w", err)
	}
	cur, err := f.d.readLoaded(ctx, env)
	if err != nil {
		return ExposureView{}, fmt.Errorf("firewall: exposure: %w", err)
	}
	view := ExposureView{Rows: []ExposureRow{}}
	if reasons := hostUnsupported(cfg, cur); len(reasons) > 0 {
		view.Notes = []string{"the host is an unsupported configuration, so no verdict is computed: " + strings.Join(reasons, "; ")}
		return view, nil
	}
	st, err := f.dockerState(ctx, env, cur)
	if err != nil {
		return ExposureView{}, fmt.Errorf("firewall: exposure: %w", err)
	}
	socks, err := f.d.sockets()
	if err != nil {
		return ExposureView{}, fmt.Errorf("firewall: exposure: read the sockets: %w", err)
	}
	ifaces, err := f.d.interfaces()
	if err != nil {
		return ExposureView{}, fmt.Errorf("firewall: exposure: read the interfaces: %w", err)
	}

	on, known, err := f.d.shieldsUp(ctx)
	if err != nil {
		on, known = false, false
		view.Notes = append(view.Notes, "tailscaled's shields-up state could not be read, so the tailnet class assumes it is off: "+err.Error())
	}
	diffs, err := f.syncDiffs(cfg, cur)
	switch {
	case err != nil:
		view.Notes = append(view.Notes, "the comparison with UFW's configuration failed: "+err.Error())
	case len(diffs) > 0:
		names := make([]string, len(diffs))
		for i, d := range diffs {
			names[i] = string(d.Family) + " " + d.Table + "/" + d.Chain
		}
		view.Notes = append(view.Notes, "out of sync with UFW's configuration ("+strings.Join(names, ", ")+"): verdicts describe the loaded state")
	}
	if !st.available {
		view.Notes = append(view.Notes, "Docker is unavailable ("+st.reason+"): published ports are not listed")
	}
	view.Notes = append(view.Notes, exposureManualNotes(cfg, st.facts)...)

	view.Rows = exposureRows(exposureInput{
		cfg: cfg, cur: cur, st: st, sockets: socks, ifaces: ifaces,
		routeGet: f.d.routeGet, shieldsUp: on && known,
	})
	return view, nil
}

// xvRowKey identifies a row: the family is that of the address.
type xvRowKey struct {
	proto string
	addr  netip.Addr
	port  uint16
}

// xvDockerProxy is the process Docker starts to serve a published port that
// the container has no address for.
const xvDockerProxy = "docker-proxy"

// exposureRows builds the rows of in in port order: the published ports of
// the containers and the listening sockets, merged by (protocol, address,
// port, family), each with the evaluator's verdicts.
func exposureRows(in exposureInput) []ExposureRow {
	rows := map[xvRowKey]*ExposureRow{}
	row := func(k xvRowKey) *ExposureRow {
		r, ok := rows[k]
		if !ok {
			family := fwstate.V6
			if k.addr.Is4() {
				family = fwstate.V4
			}
			r = &ExposureRow{Proto: k.proto, Addr: k.addr.String(), Port: k.port, Family: string(family)}
			rows[k] = r
		}
		return r
	}

	published := map[xvRowKey]bool{}
	for _, c := range in.st.facts.Containers {
		for _, p := range c.Ports {
			if (p.Proto != "tcp" && p.Proto != "udp") || !p.HostAddr.IsValid() {
				continue
			}
			addr := p.HostAddr.Unmap()
			k := xvRowKey{p.Proto, addr, p.HostPort}
			r := row(k)
			published[k] = true
			if r.Container != nil {
				continue
			}
			binding := dockerfw.Binding{Selector: strings.TrimPrefix(c.Name, "/"), HostPort: p.HostPort, Proto: p.Proto}
			if !addr.IsUnspecified() {
				binding.HostAddr = addr
			}
			r.Container = &ExposureContainer{
				Name: binding.Selector, Binding: binding, ContainerPort: p.ContainerPort,
				Proxied: !xvHasAddress(c, addr.Is4()),
			}
		}
	}

	procs := map[xvRowKey][]string{}
	for _, s := range in.sockets {
		if !s.Listening() || s.Local.Port() == 0 {
			continue
		}
		addr := s.Local.Addr().Unmap()
		keys := []xvRowKey{{string(s.Proto), addr, s.Local.Port()}}
		if addr == netip.IPv6Unspecified() {
			// A socket bound to [::] also accepts IPv4 unless it sets IPV6_V6ONLY.
			keys = append(keys, xvRowKey{string(s.Proto), netip.IPv4Unspecified(), s.Local.Port()})
		}
		for _, k := range keys {
			row(k)
			for _, p := range s.Processes {
				if (p.Comm == xvDockerProxy && published[k]) || slices.Contains(procs[k], p.Comm) {
					continue
				}
				procs[k] = append(procs[k], p.Comm)
			}
		}
	}

	inputs := map[bool]exposure.Input{}
	route := exposureRoute(in.routeGet, in.ifaces, in.st.facts.Networks, in.cur.Sysctl)
	ifaces := xvEvalIfaces(in.ifaces, in.st.facts.Networks)
	for _, is4 := range []bool{true, false} {
		x := exposure.Input{
			Rules: in.cur.V6, Family: fwstate.V6, Route: route, Ifaces: ifaces,
			Private: in.st.cfg.Settings.PrivateSources, ShieldsUp: in.shieldsUp,
		}
		rules := model.Merge(nil, in.cfg.Rules6)
		if is4 {
			x.Rules, x.Family = in.cur.V4, fwstate.V4
			rules = model.Merge(in.cfg.Rules4, nil)
		}
		x.Describe = exposureDescribe(x.Rules, rules)
		inputs[is4] = x
	}

	out := make([]ExposureRow, 0, len(rows))
	for k, r := range rows {
		r.Process = strings.Join(procs[k], ", ")
		r.Classes = exposure.Evaluate(inputs[k.addr.Is4()], exposure.Target{Proto: k.proto, Addr: k.addr, Port: k.port})
		if r.Classes == nil {
			r.Classes = []exposure.Verdict{}
		}
		out = append(out, *r)
	}
	slices.SortFunc(out, func(a, b ExposureRow) int {
		return cmp.Or(cmp.Compare(a.Port, b.Port), strings.Compare(a.Proto+" "+a.Family+" "+a.Addr, b.Proto+" "+b.Family+" "+b.Addr))
	})
	return out
}

// xvHasAddress reports whether any endpoint of c has an address of the
// family; a published port without one is served by docker-proxy.
func xvHasAddress(c dockerfw.Container, v4 bool) bool {
	return slices.ContainsFunc(c.Endpoints, func(e dockerfw.Endpoint) bool {
		if v4 {
			return e.Addr4.IsValid()
		}
		return e.Addr6.IsValid()
	})
}

// xvEvalIfaces are the interfaces as the evaluator sees them. Link-local
// addresses are left out: no remote source can reach them through a route.
func xvEvalIfaces(ifaces []netstat.Interface, nets []dockerfw.Network) []exposure.Iface {
	bridges := map[string]bool{}
	for _, n := range nets {
		if n.Bridge != "" {
			bridges[n.Bridge] = true
		}
	}
	out := make([]exposure.Iface, 0, len(ifaces))
	for _, i := range ifaces {
		e := exposure.Iface{Name: i.Name, Up: i.Up, Loopback: i.Loopback, Tailnet: i.Name == "tailscale0", Bridge: bridges[i.Name]}
		for _, p := range i.Addrs {
			if a := p.Addr().Unmap(); !a.IsLinkLocalUnicast() {
				e.Addrs = append(e.Addrs, a)
			}
		}
		out = append(out, e)
	}
	return out
}

// exposureRoute is the router of the evaluation: where a packet goes once
// nat PREROUTING has set its destination. A destination in a Docker network
// is a known fact and needs no lookup from the kernel.
func exposureRoute(routeGet func(netstat.RouteQuery) (netstat.Route, error), ifaces []netstat.Interface, nets []dockerfw.Network, sysctl map[string]string) fwstate.Router {
	local := map[netip.Addr]bool{}
	for _, i := range ifaces {
		for _, p := range i.Addrs {
			local[p.Addr().Unmap()] = true
		}
	}
	return func(p fwstate.Packet) (string, string, error) {
		dst := p.Dst.Unmap()
		if dst.IsLoopback() || local[dst] {
			if p.Path == "output" {
				return "output", "lo", nil
			}
			return "input", "", nil
		}
		if p.Path == "output" {
			rt, err := routeGet(netstat.RouteQuery{Dst: dst, Src: p.Src, Mark: p.Mark})
			switch {
			case err != nil:
				return "", "", fmt.Errorf("firewall: route to %s: %w", dst, err)
			case rt.OutIface == "":
				return "", "", fmt.Errorf("firewall: the route to %s has no single out-interface", dst)
			}
			return "output", rt.OutIface, nil
		}
		key := "net/ipv4/ip_forward"
		if !dst.Is4() {
			key = "net/ipv6/conf/all/forwarding"
		}
		if sysctl[key] == "0" {
			return "", "", errors.New("firewall: forwarding is off (" + key + " = 0)")
		}
		for _, n := range nets {
			if n.Bridge != "" && slices.ContainsFunc(n.Subnets, func(s netip.Prefix) bool { return s.Contains(dst) }) {
				return "forward", n.Bridge, nil
			}
		}
		rt, err := routeGet(netstat.RouteQuery{Dst: dst, Src: p.Src, IIF: p.InIface, Mark: p.Mark})
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
}

// xvUserChains are UFW's chains of user rules and the direction of the rules
// each holds, for both families.
var xvUserChains = map[string]model.Direction{
	"ufw-user-input": model.DirIn, "ufw6-user-input": model.DirIn,
	"ufw-user-output": model.DirOut, "ufw6-user-output": model.DirOut,
	"ufw-user-forward": model.DirRoute, "ufw6-user-forward": model.DirRoute,
}

// exposureDescribe names a deciding rule. A ref "<table>/<chain>#<n>" that rs
// holds becomes the rule's text and, when known, where the rule came from;
// any other ref, a policy included, is returned as it is. rules are the
// configured UFW rules of the ruleset's family.
func exposureDescribe(rs *netfilter.Ruleset, rules []model.Rule) func(ref string) string {
	return func(ref string) string {
		table, rest, ok := strings.Cut(ref, "/")
		if !ok {
			return ref
		}
		name, num, ok := strings.Cut(rest, "#")
		if !ok {
			return ref
		}
		n, err := strconv.Atoi(num)
		chain := chainOf(rs, table, name)
		if err != nil || n < 1 || chain == nil || n > len(chain.Rules) {
			return ref
		}
		return ref + ": " + chain.Rules[n-1].Text + xvOrigin(table, chain, n-1, rules)
	}
}

// xvOrigin is the suffix that says where the rule at index i of chain came
// from. A chain of user rules is attributed only when it holds exactly one
// rule per configured rule of its direction: logging and limit rules write
// several lines per rule, which would shift every later one.
func xvOrigin(table string, chain *netfilter.Chain, i int, rules []model.Rule) string {
	name := chain.Name
	if dir, ok := xvUserChains[name]; ok {
		var ofDir []model.Rule
		for _, r := range rules {
			if r.Direction == dir {
				ofDir = append(ofDir, r)
			}
		}
		if len(ofDir) != len(chain.Rules) {
			return ""
		}
		return " [ufw " + ruleText(ofDir[i]) + "]"
	}
	switch {
	case name == dockerfw.ChainDockerUser, strings.HasPrefix(name, "darkpanel-docker-"), strings.HasPrefix(name, "dp-b-"):
		return " [Darkpanel Docker block]"
	case name == "DOCKER", strings.HasPrefix(name, "DOCKER-"):
		return " [Docker]"
	case table == "raw" && name == "PREROUTING" && chain.Rules[i].Target.Name == "DROP":
		return " [Docker raw-table protection]"
	case strings.HasPrefix(name, "ts-"):
		return " [tailscaled]"
	case strings.HasPrefix(name, "ufw-"), strings.HasPrefix(name, "ufw6-"):
		return " [UFW framework]"
	}
	return ""
}

// exposureManualNotes warns about the manual route rules to containers: UFW
// holds them, but a published port reaches a container only through a
// Darkpanel binding.
func exposureManualNotes(cfg fwstate.Config, facts dockerfw.Facts) []string {
	var out []string
	for _, text := range statusManualRoutes(cfg, facts) {
		out = append(out, text+": no effect without a Darkpanel binding")
	}
	return out
}
