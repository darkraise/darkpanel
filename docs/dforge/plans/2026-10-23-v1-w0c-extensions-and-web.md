# v1 Wave 0c: Extension Points, Cloud Plumbing, Listeners and Web Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: the skill the **Execution:** line names — dforge:subagent-driven-development for `subagent`, dforge:executing-plans for `inline`. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Finish wave 0 of the v1 completion spec: the extension package `internal/core/ext` and its frozen call sites (spec 1.12), cloud plumbing (1.9), trusted proxies and runtime listeners (1.10), the web certificate sources, the module skeletons and every error code and dependency of section 11, the CLI and path-selection hooks, the restore-quarantine gate, the harness helpers, the web extension points with the change dialog and 1.3's web UI for undo, repair, release and per-effect resolution, ADR 0014, and register rows 92 and 93.

**Architecture:** Shared types land first in `internal/core` and `internal/core/ext` (Tasks 2, 3 and 19), so every later task consumes fixed names. Runtime listeners are split three ways: the `core_listeners` store in `internal/controller/listen`, the request wrapper in `internal/controller/api` (it owns request semantics: client address, forwarded protocol, origin check, server-sent-event behaviour), and the listener service in `internal/controller/listen`, which takes the wrapper as a handler factory. The restore-quarantine flag is one `core_meta` key read through `internal/controller/quarantine`; the dispatcher, the controller engine, the rollout driver and scheduler and the controller service runner each consult it through a function-valued config field, so no package gains a dependency on another's internals. The controller assembly wires everything last (Tasks 46 to 48). The web side adds the API client calls first, then the change dialog, then the pages that use it.

**Tech Stack:** Go 1.27 (`GOTOOLCHAIN=local`), `database/sql` over `modernc.org/sqlite` through `internal/core/store`, `net/http`, `net/netip`, `crypto/tls`, `github.com/spf13/cobra`, `github.com/tailscale/hujson` (new, Task 29); web: TypeScript, React 19, `@tanstack/react-query`, `darkraise-ui`, `react-router`, Vitest with jsdom, `vitest-axe` and `@playwright/test` (new development dependencies, Task 16).

**Spec:** docs/dforge/specs/2026-10-21-v1-completion-design.md

**Execution:** subagent — `claude --model sonnet --effort high` — the plan runs parallel waves, and each task's **Implementer:** line chooses its implementer

**Parallelism:** waves — 1-3 | 4-6 | 7-9 | 10-12 | 13-15 | 16 | 17 | 18 | 19-21 | 22-24 | 25-27 | 28 | 29 | 30 | 31 | 32 | 33-35 | 36-38 | 39 | 40 | 41 | 42-44 | 45-46 | 47 | 48 | 49

**Integration check:** `GOTOOLCHAIN=local go build ./... && GOTOOLCHAIN=local go test -count=1 ./... && GOTOOLCHAIN=local go vet ./... && golangci-lint run --allow-serial-runners && GOTOOLCHAIN=local go mod tidy -diff && make lint-web test-web`

**Worktree setup:** `make web-deps`

**Plan review:** 2026-10-23 — dforge:judge-opus — executability 16 / coherence 16 / coverage 17 / assumptions 16 (round 1)

## Global Constraints

- Scope is spec revision 8 as amended on `main`: sections 1.9, 1.10, 1.12, 11 and the entries of Contracts that concern them, the web UI of 1.3 ("the UI offers Undo on undoable changes and Repair, Release and per-effect resolution (preview first) on `needs_repair` changes"), the change dialog of 1.12 including the availability greying w0b left to it, ADR 0014, and register rows 92 and 93 of `docs/dforge/registers/2026-10-21-v1-completion.md`. Later work, which this plan does not build:
  - wave 0b parts 0b-1 to 0b-4 (register row 79): `internal/platform/dockerapi`, `tslocal`, `netstat`, `netfilter`;
  - every module's behaviour: a skeleton has an empty manifest, its CLI stub returns no commands, its web stubs render nothing;
  - wave 7: backup, restore, the quarantine marking and the quarantine commands and routes; this plan builds only the flag reader and the gate;
  - wave 4: the Tailscale certificate source and names (this plan's stubs fall through and return none);
  - wave 8: `ProxyPath.Public` (this plan leaves it empty) and the panel exposure itself;
  - register rows 82 and 91 (assigned to wave 1) and row 94 (the owner's self-hosted runner environment).
  Where this plan builds part of a later section, it builds exactly the part a task names and nothing more.
- Build only with the toolchain `go.mod` pins: every `go` command runs with `GOTOOLCHAIN=local` (ADR 0001).
- `/tmp` on this host is an 8 GiB tmpfs that parallel Go builds fill. Every `go` command, `golangci-lint`, `make` and `pnpm` runs with `TMPDIR=/root/.cache/w0c-tmp/<task number>`, also where a step's command omits it. Create the directory with `mkdir -p` and empty it when the task ends.
- Dependencies: only Task 16 edits `web/package.json` and `web/pnpm-lock.yaml`, and only Task 29 edits `go.mod` and `go.sum`. After every task `GOTOOLCHAIN=local go mod tidy -diff` prints nothing.
- Frozen after this plan (spec 1.12): `internal/core/contract.go`, `internal/core/units.go`, `internal/core/define.go`, `internal/core/registry.go`, `internal/core/ext/**`, `internal/core/errcode/errcode.go`, `internal/core/lifecycle/**`, the core migrations, `internal/platform/exec.go`, `files.go`, `systemd.go`, `internal/modules/modules.go`, `internal/cli/modules.go`, `internal/controller/server/**`, `internal/controller/api/**`, `internal/controller/events/**`, `internal/controller/listen/sources.go`, `cmd/darkpanel/paths.go`, `go.mod`, `go.sum`, `web/package.json`, `web/pnpm-lock.yaml`, `web/src/modules/registry.ts`, `web/src/modules/types.ts`, `web/src/app.tsx`, `test/harness/pins.go`. A later wave cannot add to them, so every name the Contracts below give these files is built in full here.
- Values copied from the spec, used exactly:
  - Cloud client: a 15 s timeout per request; at most 3 retries, for `GET` and `DELETE` only, with backoff, honouring `Retry-After`; an uncertain write's `CommitWait` is 10 minutes (its `CommitBy` is the call's start plus 10 minutes).
  - Runtime listeners: state `open | revoked | closed`; client headers `X-Darkpanel-Client` (NPM) and `Cf-Connecting-IP` (tunnel); `X-Forwarded-For` and `X-Real-IP` are never read; a missing or malformed client header refuses the request; a forwarded protocol other than `https` refuses the request; SSE responses on a runtime listener carry `X-Accel-Buffering: no`; a listener found `revoked` at start is not opened.
  - Extension routes: `SurfaceWeb` routes get session or token authentication, CSRF on unsafe methods and the 60 s write timeout; `Stream` routes get a 30 s deadline per write instead; a route pattern that clashes with an existing route fails startup.
  - Services: an error from `Run` restarts it with backoff from 1 s to 5 min; services start after composite recovery and only while `restore_quarantine` is unset; at shutdown their contexts are cancelled and the assembly waits for each `Run` to return; a service's audit actor is `system:<Service.Name>`.
  - The certificate source's internal reads run as `core.Principal{Actor: "system:listen.cert", Scope: core.ScopeAdmin}`.
  - The quarantine flag is `core_meta` key `restore_quarantine` in `controller.db`, whose value is the JSON object `{"tx", "restoredAt", "backupCreatedAt"}`; refusals use `core.quarantined`.
- Names copied from the spec: the eight module ids `firewall`, `docker`, `tailscale`, `tailnet`, `derp`, `npm`, `cloudflare`, `panel`; the CLI map `moduleCommands` and the slice `controllerCommands` with `backupCommands`; `selectPaths` and `addRootFlags`; `certSources`, `extraNames`, `tailscaleCertSource`, `tailscaleNames`; `backup.Contribute`; the web files `web/src/modules/<id>/{routes,container-actions,host-create,global}.tsx`, `web/src/pages/create-host-dialog.tsx`, `web/src/components/change-dialog.tsx`; the Makefile target `fuzz` running `scripts/fuzz.sh`; every error code of section 11 with the status and title of its table row.
- JSON fields are camelCase. Migrations are expand-only (ADR 0005): `CREATE TABLE`, `CREATE INDEX`, `ADD COLUMN`.
- Nothing in a unit test talks to the real systemd, D-Bus, the Docker socket, a network beyond `127.0.0.1`, `/etc`, `/var/lib`, `/run` or `/usr/local`:
  - servers use `127.0.0.1:0` or `httptest`; Unix sockets live under `os.MkdirTemp("", "dp")`; runtime-listener tests bind `127.0.0.1:0`, and a test that needs a second loopback address uses `127.0.0.2` only after checking it can bind it, skipping otherwise;
  - time comes from `coretest.NewFakeClock` wherever a duration above one second is waited on, including every retry, backoff, poll and restart timer;
  - web tests use Vitest's jsdom with an injected `EventSource` constructor where a stream is opened (`web/src/test/evt-source.ts`).
  Tasks of one wave therefore share no port, path or global state.
- This host runs an installed `darkpanel-agent` and `darkpanel-controller` whose units are active. No test, step or command starts, stops, restarts, reloads or otherwise touches them, and no command writes under `/var/lib/darkpanel`, `/etc/darkpanel` or `/run/darkpanel`.
- Tests run as root on this host (`id -u` prints 0). A test that cannot hold as root skips as root, as `internal/platform/files_test.go` does.
- Tasks that share a Go package declare no top-level identifier, test helpers included, that another task of the plan declares. A task's new test helpers carry a short prefix of its own, which its opening paragraph names. Existing shared fixtures are used as written and extended only by a task that lists their file in **Files:**: `newHarnessWith` in `internal/controller/api/server_test.go`, `newHarnessWith` in `internal/controller/dispatch/fixture_test.go`, `newFixtureWith` in `internal/core/lifecycle/fixture_test.go`, `testRegistry` in `internal/cli/fixture_test.go`, and `renderWithProviders` (or the existing helper of that file) in `web/src/test/utils.tsx`.
- Comments follow the repository style: full sentences that explain why, never what. New comments name no plan, task, register row or review.
- Error strings start with the package prefix (`core:`, `ext:`, `lifecycle:`, `api:`, `dispatch:`, `listen:`, `server:`, `cloud:`, `cloudfake:`, `quarantine:`, `rollout:`, `backup:`, `cli:`, `harness:`, and the module's id for a module package). A typed error built with `errcode.New` or `errcode.Wrap` carries its code instead.
- Lint: `golangci-lint run --allow-serial-runners`, `GOTOOLCHAIN=local go vet ./...` and, for a task that edits `web/`, `make lint-web` stay clean.
- Each task of a wave runs in its own worktree from the wave's base, so a package-wide command sees only merged work plus the task's own files.
- Commits use `<type>(<scope>): <subject>`, subject at most 50 characters, imperative, no period, and carry no attribution lines.
- The plan ends with the gates in `docs/dforge/gates.md`. Tasks 14, 15, 17, 22 and 34 touch `test/harness/`, so gate 7 (harness) applies to the branch once it is pushed, as well as on `main`; dispatch `harness.yml` on the branch per the owner's standing authorization. No task creates or pushes a tag.

## Contracts

Each entry names the task that creates it. A task's Interfaces block cites entries by number.

**C1 — Error codes (Task 1), `internal/core/errcode/errcode.go`.** One constant per section 11 code that the registry lacks, with the status and title of section 11's table, in the problem or action `const` block its Kind names. Constant names: `FirewallManualRequired`, `FirewallUnsupportedIntegration`, `FirewallUnsupportedConfiguration`, `FirewallPredictionMismatch` (action), `FirewallDockerUserInUse`, `DockerEngineUnavailable`, `DockerInUse`, `DockerOwned`, `DockerComposeUnavailable`, `TailscaleUnavailable`, `TailscaleRepoConflict`, `TailscalePackagePresent`, `TailscalePackageHeld`, `TailscalePinFailed` (action), `TailnetPolicyConflict`, `TailnetPolicyInvalid`, `CloudNotConnected`, `CloudAuthFailed`, `CloudPermissionMissing`, `CloudRateLimited`, `CloudUnavailable`, `CloudUncertainOutcome` (action), `CloudConflict`, `CloudCleanupPending`, `NPMUnhealthy`, `NPMNotPublic`, `NPMSetupFailed` (action), `NPMReserved`, `NPMUnsupportedTarget`, `CloudflareReserved`, `CloudflareUnsupportedTarget`, `CloudflareConnectorMissing`, `DERPRegionTaken`, `DERPPortInUse`, `DERPVerifyFailed` (action), `DERPNotReady`, `DERPNoFallback`, `PanelTOTPIncomplete`, `PanelAlreadyExposed`, `PanelWrongHost`, `PanelNotExposed`, `Quarantined` (`core.quarantined`). The eight `core.*` codes already present stay as they are.

**C2 — Extension package (Task 19), `internal/core/ext/ext.go`.** Exactly the declarations of spec 1.12: `Surface` with `SurfaceWeb` and `SurfaceSocket`; `Route{Surface, Method, Path, Summary, Scope, Stream, Handler}`; `Service{Name, Run}`; `Deps{DB, Sealer, Clock, Log, Hosts, Dispatch, Events, Listeners, Audit, LocalHostID}`; `Dispatcher` (`Read`, `ReadInternal`, `Plan`, `Apply` with the signatures of spec 1.12); `Publisher{Publish(eventType string, data any)}`; `Controller{Controller(deps Deps) ([]Route, []Service, error)}`. The package imports only `internal/core`, `internal/core/store` and the standard library.

**C3 — Runtime listener types (Task 2), `internal/core/listeners.go`.** `Listeners` (`Open`, `Revoke`, `Close`, `Get`), `ListenerSpec{Addr netip.AddrPort, AllowedPeer netip.Addr, ClientHeader, PublicHost, ChangeID string}`, `ListenerInfo{ID string, Spec ListenerSpec, State string}` exactly as spec 1.12, plus:

```go
// The states of a runtime listener (core_listeners.state).
const (
	ListenerOpen    = "open"
	ListenerRevoked = "revoked"
	ListenerClosed  = "closed"
)

// The client headers a runtime listener may read (1.10).
const (
	ClientHeaderNPM    = "X-Darkpanel-Client"
	ClientHeaderTunnel = "Cf-Connecting-IP"
)
```

`Env` (`internal/core/contract.go`) gains `Listeners Listeners // controller-side actions only; nil on the agent`, placed after `Units` (also Task 2).

**C4 — Proxied requests (Task 3), `internal/core/peer.go`.**

```go
// Proxied describes a request that reached the controller through a
// runtime listener (1.10). Path.ExposureID is the listener's
// ListenerSpec.ChangeID, Path.Kind is "npm" for ClientHeaderNPM and "tunnel"
// for ClientHeaderTunnel, Path.ProxyAddr is the allowed peer and
// Path.ListenerAddr the listener's address; Path.Public is left empty.
type Proxied struct {
	ListenerID string
	Client     netip.Addr     // the address the listener's client header named, unmapped
	Transport  netip.AddrPort // the proxy's TCP peer
	PublicHost string         // the exposure's public host name
	Path       ProxyPath
	Revoked    <-chan struct{} // closed once the listener is revoked
}

// WithProxied returns ctx carrying p.
func WithProxied(ctx context.Context, p Proxied) context.Context
// ProxiedFrom returns the Proxied WithProxied stored in ctx.
func ProxiedFrom(ctx context.Context) (Proxied, bool)
```

**C5 — Cloud test scaffolding (Task 4), `internal/controller/cloud/cloudfake`.**

```go
// Server is an in-process provider stand-in: an httptest TLS-less server
// that records every request and injects faults.
type Server struct{ /* unexported */ }

// New starts a server closed by tb.Cleanup.
func New(tb testing.TB) *Server
func (s *Server) URL() string
func (s *Server) Client() *http.Client
// Handle routes method and path (a net/http ServeMux pattern without the
// method) to h.
func (s *Server) Handle(method, path string, h http.HandlerFunc)
// Fail queues f for the next f.Times requests matching method and path
// (Times 0 means 1). A queued fault is used before the handler runs.
func (s *Server) Fail(method, path string, f Fault)
// Requests returns the recorded requests in arrival order, faulted ones included.
func (s *Server) Requests() []Request

// Fault is one injected failure. Status answers with that status (and
// RetryAfter as the Retry-After header when set) without running the
// handler. LoseResponse runs the handler, so the write commits, and then
// closes the connection without sending its answer.
type Fault struct {
	Status       int
	RetryAfter   string
	Body         string
	LoseResponse bool
	Times        int
}

// Request is one recorded request.
type Request struct {
	Method, Path, RawQuery string
	Header                 http.Header
	Body                   []byte
}
```

**C6 — Web development dependencies (Task 16).** `web/package.json` gains the exact devDependencies `vitest-axe` `1.0.0-pre.5` and `@playwright/test` `1.64.0`; `web/src/test/setup.ts` registers `vitest-axe`'s matchers, so any test may `expect(await axe(container)).toHaveNoViolations()` after `import { axe } from "vitest-axe"`.

**C7 — Module skeletons (Task 8; the hujson import Task 29).** Packages `internal/modules/<id>` for the eight ids, each with `const ID` and `func Module() core.Module` returning a module whose manifest is `core.Manifest{ID: ID, Version: 1, DefaultEnabled: true}` and nothing else (the manifest has no `Summary` field); `internal/modules/modules.go`'s `All` lists them after `hostinfo` in the order firewall, docker, tailscale, tailnet, derp, npm, cloudflare, panel. `internal/modules/tailnet/deps.go` holds the blank import of `github.com/tailscale/hujson` that keeps the dependency in `go.mod`.

**C8 — Cloud client (Task 23; sealing and probes Task 35), `internal/controller/cloud`.**

```go
const (
	Timeout    = 15 * time.Second // per request
	MaxRetries = 3                // GET and DELETE only
	CommitWait = 10 * time.Minute // Effect.CommitWait of every cloud write
)

type Config struct {
	Provider string                       // "tailscale" | "cloudflare" | "npm": named in error details
	BaseURL  string                       // scheme://host[:port][/prefix], no trailing slash
	Auth     func(r *http.Request) error  // adds credentials to every attempt; nil adds none
	HTTP     *http.Client                 // nil: a client with Timeout
	Clock    core.Clock                   // nil: core.SystemClock{}
	Sleep    func(ctx context.Context, d time.Duration) error // nil: waits on Clock.AfterFunc or ctx
	ErrorCode func(body []byte) string    // the provider's error code in an error body; nil reads none
}

type Request struct {
	Method string
	Path   string // joined to BaseURL
	Query  url.Values
	Header http.Header
	Body   []byte // already encoded; nil sends none
}

type Response struct {
	Status int
	Header http.Header
	Body   []byte
}

func New(cfg Config) (*Client, error)
// Do sends req. A 2xx answer returns its Response and a nil error. Every
// other outcome returns an *errcode.Error whose Detail names the provider,
// its HTTP status and its error code, never the body:
//   401 → CloudAuthFailed; 403 → CloudPermissionMissing; 409 and 412 →
//   CloudConflict; 429 → CloudRateLimited (GET/DELETE after their retries);
//   5xx or a transport failure → CloudUnavailable for GET/DELETE after their
//   retries, CloudUncertainOutcome for every other method (never retried);
//   any other status → errcode.InvalidInput for 4xx, CloudUnavailable otherwise.
// Every 4xx answer to a method other than GET and DELETE is wrapped with
// core.Definite: the provider refused it, so nothing committed.
func (c *Client) Do(ctx context.Context, req Request) (Response, error)
// JSON encodes in (nil sends no body), calls Do with Content-Type
// application/json, and decodes a 2xx body into out (nil skips decoding).
func (c *Client) JSON(ctx context.Context, method, path string, in, out any) (Response, error)
// Status returns the provider HTTP status that err carries, or 0.
func Status(err error) int

// SealCredential and OpenCredential seal an account secret with associated
// data "<table>.<column>.<id>" (ADR 0005).
func SealCredential(s core.Sealer, table, column, id string, secret []byte) ([]byte, error)
func OpenCredential(s core.Sealer, table, column, id string, ciphertext []byte) ([]byte, error)

// Capability is one probed provider capability.
type Capability struct {
	Name  string `json:"name"`
	State string `json:"state"`            // "ok" | "missing" | "unverified"
	Detail string `json:"detail,omitempty"` // a missing capability: the error code
}
// Probe is one harmless read that proves a read capability.
type Probe struct {
	Name string
	Read func(ctx context.Context) error
}
// The states of a Capability.
const (
	CapabilityOK         = "ok"
	CapabilityMissing    = "missing"
	CapabilityUnverified = "unverified"
)
// ProbeReads runs every probe and reports each as ok or missing, then
// appends writes as unverified, in the order given.
func ProbeReads(ctx context.Context, probes []Probe, writes []string) []Capability
```

Further behaviour of `Do`: each attempt is bounded by `Timeout` through its own context, whatever `HTTP` client is passed; retries wait 1 s, 2 s and 4 s, or the provider's `Retry-After` (seconds or an HTTP date), and an unreadable `Retry-After` falls back to the 1/2/4 s delays while one over 60 s ends the retries at once; the default client refuses redirects, and a 3xx answer is `CloudUnavailable`; a response body over 8 MiB is refused; an `Auth` or request-build failure sends nothing and is wrapped with `core.Definite`; an unreadable 2xx JSON body in `JSON` is `CloudUnavailable`.

**C9 — Runtime listener store (migration Task 18, store Task 28).** Controller core migration `internal/core/store/migrations/controller/0012_listeners.sql` creating `core_listeners` (`id TEXT PRIMARY KEY`, `address TEXT NOT NULL`, `port INTEGER NOT NULL`, `allowed_peer TEXT NOT NULL`, `client_header TEXT NOT NULL` checked against the two C3 headers, `public_host TEXT NOT NULL`, `change_id TEXT NOT NULL`, `state TEXT NOT NULL` checked against the three C3 states, `created_at TEXT NOT NULL`, `updated_at TEXT NOT NULL`), a `STRICT` table. In `internal/controller/listen/runtime_store.go`: the constant `listenerTimeFormat` (`"2006-01-02T15:04:05.000000000Z"`, the stored UTC time form), `insertListener(ctx, tx *sql.Tx, id string, spec core.ListenerSpec, now time.Time) error`, `setListenerState(ctx, tx *sql.Tx, id, state string, from []string, now time.Time) (bool, error)` (a conditional update that reports whether a row changed; it refuses an empty `from` and matches it through `json_each`), `getListener(ctx, db *store.DB, id string) (core.ListenerInfo, error)` (`errcode.NotFound` when absent) and `listListeners(ctx, db *store.DB, state string) ([]core.ListenerInfo, error)` (by `created_at`, then `id`).

**C10 — Runtime request wrapper (Task 30), `internal/controller/api/runtime.go`.**

```go
// RuntimeHandler serves one runtime listener: it refuses a request on a
// revoked listener, a forwarded protocol other than https and a missing or
// malformed client header, then serves s.Handler() with core.Proxied in
// the request's context. revoked is closed once the listener is revoked;
// isRevoked re-reads the durable state for every request.
func (s *Server) RuntimeHandler(info core.ListenerInfo, revoked <-chan struct{}, isRevoked func(ctx context.Context) (bool, error)) http.Handler
```

Refusals: a revoked listener answers 404 `not_found`; a durable state that cannot be read answers 503 `unavailable`; a forwarded protocol other than `https`, or a missing or malformed client header (several values, an address with a port, an address with a zone), answers 403 `auth.forbidden`.

**C11 — Proxied semantics in the API (Task 31).** For a request carrying `core.Proxied`: the guard context's client peer is `{Addr: Client, Port: 0}`; the principal's and the audit entry's `Source` is `"<Client> via <Transport.Addr()>"` (both addresses, no port; also the key login rate limiting uses); every unsafe-method request through a runtime listener, login included, whose `Origin` header is present and is not `https://<PublicHost>` is refused with `auth.csrf`; every SSE response carries `X-Accel-Buffering: no` and ends with an `end` frame of reason `revoked` once `Revoked` closes. Spec 1.10's durable check "on every stream event" is met through `Revoked`: `Revoke` commits the durable state before it closes the channel, in the same process, and the channel is the only way a running stream learns of it. Requests on other listeners are unchanged.

**C12 — Proxy path in the guard context (Task 24), `internal/controller/dispatch/guard.go`.** When the plan request's context carries `core.Proxied`, the dispatcher fills `GuardContext.Via` with a copy of its `Path`.

**C13 — Web certificate sources (Task 36, Task 42), `internal/controller/listen`.** `sources.go` exactly as spec 1.12 (`CertSource`, `SourceDeps`, `certSources`, `extraNames`). The stub `tailscale_cert.go` has `tailscaleCertSource(d SourceDeps) CertSource` (always falls through) and `tailscaleNames(ctx context.Context, d SourceDeps) ([]string, error)` (returns none). `Leaf.Ensure` becomes `Ensure(ctx context.Context, ips []netip.Addr) (bool, error)`; it reissues when the IPs, the sources' extra names or the renewal age change, and adds the extra names to the CA leaf's DNS SANs (sorted, deduplicated, without the hostname or `localhost`); when the extra names cannot be read the current names are kept and the error logged. `Leaf.GetCertificate` asks `certSources` in order; a source error is logged and skipped; the CA leaf is the fallback. Unexported `(*Leaf).useSources(d SourceDeps)` (Task 36). `(*Leaf).EnsureIssued(ctx context.Context) error` issues a leaf without IP SANs when none exists and never replaces one. `listen.Config.Sources *SourceDeps` (nil: none). `(*Service).TLSConfig() (*tls.Config, error)` calls `EnsureIssued`, then returns the one shared `tls.Config` (Task 42).

**C14 — Quarantine flag (Task 25; the dispatcher gate Task 26), `internal/controller/quarantine`.**

```go
const MetaKey = "restore_quarantine"

// Marker is the flag's value.
type Marker struct {
	Tx              string    `json:"tx"`
	RestoredAt      time.Time `json:"restoredAt"`
	BackupCreatedAt time.Time `json:"backupCreatedAt"`
}

// Read returns the marker and whether the flag is set.
func Read(ctx context.Context, db *store.DB) (Marker, bool, error)
// Active reports whether the flag is set.
func Active(ctx context.Context, db *store.DB) (bool, error)
// Check returns errcode.Quarantined while the flag is set.
func Check(ctx context.Context, db *store.DB) error
```

`dispatch.Config` gains `Quarantine func(ctx context.Context) error` (nil: never quarantined), called before every `Apply`, `Undo`, `Repair`, `Release` and `Resolve`, and before the composite runner's step `Undo` and `Release`; plans, reads, confirm, revert, `Lookup`, `Retire` and `AckPrivate` stay admitted. The auth module changes users and tokens only inside actions (login's `totp_last_step` and last-seen times excepted), so gating actions is complete.

**C15 — Engine suspension (Task 12), `internal/core/lifecycle`.** `lifecycle.Config` gains `Suspended func(ctx context.Context) (bool, error)` (nil: never suspended). While it answers true, the controller engine's composite recovery, its child watcher and its module recovery handlers do nothing, and `Wake` does nothing; an error from it counts as suspended and is logged. A recovery handler run while suspended returns `errRecovererSuspended`, leaving the change `recovering` and owned by its module; `CompositesRecovered` reports true while suspended.

**C16 — Rollout suspension (Task 13), `internal/controller/rollout`.** `DriverConfig` and `SchedulerConfig` gain `Suspended func(ctx context.Context) (bool, error)` (nil: never suspended). A pass that finds it true does nothing; an error counts as suspended and is logged.

**C17 — Controller service runner (Task 39), `internal/controller/server/extservices.go`.**

```go
type serviceRunnerConfig struct {
	Services  []ext.Service
	Suspended func(ctx context.Context) (bool, error) // nil: never suspended
	Poll      time.Duration                           // how often a suspended runner re-checks; 0 means serviceDefaultPoll
	Clock     core.Clock
	Log       *slog.Logger
}
const serviceDefaultPoll = 10 * time.Second

// startServiceRunner starts every service once Suspended answers false, and
// restarts a service whose Run returned an error after a delay from 1 s
// doubling to 5 min, reset by a run that lasted 5 min or longer.
func startServiceRunner(cfg serviceRunnerConfig) *serviceRunner
// stop cancels every service's context and waits for each Run to return.
func (r *serviceRunner) stop()
```

A `Run` that returns nil or panics before shutdown restarts like one that returned an error; a panic is logged by its type unless it is a runtime error; suspension is re-checked before every restart.

**C18 — API extension routes (web Task 41; socket and OpenAPI Task 44), `internal/controller/api`.** `api.Config` gains `Extensions []ext.Route`. `New` mounts each `SurfaceWeb` route on the network mux behind the existing authentication and CSRF middleware with a scope check (`auth.forbidden` below `Route.Scope`), each `SurfaceSocket` route on the controller socket's mux behind its peer check, and fails with an error naming the pattern when a route clashes with an existing one. An extension path must lie under `/api/v1/` and not end in `/`; the scope and the path are validated before clashes. `Stream` routes share the built-in stream lifecycle (passive authentication through a stream-pattern mux, `SSERecheck` re-authentication, `SSEHardLimit`, `CloseStreams`, `Proxied.Revoked`, `X-Accel-Buffering: no` behind a runtime listener) plus a 30 s write deadline renewed on every write; no shared stream cap exists. `(*Server).OpenAPIRoutes() []schema.Route` returns `Routes()` followed by the `SurfaceWeb` extension routes only, and the served OpenAPI document uses it.

**C19 — Runtime listener service (Task 40), `internal/controller/listen/runtime.go`.**

```go
// RevokeGrace bounds how long a revoked listener lets its running responses
// finish before it cuts the connections.
const RevokeGrace = WriteTimeout

type RuntimeConfig struct {
	DB    *store.DB
	Clock core.Clock
	Log   *slog.Logger // nil: discards
	Bind  BindFunc     // nil: Bind
}
// RuntimeHandlerFunc builds the handler of one runtime listener (api.Server.RuntimeHandler).
type RuntimeHandlerFunc func(info core.ListenerInfo, revoked <-chan struct{}, isRevoked func(ctx context.Context) (bool, error)) http.Handler

func NewRuntime(cfg RuntimeConfig) *Runtime
// Start binds every listener in state open, those Open recorded before Start included.
func (r *Runtime) Start(ctx context.Context, handler RuntimeHandlerFunc, tlsConfig *tls.Config) error
func (r *Runtime) Open(ctx context.Context, spec core.ListenerSpec) (string, error)
func (r *Runtime) Revoke(ctx context.Context, id string) error
func (r *Runtime) Close(ctx context.Context, id string) error
func (r *Runtime) Get(ctx context.Context, id string) (core.ListenerInfo, error)
// Addrs are the bound addresses of the listeners that still accept, for the guard topology.
func (r *Runtime) Addrs() []netip.AddrPort
// Shutdown stops every listener, letting running requests finish until ctx ends.
func (r *Runtime) Shutdown(ctx context.Context) error
```

`*Runtime` implements `core.Listeners`. A listener serves HTTPS only, with a clone of `tlsConfig`. Behaviour:

- `Start` binds every open row, never a revoked or closed one; a failed bind is retried every `CheckInterval`; `Start` fails only when the rows cannot be read, and a second `Start` fails.
- `Open` validates the spec (an address with a port and no zone, an allowed peer without a zone, one of the two C3 headers, a public host and a change id; anything else is `request.invalid_input`; addresses are stored unmapped), records it open in one transaction and binds it once `Start` has run (a failed bind moves the row to closed and returns the error); before `Start` it only records; after `Shutdown` it fails.
- `Revoke` commits revoked first, closes the revoked channel, stops accepting and closes connections after their current response (cut after `RevokeGrace`); it succeeds on a revoked or closed listener and is `not_found` on a missing id.
- `Close` moves revoked to closed; it succeeds on closed, is `request.invalid_input` on open and `not_found` on a missing id.
- `Addrs` are the accepting listeners' addresses, unmapped and sorted. `Shutdown` keeps the rows' states.
- Connections from any peer but `AllowedPeer` are closed before they are read; every bind re-reads its row under the runtime's lock, so a concurrent revocation wins; `isRevoked` re-reads the row.

**C20 — Controller contributions (Task 38; startup Task 46; listeners Task 47).** `internal/controller/backup/contribute.go`: `func Contribute(deps ext.Deps) ([]ext.Route, []ext.Service, error)` returning nothing (wave 7 fills it). `internal/controller/server/extensions.go`: `(*Server).extDeps() ext.Deps` builds `ext.Deps` and `(*Server).contributions() ([]ext.Route, []ext.Service, error)` calls `Controller(deps)` on every module of `modules.All()` that implements `ext.Controller`, then on every entry of `server.Options.Contributors []ext.Controller` (set by tests only; nil in production), then `backup.Contribute(deps)`, in that order. Task 38 leaves `Deps.Listeners` nil and Task 47 sets it. Contributions are gathered in `startListeners` before `api.New`, every route goes to `api.Config.Extensions`, and services start through C17 after composite recovery and after the socket serves. Shutdown order: services, socket, stream responses, runtime listeners, network listeners. `topologyOf` gains a runtime-addresses parameter after the web addresses.

**C21 — Web API client (Task 11), `web/src/api/client.ts` and `types.ts`.** Types mirroring the Go wire DTOs field for field (the task reads `internal/controller/api/wire.go` and `internal/core/contract.go`): `Guard` gains `overridable` and `overridden`; `ChangeError` gains `cause?`; `Change` gains `parentChangeId`, `owner`, `repairedBy`, `repairOutcome`, `override`; new `StateDoc`, `Effect`, `Candidate`, `Observation`, `ObserveResult`, `Step`, `RepairResult`, `ReleaseResult`, `ActionAvailability`, `ModuleAvailability`. Functions: `getChange(id)`, `listEffects(id): Promise<Effect[]>`, `observeEffect(id, seq)`, `resolveEffect(id, seq, { resolution, resource? })`, `listSteps(id): Promise<Step[]>` (both return unwrapped arrays), `undoChange(id)`, `repairChange(id, body = {})`, `releaseChange(id, reason)`, `confirmChange(id)`, `revertChange(id)`, `getAvailability(hostId, module)`, `changeInProgress(change): boolean`; `applyPlan` accepts a `202` answer. Shapes: `changeError` is null for `applying`, `awaiting_confirmation`, `undoing`, `recovering` and `releasing`; `Step.action` is `{ Name, Version }`; `candidates`, `allowed` and `earlierChildChangeIds` may be null; the new `Change` fields are `string | null` and `override` is `unknown`; `ModuleAvailability.reason` is `string | null`; query keys `changeQueryKey(id) = ["change", id]`, `stepsQueryKey(id) = ["steps", id]`, `effectsQueryKey(id) = ["effects", id]`. `connectEvents` (`web/src/api/events.ts:60-61`) invalidates `["change", id]` and `["steps", id]` but never `["effects", id]`, so the change page keys its effects query on the change's `updatedAt` and state as well (Task 43), and every effect mutation invalidates `effectsQueryKey` itself.

**C22 — Web module extension points (types and helpers Task 10, registry entries Task 21, routes and sidebar Task 33).** `web/src/modules/types.ts` exactly as spec 1.12 (`ContainerRef`, `ContainerAction`, `HostCreateExtension`, `GlobalPage`, `ModuleEntry` with optional `routes`). `registry.ts` exports `HostModuleEntry = ModuleEntry & { routes: NonNullable<ModuleEntry["routes"]> }`; `enabledModules(entries, host): HostModuleEntry[]` and the host page skip entries without `routes`; `globalPages(entries: ModuleEntry[]): GlobalPage[]`; `hostCreateExtensions(entries: ModuleEntry[], host: Host): HostCreateExtension[]` (entries enabled on `host` that have `hostCreate`, enabled-but-unavailable ones kept). Registry entries, in order: `hostinfo`, `firewall`, `docker`, `tailscale`, `tailnet`, `derp`, `npm`, `cloudflare`, `panel`; host pages for firewall, docker, tailscale, npm and panel; global pages tailnet, derp and cloudflare; container actions `firewall.allow`, `firewall.deny`, `npm.expose`, `cloudflare.expose`; the host-create extension `tailnet.join`. `app.tsx` mounts `/<global.path>/*` per entry with a `global` page inside the session guard; `shell.tsx` shows the global pages in a "Network" sidebar group.

**C23 — Change dialog (Task 27), `web/src/components/change-dialog.tsx`.**

```ts
export interface ChangeTarget {
  name: string
  version: number
  hostId?: string
  module?: string // checked against getAvailability when hostId is set
}
export interface ChangeDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: string
  target: ChangeTarget
  input?: Record<string, unknown> // planned on open; the dialog adds "overrides"
  plan?: Plan                     // a plan already made (a repair's answer): shown and applied as is
  onApplied?: (change: Change) => void
}
export function ChangeDialog(props: ChangeDialogProps): JSX.Element
```

It shows the plan diff, the guard results with an override control for each failed guard marked `overridable` (re-planning with `overrides`), the typed confirmation for a plan with a `confirmation` phrase, the revert countdown with Confirm and Revert for a pending change, composite step progress from `listSteps` under `stepsQueryKey`, a disabled Apply with the reason when the action is unavailable on the host, and a link to `/changes/<id>` once applied. It plans through a query keyed `["plan", name, version, hostId, input, overrides]`, which `connectEvents` never refetches.

**C24 — Change page (Task 43).** Route `/changes/:changeId` (`web/src/pages/change.tsx`, exported `ChangePage`) inside the session guard; the audit log links each entry's change id to it.

**C25 — Harness labs (Task 14; the VM limit Task 17), `test/harness`.** `type Lab struct { Slug, Dir string; Index, McastPort, PrivMcastPort int }`; `NewLab(tb testing.TB) *Lab`; `(*Lab).VMName(role string) string`; `type NIC int` with `NICInet`, `NICMgmt`, `NICPriv`; `(*Lab).MAC(nic NIC, host int) string`; `InetIPv4`, `InetIPv6`, `InetAddrs`, `PrivPrefix`, `PrivAddr`; `(*Lab).Boot(ctx, t, cfgs []vm.Config) []*vm.VM`. Labs use index octets 1 to 254 and multicast ports from 40100. The `-vms` flag (default 3) is applied in `bootVMs`, which `Lab.Boot` uses. No task of this plan consumes them; later waves' harness tests do.

**C26 — Harness pins (Compose Task 15, images Task 22, pull script Task 34), `test/harness`.** `RequiredPackages` gains `docker-compose-plugin` (5.6.0) after `containerd.io`; `var RequiredContainers = []string{"pebble", "pebble-challtestsrv"}`; `type Container struct { Name, Ref string }`; `Pins.Containers []Container`; `func (p Pins) Container(name string) (Container, error)`; `func PullScript(cs ...Container) string`, appended to `TargetScript(o)`, pulls each image on the VM by digest (pebble and pebble-challtestsrv 2.10.1; the digests are in Task 22).

## Assumptions (evidence)

- The controller's newest core migration is `0011_platform_surfaces.sql`, so the runtime-listener table is `0012` — `ls internal/core/store/migrations/controller/` on 2026-10-23.
- `*events.Bus` already has `Publish(eventType string, data any)` (`internal/controller/events/bus.go:96`) and `*dispatch.Dispatcher` already has `Read`, `ReadInternal`, `Plan` and `Apply` with the `ext.Dispatcher` signatures (`internal/controller/dispatch/dispatch.go:155`, `:191`, `apply.go:22`, `reveal.go:27`), so both satisfy C2 without adapters; Task 38 adds the compile-time assertions.
- No `Origin` check exists today (`rg -n Origin internal/controller` on 2026-10-23 finds only `Cross-Origin-Opener-Policy`); C11's origin check, applied to runtime listeners only, is the "CSRF origin check" spec 1.10 refers to.
- The audit entry has a single `Source` field (`internal/core/contract.go:721`), so C11 keeps both addresses in it as `"<client> via <transport peer>"`.
- The engine already turns a non-`Local` effect left `intended` into `unresolved`/`uncertain` with `CommitBy` from `CommitWait` (`internal/core/lifecycle/settle.go:43`), so the cloud client only classifies errors (C8) and modules declare `CommitWait: cloud.CommitWait`.
- `github.com/tailscale/hujson` resolves to `v0.0.0-20260727124030-b80ff77dac4f` (`go list -m github.com/tailscale/hujson@latest` on 2026-10-23); unused, `go mod tidy` would drop it, hence C7's blank import.
- `vitest-axe` publishes `0.1.0` as latest and `1.0.0-pre.5` (peer `vitest >=1`, released 2025-01-22) as pre-release; `@playwright/test` `1.64.0` was released 2026-10-07 and `1.64.1` on 2026-10-14 (`pnpm view` on 2026-10-23). The plan pins `1.64.0`, past the two-week release age. `vitest-axe@1.0.0-pre.5` works under `vitest@5.0.5` once `web/src/test/setup.ts` imports `vitest-axe/extend-expect` (verified in a scratch worktree on 2026-10-23; Task 16 keeps the check).
- Docker accepts the reference form `name:tag@sha256:<digest>` and ignores the tag when a digest is given (the grammar of https://github.com/distribution/reference/blob/main/reference.go); no task of this plan runs `docker pull`, so it is first exercised by wave 6a's harness test.
- Task 32 adds `cli.Options.SelectPaths`; `selectPaths` may be called with a nil command (the update rollback path), so its implementations handle that.
- Socket-command tests pass `--dev` with temporary XDG directories: without them they dial the live `/run/darkpanel-controller/controller.sock` (an early draft run read its user list, read only, on 2026-10-23).
- The pinned golangci-lint is `v2.15.0` on both runners (`.github/workflows/ci.yml:99-101`), so register row 94's revive finding on the self-hosted runner comes from the runner's environment, not the repository; it stays out of this plan.
- Gating actions covers the spec's user and token mutations: outside the action routes the network API's only unsafe routes are login, logout and the change operations (`internal/controller/api/routes.go:26-54`, read on 2026-10-23), and `internal/controller/auth` changes users and tokens only inside the core actions' handlers (Task 26).
- The rest of each task's assumptions are verified in its first step.

## Task index

1. Error codes of section 11
2. Core runtime listener types
3. Proxied request context
4. Cloud provider test scaffolding
5. Composite confirm test waits for the runner
6. End frame when a stream's re-authentication fails
7. CLI module command map and stubs
8. Module skeletons
9. Path-selection hook
10. Web module entry types and registry helpers
11. Web API client for changes, effects and steps
12. Engine suspension hook
13. Rollout suspension hook
14. Harness lab names, directories, links and addresses
15. Harness pins for the Compose plugin
16. Web accessibility and browser test dependencies
17. Harness VM concurrency limit
18. Runtime listener migration
19. Extension package
20. CLI hand-written command mounting
21. Module web stubs and registry entries
22. Harness pins for Pebble and its challenge server
23. Cloud HTTP client
24. Proxy path in the guard context
25. Quarantine flag
26. Dispatcher quarantine gate
27. Change dialog
28. Runtime listener store
29. Tailnet policy parser dependency
30. Runtime listener request wrapper
31. Proxied requests in the API
32. CLI socket paths and the fuzz target
33. Global page routes and sidebar navigation
34. Harness script that pulls the pinned images
35. Cloud credential sealing and capability probes
36. Web certificate sources in the leaf
37. Create host dialog
38. Controller contributions
39. Controller service runner
40. Runtime listener service
41. API extension routes on the web surface
42. Listener service shares its TLS configuration and sources
43. Change page with effects and resolution
44. Socket extension routes and the OpenAPI document
45. Change actions for undo, repair and release
46. Controller routes and services at startup
47. Assembly of runtime listeners and certificate sources
48. Assembly of the restore quarantine
49. ADR 0014 and the deferred decisions

---

### Task 1: Error codes of section 11

This task adds every error code of spec section 11 that `internal/core/errcode` lacks, with the status and title of section 11's table (C1). The eight `core.*` codes w0a and w0b added stay as they are. Problem codes go in a new problem `const` block and action codes in a new action `const` block, each placed after the existing blocks it resembles; every code gets one registry line. The test is one new table test, `TestSection11Codes`, which checks value, registration, status and title of every new code; it uses no helper.

**Files:**
- Modify: `internal/core/errcode/errcode.go` (new `const` blocks after the update action block, lines 74-79; registry lines appended to `registry`, lines 86-143)
- Test: `internal/core/errcode/errcode_test.go` (append `TestSection11Codes`)

**Interfaces:**
- Consumes: none
- Produces: C1 (every constant name listed there)

**Items:** 52

**Depends on:** none

**Implementer:** dforge:impl-sonnet-low
**Evaluation:** files 1 - spec 0 - coupling 0 - risk 0 = 1

- [ ] **Step 1: Write the failing test**

Append to `internal/core/errcode/errcode_test.go`:

```go
// The module and core codes of the v1 completion spec (section 11) are
// shared contracts: modules, the CLI and the web UI match on their values,
// and problem details show their titles.
func TestSection11Codes(t *testing.T) {
	tests := []struct {
		code         Code
		value        string
		status       int
		title        string
	}{
		{FirewallManualRequired, "firewall.manual_required", 409, "Manual action required"},
		{FirewallUnsupportedIntegration, "firewall.unsupported_integration", 409, "Docker integration unsupported"},
		{FirewallUnsupportedConfiguration, "firewall.unsupported_configuration", 409, "Firewall configuration unsupported"},
		{FirewallPredictionMismatch, "firewall.prediction_mismatch", 500, "Firewall state differs from the prediction"},
		{FirewallDockerUserInUse, "firewall.docker_user_in_use", 409, "DOCKER-USER holds foreign rules"},
		{DockerEngineUnavailable, "docker.engine_unavailable", 503, "Docker Engine unavailable"},
		{DockerInUse, "docker.in_use", 409, "In use"},
		{DockerOwned, "docker.owned", 409, "Owned by a Darkpanel module"},
		{DockerComposeUnavailable, "docker.compose_unavailable", 409, "Compose unavailable or project refused"},
		{TailscaleUnavailable, "tailscale.unavailable", 503, "Tailscale unavailable"},
		{TailscaleRepoConflict, "tailscale.repo_conflict", 409, "Conflicting Tailscale repository"},
		{TailscalePackagePresent, "tailscale.package_present", 409, "Tailscale already installed"},
		{TailscalePackageHeld, "tailscale.package_held", 409, "Tailscale package held"},
		{TailscalePinFailed, "tailscale.pin_failed", 500, "Tailscale pin failed"},
		{TailnetPolicyConflict, "tailnet.policy_conflict", 409, "Policy changed since the plan"},
		{TailnetPolicyInvalid, "tailnet.policy_invalid", 422, "Policy invalid"},
		{CloudNotConnected, "cloud.not_connected", 409, "Account not connected"},
		{CloudAuthFailed, "cloud.auth_failed", 502, "Provider rejected the credentials"},
		{CloudPermissionMissing, "cloud.permission_missing", 502, "Provider permission missing"},
		{CloudRateLimited, "cloud.rate_limited", 503, "Provider rate limit"},
		{CloudUnavailable, "cloud.unavailable", 503, "Provider unavailable"},
		{CloudUncertainOutcome, "cloud.uncertain_outcome", 504, "Provider outcome uncertain"},
		{CloudConflict, "cloud.conflict", 409, "Provider state changed"},
		{CloudCleanupPending, "cloud.cleanup_pending", 409, "Cleanup pending on the account"},
		{NPMUnhealthy, "npm.unhealthy", 503, "Nginx Proxy Manager unhealthy"},
		{NPMNotPublic, "npm.not_public", 409, "Host not publicly reachable"},
		{NPMSetupFailed, "npm.setup_failed", 500, "Nginx Proxy Manager setup failed"},
		{NPMReserved, "npm.reserved", 409, "Reserved by an exposure"},
		{NPMUnsupportedTarget, "npm.unsupported_target", 422, "Unsupported proxy target"},
		{CloudflareReserved, "cloudflare.reserved", 409, "Reserved by an exposure"},
		{CloudflareUnsupportedTarget, "cloudflare.unsupported_target", 422, "Unsupported tunnel target"},
		{CloudflareConnectorMissing, "cloudflare.connector_missing", 409, "No connector for the tunnel on this host"},
		{DERPRegionTaken, "derp.region_taken", 409, "DERP region id taken"},
		{DERPPortInUse, "derp.port_in_use", 409, "Port in use"},
		{DERPVerifyFailed, "derp.verify_failed", 500, "DERP verification failed"},
		{DERPNotReady, "derp.not_ready", 409, "DERP server not ready"},
		{DERPNoFallback, "derp.no_fallback", 409, "No other reachable DERP region"},
		{PanelTOTPIncomplete, "panel.totp_incomplete", 409, "TOTP not enrolled for every user"},
		{PanelAlreadyExposed, "panel.already_exposed", 409, "Panel already exposed"},
		{PanelWrongHost, "panel.wrong_host", 422, "Not the controller's host"},
		{PanelNotExposed, "panel.not_exposed", 404, "Panel not exposed"},
		{Quarantined, "core.quarantined", 409, "Controller in restore quarantine"},
	}
	for _, tt := range tests {
		if string(tt.code) != tt.value || !Known(tt.code) || tt.code.Status() != tt.status || tt.code.Title() != tt.title {
			t.Errorf("code %q: known %v, status %d, title %q; want value %q registered with status %d and title %q",
				tt.code, Known(tt.code), tt.code.Status(), tt.code.Title(), tt.value, tt.status, tt.title)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mkdir -p /root/.cache/w0c-tmp/1 && TMPDIR=/root/.cache/w0c-tmp/1 GOTOOLCHAIN=local go test ./internal/core/errcode/ -run TestSection11Codes`
Expected: FAIL to compile with `undefined: FirewallManualRequired` (and the other new names).

- [ ] **Step 3: Write the implementation**

In `internal/core/errcode/errcode.go`, after the block that ends with `UpdateInterrupted      Code = "update.interrupted"` and its closing `)`, add:

```go
// Problem codes of the v1 modules and of restore quarantine (spec section
// 11): they answer a request before a change exists.
const (
	FirewallManualRequired           Code = "firewall.manual_required"
	FirewallUnsupportedIntegration   Code = "firewall.unsupported_integration"
	FirewallUnsupportedConfiguration Code = "firewall.unsupported_configuration"
	FirewallDockerUserInUse          Code = "firewall.docker_user_in_use"
	DockerEngineUnavailable          Code = "docker.engine_unavailable"
	DockerInUse                      Code = "docker.in_use"
	DockerOwned                      Code = "docker.owned"
	DockerComposeUnavailable         Code = "docker.compose_unavailable"
	TailscaleUnavailable             Code = "tailscale.unavailable"
	TailscaleRepoConflict            Code = "tailscale.repo_conflict"
	TailscalePackagePresent          Code = "tailscale.package_present"
	TailscalePackageHeld             Code = "tailscale.package_held"
	TailnetPolicyConflict            Code = "tailnet.policy_conflict"
	TailnetPolicyInvalid             Code = "tailnet.policy_invalid"
	CloudNotConnected                Code = "cloud.not_connected"
	CloudAuthFailed                  Code = "cloud.auth_failed"
	CloudPermissionMissing           Code = "cloud.permission_missing"
	CloudRateLimited                 Code = "cloud.rate_limited"
	CloudUnavailable                 Code = "cloud.unavailable"
	CloudConflict                    Code = "cloud.conflict"
	CloudCleanupPending              Code = "cloud.cleanup_pending"
	NPMUnhealthy                     Code = "npm.unhealthy"
	NPMNotPublic                     Code = "npm.not_public"
	NPMReserved                      Code = "npm.reserved"
	NPMUnsupportedTarget             Code = "npm.unsupported_target"
	CloudflareReserved               Code = "cloudflare.reserved"
	CloudflareUnsupportedTarget      Code = "cloudflare.unsupported_target"
	CloudflareConnectorMissing       Code = "cloudflare.connector_missing"
	DERPRegionTaken                  Code = "derp.region_taken"
	DERPPortInUse                    Code = "derp.port_in_use"
	DERPNotReady                     Code = "derp.not_ready"
	DERPNoFallback                   Code = "derp.no_fallback"
	PanelTOTPIncomplete              Code = "panel.totp_incomplete"
	PanelAlreadyExposed              Code = "panel.already_exposed"
	PanelWrongHost                   Code = "panel.wrong_host"
	PanelNotExposed                  Code = "panel.not_exposed"
	Quarantined                      Code = "core.quarantined"
)

// Action-level codes of the v1 modules: they appear in a change's error,
// and use their status only when an API call returns them synchronously.
const (
	FirewallPredictionMismatch Code = "firewall.prediction_mismatch"
	TailscalePinFailed         Code = "tailscale.pin_failed"
	CloudUncertainOutcome      Code = "cloud.uncertain_outcome"
	NPMSetupFailed             Code = "npm.setup_failed"
	DERPVerifyFailed           Code = "derp.verify_failed"
)
```

In the same file, inside `var registry = map[Code]info{`, after the line `UpdateInterrupted:        {http.StatusInternalServerError, "Update interrupted"},` add:

```go

	FirewallManualRequired:           {http.StatusConflict, "Manual action required"},
	FirewallUnsupportedIntegration:   {http.StatusConflict, "Docker integration unsupported"},
	FirewallUnsupportedConfiguration: {http.StatusConflict, "Firewall configuration unsupported"},
	FirewallPredictionMismatch:       {http.StatusInternalServerError, "Firewall state differs from the prediction"},
	FirewallDockerUserInUse:          {http.StatusConflict, "DOCKER-USER holds foreign rules"},
	DockerEngineUnavailable:          {http.StatusServiceUnavailable, "Docker Engine unavailable"},
	DockerInUse:                      {http.StatusConflict, "In use"},
	DockerOwned:                      {http.StatusConflict, "Owned by a Darkpanel module"},
	DockerComposeUnavailable:         {http.StatusConflict, "Compose unavailable or project refused"},
	TailscaleUnavailable:             {http.StatusServiceUnavailable, "Tailscale unavailable"},
	TailscaleRepoConflict:            {http.StatusConflict, "Conflicting Tailscale repository"},
	TailscalePackagePresent:          {http.StatusConflict, "Tailscale already installed"},
	TailscalePackageHeld:             {http.StatusConflict, "Tailscale package held"},
	TailscalePinFailed:               {http.StatusInternalServerError, "Tailscale pin failed"},
	TailnetPolicyConflict:            {http.StatusConflict, "Policy changed since the plan"},
	TailnetPolicyInvalid:             {http.StatusUnprocessableEntity, "Policy invalid"},
	CloudNotConnected:                {http.StatusConflict, "Account not connected"},
	CloudAuthFailed:                  {http.StatusBadGateway, "Provider rejected the credentials"},
	CloudPermissionMissing:           {http.StatusBadGateway, "Provider permission missing"},
	CloudRateLimited:                 {http.StatusServiceUnavailable, "Provider rate limit"},
	CloudUnavailable:                 {http.StatusServiceUnavailable, "Provider unavailable"},
	CloudUncertainOutcome:            {http.StatusGatewayTimeout, "Provider outcome uncertain"},
	CloudConflict:                    {http.StatusConflict, "Provider state changed"},
	CloudCleanupPending:              {http.StatusConflict, "Cleanup pending on the account"},
	NPMUnhealthy:                     {http.StatusServiceUnavailable, "Nginx Proxy Manager unhealthy"},
	NPMNotPublic:                     {http.StatusConflict, "Host not publicly reachable"},
	NPMSetupFailed:                   {http.StatusInternalServerError, "Nginx Proxy Manager setup failed"},
	NPMReserved:                      {http.StatusConflict, "Reserved by an exposure"},
	NPMUnsupportedTarget:             {http.StatusUnprocessableEntity, "Unsupported proxy target"},
	CloudflareReserved:               {http.StatusConflict, "Reserved by an exposure"},
	CloudflareUnsupportedTarget:      {http.StatusUnprocessableEntity, "Unsupported tunnel target"},
	CloudflareConnectorMissing:       {http.StatusConflict, "No connector for the tunnel on this host"},
	DERPRegionTaken:                  {http.StatusConflict, "DERP region id taken"},
	DERPPortInUse:                    {http.StatusConflict, "Port in use"},
	DERPVerifyFailed:                 {http.StatusInternalServerError, "DERP verification failed"},
	DERPNotReady:                     {http.StatusConflict, "DERP server not ready"},
	DERPNoFallback:                   {http.StatusConflict, "No other reachable DERP region"},
	PanelTOTPIncomplete:              {http.StatusConflict, "TOTP not enrolled for every user"},
	PanelAlreadyExposed:              {http.StatusConflict, "Panel already exposed"},
	PanelWrongHost:                   {http.StatusUnprocessableEntity, "Not the controller's host"},
	PanelNotExposed:                  {http.StatusNotFound, "Panel not exposed"},
	Quarantined:                      {http.StatusConflict, "Controller in restore quarantine"},
```

Run `gofmt -w internal/core/errcode/errcode.go internal/core/errcode/errcode_test.go` so the map and the test table align.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `TMPDIR=/root/.cache/w0c-tmp/1 GOTOOLCHAIN=local go test -count=1 ./internal/core/errcode/ && TMPDIR=/root/.cache/w0c-tmp/1 golangci-lint run --allow-serial-runners ./internal/core/errcode/...`
Expected: `ok  	github.com/darkraise/darkpanel/internal/core/errcode` and `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add internal/core/errcode/errcode.go internal/core/errcode/errcode_test.go
git commit -m "feat(errcode): add the v1 module and quarantine codes"
rm -rf /root/.cache/w0c-tmp/1
```

### Task 2: Core runtime listener types

This task declares the runtime-listener interface and types in `internal/core/listeners.go` exactly as spec 1.12 gives them, with their state and client-header constants (C3), and adds `Env.Listeners`. Nothing implements or sets them yet: Task 40 implements `core.Listeners` and Task 47 sets `Env.Listeners` on the controller engine. The one test, `TestListenerConstantValues`, pins the values, because `core_listeners` stores the states and NPM and Cloudflare set the headers; it uses no helper.

**Files:**
- Create: `internal/core/listeners.go`
- Create: `internal/core/listeners_test.go`
- Modify: `internal/core/contract.go:137` (the `Env` struct gains `Listeners` after `Units`)

**Interfaces:**
- Consumes: none
- Produces: C3 (`core.Listeners`, `core.ListenerSpec`, `core.ListenerInfo`, `core.ListenerOpen`, `core.ListenerRevoked`, `core.ListenerClosed`, `core.ClientHeaderNPM`, `core.ClientHeaderTunnel`, `core.Env.Listeners`)

**Items:** 51, 52

**Depends on:** none

**Implementer:** dforge:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 0 = 3

- [ ] **Step 1: Write the failing test**

Create `internal/core/listeners_test.go`:

```go
package core

import "testing"

// The listener states are stored in core_listeners and the client headers
// are what NPM's proxy host and Cloudflare set, so their values are fixed.
func TestListenerConstantValues(t *testing.T) {
	for got, want := range map[string]string{
		ListenerOpen:       "open",
		ListenerRevoked:    "revoked",
		ListenerClosed:     "closed",
		ClientHeaderNPM:    "X-Darkpanel-Client",
		ClientHeaderTunnel: "Cf-Connecting-IP",
	} {
		if got != want {
			t.Errorf("constant = %q, want %q", got, want)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mkdir -p /root/.cache/w0c-tmp/2 && TMPDIR=/root/.cache/w0c-tmp/2 GOTOOLCHAIN=local go test ./internal/core/ -run TestListenerConstantValues`
Expected: FAIL to compile with `undefined: ListenerOpen` (and the other constants).

- [ ] **Step 3: Write the implementation**

Create `internal/core/listeners.go`:

```go
package core

import (
	"context"
	"net/netip"
)

// The states of a runtime listener (core_listeners.state).
const (
	ListenerOpen    = "open"
	ListenerRevoked = "revoked"
	ListenerClosed  = "closed"
)

// The client headers a runtime listener may read (1.10).
const (
	ClientHeaderNPM    = "X-Darkpanel-Client"
	ClientHeaderTunnel = "Cf-Connecting-IP"
)

// Listeners opens and closes the controller's runtime listeners (1.10)
// without a restart. Env carries it for controller-side actions only.
type Listeners interface {
	// Open records a core_listeners row in state open, then binds it.
	Open(ctx context.Context, spec ListenerSpec) (id string, err error)
	// Revoke commits state revoked first, then stops accepting and closes
	// tracked connections and streams (1.10). Revoking a revoked listener succeeds.
	Revoke(ctx context.Context, id string) error
	// Close moves a revoked listener to closed.
	Close(ctx context.Context, id string) error
	Get(ctx context.Context, id string) (ListenerInfo, error)
}

// ListenerSpec is what a runtime listener serves and whom it trusts.
type ListenerSpec struct {
	Addr         netip.AddrPort
	AllowedPeer  netip.Addr
	ClientHeader string // "X-Darkpanel-Client" or "Cf-Connecting-IP"
	PublicHost   string // accepted by the CSRF origin check
	ChangeID     string
}

// ListenerInfo is one core_listeners row.
type ListenerInfo struct {
	ID    string
	Spec  ListenerSpec
	State string // open | revoked | closed
}
```

In `internal/core/contract.go`, in `type Env struct`, replace the line

```go
	Units     Units // the action's module's unit client; nil on the controller
```

with

```go
	Units     Units     // the action's module's unit client; nil on the controller
	Listeners Listeners // controller-side actions only; nil on the agent
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `TMPDIR=/root/.cache/w0c-tmp/2 GOTOOLCHAIN=local go build ./... && TMPDIR=/root/.cache/w0c-tmp/2 GOTOOLCHAIN=local go test -count=1 ./internal/core/ && TMPDIR=/root/.cache/w0c-tmp/2 golangci-lint run --allow-serial-runners ./internal/core/`
Expected: the build succeeds; `ok  	github.com/darkraise/darkpanel/internal/core`; `0 issues.`

- [ ] **Step 5: Commit**

```bash
gofmt -l internal/core   # prints nothing
git add internal/core/listeners.go internal/core/listeners_test.go internal/core/contract.go
git commit -m "feat(core): add the runtime listener types"
rm -rf /root/.cache/w0c-tmp/2
```

### Task 3: Proxied request context

This task adds `core.Proxied` and its context accessors to `internal/core/peer.go` (C4): what a request that came through a runtime listener carries, so the API (Task 31) and the dispatcher (Task 24) can read the client address, the proxy path and the revocation signal without importing `internal/controller/listen`. The runtime request wrapper (Task 30) sets it. `ProxyPath` already exists in `internal/core/guard.go:36`. The one test, `TestProxiedContext`, round-trips a value and checks that the proxied key and the client-peer key never answer for each other; it uses no helper.

**Files:**
- Modify: `internal/core/peer.go` (imports; `Proxied`, `proxiedKey`, `WithProxied`, `ProxiedFrom` appended)
- Test: `internal/core/peer_test.go` (append `TestProxiedContext`)

**Interfaces:**
- Consumes: none
- Produces: C4 (`core.Proxied`, `core.WithProxied`, `core.ProxiedFrom`)

**Items:** 51

**Depends on:** none

**Implementer:** dforge:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 0 = 3

- [ ] **Step 1: Write the failing test**

Append to `internal/core/peer_test.go` (its imports `context`, `net/netip` and `testing` already cover it):

```go
func TestProxiedContext(t *testing.T) {
	revoked := make(chan struct{})
	p := Proxied{
		ListenerID: "01JLISTENER", Client: netip.MustParseAddr("203.0.113.9"),
		Transport: netip.MustParseAddrPort("172.31.255.2:40112"), PublicHost: "panel.example.com",
		Path: ProxyPath{ExposureID: "01JCHANGE", Kind: "npm", ProxyAddr: netip.MustParseAddr("172.31.255.2"),
			ListenerAddr: netip.MustParseAddrPort("172.31.255.1:9443")},
		Revoked: revoked,
	}
	got, ok := ProxiedFrom(WithProxied(context.Background(), p))
	if !ok || got.ListenerID != p.ListenerID || got.Client != p.Client || got.Transport != p.Transport ||
		got.PublicHost != p.PublicHost || got.Path.ExposureID != p.Path.ExposureID || got.Revoked != p.Revoked {
		t.Errorf("ProxiedFrom = %+v, %v; want %+v", got, ok, p)
	}
	if got, ok := ProxiedFrom(context.Background()); ok {
		t.Errorf("ProxiedFrom of a bare context = %+v, want none", got)
	}
	// A proxied request still has its own client peer, which the API sets
	// separately; one key never answers for the other.
	if _, ok := ClientPeer(WithProxied(context.Background(), p)); ok {
		t.Error("ClientPeer found a peer in a context that carries only Proxied")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mkdir -p /root/.cache/w0c-tmp/3 && TMPDIR=/root/.cache/w0c-tmp/3 GOTOOLCHAIN=local go test ./internal/core/ -run TestProxiedContext`
Expected: FAIL to compile with `undefined: Proxied`, `undefined: ProxiedFrom` and `undefined: WithProxied`.

- [ ] **Step 3: Write the implementation**

In `internal/core/peer.go`, replace `import "context"` with

```go
import (
	"context"
	"net/netip"
)
```

and append at the end of the file:

```go
// Proxied describes a request that reached the controller through a
// runtime listener (1.10). Path.ExposureID is the listener's
// ListenerSpec.ChangeID, Path.Kind is "npm" for ClientHeaderNPM and "tunnel"
// for ClientHeaderTunnel, Path.ProxyAddr is the allowed peer and
// Path.ListenerAddr the listener's address; Path.Public is left empty.
type Proxied struct {
	ListenerID string
	Client     netip.Addr     // the address the listener's client header named, unmapped
	Transport  netip.AddrPort // the proxy's TCP peer
	PublicHost string         // the exposure's public host name
	Path       ProxyPath
	Revoked    <-chan struct{} // closed once the listener is revoked
}

type proxiedKey struct{}

// WithProxied returns ctx carrying p.
func WithProxied(ctx context.Context, p Proxied) context.Context {
	return context.WithValue(ctx, proxiedKey{}, p)
}

// ProxiedFrom returns the Proxied WithProxied stored in ctx.
func ProxiedFrom(ctx context.Context) (Proxied, bool) {
	p, ok := ctx.Value(proxiedKey{}).(Proxied)
	return p, ok
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `TMPDIR=/root/.cache/w0c-tmp/3 GOTOOLCHAIN=local go test -count=1 ./internal/core/ && TMPDIR=/root/.cache/w0c-tmp/3 golangci-lint run --allow-serial-runners ./internal/core/`
Expected: `ok  	github.com/darkraise/darkpanel/internal/core`; `0 issues.`

- [ ] **Step 5: Commit**

```bash
gofmt -l internal/core   # prints nothing
git add internal/core/peer.go internal/core/peer_test.go
git commit -m "feat(core): carry proxied requests in the context"
rm -rf /root/.cache/w0c-tmp/3
```

### Task 4: Cloud provider test scaffolding

Build `internal/controller/cloud/cloudfake` (C5): the shared in-process provider stand-in that every cloud module's fake (built by the module waves) registers its handlers on. It records every request, including faulted ones, and injects three faults: a status answer (optionally with `Retry-After`) that skips the handler, and a lost response that runs the handler (so the write commits) and then drops the connection, which the client sees as a transport failure after the provider acted. Test helpers carry the prefix `cf`.

**Files:**
- Create: `internal/controller/cloud/cloudfake/fake.go`
- Test: `internal/controller/cloud/cloudfake/fake_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: C5 (`cloudfake.New`, `Server.URL`, `Client`, `Handle`, `Fail`, `Requests`, `Fault`, `Request`), used by Task 23's tests and by every cloud module's fake.

**Items:** 50

**Depends on:** none

**Implementer:** dforge:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 0 = 3

- [ ] **Step 1: Write the failing test**

Create `internal/controller/cloud/cloudfake/fake_test.go`:

```go
package cloudfake

import (
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func cfDo(t *testing.T, s *Server, method, path, body string) (*http.Response, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, s.URL()+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return s.Client().Do(req)
}

func cfBody(t *testing.T, res *http.Response) string {
	t.Helper()
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestHandleAndRecord(t *testing.T) {
	s := New(t)
	s.Handle(http.MethodPost, "/v2/keys", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write(b)
	})
	res, err := cfDo(t, s, http.MethodPost, "/v2/keys?x=1", `{"a":1}`)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusCreated || cfBody(t, res) != `{"a":1}` {
		t.Fatalf("answer = %d", res.StatusCode)
	}
	reqs := s.Requests()
	if len(reqs) != 1 {
		t.Fatalf("recorded %d requests, want 1", len(reqs))
	}
	got := reqs[0]
	if got.Method != http.MethodPost || got.Path != "/v2/keys" || got.RawQuery != "x=1" || string(got.Body) != `{"a":1}` {
		t.Fatalf("recorded %+v", got)
	}
}

func TestFaultStatusSkipsTheHandler(t *testing.T) {
	s := New(t)
	var calls atomic.Int32
	s.Handle(http.MethodGet, "/items", func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	})
	s.Fail(http.MethodGet, "/items", Fault{Status: http.StatusTooManyRequests, RetryAfter: "7", Body: "slow down", Times: 2})
	for i := range 2 {
		res, err := cfDo(t, s, http.MethodGet, "/items", "")
		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != http.StatusTooManyRequests || res.Header.Get("Retry-After") != "7" || cfBody(t, res) != "slow down" {
			t.Fatalf("attempt %d: answer %d", i, res.StatusCode)
		}
	}
	res, err := cfDo(t, s, http.MethodGet, "/items", "")
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("third answer = %d, want 200 once the faults are used", res.StatusCode)
	}
	cfBody(t, res)
	if calls.Load() != 1 {
		t.Fatalf("handler ran %d times, want 1", calls.Load())
	}
	if n := len(s.Requests()); n != 3 {
		t.Fatalf("recorded %d requests, want 3 including the faulted ones", n)
	}
}

func TestFaultTimesZeroMeansOnce(t *testing.T) {
	s := New(t)
	s.Handle(http.MethodDelete, "/items/1", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	s.Fail(http.MethodDelete, "/items/1", Fault{Status: http.StatusBadGateway})
	want := []int{http.StatusBadGateway, http.StatusNoContent}
	for i, code := range want {
		res, err := cfDo(t, s, http.MethodDelete, "/items/1", "")
		if err != nil {
			t.Fatal(err)
		}
		cfBody(t, res)
		if res.StatusCode != code {
			t.Fatalf("answer %d = %d, want %d", i, res.StatusCode, code)
		}
	}
}

func TestFaultMatchesMethodAndPath(t *testing.T) {
	s := New(t)
	s.Handle(http.MethodGet, "/a", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	s.Handle(http.MethodPost, "/a", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	s.Fail(http.MethodPost, "/a", Fault{Status: http.StatusInternalServerError})
	res, err := cfDo(t, s, http.MethodGet, "/a", "")
	if err != nil {
		t.Fatal(err)
	}
	cfBody(t, res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET answer = %d, want 200: the fault is for POST", res.StatusCode)
	}
}

func TestLoseResponseCommitsAndDropsTheAnswer(t *testing.T) {
	s := New(t)
	var committed atomic.Int32
	s.Handle(http.MethodPost, "/tunnels", func(w http.ResponseWriter, _ *http.Request) {
		committed.Add(1)
		w.WriteHeader(http.StatusOK)
	})
	s.Fail(http.MethodPost, "/tunnels", Fault{LoseResponse: true})
	res, err := cfDo(t, s, http.MethodPost, "/tunnels", "{}")
	if err == nil {
		cfBody(t, res)
		t.Fatalf("request answered %d, want a transport failure", res.StatusCode)
	}
	if committed.Load() != 1 {
		t.Fatalf("handler ran %d times, want 1: the write must commit", committed.Load())
	}
}

func TestRequestsReturnsCopies(t *testing.T) {
	s := New(t)
	s.Handle(http.MethodPut, "/c", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	res, err := cfDo(t, s, http.MethodPut, "/c", "abc")
	if err != nil {
		t.Fatal(err)
	}
	cfBody(t, res)
	first := s.Requests()
	first[0].Body[0] = 'X'
	first[0].Header.Set("X-Test", "changed")
	again := s.Requests()
	if string(again[0].Body) != "abc" || again[0].Header.Get("X-Test") != "" {
		t.Fatal("Requests returned the recorded request itself, not a copy")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mkdir -p /root/.cache/w0c-tmp/4 && export TMPDIR=/root/.cache/w0c-tmp/4 GOTOOLCHAIN=local && go test -count=1 ./internal/controller/cloud/cloudfake/`
Expected: FAIL to build: `undefined: New` (and `undefined: Fault`, `Server`).

- [ ] **Step 3: Write the implementation**

Create `internal/controller/cloud/cloudfake/fake.go`:

```go
// Package cloudfake is the shared scaffolding of the in-process provider
// fakes (spec 1.9): an HTTP server that records every request and injects
// faults, on which each module's fake registers its handlers.
package cloudfake

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
)

// Server is an in-process provider stand-in: a plain-HTTP httptest server
// that records every request and injects faults.
type Server struct {
	srv *httptest.Server
	mux *http.ServeMux

	mu       sync.Mutex
	requests []Request
	faults   map[string][]Fault // by "<METHOD> <path>", oldest first
}

// Fault is one injected failure. Status answers with that status (and
// RetryAfter as the Retry-After header when set) without running the
// handler. LoseResponse runs the handler, so the write commits, and then
// closes the connection without sending its answer.
type Fault struct {
	Status       int
	RetryAfter   string
	Body         string
	LoseResponse bool
	Times        int
}

// Request is one recorded request.
type Request struct {
	Method, Path, RawQuery string
	Header                 http.Header
	Body                   []byte
}

// New starts a server closed by tb.Cleanup.
func New(tb testing.TB) *Server {
	tb.Helper()
	s := &Server{mux: http.NewServeMux(), faults: map[string][]Fault{}}
	s.srv = httptest.NewServer(http.HandlerFunc(s.serve))
	tb.Cleanup(s.srv.Close)
	return s
}

// URL is the server's base URL, without a trailing slash.
func (s *Server) URL() string { return s.srv.URL }

// Client is an HTTP client for the server.
func (s *Server) Client() *http.Client { return s.srv.Client() }

// Handle routes method and path (a net/http ServeMux pattern without the
// method) to h.
func (s *Server) Handle(method, path string, h http.HandlerFunc) {
	s.mux.HandleFunc(method+" "+path, h)
}

// Fail queues f for the next f.Times requests matching method and path
// (Times 0 means 1). A queued fault is used before the handler runs. path is
// compared with the request's URL path exactly.
func (s *Server) Fail(method, path string, f Fault) {
	n := max(f.Times, 1)
	s.mu.Lock()
	defer s.mu.Unlock()
	key := method + " " + path
	for range n {
		s.faults[key] = append(s.faults[key], f)
	}
}

// Requests returns the recorded requests in arrival order, faulted ones
// included.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Request, len(s.requests))
	for i, r := range s.requests {
		out[i] = Request{Method: r.Method, Path: r.Path, RawQuery: r.RawQuery, Header: r.Header.Clone(), Body: slices.Clone(r.Body)}
	}
	return out
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	f, faulted := s.record(r, body)
	switch {
	case faulted && f.LoseResponse:
		s.loseResponse(w, r)
	case faulted:
		if f.RetryAfter != "" {
			w.Header().Set("Retry-After", f.RetryAfter)
		}
		w.WriteHeader(f.Status)
		_, _ = io.WriteString(w, f.Body)
	default:
		s.mux.ServeHTTP(w, r)
	}
}

// record appends r to the log and takes the next fault queued for it.
func (s *Server) record(r *http.Request, body []byte) (Fault, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, Request{
		Method: r.Method, Path: r.URL.Path, RawQuery: r.URL.RawQuery, Header: r.Header.Clone(), Body: body,
	})
	key := r.Method + " " + r.URL.Path
	queue := s.faults[key]
	if len(queue) == 0 {
		return Fault{}, false
	}
	s.faults[key] = queue[1:]
	return queue[0], true
}

// loseResponse runs the handler into a recorder, so whatever it commits is
// committed, and then drops the connection: the client sees a transport
// failure after the provider acted.
func (s *Server) loseResponse(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(httptest.NewRecorder(), r)
	hj, ok := w.(http.Hijacker)
	if !ok {
		panic("cloudfake: the response writer cannot be hijacked")
	}
	conn, _, err := hj.Hijack()
	if err != nil {
		panic("cloudfake: hijack: " + err.Error())
	}
	_ = conn.Close()
}
```

`Fail` compares the request's URL path exactly (not a pattern), so a fault targets one path. `loseResponse` panics only on a test-setup impossibility (a writer that cannot be hijacked); `net/http` recovers a handler panic and drops the connection, which still reads as a lost answer.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `mkdir -p /root/.cache/w0c-tmp/4 && export TMPDIR=/root/.cache/w0c-tmp/4 GOTOOLCHAIN=local && go test -count=1 -race ./internal/controller/cloud/cloudfake/ && go vet ./internal/controller/cloud/cloudfake/ && golangci-lint run --allow-serial-runners ./internal/controller/cloud/cloudfake/`
Expected: `ok  	github.com/darkraise/darkpanel/internal/controller/cloud/cloudfake`, then `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add internal/controller/cloud/cloudfake/fake.go internal/controller/cloud/cloudfake/fake_test.go
git commit -m "test(cloud): add the provider fake scaffolding"
```

### Task 5: Composite confirm test waits for the runner

Register row 93: `TestCompositeConfirmAnswerLost/the_runner_settles_it` (`internal/core/lifecycle/composite_confirm_test.go:104`) asserts that step `g` is `confirming` right after `Confirm` returns, but the composite's background runner may already have settled it to `confirmed`; it failed that way once in CI (run 39906809219). The fix changes only the test: right after `Confirm` it accepts either state on the way to `confirmed`, and once the composite is completed it checks the runner's outcome (step `g` confirmed, its child confirmed). No production code changes and no helper is added.

**Files:**
- Test: `internal/core/lifecycle/composite_confirm_test.go:104-109`

**Interfaces:**
- Consumes: none
- Produces: none

**Items:** 93

**Depends on:** none

**Implementer:** dforge:impl-sonnet-low
**Evaluation:** files 0 - spec 0 - coupling 0 - risk 0 = 0

- [ ] **Step 1: Confirm the race in the current assertion**

Read `internal/core/lifecycle/composite_confirm_test.go:92-110`. The subtest makes `Confirm`'s answer and the following lookup both get lost, so `Confirm` returns `awaiting_confirmation` and leaves step `g` `confirming`; the runner started by the apply then looks the child up on its own and moves the step to `confirmed`. Nothing orders that move after the test's `stepsOf` read, so the assertion `s.State != core.StepConfirming` is a race, not a property of the code. (No new failing test is written: the defect is in the test, and it fails only under scheduling the test cannot force.)

- [ ] **Step 2: Replace the intermediate assertion**

In `internal/core/lifecycle/composite_confirm_test.go`, inside `t.Run("the runner settles it", ...)`, replace

```go
		if s := f.stepsOf(v.ID)["g"]; s.State != core.StepConfirming {
			t.Fatalf("step g = %s, want confirming", s.State)
		}
		if done := f.await(v.ID, core.StateCompleted, core.StateReverted); done.State != core.StateCompleted {
			t.Fatalf("composite = %s, want completed", done.State)
		}
```

with

```go
		// The background runner may settle the step between Confirm's answer
		// and this read, so only a state on the way to confirmed is checked
		// here; the runner's outcome is checked once the composite is done.
		if s := f.stepsOf(v.ID)["g"]; s.State != core.StepConfirming && s.State != core.StepConfirmed {
			t.Fatalf("step g = %s, want confirming or confirmed", s.State)
		}
		if done := f.await(v.ID, core.StateCompleted, core.StateReverted); done.State != core.StateCompleted {
			t.Fatalf("composite = %s, want completed", done.State)
		}
		g := f.stepsOf(v.ID)["g"]
		if g.State != core.StepConfirmed {
			t.Errorf("step g = %s, want confirmed", g.State)
		}
		if child := f.agentChange(g.ChildID); child.State != core.StateConfirmed {
			t.Errorf("child of g = %s, want confirmed", child.State)
		}
```

- [ ] **Step 3: Run the test repeatedly to verify it holds**

Run: `mkdir -p /root/.cache/w0c-tmp/5 && TMPDIR=/root/.cache/w0c-tmp/5 GOTOOLCHAIN=local go test -count=50 -run 'TestCompositeConfirmAnswerLost' ./internal/core/lifecycle/`
Expected: `ok  	github.com/darkraise/darkpanel/internal/core/lifecycle` (about a minute; 200 repeats took 240 s on 2026-10-23 and all passed).

Then: `TMPDIR=/root/.cache/w0c-tmp/5 GOTOOLCHAIN=local go test -count=1 -race -run 'TestComposite' ./internal/core/lifecycle/`
Expected: `ok  	github.com/darkraise/darkpanel/internal/core/lifecycle`

- [ ] **Step 4: Lint**

Run: `TMPDIR=/root/.cache/w0c-tmp/5 golangci-lint run --allow-serial-runners ./internal/core/lifecycle/...`
Expected: `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add internal/core/lifecycle/composite_confirm_test.go
git commit -m "test(lifecycle): let the runner settle a lost confirm"
rm -rf /root/.cache/w0c-tmp/5
```

### Task 6: End frame when a stream's re-authentication fails

A relayed module stream (`GET /api/v1/hosts/{host}/streams/{module}/{name}`) must end with exactly one `end` frame (W4). When the 30-second re-authentication fails for a reason other than the credentials (a database error, so `sseRecheck` answers `"error"`), `handleStream` today returns without one, because it writes its own end frame only for `expired`, `revoked` and the `error` of `CloseStreams`. The fix tracks what actually matters — whether the connection can still take a frame — instead of listing reasons: `relayWriter` records a failed write, and `handleStream` writes the end frame for every end except a relay that already wrote one, a broken write, a closed source (`done`) and a departed client. This also covers `CloseStreams`, so the helper `streamsClosed` goes. The test reuses the package's `sseAuth` fake and the `relayGet`/`relayFrames` helpers; it declares no new helper.

**Files:**
- Modify: `internal/controller/api/streams.go:51-89` (the end of `handleStream`, `streamsClosed`, `relayWriter`)
- Test: `internal/controller/api/streams_test.go` (one new test and two imports)

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: nothing other tasks consume. Task 31 later adds a `revoked` end inside `sseStream`; it relies on `handleStream` not writing a second end frame when `relayWriter.ended` is set, which stays true here.

**Items:** 92

**Depends on:** none

**Implementer:** dforge:impl-sonnet-low
**Evaluation:** files 1 - spec 0 - coupling 0 - risk 1 = 2

- [ ] **Step 1: Write the failing test**

In `internal/controller/api/streams_test.go`, add `"errors"` after `"encoding/json"` and `"github.com/darkraise/darkpanel/internal/controller/auth"` before `"github.com/darkraise/darkpanel/internal/core"` in the import block, and append:

```go
// TestRelayEndsWhenRecheckFails: a re-authentication that fails for a
// reason other than the credentials, such as a database error, ends a
// relayed stream with one error end frame.
func TestRelayEndsWhenRecheckFails(t *testing.T) {
	h := newHarness(t)
	pr, pw := io.Pipe()
	t.Cleanup(func() { _ = pw.Close() })
	body := &relayBody{Reader: pr}
	opened := make(chan struct{}, 1)
	h.disp.OpenStreamFunc = func(context.Context, core.Principal, string, string, string, url.Values) (io.ReadCloser, error) {
		opened <- struct{}{}
		return body, nil
	}
	a := &sseAuth{}
	a.set(sseIdentity(h.clock.Now().Add(time.Hour)), nil)
	cfg := h.cfg
	cfg.Auth, cfg.Clock = a, h.clock
	srv, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	resp := relayGet(t, ts, "/api/v1/hosts/h1/streams/sm/logs", "any")
	<-opened
	a.set(auth.Identity{}, errors.New("database is locked"))
	frames := make(chan []string, 1)
	go func() { frames <- relayFrames(resp.Body) }()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case got := <-frames:
			want := []string{`end {"reason":"error","error":{"code":"internal","detail":"Internal error"}}`}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("frames %q, want %q", got, want)
			}
			if !body.closed.Load() {
				t.Error("the dispatcher's reader was not closed")
			}
			return
		case <-deadline:
			t.Fatal("the stream did not end")
		case <-time.After(5 * time.Millisecond):
			h.clock.Advance(SSERecheck)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mkdir -p /root/.cache/w0c-tmp/6 && TMPDIR=/root/.cache/w0c-tmp/6 GOTOOLCHAIN=local go test -count=1 -run TestRelayEndsWhenRecheckFails ./internal/controller/api/`
Expected: FAIL with `frames [], want ["end {\"reason\":\"error\",\"error\":{\"code\":\"internal\",\"detail\":\"Internal error\"}}"]`

- [ ] **Step 3: Write the implementation**

In `internal/controller/api/streams.go`, replace everything from `rw := &relayWriter{ResponseWriter: w}` in `handleStream` to the end of `relayWriter.Write` (this removes `streamsClosed`) with:

```go
	rw := &relayWriter{ResponseWriter: w}
	reason := s.sseStream(rw, r, frames)
	// sseStream may pick an expiry, a revocation or CloseStreams over the
	// closed source right after it wrote the relay's end frame; the stream
	// has one. A stream whose writes failed or whose client left cannot
	// take one; every other end, a failed re-authentication included, gets
	// its end frame here.
	if rw.ended || rw.broken || reason == "done" || r.Context().Err() != nil {
		return
	}
	if b, err := sseEncode(endFrame(reason, nil)); err == nil {
		_ = sseWrite(w, b)
	}
}

// relayWriter records whether an end frame was written through it, and
// whether a write failed.
type relayWriter struct {
	http.ResponseWriter
	ended  bool
	broken bool
}

func (w *relayWriter) Write(b []byte) (int, error) {
	if bytes.HasPrefix(b, []byte("event: end\n")) {
		w.ended = true
	}
	n, err := w.ResponseWriter.Write(b)
	if err != nil {
		w.broken = true
	}
	return n, err
}
```

Keep `relayWriter.Unwrap` as it is.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `TMPDIR=/root/.cache/w0c-tmp/6 GOTOOLCHAIN=local go test -count=1 ./internal/controller/api/ && TMPDIR=/root/.cache/w0c-tmp/6 golangci-lint run --allow-serial-runners ./internal/controller/api/`
Expected: `ok  	github.com/darkraise/darkpanel/internal/controller/api` (every relay test, `TestRelayEndsOnShutdown` included, still passes) and `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add internal/controller/api/streams.go internal/controller/api/streams_test.go
git commit -m "fix(api): end a relay whose recheck fails"
rm -rf /root/.cache/w0c-tmp/6
```

### Task 7: CLI module command map and stubs

This task creates the frozen file `internal/cli/modules.go` of spec 1.12 with the two hook variables `moduleCommands` and `controllerCommands`, and one stub per module plus the controller's backup stub, each returning no command. Mounting the commands under their groups is Task 20; this task only fixes the names later waves fill. Test helpers carry the prefix `mh` (none are needed here; Task 20 adds `mhSwap` and `mhCommand` to the same test file).

**Files:**
- Create: `internal/cli/modules.go`
- Create: `internal/cli/firewall.go`, `internal/cli/docker.go`, `internal/cli/tailscale.go`, `internal/cli/tailnet.go`, `internal/cli/derp.go`, `internal/cli/npm.go`, `internal/cli/cloudflare.go`, `internal/cli/panel.go`
- Create: `internal/cli/controller_backup.go`
- Test: `internal/cli/modules_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: the spec 1.12 CLI hook names `moduleCommands`, `controllerCommands`, `firewallCommands`, `dockerCommands`, `tailscaleCommands`, `tailnetCommands`, `derpCommands`, `npmCommands`, `cloudflareCommands`, `panelCommands` and `backupCommands` (each `func(a *app) []*cobra.Command`), which Task 20 mounts and later waves fill (Global Constraints, "Names copied from the spec").

**Items:** 52

**Depends on:** none

**Implementer:** dforge:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 0 = 3

- [ ] **Step 1: Write the failing test**

Create `internal/cli/modules_test.go`:

```go
package cli

import (
	"maps"
	"slices"
	"testing"
)

func TestModuleCommandsCoverEveryModule(t *testing.T) {
	want := []string{"cloudflare", "derp", "docker", "firewall", "npm", "panel", "tailnet", "tailscale"}
	if got := slices.Sorted(maps.Keys(moduleCommands)); !slices.Equal(got, want) {
		t.Fatalf("moduleCommands has %v, want %v", got, want)
	}
	for _, id := range want {
		if cmds := moduleCommands[id](&app{}); len(cmds) != 0 {
			t.Errorf("the %s stub returns %d commands, want none", id, len(cmds))
		}
	}
	if len(controllerCommands) != 1 {
		t.Fatalf("controllerCommands has %d entries, want 1 (backupCommands)", len(controllerCommands))
	}
	if cmds := controllerCommands[0](&app{}); len(cmds) != 0 {
		t.Errorf("the backup stub returns %d commands, want none", len(cmds))
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mkdir -p /root/.cache/w0c-tmp/7 && TMPDIR=/root/.cache/w0c-tmp/7 GOTOOLCHAIN=local go test -count=1 -run TestModuleCommandsCoverEveryModule ./internal/cli/`
Expected: FAIL to compile with `undefined: moduleCommands` and `undefined: controllerCommands`.

- [ ] **Step 3: Write the implementation**

Create `internal/cli/modules.go`:

```go
package cli

import "github.com/spf13/cobra"

// moduleCommands are the hand-written commands of each module; each
// function lives in internal/cli/<module>.go.
var moduleCommands = map[string]func(a *app) []*cobra.Command{
	"firewall": firewallCommands, "docker": dockerCommands, "tailscale": tailscaleCommands,
	"tailnet": tailnetCommands, "derp": derpCommands, "npm": npmCommands,
	"cloudflare": cloudflareCommands, "panel": panelCommands,
}

// controllerCommands are added under "darkpanel controller".
var controllerCommands = []func(a *app) []*cobra.Command{backupCommands}
```

Create `internal/cli/firewall.go`:

```go
package cli

import "github.com/spf13/cobra"

// firewallCommands are the hand-written commands of the firewall module,
// added under "darkpanel firewall"; it has none yet.
func firewallCommands(_ *app) []*cobra.Command { return nil }
```

Create `internal/cli/docker.go`:

```go
package cli

import "github.com/spf13/cobra"

// dockerCommands are the hand-written commands of the Docker module, added
// under "darkpanel docker"; it has none yet.
func dockerCommands(_ *app) []*cobra.Command { return nil }
```

Create `internal/cli/tailscale.go`:

```go
package cli

import "github.com/spf13/cobra"

// tailscaleCommands are the hand-written commands of the Tailscale node
// module, added under "darkpanel tailscale"; it has none yet.
func tailscaleCommands(_ *app) []*cobra.Command { return nil }
```

Create `internal/cli/tailnet.go`:

```go
package cli

import "github.com/spf13/cobra"

// tailnetCommands are the hand-written commands of the tailnet module,
// added under "darkpanel tailnet"; it has none yet.
func tailnetCommands(_ *app) []*cobra.Command { return nil }
```

Create `internal/cli/derp.go`:

```go
package cli

import "github.com/spf13/cobra"

// derpCommands are the hand-written commands of the DERP module, added
// under "darkpanel derp"; it has none yet.
func derpCommands(_ *app) []*cobra.Command { return nil }
```

Create `internal/cli/npm.go`:

```go
package cli

import "github.com/spf13/cobra"

// npmCommands are the hand-written commands of the Nginx Proxy Manager
// module, added under "darkpanel npm"; it has none yet.
func npmCommands(_ *app) []*cobra.Command { return nil }
```

Create `internal/cli/cloudflare.go`:

```go
package cli

import "github.com/spf13/cobra"

// cloudflareCommands are the hand-written commands of the Cloudflare Tunnel
// module, added under "darkpanel cloudflare"; it has none yet.
func cloudflareCommands(_ *app) []*cobra.Command { return nil }
```

Create `internal/cli/panel.go`:

```go
package cli

import "github.com/spf13/cobra"

// panelCommands are the hand-written commands of the panel exposure module,
// added under "darkpanel panel"; it has none yet.
func panelCommands(_ *app) []*cobra.Command { return nil }
```

Create `internal/cli/controller_backup.go`:

```go
package cli

import "github.com/spf13/cobra"

// backupCommands are the controller's backup, restore and quarantine
// commands, added under "darkpanel controller"; there are none yet.
func backupCommands(_ *app) []*cobra.Command { return nil }
```

The stubs name their parameter `_` because revive's `unused-parameter` rule flags an unused named one; the map's function type keeps the name `a` as spec 1.12 writes it.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `TMPDIR=/root/.cache/w0c-tmp/7 GOTOOLCHAIN=local go test -count=1 ./internal/cli/ && TMPDIR=/root/.cache/w0c-tmp/7 golangci-lint run --allow-serial-runners ./internal/cli/...`
Expected: `ok  	github.com/darkraise/darkpanel/internal/cli` and `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add internal/cli/modules.go internal/cli/modules_test.go internal/cli/firewall.go internal/cli/docker.go \
  internal/cli/tailscale.go internal/cli/tailnet.go internal/cli/derp.go internal/cli/npm.go \
  internal/cli/cloudflare.go internal/cli/panel.go internal/cli/controller_backup.go
git commit -m "feat(cli): add module command hooks and stubs"
rm -rf /root/.cache/w0c-tmp/7
```

### Task 8: Module skeletons

This task creates the eight module packages of spec 1.12 with empty manifests and adds their lines to `internal/modules/modules.go`. Each module is enabled by default and declares no action, resource, preflight check, owned path or migration, so registering it changes no schema snapshot, no generated CLI command and no web page: the web registry lists modules by its own entries (Task 21), and `enabledModules` ignores a host module the registry lacks. Each host's module list grows by eight default-enabled, available rows; the full Go suite passes with that change (run in a scratch worktree on 2026-10-23). The `github.com/tailscale/hujson` import is Task 29's, because it edits `go.mod`. No test helper is added.

**Files:**
- Create: `internal/modules/firewall/firewall.go`
- Create: `internal/modules/docker/docker.go`
- Create: `internal/modules/tailscale/tailscale.go`
- Create: `internal/modules/tailnet/tailnet.go`
- Create: `internal/modules/derp/derp.go`
- Create: `internal/modules/npm/npm.go`
- Create: `internal/modules/cloudflare/cloudflare.go`
- Create: `internal/modules/panel/panel.go`
- Modify: `internal/modules/modules.go` (whole file)
- Test: `internal/modules/modules_test.go` (append one test)

**Interfaces:**
- Consumes: nothing.
- Produces: C7 without its hujson import: `func Module() core.Module` and `const ID` in each of the eight packages, and `modules.All()` listing hostinfo, firewall, docker, tailscale, tailnet, derp, npm, cloudflare, panel in that order.

**Items:** 52

**Depends on:** none

**Implementer:** dforge:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5

- [ ] **Step 1: Write the failing test**

Append to `internal/modules/modules_test.go` (its imports already include `slices`, `testing`, `core` and `modules`):

```go
// TestAllListsTheModuleSkeletons shows the order of All and that each
// module of the v1 programme is registered, enabled by default and
// declares nothing yet.
func TestAllListsTheModuleSkeletons(t *testing.T) {
	want := []string{"hostinfo", "firewall", "docker", "tailscale", "tailnet", "derp", "npm", "cloudflare", "panel"}
	var ids []string
	for _, m := range modules.All() {
		ids = append(ids, m.Manifest().ID)
	}
	if !slices.Equal(ids, want) {
		t.Fatalf("modules.All() = %v, want %v", ids, want)
	}
	for _, m := range modules.All()[1:] {
		man := m.Manifest()
		if man.Version != 1 || !man.DefaultEnabled || len(man.Actions) != 0 || len(man.Resources) != 0 ||
			len(man.Preflight) != 0 || len(man.OwnedPaths) != 0 || len(man.Migrations) != 0 || man.Settings != nil {
			t.Errorf("module %s: manifest %+v, want version 1, enabled by default and nothing declared", man.ID, man)
		}
	}
	if _, err := core.NewRegistry(modules.All()...); err != nil {
		t.Errorf("core.NewRegistry(modules.All()...): %v", err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mkdir -p /root/.cache/w0c-tmp/8 && TMPDIR=/root/.cache/w0c-tmp/8 GOTOOLCHAIN=local go test -count=1 -run TestAllListsTheModuleSkeletons ./internal/modules/`
Expected: FAIL with `modules.All() = [hostinfo], want [hostinfo firewall docker tailscale tailnet derp npm cloudflare panel]`.

- [ ] **Step 3: Write the implementation**

Create `internal/modules/firewall/firewall.go`:

```go
// Package firewall is the UFW firewall module (spec section 2): rules, safe
// apply, snapshots, the Docker integration and the exposure view.
package firewall

import "github.com/darkraise/darkpanel/internal/core"

// ID is the module id.
const ID = "firewall"

type module struct{}

// Module returns the firewall module.
func Module() core.Module { return module{} }

// Manifest declares the module. Every module is enabled by default;
// preflight decides whether it is available on a host (R9). The manifest
// declares no action, resource or check yet.
func (module) Manifest() core.Manifest {
	return core.Manifest{ID: ID, Version: 1, DefaultEnabled: true}
}
```

Create `internal/modules/docker/docker.go`:

```go
// Package docker is the Docker Engine module (spec section 3): containers,
// images, networks, volumes and Compose projects.
package docker

import "github.com/darkraise/darkpanel/internal/core"

// ID is the module id.
const ID = "docker"

type module struct{}

// Module returns the Docker module.
func Module() core.Module { return module{} }

// Manifest declares the module. Every module is enabled by default;
// preflight decides whether it is available on a host (R9). The manifest
// declares no action, resource or check yet.
func (module) Manifest() core.Manifest {
	return core.Manifest{ID: ID, Version: 1, DefaultEnabled: true}
}
```

Create `internal/modules/tailscale/tailscale.go`:

```go
// Package tailscale is the Tailscale node module (spec 4.1): the node's
// status, preferences and tailscaled package.
package tailscale

import "github.com/darkraise/darkpanel/internal/core"

// ID is the module id.
const ID = "tailscale"

type module struct{}

// Module returns the Tailscale node module.
func Module() core.Module { return module{} }

// Manifest declares the module. Every module is enabled by default;
// preflight decides whether it is available on a host (R9). The manifest
// declares no action, resource or check yet.
func (module) Manifest() core.Manifest {
	return core.Manifest{ID: ID, Version: 1, DefaultEnabled: true}
}
```

Create `internal/modules/tailnet/tailnet.go`:

```go
// Package tailnet is the controller-side tailnet module (spec 4.2): the
// OAuth connection, devices, auth keys and the policy file.
package tailnet

import "github.com/darkraise/darkpanel/internal/core"

// ID is the module id.
const ID = "tailnet"

type module struct{}

// Module returns the tailnet module.
func Module() core.Module { return module{} }

// Manifest declares the module. Every module is enabled by default;
// preflight decides whether it is available on a host (R9). The manifest
// declares no action, resource or check yet.
func (module) Manifest() core.Manifest {
	return core.Manifest{ID: ID, Version: 1, DefaultEnabled: true}
}
```

Create `internal/modules/derp/derp.go`:

```go
// Package derp is the DERP server module (spec 4.3): a custom derper on a
// host and its region in the tailnet policy.
package derp

import "github.com/darkraise/darkpanel/internal/core"

// ID is the module id.
const ID = "derp"

type module struct{}

// Module returns the DERP module.
func Module() core.Module { return module{} }

// Manifest declares the module. Every module is enabled by default;
// preflight decides whether it is available on a host (R9). The manifest
// declares no action, resource or check yet.
func (module) Manifest() core.Manifest {
	return core.Manifest{ID: ID, Version: 1, DefaultEnabled: true}
}
```

Create `internal/modules/npm/npm.go`:

```go
// Package npm is the Nginx Proxy Manager module (spec section 6): the
// Darkpanel-owned proxy container and its proxy hosts and certificates.
package npm

import "github.com/darkraise/darkpanel/internal/core"

// ID is the module id.
const ID = "npm"

type module struct{}

// Module returns the Nginx Proxy Manager module.
func Module() core.Module { return module{} }

// Manifest declares the module. Every module is enabled by default;
// preflight decides whether it is available on a host (R9). The manifest
// declares no action, resource or check yet.
func (module) Manifest() core.Manifest {
	return core.Manifest{ID: ID, Version: 1, DefaultEnabled: true}
}
```

Create `internal/modules/cloudflare/cloudflare.go`:

```go
// Package cloudflare is the Cloudflare Tunnel module (spec section 7): the
// account, tunnels, connectors and public hostnames.
package cloudflare

import "github.com/darkraise/darkpanel/internal/core"

// ID is the module id.
const ID = "cloudflare"

type module struct{}

// Module returns the Cloudflare Tunnel module.
func Module() core.Module { return module{} }

// Manifest declares the module. Every module is enabled by default;
// preflight decides whether it is available on a host (R9). The manifest
// declares no action, resource or check yet.
func (module) Manifest() core.Manifest {
	return core.Manifest{ID: ID, Version: 1, DefaultEnabled: true}
}
```

Create `internal/modules/panel/panel.go`:

```go
// Package panel is the panel exposure module (spec section 8): exposing the
// Darkpanel UI through Nginx Proxy Manager or a tunnel, and removing it.
package panel

import "github.com/darkraise/darkpanel/internal/core"

// ID is the module id.
const ID = "panel"

type module struct{}

// Module returns the panel exposure module.
func Module() core.Module { return module{} }

// Manifest declares the module. Every module is enabled by default;
// preflight decides whether it is available on a host (R9). The manifest
// declares no action, resource or check yet.
func (module) Manifest() core.Manifest {
	return core.Manifest{ID: ID, Version: 1, DefaultEnabled: true}
}
```

Replace `internal/modules/modules.go` with:

```go
// Package modules lists the compiled-in modules. Adding a module adds one
// line to All and changes nothing else in the core (R5).
package modules

import (
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/cloudflare"
	"github.com/darkraise/darkpanel/internal/modules/derp"
	"github.com/darkraise/darkpanel/internal/modules/docker"
	"github.com/darkraise/darkpanel/internal/modules/firewall"
	"github.com/darkraise/darkpanel/internal/modules/hostinfo"
	"github.com/darkraise/darkpanel/internal/modules/npm"
	"github.com/darkraise/darkpanel/internal/modules/panel"
	"github.com/darkraise/darkpanel/internal/modules/tailnet"
	"github.com/darkraise/darkpanel/internal/modules/tailscale"
)

// All returns every compiled-in module in registration order.
func All() []core.Module {
	return []core.Module{
		hostinfo.Module(),
		firewall.Module(),
		docker.Module(),
		tailscale.Module(),
		tailnet.Module(),
		derp.Module(),
		npm.Module(),
		cloudflare.Module(),
		panel.Module(),
	}
}
```

C7 mentions a one-line `Summary`; `core.Manifest` has no such field (`internal/core/contract.go:566-577`), so the manifests carry `ID`, `Version` and `DefaultEnabled` only, as spec 1.12's "every module is default-enabled" requires.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `TMPDIR=/root/.cache/w0c-tmp/8 GOTOOLCHAIN=local go test -count=1 ./... && TMPDIR=/root/.cache/w0c-tmp/8 GOTOOLCHAIN=local go vet ./... && TMPDIR=/root/.cache/w0c-tmp/8 golangci-lint run --allow-serial-runners ./internal/modules/...`
Expected: every package `ok` (including `internal/modules`, whose `TestSnapshotsCurrent` shows that no schema snapshot changed, and `internal/controller/server`), vet silent, `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add internal/modules
git commit -m "feat(modules): add the eight v1 module skeletons"
rm -rf /root/.cache/w0c-tmp/8
```

### Task 9: Path-selection hook

This task creates the frozen file `cmd/darkpanel/paths.go` of spec 1.12 with the two hooks a harness build replaces (wave 9d's `cmd/darkpanel/paths_harness.go` sets both in `init`, so `--state-dir <dir>` selects `paths.Under(dir)`): `selectPaths` resolves the process's paths and `addRootFlags` adds persistent flags. Every `paths.Resolve` call in `cmd/darkpanel` goes through `selectPaths`. Today `agent.go` and `update.go` resolve through a test seam `resolvePaths = paths.Resolve` (`cmd/darkpanel/agent.go:49`) and `serve` calls `paths.Resolve` directly (`cmd/darkpanel/main.go:179`); `selectPaths` replaces the seam, so the existing tests that swapped `resolvePaths` swap `selectPaths` instead. The update rollback fallback (`cmd/darkpanel/update.go:229`) runs inside a `cli.LocalFallback`, which has no command, so it passes `nil`; the comment on `selectPaths` says a replacement must accept `nil`. Test helpers carry the prefix `ps` (`psSwap`).

**Files:**
- Create: `cmd/darkpanel/paths.go`
- Modify: `cmd/darkpanel/agent.go:49` (delete the `resolvePaths` seam), `:247`, `:303`, `:381`, `:417` (the four `resolvePaths(dev)` calls)
- Modify: `cmd/darkpanel/update.go:58`, `:229`
- Modify: `cmd/darkpanel/main.go:25` (the `internal/paths` import), `:108-112` (`newRoot`), `:179` (`serve`)
- Modify: `cmd/darkpanel/agent_test.go:15-17` (imports), `:72-85`, `:447`
- Modify: `cmd/darkpanel/update_test.go:15-17` (imports), `:58-61`
- Test: `cmd/darkpanel/paths_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `var selectPaths func(cmd *cobra.Command, dev bool) (paths.Paths, error)` and `var addRootFlags func(root *cobra.Command)` in `cmd/darkpanel/paths.go` (spec 1.12, named in Global Constraints); Task 32 and wave 9d use them; `newRoot` calls `addRootFlags` on the finished tree.

**Items:** 52

**Depends on:** none

**Implementer:** dforge:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 1 = 4

- [ ] **Step 1: Write the failing tests and move the existing seam**

Create `cmd/darkpanel/paths_test.go`:

```go
package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/darkraise/darkpanel/internal/cli"
	"github.com/darkraise/darkpanel/internal/paths"
)

// psSwap replaces selectPaths for one test, recording the name of every
// command it resolves for ("" for none).
func psSwap(t *testing.T, p paths.Paths) *[]string {
	t.Helper()
	saved := selectPaths
	t.Cleanup(func() { selectPaths = saved })
	var calls []string
	selectPaths = func(cmd *cobra.Command, _ bool) (paths.Paths, error) {
		name := ""
		if cmd != nil {
			name = cmd.Name()
		}
		calls = append(calls, name)
		return p, nil
	}
	return &calls
}

func TestSelectPathsResolves(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	t.Setenv("XDG_RUNTIME_DIR", "")
	if p, err := selectPaths(nil, false); err != nil || p != paths.Production() {
		t.Errorf("selectPaths(nil, false) = %+v, %v; want the production paths", p, err)
	}
	if p, err := selectPaths(&cobra.Command{Use: "serve"}, true); err != nil || p != paths.Under(filepath.Join(state, "darkpanel-dev")) {
		t.Errorf("selectPaths(serve, true) = %+v, %v; want the development paths", p, err)
	}
}

func TestServeResolvesThroughSelectPaths(t *testing.T) {
	served := fakeServe(t, 0, nil)
	want := paths.Under(t.TempDir())
	calls := psSwap(t, want)
	if out, code := runArgs("serve", "--dev"); code != cli.ExitOK {
		t.Fatalf("serve --dev: exit %d: %s", code, out)
	}
	if len(*served) != 1 || (*served)[0].Paths != want || strings.Join(*calls, ",") != "serve" {
		t.Errorf("serve --dev: served %+v after selectPaths calls %v; want the selected paths, resolved once for serve", *served, *calls)
	}
}

func TestNewRootAddsRootFlags(t *testing.T) {
	saved := addRootFlags
	t.Cleanup(func() { addRootFlags = saved })
	addRootFlags = func(root *cobra.Command) { root.PersistentFlags().String("probe-dir", "", "a test flag") }
	root, err := newRoot(cli.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if root.PersistentFlags().Lookup("probe-dir") == nil {
		t.Error("newRoot did not call addRootFlags")
	}
	if cmd, _, err := root.Find([]string{"agent", "run"}); err != nil || cmd.Flag("probe-dir") == nil {
		t.Errorf("agent run does not inherit the root flag: %v, %v", cmd, err)
	}
}
```

In `cmd/darkpanel/agent_test.go`, add the cobra import as its own group between the standard library and the repository imports:

```go
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/darkraise/darkpanel/internal/agent/daemon"
```

and in `fakeAgent` replace

```go
	oldSystemd, oldUpdater, oldResolve, oldLayout := newSystemd, newUpdater, resolvePaths, installLayout
	t.Cleanup(func() {
		geteuid, daemonRun, daemonEnroll, daemonCheck = oldEUID, oldRun, oldEnroll, oldCheck
		newSystemd, newUpdater, resolvePaths, installLayout = oldSystemd, oldUpdater, oldResolve, oldLayout
	})
```

with

```go
	oldSystemd, oldUpdater, oldResolve, oldLayout := newSystemd, newUpdater, selectPaths, installLayout
	t.Cleanup(func() {
		geteuid, daemonRun, daemonEnroll, daemonCheck = oldEUID, oldRun, oldEnroll, oldCheck
		newSystemd, newUpdater, selectPaths, installLayout = oldSystemd, oldUpdater, oldResolve, oldLayout
	})
```

and

```go
	resolvePaths = func(dev bool) (paths.Paths, error) {
```

with

```go
	selectPaths = func(_ *cobra.Command, dev bool) (paths.Paths, error) {
```

and in `TestAgentCheckExit` replace

```go
	resolvePaths = func(bool) (paths.Paths, error) { return paths.Paths{}, errors.New("no state directory") }
```

with

```go
	selectPaths = func(*cobra.Command, bool) (paths.Paths, error) {
		return paths.Paths{}, errors.New("no state directory")
	}
```

In `cmd/darkpanel/update_test.go`, add the same cobra import group (between `"time"` and `"github.com/darkraise/darkpanel/internal/cli"`), and in `fakeUpdate` replace

```go
	oldEUID, oldResolve, oldOpen := geteuid, resolvePaths, openUpdater
	t.Cleanup(func() { geteuid, resolvePaths, openUpdater = oldEUID, oldResolve, oldOpen })
	geteuid = func() int { return euid }
	resolvePaths = func(dev bool) (paths.Paths, error) {
```

with

```go
	oldEUID, oldResolve, oldOpen := geteuid, selectPaths, openUpdater
	t.Cleanup(func() { geteuid, selectPaths, openUpdater = oldEUID, oldResolve, oldOpen })
	geteuid = func() int { return euid }
	selectPaths = func(_ *cobra.Command, dev bool) (paths.Paths, error) {
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mkdir -p /root/.cache/w0c-tmp/9 && TMPDIR=/root/.cache/w0c-tmp/9 GOTOOLCHAIN=local go test -count=1 ./cmd/darkpanel/`
Expected: FAIL to compile with `undefined: selectPaths` and `undefined: addRootFlags`.

- [ ] **Step 3: Write the implementation**

Create `cmd/darkpanel/paths.go`:

```go
package main

import (
	"github.com/spf13/cobra"

	"github.com/darkraise/darkpanel/internal/paths"
)

// selectPaths resolves the process's paths; a harness build replaces it.
// cmd is the running command, or nil where none is at hand (the update
// rollback fallback), so a replacement must accept nil.
var selectPaths = func(_ *cobra.Command, dev bool) (paths.Paths, error) { return paths.Resolve(dev) }

// addRootFlags lets a harness build add persistent flags.
var addRootFlags = func(*cobra.Command) {}
```

The parameters are `_` or unnamed because revive's `unused-parameter` rule flags unused named ones; the types are those of spec 1.12.

In `cmd/darkpanel/agent.go`, delete the line `	resolvePaths = paths.Resolve` from the seam block (the `paths` import stays: `agent.go` still uses `paths.Paths`), and replace each of the four calls

```go
			p, err := resolvePaths(dev)
```

(in `newAgentRunCmd`, `newAgentEnrollCmd`, `newAgentCheckCmd` and `newAgentTaskCmd`, each inside a `RunE: func(cmd *cobra.Command, ...)`) with

```go
			p, err := selectPaths(cmd, dev)
```

In `cmd/darkpanel/update.go`, in `withUpdater` replace `p, err := resolvePaths(false)` with `p, err := selectPaths(cmd, false)`, and in `rollbackFallback` replace `p, err := resolvePaths(false)` with `p, err := selectPaths(nil, false)`.

In `cmd/darkpanel/main.go`, in `serve`'s `RunE` replace

```go
			p, err := paths.Resolve(dev)
```

with

```go
			p, err := selectPaths(cmd, dev)
```

delete the now unused import line `	"github.com/darkraise/darkpanel/internal/paths"`, and in `newRoot` replace

```go
	root, err := cli.NewRoot(opts)
	if err != nil {
		return nil, err
	}
	if err := addDirectRollback(root); err != nil {
```

with

```go
	root, err := cli.NewRoot(opts)
	if err != nil {
		return nil, err
	}
	addRootFlags(root)
	if err := addDirectRollback(root); err != nil {
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `TMPDIR=/root/.cache/w0c-tmp/9 GOTOOLCHAIN=local go test -count=1 ./cmd/darkpanel/ && TMPDIR=/root/.cache/w0c-tmp/9 GOTOOLCHAIN=local go vet -tags harness ./cmd/darkpanel/ && TMPDIR=/root/.cache/w0c-tmp/9 golangci-lint run --allow-serial-runners ./cmd/... && ! rg -n 'resolvePaths|paths\.Resolve\(' cmd/darkpanel --glob '!*_test.go' --glob '!paths.go'`
Expected: `ok  	github.com/darkraise/darkpanel/cmd/darkpanel`, vet silent, `0 issues.`, and `rg` finds no call outside `paths.go` (the test file `agent_test.go` keeps one `paths.Resolve(true)` inside its fake, which is intended).

- [ ] **Step 5: Commit**

```bash
git add cmd/darkpanel/paths.go cmd/darkpanel/paths_test.go cmd/darkpanel/agent.go cmd/darkpanel/update.go \
  cmd/darkpanel/main.go cmd/darkpanel/agent_test.go cmd/darkpanel/update_test.go
git commit -m "feat(cmd): route path resolution through selectPaths"
rm -rf /root/.cache/w0c-tmp/9
```

### Task 10: Web module entry types and registry helpers

Widens `ModuleEntry` to the shape of spec 1.12 (optional host `routes`, and the optional container actions, "Add host" extension and global page) and adds the two registry helpers the shell, the app routes and the "Add host" dialog consume. `enabledModules` now returns only entries with host pages, typed as `HostModuleEntry`, so `web/src/pages/host.tsx` compiles unchanged. The new tests extend the existing `describe("registry")` block of `web/src/pages/host.test.tsx` and reuse its `entry`, `host` and `state` helpers; they declare no new top-level helper.

**Files:**
- Modify: `web/src/modules/types.ts` (whole file)
- Modify: `web/src/modules/registry.ts` (whole file)
- Test: `web/src/pages/host.test.tsx:8` (import) and the `describe("registry")` block (three new tests before "an unavailable module without a reason gets a default one")

**Interfaces:**
- Consumes: nothing.
- Produces: C22's `ContainerRef`, `ContainerAction`, `HostCreateExtension`, `GlobalPage` and `ModuleEntry` in `types.ts`; in `registry.ts` the type `HostModuleEntry = ModuleEntry & { routes: NonNullable<ModuleEntry["routes"]> }`, `enabledModules(entries, host): HostModuleEntry[]`, `ModuleView` whose `pages` and `unavailable` kinds carry a `HostModuleEntry`, `globalPages(entries: ModuleEntry[]): GlobalPage[]` and `hostCreateExtensions(entries: ModuleEntry[], host: Host): HostCreateExtension[]`. `types.ts` and `registry.ts` are frozen after this plan (spec 1.12).

**Items:** 52

**Depends on:** none

**Implementer:** dforge:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 0 = 3

- [ ] **Step 1: Write the failing tests**

In `web/src/pages/host.test.tsx`, replace the import line

```tsx
import { enabledModules, moduleRegistry, moduleView } from "@/modules/registry"
```

with

```tsx
import { enabledModules, globalPages, hostCreateExtensions, moduleRegistry, moduleView } from "@/modules/registry"
```

and insert these tests inside `describe("registry", ...)`, directly before `test("an unavailable module without a reason gets a default one", ...)`:

```tsx
  test("an entry with only a global page has no host tab and no host view", () => {
    const tailnet: ModuleEntry = {
      id: "tailnet", label: "Tailnet", icon: Server,
      global: { path: "tailnet", label: "Tailnet", routes: lazy(async () => ({ default: () => null })) },
    }
    const h = host([state("alpha", true, true), state("tailnet", true, true)])
    expect(enabledModules([entry("alpha"), tailnet], h).map((m) => m.id)).toEqual(["alpha"])
    expect(moduleView([entry("alpha"), tailnet], h, "tailnet").kind).toBe("not-enabled")
  })

  test("globalPages lists every global page in registry order", () => {
    const page = (path: string) => ({ path, label: path, routes: lazy(async () => ({ default: () => null })) })
    const entries: ModuleEntry[] = [
      { ...entry("alpha"), global: page("alpha") },
      entry("beta"),
      { id: "gamma", label: "Gamma", icon: Server, global: page("gamma") },
    ]
    expect(globalPages(entries).map((p) => p.path)).toEqual(["alpha", "gamma"])
  })

  test("hostCreateExtensions lists the extensions of modules enabled on the host", () => {
    const ext = (id: string) => ({ id, render: lazy(async () => ({ default: () => null })) })
    const entries: ModuleEntry[] = [
      { ...entry("alpha"), hostCreate: ext("alpha.join") },
      { ...entry("beta"), hostCreate: ext("beta.join") },
      entry("gamma"),
      { id: "delta", label: "Delta", icon: Server, hostCreate: ext("delta.join") },
    ]
    const h = host([state("alpha", true, true), state("beta", false, true), state("gamma", true, true), state("delta", true, false)])
    expect(hostCreateExtensions(entries, h).map((e) => e.id)).toEqual(["alpha.join", "delta.join"])
  })
```

- [ ] **Step 2: Run them to make sure they fail**

Run: `mkdir -p /root/.cache/w0c-tmp/10 && TMPDIR=/root/.cache/w0c-tmp/10 pnpm -C web vitest run src/pages/host.test.tsx`
Expected: FAIL — "an entry with only a global page has no host tab" gets `["alpha", "tailnet"]`, and the two helper tests fail with `globalPages is not a function` and `hostCreateExtensions is not a function`.

- [ ] **Step 3: Write the types**

Replace the whole of `web/src/modules/types.ts` with:

```ts
import type { ComponentType, LazyExoticComponent } from "react"
import type { LucideIcon } from "lucide-react"

// What a module's route bundle receives: the host whose pages it renders.
export interface ModuleRouteProps {
  hostId: string
}

// A container as the docker module's container page hands it to the
// container action slot.
export interface ContainerRef {
  hostId: string
  containerId: string
  name: string
  composeService?: string
  ports: { privatePort: number; protocol: "tcp" | "udp"; bindings: { hostIp: string; hostPort: number }[] }[]
}

// One button of the container action slot, rendered on the docker module's
// container page for every module enabled on the host.
export interface ContainerAction {
  id: string // "firewall.allow"
  label: string
  render: LazyExoticComponent<ComponentType<{ container: ContainerRef; onDone: () => void }>>
}

// Extra inputs of the "Add host" dialog.
export interface HostCreateExtension {
  id: string // "tailnet.join"
  // Renders extra inputs; value is merged into core.host.create's input.
  render: LazyExoticComponent<ComponentType<{ value: Record<string, unknown>; onChange: (v: Record<string, unknown>) => void }>>
}

// A page that belongs to no host, such as the tailnet.
export interface GlobalPage {
  path: string // "tailnet"; mounted at /<path>/*
  label: string
  routes: LazyExoticComponent<ComponentType>
}

// One line of the module route registry (spec section 17, R14). A module
// with only a global page has no routes, and so no host page.
export interface ModuleEntry {
  id: string
  label: string
  icon: LucideIcon
  routes?: LazyExoticComponent<ComponentType<ModuleRouteProps>> // host pages
  containerActions?: ContainerAction[]
  hostCreate?: HostCreateExtension
  global?: GlobalPage
}
```

- [ ] **Step 4: Write the registry helpers**

Replace the whole of `web/src/modules/registry.ts` with:

```ts
import { lazy } from "react"
import { Info } from "lucide-react"
import type { Host } from "@/api/types"
import type { GlobalPage, HostCreateExtension, ModuleEntry } from "./types"

// One line per module (R14). Adding a module adds its line here and its
// folder modules/<id>/; no other module's files change.
export const moduleRegistry: ModuleEntry[] = [
  { id: "hostinfo", label: "Host information", icon: Info, routes: lazy(() => import("./hostinfo/routes")) },
]

// HostModuleEntry is an entry that has host pages.
export type HostModuleEntry = ModuleEntry & { routes: NonNullable<ModuleEntry["routes"]> }

export type ModuleView =
  | { kind: "pages"; entry: HostModuleEntry }
  | { kind: "unavailable"; entry: HostModuleEntry; reason: string }
  | { kind: "not-enabled" }

function enabledOn(host: Host, moduleId: string): boolean {
  return host.modules.some((state) => state.id === moduleId && state.enabled)
}

// enabledModules lists, in registry order, the entries with host pages that
// the host has enabled. An entry with only a global page has no host tab.
export function enabledModules(entries: ModuleEntry[], host: Host): HostModuleEntry[] {
  return entries.filter((entry): entry is HostModuleEntry => entry.routes !== undefined && enabledOn(host, entry.id))
}

// moduleView decides what a module's tab shows: its pages, or the reason an
// enabled module is unavailable (R9), or that it is not enabled.
export function moduleView(entries: ModuleEntry[], host: Host, moduleId: string): ModuleView {
  const entry = enabledModules(entries, host).find((e) => e.id === moduleId)
  const state = host.modules.find((s) => s.id === moduleId)
  if (entry === undefined || state === undefined) return { kind: "not-enabled" }
  if (!state.available) return { kind: "unavailable", entry, reason: state.reason ?? "The module is unavailable on this host." }
  return { kind: "pages", entry }
}

// globalPages lists, in registry order, the pages that belong to no host.
export function globalPages(entries: ModuleEntry[]): GlobalPage[] {
  return entries.flatMap((entry) => (entry.global === undefined ? [] : [entry.global]))
}

// hostCreateExtensions lists, in registry order, the "Add host" inputs of
// the modules enabled on host.
export function hostCreateExtensions(entries: ModuleEntry[], host: Host): HostCreateExtension[] {
  return entries.flatMap((entry) => (entry.hostCreate !== undefined && enabledOn(host, entry.id) ? [entry.hostCreate] : []))
}
```

- [ ] **Step 5: Run the tests and the checks**

Run: `TMPDIR=/root/.cache/w0c-tmp/10 pnpm -C web vitest run src/pages/host.test.tsx`
Expected: PASS, `Tests  17 passed (17)`.

Run: `TMPDIR=/root/.cache/w0c-tmp/10 make lint-web test-web`
Expected: no ESLint or `tsc` finding (`host.tsx` compiles unchanged because `enabledModules` returns `HostModuleEntry`), and every Vitest test passes.

- [ ] **Step 6: Commit**

```bash
git add web/src/modules/types.ts web/src/modules/registry.ts web/src/pages/host.test.tsx
git commit -m "feat(web): add module extension point types"
rm -rf /root/.cache/w0c-tmp/10
```

### Task 11: Web API client for changes, effects and steps

Brings the web client up to the controller's wire contract of spec 1.3, 1.5 and 1.7: the plan's guards gain `overridable` and `overridden`, a change gains its lifecycle fields, and new calls cover a single change, confirm, revert, undo, repair, release, effects, the observe preview, resolution, composite steps and module availability. Every type mirrors the Go DTO field for field: `changeResponse` and `planResponse` in `internal/controller/api/actions.go:48-87`, `repairResponse`, `releaseResponse`, `effectsResponse` and `stepsResponse` in `internal/controller/api/changes.go:22-46`, `core.EffectView`, `ObservationView`, `ObserveResult`, `Candidate`, `StateDoc` and `StepView` in `internal/core/contract.go`, and `wire.ModuleAvailability` in `internal/agent/wire/wire.go:122-134`. Two Go details show through: `StepView.Action` is a `core.ActionRef`, which has no JSON tags, so a step's action is `{ Name, Version }`; and lists the controller does not normalise (`candidates`, `allowed`, `earlierChildChangeIds`) may be `null`. `changeError` stops reporting a change that is still in progress (an asynchronous apply answers `202` with the change `applying`). The tests extend `web/src/api/client.test.ts` and reuse its `respond`, `stub` and `sent` helpers.

**Files:**
- Modify: `web/src/api/types.ts` (whole file)
- Modify: `web/src/api/client.ts` (whole file)
- Test: `web/src/api/client.test.ts:1-14` (imports), a new `describe("change helpers")` before `describe("messages")`, and four rows of the `changeError` table
- Modify: `web/src/pages/create-token-dialog.test.tsx:21-25` and `web/src/pages/tokens.test.tsx:29-34` (their `Change` fixtures gain the new fields)

**Interfaces:**
- Consumes: nothing.
- Produces: C21 — the types `Guard` (with `overridable`, `overridden`), `ChangeError` (with `cause?`), `Change` (with `parentChangeId`, `owner`, `repairedBy`, `repairOutcome`, `override`), `StateDoc`, `EffectKind`, `EffectState`, `Resolution`, `Effect`, `Candidate`, `Observation`, `ObserveResult`, `Step`, `RepairBody`, `RepairResult`, `ReleaseResult`, `ActionAvailability`, `ModuleAvailability`; the functions `getChange(id)`, `confirmChange(id)`, `revertChange(id)`, `undoChange(id)`, `repairChange(id, body = {})`, `releaseChange(id, reason)`, `listEffects(id): Promise<Effect[]>`, `observeEffect(id, seq)`, `resolveEffect(id, seq, { resolution, resource? })`, `listSteps(id): Promise<Step[]>`, `getAvailability(hostId, module)`, `changeInProgress(change): boolean`; the query keys `changeQueryKey(id)`, `stepsQueryKey(id)`, `effectsQueryKey(id)`. `applyPlan` keeps its signature and returns the `202` body like a `200` one.

**Items:** 52, 95

**Depends on:** none

**Implementer:** dforge:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 0 = 3

- [ ] **Step 1: Write the failing tests**

In `web/src/api/client.test.ts`, replace the import block from `import {` to `} from "./client"` with:

```ts
import {
  ApiError,
  applyPlan,
  changeError,
  changeInProgress,
  confirmChange,
  errorMessage,
  getAvailability,
  getChange,
  listEffects,
  listSteps,
  observeEffect,
  readAction,
  readResource,
  releaseChange,
  repairChange,
  request,
  resolveEffect,
  revertChange,
  setCsrfRejectedHandler,
  setCsrfToken,
  setUnauthorizedHandler,
  undoChange,
} from "./client"
```

insert directly before `describe("messages", () => {`:

```ts
describe("change helpers", () => {
  test.each([
    ["getChange", () => getChange("c 1"), "GET", "/api/v1/changes/c%201", undefined],
    ["confirmChange", () => confirmChange("c1"), "POST", "/api/v1/changes/c1/confirm", undefined],
    ["revertChange", () => revertChange("c1"), "POST", "/api/v1/changes/c1/revert", undefined],
    ["undoChange", () => undoChange("c1"), "POST", "/api/v1/changes/c1/undo", undefined],
    ["repairChange without a body", () => repairChange("c1"), "POST", "/api/v1/changes/c1/repair", {}],
    ["repairChange with input", () => repairChange("c1", { input: { port: 22 } }), "POST", "/api/v1/changes/c1/repair", { input: { port: 22 } }],
    ["repairChange of a step", () => repairChange("c1", { step: "ports", as: "reverted" }), "POST", "/api/v1/changes/c1/repair",
      { step: "ports", as: "reverted" }],
    ["releaseChange", () => releaseChange("c1", "gone by hand"), "POST", "/api/v1/changes/c1/release", { reason: "gone by hand" }],
    ["observeEffect", () => observeEffect("c1", 2), "GET", "/api/v1/changes/c1/effects/2/observe", undefined],
    ["resolveEffect", () => resolveEffect("c1", 2, { resolution: "accept" }), "POST", "/api/v1/changes/c1/effects/2/resolve",
      { resolution: "accept" }],
    ["getAvailability", () => getAvailability("h 1", "fire wall"), "GET", "/api/v1/hosts/h%201/modules/fire%20wall/availability", undefined],
  ])("%s calls its route", async (_, call, method, url, body) => {
    const fetchMock = stub(respond(200, { ok: true }))
    await expect(call()).resolves.toEqual({ ok: true })
    const s = sent(fetchMock)
    expect([s.method, s.url]).toEqual([method, url])
    expect(s.body).toEqual(body)
    if (method === "POST") expect(s.headers.get("Content-Type")).toBe("application/json")
  })

  test("listEffects and listSteps unwrap their lists", async () => {
    const fetchMock = stub(respond(200, { effects: [{ seq: 1 }] }))
    await expect(listEffects("c1")).resolves.toEqual([{ seq: 1 }])
    expect(sent(fetchMock).url).toBe("/api/v1/changes/c1/effects")
    const stepsMock = stub(respond(200, { steps: [{ id: "ports" }] }))
    await expect(listSteps("c1")).resolves.toEqual([{ id: "ports" }])
    expect(sent(stepsMock).url).toBe("/api/v1/changes/c1/steps")
  })

  test("applyPlan returns a change still applying from a 202 answer", async () => {
    stub(respond(202, { id: "c3", state: "applying" }))
    const change = await applyPlan(plan)
    expect(change.state).toBe("applying")
    expect(changeInProgress(change)).toBe(true)
    expect(changeError(change)).toBeNull()
  })
})
```

and in the `changeError` table, after the row `["interrupted", null, "The change ended in state interrupted."],`, add:

```ts
    ["applying", null, null],
    ["awaiting_confirmation", null, null],
    ["undoing", null, null],
    ["needs_repair", { code: "core.needs_repair", message: "the change needs repair", cause: "cloud.uncertain_outcome" }, "the change needs repair"],
```

- [ ] **Step 2: Run them to make sure they fail**

Run: `mkdir -p /root/.cache/w0c-tmp/11 && TMPDIR=/root/.cache/w0c-tmp/11 pnpm -C web vitest run src/api/client.test.ts`
Expected: FAIL — every "change helpers" row fails with `... is not a function` (for example `getChange is not a function`), and the `applying`, `awaiting_confirmation` and `undoing` rows of `changeError` get "The change ended in state applying." and so on instead of `null`.

- [ ] **Step 3: Write the types**

Replace the whole of `web/src/api/types.ts` with:

```ts
// The controller API's JSON shapes (spec section 12).
export type Scope = "read" | "operate" | "admin"
export interface User { id: string; username: string; role: string; state: string; createdAt: string }
export interface TokenInfo { id: string; name: string; scope: string; createdAt: string; expiresAt: string; lastUsedAt: string | null; revokedAt: string | null }
export interface NewToken extends TokenInfo { token: string }
export interface TokenList { tokens: TokenInfo[] }
export interface Session { user: User; csrfToken: string | null; expiresAt: string | null; token: TokenInfo | null }
export interface ModuleState { id: string; version: number; enabled: boolean; available: boolean; reason: string | null; checkedAt: string | null }
export interface Host { id: string; name: string; isLocal: boolean; online: boolean; modules: ModuleState[]; ingestion: { ok: boolean; since: string; error: string | null } | null }
export interface HostList { hosts: Host[] }
export interface DiffLine { op: string; text: string }
export interface Guard { id: string; ok: boolean; message: string; overridable: boolean; overridden: boolean }
export interface Plan { planId: string; hostId: string | null; action: string; version: number; inputHash: string; diff: DiffLine[]; guards: Guard[]; expiresAt: string; confirmation: string | null }
export interface ChangeError { code: string; message: string; cause?: string }
export interface Change<O = unknown> { id: string; hostId: string | null; action: string; version: number; class: string; state: string; actor: string; createdAt: string; updatedAt: string; revertDeadline: string | null; output: O | null; error: ChangeError | null; compensatedBy: string | null; parentChangeId: string | null; owner: string | null; repairedBy: string | null; repairOutcome: string | null; override: unknown }
// The lifecycle shapes of spec 1.3's wire contract.
export interface StateDoc { state: unknown; version?: unknown }
export type EffectKind = "create" | "modify" | "delete"
export type EffectState = "intended" | "made" | "skipped" | "unresolved" | "undoing" | "undone" | "accepted" | "abandoned"
export type Resolution = "accept" | "reconcile"
export interface Effect { changeId: string; seq: number; kind: EffectKind; resource: string; local: boolean; inverseLocal: boolean; commitBy: string | null; state: EffectState; reason: string; before: StateDoc; after: StateDoc | null; post: StateDoc | null; undoTo: StateDoc | null; undoResource: string; current: StateDoc | null; resolution: string; resolvedBy: string; resolvedAt: string | null }
export interface Candidate { resource: string; state: StateDoc }
export interface Observation { current: StateDoc; resource: string; matches: number; candidates: Candidate[] | null; exclusive: boolean }
export interface ObserveResult { effect: Effect; observation: Observation; allowed: Resolution[] | null }
// A step's action is core.ActionRef, which the controller encodes without JSON tags.
export interface Step { id: string; ordinal: number; host: string; action: { Name: string; Version: number }; state: string; attempt: number; childChangeId: string; earlierChildChangeIds: string[] | null; childDeadline: string | null; error: ChangeError | null }
export type RepairBody = { input?: Record<string, unknown> } | { step: string; as: "reverted" | "applied" }
export interface RepairResult { change?: Change; plan?: Plan }
export interface ReleaseResult { change: Change; abandoned: Effect[] }
export interface ActionAvailability { name: string; available: boolean; reason: string | null }
export interface ModuleAvailability { module: string; available: boolean; reason: string | null; actions: ActionAvailability[] }
export interface AuditEntry { id: string; at: string; actor: string; hostId: string | null; action: string; version: number; phase: string; changeId: string | null; input: unknown; result: string; errorCode: string | null; source: string | null }
export interface AuditList { entries: AuditEntry[]; nextBefore: string | null }
export interface ListenerState { tailscaleBound: boolean; message: string }
export interface FieldError { path: string; message: string }
export interface Problem { type: string; title: string; status: number; detail: string; code: string; errors?: FieldError[] }
```

- [ ] **Step 4: Write the client calls**

Replace the whole of `web/src/api/client.ts` with:

```ts
import type {
  Change,
  Effect,
  FieldError,
  HostList,
  ListenerState,
  ModuleAvailability,
  ObserveResult,
  Plan,
  Problem,
  ReleaseResult,
  RepairBody,
  RepairResult,
  Resolution,
  Step,
  TokenList,
} from "./types"

export const CSRF_HEADER = "X-CSRF-Token"

export const HOSTS_QUERY_KEY = ["hosts"] as const
export const TOKENS_QUERY_KEY = ["tokens"] as const

// The query keys of one change; connectEvents invalidates them on the
// change and composite.step events.
export const changeQueryKey = (id: string) => ["change", id] as const
export const stepsQueryKey = (id: string) => ["steps", id] as const
export const effectsQueryKey = (id: string) => ["effects", id] as const

const UNSAFE_METHODS = new Set(["POST", "PUT", "PATCH", "DELETE"])
const SUCCESSFUL_STATES = new Set(["completed", "confirmed", "pending"])
// A change in one of these states has not ended: an asynchronous apply
// answers with the change applying, and an undo with it undoing.
const IN_PROGRESS_STATES = new Set(["applying", "awaiting_confirmation", "undoing", "recovering", "releasing"])

export class ApiError extends Error {
  readonly status: number
  readonly code: string
  readonly fields: FieldError[]
  readonly retryAfterSeconds: number | null

  constructor(status: number, code: string, message: string, fields: FieldError[] = [], retryAfterSeconds: number | null = null) {
    super(message)
    this.name = "ApiError"
    this.status = status
    this.code = code
    this.fields = fields
    this.retryAfterSeconds = retryAfterSeconds
  }
}

let csrfToken: string | null = null
let generation = 0
let unauthorizedHandler: (() => void) | null = null
let csrfRejectedHandler: (() => void) | null = null

// Every login and logout sets the CSRF token and so starts a new session
// generation. An auth.required answer to a request begun in an earlier
// generation must not end the current session.
export function setCsrfToken(token: string | null): void {
  csrfToken = token
  generation++
}

export function sessionGeneration(): number {
  return generation
}

export function setUnauthorizedHandler(handler: (() => void) | null): void {
  unauthorizedHandler = handler
}

export function setCsrfRejectedHandler(handler: (() => void) | null): void {
  csrfRejectedHandler = handler
}

export async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const startedIn = generation
  const headers: Record<string, string> = { Accept: "application/json", "Content-Type": "application/json" }
  if (UNSAFE_METHODS.has(method) && csrfToken !== null) headers[CSRF_HEADER] = csrfToken
  let res: Response
  try {
    res = await fetch(path, {
      method,
      headers,
      credentials: "same-origin",
      body: body === undefined ? undefined : JSON.stringify(body),
    })
  } catch {
    throw new ApiError(0, "network", "The controller cannot be reached. Check that it is running.")
  }
  if (!res.ok) {
    const err = await problemFrom(res)
    if (err.code === "auth.required" && startedIn === generation) unauthorizedHandler?.()
    if (err.code === "auth.csrf") csrfRejectedHandler?.()
    throw err
  }
  if (res.status === 204) return undefined as T
  return (await res.json()) as T
}

async function problemFrom(res: Response): Promise<ApiError> {
  let problem: Partial<Problem> = {}
  if ((res.headers.get("Content-Type") ?? "").includes("json")) {
    try {
      problem = (await res.json()) as Partial<Problem>
    } catch {
      problem = {}
    }
  }
  const retryAfter = Number.parseInt(res.headers.get("Retry-After") ?? "", 10)
  return new ApiError(
    res.status,
    problem.code ?? "internal",
    problem.detail || res.statusText || "The request failed.",
    problem.errors ?? [],
    Number.isNaN(retryAfter) ? null : retryAfter,
  )
}

function actionPath(name: string, version: number, op: "plan" | "apply" | "read", hostId?: string): string {
  const tail = `/actions/${encodeURIComponent(name)}/v${version}/${op}`
  return hostId === undefined ? `/api/v1${tail}` : `/api/v1/hosts/${encodeURIComponent(hostId)}${tail}`
}

export async function readAction<O>(name: string, version: number, input: object = {}, hostId?: string): Promise<O> {
  const res = await request<{ output: O }>("POST", actionPath(name, version, "read", hostId), { input })
  return res.output
}

export function planAction(name: string, version: number, input: object, hostId?: string): Promise<Plan> {
  return request<Plan>("POST", actionPath(name, version, "plan", hostId), { input })
}

// applyPlan applies a plan. Composites, long-running and task actions answer
// 202 with the change still applying; the change events say when it ends.
export function applyPlan<O>(plan: Plan, confirmation?: string): Promise<Change<O>> {
  const body: { planId: string; confirmation?: string } = { planId: plan.planId }
  if (confirmation !== undefined) body.confirmation = confirmation
  return request<Change<O>>("POST", actionPath(plan.action, plan.version, "apply", plan.hostId ?? undefined), body)
}

export function readResource<O>(hostId: string, module: string, resource: string): Promise<O> {
  const path = [hostId, "resources", module, resource].map(encodeURIComponent).join("/")
  return request<O>("GET", `/api/v1/hosts/${path}`)
}

export function listHosts(): Promise<HostList> {
  return readAction<HostList>("core.host.list", 1)
}

export function listTokens(): Promise<TokenList> {
  return readAction<TokenList>("core.token.list", 1)
}

export function getListenerState(): Promise<ListenerState> {
  return request<ListenerState>("GET", "/api/v1/controller/listeners")
}

function changePath(id: string, tail = ""): string {
  return `/api/v1/changes/${encodeURIComponent(id)}${tail}`
}

export function getChange(id: string): Promise<Change> {
  return request<Change>("GET", changePath(id))
}

export function confirmChange(id: string): Promise<Change> {
  return request<Change>("POST", changePath(id, "/confirm"))
}

export function revertChange(id: string): Promise<Change> {
  return request<Change>("POST", changePath(id, "/revert"))
}

export function undoChange(id: string): Promise<Change> {
  return request<Change>("POST", changePath(id, "/undo"))
}

// repairChange answers the change a repair started, or a plan that the
// caller applies with applyPlan.
export function repairChange(id: string, body: RepairBody = {}): Promise<RepairResult> {
  return request<RepairResult>("POST", changePath(id, "/repair"), body)
}

export function releaseChange(id: string, reason: string): Promise<ReleaseResult> {
  return request<ReleaseResult>("POST", changePath(id, "/release"), { reason })
}

export async function listEffects(id: string): Promise<Effect[]> {
  const res = await request<{ effects: Effect[] }>("GET", changePath(id, "/effects"))
  return res.effects
}

// observeEffect is the read-only preview of a resolution.
export function observeEffect(id: string, seq: number): Promise<ObserveResult> {
  return request<ObserveResult>("GET", changePath(id, `/effects/${seq}/observe`))
}

export function resolveEffect(id: string, seq: number, body: { resolution: Resolution; resource?: string }): Promise<Effect> {
  return request<Effect>("POST", changePath(id, `/effects/${seq}/resolve`), body)
}

export async function listSteps(id: string): Promise<Step[]> {
  const res = await request<{ steps: Step[] }>("GET", changePath(id, "/steps"))
  return res.steps
}

export function getAvailability(hostId: string, module: string): Promise<ModuleAvailability> {
  return request<ModuleAvailability>("GET", `/api/v1/hosts/${encodeURIComponent(hostId)}/modules/${encodeURIComponent(module)}/availability`)
}

export function errorMessage(err: unknown): string {
  return err instanceof ApiError ? err.message : "Something went wrong. Try again."
}

// changeInProgress reports whether a change has not ended yet.
export function changeInProgress(change: Change): boolean {
  return IN_PROGRESS_STATES.has(change.state)
}

// changeError is the failure a change ended with, or null while it
// succeeded or is still in progress.
export function changeError(change: Change): string | null {
  if (SUCCESSFUL_STATES.has(change.state) || IN_PROGRESS_STATES.has(change.state)) return null
  return change.error?.message ?? `The change ended in state ${change.state}.`
}
```

- [ ] **Step 5: Update the change fixtures**

`Change` now requires the lifecycle fields. In `web/src/pages/create-token-dialog.test.tsx`, replace

```tsx
    createdAt: "2026-10-19T08:00:00Z", updatedAt: "2026-10-19T08:00:00Z", revertDeadline: null, output, error, compensatedBy: null,
```

with

```tsx
    createdAt: "2026-10-19T08:00:00Z", updatedAt: "2026-10-19T08:00:00Z", revertDeadline: null, output, error, compensatedBy: null,
    parentChangeId: null, owner: null, repairedBy: null, repairOutcome: null, override: null,
```

and in `web/src/pages/tokens.test.tsx`, replace

```tsx
  output: { ...tokens[0], revokedAt: "2026-10-19T08:00:00Z" }, error: null, compensatedBy: null,
```

with

```tsx
  output: { ...tokens[0], revokedAt: "2026-10-19T08:00:00Z" }, error: null, compensatedBy: null,
  parentChangeId: null, owner: null, repairedBy: null, repairOutcome: null, override: null,
```

- [ ] **Step 6: Run the tests and the checks**

Run: `TMPDIR=/root/.cache/w0c-tmp/11 pnpm -C web vitest run src/api/client.test.ts`
Expected: PASS, every test of the file passing.

Run: `TMPDIR=/root/.cache/w0c-tmp/11 make lint-web test-web`
Expected: no ESLint or `tsc` finding (both page fixtures type-check), and every Vitest test passes.

- [ ] **Step 7: Commit**

```bash
git add web/src/api/types.ts web/src/api/client.ts web/src/api/client.test.ts web/src/pages/create-token-dialog.test.tsx web/src/pages/tokens.test.tsx
git commit -m "feat(web): add change lifecycle API calls"
rm -rf /root/.cache/w0c-tmp/11
```

### Task 12: Engine suspension hook

This task adds `lifecycle.Config.Suspended` (C15) and gates the controller engine's automatic work on it: `RecoverComposites` claims nothing and starts no runner, every composite runner pass (the child watcher) sleeps a `ChildPoll` instead of stepping, `Wake` does nothing, and `runRecoverer`, the one choke point every module recovery handler run goes through (live failure, startup recovery, repair), returns `errRecovererSuspended` without calling the handler, leaving the change `recovering` and owned by `module` for the next start. An error from the hook counts as suspended and is logged. With a nil hook (the agent, and the controller until Task 48 wires the quarantine flag) nothing changes. While suspended, `CompositesRecovered` reports true, so the `core.user.*`/`core.token.*` recovery gate does not refuse forever: a restored database's unfinished composites are quarantined records, which the operator resolves one by one (spec 10). The helper and test names carry the prefix `sus`/`Suspended`; the tests reuse `cfxFixture` (`composite_fixture_test.go`), `cutAt` (`composite_watch_test.go`), `awaitingGate` (`composite_confirm_test.go`) and `newRcvFixture` (`recoverer_test.go`) as written, and add `susFlag`, `susReopenCtrl`, `susStill` and the method `(*cfxFixture).ctrlChange`.

**Files:**
- Create: `internal/core/lifecycle/suspend.go`
- Modify: `internal/core/lifecycle/engine.go:39-67` (`Config` gains `Suspended` after `ModuleUnits`)
- Modify: `internal/core/lifecycle/composite_watch.go:27-33` (`Wake`) and `:63-66` (`RecoverComposites` and its doc comment)
- Modify: `internal/core/lifecycle/composite_run.go:390-396` (`runComposite`'s loop)
- Modify: `internal/core/lifecycle/recoverer.go:103` (`runRecoverer`'s first statement)
- Test: `internal/core/lifecycle/suspend_test.go`

**Interfaces:**
- Consumes: none
- Produces: C15 (`lifecycle.Config.Suspended func(ctx context.Context) (bool, error)`), which Task 48 sets to `quarantine.Active` over `controller.db`

**Items:** 52

**Depends on:** none

**Implementer:** dforge:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5

- [ ] **Step 1: Write the failing tests**

Create `internal/core/lifecycle/suspend_test.go`:

```go
package lifecycle

import (
	"context"
	"errors"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// susFlag is a Config.Suspended whose answer a test changes while the
// engine runs.
type susFlag struct {
	held atomic.Bool
	err  atomic.Pointer[error]
}

func (s *susFlag) suspended(context.Context) (bool, error) {
	if err := s.err.Load(); err != nil {
		return false, *err
	}
	return s.held.Load(), nil
}

// susReopenCtrl is reopenCtrl with Config.Suspended set to suspended.
func susReopenCtrl(f *cfxFixture, suspended func(context.Context) (bool, error)) {
	f.t.Helper()
	f.ctrl.Close()
	f.ctrlDB = openDBAt(f.t, f.ctrlPath, store.SideController)
	cfg := f.ctrlConfig()
	cfg.Suspended = suspended
	eng, err := New(cfg)
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(eng.Close)
	f.kids.setEngines(eng, nil)
	eng.SetChildren(f.kids)
	if err := eng.Recover(context.Background()); err != nil {
		f.t.Fatal(err)
	}
	f.ctrl = eng
}

// susStill checks for a while that composite id stays in state while the
// controller clock moves past several child polls, and that the agent gets
// no call meanwhile.
func susStill(t *testing.T, f *cfxFixture, id, state string) {
	t.Helper()
	calls := len(f.kids.callLog())
	for range 5 {
		f.ctrlClock.Advance(ChildPoll)
		time.Sleep(20 * time.Millisecond)
	}
	if v := f.ctrlChange(id); v.State != state {
		t.Fatalf("composite = %s, want it held in %s", v.State, state)
	}
	if got := f.kids.callLog(); len(got) != calls {
		t.Errorf("the agent got calls %v while the engine was suspended", got[calls:])
	}
}

// ctrlChange reads change id from the controller engine.
func (f *cfxFixture) ctrlChange(id string) core.ChangeView {
	f.t.Helper()
	v, err := f.ctrl.Change(context.Background(), id)
	if err != nil {
		f.t.Fatal(err)
	}
	return v
}

func TestSuspendedHoldsCompositeRecovery(t *testing.T) {
	failed := errors.New("controller.db is locked")
	for _, c := range []struct {
		name string
		set  func(*susFlag)
	}{
		{"the flag is set", func(s *susFlag) { s.held.Store(true) }},
		{"the flag cannot be read", func(s *susFlag) { s.err.Store(&failed) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newCfxFixture(t)
			cut := cutAt(f, "b", core.StepApplying, true)
			v := f.applyC(refCfxAdd, `{"name":"web"}`, core.ApplyOptions{})
			select {
			case <-cut:
			case <-time.After(10 * time.Second):
				t.Fatal("the runner never reached the cut-off point")
			}
			f.events.setOnEvent(nil)
			flag := &susFlag{}
			c.set(flag)
			susReopenCtrl(f, flag.suspended)
			if err := f.ctrl.RecoverComposites(context.Background()); err != nil {
				t.Fatal(err)
			}
			if !f.ctrl.CompositesRecovered() {
				t.Error("CompositesRecovered = false; a suspended recovery has nothing to wait for")
			}
			susStill(t, f, v.ID, core.StateApplying)

			// Held back, not lost: a later start without the flag recovers it.
			susReopenCtrl(f, nil)
			if err := f.ctrl.RecoverComposites(context.Background()); err != nil {
				t.Fatal(err)
			}
			if done := f.await(v.ID, core.StateReverted, core.StateCompleted, core.StateNeedsRepair); done.State != core.StateReverted {
				t.Fatalf("composite = %s (%+v), want reverted by the later recovery", done.State, done.Error)
			}
		})
	}
}

func TestSuspendedHoldsTheChildWatcher(t *testing.T) {
	f := newCfxFixture(t)
	flag := &susFlag{}
	susReopenCtrl(f, flag.suspended)
	v, g := awaitingGate(t, f)
	flag.held.Store(true)
	if _, err := f.agent.Confirm(context.Background(), cfxRoot, g.ChildID); err != nil {
		t.Fatal(err)
	}
	f.ctrl.Wake(g.ChildID)
	susStill(t, f, v.ID, core.StateAwaitingConfirmation)

	flag.held.Store(false)
	if done := f.await(v.ID, core.StateCompleted, core.StateReverted); done.State != core.StateCompleted {
		t.Fatalf("composite = %s, want completed once the runner resumes", done.State)
	}
}

func TestSuspendedHoldsModuleRecoveryHandlers(t *testing.T) {
	f, h := newRcvFixture(t)
	f.eng.Close()
	flag := &susFlag{}
	flag.held.Store(true)
	cfg := f.config()
	cfg.Suspended = flag.suspended
	eng, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(eng.Close)
	f.eng = eng
	if err := f.eng.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}

	pl := f.plan(alice, refRcvFix, `{"name":"a","value":"1","failBefore":true}`)
	if _, err := f.eng.Apply(context.Background(), alice, pl.PlanID, core.ApplyOptions{}); !errors.Is(err, errRecovererSuspended) {
		t.Fatalf("Apply = %v, want the suspended recovery handler's error", err)
	}
	if calls, _, _ := h.recovered(); calls != 0 {
		t.Fatalf("the recovery handler ran %d times while suspended", calls)
	}
	ids := f.count("SELECT count(*) FROM core_changes WHERE state = ? AND recovery_owner = ?",
		core.StateRecovering, core.OwnerModule)
	if ids != 1 {
		t.Fatalf("%d changes recovering and owned by module, want the failed one held there", ids)
	}

	// Held back, not lost: the next start without the flag runs the handler.
	h.script(core.RecoveryOutcome{State: core.RecoveryCompleted}, nil)
	f.eng.Close()
	f.open()
	if err := f.eng.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	var id string
	if err := f.db.Read().QueryRow("SELECT id FROM core_changes WHERE action = ?", refRcvFix.Name).Scan(&id); err != nil {
		t.Fatal(err)
	}
	f.recoveryAwait(id, core.StateCompleted)
	if calls, _, changes := h.recovered(); calls != 1 || !slices.Equal(changes, []string{id}) {
		t.Errorf("the recovery handler ran %d times for %v, want once for %s", calls, changes, id)
	}
}

func TestUnsuspendedEngineIgnoresTheHook(t *testing.T) {
	// The agent passes no Suspended; a nil hook must never hold anything.
	f := newCfxFixture(t)
	if f.ctrl.suspended(context.Background(), "nothing") {
		t.Fatal("an engine without Config.Suspended reports suspended")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mkdir -p /root/.cache/w0c-tmp/12 && TMPDIR=/root/.cache/w0c-tmp/12 GOTOOLCHAIN=local go test ./internal/core/lifecycle/ -run 'TestSuspended|TestUnsuspended'`
Expected: FAIL to compile with `cfg.Suspended undefined (type Config has no field or method Suspended)`, `undefined: errRecovererSuspended` and `f.ctrl.suspended undefined`.

- [ ] **Step 3: Write the implementation**

Create `internal/core/lifecycle/suspend.go`:

```go
package lifecycle

import (
	"context"
	"errors"
)

// errRecovererSuspended answers a recovery handler run while the engine's
// automatic work is suspended. The change stays recovering and owned by
// module, so the next start's recovery runs the handler.
var errRecovererSuspended = errors.New("lifecycle: module recovery handlers are suspended")

// suspended reports whether Config.Suspended holds the engine's automatic
// work back: composite recovery, the composite runners that follow
// children, Wake and module recovery handlers. A restored controller
// database is quarantined until its operator releases it, and none of that
// work may act on its records before then. An error reading the flag counts
// as suspended, because acting on a quarantined record cannot be undone.
func (e *Engine) suspended(ctx context.Context, what string) bool {
	if e.cfg.Suspended == nil {
		return false
	}
	held, err := e.cfg.Suspended(ctx)
	if err != nil {
		e.cfg.Base.Log.Error("lifecycle: the suspension check failed; "+what+" is held back", "error", err)
		return true
	}
	return held
}
```

In `internal/core/lifecycle/engine.go`, in `type Config struct`, after

```go
	// ModuleUnits returns the Env.Units of a handler of module; nil leaves
	// Env.Units nil.
	ModuleUnits func(module string) core.Units
```

add

```go
	// Suspended reports whether composite recovery, the composite runners,
	// Wake and module recovery handlers must do nothing (spec 10, restore
	// quarantine); an error counts as suspended. nil never suspends.
	Suspended func(ctx context.Context) (bool, error)
```

In `internal/core/lifecycle/composite_watch.go`, in `Wake`, replace

```go
	if e.cfg.Side != core.SideController || len(changeIDs) == 0 {
		return
	}
	ctx := context.Background()
```

with

```go
	if e.cfg.Side != core.SideController || len(changeIDs) == 0 {
		return
	}
	ctx := context.Background()
	if e.suspended(ctx, "the child watcher") {
		return
	}
```

In the same file, replace the end of `RecoverComposites`'s doc comment and its opening lines

```go
// left alone. Run it once, after Recover and SetChildren; it returns once the
// runners are started, and CompositesRecovered reports when they settled.
func (e *Engine) RecoverComposites(ctx context.Context) error {
	if _, err := e.kids(); err != nil {
		return err
	}
```

with

```go
// left alone. Run it once, after Recover and SetChildren; it returns once the
// runners are started, and CompositesRecovered reports when they settled.
// While the engine is suspended it claims nothing and starts no runner, and
// CompositesRecovered reports true: a restored database's unfinished
// composites are quarantined, and their operator resolves each one.
func (e *Engine) RecoverComposites(ctx context.Context) error {
	if _, err := e.kids(); err != nil {
		return err
	}
	if e.suspended(ctx, "composite recovery") {
		e.cfg.Base.Log.Warn("lifecycle: composite recovery is suspended; no composite is claimed")
		e.compMu.Lock()
		e.compRecoverStarted = true
		e.compMu.Unlock()
		return nil
	}
```

In `internal/core/lifecycle/composite_run.go`, in `runComposite`, replace

```go
	failures := 0
	for ctx.Err() == nil {
		err := e.compositeStep(ctx, id, run)
```

with

```go
	failures := 0
	for ctx.Err() == nil {
		// A suspended runner leaves the composite as persisted and only
		// checks again at its next poll.
		if e.suspended(ctx, "the composite runner") {
			e.compositeSleep(ctx, wake, ChildPoll)
			continue
		}
		err := e.compositeStep(ctx, id, run)
```

In `internal/core/lifecycle/recoverer.go`, replace

```go
func (e *Engine) runRecoverer(ctx context.Context, changeID string) (core.ChangeView, error) {
	var ref core.ActionRef
```

with

```go
func (e *Engine) runRecoverer(ctx context.Context, changeID string) (core.ChangeView, error) {
	if e.suspended(ctx, "module recovery handlers") {
		return core.ChangeView{}, errRecovererSuspended
	}
	var ref core.ActionRef
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `TMPDIR=/root/.cache/w0c-tmp/12 GOTOOLCHAIN=local go test -count=1 -race -run 'TestSuspended|TestUnsuspended' ./internal/core/lifecycle/ && TMPDIR=/root/.cache/w0c-tmp/12 GOTOOLCHAIN=local go test -count=1 ./internal/core/lifecycle/ && TMPDIR=/root/.cache/w0c-tmp/12 GOTOOLCHAIN=local go vet ./internal/core/lifecycle/ && TMPDIR=/root/.cache/w0c-tmp/12 golangci-lint run --allow-serial-runners ./internal/core/lifecycle/...`
Expected: `ok  	github.com/darkraise/darkpanel/internal/core/lifecycle` twice (the whole package unchanged with a nil hook); vet prints nothing; `0 issues.`

- [ ] **Step 5: Commit**

```bash
gofmt -l internal/core/lifecycle   # prints nothing
git add internal/core/lifecycle/suspend.go internal/core/lifecycle/suspend_test.go internal/core/lifecycle/engine.go internal/core/lifecycle/composite_watch.go internal/core/lifecycle/composite_run.go internal/core/lifecycle/recoverer.go
git commit -m "feat(lifecycle): suspend automatic work on a hook"
rm -rf /root/.cache/w0c-tmp/12
```

### Task 13: Rollout suspension hook

The rollout driver and the update scheduler are two of the automatic mutation sources that restore quarantine suspends (spec 10). This task gives both configs the optional `Suspended` function of C16 and consults it at the top of every timed pass: a pass that finds it true does nothing and re-arms its timer, and an error from it counts as true and is logged once per pass. No other behaviour changes; nil keeps today's behaviour. Test helpers carry the prefix `sus` (`susSwitch`); the existing `newDriverEnv`, `newSchedEnv`, `mustCreate`, `lockedBuffer` and the env methods are used as written.

**Files:**
- Create: `internal/controller/rollout/suspend.go`
- Modify: `internal/controller/rollout/driver.go:50-56` (`DriverConfig`), `internal/controller/rollout/driver.go:121-132` (`tick`)
- Modify: `internal/controller/rollout/scheduler.go:36-49` (`SchedulerConfig` and its comment), `internal/controller/rollout/scheduler.go:118-129` (`tick`)
- Test: `internal/controller/rollout/suspend_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: C16 — `DriverConfig.Suspended` and `SchedulerConfig.Suspended`, `func(ctx context.Context) (bool, error)`, nil meaning never suspended (Task 48 passes `quarantine.Active` on the controller database).

**Items:** 52

**Depends on:** none

**Implementer:** dforge:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 1 = 4

- [ ] **Step 1: Write the failing test**

Create `internal/controller/rollout/suspend_test.go`:

```go
package rollout

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)

// susSwitch is a Suspended function the test flips; err, when set, is what
// every call answers.
type susSwitch struct {
	on  atomic.Bool
	err atomic.Pointer[error]
}

func (s *susSwitch) suspended(context.Context) (bool, error) {
	if err := s.err.Load(); err != nil {
		return false, *err
	}
	return s.on.Load(), nil
}

func (s *susSwitch) fail(err error) { s.err.Store(&err) }

func (s *susSwitch) clear() { s.err.Store(nil) }

func TestDriverSkipsPassesWhileSuspended(t *testing.T) {
	e := newDriverEnv(t)
	r := mustCreate(t, e.db, "1.3.0", "1.2.0", "OWN")
	var sw susSwitch
	sw.on.Store(true)
	var logs lockedBuffer
	d, err := NewDriver(DriverConfig{DB: e.db, Dispatcher: e.fleet, Clock: e.clock, Suspended: sw.suspended,
		Log: slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn}))})
	if err != nil {
		t.Fatal(err)
	}
	d.Start()
	t.Cleanup(d.Stop)

	e.passes(3)
	e.want(t, r.ID, core.RolloutRunning, "OWN=waiting")
	if calls := e.fleet.callsOf("OWN"); len(calls) != 0 {
		t.Fatalf("calls while suspended: %v, want none", calls)
	}

	sw.fail(errors.New("the quarantine flag cannot be read"))
	e.passes(1)
	e.want(t, r.ID, core.RolloutRunning, "OWN=waiting")
	if n := logs.count("the pass is skipped"); n != 1 {
		t.Fatalf("%d skip warnings after a failed read, want 1:\n%s", n, logs.String())
	}

	sw.clear()
	sw.on.Store(false)
	e.passes(1)
	e.want(t, r.ID, core.RolloutRunning, "OWN=updating")
}

func TestSchedulerSkipsPassesWhileSuspended(t *testing.T) {
	e := newSchedEnv(t)
	e.schedule(t, "OWN", core.ChannelStable, "mon", "12:00")
	var sw susSwitch
	sw.on.Store(true)
	var logs lockedBuffer
	s, err := NewScheduler(SchedulerConfig{
		DB: e.db, Clients: e.clients, Index: e.index, Audit: e.audit, Clock: e.clock, Version: "1.2.0",
		Suspended: sw.suspended, Log: slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})),
	})
	if err != nil {
		t.Fatal(err)
	}
	s.Start()
	t.Cleanup(s.Stop)

	e.minutes(3)
	if rs := e.rollouts(t); len(rs) != 0 {
		t.Fatalf("rollouts while suspended: %+v, want none", rs)
	}

	sw.fail(errors.New("the quarantine flag cannot be read"))
	e.minutes(1)
	if rs := e.rollouts(t); len(rs) != 0 {
		t.Fatalf("rollouts after a failed read: %+v, want none", rs)
	}
	if n := logs.count("the pass is skipped"); n != 1 {
		t.Fatalf("%d skip warnings after a failed read, want 1:\n%s", n, logs.String())
	}

	sw.clear()
	sw.on.Store(false)
	e.minutes(1)
	if rs := e.rollouts(t); len(rs) != 1 {
		t.Fatalf("%d rollouts once resumed, want 1", len(rs))
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mkdir -p /root/.cache/w0c-tmp/13 && TMPDIR=/root/.cache/w0c-tmp/13 GOTOOLCHAIN=local go test -count=1 -run Suspended ./internal/controller/rollout/`
Expected: FAIL to build with `unknown field Suspended in struct literal of type DriverConfig` and the same for `SchedulerConfig`.

- [ ] **Step 3: Write the implementation**

Create `internal/controller/rollout/suspend.go`:

```go
package rollout

import (
	"context"
	"log/slog"
)

// suspended asks f whether the controller's automatic mutation sources are
// suspended (restore quarantine, spec 10). A failed read counts as
// suspended: no pass may run on a controller that may be quarantined.
func suspended(ctx context.Context, f func(ctx context.Context) (bool, error), log *slog.Logger, loop string) bool {
	if f == nil {
		return false
	}
	s, err := f(ctx)
	if err != nil {
		log.Warn("rollout: cannot tell whether automatic changes are suspended; the pass is skipped", "loop", loop, "error", err)
		return true
	}
	return s
}
```

In `internal/controller/rollout/driver.go`, add the field at the end of `DriverConfig`, so the struct reads:

```go
// DriverConfig wires a Driver.
type DriverConfig struct {
	DB         *store.DB // the controller database
	Dispatcher Dispatcher
	Clock      core.Clock
	Log        *slog.Logger // nil discards
	// Suspended reports whether automatic changes are suspended (restore
	// quarantine, spec 10): a pass that finds it true does nothing, and an
	// error counts as true. nil is never suspended.
	Suspended func(ctx context.Context) (bool, error)
}
```

and replace `tick` with:

```go
func (d *Driver) tick() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.stopped {
		return
	}
	if !suspended(d.ctx, d.cfg.Suspended, d.cfg.Log, "driver") {
		if err := d.pass(d.ctx); err != nil {
			d.cfg.Log.Warn("rollout: drive the active rollout", "error", err)
		}
	}
	d.timer = d.cfg.Clock.AfterFunc(PollInterval, d.tick)
}
```

In `internal/controller/rollout/scheduler.go`, change the comment above `SchedulerConfig` and add the field at its end:

```go
// SchedulerConfig wires a Scheduler; every field but Log and Suspended is
// required.
type SchedulerConfig struct {
	DB *store.DB // the controller database
	// Clients tells which hosts are online: a host whose client answers an
	// error is offline. The controller passes its session registry.
	Clients dispatch.ClientSource
	Index   ReleaseSource
	// Audit is the controller's audit log; a claim's entry is written in
	// the claim's transaction.
	Audit   core.Journal
	Clock   core.Clock
	Log     *slog.Logger // nil discards
	Version string       // the controller's buildinfo.Version
	// Suspended reports whether automatic changes are suspended (restore
	// quarantine, spec 10): a pass that finds it true does nothing, and an
	// error counts as true. nil is never suspended.
	Suspended func(ctx context.Context) (bool, error)
}
```

and replace `tick` with:

```go
func (s *Scheduler) tick() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return
	}
	if !suspended(s.ctx, s.cfg.Suspended, s.cfg.Log, "scheduler") {
		if err := s.pass(s.ctx); err != nil {
			s.cfg.Log.Warn("rollout: scheduled updates", "error", err)
		}
	}
	s.timer = s.cfg.Clock.AfterFunc(ScheduleInterval, s.tick)
}
```

`NewDriver` and `NewScheduler` stay as they are: `Suspended` is optional.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `TMPDIR=/root/.cache/w0c-tmp/13 GOTOOLCHAIN=local go test -count=1 ./internal/controller/rollout/ && TMPDIR=/root/.cache/w0c-tmp/13 golangci-lint run --allow-serial-runners ./internal/controller/rollout/...`
Expected: `ok  	github.com/darkraise/darkpanel/internal/controller/rollout` and `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add internal/controller/rollout/suspend.go internal/controller/rollout/suspend_test.go internal/controller/rollout/driver.go internal/controller/rollout/scheduler.go
git commit -m "feat(rollout): skip passes while suspended"
rm -rf /root/.cache/w0c-tmp/13
```

### Task 14: Harness lab names, directories, links and addresses

This task adds `test/harness/lab_helpers_test.go`: a `Lab` per harness test that derives, from the test's name, a VM-name prefix (`Slug`), a run directory, a lab index (1 to 254, distinct among the labs of one process) and from it two multicast ports and the MACs and addresses of its VMs. Today TestM1 and TestM2 hard-code `52:54:00:d0:…` MACs, `198.18.0.x` and `2001:db8:d0::x` internet addresses, `10.77.0.x` private addresses and draw their multicast ports from 20000 to 40001 (`test/harness/m1_test.go:113-131`, `test/harness/m2_test.go:136-174`); labs use index octets 1 to 254 and ports from 40100 upwards, so a lab never meets TestM1's or TestM2's links or addresses, and TestM1 and TestM2 stay as they are. Later waves' harness tests (M3–M6) call `NewLab(t)` and build their `vm.Config` values from it. Every link is a QEMU multicast group of its own port (`test/harness/vm/vm.go:132-156`), so two labs can boot at the same time. The tests' helpers carry the prefix `lab` or `mustIndex` (only `mustIndex` is a test helper; the rest are test functions `TestLab*`). The package has the `harness` build tag; its unit tests run without VMs (TestM1 and TestM2 skip without `-os`).

**Files:**
- Create: `test/harness/lab_helpers_test.go`
- Test: `test/harness/lab_test.go`

**Interfaces:**
- Consumes: `vm.Config`, `vm.VM`, `vm.McastGroup` (`test/harness/vm/seed.go:22-69`); `bootVMs` and `workdirFlag` (`test/harness/m1_test.go:24-30`, `:170-190`).
- Produces (for later waves' harness tests; no task of this plan consumes them): `type Lab struct { Slug, Dir string; Index, McastPort, PrivMcastPort int }`; `func NewLab(tb testing.TB) *Lab`; `(*Lab).VMName(role string) string`; `type NIC int` with `NICInet`, `NICMgmt`, `NICPriv`; `(*Lab).MAC(nic NIC, host int) string`; `(*Lab).InetIPv4(host int) string` (`198.18.<Index>.<host>`); `(*Lab).InetIPv6(host int) string` (`2001:db8:d0:<Index hex>::<host hex>`); `(*Lab).InetAddrs(host int) []string` (`/15` and `/64`); `(*Lab).PrivPrefix() netip.Prefix` (`10.77.<Index>.0/24`); `(*Lab).PrivAddr(host int) string`; `(*Lab).Boot(ctx context.Context, t *testing.T, cfgs []vm.Config) []*vm.VM`. Host numbers are 1 to 254; a role is up to 31 lowercase letters, digits and single dashes; anything else panics (a mistake in the test).

**Items:** 52

**Depends on:** none

**Implementer:** dforge:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 1 = 4

- [ ] **Step 1: Write the failing test**

Create `test/harness/lab_test.go`:

```go
//go:build harness

package harness

import (
	"fmt"
	"net"
	"net/netip"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestLabSlug(t *testing.T) {
	hostname := regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	cases := []struct{ name, prefix string }{
		{"TestM3", "testm3-"},
		{"TestM3/refuse_ssh_lockout", "testm3-refuse-ssh-lockou-"},
		{"TestM4/Compose project with a Git include", "testm4-compose-project-w-"},
		{"Test__Odd//Name__", "test-odd-name-"},
		{"/", "t-"},
	}
	seen := map[string]string{}
	for _, c := range cases {
		s := labSlug(c.name)
		if !strings.HasPrefix(s, c.prefix) || len(s) != len(c.prefix)+6 {
			t.Errorf("labSlug(%q) = %q, want %q and six hex digits", c.name, s, c.prefix)
		}
		if !hostname.MatchString(s) || len(s) > labSlugMax+7 {
			t.Errorf("labSlug(%q) = %q is not a short hostname label", c.name, s)
		}
		if s != labSlug(c.name) {
			t.Errorf("labSlug(%q) is not deterministic", c.name)
		}
		if other, dup := seen[s]; dup {
			t.Errorf("labSlug gives %q to both %q and %q", s, other, c.name)
		}
		seen[s] = c.name
	}
	if a, b := labSlug("TestM5/a b"), labSlug("TestM5/a-b"); a == b {
		t.Errorf("names that read alike share the slug %q", a)
	}
}

func TestLabAllocator(t *testing.T) {
	a := newLabAllocator(labPortMin)
	dir := t.TempDir()
	first, err := a.lab("TestM3", dir)
	if err != nil {
		t.Fatal(err)
	}
	again, err := a.lab("TestM3", dir)
	if err != nil || again.Index != first.Index || again.McastPort != first.McastPort {
		t.Errorf("a second lab of one name = %+v, %v; want index %d again", again, err, first.Index)
	}
	if want := filepath.Join(dir, first.Slug); first.Dir != want {
		t.Errorf("Dir = %q, want %q", first.Dir, want)
	}
	if first.PrivMcastPort != first.McastPort+1 || first.McastPort != labPortMin+2*first.Index {
		t.Errorf("ports %d and %d, want %d and the next", first.McastPort, first.PrivMcastPort, labPortMin+2*first.Index)
	}
	indexes := map[int]bool{first.Index: true}
	ports := map[int]bool{first.McastPort: true, first.PrivMcastPort: true}
	for i := 1; i < labIndexes; i++ {
		l, err := a.lab(fmt.Sprintf("TestLab/%d", i), dir)
		if err != nil {
			t.Fatalf("lab %d: %v", i, err)
		}
		if l.Index < 1 || l.Index > labIndexes || indexes[l.Index] {
			t.Fatalf("lab %d got index %d, used or out of range", i, l.Index)
		}
		indexes[l.Index] = true
		if ports[l.McastPort] || ports[l.PrivMcastPort] || l.PrivMcastPort > 65535 {
			t.Fatalf("lab %d got ports %d and %d, used or out of range", i, l.McastPort, l.PrivMcastPort)
		}
		ports[l.McastPort], ports[l.PrivMcastPort] = true, true
	}
	if _, err := a.lab("TestLab/one-too-many", dir); err == nil {
		t.Error("a lab past the last index was handed out")
	}
	if b := newLabAllocator(labPortMin); mustIndex(t, b, "TestM3") != first.Index {
		t.Error("a name's first index depends on more than the name")
	}
	if labPortMin <= 40001 || labPortMin+labPortSpan+2*labIndexes+1 > 65535 {
		t.Errorf("lab ports [%d, %d] overlap TestM1's and TestM2's or leave the port range", labPortMin, labPortMin+labPortSpan+2*labIndexes+1)
	}
}

func mustIndex(t *testing.T, a *labAllocator, name string) int {
	t.Helper()
	i, err := a.index(name)
	if err != nil {
		t.Fatal(err)
	}
	return i
}

func TestLabAddresses(t *testing.T) {
	inet4 := netip.MustParsePrefix("198.18.0.0/15")
	inet6 := netip.MustParsePrefix("2001:db8:d0::/48")
	priv := netip.MustParsePrefix("10.77.0.0/16")
	m2priv := netip.MustParsePrefix("10.77.0.0/24")
	for _, index := range []int{1, 16, 254} {
		l := &Lab{Slug: "testm3-abcdef", Index: index}
		macs := map[string]bool{}
		for _, nic := range []NIC{NICInet, NICMgmt, NICPriv} {
			for _, host := range []int{1, 10, 254} {
				m := l.MAC(nic, host)
				hw, err := net.ParseMAC(m)
				if err != nil || !strings.HasPrefix(m, "52:54:00:") || hw[0]&1 != 0 || macs[m] {
					t.Errorf("index %d: MAC(%d, %d) = %q is invalid, multicast or repeated", index, nic, host, m)
				}
				macs[m] = true
			}
		}
		a4, err := netip.ParseAddr(l.InetIPv4(10))
		if err != nil || !inet4.Contains(a4) {
			t.Errorf("index %d: InetIPv4 = %q, want an address in %s", index, l.InetIPv4(10), inet4)
		}
		a6, err := netip.ParseAddr(l.InetIPv6(10))
		if err != nil || !inet6.Contains(a6) || netip.PrefixFrom(a6, 64).Masked() == netip.MustParsePrefix("2001:db8:d0::/64") {
			t.Errorf("index %d: InetIPv6 = %q, want an address in %s outside M1's /64", index, l.InetIPv6(10), inet6)
		}
		if got := l.InetAddrs(10); len(got) != 2 || got[0] != l.InetIPv4(10)+"/15" || got[1] != l.InetIPv6(10)+"/64" {
			t.Errorf("index %d: InetAddrs = %v", index, got)
		}
		p := l.PrivPrefix()
		if !priv.Contains(p.Addr()) || p.Bits() != 24 || p.Overlaps(m2priv) {
			t.Errorf("index %d: PrivPrefix = %s, want a /24 in %s apart from TestM2's %s", index, p, priv, m2priv)
		}
		if a, err := netip.ParseAddr(l.PrivAddr(11)); err != nil || !p.Contains(a) {
			t.Errorf("index %d: PrivAddr = %q, want an address in %s", index, l.PrivAddr(11), p)
		}
		if got := l.VMName("scanner"); got != "testm3-abcdef-scanner" {
			t.Errorf("VMName = %q", got)
		}
	}
	for _, bad := range []func(l *Lab){
		func(l *Lab) { l.MAC(NICInet, 0) },
		func(l *Lab) { l.PrivAddr(255) },
		func(l *Lab) { l.VMName("Target") },
		func(l *Lab) { l.VMName("a--b") },
		func(l *Lab) { l.VMName(strings.Repeat("a", labRoleMax+1)) },
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Error("an invalid host number or role did not panic")
				}
			}()
			bad(&Lab{Slug: "t-000000", Index: 1})
		}()
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mkdir -p /root/.cache/w0c-tmp/14 && TMPDIR=/root/.cache/w0c-tmp/14 GOTOOLCHAIN=local go test -count=1 -tags harness -run 'TestLab' ./test/harness/`
Expected: FAIL, the package does not compile: `undefined: labSlug` (and `newLabAllocator`, `Lab`, `NIC`).

- [ ] **Step 3: Write the implementation**

Create `test/harness/lab_helpers_test.go`:

```go
//go:build harness

package harness

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/rand/v2"
	"net/netip"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/darkraise/darkpanel/test/harness/vm"
)

// labIndexes is how many labs one process can hand out: an index fills one
// octet of the lab's addresses, and 0 and 255 stay unused.
const labIndexes = 254

// labRole is a VM role: the hostname suffix after the lab's slug.
var labRole = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// labRoleMax keeps a VM name, slug and role, within a 63-byte hostname.
const labRoleMax = 31

// labSlugMax bounds the readable part of a lab's slug, so a VM name built
// from it stays a short hostname.
const labSlugMax = 24

// The multicast ports of the labs: a process picks a random even base in
// [labPortMin, labPortMin+labPortSpan), above the ports TestM1 and TestM2
// draw (20000 to 40001), and lab i uses base+2i and base+2i+1.
const (
	labPortMin  = 40100
	labPortSpan = 9500
)

// NIC names one of a VM's network interfaces in a lab MAC address.
type NIC int

// The interfaces of a harness VM (vm.Config).
const (
	NICInet NIC = iota // the "internet" link
	NICMgmt            // the user-mode management NIC
	NICPriv            // the private link that stands in for the tailnet
)

// Lab is one harness test's share of the runner: a slug that prefixes its
// VM names, a run directory, and multicast links and addresses that no
// other lab of the process uses, all derived from the test's name. Two labs
// may boot VMs at the same time without sharing a link, a file or an
// address.
type Lab struct {
	Slug          string // a lowercase form of the test name, then six hex digits of its hash
	Dir           string // the run directory: -workdir or a test temporary directory, then Slug
	Index         int    // 1 to 254, distinct among the labs of the process
	McastPort     int    // the internet link
	PrivMcastPort int    // the private link
}

// labAllocator hands out lab indexes. An index starts at a hash of the test
// name and moves to the next free one on a collision, so a name keeps its
// index within a process and two names never share one.
type labAllocator struct {
	mu     sync.Mutex
	base   int // the internet link port of index 0
	byName map[string]int
	used   [labIndexes + 1]bool
}

func newLabAllocator(base int) *labAllocator {
	return &labAllocator{base: base, byName: map[string]int{}}
}

var labs = newLabAllocator(labPortMin + 2*rand.IntN(labPortSpan/2)) //nolint:gosec // G404: a link port, not a secret

// index returns name's lab index.
func (a *labAllocator) index(name string) (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if i, ok := a.byName[name]; ok {
		return i, nil
	}
	sum := sha256.Sum256([]byte(name))
	start := int(binary.BigEndian.Uint16(sum[3:5])) % labIndexes
	for k := range labIndexes {
		i := (start+k)%labIndexes + 1
		if !a.used[i] {
			a.used[i] = true
			a.byName[name] = i
			return i, nil
		}
	}
	return 0, fmt.Errorf("harness: all %d lab indexes are in use", labIndexes)
}

// lab returns the lab of the test name with its run directory under base.
func (a *labAllocator) lab(name, base string) (*Lab, error) {
	i, err := a.index(name)
	if err != nil {
		return nil, err
	}
	slug := labSlug(name)
	return &Lab{
		Slug: slug, Dir: filepath.Join(base, slug), Index: i,
		McastPort: a.base + 2*i, PrivMcastPort: a.base + 2*i + 1,
	}, nil
}

// labSlug is name in lowercase letters, digits and single dashes, at most
// labSlugMax of them, then a dash and six hex digits of name's SHA-256, so
// two names that read alike still get distinct slugs.
func labSlug(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case !dash && b.Len() > 0:
			b.WriteByte('-')
			dash = true
		}
	}
	s := b.String()
	if len(s) > labSlugMax {
		s = s[:labSlugMax]
	}
	s = strings.TrimSuffix(s, "-")
	if s == "" {
		s = "t"
	}
	sum := sha256.Sum256([]byte(name))
	return s + "-" + hex.EncodeToString(sum[:3])
}

// NewLab returns tb's lab. Its run directory is under -workdir when given,
// so the workflow keeps the logs, and under a test temporary directory
// otherwise.
func NewLab(tb testing.TB) *Lab {
	tb.Helper()
	base := *workdirFlag
	if base == "" {
		base = tb.TempDir()
	}
	l, err := labs.lab(tb.Name(), base)
	if err != nil {
		tb.Fatal(err)
	}
	tb.Logf("lab %s: index %d, run directory %s, links %s:%d and %s:%d",
		l.Slug, l.Index, l.Dir, vm.McastGroup, l.McastPort, vm.McastGroup, l.PrivMcastPort)
	return l
}

// checkHost panics on a host number outside 1 to 254: a caller passing one
// is a mistake in the test, not a run-time condition.
func checkHost(host int) {
	if host < 1 || host > 254 {
		panic(fmt.Sprintf("harness: lab host number %d is outside 1 to 254", host))
	}
}

// VMName is the name of the lab's VM with role, such as target or scanner:
// its hostname and the prefix of its files in Dir.
func (l *Lab) VMName(role string) string {
	if len(role) > labRoleMax || !labRole.MatchString(role) {
		panic(fmt.Sprintf("harness: VM role %q is not up to %d lowercase letters, digits and single dashes", role, labRoleMax))
	}
	return l.Slug + "-" + role
}

// MAC is the address of host's nic in the lab, in QEMU's 52:54:00 range.
func (l *Lab) MAC(nic NIC, host int) string {
	checkHost(host)
	return fmt.Sprintf("52:54:00:%02x:%02x:%02x", l.Index, int(nic), host)
}

// InetIPv4 is host's address on the lab's internet link, outside every
// private range like M1's 198.18.0.0/15.
func (l *Lab) InetIPv4(host int) string {
	checkHost(host)
	return fmt.Sprintf("198.18.%d.%d", l.Index, host)
}

// InetIPv6 is host's IPv6 address on the lab's internet link.
func (l *Lab) InetIPv6(host int) string {
	checkHost(host)
	return fmt.Sprintf("2001:db8:d0:%x::%x", l.Index, host)
}

// InetAddrs are host's internet-link addresses in CIDR form, for
// vm.Config.InetAddrs.
func (l *Lab) InetAddrs(host int) []string {
	return []string{l.InetIPv4(host) + "/15", l.InetIPv6(host) + "/64"}
}

// PrivPrefix is the lab's private-link range. TestM2 keeps 10.77.0.0/24.
func (l *Lab) PrivPrefix() netip.Prefix {
	return netip.MustParsePrefix(fmt.Sprintf("10.77.%d.0/24", l.Index))
}

// PrivAddr is host's private-link address.
func (l *Lab) PrivAddr(host int) string {
	checkHost(host)
	return fmt.Sprintf("10.77.%d.%d", l.Index, host)
}

// Boot boots cfgs in the lab's run directory within the VM limit and stops
// each VM when the test ends.
func (l *Lab) Boot(ctx context.Context, t *testing.T, cfgs []vm.Config) []*vm.VM {
	t.Helper()
	for i := range cfgs {
		cfgs[i].Dir = l.Dir
	}
	return bootVMs(ctx, t, l.Dir, cfgs)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `mkdir -p /root/.cache/w0c-tmp/14 && TMPDIR=/root/.cache/w0c-tmp/14 GOTOOLCHAIN=local go test -count=1 -tags harness -run 'TestLab' -v ./test/harness/`
Expected: PASS for `TestLabSlug`, `TestLabAllocator` and `TestLabAddresses`.

Then the whole package, vet and lint (the repository's `.golangci.yml` already sets the `harness` build tag):

Run: `mkdir -p /root/.cache/w0c-tmp/14 && TMPDIR=/root/.cache/w0c-tmp/14 GOTOOLCHAIN=local go test -count=1 -tags harness  ./test/harness/ && TMPDIR=/root/.cache/w0c-tmp/14 GOTOOLCHAIN=local go vet -tags harness ./test/... && TMPDIR=/root/.cache/w0c-tmp/14 golangci-lint run --allow-serial-runners ./test/harness/...`
Expected: `ok  	github.com/darkraise/darkpanel/test/harness`, no vet output, `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add test/harness/lab_helpers_test.go test/harness/lab_test.go
git commit -m "test(harness): derive per-test lab names and links"
```

Empty `/root/.cache/w0c-tmp/14` when the task ends.

### Task 15: Harness pins for the Compose plugin

This task pins Docker's Compose plugin (`docker-compose-plugin`, which installs `/usr/libexec/docker/cli-plugins/docker-compose`, the path the spec's Versions table names) on every target of every OS, the same way the harness pins Docker Engine: by exact version, pool URL and SHA-256 in `test/harness/images.json`, fetched and checked on the VM by the provisioning script's `fetch` (`test/harness/provision.go:21-33`), which downloads each pinned `.deb` over the VM's user-mode management NIC. The M4 harness test of wave 3a runs Compose projects on the enrolled agent VM; installing it on every target is one 7.7 MB package. The plugin's `.deb` has no `Depends` (checked with `dpkg-deb -f … Depends` on 2026-10-23). The pinned version is 5.6.0 (published 2026-10-02, 21 days before this plan), not 5.6.1 (published 2026-10-21): the harness pins nothing younger than two weeks. Values fetched from `https://download.docker.com/linux/<family>/dists/<codename>/pool/stable/amd64/` on 2026-10-23 and hashed with `sha256sum`. No new test helper.

**Files:**
- Modify: `test/harness/pins.go:24-27` (`RequiredPackages`)
- Modify: `test/harness/images.json` (one package entry per OS)
- Modify: `test/harness/provision.go:55-60` (`TargetScript`'s comment)
- Test: `test/harness/pins_test.go`, `test/harness/provision_test.go`

**Interfaces:**
- Consumes: none.
- Produces: `RequiredPackages` gains `docker-compose-plugin` after `containerd.io`; every harness target has the Compose plugin.

**Items:** 52

**Depends on:** none

**Implementer:** dforge:impl-sonnet-high
**Evaluation:** files 2 - spec 0 - coupling 1 - risk 2 = 5

- [ ] **Step 1: Write the failing tests**

In `test/harness/pins_test.go`, `validOS`'s package list becomes:

```go
		Packages: []Package{
			pkg("docker-ce", "5:29.9.1-1~debian.13~trixie"), pkg("docker-ce-cli", "5:29.9.1-1~debian.13~trixie"),
			pkg("containerd.io", "2.4.2-2~debian.13~trixie"), pkg("docker-compose-plugin", "5.6.0-1~debian.13~trixie"),
			pkg("tailscale", "1.104.1"),
		},
```

and in `TestPinChecks` three cases change (the shell-in-a-version case keeps editing the last package, and the missing-package case drops it):

```go
		{"shell in a version", func(p *Pins) { p.OS[0].Packages[4].Version = "1.0'; reboot; '" }, "not an apt version"},
```

```go
		{"missing package", func(p *Pins) { p.OS[0].Packages = p.OS[0].Packages[:4] }, "want [docker-ce docker-ce-cli containerd.io docker-compose-plugin tailscale]"},
		{"packages out of order", func(p *Pins) {
			p.OS[0].Packages[0], p.OS[0].Packages[1] = p.OS[0].Packages[1], p.OS[0].Packages[0]
		}, "want [docker-ce docker-ce-cli containerd.io docker-compose-plugin tailscale]"},
```

In `test/harness/provision_test.go`, `TestTargetScript`'s expected install line becomes:

```go
		install := `apt_get install --no-install-recommends "$pkgdir/docker-ce.deb" "$pkgdir/docker-ce-cli.deb" ` +
			`"$pkgdir/containerd.io.deb" "$pkgdir/docker-compose-plugin.deb" "$pkgdir/tailscale.deb"`
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mkdir -p /root/.cache/w0c-tmp/15 && TMPDIR=/root/.cache/w0c-tmp/15 GOTOOLCHAIN=local go test -count=1 -tags harness -run 'TestPinChecks|TestTargetScript|TestLoadPins' ./test/harness/`
Expected: FAIL: `TestPinChecks` reports `valid: OS debian-13: packages [docker-ce docker-ce-cli containerd.io docker-compose-plugin tailscale], want [docker-ce docker-ce-cli containerd.io tailscale]`, and `TestTargetScript` reports the script lacks the install line with `docker-compose-plugin.deb` (`testPackages` builds its packages from `RequiredPackages`, which does not list the plugin yet).

- [ ] **Step 3: Write the implementation**

In `test/harness/pins.go` replace:

```go
// RequiredPackages are the packages every OS pins, in the order the pin file
// lists them: Docker Engine from Docker's apt repository and tailscaled from
// Tailscale's stable repository.
var RequiredPackages = []string{"docker-ce", "docker-ce-cli", "containerd.io", "tailscale"}
```

with:

```go
// RequiredPackages are the packages every OS pins, in the order the pin file
// lists them: Docker Engine and its Compose plugin from Docker's apt
// repository, and tailscaled from Tailscale's stable repository.
var RequiredPackages = []string{"docker-ce", "docker-ce-cli", "containerd.io", "docker-compose-plugin", "tailscale"}
```

In `test/harness/images.json`, insert in each OS's `packages` array, directly after its `containerd.io` entry and before its `tailscale` entry (same two-space indentation as the file):

For `ubuntu-22.04`:

```json
        {
          "name": "docker-compose-plugin",
          "version": "5.6.0-1~ubuntu.22.04~jammy",
          "url": "https://download.docker.com/linux/ubuntu/dists/jammy/pool/stable/amd64/docker-compose-plugin_5.6.0-1~ubuntu.22.04~jammy_amd64.deb",
          "sha256": "2389d885e90df60ef9dcee0e1fae16b70ffa392d6f29d742469aa8ea63eb1daa"
        },
```

For `ubuntu-24.04`:

```json
        {
          "name": "docker-compose-plugin",
          "version": "5.6.0-1~ubuntu.24.04~noble",
          "url": "https://download.docker.com/linux/ubuntu/dists/noble/pool/stable/amd64/docker-compose-plugin_5.6.0-1~ubuntu.24.04~noble_amd64.deb",
          "sha256": "132266d499d9873aafcbe5b515997b8abd714914fa32a62ccac705c1ede7ca81"
        },
```

For `debian-12`:

```json
        {
          "name": "docker-compose-plugin",
          "version": "5.6.0-1~debian.12~bookworm",
          "url": "https://download.docker.com/linux/debian/dists/bookworm/pool/stable/amd64/docker-compose-plugin_5.6.0-1~debian.12~bookworm_amd64.deb",
          "sha256": "b3e0c6c950f7a08155e65a318ced8cf2314b7fdc0617cb807283bcef3d411c40"
        },
```

For `debian-13`:

```json
        {
          "name": "docker-compose-plugin",
          "version": "5.6.0-1~debian.13~trixie",
          "url": "https://download.docker.com/linux/debian/dists/trixie/pool/stable/amd64/docker-compose-plugin_5.6.0-1~debian.13~trixie_amd64.deb",
          "sha256": "500327ad3ca6c52f6d142225c7e6830c46a27ff0348a2b2291f0a143d99872ac"
        },
```

In `test/harness/provision.go`, `TargetScript`'s comment line

```go
// databases, and Docker Engine and tailscaled from o's pinned .deb files.
```

becomes:

```go
// databases, and Docker Engine with its Compose plugin and tailscaled from
// o's pinned .deb files.
```

The template needs no change: it fetches and installs every entry of `.Packages`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `mkdir -p /root/.cache/w0c-tmp/15 && TMPDIR=/root/.cache/w0c-tmp/15 GOTOOLCHAIN=local go test -count=1 -tags harness  ./test/harness/ && TMPDIR=/root/.cache/w0c-tmp/15 GOTOOLCHAIN=local go vet -tags harness ./test/... && TMPDIR=/root/.cache/w0c-tmp/15 golangci-lint run --allow-serial-runners ./test/harness/...`
Expected: `ok  	github.com/darkraise/darkpanel/test/harness` (`TestPinFile` loads the edited `images.json`), no vet output, `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add test/harness/pins.go test/harness/images.json test/harness/provision.go test/harness/pins_test.go test/harness/provision_test.go
git commit -m "test(harness): pin the Docker Compose plugin"
```

Gate 7 applies: after the branch is pushed, run `gh workflow run harness.yml --ref "$(git branch --show-current)"` and `gh run watch`; every OS job provisions its targets with the plugin, so TestM1 and TestM2 green on all four OS prove the pins (a moved or changed `.deb` fails provisioning with `pinned package docker-compose-plugin=… missing` or `… has SHA-256 …`). The `actions/cache` key hashes `images.json`, so the first run re-downloads the cloud images. Empty `/root/.cache/w0c-tmp/15` when the task ends.

### Task 16: Web accessibility and browser test dependencies

Adds the two web development dependencies of section 11: `vitest-axe`, registered once in the test setup so every later web test can assert `toHaveNoViolations()`, and `@playwright/test`, installed for the accessibility walk-through of wave 9b and not used by any test yet. This task alone edits `web/package.json` and `web/pnpm-lock.yaml` (Global Constraints). Its smoke test lives in `web/src/test/axe.test.tsx` and names its form controls `axe-*`; it declares no shared helper.

**Files:**
- Modify: `web/package.json` (devDependencies, through `pnpm add`)
- Modify: `web/pnpm-lock.yaml` (through `pnpm add`)
- Modify: `web/src/test/setup.ts:1-4` (imports)
- Test: `web/src/test/axe.test.tsx` (new)

**Interfaces:**
- Consumes: nothing.
- Produces: C6 — the devDependencies `vitest-axe` `1.0.0-pre.5` and `@playwright/test` `1.64.0`, and the `toHaveNoViolations` matcher registered in `web/src/test/setup.ts`, typed through `vitest-axe/extend-expect`'s module augmentation.

**Items:** 52

**Depends on:** none

**Implementer:** dforge:impl-sonnet-medium
**Evaluation:** files 1 - spec 1 - coupling 0 - risk 1 = 3

- [ ] **Step 1: Write the failing test**

Create `web/src/test/axe.test.tsx`:

```tsx
import { render } from "@testing-library/react"
import { expect, test } from "vitest"
import { axe } from "vitest-axe"

test("axe passes a labelled control", async () => {
  const { container } = render(
    <form>
      <label htmlFor="axe-name">Name</label>
      <input id="axe-name" />
      <button type="submit">Save</button>
    </form>,
  )
  expect(await axe(container)).toHaveNoViolations()
})

test("axe reports an unlabelled control", async () => {
  const { container } = render(
    <form>
      <input />
    </form>,
  )
  const results = await axe(container)
  expect(() => expect(results).toHaveNoViolations()).toThrow(/label/)
})
```

- [ ] **Step 2: Run it to make sure it fails**

Run: `mkdir -p /root/.cache/w0c-tmp/16 && TMPDIR=/root/.cache/w0c-tmp/16 pnpm -C web vitest run src/test/axe.test.tsx`
Expected: FAIL — the suite cannot resolve the import `vitest-axe` (`Failed to resolve import "vitest-axe"`).

- [ ] **Step 3: Add the dependencies**

Run from the repository root:

```bash
TMPDIR=/root/.cache/w0c-tmp/16 pnpm -C web add -D --save-exact vitest-axe@1.0.0-pre.5 @playwright/test@1.64.0
```

Expected output ends with `+ @playwright/test 1.64.0 (1.64.1 is available)` and `+ vitest-axe 1.0.0-pre.5`, and starts with `Lockfile passes supply-chain policies`. `git diff --stat web/` then shows exactly two files: `web/package.json` gains the two lines `"@playwright/test": "1.64.0",` (after `"@eslint/js"`) and `"vitest-axe": "1.0.0-pre.5"` (after `"vitest"`), and `web/pnpm-lock.yaml` gains about 74 lines: the two importer entries plus the packages `@playwright/test@1.64.0`, `playwright@1.64.0`, `playwright-core@1.64.0`, `vitest-axe@1.0.0-pre.5`, `axe-core@4.14.0`, `chalk@5.6.2`, `lodash-es@4.18.1`, `tinyrainbow@2.0.0` and `@vitest/pretty-format@3.2.7`. `web/pnpm-workspace.yaml` does not change: neither package needs a build script, and `@playwright/test` downloads no browser at install (browsers are fetched only by `playwright install`, which nothing runs). If the output differs (another version resolved, a build-script prompt, a supply-chain refusal), stop and report it.

- [ ] **Step 4: Register the matchers**

In `web/src/test/setup.ts`, replace

```ts
import { afterEach, vi } from "vitest"
```

with

```ts
import { afterEach, vi } from "vitest"
import "vitest-axe/extend-expect"
```

- [ ] **Step 5: Run the tests and the checks**

Run: `TMPDIR=/root/.cache/w0c-tmp/16 pnpm -C web vitest run src/test/axe.test.tsx`
Expected: PASS, `Tests  2 passed (2)`.

Run: `rm -f web/node_modules/.stamp && TMPDIR=/root/.cache/w0c-tmp/16 make lint-web test-web`
Expected: `pnpm -C web install --frozen-lockfile` runs and the stamp is touched, ESLint and both `tsc` runs print no findings, and the Vitest `Tests` line reports every test passed with none failed.

- [ ] **Step 6: Commit**

```bash
git add web/package.json web/pnpm-lock.yaml web/src/test/setup.ts web/src/test/axe.test.tsx
git commit -m "chore(web): add vitest-axe and playwright"
rm -rf /root/.cache/w0c-tmp/16
```

### Task 17: Harness VM concurrency limit

This task adds a process-wide limit on how many VMs the harness runs at once (`-vms`, default 3) and makes `bootVMs`, the one boot path of TestM1 and TestM2 (and of `Lab.Boot`), take all its slots before it boots and give them back after the VMs are shut down. Today nothing bounds it: each test boots its VMs in parallel (`test/harness/m1_test.go:170-190`). The GitHub `ubuntu-24.04` runner (4 vCPUs, 16 GiB) runs TestM2's three VMs (2 + 2 + 2 GiB) at once, so the default of 3 keeps TestM1 (2 VMs) and TestM2 (3 VMs) working unchanged; the workflow passes no `-vms`, so `.github/workflows/harness.yml` needs no edit. A boot takes its slots all at once, so two parallel tests can never each hold part of what the other needs. The limiter is in its own file so this task shares no file with Task 14. Test function: `TestVMLimiter`; no new test helper.

**Files:**
- Create: `test/harness/vmlimit.go`
- Modify: `test/harness/m1_test.go:170-176` (`bootVMs`)
- Test: `test/harness/vmlimit_test.go`

**Interfaces:**
- Consumes: none.
- Produces: the flag `-vms`; `bootVMs` (unchanged signature) now waits for VM slots. Later waves' harness tests consume it only through `bootVMs` and `Lab.Boot`.

**Items:** 52

**Depends on:** none

**Implementer:** dforge:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 3 = 5

- [ ] **Step 1: Write the failing test**

Create `test/harness/vmlimit_test.go`:

```go
//go:build harness

package harness

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestVMLimiter(t *testing.T) {
	ctx := context.Background()
	l := newVMLimiter(3)
	if err := l.acquire(ctx, 4); err == nil || !strings.Contains(err.Error(), "-vms allows 3") {
		t.Errorf("acquire past the limit: %v, want a refusal naming -vms", err)
	}
	if err := l.acquire(ctx, 2); err != nil {
		t.Fatal(err)
	}
	got := make(chan error, 1)
	go func() { got <- l.acquire(ctx, 2) }()
	select {
	case err := <-got:
		t.Fatalf("a boot past the free slots did not wait: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	l.release(2)
	select {
	case err := <-got:
		if err != nil {
			t.Fatalf("the waiting boot failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the waiting boot did not take the released slots")
	}
	short, cancel := context.WithCancel(ctx)
	cancel()
	if err := l.acquire(short, 2); !errors.Is(err, context.Canceled) {
		t.Errorf("acquire with an ended context: %v, want context.Canceled", err)
	}
	if err := l.acquire(ctx, 1); err != nil {
		t.Errorf("the last free slot: %v", err)
	}
	l.release(3)
	defer func() {
		if recover() == nil {
			t.Error("releasing more than acquired did not panic")
		}
	}()
	l.release(1)
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mkdir -p /root/.cache/w0c-tmp/17 && TMPDIR=/root/.cache/w0c-tmp/17 GOTOOLCHAIN=local go test -count=1 -tags harness -run TestVMLimiter ./test/harness/`
Expected: FAIL, the package does not compile: `undefined: newVMLimiter`.

- [ ] **Step 3: Write the implementation**

Create `test/harness/vmlimit.go`:

```go
//go:build harness

package harness

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"sync"
)

var vmsFlag = flag.Int("vms", 3, "most VMs the harness runs at once; a test that boots more fails")

// vmLimiter bounds how many VMs run at once. A boot takes all its slots at
// once, so two tests that each wait for a part of their VMs cannot hold
// each other's slots forever.
type vmLimiter struct {
	mu    sync.Mutex
	limit int
	used  int
	wake  chan struct{} // closed and replaced whenever slots are released
}

func newVMLimiter(limit int) *vmLimiter {
	return &vmLimiter{limit: limit, wake: make(chan struct{})}
}

// acquire takes n slots, waiting until they are free or ctx ends. More
// slots than the limit can never be free, so that fails at once.
func (l *vmLimiter) acquire(ctx context.Context, n int) error {
	if n > l.limit {
		return fmt.Errorf("harness: %d VMs at once, but -vms allows %d", n, l.limit)
	}
	for {
		l.mu.Lock()
		if l.used+n <= l.limit {
			l.used += n
			l.mu.Unlock()
			return nil
		}
		wake := l.wake
		l.mu.Unlock()
		select {
		case <-ctx.Done():
			return fmt.Errorf("harness: waiting for %d VM slots: %w", n, ctx.Err())
		case <-wake:
		}
	}
}

// release returns n slots.
func (l *vmLimiter) release(n int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.used -= n
	if l.used < 0 {
		panic(errors.New("harness: more VM slots released than acquired"))
	}
	close(l.wake)
	l.wake = make(chan struct{})
}

var (
	vmSlotsOnce sync.Once
	vmSlotsLim  *vmLimiter
)

// vmSlots is the process's VM limiter, sized by -vms, which is parsed by
// the time a test runs.
func vmSlots() *vmLimiter {
	vmSlotsOnce.Do(func() { vmSlotsLim = newVMLimiter(max(*vmsFlag, 1)) })
	return vmSlotsLim
}
```

In `test/harness/m1_test.go`, replace the head of `bootVMs`:

```go
// bootVMs boots cfgs in parallel and stops each VM when the test ends. Any
// failed boot fails the test, naming dir, where the logs stay.
func bootVMs(ctx context.Context, t *testing.T, dir string, cfgs []vm.Config) []*vm.VM {
	t.Helper()
	vms := make([]*vm.VM, len(cfgs))
```

with:

```go
// bootVMs boots cfgs in parallel, once the VM limit (-vms) has room for all
// of them, and stops each VM when the test ends, before giving its slots
// back. Any failed boot fails the test, naming dir, where the logs stay.
func bootVMs(ctx context.Context, t *testing.T, dir string, cfgs []vm.Config) []*vm.VM {
	t.Helper()
	if err := vmSlots().acquire(ctx, len(cfgs)); err != nil {
		t.Fatal(err)
	}
	// Registered before the VMs' Shutdown, so it runs after them.
	t.Cleanup(func() { vmSlots().release(len(cfgs)) })
	vms := make([]*vm.VM, len(cfgs))
```

The rest of `bootVMs` stays as it is: its `t.Cleanup(v.Shutdown)` calls are registered later, so they run first (cleanups run last-in, first-out).

- [ ] **Step 4: Run the tests to verify they pass**

Run: `mkdir -p /root/.cache/w0c-tmp/17 && TMPDIR=/root/.cache/w0c-tmp/17 GOTOOLCHAIN=local go test -count=1 -tags harness -run TestVMLimiter -race -v ./test/harness/`
Expected: PASS for `TestVMLimiter`, no race report.

Run: `mkdir -p /root/.cache/w0c-tmp/17 && TMPDIR=/root/.cache/w0c-tmp/17 GOTOOLCHAIN=local go test -count=1 -tags harness  ./test/harness/ && TMPDIR=/root/.cache/w0c-tmp/17 GOTOOLCHAIN=local go vet -tags harness ./test/... && TMPDIR=/root/.cache/w0c-tmp/17 golangci-lint run --allow-serial-runners ./test/harness/...`
Expected: `ok  	github.com/darkraise/darkpanel/test/harness`, no vet output, `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add test/harness/vmlimit.go test/harness/vmlimit_test.go test/harness/m1_test.go
git commit -m "test(harness): bound how many VMs run at once"
```

This task touches `test/harness/`, so gate 7 applies: after the branch is pushed, `gh workflow run harness.yml --ref "$(git branch --show-current)"` and `gh run watch` must end green on all four OS jobs (TestM1 and TestM2 boot through the limiter). Empty `/root/.cache/w0c-tmp/17` when the task ends.

### Task 18: Runtime listener migration

Controller core migration `0012_listeners.sql` creates `core_listeners` (C9), the durable record of every runtime listener (spec 1.10): its address, allowed peer, client header, public host, the change that opened it and its state `open | revoked | closed`. The table is `STRICT` like every core table since `0009`, checks its header and state lists and its port range, and has an index for the start-up query "the open listeners, oldest first". It is expand-only (ADR 0005). The migration test migrates a database built to `0011` and checks that `0012_listeners` runs next; the existing `TestCoreMigrationsApply` gains the table. Test helpers: none new (the test uses `openTemp`, `migrateOpts`, `coreSetUpTo`).

**Files:**
- Create: `internal/core/store/migrations/controller/0012_listeners.sql`
- Test: `internal/core/store/migrations_test.go` (line 21: the controller table list; append one test)

**Interfaces:**
- Consumes: none (the CHECK lists spell C3's header and state values).
- Produces: C9's table `core_listeners`.

**Items:** 51

**Depends on:** none

**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6
**Lenses:** test-quality

- [ ] **Step 1: Write the failing test**

In `internal/core/store/migrations_test.go`, `TestCoreMigrationsApply`, change the controller row's last line from

```go
			"core_effects", "core_lock_epochs", "core_composites", "core_composite_steps"},
```

(the first of the two lines with that text, inside the controller entry at line 21) to

```go
			"core_effects", "core_lock_epochs", "core_composites", "core_composite_steps", "core_listeners"},
```

and append (the file already imports `context`, `strconv`, `strings`, `testing` and `time`):

```go
func TestControllerListenersMigration(t *testing.T) {
	ctx := context.Background()
	db := openTemp(t)
	opts := migrateOpts(t, "w0b", time.Now())
	opts.Side = SideController
	if _, err := Migrate(ctx, db, opts, coreSetUpTo(t, SideController, 11), nil); err != nil {
		t.Fatal(err)
	}
	set, err := CoreMigrations(SideController)
	if err != nil {
		t.Fatal(err)
	}
	opts = migrateOpts(t, "w0c", time.Now())
	res, err := Migrate(ctx, db, opts, set, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Applied) == 0 || res.Applied[0].Number != 12 || res.Applied[0].Name != "listeners" {
		t.Fatalf("applied %+v, want 0012_listeners first", res.Applied)
	}
	var tableSQL string
	if err := db.Read().QueryRowContext(ctx, "SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'core_listeners'").
		Scan(&tableSQL); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(strings.TrimSpace(tableSQL), "STRICT") {
		t.Errorf("core_listeners is not STRICT: %s", tableSQL)
	}
	const at = "2026-10-23T00:00:00.000000000Z"
	insert := `INSERT INTO core_listeners (id, address, port, allowed_peer, client_header, public_host, change_id, state,
		created_at, updated_at) VALUES (?, '172.31.255.1', ?, '172.31.255.2', ?, 'panel.example.com', 'C1', ?, ?, ?)`
	exec := func(id string, port int, header, state string) error {
		_, err := db.Writer().ExecContext(ctx, insert, id, port, header, state, at, at)
		return err
	}
	for i, a := range []struct{ header, state string }{
		{"X-Darkpanel-Client", "open"}, {"Cf-Connecting-IP", "revoked"}, {"X-Darkpanel-Client", "closed"},
	} {
		if err := exec("L"+strconv.Itoa(i), 9443, a.header, a.state); err != nil {
			t.Errorf("header %s state %s refused: %v", a.header, a.state, err)
		}
	}
	refused := []struct {
		name          string
		port          int
		header, state string
	}{
		{"unknown header", 9443, "X-Forwarded-For", "open"},
		{"unknown state", 9443, "X-Darkpanel-Client", "paused"},
		{"port 0", 0, "X-Darkpanel-Client", "open"},
		{"port 65536", 65536, "X-Darkpanel-Client", "open"},
	}
	for i, r := range refused {
		if err := exec("R"+strconv.Itoa(i), r.port, r.header, r.state); err == nil {
			t.Errorf("%s: accepted", r.name)
		}
	}
	if err := exec("L0", 9443, "X-Darkpanel-Client", "open"); err == nil {
		t.Error("a duplicate id was accepted")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `mkdir -p /root/.cache/w0c-tmp/18 && TMPDIR=/root/.cache/w0c-tmp/18 GOTOOLCHAIN=local go test -count=1 -run 'TestCoreMigrationsApply|TestControllerListenersMigration' ./internal/core/store/`
Expected: FAIL: `table core_listeners missing` and `applied [], want 0012_listeners first`.

- [ ] **Step 3: Write the implementation**

Create `internal/core/store/migrations/controller/0012_listeners.sql`:

```sql
-- Runtime listeners (spec 1.10): listeners the controller opens and closes
-- without a restart, each serving one exposure's proxy. The state is
-- durable so that a revocation, committed before anything else, outlives a
-- restart: a listener found revoked at start is never opened again.
CREATE TABLE core_listeners (
    id            TEXT PRIMARY KEY,
    address       TEXT NOT NULL,
    port          INTEGER NOT NULL CHECK (port BETWEEN 1 AND 65535),
    allowed_peer  TEXT NOT NULL,
    client_header TEXT NOT NULL CHECK (client_header IN ('X-Darkpanel-Client', 'Cf-Connecting-IP')),
    public_host   TEXT NOT NULL,
    change_id     TEXT NOT NULL,
    state         TEXT NOT NULL CHECK (state IN ('open', 'revoked', 'closed')),
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL
) STRICT;

CREATE INDEX core_listeners_state ON core_listeners (state, created_at, id);
```

- [ ] **Step 4: Run test to verify it passes**

Run: `TMPDIR=/root/.cache/w0c-tmp/18 GOTOOLCHAIN=local go test -count=1 ./internal/core/store/ ./internal/controller/... && TMPDIR=/root/.cache/w0c-tmp/18 GOTOOLCHAIN=local go test -count=1 -tags harness ./internal/core/store/`
Expected: every package `ok` (the harness build adds its `9000` marker after `0012`, which `TestHarnessMigrations` still finds last).

- [ ] **Step 5: Commit**

```bash
git add internal/core/store/migrations/controller/0012_listeners.sql internal/core/store/migrations_test.go
git commit -m "feat(store): add the runtime listener table"
rm -rf /root/.cache/w0c-tmp/18
```

### Task 19: Extension package

This task creates the extension package `internal/core/ext` exactly as spec 1.12 gives it (C2): the route surfaces, `Route`, `Service`, `Deps`, `Dispatcher`, `Publisher` and `Controller`. Nothing contributes or calls it yet: Task 41 mounts its routes in the API, Task 38 calls `Controller` and Task 46 starts the services. Module packages and `internal/controller/server` both import `ext`, so it must import only `internal/core`, `internal/core/store` and the standard library; `TestExtImportsOnlyCoreStoreAndStandardLibrary` parses the package's imports to hold that. Test names and the one package variable carry the prefix `ext`/`Ext`.

**Files:**
- Create: `internal/core/ext/ext.go`
- Create: `internal/core/ext/ext_test.go`

**Interfaces:**
- Consumes: C3 (`core.Listeners`, for `Deps.Listeners`), from Task 2
- Produces: C2 (`ext.Surface`, `ext.SurfaceWeb`, `ext.SurfaceSocket`, `ext.Route`, `ext.Service`, `ext.Deps`, `ext.Dispatcher`, `ext.Publisher`, `ext.Controller`)

**Items:** 52

**Depends on:** Task 2

**Implementer:** dforge:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 0 = 3

- [ ] **Step 1: Write the failing tests**

Create `internal/core/ext/ext_test.go`:

```go
package ext

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// extAllowed are the only non-standard imports the package may have: a
// module package and internal/controller/server both import ext, so an
// import of either from here would be a cycle.
var extAllowed = map[string]bool{
	"github.com/darkraise/darkpanel/internal/core":       true,
	"github.com/darkraise/darkpanel/internal/core/store": true,
}

func TestExtImportsOnlyCoreStoreAndStandardLibrary(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name) //nolint:gosec // G304: this package's own source
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, name, src, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			// A standard-library path has no dot in its first element.
			if !strings.Contains(strings.SplitN(path, "/", 2)[0], ".") {
				continue
			}
			if !extAllowed[path] {
				t.Errorf("%s imports %s; ext may import only internal/core, internal/core/store and the standard library", name, path)
			}
		}
	}
}

func TestExtSurfaceWebIsTheZeroValue(t *testing.T) {
	// A Route literal that omits Surface must land on the web surface, the
	// one with authentication, CSRF and scope checks.
	if (Route{}).Surface != SurfaceWeb || SurfaceSocket == SurfaceWeb {
		t.Errorf("SurfaceWeb = %d, SurfaceSocket = %d; want the web surface as the zero value", SurfaceWeb, SurfaceSocket)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mkdir -p /root/.cache/w0c-tmp/19 && TMPDIR=/root/.cache/w0c-tmp/19 GOTOOLCHAIN=local go test ./internal/core/ext/`
Expected: FAIL to compile with `undefined: Route` and `undefined: SurfaceWeb`.

- [ ] **Step 3: Write the implementation**

Create `internal/core/ext/ext.go`:

```go
// Package ext is how modules and the controller's backup package contribute
// controller routes and services (spec 1.12). It imports only internal/core,
// internal/core/store and the standard library, so module packages and
// internal/controller/server can both import it without a cycle: the server
// imports the modules, the modules import ext, and no module imports the
// server.
package ext

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// Surface is where a contributed route is served.
type Surface int

// The surfaces a contributed route can be served on.
const (
	SurfaceWeb    Surface = iota // session or token, CSRF on unsafe methods, the 60 s write timeout
	SurfaceSocket                // the controller socket and its peer-uid check (api/socket.go)
)

// Route is one contributed HTTP route.
type Route struct {
	Surface Surface
	Method  string     // http.MethodGet, ...
	Path    string     // "/api/v1/controller/backup"; a Go ServeMux path pattern
	Summary string     // for the OpenAPI document
	Scope   core.Scope // SurfaceWeb only: the minimum scope; ignored on the socket
	Stream  bool       // a 30 s deadline per write instead of the 60 s total
	Handler http.Handler
}

// Service is one contributed controller service.
type Service struct {
	Name string                          // "tailnet.keysweep"; its audit actor is system:<Name>
	Run  func(ctx context.Context) error // returns when ctx is done; an error restarts it with backoff 1 s to 5 min
}

// Deps is what the assembly gives controller contributions.
type Deps struct {
	DB          *store.DB // controller.db
	Sealer      core.Sealer
	Clock       core.Clock
	Log         *slog.Logger
	Hosts       core.HostDirectory
	Dispatch    Dispatcher
	Events      Publisher
	Listeners   core.Listeners
	Audit       core.Audit // services write their audit rows here (actor system:<Service.Name>)
	LocalHostID string
}

// Dispatcher is the controller's dispatcher (internal/controller/dispatch);
// hostID is empty for controller-side actions.
type Dispatcher interface {
	Read(ctx context.Context, p core.Principal, hostID string, a core.ActionRef, input json.RawMessage) (json.RawMessage, error)
	ReadInternal(ctx context.Context, p core.Principal, hostID string, a core.ActionRef, input json.RawMessage) (json.RawMessage, error) // 1.7: p is audited
	Plan(ctx context.Context, p core.Principal, hostID string, a core.ActionRef, input json.RawMessage, opts core.PlanOptions) (core.PlanView, error)
	Apply(ctx context.Context, p core.Principal, hostID string, a core.ActionRef, planID string, opts core.ApplyOptions) (core.ChangeView, error)
}

// Publisher is the event bus as contributions see it (1.6): the bus
// encodes data as JSON and assigns the id. events.Bus implements it
// directly; there is no adapter.
type Publisher interface {
	Publish(eventType string, data any)
}

// Controller is a module's optional controller-side contribution, found
// on a core.Module by type assertion.
type Controller interface {
	Controller(deps Deps) ([]Route, []Service, error)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `TMPDIR=/root/.cache/w0c-tmp/19 GOTOOLCHAIN=local go test -count=1 ./internal/core/ext/ && TMPDIR=/root/.cache/w0c-tmp/19 GOTOOLCHAIN=local go vet ./internal/core/ext/ && TMPDIR=/root/.cache/w0c-tmp/19 golangci-lint run --allow-serial-runners ./internal/core/ext/`
Expected: `ok  	github.com/darkraise/darkpanel/internal/core/ext`; vet prints nothing; `0 issues.`

- [ ] **Step 5: Commit**

```bash
gofmt -l internal/core/ext   # prints nothing
git add internal/core/ext/ext.go internal/core/ext/ext_test.go
git commit -m "feat(ext): add the controller extension package"
rm -rf /root/.cache/w0c-tmp/19
```

### Task 20: CLI hand-written command mounting

`NewRoot` mounts the hooks of Task 7 (spec 1.12): each module's hand-written commands go under the module's group (`darkpanel <module> ...`, through the existing `group` helper, so they sit beside the module's generated commands), and each controller command goes under `darkpanel controller`. A module whose function returns no command gets no group. A name that a generated command or another hand-written command already takes fails `NewRoot`, as the existing clash rule does for generated commands. Test helpers carry the prefix `mh` (`mhSwap`, `mhCommand`); tests swap the package variables and restore them with `t.Cleanup`, as `change_test.go` does for `changePoll`.

**Files:**
- Modify: `internal/cli/modules.go` (whole file, created by Task 7)
- Modify: `internal/cli/root.go:118` (the `root.AddCommand(a.changeCommand(), a.controllerCommand())` line)
- Test: `internal/cli/modules_test.go` (whole file, created by Task 7)

**Interfaces:**
- Consumes: Task 7's `moduleCommands` and `controllerCommands`; the existing `group(root *cobra.Command, path []string) *cobra.Command` (`internal/cli/root.go:163`), `cliEnv.run` (`internal/cli/root_test.go:31`) and `testRegistry` with module `demo` (`internal/cli/fixture_test.go`).
- Produces: `func (a *app) mountHandWritten(root, controller *cobra.Command) error` and `func addHandWritten(parent *cobra.Command, cmds []*cobra.Command) error` in `internal/cli/modules.go`; no other task calls them.

**Items:** 52

**Depends on:** Task 7

**Implementer:** dforge:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 1 = 3

- [ ] **Step 1: Write the failing tests**

Replace `internal/cli/modules_test.go` with:

```go
package cli

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// mhSwap replaces moduleCommands and controllerCommands for one test.
func mhSwap(t *testing.T, mods map[string]func(a *app) []*cobra.Command, ctrl []func(a *app) []*cobra.Command) {
	t.Helper()
	savedMods, savedCtrl := moduleCommands, controllerCommands
	t.Cleanup(func() { moduleCommands, controllerCommands = savedMods, savedCtrl })
	moduleCommands, controllerCommands = mods, ctrl
}

// mhCommand is a hand-written command that prints its command path.
func mhCommand(name string) func(a *app) []*cobra.Command {
	return func(*app) []*cobra.Command {
		return []*cobra.Command{{
			Use:  name,
			Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s ran\n", cmd.CommandPath())
				return err
			},
		}}
	}
}

func TestModuleCommandsCoverEveryModule(t *testing.T) {
	want := []string{"cloudflare", "derp", "docker", "firewall", "npm", "panel", "tailnet", "tailscale"}
	if got := slices.Sorted(maps.Keys(moduleCommands)); !slices.Equal(got, want) {
		t.Fatalf("moduleCommands has %v, want %v", got, want)
	}
	for _, id := range want {
		if cmds := moduleCommands[id](&app{}); len(cmds) != 0 {
			t.Errorf("the %s stub returns %d commands, want none", id, len(cmds))
		}
	}
	if len(controllerCommands) != 1 {
		t.Fatalf("controllerCommands has %d entries, want 1 (backupCommands)", len(controllerCommands))
	}
	if cmds := controllerCommands[0](&app{}); len(cmds) != 0 {
		t.Errorf("the backup stub returns %d commands, want none", len(cmds))
	}
	root, err := NewRoot(Options{Registry: testRegistry(t)})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range root.Commands() {
		if slices.Contains(want, c.Name()) {
			t.Errorf("a module without hand-written commands got the group %q", c.CommandPath())
		}
	}
}

func TestModuleCommandsMountUnderTheirGroup(t *testing.T) {
	mhSwap(t, map[string]func(a *app) []*cobra.Command{"demo": mhCommand("extra"), "firewall": mhCommand("break-glass")},
		[]func(a *app) []*cobra.Command{mhCommand("backup")})
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"demo", "extra"}, "darkpanel demo extra ran\n"},
		{[]string{"firewall", "break-glass"}, "darkpanel firewall break-glass ran\n"},
		{[]string{"controller", "backup"}, "darkpanel controller backup ran\n"},
	} {
		stdout, stderr, code := cliEnv{}.run(t, tc.args...)
		if code != ExitOK || stdout != tc.want {
			t.Errorf("darkpanel %s: exit %d, stdout %q, stderr %q; want %q", strings.Join(tc.args, " "), code, stdout, stderr, tc.want)
		}
	}
	root, err := NewRoot(Options{Registry: testRegistry(t)})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := FindCommand(root, "demo.item.get"); !ok {
		t.Error("the generated demo item get is gone once demo has a hand-written command")
	}
}

func TestModuleCommandsRefuseClashes(t *testing.T) {
	for _, tc := range []struct {
		name string
		mods map[string]func(a *app) []*cobra.Command
		ctrl []func(a *app) []*cobra.Command
		want string
	}{
		{"a generated group", map[string]func(a *app) []*cobra.Command{"demo": mhCommand("item")}, nil, `"darkpanel demo item"`},
		{"the controller's web-ca", nil, []func(a *app) []*cobra.Command{mhCommand("web-ca")}, `"darkpanel controller web-ca"`},
		{"another hand-written command", nil,
			[]func(a *app) []*cobra.Command{mhCommand("backup"), mhCommand("backup")}, `"darkpanel controller backup"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mhSwap(t, tc.mods, tc.ctrl)
			_, err := NewRoot(Options{Registry: testRegistry(t)})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("NewRoot: error %v, want one naming %s", err, tc.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mkdir -p /root/.cache/w0c-tmp/20 && TMPDIR=/root/.cache/w0c-tmp/20 GOTOOLCHAIN=local go test -count=1 -run 'TestModuleCommands' ./internal/cli/`
Expected: FAIL — each `TestModuleCommandsMountUnderTheirGroup` case reports a non-zero exit with an `unknown command` error (for example `unknown command "extra" for "darkpanel demo"`), and every `TestModuleCommandsRefuseClashes` case reports `NewRoot: error <nil>`; `TestModuleCommandsCoverEveryModule` passes.

- [ ] **Step 3: Write the implementation**

Replace `internal/cli/modules.go` with:

```go
package cli

import (
	"fmt"
	"maps"
	"slices"

	"github.com/spf13/cobra"
)

// moduleCommands are the hand-written commands of each module; each
// function lives in internal/cli/<module>.go.
var moduleCommands = map[string]func(a *app) []*cobra.Command{
	"firewall": firewallCommands, "docker": dockerCommands, "tailscale": tailscaleCommands,
	"tailnet": tailnetCommands, "derp": derpCommands, "npm": npmCommands,
	"cloudflare": cloudflareCommands, "panel": panelCommands,
}

// controllerCommands are added under "darkpanel controller".
var controllerCommands = []func(a *app) []*cobra.Command{backupCommands}

// mountHandWritten adds each module's hand-written commands under the
// module's group, in module id order, and each controller command under
// controller. A module with no hand-written command gets no group of its
// own. A name that a generated command or another hand-written one already
// takes fails NewRoot, as a clash between generated commands does.
func (a *app) mountHandWritten(root, controller *cobra.Command) error {
	for _, id := range slices.Sorted(maps.Keys(moduleCommands)) {
		cmds := moduleCommands[id](a)
		if len(cmds) == 0 {
			continue
		}
		if err := addHandWritten(group(root, []string{id}), cmds); err != nil {
			return err
		}
	}
	for _, commands := range controllerCommands {
		if err := addHandWritten(controller, commands(a)); err != nil {
			return err
		}
	}
	return nil
}

// addHandWritten adds cmds under parent, refusing a name or alias that a
// command of parent already takes.
func addHandWritten(parent *cobra.Command, cmds []*cobra.Command) error {
	for _, c := range cmds {
		for _, have := range parent.Commands() {
			if have.Name() == c.Name() || have.HasAlias(c.Name()) {
				return fmt.Errorf("the hand-written command \"%s %s\" clashes with an existing command", parent.CommandPath(), c.Name())
			}
		}
		parent.AddCommand(c)
	}
	return nil
}
```

In `internal/cli/root.go`, replace

```go
	root.AddCommand(a.changeCommand(), a.controllerCommand())
	a.wrapEnrollment(root)
```

with

```go
	controller := a.controllerCommand()
	root.AddCommand(a.changeCommand(), controller)
	if err := a.mountHandWritten(root, controller); err != nil {
		return nil, fmt.Errorf("cli: %w", err)
	}
	a.wrapEnrollment(root)
```

The mount runs before `markRuns` and `guardLocal`, so hand-written commands get the same run-error marking and `--local` refusal as every other command.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `TMPDIR=/root/.cache/w0c-tmp/20 GOTOOLCHAIN=local go test -count=1 ./internal/cli/ ./cmd/darkpanel/ && TMPDIR=/root/.cache/w0c-tmp/20 golangci-lint run --allow-serial-runners ./internal/cli/...`
Expected: `ok` for both packages and `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add internal/cli/modules.go internal/cli/modules_test.go internal/cli/root.go
git commit -m "feat(cli): mount hand-written module commands"
rm -rf /root/.cache/w0c-tmp/20
```

### Task 21: Module web stubs and registry entries

Registers the eight new modules in the web registry (R14: one line per module) with the stub files spec 1.12 names, each a component that renders nothing until the module's own wave replaces it. Which extension points each module gets comes from its spec section: host pages for firewall (2.10), docker (3), tailscale (4.1), npm (6) and panel (8, whose exposure is managed on the controller's host page); global pages for tailnet (4.2, `/tailnet`), derp (4.3, `/derp`) and cloudflare (7, `/cloudflare`), which therefore have no host tab; the container action slot's Allow and Deny for firewall (2.10, R55), Expose for npm (6) and Expose via tunnel for cloudflare (7); and the `tailnet.join` "Add host" extension (4.2). The test file `web/src/modules/registry.test.tsx` is new; its helpers carry the prefix `reg`.

**Files:**
- Create: `web/src/modules/firewall/routes.tsx`
- Create: `web/src/modules/firewall/container-actions.tsx`
- Create: `web/src/modules/docker/routes.tsx`
- Create: `web/src/modules/tailscale/routes.tsx`
- Create: `web/src/modules/tailnet/global.tsx`
- Create: `web/src/modules/tailnet/host-create.tsx`
- Create: `web/src/modules/derp/global.tsx`
- Create: `web/src/modules/npm/routes.tsx`
- Create: `web/src/modules/npm/container-actions.tsx`
- Create: `web/src/modules/cloudflare/global.tsx`
- Create: `web/src/modules/cloudflare/container-actions.tsx`
- Create: `web/src/modules/panel/routes.tsx`
- Modify: `web/src/modules/registry.ts:1-10` (the lucide import and `moduleRegistry`)
- Test: `web/src/modules/registry.test.tsx` (new)

**Interfaces:**
- Consumes: C22's types and `registry.ts` from Task 10.
- Produces: C22's registry entries: ids `hostinfo`, `firewall`, `docker`, `tailscale`, `tailnet`, `derp`, `npm`, `cloudflare`, `panel` in that order; global pages `tailnet`, `derp`, `cloudflare`; container actions `firewall.allow`, `firewall.deny`, `npm.expose`, `cloudflare.expose`; the host-create extension `tailnet.join`. The stub files are handed over to their modules' waves (spec 1.12); each container-actions file exports one named component per action (`AllowAction`, `DenyAction`, `ExposeAction`), and every other stub has a default export.

**Items:** 52

**Depends on:** Task 10

**Implementer:** dforge:impl-sonnet-low
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 0 = 2

- [ ] **Step 1: Write the failing test**

Create `web/src/modules/registry.test.tsx`:

```tsx
import { Fragment, Suspense, type ComponentType } from "react"
import { screen, waitFor } from "@testing-library/react"
import { describe, expect, test } from "vitest"
import { renderWithProviders, stubFetch } from "@/test/utils"
import { moduleRegistry } from "./registry"
import type { ContainerRef } from "./types"

const regContainer: ContainerRef = { hostId: "h1", containerId: "c1", name: "web", ports: [] }

// regComponents lists every lazy component of the registry, named.
function regComponents(): Array<[string, ComponentType]> {
  const out: Array<[string, ComponentType]> = []
  for (const entry of moduleRegistry) {
    const { routes: Routes, global, hostCreate } = entry
    if (Routes !== undefined) out.push([`${entry.id} routes`, () => <Routes hostId="h1" />])
    if (global !== undefined) out.push([`${entry.id} global page`, () => <global.routes />])
    if (hostCreate !== undefined) out.push([`${entry.id} host-create`, () => <hostCreate.render value={{}} onChange={() => {}} />])
    for (const action of entry.containerActions ?? []) {
      out.push([`${action.id} container action`, () => <action.render container={regContainer} onDone={() => {}} />])
    }
  }
  return out
}

describe("module registry", () => {
  // One Suspense boundary for all of them: each boundary waits out React's
  // fallback throttle once.
  test("every lazy component of the registry loads", async () => {
    stubFetch({})
    renderWithProviders(
      <Suspense fallback={<p>Loading the components</p>}>
        {regComponents().map(([name, Component]) => (
          <Fragment key={name}>
            <Component />
          </Fragment>
        ))}
      </Suspense>,
    )
    await waitFor(() => expect(screen.queryByText("Loading the components")).toBeNull())
  })

  test("registers the extension points the modules' pages need", () => {
    const globals = moduleRegistry.flatMap((e) => (e.global === undefined ? [] : [e.global.path]))
    expect(globals).toEqual(["tailnet", "derp", "cloudflare"])
    const actions = moduleRegistry.flatMap((e) => (e.containerActions ?? []).map((a) => a.id))
    expect(actions).toEqual(["firewall.allow", "firewall.deny", "npm.expose", "cloudflare.expose"])
    expect(moduleRegistry.flatMap((e) => (e.hostCreate === undefined ? [] : [e.hostCreate.id]))).toEqual(["tailnet.join"])
    expect(moduleRegistry.filter((e) => e.routes === undefined && e.global === undefined)).toEqual([])
  })
})
```

- [ ] **Step 2: Run it to make sure it fails**

Run: `mkdir -p /root/.cache/w0c-tmp/21 && TMPDIR=/root/.cache/w0c-tmp/21 pnpm -C web vitest run src/modules/registry.test.tsx`
Expected: FAIL — "registers the extension points the modules' pages need" expects `["tailnet", "derp", "cloudflare"]` and receives `[]`.

- [ ] **Step 3: Create the stubs**

Create each file with exactly this content.

`web/src/modules/firewall/routes.tsx`:

```tsx
export default function FirewallRoutes() {
  return null
}
```

`web/src/modules/firewall/container-actions.tsx`:

```tsx
export function AllowAction() {
  return null
}

export function DenyAction() {
  return null
}
```

`web/src/modules/docker/routes.tsx`:

```tsx
export default function DockerRoutes() {
  return null
}
```

`web/src/modules/tailscale/routes.tsx`:

```tsx
export default function TailscaleRoutes() {
  return null
}
```

`web/src/modules/tailnet/global.tsx`:

```tsx
export default function TailnetGlobal() {
  return null
}
```

`web/src/modules/tailnet/host-create.tsx`:

```tsx
export default function TailnetHostCreate() {
  return null
}
```

`web/src/modules/derp/global.tsx`:

```tsx
export default function DerpGlobal() {
  return null
}
```

`web/src/modules/npm/routes.tsx`:

```tsx
export default function NpmRoutes() {
  return null
}
```

`web/src/modules/npm/container-actions.tsx`:

```tsx
export function ExposeAction() {
  return null
}
```

`web/src/modules/cloudflare/global.tsx`:

```tsx
export default function CloudflareGlobal() {
  return null
}
```

`web/src/modules/cloudflare/container-actions.tsx`:

```tsx
export function ExposeAction() {
  return null
}
```

`web/src/modules/panel/routes.tsx`:

```tsx
export default function PanelRoutes() {
  return null
}
```

- [ ] **Step 4: Register the modules**

In `web/src/modules/registry.ts`, replace

```ts
import { Info } from "lucide-react"
```

with

```ts
import { BrickWall, Cloud, Container, ExternalLink, Globe, Info, Network, RadioTower, Waypoints } from "lucide-react"
```

and replace the `moduleRegistry` declaration (from `export const moduleRegistry: ModuleEntry[] = [` to its closing `]`) with:

```ts
export const moduleRegistry: ModuleEntry[] = [
  { id: "hostinfo", label: "Host information", icon: Info, routes: lazy(() => import("./hostinfo/routes")) },
  {
    id: "firewall",
    label: "Firewall",
    icon: BrickWall,
    routes: lazy(() => import("./firewall/routes")),
    containerActions: [
      { id: "firewall.allow", label: "Allow", render: lazy(() => import("./firewall/container-actions").then((m) => ({ default: m.AllowAction }))) },
      { id: "firewall.deny", label: "Deny", render: lazy(() => import("./firewall/container-actions").then((m) => ({ default: m.DenyAction }))) },
    ],
  },
  { id: "docker", label: "Docker", icon: Container, routes: lazy(() => import("./docker/routes")) },
  { id: "tailscale", label: "Tailscale", icon: Network, routes: lazy(() => import("./tailscale/routes")) },
  {
    id: "tailnet",
    label: "Tailnet",
    icon: Waypoints,
    hostCreate: { id: "tailnet.join", render: lazy(() => import("./tailnet/host-create")) },
    global: { path: "tailnet", label: "Tailnet", routes: lazy(() => import("./tailnet/global")) },
  },
  { id: "derp", label: "DERP", icon: RadioTower, global: { path: "derp", label: "DERP", routes: lazy(() => import("./derp/global")) } },
  {
    id: "npm",
    label: "Nginx Proxy Manager",
    icon: Globe,
    routes: lazy(() => import("./npm/routes")),
    containerActions: [
      { id: "npm.expose", label: "Expose", render: lazy(() => import("./npm/container-actions").then((m) => ({ default: m.ExposeAction }))) },
    ],
  },
  {
    id: "cloudflare",
    label: "Cloudflare",
    icon: Cloud,
    containerActions: [
      {
        id: "cloudflare.expose",
        label: "Expose via tunnel",
        render: lazy(() => import("./cloudflare/container-actions").then((m) => ({ default: m.ExposeAction }))),
      },
    ],
    global: { path: "cloudflare", label: "Cloudflare", routes: lazy(() => import("./cloudflare/global")) },
  },
  { id: "panel", label: "Panel exposure", icon: ExternalLink, routes: lazy(() => import("./panel/routes")) },
]
```

- [ ] **Step 5: Run the tests and the checks**

Run: `TMPDIR=/root/.cache/w0c-tmp/21 pnpm -C web vitest run src/modules/registry.test.tsx`
Expected: PASS, `Tests  2 passed (2)`. To see that the load test guards the imports, change `./derp/global` to `./derp/globalx` in `registry.ts`, rerun and see `Failed to resolve import "./derp/globalx"`, then change it back.

Run: `TMPDIR=/root/.cache/w0c-tmp/21 make lint-web test-web`
Expected: no ESLint or `tsc` finding, and every Vitest test passes.

- [ ] **Step 6: Commit**

```bash
git add web/src/modules
git commit -m "feat(web): register the module stubs"
rm -rf /root/.cache/w0c-tmp/21
```

### Task 22: Harness pins for Pebble and its challenge server

This task pins the ACME stand-ins of the spec's Versions table, `ghcr.io/letsencrypt/pebble` and `ghcr.io/letsencrypt/pebble-challtestsrv`, in `test/harness/images.json` and `test/harness/pins.go` (frozen after this wave, so later waves only read them). Each is pinned by the digest of its image index, with the tag kept for the reader: `<registry>/<repository>:<tag>@sha256:<digest>`, a reference form `docker pull` accepts (the tag is ignored when a digest is given; grammar in https://github.com/distribution/reference/blob/main/reference.go). Version 2.10.1 of both (image created 2026-04-20); the digests are the `Docker-Content-Digest` of `GET https://ghcr.io/v2/letsencrypt/<name>/manifests/2.10.1` with an anonymous pull token, read on 2026-10-23. `check` refuses any other set of images and any reference not pinned by digest, because Task 34 pastes the references into the provisioning script. The test helper this task adds is `validContainers` in `pins_test.go`.

**Files:**
- Modify: `test/harness/pins.go` (`RequiredContainers`, `Pins.Containers`, `Container`, `containerRef`, `Pins.Container`, `Pins.check`)
- Modify: `test/harness/images.json` (top-level `containers`)
- Test: `test/harness/pins_test.go`

**Interfaces:**
- Consumes: Task 15's `pins_test.go` and `images.json` (this task edits the same files after it).
- Produces (Task 34 and later waves' certificate tests): `var RequiredContainers = []string{"pebble", "pebble-challtestsrv"}`; `type Container struct { Name, Ref string }` (JSON `name`, `ref`); `Pins.Containers []Container` (JSON `containers`); `func (p Pins) Container(name string) (Container, error)`.

**Items:** 52

**Depends on:** Task 15

**Implementer:** dforge:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 1 = 4

- [ ] **Step 1: Write the failing tests**

In `test/harness/pins_test.go`, at the end of `TestPinFile` (after the scanner check) add:

```go
	var names []string
	for _, c := range p.Containers {
		names = append(names, c.Name)
	}
	if !slices.Equal(names, RequiredContainers) {
		t.Errorf("pinned containers = %v, want %v", names, RequiredContainers)
	}
```

After `TestPinFile` add:

```go
// validContainers are RequiredContainers with references every check accepts.
func validContainers() []Container {
	return []Container{
		{Name: "pebble", Ref: "ghcr.io/letsencrypt/pebble:2.10.1@sha256:" + strings.Repeat("c", 64)},
		{Name: "pebble-challtestsrv", Ref: "ghcr.io/letsencrypt/pebble-challtestsrv:2.10.1@sha256:" + strings.Repeat("d", 64)},
	}
}
```

In `TestPinChecks`, append these cases after `"unknown scanner"`:

```go
		{"container by tag only", func(p *Pins) { p.Containers[0].Ref = "ghcr.io/letsencrypt/pebble:2.10.1" }, "not <registry>/<repository>:<tag>@sha256"},
		{"container by digest only", func(p *Pins) {
			p.Containers[0].Ref = "ghcr.io/letsencrypt/pebble@sha256:" + strings.Repeat("c", 64)
		}, "not <registry>/<repository>:<tag>@sha256"},
		{"short container digest", func(p *Pins) { p.Containers[1].Ref = "ghcr.io/letsencrypt/pebble-challtestsrv:2.10.1@sha256:abc" }, "not <registry>/<repository>:<tag>@sha256"},
		{"quote in a container ref", func(p *Pins) {
			p.Containers[1].Ref = "ghcr.io/letsencrypt/pebble':2.10.1@sha256:" + strings.Repeat("d", 64)
		}, "not <registry>/<repository>:<tag>@sha256"},
		{"missing container", func(p *Pins) { p.Containers = p.Containers[:1] }, "want [pebble pebble-challtestsrv]"},
		{"containers out of order", func(p *Pins) {
			p.Containers[0], p.Containers[1] = p.Containers[1], p.Containers[0]
		}, "want [pebble pebble-challtestsrv]"},
```

and the loop's starting pins become:

```go
		p := Pins{Scanner: "debian-13", OS: []OS{validOS("debian-13")}, Containers: validContainers()}
```

In `TestLoadPins`, `want` and the `invalid.json` refusal carry the containers too:

```go
	want := Pins{Scanner: "debian-13", OS: []OS{validOS("debian-13")}, Containers: validContainers()}
```

```go
		{"invalid.json", encode(Pins{Scanner: "debian-14", OS: []OS{validOS("debian-13")}, Containers: validContainers()}), "names no listed OS"},
```

At the end of the file add:

```go
func TestContainer(t *testing.T) {
	p := Pins{Containers: validContainers()}
	if c, err := p.Container("pebble-challtestsrv"); err != nil || c.Ref != validContainers()[1].Ref {
		t.Errorf("Container(pebble-challtestsrv) = %+v, %v", c, err)
	}
	if _, err := p.Container("boulder"); err == nil || !strings.Contains(err.Error(), `"boulder"`) {
		t.Errorf("Container of an unknown image: %v, want an error naming it", err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mkdir -p /root/.cache/w0c-tmp/22 && TMPDIR=/root/.cache/w0c-tmp/22 GOTOOLCHAIN=local go test -count=1 -tags harness -run 'TestPin|TestLoadPins|TestContainer' ./test/harness/`
Expected: FAIL, the package does not compile: `undefined: Container` and `undefined: RequiredContainers`.

- [ ] **Step 3: Write the implementation**

In `test/harness/pins.go`, directly after `RequiredPackages` add:

```go
// RequiredContainers are the container images the pin file lists, in its
// order: the ACME server Pebble and its DNS and challenge server, which
// stand in for Let's Encrypt in the certificate tests.
var RequiredContainers = []string{"pebble", "pebble-challtestsrv"}
```

Replace the `Pins` type with:

```go
// Pins are the pinned images and packages of every operating system the
// harness tests, and the container images its tests run on a target.
type Pins struct {
	Scanner    string      `json:"scanner"` // the OS id whose image the scanner VM boots
	OS         []OS        `json:"os"`
	Containers []Container `json:"containers"` // RequiredContainers, in that order
}

// Container is one container image, pinned by the digest of its image
// index; the tag only tells a reader which release it is.
type Container struct {
	Name string `json:"name"` // what a test asks Pins.Container for
	Ref  string `json:"ref"`  // <registry>/<repository>:<tag>@sha256:<digest>
}
```

In the `var (...)` block of regular expressions, after `debURL`, add:

```go
	containerRef = regexp.MustCompile(`^[a-z0-9.-]+(/[a-z0-9._-]+)+:[0-9A-Za-z][0-9A-Za-z._-]{0,127}@sha256:[0-9a-f]{64}$`)
```

Replace the comment of `check`

```go
// check refuses duplicate ids, a scanner that names no listed OS, and any
// entry OS.check refuses.
```

with `Container` and the new comment:

```go
// Container returns the pinned container image with name.
func (p Pins) Container(name string) (Container, error) {
	for _, c := range p.Containers {
		if c.Name == name {
			return c, nil
		}
	}
	return Container{}, fmt.Errorf("harness: no pinned container image %q", name)
}

// check refuses duplicate ids, a scanner that names no listed OS, any entry
// OS.check refuses, and container images other than RequiredContainers or
// not pinned by digest: the provisioning script pastes the references into
// shell commands.
```

and at the end of `check`, replace

```go
	if !seen[p.Scanner] {
		return fmt.Errorf("scanner %q names no listed OS", p.Scanner)
	}
	return nil
}
```

with:

```go
	if !seen[p.Scanner] {
		return fmt.Errorf("scanner %q names no listed OS", p.Scanner)
	}
	names := make([]string, 0, len(p.Containers))
	for _, c := range p.Containers {
		if !containerRef.MatchString(c.Ref) {
			return fmt.Errorf("container %s: ref %q is not <registry>/<repository>:<tag>@sha256:<64 hex digits>", c.Name, c.Ref)
		}
		names = append(names, c.Name)
	}
	if !slices.Equal(names, RequiredContainers) {
		return fmt.Errorf("containers %v, want %v", names, RequiredContainers)
	}
	return nil
}
```

In `test/harness/images.json`, after the closing `]` of the `os` array add a comma and the top-level `containers` array, so the file ends:

```json
  ],
  "containers": [
    {
      "name": "pebble",
      "ref": "ghcr.io/letsencrypt/pebble:2.10.1@sha256:ddf230642b1a584f519f32e347de1b05a6e4c1f6c35c1863b33effeab5f78199"
    },
    {
      "name": "pebble-challtestsrv",
      "ref": "ghcr.io/letsencrypt/pebble-challtestsrv:2.10.1@sha256:12ce21884def456bcf9786542113949e1f19dc7738d2c70e156c2d0c38a1405b"
    }
  ]
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `mkdir -p /root/.cache/w0c-tmp/22 && TMPDIR=/root/.cache/w0c-tmp/22 GOTOOLCHAIN=local go test -count=1 -tags harness  ./test/harness/ && TMPDIR=/root/.cache/w0c-tmp/22 GOTOOLCHAIN=local go vet -tags harness ./test/... && TMPDIR=/root/.cache/w0c-tmp/22 golangci-lint run --allow-serial-runners ./test/harness/...`
Expected: `ok  	github.com/darkraise/darkpanel/test/harness`, no vet output, `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add test/harness/pins.go test/harness/images.json test/harness/pins_test.go
git commit -m "test(harness): pin Pebble and its challenge server"
```

Empty `/root/.cache/w0c-tmp/22` when the task ends. Nothing boots with these images yet, so this task alone changes no VM.

### Task 23: Cloud HTTP client

Build the provider HTTP client of C8 in `internal/controller/cloud` (`client.go`): one attempt is bounded by `Timeout` (15 s) through its own context, whatever `http.Client` the caller passes; `GET` and `DELETE` are retried at most `MaxRetries` (3) times after a 429, a 5xx or a transport failure, waiting 1 s, 2 s and 4 s, or the provider's `Retry-After` (seconds or an HTTP date) when it gives one; a `Retry-After` longer than 60 s (`maxRetryAfter`) ends the retries at once instead of holding the call open. Every other method is sent exactly once. A failed answer becomes an `*errcode.Error` per C8's mapping, carrying the provider status for `Status(err)`; its detail names the provider, the status and the provider's error code (from `Config.ErrorCode`) and never the body. Every 4xx answer to a write is wrapped with `core.Definite`, so the engine records its effect as skipped; a 5xx or a transport failure on a write is `cloud.uncertain_outcome`, which leaves the non-`Local` effect `unresolved` (the engine already does this, `internal/core/lifecycle/settle.go:43`). The default client refuses redirects, which would carry credentials elsewhere. Tests use `cloudfake` (Task 4) and an injected `Sleep` that records delays, so nothing waits. Test helpers carry the prefix `cl`.

**Files:**
- Create: `internal/controller/cloud/client.go`
- Test: `internal/controller/cloud/client_test.go`

**Interfaces:**
- Consumes: C1 (`errcode.CloudAuthFailed`, `CloudPermissionMissing`, `CloudConflict`, `CloudRateLimited`, `CloudUnavailable`, `CloudUncertainOutcome`), C5 (`cloudfake`).
- Produces: C8's `Timeout`, `MaxRetries`, `CommitWait`, `Config`, `Request`, `Response`, `New`, `(*Client).Do`, `(*Client).JSON`, `Status`; the package comment of `internal/controller/cloud`.

**Items:** 50

**Depends on:** Task 1, Task 4

**Implementer:** dforge:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5

- [ ] **Step 1: Write the failing test**

Create `internal/controller/cloud/client_test.go`:

```go
package cloud

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/cloud/cloudfake"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// clSleeps records the delays a client asked to wait, without waiting.
type clSleeps struct {
	mu     sync.Mutex
	delays []time.Duration
}

func (s *clSleeps) sleep(_ context.Context, d time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.delays = append(s.delays, d)
	return nil
}

func (s *clSleeps) got() []time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]time.Duration(nil), s.delays...)
}

// clClient is a client of fake under the base path /api, with a bearer
// token and recorded sleeps.
func clClient(t *testing.T, fake *cloudfake.Server, mod func(*Config)) (*Client, *clSleeps) {
	t.Helper()
	sleeps := &clSleeps{}
	cfg := Config{
		Provider: "tailscale", BaseURL: fake.URL() + "/api", HTTP: fake.Client(), Sleep: sleeps.sleep,
		Auth: func(r *http.Request) error { r.Header.Set("Authorization", "Bearer tok"); return nil },
	}
	if mod != nil {
		mod(&cfg)
	}
	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return c, sleeps
}

func clOK(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, `{"ok":true}`)
}

func TestDoJoinsPathQueryAndCredentials(t *testing.T) {
	fake := cloudfake.New(t)
	fake.Handle(http.MethodGet, "/api/v2/devices", clOK)
	c, _ := clClient(t, fake, nil)
	res, err := c.Do(t.Context(), Request{Method: http.MethodGet, Path: "/v2/devices", Query: url.Values{"fields": {"all"}}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != http.StatusOK || string(res.Body) != `{"ok":true}` {
		t.Fatalf("answer = %d %s", res.Status, res.Body)
	}
	reqs := fake.Requests()
	if len(reqs) != 1 || reqs[0].RawQuery != "fields=all" || reqs[0].Header.Get("Authorization") != "Bearer tok" {
		t.Fatalf("requests = %+v", reqs)
	}
}

func TestDoMapsFailedAnswers(t *testing.T) {
	cases := []struct {
		method   string
		status   int
		times    int
		code     errcode.Code
		definite bool
		requests int
	}{
		{http.MethodGet, 401, 1, errcode.CloudAuthFailed, false, 1},
		{http.MethodPost, 401, 1, errcode.CloudAuthFailed, true, 1},
		{http.MethodPost, 403, 1, errcode.CloudPermissionMissing, true, 1},
		{http.MethodPut, 409, 1, errcode.CloudConflict, true, 1},
		{http.MethodPatch, 412, 1, errcode.CloudConflict, true, 1},
		{http.MethodPost, 400, 1, errcode.InvalidInput, true, 1},
		{http.MethodGet, 404, 1, errcode.InvalidInput, false, 1},
		{http.MethodDelete, 404, 1, errcode.InvalidInput, false, 1},
		{http.MethodPost, 429, 1, errcode.CloudRateLimited, true, 1},
		{http.MethodGet, 429, 4, errcode.CloudRateLimited, false, 4},
		{http.MethodGet, 503, 4, errcode.CloudUnavailable, false, 4},
		{http.MethodDelete, 500, 4, errcode.CloudUnavailable, false, 4},
		{http.MethodPost, 500, 1, errcode.CloudUncertainOutcome, false, 1},
		{http.MethodPut, 502, 1, errcode.CloudUncertainOutcome, false, 1},
		{http.MethodGet, 302, 1, errcode.CloudUnavailable, false, 1},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+http.StatusText(tc.status), func(t *testing.T) {
			fake := cloudfake.New(t)
			fake.Handle(tc.method, "/api/x", clOK)
			fake.Fail(tc.method, "/api/x", cloudfake.Fault{Status: tc.status, Times: tc.times})
			c, _ := clClient(t, fake, nil)
			_, err := c.Do(t.Context(), Request{Method: tc.method, Path: "/x"})
			if got := errcode.From(err).Code; got != tc.code {
				t.Fatalf("code = %s, want %s (err %v)", got, tc.code, err)
			}
			if core.IsDefinite(err) != tc.definite {
				t.Fatalf("definite = %v, want %v", core.IsDefinite(err), tc.definite)
			}
			if Status(err) != tc.status {
				t.Fatalf("Status = %d, want %d", Status(err), tc.status)
			}
			if n := len(fake.Requests()); n != tc.requests {
				t.Fatalf("sent %d requests, want %d", n, tc.requests)
			}
		})
	}
}

func TestReadsRetryWithBackoff(t *testing.T) {
	fake := cloudfake.New(t)
	fake.Handle(http.MethodGet, "/api/x", clOK)
	fake.Fail(http.MethodGet, "/api/x", cloudfake.Fault{Status: 503, Times: 2})
	c, sleeps := clClient(t, fake, nil)
	if _, err := c.Do(t.Context(), Request{Method: http.MethodGet, Path: "/x"}); err != nil {
		t.Fatal(err)
	}
	want := []time.Duration{time.Second, 2 * time.Second}
	if got := sleeps.got(); !clEqualDurations(got, want) {
		t.Fatalf("delays = %v, want %v", got, want)
	}
}

func TestExhaustedReadsWaitThreeTimes(t *testing.T) {
	fake := cloudfake.New(t)
	fake.Handle(http.MethodDelete, "/api/x", clOK)
	fake.Fail(http.MethodDelete, "/api/x", cloudfake.Fault{Status: 500, Times: 4})
	c, sleeps := clClient(t, fake, nil)
	if _, err := c.Do(t.Context(), Request{Method: http.MethodDelete, Path: "/x"}); err == nil {
		t.Fatal("Do succeeded, want cloud.unavailable")
	}
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}
	if got := sleeps.got(); !clEqualDurations(got, want) {
		t.Fatalf("delays = %v, want %v", got, want)
	}
}

func TestRetryAfter(t *testing.T) {
	start := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name       string
		retryAfter string
		delays     []time.Duration
		requests   int
	}{
		{"seconds", "7", []time.Duration{7 * time.Second}, 2},
		{"http date", start.Add(30 * time.Second).Format(http.TimeFormat), []time.Duration{30 * time.Second}, 2},
		{"unreadable", "soon", []time.Duration{time.Second}, 2},
		{"beyond the cap", "3600", nil, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := cloudfake.New(t)
			fake.Handle(http.MethodGet, "/api/x", clOK)
			fake.Fail(http.MethodGet, "/api/x", cloudfake.Fault{Status: 429, RetryAfter: tc.retryAfter})
			c, sleeps := clClient(t, fake, func(cfg *Config) { cfg.Clock = coretest.NewFakeClock(start) })
			_, err := c.Do(t.Context(), Request{Method: http.MethodGet, Path: "/x"})
			if tc.delays == nil {
				if errcode.From(err).Code != errcode.CloudRateLimited {
					t.Fatalf("err = %v, want cloud.rate_limited without waiting", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if got := sleeps.got(); !clEqualDurations(got, tc.delays) {
				t.Fatalf("delays = %v, want %v", got, tc.delays)
			}
			if n := len(fake.Requests()); n != tc.requests {
				t.Fatalf("sent %d requests, want %d", n, tc.requests)
			}
		})
	}
}

func TestLostWriteAnswerIsUncertainAndNotResent(t *testing.T) {
	fake := cloudfake.New(t)
	var commits atomic.Int32
	fake.Handle(http.MethodPost, "/api/keys", func(w http.ResponseWriter, _ *http.Request) {
		commits.Add(1)
		w.WriteHeader(http.StatusOK)
	})
	fake.Fail(http.MethodPost, "/api/keys", cloudfake.Fault{LoseResponse: true})
	c, sleeps := clClient(t, fake, nil)
	_, err := c.Do(t.Context(), Request{Method: http.MethodPost, Path: "/keys", Body: []byte(`{}`)})
	if errcode.From(err).Code != errcode.CloudUncertainOutcome || core.IsDefinite(err) {
		t.Fatalf("err = %v, want an uncertain, not definite outcome", err)
	}
	if commits.Load() != 1 || len(fake.Requests()) != 1 || len(sleeps.got()) != 0 {
		t.Fatalf("commits %d, requests %d, sleeps %d; want one send and no retry", commits.Load(), len(fake.Requests()), len(sleeps.got()))
	}
}

func TestLostReadAnswerIsRetried(t *testing.T) {
	fake := cloudfake.New(t)
	fake.Handle(http.MethodGet, "/api/x", clOK)
	fake.Fail(http.MethodGet, "/api/x", cloudfake.Fault{LoseResponse: true, Times: 2})
	c, _ := clClient(t, fake, nil)
	if _, err := c.Do(t.Context(), Request{Method: http.MethodGet, Path: "/x"}); err != nil {
		t.Fatal(err)
	}
	if n := len(fake.Requests()); n != 3 {
		t.Fatalf("sent %d requests, want 3", n)
	}
}

// clRoundTrip is an http.RoundTripper made of a function.
type clRoundTrip func(*http.Request) (*http.Response, error)

func (f clRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestEveryAttemptCarriesTheTimeout(t *testing.T) {
	var deadlines []time.Duration
	hc := &http.Client{Transport: clRoundTrip(func(r *http.Request) (*http.Response, error) {
		d, ok := r.Context().Deadline()
		if !ok {
			t.Error("attempt without a deadline")
		}
		deadlines = append(deadlines, time.Until(d))
		return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
	})}
	c, err := New(Config{Provider: "npm", BaseURL: "http://npm.invalid", HTTP: hc, Sleep: (&clSleeps{}).sleep})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = c.Do(t.Context(), Request{Method: http.MethodGet, Path: "/x"})
	if len(deadlines) != 1+MaxRetries {
		t.Fatalf("%d attempts, want %d", len(deadlines), 1+MaxRetries)
	}
	for _, d := range deadlines {
		if d <= Timeout-5*time.Second || d > Timeout {
			t.Fatalf("attempt deadline in %v, want about %v", d, Timeout)
		}
	}
}

func TestDetailNamesStatusAndCodeButNotTheBody(t *testing.T) {
	fake := cloudfake.New(t)
	fake.Handle(http.MethodPost, "/api/x", clOK)
	fake.Fail(http.MethodPost, "/api/x", cloudfake.Fault{Status: 400, Body: `{"code":"invalid_key","message":"secret-value tskey-abc"}`})
	c, _ := clClient(t, fake, func(cfg *Config) {
		cfg.ErrorCode = func(body []byte) string {
			if bytes.Contains(body, []byte("invalid_key")) {
				return "invalid_key"
			}
			return ""
		}
	})
	_, err := c.Do(t.Context(), Request{Method: http.MethodPost, Path: "/x"})
	e := errcode.From(err)
	if e.Detail != "tailscale answered 400 (invalid_key)" {
		t.Fatalf("detail = %q", e.Detail)
	}
	if strings.Contains(err.Error(), "secret-value") {
		t.Fatalf("the error carries the body: %v", err)
	}
}

func TestCredentialFailureSendsNothing(t *testing.T) {
	fake := cloudfake.New(t)
	fake.Handle(http.MethodPost, "/api/x", clOK)
	c, _ := clClient(t, fake, func(cfg *Config) {
		cfg.Auth = func(*http.Request) error { return errors.New("no token") }
	})
	_, err := c.Do(t.Context(), Request{Method: http.MethodPost, Path: "/x"})
	if err == nil || !core.IsDefinite(err) || len(fake.Requests()) != 0 {
		t.Fatalf("err = %v, requests %d; want a definite error and nothing sent", err, len(fake.Requests()))
	}
}

func TestCancelledWaitEndsTheRetries(t *testing.T) {
	fake := cloudfake.New(t)
	fake.Handle(http.MethodGet, "/api/x", clOK)
	fake.Fail(http.MethodGet, "/api/x", cloudfake.Fault{Status: 503, Times: 4})
	c, _ := clClient(t, fake, func(cfg *Config) {
		cfg.Sleep = func(context.Context, time.Duration) error { return context.Canceled }
	})
	_, err := c.Do(t.Context(), Request{Method: http.MethodGet, Path: "/x"})
	if !errors.Is(err, context.Canceled) || len(fake.Requests()) != 1 {
		t.Fatalf("err = %v after %d requests, want context.Canceled after 1", err, len(fake.Requests()))
	}
}

func TestDefaultSleepWaitsOnTheClock(t *testing.T) {
	fake := cloudfake.New(t)
	fake.Handle(http.MethodGet, "/api/x", clOK)
	fake.Fail(http.MethodGet, "/api/x", cloudfake.Fault{Status: 503})
	clock := coretest.NewFakeClock(time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC))
	c, err := New(Config{Provider: "cloudflare", BaseURL: fake.URL() + "/api", HTTP: fake.Client(), Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := c.Do(t.Context(), Request{Method: http.MethodGet, Path: "/x"})
		done <- err
	}()
	for clock.Pending() == 0 {
		select {
		case err := <-done:
			t.Fatalf("Do returned %v before waiting", err)
		case <-time.After(time.Millisecond):
		}
	}
	clock.Advance(time.Second)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestJSONEncodesAndDecodes(t *testing.T) {
	fake := cloudfake.New(t)
	fake.Handle(http.MethodPost, "/api/items", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/json" {
			w.WriteHeader(http.StatusUnsupportedMediaType)
			return
		}
		b, _ := io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write(bytes.Replace(b, []byte(`"name"`), []byte(`"id":"i1","name"`), 1))
	})
	fake.Handle(http.MethodDelete, "/api/items/i1", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	c, _ := clClient(t, fake, nil)
	var out struct{ ID, Name string }
	res, err := c.JSON(t.Context(), http.MethodPost, "/items", map[string]string{"name": "a"}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != http.StatusCreated || out.ID != "i1" || out.Name != "a" {
		t.Fatalf("answer %d, out %+v", res.Status, out)
	}
	if _, err := c.JSON(t.Context(), http.MethodDelete, "/items/i1", nil, nil); err != nil {
		t.Fatal(err)
	}
	if body := fake.Requests()[1].Body; len(body) != 0 {
		t.Fatalf("DELETE sent a body %q", body)
	}
}

func TestNewRefusesABadConfig(t *testing.T) {
	for _, cfg := range []Config{
		{BaseURL: "https://api.example"},
		{Provider: "npm", BaseURL: "ftp://npm.example"},
		{Provider: "npm", BaseURL: "https://npm.example/"},
		{Provider: "npm", BaseURL: "https://npm.example/api?x=1"},
		{Provider: "npm", BaseURL: "npm.example"},
	} {
		if _, err := New(cfg); err == nil {
			t.Errorf("New(%+v) succeeded", cfg)
		}
	}
}

func TestDefaultClientDoesNotFollowRedirects(t *testing.T) {
	fake := cloudfake.New(t)
	fake.Handle(http.MethodGet, "/api/x", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/elsewhere", http.StatusFound)
	})
	fake.Handle(http.MethodGet, "/elsewhere", clOK)
	c, err := New(Config{Provider: "npm", BaseURL: fake.URL() + "/api", Sleep: (&clSleeps{}).sleep})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Do(t.Context(), Request{Method: http.MethodGet, Path: "/x"})
	if Status(err) != http.StatusFound || len(fake.Requests()) != 1 {
		t.Fatalf("err = %v after %d requests, want the 302 itself", err, len(fake.Requests()))
	}
}

func TestStatusWithoutAProviderAnswer(t *testing.T) {
	if Status(nil) != 0 || Status(errors.New("x")) != 0 {
		t.Fatal("Status of an error without a provider answer is not 0")
	}
}

func clEqualDurations(a, b []time.Duration) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mkdir -p /root/.cache/w0c-tmp/23 && export TMPDIR=/root/.cache/w0c-tmp/23 GOTOOLCHAIN=local && go test -count=1 ./internal/controller/cloud/`
Expected: FAIL to build: `undefined: Config`, `undefined: New`, `undefined: Request`.

- [ ] **Step 3: Write the implementation**

Create `internal/controller/cloud/client.go`:

```go
// Package cloud is the controller's HTTP client for the providers modules
// manage through their APIs (spec 1.9): Tailscale, Cloudflare and Nginx
// Proxy Manager. It bounds every attempt, retries only reads and deletes,
// and turns every failed answer into a registered error code whose detail
// never carries the provider's response body.
package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// The client's bounds (spec 1.9).
const (
	Timeout    = 15 * time.Second // per request
	MaxRetries = 3                // GET and DELETE only
	CommitWait = 10 * time.Minute // Effect.CommitWait of every cloud write
)

// maxRetryAfter bounds how long a retry waits for a provider's Retry-After.
// A longer wait ends the retries at once: a request should not hold an API
// call open for minutes, and the caller can try again later.
const maxRetryAfter = 60 * time.Second

// maxBody bounds a provider's answer that the client reads.
const maxBody = 8 << 20

// Config configures New. Provider and BaseURL are required.
type Config struct {
	Provider  string                                           // "tailscale" | "cloudflare" | "npm": named in error details
	BaseURL   string                                           // scheme://host[:port][/prefix], no trailing slash
	Auth      func(r *http.Request) error                      // adds credentials to every attempt; nil adds none
	HTTP      *http.Client                                     // nil: a client with Timeout
	Clock     core.Clock                                       // nil: core.SystemClock{}
	Sleep     func(ctx context.Context, d time.Duration) error // nil: waits on Clock.AfterFunc or ctx
	ErrorCode func(body []byte) string                         // the provider's error code in an error body; nil reads none
}

// Request is one provider request.
type Request struct {
	Method string
	Path   string // joined to BaseURL; starts with "/"
	Query  url.Values
	Header http.Header
	Body   []byte // already encoded; nil sends none
}

// Response is a provider's 2xx answer.
type Response struct {
	Status int
	Header http.Header
	Body   []byte
}

// Client sends requests to one provider. It is safe for concurrent use.
type Client struct {
	cfg  Config
	base string
}

// New returns a client for cfg.
func New(cfg Config) (*Client, error) {
	if cfg.Provider == "" {
		return nil, errors.New("cloud: Config.Provider is required")
	}
	u, err := url.Parse(cfg.BaseURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" ||
		u.RawQuery != "" || u.Fragment != "" || strings.HasSuffix(cfg.BaseURL, "/") {
		return nil, fmt.Errorf("cloud: base URL %q must be scheme://host[:port][/prefix] without a trailing slash", cfg.BaseURL)
	}
	if cfg.HTTP == nil {
		cfg.HTTP = &http.Client{
			Timeout: Timeout,
			// A redirect would carry the credentials to another URL.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}
	}
	if cfg.Clock == nil {
		cfg.Clock = core.SystemClock{}
	}
	c := &Client{cfg: cfg, base: cfg.BaseURL}
	if c.cfg.Sleep == nil {
		c.cfg.Sleep = c.sleep
	}
	return c, nil
}

// retried reports whether a method may be sent again: a read changes
// nothing, and a delete that already committed answers the same way again.
func retried(method string) bool {
	return method == http.MethodGet || method == http.MethodDelete
}

// Do sends req. A 2xx answer returns its Response and a nil error. Every
// other outcome returns an *errcode.Error whose Detail names the provider,
// its HTTP status and its error code, never the body:
//
//   - 401 → CloudAuthFailed; 403 → CloudPermissionMissing; 409 and 412 →
//     CloudConflict; 429 → CloudRateLimited (GET/DELETE after their retries);
//   - 5xx or a transport failure → CloudUnavailable for GET/DELETE after
//     their retries, CloudUncertainOutcome for every other method (never
//     retried);
//   - any other status → errcode.InvalidInput for 4xx, CloudUnavailable
//     otherwise.
//
// Every 4xx answer to a method other than GET and DELETE is wrapped with
// core.Definite: the provider refused it, so nothing committed.
func (c *Client) Do(ctx context.Context, req Request) (Response, error) {
	if req.Path == "" || req.Path[0] != '/' {
		return Response{}, core.Definite(fmt.Errorf("cloud: request path %q must start with /", req.Path))
	}
	attempts := 1
	if retried(req.Method) {
		attempts += MaxRetries
	}
	var err error
	for i := range attempts {
		var res Response
		var wait time.Duration
		res, wait, err = c.attempt(ctx, req)
		if err == nil {
			return res, nil
		}
		if wait < 0 || i == attempts-1 {
			break
		}
		if wait == 0 {
			wait = time.Second << i
		}
		if wait > maxRetryAfter {
			break
		}
		if serr := c.cfg.Sleep(ctx, wait); serr != nil {
			return Response{}, fmt.Errorf("cloud: %s: waiting to retry: %w", c.cfg.Provider, serr)
		}
	}
	return Response{}, err
}

// attempt sends req once. On failure wait says what may follow: -1 means
// no retry, 0 the default backoff, and a positive value the provider's
// Retry-After.
func (c *Client) attempt(ctx context.Context, req Request) (Response, time.Duration, error) {
	actx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	hr, err := c.build(actx, req)
	if err != nil {
		return Response{}, -1, err
	}
	res, err := c.cfg.HTTP.Do(hr)
	if err != nil {
		return Response{}, 0, c.transportError(req.Method, err)
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(res.Body, maxBody+1))
	switch {
	case err != nil:
		return Response{}, 0, c.transportError(req.Method, err)
	case len(body) > maxBody:
		return Response{}, -1, errcode.New(errcode.CloudUnavailable, fmt.Sprintf("%s answered more than %d bytes", c.cfg.Provider, maxBody))
	}
	if res.StatusCode >= 200 && res.StatusCode < 300 {
		return Response{Status: res.StatusCode, Header: res.Header, Body: body}, 0, nil
	}
	wait := time.Duration(-1)
	if res.StatusCode == http.StatusTooManyRequests || res.StatusCode >= 500 {
		wait = c.retryAfter(res.Header.Get("Retry-After"))
	}
	return Response{}, wait, c.statusError(req.Method, res.StatusCode, body)
}

func (c *Client) build(ctx context.Context, req Request) (*http.Request, error) {
	target := c.base + req.Path
	if len(req.Query) > 0 {
		target += "?" + req.Query.Encode()
	}
	var body io.Reader
	if req.Body != nil {
		body = bytes.NewReader(req.Body)
	}
	hr, err := http.NewRequestWithContext(ctx, req.Method, target, body)
	if err != nil {
		return nil, core.Definite(fmt.Errorf("cloud: %s: build request: %w", c.cfg.Provider, err))
	}
	for k, vs := range req.Header {
		for _, v := range vs {
			hr.Header.Add(k, v)
		}
	}
	if c.cfg.Auth != nil {
		if err := c.cfg.Auth(hr); err != nil {
			return nil, core.Definite(fmt.Errorf("cloud: %s: credentials: %w", c.cfg.Provider, err))
		}
	}
	return hr, nil
}

// retryAfter reads a Retry-After value in seconds or as an HTTP date; 0
// means none was given.
func (c *Client) retryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}
	if s, err := strconv.Atoi(v); err == nil && s >= 0 {
		return max(time.Duration(s)*time.Second, time.Nanosecond)
	}
	if t, err := http.ParseTime(v); err == nil {
		return max(t.Sub(c.cfg.Clock.Now()), time.Nanosecond)
	}
	return 0
}

// statusErr carries the provider's answer inside an *errcode.Error, for
// Status.
type statusErr struct {
	status int
	code   string
}

func (e *statusErr) Error() string {
	if e.code == "" {
		return fmt.Sprintf("cloud: status %d", e.status)
	}
	return fmt.Sprintf("cloud: status %d (%s)", e.status, e.code)
}

func (c *Client) statusError(method string, status int, body []byte) error {
	se := &statusErr{status: status}
	if c.cfg.ErrorCode != nil {
		se.code = c.cfg.ErrorCode(body)
	}
	detail := fmt.Sprintf("%s answered %d", c.cfg.Provider, status)
	if se.code != "" {
		detail += " (" + se.code + ")"
	}
	var code errcode.Code
	switch {
	case status == http.StatusUnauthorized:
		code = errcode.CloudAuthFailed
	case status == http.StatusForbidden:
		code = errcode.CloudPermissionMissing
	case status == http.StatusConflict || status == http.StatusPreconditionFailed:
		code = errcode.CloudConflict
	case status == http.StatusTooManyRequests:
		code = errcode.CloudRateLimited
	case status >= 500 && retried(method):
		code = errcode.CloudUnavailable
	case status >= 500:
		code = errcode.CloudUncertainOutcome
		detail += "; the request may still have committed"
	case status >= 400:
		code = errcode.InvalidInput
	default:
		code = errcode.CloudUnavailable
	}
	err := error(errcode.Wrap(code, se, detail))
	if status >= 400 && status < 500 && !retried(method) {
		err = core.Definite(err)
	}
	return err
}

func (c *Client) transportError(method string, err error) error {
	if retried(method) {
		return errcode.Wrap(errcode.CloudUnavailable, err, c.cfg.Provider+" could not be reached")
	}
	return errcode.Wrap(errcode.CloudUncertainOutcome, err, c.cfg.Provider+" did not answer; the request may still have committed")
}

// sleep waits d on the client's clock, or until ctx ends.
func (c *Client) sleep(ctx context.Context, d time.Duration) error {
	done := make(chan struct{})
	t := c.cfg.Clock.AfterFunc(d, func() { close(done) })
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		t.Stop()
		return ctx.Err()
	}
}

// JSON encodes in (nil sends no body), calls Do with Content-Type
// application/json, and decodes a 2xx body into out (nil skips decoding).
func (c *Client) JSON(ctx context.Context, method, path string, in, out any) (Response, error) {
	req := Request{Method: method, Path: path, Header: http.Header{"Accept": {"application/json"}}}
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return Response{}, core.Definite(fmt.Errorf("cloud: %s: encode request: %w", c.cfg.Provider, err))
		}
		req.Body = b
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.Do(ctx, req)
	if err != nil || out == nil || len(res.Body) == 0 {
		return res, err
	}
	if err := json.Unmarshal(res.Body, out); err != nil {
		return res, errcode.Wrap(errcode.CloudUnavailable, err, c.cfg.Provider+" answered JSON that could not be read")
	}
	return res, nil
}

// Status returns the provider HTTP status that err carries, or 0.
func Status(err error) int {
	var se *statusErr
	if errors.As(err, &se) {
		return se.status
	}
	return 0
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `mkdir -p /root/.cache/w0c-tmp/23 && export TMPDIR=/root/.cache/w0c-tmp/23 GOTOOLCHAIN=local && go test -count=3 -race ./internal/controller/cloud/ && go vet ./internal/controller/cloud/ && golangci-lint run --allow-serial-runners ./internal/controller/cloud/`
Expected: `ok  	github.com/darkraise/darkpanel/internal/controller/cloud`, then `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add internal/controller/cloud/client.go internal/controller/cloud/client_test.go
git commit -m "feat(cloud): add the provider HTTP client"
rm -rf /root/.cache/w0c-tmp/23
```

### Task 24: Proxy path in the guard context

A request that came through a runtime listener carries `core.Proxied` in its context (C4; the API puts it there in Tasks 30 and 31). The dispatcher's guard context then gains `Via`, a copy of the request's `ProxyPath`, so a module's lockout guard (wave 1) can protect the exposure the operator is using. The copy drops IPv6 zones like the rest of the guard context and never shares the request's `Public` slice. Plans and the apply-time guard refresh both read `guardContext`, so both carry it. Test helpers of this task carry the prefix `dg` of the existing guard tests (`dgProxied` is new).

**Files:**
- Modify: `internal/controller/dispatch/guard.go:21-40` (`guardContext`)
- Test: `internal/controller/dispatch/guard_test.go` (append)

**Interfaces:**
- Consumes: C4 (`core.Proxied`, `core.ProxiedFrom`, `core.WithProxied`).
- Produces: C12.

**Items:** 51

**Depends on:** Task 3

**Implementer:** dforge:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 2 = 4

- [ ] **Step 1: Write the failing test**

Append to `internal/controller/dispatch/guard_test.go` (its imports already include `context`, `encoding/json`, `net/netip`, `slices` and `testing`):

```go
// dgProxied is a request through an NPM exposure's runtime listener.
var dgProxied = core.Proxied{
	ListenerID: "01LISTENER",
	Client:     netip.MustParseAddr("203.0.113.5"),
	Transport:  netip.MustParseAddrPort("172.31.255.2:40112"),
	PublicHost: "panel.example.com",
	Path: core.ProxyPath{
		ExposureID:   "01EXPOSURE",
		Kind:         "npm",
		ProxyAddr:    netip.MustParseAddr("172.31.255.2"),
		ListenerAddr: netip.MustParseAddrPort("172.31.255.1:9443"),
		Public:       []netip.AddrPort{netip.MustParseAddrPort("198.51.100.4:443")},
	},
}

func TestPlanGuardCarriesProxyPath(t *testing.T) {
	d, seen, agent := newDg(t, func() Topology { return dgTopology })
	client := core.Peer{Addr: dgProxied.Client}
	ctx := core.WithProxied(core.WithClientPeer(context.Background(), client), dgProxied)
	if _, err := d.Plan(ctx, alice, testHost, refDgAgent, json.RawMessage(`{"name":"a"}`), core.PlanOptions{}); err != nil {
		t.Fatal(err)
	}
	via := dgProxied.Path
	want := dgJSON(t, &core.GuardContext{Client: &client, Via: &via, PanelListen: dgTopology.PanelListen,
		AgentListen: dgTopology.AgentListen, AgentPeers: dgTopology.AgentPeers})
	if got := dgJSON(t, seen.guard(core.SideAgent)); got != want {
		t.Errorf("guard %s, want %s", got, want)
	}

	// The apply's guard refresh carries the path too.
	v, err := d.Plan(ctx, alice, testHost, refDgAgent, json.RawMessage(`{"name":"b"}`), core.PlanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Apply(ctx, alice, testHost, refDgAgent, v.PlanID, core.ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := agent.lastOpts(); got.Call == nil || dgJSON(t, got.Call.GuardRefresh) != want {
		t.Errorf("apply guard refresh %+v, want %s", got.Call, want)
	}
}

func TestGuardContextProxyPathIsACopyWithoutZones(t *testing.T) {
	d, _, _ := newDg(t, nil)
	px := dgProxied
	px.Path.ProxyAddr = netip.MustParseAddr("fe80::2%dp-panel")
	px.Path.ListenerAddr = netip.MustParseAddrPort("[fe80::1%dp-panel]:9443")
	px.Path.Public = []netip.AddrPort{netip.MustParseAddrPort("[fe80::9%eth0]:443")}
	g := d.guardContext(core.WithProxied(context.Background(), px))
	if g.Via == nil {
		t.Fatal("no proxy path")
	}
	if g.Via.ProxyAddr != netip.MustParseAddr("fe80::2") || g.Via.ListenerAddr != netip.MustParseAddrPort("[fe80::1]:9443") ||
		!slices.Equal(g.Via.Public, []netip.AddrPort{netip.MustParseAddrPort("[fe80::9]:443")}) {
		t.Errorf("proxy path %s kept a zone", dgJSON(t, g.Via))
	}
	if g.Via.ExposureID != "01EXPOSURE" || g.Via.Kind != "npm" {
		t.Errorf("proxy path %s, want the exposure and kind of the request", dgJSON(t, g.Via))
	}
	g.Via.Public[0] = netip.MustParseAddrPort("192.0.2.1:1")
	if px.Path.Public[0] != netip.MustParseAddrPort("[fe80::9%eth0]:443") {
		t.Error("the request's proxy path was changed through the guard context")
	}
	if g := d.guardContext(context.Background()); g.Via != nil {
		t.Errorf("a direct request has proxy path %s, want none", dgJSON(t, g.Via))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `mkdir -p /root/.cache/w0c-tmp/24 && TMPDIR=/root/.cache/w0c-tmp/24 GOTOOLCHAIN=local go test -count=1 -run 'TestPlanGuardCarriesProxyPath|TestGuardContextProxyPathIsACopyWithoutZones' ./internal/controller/dispatch/`
Expected: FAIL: `TestPlanGuardCarriesProxyPath` reports a guard without `"via"`, and `TestGuardContextProxyPathIsACopyWithoutZones` fails with `no proxy path`.

- [ ] **Step 3: Write the implementation**

In `internal/controller/dispatch/guard.go`, replace the comment and body of `guardContext` from

```go
// guardContext is the guard context of a call now: the client peer the API
// stored in ctx and the configured topology. A composite's children run on
// the engine's context and so carry no client peer. Every address loses its
```

with

```go
// guardContext is the guard context of a call now: the client peer the API
// stored in ctx, the exposure path of a request that came through a runtime
// listener, and the configured topology. A composite's children run on the
// engine's context and so carry neither. Every address loses its
```

(the rest of the comment stays), and replace

```go
	if p, ok := core.ClientPeer(ctx); ok {
		p.Addr = p.Addr.WithZone("")
		g.Client = &p
	}
	return g
}
```

with

```go
	if p, ok := core.ClientPeer(ctx); ok {
		p.Addr = p.Addr.WithZone("")
		g.Client = &p
	}
	if px, ok := core.ProxiedFrom(ctx); ok {
		path := px.Path
		path.ProxyAddr = path.ProxyAddr.WithZone("")
		path.ListenerAddr = netip.AddrPortFrom(path.ListenerAddr.Addr().WithZone(""), path.ListenerAddr.Port())
		path.Public = withoutPortZones(path.Public)
		g.Via = &path
	}
	return g
}
```

`withoutPortZones` already returns a new slice (nil for nil), so the guard context never aliases the request's.

- [ ] **Step 4: Run test to verify it passes**

Run: `TMPDIR=/root/.cache/w0c-tmp/24 GOTOOLCHAIN=local go test -count=1 ./internal/controller/dispatch/ && TMPDIR=/root/.cache/w0c-tmp/24 golangci-lint run --allow-serial-runners ./internal/controller/dispatch/...`
Expected: `ok  	github.com/darkraise/darkpanel/internal/controller/dispatch` and `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add internal/controller/dispatch/guard.go internal/controller/dispatch/guard_test.go
git commit -m "feat(dispatch): carry the proxy path in guard contexts"
rm -rf /root/.cache/w0c-tmp/24
```

### Task 25: Quarantine flag

A restored controller database starts in restore quarantine (spec 10): one `core_meta` key, `restore_quarantine`, whose value is `{"tx", "restoredAt", "backupCreatedAt"}`. Wave 7 sets and clears it; this task only reads it, through the new package `internal/controller/quarantine` (C14), so the dispatcher gate, the engine, the rollout loops and the service runner (wired in Task 48) share one reader. A flag whose value does not decode still counts as set, and `Check` fails closed: a failed read refuses rather than admits. Test helpers carry the prefix `qt` (`qtDB`, `qtSet`).

**Files:**
- Create: `internal/controller/quarantine/quarantine.go`
- Test: `internal/controller/quarantine/quarantine_test.go`

**Interfaces:**
- Consumes: C1 — `errcode.Quarantined` (Task 1).
- Produces: C14 — `quarantine.MetaKey`, `quarantine.Marker`, `quarantine.Read`, `quarantine.Active`, `quarantine.Check` (Task 48 passes `Active` and `Check` over the controller database to the dispatcher, the engine, the rollout loops and the service runner; wave 7 writes the marker).

**Items:** 52

**Depends on:** Task 1

**Implementer:** dforge:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 1 = 4

- [ ] **Step 1: Write the failing test**

Create `internal/controller/quarantine/quarantine_test.go`:

```go
package quarantine

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// qtDB is a migrated controller database in a temporary directory.
func qtDB(t *testing.T) *store.DB {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(dir, "controller.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	set, err := store.CoreMigrations(store.SideController)
	if err != nil {
		t.Fatal(err)
	}
	opts := store.MigrateOptions{Side: store.SideController, BinaryVersion: "test",
		BackupRoot: filepath.Join(dir, "backups"), Keep: 5, Now: time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)}
	if _, err := store.Migrate(ctx, db, opts, set, nil); err != nil {
		t.Fatal(err)
	}
	return db
}

func qtSet(t *testing.T, db *store.DB, value string) {
	t.Helper()
	if _, err := db.Writer().Exec(`INSERT INTO core_meta (key, value) VALUES (?, ?)`, MetaKey, value); err != nil {
		t.Fatal(err)
	}
}

func TestUnsetFlag(t *testing.T) {
	db := qtDB(t)
	ctx := context.Background()
	if m, ok, err := Read(ctx, db); err != nil || ok || m != (Marker{}) {
		t.Errorf("Read = %+v, %v, %v; want unset", m, ok, err)
	}
	if ok, err := Active(ctx, db); err != nil || ok {
		t.Errorf("Active = %v, %v; want false", ok, err)
	}
	if err := Check(ctx, db); err != nil {
		t.Errorf("Check = %v, want nil", err)
	}
}

func TestSetFlag(t *testing.T) {
	db := qtDB(t)
	ctx := context.Background()
	qtSet(t, db, `{"tx":"01RESTORE","restoredAt":"2026-10-23T12:00:00Z","backupCreatedAt":"2026-10-22T08:30:00Z"}`)
	want := Marker{Tx: "01RESTORE", RestoredAt: time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC),
		BackupCreatedAt: time.Date(2026, 10, 22, 8, 30, 0, 0, time.UTC)}
	m, ok, err := Read(ctx, db)
	if err != nil || !ok || !m.RestoredAt.Equal(want.RestoredAt) || !m.BackupCreatedAt.Equal(want.BackupCreatedAt) || m.Tx != want.Tx {
		t.Errorf("Read = %+v, %v, %v; want %+v", m, ok, err, want)
	}
	if ok, err := Active(ctx, db); err != nil || !ok {
		t.Errorf("Active = %v, %v; want true", ok, err)
	}
	var e *errcode.Error
	if err := Check(ctx, db); !errors.As(err, &e) || e.Code != errcode.Quarantined {
		t.Errorf("Check = %v, want %s", err, errcode.Quarantined)
	}
}

// TestMalformedFlagStaysSet: a value that does not decode still means the
// controller is quarantined.
func TestMalformedFlagStaysSet(t *testing.T) {
	db := qtDB(t)
	ctx := context.Background()
	qtSet(t, db, `not json`)
	if _, ok, err := Read(ctx, db); err == nil || !ok {
		t.Errorf("Read of a malformed marker = %v, %v; want set and an error", ok, err)
	}
	if ok, err := Active(ctx, db); err != nil || !ok {
		t.Errorf("Active = %v, %v; want true", ok, err)
	}
	if err := Check(ctx, db); errcode.From(err).Code != errcode.Quarantined {
		t.Errorf("Check = %v, want %s", err, errcode.Quarantined)
	}
}

// TestFailedReadFailsClosed: Check answers the read's error, never nil.
func TestFailedReadFailsClosed(t *testing.T) {
	db := qtDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Active(ctx, db); err == nil {
		t.Fatal("Active with a cancelled context succeeded")
	}
	if err := Check(ctx, db); err == nil {
		t.Error("Check with a failed read = nil, want the error")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mkdir -p /root/.cache/w0c-tmp/25 && TMPDIR=/root/.cache/w0c-tmp/25 GOTOOLCHAIN=local go test -count=1 ./internal/controller/quarantine/`
Expected: FAIL to build with `undefined: MetaKey`, `undefined: Read`, `undefined: Active`, `undefined: Check`.

- [ ] **Step 3: Write the implementation**

Create `internal/controller/quarantine/quarantine.go`:

```go
// Package quarantine reads the controller's restore-quarantine flag (spec
// 10): a restored controller database starts with the flag set, and every
// automatic mutation source and every action apply stay suspended until
// the operator releases it. Restore sets the flag and release clears it;
// this package only reads it.
package quarantine

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// MetaKey is the flag's core_meta key in controller.db.
const MetaKey = "restore_quarantine"

// Marker is the flag's value.
type Marker struct {
	Tx              string    `json:"tx"`
	RestoredAt      time.Time `json:"restoredAt"`
	BackupCreatedAt time.Time `json:"backupCreatedAt"`
}

// Read returns the marker and whether the flag is set. A set flag whose
// value does not decode is an error, never an unset flag.
func Read(ctx context.Context, db *store.DB) (Marker, bool, error) {
	raw, ok, err := value(ctx, db)
	if err != nil || !ok {
		return Marker{}, ok, err
	}
	var m Marker
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return Marker{}, true, fmt.Errorf("quarantine: decode core_meta %s: %w", MetaKey, err)
	}
	return m, true, nil
}

// Active reports whether the flag is set, whatever its value holds.
func Active(ctx context.Context, db *store.DB) (bool, error) {
	_, ok, err := value(ctx, db)
	return ok, err
}

// Check returns errcode.Quarantined while the flag is set, and the read's
// error when the flag cannot be read: a gate must not open on a failed read.
func Check(ctx context.Context, db *store.DB) error {
	ok, err := Active(ctx, db)
	switch {
	case err != nil:
		return err
	case ok:
		return errcode.New(errcode.Quarantined,
			"the controller runs in restore quarantine; resolve the quarantined records and release the quarantine with darkpanel controller quarantine")
	}
	return nil
}

func value(ctx context.Context, db *store.DB) (string, bool, error) {
	var raw string
	err := db.Read().QueryRowContext(ctx, `SELECT value FROM core_meta WHERE key = ?`, MetaKey).Scan(&raw)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", false, nil
	case err != nil:
		return "", false, fmt.Errorf("quarantine: read core_meta %s: %w", MetaKey, err)
	}
	return raw, true, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `TMPDIR=/root/.cache/w0c-tmp/25 GOTOOLCHAIN=local go test -count=1 ./internal/controller/quarantine/ && TMPDIR=/root/.cache/w0c-tmp/25 golangci-lint run --allow-serial-runners ./internal/controller/quarantine/...`
Expected: `ok  	github.com/darkraise/darkpanel/internal/controller/quarantine` and `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add internal/controller/quarantine/quarantine.go internal/controller/quarantine/quarantine_test.go
git commit -m "feat(controller): read the restore quarantine flag"
rm -rf /root/.cache/w0c-tmp/25
```

### Task 26: Dispatcher quarantine gate

While the controller is in restore quarantine, user and token mutations and every action apply are refused with `core.quarantined` (spec 10). Every action apply goes through `dispatch.Dispatcher.Apply` — the network API, the controller socket, the CLI and a composite's children alike — and user and token mutations are core actions applied the same way: `internal/controller/auth` mutates users and tokens only inside the core actions' handlers, apart from login's TOTP replay step (`totp_last_step`) and the last-seen times of sessions and tokens, which admitted logins and reads need. This task adds `Config.Quarantine` (C14) and calls it first in `Apply`, `Undo`, `Repair`, `Release`, `Resolve`, and in the composite runner's step `Undo` and `Release` (`dispatchChildren`), before anything is looked up or marked; plans, reads, confirm, revert, lookups and retirement stay admitted. Test helpers carry the prefix `qg` (`qgGate`, `qgDispatcher`); `newHarness`, `config`, `applyAgent`, `applyCtl`, `plan`, `alice` and `wantCode` are used as written.

**Files:**
- Create: `internal/controller/dispatch/quarantine.go`
- Modify: `internal/controller/dispatch/dispatch.go:33-52` (`Config`), `:64-65` (`New`'s comment)
- Modify: `internal/controller/dispatch/apply.go:22` (`Apply`)
- Modify: `internal/controller/dispatch/ops.go:74` (`Undo`), `:94` (`Repair`), `:137` (`Release`), `:182` (`Resolve`)
- Modify: `internal/controller/dispatch/children.go:51` (`dispatchChildren.Undo`), `:69` (`dispatchChildren.Release`)
- Test: `internal/controller/dispatch/quarantine_test.go`

**Interfaces:**
- Consumes: C1 — `errcode.Quarantined` (Task 1), in the test.
- Produces: C14 — `dispatch.Config.Quarantine func(ctx context.Context) error` (Task 48 passes `quarantine.Check` over the controller database).

**Items:** 52

**Depends on:** Task 1

**Implementer:** dforge:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5

- [ ] **Step 1: Write the failing test**

Create `internal/controller/dispatch/quarantine_test.go`:

```go
package dispatch

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// qgGate is the tests' Config.Quarantine: it refuses while on.
type qgGate struct {
	on    atomic.Bool
	calls atomic.Int32
}

func (g *qgGate) check(context.Context) error {
	g.calls.Add(1)
	if g.on.Load() {
		return errcode.New(errcode.Quarantined, "the test controller is quarantined")
	}
	return nil
}

func qgDispatcher(t *testing.T) (*harness, *Dispatcher, *qgGate) {
	t.Helper()
	h := newHarness(t)
	g := &qgGate{}
	cfg := h.config(&coretest.MemAudit{})
	cfg.Quarantine = g.check
	d, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return h, d, g
}

func TestQuarantineRefusesMutations(t *testing.T) {
	h, d, g := qgDispatcher(t)
	ctx := context.Background()
	confirmable := h.applyAgent(d, "a", core.ApplyOptions{})
	done := h.applyCtl(d, "x")
	agentPlan := h.plan(d, testHost, refSet, `{"name":"c","value":"1"}`)
	ctlPlan := h.plan(d, "", refCtlSet, `{"name":"n","value":"y"}`)

	g.on.Store(true)
	_, err := d.Apply(ctx, alice, testHost, refSet, agentPlan.PlanID, core.ApplyOptions{})
	_ = wantCode(t, err, errcode.Quarantined)
	_, err = d.Apply(ctx, alice, "", refCtlSet, ctlPlan.PlanID, core.ApplyOptions{})
	_ = wantCode(t, err, errcode.Quarantined)
	_, err = d.Undo(ctx, alice, done.ID)
	_ = wantCode(t, err, errcode.Quarantined)
	_, err = d.Repair(ctx, alice, done.ID, core.RepairRequest{})
	_ = wantCode(t, err, errcode.Quarantined)
	_, err = d.Release(ctx, alice, done.ID, "given up")
	_ = wantCode(t, err, errcode.Quarantined)
	_, err = d.Resolve(ctx, alice, done.ID, 1, core.ResolveRequest{Resolution: core.ResolutionAccept})
	_ = wantCode(t, err, errcode.Quarantined)
	_, err = d.Children().Undo(ctx, alice, "", done.ID, nil)
	_ = wantCode(t, err, errcode.Quarantined)
	_, err = d.Children().Release(ctx, alice, testHost, confirmable.ID, "given up", nil)
	_ = wantCode(t, err, errcode.Quarantined)

	// Plans, reads, confirm and revert stay admitted.
	h.plan(d, testHost, refSet, `{"name":"d","value":"1"}`)
	if _, err := d.Read(ctx, alice, testHost, refGet, json.RawMessage(`{"name":"a"}`)); err != nil {
		t.Errorf("read while quarantined: %v", err)
	}
	if v, err := d.Confirm(ctx, alice, confirmable.ID); err != nil || v.State != core.StateConfirmed {
		t.Errorf("confirm while quarantined = %+v, %v; want confirmed", v, err)
	}
	// kv.item.set holds its lock while pending, so the change to revert is
	// applied once the first is confirmed.
	g.on.Store(false)
	revertible := h.applyAgent(d, "b", core.ApplyOptions{})
	g.on.Store(true)
	if v, err := d.Revert(ctx, alice, revertible.ID); err != nil || v.State != core.StateReverted {
		t.Errorf("revert while quarantined = %+v, %v; want reverted", v, err)
	}

	// A refused apply left its plan unapplied, so it applies once released.
	g.on.Store(false)
	v, err := d.Apply(ctx, alice, testHost, refSet, agentPlan.PlanID, core.ApplyOptions{})
	if err != nil || v.State != core.StatePending {
		t.Errorf("apply after the quarantine = %+v, %v; want pending", v, err)
	}
}

// TestQuarantineGateRunsFirst: a quarantined controller refuses before it
// looks the change up, so an unknown change is refused the same way.
func TestQuarantineGateRunsFirst(t *testing.T) {
	_, d, g := qgDispatcher(t)
	ctx := context.Background()
	g.on.Store(true)
	_, err := d.Undo(ctx, alice, "01UNKNOWN")
	_ = wantCode(t, err, errcode.Quarantined)
	_, err = d.Apply(ctx, alice, testHost, refSet, "01UNKNOWN", core.ApplyOptions{})
	_ = wantCode(t, err, errcode.Quarantined)
	if n := g.calls.Load(); n != 2 {
		t.Errorf("gate calls = %d, want 2", n)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mkdir -p /root/.cache/w0c-tmp/26 && TMPDIR=/root/.cache/w0c-tmp/26 GOTOOLCHAIN=local go test -count=1 -run Quarantine ./internal/controller/dispatch/`
Expected: FAIL to build with `cfg.Quarantine undefined (type Config has no field or method Quarantine)`.

- [ ] **Step 3: Write the implementation**

Create `internal/controller/dispatch/quarantine.go`:

```go
package dispatch

import "context"

// admitMutation asks Config.Quarantine whether a mutation may run. A
// restored controller suspends every action apply and every change
// mutation until its quarantine is released (spec 10); plans, reads,
// confirm and revert stay admitted.
func (d *Dispatcher) admitMutation(ctx context.Context) error {
	if d.cfg.Quarantine == nil {
		return nil
	}
	return d.cfg.Quarantine(ctx)
}
```

In `internal/controller/dispatch/dispatch.go`, add the field at the end of `Config`, after `Topology`:

```go
	// Topology is read on every plan of a guarded action and every agent
	// apply, for the guard context; nil is an empty topology.
	Topology func() Topology
	// Quarantine answers core.quarantined while the controller is in restore
	// quarantine (spec 10). Apply, Undo, Repair, Release, Resolve and a
	// composite's undo or release of a step call it first, and any error it
	// returns refuses the call; nil admits everything.
	Quarantine func(ctx context.Context) error
}
```

and change the comment above `New` to:

```go
// New returns a dispatcher for cfg; every field but Ingest, Version,
// Snapshots, Topology and Quarantine is required.
```

Make the gate the first statement of each of these methods. In `internal/controller/dispatch/apply.go`:

```go
func (d *Dispatcher) Apply(ctx context.Context, p core.Principal, hostID string, a core.ActionRef, planID string, opts core.ApplyOptions) (core.ChangeView, error) {
	if err := d.admitMutation(ctx); err != nil {
		return core.ChangeView{}, err
	}
	def, err := d.action(a, hostID)
```

In `internal/controller/dispatch/ops.go`:

```go
func (d *Dispatcher) Undo(ctx context.Context, p core.Principal, changeID string) (core.ChangeView, error) {
	if err := d.admitMutation(ctx); err != nil {
		return core.ChangeView{}, err
	}
	hostID, c, err := d.changeSideInWindow(ctx, changeID)
```

```go
func (d *Dispatcher) Repair(ctx context.Context, p core.Principal, changeID string, req core.RepairRequest) (core.RepairResult, error) {
	if err := d.admitMutation(ctx); err != nil {
		return core.RepairResult{}, err
	}
	if err := d.refuseReleasingParent(ctx, changeID); err != nil {
```

```go
func (d *Dispatcher) Release(ctx context.Context, p core.Principal, changeID, reason string) (core.ReleaseResult, error) {
	if err := d.admitMutation(ctx); err != nil {
		return core.ReleaseResult{}, err
	}
	hostID, c, err := d.changeSide(ctx, changeID)
```

```go
func (d *Dispatcher) Resolve(ctx context.Context, p core.Principal, changeID string, seq int, req core.ResolveRequest) (core.EffectView, error) {
	if err := d.admitMutation(ctx); err != nil {
		return core.EffectView{}, err
	}
	hostID, c, err := d.changeSideInWindow(ctx, changeID)
```

In `internal/controller/dispatch/children.go`:

```go
func (c dispatchChildren) Undo(ctx context.Context, p core.Principal, hostID, changeID string, call *core.CallContext) (core.ChangeView, error) {
	if err := c.d.admitMutation(ctx); err != nil {
		return core.ChangeView{}, err
	}
	if hostID == "" {
```

```go
func (c dispatchChildren) Release(ctx context.Context, p core.Principal, hostID, changeID, reason string, call *core.CallContext) (core.ReleaseResult, error) {
	if err := c.d.admitMutation(ctx); err != nil {
		return core.ReleaseResult{}, err
	}
	if hostID == "" {
```

The rest of each method stays as it is. `dispatchChildren.Plan`, `Apply`, `Read`, `Confirm` and `Revert` call the dispatcher's own methods, so a child's apply is gated by `Apply`; `Lookup`, `Retire` and `AckPrivate` stay admitted.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `TMPDIR=/root/.cache/w0c-tmp/26 GOTOOLCHAIN=local go test -count=1 ./internal/controller/dispatch/ && TMPDIR=/root/.cache/w0c-tmp/26 golangci-lint run --allow-serial-runners ./internal/controller/dispatch/...`
Expected: `ok  	github.com/darkraise/darkpanel/internal/controller/dispatch` and `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add internal/controller/dispatch/quarantine.go internal/controller/dispatch/quarantine_test.go internal/controller/dispatch/dispatch.go internal/controller/dispatch/apply.go internal/controller/dispatch/ops.go internal/controller/dispatch/children.go
git commit -m "feat(dispatch): refuse mutations in restore quarantine"
rm -rf /root/.cache/w0c-tmp/26
```

### Task 27: Change dialog

The one dialog every module page and core page uses to plan, apply and follow a change (spec 1.12's `components/change-dialog.tsx`, C23). It plans on open with a `["plan", ...]` query, which `connectEvents` (`web/src/api/events.ts`) already never refetches; shows the diff and the guard results; offers an override for each failed guard marked `overridable`, which plans again with the guard id in `overrides` (the input field of `core.Guarded`, `internal/core/guard.go:14-16`); asks for the typed phrase of a plan whose `confirmation` is set; greys Apply out with the reason when `GET /api/v1/hosts/{host}/modules/{module}/availability` says the action or module is unavailable; and after the apply follows the change under `changeQueryKey`, showing the revert countdown with Confirm and Revert for a pending change and the steps of a composite. The body is a separate component mounted only while the dialog is open, so closing forgets everything without setting state in an effect (the repository's `react-hooks/set-state-in-effect` rule refuses that). Test helpers carry the prefix `cdx`.

**Files:**
- Create: `web/src/components/change-dialog.tsx`
- Test: `web/src/components/change-dialog.test.tsx`

**Interfaces:**
- Consumes: C21 (`planAction`, `applyPlan` accepting a `202`, `getChange`, `confirmChange`, `revertChange`, `listSteps` returning `Step[]`, `getAvailability`, `changeQueryKey`, `stepsQueryKey`, `changeError`, `errorMessage`; the types `Change`, `Guard` with `overridable` and `overridden`, `Plan`, `Step` whose `action` is `{ Name, Version }` because `core.ActionRef` has no JSON tags); C6 (`axe` and `toHaveNoViolations`).
- Produces: C23 `ChangeDialog`, `ChangeDialogProps`, `ChangeTarget`, used by Tasks 37 and 45.

**Items:** 52, 95

**Depends on:** Task 11, Task 16

**Implementer:** dforge:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 1 = 4

- [ ] **Step 1: Write the failing test**

Create `web/src/components/change-dialog.test.tsx`:

```tsx
import { act, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, expect, test, vi } from "vitest"
import { axe } from "vitest-axe"
import type { Change, Guard, Plan, Step } from "@/api/types"
import { renderWithProviders, stubFetch, type StubHandler } from "@/test/utils"
import { ChangeDialog, type ChangeDialogProps } from "./change-dialog"

const PLAN = "POST /api/v1/hosts/h1/actions/cdx.rule.set/v1/plan"
const APPLY = "POST /api/v1/hosts/h1/actions/cdx.rule.set/v1/apply"
const AVAILABILITY = "GET /api/v1/hosts/h1/modules/cdx/availability"

const cdxAvailable = { module: "cdx", available: true, reason: null, actions: [{ name: "cdx.rule.set", available: true, reason: null }] }

function cdxPlan(over: Partial<Plan> = {}): Plan {
  return {
    planId: "p1", hostId: "h1", action: "cdx.rule.set", version: 1, inputHash: "h",
    diff: [{ op: "+", text: "allow 443/tcp" }, { op: " ", text: "default deny" }], guards: [],
    expiresAt: "2026-10-23T09:00:00Z", confirmation: null, ...over,
  }
}

function cdxGuard(over: Partial<Guard> = {}): Guard {
  return { id: "ssh-session", ok: false, message: "this change blocks your SSH session", overridable: true, overridden: false, ...over }
}

function cdxChange(over: Partial<Change> = {}): Change {
  return {
    id: "c1", hostId: "h1", action: "cdx.rule.set", version: 1, class: "revertible", state: "completed", actor: "user:u1",
    createdAt: "2026-10-23T08:00:00Z", updatedAt: "2026-10-23T08:00:00Z", revertDeadline: null, output: null, error: null,
    compensatedBy: null, parentChangeId: null, owner: null, repairedBy: null, repairOutcome: null, override: null, ...over,
  }
}

function cdxRender(routes: Record<string, StubHandler>, props: Partial<ChangeDialogProps> = {}) {
  const calls = stubFetch({ [AVAILABILITY]: { body: cdxAvailable }, ...routes })
  const onOpenChange = vi.fn()
  const onApplied = vi.fn()
  const result = renderWithProviders(
    <ChangeDialog
      open
      onOpenChange={onOpenChange}
      title="Allow HTTPS"
      target={{ name: "cdx.rule.set", version: 1, hostId: "h1", module: "cdx" }}
      input={{ port: 443 }}
      onApplied={onApplied}
      {...props}
    />,
  )
  return { calls, onOpenChange, onApplied, ...result }
}

afterEach(() => {
  vi.useRealTimers()
})

test("plans on open, shows the diff and applies the plan", async () => {
  const { calls, onApplied } = cdxRender({ [PLAN]: { body: cdxPlan() }, [APPLY]: { body: cdxChange() } })
  const dialog = await screen.findByRole("dialog")
  expect((await within(dialog).findByLabelText("Planned changes")).textContent).toBe("+ allow 443/tcp  default deny")
  expect(calls.find((c) => c.path.endsWith("/plan"))?.body).toEqual({ input: { port: 443 } })
  await userEvent.setup().click(within(dialog).getByRole("button", { name: "Apply" }))
  expect((await within(dialog).findByText("completed")).textContent).toBe("completed")
  expect(calls.find((c) => c.path.endsWith("/apply"))?.body).toEqual({ planId: "p1" })
  expect(onApplied).toHaveBeenCalledWith(cdxChange())
  expect(within(dialog).getByRole("link", { name: "View change" }).getAttribute("href")).toBe("/changes/c1")
})

test("a failed overridable guard can be overridden, which plans again with overrides", async () => {
  const plans: unknown[] = []
  cdxRender({
    [PLAN]: (call) => {
      plans.push(call.body)
      const overridden = plans.length > 1
      return { body: cdxPlan({ planId: `p${plans.length}`, guards: [cdxGuard({ ok: overridden, overridden })] }) }
    },
    [APPLY]: { body: cdxChange() },
  })
  const dialog = await screen.findByRole("dialog")
  const user = userEvent.setup()
  expect((await within(dialog).findByText("failed")).textContent).toBe("failed")
  expect(within(dialog).getByRole("button", { name: "Apply" }).hasAttribute("disabled")).toBe(true)
  await user.click(within(dialog).getByRole("checkbox", { name: "Override ssh-session" }))
  expect((await within(dialog).findByText("overridden")).textContent).toBe("overridden")
  expect(plans).toEqual([{ input: { port: 443 } }, { input: { port: 443, overrides: ["ssh-session"] } }])
  await waitFor(() => expect(within(dialog).getByRole("button", { name: "Apply" }).hasAttribute("disabled")).toBe(false))
})

test("a failed guard that is not overridable blocks the apply and offers no override", async () => {
  cdxRender({ [PLAN]: { body: cdxPlan({ guards: [cdxGuard({ overridable: false })] }) } })
  const dialog = await screen.findByRole("dialog")
  expect((await within(dialog).findByText("this change blocks your SSH session")).textContent).toBe("this change blocks your SSH session")
  expect(within(dialog).queryByRole("checkbox")).toBeNull()
  expect(within(dialog).getByRole("button", { name: "Apply" }).hasAttribute("disabled")).toBe(true)
})

test("an irreversible plan applies only after its phrase is typed", async () => {
  const { calls } = cdxRender({ [PLAN]: { body: cdxPlan({ confirmation: "web-1" }) }, [APPLY]: { body: cdxChange() } })
  const dialog = await screen.findByRole("dialog")
  const user = userEvent.setup()
  const phrase = await within(dialog).findByLabelText("Type web-1 to confirm")
  const apply = within(dialog).getByRole("button", { name: "Apply" })
  await user.type(phrase, "web-")
  expect(apply.hasAttribute("disabled")).toBe(true)
  await user.type(phrase, "1")
  expect(apply.hasAttribute("disabled")).toBe(false)
  await user.click(apply)
  await waitFor(() => expect(calls.find((c) => c.path.endsWith("/apply"))?.body).toEqual({ planId: "p1", confirmation: "web-1" }))
})

test("a pending change counts down to its revert and can be confirmed", async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true })
  vi.setSystemTime(new Date("2026-10-23T08:00:00Z"))
  const pending = cdxChange({ state: "pending", revertDeadline: "2026-10-23T08:01:00Z" })
  const { calls } = cdxRender({
    [PLAN]: { body: cdxPlan() },
    [APPLY]: { body: pending },
    "GET /api/v1/changes/c1": () => ({ body: calls.some((c) => c.path.endsWith("/confirm")) ? cdxChange({ state: "confirmed" }) : pending }),
    "POST /api/v1/changes/c1/confirm": { body: cdxChange({ state: "confirmed" }) },
  })
  const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime })
  const dialog = await screen.findByRole("dialog")
  await user.click(await within(dialog).findByRole("button", { name: "Apply" }))
  expect((await within(dialog).findByRole("timer")).textContent).toBe("Reverts automatically in 60 s unless confirmed.")
  await act(async () => {
    await vi.advanceTimersByTimeAsync(5000)
  })
  expect(within(dialog).getByRole("timer").textContent).toBe("Reverts automatically in 55 s unless confirmed.")
  await user.click(within(dialog).getByRole("button", { name: "Confirm" }))
  expect((await within(dialog).findByText("confirmed")).textContent).toBe("confirmed")
  expect(within(dialog).queryByRole("timer")).toBeNull()
})

test("a pending change can be reverted", async () => {
  const pending = cdxChange({ state: "pending", revertDeadline: "2099-01-01T00:00:00Z" })
  const { calls } = cdxRender({
    [PLAN]: { body: cdxPlan() },
    [APPLY]: { body: pending },
    "GET /api/v1/changes/c1": () => ({ body: calls.some((c) => c.path.endsWith("/revert")) ? cdxChange({ state: "reverted" }) : pending }),
    "POST /api/v1/changes/c1/revert": { body: cdxChange({ state: "reverted" }) },
  })
  const user = userEvent.setup()
  const dialog = await screen.findByRole("dialog")
  await user.click(await within(dialog).findByRole("button", { name: "Apply" }))
  await user.click(await within(dialog).findByRole("button", { name: "Revert" }))
  expect((await within(dialog).findByText("reverted")).textContent).toBe("reverted")
})

test("an asynchronous composite apply answers 202 and shows its steps", async () => {
  const steps: Step[] = [
    { id: "ports", ordinal: 2, host: "h1", action: { Name: "cdx.ports.open", Version: 1 }, state: "pending", attempt: 0, childChangeId: "", earlierChildChangeIds: null, childDeadline: null, error: null },
    { id: "network", ordinal: 1, host: "h1", action: { Name: "cdx.net.create", Version: 1 }, state: "applied", attempt: 1, childChangeId: "k1", earlierChildChangeIds: null, childDeadline: null, error: null },
  ]
  const applying = cdxChange({ class: "composite", state: "applying" })
  cdxRender({
    [PLAN]: { body: cdxPlan() },
    [APPLY]: { status: 202, body: applying },
    "GET /api/v1/changes/c1": { body: applying },
    "GET /api/v1/changes/c1/steps": { body: { steps } },
  })
  const user = userEvent.setup()
  const dialog = await screen.findByRole("dialog")
  await user.click(await within(dialog).findByRole("button", { name: "Apply" }))
  expect((await within(dialog).findByText("applying")).textContent).toBe("applying")
  const list = await within(dialog).findByRole("list", { name: "Steps" })
  expect(within(list).getAllByRole("listitem").map((li) => li.textContent)).toEqual([
    "network (cdx.net.create/v1)applied",
    "ports (cdx.ports.open/v1)pending",
  ])
})

test("an action unavailable on the host is greyed out with the reason", async () => {
  cdxRender({
    [PLAN]: { body: cdxPlan() },
    [AVAILABILITY]: { body: { ...cdxAvailable, actions: [{ name: "cdx.rule.set", available: false, reason: "ufw is not installed" }] } },
  })
  const dialog = await screen.findByRole("dialog")
  expect((await within(dialog).findByText("ufw is not installed")).textContent).toBe("ufw is not installed")
  await within(dialog).findByLabelText("Planned changes")
  expect(within(dialog).getByRole("button", { name: "Apply" }).hasAttribute("disabled")).toBe(true)
})

test("a refused plan and a failed apply are shown", async () => {
  const problem = { type: "about:blank", title: "Locked", status: 423, detail: "lock firewall is held by change c0", code: "lock.held" }
  cdxRender({ [PLAN]: { status: 423, body: problem } })
  const dialog = await screen.findByRole("dialog")
  expect((await within(dialog).findByRole("alert")).textContent).toBe("lock firewall is held by change c0")
})

test("a change that failed shows its error", async () => {
  cdxRender({
    [PLAN]: { body: cdxPlan() },
    [APPLY]: { body: cdxChange({ state: "failed", error: { code: "internal", message: "ufw exited with status 1" } }) },
  })
  const dialog = await screen.findByRole("dialog")
  await userEvent.setup().click(await within(dialog).findByRole("button", { name: "Apply" }))
  expect((await within(dialog).findByRole("alert")).textContent).toBe("ufw exited with status 1")
})

test("a plan given as a prop is shown and applied without planning", async () => {
  const { calls } = cdxRender({ [APPLY]: { body: cdxChange() } }, { input: undefined, plan: cdxPlan({ planId: "r1" }) })
  const dialog = await screen.findByRole("dialog")
  await userEvent.setup().click(await within(dialog).findByRole("button", { name: "Apply" }))
  await waitFor(() => expect(calls.find((c) => c.path.endsWith("/apply"))?.body).toEqual({ planId: "r1" }))
  expect(calls.some((c) => c.path.endsWith("/plan"))).toBe(false)
})

test("the planned dialog has no accessibility violations", async () => {
  cdxRender({ [PLAN]: { body: cdxPlan({ guards: [cdxGuard()], confirmation: "web-1" }) } })
  const dialog = await screen.findByRole("dialog")
  await within(dialog).findByLabelText("Planned changes")
  expect(await axe(dialog)).toHaveNoViolations()
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mkdir -p /root/.cache/w0c-tmp/27 && cd web && TMPDIR=/root/.cache/w0c-tmp/27 pnpm exec vitest run src/components/change-dialog.test.tsx`
Expected: FAIL — `Failed to resolve import "./change-dialog"`.

- [ ] **Step 3: Write the implementation**

Create `web/src/components/change-dialog.tsx`:

```tsx
import { useEffect, useState } from "react"
import { Link } from "react-router"
import { useQuery } from "@tanstack/react-query"
import { Alert, AlertDescription, AlertTitle } from "darkraise-ui/components/alert"
import { Badge, type BadgeVariant } from "darkraise-ui/components/badge"
import { Button } from "darkraise-ui/components/button"
import { Checkbox } from "darkraise-ui/components/checkbox"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "darkraise-ui/components/dialog"
import { Input } from "darkraise-ui/components/input"
import { Label } from "darkraise-ui/components/label"
import { Spinner } from "darkraise-ui/components/spinner"
import {
  applyPlan,
  changeError,
  changeQueryKey,
  confirmChange,
  errorMessage,
  getAvailability,
  getChange,
  listSteps,
  planAction,
  revertChange,
  stepsQueryKey,
} from "@/api/client"
import type { Change, Guard, Plan } from "@/api/types"

export interface ChangeTarget {
  name: string
  version: number
  hostId?: string
  module?: string // checked against getAvailability when hostId is set
}

export interface ChangeDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: string
  target: ChangeTarget
  input?: Record<string, unknown> // planned on open; the dialog adds "overrides"
  plan?: Plan // a plan already made (a repair's answer): shown and applied as is
  onApplied?: (change: Change) => void
}

function stateVariant(state: string): BadgeVariant {
  switch (state) {
    case "completed":
    case "confirmed":
      return "default"
    case "failed":
    case "needs_repair":
    case "interrupted":
      return "destructive"
    default:
      return "secondary"
  }
}

// secondsLeft is the whole seconds until deadline, never below zero.
function secondsLeft(deadline: string, now: number): number {
  return Math.max(0, Math.ceil((Date.parse(deadline) - now) / 1000))
}

function Countdown({ deadline }: { deadline: string }) {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(timer)
  }, [])
  return <p role="timer" aria-live="off">{`Reverts automatically in ${secondsLeft(deadline, now)} s unless confirmed.`}</p>
}

function GuardList({ guards, overrides, canOverride, onToggle }: {
  guards: Guard[]
  overrides: string[]
  canOverride: boolean
  onToggle: (id: string, on: boolean) => void
}) {
  if (guards.length === 0) return null
  return (
    <ul aria-label="Guards" className="flex flex-col gap-2">
      {guards.map((g) => (
        <li key={g.id} className="flex flex-col gap-1">
          <div className="flex items-center gap-2">
            <Badge variant={g.ok ? (g.overridden ? "secondary" : "default") : "destructive"} size="sm">
              {g.overridden ? "overridden" : g.ok ? "passed" : "failed"}
            </Badge>
            <span className="font-mono text-sm">{g.id}</span>
          </div>
          <p className="text-sm text-muted-foreground">{g.message}</p>
          {canOverride && g.overridable && (!g.ok || g.overridden) && (
            <div className="flex items-center gap-2">
              <Checkbox
                id={`override-${g.id}`}
                checked={overrides.includes(g.id)}
                onCheckedChange={(checked) => onToggle(g.id, checked === true)}
              />
              <Label htmlFor={`override-${g.id}`}>{`Override ${g.id}`}</Label>
            </div>
          )}
        </li>
      ))}
    </ul>
  )
}

function Steps({ changeId }: { changeId: string }) {
  const steps = useQuery({ queryKey: stepsQueryKey(changeId), queryFn: () => listSteps(changeId) })
  if (steps.isPending) return <Spinner aria-label="Loading the steps" />
  if (steps.isError) {
    return (
      <Alert variant="destructive" role="alert">
        <AlertDescription>{errorMessage(steps.error)}</AlertDescription>
      </Alert>
    )
  }
  return (
    <ol aria-label="Steps" className="flex flex-col gap-1 text-sm">
      {[...steps.data].sort((a, b) => a.ordinal - b.ordinal).map((s) => (
        <li key={s.id} className="flex flex-wrap items-center gap-2">
          <span className="font-mono">{`${s.id} (${s.action.Name}/v${s.action.Version})`}</span>
          <Badge variant="outline" size="sm">{s.state}</Badge>
          {s.error !== null && <span className="text-destructive">{s.error.message}</span>}
        </li>
      ))}
    </ol>
  )
}

// Applied shows a change after its apply and follows it: the event stream
// invalidates its query, so a confirmation, a revert by the timer or a
// composite's progress shows without a reload.
function Applied({ applied, onError }: { applied: Change; onError: (message: string | null) => void }) {
  const change = useQuery({ queryKey: changeQueryKey(applied.id), queryFn: () => getChange(applied.id), initialData: applied })
  const [busy, setBusy] = useState(false)
  const current = change.data

  async function decide(confirm: boolean) {
    setBusy(true)
    onError(null)
    try {
      const next = await (confirm ? confirmChange(current.id) : revertChange(current.id))
      change.refetch().catch(() => {})
      onError(changeError(next))
    } catch (err) {
      onError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  const failure = changeError(current)
  return (
    <div className="flex flex-col gap-3">
      <div className="flex items-center gap-2">
        <span className="text-sm">State</span>
        <Badge variant={stateVariant(current.state)}>{current.state}</Badge>
      </div>
      {failure !== null && current.state !== "applying" && current.state !== "awaiting_confirmation" && (
        <Alert variant="destructive" role="alert">
          <AlertDescription>{failure}</AlertDescription>
        </Alert>
      )}
      {current.state === "pending" && current.revertDeadline !== null && (
        <div className="flex flex-col gap-2">
          <Countdown deadline={current.revertDeadline} />
          <div className="flex gap-2">
            <Button loading={busy} onClick={() => void decide(true)}>
              Confirm
            </Button>
            <Button variant="outline" disabled={busy} onClick={() => void decide(false)}>
              Revert
            </Button>
          </div>
        </div>
      )}
      {current.class === "composite" && <Steps changeId={current.id} />}
      <Link to={`/changes/${encodeURIComponent(current.id)}`} className="text-sm underline-offset-4 hover:underline">
        View change
      </Link>
    </div>
  )
}

// planQueryKey identifies one plan request. connectEvents never refetches a
// query under "plan", because refetching it would post a new plan.
function planQueryKey(target: ChangeTarget, input: string, overrides: string[]) {
  return ["plan", target.name, target.version, target.hostId ?? null, input, overrides] as const
}

interface BodyProps extends Omit<ChangeDialogProps, "open" | "onOpenChange" | "title"> {
  busy: boolean
  setBusy: (busy: boolean) => void
  close: () => void
}

// ChangeDialogBody holds one opening's state; it mounts when the dialog
// opens, so closing forgets the plan, the overrides and the outcome.
function ChangeDialogBody({ target, input, plan: given, onApplied, busy, setBusy, close }: BodyProps) {
  const [overrides, setOverrides] = useState<string[]>([])
  const [typed, setTyped] = useState("")
  const [error, setError] = useState<string | null>(null)
  const [applied, setApplied] = useState<Change | null>(null)
  const inputKey = JSON.stringify(input ?? {})

  const planned = useQuery({
    queryKey: planQueryKey(target, inputKey, overrides),
    queryFn: () => {
      const body = JSON.parse(inputKey) as Record<string, unknown>
      if (overrides.length > 0) body.overrides = overrides
      return planAction(target.name, target.version, body, target.hostId)
    },
    enabled: given === undefined,
    staleTime: Infinity,
    gcTime: 0,
    retry: false,
    refetchOnWindowFocus: false,
  })
  const availability = useQuery({
    queryKey: ["availability", target.hostId, target.module],
    queryFn: () => getAvailability(target.hostId ?? "", target.module ?? ""),
    enabled: target.hostId !== undefined && target.module !== undefined,
  })

  const plan = given ?? planned.data ?? null
  const planError = given === undefined && planned.isError ? errorMessage(planned.error) : null

  function toggleOverride(id: string, on: boolean) {
    setOverrides((cur) => (on ? [...cur.filter((x) => x !== id), id] : cur.filter((x) => x !== id)))
  }

  let unavailable: string | null = null
  if (availability.data !== undefined) {
    const action = availability.data.actions.find((a) => a.name === target.name)
    if (!availability.data.available) unavailable = availability.data.reason ?? "The module is unavailable on this host."
    else if (action !== undefined && !action.available) unavailable = action.reason ?? "The action is unavailable on this host."
  }
  const guardsOk = plan !== null && plan.guards.every((g) => g.ok)
  const confirmed = plan === null || plan.confirmation === null || typed === plan.confirmation
  const canApply = plan !== null && !planned.isFetching && guardsOk && confirmed && unavailable === null && !busy

  async function apply() {
    if (plan === null || !canApply) return
    setBusy(true)
    setError(null)
    try {
      const change = await applyPlan(plan, plan.confirmation === null ? undefined : typed)
      setApplied(change)
      onApplied?.(change)
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  const shownError = error ?? planError
  return (
    <>
      {unavailable !== null && (
        <Alert variant="warning" role="status">
          <AlertTitle>Unavailable</AlertTitle>
          <AlertDescription>{unavailable}</AlertDescription>
        </Alert>
      )}
      {applied !== null ? (
        <Applied applied={applied} onError={setError} />
      ) : plan === null ? (
        shownError === null && <Spinner aria-label="Planning the change" />
      ) : (
        <div className="flex flex-col gap-4">
          <pre aria-label="Planned changes" className="overflow-x-auto rounded-md bg-muted p-3 font-mono text-xs">
            {plan.diff.map((d, i) => (
              <div key={i} className={d.op === "+" ? "text-success" : d.op === "-" ? "text-destructive" : undefined}>
                {`${d.op} ${d.text}`}
              </div>
            ))}
          </pre>
          <GuardList guards={plan.guards} overrides={overrides} canOverride={given === undefined} onToggle={toggleOverride} />
          {plan.confirmation !== null && (
            <div className="flex flex-col gap-2">
              <Label htmlFor="change-confirmation">{`Type ${plan.confirmation} to confirm`}</Label>
              <Input id="change-confirmation" autoComplete="off" value={typed} onChange={(e) => setTyped(e.target.value)} />
            </div>
          )}
        </div>
      )}
      {shownError !== null && (
        <Alert variant="destructive" role="alert">
          <AlertDescription>{shownError}</AlertDescription>
        </Alert>
      )}
      <DialogFooter>
        {applied === null ? (
          <Button loading={busy} disabled={!canApply} onClick={() => void apply()}>
            Apply
          </Button>
        ) : (
          <Button variant="outline" onClick={close}>
            Close
          </Button>
        )}
      </DialogFooter>
    </>
  )
}

// ChangeDialog plans one action, shows what it will do and the guards it
// ran, and applies it. A failed guard the action marks overridable can be
// overridden, which plans again with the guard listed in "overrides".
export function ChangeDialog({ open, onOpenChange, title, ...body }: ChangeDialogProps) {
  const [busy, setBusy] = useState(false)

  // While an apply is in flight the dialog stays open, so its outcome
  // cannot be lost.
  function changeOpen(next: boolean) {
    if (!next && busy) return
    onOpenChange(next)
  }

  const where = body.target.hostId === undefined ? "on the controller" : `on host ${body.target.hostId}`
  return (
    <Dialog open={open} onOpenChange={changeOpen}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>{`${body.target.name}/v${body.target.version} ${where}`}</DialogDescription>
        </DialogHeader>
        {open && <ChangeDialogBody {...body} busy={busy} setBusy={setBusy} close={() => changeOpen(false)} />}
      </DialogContent>
    </Dialog>
  )
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `mkdir -p /root/.cache/w0c-tmp/27 && cd web && TMPDIR=/root/.cache/w0c-tmp/27 pnpm exec vitest run src/components/change-dialog.test.tsx`
Expected: PASS, 12 tests.

- [ ] **Step 5: Check the whole web suite, types and lint**

Run: `TMPDIR=/root/.cache/w0c-tmp/27 make lint-web test-web`
Expected: ESLint prints nothing (it runs with `--max-warnings 0`), `tsc --noEmit` prints nothing, and the Vitest summary reports every test file passed.

- [ ] **Step 6: Commit**

```bash
git add web/src/components/change-dialog.tsx web/src/components/change-dialog.test.tsx
git commit -m "feat(web): add the change dialog"
rm -rf /root/.cache/w0c-tmp/27
```

### Task 28: Runtime listener store

The listener package gains the four row functions of C9 over `core_listeners` (Task 18): insert in state `open`, a conditional state change that reports whether a row moved (the basis of "revoke is committed first" and of idempotent revoke and close), one read by id answering `not_found`, and the list of one state oldest first, which `Runtime.Start` (Task 40) reads. Times use the controller's fixed-width UTC form so `created_at` sorts as time. A stored address that does not parse is an error, never a zero value. Test helpers of this task carry the prefix `rs` (`rsSpec`, `rsInsert`, `rsSet`); Task 40's tests reuse `rsInsert` and `rsSet`.

**Files:**
- Create: `internal/controller/listen/runtime_store.go`
- Test: `internal/controller/listen/runtime_store_test.go`

**Interfaces:**
- Consumes: C3 (`core.ListenerSpec`, `core.ListenerInfo`, the state and header constants); the table of Task 18.
- Produces: C9's `insertListener`, `setListenerState`, `getListener`, `listListeners`; the constant `listenerTimeFormat`; the test helpers `rsInsert(t, db, id, spec, now)` and `rsSet(t, db, id, state, from...) bool`.

**Items:** 51

**Depends on:** Task 2, Task 18

**Implementer:** dforge:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5

- [ ] **Step 1: Write the failing test**

Create `internal/controller/listen/runtime_store_test.go` (it uses `openControllerDB` from `tailscale_test.go` and `testStart` from `authority_test.go`):

```go
package listen

import (
	"context"
	"database/sql"
	"errors"
	"net/netip"
	"reflect"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/store"
)

var rsSpec = core.ListenerSpec{
	Addr:         netip.MustParseAddrPort("172.31.255.1:9443"),
	AllowedPeer:  netip.MustParseAddr("172.31.255.2"),
	ClientHeader: core.ClientHeaderNPM,
	PublicHost:   "panel.example.com",
	ChangeID:     "01CHANGE",
}

// rsInsert records spec as listener id at now.
func rsInsert(t *testing.T, db *store.DB, id string, spec core.ListenerSpec, now time.Time) {
	t.Helper()
	if err := db.Write(context.Background(), func(tx *sql.Tx) error { return insertListener(context.Background(), tx, id, spec, now) }); err != nil {
		t.Fatal(err)
	}
}

// rsSet moves id to state from the states in from.
func rsSet(t *testing.T, db *store.DB, id, state string, from ...string) bool {
	t.Helper()
	var changed bool
	err := db.Write(context.Background(), func(tx *sql.Tx) error {
		var err error
		changed, err = setListenerState(context.Background(), tx, id, state, from, testStart)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return changed
}

func TestRuntimeStoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := openControllerDB(t)
	rsInsert(t, db, "L1", rsSpec, testStart)
	got, err := getListener(ctx, db, "L1")
	if err != nil {
		t.Fatal(err)
	}
	want := core.ListenerInfo{ID: "L1", Spec: rsSpec, State: core.ListenerOpen}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("getListener = %+v, want %+v", got, want)
	}
	six := rsSpec
	six.Addr = netip.MustParseAddrPort("[fd00:d0::1]:9443")
	six.AllowedPeer = netip.MustParseAddr("fd00:d0::2")
	six.ClientHeader = core.ClientHeaderTunnel
	rsInsert(t, db, "L2", six, testStart.Add(time.Second))
	if got, err := getListener(ctx, db, "L2"); err != nil || got.Spec != six {
		t.Fatalf("getListener(L2) = %+v, %v; want spec %+v", got, err, six)
	}
	var e *errcode.Error
	if _, err := getListener(ctx, db, "L9"); !errors.As(err, &e) || e.Code != errcode.NotFound {
		t.Fatalf("getListener of a missing id = %v, want not_found", err)
	}
}

func TestRuntimeStoreStates(t *testing.T) {
	ctx := context.Background()
	db := openControllerDB(t)
	rsInsert(t, db, "L2", rsSpec, testStart.Add(time.Second))
	rsInsert(t, db, "L1", rsSpec, testStart)
	rsInsert(t, db, "L3", rsSpec, testStart.Add(2*time.Second))

	if !rsSet(t, db, "L3", core.ListenerRevoked, core.ListenerOpen) {
		t.Fatal("open → revoked did not change the row")
	}
	if rsSet(t, db, "L3", core.ListenerClosed, core.ListenerOpen) {
		t.Fatal("the conditional update changed a row in another state")
	}
	if !rsSet(t, db, "L3", core.ListenerRevoked, core.ListenerOpen, core.ListenerRevoked) {
		t.Fatal("revoked → revoked did not match the row")
	}
	if rsSet(t, db, "L9", core.ListenerRevoked, core.ListenerOpen) {
		t.Fatal("a missing id changed a row")
	}
	var updated string
	if err := db.Read().QueryRowContext(ctx, `SELECT updated_at FROM core_listeners WHERE id = 'L3'`).Scan(&updated); err != nil ||
		updated != testStart.UTC().Format(listenerTimeFormat) {
		t.Fatalf("updated_at = %q, %v", updated, err)
	}

	open, err := listListeners(ctx, db, core.ListenerOpen)
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 2 || open[0].ID != "L1" || open[1].ID != "L2" {
		t.Fatalf("open listeners %+v, want L1 then L2", open)
	}
	if revoked, err := listListeners(ctx, db, core.ListenerRevoked); err != nil || len(revoked) != 1 || revoked[0].ID != "L3" {
		t.Fatalf("revoked listeners %+v, %v; want L3", revoked, err)
	}
	if closed, err := listListeners(ctx, db, core.ListenerClosed); err != nil || len(closed) != 0 {
		t.Fatalf("closed listeners %+v, %v; want none", closed, err)
	}
	err = db.Write(ctx, func(tx *sql.Tx) error {
		_, err := setListenerState(ctx, tx, "L1", core.ListenerClosed, nil, testStart)
		return err
	})
	if err == nil {
		t.Fatal("setListenerState without a from state succeeded")
	}
}

func TestRuntimeStoreRefusesBadRows(t *testing.T) {
	db := openControllerDB(t)
	for _, tc := range []struct {
		name string
		spec core.ListenerSpec
	}{
		{"an unknown client header", core.ListenerSpec{Addr: rsSpec.Addr, AllowedPeer: rsSpec.AllowedPeer, ClientHeader: "X-Forwarded-For"}},
		{"port 0", core.ListenerSpec{Addr: netip.MustParseAddrPort("172.31.255.1:0"), AllowedPeer: rsSpec.AllowedPeer, ClientHeader: core.ClientHeaderNPM}},
	} {
		err := db.Write(context.Background(), func(tx *sql.Tx) error { return insertListener(context.Background(), tx, "B", tc.spec, testStart) })
		if err == nil {
			t.Errorf("%s: recorded", tc.name)
		}
	}
	rsInsert(t, db, "L1", rsSpec, testStart)
	err := db.Write(context.Background(), func(tx *sql.Tx) error {
		_, err := setListenerState(context.Background(), tx, "L1", "paused", []string{core.ListenerOpen}, testStart)
		return err
	})
	if err == nil {
		t.Error("state paused accepted")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `mkdir -p /root/.cache/w0c-tmp/28 && TMPDIR=/root/.cache/w0c-tmp/28 GOTOOLCHAIN=local go test -count=1 -run TestRuntimeStore ./internal/controller/listen/`
Expected: FAIL to build: `undefined: insertListener`, `undefined: setListenerState`, `undefined: getListener`, `undefined: listListeners`.

- [ ] **Step 3: Write the implementation**

Create `internal/controller/listen/runtime_store.go`:

```go
package listen

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// listenerTimeFormat is the fixed-width RFC 3339 UTC form of every stored
// time, so the text order of created_at is its time order.
const listenerTimeFormat = "2006-01-02T15:04:05.000000000Z"

const listenerColumns = `id, address, port, allowed_peer, client_header, public_host, change_id, state`

// insertListener records spec as listener id in state open.
func insertListener(ctx context.Context, tx *sql.Tx, id string, spec core.ListenerSpec, now time.Time) error {
	at := now.UTC().Format(listenerTimeFormat)
	_, err := tx.ExecContext(ctx, `INSERT INTO core_listeners (`+listenerColumns+`, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, spec.Addr.Addr().String(), int(spec.Addr.Port()), spec.AllowedPeer.String(), spec.ClientHeader,
		spec.PublicHost, spec.ChangeID, core.ListenerOpen, at, at)
	if err != nil {
		return fmt.Errorf("listen: record runtime listener %s: %w", id, err)
	}
	return nil
}

// setListenerState moves listener id to state when its state is one of
// from, and reports whether it did.
func setListenerState(ctx context.Context, tx *sql.Tx, id, state string, from []string, now time.Time) (bool, error) {
	if len(from) == 0 {
		return false, errors.New("listen: setListenerState needs at least one state to move from")
	}
	fromJSON, err := json.Marshal(from)
	if err != nil {
		return false, fmt.Errorf("listen: set runtime listener %s %s: %w", id, state, err)
	}
	res, err := tx.ExecContext(ctx, `UPDATE core_listeners SET state = ?, updated_at = ?
		WHERE id = ? AND state IN (SELECT value FROM json_each(?))`,
		state, now.UTC().Format(listenerTimeFormat), id, string(fromJSON))
	if err != nil {
		return false, fmt.Errorf("listen: set runtime listener %s %s: %w", id, state, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("listen: set runtime listener %s %s: %w", id, state, err)
	}
	return n == 1, nil
}

// getListener reads listener id; errcode.NotFound when there is none.
func getListener(ctx context.Context, db *store.DB, id string) (core.ListenerInfo, error) {
	row := db.Read().QueryRowContext(ctx, `SELECT `+listenerColumns+` FROM core_listeners WHERE id = ?`, id)
	info, err := scanListener(row)
	if errors.Is(err, sql.ErrNoRows) {
		return core.ListenerInfo{}, errcode.New(errcode.NotFound, "no runtime listener "+id)
	}
	return info, err
}

// listListeners lists the listeners in state, oldest first.
func listListeners(ctx context.Context, db *store.DB, state string) ([]core.ListenerInfo, error) {
	rows, err := db.Read().QueryContext(ctx, `SELECT `+listenerColumns+` FROM core_listeners
		WHERE state = ? ORDER BY created_at, id`, state)
	if err != nil {
		return nil, fmt.Errorf("listen: list runtime listeners: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []core.ListenerInfo
	for rows.Next() {
		info, err := scanListener(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, info)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listen: list runtime listeners: %w", err)
	}
	return out, nil
}

type listenerScanner interface {
	Scan(dest ...any) error
}

func scanListener(row listenerScanner) (core.ListenerInfo, error) {
	var info core.ListenerInfo
	var addr, peer string
	var port int
	err := row.Scan(&info.ID, &addr, &port, &peer, &info.Spec.ClientHeader, &info.Spec.PublicHost,
		&info.Spec.ChangeID, &info.State)
	if errors.Is(err, sql.ErrNoRows) {
		return core.ListenerInfo{}, err
	}
	if err != nil {
		return core.ListenerInfo{}, fmt.Errorf("listen: read runtime listener: %w", err)
	}
	ip, err := netip.ParseAddr(addr)
	if err != nil || port < 1 || port > 65535 {
		return core.ListenerInfo{}, fmt.Errorf("listen: runtime listener %s has a bad address %q port %d", info.ID, addr, port)
	}
	info.Spec.Addr = netip.AddrPortFrom(ip, uint16(port)) //nolint:gosec // G115: checked to be within 1-65535
	if info.Spec.AllowedPeer, err = netip.ParseAddr(peer); err != nil {
		return core.ListenerInfo{}, fmt.Errorf("listen: runtime listener %s has a bad allowed peer %q", info.ID, peer)
	}
	return info, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `TMPDIR=/root/.cache/w0c-tmp/28 GOTOOLCHAIN=local go test -count=1 ./internal/controller/listen/ && TMPDIR=/root/.cache/w0c-tmp/28 golangci-lint run --allow-serial-runners ./internal/controller/listen/...`
Expected: `ok  	github.com/darkraise/darkpanel/internal/controller/listen` and `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add internal/controller/listen/runtime_store.go internal/controller/listen/runtime_store_test.go
git commit -m "feat(listen): store runtime listeners"
rm -rf /root/.cache/w0c-tmp/28
```

### Task 29: Tailnet policy parser dependency

Section 11 adds `github.com/tailscale/hujson`, which the tailnet module's policy editor (spec 4.2) uses to read and rewrite the policy file with its comments kept. `go.mod` is frozen once wave 0 merges, so the dependency must be added now, before any code uses it; an unused requirement is dropped by `go mod tidy`, so the tailnet skeleton holds a blank import that keeps it (C7). The other dependencies of section 11 need nothing: `golang.org/x/crypto` and `golang.org/x/sys` are already required (`go.mod:16-17`), and the web development dependencies are Task 16's. No test helper is added; the check is the build plus `go mod tidy -diff`.

**Files:**
- Create: `internal/modules/tailnet/deps.go`
- Modify: `go.mod`, `go.sum`

**Interfaces:**
- Consumes: Task 8's package `internal/modules/tailnet`.
- Produces: C7's hujson import; `github.com/tailscale/hujson v0.0.0-20260727124030-b80ff77dac4f` as a direct requirement in `go.mod`.

**Items:** 52

**Depends on:** Task 8

**Implementer:** dforge:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 1 = 3

- [ ] **Step 1: Write the import that needs the dependency**

Create `internal/modules/tailnet/deps.go`:

```go
package tailnet

import (
	// The policy editor parses and rewrites the tailnet policy file with its
	// comments kept (spec 4.2); this import keeps the parser pinned in
	// go.mod, which is frozen once the module skeletons exist.
	_ "github.com/tailscale/hujson"
)
```

The comment sits inside the import block, directly above the blank import: revive's `blank-imports` rule accepts a blank import only with a comment there.

- [ ] **Step 2: Run the build to verify it fails**

Run: `mkdir -p /root/.cache/w0c-tmp/29 && TMPDIR=/root/.cache/w0c-tmp/29 GOTOOLCHAIN=local go build ./internal/modules/tailnet/`
Expected: FAIL with `no required module provides package github.com/tailscale/hujson`.

- [ ] **Step 3: Add the dependency**

Run: `TMPDIR=/root/.cache/w0c-tmp/29 GOTOOLCHAIN=local go get github.com/tailscale/hujson@v0.0.0-20260727124030-b80ff77dac4f && TMPDIR=/root/.cache/w0c-tmp/29 GOTOOLCHAIN=local go mod tidy`

Expected result, checked with `git diff go.mod go.sum`: `go.mod` gains, in its first `require` block between `github.com/spf13/cobra v1.10.2` and `golang.org/x/crypto v0.58.0`, the line

```
	github.com/tailscale/hujson v0.0.0-20260727124030-b80ff77dac4f
```

and `go.sum` gains exactly these four lines (hujson and its test dependency go-cmp):

```
github.com/google/go-cmp v0.7.0 h1:wk8382ETsv4JYUZwIsn6YpYiWiBsYLSJiTsyBybVuN8=
github.com/google/go-cmp v0.7.0/go.mod h1:pXiqmnSA92OHEEa9HXL2W4E7lf9JzCmGVUdgjX3N/iU=
github.com/tailscale/hujson v0.0.0-20260727124030-b80ff77dac4f h1:9hiVElpCmKzsBKQHkBqZ8LGzt82iLfM8egxr4sew+Ys=
github.com/tailscale/hujson v0.0.0-20260727124030-b80ff77dac4f/go.mod h1:8/zr1Tv0+cKpVtGCEB/7YfRXr2TszsMxMXLaT8YuBgU=
```

If `go mod tidy` marks the requirement `// indirect`, the import of Step 1 is missing from the build; fix that rather than editing `go.mod` by hand.

- [ ] **Step 4: Run the checks to verify they pass**

Run: `TMPDIR=/root/.cache/w0c-tmp/29 GOTOOLCHAIN=local go build ./... && TMPDIR=/root/.cache/w0c-tmp/29 GOTOOLCHAIN=local go mod tidy -diff && TMPDIR=/root/.cache/w0c-tmp/29 GOTOOLCHAIN=local go test -count=1 ./internal/modules/... && TMPDIR=/root/.cache/w0c-tmp/29 golangci-lint run --allow-serial-runners ./internal/modules/... && TMPDIR=/root/.cache/w0c-tmp/29 GOTOOLCHAIN=local make vuln`
Expected: the build succeeds, `go mod tidy -diff` prints nothing, the tests print `ok`, golangci-lint prints `0 issues.`, and govulncheck prints `No vulnerabilities found.`

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum internal/modules/tailnet/deps.go
git commit -m "chore(deps): add hujson for the tailnet policy"
rm -rf /root/.cache/w0c-tmp/29
```

### Task 30: Runtime listener request wrapper

A runtime listener (spec 1.10) serves the API through a proxy Darkpanel configured: NPM sets `X-Darkpanel-Client` from its own TCP peer, a Cloudflare tunnel sets `Cf-Connecting-IP`. This task adds C10, `(*Server).RuntimeHandler`, in a new file `internal/controller/api/runtime.go`. It is the only code that reads those headers: it refuses a request on a revoked listener, a forwarded protocol other than `https`, and a missing or malformed client header, then serves the ordinary API handler with `core.Proxied` (C4) in the request's context. It never reads `X-Forwarded-For` or `X-Real-IP`. The refusals are RFC 9457 problems through the package's `writeProblem`: a revoked listener answers `404 not_found` ("this exposure was removed": the exposure no longer exists, and a 5xx would log every late request as an error); a listener state that cannot be read answers `503 unavailable` (fail closed); a bad forwarded protocol or client header answers `403 auth.forbidden`. What the API does with `core.Proxied` (source, guard peer, origin check, streams) is Task 31. Test helpers carry the prefix `rt` (`rtInfo`, `rtProbe`, `rtRequest`, `rtServe`, `rtNotRevoked`).

**Files:**
- Create: `internal/controller/api/runtime.go`
- Test: `internal/controller/api/runtime_test.go`

**Interfaces:**
- Consumes: C3 (`core.ListenerInfo`, `core.ListenerSpec`, `core.ClientHeaderNPM`, `core.ClientHeaderTunnel`, `core.ListenerOpen`), C4 (`core.Proxied`, `core.WithProxied`, `core.ProxiedFrom`, the `Path` fields C4 states).
- Produces: C10, `func (s *Server) RuntimeHandler(info core.ListenerInfo, revoked <-chan struct{}, isRevoked func(ctx context.Context) (bool, error)) http.Handler`, which Task 47 passes to the runtime listener service (C19) as its handler factory.

**Items:** 51

**Depends on:** Task 2, Task 3

**Implementer:** dforge:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 3 = 5
**Lenses:** security

- [ ] **Step 1: Write the failing test**

Create `internal/controller/api/runtime_test.go`:

```go
package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"reflect"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)

// rtInfo is a runtime listener of kind header on the panel network.
func rtInfo(header string) core.ListenerInfo {
	return core.ListenerInfo{ID: "L1", State: core.ListenerOpen, Spec: core.ListenerSpec{
		Addr: netip.MustParseAddrPort("172.31.255.1:9443"), AllowedPeer: netip.MustParseAddr("172.31.255.2"),
		ClientHeader: header, PublicHost: "panel.example.com", ChangeID: "01CHANGE00000000000000000A",
	}}
}

// rtProbe records the Proxied of the one request that reached it.
type rtProbe struct {
	ran     bool
	proxied core.Proxied
	found   bool
}

func (p *rtProbe) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.ran = true
	p.proxied, p.found = core.ProxiedFrom(r.Context())
	w.WriteHeader(http.StatusNoContent)
}

// rtRequest is a GET from the proxy at 172.31.255.2 with headers.
func rtRequest(headers map[string][]string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "https://panel.example.com/api/v1/auth/session", nil)
	r.RemoteAddr = "172.31.255.2:40000"
	for k, vs := range headers {
		for _, v := range vs {
			r.Header.Add(k, v)
		}
	}
	return r
}

// rtServe serves r through RuntimeHandler of info over a probe in place of
// the API's handler and returns the answer, its problem code and the probe.
func rtServe(t *testing.T, info core.ListenerInfo, revoked chan struct{}, isRevoked func(context.Context) (bool, error),
	r *http.Request) (*httptest.ResponseRecorder, string, *rtProbe) {
	t.Helper()
	h := newHarness(t)
	p := &rtProbe{}
	h.srv.handler = p
	rec := httptest.NewRecorder()
	h.srv.RuntimeHandler(info, revoked, isRevoked).ServeHTTP(rec, r)
	var prob struct {
		Code string `json:"code"`
	}
	if rec.Code != http.StatusNoContent {
		if err := json.Unmarshal(rec.Body.Bytes(), &prob); err != nil {
			t.Fatalf("answer %d %q is not a problem: %v", rec.Code, rec.Body.String(), err)
		}
	}
	return rec, prob.Code, p
}

func rtNotRevoked(context.Context) (bool, error) { return false, nil }

func TestRuntimeHandlerProxied(t *testing.T) {
	tests := []struct {
		name, header, value string
		client              string
		kind                string
	}{
		{"npm", core.ClientHeaderNPM, "203.0.113.7", "203.0.113.7", "npm"},
		{"tunnel over IPv6", core.ClientHeaderTunnel, "2001:db8::7", "2001:db8::7", "tunnel"},
		{"IPv4-mapped value", core.ClientHeaderNPM, "::ffff:203.0.113.7", "203.0.113.7", "npm"},
		{"surrounding blanks", core.ClientHeaderNPM, " 203.0.113.7 ", "203.0.113.7", "npm"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := rtInfo(tt.header)
			revoked := make(chan struct{})
			rec, _, p := rtServe(t, info, revoked, rtNotRevoked, rtRequest(map[string][]string{
				tt.header: {tt.value}, "X-Forwarded-Proto": {"https"},
				"X-Forwarded-For": {"198.51.100.1"}, "X-Real-IP": {"198.51.100.2"},
			}))
			if rec.Code != http.StatusNoContent || !p.ran || !p.found {
				t.Fatalf("answer %d %s, probe ran %v with Proxied %v", rec.Code, rec.Body.String(), p.ran, p.found)
			}
			want := core.Proxied{
				ListenerID: "L1",
				Client:     netip.MustParseAddr(tt.client),
				Transport:  netip.MustParseAddrPort("172.31.255.2:40000"),
				PublicHost: "panel.example.com",
				Path: core.ProxyPath{ExposureID: "01CHANGE00000000000000000A", Kind: tt.kind,
					ProxyAddr: netip.MustParseAddr("172.31.255.2"), ListenerAddr: netip.MustParseAddrPort("172.31.255.1:9443")},
				Revoked: revoked,
			}
			if !reflect.DeepEqual(p.proxied, want) {
				t.Errorf("Proxied\n%+v\nwant\n%+v", p.proxied, want)
			}
			if rec.Header().Get("Content-Security-Policy") == "" {
				t.Error("the security headers are missing")
			}
		})
	}
}

func TestRuntimeHandlerRefusals(t *testing.T) {
	npm := core.ClientHeaderNPM
	ok := map[string][]string{npm: {"203.0.113.7"}, "X-Forwarded-Proto": {"https"}}
	with := func(k string, v ...string) map[string][]string {
		h := map[string][]string{}
		for key, vs := range ok {
			h[key] = vs
		}
		if v == nil {
			delete(h, k)
		} else {
			h[k] = v
		}
		return h
	}
	closed := make(chan struct{})
	close(closed)
	tests := []struct {
		name      string
		headers   map[string][]string
		revoked   chan struct{}
		isRevoked func(context.Context) (bool, error)
		status    int
		code      string
	}{
		{"no client header, only forwarded ones", map[string][]string{"X-Forwarded-Proto": {"https"},
			"X-Forwarded-For": {"203.0.113.7"}, "X-Real-IP": {"203.0.113.7"}}, nil, nil, http.StatusForbidden, "auth.forbidden"},
		{"the other kind's header", map[string][]string{core.ClientHeaderTunnel: {"203.0.113.7"}, "X-Forwarded-Proto": {"https"}},
			nil, nil, http.StatusForbidden, "auth.forbidden"},
		{"an address with a port", with(npm, "203.0.113.7:80"), nil, nil, http.StatusForbidden, "auth.forbidden"},
		{"a host name", with(npm, "client.example.com"), nil, nil, http.StatusForbidden, "auth.forbidden"},
		{"an empty value", with(npm, ""), nil, nil, http.StatusForbidden, "auth.forbidden"},
		{"two values", with(npm, "203.0.113.7", "198.51.100.1"), nil, nil, http.StatusForbidden, "auth.forbidden"},
		{"a zone", with(npm, "fe80::1%eth0"), nil, nil, http.StatusForbidden, "auth.forbidden"},
		{"plain http", with("X-Forwarded-Proto", "http"), nil, nil, http.StatusForbidden, "auth.forbidden"},
		{"no forwarded protocol", with("X-Forwarded-Proto"), nil, nil, http.StatusForbidden, "auth.forbidden"},
		{"revoked in memory", ok, closed, nil, http.StatusNotFound, "not_found"},
		{"revoked durably", ok, nil, func(context.Context) (bool, error) { return true, nil }, http.StatusNotFound, "not_found"},
		{"state unreadable", ok, nil, func(context.Context) (bool, error) { return false, errors.New("database is locked") },
			http.StatusServiceUnavailable, "unavailable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			revoked, isRevoked := tt.revoked, tt.isRevoked
			if revoked == nil {
				revoked = make(chan struct{})
			}
			if isRevoked == nil {
				isRevoked = rtNotRevoked
			}
			rec, code, p := rtServe(t, rtInfo(npm), revoked, isRevoked, rtRequest(tt.headers))
			if rec.Code != tt.status || code != tt.code || p.ran {
				t.Errorf("answer %d %s (probe ran %v), want %d %s", rec.Code, code, p.ran, tt.status, tt.code)
			}
		})
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mkdir -p /root/.cache/w0c-tmp/30 && TMPDIR=/root/.cache/w0c-tmp/30 GOTOOLCHAIN=local go test -count=1 -run TestRuntimeHandler ./internal/controller/api/`
Expected: FAIL to build with `h.srv.RuntimeHandler undefined (type *Server has no field or method RuntimeHandler)`

- [ ] **Step 3: Write the implementation**

Create `internal/controller/api/runtime.go`:

```go
package api

import (
	"context"
	"net/http"
	"net/netip"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// forwardedProtoHeader is the forwarded protocol a proxy Darkpanel
// configured sets (spec 1.10); NPM proxy hosts force SSL and tunnels are
// HTTPS at the edge, so anything but https means a misconfigured hop.
const forwardedProtoHeader = "X-Forwarded-Proto"

// RuntimeHandler serves one runtime listener: it refuses a request on a
// revoked listener, a forwarded protocol other than https and a missing or
// malformed client header, then serves s.Handler() with core.Proxied in
// the request's context. revoked is closed once the listener is revoked;
// isRevoked re-reads the durable state for every request. The listener
// reads exactly the one client header its proxy sets and overwrites;
// X-Forwarded-For and X-Real-IP come from the client and are never read.
func (s *Server) RuntimeHandler(info core.ListenerInfo, revoked <-chan struct{}, isRevoked func(ctx context.Context) (bool, error)) http.Handler {
	kind := "npm"
	if info.Spec.ClientHeader == core.ClientHeaderTunnel {
		kind = "tunnel"
	}
	path := core.ProxyPath{ExposureID: info.Spec.ChangeID, Kind: kind, ProxyAddr: info.Spec.AllowedPeer, ListenerAddr: info.Spec.Addr}
	return securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := runtimeRevoked(r.Context(), revoked, isRevoked); err != nil {
			writeProblem(w, s.log, err)
			return
		}
		if v := r.Header.Values(forwardedProtoHeader); len(v) != 1 || v[0] != "https" {
			writeProblem(w, s.log, errcode.New(errcode.Forbidden, "a request through an exposure must arrive over https"))
			return
		}
		client, ok := runtimeClient(r.Header.Values(info.Spec.ClientHeader))
		if !ok {
			writeProblem(w, s.log, errcode.New(errcode.Forbidden, "the proxy's "+info.Spec.ClientHeader+" header is missing or malformed"))
			return
		}
		transport, err := netip.ParseAddrPort(r.RemoteAddr)
		if err != nil {
			writeProblem(w, s.log, errcode.New(errcode.Forbidden, "the request has no TCP peer"))
			return
		}
		ctx := core.WithProxied(r.Context(), core.Proxied{
			ListenerID: info.ID,
			Client:     client,
			Transport:  netip.AddrPortFrom(transport.Addr().Unmap(), transport.Port()),
			PublicHost: info.Spec.PublicHost,
			Path:       path,
			Revoked:    revoked,
		})
		s.handler.ServeHTTP(w, r.WithContext(ctx))
	}))
}

// runtimeRevoked refuses a request on a revoked listener. The durable state
// decides: revocation commits it before anything else (spec 1.10), so a
// request racing the in-memory close still finds it. A state that cannot
// be read refuses the request too.
func runtimeRevoked(ctx context.Context, revoked <-chan struct{}, isRevoked func(ctx context.Context) (bool, error)) error {
	select {
	case <-revoked:
		return errcode.New(errcode.NotFound, "this exposure was removed")
	default:
	}
	gone, err := isRevoked(ctx)
	if err != nil {
		return errcode.Wrap(errcode.Unavailable, err, "the exposure's listener state cannot be read")
	}
	if gone {
		return errcode.New(errcode.NotFound, "this exposure was removed")
	}
	return nil
}

// runtimeClient parses the one value of the client header: a bare IP
// address, IPv4-mapped addresses unmapped. Several values, a port or a zone
// make it malformed.
func runtimeClient(values []string) (netip.Addr, bool) {
	if len(values) != 1 {
		return netip.Addr{}, false
	}
	a, err := netip.ParseAddr(strings.TrimSpace(values[0]))
	if err != nil || a.Zone() != "" {
		return netip.Addr{}, false
	}
	return a.Unmap(), true
}
```

`s.handler` is the network listeners' full chain (`securityHeaders`, `recoverPanics`, `authenticate`, the mux) built in `New`; the outer `securityHeaders` only makes the refusals carry the same headers, and setting them twice is harmless.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `TMPDIR=/root/.cache/w0c-tmp/30 GOTOOLCHAIN=local go test -count=1 ./internal/controller/api/ && TMPDIR=/root/.cache/w0c-tmp/30 golangci-lint run --allow-serial-runners ./internal/controller/api/`
Expected: `ok  	github.com/darkraise/darkpanel/internal/controller/api` and `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add internal/controller/api/runtime.go internal/controller/api/runtime_test.go
git commit -m "feat(api): serve runtime listeners behind a proxy"
rm -rf /root/.cache/w0c-tmp/30
```

### Task 31: Proxied requests in the API

This task gives requests carrying `core.Proxied` (C4, set by Task 30's runtime-listener wrapper) the semantics of C11, all inside `internal/controller/api`. `clientAddr` — the one source of the principal's, the audit entry's and the login rate limiter's `Source` — becomes `"<client> via <proxy address>"`; `clientPeer`, which feeds the guard context, becomes the client the proxy named, with port 0 because the proxy does not pass it on; `authenticate` refuses an unsafe request through a runtime listener whose `Origin` is not `https://<PublicHost>` with `auth.csrf` before anything else (login included: login CSRF is a real attack through a public exposure); and `sseStream`, which every server-sent-event response goes through (the events stream and the module-stream relay), adds `X-Accel-Buffering: no` and ends with a `revoked` end frame when the listener's `Revoked` channel closes. The relay does not write a second end frame because `relayWriter.ended` is then set (Task 6). Requests on other listeners are unchanged. C11's `Source` writes the transport's address without its port, because the proxy's source port changes per connection and would give every connection its own rate-limit key. Test helpers carry the prefix `px` (`pxProxied`, `pxProbe`, `pxThrough`); the stream test reuses `sseEnv`, `sseAuth`, `sseServer`, `sseWait` and `newSSERecorder` from `sse_test.go`.

**Files:**
- Modify: `internal/controller/api/server.go:180-200` (`clientAddr`, `clientPeer`), `:271-301` (`authenticate`), and a new function `proxiedOrigin` before `requireCaller`
- Modify: `internal/controller/api/sse.go:1-12` (imports), `:82-160` (`sseStream` and its comment)
- Modify: `internal/controller/api/events.go:13-16` (the `handleEvents` comment)
- Test: `internal/controller/api/proxied_test.go`

**Interfaces:**
- Consumes: C4 (`core.Proxied`, `core.WithProxied`, `core.ProxiedFrom`, `core.ProxyPath`).
- Produces: C11 as amended above (`Source` is `"<Client> via <Transport.Addr()>"`). Task 24's dispatcher reads `core.Proxied` itself for `GuardContext.Via`; Task 41's stream routes reuse `core.ProxiedFrom` the same way.

**Items:** 51

**Depends on:** Task 3, Task 6

**Implementer:** dforge:impl-opus-high
**Evaluation:** files 2 - spec 0 - coupling 1 - risk 3 = 6
**Lenses:** security

- [ ] **Step 1: Write the failing test**

Create `internal/controller/api/proxied_test.go`:

```go
package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
)

// pxProxied is a request through the NPM runtime listener of
// panel.example.com from the client 203.0.113.7.
func pxProxied(revoked <-chan struct{}) core.Proxied {
	return core.Proxied{
		ListenerID: "L1",
		Client:     netip.MustParseAddr("203.0.113.7"),
		Transport:  netip.MustParseAddrPort("172.31.255.2:40000"),
		PublicHost: "panel.example.com",
		Path:       core.ProxyPath{ExposureID: "C1", Kind: "npm", ProxyAddr: netip.MustParseAddr("172.31.255.2")},
		Revoked:    revoked,
	}
}

// pxProbe records the caller and the guard peer of the request that
// reached it.
type pxProbe struct {
	ran     bool
	caller  caller
	peer    core.Peer
	hasPeer bool
}

func (p *pxProbe) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.ran = true
	p.caller, _ = callerFrom(r.Context())
	p.peer, p.hasPeer = core.ClientPeer(r.Context())
	w.WriteHeader(http.StatusNoContent)
}

// pxThrough runs r through authenticate over a pxProbe, proxied when p is
// set.
func pxThrough(h *harness, r *http.Request, p *core.Proxied) (*httptest.ResponseRecorder, *pxProbe) {
	if p != nil {
		r = r.WithContext(core.WithProxied(r.Context(), *p))
	}
	probe := &pxProbe{}
	rec := httptest.NewRecorder()
	h.srv.authenticate(probe).ServeHTTP(rec, r)
	return rec, probe
}

func TestProxiedSourceAndPeer(t *testing.T) {
	h := newHarness(t)
	px := pxProxied(nil)
	tests := []struct {
		name    string
		proxied *core.Proxied
		source  string
		peer    core.Peer
	}{
		{"direct", nil, "192.0.2.10", core.Peer{Addr: netip.MustParseAddr("192.0.2.10"), Port: 51234}},
		{"through a runtime listener", &px, "203.0.113.7 via 172.31.255.2", core.Peer{Addr: netip.MustParseAddr("203.0.113.7")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec, p := pxThrough(h, withToken(request("GET", "/api/v1/auth/session", ""), h.token(core.ScopeRead)), tt.proxied)
			if rec.Code != http.StatusNoContent || !p.ran {
				t.Fatalf("answer %d %s", rec.Code, rec.Body.String())
			}
			if p.caller.principal.Source != tt.source {
				t.Errorf("Source %q, want %q", p.caller.principal.Source, tt.source)
			}
			if !p.hasPeer || p.peer != tt.peer {
				t.Errorf("guard peer %+v (%v), want %+v", p.peer, p.hasPeer, tt.peer)
			}
		})
	}
}

// TestProxiedLoginSource: a login through a runtime listener is audited
// with both addresses.
func TestProxiedLoginSource(t *testing.T) {
	h := newHarness(t)
	body := `{"username":"admin","password":"` + testPassword + `","code":"` + h.code() + `"}`
	r := request("POST", loginPath, body)
	r = r.WithContext(core.WithProxied(r.Context(), pxProxied(nil)))
	rec := h.serve(r)
	if rec.Code != http.StatusOK {
		t.Fatalf("login answer %d %s", rec.Code, rec.Body.String())
	}
	entries, _, err := h.audit.List(context.Background(), core.AuditQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range entries {
		if e.Action == auth.AuditLoginAction && e.Source == "203.0.113.7 via 172.31.255.2" {
			found = true
		}
	}
	if !found {
		t.Errorf("no login entry with source 203.0.113.7 via 172.31.255.2 in %+v", entries)
	}
}

func TestProxiedOrigin(t *testing.T) {
	h := newHarness(t)
	session, csrf := h.login()
	px := pxProxied(nil)
	tests := []struct {
		name    string
		method  string
		path    string
		origin  []string
		proxied *core.Proxied
		status  int
	}{
		{"same origin", "POST", "/api/v1/auth/logout", []string{"https://panel.example.com"}, &px, http.StatusNoContent},
		{"no origin", "POST", "/api/v1/auth/logout", nil, &px, http.StatusNoContent},
		{"another origin", "POST", "/api/v1/auth/logout", []string{"https://evil.example"}, &px, http.StatusForbidden},
		{"plain http origin", "POST", "/api/v1/auth/logout", []string{"http://panel.example.com"}, &px, http.StatusForbidden},
		{"two origins", "POST", "/api/v1/auth/logout", []string{"https://panel.example.com", "https://evil.example"}, &px, http.StatusForbidden},
		{"login from another origin", "POST", loginPath, []string{"https://evil.example"}, &px, http.StatusForbidden},
		{"safe method from another origin", "GET", "/api/v1/auth/session", []string{"https://evil.example"}, &px, http.StatusNoContent},
		{"direct request from another origin", "POST", "/api/v1/auth/logout", []string{"https://evil.example"}, nil, http.StatusNoContent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := withSession(request(tt.method, tt.path, ""), session, csrf)
			for _, o := range tt.origin {
				r.Header.Add("Origin", o)
			}
			rec, _ := pxThrough(h, r, tt.proxied)
			if rec.Code != tt.status {
				t.Fatalf("answer %d %s, want %d", rec.Code, rec.Body.String(), tt.status)
			}
			if tt.status == http.StatusForbidden && !strings.Contains(rec.Body.String(), `"code":"auth.csrf"`) {
				t.Errorf("refusal %s is not auth.csrf", rec.Body.String())
			}
		})
	}
}

// TestProxiedStreamRevoked: a stream through a runtime listener carries
// X-Accel-Buffering: no and ends with a revoked end frame when the
// listener is revoked.
func TestProxiedStreamRevoked(t *testing.T) {
	e := &sseEnv{t: t, auth: &sseAuth{}, clock: coretest.NewFakeClock(testStart), rec: newSSERecorder(),
		src: make(chan sseFrame), done: make(chan string, 1)}
	id := sseIdentity(testStart.Add(time.Hour))
	e.auth.set(id, nil)
	e.srv = sseServer(t, e.auth, nil, e.clock)
	revoked := make(chan struct{})
	r := request("GET", "/api/v1/events", "")
	r = r.WithContext(core.WithProxied(withCaller(r.Context(), caller{principal: id.Principal, identity: id}), pxProxied(revoked)))
	go func() { e.done <- e.srv.sseStream(e.rec, r, e.src) }()
	sseWait(t, "the stream's timers", func() bool { return e.clock.Pending() == 4 })
	if got := e.rec.Header().Get("X-Accel-Buffering"); got != "no" {
		t.Errorf("X-Accel-Buffering %q, want no", got)
	}
	close(revoked)
	if reason := e.end(); reason != "revoked" {
		t.Errorf("end reason %q, want revoked", reason)
	}
	if text := e.rec.text(); !strings.HasSuffix(text, "event: end\ndata: {\"reason\":\"revoked\"}\n\n") {
		t.Errorf("stream %q does not end with a revoked end frame", text)
	}
}

// TestDirectStreamHeaders: a stream on an ordinary listener keeps its
// headers.
func TestDirectStreamHeaders(t *testing.T) {
	e := sseStart(t)
	if got := e.rec.Header().Get("X-Accel-Buffering"); got != "" {
		t.Errorf("X-Accel-Buffering %q on a direct stream", got)
	}
	close(e.src)
	if reason := e.end(); reason != "done" {
		t.Errorf("end reason %q", reason)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mkdir -p /root/.cache/w0c-tmp/31 && TMPDIR=/root/.cache/w0c-tmp/31 GOTOOLCHAIN=local go test -count=1 -run 'TestProxied|TestDirectStream' ./internal/controller/api/`
Expected: FAIL: `TestProxiedSourceAndPeer` (`Source "192.0.2.10", want "203.0.113.7 via 172.31.255.2"` and `guard peer {Addr:192.0.2.10 Port:51234} (true), want {Addr:203.0.113.7 Port:0}`), `TestProxiedLoginSource` (`no login entry with source 203.0.113.7 via 172.31.255.2`), `TestProxiedOrigin` (four rows `answer 204 , want 403`), `TestProxiedStreamRevoked` (`X-Accel-Buffering "", want no` and `the stream did not end`); `TestDirectStreamHeaders` passes.

- [ ] **Step 3: Write the implementation**

In `internal/controller/api/server.go`, replace `clientAddr` and `clientPeer` (with their comments) by:

```go
// clientAddr is the host part of the peer address. Forwarded headers are
// ignored; only a runtime listener names the client, from the one header
// its proxy sets (spec 1.10), and then the address keeps both ends as
// "<client> via <proxy>": the audit log records both, and login rate
// limiting keys on it without the proxy's changing source port.
func clientAddr(r *http.Request) string {
	if p, ok := core.ProxiedFrom(r.Context()); ok {
		return p.Client.String() + " via " + p.Transport.Addr().String()
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// clientPeer is the transport's peer of r, an IPv4-mapped address unmapped,
// or for a request through a runtime listener the client its proxy named,
// whose port the proxy does not pass on. A request without an IP peer, such
// as one on a Unix socket, has none.
func clientPeer(r *http.Request) (core.Peer, bool) {
	if p, ok := core.ProxiedFrom(r.Context()); ok {
		return core.Peer{Addr: p.Client}, true
	}
	ap, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		return core.Peer{}, false
	}
	return core.Peer{Addr: ap.Addr().Unmap(), Port: ap.Port()}, true
}
```

In `authenticate`, directly after the `if peer, ok := clientPeer(r); ok { ... }` block and before `switch routeAccess(r.Method, r.URL.Path) {`, insert:

```go
		if err := proxiedOrigin(r); err != nil {
			writeProblem(w, s.log, err)
			return
		}
```

and add this function directly before the `// requireCaller refuses a request ...` comment:

```go
// proxiedOrigin refuses an unsafe request through a runtime listener whose
// Origin names another site than the exposure's public host. Browsers send
// Origin on every cross-site unsafe request, and a request from the panel's
// own pages names the public host; requests on other listeners keep the
// CSRF token alone.
func proxiedOrigin(r *http.Request) error {
	p, ok := core.ProxiedFrom(r.Context())
	if !ok || safeMethod(r.Method) {
		return nil
	}
	origin := r.Header.Values("Origin")
	if len(origin) == 0 || (len(origin) == 1 && origin[0] == "https://"+p.PublicHost) {
		return nil
	}
	return errcode.New(errcode.CSRF, "the request's Origin is not https://"+p.PublicHost)
}
```

In `internal/controller/api/sse.go`, add `"github.com/darkraise/darkpanel/internal/core"` to the imports before the `errcode` import. Replace the end of `sseStream`'s doc comment, from `// SSERecheck; it returns the end reason:` to `// with done.`, by:

```go
// SSERecheck; it returns the end reason: "done" (src closed), "expired"
// (idle, hard limit, or session or token expired), "revoked" (session or
// token revoked, or the runtime listener the request came through revoked,
// after an end frame saying so) or "error" (a write failed, the client went
// away or CloseStreams ran). A HEAD request gets the headers alone and ends
// at once with done.
```

In `sseStream`, directly after `h.Set("Cache-Control", "no-store")`, insert:

```go
	// A stream through a runtime listener ends when the listener is
	// revoked, and its proxy must pass each event on at once.
	var revoked <-chan struct{}
	if p, ok := core.ProxiedFrom(r.Context()); ok {
		revoked = p.Revoked
		h.Set("X-Accel-Buffering", "no")
	}
```

and in its `select`, directly after the `case <-hard:` branch (`return "expired"`), insert:

```go
		case <-revoked:
			if b, err := sseEncode(endFrame("revoked", nil)); err == nil {
				_ = sseWrite(w, b)
			}
			return "revoked"
```

A nil `revoked` (no runtime listener) never becomes ready, so ordinary streams are unchanged.

In `internal/controller/api/events.go`, replace the last two lines of `handleEvents`'s comment (`// a reset. The stream ends without an end frame; the browser reconnects` / `// with the last id it saw.`) by:

```go
// a reset. The stream ends without an end frame, the browser reconnecting
// with the last id it saw, unless the runtime listener it came through was
// revoked: then it ends with one of reason revoked.
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `TMPDIR=/root/.cache/w0c-tmp/31 GOTOOLCHAIN=local go test -count=1 ./internal/controller/api/ && TMPDIR=/root/.cache/w0c-tmp/31 golangci-lint run --allow-serial-runners ./internal/controller/api/`
Expected: `ok  	github.com/darkraise/darkpanel/internal/controller/api` and `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add internal/controller/api/server.go internal/controller/api/sse.go internal/controller/api/events.go internal/controller/api/proxied_test.go
git commit -m "feat(api): trust a runtime listener's client header"
rm -rf /root/.cache/w0c-tmp/31
```

### Task 32: CLI socket paths and the fuzz target

Two small hooks that later waves depend on. First, the controller-socket commands of `internal/cli` (break-glass reads and every command with `--socket`) resolve the default socket with their own `paths.Resolve(dev)` (`internal/cli/read.go:38`), which `cmd/darkpanel`'s `selectPaths` cannot reach, so a harness build's `--state-dir` (wave 9d) would not move the socket they dial. `cli.Options` gains `SelectPaths`, and `newRoot` passes `selectPaths` (Task 9) into it. The agent socket that `--local` dials keeps its own option, `Options.AgentSocket`, unchanged. Second, the `Makefile` gains the `fuzz` target of spec 1.12, which runs `scripts/fuzz.sh`; that script belongs to wave 9a and does not exist yet, so the target fails with a clear message until it does, in the style of `release-prep`'s missing-tool check (`Makefile:119`). Test helpers: this task reuses `psSwap` from Task 9.

**Files:**
- Modify: `internal/cli/root.go:51-52` (the `Options` struct) and the `withDefaults` function (before `if o.AgentSocket == "" {`)
- Modify: `internal/cli/read.go:14` (the `internal/paths` import), `:38`
- Modify: `cmd/darkpanel/main.go` (`newRoot`, after the `opts.LocalFallbacks` line)
- Modify: `Makefile:34` (`.PHONY`), after the `vuln` target (`Makefile:101-102`)
- Test: `cmd/darkpanel/paths_test.go` (append one test)

**Interfaces:**
- Consumes: Task 9's `selectPaths` and `psSwap`; Task 20's edit of `internal/cli/root.go` (this task edits the same file afterwards).
- Produces: `cli.Options.SelectPaths func(cmd *cobra.Command, dev bool) (paths.Paths, error)` (nil means `paths.Resolve`); the `make fuzz` target running `bash scripts/fuzz.sh`.

**Items:** 52

**Depends on:** Task 9, Task 20

**Implementer:** dforge:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 1 = 3

- [ ] **Step 1: Write the failing test**

Append to `cmd/darkpanel/paths_test.go`:

```go
func TestSocketCommandsResolveThroughSelectPaths(t *testing.T) {
	// --dev and temporary XDG directories keep a command that ignores
	// selectPaths away from the controller socket of this host.
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	want := paths.Under(t.TempDir())
	calls := psSwap(t, want)
	out, code := runArgs("user", "list", "--dev", "--json")
	if code == cli.ExitOK || !strings.Contains(out, want.ControllerSocket) || strings.Join(*calls, ",") != "list" {
		t.Errorf("user list without a controller: exit %d, output %q, selectPaths calls %v; want a failure dialing %s",
			code, out, *calls, want.ControllerSocket)
	}
}
```

`user list` is a break-glass command over the controller socket; no controller runs under the temporary directories, so the command fails, and its error names the socket path it dialed. The test must pass `--dev` with temporary XDG directories: without them, the command as it is before this task dials `/run/darkpanel-controller/controller.sock`, the socket of the controller installed on this host.

- [ ] **Step 2: Run the test to verify it fails**

Run: `mkdir -p /root/.cache/w0c-tmp/32 && TMPDIR=/root/.cache/w0c-tmp/32 GOTOOLCHAIN=local go test -count=1 -run TestSocketCommandsResolveThroughSelectPaths ./cmd/darkpanel/`
Expected: FAIL — exit 1 with an error naming the development socket `<temp>/darkpanel-dev/controller.sock` rather than the selected `<temp>/run/controller.sock`, and `selectPaths calls []`.

- [ ] **Step 3: Write the implementation**

In `internal/cli/root.go`, in `Options`, insert before the `IsTerminal` field:

```go
	// SelectPaths resolves the paths of the controller socket a command
	// dials without --socket; nil means paths.Resolve. cmd is the running
	// command.
	SelectPaths func(cmd *cobra.Command, dev bool) (paths.Paths, error)
```

and in `withDefaults`, insert before `if o.AgentSocket == "" {`:

```go
	if o.SelectPaths == nil {
		o.SelectPaths = func(_ *cobra.Command, dev bool) (paths.Paths, error) { return paths.Resolve(dev) }
	}
```

In `internal/cli/read.go`, in `connect`, replace

```go
			p, err := paths.Resolve(dev)
```

with

```go
			p, err := a.opts.SelectPaths(cmd, dev)
```

and delete the now unused import line `	"github.com/darkraise/darkpanel/internal/paths"`.

In `cmd/darkpanel/main.go`, in `newRoot`, insert after `opts.LocalFallbacks = map[string]cli.LocalFallback{update.ActionRollback: rollbackFallback(stderr)}`:

```go
	opts.SelectPaths = selectPaths
```

In `Makefile`, append ` fuzz` to the end of the `.PHONY:` line, and insert after the `vuln` target:

```make
# fuzz runs the fuzz targets that scripts/fuzz.sh lists, each for its budget.
fuzz:
	@test -f scripts/fuzz.sh || { echo "fuzz: scripts/fuzz.sh is missing; it lists the fuzz targets to run" >&2; exit 1; }
	bash scripts/fuzz.sh
```

(the recipe lines start with a tab).

- [ ] **Step 4: Run the checks to verify they pass**

Run: `TMPDIR=/root/.cache/w0c-tmp/32 GOTOOLCHAIN=local go test -count=1 ./cmd/darkpanel/ ./internal/cli/ && TMPDIR=/root/.cache/w0c-tmp/32 golangci-lint run --allow-serial-runners ./cmd/... ./internal/cli/... && { make fuzz; test $? -eq 2; }`
Expected: `ok` for both packages, `0 issues.`, and `make fuzz` prints `fuzz: scripts/fuzz.sh is missing; it lists the fuzz targets to run` and exits 2, so the last check passes.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/root.go internal/cli/read.go cmd/darkpanel/main.go cmd/darkpanel/paths_test.go Makefile
git commit -m "feat(cli): select socket paths and add fuzz target"
rm -rf /root/.cache/w0c-tmp/32
```

### Task 33: Global page routes and sidebar navigation

Consumes the registry in the two frozen app files: `web/src/app.tsx` mounts `/<global.path>/*` for every entry with a global page, inside the session guard, and `web/src/shell.tsx` builds the sidebar from the host list and the registry: each host under Hosts with the pages of its enabled modules, and the global pages (Tailnet, DERP, Cloudflare) in their own "Network" group (spec 1.12, Consumers). The shell now reads `core.host.list` under `HOSTS_QUERY_KEY`, the same query the hosts and host pages use, so the `host` events already refresh it. The test additions reuse each file's existing helpers; the new shell fixture is `webHost`, and no other top-level helper is declared.

**Files:**
- Modify: `web/src/app.tsx` (whole file)
- Modify: `web/src/shell.tsx` (whole file)
- Test: `web/src/app.test.tsx:1` (import), `:58` (the hosts overview test), and new tests before "an unknown address shows page not found inside the shell"
- Test: `web/src/shell.test.tsx:5` (import), `renderShell` (`:26-48`), and new tests after "shows the navigation with the current page marked"

**Interfaces:**
- Consumes: C22 — `globalPages`, `enabledModules` and `moduleRegistry` from Tasks 10 and 21; `GlobalPage` from `types.ts`; `HOSTS_QUERY_KEY` and `listHosts` from `web/src/api/client.ts`.
- Produces: the routes `/tailnet/*`, `/derp/*` and `/cloudflare/*` inside the session guard, and the sidebar groups. `app.tsx` is frozen after this plan; Task 43 adds the change page route to it.

**Items:** 52

**Depends on:** Task 21

**Implementer:** dforge:impl-sonnet-medium
**Evaluation:** files 2 - spec 0 - coupling 1 - risk 0 = 3

- [ ] **Step 1: Write the failing tests**

In `web/src/app.test.tsx`, replace

```tsx
import { screen } from "@testing-library/react"
```

with

```tsx
import { screen, waitFor, within } from "@testing-library/react"
```

The sidebar will also link each host, so the hosts overview test clicks the link inside the hosts table. Replace

```tsx
    await userEvent.setup().click(await screen.findByRole("link", { name: "web-1" }))
```

with

```tsx
    await userEvent.setup().click(within(await screen.findByRole("table")).getByRole("link", { name: "web-1" }))
```

and insert directly before `test("an unknown address shows page not found inside the shell", ...)`:

```tsx
  test.each([
    ["/tailnet", "Tailnet"],
    ["/derp/regions", "DERP"],
    ["/cloudflare", "Cloudflare"],
  ])("%s mounts the %s global page inside the shell", async (path, label) => {
    stubServer({ body: session })
    renderWithProviders(<App />, { path })
    const link = (await screen.findAllByRole("link", { name: label }))[0]
    await waitFor(() => expect(screen.queryByLabelText(`Loading ${label}`)).toBeNull())
    expect(link?.getAttribute("href")).toBe(`/${path.split("/")[1] ?? ""}`)
    expect(screen.queryByText("Page not found")).toBeNull()
  })
```

In `web/src/shell.test.tsx`, replace

```tsx
import type { ListenerState, Session } from "@/api/types"
```

with

```tsx
import type { Host, ListenerState, Session } from "@/api/types"
```

replace the head of `renderShell`

```tsx
function renderShell(
  listeners: StubResponse,
  path = "/",
  logout: StubResponse = { status: 204 },
) {
  const calls = stubFetch({
    "GET /api/v1/auth/session": { body: session },
    "GET /api/v1/controller/listeners": listeners,
    "POST /api/v1/auth/logout": logout,
  })
```

with

```tsx
function renderShell(
  listeners: StubResponse,
  path = "/",
  logout: StubResponse = { status: 204 },
  hosts: Host[] = [],
) {
  const calls = stubFetch({
    "GET /api/v1/auth/session": { body: session },
    "GET /api/v1/controller/listeners": listeners,
    "POST /api/v1/auth/logout": logout,
    "POST /api/v1/actions/core.host.list/v1/read": { body: { output: { hosts } } },
  })
```

add, after the line `          <Route path="audit" element={<p>Audit content</p>} />` inside `renderShell`,

```tsx
          <Route path="hosts/:hostId/*" element={<p>Host content</p>} />
```

and insert directly after the test "shows the navigation with the current page marked":

```tsx
const webHost: Host = {
  id: "h1", name: "web-1", isLocal: true, online: true, ingestion: null,
  modules: ["hostinfo", "firewall", "tailnet", "docker"].map((id) => ({
    id, version: 1, enabled: id !== "docker", available: true, reason: null, checkedAt: null,
  })),
}

test("the sidebar lists each host with the pages of its enabled modules", async () => {
  renderShell({ body: bound }, "/hosts/h1/hostinfo", { status: 204 }, [webHost])
  await screen.findByText("Host content")
  const hostLink = (await screen.findAllByRole("link", { name: "web-1" }))[0]
  expect(hostLink?.getAttribute("href")).toBe("/hosts/h1")
  expect(screen.getAllByRole("link", { name: "Host information" })[0]?.getAttribute("href")).toBe("/hosts/h1/hostinfo")
  expect(screen.getAllByRole("link", { name: "Firewall" })[0]?.getAttribute("href")).toBe("/hosts/h1/firewall")
  expect(screen.queryByRole("link", { name: "Docker" })).toBeNull()
  expect(screen.getAllByRole("link", { name: "Tailnet" }).map((l) => l.getAttribute("href"))).toEqual(["/tailnet"])
})

test("the global pages have their own sidebar group", async () => {
  renderShell({ body: bound })
  await screen.findByText("Hosts content")
  expect(screen.getAllByText("Network").length).toBeGreaterThan(0)
  for (const [name, href] of [["Tailnet", "/tailnet"], ["DERP", "/derp"], ["Cloudflare", "/cloudflare"]] as const) {
    expect(screen.getAllByRole("link", { name })[0]?.getAttribute("href")).toBe(href)
  }
})
```

- [ ] **Step 2: Run them to make sure they fail**

Run: `mkdir -p /root/.cache/w0c-tmp/33 && TMPDIR=/root/.cache/w0c-tmp/33 pnpm -C web vitest run src/app.test.tsx src/shell.test.tsx`
Expected: FAIL — the three global-page tests (no `Tailnet`, `DERP` or `Cloudflare` link, and "Page not found" shown), "the sidebar lists each host with the pages of its enabled modules" (no `web-1` link) and "the global pages have their own sidebar group" (no "Network" text).

- [ ] **Step 3: Mount the global pages**

Replace the whole of `web/src/app.tsx` with:

```tsx
import { Suspense } from "react"
import { Route, Routes } from "react-router"
import { EmptyState } from "darkraise-ui/components/empty-state"
import { Spinner } from "darkraise-ui/components/spinner"
import { globalPages, moduleRegistry } from "@/modules/registry"
import type { GlobalPage } from "@/modules/types"
import { AuditPage } from "@/pages/audit"
import { HostPage } from "@/pages/host"
import { HostsPage } from "@/pages/hosts"
import { LoginPage } from "@/pages/login"
import { TokensPage } from "@/pages/tokens"
import { RequireSession, SessionProvider } from "@/session/session"
import { Shell } from "@/shell"

function GlobalPageView({ page }: { page: GlobalPage }) {
  const PageRoutes = page.routes
  return (
    <Suspense fallback={<Spinner aria-label={`Loading ${page.label}`} />}>
      <PageRoutes />
    </Suspense>
  )
}

// The routes of spec section 17. Every route but /login sits behind the
// session guard, the modules' global pages included.
export function App() {
  return (
    <SessionProvider>
      <Routes>
        <Route path="/login" element={<LoginPage />} />
        <Route
          element={
            <RequireSession>
              <Shell />
            </RequireSession>
          }
        >
          <Route index element={<HostsPage />} />
          <Route path="hosts/:hostId" element={<HostPage />} />
          <Route path="hosts/:hostId/:moduleId/*" element={<HostPage />} />
          <Route path="audit" element={<AuditPage />} />
          <Route path="settings/tokens" element={<TokensPage />} />
          {globalPages(moduleRegistry).map((page) => (
            <Route key={page.path} path={`${page.path}/*`} element={<GlobalPageView page={page} />} />
          ))}
          <Route path="*" element={<EmptyState title="Page not found" description="Nothing lives at this address." />} />
        </Route>
      </Routes>
    </SessionProvider>
  )
}
```

- [ ] **Step 4: Build the sidebar from the hosts and the registry**

Replace the whole of `web/src/shell.tsx` with:

```tsx
import { useEffect, useState } from "react"
import { Outlet } from "react-router"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { History, KeyRound, Server } from "lucide-react"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { SidebarLayout, type NavGroup, type NavItem } from "darkraise-ui/layout"
import { HOSTS_QUERY_KEY, errorMessage, getListenerState, listHosts } from "@/api/client"
import { connectEvents } from "@/api/events"
import type { Host } from "@/api/types"
import { enabledModules, moduleRegistry } from "@/modules/registry"
import { useSession } from "@/session/use-session"

// navigation lists each host with its module pages under Hosts, and the
// pages that belong to no host in their own group. Until the host list has
// loaded, Hosts has no children.
function navigation(hosts: Host[]): NavGroup[] {
  const hostItems: NavItem[] = hosts.map((host) => {
    const href = `/hosts/${encodeURIComponent(host.id)}`
    return {
      label: host.name,
      href,
      children: enabledModules(moduleRegistry, host).map((m) => ({ label: m.label, href: `${href}/${m.id}`, icon: m.icon })),
    }
  })
  const globals: NavItem[] = moduleRegistry.flatMap((entry) =>
    entry.global === undefined ? [] : [{ label: entry.global.label, href: `/${entry.global.path}`, icon: entry.icon }],
  )
  return [
    {
      items: [
        { label: "Hosts", href: "/", icon: Server, children: hostItems },
        { label: "Audit log", href: "/audit", icon: History },
      ],
    },
    ...(globals.length === 0 ? [] : [{ label: "Network", items: globals }]),
    { label: "Settings", items: [{ label: "API tokens", href: "/settings/tokens", icon: KeyRound }] },
  ]
}

// ListenerNotice says where the controller listens; without a Tailscale
// address it serves loopback only, and the footer says so (spec section 13).
function ListenerNotice() {
  const listeners = useQuery({ queryKey: ["listeners"], queryFn: getListenerState, refetchOnWindowFocus: true })
  if (listeners.isPending) return null
  if (listeners.isError) {
    return (
      <p role="status" className="px-3 py-2 text-xs text-destructive">
        {`Listener state unavailable: ${errorMessage(listeners.error)}`}
      </p>
    )
  }
  return (
    <p role="status" className={listeners.data.tailscaleBound ? "px-3 py-2 text-xs text-muted-foreground" : "px-3 py-2 text-xs text-warning"}>
      {listeners.data.message}
    </p>
  )
}

export function Shell() {
  const { session, logout } = useSession()
  const [logoutError, setLogoutError] = useState<string | null>(null)
  const client = useQueryClient()
  const hosts = useQuery({ queryKey: HOSTS_QUERY_KEY, queryFn: listHosts })
  useEffect(() => connectEvents(client), [client])

  async function logOut() {
    setLogoutError(null)
    try {
      await logout()
    } catch (err) {
      setLogoutError(`Logout failed: ${errorMessage(err)}`)
    }
  }

  return (
    <SidebarLayout
      nav={navigation(hosts.data?.hosts ?? [])}
      showThemeSwitcher
      showLayoutSwitcher={false}
      notificationSlot={null}
      user={session === null ? undefined : { name: session.user.username, email: `Role: ${session.user.role}` }}
      onLogout={() => void logOut()}
      navFooter={<ListenerNotice />}
    >
      {logoutError !== null && (
        <Alert variant="destructive" role="alert" className="mb-4">
          <AlertDescription>{logoutError}</AlertDescription>
        </Alert>
      )}
      <Outlet />
    </SidebarLayout>
  )
}
```

- [ ] **Step 5: Run the tests and the checks**

Run: `TMPDIR=/root/.cache/w0c-tmp/33 pnpm -C web vitest run src/app.test.tsx src/shell.test.tsx`
Expected: PASS, `Tests  22 passed (22)`.

Run: `TMPDIR=/root/.cache/w0c-tmp/33 make lint-web test-web`
Expected: no ESLint finding (`navigation` stays unexported, so react-refresh's only-export-components rule holds) and no `tsc` finding, and every Vitest test passes.

- [ ] **Step 6: Commit**

```bash
git add web/src/app.tsx web/src/shell.tsx web/src/app.test.tsx web/src/shell.test.tsx
git commit -m "feat(web): mount global pages and module navigation"
rm -rf /root/.cache/w0c-tmp/33
```

### Task 34: Harness script that pulls the pinned images

This task adds `PullScript`, a provisioning fragment that a later certificate test appends to `TargetScript(o)` in its `vm.Config.Provision` to pull the images Task 22 pins on that target, by digest, once Docker runs. It follows how the harness already gets packages: the target downloads them itself over its user-mode management NIC (`test/harness/provision.go:21-33`, `fetch`), so the target also pulls its images itself, and Docker verifies every pulled blob against the pinned digest, the role `sha256sum` plays for a `.deb`. A pull that fails three times fails provisioning with the same "update the harness pins" wording as `fetch`. TestM1 and TestM2 do not append it, so their VMs pull nothing and their run time is unchanged; wave 6a's NPM harness test is the first user. The script's retry delay is the shell variable `pull_retry_delay` (default 5 s), which only the test sets. Test function: `TestPullScript`; it uses Task 22's `validContainers` and the existing `testPackages` and `bashSyntax`, and adds no helper.

**Files:**
- Modify: `test/harness/provision.go` (imports, `pullFunc`, `PullScript`)
- Test: `test/harness/provision_test.go`

**Interfaces:**
- Consumes: Task 22's `Container` and `validContainers`; `TargetScript`, `testPackages`, `bashSyntax`.
- Produces (later waves' certificate tests): `func PullScript(cs ...Container) string` — append to `TargetScript(o)`; then run each image by its `Ref`.

**Items:** 52

**Depends on:** Task 15, Task 22

**Implementer:** dforge:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 0 = 3

- [ ] **Step 1: Write the failing test**

In `test/harness/provision_test.go`, add `"strconv"` to the imports (between `"path/filepath"` and `"strings"`) and append:

```go
func TestPullScript(t *testing.T) {
	cs := validContainers()
	s := PullScript(cs...)
	for _, c := range cs {
		if want := "pull_image '" + c.Name + "' '" + c.Ref + "'\n"; !strings.Contains(s, want) {
			t.Errorf("PullScript lacks %q:\n%s", want, s)
		}
	}
	o := OS{ID: "debian-13", Family: "debian", VersionID: "13", Packages: testPackages("debian.13~trixie", "2")}
	bashSyntax(t, TargetScript(o)+s)

	// docker is a function that fails until its call number reaches ok and
	// logs its arguments, so the retries run without a daemon.
	pull := func(ok int) (string, string, error) {
		log := filepath.Join(t.TempDir(), "docker.log")
		script := "pull_retry_delay=0\nn=0\ndocker() { n=$((n+1)); echo \"$*\" >> '" + log + "'; [ \"$n\" -ge " +
			strconv.Itoa(ok) + " ]; }\n" + PullScript(cs[0]) + "echo pulled\n"
		out, err := exec.Command("bash", "-c", script).CombinedOutput() //nolint:gosec // G204: the test's own script
		calls, _ := os.ReadFile(log)                                    //nolint:gosec // G304: the test's own temporary file
		return strings.TrimSpace(string(out)), string(calls), err
	}
	out, calls, err := pull(2)
	if want := strings.Repeat("pull --quiet "+cs[0].Ref+"\n", 2); err != nil || out != "pulled" || calls != want {
		t.Errorf("a pull that succeeds on its second attempt: %v, output %q, calls %q; want success after %q", err, out, calls, want)
	}
	out, calls, err = pull(4)
	if want := "pinned image pebble (" + cs[0].Ref + ") cannot be pulled; update the harness pins"; err == nil || out != want ||
		strings.Count(calls, "\n") != 3 {
		t.Errorf("a pull that always fails: %v, output %q, calls %q; want a failure saying %q after 3 attempts", err, out, calls, want)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mkdir -p /root/.cache/w0c-tmp/34 && TMPDIR=/root/.cache/w0c-tmp/34 GOTOOLCHAIN=local go test -count=1 -tags harness -run TestPullScript ./test/harness/`
Expected: FAIL, the package does not compile: `undefined: PullScript`.

- [ ] **Step 3: Write the implementation**

In `test/harness/provision.go`, add `"fmt"` to the imports (before `"strconv"`). After `fetchFunc` add:

```go
// pullFunc pulls a pinned image by digest once Docker runs, retrying a
// registry that fails for a moment, and fails with the pins message when it
// cannot. pull_retry_delay exists for the script's own test.
const pullFunc = `pull_image() {
  for _ in 1 2 3; do
    if docker pull --quiet "$2" >/dev/null; then
      return 0
    fi
    sleep "${pull_retry_delay:-5}"
  done
  echo "pinned image $1 ($2) cannot be pulled; update the harness pins" >&2
  exit 1
}
`
```

After `TargetScript` add:

```go
// PullScript is a provisioning fragment, appended to TargetScript, that
// pulls cs on the target by digest, as the target fetches its packages
// itself. Docker checks every pulled blob against the digest, so a changed
// image cannot pass as the pinned one; a test then runs each by its Ref.
func PullScript(cs ...Container) string {
	var b strings.Builder
	b.WriteString(pullFunc)
	for _, c := range cs {
		fmt.Fprintf(&b, "pull_image '%s' '%s'\n", c.Name, c.Ref)
	}
	return b.String()
}
```

A container's name and reference contain no quote: `Pins.check` admits only `RequiredContainers` and references matching `containerRef`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `mkdir -p /root/.cache/w0c-tmp/34 && TMPDIR=/root/.cache/w0c-tmp/34 GOTOOLCHAIN=local go test -count=1 -tags harness  ./test/harness/ && TMPDIR=/root/.cache/w0c-tmp/34 GOTOOLCHAIN=local go vet -tags harness ./test/... && TMPDIR=/root/.cache/w0c-tmp/34 golangci-lint run --allow-serial-runners ./test/harness/...`
Expected: `ok  	github.com/darkraise/darkpanel/test/harness`, no vet output, `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add test/harness/provision.go test/harness/provision_test.go
git commit -m "test(harness): pull pinned images on a target"
```

Empty `/root/.cache/w0c-tmp/34` when the task ends.

### Task 35: Cloud credential sealing and capability probes

Add the rest of C8 to `internal/controller/cloud`: `SealCredential` and `OpenCredential`, which seal an account secret with associated data `<table>.<column>.<id>` (ADR 0005), so a ciphertext copied to another row, column or table does not open; and `ProbeReads`, which reports read capabilities proven by harmless reads (`ok`, or `missing` with the error's code) and lists write capabilities as `unverified`, since a write capability is known only once a write succeeds (spec 1.9). The task also exports the three state names as constants `CapabilityOK`, `CapabilityMissing` and `CapabilityUnverified`, an addition to C8. The sealing test uses the production `secret.Sealer`. Test helpers carry the prefix `cr`.

**Files:**
- Create: `internal/controller/cloud/credentials.go`
- Create: `internal/controller/cloud/probe.go`
- Test: `internal/controller/cloud/credentials_test.go`
- Test: `internal/controller/cloud/probe_test.go`

**Interfaces:**
- Consumes: C1 (`errcode.CloudPermissionMissing`, in the test), C8's package from Task 23.
- Produces: C8's `SealCredential`, `OpenCredential`, `Capability`, `Probe`, `ProbeReads`, and the constants `CapabilityOK`, `CapabilityMissing`, `CapabilityUnverified`.

**Items:** 50

**Depends on:** Task 1, Task 23

**Implementer:** dforge:impl-sonnet-high
**Evaluation:** files 2 - spec 0 - coupling 1 - risk 2 = 5

- [ ] **Step 1: Write the failing tests**

Create `internal/controller/cloud/credentials_test.go`:

```go
package cloud

import (
	"bytes"
	"testing"

	"github.com/darkraise/darkpanel/internal/core/secret"
)

func crSealer(t *testing.T) *secret.Sealer {
	t.Helper()
	s, err := secret.NewSealer(bytes.Repeat([]byte{9}, secret.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestCredentialRoundTrip(t *testing.T) {
	s := crSealer(t)
	ct, err := SealCredential(s, "tailnet_accounts", "secret_ct", "acc1", []byte("client-secret"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ct, []byte("client-secret")) {
		t.Fatal("the ciphertext contains the secret")
	}
	pt, err := OpenCredential(s, "tailnet_accounts", "secret_ct", "acc1", ct)
	if err != nil || string(pt) != "client-secret" {
		t.Fatalf("open = %q, %v", pt, err)
	}
}

func TestCredentialIsBoundToItsRow(t *testing.T) {
	s := crSealer(t)
	ct, err := SealCredential(s, "tailnet_accounts", "secret_ct", "acc1", []byte("client-secret"))
	if err != nil {
		t.Fatal(err)
	}
	for _, place := range [][3]string{
		{"tailnet_accounts", "secret_ct", "acc2"},
		{"tailnet_accounts", "token_ct", "acc1"},
		{"cloudflare_accounts", "secret_ct", "acc1"},
	} {
		if _, err := OpenCredential(s, place[0], place[1], place[2], ct); err == nil {
			t.Errorf("opened at %v, a place it was not sealed for", place)
		}
	}
}

func TestCredentialNeedsItsPlace(t *testing.T) {
	s := crSealer(t)
	if _, err := SealCredential(s, "tailnet_accounts", "", "acc1", []byte("x")); err == nil {
		t.Fatal("sealed without a column")
	}
	if _, err := OpenCredential(s, "", "secret_ct", "acc1", []byte("x")); err == nil {
		t.Fatal("opened without a table")
	}
}
```

Create `internal/controller/cloud/probe_test.go`:

```go
package cloud

import (
	"context"
	"reflect"
	"testing"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

func TestProbeReads(t *testing.T) {
	probes := []Probe{
		{Name: "devices:read", Read: func(context.Context) error { return nil }},
		{Name: "policy:read", Read: func(context.Context) error {
			return errcode.New(errcode.CloudPermissionMissing, "tailscale answered 403")
		}},
	}
	got := ProbeReads(t.Context(), probes, []string{"devices:write", "auth_keys:write"})
	want := []Capability{
		{Name: "devices:read", State: CapabilityOK},
		{Name: "policy:read", State: CapabilityMissing, Detail: "cloud.permission_missing"},
		{Name: "devices:write", State: CapabilityUnverified},
		{Name: "auth_keys:write", State: CapabilityUnverified},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("capabilities = %+v, want %+v", got, want)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mkdir -p /root/.cache/w0c-tmp/35 && export TMPDIR=/root/.cache/w0c-tmp/35 GOTOOLCHAIN=local && go test -count=1 ./internal/controller/cloud/`
Expected: FAIL to build: `undefined: SealCredential`, `undefined: OpenCredential`, `undefined: Probe`, `undefined: ProbeReads`.

- [ ] **Step 3: Write the implementation**

Create `internal/controller/cloud/credentials.go`:

```go
package cloud

import (
	"errors"
	"fmt"

	"github.com/darkraise/darkpanel/internal/core"
)

// credentialAD is the associated data that binds a sealed account secret
// to its row (ADR 0005), so a ciphertext copied to another row or column
// does not open.
func credentialAD(table, column, id string) (string, error) {
	if table == "" || column == "" || id == "" {
		return "", errors.New("cloud: a sealed credential needs a table, a column and a row id")
	}
	return table + "." + column + "." + id, nil
}

// SealCredential seals an account secret with associated data
// "<table>.<column>.<id>".
func SealCredential(s core.Sealer, table, column, id string, secret []byte) ([]byte, error) {
	ad, err := credentialAD(table, column, id)
	if err != nil {
		return nil, err
	}
	ct, err := s.Seal(ad, secret)
	if err != nil {
		return nil, fmt.Errorf("cloud: seal %s: %w", ad, err)
	}
	return ct, nil
}

// OpenCredential opens what SealCredential sealed for the same table,
// column and id.
func OpenCredential(s core.Sealer, table, column, id string, ciphertext []byte) ([]byte, error) {
	ad, err := credentialAD(table, column, id)
	if err != nil {
		return nil, err
	}
	pt, err := s.Open(ad, ciphertext)
	if err != nil {
		return nil, fmt.Errorf("cloud: open %s: %w", ad, err)
	}
	return pt, nil
}
```

Create `internal/controller/cloud/probe.go`:

```go
package cloud

import (
	"context"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// The states of a probed capability.
const (
	CapabilityOK         = "ok"
	CapabilityMissing    = "missing"
	CapabilityUnverified = "unverified"
)

// Capability is one probed provider capability.
type Capability struct {
	Name   string `json:"name"`
	State  string `json:"state"`            // "ok" | "missing" | "unverified"
	Detail string `json:"detail,omitempty"` // a missing capability: the error code
}

// Probe is one harmless read that proves a read capability.
type Probe struct {
	Name string
	Read func(ctx context.Context) error
}

// ProbeReads runs every probe and reports each as ok or missing, then
// appends writes as unverified, in the order given. A write capability is
// known only once a write succeeds, so connecting an account never writes.
func ProbeReads(ctx context.Context, probes []Probe, writes []string) []Capability {
	out := make([]Capability, 0, len(probes)+len(writes))
	for _, p := range probes {
		if err := p.Read(ctx); err != nil {
			out = append(out, Capability{Name: p.Name, State: CapabilityMissing, Detail: string(errcode.From(err).Code)})
			continue
		}
		out = append(out, Capability{Name: p.Name, State: CapabilityOK})
	}
	for _, w := range writes {
		out = append(out, Capability{Name: w, State: CapabilityUnverified})
	}
	return out
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `mkdir -p /root/.cache/w0c-tmp/35 && export TMPDIR=/root/.cache/w0c-tmp/35 GOTOOLCHAIN=local && go test -count=1 -race ./internal/controller/cloud/... && go vet ./internal/controller/cloud/... && golangci-lint run --allow-serial-runners ./internal/controller/cloud/...`
Expected: `ok` for `internal/controller/cloud` and `internal/controller/cloud/cloudfake`, then `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add internal/controller/cloud/credentials.go internal/controller/cloud/probe.go internal/controller/cloud/credentials_test.go internal/controller/cloud/probe_test.go
git commit -m "feat(cloud): seal credentials and probe capabilities"
rm -rf /root/.cache/w0c-tmp/35
```

### Task 36: Web certificate sources in the leaf

The web leaf (`internal/controller/listen/leaf.go`) learns to ask certificate sources before it serves the Darkpanel CA leaf, and to add the sources' extra DNS names when it issues. This task creates the frozen `sources.go` exactly as spec 1.12 states it and the wave-4 stub `tailscale_cert.go`, whose source always falls through and whose names are none, so behaviour is unchanged until wave 4 fills the stub. `Leaf.Ensure` gains a context, because the extra names come from an internal read. A failing source is logged and skipped, and failing names keep the current leaf's names, so a broken source never takes the listeners down. Test helpers of this task carry the prefix `cs`.

**Files:**
- Create: `internal/controller/listen/sources.go`
- Create: `internal/controller/listen/tailscale_cert.go`
- Modify: `internal/controller/listen/leaf.go` (whole file replaced below)
- Modify: `internal/controller/listen/service.go:154` (the one `Ensure` call)
- Modify: `internal/controller/listen/leaf_test.go` (every `l.Ensure(` call gains `context.Background(), `)
- Test: `internal/controller/listen/sources_test.go`

**Interfaces:**
- Consumes: C2 (`ext.Dispatcher`, the type of `SourceDeps.Dispatch`).
- Produces: C13's `CertSource`, `SourceDeps`, `certSources`, `extraNames`, `tailscaleCertSource`, `tailscaleNames`; the unexported `(*Leaf).useSources(d SourceDeps)` that Task 42 calls; `(*Leaf).Ensure(ctx context.Context, ips []netip.Addr) (bool, error)`.

**Items:** 52

**Depends on:** Task 19

**Implementer:** dforge:impl-sonnet-high
**Evaluation:** files 2 - spec 0 - coupling 1 - risk 2 = 5

- [ ] **Step 1: Write the failing test**

Create `internal/controller/listen/sources_test.go`:

```go
package listen

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"
)

// csSource is a certificate source that answers cert and err.
type csSource struct {
	cert  *tls.Certificate
	err   error
	calls int
}

func (s *csSource) Certificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	s.calls++
	return s.cert, s.err
}

// csDNSNames are the DNS SANs of l's current CA leaf.
func csDNSNames(t *testing.T, l *Leaf) []string {
	t.Helper()
	cur := l.cur.Load()
	if cur == nil {
		t.Fatal("no leaf issued")
	}
	leaf, err := x509.ParseCertificate(cur.cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	return leaf.DNSNames
}

func TestLeafAsksSourcesInOrder(t *testing.T) {
	l, _, _ := newLeaf(t)
	log := &logBuffer{}
	l.log = slog.New(slog.NewTextHandler(log, nil))
	if _, err := l.Ensure(context.Background(), addrs("127.0.0.1")); err != nil {
		t.Fatal(err)
	}
	ca := current(t, l)
	failing := &csSource{err: errors.New("tailscaled is down")}
	silent := &csSource{}
	own := &csSource{cert: &tls.Certificate{Certificate: [][]byte{{1}}}}
	later := &csSource{cert: &tls.Certificate{Certificate: [][]byte{{2}}}}

	l.sources = []CertSource{failing, silent, own, later}
	got, err := l.GetCertificate(&tls.ClientHelloInfo{ServerName: "panel.example.ts.net"})
	if err != nil || got != own.cert {
		t.Fatalf("GetCertificate = %v, %v; want the first certificate a source supplies", got, err)
	}
	if failing.calls != 1 || silent.calls != 1 || later.calls != 0 {
		t.Errorf("source calls %d, %d, %d; want 1, 1, 0", failing.calls, silent.calls, later.calls)
	}
	if !strings.Contains(log.String(), "a certificate source failed") {
		t.Errorf("the failing source was not logged: %s", log.String())
	}

	l.sources = []CertSource{failing, silent}
	if got, err := l.GetCertificate(&tls.ClientHelloInfo{}); err != nil || got != ca {
		t.Fatalf("GetCertificate = %v, %v; want the CA leaf when every source falls through", got, err)
	}
}

func TestLeafExtraNames(t *testing.T) {
	ctx := context.Background()
	l, _, _ := newLeaf(t)
	l.log = slog.New(slog.DiscardHandler)
	var names []string
	var namesErr error
	l.names = func(context.Context) ([]string, error) { return names, namesErr }

	// The names fail before any leaf exists: the leaf is issued without them.
	namesErr = errors.New("tailscaled is down")
	if issued, err := l.Ensure(ctx, addrs("100.64.0.1")); err != nil || !issued {
		t.Fatalf("Ensure = %v, %v; want a leaf despite the failing names", issued, err)
	}
	if got := csDNSNames(t, l); !slices.Equal(got, []string{"panel", "localhost"}) {
		t.Fatalf("DNS SANs = %v", got)
	}

	namesErr = nil
	names = []string{"panel.tail1234.ts.net", "localhost", "panel", "", "alias.tail1234.ts.net", "panel.tail1234.ts.net"}
	if issued, err := l.Ensure(ctx, addrs("100.64.0.1")); err != nil || !issued {
		t.Fatalf("Ensure with new names = %v, %v; want a new leaf", issued, err)
	}
	want := []string{"panel", "localhost", "alias.tail1234.ts.net", "panel.tail1234.ts.net"}
	if got := csDNSNames(t, l); !slices.Equal(got, want) {
		t.Fatalf("DNS SANs = %v, want %v", got, want)
	}
	if issued, err := l.Ensure(ctx, addrs("100.64.0.1")); err != nil || issued {
		t.Fatalf("Ensure with the same names in another order = %v, %v; want no new leaf", issued, err)
	}

	// A failure keeps the current names and issues nothing.
	namesErr = errors.New("tailscaled is down")
	if issued, err := l.Ensure(ctx, addrs("100.64.0.1")); err != nil || issued {
		t.Fatalf("Ensure with failing names = %v, %v; want the current leaf kept", issued, err)
	}
	if got := csDNSNames(t, l); !slices.Equal(got, want) {
		t.Fatalf("DNS SANs after a failure = %v, want %v", got, want)
	}

	namesErr = nil
	names = []string{"panel.tail1234.ts.net"}
	if issued, err := l.Ensure(ctx, addrs("100.64.0.1")); err != nil || !issued {
		t.Fatalf("Ensure with a name gone = %v, %v; want a new leaf", issued, err)
	}
	if got := csDNSNames(t, l); !slices.Equal(got, []string{"panel", "localhost", "panel.tail1234.ts.net"}) {
		t.Fatalf("DNS SANs = %v", got)
	}
}

func TestLeafWithStubSources(t *testing.T) {
	l, _, _ := newLeaf(t)
	l.useSources(SourceDeps{LocalHostID: "H1"})
	if _, err := l.Ensure(context.Background(), addrs("127.0.0.1")); err != nil {
		t.Fatal(err)
	}
	if got := csDNSNames(t, l); !slices.Equal(got, []string{"panel", "localhost"}) {
		t.Fatalf("DNS SANs = %v; the stub sources add no name", got)
	}
	if got, err := l.GetCertificate(&tls.ClientHelloInfo{ServerName: "panel"}); err != nil || got != l.cur.Load().cert {
		t.Fatalf("GetCertificate = %v, %v; want the CA leaf through the stub source", got, err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `mkdir -p /root/.cache/w0c-tmp/36 && TMPDIR=/root/.cache/w0c-tmp/36 GOTOOLCHAIN=local go test -count=1 -run 'TestLeafAsksSourcesInOrder|TestLeafExtraNames|TestLeafWithStubSources' ./internal/controller/listen/`
Expected: FAIL to build, with errors such as `undefined: CertSource`, `l.sources undefined`, `l.names undefined` and `too many arguments in call to l.Ensure`.

- [ ] **Step 3: Write the implementation**

Create `internal/controller/listen/sources.go` (frozen after this plan; it is spec 1.12's text):

```go
package listen

import (
	"context"
	"crypto/tls"
	"log/slog"

	"github.com/darkraise/darkpanel/internal/core/ext"
)

// CertSource supplies the web leaf for a handshake; nil, nil falls
// through to the next source and finally to the Darkpanel CA leaf.
type CertSource interface {
	Certificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error)
}

// SourceDeps is what the certificate sources may use. Their internal reads
// run as system:listen.cert with the admin scope.
type SourceDeps struct {
	Dispatch    ext.Dispatcher
	LocalHostID string
	Log         *slog.Logger
}

func certSources(d SourceDeps) []CertSource { return []CertSource{tailscaleCertSource(d)} }

func extraNames(ctx context.Context, d SourceDeps) ([]string, error) { return tailscaleNames(ctx, d) }
```

Create `internal/controller/listen/tailscale_cert.go` (wave 4 replaces both function bodies):

```go
package listen

import (
	"context"
	"crypto/tls"
)

// tailscaleCertSource serves the host's Tailscale certificate for its
// MagicDNS name. Until the tailscale module provides one it falls through to
// the Darkpanel CA leaf.
func tailscaleCertSource(SourceDeps) CertSource { return fallThrough{} }

// tailscaleNames are the MagicDNS names the CA leaf also carries; none until
// the tailscale module provides them.
func tailscaleNames(context.Context, SourceDeps) ([]string, error) { return nil, nil }

// fallThrough is a source that never supplies a certificate.
type fallThrough struct{}

func (fallThrough) Certificate(*tls.ClientHelloInfo) (*tls.Certificate, error) { return nil, nil }
```

Replace the whole of `internal/controller/listen/leaf.go` with:

```go
package listen

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
)

// Leaf lifetimes (ADR 0010).
const (
	LeafValidity   = 90 * 24 * time.Hour
	LeafRenewAfter = 60 * 24 * time.Hour
)

// Leaf holds the HTTPS listeners' certificate, issued by the web authority
// and kept in memory only. Its SANs are the hostname, localhost, the extra
// names of the certificate sources and the IP addresses passed to Ensure. A
// handshake asks the certificate sources first and falls back to it.
type Leaf struct {
	auth     *Authority
	clock    core.Clock
	hostname string
	sources  []CertSource
	names    func(ctx context.Context) ([]string, error) // nil: no extra names
	log      *slog.Logger
	mu       sync.Mutex // serialises Ensure
	cur      atomic.Pointer[issuedLeaf]
}

type issuedLeaf struct {
	cert  *tls.Certificate
	ips   []netip.Addr
	names []string // the extra names, sorted
	at    time.Time
}

// NewLeaf returns a leaf manager for hostname without certificate sources;
// no certificate exists until the first Ensure.
func NewLeaf(a *Authority, clock core.Clock, hostname string) *Leaf {
	return &Leaf{auth: a, clock: clock, hostname: hostname, log: slog.New(slog.DiscardHandler)}
}

// useSources makes the leaf ask d's certificate sources at every handshake
// and add their extra names at every issuance.
func (l *Leaf) useSources(d SourceDeps) {
	if d.Log != nil {
		l.log = d.Log
	}
	l.sources = certSources(d)
	l.names = func(ctx context.Context) ([]string, error) { return extraNames(ctx, d) }
}

// Ensure issues a new leaf when none exists, when ips or the sources' extra
// names differ from the current leaf's, or when LeafRenewAfter has passed
// since the current one was issued. It reports whether it issued one. When
// the extra names cannot be read, the current leaf's names are kept, so a
// failing source never takes the listeners down.
func (l *Leaf) Ensure(ctx context.Context, ips []netip.Addr) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	want := normalizeAddrs(ips)
	cur := l.cur.Load()
	names := l.extraNames(ctx, cur)
	now := l.clock.Now()
	if cur != nil && slices.Equal(cur.ips, want) && slices.Equal(cur.names, names) && now.Sub(cur.at) < LeafRenewAfter {
		return false, nil
	}
	cert, err := l.auth.issue(l.hostname, names, want, now)
	if err != nil {
		return false, err
	}
	l.cur.Store(&issuedLeaf{cert: cert, ips: want, names: names, at: now})
	return true, nil
}

// extraNames reads the sources' extra names, sorted, without duplicates and
// without the names every leaf carries. On an error it logs and returns the
// names of cur.
func (l *Leaf) extraNames(ctx context.Context, cur *issuedLeaf) []string {
	if l.names == nil {
		return nil
	}
	list, err := l.names(ctx)
	if err != nil {
		l.log.Warn("listen: the certificate sources' names are unavailable; keeping the current names", "err", err)
		if cur == nil {
			return nil
		}
		return cur.names
	}
	out := make([]string, 0, len(list))
	for _, n := range list {
		if n != "" && n != "localhost" && n != l.hostname {
			out = append(out, n)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// GetCertificate serves tls.Config.GetCertificate: the first certificate a
// source supplies, else the chain [leaf, root]. A source that fails is
// logged and skipped.
func (l *Leaf) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	for _, src := range l.sources {
		cert, err := src.Certificate(hello)
		if err != nil {
			l.log.Warn("listen: a certificate source failed; trying the next", "server_name", hello.ServerName, "err", err)
			continue
		}
		if cert != nil {
			return cert, nil
		}
	}
	cur := l.cur.Load()
	if cur == nil {
		return nil, errors.New("listen: no web certificate issued yet")
	}
	return cur.cert, nil
}

// TLSConfig returns the HTTPS listeners' configuration: TLS 1.2 or later and
// the current leaf.
func (l *Leaf) TLSConfig() *tls.Config {
	return &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: l.GetCertificate}
}

func normalizeAddrs(list []netip.Addr) []netip.Addr {
	out := make([]netip.Addr, 0, len(list))
	for _, a := range list {
		out = append(out, a.Unmap())
	}
	slices.SortFunc(out, netip.Addr.Compare)
	return slices.Compact(out)
}

func (a *Authority) issue(hostname string, extra []string, ips []netip.Addr, now time.Time) (*tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("listen: web leaf: generate key: %w", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, err
	}
	names := []string{"localhost"}
	if hostname != "" && hostname != "localhost" {
		names = []string{hostname, "localhost"}
	}
	names = append(names, extra...)
	ipSANs := make([]net.IP, 0, len(ips))
	for _, ip := range ips {
		ipSANs = append(ipSANs, ip.AsSlice())
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: names[0]},
		DNSNames:              names,
		IPAddresses:           ipSANs,
		NotBefore:             now.Add(-backdate),
		NotAfter:              now.Add(LeafValidity),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, a.cert, &key.PublicKey, a.key)
	if err != nil {
		return nil, fmt.Errorf("listen: web leaf: %w", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("listen: web leaf: %w", err)
	}
	return &tls.Certificate{Certificate: [][]byte{der, a.cert.Raw}, PrivateKey: key, Leaf: leaf}, nil
}
```

In `internal/controller/listen/service.go`, inside `(*Service).apply`, change

```go
		issued, err := s.leaf.Ensure(ips)
```

to

```go
		issued, err := s.leaf.Ensure(ctx, ips)
```

Update the existing leaf tests to the new signature (eight call sites, all of the form `l.Ensure(`; the file already imports `context`):

```bash
sed -i 's/l\.Ensure(/l.Ensure(context.Background(), /g' internal/controller/listen/leaf_test.go
```

- [ ] **Step 4: Run test to verify it passes**

Run: `TMPDIR=/root/.cache/w0c-tmp/36 GOTOOLCHAIN=local go test -count=1 -race ./internal/controller/listen/ && TMPDIR=/root/.cache/w0c-tmp/36 GOTOOLCHAIN=local go build ./... && TMPDIR=/root/.cache/w0c-tmp/36 golangci-lint run --allow-serial-runners ./internal/controller/listen/...`
Expected: `ok  	github.com/darkraise/darkpanel/internal/controller/listen`, the build succeeds, and golangci-lint prints `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add internal/controller/listen/sources.go internal/controller/listen/tailscale_cert.go internal/controller/listen/leaf.go internal/controller/listen/service.go internal/controller/listen/leaf_test.go internal/controller/listen/sources_test.go
git commit -m "feat(listen): ask certificate sources for the web leaf"
rm -rf /root/.cache/w0c-tmp/36
```

### Task 37: Create host dialog

The "Add host" button of the hosts page and its dialog (spec 1.12: `pages/hosts.tsx` gains an "Add host" button opening `pages/create-host-dialog.tsx`, which calls `core.host.create` through the change dialog and renders every enabled module's `hostCreate` component, merging their values into the input). `core.host.create`/v1 takes `{ name, controllerAddress? }` (`schemas/core.host.create/v1.json`, `internal/core/fleet.go:104-107`) and answers an `EnrollmentGrant` `{ host, token, expiresAt }` whose token is secret and appears only in the apply response (`internal/core/fleet.go:121-125`). The change dialog shows no output, so the composition is three dialogs in sequence: a form (name, optional controller address, and the `hostCreate` component of every registry entry enabled on the controller's own host, the `isLocal` host), then `ChangeDialog` with the merged input, whose `onApplied` hands the grant to a third dialog that shows the token once with a copy button; closing it forgets the token. Core fields win over a module value of the same name, so a module cannot rename the host. A failed apply stays in the change dialog with its error. `CreateHostDialog` takes an optional `entries` prop (default `moduleRegistry`) so the test can supply a module with a host-creation input. Test helpers carry the prefix `chd`.

**Files:**
- Create: `web/src/pages/create-host-dialog.tsx`
- Modify: `web/src/pages/hosts.tsx` (imports, and `HostsPage`'s `PageHeader` line)
- Test: `web/src/pages/create-host-dialog.test.tsx`

**Interfaces:**
- Consumes: C22 (`moduleRegistry`, `hostCreateExtensions(entries, host)`, `ModuleEntry.hostCreate` with `id` and `render`); C23 (`ChangeDialog` with `target`, `input`, `onApplied`); C21 (`Change` with its new fields, used by the test fixtures).
- Produces: none that a later task consumes.

**Items:** 52

**Depends on:** Task 21, Task 27

**Implementer:** dforge:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 1 = 3

- [ ] **Step 1: Write the failing test**

Create `web/src/pages/create-host-dialog.test.tsx`:

```tsx
import { lazy } from "react"
import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { Info } from "lucide-react"
import { expect, test } from "vitest"
import type { Change, Host, Plan } from "@/api/types"
import type { ModuleEntry } from "@/modules/types"
import { renderWithProviders, stubFetch } from "@/test/utils"
import { CreateHostDialog } from "./create-host-dialog"
import { HostsPage } from "./hosts"

const HOSTS = "POST /api/v1/actions/core.host.list/v1/read"
const PLAN = "POST /api/v1/actions/core.host.create/v1/plan"
const APPLY = "POST /api/v1/actions/core.host.create/v1/apply"

function chdHost(modules: Host["modules"]): Host {
  return { id: "h1", name: "controller", isLocal: true, online: true, modules, ingestion: null }
}

const chdPlan: Plan = {
  planId: "p1", hostId: null, action: "core.host.create", version: 1, inputHash: "h",
  diff: [{ op: "+", text: "host web-2: pending" }], guards: [{ id: "name-free", ok: true, message: "no host is named web-2", overridable: false, overridden: false }],
  expiresAt: "2026-10-23T09:00:00Z", confirmation: null,
}

function chdChange(output: unknown, over: Partial<Change> = {}): Change {
  return {
    id: "c1", hostId: null, action: "core.host.create", version: 1, class: "compensable", state: "completed", actor: "user:u1",
    createdAt: "2026-10-23T08:00:00Z", updatedAt: "2026-10-23T08:00:00Z", revertDeadline: null, output, error: null,
    compensatedBy: null, parentChangeId: null, owner: null, repairedBy: null, repairOutcome: null, override: null, ...over,
  }
}

const chdGrant = { host: { id: "h2", name: "web-2" }, token: "dpe_example_enrollment", expiresAt: "2026-10-23T09:00:00Z" }

// chdJoin stands in for a module's host creation inputs.
const chdJoin = lazy(async () => ({
  default: ({ onChange }: { value: Record<string, unknown>; onChange: (v: Record<string, unknown>) => void }) => (
    <button type="button" onClick={() => onChange({ tailnetJoin: true, name: "ignored" })}>
      Join the tailnet
    </button>
  ),
}))

const chdEntries: ModuleEntry[] = [
  { id: "chdjoin", label: "Join", icon: Info, routes: lazy(async () => ({ default: () => null })), hostCreate: { id: "chdjoin.join", render: chdJoin } },
]

async function chdFill(user: ReturnType<typeof userEvent.setup>, name: string) {
  await user.click(screen.getByRole("button", { name: "Add host" }))
  const form = screen.getByRole("dialog")
  await user.type(within(form).getByLabelText("Name"), name)
  return form
}

test("the hosts page adds a host and shows its enrollment token once", async () => {
  const calls = stubFetch({
    [HOSTS]: { body: { output: { hosts: [chdHost([])] } } },
    [PLAN]: { body: chdPlan },
    [APPLY]: { body: chdChange(chdGrant) },
  })
  renderWithProviders(<HostsPage />)
  const user = userEvent.setup()
  await screen.findByRole("link", { name: "controller" })
  const form = await chdFill(user, "web-2")
  await user.click(within(form).getByRole("button", { name: "Continue" }))

  const plan = await screen.findByRole("dialog", { name: "Add a host" })
  expect((await within(plan).findByLabelText("Planned changes")).textContent).toBe("+ host web-2: pending")
  await user.click(within(plan).getByRole("button", { name: "Apply" }))

  const done = await screen.findByRole("dialog", { name: "Host added" })
  expect(within(done).getByDisplayValue("dpe_example_enrollment")).toBeTruthy()
  expect(calls.find((c) => c.path.endsWith("/plan"))?.body).toEqual({ input: { name: "web-2" } })
  await waitFor(() => expect(calls.filter((c) => c.path.endsWith("core.host.list/v1/read")).length).toBeGreaterThan(1))

  await user.click(within(done).getByRole("button", { name: "Done" }))
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  await user.click(screen.getByRole("button", { name: "Add host" }))
  expect(screen.queryByDisplayValue("dpe_example_enrollment")).toBeNull()
})

test("module inputs of modules enabled on the controller's host merge into the input", async () => {
  const calls = stubFetch({
    [HOSTS]: { body: { output: { hosts: [chdHost([{ id: "chdjoin", version: 1, enabled: true, available: true, reason: null, checkedAt: null }])] } } },
    [PLAN]: { body: chdPlan },
  })
  renderWithProviders(<CreateHostDialog entries={chdEntries} />)
  const user = userEvent.setup()
  const form = await chdFill(user, "web-2")
  await user.type(within(form).getByLabelText("Controller address (optional)"), "10.0.0.1:7443")
  await user.click(await within(form).findByRole("button", { name: "Join the tailnet" }))
  await user.click(within(form).getByRole("button", { name: "Continue" }))
  await waitFor(() =>
    expect(calls.find((c) => c.path.endsWith("/plan"))?.body).toEqual({
      input: { tailnetJoin: true, name: "web-2", controllerAddress: "10.0.0.1:7443" },
    }),
  )
})

test("a module that is not enabled on the controller's host adds no inputs", async () => {
  stubFetch({ [HOSTS]: { body: { output: { hosts: [chdHost([{ id: "chdjoin", version: 1, enabled: false, available: true, reason: null, checkedAt: null }])] } } } })
  renderWithProviders(<CreateHostDialog entries={chdEntries} />)
  const user = userEvent.setup()
  await waitFor(() => expect(screen.getByRole("button", { name: "Add host" })).toBeTruthy())
  const form = await chdFill(user, "web-2")
  expect(within(form).queryByRole("button", { name: "Join the tailnet" })).toBeNull()
})

test("a failed creation stays in the change dialog with its error and shows no token", async () => {
  stubFetch({
    [HOSTS]: { body: { output: { hosts: [chdHost([])] } } },
    [PLAN]: { body: chdPlan },
    [APPLY]: { body: chdChange(null, { state: "failed", error: { code: "internal", message: "the authority is not loaded" } }) },
  })
  renderWithProviders(<CreateHostDialog />)
  const user = userEvent.setup()
  const form = await chdFill(user, "web-2")
  await user.click(within(form).getByRole("button", { name: "Continue" }))
  const plan = await screen.findByRole("dialog", { name: "Add a host" })
  await user.click(await within(plan).findByRole("button", { name: "Apply" }))
  expect((await within(plan).findByRole("alert")).textContent).toBe("the authority is not loaded")
  expect(screen.queryByRole("dialog", { name: "Host added" })).toBeNull()
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mkdir -p /root/.cache/w0c-tmp/37 && cd web && TMPDIR=/root/.cache/w0c-tmp/37 pnpm exec vitest run src/pages/create-host-dialog.test.tsx`
Expected: FAIL — `Failed to resolve import "./create-host-dialog"`.

- [ ] **Step 3: Write the implementation**

Create `web/src/pages/create-host-dialog.tsx`:

```tsx
import { Suspense, useState, type FormEvent } from "react"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { Button } from "darkraise-ui/components/button"
import { Clipboard, ClipboardControl, ClipboardInput, ClipboardLabel, ClipboardTrigger } from "darkraise-ui/components/clipboard"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "darkraise-ui/components/dialog"
import { Input } from "darkraise-ui/components/input"
import { Label } from "darkraise-ui/components/label"
import { Spinner } from "darkraise-ui/components/spinner"
import { HOSTS_QUERY_KEY, changeError, listHosts } from "@/api/client"
import type { Change } from "@/api/types"
import { ChangeDialog } from "@/components/change-dialog"
import { formatTime } from "@/lib/format"
import { hostCreateExtensions, moduleRegistry } from "@/modules/registry"
import type { ModuleEntry } from "@/modules/types"

// The enrollment grant core.host.create answers; its token is in the apply
// response only, so it is shown here once and then forgotten.
interface Grant {
  host: { id: string; name: string }
  token: string
  expiresAt: string
}

type Phase = "closed" | "form" | "plan" | "grant"

function isGrant(output: unknown): output is Grant {
  if (typeof output !== "object" || output === null) return false
  const o = output as Record<string, unknown>
  return typeof o.token === "string" && typeof o.expiresAt === "string" && typeof o.host === "object" && o.host !== null
}

// CreateHostDialog adds a host: it asks for the name, renders the host
// creation inputs of every module enabled on the controller's own host,
// plans and applies core.host.create through the change dialog, and shows
// the enrollment token once.
export function CreateHostDialog({ entries = moduleRegistry }: { entries?: ModuleEntry[] }) {
  const client = useQueryClient()
  const hosts = useQuery({ queryKey: HOSTS_QUERY_KEY, queryFn: listHosts })
  const [phase, setPhase] = useState<Phase>("closed")
  const [values, setValues] = useState<Record<string, Record<string, unknown>>>({})
  const [input, setInput] = useState<Record<string, unknown>>({})
  const [grant, setGrant] = useState<Grant | null>(null)

  const localHost = hosts.data?.hosts.find((h) => h.isLocal)
  const extensions = localHost === undefined ? [] : hostCreateExtensions(entries, localHost)

  function reset() {
    setPhase("closed")
    setValues({})
    setInput({})
    setGrant(null)
  }

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const form = new FormData(event.currentTarget)
    const name = String(form.get("name") ?? "").trim()
    const controllerAddress = String(form.get("controllerAddress") ?? "").trim()
    const merged: Record<string, unknown> = Object.assign({}, ...extensions.map((e) => values[e.id] ?? {}))
    merged.name = name
    if (controllerAddress !== "") merged.controllerAddress = controllerAddress
    setInput(merged)
    setPhase("plan")
  }

  function applied(change: Change) {
    if (changeError(change) !== null || !isGrant(change.output)) return
    setGrant(change.output)
    setPhase("grant")
    void client.invalidateQueries({ queryKey: HOSTS_QUERY_KEY })
  }

  return (
    <>
      <Button onClick={() => setPhase("form")}>Add host</Button>
      <Dialog open={phase === "form"} onOpenChange={(next) => (next ? setPhase("form") : reset())}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Add a host</DialogTitle>
            <DialogDescription>The host gets a one-hour enrollment token for its agent.</DialogDescription>
          </DialogHeader>
          <form className="flex flex-col gap-4" onSubmit={submit}>
            <div className="flex flex-col gap-2">
              <Label htmlFor="host-name">Name</Label>
              <Input id="host-name" name="name" required maxLength={63} pattern="[a-z0-9][a-z0-9.\-]{0,62}" autoComplete="off" />
            </div>
            <div className="flex flex-col gap-2">
              <Label htmlFor="host-controller-address">Controller address (optional)</Label>
              <Input id="host-controller-address" name="controllerAddress" placeholder="controller.example:7443" autoComplete="off" />
            </div>
            {extensions.map((ext) => {
              const Extension = ext.render
              return (
                <Suspense key={ext.id} fallback={<Spinner aria-label="Loading the module's inputs" />}>
                  <Extension value={values[ext.id] ?? {}} onChange={(v) => setValues((cur) => ({ ...cur, [ext.id]: v }))} />
                </Suspense>
              )
            })}
            <DialogFooter>
              <Button type="submit">Continue</Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
      <ChangeDialog
        open={phase === "plan"}
        onOpenChange={(next) => {
          if (!next) reset()
        }}
        title="Add a host"
        target={{ name: "core.host.create", version: 1 }}
        input={input}
        onApplied={applied}
      />
      <Dialog open={phase === "grant"} onOpenChange={(next) => (next ? undefined : reset())}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Host added</DialogTitle>
            <DialogDescription>Copy the enrollment token now. It will not be shown again.</DialogDescription>
          </DialogHeader>
          {grant !== null && (
            <div className="flex flex-col gap-4">
              <Clipboard value={grant.token}>
                <ClipboardLabel>{`Enrollment token for ${grant.host.name}`}</ClipboardLabel>
                <ClipboardControl>
                  <ClipboardInput className="font-mono" />
                  <ClipboardTrigger>Copy</ClipboardTrigger>
                </ClipboardControl>
              </Clipboard>
              <p className="text-sm text-muted-foreground">
                {`On the new host, run sudo darkpanel agent enroll and paste the token before ${formatTime(grant.expiresAt)}.`}
              </p>
              <DialogFooter>
                <Button onClick={reset}>Done</Button>
              </DialogFooter>
            </div>
          )}
        </DialogContent>
      </Dialog>
    </>
  )
}
```

In `web/src/pages/hosts.tsx`, add the import after the `import type { Host, ModuleState } from "@/api/types"` line:

```tsx
import { CreateHostDialog } from "./create-host-dialog"
```

and replace the page header line in `HostsPage`

```tsx
      <PageHeader title="Hosts" description="The servers this controller manages." />
```

with

```tsx
      <PageHeader title="Hosts" description="The servers this controller manages." actions={<CreateHostDialog />} />
```


- [ ] **Step 4: Run the tests to verify they pass**

Run: `mkdir -p /root/.cache/w0c-tmp/37 && cd web && TMPDIR=/root/.cache/w0c-tmp/37 pnpm exec vitest run src/pages/create-host-dialog.test.tsx src/pages/hosts.test.tsx`
Expected: PASS, 4 tests in `create-host-dialog.test.tsx` and the existing tests of `hosts.test.tsx` unchanged.

- [ ] **Step 5: Check the whole web suite, types and lint**

Run: `TMPDIR=/root/.cache/w0c-tmp/37 make lint-web test-web`
Expected: ESLint prints nothing (it runs with `--max-warnings 0`), `tsc --noEmit` prints nothing, and the Vitest summary reports every test file passed.

- [ ] **Step 6: Commit**

```bash
git add web/src/pages/create-host-dialog.tsx web/src/pages/create-host-dialog.test.tsx web/src/pages/hosts.tsx
git commit -m "feat(web): add hosts from the hosts page"
rm -rf /root/.cache/w0c-tmp/37
```

### Task 38: Controller contributions

Spec 1.12's call site gathers the controller-side contributions: every module of `modules.All()` that implements `ext.Controller`, and `backup.Contribute` (`internal/controller/backup/contribute.go`, a stub wave 7 fills with the backup and quarantine routes). This task creates the `backup` package with that stub and `internal/controller/server/extensions.go`, which builds `ext.Deps` from the server's stores and services and calls each contribution in order: modules, then `Options.Contributors`, then backup. It also adds the compile-time assertions that the controller's own types satisfy the extension interfaces without adapters. Amendment to C20: `server.Options` gains `Contributors []ext.Controller` — contributions beside the modules' and backup's — because `modules.All()` and the registry are fixed, so tests have no other way to contribute a route or a service; production passes nil. `Deps.Listeners` stays nil here: Task 47 creates the runtime listener service and sets it. Task 46 wires the result into startup. Test helpers carry the prefix `xu` (`xuContributor`).

**Files:**
- Create: `internal/controller/backup/contribute.go`
- Test: `internal/controller/backup/contribute_test.go`
- Create: `internal/controller/server/extensions.go`
- Modify: `internal/controller/server/server.go:25-34` (imports), `:96-99` (`Options`, after `ReleaseIndex`)
- Test: `internal/controller/server/extensions_test.go`

**Interfaces:**
- Consumes: C2 — `ext.Deps`, `ext.Controller`, `ext.Route`, `ext.Service`, `ext.Publisher`, `ext.Dispatcher` (Task 19).
- Produces: C20 — `backup.Contribute(deps ext.Deps) ([]ext.Route, []ext.Service, error)`; `(*Server).contributions() ([]ext.Route, []ext.Service, error)`, `(*Server).extDeps() ext.Deps` and `Options.Contributors` (Tasks 46 and 47).

**Items:** 52

**Depends on:** Task 19

**Implementer:** dforge:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 1 = 4

- [ ] **Step 1: Write the failing tests**

Create `internal/controller/backup/contribute_test.go`:

```go
package backup

import (
	"testing"

	"github.com/darkraise/darkpanel/internal/core/ext"
)

// TestContributeAddsNothingYet: until the backup routes exist the
// contribution mounts no route and starts no service.
func TestContributeAddsNothingYet(t *testing.T) {
	routes, services, err := Contribute(ext.Deps{})
	if err != nil || len(routes) != 0 || len(services) != 0 {
		t.Errorf("Contribute = %v, %v, %v; want nothing", routes, services, err)
	}
}
```

Create `internal/controller/server/extensions_test.go`:

```go
package server

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core/ext"
)

// xuContributor contributes one route and one service, both named name, or
// fails with err.
type xuContributor struct {
	name string
	err  error
	got  *ext.Deps
}

func (c *xuContributor) Controller(deps ext.Deps) ([]ext.Route, []ext.Service, error) {
	c.got = &deps
	if c.err != nil {
		return nil, nil, c.err
	}
	route := ext.Route{Surface: ext.SurfaceWeb, Method: http.MethodGet, Path: "/api/v1/test/" + c.name,
		Handler: http.NotFoundHandler()}
	svc := ext.Service{Name: "test." + c.name, Run: func(ctx context.Context) error { <-ctx.Done(); return nil }}
	return []ext.Route{route}, []ext.Service{svc}, nil
}

func TestContributionsCollectInOrder(t *testing.T) {
	a, b := &xuContributor{name: "a"}, &xuContributor{name: "b"}
	s := &Server{opts: Options{Contributors: []ext.Controller{a, b}}, localHostID: "01LOCAL"}
	routes, services, err := s.contributions()
	if err != nil {
		t.Fatal(err)
	}
	var paths, names []string
	for _, r := range routes {
		paths = append(paths, r.Path)
	}
	for _, svc := range services {
		names = append(names, svc.Name)
	}
	if want := []string{"/api/v1/test/a", "/api/v1/test/b"}; !slices.Equal(paths, want) {
		t.Errorf("routes %v, want %v", paths, want)
	}
	if want := []string{"test.a", "test.b"}; !slices.Equal(names, want) {
		t.Errorf("services %v, want %v", names, want)
	}
	if a.got == nil || b.got == nil || a.got.LocalHostID != "01LOCAL" || b.got.LocalHostID != "01LOCAL" {
		t.Errorf("deps given = %+v and %+v, want the server's", a.got, b.got)
	}
}

func TestContributionErrorNamesTheContributor(t *testing.T) {
	ok, broken := &xuContributor{name: "ok"}, &xuContributor{name: "broken", err: errors.New("no credentials table")}
	s := &Server{opts: Options{Contributors: []ext.Controller{ok, broken}}}
	_, _, err := s.contributions()
	if err == nil || !strings.Contains(err.Error(), "contributor 1") || !strings.Contains(err.Error(), "no credentials table") {
		t.Fatalf("contributions = %v, want the error of contributor 1", err)
	}
}

func TestControllerFuncIsAController(t *testing.T) {
	var called bool
	f := controllerFunc(func(ext.Deps) ([]ext.Route, []ext.Service, error) {
		called = true
		return nil, nil, nil
	})
	if _, _, err := f.Controller(ext.Deps{}); err != nil || !called {
		t.Errorf("controllerFunc.Controller = %v, called %v", err, called)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mkdir -p /root/.cache/w0c-tmp/38 && TMPDIR=/root/.cache/w0c-tmp/38 GOTOOLCHAIN=local go test -count=1 ./internal/controller/backup/ && TMPDIR=/root/.cache/w0c-tmp/38 GOTOOLCHAIN=local go test -count=1 -run 'Contribution|ControllerFunc' ./internal/controller/server/`
Expected: FAIL: the backup package has no non-test Go files (`no non-test Go files` or `undefined: Contribute`), and the server package fails to build with `unknown field Contributors in struct literal of type Options`, `s.contributions undefined` and `undefined: controllerFunc`.

- [ ] **Step 3: Write the implementation**

Create `internal/controller/backup/contribute.go` (spec 1.12 makes it a stub that wave 7 replaces; its empty answer is the contract until then):

```go
// Package backup is the controller's backup, restore and restore-quarantine
// surface (spec 10). Its routes and services reach the controller through
// Contribute, which the assembly calls at startup beside the modules'
// controller contributions; the routes are served on the controller socket.
package backup

import "github.com/darkraise/darkpanel/internal/core/ext"

// Contribute returns the backup and quarantine routes and services. It
// contributes nothing until the backup and restore commands exist.
func Contribute(_ ext.Deps) ([]ext.Route, []ext.Service, error) {
	return nil, nil, nil
}
```

Create `internal/controller/server/extensions.go`:

```go
package server

import (
	"fmt"

	"github.com/darkraise/darkpanel/internal/controller/audit"
	"github.com/darkraise/darkpanel/internal/controller/backup"
	"github.com/darkraise/darkpanel/internal/controller/dispatch"
	"github.com/darkraise/darkpanel/internal/controller/events"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/ext"
	"github.com/darkraise/darkpanel/internal/modules"
)

// The controller's own types are what the extension package's interfaces
// describe, so contributions get them without adapters.
var (
	_ ext.Publisher      = (*events.Bus)(nil)
	_ ext.Dispatcher     = (*dispatch.Dispatcher)(nil)
	_ core.HostDirectory = (*dispatch.Hosts)(nil)
	_ core.Audit         = (*audit.Store)(nil)
)

// controllerFunc is a contribution given as a function, such as
// backup.Contribute.
type controllerFunc func(deps ext.Deps) ([]ext.Route, []ext.Service, error)

func (f controllerFunc) Controller(deps ext.Deps) ([]ext.Route, []ext.Service, error) { return f(deps) }

// contributor is one controller contribution and the name its errors carry.
type contributor struct {
	name string
	c    ext.Controller
}

// extDeps is what the assembly gives controller contributions (spec 1.12).
func (s *Server) extDeps() ext.Deps {
	return ext.Deps{
		DB: s.controllerDB, Sealer: s.controllerSealer, Clock: s.clock, Log: s.log, Hosts: s.hosts,
		Dispatch: s.dispatcher, Events: s.events, Audit: s.auditLog, LocalHostID: s.localHostID,
	}
}

// contributions calls Controller on every compiled-in module that has a
// controller-side contribution, on Options.Contributors, and on
// backup.Contribute, in that order, and returns their routes and services.
// A contribution's error fails startup.
func (s *Server) contributions() ([]ext.Route, []ext.Service, error) {
	var all []contributor
	for _, m := range modules.All() {
		if c, ok := m.(ext.Controller); ok {
			all = append(all, contributor{name: "module " + m.Manifest().ID, c: c})
		}
	}
	for i, c := range s.opts.Contributors {
		all = append(all, contributor{name: fmt.Sprintf("contributor %d", i), c: c})
	}
	all = append(all, contributor{name: "backup", c: controllerFunc(backup.Contribute)})
	deps := s.extDeps()
	var routes []ext.Route
	var services []ext.Service
	for _, c := range all {
		r, svc, err := c.c.Controller(deps)
		if err != nil {
			return nil, nil, fmt.Errorf("server: the controller contribution of %s: %w", c.name, err)
		}
		routes = append(routes, r...)
		services = append(services, svc...)
	}
	return routes, services, nil
}
```

In `internal/controller/server/server.go`, add the import `"github.com/darkraise/darkpanel/internal/core/ext"` between `internal/core` and `internal/core/secret`, and add the field at the end of `Options`:

```go
	// ReleaseIndex is the release index the update scheduler reads; nil
	// means the published index under trustedkeys.BaseURL().
	ReleaseIndex rollout.ReleaseSource
	// Contributors are controller contributions beside the compiled-in
	// modules' and the backup package's (spec 1.12); production passes nil.
	Contributors []ext.Controller
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `TMPDIR=/root/.cache/w0c-tmp/38 GOTOOLCHAIN=local go test -count=1 ./internal/controller/backup/ ./internal/controller/server/ && TMPDIR=/root/.cache/w0c-tmp/38 golangci-lint run --allow-serial-runners ./internal/controller/backup/... ./internal/controller/server/...`
Expected: `ok` for both packages and `0 issues.` (`contributions` is used by its tests until Task 46 calls it at startup; the linter counts test use.)

- [ ] **Step 5: Commit**

```bash
git add internal/controller/backup/contribute.go internal/controller/backup/contribute_test.go internal/controller/server/extensions.go internal/controller/server/extensions_test.go internal/controller/server/server.go
git commit -m "feat(server): collect controller contributions"
rm -rf /root/.cache/w0c-tmp/38
```

### Task 39: Controller service runner

Controller services contributed through the extension package (`ext.Service`, C2: the auth-key sweeps and the Cloudflare hostname driver of later waves) need one supervisor: start each only once the restore-quarantine flag is clear, restart a `Run` that ended before shutdown after a delay from 1 s doubling to 5 min (reset by a run of 5 min or longer), and at shutdown cancel every context and wait for every `Run` to return (spec 1.12, C17). This task builds that runner in `internal/controller/server/extservices.go` as an unexported type with fake-clock tests; Task 46 starts it with the contributed services and Task 48 gives it the quarantine check. Decisions: a `Run` that returns nil, or panics, before its context ends is restarted exactly like an error (a service runs until shutdown); a panic is recovered and logged by its type only unless it is a runtime error, as `lifecycle.Protect` does, because a service may hold credentials; `Suspended` is asked again before every restart, and an error from it counts as suspended. The runner has no `Start` error path: an empty service list starts nothing. Test helpers carry the prefix `sv` (`svService`, `newSvService`, `svNextRun`, `svNoRun`, `svWaitPending`, `svFailAndWait`, `errSvBoom`); the existing `t0` and `lockedBuffer` of `server_test.go` are used as written.

**Files:**
- Create: `internal/controller/server/extservices.go`
- Test: `internal/controller/server/extservices_test.go`

**Interfaces:**
- Consumes: C2 — `ext.Service{Name, Run}` (Task 19).
- Produces: C17 — `serviceRunnerConfig{Services, Suspended, Poll, Clock, Log}`, `startServiceRunner(cfg) *serviceRunner`, `(*serviceRunner).stop()` (Task 46 starts and stops it; Task 48 sets `Suspended`).

**Items:** 52

**Depends on:** Task 19

**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6
**Lenses:** silent-failures

- [ ] **Step 1: Write the failing test**

Create `internal/controller/server/extservices_test.go`:

```go
package server

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/ext"
)

var errSvBoom = errors.New("the service failed")

// svService is a scripted controller service: each Run reports when it
// started on runs, then returns what the test sends on results, panics with
// what it sends on panics, or returns at shutdown, once hold is closed when
// hold is set.
type svService struct {
	clock   core.Clock
	runs    chan time.Time
	results chan error
	panics  chan any
	hold    chan struct{}
}

func newSvService(clock core.Clock) *svService {
	return &svService{clock: clock, runs: make(chan time.Time, 16), results: make(chan error), panics: make(chan any)}
}

func (s *svService) run(ctx context.Context) error {
	s.runs <- s.clock.Now()
	select {
	case err := <-s.results:
		return err
	case v := <-s.panics:
		panic(v)
	case <-ctx.Done():
		if s.hold != nil {
			<-s.hold
		}
		return ctx.Err()
	}
}

func (s *svService) service(name string) ext.Service { return ext.Service{Name: name, Run: s.run} }

// svNextRun waits for the service's next Run and returns when it started.
func svNextRun(t *testing.T, s *svService) time.Time {
	t.Helper()
	select {
	case at := <-s.runs:
		return at
	case <-time.After(5 * time.Second):
		t.Fatal("the service did not run")
		return time.Time{}
	}
}

// svNoRun fails when the service runs within a short real-time wait.
func svNoRun(t *testing.T, s *svService) {
	t.Helper()
	select {
	case at := <-s.runs:
		t.Fatalf("the service ran at %v, want no run", at)
	case <-time.After(50 * time.Millisecond):
	}
}

// svWaitPending waits until the clock has n armed timers.
func svWaitPending(t *testing.T, clock *coretest.FakeClock, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for clock.Pending() != n {
		if time.Now().After(deadline) {
			t.Fatalf("pending timers = %d, want %d", clock.Pending(), n)
		}
		time.Sleep(time.Millisecond)
	}
}

// svFailAndWait makes the running Run fail and waits for its restart delay
// to be armed.
func svFailAndWait(t *testing.T, s *svService, clock *coretest.FakeClock) {
	t.Helper()
	s.results <- errSvBoom
	svWaitPending(t, clock, 1)
}

func TestServiceRunnerBacksOff(t *testing.T) {
	clock := coretest.NewFakeClock(t0)
	svc := newSvService(clock)
	r := startServiceRunner(serviceRunnerConfig{Services: []ext.Service{svc.service("test.backoff")}, Clock: clock})
	t.Cleanup(r.stop)
	at := svNextRun(t, svc)
	if !at.Equal(t0) {
		t.Fatalf("first run at %v, want %v", at, t0)
	}
	for _, d := range []time.Duration{
		time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 32 * time.Second,
		64 * time.Second, 128 * time.Second, 256 * time.Second, 5 * time.Minute, 5 * time.Minute,
	} {
		svFailAndWait(t, svc, clock)
		clock.Advance(d - time.Nanosecond)
		if n := clock.Pending(); n != 1 {
			t.Fatalf("the service restarted before its delay of %v", d)
		}
		clock.Advance(time.Nanosecond)
		got := svNextRun(t, svc)
		if want := at.Add(d); !got.Equal(want) {
			t.Fatalf("restart at %v, want %v (a delay of %v)", got, want, d)
		}
		at = got
	}
}

func TestServiceRunnerResetsTheDelayAfterALongRun(t *testing.T) {
	clock := coretest.NewFakeClock(t0)
	svc := newSvService(clock)
	r := startServiceRunner(serviceRunnerConfig{Services: []ext.Service{svc.service("test.reset")}, Clock: clock})
	t.Cleanup(r.stop)
	svNextRun(t, svc)
	for _, d := range []time.Duration{time.Second, 2 * time.Second} {
		svFailAndWait(t, svc, clock)
		clock.Advance(d)
		svNextRun(t, svc)
	}
	// This run lasts five minutes, so the next delay starts over at 1 s.
	clock.Advance(5 * time.Minute)
	svFailAndWait(t, svc, clock)
	clock.Advance(time.Second - time.Nanosecond)
	if n := clock.Pending(); n != 1 {
		t.Fatal("the service restarted before 1 s")
	}
	clock.Advance(time.Nanosecond)
	svNextRun(t, svc)
}

func TestServiceRunnerRestartsAnEarlyReturnAndAPanic(t *testing.T) {
	clock := coretest.NewFakeClock(t0)
	svc := newSvService(clock)
	var logs lockedBuffer
	r := startServiceRunner(serviceRunnerConfig{
		Services: []ext.Service{svc.service("test.early")}, Clock: clock, Log: slog.New(slog.NewTextHandler(&logs, nil)),
	})
	t.Cleanup(r.stop)
	svNextRun(t, svc)

	svc.results <- nil
	svWaitPending(t, clock, 1)
	clock.Advance(time.Second)
	svNextRun(t, svc)
	if !strings.Contains(logs.String(), "returned before shutdown") {
		t.Errorf("the log does not report the early return:\n%s", logs.String())
	}

	svc.panics <- "a secret the service held"
	svWaitPending(t, clock, 1)
	clock.Advance(2 * time.Second)
	svNextRun(t, svc)
	out := logs.String()
	if !strings.Contains(out, "panicked with a string") || strings.Contains(out, "a secret the service held") {
		t.Errorf("the log reports the panic as %q, want its type only", out)
	}
}

func TestServiceRunnerWaitsWhileSuspended(t *testing.T) {
	clock := coretest.NewFakeClock(t0)
	svc := newSvService(clock)
	var suspended, unreadable atomic.Bool
	suspended.Store(true)
	var logs lockedBuffer
	r := startServiceRunner(serviceRunnerConfig{
		Services: []ext.Service{svc.service("test.suspended")}, Clock: clock, Log: slog.New(slog.NewTextHandler(&logs, nil)),
		Suspended: func(context.Context) (bool, error) {
			if unreadable.Load() {
				return false, errors.New("the flag cannot be read")
			}
			return suspended.Load(), nil
		},
	})
	t.Cleanup(r.stop)

	svWaitPending(t, clock, 1)
	svNoRun(t, svc)
	clock.Advance(serviceDefaultPoll)
	svWaitPending(t, clock, 1)
	svNoRun(t, svc)

	unreadable.Store(true)
	clock.Advance(serviceDefaultPoll)
	svWaitPending(t, clock, 1)
	svNoRun(t, svc)
	if !strings.Contains(logs.String(), "cannot tell whether controller services are suspended") {
		t.Errorf("the log does not report the failed read:\n%s", logs.String())
	}

	unreadable.Store(false)
	suspended.Store(false)
	clock.Advance(serviceDefaultPoll)
	if at := svNextRun(t, svc); !at.Equal(t0.Add(3 * serviceDefaultPoll)) {
		t.Errorf("first run at %v, want %v", at, t0.Add(3*serviceDefaultPoll))
	}
}

func TestServiceRunnerStopWaitsForRun(t *testing.T) {
	clock := coretest.NewFakeClock(t0)
	svc := newSvService(clock)
	svc.hold = make(chan struct{})
	r := startServiceRunner(serviceRunnerConfig{Services: []ext.Service{svc.service("test.stop")}, Clock: clock})
	svNextRun(t, svc)
	stopped := make(chan struct{})
	go func() {
		r.stop()
		close(stopped)
	}()
	select {
	case <-stopped:
		t.Fatal("stop returned while Run was still running")
	case <-time.After(50 * time.Millisecond):
	}
	close(svc.hold)
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("stop did not return after Run returned")
	}
}

// TestServiceRunnerStopsWhileWaiting: stop ends a suspended wait and a
// restart delay at once, leaving no timer armed and no service started.
func TestServiceRunnerStopsWhileWaiting(t *testing.T) {
	clock := coretest.NewFakeClock(t0)
	suspendedSvc, failedSvc := newSvService(clock), newSvService(clock)
	r := startServiceRunner(serviceRunnerConfig{
		Services: []ext.Service{suspendedSvc.service("test.waiting")}, Clock: clock,
		Suspended: func(context.Context) (bool, error) { return true, nil },
	})
	svWaitPending(t, clock, 1)
	r.stop()
	svNoRun(t, suspendedSvc)
	if n := clock.Pending(); n != 0 {
		t.Errorf("pending timers after stop = %d, want 0", n)
	}

	r = startServiceRunner(serviceRunnerConfig{Services: []ext.Service{failedSvc.service("test.failed")}, Clock: clock})
	svNextRun(t, failedSvc)
	svFailAndWait(t, failedSvc, clock)
	r.stop()
	if n := clock.Pending(); n != 0 {
		t.Errorf("pending timers after stop = %d, want 0", n)
	}
	clock.Advance(time.Minute)
	svNoRun(t, failedSvc)
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mkdir -p /root/.cache/w0c-tmp/39 && TMPDIR=/root/.cache/w0c-tmp/39 GOTOOLCHAIN=local go test -count=1 -run ServiceRunner ./internal/controller/server/`
Expected: FAIL to build with `undefined: startServiceRunner`, `undefined: serviceRunnerConfig` and `undefined: serviceDefaultPoll`.

- [ ] **Step 3: Write the implementation**

Create `internal/controller/server/extservices.go`. The package already imports `internal/agent/runtime` as `runtime` in `services.go`; this file imports the standard library's `runtime` as `goruntime`, which is per file and does not clash:

```go
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	goruntime "runtime"
	"sync"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/ext"
)

// The restart delays of a controller service (spec 1.12) and how often a
// suspended runner checks again.
const (
	serviceRestartMin  = time.Second
	serviceRestartMax  = 5 * time.Minute
	serviceDefaultPoll = 10 * time.Second
)

// serviceRunnerConfig configures startServiceRunner.
type serviceRunnerConfig struct {
	Services  []ext.Service
	Suspended func(ctx context.Context) (bool, error) // nil: never suspended
	Poll      time.Duration                           // how often a suspended runner re-checks; 0 means 10 s
	Clock     core.Clock                              // nil: core.SystemClock{}
	Log       *slog.Logger                            // nil discards
}

// serviceRunner runs the controller services contributed through the
// extension package, each on its own goroutine.
type serviceRunner struct {
	cfg    serviceRunnerConfig
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// startServiceRunner starts every service once Suspended answers false, and
// restarts a service whose Run returned an error after a delay from 1 s
// doubling to 5 min, reset by a run that lasted 5 min or longer. A Run that
// returns nil or panics before its context ends is restarted the same way:
// a service runs until shutdown. Suspended is asked again before every
// restart, and an error from it counts as suspended, so a restored
// controller never starts a service it cannot prove is released.
func startServiceRunner(cfg serviceRunnerConfig) *serviceRunner {
	if cfg.Poll <= 0 {
		cfg.Poll = serviceDefaultPoll
	}
	if cfg.Clock == nil {
		cfg.Clock = core.SystemClock{}
	}
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &serviceRunner{cfg: cfg, ctx: ctx, cancel: cancel}
	for _, svc := range cfg.Services {
		r.wg.Add(1)
		go r.run(svc)
	}
	return r
}

// stop cancels every service's context and waits for each Run to return.
func (r *serviceRunner) stop() {
	r.cancel()
	r.wg.Wait()
}

func (r *serviceRunner) run(svc ext.Service) {
	defer r.wg.Done()
	delay := serviceRestartMin
	for {
		if !r.waitReleased(svc.Name) {
			return
		}
		started := r.cfg.Clock.Now()
		err := r.call(svc)
		if r.ctx.Err() != nil {
			return
		}
		if r.cfg.Clock.Now().Sub(started) >= serviceRestartMax {
			delay = serviceRestartMin
		}
		if err == nil {
			err = errors.New("server: the service returned before shutdown")
		}
		r.cfg.Log.Error("server: controller service stopped; restarting it", "service", svc.Name, "error", err, "delay", delay)
		if !r.sleep(delay) {
			return
		}
		delay = min(2*delay, serviceRestartMax)
	}
}

// call runs svc once. A panic is recovered so that one service cannot stop
// the controller; as in lifecycle.Protect its value is reported only when it
// is a runtime error, because a service may have put a credential in it.
func (r *serviceRunner) call(svc ext.Service) (err error) {
	defer func() {
		v := recover()
		if v == nil {
			return
		}
		if re, ok := v.(goruntime.Error); ok {
			err = fmt.Errorf("server: service %s panicked: %w", svc.Name, re)
			return
		}
		err = fmt.Errorf("server: service %s panicked with a %T", svc.Name, v)
	}()
	return svc.Run(r.ctx)
}

// waitReleased returns once Suspended answers false, or false when the
// runner stops first.
func (r *serviceRunner) waitReleased(name string) bool {
	for {
		if r.ctx.Err() != nil {
			return false
		}
		if r.cfg.Suspended == nil {
			return true
		}
		suspended, err := r.cfg.Suspended(r.ctx)
		if err != nil {
			if r.ctx.Err() != nil {
				return false
			}
			r.cfg.Log.Warn("server: cannot tell whether controller services are suspended; waiting", "service", name, "error", err)
			suspended = true
		}
		if !suspended {
			return true
		}
		if !r.sleep(r.cfg.Poll) {
			return false
		}
	}
}

// sleep waits d on the runner's clock and reports false when the runner
// stops first.
func (r *serviceRunner) sleep(d time.Duration) bool {
	fired := make(chan struct{})
	t := r.cfg.Clock.AfterFunc(d, func() { close(fired) })
	select {
	case <-fired:
		return true
	case <-r.ctx.Done():
		t.Stop()
		return false
	}
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `TMPDIR=/root/.cache/w0c-tmp/39 GOTOOLCHAIN=local go test -count=20 -race -run ServiceRunner ./internal/controller/server/ && TMPDIR=/root/.cache/w0c-tmp/39 GOTOOLCHAIN=local go test -count=1 ./internal/controller/server/ && TMPDIR=/root/.cache/w0c-tmp/39 golangci-lint run --allow-serial-runners ./internal/controller/server/...`
Expected: `ok  	github.com/darkraise/darkpanel/internal/controller/server` twice and `0 issues.` The twenty race-detector repetitions guard against a test that only passes by timing.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/server/extservices.go internal/controller/server/extservices_test.go
git commit -m "feat(server): run controller services with backoff"
rm -rf /root/.cache/w0c-tmp/39
```

### Task 40: Runtime listener service

`listen.Runtime` (C19) runs the runtime listeners of spec 1.10 over the `core_listeners` rows of Tasks 18 and 28 and implements `core.Listeners` (C3). Each listener is HTTPS only, with a clone of the web listeners' shared `tls.Config` (Task 42 passes it at assembly), and accepts TCP connections from its `AllowedPeer` alone: every other connection is closed before a byte is read. `Open` records the row in state `open` in one transaction and, once `Start` has run, binds it; a failed bind moves the row to `closed`. Before `Start` it only records the row, because composite recovery runs before the network listeners exist and a recovered exposure may open its listener then; `Start` binds every open row, those included. `Open` and `Start` hold the runtime's lock while they record or read rows and bind, so an `Open` that races `Start` is bound by one of the two. `Revoke` commits `revoked` first, then closes the listener's `revoked` channel (which ends its streams, C11), stops accepting at once, and lets running responses finish with keep-alives off, cutting them after `RevokeGrace`; it is idempotent and monotonic. `Close` moves `revoked` to `closed` and refuses an open listener. `Start` binds the `open` rows only, never a `revoked` one, and retries a failed bind every `CheckInterval`; every bind re-reads its row under the lock, so a revocation that commits while `Start` or a retry runs always wins. The handler of each listener comes from a `RuntimeHandlerFunc` (the API's `RuntimeHandler`, C10) with an `isRevoked` that re-reads the row. The session-resumption test the constraint "A shared tls.Config needs a session-resumption test" (`docs/dforge/distilled/constraints.md`) asks for lives here: with shared ticket keys a session from a web listener resumes on a runtime listener, and a resumed session gives no way past a revocation. Test helpers of this task carry the prefix `rt`; the tests reuse Task 28's `rsInsert` and `rsSet`, `testTLS`, `fakeBinder` and `boundAddr` from `set_test.go`, `openControllerDB` and `testStart`.

**Files:**
- Create: `internal/controller/listen/runtime.go`
- Test: `internal/controller/listen/runtime_test.go`

**Interfaces:**
- Consumes: C3 (`core.Listeners`, `core.ListenerSpec`, `core.ListenerInfo`, the state and header constants); C9 (`insertListener`, `setListenerState`, `getListener`, `listListeners`, Task 28); `BindFunc`, `Endpoint`, `CheckInterval` and the server limits of `set.go` and `service.go`.
- Produces: C19 (`RuntimeConfig`, `RuntimeHandlerFunc`, `NewRuntime`, `Start`, `Open`, `Revoke`, `Close`, `Get`, `Addrs`, `Shutdown`) and the constant `RevokeGrace`, which Task 47 wires.

**Items:** 51

**Depends on:** Task 2, Task 28

**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6
**Lenses:** security, silent-failures

- [ ] **Step 1: Write the failing test**

Create `internal/controller/listen/runtime_test.go`:

```go
package listen

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// rtSpec is a listener on an address the test binder ignores, for a proxy at
// 127.0.0.1, the address every test client dials from.
var rtSpec = core.ListenerSpec{
	Addr:         netip.MustParseAddrPort("172.31.255.1:9443"),
	AllowedPeer:  netip.MustParseAddr("127.0.0.1"),
	ClientHeader: core.ClientHeaderNPM,
	PublicHost:   "panel.example.com",
	ChangeID:     "01CHANGE",
}

// rtFixture is a Runtime over a fresh controller database, binding every
// listener on 127.0.0.1 with a free port, with handlers from rtHandler.
type rtFixture struct {
	db      *store.DB
	clock   *coretest.FakeClock
	rt      *Runtime
	tls     *tls.Config
	client  *http.Client
	binder  *rtBinder
	release chan struct{} // closed to let /slow answer
	entered chan string   // receives the path of every request that reached a handler

	mu    sync.Mutex
	built []core.ListenerInfo
	isRev map[string]func(context.Context) (bool, error)
}

// rtBinder binds 127.0.0.1:0 for every endpoint, unless fail is set.
type rtBinder struct {
	mu   sync.Mutex
	fail error
}

func (b *rtBinder) bind(ctx context.Context, e Endpoint) (net.Listener, error) {
	b.mu.Lock()
	err := b.fail
	b.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("listen: bind %s: %w", e.URL(), err)
	}
	var lc net.ListenConfig
	return lc.Listen(ctx, "tcp4", "127.0.0.1:0")
}

func (b *rtBinder) setFail(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.fail = err
}

func newRtFixture(t *testing.T) *rtFixture {
	t.Helper()
	cfg, client := testTLS(t)
	f := &rtFixture{
		db: openControllerDB(t), clock: coretest.NewFakeClock(testStart), tls: cfg, client: client,
		binder: &rtBinder{}, release: make(chan struct{}), entered: make(chan string, 16),
		isRev: map[string]func(context.Context) (bool, error){},
	}
	f.rt = NewRuntime(RuntimeConfig{DB: f.db, Clock: f.clock, Log: slog.New(slog.DiscardHandler), Bind: f.binder.bind})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = f.rt.Shutdown(ctx)
	})
	return f
}

// rtHandler answers "/" with the listener id and whether the connection
// resumed a TLS session, holds "/slow" until release is closed, and streams
// "/stream" until the listener is revoked.
func (f *rtFixture) rtHandler(info core.ListenerInfo, revoked <-chan struct{}, isRevoked func(context.Context) (bool, error)) http.Handler {
	f.mu.Lock()
	f.built = append(f.built, info)
	f.isRev[info.ID] = isRevoked
	f.mu.Unlock()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.entered <- r.URL.Path
		switch r.URL.Path {
		case "/slow":
			<-f.release
			_, _ = io.WriteString(w, "slow done")
		case "/stream":
			_, _ = io.WriteString(w, "event 1\n")
			w.(http.Flusher).Flush()
			<-revoked
			_, _ = io.WriteString(w, "end revoked\n")
		default:
			_, _ = io.WriteString(w, info.ID+" "+rtResumed(r))
		}
	})
}

// rtResumed says whether r's connection resumed a TLS session, as a fixed
// string.
func rtResumed(r *http.Request) string {
	if r.TLS != nil && r.TLS.DidResume {
		return "resumed=true"
	}
	return "resumed=false"
}

func (f *rtFixture) start(t *testing.T) {
	t.Helper()
	if err := f.rt.Start(context.Background(), f.rtHandler, f.tls); err != nil {
		t.Fatal(err)
	}
}

// open opens spec and returns the listener's id and base URL: the one
// address Addrs gained.
func (f *rtFixture) open(t *testing.T, spec core.ListenerSpec) (string, string) {
	t.Helper()
	before := f.rt.Addrs()
	id, err := f.rt.Open(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range f.rt.Addrs() {
		if !slices.Contains(before, a) {
			return id, "https://" + a.String()
		}
	}
	t.Fatalf("Open bound no new address; Addrs = %v", f.rt.Addrs())
	return "", ""
}

func (f *rtFixture) state(t *testing.T, id string) string {
	t.Helper()
	info, err := f.rt.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return info.State
}

func rtGet(client *http.Client, url string) (string, error) {
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	return string(b), err
}

func TestRuntimeOpenServesHTTPSToTheAllowedPeer(t *testing.T) {
	f := newRtFixture(t)
	f.start(t)
	id, url := f.open(t, rtSpec)
	if body, err := rtGet(f.client, url+"/"); err != nil || !strings.HasPrefix(body, id+" ") {
		t.Fatalf("GET = %q, %v; want the listener's answer", body, err)
	}
	if got := f.state(t, id); got != core.ListenerOpen {
		t.Errorf("state %s, want open", got)
	}
	f.mu.Lock()
	built := slices.Clone(f.built)
	f.mu.Unlock()
	want := core.ListenerInfo{ID: id, Spec: rtSpec, State: core.ListenerOpen}
	if len(built) != 1 || built[0] != want {
		t.Errorf("handler built for %+v, want %+v", built, want)
	}
	plain := &http.Client{Timeout: 5 * time.Second}
	if body, err := rtGet(plain, strings.Replace(url, "https://", "http://", 1)+"/"); err == nil && strings.HasPrefix(body, id) {
		t.Fatal("a plain HTTP request was served")
	}

	other := rtSpec
	other.AllowedPeer = netip.MustParseAddr("127.0.0.2")
	_, otherURL := f.open(t, other)
	if body, err := rtGet(f.client, otherURL+"/"); err == nil {
		t.Fatalf("a request from 127.0.0.1 to a listener for 127.0.0.2 was answered: %q", body)
	}
}

func TestRuntimeOpenValidates(t *testing.T) {
	f := newRtFixture(t)
	f.start(t)
	for _, tc := range []struct {
		name string
		edit func(s *core.ListenerSpec)
	}{
		{"no address", func(s *core.ListenerSpec) { s.Addr = netip.AddrPort{} }},
		{"port 0", func(s *core.ListenerSpec) { s.Addr = netip.MustParseAddrPort("172.31.255.1:0") }},
		{"a zoned address", func(s *core.ListenerSpec) { s.Addr = netip.MustParseAddrPort("[fe80::1%dp-panel]:9443") }},
		{"no allowed peer", func(s *core.ListenerSpec) { s.AllowedPeer = netip.Addr{} }},
		{"a forwarded header", func(s *core.ListenerSpec) { s.ClientHeader = "X-Forwarded-For" }},
		{"no public host", func(s *core.ListenerSpec) { s.PublicHost = "" }},
		{"no change", func(s *core.ListenerSpec) { s.ChangeID = "" }},
	} {
		spec := rtSpec
		tc.edit(&spec)
		var e *errcode.Error
		if _, err := f.rt.Open(context.Background(), spec); !errors.As(err, &e) || e.Code != errcode.InvalidInput {
			t.Errorf("%s: Open = %v, want request.invalid_input", tc.name, err)
		}
	}
	if rows, err := listListeners(context.Background(), f.db, core.ListenerOpen); err != nil || len(rows) != 0 {
		t.Fatalf("rows %+v, %v; want none recorded", rows, err)
	}
	mapped := rtSpec
	mapped.AllowedPeer = netip.MustParseAddr("::ffff:127.0.0.1")
	id, url := f.open(t, mapped)
	if info, err := f.rt.Get(context.Background(), id); err != nil || info.Spec.AllowedPeer != rtSpec.AllowedPeer {
		t.Fatalf("Get = %+v, %v; want the allowed peer unmapped", info, err)
	}
	if _, err := rtGet(f.client, url+"/"); err != nil {
		t.Fatalf("a request from the unmapped peer was refused: %v", err)
	}
}

// Composite recovery may open a listener before the network listeners
// exist: the row is recorded open, and Start binds it; a listener revoked in
// between is never bound.
func TestRuntimeOpenBeforeStart(t *testing.T) {
	ctx := context.Background()
	f := newRtFixture(t)
	early, err := f.rt.Open(ctx, rtSpec)
	if err != nil {
		t.Fatalf("Open before Start = %v", err)
	}
	gone, err := f.rt.Open(ctx, rtSpec)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.rt.Revoke(ctx, gone); err != nil {
		t.Fatalf("Revoke before Start = %v", err)
	}
	if addrs := f.rt.Addrs(); len(addrs) != 0 {
		t.Fatalf("Addrs = %v before Start, want none", addrs)
	}
	if got := f.state(t, early); got != core.ListenerOpen {
		t.Fatalf("state before Start %s, want open", got)
	}
	f.start(t)
	addrs := f.rt.Addrs()
	if len(addrs) != 1 {
		t.Fatalf("Addrs = %v after Start, want the open listener only", addrs)
	}
	if body, err := rtGet(f.client, "https://"+addrs[0].String()+"/"); err != nil || !strings.HasPrefix(body, early+" ") {
		t.Fatalf("GET = %q, %v; want the listener opened before Start", body, err)
	}
	if got := f.state(t, gone); got != core.ListenerRevoked {
		t.Errorf("state of the listener revoked before Start %s, want revoked", got)
	}
	f.mu.Lock()
	built := slices.Clone(f.built)
	f.mu.Unlock()
	if len(built) != 1 || built[0].ID != early {
		t.Errorf("handlers built for %+v, want only %s", built, early)
	}

	if err := f.rt.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.rt.Open(ctx, rtSpec); !errors.Is(err, errRuntimeShutDown) {
		t.Errorf("Open after Shutdown = %v, want shut down", err)
	}
}

func TestRuntimeBindFailureClosesTheListener(t *testing.T) {
	f := newRtFixture(t)
	f.start(t)
	f.binder.setFail(syscall.EADDRINUSE)
	if _, err := f.rt.Open(context.Background(), rtSpec); !errors.Is(err, syscall.EADDRINUSE) {
		t.Fatalf("Open = %v, want the bind error", err)
	}
	if closed, err := listListeners(context.Background(), f.db, core.ListenerClosed); err != nil || len(closed) != 1 {
		t.Fatalf("closed rows %+v, %v; want the failed listener closed", closed, err)
	}
}

func TestRuntimeRevokeEndsStreamsAndLetsResponsesFinish(t *testing.T) {
	ctx := context.Background()
	f := newRtFixture(t)
	f.start(t)
	id, url := f.open(t, rtSpec)

	slow := make(chan *http.Response, 1)
	slowErr := make(chan error, 1)
	go func() {
		resp, err := f.client.Get(url + "/slow")
		if err != nil {
			slowErr <- err
			return
		}
		slow <- resp
	}()
	stream, err := f.client.Get(url + "/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Body.Close() }()
	lines := bufio.NewReader(stream.Body)
	if line, err := lines.ReadString('\n'); err != nil || line != "event 1\n" {
		t.Fatalf("first stream line %q, %v", line, err)
	}
	for range 2 {
		<-f.entered
	}

	if err := f.rt.Revoke(ctx, id); err != nil {
		t.Fatal(err)
	}
	if got := f.state(t, id); got != core.ListenerRevoked {
		t.Errorf("state after Revoke %s, want revoked", got)
	}
	f.mu.Lock()
	isRevoked := f.isRev[id]
	f.mu.Unlock()
	if revoked, err := isRevoked(ctx); err != nil || !revoked {
		t.Errorf("isRevoked = %v, %v; want true", revoked, err)
	}
	if line, err := lines.ReadString('\n'); err != nil || line != "end revoked\n" {
		t.Errorf("stream after Revoke %q, %v; want its end", line, err)
	}
	if _, err := lines.ReadString('\n'); !errors.Is(err, io.EOF) {
		t.Errorf("the stream stayed open: %v", err)
	}
	if len(f.rt.Addrs()) != 0 {
		t.Errorf("Addrs = %v after Revoke, want none", f.rt.Addrs())
	}
	if _, err := net.DialTimeout("tcp", strings.TrimPrefix(url, "https://"), time.Second); err == nil {
		t.Error("a revoked listener accepted a new connection")
	}

	close(f.release)
	select {
	case resp := <-slow:
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil || string(body) != "slow done" || !resp.Close {
			t.Errorf("the running response %q, %v, close %v; want it written whole and its connection closed", body, err, resp.Close)
		}
	case err := <-slowErr:
		t.Fatalf("the running request failed: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("the running request never finished")
	}

	if err := f.rt.Revoke(ctx, id); err != nil {
		t.Errorf("a second Revoke = %v, want nil", err)
	}
	if err := f.rt.Close(ctx, id); err != nil || f.state(t, id) != core.ListenerClosed {
		t.Errorf("Close = %v, state %s; want closed", err, f.state(t, id))
	}
	if err := f.rt.Close(ctx, id); err != nil {
		t.Errorf("a second Close = %v, want nil", err)
	}
	if err := f.rt.Revoke(ctx, id); err != nil || f.state(t, id) != core.ListenerClosed {
		t.Errorf("Revoke of a closed listener = %v, state %s; want nil and still closed", err, f.state(t, id))
	}
}

func TestRuntimeCloseAndGetRefusals(t *testing.T) {
	ctx := context.Background()
	f := newRtFixture(t)
	f.start(t)
	id, _ := f.open(t, rtSpec)
	var e *errcode.Error
	if err := f.rt.Close(ctx, id); !errors.As(err, &e) || e.Code != errcode.InvalidInput {
		t.Errorf("Close of an open listener = %v, want request.invalid_input", err)
	}
	for name, err := range map[string]error{
		"Get":    func() error { _, err := f.rt.Get(ctx, "01MISSING"); return err }(),
		"Revoke": f.rt.Revoke(ctx, "01MISSING"),
		"Close":  f.rt.Close(ctx, "01MISSING"),
	} {
		if !errors.As(err, &e) || e.Code != errcode.NotFound {
			t.Errorf("%s of a missing listener = %v, want not_found", name, err)
		}
	}
}

func TestRuntimeStartReopensOnlyOpenListeners(t *testing.T) {
	f := newRtFixture(t)
	rsInsert(t, f.db, "L1", rtSpec, testStart)
	rsInsert(t, f.db, "L2", rtSpec, testStart.Add(time.Second))
	rsInsert(t, f.db, "L3", rtSpec, testStart.Add(2*time.Second))
	rsSet(t, f.db, "L2", core.ListenerRevoked, core.ListenerOpen)
	rsSet(t, f.db, "L3", core.ListenerRevoked, core.ListenerOpen)
	rsSet(t, f.db, "L3", core.ListenerClosed, core.ListenerRevoked)
	f.start(t)
	addrs := f.rt.Addrs()
	if len(addrs) != 1 {
		t.Fatalf("Addrs = %v, want only L1 bound", addrs)
	}
	if body, err := rtGet(f.client, "https://"+addrs[0].String()+"/"); err != nil || !strings.HasPrefix(body, "L1 ") {
		t.Fatalf("GET = %q, %v; want L1", body, err)
	}
	if err := f.rt.Start(context.Background(), f.rtHandler, f.tls); err == nil {
		t.Error("a second Start succeeded")
	}
}

func TestRuntimeStartRetriesAFailedBind(t *testing.T) {
	f := newRtFixture(t)
	rsInsert(t, f.db, "L1", rtSpec, testStart)
	rsInsert(t, f.db, "L2", rtSpec, testStart.Add(time.Second))
	f.binder.setFail(syscall.EADDRNOTAVAIL)
	f.start(t)
	if addrs := f.rt.Addrs(); len(addrs) != 0 {
		t.Fatalf("Addrs = %v while every bind fails", addrs)
	}
	if err := f.rt.Revoke(context.Background(), "L2"); err != nil {
		t.Fatal(err)
	}
	f.clock.Advance(CheckInterval)
	if addrs := f.rt.Addrs(); len(addrs) != 0 {
		t.Fatalf("Addrs = %v; the bind still fails", addrs)
	}
	f.binder.setFail(nil)
	f.clock.Advance(CheckInterval)
	addrs := f.rt.Addrs()
	if len(addrs) != 1 {
		t.Fatalf("Addrs = %v after the bind recovered, want L1 only", addrs)
	}
	if body, err := rtGet(f.client, "https://"+addrs[0].String()+"/"); err != nil || !strings.HasPrefix(body, "L1 ") {
		t.Fatalf("GET = %q, %v; want L1, never the revoked L2", body, err)
	}
	if f.clock.Pending() != 0 {
		t.Errorf("%d timers armed with nothing left to bind", f.clock.Pending())
	}
}

// TestRuntimeSessionResumption shares one tls.Config between a web listener
// and a runtime listener with shared ticket keys: a session from the web
// listener resumes on the runtime listener, and a resumed session gives no
// way past a revocation.
func TestRuntimeSessionResumption(t *testing.T) {
	f := newRtFixture(t)
	f.tls.SetSessionTicketKeys([][32]byte{{7, 7, 7}})
	f.client.Transport.(*http.Transport).TLSClientConfig.ClientSessionCache = tls.NewLRUClientSessionCache(8)
	f.client.Transport.(*http.Transport).DisableKeepAlives = true

	web := NewSet(SetConfig{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "web "+rtResumed(r))
	}), TLS: f.tls, Bind: (&fakeBinder{}).bind, Log: slog.New(slog.DiscardHandler)})
	t.Cleanup(func() { _ = web.Shutdown(context.Background()) })
	ep := Endpoint{Addr: netip.MustParseAddrPort("127.0.0.1:0"), TLS: true}
	if err := web.Update(context.Background(), []Endpoint{ep}); err != nil {
		t.Fatal(err)
	}
	if body, err := rtGet(f.client, "https://"+boundAddr(t, web, ep)+"/"); err != nil || body != "web resumed=false" {
		t.Fatalf("first web GET = %q, %v; want a full handshake", body, err)
	}

	f.start(t)
	id, url := f.open(t, rtSpec)
	if body, err := rtGet(f.client, url+"/"); err != nil || body != id+" resumed=true" {
		t.Fatalf("runtime GET = %q, %v; want the web listener's session resumed", body, err)
	}
	if err := f.rt.Revoke(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if body, err := rtGet(f.client, url+"/"); err == nil {
		t.Fatalf("a resumed session reached a revoked listener: %q", body)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `mkdir -p /root/.cache/w0c-tmp/40 && TMPDIR=/root/.cache/w0c-tmp/40 GOTOOLCHAIN=local go test -count=1 -run TestRuntime ./internal/controller/listen/`
Expected: FAIL to build: `undefined: Runtime`, `undefined: NewRuntime`, `undefined: RuntimeConfig`, `undefined: errRuntimeShutDown`.

- [ ] **Step 3: Write the implementation**

Create `internal/controller/listen/runtime.go`:

```go
package listen

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"sync"

	"github.com/oklog/ulid/v2"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// RevokeGrace bounds how long a revoked listener lets its running responses
// finish before it cuts the connections. A response cannot outlast the
// write timeout anyway; streams end as soon as the listener is revoked.
const RevokeGrace = WriteTimeout

var errRuntimeShutDown = errors.New("listen: the runtime listeners are shut down")

// RuntimeConfig configures NewRuntime. DB and Clock are required.
type RuntimeConfig struct {
	DB    *store.DB
	Clock core.Clock
	Log   *slog.Logger // nil discards
	Bind  BindFunc     // nil: Bind
}

// RuntimeHandlerFunc builds the handler of one runtime listener
// (api.Server.RuntimeHandler). revoked is closed once the listener is
// revoked; isRevoked reads the durable state.
type RuntimeHandlerFunc func(info core.ListenerInfo, revoked <-chan struct{}, isRevoked func(ctx context.Context) (bool, error)) http.Handler

// Runtime runs the runtime listeners recorded in core_listeners (spec 1.10):
// HTTPS listeners the controller opens and revokes without a restart, each
// accepting connections from one proxy only. It implements core.Listeners.
// Its methods are safe for concurrent use.
type Runtime struct {
	cfg RuntimeConfig

	// mu guards everything below. It is held while Start reads the open rows
	// and binds them, and while Open records a row and binds it, so an Open
	// that races Start is bound by one of the two.
	mu      sync.Mutex
	started bool
	closed  bool
	handler RuntimeHandlerFunc
	tls     *tls.Config
	live    map[string]*runtimeListener
	pending map[string]core.ListenerInfo // open rows whose bind failed at Start, retried every CheckInterval
	timer   core.Timer
	serving sync.WaitGroup // the Serve goroutines
}

var _ core.Listeners = (*Runtime)(nil)

// runtimeListener is one bound listener.
type runtimeListener struct {
	info    core.ListenerInfo
	srv     *http.Server
	ln      net.Listener
	revoked chan struct{}
	stopped bool // the listener no longer accepts
}

// NewRuntime returns the runtime listeners over cfg; nothing is bound until
// Start.
func NewRuntime(cfg RuntimeConfig) *Runtime {
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}
	if cfg.Bind == nil {
		cfg.Bind = Bind
	}
	return &Runtime{cfg: cfg, live: map[string]*runtimeListener{}, pending: map[string]core.ListenerInfo{}}
}

// Start binds every listener in state open, those Open recorded before
// Start included, with handler and tlsConfig, which each listener clones. A
// listener found revoked or closed is never opened. A listener whose bind
// fails stays open and is retried every CheckInterval; Start fails only when
// the rows cannot be read.
func (r *Runtime) Start(ctx context.Context, handler RuntimeHandlerFunc, tlsConfig *tls.Config) error {
	if handler == nil || tlsConfig == nil {
		return errors.New("listen: Runtime.Start needs a handler and a TLS configuration")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.started || r.closed {
		return errors.New("listen: the runtime listeners were already started")
	}
	rows, err := listListeners(ctx, r.cfg.DB, core.ListenerOpen)
	if err != nil {
		return err
	}
	r.started, r.handler, r.tls = true, handler, tlsConfig
	for _, info := range rows {
		if err := r.bindLocked(ctx, info); err != nil {
			r.cfg.Log.Warn("listen: a runtime listener could not be bound; retrying", "id", info.ID,
				"addr", info.Spec.Addr.String(), "err", err)
			r.pending[info.ID] = info
		}
	}
	r.armLocked()
	return nil
}

// Open records spec as a listener in state open and, once Start has run,
// binds it; before Start the row waits for Start, which binds every open
// row. When the bind fails the listener is closed and the error returned.
func (r *Runtime) Open(ctx context.Context, spec core.ListenerSpec) (string, error) {
	spec, err := validListenerSpec(spec)
	if err != nil {
		return "", err
	}
	now := r.cfg.Clock.Now()
	id := ulid.MustNew(ulid.Timestamp(now), rand.Reader).String()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return "", errRuntimeShutDown
	}
	if err := r.cfg.DB.Write(ctx, func(tx *sql.Tx) error { return insertListener(ctx, tx, id, spec, now) }); err != nil {
		return "", err
	}
	if !r.started {
		return id, nil
	}
	err = r.bindLocked(ctx, core.ListenerInfo{ID: id, Spec: spec, State: core.ListenerOpen})
	if err == nil {
		return id, nil
	}
	closeErr := r.cfg.DB.Write(context.WithoutCancel(ctx), func(tx *sql.Tx) error {
		_, err := setListenerState(ctx, tx, id, core.ListenerClosed, []string{core.ListenerOpen}, r.cfg.Clock.Now())
		return err
	})
	return "", errors.Join(err, closeErr)
}

// Revoke commits state revoked first, then ends the listener's streams,
// stops accepting, and closes its connections once their current response
// is written. Revoking a revoked or closed listener succeeds.
func (r *Runtime) Revoke(ctx context.Context, id string) error {
	var changed bool
	err := r.cfg.DB.Write(ctx, func(tx *sql.Tx) error {
		var err error
		changed, err = setListenerState(ctx, tx, id, core.ListenerRevoked, []string{core.ListenerOpen}, r.cfg.Clock.Now())
		return err
	})
	if err != nil {
		return err
	}
	if !changed {
		if _, err := getListener(ctx, r.cfg.DB, id); err != nil {
			return err
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.pending, id)
	if l := r.live[id]; l != nil {
		r.stopLocked(l)
	}
	return nil
}

// Close moves a revoked listener to closed. Closing a closed listener
// succeeds; an open one must be revoked first.
func (r *Runtime) Close(ctx context.Context, id string) error {
	var changed bool
	err := r.cfg.DB.Write(ctx, func(tx *sql.Tx) error {
		var err error
		changed, err = setListenerState(ctx, tx, id, core.ListenerClosed, []string{core.ListenerRevoked}, r.cfg.Clock.Now())
		return err
	})
	if err != nil {
		return err
	}
	if !changed {
		info, err := getListener(ctx, r.cfg.DB, id)
		if err != nil {
			return err
		}
		if info.State == core.ListenerOpen {
			return errcode.New(errcode.InvalidInput, "runtime listener "+id+" is open; revoke it first")
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if l := r.live[id]; l != nil {
		r.stopLocked(l)
		delete(r.live, id)
	}
	return nil
}

// Get reads listener id.
func (r *Runtime) Get(ctx context.Context, id string) (core.ListenerInfo, error) {
	return getListener(ctx, r.cfg.DB, id)
}

// Addrs are the bound addresses of the listeners that still accept, sorted.
func (r *Runtime) Addrs() []netip.AddrPort {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]netip.AddrPort, 0, len(r.live))
	for _, l := range r.live {
		if l.stopped {
			continue
		}
		if ap, err := netip.ParseAddrPort(l.ln.Addr().String()); err == nil {
			out = append(out, netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port()))
		}
	}
	slices.SortFunc(out, netip.AddrPort.Compare)
	return out
}

// Shutdown stops every listener, letting running requests finish until ctx
// ends, and waits for the serving goroutines. The rows keep their state, so
// the next Start reopens the open ones.
func (r *Runtime) Shutdown(ctx context.Context) error {
	r.mu.Lock()
	r.closed = true
	if r.timer != nil {
		r.timer.Stop()
	}
	live := r.live
	r.live = map[string]*runtimeListener{}
	r.pending = map[string]core.ListenerInfo{}
	r.mu.Unlock()
	var errs []error
	for _, l := range live {
		l.srv.SetKeepAlivesEnabled(false)
		if err := l.srv.Shutdown(ctx); err != nil {
			_ = l.srv.Close()
			errs = append(errs, fmt.Errorf("listen: runtime listener %s: %w", l.info.ID, err))
		}
		_ = l.ln.Close()
	}
	r.serving.Wait()
	return errors.Join(errs...)
}

// bindLocked binds info and serves it, unless its row is no longer open: a
// Revoke that committed after the rows were read must win.
func (r *Runtime) bindLocked(ctx context.Context, info core.ListenerInfo) error {
	if r.closed {
		return errRuntimeShutDown
	}
	ln, err := r.cfg.Bind(ctx, Endpoint{Addr: info.Spec.Addr, TLS: true})
	if err != nil {
		return err
	}
	row, err := getListener(ctx, r.cfg.DB, info.ID)
	if err != nil || row.State != core.ListenerOpen {
		_ = ln.Close()
		if err != nil {
			return err
		}
		return nil
	}
	l := &runtimeListener{info: row, ln: &peerListener{Listener: ln, allowed: info.Spec.AllowedPeer, id: info.ID, log: r.cfg.Log},
		revoked: make(chan struct{})}
	id := info.ID
	isRevoked := func(ctx context.Context) (bool, error) {
		row, err := getListener(ctx, r.cfg.DB, id)
		if err != nil {
			return true, err
		}
		return row.State != core.ListenerOpen, nil
	}
	l.srv = &http.Server{
		Handler:           r.handler(row, l.revoked, isRevoked),
		TLSConfig:         r.tls.Clone(),
		ReadHeaderTimeout: ReadHeaderTimeout,
		ReadTimeout:       ReadTimeout,
		WriteTimeout:      WriteTimeout,
		IdleTimeout:       IdleTimeout,
		ErrorLog:          slog.NewLogLogger(r.cfg.Log.Handler(), slog.LevelWarn),
	}
	r.live[id] = l
	r.serving.Go(func() {
		err := l.srv.ServeTLS(l.ln, "", "")
		r.mu.Lock()
		stopped := l.stopped || r.closed
		r.mu.Unlock()
		if !stopped && !errors.Is(err, http.ErrServerClosed) {
			r.cfg.Log.Error("listen: a runtime listener stopped serving", "id", id, "err", err)
		}
	})
	r.cfg.Log.Info("listen: serving a runtime listener", "id", id, "addr", ln.Addr().String(),
		"peer", info.Spec.AllowedPeer.String())
	return nil
}

// stopLocked ends l's streams, stops accepting at once, and closes l's
// connections after their current response, cutting them after RevokeGrace.
func (r *Runtime) stopLocked(l *runtimeListener) {
	if l.stopped {
		return
	}
	l.stopped = true
	close(l.revoked)
	l.srv.SetKeepAlivesEnabled(false)
	_ = l.ln.Close()
	ctx, cancel := context.WithCancel(context.Background())
	timer := r.cfg.Clock.AfterFunc(RevokeGrace, cancel)
	r.serving.Go(func() {
		defer cancel()
		defer timer.Stop()
		if err := l.srv.Shutdown(ctx); err != nil {
			_ = l.srv.Close()
		}
	})
	r.cfg.Log.Info("listen: revoked a runtime listener", "id", l.info.ID)
}

// armLocked schedules the next retry of the pending binds.
func (r *Runtime) armLocked() {
	if len(r.pending) == 0 || r.closed {
		return
	}
	r.timer = r.cfg.Clock.AfterFunc(CheckInterval, r.retry)
}

func (r *Runtime) retry() {
	ctx, cancel := context.WithTimeout(context.Background(), CheckInterval)
	defer cancel()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	for id, info := range r.pending {
		if err := r.bindLocked(ctx, info); err != nil {
			r.cfg.Log.Warn("listen: a runtime listener could not be bound; retrying", "id", id, "err", err)
			continue
		}
		delete(r.pending, id)
	}
	r.armLocked()
}

// validListenerSpec checks spec and returns it with its addresses unmapped.
func validListenerSpec(spec core.ListenerSpec) (core.ListenerSpec, error) {
	bad := func(detail string) (core.ListenerSpec, error) {
		return core.ListenerSpec{}, errcode.New(errcode.InvalidInput, "runtime listener: "+detail)
	}
	switch {
	case !spec.Addr.IsValid() || spec.Addr.Port() == 0 || spec.Addr.Addr().Zone() != "":
		return bad("the address needs an IP address without a zone and a port")
	case !spec.AllowedPeer.IsValid() || spec.AllowedPeer.Zone() != "":
		return bad("the allowed peer needs an IP address without a zone")
	case spec.ClientHeader != core.ClientHeaderNPM && spec.ClientHeader != core.ClientHeaderTunnel:
		return bad("the client header must be " + core.ClientHeaderNPM + " or " + core.ClientHeaderTunnel)
	case spec.PublicHost == "" || spec.ChangeID == "":
		return bad("the public host and the change id are required")
	}
	spec.Addr = netip.AddrPortFrom(spec.Addr.Addr().Unmap(), spec.Addr.Port())
	spec.AllowedPeer = spec.AllowedPeer.Unmap()
	return spec, nil
}

// peerListener accepts connections from the allowed peer only and closes
// every other one before any byte is read.
type peerListener struct {
	net.Listener
	allowed netip.Addr
	id      string
	log     *slog.Logger
}

func (l *peerListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		ap, err := netip.ParseAddrPort(c.RemoteAddr().String())
		if err == nil && ap.Addr().Unmap().WithZone("") == l.allowed {
			return c, nil
		}
		l.log.Warn("listen: refused a connection to a runtime listener from another peer", "id", l.id,
			"peer", c.RemoteAddr().String())
		_ = c.Close()
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `TMPDIR=/root/.cache/w0c-tmp/40 GOTOOLCHAIN=local go test -count=1 -race ./internal/controller/listen/ && TMPDIR=/root/.cache/w0c-tmp/40 GOTOOLCHAIN=local go test -count=20 -race -run 'TestRuntime' ./internal/controller/listen/ && TMPDIR=/root/.cache/w0c-tmp/40 golangci-lint run --allow-serial-runners ./internal/controller/listen/...`
Expected: `ok  	github.com/darkraise/darkpanel/internal/controller/listen` twice (the second run repeats the listener tests twenty times under the race detector, a few minutes) and `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add internal/controller/listen/runtime.go internal/controller/listen/runtime_test.go
git commit -m "feat(listen): run runtime listeners"
rm -rf /root/.cache/w0c-tmp/40
```

### Task 41: API extension routes on the web surface

This task builds the web half of C18 in `internal/controller/api`: `api.Config.Extensions`, validation of every contributed route, mounting of the `SurfaceWeb` routes on the network mux, and the stream lifecycle of `Stream` routes. A web route sits behind the same authentication and CSRF middleware as every network route (`authenticate`), plus a scope check against `Route.Scope` (`auth.forbidden` below it). `New` refuses, with an error naming the pattern, a route whose surface is unknown, whose handler is nil, whose method is not GET, POST, PUT, PATCH or DELETE, whose path does not lie under `/api/v1/` or names a subtree (a trailing `/`; under `/api/` `routeAccess` always demands credentials, so no contribution can claim a public UI path), whose web scope is not read, operate or admin, or whose pattern clashes with an existing route (`http.ServeMux` panics on a clash; `mount` turns the panic into the error). A `Stream` route shares the built-in streams' lifecycle, reusing their helpers: its requests authenticate passively (`identify` asks `extensionStreamRoute`, a mux holding only the stream patterns, beside `streamRoute`), so an open stream never extends its session; its write deadline is `SSEWriteTimeout` renewed on every header and body write in place of the listeners' 60 s total; and its handler's context is cancelled at `SSEHardLimit`, at `CloseStreams`, when the runtime listener it came through is revoked (`core.Proxied.Revoked`), and — on the web — when the `SSERecheck` re-authentication through `sseRecheck` fails (a revoked token, a logout, an expiry). Through a runtime listener it also gets `X-Accel-Buffering: no`. There is no per-principal cap shared by the built-in streams to join: the four-stream cap is specific to `GET /api/v1/events` (`eventSlot`), and module relays are capped by the dispatcher, so a stream route bounds its own resources. Socket routes pass validation here and are mounted by Task 44. Test helpers carry the prefix `xr` (`xrHandler`, `xrHarness`, `xrWaiter`, `xrOpen`, `xrEnded`, `xrAuth`); the tests also use `pxProxied` (Task 31) and `sseRecorder`, `sseAuth`, `sseIdentity`, `sseWait` from `sse_test.go`.

**Files:**
- Create: `internal/controller/api/extensions.go`
- Modify: `internal/controller/api/server.go` (imports, `Config`, `Server`, `New`, `identify`)
- Test: `internal/controller/api/extensions_test.go`

**Interfaces:**
- Consumes: C2 (`ext.Route`, `ext.SurfaceWeb`, `ext.SurfaceSocket`), C4 (`core.Proxied`, `core.ProxiedFrom`).
- Produces: C18's `api.Config.Extensions []ext.Route` and the web mounting; Task 44 adds the socket mounting and `OpenAPIRoutes`; Task 46 fills `Extensions`.

**Items:** 52

**Depends on:** Task 19, Task 31

**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6
**Lenses:** security

- [ ] **Step 1: Write the failing test**

Create `internal/controller/api/extensions_test.go`:

```go
package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/ext"
)

// xrHandler answers 200 with the caller's actor, so a test sees who reached
// it.
var xrHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	c, _ := callerFrom(r.Context())
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(c.principal.Actor))
})

// xrHarness is newHarness whose server also serves routes, with the
// harness's clock as the streams' clock.
func xrHarness(t *testing.T, routes ...ext.Route) *harness {
	t.Helper()
	h := newHarness(t)
	h.cfg.Extensions, h.cfg.Clock = routes, h.clock
	srv, err := New(h.cfg)
	if err != nil {
		t.Fatal(err)
	}
	h.srv = srv
	return h
}

func TestExtensionWebRoute(t *testing.T) {
	h := xrHarness(t,
		ext.Route{Surface: ext.SurfaceWeb, Method: http.MethodGet, Path: "/api/v1/xr/items", Summary: "List items",
			Scope: core.ScopeAdmin, Handler: xrHandler},
		ext.Route{Surface: ext.SurfaceWeb, Method: http.MethodPost, Path: "/api/v1/xr/items/{id}/touch", Summary: "Touch an item",
			Scope: core.ScopeRead, Handler: xrHandler},
	)
	session, csrf := h.login()
	tests := []struct {
		name   string
		r      *http.Request
		status int
		code   string
	}{
		{"no credentials", request("GET", "/api/v1/xr/items", ""), http.StatusUnauthorized, "auth.required"},
		{"scope too low", withToken(request("GET", "/api/v1/xr/items", ""), h.token(core.ScopeOperate)), http.StatusForbidden, "auth.forbidden"},
		{"admin token", withToken(request("GET", "/api/v1/xr/items", ""), h.token(core.ScopeAdmin)), http.StatusOK, ""},
		{"session without CSRF", withSession(request("POST", "/api/v1/xr/items/i1/touch", ""), session, ""), http.StatusForbidden, "auth.csrf"},
		{"session with CSRF", withSession(request("POST", "/api/v1/xr/items/i1/touch", ""), session, csrf), http.StatusOK, ""},
		{"wrong method", withToken(request("DELETE", "/api/v1/xr/items", ""), h.token(core.ScopeAdmin)), http.StatusMethodNotAllowed, "method_not_allowed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := h.serve(tt.r)
			if rec.Code != tt.status {
				t.Fatalf("answer %d %s, want %d", rec.Code, rec.Body.String(), tt.status)
			}
			if tt.code != "" && !strings.Contains(rec.Body.String(), `"code":"`+tt.code+`"`) {
				t.Errorf("answer %s, want code %s", rec.Body.String(), tt.code)
			}
			if tt.code == "" && !strings.HasPrefix(rec.Body.String(), "user:") && !strings.HasPrefix(rec.Body.String(), "token:") {
				t.Errorf("the handler saw actor %q", rec.Body.String())
			}
		})
	}
}

func TestExtensionRouteRefused(t *testing.T) {
	web := func(method, path string, scope core.Scope) ext.Route {
		return ext.Route{Surface: ext.SurfaceWeb, Method: method, Path: path, Scope: scope, Handler: xrHandler}
	}
	tests := []struct {
		name   string
		routes []ext.Route
		want   string
	}{
		{"clash with a built-in route", []ext.Route{web("GET", "/api/v1/changes", core.ScopeRead)}, "GET /api/v1/changes"},
		{"the same route twice", []ext.Route{web("GET", "/api/v1/xr/a", core.ScopeRead), web("GET", "/api/v1/xr/a", core.ScopeRead)},
			"GET /api/v1/xr/a"},
		{"wildcards that overlap", []ext.Route{web("GET", "/api/v1/xr/{a}/b", core.ScopeRead), web("GET", "/api/v1/xr/a/{b}", core.ScopeRead)},
			"/api/v1/xr/a/{b}"},
		{"a UI path", []ext.Route{web("GET", "/settings/xr", core.ScopeRead)}, "must lie under /api/v1/"},
		{"a subtree", []ext.Route{web("GET", "/api/v1/xr/", core.ScopeRead)}, "not a subtree"},
		{"an unknown method", []ext.Route{web("TRACE", "/api/v1/xr/a", core.ScopeRead)}, `method "TRACE"`},
		{"no scope on the web", []ext.Route{web("GET", "/api/v1/xr/a", "")}, `scope ""`},
		{"no handler", []ext.Route{{Surface: ext.SurfaceWeb, Method: "GET", Path: "/api/v1/xr/a", Scope: core.ScopeRead}}, "no handler"},
		{"an unknown surface", []ext.Route{{Surface: 7, Method: "GET", Path: "/api/v1/xr/a", Handler: xrHandler}}, "unknown surface 7"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			h.cfg.Extensions = tt.routes
			if _, err := New(h.cfg); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("New: %v, want an error containing %q", err, tt.want)
			}
		})
	}
}

// TestExtensionStreamDeadline: a stream route's every write renews a
// SSEWriteTimeout deadline; an ordinary route sets none.
func TestExtensionStreamDeadline(t *testing.T) {
	writer := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("one"))
		_, _ = w.Write([]byte("two"))
	})
	h := xrHarness(t,
		ext.Route{Surface: ext.SurfaceWeb, Method: "GET", Path: "/api/v1/xr/stream", Scope: core.ScopeRead, Stream: true, Handler: writer},
		ext.Route{Surface: ext.SurfaceWeb, Method: "GET", Path: "/api/v1/xr/plain", Scope: core.ScopeRead, Handler: writer},
	)
	token := h.token(core.ScopeRead)
	rec := newSSERecorder()
	h.srv.Handler().ServeHTTP(rec, withToken(request("GET", "/api/v1/xr/stream", ""), token))
	if rec.text() != "onetwo" || len(rec.deadlines) != 4 {
		t.Fatalf("stream body %q with %d deadlines, want onetwo with 4", rec.text(), len(rec.deadlines))
	}
	for _, d := range rec.deadlines {
		if d <= SSEWriteTimeout-5*time.Second || d > SSEWriteTimeout {
			t.Errorf("deadline %v ahead, want about %v", d, SSEWriteTimeout)
		}
	}
	plain := newSSERecorder()
	h.srv.Handler().ServeHTTP(plain, withToken(request("GET", "/api/v1/xr/plain", ""), token))
	if plain.text() != "onetwo" || len(plain.deadlines) != 0 {
		t.Errorf("plain body %q with %d deadlines, want onetwo with none", plain.text(), len(plain.deadlines))
	}
}

// TestExtensionStreamRevoked: through a runtime listener a stream route
// gets X-Accel-Buffering: no and a context that ends with the listener.
func TestExtensionStreamRevoked(t *testing.T) {
	waiter := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		<-r.Context().Done()
	})
	h := xrHarness(t, ext.Route{Surface: ext.SurfaceWeb, Method: "GET", Path: "/api/v1/xr/follow", Scope: core.ScopeRead,
		Stream: true, Handler: waiter})
	revoked := make(chan struct{})
	r := withToken(request("GET", "/api/v1/xr/follow", ""), h.token(core.ScopeRead))
	r = r.WithContext(core.WithProxied(r.Context(), pxProxied(revoked)))
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		h.srv.Handler().ServeHTTP(rec, r)
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("the stream ended before the listener was revoked")
	case <-time.After(50 * time.Millisecond):
	}
	close(revoked)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the stream did not end when the listener was revoked")
	}
	if got := rec.Header().Get("X-Accel-Buffering"); got != "no" {
		t.Errorf("X-Accel-Buffering %q, want no", got)
	}
}

// xrWaiter is a stream handler that holds the response open until its
// context ends.
var xrWaiter = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	<-r.Context().Done()
})

// xrOpen serves r in the background and returns a channel closed once the
// handler returned, after the stream's watcher armed its timers.
func xrOpen(t *testing.T, h *harness, r *http.Request) <-chan struct{} {
	t.Helper()
	base := h.clock.Pending()
	done := make(chan struct{})
	go func() {
		h.srv.Handler().ServeHTTP(httptest.NewRecorder(), r)
		close(done)
	}()
	sseWait(t, "the stream's timers", func() bool { return h.clock.Pending() >= base+2 })
	return done
}

// xrEnded fails t unless done closes while step keeps being applied.
func xrEnded(t *testing.T, done <-chan struct{}, step func()) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case <-done:
			return
		case <-deadline:
			t.Fatal("the stream did not end")
		case <-time.After(5 * time.Millisecond):
			step()
		}
	}
}

// TestExtensionStreamEnds: a contributed stream route ends like the
// built-in streams: on a revoked token at the next re-authentication, at
// the hard limit and at CloseStreams, and not while its credentials hold.
func TestExtensionStreamEnds(t *testing.T) {
	route := ext.Route{Surface: ext.SurfaceWeb, Method: "GET", Path: "/api/v1/xr/follow", Scope: core.ScopeRead,
		Stream: true, Handler: xrWaiter}
	t.Run("credentials hold", func(t *testing.T) {
		h := xrHarness(t, route)
		done := xrOpen(t, h, withToken(request("GET", "/api/v1/xr/follow", ""), h.token(core.ScopeRead)))
		for range 3 {
			h.clock.Advance(SSERecheck)
			time.Sleep(10 * time.Millisecond)
		}
		select {
		case <-done:
			t.Fatal("the stream ended while its token holds")
		default:
		}
		h.srv.CloseStreams()
		xrEnded(t, done, func() {})
	})
	t.Run("token revoked mid-stream", func(t *testing.T) {
		h := xrHarness(t, route)
		info, token, err := h.auth.CreateToken(context.Background(), h.user.ID, "follow", core.ScopeRead, 0)
		if err != nil {
			t.Fatal(err)
		}
		done := xrOpen(t, h, withToken(request("GET", "/api/v1/xr/follow", ""), token))
		if _, err := h.auth.RevokeToken(context.Background(), h.user.ID, info.ID); err != nil {
			t.Fatal(err)
		}
		xrEnded(t, done, func() { h.clock.Advance(SSERecheck) })
	})
	t.Run("hard limit", func(t *testing.T) {
		h := xrHarness(t, route)
		done := xrOpen(t, h, withToken(request("GET", "/api/v1/xr/follow", ""), h.token(core.ScopeRead)))
		xrEnded(t, done, func() { h.clock.Advance(SSEHardLimit) })
	})
	t.Run("CloseStreams", func(t *testing.T) {
		h := xrHarness(t, route)
		done := xrOpen(t, h, withToken(request("GET", "/api/v1/xr/follow", ""), h.token(core.ScopeRead)))
		h.srv.CloseStreams()
		xrEnded(t, done, func() {})
	})
}

// xrAuth is an sseAuth that records whether each authentication was
// passive.
type xrAuth struct {
	sseAuth
	mu      sync.Mutex
	passive []bool
}

func (a *xrAuth) Authenticate(ctx context.Context, c auth.Credentials) (auth.Identity, error) {
	a.mu.Lock()
	a.passive = append(a.passive, c.Passive)
	a.mu.Unlock()
	return a.sseAuth.Authenticate(ctx, c)
}

// TestExtensionStreamPassive: requests for a contributed stream route
// authenticate passively, so an open stream never extends its session;
// other contributed routes do not.
func TestExtensionStreamPassive(t *testing.T) {
	h := newHarness(t)
	a := &xrAuth{}
	a.set(sseIdentity(h.clock.Now().Add(time.Hour)), nil)
	h.cfg.Auth, h.cfg.Clock = a, h.clock
	h.cfg.Extensions = []ext.Route{
		{Surface: ext.SurfaceWeb, Method: "GET", Path: "/api/v1/xr/{id}/follow", Scope: core.ScopeRead, Stream: true, Handler: xrHandler},
		{Surface: ext.SurfaceWeb, Method: "GET", Path: "/api/v1/xr/{id}", Scope: core.ScopeRead, Handler: xrHandler},
	}
	srv, err := New(h.cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path    string
		passive bool
	}{
		{"/api/v1/xr/i1/follow", true},
		{"/api/v1/xr/i1", false},
		{"/api/v1/changes", false},
	} {
		a.mu.Lock()
		a.passive = nil
		a.mu.Unlock()
		srv.Handler().ServeHTTP(httptest.NewRecorder(), withToken(request("GET", tc.path, ""), "any"))
		a.mu.Lock()
		got := a.passive
		a.mu.Unlock()
		if len(got) == 0 || got[0] != tc.passive {
			t.Errorf("GET %s authenticated with Passive %v, want %v", tc.path, got, tc.passive)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mkdir -p /root/.cache/w0c-tmp/41 && TMPDIR=/root/.cache/w0c-tmp/41 GOTOOLCHAIN=local go test -count=1 -run TestExtension ./internal/controller/api/`
Expected: FAIL to build with `h.cfg.Extensions undefined (type Config has no field or method Extensions)`

- [ ] **Step 3: Write the implementation**

Create `internal/controller/api/extensions.go`:

```go
package api

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/ext"
)

// extensionPrefix is where every contributed route lives: under the API,
// routeAccess authenticates it, and no contribution can claim a UI path.
const extensionPrefix = "/api/v1/"

var extensionMethods = []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}

// checkExtension refuses a contributed route that New could not mount
// safely.
func checkExtension(rt ext.Route) error {
	pattern := rt.Method + " " + rt.Path
	switch {
	case rt.Surface != ext.SurfaceWeb && rt.Surface != ext.SurfaceSocket:
		return fmt.Errorf("api: extension route %s: unknown surface %d", pattern, rt.Surface)
	case rt.Handler == nil:
		return fmt.Errorf("api: extension route %s: no handler", pattern)
	case !slices.Contains(extensionMethods, rt.Method):
		return fmt.Errorf("api: extension route %s: method %q is not one of %s", pattern, rt.Method, strings.Join(extensionMethods, ", "))
	case !strings.HasPrefix(rt.Path, extensionPrefix) || strings.HasSuffix(rt.Path, "/"):
		return fmt.Errorf("api: extension route %s: the path must lie under %s and name one route, not a subtree", pattern, extensionPrefix)
	case rt.Surface == ext.SurfaceWeb && !slices.Contains([]core.Scope{core.ScopeRead, core.ScopeOperate, core.ScopeAdmin}, rt.Scope):
		return fmt.Errorf("api: extension route %s: scope %q is not read, operate or admin", pattern, rt.Scope)
	}
	return nil
}

// mount adds h at pattern to mux. ServeMux panics on a pattern that is
// invalid or conflicts with one it holds; for a contributed route that is
// a mistake that must fail startup, so the panic becomes an error.
func mount(mux *http.ServeMux, pattern string, h http.Handler) (err error) {
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("api: extension route %s: %v", pattern, v)
		}
	}()
	mux.Handle(pattern, h)
	return nil
}

// mountWebExtensions mounts the web surface's contributed routes on the
// network mux, behind the authentication and CSRF middleware every network
// route has, each with its scope check. Stream routes are also recorded on
// streamMux, so their requests authenticate passively like the built-in
// streams.
func (s *Server) mountWebExtensions() error {
	s.streamMux = http.NewServeMux()
	for _, rt := range s.cfg.Extensions {
		if rt.Surface != ext.SurfaceWeb {
			continue
		}
		next := s.extensionStream(rt)
		need := rt.Scope
		h := requireCaller(s.log, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c, _ := callerFrom(r.Context())
			if err := authorize(c.principal, need); err != nil {
				writeProblem(w, s.log, err)
				return
			}
			next.ServeHTTP(w, r)
		}))
		pattern := rt.Method + " " + rt.Path
		if err := mount(s.mux, pattern, h); err != nil {
			return err
		}
		if rt.Stream {
			// The network mux accepted the pattern, so this one cannot clash.
			s.streamMux.Handle(pattern, http.NotFoundHandler())
		}
	}
	return nil
}

// extensionStreamRoute reports whether r asks for a contributed web stream
// route.
func (s *Server) extensionStreamRoute(r *http.Request) bool {
	if s.streamMux == nil {
		return false
	}
	_, pattern := s.streamMux.Handler(r)
	return pattern != ""
}

// extensionStream is rt's handler. A stream route instead gets the
// built-in streams' lifecycle: a write deadline of SSEWriteTimeout renewed
// on every write in place of the listeners' 60 s total, and a context that
// ends at SSEHardLimit, at CloseStreams, when the runtime listener it came
// through is revoked, and on the web when the SSERecheck re-authentication
// fails; through a runtime listener it also gets X-Accel-Buffering: no
// (spec 1.6, 1.10).
func (s *Server) extensionStream(rt ext.Route) http.Handler {
	if !rt.Stream {
		return rt.Handler
	}
	recheck := rt.Surface == ext.SurfaceWeb
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		var revoked <-chan struct{}
		if p, ok := core.ProxiedFrom(ctx); ok {
			revoked = p.Revoked
			w.Header().Set("X-Accel-Buffering", "no")
		}
		r = r.WithContext(ctx)
		// The watcher re-authenticates a copy, so it never reads the
		// request the handler may still be using.
		go s.watchStream(ctx, cancel, r.Clone(ctx), recheck, revoked)
		dw := &deadlineWriter{ResponseWriter: w}
		dw.renew()
		rt.Handler.ServeHTTP(dw, r)
	})
}

// watchStream cancels a stream route's context when the stream must end,
// and returns once it is done. It ends the stream on a failed
// re-authentication of r only with recheck: the socket's peer check holds
// for the connection's life.
func (s *Server) watchStream(ctx context.Context, cancel context.CancelFunc, r *http.Request, recheck bool, revoked <-chan struct{}) {
	defer cancel()
	c, _ := callerFrom(r.Context())
	expires := c.identity.ExpiresAt
	clock := s.cfg.Clock
	hard, check := make(chan struct{}, 1), make(chan struct{}, 1)
	hardT := clock.AfterFunc(SSEHardLimit, sseSignal(hard))
	defer hardT.Stop()
	var checkT core.Timer
	if recheck {
		checkT = clock.AfterFunc(SSERecheck, sseSignal(check))
	}
	defer func() {
		if checkT != nil {
			checkT.Stop()
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case <-hard:
			return
		case <-s.streamsDone:
			return
		case <-revoked:
			return
		case <-check:
			if s.sseRecheck(r, &expires) != "" {
				return
			}
			checkT = clock.AfterFunc(SSERecheck, sseSignal(check))
		}
	}
}

// deadlineWriter renews the connection's write deadline before every
// header and body write.
type deadlineWriter struct {
	http.ResponseWriter
}

// renew moves the write deadline SSEWriteTimeout ahead. Its error is
// dropped: a writer without deadlines is one only tests use, and a broken
// connection fails the write that follows.
func (w *deadlineWriter) renew() {
	_ = http.NewResponseController(w.ResponseWriter).SetWriteDeadline(time.Now().Add(SSEWriteTimeout))
}

func (w *deadlineWriter) WriteHeader(status int) {
	w.renew()
	w.ResponseWriter.WriteHeader(status)
}

func (w *deadlineWriter) Write(b []byte) (int, error) {
	w.renew()
	return w.ResponseWriter.Write(b)
}

// Unwrap lets a handler's ResponseController reach the connection's
// flusher and deadlines.
func (w *deadlineWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
```

In `internal/controller/api/server.go`:

1. Add `"github.com/darkraise/darkpanel/internal/core/ext"` to the imports after the `errcode` import.
2. At the end of `Config`, after the `Clock` field, add:

```go
	// Extensions are the routes modules and the backup package contribute
	// (spec 1.12): web routes join the network mux, socket routes the
	// controller socket's.
	Extensions []ext.Route
```

3. In `Server`, after the `handler   http.Handler` field, add:

```go
	streamMux *http.ServeMux // the contributed web stream routes' patterns
```

4. Replace the comment `// New returns a server for cfg.` by:

```go
// New returns a server for cfg. It fails when a contributed route is
// malformed or clashes with another route of its surface.
```

5. In `New`, between the `for _, rt := range s.networkRoutes() { ... }` loop and `s.mux.HandleFunc("/", s.networkFallback)`, insert:

```go
	for _, rt := range cfg.Extensions {
		if err := checkExtension(rt); err != nil {
			return nil, err
		}
	}
	if err := s.mountWebExtensions(); err != nil {
		return nil, err
	}
```

6. In `identify`, replace `Passive:       streamRoute(r),` by `Passive:       streamRoute(r) || s.extensionStreamRoute(r),`, and the last line of its comment, `// stream request is authenticated passively (streamRoute).`, by:

```go
// stream request is authenticated passively (streamRoute, and the
// contributed stream routes).
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `TMPDIR=/root/.cache/w0c-tmp/41 GOTOOLCHAIN=local go test -count=1 -race -run 'TestExtension' ./internal/controller/api/ && TMPDIR=/root/.cache/w0c-tmp/41 GOTOOLCHAIN=local go test -count=1 ./internal/controller/api/ && TMPDIR=/root/.cache/w0c-tmp/41 golangci-lint run --allow-serial-runners ./internal/controller/api/`
Expected: `ok  	github.com/darkraise/darkpanel/internal/controller/api` twice and `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add internal/controller/api/extensions.go internal/controller/api/extensions_test.go internal/controller/api/server.go
git commit -m "feat(api): mount contributed web routes"
rm -rf /root/.cache/w0c-tmp/41
```

### Task 42: Listener service shares its TLS configuration and sources

`listen.Service` keeps the one `tls.Config` its HTTPS listeners clone and exposes it as `TLSConfig()`, so the runtime listeners (Task 40, wired by Task 47) present the same certificate and share its session ticket keys when they are set. A controller whose network listeners are all plain HTTP (`--listen loopback`, as the server tests run) never issues a leaf, so every runtime-listener handshake would fail with "no web certificate issued yet": `TLSConfig` therefore first issues a leaf when none exists, through the new `Leaf.EnsureIssued`, with the hostname, localhost and the sources' extra names and no IP SANs. It never replaces a current leaf, and the next `Ensure` with addresses reissues with their SANs as today. Issuing in `apply` or lazily in `GetCertificate` instead would change what `TestServiceDefaultsWithoutTailscale` and `TestLeafSANsAndChain` pin, so only `TLSConfig` issues. `listen.Config` gains `Sources *SourceDeps`: when set, the service's leaf asks the certificate sources of Task 36; nil keeps today's behaviour. The tests are appended to `service_test.go` and use its existing `newServiceFixture`; they declare no new helper.

**Files:**
- Modify: `internal/controller/listen/service.go:1-100` (imports, `Config`, `Service`, `Start`, new `TLSConfig`)
- Modify: `internal/controller/listen/leaf.go` (new method `EnsureIssued`, above `extraNames`)
- Test: `internal/controller/listen/service_test.go` (append two tests)

**Interfaces:**
- Consumes: C13's `SourceDeps`, `(*Leaf).useSources` and `(*Leaf).extraNames` (Task 36).
- Produces: C13's `listen.Config.Sources *SourceDeps`, `(*Service).TLSConfig() (*tls.Config, error)` and `(*Leaf).EnsureIssued(ctx context.Context) error`, which Task 47 uses.

**Items:** 52

**Depends on:** Task 36

**Implementer:** dforge:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5

- [ ] **Step 1: Write the failing test**

Append to `internal/controller/listen/service_test.go` (its imports already include `context`, `crypto/tls`, `crypto/x509`, `log/slog` and `reflect`):

```go
func TestServiceSharesOneTLSConfig(t *testing.T) {
	f := newServiceFixture(t)
	specs, err := ParseSpecs([]string{"tailscale"})
	if err != nil {
		t.Fatal(err)
	}
	f.ifs.set(true, "100.64.0.1")
	s, err := Start(context.Background(), Config{
		Specs: specs, Hostname: "panel", DB: f.db, Authority: f.auth, Clock: f.clock,
		Handler: schemeHandler(), Log: slog.New(slog.DiscardHandler), Interfaces: f.ifs.list, Bind: f.binder.bind,
		Sources: &SourceDeps{LocalHostID: "H1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Shutdown(context.Background()) })
	issued := s.leaf.cur.Load()
	cfg, err := s.TLSConfig()
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.TLSConfig()
	if err != nil || cfg == nil || cfg != again || cfg != s.set.cfg.TLS {
		t.Fatal("TLSConfig is not the one configuration the HTTPS listeners clone")
	}
	if s.leaf.cur.Load() != issued {
		t.Error("TLSConfig replaced the leaf the HTTPS listeners issued")
	}
	if cfg.MinVersion != tls.VersionTLS12 {
		t.Errorf("MinVersion = %x, want TLS 1.2", cfg.MinVersion)
	}
	if got, err := cfg.GetCertificate(&tls.ClientHelloInfo{}); err != nil || got != issued.cert {
		t.Fatalf("GetCertificate = %v, %v; want the CA leaf", got, err)
	}
}

// With plain listeners only, nothing issues a leaf until TLSConfig does: a
// leaf without IP SANs, which the next Ensure with addresses replaces.
func TestServiceTLSConfigIssuesALeafForPlainListeners(t *testing.T) {
	f := newServiceFixture(t)
	s, err := f.start(t)
	if err != nil {
		t.Fatal(err)
	}
	if s.leaf.cur.Load() != nil {
		t.Fatal("plain listeners issued a leaf")
	}
	cfg, err := s.TLSConfig()
	if err != nil {
		t.Fatal(err)
	}
	first, err := cfg.GetCertificate(&tls.ClientHelloInfo{ServerName: "panel"})
	if err != nil {
		t.Fatalf("GetCertificate after TLSConfig: %v", err)
	}
	leaf, err := x509.ParseCertificate(first.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(leaf.DNSNames, []string{"panel", "localhost"}) || len(leaf.IPAddresses) != 0 {
		t.Fatalf("leaf SANs %v %v, want the hostname and localhost without IP addresses", leaf.DNSNames, leaf.IPAddresses)
	}
	if _, err := s.TLSConfig(); err != nil || s.leaf.cur.Load().cert != first {
		t.Fatalf("a second TLSConfig = %v or replaced the leaf", err)
	}
	if issued, err := s.leaf.Ensure(context.Background(), addrs("100.64.0.1")); err != nil || !issued {
		t.Fatalf("Ensure with an address = %v, %v; want a new leaf with its SAN", issued, err)
	}
	if got, err := cfg.GetCertificate(&tls.ClientHelloInfo{}); err != nil || got == first {
		t.Fatalf("GetCertificate = %v, %v; want the reissued leaf through the same configuration", got, err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `mkdir -p /root/.cache/w0c-tmp/42 && TMPDIR=/root/.cache/w0c-tmp/42 GOTOOLCHAIN=local go test -count=1 -run 'TestServiceSharesOneTLSConfig|TestServiceTLSConfigIssuesALeafForPlainListeners' ./internal/controller/listen/`
Expected: FAIL to build: `unknown field Sources in struct literal of type Config` and `s.TLSConfig undefined`.

- [ ] **Step 3: Write the implementation**

In `internal/controller/listen/leaf.go`, add directly above `// extraNames reads the sources' extra names`:

```go
// EnsureIssued issues a leaf without IP SANs when none exists yet, so a
// controller whose network listeners are all plain HTTP still has a
// certificate for its runtime listeners. It never replaces a current leaf;
// the next Ensure with addresses issues one with their SANs.
func (l *Leaf) EnsureIssued(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.cur.Load() != nil {
		return nil
	}
	names := l.extraNames(ctx, nil)
	now := l.clock.Now()
	cert, err := l.auth.issue(l.hostname, names, nil, now)
	if err != nil {
		return err
	}
	l.cur.Store(&issuedLeaf{cert: cert, ips: []netip.Addr{}, names: names, at: now})
	return nil
}

```

In `internal/controller/listen/service.go`, add `"crypto/tls"` to the import block after `"context"`.

Add the field at the end of `Config`, after `OnServeError`:

```go
	// Sources are the certificate sources' dependencies; nil means no
	// source and no extra name: every handshake gets the Darkpanel CA leaf.
	Sources *SourceDeps
```

Add the field to `Service`, after `leaf`:

```go
	tls   *tls.Config // the one configuration every HTTPS listener clones
```

In `Start`, replace

```go
	s := &Service{cfg: cfg, leaf: NewLeaf(cfg.Authority, cfg.Clock, cfg.Hostname)}
	s.set = NewSet(SetConfig{Handler: cfg.Handler, TLS: s.leaf.TLSConfig(), Bind: cfg.Bind, Log: cfg.Log, OnServeError: cfg.OnServeError})
```

with

```go
	s := &Service{cfg: cfg, leaf: NewLeaf(cfg.Authority, cfg.Clock, cfg.Hostname)}
	if cfg.Sources != nil {
		s.leaf.useSources(*cfg.Sources)
	}
	s.tls = s.leaf.TLSConfig()
	s.set = NewSet(SetConfig{Handler: cfg.Handler, TLS: s.tls, Bind: cfg.Bind, Log: cfg.Log, OnServeError: cfg.OnServeError})
```

Add, directly above `// State returns the current listener state.`:

```go
// TLSConfig is the configuration the HTTPS listeners serve with. Other
// listeners that serve the web certificate, such as the runtime listeners,
// clone it, so every listener presents the same leaf and shares its session
// ticket keys when they are set. It first issues the leaf when none exists
// yet: with only plain listeners nothing else issues one.
func (s *Service) TLSConfig() (*tls.Config, error) {
	ctx, cancel := context.WithTimeout(context.Background(), CheckInterval)
	defer cancel()
	if err := s.leaf.EnsureIssued(ctx); err != nil {
		return nil, err
	}
	return s.tls, nil
}

```

- [ ] **Step 4: Run test to verify it passes**

Run: `TMPDIR=/root/.cache/w0c-tmp/42 GOTOOLCHAIN=local go test -count=1 -race ./internal/controller/listen/ && TMPDIR=/root/.cache/w0c-tmp/42 golangci-lint run --allow-serial-runners ./internal/controller/listen/...`
Expected: `ok  	github.com/darkraise/darkpanel/internal/controller/listen` and `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add internal/controller/listen/service.go internal/controller/listen/leaf.go internal/controller/listen/service_test.go
git commit -m "feat(listen): share the web TLS configuration"
rm -rf /root/.cache/w0c-tmp/42
```

### Task 43: Change page with effects and resolution

The change page of 1.3's web UI at `/changes/:changeId` (C24): the change's state, class, host, actor, times, owner, parent, repair links and its error with the `cause` code (the stored `core.needs_repair` error names the code that led there); a composite's steps in ordinal order with links to their child changes; and the change's effects. Every `unresolved` effect gets a "Preview effect <seq>" button that opens a dialog which first reads `GET /api/v1/changes/{id}/effects/{seq}/observe` (read-only) and shows the effect's before, intended and current state, and only then offers the resolutions `allowed` names (`null` means both): "Accept as it is" and "Reconcile"; when several objects match a creation, Reconcile waits until the operator picks one, which is sent as `resource`. Change events invalidate `["change", id]` but not the effects, so the effects query key appends the change's `updatedAt` and `state` to `effectsQueryKey(id)`: it refetches whenever the change moved on, and invalidating `effectsQueryKey(id)` still matches it as a prefix. The audit log links each entry that has a change id to the page, and `app.tsx` mounts the route inside the session guard. Undo, Repair and Release are Task 45. Test helpers carry the prefix `cpg`.

**Files:**
- Create: `web/src/pages/change.tsx`
- Modify: `web/src/app.tsx` (imports; the route list inside the session guard)
- Modify: `web/src/pages/audit.tsx` (imports; the action cell of `AuditBody`)
- Test: `web/src/pages/change.test.tsx`
- Test: `web/src/pages/audit.test.tsx` (the `pages` fixture and the first test)
- Test: `web/src/app.test.tsx` (`stubServer`'s routes and the `test.each` table)

**Interfaces:**
- Consumes: C21 (`getChange`, `listEffects` returning `Effect[]`, `observeEffect`, `resolveEffect`, `listSteps` returning `Step[]`, `changeQueryKey`, `effectsQueryKey`, `stepsQueryKey`, `errorMessage`; the types `Change`, `Effect`, `StateDoc`, `ObserveResult` with `allowed: string[] | null`, `Observation` with `candidates: Candidate[] | null`, `Step`).
- Produces: C24 `ChangePage` at `/changes/:changeId`; Task 45 inserts its actions into `change.tsx`.

**Items:** 95

**Depends on:** Task 11, Task 33

**Implementer:** dforge:impl-sonnet-medium
**Evaluation:** files 2 - spec 0 - coupling 1 - risk 1 = 4

- [ ] **Step 1: Write the failing tests**

Create `web/src/pages/change.test.tsx`:

```tsx
import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { Route, Routes } from "react-router"
import { expect, test } from "vitest"
import type { Change, Effect, ObserveResult, Step } from "@/api/types"
import { renderWithProviders, stubFetch, type StubHandler } from "@/test/utils"
import { ChangePage } from "./change"

function cpgChange(over: Partial<Change> = {}): Change {
  return {
    id: "c1", hostId: "h1", action: "cdx.rule.set", version: 1, class: "compensable", state: "needs_repair", actor: "user:u1",
    createdAt: "2026-10-23T08:00:00Z", updatedAt: "2026-10-23T08:05:00Z", revertDeadline: null, output: null,
    error: { code: "core.needs_repair", message: "the provider's answer was lost", cause: "cloud.uncertain_outcome" },
    compensatedBy: null, parentChangeId: null, owner: null, repairedBy: null, repairOutcome: null, override: null, ...over,
  }
}

function cpgEffect(over: Partial<Effect> = {}): Effect {
  return {
    changeId: "c1", seq: 1, kind: "create", resource: "cdx.key:?darkpanel-c1", local: false, inverseLocal: false,
    commitBy: "2026-10-23T08:10:00Z", state: "unresolved", reason: "uncertain", before: { state: null }, after: { state: { tags: ["a"] } },
    post: null, undoTo: null, undoResource: "", current: null, resolution: "", resolvedBy: "", resolvedAt: null, ...over,
  }
}

function cpgObserve(over: Partial<ObserveResult["observation"]> = {}, allowed: string[] | null = null): ObserveResult {
  return {
    effect: cpgEffect(),
    observation: { current: { state: { tags: ["a"] } }, resource: "cdx.key:k1", matches: 1, candidates: null, exclusive: true, ...over },
    allowed,
  }
}

function cpgRender(routes: Record<string, StubHandler>, path = "/changes/c1") {
  const calls = stubFetch(routes)
  renderWithProviders(
    <Routes>
      <Route path="/changes/:changeId" element={<ChangePage />} />
    </Routes>,
    { path },
  )
  return calls
}

test("shows the change with its error, cause and links", async () => {
  cpgRender({
    "GET /api/v1/changes/c1": { body: cpgChange({ parentChangeId: "p9", repairedBy: "r7" }) },
    "GET /api/v1/changes/c1/effects": { body: { effects: [] } },
  })
  expect((await screen.findByRole("heading", { level: 1, name: "cdx.rule.set/v1" })).textContent).toBe("cdx.rule.set/v1")
  expect(screen.getByText("needs_repair").textContent).toBe("needs_repair")
  expect(screen.getByRole("alert").textContent).toBe("core.needs_repair: the provider's answer was lostCause: cloud.uncertain_outcome")
  expect(screen.getByRole("link", { name: "p9" }).getAttribute("href")).toBe("/changes/p9")
  expect(screen.getByRole("link", { name: "r7" }).getAttribute("href")).toBe("/changes/r7")
  expect((await screen.findByText("This change recorded no effects.")).textContent).toBe("This change recorded no effects.")
})

test("a composite lists its steps in order with links to their changes", async () => {
  const steps: Step[] = [
    { id: "ports", ordinal: 2, host: "h1", action: { Name: "cdx.ports.open", Version: 1 }, state: "pending", attempt: 0, childChangeId: "", earlierChildChangeIds: null, childDeadline: null, error: null },
    { id: "network", ordinal: 1, host: "h1", action: { Name: "cdx.net.create", Version: 1 }, state: "applied", attempt: 1, childChangeId: "k1", earlierChildChangeIds: null, childDeadline: null, error: null },
  ]
  cpgRender({
    "GET /api/v1/changes/c1": { body: cpgChange({ class: "composite", error: null, state: "applying" }) },
    "GET /api/v1/changes/c1/steps": { body: { steps } },
    "GET /api/v1/changes/c1/effects": { body: { effects: [] } },
  })
  const section = await screen.findByRole("region", { name: "Steps" })
  const rows = await within(section).findAllByRole("row")
  expect(rows.slice(1).map((r) => within(r).getAllByRole("cell")[1]?.textContent)).toEqual(["network", "ports"])
  expect(within(section).getByRole("link", { name: "k1" }).getAttribute("href")).toBe("/changes/k1")
})

test("an unresolved effect is previewed before it is accepted", async () => {
  const calls = cpgRender({
    "GET /api/v1/changes/c1": { body: cpgChange() },
    "GET /api/v1/changes/c1/effects": () => ({
      body: { effects: [calls.some((c) => c.path.endsWith("/resolve")) ? cpgEffect({ state: "accepted", resolution: "accept", resolvedBy: "user:u1" }) : cpgEffect()] },
    }),
    "GET /api/v1/changes/c1/effects/1/observe": { body: cpgObserve() },
    "POST /api/v1/changes/c1/effects/1/resolve": { body: cpgEffect({ state: "accepted", resolution: "accept" }) },
  })
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: "Preview effect 1" }))
  const dialog = await screen.findByRole("dialog", { name: "Resolve effect 1" })
  expect((await within(dialog).findByLabelText("Now")).textContent).toBe('{\n  "tags": [\n    "a"\n  ]\n}')
  expect(within(dialog).getByLabelText("Before").textContent).toBe("null")
  await user.click(within(dialog).getByRole("button", { name: "Accept as it is" }))
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect(calls.find((c) => c.path.endsWith("/resolve"))?.body).toEqual({ resolution: "accept" })
  expect((await screen.findByText("accept by user:u1")).textContent).toBe("accept by user:u1")
})

test("several matching objects need a choice before reconcile, and allowed limits the resolutions", async () => {
  const calls = cpgRender({
    "GET /api/v1/changes/c1": { body: cpgChange() },
    "GET /api/v1/changes/c1/effects": { body: { effects: [cpgEffect()] } },
    "GET /api/v1/changes/c1/effects/1/observe": {
      body: cpgObserve(
        {
          matches: 2, resource: "", exclusive: false,
          candidates: [{ resource: "cdx.key:k1", state: { state: {} } }, { resource: "cdx.key:k2", state: { state: {} } }],
        },
        ["reconcile"],
      ),
    },
    "POST /api/v1/changes/c1/effects/1/resolve": { body: cpgEffect({ state: "made", resolution: "reconcile" }) },
  })
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: "Preview effect 1" }))
  const dialog = await screen.findByRole("dialog", { name: "Resolve effect 1" })
  const reconcile = await within(dialog).findByRole("button", { name: "Reconcile" })
  expect(within(dialog).queryByRole("button", { name: "Accept as it is" })).toBeNull()
  expect(reconcile.hasAttribute("disabled")).toBe(true)
  await user.click(within(dialog).getByRole("radio", { name: "cdx.key:k2" }))
  await user.click(reconcile)
  await waitFor(() => expect(calls.find((c) => c.path.endsWith("/resolve"))?.body).toEqual({ resolution: "reconcile", resource: "cdx.key:k2" }))
})

test("a refused resolution keeps the dialog open with the reason", async () => {
  cpgRender({
    "GET /api/v1/changes/c1": { body: cpgChange() },
    "GET /api/v1/changes/c1/effects": { body: { effects: [cpgEffect()] } },
    "GET /api/v1/changes/c1/effects/1/observe": { body: cpgObserve() },
    "POST /api/v1/changes/c1/effects/1/resolve": {
      status: 409,
      body: { type: "about:blank", title: "Change busy", status: 409, detail: "resolve after 08:10", code: "core.change_busy" },
    },
  })
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: "Preview effect 1" }))
  const dialog = await screen.findByRole("dialog", { name: "Resolve effect 1" })
  await user.click(await within(dialog).findByRole("button", { name: "Accept as it is" }))
  expect((await within(dialog).findByRole("alert")).textContent).toBe("resolve after 08:10")
})

test("an unknown change shows the API's error", async () => {
  cpgRender({
    "GET /api/v1/changes/zz": {
      status: 404,
      body: { type: "about:blank", title: "Change not found", status: 404, detail: "no change zz", code: "change.not_found" },
    },
  }, "/changes/zz")
  expect((await screen.findByRole("alert")).textContent).toBe("no change zz")
})
```

In `web/src/pages/audit.test.tsx`, give the first page's entry a change id: in `pages`, replace

```tsx
    ? { body: { output: { entries: [entry("a2", "core.token.create", { hostId: "h1" })], nextBefore: "a2" } } }
```

with

```tsx
    ? { body: { output: { entries: [entry("a2", "core.token.create", { hostId: "h1", changeId: "c2" })], nextBefore: "a2" } } }
```

and in the test "lists entries newest first and loads older ones by nextBefore", after `expect(screen.getByText("h1").textContent).toBe("h1")`, add

```tsx
  expect(screen.getByRole("link", { name: "core.token.create/v1" }).getAttribute("href")).toBe("/changes/c2")
```

In `web/src/app.test.tsx`, add these two routes to the object `stubServer` passes to `stubFetch`:

```tsx
    "GET /api/v1/changes/c1": {
      body: {
        id: "c1", hostId: null, action: "core.token.create", version: 1, class: "compensable", state: "completed", actor: "user:u1",
        createdAt: "2026-10-19T08:00:00Z", updatedAt: "2026-10-19T08:00:00Z", revertDeadline: null, output: null, error: null,
        compensatedBy: "core.token.revoke", parentChangeId: null, owner: null, repairedBy: null, repairOutcome: null, override: null,
      },
    },
    "GET /api/v1/changes/c1/effects": { body: { effects: [] } },
```

and add this row to the `test.each` table of the "%s shows %s" test:

```tsx
    ["/changes/c1", "core.token.create/v1"],
```


- [ ] **Step 2: Run the tests to verify they fail**

Run: `mkdir -p /root/.cache/w0c-tmp/43 && cd web && TMPDIR=/root/.cache/w0c-tmp/43 pnpm exec vitest run src/pages/change.test.tsx src/pages/audit.test.tsx src/app.test.tsx`
Expected: FAIL — `change.test.tsx` cannot resolve `"./change"`, the audit test finds no link named `core.token.create/v1`, and the `/changes/c1` row shows "Page not found" instead of the heading.

- [ ] **Step 3: Write the implementation**

Create `web/src/pages/change.tsx`:

```tsx
import { useState, type ReactNode } from "react"
import { Link, useParams } from "react-router"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { PageHeader } from "darkraise-ui/layout"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Badge, type BadgeVariant } from "darkraise-ui/components/badge"
import { Button } from "darkraise-ui/components/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "darkraise-ui/components/dialog"
import { Label } from "darkraise-ui/components/label"
import { RadioGroup, RadioGroupItem } from "darkraise-ui/components/radio-group"
import { Spinner } from "darkraise-ui/components/spinner"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "darkraise-ui/components/table"
import {
  changeQueryKey,
  effectsQueryKey,
  errorMessage,
  getChange,
  listEffects,
  listSteps,
  observeEffect,
  resolveEffect,
  stepsQueryKey,
} from "@/api/client"
import type { Change, Effect, StateDoc } from "@/api/types"
import { formatTime } from "@/lib/format"

function stateVariant(state: string): BadgeVariant {
  switch (state) {
    case "completed":
    case "confirmed":
    case "made":
    case "undone":
    case "accepted":
      return "default"
    case "failed":
    case "needs_repair":
    case "interrupted":
    case "unresolved":
      return "destructive"
    default:
      return "secondary"
  }
}

function ChangeLink({ id }: { id: string }) {
  return (
    <Link to={`/changes/${encodeURIComponent(id)}`} className="font-mono underline-offset-4 hover:underline">
      {id}
    </Link>
  )
}

function Doc({ label, doc }: { label: string; doc: StateDoc | null }) {
  return (
    <div className="flex flex-col gap-1">
      <span className="text-sm font-medium">{label}</span>
      <pre aria-label={label} className="overflow-x-auto rounded-md bg-muted p-2 font-mono text-xs">
        {doc === null ? "—" : JSON.stringify(doc.state, null, 2)}
      </pre>
    </div>
  )
}

function Summary({ change }: { change: Change }) {
  const rows: Array<[string, ReactNode]> = [
    ["State", <Badge variant={stateVariant(change.state)}>{change.state}</Badge>],
    ["Class", change.class],
    ["Host", change.hostId ?? "The controller"],
    ["Actor", <span className="font-mono">{change.actor}</span>],
    ["Created", formatTime(change.createdAt)],
    ["Updated", formatTime(change.updatedAt)],
  ]
  if (change.revertDeadline !== null) rows.push(["Revert deadline", formatTime(change.revertDeadline)])
  if (change.owner !== null) rows.push(["Being worked on by", change.owner])
  if (change.parentChangeId !== null) rows.push(["Step of", <ChangeLink id={change.parentChangeId} />])
  if (change.repairedBy !== null) rows.push(["Repaired by", <ChangeLink id={change.repairedBy} />])
  if (change.repairOutcome !== null) rows.push(["Repair outcome", change.repairOutcome])
  if (change.compensatedBy !== null) rows.push(["Compensated by", <span className="font-mono">{change.compensatedBy}</span>])
  return (
    <div className="flex flex-col gap-4">
      <dl className="grid grid-cols-[max-content_1fr] gap-x-6 gap-y-2 text-sm">
        {rows.map(([term, value]) => (
          <div key={term} className="contents">
            <dt className="text-muted-foreground">{term}</dt>
            <dd>{value}</dd>
          </div>
        ))}
      </dl>
      {change.error !== null && (
        <Alert variant="destructive" role="alert">
          <AlertDescription>
            <p>
              <span className="font-mono">{change.error.code}</span>
              {`: ${change.error.message}`}
            </p>
            {change.error.cause !== undefined && change.error.cause !== "" && (
              <p>
                {"Cause: "}
                <span className="font-mono">{change.error.cause}</span>
              </p>
            )}
          </AlertDescription>
        </Alert>
      )}
    </div>
  )
}

function Steps({ changeId }: { changeId: string }) {
  const steps = useQuery({ queryKey: stepsQueryKey(changeId), queryFn: () => listSteps(changeId) })
  if (steps.isPending) return <Spinner aria-label="Loading the steps" />
  if (steps.isError) {
    return (
      <Alert variant="destructive" role="alert">
        <AlertDescription>{errorMessage(steps.error)}</AlertDescription>
      </Alert>
    )
  }
  return (
    <section aria-label="Steps" className="flex flex-col gap-2">
      <h2 className="text-lg font-semibold">Steps</h2>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>#</TableHead>
            <TableHead>Step</TableHead>
            <TableHead>Action</TableHead>
            <TableHead>State</TableHead>
            <TableHead>Change</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {[...steps.data].sort((a, b) => a.ordinal - b.ordinal).map((s) => (
            <TableRow key={s.id}>
              <TableCell>{s.ordinal}</TableCell>
              <TableCell className="font-mono">{s.id}</TableCell>
              <TableCell className="font-mono">{`${s.action.Name}/v${s.action.Version}`}</TableCell>
              <TableCell>
                <Badge variant={stateVariant(s.state)} size="sm">
                  {s.state}
                </Badge>
                {s.error !== null && <p className="text-xs text-destructive">{s.error.message}</p>}
              </TableCell>
              <TableCell>{s.childChangeId === "" ? "—" : <ChangeLink id={s.childChangeId} />}</TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </section>
  )
}

// ResolveDialog shows what an unresolved effect's resource looks like now,
// read without changing anything, and only then offers the resolutions the
// module allows.
function ResolveDialog({ effect, onClose }: { effect: Effect; onClose: () => void }) {
  const client = useQueryClient()
  const observed = useQuery({
    queryKey: ["observe", effect.changeId, effect.seq],
    queryFn: () => observeEffect(effect.changeId, effect.seq),
    staleTime: 0,
    gcTime: 0,
  })
  const [resource, setResource] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function resolve(resolution: "accept" | "reconcile") {
    setBusy(true)
    setError(null)
    try {
      await resolveEffect(effect.changeId, effect.seq, resource === "" ? { resolution } : { resolution, resource })
      void client.invalidateQueries({ queryKey: effectsQueryKey(effect.changeId) })
      void client.invalidateQueries({ queryKey: changeQueryKey(effect.changeId) })
      onClose()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  const obs = observed.data?.observation
  const allowed = observed.data?.allowed ?? ["accept", "reconcile"]
  const candidates = obs?.candidates ?? []
  const needsChoice = candidates.length > 1 && resource === ""
  return (
    <Dialog open onOpenChange={(next) => (next || busy ? undefined : onClose())}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{`Resolve effect ${effect.seq}`}</DialogTitle>
          <DialogDescription>{`${effect.kind} ${effect.resource}`}</DialogDescription>
        </DialogHeader>
        {observed.isPending ? (
          <Spinner aria-label="Observing the resource" />
        ) : observed.isError ? (
          <Alert variant="destructive" role="alert">
            <AlertDescription>{errorMessage(observed.error)}</AlertDescription>
          </Alert>
        ) : (
          <div className="flex flex-col gap-3">
            <Doc label="Before" doc={effect.before} />
            <Doc label="Intended" doc={effect.after} />
            <Doc label="Now" doc={obs?.current ?? null} />
            {candidates.length > 1 && (
              <fieldset className="flex flex-col gap-2">
                <legend className="text-sm font-medium">{`${obs?.matches ?? candidates.length} objects match; pick the one this change made`}</legend>
                <RadioGroup value={resource} onValueChange={setResource} aria-label="Matching objects">
                  {candidates.map((c) => (
                    <div key={c.resource} className="flex items-center gap-2">
                      <RadioGroupItem id={`candidate-${c.resource}`} value={c.resource} />
                      <Label htmlFor={`candidate-${c.resource}`} className="font-mono">
                        {c.resource}
                      </Label>
                    </div>
                  ))}
                </RadioGroup>
              </fieldset>
            )}
          </div>
        )}
        {error !== null && (
          <Alert variant="destructive" role="alert">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}
        <DialogFooter>
          {allowed.includes("accept") && (
            <Button variant="outline" disabled={observed.data === undefined || busy} onClick={() => void resolve("accept")}>
              Accept as it is
            </Button>
          )}
          {allowed.includes("reconcile") && (
            <Button disabled={observed.data === undefined || busy || needsChoice} onClick={() => void resolve("reconcile")}>
              Reconcile
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function Effects({ change }: { change: Change }) {
  // Change events refetch the change, not its effects; keying the effects
  // on the change's last update refetches them whenever the change moved on.
  const effects = useQuery({
    queryKey: [...effectsQueryKey(change.id), change.updatedAt, change.state],
    queryFn: () => listEffects(change.id),
  })
  const [resolving, setResolving] = useState<Effect | null>(null)

  if (effects.isPending) return <Spinner aria-label="Loading the effects" />
  if (effects.isError) {
    return (
      <Alert variant="destructive" role="alert">
        <AlertDescription>{errorMessage(effects.error)}</AlertDescription>
      </Alert>
    )
  }
  return (
    <section aria-label="Effects" className="flex flex-col gap-2">
      <h2 className="text-lg font-semibold">Effects</h2>
      {effects.data.length === 0 ? (
        <p className="text-sm text-muted-foreground">This change recorded no effects.</p>
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>#</TableHead>
              <TableHead>Kind</TableHead>
              <TableHead>Resource</TableHead>
              <TableHead>State</TableHead>
              <TableHead>Resolution</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {effects.data.map((e) => (
              <TableRow key={`${e.changeId}-${e.seq}`}>
                <TableCell>{e.seq}</TableCell>
                <TableCell>{e.kind}</TableCell>
                <TableCell className="font-mono">{e.resource}</TableCell>
                <TableCell>
                  <Badge variant={stateVariant(e.state)} size="sm">
                    {e.reason === "" ? e.state : `${e.state}: ${e.reason}`}
                  </Badge>
                </TableCell>
                <TableCell>
                  {e.state === "unresolved" ? (
                    <Button size="sm" variant="outline" onClick={() => setResolving(e)}>
                      {`Preview effect ${e.seq}`}
                    </Button>
                  ) : e.resolution === "" ? (
                    "—"
                  ) : (
                    `${e.resolution} by ${e.resolvedBy}`
                  )}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
      {resolving !== null && <ResolveDialog effect={resolving} onClose={() => setResolving(null)} />}
    </section>
  )
}

// ChangePage shows one change: its state and error, a composite's steps,
// and its effects, each unresolved one with a read-only preview before it
// can be resolved.
export function ChangePage() {
  const { changeId = "" } = useParams()
  const change = useQuery({ queryKey: changeQueryKey(changeId), queryFn: () => getChange(changeId) })

  if (change.isPending) return <Spinner aria-label="Loading the change" />
  if (change.isError) {
    return (
      <Alert variant="destructive" role="alert">
        <AlertDescription>{errorMessage(change.error)}</AlertDescription>
      </Alert>
    )
  }
  const c = change.data
  return (
    <>
      <PageHeader
        breadcrumbs={[{ label: "Audit log", href: "/audit" }, { label: c.id }]}
        title={`${c.action}/v${c.version}`}
        description={`Change ${c.id}`}
      />
      <div className="flex flex-col gap-6">
        <Summary change={c} />
        {c.class === "composite" && <Steps changeId={c.id} />}
        <Effects change={c} />
      </div>
    </>
  )
}
```

In `web/src/pages/audit.tsx`, add the first import line

```tsx
import { Link } from "react-router"
```

and in `AuditBody`'s action cell replace

```tsx
                <span className="font-mono">{`${e.action}/v${e.version}`}</span>{" "}
```

with

```tsx
                {e.changeId === null ? (
                  <span className="font-mono">{`${e.action}/v${e.version}`}</span>
                ) : (
                  <Link to={`/changes/${encodeURIComponent(e.changeId)}`} className="font-mono underline-offset-4 hover:underline">
                    {`${e.action}/v${e.version}`}
                  </Link>
                )}{" "}
```


In `web/src/app.tsx`, add after the `AuditPage` import

```tsx
import { ChangePage } from "@/pages/change"
```

and after the line `<Route path="audit" element={<AuditPage />} />`

```tsx
          <Route path="changes/:changeId" element={<ChangePage />} />
```


- [ ] **Step 4: Run the tests to verify they pass**

Run: `mkdir -p /root/.cache/w0c-tmp/43 && cd web && TMPDIR=/root/.cache/w0c-tmp/43 pnpm exec vitest run src/pages/change.test.tsx src/pages/audit.test.tsx src/app.test.tsx`
Expected: PASS: 6 tests in `change.test.tsx`, and every test of `audit.test.tsx` and `app.test.tsx`.

- [ ] **Step 5: Check the whole web suite, types and lint**

Run: `TMPDIR=/root/.cache/w0c-tmp/43 make lint-web test-web`
Expected: ESLint prints nothing (it runs with `--max-warnings 0`), `tsc --noEmit` prints nothing, and the Vitest summary reports every test file passed.

- [ ] **Step 6: Commit**

```bash
git add web/src/pages/change.tsx web/src/pages/change.test.tsx web/src/pages/audit.tsx web/src/pages/audit.test.tsx web/src/app.tsx web/src/app.test.tsx
git commit -m "feat(web): add the change page with effects"
rm -rf /root/.cache/w0c-tmp/43
```

### Task 44: Socket extension routes and the OpenAPI document

This task completes C18: the `SurfaceSocket` routes of `api.Config.Extensions` join the controller socket's mux, behind its peer-uid check (root, or the service's own uid with `--dev`), and the served OpenAPI document lists the web routes. Today `SocketHandler` builds a fresh mux on every call; it now returns one handler `New` builds once (`buildSocketHandler`), so a socket route that clashes with a built-in socket route or another contribution fails `New` with an error naming the pattern, like a web clash does. A socket `Stream` route gets Task 41's lifecycle without the re-authentication: its peer check holds for the connection's life, and it still ends at `SSEHardLimit` and `CloseStreams`. `OpenAPIRoutes` returns `Routes()` followed by the web routes only — the document describes the network API, and `checkOpenAPI` requires every document operation to be served by a network route — and `handleOpenAPI` serves it. Nothing else depends on `Routes()`: `make schemas` (`internal/core/schema/schemagen`) never reads it, and there is no committed OpenAPI snapshot. Test helpers are Task 41's `xr` helpers and the existing `setUID` and `socketRequest` of `socket_test.go`.

**Files:**
- Modify: `internal/controller/api/socket.go:193-207` (`SocketHandler`)
- Modify: `internal/controller/api/server.go` (`Server`, `New`)
- Modify: `internal/controller/api/meta.go:53-62` (`handleOpenAPI`)
- Modify: `internal/controller/api/extensions.go` (imports, two new functions)
- Test: `internal/controller/api/extensions_test.go` (append)

**Interfaces:**
- Consumes: Task 41's `checkExtension`, `mount` and `(*Server).extensionStream`; C2 (`ext.SurfaceSocket`).
- Produces: C18's socket mounting and `func (s *Server) OpenAPIRoutes() []schema.Route` (Routes, then the `SurfaceWeb` extension routes).

**Items:** 52

**Depends on:** Task 41

**Implementer:** dforge:impl-sonnet-high
**Evaluation:** files 2 - spec 0 - coupling 1 - risk 2 = 5

- [ ] **Step 1: Write the failing test**

Append to `internal/controller/api/extensions_test.go`:

```go

func TestExtensionSocketRoute(t *testing.T) {
	h := xrHarness(t, ext.Route{Surface: ext.SurfaceSocket, Method: http.MethodGet, Path: "/api/v1/controller/xr",
		Summary: "Socket only", Handler: xrHandler})
	setUID(t, 1000)
	rec := h.socketRequest(request("GET", "/api/v1/controller/xr", ""), 0)
	if rec.Code != http.StatusOK || rec.Body.String() != "local:root@"+testHostID {
		t.Errorf("root on the socket: %d %q", rec.Code, rec.Body.String())
	}
	if rec := h.socketRequest(request("GET", "/api/v1/controller/xr", ""), 1000); rec.Code != http.StatusForbidden {
		t.Errorf("uid 1000 outside dev mode: %d %s", rec.Code, rec.Body.String())
	}
	if rec := h.serve(withToken(request("GET", "/api/v1/controller/xr", ""), h.token(core.ScopeAdmin))); rec.Code != http.StatusNotFound {
		t.Errorf("the socket route on the network: %d %s", rec.Code, rec.Body.String())
	}
	if rec := h.socketRequest(request("GET", "/healthz", ""), 0); rec.Code != http.StatusOK {
		t.Errorf("the socket's own routes: /healthz %d %s", rec.Code, rec.Body.String())
	}
}

func TestExtensionSocketRouteRefused(t *testing.T) {
	socket := func(method, path string) ext.Route {
		return ext.Route{Surface: ext.SurfaceSocket, Method: method, Path: path, Handler: xrHandler}
	}
	tests := []struct {
		name   string
		routes []ext.Route
		want   string
	}{
		{"clash with the web CA route", []ext.Route{socket("GET", "/api/v1/controller/web-ca")}, "GET /api/v1/controller/web-ca"},
		{"clash with the break-glass actions", []ext.Route{socket("POST", "/api/v1/actions/{n}/{v}/plan")}, "POST /api/v1/actions/{n}/{v}/plan"},
		{"the same route twice", []ext.Route{socket("POST", "/api/v1/controller/xr"), socket("POST", "/api/v1/controller/xr")},
			"POST /api/v1/controller/xr"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			h.cfg.Extensions = tt.routes
			if _, err := New(h.cfg); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("New: %v, want an error containing %q", err, tt.want)
			}
		})
	}
}

func TestOpenAPIRoutesIncludeExtensions(t *testing.T) {
	h := xrHarness(t,
		ext.Route{Surface: ext.SurfaceWeb, Method: "GET", Path: "/api/v1/xr/items", Summary: "List items", Scope: core.ScopeRead, Handler: xrHandler},
		ext.Route{Surface: ext.SurfaceSocket, Method: "POST", Path: "/api/v1/controller/xr", Summary: "Socket only", Handler: xrHandler},
	)
	routes := h.srv.OpenAPIRoutes()
	if len(routes) != len(Routes())+1 || routes[len(routes)-1].Path != "/api/v1/xr/items" || routes[len(routes)-1].Summary != "List items" {
		t.Errorf("OpenAPIRoutes ends with %+v, want Routes and the web route", routes[len(routes)-1])
	}
	rec := h.serve(withToken(request("GET", "/api/v1/openapi.json", ""), h.token(core.ScopeRead)))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"/api/v1/xr/items"`) ||
		strings.Contains(rec.Body.String(), "/api/v1/controller/xr") {
		t.Errorf("document %d lists the web route %v and the socket route %v", rec.Code,
			strings.Contains(rec.Body.String(), `"/api/v1/xr/items"`), strings.Contains(rec.Body.String(), "/api/v1/controller/xr"))
	}
}

// TestExtensionSocketStreamEnds: a socket stream route ends at the hard
// limit and at CloseStreams; the peer check needs no re-authentication.
func TestExtensionSocketStreamEnds(t *testing.T) {
	for _, tc := range []struct {
		name string
		end  func(h *harness)
	}{
		{"hard limit", func(h *harness) { h.clock.Advance(SSEHardLimit) }},
		{"CloseStreams", func(h *harness) { h.srv.CloseStreams() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := xrHarness(t, ext.Route{Surface: ext.SurfaceSocket, Method: "GET", Path: "/api/v1/controller/xr/follow",
				Stream: true, Handler: xrWaiter})
			base := h.clock.Pending()
			done := make(chan struct{})
			go func() {
				h.socketRequest(request("GET", "/api/v1/controller/xr/follow", ""), 0)
				close(done)
			}()
			sseWait(t, "the stream's hard limit", func() bool { return h.clock.Pending() == base+1 })
			for range 3 {
				h.clock.Advance(SSERecheck)
			}
			select {
			case <-done:
				t.Fatal("the socket stream ended before its limit")
			case <-time.After(20 * time.Millisecond):
			}
			tc.end(h)
			xrEnded(t, done, func() {})
		})
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mkdir -p /root/.cache/w0c-tmp/44 && TMPDIR=/root/.cache/w0c-tmp/44 GOTOOLCHAIN=local go test -count=1 -run 'TestExtensionSocket|TestOpenAPIRoutes' ./internal/controller/api/`
Expected: FAIL to build with `h.srv.OpenAPIRoutes undefined (type *Server has no field or method OpenAPIRoutes)`

- [ ] **Step 3: Write the implementation**

In `internal/controller/api/extensions.go`, add `"github.com/darkraise/darkpanel/internal/core/schema"` to the imports after the `ext` import; insert directly before the comment `// extensionStream is rt's handler.`:

```go
// mountSocketExtensions mounts the socket surface's contributed routes on
// mux, where the socket's peer check is their authentication.
func (s *Server) mountSocketExtensions(mux *http.ServeMux) error {
	for _, rt := range s.cfg.Extensions {
		if rt.Surface != ext.SurfaceSocket {
			continue
		}
		if err := mount(mux, rt.Method+" "+rt.Path, s.extensionStream(rt)); err != nil {
			return err
		}
	}
	return nil
}

```

and append at the end of the file:

```go

// OpenAPIRoutes returns Routes followed by the web surface's contributed
// routes, which the served OpenAPI document lists. The socket's routes are
// not part of the network API the document describes.
func (s *Server) OpenAPIRoutes() []schema.Route {
	out := Routes()
	for _, rt := range s.cfg.Extensions {
		if rt.Surface == ext.SurfaceWeb {
			out = append(out, schema.Route{Method: rt.Method, Path: rt.Path, Summary: rt.Summary})
		}
	}
	return out
}
```

In `internal/controller/api/socket.go`, replace `SocketHandler` and its comment by:

```go
// SocketHandler serves the controller socket's own mux (spec section 14):
// GET /healthz with full detail, GET /api/v1/controller/web-ca, plan,
// apply and read of break-glass actions, and the socket surface's
// contributed routes (spec 1.12). No session, token or CSRF applies; the
// peer uid is the authentication, and a request without an allowed one is
// refused.
func (s *Server) SocketHandler() http.Handler { return s.socket }

// buildSocketHandler builds SocketHandler's answer; it fails when a
// contributed socket route clashes with another.
func (s *Server) buildSocketHandler() (http.Handler, error) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+healthzPath, s.writeHealthDetail)
	mux.HandleFunc("GET /api/v1/controller/web-ca", s.handleWebCA)
	for _, op := range []string{opPlan, opApply, opRead} {
		mux.HandleFunc("POST /api/v1/actions/{name}/{version}/"+op, s.socketAction(op))
	}
	if err := s.mountSocketExtensions(mux); err != nil {
		return nil, err
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { s.unrouted(w, r, mux, false) })
	return securityHeaders(recoverPanics(s.log, s.socketPeer(mux))), nil
}
```

In `internal/controller/api/server.go`, add to `Server`, directly after the `handler   http.Handler` field:

```go
	socket    http.Handler   // SocketHandler's answer, built once by New
```

(`gofmt` realigns the `streamMux` comment), and in `New` replace the final `return s, nil` by:

```go
	socket, err := s.buildSocketHandler()
	if err != nil {
		return nil, err
	}
	s.socket = socket
	return s, nil
```

In `internal/controller/api/meta.go`, in `handleOpenAPI`, replace `doc, err := schema.OpenAPI(s.cfg.Registry, Routes())` by `doc, err := schema.OpenAPI(s.cfg.Registry, s.OpenAPIRoutes())` and, in its comment, `// the registry and Routes on each request; it is rarely fetched.` by `// the registry and OpenAPIRoutes on each request; it is rarely fetched.`

- [ ] **Step 4: Run the tests to verify they pass**

Run: `TMPDIR=/root/.cache/w0c-tmp/44 GOTOOLCHAIN=local go test -count=1 -race -run 'TestExtension|TestOpenAPI|TestSocket' ./internal/controller/api/ && TMPDIR=/root/.cache/w0c-tmp/44 GOTOOLCHAIN=local go test -count=1 ./internal/controller/... && TMPDIR=/root/.cache/w0c-tmp/44 golangci-lint run --allow-serial-runners ./internal/controller/api/`
Expected: every package `ok` (the socket tests of `socket_test.go` still pass against the one handler) and `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add internal/controller/api/extensions.go internal/controller/api/extensions_test.go internal/controller/api/socket.go internal/controller/api/server.go internal/controller/api/meta.go
git commit -m "feat(api): mount contributed socket routes"
rm -rf /root/.cache/w0c-tmp/44
```

### Task 45: Change actions for undo, repair and release

The actions of the change page (1.3: "the UI offers Undo on undoable changes and Repair, Release and per-effect resolution (preview first) on `needs_repair` changes"). **Undo** is offered by `undoOffered` (`web/src/lib/undo.ts`), which mirrors the engine's admission (`undoRefusal`, `internal/core/lifecycle/undo.go:204-233`) as far as `ChangeView` fields show it: class `compensable`, no owner, not a composite's step unless the step itself is `needs_repair`, and state `completed`, `failed`, `interrupted` or `needs_repair`. What the view cannot show (no undo handler, a closed window, an unresolved effect) the engine refuses, and the page shows that refusal; composites are never undone (the engine refuses every class but `compensable`). Undo asks first, then stores the answered change (`202` while undoing). **Repair** posts `{}`: a `200 { plan }` answer is applied as is through `ChangeDialog`'s `plan` prop (the client applies a repair plan through the ordinary apply route, `internal/controller/api/changes.go:121-150`), and a `202 { change }` answer is stored. **Release** asks for a reason of 1 to 500 characters (`maxReleaseReason`, `internal/controller/api/changes.go:15`) and reports how many effects were abandoned. A composite that needs repair also gets **Decide a step**: pick a step and whether it counts as reverted or applied, sent as `{ step, as }`; only the step at the composite's cursor can be decided, which the engine checks (`decideStep`, `internal/core/lifecycle/composite_ops.go:208-230`). `undoOffered` lives in its own module because the repository's `react-refresh/only-export-components` rule warns on a component file that also exports a function, and lint runs with `--max-warnings 0`. Test helpers carry the prefix `cac`.

**Files:**
- Create: `web/src/lib/undo.ts`
- Create: `web/src/pages/change-actions.tsx`
- Modify: `web/src/pages/change.tsx` (imports; `ChangePage`'s body)
- Test: `web/src/pages/change-actions.test.tsx`

**Interfaces:**
- Consumes: C21 (`undoChange`, `repairChange(id, body)` with `body` either `{}`, `{ input }` or `{ step, as }`, `releaseChange(id, reason)` returning `ReleaseResult`, `listSteps`, `changeQueryKey`, `effectsQueryKey`, `stepsQueryKey`, `errorMessage`; the types `Change`, `Plan`, `Step`); C23 (`ChangeDialog` with `plan`); C24 (`ChangePage` in `web/src/pages/change.tsx`).
- Produces: none that a later task consumes.

**Items:** 95

**Depends on:** Task 11, Task 27, Task 43

**Implementer:** dforge:impl-sonnet-medium
**Evaluation:** files 2 - spec 0 - coupling 1 - risk 1 = 4

- [ ] **Step 1: Write the failing test**

Create `web/src/pages/change-actions.test.tsx`:

```tsx
import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import { changeQueryKey } from "@/api/client"
import type { Change, Plan, Step } from "@/api/types"
import { renderWithProviders, stubFetch, type StubHandler } from "@/test/utils"
import { undoOffered } from "@/lib/undo"
import { ChangeActions } from "./change-actions"

function cacChange(over: Partial<Change> = {}): Change {
  return {
    id: "c1", hostId: "h1", action: "cdx.rule.set", version: 1, class: "compensable", state: "completed", actor: "user:u1",
    createdAt: "2026-10-23T08:00:00Z", updatedAt: "2026-10-23T08:05:00Z", revertDeadline: null, output: null, error: null,
    compensatedBy: null, parentChangeId: null, owner: null, repairedBy: null, repairOutcome: null, override: null, ...over,
  }
}

function cacRender(change: Change, routes: Record<string, StubHandler> = {}) {
  const calls = stubFetch(routes)
  const result = renderWithProviders(<ChangeActions change={change} />)
  return { calls, ...result }
}

const problem = (status: number, code: string, detail: string) => ({ status, body: { type: "about:blank", title: code, status, detail, code } })

test.each([
  ["a completed compensable change", {}, true],
  ["a failed one", { state: "failed" }, true],
  ["an interrupted one", { state: "interrupted" }, true],
  ["one that needs repair", { state: "needs_repair" }, true],
  ["a revertible change", { class: "revertible" }, false],
  ["a composite", { class: "composite" }, false],
  ["an owned change", { owner: "engine" }, false],
  ["a composite's completed step", { parentChangeId: "p1" }, false],
  ["a composite's step that needs repair", { parentChangeId: "p1", state: "needs_repair" }, true],
  ["a reverted change", { state: "reverted" }, false],
  ["an applying change", { state: "applying" }, false],
] as Array<[string, Partial<Change>, boolean]>)("undo is offered for %s: %s", (_name, over, offered) => {
  expect(undoOffered(cacChange(over))).toBe(offered)
})

test("undo asks first and stores the answered change", async () => {
  const undoing = cacChange({ state: "undoing", owner: "undo" })
  const { calls, client } = cacRender(cacChange(), { "POST /api/v1/changes/c1/undo": { status: 202, body: undoing } })
  const user = userEvent.setup()
  await user.click(screen.getByRole("button", { name: "Undo" }))
  const dialog = screen.getByRole("dialog", { name: "Undo this change?" })
  await user.click(within(dialog).getByRole("button", { name: "Undo" }))
  await waitFor(() => expect(client.getQueryData(changeQueryKey("c1"))).toEqual(undoing))
  expect(calls.filter((c) => c.path === "/api/v1/changes/c1/undo").length).toBe(1)
  expect(screen.queryByRole("dialog")).toBeNull()
})

test("a refused undo shows the reason", async () => {
  cacRender(cacChange(), { "POST /api/v1/changes/c1/undo": problem(409, "core.undo_conflict", "lock firewall was taken by a later change") })
  const user = userEvent.setup()
  await user.click(screen.getByRole("button", { name: "Undo" }))
  await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Undo" }))
  expect((await screen.findByRole("alert")).textContent).toBe("lock firewall was taken by a later change")
})

test("a change that needs no repair and cannot be undone offers nothing", () => {
  cacRender(cacChange({ class: "revertible", state: "confirmed" }))
  expect(screen.queryByRole("region", { name: "Actions" })).toBeNull()
})

test("repair applies the plan it answers through the change dialog", async () => {
  const plan: Plan = {
    planId: "rp1", hostId: "h1", action: "cdx.rule.fix", version: 1, inputHash: "h", diff: [{ op: "+", text: "restore rule 443" }],
    guards: [], expiresAt: "2026-10-23T09:00:00Z", confirmation: null,
  }
  const { calls } = cacRender(cacChange({ state: "needs_repair" }), {
    "POST /api/v1/changes/c1/repair": { body: { plan } },
    "POST /api/v1/hosts/h1/actions/cdx.rule.fix/v1/apply": { body: cacChange({ id: "r1", action: "cdx.rule.fix" }) },
  })
  const user = userEvent.setup()
  await user.click(screen.getByRole("button", { name: "Repair" }))
  const dialog = await screen.findByRole("dialog", { name: "Repair the change" })
  expect(within(dialog).getByLabelText("Planned changes").textContent).toBe("+ restore rule 443")
  await user.click(within(dialog).getByRole("button", { name: "Apply" }))
  await waitFor(() => expect(calls.find((c) => c.path.endsWith("/apply"))?.body).toEqual({ planId: "rp1" }))
  expect(calls.find((c) => c.path.endsWith("/repair"))?.body).toEqual({})
  expect(calls.some((c) => c.path.endsWith("/plan"))).toBe(false)
})

test("a repair that starts in place stores the change it answers", async () => {
  const recovering = cacChange({ state: "recovering", owner: "repair" })
  const { client } = cacRender(cacChange({ state: "needs_repair" }), { "POST /api/v1/changes/c1/repair": { status: 202, body: { change: recovering } } })
  await userEvent.setup().click(screen.getByRole("button", { name: "Repair" }))
  await waitFor(() => expect(client.getQueryData(changeQueryKey("c1"))).toEqual(recovering))
})

test("release needs a reason and reports the abandoned effects", async () => {
  const released = cacChange({ state: "abandoned" })
  const { calls, client } = cacRender(cacChange({ state: "needs_repair" }), {
    "POST /api/v1/changes/c1/release": { body: { change: released, abandoned: [{ seq: 1 }] } },
  })
  const user = userEvent.setup()
  await user.click(screen.getByRole("button", { name: "Release" }))
  const dialog = screen.getByRole("dialog", { name: "Release this change?" })
  await user.type(within(dialog).getByLabelText("Reason"), "  the key was deleted by hand ")
  await user.click(within(dialog).getByRole("button", { name: "Release" }))
  expect((await screen.findByText("Released. 1 effect abandoned.")).getAttribute("role")).toBe("status")
  expect(calls.find((c) => c.path.endsWith("/release"))?.body).toEqual({ reason: "the key was deleted by hand" })
  expect(client.getQueryData(changeQueryKey("c1"))).toEqual(released)
})

test("a refused release stays open with the reason", async () => {
  cacRender(cacChange({ state: "needs_repair" }), { "POST /api/v1/changes/c1/release": problem(403, "auth.forbidden", "release needs the admin scope") })
  const user = userEvent.setup()
  await user.click(screen.getByRole("button", { name: "Release" }))
  const dialog = screen.getByRole("dialog", { name: "Release this change?" })
  await user.type(within(dialog).getByLabelText("Reason"), "stuck")
  await user.click(within(dialog).getByRole("button", { name: "Release" }))
  expect((await within(dialog).findByRole("alert")).textContent).toBe("release needs the admin scope")
})

test("a composite that needs repair decides one step", async () => {
  const steps: Step[] = [
    { id: "proxy", ordinal: 2, host: "h1", action: { Name: "cdx.proxy.add", Version: 1 }, state: "applying", attempt: 1, childChangeId: "k2", earlierChildChangeIds: null, childDeadline: null, error: null },
    { id: "network", ordinal: 1, host: "h1", action: { Name: "cdx.net.create", Version: 1 }, state: "applied", attempt: 1, childChangeId: "k1", earlierChildChangeIds: null, childDeadline: null, error: null },
  ]
  const { calls } = cacRender(cacChange({ class: "composite", state: "needs_repair" }), {
    "GET /api/v1/changes/c1/steps": { body: { steps } },
    "GET /api/v1/changes/c1": { body: cacChange({ class: "composite", state: "applying" }) },
    "POST /api/v1/changes/c1/repair": { status: 202, body: { change: cacChange({ class: "composite", state: "applying" }) } },
  })
  const user = userEvent.setup()
  expect(screen.queryByRole("button", { name: "Undo" })).toBeNull()
  const decide = await screen.findByRole("button", { name: "Decide step" })
  expect(decide.hasAttribute("disabled")).toBe(true)
  await user.click(screen.getByRole("radio", { name: "proxy (applying)" }))
  await user.click(screen.getByRole("radio", { name: "Applied" }))
  await user.click(decide)
  await waitFor(() => expect(calls.find((c) => c.path.endsWith("/repair"))?.body).toEqual({ step: "proxy", as: "applied" }))
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mkdir -p /root/.cache/w0c-tmp/45 && cd web && TMPDIR=/root/.cache/w0c-tmp/45 pnpm exec vitest run src/pages/change-actions.test.tsx`
Expected: FAIL — `Failed to resolve import "@/lib/undo"`.

- [ ] **Step 3: Write the implementation**

Create `web/src/lib/undo.ts`:

```ts
import type { Change } from "@/api/types"

const UNDOABLE_STATES = new Set(["completed", "failed", "interrupted", "needs_repair"])

// undoOffered mirrors the engine's undo admission as far as a ChangeView
// shows it: a compensable change that nothing owns, not a composite's step
// unless the step itself needs repair, in a state undo starts from. The
// engine still refuses one whose action has no undo handler, whose window
// a later change closed, or that has an unresolved effect; the page shows
// that refusal.
export function undoOffered(change: Change): boolean {
  return (
    change.class === "compensable" &&
    change.owner === null &&
    (change.parentChangeId === null || change.state === "needs_repair") &&
    UNDOABLE_STATES.has(change.state)
  )
}
```

Create `web/src/pages/change-actions.tsx`:

```tsx
import { useState, type FormEvent } from "react"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Button } from "darkraise-ui/components/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "darkraise-ui/components/dialog"
import { Label } from "darkraise-ui/components/label"
import { RadioGroup, RadioGroupItem } from "darkraise-ui/components/radio-group"
import { Textarea } from "darkraise-ui/components/textarea"
import {
  changeQueryKey,
  effectsQueryKey,
  errorMessage,
  listSteps,
  releaseChange,
  repairChange,
  stepsQueryKey,
  undoChange,
} from "@/api/client"
import type { Change, Plan } from "@/api/types"
import { ChangeDialog } from "@/components/change-dialog"
import { undoOffered } from "@/lib/undo"

const MAX_RELEASE_REASON = 500

function UndoDialog({ change, onClose, onError }: { change: Change; onClose: () => void; onError: (m: string | null) => void }) {
  const client = useQueryClient()
  const [busy, setBusy] = useState(false)

  async function undo() {
    setBusy(true)
    onError(null)
    try {
      const next = await undoChange(change.id)
      client.setQueryData(changeQueryKey(change.id), next)
      void client.invalidateQueries({ queryKey: effectsQueryKey(change.id) })
      onClose()
    } catch (err) {
      onError(errorMessage(err))
      onClose()
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open onOpenChange={(next) => (next || busy ? undefined : onClose())}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Undo this change?</DialogTitle>
          <DialogDescription>
            Darkpanel runs the change's inverse. It refuses when a later change has taken the same locks.
          </DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <Button variant="outline" disabled={busy} onClick={onClose}>
            Cancel
          </Button>
          <Button loading={busy} onClick={() => void undo()}>
            Undo
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function ReleaseDialog({ change, onClose, onDone }: { change: Change; onClose: () => void; onDone: (message: string) => void }) {
  const client = useQueryClient()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const reason = String(new FormData(event.currentTarget).get("reason") ?? "").trim()
    if (reason === "") {
      setError("Enter why the change is given up.")
      return
    }
    setBusy(true)
    setError(null)
    try {
      const res = await releaseChange(change.id, reason)
      client.setQueryData(changeQueryKey(change.id), res.change)
      void client.invalidateQueries({ queryKey: effectsQueryKey(change.id) })
      const n = res.abandoned.length
      onDone(`Released. ${n} ${n === 1 ? "effect" : "effects"} abandoned.`)
      onClose()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open onOpenChange={(next) => (next || busy ? undefined : onClose())}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Release this change?</DialogTitle>
          <DialogDescription>
            Releasing gives the change up: its locks are freed and every effect it could not settle is abandoned as it is.
          </DialogDescription>
        </DialogHeader>
        <form className="flex flex-col gap-4" onSubmit={(event) => void submit(event)}>
          <div className="flex flex-col gap-2">
            <Label htmlFor="release-reason">Reason</Label>
            <Textarea id="release-reason" name="reason" required maxLength={MAX_RELEASE_REASON} rows={3} />
          </div>
          {error !== null && (
            <Alert variant="destructive" role="alert">
              <AlertDescription>{error}</AlertDescription>
            </Alert>
          )}
          <DialogFooter>
            <Button type="submit" variant="destructive" loading={busy}>
              Release
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

// StepDecision repairs a composite by deciding one step: the operator says
// whether its work counts as reverted or as applied. Only the step at the
// composite's cursor can be decided, which the engine checks.
function StepDecision({ change, onError }: { change: Change; onError: (m: string | null) => void }) {
  const client = useQueryClient()
  const steps = useQuery({ queryKey: stepsQueryKey(change.id), queryFn: () => listSteps(change.id) })
  const [step, setStep] = useState("")
  const [as, setAs] = useState<"reverted" | "applied">("reverted")
  const [busy, setBusy] = useState(false)

  async function decide() {
    setBusy(true)
    onError(null)
    try {
      const res = await repairChange(change.id, { step, as })
      if (res.change !== undefined) client.setQueryData(changeQueryKey(res.change.id), res.change)
      void client.invalidateQueries({ queryKey: changeQueryKey(change.id) })
      void client.invalidateQueries({ queryKey: stepsQueryKey(change.id) })
    } catch (err) {
      onError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  if (steps.data === undefined || steps.data.length === 0) return null
  return (
    <fieldset className="flex flex-col gap-3 rounded-md border p-3">
      <legend className="px-1 text-sm font-medium">Decide a step</legend>
      <RadioGroup value={step} onValueChange={setStep} aria-label="Step">
        {[...steps.data].sort((a, b) => a.ordinal - b.ordinal).map((s) => (
          <div key={s.id} className="flex items-center gap-2">
            <RadioGroupItem id={`decide-step-${s.id}`} value={s.id} />
            <Label htmlFor={`decide-step-${s.id}`}>{`${s.id} (${s.state})`}</Label>
          </div>
        ))}
      </RadioGroup>
      <RadioGroup value={as} onValueChange={(v) => setAs(v as "reverted" | "applied")} aria-label="Count it as">
        {(["reverted", "applied"] as const).map((v) => (
          <div key={v} className="flex items-center gap-2">
            <RadioGroupItem id={`decide-as-${v}`} value={v} />
            <Label htmlFor={`decide-as-${v}`}>{v === "reverted" ? "Reverted" : "Applied"}</Label>
          </div>
        ))}
      </RadioGroup>
      <Button className="self-start" variant="outline" disabled={step === ""} loading={busy} onClick={() => void decide()}>
        Decide step
      </Button>
    </fieldset>
  )
}

// ChangeActions offers what can be done to a change: Undo on an undoable
// one, and Repair, Release and, for a composite, a step decision on one
// that needs repair.
export function ChangeActions({ change }: { change: Change }) {
  const client = useQueryClient()
  const [dialog, setDialog] = useState<"undo" | "release" | null>(null)
  const [repairPlan, setRepairPlan] = useState<Plan | null>(null)
  const [repairing, setRepairing] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const needsRepair = change.state === "needs_repair"

  async function repair() {
    setRepairing(true)
    setError(null)
    setNotice(null)
    try {
      const res = await repairChange(change.id, {})
      if (res.plan !== undefined) setRepairPlan(res.plan)
      if (res.change !== undefined) client.setQueryData(changeQueryKey(res.change.id), res.change)
      void client.invalidateQueries({ queryKey: changeQueryKey(change.id) })
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setRepairing(false)
    }
  }

  if (!undoOffered(change) && !needsRepair) return null
  return (
    <section aria-label="Actions" className="flex flex-col gap-3">
      <div className="flex flex-wrap gap-2">
        {undoOffered(change) && (
          <Button variant="outline" onClick={() => setDialog("undo")}>
            Undo
          </Button>
        )}
        {needsRepair && (
          <>
            <Button loading={repairing} onClick={() => void repair()}>
              Repair
            </Button>
            <Button variant="destructive" onClick={() => setDialog("release")}>
              Release
            </Button>
          </>
        )}
      </div>
      {needsRepair && change.class === "composite" && <StepDecision change={change} onError={setError} />}
      {error !== null && (
        <Alert variant="destructive" role="alert">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}
      {notice !== null && <p role="status" className="text-sm">{notice}</p>}
      {dialog === "undo" && <UndoDialog change={change} onClose={() => setDialog(null)} onError={setError} />}
      {dialog === "release" && <ReleaseDialog change={change} onClose={() => setDialog(null)} onDone={setNotice} />}
      {repairPlan !== null && (
        <ChangeDialog
          open
          onOpenChange={(next) => {
            if (!next) setRepairPlan(null)
          }}
          title="Repair the change"
          target={{ name: repairPlan.action, version: repairPlan.version, hostId: repairPlan.hostId ?? undefined }}
          plan={repairPlan}
          onApplied={() => void client.invalidateQueries({ queryKey: changeQueryKey(change.id) })}
        />
      )}
    </section>
  )
}
```

In `web/src/pages/change.tsx`, add after the `formatTime` import

```tsx
import { ChangeActions } from "./change-actions"
```

and in `ChangePage` add the actions right after the summary, so the body reads

```tsx
        <Summary change={c} />
        <ChangeActions change={c} />
        {c.class === "composite" && <Steps changeId={c.id} />}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `mkdir -p /root/.cache/w0c-tmp/45 && cd web && TMPDIR=/root/.cache/w0c-tmp/45 pnpm exec vitest run src/pages/change-actions.test.tsx src/pages/change.test.tsx`
Expected: PASS: 19 tests in `change-actions.test.tsx` (11 rows of the `undoOffered` table and 8 tests) and the 6 of `change.test.tsx`.

- [ ] **Step 5: Check the whole web suite, types and lint**

Run: `TMPDIR=/root/.cache/w0c-tmp/45 make lint-web test-web`
Expected: ESLint prints nothing (it runs with `--max-warnings 0`), `tsc --noEmit` prints nothing, and the Vitest summary reports every test file passed.

- [ ] **Step 6: Commit**

```bash
git add web/src/lib/undo.ts web/src/pages/change-actions.tsx web/src/pages/change-actions.test.tsx web/src/pages/change.tsx
git commit -m "feat(web): offer undo, repair and release"
rm -rf /root/.cache/w0c-tmp/45
```

### Task 46: Controller routes and services at startup

This task puts the contributions of Task 38 into the running controller (spec 1.12's frozen call site). `startListeners` gathers them after the web authority is loaded and before `api.New`, so every route reaches `api.Config.Extensions` (C18: web routes behind authentication, CSRF and the route's scope; socket routes behind the peer check; a clashing pattern fails `New` and with it `Start`). Once the network listeners and the controller socket serve, it starts the contributed services through the service runner (C17). Composite recovery has already run in `startServices`, so services start after it, as the spec requires; the runner's stop step is registered last, so at shutdown the services stop first and still see the dispatcher and listeners until each `Run` returns. The quarantine check of the runner is added by Task 48. End-to-end test helpers carry the prefix `xc` (`xcContributor`, `newXcContributor`, `xcText`, `xcWait`); `testOptions`, `start`, `baseURL`, `get`, `plainClient`, `socketClient` and `readToken` are used as written.

**Files:**
- Modify: `internal/controller/server/listeners.go:34-60` (`startListeners`: its comment, the contributions before `api.New`, `Extensions`), `:106-110` (the service runner after the socket)
- Test: `internal/controller/server/extensions_e2e_test.go`

**Interfaces:**
- Consumes: C17 — `startServiceRunner`, `serviceRunnerConfig`, `(*serviceRunner).stop` (Task 39); C18 — `api.Config.Extensions` and its clash error naming the pattern (Task 41); C20 — `(*Server).contributions` and `Options.Contributors` (Task 38).
- Produces: the startup order Task 47 extends: contributions before `api.New`, services after the socket serves.

**Items:** 52

**Depends on:** Task 38, Task 39, Task 44

**Implementer:** dforge:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5

- [ ] **Step 1: Write the failing test**

Create `internal/controller/server/extensions_e2e_test.go`:

```go
package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/ext"
)

// xcContributor is a test controller contribution: a read-scoped web route,
// a socket route and one service. It records the deps it was given; its
// service closes ran when it starts and stopped when its Run returns.
type xcContributor struct {
	deps    chan ext.Deps
	ran     chan struct{}
	stopped chan struct{}
	routes  []ext.Route // nil: the default two routes
	err     error
}

func newXcContributor() *xcContributor {
	return &xcContributor{deps: make(chan ext.Deps, 1), ran: make(chan struct{}), stopped: make(chan struct{})}
}

func xcText(text string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, text) })
}

func (c *xcContributor) Controller(deps ext.Deps) ([]ext.Route, []ext.Service, error) {
	c.deps <- deps
	if c.err != nil {
		return nil, nil, c.err
	}
	routes := c.routes
	if routes == nil {
		routes = []ext.Route{
			{Surface: ext.SurfaceWeb, Method: http.MethodGet, Path: "/api/v1/test/xc", Summary: "Test route",
				Scope: core.ScopeRead, Handler: xcText("web")},
			{Surface: ext.SurfaceSocket, Method: http.MethodGet, Path: "/api/v1/controller/test-xc", Summary: "Test socket route",
				Handler: xcText("socket")},
		}
	}
	svc := ext.Service{Name: "test.xc", Run: func(ctx context.Context) error {
		close(c.ran)
		<-ctx.Done()
		close(c.stopped)
		return ctx.Err()
	}}
	return routes, []ext.Service{svc}, nil
}

// xcWait fails unless ch closes within five seconds.
func xcWait(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s did not happen", what)
	}
}

func TestContributionsAreServed(t *testing.T) {
	opts := testOptions(t)
	c := newXcContributor()
	opts.Contributors = []ext.Controller{c}
	s := start(t, opts)

	var deps ext.Deps
	select {
	case deps = <-c.deps:
	case <-time.After(5 * time.Second):
		t.Fatal("the contribution was not called")
	}
	switch {
	case deps.DB != s.controllerDB, deps.Sealer != core.Sealer(s.controllerSealer), deps.Clock != opts.Clock,
		deps.Log == nil, deps.Hosts != core.HostDirectory(s.hosts), deps.Dispatch != ext.Dispatcher(s.dispatcher),
		deps.Events != ext.Publisher(s.events), deps.Audit != core.Audit(s.auditLog), deps.LocalHostID != s.LocalHostID():
		t.Errorf("deps = %+v, want the server's stores and services", deps)
	}

	url := baseURL(t, s)
	if code, _ := get(t, plainClient, url+"/api/v1/test/xc", ""); code != http.StatusUnauthorized {
		t.Errorf("GET the contributed web route without credentials = %d, want 401", code)
	}
	token := readToken(t, s, opts.Clock.(*coretest.FakeClock))
	if code, body := get(t, plainClient, url+"/api/v1/test/xc", token); code != http.StatusOK || body != "web" {
		t.Errorf("GET the contributed web route = %d %q, want 200 web", code, body)
	}
	if code, body := get(t, socketClient(s.SocketPath()), "http://controller/api/v1/controller/test-xc", ""); code != http.StatusOK || body != "socket" {
		t.Errorf("socket GET the contributed route = %d %q, want 200 socket", code, body)
	}

	xcWait(t, c.ran, "the contributed service's start")
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.stopped:
	default:
		t.Error("Shutdown returned before the contributed service's Run returned")
	}
}

func TestContributionErrorFailsStart(t *testing.T) {
	opts := testOptions(t)
	c := newXcContributor()
	c.err = errors.New("the contribution is broken")
	opts.Contributors = []ext.Controller{c}
	_, err := Start(context.Background(), opts)
	if err == nil || !strings.Contains(err.Error(), "contributor 0") || !strings.Contains(err.Error(), "the contribution is broken") {
		t.Fatalf("Start = %v, want the contribution's error", err)
	}
	// The failed Start undid itself: a second one with the same paths starts.
	opts.Contributors = nil
	start(t, opts)
}

func TestContributedRouteClashFailsStart(t *testing.T) {
	opts := testOptions(t)
	c := newXcContributor()
	c.routes = []ext.Route{{Surface: ext.SurfaceWeb, Method: http.MethodGet, Path: "/api/v1/changes", Scope: core.ScopeRead,
		Handler: xcText("clash")}}
	opts.Contributors = []ext.Controller{c}
	_, err := Start(context.Background(), opts)
	if err == nil || !strings.Contains(err.Error(), "/api/v1/changes") {
		t.Fatalf("Start = %v, want an error naming the clashing pattern", err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mkdir -p /root/.cache/w0c-tmp/46 && TMPDIR=/root/.cache/w0c-tmp/46 GOTOOLCHAIN=local go test -count=1 -run 'TestContributionsAreServed|TestContributionErrorFailsStart|TestContributedRouteClashFailsStart' ./internal/controller/server/`
Expected: FAIL: `TestContributionsAreServed` with `the contribution was not called`, and `TestContributionErrorFailsStart` and `TestContributedRouteClashFailsStart` with `Start = <nil>`.

- [ ] **Step 3: Write the implementation**

In `internal/controller/server/listeners.go`, replace the comment and opening of `startListeners` down to the `api.New` call with:

```go
// startListeners loads or creates the web authority, builds the API with
// the controller contributions' routes, starts the network listeners with
// it, serves the controller socket and then starts the contributed services.
// Composite recovery has run by then (startServices), so a service never
// races it.
func (s *Server) startListeners(ctx context.Context) error {
	authority, err := listen.LoadOrCreateAuthority(ctx, s.controllerDB, s.controllerSealer, s.clock)
	if err != nil {
		return err
	}
	routes, services, err := s.contributions()
	if err != nil {
		return err
	}
	srv, err := api.New(api.Config{
		Dispatcher: s.dispatcher, Auth: s.authService, Registry: s.registry, WebCA: authority,
		Availability: s.dispatcher.Availability,
		SchemaVersion: func(ctx context.Context) (int, error) {
			return store.SchemaVersion(ctx, s.controllerDB, store.SideController)
		},
		HostID: s.localHostID, Dev: s.opts.Dev, UI: api.EmbeddedUI(), Log: s.log, HealthStatus: s.opts.HealthStatus,
		Events: s.events, Clock: s.clock, Extensions: routes,
	})
```

and, after the `s.onShutdown("controller socket", ...)` step and before `urls := make(...)`, add:

```go
	// Registered last, so the services stop first and see the dispatcher and
	// the listeners until they have returned.
	runner := startServiceRunner(serviceRunnerConfig{Services: services, Clock: s.clock, Log: s.log})
	s.onShutdown("controller services", func(context.Context) error { runner.stop(); return nil })
```

Everything else in `startListeners` stays as it is.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `TMPDIR=/root/.cache/w0c-tmp/46 GOTOOLCHAIN=local go test -count=1 ./internal/controller/server/ && TMPDIR=/root/.cache/w0c-tmp/46 golangci-lint run --allow-serial-runners ./internal/controller/server/...`
Expected: `ok  	github.com/darkraise/darkpanel/internal/controller/server` and `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add internal/controller/server/listeners.go internal/controller/server/extensions_e2e_test.go
git commit -m "feat(server): serve contributed routes and services"
rm -rf /root/.cache/w0c-tmp/46
```

### Task 47: Assembly of runtime listeners and certificate sources

This task wires the runtime listener service (C19) and the web certificate sources (C13) into the controller. The service is created at the top of `startServices`, before the controller engine, so controller-side actions get it as `Env.Listeners` (C3) and the contributions as `ext.Deps.Listeners` (C2); the dispatcher's guard topology lists its bound addresses among `PanelListen` (spec 1.5, the panel is reached through runtime listeners too). It is started in `startListeners` right after the network listeners, with `api.Server.RuntimeHandler` (C10) and the HTTPS listeners' shared TLS configuration (spec 1.10), and stopped before them at shutdown, after the stream responses are closed. `listen.Config.Sources` gets the dispatcher and the local host id, so the certificate sources' internal reads (Task 42) have a dispatcher. Two contract amendments this task relies on, stated for the tasks that own them: (1) C13 — `(*Service).TLSConfig()` returns `(*tls.Config, error)` and issues the web leaf, without IP SANs, when no HTTPS listener has issued one yet; with only plain listeners (`--listen loopback`, the tests' options) no leaf exists and every runtime-listener handshake would fail with `listen: no web certificate issued yet`; (2) C19 — `Open` before `Start` records the listener in state open and `Start` binds it with the others, instead of refusing: composite recovery runs in `startServices`, before the network listeners and so before `Start`, and a recovered panel exposure may open its listener then. Test helpers carry the prefix `as` (`asDeps`, `asAdminToken`, `asWebCAClient`, `asPost`); `testOptions`, `start`, `get`, `socketClient` are used as written.

**Files:**
- Modify: `internal/controller/server/topology.go` (whole file below)
- Modify: `internal/controller/server/services.go:56` (`services` struct), `:76-77` (top of `startServices`), `:180-191` (`engineConfig`)
- Modify: `internal/controller/server/extensions.go` (`extDeps`)
- Modify: `internal/controller/server/listeners.go` (`listen.Start`'s config, and the runtime listeners after `s.onShutdown("listeners", svc.Shutdown)`)
- Test: `internal/controller/server/services_test.go:499-530` (`TestTopologyOf`)
- Test: `internal/controller/server/assembly_test.go`

**Interfaces:**
- Consumes: C3 — `core.Listeners`, `core.ListenerSpec`, `core.ClientHeaderNPM`, `Env.Listeners` (Task 2); C10 — `(*api.Server).RuntimeHandler` (Task 30); C11 — the proxied `Source` `"<client> via <transport>"` (Task 31), which the test reads from the audit log; C13 — `listen.Config.Sources`, `listen.SourceDeps`, `(*listen.Service).TLSConfig` as amended above (Task 42); C19 — `listen.NewRuntime`, `listen.RuntimeConfig`, `(*Runtime).Start`, `Addrs`, `Shutdown`, `Open`, `Revoke` as amended above (Task 40); C20 — `extDeps`, `Options.Contributors` (Task 38) and the startup order of Task 46.
- Produces: `Server.runtimeListeners *listen.Runtime` and `topology.setRuntime`, which Task 48's test and later waves use; `ext.Deps.Listeners` and the controller engine's `Env.Listeners` set.

**Items:** 51, 52

**Depends on:** Task 30, Task 31, Task 40, Task 42, Task 46

**Implementer:** dforge:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5

- [ ] **Step 1: Write the failing tests**

In `internal/controller/server/services_test.go`, `TestTopologyOf` calls `topologyOf` with the runtime listeners as its second argument and checks them. Replace its body from the first `if got := topologyOf(` to the end of the function with:

```go
	if got := topologyOf(web, nil, agent, peers); !reflect.DeepEqual(got, want) {
		t.Errorf("topologyOf = %+v, want %+v", got, want)
	}
	// A runtime listener is a panel address too; an IPv4-mapped one is unmapped.
	runtime := []netip.AddrPort{netip.MustParseAddrPort("[::ffff:172.31.255.1]:9443")}
	withRuntime := want
	withRuntime.PanelListen = []netip.AddrPort{netip.MustParseAddrPort("127.0.0.1:8443"), netip.MustParseAddrPort("172.31.255.1:9443")}
	if got := topologyOf(web, runtime, agent, peers); !reflect.DeepEqual(got, withRuntime) {
		t.Errorf("topologyOf with a runtime listener = %+v, want %+v", got, withRuntime)
	}

	// The sources count once they are registered.
	var tp topology
	if got := tp.read(); !reflect.DeepEqual(got, dispatch.Topology{}) {
		t.Errorf("an empty topology reads %+v", got)
	}
	tp.setWeb(func() []listen.Bound { return web })
	tp.setAgent(func() []listen.Bound { return agent })
	tp.setPeers(func() []netip.Addr { return peers })
	if got := tp.read(); !reflect.DeepEqual(got, want) {
		t.Errorf("the registered topology reads %+v, want %+v", got, want)
	}
	tp.setRuntime(func() []netip.AddrPort { return runtime })
	if got := tp.read(); !reflect.DeepEqual(got, withRuntime) {
		t.Errorf("the topology with runtime listeners reads %+v, want %+v", got, withRuntime)
	}
}
```

Create `internal/controller/server/assembly_test.go`:

```go
package server

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net/http"
	"net/netip"
	"slices"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/ext"
)

// asDeps is a contribution that only keeps the deps it was given.
type asDeps struct{ deps ext.Deps }

func (c *asDeps) Controller(deps ext.Deps) ([]ext.Route, []ext.Service, error) {
	c.deps = deps
	return nil, nil, nil
}

// asAdminToken creates an active user with an admin token.
func asAdminToken(t *testing.T, s *Server, clock *coretest.FakeClock) string {
	t.Helper()
	ctx := context.Background()
	_, secret, err := s.authService.CreateUser(ctx, "proxy-admin", "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	code, err := auth.TOTPCode(secret, clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	u, err := s.authService.ActivateUser(ctx, "proxy-admin", code)
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := s.authService.CreateToken(ctx, u.ID, "proxy", core.ScopeAdmin, 0)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

// asWebCAClient is an HTTPS client that trusts the controller's web
// authority and names localhost, a name every web leaf carries.
func asWebCAClient(t *testing.T, s *Server) *http.Client {
	t.Helper()
	code, body := get(t, socketClient(s.SocketPath()), "http://controller/api/v1/controller/web-ca", "")
	var ca struct{ PEM string }
	if err := json.Unmarshal([]byte(body), &ca); err != nil || code != http.StatusOK {
		t.Fatalf("web CA = %d %s", code, body)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(ca.PEM)) {
		t.Fatal("the web CA does not parse")
	}
	return &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool, ServerName: "localhost", MinVersion: tls.VersionTLS12},
	}}
}

// asPost posts body to the runtime listener at addr as the proxy would,
// naming client in X-Darkpanel-Client; an empty client sends no header.
func asPost(t *testing.T, c *http.Client, addr netip.AddrPort, path, token, client string, body any) (int, []byte, error) {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://"+addr.String()+path, bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Forwarded-Proto", "https")
	if client != "" {
		req.Header.Set(core.ClientHeaderNPM, client)
	}
	resp, err := c.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	out, err := io.ReadAll(resp.Body)
	return resp.StatusCode, out, err
}

// TestRuntimeListenerReachesTheAPI: a runtime listener opened through the
// contributions' Listeners serves the API over the web certificate, records
// the proxied client with the proxy's address in the audit log, counts as a
// panel address in the guard topology, and stops serving once revoked.
func TestRuntimeListenerReachesTheAPI(t *testing.T) {
	opts := testOptions(t)
	clock := opts.Clock.(*coretest.FakeClock)
	c := &asDeps{}
	opts.Contributors = []ext.Controller{c}
	s := start(t, opts)
	ctx := context.Background()
	if c.deps.Listeners != core.Listeners(s.runtimeListeners) || c.deps.Listeners == nil {
		t.Fatalf("deps.Listeners = %v, want the server's runtime listeners", c.deps.Listeners)
	}
	if got := s.engineConfig(s.registry).Base.Listeners; got != core.Listeners(s.runtimeListeners) {
		t.Errorf("the controller engine's Env.Listeners = %v, want the server's runtime listeners", got)
	}

	id, err := c.deps.Listeners.Open(ctx, core.ListenerSpec{
		Addr: netip.MustParseAddrPort("127.0.0.1:9443"), AllowedPeer: netip.MustParseAddr("127.0.0.1"),
		ClientHeader: core.ClientHeaderNPM, PublicHost: "panel.example.com", ChangeID: "01ASSEMBLYCHANGE0000000000",
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	addrs := s.runtimeListeners.Addrs()
	if len(addrs) != 1 {
		t.Fatalf("runtime listener addresses %v, want one", addrs)
	}
	addr := addrs[0]
	if !slices.Contains(s.topo.read().PanelListen, addr) {
		t.Errorf("topology %v does not list the runtime listener %s", s.topo.read().PanelListen, addr)
	}

	hc := asWebCAClient(t, s)
	token := asAdminToken(t, s, clock)
	in := map[string]any{"input": map[string]any{"name": "via-proxy", "scope": "read", "expiresInDays": 1}}
	code, body, err := asPost(t, hc, addr, "/api/v1/actions/core.token.create/v1/plan", token, "203.0.113.9", in)
	if err != nil || code != http.StatusOK {
		t.Fatalf("plan through the runtime listener = %d %s, %v", code, body, err)
	}
	var plan struct {
		PlanID string `json:"planId"`
	}
	if err := json.Unmarshal(body, &plan); err != nil {
		t.Fatal(err)
	}
	code, body, err = asPost(t, hc, addr, "/api/v1/actions/core.token.create/v1/apply", token, "203.0.113.9",
		map[string]any{"planId": plan.PlanID})
	if err != nil || code != http.StatusOK {
		t.Fatalf("apply through the runtime listener = %d %s, %v", code, body, err)
	}
	var source string
	if err := s.controllerDB.Read().QueryRowContext(ctx,
		`SELECT source FROM core_audit WHERE action = 'core.token.create' AND phase = 'apply' ORDER BY seq DESC, at DESC LIMIT 1`).
		Scan(&source); err != nil {
		t.Fatal(err)
	}
	if source != "203.0.113.9 via 127.0.0.1" {
		t.Errorf("audit source %q, want the client and the proxy's address", source)
	}
	if code, body, err := asPost(t, hc, addr, "/api/v1/actions/core.token.create/v1/plan", token, "", in); err == nil && code < 400 {
		t.Errorf("a request without the client header = %d %s, want a refusal", code, body)
	}

	if err := c.deps.Listeners.Revoke(ctx, id); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if slices.Contains(s.topo.read().PanelListen, addr) {
		t.Errorf("topology %v still lists the revoked listener %s", s.topo.read().PanelListen, addr)
	}
	if code, _, err := asPost(t, hc, addr, "/api/v1/actions/core.token.create/v1/plan", token, "203.0.113.9", in); err == nil && code < 400 {
		t.Errorf("a request after Revoke = %d, want a refusal or no connection", code)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mkdir -p /root/.cache/w0c-tmp/47 && TMPDIR=/root/.cache/w0c-tmp/47 GOTOOLCHAIN=local go test -count=1 -run 'TestTopologyOf|TestRuntimeListenerReachesTheAPI' ./internal/controller/server/`
Expected: FAIL to build with `too many arguments in call to topologyOf`, `tp.setRuntime undefined` and `s.runtimeListeners undefined`.

- [ ] **Step 3: Write the implementation**

Replace `internal/controller/server/topology.go` with:

```go
package server

import (
	"net/netip"
	"sync"

	"github.com/darkraise/darkpanel/internal/controller/dispatch"
	"github.com/darkraise/darkpanel/internal/controller/listen"
)

// topology is the dispatcher's Topology. The listeners start after the
// dispatcher exists, and startup recovery may already call the dispatcher, so
// each source is registered under mu once it exists; until then it adds
// nothing.
type topology struct {
	mu      sync.Mutex
	web     func() []listen.Bound
	runtime func() []netip.AddrPort
	agent   func() []listen.Bound
	peers   func() []netip.Addr
}

func (t *topology) setWeb(f func() []listen.Bound) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.web = f
}

func (t *topology) setRuntime(f func() []netip.AddrPort) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.runtime = f
}

func (t *topology) setAgent(f func() []listen.Bound) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.agent = f
}

func (t *topology) setPeers(f func() []netip.Addr) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.peers = f
}

// read is the topology now.
func (t *topology) read() dispatch.Topology {
	t.mu.Lock()
	web, runtime, agent, peers := t.web, t.runtime, t.agent, t.peers
	t.mu.Unlock()
	var webBound, agentBound []listen.Bound
	var runtimeAddrs []netip.AddrPort
	var agentPeers []netip.Addr
	if web != nil {
		webBound = web()
	}
	if runtime != nil {
		runtimeAddrs = runtime()
	}
	if agent != nil {
		agentBound = agent()
	}
	if peers != nil {
		agentPeers = peers()
	}
	return topologyOf(webBound, runtimeAddrs, agentBound, agentPeers)
}

// topologyOf is the topology of the bound web listeners, the runtime
// listeners, the bound agent listeners and the live agents' addresses. The
// panel is reached through the web and the runtime listeners alike (spec
// 1.5). A listener without an IP address, such as a Unix socket, is left out.
func topologyOf(web []listen.Bound, runtime []netip.AddrPort, agent []listen.Bound, peers []netip.Addr) dispatch.Topology {
	panel := boundAddrs(web)
	for _, ap := range runtime {
		panel = append(panel, netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port()))
	}
	return dispatch.Topology{PanelListen: panel, AgentListen: boundAddrs(agent), AgentPeers: peers}
}

func boundAddrs(bound []listen.Bound) []netip.AddrPort {
	var out []netip.AddrPort
	for _, b := range bound {
		if ap, err := netip.ParseAddrPort(b.Addr.String()); err == nil {
			out = append(out, netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port()))
		}
	}
	return out
}
```

In `internal/controller/server/services.go`, add the field after `events` in the `services` struct:

```go
	topo        topology // the dispatcher's guard topology
	events      *events.Bus
	// runtimeListeners are the controller's runtime listeners (spec 1.10),
	// created before the engine so controller-side actions and contributions
	// get them, and started with the network listeners.
	runtimeListeners *listen.Runtime
```

create the service right after the event bus at the top of `startServices`:

```go
	s.events = events.NewBus(s.log)
	s.runtimeListeners = listen.NewRuntime(listen.RuntimeConfig{DB: s.controllerDB, Clock: s.clock, Log: s.log, Bind: s.opts.Bind})
	s.topo.setRuntime(s.runtimeListeners.Addrs)
	s.auditLog = audit.New(s.controllerDB, s.clock)
```

and give the controller engine's `Base` the listeners, so `engineConfig` reads:

```go
// engineConfig is the controller engine's configuration. Its Publish hands
// the composite.step events to the event bus.
func (s *Server) engineConfig(reg *core.Registry) lifecycle.Config {
	return lifecycle.Config{
		Side: core.SideController, Registry: reg, Validator: schema.NewValidator(), Journal: s.auditLog,
		Redact: schema.Redact, Publish: s.events.Publish,
		Base: core.Env{
			DB: s.controllerDB, Sealer: s.controllerSealer, Clock: s.clock, Log: s.log, Agents: s.hosts,
			Listeners: s.runtimeListeners,
		},
	}
}
```

In `internal/controller/server/extensions.go`, `extDeps` passes the same service:

```go
// extDeps is what the assembly gives controller contributions (spec 1.12).
func (s *Server) extDeps() ext.Deps {
	return ext.Deps{
		DB: s.controllerDB, Sealer: s.controllerSealer, Clock: s.clock, Log: s.log, Hosts: s.hosts,
		Dispatch: s.dispatcher, Events: s.events, Listeners: s.runtimeListeners, Audit: s.auditLog, LocalHostID: s.localHostID,
	}
}
```

In `internal/controller/server/listeners.go`, give `listen.Start` the certificate sources' dependencies:

```go
	svc, err := listen.Start(ctx, listen.Config{
		Specs: specs, Hostname: s.hostname, DB: s.controllerDB, Authority: authority, Clock: s.clock,
		Handler: srv.Handler(), Log: s.log, Interfaces: s.opts.Interfaces, Bind: s.opts.Bind,
		OnServeError: s.serveFailure,
		Sources:      &listen.SourceDeps{Dispatch: s.dispatcher, LocalHostID: s.localHostID, Log: s.log},
	})
```

and start the runtime listeners right after the network listeners' shutdown step, before the `stream responses` step:

```go
	s.listenService = svc
	s.topo.setWeb(func() []listen.Bound { return svc.State().Listeners })
	s.onShutdown("listeners", svc.Shutdown)
	// The runtime listeners share the HTTPS listeners' TLS configuration
	// (spec 1.10); every listener recorded open, also one an action opened
	// during startup recovery, is bound here.
	tlsConfig, err := svc.TLSConfig()
	if err != nil {
		return err
	}
	if err := s.runtimeListeners.Start(ctx, srv.RuntimeHandler, tlsConfig); err != nil {
		return err
	}
	s.onShutdown("runtime listeners", s.runtimeListeners.Shutdown)
	// Registered after the listeners, so it runs before their shutdown,
	// which waits for running requests.
	s.onShutdown("stream responses", func(context.Context) error { srv.CloseStreams(); return nil })
```

Shutdown therefore runs: controller services, controller socket, stream responses (which also ends the runtime listeners' streams), runtime listeners, network listeners.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `TMPDIR=/root/.cache/w0c-tmp/47 GOTOOLCHAIN=local go test -count=1 ./internal/controller/server/ && TMPDIR=/root/.cache/w0c-tmp/47 GOTOOLCHAIN=local go test -count=3 -race -run TestRuntimeListenerReachesTheAPI ./internal/controller/server/ && TMPDIR=/root/.cache/w0c-tmp/47 golangci-lint run --allow-serial-runners ./internal/controller/server/...`
Expected: `ok  	github.com/darkraise/darkpanel/internal/controller/server` twice and `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add internal/controller/server/topology.go internal/controller/server/services.go internal/controller/server/extensions.go internal/controller/server/listeners.go internal/controller/server/services_test.go internal/controller/server/assembly_test.go
git commit -m "feat(server): wire runtime listeners and cert sources"
rm -rf /root/.cache/w0c-tmp/47
```

### Task 48: Assembly of the restore quarantine

This task points every gate of restore quarantine (spec 10) at the one flag in `controller.db`: the dispatcher's `Quarantine` (C14) answers `core.quarantined` before every apply and change mutation, and `quarantine.Active` suspends the controller engine's composite recovery, child watcher and module recovery handlers (C15), the rollout driver and update scheduler (C16) and the contributed services (C17). The agent's own engine is untouched: agents keep their databases through a restore and nothing on them is assumed stopped. After the Go changes it checks that `make schemas` regenerates nothing: no task of this plan adds an action. Test helpers: the tests use the `xc` helpers of Task 46 (`newXcContributor`, `xcWait`) and the existing `planDisable`, `root`, `refDisable`, `exec`, `count`, `t0`, `embeddedOptions` and `fakeIndex` (`rollouts_test.go`); they declare none of their own.

**Files:**
- Modify: `internal/controller/server/services.go` (imports, the dispatcher's config, `engineConfig`, two new methods after it, `startRollouts`)
- Modify: `internal/controller/server/listeners.go` (the service runner's config)
- Test: `internal/controller/server/assembly_quarantine_test.go`

**Interfaces:**
- Consumes: C14 — `quarantine.MetaKey`, `quarantine.Active`, `quarantine.Check` (Task 25) and `dispatch.Config.Quarantine` (Task 26); C15 — `lifecycle.Config.Suspended` (Task 12); C16 — `rollout.DriverConfig.Suspended`, `rollout.SchedulerConfig.Suspended` (Task 13); C17 — `serviceRunnerConfig.Suspended` and `serviceDefaultPoll` (Task 39); the `xc` test helpers (Task 46).
- Produces: `(*Server).quarantined` and `(*Server).refuseQuarantined`; a controller that honours the flag wave 7's restore sets.

**Items:** 51, 52

**Depends on:** Task 12, Task 13, Task 25, Task 26, Task 39, Task 46, Task 47

**Implementer:** dforge:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5

- [ ] **Step 1: Write the failing test**

Create `internal/controller/server/assembly_quarantine_test.go`:

```go
package server

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/buildinfo"
	"github.com/darkraise/darkpanel/internal/controller/quarantine"
	"github.com/darkraise/darkpanel/internal/controller/rollout"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/ext"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// TestQuarantinedControllerHoldsMutations: a controller started on a
// database whose restore-quarantine flag is set refuses applies, does not
// start the contributed services or drive rollouts, and starts the
// services once the flag is cleared.
func TestQuarantinedControllerHoldsMutations(t *testing.T) {
	opts := testOptions(t)
	clock := opts.Clock.(*coretest.FakeClock)
	ctx := context.Background()
	first, err := Start(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(ctx, opts.Paths.ControllerDB())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Writer().ExecContext(ctx, `INSERT INTO core_meta (key, value) VALUES (?, ?)`, quarantine.MetaKey,
		`{"tx":"01RESTORE","restoredAt":"2026-10-18T12:00:00Z","backupCreatedAt":"2026-10-17T12:00:00Z"}`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	c := newXcContributor()
	opts.Contributors = []ext.Controller{c}
	s := start(t, opts)
	id := s.LocalHostID()
	plan := planDisable(t, s)
	_, err = s.dispatcher.Apply(ctx, root(id), "", refDisable, plan.PlanID, core.ApplyOptions{})
	if e := new(errcode.Error); !errors.As(err, &e) || e.Code != errcode.Quarantined {
		t.Errorf("apply in quarantine = %v, want %s", err, errcode.Quarantined)
	}

	exec(t, s, `UPDATE core_hosts SET state = 'enrolled' WHERE id = ?`, id)
	var r core.Rollout
	if err := s.controllerDB.Write(ctx, func(tx *sql.Tx) error {
		var err error
		r, err = rollout.Create(ctx, tx, rollout.CreateParams{
			Input:             core.RolloutInput{Hosts: []string{id}, Version: "0.2.0", Channel: core.ChannelStable},
			ControllerVersion: buildinfo.Version, Now: clock.Now(),
		})
		return err
	}); err != nil {
		t.Fatalf("create the rollout: %v", err)
	}
	clock.Advance(rollout.PollInterval)
	var hostState string
	if err := s.controllerDB.Read().QueryRowContext(ctx,
		`SELECT state FROM core_rollout_hosts WHERE rollout_id = ? AND host_id = ?`, r.ID, id).Scan(&hostState); err != nil {
		t.Fatal(err)
	}
	if hostState != core.RolloutHostWaiting {
		t.Errorf("rollout host in quarantine is %s, want %s: the driver must not run", hostState, core.RolloutHostWaiting)
	}
	select {
	case <-c.ran:
		t.Fatal("a contributed service started in quarantine")
	case <-time.After(100 * time.Millisecond):
	}

	exec(t, s, `DELETE FROM core_meta WHERE key = ?`, quarantine.MetaKey)
	clock.Advance(serviceDefaultPoll)
	xcWait(t, c.ran, "the contributed service's start after the quarantine")
}

// TestQuarantineSuspendsSchedulerAndEngine: once the flag is set on a
// running controller, the update scheduler claims no open window, and the
// controller engine's configuration answers suspended, so composite
// recovery, the child watcher and recovery handlers hold.
func TestQuarantineSuspendsSchedulerAndEngine(t *testing.T) {
	opts := embeddedOptions(t)
	opts.ReleaseIndex = fakeIndex{{Version: "0.2.0", Channel: core.ChannelStable}}
	clock := opts.Clock.(*coretest.FakeClock)
	s := start(t, opts)
	ctx := context.Background()
	id := s.LocalHostID()
	exec(t, s, `UPDATE core_hosts SET state = 'enrolled', agent_version = '0.1.0' WHERE id = ?`, id)
	exec(t, s, `INSERT INTO core_host_update (host_id, channel, days, time_utc) VALUES (?, 'stable', 'mon,tue,wed,thu,fri,sat,sun', ?)`,
		id, t0.Format("15:04"))
	exec(t, s, `INSERT INTO core_meta (key, value) VALUES (?, ?)`, quarantine.MetaKey,
		`{"tx":"01RESTORE","restoredAt":"2026-10-18T12:00:00Z","backupCreatedAt":"2026-10-17T12:00:00Z"}`)

	clock.Advance(rollout.ScheduleInterval)
	if n := count(t, s, `SELECT count(*) FROM core_rollouts`); n != 0 {
		t.Errorf("rollouts after one ScheduleInterval in quarantine: %d, want 0: the scheduler must not run", n)
	}

	suspended := s.engineConfig(s.registry).Suspended
	if suspended == nil {
		t.Fatal("engineConfig().Suspended is nil; restore quarantine must suspend the controller engine")
	}
	if on, err := suspended(ctx); err != nil || !on {
		t.Errorf("engine Suspended in quarantine = %v, %v; want true, nil", on, err)
	}
	exec(t, s, `DELETE FROM core_meta WHERE key = ?`, quarantine.MetaKey)
	if on, err := suspended(ctx); err != nil || on {
		t.Errorf("engine Suspended after the quarantine = %v, %v; want false, nil", on, err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mkdir -p /root/.cache/w0c-tmp/48 && TMPDIR=/root/.cache/w0c-tmp/48 GOTOOLCHAIN=local go test -count=1 -run 'TestQuarantinedControllerHoldsMutations|TestQuarantineSuspendsSchedulerAndEngine' ./internal/controller/server/`
Expected: FAIL with `apply in quarantine = <nil>, want core.quarantined` (or the apply's own outcome in place of `<nil>`), `rollout host in quarantine is updating, want waiting`, `a contributed service started in quarantine`, `rollouts after one ScheduleInterval in quarantine: 1, want 0` and `engineConfig().Suspended is nil`.

- [ ] **Step 3: Write the implementation**

In `internal/controller/server/services.go`, add the import `"github.com/darkraise/darkpanel/internal/controller/quarantine"` after `internal/controller/listen`. Give the dispatcher its gate:

```go
	s.dispatcher, err = dispatch.New(dispatch.Config{
		Registry: reg, Engine: eng, Hosts: s.hosts, DB: s.controllerDB, Clock: s.clock, Log: s.log, Ingest: s.ingester,
		Version: buildinfo.Version, Topology: s.topo.read, Quarantine: s.refuseQuarantined,
	})
```

Replace `engineConfig` with the following, and add the two methods after it:

```go
// engineConfig is the controller engine's configuration. Its Publish hands
// the composite.step events to the event bus, and restore quarantine
// suspends its composite recovery, child watcher and module recovery
// handlers.
func (s *Server) engineConfig(reg *core.Registry) lifecycle.Config {
	return lifecycle.Config{
		Side: core.SideController, Registry: reg, Validator: schema.NewValidator(), Journal: s.auditLog,
		Redact: schema.Redact, Publish: s.events.Publish, Suspended: s.quarantined,
		Base: core.Env{
			DB: s.controllerDB, Sealer: s.controllerSealer, Clock: s.clock, Log: s.log, Agents: s.hosts,
			Listeners: s.runtimeListeners,
		},
	}
}

// quarantined reports whether the controller database is in restore
// quarantine (spec 10), which suspends every automatic mutation source.
func (s *Server) quarantined(ctx context.Context) (bool, error) {
	return quarantine.Active(ctx, s.controllerDB)
}

// refuseQuarantined is the dispatcher's gate: core.quarantined while the
// controller is in restore quarantine.
func (s *Server) refuseQuarantined(ctx context.Context) error {
	return quarantine.Check(ctx, s.controllerDB)
}
```

In `startRollouts`, give both loops the same check:

```go
	driver, err := rollout.NewDriver(rollout.DriverConfig{
		DB: s.controllerDB, Dispatcher: s.dispatcher, Clock: s.clock, Log: s.log, Suspended: s.quarantined,
	})
	if err != nil {
		return fmt.Errorf("server: %w", err)
	}
	scheduler, err := rollout.NewScheduler(rollout.SchedulerConfig{
		DB: s.controllerDB, Clients: clients, Index: index, Audit: s.auditLog, Clock: s.clock, Log: s.log,
		Version: buildinfo.Version, Suspended: s.quarantined,
	})
```

In `internal/controller/server/listeners.go`, the service runner waits for the flag to clear:

```go
	runner := startServiceRunner(serviceRunnerConfig{Services: services, Suspended: s.quarantined, Clock: s.clock, Log: s.log})
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `TMPDIR=/root/.cache/w0c-tmp/48 GOTOOLCHAIN=local go test -count=1 ./internal/controller/server/ && TMPDIR=/root/.cache/w0c-tmp/48 golangci-lint run --allow-serial-runners ./internal/controller/server/...`
Expected: `ok  	github.com/darkraise/darkpanel/internal/controller/server` and `0 issues.`

- [ ] **Step 5: Check the schema snapshots**

Run: `TMPDIR=/root/.cache/w0c-tmp/48 GOTOOLCHAIN=local make schemas && git status --porcelain schemas`
Expected: `git status` prints nothing: this plan adds no action, so no snapshot changes. If it prints a path, a task added an action schema without its snapshot; stop and report it rather than committing the regenerated files.

- [ ] **Step 6: Commit**

```bash
git add internal/controller/server/services.go internal/controller/server/listeners.go internal/controller/server/assembly_quarantine_test.go
git commit -m "feat(server): honour the restore quarantine flag"
rm -rf /root/.cache/w0c-tmp/48
```

### Task 49: ADR 0014 and the deferred decisions

Write ADR 0014, which spec Contracts says records the core extensions of the v1 completion programme (spec 1.1–1.14, the composite refinement of ADR 0004, the module recovery handler exception, repair and release, the Compose exceptions to ADR 0009 and R54, the owned-path kinds and module-owned Docker objects, DERP as a signed artifact under systemd, `cloudflared` as a container, the full-vector exec rules, the controller-generated call context, the reveal path, the extension package and its frozen call sites, the quarantine gate, the ADR 0008 divergence and the new packages), and update `docs/adr/README.md`: the index line, the shared-contract additions (error codes and actors), the removal of the two deferred decisions ADR 0014 closes (how DERP servers run, how `cloudflared` runs), and traceability rows for every requirement the ADR names. This task runs last so the ADR describes what the plan built; before Step 3, check each name the ADR cites against the merged code (`rg -n 'Suspended|Quarantined|RuntimeHandler|ProbeReads|Contribute' internal/`) and, if a later task renamed something, use the merged name. No test helpers.

**Files:**
- Create: `docs/adr/0014-v1-core-extensions.md`
- Modify: `docs/adr/README.md` (Index, Shared contracts, Deferred decisions, Traceability)

**Interfaces:**
- Consumes: the names of C1–C24 as merged by Tasks 1–48.
- Produces: nothing other tasks consume.

**Items:** 53

**Depends on:** Task 48

**Implementer:** dforge:impl-sonnet-low
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 0 = 2

- [ ] **Step 1: Add the index line first (the failing check)**

In `docs/adr/README.md`, replace

```markdown
- [ADR 0013: Go toolchain patch version](0013-go-toolchain-patch-version.md)
```

with

```markdown
- [ADR 0013: Go toolchain patch version](0013-go-toolchain-patch-version.md)
- [ADR 0014: v1 core extensions](0014-v1-core-extensions.md)
```

- [ ] **Step 2: Run the checker to verify it fails**

Run: `bash scripts/check-adrs.sh --complete`
Expected: exit 1 with `check-adrs: docs/adr/README.md: links missing file 0014-v1-core-extensions.md`.

- [ ] **Step 3: Write the ADR and the rest of the index changes**

Create `docs/adr/0014-v1-core-extensions.md`:

```markdown
# ADR 0014: v1 core extensions

**Requirements:** R5, R10, R11, R12, R54, R58, R61, R69, R70, R72

## Status

Proposed

## Context

The v1 completion design (`docs/dforge/specs/2026-10-21-v1-completion-design.md`)
builds the firewall, Docker, Tailscale, DERP, Nginx Proxy Manager, Cloudflare
Tunnel and panel-exposure modules on one set of core extensions, built first
as wave 0 so that the module waves change no shared file. Several extensions
refine ADR 0004 (composites, undo, repair, recovery handlers), ADR 0009
(full-vector exec rules, new owned-path kinds, the Compose exception) and
ADR 0008 (no test tailnet in CI), and the design closes two of the index's
deferred decisions: how DERP servers run and how `cloudflared` runs.
Accepted ADRs are not edited, so the additions are recorded here. The
design's section numbers are cited as "spec 1.n".

## Decision

- **Action names (spec 1.1).** Names stay `<module>.<noun>.<verb>`; only the
  verb may contain `-`. `ActionRef` marshals as text in its
  `<name>/v<version>` form.
- **Effects journal and undo (spec 1.2).** Every agent- and controller-side
  change records its effects in `core_effects` (`Env.Effects`). An effect not
  proven settled is `unresolved`, its change goes to `needs_repair` and keeps
  its locks, and only the operator resolves it (`accept` or `reconcile`,
  after an `Observe` preview). Calls that hand work to another process
  (Docker Engine, the Tailscale local API, systemd jobs, every cloud
  provider) are not `Local` and wait `CommitWait` (cloud calls 10 minutes,
  local daemons 2 minutes) before resolution. Every compensable action
  declares exactly one of an undo handler (`Undoer`), a compensating action
  or `ForwardOnly`. Undo is allowed only while no later change took any of
  the change's lock keys (the lock epochs) and never over an unresolved
  effect.
- **Composites (spec 1.3), refining ADR 0004.** A composite is a
  controller-side action of class `composite` whose apply runs steps: child
  changes of other modules' actions, with `unwind: reverse | forward`.
  Composites and long-running actions apply asynchronously (apply answers
  `202` with the change `applying`); the composite runner follows each child
  through the agent's read-only plan lookup and is recovered at startup.
- **Repair and release (spec 1.3).** A change in `needs_repair` is repaired
  under the repaired change's own locks by a repair change of depth 1 (a
  repair is never repaired), or given up by an audited release (`admin`,
  with a reason, journaled as phase `release`), which abandons its
  non-terminal effects.
- **Module recovery handlers (spec 1.3b).** An action may declare a module
  `Recoverer` that the engine runs, owned `module`, when apply or verify
  fails without an unresolved effect, and that repair runs again. This is the
  one exception to ADR 0004's rule that compensable and irreversible actions
  are never retried on their own. Verify stays read-only.
- **Task units (spec 1.3c).** Host sequences that can restart or disconnect
  the agent (Tailscale and DERP package and binary work) run in transient
  units `darkpanel-task-<change id>-<op>-<attempt>.service` running
  `darkpanel agent task`, fenced by `core_task_runs` and the uninstall
  barrier (`core_meta` key `uninstalling`, `core.uninstalling`).
- **Background tasks and enablement (spec 1.4).** Modules declare background
  tasks that run on the agent only while the module is enabled and its
  preflight passes; the controller pushes enablement to the agent
  (`PUT /agent/v1/modules/{id}/enabled`), and a task's actor is
  `system:<task id>`.
- **Controller-generated call context (spec 1.5).** Why a change runs
  (`Env.Call`: parent composite, step, retired exposures, the guard
  topology) comes from the controller, never from input; the API and the
  agent socket refuse a supplied `guard` or `call`. Guards declared
  `Overridable` are overridden only by listing their ids in `overrides`
  (`--override`); every other guard is a hard refusal. A request through a
  runtime listener carries its proxy path in `GuardContext.Via`.
- **Streams and events (spec 1.6).** Modules declare streams (`StreamDef`);
  the controller relays them and its own event bus as server-sent events with
  epoch-qualified ids and fixed limits.
- **Availability, scope and internal actions (spec 1.7).** `ActionSpec`
  gains `Available`, `MinScope` (raise only) and `Internal` (dispatched only
  by the controller).
- **Host clients, owned paths and exec rules (spec 1.8), refining ADR 0009.**
  The runner's allowlist is a list of `ExecRule`s that check the whole
  argument vector and fix the environment (`ProductionRules()`); nothing
  else runs. Owned paths gain the kinds `restore`, `managed-dir`, `sysctl`
  and `service-dir`, and `unit` becomes writable. Docker objects a module
  creates carry the label `darkpanel.module=<id>`, and mutations of objects
  owned by another module are refused (`docker.owned`). New host clients
  live in `internal/platform/dockerapi`, `tslocal`, `netstat` and
  `netfilter`; Docker and Tailscale are reached through their APIs, with no
  SDK.
- **Compose exceptions (spec 3), to ADR 0009 and R54.** `docker compose` is
  an allowlisted binary that reads operator-authored files and starts
  containers through the daemon; it runs only with fixed verb flags, a fixed
  environment that disables Compose's Git and OCI remote loaders, and
  `--no-build`. Validation reads `docker compose config --format json`: the
  one place Darkpanel parses `docker` CLI output, Compose's own JSON
  document, parsed strictly and fuzzed.
- **Cloud plumbing (spec 1.9).** `internal/controller/cloud` is the one
  provider HTTP client: 15 s per attempt, at most 3 retries for `GET` and
  `DELETE` only, honouring `Retry-After`, never re-sending a write. A write
  that fails with a 5xx or a transport error is `cloud.uncertain_outcome`:
  its effect becomes `unresolved` and the operator resolves it after
  `CommitBy`. Every 4xx answer to a write is definite. Account credentials
  live in each module's own table, sealed with associated data
  `<table>.<column>.<id>`, write-only through the API. Tests use
  `internal/controller/cloud/cloudfake` and per-module fakes; integrations
  with real providers run only in the owner's live step.
- **Runtime listeners and trusted proxies (spec 1.10).** The controller opens
  and closes HTTPS listeners recorded in `core_listeners` without a restart
  (`core.Listeners`, on `Env` for controller-side actions). Each accepts TCP
  connections only from its allowed peer, reads exactly one client header
  that a proxy Darkpanel configured sets (`X-Darkpanel-Client` for Nginx
  Proxy Manager, `Cf-Connecting-IP` for a tunnel), never `X-Forwarded-For`
  or `X-Real-IP`, refuses a forwarded protocol other than `https`, refuses
  an unsafe request whose `Origin` is not the exposure's public host, and
  records both addresses as the audit source `"<client> via <proxy>"`. Revocation is
  durable and monotonic: `revoked` is committed before the listener stops,
  and a revoked listener is never reopened. Requests on every other listener
  never read forwarded headers.
- **Secret delivery (spec 1.14).** A reveal action (`ActionSpec.Reveal`,
  class `read`, scope `admin`) returns one stored secret once in the
  response body, audited before it returns, never over `--local`.
- **Extension package and frozen call sites (spec 1.12).** `internal/core/ext`
  (importing only `internal/core`, `internal/core/store` and the standard
  library) defines `Route`, `Service`, `Deps`, `Dispatcher`, `Publisher` and
  the optional `Controller` interface a module implements. At startup the
  controller assembly calls `Controller(deps)` on every module and
  `backup.Contribute(deps)`, mounts each route on the web surface or the
  controller socket (a clashing pattern fails startup), and starts each
  service after composite recovery with restart backoff from 1 s to 5 min
  and actor `system:<service name>`. The other call sites are the web
  certificate sources (`internal/controller/listen/sources.go`), the CLI's
  module command map (`internal/cli/modules.go`), path selection
  (`cmd/darkpanel/paths.go`) and the web registry's extension points
  (`web/src/modules/types.ts`). After wave 0 these files, the core contract,
  the lifecycle engine, the migrations, `go.mod` and the web dependency
  manifest are frozen: a later wave that needs a change there stops, and the
  change is made on `main` first.
- **Restore quarantine gate (spec 1.12, 10).** The `core_meta` key
  `restore_quarantine` in `controller.db` suspends every automatic mutation
  source (composite recovery, the child watcher, controller module recovery
  handlers, the update scheduler and rollout driver, controller services)
  and refuses every action apply, undo, repair, release and resolution with
  `core.quarantined`. Restore sets it; only `quarantine release` clears it.
- **DERP as a signed artifact under systemd (spec 4.3).** The release
  workflow builds `derper` from upstream `tailscale.com/cmd/derper` at the
  version in `release/derper.version` and signs it with the release; the
  agent installs it as `/usr/local/lib/darkpanel/derper` running as system
  user `darkpanel-derp` in the unit `darkpanel-derper.service`. This closes
  the index's deferred decision on how DERP servers run.
- **`cloudflared` as a container (spec 7.2).** The connector runs as the
  owned container `darkpanel-cloudflared-<tunnel>` from a pinned image digest,
  labelled `darkpanel.module=cloudflare`, reading its token from a file in a
  managed directory. This closes the index's deferred decision on how
  `cloudflared` runs.
- **Test tailnet, diverging from ADR 0008.** ADR 0008 planned a test tailnet
  in CI from M5. Tests that need a tailnet run in the owner's live step
  instead; the harness runs the Tailscale node tests that need none.
- **New packages.** `internal/core/ext`, `internal/platform/dockerapi`,
  `tslocal`, `netstat`, `netfilter`, `internal/controller/cloud`,
  `internal/controller/events`, `internal/controller/backup` and
  `internal/controller/quarantine`, and one package per module under
  `internal/modules/<id>`: `firewall`, `docker`, `tailscale`, `tailnet`,
  `derp`, `npm`, `cloudflare`, `panel`.

## Alternatives considered

- A package-level registry that modules fill from `init`: rejected. The
  extension package has no state; the assembly asks each compiled-in module,
  so the import graph `server → modules → ext` has no cycle and tests build
  their own module lists.
- Retrying uncertain cloud writes automatically: rejected. Providers give no
  guarantee that a timed-out request did not commit, so a resend can create
  a second object; the operator resolves against what the provider shows.
- Trusting `X-Forwarded-For` from any proxy: rejected. Only a header that a
  proxy Darkpanel configured overwrites can carry the client address, and
  only on a listener that accepts that proxy alone.
- DERP as a container: rejected. Client verification needs `derper` beside
  the host's `tailscaled`, and a systemd unit under its own user keeps the
  binary signed with the release.
- `cloudflared` as a systemd service: rejected. The pinned image needs no
  package repository and runs unprivileged as UID 65532.
- A test tailnet in CI: rejected for v1. It needs owner credentials in CI;
  the live step covers the same flows once.

## Consequences

- Modules add actions, controller routes, services, CLI commands and web
  pages only through the extension points; the frozen files change only on
  `main`, before the wave that needs the change continues.
- Every cloud write can leave a change in `needs_repair` that only the
  operator resolves, and the UI and CLI offer the preview and resolution.
- Panel exposure depends on the runtime-listener contract; any proxy other
  than the ones Darkpanel configures cannot carry a trusted client address.
- A DERP host's `tailscaled` is held at the derper version, so a Tailscale
  security release needs a Darkpanel release that bumps
  `release/derper.version`.
- Restore leaves the controller in quarantine until the operator resolves
  or keeps every record and releases it.
```

In `docs/adr/README.md`, replace

```markdown
- **Errors.** RFC 9457 problem details with a stable `code` field, from the
  registry in `internal/core/errcode`.
```

with

```markdown
- **Errors.** RFC 9457 problem details with a stable `code` field, from the
  registry in `internal/core/errcode`. ADR 0014 adds the codes of the v1
  modules (`firewall.*`, `docker.*`, `tailscale.*`, `tailnet.*`, `cloud.*`,
  `npm.*`, `cloudflare.*`, `derp.*`, `panel.*`) and the lifecycle codes
  `core.needs_repair`, `core.recovering`, `core.undo_conflict`,
  `core.change_busy`, `core.uninstalling`, `core.stream_limit`,
  `core.child_failed`, `core.deadline_exceeded` and `core.quarantined`.
```

replace

```markdown
  `system:update-schedule` and `local:root@unenrolled`.
```

with

```markdown
  `system:update-schedule` and `local:root@unenrolled`; ADR 0014 adds
  `system:recovery`, `system:listen.cert`, `system:<task id>` for module
  background tasks and `system:<service name>` for controller services
  (`system:firewall.reconcile`, `system:tailnet.keysweep`,
  `system:cloudflare.hostnames`).
```

delete these two lines under `## Deferred decisions` (the backup line stays):

```markdown
- How DERP servers run (container or systemd service): M5 DERP epic.
- How `cloudflared` runs: M6 Cloudflare Tunnel epic.
```

and in the Traceability table replace the rows R5, R10, R11 and R12 and add six rows before R80, so those parts read:

```markdown
| R5 | 0002 (manifest interface, compiled-in modules), 0014 (extension package and frozen call sites) |
```

```markdown
| R10 | 0004 (plan, apply ordering, compensable and irreversible actions), 0014 (effects journal and undo, composites, repair and release, module recovery handlers) |
| R11 | 0009 (commands, files), 0014 (host clients, owned paths and exec rules) |
| R12 | 0005 (secrets), 0006 (authentication boundary), 0014 (secret delivery) |
```

```markdown
| R36 | 0007 (order and rollouts) |
| R54 | 0014 (Compose exceptions) |
| R58 | 0014 (cloud plumbing) |
| R61 | 0014 (DERP as a signed artifact under systemd) |
| R69 | 0014 (cloud plumbing) |
| R70 | 0014 (`cloudflared` as a container) |
| R72 | 0014 (runtime listeners and trusted proxies) |
| R80 | 0012 (signature files, release index) |
```

- [ ] **Step 4: Run the checker to verify it passes**

Run: `bash scripts/check-adrs.test.sh && bash scripts/check-adrs.sh --complete && echo adr-ok`
Expected: `check-adrs tests: 21 passed, 0 failed`, no `check-adrs:` error line, then `adr-ok`.

- [ ] **Step 5: Commit**

```bash
git add docs/adr/0014-v1-core-extensions.md docs/adr/README.md
git commit -m "docs(adr): record the v1 core extensions"
```
