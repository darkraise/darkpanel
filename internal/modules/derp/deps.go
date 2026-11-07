package derp

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/user"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/agent/daemon"
	"github.com/darkraise/darkpanel/internal/buildinfo"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/modules/derp/artifact/download"
	"github.com/darkraise/darkpanel/internal/modules/derp/server"
	"github.com/darkraise/darkpanel/internal/modules/tailnet"
	"github.com/darkraise/darkpanel/internal/modules/tailscale/link"
	"github.com/darkraise/darkpanel/internal/modules/tailscale/stun"
	"github.com/darkraise/darkpanel/internal/paths"
	"github.com/darkraise/darkpanel/internal/platform"
	"github.com/darkraise/darkpanel/internal/platform/netstat"
	"github.com/darkraise/darkpanel/internal/platform/tslocal"
	"github.com/darkraise/darkpanel/internal/update"
	"github.com/darkraise/darkpanel/internal/update/trustedkeys"
)

const (
	// lockKey is the lock every mutating agent-side derp action takes.
	lockKey = "derp"
	// healthWindow is how long a binary update waits for the new derper to pass its health check.
	healthWindow = 60 * time.Second
	// certWait is how long an install or a certificate renewal waits for derper's certificate files.
	certWait = 30 * time.Second
	// availableWait bounds the availability check's call to tailscaled.
	availableWait = 5 * time.Second
	// servicePeriod is the interval of the derp.servers service's passes.
	servicePeriod = 60 * time.Second
	// staleAfter is the age past which a server record is degraded.
	staleAfter = 10 * time.Minute
	// expiryWarning is how long before its certificate expires a server is degraded.
	expiryWarning = 30 * 24 * time.Hour
	// maxProbeAgents is the most agents derp.server.probe asks to probe a server.
	maxProbeAgents = 8
	// certFileLimit is the most bytes a certificate or key file of derper's may hold.
	certFileLimit = 1 << 20
)

// dp carries the module's host dependencies; tests replace them.
type dp struct{ d deps }

// deps are the host functions the module reaches through.
type deps struct {
	local          func() localAPI                                                                 // production: tslocal client on tslocal.DefaultSocket
	fetch          func(ctx context.Context, dpVersion, want, arch string) ([]byte, string, error) // production: download.Fetch with the production Source (30 s index client, 5 min download client)
	version        string                                                                          // production: buildinfo.Version
	arch           string                                                                          // production: runtime.GOARCH
	readFile       func(name string) ([]byte, error)                                               // production: os.ReadFile (the binary, the unit link and existence checks)
	readCertFile   func(name string) ([]byte, error)                                               // production: platform.ReadRegularFile with certFileLimit (certificate and key files under CertDir)
	lookupUser     func(name string) (uid, gid int, ok bool, err error)                            // production: os/user.Lookup; an unknown user is ok false, nil error
	sockets        func() ([]netstat.Socket, error)                                                // production: netstat.Sockets
	linkViaTailnet func() (bool, error)                                                            // production: the saved controller address is an IP literal inside a Tailscale range; not enrolled: false
	tlsProbe       func(ctx context.Context, t probeTarget) (time.Duration, error)                 // production: probeTLS
	stun           func(ctx context.Context, server netip.AddrPort) (time.Duration, error)         // production: stun.Probe
	resolve        func(ctx context.Context, host string) ([]netip.Addr, error)                    // production: net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	policy         func(ctx context.Context, env core.Env) (tailnet.Policy, error)                 // production: tailnet.ReadPolicy
	devices        func(ctx context.Context, env core.Env) ([]tailnet.Device, error)               // production: tailnet.ListDevices
	connected      func(ctx context.Context, db *store.DB) (bool, error)                           // production: tailnet.Connected
}

// localAPI is the part of tailscaled's local API the module uses.
type localAPI interface {
	Status(ctx context.Context) (*tslocal.Status, error)
	WhoIs(ctx context.Context, addr netip.Addr) (*tslocal.WhoIs, error)
	DebugDERPRegion(ctx context.Context, region string) (*tslocal.DERPRegionReport, error)
}

// probeTarget is one DERP server as a prober reaches it.
type probeTarget struct {
	HostName string // the server's -hostname: a DNS name or, in ip mode, the IP literal
	Address  string // the IP literal to connect to; "" only when HostName is itself the IP literal
	Port     int    // 443
	CertName string // ip mode: the pin (PinPrefix...); "" verifies the chain against the system roots for HostName
}

// absentSocket is the socket a test binary dials instead of the host's. Test
// binaries that link the module, such as the controller's server tests, open a
// runtime that runs the module's checks, and they must never reach the host's
// tailscaled. A path that never exists makes every call fail as a missing
// socket.
const absentSocket = "/nonexistent/darkpanel/tailscaled.sock"

// productionDP wires the module to the host's tailscaled, network, file system and release server.
func productionDP() dp {
	socket := tslocal.DefaultSocket
	if testing.Testing() {
		socket = absentSocket
	}
	client := tslocal.New(socket)
	index := update.NewIndexSource(trustedkeys.BaseURL(), &http.Client{Timeout: 30 * time.Second}, core.SystemClock{})
	src := download.Source{
		Client:   &http.Client{Timeout: 5 * time.Minute},
		BaseURL:  trustedkeys.BaseURL(),
		Keys:     trustedkeys.Keys(),
		Releases: index.Releases,
	}
	return dp{d: deps{
		local: func() localAPI { return client },
		fetch: func(ctx context.Context, dpVersion, want, arch string) ([]byte, string, error) {
			return download.Fetch(ctx, src, dpVersion, want, arch)
		},
		version:        buildinfo.Version,
		arch:           runtime.GOARCH,
		readFile:       os.ReadFile,
		readCertFile:   func(name string) ([]byte, error) { return platform.ReadRegularFile(name, certFileLimit) },
		lookupUser:     lookupSystemUser,
		sockets:        netstat.Sockets,
		linkViaTailnet: func() (bool, error) { return linkViaTailnetIn(paths.Production().AgentTLSDir) },
		tlsProbe:       probeTLS,
		stun:           stun.Probe,
		resolve: func(ctx context.Context, host string) ([]netip.Addr, error) {
			return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		},
		policy:    tailnet.ReadPolicy,
		devices:   tailnet.ListDevices,
		connected: tailnet.Connected,
	}}
}

// lookupSystemUser returns the uid and gid of a user; ok is false when there is no such user.
func lookupSystemUser(name string) (uid, gid int, ok bool, err error) {
	u, err := user.Lookup(name)
	var unknown user.UnknownUserError
	if errors.As(err, &unknown) {
		return 0, 0, false, nil
	}
	if err != nil {
		return 0, 0, false, fmt.Errorf("derp: look up user %s: %w", name, err)
	}
	if uid, err = strconv.Atoi(u.Uid); err != nil {
		return 0, 0, false, fmt.Errorf("derp: user %s has uid %q: %w", name, u.Uid, err)
	}
	if gid, err = strconv.Atoi(u.Gid); err != nil {
		return 0, 0, false, fmt.Errorf("derp: user %s has gid %q: %w", name, u.Gid, err)
	}
	return uid, gid, true, nil
}

// linkViaTailnetIn reports whether the controller address saved in the agent credentials of dir is an IP literal
// inside a Tailscale range; an agent that is not enrolled, or that dials its controller by name, answers false.
func linkViaTailnetIn(dir string) (bool, error) {
	c, err := daemon.LoadCredentials(dir)
	if errors.Is(err, daemon.ErrNotEnrolled) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("derp: read the agent credentials: %w", err)
	}
	host, _, err := net.SplitHostPort(c.Controller.Address)
	if err != nil {
		return false, nil
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return false, nil
	}
	return link.IsTailscaleAddr(addr), nil
}

// probeTLS GETs https://<Address or HostName>:<Port>/derp/probe once with a 5 s timeout and no redirects; the TLS
// ServerName is HostName unless it is an IP literal; with a CertName it checks only the leaf, the first certificate
// served, which must have the pinned SHA-256 and NotBefore/NotAfter that contain now; the certificates after it
// (derper appends its meta certificate) are ignored; else it verifies the chain. Any answer but 200 is an error.
// It connects only to an IP literal and never resolves a name: the caller checked the address it resolved, and a
// second lookup could answer another one.
func probeTLS(ctx context.Context, t probeTarget) (time.Duration, error) {
	fail := func(err error) (time.Duration, error) { return 0, fmt.Errorf("derp: probe %s: %w", t.HostName, err) }
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if _, err := netip.ParseAddr(t.HostName); err != nil {
		cfg.ServerName = t.HostName
	}
	if t.CertName != "" {
		cfg.InsecureSkipVerify = true //nolint:gosec // G402: the leaf is checked against its pin in VerifyPeerCertificate
		// No ClientSessionCache is set, so no session resumes past this check.
		cfg.VerifyPeerCertificate = pinnedLeaf(t.CertName) //nolint:gosec // G123: no session is resumed
	}
	transport := &http.Transport{TLSClientConfig: cfg}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport:     transport,
		Timeout:       5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	host := t.Address
	if host == "" {
		host = t.HostName
	}
	if _, err := netip.ParseAddr(host); err != nil {
		return fail(errors.New("no IP address to connect to"))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+net.JoinHostPort(host, strconv.Itoa(t.Port))+"/derp/probe", nil)
	if err != nil {
		return fail(err)
	}
	start := time.Now()
	res, err := client.Do(req)
	if err != nil {
		return fail(err)
	}
	elapsed := time.Since(start)
	defer func() { _ = res.Body.Close() }()
	_, err = io.Copy(io.Discard, io.LimitReader(res.Body, 64<<10))
	if res.StatusCode != http.StatusOK {
		return fail(fmt.Errorf("status %d", res.StatusCode))
	}
	if err != nil {
		return fail(err)
	}
	return elapsed, nil
}

// pinnedLeaf is a VerifyPeerCertificate that accepts the served chain when its leaf, the first certificate, has
// pin and is valid now; the certificates after the leaf are not looked at.
func pinnedLeaf(pin string) func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
	return func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
		if len(rawCerts) == 0 {
			return errors.New("the server sent no certificate")
		}
		leaf, err := x509.ParseCertificate(rawCerts[0])
		if err != nil {
			return fmt.Errorf("parse the leaf certificate: %w", err)
		}
		if got := server.Pin(leaf.Raw); got != pin {
			return fmt.Errorf("the leaf certificate is %s, not the pinned %s", got, pin)
		}
		if now := time.Now(); now.Before(leaf.NotBefore) || now.After(leaf.NotAfter) {
			return fmt.Errorf("the leaf certificate is valid from %s to %s, not now", leaf.NotBefore.UTC().Format(time.RFC3339),
				leaf.NotAfter.UTC().Format(time.RFC3339))
		}
		return nil
	}
}

// available: nil when tailscaled answers Status within availableWait with BackendState "Running"; no answer:
// errcode tailscale.unavailable "tailscaled does not answer on its socket"; another state: errcode derp.not_ready
// "the host is not on the tailnet (tailscaled is <state>)".
func (d dp) available(ctx context.Context, env core.Env) error {
	clock := env.Clock
	if clock == nil {
		clock = core.SystemClock{}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	timer := clock.AfterFunc(availableWait, cancel)
	defer timer.Stop()
	st, err := d.d.local().Status(ctx)
	if err != nil {
		return errcode.Wrap(errcode.TailscaleUnavailable, err, "tailscaled does not answer on its socket")
	}
	if st.BackendState != "Running" {
		return errcode.New(errcode.DERPNotReady, fmt.Sprintf("the host is not on the tailnet (tailscaled is %s)", st.BackendState))
	}
	return nil
}

// ownedPaths: {ServiceUser, user}, {BinaryPath, file}, {BinaryNewPath, file}, {BinaryPrevPath, file},
// {UnitPath, unit}, {StateDir, service-dir, User ServiceUser, Subdirs [CertSubdir]} (the default name pattern).
func ownedPaths() []core.OwnedPath {
	return []core.OwnedPath{
		{Path: server.ServiceUser, Kind: core.OwnedUser},
		{Path: server.BinaryPath, Kind: core.OwnedFile},
		{Path: server.BinaryNewPath, Kind: core.OwnedFile},
		{Path: server.BinaryPrevPath, Kind: core.OwnedFile},
		{Path: server.UnitPath, Kind: core.OwnedUnit},
		{Path: server.StateDir, Kind: core.OwnedServiceDir, User: server.ServiceUser, Subdirs: []string{server.CertSubdir}},
	}
}
