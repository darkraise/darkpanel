package derp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strconv"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/tailscale"
)

const (
	probeController     = "controller"
	probeAgentNoAnswer  = "the agent did not answer"
	probeTailscaleState = "tailscale.status"
)

// serverProbe declares derp.server.probe: the controller and chosen agents probe one DERP server, and the read
// shows which nodes see and use its region.
func (d dp) serverProbe() core.ActionDef {
	return core.DefineRead[ServerProbeInput, ServerProbe](core.ActionSpec{
		Name:    ID + ".server.probe",
		Summary: "Probe a DERP server's TLS and STUN reachability from the controller and chosen agents, and show which nodes use its region.",
		Version: 1,
		Side:    core.SideController,
	}, probeReader{d: d})
}

// probeReader is derp.server.probe's handler.
type probeReader struct{ d dp }

// Read probes the server; a failed measurement is part of the answer, not an error.
func (r probeReader) Read(ctx context.Context, env core.Env, in ServerProbeInput) (ServerProbe, error) {
	return r.d.probeServer(ctx, env, in)
}

// probeHost is one core_hosts row.
type probeHost struct{ ID, Name string }

// probeServer is derp.server.probe's whole read: the server's status, the nodes that see and use its region, and one
// TLS and STUN measurement from the controller and from each chosen agent.
func (d dp) probeServer(ctx context.Context, env core.Env, in ServerProbeInput) (ServerProbe, error) {
	hosts, err := probeHosts(ctx, env)
	if err != nil {
		return ServerProbe{}, err
	}
	i := slices.IndexFunc(hosts, func(h probeHost) bool { return h.ID == in.HostID })
	if i < 0 {
		return ServerProbe{}, core.Definite(errcode.New(errcode.NotFound, "host "+in.HostID+" not found"))
	}
	hostName := hosts[i].Name
	agents, err := probeAgents(hosts, in)
	if err != nil {
		return ServerProbe{}, err
	}
	selves, err := probeSelves(ctx, env, hosts)
	if err != nil {
		return ServerProbe{}, err
	}
	nodes, capWarnings, err := d.probeNodes(ctx, env, hosts, selves)
	if err != nil {
		return ServerProbe{}, err
	}
	whois := make([]string, 0, len(nodes))
	for _, n := range nodes {
		whois = append(whois, n.Address)
	}
	status, err := readStatus(ctx, env, in.HostID, whois)
	if err != nil {
		return ServerProbe{}, err
	}
	if !status.Installed {
		return ServerProbe{}, core.Definite(errcode.New(errcode.DERPNotReady, "no DERP server is installed on "+hostName))
	}
	rec, _, err := loadServerRecord(ctx, env.DB.Read(), in.HostID)
	if err != nil {
		return ServerProbe{}, err
	}
	probeFillNodes(nodes, status, selves, rec.RegionCode)
	out := ServerProbe{HostID: in.HostID, RegionID: rec.RegionID, RegionCode: rec.RegionCode, Server: status, Nodes: nodes}
	ctl, err := d.probeFromController(ctx, status)
	if err != nil {
		return ServerProbe{}, err
	}
	out.Sources = []ProbeSource{ctl}
	nodeIn := NodeProbeInput{HostName: status.HostName, CertName: status.CertName, RegionID: rec.RegionID}
	for _, a := range agents {
		src, err := probeFromAgent(ctx, env, a, nodeIn)
		if err != nil {
			return ServerProbe{}, err
		}
		out.Sources = append(out.Sources, src)
	}
	ready, _, warnings := probeReady(out)
	out.Ready, out.Warnings = ready, append(warnings, capWarnings...)
	return out, nil
}

// probeHosts lists the core_hosts rows by id.
func probeHosts(ctx context.Context, env core.Env) ([]probeHost, error) {
	rows, err := env.DB.Read().QueryContext(ctx, `SELECT id, name FROM core_hosts ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("derp: read the hosts: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var hosts []probeHost
	for rows.Next() {
		var h probeHost
		if err := rows.Scan(&h.ID, &h.Name); err != nil {
			return nil, fmt.Errorf("derp: read the hosts: %w", err)
		}
		hosts = append(hosts, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("derp: read the hosts: %w", err)
	}
	return hosts, nil
}

// probeAgents are the hosts that probe the server: the given list in its order, else every other host by id, at
// most maxProbeAgents.
func probeAgents(hosts []probeHost, in ServerProbeInput) ([]probeHost, error) {
	if len(in.Agents) == 0 {
		var agents []probeHost
		for _, h := range hosts {
			if h.ID != in.HostID && len(agents) < maxProbeAgents {
				agents = append(agents, h)
			}
		}
		return agents, nil
	}
	if len(in.Agents) > maxProbeAgents {
		msg := "at most " + strconv.Itoa(maxProbeAgents) + " agents probe a server at once"
		return nil, core.Definite(&errcode.Error{Code: errcode.InvalidInput, Detail: msg,
			Fields: []errcode.FieldError{{Path: "/agents", Message: msg}}})
	}
	agents := make([]probeHost, 0, len(in.Agents))
	for i, id := range in.Agents {
		j := slices.IndexFunc(hosts, func(h probeHost) bool { return h.ID == id })
		if j < 0 {
			return nil, core.Definite(&errcode.Error{Code: errcode.InvalidInput, Detail: "unknown host " + id,
				Fields: []errcode.FieldError{{Path: "/agents/" + strconv.Itoa(i), Message: "unknown host"}}})
		}
		agents = append(agents, hosts[j])
	}
	return agents, nil
}

// probeSelves reads each host's tailscale.status, one after another. A host whose status cannot be read, or whose
// node has no address, is left out: only the caller's own cancellation fails the read.
func probeSelves(ctx context.Context, env core.Env, hosts []probeHost) (map[string]tailscale.Status, error) {
	selves := map[string]tailscale.Status{}
	for _, h := range hosts {
		st, err := probeSelf(ctx, env, h.ID)
		if err != nil {
			if cerr := ctx.Err(); cerr != nil {
				return nil, cerr
			}
			continue
		}
		if st.Self != nil && len(st.Self.Addresses) > 0 {
			selves[h.ID] = st
		}
	}
	return selves, nil
}

// probeSelf reads host hostID's tailscale.status resource through its agent.
func probeSelf(ctx context.Context, env core.Env, hostID string) (tailscale.Status, error) {
	agent, err := env.Agents.Agent(ctx, hostID)
	if err != nil {
		return tailscale.Status{}, err
	}
	raw, err := agent.Resource(ctx, env.Principal, probeTailscaleState)
	if err != nil {
		return tailscale.Status{}, err
	}
	var st tailscale.Status
	if err := json.Unmarshal(raw, &st); err != nil {
		return tailscale.Status{}, fmt.Errorf("derp: decode the tailscale status of host %s: %w", hostID, err)
	}
	return st, nil
}

// probeNodes are the nodes whose visibility and use the probe reports: the tailnet's devices when an account is
// connected, else the hosts' own nodes; at most serverStatusMaxWhois, since one status read looks up no more. The
// warnings say when nodes were cut.
func (d dp) probeNodes(ctx context.Context, env core.Env, hosts []probeHost, selves map[string]tailscale.Status) ([]NodeUse, []string, error) {
	connected, err := d.d.connected(ctx, env.DB)
	if err != nil {
		return nil, nil, err
	}
	nodes := []NodeUse{}
	if connected {
		devices, err := d.d.devices(ctx, env)
		if err != nil {
			return nil, nil, err
		}
		for _, dev := range devices {
			if len(dev.Addresses) == 0 {
				continue
			}
			addr := dev.Addresses[0]
			nodes = append(nodes, NodeUse{Name: dev.Name, Address: addr, HostID: probeHostOf(hosts, selves, addr)})
		}
	} else {
		for _, h := range hosts {
			if st, ok := selves[h.ID]; ok {
				nodes = append(nodes, NodeUse{Name: h.Name, Address: st.Self.Addresses[0], HostID: h.ID})
			}
		}
	}
	warnings := []string{}
	if len(nodes) > serverStatusMaxWhois {
		warnings = append(warnings, fmt.Sprintf("the tailnet has %d devices; only the first %d were checked for visibility",
			len(nodes), serverStatusMaxWhois))
		nodes = nodes[:serverStatusMaxWhois]
	}
	return nodes, warnings, nil
}

// probeHostOf is the id of the host whose node holds addr, or "" when no host's does.
func probeHostOf(hosts []probeHost, selves map[string]tailscale.Status, addr string) string {
	for _, h := range hosts {
		if st, ok := selves[h.ID]; ok && slices.Contains(st.Self.Addresses, addr) {
			return h.ID
		}
	}
	return ""
}

// probeFillNodes sets each node's visibility from the DERP host's whois answer, and its home region and observed use
// of regionCode from its host's tailscale.status.
func probeFillNodes(nodes []NodeUse, status ServerStatus, selves map[string]tailscale.Status, regionCode string) {
	visible := make(map[string]bool, len(status.Visible))
	for _, v := range status.Visible {
		visible[v.Address] = v.Visible
	}
	for i := range nodes {
		n := &nodes[i]
		n.Visible = visible[n.Address]
		st, ok := selves[n.HostID]
		if n.HostID == "" || !ok {
			continue
		}
		n.Preferred = st.PreferredDERP
		n.Observed = regionCode != "" && slices.ContainsFunc(st.Peers, func(p tailscale.Peer) bool {
			return p.Connection == "derp" && p.Region == regionCode
		})
	}
}

// probeFromController is the controller's own measurement of the server. A server it cannot reach is a failed
// source; only the caller's own cancellation is an error.
func (d dp) probeFromController(ctx context.Context, status ServerStatus) (ProbeSource, error) {
	src := ProbeSource{Source: probeController, Name: probeController}
	t, stunAddr, err := d.target(ctx, status)
	if err == nil {
		err = probeCheckDial(t, stunAddr)
	}
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return ProbeSource{}, cerr
		}
		src.Error = err.Error()
		src.TLS, src.STUN = ProbeResult{Error: src.Error}, ProbeResult{Error: src.Error}
		return src, nil
	}
	src.TLS, src.STUN = d.probeOnce(ctx, t, stunAddr)
	if cerr := ctx.Err(); cerr != nil {
		return ProbeSource{}, cerr
	}
	return src, nil
}

// probeCheckDial refuses to let the controller dial an address it must not: the server's name comes from an agent's
// answer and may resolve to the controller's own loopback or link-local network.
func probeCheckDial(t probeTarget, stunAddr netip.AddrPort) error {
	dial := t.Address
	if dial == "" {
		dial = t.HostName
	}
	a, err := netip.ParseAddr(dial)
	if err != nil || !nodeProbeAllowedAddr(a) || !nodeProbeAllowedAddr(stunAddr.Addr()) {
		return fmt.Errorf("derp: the controller does not probe %s at %s: loopback, unspecified, multicast and link-local addresses are refused",
			t.HostName, dial)
	}
	return nil
}

// probeFromAgent is agent a's measurement of the server. An agent that does not answer is a failed source carrying
// its client-safe detail; only the caller's own cancellation is an error.
func probeFromAgent(ctx context.Context, env core.Env, a probeHost, in NodeProbeInput) (ProbeSource, error) {
	src := ProbeSource{Source: a.ID, Name: a.Name}
	p, err := readNodeProbe(ctx, env, a.ID, in)
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return ProbeSource{}, cerr
		}
		src.Error = probeAgentNoAnswer
		var coded *errcode.Error
		if errors.As(err, &coded) && coded.Code != errcode.Internal && coded.Detail != "" {
			src.Error = coded.Detail
		}
		src.TLS, src.STUN = ProbeResult{Error: src.Error}, ProbeResult{Error: src.Error}
		return src, nil
	}
	src.TLS, src.STUN, src.Region = p.TLS, p.STUN, p.Region
	return src, nil
}

// probeReady reports whether the probe backs publishing the server's region: the controller's TLS and STUN pass, and
// at least one agent other than the DERP host passes both. With no such agent at all the controller alone decides,
// with a warning; when not ready, reason names the first failing check.
func probeReady(p ServerProbe) (ready bool, reason string, warnings []string) {
	warnings = []string{}
	i := slices.IndexFunc(p.Sources, func(s ProbeSource) bool { return s.Source == probeController })
	if i < 0 {
		return false, probeController + ": not probed", warnings
	}
	if reason, ok := probePasses(p.Sources[i]); !ok {
		return false, reason, warnings
	}
	agents := 0
	for _, s := range p.Sources {
		// The DERP host's probe of itself proves nothing about reaching it from elsewhere.
		if s.Source == probeController || s.Source == p.HostID {
			continue
		}
		agents++
		why, ok := probePasses(s)
		if ok {
			return true, "", warnings
		}
		if agents == 1 {
			reason = why
		}
	}
	if agents == 0 {
		return true, "", []string{"no agent other than the DERP host probed " + p.Server.HostName +
			"; only the controller's probe backs the region"}
	}
	return false, reason, warnings
}

// probePasses reports whether a source's TLS and STUN both passed, else the failing check as "<name>: tls: <error>"
// or "<name>: stun: <error>".
func probePasses(s ProbeSource) (reason string, ok bool) {
	switch {
	case !s.TLS.OK:
		return s.Name + ": tls: " + s.TLS.Error, false
	case !s.STUN.OK:
		return s.Name + ": stun: " + s.STUN.Error, false
	}
	return "", true
}
