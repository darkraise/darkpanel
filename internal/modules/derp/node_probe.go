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
	if stunAddr, err := d.nodeProbeSTUNAddr(ctx, in); err != nil {
		// tlsProbe resolves the name itself, so only the STUN measurement is lost.
		out.TLS = d.nodeProbeTLS(ctx, t)
		out.STUN = ProbeResult{Error: err.Error()}
	} else {
		out.TLS, out.STUN = d.probeOnce(ctx, t, stunAddr)
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
// are never on them.
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

// nodeProbeSTUNAddr is where the read sends its STUN binding: the given address, else the host name when it is an
// IP literal, else the first address the name resolves to.
func (d dp) nodeProbeSTUNAddr(ctx context.Context, in NodeProbeInput) (netip.AddrPort, error) {
	if in.Address != "" {
		return netip.AddrPortFrom(netip.MustParseAddr(in.Address), server.STUNPort), nil
	}
	if a, err := netip.ParseAddr(in.HostName); err == nil {
		return netip.AddrPortFrom(a, server.STUNPort), nil
	}
	a, err := d.nodeProbeResolve(ctx, in.HostName)
	if err != nil {
		return netip.AddrPort{}, err
	}
	return netip.AddrPortFrom(a, server.STUNPort), nil
}

// nodeProbeResolve returns the first address host resolves to, IPv4 preferred.
func (d dp) nodeProbeResolve(ctx context.Context, host string) (netip.Addr, error) {
	addrs, err := d.d.resolve(ctx, host)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("derp: cannot resolve %s: %w", host, err)
	}
	if len(addrs) == 0 {
		return netip.Addr{}, fmt.Errorf("derp: cannot resolve %s: no address", host)
	}
	// The resolver may answer an A record as an IPv4-mapped IPv6 address.
	for _, a := range addrs {
		if a = a.Unmap(); a.Is4() {
			return a, nil
		}
	}
	return addrs[0].Unmap(), nil
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
// the pin; letsencrypt resolves the name (first address, IPv4 preferred) and verifies the chain against the system
// roots.
func (d dp) target(ctx context.Context, s ServerStatus) (probeTarget, netip.AddrPort, error) {
	switch s.Mode {
	case server.ModeIP:
		a, err := netip.ParseAddr(s.HostName)
		if err != nil {
			return probeTarget{}, netip.AddrPort{}, fmt.Errorf("derp: the ip-mode server's host name %s is not an IP address", s.HostName)
		}
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
