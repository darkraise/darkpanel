# v1 wave 3: Docker module, tailnet and Cloudflare account, tunnels and hostnames Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: the skill the **Execution:** line names — dforge:subagent-driven-development for `subagent`, dforge:executing-plans for `inline`. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build wave 3 of the v1 completion spec in its three parallel parts: 3a the agent-side `docker` module (containers, images, networks, volumes, Compose projects, the three streams, the web host tabs and container page, and the M4 harness), 3b the controller-side `tailnet` module without the join at install (OAuth connection, devices, auth keys and their sweep, the policy file and its typed edits, the `/tailnet` pages), and 3c the controller-side `cloudflare` module's account, tunnels and public hostnames (with the `cloudflare.hostnames` service and the `/cloudflare` pages), each preceded by the contract check of 3b and 3c.

**Architecture:** Each module follows the firewall's shape: a `module` value holding an internal struct (`dk`, `tn`, `cf`) whose methods construct one `core.ActionDef` per action, a `deps` struct that tests replace with fakes, module-private test hosts, and one registration task per module that wires the constructors into `Manifest()` and commits the schema snapshots. Pure logic lives in small packages with no I/O: `docker/dstate` (the Docker comparator documents), `docker/compose` (the strict Compose JSON parser and the configuration refusals), `docker/logfilter` (secret masking across log frames), `tailnet/policy` (convergent HuJSON edits and the line diff) and `cloudflare/ingress` (tunnel ingress rules and the configuration hash). The two cloud modules talk to their providers only through `internal/controller/cloud`, keep their credentials sealed in their own controller tables, journal every provider write as a non-`Local` effect with `cloud.CommitWait`, and are tested against in-process fake servers in `internal/modules/<id>/fake`.

**Tech Stack:** Go 1.27 (`GOTOOLCHAIN=local`), standard library plus `github.com/tailscale/hujson` (already in `go.mod`); `internal/platform/dockerapi`, `internal/controller/cloud` and `cloud/cloudfake`, `internal/core/{lifecycle,store,schema,ext,coretest}`; `database/sql` over `modernc.org/sqlite`; Docker CE 29.9.1 and the Compose plugin 5.6.0 on the harness VMs; web: TypeScript, React 19, `react-router` 8, `@tanstack/react-query`, `darkraise-ui`, Vitest with jsdom, Testing Library and `vitest-axe`; harness: `test/harness` (build tag `harness`) on VMs in CI.

**Spec:** docs/dforge/specs/2026-10-21-v1-completion-design.md

**Execution:** subagent — `claude --model sonnet --effort high` — the plan runs parallel waves, and each task's **Implementer:** line chooses its implementer

**Parallelism:** waves — 1-3 | 4-6 | 7-9 | 10 | 11 | 12 | 13 | 14-16 | 17-19 | 20-22 | 23 | 24 | 25 | 26 | 27-29 | 30-32 | 33-35 | 36 | 37 | 38 | 39 | 40 | 41 | 42 | 43 | 44-46 | 47-48 | 49 | 50 | 51 | 52 | 53-54 | 55 | 56 | 57-59 | 60

**Integration check:** `mkdir -p /root/.cache/w3-tmp/integ && export TMPDIR=/root/.cache/w3-tmp/integ GOTOOLCHAIN=local && timeout 600 go build ./... && timeout 300 go vet ./... && timeout 300 go vet -tags harness ./test/... && timeout 1500 go test -count=1 ./... && timeout 900 golangci-lint run --allow-serial-runners ./... && timeout 600 golangci-lint run --allow-serial-runners --build-tags harness ./test/... && timeout 300 go mod tidy -diff && timeout 1200 make lint-web test-web`

**Worktree setup:** `timeout 600 make web-deps`

**Wave check:** `mkdir -p /root/.cache/w3-tmp/wave && export TMPDIR=/root/.cache/w3-tmp/wave GOTOOLCHAIN=local && timeout 600 go build ./... && timeout 300 go vet ./... && timeout 900 go test -count=1 ./internal/modules/... ./internal/platform/dockerapi/... ./schemas/...`

## Global Constraints

- Scope is spec revision 8 as amended on `main`: section 3 (R50–R55), section 4.2 without the join at install (R58, R59; `tailnet.node.join`, `join*.go`, `host-create.tsx`, the enrollment bundle and `install.sh.in` are wave 4), sections 7.1 and 7.3 without the connector and the expose composites (`connector*.go`, `expose*.go`, `cloudflare.network.attach`/`detach`, `cloudflare_attachments` and `web/src/modules/cloudflare/container-actions.tsx` are wave 6b), section 1.9's sweeps, accounts and services for these two modules, the docker, tailnet and cloudflare rows of section 5, the M4 row of Verification, and the register rows the tasks' **Items:** lines name in `docs/dforge/registers/2026-10-21-v1-completion.md`.
- Host safety (absolute). This host is live and runs an installed `darkpanel-agent` and `darkpanel-controller` and a Docker daemon. Never run `docker` against the host daemon with anything but `version`, `compose version`, and `compose --project-directory <dir> -p <name> -f <file>… config --format json` on files inside the task's own `TMPDIR` with `COMPOSE_EXPERIMENTAL_GIT_REMOTE=false COMPOSE_EXPERIMENTAL_OCI_REMOTE=false DOCKER_CONFIG=<an empty directory in TMPDIR>`; never write under `/var/lib/darkpanel`, `/etc/darkpanel`, `/run/darkpanel`, `/etc/docker` or `/usr/libexec/docker`; never start, stop, restart or reload any systemd unit; never talk to `/var/run/docker.sock` from a Go test (the gated `TestDockerLive` runs only on harness VMs). No test reaches Tailscale or Cloudflare: every provider call goes to the module's fake server on `127.0.0.1`. Only Task 1 reads the providers' public documentation over the network.
- Every `go`, `golangci-lint`, `make` and `pnpm` command runs with `GOTOOLCHAIN=local`, an explicit `timeout`, and `TMPDIR=/root/.cache/w3-tmp/<task number>` (`mkdir -p` it first; remove it and run `go clean -testcache` when the task ends), also where a step's command omits one of them: `/tmp` is an 8 GiB tmpfs and Unix socket paths over 108 bytes fail. When `df -h /` passes 90 %, trim Go build cache entries older than a few hours.
- Dependencies: no task changes `go.mod`, `go.sum`, `web/package.json` or `web/pnpm-lock.yaml`; only the standard library and modules already required are used, and after every task `GOTOOLCHAIN=local timeout 300 go mod tidy -diff` prints nothing.
- After every task all of these pass, with their real output recorded: `go build ./...`; `go vet ./...` (no output); `go test -count=1 ./...` (every line `ok` or `?`); `go test -race -count=1` on the task's packages; `golangci-lint run --allow-serial-runners ./...` (`0 issues.`); `go mod tidy -diff` (no output). A task that edits `test/harness/` also runs `go vet -tags harness ./test/...` and `go test -tags harness -count=1 ./test/harness/...`. A task that edits `web/` also runs `pnpm -C web lint`, `pnpm -C web typecheck` and `pnpm -C web test`.
- TDD: write the test, run it and see the expected RED (usually a build failure naming the first undefined identifier), implement, run GREEN.
- Frozen files (spec 1.12), which no task edits: `internal/core/contract.go`, `units.go`, `define.go`, `registry.go`, `internal/core/ext/**`, `internal/core/errcode/errcode.go`, `internal/core/lifecycle/**`, the core migrations, `internal/platform/exec.go`, `files.go`, `systemd.go`, `internal/modules/modules.go`, `internal/cli/modules.go`, `internal/controller/server/**`, `internal/controller/api/**`, `internal/controller/events/**`, `internal/controller/listen/sources.go`, `cmd/darkpanel/paths.go`, `go.mod`, `go.sum`, `web/package.json`, `web/pnpm-lock.yaml`, `web/src/modules/registry.ts`, `web/src/modules/types.ts`, `web/src/app.tsx`, `test/harness/pins.go`. Outside the spec's list, this plan's scope also leaves untouched (wave 0 owns them or no task needs them): `internal/core/stream.go`, `background.go`, `internal/platform/exec_rules.go`, `internal/controller/cloud/**`, `web/src/modules/registry.test.tsx`, `test/harness/images.json`, `Makefile`. A task that finds a missing code, allowlist rule or extension point in a frozen file stops and reports it; it does not edit the file. Every program and argument vector this plan runs is already allowlisted (Assumptions).
- A task edits only the files of its own **Files:** block. Each task of a wave runs in its own worktree from the wave's base (`main` plus every merged earlier wave), so a package-wide command sees only merged work plus the task's own files. "The base" in a task's text means the work its **Depends on** line names.
- Test isolation: `t.TempDir()` for files and databases; TCP only on `127.0.0.1` port 0 (`httptest` and `cloudfake` servers); `coretest.NewFakeClock` wherever a duration above one second is waited on; no fixed path outside a temporary directory; no global mutable state shared between tests of different tasks (the pull hub, token cache and fakes are values a test constructs). Tasks of one wave therefore share no port, path or global state.
- Tasks that share a Go package declare no top-level identifier, test helpers included, that another task declares. Every new test identifier carries its task's prefix from Contracts "Test prefixes", except the shared helpers that table names, which only their owning task declares.
- Code style: match the surrounding code; a doc comment on every exported identifier; comments only for a non-obvious why, in full sentences, never naming a plan, task, register row or review; `//nolint` names the rule and a reason; gofmt clean. Error strings start with the package name (`docker:`, `dstate:`, `compose:`, `logfilter:`, `tailnet:`, `policy:`, `cloudflare:`, `ingress:`, `fake:`, `dockerapi:`, `harness:`). Refusals are `*errcode.Error` with client-safe details using only codes already in `internal/core/errcode/errcode.go` (`docker.engine_unavailable`, `docker.in_use`, `docker.owned`, `docker.compose_unavailable`, `tailnet.policy_conflict`, `tailnet.policy_invalid`, `cloud.*`, `cloudflare.reserved`, `request.invalid_input`, `not_found`); a refusal that committed nothing is wrapped with `core.Definite`. No detail, diff line, `StateDoc`, effect, log line or error carries a secret (OAuth client secret, access token, API token, auth key, tunnel token, environment value).
- Lock keys: every docker mutating action locks `docker`, every tailnet mutating action `tailnet`, every cloudflare mutating action `cloudflare`; read actions take none. Docker Engine calls are effects with `Local: false` and `CommitWait: 2 * time.Minute`; provider writes are effects with `Local: false` and `CommitWait: cloud.CommitWait`; controller database writes that an undo reverses are effects with `Local: true`.
- Values copied from the spec, used exactly: Docker Engine API 1.44 minimum; the Compose vector `/usr/bin/docker compose --project-directory <dir> -p <name> -f <file>… [--env-file <f>]… [--profile <p>]… <verb>` with the verbs `up -d --no-build`, `down`, `pull`, `restart` and `config --format json`; `docker-config` at `/var/lib/darkpanel/agent/docker-config`; the system plugin directory `/usr/libexec/docker/cli-plugins`; the redaction placeholder `[redacted]`; tunnel names `darkpanel-<change id>`; DNS record comments `darkpanel:<change id>`; CNAME content `<tunnel id>.cfargotunnel.com`, proxied; the catch-all rule `http_status:404`; auth-key descriptions `darkpanel-<change id>`; the OAuth token endpoint `POST /api/v2/oauth/token`, tokens renewed at 50 minutes; the key sweep every 5 minutes until request time plus requested expiry plus 24 hours, then daily, and the daily orphan sweep with the 15 s + 5 min grace for `requested` rows.
- Commits use `<type>(<scope>): <subject>`, type one of feat|fix|docs|style|refactor|test|chore|perf, subject at most 50 characters, imperative, no trailing period, no attribution of any kind. English only.
- The plan ends with the gates in `docs/dforge/gates.md`. Tasks 54, 59 and 60 touch `test/harness/` or `.github/workflows/harness.yml`, so gate 7 (harness) applies to the branch once it is pushed, as well as on `main`; dispatch `harness.yml` on the branch per the owner's standing authorization. No task creates or pushes a tag.

## Contracts

Names are given with their package; everything not exported is used only inside its package. A task's Interfaces block cites these entries by their heading ("Contracts: Docker internals").

### Test prefixes

| Package | Task: prefix |
|---|---|
| `internal/platform/dockerapi` | 2: `eg` |
| `docker/dstate` | 3: `ds` |
| `docker/compose` | 4: `cp` (shared helper `cpLoad(t *testing.T, name string) []byte`, reading `testdata/<name>.json`), 24: `cv` |
| `docker/logfilter` | 11: `lf` |
| `docker` (module root) | 5: `dk` (shared helpers of Contracts "Docker test host"), 14: `mf`, 15: `ef`, 16: `rs`, 25: `st`, 26: `pj` (shared helper `pjDB`), 27: `cr`, 28: `rm`, 29: `im`, 30: `ob`, 31: `ep`, 36: `rd`, 37: `pl`, 38: `cc` (shared helper `ccProject`), 41: `cu`, 44: `reg` |
| `tailnet/fake` | 17: `fk` |
| `tailnet/policy` | 6: `ed` |
| `tailnet` (module root) | 12: `tn` (shared helpers of Contracts "Tailnet store"), 39: `ap` (shared helper `tnWith`), 42: `ac` (shared helper `acConnect`), 45: `dv`, 46: `dx`, 49: `ak`, 50: `sw`, 51: `po`, 55: `pp`, 57: `reg` |
| `cloudflare/fake` | 18: `fk` |
| `cloudflare/ingress` | 7: `in` |
| `cloudflare` (module root) | 13: `cf` (shared helpers of Contracts "Cloudflare store"), 32: `ap` (shared helper `cfWith`), 43: `ac` (shared helper `acConnect`), 47: `tu` (shared helper `tuTunnel`), 48: `tr`, 52: `ha` (shared helper `haAdd`), 53: `hs`, 56: `hr`, 58: `reg` |
| `test/harness` | 54: `m4` (shared helpers of Contracts "Harness M4"), 59: `m4c`, 60: `m4l` |
| web (`web/src/modules/<id>/`, `web/src/components/`) | each test file uses its own file's prefix; shared fixtures only in each module's `fixtures.ts` |

### Dependencies

Each task's **Depends on** line is exactly this: 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13: none; 14, 16: Task 5; 15: Task 3, Task 5; 17, 18: Task 1; 19, 20: Task 8; 21, 23: Task 9; 22: Task 9, Task 10; 24: Task 4; 25: Task 5, Task 11; 26: Task 5; 27, 28, 29, 30, 31: Task 15; 32: Task 18; 33: Task 19; 34: Task 21, Task 23; 35: Task 10, Task 22; 36: Task 15; 37: Task 15; 38: Task 15, Task 24, Task 26; 39: Task 17; 40: Task 20, Task 33; 41: Task 38; 42: Task 12, Task 39; 43: Task 13, Task 32; 44: Task 14, Task 16, Task 25, Task 26, Task 27, Task 28, Task 29, Task 30, Task 31, Task 36, Task 37, Task 41; 45, 46, 49: Task 42; 47: Task 43; 48: Task 7, Task 43; 50: Task 42; 51: Task 6, Task 42; 52: Task 7, Task 43; 53: Task 52; 54: Task 44; 55: Task 51; 56: Task 52; 57: Task 45, Task 46, Task 49, Task 50, Task 51, Task 55; 58: Task 47, Task 48, Task 53, Task 56; 59: Task 54; 60: Task 59.

### dockerapi guards (from Task 2)

Every `*dockerapi.Client` method that takes an object id, name or reference (`InspectContainer`, `StartContainer`, `StopContainer`, `RestartContainer`, `RemoveContainer`, `InspectImage`, `PullImage`, `RemoveImage`, `InspectNetwork`, `RemoveNetwork`, `ConnectNetwork`, `DisconnectNetwork` (both arguments), `InspectVolume`, `RemoveVolume`, `Logs`, `Stats`, `CopyFromContainer`, `CopyToContainer`) returns, before any request, an error wrapping the new exported sentinel `dockerapi.ErrEmptyID` (`errors.New("dockerapi: empty id")`) when that argument is `""`. `APIVersion` wraps a cancelled waiter's context error as `fmt.Errorf("dockerapi: negotiate API version: %w", ctx.Err())`.

### dstate (from Task 3)

Package `internal/modules/docker/dstate` (doc comment in `dstate.go`) imports only the standard library and `internal/platform/dockerapi`; it reads no host state. It renders spec 3's comparator table as `StateDoc.State` JSON, normalized: instants as UTC RFC 3339 with nanoseconds (`time.RFC3339Nano`), Docker's zero time `0001-01-01T00:00:00Z` and `""` as an absent member, lists of names as sorted sets, an absent and an empty list or map both omitted, addresses in canonical `netip` form.

```go
type Phase int

const (
	Recorded Phase = iota // the post-state, recorded after the action and compared before an undo
	Restored              // the state an undo restores, journaled as Before and compared after the inverse
)

// ContainerRun: Recorded {"id","created","running","startedAt","finishedAt"}; Restored {"id","created","running"}.
func ContainerRun(c dockerapi.ContainerJSON, p Phase) (json.RawMessage, error)
// ContainerRunning is the Restored document of a container with the given identity and running flag.
func ContainerRunning(id, created string, running bool) (json.RawMessage, error)
// Endpoint: {"container","network","present"} plus, when present, "aliases" and "ipamConfig" ({"ipv4","ipv6"});
// Recorded adds "endpointId". ep nil means absent.
func Endpoint(containerID, networkID string, ep *dockerapi.EndpointSettings, p Phase) (json.RawMessage, error)
// NetworkCreated: n nil → null; else {"id","created","labels","unused"} with unused = len(n.Containers) == 0 (n from InspectNetwork).
func NetworkCreated(n *dockerapi.Network) (json.RawMessage, error)
// VolumeCreated: v nil → null; else {"name","labels","unused"}.
func VolumeCreated(v *dockerapi.Volume, unused bool) (json.RawMessage, error)
// Instant normalizes one Docker timestamp; "" for the zero time and "".
func Instant(s string) (string, error)
```

### compose parser (from Task 4)

Package `internal/modules/docker/compose` (doc comment in `config.go`) imports only the standard library. `Parse` reads `docker compose config --format json` output strictly: a single JSON object, at most `MaxConfigBytes`, no trailing data, the members it reads of the types Compose documents (any other type is an error); it records every key so `Validate` can refuse unknown ones.

```go
const MaxConfigBytes = 4 << 20

type Config struct {
	Name     string
	TopKeys  []string           // every top-level key, sorted
	Services map[string]Service // by service name
	Networks map[string]Object  // top-level networks, volumes, configs, secrets by key
	Volumes  map[string]Object
	Configs  map[string]Object
	Secrets  map[string]Object
}
type Service struct {
	Keys          []string          // every key of the service, sorted
	Image         string
	Build         bool              // a build member is present
	Provider      bool              // a provider member is present
	ContainerName string
	Networks      []string          // network keys the service joins, sorted
	Volumes       []string          // named-volume sources of its volume mounts, sorted
	Labels        map[string]string
}
type Object struct {
	Keys     []string // every key of the object, sorted
	Name     string   // the resolved "name" member, "" when absent
	External bool
	Labels   map[string]string
}
func Parse(data []byte) (Config, error)
// ImageName is the image a service runs: its image member, else "<project>-<service>" (Compose's name for a built image).
func ImageName(project, service string, s Service) string
```

Test data: `internal/modules/docker/compose/testdata/*.json`, real `config --format json` output captured on this host (Assumptions), named by scenario (`simple`, `build`, `provider`, `profiles`, `external`, `include`).

### compose refusals (from Task 24)

Same package, file `validate.go`:

```go
var TopLevelKeys, ServiceKeys, NetworkKeys, VolumeKeys, ConfigKeys, SecretKeys []string // the keys the pinned Compose version documents; source URL in a comment
type Facts struct {
	ImagePresent func(ref string) bool          // an image is present locally
	Owner        func(kind, name string) string // the darkpanel.module label of an Engine object; kind "container" | "network" | "volume"; "" when unlabelled or absent
}
// Validate refuses with *errcode.Error: docker.owned for a project named darkpanel* or a configuration naming an owned
// container, network or volume; docker.compose_unavailable for a provider service, a key outside the allowlists, or
// a service with a build member whose ImageName is not present. The detail names the service, key or object.
func Validate(project string, c Config, f Facts) error
```

### logfilter (from Task 11)

Package `internal/modules/docker/logfilter` imports only the standard library.

```go
const Mask = "[redacted]"
type Filter struct{ /* unexported */ }
// New builds a filter for secrets; empty secrets are ignored, and with none the filter passes data through unchanged.
func New(secrets [][]byte) *Filter
// Push takes the next bytes of one stream ("stdout" or "stderr") and returns the bytes now safe to emit, every
// secret replaced by Mask, even when a secret spans calls. It holds back only the longest suffix that is a proper
// prefix of some secret.
func (f *Filter) Push(stream string, data []byte) []byte
// Flush returns the bytes held for stream, masked, and forgets them.
func (f *Filter) Flush(stream string) []byte
```

### Docker internals (from Tasks 5, 14, 15)

Package `internal/modules/docker`. Task 5 writes `deps.go` and changes `docker.go`'s `module` and `Module()`:

```go
type module struct{ k dk }
func Module() core.Module // module{k: productionDK()}
type dk struct{ d deps }
type deps struct {
	docker   dockerAPI
	lstat    func(name string) (fs.FileInfo, error) // os.Lstat; Sys() is *syscall.Stat_t in production
	readFile func(name string) ([]byte, error)      // os.ReadFile
	secrets  func(ctx context.Context, env core.Env, containerID string) ([][]byte, error) // production: none, nil error
}
func productionDK() dk // dockerapi.New(dockerapi.DefaultSocket) behind newDockerClient; opens no connection until a call
func newDockerClient(socket string) dockerAPI
type dockerAPI interface {
	Version(ctx context.Context) (dockerapi.VersionInfo, error)
	Info(ctx context.Context) (dockerapi.SystemInfo, error)
	ListContainers(ctx context.Context, o dockerapi.ListContainersOptions) ([]dockerapi.ContainerSummary, error)
	InspectContainer(ctx context.Context, id string) (dockerapi.ContainerJSON, error)
	StartContainer(ctx context.Context, id string) (bool, error)
	StopContainer(ctx context.Context, id string, timeout time.Duration) (bool, error)
	RestartContainer(ctx context.Context, id string, timeout time.Duration) error
	RemoveContainer(ctx context.Context, id string, o dockerapi.RemoveContainerOptions) error
	ListImages(ctx context.Context, o dockerapi.ListImagesOptions) ([]dockerapi.ImageSummary, error)
	InspectImage(ctx context.Context, ref string) (dockerapi.ImageInspect, error)
	PullImage(ctx context.Context, ref string) (pullStream, error)
	RemoveImage(ctx context.Context, ref string, o dockerapi.RemoveImageOptions) ([]dockerapi.ImageDeleteItem, error)
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
	Logs(ctx context.Context, id string, o dockerapi.LogsOptions) (logStream, error)
	Stats(ctx context.Context, id string, stream bool) (statsStream, error)
}
type pullStream interface{ Next() (dockerapi.PullMessage, error); Close() error }
type logStream interface{ Next() (dockerapi.LogFrame, error); Close() error }
type statsStream interface{ Next() (dockerapi.Stats, error); Close() error }
const (
	lockKey     = "docker"
	commitWait  = 2 * time.Minute
	stopTimeout = 10 * time.Second
)
```

The adapter returns a nil interface, never a typed nil, when a stream call fails.

Task 14 writes `preflight.go` and `engine.go` and changes `Manifest()` to add `Preflight: k.preflight()`: `func (k dk) preflight() []core.PreflightCheck` (one check, id `docker.engine`, error text the reason: socket missing, daemon unreachable, API below 1.44) and `func (k dk) engineResource() core.ResourceDef` (`docker.engine`).

Task 15 writes `effects.go`, `observe.go` and `owned.go`:

```go
func containerResource(id string) string                  // "docker.container:<id>"
func endpointResource(containerID, networkID string) string // "docker.endpoint:<container id>@<network id>"
func networkResource(id string) string                     // "docker.network:<id>"; creation "docker.network:?<name>", Recovery {Kind: "name", Value: <name>}
func volumeResource(name string) string                    // "docker.volume:<name>"; creation "docker.volume:?<name>", Recovery {Kind: "name", Value: <name>}
func dockerEffect(kind core.EffectKind, resource string, before json.RawMessage) core.Effect // Local false, CommitWait commitWait; a creation also sets Recovery
// run journals one Engine call: Intend(e), do, then Made with the Recorded post-state of the resource do names (or
// e.Resource), or Skipped when do fails with a refusal the daemon answered before changing anything (4xx), which run
// returns wrapped in core.Definite; any other failure is returned unwrapped so the engine treats the call as uncertain.
func (k dk) run(ctx context.Context, env core.Env, e core.Effect, do func(ctx context.Context) (resource string, err error)) error
// observe is every docker handler's Observer: it reads the effect's resource by prefix and renders it with dstate in
// phaseOf(e); a creation not yet named is found by its Recovery name (Matches, Candidates).
func (k dk) observe(ctx context.Context, env core.Env, e core.Effect) (core.Observation, error)
func phaseOf(e core.Effect) dstate.Phase // Restored when e.State is undoing or undone, else Recorded
func ownerOf(labels map[string]string) string // the darkpanel.module label, "" when absent
// refuseOwned is nil for an unlabelled object and otherwise core.Definite(*errcode.Error docker.owned "<kind> <name> belongs to module <id>").
func refuseOwned(kind, name string, labels map[string]string) error
// engineError maps a dockerapi error: ErrUnavailable → docker.engine_unavailable, IsNotFound → not_found naming kind and name, ErrEmptyID → request.invalid_input; others unchanged.
func engineError(kind, name string, err error) error
```

### Docker test host (from Task 5)

Module-private test helpers in package `docker`, declared only by Task 5:

- `fake_docker_test.go`: `type fakeDocker struct` implementing `dockerAPI` over in-memory containers, images, networks and volumes; `newFakeDocker(clock *coretest.FakeClock) *fakeDocker`; `(*fakeDocker).put(c dockerapi.ContainerJSON)`, `putImage(i dockerapi.ImageSummary)`, `putNetwork(n dockerapi.Network)`, `putVolume(v dockerapi.Volume)`, `container(id string) (dockerapi.ContainerJSON, bool)`, `fail(method string, err error)` (the next call of the named method returns err and changes nothing), `calls() []string` (method names in order), and the fields `pulls map[string][]dockerapi.PullMessage`, `logs map[string][]dockerapi.LogFrame`, `stats map[string][]dockerapi.Stats`. Semantics: ids and names both find a container (name without the leading `/`); start sets `State.Running` and `StartedAt` to the clock's time and answers `false` when already running; stop sets `FinishedAt` and answers `false` when already stopped; restart sets a new `StartedAt`; connect adds an endpoint with a fresh `EndpointID`; removing a network or volume a container uses, and an absent object, answer `*dockerapi.APIError` 409 and 404; `CreateVolume` of an existing name returns it unchanged.
- Builders: `dkContainer(name string, opts ...func(*dockerapi.ContainerJSON)) dockerapi.ContainerJSON` (id `dkID(name)`, `Name` `"/"+name`, created `2026-10-01T00:00:00Z`, stopped), options `dkRunning`, `dkLabels(map[string]string)`, `dkPorts(...string)` (`"8080:80/tcp"`), `dkOnNetwork(networkID string)`; `dkID(seed string) string` (64 hex digits from SHA-256); `dkNetwork(name string, opts ...func(*dockerapi.Network)) dockerapi.Network`; `dkVolume(name string, labels map[string]string) dockerapi.Volume`; `dkImage(ref string, size int64) dockerapi.ImageSummary`.
- `host_test.go`: `type dkHost struct{ t *testing.T; clock *coretest.FakeClock; docker *fakeDocker; journal *dkJournal; files map[string]dkFile; managed map[string][]byte; cmds []core.Cmd; results map[string]core.CmdResult; secrets [][]byte; db *store.DB }` with `newDKHost(t *testing.T) *dkHost`; it implements `core.Runner` (records each `Cmd`, answers `results[strings.Join(cmd.Args, " ")]`, otherwise exit 0) and `core.FileWriter` (`WriteManaged`/`RemoveManaged` record into `managed` keyed `<dir>/<name>`; every other method fails); `dkFile{Mode fs.FileMode; UID int; Symlink bool; Data []byte}` backs `lstat` and `readFile`; `(h *dkHost) dk() dk` wires the fakes into `deps`; `(h *dkHost) env(changeID string) core.Env` (`HostID` `"h1"`, `ChangeID`, `Principal` admin, `Effects` the journal when `changeID != ""`, `DB` `h.db`, `Exec` and `Files` `h`, `Sealer` `coretest.Sealer{}`, `Clock`, `Log` discarding). `dkJournal` is an in-memory `core.EffectJournal` that also offers `effects() []core.Effect`.

### Docker wire shapes (Tasks 14, 16, 25–31, 36, 37, 41; read by Tasks 8, 44 and the harness)

Constructors are methods of `dk` returning `core.ActionDef`, `core.ResourceDef` or `core.StreamDef`; each action's handler type implements `Observe` through `k.observe` and, when compensable, `Inverse`. JSON members are exactly the tags below; times are RFC 3339.

| Action / resource / stream | Constructor (task) | Class, extras | Input → output |
|---|---|---|---|
| `docker.engine` | `engineResource` (14) | resource | → `Engine` |
| `docker.containers`, `docker.images`, `docker.networks`, `docker.volumes` | `containersResource`, `imagesResource`, `networksResource`, `volumesResource` (16) | resources | → `ContainerList`, `ImageList`, `NetworkList`, `VolumeList` |
| `docker.projects` | `projectsResource` (26) | resource | → `ProjectList` |
| `docker.container.start`, `.stop`, `.restart` | `containerStart`, `containerStop`, `containerRestart` (27) | compensable, Undoer | `ContainerInput` → `ContainerState` |
| `docker.container.remove` | `containerRemove` (28) | irreversible, confirmation the container name | `ContainerRemoveInput` → `Removed` |
| `docker.image.remove` | `imageRemove` (29) | irreversible, confirmation the reference as given | `ImageRemoveInput` → `RemovedImage` |
| `docker.image.prune` | `imagePrune` (29) | irreversible, confirmation `prune` | `ImagePruneInput` → `Pruned` |
| `docker.network.create`, `docker.volume.create` | `networkCreate`, `volumeCreate` (30) | compensable, Undoer | `NetworkCreateInput`, `VolumeCreateInput` → `Created` |
| `docker.network.remove`, `docker.volume.remove` | `networkRemove`, `volumeRemove` (30) | irreversible, confirmation the name | `NetworkInput`, `VolumeInput` → `Removed` |
| `docker.network.connect`, `.disconnect` | `networkConnect`, `networkDisconnect` (31) | compensable, Undoer | `ConnectInput`, `DisconnectInput` → `EndpointView` |
| `docker.container.inspect`, `.logs` | `containerInspect`, `containerLogs` (36) | read | `ContainerInput`, `LogsInput` → `ContainerDetail`, `LogsOutput` |
| `docker.container.reveal-env` | `containerRevealEnv` (36) | read, `DefineReveal`, admin | `ContainerInput` → `RevealedEnv` |
| `docker.image.pull` | `imagePull(h *pullHub)` (37) | compensable, Undoer that journals nothing, `LongRunning`, `ApplyTimeout` 30 min | `ImagePullInput` → `PulledImage` |
| `docker.compose.up`, `.down` | `composeUp`, `composeDown` (41) | irreversible, confirmation the project name, `MinScope` admin, `LongRunning`, `ApplyTimeout` 15 min / 10 min | `ComposeInput` → `ComposeResult` |
| `docker.compose.pull` | `composePull` (41) | compensable, Undoer that journals nothing, admin, `LongRunning`, 30 min | `ComposeInput` → `ComposeResult` |
| `docker.compose.restart` | `composeRestart` (41) | compensable, Undoer (`container.run` per container running before), admin, `LongRunning`, 10 min | `ComposeInput` → `ComposeResult` |
| stream `docker.logs`, `docker.stats` | `logsStream`, `statsStream` (25) | stream | `LogsParams` → `LogEvent`; `StatsParams` → `StatsEvent` |
| stream `docker.pull` | `pullStreamDef(h *pullHub)` (37) | stream | `PullParams` → `PullEvent` |

Every other mutating spec value is fixed by its class: synchronous actions keep the 30 s default `ApplyTimeout`.

```go
type Engine struct {
	Available  bool   `json:"available"`
	Reason     string `json:"reason,omitempty"`
	Version    string `json:"version,omitempty"`
	APIVersion string `json:"apiVersion,omitempty"`
	OS         string `json:"os,omitempty"`
	Arch       string `json:"arch,omitempty"`
	Containers int    `json:"containers"`
	Running    int    `json:"running"`
	Images     int    `json:"images"`
}
type Port struct {
	IP          string `json:"ip,omitempty"`
	PrivatePort uint16 `json:"privatePort"`
	PublicPort  uint16 `json:"publicPort,omitempty"`
	Proto       string `json:"proto"`
}
type Usage struct {
	CPUPercent float64 `json:"cpuPercent"`
	MemUsage   uint64  `json:"memUsage"`
	MemLimit   uint64  `json:"memLimit"`
}
type Container struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"` // without Docker's leading "/"
	Image   string            `json:"image"`
	ImageID string            `json:"imageId"`
	State   string            `json:"state"` // Docker's State
	Status  string            `json:"status"`
	Created time.Time         `json:"created"`
	Ports   []Port            `json:"ports"`
	Labels  map[string]string `json:"labels,omitempty"`
	Project string            `json:"project,omitempty"` // com.docker.compose.project
	Service string            `json:"service,omitempty"` // com.docker.compose.service
	Owner   string            `json:"owner,omitempty"`   // darkpanel.module
	Usage   *Usage            `json:"usage,omitempty"`   // running containers; nil when the one-shot stats read failed
}
type ContainerList struct{ Containers []Container `json:"containers"` }
type Image struct {
	ID         string    `json:"id"`
	Tags       []string  `json:"tags"`
	Digests    []string  `json:"digests"`
	Size       int64     `json:"size"`
	Created    time.Time `json:"created"`
	Containers []string  `json:"containers"` // names of containers using it
	Dangling   bool      `json:"dangling"`
}
type ImageList struct{ Images []Image `json:"images"` }
type Network struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Driver     string    `json:"driver"`
	Scope      string    `json:"scope"`
	Internal   bool      `json:"internal"`
	IPv6       bool      `json:"ipv6"`
	Subnets    []string  `json:"subnets"`
	Containers []string  `json:"containers"` // names
	Owner      string    `json:"owner,omitempty"`
	Created    time.Time `json:"created"`
}
type NetworkList struct{ Networks []Network `json:"networks"` }
type Volume struct {
	Name       string            `json:"name"`
	Driver     string            `json:"driver"`
	Mountpoint string            `json:"mountpoint"`
	Created    time.Time         `json:"created"`
	Labels     map[string]string `json:"labels,omitempty"`
	Containers []string          `json:"containers"` // names
	Owner      string            `json:"owner,omitempty"`
}
type VolumeList struct{ Volumes []Volume `json:"volumes"` }
type Project struct {
	Name        string     `json:"name"`
	Directory   string     `json:"directory"`
	Files       []string   `json:"files"`
	EnvFiles    []string   `json:"envFiles"`
	Profiles    []string   `json:"profiles"`
	ConfigHash  string     `json:"configHash"`  // "" when the files cannot be read
	HashChanged bool       `json:"hashChanged"` // differs from the hash of the last run
	Containers  []string   `json:"containers"`  // names, current
	Running     int        `json:"running"`
	FirstSeen   time.Time  `json:"firstSeen"`
	LastRun     *time.Time `json:"lastRun,omitempty"`
}
type ProjectList struct{ Projects []Project `json:"projects"` }

type ContainerInput struct{ Container string `json:"container"` } // id or name
type ContainerState struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Running bool   `json:"running"`
}
type ContainerRemoveInput struct {
	Container string `json:"container"`
	Force     bool   `json:"force,omitempty"`
	Volumes   bool   `json:"volumes,omitempty"`
}
type Removed struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type EnvVar struct {
	Name  string `json:"name"`
	Value string `json:"value"` // "[redacted]" in ContainerDetail
}
type Mount struct {
	Type        string `json:"type"`
	Name        string `json:"name,omitempty"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
	RW          bool   `json:"rw"`
}
type EndpointView struct {
	Network    string   `json:"network"` // name
	NetworkID  string   `json:"networkId"`
	Container  string   `json:"container"` // name
	EndpointID string   `json:"endpointId,omitempty"`
	Aliases    []string `json:"aliases,omitempty"`
	IPv4       string   `json:"ipv4,omitempty"`
	IPv6       string   `json:"ipv6,omitempty"`
}
type ContainerDetail struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	Image         string            `json:"image"`
	ImageID       string            `json:"imageId"`
	Created       time.Time         `json:"created"`
	State         string            `json:"state"`
	Running       bool              `json:"running"`
	StartedAt     string            `json:"startedAt,omitempty"`
	FinishedAt    string            `json:"finishedAt,omitempty"`
	ExitCode      int               `json:"exitCode"`
	RestartPolicy string            `json:"restartPolicy,omitempty"`
	Cmd           []string          `json:"cmd,omitempty"`
	Entrypoint    []string          `json:"entrypoint,omitempty"`
	WorkingDir    string            `json:"workingDir,omitempty"`
	User          string            `json:"user,omitempty"`
	Env           []EnvVar          `json:"env"` // values redacted for every container
	Labels        map[string]string `json:"labels,omitempty"`
	Ports         []Port            `json:"ports"` // published bindings: ip, publicPort, privatePort, proto
	Mounts        []Mount           `json:"mounts"`
	Networks      []EndpointView    `json:"networks"`
	Project       string            `json:"project,omitempty"`
	Service       string            `json:"service,omitempty"`
	Owner         string            `json:"owner,omitempty"`
}
type LogsInput struct {
	Container  string `json:"container"`
	Tail       int    `json:"tail,omitempty"`  // 1–5000, default 200
	Since      string `json:"since,omitempty"` // RFC 3339
	Timestamps bool   `json:"timestamps,omitempty"`
}
type LogLine struct {
	Stream string `json:"stream"` // stdout | stderr
	Text   string `json:"text"`
}
type LogsOutput struct{ Lines []LogLine `json:"lines"` }
type RevealedEnv struct{ Env []EnvVar `json:"env" secret:"true"` }
type ImagePullInput struct{ Image string `json:"image"` }
type PulledImage struct {
	Image   string   `json:"image"`
	ID      string   `json:"id"`
	Digests []string `json:"digests"`
}
type ImageRemoveInput struct {
	Image string `json:"image"`
	Force bool   `json:"force,omitempty"`
}
type RemovedImage struct {
	Untagged []string `json:"untagged"`
	Deleted  []string `json:"deleted"`
}
type ImagePruneInput struct{}
type PrunedImage struct {
	ID   string   `json:"id"`
	Tags []string `json:"tags"`
	Size int64    `json:"size"`
}
type Pruned struct {
	Images    []PrunedImage `json:"images"`
	Reclaimed int64         `json:"reclaimed"`
}
type NetworkCreateInput struct {
	Name       string            `json:"name"`
	Driver     string            `json:"driver,omitempty"` // default bridge
	Internal   bool              `json:"internal,omitempty"`
	Attachable bool              `json:"attachable,omitempty"`
	IPv6       bool              `json:"ipv6,omitempty"`
	Subnet     string            `json:"subnet,omitempty"`
	Gateway    string            `json:"gateway,omitempty"`
	Labels     map[string]string `json:"labels,omitempty"` // never darkpanel.module
}
type VolumeCreateInput struct {
	Name   string            `json:"name"`
	Driver string            `json:"driver,omitempty"` // default local
	Labels map[string]string `json:"labels,omitempty"` // never darkpanel.module
}
type Created struct {
	ID   string `json:"id"` // a volume's name
	Name string `json:"name"`
}
type NetworkInput struct{ Network string `json:"network"` }
type VolumeInput struct{ Volume string `json:"volume"` }
type ConnectInput struct {
	Network   string   `json:"network"`
	Container string   `json:"container"`
	Aliases   []string `json:"aliases,omitempty"`
	IPv4      string   `json:"ipv4,omitempty"`
	IPv6      string   `json:"ipv6,omitempty"`
}
type DisconnectInput struct {
	Network   string `json:"network"`
	Container string `json:"container"`
	Force     bool   `json:"force,omitempty"`
}
type ComposeInput struct {
	Project  string    `json:"project"`
	Profiles *[]string `json:"profiles,omitempty"` // nil: the stored list; given: replaces it at apply
}
type ComposeResult struct {
	Project    string           `json:"project"`
	Containers []ContainerState `json:"containers"`
}
type LogsParams struct {
	Container string `json:"container"`
	Follow    bool   `json:"follow,omitempty"`
	Since     string `json:"since,omitempty"` // RFC 3339
	Tail      string `json:"tail,omitempty"`  // "all" or a number, default "200"
}
type LogEvent struct {
	Stream string `json:"stream"`
	Text   string `json:"text"`
}
type StatsParams struct{ Container string `json:"container"` }
type StatsEvent struct {
	Read       string  `json:"read"`
	CPUPercent float64 `json:"cpuPercent"`
	MemUsage   uint64  `json:"memUsage"`
	MemLimit   uint64  `json:"memLimit"`
	NetRx      uint64  `json:"netRx"`
	NetTx      uint64  `json:"netTx"`
	Pids       uint64  `json:"pids"`
}
type PullParams struct{ ChangeID string `json:"changeId"` }
type PullEvent struct {
	Status   string `json:"status"`
	ID       string `json:"id,omitempty"`
	Progress string `json:"progress,omitempty"`
	Current  int64  `json:"current,omitempty"`
	Total    int64  `json:"total,omitempty"`
	Error    string `json:"error,omitempty"`
}
```

`usage` helpers (Task 16, `resources.go`): `func usageOf(s dockerapi.Stats) Usage` (CPU percent from the CPU and system deltas times the online CPUs, as `docker stats` computes it; memory usage minus `inactive_file`), which Task 25's stats stream also uses for `StatsEvent`.

### Docker pull hub (from Task 37)

`pull.go`: `type pullHub struct`, `func newPullHub(clock core.Clock) *pullHub`, `func (h *pullHub) publish(changeID string, e PullEvent)`, `func (h *pullHub) finish(changeID string)`, `func (h *pullHub) subscribe(ctx context.Context, changeID string) (<-chan PullEvent, func())`. A subscriber first receives the change's backlog (the last 256 events), then live events; the channel closes when the change finishes or ctx ends. A finished change's backlog is dropped 10 minutes after `finish`. Safe for concurrent use. Task 44 creates one hub in `Module()` and passes it to `imagePull` and `pullStreamDef`.

### Docker projects store (from Task 26)

`migrations/agent/0001_docker.sql` creates `docker_projects` (`name TEXT PRIMARY KEY`, `directory TEXT NOT NULL`, `files_json TEXT NOT NULL`, `env_files_json TEXT NOT NULL`, `profiles_json TEXT NOT NULL DEFAULT '[]'`, `run_hash TEXT NOT NULL DEFAULT ''`, `first_seen TEXT NOT NULL`, `last_run TEXT`) `STRICT`. `store.go`:

```go
func agentMigrations() fs.FS
type projectRow struct {
	Name, Directory string
	Files, EnvFiles, Profiles []string
	RunHash   string
	FirstSeen time.Time
	LastRun   time.Time // zero before the first run
}
// discoverProjects records every project the containers' compose labels name that has no row yet; it never deletes or updates a row.
func discoverProjects(ctx context.Context, db *store.DB, now time.Time, cs []dockerapi.ContainerSummary) error
func loadProject(ctx context.Context, db *store.DB, name string) (projectRow, bool, error)
func listProjects(ctx context.Context, db *store.DB) ([]projectRow, error)
func setProjectRun(ctx context.Context, tx *sql.Tx, name string, profiles []string, runHash string, at time.Time) error
// configHash is the hex SHA-256 over each file's and env file's path and bytes, in order; "" when one cannot be read.
func (k dk) configHash(p projectRow) string
```

Test helper `pjDB(t *testing.T) *store.DB`: an agent database with the core and docker migrations.

### Compose checks and runs (from Tasks 38, 41)

`compose_check.go` (Task 38):

```go
const (
	dockerConfigDir = "/var/lib/darkpanel/agent/docker-config"
	pluginDir       = "/usr/libexec/docker/cli-plugins"
)
func ownedPaths() []core.OwnedPath // {Path: dockerConfigDir, Kind: core.OwnedManagedDir, Pattern: `^config\.json$`}
type composeRun struct {
	Project  projectRow
	Profiles []string // the input's, else the stored list
	Hash     string   // configHash now
	Config   compose.Config
}
// prepareCompose runs every check of spec 3 "Validation" for a plan and again at apply: the stored project, absolute
// existing paths owned by root or the directory's owner with no symlink in any component, the docker-config
// directory (absent, or holding at most a config.json without credsStore or credHelpers and no cli-plugins entry),
// the plugin directory and docker-compose root-owned and not group- or other-writable, current containers of the
// project not owned, `config --format json` run through env.Exec and parsed, and compose.Validate. Failures are
// docker.compose_unavailable or docker.owned with the reason.
func (k dk) prepareCompose(ctx context.Context, env core.Env, in ComposeInput) (composeRun, error)
// composeArgs builds the allowlisted argument vector: compose --project-directory, -p, every -f, every --env-file, every --profile, then verb.
func composeArgs(p projectRow, profiles []string, verb ...string) []string
// planCompose renders the diff every Compose action shows: "runs the project's Compose file as root", the verb, the
// files, the profiles, and the hash change when HashChanged.
func planCompose(r composeRun, verb string) []core.DiffLine
```

Task 41 writes `docker-config/config.json` as `{}` through `env.Files.WriteManaged(ctx, "docker", dockerConfigDir, "config.json", []byte("{}\n"), 0o600, 0, 0)` before every run, runs the verb with the apply context (its deadline is the action's `ApplyTimeout`), treats a non-zero exit as a failure whose detail is the verb, the exit status and the last non-empty stderr line (at most 300 bytes), records the run with `setProjectRun`, and reads the result back from the Engine API. Test helper `ccProject(t *testing.T, h *dkHost, name string) projectRow` (Task 38) puts a valid project's files into `h.files` and its row into `h.db`.

### Docker log secrets seam (Tasks 25, 36)

`deps.secrets` returns the secrets a module registered for a container. In this plan it returns none in production; `docker.logs` (the stream) and `docker.container.logs` (the read) pass the frames of a container whose `darkpanel.module` label is set through a `logfilter.Filter` built from it. Wave 6a/6b feed it (register row added by this plan).

### Tailnet internals (from Tasks 12, 39, 42)

Package `internal/modules/tailnet`. Task 39 writes `wire.go`, `client.go`, `oauth.go` and changes `tailnet.go`'s `module` and `Module()`:

```go
type module struct{ t tn }
func Module() core.Module // module{t: productionTN()}
type tn struct {
	d      deps
	tokens *tokenCache
}
type deps struct {
	baseURL string                                           // "https://api.tailscale.com"
	http    *http.Client                                     // nil: cloud's default client
	sleep   func(ctx context.Context, d time.Duration) error // nil: cloud's default
}
func productionTN() tn
type api struct{ /* a *cloud.Client with Provider "tailscale" and a bearer Auth from the token cache, and the tailnet name */ }
func (t tn) newAPI(a account, secret []byte, clock core.Clock) (*api, error)
func (a *api) Devices(ctx context.Context) ([]Device, error)                   // GET /api/v2/tailnet/{t}/devices?fields=all
func (a *api) Device(ctx context.Context, id string) (Device, error)           // GET /api/v2/device/{id}?fields=all; 404 → not_found
func (a *api) Authorize(ctx context.Context, id string, authorized bool) error // POST /api/v2/device/{id}/authorized
func (a *api) Routes(ctx context.Context, id string) (DeviceRoutes, error)     // GET /api/v2/device/{id}/routes
func (a *api) SetRoutes(ctx context.Context, id string, routes []string) (DeviceRoutes, error) // POST /api/v2/device/{id}/routes
func (a *api) Expire(ctx context.Context, id string) error                     // POST /api/v2/device/{id}/expire
func (a *api) DeleteDevice(ctx context.Context, id string) error               // DELETE /api/v2/device/{id}
func (a *api) CreateKey(ctx context.Context, r KeyRequest) (CreatedKey, error) // POST /api/v2/tailnet/{t}/keys
func (a *api) Keys(ctx context.Context) ([]KeyInfo, error)                     // GET /api/v2/tailnet/{t}/keys
func (a *api) Key(ctx context.Context, id string) (KeyInfo, error)             // GET /api/v2/tailnet/{t}/keys/{id}
func (a *api) DeleteKey(ctx context.Context, id string) error                  // DELETE /api/v2/tailnet/{t}/keys/{id}
func (a *api) Policy(ctx context.Context) (text []byte, etag string, err error) // GET /api/v2/tailnet/{t}/acl, Accept: application/hujson
func (a *api) SetPolicy(ctx context.Context, text []byte, ifMatch string) (etag string, err error) // POST, If-Match; 412 → tailnet.policy_conflict
func (a *api) ValidatePolicy(ctx context.Context, text []byte) error            // POST .../acl/validate; nil valid, else tailnet.policy_invalid with the provider's message
type KeyRequest struct {
	Description   string
	Tags          []string
	Reusable      bool
	Ephemeral     bool
	Preauthorized bool
	ExpirySeconds int
}
type KeyInfo struct {
	ID          string
	Description string
	Created     time.Time
	Expires     time.Time
	Revoked     bool
}
type tokenCache struct{ /* unexported, mutex-guarded */ }
func newTokenCache() *tokenCache
// token answers a cached token of clientID while it is younger than 50 minutes and otherwise fetches a new one
// (POST /api/v2/oauth/token, form client_id and client_secret, grant_type client_credentials); a cached token is
// keyed by client id and a hash of the secret, never stored in clear outside memory.
func (c *tokenCache) token(ctx context.Context, clientID string, fetch func(ctx context.Context) (string, time.Duration, error), now time.Time) (string, error)
```

`CreatedKey` (in Contracts "Tailnet wire shapes") carries the key secret. Test helper `tnWith(srv *fake.Server) tn` (Task 39) points `deps` at the fake and makes `sleep` return immediately.

Task 42 writes `account.go`:

```go
// connected loads the one account, opens its secret and builds its api; cloud.not_connected when there is none.
func (t tn) connected(ctx context.Context, db *store.DB, s core.Sealer, clock core.Clock) (*api, account, error)
func cloudEffect(kind core.EffectKind, resource string, before json.RawMessage) core.Effect // Local false, CommitWait cloud.CommitWait
```

Test helper `acConnect(t *testing.T, h *tnHost, srv *fake.Server) account` (Task 42) inserts a connected account whose secret matches the fake's client.

### Tailnet store (from Task 12)

`migrations/controller/0001_tailnet.sql` creates, `STRICT`:

- `tailnet_accounts` (`id TEXT PRIMARY KEY` the connecting change id, `tailnet TEXT NOT NULL`, `client_id TEXT NOT NULL`, `client_secret_ct BLOB NOT NULL`, `tags_allowed_json TEXT NOT NULL`, `capabilities_json TEXT NOT NULL`, `connected_at TEXT NOT NULL`); at most one row.
- `tailnet_keys` (`description TEXT PRIMARY KEY`, `kind TEXT NOT NULL CHECK (kind IN ('change','enroll'))`, `owner_id TEXT NOT NULL`, `requested_at TEXT NOT NULL`, `expiry_seconds INTEGER NOT NULL`, `tags_json TEXT NOT NULL`, `key_id TEXT NOT NULL DEFAULT ''`, `state TEXT NOT NULL CHECK (state IN ('requested','live','uncertain','used','retired','deleted'))`, `updated_at TEXT NOT NULL`).

`store.go`:

```go
func controllerMigrations() fs.FS
type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}
type account struct {
	ID, Tailnet, ClientID string
	SecretCT              []byte // cloud.SealCredential(s, "tailnet_accounts", "client_secret_ct", ID, secret)
	TagsAllowed           []string
	Capabilities          []cloud.Capability
	ConnectedAt           time.Time
}
type keyRow struct {
	Description, Kind, OwnerID, KeyID, State string
	RequestedAt, UpdatedAt                   time.Time
	ExpirySeconds                            int
	Tags                                     []string
}
func loadAccount(ctx context.Context, q queryer) (account, bool, error)
func insertAccount(ctx context.Context, tx *sql.Tx, a account) error
func deleteAccount(ctx context.Context, tx *sql.Tx, id string) error
func insertKey(ctx context.Context, tx *sql.Tx, k keyRow) error
func loadKey(ctx context.Context, q queryer, description string) (keyRow, bool, error)
func listKeys(ctx context.Context, q queryer) ([]keyRow, error) // by requested_at
func setKeyState(ctx context.Context, tx *sql.Tx, description, state, keyID string, at time.Time) error // keyID "" keeps the stored one
// cleanupPending names what blocks deleting the account row (spec 1.9): each tailnet_keys row in requested, uncertain
// or retired, and each unresolved effect whose resource starts with "tailnet.".
func cleanupPending(ctx context.Context, tx *sql.Tx) ([]string, error)
```

Test helpers (Task 12, `store_test.go` and `host_test.go`): `tnDB(t *testing.T) *store.DB` (a controller database with the core and tailnet migrations); `type tnHost struct{ db *store.DB; clock *coretest.FakeClock; journal *tnJournal }`, `newTNHost(t *testing.T) *tnHost`, `(h *tnHost) env(changeID string) core.Env` (`HostID` "", `Principal` admin, `DB`, `Sealer` `coretest.Sealer{}`, `Clock`, `Log` discarding, `Effects` the journal when `changeID != ""`); `tnJournal` an in-memory `core.EffectJournal` with `effects() []core.Effect`.

### Tailnet fake server (from Task 17)

Package `internal/modules/tailnet/fake`, built on `cloudfake`: `func New(tb testing.TB) *Server`; `(*Server).URL() string`, `Client() *http.Client`, `Cloud() *cloudfake.Server` (faults and the request log); credentials `ClientID = "fake-client"`, `ClientSecret = "fake-secret"`, `Tailnet = "example.com"`; state helpers `AddDevice(d Device) string` (answers its id), `Device(id string) (Device, bool)`, `Keys() []Key`, `AddKey(k Key)`, `SetPolicy(text string) string` (answers the new ETag), `Policy() (text, etag string)`, `SetScopes(scopes ...string)` (requests outside them answer 403), `Tokens() int` (tokens issued); exported `Device` and `Key` structs mirror the provider's JSON. It serves every endpoint of Contracts "Tailnet internals" with the response shapes Task 1 records in spec 4.2a, validates the bearer token, answers `ETag` on the policy and `412` on a stale `If-Match`, and answers `acl/validate` as Task 1 recorded.

### Tailnet policy package (from Task 6)

Package `internal/modules/tailnet/policy` imports the standard library, `internal/core`, `internal/core/errcode` and `github.com/tailscale/hujson`.

```go
type Edit struct {
	Op    string          `json:"op"`              // set | delete | addItem | removeItem
	Path  string          `json:"path"`            // JSON Pointer to an object member (set, delete) or an array (addItem, removeItem)
	Value json.RawMessage `json:"value,omitempty"` // set, addItem, removeItem
}
// Apply applies edits in order to HuJSON text, keeping comments and unrelated content; each kind converges as spec 4.2
// says. A path through an array index, an unknown op, a missing parent or a value of the wrong kind is
// *errcode.Error request.invalid_input with the field "/edits/<i>".
func Apply(text []byte, edits []Edit) ([]byte, error)
// Canonical is the standardized JSON of HuJSON text with object keys sorted and no insignificant whitespace.
func Canonical(text []byte) ([]byte, error)
// Diff is a line diff of two texts as core.DiffLine values (" ", "-", "+").
func Diff(before, after []byte) []core.DiffLine
```

### Tailnet wire shapes (Tasks 42, 45, 46, 49, 51, 55; read by Tasks 9, 21, 23, 34, 57)

All tailnet actions are controller-side; mutating ones lock `tailnet`. The plan adds the read actions `tailnet.account.get` and `tailnet.authkey.list` (spec 5 lets the building wave add read actions); controller modules have no resources (Assumptions).

| Action | Constructor (task) | Class, extras | Input → output |
|---|---|---|---|
| `tailnet.account.connect` | `accountConnect` (42) | compensable, Undoer | `ConnectInput` → `AccountView` |
| `tailnet.account.disconnect` | `accountDisconnect` (42) | irreversible, confirmation the tailnet name | `DisconnectInput` → `Disconnected` |
| `tailnet.account.get` | `accountGet` (42) | read | `AccountGetInput` → `AccountStatus` |
| `tailnet.device.list` | `deviceList` (45) | read | `DeviceListInput` → `DeviceList` |
| `tailnet.device.authorize` | `deviceAuthorize` (45) | compensable, Undoer | `DeviceInput` → `Device` |
| `tailnet.device.set-routes` | `deviceSetRoutes` (45) | compensable, Undoer | `SetRoutesInput` → `DeviceRoutes` |
| `tailnet.device.expire`, `.delete` | `deviceExpire`, `deviceDelete` (46) | irreversible, confirmation the device name | `DeviceInput` → `DeviceRef` |
| `tailnet.authkey.create` | `authkeyCreate` (49) | compensable, Undoer, Resolver | `AuthKeyInput` → `CreatedKey` |
| `tailnet.authkey.list` | `authkeyList` (49) | read | `AuthKeyListInput` → `AuthKeyList` |
| `tailnet.policy.get` | `policyGet` (51) | read | `PolicyGetInput` → `Policy` |
| `tailnet.policy.set` | `policySet` (51) | compensable, Undoer | `PolicySetInput` → `PolicyWritten` |
| `tailnet.policy.patch` | `policyPatch` (55) | compensable, Undoer, `Converges` | `PolicyPatchInput` → `PolicyWritten` |

```go
type ConnectInput struct {
	Tailnet      string   `json:"tailnet"`
	ClientID     string   `json:"clientId"`
	ClientSecret string   `json:"clientSecret" secret:"true"`
	TagsAllowed  []string `json:"tagsAllowed"` // at least one, each "tag:<name>"
}
type AccountView struct {
	Tailnet      string             `json:"tailnet"`
	ClientID     string             `json:"clientId"`
	TagsAllowed  []string           `json:"tagsAllowed"`
	Capabilities []cloud.Capability `json:"capabilities"`
	ConnectedAt  time.Time          `json:"connectedAt"`
}
type DisconnectInput struct{ AbandonCleanup bool `json:"abandonCleanup,omitempty"` }
type Disconnected struct{ Abandoned []string `json:"abandoned"` }
type AccountGetInput struct{}
type AccountStatus struct {
	Connected bool         `json:"connected"`
	Account   *AccountView `json:"account,omitempty"`
}
type DeviceListInput struct{}
type Device struct {
	ID                string   `json:"id"` // the provider's nodeId
	Name              string   `json:"name"`
	Hostname          string   `json:"hostname"`
	OS                string   `json:"os"`
	User              string   `json:"user"`
	Addresses         []string `json:"addresses"`
	Tags              []string `json:"tags"`
	Authorized        bool     `json:"authorized"`
	LastSeen          string   `json:"lastSeen,omitempty"`
	Expires           string   `json:"expires,omitempty"`
	KeyExpiryDisabled bool     `json:"keyExpiryDisabled"`
	AdvertisedRoutes  []string `json:"advertisedRoutes"`
	EnabledRoutes     []string `json:"enabledRoutes"`
	ExitNode          bool     `json:"exitNode"`        // advertises 0.0.0.0/0 or ::/0
	ExitNodeEnabled   bool     `json:"exitNodeEnabled"` // both approved
}
type DeviceList struct{ Devices []Device `json:"devices"` }
type DeviceInput struct{ Device string `json:"device"` }
type DeviceRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type SetRoutesInput struct {
	Device string   `json:"device"`
	Routes []string `json:"routes"` // the full approved list; an exit node is 0.0.0.0/0 and ::/0
}
type DeviceRoutes struct {
	Advertised []string `json:"advertisedRoutes"`
	Enabled    []string `json:"enabledRoutes"`
}
type AuthKeyInput struct {
	Tags          []string `json:"tags"` // each within tagsAllowed
	Reusable      bool     `json:"reusable,omitempty"`
	Ephemeral     bool     `json:"ephemeral,omitempty"`
	Preauthorized bool     `json:"preauthorized,omitempty"`
	ExpirySeconds int      `json:"expirySeconds,omitempty"` // 1–7776000, default 86400
}
type CreatedKey struct {
	ID          string    `json:"id"`
	Key         string    `json:"key" secret:"true"`
	Expires     time.Time `json:"expires"`
	Description string    `json:"description"`
}
type AuthKeyListInput struct{}
type AuthKeyRow struct {
	Description string    `json:"description"`
	Kind        string    `json:"kind"`
	OwnerID     string    `json:"ownerId"`
	KeyID       string    `json:"keyId,omitempty"`
	State       string    `json:"state"`
	RequestedAt time.Time `json:"requestedAt"`
	Expires     time.Time `json:"expires"`
}
type AuthKeyList struct{ Keys []AuthKeyRow `json:"keys"` }
type PolicyGetInput struct{}
type Policy struct {
	Text string `json:"text"`
	ETag string `json:"etag"`
}
type PolicySetInput struct{ Text string `json:"text"` }
type PolicyPatchInput struct{ Edits []policy.Edit `json:"edits"` }
type PolicyWritten struct{ ETag string `json:"etag"` }
```

Policy effects (Task 51, `policy_effects.go`, reused by Task 55): resource `tailnet.policy:<tailnet>`, kind modify, State `{"policy": <policy.Canonical of the text>}`, Version the ETag; the before-text (with comments) travels as the effect's sealed secret (`Intend`'s second argument) so the undo posts it back with `If-Match` on the after-ETag. `func (t tn) writePolicy(ctx context.Context, env core.Env, a *api, acct account, before []byte, etag string, after []byte) (string, error)` journals, posts and records; `type policyHandler struct` implements `Observe` and `Inverse` for both actions.

### Tailnet key sweep (from Task 50)

`keysweep.go`: `func (m module) Controller(deps ext.Deps) ([]ext.Route, []ext.Service, error)` answers no routes and one `ext.Service{Name: "tailnet.keysweep", Run: …}`; `func (t tn) sweep(ctx context.Context, deps ext.Deps, now time.Time, orphans bool) error` is one pass (spec 1.9's table row "Tailscale auth key create"); the service runs a pass every 5 minutes and an orphan pass every 24 hours, timed by `deps.Clock`, and audits each deletion through `deps.Audit.Record` as actor `system:tailnet.keysweep`, action `tailnet.keysweep`, phase `delete`, input `{"keyId","description"}`.

### Cloudflare internals (from Tasks 13, 32, 43)

Package `internal/modules/cloudflare`. Task 32 writes `wire.go` and `client.go` and changes `cloudflare.go`'s `module` and `Module()`:

```go
type module struct{ c cf }
func Module() core.Module // module{c: productionCF()}
type cf struct{ d deps }
type deps struct {
	baseURL string                                           // "https://api.cloudflare.com/client/v4"
	http    *http.Client
	sleep   func(ctx context.Context, d time.Duration) error
}
func productionCF() cf
type api struct{ /* a *cloud.Client with Provider "cloudflare", bearer token Auth, ErrorCode from errors[0].code, and the account id */ }
func (c cf) newAPI(accountID string, token []byte, clock core.Clock) (*api, error)
type Tunnel struct {
	ID          string
	Name        string
	Status      string // inactive | degraded | healthy | down
	CreatedAt   time.Time
	Connections []Connection
}
type Connection struct {
	ID, ColoName, OriginIP, ClientVersion string
	OpenedAt                              time.Time
}
type Zone struct{ ID, Name string }
type DNSRecord struct {
	ID, Type, Name, Content, Comment string
	Proxied                          bool
}
func (a *api) VerifyToken(ctx context.Context) error                          // the endpoint Task 1 records
func (a *api) Tunnels(ctx context.Context, name string) ([]Tunnel, error)      // GET /accounts/{a}/cfd_tunnel?is_deleted=false[&name=]
func (a *api) Tunnel(ctx context.Context, id string) (Tunnel, error)
func (a *api) CreateTunnel(ctx context.Context, name string) (Tunnel, error)   // POST, config_src cloudflare
func (a *api) DeleteTunnel(ctx context.Context, id string) error
func (a *api) TunnelToken(ctx context.Context, id string) (string, error)
func (a *api) Connections(ctx context.Context, id string) ([]Connection, error)
func (a *api) Configuration(ctx context.Context, id string) (ingress.Config, error)
func (a *api) PutConfiguration(ctx context.Context, id string, c ingress.Config) error
func (a *api) Zones(ctx context.Context) ([]Zone, error)
func (a *api) DNSRecords(ctx context.Context, zoneID, name, comment string) ([]DNSRecord, error) // "" filters nothing
func (a *api) DNSRecord(ctx context.Context, zoneID, id string) (DNSRecord, error)
func (a *api) CreateDNSRecord(ctx context.Context, zoneID string, r DNSRecord) (DNSRecord, error)
func (a *api) DeleteDNSRecord(ctx context.Context, zoneID, id string) error
// zoneFor picks the zone whose name is the longest suffix of hostname; not_found when none.
func zoneFor(zones []Zone, hostname string) (Zone, bool)
```

Test helper `cfWith(srv *fake.Server) cf` (Task 32). Task 43 writes `account.go` with `func (c cf) connected(ctx context.Context, db *store.DB, s core.Sealer, clock core.Clock) (*api, account, error)` (cloud.not_connected when there is none) and `func cloudEffect(kind core.EffectKind, resource string, before json.RawMessage) core.Effect`, and test helper `acConnect(t *testing.T, h *cfHost, srv *fake.Server) account`.

### Cloudflare store (from Task 13)

`migrations/controller/0001_cloudflare.sql` creates, `STRICT`:

- `cloudflare_accounts` (`id TEXT PRIMARY KEY` the connecting change id, `account_id TEXT NOT NULL`, `token_ct BLOB NOT NULL`, `capabilities_json TEXT NOT NULL`, `connected_at TEXT NOT NULL`); at most one row.
- `cloudflare_tunnels` (`id TEXT PRIMARY KEY` the provider's tunnel id, `name TEXT NOT NULL UNIQUE`, `change_id TEXT NOT NULL`, `config_hash TEXT NOT NULL DEFAULT ''`, `drift INTEGER NOT NULL DEFAULT 0 CHECK (drift IN (0,1))`, `created_at TEXT NOT NULL`, `checked_at TEXT`).
- `cloudflare_hostnames` (`hostname TEXT PRIMARY KEY`, `tunnel_id TEXT NOT NULL REFERENCES cloudflare_tunnels (id)`, `service TEXT NOT NULL`, `no_tls_verify INTEGER NOT NULL DEFAULT 0 CHECK (no_tls_verify IN (0,1))`, `zone_id TEXT NOT NULL DEFAULT ''`, `record_id TEXT NOT NULL DEFAULT ''`, `phase TEXT NOT NULL CHECK (phase IN ('adding_rule','adding_record','active','removing_record','removing_rule'))`, `change_id TEXT NOT NULL`, `health TEXT NOT NULL DEFAULT 'unknown' CHECK (health IN ('ok','rule_missing','record_missing','record_changed','unknown'))`, `checked_at TEXT`, `updated_at TEXT NOT NULL`).

`store.go`:

```go
func controllerMigrations() fs.FS
type queryer interface { /* as tailnet's */ }
type account struct {
	ID, AccountID string
	TokenCT       []byte // cloud.SealCredential(s, "cloudflare_accounts", "token_ct", ID, token)
	Capabilities  []cloud.Capability
	ConnectedAt   time.Time
}
type tunnelRow struct {
	ID, Name, ChangeID, ConfigHash string
	Drift                          bool
	CreatedAt, CheckedAt           time.Time
}
type hostnameRow struct {
	Hostname, TunnelID, Service, ZoneID, RecordID, Phase, ChangeID, Health string
	NoTLSVerify                                                           bool
	CheckedAt, UpdatedAt                                                  time.Time
}
func loadAccount(ctx context.Context, q queryer) (account, bool, error)
func insertAccount(ctx context.Context, tx *sql.Tx, a account) error
func deleteAccount(ctx context.Context, tx *sql.Tx, id string) error
func insertTunnel(ctx context.Context, tx *sql.Tx, t tunnelRow) error
func loadTunnel(ctx context.Context, q queryer, idOrName string) (tunnelRow, bool, error)
func listTunnels(ctx context.Context, q queryer) ([]tunnelRow, error)
func deleteTunnel(ctx context.Context, tx *sql.Tx, id string) error
func setTunnelHash(ctx context.Context, tx *sql.Tx, id, hash string) error
func setTunnelCheck(ctx context.Context, tx *sql.Tx, id string, drift bool, at time.Time) error
func insertHostname(ctx context.Context, tx *sql.Tx, h hostnameRow) error
func loadHostname(ctx context.Context, q queryer, hostname string) (hostnameRow, bool, error)
func listHostnames(ctx context.Context, q queryer) ([]hostnameRow, error)
func setHostnamePhase(ctx context.Context, tx *sql.Tx, hostname, phase, zoneID, recordID string, at time.Time) error
func setHostnameHealth(ctx context.Context, tx *sql.Tx, hostname, health string, at time.Time) error
func deleteHostname(ctx context.Context, tx *sql.Tx, hostname string) error
// cleanupPending names what blocks deleting the account row: each hostname row not in phase active, and each
// unresolved effect whose resource starts with "cloudflare.".
func cleanupPending(ctx context.Context, tx *sql.Tx) ([]string, error)
```

Test helpers (Task 13): `cfDB(t *testing.T) *store.DB`, `type cfHost struct{ db *store.DB; clock *coretest.FakeClock; journal *cfJournal }`, `newCFHost(t *testing.T) *cfHost`, `(h *cfHost) env(changeID string) core.Env`, `cfJournal` with `effects() []core.Effect`, all as tailnet's.

### Cloudflare fake server (from Task 18)

Package `internal/modules/cloudflare/fake`, built on `cloudfake`: `func New(tb testing.TB) *Server`; `URL()`, `Client()`, `Cloud() *cloudfake.Server`; `Token = "fake-token"`, `AccountID = "0123456789abcdef0123456789abcdef"`; state helpers `AddZone(name string) string`, `Tunnels() []Tunnel`, `AddTunnel(name string) string`, `Configuration(tunnelID string) json.RawMessage`, `SetConfiguration(tunnelID string, raw json.RawMessage)`, `Records(zoneID string) []Record`, `AddRecord(zoneID string, r Record) string`, `SetConnections(tunnelID string, n int)`, `SetPermissions(perms ...string)` (requests outside them answer 403 with a Cloudflare error body); exported `Tunnel` and `Record` structs mirror the provider's JSON. Every answer uses Cloudflare's envelope `{"success","errors":[{"code","message"}],"messages","result","result_info"}`, and the endpoints are those of Contracts "Cloudflare internals" with the paths and permissions Task 1 records in spec 7.1.

### Cloudflare ingress package (from Task 7)

Package `internal/modules/cloudflare/ingress` imports only the standard library.

```go
const CatchAll = "http_status:404"
type Rule struct {
	Hostname    string
	Path        string
	Service     string
	NoTLSVerify bool // originRequest.noTLSVerify
	// members not named here, inside the rule and its originRequest, are kept byte for byte
}
type Config struct {
	Ingress []Rule
	// top-level members other than ingress are kept byte for byte
}
func (r Rule) MarshalJSON() ([]byte, error); func (r *Rule) UnmarshalJSON([]byte) error
func (c Config) MarshalJSON() ([]byte, error); func (c *Config) UnmarshalJSON([]byte) error
// Add inserts {hostname, service} before the catch-all (adding the catch-all last when absent); changed false when an
// equal rule exists; an error when hostname already routes to another service.
func Add(c Config, r Rule) (out Config, changed bool, err error)
// Remove drops hostname's rule; changed false when it is absent. Other rules are carried over unchanged.
func Remove(c Config, hostname string) (out Config, changed bool)
func Lookup(c Config, hostname string) (Rule, bool)
// Hash is the hex SHA-256 of the configuration's canonical JSON (object keys sorted).
func Hash(c Config) string
```

### Cloudflare wire shapes (Tasks 43, 47, 48, 52, 53, 56; read by Tasks 10, 22, 35, 58)

All cloudflare actions here are controller-side; mutating ones lock `cloudflare`. The plan adds the read actions `cloudflare.account.get` and `cloudflare.hostname.list`.

| Action | Constructor (task) | Class, extras | Input → output |
|---|---|---|---|
| `cloudflare.account.connect` | `accountConnect` (43) | compensable, Undoer | `ConnectInput` → `AccountView` |
| `cloudflare.account.disconnect` | `accountDisconnect` (43) | irreversible, confirmation the account id | `DisconnectInput` → `Disconnected` |
| `cloudflare.account.get` | `accountGet` (43) | read | `AccountGetInput` → `AccountStatus` |
| `cloudflare.tunnel.create` | `tunnelCreate` (47) | compensable, Undoer | `TunnelCreateInput` → `TunnelRef` |
| `cloudflare.tunnel.delete` | `tunnelDelete` (47) | irreversible, confirmation the tunnel name | `TunnelInput` → `TunnelRef` |
| `cloudflare.tunnel.list` | `tunnelList` (48) | read | `TunnelListInput` → `TunnelList` |
| `cloudflare.tunnel.token` | `tunnelToken` (48) | read, `DefineInternalRead` | `TunnelInput` → `TunnelToken` |
| `cloudflare.tunnel.adopt` | `tunnelAdopt` (48) | compensable, Undoer | `TunnelInput` → `TunnelRef` |
| `cloudflare.hostname.add` | `hostnameAdd` (52) | compensable, Undoer, Recoverer | `HostnameAddInput` → `HostnameView` |
| `cloudflare.hostname.remove` | `hostnameRemove` (56) | compensable, `CompensatedBy: cloudflare.hostname.add`, `Converges`, Recoverer | `HostnameInput` → `HostnameView` |
| `cloudflare.hostname.list` | `hostnameList` (53) | read | `HostnameListInput` → `HostnameList` |

```go
type ConnectInput struct {
	AccountID string `json:"accountId"`
	APIToken  string `json:"apiToken" secret:"true"`
}
type AccountView struct {
	AccountID    string             `json:"accountId"`
	Capabilities []cloud.Capability `json:"capabilities"`
	ConnectedAt  time.Time          `json:"connectedAt"`
}
type DisconnectInput struct{ AbandonCleanup bool `json:"abandonCleanup,omitempty"` }
type Disconnected struct{ Abandoned []string `json:"abandoned"` }
type AccountGetInput struct{}
type AccountStatus struct {
	Connected bool         `json:"connected"`
	Account   *AccountView `json:"account,omitempty"`
}
type TunnelCreateInput struct{}
type TunnelInput struct{ Tunnel string `json:"tunnel"` } // id or name
type TunnelRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type TunnelListInput struct{}
type ConnectionView struct {
	ID       string    `json:"id"`
	Colo     string    `json:"colo"`
	OriginIP string    `json:"originIp"`
	OpenedAt time.Time `json:"openedAt"`
	Version  string    `json:"version"`
}
type TunnelView struct {
	ID          string           `json:"id"`
	Name        string           `json:"name"`
	Status      string           `json:"status"` // the provider's; "unknown" when the read failed
	Connections []ConnectionView `json:"connections"`
	Drift       bool             `json:"drift"`
	Hostnames   int              `json:"hostnames"`
	CreatedAt   time.Time        `json:"createdAt"`
}
type TunnelList struct{ Tunnels []TunnelView `json:"tunnels"` }
type TunnelToken struct{ Token string `json:"token" secret:"true"` }
type HostnameAddInput struct {
	Tunnel      string `json:"tunnel"`
	Hostname    string `json:"hostname"`
	Service     string `json:"service"` // http(s)://host:port, tcp://, ssh://, http_status:<code>
	NoTLSVerify bool   `json:"noTLSVerify,omitempty"`
}
type HostnameInput struct{ Hostname string `json:"hostname"` }
type HostnameView struct {
	Hostname  string     `json:"hostname"`
	Tunnel    string     `json:"tunnel"` // id
	Service   string     `json:"service"`
	RecordID  string     `json:"recordId,omitempty"`
	Phase     string     `json:"phase"`
	Health    string     `json:"health"`
	CheckedAt *time.Time `json:"checkedAt,omitempty"`
}
type HostnameListInput struct{}
type HostnameList struct{ Hostnames []HostnameView `json:"hostnames"` }
```

Hostname helpers (Task 52, `hostname.go`, reused by Tasks 53 and 56): `func reserved(service string) bool` (the service's host is an address in `172.31.255.0/24`, the panel network range of spec 8); `func (c cf) checkDrift(ctx context.Context, a *api, t tunnelRow) (ingress.Config, error)` (reads the configuration; `cloud.conflict` naming the tunnel when its hash differs from the stored one, unless none is stored yet); `type hostnameHandler struct` (Observe, Inverse, Recover for the effects `cloudflare.ingress:<tunnel id>/<hostname>` (modify, State `{"rule": <the hostname's rule or null>}`) and `cloudflare.dnsrecord:<zone id>/<record id>` (creation `cloudflare.dnsrecord:?darkpanel:<change id>`, Recovery `{Kind: "marker", Value: "darkpanel:<change id>"}`, State `{"name","content","proxied","comment"}` or null)). Test helper `haAdd(t *testing.T, h *cfHost, srv *fake.Server, hostname string) hostnameRow` adds an active hostname through the action.

### Cloudflare hostnames service (from Task 53)

`hostnames_service.go`: `func (m module) Controller(deps ext.Deps) ([]ext.Route, []ext.Service, error)` answers no routes and one `ext.Service{Name: "cloudflare.hostnames", Run: …}`; `func (c cf) checkHostnames(ctx context.Context, deps ext.Deps, now time.Time) error` is one pass: for every tunnel row with hostnames it reads the configuration (drift: hash differs) and each active row's rule and DNS record, and stores `drift`, `health` and `checked_at` (`unknown` when the provider cannot be read). It never writes to Cloudflare and never plans a change; it records one audit row (actor `system:cloudflare.hostnames`, action `cloudflare.hostnames`, phase `check`) when a tunnel's drift or a hostname's health changes. The service runs a pass every 5 minutes, timed by `deps.Clock`.

### Web: Docker (Tasks 8, 19, 20, 33, 40)

`web/src/modules/docker/types.ts` (Task 8): TypeScript interfaces with the JSON members of Contracts "Docker wire shapes" named `DockerEngine`, `DockerPort`, `DockerUsage`, `DockerContainer`, `DockerImage`, `DockerNetwork`, `DockerVolume`, `DockerProject`, `ContainerDetail`, `ContainerState`, `EnvVar`, `DockerMount`, `EndpointView`, `LogLine`, `LogEvent`, `StatsEvent`, `PullEvent`, `Pruned`; constants `DOCKER_MODULE = "docker"`, `DOCKER_ACTION_VERSION = 1`; `dockerKey(hostId: string, resource: "engine" | "containers" | "images" | "networks" | "volumes" | "projects")` = `["resource", hostId, "docker." + resource]`. `fixtures.ts`: `dkEngine`, `dkContainer`, `dkImage`, `dkNetwork`, `dkVolume`, `dkProject`, `dkDetail`, each `(over: Partial<T> = {}) => T`. `stream.ts`: `interface StreamEnd { reason: "done" | "overflow" | "expired" | "revoked" | "error"; error?: Problem }`; `streamUrl(hostId: string, module: string, name: string, params: Record<string, string>): string` (`/api/v1/hosts/<host>/streams/<module>/<name>?<params>`); `useStream<E>(url: string | null, max = 1000): { events: E[]; end: StreamEnd | null }` (one `EventSource`, `data` and `end` events, keeps the last `max` events, closes on `end`, on unmount and when `url` changes; `url` null opens nothing).

Components, each a named export in its own file: `DockerChange({hostId, action, title, input, onClose})` (`docker-change.tsx`, Task 19: `ChangeDialog` with `target={{name: action, version: DOCKER_ACTION_VERSION, hostId, module: DOCKER_MODULE}}`, invalidating `["resource", hostId]` when it closes); `NetworksTab({hostId})`, `VolumesTab({hostId})`, `ProjectsTab({hostId})` (Task 19); `LogsPanel({hostId, containerId})`, `StatsPanel({hostId, containerId})` (Task 20); `DockerPage({hostId})`, `ContainersTab({hostId})`, `ImagesTab({hostId})` and `routes.tsx`'s default export `DockerRoutes({hostId}: ModuleRouteProps)` (Task 33: index route `DockerPage`, `*` the not-found empty state); `ContainerPage({hostId})` (`container-page.tsx`, Task 40, route `containers/:containerId` added to `routes.tsx`, reading the id from the route params). The container page renders, for every registry entry with `containerActions` whose module the host has enabled (`host.modules.some(s => s.id === entry.id && s.enabled)`, computed in the page because `registry.ts` is frozen and `enabledModules` skips entries without host routes), each action's `render` in `Suspense` with a `ContainerRef` built from the container and an `onDone` that refetches; and the `firewall.exposure` rows whose published binding is one of the container's (`readResource(hostId, "firewall", "exposure")` typed by `ExposureView` from `@/modules/firewall/types`), or the reason the firewall module is unavailable.

### Web: tailnet (Tasks 9, 21, 23, 34)

`web/src/modules/tailnet/types.ts` (Task 9): interfaces with the JSON members of Contracts "Tailnet wire shapes" named `TailnetAccountView`, `TailnetAccountStatus`, `TailnetDevice`, `TailnetDeviceRoutes`, `AuthKeyRow`, `CreatedKey`, `TailnetPolicy`, `PolicyEdit`; `TAILNET_ACTION_VERSION = 1`; `tailnetKey(name: "account" | "devices" | "keys" | "policy")` = `["tailnet", name]`. `fixtures.ts`: `tnAccount`, `tnDevice`, `tnKey`, `tnPolicy`. `tailnet-change.tsx`: `TailnetChange({action, title, input, onClose})` (controller-side `ChangeDialog`, `target={{name: action, version: TAILNET_ACTION_VERSION}}`, invalidating `["tailnet"]` when it closes). `web/src/components/capability-list.tsx` (Task 9): `Capability` (`{name, state: "ok" | "missing" | "unverified", detail?}`) and `CapabilityList({capabilities})`, which shows a `missing` capability whose `detail` is `cloud.unavailable` or `cloud.rate_limited` as "could not check" instead of missing. Components: `AccountPanel()` (`account-panel.tsx`, Task 21); `DevicesPage()` and `KeysPage()` (Task 23); `PolicyPage()` and `global.tsx`'s default export `TailnetGlobal()` (Task 34: routes index → `AccountPanel` and links, `devices`, `keys`, `policy`).

### Web: Cloudflare (Tasks 10, 22, 35)

`web/src/modules/cloudflare/types.ts` (Task 10): interfaces with the JSON members of Contracts "Cloudflare wire shapes" named `CloudflareAccountView`, `CloudflareAccountStatus`, `TunnelView`, `ConnectionView`, `HostnameView`; `CLOUDFLARE_ACTION_VERSION = 1`; `cloudflareKey(name: "account" | "tunnels" | "hostnames")` = `["cloudflare", name]`. `fixtures.ts`: `cfAccount`, `cfTunnel`, `cfHostname`. `cloudflare-change.tsx`: `CloudflareChange({action, title, input, onClose})` as `TailnetChange`. Components: `AccountPanel()` (`account-panel.tsx`, Task 22, using `CapabilityList`); `TunnelsPage()`, `HostnamesPage()` and `global.tsx`'s default export `CloudflareGlobal()` (Task 35: routes index → `AccountPanel` and links, `tunnels`, `hostnames`). `container-actions.tsx` stays the wave 6b stub.

### Harness M4 (from Tasks 54, 59, 60)

`test/harness/m4_test.go` (build tag `harness`, Task 54): flag `m4Flag = flag.Bool("m4", false, "run the M4 Docker module scenarios")`; `TestM4` skips unless `*osFlag != "" && *m2Flag != "" && *m4Flag`, uses a 60-minute context, `bootFleet` and `serveReleases`, and runs an ordered step table `[]m4Step{name string; gate bool; run func(t *testing.T)}` whose first steps are `install` (gate, `scenarioInstall`) and `docker-ready` (gate, `docker.engine` available on the agent). Helpers in `m4_helpers_test.go` (Task 54): `m4Plan(ctx, api *API, host, action string, input any) (m4PlanView, error)`, `m4ApplyAsync(ctx, t, api *API, host, action string, input any) Change` (plans, applies with the plan's confirmation and waits for a long-running change to finish), `m4Undo(ctx, api *API, changeID string) error`, `m4Docker(ctx, t, host *vm.VM, args ...string) string` (`sudo docker …` on the VM), `m4Stream(ctx, api *API, host, module, name string, params url.Values, n int) ([]json.RawMessage, error)` (reads n `data` events of a stream relay). Task 59 adds only names starting `m4c` in `m4_compose_test.go` and its steps to the table; Task 60 adds only names starting `m4l` in `m4_live_test.go`, the step `dockerapi-live`, the flag `dockerapiTestFlag = flag.String("dockerapi-test", "", "path of the compiled dockerapi test binary")`, and the workflow step.

## Assumptions (evidence)

- The three modules are registered skeletons: `internal/modules/{docker,tailnet,cloudflare}/*.go` declare only `ID`, `module`, `Module()` and an empty `Manifest()`, each with a `TestManifestIsASkeleton` test, and `internal/modules/modules.go` already lists them (read 2026-10-30). `internal/cli/{docker,tailnet,cloudflare}.go` return no hand-written commands; every action, resource and stream gets a generated command (`internal/cli/root.go`, `names.go`, `follow.go`), so this plan writes no CLI file and the stubs stay.
- Optional parts are found by type assertion: streams on `core.Streamer` (`internal/core/stream.go`), controller services on `ext.Controller` (`internal/core/ext/ext.go`, `internal/controller/server/extensions.go`), and `Observe`, `Inverse`, `Resolved` and `Recover` on the handler passed to `Define*` (`internal/core/define.go` `findHandlers`). Adding an action or resource needs its schema snapshot under `schemas/<name>/v1.json` (`make schemas`), or `modules.TestSnapshotsCurrent` fails; the snapshots are one directory per name, so the three registration tasks write disjoint files (read 2026-10-30).
- Resources are agent-only: `dispatch.Dispatcher.Resource` always reads from a host's agent (`internal/controller/dispatch/dispatch.go`, read 2026-10-30). The controller-side tailnet and cloudflare modules therefore expose their state through read actions (`tailnet.account.get`, `tailnet.authkey.list`, `cloudflare.account.get`, `cloudflare.hostname.list` added, as spec 5 allows for the building wave) called with `readAction` without a host.
- The engine's undo compares `Observe`'s `Current.State` with the effect's `Post` before the inverse and with `UndoTo` (= the journaled `Before.State`, or `null` for a creation) after it, by JSON equality of `State` only (`internal/core/lifecycle/undo.go` `runInverse`, `afterInverse`; `effects.go` `undoTo`, `stateEqual`; read 2026-10-30). Spec 3's comparator table, which excludes `StartedAt`, `FinishedAt` and `EndpointID` after an undo, is therefore implemented by a phase-aware `Observe`: the Docker effects journal `Before` in `dstate.Restored` form and `Post` in `dstate.Recorded` form, and `observe` renders `Restored` while the effect is `undoing` or `undone` and `Recorded` otherwise (Contracts "dstate", "Docker internals"). No Go comparator hook exists or is added.
- Ruling: `docker.container.restart` journals its effect's `Before` with `running: true` (`dstate.ContainerRunning`), the state its undo restores ("start the container if it is stopped; a restart itself is not reversed"), so the undo of a restart of a stopped container does not report a conflict; its plan diff still shows the real before-state. `docker.compose.restart` journals one such effect per container that was running before and none for the others.
- Ruling: `docker.image.pull` and `docker.compose.pull` are compensable with an `Undoer` that journals no effect (the registry requires exactly one compensation mechanism, `internal/core/registry.go` `checkLifecycle`), so their undo succeeds and changes nothing, as spec 3 documents.
- Ruling: a project's Compose profiles are the optional `profiles` member of every Compose action's input; absent, the stored list is used; given, it is stored in `docker_projects` at apply. A separate profile-setting action would be a mutating action outside spec 5's table.
- Ruling: `docker-config` is a managed directory the docker module declares (`core.OwnedManagedDir`, pattern `^config\.json$`), written as `{}` through `Files.WriteManaged` before every Compose run; plans only read it. Spec 1.8 names no owner for it, and nothing creates it today (`rg docker-config internal` finds only `internal/platform/exec_rules.go`, 2026-10-30).
- The exec allowlist already permits exactly the Compose vector of Global Constraints, in that argument order, with the fixed environment (`internal/platform/exec_rules.go` `execMatchDocker` and its `/usr/bin/docker` rule, read 2026-10-30). The runner caps stdout and stderr at 4 MiB, kills the process group on cancel and uses the context's deadline (`internal/platform/exec.go`), and the engine gives Apply a context with the action's `ApplyTimeout` (`internal/core/lifecycle/apply.go` `context.WithTimeout(ctx, timeout)`), so a long-running Compose run is bounded by its `ApplyTimeout` (30 min pull, 15 min up, 10 min down and restart).
- Prune needs no Engine prune endpoint: the plan lists `ListImages` with the `dangling=true` filter minus images any container uses (`ListContainers(All)`'s `ImageID`), its `StateHash` covers that list, and apply removes exactly those ids with `RemoveImage`. "In use" for networks comes from `InspectNetwork().Containers` and for volumes from `ListContainers(All)` mounts' `Name` (`internal/platform/dockerapi/{images,networks,containers}.go`, read 2026-10-30).
- There is no core API through which a module registers secrets for log filtering (`rg -i 'RegisterSecret|secret.*filter' internal/core` finds none, 2026-10-30). The docker module builds the filter (`logfilter`) behind its own `deps.secrets` seam, which returns none until waves 6a and 6b feed it; this plan adds a register row for that.
- The web client has no module-stream reader; the relay serves `GET /api/v1/hosts/{host}/streams/{module}/{name}` as server-sent events with `event: data` frames and one `event: end` frame (`internal/controller/api/streams.go`), query parameters decoded by the stream's params schema (`internal/core/schema/params.go` `ParamsJSON`). The tests' `EvtSource` stands in for `EventSource` (`web/src/test/evt-source.ts`).
- `web/src/modules/registry.test.tsx` hard-asserts the module ids, container action ids and global paths and renders every lazy component against 404 stubs; no task renames an id, and every new page renders its loading or error state against those stubs. `enabledModules` (`web/src/modules/registry.ts`) returns only entries with host routes, so the container page filters the registry itself for `containerActions` (Cloudflare has only a global page).
- The Compose plugin pinned for the harness is 5.6.0 and Docker 29.9.1 (`test/harness/images.json`); this host has Compose v5.1.4 and Docker 29.5.3 (`docker compose version`, `docker version`, 2026-10-30). Task 4 captures its test data with the host's Compose; the harness (Task 59) runs the pinned version. The key allowlists of Task 24 ("the set the pinned Compose version documents"): unverified — Task 24 derives them from the compose-spec JSON schema that Compose 5.6.0 vendors and cites its URL.
- `git`, `ssh` and the buildx plugin may or may not be present on the harness VMs (the provisioning installs only the pinned debs, `test/harness/provision.go`): unverified — Task 59 installs logging shims at `/usr/bin/git`, `/usr/bin/ssh` and `/usr/libexec/docker/cli-plugins/docker-buildx` (the buildx shim answers `docker-cli-plugin-metadata` and logs every other invocation) and proves none ran.
- The harness's `vm` job runs every OS step in one job with `timeout-minutes: 280` and three `go test` steps (`.github/workflows/harness.yml`, read 2026-10-30); `TestM4` gets its own flag `-m4` so the first step (no `-run`) skips it, its own workdir `$RUNNER_TEMP/harness-m4`, a step with `-timeout 70m`, a log-upload glob, and the job's limit raised to 360 minutes (Task 60). `TestDockerLive` runs on the VM as a test binary compiled by the `build` job (`go test -c -o bin/harness/dockerapi.test ./internal/platform/dockerapi`, uploaded with `bin/harness`) and copied in with `VM.CopyFile`, so `Makefile` does not change. The spec's rule that a parallel wave's `harness.yml` change belongs to the integrator holds: Task 60 runs alone, last.
- Spec 1.9 lists `change release` and `quarantine resolve` among the paths that delete a credentials row, but the merged core's release only marks effects abandoned (`internal/core/lifecycle/release.go` `Release`), and neither deletes a module row; the lifecycle is frozen. `cleanupPending` is therefore called by `<module>.account.disconnect` and by the `Inverse` of `<module>.account.connect` (returning `core.Definite(cloud.cleanup_pending)` without writing), the only deleting paths.
- Controller-side `Undoer` handlers run like agent-side ones: `lifecycle/undo.go` has no side check, and `core_effects` exists in the controller schema (`internal/core/store/migrations/controller/0009_lifecycle_core.sql`); no production module uses one yet, and the only exemplar is `internal/core/lifecycle/composite_fixture_test.go` (`cstAdd`). Unverified end to end — Task 42's engine test undoes `tailnet.account.connect` on a controller engine.
- Provider facts this plan builds on: the Tailscale OAuth scopes and their dependencies, whether `GET /api/v2/tailnet/{t}/keys` returns descriptions, the `acl/validate` answer for a valid policy, the Cloudflare token-verify endpoint for an account token, and whether creating a tunnel or reading its token needs a permission beyond "Cloudflare Tunnel Write", "Zone Read" and "DNS Write": unverified — Task 1 settles each against the providers' documentation and writes the result into spec 4.2a and 7.1 (spec "Parallel decomposition": every part with a contract check runs it first). The fakes (Tasks 17, 18) follow what Task 1 records.
- Ruling: spec 1.9 names the `cloudflare.hostnames` service only as "the Cloudflare hostname driver"; recovery of an operation found mid-phase is the actions' `Recoverer` (spec 7.3, 1.3b). The service is read-only towards Cloudflare: every 5 minutes it records each managed tunnel's drift and each hostname's health in the module's tables, so `cloudflare.tunnel.list` and `cloudflare.hostname.list` show them, and audits each change of drift or health (Contracts "Cloudflare hostnames service").
- Ruling: a "reserved hostname" (spec 7.3) is one whose service host is an address in `172.31.255.0/24`, the range spec 8 draws the panel network from; `hostname.add` with such a service is refused with `cloudflare.reserved` unless `env.Call.ParentAction` is `panel.exposure.add`, and `hostname.remove` of such a row unless it is `panel.exposure.remove`. `hostname.add` takes an optional `noTLSVerify`, which spec 8's tunnel hostname needs.
- The Cloudflare `PUT .../configurations` and DNS record delete carry no version (spec 7.1, 7.3), so `hostname.add`/`remove` are read-then-write checks; their plans say so in a diff line.
- `internal/platform/dockerapi` is not frozen (spec 1.12 freezes no 0b package), and a later wave may edit an earlier wave's files (spec "Parallel decomposition"); Task 2 makes its two fixes there.
- `github.com/tailscale/hujson` is already required and imported by `internal/modules/tailnet/deps.go` (read 2026-10-30), so Task 6 adds no dependency; `deps.go` stays as it is.

## Task index

1. Contract check: Tailscale scopes and policy API, Cloudflare permissions
2. dockerapi: reject empty ids and wrap the negotiation error
3. dstate: Docker effect state documents
4. compose: parse the resolved configuration
5. docker: client seam, fakes and test host
6. policy: convergent HuJSON edits and the line diff
7. ingress: tunnel ingress rules and the configuration hash
8. web: Docker types, fixtures and the stream reader
9. web: tailnet types, fixtures, change dialog and capability list
10. web: Cloudflare types, fixtures and change dialog
11. logfilter: mask registered secrets across log frames
12. tailnet: tables, store and cleanupPending
13. cloudflare: tables, store and cleanupPending
14. docker: preflight and the docker.engine resource
15. docker: journaled effects, observation and the owned refusal
16. docker: containers, images, networks and volumes resources
17. tailnet: fake Tailscale API server
18. cloudflare: fake Cloudflare API server
19. web: Docker change dialog, networks, volumes and projects tabs
20. web: container logs and stats panels
21. web: tailnet account panel
22. web: Cloudflare account panel
23. web: tailnet devices, approvals and auth keys
24. compose: refuse unsupported configurations
25. docker: logs and stats streams
26. docker: projects table, discovery and docker.projects
27. docker: container start, stop and restart
28. docker: container remove
29. docker: image remove and prune
30. docker: network and volume create and remove
31. docker: network connect and disconnect
32. cloudflare: API client
33. web: Docker page, containers and images tabs
34. web: tailnet policy editor and page
35. web: Cloudflare tunnels, hostnames and page
36. docker: container inspect, logs and reveal-env
37. docker: image pull and the pull stream
38. docker: Compose project checks and docker-config
39. tailnet: API client and OAuth token cache
40. web: Docker container page
41. docker: Compose up, down, pull and restart
42. tailnet: account connect and disconnect
43. cloudflare: account connect and disconnect
44. docker: register actions, resources and streams
45. tailnet: device list, authorize and set-routes
46. tailnet: device expire and delete
47. cloudflare: tunnel create and delete
48. cloudflare: tunnel list, token and adopt
49. tailnet: auth key create, list and resolution
50. tailnet: key sweep service
51. tailnet: policy get and set
52. cloudflare: hostname add and its recovery
53. cloudflare: hostname list and the hostnames service
54. harness: TestM4 and the Docker object scenarios
55. tailnet: policy patch
56. cloudflare: hostname remove and its recovery
57. tailnet: register actions and the key sweep
58. cloudflare: register actions and the hostnames service
59. harness: M4 Compose scenarios with process shims
60. harness: TestDockerLive and the M4 workflow step

---
