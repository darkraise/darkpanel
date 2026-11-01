package tailscale

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/tailscale/stun"
	"github.com/darkraise/darkpanel/internal/platform"
	"github.com/darkraise/darkpanel/internal/platform/netstat"
	"github.com/darkraise/darkpanel/internal/platform/tslocal"
)

const (
	// lockKey is the lock every mutating tailscale action takes.
	lockKey = "tailscale"
	// tailscaledUnit is the Tailscale daemon's unit, which the module drives but does not own.
	tailscaledUnit = "tailscaled.service"
	// controllerUnit is the controller's unit, whose state tells whether a local controller runs.
	controllerUnit = "darkpanel-controller.service"
	// keyringPath is Tailscale's apt signing keyring.
	keyringPath = "/usr/share/keyrings/tailscale-archive-keyring.gpg"
	// sourcesPath is Tailscale's apt source list.
	sourcesPath = "/etc/apt/sources.list.d/tailscale.list"
	// debDir is the managed directory of staged packages.
	debDir = platform.TailscaleDebDir
	// pinCheckWindow is how long a pin waits for the node to come back on the new version.
	pinCheckWindow = 120 * time.Second
	// joinWait is how long a login waits for the node to reach the tailnet.
	joinWait = 60 * time.Second
)

// availableTimeout bounds the availability check's call to tailscaled.
const availableTimeout = 5 * time.Second

// ts carries the module's host dependencies; tests replace them.
type ts struct{ d deps }

// deps are the host functions the module reaches through.
type deps struct {
	local     func() localAPI                                                         // production: a tslocal.Client on tslocal.DefaultSocket
	link      linkHost                                                                // production: liveLinkHost{} over internal/platform/netstat
	stun      func(ctx context.Context, server netip.AddrPort) (time.Duration, error) // production: stun.Probe
	https     func(ctx context.Context, url string) (time.Duration, error)            // production: one GET with a 5 s timeout, TLS verified, body discarded
	osRelease func() (osInfo, error)                                                  // production: parse /etc/os-release
	arch      string                                                                  // production: runtime.GOARCH
	hostname  func() (string, error)                                                  // production: os.Hostname
	readFile  func(name string) ([]byte, error)                                       // production: os.ReadFile; /var/lib/dpkg/status, /etc/os-release, /var/cache/apt/archives/*.deb, staged files
	// session is the agent's controller session as the pin's transient unit sees it, from outside the agent
	// process. production: agentSession.
	session func(ctx context.Context) (up, viaTailnet bool, err error)
}

// localAPI is the part of tailscaled's local API the module uses.
type localAPI interface {
	Status(ctx context.Context) (*tslocal.Status, error)
	Prefs(ctx context.Context) (*tslocal.Prefs, error)
	EditPrefs(ctx context.Context, p tslocal.PrefsPatch) (*tslocal.Prefs, error)
	Start(ctx context.Context, o tslocal.StartOptions) error
	// LoginInteractive is POST /localapi/v0/login-interactive; 204 is success, any other status an *tslocal.APIError.
	// A login by key calls it right after a successful Start, as tailscale up --auth-key does: Start alone does not
	// log in a node without a node key (spec 1.8, Login by key).
	LoginInteractive(ctx context.Context) error
	Logout(ctx context.Context) error
	DERPMap(ctx context.Context) (*tslocal.DERPMap, error)
	CertPair(ctx context.Context, domain string) (certPEM, keyPEM []byte, err error)
}

// linkHost is the host's socket table, interfaces and routing table, and this process's id.
type linkHost interface {
	Sockets() ([]netstat.Socket, error)
	Interfaces() ([]netstat.Interface, error)
	RouteGet(q netstat.RouteQuery) (netstat.Route, error)
	PID() int
}

// osInfo is the distribution and release of /etc/os-release.
type osInfo struct{ ID, Codename string }

// liveLinkHost is the host's own link state.
type liveLinkHost struct{}

func (liveLinkHost) Sockets() ([]netstat.Socket, error)       { return netstat.Sockets() }
func (liveLinkHost) Interfaces() ([]netstat.Interface, error) { return netstat.Interfaces() }
func (liveLinkHost) RouteGet(q netstat.RouteQuery) (netstat.Route, error) {
	return netstat.RouteGet(q)
}
func (liveLinkHost) PID() int { return os.Getpid() }

// absentSocket is the socket a test binary dials instead of the host's. Test
// binaries that link the module, such as the controller's server tests, open a
// runtime that runs the module's checks, and they must never reach the host's
// tailscaled. A path that never exists makes every call fail as a missing
// socket.
const absentSocket = "/nonexistent/darkpanel/tailscaled.sock"

// productionTS wires the module to the host's tailscaled, network and file system.
func productionTS() ts {
	socket := tslocal.DefaultSocket
	if testing.Testing() {
		socket = absentSocket
	}
	client := tslocal.New(socket)
	return ts{d: deps{
		local:     func() localAPI { return client },
		link:      liveLinkHost{},
		stun:      stun.Probe,
		https:     httpsProbe(&http.Client{Timeout: 5 * time.Second}),
		osRelease: func() (osInfo, error) { return readOSRelease(os.ReadFile) },
		arch:      runtime.GOARCH,
		hostname:  os.Hostname,
		readFile:  os.ReadFile,
		session:   agentSession,
	}}
}

// httpsProbe returns a probe that sends one GET to a URL through a copy of c
// that does not follow redirects, and answers the round trip's wall time.
func httpsProbe(c *http.Client) func(ctx context.Context, url string) (time.Duration, error) {
	client := *c
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return func(ctx context.Context, url string) (time.Duration, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return 0, fmt.Errorf("tailscale: probe %s: %w", url, err)
		}
		start := time.Now()
		res, err := client.Do(req)
		if err != nil {
			return 0, fmt.Errorf("tailscale: probe %s: %w", url, err)
		}
		defer func() { _ = res.Body.Close() }()
		if _, err := io.Copy(io.Discard, res.Body); err != nil {
			return 0, fmt.Errorf("tailscale: probe %s: %w", url, err)
		}
		elapsed := time.Since(start)
		if res.StatusCode < 200 || res.StatusCode > 299 {
			return 0, fmt.Errorf("tailscale: probe %s: status %d", url, res.StatusCode)
		}
		return elapsed, nil
	}
}

// readOSRelease reads the distribution id and release codename of /etc/os-release.
func readOSRelease(read func(name string) ([]byte, error)) (osInfo, error) {
	b, err := read("/etc/os-release")
	if err != nil {
		return osInfo{}, fmt.Errorf("tailscale: read the operating system release: %w", err)
	}
	var info osInfo
	for line := range strings.Lines(string(b)) {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
			value = value[1 : len(value)-1]
		}
		switch strings.TrimSpace(key) {
		case "ID":
			info.ID = value
		case "VERSION_CODENAME":
			info.Codename = value
		}
	}
	return info, nil
}

// available is the per-action availability: nil when tailscaled answers Status within 5 s, else
// errcode tailscale.unavailable with the reason "tailscaled does not answer on its socket".
func (t ts) available(ctx context.Context, env core.Env) error {
	clock := env.Clock
	if clock == nil {
		clock = core.SystemClock{}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	timer := clock.AfterFunc(availableTimeout, cancel)
	defer timer.Stop()
	if _, err := t.d.local().Status(ctx); err != nil {
		return errcode.Wrap(errcode.TailscaleUnavailable, err, "tailscaled does not answer on its socket")
	}
	return nil
}

// ownedPaths are the files and directory the module writes through FileWriter.
func ownedPaths() []core.OwnedPath {
	return []core.OwnedPath{
		{Path: keyringPath, Kind: core.OwnedFile},
		{Path: sourcesPath, Kind: core.OwnedFile},
		{Path: debDir, Kind: core.OwnedManagedDir, Pattern: `^tailscale_[0-9][0-9A-Za-z.+~-]{0,63}_(amd64|arm64)\.deb$`},
	}
}

// externalUnits are the units the module drives without owning them.
func externalUnits() []core.ExternalUnit {
	return []core.ExternalUnit{
		{Name: tailscaledUnit, Ops: []string{core.UnitOpState, core.UnitOpRestart}},
		{Name: controllerUnit, Ops: []string{core.UnitOpState}},
	}
}

// run runs one allowlisted vector through env.Exec under ctx and turns a non-zero exit into an error naming
// path, the first argument and the last line of stderr.
func run(ctx context.Context, env core.Env, path string, args ...string) (core.CmdResult, error) {
	res, err := env.Exec.Run(ctx, core.Cmd{Path: path, Args: args})
	if err != nil {
		return res, fmt.Errorf("tailscale: run %s: %w", path, err)
	}
	if res.ExitCode == 0 {
		return res, nil
	}
	// Only the first argument is named: later ones can carry secrets.
	what := path
	if len(args) > 0 {
		what += " " + args[0]
	}
	if last := lastLine(res.Stderr); last != "" {
		return res, fmt.Errorf("tailscale: %s exited %d: %s", what, res.ExitCode, last)
	}
	return res, fmt.Errorf("tailscale: %s exited %d", what, res.ExitCode)
}

// lastLine is the last non-empty line of b, trimmed.
func lastLine(b []byte) string {
	lines := bytes.Split(b, []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(string(lines[i])); l != "" {
			return l
		}
	}
	return ""
}

// held reports whether tailscale is held: `apt-mark showhold` lists it.
func (t ts) held(ctx context.Context, env core.Env) (bool, error) {
	res, err := run(ctx, env, "/usr/bin/apt-mark", "showhold")
	if err != nil {
		return false, err
	}
	for line := range strings.Lines(string(res.Stdout)) {
		if strings.TrimSpace(line) == "tailscale" {
			return true, nil
		}
	}
	return false, nil
}

// installedVersion reads the installed tailscale version from dpkg's status file /var/lib/dpkg/status through
// t.d.readFile (the stanza "Package: tailscale" with "Status: install ok installed"); "" when not installed.
// FileWriter has no read method and dpkg-query is not allowlisted.
func (t ts) installedVersion(_ context.Context, _ core.Env) (string, error) {
	b, err := t.d.readFile("/var/lib/dpkg/status")
	if err != nil {
		return "", fmt.Errorf("tailscale: read dpkg status: %w", err)
	}
	fields := map[string]string{}
	installed := func() bool {
		status := strings.Fields(fields["Status"])
		return fields["Package"] == "tailscale" && len(status) == 3 && status[2] == "installed"
	}
	for line := range strings.Lines(string(b)) {
		switch {
		case strings.TrimSpace(line) == "":
			if installed() {
				return fields["Version"], nil
			}
			clear(fields)
		case strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t"):
		default:
			if key, value, ok := strings.Cut(line, ":"); ok {
				fields[key] = strings.TrimSpace(value)
			}
		}
	}
	if installed() {
		return fields["Version"], nil
	}
	return "", nil
}
