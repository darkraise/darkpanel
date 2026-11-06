package npm

import (
	"cmp"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/firewall/dockerfw"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/modules/firewall/loaded"
	"github.com/darkraise/darkpanel/internal/modules/npm/npmapi"
	"github.com/darkraise/darkpanel/internal/modules/npm/proxy"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
	"github.com/darkraise/darkpanel/internal/platform/netstat"
)

const (
	// lockKey is the lock every mutating npm action takes.
	lockKey = "npm"
	// commitWait is how long the Engine or NPM may still commit a call whose answer was lost: Engine calls, proxy hosts
	// and access lists.
	commitWait = 2 * time.Minute
	// certCommitWait is commitWait for a certificate, whose ACME order NPM may still complete.
	certCommitWait = 10 * time.Minute

	// networkName is the user-defined bridge network the NPM container runs on.
	networkName = "darkpanel-npm"
	// containerName is the NPM container's name.
	containerName = "darkpanel-npm"
	// dataVolume holds NPM's database and configuration, mounted at /data.
	dataVolume = "darkpanel-npm-data"
	// certVolume holds NPM's certificates, mounted at /etc/letsencrypt.
	certVolume = "darkpanel-npm-letsencrypt"
	// apiURL is NPM's admin API, published on the loopback address only.
	apiURL = "http://127.0.0.1:81"
	// backupDir is the managed directory of the upgrade's volume archives.
	backupDir = "/var/lib/darkpanel/agent/npm-backups"

	// absentSocket is the socket a test binary dials instead of the host's. Test binaries that link the module open a
	// runtime that may call it, and they must never reach the host's Docker daemon; a path that never exists makes
	// every call fail as a missing socket.
	absentSocket = "/nonexistent/darkpanel/docker.sock"
	// absentURL is the NPM API a test binary dials instead of apiURL: a port nothing listens on.
	absentURL = "http://127.0.0.1:1"

	// reservedPanelNetwork is the network the panel's own container serves on; no proxy host may forward into it.
	reservedPanelNetwork = "darkpanel-panel"

	// The Docker network options the raw-table protection depends on.
	bridgeOption = "com.docker.network.bridge.name"
	gatewayV4    = "com.docker.network.bridge.gateway_mode_ipv4"
	gatewayV6    = "com.docker.network.bridge.gateway_mode_ipv6"
)

// nm carries the module's host dependencies; tests replace them.
type nm struct{ d deps }

// deps are the host functions the module reaches through.
type deps struct {
	docker   dockerAPI
	api      func(ctx context.Context, env core.Env) (npmAPI, error)         // a token-bearing client for the stored admin
	anon     func() npmAPI                                                   // no token: for Health, CreateAdmin and Token
	info     func(ctx context.Context) (dockerapi.SystemInfo, error)         // the Engine's system information
	raw      func(ctx context.Context, env core.Env) (bool, error)           // the raw-table protection Docker loads is in place
	random   func(n int) ([]byte, error)                                     // n random bytes
	readFile func(name string) ([]byte, error)                               // os.ReadFile
	readDir  func(name string) ([]os.DirEntry, error)                        // os.ReadDir
	panel    func(ctx context.Context, env core.Env) (proxy.Reserved, error) // the upstreams a proxy host may not forward to
	sockets  func() ([]netstat.Socket, error)                                // the host's listening sockets
}

// dockerAPI is the part of the Engine API client the module uses.
type dockerAPI interface {
	ListContainers(ctx context.Context, o dockerapi.ListContainersOptions) ([]dockerapi.ContainerSummary, error)
	InspectContainer(ctx context.Context, id string) (dockerapi.ContainerJSON, error)
	CreateContainer(ctx context.Context, cfg dockerapi.ContainerCreate) (string, error)
	StartContainer(ctx context.Context, id string) (bool, error)
	StopContainer(ctx context.Context, id string, timeout time.Duration) (bool, error)
	RemoveContainer(ctx context.Context, id string, o dockerapi.RemoveContainerOptions) error
	ListNetworks(ctx context.Context, f dockerapi.Filters) ([]dockerapi.Network, error)
	InspectNetwork(ctx context.Context, id string) (dockerapi.Network, error)
	CreateNetwork(ctx context.Context, n dockerapi.NetworkCreate) (string, error)
	RemoveNetwork(ctx context.Context, id string) error
	ConnectNetwork(ctx context.Context, network, container string, ep *dockerapi.EndpointSettings) error
	DisconnectNetwork(ctx context.Context, network, container string, force bool) error
	ListVolumes(ctx context.Context, f dockerapi.Filters) ([]dockerapi.Volume, error)
	InspectVolume(ctx context.Context, name string) (dockerapi.Volume, error)
	CreateVolume(ctx context.Context, v dockerapi.VolumeCreate) (dockerapi.Volume, error)
	RemoveVolume(ctx context.Context, name string, force bool) error
	InspectImage(ctx context.Context, ref string) (dockerapi.ImageInspect, error)
	PullImage(ctx context.Context, ref string) (pullStream, error)
	CopyFromContainer(ctx context.Context, id, path string) (io.ReadCloser, error)
	CopyToContainer(ctx context.Context, id, path string, archive io.Reader) error
	Info(ctx context.Context) (dockerapi.SystemInfo, error)
}

// pullStream yields the progress messages of an image pull.
type pullStream interface {
	Next() (dockerapi.PullMessage, error)
	Close() error
}

// npmAPI is the part of the NPM API client the module uses.
type npmAPI interface {
	Health(ctx context.Context) (npmapi.Health, error)
	CreateAdmin(ctx context.Context, email, password string) error
	Token(ctx context.Context, email, password string) (string, error)
	ProxyHosts(ctx context.Context) ([]npmapi.ProxyHost, error)
	ProxyHost(ctx context.Context, id int) (npmapi.ProxyHost, error)
	CreateProxyHost(ctx context.Context, b npmapi.ProxyHostBody) (npmapi.ProxyHost, error)
	UpdateProxyHost(ctx context.Context, id int, b npmapi.ProxyHostBody) (npmapi.ProxyHost, error)
	DeleteProxyHost(ctx context.Context, id int) error
	AccessLists(ctx context.Context) ([]npmapi.AccessList, error)
	AccessList(ctx context.Context, id int) (npmapi.AccessList, error)
	CreateAccessList(ctx context.Context, b npmapi.AccessListBody) (npmapi.AccessList, error)
	DeleteAccessList(ctx context.Context, id int) error
	Certificates(ctx context.Context) ([]npmapi.Certificate, error)
	Certificate(ctx context.Context, id int) (npmapi.Certificate, error)
	CreateCertificate(ctx context.Context, b npmapi.CertificateBody) (npmapi.Certificate, error)
	DeleteCertificate(ctx context.Context, id int) error
}

// dockerClient adapts the Engine client to dockerAPI.
type dockerClient struct{ *dockerapi.Client }

// PullImage starts a pull; a failure returns a nil interface, not the client's nil pointer inside one.
func (c dockerClient) PullImage(ctx context.Context, ref string) (pullStream, error) {
	s, err := c.Client.PullImage(ctx, ref)
	if err != nil {
		return nil, err
	}
	return s, nil
}

// Module returns the Nginx Proxy Manager module.
func Module() core.Module { return module{n: productionNM()} }

// productionNM wires the module to the host's Docker daemon, its NPM instance and its file system.
func productionNM() nm {
	socket, base := dockerapi.DefaultSocket, apiURL
	if testing.Testing() {
		socket, base = absentSocket, absentURL
	}
	docker := dockerClient{dockerapi.New(socket)}
	// A certificate request waits for NPM's ACME order, hence the long timeout. NPM's admin port answers a bare /api
	// with a redirect, which the client must see as an answer rather than follow.
	hc := &http.Client{
		Timeout:       15 * time.Minute,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	newClient := func() *npmapi.Client { return npmapi.New(base, hc) }
	return nm{d: deps{
		docker:   docker,
		api:      apiVia(newClient),
		anon:     func() npmAPI { return newClient() },
		info:     docker.Info,
		raw:      rawVia(loaded.Reader{}.ReadVariant, docker),
		random:   randomBytes,
		readFile: os.ReadFile,
		readDir:  os.ReadDir,
		panel:    reservedVia(docker),
		sockets:  netstat.Sockets,
	}}
}

// randomBytes returns n bytes from crypto/rand.
func randomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("npm: read random bytes: %w", err)
	}
	return b, nil
}

// apiVia builds deps.api: a client of newClient carrying a token for the stored administrator. No instance is a
// Definite not_found; a password that does not open, a refused login or an unreachable NPM is npm.unhealthy. No error
// carries the password.
func apiVia(newClient func() *npmapi.Client) func(ctx context.Context, env core.Env) (npmAPI, error) {
	return func(ctx context.Context, env core.Env) (npmAPI, error) {
		row, ok, err := loadInstance(ctx, env.DB.Read())
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, core.Definite(errcode.New(errcode.NotFound, "no Nginx Proxy Manager instance is deployed on this host"))
		}
		password, err := env.Sealer.Open("npm_instances.password_ct.1", row.PasswordCT)
		if err != nil {
			return nil, errcode.Wrap(errcode.NPMUnhealthy, err, "the stored administrator password cannot be opened")
		}
		c := newClient()
		token, err := c.Token(ctx, row.AdminEmail, string(password))
		if err != nil {
			var answer *npmapi.APIError
			if errors.As(err, &answer) {
				if npmapi.IsClientError(err) {
					return nil, errcode.Wrap(errcode.NPMUnhealthy, err, fmt.Sprintf("NPM refused the stored administrator's login (HTTP %d)", answer.Status))
				}
				return nil, errcode.Wrap(errcode.NPMUnhealthy, err, fmt.Sprintf("Nginx Proxy Manager failed while logging in the stored administrator (HTTP %d)", answer.Status))
			}
			return nil, errcode.Wrap(errcode.NPMUnhealthy, err, "the Nginx Proxy Manager API is not reachable")
		}
		return c.WithToken(token), nil
	}
}

// variantReader reads tables of one family as one iptables variant holds them; loaded.Reader.ReadVariant is one.
type variantReader func(ctx context.Context, env core.Env, f fwstate.Family, variant string, tables []string) (*netfilter.Ruleset, error)

// rawVia builds deps.raw: whether the IPv4 raw table holds the protection Docker 28 and later loads for the running
// containers. It is false unless exactly one iptables variant holds Docker's chains, and false when that variant has no
// raw table.
func rawVia(read variantReader, docker dockerAPI) func(ctx context.Context, env core.Env) (bool, error) {
	return func(ctx context.Context, env core.Env) (bool, error) {
		var holding []string
		for _, variant := range []string{"nft", "legacy"} {
			rs, err := read(ctx, env, fwstate.V4, variant, []string{"filter"})
			if err != nil {
				return false, fmt.Errorf("npm: read the %s filter table: %w", variant, err)
			}
			if rs != nil && dockerfw.DockerChains(rs.Table("filter")) {
				holding = append(holding, variant)
			}
		}
		if len(holding) != 1 {
			return false, nil
		}
		facts, internal, err := dockerFacts(ctx, docker)
		if err != nil {
			return false, fmt.Errorf("npm: read the Docker state: %w", engineCoded(err))
		}
		rs, err := read(ctx, env, fwstate.V4, holding[0], []string{"raw"})
		if err != nil {
			return false, fmt.Errorf("npm: read the %s raw table: %w", holding[0], err)
		}
		if rs == nil {
			return false, nil
		}
		raw := rs.Table("raw")
		if raw == nil {
			return false, nil
		}
		return dockerfw.RawProtected(raw, withoutInternal(facts, internal)), nil
	}
}

// dockerFacts reads the Engine's networks and running containers as dockerfw's facts, as the firewall module does.
// internal names the networks Docker reports as internal.
func dockerFacts(ctx context.Context, docker dockerAPI) (facts dockerfw.Facts, internal map[string]bool, err error) {
	nets, err := docker.ListNetworks(ctx, nil)
	if err != nil {
		return dockerfw.Facts{}, nil, err
	}
	internal = map[string]bool{}
	bridges := map[string]dockerfw.Network{}
	for _, n := range nets {
		fn := dockerfw.Network{ID: n.ID, Name: n.Name, Driver: n.Driver, IPv6: n.EnableIPv6,
			GatewayModeV4: n.Options[gatewayV4], GatewayModeV6: n.Options[gatewayV6]}
		for _, c := range n.IPAM.Config {
			if c.Subnet.IsValid() {
				fn.Subnets = append(fn.Subnets, c.Subnet.Masked())
			}
		}
		if n.Driver == "bridge" {
			fn.Bridge = n.Options[bridgeOption]
			if fn.Bridge == "" {
				fn.Bridge = dockerfw.Bridge(fn)
			}
			bridges[fn.Name] = fn
		}
		if n.Internal {
			internal[n.Name] = true
		}
		facts.Networks = append(facts.Networks, fn)
	}
	slices.SortFunc(facts.Networks, func(a, b dockerfw.Network) int { return strings.Compare(a.Name, b.Name) })

	list, err := docker.ListContainers(ctx, dockerapi.ListContainersOptions{Filters: dockerapi.Filters{"status": {"running"}}})
	if err != nil {
		return dockerfw.Facts{}, nil, err
	}
	for _, s := range list {
		c, err := docker.InspectContainer(ctx, s.ID)
		if err != nil {
			return dockerfw.Facts{}, nil, err
		}
		if c.State.Running {
			facts.Containers = append(facts.Containers, containerFact(c, bridges))
		}
	}
	slices.SortFunc(facts.Containers, func(a, b dockerfw.Container) int { return strings.Compare(a.Name, b.Name) })
	return facts, internal, nil
}

// containerFact is a running container's published ports and its endpoints on bridge networks, each sorted.
func containerFact(c dockerapi.ContainerJSON, bridges map[string]dockerfw.Network) dockerfw.Container {
	fc := dockerfw.Container{ID: c.ID, Name: strings.TrimPrefix(c.Name, "/")}
	for key, bindings := range c.NetworkSettings.Ports {
		port, proto, ok := strings.Cut(key, "/")
		cport, err := strconv.ParseUint(port, 10, 16)
		if !ok || err != nil || (proto != "tcp" && proto != "udp") {
			continue
		}
		for _, b := range bindings {
			// Port 0 is no port a client can reach, so Docker loads no rule for it.
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
			fc.Ports = append(fc.Ports, dockerfw.Published{HostAddr: addr, HostPort: uint16(hport), ContainerPort: uint16(cport), Proto: proto}) //nolint:gosec // G115: ParseUint bounded both to 16 bits.
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

// withoutInternal copies facts without the endpoints on internal networks: Docker's bridge driver loads no raw drop
// rule for them. The copy shares no endpoint slice with facts.
func withoutInternal(facts dockerfw.Facts, internal map[string]bool) dockerfw.Facts {
	out := dockerfw.Facts{Networks: facts.Networks, Containers: make([]dockerfw.Container, len(facts.Containers))}
	for i, c := range facts.Containers {
		c.Endpoints = slices.DeleteFunc(slices.Clone(c.Endpoints), func(e dockerfw.Endpoint) bool { return internal[e.Network] })
		out.Containers[i] = c
	}
	return out
}

// engineCoded maps an unreachable Engine to docker.engine_unavailable, keeping err as the cause, and returns any other
// error unchanged.
func engineCoded(err error) error {
	if errors.Is(err, dockerapi.ErrUnavailable) {
		return errcode.Wrap(errcode.DockerEngineUnavailable, err, "the Docker Engine is not reachable")
	}
	return err
}

// reservedVia builds deps.panel: the NPM container's endpoint addresses, sorted and unmapped (none while the container
// does not exist), and the first IPv4 subnet and gateway of the panel network (none while it does not exist).
func reservedVia(docker dockerAPI) func(ctx context.Context, env core.Env) (proxy.Reserved, error) {
	return func(ctx context.Context, _ core.Env) (proxy.Reserved, error) {
		var r proxy.Reserved
		c, err := docker.InspectContainer(ctx, containerName)
		switch {
		case dockerapi.IsNotFound(err):
		case err != nil:
			return proxy.Reserved{}, fmt.Errorf("npm: inspect container %s: %w", containerName, engineCoded(err))
		default:
			for _, ep := range c.NetworkSettings.Networks {
				if ep == nil {
					continue
				}
				for _, a := range []netip.Addr{ep.IPAddress, ep.GlobalIPv6Address} {
					if a.IsValid() {
						r.NPMAddrs = append(r.NPMAddrs, a.Unmap())
					}
				}
			}
			slices.SortFunc(r.NPMAddrs, netip.Addr.Compare)
			r.NPMAddrs = slices.Compact(r.NPMAddrs)
		}
		n, err := docker.InspectNetwork(ctx, reservedPanelNetwork)
		switch {
		case dockerapi.IsNotFound(err):
		case err != nil:
			return proxy.Reserved{}, fmt.Errorf("npm: inspect network %s: %w", reservedPanelNetwork, engineCoded(err))
		default:
			for _, cfg := range n.IPAM.Config {
				if cfg.Subnet.IsValid() && cfg.Subnet.Addr().Is4() {
					r.PanelNetwork, r.PanelGateway = cfg.Subnet.Masked(), cfg.Gateway
					break
				}
			}
		}
		return r, nil
	}
}

// ownedPaths is what the module creates on the host: the managed directory of the upgrade's volume archives.
func ownedPaths() []core.OwnedPath {
	return []core.OwnedPath{{Path: backupDir, Kind: core.OwnedManagedDir, Pattern: `^[a-z0-9][a-z0-9._-]{0,63}\.tar$`}}
}

// archiveName is the managed-file name of a volume's archive taken by the upgrade change: the lowercased change id
// and the volume name without its darkpanel-npm- prefix.
func archiveName(changeID, volume string) string {
	return strings.ToLower(changeID) + "-" + strings.TrimPrefix(volume, "darkpanel-npm-") + ".tar"
}

// instanceCreate is the NPM container's configuration (spec 6): the image, the module label, the data and
// certificate volumes, ports 80 and 443 published on every address and 81 on the loopback address only, and the
// darkpanel-npm network. It has no environment, command or entrypoint, so no secret is in the container's
// configuration.
func instanceCreate(image string) dockerapi.ContainerCreate {
	return dockerapi.ContainerCreate{
		Name: containerName,
		ContainerConfig: dockerapi.ContainerConfig{
			Image:        image,
			Labels:       map[string]string{core.LabelModule: ID},
			ExposedPorts: map[string]struct{}{"80/tcp": {}, "81/tcp": {}, "443/tcp": {}},
		},
		HostConfig: &dockerapi.HostConfig{
			NetworkMode: networkName,
			PortBindings: map[string][]dockerapi.PortBinding{
				"80/tcp":  {{HostPort: "80"}},
				"81/tcp":  {{HostIP: "127.0.0.1", HostPort: "81"}},
				"443/tcp": {{HostPort: "443"}},
			},
			RestartPolicy: dockerapi.RestartPolicy{Name: "unless-stopped"},
			Mounts: []dockerapi.Mount{
				{Type: "volume", Source: dataVolume, Target: "/data"},
				{Type: "volume", Source: certVolume, Target: "/etc/letsencrypt"},
			},
		},
		NetworkingConfig: &dockerapi.NetworkingConfig{EndpointsConfig: map[string]*dockerapi.EndpointSettings{networkName: {}}},
	}
}
