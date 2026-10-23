# v1 Wave 0b: Host Clients Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: the skill the **Execution:** line names — dforge:subagent-driven-development for `subagent`, dforge:executing-plans for `inline`. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the four host clients of spec 1.8 that wave 0 left to wave 0b (Parallel decomposition parts 0b-1 to 0b-4, register row 79): the Docker Engine API client `internal/platform/dockerapi`, the Tailscale local API client `internal/platform/tslocal`, the socket table and routes reader `internal/platform/netstat`, and the netfilter save parser `internal/platform/netfilter`. (This is spec wave 0b; the earlier plan `2026-10-22-v1-w0b-platform-surfaces.md` was wave 0's second sub-plan, which shares the "w0b" label.)

**Architecture:** Four new, independent packages under `internal/platform`, each built foundation first. `dockerapi` and `tslocal` are hand-written HTTP clients over Unix sockets: a core file holds the transport, error types and the shared test fake (Tasks 1 and 2), and each later file adds one group of typed calls. `netstat` reads `/proc/net` and `/proc/<pid>/fd` for sockets (Task 3) and speaks rtnetlink through `golang.org/x/sys/unix` for routes (Task 8). `netfilter` tokenizes and structures `*-save` output (Task 4), then classifies every match and target against the contract so anything else becomes an opaque rule (Task 9). Two opt-in live tests (Tasks 12 and 13) prove the wire shapes against a real tailscaled and the host's Docker daemon. Nothing consumes these packages yet: the firewall (wave 1), the Docker module (3a) and the Tailscale module (4) will.

**Tech Stack:** Go 1.27 (`GOTOOLCHAIN=local`), the standard library (`net/http` over Unix sockets, `encoding/json`, `net/netip`, `bufio`), `golang.org/x/sys/unix` (already required at v0.49.0); no new dependency.

**Spec:** docs/dforge/specs/2026-10-21-v1-completion-design.md

**Execution:** subagent — `claude --model sonnet --effort high` — the plan runs parallel waves, and each task's **Implementer:** line chooses its implementer

**Parallelism:** waves — 1-4 | 5-9 | 10-12 | 13

**Integration check:** `mkdir -p /root/.cache/w0b2-tmp/integration && export TMPDIR=/root/.cache/w0b2-tmp/integration GOTOOLCHAIN=local && go build ./... && go test -count=1 ./... && go vet ./... && golangci-lint run --allow-serial-runners && go mod tidy -diff`

**Worktree setup:** none

**Plan review:** 2026-10-23 — dforge:judge-opus — executability 18 / coherence 19 / coverage 15 / assumptions 16 (round 1)

## Global Constraints

- Scope is spec revision 8 as amended on `main`, section 1.8's four bullets "Docker Engine client", "Tailscale local client", "Socket table and routes" and "Netfilter save parser", with the versions of the spec's Versions table, and register row 79 of `docs/dforge/registers/2026-10-21-v1-completion.md`. Later work, which this plan does not build:
  - every consumer of these packages: the firewall evaluator and its golden files (wave 1, spec 2.3), the Docker module (3a), the Tailscale and DERP modules (4, 5);
  - running the live tests in the harness: `test/harness/**` and `.github/workflows/harness.yml` are not this wave's files (Parallel decomposition). Register row 251 gives the tailscaled run to wave 4's local-API spike, with `CertPair` (wave 4) and `DebugDERPRegion` (wave 5's spike), which need a tailnet; row 252 gives the Docker run to wave 3a. Tasks 12 and 13 run both tests by hand here;
  - the fuzz inventory, the fuzz script and its CI job (wave 9a); this plan adds the netfilter fuzz target only;
  - netfilter fixtures of Docker 28, of IPv6 Docker and tailscaled rules, and of tailscaled releases in spec 2.3's 1.82–1.102 range: wave 1's golden files (2.3) capture them, and a construct they show outside the contract is added to Task 9's tables there.
- Build only with the toolchain `go.mod` pins: every `go` command runs with `GOTOOLCHAIN=local` (ADR 0001).
- `/tmp` on this host is an 8 GiB tmpfs, and a Unix socket path longer than 108 bytes fails to bind (`connect: invalid argument`). Every `go` command and `golangci-lint` runs with `TMPDIR=/root/.cache/w0b2-tmp/<task number>`, also where a step's command omits it. Create the directory with `mkdir -p` before the first command and remove it when the task ends.
- No dependency change: `go.mod` and `go.sum` are not edited, and after every task `GOTOOLCHAIN=local go mod tidy -diff` prints nothing. No Docker SDK, no Tailscale library and no netlink library: only the standard library and `golang.org/x/sys/unix`.
- Values copied from the spec, used exactly:
  - Docker Engine API: 1.44 minimum (`MinAPIVersion`); `FirewallBackend` in `/info` from API 1.49 (`MaxAPIVersion`, the highest version the client asks for), an object `{Driver, Info}` of which only `Driver` is read; an absent field means the iptables backend. The daemon socket is `/var/run/docker.sock`.
  - tailscaled 1.82 or later; its socket is `/var/run/tailscale/tailscaled.sock`; `PATCH /localapi/v0/prefs` takes `ipn.MaskedPrefs` (each changed field plus `<Field>Set: true`); `POST /localapi/v0/start` takes `ipn.Options{AuthKey, UpdatePrefs}`.
  - Netfilter tables: `filter`, `nat`, `raw`, `mangle`, `security`.
- Unit tests touch no real Docker daemon or tailscaled: fakes serve `httptest` handlers on Unix sockets under `os.MkdirTemp("", "dp")`. The live tests run only when their variable is set: `DARKPANEL_DOCKER_LIVE=1` (Task 13) and `DARKPANEL_TAILSCALED_SOCKET` (Task 12). `netstat`'s live tests read `/proc` and ask the running kernel about loopback routes; they are read-only and always run. Tasks of one wave therefore share no port, path or global state.
- This host runs an installed `darkpanel-agent` and `darkpanel-controller` whose units are active. No test, step or command starts, stops, restarts, reloads or otherwise touches them, and no command writes under `/var/lib/darkpanel`, `/etc/darkpanel` or `/run/darkpanel`. Every container a step starts is removed in the same task.
- Tests run as root on this host (`id -u` prints 0).
- Tasks that share a Go package declare no top-level identifier, test helpers included, that another task of the plan declares. The shared test helpers are `newFakeDaemon` (Task 1), `newFakeTailscaled` (Task 2) and `psSample` (Task 4); every other test helper or constant carries its task's prefix, which the task names.
- Comments follow the repository style: full sentences that explain why, never what. New comments name no plan, task, register row or review.
- Error strings start with the package prefix: `dockerapi:`, `tslocal:`, `netstat:`, `netfilter:`.
- Lint: `golangci-lint run --allow-serial-runners` and `GOTOOLCHAIN=local go vet ./...` stay clean. A `//nolint:gosec` carries the rule id and a reason, as the code below shows.
- Each task of a wave runs in its own worktree from the wave's base, so a package-wide command sees only merged work plus the task's own files.
- Commits use `<type>(<scope>): <subject>`, subject at most 50 characters, imperative, no period, and carry no attribution lines.
- The plan ends with the gates in `docs/dforge/gates.md`. No task touches `test/harness/`, `harness.yml`, `install.sh.in`, `internal/install/` or `internal/update/`, so gate 7 (harness) applies only on `main` once it is pushed. No task creates or pushes a tag.

## Contracts

Each entry names the task that creates it. A task's Interfaces block cites entries by number.

**C1 — Docker client core (Task 1), `internal/platform/dockerapi/client.go`.** Exported: `DefaultSocket`, `MinAPIVersion = "1.44"`, `MaxAPIVersion = "1.49"`, `ErrUnavailable`, `ErrUnsupportedVersion`, `APIError{StatusCode int; Message string}`, `IsNotFound(error) bool`, `IsConflict(error) bool`, `Filters map[string][]string`, `Client`, `New(socket string) *Client`, `(*Client).Close()`, `(*Client).APIVersion(ctx) (string, error)`, `Stream[T]` with `Next() (T, error)` and `Close() error`, `VersionInfo`, `(*Client).Version`, `SystemInfo`, `FirewallBackend{Driver string}`, `(*Client).Info`. Unexported, used by Tasks 5, 6, 10, 11 and 13:

```go
type request struct {
	method      string
	path        string     // without the /v<version> prefix
	query       url.Values
	body        any        // nil, an io.Reader sent as-is with contentType, or a value sent as JSON
	contentType string
}
func (c *Client) do(ctx context.Context, r request) (*http.Response, error)   // 2xx or 304; caller closes the body
func (c *Client) call(ctx context.Context, r request, out any) (int, error)   // decodes JSON into out (nil: discard); returns the status
func (f Filters) set(q url.Values) error
func newStream[T any](res *http.Response) *Stream[T]
func compareVersions(a, b string) (int, error)
```

Test helper in `client_test.go`: `func newFakeDaemon(t *testing.T, apiVersion string, mux *http.ServeMux) *Client` — serves `mux` on a temporary Unix socket and, when `apiVersion` is not empty, answers `GET /version` with that API version.

**C2 — Docker container types (Task 5), `internal/platform/dockerapi/containers.go`.** `ContainerSummary`, `PortSummary`, `SummaryNetworkSettings`, `ContainerJSON`, `ContainerState`, `ContainerConfig`, `HostConfig`, `PortBinding`, `RestartPolicy`, `Mount`, `MountPoint`, `NetworkSettings`, `EndpointSettings`, `EndpointIPAMConfig`, `ContainerCreate`, `NetworkingConfig`, `ListContainersOptions`, `RemoveContainerOptions`, and the methods `ListContainers`, `InspectContainer`, `CreateContainer`, `StartContainer`, `StopContainer`, `RestartContainer`, `RemoveContainer` with the signatures Task 5's code gives. Task 10 uses `ContainerConfig` (`ImageInspect.Config`); Task 11 uses `EndpointSettings` and `EndpointIPAMConfig`.

**C3 — Docker streams, images, volumes and networks (Tasks 6, 10, 11).** `streams.go` (Task 6): `LogsOptions`, `LogFrame`, `LogStream`, `Logs`, `Stats` and its parts, `(*Client).Stats`, `Event`, `EventActor`, `EventsOptions`, `Events`, `CopyFromContainer`, `CopyToContainer`, and the unexported `unixArg(time.Time) string`. `images.go` and `volumes.go` (Task 10): `ImageSummary`, `ImageInspect`, `PullMessage` with `Err()`, `ProgressDetail`, `ErrorDetail`, `ImageDeleteItem`, `ListImagesOptions`, `RemoveImageOptions`, `ListImages`, `InspectImage`, `PullImage`, `RemoveImage`; `Volume`, `VolumeCreate`, `ListVolumes`, `InspectVolume`, `CreateVolume`, `RemoveVolume`. `networks.go` (Task 11): `Network`, `IPAM`, `IPAMConfig`, `NetworkContainer`, `NetworkCreate`, `ListNetworks`, `InspectNetwork`, `CreateNetwork`, `RemoveNetwork`, `ConnectNetwork`, `DisconnectNetwork`. Task 13 calls them as the code gives them.

**C4 — Tailscale client core (Task 2), `internal/platform/tslocal/client.go`.** Exported: `DefaultSocket`, `ErrUnavailable`, `ErrPeerNotFound`, `APIError{StatusCode int; Message string}`, `Client`, `New(socket string) *Client`, `(*Client).Close()`, `Status`, `PeerStatus`, `TailnetStatus`, `ExitNodeStatus`, `(*Client).Status`, `Prefs` (keeps unmodelled fields across a round trip), `(*Client).Prefs`, `PrefsPatch`, `(*Client).EditPrefs`, `StartOptions`, `(*Client).Start`, `(*Client).Logout`. Unexported, used by Task 7:

```go
func (c *Client) send(ctx context.Context, method, path string, body any, want int) ([]byte, error) // body for status want, *APIError otherwise
func (c *Client) getJSON(ctx context.Context, method, path string, out any) error                 // send with want 200, then decode
```

Test helper in `client_test.go`: `func newFakeTailscaled(t *testing.T, mux *http.ServeMux) *Client` — serves `mux` on a temporary Unix socket and, like tailscaled, answers 403 to a request whose Host is not `local-tailscaled.sock` or that carries `Referer` or `Origin`.

**C5 — Tailscale DERP, certificates and WhoIs (Task 7), `internal/platform/tslocal/derp.go`.** `DERPMap`, `DERPRegion`, `DERPNode`, `(*Client).DERPMap` (nil for a logged-out node), `(*Client).CertPair(ctx, domain) (certPEM, keyPEM []byte, err error)`, `WhoIs`, `WhoIsNode`, `UserProfile`, `(*Client).WhoIs(ctx, netip.Addr)` (`ErrPeerNotFound` on 404), `DERPRegionReport`, `(*Client).DebugDERPRegion(ctx, region string)`. Task 12 calls `DERPMap` and `WhoIs`.

**C6 — Socket table (Task 3), `internal/platform/netstat/sockets.go`.** `Family int` with `IPv4 Family = 4` and `IPv6 Family = 6`; `Socket`, `(Socket).Listening()`, `Process`, `Table{Root string}`, `Sockets()`, `(Table).Sockets()`. Task 8 uses `Family`, `IPv4` and `IPv6`.

**C7 — Routes (Task 8), `internal/platform/netstat/routes.go`.** `ErrNoRoute`, `RouteProtoKernel`, `RouteProtoBoot`, `RouteProtoStatic`, `RouteProtoRA`, `RouteProtoDHCP`, `RouteQuery{Dst, Src netip.Addr; IIF, OIF string; Mark uint32}`, `Route`, `RouteGet(RouteQuery) (Route, error)`, `Routes(Family) ([]Route, error)`, `Interface`, `Interfaces() ([]Interface, error)`.

**C8 — Netfilter structure (Task 4), `internal/platform/netfilter/parse.go`.** `Tables`, `Ruleset{Tables []Table}`, `(*Ruleset).Table(name) *Table`, `Table{Name, Incompatible string; Chains []Chain}`, `(*Table).Chain(name) *Chain`, `Chain{Name string; Builtin bool; Policy string; Rules []Rule}`, `Rule{Line int; Text string; Source, Dest AddrMatch; In, Out, Proto NameMatch; Matches []Match; Target Target; Unsupported []string}`, `(Rule).Opaque() bool`, `AddrMatch{Prefix netip.Prefix; Neg bool}`, `NameMatch{Name string; Neg bool}`, `Match{Module string; Options []Option}`, `Target{Name string; Chain bool; Options []Option}`, `Option{Name string; Neg bool; Values []string}`, `Parse(io.Reader) (*Ruleset, error)`. Inside `(*parser).addRule`, the statement `rule.Line, rule.Text = p.line, line` is followed by the `Target.Chain` resolution and then `c.Rules = append(c.Rules, rule)`; Task 9 inserts `classify(&rule)` directly before that append. Test constant in `parse_test.go`: `psSample` (save output with two tables), which Task 9's fuzz target seeds with.

**C9 — Netfilter classification (Task 9), `internal/platform/netfilter/classify.go`.** `func classify(r *Rule)` appends to `r.Unsupported` one reason per module, target or option outside the contract; fixtures live in `internal/platform/netfilter/testdata/*.save`.

## Assumptions (evidence)

- Docker 29.5.3 on this host serves API 1.54 (`docker version`, 2026-10-23), so the client negotiates its cap of 1.49; `FirewallBackend` is absent at `/v1.44/info` and present at `/v1.49/info` as `{"Driver":"iptables",...}`; `POST /containers/{id}/start` and `/stop` answer 304 when the container is already in that state. Verified on this host on 2026-10-23 by the drafted `TestDockerLive` (Task 13's code), which passed.
- The daemon answers container logs with `Content-Type: application/vnd.docker.multiplexed-stream` without a TTY and `application/vnd.docker.raw-stream` with one, from API 1.42 (moby `api/server/router/container/container_routes.go`, `getContainersLogs`); the client never negotiates below 1.44, so the header is always present.
- tailscaled refuses a local API request whose Host is not `local-tailscaled.sock` or that has a `Referer` or `Origin` header; errors are JSON `{"error": ...}` or plain text; the certificate pair endpoint answers the key PEM then the chain; a logged-out node's `/localapi/v0/derpmap` is `null`; WhoIs of an unknown address answers 404. Verified on 2026-10-23 by the drafted `TestTailscaledLive` (Task 12's code) against tailscaled 1.82.0 and 1.104.2, from the Docker Hub images `tailscale/tailscale:v1.82.0` and `tailscale/tailscale:stable` (which reported 1.104.2), each run as Task 12's Step 3 runs it; the test version with `Start` and `Logout` passed against both on 2026-10-23. These two versions bracket the spec's "tailscaled 1.82 or later" and the harness's stable-repository install (`test/harness/pins.go`, `RequiredPackages`), which is how this plan meets 1.8's "a contract test runs them against the tailscaled in the harness pins" without editing harness files it does not own.
- Tasks 12 and 13 need Docker Hub access from this host for `tailscale/tailscale:v1.82.0`, `tailscale/tailscale:stable` and `alpine:3`; all three pulled on 2026-10-23.
- An input-interface route lookup needs a real interface: the kernel answers `Invalid argument` for `ip route get 127.0.0.1 from 127.0.0.1 iif lo`, while `ip route get 192.168.0.250 from 192.0.2.10 iif eth0` on this host answers `local ... dev lo` (2026-10-23). `TestRouteGetIncomingLive` therefore uses the default route's interface, so reverse-path filtering accepts the source, and skips on a host without an IPv4 default route; `TestRouteRequest` checks every attribute without a kernel.
- The kernel reports table 254 (main) for a lookup of an IPv4 local address while no policy rule beyond the defaults exists, because it merges the local table into main; `Route.Table`'s comment records it. Verified with the drafted `TestRouteGetLive` on this host on 2026-10-23.
- Spec 1.8 asks for interface addresses and route lookups "through hand-written netlink on `golang.org/x/sys/unix`". Routes use hand-written rtnetlink (Task 8); interface addresses use the standard library's `net.Interfaces` and `Addrs`, which read rtnetlink themselves (`src/net/interface_linux.go`). The plan reads 1.8's intent as "no third-party netlink library", which both satisfy; the integrator raises it with the owner when the wave merges, and a spec amendment, if wanted, is the integrator's (Parallel decomposition).
- The netfilter fixtures are real output where it could be captured: `docker29-v4.save` from this host's Docker 29.5.3 with iptables 1.8.11 (nf_tables) on 2026-10-23, trimmed to three networks; `ufw-v4.save` and `ufw-v6.save` from UFW 0.36.2 with iptables 1.8.10 (nf_tables) in an `ubuntu:24.04` container on 2026-10-23 (`ufw allow 22/tcp`, `ufw limit 2222/tcp`, a deny from a source, an allow on an interface from an IPv6 source, a `ufw route allow`, `ufw logging on`, `ufw --force enable`). tailscaled installs its chains only after login, so `tailscaled-v4.save` is reconstructed from the rule arguments in `util/linuxfw/iptables_runner.go` at tailscale v1.104.2 (https://github.com/tailscale/tailscale/blob/v1.104.2/util/linuxfw/iptables_runner.go), fetched 2026-10-23: the `ts-input` and `ts-forward` rules, the subnet-route `MARK` and `MASQUERADE`, the stateful-filtering `! --ctstate` rule, the `TCPMSS` clamp, the `CONNMARK` restore and save rules, the `statistic --mode nth` DNAT and the `SNAT` rule. The same file at v1.82.0 builds the same rules except the `CONNMARK` pair, which v1.104.2 adds. Wave 1's golden files capture tailscaled's real chains, Docker 28 and IPv6 output (spec 2.3).
- The parser recognizes a superset of spec 2.3's evaluator list (`icmp`, `icmp6`, `state`, `limit`, `recent`, `statistic`, `hl`, `rt`, `LOG`, `TCPMSS` besides the listed ones) because the pinned UFW and tailscaled generate them: a parse of their output must leave no opaque rule. Which of them decides a verdict is the evaluator's business (2.3 makes `limit`, `recent` and `statistic` on a deciding rule `unknown`). Fixture-backed in Task 9's `TestFixtures`.
- iptables-save prints an option value unquoted when it holds only letters, digits, `_` and `-` (`xtables_save_string` in iptables `libxtables/xtables.c`), so a value such as a comment `-leading` reads as an option; such a rule becomes opaque, never misparsed. Covered by Task 9's `TestClassify` row for `--comment -leading`.
- Every task's code and test was drafted and run before this plan was written: on 2026-10-23 each task's files, with exactly the files of the tasks it depends on, passed `go test` (the failing outputs in each Step 2 were captured the same way), the whole tree passed `golangci-lint run` with `0 issues.`, and `FuzzParse` ran 3.3 million inputs in 60 s without a failure.
- Wave 2 holds five tasks, above rule 5's three: they are trivially independent (five different files, no shared helper, in four packages), and the run executes a wider wave in parts.
- Spec 3's `docker.image.prune` needs no prune endpoint: R51's prune "removes exactly that list" the plan showed, so the module lists candidates with `ListImages` (with `SharedSize: true`, since `Size` minus `SharedSize` is what removing one image reclaims) and removes each with `RemoveImage`; the daemon's `POST /images/prune` would remove whatever is unused at apply time instead. Every other Docker operation sections 2–8 name maps to a call of Tasks 1, 5, 6, 10 or 11 (Compose runs through the exec allowlist, 1.8).
- `golang.org/x/sys v0.49.0` is already a direct requirement (`go.mod` line 18), so no task touches a manifest.

## Task index

1. Docker Engine client core
2. Tailscale local client core
3. Socket table
4. Netfilter save parser structure
5. Docker containers
6. Docker logs, stats, events and archives
7. Tailscale DERP map, certificates and WhoIs
8. Routes and interfaces
9. Netfilter contract classification and fixtures
10. Docker images and volumes
11. Docker networks
12. Tailscale live contract test
13. Docker live contract test

---

### Task 1: Docker Engine client core

The client's transport: a Unix-socket HTTP client, API version negotiation (the daemon's version capped at 1.49, refusing below 1.44, retried after a failure), the error type with its 404 and 409 classifiers, the filters argument, a generic JSON stream, `/version` and `/info` with `FirewallBackend`. The test file also creates the shared fake daemon `newFakeDaemon` (C1).

**Files:**
- Create: `internal/platform/dockerapi/client.go`
- Test: `internal/platform/dockerapi/client_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: C1.

**Items:** 79

**Depends on:** none

**Implementer:** dforge:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 0 = 3

- [ ] **Step 1: Write the failing test**

Create `internal/platform/dockerapi/client_test.go`:

```go
package dockerapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newFakeDaemon serves mux on a Unix socket in a short temporary directory
// and returns a client for it. When apiVersion is not empty it also answers
// GET /version with that API version, so negotiation succeeds.
func newFakeDaemon(t *testing.T, apiVersion string, mux *http.ServeMux) *Client {
	t.Helper()
	dir, err := os.MkdirTemp("", "dp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if apiVersion != "" {
		mux.HandleFunc("GET /version", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = fmt.Fprintf(w, `{"Version":"29.0.0","ApiVersion":%q,"MinAPIVersion":"1.40","Os":"linux","Arch":"amd64"}`, apiVersion)
		})
	}
	l, err := net.Listen("unix", filepath.Join(dir, "d.sock"))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(mux)
	_ = srv.Listener.Close()
	srv.Listener = l
	srv.Start()
	t.Cleanup(srv.Close)
	c := New(filepath.Join(dir, "d.sock"))
	t.Cleanup(c.Close)
	return c
}

func TestNegotiationCapsAtMax(t *testing.T) {
	cases := []struct {
		daemon, want string
	}{
		{"1.54", "1.49"},
		{"1.49", "1.49"},
		{"1.45", "1.45"},
		{"1.44", "1.44"},
	}
	for _, tc := range cases {
		t.Run(tc.daemon, func(t *testing.T) {
			mux := http.NewServeMux()
			var path string
			mux.HandleFunc("GET /v"+tc.want+"/info", func(w http.ResponseWriter, r *http.Request) {
				path = r.URL.Path
				_, _ = io.WriteString(w, `{"ServerVersion":"29.0.0"}`)
			})
			c := newFakeDaemon(t, tc.daemon, mux)
			info, err := c.Info(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if path != "/v"+tc.want+"/info" || info.ServerVersion != "29.0.0" {
				t.Fatalf("path %q, info %+v", path, info)
			}
			got, err := c.APIVersion(t.Context())
			if err != nil || got != tc.want {
				t.Fatalf("APIVersion = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestNegotiationRefusesOldDaemon(t *testing.T) {
	c := newFakeDaemon(t, "1.43", http.NewServeMux())
	_, err := c.Info(t.Context())
	if !errors.Is(err, ErrUnsupportedVersion) || !strings.Contains(err.Error(), "1.43") {
		t.Fatalf("err = %v, want ErrUnsupportedVersion naming 1.43", err)
	}
}

func TestNegotiationRefusesMalformedVersion(t *testing.T) {
	c := newFakeDaemon(t, "one.two", http.NewServeMux())
	if _, err := c.Info(t.Context()); err == nil || !strings.Contains(err.Error(), `malformed API version "one.two"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestNegotiationRetriesAfterFailure(t *testing.T) {
	mux := http.NewServeMux()
	calls := 0
	mux.HandleFunc("GET /version", func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = io.WriteString(w, `{"ApiVersion":"1.47"}`)
	})
	c := newFakeDaemon(t, "", mux)
	if _, err := c.APIVersion(t.Context()); err == nil {
		t.Fatal("first negotiation succeeded")
	}
	v, err := c.APIVersion(t.Context())
	if err != nil || v != "1.47" {
		t.Fatalf("second negotiation = %q, %v", v, err)
	}
	if _, err := c.APIVersion(t.Context()); err != nil || calls != 2 {
		t.Fatalf("cached negotiation called /version %d times, err %v", calls, err)
	}
}

func TestAPIErrorClassification(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1.49/info", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("case") {
		case "json":
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"message":"no such container: x"}`)
		case "conflict":
			w.WriteHeader(http.StatusConflict)
			_, _ = io.WriteString(w, `{"message":"volume is in use"}`)
		default:
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, "  plain failure \n")
		}
	})
	c := newFakeDaemon(t, "1.49", mux)
	call := func(kase string) error {
		_, err := c.call(t.Context(), request{method: http.MethodGet, path: "/info", query: map[string][]string{"case": {kase}}}, nil)
		return err
	}
	err := call("json")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Message != "no such container: x" || !IsNotFound(err) || IsConflict(err) {
		t.Fatalf("json error = %v", err)
	}
	if err := call("conflict"); !IsConflict(err) || IsNotFound(err) {
		t.Fatalf("conflict error = %v", err)
	}
	if err := call("plain"); !errors.As(err, &apiErr) || apiErr.StatusCode != 500 || apiErr.Message != "plain failure" {
		t.Fatalf("plain error = %v", err)
	}
}

func TestUnreachableSocketIsUnavailable(t *testing.T) {
	c := New(filepath.Join(t.TempDir(), "missing.sock"))
	defer c.Close()
	_, err := c.Version(context.Background())
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
}

func TestInfoFirewallBackend(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1.49/info", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"ID":"abc","ServerVersion":"28.1.0","Containers":3,"ContainersRunning":2,"Images":5,`+
			`"FirewallBackend":{"Driver":"iptables","Info":[["EnableUserlandProxy","true"]]}}`)
	})
	info, err := newFakeDaemon(t, "1.49", mux).Info(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if info.FirewallBackend == nil || info.FirewallBackend.Driver != "iptables" || info.ContainersRunning != 2 {
		t.Fatalf("info = %+v", info)
	}

	mux = http.NewServeMux()
	mux.HandleFunc("GET /v1.47/info", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"ServerVersion":"28.0.4"}`)
	})
	info, err = newFakeDaemon(t, "1.47", mux).Info(t.Context())
	if err != nil || info.FirewallBackend != nil {
		t.Fatalf("info without the field = %+v, %v", info, err)
	}
}

func TestFiltersAndStream(t *testing.T) {
	mux := http.NewServeMux()
	var filters string
	mux.HandleFunc("GET /v1.49/events", func(w http.ResponseWriter, r *http.Request) {
		filters = r.URL.Query().Get("filters")
		_, _ = io.WriteString(w, `{"n":1}`+"\n"+`{"n":2}`+"\n")
	})
	c := newFakeDaemon(t, "1.49", mux)
	q := map[string][]string{}
	if err := (Filters{"label": {"a=b"}}).set(q); err != nil {
		t.Fatal(err)
	}
	if err := (Filters{}).set(q); err != nil || len(q) != 1 {
		t.Fatalf("an empty filter set changed the query: %v", q)
	}
	res, err := c.do(t.Context(), request{method: http.MethodGet, path: "/events", query: q})
	if err != nil {
		t.Fatal(err)
	}
	s := newStream[struct{ N int }](res)
	defer func() { _ = s.Close() }()
	for want := 1; want <= 2; want++ {
		v, err := s.Next()
		if err != nil || v.N != want {
			t.Fatalf("value %d = %+v, %v", want, v, err)
		}
	}
	if _, err := s.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("end of stream = %v, want io.EOF", err)
	}
	if filters != `{"label":["a=b"]}` {
		t.Fatalf("filters = %s", filters)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mkdir -p /root/.cache/w0b2-tmp/1 && TMPDIR=/root/.cache/w0b2-tmp/1 GOTOOLCHAIN=local go test ./internal/platform/dockerapi/`
Expected: FAIL, build failed: `internal/platform/dockerapi/client_test.go:20:74: undefined: Client`

- [ ] **Step 3: Write the implementation**

Create `internal/platform/dockerapi/client.go`:

```go
// Package dockerapi is a hand-written client for the Docker Engine API over
// the daemon's Unix socket (spec 1.8). It models only the endpoints and
// fields Darkpanel's modules use, and it never shells out to the docker CLI
// (R54).
package dockerapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultSocket is the Docker daemon's API socket.
const DefaultSocket = "/var/run/docker.sock"

// The client speaks the newest API version both sides support within this
// range. 1.44 is Docker 25, the oldest daemon the docker module supports.
// 1.49 (Docker 28.1) is the first version whose /info reports
// FirewallBackend; newer versions change shapes this client does not model
// (1.54 adds a JSON log format), so the client never asks for more.
const (
	MinAPIVersion = "1.44"
	MaxAPIVersion = "1.49"
)

// maxBody bounds a JSON response read into memory; maxErrorBody bounds the
// text kept from an error answer.
const (
	maxBody      = 64 << 20
	maxErrorBody = 64 << 10
)

var (
	// ErrUnavailable wraps a failure to reach the daemon's socket.
	ErrUnavailable = errors.New("dockerapi: daemon unavailable")
	// ErrUnsupportedVersion is returned when the daemon's API is older than
	// MinAPIVersion.
	ErrUnsupportedVersion = errors.New("dockerapi: daemon API version unsupported")
)

// APIError is an answer outside 2xx and 304 from the daemon.
type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("dockerapi: daemon answered %d: %s", e.StatusCode, e.Message)
}

// IsNotFound reports whether err is the daemon's 404 answer.
func IsNotFound(err error) bool { return statusIs(err, http.StatusNotFound) }

// IsConflict reports whether err is the daemon's 409 answer (an object in
// use, a name taken).
func IsConflict(err error) bool { return statusIs(err, http.StatusConflict) }

func statusIs(err error, code int) bool {
	var e *APIError
	return errors.As(err, &e) && e.StatusCode == code
}

// Filters is the Engine API's filters argument: each key with its accepted
// values, for example {"label": {"darkpanel.module=npm"}}.
type Filters map[string][]string

func (f Filters) set(q url.Values) error {
	if len(f) == 0 {
		return nil
	}
	b, err := json.Marshal(map[string][]string(f))
	if err != nil {
		return fmt.Errorf("dockerapi: encode filters: %w", err)
	}
	q.Set("filters", string(b))
	return nil
}

// Client talks to one daemon. It negotiates the API version on first use
// and is safe for concurrent use.
type Client struct {
	hc *http.Client

	mu      sync.Mutex
	version string
}

// New returns a client for the daemon listening on socket.
func New(socket string) *Client {
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			conn, err := d.DialContext(ctx, "unix", socket)
			if err != nil {
				return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
			}
			return conn, nil
		},
		DisableCompression: true,
		MaxIdleConns:       4,
		IdleConnTimeout:    30 * time.Second,
	}
	return &Client{hc: &http.Client{Transport: tr}}
}

// Close releases idle connections.
func (c *Client) Close() { c.hc.CloseIdleConnections() }

// APIVersion returns the negotiated API version: the daemon's, capped at
// MaxAPIVersion. A failed negotiation is retried on the next call.
func (c *Client) APIVersion(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.version != "" {
		return c.version, nil
	}
	v, err := c.Version(ctx)
	if err != nil {
		return "", err
	}
	cmpMin, err := compareVersions(v.APIVersion, MinAPIVersion)
	if err != nil {
		return "", err
	}
	if cmpMin < 0 {
		return "", fmt.Errorf("%w: daemon API %s is older than %s", ErrUnsupportedVersion, v.APIVersion, MinAPIVersion)
	}
	c.version = MaxAPIVersion
	if cmpMax, _ := compareVersions(v.APIVersion, MaxAPIVersion); cmpMax < 0 {
		c.version = v.APIVersion
	}
	return c.version, nil
}

// compareVersions compares two "major.minor" API versions.
func compareVersions(a, b string) (int, error) {
	pa, err := parseVersion(a)
	if err != nil {
		return 0, err
	}
	pb, err := parseVersion(b)
	if err != nil {
		return 0, err
	}
	for i := range pa {
		if pa[i] != pb[i] {
			if pa[i] < pb[i] {
				return -1, nil
			}
			return 1, nil
		}
	}
	return 0, nil
}

func parseVersion(v string) ([2]int, error) {
	major, minor, ok := strings.Cut(v, ".")
	maj, err1 := strconv.Atoi(major)
	mnr, err2 := strconv.Atoi(minor)
	if !ok || err1 != nil || err2 != nil || maj < 0 || mnr < 0 {
		return [2]int{}, fmt.Errorf("dockerapi: malformed API version %q", v)
	}
	return [2]int{maj, mnr}, nil
}

// request is one call; body is nil, an io.Reader sent as-is with
// contentType, or a value sent as JSON.
type request struct {
	method      string
	path        string
	query       url.Values
	body        any
	contentType string
}

// do sends r under the negotiated version and returns the response when its
// status is 2xx or 304; the caller closes its body.
func (c *Client) do(ctx context.Context, r request) (*http.Response, error) {
	v, err := c.APIVersion(ctx)
	if err != nil {
		return nil, err
	}
	r.path = "/v" + v + r.path
	return c.send(ctx, r)
}

func (c *Client) send(ctx context.Context, r request) (*http.Response, error) {
	var body io.Reader
	contentType := r.contentType
	switch b := r.body.(type) {
	case nil:
	case io.Reader:
		body = b
	default:
		buf, err := json.Marshal(b)
		if err != nil {
			return nil, fmt.Errorf("dockerapi: encode %s %s: %w", r.method, r.path, err)
		}
		body = bytes.NewReader(buf)
		contentType = "application/json"
	}
	u := "http://docker" + r.path
	if len(r.query) > 0 {
		u += "?" + r.query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, r.method, u, body)
	if err != nil {
		return nil, fmt.Errorf("dockerapi: %s %s: %w", r.method, r.path, err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	res, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("dockerapi: %s %s: %w", r.method, r.path, err)
	}
	if res.StatusCode/100 != 2 && res.StatusCode != http.StatusNotModified {
		defer func() { _ = res.Body.Close() }()
		return nil, readAPIError(res)
	}
	return res, nil
}

func readAPIError(res *http.Response) error {
	b, _ := io.ReadAll(io.LimitReader(res.Body, maxErrorBody))
	msg := strings.TrimSpace(string(b))
	var j struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(b, &j) == nil && j.Message != "" {
		msg = j.Message
	}
	return &APIError{StatusCode: res.StatusCode, Message: msg}
}

// call sends r and decodes a JSON answer into out, or discards the body
// when out is nil. It returns the status code.
func (c *Client) call(ctx context.Context, r request, out any) (int, error) {
	res, err := c.do(ctx, r)
	if err != nil {
		return 0, err
	}
	defer func() { _ = res.Body.Close() }()
	if out == nil || res.StatusCode == http.StatusNotModified || res.StatusCode == http.StatusNoContent {
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, maxBody))
		return res.StatusCode, nil
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, maxBody)).Decode(out); err != nil {
		return res.StatusCode, fmt.Errorf("dockerapi: decode %s %s: %w", r.method, r.path, err)
	}
	return res.StatusCode, nil
}

// Stream decodes a stream of JSON values: events, pull progress, stats.
type Stream[T any] struct {
	body io.ReadCloser
	dec  *json.Decoder
}

func newStream[T any](res *http.Response) *Stream[T] {
	return &Stream[T]{body: res.Body, dec: json.NewDecoder(res.Body)}
}

// Next returns the next value, or io.EOF once the daemon ends the stream.
// Cancelling the request's context ends a stream that would block.
func (s *Stream[T]) Next() (T, error) {
	var v T
	err := s.dec.Decode(&v)
	return v, err
}

// Close ends the stream.
func (s *Stream[T]) Close() error { return s.body.Close() }

// VersionInfo is the daemon's /version answer.
type VersionInfo struct {
	Version       string
	APIVersion    string `json:"ApiVersion"`
	MinAPIVersion string
	Os            string
	Arch          string
	KernelVersion string
}

// Version reads /version without a version prefix, which every daemon
// serves; negotiation uses it.
func (c *Client) Version(ctx context.Context) (VersionInfo, error) {
	res, err := c.send(ctx, request{method: http.MethodGet, path: "/version"})
	if err != nil {
		return VersionInfo{}, err
	}
	defer func() { _ = res.Body.Close() }()
	var v VersionInfo
	if err := json.NewDecoder(io.LimitReader(res.Body, maxBody)).Decode(&v); err != nil {
		return VersionInfo{}, fmt.Errorf("dockerapi: decode /version: %w", err)
	}
	return v, nil
}

// SystemInfo is the part of /info the modules read.
type SystemInfo struct {
	ID                string
	ServerVersion     string
	Containers        int
	ContainersRunning int
	Images            int
	Driver            string
	DockerRootDir     string
	OperatingSystem   string
	// FirewallBackend is nil when the daemon does not report it (Docker
	// 28.0, or a negotiated version below 1.49): such a daemon uses its
	// iptables backend.
	FirewallBackend *FirewallBackend
}

// FirewallBackend is /info's FirewallBackend; its Info list is not stable
// and is not read.
type FirewallBackend struct {
	Driver string
}

// Info reads /info.
func (c *Client) Info(ctx context.Context) (SystemInfo, error) {
	var info SystemInfo
	_, err := c.call(ctx, request{method: http.MethodGet, path: "/info"}, &info)
	return info, err
}
```

- [ ] **Step 4: Run the tests and lint**

Run: `TMPDIR=/root/.cache/w0b2-tmp/1 GOTOOLCHAIN=local go test -count=1 ./internal/platform/dockerapi/ && TMPDIR=/root/.cache/w0b2-tmp/1 golangci-lint run --allow-serial-runners ./internal/platform/dockerapi/...`
Expected: `ok  	github.com/darkraise/darkpanel/internal/platform/dockerapi`, then `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add internal/platform/dockerapi/client.go internal/platform/dockerapi/client_test.go
git commit -m "feat(platform): add the Docker Engine client core"
rm -rf /root/.cache/w0b2-tmp/1
```

### Task 2: Tailscale local client core

tailscaled's local API transport (the fixed Host header, no Referer or Origin), its error shapes, status, preferences that keep every unmodelled field across a read-modify-write, masked preference edits, start with an auth key and logout. The test file also creates the shared fake `newFakeTailscaled` (C4); its other constant carries the prefix `ts`.

**Files:**
- Create: `internal/platform/tslocal/client.go`
- Test: `internal/platform/tslocal/client_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: C4.

**Items:** 79

**Depends on:** none

**Implementer:** dforge:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 0 = 3

- [ ] **Step 1: Write the failing test**

Create `internal/platform/tslocal/client_test.go`:

```go
package tslocal

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// newFakeTailscaled serves mux on a Unix socket in a short temporary
// directory and returns a client for it. Like tailscaled, the server
// refuses a request whose Host is not local-tailscaled.sock or that carries
// a Referer or Origin header.
func newFakeTailscaled(t *testing.T, mux *http.ServeMux) *Client {
	t.Helper()
	dir, err := os.MkdirTemp("", "dp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	l, err := net.Listen("unix", filepath.Join(dir, "ts.sock"))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "local-tailscaled.sock" || r.Referer() != "" || r.Header.Get("Origin") != "" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, "invalid localapi request\n")
			return
		}
		mux.ServeHTTP(w, r)
	}))
	_ = srv.Listener.Close()
	srv.Listener = l
	srv.Start()
	t.Cleanup(srv.Close)
	c := New(filepath.Join(dir, "ts.sock"))
	t.Cleanup(c.Close)
	return c
}

const tsStatusJSON = `{
  "Version": "1.102.0-t1234", "TUN": true, "BackendState": "Running", "AuthURL": "",
  "TailscaleIPs": ["100.101.102.103", "fd7a:115c:a1e0::1"],
  "Self": {"ID": "nSelf", "HostName": "vps", "DNSName": "vps.tail1234.ts.net.", "OS": "linux",
    "TailscaleIPs": ["100.101.102.103"], "Tags": ["tag:server"], "Online": true, "Relay": "fra"},
  "Health": [], "MagicDNSSuffix": "tail1234.ts.net",
  "CurrentTailnet": {"Name": "example.com", "MagicDNSSuffix": "tail1234.ts.net", "MagicDNSEnabled": true},
  "CertDomains": ["vps.tail1234.ts.net"],
  "Peer": {
    "nodekey:aa": {"ID": "nA", "HostName": "laptop", "DNSName": "laptop.tail1234.ts.net.", "TailscaleIPs": ["100.64.0.2"],
      "AllowedIPs": ["100.64.0.2/32", "10.0.0.0/24"], "PrimaryRoutes": ["10.0.0.0/24"],
      "Addrs": ["203.0.113.5:41641"], "CurAddr": "203.0.113.5:41641", "Relay": "fra", "Online": true, "Active": true,
      "LastHandshake": "2026-10-23T10:00:00Z", "RxBytes": 10, "TxBytes": 20, "ExitNodeOption": true},
    "nodekey:bb": {"ID": "nB", "HostName": "phone", "CurAddr": "", "Relay": "nyc", "PeerRelay": "", "Online": false,
      "LastHandshake": "0001-01-01T00:00:00Z", "KeyExpiry": "2027-01-01T00:00:00Z"}
  },
  "ExitNodeStatus": {"ID": "nA", "Online": true, "TailscaleIPs": ["100.64.0.2/32"]},
  "User": {"1": {"ID": 1, "LoginName": "owner@example.com"}}
}`

func TestStatus(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /localapi/v0/status", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, tsStatusJSON)
	})
	s, err := newFakeTailscaled(t, mux).Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if s.BackendState != "Running" || s.Self.DNSName != "vps.tail1234.ts.net." || s.TailscaleIPs[1] != netip.MustParseAddr("fd7a:115c:a1e0::1") ||
		s.CurrentTailnet.Name != "example.com" || s.ExitNodeStatus.ID != "nA" {
		t.Fatalf("status = %+v", s)
	}
	a, b := s.Peer["nodekey:aa"], s.Peer["nodekey:bb"]
	if a.CurAddr != "203.0.113.5:41641" || a.PrimaryRoutes[0] != netip.MustParsePrefix("10.0.0.0/24") || !a.ExitNodeOption ||
		b.Relay != "nyc" || b.CurAddr != "" || b.KeyExpiry == nil || !b.LastHandshake.IsZero() {
		t.Fatalf("peers = %+v %+v", a, b)
	}
}

func TestErrorsAndUnavailable(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /localapi/v0/status", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":"backend not ready"}`)
	})
	mux.HandleFunc("GET /localapi/v0/prefs", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, "prefs access denied\n")
	})
	c := newFakeTailscaled(t, mux)
	var apiErr *APIError
	if _, err := c.Status(t.Context()); !errors.As(err, &apiErr) || apiErr.StatusCode != 500 || apiErr.Message != "backend not ready" {
		t.Fatalf("json error = %v", err)
	}
	if _, err := c.Prefs(t.Context()); !errors.As(err, &apiErr) || apiErr.Message != "prefs access denied" {
		t.Fatalf("text error = %v", err)
	}
	missing := New(filepath.Join(t.TempDir(), "none.sock"))
	defer missing.Close()
	if _, err := missing.Status(t.Context()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("missing socket err = %v, want ErrUnavailable", err)
	}
}

func TestPrefsRoundTripKeepsUnmodelledFields(t *testing.T) {
	in := `{"ControlURL":"https://controlplane.tailscale.com","RouteAll":false,"ExitNodeID":"","ExitNodeIP":"",` +
		`"CorpDNS":true,"RunSSH":false,"WantRunning":false,"LoggedOut":true,"ShieldsUp":false,"AdvertiseTags":null,` +
		`"Hostname":"","AdvertiseRoutes":null,"NetfilterMode":2,"NoSNAT":false,"AutoUpdate":{"Check":true,"Apply":null},"Config":null}`
	var p Prefs
	if err := json.Unmarshal([]byte(in), &p); err != nil {
		t.Fatal(err)
	}
	if !p.CorpDNS || !p.LoggedOut || p.ControlURL != "https://controlplane.tailscale.com" {
		t.Fatalf("prefs = %+v", p)
	}
	p.WantRunning = true
	p.LoggedOut = false
	p.Hostname = "agent-1"
	p.AdvertiseTags = []string{"tag:darkpanel"}
	out, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got["NetfilterMode"] != float64(2) || got["AutoUpdate"].(map[string]any)["Check"] != true || got["WantRunning"] != true ||
		got["LoggedOut"] != false || got["Hostname"] != "agent-1" || !reflect.DeepEqual(got["AdvertiseTags"], []any{"tag:darkpanel"}) {
		t.Fatalf("encoded = %s", out)
	}
	if _, ok := got["Config"]; !ok {
		t.Fatalf("encoded prefs dropped an unmodelled field: %s", out)
	}

	fresh, err := json.Marshal(Prefs{WantRunning: true})
	if err != nil {
		t.Fatal(err)
	}
	var freshMap map[string]any
	_ = json.Unmarshal(fresh, &freshMap)
	if freshMap["WantRunning"] != true || len(freshMap) != 12 {
		t.Fatalf("prefs built in code = %s", fresh)
	}
}

func TestEditPrefsSendsMaskedPrefs(t *testing.T) {
	mux := http.NewServeMux()
	var body map[string]any
	mux.HandleFunc("PATCH /localapi/v0/prefs", func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		_, _ = io.WriteString(w, `{"ShieldsUp":true,"RunSSH":false,"AdvertiseRoutes":[],"NetfilterMode":2}`)
	})
	c := newFakeTailscaled(t, mux)
	yes, no := true, false
	none := []netip.Prefix(nil)
	ip := netip.MustParseAddr("100.64.0.2")
	p, err := c.EditPrefs(t.Context(), PrefsPatch{ShieldsUp: &yes, RunSSH: &no, AdvertiseRoutes: &none, ExitNodeIP: &ip})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"ShieldsUp": true, "ShieldsUpSet": true,
		"RunSSH": false, "RunSSHSet": true,
		"AdvertiseRoutes": []any{}, "AdvertiseRoutesSet": true,
		"ExitNodeIP": "100.64.0.2", "ExitNodeIPSet": true,
	}
	if !reflect.DeepEqual(body, want) {
		t.Fatalf("patch body = %#v", body)
	}
	if !p.ShieldsUp || p.RunSSH {
		t.Fatalf("returned prefs = %+v", p)
	}
	routes := []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0"), netip.MustParsePrefix("::/0")}
	b, err := json.Marshal(PrefsPatch{AdvertiseRoutes: &routes})
	if err != nil || string(b) != `{"AdvertiseRoutes":["0.0.0.0/0","::/0"],"AdvertiseRoutesSet":true}` {
		t.Fatalf("exit-node advertisement = %s, %v", b, err)
	}
	if b, _ := json.Marshal(PrefsPatch{}); string(b) != `{}` {
		t.Fatalf("empty patch = %s", b)
	}
}

func TestStartAndLogout(t *testing.T) {
	mux := http.NewServeMux()
	var start map[string]any
	mux.HandleFunc("POST /localapi/v0/start", func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&start); err != nil {
			t.Error(err)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	loggedOut := false
	mux.HandleFunc("POST /localapi/v0/logout", func(w http.ResponseWriter, _ *http.Request) {
		loggedOut = true
		w.WriteHeader(http.StatusNoContent)
	})
	c := newFakeTailscaled(t, mux)
	var p Prefs
	if err := json.Unmarshal([]byte(`{"CorpDNS":true,"NetfilterMode":2,"WantRunning":false}`), &p); err != nil {
		t.Fatal(err)
	}
	p.WantRunning = true
	if err := c.Start(t.Context(), StartOptions{AuthKey: "tskey-auth-x", UpdatePrefs: &p}); err != nil {
		t.Fatal(err)
	}
	up, _ := start["UpdatePrefs"].(map[string]any)
	if start["AuthKey"] != "tskey-auth-x" || up["WantRunning"] != true || up["NetfilterMode"] != float64(2) || up["CorpDNS"] != true {
		t.Fatalf("start body = %#v", start)
	}
	if err := c.Logout(t.Context()); err != nil || !loggedOut {
		t.Fatalf("logout = %v (called %v)", err, loggedOut)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mkdir -p /root/.cache/w0b2-tmp/2 && TMPDIR=/root/.cache/w0b2-tmp/2 GOTOOLCHAIN=local go test ./internal/platform/tslocal/`
Expected: FAIL, build failed: `internal/platform/tslocal/client_test.go:21:59: undefined: Client`

- [ ] **Step 3: Write the implementation**

Create `internal/platform/tslocal/client.go`:

```go
// Package tslocal is a hand-written client for tailscaled's local API over
// its Unix socket (spec 1.8). Wire types are plain structs carrying only the
// fields Darkpanel reads or writes; no Tailscale library is imported.
package tslocal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"
)

// DefaultSocket is tailscaled's local API socket on Linux.
const DefaultSocket = "/var/run/tailscale/tailscaled.sock"

// apiHost is the Host header tailscaled requires; it refuses any other
// host, and any request with a Referer or Origin header.
const apiHost = "local-tailscaled.sock"

// maxBody bounds a response read into memory.
const maxBody = 16 << 20

var (
	// ErrUnavailable wraps a failure to reach tailscaled's socket.
	ErrUnavailable = errors.New("tslocal: tailscaled unavailable")
	// ErrPeerNotFound is WhoIs's answer for an address no node holds.
	ErrPeerNotFound = errors.New("tslocal: no node holds that address")
)

// APIError is an unexpected status from tailscaled.
type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("tslocal: tailscaled answered %d: %s", e.StatusCode, e.Message)
}

// Client talks to one tailscaled. It is safe for concurrent use.
type Client struct {
	hc *http.Client
}

// New returns a client for the tailscaled listening on socket.
func New(socket string) *Client {
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			conn, err := d.DialContext(ctx, "unix", socket)
			if err != nil {
				return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
			}
			return conn, nil
		},
		DisableCompression: true,
		MaxIdleConns:       2,
		IdleConnTimeout:    30 * time.Second,
	}
	return &Client{hc: &http.Client{Transport: tr}}
}

// Close releases idle connections.
func (c *Client) Close() { c.hc.CloseIdleConnections() }

// send performs one call and returns the body when the status is want.
func (c *Client) send(ctx context.Context, method, path string, body any, want int) ([]byte, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("tslocal: encode %s %s: %w", method, path, err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://"+apiHost+path, rd)
	if err != nil {
		return nil, fmt.Errorf("tslocal: %s %s: %w", method, path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tslocal: %s %s: %w", method, path, err)
	}
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(res.Body, maxBody))
	if err != nil {
		return nil, fmt.Errorf("tslocal: read %s %s: %w", method, path, err)
	}
	if res.StatusCode != want {
		return nil, &APIError{StatusCode: res.StatusCode, Message: errorMessage(b)}
	}
	return b, nil
}

// errorMessage extracts tailscaled's error text: JSON {"error": ...} from
// most handlers, plain text from the rest.
func errorMessage(b []byte) string {
	var j struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(b, &j) == nil && j.Error != "" {
		return j.Error
	}
	return strings.TrimSpace(string(b))
}

func (c *Client) getJSON(ctx context.Context, method, path string, out any) error {
	b, err := c.send(ctx, method, path, nil, http.StatusOK)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("tslocal: decode %s: %w", path, err)
	}
	return nil
}

// Status is GET /localapi/v0/status. BackendState is one of NoState,
// NeedsLogin, NeedsMachineAuth, Stopped, Starting and Running.
type Status struct {
	Version        string
	BackendState   string
	AuthURL        string
	TailscaleIPs   []netip.Addr
	Self           *PeerStatus
	Health         []string
	MagicDNSSuffix string
	CurrentTailnet *TailnetStatus
	CertDomains    []string
	Peer           map[string]*PeerStatus // keyed by node public key
	ExitNodeStatus *ExitNodeStatus
}

// PeerStatus is a node as status reports it. A peer is reached directly
// when CurAddr is set, through DERP region Relay otherwise, and through a
// peer relay when PeerRelay is set (tailscaled 1.86 and later).
type PeerStatus struct {
	ID             string
	HostName       string
	DNSName        string
	OS             string
	TailscaleIPs   []netip.Addr
	AllowedIPs     []netip.Prefix
	PrimaryRoutes  []netip.Prefix
	Tags           []string
	Addrs          []string
	CurAddr        string
	Relay          string
	PeerRelay      string
	RxBytes        int64
	TxBytes        int64
	LastHandshake  time.Time
	Online         bool
	Active         bool
	ExitNode       bool
	ExitNodeOption bool
	KeyExpiry      *time.Time
}

// TailnetStatus is the tailnet the node belongs to.
type TailnetStatus struct {
	Name            string
	MagicDNSSuffix  string
	MagicDNSEnabled bool
}

// ExitNodeStatus is the node's current exit node.
type ExitNodeStatus struct {
	ID           string
	Online       bool
	TailscaleIPs []netip.Prefix
}

// Status reads the node's status with its peers.
func (c *Client) Status(ctx context.Context) (*Status, error) {
	var s Status
	if err := c.getJSON(ctx, http.MethodGet, "/localapi/v0/status", &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// Prefs is the node's preferences. Prefs read from tailscaled keep every
// field tailscaled sent, modelled or not, and send them back unchanged, so
// Start with UpdatePrefs replaces the preferences without resetting fields
// this type does not model.
type Prefs struct {
	ControlURL      string
	RouteAll        bool // accept routes
	ExitNodeID      string
	ExitNodeIP      netip.Addr
	CorpDNS         bool
	RunSSH          bool
	WantRunning     bool
	LoggedOut       bool
	ShieldsUp       bool
	AdvertiseTags   []string
	Hostname        string
	AdvertiseRoutes []netip.Prefix // 0.0.0.0/0 and ::/0 advertise an exit node

	raw map[string]json.RawMessage
}

// prefsFields is Prefs without its methods, for encoding the modelled
// fields.
type prefsFields Prefs

// UnmarshalJSON decodes the modelled fields and keeps every field.
func (p *Prefs) UnmarshalJSON(b []byte) error {
	var f prefsFields
	if err := json.Unmarshal(b, &f); err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	*p = Prefs(f)
	p.raw = raw
	return nil
}

// MarshalJSON encodes every kept field with the modelled fields' current
// values over them.
func (p Prefs) MarshalJSON() ([]byte, error) {
	b, err := json.Marshal(prefsFields(p))
	if err != nil {
		return nil, err
	}
	if len(p.raw) == 0 {
		return b, nil
	}
	var typed map[string]json.RawMessage
	if err := json.Unmarshal(b, &typed); err != nil {
		return nil, err
	}
	out := maps.Clone(p.raw)
	maps.Copy(out, typed)
	return json.Marshal(out)
}

// Prefs reads the node's preferences.
func (c *Client) Prefs(ctx context.Context) (*Prefs, error) {
	var p Prefs
	if err := c.getJSON(ctx, http.MethodGet, "/localapi/v0/prefs", &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// PrefsPatch is a masked preferences edit: only the non-nil fields change.
// It is sent as tailscaled's MaskedPrefs, each value with its <Field>Set
// flag.
type PrefsPatch struct {
	WantRunning     *bool
	RouteAll        *bool
	ShieldsUp       *bool
	RunSSH          *bool
	ExitNodeID      *string
	ExitNodeIP      *netip.Addr
	AdvertiseRoutes *[]netip.Prefix
}

// MarshalJSON encodes the patch as MaskedPrefs.
func (p PrefsPatch) MarshalJSON() ([]byte, error) {
	m := map[string]any{}
	set := func(name string, v any) {
		m[name] = v
		m[name+"Set"] = true
	}
	if p.WantRunning != nil {
		set("WantRunning", *p.WantRunning)
	}
	if p.RouteAll != nil {
		set("RouteAll", *p.RouteAll)
	}
	if p.ShieldsUp != nil {
		set("ShieldsUp", *p.ShieldsUp)
	}
	if p.RunSSH != nil {
		set("RunSSH", *p.RunSSH)
	}
	if p.ExitNodeID != nil {
		set("ExitNodeID", *p.ExitNodeID)
	}
	if p.ExitNodeIP != nil {
		set("ExitNodeIP", *p.ExitNodeIP)
	}
	if p.AdvertiseRoutes != nil {
		routes := *p.AdvertiseRoutes
		if routes == nil {
			routes = []netip.Prefix{}
		}
		set("AdvertiseRoutes", routes)
	}
	return json.Marshal(m)
}

// EditPrefs applies a masked edit and returns the resulting preferences.
func (c *Client) EditPrefs(ctx context.Context, patch PrefsPatch) (*Prefs, error) {
	b, err := c.send(ctx, http.MethodPatch, "/localapi/v0/prefs", patch, http.StatusOK)
	if err != nil {
		return nil, err
	}
	var p Prefs
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, fmt.Errorf("tslocal: decode prefs: %w", err)
	}
	return &p, nil
}

// StartOptions is POST /localapi/v0/start's ipn.Options: an auth key to log
// in with and, optionally, the complete preferences to start with.
type StartOptions struct {
	AuthKey     string `json:",omitempty"`
	UpdatePrefs *Prefs `json:",omitempty"`
}

// Start (re)starts the backend, logging in with opts.AuthKey when set.
// UpdatePrefs replaces every preference, so it should be the node's
// current Prefs with the wanted fields changed.
func (c *Client) Start(ctx context.Context, opts StartOptions) error {
	_, err := c.send(ctx, http.MethodPost, "/localapi/v0/start", opts, http.StatusNoContent)
	return err
}

// Logout logs the node out of its tailnet.
func (c *Client) Logout(ctx context.Context) error {
	_, err := c.send(ctx, http.MethodPost, "/localapi/v0/logout", nil, http.StatusNoContent)
	return err
}
```

- [ ] **Step 4: Run the tests and lint**

Run: `TMPDIR=/root/.cache/w0b2-tmp/2 GOTOOLCHAIN=local go test -count=1 ./internal/platform/tslocal/ && TMPDIR=/root/.cache/w0b2-tmp/2 golangci-lint run --allow-serial-runners ./internal/platform/tslocal/...`
Expected: `ok  	github.com/darkraise/darkpanel/internal/platform/tslocal`, then `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add internal/platform/tslocal/client.go internal/platform/tslocal/client_test.go
git commit -m "feat(platform): add the Tailscale local client core"
rm -rf /root/.cache/w0b2-tmp/2
```

### Task 3: Socket table

TCP and UDP sockets of both families from `/proc/net/{tcp,tcp6,udp,udp6}`, joined to the processes holding them through `/proc/<pid>/fd`. `Table.Root` lets the tests point at a fake proc tree; `TestSocketsLive` checks a real listener of the test process. Test helpers carry the prefix `sk`.

**Files:**
- Create: `internal/platform/netstat/sockets.go`
- Test: `internal/platform/netstat/sockets_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: C6.

**Items:** 79

**Depends on:** none

**Implementer:** dforge:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 0 = 3

- [ ] **Step 1: Write the failing test**

Create `internal/platform/netstat/sockets_test.go`:

```go
package netstat

import (
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// The fixtures are little-endian kernel output, as on every architecture
// Darkpanel ships for (amd64, arm64).
const (
	skHeader4 = "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n"
	skHeader6 = "  sl  local_address                         remote_address                        st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n"
	skTCP     = skHeader4 +
		"   0: 00000000:0016 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 1001 1 0000000000000000 100 0 0 10 0\n" +
		"   1: FA00A8C0:0016 0500A8C0:D431 01 00000000:00000000 02:000AE0C6 00000000     0        0 1002 4 0000000000000000 20 4 29 10 -1\n" +
		"   2: 0100007F:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000   999        0 1003 1 0000000000000000 100 0 0 10 0\n"
	skTCP6 = skHeader6 +
		"   0: 00000000000000000000000000000000:0016 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 1004 1 0000000000000000 100 0 0 10 0\n" +
		"   1: 0000000000000000FFFF0000FA00A8C0:0016 0000000000000000FFFF00000600A8C0:E001 01 00000000:00000000 00:00000000 00000000     0        0 1005 1 0000000000000000 20 4 30 10 -1\n" +
		"   2: B80D0120000000000000000001000000:01BB 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 0 1 0000000000000000 100 0 0 10 0\n"
	skUDP = skHeader4 +
		"  123: 00000000:0035 00000000:0000 07 00000000:00000000 00:00000000 00000000   101        0 1006 2 0000000000000000 0\n"
)

// skProc builds a fake proc root: the socket tables and, per pid, its comm
// and fd links to socket inodes.
func skProc(t *testing.T, tables map[string]string, procs map[string][]string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "net"), 0o750); err != nil {
		t.Fatal(err)
	}
	for name, text := range tables {
		if err := os.WriteFile(filepath.Join(root, "net", name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for pid, spec := range procs {
		fd := filepath.Join(root, pid, "fd")
		if err := os.MkdirAll(fd, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, pid, "comm"), []byte(spec[0]+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		for i, target := range spec[1:] {
			if err := os.Symlink(target, filepath.Join(fd, string(rune('3'+i)))); err != nil {
				t.Fatal(err)
			}
		}
	}
	return root
}

func TestSocketsFromProc(t *testing.T) {
	root := skProc(t,
		map[string]string{"tcp": skTCP, "tcp6": skTCP6, "udp": skUDP},
		map[string][]string{
			"812":  {"sshd", "socket:[1001]", "socket:[1004]", "/dev/null"},
			"4410": {"sshd-session", "socket:[1002]", "socket:[1005]", "socket:[1005]"},
			"4411": {"sshd-session", "socket:[1002]"},
			"9000": {"darkpanel", "pipe:[77]", "socket:[1003]"},
			"self": {"ignored", "socket:[1001]"},
		})
	socks, err := Table{Root: root}.Sockets()
	if err != nil {
		t.Fatal(err)
	}
	if len(socks) != 7 {
		t.Fatalf("read %d sockets, want 7 (udp6 is absent and skipped)", len(socks))
	}
	byInode := map[uint64]Socket{}
	for _, s := range socks {
		byInode[s.Inode] = s
	}
	sshd := byInode[1001]
	if sshd.Proto != "tcp" || sshd.Family != IPv4 || sshd.Local != netip.MustParseAddrPort("0.0.0.0:22") || !sshd.Listening() ||
		!reflect.DeepEqual(sshd.Processes, []Process{{PID: 812, Comm: "sshd"}}) {
		t.Fatalf("sshd listener = %+v", sshd)
	}
	sess := byInode[1002]
	if sess.State != "established" || sess.Listening() || sess.Local != netip.MustParseAddrPort("192.168.0.250:22") ||
		sess.Remote != netip.MustParseAddrPort("192.168.0.5:54321") || len(sess.Processes) != 2 {
		t.Fatalf("ssh session = %+v", sess)
	}
	pids := []int{sess.Processes[0].PID, sess.Processes[1].PID}
	slices.Sort(pids)
	if !slices.Equal(pids, []int{4410, 4411}) {
		t.Fatalf("session owners = %+v", sess.Processes)
	}
	if s := byInode[1003]; s.UID != 999 || s.Local != netip.MustParseAddrPort("127.0.0.1:8080") || s.Processes[0].Comm != "darkpanel" {
		t.Fatalf("loopback listener = %+v", s)
	}
	mapped := byInode[1005]
	if mapped.Family != IPv6 || mapped.Local.Addr() != netip.MustParseAddr("::ffff:192.168.0.250") ||
		mapped.Remote.Addr().Unmap() != netip.MustParseAddr("192.168.0.6") || mapped.Remote.Port() != 0xE001 ||
		len(mapped.Processes) != 1 {
		t.Fatalf("mapped session = %+v", mapped)
	}
	if s := byInode[0]; s.Local != netip.MustParseAddrPort("[2001:db8::1]:443") || s.Processes != nil {
		t.Fatalf("socket without inode = %+v", s)
	}
	if s := byInode[1006]; s.Proto != "udp" || !s.Listening() || s.Local.Port() != 53 || s.UID != 101 || s.Processes != nil {
		t.Fatalf("udp socket = %+v", s)
	}
}

func TestSocketsRejectMalformedTables(t *testing.T) {
	cases := map[string]string{
		"few fields":   skHeader4 + "   0: 00000000:0016 00000000:0000 0A\n",
		"short addr":   skHeader4 + "   0: 0000:0016 00000000:0000 0A 0:0 0:0 0 0 0 1 1\n",
		"no port":      skHeader4 + "   0: 00000000 00000000:0000 0A 0:0 0:0 0 0 0 1 1\n",
		"bad hex":      skHeader4 + "   0: 0000000G:0016 00000000:0000 0A 0:0 0:0 0 0 0 1 1\n",
		"bad state":    skHeader4 + "   0: 00000000:0016 00000000:0000 FF 0:0 0:0 0 0 0 1 1\n",
		"bad uid":      skHeader4 + "   0: 00000000:0016 00000000:0000 0A 0:0 0:0 0 x 0 1 1\n",
		"bad inode":    skHeader4 + "   0: 00000000:0016 00000000:0000 0A 0:0 0:0 0 0 0 -1 1\n",
		"port too big": skHeader4 + "   0: 00000000:10000 00000000:0000 0A 0:0 0:0 0 0 0 1 1\n",
	}
	for name, table := range cases {
		t.Run(name, func(t *testing.T) {
			root := skProc(t, map[string]string{"tcp": table}, nil)
			if _, err := (Table{Root: root}).Sockets(); err == nil || !strings.Contains(err.Error(), "line 2") {
				t.Fatalf("err = %v, want one naming line 2", err)
			}
		})
	}
}

func TestSocketsLive(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	want := netip.MustParseAddrPort(l.Addr().String())
	socks, err := Sockets()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range socks {
		if s.Proto == "tcp" && s.Local == want && s.Listening() {
			for _, p := range s.Processes {
				if p.PID == os.Getpid() {
					return
				}
			}
			t.Fatalf("listener %v found without this process: %+v", want, s.Processes)
		}
	}
	t.Fatalf("listener %v not in the socket table", want)
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mkdir -p /root/.cache/w0b2-tmp/3 && TMPDIR=/root/.cache/w0b2-tmp/3 GOTOOLCHAIN=local go test ./internal/platform/netstat/`
Expected: FAIL, build failed: `internal/platform/netstat/sockets_test.go:71:16: undefined: Table`

- [ ] **Step 3: Write the implementation**

Create `internal/platform/netstat/sockets.go`:

```go
// Package netstat reads the host's socket table, interface addresses and
// routes (spec 1.8): sockets from /proc/net joined to their processes
// through /proc/<pid>/fd, routes through hand-written rtnetlink.
package netstat

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Family is an address family.
type Family int

// The address families.
const (
	IPv4 Family = 4
	IPv6 Family = 6
)

// Socket is one entry of the kernel's TCP or UDP socket table. Addresses
// are as the kernel reports them: an IPv4 peer of a dual-stack IPv6 socket
// appears IPv4-mapped (::ffff:a.b.c.d), so callers comparing addresses
// unmap them.
type Socket struct {
	Proto     string // "tcp" or "udp"
	Family    Family // of the table the entry came from
	Local     netip.AddrPort
	Remote    netip.AddrPort
	State     string // a TCP state name; for UDP "established" (connected) or "close" (unconnected)
	UID       uint32
	Inode     uint64
	Processes []Process // every process holding the socket; empty when none is visible
}

// Listening reports whether the socket accepts new connections or
// datagrams: a TCP socket in LISTEN, or an unconnected UDP socket.
func (s Socket) Listening() bool {
	if s.Proto == "tcp" {
		return s.State == "listen"
	}
	return s.State == "close"
}

// Process is a process holding a socket.
type Process struct {
	PID  int
	Comm string // /proc/<pid>/comm
}

// tcpStates are the kernel's TCP states (include/net/tcp_states.h).
var tcpStates = map[uint64]string{
	0x01: "established", 0x02: "syn-sent", 0x03: "syn-recv", 0x04: "fin-wait1",
	0x05: "fin-wait2", 0x06: "time-wait", 0x07: "close", 0x08: "close-wait",
	0x09: "last-ack", 0x0A: "listen", 0x0B: "closing", 0x0C: "new-syn-recv",
}

// Table reads the socket table under a proc root.
type Table struct {
	Root string // "/proc" on a host
}

// Sockets reads the host's sockets from /proc.
func Sockets() ([]Socket, error) { return Table{Root: "/proc"}.Sockets() }

// Sockets reads every TCP and UDP socket of both families with the
// processes holding them. A table the kernel does not provide (IPv6
// disabled) is skipped.
func (t Table) Sockets() ([]Socket, error) {
	var out []Socket
	for _, src := range []struct {
		file   string
		proto  string
		family Family
	}{
		{"tcp", "tcp", IPv4}, {"tcp6", "tcp", IPv6}, {"udp", "udp", IPv4}, {"udp6", "udp", IPv6},
	} {
		socks, err := t.readTable(filepath.Join(t.Root, "net", src.file), src.proto, src.family)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, socks...)
	}
	owners, err := t.owners()
	if err != nil {
		return nil, err
	}
	for i := range out {
		if out[i].Inode != 0 {
			out[i].Processes = owners[out[i].Inode]
		}
	}
	return out, nil
}

func (t Table) readTable(path, proto string, family Family) ([]Socket, error) {
	f, err := os.Open(path) //nolint:gosec // G304: the proc root joined with a fixed table name
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var out []Socket
	sc := bufio.NewScanner(f)
	line := 0
	for sc.Scan() {
		line++
		if line == 1 {
			continue
		}
		fields := strings.Fields(sc.Text())
		if len(fields) == 0 {
			continue
		}
		s, err := parseEntry(fields, proto, family)
		if err != nil {
			return nil, fmt.Errorf("netstat: %s line %d: %w", path, line, err)
		}
		out = append(out, s)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("netstat: read %s: %w", path, err)
	}
	return out, nil
}

// parseEntry parses one line of /proc/net/{tcp,udp}{,6}: sl, local
// address, remote address, state, queues, timer, retransmits, uid,
// timeout, inode, and fields this reader ignores.
func parseEntry(fields []string, proto string, family Family) (Socket, error) {
	if len(fields) < 10 {
		return Socket{}, fmt.Errorf("%d fields, want at least 10", len(fields))
	}
	local, err := parseAddrPort(fields[1], family)
	if err != nil {
		return Socket{}, fmt.Errorf("local address: %w", err)
	}
	remote, err := parseAddrPort(fields[2], family)
	if err != nil {
		return Socket{}, fmt.Errorf("remote address: %w", err)
	}
	st, err := strconv.ParseUint(fields[3], 16, 8)
	if err != nil {
		return Socket{}, fmt.Errorf("state %q: %w", fields[3], err)
	}
	state, ok := tcpStates[st]
	if !ok {
		return Socket{}, fmt.Errorf("unknown state %q", fields[3])
	}
	uid, err := strconv.ParseUint(fields[7], 10, 32)
	if err != nil {
		return Socket{}, fmt.Errorf("uid %q: %w", fields[7], err)
	}
	inode, err := strconv.ParseUint(fields[9], 10, 64)
	if err != nil {
		return Socket{}, fmt.Errorf("inode %q: %w", fields[9], err)
	}
	return Socket{
		Proto: proto, Family: family, Local: local, Remote: remote,
		State: state, UID: uint32(uid), Inode: inode,
	}, nil
}

// parseAddrPort decodes the kernel's "ADDR:PORT" hex form. The address is
// printed as 32-bit words in host byte order (four words for IPv6), the
// port as a plain hex number.
func parseAddrPort(s string, family Family) (netip.AddrPort, error) {
	addrHex, portHex, ok := strings.Cut(s, ":")
	if !ok {
		return netip.AddrPort{}, fmt.Errorf("%q has no port", s)
	}
	port, err := strconv.ParseUint(portHex, 16, 16)
	if err != nil {
		return netip.AddrPort{}, fmt.Errorf("port %q: %w", portHex, err)
	}
	want := 8
	if family == IPv6 {
		want = 32
	}
	if len(addrHex) != want {
		return netip.AddrPort{}, fmt.Errorf("address %q has %d hex digits, want %d", addrHex, len(addrHex), want)
	}
	raw, err := hex.DecodeString(addrHex)
	if err != nil {
		return netip.AddrPort{}, fmt.Errorf("address %q: %w", addrHex, err)
	}
	b := make([]byte, len(raw))
	for i := 0; i < len(raw); i += 4 {
		binary.NativeEndian.PutUint32(b[i:], binary.BigEndian.Uint32(raw[i:]))
	}
	addr, _ := netip.AddrFromSlice(b)
	return netip.AddrPortFrom(addr, uint16(port)), nil
}

// owners maps socket inodes to the processes holding them. Processes that
// exit while being read, and descriptors this process may not read, are
// skipped: the table is a snapshot either way.
func (t Table) owners() (map[uint64][]Process, error) {
	entries, err := os.ReadDir(t.Root)
	if err != nil {
		return nil, fmt.Errorf("netstat: read %s: %w", t.Root, err)
	}
	out := map[uint64][]Process{}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 0 {
			continue
		}
		fdDir := filepath.Join(t.Root, e.Name(), "fd")
		fds, err := os.ReadDir(fdDir)
		if err != nil {
			continue
		}
		var proc *Process
		seen := map[uint64]bool{}
		for _, fd := range fds {
			target, err := os.Readlink(filepath.Join(fdDir, fd.Name()))
			if err != nil {
				continue
			}
			inodeText, ok := strings.CutPrefix(target, "socket:[")
			if !ok || !strings.HasSuffix(inodeText, "]") {
				continue
			}
			inode, err := strconv.ParseUint(strings.TrimSuffix(inodeText, "]"), 10, 64)
			if err != nil || seen[inode] {
				continue
			}
			seen[inode] = true
			if proc == nil {
				comm, _ := os.ReadFile(filepath.Join(t.Root, e.Name(), "comm"))
				proc = &Process{PID: pid, Comm: strings.TrimSuffix(string(comm), "\n")}
			}
			out[inode] = append(out[inode], *proc)
		}
	}
	return out, nil
}
```

- [ ] **Step 4: Run the tests and lint**

Run: `TMPDIR=/root/.cache/w0b2-tmp/3 GOTOOLCHAIN=local go test -count=1 ./internal/platform/netstat/ && TMPDIR=/root/.cache/w0b2-tmp/3 golangci-lint run --allow-serial-runners ./internal/platform/netstat/...`
Expected: `ok  	github.com/darkraise/darkpanel/internal/platform/netstat`, then `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add internal/platform/netstat/sockets.go internal/platform/netstat/sockets_test.go
git commit -m "feat(platform): read the socket table from proc"
rm -rf /root/.cache/w0b2-tmp/3
```

### Task 4: Netfilter save parser structure

Parses `iptables-save` and `ip6tables-save` output into tables, chains and rules: the iptables-restore tokenizer (double quotes with `\"`, `\\` and `\'` escapes; a quoted token is always a value), `!` negation of the following option, the basic matches typed (`-s`, `-d`, `-i`, `-o`, `-p`), every `-m` module and the `-j` target with their options in order, and iptables-nft-save's incompatible-table notes. Structural faults (a line outside a table, an undeclared chain, an unknown table, a missing COMMIT, an unterminated quote) are errors; `-g`, `-f`, a non-CIDR address, a repeated basic option or an option outside any module make the rule opaque. Task 9 adds the classification of modules and targets. The test constant `psSample` is shared with Task 9 (C8).

**Files:**
- Create: `internal/platform/netfilter/parse.go`
- Test: `internal/platform/netfilter/parse_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: C8.

**Items:** 79

**Depends on:** none

**Implementer:** dforge:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 0 = 3

- [ ] **Step 1: Write the failing test**

Create `internal/platform/netfilter/parse_test.go`:

```go
package netfilter

import (
	"net/netip"
	"reflect"
	"strings"
	"testing"
)

const psSample = `# Generated by iptables-save v1.8.11 (nf_tables)
*filter
:INPUT DROP [0:0]
:FORWARD ACCEPT [12:345]
:DOCKER-USER - [0:0]
-A INPUT -s 10.0.0.0/8 ! -i lo -p tcp -m tcp ! --dport 22 -m conntrack --ctstate NEW -j DOCKER-USER
-A FORWARD ! -d 2001:db8::/32 -o br+ -m comment --comment "say \"hi\" \\ there" -j LOG --log-prefix "[UFW BLOCK] "
-A DOCKER-USER
COMMIT
# Completed
*nat
:POSTROUTING ACCEPT [0:0]
COMMIT
`

func TestParseStructure(t *testing.T) {
	rs, err := Parse(strings.NewReader(psSample))
	if err != nil {
		t.Fatal(err)
	}
	if len(rs.Tables) != 2 || rs.Tables[0].Name != "filter" || rs.Tables[1].Name != "nat" {
		t.Fatalf("tables = %+v", rs.Tables)
	}
	filter := rs.Table("filter")
	if got := []Chain{filter.Chains[0], filter.Chains[1], filter.Chains[2]}; got[0].Policy != "DROP" || !got[0].Builtin ||
		got[1].Policy != "ACCEPT" || got[2].Builtin || got[2].Policy != "" {
		t.Fatalf("chains = %+v", got)
	}
	in := filter.Chain("INPUT").Rules[0]
	want := Rule{
		Line:   6,
		Text:   "-A INPUT -s 10.0.0.0/8 ! -i lo -p tcp -m tcp ! --dport 22 -m conntrack --ctstate NEW -j DOCKER-USER",
		Source: AddrMatch{Prefix: netip.MustParsePrefix("10.0.0.0/8")},
		In:     NameMatch{Name: "lo", Neg: true},
		Proto:  NameMatch{Name: "tcp"},
		Matches: []Match{
			{Module: "tcp", Options: []Option{{Name: "dport", Neg: true, Values: []string{"22"}}}},
			{Module: "conntrack", Options: []Option{{Name: "ctstate", Values: []string{"NEW"}}}},
		},
		Target: Target{Name: "DOCKER-USER", Chain: true},
	}
	if !reflect.DeepEqual(in, want) {
		t.Fatalf("INPUT rule:\n got %+v\nwant %+v", in, want)
	}
	fwd := filter.Chain("FORWARD").Rules[0]
	if fwd.Dest != (AddrMatch{Prefix: netip.MustParsePrefix("2001:db8::/32"), Neg: true}) || fwd.Out != (NameMatch{Name: "br+"}) {
		t.Fatalf("FORWARD addresses = %+v %+v", fwd.Dest, fwd.Out)
	}
	if got := fwd.Matches[0].Options[0].Values[0]; got != `say "hi" \ there` {
		t.Fatalf("comment = %q", got)
	}
	if fwd.Target.Name != "LOG" || fwd.Target.Chain || fwd.Target.Options[0].Values[0] != "[UFW BLOCK] " {
		t.Fatalf("target = %+v", fwd.Target)
	}
	if fwd.Opaque() || in.Opaque() {
		t.Fatalf("unsupported = %v %v", in.Unsupported, fwd.Unsupported)
	}
	if bare := filter.Chain("DOCKER-USER").Rules[0]; bare.Target.Name != "" || len(bare.Matches) != 0 {
		t.Fatalf("bare rule = %+v", bare)
	}
	if rs.Table("raw") != nil || filter.Chain("OUTPUT") != nil {
		t.Fatal("lookup of a missing table or chain returned one")
	}
}

func TestParseErrors(t *testing.T) {
	for name, tc := range map[string]struct{ in, want string }{
		"unknown table":     {"*broute\nCOMMIT\n", `line 1: unknown table "broute"`},
		"table twice":       {"*nat\nCOMMIT\n*nat\nCOMMIT\n", "line 3: table nat appears twice"},
		"nested table":      {"*nat\n*raw\n", "line 2: table raw opened inside table nat"},
		"no commit":         {"*nat\n:INPUT ACCEPT [0:0]\n", "table nat has no COMMIT"},
		"stray commit":      {"COMMIT\n", "line 1: COMMIT outside a table"},
		"chain outside":     {":INPUT ACCEPT [0:0]\n", "line 1: chain declared outside a table"},
		"bad declaration":   {"*nat\n:INPUT\n", `line 2: malformed chain declaration "INPUT"`},
		"bad policy":        {"*nat\n:INPUT REJECT [0:0]\n", `line 2: chain INPUT has policy "REJECT"`},
		"chain twice":       {"*nat\n:X - [0:0]\n:X - [0:0]\n", "line 3: chain X declared twice"},
		"rule outside":      {"-A INPUT -j ACCEPT\n", "line 1: rule outside a table"},
		"undeclared chain":  {"*nat\n-A X -j ACCEPT\n", `line 2: rule for undeclared chain "X"`},
		"unterminated":      {"*nat\n:X - [0:0]\n-A X -m comment --comment \"open\n", "line 3: unterminated quote"},
		"dangling negation": {"*nat\n:X - [0:0]\n-A X -s 10.0.0.1/32 !\n", "line 3: ! not followed by an option"},
		"loose value":       {"*nat\n:X - [0:0]\n-A X extra\n", `line 3: value "extra" without an option`},
		"match without":     {"*nat\n:X - [0:0]\n-A X -m\n", "line 3: -m needs one name"},
		"negated jump":      {"*nat\n:X - [0:0]\n-A X ! -j ACCEPT\n", "line 3: -j needs one name"},
		"source without":    {"*nat\n:X - [0:0]\n-A X -s -j ACCEPT\n", "line 3: -s needs one value"},
		"unexpected":        {"*nat\nhello\n", `line 2: unexpected line "hello"`},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Parse(strings.NewReader(tc.in))
			if err == nil || err.Error() != "netfilter: "+tc.want {
				t.Fatalf("err = %v, want netfilter: %s", err, tc.want)
			}
		})
	}
}

func TestParseIncompatibleNotes(t *testing.T) {
	in := "# Table `raw' is incompatible, use 'nft' tool.\n" +
		"# Table `filter' contains incompatible base-chains, use 'nft' tool to list them.\n" +
		"*filter\n:INPUT ACCEPT [0:0]\nCOMMIT\n" +
		"# Warning: iptables-legacy tables present, use iptables-legacy-save to see them\n"
	rs, err := Parse(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(rs.Tables) != 2 {
		t.Fatalf("tables = %+v", rs.Tables)
	}
	if f := rs.Table("filter"); f.Incompatible != "Table `filter' contains incompatible base-chains, use 'nft' tool to list them." || len(f.Chains) != 1 {
		t.Fatalf("filter = %+v", f)
	}
	if r := rs.Table("raw"); r.Incompatible != "Table `raw' is incompatible, use 'nft' tool." || r.Chains != nil {
		t.Fatalf("raw = %+v", r)
	}
}

func TestParseStructuralUnsupported(t *testing.T) {
	for _, tc := range []struct{ rule, want string }{
		{"-A X -g Y", "goto"},
		{"-A X -f -j DROP", "fragment match"},
		{"-A X -s 10.0.0.0/255.0.255.0 -j DROP", `address "10.0.0.0/255.0.255.0"`},
		{"-A X -s 10.0.0.1 -j DROP", `address "10.0.0.1"`},
		{"-A X -s 10.0.0.0/8 -s 10.1.0.0/16 -j DROP", "repeated -s"},
		{"-A X --dport 22 -j ACCEPT", "option --dport"},
		{"-A X -s 10.0.0.0/8 --dport 22 -j ACCEPT", "option --dport"},
	} {
		rs, err := Parse(strings.NewReader("*filter\n:X - [0:0]\n:Y - [0:0]\n" + tc.rule + "\nCOMMIT\n"))
		if err != nil {
			t.Fatalf("%s: %v", tc.rule, err)
		}
		r := rs.Tables[0].Chain("X").Rules[0]
		if !r.Opaque() || !reflect.DeepEqual(r.Unsupported, []string{tc.want}) {
			t.Errorf("%s: unsupported = %q, want [%q]", tc.rule, r.Unsupported, tc.want)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mkdir -p /root/.cache/w0b2-tmp/4 && TMPDIR=/root/.cache/w0b2-tmp/4 GOTOOLCHAIN=local go test ./internal/platform/netfilter/`
Expected: FAIL, build failed: `internal/platform/netfilter/parse_test.go:26:13: undefined: Parse`

- [ ] **Step 3: Write the implementation**

Create `internal/platform/netfilter/parse.go`:

```go
// Package netfilter parses iptables-save and ip6tables-save output (spec
// 1.8) into tables, chains and rules. A rule using a match, target or
// option outside the parser's contract is kept as an opaque rule: its
// Unsupported reasons are set and the evaluator (2.3) treats it as
// unsupported. Output that is not save output at all is an error.
package netfilter

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"slices"
	"strings"
)

// Tables are the tables the parser accepts, in the order the firewall
// module reads them.
var Tables = []string{"filter", "nat", "raw", "mangle", "security"}

// Ruleset is one save output.
type Ruleset struct {
	Tables []Table
}

// Table is one *<name> ... COMMIT block.
type Table struct {
	Name string
	// Incompatible is iptables-nft-save's note that the table holds
	// rules or base chains it cannot show ("# Table `filter' is
	// incompatible, use 'nft' tool."), "" when none. A table with only
	// the note has no chains.
	Incompatible string
	Chains       []Chain
}

// Chain is a declared chain with its rules in order.
type Chain struct {
	Name    string
	Builtin bool   // declared with a policy
	Policy  string // ACCEPT or DROP for a built-in chain, "" otherwise
	Rules   []Rule
}

// Rule is one -A line.
type Rule struct {
	Line   int    // 1-based line number in the input
	Text   string // the line as saved
	Source AddrMatch
	Dest   AddrMatch
	In     NameMatch // -i; a trailing "+" is iptables' wildcard, kept as saved
	Out    NameMatch // -o
	Proto  NameMatch // -p, as saved ("tcp", "ipv6-icmp", a number)
	// Matches are the -m modules in order, each with its options.
	Matches []Match
	// Target is the -j target; Name is "" for a rule without one.
	Target Target
	// Unsupported lists why the rule is opaque; empty for a rule fully
	// inside the contract.
	Unsupported []string
}

// Opaque reports whether the rule uses anything outside the contract.
func (r Rule) Opaque() bool { return len(r.Unsupported) > 0 }

// AddrMatch is -s or -d. Prefix is invalid (the zero value) when the rule
// has no such option.
type AddrMatch struct {
	Prefix netip.Prefix
	Neg    bool
}

// NameMatch is -i, -o or -p. Name is "" when the rule has no such option.
type NameMatch struct {
	Name string
	Neg  bool
}

// Match is one -m module.
type Match struct {
	Module  string
	Options []Option
}

// Target is the -j target. Chain reports a jump to a chain declared in
// the same table.
type Target struct {
	Name    string
	Chain   bool
	Options []Option
}

// Option is one --name option with its values, without the dashes.
type Option struct {
	Name   string
	Neg    bool
	Values []string
}

// Table returns the named table, or nil.
func (rs *Ruleset) Table(name string) *Table {
	for i := range rs.Tables {
		if rs.Tables[i].Name == name {
			return &rs.Tables[i]
		}
	}
	return nil
}

// Chain returns the named chain, or nil.
func (t *Table) Chain(name string) *Chain {
	for i := range t.Chains {
		if t.Chains[i].Name == name {
			return &t.Chains[i]
		}
	}
	return nil
}

// maxLine bounds one line; iptables limits a rule far below it.
const maxLine = 1 << 20

// Parse reads save output. Comment lines are skipped except the
// incompatible-table notes, which are attached to their table.
func Parse(r io.Reader) (*Ruleset, error) {
	p := parser{notes: map[string]string{}}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), maxLine)
	for sc.Scan() {
		p.line++
		if err := p.parseLine(strings.TrimRight(sc.Text(), " \t\r")); err != nil {
			return nil, fmt.Errorf("netfilter: line %d: %w", p.line, err)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("netfilter: %w", err)
	}
	if p.table != nil {
		return nil, fmt.Errorf("netfilter: table %s has no COMMIT", p.table.Name)
	}
	for _, name := range Tables {
		if note, ok := p.notes[name]; ok {
			p.rs.Tables = append(p.rs.Tables, Table{Name: name, Incompatible: note})
		}
	}
	return &p.rs, nil
}

type parser struct {
	rs    Ruleset
	table *Table
	line  int
	notes map[string]string // incompatible notes of tables not yet opened
}

func (p *parser) parseLine(line string) error {
	switch {
	case line == "":
		return nil
	case strings.HasPrefix(line, "#"):
		p.note(line)
		return nil
	case strings.HasPrefix(line, "*"):
		return p.openTable(line[1:])
	case line == "COMMIT":
		if p.table == nil {
			return errors.New("COMMIT outside a table")
		}
		p.rs.Tables = append(p.rs.Tables, *p.table)
		p.table = nil
		return nil
	case strings.HasPrefix(line, ":"):
		return p.declareChain(line[1:])
	case strings.HasPrefix(line, "-A "):
		return p.addRule(line)
	}
	return fmt.Errorf("unexpected line %q", truncate(line))
}

// note records iptables-nft-save's incompatible-table comments.
func (p *parser) note(line string) {
	name, ok := strings.CutPrefix(line, "# Table `")
	if !ok {
		return
	}
	name, rest, ok := strings.Cut(name, "'")
	if !ok || !strings.HasPrefix(rest, " is incompatible") && !strings.HasPrefix(rest, " contains incompatible") {
		return
	}
	p.notes[name] = strings.TrimPrefix(line, "# ")
}

func (p *parser) openTable(name string) error {
	if p.table != nil {
		return fmt.Errorf("table %s opened inside table %s", name, p.table.Name)
	}
	if !slices.Contains(Tables, name) {
		return fmt.Errorf("unknown table %q", truncate(name))
	}
	if p.rs.Table(name) != nil {
		return fmt.Errorf("table %s appears twice", name)
	}
	p.table = &Table{Name: name, Incompatible: p.notes[name]}
	delete(p.notes, name)
	return nil
}

func (p *parser) declareChain(decl string) error {
	if p.table == nil {
		return errors.New("chain declared outside a table")
	}
	f := strings.Fields(decl)
	if len(f) < 2 || len(f) > 3 {
		return fmt.Errorf("malformed chain declaration %q", truncate(decl))
	}
	if p.table.Chain(f[0]) != nil {
		return fmt.Errorf("chain %s declared twice", f[0])
	}
	c := Chain{Name: f[0]}
	switch f[1] {
	case "-":
	case "ACCEPT", "DROP":
		c.Builtin, c.Policy = true, f[1]
	default:
		return fmt.Errorf("chain %s has policy %q", f[0], truncate(f[1]))
	}
	p.table.Chains = append(p.table.Chains, c)
	return nil
}

func (p *parser) addRule(line string) error {
	if p.table == nil {
		return errors.New("rule outside a table")
	}
	toks, err := tokenize(line)
	if err != nil {
		return err
	}
	if len(toks) < 2 || toks[1].quoted {
		return errors.New("-A without a chain")
	}
	c := p.table.Chain(toks[1].text)
	if c == nil {
		return fmt.Errorf("rule for undeclared chain %q", truncate(toks[1].text))
	}
	rule, err := parseRule(toks[2:])
	if err != nil {
		return err
	}
	rule.Line, rule.Text = p.line, line
	if rule.Target.Name != "" && p.table.Chain(rule.Target.Name) != nil {
		rule.Target.Chain = true
	}
	c.Rules = append(c.Rules, rule)
	return nil
}

type token struct {
	text   string
	quoted bool
}

// tokenize splits a line as iptables-restore does: whitespace separates
// tokens, and a double-quoted token may hold spaces with \" \\ and \'
// escaped. A quoted token is always a value.
func tokenize(line string) ([]token, error) {
	var out []token
	for i := 0; i < len(line); {
		switch line[i] {
		case ' ', '\t':
			i++
		case '"':
			var b strings.Builder
			i++
			for {
				if i >= len(line) {
					return nil, errors.New("unterminated quote")
				}
				if line[i] == '"' {
					i++
					break
				}
				if line[i] == '\\' && i+1 < len(line) {
					i++
				}
				b.WriteByte(line[i])
				i++
			}
			out = append(out, token{text: b.String(), quoted: true})
		default:
			j := i
			for j < len(line) && line[j] != ' ' && line[j] != '\t' {
				j++
			}
			out = append(out, token{text: line[i:j]})
			i = j
		}
	}
	return out, nil
}

// optionName returns a token's option name without dashes, or "" when the
// token is a value.
func optionName(t token) string {
	if t.quoted || len(t.text) < 2 || t.text[0] != '-' {
		return ""
	}
	return strings.TrimLeft(t.text, "-")
}

// topLevel maps every spelling of a top-level option to its short form.
// These are never a module's or target's option, wherever they appear.
var topLevel = map[string]string{
	"s": "s", "source": "s", "d": "d", "destination": "d",
	"i": "i", "in-interface": "i", "o": "o", "out-interface": "o",
	"p": "p", "protocol": "p", "m": "m", "match": "m",
	"j": "j", "jump": "j", "g": "g", "goto": "g", "f": "f", "fragment": "f",
}

// parseRule reads the tokens after "-A <chain>". Options of a -m module
// or the -j target follow it until the next top-level option.
func parseRule(toks []token) (Rule, error) {
	var r Rule
	var cur *[]Option // the module's or target's options being read
	seen := map[string]bool{}
	for i := 0; i < len(toks); {
		neg := false
		if !toks[i].quoted && toks[i].text == "!" {
			neg = true
			i++
			if i == len(toks) || optionName(toks[i]) == "" {
				return Rule{}, errors.New("! not followed by an option")
			}
		}
		name := optionName(toks[i])
		if name == "" {
			return Rule{}, fmt.Errorf("value %q without an option", truncate(toks[i].text))
		}
		i++
		var vals []string
		for i < len(toks) && optionName(toks[i]) == "" && (toks[i].quoted || toks[i].text != "!") {
			vals = append(vals, toks[i].text)
			i++
		}
		short, top := topLevel[name]
		if !top {
			if cur == nil {
				r.Unsupported = append(r.Unsupported, "option --"+name)
				continue
			}
			*cur = append(*cur, Option{Name: name, Neg: neg, Values: vals})
			continue
		}
		if short != "m" && seen[short] {
			r.Unsupported = append(r.Unsupported, "repeated -"+short)
		}
		seen[short] = true
		switch short {
		case "m", "j":
			if len(vals) != 1 || neg {
				return Rule{}, fmt.Errorf("-%s needs one name", short)
			}
			if short == "m" {
				r.Matches = append(r.Matches, Match{Module: vals[0]})
				cur = &r.Matches[len(r.Matches)-1].Options
			} else {
				r.Target.Name = vals[0]
				cur = &r.Target.Options
			}
			continue
		case "g", "f":
			r.Unsupported = append(r.Unsupported, map[string]string{"g": "goto", "f": "fragment match"}[short])
			cur = nil
			continue
		}
		if len(vals) != 1 {
			return Rule{}, fmt.Errorf("-%s needs one value", short)
		}
		cur = nil
		switch short {
		case "s", "d":
			pfx, err := netip.ParsePrefix(vals[0])
			if err != nil {
				r.Unsupported = append(r.Unsupported, fmt.Sprintf("address %q", truncate(vals[0])))
				continue
			}
			am := AddrMatch{Prefix: pfx, Neg: neg}
			if short == "s" {
				r.Source = am
			} else {
				r.Dest = am
			}
		case "i":
			r.In = NameMatch{Name: vals[0], Neg: neg}
		case "o":
			r.Out = NameMatch{Name: vals[0], Neg: neg}
		case "p":
			r.Proto = NameMatch{Name: vals[0], Neg: neg}
		}
	}
	return r, nil
}

func truncate(s string) string {
	if len(s) > 64 {
		return s[:64] + "..."
	}
	return s
}
```

- [ ] **Step 4: Run the tests and lint**

Run: `TMPDIR=/root/.cache/w0b2-tmp/4 GOTOOLCHAIN=local go test -count=1 ./internal/platform/netfilter/ && TMPDIR=/root/.cache/w0b2-tmp/4 golangci-lint run --allow-serial-runners ./internal/platform/netfilter/...`
Expected: `ok  	github.com/darkraise/darkpanel/internal/platform/netfilter`, then `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add internal/platform/netfilter/parse.go internal/platform/netfilter/parse_test.go
git commit -m "feat(platform): parse netfilter save output"
rm -rf /root/.cache/w0b2-tmp/4
```

### Task 5: Docker containers

Container list, inspect, create, start, stop, restart and remove, with the types the modules read. Start and stop report whether they changed anything (the daemon answers 304 when the container is already in that state), which an undo needs. Test constants carry the prefix `ct`.

**Files:**
- Create: `internal/platform/dockerapi/containers.go`
- Test: `internal/platform/dockerapi/containers_test.go`

**Interfaces:**
- Consumes: C1 (`request`, `call`, `Filters.set`, `newFakeDaemon`, `IsNotFound`, `IsConflict`).
- Produces: C2.

**Items:** 79

**Depends on:** Task 1

**Implementer:** dforge:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 0 = 3

- [ ] **Step 1: Write the failing test**

Create `internal/platform/dockerapi/containers_test.go`:

```go
package dockerapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/netip"
	"reflect"
	"testing"
	"time"
)

const ctInspectJSON = `{
  "Id": "4fa6e0f0c678",
  "Created": "2026-10-23T09:15:02.123456789Z",
  "Name": "/web",
  "Image": "sha256:aaaa",
  "RestartCount": 1,
  "State": {"Status": "running", "Running": true, "Pid": 4242, "ExitCode": 0,
    "StartedAt": "2026-10-23T09:15:03.5Z", "FinishedAt": "0001-01-01T00:00:00Z"},
  "Config": {"Hostname": "4fa6e0f0c678", "Env": ["A=1", "SECRET=x"], "Cmd": ["nginx", "-g", "daemon off;"],
    "Image": "nginx:1.27", "Labels": {"com.docker.compose.project": "site"}, "ExposedPorts": {"80/tcp": {}}, "Tty": false},
  "HostConfig": {"NetworkMode": "site_default", "PortBindings": {"80/tcp": [{"HostIp": "", "HostPort": "8080"}]},
    "RestartPolicy": {"Name": "unless-stopped", "MaximumRetryCount": 0}},
  "NetworkSettings": {
    "Ports": {"80/tcp": [{"HostIp": "0.0.0.0", "HostPort": "8080"}, {"HostIp": "::", "HostPort": "8080"}]},
    "Networks": {"site_default": {"IPAMConfig": null, "Aliases": ["web"], "NetworkID": "net1", "EndpointID": "ep1",
      "Gateway": "172.20.0.1", "IPAddress": "172.20.0.2", "IPPrefixLen": 16, "IPv6Gateway": "",
      "GlobalIPv6Address": "", "GlobalIPv6PrefixLen": 0, "MacAddress": "02:42:ac:14:00:02", "DNSNames": ["web", "4fa6e0f0c678"]}}
  },
  "Mounts": [{"Type": "volume", "Name": "site_data", "Source": "/var/lib/docker/volumes/site_data/_data", "Destination": "/data", "RW": true}]
}`

func TestInspectContainer(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1.49/containers/{id}/json", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") != "web" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"message":"No such container: `+r.PathValue("id")+`"}`) //nolint:gosec // G705: a test fake answering JSON
			return
		}
		_, _ = io.WriteString(w, ctInspectJSON)
	})
	c := newFakeDaemon(t, "1.49", mux)
	got, err := c.InspectContainer(t.Context(), "web")
	if err != nil {
		t.Fatal(err)
	}
	ep := got.NetworkSettings.Networks["site_default"]
	if got.ID != "4fa6e0f0c678" || got.Created != "2026-10-23T09:15:02.123456789Z" || !got.State.Running ||
		got.State.StartedAt != "2026-10-23T09:15:03.5Z" || got.HostConfig.RestartPolicy.Name != "unless-stopped" ||
		got.Config.Labels["com.docker.compose.project"] != "site" || got.Mounts[0].Name != "site_data" {
		t.Fatalf("inspect = %+v", got)
	}
	if ep == nil || ep.IPAddress != netip.MustParseAddr("172.20.0.2") || ep.GlobalIPv6Address.IsValid() || ep.IPAMConfig != nil {
		t.Fatalf("endpoint = %+v", ep)
	}
	if b := got.NetworkSettings.Ports["80/tcp"]; len(b) != 2 || b[1].HostIP != "::" || b[1].HostPort != "8080" {
		t.Fatalf("ports = %+v", got.NetworkSettings.Ports)
	}
	if _, err := c.InspectContainer(t.Context(), "gone"); !IsNotFound(err) {
		t.Fatalf("missing container err = %v", err)
	}
}

func TestListContainers(t *testing.T) {
	mux := http.NewServeMux()
	var query string
	mux.HandleFunc("GET /v1.49/containers/json", func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		_, _ = io.WriteString(w, `[{"Id":"c1","Names":["/web"],"Image":"nginx","ImageID":"sha256:aaaa","Created":1761210902,`+
			`"State":"running","Status":"Up 2 minutes","Ports":[{"IP":"0.0.0.0","PrivatePort":80,"PublicPort":8080,"Type":"tcp"},{"PrivatePort":443,"Type":"tcp"}],`+
			`"Labels":{"darkpanel.module":"npm"},"NetworkSettings":{"Networks":{"bridge":{"IPAddress":"172.17.0.2"}}}}]`)
	})
	c := newFakeDaemon(t, "1.49", mux)
	got, err := c.ListContainers(t.Context(), ListContainersOptions{All: true, Filters: Filters{"label": {"darkpanel.module"}}})
	if err != nil {
		t.Fatal(err)
	}
	if query != "all=1&filters=%7B%22label%22%3A%5B%22darkpanel.module%22%5D%7D" {
		t.Fatalf("query = %s", query)
	}
	if len(got) != 1 || got[0].Names[0] != "/web" || got[0].Ports[0].PublicPort != 8080 || got[0].Ports[1].IP != "" ||
		got[0].NetworkSettings.Networks["bridge"].IPAddress != netip.MustParseAddr("172.17.0.2") {
		t.Fatalf("list = %+v", got)
	}
}

func TestCreateContainerBody(t *testing.T) {
	mux := http.NewServeMux()
	var name string
	var body map[string]any
	mux.HandleFunc("POST /v1.49/containers/create", func(w http.ResponseWriter, r *http.Request) {
		name = r.URL.Query().Get("name")
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("content type %q", r.Header.Get("Content-Type"))
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"Id":"new1","Warnings":[]}`)
	})
	c := newFakeDaemon(t, "1.49", mux)
	id, err := c.CreateContainer(t.Context(), ContainerCreate{
		Name: "darkpanel-npm",
		ContainerConfig: ContainerConfig{
			Image:  "jc21/nginx-proxy-manager@sha256:bbbb",
			Labels: map[string]string{"darkpanel.module": "npm"},
		},
		HostConfig: &HostConfig{
			PortBindings:  map[string][]PortBinding{"81/tcp": {{HostIP: "127.0.0.1", HostPort: "81"}}},
			RestartPolicy: RestartPolicy{Name: "unless-stopped"},
			Mounts:        []Mount{{Type: "volume", Source: "darkpanel-npm-data", Target: "/data"}},
		},
		NetworkingConfig: &NetworkingConfig{EndpointsConfig: map[string]*EndpointSettings{
			"darkpanel-npm": {IPAMConfig: &EndpointIPAMConfig{IPv4Address: netip.MustParseAddr("10.9.0.2")}},
		}},
	})
	if err != nil || id != "new1" || name != "darkpanel-npm" {
		t.Fatalf("create = %q, %v (name %q)", id, err, name)
	}
	want := map[string]any{
		"Image":  "jc21/nginx-proxy-manager@sha256:bbbb",
		"Labels": map[string]any{"darkpanel.module": "npm"},
		"HostConfig": map[string]any{
			"PortBindings":  map[string]any{"81/tcp": []any{map[string]any{"HostIp": "127.0.0.1", "HostPort": "81"}}},
			"RestartPolicy": map[string]any{"Name": "unless-stopped"},
			"Mounts":        []any{map[string]any{"Type": "volume", "Source": "darkpanel-npm-data", "Target": "/data"}},
		},
		"NetworkingConfig": map[string]any{"EndpointsConfig": map[string]any{
			"darkpanel-npm": map[string]any{"IPAMConfig": map[string]any{"IPv4Address": "10.9.0.2"}},
		}},
	}
	if !reflect.DeepEqual(body, want) {
		t.Fatalf("body = %#v", body)
	}
}

func TestStartStopRestartRemove(t *testing.T) {
	mux := http.NewServeMux()
	var seen []string
	mux.HandleFunc("POST /v1.49/containers/{id}/{op}", func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.PathValue("op")+" "+r.PathValue("id")+" "+r.URL.RawQuery)
		if r.PathValue("id") == "already" {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("DELETE /v1.49/containers/{id}", func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, "delete "+r.PathValue("id")+" "+r.URL.RawQuery)
		if r.PathValue("id") == "busy" {
			w.WriteHeader(http.StatusConflict)
			_, _ = io.WriteString(w, `{"message":"container is running"}`)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	c := newFakeDaemon(t, "1.49", mux)
	ctx := t.Context()
	if changed, err := c.StartContainer(ctx, "web"); err != nil || !changed {
		t.Fatalf("start = %v, %v", changed, err)
	}
	if changed, err := c.StartContainer(ctx, "already"); err != nil || changed {
		t.Fatalf("start of a running container = %v, %v", changed, err)
	}
	if changed, err := c.StopContainer(ctx, "web", 10*time.Second); err != nil || !changed {
		t.Fatalf("stop = %v, %v", changed, err)
	}
	if changed, err := c.StopContainer(ctx, "already", 0); err != nil || changed {
		t.Fatalf("stop of a stopped container = %v, %v", changed, err)
	}
	if err := c.RestartContainer(ctx, "web", 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := c.RemoveContainer(ctx, "web", RemoveContainerOptions{Force: true, Volumes: true}); err != nil {
		t.Fatal(err)
	}
	if err := c.RemoveContainer(ctx, "busy", RemoveContainerOptions{}); !IsConflict(err) {
		t.Fatalf("remove of a running container err = %v", err)
	}
	want := []string{
		"start web ", "start already ", "stop web t=10", "stop already t=0", "restart web t=5",
		"delete web force=true&v=true", "delete busy force=false&v=false",
	}
	if !reflect.DeepEqual(seen, want) {
		t.Fatalf("requests = %q", seen)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mkdir -p /root/.cache/w0b2-tmp/5 && TMPDIR=/root/.cache/w0b2-tmp/5 GOTOOLCHAIN=local go test ./internal/platform/dockerapi/`
Expected: FAIL, build failed: `internal/platform/dockerapi/containers_test.go:45:16: c.InspectContainer undefined (type *Client has no field or method InspectContainer)`

- [ ] **Step 3: Write the implementation**

Create `internal/platform/dockerapi/containers.go`:

```go
package dockerapi

import (
	"context"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"time"
)

// ContainerSummary is one entry of GET /containers/json.
type ContainerSummary struct {
	ID              string `json:"Id"`
	Names           []string
	Image           string
	ImageID         string
	Command         string
	Created         int64 // Unix seconds
	State           string
	Status          string
	Ports           []PortSummary
	Labels          map[string]string
	NetworkSettings *SummaryNetworkSettings
	Mounts          []MountPoint
}

// PortSummary is a published or exposed port of a container summary. IP is
// "" for a port that is exposed but not published.
type PortSummary struct {
	IP          string
	PrivatePort uint16
	PublicPort  uint16
	Type        string
}

// SummaryNetworkSettings is a container summary's networks.
type SummaryNetworkSettings struct {
	Networks map[string]*EndpointSettings
}

// ContainerJSON is GET /containers/{id}/json.
type ContainerJSON struct {
	ID              string `json:"Id"`
	Created         string // RFC 3339 with nanoseconds
	Name            string
	Image           string // the image ID
	RestartCount    int
	State           ContainerState
	Config          ContainerConfig
	HostConfig      HostConfig
	NetworkSettings NetworkSettings
	Mounts          []MountPoint
}

// ContainerState is a container's run state. StartedAt and FinishedAt are
// RFC 3339 with nanoseconds; a container that never ran has the zero time.
type ContainerState struct {
	Status     string
	Running    bool
	Paused     bool
	Restarting bool
	OOMKilled  bool
	Dead       bool
	Pid        int
	ExitCode   int
	Error      string
	StartedAt  string
	FinishedAt string
}

// ContainerConfig is the portable configuration of a container.
type ContainerConfig struct {
	Hostname     string              `json:",omitempty"`
	User         string              `json:",omitempty"`
	Env          []string            `json:",omitempty"`
	Cmd          []string            `json:",omitempty"`
	Entrypoint   []string            `json:",omitempty"`
	Image        string              `json:",omitempty"`
	WorkingDir   string              `json:",omitempty"`
	Labels       map[string]string   `json:",omitempty"`
	ExposedPorts map[string]struct{} `json:",omitempty"`
	Tty          bool                `json:",omitempty"`
}

// HostConfig is the host-dependent configuration of a container. Ports are
// keyed "<port>/<proto>", for example "80/tcp".
type HostConfig struct {
	Binds          []string                 `json:",omitempty"`
	NetworkMode    string                   `json:",omitempty"`
	PortBindings   map[string][]PortBinding `json:",omitempty"`
	RestartPolicy  RestartPolicy            `json:",omitzero"`
	Mounts         []Mount                  `json:",omitempty"`
	ReadonlyRootfs bool                     `json:",omitempty"`
	Privileged     bool                     `json:",omitempty"`
	CapAdd         []string                 `json:",omitempty"`
	CapDrop        []string                 `json:",omitempty"`
	SecurityOpt    []string                 `json:",omitempty"`
}

// PortBinding is one host side of a published port. HostIP is "" or an
// unspecified address for every host address.
type PortBinding struct {
	HostIP   string `json:"HostIp"`
	HostPort string
}

// RestartPolicy is a container's restart policy ("no", "always",
// "unless-stopped", "on-failure").
type RestartPolicy struct {
	Name              string
	MaximumRetryCount int `json:",omitempty"`
}

// Mount is a mount requested at creation.
type Mount struct {
	Type     string // bind | volume | tmpfs
	Source   string `json:",omitempty"`
	Target   string
	ReadOnly bool `json:",omitempty"`
}

// MountPoint is a mount as a container reports it.
type MountPoint struct {
	Type        string
	Name        string
	Source      string
	Destination string
	RW          bool
}

// NetworkSettings is a container's published ports and network endpoints.
type NetworkSettings struct {
	Ports    map[string][]PortBinding
	Networks map[string]*EndpointSettings
}

// EndpointSettings is a container's endpoint on one network, as inspect
// reports it and as create and connect requests carry it.
type EndpointSettings struct {
	IPAMConfig          *EndpointIPAMConfig `json:",omitempty"`
	Aliases             []string            `json:",omitempty"`
	NetworkID           string              `json:",omitempty"`
	EndpointID          string              `json:",omitempty"`
	Gateway             netip.Addr          `json:",omitzero"`
	IPAddress           netip.Addr          `json:",omitzero"`
	IPPrefixLen         int                 `json:",omitempty"`
	IPv6Gateway         netip.Addr          `json:",omitzero"`
	GlobalIPv6Address   netip.Addr          `json:",omitzero"`
	GlobalIPv6PrefixLen int                 `json:",omitempty"`
	MacAddress          string              `json:",omitempty"`
	DNSNames            []string            `json:",omitempty"`
}

// EndpointIPAMConfig is an endpoint's static addresses.
type EndpointIPAMConfig struct {
	IPv4Address netip.Addr `json:",omitzero"`
	IPv6Address netip.Addr `json:",omitzero"`
}

// ContainerCreate is a POST /containers/create request; Name is the
// container's name, sent in the query.
type ContainerCreate struct {
	Name string `json:"-"`
	ContainerConfig
	HostConfig       *HostConfig       `json:",omitempty"`
	NetworkingConfig *NetworkingConfig `json:",omitempty"`
}

// NetworkingConfig names the networks a container is created on.
type NetworkingConfig struct {
	EndpointsConfig map[string]*EndpointSettings
}

// ListContainersOptions selects the containers GET /containers/json lists.
type ListContainersOptions struct {
	All     bool // include stopped containers
	Filters Filters
}

// ListContainers lists containers.
func (c *Client) ListContainers(ctx context.Context, opts ListContainersOptions) ([]ContainerSummary, error) {
	q := url.Values{}
	if opts.All {
		q.Set("all", "1")
	}
	if err := opts.Filters.set(q); err != nil {
		return nil, err
	}
	var out []ContainerSummary
	_, err := c.call(ctx, request{method: http.MethodGet, path: "/containers/json", query: q}, &out)
	return out, err
}

// InspectContainer inspects a container by ID or name.
func (c *Client) InspectContainer(ctx context.Context, id string) (ContainerJSON, error) {
	var out ContainerJSON
	_, err := c.call(ctx, request{method: http.MethodGet, path: "/containers/" + url.PathEscape(id) + "/json"}, &out)
	return out, err
}

// CreateContainer creates a container and returns its ID.
func (c *Client) CreateContainer(ctx context.Context, cfg ContainerCreate) (string, error) {
	q := url.Values{}
	if cfg.Name != "" {
		q.Set("name", cfg.Name)
	}
	var out struct {
		ID string `json:"Id"`
	}
	_, err := c.call(ctx, request{method: http.MethodPost, path: "/containers/create", query: q, body: cfg}, &out)
	return out.ID, err
}

// StartContainer starts a container. It reports false when the container
// was already running (the daemon's 304).
func (c *Client) StartContainer(ctx context.Context, id string) (bool, error) {
	code, err := c.call(ctx, request{method: http.MethodPost, path: "/containers/" + url.PathEscape(id) + "/start"}, nil)
	return code != http.StatusNotModified, err
}

// StopContainer stops a container, killing it after timeout. It reports
// false when the container was already stopped (the daemon's 304).
func (c *Client) StopContainer(ctx context.Context, id string, timeout time.Duration) (bool, error) {
	q := url.Values{"t": {strconv.Itoa(int(timeout / time.Second))}}
	code, err := c.call(ctx, request{method: http.MethodPost, path: "/containers/" + url.PathEscape(id) + "/stop", query: q}, nil)
	return code != http.StatusNotModified, err
}

// RestartContainer restarts a container, killing it after timeout if it
// does not stop.
func (c *Client) RestartContainer(ctx context.Context, id string, timeout time.Duration) error {
	q := url.Values{"t": {strconv.Itoa(int(timeout / time.Second))}}
	_, err := c.call(ctx, request{method: http.MethodPost, path: "/containers/" + url.PathEscape(id) + "/restart", query: q}, nil)
	return err
}

// RemoveContainerOptions modifies DELETE /containers/{id}.
type RemoveContainerOptions struct {
	Force   bool // kill a running container first
	Volumes bool // also remove its anonymous volumes
}

// RemoveContainer removes a container.
func (c *Client) RemoveContainer(ctx context.Context, id string, opts RemoveContainerOptions) error {
	q := url.Values{"force": {strconv.FormatBool(opts.Force)}, "v": {strconv.FormatBool(opts.Volumes)}}
	_, err := c.call(ctx, request{method: http.MethodDelete, path: "/containers/" + url.PathEscape(id), query: q}, nil)
	return err
}
```

- [ ] **Step 4: Run the tests and lint**

Run: `TMPDIR=/root/.cache/w0b2-tmp/5 GOTOOLCHAIN=local go test -count=1 ./internal/platform/dockerapi/ && TMPDIR=/root/.cache/w0b2-tmp/5 golangci-lint run --allow-serial-runners ./internal/platform/dockerapi/...`
Expected: `ok  	github.com/darkraise/darkpanel/internal/platform/dockerapi`, then `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add internal/platform/dockerapi/containers.go internal/platform/dockerapi/containers_test.go
git commit -m "feat(platform): add Docker container calls"
rm -rf /root/.cache/w0b2-tmp/5
```

### Task 6: Docker logs, stats, events and archives

The streaming calls: container logs demultiplexed by the response's content type (8-byte frame headers without a TTY, raw bytes with one), one or many stats samples, the daemon's event stream with filters, and tar archive copy out of and into a container. Test helpers carry the prefix `st`.

**Files:**
- Create: `internal/platform/dockerapi/streams.go`
- Test: `internal/platform/dockerapi/streams_test.go`

**Interfaces:**
- Consumes: C1 (`request`, `do`, `call`, `Filters.set`, `newStream`, `Stream`, `newFakeDaemon`, `IsNotFound`).
- Produces: C3 (`streams.go` part).

**Items:** 79

**Depends on:** Task 1

**Implementer:** dforge:impl-sonnet-low
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 0 = 2

- [ ] **Step 1: Write the failing test**

Create `internal/platform/dockerapi/streams_test.go`:

```go
package dockerapi

import (
	"archive/tar"
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// stFrame encodes one multiplexed log frame.
func stFrame(stream byte, data string) []byte {
	hdr := make([]byte, 8, 8+len(data))
	hdr[0] = stream
	binary.BigEndian.PutUint32(hdr[4:], uint32(len(data))) //nolint:gosec // G115: test frames are a few bytes
	return append(hdr, data...)
}

func stReadAll(t *testing.T, s *LogStream) []LogFrame {
	t.Helper()
	var out []LogFrame
	for {
		f, err := s.Next()
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, f)
	}
}

func TestLogsMultiplexed(t *testing.T) {
	mux := http.NewServeMux()
	var query string
	mux.HandleFunc("GET /v1.49/containers/{id}/logs", func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/vnd.docker.multiplexed-stream")
		_, _ = w.Write(append(stFrame(1, "out line\n"), stFrame(2, "err line\n")...))
	})
	c := newFakeDaemon(t, "1.49", mux)
	s, err := c.Logs(t.Context(), "web", LogsOptions{
		Follow: true, Stdout: true, Stderr: true, Tail: "100",
		Since: time.Unix(1761210902, 5000).UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	frames := stReadAll(t, s)
	if len(frames) != 2 || frames[0].Stream != "stdout" || string(frames[0].Data) != "out line\n" ||
		frames[1].Stream != "stderr" || string(frames[1].Data) != "err line\n" {
		t.Fatalf("frames = %+v", frames)
	}
	if query != "follow=true&since=1761210902.000005000&stderr=true&stdout=true&tail=100&timestamps=false" {
		t.Fatalf("query = %s", query)
	}
}

func TestLogsRawForTTY(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1.49/containers/{id}/logs", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.docker.raw-stream")
		_, _ = io.WriteString(w, "plain tty output\n")
	})
	s, err := newFakeDaemon(t, "1.49", mux).Logs(t.Context(), "tty", LogsOptions{Stdout: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	var b strings.Builder
	for _, f := range stReadAll(t, s) {
		if f.Stream != "stdout" {
			t.Fatalf("raw frame stream = %q", f.Stream)
		}
		b.Write(f.Data)
	}
	if b.String() != "plain tty output\n" {
		t.Fatalf("output = %q", b.String())
	}
}

func TestLogsMalformedFrames(t *testing.T) {
	big := make([]byte, 8)
	big[0] = 1
	binary.BigEndian.PutUint32(big[4:], maxFrame+1)
	cases := map[string]struct {
		body []byte
		want string
	}{
		"unknown stream":  {stFrame(7, "x"), "unknown stream 7"},
		"oversized frame": {big, "exceeds"},
		"short header":    {[]byte{1, 0, 0}, "truncated frame header"},
		"short payload":   {stFrame(1, "abcdef")[:10], "truncated frame"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("GET /v1.49/containers/{id}/logs", func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/vnd.docker.multiplexed-stream")
				_, _ = w.Write(tc.body)
			})
			s, err := newFakeDaemon(t, "1.49", mux).Logs(t.Context(), "web", LogsOptions{Stdout: true})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = s.Close() }()
			if _, err := s.Next(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestStatsOneSample(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1.49/containers/{id}/stats", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "stream=false" {
			t.Errorf("query = %s", r.URL.RawQuery)
		}
		_, _ = io.WriteString(w, `{"read":"2026-10-23T10:00:01Z","preread":"2026-10-23T10:00:00Z",`+
			`"cpu_stats":{"cpu_usage":{"total_usage":2000},"system_cpu_usage":100000,"online_cpus":4},`+
			`"precpu_stats":{"cpu_usage":{"total_usage":1000},"system_cpu_usage":90000,"online_cpus":4},`+
			`"memory_stats":{"usage":5242880,"limit":1073741824,"stats":{"inactive_file":1048576}},`+
			`"pids_stats":{"current":3},"networks":{"eth0":{"rx_bytes":10,"tx_bytes":20}}}`)
	})
	s, err := newFakeDaemon(t, "1.49", mux).Stats(t.Context(), "web", false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	st, err := s.Next()
	if err != nil {
		t.Fatal(err)
	}
	if st.CPUStats.CPUUsage.TotalUsage != 2000 || st.PreCPUStats.SystemCPUUsage != 90000 || st.CPUStats.OnlineCPUs != 4 ||
		st.MemoryStats.Stats["inactive_file"] != 1048576 || st.PidsStats.Current != 3 || st.Networks["eth0"].TxBytes != 20 {
		t.Fatalf("stats = %+v", st)
	}
	if _, err := s.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("second sample err = %v, want io.EOF", err)
	}
}

func TestEvents(t *testing.T) {
	mux := http.NewServeMux()
	var query string
	mux.HandleFunc("GET /v1.49/events", func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		_, _ = io.WriteString(w, `{"status":"start","id":"c1","Type":"container","Action":"start",`+
			`"Actor":{"ID":"c1","Attributes":{"name":"web","darkpanel.module":"npm"}},"scope":"local","time":1761210902,"timeNano":1761210902000000001}`+"\n")
	})
	s, err := newFakeDaemon(t, "1.49", mux).Events(t.Context(), EventsOptions{
		Since:   time.Unix(1761210900, 0),
		Filters: Filters{"type": {"container", "network"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	ev, err := s.Next()
	if err != nil {
		t.Fatal(err)
	}
	if ev.Type != "container" || ev.Action != "start" || ev.Actor.Attributes["name"] != "web" || ev.TimeNano != 1761210902000000001 {
		t.Fatalf("event = %+v", ev)
	}
	if query != "filters=%7B%22type%22%3A%5B%22container%22%2C%22network%22%5D%7D&since=1761210900.000000000" {
		t.Fatalf("query = %s", query)
	}
}

func TestArchiveCopy(t *testing.T) {
	var tarball bytes.Buffer
	tw := tar.NewWriter(&tarball)
	if err := tw.WriteHeader(&tar.Header{Name: "hello", Mode: 0o644, Size: 5}); err != nil {
		t.Fatal(err)
	}
	_, _ = tw.Write([]byte("world"))
	_ = tw.Close()

	mux := http.NewServeMux()
	var put []byte
	mux.HandleFunc("PUT /v1.49/containers/{id}/archive", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "noOverwriteDirNonDir=true&path=%2Fdata" || r.Header.Get("Content-Type") != "application/x-tar" {
			t.Errorf("query %s, content type %s", r.URL.RawQuery, r.Header.Get("Content-Type"))
		}
		put, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /v1.49/containers/{id}/archive", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("path") != "/data/hello" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"message":"Could not find the file"}`)
			return
		}
		w.Header().Set("Content-Type", "application/x-tar")
		_, _ = w.Write(put)
	})
	c := newFakeDaemon(t, "1.49", mux)
	ctx := t.Context()
	if err := c.CopyToContainer(ctx, "npm", "/data", bytes.NewReader(tarball.Bytes())); err != nil {
		t.Fatal(err)
	}
	rc, err := c.CopyFromContainer(ctx, "npm", "/data/hello")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rc.Close() }()
	tr := tar.NewReader(rc)
	hdr, err := tr.Next()
	if err != nil || hdr.Name != "hello" {
		t.Fatalf("archive entry = %+v, %v", hdr, err)
	}
	if b, _ := io.ReadAll(tr); string(b) != "world" {
		t.Fatalf("content = %q", b)
	}
	if _, err := c.CopyFromContainer(ctx, "npm", "/missing"); !IsNotFound(err) {
		t.Fatalf("missing path err = %v", err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mkdir -p /root/.cache/w0b2-tmp/6 && TMPDIR=/root/.cache/w0b2-tmp/6 GOTOOLCHAIN=local go test ./internal/platform/dockerapi/`
Expected: FAIL, build failed: `internal/platform/dockerapi/streams_test.go:23:33: undefined: LogStream`

- [ ] **Step 3: Write the implementation**

Create `internal/platform/dockerapi/streams.go`:

```go
package dockerapi

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// maxFrame bounds one multiplexed log frame; the daemon splits long lines
// well below it.
const maxFrame = 8 << 20

// LogsOptions selects a container's log output.
type LogsOptions struct {
	Follow     bool
	Since      time.Time // zero: from the start
	Tail       string    // "": every line; otherwise a line count
	Timestamps bool
	Stdout     bool
	Stderr     bool
}

// LogFrame is one chunk of log output. Stream is "stdout" or "stderr"; a
// container with a TTY has one combined stream, reported as "stdout".
type LogFrame struct {
	Stream string
	Data   []byte
}

// LogStream reads a container's log output frame by frame.
type LogStream struct {
	body        io.ReadCloser
	r           *bufio.Reader
	multiplexed bool
}

// Logs opens a container's log output.
func (c *Client) Logs(ctx context.Context, id string, opts LogsOptions) (*LogStream, error) {
	q := url.Values{
		"follow":     {strconv.FormatBool(opts.Follow)},
		"timestamps": {strconv.FormatBool(opts.Timestamps)},
		"stdout":     {strconv.FormatBool(opts.Stdout)},
		"stderr":     {strconv.FormatBool(opts.Stderr)},
	}
	if !opts.Since.IsZero() {
		q.Set("since", unixArg(opts.Since))
	}
	if opts.Tail != "" {
		q.Set("tail", opts.Tail)
	}
	res, err := c.do(ctx, request{method: http.MethodGet, path: "/containers/" + url.PathEscape(id) + "/logs", query: q})
	if err != nil {
		return nil, err
	}
	// From API 1.42 the content type says whether the output is
	// multiplexed (no TTY) or raw (TTY).
	return &LogStream{
		body:        res.Body,
		r:           bufio.NewReader(res.Body),
		multiplexed: res.Header.Get("Content-Type") == "application/vnd.docker.multiplexed-stream",
	}, nil
}

// Next returns the next frame, or io.EOF at the end of the output.
func (s *LogStream) Next() (LogFrame, error) {
	if !s.multiplexed {
		buf := make([]byte, 32<<10)
		n, err := s.r.Read(buf)
		if n > 0 {
			return LogFrame{Stream: "stdout", Data: buf[:n]}, nil
		}
		return LogFrame{}, err
	}
	var hdr [8]byte
	if _, err := io.ReadFull(s.r, hdr[:]); err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return LogFrame{}, fmt.Errorf("dockerapi: logs: truncated frame header")
		}
		return LogFrame{}, err
	}
	var stream string
	switch hdr[0] {
	case 1:
		stream = "stdout"
	case 2:
		stream = "stderr"
	default:
		return LogFrame{}, fmt.Errorf("dockerapi: logs: unknown stream %d", hdr[0])
	}
	size := binary.BigEndian.Uint32(hdr[4:])
	if size > maxFrame {
		return LogFrame{}, fmt.Errorf("dockerapi: logs: frame of %d bytes exceeds %d", size, maxFrame)
	}
	data := make([]byte, size)
	if _, err := io.ReadFull(s.r, data); err != nil {
		return LogFrame{}, fmt.Errorf("dockerapi: logs: truncated frame: %w", err)
	}
	return LogFrame{Stream: stream, Data: data}, nil
}

// Close ends the stream.
func (s *LogStream) Close() error { return s.body.Close() }

// unixArg renders t as the API's "seconds.nanoseconds" time argument.
func unixArg(t time.Time) string {
	return fmt.Sprintf("%d.%09d", t.Unix(), t.Nanosecond())
}

// Stats is one resource-usage sample of a container.
type Stats struct {
	Read        string                  `json:"read"`
	PreRead     string                  `json:"preread"`
	CPUStats    CPUStats                `json:"cpu_stats"`
	PreCPUStats CPUStats                `json:"precpu_stats"`
	MemoryStats MemoryStats             `json:"memory_stats"`
	PidsStats   PidsStats               `json:"pids_stats"`
	Networks    map[string]NetworkStats `json:"networks"`
}

// CPUStats is a container's cumulative CPU time in nanoseconds against the
// host's.
type CPUStats struct {
	CPUUsage       CPUUsage `json:"cpu_usage"`
	SystemCPUUsage uint64   `json:"system_cpu_usage"`
	OnlineCPUs     uint32   `json:"online_cpus"`
}

// CPUUsage is a container's CPU time.
type CPUUsage struct {
	TotalUsage uint64 `json:"total_usage"`
}

// MemoryStats is a container's memory use in bytes.
type MemoryStats struct {
	Usage uint64            `json:"usage"`
	Limit uint64            `json:"limit"`
	Stats map[string]uint64 `json:"stats"`
}

// PidsStats is a container's process count.
type PidsStats struct {
	Current uint64 `json:"current"`
}

// NetworkStats is one interface's traffic in bytes.
type NetworkStats struct {
	RxBytes uint64 `json:"rx_bytes"`
	TxBytes uint64 `json:"tx_bytes"`
}

// Stats opens a container's resource-usage samples: one sample when stream
// is false (the daemon takes two readings so the CPU delta is filled),
// otherwise one sample per second until the stream is closed.
func (c *Client) Stats(ctx context.Context, id string, stream bool) (*Stream[Stats], error) {
	q := url.Values{"stream": {strconv.FormatBool(stream)}}
	res, err := c.do(ctx, request{method: http.MethodGet, path: "/containers/" + url.PathEscape(id) + "/stats", query: q})
	if err != nil {
		return nil, err
	}
	return newStream[Stats](res), nil
}

// Event is one daemon event.
type Event struct {
	Type     string
	Action   string
	Actor    EventActor
	Scope    string `json:"scope"`
	Time     int64  `json:"time"`
	TimeNano int64  `json:"timeNano"`
}

// EventActor is the object an event concerns; Attributes carries its name
// and labels.
type EventActor struct {
	ID         string
	Attributes map[string]string
}

// EventsOptions selects daemon events.
type EventsOptions struct {
	Since   time.Time // zero: only new events
	Filters Filters
}

// Events opens the daemon's event stream; it stays open until the stream
// is closed or the context ends.
func (c *Client) Events(ctx context.Context, opts EventsOptions) (*Stream[Event], error) {
	q := url.Values{}
	if !opts.Since.IsZero() {
		q.Set("since", unixArg(opts.Since))
	}
	if err := opts.Filters.set(q); err != nil {
		return nil, err
	}
	res, err := c.do(ctx, request{method: http.MethodGet, path: "/events", query: q})
	if err != nil {
		return nil, err
	}
	return newStream[Event](res), nil
}

// CopyFromContainer returns a tar archive of path inside a container; the
// caller closes it.
func (c *Client) CopyFromContainer(ctx context.Context, id, path string) (io.ReadCloser, error) {
	q := url.Values{"path": {path}}
	res, err := c.do(ctx, request{method: http.MethodGet, path: "/containers/" + url.PathEscape(id) + "/archive", query: q})
	if err != nil {
		return nil, err
	}
	return res.Body, nil
}

// CopyToContainer extracts a tar archive into the directory path inside a
// container, refusing to replace a directory with a file or the reverse.
func (c *Client) CopyToContainer(ctx context.Context, id, path string, archive io.Reader) error {
	q := url.Values{"path": {path}, "noOverwriteDirNonDir": {"true"}}
	_, err := c.call(ctx, request{
		method: http.MethodPut, path: "/containers/" + url.PathEscape(id) + "/archive", query: q,
		body: archive, contentType: "application/x-tar",
	}, nil)
	return err
}
```

- [ ] **Step 4: Run the tests and lint**

Run: `TMPDIR=/root/.cache/w0b2-tmp/6 GOTOOLCHAIN=local go test -count=1 ./internal/platform/dockerapi/ && TMPDIR=/root/.cache/w0b2-tmp/6 golangci-lint run --allow-serial-runners ./internal/platform/dockerapi/...`
Expected: `ok  	github.com/darkraise/darkpanel/internal/platform/dockerapi`, then `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add internal/platform/dockerapi/streams.go internal/platform/dockerapi/streams_test.go
git commit -m "feat(platform): add Docker log, stats and event streams"
rm -rf /root/.cache/w0b2-tmp/6
```

### Task 7: Tailscale DERP map, certificates and WhoIs

The node's DERP map (`null` for a logged-out node), the certificate pair endpoint with a domain pattern check and the key-then-chain split, WhoIs with `ErrPeerNotFound` on 404, and the DERP-region debug report (4.3). Its tests use `newFakeTailscaled` and declare no helper of their own.

**Files:**
- Create: `internal/platform/tslocal/derp.go`
- Test: `internal/platform/tslocal/derp_test.go`

**Interfaces:**
- Consumes: C4 (`send`, `getJSON`, `APIError`, `ErrPeerNotFound`, `newFakeTailscaled`).
- Produces: C5.

**Items:** 79

**Depends on:** Task 2

**Implementer:** dforge:impl-sonnet-low
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 0 = 2

- [ ] **Step 1: Write the failing test**

Create `internal/platform/tslocal/derp_test.go`:

```go
package tslocal

import (
	"errors"
	"io"
	"net/http"
	"net/netip"
	"reflect"
	"strings"
	"testing"
)

func TestDERPMap(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /localapi/v0/derpmap", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"Regions":{"1":{"RegionID":1,"RegionCode":"nyc","RegionName":"New York City","Nodes":[`+
			`{"Name":"1a","RegionID":1,"HostName":"derp1.tailscale.com","IPv4":"192.0.2.1","IPv6":"2001:db8::1"}]},`+
			`"900":{"RegionID":900,"RegionCode":"dp","RegionName":"Darkpanel","Nodes":[{"Name":"900a","RegionID":900,`+
			`"HostName":"203.0.113.7","CertName":"sha256-raw:abcd","IPv4":"203.0.113.7","STUNPort":3478,"DERPPort":443}]}},`+
			`"omitDefaultRegions":true}`)
	})
	m, err := newFakeTailscaled(t, mux).DERPMap(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	dp := m.Regions[900]
	if !m.OmitDefaultRegions || m.Regions[1].RegionCode != "nyc" || dp == nil || dp.Nodes[0].CertName != "sha256-raw:abcd" || dp.Nodes[0].STUNPort != 3478 {
		t.Fatalf("map = %+v", m)
	}
}

func TestDERPMapOfLoggedOutNode(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /localapi/v0/derpmap", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "null\n")
	})
	m, err := newFakeTailscaled(t, mux).DERPMap(t.Context())
	if err != nil || m == nil || m.Regions != nil {
		t.Fatalf("map = %+v, %v", m, err)
	}
}

func TestCertPair(t *testing.T) {
	const key = "-----BEGIN EC PRIVATE KEY-----\nAAAA\n-----END EC PRIVATE KEY-----\n" //nolint:gosec // G101: a placeholder PEM, not a key
	const chain = "-----BEGIN CERTIFICATE-----\nBBBB\n-----END CERTIFICATE-----\n-----BEGIN CERTIFICATE-----\nCCCC\n-----END CERTIFICATE-----\n"
	mux := http.NewServeMux()
	var path, query string
	mux.HandleFunc("GET /localapi/v0/cert/{domain}", func(w http.ResponseWriter, r *http.Request) {
		path, query = r.URL.Path, r.URL.RawQuery
		switch r.PathValue("domain") {
		case "vps.tail1234.ts.net":
			_, _ = io.WriteString(w, key+chain)
		case "broken.ts.net":
			_, _ = io.WriteString(w, chain)
		default:
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, "cert access denied\n")
		}
	})
	c := newFakeTailscaled(t, mux)
	cert, k, err := c.CertPair(t.Context(), "vps.tail1234.ts.net")
	if err != nil {
		t.Fatal(err)
	}
	if string(k) != key || string(cert) != chain || path != "/localapi/v0/cert/vps.tail1234.ts.net" || query != "type=pair" {
		t.Fatalf("key %q cert %q (path %s?%s)", k, cert, path, query)
	}
	if _, _, err := c.CertPair(t.Context(), "broken.ts.net"); err == nil {
		t.Fatal("a chain without a key block was accepted as a pair")
	}
	var apiErr *APIError
	if _, _, err := c.CertPair(t.Context(), "other.ts.net"); !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusForbidden {
		t.Fatalf("denied err = %v", err)
	}
	for _, bad := range []string{"", "a/b", "../x", "a..b", "-a.net", "a.net?x=1", strings.Repeat("a", 64) + ".net"} {
		if _, _, err := c.CertPair(t.Context(), bad); err == nil || !strings.Contains(err.Error(), "invalid certificate domain") {
			t.Errorf("CertPair(%q) err = %v", bad, err)
		}
	}
}

func TestCertPairWithoutKey(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /localapi/v0/cert/{domain}", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "-----BEGIN CERTIFICATE-----\nBBBB\n-----END CERTIFICATE-----\n")
	})
	if _, _, err := newFakeTailscaled(t, mux).CertPair(t.Context(), "vps.ts.net"); err == nil || !strings.Contains(err.Error(), "no delimiter") {
		t.Fatalf("err = %v", err)
	}
}

func TestWhoIs(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /localapi/v0/whois", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("addr") != "100.64.0.2" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, "no match for IP:port\n")
			return
		}
		_, _ = io.WriteString(w, `{"Node":{"ID":42,"StableID":"nA","Name":"laptop.tail1234.ts.net.","Addresses":["100.64.0.2/32"],`+
			`"Tags":["tag:dev"]},"UserProfile":{"ID":7,"LoginName":"owner@example.com","DisplayName":"Owner"},"CapMap":{}}`)
	})
	c := newFakeTailscaled(t, mux)
	w, err := c.WhoIs(t.Context(), netip.MustParseAddr("100.64.0.2"))
	if err != nil {
		t.Fatal(err)
	}
	if w.Node.StableID != "nA" || w.Node.Addresses[0] != netip.MustParsePrefix("100.64.0.2/32") || w.UserProfile.LoginName != "owner@example.com" {
		t.Fatalf("whois = %+v %+v", w.Node, w.UserProfile)
	}
	if _, err := c.WhoIs(t.Context(), netip.MustParseAddr("100.64.0.9")); !errors.Is(err, ErrPeerNotFound) {
		t.Fatalf("unknown address err = %v", err)
	}
}

func TestDebugDERPRegion(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /localapi/v0/debug-derp-region", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("region") != "900" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, "no such region\n")
			return
		}
		_, _ = io.WriteString(w, `{"Info":["Region 900 == \"dp\""],"Warnings":["no IPv6"],"Errors":null}`)
	})
	c := newFakeTailscaled(t, mux)
	r, err := c.DebugDERPRegion(t.Context(), "900")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r, &DERPRegionReport{Info: []string{`Region 900 == "dp"`}, Warnings: []string{"no IPv6"}}) {
		t.Fatalf("report = %+v", r)
	}
	var apiErr *APIError
	if _, err := c.DebugDERPRegion(t.Context(), "x&y"); !errors.As(err, &apiErr) || apiErr.Message != "no such region" {
		t.Fatalf("bad region err = %v", err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mkdir -p /root/.cache/w0b2-tmp/7 && TMPDIR=/root/.cache/w0b2-tmp/7 GOTOOLCHAIN=local go test ./internal/platform/tslocal/`
Expected: FAIL, build failed: `internal/platform/tslocal/derp_test.go:22:38: newFakeTailscaled(t, mux).DERPMap undefined (type *Client has no field or method DERPMap)`

- [ ] **Step 3: Write the implementation**

Create `internal/platform/tslocal/derp.go`:

```go
package tslocal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
)

// DERPMap is the node's current DERP map; a node without a network map has
// none (Regions is nil).
type DERPMap struct {
	Regions            map[int]*DERPRegion
	OmitDefaultRegions bool `json:"omitDefaultRegions,omitempty"`
}

// DERPRegion is one region of a DERP map.
type DERPRegion struct {
	RegionID   int
	RegionCode string
	RegionName string
	Avoid      bool `json:",omitempty"`
	Nodes      []*DERPNode
}

// DERPNode is one server of a DERP region. CertName, when set, is the name
// clients verify, or "sha256-raw:<hex>" for a pinned certificate.
type DERPNode struct {
	Name     string
	RegionID int
	HostName string
	CertName string `json:",omitempty"`
	IPv4     string `json:",omitempty"`
	IPv6     string `json:",omitempty"`
	STUNPort int    `json:",omitempty"` // 0 means 3478; -1 disables STUN
	STUNOnly bool   `json:",omitempty"`
	DERPPort int    `json:",omitempty"` // 0 means 443
}

// DERPMap reads the node's current DERP map.
func (c *Client) DERPMap(ctx context.Context) (*DERPMap, error) {
	var m DERPMap
	if err := c.getJSON(ctx, http.MethodGet, "/localapi/v0/derpmap", &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// certDomainPattern is a DNS name; the domain travels in the URL path.
var certDomainPattern = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*$`)

// CertPair returns the certificate chain and private key tailscaled holds
// (or obtains) for domain, one of the node's CertDomains.
func (c *Client) CertPair(ctx context.Context, domain string) (certPEM, keyPEM []byte, err error) {
	if len(domain) > 253 || !certDomainPattern.MatchString(domain) {
		return nil, nil, fmt.Errorf("tslocal: invalid certificate domain %q", domain)
	}
	b, err := c.send(ctx, http.MethodGet, "/localapi/v0/cert/"+domain+"?type=pair", nil, http.StatusOK)
	if err != nil {
		return nil, nil, err
	}
	// The pair answer is the key's PEM block followed by the certificate
	// blocks.
	i := bytes.Index(b, []byte("--\n--"))
	if i < 0 {
		return nil, nil, errors.New("tslocal: certificate pair: no delimiter between key and certificate")
	}
	i += len("--\n")
	keyPEM, certPEM = b[:i], b[i:]
	if !bytes.Contains(keyPEM, []byte(" PRIVATE KEY-----")) || bytes.Contains(certPEM, []byte(" PRIVATE KEY-----")) {
		return nil, nil, errors.New("tslocal: certificate pair: the first block is not the only private key")
	}
	return certPEM, keyPEM, nil
}

// WhoIs is the node and user holding a tailnet address.
type WhoIs struct {
	Node        *WhoIsNode
	UserProfile *UserProfile
}

// WhoIsNode is a node as WhoIs reports it.
type WhoIsNode struct {
	ID        int64
	StableID  string
	Name      string // MagicDNS name with a trailing dot
	Addresses []netip.Prefix
	Tags      []string
}

// UserProfile is a tailnet user.
type UserProfile struct {
	ID          int64
	LoginName   string
	DisplayName string
}

// WhoIs looks up the node holding addr; ErrPeerNotFound when none does.
func (c *Client) WhoIs(ctx context.Context, addr netip.Addr) (*WhoIs, error) {
	var w WhoIs
	err := c.getJSON(ctx, http.MethodGet, "/localapi/v0/whois?addr="+url.QueryEscape(addr.String()), &w)
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
		return nil, ErrPeerNotFound
	}
	if err != nil {
		return nil, err
	}
	return &w, nil
}

// DERPRegionReport is tailscaled's connectivity diagnosis of one DERP
// region. It uses a throwaway node key, so it shows reachability, not an
// authenticated relay between two nodes.
type DERPRegionReport struct {
	Info     []string
	Warnings []string
	Errors   []string
}

// DebugDERPRegion diagnoses the DERP region with the given ID or code.
func (c *Client) DebugDERPRegion(ctx context.Context, region string) (*DERPRegionReport, error) {
	var r DERPRegionReport
	q := url.Values{"region": {region}}
	b, err := c.send(ctx, http.MethodPost, "/localapi/v0/debug-derp-region?"+q.Encode(), nil, http.StatusOK)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("tslocal: decode DERP region report: %w", err)
	}
	return &r, nil
}
```

- [ ] **Step 4: Run the tests and lint**

Run: `TMPDIR=/root/.cache/w0b2-tmp/7 GOTOOLCHAIN=local go test -count=1 ./internal/platform/tslocal/ && TMPDIR=/root/.cache/w0b2-tmp/7 golangci-lint run --allow-serial-runners ./internal/platform/tslocal/...`
Expected: `ok  	github.com/darkraise/darkpanel/internal/platform/tslocal`, then `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add internal/platform/tslocal/derp.go internal/platform/tslocal/derp_test.go
git commit -m "feat(platform): add Tailscale DERP, cert and WhoIs calls"
rm -rf /root/.cache/w0b2-tmp/7
```

### Task 8: Routes and interfaces

Route lookups as `ip route get` makes them (destination, optional source, input and output interface and mark, so the reverse lookup for an incoming flow works), a dump of every route of one family, and the interface list with addresses. Routes speak rtnetlink directly through `golang.org/x/sys/unix`. `routeRequest` builds the lookup body and is unit-tested attribute by attribute; message parsing is unit-tested with hand-built messages; live tests ask the kernel about loopback and about an incoming flow on the default route's interface. Test helpers carry the prefixes `rt` (messages) and `lr` (live routes).

**Files:**
- Create: `internal/platform/netstat/routes.go`
- Test: `internal/platform/netstat/routes_test.go`

**Interfaces:**
- Consumes: C6 (`Family`, `IPv4`, `IPv6`).
- Produces: C7.

**Items:** 79

**Depends on:** Task 3

**Implementer:** dforge:impl-sonnet-low
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 0 = 2

- [ ] **Step 1: Write the failing test**

Create `internal/platform/netstat/routes_test.go`:

```go
package netstat

import (
	"encoding/binary"
	"errors"
	"net/netip"
	"reflect"
	"slices"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// rtMessage frames payload as one netlink message of type typ.
func rtMessage(typ uint16, seq uint32, payload []byte) []byte {
	b := make([]byte, unix.SizeofNlMsghdr, unix.SizeofNlMsghdr+len(payload))
	binary.NativeEndian.PutUint32(b[0:], uint32(unix.SizeofNlMsghdr+len(payload))) //nolint:gosec // G115: test payloads are a few bytes
	binary.NativeEndian.PutUint16(b[4:], typ)
	binary.NativeEndian.PutUint32(b[8:], seq)
	b = append(b, payload...)
	return append(b, make([]byte, nlAlign(len(b))-len(b))...)
}

// rtRoute builds an RTM_NEWROUTE payload.
func rtRoute(family, dstLen, table, proto, typ uint8, attrs ...[]byte) []byte {
	b := rtMsg(family, dstLen, 0, 0)
	b[4], b[5], b[7] = table, proto, typ
	for _, a := range attrs {
		b = append(b, a...)
	}
	return b
}

func TestParseRoute(t *testing.T) {
	m := rtRoute(unix.AF_INET6, 0, unix.RT_TABLE_MAIN, RouteProtoRA, unix.RTN_UNICAST,
		nlAttr(unix.RTA_TABLE, nlUint32(254)),
		nlAttr(unix.RTA_GATEWAY, netip.MustParseAddr("fe80::1").AsSlice()),
		nlAttr(unix.RTA_OIF, nlUint32(2)),
		nlAttr(unix.RTA_PRIORITY, nlUint32(1024)),
		nlAttr(unix.RTA_CACHEINFO, make([]byte, 32)),
	)
	r, err := parseRoute(m)
	if err != nil {
		t.Fatal(err)
	}
	want := Route{
		Type: "unicast", Table: 254, Protocol: RouteProtoRA, Dst: netip.MustParsePrefix("::/0"),
		Gateway: netip.MustParseAddr("fe80::1"), OutIndex: 2, Priority: 1024,
	}
	if r != want {
		t.Fatalf("route = %+v, want %+v", r, want)
	}

	m = rtRoute(unix.AF_INET, 32, unix.RT_TABLE_LOCAL, RouteProtoKernel, unix.RTN_LOCAL,
		nlAttr(unix.RTA_DST, netip.MustParseAddr("127.0.0.1").AsSlice()),
		nlAttr(unix.RTA_PREFSRC, netip.MustParseAddr("127.0.0.1").AsSlice()),
	)
	if r, err = parseRoute(m); err != nil || r.Type != "local" || r.Dst != netip.MustParsePrefix("127.0.0.1/32") ||
		r.Table != unix.RT_TABLE_LOCAL || r.PrefSrc != netip.MustParseAddr("127.0.0.1") {
		t.Fatalf("lookup answer = %+v, %v", r, err)
	}

	for name, bad := range map[string][]byte{
		"short header":   {2, 0, 0},
		"attr too long":  append(rtRoute(unix.AF_INET, 0, 254, 0, 1), 0xff, 0, 1, 0),
		"attr too short": append(rtRoute(unix.AF_INET, 0, 254, 0, 1), 2, 0, 1, 0),
		"dst length":     rtRoute(unix.AF_INET, 33, 254, 0, 1),
	} {
		if _, err := parseRoute(bad); err == nil {
			t.Errorf("%s: parsed", name)
		}
	}
}

func TestParseNetlink(t *testing.T) {
	a := rtMessage(unix.RTM_NEWROUTE, 1, []byte{1, 2, 3})
	b := rtMessage(unix.RTM_NEWROUTE, 1, []byte{4, 5, 6, 7})
	msgs, done, err := parseNetlink(append(a, b...), 1)
	if err != nil || done || len(msgs) != 2 || !slices.Equal(msgs[0], []byte{1, 2, 3}) || !slices.Equal(msgs[1], []byte{4, 5, 6, 7}) {
		t.Fatalf("messages = %v, done %v, %v", msgs, done, err)
	}
	if _, done, err := parseNetlink(rtMessage(unix.NLMSG_DONE, 1, []byte{0, 0, 0, 0}), 1); err != nil || !done {
		t.Fatalf("done = %v, %v", done, err)
	}
	code := -int32(unix.ENETUNREACH)
	errMsg := binary.NativeEndian.AppendUint32(nil, uint32(code)) //nolint:gosec // G115: encodes the negative errno the kernel sends
	if _, _, err := parseNetlink(rtMessage(unix.NLMSG_ERROR, 1, errMsg), 1); !errors.Is(err, unix.ENETUNREACH) {
		t.Fatalf("error message = %v", err)
	}
	if _, done, err := parseNetlink(rtMessage(unix.NLMSG_ERROR, 1, make([]byte, 20)), 1); err != nil || !done {
		t.Fatalf("acknowledgement = %v, %v", done, err)
	}
	for name, bad := range map[string][]byte{
		"short":       {1, 0, 0},
		"overlong":    rtMessage(unix.RTM_NEWROUTE, 1, []byte{1})[:12+4],
		"wrong seq":   rtMessage(unix.RTM_NEWROUTE, 9, []byte{1}),
		"short error": rtMessage(unix.NLMSG_ERROR, 1, []byte{1}),
	} {
		if _, _, err := parseNetlink(bad, 1); err == nil {
			t.Errorf("%s: parsed", name)
		}
	}
}

func TestRouteGetRefusals(t *testing.T) {
	if _, err := RouteGet(RouteQuery{}); err == nil || !strings.Contains(err.Error(), "without a destination") {
		t.Fatalf("empty query err = %v", err)
	}
	q := RouteQuery{Dst: netip.MustParseAddr("192.0.2.1"), Src: netip.MustParseAddr("2001:db8::1")}
	if _, err := RouteGet(q); err == nil || !strings.Contains(err.Error(), "mixes address families") {
		t.Fatalf("mixed families err = %v", err)
	}
	q = RouteQuery{Dst: netip.MustParseAddr("192.0.2.1"), OIF: "no-such-if0"}
	if _, err := RouteGet(q); err == nil || !strings.Contains(err.Error(), "no-such-if0") {
		t.Fatalf("unknown interface err = %v", err)
	}
}

func TestRouteRequest(t *testing.T) {
	q := RouteQuery{Dst: netip.MustParseAddr("::ffff:192.0.2.1"), Src: netip.MustParseAddr("192.0.2.2"), Mark: 0x40000}
	b, err := routeRequest(q, 7, 9)
	if err != nil {
		t.Fatal(err)
	}
	if b[0] != unix.AF_INET || b[1] != 32 || b[2] != 32 || binary.NativeEndian.Uint32(b[8:]) != unix.RTM_F_LOOKUP_TABLE {
		t.Fatalf("rtmsg = %v", b[:unix.SizeofRtMsg])
	}
	got := map[uint16][]byte{}
	for a := b[unix.SizeofRtMsg:]; len(a) > 0; {
		l := int(binary.NativeEndian.Uint16(a[0:]))
		got[binary.NativeEndian.Uint16(a[2:])] = a[unix.SizeofRtAttr:l]
		a = a[nlAlign(l):]
	}
	want := map[uint16][]byte{
		unix.RTA_DST:  {192, 0, 2, 1},
		unix.RTA_SRC:  {192, 0, 2, 2},
		unix.RTA_IIF:  nlUint32(7),
		unix.RTA_OIF:  nlUint32(9),
		unix.RTA_MARK: nlUint32(0x40000),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("attributes = %v, want %v", got, want)
	}
	b, err = routeRequest(RouteQuery{Dst: netip.MustParseAddr("2001:db8::1")}, 0, 0)
	if err != nil || b[0] != unix.AF_INET6 || b[1] != 128 || b[2] != 0 || len(b) != unix.SizeofRtMsg+unix.SizeofRtAttr+16 {
		t.Fatalf("IPv6 request = %v, %v", b, err)
	}
}

// The live tests ask the running kernel about loopback, which every Linux
// host and container has.
func TestRouteGetLive(t *testing.T) {
	r, err := RouteGet(RouteQuery{Dst: netip.MustParseAddr("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	if r.Type != "local" || r.OutIface != "lo" || r.Dst != netip.MustParsePrefix("127.0.0.1/32") {
		t.Fatalf("route to 127.0.0.1 = %+v", r)
	}
	back, err := RouteGet(RouteQuery{Dst: netip.MustParseAddr("::ffff:127.0.0.1"), Src: netip.MustParseAddr("127.0.0.1")})
	if err != nil || back.OutIface != "lo" {
		t.Fatalf("reverse lookup from the local address = %+v, %v", back, err)
	}
	if !lrHasIPv6Loopback(t) {
		return
	}
	r6, err := RouteGet(RouteQuery{Dst: netip.MustParseAddr("::1")})
	if err != nil || r6.Type != "local" || r6.OutIface != "lo" {
		t.Fatalf("route to ::1 = %+v, %v", r6, err)
	}
}

// TestRouteGetIncomingLive looks up an incoming flow: a packet from a
// documentation address arriving on the default route's interface for that
// interface's own address is delivered locally. It uses the default route's
// interface so reverse-path filtering accepts the source.
func TestRouteGetIncomingLive(t *testing.T) {
	remote := netip.MustParseAddr("192.0.2.10")
	out, err := RouteGet(RouteQuery{Dst: remote})
	if errors.Is(err, ErrNoRoute) {
		t.Skip("no IPv4 default route")
	}
	if err != nil {
		t.Fatal(err)
	}
	ifs, err := Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range ifs {
		if i.Name != out.OutIface {
			continue
		}
		for _, a := range i.Addrs {
			if !a.Addr().Is4() {
				continue
			}
			in, err := RouteGet(RouteQuery{Dst: a.Addr(), Src: remote, IIF: i.Name})
			if err != nil || in.Type != "local" || in.OutIface != "lo" {
				t.Fatalf("incoming %s on %s = %+v, %v", a.Addr(), i.Name, in, err)
			}
			return
		}
	}
	t.Skipf("default route interface %q has no IPv4 address", out.OutIface)
}

func TestRoutesAndInterfacesLive(t *testing.T) {
	routes, err := Routes(IPv4)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range routes {
		if r.Type == "local" && r.Table == unix.RT_TABLE_LOCAL && r.Dst == netip.MustParsePrefix("127.0.0.0/8") && r.OutIface == "lo" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no local 127.0.0.0/8 route on lo in %+v", routes)
	}
	if _, err := Routes(IPv6); err != nil {
		t.Fatal(err)
	}
	ifs, err := Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range ifs {
		if i.Name == "lo" {
			if !i.Loopback || !i.Up || !slices.Contains(i.Addrs, netip.MustParsePrefix("127.0.0.1/8")) {
				t.Fatalf("lo = %+v", i)
			}
			return
		}
	}
	t.Fatalf("no lo in %+v", ifs)
}

func lrHasIPv6Loopback(t *testing.T) bool {
	t.Helper()
	ifs, err := Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range ifs {
		if i.Name == "lo" && slices.Contains(i.Addrs, netip.MustParsePrefix("::1/128")) {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mkdir -p /root/.cache/w0b2-tmp/8 && TMPDIR=/root/.cache/w0b2-tmp/8 GOTOOLCHAIN=local go test ./internal/platform/netstat/`
Expected: FAIL, build failed: `internal/platform/netstat/routes_test.go:22:32: undefined: nlAlign`

- [ ] **Step 3: Write the implementation**

Create `internal/platform/netstat/routes.go`:

```go
package netstat

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"

	"golang.org/x/sys/unix"
)

// ErrNoRoute is a lookup the kernel answers with an unreachable,
// prohibited or missing route.
var ErrNoRoute = errors.New("netstat: no route")

// The route protocols (RTPROT_*) callers test for.
const (
	RouteProtoKernel uint8 = 2
	RouteProtoBoot   uint8 = 3
	RouteProtoStatic uint8 = 4
	RouteProtoRA     uint8 = 9 // learned from IPv6 router advertisements
	RouteProtoDHCP   uint8 = 16
)

// routeTypes are the kernel's route types (RTN_*), by number.
var routeTypes = []string{
	"unspec", "unicast", "local", "broadcast", "anycast", "multicast",
	"blackhole", "unreachable", "prohibit", "throw", "nat", "xresolve",
}

// RouteQuery is one route lookup, as `ip route get` makes it: the kernel
// applies its policy rules and answers the route a packet would take.
type RouteQuery struct {
	Dst  netip.Addr // required
	Src  netip.Addr // optional: the packet's source address
	IIF  string     // optional: look up as for a packet received on this interface
	OIF  string     // optional: only routes through this interface
	Mark uint32     // optional: the packet's firewall mark
}

// Route is a route entry, or the answer to a lookup (whose Dst is the
// looked-up address as a host prefix). A multipath route has no OutIface.
type Route struct {
	Type string
	// Table is the entry's table, or the table a lookup matched. While no
	// policy rule beyond the defaults exists the kernel merges the IPv4
	// local table into main, and a lookup then reports main for local
	// routes too.
	Table    uint32
	Protocol uint8
	Dst      netip.Prefix
	Gateway  netip.Addr
	PrefSrc  netip.Addr
	OutIndex int
	OutIface string
	Priority uint32
}

// RouteGet looks up the route for q.
func RouteGet(q RouteQuery) (Route, error) {
	if !q.Dst.IsValid() {
		return Route{}, errors.New("netstat: route lookup without a destination")
	}
	var index [2]uint32
	for i, name := range []string{q.IIF, q.OIF} {
		if name == "" {
			continue
		}
		ifc, err := net.InterfaceByName(name)
		if err != nil {
			return Route{}, fmt.Errorf("netstat: interface %s: %w", name, err)
		}
		index[i] = uint32(ifc.Index) //nolint:gosec // G115: the kernel numbers interfaces from 1 as int32
	}
	body, err := routeRequest(q, index[0], index[1])
	if err != nil {
		return Route{}, err
	}
	msgs, err := netlinkRequest(unix.RTM_GETROUTE, 0, body)
	if err != nil {
		var errno unix.Errno
		if errors.As(err, &errno) && (errno == unix.ENETUNREACH || errno == unix.EHOSTUNREACH || errno == unix.EACCES) {
			return Route{}, fmt.Errorf("%w to %s: %w", ErrNoRoute, q.Dst, errno)
		}
		return Route{}, err
	}
	if len(msgs) != 1 {
		return Route{}, fmt.Errorf("netstat: route lookup answered %d messages", len(msgs))
	}
	r, err := parseRoute(msgs[0])
	if err != nil {
		return Route{}, err
	}
	r.OutIface = ifaceName(r.OutIndex, nil)
	return r, nil
}

// routeRequest encodes q as an RTM_GETROUTE body: struct rtmsg, then the
// destination, source, input and output interface indexes (0: none) and
// mark as attributes.
func routeRequest(q RouteQuery, iif, oif uint32) ([]byte, error) {
	dst, src := q.Dst.Unmap(), q.Src.Unmap()
	family, bits := familyOf(dst)
	attrs := nlAttr(unix.RTA_DST, dst.AsSlice())
	srcLen := uint8(0)
	if src.IsValid() {
		if src.Is4() != dst.Is4() {
			return nil, errors.New("netstat: route lookup mixes address families")
		}
		attrs = append(attrs, nlAttr(unix.RTA_SRC, src.AsSlice())...)
		srcLen = bits
	}
	if iif != 0 {
		attrs = append(attrs, nlAttr(unix.RTA_IIF, nlUint32(iif))...)
	}
	if oif != 0 {
		attrs = append(attrs, nlAttr(unix.RTA_OIF, nlUint32(oif))...)
	}
	if q.Mark != 0 {
		attrs = append(attrs, nlAttr(unix.RTA_MARK, nlUint32(q.Mark))...)
	}
	return append(rtMsg(family, bits, srcLen, unix.RTM_F_LOOKUP_TABLE), attrs...), nil
}

// Routes lists every route of every table for one family.
func Routes(family Family) ([]Route, error) {
	af := uint8(unix.AF_INET)
	if family == IPv6 {
		af = unix.AF_INET6
	}
	msgs, err := netlinkRequest(unix.RTM_GETROUTE, unix.NLM_F_DUMP, rtMsg(af, 0, 0, 0))
	if err != nil {
		return nil, err
	}
	names := map[int]string{}
	if ifs, err := net.Interfaces(); err == nil {
		for _, ifc := range ifs {
			names[ifc.Index] = ifc.Name
		}
	}
	out := make([]Route, 0, len(msgs))
	for _, m := range msgs {
		r, err := parseRoute(m)
		if err != nil {
			return nil, err
		}
		r.OutIface = ifaceName(r.OutIndex, names)
		out = append(out, r)
	}
	return out, nil
}

// Interface is a network interface with its addresses.
type Interface struct {
	Index    int
	Name     string
	Up       bool // administratively up (IFF_UP)
	Running  bool // operationally up (IFF_RUNNING)
	Loopback bool
	Addrs    []netip.Prefix
}

// Interfaces lists the host's interfaces. The standard library reads them
// through rtnetlink.
func Interfaces() ([]Interface, error) {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("netstat: interfaces: %w", err)
	}
	out := make([]Interface, 0, len(ifs))
	for _, ifc := range ifs {
		addrs, err := ifc.Addrs()
		if err != nil {
			return nil, fmt.Errorf("netstat: addresses of %s: %w", ifc.Name, err)
		}
		i := Interface{
			Index:    ifc.Index,
			Name:     ifc.Name,
			Up:       ifc.Flags&net.FlagUp != 0,
			Running:  ifc.Flags&net.FlagRunning != 0,
			Loopback: ifc.Flags&net.FlagLoopback != 0,
		}
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			addr, ok := netip.AddrFromSlice(ipn.IP)
			if !ok {
				continue
			}
			ones, _ := ipn.Mask.Size()
			i.Addrs = append(i.Addrs, netip.PrefixFrom(addr.Unmap(), ones))
		}
		out = append(out, i)
	}
	return out, nil
}

func ifaceName(index int, names map[int]string) string {
	if index == 0 {
		return ""
	}
	if names != nil {
		return names[index]
	}
	if ifc, err := net.InterfaceByIndex(index); err == nil {
		return ifc.Name
	}
	return ""
}

func familyOf(a netip.Addr) (family, bits uint8) {
	if a.Is4() {
		return unix.AF_INET, 32
	}
	return unix.AF_INET6, 128
}

// rtMsg encodes struct rtmsg: family, dst_len, src_len, tos, table,
// protocol, scope, type, then the 32-bit flags.
func rtMsg(family, dstLen, srcLen uint8, flags uint32) []byte {
	b := make([]byte, unix.SizeofRtMsg)
	b[0], b[1], b[2] = family, dstLen, srcLen
	binary.NativeEndian.PutUint32(b[8:], flags)
	return b
}

func nlUint32(v uint32) []byte {
	return binary.NativeEndian.AppendUint32(nil, v)
}

// nlAttr encodes one attribute, padded to 4 bytes.
func nlAttr(typ uint16, payload []byte) []byte {
	n := unix.SizeofRtAttr + len(payload)
	b := make([]byte, nlAlign(n))
	binary.NativeEndian.PutUint16(b[0:], uint16(n)) //nolint:gosec // G115: an attribute holds a few bytes
	binary.NativeEndian.PutUint16(b[2:], typ)
	copy(b[unix.SizeofRtAttr:], payload)
	return b
}

func nlAlign(n int) int { return (n + 3) &^ 3 }

// parseRoute decodes an RTM_NEWROUTE payload: struct rtmsg, then
// attributes.
func parseRoute(m []byte) (Route, error) {
	if len(m) < unix.SizeofRtMsg {
		return Route{}, fmt.Errorf("netstat: route message of %d bytes", len(m))
	}
	family, dstLen := m[0], int(m[1])
	r := Route{Table: uint32(m[4]), Protocol: m[5], Type: "unknown"}
	if int(m[7]) < len(routeTypes) {
		r.Type = routeTypes[m[7]]
	}
	var dst netip.Addr
	attrs := m[unix.SizeofRtMsg:]
	for len(attrs) >= unix.SizeofRtAttr {
		l := int(binary.NativeEndian.Uint16(attrs[0:]))
		typ := binary.NativeEndian.Uint16(attrs[2:])
		if l < unix.SizeofRtAttr || l > len(attrs) {
			return Route{}, fmt.Errorf("netstat: route attribute of length %d in %d bytes", l, len(attrs))
		}
		v := attrs[unix.SizeofRtAttr:l]
		switch typ {
		case unix.RTA_DST:
			dst, _ = netip.AddrFromSlice(v)
		case unix.RTA_GATEWAY:
			r.Gateway, _ = netip.AddrFromSlice(v)
		case unix.RTA_PREFSRC:
			r.PrefSrc, _ = netip.AddrFromSlice(v)
		case unix.RTA_OIF:
			if len(v) == 4 {
				r.OutIndex = int(binary.NativeEndian.Uint32(v))
			}
		case unix.RTA_PRIORITY:
			if len(v) == 4 {
				r.Priority = binary.NativeEndian.Uint32(v)
			}
		case unix.RTA_TABLE:
			if len(v) == 4 {
				r.Table = binary.NativeEndian.Uint32(v)
			}
		}
		attrs = attrs[min(nlAlign(l), len(attrs)):]
	}
	if !dst.IsValid() {
		dst = netip.IPv4Unspecified()
		if family == unix.AF_INET6 {
			dst = netip.IPv6Unspecified()
		}
	}
	p, err := dst.Prefix(dstLen)
	if err != nil {
		return Route{}, fmt.Errorf("netstat: route destination %s/%d: %w", dst, dstLen, err)
	}
	r.Dst = p
	return r, nil
}

// netlinkRequest sends one rtnetlink request and returns the payloads of
// its answer: one message, or every message of a dump. An error message
// is returned as its errno.
func netlinkRequest(typ, flags uint16, body []byte) ([][]byte, error) {
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.NETLINK_ROUTE)
	if err != nil {
		return nil, fmt.Errorf("netstat: netlink socket: %w", err)
	}
	defer func() { _ = unix.Close(fd) }()
	tv := unix.Timeval{Sec: 5}
	if err := unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &tv); err != nil {
		return nil, fmt.Errorf("netstat: netlink timeout: %w", err)
	}
	if err := unix.Bind(fd, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		return nil, fmt.Errorf("netstat: netlink bind: %w", err)
	}
	const seq = 1
	msg := make([]byte, unix.SizeofNlMsghdr, unix.SizeofNlMsghdr+len(body))
	binary.NativeEndian.PutUint32(msg[0:], uint32(unix.SizeofNlMsghdr+len(body))) //nolint:gosec // G115: a request is well under 64 KiB
	binary.NativeEndian.PutUint16(msg[4:], typ)
	binary.NativeEndian.PutUint16(msg[6:], unix.NLM_F_REQUEST|flags)
	binary.NativeEndian.PutUint32(msg[8:], seq)
	msg = append(msg, body...)
	if err := unix.Sendto(fd, msg, 0, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		return nil, fmt.Errorf("netstat: netlink send: %w", err)
	}
	dump := flags&unix.NLM_F_DUMP != 0
	var out [][]byte
	buf := make([]byte, 1<<16)
	for {
		n, _, err := unix.Recvfrom(fd, buf, 0)
		if err != nil {
			return nil, fmt.Errorf("netstat: netlink receive: %w", err)
		}
		msgs, done, err := parseNetlink(buf[:n], seq)
		if err != nil {
			return nil, err
		}
		out = append(out, msgs...)
		if done || (!dump && len(out) > 0) {
			return out, nil
		}
	}
}

// parseNetlink splits one datagram into message payloads. done reports a
// NLMSG_DONE or a zero error (an acknowledgement); a non-zero error is
// returned as its errno.
func parseNetlink(b []byte, seq uint32) (msgs [][]byte, done bool, err error) {
	for len(b) > 0 {
		if len(b) < unix.SizeofNlMsghdr {
			return nil, false, fmt.Errorf("netstat: netlink header of %d bytes", len(b))
		}
		l := int(binary.NativeEndian.Uint32(b[0:]))
		typ := binary.NativeEndian.Uint16(b[4:])
		if l < unix.SizeofNlMsghdr || l > len(b) {
			return nil, false, fmt.Errorf("netstat: netlink message of length %d in %d bytes", l, len(b))
		}
		payload := b[unix.SizeofNlMsghdr:l]
		if got := binary.NativeEndian.Uint32(b[8:]); got != seq {
			return nil, false, fmt.Errorf("netstat: netlink answer with sequence %d, want %d", got, seq)
		}
		switch typ {
		case unix.NLMSG_DONE:
			return msgs, true, nil
		case unix.NLMSG_ERROR:
			if len(payload) < 4 {
				return nil, false, errors.New("netstat: truncated netlink error")
			}
			if code := int32(binary.NativeEndian.Uint32(payload)); code != 0 { //nolint:gosec // G115: the error field is a signed errno
				return nil, false, unix.Errno(-code) //nolint:gosec // G115: the kernel sends a negative errno
			}
			return msgs, true, nil
		default:
			msgs = append(msgs, append([]byte(nil), payload...))
		}
		b = b[min(nlAlign(l), len(b)):]
	}
	return msgs, false, nil
}
```

- [ ] **Step 4: Run the tests and lint**

Run: `TMPDIR=/root/.cache/w0b2-tmp/8 GOTOOLCHAIN=local go test -count=1 ./internal/platform/netstat/ && TMPDIR=/root/.cache/w0b2-tmp/8 golangci-lint run --allow-serial-runners ./internal/platform/netstat/...`
Expected: `ok  	github.com/darkraise/darkpanel/internal/platform/netstat`, then `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add internal/platform/netstat/routes.go internal/platform/netstat/routes_test.go
git commit -m "feat(platform): look up routes over rtnetlink"
rm -rf /root/.cache/w0b2-tmp/8
```

### Task 9: Netfilter contract classification and fixtures

Classifies every rule against the contract: the modules and targets of spec 2.3's list plus those the pinned UFW, Docker and tailscaled generate, each with its options and their number of values, and which options may be negated. Anything else adds a reason to `Rule.Unsupported`, making the rule opaque. Real save output from Docker 29, UFW 0.36.2 (both families) and tailscaled's chains must parse with no opaque rule. A fuzz target, seeded with the fixtures and `psSample`, checks that `Parse` never panics and that an accepted ruleset only jumps to declared chains (spec 1.8, R74). Its tests declare no helper of their own.

**Files:**
- Create: `internal/platform/netfilter/classify.go`
- Modify: `internal/platform/netfilter/parse.go` (one line in `(*parser).addRule`, C8)
- Test: `internal/platform/netfilter/classify_test.go`
- Test: `internal/platform/netfilter/parse_fuzz_test.go`
- Test: `internal/platform/netfilter/testdata/docker29-v4.save`, `ufw-v4.save`, `ufw-v6.save`, `tailscaled-v4.save`

**Interfaces:**
- Consumes: C8 (every type, `Parse`, the `addRule` insertion point, `psSample`).
- Produces: C9.

**Items:** 79

**Depends on:** Task 4

**Implementer:** dforge:impl-sonnet-medium
**Evaluation:** files 2 - spec 0 - coupling 1 - risk 0 = 3

- [ ] **Step 1: Write the fixtures**

Create each file under `internal/platform/netfilter/testdata/` with exactly the content shown (no trailing spaces; each file ends with a newline). The rule counts in `TestFixtures` depend on every line.

`internal/platform/netfilter/testdata/docker29-v4.save`:

```text
# Generated by iptables-save v1.8.11 (nf_tables) on Fri Oct 23 22:06:52 2026
*raw
:PREROUTING ACCEPT [62545966:78696056709]
:OUTPUT ACCEPT [0:0]
-A PREROUTING -d 172.17.0.2/32 ! -i docker0 -j DROP
-A PREROUTING -d 172.21.0.2/32 ! -i br-a2871ddf38b3 -j DROP
-A PREROUTING -d 127.0.0.1/32 ! -i lo -p tcp -m tcp --dport 5432 -j DROP
-A PREROUTING -d 172.21.0.3/32 ! -i br-a2871ddf38b3 -j DROP
COMMIT
# Completed on Fri Oct 23 22:06:52 2026
# Generated by iptables-save v1.8.11 (nf_tables) on Fri Oct 23 22:06:52 2026
*filter
:INPUT ACCEPT [0:0]
:FORWARD DROP [0:0]
:OUTPUT ACCEPT [0:0]
:DOCKER - [0:0]
:DOCKER-BRIDGE - [0:0]
:DOCKER-CT - [0:0]
:DOCKER-FORWARD - [0:0]
:DOCKER-INTERNAL - [0:0]
:DOCKER-USER - [0:0]
-A FORWARD -j DOCKER-USER
-A FORWARD -j DOCKER-FORWARD
-A DOCKER -d 172.21.0.3/32 ! -i br-a2871ddf38b3 -o br-a2871ddf38b3 -p tcp -m tcp --dport 8000 -j ACCEPT
-A DOCKER -d 172.21.0.2/32 ! -i br-a2871ddf38b3 -o br-a2871ddf38b3 -p tcp -m tcp --dport 5432 -j ACCEPT
-A DOCKER ! -i br-a2871ddf38b3 -o br-a2871ddf38b3 -j DROP
-A DOCKER ! -i docker0 -o docker0 -j DROP
-A DOCKER-BRIDGE -o br-a2871ddf38b3 -j DOCKER
-A DOCKER-BRIDGE -o docker0 -j DOCKER
-A DOCKER-CT -o br-a2871ddf38b3 -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT
-A DOCKER-CT -o docker0 -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT
-A DOCKER-FORWARD -j DOCKER-CT
-A DOCKER-FORWARD -j DOCKER-INTERNAL
-A DOCKER-FORWARD -j DOCKER-BRIDGE
-A DOCKER-FORWARD -i br-139bdfd2f0d6 -o br-139bdfd2f0d6 -j ACCEPT
-A DOCKER-FORWARD -i br-a2871ddf38b3 -j ACCEPT
-A DOCKER-FORWARD -i docker0 -j ACCEPT
-A DOCKER-INTERNAL ! -s 172.23.0.0/16 -o br-139bdfd2f0d6 -j DROP
-A DOCKER-INTERNAL ! -d 172.23.0.0/16 -i br-139bdfd2f0d6 -j DROP
COMMIT
# Completed on Fri Oct 23 22:06:52 2026
# Generated by iptables-save v1.8.11 (nf_tables) on Fri Oct 23 22:06:52 2026
*nat
:PREROUTING ACCEPT [81307:9957922]
:INPUT ACCEPT [0:0]
:OUTPUT ACCEPT [405100:24864032]
:POSTROUTING ACCEPT [409885:25146620]
:DOCKER - [0:0]
-A PREROUTING -m addrtype --dst-type LOCAL -j DOCKER
-A OUTPUT ! -d 127.0.0.0/8 -m addrtype --dst-type LOCAL -j DOCKER
-A POSTROUTING -s 172.17.0.0/16 ! -o docker0 -j MASQUERADE
-A POSTROUTING -s 172.21.0.0/16 ! -o br-a2871ddf38b3 -j MASQUERADE
-A DOCKER -d 127.0.0.1/32 ! -i br-a2871ddf38b3 -p tcp -m tcp --dport 5432 -j DNAT --to-destination 172.21.0.2:5432
-A DOCKER ! -i br-a2871ddf38b3 -p tcp -m tcp --dport 8000 -j DNAT --to-destination 172.21.0.3:8000
COMMIT
# Completed on Fri Oct 23 22:06:52 2026
# Generated by iptables-save v1.8.11 (nf_tables) on Fri Oct 23 22:06:52 2026
*mangle
:PREROUTING ACCEPT [0:0]
:INPUT ACCEPT [0:0]
:FORWARD ACCEPT [0:0]
:OUTPUT ACCEPT [0:0]
:POSTROUTING ACCEPT [0:0]
COMMIT
# Completed on Fri Oct 23 22:06:52 2026
# Generated by iptables-save v1.8.11 (nf_tables) on Fri Oct 23 22:06:52 2026
*security
:INPUT ACCEPT [0:0]
:FORWARD ACCEPT [0:0]
:OUTPUT ACCEPT [0:0]
COMMIT
# Completed on Fri Oct 23 22:06:52 2026
```

`internal/platform/netfilter/testdata/ufw-v4.save`:

```text
*filter
:INPUT DROP [0:0]
:FORWARD DROP [0:0]
:OUTPUT ACCEPT [0:0]
:ufw-after-forward - [0:0]
:ufw-after-input - [0:0]
:ufw-after-logging-forward - [0:0]
:ufw-after-logging-input - [0:0]
:ufw-after-logging-output - [0:0]
:ufw-after-output - [0:0]
:ufw-before-forward - [0:0]
:ufw-before-input - [0:0]
:ufw-before-logging-forward - [0:0]
:ufw-before-logging-input - [0:0]
:ufw-before-logging-output - [0:0]
:ufw-before-output - [0:0]
:ufw-logging-allow - [0:0]
:ufw-logging-deny - [0:0]
:ufw-not-local - [0:0]
:ufw-reject-forward - [0:0]
:ufw-reject-input - [0:0]
:ufw-reject-output - [0:0]
:ufw-skip-to-policy-forward - [0:0]
:ufw-skip-to-policy-input - [0:0]
:ufw-skip-to-policy-output - [0:0]
:ufw-track-forward - [0:0]
:ufw-track-input - [0:0]
:ufw-track-output - [0:0]
:ufw-user-forward - [0:0]
:ufw-user-input - [0:0]
:ufw-user-limit - [0:0]
:ufw-user-limit-accept - [0:0]
:ufw-user-logging-forward - [0:0]
:ufw-user-logging-input - [0:0]
:ufw-user-logging-output - [0:0]
:ufw-user-output - [0:0]
-A INPUT -j ufw-before-logging-input
-A INPUT -j ufw-before-input
-A INPUT -j ufw-after-input
-A INPUT -j ufw-after-logging-input
-A INPUT -j ufw-reject-input
-A INPUT -j ufw-track-input
-A FORWARD -j ufw-before-logging-forward
-A FORWARD -j ufw-before-forward
-A FORWARD -j ufw-after-forward
-A FORWARD -j ufw-after-logging-forward
-A FORWARD -j ufw-reject-forward
-A FORWARD -j ufw-track-forward
-A OUTPUT -j ufw-before-logging-output
-A OUTPUT -j ufw-before-output
-A OUTPUT -j ufw-after-output
-A OUTPUT -j ufw-after-logging-output
-A OUTPUT -j ufw-reject-output
-A OUTPUT -j ufw-track-output
-A ufw-after-input -p udp -m udp --dport 137 -j ufw-skip-to-policy-input
-A ufw-after-input -p udp -m udp --dport 138 -j ufw-skip-to-policy-input
-A ufw-after-input -p tcp -m tcp --dport 139 -j ufw-skip-to-policy-input
-A ufw-after-input -p tcp -m tcp --dport 445 -j ufw-skip-to-policy-input
-A ufw-after-input -p udp -m udp --dport 67 -j ufw-skip-to-policy-input
-A ufw-after-input -p udp -m udp --dport 68 -j ufw-skip-to-policy-input
-A ufw-after-input -m addrtype --dst-type BROADCAST -j ufw-skip-to-policy-input
-A ufw-after-logging-forward -m limit --limit 3/min --limit-burst 10 -j LOG --log-prefix "[UFW BLOCK] "
-A ufw-after-logging-input -m limit --limit 3/min --limit-burst 10 -j LOG --log-prefix "[UFW BLOCK] "
-A ufw-before-forward -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT
-A ufw-before-forward -p icmp -m icmp --icmp-type 3 -j ACCEPT
-A ufw-before-forward -p icmp -m icmp --icmp-type 11 -j ACCEPT
-A ufw-before-forward -p icmp -m icmp --icmp-type 12 -j ACCEPT
-A ufw-before-forward -p icmp -m icmp --icmp-type 8 -j ACCEPT
-A ufw-before-forward -j ufw-user-forward
-A ufw-before-input -i lo -j ACCEPT
-A ufw-before-input -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT
-A ufw-before-input -m conntrack --ctstate INVALID -j ufw-logging-deny
-A ufw-before-input -m conntrack --ctstate INVALID -j DROP
-A ufw-before-input -p icmp -m icmp --icmp-type 3 -j ACCEPT
-A ufw-before-input -p icmp -m icmp --icmp-type 11 -j ACCEPT
-A ufw-before-input -p icmp -m icmp --icmp-type 12 -j ACCEPT
-A ufw-before-input -p icmp -m icmp --icmp-type 8 -j ACCEPT
-A ufw-before-input -p udp -m udp --sport 67 --dport 68 -j ACCEPT
-A ufw-before-input -j ufw-not-local
-A ufw-before-input -d 224.0.0.251/32 -p udp -m udp --dport 5353 -j ACCEPT
-A ufw-before-input -d 239.255.255.250/32 -p udp -m udp --dport 1900 -j ACCEPT
-A ufw-before-input -j ufw-user-input
-A ufw-before-output -o lo -j ACCEPT
-A ufw-before-output -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT
-A ufw-before-output -j ufw-user-output
-A ufw-logging-allow -m limit --limit 3/min --limit-burst 10 -j LOG --log-prefix "[UFW ALLOW] "
-A ufw-logging-deny -m conntrack --ctstate INVALID -m limit --limit 3/min --limit-burst 10 -j RETURN
-A ufw-logging-deny -m limit --limit 3/min --limit-burst 10 -j LOG --log-prefix "[UFW BLOCK] "
-A ufw-not-local -m addrtype --dst-type LOCAL -j RETURN
-A ufw-not-local -m addrtype --dst-type MULTICAST -j RETURN
-A ufw-not-local -m addrtype --dst-type BROADCAST -j RETURN
-A ufw-not-local -m limit --limit 3/min --limit-burst 10 -j ufw-logging-deny
-A ufw-not-local -j DROP
-A ufw-skip-to-policy-forward -j DROP
-A ufw-skip-to-policy-input -j DROP
-A ufw-skip-to-policy-output -j ACCEPT
-A ufw-track-output -p tcp -m conntrack --ctstate NEW -j ACCEPT
-A ufw-track-output -p udp -m conntrack --ctstate NEW -j ACCEPT
-A ufw-user-forward -d 172.17.0.2/32 -i eth0 -o docker0 -p tcp -m tcp --dport 80 -j ACCEPT
-A ufw-user-input -p tcp -m tcp --dport 22 -j ACCEPT
-A ufw-user-input -p tcp -m tcp --dport 2222 -m conntrack --ctstate NEW -m recent --set --name DEFAULT --mask 255.255.255.255 --rsource
-A ufw-user-input -p tcp -m tcp --dport 2222 -m conntrack --ctstate NEW -m recent --update --seconds 30 --hitcount 6 --name DEFAULT --mask 255.255.255.255 --rsource -j ufw-user-limit
-A ufw-user-input -p tcp -m tcp --dport 2222 -j ufw-user-limit-accept
-A ufw-user-input -s 203.0.113.0/24 -p tcp -m tcp --dport 8080 -j DROP
-A ufw-user-limit -m limit --limit 3/min -j LOG --log-prefix "[UFW LIMIT BLOCK] "
-A ufw-user-limit -j REJECT --reject-with icmp-port-unreachable
-A ufw-user-limit-accept -j ACCEPT
COMMIT
```

`internal/platform/netfilter/testdata/ufw-v6.save`:

```text
*filter
:INPUT DROP [0:0]
:FORWARD DROP [0:0]
:OUTPUT ACCEPT [0:0]
:ufw6-after-forward - [0:0]
:ufw6-after-input - [0:0]
:ufw6-after-logging-forward - [0:0]
:ufw6-after-logging-input - [0:0]
:ufw6-after-logging-output - [0:0]
:ufw6-after-output - [0:0]
:ufw6-before-forward - [0:0]
:ufw6-before-input - [0:0]
:ufw6-before-logging-forward - [0:0]
:ufw6-before-logging-input - [0:0]
:ufw6-before-logging-output - [0:0]
:ufw6-before-output - [0:0]
:ufw6-logging-allow - [0:0]
:ufw6-logging-deny - [0:0]
:ufw6-reject-forward - [0:0]
:ufw6-reject-input - [0:0]
:ufw6-reject-output - [0:0]
:ufw6-skip-to-policy-forward - [0:0]
:ufw6-skip-to-policy-input - [0:0]
:ufw6-skip-to-policy-output - [0:0]
:ufw6-track-forward - [0:0]
:ufw6-track-input - [0:0]
:ufw6-track-output - [0:0]
:ufw6-user-forward - [0:0]
:ufw6-user-input - [0:0]
:ufw6-user-limit - [0:0]
:ufw6-user-limit-accept - [0:0]
:ufw6-user-logging-forward - [0:0]
:ufw6-user-logging-input - [0:0]
:ufw6-user-logging-output - [0:0]
:ufw6-user-output - [0:0]
-A INPUT -j ufw6-before-logging-input
-A INPUT -j ufw6-before-input
-A INPUT -j ufw6-after-input
-A INPUT -j ufw6-after-logging-input
-A INPUT -j ufw6-reject-input
-A INPUT -j ufw6-track-input
-A FORWARD -j ufw6-before-logging-forward
-A FORWARD -j ufw6-before-forward
-A FORWARD -j ufw6-after-forward
-A FORWARD -j ufw6-after-logging-forward
-A FORWARD -j ufw6-reject-forward
-A FORWARD -j ufw6-track-forward
-A OUTPUT -j ufw6-before-logging-output
-A OUTPUT -j ufw6-before-output
-A OUTPUT -j ufw6-after-output
-A OUTPUT -j ufw6-after-logging-output
-A OUTPUT -j ufw6-reject-output
-A OUTPUT -j ufw6-track-output
-A ufw6-after-input -p udp -m udp --dport 137 -j ufw6-skip-to-policy-input
-A ufw6-after-input -p udp -m udp --dport 138 -j ufw6-skip-to-policy-input
-A ufw6-after-input -p tcp -m tcp --dport 139 -j ufw6-skip-to-policy-input
-A ufw6-after-input -p tcp -m tcp --dport 445 -j ufw6-skip-to-policy-input
-A ufw6-after-input -p udp -m udp --dport 546 -j ufw6-skip-to-policy-input
-A ufw6-after-input -p udp -m udp --dport 547 -j ufw6-skip-to-policy-input
-A ufw6-after-logging-forward -m limit --limit 3/min --limit-burst 10 -j LOG --log-prefix "[UFW BLOCK] "
-A ufw6-after-logging-input -m limit --limit 3/min --limit-burst 10 -j LOG --log-prefix "[UFW BLOCK] "
-A ufw6-before-forward -m rt --rt-type 0 -j DROP
-A ufw6-before-forward -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT
-A ufw6-before-forward -p ipv6-icmp -m icmp6 --icmpv6-type 1 -j ACCEPT
-A ufw6-before-forward -p ipv6-icmp -m icmp6 --icmpv6-type 2 -j ACCEPT
-A ufw6-before-forward -p ipv6-icmp -m icmp6 --icmpv6-type 3 -j ACCEPT
-A ufw6-before-forward -p ipv6-icmp -m icmp6 --icmpv6-type 4 -j ACCEPT
-A ufw6-before-forward -p ipv6-icmp -m icmp6 --icmpv6-type 128 -j ACCEPT
-A ufw6-before-forward -p ipv6-icmp -m icmp6 --icmpv6-type 129 -j ACCEPT
-A ufw6-before-forward -j ufw6-user-forward
-A ufw6-before-input -i lo -j ACCEPT
-A ufw6-before-input -m rt --rt-type 0 -j DROP
-A ufw6-before-input -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT
-A ufw6-before-input -p ipv6-icmp -m icmp6 --icmpv6-type 129 -j ACCEPT
-A ufw6-before-input -m conntrack --ctstate INVALID -j ufw6-logging-deny
-A ufw6-before-input -m conntrack --ctstate INVALID -j DROP
-A ufw6-before-input -p ipv6-icmp -m icmp6 --icmpv6-type 1 -j ACCEPT
-A ufw6-before-input -p ipv6-icmp -m icmp6 --icmpv6-type 2 -j ACCEPT
-A ufw6-before-input -p ipv6-icmp -m icmp6 --icmpv6-type 3 -j ACCEPT
-A ufw6-before-input -p ipv6-icmp -m icmp6 --icmpv6-type 4 -j ACCEPT
-A ufw6-before-input -p ipv6-icmp -m icmp6 --icmpv6-type 128 -j ACCEPT
-A ufw6-before-input -p ipv6-icmp -m icmp6 --icmpv6-type 133 -m hl --hl-eq 255 -j ACCEPT
-A ufw6-before-input -p ipv6-icmp -m icmp6 --icmpv6-type 134 -m hl --hl-eq 255 -j ACCEPT
-A ufw6-before-input -p ipv6-icmp -m icmp6 --icmpv6-type 135 -m hl --hl-eq 255 -j ACCEPT
-A ufw6-before-input -p ipv6-icmp -m icmp6 --icmpv6-type 136 -m hl --hl-eq 255 -j ACCEPT
-A ufw6-before-input -p ipv6-icmp -m icmp6 --icmpv6-type 141 -m hl --hl-eq 255 -j ACCEPT
-A ufw6-before-input -p ipv6-icmp -m icmp6 --icmpv6-type 142 -m hl --hl-eq 255 -j ACCEPT
-A ufw6-before-input -s fe80::/10 -p ipv6-icmp -m icmp6 --icmpv6-type 130 -j ACCEPT
-A ufw6-before-input -s fe80::/10 -p ipv6-icmp -m icmp6 --icmpv6-type 131 -j ACCEPT
-A ufw6-before-input -s fe80::/10 -p ipv6-icmp -m icmp6 --icmpv6-type 132 -j ACCEPT
-A ufw6-before-input -s fe80::/10 -p ipv6-icmp -m icmp6 --icmpv6-type 143 -j ACCEPT
-A ufw6-before-input -p ipv6-icmp -m icmp6 --icmpv6-type 148 -m hl --hl-eq 255 -j ACCEPT
-A ufw6-before-input -p ipv6-icmp -m icmp6 --icmpv6-type 149 -m hl --hl-eq 255 -j ACCEPT
-A ufw6-before-input -s fe80::/10 -p ipv6-icmp -m icmp6 --icmpv6-type 151 -m hl --hl-eq 1 -j ACCEPT
-A ufw6-before-input -s fe80::/10 -p ipv6-icmp -m icmp6 --icmpv6-type 152 -m hl --hl-eq 1 -j ACCEPT
-A ufw6-before-input -s fe80::/10 -p ipv6-icmp -m icmp6 --icmpv6-type 153 -m hl --hl-eq 1 -j ACCEPT
-A ufw6-before-input -p ipv6-icmp -m icmp6 --icmpv6-type 144 -j ACCEPT
-A ufw6-before-input -p ipv6-icmp -m icmp6 --icmpv6-type 145 -j ACCEPT
-A ufw6-before-input -p ipv6-icmp -m icmp6 --icmpv6-type 146 -j ACCEPT
-A ufw6-before-input -p ipv6-icmp -m icmp6 --icmpv6-type 147 -j ACCEPT
-A ufw6-before-input -s fe80::/10 -d fe80::/10 -p udp -m udp --sport 547 --dport 546 -j ACCEPT
-A ufw6-before-input -d ff02::fb/128 -p udp -m udp --dport 5353 -j ACCEPT
-A ufw6-before-input -d ff02::f/128 -p udp -m udp --dport 1900 -j ACCEPT
-A ufw6-before-input -j ufw6-user-input
-A ufw6-before-output -o lo -j ACCEPT
-A ufw6-before-output -m rt --rt-type 0 -j DROP
-A ufw6-before-output -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT
-A ufw6-before-output -p ipv6-icmp -m icmp6 --icmpv6-type 1 -j ACCEPT
-A ufw6-before-output -p ipv6-icmp -m icmp6 --icmpv6-type 2 -j ACCEPT
-A ufw6-before-output -p ipv6-icmp -m icmp6 --icmpv6-type 3 -j ACCEPT
-A ufw6-before-output -p ipv6-icmp -m icmp6 --icmpv6-type 4 -j ACCEPT
-A ufw6-before-output -p ipv6-icmp -m icmp6 --icmpv6-type 128 -j ACCEPT
-A ufw6-before-output -p ipv6-icmp -m icmp6 --icmpv6-type 129 -j ACCEPT
-A ufw6-before-output -p ipv6-icmp -m icmp6 --icmpv6-type 133 -m hl --hl-eq 255 -j ACCEPT
-A ufw6-before-output -p ipv6-icmp -m icmp6 --icmpv6-type 136 -m hl --hl-eq 255 -j ACCEPT
-A ufw6-before-output -p ipv6-icmp -m icmp6 --icmpv6-type 135 -m hl --hl-eq 255 -j ACCEPT
-A ufw6-before-output -p ipv6-icmp -m icmp6 --icmpv6-type 134 -m hl --hl-eq 255 -j ACCEPT
-A ufw6-before-output -p ipv6-icmp -m icmp6 --icmpv6-type 141 -m hl --hl-eq 255 -j ACCEPT
-A ufw6-before-output -p ipv6-icmp -m icmp6 --icmpv6-type 142 -m hl --hl-eq 255 -j ACCEPT
-A ufw6-before-output -s fe80::/10 -p ipv6-icmp -m icmp6 --icmpv6-type 130 -j ACCEPT
-A ufw6-before-output -s fe80::/10 -p ipv6-icmp -m icmp6 --icmpv6-type 131 -j ACCEPT
-A ufw6-before-output -s fe80::/10 -p ipv6-icmp -m icmp6 --icmpv6-type 132 -j ACCEPT
-A ufw6-before-output -s fe80::/10 -p ipv6-icmp -m icmp6 --icmpv6-type 143 -j ACCEPT
-A ufw6-before-output -p ipv6-icmp -m icmp6 --icmpv6-type 148 -m hl --hl-eq 255 -j ACCEPT
-A ufw6-before-output -p ipv6-icmp -m icmp6 --icmpv6-type 149 -m hl --hl-eq 255 -j ACCEPT
-A ufw6-before-output -s fe80::/10 -p ipv6-icmp -m icmp6 --icmpv6-type 151 -m hl --hl-eq 1 -j ACCEPT
-A ufw6-before-output -s fe80::/10 -p ipv6-icmp -m icmp6 --icmpv6-type 152 -m hl --hl-eq 1 -j ACCEPT
-A ufw6-before-output -s fe80::/10 -p ipv6-icmp -m icmp6 --icmpv6-type 153 -m hl --hl-eq 1 -j ACCEPT
-A ufw6-before-output -j ufw6-user-output
-A ufw6-logging-allow -m limit --limit 3/min --limit-burst 10 -j LOG --log-prefix "[UFW ALLOW] "
-A ufw6-logging-deny -m conntrack --ctstate INVALID -m limit --limit 3/min --limit-burst 10 -j RETURN
-A ufw6-logging-deny -m limit --limit 3/min --limit-burst 10 -j LOG --log-prefix "[UFW BLOCK] "
-A ufw6-skip-to-policy-forward -j DROP
-A ufw6-skip-to-policy-input -j DROP
-A ufw6-skip-to-policy-output -j ACCEPT
-A ufw6-track-output -p tcp -m conntrack --ctstate NEW -j ACCEPT
-A ufw6-track-output -p udp -m conntrack --ctstate NEW -j ACCEPT
-A ufw6-user-input -p tcp -m tcp --dport 22 -j ACCEPT
-A ufw6-user-input -p tcp -m tcp --dport 2222 -m conntrack --ctstate NEW -m recent --set --name DEFAULT --mask ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff --rsource
-A ufw6-user-input -p tcp -m tcp --dport 2222 -m conntrack --ctstate NEW -m recent --update --seconds 30 --hitcount 6 --name DEFAULT --mask ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff --rsource -j ufw6-user-limit
-A ufw6-user-input -p tcp -m tcp --dport 2222 -j ufw6-user-limit-accept
-A ufw6-user-input -s 2001:db8::/32 -i eth0 -p tcp -m tcp --dport 443 -j ACCEPT
-A ufw6-user-limit -m limit --limit 3/min -j LOG --log-prefix "[UFW LIMIT BLOCK] "
-A ufw6-user-limit -j REJECT --reject-with icmp6-port-unreachable
-A ufw6-user-limit-accept -j ACCEPT
COMMIT
```

`internal/platform/netfilter/testdata/tailscaled-v4.save`:

```text
*mangle
:PREROUTING ACCEPT [0:0]
:INPUT ACCEPT [0:0]
:FORWARD ACCEPT [0:0]
:OUTPUT ACCEPT [0:0]
:POSTROUTING ACCEPT [0:0]
-A PREROUTING -m conntrack --ctstate RELATED,ESTABLISHED -m connmark ! --mark 0x0/0xff0000 -j CONNMARK --restore-mark --nfmask 0xff0000 --ctmask 0xff0000
-A FORWARD -o tailscale0 -p tcp -m tcp --tcp-flags SYN,RST SYN -j TCPMSS --clamp-mss-to-pmtu
-A OUTPUT -m conntrack --ctstate NEW -m mark ! --mark 0x0/0xff0000 -j CONNMARK --save-mark --nfmask 0xff0000 --ctmask 0xff0000
COMMIT
*filter
:INPUT DROP [0:0]
:FORWARD DROP [0:0]
:OUTPUT ACCEPT [0:0]
:ts-forward - [0:0]
:ts-input - [0:0]
-A INPUT -j ts-input
-A FORWARD -j ts-forward
-A ts-forward -i tailscale0 -j MARK --set-xmark 0x40000/0xff0000
-A ts-forward -m mark --mark 0x40000/0xff0000 -j ACCEPT
-A ts-forward -o tailscale0 -m conntrack ! --ctstate RELATED,ESTABLISHED -j DROP
-A ts-forward -s 100.64.0.0/10 -o tailscale0 -j DROP
-A ts-forward -o tailscale0 -j ACCEPT
-A ts-input -s 100.101.102.103/32 -i lo -j ACCEPT
-A ts-input -s 100.115.92.0/23 ! -i tailscale0 -j RETURN
-A ts-input -s 100.64.0.0/10 ! -i tailscale0 -j DROP
-A ts-input -i tailscale0 -j ACCEPT
-A ts-input -p udp -m udp --dport 41641 -j ACCEPT
COMMIT
*nat
:PREROUTING ACCEPT [0:0]
:INPUT ACCEPT [0:0]
:OUTPUT ACCEPT [0:0]
:POSTROUTING ACCEPT [0:0]
:ts-postrouting - [0:0]
-A PREROUTING -d 100.101.102.103/32 -m statistic --mode nth --every 2 --packet 0 -j DNAT --to-destination 10.0.0.5
-A PREROUTING -d 100.101.102.103/32 -j DNAT --to-destination 10.0.0.6
-A POSTROUTING -d 10.0.0.0/24 -j SNAT --to-source 100.101.102.103
-A POSTROUTING -j ts-postrouting
-A ts-postrouting -m mark --mark 0x40000/0xff0000 -j MASQUERADE
COMMIT
```

Check the transcription: `rg -c '^-A' internal/platform/netfilter/testdata/` prints one line per file, in any order, ending `docker29-v4.save:28`, `ufw-v4.save:71`, `ufw-v6.save:110` and `tailscaled-v4.save:20`.

- [ ] **Step 2: Write the failing tests**

Create `internal/platform/netfilter/classify_test.go`:

```go
package netfilter

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestClassify(t *testing.T) {
	for _, tc := range []struct {
		rule string
		want []string
	}{
		{"-A X -p tcp -m multiport --dports 80,443 -m conntrack --ctstate NEW --ctorigdstport 8080 -j ACCEPT", nil},
		{"-A X -m connmark --mark 0x1/0x1 -m statistic --mode random --probability 0.5 -j RETURN", nil},
		{"-A X -m state --state NEW -m addrtype ! --src-type LOCAL -j REJECT --reject-with tcp-reset", nil},
		{"-A X -m recent --rcheck --seconds 60 --name ssh --rsource -j DROP", nil},
		{"-A X -j Y", nil},
		{"-A X -m set --match-set allow src -j ACCEPT", []string{"match set"}},
		{"-A X -m pkttype --pkt-type broadcast -j NFLOG", []string{"match pkttype", "target NFLOG"}},
		{"-A X -p tcp -m tcp --syn -j ACCEPT", []string{"match tcp option --syn"}},
		{"-A X -p tcp -m tcp --tcp-flags SYN -j ACCEPT", []string{"match tcp option --tcp-flags with 1 values"}},
		{"-A X -m comment ! --comment x -j ACCEPT", []string{"match comment option ! --comment"}},
		{"-A X -j DNAT --to-destination 10.0.0.1:80 --random", []string{"target DNAT option --random"}},
		{"-A X -j Y --log-prefix x", []string{"jump to Y option --log-prefix"}},
		{"-A X -j ACCEPT --to-ports 80", []string{"target ACCEPT option --to-ports"}},
		{"-A X -j MARK ! --set-xmark 0x1/0x1", []string{"target MARK option ! --set-xmark"}},
		{"-A X -m comment --comment -leading -j ACCEPT", []string{"match comment option --comment with 0 values", "match comment option --leading"}},
	} {
		rs, err := Parse(strings.NewReader("*filter\n:X - [0:0]\n:Y - [0:0]\n" + tc.rule + "\nCOMMIT\n"))
		if err != nil {
			t.Fatalf("%s: %v", tc.rule, err)
		}
		if got := rs.Tables[0].Chain("X").Rules[0].Unsupported; !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s:\n got %q\nwant %q", tc.rule, got, tc.want)
		}
	}
}

// TestFixtures parses real save output: Docker 29.5.3 (iptables-nft
// 1.8.11, captured 2026-10-23 and trimmed to three networks), UFW 0.36.2
// on Ubuntu 24.04 (captured 2026-10-23) and tailscaled's chains
// (reconstructed from tailscale's util/linuxfw at v1.104.2). None of it may be opaque.
func TestFixtures(t *testing.T) {
	for _, tc := range []struct {
		file          string
		tables, rules int
		check         func(*testing.T, *Ruleset)
	}{
		{"docker29-v4.save", 5, 28, func(t *testing.T, rs *Ruleset) {
			r := rs.Table("nat").Chain("DOCKER").Rules[0]
			if r.Dest.Prefix.String() != "127.0.0.1/32" || r.In != (NameMatch{Name: "br-a2871ddf38b3", Neg: true}) ||
				r.Target.Name != "DNAT" || r.Target.Options[0].Values[0] != "172.21.0.2:5432" {
				t.Fatalf("nat DOCKER rule = %+v", r)
			}
			if c := rs.Table("filter").Chain("DOCKER-USER"); len(c.Rules) != 0 {
				t.Fatalf("DOCKER-USER = %+v", c)
			}
		}},
		{"ufw-v4.save", 1, 71, func(t *testing.T, rs *Ruleset) {
			r := rs.Table("filter").Chain("ufw-user-input").Rules[2]
			if r.Matches[2].Module != "recent" || r.Target.Name != "ufw-user-limit" || !r.Target.Chain {
				t.Fatalf("limit rule = %+v", r)
			}
		}},
		{"ufw-v6.save", 1, 110, func(t *testing.T, rs *Ruleset) {
			r := rs.Table("filter").Chain("ufw6-before-input").Rules[1]
			if r.Matches[0].Module != "rt" || r.Target.Name != "DROP" {
				t.Fatalf("rt rule = %+v", r)
			}
		}},
		{"tailscaled-v4.save", 3, 20, func(t *testing.T, rs *Ruleset) {
			r := rs.Table("filter").Chain("ts-forward").Rules[2]
			if o := r.Matches[0].Options[0]; o.Name != "ctstate" || !o.Neg {
				t.Fatalf("stateful rule = %+v", r)
			}
		}},
	} {
		t.Run(tc.file, func(t *testing.T) {
			f, err := os.Open(filepath.Join("testdata", tc.file))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = f.Close() }()
			rs, err := Parse(f)
			if err != nil {
				t.Fatal(err)
			}
			rules := 0
			for _, tb := range rs.Tables {
				for _, c := range tb.Chains {
					for _, r := range c.Rules {
						rules++
						if r.Opaque() {
							t.Errorf("line %d opaque: %q", r.Line, r.Unsupported)
						}
					}
				}
			}
			if len(rs.Tables) != tc.tables || rules != tc.rules {
				t.Fatalf("%d tables, %d rules; want %d, %d", len(rs.Tables), rules, tc.tables, tc.rules)
			}
			tc.check(t, rs)
		})
	}
}
```

Create `internal/platform/netfilter/parse_fuzz_test.go`:

```go
package netfilter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// FuzzParse feeds arbitrary input to Parse: it must never panic, and a
// ruleset it accepts holds only known tables and declared chains.
func FuzzParse(f *testing.F) {
	seeds, err := filepath.Glob(filepath.Join("testdata", "*.save"))
	if err != nil {
		f.Fatal(err)
	}
	for _, name := range seeds {
		b, err := os.ReadFile(name) //nolint:gosec // G304: a fixture under testdata/
		if err != nil {
			f.Fatal(err)
		}
		f.Add(string(b))
	}
	f.Add(psSample)
	f.Fuzz(func(t *testing.T, in string) {
		rs, err := Parse(strings.NewReader(in))
		if err != nil {
			return
		}
		for _, tb := range rs.Tables {
			if tb.Name == "" || strings.ContainsAny(tb.Name, " \t") {
				t.Fatalf("table name %q", tb.Name)
			}
			for _, c := range tb.Chains {
				for _, r := range c.Rules {
					if r.Target.Chain && tb.Chain(r.Target.Name) == nil {
						t.Fatalf("jump to undeclared chain %q", r.Target.Name)
					}
				}
			}
		}
	})
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `mkdir -p /root/.cache/w0b2-tmp/9 && TMPDIR=/root/.cache/w0b2-tmp/9 GOTOOLCHAIN=local go test ./internal/platform/netfilter/`
Expected: FAIL in `TestClassify` with ten mismatches, the first:

```text
--- FAIL: TestClassify (0.00s)
    classify_test.go:37: -A X -m set --match-set allow src -j ACCEPT:
         got []
        want ["match set"]
```

`TestFixtures` already passes: without classification nothing is opaque.

- [ ] **Step 4: Write the implementation**

Create `internal/platform/netfilter/classify.go`:

```go
package netfilter

import "fmt"

// arity is how many values an option takes.
type arity map[string]int

// modules are the -m modules inside the contract, with their options.
// They cover spec 2.3's list and everything the pinned UFW, Docker and
// tailscaled generate; the evaluator decides which of them it models.
var modules = map[string]arity{
	"tcp":       {"sport": 1, "dport": 1, "tcp-flags": 2},
	"udp":       {"sport": 1, "dport": 1},
	"icmp":      {"icmp-type": 1},
	"icmp6":     {"icmpv6-type": 1},
	"multiport": {"sports": 1, "dports": 1},
	"conntrack": {"ctstate": 1, "ctorigdst": 1, "ctorigdstport": 1},
	"state":     {"state": 1},
	"addrtype":  {"src-type": 1, "dst-type": 1},
	"comment":   {"comment": 1},
	"mark":      {"mark": 1},
	"connmark":  {"mark": 1},
	"limit":     {"limit": 1, "limit-burst": 1},
	"recent": {
		"set": 0, "update": 0, "rcheck": 0, "remove": 0, "name": 1, "mask": 1,
		"rsource": 0, "rdest": 0, "seconds": 1, "hitcount": 1, "reap": 0, "rttl": 0,
	},
	"statistic": {"mode": 1, "probability": 1, "every": 1, "packet": 1},
	"hl":        {"hl-eq": 1, "hl-lt": 1, "hl-gt": 1},
	"rt":        {"rt-type": 1},
}

// targets are the -j targets inside the contract, with their options.
var targets = map[string]arity{
	"ACCEPT":     {},
	"DROP":       {},
	"RETURN":     {},
	"REJECT":     {"reject-with": 1},
	"DNAT":       {"to-destination": 1},
	"SNAT":       {"to-source": 1},
	"MASQUERADE": {"to-ports": 1},
	"MARK":       {"set-xmark": 1},
	"CONNMARK":   {"set-xmark": 1, "save-mark": 0, "restore-mark": 0, "nfmask": 1, "ctmask": 1},
	"LOG": {
		"log-prefix": 1, "log-level": 1, "log-tcp-sequence": 0, "log-tcp-options": 0,
		"log-ip-options": 0, "log-uid": 0,
	},
	"TCPMSS": {"clamp-mss-to-pmtu": 0, "set-mss": 1},
}

// negatable are the module options iptables-save prints with "!".
var negatable = map[string]bool{
	"sport": true, "dport": true, "sports": true, "dports": true, "tcp-flags": true,
	"icmp-type": true, "icmpv6-type": true, "ctstate": true, "ctorigdst": true,
	"ctorigdstport": true, "state": true, "src-type": true, "dst-type": true,
	"mark": true, "rt-type": true,
}

// classify appends to r.Unsupported every module, target or option
// outside the contract. A jump to a declared chain takes no options.
func classify(r *Rule) {
	for _, m := range r.Matches {
		known, ok := modules[m.Module]
		if !ok {
			r.Unsupported = append(r.Unsupported, "match "+m.Module)
			continue
		}
		checkOptions(r, "match "+m.Module, known, m.Options, true)
	}
	switch {
	case r.Target.Name == "":
	case r.Target.Chain:
		checkOptions(r, "jump to "+r.Target.Name, arity{}, r.Target.Options, false)
	default:
		known, ok := targets[r.Target.Name]
		if !ok {
			r.Unsupported = append(r.Unsupported, "target "+r.Target.Name)
			return
		}
		checkOptions(r, "target "+r.Target.Name, known, r.Target.Options, false)
	}
}

// negOK allows "!" on the options negatable lists; no target option is.
func checkOptions(r *Rule, where string, known arity, opts []Option, negOK bool) {
	for _, o := range opts {
		n, ok := known[o.Name]
		switch {
		case !ok:
			r.Unsupported = append(r.Unsupported, fmt.Sprintf("%s option --%s", where, o.Name))
		case len(o.Values) != n:
			r.Unsupported = append(r.Unsupported, fmt.Sprintf("%s option --%s with %d values", where, o.Name, len(o.Values)))
		case o.Neg && (!negOK || !negatable[o.Name]):
			r.Unsupported = append(r.Unsupported, fmt.Sprintf("%s option ! --%s", where, o.Name))
		}
	}
}
```

In `internal/platform/netfilter/parse.go`, inside `(*parser).addRule`, insert `classify(&rule)` before the append, so the end of the function reads:

```go
	rule.Line, rule.Text = p.line, line
	if rule.Target.Name != "" && p.table.Chain(rule.Target.Name) != nil {
		rule.Target.Chain = true
	}
	classify(&rule)
	c.Rules = append(c.Rules, rule)
	return nil
}
```

- [ ] **Step 5: Run the tests, a short fuzz run and lint**

Run: `TMPDIR=/root/.cache/w0b2-tmp/9 GOTOOLCHAIN=local go test -count=1 ./internal/platform/netfilter/ && TMPDIR=/root/.cache/w0b2-tmp/9 GOTOOLCHAIN=local timeout 120 go test -run '^$' -fuzz FuzzParse -fuzztime 30s ./internal/platform/netfilter/ && TMPDIR=/root/.cache/w0b2-tmp/9 golangci-lint run --allow-serial-runners ./internal/platform/netfilter/...`
Expected: `ok  	github.com/darkraise/darkpanel/internal/platform/netfilter`, then the fuzz run ending `PASS` and `ok`, then `0 issues.` The fuzz run writes its corpus under the Go build cache, not the repository; if it reports a failure it writes `testdata/fuzz/FuzzParse/<hash>`: do not change the parser; stop and report BLOCKED with the failing input and the fuzz output.

- [ ] **Step 6: Commit**

```bash
git add internal/platform/netfilter/
git commit -m "feat(platform): classify netfilter rules by contract"
rm -rf /root/.cache/w0b2-tmp/9
```

### Task 10: Docker images and volumes

Image list, inspect, pull (a progress stream whose messages carry the daemon's in-band errors; a reference without a tag or digest pulls `latest`, because the daemon would otherwise pull every tag, and the tag split ignores a registry port) and remove; image references are checked so they cannot leave their path segment, since the daemon routes them unescaped. `ListImagesOptions.SharedSize` asks the daemon for `ImageSummary.SharedSize`, so a prune plan can show the bytes it reclaims (R51). Volume list, inspect, create and remove. Its tests use `newFakeDaemon` and declare no helper of their own.

**Files:**
- Create: `internal/platform/dockerapi/images.go`
- Create: `internal/platform/dockerapi/volumes.go`
- Test: `internal/platform/dockerapi/images_test.go`

**Interfaces:**
- Consumes: C1 (`request`, `do`, `call`, `Filters.set`, `newStream`, `Stream`, `newFakeDaemon`, `IsNotFound`, `IsConflict`), C2 (`ContainerConfig`).
- Produces: C3 (`images.go` and `volumes.go` part).

**Items:** 79

**Depends on:** Task 1, Task 5

**Implementer:** dforge:impl-sonnet-low
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 0 = 2

- [ ] **Step 1: Write the failing test**

Create `internal/platform/dockerapi/images_test.go`:

```go
package dockerapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestSplitImageRef(t *testing.T) {
	cases := []struct{ ref, name, tag string }{
		{"alpine", "alpine", "latest"},
		{"alpine:3", "alpine", "3"},
		{"registry.local:5000/team/app", "registry.local:5000/team/app", "latest"},
		{"registry.local:5000/team/app:1.2", "registry.local:5000/team/app", "1.2"},
		{"jc21/nginx-proxy-manager:2.13@sha256:bbbb", "jc21/nginx-proxy-manager:2.13@sha256:bbbb", ""},
	}
	for _, tc := range cases {
		if name, tag := splitImageRef(tc.ref); name != tc.name || tag != tc.tag {
			t.Errorf("splitImageRef(%q) = %q, %q; want %q, %q", tc.ref, name, tag, tc.name, tc.tag)
		}
	}
}

func TestImageCalls(t *testing.T) {
	mux := http.NewServeMux()
	var seen []string
	mux.HandleFunc("GET /v1.49/images/json", func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, "list "+r.URL.RawQuery)
		_, _ = io.WriteString(w, `[{"Id":"sha256:aaaa","ParentId":"","RepoTags":["alpine:3"],"RepoDigests":["alpine@sha256:cccc"],`+
			`"Created":1761210902,"Size":8000000,"SharedSize":5000000,"Labels":null,"Containers":-1}]`)
	})
	mux.HandleFunc("GET /v1.49/images/", func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, "inspect "+r.URL.Path)
		_, _ = io.WriteString(w, `{"Id":"sha256:aaaa","RepoTags":["alpine:3"],"Created":"2026-10-01T00:00:00Z","Size":8000000,`+
			`"Architecture":"amd64","Os":"linux","Config":{"Cmd":["/bin/sh"],"Labels":{"x":"y"}}}`)
	})
	mux.HandleFunc("DELETE /v1.49/images/", func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, "delete "+r.URL.Path+" "+r.URL.RawQuery)
		_, _ = io.WriteString(w, `[{"Untagged":"alpine:3"},{"Deleted":"sha256:aaaa"}]`)
	})
	c := newFakeDaemon(t, "1.49", mux)
	ctx := t.Context()
	list, err := c.ListImages(ctx, ListImagesOptions{SharedSize: true, Filters: Filters{"dangling": {"true"}}})
	if err != nil || len(list) != 1 || list[0].RepoDigests[0] != "alpine@sha256:cccc" || list[0].Containers != -1 || list[0].SharedSize != 5000000 {
		t.Fatalf("list = %+v, %v", list, err)
	}
	img, err := c.InspectImage(ctx, "docker.io/library/alpine:3")
	if err != nil || img.Config.Labels["x"] != "y" || img.Os != "linux" {
		t.Fatalf("inspect = %+v, %v", img, err)
	}
	items, err := c.RemoveImage(ctx, "alpine:3", RemoveImageOptions{NoPrune: true})
	if err != nil || !reflect.DeepEqual(items, []ImageDeleteItem{{Untagged: "alpine:3"}, {Deleted: "sha256:aaaa"}}) {
		t.Fatalf("remove = %+v, %v", items, err)
	}
	want := []string{
		"list filters=%7B%22dangling%22%3A%5B%22true%22%5D%7D&shared-size=1",
		"inspect /v1.49/images/docker.io/library/alpine:3/json",
		"delete /v1.49/images/alpine:3 force=false&noprune=true",
	}
	if !reflect.DeepEqual(seen, want) {
		t.Fatalf("requests = %q", seen)
	}
	for _, bad := range []string{"", "a?b", "a b", "../../containers/x", "a#b", "a%2Fb"} {
		if _, err := c.InspectImage(ctx, bad); err == nil || !strings.Contains(err.Error(), "invalid image reference") {
			t.Errorf("InspectImage(%q) err = %v", bad, err)
		}
	}
}

func TestPullImageStream(t *testing.T) {
	mux := http.NewServeMux()
	var query string
	mux.HandleFunc("POST /v1.49/images/create", func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		_, _ = io.WriteString(w, `{"status":"Pulling from library/alpine","id":"3"}
{"status":"Downloading","progressDetail":{"current":10,"total":100},"progress":"[=>  ]","id":"abc"}
{"errorDetail":{"message":"manifest unknown"},"error":"manifest unknown"}
`)
	})
	c := newFakeDaemon(t, "1.49", mux)
	s, err := c.PullImage(t.Context(), "alpine:3")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	var msgs []PullMessage
	for {
		m, err := s.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		msgs = append(msgs, m)
	}
	if query != "fromImage=alpine&tag=3" {
		t.Fatalf("query = %s", query)
	}
	if len(msgs) != 3 || msgs[1].ProgressDetail.Total != 100 || msgs[0].Err() != nil {
		t.Fatalf("messages = %+v", msgs)
	}
	if err := msgs[2].Err(); err == nil || err.Error() != "dockerapi: pull: manifest unknown" {
		t.Fatalf("stream failure = %v", err)
	}
	if err := (PullMessage{Error: "denied"}).Err(); err == nil || err.Error() != "dockerapi: pull: denied" {
		t.Fatalf("legacy error field = %v", err)
	}
}

func TestVolumeCalls(t *testing.T) {
	mux := http.NewServeMux()
	var created VolumeCreate
	mux.HandleFunc("GET /v1.49/volumes", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("filters") != `{"label":["darkpanel.module=npm"]}` {
			t.Errorf("filters = %s", r.URL.Query().Get("filters"))
		}
		_, _ = io.WriteString(w, `{"Volumes":[{"Name":"darkpanel-npm-data","Driver":"local","Mountpoint":"/var/lib/docker/volumes/darkpanel-npm-data/_data",`+
			`"CreatedAt":"2026-10-01T00:00:00Z","Scope":"local","Labels":{"darkpanel.module":"npm"},"Options":null}],"Warnings":null}`)
	})
	mux.HandleFunc("GET /v1.49/volumes/{name}", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"message":"get `+r.PathValue("name")+`: no such volume"}`) //nolint:gosec // G705: a test fake answering JSON
	})
	mux.HandleFunc("POST /v1.49/volumes/create", func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&created); err != nil {
			t.Error(err)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"Name":"v1","Driver":"local","Labels":{"darkpanel.module":"npm"},"Scope":"local"}`)
	})
	mux.HandleFunc("DELETE /v1.49/volumes/{name}", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "force=false" {
			t.Errorf("query = %s", r.URL.RawQuery)
		}
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, `{"message":"remove v1: volume is in use - [c1]"}`)
	})
	c := newFakeDaemon(t, "1.49", mux)
	ctx := t.Context()
	vols, err := c.ListVolumes(ctx, Filters{"label": {"darkpanel.module=npm"}})
	if err != nil || len(vols) != 1 || vols[0].Labels["darkpanel.module"] != "npm" {
		t.Fatalf("list = %+v, %v", vols, err)
	}
	if _, err := c.InspectVolume(ctx, "gone"); !IsNotFound(err) {
		t.Fatalf("inspect err = %v", err)
	}
	v, err := c.CreateVolume(ctx, VolumeCreate{Name: "v1", Labels: map[string]string{"darkpanel.module": "npm"}})
	if err != nil || v.Name != "v1" || created.Name != "v1" || created.Labels["darkpanel.module"] != "npm" {
		t.Fatalf("create = %+v, %v (sent %+v)", v, err, created)
	}
	if err := c.RemoveVolume(ctx, "v1", false); !IsConflict(err) {
		t.Fatalf("remove err = %v", err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mkdir -p /root/.cache/w0b2-tmp/10 && TMPDIR=/root/.cache/w0b2-tmp/10 GOTOOLCHAIN=local go test ./internal/platform/dockerapi/`
Expected: FAIL, build failed: `internal/platform/dockerapi/images_test.go:22:19: undefined: splitImageRef`

- [ ] **Step 3: Write the implementation**

Create `internal/platform/dockerapi/images.go`:

```go
package dockerapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// ImageSummary is one entry of GET /images/json.
type ImageSummary struct {
	ID          string `json:"Id"`
	ParentID    string `json:"ParentId"`
	RepoTags    []string
	RepoDigests []string
	Created     int64 // Unix seconds
	Size        int64
	Labels      map[string]string
	Containers  int64 // -1 unless the daemon computed it
	// SharedSize is the bytes this image shares with others, -1 unless
	// ListImagesOptions.SharedSize asked for it; Size minus SharedSize is what
	// removing the image alone reclaims.
	SharedSize int64
}

// ImageInspect is GET /images/{name}/json.
type ImageInspect struct {
	ID           string `json:"Id"`
	RepoTags     []string
	RepoDigests  []string
	Created      string // RFC 3339 with nanoseconds
	Size         int64
	Architecture string
	Os           string
	Config       *ContainerConfig
}

// PullMessage is one progress message of an image pull.
type PullMessage struct {
	Status         string          `json:"status,omitempty"`
	ID             string          `json:"id,omitempty"`
	Progress       string          `json:"progress,omitempty"`
	ProgressDetail *ProgressDetail `json:"progressDetail,omitempty"`
	Error          string          `json:"error,omitempty"`
	ErrorDetail    *ErrorDetail    `json:"errorDetail,omitempty"`
}

// ProgressDetail is a layer's transfer progress in bytes.
type ProgressDetail struct {
	Current int64 `json:"current,omitempty"`
	Total   int64 `json:"total,omitempty"`
}

// ErrorDetail is a failure the daemon reports inside a stream.
type ErrorDetail struct {
	Code    int    `json:"code,omitempty"`
	Message string `json:"message"`
}

// Err returns the failure the message reports, or nil. A pull that fails
// after it started answers 200 and reports the failure in the stream.
func (m PullMessage) Err() error {
	switch {
	case m.ErrorDetail != nil && m.ErrorDetail.Message != "":
		return fmt.Errorf("dockerapi: pull: %s", m.ErrorDetail.Message)
	case m.Error != "":
		return fmt.Errorf("dockerapi: pull: %s", m.Error)
	}
	return nil
}

// ImageDeleteItem is one entry of DELETE /images/{name}'s answer.
type ImageDeleteItem struct {
	Untagged string `json:",omitempty"`
	Deleted  string `json:",omitempty"`
}

// ListImagesOptions selects the images GET /images/json lists.
type ListImagesOptions struct {
	All        bool // include intermediate images
	SharedSize bool // compute ImageSummary.SharedSize
	Filters    Filters
}

// ListImages lists images.
func (c *Client) ListImages(ctx context.Context, opts ListImagesOptions) ([]ImageSummary, error) {
	q := url.Values{}
	if opts.All {
		q.Set("all", "1")
	}
	if opts.SharedSize {
		q.Set("shared-size", "1")
	}
	if err := opts.Filters.set(q); err != nil {
		return nil, err
	}
	var out []ImageSummary
	_, err := c.call(ctx, request{method: http.MethodGet, path: "/images/json", query: q}, &out)
	return out, err
}

// InspectImage inspects an image by ID or reference.
func (c *Client) InspectImage(ctx context.Context, ref string) (ImageInspect, error) {
	if err := checkImageRef(ref); err != nil {
		return ImageInspect{}, err
	}
	var out ImageInspect
	_, err := c.call(ctx, request{method: http.MethodGet, path: "/images/" + ref + "/json"}, &out)
	return out, err
}

// PullImage starts pulling ref and returns its progress stream; the caller
// reads it to the end, checking each message's Err. A reference without a
// tag or digest pulls the tag "latest": the daemon would otherwise pull
// every tag of the repository.
func (c *Client) PullImage(ctx context.Context, ref string) (*Stream[PullMessage], error) {
	if err := checkImageRef(ref); err != nil {
		return nil, err
	}
	name, tag := splitImageRef(ref)
	q := url.Values{"fromImage": {name}, "tag": {tag}}
	res, err := c.do(ctx, request{method: http.MethodPost, path: "/images/create", query: q})
	if err != nil {
		return nil, err
	}
	return newStream[PullMessage](res), nil
}

// splitImageRef splits a reference into the fromImage and tag arguments:
// a digest reference stays whole with no tag.
func splitImageRef(ref string) (name, tag string) {
	if strings.Contains(ref, "@") {
		return ref, ""
	}
	slash := strings.LastIndex(ref, "/")
	if colon := strings.LastIndex(ref, ":"); colon > slash {
		return ref[:colon], ref[colon+1:]
	}
	return ref, "latest"
}

// RemoveImageOptions modifies DELETE /images/{name}.
type RemoveImageOptions struct {
	Force   bool
	NoPrune bool // keep untagged parents
}

// RemoveImage removes an image by ID or reference.
func (c *Client) RemoveImage(ctx context.Context, ref string, opts RemoveImageOptions) ([]ImageDeleteItem, error) {
	if err := checkImageRef(ref); err != nil {
		return nil, err
	}
	q := url.Values{"force": {strconv.FormatBool(opts.Force)}, "noprune": {strconv.FormatBool(opts.NoPrune)}}
	var out []ImageDeleteItem
	_, err := c.call(ctx, request{method: http.MethodDelete, path: "/images/" + ref, query: q}, &out)
	return out, err
}

// checkImageRef refuses a reference that could leave the path segment: the
// daemon routes image references with their slashes unescaped, so the
// reference cannot be path-escaped.
func checkImageRef(ref string) error {
	if ref == "" || strings.ContainsAny(ref, "?#% \t\r\n") || strings.Contains(ref, "..") {
		return errors.New("dockerapi: invalid image reference")
	}
	return nil
}
```

Create `internal/platform/dockerapi/volumes.go`:

```go
package dockerapi

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

// Volume is a Docker volume.
type Volume struct {
	Name       string
	Driver     string
	Mountpoint string
	CreatedAt  string
	Scope      string
	Labels     map[string]string
	Options    map[string]string
}

// VolumeCreate is a POST /volumes/create request.
type VolumeCreate struct {
	Name       string
	Driver     string            `json:",omitempty"`
	DriverOpts map[string]string `json:",omitempty"`
	Labels     map[string]string `json:",omitempty"`
}

// ListVolumes lists volumes.
func (c *Client) ListVolumes(ctx context.Context, filters Filters) ([]Volume, error) {
	q := url.Values{}
	if err := filters.set(q); err != nil {
		return nil, err
	}
	var out struct {
		Volumes []Volume
	}
	_, err := c.call(ctx, request{method: http.MethodGet, path: "/volumes", query: q}, &out)
	return out.Volumes, err
}

// InspectVolume inspects a volume by name.
func (c *Client) InspectVolume(ctx context.Context, name string) (Volume, error) {
	var out Volume
	_, err := c.call(ctx, request{method: http.MethodGet, path: "/volumes/" + url.PathEscape(name)}, &out)
	return out, err
}

// CreateVolume creates a volume; the daemon returns an existing volume of
// the same name and driver unchanged.
func (c *Client) CreateVolume(ctx context.Context, v VolumeCreate) (Volume, error) {
	var out Volume
	_, err := c.call(ctx, request{method: http.MethodPost, path: "/volumes/create", body: v}, &out)
	return out, err
}

// RemoveVolume removes a volume; the daemon answers 409 while a container
// uses it.
func (c *Client) RemoveVolume(ctx context.Context, name string, force bool) error {
	q := url.Values{"force": {strconv.FormatBool(force)}}
	_, err := c.call(ctx, request{method: http.MethodDelete, path: "/volumes/" + url.PathEscape(name), query: q}, nil)
	return err
}
```

- [ ] **Step 4: Run the tests and lint**

Run: `TMPDIR=/root/.cache/w0b2-tmp/10 GOTOOLCHAIN=local go test -count=1 ./internal/platform/dockerapi/ && TMPDIR=/root/.cache/w0b2-tmp/10 golangci-lint run --allow-serial-runners ./internal/platform/dockerapi/...`
Expected: `ok  	github.com/darkraise/darkpanel/internal/platform/dockerapi`, then `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add internal/platform/dockerapi/images.go internal/platform/dockerapi/volumes.go internal/platform/dockerapi/images_test.go
git commit -m "feat(platform): add Docker image and volume calls"
rm -rf /root/.cache/w0b2-tmp/10
```

### Task 11: Docker networks

Network list, inspect, create, remove, connect (with an endpoint configuration such as a fixed address) and disconnect. `Network.EnableIPv4` is a pointer because daemons below API 1.47 do not report it. Test constants carry the prefix `nw`.

**Files:**
- Create: `internal/platform/dockerapi/networks.go`
- Test: `internal/platform/dockerapi/networks_test.go`

**Interfaces:**
- Consumes: C1 (`request`, `call`, `Filters.set`, `newFakeDaemon`, `IsNotFound`), C2 (`EndpointSettings`, `EndpointIPAMConfig`).
- Produces: C3 (`networks.go` part).

**Items:** 79

**Depends on:** Task 1, Task 5

**Implementer:** dforge:impl-sonnet-low
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 0 = 2

- [ ] **Step 1: Write the failing test**

Create `internal/platform/dockerapi/networks_test.go`:

```go
package dockerapi

import (
	"io"
	"net/http"
	"net/netip"
	"reflect"
	"strings"
	"testing"
)

const nwInspectJSON = `{
  "Name": "site_default", "Id": "net1", "Created": "2026-10-01T00:00:00.5Z", "Scope": "local", "Driver": "bridge",
  "EnableIPv4": true, "EnableIPv6": true,
  "IPAM": {"Driver": "default", "Options": null, "Config": [
    {"Subnet": "172.20.0.0/16", "Gateway": "172.20.0.1"},
    {"Subnet": "fd00:20::/64", "Gateway": "fd00:20::1"}]},
  "Internal": false, "Attachable": false, "Ingress": false, "ConfigFrom": {"Network": ""}, "ConfigOnly": false,
  "Containers": {
    "c1": {"Name": "web", "EndpointID": "ep1", "MacAddress": "02:42:ac:14:00:02", "IPv4Address": "172.20.0.2/16", "IPv6Address": "fd00:20::2/64"},
    "c2": {"Name": "db", "EndpointID": "ep2", "MacAddress": "02:42:ac:14:00:03", "IPv4Address": "172.20.0.3/16", "IPv6Address": ""}},
  "Options": {"com.docker.network.bridge.name": "br-site"},
  "Labels": {"com.docker.compose.project": "site"}
}`

func TestInspectAndListNetworks(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1.49/networks/{id}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") != "site_default" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"message":"network `+r.PathValue("id")+` not found"}`) //nolint:gosec // G705: a test fake answering JSON
			return
		}
		_, _ = io.WriteString(w, nwInspectJSON)
	})
	mux.HandleFunc("GET /v1.49/networks", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("filters") != `{"driver":["bridge"]}` {
			t.Errorf("filters = %s", r.URL.Query().Get("filters"))
		}
		_, _ = io.WriteString(w, `[`+nwInspectJSON+`]`)
	})
	c := newFakeDaemon(t, "1.49", mux)
	ctx := t.Context()
	n, err := c.InspectNetwork(ctx, "site_default")
	if err != nil {
		t.Fatal(err)
	}
	if n.ID != "net1" || n.EnableIPv4 == nil || !*n.EnableIPv4 || !n.EnableIPv6 || n.Options["com.docker.network.bridge.name"] != "br-site" ||
		n.IPAM.Config[0].Subnet != netip.MustParsePrefix("172.20.0.0/16") ||
		n.IPAM.Config[1].Gateway != netip.MustParseAddr("fd00:20::1") {
		t.Fatalf("network = %+v", n)
	}
	web, db := n.Containers["c1"], n.Containers["c2"]
	if web.IPv4Address != netip.MustParsePrefix("172.20.0.2/16") || web.IPv6Address != netip.MustParsePrefix("fd00:20::2/64") ||
		db.IPv6Address.IsValid() || db.Name != "db" {
		t.Fatalf("containers = %+v", n.Containers)
	}
	list, err := c.ListNetworks(ctx, Filters{"driver": {"bridge"}})
	if err != nil || len(list) != 1 || list[0].Name != "site_default" {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if _, err := c.InspectNetwork(ctx, "gone"); !IsNotFound(err) {
		t.Fatalf("missing network err = %v", err)
	}
}

func TestNetworkMutations(t *testing.T) {
	mux := http.NewServeMux()
	var seen []string
	record := func(name string, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		seen = append(seen, name+" "+string(b))
	}
	mux.HandleFunc("POST /v1.49/networks/create", func(w http.ResponseWriter, r *http.Request) {
		record("create", r)
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"Id":"net9","Warning":""}`)
	})
	mux.HandleFunc("POST /v1.49/networks/{id}/{op}", func(w http.ResponseWriter, r *http.Request) {
		record(r.PathValue("op")+" "+r.PathValue("id"), r)
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("DELETE /v1.49/networks/{id}", func(w http.ResponseWriter, r *http.Request) {
		record("delete "+r.PathValue("id"), r)
		w.WriteHeader(http.StatusNoContent)
	})
	c := newFakeDaemon(t, "1.49", mux)
	ctx := t.Context()
	v6 := false
	id, err := c.CreateNetwork(ctx, NetworkCreate{
		Name:       "darkpanel-panel",
		Driver:     "bridge",
		EnableIPv6: &v6,
		IPAM:       &IPAM{Config: []IPAMConfig{{Subnet: netip.MustParsePrefix("10.200.0.0/29")}}},
		Options:    map[string]string{"com.docker.network.bridge.name": "dp-panel"},
		Labels:     map[string]string{"darkpanel.module": "panel"},
	})
	if err != nil || id != "net9" {
		t.Fatalf("create = %q, %v", id, err)
	}
	if err := c.ConnectNetwork(ctx, "net9", "npm", &EndpointSettings{
		Aliases:    []string{"proxy"},
		IPAMConfig: &EndpointIPAMConfig{IPv4Address: netip.MustParseAddr("10.200.0.2")},
	}); err != nil {
		t.Fatal(err)
	}
	if err := c.ConnectNetwork(ctx, "net9", "plain", nil); err != nil {
		t.Fatal(err)
	}
	if err := c.DisconnectNetwork(ctx, "net9", "npm", true); err != nil {
		t.Fatal(err)
	}
	if err := c.RemoveNetwork(ctx, "net9"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		`create {"Name":"darkpanel-panel","Driver":"bridge","EnableIPv6":false,"IPAM":{"Config":[{"Subnet":"10.200.0.0/29"}]},` +
			`"Options":{"com.docker.network.bridge.name":"dp-panel"},"Labels":{"darkpanel.module":"panel"}}`,
		`connect net9 {"Container":"npm","EndpointConfig":{"IPAMConfig":{"IPv4Address":"10.200.0.2"},"Aliases":["proxy"]}}`,
		`connect net9 {"Container":"plain"}`,
		`disconnect net9 {"Container":"npm","Force":true}`,
		`delete net9 `,
	}
	if !reflect.DeepEqual(seen, want) {
		t.Fatalf("requests =\n%s", strings.Join(seen, "\n"))
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mkdir -p /root/.cache/w0b2-tmp/11 && TMPDIR=/root/.cache/w0b2-tmp/11 GOTOOLCHAIN=local go test ./internal/platform/dockerapi/`
Expected: FAIL, build failed: `internal/platform/dockerapi/networks_test.go:44:14: c.InspectNetwork undefined (type *Client has no field or method InspectNetwork)`

- [ ] **Step 3: Write the implementation**

Create `internal/platform/dockerapi/networks.go`:

```go
package dockerapi

import (
	"context"
	"net/http"
	"net/netip"
	"net/url"
)

// Network is a Docker network. Containers is filled by InspectNetwork only;
// the list endpoint leaves it empty.
type Network struct {
	Name    string
	ID      string `json:"Id"`
	Created string
	Scope   string
	Driver  string
	// EnableIPv4 is nil below API 1.47, which does not report it; such a
	// daemon cannot create an IPv6-only network, so nil means IPv4 is on.
	EnableIPv4 *bool
	EnableIPv6 bool
	IPAM       IPAM
	Internal   bool
	Attachable bool
	Containers map[string]NetworkContainer
	Options    map[string]string
	Labels     map[string]string
}

// IPAM is a network's address management.
type IPAM struct {
	Driver string       `json:",omitempty"`
	Config []IPAMConfig `json:",omitempty"`
}

// IPAMConfig is one subnet of a network.
type IPAMConfig struct {
	Subnet  netip.Prefix `json:",omitzero"`
	IPRange netip.Prefix `json:",omitzero"`
	Gateway netip.Addr   `json:",omitzero"`
}

// NetworkContainer is a container's endpoint as a network reports it; the
// addresses carry the subnet's prefix length.
type NetworkContainer struct {
	Name        string
	EndpointID  string
	MacAddress  string
	IPv4Address netip.Prefix
	IPv6Address netip.Prefix
}

// NetworkCreate is a POST /networks/create request.
type NetworkCreate struct {
	Name       string
	Driver     string            `json:",omitempty"`
	Internal   bool              `json:",omitempty"`
	Attachable bool              `json:",omitempty"`
	EnableIPv6 *bool             `json:",omitempty"`
	IPAM       *IPAM             `json:",omitempty"`
	Options    map[string]string `json:",omitempty"`
	Labels     map[string]string `json:",omitempty"`
}

// ListNetworks lists networks.
func (c *Client) ListNetworks(ctx context.Context, filters Filters) ([]Network, error) {
	q := url.Values{}
	if err := filters.set(q); err != nil {
		return nil, err
	}
	var out []Network
	_, err := c.call(ctx, request{method: http.MethodGet, path: "/networks", query: q}, &out)
	return out, err
}

// InspectNetwork inspects a network by ID or name, with its containers.
func (c *Client) InspectNetwork(ctx context.Context, id string) (Network, error) {
	var out Network
	_, err := c.call(ctx, request{method: http.MethodGet, path: "/networks/" + url.PathEscape(id)}, &out)
	return out, err
}

// CreateNetwork creates a network and returns its ID.
func (c *Client) CreateNetwork(ctx context.Context, n NetworkCreate) (string, error) {
	var out struct {
		ID string `json:"Id"`
	}
	_, err := c.call(ctx, request{method: http.MethodPost, path: "/networks/create", body: n}, &out)
	return out.ID, err
}

// RemoveNetwork removes a network; the daemon refuses one with endpoints.
func (c *Client) RemoveNetwork(ctx context.Context, id string) error {
	_, err := c.call(ctx, request{method: http.MethodDelete, path: "/networks/" + url.PathEscape(id)}, nil)
	return err
}

// ConnectNetwork attaches a container to a network; endpoint may be nil or
// carry aliases and static addresses.
func (c *Client) ConnectNetwork(ctx context.Context, network, container string, endpoint *EndpointSettings) error {
	body := struct {
		Container      string
		EndpointConfig *EndpointSettings `json:",omitempty"`
	}{container, endpoint}
	_, err := c.call(ctx, request{method: http.MethodPost, path: "/networks/" + url.PathEscape(network) + "/connect", body: body}, nil)
	return err
}

// DisconnectNetwork detaches a container from a network.
func (c *Client) DisconnectNetwork(ctx context.Context, network, container string, force bool) error {
	body := struct {
		Container string
		Force     bool
	}{container, force}
	_, err := c.call(ctx, request{method: http.MethodPost, path: "/networks/" + url.PathEscape(network) + "/disconnect", body: body}, nil)
	return err
}
```

- [ ] **Step 4: Run the tests and lint**

Run: `TMPDIR=/root/.cache/w0b2-tmp/11 GOTOOLCHAIN=local go test -count=1 ./internal/platform/dockerapi/ && TMPDIR=/root/.cache/w0b2-tmp/11 golangci-lint run --allow-serial-runners ./internal/platform/dockerapi/...`
Expected: `ok  	github.com/darkraise/darkpanel/internal/platform/dockerapi`, then `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add internal/platform/dockerapi/networks.go internal/platform/dockerapi/networks_test.go
git commit -m "feat(platform): add Docker network calls"
rm -rf /root/.cache/w0b2-tmp/11
```

### Task 12: Tailscale live contract test

The contract test of spec 1.8 for the Tailscale client: against a real tailscaled it reads a logged-out node's status, preferences and DERP map, patches one preference and restores it, replaces the preferences through `Start` (a changed host name must arrive and the unmodelled `NetfilterMode` must survive) and restores them, logs out, and looks up an address no node holds. `CertPair` and `DebugDERPRegion` need a tailnet; register row 251 gives them to the wave 4 and wave 5 spikes. It runs only when `DARKPANEL_TAILSCALED_SOCKET` names a socket. This task runs it against tailscaled 1.82.0, the spec's floor, and against the current stable image, in throwaway containers.

**Files:**
- Test: `internal/platform/tslocal/live_test.go`

**Interfaces:**
- Consumes: C4, C5 (`DERPMap`, `WhoIs`, `ErrPeerNotFound`).
- Produces: nothing.

**Items:** 79

**Depends on:** Task 2, Task 7

**Implementer:** dforge:impl-sonnet-low
**Evaluation:** files 0 - spec 0 - coupling 1 - risk 0 = 1

- [ ] **Step 1: Write the test**

Create `internal/platform/tslocal/live_test.go`:

```go
package tslocal

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"testing"
	"time"
)

// TestTailscaledLive drives a real tailscaled, so it runs only when
// DARKPANEL_TAILSCALED_SOCKET names its socket. It needs no tailnet: it
// reads a logged-out node's status, preferences and DERP map, patches one
// preference and restores it, replaces the preferences through Start and
// restores them, logs out, and looks up an address no node holds.
func TestTailscaledLive(t *testing.T) {
	socket := os.Getenv("DARKPANEL_TAILSCALED_SOCKET")
	if socket == "" {
		t.Skip("set DARKPANEL_TAILSCALED_SOCKET to drive a real tailscaled")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	c := New(socket)
	defer c.Close()

	var s *Status
	var err error
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(200 * time.Millisecond) {
		s, err = c.Status(ctx)
		if err == nil && s.BackendState != "NoState" || time.Now().After(deadline) {
			break
		}
	}
	if err != nil || s.Version == "" || s.BackendState == "" {
		t.Fatalf("status = %+v, %v", s, err)
	}

	before, err := c.Prefs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(before.raw) <= 12 {
		t.Fatalf("prefs kept %d fields; tailscaled sends more than the 12 modelled", len(before.raw))
	}
	flipped := !before.ShieldsUp
	after, err := c.EditPrefs(ctx, PrefsPatch{ShieldsUp: &flipped})
	if err != nil {
		t.Fatal(err)
	}
	if after.ShieldsUp != flipped || after.CorpDNS != before.CorpDNS || after.ControlURL != before.ControlURL {
		t.Fatalf("patched prefs = %+v, before %+v", after, before)
	}
	restore := before.ShieldsUp
	if _, err := c.EditPrefs(ctx, PrefsPatch{ShieldsUp: &restore}); err != nil {
		t.Fatal(err)
	}
	if restored, err := c.Prefs(ctx); err != nil || restored.ShieldsUp != before.ShieldsUp {
		t.Fatalf("restored prefs = %+v, %v", restored, err)
	}

	// Start with UpdatePrefs replaces every preference: the changed host
	// name must arrive, and NetfilterMode, which this package does not model
	// and tailscaled sets non-zero on Linux, must come back unchanged.
	cur, err := c.Prefs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	mode := string(cur.raw["NetfilterMode"])
	if mode == "" || mode == "0" {
		t.Fatalf("NetfilterMode = %q, want a non-zero value to carry through Start", mode)
	}
	renamed := *cur
	renamed.Hostname = "dp-live-check"
	if err := c.Start(ctx, StartOptions{UpdatePrefs: &renamed}); err != nil {
		t.Fatal(err)
	}
	got, err := c.Prefs(ctx)
	if err != nil || got.Hostname != "dp-live-check" || string(got.raw["NetfilterMode"]) != mode || got.WantRunning != cur.WantRunning {
		t.Fatalf("prefs after Start = %+v (NetfilterMode %s), %v", got, got.raw["NetfilterMode"], err)
	}
	if err := c.Start(ctx, StartOptions{UpdatePrefs: cur}); err != nil {
		t.Fatal(err)
	}
	if err := c.Logout(ctx); err != nil {
		t.Fatalf("logout of a logged-out node = %v", err)
	}

	if _, err := c.DERPMap(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := c.WhoIs(ctx, netip.MustParseAddr("192.0.2.1")); !errors.Is(err, ErrPeerNotFound) {
		t.Fatalf("whois of an unheld address err = %v, want ErrPeerNotFound", err)
	}
}
```

- [ ] **Step 2: Run it without a tailscaled**

Run: `mkdir -p /root/.cache/w0b2-tmp/12 && TMPDIR=/root/.cache/w0b2-tmp/12 GOTOOLCHAIN=local go test -count=1 -run TestTailscaledLive -v ./internal/platform/tslocal/`
Expected: `--- SKIP: TestTailscaledLive` with "set DARKPANEL_TAILSCALED_SOCKET to drive a real tailscaled", then `ok`.

- [ ] **Step 3: Run it against tailscaled 1.82.0 and the current stable release**

For each image in turn, start a userspace tailscaled whose socket is bind-mounted into a short host path, run the test, and remove the container:

```bash
mkdir -p /root/.cache/w0b2-tmp/12/tsd
for image in tailscale/tailscale:v1.82.0 tailscale/tailscale:stable; do
  docker run -d --rm --name dp-tsd-check-12 -v /root/.cache/w0b2-tmp/12/tsd:/sock --entrypoint tailscaled "$image" \
    --tun=userspace-networking --state=mem: --socket=/sock/tailscaled.sock
  DARKPANEL_TAILSCALED_SOCKET=/root/.cache/w0b2-tmp/12/tsd/tailscaled.sock TMPDIR=/root/.cache/w0b2-tmp/12 GOTOOLCHAIN=local \
    timeout 180 go test -count=1 -run TestTailscaledLive -v ./internal/platform/tslocal/
  docker rm -f dp-tsd-check-12
done
```

Expected: `--- PASS: TestTailscaledLive` and `ok` for both images. Afterwards `docker ps -a --filter name=dp-tsd-check-12 -q` prints nothing. Record both tailscaled versions (`docker run --rm --entrypoint tailscaled <image> --version`, first line) in the task report.

- [ ] **Step 4: Lint**

Run: `TMPDIR=/root/.cache/w0b2-tmp/12 golangci-lint run --allow-serial-runners ./internal/platform/tslocal/...`
Expected: `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add internal/platform/tslocal/live_test.go
git commit -m "test(platform): run tslocal against a real tailscaled"
rm -rf /root/.cache/w0b2-tmp/12
```

### Task 13: Docker live contract test

Drives the host's Docker daemon through every call group: negotiation and `/info` (with `FirewallBackend` at 1.49), pulling and inspecting an image, the event stream, a labelled volume, network and container through create, start (and the 304 answer to a second start), inspect, list, logs, stats, archive copy both ways, network disconnect and connect, stop (and the 304 answer to a second stop) and remove. It runs only when `DARKPANEL_DOCKER_LIVE=1`; every object carries the label `darkpanel.live` and is removed at the end, also on failure.

**Files:**
- Test: `internal/platform/dockerapi/live_test.go`

**Interfaces:**
- Consumes: C1, C2, C3.
- Produces: nothing.

**Items:** 79

**Depends on:** Task 1, Task 5, Task 6, Task 10, Task 11

**Implementer:** dforge:impl-sonnet-low
**Evaluation:** files 0 - spec 0 - coupling 1 - risk 1 = 2

- [ ] **Step 1: Write the test**

Create `internal/platform/dockerapi/live_test.go`:

```go
package dockerapi

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestDockerLive drives the host's Docker daemon, so it runs only when
// DARKPANEL_DOCKER_LIVE=1. It proves the wire shapes the fake daemon only
// asserts. Every object it creates carries the label darkpanel.live and is
// removed again; DARKPANEL_DOCKER_LIVE_IMAGE names the image (default
// alpine:3), which the test pulls.
func TestDockerLive(t *testing.T) {
	if os.Getenv("DARKPANEL_DOCKER_LIVE") != "1" {
		t.Skip("set DARKPANEL_DOCKER_LIVE=1 to drive the host's Docker daemon")
	}
	image := os.Getenv("DARKPANEL_DOCKER_LIVE_IMAGE")
	if image == "" {
		image = "alpine:3"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	c := New(DefaultSocket)
	defer c.Close()
	prefix := fmt.Sprintf("dp-live-%d-%d", os.Getpid(), time.Now().UnixNano())
	labels := map[string]string{"darkpanel.live": prefix}
	t.Cleanup(func() {
		bg := context.Background()
		_ = c.RemoveContainer(bg, prefix, RemoveContainerOptions{Force: true})
		_ = c.RemoveNetwork(bg, prefix)
		_ = c.RemoveVolume(bg, prefix, true)
	})

	v, err := c.APIVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if cmp, _ := compareVersions(v, MaxAPIVersion); cmp > 0 {
		t.Fatalf("negotiated %s above %s", v, MaxAPIVersion)
	}
	info, err := c.Info(ctx)
	if err != nil || info.ServerVersion == "" {
		t.Fatalf("info = %+v, %v", info, err)
	}
	if v == MaxAPIVersion && (info.FirewallBackend == nil || info.FirewallBackend.Driver == "") {
		t.Fatalf("API %s reports no firewall backend: %+v", v, info)
	}

	pull, err := c.PullImage(ctx, image)
	if err != nil {
		t.Fatal(err)
	}
	for {
		m, err := pull.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := m.Err(); err != nil {
			t.Fatal(err)
		}
	}
	_ = pull.Close()
	if _, err := c.InspectImage(ctx, image); err != nil {
		t.Fatal(err)
	}

	events, err := c.Events(ctx, EventsOptions{Since: time.Now(), Filters: Filters{"label": {"darkpanel.live=" + prefix}}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = events.Close() }()
	seen := make(chan string, 64)
	go func() {
		for {
			ev, err := events.Next()
			if err != nil {
				close(seen)
				return
			}
			seen <- ev.Type + "/" + ev.Action
		}
	}()

	if _, err := c.CreateVolume(ctx, VolumeCreate{Name: prefix, Labels: labels}); err != nil {
		t.Fatal(err)
	}
	netID, err := c.CreateNetwork(ctx, NetworkCreate{Name: prefix, Driver: "bridge", Labels: labels})
	if err != nil {
		t.Fatal(err)
	}
	id, err := c.CreateContainer(ctx, ContainerCreate{
		Name: prefix,
		ContainerConfig: ContainerConfig{
			Image:  image,
			Cmd:    []string{"sh", "-c", "echo out; echo err >&2; exec sleep 300"},
			Labels: labels,
		},
		HostConfig: &HostConfig{Mounts: []Mount{{Type: "volume", Source: prefix, Target: "/data"}}},
		NetworkingConfig: &NetworkingConfig{EndpointsConfig: map[string]*EndpointSettings{
			prefix: {Aliases: []string{"dpalias"}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := c.StartContainer(ctx, id); err != nil || !changed {
		t.Fatalf("start = %v, %v", changed, err)
	}
	if changed, err := c.StartContainer(ctx, id); err != nil || changed {
		t.Fatalf("second start = %v, %v; want the daemon's 304", changed, err)
	}

	got, err := c.InspectContainer(ctx, prefix)
	if err != nil {
		t.Fatal(err)
	}
	ep := got.NetworkSettings.Networks[prefix]
	if !got.State.Running || got.ID != id || ep == nil || !slices.Contains(ep.Aliases, "dpalias") || !ep.IPAddress.IsValid() {
		t.Fatalf("inspect = %+v, endpoint %+v", got, ep)
	}
	if _, err := time.Parse(time.RFC3339Nano, got.Created); err != nil {
		t.Fatalf("Created %q: %v", got.Created, err)
	}
	if len(got.Mounts) != 1 || got.Mounts[0].Name != prefix {
		t.Fatalf("mounts = %+v", got.Mounts)
	}
	n, err := c.InspectNetwork(ctx, netID)
	if err != nil || n.Containers[id].IPv4Address.Addr() != ep.IPAddress {
		t.Fatalf("network = %+v, %v", n, err)
	}
	list, err := c.ListContainers(ctx, ListContainersOptions{Filters: Filters{"label": {"darkpanel.live=" + prefix}}})
	if err != nil || len(list) != 1 || list[0].ID != id {
		t.Fatalf("list = %+v, %v", list, err)
	}

	var stdout, stderr string
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		s, err := c.Logs(ctx, id, LogsOptions{Stdout: true, Stderr: true})
		if err != nil {
			t.Fatal(err)
		}
		stdout, stderr = "", ""
		for {
			f, err := s.Next()
			if err != nil {
				break
			}
			if f.Stream == "stderr" {
				stderr += string(f.Data)
			} else {
				stdout += string(f.Data)
			}
		}
		_ = s.Close()
		if stdout == "out\n" && stderr == "err\n" {
			break
		}
	}
	if stdout != "out\n" || stderr != "err\n" {
		t.Fatalf("logs stdout %q stderr %q", stdout, stderr)
	}

	stats, err := c.Stats(ctx, id, false)
	if err != nil {
		t.Fatal(err)
	}
	sample, err := stats.Next()
	_ = stats.Close()
	if err != nil || sample.Read == "" || sample.PidsStats.Current == 0 {
		t.Fatalf("stats = %+v, %v", sample, err)
	}

	var tarball bytes.Buffer
	tw := tar.NewWriter(&tarball)
	_ = tw.WriteHeader(&tar.Header{Name: "hello", Mode: 0o644, Size: 5})
	_, _ = tw.Write([]byte("world"))
	_ = tw.Close()
	if err := c.CopyToContainer(ctx, id, "/data", &tarball); err != nil {
		t.Fatal(err)
	}
	rc, err := c.CopyFromContainer(ctx, id, "/data/hello")
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(rc)
	if hdr, err := tr.Next(); err != nil || hdr.Name != "hello" {
		t.Fatalf("archive entry = %+v, %v", hdr, err)
	}
	if b, _ := io.ReadAll(tr); string(b) != "world" {
		t.Fatalf("archive content = %q", b)
	}
	_ = rc.Close()

	if err := c.DisconnectNetwork(ctx, netID, id, false); err != nil {
		t.Fatal(err)
	}
	if err := c.ConnectNetwork(ctx, netID, id, &EndpointSettings{Aliases: []string{"again"}}); err != nil {
		t.Fatal(err)
	}
	if got, err = c.InspectContainer(ctx, id); err != nil || !slices.Contains(got.NetworkSettings.Networks[prefix].Aliases, "again") {
		t.Fatalf("reconnected endpoint = %+v, %v", got.NetworkSettings.Networks[prefix], err)
	}
	if err := c.RemoveVolume(ctx, prefix, false); !IsConflict(err) {
		t.Fatalf("removing a volume in use err = %v, want 409", err)
	}
	if changed, err := c.StopContainer(ctx, id, 2*time.Second); err != nil || !changed {
		t.Fatalf("stop = %v, %v", changed, err)
	}
	if changed, err := c.StopContainer(ctx, id, 2*time.Second); err != nil || changed {
		t.Fatalf("second stop = %v, %v; want the daemon's 304", changed, err)
	}
	if err := c.RemoveContainer(ctx, id, RemoveContainerOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := c.RemoveNetwork(ctx, netID); err != nil {
		t.Fatal(err)
	}
	if err := c.RemoveVolume(ctx, prefix, false); err != nil {
		t.Fatal(err)
	}
	if _, err := c.InspectVolume(ctx, prefix); !IsNotFound(err) {
		t.Fatalf("removed volume err = %v", err)
	}

	want := []string{"container/create", "container/start", "container/die", "container/destroy"}
	var got2 []string
	timeout := time.After(10 * time.Second)
	for len(want) > 0 {
		select {
		case ev, ok := <-seen:
			if !ok {
				t.Fatalf("event stream ended; still waiting for %v (seen %v)", want, got2)
			}
			got2 = append(got2, ev)
			if ev == want[0] {
				want = want[1:]
			}
		case <-timeout:
			t.Fatalf("still waiting for %v; seen %s", want, strings.Join(got2, " "))
		}
	}
}
```

- [ ] **Step 2: Run it without the variable**

Run: `mkdir -p /root/.cache/w0b2-tmp/13 && TMPDIR=/root/.cache/w0b2-tmp/13 GOTOOLCHAIN=local go test -count=1 -run TestDockerLive -v ./internal/platform/dockerapi/`
Expected: `--- SKIP: TestDockerLive` with "set DARKPANEL_DOCKER_LIVE=1 to drive the host's Docker daemon", then `ok`.

- [ ] **Step 3: Run it against the host's daemon**

Run: `DARKPANEL_DOCKER_LIVE=1 TMPDIR=/root/.cache/w0b2-tmp/13 GOTOOLCHAIN=local timeout 300 go test -count=1 -run TestDockerLive -v ./internal/platform/dockerapi/`
Expected: `--- PASS: TestDockerLive`, then `ok`. Afterwards `docker ps -a --filter label=darkpanel.live -q`, `docker network ls --filter label=darkpanel.live -q` and `docker volume ls --filter label=darkpanel.live -q` print nothing. Record `docker version --format '{{.Server.Version}} {{.Server.APIVersion}}'` in the task report.

- [ ] **Step 4: Lint**

Run: `TMPDIR=/root/.cache/w0b2-tmp/13 golangci-lint run --allow-serial-runners ./internal/platform/dockerapi/...`
Expected: `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add internal/platform/dockerapi/live_test.go
git commit -m "test(platform): run dockerapi against a real daemon"
rm -rf /root/.cache/w0b2-tmp/13
```
