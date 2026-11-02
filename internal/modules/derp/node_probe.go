package derp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/derp/server"
)

const (
	nodeProbeAction       = "derp.node.probe"
	nodeProbeMaxName      = 253
	nodeProbeMaxLabel     = 63
	nodeProbePinHexDigits = 64
	nodeProbeMinRegion    = 900
	nodeProbeMaxRegion    = 999
)

// nodeProbe declares derp.node.probe: this agent measures one DERP server over TLS and STUN.
func (d dp) nodeProbe() core.ActionDef {
	return core.DefineRead[NodeProbeInput, NodeProbe](core.ActionSpec{
		Name:    nodeProbeAction,
		Summary: "Probe a DERP server over TLS and STUN from this host.",
		Version: 1,
		Side:    core.SideAgent,
	}, nodeProbeHandler{d: d})
}

// nodeProbeHandler is derp.node.probe's handler.
type nodeProbeHandler struct{ d dp }

// Read checks the input, then measures the server; a failed measurement is part of the answer, not an error.
func (h nodeProbeHandler) Read(ctx context.Context, env core.Env, in NodeProbeInput) (NodeProbe, error) {
	if err := nodeProbeCheck(in); err != nil {
		return NodeProbe{}, err
	}
	d := h.d
	t := probeTarget{HostName: in.HostName, Address: in.Address, Port: server.HTTPSPort, CertName: in.CertName}
	var out NodeProbe
	if a, err := d.nodeProbeAddr(ctx, in); err != nil {
		out.TLS = ProbeResult{Error: err.Error()}
		out.STUN = out.TLS
	} else {
		if _, err := netip.ParseAddr(in.HostName); err != nil {
			t.Address = a.String()
		}
		out.TLS, out.STUN = d.probeOnce(ctx, t, netip.AddrPortFrom(a, server.STUNPort))
	}
	if in.RegionID != 0 {
		region, err := d.nodeProbeRegion(ctx, env, in.RegionID)
		if err != nil {
			return NodeProbe{}, err
		}
		out.Region = region
	}
	return out, nil
}

// nodeProbeCheck refuses an input this host must not probe. The input makes this host send a request to the named
// address, so loopback, unspecified, multicast and link-local addresses are refused: the controller's own servers
// are never on them. nodeProbeResolve refuses the same classes among the addresses a name resolves to.
func nodeProbeCheck(in NodeProbeInput) error {
	if in.HostName == "" {
		return nodeProbeRefuse("/hostName", "the host name is required")
	}
	if a, err := netip.ParseAddr(in.HostName); err == nil {
		if !nodeProbeAllowedAddr(a) {
			return nodeProbeRefuse("/hostName",
				"the host name must be an IP address without a zone that is not loopback, unspecified, multicast or link-local")
		}
	} else if !nodeProbeDNSName(in.HostName) {
		return nodeProbeRefuse("/hostName",
			"the host name must be an IP address or a lower-case DNS name of at most 253 bytes with at least two labels")
	}
	if in.Address != "" {
		a, err := netip.ParseAddr(in.Address)
		if err != nil || !nodeProbeAllowedAddr(a) {
			return nodeProbeRefuse("/address",
				"the address must be an IP address without a zone that is not loopback, unspecified, multicast or link-local")
		}
	}
	if in.CertName != "" && !nodeProbePin(in.CertName) {
		return nodeProbeRefuse("/certName", "the certificate name must be "+server.PinPrefix+" followed by 64 lower-case hex digits")
	}
	if in.RegionID != 0 && (in.RegionID < nodeProbeMinRegion || in.RegionID > nodeProbeMaxRegion) {
		return nodeProbeRefuse("/regionId", "the region id must be between 900 and 999")
	}
	return nil
}

// nodeProbeRefuse is the refusal of the input member at path.
func nodeProbeRefuse(path, message string) error {
	return core.Definite(&errcode.Error{Code: errcode.InvalidInput, Detail: "the probe input is invalid",
		Fields: []errcode.FieldError{{Path: path, Message: message}}})
}

// nodeProbeAllowedAddr reports whether a is an IP literal without a zone outside the refused classes.
func nodeProbeAllowedAddr(a netip.Addr) bool {
	if a.Zone() != "" {
		return false
	}
	a = a.Unmap()
	return !a.IsLoopback() && !a.IsUnspecified() && !a.IsMulticast() && !a.IsLinkLocalUnicast()
}

// nodeProbeDNSName reports whether s is a lower-case DNS name of at most 253 bytes with at least two labels.
func nodeProbeDNSName(s string) bool {
	if len(s) > nodeProbeMaxName {
		return false
	}
	labels := strings.Split(s, ".")
	if len(labels) < 2 {
		return false
	}
	for _, l := range labels {
		if len(l) < 1 || len(l) > nodeProbeMaxLabel || l[0] == '-' || l[len(l)-1] == '-' {
			return false
		}
		for i := range len(l) {
			if b := l[i]; (b < 'a' || b > 'z') && (b < '0' || b > '9') && b != '-' {
				return false
			}
		}
	}
	return true
}

// nodeProbePin reports whether s is a DERP map node pin: PinPrefix and 64 lower-case hex digits.
func nodeProbePin(s string) bool {
	hex, ok := strings.CutPrefix(s, server.PinPrefix)
	if !ok || len(hex) != nodeProbePinHexDigits {
		return false
	}
	for i := range len(hex) {
		if b := hex[i]; (b < '0' || b > '9') && (b < 'a' || b > 'f') {
			return false
		}
	}
	return true
}

// nodeProbeAddr is the one address the read probes over both TLS and STUN: the given address, else the host name
// when it is an IP literal, else the first address the name resolves to that a probe may reach.
func (d dp) nodeProbeAddr(ctx context.Context, in NodeProbeInput) (netip.Addr, error) {
	if in.Address != "" {
		return netip.MustParseAddr(in.Address), nil
	}
	if a, err := netip.ParseAddr(in.HostName); err == nil {
		return a, nil
	}
	return d.nodeProbeResolve(ctx, in.HostName)
}

// nodeProbeResolve returns the first address host resolves to that nodeProbeAllowedAddr accepts, IPv4 preferred. A
// name with no such address is refused, naming what it resolved to. On the DERP host itself this is the usual
// outcome for its own name: /etc/hosts maps a host's own name to 127.0.1.1, and the resolver then answers from that
// file alone. Such a host cannot probe its own server by name; the probe refuses rather than reach loopback.
func (d dp) nodeProbeResolve(ctx context.Context, host string) (netip.Addr, error) {
	addrs, err := d.d.resolve(ctx, host)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("derp: cannot resolve %s: %w", host, err)
	}
	if len(addrs) == 0 {
		return netip.Addr{}, fmt.Errorf("derp: cannot resolve %s: no address", host)
	}
	var allowed []netip.Addr
	for _, a := range addrs {
		if nodeProbeAllowedAddr(a) {
			// The resolver may answer an A record as an IPv4-mapped IPv6 address.
			allowed = append(allowed, a.Unmap())
		}
	}
	if len(allowed) == 0 {
		refused := make([]string, 0, len(addrs))
		for _, a := range addrs {
			refused = append(refused, a.String())
		}
		return netip.Addr{}, fmt.Errorf("derp: %s resolves only to loopback, unspecified, multicast or link-local addresses (%s), "+
			"which a probe does not reach; if it is this host's own name, /etc/hosts maps it to loopback, and only a host "+
			"that resolves it to its public address can probe it", host, strings.Join(refused, ", "))
	}
	for _, a := range allowed {
		if a.Is4() {
			return a, nil
		}
	}
	return allowed[0], nil
}

// nodeProbeRegion is this host's tailscaled diagnosis of the region; a failed diagnosis is reported in it, and only
// a done ctx fails the read.
func (d dp) nodeProbeRegion(ctx context.Context, env core.Env, regionID int) (*RegionDiagnostics, error) {
	clock := env.Clock
	if clock == nil {
		clock = core.SystemClock{}
	}
	bounded, cancel := context.WithCancel(ctx)
	defer cancel()
	timer := clock.AfterFunc(availableWait, cancel)
	defer timer.Stop()
	r, err := d.d.local().DebugDERPRegion(bounded, strconv.Itoa(regionID))
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return &RegionDiagnostics{Info: []string{}, Warnings: []string{},
			Errors: []string{"derp: the region diagnosis failed: " + err.Error()}}, nil
	}
	return &RegionDiagnostics{
		Info:     append([]string{}, r.Info...),
		Warnings: append([]string{}, r.Warnings...),
		Errors:   append([]string{}, r.Errors...),
	}, nil
}

// target is the probe target and STUN address of the server s describes: ip mode reaches its address and checks
// the pin; letsencrypt resolves the name (first address a probe may reach, IPv4 preferred) and verifies the chain
// against the system roots. s is an agent's report, so it is held to the rules its install input met
// (server.CheckConfig: a public IP or a lower-case DNS name) and to the pin format before anything is resolved.
func (d dp) target(ctx context.Context, s ServerStatus) (probeTarget, netip.AddrPort, error) {
	if s.Mode == server.ModeIP || s.Mode == server.ModeLetsEncrypt {
		if err := server.CheckConfig(server.Config{Mode: s.Mode, HostName: s.HostName}); err != nil {
			return probeTarget{}, netip.AddrPort{}, fmt.Errorf("derp: the %s-mode server's host name %q is not probed: %s",
				s.Mode, s.HostName, errcode.From(err).Fields[0].Message)
		}
	}
	switch s.Mode {
	case server.ModeIP:
		if s.CertName != "" && !nodeProbePin(s.CertName) {
			return probeTarget{}, netip.AddrPort{}, fmt.Errorf("derp: the ip-mode server's certificate name %q is not a pin", s.CertName)
		}
		a := netip.MustParseAddr(s.HostName)
		return probeTarget{HostName: s.HostName, Port: server.HTTPSPort, CertName: s.CertName},
			netip.AddrPortFrom(a, server.STUNPort), nil
	case server.ModeLetsEncrypt:
		a, err := d.nodeProbeResolve(ctx, s.HostName)
		if err != nil {
			return probeTarget{}, netip.AddrPort{}, err
		}
		return probeTarget{HostName: s.HostName, Address: a.String(), Port: server.HTTPSPort},
			netip.AddrPortFrom(a, server.STUNPort), nil
	case "":
		return probeTarget{}, netip.AddrPort{}, errors.New("derp: no DERP server is installed to probe")
	default:
		return probeTarget{}, netip.AddrPort{}, fmt.Errorf("derp: the server's mode %q has no probe target", s.Mode)
	}
}

// probeOnce takes one TLS and then one STUN measurement of a server.
func (d dp) probeOnce(ctx context.Context, t probeTarget, stunAddr netip.AddrPort) (tls, stun ProbeResult) {
	tls = d.nodeProbeTLS(ctx, t)
	stun = d.nodeProbeSTUN(ctx, stunAddr)
	return tls, stun
}

// nodeProbeTLS is one TLS probe of t as a result.
func (d dp) nodeProbeTLS(ctx context.Context, t probeTarget) ProbeResult {
	return nodeProbeResult(d.d.tlsProbe(ctx, t))
}

// nodeProbeSTUN is one STUN binding with addr as a result.
func (d dp) nodeProbeSTUN(ctx context.Context, addr netip.AddrPort) ProbeResult {
	return nodeProbeResult(d.d.stun(ctx, addr))
}

// nodeProbeResult is a measurement as a result: its latency in milliseconds, or its failure.
func nodeProbeResult(latency time.Duration, err error) ProbeResult {
	if err != nil {
		return ProbeResult{Error: err.Error()}
	}
	return ProbeResult{OK: true, LatencyMs: float64(latency) / float64(time.Millisecond)}
}

// readNodeProbe asks host hostID's agent to probe the server in describes, through its derp.node.probe/v1.
func readNodeProbe(ctx context.Context, env core.Env, hostID string, in NodeProbeInput) (NodeProbe, error) {
	fail := func(err error) (NodeProbe, error) {
		return NodeProbe{}, fmt.Errorf("derp: probe from host %s: %w", hostID, err)
	}
	input, err := json.Marshal(in)
	if err != nil {
		return fail(err)
	}
	agent, err := env.Agents.Agent(ctx, hostID)
	if err != nil {
		return fail(err)
	}
	raw, err := agent.Read(ctx, env.Principal, core.ActionRef{Name: nodeProbeAction, Version: 1}, input)
	if err != nil {
		return fail(err)
	}
	var out NodeProbe
	if err := json.Unmarshal(raw, &out); err != nil {
		return fail(err)
	}
	return out, nil
}
