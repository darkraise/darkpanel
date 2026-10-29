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
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/firewall/dockerfw"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
	"github.com/darkraise/darkpanel/internal/platform/netstat"
)

const (
	dfBridgeOption = "com.docker.network.bridge.name"
	dfGatewayV4    = "com.docker.network.bridge.gateway_mode_ipv4"
	dfGatewayV6    = "com.docker.network.bridge.gateway_mode_ipv6"
	dfProjectLabel = "com.docker.compose.project"
	dfServiceLabel = "com.docker.compose.service"

	dfUnreachable = "the Docker daemon is not reachable"
)

// dockerState is everything the Docker integration reads before it decides
// or changes anything: the daemon's detection and facts, the stored
// configuration and container rules, the block those realize and the host's
// interfaces. With the daemon unreachable, available is false, reason says
// why, det and facts are empty, and the stored parts are still filled.
type dockerState struct {
	available  bool
	reason     string // why Docker is unavailable
	det        dockerfw.Detection
	facts      dockerfw.Facts
	cfg        dockerConfig
	rows       []dockerfw.ContainerRule
	realized   dockerfw.Realized
	interfaces []netstat.Interface
}

// dockerState reads the Docker state of the host whose loaded firewall is
// cur. An unreachable daemon is not an error, so the integration can still be
// turned off; every other failure is.
func (f fw) dockerState(ctx context.Context, env core.Env, cur fwstate.Loaded) (dockerState, error) {
	cfg, err := readDockerConfig(ctx, env.DB)
	if err != nil {
		return dockerState{}, err
	}
	rows, err := listContainerRules(ctx, env.DB)
	if err != nil {
		return dockerState{}, err
	}
	ifs, err := f.d.interfaces()
	if err != nil {
		return dockerState{}, fmt.Errorf("firewall: list the interfaces: %w", err)
	}
	st := dockerState{cfg: cfg, rows: rows, interfaces: ifs}

	info, err := f.d.docker.Info(ctx)
	if err != nil {
		return dfDaemonFailed(st, err)
	}
	facts, internal, err := f.dfReadFacts(ctx)
	if err != nil {
		return dfDaemonFailed(st, err)
	}
	det, err := f.dfDetect(ctx, env, cur, info, facts, internal)
	if err != nil {
		return dockerState{}, err
	}
	st.available, st.det, st.facts = true, det, facts
	st.realized = dockerfw.Realize(rows, facts, cfg.Settings)
	return st, nil
}

// dfDaemonFailed turns an unreachable daemon into an unavailable state and
// wraps any other daemon failure.
func dfDaemonFailed(st dockerState, err error) (dockerState, error) {
	if !errors.Is(err, dockerapi.ErrUnavailable) {
		return dockerState{}, fmt.Errorf("firewall: docker: %w", err)
	}
	st.reason = dfUnreachable
	st.realized = dockerfw.Realize(st.rows, dockerfw.Facts{}, st.cfg.Settings)
	return st, nil
}

// dfReadFacts reads the daemon's networks and running containers as
// dockerfw's facts. internal names the networks Docker reports as internal.
func (f fw) dfReadFacts(ctx context.Context) (facts dockerfw.Facts, internal map[string]bool, err error) {
	nets, err := f.d.docker.ListNetworks(ctx, nil)
	if err != nil {
		return dockerfw.Facts{}, nil, err
	}
	internal = map[string]bool{}
	bridges := map[string]dockerfw.Network{}
	for _, n := range nets {
		fn := dfNetworkFact(n)
		facts.Networks = append(facts.Networks, fn)
		if fn.Driver == "bridge" {
			bridges[fn.Name] = fn
		}
		if n.Internal {
			internal[n.Name] = true
		}
	}
	slices.SortFunc(facts.Networks, func(a, b dockerfw.Network) int { return strings.Compare(a.Name, b.Name) })

	list, err := f.d.docker.ListContainers(ctx, dockerapi.ListContainersOptions{Filters: dockerapi.Filters{"status": {"running"}}})
	if err != nil {
		return dockerfw.Facts{}, nil, err
	}
	for _, s := range list {
		c, err := f.d.docker.InspectContainer(ctx, s.ID)
		if err != nil {
			return dockerfw.Facts{}, nil, err
		}
		if !c.State.Running {
			continue
		}
		facts.Containers = append(facts.Containers, dfContainerFact(c, bridges))
	}
	slices.SortFunc(facts.Containers, func(a, b dockerfw.Container) int { return strings.Compare(a.Name, b.Name) })
	return facts, internal, nil
}

func dfNetworkFact(n dockerapi.Network) dockerfw.Network {
	fn := dockerfw.Network{ID: n.ID, Name: n.Name, Driver: n.Driver, IPv6: n.EnableIPv6,
		GatewayModeV4: n.Options[dfGatewayV4], GatewayModeV6: n.Options[dfGatewayV6]}
	for _, c := range n.IPAM.Config {
		if c.Subnet.IsValid() {
			fn.Subnets = append(fn.Subnets, c.Subnet.Masked())
		}
	}
	if n.Driver == "bridge" {
		fn.Bridge = n.Options[dfBridgeOption]
		if fn.Bridge == "" {
			fn.Bridge = dockerfw.Bridge(fn)
		}
	}
	return fn
}

func dfContainerFact(c dockerapi.ContainerJSON, bridges map[string]dockerfw.Network) dockerfw.Container {
	fc := dockerfw.Container{ID: c.ID, Name: strings.TrimPrefix(c.Name, "/"),
		Project: c.Config.Labels[dfProjectLabel], Service: c.Config.Labels[dfServiceLabel]}
	for key, bindings := range c.NetworkSettings.Ports {
		port, proto, ok := strings.Cut(key, "/")
		cport, err := strconv.ParseUint(port, 10, 16)
		if !ok || err != nil || (proto != "tcp" && proto != "udp") {
			continue
		}
		for _, b := range bindings {
			// Port 0 is no port a client can reach, so it realizes no rule.
			hport, err := strconv.ParseUint(b.HostPort, 10, 16)
			if err != nil || hport == 0 {
				continue
			}
			addr := netip.IPv4Unspecified()
			if b.HostIP != "" {
				a, err := netip.ParseAddr(b.HostIP)
				if err != nil {
					continue
				}
				addr = a.Unmap()
			}
			fc.Ports = append(fc.Ports, dockerfw.Published{HostAddr: addr, HostPort: uint16(hport), ContainerPort: uint16(cport), Proto: proto})
		}
	}
	slices.SortFunc(fc.Ports, func(a, b dockerfw.Published) int {
		return cmp.Or(cmp.Compare(a.ContainerPort, b.ContainerPort), strings.Compare(a.Proto, b.Proto),
			cmp.Compare(a.HostPort, b.HostPort), a.HostAddr.Compare(b.HostAddr))
	})
	for name, e := range c.NetworkSettings.Networks {
		n, ok := bridges[name]
		if e == nil || !ok {
			continue
		}
		ep := dockerfw.Endpoint{Network: name, Bridge: n.Bridge, Addr4: e.IPAddress.Unmap()}
		if !e.GlobalIPv6Address.Is4In6() {
			ep.Addr6 = e.GlobalIPv6Address
		}
		fc.Endpoints = append(fc.Endpoints, ep)
	}
	slices.SortFunc(fc.Endpoints, func(a, b dockerfw.Endpoint) int { return strings.Compare(a.Network, b.Network) })
	return fc
}

// dfDetect finds which iptables variant holds Docker's chains and, when
// exactly one does, whether its raw table protects the running containers.
func (f fw) dfDetect(ctx context.Context, env core.Env, cur fwstate.Loaded, info dockerapi.SystemInfo,
	facts dockerfw.Facts, internal map[string]bool) (dockerfw.Detection, error) {
	det := dockerfw.Detection{Version: info.ServerVersion, Driver: "iptables", UFWVariant: cur.Variant}
	if b := info.FirewallBackend; b != nil && b.Driver != "" {
		det.Driver = b.Driver
	}
	var holding []string
	for _, variant := range []string{"nft", "legacy"} {
		rs, err := f.d.readVariant(ctx, env, fwstate.V4, variant, []string{"filter"})
		if err != nil {
			return dockerfw.Detection{}, fmt.Errorf("firewall: read the %s filter table: %w", variant, err)
		}
		if rs != nil && dockerfw.DockerChains(rs.Table("filter")) {
			holding = append(holding, variant)
		}
	}
	switch len(holding) {
	case 0:
		return det, nil
	case 2:
		det.DockerVariant = "both"
		return det, nil
	}
	det.DockerVariant = holding[0]
	rs, err := f.d.readVariant(ctx, env, fwstate.V4, det.DockerVariant, []string{"raw"})
	if err != nil {
		return dockerfw.Detection{}, fmt.Errorf("firewall: read the %s raw table: %w", det.DockerVariant, err)
	}
	if rs != nil {
		if raw := rs.Table("raw"); raw != nil {
			det.RawProtection = dockerfw.RawProtected(raw, dfWithoutInternal(facts, internal))
		}
	}
	return det, nil
}

// dfWithoutInternal copies facts without the endpoints on internal networks:
// Docker's bridge driver loads no raw drop rule for them. The copy shares no
// slice with facts.
func dfWithoutInternal(facts dockerfw.Facts, internal map[string]bool) dockerfw.Facts {
	out := dockerfw.Facts{Networks: facts.Networks, Containers: make([]dockerfw.Container, len(facts.Containers))}
	for i, c := range facts.Containers {
		c.Endpoints = slices.DeleteFunc(slices.Clone(c.Endpoints), func(e dockerfw.Endpoint) bool { return internal[e.Network] })
		out.Containers[i] = c
	}
	return out
}

// dockerUnsupported returns the reasons the integration cannot manage the
// host of st: those of its detection, then each bridge network whose bridge
// name the block cannot carry.
func dockerUnsupported(st dockerState) []string {
	return append(dockerfw.Unsupported(st.det), dockerfw.BadBridges(st.facts.Networks)...)
}

// blockFiles returns the contents of /etc/ufw/after.rules and
// /etc/ufw/after6.rules with the realized Docker block of their family
// (enabled) or without any block. It writes nothing, and refuses to write a
// block over a bridge name the block cannot carry.
func blockFiles(cfg fwstate.Config, st dockerState, enabled bool) (map[string][]byte, error) {
	if bad := dockerfw.BadBridges(st.facts.Networks); enabled && len(bad) > 0 {
		return nil, errcode.New(errcode.FirewallUnsupportedIntegration, strings.Join(bad, "; "))
	}
	out := map[string][]byte{}
	for _, p := range []struct {
		path   string
		family fwstate.Family
	}{{"/etc/ufw/after.rules", fwstate.V4}, {"/etc/ufw/after6.rules", fwstate.V6}} {
		e, ok := cfg.Files[p.path]
		if !ok || !e.Present {
			return nil, fmt.Errorf("firewall: %s is missing", p.path)
		}
		if _, _, err := dockerfw.BlockOf(e.Data); err != nil {
			return nil, fmt.Errorf("firewall: %s: %w", p.path, err)
		}
		var data []byte
		var err error
		if enabled {
			data, err = dockerfw.WithBlock(e.Data, dockerfw.Render(st.realized.Block, p.family))
		} else {
			data, err = dockerfw.WithoutBlock(e.Data)
		}
		if err != nil {
			return nil, fmt.Errorf("firewall: %s: %w", p.path, err)
		}
		out[p.path] = data
	}
	return out, nil
}
