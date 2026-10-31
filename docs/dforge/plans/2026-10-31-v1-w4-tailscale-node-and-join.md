# v1 wave 4: Tailscale node, guard, package, web certificate and tailnet join at install Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: the skill the **Execution:** line names — dforge:subagent-driven-development for `subagent`, dforge:executing-plans for `inline`. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build wave 4 of the v1 completion spec: the agent-side `tailscale` module (status, latency, preferences and their repair, login and logout, the `tailscale.link` guard, package install, update, pin, unpin and uninstall, and the internal `tailscale.cert.get`), the controller's Tailscale web certificate and MagicDNS name, the tailnet join at install (the `dpb2.` bootstrap bundle, `core.host.create`'s `tailnetJoin`, enroll keys and their cleanup, the installer's join, `tailnet.node.join`), the `/hosts/:id/tailscale` page and the Hosts dialog extension, and the M5 node harness tests that need no tailnet.

**Architecture:** The `tailscale` module follows the docker module's shape: a `module` value holding an internal struct `ts` whose methods construct one `core.ActionDef` per action, a `deps` struct that tests replace with fakes, a module-private test host, and one registration task that wires every constructor into `Manifest()` and commits the schema snapshots. Pure logic lives in two small packages with no I/O: `tailscale/stun` (a hand-written STUN binding client) and `tailscale/link` (the `tailscale.link` guard's rules over classified flows). The package actions are task actions (`core.DefineTask`, spec 1.3c); the pin is an agent-local transaction journaled in its own table that rolls itself back and has a module recovery handler (1.3b). The join at install adds a versioned bundle beside the unchanged token (`internal/transport`), lets `core.host.create` ask a module implementing `core.EnrollJoiner` (the tailnet module) for a one-use auth key, and lets the installer join the tailnet through the tailscale module's exported `Join` before it enrolls over the tailnet.

**Tech Stack:** Go 1.26 (standard library, `internal/platform/tslocal`, `internal/platform/netstat`, `internal/platform` exec allowlist with the APT vectors), SQLite through `internal/core/store`, React 19 + TypeScript + TanStack Query + darkraise-ui (Vitest), the KVM harness in `test/harness` (GitHub Actions `harness.yml`).

**Spec:** docs/dforge/specs/2026-10-21-v1-completion-design.md

**Execution:** subagent — `claude --model sonnet --effort high` — the plan runs parallel waves, and each task's **Implementer:** line chooses its implementer

**Parallelism:** waves — 1-3 | 4 | 5-7 | 8-10 | 11-13 | 14-16 | 17-19 | 20 | 21-23 | 24 | 25-26 | 27-28 | 29 | 30 | 31 | 32-33 | 34 | 35

**Integration check:** `mkdir -p /root/.cache/w4-tmp/integ && export TMPDIR=/root/.cache/w4-tmp/integ GOTOOLCHAIN=local && timeout 600 go build ./... && timeout 300 go vet ./... && timeout 300 go vet -tags harness ./test/... && timeout 1500 go test -count=1 ./... && timeout 900 golangci-lint run --allow-serial-runners ./... && timeout 600 golangci-lint run --allow-serial-runners --build-tags harness ./test/... && timeout 300 go mod tidy -diff && timeout 1200 make lint-web test-web`

**Worktree setup:** `timeout 600 make web-deps`

**Wave check:** `mkdir -p /root/.cache/w4-tmp/wave && export TMPDIR=/root/.cache/w4-tmp/wave GOTOOLCHAIN=local && timeout 600 go build ./... && timeout 300 go vet ./... && timeout 900 go test -count=1 ./internal/modules/... ./internal/transport/... ./internal/controller/... ./internal/install/... ./internal/core/... ./schemas/... && timeout 300 go vet -tags harness ./test/... && timeout 1200 make lint-web test-web`

## Global Constraints

- Scope is spec revision 8 as amended on `main`: section 4.1 (R56, R57), section 4.2's join at install (R60: steps 1–5, the bundle, the enroll key's cleanup and `tailnet.node.join`), ADR 0010's web certificate through `internal/controller/listen/tailscale_cert.go` (4.1, 1.12), the tailscale rows and the `tailnet.node.join` row of section 5, the tailscale error codes of section 11, the M5 row of Verification for the node tests that need no tailnet (Contracts of the spec: "the harness runs the node tests that need no tailnet"), and the register rows the tasks' **Items:** lines name. DERP (4.3, wave 5), the live tailnet demonstrations (spec 12 item 2) and everything of waves 6–10 are out of scope.
- Host safety (absolute). This host is live: it runs an installed `darkpanel-agent`, `darkpanel-controller` and a Docker daemon, and has no tailscaled. No test or command of this plan runs `apt-get`, `apt-mark`, `dpkg`, `tailscale`, `tailscaled` or `systemctl` on this host, talks to a tailscaled socket, writes under `/etc/apt`, `/usr/share/keyrings`, `/var/lib/darkpanel`, `/etc/darkpanel` or `/run/darkpanel`, or starts, stops, restarts or reloads any systemd unit. Every host effect in a unit test goes through the module's fakes (Contracts "Tailscale test host"); `TestTailscaledLive` and the M5 harness tests run only on harness VMs. No test reaches Tailscale's servers: the tailnet module's provider calls go to its fake server on `127.0.0.1` (`internal/modules/tailnet/fake`). Only Task 1 reads public documentation or downloads anything over the network.
- Every `go`, `golangci-lint`, `make` and `pnpm` command runs with `GOTOOLCHAIN=local`, an explicit `timeout`, and `TMPDIR=/root/.cache/w4-tmp/<task number>` (`mkdir -p` it first; remove it and run `go clean -testcache` when the task ends), also where a step's command omits one of them: `/tmp` is an 8 GiB tmpfs and Unix socket paths over 108 bytes fail. When `df -h /` passes 90 %, trim Go build cache entries older than a few hours. Web commands use `pnpm -C web exec vitest run <files>` for single files (`pnpm -C web vitest run` does not work here).
- Dependencies: no task changes `go.mod`, `go.sum`, `web/package.json` or `web/pnpm-lock.yaml`; only the standard library and modules already required are used, and after every task `GOTOOLCHAIN=local timeout 300 go mod tidy -diff` prints nothing. The STUN client is hand-written; no Tailscale library is imported (spec 4.1).
- After every task all of these pass, with their real output recorded: `go build ./...`; `go vet ./...` (no output); `go test -count=1 ./...` (every line `ok` or `?`); `go test -race -count=1` on the task's packages; `golangci-lint run --allow-serial-runners ./...` (`0 issues.`); `go mod tidy -diff` (no output). A task that edits `test/harness/` also runs `go vet -tags harness ./test/...` and `go test -tags harness -count=1 -run 'Unit' ./test/harness/...`. A task that edits `web/` also runs `pnpm -C web lint`, `pnpm -C web typecheck` and `pnpm -C web test`. A task that adds or changes an action, resource or input type runs `make schemas` and commits the snapshots it changes, or `modules.TestSnapshotsCurrent` fails; only the tasks whose **Files:** name `schemas/` do this.
- Contract-check gate: after wave 1-3 merges, the controller reads Task 1's report. Any entry under its `Divergences` heading other than `none` stops execution until the integrator amends Contracts and every task that states the old value (a header amendment), before wave 4 starts. Tasks after wave 1-3 implement their text as written.
- TDD: write the test, run it and see the expected RED (usually a build failure naming the first undefined identifier), implement, run GREEN. Quote the RED and GREEN output in the report, not a paraphrase.
- Frozen files (spec 1.12), which no task edits: `internal/core/contract.go`, `units.go`, `define.go`, `registry.go`, `guard.go`, `internal/core/ext/**`, `internal/core/errcode/errcode.go`, `internal/core/lifecycle/**`, the core migrations, `internal/platform/exec.go`, `files.go`, `systemd.go`, `internal/platform/exec_rules.go`, `internal/modules/modules.go`, `internal/cli/modules.go`, `internal/controller/server/**`, `internal/controller/api/**`, `internal/controller/events/**`, `internal/controller/listen/sources.go`, `cmd/darkpanel/paths.go`, `go.mod`, `go.sum`, `web/package.json`, `web/pnpm-lock.yaml`, `web/src/modules/registry.ts`, `web/src/modules/types.ts`, `web/src/app.tsx`, `test/harness/pins.go`, `test/harness/images.json`, `internal/platform/tslocal/**`, `internal/platform/netstat/**`, `internal/modules/firewall/**`. A task that finds a missing code, allowlist rule or extension point in a frozen file stops and reports it; it does not edit the file. Every program and argument vector this plan runs is already allowlisted (Assumptions).
- A task edits only the files of its own **Files:** block. Each task of a wave runs in its own worktree from the wave's base (`main` plus every merged earlier wave), so a package-wide command sees only merged work plus the task's own files. "The base" in a task's text means the work its **Depends on** line names.
- Test isolation: `t.TempDir()` for files and databases; TCP and UDP only on `127.0.0.1` port 0 (`httptest` servers, the STUN fake on `net.ListenPacket("udp", "127.0.0.1:0")`); `coretest.NewFakeClock` wherever a duration above one second is waited on (the 120 s pin check, the 60 s join wait, the 2/3-life certificate renewal); no fixed path outside a temporary directory; no global mutable state shared between tests of different tasks. Tasks of one wave therefore share no port, path or global state.
- Tasks that share a Go package declare no top-level identifier, test helpers included, that another task declares. Every new test identifier carries its task's prefix from Contracts "Test prefixes", except the shared helpers of Contracts "Tailscale test host", which only Task 8 declares (and Task 24 its `tsSession` additions).
- Code style: match the surrounding code; a doc comment on every exported identifier; comments only for a non-obvious why, in full sentences, never naming a plan, task, register row or review; `//nolint` names the rule and a reason; gofmt clean. Error strings start with the package name (`tailscale:`, `stun:`, `link:`, `tailnet:`, `transport:`, `coreactions:`, `listen:`, `install:`, `harness:`). Refusals are `*errcode.Error` with client-safe details using only codes already in `internal/core/errcode/errcode.go` (`tailscale.unavailable`, `tailscale.repo_conflict`, `tailscale.package_present`, `tailscale.package_held`, `tailscale.pin_failed`, `plan.guard_failed`, `plan.state_changed`, `request.invalid_input`, `enroll.token_invalid`, `cloud.*`, `not_found`); a refusal that committed nothing is wrapped with `core.Definite`. No detail, diff line, `StateDoc`, effect, log line, audit entry or error carries a secret (auth key, bootstrap bundle, enrollment token, certificate private key).
- Lock keys: every tailscale mutating action locks `tailscale`; `tailscale.status`, `tailscale.latency` and `tailscale.cert.get` take none. `tailnet.node.join` is a composite and takes `composite:tailnet`; its children take their own locks.
- Values copied from the spec, used exactly: lock key `tailscale`; guard id `tailscale.link`, overridable; interface `tailscale0`; Tailscale address ranges `100.64.0.0/10` and `fd7a:115c:a1e0::/48`; task unit names `darkpanel-task-<change id>-<op>-<attempt>.service`; the staged-package directory `/var/lib/darkpanel/agent/tailscale-debs/` with flat names `tailscale_<version>_<arch>.deb`; the keyring `/usr/share/keyrings/tailscale-archive-keyring.gpg` and the source list `/etc/apt/sources.list.d/tailscale.list`; the `stable` track; `<version>` matching `^[0-9][0-9A-Za-z.+~-]{0,63}$`; the pin check window 120 s; the join wait 60 s; latency: three probes per measurement, median; the DERP HTTPS probe `GET /derp/latency-check`; the bundle prefix `dpb2.` and version `2`; the enroll key: one use, pre-authorized, not ephemeral, expiry 3600 s, description `darkpanel-enroll-<host id>`, `tailnet_keys` kind `enroll` and owner the host id; the internal-read principal `core.Principal{Actor: "system:listen.cert", Scope: core.ScopeAdmin}`; certificate renewal at two thirds of its life; the apt vectors only through `platform.AptUpdateTailscale`, `AptInstallTailscale`, `AptInstallVersion`, `AptStageVersion`, `AptInstallDeb` and `AptRemoveTailscale`; `apt-mark` only `hold tailscale`, `unhold tailscale` and `showhold`; `dpkg` only `--configure -a`, called only by the pin's rollback and recovery.
- Commits use `<type>(<scope>): <subject>`, type one of feat|fix|docs|style|refactor|test|chore|perf, subject at most 50 characters, imperative, no trailing period, no attribution of any kind. English only.
- The plan ends with the gates in `docs/dforge/gates.md`. Tasks 30, 33, 34 and 35 touch `internal/install/`, `install.sh.in`, `test/harness/` or `.github/workflows/harness.yml`, so gate 7 (harness) applies to the branch once it is pushed, as well as on `main`; dispatch `harness.yml` on the branch per the owner's standing authorization. No task creates or pushes a tag.

## Contracts

Names are given with their package; everything not exported is used only inside its package. A task's Interfaces block cites these entries by their heading ("Contracts: Tailscale wire shapes").

### Test prefixes

| Package | Task: prefix |
|---|---|
| `internal/transport` | 2: `bd` |
| `tailscale/stun` | 3: `sn` |
| `tailscale/link` | 4: `lk` |
| `internal/controller/listen` | 6: `tc` |
| `internal/core` | 7: `ej` |
| `tailscale` (module root) | 8: `th` (the shared helpers of Contracts "Tailscale test host"), 14: `st`, 15: `lt`, 16: `ce`, 17: `lg`, 20: `pi`, 21: `pf`, 22: `li`, 23: `pu`, 24: `pn`, 25: `un`, 26: `jn`, 27: `pr`, 28: `up`, 29: `rb`, 31: `reg` |
| `internal/controller/coreactions` | 9: `hj` |
| `tailnet` (module root) | 10: `je`, 18: `js`, 32: `nj` |
| `internal/install`, `cmd/darkpanel`, `internal/agent/daemon` | 30: `ij` |
| `test/harness` | 33: `m5` (shared helpers of Contracts "Harness M5"), 34: `m5p`, 35: `m5l` |
| web (`web/src/modules/tailscale/`, `web/src/modules/tailnet/`, `web/src/pages/`) | each test file uses its own file's prefix; shared fixtures only in `web/src/modules/tailscale/fixtures.ts` |

### Dependencies

Each task's **Depends on** line is exactly this: 1, 2, 3, 4, 5, 6, 7: none; 8: Task 1; 9: Task 2, Task 7; 10, 11: Task 7; 12, 13: Task 5; 14, 16, 20: Task 8; 15: Task 3, Task 8; 17: Task 4, Task 8; 18: Task 10; 19: Task 12, Task 13; 21: Task 14, Task 17; 22: Task 17; 23, 24, 25, 26: Task 20; 27: Task 21; 28, 29: Task 24; 30: Task 2, Task 26; 31: Task 14, Task 15, Task 16, Task 20, Task 21, Task 22, Task 23, Task 24, Task 25, Task 27, Task 28, Task 29; 32: Task 10, Task 31; 33: Task 31; 34: Task 33; 35: Task 34.

### Bootstrap bundle (from Task 2)

Package `internal/transport`, file `token.go`:

```go
// BundlePrefix starts a version-2 bootstrap bundle; "dpb" and the digit are the format and its version.
const BundlePrefix = "dpb2."

// Bundle is a bootstrap bundle: an enrollment token plus what a host needs to join the tailnet first (spec 4.2).
// JSON members: {"v": 2, "token": <Token.String()>, "tailnetKey": <auth key>, "controllerTailnetAddr": <host:port>}.
type Bundle struct {
	Token                 Token
	TailnetKey            string // a Tailscale auth key, "tskey-auth-..."; never empty
	ControllerTailnetAddr string // host:port inside 100.64.0.0/10 or fd7a:115c:a1e0::/48 (IPv6 in brackets)
}

// String is the bundle as the operator copies it: BundlePrefix + base64url (no padding) of the JSON above.
func (b Bundle) String() string

// ParseBootstrap reads what the operator pasted: a plain token (TokenPrefix) gives (token, nil, nil);
// a BundlePrefix bundle gives (bundle.Token, &bundle, nil). "dpb<digits>." with a version other than 2 is refused
// with enroll.token_invalid and the detail "bootstrap bundle version <n> is not supported by this darkpanel";
// every other malformed input is refused with ParseToken's own error (enroll.token_invalid, "the enrollment token is malformed").
// A bundle must hold exactly the four members, v == 2, a token ParseToken accepts, a non-empty tailnetKey without
// whitespace, and a controllerTailnetAddr that net.SplitHostPort accepts, whose host is a Tailscale address and
// whose port is 1-65535.
func ParseBootstrap(s string) (Token, *Bundle, error)
```

`ParseToken` is unchanged. `internal/transport/token_fuzz_test.go` holds `FuzzParseBootstrap` (seeds: a plain token, a bundle, a version-3 bundle, an empty string) asserting that an accepted bundle round-trips through `String` and `ParseBootstrap`.

### STUN client (from Task 3)

Package `internal/modules/tailscale/stun`:

```go
// Probe sends one RFC 5389 Binding request (a random 96-bit transaction id, no attributes) to server over a
// UDP socket it opens with net.ListenPacket("udp", ""), and returns the round-trip time of the first Binding
// success response with the same transaction id and a valid XOR-MAPPED-ADDRESS (or MAPPED-ADDRESS) attribute.
// It honours ctx's deadline; with none it waits at most 3 s. Any other datagram is ignored.
func Probe(ctx context.Context, server netip.AddrPort) (time.Duration, error)

// Request encodes a Binding request with transaction id tx; Parse decodes a Binding success response,
// returning its transaction id and mapped address, or an error naming what is wrong.
func Request(tx [12]byte) []byte
func Parse(b []byte) (tx [12]byte, mapped netip.AddrPort, err error)
```

### Link guard rules (from Task 4)

Package `internal/modules/tailscale/link`, pure (no I/O):

```go
const (
	GuardID   = "tailscale.link"
	Interface = "tailscale0"
)

// Flow is one protected flow (spec 2.3) as the tailscale.link guard classifies it.
type Flow struct {
	Label    string     // "ssh", "panel", "agent link", "agent peer", "controller session", "controller link"
	Inbound  bool       // the flow arrives at this host; false: this host opens it
	Local    netip.Addr // this host's address of the flow
	Remote   netip.Addr // the other end
	OutIface string     // the interface the kernel routes Remote through from Local ("" when unknown)
	Default  bool       // the route to Remote is a default route (0.0.0.0/0 or ::/0)
	Request  bool       // this flow carries the request being planned
	Unknown  string     // non-empty: the flow could not be classified; the guard fails with this reason
}

// Prefs are the node's preferences the rules read.
type Prefs struct {
	WantRunning       bool
	AcceptRoutes      bool
	ShieldsUp         bool
	RunSSH            bool
	ExitNode          string // "" when none
	AdvertiseExitNode bool
	AdvertiseRoutes   []netip.Prefix // without 0.0.0.0/0 and ::/0
}

// Change is what an action would do to the node.
type Change struct {
	Kind  string // "prefs" | "logout" | "relogin"
	After Prefs  // Kind "prefs": the node's preferences after the change (current ones with the masked fields applied)
}

// Check evaluates spec 4.1's guard for change c on a node whose current preferences are cur:
//   - depends on Tailscale: OutIface == Interface or Local is a Tailscale address (100.64.0.0/10, fd7a:115c:a1e0::/48);
//   - "logout", "relogin", or After.WantRunning false: fails when any flow depends on Tailscale or cur.RunSSH;
//   - After.ShieldsUp true (cur false): fails when an Inbound flow depends on Tailscale or cur.RunSSH;
//   - After.RunSSH false (cur true): fails unless some Request flow does not depend on Tailscale;
//   - After.AcceptRoutes false (cur true): fails when a flow's OutIface is Interface, Default is false and Remote is not a Tailscale address;
//   - After.ExitNode differs from cur.ExitNode (cur non-empty): fails when a flow's OutIface is Interface and Default is true;
//   - a prefix in cur.AdvertiseRoutes missing from After: fails when an Inbound flow with OutIface Interface has Local inside it;
//   - cur.AdvertiseExitNode true, After false: fails when an Inbound flow with OutIface Interface has a Local that is neither a Tailscale address nor inside any of cur.AdvertiseRoutes;
//   - a flow with Unknown set fails whatever the change.
// The result is core.Guard{ID: GuardID, Overridable: true, OK, Message}; Message names every failing flow by
// Label, Remote and the rule (one sentence each, joined with "; "), or "no protected flow depends on what this change removes".
func Check(cur Prefs, c Change, flows []Flow) core.Guard

// IsTailscaleAddr reports whether a is inside 100.64.0.0/10 or fd7a:115c:a1e0::/48 (after Unmap).
func IsTailscaleAddr(a netip.Addr) bool
```

### Core host join (from Task 7)

Package `internal/core`, file `fleet.go` (additive; schemas of `core.host.create`, `core.host.token` and `core.host.enroll-local` regenerate):

```go
// HostCreateInput gains, after ControllerAddress:
TailnetJoin bool     `json:"tailnetJoin,omitempty" jsonschema_description:"Join the new host to the tailnet before it enrolls: the answer carries a dpb2. bootstrap bundle instead of only the token."`
Tags        []string `json:"tags,omitempty" jsonschema:"maxItems=16,uniqueItems=true" jsonschema_description:"Tailscale tags of the new node, each tag:<name>; only with tailnetJoin, and each within the tailnet account's tagsAllowed."`
// Each tag matches ^tag:[a-z0-9][a-z0-9-]{0,62}$ (the items' jsonschema pattern).

// EnrollmentGrant gains, after ExpiresAt:
Bundle string `json:"bundle,omitempty" secret:"true" jsonschema_description:"Single-use dpb2. bootstrap bundle holding the token and a tailnet auth key; set only when tailnetJoin was asked; shown only once."`

// EnrollJoiner is the optional controller-side part of a module that joins a new host to a network before it
// enrolls (the tailnet module, spec 4.2), found on a Module by type assertion.
type EnrollJoiner interface {
	// PlanJoin checks req without writing: the account is connected, every tag is within tagsAllowed. It returns
	// the diff lines the host create plan shows and a state string that goes into the plan's StateHash.
	PlanJoin(ctx context.Context, env Env, req JoinRequest) (diff []DiffLine, state string, err error)
	// PrepareJoin creates the one-use auth key for req.HostID, recording its key row before the provider call.
	PrepareJoin(ctx context.Context, env Env, req JoinRequest) (authKey string, err error)
}

// JoinRequest is one host's join.
type JoinRequest struct {
	HostID  string    // "" in PlanJoin: the id is chosen at apply
	Tags    []string
	Expires time.Time // the enrollment token's expiry; the key expires with it
}
```

### Host create join (from Task 9)

Package `internal/controller/coreactions`. `hostCreate` (hostenroll.go) with `TailnetJoin`: plan refuses `Tags` without `TailnetJoin` (`request.invalid_input`, field `/tags`); plan and apply find the one `core.EnrollJoiner` among `Deps.Modules` that is enabled on the controller (none: `request.invalid_input`, "tailnet join needs the tailnet module"); the controller tailnet address is `controllerTailnetAddr(ctx, db) (string, error)` in the new file `hostjoin.go`: the first IPv4 address, else the first IPv6 address in brackets, of `listen.LoadTailscaleAddrs`, joined with `listen.AgentPort`; none saved → `request.invalid_input`, "the controller is not on a tailnet". With a join, the token's address is that tailnet address (whatever `ControllerAddress` says, which is then refused if also given: `request.invalid_input`, field `/controllerAddress`), apply chooses the host id first, calls `PrepareJoin` before the host write, and the grant's `Bundle` is `transport.Bundle{Token, TailnetKey, ControllerTailnetAddr}.String()` while `Token` keeps the plain token. Verify is unchanged. The plan diff adds the joiner's lines and the line `+ bootstrap bundle: the token plus a one-use tailnet auth key for <tags or "no tags">, shown once`.

### Tailnet enroll keys (from Tasks 10, 18)

Package `internal/modules/tailnet`. Task 10 writes `join_enroll.go`: `module` implements `core.EnrollJoiner` (`func (m module) PlanJoin(...)`, `func (m module) PrepareJoin(...)`), through `func (t tn) planJoin(...)` and `func (t tn) prepareJoin(...)`. PrepareJoin inserts the `tailnet_keys` row `{description: "darkpanel-enroll-" + HostID, kind: "enroll", owner_id: HostID, state: "requested", expiry_seconds: 3600, tags_json}` in its own write transaction, then calls `api.CreateKey(KeyRequest{Description, Tags, Reusable: false, Ephemeral: false, Preauthorized: true, ExpirySeconds: 3600})`, then settles the row `live` with the key id (`settleKey`); on a provider failure it settles the row `deleted` when the error is `core.IsDefinite` (nothing was created) and `uncertain` otherwise (left for the sweep, spec 1.9's uncertain-create rule), and returns the error. Task 18 writes `join_sweep.go`: `func settleEnrollKeys(ctx context.Context, db *store.DB, now time.Time) error` moves each `enroll` row in `live` or `uncertain`: to `used` when `core_hosts` holds its owner in state `enrolled`; to `retired` when the owner row is gone, or its enrollment token (`core_enroll_tokens` for the owner) expired unconsumed, or the row is older than its expiry plus 15 minutes; and `keysweep.go`'s `sweep` calls it first in every pass.

### Tailscale wire shapes (Tasks 8, 14–16, 20–29; read by Tasks 5, 12, 13, 19, 26, 31, 33)

Package `internal/modules/tailscale`, file `wire.go`, written by Task 8 verbatim with `json` tags in lower camel case as named (and `jsonschema_description` on every field); no other task declares any of these:

```go
// Resource tailscale.status (R56).
type Status struct {
	Version       string   `json:"version"`
	BackendState  string   `json:"backendState"`  // NoState | NeedsLogin | NeedsMachineAuth | Stopped | Starting | Running
	Self          *Node    `json:"self"`          // null while logged out
	Tailnet       string   `json:"tailnet"`       // "" when none
	MagicDNSSuffix string  `json:"magicDNSSuffix"`
	PreferredDERP string   `json:"preferredDerp"` // region code of the node's home DERP; "" when none
	RunSSH        bool     `json:"runSSH"`
	Prefs         PrefsFields `json:"prefs"`      // the node's current preferences, all seven fields set (prefsFieldsOf)
	Peers         []Peer   `json:"peers"`         // sorted by DNSName, then ID
	Health        []string `json:"health"`
}
type Node struct {
	ID        string   `json:"id"`
	HostName  string   `json:"hostName"`
	DNSName   string   `json:"dnsName"`   // without the trailing dot
	OS        string   `json:"os"`
	Addresses []string `json:"addresses"` // netip.Addr.String(), IPv4 first
	Tags      []string `json:"tags"`
}
type Peer struct {
	Node
	Online         bool       `json:"online"`
	Connection     string     `json:"connection"` // "direct" | "derp" | "peer-relay" | "none"
	Endpoint       string     `json:"endpoint"`   // direct: the UDP endpoint in use; "" otherwise
	Region         string     `json:"region"`     // derp: the DERP region code; "" otherwise
	ExitNode       bool       `json:"exitNode"`
	ExitNodeOption bool       `json:"exitNodeOption"`
	LastHandshake  *time.Time `json:"lastHandshake"`
}

// Resource tailscale.latency.
type Latency struct {
	Regions []RegionLatency `json:"regions"` // sorted by RegionID
}
type RegionLatency struct {
	RegionID int         `json:"regionId"`
	Code     string      `json:"code"`
	Name     string      `json:"name"`
	STUN     Measurement `json:"stun"`  // STUN latency
	HTTPS    Measurement `json:"https"` // DERP HTTPS probe latency
}
type Measurement struct {
	Available bool    `json:"available"`
	MedianMs  float64 `json:"medianMs"` // 0 when unavailable
	Reason    string  `json:"reason"`   // "" when available
}

// Preferences: a masked set of fields; nil means "not set" (unchanged).
type PrefsFields struct {
	WantRunning       *bool     `json:"wantRunning,omitempty"`
	AdvertiseRoutes   *[]string `json:"advertiseRoutes,omitempty"` // prefixes, never 0.0.0.0/0 or ::/0
	ExitNode          *string   `json:"exitNode,omitempty"`        // a node's stable ID or Tailscale IP; "" clears
	AdvertiseExitNode *bool     `json:"advertiseExitNode,omitempty"`
	AcceptRoutes      *bool     `json:"acceptRoutes,omitempty"`
	ShieldsUp         *bool     `json:"shieldsUp,omitempty"`
	RunSSH            *bool     `json:"runSSH,omitempty"`
}
type PrefsSetInput struct { // tailscale.prefs.set
	core.Guarded
	PrefsFields
}
type PrefsResult struct { // output of tailscale.prefs.set
	Prefs PrefsFields `json:"prefs"` // the values now, for exactly the fields the input set
}
type PrefsRepairInput struct { // tailscale.prefs.repair
	core.Guarded
	Outcome string `json:"outcome,omitempty"` // "restore" (default) | "keep"
}
type PrefsRepaired struct {
	RepairOutcome string      `json:"repairOutcome"` // "restored" | "kept"
	Prefs         PrefsFields `json:"prefs"`
}

type LoginInput struct { // tailscale.node.login
	core.Guarded
	AuthKey       string   `json:"authKey" secret:"true"`
	Hostname      string   `json:"hostname,omitempty"`
	AdvertiseTags []string `json:"advertiseTags,omitempty"` // each tag:<name>
	Relogin       bool     `json:"relogin,omitempty"`
}
type LoginResult struct {
	BackendState string   `json:"backendState"`
	DNSName      string   `json:"dnsName"`
	Addresses    []string `json:"addresses"`
}
type LogoutInput struct{ core.Guarded } // tailscale.node.logout
type LogoutResult struct {
	BackendState string `json:"backendState"`
}

type PackageInstallInput struct{} // tailscale.package.install
type PackageInstalled struct {
	Version string `json:"version"`
}
type PackageUpdateInput struct{} // tailscale.package.update
type PackageUpdated struct {
	From string `json:"from"`
	To   string `json:"to"`
}
type PinInput struct { // tailscale.package.pin
	Version string `json:"version"` // ^[0-9][0-9A-Za-z.+~-]{0,63}$
}
type Pinned struct {
	Version      string `json:"version"`
	PriorVersion string `json:"priorVersion"`
	PriorHeld    bool   `json:"priorHeld"`
}
type UnpinInput struct{} // tailscale.package.unpin: reverses the newest pin in phase done
type Unpinned struct {
	Version string `json:"version"` // installed version, unchanged
	Held    bool   `json:"held"`    // the hold state now: the pin's prior hold state
}

type CertGetInput struct{} // tailscale.cert.get (Internal)
type CertPair struct {
	Name     string    `json:"name"`    // the node's MagicDNS name, without the trailing dot
	CertPEM  string    `json:"certPem"`
	KeyPEM   string    `json:"keyPem" secret:"true"`
	NotAfter time.Time `json:"notAfter"`
}
```

Action specs, all `Version: 1`, side agent, `LockKeys: []string{"tailscale"}` for every mutation:

| Action | Class / constructor | Guards | Other |
|---|---|---|---|
| `tailscale.prefs.set` | revertible, `DefineRevertible` | `tailscale.link` (overridable) | `RepairedBy: "tailscale.prefs.repair"`; `Available: t.available` |
| `tailscale.prefs.repair` | revertible, `DefineRevertible` | `tailscale.link` | `RepairedBy: "tailscale.prefs.repair"`; `Available: t.available` |
| `tailscale.node.login` | compensable, `DefineMutating` with an `Undoer` | `tailscale.link` | `Available: t.available` |
| `tailscale.node.logout` | irreversible, `DefineMutating`, confirmation the host name (`os.Hostname`) | `tailscale.link` | `Available: t.available` |
| `tailscale.package.install` | compensable task, `DefineTask` with an `Undoer` | — | `LongRunning`; `ApplyTimeout: 15 * time.Minute`; no `Available` |
| `tailscale.package.update` | irreversible task, `DefineTask`, confirmation the host name | — | `ApplyTimeout: 15 * time.Minute`; `Available: t.available` |
| `tailscale.package.pin` | compensable task, `DefineTask` with an `Undoer` and a `Recoverer` | — | `ApplyTimeout: 20 * time.Minute`; `Available: t.available` |
| `tailscale.package.unpin` | compensable task, `DefineTask` with an `Undoer` | — | `Converges: true`; `ApplyTimeout: 5 * time.Minute`; `Available: t.available` |
| `tailscale.cert.get` | read, `DefineInternalRead`, `Internal: true` | — | identity `"tailscale certificate for <name>"` |
| resources `tailscale.status`, `tailscale.latency` | `DefineResource` | — | answer `tailscale.unavailable` when the socket does not answer |

### Tailscale constructors (from Tasks 14-16, 20-24, 27, 28)

One method on `ts` per action or resource, each in its task's file, each returning the declaration only (no socket, file or exec is touched while constructing). Task 31 calls exactly these and declares no other:

| Method | Declares | File | Task |
|---|---|---|---|
| `func (t ts) statusResource() core.ResourceDef` | `tailscale.status` | `status.go` | 14 |
| `func (t ts) latencyResource() core.ResourceDef` | `tailscale.latency` | `latency.go` | 15 |
| `func (t ts) certGet() core.ActionDef` | `tailscale.cert.get/v1` | `cert_get.go` | 16 |
| `func (t ts) packageInstall() core.ActionDef` | `tailscale.package.install/v1` | `package_install.go` | 20 |
| `func (t ts) prefsSet() core.ActionDef` | `tailscale.prefs.set/v1` | `prefs_set.go` | 21 |
| `func (t ts) nodeLogin() core.ActionDef`, `func (t ts) nodeLogout() core.ActionDef` | `tailscale.node.login/v1`, `tailscale.node.logout/v1` | `node_login.go`, `node_logout.go` | 22 |
| `func (t ts) packageUpdate() core.ActionDef` | `tailscale.package.update/v1` | `package_update.go` | 23 |
| `func (t ts) packagePin() core.ActionDef` | `tailscale.package.pin/v1` | `package_pin.go` (Task 29 adds `package_pin_recover.go`) | 24 |
| `func (t ts) prefsRepair() core.ActionDef` | `tailscale.prefs.repair/v1` | `prefs_repair.go` | 27 |
| `func (t ts) packageUnpin() core.ActionDef` | `tailscale.package.unpin/v1` | `package_unpin.go` | 28 |

Task 25's uninstall step is not a constructor: `module` itself implements `core.Uninstaller` (Contracts "Tailscale uninstall"), which the core finds by type assertion, so Task 31 does not wire it.

### Tailscale preferences (from Tasks 14, 21)

Task 14 writes, in `status.go`, the one conversion every preferences task uses, and fills `Status.Prefs` with it from `localAPI.Prefs`:

```go
// prefsFieldsOf returns all seven fields of p set (no nil pointer): wantRunning from WantRunning; acceptRoutes
// from RouteAll; shieldsUp; runSSH; exitNode from ExitNodeID when non-empty, else ExitNodeIP.String() when the
// address is valid, else ""; advertiseRoutes from the AdvertiseRoutes entries other than 0.0.0.0/0 and ::/0,
// sorted with netip.Prefix.Compare, without duplicates, as strings, a non-nil empty slice when there are none;
// advertiseExitNode true only when both default prefixes are advertised.
func prefsFieldsOf(p *tslocal.Prefs) PrefsFields
```

Task 21 writes, in `prefs_set.go`, these helpers, which Task 27 calls and does not redeclare:

```go
const (
	prefsEffectResource = "tailscale.prefs:node" // the one modify effect of prefs.set and prefs.repair
	prefsCommitWait     = 2 * time.Minute
)
// prefsSetPick keeps of all exactly the fields mask sets.
func prefsSetPick(all, mask PrefsFields) PrefsFields
// prefsSetNormalize validates and canonicalises a masked set (Task 21 Step 3 lists its refusals).
func prefsSetNormalize(f PrefsFields) (PrefsFields, error)
// prefsSetPatch is the tslocal patch that sets exactly the fields want sets, given the current preferences.
func prefsSetPatch(cur *tslocal.Prefs, want PrefsFields) (tslocal.PrefsPatch, error)
// prefsSetLinkAfter is the node's preferences after want applies, as the guard reads them: a copy of lp with the
// masked fields applied, ExitNode replaced only when want's exitNode differs from cur's (cur is prefsFieldsOf of
// the current preferences).
func prefsSetLinkAfter(lp link.Prefs, cur, want PrefsFields) link.Prefs
// prefsSetHandler is prefs.set's handler; its value methods Revert and Observe work on any change whose snapshot
// is the JSON of PrefsFields (the previous values of exactly the fields it sets) and whose effects are the one
// modify effect on prefsEffectResource with Before.State the previous values and Post the values after.
type prefsSetHandler struct{ t ts }
func (h prefsSetHandler) Revert(ctx context.Context, env core.Env, snapshot []byte) (core.RevertOutcome, error)
func (h prefsSetHandler) Observe(ctx context.Context, env core.Env, e core.Effect) (core.Observation, error)
```

### Tailscale prefs repair (from Task 27)

`tailscale.prefs.repair` keeps prefs.set's snapshot and effect shapes (Contracts "Tailscale preferences"), so its `Revert` and `Observe` are `prefsSetHandler{t}.Revert` and `.Observe`. It reads the repaired change itself, in `prefs_repair.go`:

```go
// prefsRepairTarget reads change env.Call.RepairOf from core_changes on env.DB.Read()
// (SELECT action, input_json, snapshot_ct FROM core_changes WHERE id = ?) and opens the snapshot with
// env.Sealer.Open("core_changes.snapshot_ct."+id, ct), the associated data the engine seals it with
// (internal/core/lifecycle/apply.go snapshotAD). prior is the decoded snapshot (PrefsFields). For a
// tailscale.prefs.set change, set is prefsSetNormalize of its input's PrefsFields; for a tailscale.prefs.repair
// change, set is the zero PrefsFields (a repair's input names no preferences).
func prefsRepairTarget(ctx context.Context, env core.Env) (action string, prior, set PrefsFields, err error)
```

Refusals, each `request.invalid_input` marked `core.Definite`: no `RepairOf` ("tailscale.prefs.repair runs only as the repair of a change"); an outcome other than `restore`, `keep` or empty (field `/outcome`); `keep` of a repaired `tailscale.prefs.repair` change (field `/outcome`, "change <id> is a repair; repair it with outcome restore"). The target is `prior` for `restore` and `set` for `keep`; the patch covers exactly the target's fields.

### Tailscale internals (from Task 8)

Package `internal/modules/tailscale`. Task 8 writes `deps.go`, `wire.go`, `store.go`, `preflight.go`, `migrations/agent/0001_tailscale.sql`, and changes `tailscale.go` to hold `type module struct{ t ts }` and `Module() core.Module { return module{t: productionTS()} }` (its manifest still declares nothing until Task 31):

```go
const (
	lockKey        = "tailscale"
	tailscaledUnit = "tailscaled.service"
	controllerUnit = "darkpanel-controller.service"
	keyringPath    = "/usr/share/keyrings/tailscale-archive-keyring.gpg"
	sourcesPath    = "/etc/apt/sources.list.d/tailscale.list"
	debDir         = platform.TailscaleDebDir // "/var/lib/darkpanel/agent/tailscale-debs"
	pinCheckWindow = 120 * time.Second
	joinWait       = 60 * time.Second
)
type ts struct{ d deps }
type deps struct {
	local     func() localAPI                                                   // production: a tslocal.Client on tslocal.DefaultSocket
	link      linkHost                                                          // production: liveLinkHost{} over internal/platform/netstat
	stun      func(ctx context.Context, server netip.AddrPort) (time.Duration, error) // production: stun.Probe
	https     func(ctx context.Context, url string) (time.Duration, error)      // production: one GET with a 5 s timeout, TLS verified, body discarded
	osRelease func() (osInfo, error)                                            // production: parse /etc/os-release
	arch      string                                                            // production: runtime.GOARCH
	hostname  func() (string, error)                                            // production: os.Hostname
	readFile  func(name string) ([]byte, error)                                 // production: os.ReadFile; /var/lib/dpkg/status, /etc/os-release, /var/cache/apt/archives/*.deb, staged files
}
type localAPI interface {
	Status(ctx context.Context) (*tslocal.Status, error)
	Prefs(ctx context.Context) (*tslocal.Prefs, error)
	EditPrefs(ctx context.Context, p tslocal.PrefsPatch) (*tslocal.Prefs, error)
	Start(ctx context.Context, o tslocal.StartOptions) error
	Logout(ctx context.Context) error
	DERPMap(ctx context.Context) (*tslocal.DERPMap, error)
	CertPair(ctx context.Context, domain string) (certPEM, keyPEM []byte, err error)
}
type linkHost interface {
	Sockets() ([]netstat.Socket, error)
	Interfaces() ([]netstat.Interface, error)
	RouteGet(q netstat.RouteQuery) (netstat.Route, error)
	PID() int
}
type osInfo struct{ ID, Codename string } // ID "ubuntu" | "debian"; Codename "jammy" | "noble" | "bookworm" | "trixie"
func productionTS() ts
// available is the per-action availability: nil when tailscaled answers Status within 5 s, else
// errcode tailscale.unavailable with the reason "tailscaled does not answer on its socket".
func (t ts) available(ctx context.Context, env core.Env) error
// preflight checks only the OS: osRelease succeeds with a supported (ID, Codename) and arch is amd64 or arm64.
func (t ts) preflight() []core.PreflightCheck
func agentMigrations() fs.FS
func ownedPaths() []core.OwnedPath // keyringPath and sourcesPath kind file; debDir kind managed-dir, Pattern ^tailscale_[0-9][0-9A-Za-z.+~-]{0,63}_(amd64|arm64)\.deb$
func externalUnits() []core.ExternalUnit // {tailscaled.service: state, restart}, {darkpanel-controller.service: state}
```

`migrations/agent/0001_tailscale.sql` creates, `STRICT`, with times as fixed-width RFC 3339 UTC text:

```sql
-- One row per tailscale.package.pin change: its agent-local transaction (spec 4.1).
CREATE TABLE tailscale_pins (
    change_id      TEXT PRIMARY KEY,
    version        TEXT NOT NULL,
    arch           TEXT NOT NULL,
    prior_version  TEXT NOT NULL,
    prior_held     INTEGER NOT NULL CHECK (prior_held IN (0, 1)),
    prior_backend  TEXT NOT NULL,
    link_tailnet   INTEGER NOT NULL CHECK (link_tailnet IN (0, 1)),
    phase          TEXT NOT NULL CHECK (phase IN ('recorded', 'unheld', 'staged', 'installed', 'held', 'checking', 'done', 'rolling_back', 'rolled_back', 'failed')),
    staged_deb     TEXT NOT NULL DEFAULT '',
    staged_sha256  TEXT NOT NULL DEFAULT '',
    unpinned_by    TEXT NOT NULL DEFAULT '',
    updated_at     TEXT NOT NULL
) STRICT;
-- Every step a pin or its rollback took, in order.
CREATE TABLE tailscale_pin_steps (
    change_id  TEXT NOT NULL REFERENCES tailscale_pins (change_id),
    seq        INTEGER NOT NULL,
    step       TEXT NOT NULL,
    result     TEXT NOT NULL,
    at         TEXT NOT NULL,
    PRIMARY KEY (change_id, seq)
) STRICT;
-- The package installs Darkpanel made, so uninstall removes only a package it installed.
CREATE TABLE tailscale_installs (
    change_id    TEXT PRIMARY KEY,
    version      TEXT NOT NULL,
    installed_at TEXT NOT NULL,
    removed_at   TEXT NOT NULL DEFAULT ''
) STRICT;
```

`staged_deb` holds the staged prior package's flat file name `tailscale_<prior version>_<arch>.deb` (the file is `debDir + "/" + name`; `rollback` also accepts a full path inside `debDir`), and `staged_sha256` its SHA-256 as 64 lower-case hex digits; both are `''` until phase `staged` completes.

`store.go` declares `type pinRow struct{ ChangeID, Version, Arch, PriorVersion string; PriorHeld bool; PriorBackend string; LinkTailnet bool; Phase, StagedDeb, StagedSHA256, UnpinnedBy string; UpdatedAt time.Time }` and `insertPin(ctx, tx, pinRow) error`, `loadPin(ctx, q queryer, changeID string) (pinRow, bool, error)`, `setPinPhase(ctx, db *store.DB, changeID, phase string, at time.Time) error`, `setPinStaged(ctx, db *store.DB, changeID, deb, sha string, at time.Time) error`, `addPinStep(ctx, db *store.DB, changeID, step, result string, at time.Time) error`, `activePin(ctx, q queryer) (pinRow, bool, error)` (the newest row in phase `done` with an empty `unpinned_by`), `setPinUnpinned(ctx, db *store.DB, changeID, by string, at time.Time) error`, `insertInstall(ctx, tx, changeID, version string, at time.Time) error`, `liveInstall(ctx, q queryer) (changeID, version string, ok bool, err error)`, `setInstallRemoved(ctx, db *store.DB, changeID string, at time.Time) error`, with `type queryer interface { QueryRowContext(...) *sql.Row; QueryContext(...) (*sql.Rows, error) }`.

Shared host helpers that later tasks call, also in Task 8's files:

```go
// installedVersion reads the installed tailscale version from dpkg's status file /var/lib/dpkg/status through
// t.d.readFile (the stanza "Package: tailscale" with "Status: install ok installed"); "" when not installed.
// FileWriter has no read method and dpkg-query is not allowlisted.
func (t ts) installedVersion(ctx context.Context, env core.Env) (string, error)
// held reports whether tailscale is held: `apt-mark showhold` lists it.
func (t ts) held(ctx context.Context, env core.Env) (bool, error)
// run runs one allowlisted vector through env.Exec under ctx and turns a non-zero exit into an error naming
// path, the first argument and the last line of stderr.
func run(ctx context.Context, env core.Env, path string, args ...string) (core.CmdResult, error)
```

### Tailscale test host (from Task 8)

`internal/modules/tailscale/testhost_test.go` and `fake_local_test.go`, used by every module-root test task:

```go
type tsHost struct {
	t       *testing.T
	local   *tsLocal            // the fake local API
	link    *tsLink             // the fake socket table, interfaces and routes
	units   *tsUnits            // records State and Restart calls; State answers from a map
	files   map[string][]byte   // what Files wrote, by path (managed files as dir + "/" + name)
	reads   map[string][]byte   // what deps.readFile answers, by path (/var/lib/dpkg/status, /etc/os-release, .deb files); a missing path is fs.ErrNotExist
	ran     [][]string          // every exec vector run: path then args
	scripts map[string][]tsRun  // queued answers per "path arg0 arg1 ..." key, consumed in order; unscripted runs answer exit 0
	db      *store.DB           // an agent database with the module's migrations applied
	journal *tsJournal          // an in-memory core.EffectJournal
	clock   *coretest.FakeClock
}
type tsRun struct {
	res core.CmdResult
	err error
}
func newTSHost(t *testing.T) *tsHost
func (h *tsHost) ts() ts                          // a ts whose deps all point at the host's fakes
func (h *tsHost) env(changeID string) core.Env    // HostID "h1", the host's DB, Exec, Files, Units, Clock, Effects (when changeID != "")
func (h *tsHost) script(vector string, runs ...tsRun) // vector: "path arg0 arg1 ..."
type tsLocal struct {
	Status  *tslocal.Status
	Prefs   *tslocal.Prefs
	DERP    *tslocal.DERPMap
	Cert    [2][]byte
	Errs    map[string]error // by method name: "Status", "Prefs", "EditPrefs", "Start", "Logout", "DERPMap", "CertPair"
	Edits   []tslocal.PrefsPatch
	Starts  []tslocal.StartOptions
	Logouts int
}
type tsLink struct {
	Sockets []netstat.Socket
	Ifaces  []netstat.Interface
	Routes  map[netip.Addr]netstat.Route // RouteGet answers by destination; a missing entry is netstat.ErrNoRoute
	Pid     int
}
type tsUnits struct{ States map[string]core.UnitState; Restarts []string }
```

`EditPrefs` on `tsLocal` applies the patch to `Prefs` and returns it. `tsUnits` implements `core.Units`, refusing every unit other than `tailscaled.service` and `darkpanel-controller.service`.

Go refuses a field and a method of one name, so `tsLocal` and `tsLink` do not implement `localAPI` and `linkHost` themselves: Task 8 adds the adapters `thLocal{*tsLocal}` and `thLink{*tsLink}`, which `h.ts()` hands to the deps. Later tasks read and write the fields (`h.local.Status`, `h.link.Sockets`, ...) and never name the adapters. Task 8 also adds `(*tsLocal).update(f func())`, the field `tsLocal.CertCalls []string`, `thDpkgStatus(version string) []byte` and the constants `thHostname`, `thDpkgPath`, `thOSReleasePath`.

Task 24 adds, in `testhost_test.go`, the fake of `deps.session` (Contracts "Tailscale pin"):

```go
type tsSession struct {
	Answers []tsSessionAnswer // call i answers Answers[min(i, len(Answers)-1)]; none answers {Up: true}
	Calls   int
}
type tsSessionAnswer struct {
	Up, ViaTailnet bool
	Err            error
}
// tsHost gains the field: session *tsSession (newTSHost sets &tsSession{}; h.ts() wires d.session to it)
```

### Tailscale shared plan helpers (from Tasks 17, 20)

Task 17 writes `linkguard.go`:

```go
// linkGuard gathers this host's protected flows (spec 2.3: SSH sessions, the agent's own session to the
// controller, a local controller's sessions and listeners, and the guard context's panel client, agent peers and
// listeners) through t.d.link and env.Units' controllerUnit state, classifies each with a route lookup, marks the
// request flows, and returns link.Check's guard for c, honouring in.Overrides (an overridden failing guard reports
// OK true, Overridden true). in.Guard is the plan's guard context; env.Call.GuardRefresh is added at apply.
func (t ts) linkGuard(ctx context.Context, env core.Env, in core.Guarded, cur link.Prefs, c link.Change) (core.Guard, error)
// currentLinkPrefs reads the node's preferences as link.Prefs.
func (t ts) currentLinkPrefs(ctx context.Context) (link.Prefs, *tslocal.Prefs, error)
```

Request flows: with a guard context whose `Client` is set and a controller on this host, the panel flow from `Client`; with a guard context and the controller elsewhere, the agent link; with no guard context (break-glass and system plans), every SSH flow.

Task 20 writes `repo.go` and `package_install.go`:

```go
// repoFiles renders the keyring (the bytes embedded from repo_keyring.gpg, whose fingerprint Task 1 records)
// and the source list line "deb [signed-by=/usr/share/keyrings/tailscale-archive-keyring.gpg] https://pkgs.tailscale.com/stable/<ID> <Codename> main\n"
// for this host, or refuses an unsupported OS or architecture.
func (t ts) repoFiles() (keyring, sources []byte, err error)
// ensureRepo writes the two files when absent (recording them as owned), uses them unrecorded when byte-identical,
// and refuses with tailscale.repo_conflict when either differs; written reports what it wrote.
func (t ts) ensureRepo(ctx context.Context, env core.Env) (written []string, err error)
// aptInstall runs APT-1 then APT-2 under ctx.
func aptInstall(ctx context.Context, env core.Env) error
```

### Tailscale pin (from Tasks 24, 29)

Task 24 adds to `deps` (in `deps.go`, and to `productionTS`) the agent's controller session as the pin's transient unit sees it, from outside the agent process:

```go
	// production: agentSession in pin_session.go: daemon.LoadCredentials(paths.Production().AgentTLSDir) gives the
	// controller address (not enrolled: up false, viaTailnet false, nil error); viaTailnet is link.IsTailscaleAddr of
	// its host when that host is an IP literal; up is Hello's ControllerConnected (non-nil and true) through
	// api.NewClient(api.ClientConfig{Dial: a unix dial of paths.Production().AgentSocket}), bounded by 5 s.
	session func(ctx context.Context) (up, viaTailnet bool, err error)
```

Task 24 writes `package_pin.go` with the handler `type packagePinHandler struct{ t ts }` (value receivers) holding `Plan`, `Task` and `Verify`, and the rollback that the failed-check path calls and Task 29's `Inverse` and `Recover` call:

```go
// rollback restores the prior version and hold state of pin p, each step journaled with addPinStep: set phase
// rolling_back; "rollback-unhold" (apt-mark unhold tailscale, only when held); "rollback-configure"
// (dpkg --configure -a, only when p.Phase is installed, i.e. phase 4 may have been interrupted);
// "rollback-install" (APT-5 with p.StagedDeb after checking its SHA-256 against p.StagedSHA256);
// "rollback-hold" (apt-mark hold tailscale, only when p.PriorHeld); "rollback-restart" (env.Units restart
// tailscaled.service); "rollback-verify" (installedVersion == p.PriorVersion, held == p.PriorHeld, and the backend
// state back to p.PriorBackend within pinCheckWindow). It ends in phase rolled_back, or failed with the error of the
// first failing step (which it returns). A pin row in phase recorded changed nothing: rollback sets rolled_back and
// runs no step.
func (h packagePinHandler) rollback(ctx context.Context, env core.Env, p pinRow) error
```

Forward steps, recorded with `addPinStep` after each runs, results `ok` or `failed: <reason>`: `unhold`, `stage-prior`, `stage-target`, `copy-prior`, `install`, `hold`, `check`. Phase `recorded` stores `link_tailnet` from `deps.session`'s `viaTailnet` and `prior_backend` from `Status`; phase 6 (`checking`) requires, within `pinCheckWindow` on `env.Clock`, tailscaled answering, the backend state equal to `prior_backend`, and, when `link_tailnet`, `deps.session` answering `up`.

The pin journals one effect before its first host change (phase `unheld`): `{Kind: core.EffectModify, Resource: "tailscale.package:tailscale", Local: true, InverseLocal: true, Before: {State: {"version": <prior>, "held": <prior held>}}, After: &{State: {"version": <version>, "held": true}}}`; `Made` at phase `done` with that After as `Post`; `Skipped` after a rollback that verified (the host is as before). A failed check returns `tailscale.pin_failed` after the rollback; a prior version that cannot be staged returns `tailscale.pin_failed` after restoring the prior hold state (phase `rolled_back`).

Task 29 writes `package_pin_recover.go`: `Observe` (for `tailscale.package:tailscale`: `{"version": installedVersion, "held": held}`, `Matches` 1, `Exclusive` true), `Inverse` (loads the pin by `e.ChangeID`, runs `rollback`, `InverseResult{}`), and `Recover`. The engine calls `Recover` only once the apply unit has ended (it waits for a running unit, 1.3c). By the pin row's phase: none or `recorded` → `RecoveryRolledBack`; `done` → `RecoveryCompleted` with the `Pinned` output; `rolled_back` → `RecoveryRolledBack`; `failed` → `RecoveryNeedsRepair` with the last step's result; `unheld` through `checking`, or `rolling_back` → `rollback`, then `RecoveryRolledBack`, or `RecoveryNeedsRepair` with its error.

Task 28's unpin journals `{Kind: core.EffectModify, Resource: "tailscale.package:tailscale", Local: true, InverseLocal: true, Before: {State: {"held": true}}, After: &{State: {"held": <the pin's prior_held>}}}`; its undo re-holds.

### Tailscale uninstall (from Task 25)

Package `internal/modules/tailscale`, file `uninstall.go`: `module` implements `core.Uninstaller`:

```go
// UninstallOptions is one option: {Key: "package", Values: ["keep", "remove"], Default: "keep",
// Help: "remove the tailscale package if Darkpanel installed it"}.
func (m module) UninstallOptions() []core.UninstallOption
// Uninstall with package=remove and a liveInstall row: apt-mark unhold tailscale when held, APT-6, then
// setInstallRemoved; with keep, or no row, it changes nothing. It succeeds when run again on a host where it
// already ran. keepData does not change it (the module's tables are its data).
func (m module) Uninstall(ctx context.Context, env core.Env, keepData bool, opts map[string]string) error
```

### Tailscale installer join (from Task 26)

Package `internal/modules/tailscale`, file `bootstrap.go`, the installer's entry point (spec 4.2 step 4):

```go
// JoinDeps is what Join uses on the host being installed.
type JoinDeps struct {
	Exec   core.Runner     // the production allowlisted runner
	Files  core.FileWriter // the production file provider over the new agent.db
	DB     *store.DB       // the new agent.db, migrated with the module's migrations; Join writes its tailscale_installs row
	Clock  core.Clock
	Out    io.Writer       // progress lines
	Socket string          // "" is tslocal.DefaultSocket
}
// Join installs tailscale when it is absent (ensureRepo, then APT-1 and APT-2, recording a tailscale_installs row
// under change id "install"), logs in with authKey through the local API (Start with AuthKey and WantRunning), and
// waits up to joinWait for a Tailscale address; it returns the node's addresses. A failure names its step.
func Join(ctx context.Context, d JoinDeps, authKey string) ([]netip.Addr, error)
```

### Installer join (from Task 30)

Package `internal/install`: `Config` gains `Tailnet TailnetJoiner` (required only when the token is a bundle) with

```go
// TailnetJoiner joins this host to the tailnet before it enrolls (spec 4.2 step 4).
type TailnetJoiner interface {
	Join(ctx context.Context, authKey string) error
}
```

The agent role's install parses `Config.Token` with `transport.ParseBootstrap`; for a bundle it calls `Tailnet.Join` with `TailnetKey`, then enrolls with `bundle.Token.String()` (whose address is the controller's tailnet address); a join failure stops the install un-enrolled with "tailnet join failed: <reason>; rerun with a fresh bundle". `cmd/darkpanel/system.go` wires `tailscale.Join`. `daemon.Enroll` refuses a `dpb2.` bundle with `enroll.token_invalid` and the detail "this is a bootstrap bundle; install the agent with install.sh --role agent --token-file or darkpanel system install --role agent". `install.sh.in` (and the generated `install.sh`) name "enrollment token or bootstrap bundle" in its prompts, usage and errors and pass the value unchanged.

### Listen certificate source (from Task 6)

Package `internal/controller/listen`, file `tailscale_cert.go` (the stub's two functions keep their signatures; `sources.go` is frozen): `tailscaleCertSource(d SourceDeps) CertSource` returns a source that reads `tailscale.cert.get/v1` with `d.Dispatch.ReadInternal(ctx, core.Principal{Actor: "system:listen.cert", Scope: core.ScopeAdmin}, d.LocalHostID, ..., {})` (bounded by 10 s), caches the `CertPair` (JSON shape of Contracts "Tailscale wire shapes"), answers a handshake whose `ServerName` equals the pair's name (case-insensitive, trailing dot ignored) and falls through otherwise, and refreshes in the background once two thirds of the pair's life has passed; `tailscaleNames` returns the cached pair's name, refreshed with the same bounded read, lower-cased, without a trailing dot, and dropped when it is empty, an IP literal, non-ASCII or not a valid DNS name. Any read error falls through and is logged at debug level once per distinct error. Because the frozen `sources.go` passes only `SourceDeps`, the certificate cache is a package-level `sync.Map` keyed by dispatcher and host id. Task 6 also adds `ReadInternalFunc` to `internal/core/coretest/dispatcher.go`, and Task 7 edits `internal/controller/coreactions/registry_test.go` (`TestCoreActionSecrets` must know the grant's new secret `Bundle`).

### Web: tailscale (Tasks 5, 12, 13, 19)

`web/src/modules/tailscale/types.ts` (Task 5) exports `TAILSCALE_MODULE = "tailscale"`, `TAILSCALE_ACTION_VERSION = 1`, the TypeScript mirrors `TailscaleStatus`, `TailscaleNode`, `TailscalePeer`, `TailscaleLatency`, `RegionLatency`, `Measurement`, `PrefsFields` of Contracts "Tailscale wire shapes" (same JSON names; `TailscaleStatus.prefs` is optional in the mirror, `prefs?: PrefsFields`, and the guard refuses it only when present and not a plain object), and the guards `isTailscaleStatus(v: unknown): v is TailscaleStatus` and `isTailscaleLatency(v: unknown): v is TailscaleLatency`; `fixtures.ts` exports `statusRunning`, `statusLoggedOut`, `latencyFixture` (three regions, one with STUN unavailable); `tailscale-change.tsx` exports `TailscaleChange({hostId, action, title, input, onClose})`, the docker module's `DockerChange` shape with `TAILSCALE_*` constants and, for `tailscale.prefs.set`, the change dialog's revert-timeout field. Task 12 writes `status-panel.tsx` (`StatusPanel({hostId})`), `peers-table.tsx` (`PeersTable({peers})`) and `latency-panel.tsx` (`LatencyPanel({hostId})`). Task 13 writes `prefs-form.tsx` (`PrefsForm({hostId, status, prefs})`), `node-controls.tsx` (`NodeControls({hostId, status})`: login with auth key, logout) and `package-panel.tsx` (`PackagePanel({hostId, available})`: install when the module reports tailscaled unavailable, update otherwise). Task 19 writes `tailscale-page.tsx` (`TailscalePage({hostId})`, passing `prefs={status.prefs ?? {}}` to `PrefsForm`, including a "Join tailnet" button applying `tailnet.node.join` through the change dialog when the host is not on a tailnet) and replaces `routes.tsx`. The resources are read with the existing resource query of `web/src/api/client.ts`.

### Web: host create (from Task 11)

`web/src/modules/tailnet/host-create.tsx` renders a "Join the tailnet" checkbox and a tags input (comma-separated `tag:` names) and calls `onChange({tailnetJoin: true, tags})` or `onChange({})`; `web/src/pages/create-host-dialog.tsx` shows the grant's `bundle`, when present, in place of the token with the instruction "On the new host, run install.sh --role agent --token-file <file> with this bundle before <time>."

### Tailnet node join (from Task 32)

Package `internal/modules/tailnet`, file `join.go`: `tailnet.node.join/v1`, `DefineComposite`, `Unwind: reverse`, controller side, input `NodeJoinInput{HostID string json:"hostId"; Tags []string json:"tags,omitempty"; Install bool json:"install,omitempty"}`, output `NodeJoined{BackendState string; Addresses []string}`, steps: `install` (`tailscale.package.install/v1` on the host, only when `Install`), `key` (`tailnet.authkey.create/v1`: tags, not reusable, pre-authorized, expiry 3600), `login` (`tailscale.node.login/v1` on the host with `authKey: "${steps.key.output.key}"`, after `key` and `install`). `tailnet.go`'s `actions()` gains it; `schemas/tailnet.node.join/v1.json` is committed.

### Harness M5 (from Tasks 33–35)

`test/harness/m5_helpers_test.go` (Task 33): `m5Lab(t *testing.T) (*Lab, *VM)` boots one enrolled agent VM per OS through the wave 0 lab helpers; `m5Apply(t, vm, action string, input any) ChangeView` plans and applies through the controller API and confirms revertible changes; `m5Status(t, vm) tailscale.Status`. `TestM5Node` runs behind the flag `-m5` with its own workdir; Task 34 adds `TestM5Pin`; Task 35 adds the `vm-m5` job to `.github/workflows/harness.yml` (its own 100-minute limit) and runs `TestTailscaledLive` on each VM as a test binary compiled by the `build` job (`go test -c -o bin/harness/tslocal.test ./internal/platform/tslocal`).

## Assumptions (evidence)

- The `tailscale` module is a registered skeleton: `internal/modules/tailscale/tailscale.go` declares only `ID`, `module`, `Module()` and an empty `Manifest()`, with `TestManifestIsASkeleton`; `internal/cli/tailscale.go` returns no command; `web/src/modules/tailscale/routes.tsx` and `web/src/modules/tailnet/host-create.tsx` render nothing (read 2026-10-31). Every action and resource gets a generated CLI command (`internal/cli/input.go` `addInputFlags` turns `tailnetJoin` and `tags` into `--tailnet-join` and `--tags`), so no CLI file changes.
- `internal/platform/tslocal` already implements every local API call wave 4 uses (`Status`, `Prefs`, `EditPrefs`, `Start`, `Logout`, `DERPMap`, `CertPair`, `WhoIs`, `DebugDERPRegion`; `internal/platform/tslocal/client.go`, `derp.go`, read 2026-10-31), with `PeerStatus.CurAddr`, `Relay` and `PeerRelay`. Whether those fields mean "direct endpoint", "home DERP region code" and "peer relay in use" on tailscaled 1.82 to the pinned 1.104.1, whether `/localapi/v0/cert/<domain>?type=pair` is the pair endpoint, and whether the keyring at `https://pkgs.tailscale.com/stable/<os>/<codename>.noarmor.gpg` is one key for all four OS: unverified — Task 1 settles each and writes the result into spec 4.1 and 1.8.
- The exec allowlist already permits the six APT vectors, `apt-mark hold|unhold tailscale`, `apt-mark showhold` and `dpkg --configure -a`, built by `platform.AptUpdateTailscale` … `AptRemoveTailscale` and `platform.TailscaleDebDir` (`internal/platform/exec_rules.go`, read 2026-10-31); `exec_rules.go` is frozen for this plan.
- Task actions are supported end to end by the core: `core.DefineTask`, `ActionSpec.Task`, `core_task_runs`, the `darkpanel agent task <change> <op> <attempt>` subcommand (`cmd/darkpanel/agent.go`) and `Recoverer` (`internal/core/define.go`, `contract.go`, `internal/core/lifecycle/taskunit_test.go`, read 2026-10-31); no production module uses `DefineTask` yet. Unverified on a real host — Task 33's harness scenarios run `tailscale.package.install` and Task 34 runs the pin and its rollback in real transient units.
- The controller saves its Tailscale addresses in `core_meta` key `tailscale_addrs` and binds its agent listener on each at `listen.AgentPort` (`internal/controller/listen/tailscale.go` `LoadTailscaleAddrs`, `agent.go` `AgentEndpoints`, read 2026-10-31), so `controllerTailnetAddr` reads them without asking tailscaled.
- `tailnet_keys` already allows kind `enroll` and the sweep's namespace already matches `darkpanel-enroll-<ulid>` (`internal/modules/tailnet/migrations/controller/0001_tailnet.sql`, `keysweep.go` `keyNamespace`, read 2026-10-31); nothing yet moves an `enroll` row to `used` or `retired`.
- The composite engine carries a child's secret output to a later step through a `${steps.<id>.output.<field>}` reference from the private output and refuses a literal in a secret input field (`internal/core/lifecycle/composite.go` `secretRefsOnly`, `composite_run.go`, read 2026-10-31), so `tailnet.node.join` passes the created key to `tailscale.node.login` without it reaching a log or an effect.
- `internal/core/fleet.go`, `internal/controller/coreactions/**`, `internal/transport/token.go`, `internal/install/**`, `cmd/darkpanel/system.go`, `internal/agent/daemon/enroll.go`, `web/src/pages/create-host-dialog*` and `internal/modules/tailnet/keysweep.go` are not frozen (spec 1.12 "Not frozen" and "Parallel decomposition": a later wave may edit an earlier wave's files; wave 4's row names the host-create, bundle, installer and Hosts dialog files).
- Ruling: `core.host.create` reaches the tailnet module through the interface `core.EnrollJoiner` found on `Deps.Modules` by type assertion, as `ext.Controller` is found; no frozen file changes and no new wiring in `internal/controller/server` is needed.
- Ruling: with `tailnetJoin`, the token's address is the controller's tailnet agent address, so `daemon.Enroll` and the saved controller endpoint need no change; `controllerAddress` together with `tailnetJoin` is refused.
- Ruling: the enroll key's `used` and `retired` transitions are made by the tailnet key sweep reading `core_hosts` and `core_enroll_tokens` in the same controller database every pass (at most 5 minutes late), instead of hooks in enrollment and host deletion, which keeps the fleet code free of tailnet knowledge.
- Ruling: `tailnet.node.join`'s `install` step runs only when its input's `install` is true; a composite's `Steps` never reads a host (`core.Composer`), so the web join button sets it from the host's `tailscale` availability, and an `install: true` on a host with tailscale refuses at the child's plan with `tailscale.package_present`.
- Ruling: `tailscale.package.unpin` takes no input and reverses the newest pin in phase `done` (`activePin`); with none it plans an empty diff, which `Converges` requires for the forward composite `derp.server.remove`.
- Ruling: spec 4.1 says uninstall runs `APT-6` but 1.11 lists only the firewall's option. The tailscale module implements `core.Uninstaller` with one option `package` (values `keep`, `remove`; default `keep`; help "remove the tailscale package if Darkpanel installed it"); `remove` runs `APT-6` only when `liveInstall` finds a row, so an uninstall never removes a tailscaled the owner installed and never cuts a tailnet SSH session by default.
- Ruling: `tailscale.prefs.repair` reads the repaired change's input and sealed snapshot from `core_changes` (`input_json` holds the redacted input, and prefs.set's input has no secret field; `snapshot_ct` is sealed with `core_changes.snapshot_ct.<id>`, `internal/core/lifecycle/apply.go:23`, read 2026-11-01; `env.Sealer` is the agent key's sealer, `internal/agent/runtime/runtime.go:136`) instead of a module table of its own, so prefs.set stores nothing beyond its core snapshot. A repair's input names no preferences, so `keep` of a repaired repair is refused and `restore` is the way out.
- Ruling: `tailscale.status` carries the node's current preferences (`Status.Prefs`, all seven fields), because the web's preferences form needs them and no other read returns them; the conversion `prefsFieldsOf` belongs to Task 14 and prefs.set uses it.
- Ruling: the pin's phase 6 learns the agent's controller session from outside the agent process, because it runs in a transient unit: through the agent socket's `Hello` (`controllerConnected`, `internal/core/contract.go:1003`) and the saved controller address (`daemon.LoadCredentials`, `internal/agent/daemon/creds.go:118`); `internal/agent/daemon` and `internal/agent/api` do not import `internal/modules` (`go list -deps`, 2026-11-01), so the tailscale module may import them.
- Ruling: the engine waits for a running task unit before it calls a recovery handler (`internal/core/lifecycle/taskunit.go`, `taskproc.go`, read 2026-11-01), so the pin's `Recover` decides only by the pin row's phase.
- Ruling: the `tailscale.link` guard gathers its own flows through `internal/platform/netstat` (as the firewall does, owner ruling R3) instead of importing `internal/modules/firewall/guard`, whose flow builder is unexported and evaluates netfilter state the tailscale guard does not need; the module therefore declares `darkpanel-controller.service` with the `state` operation besides `tailscaled.service` with `state` and `restart`.
- Ruling: a peer's connection is `direct` when `CurAddr` is set (endpoint `CurAddr`), else `peer-relay` when `PeerRelay` is set, else `derp` when `Relay` is set (region `Relay`), else `none`; `preferredDerp` is `Self.Relay`. Task 1 confirms or amends this mapping (contract-check gate).
- Ruling: `tailscale.latency` reads the region list from `DERPMap`; a region's STUN endpoints are its nodes' `IPv4`/`IPv6` with `STUNPort` (default 3478, `-1` means none), and its HTTPS probe URL is `https://<node HostName>/derp/latency-check`. A logged-out node may answer an empty DERP map, which the resource reports as no regions; the harness checks the resource against whatever map tailscaled answers (spec Contracts: "the latency resource against the DERP map").
- The harness VMs have tailscaled 1.104.1 pre-installed and running but logged out (`test/harness/provision.go` lines 68–78, `images.json`, read 2026-10-31), and reach the internet (Task 59 of plan w3 pulled container images on them). `tailscale.package.install` is exercised after the scenario removes the package with `apt-get remove -y tailscale` over the VM's SSH; whether `pkgs.tailscale.com` is reachable from the VMs: unverified — Task 33's install scenario proves it, and fails naming the URL if not.
- The installer can build the production `core.Runner` and a `core.FileWriter` over the agent database it has just migrated, so `tailscale.Join` records the repository files as owned exactly as `tailscale.package.install` does: unverified — Task 26 builds `JoinDeps` from the platform constructors in a test over a temporary layout, and Task 30 wires the same construction in `cmd/darkpanel/system.go`; if the provider cannot run before the agent's first start, Task 26 stops and reports it.
- Every revertible change in the harness is confirmed or left to revert by the scenario itself; harness changes to `.github/workflows/harness.yml` happen only in Task 35, alone and last, as spec 1.12 requires of a wave's `harness.yml` change.

## Task index

1. Spike: local API endpoints, peer fields and the repository key
2. transport: the dpb2 bootstrap bundle
3. stun: hand-written STUN binding client
4. link: the tailscale.link guard rules
5. web: tailscale types, fixtures and change dialog
6. listen: Tailscale certificate source and MagicDNS name
7. core: host create join fields, bundle and EnrollJoiner
8. tailscale: module seam, wire types, store and test host
9. coreactions: core.host.create with a tailnet join
10. tailnet: enroll key creation
11. web: tailnet join in the Add host dialog
12. web: tailscale status, peers and latency panels
13. web: tailscale preferences, node and package controls
14. tailscale: status resource
15. tailscale: latency resource
16. tailscale: cert.get internal read
17. tailscale: link guard facts and the plan check
18. tailnet: enroll keys used and retired by the sweep
19. web: tailscale page and routes
20. tailscale: repository files and package.install
21. tailscale: prefs.set
22. tailscale: node.login and node.logout
23. tailscale: package.update
24. tailscale: package.pin transaction
25. tailscale: uninstall step
26. tailscale: installer join entry point
27. tailscale: prefs.repair
28. tailscale: package.unpin
29. tailscale: pin rollback, undo and recovery handler
30. install: join the tailnet from a dpb2 bundle
31. tailscale: register actions and resources
32. tailnet: node.join composite
33. harness: TestM5Node scenarios
34. harness: pin rollback with the controller link blocked
35. harness: TestTailscaledLive and the M5 workflow job

---

### Task 1: Spike: local API endpoints, peer fields and the repository key

**Files:**
- Modify: `docs/dforge/specs/2026-10-21-v1-completion-design.md`, three places and no other line:
  - section 1.8: one new bullet `**Recorded contract (tailscaled local API, checked YYYY-MM-DD).**` placed directly after the "Tailscale local client" bullet (before the "Socket table and routes" bullet);
  - section 4.1: one new bullet `**Recorded contract (tailscale node, checked YYYY-MM-DD).**` placed directly before the "Module preflight" bullet;
  - section 4.2: only the Join step 4 clause "log in through the local API with the key", which gains a pointer to the 1.8 entry "Login by key".
- Create: `internal/modules/tailscale/repo_keyring.gpg` (binary; Task 20 embeds it).
- Read only (frozen, spec 1.12): `internal/platform/tslocal/client.go`, `internal/platform/tslocal/derp.go`, `internal/platform/tslocal/live_test.go`.
- Test: none; the check script of Step 1 is the test. It lives in `$TMPDIR`, not in the repository.

**Interfaces:**
- Consumes: nothing from earlier tasks. Tailscale's public source files and the four repository keyring URLs named in Step 3, read over the network (only this task does; Global Constraints "Host safety"). Nothing is installed, no `tailscale` or `tailscaled` command runs, and `gpg` runs only with a `--homedir` under `$TMPDIR`.
- Produces: two spec bullets and one file that later tasks read as fixed facts. Task 8's `localAPI` interface and Tasks 6, 16, 22 and 26 call the endpoints as 1.8 "Endpoints", "Cert pair" and "Login by key" record them; Task 14 builds `Peer.Connection`, `Endpoint`, `Region`, `Node.ID` and `PreferredDERP` from 4.1 "Connection" and "Identifiers"; Task 15 builds its probes from 1.8 "Latency check"; Task 20 embeds `repo_keyring.gpg` and pins the fingerprint 4.1 "Keyring" records (Contracts "Tailscale shared plan helpers"). The two `Divergences` bullets gate the plan: after wave 1-3 merges, any entry other than `none` stops execution until the integrator amends Contracts and the tasks that state the old value (Global Constraints "Contract-check gate").

**Depends on:** none

This task changes no Go code. It settles the facts the plan's Assumptions mark "unverified — Task 1 settles each", and it checks the login sequence of a key join, which Tasks 22 and 26 rely on, so every later task builds on recorded behaviour instead of a guess. The facts are read from Tailscale's source at two tags: `v1.82.0`, the oldest tailscaled the spec supports (Versions table), and `v1.104.1`, the version the harness VMs run (`test/harness/images.json`).

Every divergence the task finds, in the spec bullets and in the report, is written in one fixed form, because the integrator acts on it mechanically:

`<Contracts section> — <plan value> → <provider value> — <source URL>`

for example `Tailscale wire shapes — Peer.Connection is "peer-relay" only when CurAddr is empty and PeerRelay is set → PeerStatus.CurAddr is also set while a peer relay carries the path — https://raw.githubusercontent.com/tailscale/tailscale/v1.104.1/ipn/ipnstate/ipnstate.go` (an illustration of the form, not a finding). The first field is the heading of the Contracts section that states the plan value and is one of `Tailscale wire shapes`, `Tailscale internals`, `Tailscale shared plan helpers`, `Tailscale installer join` or `Listen certificate source`; the separators are an em dash with spaces around it and an arrow; the URL is the file that states the provider value. One entry per difference, never two differences in one entry.

- [ ] **Step 1: Write the check**

Create `$TMPDIR/check-spike.sh` (after `mkdir -p /root/.cache/w4-tmp/1 && export TMPDIR=/root/.cache/w4-tmp/1 GOTOOLCHAIN=local`) with exactly this content. It verifies that the two recorded-contract bullets exist with every required label, that each `Divergences` bullet is `none` or is followed by entries in the fixed form, that 4.2 points at "Login by key", that the committed keyring file is the file the `Keyring` bullet describes (hash and every primary-key fingerprint, read with a throw-away `gpg` home directory) and equals the four downloads Step 3 saves, that no placeholder is left, and that the only files changed are the spec, with hunks only in sections 1.8, 4.1 and 4.2, and the keyring.

```bash
#!/usr/bin/env bash
set -u
spec=docs/dforge/specs/2026-10-21-v1-completion-design.md
keyring=internal/modules/tailscale/repo_keyring.gpg
docs=${TMPDIR:?}/docs
fail=0
bad() { echo "FAIL: $*"; fail=1; }

# section <start prefix> <end prefix>: the lines between two headings, exclusive.
section() { awk -v s="$1" -v e="$2" 'index($0,s)==1{f=1;next} index($0,e)==1{f=0} f' "$spec"; }
# bounds <start prefix> <end prefix>: first and last line number of that section, headings included.
bounds() { awk -v s="$1" -v e="$2" 'index($0,s)==1{a=NR} index($0,e)==1&&a{print a, NR-1; exit}' "$spec"; }

s18=$(section '#### 1.8 Host clients' '#### 1.9 ')
s41=$(section '#### 4.1 Node' '#### 4.2 ')
s42=$(section '#### 4.2 Tailnet' '#### 4.3 ')

need() { printf '%s\n' "$1" | grep -Eq -- "$3" || bad "$2 lacks: $3"; }

# entries <section text> <label>: the bullet with that label is "none" or is followed by nested entries of the form
# "<Contracts section> — <plan value> → <provider value> — <source URL>".
entries() {
  printf '%s\n' "$1" | awk -v label="$2" '
    function finish() { if (open && n == 0) { print "FAIL: " label " lists no entry and is not none"; rc = 1 } open = 0 }
    index($0, "**" label ":**") {
      finish()
      rest = substr($0, index($0, "**" label ":**") + length("**" label ":**"))
      if (rest ~ /^ *none\.? *$/) next
      if (rest ~ /^ *$/) { open = 1; n = 0; next }
      print "FAIL: " label " must be none or be followed by entries: " $0; rc = 1; next
    }
    open && /^ +- (Tailscale wire shapes|Tailscale internals|Tailscale shared plan helpers|Tailscale installer join|Listen certificate source) — .+ → .+ — https?:\/\/[^ ]+$/ { n++; next }
    open { finish() }
    END { finish(); exit rc }'
}

date_re='20[0-9]{2}-[0-9]{2}-[0-9]{2}'

need "$s18" "1.8" "Recorded contract \\(tailscaled local API, checked $date_re\\)"
for l in Sources Endpoints 'Masked prefs' 'Login by key' 'Cert pair' 'Latency check' Divergences; do
  need "$s18" "1.8" "\\*\\*$l:\\*\\*"
done
entries "$s18" Divergences || fail=1

need "$s41" "4.1" "Recorded contract \\(tailscale node, checked $date_re\\)"
for l in Sources Connection Identifiers Keyring Divergences; do
  need "$s41" "4.1" "\\*\\*$l:\\*\\*"
done
entries "$s41" Divergences || fail=1

need "$s42" "4.2" 'Login by key'

# The keyring bullet is one line; it either records one key for all four systems, which must then be the committed
# file, or records that the four differ, in which case no file is committed and a divergence is listed.
kb=$(printf '%s\n' "$s41" | grep -F '**Keyring:**' | head -1)
if [ -z "$kb" ]; then
  bad "4.1 has no Keyring bullet"
elif printf '%s\n' "$kb" | grep -Fq 'identical on ubuntu/jammy, ubuntu/noble, debian/bookworm and debian/trixie'; then
  sha=$(printf '%s\n' "$kb" | grep -oE 'sha256 [0-9a-f]{64}' | head -1 | cut -d' ' -f2)
  [ -n "$sha" ] || bad "the Keyring bullet lacks 'sha256 <64 hex digits>'"
  if [ ! -f "$keyring" ]; then
    bad "$keyring does not exist"
  else
    got=$(sha256sum "$keyring" | cut -d' ' -f1)
    [ "$got" = "$sha" ] || bad "$keyring has sha256 $got, the Keyring bullet says ${sha:-nothing}"
    mkdir -p -m 700 "$TMPDIR/gnupg"
    fprs=$(gpg --homedir "$TMPDIR/gnupg" --batch --show-keys --with-colons "$keyring" 2>/dev/null \
      | awk -F: '$1=="pub"{p=1;next} p&&$1=="fpr"{print $10;p=0}')
    [ -n "$fprs" ] || bad "gpg finds no key in $keyring"
    for f in $fprs; do
      printf '%s\n' "$kb" | grep -Fq "fingerprint $f" || bad "the Keyring bullet lacks 'fingerprint $f'"
    done
  fi
  for os in ubuntu-jammy ubuntu-noble debian-bookworm debian-trixie; do
    d="$docs/keyring-$os/keyring.gpg"
    if [ ! -f "$d" ]; then bad "the download $d is missing"; continue; fi
    [ "$(sha256sum "$d" | cut -d' ' -f1)" = "${sha:-x}" ] || bad "the download for $os differs from the Keyring bullet's sha256"
  done
elif printf '%s\n' "$kb" | grep -Fq 'differs'; then
  [ ! -e "$keyring" ] || bad "the keyrings differ, so $keyring must not be committed"
  printf '%s\n' "$s41" | awk '/\*\*Divergences:\*\*/{d=1;next} d' | grep -Eq 'Tailscale shared plan helpers — ' \
    || bad "the keyrings differ but 4.1 Divergences has no 'Tailscale shared plan helpers' entry"
else
  bad "the Keyring bullet says neither 'identical on ubuntu/jammy, ubuntu/noble, debian/bookworm and debian/trixie' nor 'differs'"
fi

printf '%s\n%s\n%s\n' "$s18" "$s41" "$s42" | grep -En 'TBD|TODO|\?\?\?' && bad "a placeholder is left in 1.8, 4.1 or 4.2"

read -r a0 b0 < <(bounds '#### 1.8 Host clients' '#### 1.9 ')
read -r a1 b1 < <(bounds '#### 4.1 Node' '#### 4.2 ')
read -r a2 b2 < <(bounds '#### 4.2 Tailnet' '#### 4.3 ')
stray=$(git diff -U0 -- "$spec" | grep -E '^@@' | while read -r _ _ plus _; do
  start=${plus#+}; start=${start%%,*}; len=1
  case $plus in *,*) len=${plus#*,};; esac
  end=$((start + len - 1)); [ "$len" -eq 0 ] && end=$start
  if { [ "$start" -ge "$a0" ] && [ "$end" -le "$b0" ]; } \
     || { [ "$start" -ge "$a1" ] && [ "$end" -le "$b1" ]; } \
     || { [ "$start" -ge "$a2" ] && [ "$end" -le "$b2" ]; }; then continue; fi
  echo "hunk +$start,$len"
done)
[ -z "$stray" ] || bad "the spec changed outside sections 1.8, 4.1 and 4.2: $stray"

others=$(git status --porcelain | grep -v -e " $spec\$" -e " $keyring\$" || true)
[ -z "$others" ] || bad "files other than the spec and the keyring changed: $others"

[ "$fail" -eq 0 ] && echo "spike check OK"
exit "$fail"
```

- [ ] **Step 2: Run the check to verify it fails**

Run, from the repository root: `bash $TMPDIR/check-spike.sh`
Expected: exit status 1 and 16 `FAIL:` lines: the 1.8 bullet and its seven labels reported missing, the 4.1 bullet and its five labels reported missing, "4.2 lacks: Login by key", and "4.1 has no Keyring bullet". No "changed outside" line and no "files other than" line, because nothing is edited yet.

- [ ] **Step 3: Read the sources and record the result**

Network rules. Use only unauthenticated `curl -sSL --max-time 60` GETs of the URLs below. Send no credential and make no authenticated request. Save each source in its own new empty directory under `$TMPDIR/docs/` (the Tailscale files of one tag together in `ts-<tag>/`, each keyring in its own `keyring-<os>-<codename>/keyring.gpg`), keep every script you write in `$TMPDIR/scripts/`, and read files with `rg` and `sed -n`. If a source cannot be fetched, stop and report BLOCKED with the URL; never fill a fact in from memory.

Download the Tailscale source files for both tags. A path that answers 404 at a tag is not at that path there: delete the saved error page, list the directory with `curl -sSL "https://api.github.com/repos/tailscale/tailscale/contents/<dir>?ref=<tag>"` (an unauthenticated read) and download the file that holds the symbol you need.

```bash
paths="ipn/ipnstate/ipnstate.go ipn/localapi/localapi.go ipn/localapi/cert.go ipn/prefs.go ipn/backend.go ipn/ipnlocal/local.go cmd/tailscale/cli/up.go client/local/local.go client/local/cert.go tailcfg/derpmap.go cmd/derper/derper.go"
for tag in v1.82.0 v1.104.1; do
  mkdir -p "$TMPDIR/docs/ts-$tag"
  for p in $paths; do
    curl -sSL --max-time 60 -o "$TMPDIR/docs/ts-$tag/$(echo "$p" | tr / _)" -w "$tag $p %{http_code}\n" "https://raw.githubusercontent.com/tailscale/tailscale/$tag/$p"
  done
done
```

Download the four repository keyrings, one per supported system, and compare them:

```bash
for os in ubuntu/jammy ubuntu/noble debian/bookworm debian/trixie; do
  d="$TMPDIR/docs/keyring-$(echo "$os" | tr / -)"; mkdir -p "$d"
  curl -sSL --max-time 60 -o "$d/keyring.gpg" -w "$os %{http_code} %{size_download}\n" "https://pkgs.tailscale.com/stable/$os.noarmor.gpg"
done
sha256sum "$TMPDIR"/docs/keyring-*/keyring.gpg
mkdir -p -m 700 "$TMPDIR/gnupg"
gpg --homedir "$TMPDIR/gnupg" --batch --show-keys --with-colons "$TMPDIR/docs/keyring-ubuntu-jammy/keyring.gpg"
```

In the `--with-colons` output, field 6 and field 7 of a `pub` record are the key's creation and expiry times in epoch seconds (field 7 is empty for a key that does not expire) and field 10 of the `fpr` record that follows a `pub` record is the primary key's fingerprint.

Edit the spec, in sections 1.8, 4.1 and 4.2 only, as follows. State only what a source says; where a source is silent write `not documented` for that item, never a guess. Each bullet is factual and at most about eight lines; a nested bullet's continuation lines are indented four spaces. Use the absolute date you ran the check as `YYYY-MM-DD`.

1. **Section 1.8.** Directly after the "Tailscale local client" bullet add `- **Recorded contract (tailscaled local API, checked YYYY-MM-DD).**` with exactly these nested bullets, each starting with its bold label, in this order, each naming the tag (`v1.82.0` or `v1.104.1`) a statement holds at:
   - `**Sources:**` each URL read, as `tag path`.
   - `**Endpoints:**` one line per call `internal/platform/tslocal` makes: `GET /localapi/v0/status`, `GET` and `PATCH /localapi/v0/prefs`, `POST /localapi/v0/start`, `POST /localapi/v0/logout`, `GET /localapi/v0/derpmap`, `GET /localapi/v0/cert/<domain>?type=pair`, `GET /localapi/v0/whois?addr=` and `POST /localapi/v0/debug-derp-region?region=`. Each line states the methods the handler accepts, its success status (tslocal expects 200, except 204 for `start` and `logout`), whether the handler is registered under that name at both tags, and the permission it demands (read, write or cert) with whether a caller that is root on the local socket holds it. Look in the handler table of `ipn/localapi/localapi.go` and, where the table does not list a name at a tag, in the other `ipn/localapi/*.go` files of that tag (list the directory).
   - `**Masked prefs:**` the `ipn.MaskedPrefs` field names behind tslocal's `PrefsPatch` (`WantRunning`, `RouteAll`, `ShieldsUp`, `RunSSH`, `ExitNodeID`, `ExitNodeIP`, `AdvertiseRoutes`), each with its `<Field>Set` flag; whether `AdvertiseRoutes` is the only way to advertise an exit node (the routes `0.0.0.0/0` and `::/0`); and whether a patch that sets `ExitNodeID` and `ExitNodeIP` together is refused (`ipn/prefs.go`).
   - `**Login by key:**` the local API calls `tailscale up --auth-key=<key>` makes against a logged-out node, in order (`cmd/tailscale/cli/up.go`); whether `StartLoginInteractive` (`POST /localapi/v0/login-interactive`) is also called when a key is given; whether `Start` with `ipn.Options{AuthKey, UpdatePrefs}` alone, with `WantRunning` true in the prefs, takes a node in `NeedsLogin` to `Running` (`ipn/ipnlocal/local.go`, `Start`); what `Start` does with an `AuthKey` on a node that is already logged in; and which prefs carry the host name and the advertised tags (`Hostname`, `AdvertiseTags`).
   - `**Cert pair:**` the path and query of the pair endpoint, `min_validity` if the handler reads it, the layout of the answer (tslocal splits it at the first `--\n--` and requires the first block to be the only private key; `internal/platform/tslocal/derp.go` lines 59-81), the status and error text when the node's tailnet has HTTPS disabled, when the domain is not one of the node's `CertDomains` and when the node is not running, and whether the call can block while a certificate is obtained. The client code is `client/local/cert.go` at `v1.104.1`; at `v1.82.0` search the downloaded `client/local` files for `CertPair`.
   - `**Latency check:**` the derper's `/derp/latency-check` handler (find it with `rg -n 'latency-check' "$TMPDIR/docs/ts-<tag>"/*`, then follow the function it names into its package): method, success status and body, and whether it is served on the DERP port; and, from `tailcfg/derpmap.go`, the meaning of `STUNPort` (0, a positive value, -1), `STUNOnly`, `DERPPort` (0 and other values), `HostName`, `IPv4` and `IPv6` (including the value that means none), and the URL form of the probe when `DERPPort` is not 443.
   - `**Divergences:**` each place where tailscaled differs from `internal/platform/tslocal` (a path, a method, a status, a layout) or from the plan's Contracts "Tailscale internals", "Tailscale wire shapes" (the latency regions, STUN endpoints and probe URL), "Listen certificate source" and "Tailscale installer join" (Join "logs in with authKey through the local API (Start with AuthKey and WantRunning)" and waits up to 60 s for a Tailscale address), one entry in the fixed divergence form per difference as nested bullets after the label, or the single word `none` on the label's line.

2. **Section 4.1.** Directly before the "Module preflight" bullet add `- **Recorded contract (tailscale node, checked YYYY-MM-DD).**` with exactly these nested bullets, in this order:
   - `**Sources:**` each URL read, as `tag path`, and the four keyring URLs.
   - `**Connection:**` from `ipn/ipnstate/ipnstate.go`: the comment and the behaviour of `PeerStatus.CurAddr`, `Relay` and `PeerRelay` at each tag (`PeerRelay` does not exist at `v1.82.0` if the file says so; a tslocal `PeerStatus` then decodes it as empty); which of them can be set together; what `Relay` holds (a DERP region code, not an id); and what `Self.Relay` holds (the node's home DERP region). Then confirm or amend the plan's mapping: a peer is `direct` when `CurAddr` is set (endpoint `CurAddr`), else `peer-relay` when `PeerRelay` is set, else `derp` when `Relay` is set (region `Relay`), else `none`; `preferredDerp` is `Self.Relay` (Assumptions, ruling on a peer's connection).
   - `**Identifiers:**` from the same file: that `PeerStatus.ID` is the stable node ID (the value `ExitNodeID` takes); that `DNSName` ends with a dot; the order of `TailscaleIPs`; what `Online` means for a peer and for the exit node; the value of `LastHandshake` when there was none; the form of `Tags`; what `Status.Peer` is keyed by; and the meaning of `MagicDNSSuffix`, `CurrentTailnet.Name` and `CertDomains`.
   - `**Keyring:**` one line, which the check parses: the URL form `https://pkgs.tailscale.com/stable/<os>/<codename>.noarmor.gpg`; then either `identical on ubuntu/jammy, ubuntu/noble, debian/bookworm and debian/trixie` followed by `sha256 <64 lower-case hex digits>`, `fingerprint <40 upper-case hex digits>` for every primary key of the file, the key's user id, its creation date and its expiry date (or `does not expire`), and `saved as internal/modules/tailscale/repo_keyring.gpg`; or, when the four files are not byte-identical, `differs` with each system's sha256 and `no file saved`. When they are identical, copy the file: `cp "$TMPDIR/docs/keyring-ubuntu-jammy/keyring.gpg" internal/modules/tailscale/repo_keyring.gpg`. When they differ, create no file and add a Divergences entry `Tailscale shared plan helpers — one keyring embedded for all four systems → the keyrings differ (<system>: <sha256>, ...) — <keyring URL>`, then report it first.
   - `**Divergences:**` each place where the files differ from the plan's `Peer.Connection` mapping, `Node` identifiers and `preferredDerp` (Contracts "Tailscale wire shapes") or from the shared keyring (Contracts "Tailscale shared plan helpers"), in the fixed form, or `none` on the label's line.

3. **Section 4.2.** In the Join step 4 sentence change "log in through the local API with the key," to "log in through the local API with the key (the calls are recorded in 1.8, Login by key),". Change nothing else there. If "Login by key" shows that a node needs more than `Start` to log in, add the divergence to the 1.8 `Divergences` bullet, not here.

Do not edit the plan, Contracts, `internal/platform/tslocal` or any other section of the spec. If a result breaks a requirement (for example `Start` with a key does not log in by itself, or the pair endpoint is not `type=pair`), record the facts in the bullet and put the conflict at the top of your report; do not work around it.

- [ ] **Step 4: Run the check to verify it passes**

Run, from the repository root: `bash $TMPDIR/check-spike.sh`
Expected: `spike check OK` and exit status 0. Then run `git status --short` and confirm it names only `docs/dforge/specs/2026-10-21-v1-completion-design.md` and, when the keyrings are identical, `internal/modules/tailscale/repo_keyring.gpg`.

Then run the Global Constraints gates; no Go file changed, so they must pass as on the base:

```bash
export TMPDIR=/root/.cache/w4-tmp/1 GOTOOLCHAIN=local
timeout 600 go build ./... && timeout 300 go vet ./... && timeout 1500 go test -count=1 ./... && timeout 900 golangci-lint run --allow-serial-runners ./... && timeout 300 go mod tidy -diff
```

Expected: no build or vet output, every test line `ok` or `?`, `0 issues.`, and nothing from `go mod tidy -diff`.

- [ ] **Step 5: Commit**

```bash
git add docs/dforge/specs/2026-10-21-v1-completion-design.md internal/modules/tailscale/repo_keyring.gpg
git commit -m "docs(spec): record the tailscale node contract"
rm -rf /root/.cache/w4-tmp/1 && GOTOOLCHAIN=local go clean -testcache
```

Your report ends with a section "Divergences" that repeats, verbatim and one per line, every nested entry of the two `Divergences` bullets (1.8 and 4.1), each in the fixed form `<Contracts section> — <plan value> → <provider value> — <source URL>`; when both are `none`, the section says `none`. The plan's Contracts are not edited by this task. Any entry other than `none` stops the plan after wave 1-3 merges until the integrator amends Contracts and every task that states the old value, so write each entry precisely enough to be applied without reading the spec bullet.

### Task 2: transport: the dpb2 bootstrap bundle

**Files:**
- Modify: `internal/transport/token.go` (add `BundlePrefix`, `Bundle`, `ParseBootstrap` beside `ParseToken`; `ParseToken` and `Token` do not change)
- Create: `internal/transport/token_test.go` (the bundle tests; the existing token tests stay in `identity_test.go`, which this task does not edit)
- Create: `internal/transport/token_fuzz_test.go`

**Interfaces:**
- Consumes: `Token`, `ParseToken`, `malformed()` and the package's `errcode.EnrollTokenInvalid`; the test helper `validToken()` from `identity_test.go` (same package).
- Produces: Contracts "Bootstrap bundle (from Task 2)": `BundlePrefix`, `Bundle` with `String`, `ParseBootstrap`, and `FuzzParseBootstrap`. Task 9 builds the grant's bundle with `Bundle.String`; Task 30 and `daemon.Enroll` parse with `ParseBootstrap`.

**Items:** 70

**Depends on:** none

The test prefix is `bd` (Contracts "Test prefixes"); `FuzzParseBootstrap` keeps the name the Contracts give it.

- [ ] **Step 1: Write the failing tests**

Create `internal/transport/token_test.go`:

```go
package transport

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

const bdKey = "tskey-auth-kBdTest1CNTRL-0123456789abcdefghij"

const bdMalformed = "the enrollment token is malformed"

// bdBundle is a well-formed bundle whose token dials the controller's tailnet address.
func bdBundle() Bundle {
	tok := validToken()
	tok.Address = "100.64.0.7:7443"
	return Bundle{Token: tok, TailnetKey: bdKey, ControllerTailnetAddr: "100.64.0.7:7443"}
}

// bdEncode wraps a JSON text as a version-2 bundle.
func bdEncode(js string) string {
	return BundlePrefix + base64.RawURLEncoding.EncodeToString([]byte(js))
}

// bdJSON builds a bundle's JSON text from raw member values, so a case can put any JSON type in any member.
func bdJSON(v, token, key, addr string) string {
	return `{"v":` + v + `,"token":` + token + `,"tailnetKey":` + key + `,"controllerTailnetAddr":` + addr + `}`
}

// bdQuote is s as a JSON string.
func bdQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// bdRefused fails the test unless ParseBootstrap refused with enroll.token_invalid and the given detail, returning
// nothing and leaking nothing of the bundle.
func bdRefused(t *testing.T, input, detail string) {
	t.Helper()
	tok, b, err := ParseBootstrap(input)
	var coded *errcode.Error
	if !errors.As(err, &coded) || coded.Code != errcode.EnrollTokenInvalid || coded.Detail != detail {
		t.Fatalf("ParseBootstrap(%q) = %+v, %+v, %v; want %s %q", input, tok, b, err, errcode.EnrollTokenInvalid, detail)
	}
	if tok != (Token{}) || b != nil {
		t.Fatalf("ParseBootstrap(%q) returned %+v, %+v with an error", input, tok, b)
	}
	if strings.Contains(err.Error(), bdKey) {
		t.Fatalf("the error %q carries the tailnet key", err)
	}
}

func TestBdBundleRoundTrip(t *testing.T) {
	want := bdBundle()
	text := want.String()
	if !strings.HasPrefix(text, "dpb2.") || strings.ContainsAny(text, "=+/") {
		t.Fatalf("String = %q, want dpb2. and base64url without padding", text)
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(strings.TrimPrefix(text, BundlePrefix))
	if err != nil {
		t.Fatal(err)
	}
	var members map[string]any
	if err := json.Unmarshal(raw, &members); err != nil {
		t.Fatal(err)
	}
	if len(members) != 4 || members["v"] != float64(2) || members["token"] != want.Token.String() ||
		members["tailnetKey"] != want.TailnetKey || members["controllerTailnetAddr"] != want.ControllerTailnetAddr {
		t.Fatalf("bundle JSON = %s", raw)
	}
	tok, got, err := ParseBootstrap("  " + text + "\n")
	if err != nil || got == nil || *got != want || tok != want.Token {
		t.Fatalf("ParseBootstrap = %+v, %+v, %v; want %+v", tok, got, err, want)
	}
}

func TestBdParseBootstrapPlainToken(t *testing.T) {
	want := validToken()
	tok, b, err := ParseBootstrap(" " + want.String() + "\n")
	if err != nil || b != nil || tok != want {
		t.Fatalf("ParseBootstrap = %+v, %+v, %v; want %+v, nil, nil", tok, b, err, want)
	}
}

func TestBdParseBootstrapAcceptsTailnetAddresses(t *testing.T) {
	for _, addr := range []string{
		"100.64.0.0:7443",
		"100.127.255.255:1",
		"100.100.100.100:65535",
		"[fd7a:115c:a1e0::1]:7443",
		"[fd7a:115c:a1e0:ffff:ffff:ffff:ffff:ffff]:7443",
	} {
		t.Run(addr, func(t *testing.T) {
			want := bdBundle()
			want.ControllerTailnetAddr = addr
			tok, got, err := ParseBootstrap(want.String())
			if err != nil || got == nil || *got != want || tok != want.Token {
				t.Fatalf("ParseBootstrap = %+v, %+v, %v; want %+v", tok, got, err, want)
			}
		})
	}
}

func TestBdParseBootstrapRefuses(t *testing.T) {
	tok := bdQuote(validToken().String())
	key := bdQuote(bdKey)
	addr := bdQuote("100.64.0.7:7443")
	good := bdJSON("2", tok, key, addr)
	withRawAddr := func(a string) string { return bdEncode(bdJSON("2", tok, key, a)) }
	withAddr := func(a string) string { return withRawAddr(bdQuote(a)) }
	withKey := func(k string) string { return bdEncode(bdJSON("2", tok, k, addr)) }
	withToken := func(k string) string { return bdEncode(bdJSON("2", k, key, addr)) }
	withVersion := func(v string) string { return bdEncode(bdJSON(v, tok, key, addr)) }
	padded := BundlePrefix + base64.URLEncoding.EncodeToString([]byte(good))
	if !strings.HasSuffix(padded, "=") {
		t.Fatal("the padded case needs a JSON text whose length is not a multiple of three")
	}

	tests := []struct{ name, text string }{
		{"empty", ""},
		{"blank", "  \n"},
		{"prefix only", BundlePrefix},
		{"upper-case prefix", "DPB2." + strings.TrimPrefix(bdEncode(good), BundlePrefix)},
		{"no dot", "dpb2" + strings.TrimPrefix(bdEncode(good), BundlePrefix)},
		{"padded base64", padded},
		{"not base64", BundlePrefix + "!!!"},
		{"not JSON", bdEncode("not json")},
		{"JSON array", bdEncode(`["a"]`)},
		{"trailing JSON", bdEncode(good + "{}")},
		{"missing tailnetKey", bdEncode(`{"v":2,"token":` + tok + `,"controllerTailnetAddr":` + addr + `}`)},
		{"extra member", bdEncode(strings.TrimSuffix(good, "}") + `,"x":1}`)},
		{"member v is 3", withVersion("3")},
		{"member v is a string", withVersion(`"2"`)},
		{"member v is null", withVersion("null")},
		{"member v is 2.5", withVersion("2.5")},
		{"member v is 2.0", withVersion("2.0")},
		{"token is a number", withToken("7")},
		{"token is null", withToken("null")},
		{"token is malformed", withToken(bdQuote("dpe1.AAAA"))},
		{"token is a bundle", withToken(bdQuote(bdBundle().String()))},
		{"token without its prefix", withToken(bdQuote(strings.TrimPrefix(validToken().String(), TokenPrefix)))},
		{"key is empty", withKey(bdQuote(""))},
		{"key is null", withKey("null")},
		{"key is a number", withKey("7")},
		{"key has a space", withKey(bdQuote("tskey-auth-a b"))},
		{"key has a newline", withKey(bdQuote("tskey-auth-a\nb"))},
		{"key has a tab", withKey(bdQuote("tskey-auth-a\tb"))},
		{"address is empty", withAddr("")},
		{"address is null", withRawAddr("null")},
		{"address is a number", withRawAddr("7")},
		{"address has no port", withAddr("100.64.0.7")},
		{"port is empty", withAddr("100.64.0.7:")},
		{"port is zero", withAddr("100.64.0.7:0")},
		{"port is 65536", withAddr("100.64.0.7:65536")},
		{"port is a name", withAddr("100.64.0.7:http")},
		{"port is signed", withAddr("100.64.0.7:+80")},
		{"host is a name", withAddr("controller.example:7443")},
		{"public IPv4", withAddr("203.0.113.5:7443")},
		{"loopback IPv4", withAddr("127.0.0.1:7443")},
		{"just below 100.64.0.0/10", withAddr("100.63.255.255:7443")},
		{"just above 100.64.0.0/10", withAddr("100.128.0.0:7443")},
		{"IPv6 without brackets", withAddr("fd7a:115c:a1e0::1:7443")},
		{"IPv6 outside fd7a:115c:a1e0::/48", withAddr("[fd7a:115c:a1e1::1]:7443")},
		{"IPv6 loopback", withAddr("[::1]:7443")},
		{"IPv6 with a zone", withAddr("[fd7a:115c:a1e0::1%eth0]:7443")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { bdRefused(t, tt.text, bdMalformed) })
	}
}

func TestBdParseBootstrapRefusesOtherVersions(t *testing.T) {
	body := base64.RawURLEncoding.EncodeToString([]byte(bdJSON("3", bdQuote(validToken().String()), bdQuote(bdKey), bdQuote("100.64.0.7:7443"))))
	tests := []struct{ prefix, version, body string }{
		{"dpb1.", "1", body},
		{"dpb3.", "3", body},
		{"dpb3.", "3", "not base64 at all"},
		{"dpb3.", "3", ""},
		{"dpb10.", "10", body},
		{"dpb02.", "02", body},
		{"dpb0.", "0", body},
		{"dpb99999999999999999999.", "99999999999999999999", body},
	}
	for _, tt := range tests {
		t.Run(tt.prefix+tt.body[:min(len(tt.body), 4)], func(t *testing.T) {
			want := "bootstrap bundle version " + tt.version + " is not supported by this darkpanel"
			bdRefused(t, tt.prefix+tt.body, want)
			bdRefused(t, "  "+tt.prefix+tt.body+"\n", want)
		})
	}
	for _, text := range []string{"dpb.", "dpb.x", "dpbx.x", "dpb3", "dpb-3.x", "dpe2.x", "dpb 3.x"} {
		t.Run(text, func(t *testing.T) { bdRefused(t, text, bdMalformed) })
	}
}
```

Create `internal/transport/token_fuzz_test.go`:

```go
package transport

import (
	"errors"
	"net"
	"net/netip"
	"strings"
	"testing"
	"unicode"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// FuzzParseBootstrap feeds arbitrary input to ParseBootstrap: it must never panic, every refusal is
// enroll.token_invalid, a plain token is what ParseToken reads, and an accepted bundle holds a key without
// whitespace and a Tailscale controller address and round-trips through String and ParseBootstrap.
func FuzzParseBootstrap(f *testing.F) {
	bundle := bdBundle()
	v3 := strings.Replace(bdEncode(bdJSON("3", bdQuote(bundle.Token.String()), bdQuote(bdKey), bdQuote(bundle.ControllerTailnetAddr))), BundlePrefix, "dpb3.", 1)
	f.Add(validToken().String())
	f.Add(bundle.String())
	f.Add(v3)
	f.Add("")
	f.Fuzz(func(t *testing.T, in string) {
		tok, b, err := ParseBootstrap(in)
		if err != nil {
			var coded *errcode.Error
			if !errors.As(err, &coded) || coded.Code != errcode.EnrollTokenInvalid {
				t.Fatalf("ParseBootstrap(%q) error = %v, want enroll.token_invalid", in, err)
			}
			return
		}
		if b == nil {
			again, err := ParseToken(in)
			if err != nil || again != tok {
				t.Fatalf("ParseBootstrap(%q) accepted a plain token that ParseToken reads as %+v, %v", in, again, err)
			}
			return
		}
		if tok != b.Token {
			t.Fatalf("ParseBootstrap(%q) returned token %+v beside bundle token %+v", in, tok, b.Token)
		}
		if b.TailnetKey == "" || strings.IndexFunc(b.TailnetKey, unicode.IsSpace) >= 0 {
			t.Fatalf("accepted tailnet key %q", b.TailnetKey)
		}
		host, _, err := net.SplitHostPort(b.ControllerTailnetAddr)
		addr, perr := netip.ParseAddr(host)
		inTailnet := netip.MustParsePrefix("100.64.0.0/10").Contains(addr.Unmap()) || netip.MustParsePrefix("fd7a:115c:a1e0::/48").Contains(addr.Unmap())
		if err != nil || perr != nil || !inTailnet {
			t.Fatalf("accepted controller address %q", b.ControllerTailnetAddr)
		}
		tok2, b2, err := ParseBootstrap(b.String())
		if err != nil || b2 == nil || *b2 != *b || tok2 != tok {
			t.Fatalf("round trip of %+v gave %+v, %+v, %v", *b, tok2, b2, err)
		}
	})
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
mkdir -p /root/.cache/w4-tmp/2 && export TMPDIR=/root/.cache/w4-tmp/2 GOTOOLCHAIN=local
timeout 300 go test -count=1 -run 'Bd|FuzzParseBootstrap' ./internal/transport/
```

Expected: FAIL at build time, `undefined: Bundle`, `undefined: BundlePrefix` and `undefined: ParseBootstrap`.

- [ ] **Step 3: Implement**

All in `internal/transport/token.go`, with the style of the code around it (a doc comment on every exported identifier, the `transport:` error prefix where an error is not an `errcode`).

1. Declare `BundlePrefix = "dpb2."` and `Bundle` exactly as Contracts "Bootstrap bundle" gives them, with their doc comments.
2. `Bundle.String` encodes an unexported struct with the `json` names `v` (always the integer 2), `token` (`Token.String()`), `tailnetKey` and `controllerTailnetAddr`, in that order, and returns `BundlePrefix` followed by the base64url (`base64.RawURLEncoding`, no padding) of the JSON. A struct of strings and an int always encodes, as `Token.String` assumes. If `golangci-lint` reports gosec G117 for the key field, add `//nolint:gosec // G117: the bundle exists to carry the key`, as `Token.String` does for its secret.
3. `ParseBootstrap(s)` trims whitespace with `strings.TrimSpace`, then decides by prefix, in this order: (a) `TokenPrefix`: return `ParseToken`'s token, a nil bundle and its error; (b) `BundlePrefix`: parse the rest as a bundle (item 4); (c) text matching `^dpb([0-9]+)\.`, which is every `dpb<digits>.` other than exactly `dpb2.` (so `dpb1.`, `dpb3.`, `dpb10.` and `dpb02.` all land here): `errcode.New(errcode.EnrollTokenInvalid, "bootstrap bundle version <digits as written> is not supported by this darkpanel")`, without reading the body; (d) anything else: `malformed()`. On every error it returns the zero `Token` and a nil bundle; on a bundle it returns `bundle.Token` and a pointer to the bundle.
4. A bundle body is valid only when all of these hold; any failure returns `malformed()` (the same refusal as a bad token, so a probe learns nothing about which part failed, and no error text contains any part of the input):
   - the body decodes with `base64.RawURLEncoding.Strict()` (no padding, no standard-alphabet characters);
   - it is one JSON object (`json.Unmarshal` into a `map[string]json.RawMessage` rejects trailing data and non-objects) with exactly four members: `v`, `token`, `tailnetKey`, `controllerTailnetAddr`;
   - `v` unmarshals into an `int` and equals 2 (so `"2"`, `null`, `2.5` and `2.0` are refused), and the other three unmarshal into strings (so a number is refused; a `null` leaves an empty string, which the next rules refuse);
   - `token` is accepted by `ParseToken` (so an empty token, a token without its prefix and a nested bundle are refused);
   - `tailnetKey` is not empty and holds no whitespace (`strings.IndexFunc(key, unicode.IsSpace) < 0`);
   - `controllerTailnetAddr` is accepted by `net.SplitHostPort`, its host parses with `netip.ParseAddr` (a host name is refused), the address after `Unmap` is inside `100.64.0.0/10` or `fd7a:115c:a1e0::/48`, and its port parses with `strconv.ParseUint(port, 10, 16)` and is not 0. Declare the two prefixes as unexported variables in `token.go`: this package cannot import the tailscale module's `link.IsTailscaleAddr`.
5. `ParseToken`, `Token`, `malformed` and every existing test in the package stay as they are.

- [ ] **Step 4: Run the tests to verify they pass**

```bash
export TMPDIR=/root/.cache/w4-tmp/2 GOTOOLCHAIN=local
timeout 300 go test -count=1 -run 'Bd|FuzzParseBootstrap' ./internal/transport/
timeout 300 go test -race -count=1 ./internal/transport/
timeout 120 go test -count=1 -run '^$' -fuzz '^FuzzParseBootstrap$' -fuzztime 20s ./internal/transport/
```

Expected: `ok` for the first two. The third prints `PASS` after about 20 s. If the fuzzer finds a failing input, Go saves it under `internal/transport/testdata/fuzz/FuzzParseBootstrap/`; fix `ParseBootstrap` and commit that file with the fix.

Then run the Global Constraints gates; the task's package is `./internal/transport/`:

```bash
timeout 600 go build ./... && timeout 300 go vet ./... && timeout 1500 go test -count=1 ./... && timeout 900 golangci-lint run --allow-serial-runners ./... && timeout 300 go mod tidy -diff
```

Expected: no build or vet output, every test line `ok` or `?`, `0 issues.`, and nothing from `go mod tidy -diff`.

- [ ] **Step 5: Commit**

```bash
git add internal/transport/token.go internal/transport/token_test.go internal/transport/token_fuzz_test.go
git commit -m "feat(transport): parse the dpb2 bootstrap bundle"
rm -rf /root/.cache/w4-tmp/2 && GOTOOLCHAIN=local go clean -testcache
```

### Task 3: stun: hand-written STUN binding client

**Files:**
- Create: `internal/modules/tailscale/stun/stun.go`
- Create: `internal/modules/tailscale/stun/stun_test.go`

**Interfaces:**
- Consumes: the standard library only (`net`, `net/netip`, `crypto/rand`, `encoding/binary`, `context`); no Tailscale library (Global Constraints "Dependencies").
- Produces: Contracts "STUN client (from Task 3)": `stun.Probe`, `stun.Request`, `stun.Parse`. Task 8's `deps.stun` is `stun.Probe`; Task 15 calls it three times per region.

**Depends on:** none

The test prefix is `sn`. The package is pure protocol code: it knows nothing about DERP maps or the module. Its tests use a UDP fake on `127.0.0.1` port 0; the one wildcard socket is `Probe`'s own client socket, which the Contracts fix as `net.ListenPacket("udp", "")`. No test waits longer than a fraction of a second, so none needs a fake clock.

- [ ] **Step 1: Write the failing tests**

Create `internal/modules/tailscale/stun/stun_test.go`. The two RFC 5769 vectors are the RFC's sample responses (sections 2.2 and 2.3) cut before their MESSAGE-INTEGRITY and FINGERPRINT attributes, with the header length changed to match; every other message is built by the test's own encoder from RFC 5389 sections 6, 15.1 and 15.2, so the tests do not depend on the code they check.

```go
package stun

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"net"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	snBindingSuccess = 0x0101
	snBindingError   = 0x0111
	snMapped         = 0x0001
	snXorMapped      = 0x0020
	snSoftware       = 0x8022
)

// snTx is the transaction id of RFC 5769's sample responses.
var snTx = [12]byte{0xb7, 0xe7, 0xa7, 0x01, 0xbc, 0x34, 0xd6, 0x86, 0xfa, 0x87, 0xdf, 0xae}

// snMessage builds a STUN message of type typ from complete attributes.
func snMessage(typ uint16, tx [12]byte, attrs ...[]byte) []byte {
	body := bytes.Join(attrs, nil)
	b := binary.BigEndian.AppendUint16(nil, typ)
	b = binary.BigEndian.AppendUint16(b, uint16(len(body))) //nolint:gosec // G115: the test attributes are a few bytes
	b = binary.BigEndian.AppendUint32(b, 0x2112a442)
	b = append(b, tx[:]...)
	return append(b, body...)
}

// snAttr builds one attribute: type, length, the value and zero padding to a multiple of four bytes.
func snAttr(typ uint16, value []byte) []byte {
	a := binary.BigEndian.AppendUint16(nil, typ)
	a = binary.BigEndian.AppendUint16(a, uint16(len(value))) //nolint:gosec // G115: the test values are a few bytes
	a = append(a, value...)
	for len(a)%4 != 0 {
		a = append(a, 0)
	}
	return a
}

// snAddress is the value of a MAPPED-ADDRESS attribute, or of an XOR-MAPPED-ADDRESS one when tx is not nil
// (RFC 5389 sections 15.1 and 15.2): a zero byte, the family, the port and the address, the last two XORed with
// the magic cookie, and for IPv6 the transaction id, when tx is given.
func snAddress(ap netip.AddrPort, tx *[12]byte) []byte {
	family, port, addr := byte(1), ap.Port(), ap.Addr().AsSlice()
	if ap.Addr().Is6() {
		family = 2
	}
	if tx != nil {
		mask := append([]byte{0x21, 0x12, 0xa4, 0x42}, tx[:]...)
		port ^= 0x2112
		for i := range addr {
			addr[i] ^= mask[i]
		}
	}
	v := binary.BigEndian.AppendUint16([]byte{0, family}, port)
	return append(v, addr...)
}

func snHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(strings.Join(strings.Fields(s), ""))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSnRequestEncodesABindingRequest(t *testing.T) {
	got := Request(snTx)
	want := snHex(t, "0001 0000 2112a442 b7e7a701 bc34d686 fa87dfae")
	if !bytes.Equal(got, want) {
		t.Fatalf("Request = %x, want %x", got, want)
	}
}

func TestSnParseAcceptsBindingSuccess(t *testing.T) {
	v4 := netip.MustParseAddrPort("192.0.2.1:32853")
	v6 := netip.MustParseAddrPort("[2001:db8:1234:5678:11:2233:4455:6677]:32853")
	other := netip.MustParseAddrPort("198.51.100.9:1")
	tests := []struct {
		name string
		msg  []byte
		want netip.AddrPort
	}{
		{
			// RFC 5769 section 2.2 without its MESSAGE-INTEGRITY and FINGERPRINT attributes.
			"RFC 5769 IPv4 response",
			snHex(t, "0101 001c 2112a442 b7e7a701 bc34d686 fa87dfae  8022 000b 74657374 20766563 746f7220  0020 0008 0001 a147 e112a643"),
			v4,
		},
		{
			// RFC 5769 section 2.3 without its MESSAGE-INTEGRITY and FINGERPRINT attributes.
			"RFC 5769 IPv6 response",
			snHex(t, "0101 0018 2112a442 b7e7a701 bc34d686 fa87dfae  0020 0014 0002 a147 0113a9fa a5d3f179 bc25f4b5 bed2b9d9"),
			v6,
		},
		{"mapped address only", snMessage(snBindingSuccess, snTx, snAttr(snMapped, snAddress(v4, nil))), v4},
		{"mapped IPv6 address only", snMessage(snBindingSuccess, snTx, snAttr(snMapped, snAddress(v6, nil))), v6},
		{
			"xor-mapped address built by the helper",
			snMessage(snBindingSuccess, snTx, snAttr(snXorMapped, snAddress(v6, &snTx))),
			v6,
		},
		{
			"xor-mapped wins when it follows a mapped address",
			snMessage(snBindingSuccess, snTx, snAttr(snMapped, snAddress(other, nil)), snAttr(snXorMapped, snAddress(v4, &snTx))),
			v4,
		},
		{
			"xor-mapped wins when it precedes a mapped address",
			snMessage(snBindingSuccess, snTx, snAttr(snXorMapped, snAddress(v4, &snTx)), snAttr(snMapped, snAddress(other, nil))),
			v4,
		},
		{
			"unknown attributes with odd lengths are skipped",
			snMessage(snBindingSuccess, snTx, snAttr(0x8000, []byte{1, 2, 3}), snAttr(snSoftware, []byte("x")), snAttr(snXorMapped, snAddress(v4, &snTx))),
			v4,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tx, got, err := Parse(tt.msg)
			if err != nil || tx != snTx || got != tt.want {
				t.Fatalf("Parse = %x, %v, %v; want %x, %v, nil", tx, got, err, snTx, tt.want)
			}
		})
	}
}

func TestSnParseRefuses(t *testing.T) {
	v4 := netip.MustParseAddrPort("192.0.2.1:32853")
	good := snMessage(snBindingSuccess, snTx, snAttr(snXorMapped, snAddress(v4, &snTx)))
	badCookie := slices.Clone(good)
	badCookie[4] ^= 0xff
	longer := slices.Clone(good)
	longer[3] += 4
	odd := append(snMessage(snBindingSuccess, snTx), 0, 0)
	odd[3] = 2
	tests := []struct {
		name string
		msg  []byte
		want string
	}{
		{"empty", nil, "shorter than the 20-byte header"},
		{"one byte short of a header", good[:19], "shorter than the 20-byte header"},
		{"wrong magic cookie", badCookie, "magic cookie"},
		{"declared length past the datagram", longer, "does not match"},
		{"bytes after the declared length", append(slices.Clone(good), 0, 0, 0, 0), "does not match"},
		{"declared length not a multiple of four", odd, "does not match"},
		{"binding request", Request(snTx), "not a binding success response"},
		{"binding error response", snMessage(snBindingError, snTx, snAttr(0x0009, []byte{0, 0, 4, 1})), "not a binding success response"},
		{"attribute longer than the message", snMessage(snBindingSuccess, snTx, []byte{0x00, 0x20, 0x00, 0x08, 0x00, 0x01, 0xa1, 0x47}), "overruns"},
		{"unknown address family", snMessage(snBindingSuccess, snTx, snAttr(snXorMapped, []byte{0, 3, 0, 0, 1, 2, 3, 4})), "malformed"},
		{"IPv4 value of the wrong length", snMessage(snBindingSuccess, snTx, snAttr(snXorMapped, []byte{0, 1, 0, 0, 1, 2, 3, 4, 5, 6, 7, 8})), "malformed"},
		{"IPv6 value too short", snMessage(snBindingSuccess, snTx, snAttr(snXorMapped, []byte{0, 2, 0, 0, 1, 2, 3, 4})), "malformed"},
		{"mapped address of the wrong length", snMessage(snBindingSuccess, snTx, snAttr(snMapped, []byte{0, 1, 0, 0})), "malformed"},
		{"no attributes", snMessage(snBindingSuccess, snTx), "no mapped address"},
		{"only a software attribute", snMessage(snBindingSuccess, snTx, snAttr(snSoftware, []byte("test vector"))), "no mapped address"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tx, got, err := Parse(tt.msg)
			if err == nil || !strings.HasPrefix(err.Error(), "stun: ") || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Parse = %x, %v, %v; want an error containing %q", tx, got, err, tt.want)
			}
			if tx != ([12]byte{}) || got.IsValid() {
				t.Fatalf("Parse returned %x, %v with an error", tx, got)
			}
		})
	}
}

// snListen starts a UDP server on 127.0.0.1 and calls reply in its goroutine for each datagram it receives.
func snListen(t *testing.T, reply func(conn net.PacketConn, req []byte, from net.Addr)) netip.AddrPort {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 1500)
		for {
			n, from, err := conn.ReadFrom(buf)
			if err != nil {
				return
			}
			reply(conn, slices.Clone(buf[:n]), from)
		}
	}()
	t.Cleanup(func() {
		_ = conn.Close()
		<-done
	})
	ap := conn.LocalAddr().(*net.UDPAddr).AddrPort()
	return netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port())
}

// snTxOf returns the transaction id of a request, failing the test when it is not a bare Binding request.
func snTxOf(t *testing.T, req []byte) [12]byte {
	t.Helper()
	if len(req) != 20 || req[0] != 0x00 || req[1] != 0x01 || req[2] != 0x00 || req[3] != 0x00 || !bytes.Equal(req[4:8], []byte{0x21, 0x12, 0xa4, 0x42}) {
		t.Errorf("request = %x, want a 20-byte Binding request with no attributes", req)
	}
	return [12]byte(req[8:20])
}

// snAnswer writes the Binding success response that reports from as the mapped address.
func snAnswer(conn net.PacketConn, tx [12]byte, from net.Addr) {
	ap := from.(*net.UDPAddr).AddrPort()
	_, _ = conn.WriteTo(snMessage(snBindingSuccess, tx, snAttr(snXorMapped, snAddress(ap, &tx))), from)
}

func TestSnProbeMeasuresARoundTrip(t *testing.T) {
	const delay = 40 * time.Millisecond
	var mu sync.Mutex
	var txs [][12]byte
	server := snListen(t, func(conn net.PacketConn, req []byte, from net.Addr) {
		tx := snTxOf(t, req)
		mu.Lock()
		txs = append(txs, tx)
		mu.Unlock()
		time.Sleep(delay)
		snAnswer(conn, tx, from)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for range 2 {
		d, err := Probe(ctx, server)
		if err != nil || d < delay || d > 3*time.Second {
			t.Fatalf("Probe = %v, %v; want a duration of at least %v", d, err, delay)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(txs) != 2 || txs[0] == txs[1] {
		t.Fatalf("transaction ids = %x, want two different ids", txs)
	}
}

func TestSnProbeIgnoresDatagramsThatAreNotItsAnswer(t *testing.T) {
	const delay = 60 * time.Millisecond
	v4 := netip.MustParseAddrPort("192.0.2.1:32853")
	server := snListen(t, func(conn net.PacketConn, req []byte, from net.Addr) {
		tx := snTxOf(t, req)
		other := tx
		other[0] ^= 0xff
		for _, junk := range [][]byte{
			[]byte("not a stun message"),
			snMessage(snBindingSuccess, other, snAttr(snXorMapped, snAddress(v4, &other))),
			snMessage(snBindingError, tx, snAttr(0x0009, []byte{0, 0, 4, 1})),
			snMessage(snBindingSuccess, tx, snAttr(snSoftware, []byte("no address"))),
		} {
			_, _ = conn.WriteTo(junk, from)
		}
		time.Sleep(delay)
		snAnswer(conn, tx, from)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	d, err := Probe(ctx, server)
	if err != nil || d < delay {
		t.Fatalf("Probe = %v, %v; want success only after the real answer, at least %v", d, err, delay)
	}
}

func TestSnProbeEndsWithTheContext(t *testing.T) {
	server := snListen(t, func(net.PacketConn, []byte, net.Addr) {})

	t.Run("deadline", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
		defer cancel()
		start := time.Now()
		d, err := Probe(ctx, server)
		if !errors.Is(err, context.DeadlineExceeded) || d != 0 || time.Since(start) > 3*time.Second {
			t.Fatalf("Probe = %v, %v after %v; want context.DeadlineExceeded", d, err, time.Since(start))
		}
	})
	t.Run("cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		time.AfterFunc(50*time.Millisecond, cancel)
		start := time.Now()
		d, err := Probe(ctx, server)
		if !errors.Is(err, context.Canceled) || d != 0 || time.Since(start) > 3*time.Second {
			t.Fatalf("Probe = %v, %v after %v; want context.Canceled", d, err, time.Since(start))
		}
	})
	t.Run("no deadline", func(t *testing.T) {
		start := time.Now()
		d, err := probe(context.Background(), server, 150*time.Millisecond)
		if !errors.Is(err, context.DeadlineExceeded) || d != 0 || time.Since(start) > 3*time.Second {
			t.Fatalf("probe = %v, %v after %v; want context.DeadlineExceeded", d, err, time.Since(start))
		}
	})
	t.Run("the default wait is three seconds", func(t *testing.T) {
		if defaultWait != 3*time.Second {
			t.Fatalf("defaultWait = %v, want 3s", defaultWait)
		}
	})
}

func TestSnProbeRefusesAnInvalidServer(t *testing.T) {
	d, err := Probe(context.Background(), netip.AddrPort{})
	if err == nil || !strings.HasPrefix(err.Error(), "stun: ") || !strings.Contains(err.Error(), "invalid server address") || d != 0 {
		t.Fatalf("Probe = %v, %v; want a stun: invalid server address error", d, err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
mkdir -p /root/.cache/w4-tmp/3 && export TMPDIR=/root/.cache/w4-tmp/3 GOTOOLCHAIN=local
timeout 300 go test -count=1 ./internal/modules/tailscale/stun/
```

Expected: FAIL at build time, `undefined: Request`, `undefined: Parse`, `undefined: Probe`, `undefined: probe` and `undefined: defaultWait`.

- [ ] **Step 3: Implement**

All in `stun.go`, with a package comment ("Package stun is a hand-written RFC 5389 STUN binding client.") and a doc comment on every exported identifier.

1. Constants: the header size 20, the magic cookie `0x2112a442`, message types `0x0001` (Binding request) and `0x0101` (Binding success response), attribute types `0x0001` (MAPPED-ADDRESS) and `0x0020` (XOR-MAPPED-ADDRESS), and `defaultWait = 3 * time.Second`.
2. `Request(tx)` returns exactly 20 bytes: type `0x0001`, message length 0, the magic cookie, then `tx`. All integers are big-endian.
3. `Parse(b)` checks, in this order, and returns the zero values with an error whose text starts with `stun: ` and contains the fragment shown:
   1. `len(b) < 20`: `message of <n> bytes is shorter than the 20-byte header`;
   2. the cookie bytes 4-8 are not `0x2112a442`: `magic cookie is <value>, want 0x2112a442`;
   3. the header length is not a multiple of four, or is not `len(b) - 20`: `header length <n> does not match the <m> bytes that follow`;
   4. the type is not `0x0101` (a request and an error response `0x0111` are both refused): `message type <value> is not a binding success response`.

   Then it walks the attributes (type, length, the value, zero padding to a multiple of four bytes). An attribute whose header or padded value does not fit in the rest of the message gives `attribute <type> overruns the message`. Attributes other than MAPPED-ADDRESS and XOR-MAPPED-ADDRESS are skipped (SOFTWARE, MESSAGE-INTEGRITY, FINGERPRINT and unknown ones; none is verified). An address attribute must be 8 bytes with family byte 1 (IPv4) or 20 bytes with family byte 2 (IPv6), else `mapped address attribute <type> is malformed`. Decoding: the port is bytes 2-4; for XOR-MAPPED-ADDRESS the port is XORed with the top 16 bits of the cookie (`0x2112`), an IPv4 address with the cookie, and an IPv6 address with the cookie followed by the transaction id. The first attribute of each kind counts; XOR-MAPPED-ADDRESS wins over MAPPED-ADDRESS whatever their order; with neither, `response has no mapped address`. The returned transaction id is bytes 8-20.
4. `Probe(ctx, server)` is `probe(ctx, server, defaultWait)`. `probe` (unexported, so the no-deadline path can be tested with a short wait):
   1. refuses an invalid `server` (`server.IsValid()` false) with `stun: invalid server address`;
   2. draws a random 96-bit transaction id from `crypto/rand`;
   3. opens `net.ListenPacket("udp", "")` and closes it on return;
   4. uses `ctx`'s deadline when it has one, else a deadline `wait` from now (derive a context with `context.WithTimeout` so the same code path handles both); sets the socket deadline to it and registers `context.AfterFunc(ctx, ...)` to set the socket deadline to now, so a cancellation wakes the read; stops the AfterFunc on return;
   5. records the start time just before writing `Request(tx)` to `net.UDPAddrFromAddrPort(server)` (a write error is `stun: send to <server>: <cause>`);
   6. reads datagrams in a loop, running each through `Parse`; a datagram that does not parse, or whose transaction id differs, is ignored and the loop continues; the first parsed datagram with the same id returns `time.Since(start)` and a nil error. The source address of the datagram is not checked;
   7. on a read error: when `ctx.Err()` is not nil, return `stun: no answer from <server>: ` wrapping `ctx.Err()`; else when the error `errors.Is` `os.ErrDeadlineExceeded` (the derived deadline passed first), return the same text wrapping `context.DeadlineExceeded`; else `stun: read from <server>: <cause>`. Every error return has a zero duration.
   
   `errors.Is(err, context.DeadlineExceeded)` and `errors.Is(err, context.Canceled)` therefore tell the caller why `Probe` stopped.
5. `gosec` G115 flags narrowing integer conversions: bound the value before converting, or use a `//nolint:gosec // G115: <reason>` that names the reason, as the test file does for its encoder.

- [ ] **Step 4: Run the tests to verify they pass**

```bash
export TMPDIR=/root/.cache/w4-tmp/3 GOTOOLCHAIN=local
timeout 300 go test -race -count=1 ./internal/modules/tailscale/stun/
timeout 300 go test -race -count=5 ./internal/modules/tailscale/stun/
```

Expected: `ok` both times (the second run shows the timing assertions are stable).

Then run the Global Constraints gates; the task's package is `./internal/modules/tailscale/stun/`:

```bash
timeout 600 go build ./... && timeout 300 go vet ./... && timeout 1500 go test -count=1 ./... && timeout 900 golangci-lint run --allow-serial-runners ./... && timeout 300 go mod tidy -diff
```

Expected: no build or vet output, every test line `ok` or `?`, `0 issues.`, and nothing from `go mod tidy -diff`.

- [ ] **Step 5: Commit**

```bash
git add internal/modules/tailscale/stun/stun.go internal/modules/tailscale/stun/stun_test.go
git commit -m "feat(tailscale): add a STUN binding client"
rm -rf /root/.cache/w4-tmp/3 && GOTOOLCHAIN=local go clean -testcache
```

### Task 4: link: the tailscale.link guard rules

**Files:**
- Create: `internal/modules/tailscale/link/link.go`
- Create: `internal/modules/tailscale/link/link_test.go`

**Interfaces:**
- Consumes: `core.Guard` (`internal/core/contract.go`: `ID`, `OK`, `Message`, `Overridable`, `Overridden`). Nothing else: the package does no I/O and imports no other package of the module.
- Produces: Contracts "Link guard rules (from Task 4)": `link.GuardID`, `link.Interface`, `link.Flow`, `link.Prefs`, `link.Change`, `link.Check`, `link.IsTailscaleAddr`. Task 17's `linkGuard` builds the flows and calls `Check`; `Check` never sets `Overridden` (the engine and Task 17 do).

**Depends on:** none

The test prefix is `lk`. The firewall's `internal/modules/firewall/guard` is the model for what a flow is (`flows.go`, `facts.go`); this package takes the already-classified flows and holds only the decision.

- [ ] **Step 1: Write the failing tests**

Create `internal/modules/tailscale/link/link_test.go`. One table drives `Check`; every row names the exact message, so it pins the rule, the flows named, their order and the join. A row with an empty `fail` expects the passing guard.

```go
package link

import (
	"net/netip"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)

const lkOK = "no protected flow depends on what this change removes"

const lkSSHOn = "Tailscale SSH is on and a session may use it"

// lkFlow is a protected flow between two addresses, routed through iface.
func lkFlow(label string, inbound bool, local, remote, iface string) Flow {
	f := Flow{Label: label, Inbound: inbound, OutIface: iface}
	if local != "" {
		f.Local = netip.MustParseAddr(local)
	}
	if remote != "" {
		f.Remote = netip.MustParseAddr(remote)
	}
	return f
}

// lkRequest marks f as the flow that carries the request being planned.
func lkRequest(f Flow) Flow {
	f.Request = true
	return f
}

// lkPrefs changes to the node's preferences: a running node plus whatever set applies.
func lkPrefs(set func(*Prefs)) Prefs {
	p := Prefs{WantRunning: true}
	if set != nil {
		set(&p)
	}
	return p
}

type lkCase struct {
	name  string
	cur   Prefs
	chg   Change
	flows []Flow
	fail  string // the guard's message; empty when the guard passes
}

func lkPrefsChange(after Prefs) Change { return Change{Kind: "prefs", After: after} }

func lkCases() []lkCase {
	tailnetSSH := lkFlow("ssh", true, "100.64.0.5", "100.64.0.9", "tailscale0")
	lanSSH := lkFlow("ssh", true, "192.0.2.10", "198.51.100.7", "eth0")
	panel := lkFlow("panel", true, "100.64.0.5", "100.64.0.20", "tailscale0")
	subnet := lkFlow("controller link", false, "100.64.0.5", "10.8.0.3", "tailscale0")
	viaExit := lkFlow("controller session", false, "100.64.0.5", "203.0.113.9", "tailscale0")
	viaExit.Default = true
	route := netip.MustParsePrefix("192.168.7.0/24")

	return []lkCase{
		// logout, relogin and wantRunning false
		{"logout cuts a tailnet ssh session", lkPrefs(nil), Change{Kind: "logout"}, []Flow{tailnetSSH}, "ssh flow with 100.64.0.9 depends on Tailscale"},
		{"logout leaves flows that do not use tailscale", lkPrefs(nil), Change{Kind: "logout"}, []Flow{lanSSH}, ""},
		{"logout with no flows", lkPrefs(nil), Change{Kind: "logout"}, nil, ""},
		{"logout while Tailscale SSH is on", lkPrefs(func(p *Prefs) { p.RunSSH = true }), Change{Kind: "logout"}, []Flow{lanSSH}, lkSSHOn},
		{
			"logout names the flows and then Tailscale SSH", lkPrefs(func(p *Prefs) { p.RunSSH = true }), Change{Kind: "logout"},
			[]Flow{tailnetSSH}, "ssh flow with 100.64.0.9 depends on Tailscale; " + lkSSHOn,
		},
		{
			"relogin cuts a flow whose local address is on the tailnet", lkPrefs(nil), Change{Kind: "relogin"},
			[]Flow{lkFlow("agent link", false, "100.64.0.5", "203.0.113.9", "")}, "agent link flow with 203.0.113.9 depends on Tailscale",
		},
		{
			"an IPv6 tailnet address depends on tailscale", lkPrefs(nil), Change{Kind: "logout"},
			[]Flow{lkFlow("ssh", true, "fd7a:115c:a1e0::5", "fd7a:115c:a1e0::9", "")}, "ssh flow with fd7a:115c:a1e0::9 depends on Tailscale",
		},
		{
			"an IPv4-mapped tailnet address depends on tailscale", lkPrefs(nil), Change{Kind: "logout"},
			[]Flow{lkFlow("ssh", true, "::ffff:100.64.0.5", "198.51.100.7", "")}, "ssh flow with 198.51.100.7 depends on Tailscale",
		},
		{
			"wantRunning false cuts the panel", lkPrefs(nil), lkPrefsChange(Prefs{}),
			[]Flow{panel}, "panel flow with 100.64.0.20 depends on Tailscale",
		},
		{"wantRunning true leaves tailnet flows alone", lkPrefs(nil), lkPrefsChange(lkPrefs(nil)), []Flow{tailnetSSH}, ""},
		{
			"every dependent flow is named once, in order", lkPrefs(nil), Change{Kind: "logout"},
			[]Flow{tailnetSSH, lanSSH, panel, tailnetSSH},
			"ssh flow with 100.64.0.9 depends on Tailscale; panel flow with 100.64.0.20 depends on Tailscale",
		},
		{
			"logout does not apply the preferences of After", lkPrefs(func(p *Prefs) { p.AcceptRoutes = true }), Change{Kind: "logout"},
			[]Flow{subnet}, "controller link flow with 10.8.0.3 depends on Tailscale",
		},

		// shields-up
		{
			"shields-up blocks an inbound flow over tailscale", lkPrefs(nil), lkPrefsChange(lkPrefs(func(p *Prefs) { p.ShieldsUp = true })),
			[]Flow{tailnetSSH}, "ssh flow with 100.64.0.9 arrives over Tailscale and shields-up would block it",
		},
		{
			"shields-up leaves an outbound flow over tailscale", lkPrefs(nil), lkPrefsChange(lkPrefs(func(p *Prefs) { p.ShieldsUp = true })),
			[]Flow{subnet, lanSSH}, "",
		},
		{
			"shields-up while Tailscale SSH is on", lkPrefs(func(p *Prefs) { p.RunSSH = true }),
			lkPrefsChange(lkPrefs(func(p *Prefs) { p.RunSSH, p.ShieldsUp = true, true })), []Flow{lanSSH}, lkSSHOn,
		},
		{
			"shields-up already on", lkPrefs(func(p *Prefs) { p.ShieldsUp = true }),
			lkPrefsChange(lkPrefs(func(p *Prefs) { p.ShieldsUp = true })), []Flow{tailnetSSH}, "",
		},

		// runSSH off
		{
			"runSSH off with a request that arrives another way", lkPrefs(func(p *Prefs) { p.RunSSH = true }), lkPrefsChange(lkPrefs(nil)),
			[]Flow{lkRequest(tailnetSSH), lkRequest(lanSSH)}, "",
		},
		{
			"runSSH off with a request over tailscale", lkPrefs(func(p *Prefs) { p.RunSSH = true }), lkPrefsChange(lkPrefs(nil)),
			[]Flow{lkRequest(tailnetSSH), lanSSH}, "Tailscale SSH is on and no request flow is known to arrive another way",
		},
		{
			"runSSH off with no request flow", lkPrefs(func(p *Prefs) { p.RunSSH = true }), lkPrefsChange(lkPrefs(nil)),
			[]Flow{lanSSH}, "Tailscale SSH is on and no request flow is known to arrive another way",
		},
		{
			"runSSH off with a request flow that cannot be classified", lkPrefs(func(p *Prefs) { p.RunSSH = true }), lkPrefsChange(lkPrefs(nil)),
			[]Flow{lkRequest(Flow{Label: "ssh", Inbound: true, Unknown: "the socket has no owner"})},
			"Tailscale SSH is on and no request flow is known to arrive another way; ssh flow with an unknown address cannot be classified: the socket has no owner",
		},
		{"runSSH already off", lkPrefs(nil), lkPrefsChange(lkPrefs(nil)), []Flow{lkRequest(tailnetSSH)}, ""},

		// acceptRoutes off
		{
			"acceptRoutes off cuts a flow that uses an accepted route", lkPrefs(func(p *Prefs) { p.AcceptRoutes = true }), lkPrefsChange(lkPrefs(nil)),
			[]Flow{subnet}, "controller link flow with 10.8.0.3 uses a route accepted from the tailnet",
		},
		{
			"acceptRoutes off keeps a flow to a tailnet peer", lkPrefs(func(p *Prefs) { p.AcceptRoutes = true }), lkPrefsChange(lkPrefs(nil)),
			[]Flow{lkFlow("agent peer", false, "100.64.0.5", "100.64.0.9", "tailscale0")}, "",
		},
		{
			"acceptRoutes off leaves the exit node path to the exit node rule", lkPrefs(func(p *Prefs) { p.AcceptRoutes = true }), lkPrefsChange(lkPrefs(nil)),
			[]Flow{viaExit}, "",
		},
		{"acceptRoutes already off", lkPrefs(nil), lkPrefsChange(lkPrefs(nil)), []Flow{subnet}, ""},

		// exit node changes
		{
			"changing the exit node cuts a flow through it", lkPrefs(func(p *Prefs) { p.ExitNode = "nA" }),
			lkPrefsChange(lkPrefs(func(p *Prefs) { p.ExitNode = "nB" })), []Flow{viaExit},
			"controller session flow with 203.0.113.9 leaves through the exit node",
		},
		{
			"clearing the exit node cuts a flow through it", lkPrefs(func(p *Prefs) { p.ExitNode = "nA" }), lkPrefsChange(lkPrefs(nil)),
			[]Flow{viaExit}, "controller session flow with 203.0.113.9 leaves through the exit node",
		},
		{"choosing a first exit node", lkPrefs(nil), lkPrefsChange(lkPrefs(func(p *Prefs) { p.ExitNode = "nB" })), []Flow{viaExit}, ""},
		{
			"keeping the exit node", lkPrefs(func(p *Prefs) { p.ExitNode = "nA" }),
			lkPrefsChange(lkPrefs(func(p *Prefs) { p.ExitNode = "nA" })), []Flow{viaExit}, "",
		},
		{
			"changing the exit node leaves a subnet route alone", lkPrefs(func(p *Prefs) { p.ExitNode = "nA" }), lkPrefsChange(lkPrefs(nil)),
			[]Flow{subnet}, "",
		},

		// advertised routes removed
		{
			"removing an advertised route under an inbound flow", lkPrefs(func(p *Prefs) { p.AdvertiseRoutes = []netip.Prefix{route} }), lkPrefsChange(lkPrefs(nil)),
			[]Flow{lkFlow("ssh", true, "192.168.7.20", "100.64.0.9", "tailscale0")}, "ssh flow with 100.64.0.9 arrives through the advertised route 192.168.7.0/24",
		},
		{
			"keeping the advertised route", lkPrefs(func(p *Prefs) { p.AdvertiseRoutes = []netip.Prefix{route} }),
			lkPrefsChange(lkPrefs(func(p *Prefs) { p.AdvertiseRoutes = []netip.Prefix{route} })),
			[]Flow{lkFlow("ssh", true, "192.168.7.20", "100.64.0.9", "tailscale0")}, "",
		},
		{
			"keeping the advertised route written unmasked", lkPrefs(func(p *Prefs) { p.AdvertiseRoutes = []netip.Prefix{route} }),
			lkPrefsChange(lkPrefs(func(p *Prefs) { p.AdvertiseRoutes = []netip.Prefix{netip.MustParsePrefix("192.168.7.5/24")} })),
			[]Flow{lkFlow("ssh", true, "192.168.7.20", "100.64.0.9", "tailscale0")}, "",
		},
		{
			"removing an advertised route outside every flow", lkPrefs(func(p *Prefs) { p.AdvertiseRoutes = []netip.Prefix{route} }), lkPrefsChange(lkPrefs(nil)),
			[]Flow{lkFlow("ssh", true, "192.168.8.20", "100.64.0.9", "tailscale0")}, "",
		},
		{
			"removing an advertised route under an outbound flow", lkPrefs(func(p *Prefs) { p.AdvertiseRoutes = []netip.Prefix{route} }), lkPrefsChange(lkPrefs(nil)),
			[]Flow{lkFlow("ssh", false, "192.168.7.20", "100.64.0.9", "tailscale0")}, "",
		},
		{
			"removing an advertised route under a flow on another interface", lkPrefs(func(p *Prefs) { p.AdvertiseRoutes = []netip.Prefix{route} }), lkPrefsChange(lkPrefs(nil)),
			[]Flow{lkFlow("ssh", true, "192.168.7.20", "198.51.100.7", "eth0")}, "",
		},

		// exit node advertisement removed
		{
			"dropping the exit node advertisement under a public address", lkPrefs(func(p *Prefs) { p.AdvertiseExitNode = true }), lkPrefsChange(lkPrefs(nil)),
			[]Flow{lkFlow("ssh", true, "203.0.113.50", "100.64.0.9", "tailscale0")}, "ssh flow with 100.64.0.9 arrives through the advertised exit node",
		},
		{
			"dropping the exit node advertisement under a tailnet address", lkPrefs(func(p *Prefs) { p.AdvertiseExitNode = true }), lkPrefsChange(lkPrefs(nil)),
			[]Flow{tailnetSSH}, "",
		},
		{
			"dropping the exit node advertisement under an advertised route",
			lkPrefs(func(p *Prefs) {
				p.AdvertiseExitNode = true
				p.AdvertiseRoutes = []netip.Prefix{netip.MustParsePrefix("203.0.113.0/24")}
			}),
			lkPrefsChange(lkPrefs(func(p *Prefs) { p.AdvertiseRoutes = []netip.Prefix{netip.MustParsePrefix("203.0.113.0/24")} })),
			[]Flow{lkFlow("ssh", true, "203.0.113.50", "100.64.0.9", "tailscale0")}, "",
		},
		{
			"no exit node advertisement to drop", lkPrefs(nil), lkPrefsChange(lkPrefs(nil)),
			[]Flow{lkFlow("ssh", true, "203.0.113.50", "100.64.0.9", "tailscale0")}, "",
		},

		// flows that cannot be classified
		{
			"an unknown flow fails whatever the change", lkPrefs(nil), lkPrefsChange(lkPrefs(nil)),
			[]Flow{{Label: "panel", Remote: netip.MustParseAddr("198.51.100.7"), Unknown: "the socket has no owner"}},
			"panel flow with 198.51.100.7 cannot be classified: the socket has no owner",
		},
		{
			"an unknown flow without an address", lkPrefs(nil), lkPrefsChange(lkPrefs(nil)),
			[]Flow{{Label: "agent link", Unknown: "no route to the controller"}},
			"agent link flow with an unknown address cannot be classified: no route to the controller",
		},
		{
			"an unknown flow is named after the dependent ones", lkPrefs(nil), Change{Kind: "logout"},
			[]Flow{{Label: "agent link", Unknown: "no route to the controller"}, tailnetSSH},
			"ssh flow with 100.64.0.9 depends on Tailscale; agent link flow with an unknown address cannot be classified: no route to the controller",
		},

		// several rules
		{
			"one flow can break two rules", lkPrefs(func(p *Prefs) { p.AcceptRoutes = true }), lkPrefsChange(Prefs{}),
			[]Flow{lkFlow("panel", false, "100.64.0.5", "10.2.0.9", "tailscale0")},
			"panel flow with 10.2.0.9 depends on Tailscale; panel flow with 10.2.0.9 uses a route accepted from the tailnet",
		},
		{
			"nothing removed", lkPrefs(func(p *Prefs) {
				p.AcceptRoutes, p.RunSSH, p.ShieldsUp, p.AdvertiseExitNode = true, true, true, true
				p.ExitNode = "nA"
				p.AdvertiseRoutes = []netip.Prefix{route}
			}),
			lkPrefsChange(lkPrefs(func(p *Prefs) {
				p.AcceptRoutes, p.RunSSH, p.ShieldsUp, p.AdvertiseExitNode = true, true, true, true
				p.ExitNode = "nA"
				p.AdvertiseRoutes = []netip.Prefix{route}
			})),
			[]Flow{tailnetSSH, subnet, viaExit, lkFlow("ssh", true, "192.168.7.20", "100.64.0.9", "tailscale0")}, "",
		},
	}
}

func TestLkCheck(t *testing.T) {
	for _, tt := range lkCases() {
		t.Run(tt.name, func(t *testing.T) {
			want := core.Guard{ID: "tailscale.link", OK: tt.fail == "", Message: tt.fail, Overridable: true}
			if want.OK {
				want.Message = lkOK
			}
			if got := Check(tt.cur, tt.chg, tt.flows); got != want {
				t.Fatalf("Check = %+v, want %+v", got, want)
			}
		})
	}
}

func TestLkIsTailscaleAddr(t *testing.T) {
	tests := []struct {
		addr string
		want bool
	}{
		{"100.64.0.0", true},
		{"100.100.100.100", true},
		{"100.127.255.255", true},
		{"100.63.255.255", false},
		{"100.128.0.0", false},
		{"192.0.2.1", false},
		{"127.0.0.1", false},
		{"fd7a:115c:a1e0::1", true},
		{"fd7a:115c:a1e0:ffff:ffff:ffff:ffff:ffff", true},
		{"fd7a:115c:a1e1::1", false},
		{"fd7a:115c:a1df::1", false},
		{"::1", false},
		{"::ffff:100.64.0.1", true},
		{"::ffff:192.0.2.1", false},
	}
	for _, tt := range tests {
		if got := IsTailscaleAddr(netip.MustParseAddr(tt.addr)); got != tt.want {
			t.Errorf("IsTailscaleAddr(%s) = %v, want %v", tt.addr, got, tt.want)
		}
	}
	if IsTailscaleAddr(netip.Addr{}) {
		t.Error("IsTailscaleAddr(zero Addr) = true")
	}
}

func TestLkConstants(t *testing.T) {
	if GuardID != "tailscale.link" || Interface != "tailscale0" {
		t.Fatalf("GuardID = %q, Interface = %q", GuardID, Interface)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
mkdir -p /root/.cache/w4-tmp/4 && export TMPDIR=/root/.cache/w4-tmp/4 GOTOOLCHAIN=local
timeout 300 go test -count=1 ./internal/modules/tailscale/link/
```

Expected: FAIL at build time, `undefined: Flow`, `undefined: Prefs`, `undefined: Change`, `undefined: Check`, `undefined: IsTailscaleAddr`, `undefined: GuardID` and `undefined: Interface`.

- [ ] **Step 3: Implement**

All in `link.go`, with the package comment "Package link holds the rules of the tailscale.link guard (spec 4.1)." and the types, constants and doc comments of Contracts "Link guard rules".

1. `IsTailscaleAddr(a)`: `a.Unmap()` is inside `100.64.0.0/10` or `fd7a:115c:a1e0::/48` (package-level `netip.Prefix` variables). The zero `Addr` is not.
2. A flow *depends on Tailscale* when `OutIface == Interface` or `IsTailscaleAddr(Local)`.
3. `Check(cur, c, flows)` evaluates the rules below in this order, appending one sentence per failing flow (in the order of `flows`) for each rule that fires. A flow with `Unknown` set takes part in no rule except the last one (so an unclassified request flow does not count as one that arrives another way). A *flow sentence* is `<Label> flow with <Remote> <phrase>`, where `<Remote>` is `Remote.String()` or, when `Remote` is not valid, `an unknown address`. Rules 2 to 7 apply only when `c.Kind` is `"prefs"`; rule 1 also applies to `"logout"` and `"relogin"`, for which `c.After` is not read; rule 8 applies to every kind.

   | # | Rule fires when | Sentences |
   |---|---|---|
   | 1 | `Kind` is `logout` or `relogin`, or `Kind` is `prefs` and `After.WantRunning` is false (the field is read as given: it does not compare with `cur.WantRunning`) | for each flow that depends on Tailscale: phrase `depends on Tailscale`; then, when `cur.RunSSH`, the plain sentence `Tailscale SSH is on and a session may use it` |
   | 2 | `Kind` is `prefs`, `After.ShieldsUp` and not `cur.ShieldsUp` | for each `Inbound` flow that depends on Tailscale: phrase `arrives over Tailscale and shields-up would block it`; then, when `cur.RunSSH`, the same plain SSH sentence as rule 1 |
   | 3 | `Kind` is `prefs`, `cur.RunSSH` and not `After.RunSSH`, and no flow has `Request` set, no `Unknown` and does not depend on Tailscale | the plain sentence `Tailscale SSH is on and no request flow is known to arrive another way` |
   | 4 | `Kind` is `prefs`, `cur.AcceptRoutes` and not `After.AcceptRoutes` | for each flow with `OutIface == Interface`, `Default` false and `Remote` not a Tailscale address: phrase `uses a route accepted from the tailnet` |
   | 5 | `Kind` is `prefs`, `cur.ExitNode != ""` and `After.ExitNode != cur.ExitNode` | for each flow with `OutIface == Interface` and `Default` true: phrase `leaves through the exit node` |
   | 6 | `Kind` is `prefs`, and a prefix of `cur.AdvertiseRoutes` is missing from `After.AdvertiseRoutes` (prefixes compared after `Masked()`) | for each such prefix `p`, in the order of `cur.AdvertiseRoutes`, for each `Inbound` flow with `OutIface == Interface` and `p.Contains(Local)`: phrase `arrives through the advertised route <p.Masked()>` |
   | 7 | `Kind` is `prefs`, `cur.AdvertiseExitNode` and not `After.AdvertiseExitNode` | for each `Inbound` flow with `OutIface == Interface`, `Local` not a Tailscale address and `Local` inside none of `cur.AdvertiseRoutes`: phrase `arrives through the advertised exit node` |
   | 8 | any `Kind` | for each flow with `Unknown` set: phrase `cannot be classified: <Unknown>` |

4. Identical sentences are kept once (the first occurrence). The result is `core.Guard{ID: GuardID, Overridable: true, OK: len(sentences) == 0}`; `Message` is the sentences joined with `"; "`, or `no protected flow depends on what this change removes` when there are none.
5. The package declares no state, performs no I/O and reads no clock. `golangci-lint` has no cyclomatic limit here, but keep each rule a small helper so the table above stays readable in the code.

- [ ] **Step 4: Run the tests to verify they pass**

```bash
export TMPDIR=/root/.cache/w4-tmp/4 GOTOOLCHAIN=local
timeout 300 go test -race -count=1 ./internal/modules/tailscale/link/
```

Expected: `ok`.

Then run the Global Constraints gates; the task's package is `./internal/modules/tailscale/link/`:

```bash
timeout 600 go build ./... && timeout 300 go vet ./... && timeout 1500 go test -count=1 ./... && timeout 900 golangci-lint run --allow-serial-runners ./... && timeout 300 go mod tidy -diff
```

Expected: no build or vet output, every test line `ok` or `?`, `0 issues.`, and nothing from `go mod tidy -diff`.

- [ ] **Step 5: Commit**

```bash
git add internal/modules/tailscale/link/link.go internal/modules/tailscale/link/link_test.go
git commit -m "feat(tailscale): add the link guard rules"
rm -rf /root/.cache/w4-tmp/4 && GOTOOLCHAIN=local go clean -testcache
```

### Task 5: web: tailscale types, fixtures and change dialog

**Files:**
- Create: `web/src/modules/tailscale/types.ts`
- Create: `web/src/modules/tailscale/fixtures.ts`
- Create: `web/src/modules/tailscale/tailscale-change.tsx`
- Test: `web/src/modules/tailscale/types.test.ts`
- Test: `web/src/modules/tailscale/fixtures.test.ts`
- Test: `web/src/modules/tailscale/tailscale-change.test.tsx`

Read, do not edit: `web/src/modules/docker/docker-change.tsx` and `types.ts` (the shape to follow), `web/src/modules/firewall/firewall-change.tsx` (the revert-timeout and refresh-on-apply shape), `web/src/components/change-dialog.tsx` (it already takes `target.revertible`, `onApplied` and asks "Revert after (seconds)"; it does not change), `web/src/test/utils.tsx` (`stubFetch`, `renderWithProviders`). `web/src/modules/tailscale/routes.tsx` stays as it is; Task 19 replaces it.

**Interfaces:**
- Consumes: Contracts "Tailscale wire shapes" (the JSON names and meanings of `Status`, `Node`, `Peer`, `Latency`, `RegionLatency`, `Measurement` and `PrefsFields`); the existing `ChangeDialog` and its `ChangeTarget`.
- Produces: Contracts "Web: tailscale" for Tasks 12, 13 and 19, exactly:
  - `types.ts` exports `TAILSCALE_MODULE = "tailscale"`, `TAILSCALE_ACTION_VERSION = 1`, the interfaces `TailscaleNode`, `TailscalePeer`, `TailscaleStatus`, `Measurement`, `RegionLatency`, `TailscaleLatency`, `PrefsFields`, the type `PeerConnection = "direct" | "derp" | "peer-relay" | "none"`, and the guards `isTailscaleStatus(v: unknown): v is TailscaleStatus` and `isTailscaleLatency(v: unknown): v is TailscaleLatency`. Member names are the wire JSON names (`magicDNSSuffix`, `preferredDerp`, `runSSH`, `regionId`, `medianMs`, `lastHandshake: string | null`, `self: TailscaleNode | null`). Every `PrefsFields` member is optional.
  - `fixtures.ts` exports the three constants (values, not functions) `statusRunning: TailscaleStatus`, `statusLoggedOut: TailscaleStatus` and `latencyFixture: TailscaleLatency`.
  - `tailscale-change.tsx` exports `TailscaleChange({ hostId, action, title, input, onClose })` and `TailscaleChangeProps`, with `input: Record<string, unknown> | null` (null keeps the dialog closed).

**Depends on:** none

- [ ] **Step 1: Write the failing tests**

Create `web/src/modules/tailscale/types.test.ts`:

```ts
import { expect, test } from "vitest"
import { latencyFixture, statusLoggedOut, statusRunning } from "./fixtures"
import {
  TAILSCALE_ACTION_VERSION,
  TAILSCALE_MODULE,
  isTailscaleLatency,
  isTailscaleStatus,
  type Measurement,
  type PrefsFields,
  type RegionLatency,
  type TailscaleNode,
  type TailscalePeer,
} from "./types"

type Json = Record<string, unknown>

function typesClone<T>(v: T): Json {
  return structuredClone(v) as Json
}

test("the module id and the action version are the tailscale module's", () => {
  expect(TAILSCALE_MODULE).toBe("tailscale")
  expect(TAILSCALE_ACTION_VERSION).toBe(1)
})

test("isTailscaleStatus accepts the agent's JSON for a running and a logged-out node", () => {
  expect(isTailscaleStatus(JSON.parse(JSON.stringify(statusRunning)))).toBe(true)
  expect(isTailscaleStatus(JSON.parse(JSON.stringify(statusLoggedOut)))).toBe(true)
  expect(isTailscaleStatus({ ...typesClone(statusRunning), prefs: undefined })).toBe(true)
})

test("isTailscaleStatus refuses what is not a complete status", () => {
  const peer = (over: Json) => ({ ...typesClone(statusRunning.peers[0]), ...over })
  const rows: [string, unknown][] = [
    ["null", null],
    ["a string", "Running"],
    ["an array", []],
    ["an empty object", {}],
    ["a missing version", { ...typesClone(statusRunning), version: undefined }],
    ["a backend state that is not a string", { ...typesClone(statusRunning), backendState: 3 }],
    ["a self that is not a node", { ...typesClone(statusRunning), self: "web-1" }],
    ["a self without addresses", { ...typesClone(statusRunning), self: { ...typesClone(statusRunning.self), addresses: undefined } }],
    ["a missing runSSH", { ...typesClone(statusRunning), runSSH: undefined }],
    ["a runSSH that is not a boolean", { ...typesClone(statusRunning), runSSH: "no" }],
    ["prefs that are an array", { ...typesClone(statusRunning), prefs: [] }],
    ["prefs that are null", { ...typesClone(statusRunning), prefs: null }],
    ["peers that are null", { ...typesClone(statusRunning), peers: null }],
    ["a peer that is a string", { ...typesClone(statusRunning), peers: ["db-1"] }],
    ["a peer with an unknown connection", { ...typesClone(statusRunning), peers: [peer({ connection: "wireguard" })] }],
    ["a peer with a lastHandshake that is a number", { ...typesClone(statusRunning), peers: [peer({ lastHandshake: 1 })] }],
    ["a peer without online", { ...typesClone(statusRunning), peers: [peer({ online: undefined })] }],
    ["health that is not strings", { ...typesClone(statusRunning), health: [1] }],
  ]
  for (const [name, value] of rows) expect(isTailscaleStatus(value), name).toBe(false)
})

test("isTailscaleLatency accepts the agent's JSON and refuses an incomplete answer", () => {
  expect(isTailscaleLatency(JSON.parse(JSON.stringify(latencyFixture)))).toBe(true)
  expect(isTailscaleLatency({ regions: [] })).toBe(true)
  const region = typesClone(latencyFixture.regions[0])
  const rows: [string, unknown][] = [
    ["null", null],
    ["an empty object", {}],
    ["regions that are null", { regions: null }],
    ["a region that is a number", { regions: [1] }],
    ["a region without a code", { regions: [{ ...region, code: undefined }] }],
    ["a regionId that is a string", { regions: [{ ...region, regionId: "1" }] }],
    ["a missing stun measurement", { regions: [{ ...region, stun: undefined }] }],
    ["an https measurement without available", { regions: [{ ...region, https: { medianMs: 1, reason: "" } }] }],
    ["a medianMs that is a string", { regions: [{ ...region, https: { available: true, medianMs: "9", reason: "" } }] }],
  ]
  for (const [name, value] of rows) expect(isTailscaleLatency(value), name).toBe(false)
})

// The types below are checked by `pnpm typecheck`: Vitest strips types, so a
// missing, misspelled or mistyped member of the agent's JSON fails the
// compilation of this file.
test("the wire types carry every member the agent sends", () => {
  const node: TailscaleNode = { id: "n1", hostName: "web-1", dnsName: "web-1.tail1234.ts.net", os: "linux", addresses: ["100.64.0.5"], tags: [] }
  const peer: TailscalePeer = {
    ...node, online: true, connection: "peer-relay", endpoint: "", region: "", exitNode: false, exitNodeOption: true, lastHandshake: null,
  }
  const measurement: Measurement = { available: false, medianMs: 0, reason: "no STUN endpoint" }
  const region: RegionLatency = { regionId: 1, code: "nyc", name: "New York City", stun: measurement, https: { available: true, medianMs: 20, reason: "" } }
  const prefs: PrefsFields = {
    wantRunning: true, advertiseRoutes: ["10.0.0.0/24"], exitNode: "", advertiseExitNode: false, acceptRoutes: true, shieldsUp: false, runSSH: true,
  }
  const unset: PrefsFields = {}
  expect([node, peer, measurement, region, prefs, unset]).toHaveLength(6)
  expect(JSON.parse(JSON.stringify(peer)) as TailscalePeer).toEqual(peer)
  expect(JSON.parse(JSON.stringify(prefs)) as PrefsFields).toEqual(prefs)
  expect(JSON.stringify(unset)).toBe("{}")
})
```

Create `web/src/modules/tailscale/fixtures.test.ts`:

```ts
import { expect, test } from "vitest"
import { latencyFixture, statusLoggedOut, statusRunning } from "./fixtures"
import { isTailscaleLatency, isTailscaleStatus } from "./types"

test("statusRunning is a node on a tailnet with one peer of each connection kind", () => {
  expect(isTailscaleStatus(statusRunning)).toBe(true)
  expect(statusRunning.backendState).toBe("Running")
  expect(statusRunning.self?.dnsName).toBe("web-1.tail1234.ts.net")
  expect(statusRunning.self?.addresses).toEqual(["100.64.0.5", "fd7a:115c:a1e0::5"])
  expect(statusRunning.magicDNSSuffix).toBe("tail1234.ts.net")
  expect(statusRunning.preferredDerp).toBe("fra")
  expect(statusRunning.peers.map((p) => p.connection)).toEqual(["derp", "direct", "peer-relay", "none"])
  const [derp, direct, relay, offline] = statusRunning.peers
  expect([derp?.region, direct?.endpoint, relay?.region, offline?.online]).toEqual(["fra", "203.0.113.7:41641", "", false])
  expect(offline?.lastHandshake).toBeNull()
})

test("statusRunning lists its peers by DNS name, as the agent does", () => {
  const names = statusRunning.peers.map((p) => p.dnsName)
  expect(names).toEqual([...names].sort())
})

test("statusLoggedOut is a node that needs login", () => {
  expect(isTailscaleStatus(statusLoggedOut)).toBe(true)
  expect(statusLoggedOut.backendState).toBe("NeedsLogin")
  expect(statusLoggedOut.self).toBeNull()
  expect(statusLoggedOut.peers).toEqual([])
  expect([statusLoggedOut.tailnet, statusLoggedOut.magicDNSSuffix, statusLoggedOut.preferredDerp]).toEqual(["", "", ""])
})

test("latencyFixture has three regions by id and one without STUN", () => {
  expect(isTailscaleLatency(latencyFixture)).toBe(true)
  const ids = latencyFixture.regions.map((r) => r.regionId)
  expect(ids).toHaveLength(3)
  expect(ids).toEqual([...ids].sort((a, b) => a - b))
  const noStun = latencyFixture.regions.filter((r) => !r.stun.available)
  expect(noStun.map((r) => r.code)).toEqual(["syd"])
  expect(noStun[0]?.stun).toEqual({ available: false, medianMs: 0, reason: "no STUN endpoint" })
  for (const r of latencyFixture.regions) {
    expect(r.https.available, r.code).toBe(true)
    expect(r.https.reason, r.code).toBe("")
  }
})

test("the fixtures survive a JSON round trip unchanged", () => {
  for (const v of [statusRunning, statusLoggedOut, latencyFixture]) expect(JSON.parse(JSON.stringify(v))).toEqual(v)
})
```

Create `web/src/modules/tailscale/tailscale-change.test.tsx`:

```tsx
import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test, vi } from "vitest"
import type { Plan } from "@/api/types"
import { renderWithProviders, stubFetch, type FetchCall, type StubHandler } from "@/test/utils"
import { TailscaleChange } from "./tailscale-change"

function tchPlan(action: string, over: Partial<Plan> = {}): Plan {
  return {
    planId: "p1", hostId: "h1", action, version: 1, inputHash: "h",
    diff: [{ op: "~", text: `${action} web-1` }], guards: [], expiresAt: "2026-10-31T09:00:00Z", confirmation: null, ...over,
  }
}

const TCH_APPLIED = { id: "c1", state: "completed", class: "compensable", error: null, output: null, revertDeadline: null }

function tchAvailability(actions: Record<string, string | null>): StubHandler {
  return {
    body: {
      module: "tailscale", available: true, reason: null,
      actions: Object.entries(actions).map(([name, reason]) => ({ name, available: reason === null, reason })),
    },
  }
}

function tchRender(action: string, input: Record<string, unknown> | null, routes: Record<string, StubHandler> = {}) {
  const calls = stubFetch({
    "GET /api/v1/hosts/h1/modules/tailscale/availability": tchAvailability({ [action]: null }),
    [`POST /api/v1/hosts/h1/actions/${action}/v1/plan`]: { body: tchPlan(action) },
    [`POST /api/v1/hosts/h1/actions/${action}/v1/apply`]: { body: TCH_APPLIED },
    ...routes,
  })
  const onClose = vi.fn()
  const result = renderWithProviders(<TailscaleChange hostId="h1" action={action} title="Change the node" input={input} onClose={onClose} />)
  return { calls, onClose, ...result }
}

async function tchApply(calls: FetchCall[]) {
  const dialog = await screen.findByRole("dialog", { name: "Change the node" })
  const apply = await within(dialog).findByRole("button", { name: "Apply" })
  await waitFor(() => expect(apply.hasAttribute("disabled")).toBe(false))
  await userEvent.setup().click(apply)
  await waitFor(() => expect(calls.some((c) => c.path.endsWith("/apply"))).toBe(true))
  return dialog
}

test("stays closed and asks nothing while the input is null", () => {
  const { calls } = tchRender("tailscale.package.install", null)
  expect(screen.queryByRole("dialog")).toBeNull()
  expect(calls).toEqual([])
})

test("plans the action on the host and applies it without a revert timeout", async () => {
  const { calls } = tchRender("tailscale.package.install", {})
  const dialog = await screen.findByRole("dialog", { name: "Change the node" })
  expect((await within(dialog).findByLabelText("Planned changes")).textContent).toBe("~ tailscale.package.install web-1")
  expect(within(dialog).queryByLabelText("Revert after (seconds)")).toBeNull()
  await tchApply(calls)
  const plan = calls.find((c) => c.path.endsWith("/plan"))
  expect(plan?.path).toBe("/api/v1/hosts/h1/actions/tailscale.package.install/v1/plan")
  expect(plan?.body).toEqual({ input: {} })
  expect(calls.find((c) => c.path.endsWith("/apply"))?.body).toEqual({ planId: "p1" })
})

test("tailscale.prefs.set asks for the revert timeout and sends it with the apply", async () => {
  const { calls } = tchRender("tailscale.prefs.set", { shieldsUp: true })
  const dialog = await screen.findByRole("dialog", { name: "Change the node" })
  const field = await within(dialog).findByLabelText("Revert after (seconds)")
  expect((field as HTMLInputElement).value).toBe("60")
  const user = userEvent.setup()
  await user.clear(field)
  await user.type(field, "30")
  await tchApply(calls)
  expect(calls.find((c) => c.path.endsWith("/plan"))?.body).toEqual({ input: { shieldsUp: true } })
  expect(calls.find((c) => c.path.endsWith("/apply"))?.body).toEqual({ planId: "p1", confirmTimeoutSeconds: 30 })
})

test("an action that asks for the typed name applies only after it is typed", async () => {
  const action = "tailscale.node.logout"
  const { calls } = tchRender(action, {}, { [`POST /api/v1/hosts/h1/actions/${action}/v1/plan`]: { body: tchPlan(action, { confirmation: "web-1" }) } })
  const dialog = await screen.findByRole("dialog", { name: "Change the node" })
  const user = userEvent.setup()
  const apply = await within(dialog).findByRole("button", { name: "Apply" })
  expect(apply.hasAttribute("disabled")).toBe(true)
  expect(within(dialog).queryByLabelText("Revert after (seconds)")).toBeNull()
  await user.type(within(dialog).getByLabelText("Type web-1 to confirm"), "web-1")
  await waitFor(() => expect(apply.hasAttribute("disabled")).toBe(false))
  await user.click(apply)
  await waitFor(() => expect(calls.some((c) => c.path.endsWith("/apply"))).toBe(true))
  expect(calls.find((c) => c.path.endsWith("/apply"))?.body).toEqual({ planId: "p1", confirmation: "web-1" })
})

const TCH_DOWN = "tailscaled does not answer on its socket"

test.each([
  ["tailscale.node.logout", true],
  ["tailscale.package.install", false],
])("%s is applied only when the host reports it available (blocked: %s)", async (action, blocked) => {
  tchRender(action, {}, {
    "GET /api/v1/hosts/h1/modules/tailscale/availability": tchAvailability({ "tailscale.node.logout": TCH_DOWN, "tailscale.package.install": null }),
  })
  const dialog = await screen.findByRole("dialog", { name: "Change the node" })
  await within(dialog).findByLabelText("Planned changes")
  if (blocked) expect((await within(dialog).findByText(TCH_DOWN)).textContent).toBe(TCH_DOWN)
  await waitFor(() => expect(within(dialog).getByRole("button", { name: "Apply" }).hasAttribute("disabled")).toBe(blocked))
})

test("closing the dialog reports the close and refetches only that host's resources", async () => {
  const { client, onClose } = tchRender("tailscale.package.install", {})
  client.setQueryData(["resource", "h1", "tailscale.status"], { backendState: "Running" })
  client.setQueryData(["resource", "h1", "tailscale.latency"], { regions: [] })
  client.setQueryData(["resource", "h2", "tailscale.status"], { backendState: "Running" })
  const dialog = await screen.findByRole("dialog", { name: "Change the node" })
  await within(dialog).findByLabelText("Planned changes")
  await userEvent.setup().keyboard("{Escape}")
  await waitFor(() => expect(onClose).toHaveBeenCalledTimes(1))
  expect(client.getQueryState(["resource", "h1", "tailscale.status"])?.isInvalidated).toBe(true)
  expect(client.getQueryState(["resource", "h1", "tailscale.latency"])?.isInvalidated).toBe(true)
  expect(client.getQueryState(["resource", "h2", "tailscale.status"])?.isInvalidated).toBe(false)
})

test("an applied change refetches the host's resources before the dialog closes", async () => {
  const { calls, client, onClose } = tchRender("tailscale.prefs.set", { shieldsUp: true })
  client.setQueryData(["resource", "h1", "tailscale.status"], { backendState: "Running" })
  client.setQueryData(["resource", "h2", "tailscale.status"], { backendState: "Running" })
  await tchApply(calls)
  await waitFor(() => expect(client.getQueryState(["resource", "h1", "tailscale.status"])?.isInvalidated).toBe(true))
  expect(client.getQueryState(["resource", "h2", "tailscale.status"])?.isInvalidated).toBe(false)
  expect(onClose).not.toHaveBeenCalled()
})
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mkdir -p /root/.cache/w4-tmp/5 && export TMPDIR=/root/.cache/w4-tmp/5 GOTOOLCHAIN=local && timeout 300 pnpm -C web exec vitest run src/modules/tailscale/types.test.ts src/modules/tailscale/fixtures.test.ts src/modules/tailscale/tailscale-change.test.tsx`
Expected: FAIL; all three files fail to load with `Failed to resolve import "./fixtures"` or `"./types"` or `"./tailscale-change"` (the modules do not exist). Quote this output in the report.

- [ ] **Step 3: Implement**

1. `types.ts`: declare the exports listed under Interfaces. `TailscaleNode` is `{ id, hostName, dnsName, os: string; addresses: string[]; tags: string[] }`. `TailscalePeer` extends it with `online: boolean`, `connection: PeerConnection`, `endpoint: string`, `region: string`, `exitNode: boolean`, `exitNodeOption: boolean`, `lastHandshake: string | null`. `TailscaleStatus` is `{ version, backendState: string; self: TailscaleNode | null; tailnet, magicDNSSuffix, preferredDerp: string; runSSH: boolean; prefs?: PrefsFields; peers: TailscalePeer[]; health: string[] }` (`prefs` is optional in the mirror; the agent always sends it, with all seven members); `backendState` is a plain `string` (tailscaled may add states). `Measurement` is `{ available: boolean; medianMs: number; reason: string }`, `RegionLatency` is `{ regionId: number; code: string; name: string; stun: Measurement; https: Measurement }`, `TailscaleLatency` is `{ regions: RegionLatency[] }`. `PrefsFields` has `wantRunning?: boolean`, `advertiseRoutes?: string[]`, `exitNode?: string`, `advertiseExitNode?: boolean`, `acceptRoutes?: boolean`, `shieldsUp?: boolean`, `runSSH?: boolean`.
2. The guards check structure at runtime and ignore members they do not know. `isTailscaleStatus` is true only for a plain object (not null, not an array) whose `version`, `backendState`, `tailnet`, `magicDNSSuffix` and `preferredDerp` are strings, `runSSH` is a boolean, `prefs` is absent or a plain object (not null, not an array), `self` is null or a node, `peers` is an array of peers and `health` an array of strings. A node has string `id`, `hostName`, `dnsName`, `os` and string arrays `addresses` and `tags`. A peer is a node whose `online`, `exitNode` and `exitNodeOption` are booleans, `connection` is one of the four `PeerConnection` strings, `endpoint` and `region` are strings and `lastHandshake` is null or a string. `isTailscaleLatency` is true only for a plain object whose `regions` is an array of regions, a region having a numeric `regionId`, string `code` and `name`, and `stun` and `https` measurements whose `available` is a boolean, `medianMs` a number and `reason` a string. A `null` where an array is expected (`peers`, `health`, `regions`, `addresses`, `tags`) is refused.
3. `fixtures.ts`: export the three constants with these exact values (the tests pin the ones they assert; keep the rest as listed so the pages' tests share one picture). Add a header comment saying they are test fixtures. `statusRunning`: `version "1.104.1"`, `backendState "Running"`, `self` `{ id "n1", hostName "web-1", dnsName "web-1.tail1234.ts.net", os "linux", addresses ["100.64.0.5", "fd7a:115c:a1e0::5"], tags ["tag:server"] }`, `tailnet "example.com"`, `magicDNSSuffix "tail1234.ts.net"`, `preferredDerp "fra"`, `runSSH false`, `prefs { wantRunning true, advertiseRoutes [], exitNode "", advertiseExitNode false, acceptRoutes false, shieldsUp false, runSSH false }`, `health []`, and four peers in DNS-name order, each with `online`, `exitNode false` unless noted, `region ""`, `endpoint ""` unless noted: `n2` `db-1` (`dnsName "db-1.tail1234.ts.net"`, os `linux`, addresses `["100.64.0.7"]`, tags `["tag:server"]`, online, connection `derp`, region `fra`, exitNodeOption false, lastHandshake `"2026-10-30T11:59:00Z"`); `n3` `laptop` (os `macOS`, addresses `["100.64.0.8"]`, tags `[]`, online, connection `direct`, endpoint `"203.0.113.7:41641"`, exitNodeOption true, lastHandshake `"2026-10-30T11:59:30Z"`); `n4` `nas` (os `linux`, addresses `["100.64.0.9"]`, tags `[]`, online, connection `peer-relay`, exitNodeOption false, lastHandshake `"2026-10-30T11:58:00Z"`); `n5` `old-box` (os `linux`, addresses `["100.64.0.10"]`, tags `[]`, offline, connection `none`, exitNodeOption false, lastHandshake null). The DNS names are `<hostName>.tail1234.ts.net`. `statusLoggedOut`: version `"1.104.1"`, backendState `"NeedsLogin"`, `self null`, `tailnet`, `magicDNSSuffix` and `preferredDerp` empty strings, `runSSH false`, `prefs { wantRunning true, advertiseRoutes [], exitNode "", advertiseExitNode false, acceptRoutes false, shieldsUp false, runSSH false }`, `peers []`, `health ["You are logged out."]`. `latencyFixture`: regions `{ regionId 1, code "nyc", name "New York City", stun {available true, medianMs 18.4, reason ""}, https {available true, medianMs 21.7, reason ""} }`, `{ regionId 4, code "fra", name "Frankfurt", stun {true, 9.2, ""}, https {true, 12.5, ""} }` and `{ regionId 17, code "syd", name "Sydney", stun {available false, medianMs 0, reason "no STUN endpoint"}, https {true, 231.8, ""} }`.
4. `tailscale-change.tsx`: `TailscaleChange` has the shape of `DockerChange` and uses the firewall's refresh. It renders `ChangeDialog` with `open` true exactly when `input !== null`, the given `title`, `input={input ?? undefined}` and target `{ name: action, version: TAILSCALE_ACTION_VERSION, hostId, module: TAILSCALE_MODULE, revertible: action === "tailscale.prefs.set" }`; only `tailscale.prefs.set` is reverted by a timer, so only it asks the revert timeout (a private constant holds the action name). `refresh` invalidates the query key `["resource", hostId]` (every resource of that host, no other host). `onApplied` calls `refresh`, because a revert by the timer raises no event; closing the dialog (`onOpenChange(false)`) calls `onClose()` and then `refresh`. A doc comment says this.
5. Do not edit `change-dialog.tsx`, `routes.tsx` or any docker or firewall file. No new dependency.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `export TMPDIR=/root/.cache/w4-tmp/5 GOTOOLCHAIN=local && timeout 300 pnpm -C web exec vitest run src/modules/tailscale/types.test.ts src/modules/tailscale/fixtures.test.ts src/modules/tailscale/tailscale-change.test.tsx`
Expected: PASS, 3 files, 18 tests.

- [ ] **Step 5: Run the gates**

Run: `export TMPDIR=/root/.cache/w4-tmp/5 GOTOOLCHAIN=local && timeout 600 pnpm -C web lint && timeout 600 pnpm -C web typecheck && timeout 900 pnpm -C web test`
Expected: lint prints no warnings, typecheck prints no errors, every web test passes.

Run: `export TMPDIR=/root/.cache/w4-tmp/5 GOTOOLCHAIN=local && timeout 600 go build ./... && timeout 300 go vet ./... && timeout 1500 go test -count=1 ./... && timeout 900 golangci-lint run --allow-serial-runners ./... && timeout 300 go mod tidy -diff`
Expected: no build or vet output, every test line `ok` or `?`, `0 issues.`, and no `go mod tidy` output (this task changes no Go file; the gates still run and are recorded).

- [ ] **Step 6: Commit**

```bash
rm -rf /root/.cache/w4-tmp/5 && GOTOOLCHAIN=local go clean -testcache
git add web/src/modules/tailscale/types.ts web/src/modules/tailscale/types.test.ts web/src/modules/tailscale/fixtures.ts web/src/modules/tailscale/fixtures.test.ts web/src/modules/tailscale/tailscale-change.tsx web/src/modules/tailscale/tailscale-change.test.tsx
git commit -m "feat(web): add tailscale types and change dialog"
```

### Task 6: listen: Tailscale certificate source and MagicDNS name

**Files:**
- Modify: `internal/controller/listen/tailscale_cert.go` (replace the stub; `fallThrough` is deleted)
- Modify: `internal/core/coretest/dispatcher.go` (add `ReadInternalFunc` and `ReadInternal` to `FakeDispatcher`)
- Test: `internal/controller/listen/tailscale_cert_test.go` (new)

Read, do not edit: `internal/controller/listen/sources.go` (frozen: `CertSource`, `SourceDeps`, `certSources`, `extraNames`), `leaf.go` (`Leaf.useSources`, `GetCertificate`, `Ensure`, `extraNames`), `service.go`, and the existing tests `sources_test.go`, `leaf_test.go` and `service_test.go`, whose helpers this test reuses (`newLeaf`, `csDNSNames`, `addrs`, `logBuffer`, `testStart`) and whose `SourceDeps{LocalHostID: "H1"}` (no dispatcher) must keep working. `internal/core/ext/ext.go` (`ext.Dispatcher.ReadInternal`).

**Interfaces:**
- Consumes: Contracts "Listen certificate source" and the JSON shape of `CertPair` from "Tailscale wire shapes" (`name`, `certPem`, `keyPem`, `notAfter`). The package must not import `internal/modules/tailscale`: it declares a private struct with the `name`, `certPem` and `keyPem` JSON members.
- Produces: `tailscaleCertSource(d SourceDeps) CertSource` and `tailscaleNames(ctx context.Context, d SourceDeps) ([]string, error)` with their stub signatures; and `coretest.FakeDispatcher.ReadInternalFunc`, which makes `*FakeDispatcher` satisfy `ext.Dispatcher`.

**Items:** 60

**Depends on:** none

- [ ] **Step 1: Write the failing test**

Create `internal/controller/listen/tailscale_cert_test.go`:

```go
package listen

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"log/slog"
	"math/big"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
)

const tcName = "panel.tail1234.ts.net"

// tcCall is one tailscale.cert.get read as the fake dispatcher saw it.
type tcCall struct {
	principal core.Principal
	host      string
	ref       core.ActionRef
	input     string
	left      time.Duration // time to the context's deadline when the read arrived; 0 without one
}

// tcReader is the dispatcher side of the certificate reads: it records every
// call and answers from answer, called with the call's 1-based number.
type tcReader struct {
	mu     sync.Mutex
	calls  []tcCall
	answer func(ctx context.Context, n int) (json.RawMessage, error)
}

func (r *tcReader) read(ctx context.Context, p core.Principal, host string, ref core.ActionRef, in json.RawMessage) (json.RawMessage, error) {
	var left time.Duration
	if dl, ok := ctx.Deadline(); ok {
		left = time.Until(dl)
	}
	r.mu.Lock()
	r.calls = append(r.calls, tcCall{principal: p, host: host, ref: ref, input: string(in), left: left})
	n := len(r.calls)
	answer := r.answer
	r.mu.Unlock()
	return answer(ctx, n)
}

func (r *tcReader) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func (r *tcReader) deps(log *slog.Logger) SourceDeps {
	return SourceDeps{Dispatch: &coretest.FakeDispatcher{ReadInternalFunc: r.read}, LocalHostID: "H1", Log: log}
}

// tcAlways answers every read with body.
func tcAlways(body json.RawMessage) func(context.Context, int) (json.RawMessage, error) {
	return func(context.Context, int) (json.RawMessage, error) { return body, nil }
}

// tcParts makes a self-signed certificate valid from notBefore to notAfter and
// returns its PEM, its private key's PEM and its DER.
func tcParts(t *testing.T, name string, notBefore, notAfter time.Time) (certPEM, keyPEM string, der []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: name}, DNSNames: []string{name},
		NotBefore: notBefore, NotAfter: notAfter, KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true,
	}
	der, err = x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	keyPEM = string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
	return certPEM, keyPEM, der
}

// tcBody is the JSON of a tailscale.cert.get answer.
func tcBody(t *testing.T, name, certPEM, keyPEM string, notAfter time.Time) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(map[string]any{"name": name, "certPem": certPEM, "keyPem": keyPEM, "notAfter": notAfter})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// tcPair is a fresh answer for name and the DER of its certificate.
func tcPair(t *testing.T, name string, notBefore, notAfter time.Time) (json.RawMessage, []byte) {
	t.Helper()
	certPEM, keyPEM, der := tcParts(t, name, notBefore, notAfter)
	return tcBody(t, name, certPEM, keyPEM, notAfter), der
}

func tcNewCache(r *tcReader, log *slog.Logger) (*pairCache, *coretest.FakeClock) {
	clock := coretest.NewFakeClock(testStart)
	return newPairCache(r.deps(log), clock), clock
}

func tcServed(t *testing.T, c *pairCache, serverName string) []byte {
	t.Helper()
	cert, err := c.Certificate(&tls.ClientHelloInfo{ServerName: serverName})
	if err != nil {
		t.Fatalf("Certificate(%q) error: %v", serverName, err)
	}
	if cert == nil {
		return nil
	}
	return cert.Certificate[0]
}

func TestTcNamesReadsThePairAsTheListenPrincipal(t *testing.T) {
	body, _ := tcPair(t, tcName, testStart, testStart.Add(90*time.Hour))
	r := &tcReader{answer: tcAlways(body)}
	c, _ := tcNewCache(r, nil)
	names, err := c.names(context.Background())
	if err != nil || !slices.Equal(names, []string{tcName}) {
		t.Fatalf("names = %v, %v; want [%s]", names, err, tcName)
	}
	if r.count() != 1 {
		t.Fatalf("%d reads; want 1", r.count())
	}
	call := r.calls[0]
	if call.principal != (core.Principal{Actor: "system:listen.cert", Scope: core.ScopeAdmin}) || call.host != "H1" ||
		call.ref != (core.ActionRef{Name: "tailscale.cert.get", Version: 1}) || call.input != "{}" {
		t.Errorf("read = %+v", call)
	}
	if call.left <= 9*time.Second || call.left > 10*time.Second {
		t.Errorf("the read had %v to its deadline; want the 10 s bound", call.left)
	}
	if names, _ := c.names(context.Background()); !slices.Equal(names, []string{tcName}) || r.count() != 1 {
		t.Errorf("a second names = %v after %d reads; want the cached pair, 1 read", names, r.count())
	}
}

func TestTcCertificateAnswersOnlyItsName(t *testing.T) {
	body, der := tcPair(t, tcName, testStart, testStart.Add(90*time.Hour))
	r := &tcReader{answer: tcAlways(body)}
	c, _ := tcNewCache(r, nil)
	if _, err := c.names(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		serverName string
		serves     bool
	}{
		{tcName, true},
		{"PANEL.Tail1234.TS.net", true},
		{tcName + ".", true},
		{"", false},
		{"panel", false},
		{"other.tail1234.ts.net", false},
		{"xpanel.tail1234.ts.net", false},
		{"100.64.0.1", false},
	} {
		got := tcServed(t, c, tt.serverName)
		if tt.serves != (got != nil) || (tt.serves && !bytes.Equal(got, der)) {
			t.Errorf("ServerName %q: served the pair = %v, want %v", tt.serverName, got != nil, tt.serves)
		}
	}
	c.wait()
	if r.count() != 1 {
		t.Errorf("%d reads; handshakes on a fresh pair must not read", r.count())
	}
}

func TestTcCertificateWithoutAPairFallsThroughAndLoadsOnce(t *testing.T) {
	body, der := tcPair(t, tcName, testStart, testStart.Add(90*time.Hour))
	release := make(chan struct{})
	r := &tcReader{answer: func(context.Context, int) (json.RawMessage, error) {
		<-release
		return body, nil
	}}
	c, _ := tcNewCache(r, nil)
	for range 3 {
		if got := tcServed(t, c, tcName); got != nil {
			t.Fatal("a handshake before any pair was loaded was served a certificate")
		}
	}
	close(release)
	c.wait()
	if r.count() != 1 {
		t.Fatalf("%d reads for three handshakes; want 1", r.count())
	}
	if got := tcServed(t, c, tcName); !bytes.Equal(got, der) {
		t.Error("the pair loaded in the background is not served")
	}
}

func TestTcRefreshesInTheBackgroundAtTwoThirdsOfLife(t *testing.T) {
	first, firstDER := tcPair(t, tcName, testStart, testStart.Add(90*time.Hour))
	second, secondDER := tcPair(t, tcName, testStart.Add(60*time.Hour), testStart.Add(150*time.Hour))
	r := &tcReader{answer: func(_ context.Context, n int) (json.RawMessage, error) {
		if n == 1 {
			return first, nil
		}
		return second, nil
	}}
	c, clock := tcNewCache(r, nil)
	if _, err := c.names(context.Background()); err != nil {
		t.Fatal(err)
	}
	clock.Advance(60*time.Hour - time.Second)
	if got := tcServed(t, c, tcName); !bytes.Equal(got, firstDER) {
		t.Fatal("the pair is not served just before two thirds of its life")
	}
	c.wait()
	if r.count() != 1 {
		t.Fatalf("%d reads before two thirds of the life; want 1", r.count())
	}
	clock.Advance(time.Second)
	if got := tcServed(t, c, tcName); !bytes.Equal(got, firstDER) {
		t.Fatal("the handshake that finds the pair due must still be served the old pair")
	}
	c.wait()
	if r.count() != 2 {
		t.Fatalf("%d reads at two thirds of the life; want 2", r.count())
	}
	if got := tcServed(t, c, tcName); !bytes.Equal(got, secondDER) {
		t.Error("the refreshed pair is not served")
	}
}

func TestTcExpiredPairIsNotServed(t *testing.T) {
	body, _ := tcPair(t, tcName, testStart, testStart.Add(90*time.Hour))
	r := &tcReader{answer: func(_ context.Context, n int) (json.RawMessage, error) {
		if n == 1 {
			return body, nil
		}
		return nil, errors.New("tailscaled does not answer")
	}}
	c, clock := tcNewCache(r, nil)
	if _, err := c.names(context.Background()); err != nil {
		t.Fatal(err)
	}
	clock.Advance(90 * time.Hour)
	if got := tcServed(t, c, tcName); got != nil {
		t.Error("a pair at its NotAfter is still served")
	}
	c.wait()
}

func TestTcFailedRefreshIsNotRetriedForAMinute(t *testing.T) {
	r := &tcReader{answer: func(context.Context, int) (json.RawMessage, error) {
		return nil, errors.New("tailscaled does not answer")
	}}
	c, clock := tcNewCache(r, nil)
	tcServed(t, c, tcName)
	c.wait()
	tcServed(t, c, tcName)
	c.wait()
	if r.count() != 1 {
		t.Fatalf("%d reads right after a failure; want 1", r.count())
	}
	clock.Advance(time.Minute - time.Second)
	tcServed(t, c, tcName)
	c.wait()
	if r.count() != 1 {
		t.Fatalf("%d reads before the minute passed; want 1", r.count())
	}
	clock.Advance(time.Second)
	tcServed(t, c, tcName)
	c.wait()
	if r.count() != 2 {
		t.Fatalf("%d reads after the minute passed; want 2", r.count())
	}
}

func TestTcReadErrorsFallThroughAndLogOncePerDistinctError(t *testing.T) {
	body, _ := tcPair(t, tcName, testStart, testStart.Add(90*time.Hour))
	failures := []string{"tailscaled does not answer", "tailscaled does not answer", "the agent is offline", "the agent is offline", "tailscaled does not answer"}
	r := &tcReader{answer: func(_ context.Context, n int) (json.RawMessage, error) {
		if n <= len(failures) {
			return nil, errors.New(failures[n-1])
		}
		return body, nil
	}}
	buf := &logBuffer{}
	c, _ := tcNewCache(r, slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	for i := range failures {
		if names, err := c.names(context.Background()); names != nil || err != nil {
			t.Fatalf("names after failure %d = %v, %v; want none and no error", i+1, names, err)
		}
	}
	out := buf.String()
	if n := strings.Count(out, "tailscaled does not answer"); n != 2 {
		t.Errorf("tailscaled error logged %d times; want 2 (it changed and came back)", n)
	}
	if n := strings.Count(out, "the agent is offline"); n != 1 {
		t.Errorf("agent error logged %d times; want 1", n)
	}
	if !strings.Contains(out, "level=DEBUG") || strings.Contains(out, "level=WARN") || strings.Contains(out, "level=ERROR") {
		t.Errorf("read errors must be logged at debug level only: %s", out)
	}
	if names, err := c.names(context.Background()); err != nil || !slices.Equal(names, []string{tcName}) {
		t.Errorf("names after the read recovers = %v, %v", names, err)
	}
}

func TestTcNamesKeepsTheCachedNameWhenARefreshFails(t *testing.T) {
	body, der := tcPair(t, tcName, testStart, testStart.Add(90*time.Hour))
	r := &tcReader{answer: func(_ context.Context, n int) (json.RawMessage, error) {
		if n == 1 {
			return body, nil
		}
		return nil, errors.New("tailscaled does not answer")
	}}
	c, clock := tcNewCache(r, nil)
	if _, err := c.names(context.Background()); err != nil {
		t.Fatal(err)
	}
	clock.Advance(60 * time.Hour)
	names, err := c.names(context.Background())
	if err != nil || !slices.Equal(names, []string{tcName}) || r.count() != 2 {
		t.Fatalf("names = %v, %v after %d reads; want the cached name after a failed refresh", names, err, r.count())
	}
	if got := tcServed(t, c, tcName); !bytes.Equal(got, der) {
		t.Error("the cached pair stopped being served after a failed refresh inside its life")
	}
	c.wait()
}

func TestTcBadAnswersAreNeverCached(t *testing.T) {
	notBefore, notAfter := testStart, testStart.Add(90*time.Hour)
	certPEM, keyPEM, _ := tcParts(t, tcName, notBefore, notAfter)
	_, otherKeyPEM, _ := tcParts(t, tcName, notBefore, notAfter)
	tests := []struct {
		name string
		body json.RawMessage
	}{
		{"not JSON", json.RawMessage(`not json`)},
		{"an empty object", json.RawMessage(`{}`)},
		{"no certificate", tcBody(t, tcName, "", keyPEM, notAfter)},
		{"a certificate that is not PEM", tcBody(t, tcName, "certificate", keyPEM, notAfter)},
		{"a key that is not the certificate's", tcBody(t, tcName, certPEM, otherKeyPEM, notAfter)},
		{"an empty name", tcBody(t, "", certPEM, keyPEM, notAfter)},
		{"an IPv4 literal", tcBody(t, "100.64.0.1", certPEM, keyPEM, notAfter)},
		{"an IPv6 literal", tcBody(t, "fd7a:115c:a1e0::1", certPEM, keyPEM, notAfter)},
		{"a non-ASCII name", tcBody(t, "pänel.ts.net", certPEM, keyPEM, notAfter)},
		{"an empty label", tcBody(t, "panel..ts.net", certPEM, keyPEM, notAfter)},
		{"a label that starts with a hyphen", tcBody(t, "-panel.ts.net", certPEM, keyPEM, notAfter)},
		{"a label that ends with a hyphen", tcBody(t, "panel-.ts.net", certPEM, keyPEM, notAfter)},
		{"an underscore", tcBody(t, "pa_nel.ts.net", certPEM, keyPEM, notAfter)},
		{"a space", tcBody(t, "pa nel.ts.net", certPEM, keyPEM, notAfter)},
		{"a label of 64 characters", tcBody(t, strings.Repeat("a", 64)+".ts.net", certPEM, keyPEM, notAfter)},
		{"a name of 254 characters", tcBody(t, strings.Repeat(strings.Repeat("a", 61)+".", 4)+"abcdef", certPEM, keyPEM, notAfter)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &tcReader{answer: tcAlways(tt.body)}
			buf := &logBuffer{}
			c, _ := tcNewCache(r, slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
			if names, err := c.names(context.Background()); names != nil || err != nil {
				t.Errorf("names = %v, %v; want none and no error", names, err)
			}
			if got := tcServed(t, c, tcName); got != nil {
				t.Error("a certificate was served from a bad answer")
			}
			c.wait()
			if buf.String() == "" {
				t.Error("the bad answer was not logged")
			}
			if strings.Contains(buf.String(), "PRIVATE KEY") || strings.Contains(buf.String(), keyPEM) {
				t.Error("the log holds key material")
			}
		})
	}
}

func TestTcNamesNormalizesTheName(t *testing.T) {
	for _, tt := range []struct{ in, want string }{
		{"panel.tail1234.ts.net", "panel.tail1234.ts.net"},
		{"PANEL.Tail1234.TS.net.", "panel.tail1234.ts.net"},
		{"panel", "panel"},
		{strings.Repeat("a", 63) + ".ts.net", strings.Repeat("a", 63) + ".ts.net"},
	} {
		body, _ := tcPair(t, tt.in, testStart, testStart.Add(90*time.Hour))
		r := &tcReader{answer: tcAlways(body)}
		c, _ := tcNewCache(r, nil)
		names, err := c.names(context.Background())
		if err != nil || !slices.Equal(names, []string{tt.want}) {
			t.Errorf("names for %q = %v, %v; want [%s]", tt.in, names, err, tt.want)
		}
		if got := tcServed(t, c, tt.want); got == nil {
			t.Errorf("the pair named %q is not served for %q", tt.in, tt.want)
		}
	}
}

func TestTcReadIsBoundedByTheTimeout(t *testing.T) {
	r := &tcReader{answer: func(ctx context.Context, _ int) (json.RawMessage, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	c, _ := tcNewCache(r, nil)
	c.timeout = 20 * time.Millisecond
	start := time.Now()
	if names, err := c.names(context.Background()); names != nil || err != nil {
		t.Fatalf("names = %v, %v; want none and no error", names, err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("a read that never answers took %v", elapsed)
	}
}

func TestTcWithoutADispatcherEverythingFallsThrough(t *testing.T) {
	d := SourceDeps{LocalHostID: "H1"}
	c := newPairCache(d, coretest.NewFakeClock(testStart))
	if got := tcServed(t, c, tcName); got != nil {
		t.Error("a source without a dispatcher served a certificate")
	}
	c.wait()
	if names, err := c.names(context.Background()); names != nil || err != nil {
		t.Errorf("names = %v, %v; want none", names, err)
	}
	if cert, err := tailscaleCertSource(d).Certificate(&tls.ClientHelloInfo{ServerName: tcName}); cert != nil || err != nil {
		t.Errorf("tailscaleCertSource = %v, %v; want it to fall through", cert, err)
	}
	if names, err := tailscaleNames(context.Background(), d); names != nil || err != nil {
		t.Errorf("tailscaleNames = %v, %v; want none", names, err)
	}
}

func TestTcSourceAndNamesShareOnePair(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	body, der := tcPair(t, tcName, now.Add(-time.Hour), now.Add(90*24*time.Hour))
	r := &tcReader{answer: tcAlways(body)}
	d := r.deps(nil)
	names, err := tailscaleNames(context.Background(), d)
	if err != nil || !slices.Equal(names, []string{tcName}) {
		t.Fatalf("tailscaleNames = %v, %v", names, err)
	}
	cert, err := tailscaleCertSource(d).Certificate(&tls.ClientHelloInfo{ServerName: tcName})
	if err != nil || cert == nil || !bytes.Equal(cert.Certificate[0], der) {
		t.Fatalf("tailscaleCertSource did not serve the pair tailscaleNames loaded: %v, %v", cert, err)
	}
	if r.count() != 1 {
		t.Errorf("%d reads; want the one read shared by the source and the names", r.count())
	}
	other := &tcReader{answer: tcAlways(body)}
	if _, err := tailscaleNames(context.Background(), other.deps(nil)); err != nil || other.count() != 1 {
		t.Errorf("another dispatcher: %d reads, %v; want its own read", other.count(), err)
	}
	if r.count() != 1 {
		t.Errorf("the first dispatcher was read again: %d reads", r.count())
	}
}

func TestTcLeafCarriesTheNameAndServesThePair(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	body, der := tcPair(t, tcName, now.Add(-time.Hour), now.Add(90*24*time.Hour))
	r := &tcReader{answer: tcAlways(body)}
	l, _, _ := newLeaf(t)
	l.useSources(r.deps(slog.New(slog.DiscardHandler)))
	if issued, err := l.Ensure(context.Background(), addrs("100.64.0.1")); err != nil || !issued {
		t.Fatalf("Ensure = %v, %v", issued, err)
	}
	if got := csDNSNames(t, l); !slices.Equal(got, []string{"panel", "localhost", tcName}) {
		t.Fatalf("DNS SANs = %v; want the MagicDNS name added", got)
	}
	got, err := l.GetCertificate(&tls.ClientHelloInfo{ServerName: tcName})
	if err != nil || !bytes.Equal(got.Certificate[0], der) {
		t.Fatalf("GetCertificate(%s) = %v, %v; want the Tailscale pair", tcName, got, err)
	}
	if ca, err := l.GetCertificate(&tls.ClientHelloInfo{ServerName: "panel"}); err != nil || ca != l.cur.Load().cert {
		t.Errorf("GetCertificate(panel) = %v, %v; want the CA leaf", ca, err)
	}
	if r.count() != 1 {
		t.Errorf("%d reads; want 1", r.count())
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mkdir -p /root/.cache/w4-tmp/6 && export TMPDIR=/root/.cache/w4-tmp/6 GOTOOLCHAIN=local && timeout 300 go test -count=1 -run 'TestTc' ./internal/controller/listen/`
Expected: FAIL to build: `unknown field ReadInternalFunc in struct literal of type coretest.FakeDispatcher` and `undefined: newPairCache`. Quote this output in the report.

- [ ] **Step 3: Implement**

1. `internal/core/coretest/dispatcher.go`: add the field `ReadInternalFunc` with the signature of `ReadFunc`, and the method `ReadInternal(ctx, p, hostID, a, input)` that calls it, or returns `nil` and `notConfigured("FakeDispatcher", "ReadInternal")` when the field is nil, with the doc comment `// ReadInternal calls ReadInternalFunc.` as the neighbouring methods have. Run `gofmt -w` on the file (it realigns the field block). Commit this file alone first (Step 6).
2. `internal/controller/listen/tailscale_cert.go`: delete `fallThrough`, keep the two function signatures, and implement them over one cache type, so a handshake and the leaf's name hook share one pair. `sources.go` is frozen and hands both functions only a `SourceDeps`, so the shared cache is found in a package-level `sync.Map` keyed by a comparable struct of `d.Dispatch` and `d.LocalHostID`; `tailscaleCertSource(d)` and `tailscaleNames(ctx, d)` both call one `sharedPairs(d)` that returns the stored `*pairCache` or stores a new `newPairCache(d, core.SystemClock{})`. A comment says why the registry exists. Every `ext.Dispatcher` in the program is a pointer, so the key is comparable; a dispatcher of a non-comparable type is not supported.
3. Types and constants. Constants `certReadTimeout = 10 * time.Second` and `certRetryAfter = time.Minute`; a package variable `certReader = core.Principal{Actor: "system:listen.cert", Scope: core.ScopeAdmin}`. `pairCache` holds the `SourceDeps`, a `core.Clock`, a logger (`d.Log`, or a discarding one when nil), a field `timeout time.Duration` (initialised to `certReadTimeout`; the test shrinks it), a `sync.WaitGroup` for background reads, and, under a mutex, the cached pair, a `loading` flag, a `nextTry` time and the last logged error text. A cached pair holds the normalised name, the `*tls.Certificate` (with `Leaf` set), `expires` (the leaf's `NotAfter`) and `renewAt` (the leaf's `NotBefore` plus two thirds of `NotAfter.Sub(NotBefore)`). `newPairCache(d SourceDeps, clock core.Clock) *pairCache` builds one; `(*pairCache).wait()` waits for the background reads.
4. The read (`fetch`): call `d.Dispatch.ReadInternal(ctx, certReader, d.LocalHostID, core.ActionRef{Name: "tailscale.cert.get", Version: 1}, json.RawMessage("{}"))`; decode the answer into the private struct; normalise the name (`strings.ToLower`, one trailing dot removed) and refuse it unless it is a valid DNS name: 1 to 253 bytes, not an IP literal (`netip.ParseAddr` succeeds), dot-separated labels of 1 to 63 bytes, each only `a-z`, `0-9` or `-` and neither starting nor ending with `-` (this also refuses non-ASCII, underscores and spaces); build the pair with `tls.X509KeyPair` on the two PEM strings and `x509.ParseCertificate` on its first certificate. Every failure is an error whose text starts with `listen:` and wraps the cause with `%w`. Never log or return the answer's body, the PEM text or the key. The read wrapper (`read`) stores a successful pair, clears `nextTry` and the last error; on failure it sets `nextTry` to now plus `certRetryAfter` and logs the error at debug level only when its text differs from the last logged one.
5. `(*pairCache).Certificate(hello)`: with a nil `d.Dispatch` return `nil, nil`. Read the clock once. Under the mutex take the cached pair and decide to refresh when no read is in flight and (there is no pair or now is not before `renewAt`) and now is not before `nextTry`; then mark the read in flight and add to the wait group. A refresh runs in a goroutine with `context.WithTimeout(context.Background(), timeout)` and clears the in-flight flag when done; the handshake never waits for it. Serve the cached pair's certificate only when a pair exists, now is before its `expires` and `normalise(hello.ServerName)` equals the pair's name; otherwise return `nil, nil` (fall through). The pair that is due keeps being served while its refresh runs. Never return an error.
6. `(*pairCache).names(ctx)`: with a nil `d.Dispatch` return `nil, nil`. When there is no pair, or now is not before its `renewAt`, read synchronously with `context.WithTimeout(ctx, timeout)` (a failure is only logged, and `nextTry` is ignored on this path). Return a one-element slice of the cached pair's name, or `nil, nil` when there is no pair; never return an error, so a failing source keeps the cached name and a never-loaded one adds none.
7. gofmt; every exported identifier keeps its doc comment; unexported identifiers get a comment only where the why is not obvious (the registry, the retry gate, the pair that is served while due).

- [ ] **Step 4: Run the tests to verify they pass**

Run: `export TMPDIR=/root/.cache/w4-tmp/6 GOTOOLCHAIN=local && timeout 300 go test -race -count=1 ./internal/controller/listen/ ./internal/core/coretest/`
Expected: PASS for both packages, including the existing `TestLeafWithStubSources` and the service tests (a `SourceDeps` without a dispatcher still falls through).

- [ ] **Step 5: Run the gates**

Run: `export TMPDIR=/root/.cache/w4-tmp/6 GOTOOLCHAIN=local && timeout 600 go build ./... && timeout 300 go vet ./... && timeout 1500 go test -count=1 ./... && timeout 900 golangci-lint run --allow-serial-runners ./... && timeout 300 go mod tidy -diff`
Expected: no build or vet output, every line `ok` or `?`, `0 issues.`, no `go mod tidy` output.

- [ ] **Step 6: Commit**

```bash
rm -rf /root/.cache/w4-tmp/6 && GOTOOLCHAIN=local go clean -testcache
git add internal/core/coretest/dispatcher.go
git commit -m "test(coretest): add ReadInternal to FakeDispatcher"
git add internal/controller/listen/tailscale_cert.go internal/controller/listen/tailscale_cert_test.go
git commit -m "feat(listen): serve the Tailscale web certificate"
```

### Task 7: core: host create join fields, bundle and EnrollJoiner

**Files:**
- Modify: `internal/core/fleet.go` (`HostCreateInput`, `EnrollmentGrant`, new `EnrollJoiner` and `JoinRequest`; the import gains `context`)
- Modify: `schemas/core.host.create/v1.json`, `schemas/core.host.token/v1.json`, `schemas/core.host.enroll-local/v1.json` (generated by `make schemas`)
- Test: `internal/core/fleet_test.go` (rows in `TestFleetInputPatterns`, new tests)
- Test: `internal/controller/coreactions/registry_test.go` (`TestCoreActionSecrets` now expects `bundle` beside `token`)

Read, do not edit: `internal/core/define.go` and `contract.go` (frozen; `Env`, `DiffLine`, `Module`), `Makefile` (the `schemas` target runs `go run ./internal/core/schema/schemagen -out schemas`), `internal/core/schema/redact.go`.

**Interfaces:**
- Consumes: none.
- Produces: Contracts "Core host join" exactly, for Tasks 9 and 10:

```go
// HostCreateInput gains, after ControllerAddress:
TailnetJoin bool     `json:"tailnetJoin,omitempty" jsonschema_description:"Join the new host to the tailnet before it enrolls: the answer carries a dpb2. bootstrap bundle instead of only the token."`
Tags        []string `json:"tags,omitempty" jsonschema:"maxItems=16,uniqueItems=true,pattern=^tag:[a-z0-9][a-z0-9-]{0\\,62}$" jsonschema_description:"Tailscale tags of the new node, each tag:<name>; only with tailnetJoin, and each within the tailnet account's tagsAllowed."`

// EnrollmentGrant gains, after ExpiresAt:
Bundle string `json:"bundle,omitempty" secret:"true" jsonschema_description:"Single-use dpb2. bootstrap bundle holding the token and a tailnet auth key; set only when tailnetJoin was asked; shown only once."`

// EnrollJoiner and JoinRequest, as in the Contract, with a doc comment each.
```

  The `pattern=` in the `Tags` tag is the item pattern: invopop/jsonschema applies a `pattern` on a slice field to its items, and the comma of `{0,62}` is escaped as `\\,` inside the Go tag, as the neighbouring `Name` pattern does. Do not write `Tags` without the escape.

**Items:** 24

**Depends on:** none

- [ ] **Step 1: Write the failing tests**

In `internal/core/fleet_test.go`: change the import block to add `"context"` (after `"bytes"`), `"slices"` (after `"regexp"`) and `"github.com/darkraise/darkpanel/internal/core/secret"` (after the `schema` import), keeping the rest.

Insert these rows into the table of `TestFleetInputPatterns`, immediately before the row `{core.HostIDInput{}, `{"hostId":""}`, false},`:

```go
		{core.HostCreateInput{}, `{"name":"web-1","tailnetJoin":true}`, true},
		{core.HostCreateInput{}, `{"name":"web-1","tailnetJoin":true,"tags":["tag:server","tag:db-1"]}`, true},
		{core.HostCreateInput{}, `{"name":"web-1","tailnetJoin":"yes"}`, false},
		{core.HostCreateInput{}, `{"name":"web-1","tailnetJoin":true,"tags":["server"]}`, false},
		{core.HostCreateInput{}, `{"name":"web-1","tailnetJoin":true,"tags":["tag:Server"]}`, false},
		{core.HostCreateInput{}, `{"name":"web-1","tailnetJoin":true,"tags":["tag:"]}`, false},
		{core.HostCreateInput{}, `{"name":"web-1","tailnetJoin":true,"tags":["tag:-x"]}`, false},
		{core.HostCreateInput{}, `{"name":"web-1","tailnetJoin":true,"tags":["tag:a b"]}`, false},
		{core.HostCreateInput{}, `{"name":"web-1","tailnetJoin":true,"tags":["tag:` + strings.Repeat("a", 63) + `"]}`, true},
		{core.HostCreateInput{}, `{"name":"web-1","tailnetJoin":true,"tags":["tag:` + strings.Repeat("a", 64) + `"]}`, false},
		{core.HostCreateInput{}, `{"name":"web-1","tailnetJoin":true,"tags":["tag:a","tag:a"]}`, false},
		{core.HostCreateInput{}, `{"name":"web-1","tailnetJoin":true,"tags":[` + ejTags(16) + `]}`, true},
		{core.HostCreateInput{}, `{"name":"web-1","tailnetJoin":true,"tags":[` + ejTags(17) + `]}`, false},
```

Append to the end of the file:

```go
// ejTags is n distinct, valid tags as the inside of a JSON array.
func ejTags(n int) string {
	tags := make([]string, n)
	for i := range tags {
		tags[i] = `"tag:t` + strings.Repeat("x", i) + `"`
	}
	return strings.Join(tags, ",")
}

func TestEjGrantBundleIsSecretAndOptional(t *testing.T) {
	f, ok := reflect.TypeFor[core.EnrollmentGrant]().FieldByName("Bundle")
	if !ok || f.Tag.Get("secret") != "true" {
		t.Fatal(`EnrollmentGrant.Bundle is not tagged secret:"true"`)
	}
	out, err := schema.Output(reflect.TypeFor[core.EnrollmentGrant]())
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Defs map[string]struct {
			Properties map[string]map[string]any `json:"properties"`
			Required   []string                  `json:"required"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	grant := doc.Defs["EnrollmentGrant"]
	if grant.Properties["bundle"]["x-secret"] != true {
		t.Errorf("the bundle property has no x-secret: %v", grant.Properties["bundle"])
	}
	if slices.Contains(grant.Required, "bundle") || !slices.Contains(grant.Required, "token") {
		t.Errorf("required = %v; want token required and bundle optional", grant.Required)
	}
}

func TestEjRedactionHidesTheBundle(t *testing.T) {
	raw := json.RawMessage(`{"token":"dpt1.secret","bundle":"dpb2.secret","expiresAt":"2026-10-31T10:00:00Z"}`)
	var got map[string]string
	if err := json.Unmarshal(schema.Redact(reflect.TypeFor[core.EnrollmentGrant](), raw), &got); err != nil {
		t.Fatal(err)
	}
	if got["bundle"] != secret.Redacted || got["token"] != secret.Redacted || got["expiresAt"] != "2026-10-31T10:00:00Z" {
		t.Errorf("redacted grant = %v; want token and bundle redacted and the expiry kept", got)
	}
}

func TestEjJoinFieldsAreOmittedWhenUnset(t *testing.T) {
	tests := []struct {
		name string
		v    any
		has  []string
		not  []string
	}{
		{"plain input", core.HostCreateInput{Name: "web-1"}, nil, []string{"tailnetJoin", "tags"}},
		{"join input", core.HostCreateInput{Name: "web-1", TailnetJoin: true, Tags: []string{"tag:server"}}, []string{`"tailnetJoin":true`, `"tags":["tag:server"]`}, nil},
		{"plain grant", core.EnrollmentGrant{Token: "t"}, nil, []string{"bundle"}},
		{"join grant", core.EnrollmentGrant{Token: "t", Bundle: "dpb2.x"}, []string{`"bundle":"dpb2.x"`, `"token":"t"`}, nil},
	}
	for _, tt := range tests {
		b, err := json.Marshal(tt.v)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range tt.has {
			if !strings.Contains(string(b), want) {
				t.Errorf("%s: %s lacks %s", tt.name, b, want)
			}
		}
		for _, no := range tt.not {
			if strings.Contains(string(b), no) {
				t.Errorf("%s: %s holds %s", tt.name, b, no)
			}
		}
	}
}

// ejJoinModule is a module that also joins hosts to a network.
type ejJoinModule struct{ prepared []core.JoinRequest }

func (*ejJoinModule) Manifest() core.Manifest { return core.Manifest{} }

func (*ejJoinModule) PlanJoin(context.Context, core.Env, core.JoinRequest) ([]core.DiffLine, string, error) {
	return []core.DiffLine{{Op: "+", Text: "tailnet auth key"}}, "state-1", nil
}

func (m *ejJoinModule) PrepareJoin(_ context.Context, _ core.Env, req core.JoinRequest) (string, error) {
	m.prepared = append(m.prepared, req)
	return "tskey-auth-x", nil
}

// ejPlainModule is a module with no join step.
type ejPlainModule struct{}

func (ejPlainModule) Manifest() core.Manifest { return core.Manifest{} }

func TestEjEnrollJoinerIsFoundOnAModuleByTypeAssertion(t *testing.T) {
	joiner := &ejJoinModule{}
	var found []core.EnrollJoiner
	for _, m := range []core.Module{ejPlainModule{}, joiner} {
		if j, ok := m.(core.EnrollJoiner); ok {
			found = append(found, j)
		}
	}
	if len(found) != 1 {
		t.Fatalf("%d modules are EnrollJoiners; want only the one that implements both methods", len(found))
	}
	ctx := context.Background()
	expires := time.Date(2026, 10, 31, 10, 0, 0, 0, time.UTC)
	diff, state, err := found[0].PlanJoin(ctx, core.Env{}, core.JoinRequest{Tags: []string{"tag:server"}, Expires: expires})
	if err != nil || state != "state-1" || len(diff) != 1 || diff[0].Op != "+" {
		t.Fatalf("PlanJoin = %v, %q, %v", diff, state, err)
	}
	req := core.JoinRequest{HostID: "h1", Tags: []string{"tag:server"}, Expires: expires}
	key, err := found[0].PrepareJoin(ctx, core.Env{}, req)
	if err != nil || key != "tskey-auth-x" {
		t.Fatalf("PrepareJoin = %q, %v", key, err)
	}
	if !reflect.DeepEqual(joiner.prepared, []core.JoinRequest{req}) {
		t.Errorf("PrepareJoin saw %+v", joiner.prepared)
	}
}
```

In `internal/controller/coreactions/registry_test.go`, in `TestCoreActionSecrets`, change the three entries of `wantOut` to `"core.host.create": {"token": false, "bundle": false},`, `"core.host.token": {"token": false, "bundle": false},` and `"core.host.enroll-local": {"token": false, "bundle": false},`; the other entries stay.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mkdir -p /root/.cache/w4-tmp/7 && export TMPDIR=/root/.cache/w4-tmp/7 GOTOOLCHAIN=local && timeout 300 go test -count=1 -run 'TestFleet|TestEj|TestEnrollmentTokenIsSecret' ./internal/core/`
Expected: FAIL to build: `undefined: core.EnrollJoiner`, `undefined: core.JoinRequest`, `unknown field TailnetJoin in struct literal of type core.HostCreateInput`, `unknown field Bundle in struct literal of type core.EnrollmentGrant`.

Run: `export TMPDIR=/root/.cache/w4-tmp/7 GOTOOLCHAIN=local && timeout 300 go test -count=1 -run 'TestCoreActionSecrets' ./internal/controller/coreactions/`
Expected: FAIL with `core.host.create/v1: secret output fields = map[token:false], want map[bundle:false token:false]` and the same for `core.host.token/v1` and `core.host.enroll-local/v1`. Quote both outputs in the report.

- [ ] **Step 3: Implement**

1. `internal/core/fleet.go`: add `context` to the import (`import ( "context"; "time" )`). Add `TailnetJoin` and `Tags` to `HostCreateInput` after `ControllerAddress`, and `Bundle` to `EnrollmentGrant` after `ExpiresAt`, with the tags in Interfaces. `HostTokenInput` is unchanged. Add `EnrollJoiner` (methods `PlanJoin(ctx context.Context, env Env, req JoinRequest) (diff []DiffLine, state string, err error)` and `PrepareJoin(ctx context.Context, env Env, req JoinRequest) (authKey string, err error)`) and `JoinRequest` (`HostID string`, `Tags []string`, `Expires time.Time`) with the doc comments of Contracts "Core host join" and the comments on their fields: `PlanJoin` checks without writing and returns the diff lines and the state string that goes into the plan's `StateHash`; `PrepareJoin` creates the one-use auth key for `req.HostID`, recording its key row before the provider call; `HostID` is empty in `PlanJoin`; `Expires` is the enrollment token's expiry.
2. No behaviour of `core.host.create` changes in this task: `internal/controller/coreactions` ignores the new fields until Task 9. Do not edit it beyond `registry_test.go`.
3. Regenerate the snapshots: `export TMPDIR=/root/.cache/w4-tmp/7 GOTOOLCHAIN=local && timeout 600 make schemas`. `git status --short schemas` must list exactly the three files `schemas/core.host.create/v1.json`, `schemas/core.host.token/v1.json` and `schemas/core.host.enroll-local/v1.json`; if any other file changes, stop and report it. Read the diff: `core.host.create` gains the `tailnetJoin` and `tags` input properties (the items pattern `^tag:[a-z0-9][a-z0-9-]{0,62}$`, `maxItems` 16, `uniqueItems`) and the `bundle` output property with `"x-secret": true`; the two others gain only the `bundle` output property.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `export TMPDIR=/root/.cache/w4-tmp/7 GOTOOLCHAIN=local && timeout 300 go test -race -count=1 ./internal/core/ ./internal/controller/coreactions/ ./schemas/ && timeout 300 go test -count=1 -run 'TestSnapshotsCurrent|TestReleasedSnapshotsCompatible' ./internal/modules/`
Expected: PASS for all four packages.

- [ ] **Step 5: Run the gates**

Run: `export TMPDIR=/root/.cache/w4-tmp/7 GOTOOLCHAIN=local && timeout 600 go build ./... && timeout 300 go vet ./... && timeout 1500 go test -count=1 ./... && timeout 900 golangci-lint run --allow-serial-runners ./... && timeout 300 go mod tidy -diff`
Expected: no build or vet output, every line `ok` or `?`, `0 issues.`, no `go mod tidy` output.

- [ ] **Step 6: Commit**

```bash
rm -rf /root/.cache/w4-tmp/7 && GOTOOLCHAIN=local go clean -testcache
git add internal/core/fleet.go internal/core/fleet_test.go internal/controller/coreactions/registry_test.go schemas/core.host.create/v1.json schemas/core.host.token/v1.json schemas/core.host.enroll-local/v1.json
git commit -m "feat(core): add host create join fields and bundle"
```

### Task 8: tailscale: module seam, wire types, store and test host

**Files:**
- Modify: `internal/modules/tailscale/tailscale.go`
- Create: `internal/modules/tailscale/deps.go`
- Create: `internal/modules/tailscale/wire.go`
- Create: `internal/modules/tailscale/store.go`
- Create: `internal/modules/tailscale/preflight.go`
- Create: `internal/modules/tailscale/migrations/agent/0001_tailscale.sql`
- Test: `internal/modules/tailscale/fake_local_test.go`
- Test: `internal/modules/tailscale/testhost_test.go`
- Test: `internal/modules/tailscale/testhost_check_test.go`
- Test: `internal/modules/tailscale/deps_test.go`
- Test: `internal/modules/tailscale/wire_test.go`
- Test: `internal/modules/tailscale/store_test.go`
- Test: `internal/modules/tailscale/preflight_test.go`
- Unchanged and still passing: `internal/modules/tailscale/tailscale_test.go` (`TestManifestIsASkeleton`). The manifest still declares nothing until Task 31, so `ownedPaths`, `externalUnits`, `preflight` and `agentMigrations` are not wired into `Manifest()` here.

Read, do not edit: `internal/modules/docker/deps.go`, `store.go`, `preflight.go` and `host_test.go` (the shape to follow), `internal/platform/exec_rules.go` (the allowlist the test host enforces), `internal/platform/tslocal/client.go` and `derp.go`, `internal/platform/netstat/routes.go` and `sockets.go`.

**Interfaces:**
- Consumes: Contracts "Tailscale internals", "Tailscale wire shapes" and "Tailscale test host", which this task writes; "STUN client" (`stun.Probe`, merged before this wave and wired as the production `deps.stun`); `platform.TailscaleDebDir` and `platform.ProductionRules` (`internal/platform/exec_rules.go`); `tslocal.Client`, `tslocal.DefaultSocket`, `tslocal.ErrUnavailable` (`internal/platform/tslocal`); `netstat.Sockets`, `netstat.Interfaces`, `netstat.RouteGet`, `netstat.ErrNoRoute`; `core.Units`, `core.FileWriter`, `core.Runner`, `core.OwnedPath`, `core.ExternalUnit`; `errcode.TailscaleUnavailable`.
- Produces: every name of Contracts "Tailscale internals", "Tailscale wire shapes" and "Tailscale test host", exactly as stated there, so Tasks 14 to 29 and 31 compile against them. Beyond the Contracts:
  - non-test, private to the package: `readOSRelease(read func(name string) ([]byte, error)) (osInfo, error)` (parses `ID` and `VERSION_CODENAME` of an os-release file read through `read`), `httpsProbe(c *http.Client) func(ctx context.Context, url string) (time.Duration, error)` (the production `deps.https`), `liveLinkHost` (the production `deps.link`), `storeTimeFormat`, and the preflight check id `tailscale.os`;
  - test-only, in `fake_local_test.go` and `testhost_test.go`: the adapters `thLocal{*tsLocal}` and `thLink{*tsLink}`, `(*tsLocal).update(f func())`, the field `tsLocal.CertCalls []string` (the domains `CertPair` was asked for), `thDpkgStatus(version string) []byte` (a dpkg status file with the tailscale package installed at `version`, or absent when `version` is empty), and the constants `thHostname`, `thDpkgPath` and `thOSReleasePath`.
- The Contract forces two adapters. `tsLocal` has the fields `Status` and `Prefs`, and `tsLink` has the field `Sockets`, so neither can also have the `localAPI` methods `Status` and `Prefs` or the `linkHost` method `Sockets` (Go refuses a field and a method of one name). The host therefore hands `thLocal{h.local}` and `thLink{h.link}` to the deps. Later tasks keep reading and writing the fields (`h.local.Status`, `h.local.Prefs`, `h.link.Sockets`, ...) and never name the adapters.
- What the test host does, for every task that builds on it: `newTSHost` starts a logged-in tailscaled 1.104.1 (`BackendState` Running, address 100.64.0.7, `Self` set, `Prefs` with `WantRunning` and `CorpDNS` true), Ubuntu 24.04 (noble) on amd64, the tailscale package installed at 1.104.1 in `reads["/var/lib/dpkg/status"]`, `tailscaled.service` active and `darkpanel-controller.service` not found, interfaces `lo`, `eth0` and `tailscale0`, no sockets and no routes, and the clock at 2026-10-31 12:00 UTC. `Run` records every vector in `ran`, then refuses any vector the production exec allowlist (`platform.ProductionRules`) does not admit, then answers the next scripted result of that vector (exit 0 and no output when none is left). `WriteFile`, `Remove`, `WriteManaged`, `RemoveManaged` and `Owns` accept only module `tailscale` and only what `ownedPaths()` declares (the two files, and names matching the managed-dir pattern in `debDir`); every other `FileWriter` method returns an error. `deps.readFile` answers `reads` first, then what `Files` wrote, then `fs.ErrNotExist`. `tsUnits` allows `State` of both declared units and `Restart` of `tailscaled.service` only. The default `deps.stun` and `deps.https` answer an error until a test replaces them on the `ts` value that `h.ts()` returns.

**Depends on:** Task 1

- [ ] **Step 1: Write the failing tests**

Create these seven files with exactly this content. The first two are the shared test host and its fakes; `testhost_check_test.go` tests them; the others test what Step 3 builds.

`internal/modules/tailscale/fake_local_test.go`:

```go
package tailscale

import (
	"context"
	"fmt"
	"net/netip"
	"slices"
	"sync"

	"github.com/darkraise/darkpanel/internal/platform/netstat"
	"github.com/darkraise/darkpanel/internal/platform/tslocal"
)

var (
	_ localAPI = thLocal{}
	_ linkHost = thLink{}
)

// tsLocal is the fake local API. Its Status and Prefs fields share a name with localAPI methods, so thLocal
// is the value that satisfies localAPI. A call answers Errs[<method name>] when that holds an error;
// otherwise it answers from the fields. A test that changes a field while an action runs does it in update.
type tsLocal struct {
	Status  *tslocal.Status
	Prefs   *tslocal.Prefs
	DERP    *tslocal.DERPMap
	Cert    [2][]byte // the certificate chain, then the private key
	Errs    map[string]error
	Edits   []tslocal.PrefsPatch
	Starts  []tslocal.StartOptions
	Logouts int
	// CertCalls lists the domains CertPair was asked for.
	CertCalls []string

	mu sync.Mutex
}

// thLocal adapts a tsLocal to localAPI.
type thLocal struct{ *tsLocal }

// update runs f while holding the fake's lock.
func (l *tsLocal) update(f func()) {
	l.mu.Lock()
	defer l.mu.Unlock()
	f()
}

func (l *tsLocal) failure(method string) error { return l.Errs[method] }

func (a thLocal) Status(context.Context) (*tslocal.Status, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.failure("Status"); err != nil {
		return nil, err
	}
	if a.tsLocal.Status == nil {
		return nil, fmt.Errorf("%w: the fake has no status", tslocal.ErrUnavailable)
	}
	s := *a.tsLocal.Status
	return &s, nil
}

func (a thLocal) Prefs(context.Context) (*tslocal.Prefs, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.failure("Prefs"); err != nil {
		return nil, err
	}
	if a.tsLocal.Prefs == nil {
		return nil, fmt.Errorf("%w: the fake has no preferences", tslocal.ErrUnavailable)
	}
	p := *a.tsLocal.Prefs
	return &p, nil
}

// EditPrefs records the patch, applies each field it sets to Prefs and answers a copy of Prefs.
func (l *tsLocal) EditPrefs(_ context.Context, patch tslocal.PrefsPatch) (*tslocal.Prefs, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.Edits = append(l.Edits, patch)
	if err := l.failure("EditPrefs"); err != nil {
		return nil, err
	}
	if l.Prefs == nil {
		return nil, fmt.Errorf("%w: the fake has no preferences", tslocal.ErrUnavailable)
	}
	if patch.WantRunning != nil {
		l.Prefs.WantRunning = *patch.WantRunning
	}
	if patch.RouteAll != nil {
		l.Prefs.RouteAll = *patch.RouteAll
	}
	if patch.ShieldsUp != nil {
		l.Prefs.ShieldsUp = *patch.ShieldsUp
	}
	if patch.RunSSH != nil {
		l.Prefs.RunSSH = *patch.RunSSH
	}
	if patch.ExitNodeID != nil {
		l.Prefs.ExitNodeID = *patch.ExitNodeID
	}
	if patch.ExitNodeIP != nil {
		l.Prefs.ExitNodeIP = *patch.ExitNodeIP
	}
	if patch.AdvertiseRoutes != nil {
		l.Prefs.AdvertiseRoutes = slices.Clone(*patch.AdvertiseRoutes)
	}
	p := *l.Prefs
	return &p, nil
}

// Start records the options and changes nothing else.
func (l *tsLocal) Start(_ context.Context, o tslocal.StartOptions) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.Starts = append(l.Starts, o)
	return l.failure("Start")
}

// Logout counts the call and changes nothing else.
func (l *tsLocal) Logout(context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.Logouts++
	return l.failure("Logout")
}

// DERPMap answers an empty map when DERP is nil.
func (l *tsLocal) DERPMap(context.Context) (*tslocal.DERPMap, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.failure("DERPMap"); err != nil {
		return nil, err
	}
	if l.DERP == nil {
		return &tslocal.DERPMap{}, nil
	}
	m := *l.DERP
	return &m, nil
}

// CertPair records the domain and answers Cert.
func (l *tsLocal) CertPair(_ context.Context, domain string) (certPEM, keyPEM []byte, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.CertCalls = append(l.CertCalls, domain)
	if err := l.failure("CertPair"); err != nil {
		return nil, nil, err
	}
	return slices.Clone(l.Cert[0]), slices.Clone(l.Cert[1]), nil
}

// tsLink is the fake socket table, interface list and routing table. Its Sockets field shares a name with
// a linkHost method, so thLink is the value that satisfies linkHost.
type tsLink struct {
	Sockets []netstat.Socket
	Ifaces  []netstat.Interface
	Routes  map[netip.Addr]netstat.Route // RouteGet answers by destination; a missing entry is netstat.ErrNoRoute
	Pid     int
}

// thLink adapts a tsLink to linkHost.
type thLink struct{ *tsLink }

func (a thLink) Sockets() ([]netstat.Socket, error) { return slices.Clone(a.tsLink.Sockets), nil }

func (l *tsLink) Interfaces() ([]netstat.Interface, error) { return slices.Clone(l.Ifaces), nil }

func (l *tsLink) RouteGet(q netstat.RouteQuery) (netstat.Route, error) {
	if r, ok := l.Routes[q.Dst]; ok {
		return r, nil
	}
	return netstat.Route{}, netstat.ErrNoRoute
}

func (l *tsLink) PID() int { return l.Pid }
```

`internal/modules/tailscale/testhost_test.go`:

```go
package tailscale

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/netip"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/platform"
	"github.com/darkraise/darkpanel/internal/platform/netstat"
	"github.com/darkraise/darkpanel/internal/platform/tslocal"
)

var (
	_ core.Runner        = (*tsHost)(nil)
	_ core.FileWriter    = (*tsHost)(nil)
	_ core.Units         = (*tsUnits)(nil)
	_ core.EffectJournal = (*tsJournal)(nil)
)

// thHostname is the host name the test host reports.
const thHostname = "ts-host-1"

// thDpkgPath and thOSReleasePath are the files the module reads through deps.readFile.
const (
	thDpkgPath      = "/var/lib/dpkg/status"
	thOSReleasePath = "/etc/os-release"
)

var (
	errTHUnused = errors.New("tsHost: not used by the tailscale module")
	errTHNoFake = errors.New("tsHost: nothing is faked for this probe; the test sets deps itself")
)

// thDebPattern is the managed-dir file-name pattern the module declares for staged packages.
var thDebPattern = regexp.MustCompile(`^tailscale_[0-9][0-9A-Za-z.+~-]{0,63}_(amd64|arm64)\.deb$`)

// tsRun is one scripted answer of a command.
type tsRun struct {
	res core.CmdResult
	err error
}

// tsHost is the fake host a tailscale test runs on: a fake local API, socket table, units, command runner,
// file provider and agent database. It stands in for core.Runner and core.FileWriter, so every command goes
// through the production exec allowlist and every file write is checked against the module's owned paths.
type tsHost struct {
	t       *testing.T
	local   *tsLocal
	link    *tsLink
	units   *tsUnits
	files   map[string][]byte // what Files wrote, by path (managed files as dir + "/" + name)
	reads   map[string][]byte // what deps.readFile answers before it looks at files; a missing path is fs.ErrNotExist
	ran     [][]string        // every exec vector run: path, then args
	scripts map[string][]tsRun
	db      *store.DB
	journal *tsJournal
	clock   *coretest.FakeClock
	owned   map[string]bool // paths Files recorded as owned

	mu sync.Mutex
}

// newTSHost returns a host running a logged-in tailscaled 1.104.1 on Ubuntu 24.04 (noble) for amd64, with the
// tailscale package installed, no controller unit, and an agent database with the core and tailscale
// migrations applied. The clock reads 2026-10-31 12:00 UTC.
func newTSHost(t *testing.T) *tsHost {
	t.Helper()
	ctx := context.Background()
	start := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(dir, "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	coreSet, err := store.CoreMigrations(store.SideAgent)
	if err != nil {
		t.Fatal(err)
	}
	res, err := store.Migrate(ctx, db, store.MigrateOptions{
		Side: store.SideAgent, BinaryVersion: "test", BackupRoot: filepath.Join(dir, "backups"), Keep: 5, Now: start,
	}, coreSet, []store.MigrationSet{{Module: ID, FS: agentMigrations()}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Failed) > 0 {
		t.Fatalf("migrations failed: %+v", res.Failed)
	}
	addr := netip.MustParseAddr("100.64.0.7")
	self := &tslocal.PeerStatus{
		ID: "n1", HostName: thHostname, DNSName: thHostname + ".example.ts.net.", OS: "linux",
		TailscaleIPs: []netip.Addr{addr}, Online: true,
	}
	return &tsHost{
		t: t,
		local: &tsLocal{
			Status: &tslocal.Status{
				Version: "1.104.1", BackendState: "Running", TailscaleIPs: []netip.Addr{addr}, Self: self,
				MagicDNSSuffix: "example.ts.net", CurrentTailnet: &tslocal.TailnetStatus{Name: "example.com", MagicDNSSuffix: "example.ts.net", MagicDNSEnabled: true},
			},
			Prefs: &tslocal.Prefs{WantRunning: true, CorpDNS: true},
		},
		link: &tsLink{
			Ifaces: []netstat.Interface{
				{Index: 1, Name: "lo", Up: true, Running: true, Loopback: true, Addrs: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/8")}},
				{Index: 2, Name: "eth0", Up: true, Running: true, Addrs: []netip.Prefix{netip.MustParsePrefix("192.0.2.10/24")}},
				{Index: 3, Name: "tailscale0", Up: true, Running: true, Addrs: []netip.Prefix{netip.PrefixFrom(addr, 32)}},
			},
			Pid: 4242,
		},
		units: &tsUnits{States: map[string]core.UnitState{
			tailscaledUnit: {LoadState: "loaded", ActiveState: "active", SubState: "running", MainPID: 900},
			controllerUnit: {LoadState: "not-found"},
		}},
		files: map[string][]byte{},
		reads: map[string][]byte{
			thDpkgPath:      thDpkgStatus("1.104.1"),
			thOSReleasePath: []byte("PRETTY_NAME=\"Ubuntu 24.04.1 LTS\"\nNAME=\"Ubuntu\"\nVERSION_ID=\"24.04\"\nVERSION_CODENAME=noble\nID=ubuntu\nID_LIKE=debian\n"),
		},
		scripts: map[string][]tsRun{},
		db:      db,
		journal: &tsJournal{},
		clock:   coretest.NewFakeClock(start),
		owned:   map[string]bool{},
	}
}

// thDpkgStatus is a /var/lib/dpkg/status with a few unrelated packages and, when version is not empty, the
// tailscale package installed at that version.
func thDpkgStatus(version string) []byte {
	var b strings.Builder
	b.WriteString("Package: tailscale-archive-keyring\nStatus: install ok installed\nVersion: 9.9.9\nArchitecture: all\n\n")
	b.WriteString("Package: curl\nStatus: install ok installed\nVersion: 8.5.0-2ubuntu10\nArchitecture: amd64\nDescription: command line tool\n for transferring data\n\n")
	if version != "" {
		fmt.Fprintf(&b, "Package: tailscale\nStatus: install ok installed\nPriority: optional\nArchitecture: amd64\nVersion: %s\nDescription: The easiest, most secure way to use WireGuard\n\n", version)
	}
	b.WriteString("Package: zlib1g\nStatus: install ok installed\nVersion: 1:1.3.dfsg-3.1ubuntu2\nArchitecture: amd64\n")
	return []byte(b.String())
}

// ts returns a ts whose deps all point at the host's fakes. The STUN and HTTPS probes answer an error until a
// test sets t.d.stun or t.d.https.
func (h *tsHost) ts() ts {
	return ts{d: deps{
		local:     func() localAPI { return thLocal{h.local} },
		link:      thLink{h.link},
		stun:      func(context.Context, netip.AddrPort) (time.Duration, error) { return 0, errTHNoFake },
		https:     func(context.Context, string) (time.Duration, error) { return 0, errTHNoFake },
		osRelease: func() (osInfo, error) { return readOSRelease(h.readFile) },
		arch:      "amd64",
		hostname:  func() (string, error) { return thHostname, nil },
		readFile:  h.readFile,
	}}
}

// readFile answers reads first, then what Files wrote.
func (h *tsHost) readFile(name string) ([]byte, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if b, ok := h.reads[name]; ok {
		return bytes.Clone(b), nil
	}
	if b, ok := h.files[name]; ok {
		return bytes.Clone(b), nil
	}
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
}

// env returns the environment of a call of the change; an empty changeID is a plan or a read, which has no
// effects journal.
func (h *tsHost) env(changeID string) core.Env {
	env := core.Env{
		HostID:    "h1",
		ChangeID:  changeID,
		Principal: core.Principal{Actor: "user:u1", UserID: "u1", Scope: core.ScopeAdmin},
		DB:        h.db,
		Exec:      h,
		Files:     h,
		Sealer:    coretest.Sealer{},
		Clock:     h.clock,
		Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		Units:     h.units,
	}
	if changeID != "" {
		h.journal.mu.Lock()
		h.journal.change = changeID
		h.journal.mu.Unlock()
		env.Effects = h.journal
	}
	return env
}

// script queues answers for the command whose path and arguments, joined by single spaces, are vector.
// Each run of that command consumes one answer; a command with none left answers exit 0 and no output.
func (h *tsHost) script(vector string, runs ...tsRun) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.scripts[vector] = append(h.scripts[vector], runs...)
}

// thAllowed reports whether the production exec allowlist admits the command.
func thAllowed(path string, args []string) bool {
	for _, rule := range platform.ProductionRules() {
		if rule.Path == path && rule.Match(args) == nil {
			return true
		}
	}
	return false
}

// Run records the vector, refuses one the production allowlist does not admit, and answers the next scripted
// result of the vector. It runs nothing.
func (h *tsHost) Run(_ context.Context, cmd core.Cmd) (core.CmdResult, error) {
	vector := append([]string{cmd.Path}, cmd.Args...)
	key := strings.Join(vector, " ")
	h.mu.Lock()
	defer h.mu.Unlock()
	h.ran = append(h.ran, vector)
	if !thAllowed(cmd.Path, cmd.Args) {
		return core.CmdResult{}, fmt.Errorf("tsHost: %s is not in the production exec allowlist", key)
	}
	queue := h.scripts[key]
	if len(queue) == 0 {
		return core.CmdResult{}, nil
	}
	h.scripts[key] = queue[1:]
	return queue[0].res, queue[0].err
}

// thOwned refuses what the module's manifest would not let its file provider write.
func thOwned(module, path, kind string) error {
	if module != ID {
		return fmt.Errorf("tsHost: module %q is not %q", module, ID)
	}
	for _, op := range ownedPaths() {
		if op.Path == path && op.Kind == kind {
			return nil
		}
	}
	return fmt.Errorf("tsHost: %s is not an owned path of kind %s", path, kind)
}

func (h *tsHost) WriteFile(_ context.Context, module, path string, data []byte, _ fs.FileMode) error {
	if err := thOwned(module, path, core.OwnedFile); err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.files[path] = bytes.Clone(data)
	h.owned[path] = true
	return nil
}

func (h *tsHost) Remove(_ context.Context, module, path string) error {
	if err := thOwned(module, path, core.OwnedFile); err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.files, path)
	delete(h.owned, path)
	return nil
}

func (h *tsHost) WriteManaged(_ context.Context, module, dir, name string, data []byte, _ fs.FileMode, _, _ int) error {
	if err := thOwned(module, dir, core.OwnedManagedDir); err != nil {
		return err
	}
	if !thDebPattern.MatchString(name) {
		return fmt.Errorf("tsHost: %q does not match the managed-dir pattern", name)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.files[dir+"/"+name] = bytes.Clone(data)
	h.owned[dir+"/"+name] = true
	return nil
}

func (h *tsHost) RemoveManaged(_ context.Context, module, dir, name string) error {
	if err := thOwned(module, dir, core.OwnedManagedDir); err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.files, dir+"/"+name)
	delete(h.owned, dir+"/"+name)
	return nil
}

// Owns reports whether Files wrote path and has not removed it.
func (h *tsHost) Owns(_ context.Context, module, path string) (bool, error) {
	if module != ID {
		return false, fmt.Errorf("tsHost: module %q is not %q", module, ID)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.owned[path], nil
}

func (h *tsHost) ReplaceBlock(context.Context, string, string, string, []byte) error {
	return errTHUnused
}

func (h *tsHost) RemoveBlock(context.Context, string, string, string) error { return errTHUnused }

func (h *tsHost) Restore(context.Context, string, core.RestoreEntry) error { return errTHUnused }

func (h *tsHost) ReadSysctl(context.Context, string, string) (string, error) {
	return "", errTHUnused
}

func (h *tsHost) WriteSysctl(context.Context, string, string, string) error { return errTHUnused }

func (h *tsHost) Rename(context.Context, string, string, string) error { return errTHUnused }

func (h *tsHost) EnsureServiceDir(context.Context, string, string) error { return errTHUnused }

func (h *tsHost) RenameIn(context.Context, string, string, string, string) error {
	return errTHUnused
}

func (h *tsHost) RemoveIn(context.Context, string, string, string) error { return errTHUnused }

func (h *tsHost) RemoveServiceDir(context.Context, string, string) error { return errTHUnused }

// tsUnits is the module's unit client: State of tailscaled.service and darkpanel-controller.service answers
// from States, Restart of tailscaled.service is recorded, and every other call is refused, as the module's
// declared external units would refuse it.
type tsUnits struct {
	States   map[string]core.UnitState
	Restarts []string

	mu sync.Mutex
}

func (u *tsUnits) refuse(method, unit string) error {
	return fmt.Errorf("tsUnits: the module may not %s unit %s", method, unit)
}

func (u *tsUnits) State(_ context.Context, unit string) (core.UnitState, error) {
	if unit != tailscaledUnit && unit != controllerUnit {
		return core.UnitState{}, u.refuse("State", unit)
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if st, ok := u.States[unit]; ok {
		return st, nil
	}
	return core.UnitState{LoadState: "not-found"}, nil
}

func (u *tsUnits) Restart(_ context.Context, unit string) error {
	if unit != tailscaledUnit {
		return u.refuse("Restart", unit)
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	u.Restarts = append(u.Restarts, unit)
	return nil
}

func (u *tsUnits) StartTransientService(_ context.Context, name string, _ core.TransientService) error {
	return u.refuse("StartTransientService", name)
}

func (u *tsUnits) StartTransientTimer(_ context.Context, name string, _ core.TransientTimer) error {
	return u.refuse("StartTransientTimer", name)
}

func (u *tsUnits) Start(_ context.Context, unit string) error { return u.refuse("Start", unit) }
func (u *tsUnits) Stop(_ context.Context, unit string) error  { return u.refuse("Stop", unit) }
func (u *tsUnits) Kill(_ context.Context, unit string, _ syscall.Signal) error {
	return u.refuse("Kill", unit)
}
func (u *tsUnits) ResetFailed(_ context.Context, unit string) error {
	return u.refuse("ResetFailed", unit)
}
func (u *tsUnits) Enable(_ context.Context, unitFiles ...string) error {
	return u.refuse("Enable", strings.Join(unitFiles, ","))
}
func (u *tsUnits) Disable(_ context.Context, unitFiles ...string) error {
	return u.refuse("Disable", strings.Join(unitFiles, ","))
}
func (u *tsUnits) Reload(context.Context) error { return u.refuse("Reload", "") }
func (u *tsUnits) List(_ context.Context, pattern string) ([]core.UnitStatus, error) {
	return nil, u.refuse("List", pattern)
}

// tsJournal is an in-memory core.EffectJournal.
type tsJournal struct {
	mu      sync.Mutex
	change  string
	list    []core.Effect
	secrets map[int][]byte
}

// effects returns a copy of the journaled effects.
func (j *tsJournal) effects() []core.Effect {
	j.mu.Lock()
	defer j.mu.Unlock()
	return slices.Clone(j.list)
}

func (j *tsJournal) Intend(_ context.Context, e core.Effect, secret []byte) (int, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	e.Seq = len(j.list) + 1
	e.State = core.EffectIntended
	e.ChangeID = j.change
	j.list = append(j.list, e)
	if secret != nil {
		if j.secrets == nil {
			j.secrets = map[int][]byte{}
		}
		j.secrets[e.Seq] = bytes.Clone(secret)
	}
	return e.Seq, nil
}

// intended returns the index of effect seq when it is still intended.
func (j *tsJournal) intended(seq int) (int, error) {
	if seq < 1 || seq > len(j.list) {
		return 0, fmt.Errorf("fake: no effect %d", seq)
	}
	if st := j.list[seq-1].State; st != core.EffectIntended {
		return 0, fmt.Errorf("fake: effect %d is %s, not intended", seq, st)
	}
	return seq - 1, nil
}

func (j *tsJournal) Made(_ context.Context, seq int, resource string, post core.StateDoc) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	i, err := j.intended(seq)
	if err != nil {
		return err
	}
	j.list[i].State = core.EffectMade
	j.list[i].Resource = resource
	j.list[i].Post = &post
	return nil
}

func (j *tsJournal) Skipped(_ context.Context, seq int) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	i, err := j.intended(seq)
	if err != nil {
		return err
	}
	j.list[i].State = core.EffectSkipped
	return nil
}

func (j *tsJournal) List(context.Context) ([]core.Effect, error) { return j.effects(), nil }

func (j *tsJournal) Secret(_ context.Context, seq int) ([]byte, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if seq < 1 || seq > len(j.list) {
		return nil, fmt.Errorf("fake: no effect %d", seq)
	}
	return bytes.Clone(j.secrets[seq]), nil
}
```

`internal/modules/tailscale/testhost_check_test.go`:

```go
package tailscale

import (
	"context"
	"errors"
	"io/fs"
	"net/netip"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/platform/netstat"
	"github.com/darkraise/darkpanel/internal/platform/tslocal"
)

func TestTHHostDefaults(t *testing.T) {
	h := newTSHost(t)
	ctx := context.Background()

	if e := h.env(""); e.Effects != nil || e.ChangeID != "" {
		t.Errorf("env(\"\") = change %q, journal %v, want neither", e.ChangeID, e.Effects)
	}
	env := h.env("c1")
	if env.HostID != "h1" || env.ChangeID != "c1" || env.DB != h.db || env.Exec == nil || env.Files == nil || env.Units == nil ||
		env.Effects == nil || env.Clock == nil || env.Log == nil || env.Principal.Scope != core.ScopeAdmin {
		t.Fatalf("env(c1) = %+v", env)
	}
	if got := env.Clock.Now(); !got.Equal(time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("clock reads %v", got)
	}

	var tables []string
	rows, err := h.db.Read().Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name LIKE 'tailscale_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, n)
	}
	if want := []string{"tailscale_installs", "tailscale_pin_steps", "tailscale_pins"}; !reflect.DeepEqual(tables, want) {
		t.Errorf("tables %v, want %v", tables, want)
	}

	d := h.ts().d
	st, err := d.local().Status(ctx)
	if err != nil || st.BackendState != "Running" || st.Version != "1.104.1" || st.Self == nil {
		t.Errorf("Status = %+v, %v; want a running node on 1.104.1", st, err)
	}
	if info, err := d.osRelease(); err != nil || info != (osInfo{ID: "ubuntu", Codename: "noble"}) {
		t.Errorf("osRelease = %+v, %v; want ubuntu noble", info, err)
	}
	if name, err := d.hostname(); err != nil || name != thHostname || d.arch != "amd64" {
		t.Errorf("hostname = %q, %v, arch %q", name, err, d.arch)
	}
	if v, err := h.ts().installedVersion(ctx, env); err != nil || v != "1.104.1" {
		t.Errorf("installedVersion = %q, %v; the host starts with tailscale 1.104.1 installed", v, err)
	}
	if _, err := d.stun(ctx, netip.MustParseAddrPort("127.0.0.1:3478")); err == nil {
		t.Error("stun answered before a test set it")
	}
	if _, err := d.https(ctx, "https://example.invalid/derp/latency-check"); err == nil {
		t.Error("https answered before a test set it")
	}
	if _, err := d.readFile("/nonexistent"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("readFile of a missing path = %v, want fs.ErrNotExist", err)
	}
}

func TestTHHostRun(t *testing.T) {
	h := newTSHost(t)
	ctx := context.Background()
	showhold := core.Cmd{Path: "/usr/bin/apt-mark", Args: []string{"showhold"}}

	if res, err := h.Run(ctx, showhold); err != nil || res.ExitCode != 0 || len(res.Stdout) != 0 {
		t.Errorf("an unscripted run = %+v, %v; want exit 0 and no output", res, err)
	}
	h.script("/usr/bin/apt-mark showhold",
		tsRun{res: core.CmdResult{Stdout: []byte("tailscale\n")}},
		tsRun{err: errors.New("boom")},
	)
	if res, err := h.Run(ctx, showhold); err != nil || string(res.Stdout) != "tailscale\n" {
		t.Errorf("first scripted run = %+v, %v", res, err)
	}
	if _, err := h.Run(ctx, showhold); err == nil || err.Error() != "boom" {
		t.Errorf("second scripted run error = %v, want boom", err)
	}
	if res, err := h.Run(ctx, showhold); err != nil || len(res.Stdout) != 0 {
		t.Errorf("after the queue = %+v, %v; want the unscripted answer", res, err)
	}

	for _, cmd := range []core.Cmd{
		{Path: "/usr/bin/apt-get", Args: []string{"remove", "-y", "curl"}},
		{Path: "/usr/bin/systemctl", Args: []string{"restart", "tailscaled"}},
		{Path: "/usr/bin/tailscale", Args: []string{"up"}},
	} {
		if _, err := h.Run(ctx, cmd); err == nil || !strings.Contains(err.Error(), "allowlist") {
			t.Errorf("%v: error %v, want a refusal by the production allowlist", cmd, err)
		}
	}
	if _, err := h.Run(ctx, core.Cmd{Path: "/usr/bin/apt-mark", Args: []string{"hold", "tailscale"}}); err != nil {
		t.Errorf("apt-mark hold tailscale refused: %v", err)
	}
	want := [][]string{
		{"/usr/bin/apt-mark", "showhold"}, {"/usr/bin/apt-mark", "showhold"}, {"/usr/bin/apt-mark", "showhold"},
		{"/usr/bin/apt-mark", "showhold"},
		{"/usr/bin/apt-get", "remove", "-y", "curl"}, {"/usr/bin/systemctl", "restart", "tailscaled"},
		{"/usr/bin/tailscale", "up"}, {"/usr/bin/apt-mark", "hold", "tailscale"},
	}
	if !reflect.DeepEqual(h.ran, want) {
		t.Errorf("ran %v, want %v", h.ran, want)
	}
}

func TestTHHostFiles(t *testing.T) {
	h := newTSHost(t)
	ctx := context.Background()
	deb := "tailscale_1.104.1_amd64.deb"

	if err := h.WriteFile(ctx, ID, keyringPath, []byte("key"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := h.WriteManaged(ctx, ID, debDir, deb, []byte("deb"), 0o600, 0, 0); err != nil {
		t.Fatal(err)
	}
	if string(h.files[keyringPath]) != "key" || string(h.files[debDir+"/"+deb]) != "deb" {
		t.Errorf("files %v", h.files)
	}
	for path, want := range map[string]string{keyringPath: "key", debDir + "/" + deb: "deb"} {
		if b, err := h.ts().d.readFile(path); err != nil || string(b) != want {
			t.Errorf("readFile(%s) = %q, %v; deps.readFile must see what Files wrote", path, b, err)
		}
	}
	h.reads[keyringPath] = []byte("seeded")
	if b, _ := h.ts().d.readFile(keyringPath); string(b) != "seeded" {
		t.Errorf("readFile(%s) = %q; a seeded read wins over a written file", keyringPath, b)
	}
	for path, want := range map[string]bool{keyringPath: true, debDir + "/" + deb: true, sourcesPath: false} {
		if got, err := h.Owns(ctx, ID, path); err != nil || got != want {
			t.Errorf("Owns(%s) = %v, %v; want %v", path, got, err, want)
		}
	}

	refused := map[string]error{
		"another module":          h.WriteFile(ctx, "docker", keyringPath, nil, 0o644),
		"an undeclared path":      h.WriteFile(ctx, ID, "/etc/passwd", nil, 0o644),
		"a managed dir as a file": h.WriteFile(ctx, ID, debDir, nil, 0o644),
		"another directory":       h.WriteManaged(ctx, ID, "/var/lib/other", deb, nil, 0o600, 0, 0),
		"a name off pattern":      h.WriteManaged(ctx, ID, debDir, "evil.deb", nil, 0o600, 0, 0),
		"a path in a name":        h.WriteManaged(ctx, ID, debDir, "../tailscale_1.0_amd64.deb", nil, 0o600, 0, 0),
		"a block":                 h.ReplaceBlock(ctx, ID, keyringPath, "b", nil),
	}
	for name, err := range refused {
		if err == nil {
			t.Errorf("%s was accepted", name)
		}
	}

	if err := h.RemoveManaged(ctx, ID, debDir, deb); err != nil {
		t.Fatal(err)
	}
	if err := h.Remove(ctx, ID, keyringPath); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.files[debDir+"/"+deb]; ok || len(h.files) != 0 {
		t.Errorf("files after removal %v", h.files)
	}
	if got, _ := h.Owns(ctx, ID, keyringPath); got {
		t.Error("a removed file is still owned")
	}
}

func TestTHFakeLocal(t *testing.T) {
	h := newTSHost(t)
	ctx := context.Background()
	api := h.ts().d.local()

	yes, no := true, false
	routes := []netip.Prefix{netip.MustParsePrefix("192.168.1.0/24")}
	exit := "n123"
	p, err := api.EditPrefs(ctx, tslocal.PrefsPatch{WantRunning: &no, RunSSH: &yes, ExitNodeID: &exit, AdvertiseRoutes: &routes})
	if err != nil {
		t.Fatal(err)
	}
	if p.WantRunning || !p.RunSSH || p.ExitNodeID != "n123" || !reflect.DeepEqual(p.AdvertiseRoutes, routes) || !p.CorpDNS {
		t.Errorf("EditPrefs answered %+v; it applies the fields the patch sets and keeps the rest", p)
	}
	got, err := api.Prefs(ctx)
	if err != nil || got.WantRunning || !got.RunSSH {
		t.Errorf("Prefs after the edit = %+v, %v", got, err)
	}
	if len(h.local.Edits) != 1 || *h.local.Edits[0].RunSSH != true {
		t.Errorf("Edits %+v", h.local.Edits)
	}

	if err := api.Start(ctx, tslocal.StartOptions{AuthKey: "k"}); err != nil || len(h.local.Starts) != 1 || h.local.Starts[0].AuthKey != "k" {
		t.Errorf("Start: %v, recorded %+v", err, h.local.Starts)
	}
	if err := api.Logout(ctx); err != nil || h.local.Logouts != 1 {
		t.Errorf("Logout: %v, count %d", err, h.local.Logouts)
	}
	if st, _ := api.Status(ctx); st.BackendState != "Running" {
		t.Error("Start and Logout must only be recorded: the fake does not change the status")
	}

	h.local.Cert = [2][]byte{[]byte("CERT"), []byte("KEY")}
	cert, key, err := api.CertPair(ctx, "ts-host-1.example.ts.net")
	if err != nil || string(cert) != "CERT" || string(key) != "KEY" || !reflect.DeepEqual(h.local.CertCalls, []string{"ts-host-1.example.ts.net"}) {
		t.Errorf("CertPair = %q, %q, %v; calls %v", cert, key, err, h.local.CertCalls)
	}

	if m, err := api.DERPMap(ctx); err != nil || len(m.Regions) != 0 {
		t.Errorf("DERPMap with none set = %+v, %v; want an empty map", m, err)
	}
	h.local.DERP = &tslocal.DERPMap{Regions: map[int]*tslocal.DERPRegion{1: {RegionID: 1, RegionCode: "nyc"}}}
	if m, err := api.DERPMap(ctx); err != nil || m.Regions[1].RegionCode != "nyc" {
		t.Errorf("DERPMap = %+v, %v", m, err)
	}

	boom := errors.New("boom")
	h.local.Errs = map[string]error{"Status": boom, "Prefs": boom, "EditPrefs": boom, "Start": boom, "Logout": boom, "DERPMap": boom, "CertPair": boom}
	_, e1 := api.Status(ctx)
	_, e2 := api.Prefs(ctx)
	_, e3 := api.EditPrefs(ctx, tslocal.PrefsPatch{})
	e4 := api.Start(ctx, tslocal.StartOptions{})
	e5 := api.Logout(ctx)
	_, e6 := api.DERPMap(ctx)
	_, _, e7 := api.CertPair(ctx, "x.example.ts.net")
	for i, err := range []error{e1, e2, e3, e4, e5, e6, e7} {
		if !errors.Is(err, boom) {
			t.Errorf("method %d answered %v, want the queued error", i, err)
		}
	}

	h.local.update(func() {
		h.local.Errs = nil
		h.local.Status = nil
	})
	if _, err := api.Status(ctx); !errors.Is(err, tslocal.ErrUnavailable) {
		t.Errorf("Status with none set = %v, want tslocal.ErrUnavailable", err)
	}
}

func TestTHFakeLink(t *testing.T) {
	h := newTSHost(t)
	l := h.ts().d.link
	dst := netip.MustParseAddr("100.64.0.9")
	h.link.Routes = map[netip.Addr]netstat.Route{dst: {OutIface: "tailscale0"}}
	h.link.Sockets = []netstat.Socket{{Proto: netstat.ProtoTCP, State: netstat.StateEstablished}}

	if r, err := l.RouteGet(netstat.RouteQuery{Dst: dst}); err != nil || r.OutIface != "tailscale0" {
		t.Errorf("RouteGet = %+v, %v", r, err)
	}
	if _, err := l.RouteGet(netstat.RouteQuery{Dst: netip.MustParseAddr("192.0.2.99")}); !errors.Is(err, netstat.ErrNoRoute) {
		t.Errorf("RouteGet of an unknown destination = %v, want netstat.ErrNoRoute", err)
	}
	if s, err := l.Sockets(); err != nil || len(s) != 1 || s[0].State != netstat.StateEstablished {
		t.Errorf("Sockets = %+v, %v", s, err)
	}
	if ifs, err := l.Interfaces(); err != nil || len(ifs) != 3 || ifs[2].Name != "tailscale0" {
		t.Errorf("Interfaces = %+v, %v", ifs, err)
	}
	if l.PID() != 4242 {
		t.Errorf("PID = %d", l.PID())
	}
}

func TestTHFakeUnits(t *testing.T) {
	h := newTSHost(t)
	ctx := context.Background()
	u := h.env("").Units

	if st, err := u.State(ctx, tailscaledUnit); err != nil || st.ActiveState != "active" {
		t.Errorf("State(tailscaled) = %+v, %v", st, err)
	}
	if st, err := u.State(ctx, controllerUnit); err != nil || st.LoadState != "not-found" {
		t.Errorf("State(controller) = %+v, %v", st, err)
	}
	h.units.States[controllerUnit] = core.UnitState{LoadState: "loaded", ActiveState: "active"}
	if st, _ := u.State(ctx, controllerUnit); st.ActiveState != "active" {
		t.Errorf("State(controller) after a change = %+v", st)
	}
	if err := u.Restart(ctx, tailscaledUnit); err != nil || !reflect.DeepEqual(h.units.Restarts, []string{tailscaledUnit}) {
		t.Errorf("Restart(tailscaled): %v, recorded %v", err, h.units.Restarts)
	}
	for name, err := range map[string]error{
		"State of docker":           func() error { _, err := u.State(ctx, "docker.service"); return err }(),
		"Restart of the controller": u.Restart(ctx, controllerUnit),
		"Stop of tailscaled":        u.Stop(ctx, tailscaledUnit),
		"Start of tailscaled":       u.Start(ctx, tailscaledUnit),
		"a transient service":       u.StartTransientService(ctx, "darkpanel-task-x-apply-1.service", core.TransientService{}),
		"Reload":                    u.Reload(ctx),
	} {
		if err == nil {
			t.Errorf("%s was allowed", name)
		}
	}
}

func TestTHJournal(t *testing.T) {
	h := newTSHost(t)
	ctx := context.Background()
	env := h.env("c1")
	seq, err := env.Effects.Intend(ctx, core.Effect{Kind: core.EffectCreate, Resource: "tailscale.install"}, []byte("secret"))
	if err != nil || seq != 1 {
		t.Fatalf("Intend = %d, %v", seq, err)
	}
	if err := env.Effects.Made(ctx, seq, "tailscale.install", core.StateDoc{}); err != nil {
		t.Fatal(err)
	}
	if err := env.Effects.Skipped(ctx, seq); err == nil {
		t.Error("an effect already made was skipped")
	}
	got := h.journal.effects()
	if len(got) != 1 || got[0].ChangeID != "c1" || got[0].State != core.EffectMade || got[0].Post == nil {
		t.Errorf("effects %+v", got)
	}
	if s, err := env.Effects.Secret(ctx, seq); err != nil || string(s) != "secret" {
		t.Errorf("Secret = %q, %v", s, err)
	}
}
```

`internal/modules/tailscale/deps_test.go`:

```go
package tailscale

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform/tslocal"
)

// thManifestModule lets the registry check a manifest built from the module's pieces.
type thManifestModule struct{ m core.Manifest }

func (x thManifestModule) Manifest() core.Manifest { return x.m }

func TestTHConstants(t *testing.T) {
	if lockKey != "tailscale" || tailscaledUnit != "tailscaled.service" || controllerUnit != "darkpanel-controller.service" ||
		keyringPath != "/usr/share/keyrings/tailscale-archive-keyring.gpg" || sourcesPath != "/etc/apt/sources.list.d/tailscale.list" ||
		debDir != "/var/lib/darkpanel/agent/tailscale-debs" || pinCheckWindow != 120*time.Second || joinWait != 60*time.Second {
		t.Errorf("constants: %q %q %q %q %q %q %v %v", lockKey, tailscaledUnit, controllerUnit, keyringPath, sourcesPath, debDir, pinCheckWindow, joinWait)
	}
}

// TestTHProductionTS: the production wiring opens no connection, and a test binary never reaches the host's
// tailscaled socket.
func TestTHProductionTS(t *testing.T) {
	p := productionTS()
	if p.d.local == nil || p.d.link == nil || p.d.stun == nil || p.d.https == nil || p.d.osRelease == nil || p.d.hostname == nil || p.d.readFile == nil {
		t.Fatalf("incomplete wiring: %+v", p.d)
	}
	if p.d.arch != runtime.GOARCH {
		t.Errorf("arch %q, want %q", p.d.arch, runtime.GOARCH)
	}
	_, err := p.d.local().Status(context.Background())
	if !errors.Is(err, tslocal.ErrUnavailable) || !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Status in a test binary = %v, want a dial of a socket that does not exist", err)
	}
	want, _ := os.Hostname()
	if got, err := p.d.hostname(); err != nil || got != want {
		t.Errorf("hostname = %q, %v; want %q", got, err, want)
	}
	file := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(file, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if b, err := p.d.readFile(file); err != nil || string(b) != "data" {
		t.Errorf("readFile = %q, %v", b, err)
	}
	if p.d.link.PID() != os.Getpid() {
		t.Errorf("PID = %d, want %d", p.d.link.PID(), os.Getpid())
	}
	ifs, err := p.d.link.Interfaces()
	if err != nil || len(ifs) == 0 {
		t.Fatalf("Interfaces = %v, %v", ifs, err)
	}
	var loopback bool
	for _, i := range ifs {
		loopback = loopback || i.Loopback
	}
	if !loopback {
		t.Error("Interfaces lists no loopback interface")
	}
}

func TestTHModuleWiresTheProductionTS(t *testing.T) {
	m, ok := Module().(module)
	if !ok || m.t.d.local == nil || m.t.d.readFile == nil {
		t.Fatalf("Module() = %#v, want a module over the production ts", Module())
	}
}

func TestTHHTTPSProbe(t *testing.T) {
	var hits, redirected atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch r.URL.Path {
		case "/derp/latency-check":
			if r.Method != http.MethodGet {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			_, _ = w.Write([]byte("body that is discarded"))
		case "/empty":
			w.WriteHeader(http.StatusNoContent)
		case "/moved":
			http.Redirect(w, r, "/target", http.StatusFound)
		case "/target":
			redirected.Add(1)
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	t.Cleanup(srv.Close)
	probe := httpsProbe(srv.Client())
	ctx := context.Background()

	for _, path := range []string{"/derp/latency-check", "/empty"} {
		d, err := probe(ctx, srv.URL+path)
		if err != nil || d <= 0 || d > 5*time.Second {
			t.Errorf("probe %s = %v, %v; want a positive round trip", path, d, err)
		}
	}
	for path, want := range map[string]string{"/broken": "500", "/moved": "302"} {
		if _, err := probe(ctx, srv.URL+path); err == nil || !strings.Contains(err.Error(), want) || !strings.HasPrefix(err.Error(), "tailscale:") {
			t.Errorf("probe %s error = %v, want a tailscale: error naming status %s", path, err, want)
		}
	}
	if redirected.Load() != 0 {
		t.Error("the probe followed a redirect")
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := probe(cancelled, srv.URL+"/empty"); !errors.Is(err, context.Canceled) {
		t.Errorf("probe with a cancelled context = %v", err)
	}

	// A client that does not trust the server's certificate must fail: the probe does not skip verification.
	if _, err := httpsProbe(&http.Client{Timeout: 2 * time.Second})(ctx, srv.URL+"/empty"); err == nil {
		t.Error("the probe accepted a certificate the client does not trust")
	}
	if _, err := probe(ctx, "://bad"); err == nil {
		t.Error("the probe accepted a malformed URL")
	}
}

func TestTHReadOSRelease(t *testing.T) {
	read := func(content string) func(string) ([]byte, error) {
		return func(name string) ([]byte, error) {
			if name != "/etc/os-release" {
				return nil, fs.ErrNotExist
			}
			return []byte(content), nil
		}
	}
	for name, c := range map[string]struct {
		content string
		want    osInfo
	}{
		"ubuntu":              {"NAME=\"Ubuntu\"\nVERSION_CODENAME=noble\nID=ubuntu\nID_LIKE=debian\n", osInfo{"ubuntu", "noble"}},
		"debian quoted":       {"PRETTY_NAME=\"Debian GNU/Linux 12 (bookworm)\"\nID=\"debian\"\nVERSION_CODENAME='bookworm'\n", osInfo{"debian", "bookworm"}},
		"comments and blanks": {"# a comment\n\nID=debian\n  VERSION_CODENAME=trixie  \n#ID=ubuntu\n", osInfo{"debian", "trixie"}},
		"no codename":         {"ID=debian\nVERSION_ID=\"13\"\n", osInfo{ID: "debian"}},
		"empty":               {"", osInfo{}},
	} {
		got, err := readOSRelease(read(c.content))
		if err != nil || got != c.want {
			t.Errorf("%s: %+v, %v; want %+v", name, got, err, c.want)
		}
	}
	if _, err := readOSRelease(func(string) ([]byte, error) { return nil, fs.ErrNotExist }); !errors.Is(err, fs.ErrNotExist) || !strings.HasPrefix(err.Error(), "tailscale:") {
		t.Errorf("a missing file gave %v, want a tailscale: error wrapping fs.ErrNotExist", err)
	}
}

func TestTHAvailable(t *testing.T) {
	h := newTSHost(t)
	tt, ctx := h.ts(), context.Background()

	if err := tt.available(ctx, h.env("")); err != nil {
		t.Errorf("available with a running tailscaled = %v", err)
	}
	h.local.Errs = map[string]error{"Status": tslocal.ErrUnavailable}
	err := tt.available(ctx, h.env(""))
	coded := errcode.From(err)
	if err == nil || coded.Code != errcode.TailscaleUnavailable || coded.Detail != "tailscaled does not answer on its socket" {
		t.Fatalf("available with no socket = %#v, want tailscale.unavailable with the reason", err)
	}
	if !errors.Is(err, tslocal.ErrUnavailable) {
		t.Error("the cause is not kept in the chain")
	}
}

// thHang is a local API whose Status answers only when its context ends.
type thHang struct {
	thLocal
	entered chan struct{}
}

func (a thHang) Status(ctx context.Context) (*tslocal.Status, error) {
	close(a.entered)
	<-ctx.Done()
	return nil, ctx.Err()
}

// TestTHAvailableGivesUpAfterFiveSeconds: a tailscaled that accepts the connection and never answers makes the
// action unavailable once five seconds of the env's clock have passed.
func TestTHAvailableGivesUpAfterFiveSeconds(t *testing.T) {
	h := newTSHost(t)
	hang := thHang{thLocal: thLocal{h.local}, entered: make(chan struct{})}
	tt := h.ts()
	tt.d.local = func() localAPI { return hang }

	done := make(chan error, 1)
	go func() { done <- tt.available(context.Background(), h.env("")) }()
	<-hang.entered
	h.clock.Advance(4999 * time.Millisecond)
	select {
	case err := <-done:
		t.Fatalf("available returned %v before five seconds", err)
	default:
	}
	h.clock.Advance(time.Millisecond)
	select {
	case err := <-done:
		if errcode.From(err).Code != errcode.TailscaleUnavailable {
			t.Errorf("available = %v, want tailscale.unavailable", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("available did not return after the clock passed five seconds")
	}
}

func TestTHOwnedPathsAndExternalUnits(t *testing.T) {
	wantPaths := []core.OwnedPath{
		{Path: "/usr/share/keyrings/tailscale-archive-keyring.gpg", Kind: core.OwnedFile},
		{Path: "/etc/apt/sources.list.d/tailscale.list", Kind: core.OwnedFile},
		{Path: "/var/lib/darkpanel/agent/tailscale-debs", Kind: core.OwnedManagedDir, Pattern: `^tailscale_[0-9][0-9A-Za-z.+~-]{0,63}_(amd64|arm64)\.deb$`},
	}
	if got := ownedPaths(); !reflect.DeepEqual(got, wantPaths) {
		t.Errorf("ownedPaths = %+v\nwant %+v", got, wantPaths)
	}
	wantUnits := []core.ExternalUnit{
		{Name: "tailscaled.service", Ops: []string{core.UnitOpState, core.UnitOpRestart}},
		{Name: "darkpanel-controller.service", Ops: []string{core.UnitOpState}},
	}
	if got := externalUnits(); !reflect.DeepEqual(got, wantUnits) {
		t.Errorf("externalUnits = %+v\nwant %+v", got, wantUnits)
	}
	// The registry's own rules accept both declarations.
	m := core.Manifest{
		ID: ID, Version: 1, DefaultEnabled: true, OwnedPaths: ownedPaths(), ExternalUnits: externalUnits(),
		Migrations: map[core.Side]fs.FS{core.SideAgent: agentMigrations()},
	}
	if _, err := core.NewRegistry(thManifestModule{m}); err != nil {
		t.Errorf("the registry refuses the module's declarations: %v", err)
	}
}

func TestTHInstalledVersion(t *testing.T) {
	h := newTSHost(t)
	ctx := context.Background()
	for name, c := range map[string]struct {
		status string
		want   string
	}{
		"installed":                      {string(thDpkgStatus("1.104.1")), "1.104.1"},
		"epoch and revision are kept":    {string(thDpkgStatus("1:1.104.1-1~noble")), "1:1.104.1-1~noble"},
		"not listed":                     {string(thDpkgStatus("")), ""},
		"removed with its configuration": {"Package: tailscale\nStatus: deinstall ok config-files\nVersion: 1.102.0\n", ""},
		"unpacked only":                  {"Package: tailscale\nStatus: install ok unpacked\nVersion: 1.102.0\n", ""},
		"half installed":                 {"Package: tailscale\nStatus: install ok half-installed\nVersion: 1.102.0\n", ""},
		"a package whose name starts with tailscale": {"Package: tailscaled-extra\nStatus: install ok installed\nVersion: 3.0\n", ""},
		"the last stanza without a trailing newline": {"Package: curl\nStatus: install ok installed\nVersion: 8\n\nPackage: tailscale\nStatus: install ok installed\nVersion: 1.2.3", "1.2.3"},
		"an empty file": {"", ""},
	} {
		h.reads["/var/lib/dpkg/status"] = []byte(c.status)
		got, err := h.ts().installedVersion(ctx, h.env(""))
		if err != nil || got != c.want {
			t.Errorf("%s: %q, %v; want %q", name, got, err, c.want)
		}
	}
	delete(h.reads, "/var/lib/dpkg/status")
	if _, err := h.ts().installedVersion(ctx, h.env("")); !errors.Is(err, fs.ErrNotExist) || !strings.HasPrefix(err.Error(), "tailscale:") {
		t.Errorf("a missing status file gave %v, want a tailscale: error wrapping fs.ErrNotExist", err)
	}
}

func TestTHHeld(t *testing.T) {
	h := newTSHost(t)
	ctx := context.Background()
	for name, c := range map[string]struct {
		stdout string
		want   bool
	}{
		"held":               {"tailscale\n", true},
		"held among others":  {"linux-image-generic\ntailscale\nvim\n", true},
		"nothing held":       {"", false},
		"only a longer name": {"tailscaled\ntailscale-archive-keyring\n", false},
	} {
		h.script("/usr/bin/apt-mark showhold", tsRun{res: core.CmdResult{Stdout: []byte(c.stdout)}})
		got, err := h.ts().held(ctx, h.env(""))
		if err != nil || got != c.want {
			t.Errorf("%s: %v, %v; want %v", name, got, err, c.want)
		}
	}
	h.script("/usr/bin/apt-mark showhold", tsRun{res: core.CmdResult{ExitCode: 100, Stderr: []byte("E: lock\n")}})
	if _, err := h.ts().held(ctx, h.env("")); err == nil {
		t.Error("held ignored a failed apt-mark")
	}
}

func TestTHRun(t *testing.T) {
	h := newTSHost(t)
	ctx, env := context.Background(), h.env("")

	h.script("/usr/bin/apt-get install -o DPkg::Lock::Timeout=120 -y tailscale", tsRun{res: core.CmdResult{Stdout: []byte("done")}})
	res, err := run(ctx, env, "/usr/bin/apt-get", "install", "-o", "DPkg::Lock::Timeout=120", "-y", "tailscale")
	if err != nil || string(res.Stdout) != "done" {
		t.Fatalf("run = %+v, %v", res, err)
	}
	if want := []string{"/usr/bin/apt-get", "install", "-o", "DPkg::Lock::Timeout=120", "-y", "tailscale"}; !reflect.DeepEqual(h.ran[0], want) {
		t.Errorf("ran %v, want %v", h.ran[0], want)
	}

	h.script("/usr/bin/apt-get remove -o DPkg::Lock::Timeout=120 -y tailscale", tsRun{res: core.CmdResult{
		ExitCode: 100, Stdout: []byte("partial"), Stderr: []byte("Reading package lists...\nE: Could not get lock /var/lib/dpkg/lock-frontend\n"),
	}})
	res, err = run(ctx, env, "/usr/bin/apt-get", "remove", "-o", "DPkg::Lock::Timeout=120", "-y", "tailscale")
	if err == nil || res.ExitCode != 100 || string(res.Stdout) != "partial" {
		t.Fatalf("run of a failing command = %+v, %v; want the result and an error", res, err)
	}
	for _, want := range []string{"tailscale:", "/usr/bin/apt-get", "remove", "100", "E: Could not get lock /var/lib/dpkg/lock-frontend"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
	for _, leak := range []string{"Reading package lists", "DPkg::Lock::Timeout", "-y"} {
		if strings.Contains(err.Error(), leak) {
			t.Errorf("error %q holds %q: only the first argument and the last stderr line belong in it", err, leak)
		}
	}

	boom := errors.New("not allowlisted")
	h.script("/usr/bin/apt-mark showhold", tsRun{err: boom})
	if _, err := run(ctx, env, "/usr/bin/apt-mark", "showhold"); !errors.Is(err, boom) || !strings.Contains(err.Error(), "/usr/bin/apt-mark") {
		t.Errorf("a runner error gave %v, want it wrapped with the path", err)
	}
	if _, err := run(ctx, env, "/usr/bin/apt-mark", "hold", "evil"); err == nil {
		t.Error("run passed a vector the production allowlist refuses")
	}
}
```

`internal/modules/tailscale/wire_test.go`:

```go
package tailscale

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/schema"
)

func thWireTypes() []any {
	return []any{
		Status{}, Node{}, Peer{}, Latency{}, RegionLatency{}, Measurement{}, PrefsFields{}, PrefsSetInput{}, PrefsResult{},
		PrefsRepairInput{}, PrefsRepaired{}, LoginInput{}, LoginResult{}, LogoutInput{}, LogoutResult{}, PackageInstallInput{},
		PackageInstalled{}, PackageUpdateInput{}, PackageUpdated{}, PinInput{}, Pinned{}, UnpinInput{}, Unpinned{},
		CertGetInput{}, CertPair{},
	}
}

// TestTHWireTypesExist names every wire type, so a missing or renamed one fails the build.
func TestTHWireTypesExist(t *testing.T) {
	if n := len(thWireTypes()); n != 25 {
		t.Errorf("%d wire types listed, want 25", n)
	}
}

func TestTHWireJSON(t *testing.T) {
	handshake := time.Date(2026, 10, 31, 11, 59, 0, 0, time.UTC)
	yes, no := true, false
	none, exit := []string{}, ""
	tests := []struct {
		name string
		v    any
		want string
	}{
		{"status logged out", Status{Version: "1.104.1", BackendState: "NeedsLogin", Peers: []Peer{}, Health: []string{}},
			`{"version":"1.104.1","backendState":"NeedsLogin","self":null,"tailnet":"","magicDNSSuffix":"","preferredDerp":"","runSSH":false,"prefs":{},"peers":[],"health":[]}`},
		{"status running", Status{Version: "1.104.1", BackendState: "Running",
			Self:    &Node{ID: "n1", HostName: "web", DNSName: "web.example.ts.net", OS: "linux", Addresses: []string{"100.64.0.1"}, Tags: []string{}},
			Tailnet: "example.com", MagicDNSSuffix: "example.ts.net", PreferredDERP: "nyc", RunSSH: true, Prefs: PrefsFields{RunSSH: &yes}, Peers: []Peer{}, Health: []string{"a warning"}},
			`{"version":"1.104.1","backendState":"Running","self":{"id":"n1","hostName":"web","dnsName":"web.example.ts.net","os":"linux","addresses":["100.64.0.1"],"tags":[]},` +
				`"tailnet":"example.com","magicDNSSuffix":"example.ts.net","preferredDerp":"nyc","runSSH":true,"prefs":{"runSSH":true},"peers":[],"health":["a warning"]}`},
		{"peer", Peer{Node: Node{ID: "n2", HostName: "db", DNSName: "db.example.ts.net", OS: "linux", Addresses: []string{"100.64.0.2"}, Tags: []string{"tag:db"}},
			Online: true, Connection: "direct", Endpoint: "203.0.113.5:41641", ExitNodeOption: true, LastHandshake: &handshake},
			`{"id":"n2","hostName":"db","dnsName":"db.example.ts.net","os":"linux","addresses":["100.64.0.2"],"tags":["tag:db"],` +
				`"online":true,"connection":"direct","endpoint":"203.0.113.5:41641","region":"","exitNode":false,"exitNodeOption":true,"lastHandshake":"2026-10-31T11:59:00Z"}`},
		{"peer without a handshake", Peer{Node: Node{Addresses: []string{}, Tags: []string{}}, Connection: "none"},
			`{"id":"","hostName":"","dnsName":"","os":"","addresses":[],"tags":[],"online":false,"connection":"none","endpoint":"","region":"","exitNode":false,"exitNodeOption":false,"lastHandshake":null}`},
		{"latency", Latency{Regions: []RegionLatency{{RegionID: 1, Code: "nyc", Name: "New York City",
			STUN: Measurement{Available: true, MedianMs: 12.5}, HTTPS: Measurement{Reason: "timeout"}}}},
			`{"regions":[{"regionId":1,"code":"nyc","name":"New York City","stun":{"available":true,"medianMs":12.5,"reason":""},"https":{"available":false,"medianMs":0,"reason":"timeout"}}]}`},
		{"no prefs", PrefsFields{}, `{}`},
		{"a false bool is set", PrefsFields{WantRunning: &no}, `{"wantRunning":false}`},
		{"an empty route list is set", PrefsFields{AdvertiseRoutes: &none}, `{"advertiseRoutes":[]}`},
		{"an empty exit node is set", PrefsFields{ExitNode: &exit}, `{"exitNode":""}`},
		{"every pref", PrefsFields{WantRunning: &yes, AdvertiseRoutes: &none, ExitNode: &exit, AdvertiseExitNode: &no, AcceptRoutes: &yes, ShieldsUp: &no, RunSSH: &yes},
			`{"wantRunning":true,"advertiseRoutes":[],"exitNode":"","advertiseExitNode":false,"acceptRoutes":true,"shieldsUp":false,"runSSH":true}`},
		{"prefs set input flattens its embedded types", PrefsSetInput{Guarded: core.Guarded{Overrides: []string{"tailscale.link"}}, PrefsFields: PrefsFields{ShieldsUp: &yes}},
			`{"overrides":["tailscale.link"],"shieldsUp":true}`},
		{"prefs result", PrefsResult{Prefs: PrefsFields{RunSSH: &yes}}, `{"prefs":{"runSSH":true}}`},
		{"prefs repair input", PrefsRepairInput{Outcome: "keep"}, `{"outcome":"keep"}`},
		{"prefs repair input default", PrefsRepairInput{}, `{}`},
		{"prefs repaired", PrefsRepaired{RepairOutcome: "kept", Prefs: PrefsFields{ShieldsUp: &yes}}, `{"repairOutcome":"kept","prefs":{"shieldsUp":true}}`},
		{"login input", LoginInput{AuthKey: "tskey-auth-x", Hostname: "web", AdvertiseTags: []string{"tag:web"}, Relogin: true},
			`{"authKey":"tskey-auth-x","hostname":"web","advertiseTags":["tag:web"],"relogin":true}`},
		{"login input with only a key", LoginInput{AuthKey: "k"}, `{"authKey":"k"}`},
		{"login result", LoginResult{BackendState: "Running", DNSName: "web.example.ts.net", Addresses: []string{"100.64.0.7"}},
			`{"backendState":"Running","dnsName":"web.example.ts.net","addresses":["100.64.0.7"]}`},
		{"logout input", LogoutInput{}, `{}`},
		{"logout result", LogoutResult{BackendState: "NeedsLogin"}, `{"backendState":"NeedsLogin"}`},
		{"install input", PackageInstallInput{}, `{}`},
		{"installed", PackageInstalled{Version: "1.104.1"}, `{"version":"1.104.1"}`},
		{"update input", PackageUpdateInput{}, `{}`},
		{"updated", PackageUpdated{From: "1.102.0", To: "1.104.1"}, `{"from":"1.102.0","to":"1.104.1"}`},
		{"pin input", PinInput{Version: "1.102.0"}, `{"version":"1.102.0"}`},
		{"pinned", Pinned{Version: "1.102.0", PriorVersion: "1.104.1"}, `{"version":"1.102.0","priorVersion":"1.104.1","priorHeld":false}`},
		{"unpin input", UnpinInput{}, `{}`},
		{"unpinned", Unpinned{Version: "1.102.0", Held: true}, `{"version":"1.102.0","held":true}`},
		{"cert input", CertGetInput{}, `{}`},
		{"cert pair", CertPair{Name: "web.example.ts.net", CertPEM: "C", KeyPEM: "K", NotAfter: time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)},
			`{"name":"web.example.ts.net","certPem":"C","keyPem":"K","notAfter":"2026-12-01T00:00:00Z"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b, err := json.Marshal(tc.v)
			if err != nil {
				t.Fatal(err)
			}
			if string(b) != tc.want {
				t.Errorf("got  %s\nwant %s", b, tc.want)
			}
			back := reflect.New(reflect.TypeOf(tc.v))
			if err := json.Unmarshal(b, back.Interface()); err != nil {
				t.Fatal(err)
			}
			if again, _ := json.Marshal(back.Elem().Interface()); string(again) != tc.want {
				t.Errorf("round trip changed the document: %s", again)
			}
		})
	}
}

// TestTHWirePrefsSetInputDecodes: an input that carries the guard fields and a mask sets both parts.
func TestTHWirePrefsSetInputDecodes(t *testing.T) {
	var in PrefsSetInput
	if err := json.Unmarshal([]byte(`{"overrides":["tailscale.link"],"wantRunning":false,"exitNode":"100.64.0.9"}`), &in); err != nil {
		t.Fatal(err)
	}
	if len(in.Overrides) != 1 || in.WantRunning == nil || *in.WantRunning || in.ExitNode == nil || *in.ExitNode != "100.64.0.9" || in.ShieldsUp != nil {
		t.Errorf("decoded %+v", in)
	}
}

// TestTHWireDescriptions: every field of a wire type carries a description for the generated schema. An
// embedded type is checked through its own fields, and core.Guarded is the core's.
func TestTHWireDescriptions(t *testing.T) {
	var check func(rt reflect.Type)
	check = func(rt reflect.Type) {
		for i := range rt.NumField() {
			f := rt.Field(i)
			switch {
			case f.Anonymous && f.Type == reflect.TypeOf(core.Guarded{}):
			case f.Anonymous:
				check(f.Type)
			case f.Tag.Get("jsonschema_description") == "":
				t.Errorf("%s.%s has no jsonschema_description", rt.Name(), f.Name)
			}
		}
	}
	for _, v := range thWireTypes() {
		check(reflect.TypeOf(v))
	}
}

// thSchemaDef decodes a generated schema and returns the definition its root refers to, with every
// definition by name.
func thSchemaDef(t *testing.T, raw json.RawMessage) (def map[string]any, defs map[string]map[string]any) {
	t.Helper()
	var doc struct {
		Ref  string                    `json:"$ref"`
		Defs map[string]map[string]any `json:"$defs"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("schema %s: %v", raw, err)
	}
	def = doc.Defs[strings.TrimPrefix(doc.Ref, "#/$defs/")]
	if def == nil {
		t.Fatalf("schema %s: no definition for %q", raw, doc.Ref)
	}
	return def, doc.Defs
}

func thProps(t *testing.T, def map[string]any) map[string]map[string]any {
	t.Helper()
	props, _ := def["properties"].(map[string]any)
	out := map[string]map[string]any{}
	for k, v := range props {
		out[k], _ = v.(map[string]any)
	}
	return out
}

func thRequired(def map[string]any) []string {
	var out []string
	list, _ := def["required"].([]any)
	for _, v := range list {
		out = append(out, v.(string))
	}
	return out
}

// TestTHWireSchemas: the schema generator accepts every input and output type, marks the secrets, and keeps
// the version pattern and the nullable members.
func TestTHWireSchemas(t *testing.T) {
	for _, v := range []any{PrefsSetInput{}, PrefsRepairInput{}, LoginInput{}, LogoutInput{}, PackageInstallInput{}, PackageUpdateInput{}, PinInput{}, UnpinInput{}, CertGetInput{}} {
		if _, err := schema.Input(reflect.TypeOf(v)); err != nil {
			t.Errorf("input schema of %T: %v", v, err)
		}
	}
	for _, v := range []any{Status{}, Latency{}, PrefsResult{}, PrefsRepaired{}, LoginResult{}, LogoutResult{}, PackageInstalled{}, PackageUpdated{}, Pinned{}, Unpinned{}, CertPair{}} {
		if _, err := schema.Output(reflect.TypeOf(v)); err != nil {
			t.Errorf("output schema of %T: %v", v, err)
		}
	}

	raw, err := schema.Input(reflect.TypeOf(LoginInput{}))
	if err != nil {
		t.Fatal(err)
	}
	def, _ := thSchemaDef(t, raw)
	props := thProps(t, def)
	if props["authKey"]["x-secret"] != true {
		t.Errorf("authKey is not marked secret: %v", props["authKey"])
	}
	if req := thRequired(def); !reflect.DeepEqual(req, []string{"authKey"}) {
		t.Errorf("login input requires %v, want only authKey", req)
	}

	raw, err = schema.Input(reflect.TypeOf(PrefsSetInput{}))
	if err != nil {
		t.Fatal(err)
	}
	def, _ = thSchemaDef(t, raw)
	props = thProps(t, def)
	for _, name := range []string{"wantRunning", "advertiseRoutes", "exitNode", "advertiseExitNode", "acceptRoutes", "shieldsUp", "runSSH", "overrides"} {
		if props[name] == nil {
			t.Errorf("prefs.set input lacks %s", name)
		}
	}
	if req := thRequired(def); len(req) != 0 {
		t.Errorf("prefs.set input requires %v, want every field optional", req)
	}

	raw, err = schema.Input(reflect.TypeOf(PinInput{}))
	if err != nil {
		t.Fatal(err)
	}
	def, _ = thSchemaDef(t, raw)
	if got := thProps(t, def)["version"]["pattern"]; got != `^[0-9][0-9A-Za-z.+~-]{0,63}$` {
		t.Errorf("version pattern %v", got)
	}

	raw, err = schema.Output(reflect.TypeOf(CertPair{}))
	if err != nil {
		t.Fatal(err)
	}
	def, _ = thSchemaDef(t, raw)
	if props := thProps(t, def); props["keyPem"]["x-secret"] != true || props["certPem"]["x-secret"] != nil {
		t.Errorf("cert pair secrets: keyPem %v, certPem %v", props["keyPem"], props["certPem"])
	}

	raw, err = schema.Output(reflect.TypeOf(Status{}))
	if err != nil {
		t.Fatal(err)
	}
	def, defs := thSchemaDef(t, raw)
	if b, _ := json.Marshal(thProps(t, def)["self"]); !strings.Contains(string(b), `"null"`) {
		t.Errorf("status.self is not nullable: %s", b)
	}
	if b, _ := json.Marshal(thProps(t, defs["Peer"])["lastHandshake"]); !strings.Contains(string(b), `"null"`) {
		t.Errorf("peer.lastHandshake is not nullable: %s", b)
	}
}
```

`internal/modules/tailscale/store_test.go`:

```go
package tailscale

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
	"time"
)

var thNow = time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)

func thColumns(t *testing.T, h *tsHost, table string) []string {
	t.Helper()
	rows, err := h.db.Read().Query(`SELECT name FROM pragma_table_info(?) ORDER BY cid`, table)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var cols []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			t.Fatal(err)
		}
		cols = append(cols, c)
	}
	return cols
}

func thPin(id string, at time.Time) pinRow {
	return pinRow{
		ChangeID: id, Version: "1.102.0", Arch: "amd64", PriorVersion: "1.104.1", PriorHeld: true, PriorBackend: "Running",
		LinkTailnet: true, Phase: "recorded", UpdatedAt: at,
	}
}

func thInsertPin(t *testing.T, h *tsHost, p pinRow) {
	t.Helper()
	ctx := context.Background()
	if err := h.db.Write(ctx, func(tx *sql.Tx) error { return insertPin(ctx, tx, p) }); err != nil {
		t.Fatal(err)
	}
}

func thSteps(t *testing.T, h *tsHost, changeID string) [][3]string {
	t.Helper()
	rows, err := h.db.Read().Query(`SELECT seq, step, result FROM tailscale_pin_steps WHERE change_id = ? ORDER BY seq`, changeID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out [][3]string
	next := 1
	for rows.Next() {
		var seq int
		var step, result string
		if err := rows.Scan(&seq, &step, &result); err != nil {
			t.Fatal(err)
		}
		if seq != next {
			t.Errorf("step %d has seq %d, want %d", len(out), seq, next)
		}
		next++
		out = append(out, [3]string{"", step, result})
	}
	return out
}

func TestTHMigrationShape(t *testing.T) {
	h := newTSHost(t)
	for table, want := range map[string][]string{
		"tailscale_pins": {"change_id", "version", "arch", "prior_version", "prior_held", "prior_backend", "link_tailnet", "phase",
			"staged_deb", "staged_sha256", "unpinned_by", "updated_at"},
		"tailscale_pin_steps": {"change_id", "seq", "step", "result", "at"},
		"tailscale_installs":  {"change_id", "version", "installed_at", "removed_at"},
	} {
		if got := thColumns(t, h, table); !reflect.DeepEqual(got, want) {
			t.Errorf("%s columns %v, want %v", table, got, want)
		}
		var strict int
		if err := h.db.Read().QueryRow(`SELECT strict FROM pragma_table_list WHERE name = ?`, table).Scan(&strict); err != nil || strict != 1 {
			t.Errorf("%s: strict = %d, %v; want a STRICT table", table, strict, err)
		}
	}
}

func TestTHMigrationConstraints(t *testing.T) {
	h := newTSHost(t)
	ctx := context.Background()
	insert := func(p pinRow) error {
		return h.db.Write(ctx, func(tx *sql.Tx) error { return insertPin(ctx, tx, p) })
	}
	bad := thPin("c-phase", thNow)
	bad.Phase = "paused"
	if err := insert(bad); err == nil {
		t.Error("a phase outside the list was stored")
	}
	for _, phase := range []string{"recorded", "unheld", "staged", "installed", "held", "checking", "done", "rolling_back", "rolled_back", "failed"} {
		p := thPin("c-"+phase, thNow)
		p.Phase = phase
		if err := insert(p); err != nil {
			t.Errorf("phase %s refused: %v", phase, err)
		}
	}
	if err := insert(thPin("c-recorded", thNow)); err == nil {
		t.Error("a pin was stored twice under one change id")
	}
	if err := h.db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE tailscale_pins SET prior_held = 2 WHERE change_id = 'c-held'`)
		return err
	}); err == nil {
		t.Error("prior_held 2 was stored")
	}
	if err := addPinStep(ctx, h.db, "no-such-pin", "unheld", "ok", thNow); err == nil {
		t.Error("a step of an unknown pin was stored: the foreign key must hold")
	}
}

func TestTHPinRoundTrip(t *testing.T) {
	h := newTSHost(t)
	ctx := context.Background()
	if _, ok, err := loadPin(ctx, h.db.Read(), "c1"); ok || err != nil {
		t.Fatalf("loadPin of none = %v, %v", ok, err)
	}
	want := thPin("c1", thNow)
	want.StagedDeb, want.StagedSHA256 = "tailscale_1.104.1_amd64.deb", "ab12"
	want.UnpinnedBy = "c9"
	thInsertPin(t, h, want)
	got, ok, err := loadPin(ctx, h.db.Read(), "c1")
	if err != nil || !ok || !reflect.DeepEqual(got, want) || !got.UpdatedAt.Equal(thNow) {
		t.Fatalf("loadPin = %+v, %v, %v\nwant %+v", got, ok, err, want)
	}

	// Times are stored as fixed-width UTC text, whatever zone they arrive in.
	zone := time.FixedZone("x", 2*3600)
	p := thPin("c2", time.Date(2026, 10, 31, 14, 0, 0, 5, zone))
	thInsertPin(t, h, p)
	var raw string
	if err := h.db.Read().QueryRow(`SELECT updated_at FROM tailscale_pins WHERE change_id = 'c2'`).Scan(&raw); err != nil || raw != "2026-10-31T12:00:00.000000005Z" {
		t.Errorf("stored time %q, %v; want 2026-10-31T12:00:00.000000005Z", raw, err)
	}
}

func TestTHPinUpdates(t *testing.T) {
	h := newTSHost(t)
	ctx := context.Background()
	thInsertPin(t, h, thPin("c1", thNow))
	later := thNow.Add(time.Minute)

	if err := setPinPhase(ctx, h.db, "c1", "unheld", later); err != nil {
		t.Fatal(err)
	}
	p, _, _ := loadPin(ctx, h.db.Read(), "c1")
	if p.Phase != "unheld" || !p.UpdatedAt.Equal(later) {
		t.Errorf("after setPinPhase: %+v", p)
	}

	evenLater := later.Add(time.Minute)
	if err := setPinStaged(ctx, h.db, "c1", "tailscale_1.104.1_amd64.deb", "ab12", evenLater); err != nil {
		t.Fatal(err)
	}
	p, _, _ = loadPin(ctx, h.db.Read(), "c1")
	if p.StagedDeb != "tailscale_1.104.1_amd64.deb" || p.StagedSHA256 != "ab12" || p.Phase != "unheld" || !p.UpdatedAt.Equal(evenLater) {
		t.Errorf("after setPinStaged: %+v; the phase must stay as it was", p)
	}

	if err := setPinUnpinned(ctx, h.db, "c1", "c7", evenLater.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	p, _, _ = loadPin(ctx, h.db.Read(), "c1")
	if p.UnpinnedBy != "c7" || p.Phase != "unheld" {
		t.Errorf("after setPinUnpinned: %+v", p)
	}

	if err := setPinPhase(ctx, h.db, "c1", "paused", later); err == nil {
		t.Error("an unknown phase was accepted")
	}
	if p, _, _ := loadPin(ctx, h.db.Read(), "c1"); p.Phase != "unheld" {
		t.Errorf("a refused phase changed the row to %s", p.Phase)
	}
	for name, err := range map[string]error{
		"setPinPhase":    setPinPhase(ctx, h.db, "nope", "done", later),
		"setPinStaged":   setPinStaged(ctx, h.db, "nope", "d", "s", later),
		"setPinUnpinned": setPinUnpinned(ctx, h.db, "nope", "c", later),
	} {
		if err == nil {
			t.Errorf("%s of an unknown pin succeeded", name)
		}
	}
}

func TestTHPinSteps(t *testing.T) {
	h := newTSHost(t)
	ctx := context.Background()
	thInsertPin(t, h, thPin("c1", thNow))
	thInsertPin(t, h, thPin("c2", thNow))
	for i, s := range [][2]string{{"unhold", "ok"}, {"stage prior", "ok"}, {"install", "exit 100"}} {
		if err := addPinStep(ctx, h.db, "c1", s[0], s[1], thNow.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	if err := addPinStep(ctx, h.db, "c2", "unhold", "ok", thNow); err != nil {
		t.Fatal(err)
	}
	got := thSteps(t, h, "c1")
	want := [][3]string{{"", "unhold", "ok"}, {"", "stage prior", "ok"}, {"", "install", "exit 100"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("steps of c1 %v, want %v", got, want)
	}
	if got := thSteps(t, h, "c2"); len(got) != 1 {
		t.Errorf("steps of c2 %v, want one: each pin numbers its own steps from 1", got)
	}
}

func TestTHActivePin(t *testing.T) {
	h := newTSHost(t)
	ctx := context.Background()
	active := func() (string, bool) {
		p, ok, err := activePin(ctx, h.db.Read())
		if err != nil {
			t.Fatal(err)
		}
		return p.ChangeID, ok
	}
	if id, ok := active(); ok {
		t.Fatalf("activePin with no pins = %q", id)
	}
	a, b, c := thPin("a", thNow), thPin("b", thNow.Add(10*time.Minute)), thPin("c", thNow.Add(20*time.Minute))
	a.Phase, b.Phase, c.Phase = "done", "done", "checking"
	thInsertPin(t, h, a)
	thInsertPin(t, h, b)
	thInsertPin(t, h, c)
	if id, ok := active(); !ok || id != "b" {
		t.Errorf("activePin = %q, %v; want b: the newest pin in phase done (c is still checking)", id, ok)
	}
	if err := setPinUnpinned(ctx, h.db, "b", "u1", thNow.Add(30*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if id, ok := active(); !ok || id != "a" {
		t.Errorf("activePin after b was unpinned = %q, %v; want a", id, ok)
	}
	if err := setPinUnpinned(ctx, h.db, "a", "u2", thNow.Add(31*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if id, ok := active(); ok {
		t.Errorf("activePin with every done pin unpinned = %q", id)
	}

	// Two pins with one update time: the later change id is the newer.
	d, e := thPin("d1", thNow.Add(time.Hour)), thPin("d2", thNow.Add(time.Hour))
	d.Phase, e.Phase = "done", "done"
	thInsertPin(t, h, d)
	thInsertPin(t, h, e)
	if id, _ := active(); id != "d2" {
		t.Errorf("activePin on a tie = %q, want d2", id)
	}
}

func TestTHInstalls(t *testing.T) {
	h := newTSHost(t)
	ctx := context.Background()
	if _, _, ok, err := liveInstall(ctx, h.db.Read()); ok || err != nil {
		t.Fatalf("liveInstall with none = %v, %v", ok, err)
	}
	insert := func(id, version string, at time.Time) error {
		return h.db.Write(ctx, func(tx *sql.Tx) error { return insertInstall(ctx, tx, id, version, at) })
	}
	if err := insert("i1", "1.102.0", thNow); err != nil {
		t.Fatal(err)
	}
	if err := insert("i1", "1.102.0", thNow); err == nil {
		t.Error("an install was recorded twice under one change id")
	}
	if id, v, ok, err := liveInstall(ctx, h.db.Read()); err != nil || !ok || id != "i1" || v != "1.102.0" {
		t.Errorf("liveInstall = %q, %q, %v, %v", id, v, ok, err)
	}
	if err := insert("i2", "1.104.1", thNow.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if id, _, _, _ := liveInstall(ctx, h.db.Read()); id != "i2" {
		t.Errorf("liveInstall = %q, want the newest install i2", id)
	}
	if err := setInstallRemoved(ctx, h.db, "i2", thNow.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if id, _, ok, _ := liveInstall(ctx, h.db.Read()); !ok || id != "i1" {
		t.Errorf("liveInstall after i2 was removed = %q, %v; want i1", id, ok)
	}
	if err := setInstallRemoved(ctx, h.db, "i1", thNow.Add(3*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, _, ok, _ := liveInstall(ctx, h.db.Read()); ok {
		t.Error("liveInstall found an install after every one was removed")
	}
	if err := setInstallRemoved(ctx, h.db, "nope", thNow); err == nil {
		t.Error("removing an unknown install succeeded")
	}
}
```

`internal/modules/tailscale/preflight_test.go`:

```go
package tailscale

import (
	"context"
	"errors"
	"io/fs"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)

func thPreflight(t *testing.T, info osInfo, osErr error, arch string) error {
	t.Helper()
	tt := ts{d: deps{osRelease: func() (osInfo, error) { return info, osErr }, arch: arch}}
	checks := tt.preflight()
	if len(checks) != 1 || checks[0].ID != "tailscale.os" || checks[0].Run == nil {
		t.Fatalf("preflight = %+v, want the one check tailscale.os", checks)
	}
	return checks[0].Run(context.Background(), core.Env{})
}

func TestTHPreflightPasses(t *testing.T) {
	for _, c := range []struct {
		info osInfo
		arch string
	}{
		{osInfo{"ubuntu", "jammy"}, "amd64"}, {osInfo{"ubuntu", "noble"}, "arm64"},
		{osInfo{"debian", "bookworm"}, "amd64"}, {osInfo{"debian", "trixie"}, "arm64"},
	} {
		if err := thPreflight(t, c.info, nil, c.arch); err != nil {
			t.Errorf("%+v on %s: %v", c.info, c.arch, err)
		}
	}
}

// TestTHPreflightFails: each reason names what was found, and starts with the package name. The check reads
// only the operating system, never tailscaled.
func TestTHPreflightFails(t *testing.T) {
	for name, c := range map[string]struct {
		info osInfo
		arch string
		want []string
	}{
		"ubuntu too old":               {osInfo{"ubuntu", "focal"}, "amd64", []string{"ubuntu", "focal"}},
		"debian too old":               {osInfo{"debian", "bullseye"}, "amd64", []string{"debian", "bullseye"}},
		"codename of the other distro": {osInfo{"ubuntu", "bookworm"}, "amd64", []string{"ubuntu", "bookworm"}},
		"another distribution":         {osInfo{"alpine", ""}, "amd64", []string{"alpine"}},
		"no codename":                  {osInfo{"debian", ""}, "amd64", []string{"debian"}},
		"32-bit x86":                   {osInfo{"ubuntu", "noble"}, "386", []string{"386", "amd64", "arm64"}},
		"32-bit arm":                   {osInfo{"debian", "bookworm"}, "arm", []string{"arm"}},
	} {
		err := thPreflight(t, c.info, nil, c.arch)
		if err == nil || !strings.HasPrefix(err.Error(), "tailscale:") {
			t.Errorf("%s: error %v, want one starting with tailscale:", name, err)
			continue
		}
		for _, part := range c.want {
			if !strings.Contains(err.Error(), part) {
				t.Errorf("%s: error %q lacks %q", name, err, part)
			}
		}
	}
	if err := thPreflight(t, osInfo{}, fs.ErrNotExist, "amd64"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("an unreadable os-release gave %v, want the read error", err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mkdir -p /root/.cache/w4-tmp/8 && export TMPDIR=/root/.cache/w4-tmp/8 GOTOOLCHAIN=local && timeout 300 go test -count=1 ./internal/modules/tailscale/ 2>&1 | head -20`
Expected: FAIL to build, with errors such as `undefined: localAPI`, `undefined: linkHost`, `undefined: ts`, `undefined: deps`, `undefined: osInfo`, `undefined: pinRow`. Quote the first lines in the report.

- [ ] **Step 3: Implement**

1. `migrations/agent/0001_tailscale.sql`: the migration of Contracts "Tailscale internals", exactly this (it is a fixed schema the spec names):

   ```sql
   -- One row per tailscale.package.pin change: its agent-local transaction (spec 4.1).
   CREATE TABLE tailscale_pins (
       change_id      TEXT PRIMARY KEY,
       version        TEXT NOT NULL,
       arch           TEXT NOT NULL,
       prior_version  TEXT NOT NULL,
       prior_held     INTEGER NOT NULL CHECK (prior_held IN (0, 1)),
       prior_backend  TEXT NOT NULL,
       link_tailnet   INTEGER NOT NULL CHECK (link_tailnet IN (0, 1)),
       phase          TEXT NOT NULL CHECK (phase IN ('recorded', 'unheld', 'staged', 'installed', 'held', 'checking', 'done', 'rolling_back', 'rolled_back', 'failed')),
       staged_deb     TEXT NOT NULL DEFAULT '',
       staged_sha256  TEXT NOT NULL DEFAULT '',
       unpinned_by    TEXT NOT NULL DEFAULT '',
       updated_at     TEXT NOT NULL
   ) STRICT;
   -- Every step a pin or its rollback took, in order.
   CREATE TABLE tailscale_pin_steps (
       change_id  TEXT NOT NULL REFERENCES tailscale_pins (change_id),
       seq        INTEGER NOT NULL,
       step       TEXT NOT NULL,
       result     TEXT NOT NULL,
       at         TEXT NOT NULL,
       PRIMARY KEY (change_id, seq)
   ) STRICT;
   -- The package installs Darkpanel made, so uninstall removes only a package it installed.
   CREATE TABLE tailscale_installs (
       change_id    TEXT PRIMARY KEY,
       version      TEXT NOT NULL,
       installed_at TEXT NOT NULL,
       removed_at   TEXT NOT NULL DEFAULT ''
   ) STRICT;
   ```

2. `tailscale.go`: replace `type module struct{}` with `type module struct{ t ts }` and `Module()` with `return module{t: productionTS()}`. `Manifest()` and its doc comment stay as they are (still the empty skeleton).
3. `wire.go`: the types of Contracts "Tailscale wire shapes", copied with the field names, types and `json` tags exactly as written there, gofmt-aligned, each with a doc comment, and a `jsonschema_description:"..."` tag on every field (a short sentence stating what the field is; the embedded `Node`, `PrefsFields` and `core.Guarded` carry none). Two schema annotations beyond the Contract, so the generated schemas stay valid: `jsonschema:"nullable"` on `Status.Self` and `Peer.LastHandshake` (`Self` is null while logged out and `LastHandshake` for a peer that never handshook; the docker module's `Container.Usage` is tagged the same way), and on `PinInput.Version` the pattern of Global Constraints, written in the tag as `jsonschema:"pattern=^[0-9][0-9A-Za-z.+~-]{0\\,63}$"` (the comma is escaped as `\\,`, as in `HostCreateInput.Name` of `internal/core/fleet.go`). The empty inputs (`PackageInstallInput`, `PackageUpdateInput`, `UnpinInput`, `CertGetInput`) stay `struct{}` and `LogoutInput` stays `struct{ core.Guarded }`, as the Contract gives them.
4. `store.go`: `//go:embed migrations/agent/*.sql` and `agentMigrations() fs.FS` (panics only when `fs.Sub` fails on its constant path, as the docker module's does); `storeTimeFormat = "2006-01-02T15:04:05.000000000Z"`, used to write every stored time in UTC; the `queryer` interface, `pinRow` and the ten functions of Contracts "Tailscale internals", with these rules:
   - `insertPin` writes every column of `pinRow`; `loadPin` and `activePin` read them back (`PriorHeld` and `LinkTailnet` as booleans, `UpdatedAt` parsed from the stored text); a missing row is `ok == false` and a nil error.
   - `activePin` is the pin in phase `done` with an empty `unpinned_by` whose `updated_at` is greatest, ties broken by the greater `change_id`.
   - `setPinPhase`, `setPinStaged` and `setPinUnpinned` each update their columns and `updated_at`, run in `db.Write`, and fail with an error naming the change when no row matched. `setPinStaged` leaves the phase alone. A phase outside the migration's list fails through the CHECK constraint and leaves the row as it was.
   - `addPinStep` assigns `seq` as one more than the pin's current maximum (1 for the first step) inside the write transaction, so each pin numbers its own steps; a step for an unknown pin fails on the foreign key.
   - `insertInstall` fails when the change id is recorded already. `liveInstall` returns the install whose `removed_at` is empty with the greatest `installed_at` (ties: greater `change_id`). `setInstallRemoved` fails when no row matched.
   - Build each statement as a constant string, one per setter, not by joining SQL text (gosec G202 fails the lint otherwise).
5. `preflight.go`: `func (t ts) preflight() []core.PreflightCheck` returns one check with id `tailscale.os`. Its `Run` calls `t.d.osRelease()` and returns that error unchanged; refuses with an error starting `tailscale:` that names the id and the codename found when the pair is not one of ubuntu with jammy or noble, or debian with bookworm or trixie; then refuses with an error starting `tailscale:` that names the architecture when `t.d.arch` is neither `amd64` nor `arm64`. It reads nothing else and never calls tailscaled.
6. `deps.go`:
   - The constants of Contracts "Tailscale internals", with the values given there (`debDir` is `platform.TailscaleDebDir`).
   - `ts`, `deps`, `localAPI`, `linkHost` and `osInfo` exactly as in the Contract; `liveLinkHost` is an empty struct whose `Sockets`, `Interfaces` and `RouteGet` call `netstat.Sockets`, `netstat.Interfaces` and `netstat.RouteGet`, and whose `PID` is `os.Getpid`.
   - `productionTS() ts` builds one `tslocal.Client` and wires `local` to return it, `link` to `liveLinkHost{}`, `stun` to `stun.Probe`, `https` to `httpsProbe(&http.Client{Timeout: 5 * time.Second})`, `osRelease` to `readOSRelease(os.ReadFile)`, `arch` to `runtime.GOARCH`, `hostname` to `os.Hostname` and `readFile` to `os.ReadFile`. The client's socket is `tslocal.DefaultSocket`, except that when `testing.Testing()` is true it is a path that never exists (`/nonexistent/darkpanel/tailscaled.sock`), because test binaries that link the module must never reach the host's tailscaled (the docker module does the same with its `absentSocket`; put the reason in a comment).
   - `httpsProbe(c)` copies the client, sets its `CheckRedirect` to return `http.ErrUseLastResponse`, and returns a function that sends one GET to the URL under the caller's context, discards the body, and returns the elapsed wall time. A status outside 200 to 299, a transport error and a malformed URL are errors starting `tailscale: probe <url>:`, the status error naming the status code; certificates are verified by the client as given (no `InsecureSkipVerify`).
   - `readOSRelease(read)` reads `/etc/os-release` through `read` (a read error is returned wrapped as `tailscale: read the operating system release: %w`), skips blank lines and lines starting with `#`, splits `KEY=value` lines (surrounding spaces trimmed), strips one pair of matching single or double quotes from the value, and takes `ID` and `VERSION_CODENAME`; a missing key leaves the field empty.
   - `func (t ts) available(ctx context.Context, env core.Env) error`: use `env.Clock` (the system clock when nil) to arm a 5 s timer that cancels a derived context, the way the docker module's preflight does; call `t.d.local().Status(ctx)`; any error becomes `errcode.Wrap(errcode.TailscaleUnavailable, err, "tailscaled does not answer on its socket")` (the cause stays in the chain); success returns nil. Stop the timer on return.
   - `ownedPaths()` and `externalUnits()` return exactly the values `TestTHOwnedPathsAndExternalUnits` lists.
   - `run(ctx, env, path, args...)`: runs `core.Cmd{Path, Args}` through `env.Exec`. A runner error returns `tailscale: run <path>: %w`. A non-zero exit returns the result together with an error `tailscale: <path> <first argument> exited <code>: <last non-empty line of stderr>` (the path alone when there are no arguments); no other argument and no other stderr line appears in it, because arguments can carry secrets.
   - `func (t ts) held(ctx, env) (bool, error)`: `run` of `/usr/bin/apt-mark showhold`; true when a stdout line, trimmed, equals `tailscale`.
   - `func (t ts) installedVersion(ctx, env) (string, error)`: reads `/var/lib/dpkg/status` through `t.d.readFile` (an error, a missing file included, is returned wrapped as `tailscale: read dpkg status: %w`); splits it into stanzas at blank lines; a stanza counts when its `Package:` field is exactly `tailscale` and its `Status:` field is three words ending in `installed` (so `config-files`, `unpacked` and `half-installed` do not count); returns that stanza's `Version:` value, or `""` when no stanza counts. Continuation lines (starting with a space) are ignored.
7. Nothing in this task starts a program, writes outside a temporary directory or talks to a socket: the tests reach every host effect through the fakes.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `export TMPDIR=/root/.cache/w4-tmp/8 GOTOOLCHAIN=local && timeout 300 go test -count=1 -run 'TestTH|TestManifestIsASkeleton' -v ./internal/modules/tailscale/ 2>&1 | tail -40`
Expected: PASS for every test, including `TestManifestIsASkeleton`.

- [ ] **Step 5: Run the task's checks**

Run each with `export TMPDIR=/root/.cache/w4-tmp/8 GOTOOLCHAIN=local` and an explicit timeout, and record the real output:

```bash
timeout 300 go build ./...
timeout 300 go vet ./...
timeout 900 go test -count=1 ./...
timeout 600 go test -race -count=1 ./internal/modules/tailscale/
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
rm -rf /root/.cache/w4-tmp/8 && go clean -testcache
```

Expected: `go build` and `go vet` print nothing; every `go test` line is `ok` or `?`; the lint prints `0 issues.`; `go mod tidy -diff` prints nothing. No schema snapshot changes in this task (it registers no action), so `make schemas` is not run.

- [ ] **Step 6: Commit**

```bash
git add internal/modules/tailscale/tailscale.go internal/modules/tailscale/deps.go internal/modules/tailscale/wire.go internal/modules/tailscale/store.go internal/modules/tailscale/preflight.go internal/modules/tailscale/migrations/agent/0001_tailscale.sql internal/modules/tailscale/fake_local_test.go internal/modules/tailscale/testhost_test.go internal/modules/tailscale/testhost_check_test.go internal/modules/tailscale/deps_test.go internal/modules/tailscale/wire_test.go internal/modules/tailscale/store_test.go internal/modules/tailscale/preflight_test.go
git commit -m "feat(tailscale): add module seam and test host"
```

### Task 9: coreactions: core.host.create with a tailnet join

**Files:**
- Create: `internal/controller/coreactions/hostjoin.go`
- Modify: `internal/controller/coreactions/hostenroll.go` (`hostCreate.Plan` and `hostCreate.Apply`)
- Test: `internal/controller/coreactions/hostjoin_test.go`
- Unchanged: `internal/controller/coreactions/hostenroll_test.go` and `registry_test.go` (Task 7 already moved `TestCoreActionSecrets` to expect `bundle` beside `token`)

Read, do not edit: `internal/controller/listen/tailscale.go` (`LoadTailscaleAddrs`), `internal/controller/listen/agent.go` (`AgentPort`), `internal/controller/fleet/tokens.go` (`IssueToken`, `TokenTTL`), `internal/controller/coreactions/module.go` (`Deps.Modules`), `enablement.go`, `hosts.go` (`moduleStates`), and the test helpers `newEnrollEnv`, `enrollEnv.checkGrant`, `noSecret`, `wantField`, `wantCode`, `applyDirect` and `setModuleState` in the package's existing `_test.go` files.

**Interfaces:**
- Consumes: Contracts "Core host join" (`core.HostCreateInput.TailnetJoin` and `Tags`, `core.EnrollmentGrant.Bundle`, `core.EnrollJoiner`, `core.JoinRequest`, all written by Task 7); "Bootstrap bundle" (`transport.Bundle`, `transport.ParseBootstrap`, written by Task 2); `listen.LoadTailscaleAddrs` and `listen.AgentPort`; `fleet.TokenTTL` and `Authority.IssueToken`; `errcode.InvalidInput`, `errcode.PlanStateChanged`.
- Produces: Contracts "Host create join", exactly: `core.host.create` with `tailnetJoin` plans and applies through the one enabled `core.EnrollJoiner` among `Deps.Modules`, and `controllerTailnetAddr(ctx context.Context, db *store.DB) (string, error)` in the new file `hostjoin.go`. Private to the package: `(module).joiner(ctx context.Context, q querier) (core.EnrollJoiner, error)`.
- Errors, all `request.invalid_input` with one field error whose message is the detail: tags without `tailnetJoin` (field `/tags`, detail `tags need tailnetJoin`); `controllerAddress` together with `tailnetJoin` (field `/controllerAddress`, detail `a tailnet join uses the controller's tailnet address; leave controllerAddress out`); no enabled joiner (field `/tailnetJoin`, detail `tailnet join needs the tailnet module`); no saved Tailscale address (field `/tailnetJoin`, detail `the controller is not on a tailnet`).

**Items:** 24

**Depends on:** Task 2, Task 7

- [ ] **Step 1: Write the failing tests**

Create `internal/controller/coreactions/hostjoin_test.go`:

```go
package coreactions

import (
	"context"
	"errors"
	"net/netip"
	"reflect"
	"sync"
	"testing"

	"github.com/darkraise/darkpanel/internal/controller/fleet"
	"github.com/darkraise/darkpanel/internal/controller/listen"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/transport"
)

const hjKey = "tskey-auth-hj-secret"

var _ core.EnrollJoiner = (*hjJoiner)(nil)

// hjJoiner is a module that joins a new host to a network: the tailnet module's part of core.host.create.
type hjJoiner struct {
	defaultOff bool
	diff       []core.DiffLine
	state      string
	planErr    error
	prepareErr error

	mu       sync.Mutex
	plans    []core.JoinRequest
	prepares []core.JoinRequest
	hostSeen []bool // whether the host row existed when PrepareJoin ran
}

func (j *hjJoiner) Manifest() core.Manifest {
	return core.Manifest{ID: "tailnet", Version: 1, DefaultEnabled: !j.defaultOff}
}

func (j *hjJoiner) PlanJoin(_ context.Context, _ core.Env, req core.JoinRequest) ([]core.DiffLine, string, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.plans = append(j.plans, req)
	if j.planErr != nil {
		return nil, "", j.planErr
	}
	return j.diff, j.state, nil
}

func (j *hjJoiner) PrepareJoin(ctx context.Context, env core.Env, req core.JoinRequest) (string, error) {
	var n int
	if err := env.DB.Read().QueryRowContext(ctx, `SELECT count(*) FROM core_hosts WHERE id = ?`, req.HostID).Scan(&n); err != nil {
		return "", err
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.prepares = append(j.prepares, req)
	j.hostSeen = append(j.hostSeen, n > 0)
	if j.prepareErr != nil {
		return "", j.prepareErr
	}
	return hjKey, nil
}

func (j *hjJoiner) calls() (plans, prepares int) {
	j.mu.Lock()
	defer j.mu.Unlock()
	return len(j.plans), len(j.prepares)
}

func newHJJoiner() *hjJoiner {
	return &hjJoiner{diff: []core.DiffLine{{Op: "+", Text: "joiner: a one-use auth key"}}, state: "joiner-state-1"}
}

// hjEnv is the enrollment test env with the joiner as a compiled-in module, the controller on a tailnet and
// the joiner enabled on the local host.
func hjEnv(t *testing.T, j *hjJoiner, advertise string, addrs ...string) *enrollEnv {
	t.Helper()
	out := &enrollEnv{sessions: &fakeSessions{}}
	out.testEnv = newTestEnvWith(t, func(e *testEnv, d *Deps) {
		a, err := fleet.LoadOrCreateAuthority(context.Background(), e.db, coretest.Sealer{}, e.clock)
		if err != nil {
			t.Fatal(err)
		}
		out.authority = a
		d.Authority, d.AgentAdvertise, d.Sessions = a, advertise, out.sessions
	}, fakeModule{"demo", 1}, j)
	out.addHost("h1", "alpha", true)
	out.setModuleState("h1", "tailnet", true, true, "")
	hjSaveAddrs(t, out, addrs...)
	return out
}

func hjSaveAddrs(t *testing.T, e *enrollEnv, addrs ...string) {
	t.Helper()
	var list []netip.Addr
	for _, a := range addrs {
		list = append(list, netip.MustParseAddr(a))
	}
	if err := listen.SaveTailscaleAddrs(context.Background(), e.db, list); err != nil {
		t.Fatal(err)
	}
}

// hjPlanOutcome calls the Plan handler of core.host.create directly, to read the state hash a plan view hides.
func hjPlanOutcome(t *testing.T, e *enrollEnv, in core.HostCreateInput) core.PlanOutcome {
	t.Helper()
	def, ok := e.reg.Action(ref("core.host.create"))
	if !ok {
		t.Fatal("core.host.create is not registered")
	}
	env := core.Env{DB: e.db, Clock: e.clock, Principal: operator}
	out, err := def.Handler.Plan(context.Background(), env, encodeInput(t, in))
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func hjHosts(e *enrollEnv, name string) int {
	return e.count(`SELECT count(*) FROM core_hosts WHERE name = ?`, name)
}

func hjDetail(t *testing.T, err error) string {
	t.Helper()
	var coded *errcode.Error
	if !errors.As(err, &coded) || coded.Code != errcode.InvalidInput {
		t.Fatalf("err = %#v, want request.invalid_input", err)
	}
	return coded.Detail
}

func TestHJHostCreateWithATailnetJoin(t *testing.T) {
	j := newHJJoiner()
	e := hjEnv(t, j, "", "fd7a:115c:a1e0::1", "100.64.0.7", "100.64.0.3")
	in := core.HostCreateInput{Name: "web1", TailnetJoin: true, Tags: []string{"tag:web"}}

	v := e.run(operator, "core.host.create", in, nil)
	g := e.checkGrant(v.Output, "100.64.0.3:7443")

	token, bundle, err := transport.ParseBootstrap(g.Bundle)
	if err != nil || bundle == nil {
		t.Fatalf("the grant's bundle %q does not parse: %v", g.Bundle[:min(len(g.Bundle), 12)], err)
	}
	if token != bundle.Token || bundle.Token.String() != g.Token {
		t.Errorf("the bundle holds a different token than the grant: %+v", bundle.Token)
	}
	if bundle.TailnetKey != hjKey || bundle.ControllerTailnetAddr != "100.64.0.3:7443" {
		t.Errorf("bundle %+v, want the joiner's key and the first IPv4 tailnet address with the agent port", bundle)
	}

	// The engine plans again when it applies, to compare the state hash, so PlanJoin runs at least once.
	plans, prepares := j.calls()
	if plans < 1 || prepares != 1 {
		t.Fatalf("PlanJoin ran %d times and PrepareJoin %d times, want PlanJoin at least once and PrepareJoin once", plans, prepares)
	}
	req := j.prepares[0]
	if req.HostID != g.Host.ID || !reflect.DeepEqual(req.Tags, []string{"tag:web"}) || !req.Expires.Equal(t0.Add(fleet.TokenTTL)) {
		t.Errorf("PrepareJoin got %+v, want the new host's id, the tags and the token's expiry", req)
	}
	if j.hostSeen[0] {
		t.Error("the host row existed when PrepareJoin ran: the key is requested before the host is written")
	}
	if g.Host.State != core.HostPending {
		t.Errorf("host state %s, want pending", g.Host.State)
	}
	noSecret(t, []string{g.Token, g.Bundle, hjKey}, e.change(v.ID), e.auditEntries())
	if err := e.verify("core.host.create", in); err != nil {
		t.Errorf("Verify: %v", err)
	}
}

// TestHJHostCreateJoinUsesTheFirstIPv6WhenThereIsNoIPv4: the address is bracketed.
func TestHJHostCreateJoinUsesTheFirstIPv6WhenThereIsNoIPv4(t *testing.T) {
	e := hjEnv(t, newHJJoiner(), testAdvertise, "fd7a:115c:a1e0::9", "fd7a:115c:a1e0::1")
	v := e.run(operator, "core.host.create", core.HostCreateInput{Name: "web1", TailnetJoin: true}, nil)
	g := e.checkGrant(v.Output, "[fd7a:115c:a1e0::1]:7443")
	if _, bundle, err := transport.ParseBootstrap(g.Bundle); err != nil || bundle == nil || bundle.ControllerTailnetAddr != "[fd7a:115c:a1e0::1]:7443" {
		t.Errorf("bundle %+v, %v", bundle, err)
	}
}

func TestHJHostCreateJoinPlan(t *testing.T) {
	j := newHJJoiner()
	e := hjEnv(t, j, testAdvertise, "100.64.0.7")
	host := core.DiffLine{Op: "+", Text: "host web1: pending, with an enrollment token for 100.64.0.7:7443, shown once and valid for one hour"}
	compensated := core.DiffLine{Op: " ", Text: "compensated by core.host.delete"}

	pv, err := e.plan(operator, "core.host.create", core.HostCreateInput{Name: "web1", TailnetJoin: true, Tags: []string{"tag:web", "tag:ci"}})
	if err != nil {
		t.Fatal(err)
	}
	want := []core.DiffLine{
		host, {Op: "+", Text: "joiner: a one-use auth key"},
		{Op: "+", Text: "bootstrap bundle: the token plus a one-use tailnet auth key for tag:web, tag:ci, shown once"},
		compensated,
	}
	if !reflect.DeepEqual(pv.Diff, want) {
		t.Errorf("diff\n%+v\nwant\n%+v", pv.Diff, want)
	}
	if len(pv.Guards) != 1 || pv.Guards[0].ID != "name-free" || !pv.Guards[0].OK {
		t.Errorf("guards %+v", pv.Guards)
	}
	if len(j.plans) != 1 || j.plans[0].HostID != "" || !reflect.DeepEqual(j.plans[0].Tags, []string{"tag:web", "tag:ci"}) || !j.plans[0].Expires.Equal(t0.Add(fleet.TokenTTL)) {
		t.Errorf("PlanJoin got %+v: the host id is chosen at apply", j.plans)
	}
	if _, prepares := j.calls(); prepares != 0 {
		t.Error("planning created a key")
	}

	pv, err = e.plan(operator, "core.host.create", core.HostCreateInput{Name: "web1", TailnetJoin: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := pv.Diff[len(pv.Diff)-2].Text; got != "bootstrap bundle: the token plus a one-use tailnet auth key for no tags, shown once" {
		t.Errorf("bundle line %q", got)
	}
}

// TestHJHostCreateJoinPlanStateHash: the hash follows everything the apply depends on and leaves a plain create alone.
func TestHJHostCreateJoinPlanStateHash(t *testing.T) {
	j := newHJJoiner()
	e := hjEnv(t, j, testAdvertise, "100.64.0.7")
	join := core.HostCreateInput{Name: "web1", TailnetJoin: true, Tags: []string{"tag:web"}}

	base := hjPlanOutcome(t, e, join).StateHash
	if again := hjPlanOutcome(t, e, join).StateHash; again != base {
		t.Error("the same plan hashed differently")
	}
	j.state = "joiner-state-2"
	if hjPlanOutcome(t, e, join).StateHash == base {
		t.Error("the joiner's state is not in the hash")
	}
	j.state = "joiner-state-1"
	hjSaveAddrs(t, e, "100.64.0.8")
	if hjPlanOutcome(t, e, join).StateHash == base {
		t.Error("the tailnet address is not in the hash")
	}
	hjSaveAddrs(t, e, "100.64.0.7")
	other := join
	other.Tags = []string{"tag:ci"}
	if hjPlanOutcome(t, e, other).StateHash == base {
		t.Error("the tags are not in the hash")
	}
	plain := hjPlanOutcome(t, e, core.HostCreateInput{Name: "web1"}).StateHash
	if plain != stateHash("web1", "false") {
		t.Error("a create without a join must hash as it did before the join existed")
	}
}

// TestHJPlainHostCreateIgnoresTheJoiner: without tailnetJoin the answer, the address and the calls are what they were.
func TestHJPlainHostCreateIgnoresTheJoiner(t *testing.T) {
	j := newHJJoiner()
	e := hjEnv(t, j, testAdvertise, "100.64.0.7")
	v := e.run(operator, "core.host.create", core.HostCreateInput{Name: "web1"}, nil)
	g := e.checkGrant(v.Output, testAdvertise)
	if g.Bundle != "" {
		t.Error("a create without tailnetJoin returned a bundle")
	}
	if plans, prepares := j.calls(); plans != 0 || prepares != 0 {
		t.Errorf("the joiner ran %d plans and %d prepares", plans, prepares)
	}
}

func TestHJHostCreateJoinRefusals(t *testing.T) {
	e := hjEnv(t, newHJJoiner(), testAdvertise, "100.64.0.7")
	for name, c := range map[string]struct {
		in    core.HostCreateInput
		field string
	}{
		"tags without tailnetJoin":           {core.HostCreateInput{Name: "web1", Tags: []string{"tag:web"}}, "/tags"},
		"controllerAddress with tailnetJoin": {core.HostCreateInput{Name: "web1", TailnetJoin: true, ControllerAddress: "web.example:7443"}, "/controllerAddress"},
	} {
		_, err := e.plan(operator, "core.host.create", c.in)
		wantField(t, err, c.field)
		wantField(t, e.applyDirect("core.host.create", c.in), c.field)
		if hjHosts(e, "web1") != 0 || e.count(`SELECT count(*) FROM core_enroll_tokens`) != 0 {
			t.Errorf("%s: a refused create wrote a host or a token", name)
		}
	}
}

func TestHJHostCreateJoinNeedsTheJoinerModule(t *testing.T) {
	join := core.HostCreateInput{Name: "web1", TailnetJoin: true}

	// No compiled-in module implements core.EnrollJoiner.
	bare := newEnrollEnv(t, testAdvertise)
	hjSaveAddrs(t, bare, "100.64.0.7")
	_, err := bare.plan(operator, "core.host.create", join)
	if d := hjDetail(t, err); d != "tailnet join needs the tailnet module" {
		t.Errorf("without a joiner: %q", d)
	}
	if d := hjDetail(t, bare.applyDirect("core.host.create", join)); d != "tailnet join needs the tailnet module" {
		t.Errorf("apply without a joiner: %q", d)
	}

	// The module is disabled on the controller's own host.
	e := hjEnv(t, newHJJoiner(), testAdvertise, "100.64.0.7")
	e.setModuleState("h1", "tailnet", false, true, "")
	_, err = e.plan(operator, "core.host.create", join)
	if d := hjDetail(t, err); d != "tailnet join needs the tailnet module" {
		t.Errorf("with the joiner disabled: %q", d)
	}
	e.setModuleState("h1", "tailnet", true, true, "")
	if _, err := e.plan(operator, "core.host.create", join); err != nil {
		t.Errorf("with the joiner enabled: %v", err)
	}

	// No row yet: the module's default decides.
	for defaultOff, wantOK := range map[bool]bool{false: true, true: false} {
		j := newHJJoiner()
		j.defaultOff = defaultOff
		e := hjEnv(t, j, testAdvertise, "100.64.0.7")
		e.exec(`DELETE FROM core_module_state WHERE module_id = 'tailnet'`)
		_, err := e.plan(operator, "core.host.create", join)
		if (err == nil) != wantOK {
			t.Errorf("no module state row, default off %v: err %v", defaultOff, err)
		}
	}
}

func TestHJHostCreateJoinNeedsATailnetAddress(t *testing.T) {
	e := hjEnv(t, newHJJoiner(), testAdvertise)
	join := core.HostCreateInput{Name: "web1", TailnetJoin: true}
	_, err := e.plan(operator, "core.host.create", join)
	if d := hjDetail(t, err); d != "the controller is not on a tailnet" {
		t.Errorf("plan: %q", d)
	}
	if d := hjDetail(t, e.applyDirect("core.host.create", join)); d != "the controller is not on a tailnet" {
		t.Errorf("apply: %q", d)
	}
}

func TestHJHostCreateJoinerFailures(t *testing.T) {
	join := core.HostCreateInput{Name: "web1", TailnetJoin: true, Tags: []string{"tag:web"}}

	j := newHJJoiner()
	j.planErr = errcode.New(errcode.CloudNotConnected, "no tailnet is connected")
	e := hjEnv(t, j, testAdvertise, "100.64.0.7")
	_, err := e.plan(operator, "core.host.create", join)
	wantCode(t, err, errcode.CloudNotConnected)

	j = newHJJoiner()
	j.prepareErr = errcode.New(errcode.CloudUncertainOutcome, "the key may exist")
	e = hjEnv(t, j, testAdvertise, "100.64.0.7")
	wantCode(t, e.applyDirect("core.host.create", join), errcode.CloudUncertainOutcome)
	if hjHosts(e, "web1") != 0 || e.count(`SELECT count(*) FROM core_enroll_tokens`) != 0 {
		t.Error("a failed PrepareJoin left a host or a token")
	}
}

// TestHJHostCreateJoinTakenName: a name taken since the plan is refused before any key is requested.
func TestHJHostCreateJoinTakenName(t *testing.T) {
	j := newHJJoiner()
	e := hjEnv(t, j, testAdvertise, "100.64.0.7")
	wantCode(t, e.applyDirect("core.host.create", core.HostCreateInput{Name: "alpha", TailnetJoin: true}), errcode.PlanStateChanged)
	if _, prepares := j.calls(); prepares != 0 {
		t.Errorf("PrepareJoin ran %d times for a name that is taken", prepares)
	}
}

// TestHJControllerTailnetAddr covers the address rule directly.
func TestHJControllerTailnetAddr(t *testing.T) {
	e := hjEnv(t, newHJJoiner(), "")
	ctx := context.Background()
	for name, c := range map[string]struct {
		saved []string
		want  string
	}{
		"an IPv4 address":       {[]string{"100.64.0.7"}, "100.64.0.7:7443"},
		"the first of two IPv4": {[]string{"100.100.1.1", "100.64.0.7"}, "100.64.0.7:7443"},
		"IPv4 before IPv6":      {[]string{"fd7a:115c:a1e0::1", "100.64.0.7"}, "100.64.0.7:7443"},
		"an IPv6 address only":  {[]string{"fd7a:115c:a1e0::1"}, "[fd7a:115c:a1e0::1]:7443"},
	} {
		hjSaveAddrs(t, e, c.saved...)
		got, err := controllerTailnetAddr(ctx, e.db)
		if err != nil || got != c.want {
			t.Errorf("%s: %q, %v; want %q", name, got, err, c.want)
		}
	}
	hjSaveAddrs(t, e)
	if _, err := controllerTailnetAddr(ctx, e.db); hjDetail(t, err) != "the controller is not on a tailnet" {
		t.Errorf("with none saved: %v", err)
	}
	e.exec(`DELETE FROM core_meta WHERE key = 'tailscale_addrs'`)
	if _, err := controllerTailnetAddr(ctx, e.db); hjDetail(t, err) != "the controller is not on a tailnet" {
		t.Errorf("with no row: %v", err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mkdir -p /root/.cache/w4-tmp/9 && export TMPDIR=/root/.cache/w4-tmp/9 GOTOOLCHAIN=local && timeout 300 go test -count=1 -run 'TestHJ' ./internal/controller/coreactions/ 2>&1 | head -20`
Expected: FAIL to build with `undefined: controllerTailnetAddr`. Quote it in the report. (The test file compiles against Task 7's `core.HostCreateInput.TailnetJoin`, `core.EnrollJoiner` and `core.JoinRequest` and Task 2's `transport.ParseBootstrap`, which the base provides.)

- [ ] **Step 3: Implement**

1. `hostjoin.go`, `package coreactions`:
   - `controllerTailnetAddr(ctx, db)`: call `listen.LoadTailscaleAddrs(ctx, db)` (sorted, IPv4 before IPv6, only Tailscale addresses). Return the first IPv4 address, else the first IPv6 address, as `netip.AddrPortFrom(addr, listen.AgentPort).String()` (which brackets an IPv6 address: `[fd7a:115c:a1e0::1]:7443`). No address, whether no row or an empty list, is the `/tailnetJoin` error above. A read error is returned as it is.
   - `func (m module) joiner(ctx, q querier) (core.EnrollJoiner, error)`: walk `m.d.Modules` in order and return the first module that implements `core.EnrollJoiner` and is enabled on the controller. "Enabled on the controller" means the `enabled` column of the `core_module_state` row for the module's id on the local host (`core_hosts.is_local = 1`); when the local host or the row does not exist yet, the module manifest's `DefaultEnabled` decides (the dispatcher inserts rows with that default at a host's first hello). If none qualifies, return the `/tailnetJoin` error `tailnet join needs the tailnet module`.
   - A small constructor for the single-field `request.invalid_input` errors above, and a `joinTags` helper that renders the tags joined with `, ` in the order given, or `no tags` for none.
2. `hostenroll.go`, a helper `func (h hostCreate) joinTarget(ctx, env, in) (joiner core.EnrollJoiner, addr string, err error)` used by both `Plan` and `Apply` when `in.TailnetJoin` or `len(in.Tags) > 0`. In this order it: refuses tags without `tailnetJoin`; refuses a non-empty `ControllerAddress` with `tailnetJoin`; finds the joiner with `h.m.joiner(ctx, env.DB.Read())`; reads the tailnet address with `controllerTailnetAddr(ctx, env.DB)`. A join never calls `h.m.address`, so a controller with no `--agent-advertise` can still create a join host.
3. `hostCreate.Plan`:
   - Without a join the method behaves exactly as before: same diff, same guards and `stateHash(in.Name, strconv.FormatBool(taken))`.
   - With a join: `addr` is the tailnet address. The diff is, in order: the existing host line with that address; the lines `PlanJoin` returned (called with `core.JoinRequest{Tags: in.Tags, Expires: env.Clock.Now().Add(fleet.TokenTTL)}`, `HostID` empty); the line `bootstrap bundle: the token plus a one-use tailnet auth key for <joinTags(in.Tags)>, shown once` with Op `+`; the existing ` compensated by core.host.delete` line. A `PlanJoin` error is returned as it is. The state hash is `stateHash(in.Name, strconv.FormatBool(taken), addr, strings.Join(in.Tags, ","), joinerState)` where `joinerState` is the string `PlanJoin` returned.
4. `hostCreate.Apply`:
   - Without a join, unchanged.
   - With a join: run `joinTarget` again; read whether the name is taken and, if so, return the same `plan.state_changed` error the write transaction returns, before any key is requested; choose the host id (ULID from `env.Clock.Now()`, as today); call `PrepareJoin` with `core.JoinRequest{HostID: id, Tags: in.Tags, Expires: now.Add(fleet.TokenTTL)}` before the host write, returning its error as it is and writing nothing; then run the existing write transaction (name recheck, host insert, `IssueToken`) with `addr` as the token's address.
   - After the grant is built, set `Bundle` to `transport.Bundle{Token: <the parsed token>, TailnetKey: <PrepareJoin's key>, ControllerTailnetAddr: addr}.String()`; `Token` keeps the plain token string and `ExpiresAt` the token's expiry. The auth key is never logged, put in an error or stored: only `EnrollmentGrant.Bundle`, a `secret:"true"` field, carries it.
   - `Verify` is unchanged.
5. Do not edit `core.host.token`, `core.host.enroll-local` or any file outside this task's Files. This task adds no action or input type, so no schema snapshot changes.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `export TMPDIR=/root/.cache/w4-tmp/9 GOTOOLCHAIN=local && timeout 300 go test -count=1 -run 'TestHJ' -v ./internal/controller/coreactions/ 2>&1 | tail -30`
Expected: PASS for every `TestHJ` test.

- [ ] **Step 5: Run the task's checks**

Run each with `export TMPDIR=/root/.cache/w4-tmp/9 GOTOOLCHAIN=local` and an explicit timeout, and record the real output:

```bash
timeout 300 go build ./...
timeout 300 go vet ./...
timeout 900 go test -count=1 ./...
timeout 600 go test -race -count=1 ./internal/controller/coreactions/
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
rm -rf /root/.cache/w4-tmp/9 && go clean -testcache
```

Expected: `go build` and `go vet` print nothing; every `go test` line is `ok` or `?` (the existing `TestHostCreate*`, `TestCoreActionSecrets` and snapshot tests still pass); the lint prints `0 issues.`; `go mod tidy -diff` prints nothing.

- [ ] **Step 6: Commit**

```bash
git add internal/controller/coreactions/hostjoin.go internal/controller/coreactions/hostenroll.go internal/controller/coreactions/hostjoin_test.go
git commit -m "feat(coreactions): join tailnet at host create"
```

### Task 10: tailnet: enroll key creation

**Files:**
- Create: `internal/modules/tailnet/join_enroll.go`
- Test: `internal/modules/tailnet/join_enroll_test.go`
- Unchanged: `tailnet.go` (its `module` already holds `t tn`; the new methods go on `module`), `authkey.go`, `store.go`, `keysweep.go`

Read, do not edit: `internal/modules/tailnet/authkey.go` (`normalizeAuthKey`, `authkeyHandler.Apply`, the shape to follow), `store.go` (`insertKey`, `settleKey`, `loadKey`), `account.go` (`connected`, `accountHash`), `client.go` (`api.CreateKey`, `KeyRequest`), `keysweep.go` (`keyNamespace`), `fake/keys.go`, and the test helpers `newTNHost`, `acConnect`, `tnWith` in the package's existing `_test.go` files.

**Interfaces:**
- Consumes: Contracts "Core host join" (`core.EnrollJoiner`, `core.JoinRequest`, written by Task 7); the package's `tn.connected`, `normalizeAuthKey`, `AuthKeyInput`, `KeyRequest`, `api.CreateKey`, `insertKey`, `settleKey`, `keyRow`, `accountHash`, `authkeyExpiry`; `errcode.CloudUncertainOutcome`, `errcode.CloudNotConnected`, `errcode.InvalidInput`; the fake provider `internal/modules/tailnet/fake`.
- Produces: Contracts "Tailnet enroll keys" (the part of Task 10): `module` implements `core.EnrollJoiner` through `func (m module) PlanJoin(ctx context.Context, env core.Env, req core.JoinRequest) ([]core.DiffLine, string, error)` and `func (m module) PrepareJoin(ctx context.Context, env core.Env, req core.JoinRequest) (string, error)`, which call `func (t tn) planJoin(...)` and `func (t tn) prepareJoin(...)` with the same parameters and results. The `tailnet_keys` row an enroll key leaves: description `darkpanel-enroll-<host id>`, kind `enroll`, owner the host id, `expiry_seconds` 3600, tags sorted, state `live` with the provider's key id (Task 18's `settleEnrollKeys` takes it from there). Private constants `enrollKeyPrefix = "darkpanel-enroll-"` and `enrollKeySeconds = 3600`.

**Items:** 24

**Depends on:** Task 7

- [ ] **Step 1: Write the failing tests**

Create `internal/modules/tailnet/join_enroll_test.go`:

```go
package tailnet

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/cloud/cloudfake"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/tailnet/fake"
)

const (
	jeHost   = "01J9X4K8Q2M5N7P0R3S6T8V1H2"
	jeChange = "01J9X4K8Q2M5N7P0R3S6T8V1W9"
)

var jeKeysPath = "/api/v2/tailnet/" + fake.Tailnet + "/keys"

var _ core.EnrollJoiner = module{}

// jeEnv is a connected account whose OAuth client may assign tag:ci and tag:web, and the module over the fake.
type jeEnv struct {
	h   *tnHost
	srv *fake.Server
	m   module
}

func jeSetup(t *testing.T) jeEnv {
	t.Helper()
	h := newTNHost(t)
	srv := fake.New(t)
	acct := acConnect(t, h, srv)
	err := h.db.Write(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(context.Background(), `UPDATE tailnet_accounts SET tags_allowed_json = ? WHERE id = ?`, `["tag:ci","tag:web"]`, acct.ID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return jeEnv{h: h, srv: srv, m: module{t: tnWith(srv)}}
}

func (e jeEnv) request(tags ...string) core.JoinRequest {
	return core.JoinRequest{HostID: jeHost, Tags: tags, Expires: e.h.clock.Now().Add(time.Hour)}
}

func (e jeEnv) prepare(tags ...string) (string, error) {
	return e.m.PrepareJoin(context.Background(), e.h.env(jeChange), e.request(tags...))
}

func jeRow(t *testing.T, e jeEnv) keyRow {
	t.Helper()
	r, ok, err := loadKey(context.Background(), e.h.db.Read(), "darkpanel-enroll-"+jeHost)
	if err != nil || !ok {
		t.Fatalf("tailnet_keys row for the enroll key: ok %v, err %v", ok, err)
	}
	return r
}

func jeRows(t *testing.T, e jeEnv) int {
	t.Helper()
	rows, err := listKeys(context.Background(), e.h.db.Read())
	if err != nil {
		t.Fatal(err)
	}
	return len(rows)
}

func jeCreates(e jeEnv) int {
	n := 0
	for _, r := range e.srv.Cloud().Requests() {
		if r.Method == http.MethodPost && r.Path == jeKeysPath {
			n++
		}
	}
	return n
}

func jeCode(t *testing.T, err error) *errcode.Error {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error")
	}
	return errcode.From(err)
}

func jeHasField(e *errcode.Error, path string) bool {
	return slices.ContainsFunc(e.Fields, func(f errcode.FieldError) bool { return f.Path == path })
}

func TestJEModuleIsAnEnrollJoiner(t *testing.T) {
	if _, ok := Module().(core.EnrollJoiner); !ok {
		t.Fatal("the tailnet module does not implement core.EnrollJoiner: core.host.create finds it by a type assertion")
	}
}

func TestJEPlanJoin(t *testing.T) {
	e := jeSetup(t)
	ctx := context.Background()

	diff, state, err := e.m.PlanJoin(ctx, e.h.env(""), core.JoinRequest{Tags: []string{"tag:web", "tag:ci", "tag:ci"}})
	if err != nil {
		t.Fatal(err)
	}
	if state == "" {
		t.Error("the plan state is empty")
	}
	if len(diff) == 0 {
		t.Fatal("no diff lines")
	}
	var text strings.Builder
	for _, l := range diff {
		if l.Op != "+" {
			t.Errorf("line %+v: a join only adds", l)
		}
		text.WriteString(l.Text + "\n")
	}
	for _, want := range []string{"tag:ci, tag:web", "one use", "preauthorized", "not ephemeral", "1h0m0s", "darkpanel-enroll-<host id>", "tailnet_keys"} {
		if !strings.Contains(text.String(), want) {
			t.Errorf("the diff lacks %q:\n%s", want, text.String())
		}
	}

	diff, _, err = e.m.PlanJoin(ctx, e.h.env(""), e.request("tag:ci"))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(diff, func(l core.DiffLine) bool { return strings.Contains(l.Text, "darkpanel-enroll-"+jeHost) }) {
		t.Errorf("with a host id the diff names the description: %v", diff)
	}

	if n := jeRows(t, e); n != 0 {
		t.Errorf("planning wrote %d key rows", n)
	}
	if n := jeCreates(e); n != 0 {
		t.Errorf("planning sent %d create requests", n)
	}
}

func TestJEPlanJoinState(t *testing.T) {
	e := jeSetup(t)
	state := func(tags ...string) string {
		_, s, err := e.m.PlanJoin(context.Background(), e.h.env(""), core.JoinRequest{Tags: tags})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	if a, b := state("tag:ci", "tag:web"), state("tag:web", "tag:ci", "tag:ci"); a != b {
		t.Errorf("the same tags in another order and with a repeat give different states: %s, %s", a, b)
	}
	if a, b := state("tag:ci"), state("tag:web"); a == b {
		t.Error("different tags give one state")
	}
}

func TestJEPlanJoinRefusals(t *testing.T) {
	e := jeSetup(t)
	ctx := context.Background()
	for name, tags := range map[string][]string{
		"no tags":                   nil,
		"a tag outside tagsAllowed": {"tag:db"},
		"a malformed tag":           {"ci"},
		"one good and one bad tag":  {"tag:ci", "tag:db"},
	} {
		_, _, err := e.m.PlanJoin(ctx, e.h.env(""), core.JoinRequest{Tags: tags})
		if c := jeCode(t, err); c.Code != errcode.InvalidInput || !jeHasField(c, "/tags") {
			t.Errorf("%s: %v, want request.invalid_input naming /tags", name, err)
		}
	}

	bare := jeEnv{h: newTNHost(t), srv: e.srv, m: e.m}
	_, _, err := bare.m.PlanJoin(ctx, bare.h.env(""), core.JoinRequest{Tags: []string{"tag:ci"}})
	if c := jeCode(t, err); c.Code != errcode.CloudNotConnected {
		t.Errorf("without an account: %v, want %s", err, errcode.CloudNotConnected)
	}
}

func TestJEPrepareJoinCreatesTheKey(t *testing.T) {
	e := jeSetup(t)
	key, err := e.prepare("tag:web", "tag:ci")
	if err != nil {
		t.Fatal(err)
	}
	keys := e.srv.Keys()
	if len(keys) != 1 {
		t.Fatalf("the provider holds %d keys, want 1", len(keys))
	}
	k := keys[0]
	if key == "" || key != k.Key {
		t.Errorf("returned key %q, provider key %q: PrepareJoin returns the secret the provider answered", key, k.Key)
	}
	desc := "darkpanel-enroll-" + jeHost
	if k.Description != desc || !keyNamespace.MatchString(desc) {
		t.Errorf("description %q; it must be darkpanel-enroll-<host id> and inside the sweep's namespace", k.Description)
	}
	if k.Reusable || k.Ephemeral || !k.Preauthorized || !reflect.DeepEqual(k.Tags, []string{"tag:ci", "tag:web"}) {
		t.Errorf("key %+v: want one use, preauthorized, not ephemeral, tags sorted", k)
	}
	if k.Expires.Sub(k.Created) != time.Hour {
		t.Errorf("the key lives %v, want 1h", k.Expires.Sub(k.Created))
	}

	var body struct {
		Description   string `json:"description"`
		ExpirySeconds int    `json:"expirySeconds"`
		Capabilities  struct {
			Devices struct {
				Create struct {
					Reusable, Ephemeral, Preauthorized bool
					Tags                               []string
				} `json:"create"`
			} `json:"devices"`
		} `json:"capabilities"`
	}
	for _, r := range e.srv.Cloud().Requests() {
		if r.Method == http.MethodPost && r.Path == jeKeysPath {
			if err := json.Unmarshal(r.Body, &body); err != nil {
				t.Fatal(err)
			}
		}
	}
	c := body.Capabilities.Devices.Create
	if body.Description != desc || body.ExpirySeconds != 3600 || c.Reusable || c.Ephemeral || !c.Preauthorized || !reflect.DeepEqual(c.Tags, []string{"tag:ci", "tag:web"}) {
		t.Errorf("create request %+v", body)
	}

	row := jeRow(t, e)
	if row.Kind != "enroll" || row.OwnerID != jeHost || row.State != "live" || row.KeyID != k.ID || row.ExpirySeconds != 3600 ||
		!reflect.DeepEqual(row.Tags, []string{"tag:ci", "tag:web"}) || !row.RequestedAt.Equal(e.h.clock.Now()) {
		t.Errorf("row %+v", row)
	}
	if eff := e.h.journal.effects(); len(eff) != 0 {
		t.Errorf("the journal holds %d effects: the sweep, not the change, cleans up an enroll key", len(eff))
	}
	var stored string
	if err := e.h.db.Read().QueryRow(`SELECT group_concat(description || kind || owner_id || key_id || state || tags_json) FROM tailnet_keys`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored, key) {
		t.Error("tailnet_keys holds the key secret")
	}
}

// jeTap observes each request the module sends, before it reaches the fake.
type jeTap struct {
	inner http.RoundTripper
	on    func(*http.Request)
}

func (t jeTap) RoundTrip(r *http.Request) (*http.Response, error) {
	t.on(r)
	return t.inner.RoundTrip(r)
}

// TestJEPrepareJoinRecordsTheRowFirst: when the create request leaves, its row already exists as requested,
// so a key committed whatever happens next is always sweepable.
func TestJEPrepareJoinRecordsTheRowFirst(t *testing.T) {
	e := jeSetup(t)
	seen := ""
	client := e.srv.Client()
	client.Transport = jeTap{inner: client.Transport, on: func(r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == jeKeysPath {
			row, ok, err := loadKey(context.Background(), e.h.db.Read(), "darkpanel-enroll-"+jeHost)
			switch {
			case err != nil:
				seen = "error: " + err.Error()
			case !ok:
				seen = "no row"
			default:
				seen = row.State + "/" + row.Kind + "/" + row.OwnerID
			}
		}
	}}
	e.m.t.d.http = client
	if _, err := e.prepare("tag:ci"); err != nil {
		t.Fatal(err)
	}
	if want := "requested/enroll/" + jeHost; seen != want {
		t.Errorf("at the create request the row was %q, want %q", seen, want)
	}
}

func TestJEPrepareJoinFailures(t *testing.T) {
	t.Run("a definite refusal settles the row as deleted", func(t *testing.T) {
		e := jeSetup(t)
		e.srv.SetScopes("devices:core")
		_, err := e.prepare("tag:ci")
		if c := jeCode(t, err); c.Code != errcode.CloudPermissionMissing || !core.IsDefinite(err) {
			t.Fatalf("error %v (definite %v), want a definite %s", err, core.IsDefinite(err), errcode.CloudPermissionMissing)
		}
		if r := jeRow(t, e); r.State != "deleted" {
			t.Errorf("row %+v, want deleted: nothing was created", r)
		}
		if n := len(e.srv.Keys()); n != 0 {
			t.Errorf("provider holds %d keys", n)
		}
	})
	for name, fault := range map[string]cloudfake.Fault{
		"a server error":           {Status: http.StatusInternalServerError},
		"a lost answer":            {LoseResponse: true},
		"an answer with no id":     {Status: http.StatusOK, Body: `{"key":"tskey-auth-noid","expires":"2026-11-01T00:00:00Z","description":"darkpanel-enroll-` + jeHost + `"}`},
		"an answer with no secret": {Status: http.StatusOK, Body: `{"id":"k-nosecret","key":"","expires":"2026-11-01T00:00:00Z","description":"darkpanel-enroll-` + jeHost + `"}`},
	} {
		t.Run(name+" leaves the row uncertain for the sweep", func(t *testing.T) {
			e := jeSetup(t)
			e.srv.Cloud().Fail(http.MethodPost, jeKeysPath, fault)
			key, err := e.prepare("tag:ci")
			if c := jeCode(t, err); c.Code != errcode.CloudUncertainOutcome || core.IsDefinite(err) {
				t.Fatalf("error %v (definite %v), want an uncertain outcome", err, core.IsDefinite(err))
			}
			if key != "" {
				t.Error("a key was returned with the failure")
			}
			if r := jeRow(t, e); r.State != "uncertain" || r.KeyID != "" || r.Kind != "enroll" || r.OwnerID != jeHost {
				t.Errorf("row %+v, want an uncertain enroll row with no key id", r)
			}
		})
	}
}

// TestJEPrepareJoinRefusesBeforeAnyWrite: a request the account cannot serve records no row and sends nothing.
func TestJEPrepareJoinRefusesBeforeAnyWrite(t *testing.T) {
	e := jeSetup(t)
	ctx := context.Background()
	for name, run := range map[string]func() error{
		"a tag outside tagsAllowed": func() error { _, err := e.prepare("tag:db"); return err },
		"no tags":                   func() error { _, err := e.prepare(); return err },
		"a host id that is not one": func() error {
			_, err := e.m.PrepareJoin(ctx, e.h.env(jeChange), core.JoinRequest{HostID: "host-1", Tags: []string{"tag:ci"}})
			return err
		},
		"no host id": func() error {
			_, err := e.m.PrepareJoin(ctx, e.h.env(jeChange), core.JoinRequest{Tags: []string{"tag:ci"}})
			return err
		},
	} {
		if err := run(); err == nil || !core.IsDefinite(err) {
			t.Errorf("%s: %v, want a definite refusal", name, err)
		}
	}
	bare := jeEnv{h: newTNHost(t), srv: e.srv, m: e.m}
	_, err := bare.prepare("tag:ci")
	if c := jeCode(t, err); c.Code != errcode.CloudNotConnected || !core.IsDefinite(err) {
		t.Errorf("without an account: %v, want a definite %s", err, errcode.CloudNotConnected)
	}
	if n := jeRows(t, e) + jeRows(t, bare); n != 0 {
		t.Errorf("refusals wrote %d key rows", n)
	}
	if n := jeCreates(e); n != 0 {
		t.Errorf("refusals sent %d create requests", n)
	}
}

// TestJEPrepareJoinTwiceForOneHost: a host gets one enroll key; the second call fails on its row before it
// reaches the provider.
func TestJEPrepareJoinTwiceForOneHost(t *testing.T) {
	e := jeSetup(t)
	if _, err := e.prepare("tag:ci"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.prepare("tag:ci"); err == nil {
		t.Fatal("a second key was created for one host")
	}
	if n := jeCreates(e); n != 1 {
		t.Errorf("%d create requests, want 1", n)
	}
	if n := len(e.srv.Keys()); n != 1 {
		t.Errorf("provider holds %d keys, want 1", n)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mkdir -p /root/.cache/w4-tmp/10 && export TMPDIR=/root/.cache/w4-tmp/10 GOTOOLCHAIN=local && timeout 300 go test -count=1 -run 'TestJE' ./internal/modules/tailnet/ 2>&1 | head -20`
Expected: FAIL to build with `cannot use module{} (value of struct type module) as core.EnrollJoiner value in variable declaration: module does not implement core.EnrollJoiner (missing method PlanJoin)`. Quote it in the report.

- [ ] **Step 3: Implement**

`join_enroll.go`, `package tailnet`:

1. The two `module` methods delegate to `m.t.planJoin` and `m.t.prepareJoin`.
2. The key's request: tags from the request (validated and sorted by `normalizeAuthKey`, called with `AuthKeyInput{Tags: req.Tags, Preauthorized: true, ExpirySeconds: enrollKeySeconds}` and the account's `TagsAllowed`), one use (`Reusable` false), `Ephemeral` false, `Preauthorized` true, expiry `enrollKeySeconds` (3600). `req.Expires` is not read: the key's lifetime is the fixed 3600 s, which is also the enrollment token's life.
3. `planJoin` writes nothing and calls the provider not at all. It loads the account with `t.connected(ctx, env.DB, env.Sealer, env.Clock)` (no account is `cloud.not_connected`, returned as it is), normalises the tags (an empty list, a malformed tag or one outside `tagsAllowed` is the `request.invalid_input` error with a `/tags` field that `normalizeAuthKey` returns), and returns:
   - three diff lines, all Op `+`: `tailnet auth key with tags <tags joined by ", ">; one use, preauthorized, not ephemeral; expires after 1h0m0s` (the duration from `authkeyExpiry(enrollKeySeconds)`), `description darkpanel-enroll-<host id>` (the literal `<host id>` when `req.HostID` is empty), and `the key is recorded in tailnet_keys before it is requested`;
   - the state string `accountHash(account id, tags joined by ",")`, so the same tags in any order or with repeats give one state and different tags or another account give another.
4. `prepareJoin`, in this order:
   1. `req.HostID` must match `^[0-9A-HJKMNP-TV-Z]{26}$` (a ULID, the namespace the key sweep recognises); otherwise return `core.Definite` of a `tailnet:` error and write nothing.
   2. Load the account and normalise the tags as in `planJoin`; either failure is returned wrapped in `core.Definite`, with nothing written and no request sent.
   3. In its own write transaction, `insertKey` the row `{Description: "darkpanel-enroll-" + HostID, Kind: "enroll", OwnerID: HostID, State: "requested", RequestedAt and UpdatedAt: the env clock's now in UTC, ExpirySeconds: 3600, Tags}`. The primary key makes a second call for one host fail here, before the provider is reached.
   4. Call `CreateKey` with `KeyRequest{Description, Tags, Reusable: false, Ephemeral: false, Preauthorized: true, ExpirySeconds: 3600}`. An answer with an empty id or an empty key is treated as `errcode.CloudUncertainOutcome` (the key may exist).
   5. On a failure, settle the row with `settleKey`, using a context that survives the caller's cancellation (`context.WithoutCancel`): `deleted` when the error is `core.IsDefinite` (the provider refused, so no key exists), `uncertain` otherwise (the key may exist; the five-minute sweep deletes it by description, as spec 1.9 requires). Return the create error (joined with any error from settling the row). No key is returned with a failure.
   6. On success, settle the row `live` with the provider's key id (`settleKey`, same context) and return the provider's key secret. If settling fails, return that error; the sweep resolves the leftover row.
   7. No effect is written to the change's journal (`env.Effects` is not used): an enroll key is cleaned up by the sweep, not by undoing a change. The secret appears in no row, log line, error or journal entry.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `export TMPDIR=/root/.cache/w4-tmp/10 GOTOOLCHAIN=local && timeout 300 go test -count=1 -run 'TestJE' -v ./internal/modules/tailnet/ 2>&1 | tail -30`
Expected: PASS for every `TestJE` test.

- [ ] **Step 5: Run the task's checks**

Run each with `export TMPDIR=/root/.cache/w4-tmp/10 GOTOOLCHAIN=local` and an explicit timeout, and record the real output:

```bash
timeout 300 go build ./...
timeout 300 go vet ./...
timeout 900 go test -count=1 ./...
timeout 600 go test -race -count=1 ./internal/modules/tailnet/
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
rm -rf /root/.cache/w4-tmp/10 && go clean -testcache
```

Expected: `go build` and `go vet` print nothing; every `go test` line is `ok` or `?`; the lint prints `0 issues.`; `go mod tidy -diff` prints nothing. The task adds no action, resource or input type, so no schema snapshot changes.

- [ ] **Step 6: Commit**

```bash
git add internal/modules/tailnet/join_enroll.go internal/modules/tailnet/join_enroll_test.go
git commit -m "feat(tailnet): create enroll keys for host join"
```

### Task 11: web: tailnet join in the Add host dialog

**Files:**
- Modify: `web/src/modules/tailnet/host-create.tsx` (today a stub whose default export renders `null`)
- Create: `web/src/modules/tailnet/host-create.test.tsx`
- Modify: `web/src/pages/create-host-dialog.tsx`
- Modify: `web/src/pages/create-host-dialog.test.tsx`

Read-only context: `web/src/modules/registry.ts` and `web/src/modules/types.ts` (frozen; the registry already lazy-loads `./tailnet/host-create` as the `tailnet.join` host-create extension, whose component receives `{ value, onChange }`), `web/src/modules/tailnet/account-panel.tsx` (the `Checkbox` plus `Label htmlFor` pattern), `web/src/test/utils.tsx`.

**Interfaces:**
- Consumes: Contracts "Core host join (from Task 7)": the `core.host.create` input members `tailnetJoin` (boolean) and `tags` (array of strings) and the grant member `bundle` (string, secret, set only when `tailnetJoin` was asked; `token` keeps the plain token).
- Produces: Contracts "Web: host create (from Task 11)". `host-create.tsx` default-exports `TailnetHostCreate({ value, onChange })` (the `HostCreateExtension` component props of `web/src/modules/types.ts`); it calls `onChange({ tailnetJoin: true, tags })` with `tags` a `string[]` (possibly empty), or `onChange({})` when the box is unchecked. `create-host-dialog.tsx` keeps its exports (`CreateHostDialog`) and shows the grant's `bundle`, when it is a non-empty string, in place of the token.

**Items:** 2026-10-21-v1-completion#24

**Depends on:** Task 7

- [ ] **Step 1: Write the failing tests**

Create `web/src/modules/tailnet/host-create.test.tsx`:

```tsx
import { useState } from "react"
import { screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test, vi } from "vitest"
import { axe } from "vitest-axe"
import { renderWithProviders } from "@/test/utils"
import TailnetHostCreate from "./host-create"

type HcValue = Record<string, unknown>

// hcRender keeps the value the way the Add host dialog does: whatever the
// component reports becomes the value it is given next.
function hcRender(initial: HcValue = {}) {
  const onChange = vi.fn()
  function Harness() {
    const [value, setValue] = useState<HcValue>(initial)
    return (
      <TailnetHostCreate
        value={value}
        onChange={(v) => {
          onChange(v)
          setValue(v)
        }}
      />
    )
  }
  const result = renderWithProviders(<Harness />)
  return { onChange, ...result }
}

const HC_TAGS = "Tags (comma-separated)"

test("offers to join the tailnet, unchecked, with no tags input", () => {
  hcRender()
  expect(screen.getByRole("checkbox", { name: "Join the tailnet", checked: false })).toBeTruthy()
  expect(screen.queryByLabelText(HC_TAGS)).toBeNull()
})

test("checking the box asks for tags and reports a join with none yet", async () => {
  const { onChange } = hcRender()
  await userEvent.setup().click(screen.getByRole("checkbox", { name: "Join the tailnet" }))
  expect(onChange).toHaveBeenLastCalledWith({ tailnetJoin: true, tags: [] })
  expect(screen.getByRole("checkbox", { name: "Join the tailnet", checked: true })).toBeTruthy()
  expect(screen.getByLabelText(HC_TAGS)).toBeTruthy()
})

test("typed tags are trimmed, de-duplicated and reported as a list", async () => {
  const { onChange } = hcRender()
  const user = userEvent.setup()
  await user.click(screen.getByRole("checkbox", { name: "Join the tailnet" }))
  await user.type(screen.getByLabelText(HC_TAGS), "tag:server, tag:ci ,, tag:server")
  expect(onChange).toHaveBeenLastCalledWith({ tailnetJoin: true, tags: ["tag:server", "tag:ci"] })
})

test("unchecking the box reports no input and hides the tags", async () => {
  const { onChange } = hcRender()
  const user = userEvent.setup()
  await user.click(screen.getByRole("checkbox", { name: "Join the tailnet" }))
  await user.type(screen.getByLabelText(HC_TAGS), "tag:server")
  await user.click(screen.getByRole("checkbox", { name: "Join the tailnet" }))
  expect(onChange).toHaveBeenLastCalledWith({})
  expect(screen.queryByLabelText(HC_TAGS)).toBeNull()
})

test("shows the value it is given", () => {
  hcRender({ tailnetJoin: true, tags: ["tag:server", "tag:ci"] })
  expect(screen.getByRole("checkbox", { name: "Join the tailnet", checked: true })).toBeTruthy()
  expect((screen.getByLabelText(HC_TAGS) as HTMLInputElement).value).toBe("tag:server, tag:ci")
})

test("a tag that is not tag:<name> is flagged", async () => {
  hcRender({ tailnetJoin: true, tags: [] })
  const user = userEvent.setup()
  const input = screen.getByLabelText(HC_TAGS)
  expect(screen.queryByText("Each tag looks like tag:server.")).toBeNull()
  await user.type(input, "server")
  expect(screen.getByText("Each tag looks like tag:server.")).toBeTruthy()
  expect(input.getAttribute("aria-invalid")).toBe("true")
  await user.clear(input)
  await user.type(input, "tag:server")
  expect(screen.queryByText("Each tag looks like tag:server.")).toBeNull()
})

test("the join input has no accessibility violations", async () => {
  const { container } = hcRender({ tailnetJoin: true, tags: ["tag:server"] })
  expect(await axe(container)).toHaveNoViolations()
})
```

In `web/src/pages/create-host-dialog.test.tsx`, add the import `import { formatTime } from "@/lib/format"` beside the other `@/` imports, and append these tests (the file's existing helpers `HOSTS`, `PLAN`, `APPLY`, `chdHost`, `chdPlan`, `chdChange`, `chdGrant` and `chdFill` are reused):

```tsx
async function chdApply(output: unknown) {
  stubFetch({
    [HOSTS]: { body: { output: { hosts: [chdHost([])] } } },
    [PLAN]: { body: chdPlan },
    [APPLY]: { body: chdChange(output) },
  })
  renderWithProviders(<CreateHostDialog />)
  const user = userEvent.setup()
  const form = await chdFill(user, "web-2")
  await user.click(within(form).getByRole("button", { name: "Continue" }))
  const plan = await screen.findByRole("dialog", { name: "Add a host" })
  await user.click(await within(plan).findByRole("button", { name: "Apply" }))
  return screen.findByRole("dialog", { name: "Host added" })
}

test("a grant with a bootstrap bundle shows the bundle in place of the token", async () => {
  const done = await chdApply({ ...chdGrant, bundle: "dpb2.example-bootstrap" })
  expect(within(done).getByDisplayValue("dpb2.example-bootstrap")).toBeTruthy()
  expect(within(done).queryByDisplayValue("dpe_example_enrollment")).toBeNull()
  expect(within(done).getByText("Bootstrap bundle for web-2")).toBeTruthy()
  expect(within(done).getByText("Copy the bootstrap bundle now. It will not be shown again.")).toBeTruthy()
  expect(
    within(done).getByText(`On the new host, run install.sh --role agent --token-file <file> with this bundle before ${formatTime(chdGrant.expiresAt)}.`),
  ).toBeTruthy()
})

test("an empty bundle counts as none and the token is shown", async () => {
  const done = await chdApply({ ...chdGrant, bundle: "" })
  expect(within(done).getByDisplayValue("dpe_example_enrollment")).toBeTruthy()
  expect(within(done).getByText("Enrollment token for web-2")).toBeTruthy()
})

test("the tailnet join input adds tailnetJoin and tags to the planned input", async () => {
  const calls = stubFetch({
    [HOSTS]: { body: { output: { hosts: [chdHost([{ id: "tailnet", version: 1, enabled: true, available: true, reason: null, checkedAt: null }])] } } },
    [PLAN]: { body: chdPlan },
  })
  renderWithProviders(<CreateHostDialog />)
  const user = userEvent.setup()
  const form = await chdFill(user, "web-2")
  await user.click(await within(form).findByRole("checkbox", { name: "Join the tailnet" }))
  await user.type(within(form).getByLabelText("Tags (comma-separated)"), "tag:server")
  await user.click(within(form).getByRole("button", { name: "Continue" }))
  await waitFor(() =>
    expect(calls.find((c) => c.path.endsWith("/plan"))?.body).toEqual({ input: { name: "web-2", tailnetJoin: true, tags: ["tag:server"] } }),
  )
})
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
mkdir -p /root/.cache/w4-tmp/11 && export TMPDIR=/root/.cache/w4-tmp/11 GOTOOLCHAIN=local
timeout 300 pnpm -C web exec vitest run src/modules/tailnet/host-create.test.tsx src/pages/create-host-dialog.test.tsx
```

Expected: FAIL. Every `host-create.test.tsx` test fails with "Unable to find an accessible element with the role "checkbox" and name "Join the tailnet"" (the stub renders nothing); the two bundle tests in `create-host-dialog.test.tsx` fail because the dialog shows `dpe_example_enrollment` and "Enrollment token for web-2" for a grant with a bundle; the join-input test fails on the same missing checkbox. The existing tests of that file still pass. Quote the output.

- [ ] **Step 3: Implement**

1. `host-create.tsx`: keep the default export `TailnetHostCreate({ value, onChange })`, a function component with no effects (derive from `value` while rendering). `joined` is `value.tailnetJoin === true`. Local state `text` (the raw tags text) starts as `value.tags` joined with `", "` when it is an array of strings, else `""`, so a half-typed `tag:a, ` survives re-renders. A parse helper splits `text` on `,`, trims, drops empty entries and drops repeats keeping the first.
2. Render a `Checkbox` (`darkraise-ui/components/checkbox`, `checked={joined}`, `onCheckedChange` with `checked === true`) with id `host-tailnet-join` and a `Label htmlFor` reading exactly "Join the tailnet". Checking calls `onChange({ tailnetJoin: true, tags: parse(text) })`; unchecking calls `onChange({})` and keeps `text` for a later re-check.
3. Only while joined, render an `Input` with id `host-tailnet-tags`, `Label` exactly "Tags (comma-separated)", placeholder `tag:server, tag:ci`, `autoComplete="off"`, bound to `text`; every change updates `text` and calls `onChange({ tailnetJoin: true, tags: parse(newText) })`. A tag is valid when it matches `^tag:[a-z0-9][a-z0-9-]{0,62}$` (the pattern of Contracts "Core host join"). When any parsed tag is invalid, set `aria-invalid="true"` on the input, point `aria-describedby` at a paragraph (destructive text colour) reading exactly "Each tag looks like tag:server." and render that paragraph; otherwise render neither. Nothing blocks the form: the plan refuses a bad tag (`request.invalid_input`, field `/tags`).
4. While joined also render one muted paragraph: "The new host gets a one-use tailnet auth key in a bootstrap bundle and enrolls over the tailnet, so leave the controller address empty." (Task 9 refuses `controllerAddress` together with `tailnetJoin`.)
5. `create-host-dialog.tsx`: add `bundle?: string` to `Grant`. `isGrant` still requires `token`, `expiresAt` and `host`; derive the shown bundle as `grant.bundle` only when `typeof === "string"` and non-empty, else none (an empty or non-string `bundle` counts as absent). With a bundle: the `Clipboard` value is the bundle and its `ClipboardLabel` reads `Bootstrap bundle for <host name>`; the dialog description reads "Copy the bootstrap bundle now. It will not be shown again."; the instruction paragraph reads exactly `On the new host, run install.sh --role agent --token-file <file> with this bundle before <formatTime(expiresAt)>.` (the `<file>` is literal text, escape it in JSX). Without a bundle nothing about the token path changes (label, description and the `sudo darkpanel agent enroll` instruction stay). The token is not rendered when a bundle is. Do not change `submit`: `controllerAddress` and module values still merge as before (the existing merge test pins that).
6. No effect, query or state outlives the dialog: `reset` already clears the grant, so the bundle is forgotten when the dialog closes.

- [ ] **Step 4: Run the tests to verify they pass**

```bash
export TMPDIR=/root/.cache/w4-tmp/11 GOTOOLCHAIN=local
timeout 300 pnpm -C web exec vitest run src/modules/tailnet/host-create.test.tsx src/pages/create-host-dialog.test.tsx
timeout 300 pnpm -C web lint
timeout 300 pnpm -C web typecheck
timeout 600 pnpm -C web test
timeout 600 go build ./... && timeout 300 go vet ./... && timeout 1500 go test -count=1 ./... && timeout 900 golangci-lint run --allow-serial-runners ./... && timeout 300 go mod tidy -diff
rm -rf /root/.cache/w4-tmp/11 && go clean -testcache
```

Expected: both test files PASS; `lint` and `typecheck` print no errors; `pnpm -C web test` passes; every Go line is `ok` or `?`, `0 issues.`, and `go mod tidy -diff` prints nothing. Record the real output.

- [ ] **Step 5: Commit**

```bash
git add web/src/modules/tailnet/host-create.tsx web/src/modules/tailnet/host-create.test.tsx web/src/pages/create-host-dialog.tsx web/src/pages/create-host-dialog.test.tsx
git commit -m "feat(web): join the tailnet from the Add host dialog"
```

### Task 12: web: tailscale status, peers and latency panels

**Files:**
- Create: `web/src/modules/tailscale/status-panel.tsx`, `web/src/modules/tailscale/status-panel.test.tsx`
- Create: `web/src/modules/tailscale/peers-table.tsx`, `web/src/modules/tailscale/peers-table.test.tsx`
- Create: `web/src/modules/tailscale/latency-panel.tsx`, `web/src/modules/tailscale/latency-panel.test.tsx`

Read-only context for conventions: `web/src/modules/docker/containers-tab.tsx` and its test (resource query, table, empty state, error alert), `web/src/modules/firewall/status-panel.tsx` and its test (the `dl` rows layout), `web/src/api/client.ts` (`readResource`, `errorMessage`), `web/src/lib/format.ts` (`formatTime`), `web/src/test/utils.tsx`.

**Interfaces:**
- Consumes: Contracts "Web: tailscale (Tasks 5, 12, 13, 19)": `TAILSCALE_MODULE`, the types `TailscaleStatus`, `TailscaleNode`, `TailscalePeer`, `TailscaleLatency`, `RegionLatency`, `Measurement` and the guards `isTailscaleStatus`, `isTailscaleLatency`, all from `web/src/modules/tailscale/types.ts`; the field names are those of Contracts "Tailscale wire shapes" (`backendState`, `self`, `magicDNSSuffix`, `preferredDerp`, `runSSH`, `peers`, `health`, `regions`, `stun`, `https`, `medianMs`, `reason`, `lastHandshake`, ...). The resources are read with `readResource` from `web/src/api/client.ts` (`GET /api/v1/hosts/<host>/resources/tailscale/status` and `.../latency`), under the query keys `["resource", hostId, "tailscale.status"]` and `["resource", hostId, "tailscale.latency"]` (the convention of `dockerKey` and the firewall's `statusQueryKey`, so `TailscaleChange` invalidating `["resource", hostId]` refreshes them).
- Produces: Contracts "Web: tailscale": `StatusPanel({ hostId })` in `status-panel.tsx`, `PeersTable({ peers })` in `peers-table.tsx` (`peers: TailscalePeer[]`), `LatencyPanel({ hostId })` in `latency-panel.tsx`, each a named export. Task 19 composes them.

**Items:** 2026-10-21-v1-completion#20

**Depends on:** Task 5

- [ ] **Step 1: Write the failing tests**

`web/src/modules/tailscale/status-panel.test.tsx`:

```tsx
import { screen, within } from "@testing-library/react"
import { expect, test } from "vitest"
import { axe } from "vitest-axe"
import { renderWithProviders, stubFetch } from "@/test/utils"
import { StatusPanel } from "./status-panel"
import type { TailscaleStatus } from "./types"

const SP_STATUS = "GET /api/v1/hosts/h1/resources/tailscale/status"

function spStatus(over: Partial<TailscaleStatus> = {}): TailscaleStatus {
  return {
    version: "1.104.1",
    backendState: "Running",
    self: {
      id: "n1",
      hostName: "web-1",
      dnsName: "web-1.tail1234.ts.net",
      os: "linux",
      addresses: ["100.64.0.1", "fd7a:115c:a1e0::1"],
      tags: ["tag:server"],
    },
    tailnet: "example.com",
    magicDNSSuffix: "tail1234.ts.net",
    preferredDerp: "fra",
    runSSH: true,
    peers: [],
    health: [],
    ...over,
  }
}

function spValue(list: HTMLElement, label: string): string | null | undefined {
  return within(list).getByText(label, { selector: "dt" }).nextElementSibling?.textContent
}

test("shows the node's state, names, addresses, home DERP region and SSH setting", async () => {
  stubFetch({ [SP_STATUS]: { body: spStatus() } })
  renderWithProviders(<StatusPanel hostId="h1" />)
  const list = await screen.findByLabelText("Tailscale status")
  expect(spValue(list, "Backend state")).toBe("Running")
  expect(spValue(list, "Version")).toBe("1.104.1")
  expect(spValue(list, "Tailnet")).toBe("example.com")
  expect(spValue(list, "MagicDNS suffix")).toBe("tail1234.ts.net")
  expect(spValue(list, "DNS name")).toBe("web-1.tail1234.ts.net")
  expect(spValue(list, "Addresses")).toBe("100.64.0.1, fd7a:115c:a1e0::1")
  expect(spValue(list, "Tags")).toBe("tag:server")
  expect(spValue(list, "Home DERP region")).toBe("fra")
  expect(spValue(list, "Tailscale SSH")).toBe("On")
})

test("a logged-out node shows its state and dashes where it has no value", async () => {
  stubFetch({
    [SP_STATUS]: { body: spStatus({ backendState: "NeedsLogin", self: null, tailnet: "", magicDNSSuffix: "", preferredDerp: "", runSSH: false }) },
  })
  renderWithProviders(<StatusPanel hostId="h1" />)
  const list = await screen.findByLabelText("Tailscale status")
  expect(spValue(list, "Backend state")).toBe("NeedsLogin")
  for (const label of ["Tailnet", "MagicDNS suffix", "DNS name", "Addresses", "Tags", "Home DERP region"]) {
    expect(spValue(list, label)).toBe("—")
  }
  expect(spValue(list, "Tailscale SSH")).toBe("Off")
})

test("health warnings are listed, and none shows no list", async () => {
  stubFetch({ [SP_STATUS]: { body: spStatus({ health: ["Tailscale can't reach the log server.", "The DNS configuration is broken."] }) } })
  const { unmount } = renderWithProviders(<StatusPanel hostId="h1" />)
  const warnings = await screen.findByRole("list", { name: "Health warnings" })
  expect(within(warnings).getAllByRole("listitem").map((li) => li.textContent)).toEqual([
    "Tailscale can't reach the log server.",
    "The DNS configuration is broken.",
  ])
  unmount()

  stubFetch({ [SP_STATUS]: { body: spStatus() } })
  renderWithProviders(<StatusPanel hostId="h1" />)
  await screen.findByLabelText("Tailscale status")
  expect(screen.queryByRole("list", { name: "Health warnings" })).toBeNull()
})

test("an unavailable tailscaled is an error with the host's own reason", async () => {
  stubFetch({
    [SP_STATUS]: {
      status: 503,
      body: { type: "about:blank", title: "Unavailable", status: 503, detail: "tailscaled does not answer on its socket", code: "tailscale.unavailable" },
    },
  })
  renderWithProviders(<StatusPanel hostId="h1" />)
  expect((await screen.findByRole("alert")).textContent).toBe("tailscaled does not answer on its socket")
})

test("an answer that is not a status is an error", async () => {
  stubFetch({ [SP_STATUS]: { body: { backendState: 7 } } })
  renderWithProviders(<StatusPanel hostId="h1" />)
  expect((await screen.findByRole("alert")).textContent).toBe("The host answered with an unexpected Tailscale status.")
})

test("the status panel has no accessibility violations", async () => {
  stubFetch({ [SP_STATUS]: { body: spStatus({ health: ["The DNS configuration is broken."] }) } })
  const { container } = renderWithProviders(<StatusPanel hostId="h1" />)
  await screen.findByLabelText("Tailscale status")
  expect(await axe(container)).toHaveNoViolations()
})
```

`web/src/modules/tailscale/peers-table.test.tsx`:

```tsx
import { screen, within } from "@testing-library/react"
import { expect, test } from "vitest"
import { axe } from "vitest-axe"
import { formatTime } from "@/lib/format"
import { renderWithProviders } from "@/test/utils"
import { PeersTable } from "./peers-table"
import type { TailscalePeer } from "./types"

function ptPeer(over: Partial<TailscalePeer> = {}): TailscalePeer {
  return {
    id: "n2",
    hostName: "db-1",
    dnsName: "db-1.tail1234.ts.net",
    os: "linux",
    addresses: ["100.64.0.2"],
    tags: [],
    online: true,
    connection: "direct",
    endpoint: "203.0.113.7:41641",
    region: "",
    exitNode: false,
    exitNodeOption: false,
    lastHandshake: "2026-10-31T08:00:00Z",
    ...over,
  }
}

const PT_PEERS: TailscalePeer[] = [
  ptPeer({ addresses: ["100.64.0.2", "fd7a:115c:a1e0::2"] }),
  ptPeer({ id: "n3", dnsName: "app-1.tail1234.ts.net", addresses: ["100.64.0.3"], connection: "derp", endpoint: "", region: "fra", lastHandshake: null }),
  ptPeer({ id: "n4", hostName: "edge", dnsName: "", addresses: ["100.64.0.4"], connection: "peer-relay", endpoint: "", exitNodeOption: true, lastHandshake: null }),
  ptPeer({ id: "n5", dnsName: "gw.tail1234.ts.net", addresses: ["100.64.0.5"], online: false, connection: "none", endpoint: "", exitNode: true, exitNodeOption: true, lastHandshake: null }),
]

function ptCells(row: HTMLElement | undefined): (string | null)[] {
  return within(row as HTMLElement).getAllByRole("cell").map((c) => c.textContent)
}

test("lists each peer with its addresses, state, connection, exit node role and last handshake", () => {
  renderWithProviders(<PeersTable peers={PT_PEERS} />)
  const table = screen.getByRole("table", { name: "Tailscale peers" })
  expect(within(table).getAllByRole("columnheader").map((h) => h.textContent)).toEqual([
    "Name",
    "Addresses",
    "Status",
    "Connection",
    "Exit node",
    "Last handshake",
  ])
  const rows = within(table).getAllByRole("row")
  expect(rows).toHaveLength(5)
  expect(ptCells(rows[1])).toEqual([
    "db-1.tail1234.ts.net",
    "100.64.0.2, fd7a:115c:a1e0::2",
    "Online",
    "direct 203.0.113.7:41641",
    "—",
    formatTime("2026-10-31T08:00:00Z"),
  ])
  expect(ptCells(rows[2])).toEqual(["app-1.tail1234.ts.net", "100.64.0.3", "Online", "derp fra", "—", "—"])
  expect(ptCells(rows[3])).toEqual(["edge", "100.64.0.4", "Online", "peer-relay", "Available", "—"])
  expect(ptCells(rows[4])).toEqual(["gw.tail1234.ts.net", "100.64.0.5", "Offline", "none", "In use", "—"])
})

test("no peers is an empty state, not an empty table", () => {
  renderWithProviders(<PeersTable peers={[]} />)
  expect(screen.getByText("No peers")).toBeTruthy()
  expect(screen.queryByRole("table")).toBeNull()
})

test("the peers table has no accessibility violations", async () => {
  const { container } = renderWithProviders(<PeersTable peers={PT_PEERS} />)
  expect(await axe(container)).toHaveNoViolations()
})
```

`web/src/modules/tailscale/latency-panel.test.tsx`:

```tsx
import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import { axe } from "vitest-axe"
import { renderWithProviders, stubFetch } from "@/test/utils"
import { LatencyPanel } from "./latency-panel"
import type { TailscaleLatency } from "./types"

const LP_LATENCY = "GET /api/v1/hosts/h1/resources/tailscale/latency"

const lpLatency: TailscaleLatency = {
  regions: [
    {
      regionId: 1,
      code: "nyc",
      name: "New York City",
      stun: { available: true, medianMs: 12.34, reason: "" },
      https: { available: true, medianMs: 20.5, reason: "" },
    },
    {
      regionId: 9,
      code: "fra",
      name: "Frankfurt",
      stun: { available: false, medianMs: 0, reason: "no STUN endpoint" },
      https: { available: true, medianMs: 101, reason: "" },
    },
  ],
}

test("shows both measurements for each region and says what they measure", async () => {
  stubFetch({ [LP_LATENCY]: { body: lpLatency } })
  renderWithProviders(<LatencyPanel hostId="h1" />)
  const table = await screen.findByRole("table", { name: "DERP latency" })
  expect(within(table).getAllByRole("columnheader").map((h) => h.textContent)).toEqual(["Region", "STUN latency", "DERP HTTPS probe latency"])
  const rows = within(table).getAllByRole("row")
  const cells = (row: HTMLElement | undefined) => within(row as HTMLElement).getAllByRole("cell").map((c) => c.textContent)
  expect(cells(rows[1])).toEqual(["New York City (nyc)", "12.3 ms", "20.5 ms"])
  expect(cells(rows[2])).toEqual(["Frankfurt (fra)", "Unavailable: no STUN endpoint", "101.0 ms"])
  expect(screen.getByText(/measures reaching the server, not a relayed peer path/)).toBeTruthy()
})

test("an empty DERP map is an empty state", async () => {
  stubFetch({ [LP_LATENCY]: { body: { regions: [] } } })
  renderWithProviders(<LatencyPanel hostId="h1" />)
  expect(await screen.findByText("No DERP regions")).toBeTruthy()
  expect(screen.queryByRole("table")).toBeNull()
})

test("Measure again reads the latency once more", async () => {
  const calls = stubFetch({ [LP_LATENCY]: { body: lpLatency } })
  renderWithProviders(<LatencyPanel hostId="h1" />)
  await screen.findByRole("table", { name: "DERP latency" })
  expect(calls.filter((c) => c.path.endsWith("/latency"))).toHaveLength(1)
  await userEvent.setup().click(screen.getByRole("button", { name: "Measure again" }))
  await waitFor(() => expect(calls.filter((c) => c.path.endsWith("/latency"))).toHaveLength(2))
})

test("a failed read and an answer that is not a measurement are errors", async () => {
  stubFetch({
    [LP_LATENCY]: {
      status: 503,
      body: { type: "about:blank", title: "Unavailable", status: 503, detail: "tailscaled does not answer on its socket", code: "tailscale.unavailable" },
    },
  })
  const { unmount } = renderWithProviders(<LatencyPanel hostId="h1" />)
  expect((await screen.findByRole("alert")).textContent).toBe("tailscaled does not answer on its socket")
  unmount()

  stubFetch({ [LP_LATENCY]: { body: { regions: "none" } } })
  renderWithProviders(<LatencyPanel hostId="h1" />)
  expect((await screen.findByRole("alert")).textContent).toBe("The host answered with an unexpected latency measurement.")
})

test("the latency panel has no accessibility violations", async () => {
  stubFetch({ [LP_LATENCY]: { body: lpLatency } })
  const { container } = renderWithProviders(<LatencyPanel hostId="h1" />)
  await screen.findByRole("table", { name: "DERP latency" })
  expect(await axe(container)).toHaveNoViolations()
})
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
mkdir -p /root/.cache/w4-tmp/12 && export TMPDIR=/root/.cache/w4-tmp/12 GOTOOLCHAIN=local
timeout 300 pnpm -C web exec vitest run src/modules/tailscale/status-panel.test.tsx src/modules/tailscale/peers-table.test.tsx src/modules/tailscale/latency-panel.test.tsx
```

Expected: FAIL, each file with "Failed to resolve import "./status-panel"" (respectively `./peers-table`, `./latency-panel`): the components do not exist. Quote the output.

- [ ] **Step 3: Implement**

1. `status-panel.tsx` exports `StatusPanel({ hostId })`. A `useQuery` with key `["resource", hostId, "tailscale.status"]` whose function calls `readResource<unknown>(hostId, TAILSCALE_MODULE, "status")` and throws `new Error("The host answered with an unexpected Tailscale status.")` when `isTailscaleStatus` is false. While pending render `<Spinner aria-label="Loading the Tailscale status" />`. On error render a destructive `Alert` with `role="alert"` whose `AlertDescription` is the message: `errorMessage(error)` for an `ApiError` (so `tailscale.unavailable` shows the host's own reason), and the thrown message for the guard failure. Check `errorMessage` in `web/src/api/client.ts` returns a generic text for a plain `Error`; for the guard failure show `error.message` directly (test `an answer that is not a status` pins the exact text).
2. On success render a `Card` titled "Node" holding `<dl aria-label="Tailscale status">` in the firewall `Rows` layout (`dt` label, `dd` value, same classes), in this order with these labels and values: "Backend state" (a `Badge` with `backendState`, variant `default` when `Running`, `destructive` for `NeedsLogin` or `NeedsMachineAuth`, else `secondary`; the `dd` text is the state itself), "Version", "Tailnet", "MagicDNS suffix", "DNS name" (`self.dnsName`), "Addresses" (`self.addresses` joined with `", "`), "Tags" (`self.tags` joined with `", "`), "Home DERP region" (`preferredDerp`), "Tailscale SSH" ("On" when `runSSH`, else "Off"). Any empty string, empty list or null `self` shows `—`.
3. When `health` is non-empty, above the card render an `Alert` (variant `warning`, `role="status"`) titled "Tailscale reports problems" holding `<ul aria-label="Health warnings">` with one `li` per message. With none, render neither alert nor list.
4. `peers-table.tsx` exports `PeersTable({ peers })`. Empty `peers` renders `<EmptyState title="No peers" />` and no table. Otherwise a `Table` with `aria-label="Tailscale peers"` and column headers, in order, "Name", "Addresses", "Status", "Connection", "Exit node", "Last handshake". Rows keep the order given (the agent sorts). Cells: Name is `dnsName`, or `hostName` when `dnsName` is empty; Addresses joined with `", "`; Status is a `Badge` reading "Online" or "Offline"; Connection is `direct <endpoint>` for `direct`, `derp <region>` for `derp`, `peer-relay` for `peer-relay` and `none` for `none`; Exit node is "In use" when `exitNode`, else "Available" when `exitNodeOption`, else `—`; Last handshake is `formatTime(lastHandshake)` (`—` for null).
5. `latency-panel.tsx` exports `LatencyPanel({ hostId })`. A `useQuery` with key `["resource", hostId, "tailscale.latency"]`, `refetchOnWindowFocus: false` (a read sends probes), reading `readResource<unknown>(hostId, TAILSCALE_MODULE, "latency")` and throwing `new Error("The host answered with an unexpected latency measurement.")` when `isTailscaleLatency` is false. Pending: `<Spinner aria-label="Measuring the latency" />`. Error: the same destructive alert as the status panel. Empty `regions`: `<EmptyState title="No DERP regions" />`. Otherwise a `Table` with `aria-label="DERP latency"`, headers "Region", "STUN latency", "DERP HTTPS probe latency", one row per region in the order given, cells `<name> (<code>)` and, for each measurement, `<medianMs to one decimal> ms` when `available`, else `Unavailable: <reason>` (just `Unavailable` when `reason` is empty). Under the table a muted paragraph: "STUN latency is the median of three STUN binding requests from the host. The DERP HTTPS probe measures reaching the server, not a relayed peer path." A `Button` "Measure again" (outline, disabled while `isFetching`) calls `refetch()`.
6. No effects; no module-level state. Do not edit `types.ts`, `fixtures.ts` or `tailscale-change.tsx` (Task 5's files).

- [ ] **Step 4: Run the tests to verify they pass**

```bash
export TMPDIR=/root/.cache/w4-tmp/12 GOTOOLCHAIN=local
timeout 300 pnpm -C web exec vitest run src/modules/tailscale/status-panel.test.tsx src/modules/tailscale/peers-table.test.tsx src/modules/tailscale/latency-panel.test.tsx
timeout 300 pnpm -C web lint
timeout 300 pnpm -C web typecheck
timeout 600 pnpm -C web test
timeout 600 go build ./... && timeout 300 go vet ./... && timeout 1500 go test -count=1 ./... && timeout 900 golangci-lint run --allow-serial-runners ./... && timeout 300 go mod tidy -diff
rm -rf /root/.cache/w4-tmp/12 && go clean -testcache
```

Expected: the three files PASS; lint, typecheck and the whole web suite pass; the Go lines are `ok` or `?`, `0 issues.`, and `go mod tidy -diff` prints nothing. Record the real output.

- [ ] **Step 5: Commit**

```bash
git add web/src/modules/tailscale/status-panel.tsx web/src/modules/tailscale/status-panel.test.tsx web/src/modules/tailscale/peers-table.tsx web/src/modules/tailscale/peers-table.test.tsx web/src/modules/tailscale/latency-panel.tsx web/src/modules/tailscale/latency-panel.test.tsx
git commit -m "feat(web): tailscale status, peers and latency panels"
```

### Task 13: web: tailscale preferences, node and package controls

**Files:**
- Create: `web/src/modules/tailscale/prefs-form.tsx`, `web/src/modules/tailscale/prefs-form.test.tsx`
- Create: `web/src/modules/tailscale/node-controls.tsx`, `web/src/modules/tailscale/node-controls.test.tsx`
- Create: `web/src/modules/tailscale/package-panel.tsx`, `web/src/modules/tailscale/package-panel.test.tsx`

Read-only context for conventions: `web/src/modules/firewall/state-controls.tsx` and its test (forms that open a change through `FirewallChange`, the `scPlanned` helper), `web/src/modules/docker/containers-tab.tsx`, `web/src/components/change-dialog.tsx` (the dialog adds the typed-confirmation field, the guard override and, for `revertible` targets, the revert timeout), `web/src/test/utils.tsx`.

**Interfaces:**
- Consumes: Contracts "Web: tailscale": from `web/src/modules/tailscale/types.ts` `TailscaleStatus`, `TailscalePeer` and `PrefsFields` (the mirror of Contracts "Tailscale wire shapes": optional `wantRunning`, `advertiseRoutes` (string[]), `exitNode`, `advertiseExitNode`, `acceptRoutes`, `shieldsUp`, `runSSH`); from `tailscale-change.tsx` `TailscaleChange({ hostId, action, title, input, onClose })` (`input: Record<string, unknown> | null`, null keeps the dialog closed; for `tailscale.prefs.set` it already asks for the revert timeout). The action names and input members are those of Contracts "Tailscale wire shapes": `tailscale.prefs.set` (`PrefsFields` members), `tailscale.node.login` (`authKey`, `hostname`, `advertiseTags`, `relogin`), `tailscale.node.logout` (no input), `tailscale.package.install` and `tailscale.package.update` (no input).
- Produces: Contracts "Web: tailscale": `PrefsForm({ hostId, status, prefs })` with `prefs: PrefsFields` (the node's current preferences; a member left out is unknown and is taken from `status`), `NodeControls({ hostId, status })` and `PackagePanel({ hostId, available })` with `available: boolean`, each a named export. Task 19 composes them.

**Items:** 2026-10-21-v1-completion#21

**Depends on:** Task 5

- [ ] **Step 1: Write the failing tests**

Every test file stubs the module availability that `TailscaleChange` reads and the plan of the action under test. `web/src/modules/tailscale/prefs-form.test.tsx`:

```tsx
import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import { axe } from "vitest-axe"
import { renderWithProviders, stubFetch, type FetchCall } from "@/test/utils"
import { PrefsForm } from "./prefs-form"
import type { PrefsFields, TailscalePeer, TailscaleStatus } from "./types"

const PF_PLAN = "POST /api/v1/hosts/h1/actions/tailscale.prefs.set/v1/plan"

function pfPeer(over: Partial<TailscalePeer> = {}): TailscalePeer {
  return {
    id: "n9",
    hostName: "gw",
    dnsName: "gw.tail1234.ts.net",
    os: "linux",
    addresses: ["100.64.0.9"],
    tags: [],
    online: true,
    connection: "direct",
    endpoint: "203.0.113.9:41641",
    region: "",
    exitNode: false,
    exitNodeOption: true,
    lastHandshake: null,
    ...over,
  }
}

function pfStatus(over: Partial<TailscaleStatus> = {}): TailscaleStatus {
  return {
    version: "1.104.1",
    backendState: "Running",
    self: { id: "n1", hostName: "web-1", dnsName: "web-1.tail1234.ts.net", os: "linux", addresses: ["100.64.0.1"], tags: [] },
    tailnet: "example.com",
    magicDNSSuffix: "tail1234.ts.net",
    preferredDerp: "fra",
    runSSH: true,
    peers: [pfPeer()],
    health: [],
    ...over,
  }
}

const pfPrefs: PrefsFields = {
  wantRunning: true,
  advertiseRoutes: ["10.0.0.0/24"],
  exitNode: "",
  advertiseExitNode: false,
  acceptRoutes: true,
  shieldsUp: false,
  runSSH: true,
}

function pfRender(status: TailscaleStatus, prefs: PrefsFields) {
  const calls = stubFetch({
    "GET /api/v1/hosts/h1/modules/tailscale/availability": { body: { module: "tailscale", available: true, reason: null, actions: [] } },
    [PF_PLAN]: {
      body: {
        planId: "p1", hostId: "h1", action: "tailscale.prefs.set", version: 1, inputHash: "h",
        diff: [{ op: "~", text: "prefs" }], guards: [], expiresAt: "2026-10-31T09:00:00Z", confirmation: null,
      },
    },
  })
  const { container } = renderWithProviders(<PrefsForm hostId="h1" status={status} prefs={prefs} />)
  return { calls, container }
}

async function pfPlanned(calls: FetchCall[]): Promise<{ path: string; body: unknown }> {
  const dialog = await screen.findByRole("dialog", { name: "Change the Tailscale preferences" })
  await within(dialog).findByLabelText("Planned changes")
  const call = calls.find((c) => c.path.endsWith("/plan"))
  return { path: call?.path ?? "", body: call?.body }
}

const pfApply = () => screen.getByRole("button", { name: "Apply preferences" })

test("shows the node's preferences and offers nothing to apply until one changes", () => {
  pfRender(pfStatus(), pfPrefs)
  expect(screen.getByRole("checkbox", { name: "Keep Tailscale running", checked: true })).toBeTruthy()
  expect(screen.getByRole("checkbox", { name: "Accept subnet routes", checked: true })).toBeTruthy()
  expect(screen.getByRole("checkbox", { name: "Shields up", checked: false })).toBeTruthy()
  expect(screen.getByRole("checkbox", { name: "Tailscale SSH", checked: true })).toBeTruthy()
  expect(screen.getByRole("checkbox", { name: "Advertise as an exit node", checked: false })).toBeTruthy()
  expect((screen.getByLabelText("Exit node") as HTMLInputElement).value).toBe("")
  expect((screen.getByLabelText("Advertised routes (comma-separated)") as HTMLInputElement).value).toBe("10.0.0.0/24")
  expect(pfApply().hasAttribute("disabled")).toBe(true)
})

test("a preference the node did not report is taken from the status", () => {
  pfRender(pfStatus({ backendState: "Stopped", runSSH: true, peers: [pfPeer({ id: "n9", exitNode: true })] }), {})
  expect(screen.getByRole("checkbox", { name: "Keep Tailscale running", checked: false })).toBeTruthy()
  expect(screen.getByRole("checkbox", { name: "Tailscale SSH", checked: true })).toBeTruthy()
  expect((screen.getByLabelText("Exit node") as HTMLInputElement).value).toBe("n9")
})

test("plans only the fields that changed", async () => {
  const { calls } = pfRender(pfStatus(), pfPrefs)
  const user = userEvent.setup()
  await user.click(screen.getByRole("checkbox", { name: "Accept subnet routes" }))
  await user.click(screen.getByRole("checkbox", { name: "Shields up" }))
  await user.click(pfApply())
  expect(await pfPlanned(calls)).toEqual({
    path: "/api/v1/hosts/h1/actions/tailscale.prefs.set/v1/plan",
    body: { input: { acceptRoutes: false, shieldsUp: true } },
  })
})

test("sends the advertised routes and the exit node as typed", async () => {
  const { calls } = pfRender(pfStatus(), pfPrefs)
  const user = userEvent.setup()
  const routes = screen.getByLabelText("Advertised routes (comma-separated)")
  await user.clear(routes)
  await user.type(routes, "10.0.0.0/24, 192.168.1.0/24")
  await user.type(screen.getByLabelText("Exit node"), "100.64.0.9")
  await user.click(pfApply())
  expect((await pfPlanned(calls)).body).toEqual({ input: { advertiseRoutes: ["10.0.0.0/24", "192.168.1.0/24"], exitNode: "100.64.0.9" } })
})

test("clearing the exit node and the routes sends an empty value for each", async () => {
  const { calls } = pfRender(pfStatus(), { ...pfPrefs, exitNode: "n9" })
  const user = userEvent.setup()
  await user.clear(screen.getByLabelText("Exit node"))
  await user.clear(screen.getByLabelText("Advertised routes (comma-separated)"))
  await user.click(pfApply())
  expect((await pfPlanned(calls)).body).toEqual({ input: { exitNode: "", advertiseRoutes: [] } })
})

test("a default route cannot be advertised as a route", async () => {
  pfRender(pfStatus(), pfPrefs)
  const user = userEvent.setup()
  const routes = screen.getByLabelText("Advertised routes (comma-separated)")
  await user.clear(routes)
  await user.type(routes, "0.0.0.0/0")
  expect(screen.getByText("Use the exit node option to advertise a default route.")).toBeTruthy()
  expect(routes.getAttribute("aria-invalid")).toBe("true")
  expect(pfApply().hasAttribute("disabled")).toBe(true)
})

test("a logged-out node cannot have its preferences changed", () => {
  pfRender(pfStatus({ backendState: "NeedsLogin", self: null }), pfPrefs)
  expect(screen.getByText("Log the node in to change its preferences.")).toBeTruthy()
  expect(screen.getByRole("checkbox", { name: "Shields up" }).hasAttribute("disabled")).toBe(true)
  expect(screen.getByLabelText("Exit node").hasAttribute("disabled")).toBe(true)
  expect(pfApply().hasAttribute("disabled")).toBe(true)
})

test("closing the change dialog shows the node's values again", async () => {
  const { calls } = pfRender(pfStatus(), pfPrefs)
  const user = userEvent.setup()
  await user.click(screen.getByRole("checkbox", { name: "Accept subnet routes" }))
  await user.click(pfApply())
  await pfPlanned(calls)
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect(screen.getByRole("checkbox", { name: "Accept subnet routes", checked: true })).toBeTruthy()
  expect(pfApply().hasAttribute("disabled")).toBe(true)
})

test("the preferences form has no accessibility violations", async () => {
  const { container } = pfRender(pfStatus(), pfPrefs)
  expect(await axe(container)).toHaveNoViolations()
})
```

`web/src/modules/tailscale/node-controls.test.tsx`:

```tsx
import { screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import { axe } from "vitest-axe"
import { renderWithProviders, stubFetch, type FetchCall } from "@/test/utils"
import { NodeControls } from "./node-controls"
import type { TailscaleStatus } from "./types"

const ncLoggedIn: TailscaleStatus = {
  version: "1.104.1",
  backendState: "Running",
  self: { id: "n1", hostName: "web-1", dnsName: "web-1.tail1234.ts.net", os: "linux", addresses: ["100.64.0.1"], tags: [] },
  tailnet: "example.com",
  magicDNSSuffix: "tail1234.ts.net",
  preferredDerp: "fra",
  runSSH: false,
  peers: [],
  health: [],
}
const ncLoggedOut: TailscaleStatus = { ...ncLoggedIn, backendState: "NeedsLogin", self: null, tailnet: "", magicDNSSuffix: "", preferredDerp: "" }

function ncRender(status: TailscaleStatus) {
  const plan = (call: FetchCall) => {
    const action = call.path.split("/")[6] ?? ""
    return {
      body: {
        planId: "p1", hostId: "h1", action, version: 1, inputHash: "h",
        diff: [{ op: "~", text: action }], guards: [], expiresAt: "2026-10-31T09:00:00Z", confirmation: null,
      },
    }
  }
  const calls = stubFetch({
    "GET /api/v1/hosts/h1/modules/tailscale/availability": { body: { module: "tailscale", available: true, reason: null, actions: [] } },
    "POST /api/v1/hosts/h1/actions/tailscale.node.login/v1/plan": plan,
    "POST /api/v1/hosts/h1/actions/tailscale.node.logout/v1/plan": plan,
  })
  const { container } = renderWithProviders(<NodeControls hostId="h1" status={status} />)
  return { calls, container }
}

async function ncPlanned(calls: FetchCall[], title: string): Promise<{ path: string; body: unknown }> {
  const dialog = await screen.findByRole("dialog", { name: title })
  await within(dialog).findByLabelText("Planned changes")
  const call = calls.find((c) => c.path.endsWith("/plan"))
  return { path: call?.path ?? "", body: call?.body }
}

test("a logged-out node offers a login with an auth key and no logout", () => {
  ncRender(ncLoggedOut)
  const form = screen.getByRole("form", { name: "Log in" })
  const key = within(form).getByLabelText("Auth key")
  expect(key.getAttribute("type")).toBe("password")
  expect(within(form).getByRole("button", { name: "Log in" }).hasAttribute("disabled")).toBe(true)
  expect(screen.queryByRole("button", { name: "Log out" })).toBeNull()
})

test("logs in with the key, hostname and tags, and forgets the key once the change is planned", async () => {
  const { calls } = ncRender(ncLoggedOut)
  const user = userEvent.setup()
  const form = screen.getByRole("form", { name: "Log in" })
  await user.type(within(form).getByLabelText("Auth key"), "tskey-auth-example")
  await user.type(within(form).getByLabelText("Hostname (optional)"), "web-2")
  await user.type(within(form).getByLabelText("Tags (comma-separated)"), "tag:server, tag:ci")
  await user.click(within(form).getByRole("button", { name: "Log in" }))
  expect(await ncPlanned(calls, "Log the node in to the tailnet")).toEqual({
    path: "/api/v1/hosts/h1/actions/tailscale.node.login/v1/plan",
    body: { input: { authKey: "tskey-auth-example", hostname: "web-2", advertiseTags: ["tag:server", "tag:ci"] } },
  })
  expect((screen.getByLabelText("Auth key") as HTMLInputElement).value).toBe("")
})

test("a login with only a key sends only the key", async () => {
  const { calls } = ncRender(ncLoggedOut)
  const user = userEvent.setup()
  const form = screen.getByRole("form", { name: "Log in" })
  await user.type(within(form).getByLabelText("Auth key"), "tskey-auth-example")
  await user.click(within(form).getByRole("button", { name: "Log in" }))
  expect((await ncPlanned(calls, "Log the node in to the tailnet")).body).toEqual({ input: { authKey: "tskey-auth-example" } })
})

test("a logged-in node offers Log out and a login again that asks for relogin", async () => {
  const { calls } = ncRender(ncLoggedIn)
  const user = userEvent.setup()
  const form = screen.getByRole("form", { name: "Log in" })
  await user.type(within(form).getByLabelText("Auth key"), "tskey-auth-example")
  await user.click(within(form).getByRole("button", { name: "Log in again" }))
  expect(await ncPlanned(calls, "Log the node in again")).toEqual({
    path: "/api/v1/hosts/h1/actions/tailscale.node.login/v1/plan",
    body: { input: { authKey: "tskey-auth-example", relogin: true } },
  })
})

test("Log out plans tailscale.node.logout with no input", async () => {
  const { calls } = ncRender(ncLoggedIn)
  await userEvent.setup().click(screen.getByRole("button", { name: "Log out" }))
  expect(await ncPlanned(calls, "Log the node out of the tailnet")).toEqual({
    path: "/api/v1/hosts/h1/actions/tailscale.node.logout/v1/plan",
    body: { input: {} },
  })
})

test("the node controls have no accessibility violations", async () => {
  const { container } = ncRender(ncLoggedIn)
  expect(await axe(container)).toHaveNoViolations()
})
```

`web/src/modules/tailscale/package-panel.test.tsx`:

```tsx
import { screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import { axe } from "vitest-axe"
import { renderWithProviders, stubFetch, type FetchCall } from "@/test/utils"
import { PackagePanel } from "./package-panel"

function ppRender(available: boolean) {
  const plan = (call: FetchCall) => {
    const action = call.path.split("/")[6] ?? ""
    return {
      body: {
        planId: "p1", hostId: "h1", action, version: 1, inputHash: "h",
        diff: [{ op: "~", text: action }], guards: [], expiresAt: "2026-10-31T09:00:00Z", confirmation: null,
      },
    }
  }
  const calls = stubFetch({
    "GET /api/v1/hosts/h1/modules/tailscale/availability": { body: { module: "tailscale", available: true, reason: null, actions: [] } },
    "POST /api/v1/hosts/h1/actions/tailscale.package.install/v1/plan": plan,
    "POST /api/v1/hosts/h1/actions/tailscale.package.update/v1/plan": plan,
  })
  const { container } = renderWithProviders(<PackagePanel hostId="h1" available={available} />)
  return { calls, container }
}

async function ppPlanned(calls: FetchCall[], title: string): Promise<{ path: string; body: unknown }> {
  const dialog = await screen.findByRole("dialog", { name: title })
  await within(dialog).findByLabelText("Planned changes")
  const call = calls.find((c) => c.path.endsWith("/plan"))
  return { path: call?.path ?? "", body: call?.body }
}

test("a host where tailscaled does not answer offers the install and no update", async () => {
  const { calls } = ppRender(false)
  expect(screen.getByText("Tailscale is not installed on this host, or tailscaled does not answer.")).toBeTruthy()
  expect(screen.queryByRole("button", { name: "Update Tailscale" })).toBeNull()
  await userEvent.setup().click(screen.getByRole("button", { name: "Install Tailscale" }))
  expect(await ppPlanned(calls, "Install Tailscale")).toEqual({
    path: "/api/v1/hosts/h1/actions/tailscale.package.install/v1/plan",
    body: { input: {} },
  })
})

test("a host where tailscaled answers offers the update and no install", async () => {
  const { calls } = ppRender(true)
  expect(screen.queryByRole("button", { name: "Install Tailscale" })).toBeNull()
  await userEvent.setup().click(screen.getByRole("button", { name: "Update Tailscale" }))
  expect(await ppPlanned(calls, "Update Tailscale")).toEqual({
    path: "/api/v1/hosts/h1/actions/tailscale.package.update/v1/plan",
    body: { input: {} },
  })
})

test("the package panel has no accessibility violations", async () => {
  const { container } = ppRender(true)
  expect(await axe(container)).toHaveNoViolations()
})
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
mkdir -p /root/.cache/w4-tmp/13 && export TMPDIR=/root/.cache/w4-tmp/13 GOTOOLCHAIN=local
timeout 300 pnpm -C web exec vitest run src/modules/tailscale/prefs-form.test.tsx src/modules/tailscale/node-controls.test.tsx src/modules/tailscale/package-panel.test.tsx
```

Expected: FAIL, each file with "Failed to resolve import "./prefs-form"" (respectively `./node-controls`, `./package-panel`). Quote the output.

- [ ] **Step 3: Implement**

All three components open their change through `TailscaleChange` (Task 5) with the `hostId`, an `action`, a `title`, an `input` (null while closed) and an `onClose` that clears it. None has an effect; each derives what it shows while rendering.

1. `prefs-form.tsx` exports `PrefsForm({ hostId, status, prefs })`. The node's values are derived from the props: `wantRunning` is `prefs.wantRunning`, else `status.backendState !== "Stopped"`; `runSSH` is `prefs.runSSH`, else `status.runSSH`; `exitNode` is `prefs.exitNode`, else the `id` of the first peer with `exitNode`, else `""`; `acceptRoutes`, `shieldsUp` and `advertiseExitNode` default to false and `advertiseRoutes` to `[]`. The user's edits are kept as a partial draft over those values (so a refreshed prop shows through wherever the user changed nothing); the field shows `draft ?? derived`.
2. Render a `<form aria-label="Tailscale preferences">` in a `Card` titled "Preferences", with `Checkbox` plus `Label` pairs (as in `web/src/modules/tailnet/account-panel.tsx`) labelled exactly "Keep Tailscale running" (`wantRunning`), "Accept subnet routes" (`acceptRoutes`), "Shields up" (`shieldsUp`), "Tailscale SSH" (`runSSH`), "Advertise as an exit node" (`advertiseExitNode`); an `Input` labelled "Exit node" (placeholder `node ID or Tailscale IP; empty for none`) and an `Input` labelled "Advertised routes (comma-separated)" (placeholder `10.0.0.0/24, 192.168.1.0/24`); a submit `Button` "Apply preferences". Routes text is split on `,`, trimmed, with empty entries dropped. The routes input keeps its raw text in the draft so a half-typed list survives.
3. The input is only the fields whose value differs from the derived one: booleans by `!==`, `exitNode` by the trimmed string, `advertiseRoutes` as a set (order and repeats do not count as a change); a changed `advertiseRoutes` is sent in the typed order and `[]` when cleared. "Apply preferences" is disabled when nothing changed, when the node is logged out, or when a route is invalid. A route is invalid when it equals `0.0.0.0/0` or `::/0`: then set `aria-invalid="true"` on the routes input and show the paragraph "Use the exit node option to advertise a default route." (Everything else is validated by the agent at plan.) Submitting opens `TailscaleChange` with action `tailscale.prefs.set`, title "Change the Tailscale preferences" and that input.
4. When `status.self === null`, disable every control and show a muted paragraph "Log the node in to change its preferences.". When the change dialog closes, clear the draft so the form shows the node's values again.
5. `node-controls.tsx` exports `NodeControls({ hostId, status })`; `loggedIn` is `status.self !== null`. A `Card` titled "Node" holds `<form aria-label="Log in">` with an `Input` labelled "Auth key" (`type="password"`, `autoComplete="off"`), an `Input` labelled "Hostname (optional)", an `Input` labelled "Tags (comma-separated)" and a submit `Button` reading "Log in" (logged out) or "Log in again" (logged in), disabled while the key is blank. Submitting opens `TailscaleChange` with action `tailscale.node.login` and an input of `authKey` (trimmed), plus `hostname` only when non-blank, `advertiseTags` only when at least one tag (split on `,`, trimmed, empties and repeats dropped), and `relogin: true` only when logged in. The title is "Log the node in to the tailnet" (logged out) or "Log the node in again". On submit clear the auth key field at once (the secret then lives only in the open change's input until it closes). When logged in, also render a `Button` "Log out" (variant `outline`) that opens `TailscaleChange` with action `tailscale.node.logout`, title "Log the node out of the tailnet" and input `{}`. The typed hostname confirmation and the `tailscale.link` guard override come from the change dialog.
6. `package-panel.tsx` exports `PackagePanel({ hostId, available })`: a `Card` titled "Package". When `available` is false, show the paragraph "Tailscale is not installed on this host, or tailscaled does not answer." and a `Button` "Install Tailscale" opening `TailscaleChange` with action `tailscale.package.install`, title "Install Tailscale", input `{}`. When true, show a `Button` "Update Tailscale" (outline) opening action `tailscale.package.update`, title "Update Tailscale", input `{}`; no install button. (The agent refuses an install over a present package with `tailscale.package_present` and an update of a held package with `tailscale.package_held`; the change dialog shows those refusals.)
7. Do not edit `types.ts`, `fixtures.ts` or `tailscale-change.tsx` (Task 5's files) or any frozen file.

- [ ] **Step 4: Run the tests to verify they pass**

```bash
export TMPDIR=/root/.cache/w4-tmp/13 GOTOOLCHAIN=local
timeout 300 pnpm -C web exec vitest run src/modules/tailscale/prefs-form.test.tsx src/modules/tailscale/node-controls.test.tsx src/modules/tailscale/package-panel.test.tsx
timeout 300 pnpm -C web lint
timeout 300 pnpm -C web typecheck
timeout 600 pnpm -C web test
timeout 600 go build ./... && timeout 300 go vet ./... && timeout 1500 go test -count=1 ./... && timeout 900 golangci-lint run --allow-serial-runners ./... && timeout 300 go mod tidy -diff
rm -rf /root/.cache/w4-tmp/13 && go clean -testcache
```

Expected: the three files PASS; lint, typecheck and the whole web suite pass; the Go lines are `ok` or `?`, `0 issues.`, and `go mod tidy -diff` prints nothing. Record the real output.

- [ ] **Step 5: Commit**

```bash
git add web/src/modules/tailscale/prefs-form.tsx web/src/modules/tailscale/prefs-form.test.tsx web/src/modules/tailscale/node-controls.tsx web/src/modules/tailscale/node-controls.test.tsx web/src/modules/tailscale/package-panel.tsx web/src/modules/tailscale/package-panel.test.tsx
git commit -m "feat(web): tailscale preferences, node and package controls"
```

### Task 14: tailscale: status resource

**Files:**
- Create: `internal/modules/tailscale/status.go`
- Test: `internal/modules/tailscale/status_test.go`

Read, do not edit: `internal/platform/tslocal/client.go` (`Status`, `PeerStatus`, `Prefs`), `internal/modules/docker/engine.go` (the shape of a resource constructor and its read), `internal/modules/tailscale/wire.go`, `deps.go`, `fake_local_test.go` and `testhost_test.go` (Task 8's files, which this task builds on).

**Interfaces:**
- Consumes: Contracts "Tailscale wire shapes" (`Status`, `Node`, `Peer`, `PrefsFields`), "Tailscale internals" (`ts`, `deps.local`, `localAPI`), "Tailscale test host" (`newTSHost`, `h.ts()`, `h.env`, `h.local`, `thHostname`); `tslocal.Status`, `tslocal.PeerStatus`, `tslocal.Prefs`, `tslocal.ErrUnavailable`; `errcode.TailscaleUnavailable`; `core.DefineResource`.
- Produces: Contracts "Tailscale constructors" row `func (t ts) statusResource() core.ResourceDef` (declares `tailscale.status`), and Contracts "Tailscale preferences" `func prefsFieldsOf(p *tslocal.Prefs) PrefsFields`, which Tasks 21, 22, 27 and 28 call. Private beyond the Contracts, in `status.go`: the read method `func (t ts) statusRead(ctx context.Context, env core.Env) (Status, error)`, `statusNode(p *tslocal.PeerStatus, fallback []netip.Addr) Node`, `statusPeer(p *tslocal.PeerStatus) Peer` and `statusLocalErr(ctx context.Context, err error) error`.

**Depends on:** Task 8

The test prefix is `st`. The resource is a pure read of tailscaled's local API through the fake of Task 8: no file, command or journal is touched, and no test waits on a clock.

- [ ] **Step 1: Write the failing tests**

Create `internal/modules/tailscale/status_test.go` with exactly this content.

```go
package tailscale

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform/tslocal"
)

func stBool(b bool) *bool    { return &b }
func stStr(s string) *string { return &s }

// stRoutes is a pointer to a non-nil list, the way a decoded "advertiseRoutes": [...] reads.
func stRoutes(r ...string) *[]string {
	if r == nil {
		r = []string{}
	}
	return &r
}

// stWant builds the seven preferences fields the status carries.
func stWant(running, accept, shields, ssh, advertiseExit bool, exit string, routes ...string) PrefsFields {
	return PrefsFields{
		WantRunning: stBool(running), AdvertiseRoutes: stRoutes(routes...), ExitNode: stStr(exit),
		AdvertiseExitNode: stBool(advertiseExit), AcceptRoutes: stBool(accept), ShieldsUp: stBool(shields), RunSSH: stBool(ssh),
	}
}

func stAddrs(s ...string) []netip.Addr {
	out := make([]netip.Addr, 0, len(s))
	for _, a := range s {
		out = append(out, netip.MustParseAddr(a))
	}
	return out
}

func stPrefixes(s ...string) []netip.Prefix {
	out := make([]netip.Prefix, 0, len(s))
	for _, p := range s {
		out = append(out, netip.MustParsePrefix(p))
	}
	return out
}

// stRead reads tailscale.status on the host and decodes the answer; raw is the JSON as the resource encoded it.
func stRead(t *testing.T, h *tsHost) (got Status, raw string, err error) {
	t.Helper()
	b, err := h.ts().statusResource().Read(context.Background(), h.env(""))
	if err != nil {
		return Status{}, "", err
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("the answer %s does not decode as a Status: %v", b, err)
	}
	return got, string(b), nil
}

func TestStResourceDefinition(t *testing.T) {
	def := newTSHost(t).ts().statusResource()
	if def.Name != "tailscale.status" || def.OutputType != reflect.TypeFor[Status]() {
		t.Errorf("resource %s with output %v, want tailscale.status with Status", def.Name, def.OutputType)
	}
}

// TestStLoggedInNode: the default host is a logged-in node with no peers; every list is an empty array and
// every preference is set, and the read touches nothing on the host.
func TestStLoggedInNode(t *testing.T) {
	h := newTSHost(t)
	got, raw, err := stRead(t, h)
	if err != nil {
		t.Fatal(err)
	}
	want := Status{
		Version: "1.104.1", BackendState: "Running",
		Self: &Node{ID: "n1", HostName: thHostname, DNSName: thHostname + ".example.ts.net", OS: "linux",
			Addresses: []string{"100.64.0.7"}, Tags: []string{}},
		Tailnet: "example.com", MagicDNSSuffix: "example.ts.net", PreferredDERP: "", RunSSH: false,
		Prefs: stWant(true, false, false, false, false, ""),
		Peers: []Peer{}, Health: []string{},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("status\n got %+v\nwant %+v", got, want)
	}
	for _, member := range []string{`"peers":[]`, `"health":[]`, `"tags":[]`, `"advertiseRoutes":[]`} {
		if !strings.Contains(raw, member) {
			t.Errorf("answer %s lacks %s", raw, member)
		}
	}
	if len(h.ran) != 0 || len(h.files) != 0 || len(h.local.Edits) != 0 || len(h.local.Starts) != 0 || h.local.Logouts != 0 ||
		len(h.journal.effects()) != 0 || h.local.CertCalls != nil {
		t.Errorf("the read changed the host: ran %v, files %d, edits %d, journal %v", h.ran, len(h.files), len(h.local.Edits), h.journal.effects())
	}
}

// TestStSelfPreferencesAndHealth: the home DERP region is the node's Relay, RunSSH comes from the preferences,
// addresses list IPv4 first, and the preferences are all seven fields.
func TestStSelfPreferencesAndHealth(t *testing.T) {
	h := newTSHost(t)
	self := h.local.Status.Self
	self.Relay = "fra"
	self.Tags = []string{"tag:web", "tag:db"}
	self.TailscaleIPs = stAddrs("fd7a:115c:a1e0::7", "100.64.0.7")
	h.local.Status.Health = []string{"Tailscale could not reach the DERP relay"}
	h.local.Prefs = &tslocal.Prefs{
		WantRunning: true, RouteAll: true, ShieldsUp: true, RunSSH: true, ExitNodeID: "nExit1",
		AdvertiseRoutes: stPrefixes("10.0.0.0/8", "0.0.0.0/0", "::/0"),
	}
	got, _, err := stRead(t, h)
	if err != nil {
		t.Fatal(err)
	}
	wantSelf := &Node{ID: "n1", HostName: thHostname, DNSName: thHostname + ".example.ts.net", OS: "linux",
		Addresses: []string{"100.64.0.7", "fd7a:115c:a1e0::7"}, Tags: []string{"tag:web", "tag:db"}}
	if !reflect.DeepEqual(got.Self, wantSelf) {
		t.Errorf("self = %+v, want %+v", got.Self, wantSelf)
	}
	if got.PreferredDERP != "fra" || !got.RunSSH {
		t.Errorf("preferredDerp %q runSSH %v, want fra and true", got.PreferredDERP, got.RunSSH)
	}
	if want := stWant(true, true, true, true, true, "nExit1", "10.0.0.0/8"); !reflect.DeepEqual(got.Prefs, want) {
		t.Errorf("prefs %+v, want %+v", got.Prefs, want)
	}
	if !reflect.DeepEqual(got.Health, []string{"Tailscale could not reach the DERP relay"}) {
		t.Errorf("health = %v", got.Health)
	}
}

// TestStLoggedOut: a node that needs a login has no self (a stale record of its last identity is not shown) and
// no tailnet.
func TestStLoggedOut(t *testing.T) {
	h := newTSHost(t)
	h.local.Status = &tslocal.Status{
		Version: "1.104.1", BackendState: "NeedsLogin", AuthURL: "https://login.tailscale.example/a/abc",
		Self: &tslocal.PeerStatus{ID: "stale", HostName: "old-name", DNSName: "old-name.example.ts.net."},
	}
	h.local.Prefs = &tslocal.Prefs{LoggedOut: true}
	got, raw, err := stRead(t, h)
	if err != nil {
		t.Fatal(err)
	}
	if got.BackendState != "NeedsLogin" || got.Self != nil || got.Tailnet != "" || got.MagicDNSSuffix != "" || got.PreferredDERP != "" {
		t.Errorf("status %+v, want NeedsLogin with no self, tailnet, suffix or region", got)
	}
	if !strings.Contains(raw, `"self":null`) {
		t.Errorf("answer %s lacks \"self\":null", raw)
	}
	if strings.Contains(raw, "login.tailscale.example") {
		t.Errorf("answer %s carries the login URL", raw)
	}
	if want := stWant(false, false, false, false, false, ""); !reflect.DeepEqual(got.Prefs, want) {
		t.Errorf("prefs %+v, want %+v", got.Prefs, want)
	}
}

func TestStSelfByBackendState(t *testing.T) {
	for _, tt := range []struct {
		state   string
		hasSelf bool
		want    bool
	}{
		{"NeedsLogin", true, false},
		{"NoState", true, false},
		{"Stopped", true, true},
		{"Starting", true, true},
		{"NeedsMachineAuth", true, true},
		{"Running", true, true},
		{"Running", false, false},
	} {
		h := newTSHost(t)
		h.local.Status.BackendState = tt.state
		if !tt.hasSelf {
			h.local.Status.Self = nil
		}
		got, _, err := stRead(t, h)
		if err != nil {
			t.Fatal(err)
		}
		if (got.Self != nil) != tt.want {
			t.Errorf("state %s with self %v: self = %+v, want present %v", tt.state, tt.hasSelf, got.Self, tt.want)
		}
	}
}

// TestStFallbacks: a status that leaves out the top-level suffix or the self addresses is completed from the
// current tailnet and the node's own address list.
func TestStFallbacks(t *testing.T) {
	h := newTSHost(t)
	h.local.Status.MagicDNSSuffix = ""
	h.local.Status.Self.TailscaleIPs = nil
	got, _, err := stRead(t, h)
	if err != nil {
		t.Fatal(err)
	}
	if got.MagicDNSSuffix != "example.ts.net" {
		t.Errorf("magicDNSSuffix = %q, want the current tailnet's example.ts.net", got.MagicDNSSuffix)
	}
	if got.Self == nil || !reflect.DeepEqual(got.Self.Addresses, []string{"100.64.0.7"}) {
		t.Errorf("self = %+v, want the status's address list", got.Self)
	}
}

// TestStPeers: peers are sorted by DNS name then ID; the connection is direct when an endpoint is in use, else a
// peer relay, else DERP, else none; a never-handshaking peer has no handshake time and a handshake time is UTC.
func TestStPeers(t *testing.T) {
	h := newTSHost(t)
	hs := time.Date(2026, 10, 31, 11, 59, 0, 0, time.FixedZone("plus1", 3600))
	h.local.Status.Peer = map[string]*tslocal.PeerStatus{
		"nodekey:9": {ID: "n9", HostName: "dup", DNSName: "e-dup.example.ts.net.", OS: "linux",
			TailscaleIPs: stAddrs("100.64.0.99"), Online: true},
		"nodekey:5": {ID: "n5", HostName: "dup", DNSName: "e-dup.example.ts.net.", OS: "linux",
			TailscaleIPs: stAddrs("100.64.0.55"), Online: true},
		"nodekey:d": {ID: "n4", HostName: "d-none", DNSName: "d-none.example.ts.net.", OS: "windows",
			TailscaleIPs: stAddrs("100.64.0.4"), Tags: []string{"tag:server"}},
		"nodekey:c": {ID: "n3", HostName: "c-relay", DNSName: "c-relay.example.ts.net.", OS: "macOS",
			TailscaleIPs: stAddrs("100.64.0.3"), PeerRelay: "198.51.100.9:3478:vni:7", Online: true},
		"nodekey:b": {ID: "n2", HostName: "b-direct", DNSName: "b-direct.example.ts.net.", OS: "linux",
			TailscaleIPs: stAddrs("fd7a:115c:a1e0::2", "100.64.0.2"), CurAddr: "203.0.113.5:41641", Relay: "nyc",
			Online: true, LastHandshake: hs},
		"nodekey:a": {ID: "n1x", HostName: "a-derp", DNSName: "a-derp.example.ts.net.", OS: "linux",
			TailscaleIPs: stAddrs("100.64.0.1"), Relay: "fra", Online: true, ExitNode: true, ExitNodeOption: true},
	}
	got, raw, err := stRead(t, h)
	if err != nil {
		t.Fatal(err)
	}
	want := []Peer{
		{Node: Node{ID: "n1x", HostName: "a-derp", DNSName: "a-derp.example.ts.net", OS: "linux", Addresses: []string{"100.64.0.1"}, Tags: []string{}},
			Online: true, Connection: "derp", Region: "fra", ExitNode: true, ExitNodeOption: true},
		{Node: Node{ID: "n2", HostName: "b-direct", DNSName: "b-direct.example.ts.net", OS: "linux", Addresses: []string{"100.64.0.2", "fd7a:115c:a1e0::2"}, Tags: []string{}},
			Online: true, Connection: "direct", Endpoint: "203.0.113.5:41641"},
		{Node: Node{ID: "n3", HostName: "c-relay", DNSName: "c-relay.example.ts.net", OS: "macOS", Addresses: []string{"100.64.0.3"}, Tags: []string{}},
			Online: true, Connection: "peer-relay"},
		{Node: Node{ID: "n4", HostName: "d-none", DNSName: "d-none.example.ts.net", OS: "windows", Addresses: []string{"100.64.0.4"}, Tags: []string{"tag:server"}},
			Connection: "none"},
		{Node: Node{ID: "n5", HostName: "dup", DNSName: "e-dup.example.ts.net", OS: "linux", Addresses: []string{"100.64.0.55"}, Tags: []string{}},
			Online: true, Connection: "none"},
		{Node: Node{ID: "n9", HostName: "dup", DNSName: "e-dup.example.ts.net", OS: "linux", Addresses: []string{"100.64.0.99"}, Tags: []string{}},
			Online: true, Connection: "none"},
	}
	if len(got.Peers) != len(want) {
		t.Fatalf("%d peers, want %d: %+v", len(got.Peers), len(want), got.Peers)
	}
	handshake := got.Peers[1].LastHandshake
	if handshake == nil || !handshake.Equal(hs) {
		t.Errorf("direct peer's handshake = %v, want %v", handshake, hs)
	}
	got.Peers[1].LastHandshake = nil
	for i := range want {
		if !reflect.DeepEqual(got.Peers[i], want[i]) {
			t.Errorf("peer %d\n got %+v\nwant %+v", i, got.Peers[i], want[i])
		}
	}
	if !strings.Contains(raw, `"lastHandshake":"2026-10-31T10:59:00Z"`) {
		t.Errorf("answer %s lacks the handshake as UTC", raw)
	}
	if strings.Count(raw, `"lastHandshake":null`) != len(want)-1 {
		t.Errorf("answer %s: want a null handshake for each of the %d peers that never handshook", raw, len(want)-1)
	}
}

func TestStPrefsFieldsOf(t *testing.T) {
	tests := []struct {
		name string
		in   tslocal.Prefs
		want PrefsFields
	}{
		{"zero", tslocal.Prefs{}, stWant(false, false, false, false, false, "")},
		{"flags", tslocal.Prefs{WantRunning: true, RouteAll: true, ShieldsUp: true, RunSSH: true},
			stWant(true, true, true, true, false, "")},
		{"exit node by id", tslocal.Prefs{ExitNodeID: "nExit1"}, stWant(false, false, false, false, false, "nExit1")},
		{"exit node by address", tslocal.Prefs{ExitNodeIP: netip.MustParseAddr("100.64.0.9")},
			stWant(false, false, false, false, false, "100.64.0.9")},
		{"the id wins over the address", tslocal.Prefs{ExitNodeID: "nExit1", ExitNodeIP: netip.MustParseAddr("100.64.0.9")},
			stWant(false, false, false, false, false, "nExit1")},
		{"routes are sorted, unique and without the default routes",
			tslocal.Prefs{AdvertiseRoutes: stPrefixes("10.2.0.0/16", "0.0.0.0/0", "10.1.0.0/16", "10.2.0.0/16", "::/0")},
			stWant(false, false, false, false, true, "", "10.1.0.0/16", "10.2.0.0/16")},
		{"one default route is not an exit node", tslocal.Prefs{AdvertiseRoutes: stPrefixes("0.0.0.0/0")},
			stWant(false, false, false, false, false, "")},
		{"the other default route alone either", tslocal.Prefs{AdvertiseRoutes: stPrefixes("::/0", "192.168.0.0/24")},
			stWant(false, false, false, false, false, "", "192.168.0.0/24")},
		{"both default routes and nothing else", tslocal.Prefs{AdvertiseRoutes: stPrefixes("::/0", "0.0.0.0/0")},
			stWant(false, false, false, false, true, "")},
		{"IPv4 sorts before IPv6", tslocal.Prefs{AdvertiseRoutes: stPrefixes("fd00::/8", "192.168.0.0/24", "10.0.0.0/8")},
			stWant(false, false, false, false, false, "", "10.0.0.0/8", "192.168.0.0/24", "fd00::/8")},
	}
	for _, tt := range tests {
		in := tt.in
		got := prefsFieldsOf(&in)
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s:\n got %+v\nwant %+v", tt.name, got, tt.want)
		}
		if got.AdvertiseRoutes == nil || *got.AdvertiseRoutes == nil {
			t.Errorf("%s: advertiseRoutes is nil, want a non-nil list", tt.name)
		}
	}
}

// TestStUnavailable: a tailscaled that does not answer, on either call, is tailscale.unavailable with the
// cause kept; a read cut off by its own context is that context's error.
func TestStUnavailable(t *testing.T) {
	for _, method := range []string{"Status", "Prefs"} {
		h := newTSHost(t)
		cause := tslocal.ErrUnavailable
		if method == "Prefs" {
			cause = &tslocal.APIError{StatusCode: 500, Message: "boom"}
		}
		h.local.Errs = map[string]error{method: cause}
		_, _, err := stRead(t, h)
		coded := errcode.From(err)
		if err == nil || coded.Code != errcode.TailscaleUnavailable || coded.Detail != "tailscaled does not answer on its socket" {
			t.Errorf("%s failing: err = %#v, want tailscale.unavailable with the reason", method, err)
			continue
		}
		if !errors.Is(err, cause) {
			t.Errorf("%s failing: the cause is not kept in the chain", method)
		}
	}

	h := newTSHost(t)
	h.local.Errs = map[string]error{"Status": context.Canceled}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := h.ts().statusResource().Read(ctx, h.env(""))
	if !errors.Is(err, context.Canceled) || errcode.From(err).Code == errcode.TailscaleUnavailable {
		t.Errorf("cancelled read = %v, want context.Canceled and not tailscale.unavailable", err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mkdir -p /root/.cache/w4-tmp/14 && export TMPDIR=/root/.cache/w4-tmp/14 GOTOOLCHAIN=local && timeout 300 go test -count=1 -run 'TestSt' ./internal/modules/tailscale/ 2>&1 | head -20`
Expected: FAIL to build, with errors such as `h.ts().statusResource undefined (type ts has no field or method statusResource)` and `undefined: prefsFieldsOf`. Quote the first lines in the report.

- [ ] **Step 3: Implement**

Create `internal/modules/tailscale/status.go`:

1. `func (t ts) statusResource() core.ResourceDef` returns `core.DefineResource(ID+".status", t.statusRead)`; it touches nothing while constructing.
2. `func (t ts) statusRead(ctx context.Context, env core.Env) (Status, error)`: call `t.d.local()` once, then `Status(ctx)`, then `Prefs(ctx)`, in that order, and nothing else (no `DERPMap`, no exec, no file). Each error goes through `statusLocalErr(ctx, err)`: when `ctx.Err() != nil` it returns `ctx.Err()` unchanged (a read cut off by its own context is not an unavailable tailscaled); otherwise it returns `errcode.Wrap(errcode.TailscaleUnavailable, err, "tailscaled does not answer on its socket")`, keeping the cause in the chain, as `ts.available` does.
3. Build the `Status` from the two answers:
   - `Version`, `BackendState` copied; `Tailnet` is `CurrentTailnet.Name` (`""` when `CurrentTailnet` is nil); `MagicDNSSuffix` is the status's `MagicDNSSuffix`, else `CurrentTailnet.MagicDNSSuffix`, else `""`; `Health` is a copy of the status's list, a non-nil empty slice when there is none.
   - `Self` is nil when the status has no `Self`, or when `BackendState` is `NeedsLogin` or `NoState` (a logged-out node must not show the identity it last held). Otherwise it is `statusNode(Self, s.TailscaleIPs)`, where the second argument is used as the address list only when the node's own `TailscaleIPs` is empty. `PreferredDERP` is `Self.Relay` when `Self` is shown, else `""`.
   - `statusNode` builds a `Node`: `ID`, `HostName`, `OS` copied; `DNSName` with one trailing `.` removed; `Addresses` the `String()` of each address, IPv4 addresses first and IPv6 after them, each family in the order tailscaled gave; `Tags` a copy, a non-nil empty slice when there is none. Every list of the answer is a non-nil slice, so the JSON never carries `null` for a list.
   - `RunSSH` and `Prefs` come from the `Prefs` answer: `RunSSH` is its `RunSSH`; `Prefs` is `prefsFieldsOf(prefs)`.
   - `Peers` is one `statusPeer` per `Status.Peer` value (skip a nil value), sorted by `DNSName`, then `ID` (`strings.Compare`; the sort must not depend on map order), and a non-nil empty slice when there are none. `statusPeer` is `Node` (as above) plus `Online`, `ExitNode`, `ExitNodeOption`; the connection by the mapping in the plan's Assumptions: `CurAddr` non-empty gives `Connection` `direct` and `Endpoint` `CurAddr`; else `PeerRelay` non-empty gives `peer-relay`; else `Relay` non-empty gives `derp` and `Region` `Relay`; else `none`. The fields not set by that rule stay `""`. `LastHandshake` is nil when the time is the zero time, else a pointer to it converted with `.UTC()`.
   - The login URL (`AuthURL`) and every other tailscaled field the wire types do not name are not copied.
4. `func prefsFieldsOf(p *tslocal.Prefs) PrefsFields` exactly as Contracts "Tailscale preferences" states it: all seven fields set; `exitNode` from `ExitNodeID` when non-empty, else `ExitNodeIP.String()` when the address is valid, else `""`; `advertiseRoutes` the entries other than `0.0.0.0/0` and `::/0`, sorted with `netip.Prefix.Compare`, without duplicates, as strings (the prefix as tailscaled gave it, not re-masked), a non-nil empty slice when there are none; `advertiseExitNode` true only when both default prefixes are in the list. It does not modify `p` or its slices.
5. Doc comments on `statusResource` and `prefsFieldsOf`; comments elsewhere only for a non-obvious why (the `NeedsLogin` rule). Error strings start `tailscale:` where this task creates one.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `export TMPDIR=/root/.cache/w4-tmp/14 GOTOOLCHAIN=local && timeout 300 go test -count=1 -run 'TestSt|TestTH|TestManifestIsASkeleton' -v ./internal/modules/tailscale/ 2>&1 | tail -40`
Expected: PASS for every test.

- [ ] **Step 5: Run the task's checks**

Run each with `export TMPDIR=/root/.cache/w4-tmp/14 GOTOOLCHAIN=local` and an explicit timeout, and record the real output:

```bash
timeout 300 go build ./...
timeout 300 go vet ./...
timeout 900 go test -count=1 ./...
timeout 600 go test -race -count=1 ./internal/modules/tailscale/
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
rm -rf /root/.cache/w4-tmp/14 && go clean -testcache
```

Expected: `go build` and `go vet` print nothing; every `go test` line is `ok` or `?`; the lint prints `0 issues.`; `go mod tidy -diff` prints nothing. The resource is not registered in the manifest here (Task 31 does that and runs `make schemas`), so no schema snapshot changes.

- [ ] **Step 6: Commit**

```bash
git add internal/modules/tailscale/status.go internal/modules/tailscale/status_test.go
git commit -m "feat(tailscale): add the status resource"
```

### Task 15: tailscale: latency resource

**Files:**
- Create: `internal/modules/tailscale/latency.go`
- Test: `internal/modules/tailscale/latency_test.go`

Read, do not edit: `internal/platform/tslocal/derp.go` (`DERPMap`, `DERPRegion`, `DERPNode`), `internal/modules/tailscale/stun/stun.go` (Task 3's `Probe`, wired as `deps.stun`), `internal/modules/tailscale/wire.go`, `deps.go` and the test host files of Task 8.

**Interfaces:**
- Consumes: Contracts "Tailscale wire shapes" (`Latency`, `RegionLatency`, `Measurement`), "Tailscale internals" (`ts`, `deps.local`, `deps.stun`, `deps.https`), "Tailscale test host" (`newTSHost`, `h.ts()`, `h.env`, `h.local.DERP`, `h.local.Errs`), "STUN client" (the signature of `deps.stun` is `stun.Probe`'s); `tslocal.DERPMap`, `tslocal.DERPRegion`, `tslocal.DERPNode`; `errcode.TailscaleUnavailable`; `core.DefineResource`.
- Produces: Contracts "Tailscale constructors" row `func (t ts) latencyResource() core.ResourceDef` (declares `tailscale.latency`). Private, in `latency.go`: `latencyRead`, `latencyStunServers(r *tslocal.DERPRegion) []netip.AddrPort`, `latencyProbeURLs(r *tslocal.DERPRegion) []string`, `latencyMedian(d []time.Duration) time.Duration` and the generic `latencyMeasure`, all named so no other task of the package declares them.

**Depends on:** Task 3, Task 8

The test prefix is `lt`. No test reaches a network: `deps.stun` and `deps.https` are replaced by a fake that answers from a script. The scripts are instant, so no test needs the fake clock; the one test that proves regions are probed together waits on a channel and gives up after five seconds of real time only when the code under test is wrong.

- [ ] **Step 1: Write the failing tests**

Create `internal/modules/tailscale/latency_test.go` with exactly this content.

```go
package tailscale

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform/tslocal"
)

var errLTDown = errors.New("connection refused")

// ltFake answers the STUN and HTTPS probes from a script. A target's answers are consumed in order; a negative
// duration is a failed probe, and a probe beyond the script's end fails. It records the calls per target.
type ltFake struct {
	mu        sync.Mutex
	answers   map[string][]time.Duration
	calls     map[string]int
	unbounded []string
	// first, when set, runs outside the lock on the first probe of each target; its error fails that probe.
	first func(target string) error
}

func ltNew() *ltFake {
	return &ltFake{answers: map[string][]time.Duration{}, calls: map[string]int{}}
}

// ltSteady is three answers of d.
func ltSteady(d time.Duration) []time.Duration { return []time.Duration{d, d, d} }

func (f *ltFake) probe(ctx context.Context, target string) (time.Duration, error) {
	f.mu.Lock()
	n := f.calls[target]
	f.calls[target]++
	list := f.answers[target]
	if dl, ok := ctx.Deadline(); !ok || time.Until(dl) > 3*time.Second {
		f.unbounded = append(f.unbounded, target)
	}
	f.mu.Unlock()
	if n == 0 && f.first != nil {
		if err := f.first(target); err != nil {
			return 0, err
		}
	}
	if n >= len(list) || list[n] < 0 {
		return 0, errLTDown
	}
	return list[n], nil
}

func (f *ltFake) stun(ctx context.Context, server netip.AddrPort) (time.Duration, error) {
	return f.probe(ctx, server.String())
}

func (f *ltFake) https(ctx context.Context, url string) (time.Duration, error) {
	return f.probe(ctx, url)
}

func (f *ltFake) callCount(target string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[target]
}

func ltNode(host, v4 string) *tslocal.DERPNode {
	return &tslocal.DERPNode{Name: host, HostName: host, IPv4: v4}
}

func ltRegion(id int, code, name string, nodes ...*tslocal.DERPNode) *tslocal.DERPRegion {
	return &tslocal.DERPRegion{RegionID: id, RegionCode: code, RegionName: name, Nodes: nodes}
}

func ltMap(regions ...*tslocal.DERPRegion) *tslocal.DERPMap {
	m := &tslocal.DERPMap{Regions: map[int]*tslocal.DERPRegion{}}
	for _, r := range regions {
		m.Regions[r.RegionID] = r
	}
	return m
}

func ltURL(host string) string { return "https://" + host + "/derp/latency-check" }

// ltRead reads tailscale.latency on the host with the fake as both probes, and requires every probe to have been
// bounded to three seconds.
func ltRead(t *testing.T, h *tsHost, f *ltFake) (Latency, string) {
	t.Helper()
	tt := h.ts()
	tt.d.stun, tt.d.https = f.stun, f.https
	b, err := tt.latencyResource().Read(context.Background(), h.env(""))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	var got Latency
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("the answer %s does not decode as a Latency: %v", b, err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.unbounded) > 0 {
		t.Errorf("probes of %v ran without a deadline of at most 3 s", f.unbounded)
	}
	return got, string(b)
}

func TestLtResourceDefinition(t *testing.T) {
	def := newTSHost(t).ts().latencyResource()
	if def.Name != "tailscale.latency" || def.OutputType != reflect.TypeFor[Latency]() {
		t.Errorf("resource %s with output %v, want tailscale.latency with Latency", def.Name, def.OutputType)
	}
}

func TestLtMedian(t *testing.T) {
	ms := time.Millisecond
	for _, tt := range []struct {
		in   []time.Duration
		want time.Duration
	}{
		{[]time.Duration{7 * ms}, 7 * ms},
		{[]time.Duration{30 * ms, 10 * ms, 20 * ms}, 20 * ms},
		{[]time.Duration{40 * ms, 20 * ms}, 30 * ms},
		{[]time.Duration{4 * ms, 1 * ms, 3 * ms, 2 * ms}, 2500 * time.Microsecond},
	} {
		in := slices.Clone(tt.in)
		if got := latencyMedian(in); got != tt.want {
			t.Errorf("latencyMedian(%v) = %v, want %v", tt.in, got, tt.want)
		}
		if !slices.Equal(in, tt.in) {
			t.Errorf("latencyMedian reordered its argument: %v", in)
		}
	}
}

func TestLtStunServers(t *testing.T) {
	tests := []struct {
		name  string
		nodes []*tslocal.DERPNode
		want  []string
	}{
		{"default port, IPv4 of every node before IPv6", []*tslocal.DERPNode{
			{Name: "1a", HostName: "a.example.net", IPv4: "192.0.2.1", IPv6: "2001:db8::1"},
			{Name: "1b", HostName: "b.example.net", IPv4: "192.0.2.2", STUNPort: 3479},
		}, []string{"192.0.2.1:3478", "192.0.2.2:3479", "[2001:db8::1]:3478"}},
		{"a disabled port, a nil node, an invalid address and a bad port are skipped", []*tslocal.DERPNode{
			nil,
			{Name: "1a", IPv4: "192.0.2.1", STUNPort: -1},
			{Name: "1b", IPv4: "not-an-ip", IPv6: "2001:db8::2"},
			{Name: "1c", IPv4: "192.0.2.3", STUNPort: 70000},
			{Name: "1d", IPv4: "192.0.2.4", STUNPort: 0},
		}, []string{"192.0.2.4:3478", "[2001:db8::2]:3478"}},
		{"a STUN-only node counts and a repeated address is listed once", []*tslocal.DERPNode{
			{Name: "1a", IPv4: "192.0.2.1", STUNOnly: true},
			{Name: "1b", IPv4: "192.0.2.1"},
		}, []string{"192.0.2.1:3478"}},
		{"no node", nil, nil},
		{"a node with no address", []*tslocal.DERPNode{{Name: "1a", HostName: "a.example.net"}}, nil},
	}
	for _, tt := range tests {
		var got []string
		for _, ap := range latencyStunServers(&tslocal.DERPRegion{Nodes: tt.nodes}) {
			got = append(got, ap.String())
		}
		if !slices.Equal(got, tt.want) {
			t.Errorf("%s: servers %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestLtProbeURLs(t *testing.T) {
	tests := []struct {
		name  string
		nodes []*tslocal.DERPNode
		want  []string
	}{
		{"node order, the default port left out", []*tslocal.DERPNode{
			{Name: "1a", HostName: "a.example.net"}, {Name: "1b", HostName: "b.example.net", DERPPort: 443},
		}, []string{ltURL("a.example.net"), ltURL("b.example.net")}},
		{"another port is part of the host", []*tslocal.DERPNode{
			{Name: "1a", HostName: "a.example.net", DERPPort: 8443},
		}, []string{"https://a.example.net:8443/derp/latency-check"}},
		{"STUN-only nodes, disabled DERP, nil nodes and nodes without a host name are skipped", []*tslocal.DERPNode{
			nil,
			{Name: "1a", HostName: "a.example.net", STUNOnly: true},
			{Name: "1b", HostName: "b.example.net", DERPPort: -1},
			{Name: "1c"},
			{Name: "1d", HostName: "d.example.net"},
		}, []string{ltURL("d.example.net")}},
		{"no node", nil, nil},
	}
	for _, tt := range tests {
		got := latencyProbeURLs(&tslocal.DERPRegion{Nodes: tt.nodes})
		if !slices.Equal(got, tt.want) {
			t.Errorf("%s: urls %v, want %v", tt.name, got, tt.want)
		}
	}
}

// TestLtMeasuresEachRegionTwice: each region has a STUN and an HTTPS measurement, each the median of three
// probes sent to one endpoint, and the read touches nothing on the host.
func TestLtMeasuresEachRegionTwice(t *testing.T) {
	h := newTSHost(t)
	h.local.DERP = ltMap(ltRegion(1, "nyc", "New York City", ltNode("derp1.example.net", "192.0.2.1")))
	f := ltNew()
	ms := time.Millisecond
	f.answers["192.0.2.1:3478"] = []time.Duration{30 * ms, 10 * ms, 20 * ms}
	f.answers[ltURL("derp1.example.net")] = []time.Duration{90 * ms, 45 * ms, 15 * ms}

	got, raw := ltRead(t, h, f)
	want := []RegionLatency{{
		RegionID: 1, Code: "nyc", Name: "New York City",
		STUN:  Measurement{Available: true, MedianMs: 20},
		HTTPS: Measurement{Available: true, MedianMs: 45},
	}}
	if !reflect.DeepEqual(got.Regions, want) {
		t.Errorf("regions\n got %+v\nwant %+v", got.Regions, want)
	}
	if n := f.callCount("192.0.2.1:3478"); n != 3 {
		t.Errorf("%d STUN probes, want 3", n)
	}
	if n := f.callCount(ltURL("derp1.example.net")); n != 3 {
		t.Errorf("%d HTTPS probes, want 3", n)
	}
	if !strings.Contains(raw, `"stun":{"available":true,"medianMs":20,"reason":""}`) {
		t.Errorf("answer %s lacks the STUN measurement in the wire shape", raw)
	}
	if len(h.ran) != 0 || len(h.files) != 0 || len(h.local.Edits) != 0 || len(h.journal.effects()) != 0 {
		t.Errorf("the read changed the host: ran %v, files %d", h.ran, len(h.files))
	}
}

// TestLtPartialAnswers: a probe that fails after the first answered is dropped and the median is of the
// answers; an endpoint whose first probe fails is given up without sending its other two probes.
func TestLtPartialAnswers(t *testing.T) {
	h := newTSHost(t)
	h.local.DERP = ltMap(ltRegion(1, "nyc", "New York City", ltNode("derp1.example.net", "192.0.2.1")))
	f := ltNew()
	ms := time.Millisecond
	f.answers["192.0.2.1:3478"] = []time.Duration{20 * ms, -1, 40 * ms}
	f.answers[ltURL("derp1.example.net")] = []time.Duration{-1, 10 * ms, 10 * ms}

	got, _ := ltRead(t, h, f)
	r := got.Regions[0]
	if want := (Measurement{Available: true, MedianMs: 30}); r.STUN != want {
		t.Errorf("STUN = %+v, want %+v", r.STUN, want)
	}
	if n := f.callCount("192.0.2.1:3478"); n != 3 {
		t.Errorf("%d STUN probes, want 3 (a later failure does not stop the endpoint)", n)
	}
	if want := (Measurement{Reason: "no HTTPS answer from 1 node(s): connection refused"}); r.HTTPS != want {
		t.Errorf("HTTPS = %+v, want %+v", r.HTTPS, want)
	}
	if n := f.callCount(ltURL("derp1.example.net")); n != 1 {
		t.Errorf("%d HTTPS probes, want 1 (the first failed)", n)
	}
}

// TestLtFallsBackToTheNextEndpoint: a region measures with the first endpoint that answers its first probe.
func TestLtFallsBackToTheNextEndpoint(t *testing.T) {
	h := newTSHost(t)
	h.local.DERP = ltMap(ltRegion(2, "fra", "Frankfurt", ltNode("a.example.net", "192.0.2.1"), ltNode("b.example.net", "192.0.2.2")))
	f := ltNew()
	f.answers["192.0.2.1:3478"] = []time.Duration{-1}
	f.answers["192.0.2.2:3478"] = ltSteady(25 * time.Millisecond)
	f.answers[ltURL("a.example.net")] = []time.Duration{-1}
	f.answers[ltURL("b.example.net")] = ltSteady(60 * time.Millisecond)

	got, _ := ltRead(t, h, f)
	r := got.Regions[0]
	if r.STUN != (Measurement{Available: true, MedianMs: 25}) || r.HTTPS != (Measurement{Available: true, MedianMs: 60}) {
		t.Errorf("measurements STUN %+v HTTPS %+v, want 25 ms and 60 ms from the second endpoints", r.STUN, r.HTTPS)
	}
	for target, want := range map[string]int{
		"192.0.2.1:3478": 1, "192.0.2.2:3478": 3, ltURL("a.example.net"): 1, ltURL("b.example.net"): 3,
	} {
		if n := f.callCount(target); n != want {
			t.Errorf("%d probes of %s, want %d", n, target, want)
		}
	}
}

// TestLtUnavailableReasons: a measurement that cannot be made says why, with a zero median.
func TestLtUnavailableReasons(t *testing.T) {
	h := newTSHost(t)
	h.local.DERP = ltMap(
		ltRegion(1, "nostun", "No STUN", &tslocal.DERPNode{Name: "1a", HostName: "a.example.net", IPv4: "192.0.2.1", STUNPort: -1}),
		ltRegion(2, "blocked", "UDP blocked", ltNode("b.example.net", "192.0.2.2"), ltNode("c.example.net", "192.0.2.3")),
		ltRegion(3, "stunonly", "STUN only", &tslocal.DERPNode{Name: "3a", HostName: "d.example.net", IPv4: "192.0.2.4", STUNOnly: true}),
		ltRegion(4, "empty", "No nodes"),
	)
	f := ltNew()
	f.answers[ltURL("a.example.net")] = ltSteady(5 * time.Millisecond)
	f.answers["192.0.2.4:3478"] = ltSteady(6 * time.Millisecond)

	got, _ := ltRead(t, h, f)
	want := []RegionLatency{
		{RegionID: 1, Code: "nostun", Name: "No STUN",
			STUN:  Measurement{Reason: "the region has no STUN endpoint"},
			HTTPS: Measurement{Available: true, MedianMs: 5}},
		{RegionID: 2, Code: "blocked", Name: "UDP blocked",
			STUN:  Measurement{Reason: "no STUN answer from 2 endpoint(s); UDP may be blocked"},
			HTTPS: Measurement{Reason: "no HTTPS answer from 2 node(s): connection refused"}},
		{RegionID: 3, Code: "stunonly", Name: "STUN only",
			STUN:  Measurement{Available: true, MedianMs: 6},
			HTTPS: Measurement{Reason: "the region has no DERP node"}},
		{RegionID: 4, Code: "empty", Name: "No nodes",
			STUN:  Measurement{Reason: "the region has no STUN endpoint"},
			HTTPS: Measurement{Reason: "the region has no DERP node"}},
	}
	if !reflect.DeepEqual(got.Regions, want) {
		t.Errorf("regions\n got %+v\nwant %+v", got.Regions, want)
	}
	if n := f.callCount("192.0.2.1:3478"); n != 0 {
		t.Errorf("%d STUN probes of a region whose STUN is disabled, want 0", n)
	}
}

// TestLtRegionsSortedAndNilsSkipped: regions come back by ID whatever the map order, and a nil entry of the map
// is not a region.
func TestLtRegionsSortedAndNilsSkipped(t *testing.T) {
	h := newTSHost(t)
	h.local.DERP = ltMap(
		ltRegion(3, "sin", "Singapore", ltNode("c.example.net", "192.0.2.3")),
		ltRegion(1, "nyc", "New York City", ltNode("a.example.net", "192.0.2.1")),
		ltRegion(2, "fra", "Frankfurt", ltNode("b.example.net", "192.0.2.2")),
	)
	h.local.DERP.Regions[9] = nil
	f := ltNew()
	for _, n := range []struct{ host, ip string }{{"a.example.net", "192.0.2.1"}, {"b.example.net", "192.0.2.2"}, {"c.example.net", "192.0.2.3"}} {
		f.answers[n.ip+":3478"] = ltSteady(time.Millisecond)
		f.answers[ltURL(n.host)] = ltSteady(2 * time.Millisecond)
	}
	got, _ := ltRead(t, h, f)
	var ids []int
	for _, r := range got.Regions {
		ids = append(ids, r.RegionID)
		if !r.STUN.Available || !r.HTTPS.Available {
			t.Errorf("region %d: %+v, want both measurements", r.RegionID, r)
		}
	}
	if !slices.Equal(ids, []int{1, 2, 3}) {
		t.Errorf("region ids %v, want [1 2 3]", ids)
	}
}

// TestLtEmptyDERPMap: a node without a network map has no regions, which is an answer, not an error.
func TestLtEmptyDERPMap(t *testing.T) {
	for name, m := range map[string]*tslocal.DERPMap{"none": nil, "empty": {}} {
		h := newTSHost(t)
		h.local.DERP = m
		got, raw := ltRead(t, h, ltNew())
		if got.Regions == nil || len(got.Regions) != 0 || raw != `{"regions":[]}` {
			t.Errorf("%s: answer %s, want {\"regions\":[]}", name, raw)
		}
	}
}

// TestLtDERPMapFailure: a tailscaled that does not answer is tailscale.unavailable with the cause kept; a read
// cut off by its own context is that context's error.
func TestLtDERPMapFailure(t *testing.T) {
	h := newTSHost(t)
	h.local.Errs = map[string]error{"DERPMap": tslocal.ErrUnavailable}
	_, err := h.ts().latencyResource().Read(context.Background(), h.env(""))
	coded := errcode.From(err)
	if err == nil || coded.Code != errcode.TailscaleUnavailable || coded.Detail != "tailscaled does not answer on its socket" || !errors.Is(err, tslocal.ErrUnavailable) {
		t.Errorf("err = %#v, want tailscale.unavailable with the reason and the cause", err)
	}

	h = newTSHost(t)
	h.local.DERP = ltMap(ltRegion(1, "nyc", "New York City", ltNode("a.example.net", "192.0.2.1")))
	ctx, cancel := context.WithCancel(context.Background())
	tt := h.ts()
	tt.d.stun = func(context.Context, netip.AddrPort) (time.Duration, error) { cancel(); return 0, errLTDown }
	tt.d.https = func(context.Context, string) (time.Duration, error) { return 0, errLTDown }
	if _, err := tt.latencyResource().Read(ctx, h.env("")); !errors.Is(err, context.Canceled) {
		t.Errorf("a read cancelled during the probes = %v, want context.Canceled", err)
	}
}

// TestLtRegionsRunTogether: three regions are probed at the same time. Each region's first STUN probe waits
// until all three have started, so regions probed one after another fail the wait.
func TestLtRegionsRunTogether(t *testing.T) {
	h := newTSHost(t)
	h.local.DERP = ltMap(
		ltRegion(1, "nyc", "New York City", ltNode("a.example.net", "192.0.2.1")),
		ltRegion(2, "fra", "Frankfurt", ltNode("b.example.net", "192.0.2.2")),
		ltRegion(3, "sin", "Singapore", ltNode("c.example.net", "192.0.2.3")),
	)
	f := ltNew()
	var arrived atomic.Int32
	ready := make(chan struct{})
	f.first = func(target string) error {
		if !strings.HasPrefix(target, "192.0.2.") {
			return nil
		}
		if arrived.Add(1) == 3 {
			close(ready)
		}
		select {
		case <-ready:
			return nil
		case <-time.After(5 * time.Second):
			return errors.New("the regions are probed one after another")
		}
	}
	for _, ip := range []string{"192.0.2.1", "192.0.2.2", "192.0.2.3"} {
		f.answers[ip+":3478"] = ltSteady(time.Millisecond)
	}
	got, _ := ltRead(t, h, f)
	for _, r := range got.Regions {
		if !r.STUN.Available {
			t.Errorf("region %d STUN = %+v, want available", r.RegionID, r.STUN)
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mkdir -p /root/.cache/w4-tmp/15 && export TMPDIR=/root/.cache/w4-tmp/15 GOTOOLCHAIN=local && timeout 300 go test -count=1 -run 'TestLt' ./internal/modules/tailscale/ 2>&1 | head -20`
Expected: FAIL to build, with errors such as `undefined: latencyMedian`, `undefined: latencyStunServers`, `undefined: latencyProbeURLs` and `h.ts().latencyResource undefined`. Quote the first lines in the report.

- [ ] **Step 3: Implement**

Create `internal/modules/tailscale/latency.go`:

1. Fixed values (spec 4.1): three probes per measurement, the median; the HTTPS path `/derp/latency-check`; STUN port 3478 when a node's `STUNPort` is 0. Choices of this task: each probe runs under `context.WithTimeout(ctx, 3*time.Second)` (real time; the probe functions honour the context), at most 8 measurements run at once. Name these as unexported constants.
2. `func (t ts) latencyResource() core.ResourceDef` returns `core.DefineResource(ID+".latency", t.latencyRead)`; it touches nothing while constructing.
3. `func (t ts) latencyRead(ctx context.Context, env core.Env) (Latency, error)`: read `t.d.local().DERPMap(ctx)`. On an error, return `ctx.Err()` when the context is done, else `errcode.Wrap(errcode.TailscaleUnavailable, err, "tailscaled does not answer on its socket")` (cause kept). A nil map, a map with no regions and nil map entries yield `Latency{Regions: []RegionLatency{}}` (the JSON is `{"regions":[]}`). For every other region build a `RegionLatency` with `RegionID`, `Code` (`RegionCode`) and `Name` (`RegionName`) and run its two measurements. Run the measurements as jobs on goroutines bounded by a semaphore of 8, each job writing only its own `Measurement` field of its own slice element (no shared lock needed); wait for all of them; when `ctx.Err() != nil` return it (a read cut off by its own context fails rather than reporting every region unavailable). Return the regions sorted by `RegionID`.
4. `latencyStunServers(r) []netip.AddrPort`: the region's STUN endpoints. Skip a nil node; for each node, port is `STUNPort`, or 3478 when it is 0; skip the node when `STUNPort` is -1 or outside 1 to 65535 (any other value). Parse `IPv4` and `IPv6` with `netip.ParseAddr`; an empty or unparsable address is skipped; an IPv4 field must hold an IPv4 address and an IPv6 field an IPv6 address (skip a mismatch). The result lists the IPv4 endpoints of all nodes in node order, then the IPv6 endpoints in node order, each address and port once. STUN-only nodes count. The result is nil when there are none.
5. `latencyProbeURLs(r) []string`: `https://<HostName>/derp/latency-check` per node, in node order, with `:<DERPPort>` after the host when `DERPPort` is neither 0 nor 443 (an IPv6 literal host is written in brackets). Skip a nil node, a `STUNOnly` node, a node with `DERPPort` below 0 and a node with an empty `HostName`. Nil when there are none.
6. `latencyMedian(d []time.Duration) time.Duration`: the median of a non-empty list without reordering the caller's slice (sort a copy); for an even count the mean of the two middle values.
7. `latencyMeasure[T any](ctx, targets []T, probe func(context.Context, T) (time.Duration, error)) (median time.Duration, answered bool, lastErr error)`: for each target in order, send the first probe under its own 3 s context; when it fails, remember the error, and go on to the next target without sending the other two. When it answers, send two more probes to the same target one after another (not at once), drop the ones that fail, and return `latencyMedian` of the answers. Stop at once when `ctx` is done. When no target answers, `answered` is false and `lastErr` is the last probe error.
8. A measurement's value: available with `MedianMs` the median as `float64(d) / float64(time.Millisecond)` and an empty `Reason`. When unavailable, `MedianMs` is 0 and `Reason` is exactly: `the region has no STUN endpoint` (no STUN server), `no STUN answer from <n> endpoint(s); UDP may be blocked` (n servers, none answered), `the region has no DERP node` (no URL), `no HTTPS answer from <n> node(s): <last error text>` (n URLs, none answered). The HTTPS probe is `t.d.https(ctx, url)`, the STUN probe `t.d.stun(ctx, server)`; nothing else in `deps` is used and no exec, file or journal is touched.
9. Doc comments on the exported-looking and non-obvious functions; one comment on why the first failed probe gives an endpoint up (a dead endpoint must not cost three timeouts).

- [ ] **Step 4: Run the tests to verify they pass**

Run: `export TMPDIR=/root/.cache/w4-tmp/15 GOTOOLCHAIN=local && timeout 300 go test -count=1 -run 'TestLt|TestTH|TestManifestIsASkeleton' -v ./internal/modules/tailscale/ 2>&1 | tail -40`
Expected: PASS for every test.

- [ ] **Step 5: Run the task's checks**

Run each with `export TMPDIR=/root/.cache/w4-tmp/15 GOTOOLCHAIN=local` and an explicit timeout, and record the real output:

```bash
timeout 300 go build ./...
timeout 300 go vet ./...
timeout 900 go test -count=1 ./...
timeout 600 go test -race -count=1 ./internal/modules/tailscale/...
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
rm -rf /root/.cache/w4-tmp/15 && go clean -testcache
```

Expected: `go build` and `go vet` print nothing; every `go test` line is `ok` or `?`; the lint prints `0 issues.`; `go mod tidy -diff` prints nothing. The resource is not registered here (Task 31 registers it and runs `make schemas`).

- [ ] **Step 6: Commit**

```bash
git add internal/modules/tailscale/latency.go internal/modules/tailscale/latency_test.go
git commit -m "feat(tailscale): add the latency resource"
```

### Task 16: tailscale: cert.get internal read

**Files:**
- Create: `internal/modules/tailscale/cert_get.go`
- Test: `internal/modules/tailscale/cert_get_test.go`

Read, do not edit: `internal/core/define.go` (`DefineInternalRead`, `Revealed`), `internal/modules/docker/inspect.go` (`containerRevealEnv`, the docker module's reveal constructor, the shape to follow), `internal/platform/tslocal/derp.go` (`CertPair`), `internal/modules/tailscale/wire.go`, `deps.go` and the test host files of Task 8.

**Interfaces:**
- Consumes: Contracts "Tailscale wire shapes" (`CertGetInput`, `CertPair`, whose `KeyPEM` carries `secret:"true"`), "Tailscale internals" (`ts`, `deps.local`, `localAPI.Status`, `localAPI.CertPair`), "Tailscale test host" (`newTSHost`, `h.ts()`, `h.env`, `h.local.Status`, `h.local.Cert`, `h.local.CertCalls`, `h.local.Errs`); `core.DefineInternalRead`, `core.Revealed`, `core.ActionSpec`; `tslocal.Status.CertDomains`; `errcode.TailscaleUnavailable`.
- Produces: Contracts "Tailscale constructors" row `func (t ts) certGet() core.ActionDef` (declares `tailscale.cert.get/v1`), the internal read the controller's certificate source dispatches (Contracts "Listen certificate source"). Private, in `cert_get.go`: `func (t ts) certRead(ctx context.Context, env core.Env, in CertGetInput) (core.Revealed[CertPair], error)`.

**Depends on:** Task 8

The test prefix is `ce`. The tests generate certificates with the standard library (ECDSA P-256, self-signed), so no file or network is used, and no command runs. Per Task 31's registration table the action declares no `MinScope`, no lock, no guard and no `Available`: it reports an unreachable tailscaled from its own handler.

- [ ] **Step 1: Write the failing tests**

Create `internal/modules/tailscale/cert_get_test.go` with exactly this content.

```go
package tailscale

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform/tslocal"
)

const ceName = thHostname + ".example.ts.net"

var ceNotAfter = time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)

// ceMakePair returns a self-signed certificate for names, valid until notAfter, and its EC private key, both PEM.
func ceMakePair(t *testing.T, notAfter time.Time, names ...string) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: names[0]}, DNSNames: names,
		NotBefore: notAfter.Add(-90 * 24 * time.Hour), NotAfter: notAfter,
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
}

// ceHost is a host whose tailscaled offers ceName as its certificate domain and holds a valid pair for it.
func ceHost(t *testing.T) (h *tsHost, certPEM, keyPEM []byte) {
	t.Helper()
	h = newTSHost(t)
	certPEM, keyPEM = ceMakePair(t, ceNotAfter, ceName)
	h.local.Status.CertDomains = []string{ceName}
	h.local.Cert = [2][]byte{certPEM, keyPEM}
	return h, certPEM, keyPEM
}

// ceReveal runs the action's reveal handler the way the agent's Engine.Reveal does and decodes its value.
func ceReveal(t *testing.T, h *tsHost) (identity string, out CertPair, raw json.RawMessage, err error) {
	t.Helper()
	identity, raw, err = h.ts().certGet().Handler.Reveal(context.Background(), h.env(""), json.RawMessage(`{}`))
	if err != nil {
		return identity, CertPair{}, nil, err
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("the value %s does not decode as a CertPair: %v", raw, err)
	}
	return identity, out, raw, nil
}

func TestCeCertGetDeclaration(t *testing.T) {
	def := newTSHost(t).ts().certGet()
	s := def.Spec
	if s.Name != "tailscale.cert.get" || s.Version != 1 || s.Class != core.ClassRead || s.Side != core.SideAgent ||
		!s.Internal || s.Reveal || s.MinScope != "" || len(s.LockKeys) != 0 || len(s.Guards) != 0 || s.Available != nil || s.Summary == "" {
		t.Errorf("spec %+v, want the internal read tailscale.cert.get v1 on the agent with no lock, guard, minimum scope or availability check", s)
	}
	if def.InputType != reflect.TypeFor[CertGetInput]() || def.OutputType != reflect.TypeFor[CertPair]() {
		t.Errorf("types %v and %v, want CertGetInput and CertPair", def.InputType, def.OutputType)
	}
	if def.Handler.Reveal == nil || def.Handler.Read != nil || def.Handler.Plan != nil || def.Handler.Apply != nil {
		t.Errorf("handler %+v, want the reveal handler only", def.Handler)
	}
}

// TestCeCertGetReturnsThePair: the answer is the node's name, both PEM texts as tailscaled gave them (the key
// not redacted: the reveal path applies none) and the certificate's expiry; the identity names the certificate
// and the read changes nothing on the host.
func TestCeCertGetReturnsThePair(t *testing.T) {
	h, certPEM, keyPEM := ceHost(t)
	identity, out, raw, err := ceReveal(t, h)
	if err != nil {
		t.Fatal(err)
	}
	if identity != "tailscale certificate for "+ceName {
		t.Errorf("identity = %q", identity)
	}
	if out.Name != ceName || out.CertPEM != string(certPEM) || out.KeyPEM != string(keyPEM) || !out.NotAfter.Equal(ceNotAfter) {
		t.Errorf("pair name %q notAfter %v (cert match %v, key match %v), want %s until %v with the PEM texts unchanged",
			out.Name, out.NotAfter, out.CertPEM == string(certPEM), out.KeyPEM == string(keyPEM), ceName, ceNotAfter)
	}
	if !strings.Contains(string(raw), `"notAfter":"2026-12-01T00:00:00Z"`) {
		t.Errorf("value %s lacks the expiry as UTC", raw)
	}
	if !reflect.DeepEqual(h.local.CertCalls, []string{ceName}) {
		t.Errorf("tailscaled was asked for %v, want only %s", h.local.CertCalls, ceName)
	}
	if len(h.ran) != 0 || len(h.files) != 0 || len(h.local.Edits) != 0 || len(h.local.Starts) != 0 || len(h.journal.effects()) != 0 {
		t.Errorf("the read changed the host: ran %v, files %d, journal %v", h.ran, len(h.files), h.journal.effects())
	}
}

// TestCeCertGetName: the first certificate domain is the node's name, written without a trailing dot, and only
// that one is requested.
func TestCeCertGetName(t *testing.T) {
	h, _, _ := ceHost(t)
	h.local.Status.CertDomains = []string{ceName + ".", "alias.example.ts.net"}
	_, out, _, err := ceReveal(t, h)
	if err != nil {
		t.Fatal(err)
	}
	if out.Name != ceName || !reflect.DeepEqual(h.local.CertCalls, []string{ceName}) {
		t.Errorf("name %q, asked for %v, want %s once", out.Name, h.local.CertCalls, ceName)
	}
}

// TestCeCertGetChain: the expiry is the leaf's, the first certificate of the chain, and the chain is returned
// whole.
func TestCeCertGetChain(t *testing.T) {
	h, leaf, key := ceHost(t)
	issuer, _ := ceMakePair(t, ceNotAfter.Add(365*24*time.Hour), "issuer.example.net")
	chain := append(append([]byte{}, leaf...), issuer...)
	h.local.Cert = [2][]byte{chain, key}
	_, out, _, err := ceReveal(t, h)
	if err != nil {
		t.Fatal(err)
	}
	if !out.NotAfter.Equal(ceNotAfter) || out.CertPEM != string(chain) {
		t.Errorf("notAfter %v, chain kept %v, want the leaf's expiry %v and the whole chain", out.NotAfter, out.CertPEM == string(chain), ceNotAfter)
	}
}

// TestCeCertGetRefusals: every refusal is tailscale.unavailable with a fixed client-safe detail, and none of the
// error text carries the private key.
func TestCeCertGetRefusals(t *testing.T) {
	const (
		noSocket = "tailscaled does not answer on its socket"
		noDomain = "tailscaled offers no certificate domain; enable HTTPS certificates for the tailnet and log the node in"
		noPair   = "tailscaled could not provide a certificate for the node's name"
		badPair  = "tailscaled returned a certificate pair that is not usable"
	)
	otherCert, otherKey := ceMakePair(t, ceNotAfter, "other.example.ts.net")
	tests := []struct {
		name   string
		setup  func(h *tsHost, certPEM, keyPEM []byte)
		detail string
		cause  error
		asked  int
	}{
		{"tailscaled does not answer", func(h *tsHost, _, _ []byte) {
			h.local.Errs = map[string]error{"Status": tslocal.ErrUnavailable}
		}, noSocket, tslocal.ErrUnavailable, 0},
		{"no certificate domain", func(h *tsHost, _, _ []byte) { h.local.Status.CertDomains = nil }, noDomain, nil, 0},
		{"an empty domain", func(h *tsHost, _, _ []byte) { h.local.Status.CertDomains = []string{"."} }, noDomain, nil, 0},
		{"tailscaled cannot obtain the pair", func(h *tsHost, _, _ []byte) {
			h.local.Errs = map[string]error{"CertPair": &tslocal.APIError{StatusCode: 500, Message: "HTTPS is not enabled"}}
		}, noPair, &tslocal.APIError{StatusCode: 500, Message: "HTTPS is not enabled"}, 1},
		{"the certificate is not PEM", func(h *tsHost, _, keyPEM []byte) {
			h.local.Cert = [2][]byte{[]byte("not a certificate"), keyPEM}
		}, badPair, nil, 1},
		{"the key is not the certificate's", func(h *tsHost, certPEM, _ []byte) {
			h.local.Cert = [2][]byte{certPEM, otherKey}
		}, badPair, nil, 1},
		{"the certificate is for another name", func(h *tsHost, _, _ []byte) {
			h.local.Cert = [2][]byte{otherCert, otherKey}
		}, badPair, nil, 1},
	}
	for _, tt := range tests {
		h, certPEM, keyPEM := ceHost(t)
		tt.setup(h, certPEM, keyPEM)
		_, _, _, err := ceReveal(t, h)
		coded := errcode.From(err)
		if err == nil || coded.Code != errcode.TailscaleUnavailable || coded.Detail != tt.detail {
			t.Errorf("%s: err = %#v, want tailscale.unavailable with %q", tt.name, err, tt.detail)
			continue
		}
		if tt.cause != nil && !errors.Is(err, tt.cause) && !reflect.DeepEqual(errors.Unwrap(err), tt.cause) {
			t.Errorf("%s: the cause %v is not kept in the chain of %v", tt.name, tt.cause, err)
		}
		if len(h.local.CertCalls) != tt.asked {
			t.Errorf("%s: tailscaled was asked for %d pairs, want %d", tt.name, len(h.local.CertCalls), tt.asked)
		}
		for _, secret := range [][]byte{keyPEM, otherKey} {
			body := strings.Split(string(secret), "\n")[1]
			if strings.Contains(err.Error(), body) || strings.Contains(coded.Detail, body) {
				t.Errorf("%s: the error text carries private key material: %v", tt.name, err)
			}
		}
	}
}

// TestCeCertGetCancelled: a read cut off by its own context is that context's error, not an unavailable
// tailscaled.
func TestCeCertGetCancelled(t *testing.T) {
	h, _, _ := ceHost(t)
	h.local.Errs = map[string]error{"Status": context.Canceled}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := h.ts().certGet().Handler.Reveal(ctx, h.env(""), json.RawMessage(`{}`))
	if !errors.Is(err, context.Canceled) || errcode.From(err).Code == errcode.TailscaleUnavailable {
		t.Errorf("err = %v, want context.Canceled and not tailscale.unavailable", err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mkdir -p /root/.cache/w4-tmp/16 && export TMPDIR=/root/.cache/w4-tmp/16 GOTOOLCHAIN=local && timeout 300 go test -count=1 -run 'TestCe' ./internal/modules/tailscale/ 2>&1 | head -20`
Expected: FAIL to build, with errors such as `h.ts().certGet undefined (type ts has no field or method certGet)`. Quote the first lines in the report.

- [ ] **Step 3: Implement**

Create `internal/modules/tailscale/cert_get.go`:

1. `func (t ts) certGet() core.ActionDef` returns `core.DefineInternalRead[CertGetInput, CertPair]` with `core.ActionSpec{Name: ID + ".cert.get", Summary: <one sentence: returns the TLS certificate pair for the node's MagicDNS name, for the controller's own use>, Version: 1, Side: core.SideAgent}` and `t.certRead`. No `LockKeys`, `Guards`, `MinScope` or `Available` (Contracts "Tailscale wire shapes", action table; Task 31's table pins this). It touches nothing while constructing.
2. `func (t ts) certRead(ctx context.Context, env core.Env, in CertGetInput) (core.Revealed[CertPair], error)`, in this order:
   - `t.d.local().Status(ctx)`. Every error goes through one private closure or function of this file that returns `ctx.Err()` when the context is done, else `errcode.Wrap(errcode.TailscaleUnavailable, err, <detail>)` with the cause kept; the details are the exact strings the tests use. For this call: `tailscaled does not answer on its socket`.
   - The name is the first element of the status's `CertDomains` with one trailing `.` removed. When there is none, or the first is empty after that, refuse `errcode.New(errcode.TailscaleUnavailable, "tailscaled offers no certificate domain; enable HTTPS certificates for the tailnet and log the node in")`. Only the first domain is requested.
   - `t.d.local().CertPair(ctx, name)`. An error becomes `tailscale.unavailable` with the detail `tailscaled could not provide a certificate for the node's name` and the cause kept (tailscaled's text stays in the hidden cause, because it can quote the account's HTTPS settings), unless the context is done.
   - Validate the pair before returning it: `tls.X509KeyPair(certPEM, keyPEM)` must succeed (this also proves the key belongs to the certificate); parse the first certificate of the chain with `x509.ParseCertificate`; and `cert.VerifyHostname(name)` must succeed. Any failure is `tailscale.unavailable` with the detail `tailscaled returned a certificate pair that is not usable`; the error carries no cause that could quote the key (wrap nothing from `tls.X509KeyPair`'s error beyond a `tailscale:` message naming the failed step), and neither the detail nor the error text contains any PEM text.
   - Return `core.Revealed[CertPair]{Identity: "tailscale certificate for " + name, Value: CertPair{Name: name, CertPEM: string(certPEM), KeyPEM: string(keyPEM), NotAfter: cert.NotAfter.UTC()}}`. The key is returned unredacted: the reveal path applies no redaction and the `secret` tag only marks the schema.
3. The handler logs nothing (not the name's pair, not the key), journals nothing, runs no command and writes no file. Do not check expiry against the clock: renewal is the controller's decision.
4. Doc comments on `certGet`; a comment only where the reason is not obvious (why the cause of a key-pair failure is not wrapped).

- [ ] **Step 4: Run the tests to verify they pass**

Run: `export TMPDIR=/root/.cache/w4-tmp/16 GOTOOLCHAIN=local && timeout 300 go test -count=1 -run 'TestCe|TestTH|TestManifestIsASkeleton' -v ./internal/modules/tailscale/ 2>&1 | tail -40`
Expected: PASS for every test.

- [ ] **Step 5: Run the task's checks**

Run each with `export TMPDIR=/root/.cache/w4-tmp/16 GOTOOLCHAIN=local` and an explicit timeout, and record the real output:

```bash
timeout 300 go build ./...
timeout 300 go vet ./...
timeout 900 go test -count=1 ./...
timeout 600 go test -race -count=1 ./internal/modules/tailscale/
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
rm -rf /root/.cache/w4-tmp/16 && go clean -testcache
```

Expected: `go build` and `go vet` print nothing; every `go test` line is `ok` or `?`; the lint prints `0 issues.`; `go mod tidy -diff` prints nothing. The action is not registered here (Task 31 registers it and commits `schemas/tailscale.cert.get/v1.json`), so no schema snapshot changes.

- [ ] **Step 6: Commit**

```bash
git add internal/modules/tailscale/cert_get.go internal/modules/tailscale/cert_get_test.go
git commit -m "feat(tailscale): add cert.get internal read"
```

### Task 17: tailscale: link guard facts and the plan check

**Files:**
- Create: `internal/modules/tailscale/linkguard.go`
- Test: `internal/modules/tailscale/linkguard_test.go`

**Interfaces:**
- Consumes: Contracts "Link guard rules" (Task 4): `link.Check`, `link.Flow`, `link.Prefs`, `link.Change`, `link.GuardID`, `link.Interface`, `link.IsTailscaleAddr`. Contracts "Tailscale internals" and "Tailscale test host" (Task 8): `ts`, `deps` (`d.local`, `d.link`), `linkHost`, `controllerUnit`, `newTSHost`, `h.ts()`, `h.env()`, `h.link` (`Sockets`, `Ifaces`, `Routes`, `Pid`), `h.units` (`States`), `h.local` (`Prefs`, `Errs`). `core.Guarded`, `core.GuardContext`, `core.Env.Call.GuardRefresh` (`internal/core/guard.go`, `contract.go`).
- Produces: Contracts "Tailscale shared plan helpers": `func (t ts) linkGuard(ctx context.Context, env core.Env, in core.Guarded, cur link.Prefs, c link.Change) (core.Guard, error)` and `func (t ts) currentLinkPrefs(ctx context.Context) (link.Prefs, *tslocal.Prefs, error)`, read by Tasks 21, 22 and 27. This task adds no method to `linkHost` and touches no file of Task 8; the unexported names it declares all start with `link` (`linkInternet4`, `linkInternet6`, `linkTailnet4`, `linkTailnet6`, `linkSSHComms`, `linkRaw`, `linkFacts` and the helpers named in Step 3), so no other task of the package collides with them.

**Depends on:** Task 4, Task 8

The guard is the agent's half of spec 4.1's `tailscale.link` guard: it reads this host's protected flows, decides for each which interface the kernel routes it through, and hands them to the pure rules of Task 4. It never calls the local API for flows (the local API lists none) and never changes the host.

- [ ] **Step 1: Write the failing test**

Create `internal/modules/tailscale/linkguard_test.go`:

```go
package tailscale

import (
	"context"
	"errors"
	"net/netip"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/tailscale/link"
	"github.com/darkraise/darkpanel/internal/platform/netstat"
	"github.com/darkraise/darkpanel/internal/platform/tslocal"
)

const (
	lgAgentPID      = 99
	lgControllerPID = 4242
	lgInternet4     = "198.51.100.1" // the stand-in for any internet address
)

// lgSock is a TCP socket of the table held by one process.
func lgSock(state netstat.State, local, remote, comm string, pid int) netstat.Socket {
	s := netstat.Socket{Proto: netstat.ProtoTCP, Family: netstat.IPv4, State: state, Local: netip.MustParseAddrPort(local)}
	if remote != "" {
		s.Remote = netip.MustParseAddrPort(remote)
	}
	if s.Local.Addr().Is6() {
		s.Family = netstat.IPv6
	}
	s.Processes = []netstat.Process{{PID: pid, Comm: comm}}
	return s
}

// lgSSH is a listening sshd and one accepted session.
func lgSSH(local, remote string) []netstat.Socket {
	return []netstat.Socket{
		lgSock(netstat.StateListen, "0.0.0.0:22", "", "sshd", 7),
		lgSock(netstat.StateEstablished, local, remote, "sshd-session", 8),
	}
}

// lgControllerSession is the controller's listener and one session it accepted.
func lgControllerSession(local, remote string) []netstat.Socket {
	return []netstat.Socket{
		lgSock(netstat.StateListen, local, "", "darkpanel", lgControllerPID),
		lgSock(netstat.StateEstablished, local, remote, "darkpanel", lgControllerPID),
	}
}

func lgIfaces() []netstat.Interface {
	return []netstat.Interface{
		{Index: 1, Name: "lo", Up: true, Running: true, Loopback: true, Addrs: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/8")}},
		{Index: 2, Name: "eth0", Up: true, Running: true, Addrs: []netip.Prefix{netip.MustParsePrefix("192.0.2.1/24")}},
		{Index: 3, Name: "tailscale0", Up: true, Running: true, Addrs: []netip.Prefix{netip.MustParsePrefix("100.64.0.1/32")}},
		{Index: 4, Name: "dp-panel", Up: true, Running: true, Addrs: []netip.Prefix{netip.MustParsePrefix("172.30.0.1/24")}},
	}
}

// lgWorld is what the fake host reports.
type lgWorld struct {
	sockets    []netstat.Socket
	routes     map[string]string // destination to the interface the kernel routes it through
	controller bool              // darkpanel-controller.service runs with lgControllerPID
}

// lgHost builds a host and a ts over w. A lookup of an address w.routes does
// not name has no route.
func lgHost(t *testing.T, w lgWorld) (*tsHost, ts) {
	t.Helper()
	h := newTSHost(t)
	h.link.Sockets = w.sockets
	h.link.Ifaces = lgIfaces()
	h.link.Pid = lgAgentPID
	h.link.Routes = map[netip.Addr]netstat.Route{}
	for dst, iface := range w.routes {
		h.link.Routes[netip.MustParseAddr(dst)] = netstat.Route{Type: "unicast", OutIface: iface}
	}
	if h.units.States == nil {
		h.units.States = map[string]core.UnitState{}
	}
	h.units.States[controllerUnit] = core.UnitState{LoadState: "not-found", ActiveState: "inactive", SubState: "dead"}
	if w.controller {
		h.units.States[controllerUnit] = core.UnitState{LoadState: "loaded", ActiveState: "active", SubState: "running", MainPID: lgControllerPID}
	}
	return h, h.ts()
}

func lgPrefs(mut func(*link.Prefs)) link.Prefs {
	p := link.Prefs{WantRunning: true}
	if mut != nil {
		mut(&p)
	}
	return p
}

// lgPrefsChange is a prefs change from cur that applies mut.
func lgPrefsChange(cur link.Prefs, mut func(*link.Prefs)) link.Change {
	after := cur
	mut(&after)
	return link.Change{Kind: "prefs", After: after}
}

func lgAddrs(s ...string) []netip.AddrPort {
	out := make([]netip.AddrPort, len(s))
	for i, a := range s {
		out[i] = netip.MustParseAddrPort(a)
	}
	return out
}

var lgLogout = link.Change{Kind: "logout"}

func TestLgLinkGuard(t *testing.T) {
	sshOverTailnet := lgSSH("100.64.0.1:22", "100.64.0.9:50000")
	sshOverEth0 := lgSSH("192.0.2.1:22", "203.0.113.7:50000")
	panelCtx := func(client string) *core.GuardContext {
		return &core.GuardContext{Complete: true, Client: &core.Peer{Addr: netip.MustParseAddr(client), Port: 51000},
			PanelListen: lgAddrs("0.0.0.0:8443")}
	}
	cases := []struct {
		name    string
		w       lgWorld
		in      core.Guarded
		refresh *core.GuardContext
		cur     link.Prefs
		change  link.Change
		wantOK  bool
		wantIn  []string // the message of a failing guard names these
	}{
		{name: "no protected flow", cur: lgPrefs(nil), change: lgLogout, wantOK: true},
		{
			name: "an ssh session over eth0 does not depend on Tailscale", cur: lgPrefs(nil), change: lgLogout, wantOK: true,
			w: lgWorld{sockets: sshOverEth0, routes: map[string]string{"203.0.113.7": "eth0"}},
		},
		{
			name: "an ssh session over the tailnet fails a logout", cur: lgPrefs(nil), change: lgLogout,
			w:      lgWorld{sockets: sshOverTailnet, routes: map[string]string{"100.64.0.9": "tailscale0"}},
			wantIn: []string{"ssh", "100.64.0.9"},
		},
		{
			name: "Tailscale SSH on fails a logout even with every flow on eth0", cur: lgPrefs(func(p *link.Prefs) { p.RunSSH = true }), change: lgLogout,
			w: lgWorld{sockets: sshOverEth0, routes: map[string]string{"203.0.113.7": "eth0"}},
		},
		{
			name: "a loopback ssh session is not a flow", cur: lgPrefs(nil), change: lgLogout, wantOK: true,
			w: lgWorld{sockets: lgSSH("127.0.0.1:22", "127.0.0.1:50000")},
		},
		{
			name: "stopping the node fails for a flow over the tailnet", cur: lgPrefs(nil),
			change: lgPrefsChange(lgPrefs(nil), func(p *link.Prefs) { p.WantRunning = false }),
			w:      lgWorld{sockets: sshOverTailnet, routes: map[string]string{"100.64.0.9": "tailscale0"}},
			wantIn: []string{"ssh"},
		},
		{
			name: "shields-up fails for an inbound flow over the tailnet", cur: lgPrefs(nil),
			change: lgPrefsChange(lgPrefs(nil), func(p *link.Prefs) { p.ShieldsUp = true }),
			w:      lgWorld{sockets: sshOverTailnet, routes: map[string]string{"100.64.0.9": "tailscale0"}},
			wantIn: []string{"ssh"},
		},
		{
			name: "shields-up passes for an inbound flow over eth0", cur: lgPrefs(nil), wantOK: true,
			change: lgPrefsChange(lgPrefs(nil), func(p *link.Prefs) { p.ShieldsUp = true }),
			w:      lgWorld{sockets: sshOverEth0, routes: map[string]string{"203.0.113.7": "eth0"}},
		},
		{
			name: "a controller session over the tailnet fails a logout", cur: lgPrefs(nil), change: lgLogout,
			w: lgWorld{controller: true, sockets: lgControllerSession("100.64.0.1:7443", "100.64.0.5:41000"),
				routes: map[string]string{"100.64.0.5": "tailscale0"}},
			wantIn: []string{"controller session", "100.64.0.5"},
		},
		{
			name: "a controller listener on a tailnet address fails a logout", cur: lgPrefs(nil), change: lgLogout,
			w: lgWorld{controller: true, sockets: []netstat.Socket{
				lgSock(netstat.StateListen, "100.64.0.1:7443", "", "darkpanel", lgControllerPID)}},
			wantIn: []string{"controller link"},
		},
		{
			name: "a controller listener on every address reached over eth0 passes", cur: lgPrefs(nil), change: lgLogout, wantOK: true,
			w: lgWorld{controller: true, sockets: []netstat.Socket{lgSock(netstat.StateListen, "0.0.0.0:443", "", "darkpanel", lgControllerPID)},
				routes: map[string]string{lgInternet4: "eth0"}},
		},
		{
			name: "a loopback controller listener is not a flow", cur: lgPrefs(nil), change: lgLogout, wantOK: true,
			w: lgWorld{controller: true, sockets: []netstat.Socket{lgSock(netstat.StateListen, "127.0.0.1:8080", "", "darkpanel", lgControllerPID)}},
		},
		{
			name: "an exit node change fails for a dual-stack listener whose IPv4 reply path is the exit node", cur: lgPrefs(func(p *link.Prefs) { p.ExitNode = "n1" }),
			change: lgPrefsChange(lgPrefs(func(p *link.Prefs) { p.ExitNode = "n1" }), func(p *link.Prefs) { p.ExitNode = "" }),
			w: lgWorld{controller: true, sockets: []netstat.Socket{lgSock(netstat.StateListen, "[::]:443", "", "darkpanel", lgControllerPID)},
				routes: map[string]string{lgInternet4: "tailscale0"}},
			wantIn: []string{"controller link"},
		},
		{
			name: "the agent's session over the tailnet fails a logout", cur: lgPrefs(nil), change: lgLogout,
			w: lgWorld{sockets: []netstat.Socket{lgSock(netstat.StateEstablished, "100.64.0.1:50000", "100.64.0.7:7443", "darkpanel", lgAgentPID)},
				routes: map[string]string{"100.64.0.7": "tailscale0"}},
			wantIn: []string{"agent link", "100.64.0.7"},
		},
		{
			name: "the agent's session over eth0 passes", cur: lgPrefs(nil), change: lgLogout, wantOK: true,
			w: lgWorld{sockets: []netstat.Socket{lgSock(netstat.StateEstablished, "192.0.2.1:50000", "203.0.113.9:7443", "darkpanel", lgAgentPID)},
				routes: map[string]string{"203.0.113.9": "eth0"}},
		},
		{
			name: "the panel client over the tailnet fails a logout", cur: lgPrefs(nil), change: lgLogout,
			w:      lgWorld{controller: true, routes: map[string]string{"100.64.0.9": "tailscale0"}},
			in:     core.Guarded{Guard: panelCtx("100.64.0.9")},
			wantIn: []string{"panel", "100.64.0.9"},
		},
		{
			name: "the panel client over eth0 passes", cur: lgPrefs(nil), change: lgLogout, wantOK: true,
			w:  lgWorld{controller: true, routes: map[string]string{"203.0.113.7": "eth0"}},
			in: core.Guarded{Guard: panelCtx("203.0.113.7")},
		},
		{
			name: "a panel client of a controller elsewhere is not a flow", cur: lgPrefs(nil), change: lgLogout, wantOK: true,
			w: lgWorld{routes: map[string]string{"100.64.0.9": "tailscale0"}},
			in: core.Guarded{Guard: &core.GuardContext{Complete: true, Client: &core.Peer{Addr: netip.MustParseAddr("100.64.0.9")},
				PanelListen: lgAddrs("10.9.9.9:8443")}},
		},
		{
			name: "an agent peer over the tailnet fails a logout", cur: lgPrefs(nil), change: lgLogout,
			w: lgWorld{controller: true, routes: map[string]string{"100.64.0.7": "tailscale0"}},
			in: core.Guarded{Guard: &core.GuardContext{Complete: true, AgentPeers: []netip.Addr{netip.MustParseAddr("100.64.0.7")},
				AgentListen: lgAddrs("100.64.0.1:7443")}},
			wantIn: []string{"agent peer", "100.64.0.7"},
		},
		{
			name: "an agent peer over eth0 passes", cur: lgPrefs(nil), change: lgLogout, wantOK: true,
			w: lgWorld{controller: true, routes: map[string]string{"192.0.2.77": "eth0"}},
			in: core.Guarded{Guard: &core.GuardContext{Complete: true, AgentPeers: []netip.Addr{netip.MustParseAddr("192.0.2.77")},
				AgentListen: lgAddrs("192.0.2.1:7443")}},
		},
		{
			name: "an incomplete topology fails whatever the change", cur: lgPrefs(nil), change: lgLogout,
			in: core.Guarded{Guard: &core.GuardContext{Complete: false}},
		},
		{
			name: "the refreshed topology is read at apply too", cur: lgPrefs(nil), change: lgLogout,
			w:       lgWorld{controller: true, routes: map[string]string{"100.64.0.9": "tailscale0"}},
			in:      core.Guarded{Guard: &core.GuardContext{Complete: true}},
			refresh: panelCtx("100.64.0.9"),
			wantIn:  []string{"panel"},
		},
		{
			name: "a flow with no route fails whatever the change", cur: lgPrefs(nil),
			change: lgPrefsChange(lgPrefs(nil), func(*link.Prefs) {}),
			w:      lgWorld{sockets: sshOverEth0},
		},
		{
			name: "accept routes off fails for a subnet route flow", cur: lgPrefs(func(p *link.Prefs) { p.AcceptRoutes = true }),
			change: lgPrefsChange(lgPrefs(func(p *link.Prefs) { p.AcceptRoutes = true }), func(p *link.Prefs) { p.AcceptRoutes = false }),
			w: lgWorld{sockets: lgSSH("192.0.2.1:22", "10.20.0.5:50000"),
				routes: map[string]string{"10.20.0.5": "tailscale0", lgInternet4: "eth0"}},
			wantIn: []string{"ssh", "10.20.0.5"},
		},
		{
			name: "accept routes off passes when the exit node carries the flow anyway", cur: lgPrefs(func(p *link.Prefs) { p.AcceptRoutes = true }), wantOK: true,
			change: lgPrefsChange(lgPrefs(func(p *link.Prefs) { p.AcceptRoutes = true }), func(p *link.Prefs) { p.AcceptRoutes = false }),
			w: lgWorld{sockets: lgSSH("192.0.2.1:22", "10.20.0.5:50000"),
				routes: map[string]string{"10.20.0.5": "tailscale0", lgInternet4: "tailscale0"}},
		},
		{
			name: "an exit node change fails for a flow over the default route", cur: lgPrefs(func(p *link.Prefs) { p.ExitNode = "n1" }),
			change: lgPrefsChange(lgPrefs(func(p *link.Prefs) { p.ExitNode = "n1" }), func(p *link.Prefs) { p.ExitNode = "" }),
			w:      lgWorld{sockets: sshOverEth0, routes: map[string]string{"203.0.113.7": "tailscale0", lgInternet4: "tailscale0"}},
			wantIn: []string{"ssh", "203.0.113.7"},
		},
		{
			name: "an exit node change passes for a flow over a subnet route", cur: lgPrefs(func(p *link.Prefs) { p.ExitNode = "n1" }), wantOK: true,
			change: lgPrefsChange(lgPrefs(func(p *link.Prefs) { p.ExitNode = "n1" }), func(p *link.Prefs) { p.ExitNode = "" }),
			w:      lgWorld{sockets: sshOverEth0, routes: map[string]string{"203.0.113.7": "tailscale0", lgInternet4: "eth0"}},
		},
		{
			name: "turning Tailscale SSH off passes with no guard context and the ssh session over eth0", wantOK: true,
			cur:    lgPrefs(func(p *link.Prefs) { p.RunSSH = true }),
			change: lgPrefsChange(lgPrefs(func(p *link.Prefs) { p.RunSSH = true }), func(p *link.Prefs) { p.RunSSH = false }),
			w:      lgWorld{sockets: sshOverEth0, routes: map[string]string{"203.0.113.7": "eth0"}},
		},
		{
			name:   "turning Tailscale SSH off fails with no guard context and the ssh session over the tailnet",
			cur:    lgPrefs(func(p *link.Prefs) { p.RunSSH = true }),
			change: lgPrefsChange(lgPrefs(func(p *link.Prefs) { p.RunSSH = true }), func(p *link.Prefs) { p.RunSSH = false }),
			w:      lgWorld{sockets: sshOverTailnet, routes: map[string]string{"100.64.0.9": "tailscale0"}},
		},
		{
			name: "turning Tailscale SSH off passes when one of two ssh sessions is not over the tailnet", wantOK: true,
			cur:    lgPrefs(func(p *link.Prefs) { p.RunSSH = true }),
			change: lgPrefsChange(lgPrefs(func(p *link.Prefs) { p.RunSSH = true }), func(p *link.Prefs) { p.RunSSH = false }),
			w: lgWorld{sockets: slices.Concat(sshOverTailnet, lgSSH("192.0.2.1:22", "203.0.113.7:50000")[1:]),
				routes: map[string]string{"100.64.0.9": "tailscale0", "203.0.113.7": "eth0"}},
		},
		{
			name: "turning Tailscale SSH off passes when the panel request came over eth0", wantOK: true,
			cur:    lgPrefs(func(p *link.Prefs) { p.RunSSH = true }),
			change: lgPrefsChange(lgPrefs(func(p *link.Prefs) { p.RunSSH = true }), func(p *link.Prefs) { p.RunSSH = false }),
			w: lgWorld{controller: true, sockets: sshOverTailnet,
				routes: map[string]string{"100.64.0.9": "tailscale0", "203.0.113.7": "eth0"}},
			in: core.Guarded{Guard: panelCtx("203.0.113.7")},
		},
		{
			name:   "turning Tailscale SSH off fails when the panel request came over the tailnet",
			cur:    lgPrefs(func(p *link.Prefs) { p.RunSSH = true }),
			change: lgPrefsChange(lgPrefs(func(p *link.Prefs) { p.RunSSH = true }), func(p *link.Prefs) { p.RunSSH = false }),
			w: lgWorld{controller: true, sockets: sshOverEth0,
				routes: map[string]string{"100.64.0.9": "tailscale0", "203.0.113.7": "eth0"}},
			in: core.Guarded{Guard: panelCtx("100.64.0.9")},
		},
		{
			name:   "turning Tailscale SSH off fails when the guard context names no client",
			cur:    lgPrefs(func(p *link.Prefs) { p.RunSSH = true }),
			change: lgPrefsChange(lgPrefs(func(p *link.Prefs) { p.RunSSH = true }), func(p *link.Prefs) { p.RunSSH = false }),
			w:      lgWorld{controller: true, sockets: sshOverEth0, routes: map[string]string{"203.0.113.7": "eth0"}},
			in:     core.Guarded{Guard: &core.GuardContext{Complete: true}},
		},
		{
			name: "turning Tailscale SSH off passes when the request came through an exposure", wantOK: true,
			cur:    lgPrefs(func(p *link.Prefs) { p.RunSSH = true }),
			change: lgPrefsChange(lgPrefs(func(p *link.Prefs) { p.RunSSH = true }), func(p *link.Prefs) { p.RunSSH = false }),
			w: lgWorld{controller: true, sockets: sshOverTailnet,
				routes: map[string]string{"100.64.0.9": "tailscale0", "172.30.0.2": "dp-panel"}},
			in: core.Guarded{Guard: &core.GuardContext{Complete: true,
				Client: &core.Peer{Addr: netip.MustParseAddr("172.30.0.2")},
				Via: &core.ProxyPath{ExposureID: "e1", Kind: "npm", ProxyAddr: netip.MustParseAddr("172.30.0.2"),
					ListenerAddr: netip.MustParseAddrPort("172.30.0.1:8080")}}},
		},
		{
			name: "turning Tailscale SSH off passes when the agent link of a remote controller is over eth0", wantOK: true,
			cur:    lgPrefs(func(p *link.Prefs) { p.RunSSH = true }),
			change: lgPrefsChange(lgPrefs(func(p *link.Prefs) { p.RunSSH = true }), func(p *link.Prefs) { p.RunSSH = false }),
			w: lgWorld{sockets: []netstat.Socket{lgSock(netstat.StateEstablished, "192.0.2.1:50000", "203.0.113.9:7443", "darkpanel", lgAgentPID)},
				routes: map[string]string{"203.0.113.9": "eth0"}},
			in: core.Guarded{Guard: &core.GuardContext{Complete: true, PanelListen: lgAddrs("10.9.9.9:443")}},
		},
		{
			name:   "turning Tailscale SSH off fails when the agent link of a remote controller is over the tailnet",
			cur:    lgPrefs(func(p *link.Prefs) { p.RunSSH = true }),
			change: lgPrefsChange(lgPrefs(func(p *link.Prefs) { p.RunSSH = true }), func(p *link.Prefs) { p.RunSSH = false }),
			w: lgWorld{sockets: slices.Concat(sshOverEth0, []netstat.Socket{lgSock(netstat.StateEstablished, "100.64.0.1:50000", "100.64.0.7:7443", "darkpanel", lgAgentPID)}),
				routes: map[string]string{"100.64.0.7": "tailscale0", "203.0.113.7": "eth0"}},
			in: core.Guarded{Guard: &core.GuardContext{Complete: true, PanelListen: lgAddrs("10.9.9.9:443")}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, tsv := lgHost(t, tc.w)
			env := h.env("")
			env.Call.GuardRefresh = tc.refresh
			g, err := tsv.linkGuard(context.Background(), env, tc.in, tc.cur, tc.change)
			if err != nil {
				t.Fatal(err)
			}
			if g.ID != link.GuardID || !g.Overridable || g.Overridden {
				t.Errorf("guard %+v, want %s, overridable, not overridden", g, link.GuardID)
			}
			if g.OK != tc.wantOK {
				t.Fatalf("guard OK %v (%s), want %v", g.OK, g.Message, tc.wantOK)
			}
			for _, want := range tc.wantIn {
				if !strings.Contains(g.Message, want) {
					t.Errorf("message %q does not name %q", g.Message, want)
				}
			}
		})
	}
}

func TestLgLinkGuardHonoursOverrides(t *testing.T) {
	w := lgWorld{sockets: lgSSH("100.64.0.1:22", "100.64.0.9:50000"), routes: map[string]string{"100.64.0.9": "tailscale0"}}
	h, tsv := lgHost(t, w)
	g, err := tsv.linkGuard(context.Background(), h.env(""), core.Guarded{Overrides: []string{link.GuardID}}, lgPrefs(nil), lgLogout)
	if err != nil {
		t.Fatal(err)
	}
	if !g.OK || !g.Overridden || !g.Overridable || !strings.Contains(g.Message, "100.64.0.9") {
		t.Errorf("overridden guard %+v, want OK, overridden, overridable and the failing flow still named", g)
	}

	h, tsv = lgHost(t, lgWorld{sockets: lgSSH("192.0.2.1:22", "203.0.113.7:50000"), routes: map[string]string{"203.0.113.7": "eth0"}})
	g, err = tsv.linkGuard(context.Background(), h.env(""), core.Guarded{Overrides: []string{link.GuardID}}, lgPrefs(nil), lgLogout)
	if err != nil {
		t.Fatal(err)
	}
	if !g.OK || g.Overridden {
		t.Errorf("passing guard %+v, want OK and not overridden", g)
	}

	h, tsv = lgHost(t, w)
	g, err = tsv.linkGuard(context.Background(), h.env(""), core.Guarded{Overrides: []string{"firewall.lockout"}}, lgPrefs(nil), lgLogout)
	if err != nil {
		t.Fatal(err)
	}
	if g.OK || g.Overridden {
		t.Errorf("guard %+v with another guard overridden, want it failing", g)
	}
}

func TestLgLinkGuardReadsTheControllerUnit(t *testing.T) {
	w := lgWorld{sockets: lgControllerSession("100.64.0.1:7443", "100.64.0.5:41000"), routes: map[string]string{"100.64.0.5": "tailscale0"}}
	// With the controller unit absent its pid is not a controller, so its sockets are no flows.
	h, tsv := lgHost(t, w)
	g, err := tsv.linkGuard(context.Background(), h.env(""), core.Guarded{}, lgPrefs(nil), lgLogout)
	if err != nil || !g.OK {
		t.Errorf("guard %+v, error %v, want it passing without a controller", g, err)
	}
	w.controller = true
	h, tsv = lgHost(t, w)
	if g, err = tsv.linkGuard(context.Background(), h.env(""), core.Guarded{}, lgPrefs(nil), lgLogout); err != nil || g.OK {
		t.Errorf("guard %+v, error %v, want it failing with the controller running", g, err)
	}
	h, tsv = lgHost(t, w)
	h.units.States[controllerUnit] = core.UnitState{LoadState: "loaded", ActiveState: "inactive", SubState: "dead", MainPID: lgControllerPID}
	if g, err = tsv.linkGuard(context.Background(), h.env(""), core.Guarded{}, lgPrefs(nil), lgLogout); err != nil || !g.OK {
		t.Errorf("guard %+v, error %v, want it passing with the controller stopped", g, err)
	}
}

func TestLgLinkGuardRefusesFactsItCannotRead(t *testing.T) {
	h, tsv := lgHost(t, lgWorld{})
	env := h.env("")
	env.Units = nil
	if _, err := tsv.linkGuard(context.Background(), env, core.Guarded{}, lgPrefs(nil), lgLogout); err == nil {
		t.Error("the guard passed without the unit client")
	}
}

func TestLgCurrentLinkPrefs(t *testing.T) {
	cases := []struct {
		name string
		raw  tslocal.Prefs
		want link.Prefs
	}{
		{
			name: "every preference",
			raw: tslocal.Prefs{
				WantRunning: true, RouteAll: true, ShieldsUp: true, RunSSH: true, ExitNodeID: "n1",
				AdvertiseRoutes: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/24"), netip.MustParsePrefix("0.0.0.0/0"), netip.MustParsePrefix("::/0")},
			},
			want: link.Prefs{
				WantRunning: true, AcceptRoutes: true, ShieldsUp: true, RunSSH: true, ExitNode: "n1", AdvertiseExitNode: true,
				AdvertiseRoutes: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/24")},
			},
		},
		{
			name: "an exit node by address",
			raw:  tslocal.Prefs{ExitNodeIP: netip.MustParseAddr("100.64.0.9")},
			want: link.Prefs{ExitNode: "100.64.0.9"},
		},
		{
			name: "only the IPv6 exit route advertises an exit node",
			raw:  tslocal.Prefs{AdvertiseRoutes: []netip.Prefix{netip.MustParsePrefix("::/0")}},
			want: link.Prefs{AdvertiseExitNode: true},
		},
		{name: "nothing set", raw: tslocal.Prefs{}, want: link.Prefs{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newTSHost(t)
			raw := tc.raw
			h.local.Prefs = &raw
			got, back, err := h.ts().currentLinkPrefs(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if back == nil || !reflect.DeepEqual(*back, raw) {
				t.Errorf("the tailscaled preferences it returned are %+v, want %+v", back, raw)
			}
			if !slices.Equal(got.AdvertiseRoutes, tc.want.AdvertiseRoutes) {
				t.Errorf("advertised routes %v, want %v", got.AdvertiseRoutes, tc.want.AdvertiseRoutes)
			}
			got.AdvertiseRoutes, tc.want.AdvertiseRoutes = nil, nil
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("prefs %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestLgCurrentLinkPrefsReportsAFailedRead(t *testing.T) {
	h := newTSHost(t)
	h.local.Errs = map[string]error{"Prefs": errors.New("socket closed")}
	_, _, err := h.ts().currentLinkPrefs(context.Background())
	if err == nil || !strings.Contains(err.Error(), "socket closed") || !strings.HasPrefix(err.Error(), "tailscale:") {
		t.Errorf("error %v, want a tailscale: error wrapping the cause", err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mkdir -p /root/.cache/w4-tmp/17 && export TMPDIR=/root/.cache/w4-tmp/17 GOTOOLCHAIN=local && timeout 300 go test -count=1 -run 'Lg' ./internal/modules/tailscale/`
Expected: FAIL to build, naming `linkGuard` or `currentLinkPrefs` as undefined (`tsv.linkGuard undefined (type ts has no field or method linkGuard)`).

- [ ] **Step 3: Implement**

Create `internal/modules/tailscale/linkguard.go` (package `tailscale`; doc comment on every function; error strings start `tailscale:`).

1. Values, all unexported and prefixed `link`: the stand-in internet addresses `198.51.100.1` and `2001:db8:ffff::1` (documentation addresses standing for "any internet address"); the stand-in tailnet peers `100.100.100.100` and `fd7a:115c:a1e0::1`; the SSH process names `sshd`, `sshd-session`, `sshd-auth` (OpenSSH keeps a session's socket in `sshd` before 9.8, in `sshd-session` after it, and authenticates in `sshd-auth` from 10.0). A comment on the process names says a process can set its own name, so a name match can only add a protected flow, never remove one.
2. `currentLinkPrefs(ctx)` reads `t.d.local().Prefs(ctx)`; a failure returns `tailscale: read the preferences: %w`. It maps `WantRunning`, `RouteAll` (to `AcceptRoutes`), `ShieldsUp` and `RunSSH` as they are; `ExitNode` is `ExitNodeID`, else `ExitNodeIP.String()` when that address is valid, else `""`; `AdvertiseExitNode` is true when any advertised prefix has zero bits (`0.0.0.0/0` or `::/0`); `AdvertiseRoutes` holds the other prefixes in their order. The second result is the `*tslocal.Prefs` it read.
3. `linkGuard` first reads facts, each failure returned wrapped, because a guard must never pass on facts it could not read:
   - `env.Units` nil is an error (`tailscale: the link guard needs the unit client`); `t.d.link.Sockets()`, `Interfaces()` and `PID()`; the host's addresses are every address of every interface, unmapped;
   - the local controller's process id: `env.Units.State(ctx, controllerUnit)`; its `MainPID` when `LoadState` is not `not-found` and `ActiveState` is not `inactive`, else 0 (a zero id matches no socket).
4. Guard contexts are `in.Guard` and `env.Call.GuardRefresh`, the non-nil ones, in that order. The **request context** is `in.Guard`, else the refresh context when `in.Guard` is nil; with both nil there is none. The controller **runs here** when its process id is not 0 or a `PanelListen` or `AgentListen` address of any context is a host address.
5. Flows before routing, in this order, each with label, direction, local and remote address (unmapped) and a request mark. An established TCP socket is **inbound** when the table has a listening TCP socket on the same local port whose address is unspecified or equals the socket's local address. A remote address that is loopback or one of the host's own addresses is skipped everywhere (such a flow never leaves the host).
   1. For each context whose `Complete` is false: one flow with label `controller link`, no addresses, and `Unknown` "the controller's topology is incomplete".
   2. Label `ssh`, inbound: each established inbound socket held by a process named in the SSH list. Request when there is no request context.
   3. Only when the controller runs here, for each context in order: if `Via` is set, label `panel`, inbound, local `Via.ListenerAddr`'s address, remote `Via.ProxyAddr` (a context whose `Via` has either address invalid adds nothing); otherwise, if `Client` has a valid address, label `panel`, inbound, remote the client, one flow for each `PanelListen` that can take the client's family (below). Both kinds are a request flow when the context is the request context. Then label `agent peer`, inbound, for each `AgentPeers` address and each `AgentListen` that can take its family, remote the peer. A context with `Via` set never adds a flow from its `Client`: that address is the proxy's.
   4. Only when the controller's process id is not 0: label `controller session`, inbound, each established inbound socket held by that process; label `controller link`, inbound, for each listening TCP socket held by that process whose address is not loopback: a listener bound to a Tailscale address is one flow with remote the stand-in tailnet peer of its family and no route lookup (it is reached only through `tailscale0`, and Step 3.7 gives it that interface); any other listener is one flow per stand-in internet address its family can take, remote that address.
   5. Label `agent link`, outbound: each established TCP socket held by the agent's own process id (`PID()`) that is not inbound. Request when a request context exists and the controller does not run here: the request then arrives over this session.
   6. With no request context, every `ssh` flow is a request flow.
6. "Can take the client's family" is the firewall guard's `listenFor` rule: a listener on a specified address that is not a host address takes nothing; an unspecified IPv6 listener takes an IPv4 client with destination `0.0.0.0` and an IPv6 client with destination `::`; otherwise the listener takes a client of its own family with destination the listener address. Do not import `internal/modules/firewall/guard` (its helpers are unexported and the firewall is frozen); write the small rule again here.
7. Two flows with the same direction, local and remote address are one: the first label wins and the request mark is the OR of the two. Unknown flows are deduplicated by label and reason. Then classify each flow, in order, into a `link.Flow`:
   - an Unknown flow keeps its reason;
   - a tailnet-bound listener flow gets `OutIface` `link.Interface` and `Default` false;
   - every other flow asks `t.d.link.RouteGet` for the route from `Local` to `Remote` (`Src` set only when `Local` is not the unspecified address); `netstat.ErrNoRoute` makes a stand-in-internet flow vanish (a host without a default route has nothing to protect there) and any other flow Unknown with "no route to <remote>"; any other lookup error is returned wrapped; an empty `OutIface` makes the flow Unknown with "the route to <remote> has no single output interface";
   - `Default`: the lookup answers a host route, so a default route is recognized by a probe. `Default` is true when `OutIface` is `link.Interface`, `Remote` is not a Tailscale address, and the stand-in internet address of `Remote`'s family, looked up from the same `Local`, also leaves through `link.Interface` (a stand-in-internet flow is its own probe). One probe is made for each (family, local address) pair per call. Exit-node traffic is then `Default`, a subnet route is not, and a host with both is `Default` (the safe side: it can only add a failure, which the operator may override).
8. The result is `link.Check(cur, c, flows)`. When it fails and `in.Overrides` contains `link.GuardID`, set `OK` and `Overridden` true and leave the message, so the plan still shows what was overridden.

- [ ] **Step 4: Run the test to verify it passes, then the gates**

Run: `mkdir -p /root/.cache/w4-tmp/17 && export TMPDIR=/root/.cache/w4-tmp/17 GOTOOLCHAIN=local && timeout 300 go test -count=1 -run 'Lg' ./internal/modules/tailscale/`
Expected: PASS.

Then every gate of Global Constraints, with the real output recorded:

```bash
mkdir -p /root/.cache/w4-tmp/17 && export TMPDIR=/root/.cache/w4-tmp/17 GOTOOLCHAIN=local
timeout 600 go test -count=1 -race ./internal/modules/tailscale/...
timeout 600 go build ./... && timeout 300 go vet ./...
timeout 1500 go test -count=1 ./...
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
rm -rf /root/.cache/w4-tmp/17 && go clean -testcache
```

Expected: every line `ok` or `?`; `go vet` and `go mod tidy -diff` print nothing; `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add internal/modules/tailscale/linkguard.go internal/modules/tailscale/linkguard_test.go
git commit -m "feat(tailscale): add the link guard's host facts"
```

### Task 18: tailnet: enroll keys used and retired by the sweep

**Files:**
- Create: `internal/modules/tailnet/join_sweep.go`
- Create: `internal/modules/tailnet/join_sweep_test.go`
- Modify: `internal/modules/tailnet/keysweep.go` (`sweep`)

**Interfaces:**
- Consumes: Contracts "Tailnet enroll keys" (Tasks 10, 18): the `tailnet_keys` rows of kind `enroll` that Task 10's `prepareJoin` writes (owner the host id, description `darkpanel-enroll-<host id>`, expiry 3600 s). Existing: `setKeyState`, `insertKey`, `loadKey`, `keyRow`, `formatStoreTime`, `parseStoreTime` (`store.go`), `swSetup`, `swFix`, `tnWith`, `newTNHost` (existing tests), `core_hosts` and `core_enroll_tokens` of the controller database (`internal/core/store/migrations/controller/0001_core.sql`, `0005_fleet.sql`), `core.HostEnrolled`.
- Produces: `func settleEnrollKeys(ctx context.Context, db *store.DB, now time.Time) error` (Contracts "Tailnet enroll keys"); `keysweep.go`'s `sweep` calls it first in every pass.

**Depends on:** Task 10

Spec 4.2 step 3: an enroll key whose host enrolled is `used` and nothing is deleted; one whose token expired unused, or whose host was deleted, is `retired` and the sweep deletes the key by description. The fleet code has no tailnet knowledge (ruling in Assumptions): the sweep reads `core_hosts` and `core_enroll_tokens` itself, at most five minutes late.

- [ ] **Step 1: Write the failing test**

Create `internal/modules/tailnet/join_sweep_test.go`:

```go
package tailnet

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/ext"
	"github.com/darkraise/darkpanel/internal/modules/tailnet/fake"
)

// jsHostID returns a 26-character host id that differs by n.
func jsHostID(n int) string { return fmt.Sprintf("01J9X4K8Q2M5N7P0R3S6T8W%03d", n) }

// jsHost records a core_hosts row in the given state.
func jsHost(t *testing.T, h *tnHost, id, state string) {
	t.Helper()
	ctx := context.Background()
	err := h.db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO core_hosts (id, name, is_local, created_at, state) VALUES (?, ?, 0, ?, ?)`,
			id, "host-"+id[len(id)-3:], formatStoreTime(h.clock.Now()), state)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

// jsToken records the enrollment token of a host, expiring at expires and
// consumed or not.
func jsToken(t *testing.T, h *tnHost, hostID string, expires time.Time, consumed bool) {
	t.Helper()
	ctx := context.Background()
	var consumedAt sql.NullString
	if consumed {
		consumedAt = sql.NullString{String: formatStoreTime(h.clock.Now()), Valid: true}
	}
	err := h.db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO core_enroll_tokens (host_id, hash, expires_at, consumed_at, created_at) VALUES (?, x'00', ?, ?, ?)`,
			hostID, formatStoreTime(expires), consumedAt, formatStoreTime(h.clock.Now()))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

// jsKey records the enroll key row of host, requested age ago with a one hour
// expiry, and returns its description.
func jsKey(t *testing.T, h *tnHost, host, kind, state string, age time.Duration) string {
	t.Helper()
	description := "darkpanel-enroll-" + host
	if kind == "change" {
		description = "darkpanel-" + host
	}
	now := h.clock.Now()
	err := h.db.Write(context.Background(), func(tx *sql.Tx) error {
		return insertKey(context.Background(), tx, keyRow{
			Description: description, Kind: kind, OwnerID: host, State: state,
			RequestedAt: now.Add(-age), UpdatedAt: now.Add(-age), ExpirySeconds: 3600, Tags: []string{"tag:ci"},
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	return description
}

func jsRow(t *testing.T, h *tnHost, description string) keyRow {
	t.Helper()
	r, ok, err := loadKey(context.Background(), h.db.Read(), description)
	if err != nil || !ok {
		t.Fatalf("row %s: found %v, error %v", description, ok, err)
	}
	return r
}

func TestJsSettleEnrollKeys(t *testing.T) {
	const (
		young = time.Minute
		// The enroll key lives one hour; a row older than that plus 15 minutes is over.
		window = 75 * time.Minute
	)
	cases := []struct {
		name  string
		state string        // the key row's state
		host  string        // the owner's state; "" records no core_hosts row
		token string        // "", "open", "expired", "now", "soon" or "consumed-expired"
		age   time.Duration // how long ago the key was requested
		want  string
	}{
		{"live key of an enrolled host is used", "live", "enrolled", "", young, "used"},
		{"uncertain key of an enrolled host is used", "uncertain", "enrolled", "", young, "used"},
		{"enrolled wins over an expired token", "live", "enrolled", "expired", young, "used"},
		{"enrolled wins over age", "live", "enrolled", "", 48 * time.Hour, "used"},
		{"live key of a pending host with an open token stays live", "live", "pending", "open", young, "live"},
		{"uncertain key of a pending host with an open token stays uncertain", "uncertain", "pending", "open", young, "uncertain"},
		{"owner row gone retires a live key", "live", "", "", young, "retired"},
		{"owner row gone retires an uncertain key", "uncertain", "", "", young, "retired"},
		{"expired unconsumed token retires the key", "live", "pending", "expired", young, "retired"},
		{"a token expiring now is expired", "live", "pending", "now", young, "retired"},
		{"a token expiring in a second is open", "live", "pending", "soon", young, "live"},
		{"a consumed token is not an unconsumed one", "live", "pending", "consumed-expired", young, "live"},
		{"no token and exactly the window old stays live", "live", "pending", "", window, "live"},
		{"no token and past the window retires the key", "live", "pending", "", window + time.Second, "retired"},
		{"requested row is left to the sweep", "requested", "", "", 48 * time.Hour, "requested"},
		{"used row is final", "used", "", "", 48 * time.Hour, "used"},
		{"retired row is final", "retired", "enrolled", "", young, "retired"},
		{"deleted row is final", "deleted", "enrolled", "", young, "deleted"},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newTNHost(t)
			now := h.clock.Now()
			owner := jsHostID(i)
			if tc.host != "" {
				jsHost(t, h, owner, tc.host)
			}
			switch tc.token {
			case "open":
				jsToken(t, h, owner, now.Add(30*time.Minute), false)
			case "expired":
				jsToken(t, h, owner, now.Add(-time.Second), false)
			case "now":
				jsToken(t, h, owner, now, false)
			case "soon":
				jsToken(t, h, owner, now.Add(time.Second), false)
			case "consumed-expired":
				jsToken(t, h, owner, now.Add(-time.Second), true)
			}
			description := jsKey(t, h, owner, "enroll", tc.state, tc.age)
			if err := settleEnrollKeys(context.Background(), h.db, now); err != nil {
				t.Fatal(err)
			}
			r := jsRow(t, h, description)
			if r.State != tc.want {
				t.Errorf("row %q, want %q", r.State, tc.want)
			}
			wantAt := now.Add(-tc.age)
			if r.State != tc.state {
				wantAt = now
			}
			if !r.UpdatedAt.Equal(wantAt) {
				t.Errorf("updated at %v, want %v", r.UpdatedAt, wantAt)
			}
			if err := settleEnrollKeys(context.Background(), h.db, now); err != nil {
				t.Fatal(err)
			}
			if again := jsRow(t, h, description); again.State != tc.want {
				t.Errorf("row %q after a second pass, want %q", again.State, tc.want)
			}
		})
	}
}

func TestJsSettleLeavesChangeKeysAlone(t *testing.T) {
	h := newTNHost(t)
	description := jsKey(t, h, jsHostID(90), "change", "live", time.Minute)
	if err := settleEnrollKeys(context.Background(), h.db, h.clock.Now()); err != nil {
		t.Fatal(err)
	}
	if r := jsRow(t, h, description); r.State != "live" {
		t.Errorf("a change key's row %q, want live", r.State)
	}
}

func TestJsSettleMovesEveryRow(t *testing.T) {
	h := newTNHost(t)
	enrolled, gone := jsHostID(91), jsHostID(92)
	jsHost(t, h, enrolled, "enrolled")
	used := jsKey(t, h, enrolled, "enroll", "live", time.Minute)
	retired := jsKey(t, h, gone, "enroll", "live", time.Minute)
	if err := settleEnrollKeys(context.Background(), h.db, h.clock.Now()); err != nil {
		t.Fatal(err)
	}
	if got := jsRow(t, h, used).State; got != "used" {
		t.Errorf("enrolled host's key %q, want used", got)
	}
	if got := jsRow(t, h, retired).State; got != "retired" {
		t.Errorf("missing host's key %q, want retired", got)
	}
}

func TestJsSweepSettlesBeforeItDeletes(t *testing.T) {
	f := swSetup(t)
	enrolled, gone := jsHostID(93), jsHostID(94)
	jsHost(t, f.h, enrolled, "enrolled")
	usedDesc := jsKey(t, f.h, enrolled, "enroll", "live", time.Minute)
	goneDesc := jsKey(t, f.h, gone, "enroll", "live", time.Minute)
	usedID, goneID := f.key(t, usedDesc), f.key(t, goneDesc)

	if err := f.t.sweep(context.Background(), f.deps, f.h.clock.Now(), false); err != nil {
		t.Fatal(err)
	}
	if !f.alive(usedID) {
		t.Error("the pass deleted the key of an enrolled host")
	}
	if got := jsRow(t, f.h, usedDesc).State; got != "used" {
		t.Errorf("enrolled host's row %q, want used", got)
	}
	if f.alive(goneID) {
		t.Error("the pass left the key of a host that no longer exists")
	}
	if got := jsRow(t, f.h, goneDesc).State; got != "deleted" {
		t.Errorf("missing host's row %q, want deleted", got)
	}
}

func TestJsSweepSettlesWithoutAnAccount(t *testing.T) {
	srv := fake.New(t)
	h := newTNHost(t)
	description := jsKey(t, h, jsHostID(95), "enroll", "live", time.Minute)
	deps := ext.Deps{DB: h.db, Sealer: coretest.Sealer{}, Clock: h.clock, Audit: &coretest.MemAudit{}, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := tnWith(srv).sweep(context.Background(), deps, h.clock.Now(), true); err != nil {
		t.Fatal(err)
	}
	if got := jsRow(t, h, description).State; got != "retired" {
		t.Errorf("row %q, want retired", got)
	}
	if n := len(srv.Cloud().Requests()); n != 0 {
		t.Errorf("%d provider requests without an account", n)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mkdir -p /root/.cache/w4-tmp/18 && export TMPDIR=/root/.cache/w4-tmp/18 GOTOOLCHAIN=local && timeout 300 go test -count=1 -run 'Js' ./internal/modules/tailnet/`
Expected: FAIL to build with `undefined: settleEnrollKeys`.

- [ ] **Step 3: Implement**

1. Create `join_sweep.go` with `settleEnrollKeys(ctx, db, now)` and an unexported constant for the 15-minute grace after a key's expiry. It runs in one `db.Write` transaction. It selects every `tailnet_keys` row with kind `enroll` and state `live` or `uncertain`, left-joined to `core_hosts` on `id = owner_id` and to `core_enroll_tokens` on `host_id = owner_id`, reads all rows, closes the cursor, and only then writes (the transaction has one connection, as `cleanupPending` in `store.go` notes).
2. For each row, in this order of precedence, decide:
   - the owner's `core_hosts.state` is `core.HostEnrolled` → `used`;
   - otherwise → `retired` when any of: the owner row does not exist; the owner has a `core_enroll_tokens` row that is unconsumed (`consumed_at` null) and whose `expires_at` is at or before `now` (compare as `formatStoreTime` text, as `fleet.Enroll` does); `now` is later than the row's `requested_at` plus `expiry_seconds` plus 15 minutes (a row exactly that old is not yet retired);
   - otherwise leave the row.
   A moved row is written with the existing `setKeyState(ctx, tx, description, state, "", now)`, which keeps `key_id`, so the sweep's own pass still knows the key. Rows in any other state, and rows of kind `change`, are never read. A second call changes nothing more.
3. In `keysweep.go`, split `sweep`: the existing body moves unchanged into a new method `sweepKeys(ctx, deps, now, orphans)`, and `sweep` becomes `settleErr := settleEnrollKeys(ctx, deps.DB, now)` followed by `return errors.Join(settleErr, t.sweepKeys(ctx, deps, now, orphans))`. Settling runs first and also when no account is connected (the early `return nil` of `sweepKeys` stays); a settle failure does not stop the provider pass. Because settling precedes the read of the rows, a key retired in this call is deleted in the same pass by the existing rules (`keySweepable` accepts a retired row inside its window). Leave `runKeySweep`, `Controller` and the constants alone; `sweep`'s doc comment gains one sentence saying it settles enroll keys first.

- [ ] **Step 4: Run the test to verify it passes, then the gates**

Run: `mkdir -p /root/.cache/w4-tmp/18 && export TMPDIR=/root/.cache/w4-tmp/18 GOTOOLCHAIN=local && timeout 300 go test -count=1 -run 'Js|Sw' ./internal/modules/tailnet/`
Expected: PASS, the existing `Sw` tests included.

Then every gate of Global Constraints, with the real output recorded:

```bash
mkdir -p /root/.cache/w4-tmp/18 && export TMPDIR=/root/.cache/w4-tmp/18 GOTOOLCHAIN=local
timeout 600 go test -count=1 -race ./internal/modules/tailnet/...
timeout 600 go build ./... && timeout 300 go vet ./...
timeout 1500 go test -count=1 ./...
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
rm -rf /root/.cache/w4-tmp/18 && go clean -testcache
```

Expected: every line `ok` or `?`; `go vet` and `go mod tidy -diff` print nothing; `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add internal/modules/tailnet/join_sweep.go internal/modules/tailnet/join_sweep_test.go internal/modules/tailnet/keysweep.go
git commit -m "feat(tailnet): settle enroll keys in the sweep"
```

### Task 19: web: tailscale page and routes

**Files:**
- Create: `web/src/modules/tailscale/tailscale-page.tsx`
- Modify: `web/src/modules/tailscale/routes.tsx` (replace the placeholder that renders nothing)
- Test: `web/src/modules/tailscale/tailscale-page.test.tsx`

**Interfaces:**
- Consumes: Contracts "Web: tailscale": `types.ts` (Task 5: `TAILSCALE_MODULE`, `TailscaleStatus`, `TailscalePeer`, `PrefsFields`); `StatusPanel({hostId})`, `PeersTable({peers})`, `LatencyPanel({hostId})` (Task 12); `PrefsForm({hostId, status, prefs})`, `NodeControls({hostId, status})`, `PackagePanel({hostId, available})` (Task 13), each a named export of `status-panel.tsx`, `peers-table.tsx`, `latency-panel.tsx`, `prefs-form.tsx`, `node-controls.tsx` and `package-panel.tsx` in the same directory. Existing: `readResource`, `ApiError`, `errorMessage` (`web/src/api/client.ts`); `TailnetChange({action, title, input, onClose})` (`web/src/modules/tailnet/tailnet-change.tsx`, applies a controller-side action through the change dialog); `ModuleRouteProps` (`web/src/modules/types.ts`); the Docker module's `routes.tsx` and `docker-page.tsx` as the shape to follow. `PrefsForm`'s `prefs` prop is `PrefsFields`, taken from `TailscaleStatus.prefs` (optional in the mirror).
- Produces: `TailscalePage({hostId})` (Contracts "Web: tailscale") and the module's route bundle (`export default function TailscaleRoutes({hostId}: ModuleRouteProps)`), which `web/src/modules/registry.ts` already loads lazily. Frozen files (`registry.ts`, `types.ts` of the registry, `app.tsx`) and `registry.test.tsx` are not edited.

**Depends on:** Task 12, Task 13

`/hosts/:hostId/tailscale` (spec 4.1 Web): status, peers, latency, preferences, and install or update, plus the "Join tailnet" button that applies `tailnet.node.join` (spec 4.2 step 5) when the host is not on a tailnet. The page reads the status once and gives the panels what they need; the panels, which have their own tests, are mocked here so this test pins only what the page decides.

- [ ] **Step 1: Write the failing test**

Create `web/src/modules/tailscale/tailscale-page.test.tsx`:

```tsx
import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { Route, Routes } from "react-router"
import { expect, test, vi } from "vitest"
import { axe } from "vitest-axe"
import { renderWithProviders, stubFetch, type StubHandler } from "@/test/utils"
import TailscaleRoutes from "./routes"
import type { TailscalePeer, TailscaleStatus } from "./types"

// The panels have their own tests. Here each one shows the props the page
// gives it.
vi.mock("./status-panel", () => ({
  StatusPanel: (props: { hostId: string }) => <p data-testid="status-panel">{JSON.stringify(props)}</p>,
}))
vi.mock("./peers-table", () => ({
  PeersTable: (props: { peers: unknown[] }) => <p data-testid="peers-table">{JSON.stringify(props)}</p>,
}))
vi.mock("./latency-panel", () => ({
  LatencyPanel: (props: { hostId: string }) => <p data-testid="latency-panel">{JSON.stringify(props)}</p>,
}))
vi.mock("./prefs-form", () => ({
  PrefsForm: (props: { hostId: string; status: unknown; prefs: unknown }) => <p data-testid="prefs-form">{JSON.stringify(props)}</p>,
}))
vi.mock("./node-controls", () => ({
  NodeControls: (props: { hostId: string; status: unknown }) => <p data-testid="node-controls">{JSON.stringify(props)}</p>,
}))
vi.mock("./package-panel", () => ({
  PackagePanel: (props: { hostId: string; available: boolean }) => <p data-testid="package-panel">{JSON.stringify(props)}</p>,
}))
vi.mock("@/modules/tailnet/tailnet-change", () => ({
  TailnetChange: ({ action, title, input, onClose }: { action: string; title: string; input: Record<string, unknown> | null; onClose: () => void }) =>
    input === null ? null : (
      <div role="dialog" aria-label={title}>
        <p data-testid="join-action">{action}</p>
        <p data-testid="join-input">{JSON.stringify(input)}</p>
        <button onClick={onClose}>Close the dialog</button>
      </div>
    ),
}))

const TP_STATUS = "GET /api/v1/hosts/h1/resources/tailscale/status"

function tpPeer(over: Partial<TailscalePeer> = {}): TailscalePeer {
  return {
    id: "n-peer", hostName: "db", dnsName: "db.tail1234.ts.net", os: "linux", addresses: ["100.64.0.7"], tags: [],
    online: true, connection: "direct", endpoint: "203.0.113.9:41641", region: "", exitNode: false, exitNodeOption: false,
    lastHandshake: null, ...over,
  }
}

function tpStatus(over: Partial<TailscaleStatus> = {}): TailscaleStatus {
  return {
    version: "1.104.1", backendState: "Running",
    self: { id: "n-self", hostName: "web", dnsName: "web.tail1234.ts.net", os: "linux", addresses: ["100.64.0.1"], tags: ["tag:server"] },
    tailnet: "example.com", magicDNSSuffix: "tail1234.ts.net", preferredDerp: "fra", runSSH: true,
    peers: [tpPeer()], health: [], ...over,
  }
}

const tpUnavailable: StubHandler = {
  status: 503,
  body: { type: "about:blank", title: "Tailscale unavailable", status: 503, detail: "tailscaled does not answer on its socket", code: "tailscale.unavailable" },
}

function tpRender(path: string, status: StubHandler = { body: tpStatus() }) {
  const calls = stubFetch({ [TP_STATUS]: status })
  const result = renderWithProviders(
    <Routes>
      <Route path="/hosts/:hostId/tailscale/*" element={<TailscaleRoutes hostId="h1" />} />
    </Routes>,
    { path },
  )
  return { calls, ...result }
}

function tpProps(testId: string): Record<string, unknown> {
  return JSON.parse(screen.getByTestId(testId).textContent ?? "") as Record<string, unknown>
}

test("the page opens on the status tab with the status panel and the peers", async () => {
  tpRender("/hosts/h1/tailscale")
  expect(await screen.findByTestId("status-panel")).toBeTruthy()
  expect(tpProps("status-panel")).toEqual({ hostId: "h1" })
  expect(tpProps("peers-table")).toEqual({ peers: tpStatus().peers })
  expect(within(screen.getByRole("tablist")).getAllByRole("tab").map((t) => t.textContent)).toEqual(["Status", "Latency", "Preferences", "Package"])
  expect(screen.getByRole("tab", { name: "Status", selected: true })).toBeTruthy()
  expect(screen.queryByTestId("latency-panel")).toBeNull()
  expect(screen.queryByRole("button", { name: "Join tailnet" })).toBeNull()
})

test("the Latency tab mounts the latency panel for the host", async () => {
  tpRender("/hosts/h1/tailscale")
  await userEvent.setup().click(await screen.findByRole("tab", { name: "Latency" }))
  expect(tpProps("latency-panel")).toEqual({ hostId: "h1" })
  expect(screen.queryByTestId("status-panel")).toBeNull()
})

test("the Package tab tells the panel that tailscaled answers", async () => {
  tpRender("/hosts/h1/tailscale")
  await userEvent.setup().click(await screen.findByRole("tab", { name: "Package" }))
  expect(tpProps("package-panel")).toEqual({ hostId: "h1", available: true })
})

test.each([
  ["the node's preferences", tpStatus({ prefs: { wantRunning: true, advertiseRoutes: ["10.0.0.0/24"], exitNode: "n-exit", advertiseExitNode: false, acceptRoutes: true, shieldsUp: false, runSSH: true } }),
    { wantRunning: true, advertiseRoutes: ["10.0.0.0/24"], exitNode: "n-exit", advertiseExitNode: false, acceptRoutes: true, shieldsUp: false, runSSH: true }],
  ["no preferences from an older agent", tpStatus({ backendState: "NeedsLogin", self: null, peers: [] }), {}],
])("the Preferences tab gives the form and the controls %s", async (_name, status, prefs) => {
  tpRender("/hosts/h1/tailscale", { body: status })
  await userEvent.setup().click(await screen.findByRole("tab", { name: "Preferences" }))
  expect(tpProps("prefs-form")).toEqual({ hostId: "h1", status, prefs })
  expect(tpProps("node-controls")).toEqual({ hostId: "h1", status })
})

test("a node that is logged out offers to join the tailnet and reads its status again when the dialog closes", async () => {
  const { calls } = tpRender("/hosts/h1/tailscale", { body: tpStatus({ backendState: "NeedsLogin", self: null, peers: [] }) })
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: "Join tailnet" }))
  expect(screen.getByTestId("join-action").textContent).toBe("tailnet.node.join")
  expect(JSON.parse(screen.getByTestId("join-input").textContent ?? "")).toEqual({ hostId: "h1" })
  const reads = () => calls.filter((c) => c.method === "GET" && c.path === "/api/v1/hosts/h1/resources/tailscale/status").length
  expect(reads()).toBe(1)
  await user.click(screen.getByRole("button", { name: "Close the dialog" }))
  expect(screen.queryByRole("dialog")).toBeNull()
  await waitFor(() => expect(reads()).toBe(2))
})

test("a node whose tailscaled does not answer shows why, offers the install and joins with the package", async () => {
  tpRender("/hosts/h1/tailscale", tpUnavailable)
  expect(await screen.findByText("Tailscale is not available on this host: tailscaled does not answer on its socket")).toBeTruthy()
  expect(tpProps("package-panel")).toEqual({ hostId: "h1", available: false })
  expect(screen.queryByRole("tablist")).toBeNull()
  await userEvent.setup().click(screen.getByRole("button", { name: "Join tailnet" }))
  expect(JSON.parse(screen.getByTestId("join-input").textContent ?? "")).toEqual({ hostId: "h1", install: true })
})

test("any other failed status read shows the server's reason and nothing else", async () => {
  tpRender("/hosts/h1/tailscale", {
    status: 409,
    body: { type: "about:blank", title: "Unavailable", status: 409, detail: "the tailscale module is unavailable on this host", code: "module.unavailable" },
  })
  expect((await screen.findByRole("alert")).textContent).toBe("the tailscale module is unavailable on this host")
  expect(screen.queryByTestId("package-panel")).toBeNull()
  expect(screen.queryByRole("button", { name: "Join tailnet" })).toBeNull()
})

test("an unknown subpage is answered", () => {
  tpRender("/hosts/h1/tailscale/nope")
  expect(screen.getByText("Page not found").textContent).toBe("Page not found")
})

test("the Tailscale page has no accessibility violations", async () => {
  const { container } = tpRender("/hosts/h1/tailscale", { body: tpStatus({ self: null }) })
  await screen.findByTestId("status-panel")
  expect(await axe(container)).toHaveNoViolations()
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mkdir -p /root/.cache/w4-tmp/19 && export TMPDIR=/root/.cache/w4-tmp/19 && timeout 300 pnpm -C web exec vitest run src/modules/tailscale/tailscale-page.test.tsx`
Expected: FAIL: the routes still render nothing, so `findByTestId("status-panel")` times out (and, once `routes.tsx` imports it, Vite cannot resolve `./tailscale-page`).

- [ ] **Step 3: Implement**

1. Create `tailscale-page.tsx` exporting `TailscalePage({hostId}: { hostId: string })` (the only export, so the react-refresh lint rule stays quiet; keep helpers unexported). It reads the status with `useQuery`, key `["resource", hostId, "tailscale.status"]` (the key shape every module uses, so the change dialogs' `["resource", hostId]` invalidation reaches it) and `readResource<TailscaleStatus>(hostId, TAILSCALE_MODULE, "status")`.
2. While pending: `Spinner` with `aria-label` "Loading Tailscale". On an `ApiError` whose `code` is `tailscale.unavailable`: a warning `Alert` with `role="status"` whose text is `Tailscale is not available on this host: ` followed by `errorMessage(error)`, the "Join tailnet" button, and `PackagePanel` with `available={false}`; no tabs. On any other error: a destructive `Alert` with `role="alert"` and `errorMessage(error)`, nothing else.
3. With a status: when `self` is `null` (logged out), the "Join tailnet" button above the tabs. Then `Tabs` (as in `docker-page.tsx`, `TabsList` `aria-label` "Tailscale", default `status`) with four tabs and contents:
   - "Status": `StatusPanel hostId`, then `PeersTable peers={status.peers}`;
   - "Latency": `LatencyPanel hostId` (a tab, so its probes run only when the operator opens it);
   - "Preferences": `PrefsForm hostId status prefs`, then `NodeControls hostId status`;
   - "Package": `PackagePanel hostId available` (true: tailscaled answered).
   `prefs` is `status.prefs ?? {}`: the agent sends the node's current preferences in the status (Contracts "Tailscale wire shapes", `Status.Prefs`); an answer without them gives `{}`, and `PrefsForm` then takes what it needs from `status`.
4. The "Join tailnet" button (`Button` variant `outline`) opens `TailnetChange` with `action` `tailnet.node.join`, `title` "Join the tailnet" and `input` `{ hostId }`, plus `install: true` only on the unavailable path (Assumptions: the button sets `install` from the host's availability). `input` is `null` while the dialog is closed. `onClose` closes it and invalidates `["resource", hostId]` so the status is read again.
5. Replace `routes.tsx` with the shape of the Docker module's: `export default function TailscaleRoutes({ hostId }: ModuleRouteProps)` returning `Routes` with an index route to `TailscalePage` and a `*` route showing `EmptyState` titled "Page not found" with the description "The Tailscale module has no page at this address.".

- [ ] **Step 4: Run the tests to verify they pass, then the gates**

Run: `mkdir -p /root/.cache/w4-tmp/19 && export TMPDIR=/root/.cache/w4-tmp/19 && timeout 300 pnpm -C web exec vitest run src/modules/tailscale/tailscale-page.test.tsx src/modules/registry.test.tsx`
Expected: PASS. `registry.test.tsx` renders every module's routes with an empty fetch stub; the Tailscale page now shows its error alert there, and the test still passes without being edited.

Then every web gate of Global Constraints, with the real output recorded:

```bash
mkdir -p /root/.cache/w4-tmp/19 && export TMPDIR=/root/.cache/w4-tmp/19
timeout 600 pnpm -C web lint
timeout 600 pnpm -C web typecheck
timeout 900 pnpm -C web test
rm -rf /root/.cache/w4-tmp/19
```

Expected: no lint or type errors; every test file passes.

- [ ] **Step 5: Commit**

```bash
git add web/src/modules/tailscale/tailscale-page.tsx web/src/modules/tailscale/tailscale-page.test.tsx web/src/modules/tailscale/routes.tsx
git commit -m "feat(web): add the Tailscale host page"
```

### Task 20: tailscale: repository files and package.install

**Files:**
- Create: `internal/modules/tailscale/repo.go`
- Create: `internal/modules/tailscale/package_install.go`
- Test: `internal/modules/tailscale/repo_test.go`
- Test: `internal/modules/tailscale/package_install_test.go`

**Interfaces:**
- Consumes: `internal/modules/tailscale/repo_keyring.gpg`, the Tailscale repository key that Task 1 committed in the module directory (this task embeds it and does not write it). Contracts "Tailscale internals" (Task 8): `ts`, `deps` (`osRelease`, `arch`, `readFile`), `osInfo`, `run`, `installedVersion`, `insertInstall`, `liveInstall`, `setInstallRemoved`, `lockKey`, `keyringPath`, `sourcesPath`, `ID`; Contracts "Tailscale wire shapes": `PackageInstallInput`, `PackageInstalled`; Contracts "Tailscale test host": `newTSHost`, `h.env`, `h.ts`, `h.reads`, `h.files`, `h.ran`, `h.script`, `h.db`. `platform.AptUpdateTailscale` (APT-1), `AptInstallTailscale` (APT-2), `AptRemoveTailscale` (APT-6) from `internal/platform/exec_rules.go` (frozen; already allowlisted). `core.DefineTask`, `core.TaskMutator`, `core.Undoer`, `core.Observer` (`internal/core/define.go`, `contract.go`; `internal/core/lifecycle/taskunit_test.go` shows the engine side).
- Produces: Contracts "Tailscale shared plan helpers": `func (t ts) repoFiles() (keyring, sources []byte, err error)`, `func (t ts) ensureRepo(ctx context.Context, env core.Env) (written []string, err error)`, `func aptInstall(ctx context.Context, env core.Env) error` (Task 26's `Join` calls `ensureRepo` and `aptInstall`); the action constructor `func (t ts) packageInstall() core.ActionDef` (the docker module's naming: the action name without its module, in camel case), which Task 31 registers; the effect resources `tailscale.package:tailscale` and `tailscale.file:<path>`. `ensureRepo` journals the files it writes only when `env.Effects` is not nil, so the installer, which has no change, can call it. New unexported names: `repoKeyring`, `keyringFingerprint`, `keyringFingerprints`, `repoFileResource`, `repoFileNoun`, `repoFile`, `repoStatus`, `repoFileDoc`, `repoCodenames`, `packageInstallHandler`, `packageInstallTimeout`, `installPackageResource`, `installPackageDoc`.

**Depends on:** Task 8

`tailscale.package.install` (spec 4.1): a compensable task action. It writes Tailscale's apt keyring and source list when absent, runs `apt-get` APT-1 then APT-2, and refuses a host that has the package (`tailscale.package_present`). Its undo removes the package it installed, and the repository files it wrote, and nothing else. No test here runs `apt-get`, `dpkg`, `systemctl` or touches `/etc` or `/usr/share`: every host effect goes through the test host's fakes.

- [ ] **Step 1: Write the failing tests**

Create `internal/modules/tailscale/repo_test.go`:

```go
package tailscale

import (
	"bytes"
	"context"
	"crypto/sha1" //nolint:gosec // G505: an OpenPGP v4 fingerprint is defined as a SHA-1
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// piTS is a ts on a host that runs the given OS and architecture.
func piTS(h *tsHost, id, codename, arch string) ts {
	t := h.ts()
	t.d.osRelease = func() (osInfo, error) { return osInfo{ID: id, Codename: codename}, nil }
	t.d.arch = arch
	return t
}

func piKeyring(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("repo_keyring.gpg")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func piSources(id, codename string) string {
	return "deb [signed-by=/usr/share/keyrings/tailscale-archive-keyring.gpg] https://pkgs.tailscale.com/stable/" + id + " " + codename + " main\n"
}

func TestPiRepoFiles(t *testing.T) {
	for _, tc := range []struct{ id, codename, arch string }{
		{"ubuntu", "jammy", "amd64"}, {"ubuntu", "noble", "arm64"}, {"debian", "bookworm", "amd64"}, {"debian", "trixie", "arm64"},
	} {
		t.Run(tc.id+" "+tc.codename+" "+tc.arch, func(t *testing.T) {
			keyring, sources, err := piTS(newTSHost(t), tc.id, tc.codename, tc.arch).repoFiles()
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(keyring, piKeyring(t)) {
				t.Error("the keyring is not the bytes of repo_keyring.gpg")
			}
			if string(sources) != piSources(tc.id, tc.codename) {
				t.Errorf("source list %q, want %q", sources, piSources(tc.id, tc.codename))
			}
		})
	}
}

func TestPiRepoFilesRefuseWhatTailscaleDoesNotSupport(t *testing.T) {
	for _, tc := range []struct{ id, codename, arch string }{
		{"ubuntu", "focal", "amd64"}, {"debian", "bullseye", "amd64"}, {"fedora", "noble", "amd64"}, {"ubuntu", "noble", "386"}, {"", "", "amd64"},
	} {
		_, _, err := piTS(newTSHost(t), tc.id, tc.codename, tc.arch).repoFiles()
		var coded *errcode.Error
		if !errors.As(err, &coded) || coded.Code != errcode.TailscaleUnavailable {
			t.Errorf("%s %s %s: error %v, want %s", tc.id, tc.codename, tc.arch, err, errcode.TailscaleUnavailable)
		}
	}
	h := newTSHost(t)
	tsv := h.ts()
	tsv.d.osRelease = func() (osInfo, error) { return osInfo{}, errors.New("no os-release") }
	if _, _, err := tsv.repoFiles(); err == nil || !strings.Contains(err.Error(), "no os-release") {
		t.Errorf("error %v, want the os-release failure", err)
	}
}

// piFingerprint is the OpenPGP v4 fingerprint of a public key packet body.
func piFingerprint(body []byte) string {
	h := sha1.New()                                                         //nolint:gosec // G401: see the import
	h.Write(binary.BigEndian.AppendUint16([]byte{0x99}, uint16(len(body)))) //nolint:gosec // G115: the test bodies are under 64 KiB
	h.Write(body)
	return strings.ToUpper(hex.EncodeToString(h.Sum(nil)))
}

func TestPiKeyringFingerprints(t *testing.T) {
	keyBody := append([]byte{4, 0x65, 0x00, 0x00, 0x01, 1}, bytes.Repeat([]byte{0xAB}, 20)...)
	subBody := append([]byte{4, 0x65, 0x00, 0x00, 0x02, 1}, bytes.Repeat([]byte{0xCD}, 300)...)
	v5Body := append([]byte{5}, bytes.Repeat([]byte{0x11}, 10)...)
	uid := []byte("Tailscale Inc. <info@tailscale.com>")
	oldHeader := func(tag byte, body []byte) []byte { // old format, two-byte length
		return append(binary.BigEndian.AppendUint16([]byte{0x80 | tag<<2 | 1}, uint16(len(body))), body...) //nolint:gosec // G115: the test bodies are under 64 KiB
	}
	newHeader := func(tag byte, body []byte) []byte { // new format, one-byte length when it fits
		if len(body) < 192 {
			return append([]byte{0xC0 | tag, byte(len(body))}, body...) //nolint:gosec // G115: shorter than 192
		}
		n := len(body) - 192
		return append([]byte{0xC0 | tag, byte(n>>8) + 192, byte(n)}, body...) //nolint:gosec // G115: the test bodies are under 16 KiB
	}
	cases := []struct {
		name string
		in   []byte
		want []string
		err  bool
	}{
		{"old format public key", oldHeader(6, keyBody), []string{piFingerprint(keyBody)}, false},
		{"new format public key", newHeader(6, keyBody), []string{piFingerprint(keyBody)}, false},
		{"a key, its user id and a long subkey in new format", slices.Concat(newHeader(6, keyBody), newHeader(13, uid), newHeader(14, subBody)),
			[]string{piFingerprint(keyBody), piFingerprint(subBody)}, false},
		{"a key and a subkey in old format", slices.Concat(oldHeader(6, keyBody), oldHeader(14, subBody)),
			[]string{piFingerprint(keyBody), piFingerprint(subBody)}, false},
		{"a version 5 key has no version 4 fingerprint", newHeader(6, v5Body), nil, false},
		{"an empty keyring", nil, nil, false},
		{"not a packet", []byte("-----BEGIN PGP PUBLIC KEY BLOCK-----"), nil, true},
		{"a packet longer than the data", newHeader(6, keyBody)[:10], nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := keyringFingerprints(tc.in)
			if (err != nil) != tc.err {
				t.Fatalf("error %v, want error %v", err, tc.err)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("fingerprints %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPiEmbeddedKeyringCarriesThePinnedFingerprint(t *testing.T) {
	if len(keyringFingerprint) != 40 || keyringFingerprint != strings.ToUpper(keyringFingerprint) {
		t.Fatalf("pinned fingerprint %q, want 40 upper-case hex digits", keyringFingerprint)
	}
	fps, err := keyringFingerprints(piKeyring(t))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(fps, keyringFingerprint) {
		t.Errorf("repo_keyring.gpg holds %v, none is the pinned %s", fps, keyringFingerprint)
	}
}

func TestPiRepoFilesRefuseAKeyringWithoutThePinnedFingerprint(t *testing.T) {
	orig := repoKeyring
	t.Cleanup(func() { repoKeyring = orig })
	repoKeyring = []byte{0xC0 | 6, 3, 4, 1, 2}
	_, _, err := piTS(newTSHost(t), "ubuntu", "noble", "amd64").repoFiles()
	if err == nil || !strings.Contains(err.Error(), "fingerprint") {
		t.Errorf("error %v, want a refusal naming the fingerprint", err)
	}
}

func TestPiEnsureRepo(t *testing.T) {
	want := map[string][]byte{keyringPath: nil, sourcesPath: []byte(piSources("ubuntu", "noble"))}
	cases := []struct {
		name        string
		reads       func(keyring []byte) map[string][]byte
		wantWritten []string
		conflict    string // the path the refusal names
	}{
		{"both absent", func([]byte) map[string][]byte { return nil }, []string{keyringPath, sourcesPath}, ""},
		{"both identical", func(k []byte) map[string][]byte {
			return map[string][]byte{keyringPath: k, sourcesPath: want[sourcesPath]}
		}, nil, ""},
		{"keyring identical, sources absent", func(k []byte) map[string][]byte { return map[string][]byte{keyringPath: k} }, []string{sourcesPath}, ""},
		{"keyring absent, sources identical", func([]byte) map[string][]byte { return map[string][]byte{sourcesPath: want[sourcesPath]} }, []string{keyringPath}, ""},
		{"keyring differs", func([]byte) map[string][]byte { return map[string][]byte{keyringPath: []byte("another key")} }, nil, keyringPath},
		{"sources differ, keyring absent", func([]byte) map[string][]byte {
			return map[string][]byte{sourcesPath: []byte("deb https://example.com/ stable main\n")}
		}, nil, sourcesPath},
		{"sources differ by a newline", func(k []byte) map[string][]byte {
			return map[string][]byte{keyringPath: k, sourcesPath: bytes.TrimSuffix(want[sourcesPath], []byte("\n"))}
		}, nil, sourcesPath},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newTSHost(t)
			keyring := piKeyring(t)
			for p, b := range tc.reads(keyring) {
				h.reads[p] = b
			}
			written, err := piTS(h, "ubuntu", "noble", "amd64").ensureRepo(context.Background(), h.env(""))
			if tc.conflict != "" {
				var coded *errcode.Error
				if !errors.As(err, &coded) || coded.Code != errcode.TailscaleRepoConflict || !strings.Contains(coded.Detail, tc.conflict) || !core.IsDefinite(err) {
					t.Fatalf("error %v, want a definite %s naming %s", err, errcode.TailscaleRepoConflict, tc.conflict)
				}
				if len(h.files) != 0 {
					t.Errorf("a conflict still wrote %v", h.files)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(written, tc.wantWritten) {
				t.Errorf("written %v, want %v", written, tc.wantWritten)
			}
			if len(h.files) != len(tc.wantWritten) {
				t.Errorf("files written %d, want %d", len(h.files), len(tc.wantWritten))
			}
			for _, p := range tc.wantWritten {
				wantBytes := want[p]
				if p == keyringPath {
					wantBytes = keyring
				}
				if !bytes.Equal(h.files[p], wantBytes) {
					t.Errorf("%s holds %q", p, h.files[p])
				}
			}
		})
	}
}

func TestPiEnsureRepoReportsAnUnreadableFile(t *testing.T) {
	h := newTSHost(t)
	tsv := piTS(h, "ubuntu", "noble", "amd64")
	tsv.d.readFile = func(string) ([]byte, error) { return nil, fs.ErrPermission }
	_, err := tsv.ensureRepo(context.Background(), h.env(""))
	var coded *errcode.Error
	if err == nil || errors.As(err, &coded) || !errors.Is(err, fs.ErrPermission) || len(h.files) != 0 {
		t.Errorf("error %v, files %v, want the read failure and nothing written", err, h.files)
	}
}

func TestPiEnsureRepoJournalsWhatItWrites(t *testing.T) {
	h := newTSHost(t)
	h.reads[keyringPath] = piKeyring(t)
	env := h.env("c1")
	if _, err := piTS(h, "ubuntu", "noble", "amd64").ensureRepo(context.Background(), env); err != nil {
		t.Fatal(err)
	}
	effects, err := env.Effects.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(effects) != 1 {
		t.Fatalf("%d effects, want one for the source list only", len(effects))
	}
	e := effects[0]
	if e.Kind != core.EffectCreate || e.Resource != "tailscale.file:"+sourcesPath || e.State != core.EffectMade || !e.Local || !e.InverseLocal ||
		string(e.Before.State) != "null" || e.Post == nil || !strings.Contains(string(e.Post.State), `"sha256"`) {
		t.Errorf("effect %+v, want a made, local creation of the source list from null", e)
	}
}
```

Create `internal/modules/tailscale/package_install_test.go`:

```go
package tailscale

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform"
)

const (
	piApt     = "/usr/bin/apt-get"
	piVersion = "1.104.1"
	piStatus  = "/var/lib/dpkg/status"
)

// piDpkg is a dpkg status file that lists the tailscale package as installed.
func piDpkg(version string) []byte {
	return []byte("Package: bash\nStatus: install ok installed\nVersion: 5.2.21\n\n" +
		"Package: tailscale\nStatus: install ok installed\nPriority: optional\nArchitecture: amd64\nVersion: " + version + "\nDescription: The easiest, most secure way to use WireGuard\n\n")
}

func piVec(path string, args []string) string {
	return strings.Join(append([]string{path}, args...), " ")
}

// piRunner passes every vector to the host's runner and runs after for each
// one that succeeded, so a test can make apt-get change the dpkg status file
// and records the deadline of the context each call got.
type piRunner struct {
	next      core.Runner
	after     func(cmd core.Cmd)
	deadlines *[]time.Duration
}

func (r piRunner) Run(ctx context.Context, cmd core.Cmd) (core.CmdResult, error) {
	if dl, ok := ctx.Deadline(); ok && r.deadlines != nil {
		*r.deadlines = append(*r.deadlines, time.Until(dl))
	}
	res, err := r.next.Run(ctx, cmd)
	if err == nil && res.ExitCode == 0 && r.after != nil {
		r.after(cmd)
	}
	return res, err
}

// piSetup is a supported host without tailscale. apt-get install of the
// package puts version in the dpkg status file and apt-get remove takes it out.
type piSetup struct {
	h         *tsHost
	t         ts
	env       core.Env
	deadlines []time.Duration
}

func piNewSetup(t *testing.T) *piSetup {
	t.Helper()
	h := newTSHost(t)
	s := &piSetup{h: h, t: piTS(h, "ubuntu", "noble", "amd64")}
	h.reads[piStatus] = []byte("Package: bash\nStatus: install ok installed\nVersion: 5.2.21\n\n")
	s.env = h.env("c1")
	s.env.Exec = piRunner{next: s.env.Exec, deadlines: &s.deadlines, after: func(cmd core.Cmd) {
		switch {
		case cmd.Path == piApt && slices.Equal(cmd.Args, platform.AptInstallTailscale()):
			h.reads[piStatus] = piDpkg(piVersion)
		case cmd.Path == piApt && slices.Equal(cmd.Args, platform.AptRemoveTailscale()):
			h.reads[piStatus] = []byte("Package: bash\nStatus: install ok installed\nVersion: 5.2.21\n\n")
		}
	}}
	return s
}

func (s *piSetup) installed() { s.h.reads[piStatus] = piDpkg(piVersion) }

func (s *piSetup) ranApt() [][]string {
	var out [][]string
	for _, v := range s.h.ran {
		if v[0] == piApt {
			out = append(out, v[1:])
		}
	}
	return out
}

func piRaw(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func piRefused(t *testing.T, err error, code errcode.Code) {
	t.Helper()
	var coded *errcode.Error
	if !errors.As(err, &coded) || coded.Code != code || !core.IsDefinite(err) {
		t.Fatalf("error %v, want a definite %s", err, code)
	}
}

// piModule registers an action list, so the registry's own checks run on it.
type piModule struct{ defs []core.ActionDef }

func (m piModule) Manifest() core.Manifest {
	return core.Manifest{ID: ID, Version: 1, Actions: m.defs}
}

func TestPiPackageInstallSpec(t *testing.T) {
	def := piNewSetup(t).t.packageInstall()
	s, h := def.Spec, def.Handler
	if s.Name != "tailscale.package.install" || s.Version != 1 || s.Class != core.ClassCompensable || s.Side != core.SideAgent ||
		!slices.Equal(s.LockKeys, []string{"tailscale"}) || s.ApplyTimeout != 15*time.Minute || !s.Task || !s.LongRunning ||
		s.Available != nil || len(s.Guards) != 0 || s.Internal {
		t.Errorf("spec %+v, want a long-running compensable task on the agent, lock tailscale, 15 minutes, no Available, no guard", s)
	}
	if h.Task == nil || h.Apply != nil || h.Plan == nil || h.Verify == nil || h.Observe == nil || h.Inverse == nil || h.Recover != nil {
		t.Errorf("handler has Task %v Apply %v Plan %v Verify %v Observe %v Inverse %v Recover %v, want a Task handler with an undo and no recovery handler",
			h.Task != nil, h.Apply != nil, h.Plan != nil, h.Verify != nil, h.Observe != nil, h.Inverse != nil, h.Recover != nil)
	}
	if _, err := core.NewRegistry(piModule{defs: []core.ActionDef{def}}); err != nil {
		t.Errorf("the registry refuses the action: %v", err)
	}
}

func TestPiPackageInstallPlan(t *testing.T) {
	s := piNewSetup(t)
	h := s.t.packageInstall().Handler
	out, err := h.Plan(context.Background(), s.env, piRaw(map[string]any{}))
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Guards) != 0 || out.Confirmation != "" || out.StateHash == "" {
		t.Errorf("outcome %+v, want no guard, no confirmation and a state hash", out)
	}
	want := []core.DiffLine{
		{Op: "+", Text: "file " + keyringPath},
		{Op: "+", Text: "file " + sourcesPath},
		{Op: "+", Text: "package tailscale (the repository's current version)"},
	}
	if !slices.Equal(out.Diff, want) {
		t.Errorf("diff %+v, want %+v", out.Diff, want)
	}
	if len(s.h.ran) != 0 || len(s.h.files) != 0 {
		t.Errorf("a plan ran %v and wrote %v", s.h.ran, s.h.files)
	}
	again, err := h.Plan(context.Background(), s.env, piRaw(map[string]any{}))
	if err != nil || again.StateHash != out.StateHash {
		t.Errorf("a second plan hash %q (error %v), want %q", again.StateHash, err, out.StateHash)
	}

	s.h.reads[keyringPath] = piKeyring(t)
	same, err := h.Plan(context.Background(), s.env, piRaw(map[string]any{}))
	if err != nil {
		t.Fatal(err)
	}
	if same.StateHash == out.StateHash {
		t.Error("the state hash did not change when the keyring became present")
	}
	if len(same.Diff) != 3 || same.Diff[0].Op != " " || !strings.Contains(same.Diff[0].Text, keyringPath) || same.Diff[1].Op != "+" {
		t.Errorf("diff %+v, want the identical keyring shown as unchanged and the rest added", same.Diff)
	}

	other := piNewSetup(t)
	other.t = piTS(other.h, "debian", "bookworm", "arm64")
	other.h.reads[piStatus] = s.h.reads[piStatus]
	diff, err := other.t.packageInstall().Handler.Plan(context.Background(), other.env, piRaw(map[string]any{}))
	if err != nil || diff.StateHash == out.StateHash {
		t.Errorf("another OS gives hash %q (error %v), want one that differs from %q", diff.StateHash, err, out.StateHash)
	}
}

func TestPiPackageInstallPlanRefusals(t *testing.T) {
	t.Run("the package is present", func(t *testing.T) {
		s := piNewSetup(t)
		s.installed()
		_, err := s.t.packageInstall().Handler.Plan(context.Background(), s.env, piRaw(map[string]any{}))
		piRefused(t, err, errcode.TailscalePackagePresent)
		if !strings.Contains(err.Error(), piVersion) {
			t.Errorf("error %v does not name the installed version", err)
		}
	})
	t.Run("a repository file differs", func(t *testing.T) {
		s := piNewSetup(t)
		s.h.reads[sourcesPath] = []byte("deb https://example.com/ stable main\n")
		_, err := s.t.packageInstall().Handler.Plan(context.Background(), s.env, piRaw(map[string]any{}))
		piRefused(t, err, errcode.TailscaleRepoConflict)
	})
	t.Run("the OS is not supported", func(t *testing.T) {
		s := piNewSetup(t)
		s.t = piTS(s.h, "ubuntu", "focal", "amd64")
		_, err := s.t.packageInstall().Handler.Plan(context.Background(), s.env, piRaw(map[string]any{}))
		piRefused(t, err, errcode.TailscaleUnavailable)
	})
}

func TestPiPackageInstallTask(t *testing.T) {
	s := piNewSetup(t)
	def := s.t.packageInstall()
	raw, err := def.Handler.Task(context.Background(), s.env, piRaw(map[string]any{}))
	if err != nil {
		t.Fatal(err)
	}
	var out PackageInstalled
	if err := json.Unmarshal(raw, &out); err != nil || out.Version != piVersion {
		t.Fatalf("output %s (error %v), want version %s", raw, err, piVersion)
	}
	wantApt := [][]string{platform.AptUpdateTailscale(), platform.AptInstallTailscale()}
	if got := s.ranApt(); !slices.EqualFunc(got, wantApt, slices.Equal[[]string]) {
		t.Errorf("apt-get ran %v, want APT-1 then APT-2 %v", got, wantApt)
	}
	for _, d := range s.deadlines {
		if d <= 14*time.Minute || d > 15*time.Minute {
			t.Errorf("an apt-get call had %v left, want its context bounded by the 15 minute ApplyTimeout", d)
		}
	}
	if len(s.deadlines) != 2 {
		t.Errorf("%d calls saw a deadline, want 2", len(s.deadlines))
	}
	if !bytes.Equal(s.h.files[keyringPath], piKeyring(t)) || string(s.h.files[sourcesPath]) != piSources("ubuntu", "noble") {
		t.Errorf("files written %v, want the keyring and the source list", s.h.files)
	}

	effects, err := s.env.Effects.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var resources []string
	for _, e := range effects {
		resources = append(resources, e.Resource)
		if e.State != core.EffectMade || e.Kind != core.EffectCreate || string(e.Before.State) != "null" {
			t.Errorf("effect %+v, want a made creation from null", e)
		}
	}
	if want := []string{"tailscale.file:" + keyringPath, "tailscale.file:" + sourcesPath, "tailscale.package:tailscale"}; !slices.Equal(resources, want) {
		t.Errorf("effects %v, want %v", resources, want)
	}
	if post := effects[2].Post; post == nil || string(post.State) != `{"installed":true}` || !strings.Contains(string(post.Version), piVersion) {
		t.Errorf("package post-state %+v, want installed with the version", post)
	}

	id, version, ok, err := liveInstall(context.Background(), s.h.db.Read())
	if err != nil || !ok || id != "c1" || version != piVersion {
		t.Errorf("recorded install %q %q (found %v, error %v), want c1 %s", id, version, ok, err, piVersion)
	}
	if err := def.Handler.Verify(context.Background(), s.env, piRaw(map[string]any{}), raw); err != nil {
		t.Errorf("verify after the task: %v", err)
	}
}

func TestPiPackageInstallTaskLeavesIdenticalRepositoryFilesAlone(t *testing.T) {
	s := piNewSetup(t)
	s.h.reads[keyringPath] = piKeyring(t)
	s.h.reads[sourcesPath] = []byte(piSources("ubuntu", "noble"))
	if _, err := s.t.packageInstall().Handler.Task(context.Background(), s.env, piRaw(map[string]any{})); err != nil {
		t.Fatal(err)
	}
	if len(s.h.files) != 0 {
		t.Errorf("wrote %v over identical files", s.h.files)
	}
	effects, _ := s.env.Effects.List(context.Background())
	if len(effects) != 1 || effects[0].Resource != "tailscale.package:tailscale" {
		t.Errorf("effects %+v, want only the package", effects)
	}
}

func TestPiPackageInstallTaskRefusesAnInstalledPackage(t *testing.T) {
	s := piNewSetup(t)
	s.installed()
	_, err := s.t.packageInstall().Handler.Task(context.Background(), s.env, piRaw(map[string]any{}))
	piRefused(t, err, errcode.TailscalePackagePresent)
	if len(s.h.ran) != 0 || len(s.h.files) != 0 {
		t.Errorf("ran %v and wrote %v despite the refusal", s.h.ran, s.h.files)
	}
}

func TestPiPackageInstallTaskRefusesAConflictBeforeWriting(t *testing.T) {
	s := piNewSetup(t)
	s.h.reads[sourcesPath] = []byte("deb https://example.com/ stable main\n")
	_, err := s.t.packageInstall().Handler.Task(context.Background(), s.env, piRaw(map[string]any{}))
	piRefused(t, err, errcode.TailscaleRepoConflict)
	if len(s.h.ran) != 0 || len(s.h.files) != 0 {
		t.Errorf("ran %v and wrote %v despite the conflict", s.h.ran, s.h.files)
	}
}

func TestPiPackageInstallTaskFailures(t *testing.T) {
	apt1, apt2 := piVec(piApt, platform.AptUpdateTailscale()), piVec(piApt, platform.AptInstallTailscale())
	t.Run("the repository refresh fails", func(t *testing.T) {
		s := piNewSetup(t)
		s.h.script(apt1, tsRun{res: core.CmdResult{ExitCode: 100, Stderr: []byte("W: x\nE: Failed to fetch https://pkgs.tailscale.com\n")}})
		_, err := s.t.packageInstall().Handler.Task(context.Background(), s.env, piRaw(map[string]any{}))
		if err == nil || !strings.Contains(err.Error(), "update") || !strings.Contains(err.Error(), "Failed to fetch") || core.IsDefinite(err) {
			t.Errorf("error %v, want the apt-get failure, not definite", err)
		}
		if got := s.ranApt(); len(got) != 1 {
			t.Errorf("apt-get ran %v, want only APT-1", got)
		}
	})
	t.Run("the install fails and leaves its effect intended", func(t *testing.T) {
		s := piNewSetup(t)
		s.h.script(apt2, tsRun{res: core.CmdResult{ExitCode: 100, Stderr: []byte("E: Unable to locate package tailscale\n")}})
		_, err := s.t.packageInstall().Handler.Task(context.Background(), s.env, piRaw(map[string]any{}))
		if err == nil || !strings.Contains(err.Error(), "Unable to locate package") || core.IsDefinite(err) {
			t.Errorf("error %v, want the apt-get failure, not definite", err)
		}
		effects, _ := s.env.Effects.List(context.Background())
		if len(effects) != 3 || effects[2].State != core.EffectIntended {
			t.Errorf("effects %+v, want the package effect left intended for the engine to settle", effects)
		}
		if _, _, ok, _ := liveInstall(context.Background(), s.h.db.Read()); ok {
			t.Error("an install was recorded for a failed apt-get")
		}
	})
	t.Run("apt-get succeeds but the package is not installed", func(t *testing.T) {
		s := piNewSetup(t)
		s.env.Exec = s.h.env("c1").Exec
		_, err := s.t.packageInstall().Handler.Task(context.Background(), s.env, piRaw(map[string]any{}))
		if err == nil || !strings.Contains(err.Error(), "not installed") {
			t.Errorf("error %v, want a report that tailscale is not installed", err)
		}
	})
}

func TestPiPackageInstallVerify(t *testing.T) {
	s := piNewSetup(t)
	h := s.t.packageInstall().Handler
	if err := h.Verify(context.Background(), s.env, piRaw(map[string]any{}), piRaw(PackageInstalled{Version: piVersion})); err == nil {
		t.Error("verify passed without the package")
	}
	s.installed()
	for name, out := range map[string]PackageInstalled{"the recorded version": {Version: piVersion}, "no recorded version (recovery)": {}} {
		if err := h.Verify(context.Background(), s.env, piRaw(map[string]any{}), piRaw(out)); err != nil {
			t.Errorf("verify with %s: %v", name, err)
		}
	}
	if err := h.Verify(context.Background(), s.env, piRaw(map[string]any{}), piRaw(PackageInstalled{Version: "1.0.0"})); err == nil {
		t.Error("verify passed with a version other than the installed one")
	}
}

func TestPiPackageInstallObserve(t *testing.T) {
	s := piNewSetup(t)
	h := s.t.packageInstall().Handler
	observe := func(resource string) core.Observation {
		t.Helper()
		obs, err := h.Observe(context.Background(), s.env, core.Effect{Resource: resource, State: core.EffectIntended})
		if err != nil {
			t.Fatal(err)
		}
		if obs.Matches != 1 || !obs.Exclusive {
			t.Errorf("observation of %s %+v, want one exclusive match", resource, obs)
		}
		return obs
	}
	if got := observe("tailscale.package:tailscale").Current; string(got.State) != "null" {
		t.Errorf("absent package observed as %s, want null", got.State)
	}
	s.installed()
	obs := observe("tailscale.package:tailscale")
	if string(obs.Current.State) != `{"installed":true}` || !strings.Contains(string(obs.Current.Version), piVersion) {
		t.Errorf("installed package observed as %+v", obs.Current)
	}
	if got := observe("tailscale.file:" + sourcesPath).Current; string(got.State) != "null" {
		t.Errorf("absent file observed as %s, want null", got.State)
	}
	s.h.reads[sourcesPath] = []byte(piSources("ubuntu", "noble"))
	if got := observe("tailscale.file:" + sourcesPath).Current; got.State == nil || string(got.State) == "null" || !strings.Contains(string(got.State), "sha256") {
		t.Errorf("present file observed as %s, want its digest", got.State)
	}
	// What the task records after a write is what an observation finds.
	done := piNewSetup(t)
	if _, err := done.t.packageInstall().Handler.Task(context.Background(), done.env, piRaw(map[string]any{})); err != nil {
		t.Fatal(err)
	}
	done.h.reads[sourcesPath] = done.h.files[sourcesPath]
	effects, _ := done.env.Effects.List(context.Background())
	for _, e := range []core.Effect{effects[1], effects[2]} {
		obs, err := done.t.packageInstall().Handler.Observe(context.Background(), done.env, e)
		if err != nil || string(obs.Current.State) != string(e.Post.State) || string(obs.Current.Version) != string(e.Post.Version) {
			t.Errorf("observation of %s %+v (error %v) differs from the recorded post-state %+v", e.Resource, obs.Current, err, *e.Post)
		}
	}
	for _, bad := range []string{"", "docker.container:c1", "tailscale.file:/etc/passwd/x"} {
		if _, err := h.Observe(context.Background(), s.env, core.Effect{Resource: bad}); err == nil {
			t.Errorf("observed %q without an error", bad)
		}
	}
}

func TestPiPackageInstallInverse(t *testing.T) {
	apt6 := platform.AptRemoveTailscale()
	t.Run("removes the package this change installed", func(t *testing.T) {
		s := piNewSetup(t)
		def := s.t.packageInstall()
		if _, err := def.Handler.Task(context.Background(), s.env, piRaw(map[string]any{})); err != nil {
			t.Fatal(err)
		}
		s.h.ran = nil
		if _, err := def.Handler.Inverse(context.Background(), s.env, core.Effect{Resource: "tailscale.package:tailscale", State: core.EffectMade}); err != nil {
			t.Fatal(err)
		}
		if got := s.ranApt(); !slices.EqualFunc(got, [][]string{apt6}, slices.Equal[[]string]) {
			t.Errorf("apt-get ran %v, want APT-6 only", got)
		}
		if _, _, ok, err := liveInstall(context.Background(), s.h.db.Read()); err != nil || ok {
			t.Errorf("live install after the undo: found %v, error %v, want none", ok, err)
		}
	})
	t.Run("a package already gone is left", func(t *testing.T) {
		s := piNewSetup(t)
		if _, err := s.t.packageInstall().Handler.Inverse(context.Background(), s.env, core.Effect{Resource: "tailscale.package:tailscale"}); err != nil {
			t.Fatal(err)
		}
		if len(s.h.ran) != 0 {
			t.Errorf("ran %v for a package that is not installed", s.h.ran)
		}
	})
	t.Run("a failed removal is reported", func(t *testing.T) {
		s := piNewSetup(t)
		s.installed()
		s.h.script(piVec(piApt, apt6), tsRun{res: core.CmdResult{ExitCode: 100, Stderr: []byte("E: Could not get lock\n")}})
		_, err := s.t.packageInstall().Handler.Inverse(context.Background(), s.env, core.Effect{Resource: "tailscale.package:tailscale"})
		if err == nil || !strings.Contains(err.Error(), "Could not get lock") {
			t.Errorf("error %v, want the apt-get failure", err)
		}
	})
	t.Run("removes a repository file the change wrote", func(t *testing.T) {
		s := piNewSetup(t)
		s.h.files[sourcesPath] = []byte("x")
		s.h.files[keyringPath] = []byte("y")
		if _, err := s.t.packageInstall().Handler.Inverse(context.Background(), s.env, core.Effect{Resource: "tailscale.file:" + sourcesPath}); err != nil {
			t.Fatal(err)
		}
		if _, ok := s.h.files[sourcesPath]; ok {
			t.Error("the source list is still there")
		}
		if _, ok := s.h.files[keyringPath]; !ok {
			t.Error("the keyring was removed with it")
		}
	})
	t.Run("refuses a resource it did not make", func(t *testing.T) {
		s := piNewSetup(t)
		for _, bad := range []string{"", "tailscale.file:/etc/passwd", "tailscale.package:other", "docker.container:c1"} {
			if _, err := s.t.packageInstall().Handler.Inverse(context.Background(), s.env, core.Effect{Resource: bad}); err == nil {
				t.Errorf("inverse of %q succeeded", bad)
			}
		}
	})
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mkdir -p /root/.cache/w4-tmp/20 && export TMPDIR=/root/.cache/w4-tmp/20 GOTOOLCHAIN=local && timeout 300 go test -count=1 -run 'Pi' ./internal/modules/tailscale/`
Expected: FAIL to build, naming `repoKeyring`, `keyringFingerprint`, `repoFiles`, `ensureRepo` or `packageInstall` as undefined.

- [ ] **Step 3: Implement**

`repo.go`:

1. `//go:embed repo_keyring.gpg` into `var repoKeyring []byte` (a variable, because a test replaces it). The file is the Tailscale repository key Task 1 committed in the module directory; this task does not download anything. If the file is absent, stop and report: only Task 1 reads the network.
2. `const keyringFingerprint`: the fingerprint of that key as Task 1 recorded it in spec 4.1 (the "Files" bullet, "fingerprint pinned in code"), written as 40 upper-case hexadecimal digits with no spaces. If the spec does not give it, stop and report. Do not compute it from the file and paste the result: the pin exists to catch a changed file.
3. `keyringFingerprints(b []byte) ([]string, error)` lists the OpenPGP version 4 fingerprints (40 upper-case hex digits) of every public-key (tag 6) and public-subkey (tag 14) packet in a binary keyring, in order. A fingerprint is the SHA-1 of the byte 0x99, the packet body's length as 16 bits, big-endian, and the body (`//nolint:gosec` on the `crypto/sha1` import with G505 and on the use with G401, and on the length conversion with G115, each with the reason). Packet headers: bit 7 of the first byte must be set, else an error; bit 6 set is the new format (tag in the low six bits; length: one byte below 192, two bytes for 192 to 223 as `((first-192)<<8)+second+192`, `0xFF` then four bytes; 224 to 254 is a partial length, which is an error); bit 6 clear is the old format (tag in bits 5 to 2; the low two bits give a length of 1, 2 or 4 bytes, and 3 means "to the end of the data"). A packet that runs past the data is an error. Keys whose version byte is not 4 are skipped. An empty keyring gives no fingerprints and no error.
4. `repoFiles()`: `t.d.osRelease()` (an error is returned wrapped, `tailscale: read the OS release: %w`); supported systems are Ubuntu `jammy` and `noble` and Debian `bookworm` and `trixie` on `t.d.arch` `amd64` or `arm64`, anything else returns `errcode.New(errcode.TailscaleUnavailable, "tailscale does not support <ID> <Codename> on <arch>")`. It then requires `keyringFingerprint` among `keyringFingerprints(repoKeyring)` and returns a plain error mentioning the fingerprint when it is not, so a keyring that is not the pinned one is never written. It returns a copy of `repoKeyring` and the single line `deb [signed-by=/usr/share/keyrings/tailscale-archive-keyring.gpg] https://pkgs.tailscale.com/stable/<ID> <Codename> main` plus a newline (use the `keyringPath` constant for the path).
5. `repoStatus()` (unexported, shared by the plan and `ensureRepo`): calls `repoFiles`, then reads each of `keyringPath` and `sourcesPath` through `t.d.readFile`. `fs.ErrNotExist` means absent; another read error is returned wrapped and is not a coded refusal; identical bytes mean present; different bytes return `core.Definite(errcode.New(errcode.TailscaleRepoConflict, "<path> exists and is not what Darkpanel would write"))`. It returns the two files in order (keyring, source list) with their bytes and whether each is present.
6. `ensureRepo(ctx, env)`: `repoStatus` first, so a conflict or an unreadable file writes nothing, then, for each absent file in order, `env.Files.WriteFile(ctx, ID, path, data, 0o644)` (the file writer records the path as owned; a present identical file is not written, so uninstall leaves it). When `env.Effects` is not nil each write is journaled: `Intend` a `core.Effect` of kind create, resource `repoFileResource(path)` (`tailscale.file:<path>`), `Local` and `InverseLocal` true, `Before` the state `null`, `After` the state `repoFileDoc(data)`; then write; then `Made` with the same document. `repoFileDoc` renders `{"sha256":"<hex of the SHA-256 of the bytes>"}`. It returns the paths it wrote, in order, also on a failure part way.
7. `aptInstall(ctx, env)`: `run` of `/usr/bin/apt-get` with `platform.AptUpdateTailscale()`, then, only if that succeeded, with `platform.AptInstallTailscale()`; the first error is returned as `run` made it.

`package_install.go`:

1. `func (t ts) packageInstall() core.ActionDef` is `core.DefineTask[PackageInstallInput, PackageInstalled]` with name `tailscale.package.install`, a summary sentence, version 1, `core.ClassCompensable`, `core.SideAgent`, `LockKeys` `[]string{lockKey}`, `ApplyTimeout` the constant `packageInstallTimeout` (15 minutes), no guards and no `Available` (the action must work without tailscaled). The handler type `packageInstallHandler{t ts}` has `Plan`, `Task`, `Verify`, `Observe` and `Inverse`, so `DefineTask` finds the `Undoer` by type assertion; it has no `Recover`.
2. `Plan`: read `installedVersion`; non-empty returns `core.Definite(errcode.New(errcode.TailscalePackagePresent, "tailscale <version> is already installed"))`. `repoStatus`: a coded error (unsupported OS, repository conflict) is returned `core.Definite`; any other error as it is. The diff has, in order, one line per repository file: op `+` and text `file <path>` when absent, or op space and text `file <path> is already present and identical; it stays`; then the line `+ package tailscale (the repository's current version)`. No guard, no confirmation. `StateHash` is the hex SHA-256 of the lines `os=<ID>/<Codename>`, `arch=<arch>` and, for each file, `<path>=present` or `<path>=absent`, joined by newlines.
3. `Task` (runs in the transient unit): bound the context with `packageInstallTimeout` (the runner gives a context without a deadline only 30 seconds, and apt takes longer); re-read `installedVersion` and refuse a present package as in `Plan`; call `ensureRepo` (a conflict stops here, before any write or any `apt-get`); `Intend` the package effect (kind create, resource `installPackageResource` = `tailscale.package:tailscale`, `Local` and `InverseLocal` true, `Before` the state `null`, no `After`); `aptInstall`; read `installedVersion` again and return a plain error "tailscale: apt-get finished but tailscale is not installed" when it is empty; `Made` with `installPackageDoc(version)` (state `{"installed":true}`, version member `{"version":"<version>"}`); record the install with `insertInstall(ctx, tx, env.ChangeID, version, env.Clock.Now())` inside `env.DB.Write`. A failed `apt-get` leaves the package effect intended (the engine settles it through `Observe`). It returns `PackageInstalled{Version: version}`.
4. `Verify`: the installed version must be non-empty and equal `out.Version` when that is set (recovery passes the zero output).
5. `Observe` only reads. For `tailscale.package:tailscale` it reports the document `installPackageDoc(version)`, or the state `null` when no version is installed; for `tailscale.file:<path>` with `<path>` exactly `keyringPath` or `sourcesPath` it reads the file through `t.d.readFile` and reports `repoFileDoc(bytes)`, or `null` for `fs.ErrNotExist`. Both observations have `Matches` 1 and `Exclusive` true. Any other resource is an error.
6. `Inverse` for `tailscale.package:tailscale`: if no version is installed, nothing; otherwise `run` `/usr/bin/apt-get` with `platform.AptRemoveTailscale()` and then `setInstallRemoved(ctx, env.DB, env.ChangeID, env.Clock.Now())`. For `tailscale.file:<path>` with `<path>` exactly one of the two repository paths: `env.Files.Remove(ctx, ID, path)`. Any other resource is an error. The undo runs the effects in reverse order, so the package goes before the source list and the keyring. The effects exist only for what this change made: the package effect is journaled after the `package_present` check and the file effects only for files `ensureRepo` wrote, which is how "remove the package only if this change installed it" holds.

- [ ] **Step 4: Run the tests to verify they pass, then the gates**

Run: `mkdir -p /root/.cache/w4-tmp/20 && export TMPDIR=/root/.cache/w4-tmp/20 GOTOOLCHAIN=local && timeout 300 go test -count=1 -run 'Pi' ./internal/modules/tailscale/`
Expected: PASS.

Then every gate of Global Constraints, with the real output recorded:

```bash
mkdir -p /root/.cache/w4-tmp/20 && export TMPDIR=/root/.cache/w4-tmp/20 GOTOOLCHAIN=local
timeout 600 go test -count=1 -race ./internal/modules/tailscale/...
timeout 600 go build ./... && timeout 300 go vet ./...
timeout 1500 go test -count=1 ./...
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
rm -rf /root/.cache/w4-tmp/20 && go clean -testcache
```

Expected: every line `ok` or `?`; `go vet` and `go mod tidy -diff` print nothing; `0 issues.` (`go mod tidy -diff` must stay empty: the task imports only the standard library and packages already in the module).

- [ ] **Step 5: Commit**

```bash
git add internal/modules/tailscale/repo.go internal/modules/tailscale/package_install.go internal/modules/tailscale/repo_test.go internal/modules/tailscale/package_install_test.go
git commit -m "feat(tailscale): add package.install action"
```

### Task 21: tailscale: prefs.set

**Files:**
- Create: `internal/modules/tailscale/prefs_set.go`
- Test: `internal/modules/tailscale/prefs_set_test.go`

**Interfaces:**
- Consumes: Contracts "Tailscale wire shapes" (`PrefsFields`, `PrefsSetInput`, `PrefsResult`, and the action-spec row of `tailscale.prefs.set`); "Tailscale internals" (`ts`, `deps.local`, `localAPI`, `lockKey`, `ts.available`); "Tailscale test host" (`newTSHost`, `tsHost`, `tsLocal`, `tsUnits`, `tsJournal`); "Tailscale shared plan helpers" (`ts.linkGuard`, `ts.currentLinkPrefs`); "Tailscale preferences" (`prefsFieldsOf`, written by Task 14 in `status.go`); "Link guard rules" (`link.GuardID`, `link.Prefs`, `link.Change`, `link.IsTailscaleAddr`); `core.DefineRevertible`, `core.Reverter`, `core.Observer`, `core.RevertOutcome` (`internal/core/contract.go`, `define.go`); `tslocal.Prefs`, `tslocal.PrefsPatch`, `tslocal.APIError` and `tslocal.ErrUnavailable` (`internal/platform/tslocal/client.go`).
- Produces: `func (t ts) prefsSet() core.ActionDef`, the constructor of `tailscale.prefs.set/v1`. Task 31 adds it to `Manifest().Actions`; this task does not edit `tailscale.go`. Contracts "Tailscale preferences": `prefsEffectResource`, `prefsCommitWait`, `prefsSetPick`, `prefsSetNormalize`, `prefsSetPatch`, `prefsSetLinkAfter` and `prefsSetHandler` with its `Revert` and `Observe`, which Task 27 calls. The other package-level helpers of `prefs_set.go` are named with the prefix `prefsSet` (`prefsSetState`, `prefsSetSame`, `prefsSetRefusal`) and are not Contracts.

**Depends on:** Task 14, Task 17

The tests are module-private and use only the fakes of Contracts "Tailscale test host"; nothing here touches tailscaled, apt or systemd on this host. Helpers `pfSetup`, `pfEffects` and `pfSame` are declared by this task's test file only; the test files of Tasks 22 and 23 declare their own.

- [ ] **Step 1: Write the failing test**

Create `internal/modules/tailscale/prefs_set_test.go` with exactly this content:

```go
package tailscale

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/tailscale/link"
	"github.com/darkraise/darkpanel/internal/platform/tslocal"
)

func pfBool(b bool) *bool       { return &b }
func pfString(s string) *string { return &s }

func pfPrefixes(s ...string) []netip.Prefix {
	out := []netip.Prefix{}
	for _, p := range s {
		out = append(out, netip.MustParsePrefix(p))
	}
	return out
}

// pfSetup is a running node with prefs behind a ts whose fakes are the host's.
func pfSetup(t *testing.T, prefs tslocal.Prefs) (*tsHost, ts) {
	t.Helper()
	h := newTSHost(t)
	h.local.Status = &tslocal.Status{BackendState: "Running"}
	h.local.Prefs = &prefs
	if h.local.Errs == nil {
		h.local.Errs = map[string]error{}
	}
	if h.units.States == nil {
		h.units.States = map[string]core.UnitState{}
	}
	if h.reads == nil {
		h.reads = map[string][]byte{}
	}
	h.units.States[tailscaledUnit] = core.UnitState{LoadState: "loaded", ActiveState: "active", SubState: "running"}
	h.units.States[controllerUnit] = core.UnitState{LoadState: "not-found", ActiveState: "inactive"}
	return h, h.ts()
}

func pfEffects(t *testing.T, h *tsHost) []core.Effect {
	t.Helper()
	effects, err := h.env("c1").Effects.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return effects
}

// pfSame compares two JSON documents as values.
func pfSame(t *testing.T, a, b []byte) bool {
	t.Helper()
	var va, vb any
	if err := json.Unmarshal(a, &va); err != nil {
		t.Fatalf("document %s: %v", a, err)
	}
	if err := json.Unmarshal(b, &vb); err != nil {
		t.Fatalf("document %s: %v", b, err)
	}
	return reflect.DeepEqual(va, vb)
}

func TestPfActionDefinition(t *testing.T) {
	_, tt := pfSetup(t, tslocal.Prefs{})
	def := tt.prefsSet()
	s, h := def.Spec, def.Handler
	if s.Name != "tailscale.prefs.set" || s.Version != 1 || s.Class != core.ClassRevertible || s.Side != core.SideAgent ||
		!slices.Equal(s.LockKeys, []string{lockKey}) || s.RepairedBy != "tailscale.prefs.repair" || s.Available == nil || s.Task || s.LongRunning {
		t.Errorf("spec %+v, want a synchronous revertible agent action locking %q, repaired by tailscale.prefs.repair, with an availability check", s, lockKey)
	}
	if want := []core.GuardSpec{{ID: link.GuardID, Overridable: true}}; !reflect.DeepEqual(s.Guards, want) {
		t.Errorf("guards %+v, want %+v", s.Guards, want)
	}
	if def.InputType != reflect.TypeFor[PrefsSetInput]() || def.OutputType != reflect.TypeFor[PrefsResult]() {
		t.Errorf("input %v and output %v, want PrefsSetInput and PrefsResult", def.InputType, def.OutputType)
	}
	if h.Plan == nil || h.Apply == nil || h.Verify == nil || h.Snapshot == nil || h.Revert == nil || h.Observe == nil || h.Inverse != nil {
		t.Error("handler lacks Plan, Apply, Verify, Snapshot, Revert or Observe, or has an Inverse")
	}
}

func TestPfPlan(t *testing.T) {
	line := func(op, text string) core.DiffLine { return core.DiffLine{Op: op, Text: text} }
	exitRoutes := pfPrefixes("10.1.0.0/24", "0.0.0.0/0", "::/0")
	tests := []struct {
		name  string
		prefs tslocal.Prefs
		in    string
		want  []core.DiffLine
	}{
		{"shields up", tslocal.Prefs{WantRunning: true}, `{"shieldsUp":true}`,
			[]core.DiffLine{line("-", "shieldsUp: false"), line("+", "shieldsUp: true")}},
		{"a value already set", tslocal.Prefs{WantRunning: true}, `{"wantRunning":true}`,
			[]core.DiffLine{line(" ", "wantRunning: true (unchanged)")}},
		{"routes are shown sorted", tslocal.Prefs{AdvertiseRoutes: exitRoutes}, `{"advertiseRoutes":["192.168.5.0/24","10.2.0.0/16"]}`,
			[]core.DiffLine{line("-", "advertiseRoutes: 10.1.0.0/24"), line("+", "advertiseRoutes: 10.2.0.0/16, 192.168.5.0/24")}},
		{"no routes", tslocal.Prefs{AdvertiseRoutes: exitRoutes}, `{"advertiseRoutes":[]}`,
			[]core.DiffLine{line("-", "advertiseRoutes: 10.1.0.0/24"), line("+", "advertiseRoutes: none")}},
		{"exit node cleared", tslocal.Prefs{ExitNodeID: "nABC123CNTRL"}, `{"exitNode":""}`,
			[]core.DiffLine{line("-", "exitNode: nABC123CNTRL"), line("+", "exitNode: none")}},
		{"exit node by address", tslocal.Prefs{}, `{"exitNode":"100.64.0.9"}`,
			[]core.DiffLine{line("-", "exitNode: none"), line("+", "exitNode: 100.64.0.9")}},
		{"advertise an exit node", tslocal.Prefs{}, `{"advertiseExitNode":true}`,
			[]core.DiffLine{line("-", "advertiseExitNode: false"), line("+", "advertiseExitNode: true")}},
		{"several fields in the order of the wire shape", tslocal.Prefs{WantRunning: true}, `{"runSSH":true,"acceptRoutes":true,"wantRunning":false}`,
			[]core.DiffLine{
				line("-", "wantRunning: true"), line("+", "wantRunning: false"),
				line("-", "acceptRoutes: false"), line("+", "acceptRoutes: true"),
				line("-", "runSSH: false"), line("+", "runSSH: true"),
			}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, tt := pfSetup(t, tc.prefs)
			out, err := tt.prefsSet().Handler.Plan(context.Background(), h.env(""), json.RawMessage(tc.in))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(out.Diff, tc.want) {
				t.Errorf("diff %+v, want %+v", out.Diff, tc.want)
			}
			if len(out.Guards) != 1 || out.Guards[0].ID != link.GuardID || !out.Guards[0].OK {
				t.Errorf("guards %+v, want %s passing", out.Guards, link.GuardID)
			}
			if len(out.StateHash) != 64 || out.Confirmation != "" {
				t.Errorf("state hash %q and confirmation %q, want a 64-digit hash and none", out.StateHash, out.Confirmation)
			}
			if len(h.local.Edits) != 0 || len(h.ran) != 0 {
				t.Errorf("plan edited the preferences %v or ran %v", h.local.Edits, h.ran)
			}
		})
	}
}

func TestPfPlanGuard(t *testing.T) {
	plan := func(in string) core.Guard {
		t.Helper()
		h, tt := pfSetup(t, tslocal.Prefs{WantRunning: true, RunSSH: true})
		out, err := tt.prefsSet().Handler.Plan(context.Background(), h.env(""), json.RawMessage(in))
		if err != nil || len(out.Guards) != 1 {
			t.Fatalf("plan %s: guards %+v, err %v", in, out.Guards, err)
		}
		return out.Guards[0]
	}
	if g := plan(`{"wantRunning":false}`); g.ID != link.GuardID || g.OK || g.Message == "" {
		t.Errorf("stopping the node while Tailscale SSH is on: guard %+v, want a failure with a message", g)
	}
	if g := plan(`{"wantRunning":true}`); !g.OK {
		t.Errorf("a change that removes nothing: guard %+v, want it to pass", g)
	}
	if g := plan(`{"wantRunning":false,"overrides":["tailscale.link"]}`); !g.OK || !g.Overridden {
		t.Errorf("overridden guard %+v, want OK and Overridden", g)
	}
}

func TestPfPlanStateHash(t *testing.T) {
	hash := func(prefs tslocal.Prefs) string {
		t.Helper()
		h, tt := pfSetup(t, prefs)
		out, err := tt.prefsSet().Handler.Plan(context.Background(), h.env(""), json.RawMessage(`{"shieldsUp":true}`))
		if err != nil {
			t.Fatal(err)
		}
		return out.StateHash
	}
	base := hash(tslocal.Prefs{WantRunning: true})
	if other := hash(tslocal.Prefs{WantRunning: true, ShieldsUp: true}); other == base {
		t.Error("another current value of a field the change sets has the same state hash")
	}
	if other := hash(tslocal.Prefs{WantRunning: true, RunSSH: true}); other != base {
		t.Error("a preference the change does not set changed the state hash")
	}
}

func TestPfRefusals(t *testing.T) {
	tests := []struct {
		name, in, path string
	}{
		{"nothing set", `{}`, ""},
		{"a default route", `{"advertiseRoutes":["0.0.0.0/0"]}`, "/advertiseRoutes/0"},
		{"the IPv6 default route", `{"advertiseRoutes":["10.0.0.0/24","::/0"]}`, "/advertiseRoutes/1"},
		{"bits past the mask", `{"advertiseRoutes":["10.0.0.5/24"]}`, "/advertiseRoutes/0"},
		{"not a prefix", `{"advertiseRoutes":["lan"]}`, "/advertiseRoutes/0"},
		{"a public exit node address", `{"exitNode":"8.8.8.8"}`, "/exitNode"},
		{"an exit node that is no id", `{"exitNode":"not a node!"}`, "/exitNode"},
	}
	for _, tc := range tests {
		for _, phase := range []string{"plan", "snapshot", "apply"} {
			t.Run(tc.name+" at "+phase, func(t *testing.T) {
				h, tt := pfSetup(t, tslocal.Prefs{WantRunning: true})
				def, ctx, raw := tt.prefsSet(), context.Background(), json.RawMessage(tc.in)
				var err error
				switch phase {
				case "plan":
					_, err = def.Handler.Plan(ctx, h.env(""), raw)
				case "snapshot":
					_, err = def.Handler.Snapshot(ctx, h.env("c1"), raw)
				default:
					_, err = def.Handler.Apply(ctx, h.env("c1"), raw)
				}
				e := errcode.From(err)
				if e.Code != errcode.InvalidInput || !core.IsDefinite(err) {
					t.Fatalf("err = %v (definite %v), want %s marked definite", err, core.IsDefinite(err), errcode.InvalidInput)
				}
				if tc.path == "" && len(e.Fields) != 0 || tc.path != "" && (len(e.Fields) != 1 || e.Fields[0].Path != tc.path) {
					t.Errorf("fields %+v, want one at %q", e.Fields, tc.path)
				}
				if len(pfEffects(t, h)) != 0 || len(h.local.Edits) != 0 {
					t.Errorf("effects %v and edits %v, want none", pfEffects(t, h), h.local.Edits)
				}
			})
		}
	}
}

func TestPfApplyPatch(t *testing.T) {
	zero := netip.Addr{}
	addr := netip.MustParseAddr("100.64.0.9")
	withExit := tslocal.Prefs{AdvertiseRoutes: pfPrefixes("10.1.0.0/24", "0.0.0.0/0", "::/0")}
	tests := []struct {
		name  string
		prefs tslocal.Prefs
		in    string
		want  tslocal.PrefsPatch
		out   string
	}{
		{"one boolean", tslocal.Prefs{WantRunning: true}, `{"shieldsUp":true}`,
			tslocal.PrefsPatch{ShieldsUp: pfBool(true)}, `{"shieldsUp":true}`},
		{"accept routes is the RouteAll preference", tslocal.Prefs{}, `{"acceptRoutes":true}`,
			tslocal.PrefsPatch{RouteAll: pfBool(true)}, `{"acceptRoutes":true}`},
		{"two fields and no other", tslocal.Prefs{WantRunning: true}, `{"wantRunning":false,"runSSH":true}`,
			tslocal.PrefsPatch{WantRunning: pfBool(false), RunSSH: pfBool(true)}, `{"wantRunning":false,"runSSH":true}`},
		{"exit node by id clears the address", tslocal.Prefs{ExitNodeIP: addr}, `{"exitNode":"nABC123CNTRL"}`,
			tslocal.PrefsPatch{ExitNodeID: pfString("nABC123CNTRL"), ExitNodeIP: &zero}, `{"exitNode":"nABC123CNTRL"}`},
		{"exit node by address clears the id", tslocal.Prefs{ExitNodeID: "nABC123CNTRL"}, `{"exitNode":"100.64.0.9"}`,
			tslocal.PrefsPatch{ExitNodeID: pfString(""), ExitNodeIP: &addr}, `{"exitNode":"100.64.0.9"}`},
		{"clear the exit node", tslocal.Prefs{ExitNodeID: "nABC123CNTRL"}, `{"exitNode":""}`,
			tslocal.PrefsPatch{ExitNodeID: pfString(""), ExitNodeIP: &zero}, `{"exitNode":""}`},
		{"routes keep the exit node advertisement", withExit, `{"advertiseRoutes":["192.168.5.0/24","10.2.0.0/16"]}`,
			tslocal.PrefsPatch{AdvertiseRoutes: &[]netip.Prefix{netip.MustParsePrefix("10.2.0.0/16"), netip.MustParsePrefix("192.168.5.0/24"), netip.MustParsePrefix("0.0.0.0/0"), netip.MustParsePrefix("::/0")}},
			`{"advertiseRoutes":["10.2.0.0/16","192.168.5.0/24"]}`},
		{"the exit node advertisement keeps the routes", tslocal.Prefs{AdvertiseRoutes: pfPrefixes("10.1.0.0/24")}, `{"advertiseExitNode":true}`,
			tslocal.PrefsPatch{AdvertiseRoutes: &[]netip.Prefix{netip.MustParsePrefix("10.1.0.0/24"), netip.MustParsePrefix("0.0.0.0/0"), netip.MustParsePrefix("::/0")}},
			`{"advertiseExitNode":true}`},
		{"stop advertising the exit node", withExit, `{"advertiseExitNode":false}`,
			tslocal.PrefsPatch{AdvertiseRoutes: &[]netip.Prefix{netip.MustParsePrefix("10.1.0.0/24")}}, `{"advertiseExitNode":false}`},
		{"no routes at all", withExit, `{"advertiseRoutes":[]}`,
			tslocal.PrefsPatch{AdvertiseRoutes: &[]netip.Prefix{netip.MustParsePrefix("0.0.0.0/0"), netip.MustParsePrefix("::/0")}}, `{"advertiseRoutes":[]}`},
		{"routes and exit node together", tslocal.Prefs{}, `{"advertiseRoutes":["10.3.0.0/24"],"advertiseExitNode":true}`,
			tslocal.PrefsPatch{AdvertiseRoutes: &[]netip.Prefix{netip.MustParsePrefix("10.3.0.0/24"), netip.MustParsePrefix("0.0.0.0/0"), netip.MustParsePrefix("::/0")}},
			`{"advertiseRoutes":["10.3.0.0/24"],"advertiseExitNode":true}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, tt := pfSetup(t, tc.prefs)
			raw, err := tt.prefsSet().Handler.Apply(context.Background(), h.env("c1"), json.RawMessage(tc.in))
			if err != nil {
				t.Fatal(err)
			}
			if len(h.local.Edits) != 1 || !reflect.DeepEqual(h.local.Edits[0], tc.want) {
				t.Errorf("edits %+v, want exactly %+v", h.local.Edits, tc.want)
			}
			var res PrefsResult
			if err := json.Unmarshal(raw, &res); err != nil {
				t.Fatal(err)
			}
			if got, _ := json.Marshal(res.Prefs); !pfSame(t, got, []byte(tc.out)) {
				t.Errorf("output prefs %s, want exactly the fields set: %s", got, tc.out)
			}
		})
	}
}

func TestPfApplyJournalsTheChange(t *testing.T) {
	h, tt := pfSetup(t, tslocal.Prefs{WantRunning: true})
	if _, err := tt.prefsSet().Handler.Apply(context.Background(), h.env("c1"), json.RawMessage(`{"shieldsUp":true,"runSSH":true}`)); err != nil {
		t.Fatal(err)
	}
	effects := pfEffects(t, h)
	if len(effects) != 1 {
		t.Fatalf("%d effects journaled, want 1", len(effects))
	}
	e := effects[0]
	if e.Kind != core.EffectModify || e.Resource != "tailscale.prefs:node" || e.Local || e.CommitWait != 2*time.Minute || e.State != core.EffectMade {
		t.Errorf("effect %+v, want a made, non-local modify of tailscale.prefs:node with a 2 minute commit wait", e)
	}
	if !pfSame(t, e.Before.State, []byte(`{"shieldsUp":false,"runSSH":false}`)) {
		t.Errorf("before %s, want the previous values of the two fields", e.Before.State)
	}
	if e.After == nil || !pfSame(t, e.After.State, []byte(`{"shieldsUp":true,"runSSH":true}`)) {
		t.Errorf("after %+v, want the two requested values", e.After)
	}
	if e.Post == nil || !pfSame(t, e.Post.State, []byte(`{"shieldsUp":true,"runSSH":true}`)) {
		t.Errorf("post %+v, want the two values now", e.Post)
	}
}

func TestPfApplyRefusals(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		code     errcode.Code // "" for an error that is not coded
		definite bool
		state    core.EffectState
	}{
		{"tailscaled refuses the edit", &tslocal.APIError{StatusCode: 400, Message: "bad route"}, errcode.InvalidInput, true, core.EffectSkipped},
		{"tailscaled is not reachable", fmt.Errorf("dial: %w", tslocal.ErrUnavailable), errcode.TailscaleUnavailable, true, core.EffectSkipped},
		{"tailscaled fails", &tslocal.APIError{StatusCode: 500, Message: "boom"}, "", false, core.EffectIntended},
		{"the call fails without an answer", errors.New("connection reset"), "", false, core.EffectIntended},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, tt := pfSetup(t, tslocal.Prefs{WantRunning: true})
			h.local.Errs["EditPrefs"] = tc.err
			_, err := tt.prefsSet().Handler.Apply(context.Background(), h.env("c1"), json.RawMessage(`{"shieldsUp":true}`))
			if err == nil || core.IsDefinite(err) != tc.definite {
				t.Fatalf("err = %v (definite %v), want an error with definite %v", err, core.IsDefinite(err), tc.definite)
			}
			var coded *errcode.Error
			if tc.code != "" && (!errors.As(err, &coded) || coded.Code != tc.code) {
				t.Errorf("err = %v, want code %s", err, tc.code)
			}
			if effects := pfEffects(t, h); len(effects) != 1 || effects[0].State != tc.state {
				t.Errorf("effects %+v, want one %s: the effect is journaled before the call", effects, tc.state)
			}
		})
	}
}

func TestPfVerify(t *testing.T) {
	ctx := context.Background()
	h, tt := pfSetup(t, tslocal.Prefs{WantRunning: true, AdvertiseRoutes: pfPrefixes("10.1.0.0/24")})
	def, in := tt.prefsSet(), json.RawMessage(`{"shieldsUp":true,"advertiseRoutes":["10.9.0.0/24"],"exitNode":"100.64.0.9"}`)
	out, err := def.Handler.Apply(ctx, h.env("c1"), in)
	if err != nil {
		t.Fatal(err)
	}
	if err := def.Handler.Verify(ctx, h.env("c1"), in, out); err != nil {
		t.Errorf("verify after apply: %v", err)
	}
	if err := def.Handler.Verify(ctx, h.env("c1"), in, json.RawMessage(`{"prefs":{}}`)); err != nil {
		t.Errorf("verify without the output, as recovery calls it: %v", err)
	}
	h.local.Prefs.ShieldsUp = false
	err = def.Handler.Verify(ctx, h.env("c1"), in, out)
	if err == nil || !strings.Contains(err.Error(), "shieldsUp") {
		t.Errorf("verify with shields down = %v, want an error naming shieldsUp", err)
	}
	h.local.Prefs.ShieldsUp = true
	h.local.Prefs.AdvertiseRoutes = nil
	if err := def.Handler.Verify(ctx, h.env("c1"), in, out); err == nil {
		t.Error("verify passed with the routes gone, want an error")
	}
	h.local.Errs["Prefs"] = errors.New("boom")
	if err := def.Handler.Verify(ctx, h.env("c1"), in, out); err == nil {
		t.Error("verify passed with the preferences unreadable, want an error")
	}
}

func TestPfRevert(t *testing.T) {
	ctx := context.Background()
	start := tslocal.Prefs{WantRunning: true, RouteAll: true, AdvertiseRoutes: pfPrefixes("10.1.0.0/24", "0.0.0.0/0", "::/0"), RunSSH: true}
	h, tt := pfSetup(t, start)
	def := tt.prefsSet()
	in := json.RawMessage(`{"wantRunning":false,"acceptRoutes":false,"advertiseExitNode":false,"exitNode":"100.64.0.9","shieldsUp":true}`)
	snap, err := def.Handler.Snapshot(ctx, h.env("c1"), in)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"wantRunning":true,"advertiseExitNode":true,"acceptRoutes":true,"exitNode":"","shieldsUp":false}`; !pfSame(t, snap, []byte(want)) {
		t.Errorf("snapshot %s, want the previous values of exactly the fields set: %s", snap, want)
	}
	if _, err := def.Handler.Apply(ctx, h.env("c1"), in); err != nil {
		t.Fatal(err)
	}
	if h.local.Prefs.WantRunning || !h.local.Prefs.ShieldsUp {
		t.Fatalf("apply left the preferences %+v", *h.local.Prefs)
	}
	out, err := def.Handler.Revert(ctx, h.env("c1"), snap)
	if err != nil {
		t.Fatal(err)
	}
	if want := []core.EffectOutcome{{Seq: 1, State: core.EffectUndone}}; !reflect.DeepEqual(out.Effects, want) || out.Override {
		t.Errorf("outcome %+v, want the effect undone and no override", out)
	}
	if !reflect.DeepEqual(*h.local.Prefs, start) {
		t.Errorf("preferences %+v after the revert, want %+v", *h.local.Prefs, start)
	}
	if len(h.local.Edits) != 2 {
		t.Errorf("%d edits, want the apply's and the revert's", len(h.local.Edits))
	}
}

func TestPfRevertLeavesAConflict(t *testing.T) {
	ctx := context.Background()
	h, tt := pfSetup(t, tslocal.Prefs{WantRunning: true})
	def, in := tt.prefsSet(), json.RawMessage(`{"shieldsUp":true}`)
	snap, err := def.Handler.Snapshot(ctx, h.env("c1"), in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := def.Handler.Apply(ctx, h.env("c1"), in); err != nil {
		t.Fatal(err)
	}
	h.local.Prefs.ShieldsUp = false // another operator's edit after the change
	out, err := def.Handler.Revert(ctx, h.env("c1"), snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Effects) != 1 || out.Effects[0].Seq != 1 || out.Effects[0].State != core.EffectUnresolved ||
		out.Effects[0].Reason != core.ReasonConflict || out.Effects[0].Current == nil ||
		!pfSame(t, out.Effects[0].Current.State, []byte(`{"shieldsUp":false}`)) {
		t.Errorf("outcome %+v, want effect 1 unresolved as a conflict, with the state it found", out)
	}
	if len(h.local.Edits) != 1 {
		t.Errorf("%d edits, want only the apply's: a conflict is left in place", len(h.local.Edits))
	}
}

func TestPfRevertRefusesAnEffectInDoubt(t *testing.T) {
	ctx := context.Background()
	h, tt := pfSetup(t, tslocal.Prefs{WantRunning: true})
	def, in := tt.prefsSet(), json.RawMessage(`{"shieldsUp":true}`)
	snap, err := def.Handler.Snapshot(ctx, h.env("c1"), in)
	if err != nil {
		t.Fatal(err)
	}
	h.local.Errs["EditPrefs"] = errors.New("connection reset")
	if _, err := def.Handler.Apply(ctx, h.env("c1"), in); err == nil {
		t.Fatal("apply succeeded, want the failed call")
	}
	delete(h.local.Errs, "EditPrefs")
	if _, err := def.Handler.Revert(ctx, h.env("c1"), snap); err == nil {
		t.Error("revert succeeded over an intended effect, want an error")
	}
	if len(h.local.Edits) != 1 {
		t.Errorf("%d edits, want only the failed apply's: an effect in doubt blocks every write", len(h.local.Edits))
	}
}

func TestPfRevertWithNothingMade(t *testing.T) {
	ctx := context.Background()
	h, tt := pfSetup(t, tslocal.Prefs{WantRunning: true})
	def, in := tt.prefsSet(), json.RawMessage(`{"shieldsUp":true}`)
	snap, err := def.Handler.Snapshot(ctx, h.env("c1"), in)
	if err != nil {
		t.Fatal(err)
	}
	h.local.Errs["EditPrefs"] = &tslocal.APIError{StatusCode: 400, Message: "no"}
	if _, err := def.Handler.Apply(ctx, h.env("c1"), in); !core.IsDefinite(err) {
		t.Fatalf("apply = %v, want a definite refusal", err)
	}
	delete(h.local.Errs, "EditPrefs")
	out, err := def.Handler.Revert(ctx, h.env("c1"), snap)
	if err != nil || len(out.Effects) != 0 || out.Override {
		t.Errorf("revert = %+v, %v, want an empty outcome", out, err)
	}
	if len(h.local.Edits) != 1 {
		t.Errorf("%d edits, want only the refused apply's", len(h.local.Edits))
	}
}

func TestPfObserve(t *testing.T) {
	ctx := context.Background()
	h, tt := pfSetup(t, tslocal.Prefs{WantRunning: true, ShieldsUp: true})
	def := tt.prefsSet()
	e := core.Effect{Resource: "tailscale.prefs:node", Before: core.StateDoc{State: json.RawMessage(`{"runSSH":false}`)}}
	obs, err := def.Handler.Observe(ctx, h.env("c1"), e)
	if err != nil || !pfSame(t, obs.Current.State, []byte(`{"runSSH":false}`)) || obs.Matches != 1 || !obs.Exclusive {
		t.Errorf("observation %+v (%v), want only the field the effect covers, one exclusive match", obs, err)
	}
	e.Resource = "tailscale.node:self"
	if _, err := def.Handler.Observe(ctx, h.env("c1"), e); err == nil {
		t.Error("observed another resource, want an error")
	}
	e.Resource = "tailscale.prefs:node"
	h.local.Errs["Prefs"] = errors.New("boom")
	if _, err := def.Handler.Observe(ctx, h.env("c1"), e); err == nil {
		t.Error("observed with the preferences unreadable, want an error")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

```bash
mkdir -p /root/.cache/w4-tmp/21 && export TMPDIR=/root/.cache/w4-tmp/21 GOTOOLCHAIN=local
timeout 300 go test -count=1 -run 'TestPf' ./internal/modules/tailscale/
```

Expected: FAIL to build, `tt.prefsSet undefined (type ts has no field or method prefsSet)`. Quote the output.

- [ ] **Step 3: Implement `internal/modules/tailscale/prefs_set.go`**

Decisions this task embodies. The action journals one `modify` effect so the engine can observe, settle and report the change (spec 1.2), but the revert goes by the snapshot; `tailscale.link` judges a change, not a state, so the connectivity exception of spec 1.2 (a whole restore over an unresolved effect) has nothing to evaluate here: an effect in doubt blocks the revert, and `tailscale.prefs.repair` (Task 27) is the way out, as spec 4.1 says.

1. `func (t ts) prefsSet() core.ActionDef` returns `core.DefineRevertible[PrefsSetInput, PrefsResult]` with `ActionSpec{Name: "tailscale.prefs.set", Summary: "Set the node's Tailscale preferences.", Version: 1, Side: core.SideAgent, LockKeys: []string{lockKey}, RepairedBy: "tailscale.prefs.repair", Guards: []core.GuardSpec{{ID: link.GuardID, Overridable: true}}, Available: t.available}`. Set no `ApplyTimeout` (the 30 s default) and no `Class` (`DefineRevertible` sets it). The handler is the value type `prefsSetHandler{t ts}`; its Plan, Apply, Verify, Snapshot, Revert and Observe methods have value receivers and it has no Inverse, so `DefineRevertible` finds the `Observer` by type assertion. Two constants, as Contracts "Tailscale preferences" names them: `prefsEffectResource = "tailscale.prefs:node"` and `prefsCommitWait = 2 * time.Minute` (a local daemon's, spec 1.2).
2. The current preferences as `PrefsFields` are always `prefsFieldsOf(p)` (Task 14, Contracts "Tailscale preferences"; do not redeclare it). `prefsSetPick(all, mask PrefsFields) PrefsFields` keeps of `all` exactly the fields `mask` sets.
3. `prefsSetNormalize(PrefsFields) (PrefsFields, error)` is called by Plan, Snapshot, Apply and Verify. Every refusal is `request.invalid_input` marked `core.Definite`, with the message as the detail and, except for "nothing set", one `errcode.FieldError` at the path:
   - no field set: detail "set at least one preference", no field errors;
   - `advertiseRoutes[i]`, path `/advertiseRoutes/<i>`: not parsable by `netip.ParsePrefix`; a default route (`Bits() == 0`: "advertise an exit node with advertiseExitNode, not with a default route"); a prefix with bits past the mask ("<s> has non-address bits set; use <masked form>"). The accepted list is returned sorted with `netip.Prefix.Compare`, without duplicates, as canonical strings;
   - `exitNode`, path `/exitNode`, when non-empty: a value `netip.ParseAddr` accepts must satisfy `link.IsTailscaleAddr` and is returned as `Unmap().String()`; any other value must match `^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$` (a node's stable ID). The empty string clears the exit node and is kept.
4. `prefsSetPatch(cur *tslocal.Prefs, want PrefsFields) (tslocal.PrefsPatch, error)` sets only the `PrefsPatch` fields for the fields `want` sets: `WantRunning`, `ShieldsUp` and `RunSSH` as given, `RouteAll` from `acceptRoutes`. For `exitNode` it always sets both `ExitNodeID` and `ExitNodeIP`, so that one clears the other: a value `netip.ParseAddr` accepts gives `ExitNodeID` pointing at `""` and `ExitNodeIP` at the address; any other value gives `ExitNodeID` at the value and `ExitNodeIP` at the zero `netip.Addr`; `""` gives `ExitNodeID` at `""` and `ExitNodeIP` at the zero address. `AdvertiseRoutes` is set when `advertiseRoutes` or `advertiseExitNode` is set: the routes (those given, else the current non-default ones from `prefsFieldsOf(cur)`) parsed to prefixes in sorted order, then, when the exit-node flag (given, else current) is true, `0.0.0.0/0` and `::/0` appended in that order; the slice is never nil.
5. Plan (reads and writes nothing on the host): normalise the input; `lp, raw, err := t.currentLinkPrefs(ctx)`; `cur := prefsSetPick(prefsFieldsOf(raw), want)`; build the node's preferences after the change with `after := prefsSetLinkAfter(lp, prefsFieldsOf(raw), want)`: a copy of `lp` with the masked fields applied (`WantRunning`, `AcceptRoutes`, `ShieldsUp`, `RunSSH`, `AdvertiseExitNode`, `AdvertiseRoutes` parsed to prefixes), and `ExitNode` replaced by the requested value only when it differs from the current `exitNode` as `prefsFieldsOf` spells it, so asking for the exit node already in use is no change in the guard's eyes. `g, err := t.linkGuard(ctx, env, in.Guarded, lp, link.Change{Kind: "prefs", After: after})`; `PlanOutcome.Guards` is that one guard. The diff has, for each field the input sets in the order `wantRunning`, `advertiseRoutes`, `exitNode`, `advertiseExitNode`, `acceptRoutes`, `shieldsUp`, `runSSH`: one line `{Op: " ", Text: "<name>: <value> (unchanged)"}` when the value equals the current one, else `{Op: "-", Text: "<name>: <current>"}` then `{Op: "+", Text: "<name>: <new>"}`. Booleans read `true`/`false`; routes are joined with `", "` and read `none` when empty; an empty exit node reads `none`. `StateHash` is the hex SHA-256 of the JSON of `cur`. No `Confirmation`.
6. Snapshot: normalise, read the preferences through `t.d.local()`, and return the JSON of `prefsSetPick(prefsFieldsOf(raw), want)`: the previous values of exactly the fields the input sets.
7. Apply: normalise; read the preferences; build the patch; `Intend` one effect `{Kind: core.EffectModify, Resource: <tailscale.prefs:node>, Local: false, CommitWait: 2 min, Before: {State: the previous values of the fields set, as JSON of PrefsFields}, After: &{State: the requested values}}` before the call; call `EditPrefs`. On an error, `prefsSetRefusal` classifies it: `tslocal.ErrUnavailable` becomes `core.Definite(errcode.Wrap(errcode.TailscaleUnavailable, err, "tailscaled does not answer on its socket"))`; an `*tslocal.APIError` with a status from 400 to 499 becomes `core.Definite(errcode.Wrap(errcode.InvalidInput, err, "tailscaled refused the preferences: " + Message))`; for these two the effect is marked `Skipped` and the definite error is returned; any other error is returned unchanged and the effect stays `intended` for the engine to settle. On success, `Post` is the JSON of `prefsSetPick(prefsFieldsOf(returned prefs), want)`; record `Made` with the resource and that document, and return `PrefsResult{Prefs: that pick}`.
8. Verify, from the input alone: normalise, read the preferences, and compare each field the input sets with `prefsSetPick(prefsFieldsOf(...), want)`; the error names the first field that differs and both values (`tailscale: shieldsUp is false after the change, want true`). It ignores its output.
9. Observe: refuse a resource other than `tailscale.prefs:node`; decode `e.Before.State` as `PrefsFields` to learn which fields the effect covers; read the preferences and return `Observation{Current: StateDoc{State: prefsSetPick(current, covered) as JSON}, Matches: 1, Exclusive: true}`.
10. Revert(snapshot): decode the snapshot as `PrefsFields`; list the effects; an effect `intended` or `unresolved` returns an error that restores nothing ("the revert left the preferences as they are: effect N (resource) is <state>; resolve it, then repair the change"); with no `made` effect return an empty `RevertOutcome` and write nothing. For each `made` effect, `Observe` it: when `Post` is nil or its State differs from the observed State (compared as JSON values) report `EffectOutcome{Seq, State: core.EffectUnresolved, Reason: core.ReasonConflict, Current: &observed}` and leave it; otherwise it is restorable. When any effect is restorable, read the preferences, build the patch from the snapshot with `prefsSetPatch`, call `EditPrefs`, check that the masked fields of the returned preferences equal the snapshot (else return an error), and report each restored effect `EffectOutcome{Seq, State: core.EffectUndone}`. Outcomes are sorted by `Seq`; `Override` is never set.

Add no other file and do not touch `tailscale.go`; `TestManifestIsASkeleton` keeps passing.

- [ ] **Step 4: Run the tests and the task checks**

```bash
mkdir -p /root/.cache/w4-tmp/21 && export TMPDIR=/root/.cache/w4-tmp/21 GOTOOLCHAIN=local
timeout 300 go test -count=1 -run 'TestPf' -v ./internal/modules/tailscale/
timeout 600 go test -race -count=1 ./internal/modules/tailscale/...
timeout 600 go build ./... && timeout 300 go vet ./...
timeout 900 go test -count=1 ./...
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
rm -rf /root/.cache/w4-tmp/21 && go clean -testcache
```

Expected: every test of the task `--- PASS`, every package line of `go test ./...` `ok` or `?`, `go vet` and `go mod tidy -diff` print nothing, `golangci-lint` prints `0 issues.`. Record the real output.

- [ ] **Step 5: Commit**

```bash
git add internal/modules/tailscale/prefs_set.go internal/modules/tailscale/prefs_set_test.go
git commit -m "feat(tailscale): add prefs.set action"
```

### Task 22: tailscale: node.login and node.logout

**Files:**
- Create: `internal/modules/tailscale/node_login.go`
- Create: `internal/modules/tailscale/node_logout.go`
- Test: `internal/modules/tailscale/node_login_test.go`

**Interfaces:**
- Consumes: Contracts "Tailscale wire shapes" (`LoginInput`, `LoginResult`, `LogoutInput`, `LogoutResult`, and the action-spec rows of `tailscale.node.login` and `tailscale.node.logout`); "Tailscale internals" (`ts`, `deps.local`, `deps.hostname`, `localAPI`, `lockKey`, `ts.available`); "Tailscale test host" (`newTSHost`, `tsHost`, `tsLocal`, `tsUnits`, `tsJournal`); "Tailscale shared plan helpers" (`ts.linkGuard`, `ts.currentLinkPrefs`); "Link guard rules" (`link.GuardID`, `link.Change` with Kind `prefs`, `relogin` and `logout`); `core.DefineMutating`, `core.Undoer`, `core.Observer` (`internal/core/contract.go`); `tslocal.Status`, `tslocal.StartOptions`, `tslocal.Prefs`, `tslocal.APIError`, `tslocal.ErrUnavailable`.
- Produces: `func (t ts) nodeLogin() core.ActionDef` (`tailscale.node.login/v1`) and `func (t ts) nodeLogout() core.ActionDef` (`tailscale.node.logout/v1`). Task 31 adds both to `Manifest().Actions`; this task does not edit `tailscale.go`. Every package-level identifier these files add is named with the prefix `nodeLogin` or `nodeLogout` (`nodeLoginLoggedIn`, `nodeLoginSettled`, `nodeLoginDoc`, `nodeLoginFlagDoc`, `nodeLoginSelfName`, `nodeLoginRefusal`, `nodeLoginValidate`, `nodeLoginHandler`, `nodeLogoutHandler` and the constants); they are not Contracts.

**Depends on:** Task 17

The tests are module-private and use only the fakes of Contracts "Tailscale test host"; nothing here talks to a tailscaled socket or runs `tailscale` on this host. Helpers `liSetup`, `liEffects`, `liSame` and the `liFlow` wrapper are declared by this task's test file only.

- [ ] **Step 1: Write the failing test**

Create `internal/modules/tailscale/node_login_test.go` with exactly this content:

```go
package tailscale

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/tailscale/link"
	"github.com/darkraise/darkpanel/internal/platform/tslocal"
)

const liKey = "tskey-auth-kABCDEF1CNTRL-0123456789abcdefghij"

func liStatus(state string) *tslocal.Status {
	st := &tslocal.Status{BackendState: state}
	if state != "NeedsLogin" && state != "NoState" {
		st.Self = &tslocal.PeerStatus{ID: "n1", HostName: "host-1", DNSName: "host-1.tail1234.ts.net."}
		st.TailscaleIPs = []netip.Addr{netip.MustParseAddr("fd7a:115c:a1e0::7"), netip.MustParseAddr("100.64.0.7")}
	}
	return st
}

// liFlow makes the fake tailscaled behave as the real one does around a
// login: Start moves the node to Starting, which becomes Running at the
// status read numbered after+1 since Start (never when after is negative);
// Logout moves the node to NeedsLogin.
type liFlow struct {
	h       *tsHost
	after   int
	started bool
	polls   int
}

type liLocal struct {
	localAPI
	f *liFlow
}

func (l liLocal) Start(ctx context.Context, o tslocal.StartOptions) error {
	if err := l.localAPI.Start(ctx, o); err != nil {
		return err
	}
	l.f.started = true
	l.f.h.local.Status = liStatus("Starting")
	return nil
}

func (l liLocal) Status(ctx context.Context) (*tslocal.Status, error) {
	if l.f.started {
		l.f.polls++
		if l.f.after >= 0 && l.f.polls == l.f.after+1 {
			l.f.h.local.Status = liStatus("Running")
		}
	}
	return l.localAPI.Status(ctx)
}

func (l liLocal) Logout(ctx context.Context) error {
	if err := l.localAPI.Logout(ctx); err != nil {
		return err
	}
	l.f.h.local.Status = liStatus("NeedsLogin")
	return nil
}

// liSetup is a node in the backend state, behind a ts whose tailscaled
// follows the flow (after: the status reads a login takes) and whose host
// name is host-1.
func liSetup(t *testing.T, state string, prefs tslocal.Prefs, after int) (*tsHost, ts) {
	t.Helper()
	h := newTSHost(t)
	h.local.Status = liStatus(state)
	h.local.Prefs = &prefs
	if h.local.Errs == nil {
		h.local.Errs = map[string]error{}
	}
	if h.units.States == nil {
		h.units.States = map[string]core.UnitState{}
	}
	h.units.States[tailscaledUnit] = core.UnitState{LoadState: "loaded", ActiveState: "active", SubState: "running"}
	h.units.States[controllerUnit] = core.UnitState{LoadState: "not-found", ActiveState: "inactive"}
	f := &liFlow{h: h, after: after}
	tt := h.ts()
	inner := tt.d.local
	tt.d.local = func() localAPI { return liLocal{localAPI: inner(), f: f} }
	tt.d.hostname = func() (string, error) { return "host-1", nil }
	return h, tt
}

func liEffects(t *testing.T, h *tsHost) []core.Effect {
	t.Helper()
	effects, err := h.env("c1").Effects.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return effects
}

// liSame compares two JSON documents as values.
func liSame(t *testing.T, a, b []byte) bool {
	t.Helper()
	var va, vb any
	if err := json.Unmarshal(a, &va); err != nil {
		t.Fatalf("document %s: %v", a, err)
	}
	if err := json.Unmarshal(b, &vb); err != nil {
		t.Fatalf("document %s: %v", b, err)
	}
	return reflect.DeepEqual(va, vb)
}

// liDrive runs call while advancing the host's clock whenever it waits.
func liDrive(t *testing.T, h *tsHost, call func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		call()
	}()
	for deadline := time.Now().Add(10 * time.Second); ; {
		select {
		case <-done:
			return
		default:
		}
		if h.clock.Pending() > 0 {
			h.clock.Advance(nodeLoginPoll)
		} else {
			time.Sleep(time.Millisecond)
		}
		if time.Now().After(deadline) {
			t.Fatal("the call did not finish")
		}
	}
}

func liLoginIn(extra string) json.RawMessage {
	return json.RawMessage(`{"authKey":"` + liKey + `"` + extra + `}`)
}

func TestLiActionDefinitions(t *testing.T) {
	_, tt := liSetup(t, "Running", tslocal.Prefs{}, 0)
	login, logout := tt.nodeLogin(), tt.nodeLogout()
	for _, tc := range []struct {
		def   core.ActionDef
		name  string
		class core.Class
		in    reflect.Type
		out   reflect.Type
	}{
		{login, "tailscale.node.login", core.ClassCompensable, reflect.TypeFor[LoginInput](), reflect.TypeFor[LoginResult]()},
		{logout, "tailscale.node.logout", core.ClassIrreversible, reflect.TypeFor[LogoutInput](), reflect.TypeFor[LogoutResult]()},
	} {
		s := tc.def.Spec
		if s.Name != tc.name || s.Version != 1 || s.Class != tc.class || s.Side != core.SideAgent || !slices.Equal(s.LockKeys, []string{lockKey}) ||
			s.Available == nil || s.Task || s.LongRunning || s.Internal {
			t.Errorf("%s: spec %+v, want a synchronous %s agent action locking %q with an availability check", tc.name, s, tc.class, lockKey)
		}
		if want := []core.GuardSpec{{ID: link.GuardID}}; !reflect.DeepEqual(s.Guards, want) {
			t.Errorf("%s: guards %+v, want %+v", tc.name, s.Guards, want)
		}
		if tc.def.InputType != tc.in || tc.def.OutputType != tc.out {
			t.Errorf("%s: input %v and output %v, want %v and %v", tc.name, tc.def.InputType, tc.def.OutputType, tc.in, tc.out)
		}
		if h := tc.def.Handler; h.Plan == nil || h.Apply == nil || h.Verify == nil || h.Snapshot != nil || h.Revert != nil {
			t.Errorf("%s: handler lacks Plan, Apply or Verify, or has a Snapshot or Revert", tc.name)
		}
	}
	if h := login.Handler; h.Observe == nil || h.Inverse == nil {
		t.Error("login lacks Observe or Inverse, the undo handler of a compensable action")
	}
	if h := logout.Handler; h.Observe != nil || h.Inverse != nil {
		t.Error("logout has an Observe or Inverse, but an irreversible action journals nothing")
	}
}

func TestLiLoginPlan(t *testing.T) {
	line := func(op, text string) core.DiffLine { return core.DiffLine{Op: op, Text: text} }
	tests := []struct {
		name  string
		state string
		prefs tslocal.Prefs
		in    string
		want  []core.DiffLine
		guard bool
	}{
		{"a logged out node", "NeedsLogin", tslocal.Prefs{}, ``,
			[]core.DiffLine{line("-", "node: logged out"), line("+", "node: logged in with an auth key")}, true},
		{"hostname and tags", "NeedsLogin", tslocal.Prefs{}, `,"hostname":"web-1","advertiseTags":["tag:server","tag:web"]`,
			[]core.DiffLine{line("-", "node: logged out"), line("+", "node: logged in with an auth key"),
				line("+", "hostname: web-1"), line("+", "advertiseTags: tag:server, tag:web")}, true},
		{"a login again", "Running", tslocal.Prefs{WantRunning: true}, `,"relogin":true`,
			[]core.DiffLine{line("-", "node: logged in as host-1.tail1234.ts.net"), line("+", "node: logged in again with an auth key")}, true},
		{"a login again while Tailscale SSH may be the access path", "Running", tslocal.Prefs{WantRunning: true, RunSSH: true}, `,"relogin":true`,
			[]core.DiffLine{line("-", "node: logged in as host-1.tail1234.ts.net"), line("+", "node: logged in again with an auth key")}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, tt := liSetup(t, tc.state, tc.prefs, 0)
			out, err := tt.nodeLogin().Handler.Plan(context.Background(), h.env(""), liLoginIn(tc.in))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(out.Diff, tc.want) {
				t.Errorf("diff %+v, want %+v", out.Diff, tc.want)
			}
			if len(out.Guards) != 1 || out.Guards[0].ID != link.GuardID || out.Guards[0].OK != tc.guard {
				t.Errorf("guards %+v, want %s with OK %v", out.Guards, link.GuardID, tc.guard)
			}
			if b, _ := json.Marshal(out); len(out.StateHash) != 64 || out.Confirmation != "" || strings.Contains(string(b), liKey) {
				t.Errorf("plan %s, want a 64-digit hash, no confirmation and no auth key", b)
			}
			if len(h.local.Starts) != 0 {
				t.Error("plan logged the node in")
			}
		})
	}
}

func TestLiLoginRefusals(t *testing.T) {
	tests := []struct {
		name, state, in string
		code            errcode.Code
		path            string
		planOnly        bool // the engine plans again before it applies, so apply does not repeat a state check
	}{
		{"no auth key", "NeedsLogin", `{}`, errcode.InvalidInput, "/authKey", false},
		{"an auth key with a space", "NeedsLogin", `{"authKey":"tskey-auth-a b"}`, errcode.InvalidInput, "/authKey", false},
		{"a host name with a dot", "NeedsLogin", `{"authKey":"` + liKey + `","hostname":"web.1"}`, errcode.InvalidInput, "/hostname", false},
		{"a tag without the prefix", "NeedsLogin", `{"authKey":"` + liKey + `","advertiseTags":["tag:ok","server"]}`, errcode.InvalidInput, "/advertiseTags/1", false},
		{"a node that is logged in", "Running", `{"authKey":"` + liKey + `"}`, errcode.InvalidInput, "/relogin", true},
		{"a node whose backend has not started", "NoState", `{"authKey":"` + liKey + `"}`, errcode.TailscaleUnavailable, "", true},
	}
	for _, tc := range tests {
		for _, phase := range []string{"plan", "apply"} {
			if phase == "apply" && tc.planOnly {
				continue
			}
			t.Run(tc.name+" at "+phase, func(t *testing.T) {
				h, tt := liSetup(t, tc.state, tslocal.Prefs{}, 0)
				def, ctx, raw := tt.nodeLogin(), context.Background(), json.RawMessage(tc.in)
				var err error
				if phase == "plan" {
					_, err = def.Handler.Plan(ctx, h.env(""), raw)
				} else {
					_, err = def.Handler.Apply(ctx, h.env("c1"), raw)
				}
				e := errcode.From(err)
				if e.Code != tc.code || !core.IsDefinite(err) {
					t.Fatalf("err = %v (definite %v), want %s marked definite", err, core.IsDefinite(err), tc.code)
				}
				if tc.path != "" && (len(e.Fields) != 1 || e.Fields[0].Path != tc.path) {
					t.Errorf("fields %+v, want one at %q", e.Fields, tc.path)
				}
				if len(liEffects(t, h)) != 0 || len(h.local.Starts) != 0 {
					t.Errorf("effects %v and starts %v, want none", liEffects(t, h), h.local.Starts)
				}
			})
		}
	}
}

func TestLiLoginApply(t *testing.T) {
	old := tslocal.Prefs{LoggedOut: true, ShieldsUp: true, Hostname: "old", AdvertiseTags: []string{"tag:old"}}
	tests := []struct {
		name     string
		in       string
		hostname string
		tags     []string
	}{
		{"with a host name and tags", `,"hostname":"web-1","advertiseTags":["tag:server","tag:web"]`, "web-1", []string{"tag:server", "tag:web"}},
		{"keeping the host name and tags", ``, "old", []string{"tag:old"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, tt := liSetup(t, "NeedsLogin", old, 0)
			raw, err := tt.nodeLogin().Handler.Apply(context.Background(), h.env("c1"), liLoginIn(tc.in))
			if err != nil {
				t.Fatal(err)
			}
			if len(h.local.Starts) != 1 || h.local.Starts[0].AuthKey != liKey || h.local.Starts[0].UpdatePrefs == nil {
				t.Fatalf("starts %+v, want one with the auth key and the preferences", h.local.Starts)
			}
			p := h.local.Starts[0].UpdatePrefs
			if !p.WantRunning || p.LoggedOut || !p.ShieldsUp || p.Hostname != tc.hostname || !slices.Equal(p.AdvertiseTags, tc.tags) {
				t.Errorf("start preferences %+v, want running and logged in, shields up kept, host name %q, tags %v", *p, tc.hostname, tc.tags)
			}
			var res LoginResult
			if err := json.Unmarshal(raw, &res); err != nil {
				t.Fatal(err)
			}
			if want := (LoginResult{BackendState: "Running", DNSName: "host-1.tail1234.ts.net", Addresses: []string{"100.64.0.7", "fd7a:115c:a1e0::7"}}); !reflect.DeepEqual(res, want) {
				t.Errorf("output %+v, want %+v", res, want)
			}
			effects := liEffects(t, h)
			if len(effects) != 1 {
				t.Fatalf("%d effects journaled, want 1", len(effects))
			}
			e := effects[0]
			if e.Kind != core.EffectModify || e.Resource != "tailscale.node:self" || e.Local || e.CommitWait != 2*time.Minute || e.State != core.EffectMade {
				t.Errorf("effect %+v, want a made, non-local modify of tailscale.node:self with a 2 minute commit wait", e)
			}
			if !liSame(t, e.Before.State, []byte(`{"loggedIn":false}`)) || e.After == nil || !liSame(t, e.After.State, []byte(`{"loggedIn":true}`)) ||
				e.Post == nil || !liSame(t, e.Post.State, []byte(`{"loggedIn":true}`)) || !liSame(t, e.Post.Version, []byte(`{"backendState":"Running"}`)) {
				t.Errorf("effect documents before %s, after %+v, post %+v", e.Before.State, e.After, e.Post)
			}
			if b, _ := json.Marshal(effects); strings.Contains(string(b)+string(raw), liKey) {
				t.Error("the auth key reached an effect or the output")
			}
		})
	}
}

func TestLiLoginWaitsForTheNode(t *testing.T) {
	h, tt := liSetup(t, "NeedsLogin", tslocal.Prefs{}, 3)
	start := h.clock.Now()
	var res json.RawMessage
	var err error
	liDrive(t, h, func() { res, err = tt.nodeLogin().Handler.Apply(context.Background(), h.env("c1"), liLoginIn("")) })
	if err != nil {
		t.Fatal(err)
	}
	if got := h.clock.Now().Sub(start); got != 3*nodeLoginPoll {
		t.Errorf("waited %s, want three polls of %s", got, nodeLoginPoll)
	}
	if !strings.Contains(string(res), `"backendState":"Running"`) {
		t.Errorf("output %s, want the Running backend", res)
	}
}

func TestLiLoginGivesUpWaiting(t *testing.T) {
	h, tt := liSetup(t, "NeedsLogin", tslocal.Prefs{}, -1)
	start := h.clock.Now()
	var err error
	liDrive(t, h, func() { _, err = tt.nodeLogin().Handler.Apply(context.Background(), h.env("c1"), liLoginIn("")) })
	if err == nil || core.IsDefinite(err) || !strings.Contains(err.Error(), "did not log in within 20s") || !strings.Contains(err.Error(), "Starting") {
		t.Fatalf("err = %v, want an uncertain error naming the 20 s wait and the backend state", err)
	}
	if got := h.clock.Now().Sub(start); got != 20*time.Second {
		t.Errorf("waited %s, want 20s", got)
	}
	if effects := liEffects(t, h); len(effects) != 1 || effects[0].State != core.EffectIntended {
		t.Errorf("effects %+v, want one still intended: the login may yet complete", effects)
	}
}

func TestLiLoginApplyRefusals(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		code     errcode.Code // "" for an error that is not coded
		definite bool
		state    core.EffectState
	}{
		{"tailscaled refuses the key", &tslocal.APIError{StatusCode: 400, Message: "invalid key " + liKey}, errcode.InvalidInput, true, core.EffectSkipped},
		{"tailscaled is not reachable", fmt.Errorf("dial: %w", tslocal.ErrUnavailable), errcode.TailscaleUnavailable, true, core.EffectSkipped},
		{"tailscaled fails", &tslocal.APIError{StatusCode: 500, Message: "boom " + liKey}, "", false, core.EffectIntended},
		{"the call fails without an answer", errors.New("post failed for " + liKey), "", false, core.EffectIntended},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, tt := liSetup(t, "NeedsLogin", tslocal.Prefs{}, 0)
			h.local.Errs["Start"] = tc.err
			_, err := tt.nodeLogin().Handler.Apply(context.Background(), h.env("c1"), liLoginIn(""))
			if err == nil || core.IsDefinite(err) != tc.definite {
				t.Fatalf("err = %v, want an error with definite %v", err, tc.definite)
			}
			var coded *errcode.Error
			if tc.code != "" && (!errors.As(err, &coded) || coded.Code != tc.code) {
				t.Errorf("err = %v, want code %s", err, tc.code)
			}
			if strings.Contains(err.Error(), liKey) || (coded != nil && strings.Contains(coded.Detail, liKey)) {
				t.Errorf("the error carries the auth key: %v", err)
			}
			if effects := liEffects(t, h); len(effects) != 1 || effects[0].State != tc.state {
				t.Errorf("effects %+v, want one %s: the effect is journaled before the call", effects, tc.state)
			}
		})
	}
}

func TestLiLoginVerify(t *testing.T) {
	ctx := context.Background()
	h, tt := liSetup(t, "NeedsLogin", tslocal.Prefs{}, 0)
	def, in := tt.nodeLogin(), liLoginIn("")
	out, err := def.Handler.Apply(ctx, h.env("c1"), in)
	if err != nil {
		t.Fatal(err)
	}
	if err := def.Handler.Verify(ctx, h.env("c1"), in, out); err != nil {
		t.Errorf("verify after apply: %v", err)
	}
	if err := def.Handler.Verify(ctx, h.env("c1"), in, json.RawMessage(`{}`)); err != nil {
		t.Errorf("verify without the output, as recovery calls it: %v", err)
	}
	h.local.Status = liStatus("NeedsMachineAuth")
	if err := def.Handler.Verify(ctx, h.env("c1"), in, out); err != nil {
		t.Errorf("verify while the node waits for approval: %v", err)
	}
	for _, state := range []string{"NeedsLogin", "Starting", "Stopped"} {
		h.local.Status = liStatus(state)
		if err := def.Handler.Verify(ctx, h.env("c1"), in, out); err == nil {
			t.Errorf("verify passed in backend state %s, want an error", state)
		}
	}
	h.local.Errs["Status"] = errors.New("boom")
	if err := def.Handler.Verify(ctx, h.env("c1"), in, out); err == nil {
		t.Error("verify passed with tailscaled silent, want an error")
	}
}

// TestLiLoginUndo applies the login, then plays the engine's undo of the
// journaled effect: compare with the recorded state, run the inverse, compare
// with the state the undo restores.
func TestLiLoginUndo(t *testing.T) {
	tests := []struct {
		name    string
		state   string
		in      string
		logouts int
		want    string // the State the node has after the undo
	}{
		{"a node that was logged out is logged out again", "NeedsLogin", ``, 1, `{"loggedIn":false}`},
		{"a node that was logged in stays logged in", "Running", `,"relogin":true`, 0, `{"loggedIn":true}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			h, tt := liSetup(t, tc.state, tslocal.Prefs{WantRunning: true}, 0)
			def := tt.nodeLogin()
			if _, err := def.Handler.Apply(ctx, h.env("c1"), liLoginIn(tc.in)); err != nil {
				t.Fatal(err)
			}
			e := liEffects(t, h)[0]
			now, err := def.Handler.Observe(ctx, h.env("c1"), e)
			if err != nil || !liSame(t, now.Current.State, e.Post.State) || now.Matches != 1 || !now.Exclusive {
				t.Fatalf("observed %+v (%v) before the undo, want the recorded state %s", now, err, e.Post.State)
			}
			e.State, e.UndoTo = core.EffectUndoing, &core.StateDoc{State: e.Before.State}
			res, err := def.Handler.Inverse(ctx, h.env("c1"), e)
			if err != nil || res.Resource != "" {
				t.Fatalf("inverse = %+v, %v", res, err)
			}
			if h.local.Logouts != tc.logouts {
				t.Errorf("%d logouts, want %d", h.local.Logouts, tc.logouts)
			}
			after, err := def.Handler.Observe(ctx, h.env("c1"), e)
			if err != nil || !liSame(t, after.Current.State, []byte(tc.want)) || !liSame(t, after.Current.State, e.UndoTo.State) {
				t.Errorf("observed %s (%v) after the undo, want %s, the state the undo leaves", after.Current.State, err, tc.want)
			}
		})
	}
}

func TestLiLoginInverseRefusals(t *testing.T) {
	loggedOut := core.Effect{Seq: 1, Resource: "tailscale.node:self", Before: core.StateDoc{State: json.RawMessage(`{"loggedIn":false}`)}}
	tests := []struct {
		name     string
		effect   core.Effect
		err      error
		code     errcode.Code
		definite bool
	}{
		{"tailscaled refuses", loggedOut, &tslocal.APIError{StatusCode: 409, Message: "busy"}, errcode.InvalidInput, true},
		{"tailscaled is not reachable", loggedOut, fmt.Errorf("dial: %w", tslocal.ErrUnavailable), errcode.TailscaleUnavailable, true},
		{"the call fails without an answer", loggedOut, errors.New("connection reset"), "", false},
		{"a before-state without the login flag", core.Effect{Seq: 1, Before: core.StateDoc{State: json.RawMessage(`{}`)}}, nil, "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, tt := liSetup(t, "Running", tslocal.Prefs{}, 0)
			h.local.Errs["Logout"] = tc.err
			_, err := tt.nodeLogin().Handler.Inverse(context.Background(), h.env("c1"), tc.effect)
			if err == nil || core.IsDefinite(err) != tc.definite {
				t.Fatalf("err = %v, want an error with definite %v", err, tc.definite)
			}
			var coded *errcode.Error
			if tc.code != "" && (!errors.As(err, &coded) || coded.Code != tc.code) {
				t.Errorf("err = %v, want code %s", err, tc.code)
			}
		})
	}
}

func TestLiObserveRefusals(t *testing.T) {
	h, tt := liSetup(t, "Running", tslocal.Prefs{}, 0)
	def := tt.nodeLogin()
	if _, err := def.Handler.Observe(context.Background(), h.env("c1"), core.Effect{Resource: "tailscale.prefs:node"}); err == nil {
		t.Error("observed another resource, want an error")
	}
	h.local.Errs["Status"] = errors.New("boom")
	if _, err := def.Handler.Observe(context.Background(), h.env("c1"), core.Effect{Resource: "tailscale.node:self"}); err == nil {
		t.Error("observed with tailscaled silent, want an error")
	}
}

func TestLiLogoutPlan(t *testing.T) {
	h, tt := liSetup(t, "Running", tslocal.Prefs{WantRunning: true}, 0)
	out, err := tt.nodeLogout().Handler.Plan(context.Background(), h.env(""), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	want := []core.DiffLine{{Op: "-", Text: "node: logged in as host-1.tail1234.ts.net"}, {Op: "+", Text: "node: logged out"}}
	if !reflect.DeepEqual(out.Diff, want) || out.Confirmation != "host-1" || len(out.StateHash) != 64 {
		t.Errorf("plan %+v, want diff %v, the host name as the confirmation and a 64-digit hash", out, want)
	}
	if len(out.Guards) != 1 || out.Guards[0].ID != link.GuardID || !out.Guards[0].OK {
		t.Errorf("guards %+v, want %s passing", out.Guards, link.GuardID)
	}
	if h.local.Logouts != 0 {
		t.Error("plan logged the node out")
	}
}

func TestLiLogoutPlanGuard(t *testing.T) {
	h, tt := liSetup(t, "Running", tslocal.Prefs{WantRunning: true, RunSSH: true}, 0)
	out, err := tt.nodeLogout().Handler.Plan(context.Background(), h.env(""), json.RawMessage(`{}`))
	if err != nil || len(out.Guards) != 1 || out.Guards[0].ID != link.GuardID || out.Guards[0].OK || out.Guards[0].Message == "" {
		t.Errorf("plan %+v (%v), want the guard failing with a message while Tailscale SSH may be the access path", out.Guards, err)
	}
}

func TestLiLogoutRefusals(t *testing.T) {
	tests := []struct {
		name, state string
		code        errcode.Code
	}{
		{"a node that is logged out", "NeedsLogin", errcode.InvalidInput},
		{"a node whose backend has not started", "NoState", errcode.TailscaleUnavailable},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, tt := liSetup(t, tc.state, tslocal.Prefs{}, 0)
			_, err := tt.nodeLogout().Handler.Plan(context.Background(), h.env(""), json.RawMessage(`{}`))
			if e := errcode.From(err); e.Code != tc.code || !core.IsDefinite(err) {
				t.Errorf("err = %v (definite %v), want %s marked definite", err, core.IsDefinite(err), tc.code)
			}
		})
	}
}

func TestLiLogoutApplyAndVerify(t *testing.T) {
	ctx := context.Background()
	h, tt := liSetup(t, "Running", tslocal.Prefs{WantRunning: true}, 0)
	def := tt.nodeLogout()
	in := json.RawMessage(`{}`)
	if err := def.Handler.Verify(ctx, h.env("c1"), in, json.RawMessage(`{}`)); err == nil {
		t.Error("verify passed on a node that is still logged in, want an error")
	}
	out, err := def.Handler.Apply(ctx, h.env("c1"), in)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"backendState":"NeedsLogin"}` || h.local.Logouts != 1 {
		t.Errorf("output %s after %d logouts, want the NeedsLogin backend after one", out, h.local.Logouts)
	}
	if err := def.Handler.Verify(ctx, h.env("c1"), in, out); err != nil {
		t.Errorf("verify after apply: %v", err)
	}
	if err := def.Handler.Verify(ctx, h.env("c1"), in, json.RawMessage(`{}`)); err != nil {
		t.Errorf("verify without the output, as recovery calls it: %v", err)
	}
	if effects := liEffects(t, h); len(effects) != 0 {
		t.Errorf("effects %+v, want none: a logout is irreversible and journals nothing", effects)
	}
}

func TestLiLogoutApplyRefusals(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		code     errcode.Code
		definite bool
	}{
		{"tailscaled refuses", &tslocal.APIError{StatusCode: 403, Message: "no"}, errcode.InvalidInput, true},
		{"tailscaled is not reachable", fmt.Errorf("dial: %w", tslocal.ErrUnavailable), errcode.TailscaleUnavailable, true},
		{"the call fails without an answer", errors.New("connection reset"), "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, tt := liSetup(t, "Running", tslocal.Prefs{}, 0)
			h.local.Errs["Logout"] = tc.err
			_, err := tt.nodeLogout().Handler.Apply(context.Background(), h.env("c1"), json.RawMessage(`{}`))
			var coded *errcode.Error
			if err == nil || core.IsDefinite(err) != tc.definite || (tc.code != "" && (!errors.As(err, &coded) || coded.Code != tc.code)) {
				t.Errorf("err = %v (definite %v), want code %q with definite %v", err, core.IsDefinite(err), tc.code, tc.definite)
			}
		})
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

```bash
mkdir -p /root/.cache/w4-tmp/22 && export TMPDIR=/root/.cache/w4-tmp/22 GOTOOLCHAIN=local
timeout 300 go test -count=1 -run 'TestLi' ./internal/modules/tailscale/
```

Expected: FAIL to build, `tt.nodeLogin undefined (type ts has no field or method nodeLogin)`. Quote the output.

- [ ] **Step 3: Implement `node_login.go` and `node_logout.go`**

Decisions this task embodies. Both actions use the guard `tailscale.link` as a non-overridable `GuardSpec` (the Contracts table marks only `prefs.set` overridable). Login journals one effect on the resource `tailscale.node:self` whose State is only `{"loggedIn": <bool>}`; what an undo cannot restore (the backend state) is in `Version`. Logout is irreversible and journals nothing. The auth key is a secret: it appears in no effect, diff, plan, output or error text. A login never waits longer than 20 s, so it fits the default 30 s apply budget.

`node_login.go`:

1. `func (t ts) nodeLogin() core.ActionDef` returns `core.DefineMutating[LoginInput, LoginResult]` with `ActionSpec{Name: "tailscale.node.login", Summary: "Log the node in to a tailnet with an auth key.", Version: 1, Class: core.ClassCompensable, Side: core.SideAgent, LockKeys: []string{lockKey}, Guards: []core.GuardSpec{{ID: link.GuardID}}, Available: t.available}`. The handler is the value type `nodeLoginHandler{t ts}` with value-receiver methods Plan, Apply, Verify, Observe and Inverse (so `DefineMutating` finds the `Undoer`); it has no Snapshot or Revert. Constants: the resource `tailscale.node:self`; the effect's commit wait, 2 minutes; the login wait, 20 s; the poll interval, 500 ms; the tag pattern `^tag:[a-z0-9][a-z0-9-]{0,62}$`; the host name pattern, one DNS label `^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`.
2. `nodeLoginLoggedIn(st *tslocal.Status) (loggedIn, known bool)`: backend state `NoState` is `(false, false)`, `NeedsLogin` is `(false, true)`, every other state (`Stopped`, `Starting`, `Running`, `NeedsMachineAuth`) is `(true, true)`. `nodeLoginSettled(st)` is true for `Running` and `NeedsMachineAuth`. `nodeLoginDoc(st) core.StateDoc` has State `{"loggedIn": <bool>}` and Version `{"backendState": st.BackendState}`; `nodeLoginFlagDoc(bool)` has the State only. `nodeLoginSelfName(st)` is `Self.DNSName` without its trailing dot, or `this node` when there is no self or no name.
3. `nodeLoginValidate(LoginInput) error`: `request.invalid_input`, `core.Definite`, one field error with the message as detail: `/authKey` when empty or containing whitespace; `/hostname` when set and not matching the host name pattern; `/advertiseTags/<i>` for the first tag not matching the tag pattern.
4. `nodeLoginRefusal(err error, what, secret string) error`: replaces `secret` (when non-empty) by `[redacted]` in the error text and in tailscaled's message, then returns `core.Definite(errcode.Wrap(errcode.TailscaleUnavailable, <redacted cause>, "tailscaled does not answer on its socket"))` for `tslocal.ErrUnavailable`; `core.Definite(errcode.Wrap(errcode.InvalidInput, <redacted cause>, "tailscaled refused the " + what + ": " + <redacted message>))` for an `*tslocal.APIError` with a status from 400 to 499; and for anything else a new, unwrapped error `tailscale: <what>: <redacted text>` (the original is not wrapped because its text may carry the key).
5. Plan: validate; read `Status` through `t.d.local()`; `known` false is refused, `core.Definite`, `tailscale.unavailable`, "tailscaled has not finished starting; plan again in a moment"; a logged-in node without `relogin` is refused, `request.invalid_input`, `core.Definite`, with one field error at `/relogin` and the message "the node is logged in as <name>; set relogin to log it in again". Read `ts.currentLinkPrefs`; the guard's change is `link.Change{Kind: "prefs", After: <current prefs with WantRunning true>}` for a logged-out node and the same with Kind `relogin` for a logged-in one; `PlanOutcome.Guards` is `t.linkGuard(ctx, env, in.Guarded, lp, change)`. Diff, logged out: `- node: logged out`, `+ node: logged in with an auth key`; logged in: `- node: logged in as <name>`, `+ node: logged in again with an auth key`; then, when given, `+ hostname: <hostname>` and `+ advertiseTags: <tags joined with ", ">`. `StateHash` is the hex SHA-256 of `<loggedIn>|<Self.ID or empty>`. No `Confirmation`.
6. Apply: validate; read `Status` (`was` is whether it is logged in) and `Prefs`; copy the preferences, set `WantRunning` true and `LoggedOut` false, set `Hostname` when given, and replace `AdvertiseTags` with a copy of the input's when it has any (otherwise leave both as they are). `Intend` one effect `{Kind: core.EffectModify, Resource: <tailscale.node:self>, Local: false, CommitWait: 2 min, Before: nodeLoginFlagDoc(was), After: &nodeLoginFlagDoc(true)}`, then call `Start(ctx, tslocal.StartOptions{AuthKey: in.AuthKey, UpdatePrefs: &copy})`. A `Start` error goes through `nodeLoginRefusal(err, "login", in.AuthKey)`; when the result is definite the effect is marked `Skipped`; otherwise the effect stays `intended`. After a successful `Start`, poll `Status` until `nodeLoginSettled`, sleeping the poll interval between reads through `env.Clock.AfterFunc` and a channel (and returning `ctx.Err()` if the context ends first), and give up when `env.Clock.Now()` is no longer before the start of the wait plus 20 s with the error `tailscale: the node did not log in within 20s (backend state <state>)`, which is not definite and leaves the effect `intended`. When settled, record `Made` with `nodeLoginDoc(<settled status>)` and return a `LoginResult` whose `BackendState` is the settled state, whose `DNSName` is the self node's DNS name without its trailing dot (empty when there is no self) and whose `Addresses` are the node's Tailscale IPs as strings, IPv4 first (a stable sort), a non-nil empty slice when there are none.
7. Verify, from the input alone: `Status` must be `Running` or `NeedsMachineAuth`, else an error naming the backend state; a `Status` error is returned.
8. Observe: refuse a resource other than `tailscale.node:self`; return `Observation{Current: nodeLoginDoc(<Status>), Matches: 1, Exclusive: true}`.
9. Inverse (the undo): decode `e.Before.State`; a missing `loggedIn` flag is an error. When `loggedIn` was true (a relogin) do nothing and return `InverseResult{}`: the earlier identity cannot be restored. When it was false call `Logout` and return the call's error through `nodeLoginRefusal(err, "logout", "")`.

`node_logout.go`:

10. `func (t ts) nodeLogout() core.ActionDef` returns `core.DefineMutating[LogoutInput, LogoutResult]` with `ActionSpec{Name: "tailscale.node.logout", Summary: "Log the node out of its tailnet.", Version: 1, Class: core.ClassIrreversible, Side: core.SideAgent, LockKeys: []string{lockKey}, Guards: []core.GuardSpec{{ID: link.GuardID}}, Available: t.available}` and the value type `nodeLogoutHandler{t ts}` with Plan, Apply and Verify only.
11. Plan: read `Status`; `known` false and a logged-out node are refused as in step 5 (`tailscale.unavailable`, and `request.invalid_input` "the node is already logged out", both `core.Definite`); read the host name through `t.d.hostname`; the guard is `t.linkGuard(ctx, env, in.Guarded, lp, link.Change{Kind: "logout", After: lp})`. Diff: `- node: logged in as <name>`, `+ node: logged out`. `Confirmation` is the host name. `StateHash` is the hex SHA-256 of `<BackendState>|<Self.ID or empty>`.
12. Apply: call `Logout`, errors through `nodeLoginRefusal(err, "logout", "")`; return `LogoutResult{BackendState: <Status after>}`. Verify, from the input alone: the backend state must be `NeedsLogin`, else an error naming it.

Add no other file and do not touch `tailscale.go`; `TestManifestIsASkeleton` keeps passing.

- [ ] **Step 4: Run the tests and the task checks**

```bash
mkdir -p /root/.cache/w4-tmp/22 && export TMPDIR=/root/.cache/w4-tmp/22 GOTOOLCHAIN=local
timeout 300 go test -count=1 -run 'TestLi' -v ./internal/modules/tailscale/
timeout 600 go test -race -count=1 ./internal/modules/tailscale/...
timeout 600 go build ./... && timeout 300 go vet ./...
timeout 900 go test -count=1 ./...
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
rm -rf /root/.cache/w4-tmp/22 && go clean -testcache
```

Expected: every test of the task `--- PASS`, every package line of `go test ./...` `ok` or `?`, `go vet` and `go mod tidy -diff` print nothing, `golangci-lint` prints `0 issues.`. Record the real output.

- [ ] **Step 5: Commit**

```bash
git add internal/modules/tailscale/node_login.go internal/modules/tailscale/node_logout.go internal/modules/tailscale/node_login_test.go
git commit -m "feat(tailscale): add node login and logout"
```

### Task 23: tailscale: package.update

**Files:**
- Create: `internal/modules/tailscale/package_update.go`
- Test: `internal/modules/tailscale/package_update_test.go`

**Interfaces:**
- Consumes: Contracts "Tailscale wire shapes" (`PackageUpdateInput`, `PackageUpdated`, and the action-spec row of `tailscale.package.update`); "Tailscale internals" (`ts`, `deps.hostname`, `deps.osRelease`, `deps.arch`, `deps.readFile`, `lockKey`, `keyringPath`, `sourcesPath`, `ts.available`, `ts.installedVersion`, `ts.held`); "Tailscale test host" (`newTSHost`, `tsHost`, `tsRun`, `h.script`, `h.ran`, `h.reads`, `h.files`); "Tailscale shared plan helpers" (`ts.repoFiles`, `ts.ensureRepo`, `aptInstall`); `core.DefineTask`, `core.TaskMutator` (`internal/core/contract.go`, `define.go`); `platform.AptUpdateTailscale` and `platform.AptInstallTailscale` (`internal/platform/exec_rules.go`).
- Produces: `func (t ts) packageUpdate() core.ActionDef`, the constructor of `tailscale.package.update/v1`. Task 31 adds it to `Manifest().Actions`; this task does not edit `tailscale.go`. The handler type is `packageUpdateHandler`; no other package-level identifier is added.

**Depends on:** Task 20

The tests are module-private and use only the fakes of Contracts "Tailscale test host"; no test runs `apt-get`, `apt-mark` or `dpkg` on this host or writes under `/etc/apt` or `/usr/share/keyrings`. The `pu` helpers (`puStatus`, `puSetup`, `puRunner`, `puEnv`, `puVector`) are declared by this task's test file only. `puRunner` stands for apt-get: when the install vector succeeds it rewrites dpkg's status file in the host fake, because the host fake does not.

- [ ] **Step 1: Write the failing test**

Create `internal/modules/tailscale/package_update_test.go` with exactly this content:

```go
package tailscale

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform"
)

const (
	puDpkgStatus = "/var/lib/dpkg/status"
	puAptGet     = "/usr/bin/apt-get"
	puAptMark    = "/usr/bin/apt-mark"
)

// puStatus is a dpkg status file that lists tailscale in the given state at
// the given version; an empty version leaves the package out.
func puStatus(version, status string) []byte {
	var b strings.Builder
	b.WriteString("Package: base-files\nStatus: install ok installed\nArchitecture: amd64\nVersion: 13ubuntu10\n\n")
	if version != "" {
		b.WriteString("Package: tailscale\nStatus: " + status + "\nPriority: optional\nSection: net\nArchitecture: amd64\nVersion: " + version +
			"\nDescription: The easiest, most secure way to use WireGuard and 2FA.\n\n")
	}
	b.WriteString("Package: zlib1g\nStatus: install ok installed\nArchitecture: amd64\nVersion: 1:1.3.dfsg-3.1ubuntu2\n")
	return []byte(b.String())
}

// puVector is the key the host's scripted runs use: the program and its arguments.
func puVector(path string, args []string) string {
	return strings.Join(append([]string{path}, args...), " ")
}

// puSetup is a host with tailscale installed at version and a supported OS.
func puSetup(t *testing.T, version string) (*tsHost, ts) {
	t.Helper()
	h := newTSHost(t)
	if h.reads == nil {
		h.reads = map[string][]byte{}
	}
	tt := h.ts()
	h.reads[puDpkgStatus] = puStatus(version, "install ok installed")
	tt.d.osRelease = func() (osInfo, error) { return osInfo{ID: "ubuntu", Codename: "noble"}, nil }
	tt.d.arch = "amd64"
	tt.d.hostname = func() (string, error) { return "host-1", nil }
	return h, tt
}

// puRunner runs vectors on the host and, when the install vector (APT-2)
// succeeds, leaves the package at version in dpkg's status file, as apt-get
// would; an empty version leaves it uninstalled.
type puRunner struct {
	h       *tsHost
	version string
}

func (r puRunner) Run(ctx context.Context, cmd core.Cmd) (core.CmdResult, error) {
	res, err := r.h.Run(ctx, cmd)
	if err == nil && res.ExitCode == 0 && cmd.Path == puAptGet && slices.Equal(cmd.Args, platform.AptInstallTailscale()) {
		r.h.reads[puDpkgStatus] = puStatus(r.version, "install ok installed")
	}
	return res, err
}

// puEnv is the environment of the update's task unit, whose apt-get install
// leaves version installed.
func puEnv(h *tsHost, version string) core.Env {
	env := h.env("c1")
	env.Exec = puRunner{h: h, version: version}
	return env
}

func TestPuActionDefinition(t *testing.T) {
	_, tt := puSetup(t, "1.82.0")
	def := tt.packageUpdate()
	s, h := def.Spec, def.Handler
	if s.Name != "tailscale.package.update" || s.Version != 1 || s.Class != core.ClassIrreversible || s.Side != core.SideAgent ||
		!slices.Equal(s.LockKeys, []string{lockKey}) || s.ApplyTimeout != 15*time.Minute || !s.Task || !s.LongRunning || s.Available == nil || len(s.Guards) != 0 {
		t.Errorf("spec %+v, want an irreversible agent task locking %q, with a 15 minute timeout and an availability check", s, lockKey)
	}
	if def.InputType != reflect.TypeFor[PackageUpdateInput]() || def.OutputType != reflect.TypeFor[PackageUpdated]() {
		t.Errorf("input %v and output %v, want PackageUpdateInput and PackageUpdated", def.InputType, def.OutputType)
	}
	if h.Plan == nil || h.Task == nil || h.Verify == nil || h.Apply != nil || h.Observe != nil || h.Inverse != nil {
		t.Error("handler lacks Plan, Task or Verify, or has an Apply, Observe or Inverse")
	}
}

func TestPuPlan(t *testing.T) {
	h, tt := puSetup(t, "1.82.0")
	out, err := tt.packageUpdate().Handler.Plan(context.Background(), h.env(""), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	want := []core.DiffLine{
		{Op: " ", Text: "tailscale 1.82.0 is installed"},
		{Op: "+", Text: "update to the version the Tailscale repository offers (apt-get update of that repository, then apt-get install tailscale)"},
	}
	if !reflect.DeepEqual(out.Diff, want) || out.Confirmation != "host-1" || len(out.StateHash) != 64 || len(out.Guards) != 0 {
		t.Errorf("plan %+v, want diff %v, the host name as the confirmation, a 64-digit hash and no guards", out, want)
	}
	if wantRan := [][]string{{puAptMark, "showhold"}}; !reflect.DeepEqual(h.ran, wantRan) || len(h.files) != 0 {
		t.Errorf("plan ran %v and wrote %v, want only the read of the holds", h.ran, h.files)
	}
}

func TestPuPlanRefusals(t *testing.T) {
	tests := []struct {
		name  string
		setup func(h *tsHost, tt *ts)
		code  errcode.Code // "" when the module's own code is not fixed
	}{
		{"the package is not installed", func(h *tsHost, _ *ts) { h.reads[puDpkgStatus] = puStatus("", "") }, errcode.TailscaleUnavailable},
		{"only the configuration of a removed package remains", func(h *tsHost, _ *ts) {
			h.reads[puDpkgStatus] = puStatus("1.82.0", "deinstall ok config-files")
		}, errcode.TailscaleUnavailable},
		{"the package is held", func(h *tsHost, _ *ts) {
			h.script(puAptMark+" showhold", tsRun{res: core.CmdResult{Stdout: []byte("tailscale\n")}})
		}, errcode.TailscalePackageHeld},
		{"the OS cannot be read", func(_ *tsHost, tt *ts) {
			tt.d.osRelease = func() (osInfo, error) { return osInfo{}, errors.New("no os-release") }
		}, ""},
		{"the architecture has no repository", func(_ *tsHost, tt *ts) { tt.d.arch = "riscv64" }, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, tt := puSetup(t, "1.82.0")
			tc.setup(h, &tt)
			_, err := tt.packageUpdate().Handler.Plan(context.Background(), h.env(""), json.RawMessage(`{}`))
			if err == nil || !core.IsDefinite(err) || (tc.code != "" && errcode.From(err).Code != tc.code) {
				t.Errorf("err = %v (definite %v), want a definite refusal with code %q", err, core.IsDefinite(err), tc.code)
			}
			if slices.ContainsFunc(h.ran, func(v []string) bool { return v[0] == puAptGet }) || len(h.files) != 0 {
				t.Errorf("plan ran %v and wrote %v, want no apt-get and no file", h.ran, h.files)
			}
		})
	}
}

func TestPuPlanStateHash(t *testing.T) {
	hash := func(version string) string {
		t.Helper()
		h, tt := puSetup(t, version)
		out, err := tt.packageUpdate().Handler.Plan(context.Background(), h.env(""), json.RawMessage(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		return out.StateHash
	}
	first, again, other := hash("1.82.0"), hash("1.82.0"), hash("1.84.2")
	if first != again || first == other {
		t.Error("the state hash must be the same for one installed version and differ between two")
	}
}

func TestPuTaskWritesTheRepositoryAndUpdates(t *testing.T) {
	h, tt := puSetup(t, "1.82.0")
	raw, err := tt.packageUpdate().Handler.Task(context.Background(), puEnv(h, "1.90.1"), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"from":"1.82.0","to":"1.90.1"}` {
		t.Errorf("output %s, want the version before and after", raw)
	}
	wantRan := [][]string{append([]string{puAptGet}, platform.AptUpdateTailscale()...), append([]string{puAptGet}, platform.AptInstallTailscale()...)}
	if !reflect.DeepEqual(h.ran, wantRan) {
		t.Errorf("ran %v, want APT-1 then APT-2 and nothing else", h.ran)
	}
	keyring, sources, err := tt.repoFiles()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(h.files, map[string][]byte{keyringPath: keyring, sourcesPath: sources}) {
		t.Errorf("files written %v, want the keyring and the source list this host's repository needs", h.files)
	}
}

func TestPuTaskUsesTheRepositoryInPlace(t *testing.T) {
	h, tt := puSetup(t, "1.82.0")
	keyring, sources, err := tt.repoFiles()
	if err != nil {
		t.Fatal(err)
	}
	h.reads[keyringPath], h.reads[sourcesPath] = keyring, sources
	if _, err := tt.packageUpdate().Handler.Task(context.Background(), puEnv(h, "1.82.0"), json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if len(h.files) != 0 || len(h.ran) != 2 {
		t.Errorf("wrote %v and ran %v, want the files left as they are and APT-1 and APT-2 run", h.files, h.ran)
	}
}

func TestPuTaskRefusesAForeignRepository(t *testing.T) {
	h, tt := puSetup(t, "1.82.0")
	keyring, _, err := tt.repoFiles()
	if err != nil {
		t.Fatal(err)
	}
	h.reads[keyringPath], h.reads[sourcesPath] = keyring, []byte("deb https://example.com/apt stable main\n")
	_, err = tt.packageUpdate().Handler.Task(context.Background(), puEnv(h, "1.90.1"), json.RawMessage(`{}`))
	if e := errcode.From(err); e.Code != errcode.TailscaleRepoConflict {
		t.Errorf("err = %v, want %s", err, errcode.TailscaleRepoConflict)
	}
	if len(h.ran) != 0 || len(h.files) != 0 {
		t.Errorf("ran %v and wrote %v, want nothing after a conflict", h.ran, h.files)
	}
}

func TestPuTaskAptFailures(t *testing.T) {
	update := puVector(puAptGet, platform.AptUpdateTailscale())
	install := puVector(puAptGet, platform.AptInstallTailscale())
	failed := tsRun{res: core.CmdResult{ExitCode: 100, Stderr: []byte("E: Unable to locate package tailscale")}}
	tests := []struct {
		name, fails string
		wantRan     int
	}{
		{"the repository refresh fails", update, 1},
		{"the install fails", install, 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, tt := puSetup(t, "1.82.0")
			h.script(tc.fails, failed)
			if _, err := tt.packageUpdate().Handler.Task(context.Background(), puEnv(h, "1.90.1"), json.RawMessage(`{}`)); err == nil {
				t.Fatal("task succeeded, want the apt-get failure")
			}
			if len(h.ran) != tc.wantRan {
				t.Errorf("ran %v, want %d apt-get runs", h.ran, tc.wantRan)
			}
		})
	}
}

func TestPuTaskFailsWhenThePackageIsGone(t *testing.T) {
	h, tt := puSetup(t, "1.82.0")
	if _, err := tt.packageUpdate().Handler.Task(context.Background(), puEnv(h, ""), json.RawMessage(`{}`)); err == nil {
		t.Error("task succeeded with no tailscale installed afterwards, want an error")
	}
}

func TestPuVerify(t *testing.T) {
	ctx := context.Background()
	h, tt := puSetup(t, "1.82.0")
	def, in := tt.packageUpdate(), json.RawMessage(`{}`)
	out, err := def.Handler.Task(ctx, puEnv(h, "1.90.1"), in)
	if err != nil {
		t.Fatal(err)
	}
	if err := def.Handler.Verify(ctx, h.env("c1"), in, out); err != nil {
		t.Errorf("verify after the task: %v", err)
	}
	if err := def.Handler.Verify(ctx, h.env("c1"), in, json.RawMessage(`{}`)); err != nil {
		t.Errorf("verify without the output, as recovery calls it: %v", err)
	}
	h.reads[puDpkgStatus] = puStatus("1.88.0", "install ok installed")
	if err := def.Handler.Verify(ctx, h.env("c1"), in, out); err == nil || !strings.Contains(err.Error(), "1.88.0") {
		t.Errorf("verify with another version installed = %v, want an error naming it", err)
	}
	h.reads[puDpkgStatus] = puStatus("", "")
	if err := def.Handler.Verify(ctx, h.env("c1"), in, json.RawMessage(`{}`)); err == nil {
		t.Error("verify passed with the package gone, want an error")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

```bash
mkdir -p /root/.cache/w4-tmp/23 && export TMPDIR=/root/.cache/w4-tmp/23 GOTOOLCHAIN=local
timeout 300 go test -count=1 -run 'TestPu' ./internal/modules/tailscale/
```

Expected: FAIL to build, `tt.packageUpdate undefined (type ts has no field or method packageUpdate)`. Quote the output.

- [ ] **Step 3: Implement `internal/modules/tailscale/package_update.go`**

Decisions this task embodies. The update cannot know the version the repository offers before the repository is refreshed, so the plan names none; the task refreshes and installs, and reports the versions before and after. A package that is held is refused, not updated around (apt aborts a `-y` run on a held package). The update has no rollback (irreversible, typed host name); a pin (Task 24) is the transactional way to change the version.

1. `func (t ts) packageUpdate() core.ActionDef` returns `core.DefineTask[PackageUpdateInput, PackageUpdated]` with `ActionSpec{Name: "tailscale.package.update", Summary: "Update the tailscale package to the repository's current version.", Version: 1, Class: core.ClassIrreversible, Side: core.SideAgent, LockKeys: []string{lockKey}, ApplyTimeout: 15 * time.Minute, Available: t.available}`; `DefineTask` sets `Task` and `LongRunning`. The handler is the value type `packageUpdateHandler{t ts}` with Plan, Task and Verify; it has no Apply, Observe or Inverse and the action has no guards.
2. Plan (writes nothing; the only command it runs is the read of the holds): `t.installedVersion` empty is refused, `core.Definite`, `tailscale.unavailable`, "the tailscale package is not installed; install it with tailscale.package.install"; `t.held` true is refused, `core.Definite`, `tailscale.package_held`, "the tailscale package is held at <version>; release the hold before updating"; `t.repoFiles()` failing (an OS or architecture without a repository) is refused with that error marked `core.Definite`. Diff: `{Op: " ", Text: "tailscale <version> is installed"}` then `{Op: "+", Text: "update to the version the Tailscale repository offers (apt-get update of that repository, then apt-get install tailscale)"}`. `Confirmation` is the host name from `t.d.hostname`. `StateHash` is the hex SHA-256 of the installed version. No guards.
3. Task: read the installed version (`from`); `t.ensureRepo(ctx, env)` (it writes absent repository files, uses identical ones and refuses a differing one with `tailscale.repo_conflict`, before any apt-get runs); `aptInstall(ctx, env)` (APT-1 then APT-2); read the installed version again (`to`), an error "the package is not installed after the update" when it is empty; return `PackageUpdated{From: from, To: to}`. Any error is returned as it is.
4. Verify, from the input alone (recovery calls it with the zero output): the installed version must be non-empty, and equal to `out.To` when `out.To` is set; the errors name the version found.

Add no other file and do not touch `tailscale.go`; `TestManifestIsASkeleton` keeps passing.

- [ ] **Step 4: Run the tests and the task checks**

```bash
mkdir -p /root/.cache/w4-tmp/23 && export TMPDIR=/root/.cache/w4-tmp/23 GOTOOLCHAIN=local
timeout 300 go test -count=1 -run 'TestPu' -v ./internal/modules/tailscale/
timeout 600 go test -race -count=1 ./internal/modules/tailscale/...
timeout 600 go build ./... && timeout 300 go vet ./...
timeout 900 go test -count=1 ./...
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
rm -rf /root/.cache/w4-tmp/23 && go clean -testcache
```

Expected: every test of the task `--- PASS`, every package line of `go test ./...` `ok` or `?`, `go vet` and `go mod tidy -diff` print nothing, `golangci-lint` prints `0 issues.`. Record the real output.

- [ ] **Step 5: Commit**

```bash
git add internal/modules/tailscale/package_update.go internal/modules/tailscale/package_update_test.go
git commit -m "feat(tailscale): add package.update action"
```

### Task 24: tailscale: package.pin transaction

**Files:**
- Create: `internal/modules/tailscale/package_pin.go`
- Create: `internal/modules/tailscale/pin_session.go`
- Modify: `internal/modules/tailscale/deps.go` (Task 8's `deps` struct and `productionTS`)
- Modify: `internal/modules/tailscale/testhost_test.go` (Task 8's shared test host: adds the `tsSession` fake)
- Test: `internal/modules/tailscale/package_pin_test.go`

**Interfaces:**
- Consumes: Contracts "Tailscale pin" (this task writes everything it names except `Observe`, `Inverse` and `Recover`, which are Task 29's); "Tailscale internals" (`ts`, `deps`, `localAPI`, `lockKey`, `tailscaledUnit`, `debDir`, `pinCheckWindow`, `ts.available`, `ts.installedVersion`, `ts.held`, `run`, `pinRow` and the store functions `insertPin`, `loadPin`, `setPinPhase`, `setPinStaged`, `addPinStep`); "Tailscale wire shapes" (`PinInput`, `Pinned`, and the action-spec row of `tailscale.package.pin`); "Tailscale shared plan helpers" (`ts.repoFiles`, `ts.ensureRepo`, Task 20); "Tailscale test host" (`newTSHost`, `tsHost`, `tsRun`, `tsJournal`, `h.script`, `h.ran`, `h.reads`, `h.files`, `h.units`, `h.local`, `h.clock`); `core.DefineTask`, `core.TaskMutator`, `core.EffectJournal` (`internal/core/contract.go`, `define.go`); `platform.AptUpdateTailscale`, `AptStageVersion`, `AptInstallVersion`, `AptInstallDeb` (`internal/platform/exec_rules.go`); `daemon.LoadCredentials`, `daemon.ErrNotEnrolled` (`internal/agent/daemon/creds.go`); `api.NewClient`, `Client.Hello` (`internal/agent/api/client.go`); `paths.Production`, `paths.Under` (`internal/paths/paths.go`); `link.IsTailscaleAddr` (Contracts "Link guard rules", Task 4).
- Produces: `func (t ts) packagePin() core.ActionDef` (Task 31 registers it); `type packagePinHandler struct{ t ts }` with value-receiver `Plan`, `Task`, `Verify` and the method `rollback(ctx context.Context, env core.Env, p pinRow) error` of Contracts "Tailscale pin", which Task 29's `Inverse` and `Recover` call; `deps.session` and its production function `agentSession`; the test fake `tsSession` and `tsHost.session` of Contracts "Tailscale test host". Beyond the Contracts, and fixed here because Tasks 28 and 29 read the rows this task writes:
  - The pin row's `staged_deb` is the flat file name `tailscale_<prior version>_<arch>.deb` (the file sits at `debDir + "/" + name`, written with `WriteManaged`); `rollback` also accepts a `staged_deb` that is already a full path inside `debDir`. `staged_sha256` is the lower-case hex SHA-256 of the file.
  - A phase is written before the command it covers runs: `unheld` before `apt-mark unhold`, `staged` before the first `apt-get` of staging, `installed` before APT-3, `held` before `apt-mark hold`, `checking` before the wait, `done` last. A step is appended with `addPinStep` after it ran, as `ok` or `failed: <first line of the error, at most 200 characters>`. Forward steps: `unhold`, `stage-prior` (APT-1, then APT-4 of the prior version), `stage-target` (APT-4 of the new version), `copy-prior`, `install`, `hold`, `check`. Rollback steps, each appended only when it ran: `rollback-unhold`, `rollback-configure`, `rollback-install`, `rollback-hold`, `rollback-restart`, `rollback-verify`.
  - The pin effect (resource `tailscale.package:tailscale`) holds the JSON objects `{"version":"<v>","held":<bool>}` exactly in that key order.
  - Unexported names the tests use: `pinTxPoll` (2 s, the wait between two probes of the check and of the rollback's verification), `sessionAt`. Other new unexported names (all prefixed `pinTx` or `tx`) are the implementer's.

**Items:** 21

**Depends on:** Task 20

`tailscale.package.pin` (spec 4.1) installs exactly `tailscale=<version>` and holds it, as an agent-local transaction that rolls itself back when the node does not come back. This task writes the forward phases, the rollback, the plan, the verification and the production reader of the agent's controller session; Task 29 adds the undo and recovery handlers to the same handler type. The real engine refuses `Intend` for an action without an `Observe`, and the registry refuses a compensable action without an undo handler, so this action is not registered until Tasks 29 and 31; this task's tests call the handler directly with the module's fake journal. No test runs `apt-get`, `apt-mark`, `dpkg` or `systemctl`, writes under `/etc`, `/usr/share` or `/var/lib/darkpanel`, or touches the agent socket at `/run/darkpanel-agent`: every host effect goes through the test host, and the session reader is tested with `paths.Under` of a temporary directory.

- [ ] **Step 1: Write the failing tests**

First extend Task 8's test host. In `internal/modules/tailscale/testhost_test.go` make these four changes, then run `gofmt -w` on the file:

1. In `type tsHost struct`, after the `owned` field, add the field `session *tsSession // the fake of deps.session`.
2. In `newTSHost`'s returned `&tsHost{...}` literal, after `owned: map[string]bool{},` add `session: &tsSession{},`.
3. In `func (h *tsHost) ts() ts`, in the `deps{...}` literal, after `readFile:  h.readFile,` add `session:   h.session.answer,`.
4. Append at the end of the file:

```go
// tsSession is the fake of deps.session: the agent's session to the controller as the pin's transient unit sees it.
// Call i answers Answers[min(i, len(Answers)-1)]; with no answers every call answers Up true.
type tsSession struct {
	Answers []tsSessionAnswer
	Calls   int

	mu sync.Mutex
}

// tsSessionAnswer is one answer of the fake session.
type tsSessionAnswer struct {
	Up, ViaTailnet bool
	Err            error
}

func (s *tsSession) answer(context.Context) (up, viaTailnet bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := tsSessionAnswer{Up: true}
	if len(s.Answers) > 0 {
		a = s.Answers[min(s.Calls, len(s.Answers)-1)]
	}
	s.Calls++
	return a.Up, a.ViaTailnet, a.Err
}
```

(`testhost_test.go` already imports `context` and `sync`.) Then create `internal/modules/tailscale/package_pin_test.go` with exactly this content. `pnApt` stands for apt-get, apt-mark and dpkg: it keeps the installed version and the hold, puts downloaded packages in apt's archive, and records the pin row's phase as each vector starts. `pnDrive` advances the host's clock by one poll whenever the pin waits.

```go
package tailscale

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/agent/daemon"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/paths"
	"github.com/darkraise/darkpanel/internal/platform"
	"github.com/darkraise/darkpanel/internal/transport"
)

const (
	pnPrior   = "1.82.0"
	pnTarget  = "1.90.1"
	pnAptGet  = "/usr/bin/apt-get"
	pnAptMark = "/usr/bin/apt-mark"
	pnDpkg    = "/usr/bin/dpkg"
	pnArchive = "/var/cache/apt/archives"
)

func pnDebName(version string) string { return "tailscale_" + version + "_amd64.deb" }
func pnDebPath(version string) string { return debDir + "/" + pnDebName(version) }
func pnDeb(version string) []byte     { return []byte("deb-of-" + version) }

func pnSum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func pnVec(program string, args []string) string {
	return strings.Join(append([]string{program}, args...), " ")
}

func pnRan(program string, args ...string) []string { return append([]string{program}, args...) }

func pnInput(version string) json.RawMessage {
	b, err := json.Marshal(PinInput{Version: version})
	if err != nil {
		panic(err)
	}
	return b
}

// pnApt stands in for apt-get, apt-mark and dpkg on the test host. It passes every vector to the host's runner,
// which records it, refuses what the production allowlist refuses and answers scripted results, and for a vector
// that exited 0 it leaves what the real program would: the hold, the package files in apt's archive and the
// version in dpkg's status file. phases holds the phase of change c1's pin row as each vector started.
type pnApt struct {
	h       *tsHost
	version string
	held    bool
	backend map[string]string // the backend state tailscaled reports once a version is installed
	missing map[string]bool   // versions apt cannot download
	phases  []string
}

func pnNewApt(h *tsHost, version string, held bool) *pnApt {
	a := &pnApt{h: h, version: version, held: held, backend: map[string]string{}, missing: map[string]bool{}}
	a.sync()
	return a
}

func (a *pnApt) sync() {
	a.h.mu.Lock()
	defer a.h.mu.Unlock()
	a.h.reads[thDpkgPath] = thDpkgStatus(a.version)
}

func (a *pnApt) install(version string) {
	a.version = version
	a.sync()
	if state, ok := a.backend[version]; ok {
		a.h.local.update(func() { a.h.local.Status.BackendState = state })
	}
}

func (a *pnApt) Run(ctx context.Context, cmd core.Cmd) (core.CmdResult, error) {
	pin, _, _ := loadPin(ctx, a.h.db.Read(), "c1")
	a.phases = append(a.phases, pin.Phase)
	res, err := a.h.Run(ctx, cmd)
	if err != nil || res.ExitCode != 0 {
		return res, err
	}
	switch cmd.Path {
	case pnAptMark:
		switch cmd.Args[0] {
		case "showhold":
			if res.Stdout == nil && a.held {
				res.Stdout = []byte("tailscale\n")
			}
		case "hold":
			a.held = true
		case "unhold":
			a.held = false
		}
	case pnAptGet:
		last := cmd.Args[len(cmd.Args)-1]
		switch {
		case slices.Contains(cmd.Args, "--download-only"):
			version := strings.TrimPrefix(last, "tailscale=")
			if a.missing[version] {
				return core.CmdResult{ExitCode: 100, Stderr: []byte("E: Version '" + version + "' for 'tailscale' was not found\n")}, nil
			}
			a.h.mu.Lock()
			a.h.reads[pnArchive+"/"+pnDebName(version)] = pnDeb(version)
			a.h.mu.Unlock()
		case strings.HasPrefix(last, "tailscale="):
			a.install(strings.TrimPrefix(last, "tailscale="))
		case strings.HasSuffix(last, ".deb"):
			version := strings.TrimSuffix(strings.TrimPrefix(path.Base(last), "tailscale_"), "_amd64.deb")
			a.h.mu.Lock()
			got := a.h.files[last]
			a.h.mu.Unlock()
			if !bytes.Equal(got, pnDeb(version)) {
				return core.CmdResult{ExitCode: 100, Stderr: []byte("E: Sub-process /usr/bin/dpkg returned an error code (1)\n")}, nil
			}
			a.install(version)
		}
	}
	return res, nil
}

// pnSetup is a host with tailscale pnPrior installed, running, its repository files in place, and change c1's
// environment, whose runner is the pnApt.
type pnSetup struct {
	h   *tsHost
	t   ts
	apt *pnApt
	env core.Env
}

func pnNewSetup(t *testing.T, held bool) *pnSetup {
	t.Helper()
	h := newTSHost(t)
	tt := h.ts()
	keyring, sources, err := tt.repoFiles()
	if err != nil {
		t.Fatal(err)
	}
	h.reads[keyringPath], h.reads[sourcesPath] = keyring, sources
	apt := pnNewApt(h, pnPrior, held)
	env := h.env("c1")
	env.Exec = apt
	return &pnSetup{h: h, t: tt, apt: apt, env: env}
}

// planEnv is the environment of a plan: no change, so no journal.
func (s *pnSetup) planEnv() core.Env {
	env := s.h.env("")
	env.Exec = s.apt
	return env
}

// pnDrive runs call while advancing the host's clock by one poll whenever it waits; tick, when set, runs before
// each advance with the number of advances so far.
func pnDrive(t *testing.T, h *tsHost, tick func(n int), call func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		call()
	}()
	n := 0
	for deadline := time.Now().Add(20 * time.Second); ; {
		select {
		case <-done:
			return
		default:
		}
		if h.clock.Pending() > 0 {
			if tick != nil {
				tick(n)
			}
			n++
			h.clock.Advance(pinTxPoll)
		} else {
			time.Sleep(time.Millisecond)
		}
		if time.Now().After(deadline) {
			t.Fatal("the call did not finish")
		}
	}
}

// pnStep is a recorded step: its name and result.
type pnStep struct{ Step, Result string }

func pnSteps(t *testing.T, h *tsHost, id string) []pnStep {
	t.Helper()
	rows, err := h.db.Read().QueryContext(context.Background(), `SELECT step, result FROM tailscale_pin_steps WHERE change_id = ? ORDER BY seq`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []pnStep
	for rows.Next() {
		var s pnStep
		if err := rows.Scan(&s.Step, &s.Result); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// pnStepNames lists the steps' names; a result that starts with "failed" is marked with a trailing "!".
func pnStepNames(steps []pnStep) []string {
	var out []string
	for _, s := range steps {
		name := s.Step
		if strings.HasPrefix(s.Result, "failed: ") {
			name += "!"
		} else if s.Result != "ok" {
			name += "?" + s.Result
		}
		out = append(out, name)
	}
	return out
}

func pnPin(t *testing.T, h *tsHost) pinRow {
	t.Helper()
	p, ok, err := loadPin(context.Background(), h.db.Read(), "c1")
	if err != nil || !ok {
		t.Fatalf("pin row of c1: found %v, error %v", ok, err)
	}
	return p
}

// pnPinEffect returns the effect the pin journaled, whatever else the change journaled.
func pnPinEffect(t *testing.T, h *tsHost) core.Effect {
	t.Helper()
	var found []core.Effect
	for _, e := range h.journal.effects() {
		if e.Resource == "tailscale.package:tailscale" {
			found = append(found, e)
		}
	}
	if len(found) != 1 {
		t.Fatalf("%d effects on tailscale.package:tailscale, want 1: %+v", len(found), h.journal.effects())
	}
	return found[0]
}

func pnCoded(t *testing.T, err error, code errcode.Code) *errcode.Error {
	t.Helper()
	var coded *errcode.Error
	if !errors.As(err, &coded) || coded.Code != code {
		t.Fatalf("error %v, want code %s", err, code)
	}
	return coded
}

func TestPnSessionFake(t *testing.T) {
	ctx := context.Background()
	s := &tsSession{}
	if up, via, err := s.answer(ctx); !up || via || err != nil || s.Calls != 1 {
		t.Errorf("default answer = %v %v %v after %d calls, want up, not via the tailnet, no error, 1 call", up, via, err, s.Calls)
	}
	boom := errors.New("socket closed")
	s = &tsSession{Answers: []tsSessionAnswer{{Up: true, ViaTailnet: true}, {Err: boom}}}
	for i, want := range []tsSessionAnswer{{Up: true, ViaTailnet: true}, {Err: boom}, {Err: boom}} {
		up, via, err := s.answer(ctx)
		if up != want.Up || via != want.ViaTailnet || !errors.Is(err, want.Err) {
			t.Errorf("call %d = %v %v %v, want %+v", i, up, via, err, want)
		}
	}
	h := newTSHost(t)
	if up, _, err := h.ts().d.session(ctx); !up || err != nil || h.session.Calls != 1 {
		t.Errorf("the host's ts answers %v, %v after %d calls, want its session fake consulted once", up, err, h.session.Calls)
	}
}

func TestPnProductionTSWiresTheSession(t *testing.T) {
	if productionTS().d.session == nil {
		t.Fatal("productionTS has no session")
	}
}

// pnCredentials saves an enrolled agent's credentials for a controller at address in dir.
func pnCredentials(t *testing.T, dir, address string) {
	t.Helper()
	caPub, caKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test agent root"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, caPub, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	uri, err := url.Parse(transport.HostURI("h1"))
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(12 * time.Hour),
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, URIs: []*url.URL{uri}, KeyUsage: x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, pub, caKey)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	c := &daemon.Credentials{Key: key, Cert: cert, CA: ca, Controller: daemon.Endpoint{Address: address, SAN: transport.ControllerURI("C1")}}
	if _, err := daemon.SaveGeneration(dir, c, daemon.AnyCurrent); err != nil {
		t.Fatal(err)
	}
}

// pnServeAgent answers GET hello on the Unix socket with body, as the agent socket does.
func pnServeAgent(t *testing.T, socket, body string) {
	t.Helper()
	l, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/agent/v1/hello" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	_ = srv.Listener.Close()
	srv.Listener = l
	srv.Start()
	t.Cleanup(srv.Close)
}

func TestPnSessionAt(t *testing.T) {
	tests := []struct {
		name    string
		address string // "" leaves the agent not enrolled
		hello   string // "" starts no agent socket
		up, via bool
		wantErr bool
	}{
		{name: "not enrolled", up: false, via: false},
		{name: "enrolled over the tailnet and connected", address: "100.64.0.9:7443", hello: `{"controllerConnected":true}`, up: true, via: true},
		{name: "enrolled over an IPv6 tailnet address", address: "[fd7a:115c:a1e0::9]:7443", hello: `{"controllerConnected":true}`, up: true, via: true},
		{name: "enrolled over a LAN address", address: "192.0.2.5:7443", hello: `{"controllerConnected":true}`, up: true, via: false},
		{name: "enrolled by name", address: "controller.example.com:7443", hello: `{"controllerConnected":true}`, up: true, via: false},
		{name: "the controller session is down", address: "100.64.0.9:7443", hello: `{"controllerConnected":false}`, up: false, via: true},
		{name: "the agent reports no controller", address: "100.64.0.9:7443", hello: `{"controllerConnected":null}`, up: false, via: true},
		{name: "the agent socket does not answer", address: "100.64.0.9:7443", up: false, via: true, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root, err := os.MkdirTemp("", "pn")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(root) })
			p := paths.Under(root)
			if tc.address != "" {
				pnCredentials(t, p.AgentTLSDir, tc.address)
			}
			if err := os.MkdirAll(path.Dir(p.AgentSocket), 0o700); err != nil {
				t.Fatal(err)
			}
			if tc.hello != "" {
				pnServeAgent(t, p.AgentSocket, tc.hello)
			}
			up, via, err := sessionAt(context.Background(), p)
			if (err != nil) != tc.wantErr || up != tc.up || via != tc.via {
				t.Errorf("sessionAt = up %v, via %v, error %v; want up %v, via %v, error %v", up, via, err, tc.up, tc.via, tc.wantErr)
			}
		})
	}
}

func TestPnPackagePinSpec(t *testing.T) {
	def := pnNewSetup(t, false).t.packagePin()
	sp, h := def.Spec, def.Handler
	if sp.Name != "tailscale.package.pin" || sp.Version != 1 || sp.Class != core.ClassCompensable || sp.Side != core.SideAgent ||
		!slices.Equal(sp.LockKeys, []string{"tailscale"}) || sp.ApplyTimeout != 20*time.Minute || !sp.Task || !sp.LongRunning ||
		sp.Available == nil || len(sp.Guards) != 0 || sp.Internal {
		t.Errorf("spec %+v, want a long-running compensable agent task locking tailscale for 20 minutes, with an availability check and no guard", sp)
	}
	if def.InputType != reflect.TypeFor[PinInput]() || def.OutputType != reflect.TypeFor[Pinned]() {
		t.Errorf("input %v and output %v, want PinInput and Pinned", def.InputType, def.OutputType)
	}
	if h.Plan == nil || h.Task == nil || h.Verify == nil || h.Apply != nil {
		t.Errorf("handler has Plan %v Task %v Verify %v Apply %v, want a task handler", h.Plan != nil, h.Task != nil, h.Verify != nil, h.Apply != nil)
	}
}

func TestPnPlan(t *testing.T) {
	ctx := context.Background()
	line := func(op, text string) core.DiffLine { return core.DiffLine{Op: op, Text: text} }
	rollback := line(" ", "roll back to tailscale 1.82.0 if the node is not back within 120 s")
	tests := []struct {
		name    string
		held    bool
		version string
		want    []core.DiffLine
	}{
		{"a new version, not held", false, pnTarget, []core.DiffLine{
			line("-", "tailscale 1.82.0"), line("+", "tailscale 1.90.1"), line("+", "hold tailscale at 1.90.1"), rollback}},
		{"a new version, held", true, pnTarget, []core.DiffLine{
			line("-", "tailscale 1.82.0"), line("+", "tailscale 1.90.1"), line(" ", "tailscale is held now; the hold moves to 1.90.1"), rollback}},
		{"the installed version", false, pnPrior, []core.DiffLine{
			line(" ", "tailscale 1.82.0 stays installed"), line("+", "hold tailscale at 1.82.0"), rollback}},
	}
	hashes := map[string]string{}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := pnNewSetup(t, tc.held)
			out, err := s.t.packagePin().Handler.Plan(ctx, s.planEnv(), pnInput(tc.version))
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(out.Diff, tc.want) || out.Confirmation != "" || len(out.Guards) != 0 || len(out.StateHash) != 64 {
				t.Errorf("plan %+v, want diff %+v, no confirmation, no guard and a 64-digit hash", out, tc.want)
			}
			if want := [][]string{pnRan(pnAptMark, "showhold")}; !reflect.DeepEqual(s.h.ran, want) || len(s.h.files) != 0 {
				t.Errorf("plan ran %v and wrote %v, want only the read of the holds", s.h.ran, s.h.files)
			}
			again, err := s.t.packagePin().Handler.Plan(ctx, s.planEnv(), pnInput(tc.version))
			if err != nil || again.StateHash != out.StateHash {
				t.Errorf("a second plan hash %q (error %v), want %q", again.StateHash, err, out.StateHash)
			}
			if other, ok := hashes[out.StateHash]; ok {
				t.Errorf("plans %q and %q share the state hash %s", other, tc.name, out.StateHash)
			}
			hashes[out.StateHash] = tc.name
		})
	}
}

func TestPnRefusals(t *testing.T) {
	tests := []struct {
		name    string
		version string
		setup   func(s *pnSetup)
		code    errcode.Code
		field   string
	}{
		{"a version that is not a package version", "1.90.1 --reinstall", nil, errcode.InvalidInput, "/version"},
		{"a version that starts with a letter", "v1.90.1", nil, errcode.InvalidInput, "/version"},
		{"no package", pnTarget, func(s *pnSetup) { s.apt.version = ""; s.apt.sync() }, errcode.TailscaleUnavailable, ""},
		{"an OS without a repository", pnTarget, func(s *pnSetup) {
			s.t.d.osRelease = func() (osInfo, error) { return osInfo{ID: "ubuntu", Codename: "focal"}, nil }
		}, errcode.TailscaleUnavailable, ""},
		{"a prior version whose package name the allowlist would refuse", pnTarget, func(s *pnSetup) {
			s.apt.version = "1.82.0~rc1"
			s.apt.sync()
		}, errcode.TailscalePinFailed, ""},
		{"a prior version with a capital letter", pnTarget, func(s *pnSetup) { s.apt.version = "1.82.0-T1"; s.apt.sync() }, errcode.TailscalePinFailed, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := pnNewSetup(t, false)
			if tc.setup != nil {
				tc.setup(s)
			}
			h := s.t.packagePin().Handler
			_, planErr := h.Plan(context.Background(), s.planEnv(), pnInput(tc.version))
			_, taskErr := h.Task(context.Background(), s.env, pnInput(tc.version))
			for what, err := range map[string]error{"plan": planErr, "task": taskErr} {
				if !core.IsDefinite(err) {
					t.Errorf("%s: error %v is not definite", what, err)
				}
				coded := pnCoded(t, err, tc.code)
				if tc.field != "" && (len(coded.Fields) != 1 || coded.Fields[0].Path != tc.field) {
					t.Errorf("%s: fields %+v, want one at %s", what, coded.Fields, tc.field)
				}
			}
			if _, ok, _ := loadPin(context.Background(), s.h.db.Read(), "c1"); ok || len(s.h.journal.effects()) != 0 || len(s.h.files) != 0 || len(s.h.ran) != 0 {
				t.Errorf("a refusal left a pin row (%v), effects %v or files %v, or ran %v", ok, s.h.journal.effects(), s.h.files, s.h.ran)
			}
		})
	}
}

func TestPnTaskPinsAndHolds(t *testing.T) {
	for _, held := range []bool{false, true} {
		t.Run(map[bool]string{false: "not held", true: "held"}[held], func(t *testing.T) {
			ctx := context.Background()
			s := pnNewSetup(t, held)
			def := s.t.packagePin()
			raw, err := def.Handler.Task(ctx, s.env, pnInput(pnTarget))
			if err != nil {
				t.Fatal(err)
			}
			var out Pinned
			if err := json.Unmarshal(raw, &out); err != nil || out != (Pinned{Version: pnTarget, PriorVersion: pnPrior, PriorHeld: held}) {
				t.Errorf("output %s (error %v), want the new version, the prior version and the prior hold", raw, err)
			}
			wantRan := [][]string{
				pnRan(pnAptMark, "showhold"),
				pnRan(pnAptMark, "unhold", "tailscale"),
				pnRan(pnAptGet, platform.AptUpdateTailscale()...),
				pnRan(pnAptGet, platform.AptStageVersion(pnPrior)...),
				pnRan(pnAptGet, platform.AptStageVersion(pnTarget)...),
				pnRan(pnAptGet, platform.AptInstallVersion(pnTarget)...),
				pnRan(pnAptMark, "hold", "tailscale"),
			}
			if !reflect.DeepEqual(s.h.ran, wantRan) {
				t.Errorf("ran %v, want %v", s.h.ran, wantRan)
			}
			// Each phase is journaled before the vector it covers runs.
			if want := []string{"", "unheld", "staged", "staged", "staged", "installed", "held"}; !slices.Equal(s.apt.phases, want) {
				t.Errorf("pin phase at each vector %v, want %v", s.apt.phases, want)
			}
			want := pinRow{
				ChangeID: "c1", Version: pnTarget, Arch: "amd64", PriorVersion: pnPrior, PriorHeld: held, PriorBackend: "Running",
				Phase: "done", StagedDeb: pnDebName(pnPrior), StagedSHA256: pnSum(pnDeb(pnPrior)),
			}
			got := pnPin(t, s.h)
			if !got.UpdatedAt.Equal(s.h.clock.Now()) {
				t.Errorf("updated_at %v, want %v", got.UpdatedAt, s.h.clock.Now())
			}
			got.UpdatedAt = time.Time{}
			if got != want {
				t.Errorf("pin row %+v, want %+v", got, want)
			}
			steps := []string{"unhold", "stage-prior", "stage-target", "copy-prior", "install", "hold", "check"}
			if got := pnStepNames(pnSteps(t, s.h, "c1")); !slices.Equal(got, steps) {
				t.Errorf("steps %v, want %v, each ok", got, steps)
			}
			if !bytes.Equal(s.h.files[pnDebPath(pnPrior)], pnDeb(pnPrior)) || len(s.h.files) != 1 {
				t.Errorf("files %v, want only the prior package staged in %s", s.h.files, debDir)
			}
			e := pnPinEffect(t, s.h)
			wantBefore := `{"version":"1.82.0","held":` + map[bool]string{false: "false", true: "true"}[held] + `}`
			if e.Kind != core.EffectModify || !e.Local || !e.InverseLocal || e.State != core.EffectMade || string(e.Before.State) != wantBefore ||
				e.After == nil || string(e.After.State) != `{"version":"1.90.1","held":true}` || e.Post == nil || string(e.Post.State) != string(e.After.State) {
				t.Errorf("effect %+v, want a made, local modification from %s to the new version, held", e, wantBefore)
			}
			if err := def.Handler.Verify(ctx, s.env, pnInput(pnTarget), raw); err != nil {
				t.Errorf("verify after the pin: %v", err)
			}
			if err := def.Handler.Verify(ctx, s.env, pnInput(pnTarget), json.RawMessage(`{}`)); err != nil {
				t.Errorf("verify without the output, as recovery calls it: %v", err)
			}
		})
	}
}

func TestPnTaskWritesAbsentRepositoryFiles(t *testing.T) {
	s := pnNewSetup(t, false)
	delete(s.h.reads, keyringPath)
	delete(s.h.reads, sourcesPath)
	if _, err := s.t.packagePin().Handler.Task(context.Background(), s.env, pnInput(pnTarget)); err != nil {
		t.Fatal(err)
	}
	keyring, sources, _ := s.t.repoFiles()
	if !bytes.Equal(s.h.files[keyringPath], keyring) || !bytes.Equal(s.h.files[sourcesPath], sources) {
		t.Errorf("files %v, want the keyring and the source list written", s.h.files)
	}
	if pnPin(t, s.h).Phase != "done" {
		t.Error("the pin did not finish")
	}
}

func TestPnTaskRefusesAForeignRepository(t *testing.T) {
	s := pnNewSetup(t, false)
	s.h.reads[sourcesPath] = []byte("deb https://example.com/apt stable main\n")
	_, err := s.t.packagePin().Handler.Task(context.Background(), s.env, pnInput(pnTarget))
	_ = pnCoded(t, err, errcode.TailscaleRepoConflict)
	if want := [][]string{pnRan(pnAptMark, "showhold")}; !reflect.DeepEqual(s.h.ran, want) || len(s.h.files) != 0 {
		t.Errorf("ran %v and wrote %v, want only the read of the holds", s.h.ran, s.h.files)
	}
	if p := pnPin(t, s.h); p.Phase != "rolled_back" {
		t.Errorf("pin phase %s, want rolled_back: nothing had changed", p.Phase)
	}
	if got := pnSteps(t, s.h, "c1"); len(got) != 0 {
		t.Errorf("steps %v, want none", got)
	}
	if e := pnPinEffect(t, s.h); e.State != core.EffectSkipped {
		t.Errorf("effect state %s, want skipped", e.State)
	}
}

func TestPnTaskRollsBackAFailedStep(t *testing.T) {
	failed := core.CmdResult{ExitCode: 100, Stderr: []byte("E: Could not get lock\n")}
	showhold := pnRan(pnAptMark, "showhold")
	tests := []struct {
		name     string
		held     bool
		setup    func(s *pnSetup)
		failStep string
		failedAt int        // the number of vectors run up to and including the failing one
		after    [][]string // the vectors the rollback ran
		steps    []string
		restarts int
	}{
		{"the prior version cannot be downloaded", false, func(s *pnSetup) { s.apt.missing[pnPrior] = true }, "stage-prior", 4,
			[][]string{showhold, showhold},
			[]string{"unhold", "stage-prior!", "rollback-verify"}, 0},
		{"the prior version cannot be downloaded and was held", true, func(s *pnSetup) { s.apt.missing[pnPrior] = true }, "stage-prior", 4,
			[][]string{showhold, pnRan(pnAptMark, "hold", "tailscale"), showhold},
			[]string{"unhold", "stage-prior!", "rollback-hold", "rollback-verify"}, 0},
		{"the target version cannot be downloaded", false, func(s *pnSetup) { s.apt.missing[pnTarget] = true }, "stage-target", 5,
			[][]string{showhold, showhold},
			[]string{"unhold", "stage-prior", "stage-target!", "rollback-verify"}, 0},
		{"the install fails", false, func(s *pnSetup) {
			s.h.script(pnVec(pnAptGet, platform.AptInstallVersion(pnTarget)), tsRun{res: failed})
		}, "install", 6,
			[][]string{showhold, pnRan(pnDpkg, "--configure", "-a"), pnRan(pnAptGet, platform.AptInstallDeb(pnDebPath(pnPrior))...), showhold},
			[]string{"unhold", "stage-prior", "stage-target", "copy-prior", "install!", "rollback-configure", "rollback-install", "rollback-restart", "rollback-verify"}, 1},
		{"the hold fails", true, func(s *pnSetup) {
			s.h.script(pnVec(pnAptMark, []string{"hold", "tailscale"}), tsRun{res: failed})
		}, "hold", 7,
			[][]string{showhold, pnRan(pnAptGet, platform.AptInstallDeb(pnDebPath(pnPrior))...), pnRan(pnAptMark, "hold", "tailscale"), showhold},
			[]string{"unhold", "stage-prior", "stage-target", "copy-prior", "install", "hold!", "rollback-install", "rollback-hold", "rollback-restart", "rollback-verify"}, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := pnNewSetup(t, tc.held)
			tc.setup(s)
			_, err := s.t.packagePin().Handler.Task(context.Background(), s.env, pnInput(pnTarget))
			coded := pnCoded(t, err, errcode.TailscalePinFailed)
			if !strings.Contains(coded.Detail, tc.failStep) || !strings.Contains(coded.Detail, "tailscale is back at "+pnPrior) {
				t.Errorf("detail %q, want it to name %s and the restored version", coded.Detail, tc.failStep)
			}
			if len(s.h.ran) < tc.failedAt || !reflect.DeepEqual(s.h.ran[tc.failedAt:], tc.after) {
				t.Errorf("ran %v; after the first %d vectors want %v", s.h.ran, tc.failedAt, tc.after)
			}
			if got := pnStepNames(pnSteps(t, s.h, "c1")); !slices.Equal(got, tc.steps) {
				t.Errorf("steps %v, want %v", got, tc.steps)
			}
			if p := pnPin(t, s.h); p.Phase != "rolled_back" {
				t.Errorf("pin phase %s, want rolled_back", p.Phase)
			}
			if s.apt.version != pnPrior || s.apt.held != tc.held {
				t.Errorf("host has version %s, held %v; want %s, held %v", s.apt.version, s.apt.held, pnPrior, tc.held)
			}
			if len(s.h.units.Restarts) != tc.restarts {
				t.Errorf("tailscaled restarted %v, want %d restarts", s.h.units.Restarts, tc.restarts)
			}
			if e := pnPinEffect(t, s.h); e.State != core.EffectSkipped {
				t.Errorf("effect state %s, want skipped: the host is as before", e.State)
			}
		})
	}
}

func TestPnTaskRollsBackAFailedCheck(t *testing.T) {
	tests := []struct {
		name    string
		answers []tsSessionAnswer
		backend string // the backend state after the new version is installed
		link    bool
	}{
		{"the node does not return to its backend state", nil, "Stopped", false},
		{"the controller link stays down", []tsSessionAnswer{{Up: true, ViaTailnet: true}, {Up: false}}, "Running", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := pnNewSetup(t, false)
			s.h.session.Answers = tc.answers
			s.apt.backend[pnTarget], s.apt.backend[pnPrior] = tc.backend, "Running"
			start := s.h.clock.Now()
			var err error
			pnDrive(t, s.h, nil, func() {
				_, err = s.t.packagePin().Handler.Task(context.Background(), s.env, pnInput(pnTarget))
			})
			coded := pnCoded(t, err, errcode.TailscalePinFailed)
			if !strings.Contains(coded.Detail, "check") || !strings.Contains(coded.Detail, "tailscale is back at "+pnPrior) {
				t.Errorf("detail %q, want it to name the check and the restored version", coded.Detail)
			}
			if waited := s.h.clock.Now().Sub(start); waited < pinCheckWindow || waited > pinCheckWindow+2*pinTxPoll {
				t.Errorf("the check waited %v, want the %v window", waited, pinCheckWindow)
			}
			if p := pnPin(t, s.h); p.Phase != "rolled_back" || p.LinkTailnet != tc.link {
				t.Errorf("pin row %+v, want rolled_back with link_tailnet %v", p, tc.link)
			}
			want := []string{"unhold", "stage-prior", "stage-target", "copy-prior", "install", "hold", "check!",
				"rollback-unhold", "rollback-install", "rollback-restart", "rollback-verify"}
			if got := pnStepNames(pnSteps(t, s.h, "c1")); !slices.Equal(got, want) {
				t.Errorf("steps %v, want %v", got, want)
			}
			if s.apt.version != pnPrior || s.apt.held || !reflect.DeepEqual(s.h.units.Restarts, []string{tailscaledUnit}) {
				t.Errorf("host has version %s, held %v, restarts %v; want %s, not held, one restart", s.apt.version, s.apt.held, s.h.units.Restarts, pnPrior)
			}
			if slices.ContainsFunc(s.h.ran, func(v []string) bool { return v[0] == pnDpkg }) {
				t.Error("dpkg --configure -a ran for a pin whose install finished")
			}
			if e := pnPinEffect(t, s.h); e.State != core.EffectSkipped {
				t.Errorf("effect state %s, want skipped", e.State)
			}
			if tc.link && s.h.session.Calls < 3 {
				t.Errorf("the session was asked %d times, want it polled", s.h.session.Calls)
			}
		})
	}
}

func TestPnTaskWaitsForTheNodeAndTheLink(t *testing.T) {
	s := pnNewSetup(t, false)
	s.h.session.Answers = []tsSessionAnswer{{Up: true, ViaTailnet: true}, {Up: false}, {Up: true}}
	s.apt.backend[pnTarget] = "Starting"
	start := s.h.clock.Now()
	var err error
	pnDrive(t, s.h, func(n int) {
		if n == 2 {
			s.h.local.update(func() { s.h.local.Status.BackendState = "Running" })
		}
	}, func() {
		_, err = s.t.packagePin().Handler.Task(context.Background(), s.env, pnInput(pnTarget))
	})
	if err != nil {
		t.Fatal(err)
	}
	if waited := s.h.clock.Now().Sub(start); waited != 4*pinTxPoll || s.h.session.Calls != 3 {
		t.Errorf("waited %v with %d session calls, want 4 polls and 3 calls (the first records the link)", waited, s.h.session.Calls)
	}
	if p := pnPin(t, s.h); p.Phase != "done" || !p.LinkTailnet {
		t.Errorf("pin row %+v, want done with link_tailnet", p)
	}
}

func TestPnTaskReportsAFailedRollback(t *testing.T) {
	s := pnNewSetup(t, false)
	s.apt.backend[pnTarget] = "Stopped"
	s.h.script(pnVec(pnAptGet, platform.AptInstallDeb(pnDebPath(pnPrior))), tsRun{res: core.CmdResult{ExitCode: 100, Stderr: []byte("E: dpkg was interrupted\n")}})
	var err error
	pnDrive(t, s.h, nil, func() {
		_, err = s.t.packagePin().Handler.Task(context.Background(), s.env, pnInput(pnTarget))
	})
	coded := pnCoded(t, err, errcode.TailscalePinFailed)
	if !strings.Contains(coded.Detail, "rollback-install") || !strings.Contains(coded.Detail, "needs repair") {
		t.Errorf("detail %q, want the failed rollback step and a call for repair", coded.Detail)
	}
	if p := pnPin(t, s.h); p.Phase != "failed" {
		t.Errorf("pin phase %s, want failed", p.Phase)
	}
	steps := pnSteps(t, s.h, "c1")
	if last := steps[len(steps)-1]; last.Step != "rollback-install" || !strings.HasPrefix(last.Result, "failed: ") || !strings.Contains(last.Result, "dpkg was interrupted") {
		t.Errorf("last step %+v, want rollback-install failed with apt's last line", last)
	}
	if e := pnPinEffect(t, s.h); e.State != core.EffectIntended {
		t.Errorf("effect state %s, want intended: the host is not as before", e.State)
	}
}

// pnRollbackHost inserts a pin row of change c1 in the phase and, when staged, the prior package the pin copied.
func pnRollbackHost(t *testing.T, held bool, row pinRow, stage bool) *pnSetup {
	t.Helper()
	s := pnNewSetup(t, held)
	s.apt.backend[pnPrior] = "Running"
	if row.Phase != "recorded" && row.Phase != "unheld" && row.Phase != "staged" {
		s.apt.install(pnTarget)
	}
	row.ChangeID, row.Version, row.Arch, row.PriorVersion, row.PriorBackend = "c1", pnTarget, "amd64", pnPrior, "Running"
	row.UpdatedAt = s.h.clock.Now()
	if stage {
		if row.StagedDeb == "" {
			row.StagedDeb = pnDebName(pnPrior)
		}
		row.StagedSHA256 = pnSum(pnDeb(pnPrior))
		s.h.files[pnDebPath(pnPrior)] = pnDeb(pnPrior)
	}
	ctx := context.Background()
	if err := s.h.db.Write(ctx, func(tx *sql.Tx) error { return insertPin(ctx, tx, row) }); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestPnRollbackByPhase(t *testing.T) {
	showhold := pnRan(pnAptMark, "showhold")
	unhold := pnRan(pnAptMark, "unhold", "tailscale")
	hold := pnRan(pnAptMark, "hold", "tailscale")
	configure := pnRan(pnDpkg, "--configure", "-a")
	install := pnRan(pnAptGet, platform.AptInstallDeb(pnDebPath(pnPrior))...)
	tests := []struct {
		name         string
		phase        string
		held         bool // the hold when the rollback starts
		priorHeld    bool
		staged       bool
		stagedDeb    string
		wantRan      [][]string
		wantSteps    []string
		wantRestarts int
	}{
		{"recorded: nothing changed", "recorded", false, false, false, "", nil, nil, 0},
		{"unheld restores the prior hold", "unheld", false, true, false, "",
			[][]string{showhold, hold, showhold}, []string{"rollback-hold", "rollback-verify"}, 0},
		{"staged installs nothing", "staged", false, false, true, "",
			[][]string{showhold, showhold}, []string{"rollback-verify"}, 0},
		{"installed configures dpkg first", "installed", false, false, true, "",
			[][]string{showhold, configure, install, showhold}, []string{"rollback-configure", "rollback-install", "rollback-restart", "rollback-verify"}, 1},
		{"held lifts the hold", "held", true, false, true, "",
			[][]string{showhold, unhold, install, showhold}, []string{"rollback-unhold", "rollback-install", "rollback-restart", "rollback-verify"}, 1},
		{"checking, with the staged package named by its full path", "checking", true, false, true, pnDebPath(pnPrior),
			[][]string{showhold, unhold, install, showhold}, []string{"rollback-unhold", "rollback-install", "rollback-restart", "rollback-verify"}, 1},
		{"done restores a prior hold", "done", true, true, true, "",
			[][]string{showhold, unhold, install, hold, showhold}, []string{"rollback-unhold", "rollback-install", "rollback-hold", "rollback-restart", "rollback-verify"}, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := pnRollbackHost(t, tc.held, pinRow{Phase: tc.phase, PriorHeld: tc.priorHeld, StagedDeb: tc.stagedDeb}, tc.staged)
			if err := (packagePinHandler{s.t}).rollback(context.Background(), s.env, pnPin(t, s.h)); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(s.h.ran, tc.wantRan) {
				t.Errorf("ran %v, want %v", s.h.ran, tc.wantRan)
			}
			if got := pnStepNames(pnSteps(t, s.h, "c1")); !slices.Equal(got, tc.wantSteps) {
				t.Errorf("steps %v, want %v", got, tc.wantSteps)
			}
			if p := pnPin(t, s.h); p.Phase != "rolled_back" {
				t.Errorf("phase %s, want rolled_back", p.Phase)
			}
			if s.apt.version != pnPrior || s.apt.held != tc.priorHeld || len(s.h.units.Restarts) != tc.wantRestarts {
				t.Errorf("host has version %s, held %v, %d restarts; want %s, held %v, %d", s.apt.version, s.apt.held, len(s.h.units.Restarts), pnPrior, tc.priorHeld, tc.wantRestarts)
			}
		})
	}
}

func TestPnRollbackRefusesWhatItCannotTrust(t *testing.T) {
	tests := []struct {
		name  string
		row   pinRow
		stage bool
		setup func(s *pnSetup)
		want  string
	}{
		{"a staged package that is not what was recorded", pinRow{Phase: "checking"}, true,
			func(s *pnSetup) { s.h.files[pnDebPath(pnPrior)] = []byte("tampered") }, "SHA-256"},
		{"no staged package recorded", pinRow{Phase: "checking"}, false, nil, "staged"},
		{"a staged package outside the managed directory", pinRow{Phase: "checking", StagedDeb: "/tmp/tailscale_1.82.0_amd64.deb"}, true, nil, "not in " + debDir},
		{"a staged package that is gone", pinRow{Phase: "checking"}, true, func(s *pnSetup) { delete(s.h.files, pnDebPath(pnPrior)) }, "staged package"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := pnRollbackHost(t, true, tc.row, tc.stage)
			if tc.setup != nil {
				tc.setup(s)
			}
			err := (packagePinHandler{s.t}).rollback(context.Background(), s.env, pnPin(t, s.h))
			if err == nil || !strings.Contains(err.Error(), "rollback-install") || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("rollback = %v, want a failure of rollback-install naming %q", err, tc.want)
			}
			for _, v := range s.h.ran {
				if v[0] == pnAptGet {
					t.Errorf("apt-get ran %v with a package that cannot be trusted", v)
				}
			}
			if p := pnPin(t, s.h); p.Phase != "failed" {
				t.Errorf("phase %s, want failed", p.Phase)
			}
			steps := pnSteps(t, s.h, "c1")
			if last := steps[len(steps)-1]; last.Step != "rollback-install" || !strings.HasPrefix(last.Result, "failed: ") {
				t.Errorf("last step %+v, want rollback-install failed", last)
			}
		})
	}
}

func TestPnRollbackVerifyWaitsAndGivesUp(t *testing.T) {
	s := pnRollbackHost(t, true, pinRow{Phase: "checking"}, true)
	s.apt.backend[pnPrior] = "Stopped"
	start := s.h.clock.Now()
	var err error
	pnDrive(t, s.h, nil, func() { err = (packagePinHandler{s.t}).rollback(context.Background(), s.env, pnPin(t, s.h)) })
	if err == nil || !strings.Contains(err.Error(), "rollback-verify") || !strings.Contains(err.Error(), "Stopped") {
		t.Fatalf("rollback = %v, want rollback-verify to fail naming the backend state", err)
	}
	if waited := s.h.clock.Now().Sub(start); waited < pinCheckWindow || waited > pinCheckWindow+2*pinTxPoll {
		t.Errorf("waited %v, want the %v window", waited, pinCheckWindow)
	}
	if p := pnPin(t, s.h); p.Phase != "failed" {
		t.Errorf("phase %s, want failed", p.Phase)
	}
}

func TestPnVerify(t *testing.T) {
	ctx := context.Background()
	s := pnNewSetup(t, true)
	h := s.t.packagePin().Handler
	if err := h.Verify(ctx, s.env, pnInput(pnTarget), json.RawMessage(`{}`)); err == nil || !strings.Contains(err.Error(), pnPrior) {
		t.Errorf("verify with another version installed = %v, want an error naming it", err)
	}
	s.apt.install(pnTarget)
	s.apt.held = false
	if err := h.Verify(ctx, s.env, pnInput(pnTarget), json.RawMessage(`{}`)); err == nil || !strings.Contains(err.Error(), "held") {
		t.Errorf("verify without the hold = %v, want an error about the hold", err)
	}
	s.apt.held = true
	if err := h.Verify(ctx, s.env, pnInput(pnTarget), json.RawMessage(`{}`)); err != nil {
		t.Errorf("verify of a pinned host: %v", err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
mkdir -p /root/.cache/w4-tmp/24 && export TMPDIR=/root/.cache/w4-tmp/24 GOTOOLCHAIN=local
timeout 300 go test -count=1 -run 'TestPn' ./internal/modules/tailscale/ 2>&1 | head -20
```

Expected: FAIL to build, with errors such as `undefined: pinTxPoll`, `unknown field session in struct literal of type deps`, `h.ts().d.session undefined (type deps has no field or method session)`, `undefined: sessionAt` and `s.t.packagePin undefined (type ts has no field or method packagePin)`. Quote the first lines in the report.

- [ ] **Step 3: Implement**

Decisions this task embodies. The pin changes the package under a running node that may be the agent's own link, so every change is bracketed by a recorded prior state and a rollback that depends on neither the network nor the repository (the prior package is staged on disk first). Phases are written before the commands they cover, so a crash leaves the row naming the last phase that may be half done. Nothing in the module's plan or task text carries a secret. The rollback does less where nothing was installed yet: it never installs, configures or restarts for a pin that failed while staging.

1. `deps.go`: add to `deps` the field `session func(ctx context.Context) (up, viaTailnet bool, err error)`, with a comment that it is the agent's controller session as the pin's transient unit sees it, from outside the agent process; `productionTS` sets `session: agentSession`. `TestPnProductionTSWiresTheSession` pins this.
2. `pin_session.go`:
   - `const pinTxProbe = 5 * time.Second`, the bound of every single probe of tailscaled or of the session.
   - `func agentSession(ctx context.Context) (up, viaTailnet bool, err error)` is `sessionAt(ctx, paths.Production())`. (Contracts "Tailscale pin" writes `paths.Default()`, which does not exist in `internal/paths`; `paths.Production()` is the layout of an installed host and is what the transient unit runs on.)
   - `func sessionAt(ctx context.Context, p paths.Paths) (up, viaTailnet bool, err error)`: `daemon.LoadCredentials(p.AgentTLSDir)`; `daemon.ErrNotEnrolled` (use `errors.Is`) gives `false, false, nil`; any other error is returned wrapped as `tailscale: read the agent credentials: %w` with both flags false. `viaTailnet` is `link.IsTailscaleAddr` of the host part of `Controller.Address` (split with `net.SplitHostPort`) when that host parses as an IP address with `netip.ParseAddr`, else false. Then, under a context bounded by `pinTxProbe`, build `api.NewClient(api.ClientConfig{Dial: ...})` whose dial is a `net.Dialer` `DialContext` of network `unix` and address `p.AgentSocket`, with no `Controller` set, and call `Hello`. A `Hello` error is returned wrapped as `tailscale: ask the agent about its controller session: %w` with `up` false and `viaTailnet` as computed. `up` is `ControllerConnected != nil && *ControllerConnected`.
3. `package_pin.go`: constants `pinTxResource = "tailscale.package:tailscale"`, `pinTxArchive = "/var/cache/apt/archives"` and `pinTxPoll = 2 * time.Second`; two regular expressions: the version pattern `^[0-9][0-9A-Za-z.+~-]{0,63}$` and the staged-name stem pattern `^[a-z0-9][a-z0-9._-]{0,63}$`, which is the pattern the exec allowlist applies to the part before `.deb` of an APT-5 package path (`aptDebStemPattern` in `internal/platform/exec_rules.go`, unexported, so repeated here); `type packagePinHandler struct{ t ts }` with value receivers throughout.
4. `func (t ts) packagePin() core.ActionDef` is `core.DefineTask[PinInput, Pinned]` with `ActionSpec{Name: "tailscale.package.pin", Summary: <one sentence>, Version: 1, Class: core.ClassCompensable, Side: core.SideAgent, LockKeys: []string{lockKey}, ApplyTimeout: 20 * time.Minute, Available: t.available}`, no guards, and `packagePinHandler{t}`. `DefineTask` sets `Task` and `LongRunning`.
5. A shared precheck, used by `Plan` and `Task` and returning `(prior string, held bool, err error)`; every refusal is `core.Definite` and nothing is written or run before it passes. In order: `in.Version` must match the version pattern, else `request.invalid_input` with one field error at `/version`; `t.installedVersion` empty is `tailscale.unavailable`, "the tailscale package is not installed; install it with tailscale.package.install"; `t.repoFiles()` failing (an OS or architecture without a repository) is refused with that error; the staged name `tailscale_<prior>_<arch>` (arch from `t.d.arch`) must match the stem pattern, else `tailscale.pin_failed` with a detail saying the installed version cannot be staged for a rollback because its package file name is not one the exec allowlist admits (the rollback installs that file with APT-5, so a pin that could not roll back is refused); then `t.held`. The only command a refusal or a plan runs is `apt-mark showhold`.
6. `Plan`: run the precheck. Diff, in order: when the prior version differs from `in.Version`, `-` `tailscale <prior>` and `+` `tailscale <version>`, else ` ` `tailscale <prior> stays installed`; when the package is held now, ` ` `tailscale is held now; the hold moves to <version>`, else `+` `hold tailscale at <version>`; last ` ` `roll back to tailscale <prior> if the node is not back within <pinCheckWindow in whole seconds> s`. No guards, no confirmation. `StateHash` is the hex SHA-256 of the lines `installed=<prior>`, `held=<true|false>`, `version=<version>`, `arch=<arch>` joined by newlines.
7. `Task`, in this order:
   1. The precheck again. Then `Status` through `t.d.local()` under a `pinTxProbe` bound (real time) for the prior backend state: an error is `core.Definite` `tailscale.unavailable` "tailscaled does not answer on its socket" wrapping the cause. Then `t.d.session` under the same bound for `viaTailnet`: an error is returned `core.Definite`, wrapped as `tailscale: read the agent's controller session: %w`.
   2. Insert the pin row in phase `recorded` inside `env.DB.Write` with `insertPin`: `ChangeID` from `env.ChangeID`, `Arch` from `t.d.arch`, the prior version, prior hold, prior backend state, `LinkTailnet` from `viaTailnet`, `UpdatedAt` from `env.Clock`.
   3. `env.Effects.Intend` the one pin effect: `Kind` modify, `Resource` `pinTxResource`, `Local` and `InverseLocal` true, `Before` the state `{"version":"<prior>","held":<prior held>}`, `After` the state `{"version":"<version>","held":true}`. An `Intend` error runs `rollback` on the recorded row (which only marks it `rolled_back`) and is returned.
   4. `t.ensureRepo(ctx, env)`. An error runs `rollback` on the recorded row, marks the effect `Skipped`, and returns the error as it is (a repository conflict stays the `core.Definite` `tailscale.repo_conflict` Task 20 makes). Nothing has been unheld at this point.
   5. The forward steps, in order, each as: write its phase (when it has one), run it, append its step. The first failing step stops the run: `unhold` (phase `unheld`; `apt-mark unhold tailscale`); `stage-prior` (phase `staged`; APT-1, then APT-4 of the prior version); `stage-target` (APT-4 of the new version); `copy-prior` (read `pinTxArchive/tailscale_<prior>_<arch>.deb` through `t.d.readFile`, `env.Files.WriteManaged(ctx, ID, debDir, <flat name>, data, 0o600, 0, 0)`, then `setPinStaged` with the flat name and the SHA-256); `install` (phase `installed`; APT-3 of the new version); `hold` (phase `held`; `apt-mark hold tailscale`); `check` (phase `checking`; the wait below). All commands go through `run` and `env.Exec`; the engine already bounds `ctx` by the 20 minute `ApplyTimeout`.
   6. The wait, used by `check` and by `rollback-verify`: one probe at once; success returns nil; the deadline is `env.Clock.Now()` plus `pinCheckWindow`, fixed before the first probe; after a failed probe, if `env.Clock.Now()` is not before the deadline return `tailscale: not reached within <pinCheckWindow>: <the probe's error>`, else wait `pinTxPoll` on `env.Clock.AfterFunc` and a channel (returning `ctx.Err()` when the context ends first) and probe again. Each probe runs under a `pinTxProbe` bound. The check's probe needs `Status` to answer, the backend state to equal the row's `prior_backend`, and, when the row's `link_tailnet` is set, `t.d.session` to answer `up` (its error or `up` false fails the probe).
   7. On a failing forward step: reload the pin row (its phase is the step's) and call `rollback`. When the rollback succeeds, mark the pin effect `Skipped` (the host is as before) and return `errcode.Wrap(errcode.TailscalePinFailed, <cause>, "<step> failed (<reason>); tailscale is back at <prior>")` where `<reason>` is the first line of the cause. When the rollback fails, leave the effect `intended` and return `errcode.Wrap(errcode.TailscalePinFailed, <both errors joined>, "<step> failed (<reason>) and the rollback to <prior> failed (<rollback error>); tailscale needs repair")`. Neither detail carries more than the first line of a command's stderr.
   8. On success: `setPinPhase` `done`, then `Made` on the effect with its `After` document as the post-state, and return `Pinned{Version, PriorVersion, PriorHeld}`.
8. `rollback(ctx, env, p)` follows Contracts "Tailscale pin" with these exact conditions. A row in phase `recorded` becomes `rolled_back` with no step and no command. Otherwise set the phase `rolling_back` and take, in order: (1) read `t.held`; when held run `apt-mark unhold tailscale` (step `rollback-unhold`; a failure of the read is a failure of this step); (2) when `p.Phase` is `installed`, run `dpkg --configure -a` (`rollback-configure`); (3) when the install may have started, meaning `p.Phase` is one of `installed`, `held`, `checking`, `done` or `rolling_back`: resolve the staged package (an empty `staged_deb` is an error "tailscale: no prior package was staged"; a name without `/` is `debDir + "/" + name`; a path whose directory is not `debDir` is an error "tailscale: the staged package <path> is not in <debDir>"), read it through `t.d.readFile` (an error is wrapped "tailscale: read the staged package: %w"), compare its SHA-256 with `staged_sha256` (a difference is an error "tailscale: the staged package does not match its recorded SHA-256" and APT-5 does not run), then run APT-5 with that path (`rollback-install`); (4) when `p.PriorHeld`, run `apt-mark hold tailscale` (`rollback-hold`); (5) when the install may have started, `env.Units.Restart(ctx, tailscaledUnit)` (`rollback-restart`; a nil `env.Units` is an error); (6) the wait with a probe that needs `installedVersion` equal to `p.PriorVersion`, `held` equal to `p.PriorHeld` and the backend state equal to `p.PriorBackend` (`rollback-verify`). Each step appends its row, `ok` or `failed: ...`. The first failing step sets the phase `failed` and the function returns `<step name>: <cause>`; success sets `rolled_back` and returns nil. Because a step failing to journal is also a failure, a database error stops the rollback the same way.
9. `Verify`, from the input alone (recovery passes the zero output): `installedVersion` must equal `in.Version`, else an error naming the version found; `t.held` must be true, else an error saying the package is not held.
10. Add no `Observe`, `Inverse` or `Recover` (Task 29 adds them in `package_pin_recover.go`), and do not touch `tailscale.go`: `TestManifestIsASkeleton` keeps passing.

- [ ] **Step 4: Run the tests and the task checks**

```bash
mkdir -p /root/.cache/w4-tmp/24 && export TMPDIR=/root/.cache/w4-tmp/24 GOTOOLCHAIN=local
timeout 300 go test -count=1 -run 'TestPn' -v ./internal/modules/tailscale/
timeout 600 go test -race -count=1 ./internal/modules/tailscale/...
timeout 600 go build ./... && timeout 300 go vet ./...
timeout 1500 go test -count=1 ./...
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
rm -rf /root/.cache/w4-tmp/24 && go clean -testcache
```

Expected: every `TestPn` test `--- PASS`, every package line of `go test ./...` `ok` or `?`, `go vet` and `go mod tidy -diff` print nothing, `golangci-lint` prints `0 issues.`. The Unix sockets of `TestPnSessionAt` sit in a short directory under `TMPDIR`, which is why `TMPDIR` is `/root/.cache/w4-tmp/24`. Record the real output. No schema snapshot changes (the action is not registered), so `make schemas` is not run.

- [ ] **Step 5: Commit**

```bash
git add internal/modules/tailscale/package_pin.go internal/modules/tailscale/pin_session.go internal/modules/tailscale/deps.go internal/modules/tailscale/testhost_test.go internal/modules/tailscale/package_pin_test.go
git commit -m "feat(tailscale): add package.pin transaction"
```

### Task 25: tailscale: uninstall step

**Files:**
- Create: `internal/modules/tailscale/uninstall.go`
- Test: `internal/modules/tailscale/uninstall_test.go`

**Interfaces:**
- Consumes: Contracts "Tailscale uninstall" (this task writes it); "Tailscale internals" (`module`, `ts`, `ID`, `ts.installedVersion`, `ts.held`, `run`, `liveInstall`, `setInstallRemoved`, `storeTimeFormat`); "Tailscale test host" (`newTSHost`, `tsHost`, `tsRun`, `h.script`, `h.ran`, `h.reads`, `h.db`, `h.clock`, `thDpkgPath`, `thDpkgStatus`); `core.Uninstaller`, `core.UninstallOption` (`internal/core/contract.go`); `platform.AptRemoveTailscale` (APT-6, `internal/platform/exec_rules.go`); how `internal/install/uninstall.go` `runModules` calls the step (an `Env` with `DB`, `Exec`, `Files`, `Sealer`, `Clock` and `Log` only: no change, no journal, no unit client, no host id).
- Produces: `module` implements `core.Uninstaller` (`UninstallOptions`, `Uninstall` of Contracts "Tailscale uninstall"), which the core finds by a type assertion on the `Module`, so Task 31 does not wire it. New unexported name: `uninstallAptTimeout`.

**Items:** 21

**Depends on:** Task 20

`darkpanel uninstall` runs each module's `Uninstaller` before it removes the module's files (spec 1.11). The tailscale module declares one option, `package` (`keep` or `remove`, default `keep`), and with `remove` takes out the tailscale package only if Darkpanel installed it, which `tailscale_installs` (written by `tailscale.package.install` and by Task 26's `Join`) records. By default an uninstall therefore never removes a tailscaled the owner installed, and never cuts a tailnet SSH session. No test runs `apt-get` or `apt-mark` on this host: every command goes through the test host, which also refuses what the production allowlist does not admit.

- [ ] **Step 1: Write the failing tests**

Create `internal/modules/tailscale/uninstall_test.go` with exactly this content. `unEnv` is the environment `runModules` builds. `unDeadlines` records how long the context of each command had left, because the production runner gives a command whose context has no deadline only 30 seconds, and `apt-get remove` can take longer.

```go
package tailscale

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/platform"
)

const (
	unAptGet  = "/usr/bin/apt-get"
	unAptMark = "/usr/bin/apt-mark"
)

var (
	unShowhold = []string{unAptMark, "showhold"}
	unUnhold   = []string{unAptMark, "unhold", "tailscale"}
	unHold     = []string{unAptMark, "hold", "tailscale"}
	unRemove   = append([]string{unAptGet}, platform.AptRemoveTailscale()...)
)

// unEnv is the environment the uninstall gives a module: a database, the runner, the file provider, the sealer and
// the clock, with no change, no journal and no unit client.
func unEnv(h *tsHost) core.Env {
	return core.Env{
		DB: h.db, Exec: h, Files: h, Sealer: coretest.Sealer{}, Clock: h.clock,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

// unDeadlines records, by vector, how long the context of each run had left.
type unDeadlines struct {
	next core.Runner
	left map[string]time.Duration
}

func (r unDeadlines) Run(ctx context.Context, cmd core.Cmd) (core.CmdResult, error) {
	if dl, ok := ctx.Deadline(); ok {
		r.left[strings.Join(append([]string{cmd.Path}, cmd.Args...), " ")] = time.Until(dl)
	}
	return r.next.Run(ctx, cmd)
}

// unInstalled records the install the way package.install does.
func unInstalled(t *testing.T, h *tsHost) {
	t.Helper()
	ctx := context.Background()
	if err := h.db.Write(ctx, func(tx *sql.Tx) error { return insertInstall(ctx, tx, "c-install", "1.104.1", h.clock.Now()) }); err != nil {
		t.Fatal(err)
	}
}

func unRemovedAt(t *testing.T, h *tsHost) string {
	t.Helper()
	var at string
	if err := h.db.Read().QueryRowContext(context.Background(), `SELECT removed_at FROM tailscale_installs WHERE change_id = 'c-install'`).Scan(&at); err != nil {
		t.Fatal(err)
	}
	return at
}

func unHeld(h *tsHost) {
	h.script(strings.Join(unShowhold, " "), tsRun{res: core.CmdResult{Stdout: []byte("other\ntailscale\n")}})
}

func unOpts(v string) map[string]string { return map[string]string{"package": v} }

func TestUnOptions(t *testing.T) {
	var _ core.Uninstaller = module{}
	want := []core.UninstallOption{{Key: "package", Values: []string{"keep", "remove"}, Default: "keep", Help: "remove the tailscale package if Darkpanel installed it"}}
	if got := (module{}).UninstallOptions(); !reflect.DeepEqual(got, want) {
		t.Errorf("options %+v, want %+v", got, want)
	}
}

func TestUnKeepChangesNothing(t *testing.T) {
	for _, keepData := range []bool{false, true} {
		h := newTSHost(t)
		unInstalled(t, h)
		if err := (module{t: h.ts()}).Uninstall(context.Background(), unEnv(h), keepData, unOpts("keep")); err != nil {
			t.Fatal(err)
		}
		if len(h.ran) != 0 || unRemovedAt(t, h) != "" {
			t.Errorf("keepData %v: ran %v and removed_at %q, want nothing changed", keepData, h.ran, unRemovedAt(t, h))
		}
	}
}

func TestUnRemove(t *testing.T) {
	tests := []struct {
		name     string
		held     bool
		keepData bool
		want     [][]string
	}{
		{"the package is not held", false, false, [][]string{unShowhold, unRemove}},
		{"the package is held", true, false, [][]string{unShowhold, unUnhold, unRemove}},
		{"keepData does not change it", true, true, [][]string{unShowhold, unUnhold, unRemove}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newTSHost(t)
			unInstalled(t, h)
			if tc.held {
				unHeld(h)
			}
			env := unEnv(h)
			left := map[string]time.Duration{}
			env.Exec = unDeadlines{next: h, left: left}
			m := module{t: h.ts()}
			if err := m.Uninstall(context.Background(), env, tc.keepData, unOpts("remove")); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(h.ran, tc.want) {
				t.Errorf("ran %v, want %v", h.ran, tc.want)
			}
			if got, want := unRemovedAt(t, h), h.clock.Now().UTC().Format(storeTimeFormat); got != want {
				t.Errorf("removed_at %q, want %q", got, want)
			}
			if d := left[strings.Join(unRemove, " ")]; d <= 4*time.Minute || d > 5*time.Minute {
				t.Errorf("apt-get remove had %v left, want a context bounded by 5 minutes (the runner's own default is 30 s)", d)
			}
			// A second uninstall finds nothing to do.
			h.ran = nil
			if err := m.Uninstall(context.Background(), unEnv(h), tc.keepData, unOpts("remove")); err != nil || len(h.ran) != 0 {
				t.Errorf("a second uninstall = %v and ran %v, want success and nothing run", err, h.ran)
			}
		})
	}
}

func TestUnRemoveLeavesAPackageDarkpanelDidNotInstall(t *testing.T) {
	h := newTSHost(t)
	if err := (module{t: h.ts()}).Uninstall(context.Background(), unEnv(h), false, unOpts("remove")); err != nil {
		t.Fatal(err)
	}
	if len(h.ran) != 0 {
		t.Errorf("ran %v with no install recorded, want nothing", h.ran)
	}
}

func TestUnRemoveWhenThePackageIsAlreadyGone(t *testing.T) {
	h := newTSHost(t)
	unInstalled(t, h)
	h.reads[thDpkgPath] = thDpkgStatus("")
	if err := (module{t: h.ts()}).Uninstall(context.Background(), unEnv(h), false, unOpts("remove")); err != nil {
		t.Fatal(err)
	}
	if len(h.ran) != 0 || unRemovedAt(t, h) == "" {
		t.Errorf("ran %v and removed_at %q, want no command and the install recorded as removed", h.ran, unRemovedAt(t, h))
	}
}

func TestUnRemoveFailure(t *testing.T) {
	failed := tsRun{res: core.CmdResult{ExitCode: 100, Stderr: []byte("W: x\nE: Could not get lock /var/lib/dpkg/lock\n")}}
	tests := []struct {
		name string
		held bool
		want [][]string
	}{
		{"not held", false, [][]string{unShowhold, unRemove}},
		{"held: the hold is put back", true, [][]string{unShowhold, unUnhold, unRemove, unHold}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newTSHost(t)
			unInstalled(t, h)
			if tc.held {
				unHeld(h)
			}
			h.script(strings.Join(unRemove, " "), failed)
			m := module{t: h.ts()}
			err := m.Uninstall(context.Background(), unEnv(h), false, unOpts("remove"))
			if err == nil || !strings.HasPrefix(err.Error(), "tailscale:") || !strings.Contains(err.Error(), "Could not get lock") {
				t.Fatalf("error %v, want the apt-get failure", err)
			}
			if !reflect.DeepEqual(h.ran, tc.want) || unRemovedAt(t, h) != "" {
				t.Errorf("ran %v and removed_at %q, want %v and the install still live", h.ran, unRemovedAt(t, h), tc.want)
			}
			// Running the uninstall again finishes it.
			h.ran = nil
			if err := m.Uninstall(context.Background(), unEnv(h), false, unOpts("remove")); err != nil || unRemovedAt(t, h) == "" {
				t.Errorf("the second uninstall = %v with removed_at %q, want success", err, unRemovedAt(t, h))
			}
		})
	}
}

func TestUnRefusesAnOptionItDoesNotDeclare(t *testing.T) {
	for _, opts := range []map[string]string{unOpts("purge"), unOpts(""), {}} {
		h := newTSHost(t)
		unInstalled(t, h)
		err := (module{t: h.ts()}).Uninstall(context.Background(), unEnv(h), false, opts)
		if err == nil || !strings.HasPrefix(err.Error(), "tailscale:") || !strings.Contains(err.Error(), "package") {
			t.Errorf("options %v: error %v, want a refusal naming the option", opts, err)
		}
		if len(h.ran) != 0 || unRemovedAt(t, h) != "" {
			t.Errorf("options %v: ran %v, removed_at %q, want nothing changed", opts, h.ran, unRemovedAt(t, h))
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
mkdir -p /root/.cache/w4-tmp/25 && export TMPDIR=/root/.cache/w4-tmp/25 GOTOOLCHAIN=local
timeout 300 go test -count=1 -run 'TestUn' ./internal/modules/tailscale/ 2>&1 | head -20
```

Expected: FAIL to build, with `cannot use module{} (value of struct type module) as core.Uninstaller value in variable declaration: module does not implement core.Uninstaller (missing method Uninstall)`, `(module{}).UninstallOptions undefined (type module has no field or method UninstallOptions)` and `m.Uninstall undefined`. Quote the first lines in the report.

- [ ] **Step 3: Implement `internal/modules/tailscale/uninstall.go`**

Decisions this task embodies. `remove` acts only on the newest live `tailscale_installs` row, so a package the owner installed is left. The step must succeed when run again, and an uninstall that stopped at a later step is run anew, so it keys on the row and on the host, not on what an earlier run did. `keepData` does not change it: the module's tables are removed with the agent database, and the package is not Darkpanel's data.

1. Declare `const uninstallAptTimeout = 5 * time.Minute`.
2. `func (m module) UninstallOptions() []core.UninstallOption` returns exactly one option: `Key` `package`, `Values` `[]string{"keep", "remove"}`, `Default` `keep`, `Help` `remove the tailscale package if Darkpanel installed it`.
3. `func (m module) Uninstall(ctx context.Context, env core.Env, _ bool, opts map[string]string) error` (the `keepData` parameter is unused; name it `_`):
   1. `opts["package"]` `keep`: return nil before reading anything. `remove`: continue. Any other value, including a missing key: return the error `tailscale: uninstall option package=%q is not keep or remove` (with the value) and run and change nothing.
   2. `liveInstall(ctx, env.DB.Read())`: an error is returned wrapped (`tailscale: uninstall: %w`); no live row returns nil without running anything.
   3. `m.t.installedVersion(ctx, env)`. When it is empty the package is already gone (removed by hand, or by an earlier run that stopped before the next step): skip every command and go to step 5.
   4. Bound the context with `uninstallAptTimeout`. Read `m.t.held`; when held, run `apt-mark unhold tailscale`. Run APT-6 (`platform.AptRemoveTailscale()`) with `/usr/bin/apt-get`. All through `run` and `env.Exec`. When APT-6 fails and the package had been held, run `apt-mark hold tailscale` to put the hold back and join a failure of that to the APT-6 error with `errors.Join`; return the error, leaving the row live so that running the uninstall again retries.
   5. `setInstallRemoved(ctx, env.DB, <the row's change id>, env.Clock.Now())` and return its error.
4. Do not touch `tailscale.go` (the manifest still declares nothing until Task 31) or any file outside this task's two.

- [ ] **Step 4: Run the tests and the task checks**

```bash
mkdir -p /root/.cache/w4-tmp/25 && export TMPDIR=/root/.cache/w4-tmp/25 GOTOOLCHAIN=local
timeout 300 go test -count=1 -run 'TestUn' -v ./internal/modules/tailscale/
timeout 600 go test -race -count=1 ./internal/modules/tailscale/...
timeout 600 go build ./... && timeout 300 go vet ./...
timeout 1500 go test -count=1 ./...
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
rm -rf /root/.cache/w4-tmp/25 && go clean -testcache
```

Expected: every `TestUn` test `--- PASS`, every package line of `go test ./...` `ok` or `?`, `go vet` and `go mod tidy -diff` print nothing, `golangci-lint` prints `0 issues.`. Record the real output. No schema snapshot changes.

- [ ] **Step 5: Commit**

```bash
git add internal/modules/tailscale/uninstall.go internal/modules/tailscale/uninstall_test.go
git commit -m "feat(tailscale): add uninstall step"
```

### Task 26: tailscale: installer join entry point

**Files:**
- Create: `internal/modules/tailscale/bootstrap.go`
- Test: `internal/modules/tailscale/bootstrap_test.go`

**Interfaces:**
- Consumes: Contracts "Tailscale installer join" (this task writes it); "Tailscale shared plan helpers" (`ts.ensureRepo`, `aptInstall`, Task 20, and Task 20's `packageInstallTimeout`); "Tailscale internals" (`ts`, `productionTS`, `deps.local`, `localAPI`, `ID`, `joinWait`, `ts.installedVersion`, `storeTimeFormat`, the `tailscale_installs` table); "Link guard rules" (`link.IsTailscaleAddr`); "Tailscale test host" (`newTSHost`, `tsHost`, `tsRun`, `h.script`, `h.ran`, `h.reads`, `h.files`, `h.local`, `h.db`, `h.clock`, `h.journal`, `thDpkgPath`, `thDpkgStatus`); `tslocal.New`, `tslocal.DefaultSocket`, `tslocal.Status`, `tslocal.StartOptions` (`internal/platform/tslocal`); `platform.NewRunner`, `platform.ProductionRules`, `platform.NewFiles`, `platform.NewOwnership` (the production providers the installer builds).
- Produces: `type JoinDeps struct` and `func Join(ctx context.Context, d JoinDeps, authKey string) ([]netip.Addr, error)` exactly as Contracts "Tailscale installer join" (Task 30 calls `Join` from `cmd/darkpanel/system.go`). New unexported names: `joinPoll` (500 ms), `joinInstallID` (`install`), `joinSocket`, `joinTS`, `JoinDeps.check`, `ts.join` (`func (t ts) join(ctx context.Context, d JoinDeps, authKey string) ([]netip.Addr, error)`, the testable body of `Join`), `joinAddrs`, `joinSettled`, `joinSleep`, `joinPollStatus`, `joinRecordInstall`, `joinList`.

**Items:** 24

**Depends on:** Task 20

Spec 4.2 step 4: `install.sh --role agent` and `darkpanel system install --role agent`, given a `dpb2.` bundle, install tailscale (the same code as `tailscale.package.install`), log in through the local API with the bundle's key, wait up to 60 s for a tailnet address, and then enroll over the tailnet. `Join` is the module's part of that: it runs in the installer, before the agent has ever started, so it has no change, no journal and no unit client; it takes its providers from `JoinDeps`. The tests are module-private and use the test host's fakes plus a fake tailscaled (`jnLocal`) that answers `Status` as a node that is down, logged out, starting or running; the wait runs on the host's fake clock. No test runs `apt-get` or `tailscaled` on this host, writes under `/etc` or `/usr/share`, or talks to the host's tailscaled socket: `TestJnJoinOverTheSocket` serves a Unix socket of its own in a temporary directory, and `Join` itself is called only with arguments that are refused before anything runs.

The assumption of the plan header that the installer can build the production runner and a file provider over the agent database it has just migrated is checked by `TestJnProductionProvidersOverAFreshDatabase`: it builds `platform.NewRunner(platform.ProductionRules())` and `platform.NewFiles` over `platform.NewOwnership` of a migrated database in a temporary directory, writes and removes a file declared for the module, and checks the ownership row. If that test cannot pass without starting an agent, stop and report it; do not work around it.

- [ ] **Step 1: Write the failing tests**

Create `internal/modules/tailscale/bootstrap_test.go` with exactly this content. `jnRunner` stands for `apt-get` as far as the install goes: when APT-2 succeeds it writes the package into dpkg's status file, which the host fake does not, and it records the time left on each command's context. `jnDrive` advances the host's clock by one poll whenever the join waits.

```go
package tailscale

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform"
	"github.com/darkraise/darkpanel/internal/platform/tslocal"
)

const (
	jnKey     = "tskey-auth-kABCDEF1234-SECRETSECRETSECRET"
	jnVersion = "1.104.1"
	jnAptGet  = "/usr/bin/apt-get"
)

// jnLocal is the fake tailscaled of a join. Until Start it answers like the host's fake local API, except that
// its first downFor Status calls fail as a socket that is not there yet; after Start, Status answers after in order,
// the last one for ever.
type jnLocal struct {
	localAPI

	mu         sync.Mutex
	downFor    int
	started    bool
	after      []*tslocal.Status
	sinceStart int
}

func (l *jnLocal) Status(ctx context.Context) (*tslocal.Status, error) {
	l.mu.Lock()
	if l.downFor > 0 {
		l.downFor--
		l.mu.Unlock()
		return nil, fmt.Errorf("%w: connection refused", tslocal.ErrUnavailable)
	}
	if l.started && len(l.after) > 0 {
		st := *l.after[min(l.sinceStart, len(l.after)-1)]
		l.sinceStart++
		l.mu.Unlock()
		return &st, nil
	}
	l.mu.Unlock()
	return l.localAPI.Status(ctx)
}

func (l *jnLocal) Start(ctx context.Context, o tslocal.StartOptions) error {
	if err := l.localAPI.Start(ctx, o); err != nil {
		return err
	}
	l.mu.Lock()
	l.started = true
	l.mu.Unlock()
	return nil
}

func jnStatus(state string, addrs ...string) *tslocal.Status {
	st := &tslocal.Status{BackendState: state}
	for _, a := range addrs {
		st.TailscaleIPs = append(st.TailscaleIPs, netip.MustParseAddr(a))
	}
	return st
}

// jnRunner passes every vector to the host's runner, records the time the context of each had left, and leaves
// tailscale installed in dpkg's status file when the install vector (APT-2) succeeds, which the host fake does not.
type jnRunner struct {
	h         *tsHost
	deadlines *[]time.Duration
}

func (r jnRunner) Run(ctx context.Context, cmd core.Cmd) (core.CmdResult, error) {
	if dl, ok := ctx.Deadline(); ok {
		*r.deadlines = append(*r.deadlines, time.Until(dl))
	}
	res, err := r.h.Run(ctx, cmd)
	if err == nil && res.ExitCode == 0 && cmd.Path == jnAptGet && slices.Equal(cmd.Args, platform.AptInstallTailscale()) {
		r.h.mu.Lock()
		r.h.reads[thDpkgPath] = thDpkgStatus(jnVersion)
		r.h.mu.Unlock()
	}
	return res, err
}

// jnSetup is a supported host without tailscale whose tailscaled, once it exists, is logged out.
type jnSetup struct {
	h         *tsHost
	t         ts
	local     *jnLocal
	d         JoinDeps
	out       *bytes.Buffer
	deadlines []time.Duration
}

func jnNewSetup(t *testing.T) *jnSetup {
	t.Helper()
	h := newTSHost(t)
	h.reads[thDpkgPath] = thDpkgStatus("")
	h.local.Status = jnStatus("NeedsLogin")
	s := &jnSetup{h: h, out: &bytes.Buffer{}}
	tt := h.ts()
	s.local = &jnLocal{localAPI: tt.d.local()}
	tt.d.local = func() localAPI { return s.local }
	s.t = tt
	s.d = JoinDeps{Exec: jnRunner{h: h, deadlines: &s.deadlines}, Files: h, DB: h.db, Clock: h.clock, Out: s.out}
	return s
}

// installed makes the package present from the start.
func (s *jnSetup) installed() { s.h.reads[thDpkgPath] = thDpkgStatus(jnVersion) }

// jnDrive runs call while advancing the clock by one poll whenever it waits.
func jnDrive(t *testing.T, h *tsHost, call func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		call()
	}()
	for deadline := time.Now().Add(20 * time.Second); ; {
		select {
		case <-done:
			return
		default:
		}
		if h.clock.Pending() > 0 {
			h.clock.Advance(joinPoll)
		} else {
			time.Sleep(time.Millisecond)
		}
		if time.Now().After(deadline) {
			t.Fatal("the call did not finish")
		}
	}
}

func (s *jnSetup) join(t *testing.T) ([]netip.Addr, error) {
	t.Helper()
	var addrs []netip.Addr
	var err error
	jnDrive(t, s.h, func() { addrs, err = s.t.join(context.Background(), s.d, jnKey) })
	return addrs, err
}

func jnInstallRow(t *testing.T, h *tsHost) (id, version string, ok bool) {
	t.Helper()
	id, version, ok, err := liveInstall(context.Background(), h.db.Read())
	if err != nil {
		t.Fatal(err)
	}
	return id, version, ok
}

func TestJnJoinInstallsAndJoins(t *testing.T) {
	s := jnNewSetup(t)
	s.local.after = []*tslocal.Status{jnStatus("Starting"), jnStatus("Running", "fd7a:115c:a1e0::7", "100.64.0.7")}
	start := s.h.clock.Now()
	addrs, err := s.join(t)
	if err != nil {
		t.Fatal(err)
	}
	if want := []netip.Addr{netip.MustParseAddr("100.64.0.7"), netip.MustParseAddr("fd7a:115c:a1e0::7")}; !slices.Equal(addrs, want) {
		t.Errorf("addresses %v, want %v (IPv4 first)", addrs, want)
	}
	wantRan := [][]string{append([]string{jnAptGet}, platform.AptUpdateTailscale()...), append([]string{jnAptGet}, platform.AptInstallTailscale()...)}
	if !reflect.DeepEqual(s.h.ran, wantRan) {
		t.Errorf("ran %v, want APT-1 then APT-2 and nothing else", s.h.ran)
	}
	if len(s.deadlines) != 2 || s.deadlines[0] <= 14*time.Minute || s.deadlines[0] > 15*time.Minute || s.deadlines[1] <= 14*time.Minute || s.deadlines[1] > 15*time.Minute {
		t.Errorf("apt-get calls had %v left, want both bounded by 15 minutes", s.deadlines)
	}
	keyring, sources, _ := s.t.repoFiles()
	if !bytes.Equal(s.h.files[keyringPath], keyring) || !bytes.Equal(s.h.files[sourcesPath], sources) || len(s.h.files) != 2 {
		t.Errorf("files %v, want the keyring and the source list written", s.h.files)
	}
	if len(s.h.local.Starts) != 1 {
		t.Fatalf("Start called %d times, want once", len(s.h.local.Starts))
	}
	if o := s.h.local.Starts[0]; o.AuthKey != jnKey || o.UpdatePrefs == nil || !o.UpdatePrefs.WantRunning || o.UpdatePrefs.LoggedOut || !o.UpdatePrefs.CorpDNS {
		t.Errorf("Start options %+v, want the key and the current preferences with WantRunning and not LoggedOut", o)
	}
	if id, v, ok := jnInstallRow(t, s.h); !ok || id != "install" || v != jnVersion {
		t.Errorf("recorded install %q %q (found %v), want install %s", id, v, ok, jnVersion)
	}
	if waited := s.h.clock.Now().Sub(start); waited != joinPoll {
		t.Errorf("waited %v, want one poll of %v for the Starting state", waited, joinPoll)
	}
	out := s.out.String()
	if !strings.Contains(out, "installing the package") || !strings.Contains(out, "joined the tailnet as 100.64.0.7, fd7a:115c:a1e0::7") || strings.Contains(out, jnKey) {
		t.Errorf("progress output %q, want the install and the join named, and no key", out)
	}
	if len(s.h.journal.effects()) != 0 {
		t.Errorf("effects %v, want none: the installer has no change", s.h.journal.effects())
	}
}

func TestJnJoinLeavesAnInstalledPackage(t *testing.T) {
	s := jnNewSetup(t)
	s.installed()
	s.local.after = []*tslocal.Status{jnStatus("Running", "100.64.0.7")}
	if _, err := s.join(t); err != nil {
		t.Fatal(err)
	}
	if len(s.h.ran) != 0 || len(s.h.files) != 0 {
		t.Errorf("ran %v and wrote %v for a host that has the package", s.h.ran, s.h.files)
	}
	if _, _, ok := jnInstallRow(t, s.h); ok {
		t.Error("an install was recorded for a package Darkpanel did not install")
	}
	if !strings.Contains(s.out.String(), "version "+jnVersion+" is already installed") || len(s.h.local.Starts) != 1 {
		t.Errorf("output %q with %d Start calls, want the package noted and the login made", s.out.String(), len(s.h.local.Starts))
	}
}

func TestJnJoinWaitsForTailscaled(t *testing.T) {
	s := jnNewSetup(t)
	s.installed()
	s.local.downFor = 3
	s.local.after = []*tslocal.Status{jnStatus("Running", "100.64.0.7")}
	start := s.h.clock.Now()
	if _, err := s.join(t); err != nil {
		t.Fatal(err)
	}
	if waited := s.h.clock.Now().Sub(start); waited != 3*joinPoll {
		t.Errorf("waited %v, want 3 polls of %v for the socket", waited, joinPoll)
	}
}

func TestJnJoinGivesUpOnTailscaled(t *testing.T) {
	s := jnNewSetup(t)
	s.installed()
	s.local.downFor = 1 << 20
	start := s.h.clock.Now()
	_, err := s.join(t)
	if err == nil || !strings.HasPrefix(err.Error(), "tailscale: join: tailscaled:") || !strings.Contains(err.Error(), "60s") || !errors.Is(err, tslocal.ErrUnavailable) {
		t.Fatalf("error %v, want a failure of step tailscaled after 60s that keeps the socket error", err)
	}
	if waited := s.h.clock.Now().Sub(start); waited != joinWait || len(s.h.local.Starts) != 0 {
		t.Errorf("waited %v with %d Start calls, want %v and none", waited, len(s.h.local.Starts), joinWait)
	}
}

func TestJnJoinKeepsAJoinedNode(t *testing.T) {
	s := jnNewSetup(t)
	s.installed()
	s.h.local.Status = jnStatus("Running", "100.64.0.9")
	addrs, err := s.join(t)
	if err != nil || !slices.Equal(addrs, []netip.Addr{netip.MustParseAddr("100.64.0.9")}) {
		t.Fatalf("addresses %v, error %v, want the node's address", addrs, err)
	}
	if len(s.h.local.Starts) != 0 || !strings.Contains(s.out.String(), "already joined as 100.64.0.9") {
		t.Errorf("%d Start calls and output %q, want no login for a node that is on the tailnet", len(s.h.local.Starts), s.out.String())
	}
}

func TestJnJoinGivesUpWithoutAnAddress(t *testing.T) {
	s := jnNewSetup(t)
	s.installed()
	s.local.after = []*tslocal.Status{jnStatus("NeedsLogin")}
	start := s.h.clock.Now()
	_, err := s.join(t)
	if err == nil || !strings.HasPrefix(err.Error(), "tailscale: join: address:") || !strings.Contains(err.Error(), "60s") || !strings.Contains(err.Error(), "NeedsLogin") {
		t.Fatalf("error %v, want a failure of step address after 60s naming the backend state", err)
	}
	if waited := s.h.clock.Now().Sub(start); waited != joinWait {
		t.Errorf("waited %v, want %v from the start of the join", waited, joinWait)
	}
}

func TestJnJoinNeverShowsTheKey(t *testing.T) {
	s := jnNewSetup(t)
	s.installed()
	s.h.local.Errs = map[string]error{"Start": errors.New("tailscaled refused the key " + jnKey + ": invalid")}
	_, err := s.join(t)
	if err == nil || !strings.HasPrefix(err.Error(), "tailscale: join: login:") || strings.Contains(err.Error(), jnKey) || !strings.Contains(err.Error(), "[redacted]") {
		t.Errorf("error %v, want a failure of step login with the key redacted", err)
	}
	if strings.Contains(s.out.String(), jnKey) {
		t.Errorf("progress output %q shows the key", s.out.String())
	}
}

func TestJnJoinNamesTheFailedStep(t *testing.T) {
	apt1 := strings.Join(append([]string{jnAptGet}, platform.AptUpdateTailscale()...), " ")
	tests := []struct {
		name   string
		setup  func(s *jnSetup)
		want   string
		wantIn string // text the error must also hold
		ranApt int
	}{
		{"the repository differs", func(s *jnSetup) { s.h.reads[sourcesPath] = []byte("deb https://example.com/apt stable main\n") }, "join: repository:", "", 0},
		{"the repository refresh fails", func(s *jnSetup) {
			s.h.script(apt1, tsRun{res: core.CmdResult{ExitCode: 100, Stderr: []byte("E: Failed to fetch https://pkgs.tailscale.com\n")}})
		}, "join: install:", "Failed to fetch", 1},
		{"apt-get finishes without the package", func(s *jnSetup) { s.d.Exec = s.h }, "join: install:", "not installed", 2},
		{"dpkg's status cannot be read", func(s *jnSetup) { s.t.d.readFile = func(string) ([]byte, error) { return nil, os.ErrPermission } }, "join: inspect:", "permission", 0},
		{"the preferences cannot be read", func(s *jnSetup) {
			s.installed()
			s.h.local.Errs = map[string]error{"Prefs": errors.New("prefs unavailable")}
		}, "join: login:", "prefs unavailable", 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := jnNewSetup(t)
			tc.setup(s)
			_, err := s.join(t)
			if err == nil || !strings.HasPrefix(err.Error(), "tailscale: "+tc.want) || !strings.Contains(err.Error(), tc.wantIn) {
				t.Fatalf("error %v, want a failure starting %q holding %q", err, "tailscale: "+tc.want, tc.wantIn)
			}
			apt := 0
			for _, v := range s.h.ran {
				if v[0] == jnAptGet {
					apt++
				}
			}
			if apt != tc.ranApt || len(s.h.local.Starts) != 0 {
				t.Errorf("apt-get ran %d times and Start %d times, want %d and none", apt, len(s.h.local.Starts), tc.ranApt)
			}
			if _, _, ok := jnInstallRow(t, s.h); ok {
				t.Error("an install was recorded for a join that failed before the package was installed")
			}
		})
	}
}

func TestJnJoinReportsARepositoryConflict(t *testing.T) {
	s := jnNewSetup(t)
	s.h.reads[sourcesPath] = []byte("deb https://example.com/apt stable main\n")
	_, err := s.join(t)
	var coded *errcode.Error
	if !errors.As(err, &coded) || coded.Code != errcode.TailscaleRepoConflict || len(s.h.files) != 0 {
		t.Errorf("error %v with files %v, want %s and nothing written", err, s.h.files, errcode.TailscaleRepoConflict)
	}
}

func TestJnJoinRecordsAReinstall(t *testing.T) {
	s := jnNewSetup(t)
	ctx := context.Background()
	if err := s.h.db.Write(ctx, func(tx *sql.Tx) error {
		return insertInstall(ctx, tx, "install", "1.90.1", s.h.clock.Now().Add(-time.Hour))
	}); err != nil {
		t.Fatal(err)
	}
	if err := setInstallRemoved(ctx, s.h.db, "install", s.h.clock.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	s.local.after = []*tslocal.Status{jnStatus("Running", "100.64.0.7")}
	if _, err := s.join(t); err != nil {
		t.Fatal(err)
	}
	if id, v, ok := jnInstallRow(t, s.h); !ok || id != "install" || v != jnVersion {
		t.Errorf("recorded install %q %q (found %v), want the earlier row live again at %s", id, v, ok, jnVersion)
	}
}

func TestJnJoinChecksItsArguments(t *testing.T) {
	h := newTSHost(t)
	full := JoinDeps{Exec: h, Files: h, DB: h.db, Clock: h.clock, Out: io.Discard}
	for _, tc := range []struct {
		name string
		deps func(d *JoinDeps)
		key  string
		want string
	}{
		{"no runner", func(d *JoinDeps) { d.Exec = nil }, jnKey, "JoinDeps.Exec"},
		{"no file provider", func(d *JoinDeps) { d.Files = nil }, jnKey, "JoinDeps.Files"},
		{"no database", func(d *JoinDeps) { d.DB = nil }, jnKey, "JoinDeps.DB"},
		{"no clock", func(d *JoinDeps) { d.Clock = nil }, jnKey, "JoinDeps.Clock"},
		{"no output", func(d *JoinDeps) { d.Out = nil }, jnKey, "JoinDeps.Out"},
		{"no key", func(*JoinDeps) {}, "", "auth key"},
		{"a key with whitespace", func(*JoinDeps) {}, "tskey-auth-a b", "auth key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := full
			tc.deps(&d)
			addrs, err := Join(context.Background(), d, tc.key)
			if err == nil || !strings.HasPrefix(err.Error(), "tailscale: join:") || !strings.Contains(err.Error(), tc.want) || addrs != nil {
				t.Errorf("Join = %v, %v; want a refusal naming %q", addrs, err, tc.want)
			}
			if tc.key != "" && strings.Contains(err.Error(), tc.key) {
				t.Errorf("error %q shows the key", err)
			}
			if len(h.ran) != 0 {
				t.Errorf("ran %v before the arguments were checked", h.ran)
			}
		})
	}
}

func TestJnJoinOverTheSocket(t *testing.T) {
	if got := joinSocket(""); got != tslocal.DefaultSocket {
		t.Errorf("default socket %q, want %q", got, tslocal.DefaultSocket)
	}
	dir, err := os.MkdirTemp("", "jn")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "ts.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/localapi/v0/status" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, `{"BackendState":"NeedsLogin"}`)
	}))
	_ = srv.Listener.Close()
	srv.Listener = l
	srv.Start()
	t.Cleanup(srv.Close)
	tt, closeClient := joinTS(JoinDeps{Socket: sock})
	defer closeClient()
	st, err := tt.d.local().Status(context.Background())
	if err != nil || st.BackendState != "NeedsLogin" {
		t.Errorf("Status over the socket = %+v, %v, want the fake tailscaled's answer", st, err)
	}
	if tt.d.readFile == nil || tt.d.osRelease == nil || tt.d.arch == "" {
		t.Error("the join's ts lacks the production readFile, osRelease or arch")
	}
}

// TestJnProductionProvidersOverAFreshDatabase builds the join's dependencies as the installer can before the
// agent has ever run: the production runner, and a file provider over an agent database that was just migrated.
func TestJnProductionProvidersOverAFreshDatabase(t *testing.T) {
	h := newTSHost(t)
	dir := t.TempDir()
	target := filepath.Join(dir, "tailscale.list")
	files := platform.NewFiles(platform.FilesConfig{
		Owned:     map[string][]core.OwnedPath{ID: {{Path: target, Kind: core.OwnedFile}}},
		Ownership: platform.NewOwnership(h.db),
		Clock:     h.clock,
		TestMode:  true,
	})
	d := JoinDeps{Exec: platform.NewRunner(platform.ProductionRules()), Files: files, DB: h.db, Clock: core.SystemClock{}, Out: io.Discard}
	if err := d.check(jnKey); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := d.Files.WriteFile(ctx, ID, target, []byte("deb x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if owned, err := d.Files.Owns(ctx, ID, target); err != nil || !owned {
		t.Errorf("Owns = %v, %v, want the written file recorded as the module's", owned, err)
	}
	if err := d.Files.Remove(ctx, ID, target); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec.Run(ctx, core.Cmd{Path: "/bin/sh", Args: []string{"-c", "true"}}); err == nil {
		t.Error("the production runner ran a program outside the allowlist")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
mkdir -p /root/.cache/w4-tmp/26 && export TMPDIR=/root/.cache/w4-tmp/26 GOTOOLCHAIN=local
timeout 300 go test -count=1 -run 'TestJn' ./internal/modules/tailscale/ 2>&1 | head -20
```

Expected: FAIL to build, with `undefined: JoinDeps`, `undefined: joinSocket`, `undefined: joinPoll` and `s.t.join undefined (type ts has no field or method join)`. Quote the first lines in the report.

- [ ] **Step 3: Implement `internal/modules/tailscale/bootstrap.go`**

Decisions this task embodies. The join does the same installation as `tailscale.package.install` (`ensureRepo`, then APT-1 and APT-2) but records it under the fixed change id `install`, because the installer has no change. The auth key is a one-hour, one-use secret: no progress line, error or log carries it. A host that is already on the tailnet is left alone, so the retry the spec names ("rerunning with a fresh bundle") is safe after a later install step failed. The 60 second wait of the spec is one budget that starts once the package is in place and covers tailscaled's socket, the login and the address.

1. `JoinDeps` has the six fields of Contracts "Tailscale installer join" with their comments. `func (d JoinDeps) check(authKey string) error` refuses, in this order, a nil `Exec`, `Files`, `DB`, `Clock` or `Out` with `tailscale: join: JoinDeps.<Field> is required`, and an `authKey` that is empty or contains whitespace (space, tab, carriage return or line feed) with `tailscale: join: the auth key is empty or contains whitespace`; the error never contains the key.
2. `joinSocket(s)` is `tslocal.DefaultSocket` for `""` and `s` otherwise. `joinTS(d JoinDeps) (ts, func())` returns `productionTS()` with its `local` replaced by a function returning one `tslocal.New(joinSocket(d.Socket))` client (every other dep stays production), and the client's `Close`.
3. `Join`: `d.check(authKey)`, then `joinTS`, `defer` the close, and `t.join(ctx, d, authKey)`.
4. `func (t ts) join(ctx, d, authKey)` works on a `core.Env` built from `d`: `Exec`, `Files`, `DB`, `Clock`, and a `Log` that discards (the env has no `Effects`, so `ensureRepo` journals nothing). Progress lines go to `d.Out` as `tailscale: <message>` plus a newline. Every failure is returned as `tailscale: join: <step>: <cause>` with the cause wrapped (`%w`), step being one of `inspect`, `repository`, `install`, `tailscaled`, `login`, `address`. In order:
   1. `inspect`: `t.installedVersion`; an error fails the step. When a version is installed, print `tailscale: version <v> is already installed` and go to step 3; otherwise print `tailscale: installing the package`.
   2. Install, when absent: `t.ensureRepo` (step `repository`; a repository conflict is returned with its error code intact, and nothing is written); then, under a context bounded by `packageInstallTimeout`, `aptInstall` (step `install`); then `installedVersion` again: empty fails step `install` with "apt-get finished but tailscale is not installed"; then `joinRecordInstall` (step `install`), which writes the row `tailscale_installs(change_id = 'install', version, installed_at = d.Clock.Now() as UTC in storeTimeFormat)` with one `INSERT ... ON CONFLICT (change_id) DO UPDATE` statement that also clears `removed_at`, so a reinstall after an uninstall that kept the database reuses the row instead of failing on the key; then print `tailscale: installed version <v>`.
   3. Fix `deadline = d.Clock.Now() + joinWait`. Poll `Status` of `t.d.local()` (`joinPollStatus`) until it answers: each call under a 5 s real-time bound; after a failure or an unfinished state, return when the context has ended (the context's error) or `d.Clock.Now()` is not before the deadline, else wait `joinPoll` on `d.Clock.AfterFunc` and a channel (`joinSleep`) and ask again. The first answer is taken at once. When it never answers, fail step `tailscaled` with `did not answer on its socket within 60s: <last Status error>` (the error chain keeps `tslocal.ErrUnavailable`; the seconds are `int(joinWait.Seconds())` followed by `s`); a cancelled context fails the same step with the context's error.
   4. If that status is settled (`joinSettled`: backend state `Running` and at least one address that `link.IsTailscaleAddr` accepts), print `tailscale: already joined as <addresses>` and return them without a login.
   5. `login`: read `Prefs`; copy the value; set `WantRunning` true and `LoggedOut` false; call `Start` with `tslocal.StartOptions{AuthKey: authKey, UpdatePrefs: &copy}`. A `Prefs` error is wrapped. A `Start` error is returned as a new, unwrapped error whose text is the original with every occurrence of the key replaced by `[redacted]`. Print `tailscale: logged in; waiting for a Tailscale address`.
   6. `address`: poll again to the same deadline until settled. Timing out fails with `no Tailscale address within 60s (backend state <state>)`, the state of the last status or `unknown` when there was none.
   7. Print `tailscale: joined the tailnet as <addresses joined by ", ">` and return the addresses: `joinAddrs` keeps those of `Status.TailscaleIPs` that `link.IsTailscaleAddr` accepts, IPv4 first and otherwise in the order given (a stable sort).
5. Add no `tailscale.go` change (the manifest still declares nothing until Task 31) and no file outside this task's two.

- [ ] **Step 4: Run the tests and the task checks**

```bash
mkdir -p /root/.cache/w4-tmp/26 && export TMPDIR=/root/.cache/w4-tmp/26 GOTOOLCHAIN=local
timeout 300 go test -count=1 -run 'TestJn' -v ./internal/modules/tailscale/
timeout 600 go test -race -count=1 ./internal/modules/tailscale/...
timeout 600 go build ./... && timeout 300 go vet ./...
timeout 1500 go test -count=1 ./...
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
rm -rf /root/.cache/w4-tmp/26 && go clean -testcache
```

Expected: every `TestJn` test `--- PASS`, every package line of `go test ./...` `ok` or `?`, `go vet` and `go mod tidy -diff` print nothing, `golangci-lint` prints `0 issues.`. The Unix socket of `TestJnJoinOverTheSocket` sits in a short directory under `TMPDIR`, which is why `TMPDIR` is `/root/.cache/w4-tmp/26`. Record the real output. No schema snapshot changes.

- [ ] **Step 5: Commit**

```bash
git add internal/modules/tailscale/bootstrap.go internal/modules/tailscale/bootstrap_test.go
git commit -m "feat(tailscale): add installer join entry point"
```

### Task 27: tailscale: prefs.repair

**Files:**
- Create: `internal/modules/tailscale/prefs_repair.go`
- Test: `internal/modules/tailscale/prefs_repair_test.go`

**Interfaces:**
- Consumes: Contracts "Tailscale prefs repair" (`prefsRepairTarget` and its refusals, which this task writes); "Tailscale preferences" (`prefsFieldsOf` from Task 14; `prefsEffectResource`, `prefsCommitWait`, `prefsSetPick`, `prefsSetNormalize`, `prefsSetPatch`, `prefsSetLinkAfter` and `prefsSetHandler` with its `Revert` and `Observe` from Task 21); "Tailscale wire shapes" (`PrefsFields`, `PrefsSetInput`, `PrefsRepairInput`, `PrefsRepaired`, and the action-spec row of `tailscale.prefs.repair`); "Tailscale internals" (`ts`, `deps.local`, `localAPI`, `lockKey`, `ts.available`); "Tailscale shared plan helpers" (`ts.linkGuard`, `ts.currentLinkPrefs`); "Link guard rules" (`link.GuardID`, `link.Change`); "Tailscale test host" (`newTSHost`, `tsHost`, `tsLocal`, `tsJournal`, `thLocal`); `core.DefineRevertible`, `core.Reverter`, `core.Env.Call.RepairOf`, `core.Env.Sealer` (`internal/core/contract.go`, `define.go`); the table `core_changes` (columns `action`, `input_json`, `snapshot_ct`) and the associated data `"core_changes.snapshot_ct."+<change id>` that `internal/core/lifecycle/apply.go` `snapshotAD` seals the snapshot with; `tslocal.PrefsPatch`, `tslocal.APIError`, `tslocal.ErrUnavailable`.
- Produces: `func (t ts) prefsRepair() core.ActionDef`, the constructor of `tailscale.prefs.repair/v1`, and `prefsRepairTarget` as Contracts "Tailscale prefs repair" gives it. Task 31 adds the constructor to `Manifest().Actions`; this task does not edit `tailscale.go`. Every other package-level identifier of `prefs_repair.go` starts with `prefsRepair` (`prefsRepairHandler`, `prefsRepairResolve`, `prefsRepairLines`, `prefsRepairMismatch`, `prefsRepairRefusal`) and is not a Contract.

**Items:** 21

**Depends on:** Task 21

`tailscale.prefs.repair` is the repair action of a `tailscale.prefs.set` whose revert failed (spec 4.1) and of itself. The tests are module-private and use only the fakes of Contracts "Tailscale test host"; nothing here touches tailscaled, apt or systemd on this host. The repaired change is stored as the engine would leave it, with a row pair in `core_plans` and `core_changes` whose snapshot is sealed with `coretest.Sealer{}` (the sealer `h.env` hands to the module). The helpers `prSetup`, `prSeed`, `prSeedAD`, `prSeedSet`, `prEnv`, `prEffects`, `prSame` and the type `prStale` are declared by this task's test file only.

- [ ] **Step 1: Write the failing test**

Create `internal/modules/tailscale/prefs_repair_test.go` with exactly this content:

```go
package tailscale

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/tailscale/link"
	"github.com/darkraise/darkpanel/internal/platform/tslocal"
)

const (
	prSnapshotAD   = "core_changes.snapshot_ct."
	prSetAction    = "tailscale.prefs.set"
	prRepairAction = "tailscale.prefs.repair"
	// prSetInput and prSetSnapshot are the change prSeedSet stores: it set two fields, and its snapshot holds their
	// previous values.
	prSetInput    = `{"acceptRoutes":true,"shieldsUp":true}`
	prSetSnapshot = `{"acceptRoutes":false,"shieldsUp":false}`
)

func prBool(b bool) *bool       { return &b }
func prString(s string) *string { return &s }

func prPrefixes(s ...string) []netip.Prefix {
	out := []netip.Prefix{}
	for _, p := range s {
		out = append(out, netip.MustParsePrefix(p))
	}
	return out
}

// prSetup is a running node with the given preferences behind a ts whose fakes are the host's.
func prSetup(t *testing.T, prefs tslocal.Prefs) (*tsHost, ts) {
	t.Helper()
	h := newTSHost(t)
	h.local.Prefs = &prefs
	h.local.Errs = map[string]error{}
	return h, h.ts()
}

// prSeedAD stores change id the way the engine leaves a change: its redacted input and its snapshot, sealed with ad.
func prSeedAD(t *testing.T, h *tsHost, id, action, input, snapshot, ad string) {
	t.Helper()
	ct, err := coretest.Sealer{}.Seal(ad, []byte(snapshot))
	if err != nil {
		t.Fatal(err)
	}
	at := h.clock.Now().UTC().Format(time.RFC3339)
	ctx := context.Background()
	err = h.db.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO core_plans (id, host_id, action, version, actor, input_hash, state_hash,
			diff_json, guards_json, confirmation, created_at, expires_at)
			VALUES (?, 'h1', ?, 1, 'user:u1', '', '', '[]', '[]', '', ?, ?)`, "p-"+id, action, at, at); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO core_changes (id, plan_id, host_id, action, version, class, compensated_by, actor,
			state, input_json, snapshot_ct, created_at, updated_at)
			VALUES (?, ?, 'h1', ?, 1, 'revertible', '', 'user:u1', 'needs_repair', ?, ?, ?, ?)`,
			id, "p-"+id, action, input, ct, at, at)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func prSeed(t *testing.T, h *tsHost, id, action, input, snapshot string) {
	t.Helper()
	prSeedAD(t, h, id, action, input, snapshot, prSnapshotAD+id)
}

// prSeedSet stores change c1, a tailscale.prefs.set that set acceptRoutes and shieldsUp.
func prSeedSet(t *testing.T, h *tsHost) {
	t.Helper()
	prSeed(t, h, "c1", prSetAction, prSetInput, prSetSnapshot)
}

// prEnv is the environment of the call of change changeID (empty: a plan) that repairs change repairs (empty: a
// call that repairs nothing).
func prEnv(h *tsHost, changeID, repairs string) core.Env {
	env := h.env(changeID)
	env.Call.RepairOf = repairs
	return env
}

func prEffects(h *tsHost) []core.Effect { return h.journal.effects() }

// prSame compares two JSON documents as values.
func prSame(t *testing.T, a, b []byte) bool {
	t.Helper()
	var va, vb any
	if err := json.Unmarshal(a, &va); err != nil {
		t.Fatalf("document %s: %v", a, err)
	}
	if err := json.Unmarshal(b, &vb); err != nil {
		t.Fatalf("document %s: %v", b, err)
	}
	return reflect.DeepEqual(va, vb)
}

// prStale is a local API whose EditPrefs answers preferences that differ from the ones it stored.
type prStale struct{ localAPI }

func (s prStale) EditPrefs(ctx context.Context, p tslocal.PrefsPatch) (*tslocal.Prefs, error) {
	got, err := s.localAPI.EditPrefs(ctx, p)
	if err != nil {
		return nil, err
	}
	got.ShieldsUp = !got.ShieldsUp
	return got, nil
}

func TestPrActionDefinition(t *testing.T) {
	_, tt := prSetup(t, tslocal.Prefs{})
	def := tt.prefsRepair()
	s, h := def.Spec, def.Handler
	if s.Name != prRepairAction || s.Version != 1 || s.Class != core.ClassRevertible || s.Side != core.SideAgent ||
		!slices.Equal(s.LockKeys, []string{lockKey}) || s.RepairedBy != prRepairAction || s.Available == nil || s.Task || s.LongRunning {
		t.Errorf("spec %+v, want a synchronous revertible agent action locking %q, repaired by itself, with an availability check", s, lockKey)
	}
	if want := []core.GuardSpec{{ID: link.GuardID, Overridable: true}}; !reflect.DeepEqual(s.Guards, want) {
		t.Errorf("guards %+v, want %+v", s.Guards, want)
	}
	if def.InputType != reflect.TypeFor[PrefsRepairInput]() || def.OutputType != reflect.TypeFor[PrefsRepaired]() {
		t.Errorf("input %v and output %v, want PrefsRepairInput and PrefsRepaired", def.InputType, def.OutputType)
	}
	if h.Plan == nil || h.Apply == nil || h.Verify == nil || h.Snapshot == nil || h.Revert == nil || h.Observe == nil || h.Inverse != nil {
		t.Error("handler lacks Plan, Apply, Verify, Snapshot, Revert or Observe, or has an Inverse")
	}
}

func TestPrTarget(t *testing.T) {
	ctx := context.Background()
	h, _ := prSetup(t, tslocal.Prefs{})
	prSeed(t, h, "c1", prSetAction,
		`{"overrides":["tailscale.link"],"advertiseRoutes":["192.168.5.0/24","10.2.0.0/16"],"shieldsUp":true}`,
		`{"advertiseRoutes":[],"shieldsUp":false}`)
	prSeed(t, h, "c2", prRepairAction, `{"outcome":"restore"}`, `{"shieldsUp":true}`)
	prSeedAD(t, h, "c3", prSetAction, `{"shieldsUp":true}`, `{"shieldsUp":false}`, prSnapshotAD+"c8")

	action, prior, set, err := prefsRepairTarget(ctx, prEnv(h, "c9", "c1"))
	if err != nil || action != prSetAction {
		t.Fatalf("target of c1 = %q, %v", action, err)
	}
	if got, _ := json.Marshal(prior); !prSame(t, got, []byte(`{"advertiseRoutes":[],"shieldsUp":false}`)) {
		t.Errorf("prior %s, want the snapshot's fields", got)
	}
	if got, _ := json.Marshal(set); !prSame(t, got, []byte(`{"advertiseRoutes":["10.2.0.0/16","192.168.5.0/24"],"shieldsUp":true}`)) {
		t.Errorf("set %s, want the input's fields, normalised and without the guard members", got)
	}

	action, prior, set, err = prefsRepairTarget(ctx, prEnv(h, "c9", "c2"))
	if err != nil || action != prRepairAction {
		t.Fatalf("target of c2 = %q, %v", action, err)
	}
	if got, _ := json.Marshal(prior); !prSame(t, got, []byte(`{"shieldsUp":true}`)) || !reflect.DeepEqual(set, PrefsFields{}) {
		t.Errorf("prior %s and set %+v of a repair, want the snapshot and no fields", got, set)
	}

	if _, _, _, err = prefsRepairTarget(ctx, prEnv(h, "c9", "c3")); err == nil {
		t.Error("a snapshot sealed for another change was opened, want an error")
	}
	if _, _, _, err = prefsRepairTarget(ctx, prEnv(h, "c9", "c7")); errcode.From(err).Code != errcode.NotFound {
		t.Errorf("target of a change that does not exist: %v, want %s", err, errcode.NotFound)
	}
}

func TestPrPlan(t *testing.T) {
	line := func(op, text string) core.DiffLine { return core.DiffLine{Op: op, Text: text} }
	set := [2]string{prSetInput, prSetSnapshot}
	routes := [2]string{`{"advertiseRoutes":["192.168.5.0/24","10.2.0.0/16"]}`, `{"advertiseRoutes":[]}`}
	restore := []core.DiffLine{line("-", "acceptRoutes: true"), line("+", "acceptRoutes: false"), line("-", "shieldsUp: true"), line("+", "shieldsUp: false")}
	tests := []struct {
		name   string
		action string
		change [2]string // the repaired change's input and snapshot
		prefs  tslocal.Prefs
		in     string
		want   []core.DiffLine
	}{
		{"restore is the default", prSetAction, set, tslocal.Prefs{WantRunning: true, RouteAll: true, ShieldsUp: true}, `{}`, restore},
		{"restore named", prSetAction, set, tslocal.Prefs{WantRunning: true, RouteAll: true, ShieldsUp: true}, `{"outcome":"restore"}`, restore},
		{"restore with a field already back", prSetAction, set, tslocal.Prefs{ShieldsUp: true}, `{"outcome":"restore"}`,
			[]core.DiffLine{line(" ", "acceptRoutes: false (unchanged)"), line("-", "shieldsUp: true"), line("+", "shieldsUp: false")}},
		{"keep", prSetAction, set, tslocal.Prefs{RouteAll: true}, `{"outcome":"keep"}`,
			[]core.DiffLine{line(" ", "acceptRoutes: true (unchanged)"), line("-", "shieldsUp: false"), line("+", "shieldsUp: true")}},
		{"keep shows the routes sorted", prSetAction, routes, tslocal.Prefs{}, `{"outcome":"keep"}`,
			[]core.DiffLine{line("-", "advertiseRoutes: none"), line("+", "advertiseRoutes: 10.2.0.0/16, 192.168.5.0/24")}},
		{"restore the routes", prSetAction, routes, tslocal.Prefs{AdvertiseRoutes: prPrefixes("10.2.0.0/16", "192.168.5.0/24")}, `{}`,
			[]core.DiffLine{line("-", "advertiseRoutes: 10.2.0.0/16, 192.168.5.0/24"), line("+", "advertiseRoutes: none")}},
		{"restore an exit node", prSetAction, [2]string{`{"exitNode":"100.64.0.9"}`, `{"exitNode":""}`},
			tslocal.Prefs{ExitNodeIP: netip.MustParseAddr("100.64.0.9")}, `{}`,
			[]core.DiffLine{line("-", "exitNode: 100.64.0.9"), line("+", "exitNode: none")}},
		{"fields in the order of the wire shape", prSetAction,
			[2]string{`{"runSSH":true,"acceptRoutes":true,"wantRunning":false}`, `{"runSSH":false,"acceptRoutes":false,"wantRunning":true}`},
			tslocal.Prefs{RunSSH: true, RouteAll: true}, `{}`,
			[]core.DiffLine{
				line("-", "wantRunning: false"), line("+", "wantRunning: true"),
				line("-", "acceptRoutes: true"), line("+", "acceptRoutes: false"),
				line("-", "runSSH: true"), line("+", "runSSH: false"),
			}},
		{"restore a repair", prRepairAction, [2]string{`{}`, `{"shieldsUp":true}`}, tslocal.Prefs{}, `{}`,
			[]core.DiffLine{line("-", "shieldsUp: false"), line("+", "shieldsUp: true")}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, tt := prSetup(t, tc.prefs)
			prSeed(t, h, "c1", tc.action, tc.change[0], tc.change[1])
			out, err := tt.prefsRepair().Handler.Plan(context.Background(), prEnv(h, "", "c1"), json.RawMessage(tc.in))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(out.Diff, tc.want) {
				t.Errorf("diff %+v, want %+v", out.Diff, tc.want)
			}
			if len(out.Guards) != 1 || out.Guards[0].ID != link.GuardID {
				t.Errorf("guards %+v, want only %s", out.Guards, link.GuardID)
			}
			if len(out.StateHash) != 64 || out.Confirmation != "" {
				t.Errorf("state hash %q and confirmation %q, want a 64-digit hash and none", out.StateHash, out.Confirmation)
			}
			if len(h.local.Edits) != 0 || len(h.ran) != 0 {
				t.Errorf("plan edited the preferences %v or ran %v", h.local.Edits, h.ran)
			}
		})
	}
}

func TestPrPlanGuard(t *testing.T) {
	plan := func(in string) core.Guard {
		t.Helper()
		h, tt := prSetup(t, tslocal.Prefs{WantRunning: true, RunSSH: true})
		prSeed(t, h, "c1", prSetAction, `{"wantRunning":false}`, `{"wantRunning":true}`)
		out, err := tt.prefsRepair().Handler.Plan(context.Background(), prEnv(h, "", "c1"), json.RawMessage(in))
		if err != nil || len(out.Guards) != 1 {
			t.Fatalf("plan %s: guards %+v, err %v", in, out.Guards, err)
		}
		return out.Guards[0]
	}
	if g := plan(`{"outcome":"keep"}`); g.ID != link.GuardID || g.OK || g.Message == "" {
		t.Errorf("keeping the node stopped while Tailscale SSH is on: guard %+v, want a failure with a message", g)
	}
	if g := plan(`{"outcome":"restore"}`); !g.OK {
		t.Errorf("restoring the node to running: guard %+v, want it to pass", g)
	}
	if g := plan(`{"outcome":"keep","overrides":["tailscale.link"]}`); !g.OK || !g.Overridden {
		t.Errorf("overridden guard %+v, want OK and Overridden", g)
	}
}

func TestPrPlanStateHash(t *testing.T) {
	hash := func(prefs tslocal.Prefs) string {
		t.Helper()
		h, tt := prSetup(t, prefs)
		prSeedSet(t, h)
		out, err := tt.prefsRepair().Handler.Plan(context.Background(), prEnv(h, "", "c1"), json.RawMessage(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		return out.StateHash
	}
	base := hash(tslocal.Prefs{RouteAll: true, ShieldsUp: true})
	if other := hash(tslocal.Prefs{RouteAll: true}); other == base {
		t.Error("another current value of a field the repair sets has the same state hash")
	}
	if other := hash(tslocal.Prefs{RouteAll: true, ShieldsUp: true, RunSSH: true}); other != base {
		t.Error("a preference the repair does not set changed the state hash")
	}
}

func TestPrRefusals(t *testing.T) {
	seedSet := func(t *testing.T, h *tsHost) { prSeedSet(t, h) }
	tests := []struct {
		name    string
		repairs string
		seed    func(t *testing.T, h *tsHost)
		in      string
		code    errcode.Code
		path    string // the one field error's path; "" when there is none
		detail  string // a part of the detail
	}{
		{"no repaired change", "", func(*testing.T, *tsHost) {}, `{}`, errcode.InvalidInput, "", "runs only as the repair of a change"},
		{"an unknown outcome", "c1", seedSet, `{"outcome":"merge"}`, errcode.InvalidInput, "/outcome", "merge"},
		{"keep of a repair", "c1", func(t *testing.T, h *tsHost) {
			prSeed(t, h, "c1", prRepairAction, `{}`, `{"shieldsUp":true}`)
		}, `{"outcome":"keep"}`, errcode.InvalidInput, "/outcome", "change c1 is a repair; repair it with outcome restore"},
		{"a change of another action", "c1", func(t *testing.T, h *tsHost) {
			prSeed(t, h, "c1", "tailscale.node.login", `{}`, `{}`)
		}, `{}`, errcode.InvalidInput, "", "tailscale.node.login"},
		{"a change that does not exist", "c7", func(*testing.T, *tsHost) {}, `{}`, errcode.NotFound, "", "c7"},
	}
	for _, tc := range tests {
		for _, phase := range []string{"plan", "snapshot", "apply"} {
			t.Run(tc.name+" at "+phase, func(t *testing.T) {
				h, tt := prSetup(t, tslocal.Prefs{RouteAll: true, ShieldsUp: true})
				tc.seed(t, h)
				def, ctx, raw := tt.prefsRepair(), context.Background(), json.RawMessage(tc.in)
				var err error
				switch phase {
				case "plan":
					_, err = def.Handler.Plan(ctx, prEnv(h, "", tc.repairs), raw)
				case "snapshot":
					_, err = def.Handler.Snapshot(ctx, prEnv(h, "c9", tc.repairs), raw)
				default:
					_, err = def.Handler.Apply(ctx, prEnv(h, "c9", tc.repairs), raw)
				}
				e := errcode.From(err)
				if e.Code != tc.code || !core.IsDefinite(err) {
					t.Fatalf("err = %v (definite %v), want %s marked definite", err, core.IsDefinite(err), tc.code)
				}
				if !strings.Contains(e.Detail, tc.detail) {
					t.Errorf("detail %q, want it to contain %q", e.Detail, tc.detail)
				}
				if tc.path == "" && len(e.Fields) != 0 || tc.path != "" && (len(e.Fields) != 1 || e.Fields[0].Path != tc.path) {
					t.Errorf("fields %+v, want one at %q", e.Fields, tc.path)
				}
				if len(prEffects(h)) != 0 || len(h.local.Edits) != 0 {
					t.Errorf("effects %v and edits %v, want none", prEffects(h), h.local.Edits)
				}
			})
		}
	}
}

func TestPrApply(t *testing.T) {
	addr := netip.MustParseAddr("100.64.0.9")
	set := [2]string{prSetInput, prSetSnapshot}
	tests := []struct {
		name          string
		action        string
		change        [2]string
		prefs         tslocal.Prefs
		in            string
		patch         tslocal.PrefsPatch
		out           string
		before, after string
	}{
		{"restore", prSetAction, set, tslocal.Prefs{WantRunning: true, RunSSH: true, RouteAll: true, ShieldsUp: true}, `{}`,
			tslocal.PrefsPatch{RouteAll: prBool(false), ShieldsUp: prBool(false)},
			`{"repairOutcome":"restored","prefs":{"acceptRoutes":false,"shieldsUp":false}}`,
			`{"acceptRoutes":true,"shieldsUp":true}`, `{"acceptRoutes":false,"shieldsUp":false}`},
		{"keep", prSetAction, set, tslocal.Prefs{WantRunning: true, RunSSH: true}, `{"outcome":"keep"}`,
			tslocal.PrefsPatch{RouteAll: prBool(true), ShieldsUp: prBool(true)},
			`{"repairOutcome":"kept","prefs":{"acceptRoutes":true,"shieldsUp":true}}`,
			`{"acceptRoutes":false,"shieldsUp":false}`, `{"acceptRoutes":true,"shieldsUp":true}`},
		{"keep an exit node", prSetAction, [2]string{`{"exitNode":"100.64.0.9"}`, `{"exitNode":""}`}, tslocal.Prefs{}, `{"outcome":"keep"}`,
			tslocal.PrefsPatch{ExitNodeID: prString(""), ExitNodeIP: &addr},
			`{"repairOutcome":"kept","prefs":{"exitNode":"100.64.0.9"}}`,
			`{"exitNode":""}`, `{"exitNode":"100.64.0.9"}`},
		{"restore a repair", prRepairAction, [2]string{`{}`, `{"shieldsUp":true}`}, tslocal.Prefs{}, `{}`,
			tslocal.PrefsPatch{ShieldsUp: prBool(true)},
			`{"repairOutcome":"restored","prefs":{"shieldsUp":true}}`,
			`{"shieldsUp":false}`, `{"shieldsUp":true}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, tt := prSetup(t, tc.prefs)
			prSeed(t, h, "c1", tc.action, tc.change[0], tc.change[1])
			raw, err := tt.prefsRepair().Handler.Apply(context.Background(), prEnv(h, "c9", "c1"), json.RawMessage(tc.in))
			if err != nil {
				t.Fatal(err)
			}
			if len(h.local.Edits) != 1 || !reflect.DeepEqual(h.local.Edits[0], tc.patch) {
				t.Errorf("edits %+v, want exactly %+v: the patch covers the target's fields and no others", h.local.Edits, tc.patch)
			}
			if !prSame(t, raw, []byte(tc.out)) {
				t.Errorf("output %s, want %s", raw, tc.out)
			}
			effects := prEffects(h)
			if len(effects) != 1 {
				t.Fatalf("%d effects journaled, want 1", len(effects))
			}
			e := effects[0]
			if e.Kind != core.EffectModify || e.Resource != "tailscale.prefs:node" || e.Local || e.CommitWait != 2*time.Minute || e.State != core.EffectMade {
				t.Errorf("effect %+v, want a made, non-local modify of tailscale.prefs:node with a 2 minute commit wait", e)
			}
			if !prSame(t, e.Before.State, []byte(tc.before)) {
				t.Errorf("before %s, want %s", e.Before.State, tc.before)
			}
			if e.After == nil || !prSame(t, e.After.State, []byte(tc.after)) || e.Post == nil || !prSame(t, e.Post.State, []byte(tc.after)) {
				t.Errorf("after %+v and post %+v, want %s", e.After, e.Post, tc.after)
			}
		})
	}
}

func TestPrApplyRefusals(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		code     errcode.Code // "" for an error that is not coded
		definite bool
		state    core.EffectState
	}{
		{"tailscaled refuses the edit", &tslocal.APIError{StatusCode: 400, Message: "bad route"}, errcode.InvalidInput, true, core.EffectSkipped},
		{"tailscaled is not reachable", fmt.Errorf("dial: %w", tslocal.ErrUnavailable), errcode.TailscaleUnavailable, true, core.EffectSkipped},
		{"tailscaled fails", &tslocal.APIError{StatusCode: 500, Message: "boom"}, "", false, core.EffectIntended},
		{"the call fails without an answer", errors.New("connection reset"), "", false, core.EffectIntended},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, tt := prSetup(t, tslocal.Prefs{RouteAll: true, ShieldsUp: true})
			prSeedSet(t, h)
			h.local.Errs["EditPrefs"] = tc.err
			_, err := tt.prefsRepair().Handler.Apply(context.Background(), prEnv(h, "c9", "c1"), json.RawMessage(`{}`))
			if err == nil || core.IsDefinite(err) != tc.definite {
				t.Fatalf("err = %v (definite %v), want an error with definite %v", err, core.IsDefinite(err), tc.definite)
			}
			var coded *errcode.Error
			if tc.code != "" && (!errors.As(err, &coded) || coded.Code != tc.code) {
				t.Errorf("err = %v, want code %s", err, tc.code)
			}
			if effects := prEffects(h); len(effects) != 1 || effects[0].State != tc.state {
				t.Errorf("effects %+v, want one %s: the effect is journaled before the call", effects, tc.state)
			}
		})
	}
}

func TestPrApplyChecksWhatTailscaledAnswers(t *testing.T) {
	h, tt := prSetup(t, tslocal.Prefs{RouteAll: true, ShieldsUp: true})
	prSeedSet(t, h)
	tt.d.local = func() localAPI { return prStale{thLocal{h.local}} }
	out, err := tt.prefsRepair().Handler.Apply(context.Background(), prEnv(h, "c9", "c1"), json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "shieldsUp") || core.IsDefinite(err) {
		t.Fatalf("apply = %s, %v; want an error naming shieldsUp that is not definite: the edit committed", out, err)
	}
	if strings.Contains(string(out), "repairOutcome") {
		t.Errorf("output %s names an outcome although the preferences do not hold", out)
	}
}

func TestPrVerify(t *testing.T) {
	ctx := context.Background()
	h, tt := prSetup(t, tslocal.Prefs{RouteAll: true, ShieldsUp: true, RunSSH: true})
	prSeedSet(t, h)
	def, in, env := tt.prefsRepair(), json.RawMessage(`{}`), prEnv(h, "c9", "c1")
	out, err := def.Handler.Apply(ctx, env, in)
	if err != nil {
		t.Fatal(err)
	}
	if err := def.Handler.Verify(ctx, env, in, out); err != nil {
		t.Errorf("verify after apply: %v", err)
	}
	if err := def.Handler.Verify(ctx, env, in, json.RawMessage(`{"repairOutcome":"restored","prefs":{}}`)); err != nil {
		t.Errorf("verify without the output, as recovery calls it: %v", err)
	}
	h.local.Prefs.ShieldsUp = true
	if err := def.Handler.Verify(ctx, env, in, out); err == nil || !strings.Contains(err.Error(), "shieldsUp") {
		t.Errorf("verify with shields up = %v, want an error naming shieldsUp", err)
	}
	h.local.Prefs.ShieldsUp, h.local.Prefs.RouteAll = false, true
	if err := def.Handler.Verify(ctx, env, in, out); err == nil || !strings.Contains(err.Error(), "acceptRoutes") {
		t.Errorf("verify with routes accepted = %v, want an error naming acceptRoutes", err)
	}
	h.local.Errs["Prefs"] = errors.New("boom")
	if err := def.Handler.Verify(ctx, env, in, out); err == nil {
		t.Error("verify passed with the preferences unreadable, want an error")
	}
}

func TestPrSnapshotAndRevert(t *testing.T) {
	ctx := context.Background()
	start := tslocal.Prefs{WantRunning: true, RouteAll: true, ShieldsUp: true}
	h, tt := prSetup(t, start)
	prSeedSet(t, h)
	def, in, env := tt.prefsRepair(), json.RawMessage(`{}`), prEnv(h, "c9", "c1")
	snap, err := def.Handler.Snapshot(ctx, env, in)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"acceptRoutes":true,"shieldsUp":true}`; !prSame(t, snap, []byte(want)) {
		t.Errorf("snapshot %s, want the current values of the fields the repair sets: %s", snap, want)
	}
	if _, err := def.Handler.Apply(ctx, env, in); err != nil {
		t.Fatal(err)
	}
	if h.local.Prefs.RouteAll || h.local.Prefs.ShieldsUp {
		t.Fatalf("apply left the preferences %+v", *h.local.Prefs)
	}
	out, err := def.Handler.Revert(ctx, env, snap)
	if err != nil {
		t.Fatal(err)
	}
	if want := []core.EffectOutcome{{Seq: 1, State: core.EffectUndone}}; !reflect.DeepEqual(out.Effects, want) || out.Override {
		t.Errorf("outcome %+v, want the effect undone and no override", out)
	}
	if !reflect.DeepEqual(*h.local.Prefs, start) {
		t.Errorf("preferences %+v after the revert, want %+v: an unconfirmed repair reverts like any prefs.set", *h.local.Prefs, start)
	}
}

func TestPrObserve(t *testing.T) {
	ctx := context.Background()
	h, tt := prSetup(t, tslocal.Prefs{WantRunning: true, ShieldsUp: true})
	e := core.Effect{Resource: "tailscale.prefs:node", Before: core.StateDoc{State: json.RawMessage(`{"shieldsUp":false}`)}}
	obs, err := tt.prefsRepair().Handler.Observe(ctx, h.env("c9"), e)
	if err != nil || !prSame(t, obs.Current.State, []byte(`{"shieldsUp":true}`)) || obs.Matches != 1 || !obs.Exclusive {
		t.Errorf("observation %+v (%v), want only the field the effect covers, one exclusive match", obs, err)
	}
	e.Resource = "tailscale.node:self"
	if _, err := tt.prefsRepair().Handler.Observe(ctx, h.env("c9"), e); err == nil {
		t.Error("observed another resource, want an error")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

```bash
mkdir -p /root/.cache/w4-tmp/27 && export TMPDIR=/root/.cache/w4-tmp/27 GOTOOLCHAIN=local
timeout 300 go test -count=1 -run 'TestPr' ./internal/modules/tailscale/
```

Expected: FAIL to build, `tt.prefsRepair undefined (type ts has no field or method prefsRepair)`. Quote the output.

- [ ] **Step 3: Implement `internal/modules/tailscale/prefs_repair.go`**

Decisions this task embodies. The repair reads the repaired change itself (spec 4.1): its redacted input and its sealed snapshot, both from `core_changes`, so `tailscale.prefs.set` stores nothing of its own. The repair keeps prefs.set's snapshot, effect and revert shapes (Contracts "Tailscale preferences"), so an unconfirmed repair reverts like any prefs.set and its `Revert` and `Observe` are prefs.set's. The repair never edits a field the target does not name.

1. `func (t ts) prefsRepair() core.ActionDef` returns `core.DefineRevertible[PrefsRepairInput, PrefsRepaired]` with `ActionSpec{Name: "tailscale.prefs.repair", Summary: "Repair a change to the node's Tailscale preferences.", Version: 1, Side: core.SideAgent, LockKeys: []string{lockKey}, RepairedBy: "tailscale.prefs.repair", Guards: []core.GuardSpec{{ID: link.GuardID, Overridable: true}}, Available: t.available}`; no `ApplyTimeout` and no `Class` (`DefineRevertible` sets it). The guard is overridable because spec 4.1 declares `tailscale.link` overridable and `ts.linkGuard` always reports it so. The handler is the value type `prefsRepairHandler{t ts}` with value-receiver methods Plan, Apply, Verify, Snapshot, Revert and Observe, and no Inverse. `Revert` and `Observe` return `prefsSetHandler{h.t}.Revert` and `prefsSetHandler{h.t}.Observe` unchanged.
2. `prefsRepairTarget(ctx, env)` as Contracts "Tailscale prefs repair" gives it. Its first check is the `RepairOf` refusal; then one `SELECT action, input_json, snapshot_ct FROM core_changes WHERE id = ?` on `env.DB.Read()`; `sql.ErrNoRows` is `errcode.NotFound` with the detail "change <id> not found", marked `core.Definite`; the snapshot is opened with `env.Sealer.Open("core_changes.snapshot_ct."+id, ct)` and decoded as `PrefsFields` (an open or decode failure is a plain wrapped error, not definite, because the stored change is damaged). For `tailscale.prefs.set` the input is decoded as `PrefsSetInput` (its guard members are ignored) and `set` is `prefsSetNormalize` of its `PrefsFields`; for `tailscale.prefs.repair` `set` stays the zero `PrefsFields`; any other action is `request.invalid_input`, marked `core.Definite`, "change <id> is a <action> change, not a preferences change".
3. `prefsRepairResolve(ctx, env, outcome string) (kept bool, target PrefsFields, err error)` serves every method that needs the target. It checks, in this order, with `request.invalid_input` marked `core.Definite` and the field error at the path as Contracts lists: the `RepairOf` refusal (through `prefsRepairTarget`'s own check, before anything else is read); an outcome other than `""`, `restore` or `keep` (field `/outcome`, detail `the outcome "<x>" is neither restore nor keep`) before the change is read; then it reads the target; `keep` of a `tailscale.prefs.repair` change is refused (field `/outcome`, detail "change <id> is a repair; repair it with outcome restore"). `target` is `prior` for `restore` (also the default) and `set` for `keep`.
4. Rendering, shared by the diff and the errors: the fields of a `PrefsFields` in the order `wantRunning`, `advertiseRoutes`, `exitNode`, `advertiseExitNode`, `acceptRoutes`, `shieldsUp`, `runSSH`, only those it sets, as name and value text: booleans `true` or `false`; routes joined with `", "` and `none` when empty; an exit node `none` when empty. `prefsRepairLines(cur, target PrefsFields) []core.DiffLine` gives, per field of `target`: `{Op: " ", Text: "<name>: <value> (unchanged)"}` when `cur` holds the same value, else `{Op: "-", Text: "<name>: <current>"}` then `{Op: "+", Text: "<name>: <target>"}`. `prefsRepairMismatch(got, want PrefsFields) string` is "" when every field of `want` equals the same field of `got`, else the sentence for the first differing field in that order: `tailscale: <name> is <got value> after the repair, want <want value>`.
5. Plan (reads the repaired change and the node, writes nothing, runs nothing): resolve; `lp, raw, err := t.currentLinkPrefs(ctx)`; `cur := prefsSetPick(prefsFieldsOf(raw), target)`; `after := prefsSetLinkAfter(lp, prefsFieldsOf(raw), target)`; `g, err := t.linkGuard(ctx, env, in.Guarded, lp, link.Change{Kind: "prefs", After: after})`; `PlanOutcome` has `Diff: prefsRepairLines(cur, target)`, `Guards: []core.Guard{g}`, `StateHash` the hex SHA-256 of the JSON of `cur`, no `Confirmation`.
6. Snapshot: resolve, read the preferences through `t.d.local()`, return the JSON of `prefsSetPick(prefsFieldsOf(raw), target)`: the current values of exactly the fields the repair sets.
7. Apply: resolve; read the preferences; `patch, err := prefsSetPatch(raw, target)`; `Intend` one effect `{Kind: core.EffectModify, Resource: prefsEffectResource, Local: false, CommitWait: prefsCommitWait, Before: {State: JSON of the current pick}, After: &{State: JSON of target}}` before the call; call `EditPrefs`. `prefsRepairRefusal` classifies an error as prefs.set does: `tslocal.ErrUnavailable` becomes `core.Definite(errcode.Wrap(errcode.TailscaleUnavailable, err, "tailscaled does not answer on its socket"))`; an `*tslocal.APIError` with a status from 400 to 499 becomes `core.Definite(errcode.Wrap(errcode.InvalidInput, err, "tailscaled refused the preferences: " + Message))`; for these two the effect is `Skipped` and the definite error is returned; any other error is returned unchanged and the effect stays `intended`. On success take `got := prefsSetPick(prefsFieldsOf(returned), target)`, record `Made` with `prefsEffectResource` and the JSON of `got` as the post state, then require `prefsRepairMismatch(got, target) == ""`: a difference returns that sentence as a plain error (the edit committed, so it is not definite), and the output is not built. Otherwise return `PrefsRepaired{RepairOutcome: "restored" (or "kept" when `kept`), Prefs: got}`.
8. Verify, from the input and the repaired change alone: resolve, read the preferences, and return the `prefsRepairMismatch` sentence (as an error) for `prefsSetPick(prefsFieldsOf(...), target)` against `target`; it ignores its output.

Add no other file and do not touch `tailscale.go`; `TestManifestIsASkeleton` keeps passing.

- [ ] **Step 4: Run the tests and the task checks**

```bash
mkdir -p /root/.cache/w4-tmp/27 && export TMPDIR=/root/.cache/w4-tmp/27 GOTOOLCHAIN=local
timeout 300 go test -count=1 -run 'TestPr' -v ./internal/modules/tailscale/
timeout 600 go test -race -count=1 ./internal/modules/tailscale/...
timeout 600 go build ./... && timeout 300 go vet ./...
timeout 900 go test -count=1 ./...
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
rm -rf /root/.cache/w4-tmp/27 && go clean -testcache
```

Expected: every test of the task `--- PASS`, every package line of `go test ./...` `ok` or `?`, `go vet` and `go mod tidy -diff` print nothing, `golangci-lint` prints `0 issues.`. Record the real output.

- [ ] **Step 5: Commit**

```bash
git add internal/modules/tailscale/prefs_repair.go internal/modules/tailscale/prefs_repair_test.go
git commit -m "feat(tailscale): add prefs.repair action"
```

### Task 28: tailscale: package.unpin

**Files:**
- Create: `internal/modules/tailscale/package_unpin.go`
- Test: `internal/modules/tailscale/package_unpin_test.go`

**Interfaces:**
- Consumes: Contracts "Tailscale wire shapes" (`UnpinInput`, `Unpinned`, and the action-spec row of `tailscale.package.unpin`); "Tailscale internals" (`ts`, `lockKey`, `ts.available`, `ts.installedVersion`, `ts.held`, `run`, `pinRow`, `activePin`, `setPinUnpinned`, `loadPin`, `insertPin`); "Tailscale pin" (the effect resource `tailscale.package:tailscale` and Task 28's unpin effect); "Tailscale test host" (`newTSHost`, `tsHost`, `tsRun`, `h.script`, `h.ran`, `h.reads`, `h.db`, `h.clock`, `h.journal`, `thDpkgStatus`, `thDpkgPath`); `core.DefineTask`, `core.TaskMutator`, `core.Undoer`, `core.Observer` (`internal/core/contract.go`, `define.go`).
- Produces: `func (t ts) packageUnpin() core.ActionDef`, the constructor of `tailscale.package.unpin/v1`. Task 31 adds it to `Manifest().Actions`; this task does not edit `tailscale.go`. The handler type is `packageUnpinHandler`, the constant `packageUnpinResource` is `"tailscale.package:tailscale"`; neither is a Contract, and every other package-level helper of `package_unpin.go` is named with the prefix `packageUnpin`.

**Items:** 21

**Depends on:** Task 24

`tailscale.package.unpin` (spec 4.1) takes no input and reverses the newest pin in phase `done` that is not yet unpinned (`activePin`): it restores the hold state the pin recorded (`prior_held`) and leaves the installed version alone. With no such pin it plans an empty diff and applies as a no-op, which is what `Converges` requires of the forward composite `derp.server.remove`. Its undo re-holds. The tests are module-private and use only the fakes of Contracts "Tailscale test host"; no test runs `apt-mark` on this host. The helpers `upRunner`, `upSetup`, `upEnv`, `upPin`, `upAdd`, `upLoad`, `upHolds` and `upSame` are declared by this task's test file only. `upRunner` stands for apt-mark: it keeps the hold state in a boolean the test owns and runs every vector through the host fake, which records it and checks it against the production exec allowlist.

- [ ] **Step 1: Write the failing test**

Create `internal/modules/tailscale/package_unpin_test.go` with exactly this content:

```go
package tailscale

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
)

const (
	upAptMark  = "/usr/bin/apt-mark"
	upResource = "tailscale.package:tailscale"
)

// upRunner runs vectors on the host and keeps the hold state of the tailscale package in *held, as apt-mark would.
type upRunner struct {
	h    *tsHost
	held *bool
}

func (r upRunner) Run(ctx context.Context, cmd core.Cmd) (core.CmdResult, error) {
	res, err := r.h.Run(ctx, cmd)
	if err != nil || res.ExitCode != 0 || cmd.Path != upAptMark {
		return res, err
	}
	switch strings.Join(cmd.Args, " ") {
	case "hold tailscale":
		*r.held = true
	case "unhold tailscale":
		*r.held = false
	case "showhold":
		if *r.held {
			res.Stdout = []byte("tailscale\n")
		}
	}
	return res, nil
}

// upSetup is a host with tailscale installed at version and held or not, behind a ts.
func upSetup(t *testing.T, version string, held bool) (*tsHost, ts, *bool) {
	t.Helper()
	h := newTSHost(t)
	h.reads[thDpkgPath] = thDpkgStatus(version)
	return h, h.ts(), &held
}

// upEnv is the environment of a call of change changeID (empty: a plan) whose apt-mark acts on *held.
func upEnv(h *tsHost, held *bool, changeID string) core.Env {
	env := h.env(changeID)
	env.Exec = upRunner{h: h, held: held}
	return env
}

// upPin is a pin of 1.102.0 over 1.104.1 that reached phase done.
func upPin(id string, priorHeld bool, at time.Time) pinRow {
	return pinRow{
		ChangeID: id, Version: "1.102.0", Arch: "amd64", PriorVersion: "1.104.1", PriorHeld: priorHeld, PriorBackend: "Running",
		Phase: "done", UpdatedAt: at,
	}
}

func upAdd(t *testing.T, h *tsHost, p pinRow) {
	t.Helper()
	ctx := context.Background()
	if err := h.db.Write(ctx, func(tx *sql.Tx) error { return insertPin(ctx, tx, p) }); err != nil {
		t.Fatal(err)
	}
}

func upLoad(t *testing.T, h *tsHost, id string) pinRow {
	t.Helper()
	p, ok, err := loadPin(context.Background(), h.db.Read(), id)
	if err != nil || !ok {
		t.Fatalf("pin %s: found %v, err %v", id, ok, err)
	}
	return p
}

// upHolds lists the apt-mark vectors, other than showhold, run from index from on, as "hold tailscale" or
// "unhold tailscale".
func upHolds(h *tsHost, from int) []string {
	var out []string
	for _, v := range h.ran[from:] {
		if v[0] == upAptMark && v[1] != "showhold" {
			out = append(out, strings.Join(v[1:], " "))
		}
	}
	return out
}

// upSame compares two JSON documents as values.
func upSame(t *testing.T, a, b []byte) bool {
	t.Helper()
	var va, vb any
	if err := json.Unmarshal(a, &va); err != nil {
		t.Fatalf("document %s: %v", a, err)
	}
	if err := json.Unmarshal(b, &vb); err != nil {
		t.Fatalf("document %s: %v", b, err)
	}
	return reflect.DeepEqual(va, vb)
}

func TestUpActionDefinition(t *testing.T) {
	_, tt, _ := upSetup(t, "1.102.0", true)
	def := tt.packageUnpin()
	s, h := def.Spec, def.Handler
	if s.Name != "tailscale.package.unpin" || s.Version != 1 || s.Class != core.ClassCompensable || s.Side != core.SideAgent ||
		!slices.Equal(s.LockKeys, []string{lockKey}) || !s.Converges || s.ApplyTimeout != 5*time.Minute || !s.Task || !s.LongRunning ||
		s.Available == nil || len(s.Guards) != 0 {
		t.Errorf("spec %+v, want a converging compensable agent task locking %q, with a 5 minute timeout and an availability check", s, lockKey)
	}
	if def.InputType != reflect.TypeFor[UnpinInput]() || def.OutputType != reflect.TypeFor[Unpinned]() {
		t.Errorf("input %v and output %v, want UnpinInput and Unpinned", def.InputType, def.OutputType)
	}
	if h.Plan == nil || h.Task == nil || h.Verify == nil || h.Observe == nil || h.Inverse == nil || h.Apply != nil || h.Recover != nil {
		t.Error("handler lacks Plan, Task, Verify, Observe or Inverse, or has an Apply or a Recover")
	}
}

func TestUpPlan(t *testing.T) {
	line := func(op, text string) core.DiffLine { return core.DiffLine{Op: op, Text: text} }
	release := line(" ", "release the pin of change p1: tailscale 1.102.0 stays installed")
	tests := []struct {
		name      string
		priorHeld bool
		held      bool
		want      []core.DiffLine
	}{
		{"the pin took the hold", false, true, []core.DiffLine{release, line("-", "tailscale is held"), line("+", "tailscale is not held")}},
		{"the hold was there before the pin", true, true, []core.DiffLine{release, line(" ", "tailscale is held (unchanged)")}},
		{"the hold the prior state needs was released", true, false, []core.DiffLine{release, line("-", "tailscale is not held"), line("+", "tailscale is held")}},
		{"the hold is already gone", false, false, []core.DiffLine{release, line(" ", "tailscale is not held (unchanged)")}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, tt, held := upSetup(t, "1.102.0", tc.held)
			upAdd(t, h, upPin("p1", tc.priorHeld, h.clock.Now()))
			out, err := tt.packageUnpin().Handler.Plan(context.Background(), upEnv(h, held, ""), json.RawMessage(`{}`))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(out.Diff, tc.want) || out.Confirmation != "" || len(out.Guards) != 0 || len(out.StateHash) != 64 {
				t.Errorf("plan %+v, want diff %v, no confirmation, no guards and a 64-digit hash", out, tc.want)
			}
			if got := upHolds(h, 0); len(got) != 0 {
				t.Errorf("plan changed the hold: %v", got)
			}
		})
	}
}

func TestUpPlanWithNoPin(t *testing.T) {
	h, tt, held := upSetup(t, "1.104.1", true)
	upAdd(t, h, pinRow{ChangeID: "p0", Version: "1.102.0", Arch: "amd64", PriorVersion: "1.104.1", PriorBackend: "Running", Phase: "rolled_back", UpdatedAt: h.clock.Now()})
	upAdd(t, h, func() pinRow { p := upPin("p1", false, h.clock.Now()); p.UnpinnedBy = "c0"; return p }())
	out, err := tt.packageUnpin().Handler.Plan(context.Background(), upEnv(h, held, ""), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Diff) != 0 || len(out.Guards) != 0 || out.Confirmation != "" || len(out.StateHash) != 64 {
		t.Errorf("plan %+v, want an empty diff, no guards and a 64-digit hash when no pin is active", out)
	}
	if len(h.ran) != 0 {
		t.Errorf("plan ran %v, want nothing when no pin is active", h.ran)
	}
}

func TestUpPlanStateHash(t *testing.T) {
	hash := func(pinID string, held bool, version string) string {
		t.Helper()
		h, tt, heldPtr := upSetup(t, version, held)
		upAdd(t, h, upPin(pinID, false, h.clock.Now()))
		out, err := tt.packageUnpin().Handler.Plan(context.Background(), upEnv(h, heldPtr, ""), json.RawMessage(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		return out.StateHash
	}
	base := hash("p1", true, "1.102.0")
	if again := hash("p1", true, "1.102.0"); again != base {
		t.Error("the same pin, hold and version have different state hashes")
	}
	for name, other := range map[string]string{
		"another pin":     hash("p2", true, "1.102.0"),
		"another hold":    hash("p1", false, "1.102.0"),
		"another version": hash("p1", true, "1.103.0"),
	} {
		if other == base {
			t.Errorf("%s has the same state hash", name)
		}
	}
}

func TestUpTask(t *testing.T) {
	tests := []struct {
		name      string
		priorHeld bool
		held      bool
		wantRan   []string
	}{
		{"release the hold the pin took", false, true, []string{"unhold tailscale"}},
		{"keep the hold that was there before the pin", true, true, nil},
		{"restore a hold that someone released", true, false, []string{"hold tailscale"}},
		{"the hold is already gone", false, false, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			h, tt, held := upSetup(t, "1.102.0", tc.held)
			upAdd(t, h, upPin("p1", tc.priorHeld, h.clock.Now()))
			raw, err := tt.packageUnpin().Handler.Task(ctx, upEnv(h, held, "c1"), json.RawMessage(`{}`))
			if err != nil {
				t.Fatal(err)
			}
			if want := fmt.Sprintf(`{"version":"1.102.0","held":%v}`, tc.priorHeld); string(raw) != want {
				t.Errorf("output %s, want %s", raw, want)
			}
			if got := upHolds(h, 0); !slices.Equal(got, tc.wantRan) {
				t.Errorf("hold changes %v, want %v", got, tc.wantRan)
			}
			for _, v := range h.ran {
				if v[0] != upAptMark {
					t.Errorf("ran %v, want only apt-mark: unpin installs and removes nothing", v)
				}
			}
			if *held != tc.priorHeld {
				t.Errorf("hold state %v after the task, want the pin's prior state %v", *held, tc.priorHeld)
			}
			if p := upLoad(t, h, "p1"); p.UnpinnedBy != "c1" {
				t.Errorf("pin unpinned by %q, want c1", p.UnpinnedBy)
			}
			effects := h.journal.effects()
			if len(effects) != 1 {
				t.Fatalf("%d effects journaled, want 1", len(effects))
			}
			e, want := effects[0], fmt.Sprintf(`{"held":%v}`, tc.priorHeld)
			if e.Kind != core.EffectModify || e.Resource != upResource || !e.Local || !e.InverseLocal || e.State != core.EffectMade {
				t.Errorf("effect %+v, want a made modify of %s that is local both ways", e, upResource)
			}
			if !upSame(t, e.Before.State, []byte(`{"held":true}`)) {
				t.Errorf("before %s, want the hold the pin set", e.Before.State)
			}
			if e.After == nil || !upSame(t, e.After.State, []byte(want)) || e.Post == nil || !upSame(t, e.Post.State, []byte(want)) {
				t.Errorf("after %+v and post %+v, want %s", e.After, e.Post, want)
			}
		})
	}
}

func TestUpTaskWithNoPin(t *testing.T) {
	h, tt, held := upSetup(t, "1.104.1", true)
	raw, err := tt.packageUnpin().Handler.Task(context.Background(), upEnv(h, held, "c1"), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"version":"1.104.1","held":true}` {
		t.Errorf("output %s, want the installed version and the hold as they are", raw)
	}
	if got := upHolds(h, 0); len(got) != 0 || len(h.journal.effects()) != 0 || !*held {
		t.Errorf("hold changes %v, effects %v, held %v; want nothing changed and nothing journaled", got, h.journal.effects(), *held)
	}
}

func TestUpTaskTakesTheNewestPinFirst(t *testing.T) {
	ctx := context.Background()
	h, tt, held := upSetup(t, "1.102.0", true)
	upAdd(t, h, upPin("p1", false, h.clock.Now()))
	upAdd(t, h, upPin("p2", false, h.clock.Now().Add(time.Hour)))
	def, in := tt.packageUnpin(), json.RawMessage(`{}`)
	if _, err := def.Handler.Task(ctx, upEnv(h, held, "c1"), in); err != nil {
		t.Fatal(err)
	}
	if a, b := upLoad(t, h, "p1").UnpinnedBy, upLoad(t, h, "p2").UnpinnedBy; a != "" || b != "c1" {
		t.Errorf("unpinned by %q and %q, want only the newer pin taken", a, b)
	}
	if _, err := def.Handler.Task(ctx, upEnv(h, held, "c2"), in); err != nil {
		t.Fatal(err)
	}
	if a := upLoad(t, h, "p1").UnpinnedBy; a != "c2" {
		t.Errorf("older pin unpinned by %q, want c2", a)
	}
}

func TestUpTaskFailsWhenAptMarkFails(t *testing.T) {
	h, tt, held := upSetup(t, "1.102.0", true)
	upAdd(t, h, upPin("p1", false, h.clock.Now()))
	h.script(upAptMark+" unhold tailscale", tsRun{res: core.CmdResult{ExitCode: 100, Stderr: []byte("E: cannot unhold")}})
	if _, err := tt.packageUnpin().Handler.Task(context.Background(), upEnv(h, held, "c1"), json.RawMessage(`{}`)); err == nil {
		t.Fatal("task succeeded, want the apt-mark failure")
	}
	if p := upLoad(t, h, "p1"); p.UnpinnedBy != "" {
		t.Errorf("pin unpinned by %q after a failed unhold, want it still active", p.UnpinnedBy)
	}
	if effects := h.journal.effects(); len(effects) != 1 || effects[0].State != core.EffectIntended {
		t.Errorf("effects %+v, want one intended: it is journaled before the call", effects)
	}
}

func TestUpConverges(t *testing.T) {
	ctx := context.Background()
	h, tt, held := upSetup(t, "1.102.0", true)
	upAdd(t, h, upPin("p1", false, h.clock.Now()))
	def, in := tt.packageUnpin(), json.RawMessage(`{}`)
	if _, err := def.Handler.Task(ctx, upEnv(h, held, "c1"), in); err != nil {
		t.Fatal(err)
	}
	out, err := def.Handler.Plan(ctx, upEnv(h, held, ""), in)
	if err != nil || len(out.Diff) != 0 {
		t.Errorf("plan after the task = %+v, %v; want an empty diff", out, err)
	}
}

func TestUpVerify(t *testing.T) {
	ctx := context.Background()
	h, tt, held := upSetup(t, "1.102.0", true)
	upAdd(t, h, upPin("p1", false, h.clock.Now()))
	def, in := tt.packageUnpin(), json.RawMessage(`{}`)
	if err := def.Handler.Verify(ctx, upEnv(h, held, "c2"), in, json.RawMessage(`{}`)); err != nil {
		t.Errorf("verify of a change that unpinned nothing: %v", err)
	}
	out, err := def.Handler.Task(ctx, upEnv(h, held, "c1"), in)
	if err != nil {
		t.Fatal(err)
	}
	if err := def.Handler.Verify(ctx, upEnv(h, held, "c1"), in, out); err != nil {
		t.Errorf("verify after the task: %v", err)
	}
	if err := def.Handler.Verify(ctx, upEnv(h, held, "c1"), in, json.RawMessage(`{}`)); err != nil {
		t.Errorf("verify without the output, as recovery calls it: %v", err)
	}
	*held = true
	if err := def.Handler.Verify(ctx, upEnv(h, held, "c1"), in, out); err == nil || !strings.Contains(err.Error(), "held") {
		t.Errorf("verify with the hold back = %v, want an error naming the hold", err)
	}
}

func TestUpObserve(t *testing.T) {
	ctx := context.Background()
	h, tt, held := upSetup(t, "1.102.0", true)
	def := tt.packageUnpin()
	for _, want := range []bool{true, false} {
		*held = want
		obs, err := def.Handler.Observe(ctx, upEnv(h, held, "c1"), core.Effect{Resource: upResource})
		if err != nil || !upSame(t, obs.Current.State, []byte(fmt.Sprintf(`{"held":%v}`, want))) || obs.Matches != 1 || !obs.Exclusive {
			t.Errorf("observation %+v (%v), want the hold state %v, one exclusive match", obs, err, want)
		}
	}
	if _, err := def.Handler.Observe(ctx, upEnv(h, held, "c1"), core.Effect{Resource: "tailscale.file:/etc/apt/sources.list.d/tailscale.list"}); err == nil {
		t.Error("observed another resource, want an error")
	}
	h.script(upAptMark+" showhold", tsRun{res: core.CmdResult{ExitCode: 100}})
	if _, err := def.Handler.Observe(ctx, upEnv(h, held, "c1"), core.Effect{Resource: upResource}); err == nil {
		t.Error("observed with showhold failing, want an error")
	}
}

func TestUpInverse(t *testing.T) {
	ctx := context.Background()
	e := core.Effect{ChangeID: "c1", Resource: upResource, State: core.EffectMade}
	tests := []struct {
		name      string
		priorHeld bool
		heldAfter bool // the hold when the undo starts
		wantRan   []string
	}{
		{"the pin took the hold, so the undo takes it back", false, false, []string{"hold tailscale"}},
		{"the hold stayed through the unpin", true, true, nil},
		{"someone already held it again", false, true, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, tt, held := upSetup(t, "1.102.0", true)
			upAdd(t, h, upPin("p1", tc.priorHeld, h.clock.Now()))
			def, in := tt.packageUnpin(), json.RawMessage(`{}`)
			if _, err := def.Handler.Task(ctx, upEnv(h, held, "c1"), in); err != nil {
				t.Fatal(err)
			}
			*held = tc.heldAfter
			from := len(h.ran)
			res, err := def.Handler.Inverse(ctx, upEnv(h, held, "c1"), e)
			if err != nil || res != (core.InverseResult{}) {
				t.Fatalf("inverse = %+v, %v; want an empty result", res, err)
			}
			if got := upHolds(h, from); !slices.Equal(got, tc.wantRan) {
				t.Errorf("hold changes %v, want %v", got, tc.wantRan)
			}
			if !*held {
				t.Error("the package is not held after the undo")
			}
			if p := upLoad(t, h, "p1"); p.UnpinnedBy != "" {
				t.Errorf("pin unpinned by %q after the undo, want it active again", p.UnpinnedBy)
			}
			if p, ok, err := activePin(ctx, h.db.Read()); err != nil || !ok || p.ChangeID != "p1" {
				t.Errorf("active pin = %+v, %v, %v; want p1", p, ok, err)
			}
		})
	}
}

func TestUpInverseFailures(t *testing.T) {
	ctx := context.Background()
	h, tt, held := upSetup(t, "1.102.0", true)
	upAdd(t, h, upPin("p1", false, h.clock.Now()))
	def, in := tt.packageUnpin(), json.RawMessage(`{}`)
	if _, err := def.Handler.Task(ctx, upEnv(h, held, "c1"), in); err != nil {
		t.Fatal(err)
	}
	from := len(h.ran)
	if _, err := def.Handler.Inverse(ctx, upEnv(h, held, "c1"), core.Effect{ChangeID: "c1", Resource: "tailscale.file:/x"}); err == nil || len(h.ran) != from {
		t.Errorf("inverse of another resource = %v after running %v, want an error and no command", err, h.ran[from:])
	}
	h.script(upAptMark+" hold tailscale", tsRun{res: core.CmdResult{ExitCode: 100, Stderr: []byte("E: cannot hold")}})
	if _, err := def.Handler.Inverse(ctx, upEnv(h, held, "c1"), core.Effect{ChangeID: "c1", Resource: upResource}); err == nil {
		t.Fatal("inverse succeeded, want the apt-mark failure")
	}
	if p := upLoad(t, h, "p1"); p.UnpinnedBy != "c1" {
		t.Errorf("pin unpinned by %q after a failed re-hold, want c1: the pin is active again only once the hold is back", p.UnpinnedBy)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

```bash
mkdir -p /root/.cache/w4-tmp/28 && export TMPDIR=/root/.cache/w4-tmp/28 GOTOOLCHAIN=local
timeout 300 go test -count=1 -run 'TestUp' ./internal/modules/tailscale/
```

Expected: FAIL to build, `tt.packageUnpin undefined (type ts has no field or method packageUnpin)`. Quote the output.

- [ ] **Step 3: Implement `internal/modules/tailscale/package_unpin.go`**

Decisions this task embodies. The unpin reverses a pin's hold, not its version (the pin's rollback is the undo of the pin, Task 29). Which pin it reverses is decided from the agent database at plan and again in the task, so a pin that appears or is unpinned between them changes the plan's state hash and the plan is refused with `plan.state_changed`. The hold state it restores is the pin's own `prior_held`, whatever the host shows now.

1. `func (t ts) packageUnpin() core.ActionDef` returns `core.DefineTask[UnpinInput, Unpinned]` with `ActionSpec{Name: "tailscale.package.unpin", Summary: "Release the pin of the tailscale package and restore the hold state it replaced.", Version: 1, Class: core.ClassCompensable, Side: core.SideAgent, LockKeys: []string{lockKey}, Converges: true, ApplyTimeout: 5 * time.Minute, Available: t.available}`; `DefineTask` sets `Task` and `LongRunning`. The handler is the value type `packageUnpinHandler{t ts}` with value-receiver methods Plan, Task, Verify, Observe and Inverse, so `DefineTask` finds the `Undoer` by type assertion; it has no Apply, no Recover and no guards. The constant `packageUnpinResource = "tailscale.package:tailscale"`.
2. Plan (runs nothing but the read of the holds, writes nothing): `p, ok, err := activePin(ctx, env.DB.Read())`. With no active pin the outcome has an empty diff, no guards, no confirmation, runs no command, and `StateHash` the hex SHA-256 of the text `no active pin`. With a pin: `installed` from `t.installedVersion`, `held` from `t.held`, `want := p.PriorHeld`. The diff is `{Op: " ", Text: "release the pin of change <p.ChangeID>: tailscale <installed> stays installed"}`, then, when `held == want`, `{Op: " ", Text: "<hold text> (unchanged)"}`, else `{Op: "-", Text: "<hold text of held>"}` and `{Op: "+", Text: "<hold text of want>"}`, where the hold text is `tailscale is held` or `tailscale is not held`. `StateHash` is the hex SHA-256 of `<p.ChangeID>\n<installed>\n<held as true or false>`.
3. Task: `activePin` again. With none, return `Unpinned{Version: installed, Held: held}` from the host as it is, journaling and changing nothing. With a pin: read `installed` and `held`; `Intend` one effect `{Kind: core.EffectModify, Resource: packageUnpinResource, Local: true, InverseLocal: true, Before: {State: {"held": true}}, After: &{State: {"held": p.PriorHeld}}}` (Contracts "Tailscale pin"); when `held != p.PriorHeld`, `run` `/usr/bin/apt-mark` with `unhold tailscale` (prior state not held) or `hold tailscale` (prior state held); then `setPinUnpinned(ctx, env.DB, p.ChangeID, env.ChangeID, env.Clock.Now())`; then `Made` with the post state `{"held": p.PriorHeld}`; return `Unpinned{Version: installed, Held: p.PriorHeld}`. A failed apt-mark leaves the effect `intended` for the engine to settle through `Observe`, and the pin active; the error is returned as `run` gives it. A crash between the apt-mark and the row update leaves the hold right and the pin active, and a rerun finishes without another apt-mark.
4. Verify, from the input alone (recovery calls it with the zero output): find the pin the change unpinned with one query on `env.DB.Read()`, `SELECT change_id, prior_held FROM tailscale_pins WHERE unpinned_by = ?` with `env.ChangeID`; none is a pass (the change had nothing to unpin); otherwise `t.held` must equal `prior_held`, and the error names the hold state found and wanted (`tailscale: the package is held after the unpin, want not held`).
5. Observe only reads. For `packageUnpinResource` it reports the state `{"held": <t.held>}` with `Matches` 1 and `Exclusive` true; any other resource is an error; a failing read of the holds is an error.
6. Inverse (the undo): refuse any resource other than `packageUnpinResource`. Run `apt-mark hold tailscale` only when `t.held` is false; only after that succeeds, find the pin with `unpinned_by = e.ChangeID` (the query of Verify) and, when one exists, call `setPinUnpinned(ctx, env.DB, <that pin>, "", env.Clock.Now())`, which makes it the active pin again. Return `core.InverseResult{}`. A failed apt-mark returns its error and leaves the pin marked unpinned.

Add no other file and do not touch `tailscale.go`; `TestManifestIsASkeleton` keeps passing.

- [ ] **Step 4: Run the tests and the task checks**

```bash
mkdir -p /root/.cache/w4-tmp/28 && export TMPDIR=/root/.cache/w4-tmp/28 GOTOOLCHAIN=local
timeout 300 go test -count=1 -run 'TestUp' -v ./internal/modules/tailscale/
timeout 600 go test -race -count=1 ./internal/modules/tailscale/...
timeout 600 go build ./... && timeout 300 go vet ./...
timeout 900 go test -count=1 ./...
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
rm -rf /root/.cache/w4-tmp/28 && go clean -testcache
```

Expected: every test of the task `--- PASS`, every package line of `go test ./...` `ok` or `?`, `go vet` and `go mod tidy -diff` print nothing, `golangci-lint` prints `0 issues.`. Record the real output.

- [ ] **Step 5: Commit**

```bash
git add internal/modules/tailscale/package_unpin.go internal/modules/tailscale/package_unpin_test.go
git commit -m "feat(tailscale): add package.unpin action"
```

### Task 29: tailscale: pin rollback, undo and recovery handler

**Files:**
- Create: `internal/modules/tailscale/package_pin_recover.go`
- Test: `internal/modules/tailscale/package_pin_recover_test.go`

**Interfaces:**
- Consumes: Contracts "Tailscale pin" (`packagePinHandler`, its `rollback`, the forward steps and the pin's effect; written by Task 24 in `package_pin.go`, which this task does not edit); "Tailscale internals" (`ts`, `pinRow`, `loadPin`, `addPinStep`, `ts.installedVersion`, `ts.held`, `debDir`, `lockKey`, the tables `tailscale_pins` and `tailscale_pin_steps`); "Tailscale wire shapes" (`Pinned`); "Tailscale test host" (`newTSHost`, `tsHost`, `tsRun`, `h.ran`, `h.reads`, `h.db`, `h.clock`, `h.units`, `thDpkgStatus`, `thDpkgPath`); `core.Undoer`, `core.Observer`, `core.Recoverer`, `core.RecoveryOutcome`, `core.InverseResult` (`internal/core/contract.go`); `platform.AptInstallDeb` (`internal/platform/exec_rules.go`).
- Produces: the methods `Observe`, `Inverse` and `Recover` of `packagePinHandler` (value receivers), so `core.DefineTask` in Task 24's `packagePin()` finds the `Undoer` and the `Recoverer` by type assertion and `Manifest()` needs no change. No new package-level identifier except what a helper needs, named with the prefix `packagePinRecover`.

**Items:** 21

**Depends on:** Task 24

The engine calls `Recover` only once the apply unit has ended (spec 1.3c), so `Recover` decides by the pin row's phase alone. The engine settles the pin's effect itself through `Observe` after `Recover` returns, so `Recover` journals nothing. The tests are module-private and use only the fakes of Contracts "Tailscale test host"; no test runs `apt-get`, `apt-mark` or `dpkg` on this host. The helpers `rbRunner`, `rbSetup`, `rbStaged`, `rbPin`, `rbAdd`, `rbDef`, `rbSteps` and `rbSame` are declared by this task's test file only. `rbRunner` stands for apt-mark and apt-get: it keeps the hold state in a boolean the test owns and, when the install of the staged package succeeds, rewrites dpkg's status file in the host fake to the prior version, because the host fake does not.

The pin rows these tests insert are built with `rbStaged`, the one place that decides the form of the two columns Contracts "Tailscale internals" leaves open: `staged_deb` holds the full path `debDir + "/tailscale_<version>_<arch>.deb"` that APT-5 takes as its argument, and `staged_sha256` the lower-case hexadecimal SHA-256 of that file. If Task 24's `rollback` reads another form, `rbStaged` is the only function to change.

- [ ] **Step 1: Write the failing test**

Create `internal/modules/tailscale/package_pin_recover_test.go` with exactly this content:

```go
package tailscale

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/platform"
)

const (
	rbAptGet       = "/usr/bin/apt-get"
	rbAptMark      = "/usr/bin/apt-mark"
	rbDpkg         = "/usr/bin/dpkg"
	rbResource     = "tailscale.package:tailscale"
	rbPriorVersion = "1.104.1"
	rbTarget       = "1.102.0"
)

// rbDeb is the content of the staged prior package.
var rbDeb = []byte("staged tailscale 1.104.1 package")

// rbStaged is the staged_deb and staged_sha256 a pin that staged rbDeb holds, and the bytes of that file.
func rbStaged() (path, sum string, data []byte) {
	h := sha256.Sum256(rbDeb)
	return debDir + "/tailscale_" + rbPriorVersion + "_amd64.deb", hex.EncodeToString(h[:]), rbDeb
}

// rbRunner runs vectors on the host and keeps the hold state of the tailscale package in *held; when the install of
// the staged package succeeds it leaves the prior version installed, as apt-get would.
type rbRunner struct {
	h    *tsHost
	held *bool
}

func (r rbRunner) Run(ctx context.Context, cmd core.Cmd) (core.CmdResult, error) {
	res, err := r.h.Run(ctx, cmd)
	if err != nil || res.ExitCode != 0 {
		return res, err
	}
	deb, _, _ := rbStaged()
	switch {
	case cmd.Path == rbAptMark && slices.Equal(cmd.Args, []string{"hold", "tailscale"}):
		*r.held = true
	case cmd.Path == rbAptMark && slices.Equal(cmd.Args, []string{"unhold", "tailscale"}):
		*r.held = false
	case cmd.Path == rbAptMark && slices.Equal(cmd.Args, []string{"showhold"}):
		if *r.held {
			res.Stdout = []byte("tailscale\n")
		}
	case cmd.Path == rbAptGet && slices.Equal(cmd.Args, platform.AptInstallDeb(deb)):
		r.h.reads[thDpkgPath] = thDpkgStatus(rbPriorVersion)
	}
	return res, nil
}

// rbSetup is a host on which the pin to rbTarget over rbPriorVersion has installed rbTarget, held or not, with the
// staged prior package on disk.
func rbSetup(t *testing.T, held bool) (*tsHost, ts, *bool) {
	t.Helper()
	h := newTSHost(t)
	deb, _, data := rbStaged()
	h.reads[thDpkgPath] = thDpkgStatus(rbTarget)
	h.reads[deb] = data
	return h, h.ts(), &held
}

func rbEnv(h *tsHost, held *bool, changeID string) core.Env {
	env := h.env(changeID)
	env.Exec = rbRunner{h: h, held: held}
	return env
}

// rbPin is the pin change id in phase, with its prior package staged.
func rbPin(id, phase string, at time.Time) pinRow {
	deb, sum, _ := rbStaged()
	return pinRow{
		ChangeID: id, Version: rbTarget, Arch: "amd64", PriorVersion: rbPriorVersion, PriorBackend: "Running",
		Phase: phase, StagedDeb: deb, StagedSHA256: sum, UpdatedAt: at,
	}
}

func rbAdd(t *testing.T, h *tsHost, p pinRow) {
	t.Helper()
	ctx := context.Background()
	if err := h.db.Write(ctx, func(tx *sql.Tx) error { return insertPin(ctx, tx, p) }); err != nil {
		t.Fatal(err)
	}
}

// rbDef is the pin action's handler; it fails the test when the undo or the recovery handler is missing.
func rbDef(t *testing.T, tt ts) core.ErasedHandler {
	t.Helper()
	h := tt.packagePin().Handler
	if h.Observe == nil || h.Inverse == nil || h.Recover == nil {
		t.Fatalf("handler has Observe %v, Inverse %v, Recover %v; want all three", h.Observe != nil, h.Inverse != nil, h.Recover != nil)
	}
	return h
}

// rbSteps lists "<step> <result>" of the pin's journal, in order.
func rbSteps(t *testing.T, h *tsHost, id string) []string {
	t.Helper()
	rows, err := h.db.Read().Query(`SELECT step, result FROM tailscale_pin_steps WHERE change_id = ? ORDER BY seq`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var step, result string
		if err := rows.Scan(&step, &result); err != nil {
			t.Fatal(err)
		}
		out = append(out, step+" "+result)
	}
	return out
}

func rbSame(t *testing.T, a, b []byte) bool {
	t.Helper()
	var va, vb any
	if err := json.Unmarshal(a, &va); err != nil {
		t.Fatalf("document %s: %v", a, err)
	}
	if err := json.Unmarshal(b, &vb); err != nil {
		t.Fatalf("document %s: %v", b, err)
	}
	return reflect.DeepEqual(va, vb)
}

// rbRolledBack fails the test unless the host is back at the prior version, not held (the pin's prior hold state),
// with the staged package installed through APT-5 and tailscaled restarted.
func rbRolledBack(t *testing.T, h *tsHost, tt ts, held *bool, wantConfigure bool) {
	t.Helper()
	deb, _, _ := rbStaged()
	if v, err := tt.installedVersion(context.Background(), h.env("")); err != nil || v != rbPriorVersion {
		t.Errorf("installed version %q, %v; want %s", v, err, rbPriorVersion)
	}
	if *held {
		t.Error("the package is held after the rollback, want the pin's prior state: not held")
	}
	install := append([]string{rbAptGet}, platform.AptInstallDeb(deb)...)
	if !slices.ContainsFunc(h.ran, func(v []string) bool { return slices.Equal(v, install) }) {
		t.Errorf("ran %v, want the install of the staged package %v", h.ran, install)
	}
	configure := slices.ContainsFunc(h.ran, func(v []string) bool { return slices.Equal(v, []string{rbDpkg, "--configure", "-a"}) })
	if configure != wantConfigure {
		t.Errorf("dpkg --configure -a ran %v, want %v", configure, wantConfigure)
	}
	if !slices.Contains(h.units.Restarts, tailscaledUnit) {
		t.Errorf("restarts %v, want %s", h.units.Restarts, tailscaledUnit)
	}
}

func TestRbDefinition(t *testing.T) {
	_, tt, _ := rbSetup(t, false)
	h := rbDef(t, tt)
	if h.Plan == nil || h.Task == nil || h.Verify == nil || h.Apply != nil {
		t.Error("handler lacks Plan, Task or Verify, or has an Apply")
	}
}

func TestRbObserve(t *testing.T) {
	ctx := context.Background()
	h, tt, held := rbSetup(t, true)
	def := rbDef(t, tt)
	obs, err := def.Observe(ctx, rbEnv(h, held, "c1"), core.Effect{Resource: rbResource})
	if err != nil || !rbSame(t, obs.Current.State, []byte(`{"version":"1.102.0","held":true}`)) || obs.Matches != 1 || !obs.Exclusive {
		t.Errorf("observation %+v (%v), want the installed version and the hold, one exclusive match", obs, err)
	}
	*held = false
	h.reads[thDpkgPath] = thDpkgStatus("")
	obs, err = def.Observe(ctx, rbEnv(h, held, "c1"), core.Effect{Resource: rbResource})
	if err != nil || !rbSame(t, obs.Current.State, []byte(`{"version":"","held":false}`)) {
		t.Errorf("observation %+v (%v), want an empty version when the package is not installed", obs, err)
	}
	if _, err := def.Observe(ctx, rbEnv(h, held, "c1"), core.Effect{Resource: "tailscale.prefs:node"}); err == nil {
		t.Error("observed another resource, want an error")
	}
	h.script(rbAptMark+" showhold", tsRun{res: core.CmdResult{ExitCode: 100}})
	if _, err := def.Observe(ctx, rbEnv(h, held, "c1"), core.Effect{Resource: rbResource}); err == nil {
		t.Error("observed with showhold failing, want an error")
	}
}

func TestRbRecoverWithoutARollback(t *testing.T) {
	tests := []struct {
		name  string
		phase string // "" for no pin row
		step  [2]string
		want  core.RecoveryOutcome
	}{
		{"no pin row", "", [2]string{}, core.RecoveryOutcome{State: core.RecoveryRolledBack}},
		{"recorded: nothing was changed", "recorded", [2]string{}, core.RecoveryOutcome{State: core.RecoveryRolledBack}},
		{"already rolled back", "rolled_back", [2]string{}, core.RecoveryOutcome{State: core.RecoveryRolledBack}},
		{"done", "done", [2]string{}, core.RecoveryOutcome{State: core.RecoveryCompleted, Output: json.RawMessage(`{"version":"1.102.0","priorVersion":"1.104.1","priorHeld":false}`)}},
		{"failed", "failed", [2]string{"rollback-install", "failed: sha256 mismatch"}, core.RecoveryOutcome{State: core.RecoveryNeedsRepair}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			h, tt, held := rbSetup(t, true)
			if tc.phase != "" {
				rbAdd(t, h, rbPin("c1", tc.phase, h.clock.Now()))
			}
			if tc.step[0] != "" {
				if err := addPinStep(ctx, h.db, "c1", tc.step[0], tc.step[1], h.clock.Now()); err != nil {
					t.Fatal(err)
				}
			}
			out, err := rbDef(t, tt).Recover(ctx, rbEnv(h, held, "c1"))
			if err != nil {
				t.Fatal(err)
			}
			if out.State != tc.want.State || (tc.want.Output != nil && !rbSame(t, out.Output, tc.want.Output)) {
				t.Errorf("outcome %+v, want %+v", out, tc.want)
			}
			if tc.want.State == core.RecoveryNeedsRepair && (!strings.Contains(out.Reason, "rollback-install") || !strings.Contains(out.Reason, "failed: sha256 mismatch")) {
				t.Errorf("reason %q, want the last step and its result", out.Reason)
			}
			if len(h.ran) != 0 || len(h.units.Restarts) != 0 {
				t.Errorf("recover ran %v and restarted %v, want nothing", h.ran, h.units.Restarts)
			}
		})
	}
}

func TestRbRecoverRollsAnInterruptedPinBack(t *testing.T) {
	tests := []struct {
		phase         string
		held          bool
		wantConfigure bool
	}{
		{"staged", false, false},
		{"installed", false, true},
		{"held", true, false},
		{"checking", true, false},
		{"rolling_back", false, false},
	}
	for _, tc := range tests {
		t.Run(tc.phase, func(t *testing.T) {
			ctx := context.Background()
			h, tt, held := rbSetup(t, tc.held)
			rbAdd(t, h, rbPin("c1", tc.phase, h.clock.Now()))
			out, err := rbDef(t, tt).Recover(ctx, rbEnv(h, held, "c1"))
			if err != nil || out.State != core.RecoveryRolledBack {
				t.Fatalf("outcome %+v, %v; want %s", out, err, core.RecoveryRolledBack)
			}
			rbRolledBack(t, h, tt, held, tc.wantConfigure)
			if p, ok, err := loadPin(ctx, h.db.Read(), "c1"); err != nil || !ok || p.Phase != "rolled_back" {
				t.Errorf("pin %+v, %v, %v; want phase rolled_back", p, ok, err)
			}
			if steps := rbSteps(t, h, "c1"); len(steps) == 0 {
				t.Error("the rollback journaled no step")
			}
		})
	}
}

func TestRbRecoverNeedsRepairWhenTheRollbackFails(t *testing.T) {
	ctx := context.Background()
	h, tt, held := rbSetup(t, true)
	p := rbPin("c1", "checking", h.clock.Now())
	p.StagedSHA256 = strings.Repeat("0", 64)
	rbAdd(t, h, p)
	out, err := rbDef(t, tt).Recover(ctx, rbEnv(h, held, "c1"))
	if err != nil {
		t.Fatal(err)
	}
	if out.State != core.RecoveryNeedsRepair || !strings.HasPrefix(out.Reason, "the rollback of the pin of 1.102.0 failed: ") {
		t.Errorf("outcome %+v, want %s with the rollback's error as the reason", out, core.RecoveryNeedsRepair)
	}
	deb, _, _ := rbStaged()
	if slices.ContainsFunc(h.ran, func(v []string) bool {
		return slices.Equal(v, append([]string{rbAptGet}, platform.AptInstallDeb(deb)...))
	}) {
		t.Errorf("ran %v, want no install of a package whose SHA-256 does not match", h.ran)
	}
	if got, _, _ := loadPin(ctx, h.db.Read(), "c1"); got.Phase != "failed" {
		t.Errorf("pin phase %q, want failed", got.Phase)
	}
}

func TestRbRecoverSettlesTheRowOfAPinWithNothingStaged(t *testing.T) {
	ctx := context.Background()
	h, tt, held := rbSetup(t, false)
	p := rbPin("c1", "unheld", h.clock.Now())
	p.StagedDeb, p.StagedSHA256 = "", ""
	rbAdd(t, h, p)
	out, err := rbDef(t, tt).Recover(ctx, rbEnv(h, held, "c1"))
	if err != nil {
		t.Fatal(err)
	}
	got, _, _ := loadPin(ctx, h.db.Read(), "c1")
	switch out.State {
	case core.RecoveryRolledBack:
		if got.Phase != "rolled_back" {
			t.Errorf("rolled back, but the pin is in phase %q", got.Phase)
		}
	case core.RecoveryNeedsRepair:
		if got.Phase != "failed" || out.Reason == "" {
			t.Errorf("needs repair with phase %q and reason %q, want phase failed and a reason", got.Phase, out.Reason)
		}
	default:
		t.Errorf("outcome %+v, want rolled_back or needs_repair", out)
	}
}

func TestRbInverse(t *testing.T) {
	ctx := context.Background()
	h, tt, held := rbSetup(t, true)
	rbAdd(t, h, rbPin("c1", "done", h.clock.Now()))
	def := rbDef(t, tt)
	res, err := def.Inverse(ctx, rbEnv(h, held, "c1"), core.Effect{ChangeID: "c1", Resource: rbResource, State: core.EffectUndoing})
	if err != nil || res != (core.InverseResult{}) {
		t.Fatalf("inverse = %+v, %v; want an empty result", res, err)
	}
	rbRolledBack(t, h, tt, held, false)
	if p, ok, err := loadPin(ctx, h.db.Read(), "c1"); err != nil || !ok || p.Phase != "rolled_back" {
		t.Errorf("pin %+v, %v, %v; want phase rolled_back", p, ok, err)
	}
	if _, ok, err := activePin(ctx, h.db.Read()); err != nil || ok {
		t.Errorf("active pin after the undo: found %v, %v; want none", ok, err)
	}
}

func TestRbInverseFailures(t *testing.T) {
	ctx := context.Background()
	h, tt, held := rbSetup(t, true)
	def := rbDef(t, tt)
	if _, err := def.Inverse(ctx, rbEnv(h, held, "c5"), core.Effect{ChangeID: "c5", Resource: rbResource}); err == nil {
		t.Error("inverse of a change with no pin row succeeded, want an error")
	}
	rbAdd(t, h, rbPin("c1", "done", h.clock.Now()))
	if _, err := def.Inverse(ctx, rbEnv(h, held, "c1"), core.Effect{ChangeID: "c1", Resource: "tailscale.prefs:node"}); err == nil {
		t.Error("inverse of another resource succeeded, want an error")
	}
	if len(h.ran) != 0 {
		t.Errorf("ran %v before any valid inverse, want nothing", h.ran)
	}
	p := rbPin("c2", "done", h.clock.Now().Add(time.Hour))
	p.StagedSHA256 = strings.Repeat("0", 64)
	rbAdd(t, h, p)
	if _, err := def.Inverse(ctx, rbEnv(h, held, "c2"), core.Effect{ChangeID: "c2", Resource: rbResource}); err == nil {
		t.Error("inverse with a staged package whose SHA-256 does not match succeeded, want the rollback's error")
	}
	if got, _, _ := loadPin(ctx, h.db.Read(), "c2"); got.Phase != "failed" {
		t.Errorf("pin phase %q after a failed rollback, want failed", got.Phase)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

```bash
mkdir -p /root/.cache/w4-tmp/29 && export TMPDIR=/root/.cache/w4-tmp/29 GOTOOLCHAIN=local
timeout 300 go test -count=1 -run 'TestRb' ./internal/modules/tailscale/
```

Expected: the package builds (Task 24's `packagePin()` exists) and every test of the task FAILS on `handler has Observe false, Inverse false, Recover false; want all three`. Quote the output.

- [ ] **Step 3: Implement `internal/modules/tailscale/package_pin_recover.go`**

Decisions this task embodies. The pin's rollback is one routine (`packagePinHandler.rollback`, Task 24) that the failed-check path, the undo and the recovery handler all call, so a pin that is rolled back by any of them ends in the same row phase (`rolled_back`, or `failed` with the first failing step) and the same journaled steps. This task adds no step of its own.

1. Methods with value receivers on `packagePinHandler`: `Observe`, `Inverse` and `Recover`. Add no other method to the type and do not edit `package_pin.go`; `DefineTask` in `packagePin()` finds the `Undoer` and the `Recoverer` by type assertion, and `Manifest()` is unchanged.
2. `Observe(ctx, env, e)` only reads. For the resource `tailscale.package:tailscale` it reports the state `{"version": <installedVersion>, "held": <held>}` (an empty version when the package is not installed) with `Matches` 1 and `Exclusive` true; any other resource is an error; a failing `installedVersion` or `held` is an error. The state has the members the pin's effect records (Contracts "Tailscale pin"), which is how the engine settles the effect after a crash.
3. `Inverse(ctx, env, e)` (the undo): refuse any resource other than `tailscale.package:tailscale` before anything is read or run; `loadPin(ctx, env.DB.Read(), e.ChangeID)`; no row is the error `tailscale: no pin is recorded for change <id>`; otherwise `h.rollback(ctx, env, p)` and return its error as it is; success returns `core.InverseResult{}`.
4. `Recover(ctx, env)` loads the pin with `loadPin(ctx, env.DB.Read(), env.ChangeID)`; a load error is returned as the error. By the row's phase, as Contracts "Tailscale pin" lists:
   - no row, or `recorded`: `RecoveryOutcome{State: core.RecoveryRolledBack}` with nothing run;
   - `rolled_back`: the same;
   - `done`: `RecoveryOutcome{State: core.RecoveryCompleted, Output: <JSON of Pinned{Version: p.Version, PriorVersion: p.PriorVersion, PriorHeld: p.PriorHeld}>}`;
   - `failed`: `RecoveryOutcome{State: core.RecoveryNeedsRepair, Reason: ...}` where the reason is `the pin of <version> failed; last step <step>: <result>` from the row of the highest `seq` in `tailscale_pin_steps` for the change (one query on `env.DB.Read()`), or `the pin of <version> failed` when it has none; nothing is run;
   - `unheld`, `staged`, `installed`, `held`, `checking` or `rolling_back`: `h.rollback(ctx, env, p)`; no error is `RecoveryOutcome{State: core.RecoveryRolledBack}`; an error is `RecoveryOutcome{State: core.RecoveryNeedsRepair, Reason: "the rollback of the pin of <version> failed: <error text>"}` returned with a nil error, because the failure is the outcome, not a crash of the handler.
   Any other phase text is an error naming it.

- [ ] **Step 4: Run the tests and the task checks**

```bash
mkdir -p /root/.cache/w4-tmp/29 && export TMPDIR=/root/.cache/w4-tmp/29 GOTOOLCHAIN=local
timeout 300 go test -count=1 -run 'TestRb' -v ./internal/modules/tailscale/
timeout 600 go test -race -count=1 ./internal/modules/tailscale/...
timeout 600 go build ./... && timeout 300 go vet ./...
timeout 900 go test -count=1 ./...
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
rm -rf /root/.cache/w4-tmp/29 && go clean -testcache
```

Expected: every test of the task `--- PASS`, every package line of `go test ./...` `ok` or `?`, `go vet` and `go mod tidy -diff` print nothing, `golangci-lint` prints `0 issues.`. Record the real output.

- [ ] **Step 5: Commit**

```bash
git add internal/modules/tailscale/package_pin_recover.go internal/modules/tailscale/package_pin_recover_test.go
git commit -m "feat(tailscale): add pin undo and recovery"
```

### Task 30: install: join the tailnet from a dpb2 bundle

**Files:**
- Modify: `internal/install/install.go` (`Config`, `Config.check`, `enroll`; new `TailnetJoiner`)
- Modify: `internal/install/fakes_test.go` (`testHost`, `newTestHost`, `config`; new `ijTailnet`, `ijToken`, `ijBundle`)
- Modify: `internal/install/install_test.go` (new tests)
- Modify: `cmd/darkpanel/system.go` (`runInstall`'s neighbours, `RunE`, `Long`; new `installTailnet`, `joinTailnet`)
- Modify: `cmd/darkpanel/system_test.go` (new tests)
- Modify: `internal/agent/daemon/enroll.go` (`Enroll`)
- Modify: `internal/agent/daemon/enroll_test.go` (new test)
- Modify: `install.sh.in`; regenerate `install.sh` with `make install-sh`
- Modify: `scripts/gen-install-sh/main_test.go` (`TestInstallWithoutATerminal` asserts a message this task changes; one new test)

Read, do not edit: `internal/transport/token.go` (`ParseBootstrap`, `Bundle`), `internal/modules/tailscale/bootstrap.go` (`Join`, `JoinDeps`), `internal/platform/files.go` (`NewFiles`, `FilesConfig`, `OwnedPaths`), `internal/platform/ownership.go`, `scripts/gen-install-sh/main.go`.

**Interfaces:**
- Consumes: Contracts "Bootstrap bundle" (`BundlePrefix`, `Bundle`, `ParseBootstrap`, Task 2); "Tailscale installer join" (`tailscale.Join`, `JoinDeps`, Task 26); "Installer join" (the contract this task writes); `platform.NewRunner`, `platform.ProductionRules`, `platform.NewFiles`, `platform.FilesConfig`, `platform.OwnedPaths`, `platform.NewOwnership`; `store.Open`; `core.SystemClock`; `errcode.EnrollTokenInvalid`.
- Produces: Contracts "Installer join" as written there: `install.TailnetJoiner`, `Config.Tailnet`, the bundle handling of the agent role's install, the wiring in `cmd/darkpanel/system.go`, `daemon.Enroll`'s refusal of a `dpb2.` bundle, and the prompts, usage and errors of `install.sh`. Beyond the Contracts, private to their packages: `Config.bootstrap`, `installTailnet`, `joinTailnet`.

**Items:** 24

**Depends on:** Task 2, Task 26

The bundle is a one-use bearer secret: nothing in this task prints it, the token inside it or the tailnet key, and a refused bundle's error carries only the parser's own detail. No test of this task runs `tailscale.Join`, `apt-get` or `tailscale`; the join is a fake in `internal/install` and a replaced `joinTailnet` in `cmd/darkpanel`. At run time `installTailnet` needs the tailscale module's manifest (its owned paths and its agent migrations), which Task 31 registers; Task 30 ships before it, so the tests stand on the seams.

`install.sh` itself interprets nothing in the value: it passes the file's content on unchanged, as it does for a token. Only its words change. `scripts/gen-install-sh/main_test.go` is in this task's files because `TestInstallWithoutATerminal` asserts the old prompt wording.

- [ ] **Step 1: Write the failing tests**

**`internal/install/fakes_test.go`.** Add `"encoding/base64"` and `"github.com/darkraise/darkpanel/internal/transport"` to the imports (`bytes`, `context`, `strings` and `sync` are already imported). In `testHost` add the field `tailnet *ijTailnet` after `agent`. In `newTestHost` build the agent first and give the host both:

```go
func newTestHost(t *testing.T) *testHost {
	t.Helper()
	l := Rooted(t.TempDir())
	users := &fakeUsers{users: map[string][2]int{}}
	agent := &fakeAgent{layout: l, check: CheckAccepted}
	return &testHost{
		layout:     l,
		systemd:    newFakeSystemd(),
		users:      users,
		runner:     &fakeRunner{users: users},
		controller: &fakeController{},
		agent:      agent,
		tailnet:    &ijTailnet{agent: agent},
		chowns:     &chowns{owners: map[string][2]int{}},
		clock:      coretest.NewFakeClock(installTime),
		out:        &bytes.Buffer{},
	}
}
```

In `config` add `Tailnet: h.tailnet,` after `Agent: h.agent,` on the returned `Config`. Append at the end of the file:

```go
// ijKey is the tailnet auth key the bundles of these tests carry.
const ijKey = "tskey-auth-kIjTest1CNTRL-0123456789abcdefghij"

// ijToken is a well-formed enrollment token whose address is a tailnet address.
func ijToken() transport.Token {
	return transport.Token{
		Address:       "100.64.0.9:7443",
		Fingerprint:   strings.Repeat("ab", 32),
		ControllerSAN: transport.ControllerURI("01J0IJCONTROLLER"),
		HostID:        "01J0IJHOST",
		Secret:        base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 16)),
	}
}

// ijBundle is the dpb2 bootstrap bundle of ijToken and the auth key key.
func ijBundle(key string) string {
	return transport.Bundle{Token: ijToken(), TailnetKey: key, ControllerTailnetAddr: "100.64.0.9:7443"}.String()
}

var _ TailnetJoiner = (*ijTailnet)(nil)

// ijTailnet is the tailnet joiner: it records the auth keys it is given and how many enrollments the agent had
// made when each join ran, and fails with err when that is set.
type ijTailnet struct {
	mu       sync.Mutex
	agent    *fakeAgent
	err      error
	keys     []string
	enrolled []int
}

func (j *ijTailnet) Join(_ context.Context, authKey string) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.keys = append(j.keys, authKey)
	j.agent.mu.Lock()
	j.enrolled = append(j.enrolled, len(j.agent.tokens))
	j.agent.mu.Unlock()
	return j.err
}
```

**`internal/install/install_test.go`.** Append these tests (no new imports: `context`, `errors`, `io/fs`, `os`, `reflect`, `slices`, `strings` and `testing` are already imported):

```go
func TestIjBundleJoinsBeforeEnrolling(t *testing.T) {
	h := newTestHost(t)
	cfg := h.config(RoleAgent)
	cfg.Token = ijBundle(ijKey)
	if err := Install(context.Background(), cfg); err != nil {
		t.Fatalf("Install: %v\n%s", err, h.out)
	}
	if !reflect.DeepEqual(h.tailnet.keys, []string{ijKey}) || !reflect.DeepEqual(h.tailnet.enrolled, []int{0}) {
		t.Errorf("joined with %v after %v enrollments, want the bundle's key once, before any enrollment", h.tailnet.keys, h.tailnet.enrolled)
	}
	if want := []string{ijToken().String()}; !reflect.DeepEqual(h.agent.tokens, want) {
		t.Errorf("enrolled with %v, want the plain token %v: the agent never sees the bundle", h.agent.tokens, want)
	}
	if !strings.Contains(h.out.String(), "enrollment: joined the tailnet and enrolled") {
		t.Errorf("output lacks the join and the enrollment:\n%s", h.out)
	}
	if strings.Contains(h.out.String(), ijKey) || strings.Contains(h.out.String(), "dpb2.") || strings.Contains(h.out.String(), ijToken().Secret) {
		t.Errorf("output carries a secret of the bundle:\n%s", h.out)
	}
	if got := h.systemd.recorded(); got[len(got)-1] != "Restart darkpanel-agent.service" {
		t.Errorf("the install ended with %q, want the agent started", got[len(got)-1])
	}
}

func TestIjBundleJoinFailureLeavesTheHostUnenrolled(t *testing.T) {
	h := newTestHost(t)
	h.tailnet.err = errors.New("the node did not get a tailnet address within 1m0s")
	cfg := h.config(RoleAgent)
	cfg.Token = ijBundle(ijKey)
	ctx := context.Background()
	err := Install(ctx, cfg)
	want := "install: enrollment: tailnet join failed: the node did not get a tailnet address within 1m0s; rerun with a fresh bundle"
	if err == nil || err.Error() != want {
		t.Fatalf("Install = %v, want %q", err, want)
	}
	if len(h.agent.tokens) != 0 {
		t.Errorf("the agent enrolled with %v after a failed join", h.agent.tokens)
	}
	if _, statErr := os.Lstat(h.layout.Paths.AgentTLSCurrent()); !errors.Is(statErr, fs.ErrNotExist) {
		t.Errorf("credentials exist after a failed join: %v", statErr)
	}
	if got := h.systemd.recorded(); slices.Contains(got, "Restart darkpanel-agent.service") {
		t.Errorf("systemd calls %v, want the agent left unstarted", got)
	}
	h.tailnet.err = nil
	if err := Install(ctx, cfg); err != nil {
		t.Fatalf("rerun with the join working: %v\n%s", err, h.out)
	}
	if want := []string{ijToken().String()}; !reflect.DeepEqual(h.agent.tokens, want) {
		t.Errorf("enrolled with %v after the rerun, want %v", h.agent.tokens, want)
	}
}

func TestIjBundleRefusals(t *testing.T) {
	tests := []struct {
		name   string
		token  string
		joiner bool
		want   string
	}{
		{"no joiner", ijBundle(ijKey), false, "Config.Tailnet is required"},
		{"a bundle of another version", "dpb3.e30", true, "bootstrap bundle version 3 is not supported by this darkpanel"},
		{"a malformed bundle", "dpb2.!!!", true, "the enrollment token is malformed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newTestHost(t)
			cfg := h.config(RoleAgent)
			cfg.Token = tt.token
			if !tt.joiner {
				cfg.Tailnet = nil
			}
			err := Install(context.Background(), cfg)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Install = %v, want an error containing %q", err, tt.want)
			}
			if strings.Contains(err.Error(), ijKey) {
				t.Errorf("the error %q carries the tailnet key", err)
			}
			if _, statErr := os.Lstat(h.layout.DataDir); !errors.Is(statErr, fs.ErrNotExist) {
				t.Error("a refused bundle changed the host")
			}
			if len(h.tailnet.keys) != 0 || len(h.agent.tokens) != 0 || len(h.systemd.recorded()) != 0 {
				t.Errorf("joined %v, enrolled %v, systemd %v; want nothing", h.tailnet.keys, h.agent.tokens, h.systemd.recorded())
			}
		})
	}
}

func TestIjSavedCredentialsSkipTheJoin(t *testing.T) {
	h := newTestHost(t)
	if err := h.agent.Enroll(context.Background(), "earlier"); err != nil {
		t.Fatal(err)
	}
	h.agent.tokens = nil
	cfg := h.config(RoleAgent)
	cfg.Token = ijBundle(ijKey)
	if err := Install(context.Background(), cfg); err != nil {
		t.Fatalf("Install: %v\n%s", err, h.out)
	}
	if len(h.tailnet.keys) != 0 || len(h.agent.tokens) != 0 {
		t.Errorf("joined %v and enrolled %v although the saved credentials are valid", h.tailnet.keys, h.agent.tokens)
	}
	if !strings.Contains(h.out.String(), "the enrollment token was not used") {
		t.Errorf("output lacks the note that the token was not used:\n%s", h.out)
	}
}

func TestIjPlainTokenIsNotJoined(t *testing.T) {
	h := newTestHost(t)
	cfg := h.config(RoleAgent)
	cfg.Token = "dpe1.plain"
	if err := Install(context.Background(), cfg); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if len(h.tailnet.keys) != 0 || !reflect.DeepEqual(h.agent.tokens, []string{"dpe1.plain"}) {
		t.Errorf("joined %v, enrolled with %v; want no join and the token unchanged", h.tailnet.keys, h.agent.tokens)
	}
	if !strings.Contains(h.out.String(), "enrollment: enrolled") {
		t.Errorf("output lacks the plain enrollment line:\n%s", h.out)
	}
}
```

**`internal/agent/daemon/enroll_test.go`.** Append (the imports `context`, `errors`, `os`, `path/filepath`, `testing`, `time`, `errcode` and `transport` are already there):

```go
func TestIjEnrollRefusesABootstrapBundle(t *testing.T) {
	ctrl := newTestController(t, time.Now().UTC().Truncate(time.Second))
	ctrl.addHost("H1", "pending")
	tok, err := transport.ParseToken(ctrl.token("H1"))
	if err != nil {
		t.Fatal(err)
	}
	bundle := transport.Bundle{Token: tok, TailnetKey: "tskey-auth-kEnrollTest1CNTRL-abcdefghij", ControllerTailnetAddr: "100.64.0.7:7443"}.String()
	dir := filepath.Join(t.TempDir(), "agent")
	tests := []struct{ name, input, detail string }{
		{"a version 2 bundle", " " + bundle + "\n",
			"this is a bootstrap bundle; install the agent with install.sh --role agent --token-file or darkpanel system install --role agent"},
		{"a bundle of another version", "dpb3.e30", "bootstrap bundle version 3 is not supported by this darkpanel"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Enroll(context.Background(), dir, tt.input)
			wantCode(t, err, errcode.EnrollTokenInvalid)
			var e *errcode.Error
			if !errors.As(err, &e) || e.Detail != tt.detail {
				t.Errorf("err = %v, want the detail %q", err, tt.detail)
			}
			if strings.Contains(err.Error(), "tskey-") {
				t.Errorf("the error %q carries the tailnet key", err)
			}
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Errorf("a refused bundle created %s: %v", dir, err)
			}
		})
	}
	if st := ctrl.hostState("H1"); st != "pending" {
		t.Errorf("host state %q, want pending: the token inside the bundle was not consumed", st)
	}
}
```

**`cmd/darkpanel/system_test.go`.** Add `"bytes"`, `"net/netip"` and `"github.com/darkraise/darkpanel/internal/modules/tailscale"` to the imports (the file already imports `context`, `errors`, `os`, `path/filepath`, `slices`, `strings`, `testing`, `cli`, `install` and `modules`), and append:

```go
func TestIjSystemInstallWiresTheTailnetJoin(t *testing.T) {
	const bundle = "dpb2.example-bundle"
	for _, role := range []string{"agent", "controller"} {
		t.Run(role, func(t *testing.T) {
			got, s := fakeInstall(t, 0, false)
			args := []string{"system", "install", "--role", role}
			if stdout, stderr, code := runStdin(strings.NewReader("  "+bundle+" \n"), args...); code != cli.ExitOK {
				t.Fatalf("exit %d: %s%s", code, stdout, stderr)
			}
			if len(*got) != 1 {
				t.Fatalf("the installer ran %d times, want once", len(*got))
			}
			cfg := (*got)[0]
			if role == "controller" {
				if cfg.Tailnet != nil {
					t.Errorf("the controller role got a tailnet joiner %#v", cfg.Tailnet)
				}
				return
			}
			j, ok := cfg.Tailnet.(installTailnet)
			if !ok || j.dbPath != s.layout.Paths.AgentDB() || j.out != cfg.Out || len(j.modules) != len(modules.All()) {
				t.Errorf("Tailnet = %#v, want the installed agent.db, the installer's output and the compiled-in manifests", cfg.Tailnet)
			}
			if cfg.Token != bundle {
				t.Errorf("Token = %q, want the bundle passed on unchanged", cfg.Token)
			}
		})
	}
}

func TestIjInstallTailnetJoinBuildsTheHostDependencies(t *testing.T) {
	ctx := context.Background()
	s := fakeAgent(t, 0)
	if err := os.MkdirAll(s.layout.Paths.AgentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	var (
		gotDeps tailscale.JoinDeps
		gotKey  string
		pingErr error
		calls   int
	)
	old := joinTailnet
	t.Cleanup(func() { joinTailnet = old })
	joinTailnet = func(ctx context.Context, d tailscale.JoinDeps, authKey string) ([]netip.Addr, error) {
		calls++
		gotDeps, gotKey = d, authKey
		pingErr = d.DB.Read().PingContext(ctx)
		return []netip.Addr{netip.MustParseAddr("100.64.0.7")}, nil
	}
	var out bytes.Buffer
	j := installTailnet{dbPath: s.layout.Paths.AgentDB(), modules: manifests(modules.All()), out: &out}
	if err := j.Join(ctx, "tskey-auth-k1"); err != nil {
		t.Fatalf("Join: %v", err)
	}
	if calls != 1 || gotKey != "tskey-auth-k1" {
		t.Errorf("tailscale.Join ran %d times with key %q, want once with the key passed on", calls, gotKey)
	}
	if gotDeps.Exec == nil || gotDeps.Files == nil || gotDeps.Clock == nil || gotDeps.Out != io.Writer(&out) || gotDeps.Socket != "" {
		t.Errorf("deps %+v, want the production runner, a file writer, the system clock, the installer's output and the default socket", gotDeps)
	}
	if gotDeps.DB == nil || gotDeps.DB.Path() != s.layout.Paths.AgentDB() || pingErr != nil {
		t.Errorf("deps database %v (ping %v), want agent.db open during the join", gotDeps.DB, pingErr)
	}
	if err := gotDeps.DB.Read().PingContext(ctx); err == nil {
		t.Error("agent.db is still open after Join returned")
	}

	boom := errors.New("boom")
	joinTailnet = func(context.Context, tailscale.JoinDeps, string) ([]netip.Addr, error) { return nil, boom }
	if err := j.Join(ctx, "tskey-auth-k1"); !errors.Is(err, boom) {
		t.Errorf("Join = %v, want the failure of tailscale.Join unchanged", err)
	}

	calls = 0
	joinTailnet = func(context.Context, tailscale.JoinDeps, string) ([]netip.Addr, error) { calls++; return nil, nil }
	j.dbPath = filepath.Join(t.TempDir(), "missing", "agent.db")
	if err := j.Join(ctx, "tskey-auth-k1"); err == nil || calls != 0 {
		t.Errorf("Join without a database = %v after %d joins, want an error and no join", err, calls)
	}
}
```

`io` is already imported in `system_test.go`; nothing else changes there.

**`scripts/gen-install-sh/main_test.go`.** In `TestInstallWithoutATerminal` replace the expected text with `"no terminal to read the enrollment token or bootstrap bundle from; pass --token-file"`, and append:

```go
func TestIjInstallNamesTheBootstrapBundle(t *testing.T) {
	f := newFixture(t)
	bundle := "dpb2." + strings.Repeat("eyJ2IjoyfQ", 60)
	if out, err := f.run("--role", "agent", "--token-file", f.tokenFile(t, bundle+"\n"), "--base-url", f.srv.URL); err != nil {
		t.Fatalf("install.sh: %v\n%s", err, out)
	}
	if got := readFile(t, f.logBase+".stdin"); got != bundle+"\n" {
		t.Errorf("darkpanel read %q on standard input, want the bundle passed on unchanged", got)
	}
	writeFile(t, filepath.Join(f.root, "etc", "darkpanel", "agent", "current", "agent.crt"), "cert\n")
	out, err := f.run("--role", "agent")
	if err != nil {
		t.Fatalf("install.sh with saved credentials: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "installing without an enrollment token or bootstrap bundle") {
		t.Errorf("output %q does not name the bundle next to the token", out)
	}
	out, err = f.run()
	if err == nil || !strings.Contains(string(out), "enrollment token or bootstrap bundle") {
		t.Errorf("usage: err %v, output %q; want a usage line that names the bundle", err, out)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
mkdir -p /root/.cache/w4-tmp/30 && export TMPDIR=/root/.cache/w4-tmp/30 GOTOOLCHAIN=local
timeout 600 go test -count=1 -run 'TestIj|TestInstallWithoutATerminal' ./internal/install/ ./cmd/darkpanel/ ./internal/agent/daemon/ ./scripts/gen-install-sh/
```

Expected: `internal/install` fails to build with `undefined: TailnetJoiner` (and `h.tailnet undefined`), `cmd/darkpanel` with `undefined: installTailnet` and `undefined: joinTailnet`, `internal/agent/daemon` fails `TestIjEnrollRefusesABootstrapBundle` with the detail "the enrollment token is malformed" instead of the bundle text, and `scripts/gen-install-sh` fails `TestIjInstallNamesTheBootstrapBundle` and `TestInstallWithoutATerminal` on the old wording. Quote the output.

- [ ] **Step 3: Implement**

Decisions this task embodies. The installer joins the tailnet only when it is about to enroll with a bundle, so a rerun on an enrolled host never consumes the one-use key a second time. Any value starting with `dpb` is a bundle of some version and is parsed at once, before the host changes, so a bundle of another version or a malformed one costs nothing and names its reason; every other value reaches the agent unchanged, as before, so the existing plain-token flow and its tests are untouched. The agent only ever enrolls with the plain token inside the bundle, whose address is the controller's tailnet address (Contracts "Host create join").

1. `internal/install/install.go`:
   - Declare `TailnetJoiner` exactly as Contracts "Installer join" gives it, with its doc comment, and add `Tailnet TailnetJoiner` to `Config` after `Agent`, documented as required only when the token is a bootstrap bundle. Update the doc of `Token` to "the agent role's enrollment token or bootstrap bundle from standard input, trimmed".
   - Add `func (c Config) bootstrap() (*transport.Bundle, error)`: `nil, nil` unless `c.Role == RoleAgent` and `c.Token` starts with `dpb`; otherwise `transport.ParseBootstrap(c.Token)`, whose error is returned as `install: bootstrap bundle: <error>` (wrapped with `%w`) and whose bundle is returned. A comment says why the prefix test is `dpb` and not `BundlePrefix`: a bundle of another version must reach the parser to be refused by name.
   - `Config.check` calls `bootstrap` after its existing checks (the controller role's refusal of a token stays first); a bundle with `c.Tailnet == nil` is the error `install: Config.Tailnet is required when the token is a bootstrap bundle`.
   - `enroll`: after the existing `switch` that decides which token to use and refuses a missing one, and before `Agent.Enroll`, call `in.cfg.bootstrap()` (return its error, which `check` already ruled out); for a bundle call `in.cfg.Tailnet.Join(ctx, bundle.TailnetKey)`, returning `fmt.Errorf("tailnet join failed: %w; rerun with a fresh bundle", err)` on failure, and then set `token` to `bundle.Token.String()`. The returned line is `joined the tailnet and enrolled` after a join and `enrolled` otherwise. The other branches of `enroll` (valid saved credentials, controller unreachable) are unchanged and never join. The refusal text for `errcode.EnrollTokenInvalid` stays.
2. `cmd/darkpanel/system.go`:
   - Next to `runInstall` add `var joinTailnet = tailscale.Join`, with a comment that tests replace it.
   - Add `type installTailnet struct { dbPath string; modules []core.Manifest; out io.Writer }` implementing `install.TailnetJoiner`. `Join(ctx, authKey)` opens the agent database with `store.Open(ctx, j.dbPath)` (an open failure is returned wrapped as `open agent.db: %w`, and `joinTailnet` is not called), builds `tailscale.JoinDeps{Exec: platform.NewRunner(platform.ProductionRules()), Files: platform.NewFiles(platform.FilesConfig{Owned: platform.OwnedPaths(j.modules), Ownership: platform.NewOwnership(db), Clock: core.SystemClock{}}), DB: db, Clock: core.SystemClock{}, Out: j.out}` (`Socket` left empty: the default socket), calls `joinTailnet(ctx, deps, authKey)` and discards the addresses, closes the database after the call, and returns the join's error unchanged (joined with a close error through `errors.Join`).
   - In `RunE`, for `role == install.RoleAgent` set `cfg.Tailnet = installTailnet{dbPath: l.Paths.AgentDB(), modules: cfg.Modules, out: cmd.OutOrStdout()}`; the controller role gets none. In `Long`, say the agent role's value is an enrollment token or a bootstrap bundle.
3. `internal/agent/daemon/enroll.go`, `Enroll`: replace the first `transport.ParseToken(token)` with `transport.ParseBootstrap(token)`; return its error as it is; a non-nil bundle returns `errcode.New(errcode.EnrollTokenInvalid, "this is a bootstrap bundle; install the agent with install.sh --role agent --token-file or darkpanel system install --role agent")`; otherwise continue with the token it returned. This happens before the key is generated, so a refused bundle creates nothing and consumes nothing. Update the doc comment of `Enroll` to say it refuses a bundle.
4. `install.sh.in`: wherever the script names the value an agent installs with, say "enrollment token or bootstrap bundle" and change nothing else about how it is read or passed. That is: the usage comment (describe `PATH` as holding an enrollment token or a bootstrap bundle from `darkpanel host create --tailnet-join`); the comments of `read_token_file` and `read_token`; the prompt `Enrollment token or bootstrap bundle: `; the errors "no terminal to read the enrollment token or bootstrap bundle from; pass --token-file" and "no enrollment token or bootstrap bundle entered"; the `usage` message (append that, with `--role agent`, the token file holds an enrollment token or bootstrap bundle); and "Saved agent credentials found; installing without an enrollment token or bootstrap bundle." Then regenerate the script and keep it shell-clean:

   ```bash
   make install-sh
   git diff --stat install.sh install.sh.in
   shellcheck install.sh
   ```

   `git diff --stat` shows the same changed lines in both files and no change to the `KEYS` and `DEFAULT_BASE_URL` lines.
5. `scripts/gen-install-sh/main_test.go` holds the two edits of Step 1; edit nothing else there.

Add no other file. Do not run `tailscale.Join`, `apt-get`, `tailscale` or `systemctl` on this host.

- [ ] **Step 4: Run the tests and the task checks**

```bash
mkdir -p /root/.cache/w4-tmp/30 && export TMPDIR=/root/.cache/w4-tmp/30 GOTOOLCHAIN=local
timeout 600 go test -count=1 -run 'TestIj|TestInstallWithoutATerminal' -v ./internal/install/ ./cmd/darkpanel/ ./internal/agent/daemon/ ./scripts/gen-install-sh/
timeout 900 go test -race -count=1 ./internal/install/... ./cmd/darkpanel/... ./internal/agent/daemon/... ./scripts/gen-install-sh/...
shellcheck install.sh
timeout 600 go build ./... && timeout 300 go vet ./...
timeout 900 go test -count=1 ./...
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
rm -rf /root/.cache/w4-tmp/30 && go clean -testcache
```

Expected: every test of the task `--- PASS`, `TestCommittedScriptIsCurrent` still passes (the generated script equals the template rendered with the trusted keys), `shellcheck` prints nothing, every package line of `go test ./...` `ok` or `?`, `go vet` and `go mod tidy -diff` print nothing, `golangci-lint` prints `0 issues.`. Record the real output. This task touches `internal/install/` and `install.sh.in`, so gate 7 (harness) applies once the branch is pushed (Global Constraints).

- [ ] **Step 5: Commit**

```bash
git add internal/install/install.go internal/install/install_test.go internal/install/fakes_test.go \
  cmd/darkpanel/system.go cmd/darkpanel/system_test.go internal/agent/daemon/enroll.go internal/agent/daemon/enroll_test.go \
  install.sh.in install.sh scripts/gen-install-sh/main_test.go
git commit -m "feat(install): join the tailnet from a bundle"
```

### Task 31: tailscale: register actions and resources

**Files:**
- Modify: `internal/modules/tailscale/tailscale.go` (`Manifest`; new `actions` and `resources` methods on `ts`)
- Modify: `internal/modules/tailscale/tailscale_test.go` (delete `TestManifestIsASkeleton`)
- Modify: `internal/modules/tailscale/wire.go` (only the `jsonschema:"nullable"` tags of Step 4, and only where an earlier task did not already add them)
- Create: `internal/modules/tailscale/register_test.go`
- Create (generated by `make schemas`): `schemas/tailscale.cert.get/v1.json`, `schemas/tailscale.latency/v1.json`, `schemas/tailscale.node.login/v1.json`, `schemas/tailscale.node.logout/v1.json`, `schemas/tailscale.package.install/v1.json`, `schemas/tailscale.package.pin/v1.json`, `schemas/tailscale.package.unpin/v1.json`, `schemas/tailscale.package.update/v1.json`, `schemas/tailscale.prefs.repair/v1.json`, `schemas/tailscale.prefs.set/v1.json`, `schemas/tailscale.status/v1.json`

**Interfaces:**
- Consumes: Contracts "Tailscale internals" (`ts`, `preflight`, `ownedPaths`, `externalUnits`, `agentMigrations`, the `ID` constant that `tailscale.go` already declares); Contracts "Tailscale wire shapes" (the action specs table and the wire types); Contracts "Tailscale test host" (`newTSHost`, `(*tsHost).ts`); and Contracts "Tailscale constructors" (the nine action constructors and two resource constructors of Tasks 14-16, 20-24, 27 and 28, by the names that table gives). Task 25's `core.Uninstaller` is on `module` and needs no wiring here.
- Produces: `Module().Manifest()` declaring the nine actions of the table, the two resources `tailscale.status` and `tailscale.latency`, the OS preflight, the owned paths, the external units and the agent migrations; the eleven schema snapshots. Task 32 builds a registry from this module, and Tasks 33-35 call these actions through the controller API.

**Depends on:** Task 14, Task 15, Task 16, Task 20, Task 21, Task 22, Task 23, Task 24, Task 25, Task 27, Task 28, Task 29

- [ ] **Step 1: Write the failing test**

Delete `TestManifestIsASkeleton` from `internal/modules/tailscale/tailscale_test.go` (it asserts the empty manifest this task replaces), together with any import it alone used; delete the file if nothing else is left in it. Then create `internal/modules/tailscale/register_test.go`:

```go
package tailscale

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"slices"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/schema"
)

type regAction struct {
	name       string
	class      core.Class
	task       bool
	apply      time.Duration // 0 keeps the 30 s default
	available  bool          // declares its own availability check
	guarded    bool          // declares the tailscale.link guard
	undoer     bool
	recoverer  bool
	converges  bool
	repairedBy string
}

var regActions = []regAction{
	{name: "tailscale.prefs.set", class: core.ClassRevertible, available: true, guarded: true, repairedBy: "tailscale.prefs.repair"},
	{name: "tailscale.prefs.repair", class: core.ClassRevertible, available: true, guarded: true, repairedBy: "tailscale.prefs.repair"},
	{name: "tailscale.node.login", class: core.ClassCompensable, available: true, guarded: true, undoer: true},
	{name: "tailscale.node.logout", class: core.ClassIrreversible, available: true, guarded: true},
	{name: "tailscale.package.install", class: core.ClassCompensable, task: true, apply: 15 * time.Minute, undoer: true},
	{name: "tailscale.package.update", class: core.ClassIrreversible, task: true, apply: 15 * time.Minute, available: true},
	{name: "tailscale.package.pin", class: core.ClassCompensable, task: true, apply: 20 * time.Minute, available: true, undoer: true, recoverer: true},
	{name: "tailscale.package.unpin", class: core.ClassCompensable, task: true, apply: 5 * time.Minute, available: true, undoer: true, converges: true},
	{name: "tailscale.cert.get", class: core.ClassRead},
}

func regModule(t *testing.T) module {
	t.Helper()
	return module{t: newTSHost(t).ts()}
}

// TestRegManifestDeclaresTheActions: the module declares section 5's tailscale
// rows, all version 1 and agent-side, each mutation under the tailscale lock
// and the read under none, with the class, handlers, guard and deadlines of
// the action table.
func TestRegManifestDeclaresTheActions(t *testing.T) {
	man := regModule(t).Manifest()
	if man.ID != ID || man.Version != 1 || !man.DefaultEnabled {
		t.Errorf("manifest %q v%d default-enabled %v, want tailscale v1 enabled by default", man.ID, man.Version, man.DefaultEnabled)
	}
	got := map[string]core.ActionDef{}
	for _, a := range man.Actions {
		if _, dup := got[a.Spec.Name]; dup {
			t.Errorf("%s is declared twice", a.Spec.Name)
		}
		got[a.Spec.Name] = a
	}
	for _, want := range regActions {
		a, ok := got[want.name]
		if !ok {
			t.Errorf("%s is not declared", want.name)
			continue
		}
		delete(got, want.name)
		s := a.Spec
		wantTimeout := want.apply
		if wantTimeout == 0 {
			wantTimeout = 30 * time.Second
		}
		timeout := s.ApplyTimeout
		if timeout == 0 {
			timeout = 30 * time.Second
		}
		wantLocks := []string{"tailscale"}
		if want.class == core.ClassRead {
			wantLocks = nil
		}
		internal := want.name == "tailscale.cert.get"
		if s.Version != 1 || s.Class != want.class || s.Side != core.SideAgent || !slices.Equal(s.LockKeys, wantLocks) ||
			s.MinScope != "" || s.LongRunning != want.task || s.Task != want.task || timeout != wantTimeout ||
			s.Internal != internal || s.Reveal || s.Converges != want.converges || s.RepairedBy != want.repairedBy ||
			s.CompensatedBy != "" || s.ForwardOnly {
			t.Errorf("%s: spec %+v; want class %s on the agent, locks %v, task %v, timeout %s, internal %v, converges %v, repaired by %q",
				want.name, s, want.class, wantLocks, want.task, wantTimeout, internal, want.converges, want.repairedBy)
		}
		// cert.get answers tailscale.unavailable from its handler, so the table does not pin its check.
		if want.name != "tailscale.cert.get" && (s.Available != nil) != want.available {
			t.Errorf("%s: availability check %v, want %v", want.name, s.Available != nil, want.available)
		}
		wantGuards := []core.GuardSpec(nil)
		if want.guarded {
			wantGuards = []core.GuardSpec{{ID: "tailscale.link", Overridable: true}}
		}
		if !slices.Equal(s.Guards, wantGuards) {
			t.Errorf("%s: guards %v, want %v", want.name, s.Guards, wantGuards)
		}
		if (a.Handler.Inverse != nil) != want.undoer || (want.undoer && a.Handler.Observe == nil) {
			t.Errorf("%s: undo handler %v, observer %v, want an undo handler %v with an observer", want.name, a.Handler.Inverse != nil, a.Handler.Observe != nil, want.undoer)
		}
		if (a.Handler.Recover != nil) != want.recoverer {
			t.Errorf("%s: recovery handler %v, want %v", want.name, a.Handler.Recover != nil, want.recoverer)
		}
		if internal != (a.Handler.Reveal != nil) {
			t.Errorf("%s: reveal handler %v, want %v", want.name, a.Handler.Reveal != nil, internal)
		}
		if _, err := schema.ActionSnapshot(a); err != nil {
			t.Errorf("%s: %v", a.Ref(), err)
		}
	}
	for name := range got {
		t.Errorf("%s is declared but is not in the action table", name)
	}
}

// TestRegManifestDeclaresTheRest: the resources, the OS preflight, the owned
// paths, the units the module drives and the agent migrations.
func TestRegManifestDeclaresTheRest(t *testing.T) {
	man := regModule(t).Manifest()

	var resources []string
	for _, r := range man.Resources {
		resources = append(resources, r.Name)
		if _, err := schema.ResourceSnapshot(r); err != nil {
			t.Errorf("%s: %v", r.Name, err)
		}
	}
	slices.Sort(resources)
	if want := []string{"tailscale.latency", "tailscale.status"}; !slices.Equal(resources, want) {
		t.Errorf("resources %v, want %v", resources, want)
	}

	if len(man.Preflight) == 0 {
		t.Error("the module declares no preflight check, want the OS check")
	}

	owned := map[string]core.OwnedPath{}
	for _, p := range man.OwnedPaths {
		owned[p.Path] = p
	}
	wantOwned := []core.OwnedPath{
		{Path: "/usr/share/keyrings/tailscale-archive-keyring.gpg", Kind: core.OwnedFile},
		{Path: "/etc/apt/sources.list.d/tailscale.list", Kind: core.OwnedFile},
		{Path: "/var/lib/darkpanel/agent/tailscale-debs", Kind: core.OwnedManagedDir, Pattern: `^tailscale_[0-9][0-9A-Za-z.+~-]{0,63}_(amd64|arm64)\.deb$`},
	}
	if len(man.OwnedPaths) != len(wantOwned) {
		t.Errorf("owned paths %+v, want %d", man.OwnedPaths, len(wantOwned))
	}
	for _, w := range wantOwned {
		if g := owned[w.Path]; g.Kind != w.Kind || g.Pattern != w.Pattern {
			t.Errorf("owned path %s = %+v, want kind %s with pattern %q", w.Path, g, w.Kind, w.Pattern)
		}
	}

	units := map[string][]string{}
	for _, u := range man.ExternalUnits {
		units[u.Name] = slices.Sorted(slices.Values(u.Ops))
	}
	wantUnits := map[string][]string{
		"tailscaled.service":           {core.UnitOpRestart, core.UnitOpState},
		"darkpanel-controller.service": {core.UnitOpState},
	}
	if len(units) != len(wantUnits) {
		t.Errorf("external units %v, want %v", units, wantUnits)
	}
	for name, ops := range wantUnits {
		if !slices.Equal(units[name], ops) {
			t.Errorf("external unit %s operations %v, want %v", name, units[name], ops)
		}
	}

	migrations, ok := man.Migrations[core.SideAgent]
	if !ok || len(man.Migrations) != 1 {
		t.Fatalf("migrations %v, want the agent side only", man.Migrations)
	}
	if _, err := fs.Stat(migrations, "0001_tailscale.sql"); err != nil {
		t.Errorf("agent migrations: %v", err)
	}
}

// TestRegInputs: each action is registered with its own input type, so its
// schema accepts the input the action takes and names the field of the one
// it refuses.
func TestRegInputs(t *testing.T) {
	defs := map[string]core.ActionDef{}
	for _, a := range regModule(t).Manifest().Actions {
		defs[a.Spec.Name] = a
	}
	v := schema.NewValidator()
	tests := []struct {
		action, input string
		field         string // "" when the input is accepted
	}{
		{"tailscale.prefs.set", `{"wantRunning":true}`, ""},
		{"tailscale.prefs.set", `{"acceptRoutes":false,"shieldsUp":true,"runSSH":false,"exitNode":"","advertiseExitNode":true}`, ""},
		{"tailscale.prefs.set", `{"advertiseRoutes":["10.0.0.0/24"],"overrides":["tailscale.link"]}`, ""},
		{"tailscale.prefs.set", `{"wantRunning":"yes"}`, "/wantRunning"},
		{"tailscale.prefs.set", `{"advertiseRoutes":"10.0.0.0/24"}`, "/advertiseRoutes"},
		{"tailscale.prefs.set", `{"unknown":1}`, "/unknown"},
		{"tailscale.prefs.repair", `{}`, ""},
		{"tailscale.prefs.repair", `{"outcome":"keep"}`, ""},
		{"tailscale.prefs.repair", `{"outcome":5}`, "/outcome"},
		{"tailscale.node.login", `{"authKey":"tskey-auth-x"}`, ""},
		{"tailscale.node.login", `{"authKey":"k","hostname":"h","advertiseTags":["tag:a"],"relogin":true}`, ""},
		{"tailscale.node.login", `{}`, "/authKey"},
		{"tailscale.node.login", `{"authKey":"k","advertiseTags":"tag:a"}`, "/advertiseTags"},
		{"tailscale.node.logout", `{}`, ""},
		{"tailscale.node.logout", `{"x":1}`, "/x"},
		{"tailscale.package.install", `{}`, ""},
		{"tailscale.package.install", `{"x":1}`, "/x"},
		{"tailscale.package.update", `{}`, ""},
		{"tailscale.package.update", `{"x":1}`, "/x"},
		{"tailscale.package.pin", `{"version":"1.104.1"}`, ""},
		{"tailscale.package.pin", `{}`, "/version"},
		{"tailscale.package.pin", `{"version":1}`, "/version"},
		{"tailscale.package.unpin", `{}`, ""},
		{"tailscale.package.unpin", `{"x":1}`, "/x"},
		{"tailscale.cert.get", `{}`, ""},
		{"tailscale.cert.get", `{"x":1}`, "/x"},
	}
	for _, tt := range tests {
		def, ok := defs[tt.action]
		if !ok {
			t.Errorf("%s is not declared", tt.action)
			continue
		}
		err := v.ValidateInput(def, json.RawMessage(tt.input))
		if tt.field == "" {
			if err != nil {
				t.Errorf("%s %s: %v, want it accepted", tt.action, tt.input, err)
			}
			continue
		}
		var coded *errcode.Error
		if !errors.As(err, &coded) || coded.Code != errcode.InvalidInput ||
			!slices.ContainsFunc(coded.Fields, func(f errcode.FieldError) bool { return f.Path == tt.field }) {
			t.Errorf("%s %s: %v, want %s naming %s", tt.action, tt.input, err, errcode.InvalidInput, tt.field)
		}
	}
}

// TestRegSecretsAreRedacted: the auth key of node.login and the private key of
// cert.get carry the secret tag, so no log, diff or stored output shows them.
func TestRegSecretsAreRedacted(t *testing.T) {
	defs := map[string]core.ActionDef{}
	for _, a := range regModule(t).Manifest().Actions {
		defs[a.Spec.Name] = a
	}
	login, ok := defs["tailscale.node.login"]
	if !ok {
		t.Fatal("tailscale.node.login is not declared")
	}
	cert, ok := defs["tailscale.cert.get"]
	if !ok {
		t.Fatal("tailscale.cert.get is not declared")
	}
	in := schema.Redact(login.InputType, json.RawMessage(`{"authKey":"tskey-auth-SECRET","hostname":"web1"}`))
	if bytes.Contains(in, []byte("SECRET")) || !bytes.Contains(in, []byte("web1")) {
		t.Errorf("redacted login input %s, want the auth key hidden and the hostname kept", in)
	}
	out := schema.Redact(cert.OutputType, json.RawMessage(`{"name":"web1.example.ts.net","certPem":"CERT","keyPem":"PRIVATE","notAfter":"2026-12-01T00:00:00Z"}`))
	if bytes.Contains(out, []byte("PRIVATE")) || !bytes.Contains(out, []byte("CERT")) {
		t.Errorf("redacted certificate output %s, want the private key hidden and the certificate kept", out)
	}
}

func TestRegRegistryAcceptsTheModule(t *testing.T) {
	for _, m := range []core.Module{regModule(t), Module()} {
		reg, err := core.NewRegistry(m)
		if err != nil {
			t.Fatal(err)
		}
		for _, a := range regActions {
			if _, ok := reg.Action(core.ActionRef{Name: a.name, Version: 1}); !ok {
				t.Errorf("the registry has no %s", a.name)
			}
		}
		for _, r := range []string{"tailscale.status", "tailscale.latency"} {
			if _, ok := reg.Resource(r); !ok {
				t.Errorf("the registry has no %s resource", r)
			}
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `mkdir -p /root/.cache/w4-tmp/31 && export TMPDIR=/root/.cache/w4-tmp/31 GOTOOLCHAIN=local && timeout 300 go test -count=1 -run 'TestReg' ./internal/modules/tailscale/`
Expected: FAIL. `TestRegManifestDeclaresTheActions` prints `tailscale.prefs.set is not declared` and eight more lines of that form; `TestRegManifestDeclaresTheRest` prints `resources [], want [tailscale.latency tailscale.status]`, `the module declares no preflight check, want the OS check` and the owned path, unit and migration lines; `TestRegInputs`, `TestRegSecretsAreRedacted` and `TestRegRegistryAcceptsTheModule` fail on the same missing declarations.

- [ ] **Step 3: Implement**

1. The constructors are those of Contracts "Tailscale constructors": `prefsSet`, `prefsRepair`, `nodeLogin`, `nodeLogout`, `packageInstall`, `packageUpdate`, `packagePin`, `packageUnpin`, `certGet`, `statusResource` and `latencyResource`, all methods on `ts`. If one is missing on the base, an earlier task is incomplete: stop and report which, and do not write it here. Do not edit any constructor's file; if the test shows a spec field that differs from the table of `regActions`, report the action and the field, because that constructor belongs to an earlier task.
2. In `tailscale.go` add `func (t ts) actions() []core.ActionDef`, returning the nine constructors' results in the order of `regActions`, and `func (t ts) resources() []core.ResourceDef`, returning the status resource then the latency resource, the way the docker module's `actions` and `resources` methods are written.
3. Change `Manifest()` to return `Actions: m.t.actions()`, `Resources: m.t.resources()`, `Preflight: m.t.preflight()`, `OwnedPaths: ownedPaths()`, `ExternalUnits: externalUnits()` and `Migrations: map[core.Side]fs.FS{core.SideAgent: agentMigrations()}`, keeping `ID`, `Version: 1` and `DefaultEnabled: true`. Rewrite its doc comment: it no longer says the manifest declares nothing; it says what the module declares and that preflight (the OS check) decides whether the module is available on a host.
4. The constructors only build declarations. Registering the module must not read a socket, a file or an exec result, because the registry is built on hosts without tailscale and by the schema generator.

- [ ] **Step 4: Run test to verify it passes, then make the registry-wide tests pass**

Run: `timeout 300 go test -count=1 -run 'TestReg' ./internal/modules/tailscale/`
Expected: PASS.

Run: `timeout 600 go test -count=1 ./internal/modules/`
Expected: FAIL in two tests, which this step fixes.
- `TestPointerOutputsAreNullable` names each pointer field of an output type whose schema does not accept null: `tailscale.status: output field self ...`, `tailscale.status: output field peers[].lastHandshake ...`, and `tailscale.prefs.set/v1` and `tailscale.prefs.repair/v1` `output field prefs.<field> ...` for each of the seven `PrefsFields` members. In `wire.go` add `nullable` to the `jsonschema` tag of exactly those fields that do not carry it (write `jsonschema:"nullable"` when the field has no `jsonschema` tag, otherwise append `,nullable` to its value): `Status.Self`, `Peer.LastHandshake`, and `PrefsFields.WantRunning`, `AdvertiseRoutes`, `ExitNode`, `AdvertiseExitNode`, `AcceptRoutes`, `ShieldsUp`, `RunSSH`. Keep each field's `json` tag and its `jsonschema_description` as they are. A `null` for a `PrefsFields` member in an input still means "not set".
- `TestSnapshotsCurrent` lists the eleven `schemas/tailscale.*/v1.json` files as missing. Run `timeout 600 make schemas`; it must create exactly those eleven directories and change no other file under `schemas/` (`git status --short schemas` shows eleven `??` lines and nothing else).

Run again: `timeout 600 go test -count=1 ./internal/modules/...`
Expected: every line `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/modules/tailscale/tailscale.go internal/modules/tailscale/tailscale_test.go internal/modules/tailscale/wire.go internal/modules/tailscale/register_test.go schemas/tailscale.*
git commit -m "feat(tailscale): register actions and resources"
```

**Verify:** `mkdir -p /root/.cache/w4-tmp/31 && export TMPDIR=/root/.cache/w4-tmp/31 GOTOOLCHAIN=local && timeout 300 go build ./... && timeout 300 go vet ./... && timeout 300 go test -race -count=1 ./internal/modules/tailscale/ && timeout 900 go test -count=1 ./internal/modules/... ./schemas/... && timeout 900 golangci-lint run --allow-serial-runners ./... && timeout 300 go mod tidy -diff`; then `rm -rf /root/.cache/w4-tmp/31 && go clean -testcache`.

### Task 32: tailnet: node.join composite

**Files:**
- Create: `internal/modules/tailnet/join.go`
- Modify: `internal/modules/tailnet/tailnet.go` (`actions()`)
- Modify: `internal/modules/tailnet/register_test.go` (`TestRegManifestActions`, `TestRegInputs`)
- Create: `internal/modules/tailnet/join_test.go`
- Create (generated by `make schemas`): `schemas/tailnet.node.join/v1.json`

**Interfaces:**
- Consumes: Contracts "Tailnet node join (from Task 32)" for the steps; Contracts "Tailscale wire shapes" for `tailscale.package.install/v1` (input `{}`) and `tailscale.node.login/v1` (input `LoginInput`, whose `authKey` is secret, output `LoginResult`); the module's own `tailnet.authkey.create/v1` (`AuthKeyInput`, `CreatedKey` in `wire.go`, whose `key` is secret); `core.DefineComposite`, `core.Step` and `core.Composer` from `internal/core`. The `tailscale` actions are registered since Task 31.
- Produces: the action `tailnet.node.join/v1` (controller-side composite, `Unwind: reverse`, lock `composite:tailnet`) and these types in `join.go`, which Task 19's web page and the harness use by their JSON names:

```go
// NodeJoinInput is the input of tailnet.node.join.
type NodeJoinInput struct {
	HostID  string   `json:"hostId" jsonschema:"minLength=1"`
	Tags    []string `json:"tags,omitempty"`
	Install bool     `json:"install,omitempty"`
}

// NodeJoined is the output of tailnet.node.join.
type NodeJoined struct {
	BackendState string   `json:"backendState"`
	Addresses    []string `json:"addresses"`
}
```

**Depends on:** Task 10, Task 31

- [ ] **Step 1: Write the failing tests**

Create `internal/modules/tailnet/join_test.go`:

```go
package tailnet

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/modules/tailscale"
)

// njDef returns the declaration of tailnet.node.join.
func njDef(t *testing.T) core.ActionDef {
	t.Helper()
	for _, a := range Module().Manifest().Actions {
		if a.Spec.Name == "tailnet.node.join" {
			return a
		}
	}
	t.Fatal("tailnet.node.join is not declared")
	return core.ActionDef{}
}

// njSteps asks the composite for its steps the way the engine does, through
// the erased handler.
func njSteps(t *testing.T, in NodeJoinInput) ([]core.Step, error) {
	t.Helper()
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	return njDef(t).Handler.Steps(context.Background(), core.Env{}, raw)
}

func njJSON(t *testing.T, raw json.RawMessage) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("%s: %v", raw, err)
	}
	return v
}

// TestNJDeclaration: the join is a controller-side composite that unwinds in
// reverse and holds the composite lock of its module, which the registry
// accepts together with the tailscale module its steps run in.
func TestNJDeclaration(t *testing.T) {
	s := njDef(t).Spec
	if s.Version != 1 || s.Class != core.ClassComposite || s.Side != core.SideController || s.Unwind != core.UnwindReverse ||
		!slices.Equal(s.LockKeys, []string{"composite:tailnet"}) {
		t.Errorf("spec %+v, want version 1, a controller-side composite, unwind reverse, lock composite:tailnet", s)
	}
	if _, err := core.NewRegistry(Module(), tailscale.Module()); err != nil {
		t.Errorf("core.NewRegistry: %v", err)
	}
}

func TestNJStepsWithoutInstall(t *testing.T) {
	steps, err := njSteps(t, NodeJoinInput{HostID: "h1", Tags: []string{"tag:darkpanel", "tag:web"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 2 {
		t.Fatalf("%d steps, want key and login", len(steps))
	}
	key, login := steps[0], steps[1]
	if key.ID != "key" || key.Host != "" || key.Action != (core.ActionRef{Name: "tailnet.authkey.create", Version: 1}) || len(key.After) != 0 {
		t.Errorf("first step %+v, want key: tailnet.authkey.create/v1 on the controller, after nothing", key)
	}
	wantKey := map[string]any{"tags": []any{"tag:darkpanel", "tag:web"}, "preauthorized": true, "expirySeconds": float64(3600)}
	if got := njJSON(t, key.Input); !reflect.DeepEqual(got, wantKey) {
		t.Errorf("key input %s, want %v: pre-authorized, not reusable, not ephemeral, expiring after 3600 s", key.Input, wantKey)
	}
	if login.ID != "login" || login.Host != "h1" || login.Action != (core.ActionRef{Name: "tailscale.node.login", Version: 1}) || !slices.Equal(login.After, []string{"key"}) {
		t.Errorf("second step %+v, want login: tailscale.node.login/v1 on h1, after key", login)
	}
	if got := njJSON(t, login.Input); !reflect.DeepEqual(got, map[string]any{"authKey": "${steps.key.output.key}"}) {
		t.Errorf("login input %s, want the auth key as a reference to the key step's output and nothing else", login.Input)
	}
}

func TestNJStepsWithInstall(t *testing.T) {
	steps, err := njSteps(t, NodeJoinInput{HostID: "h1", Tags: []string{"tag:darkpanel"}, Install: true})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, s := range steps {
		ids = append(ids, s.ID)
	}
	if !slices.Equal(ids, []string{"install", "key", "login"}) {
		t.Fatalf("steps %v, want install, key, login", ids)
	}
	install, login := steps[0], steps[2]
	if install.Host != "h1" || install.Action != (core.ActionRef{Name: "tailscale.package.install", Version: 1}) || len(install.After) != 0 {
		t.Errorf("install step %+v, want tailscale.package.install/v1 on h1, after nothing", install)
	}
	if got := njJSON(t, install.Input); !reflect.DeepEqual(got, map[string]any{}) {
		t.Errorf("install input %s, want {}", install.Input)
	}
	if !slices.Equal(login.After, []string{"install", "key"}) {
		t.Errorf("login follows %v, want install and key", login.After)
	}
}

func TestNJStepsRefuseNoTags(t *testing.T) {
	for _, tags := range [][]string{nil, {}} {
		_, err := njSteps(t, NodeJoinInput{HostID: "h1", Tags: tags})
		var coded *errcode.Error
		if !errors.As(err, &coded) || coded.Code != errcode.InvalidInput ||
			!slices.ContainsFunc(coded.Fields, func(f errcode.FieldError) bool { return f.Path == "/tags" }) {
			t.Errorf("tags %v: %v, want %s naming /tags", tags, err, errcode.InvalidInput)
		}
	}
}

// TestNJStepsMatchTheRegistry: every step the composite returns names a
// registered action, and obeys the rules the engine checks at plan time: a
// controller action takes no host and an agent action needs one, no step is
// irreversible or a composite, every compensable step has an undo handler
// (the join unwinds in reverse), and each step's input is valid for its
// action's schema once the auth key reference is filled in.
func TestNJStepsMatchTheRegistry(t *testing.T) {
	reg, err := core.NewRegistry(Module(), tailscale.Module())
	if err != nil {
		t.Fatal(err)
	}
	steps, err := njSteps(t, NodeJoinInput{HostID: "h1", Tags: []string{"tag:darkpanel"}, Install: true})
	if err != nil {
		t.Fatal(err)
	}
	validator := schema.NewValidator()
	for _, s := range steps {
		def, ok := reg.Action(s.Action)
		if !ok {
			t.Errorf("step %s: %s is not registered", s.ID, s.Action)
			continue
		}
		switch {
		case def.Spec.Side == core.SideController && s.Host != "", def.Spec.Side == core.SideAgent && s.Host == "":
			t.Errorf("step %s: %s is %s-side but the step names host %q", s.ID, s.Action, def.Spec.Side, s.Host)
		case def.Spec.Class == core.ClassIrreversible, def.Spec.Class == core.ClassComposite:
			t.Errorf("step %s: %s is %s, which a step never is", s.ID, s.Action, def.Spec.Class)
		case def.Spec.Class == core.ClassCompensable && def.Handler.Inverse == nil:
			t.Errorf("step %s: %s has no undo handler, which a reverse composite needs", s.ID, s.Action)
		}
		input := json.RawMessage(strings.ReplaceAll(string(s.Input), "${steps.key.output.key}", "tskey-auth-example"))
		if err := validator.ValidateInput(def, input); err != nil {
			t.Errorf("step %s: input %s is invalid for %s: %v", s.ID, input, s.Action, err)
		}
	}
	created, err := json.Marshal(CreatedKey{ID: "k1", Key: "tskey-auth-example"})
	if err != nil || !strings.Contains(string(created), `"key":"tskey-auth-example"`) {
		t.Errorf("tailnet.authkey.create output %s, %v; want the member key that the login step references", created, err)
	}
}

func TestNJOutput(t *testing.T) {
	def := njDef(t)
	in := json.RawMessage(`{"hostId":"h1","tags":["tag:darkpanel"]}`)
	login := json.RawMessage(`{"backendState":"Running","dnsName":"h1.example.ts.net","addresses":["100.64.0.5","fd7a:115c:a1e0::5"]}`)
	out, err := def.Handler.Output(context.Background(), core.Env{}, in, map[string]json.RawMessage{"key": json.RawMessage(`{"id":"k1"}`), "login": login})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"backendState": "Running", "addresses": []any{"100.64.0.5", "fd7a:115c:a1e0::5"}}
	if got := njJSON(t, out); !reflect.DeepEqual(got, want) {
		t.Errorf("output %s, want %v", out, want)
	}
	if _, err := def.Handler.Output(context.Background(), core.Env{}, in, map[string]json.RawMessage{"key": json.RawMessage(`{"id":"k1"}`)}); err == nil || !strings.Contains(err.Error(), "login") {
		t.Errorf("output without the login step: %v, want an error naming login", err)
	}
}
```

Then edit `internal/modules/tailnet/register_test.go` in two places.

1. In `TestRegManifestActions`, append the row `{"tailnet.node.join", core.ClassComposite, false, false, false},` after the `tailnet.policy.patch` row of `want`, and replace the three lines that compute `wantLocks` with:

```go
		wantLocks := []string{"tailnet"}
		switch w.class {
		case core.ClassRead:
			wantLocks = nil
		case core.ClassComposite:
			wantLocks = []string{"composite:tailnet"}
		}
```

   and replace the doc comment of the test with one that says the join composite holds `composite:tailnet`, as the join test file does: "all controller-side at version 1. Mutating actions lock tailnet, the join composite holds composite:tailnet and reads take no lock".
2. In `TestRegInputs`, append these rows to `tests` after the `tailnet.policy.patch` rows:

```go
		{"tailnet.node.join", `{"hostId":"h1","tags":["tag:a"]}`, ""},
		{"tailnet.node.join", `{"hostId":"h1","tags":["tag:a"],"install":true}`, ""},
		{"tailnet.node.join", `{"tags":["tag:a"]}`, "/hostId"},
		{"tailnet.node.join", `{"hostId":""}`, "/hostId"},
		{"tailnet.node.join", `{"hostId":"h1","install":"yes"}`, "/install"},
```

- [ ] **Step 2: Run test to verify it fails**

Run: `mkdir -p /root/.cache/w4-tmp/32 && export TMPDIR=/root/.cache/w4-tmp/32 GOTOOLCHAIN=local && timeout 300 go test -count=1 ./internal/modules/tailnet/`
Expected: FAIL to build, `undefined: NodeJoinInput` (the first of several in `join_test.go`).

- [ ] **Step 3: Implement**

1. Create `join.go` with the two types of the Interfaces block, a constant for the key's lifetime of 3600 seconds, and a doc comment on every exported identifier.
2. Add `func (t tn) nodeJoin() core.ActionDef`, which returns `core.DefineComposite[NodeJoinInput, NodeJoined]` with the spec `Name: ID + ".node.join"`, a `Summary` ("Join an enrolled host to the tailnet."), `Version: 1` and `Unwind: core.UnwindReverse`. `DefineComposite` sets the class, the side and the lock key itself; do not set them. The handler is a new type `nodeJoinComposer` with the two methods of `core.Composer`.
3. `Steps(ctx, env, in)` reads nothing but `in`. With no tag in `in.Tags` it returns an `*errcode.Error` with code `errcode.InvalidInput`, detail "the join input is invalid" and one field error, path `/tags`, message "at least one tag is required". Otherwise it returns, in this order:
   - only when `in.Install`: step `install`, host `in.HostID`, action `tailscale.package.install` version 1, input `{}`, no `After`;
   - step `key`, host `""`, action `tailnet.authkey.create` version 1, no `After`, input the JSON of an `AuthKeyInput` with `Tags` as given (order kept), `Preauthorized` true and `ExpirySeconds` 3600, which leaves `reusable` and `ephemeral` out (both false);
   - step `login`, host `in.HostID`, action `tailscale.node.login` version 1, `After` `["install","key"]` when `in.Install` and `["key"]` otherwise, input exactly `{"authKey":"${steps.key.output.key}"}`. The auth key is only ever a reference to the key step's private output: the engine refuses a literal in a secret field, and a template is stored and shown in clear.
4. `Output(ctx, env, in, steps)` decodes `steps["login"]` (the redacted output of `tailscale.node.login`, a `LoginResult`) and returns a `NodeJoined` with its `backendState` and `addresses`. When there is no `login` entry it returns an error whose text names the login step; when the entry is not JSON it returns the decode error wrapped with `tailnet:`.
5. In `tailnet.go` append `t.nodeJoin()` to `actions()` after `t.policyPatch()` and add the join composite to the comment above `actions()`.
6. If a name you declare collides with one an earlier task declared in the package (Tasks 10 and 18 added files for the enroll keys), rename yours; the test uses only `NodeJoinInput` and `NodeJoined`.

- [ ] **Step 4: Run test to verify it passes**

Run: `timeout 300 go test -count=1 -run 'TestNJ|TestReg' ./internal/modules/tailnet/`
Expected: PASS, including `TestRegManifestActions` and `TestRegInputs` with the join rows.

Run: `timeout 600 make schemas`
Expected: creates `schemas/tailnet.node.join/v1.json` and changes no other file (`git status --short schemas` shows that one `??` line). Its input has `hostId` required with `minLength` 1, `tags` and `install` optional; its output has `backendState` and `addresses`.

Run: `timeout 900 go test -count=1 ./internal/modules/...`
Expected: every line `ok` (`TestSnapshotsCurrent`, `TestRegistrationRules`, `TestEveryActionHasACLICommand` and `TestPointerOutputsAreNullable` see the new composite).

- [ ] **Step 5: Commit**

```bash
git add internal/modules/tailnet/join.go internal/modules/tailnet/join_test.go internal/modules/tailnet/tailnet.go internal/modules/tailnet/register_test.go schemas/tailnet.node.join
git commit -m "feat(tailnet): add the node.join composite"
```

**Verify:** `mkdir -p /root/.cache/w4-tmp/32 && export TMPDIR=/root/.cache/w4-tmp/32 GOTOOLCHAIN=local && timeout 300 go build ./... && timeout 300 go vet ./... && timeout 600 go test -race -count=1 ./internal/modules/tailnet/ && timeout 900 go test -count=1 ./internal/modules/... && timeout 900 golangci-lint run --allow-serial-runners ./... && timeout 300 go mod tidy -diff`; then `rm -rf /root/.cache/w4-tmp/32 && go clean -testcache`.

### Task 33: harness: TestM5Node scenarios

**Files:**
- Create: `test/harness/m5_helpers_test.go` (the shared helpers of Contracts "Harness M5")
- Create: `test/harness/m5_test.go` (`TestM5Node` and its scenarios)
- Create: `test/harness/m5_unit_test.go` (`TestM5Unit...`, no VM)

**Interfaces:**
- Consumes: Contracts "Harness M5"; Contracts "Tailscale wire shapes" (`tailscale.Status`, `Latency`, `PrefsResult`, `PackageInstalled`, imported from `internal/modules/tailscale`); the registered actions of Task 31 (`tailscale.prefs.set`, `tailscale.package.install`) and resources (`tailscale.status`, `tailscale.latency`); the wave 0 lab helpers `NewLab`, `(*Lab).Boot`, `VMName`, `MAC` and the NIC constants (`lab_helpers_test.go`); `harnessImages`, `serveReleases`, `scenarioInstall`, `fleet`, `eventually`, `unitState` (`m1_test.go`, `m2_test.go`); `m3Run`, `m3JSON`, `m3RequirePending`, `m3WaitChange`, `(*API).m3Apply`, `m3Revert`, `m3Change`; `m4Plan`, `m4xTerminal`, `m4SettleTimeout`, `m4TestAPI`; `API.do`, `API.Resource`, `actionPath`, `APIError`, `Change`; `LoadPins`, `PinsFile`, `OS`, `TargetScript`, `ScannerScript` (`pins.go`, `provision.go`). `api.go`, `provision.go` and the M2 to M4 files are not changed.
- Produces (Contracts "Harness M5", used by Tasks 34 and 35):

```go
var m5Flag *bool                    // flag -m5
type VM = vm.VM                     // test/harness/vm
type ChangeView = Change            // the harness's change type
func m5Lab(t *testing.T) (*Lab, *vm.VM)
func m5Apply(t *testing.T, v *vm.VM, action string, input any) ChangeView
func m5Status(t *testing.T, v *vm.VM) tailscale.Status
```

  Also produced, for Task 34 to reuse: `m5ReadStatus`, `m5HostPrefs`, `m5PlanLink` and `m5Lookup` (below).

**Depends on:** Task 31

The scenarios of `m5_test.go` need KVM virtual machines, so they run only in the harness workflow (Task 35 adds the `vm-m5` job; gate 7 covers them). On this host the task compiles them, runs the VM-free unit tests, and sees `TestM5Node` skip. Never pass `-os`, `-m2` or `-m5` here.

- [ ] **Step 1: Write the failing tests**

Create `test/harness/m5_unit_test.go`:

```go
//go:build harness

package harness

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/tailscale"
	"github.com/darkraise/darkpanel/internal/platform/tslocal"
	"github.com/darkraise/darkpanel/test/harness/vm"
)

// m5TestNode registers a node for a VM that was never booted: the helpers
// use the *vm.VM only as the key of their node.
func m5TestNode(t *testing.T, srv *httptest.Server) *vm.VM {
	t.Helper()
	v := new(vm.VM)
	m5Register(t, v, m5Node{ctx: context.Background(), api: m4TestAPI(t, srv), hostID: "h1", os: OS{ID: "ubuntu-24.04", Family: "ubuntu"}})
	return v
}

func TestM5UnitRegister(t *testing.T) {
	v := new(vm.VM)
	t.Run("registered", func(t *testing.T) {
		m5Register(t, v, m5Node{hostID: "h1"})
		if got := m5Lookup(t, v).hostID; got != "h1" {
			t.Errorf("m5Lookup = host %q, want h1", got)
		}
	})
	if _, ok := m5Nodes.Load(v); ok {
		t.Error("the node of a finished test is still registered")
	}
}

func TestM5UnitConfigs(t *testing.T) {
	pins, err := LoadPins(PinsFile)
	if err != nil {
		t.Fatal(err)
	}
	o, err := pins.Find(pins.OS[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	lab, err := newLabAllocator(labPortMin).lab("TestM5Node", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	key, err := vm.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	cfgs := m5Configs(lab, o, "/images/target.img", "/images/scanner.img", key)
	if len(cfgs) != 3 {
		t.Fatalf("%d VM configs, want the controller, the agent and the scanner", len(cfgs))
	}
	roles := []string{"controller", "agent", "scanner"}
	macs := map[string]bool{}
	for i, c := range cfgs {
		if c.Name != lab.VMName(roles[i]) {
			t.Errorf("VM %d is named %q, want %q", i, c.Name, lab.VMName(roles[i]))
		}
		if c.McastPort != lab.McastPort {
			t.Errorf("%s uses internet link port %d, want the lab's %d", c.Name, c.McastPort, lab.McastPort)
		}
		if (c.PrivMcastPort != 0) != (roles[i] != "scanner") || (c.PrivMcastPort != 0 && c.PrivMcastPort != lab.PrivMcastPort) {
			t.Errorf("%s uses private link port %d, want the lab's %d on the targets only", c.Name, c.PrivMcastPort, lab.PrivMcastPort)
		}
		if c.Dir != "" {
			t.Errorf("%s has Dir %q; Lab.Boot sets it", c.Name, c.Dir)
		}
		for _, mac := range []string{c.MgmtMAC, c.InetMAC, c.PrivMAC} {
			if mac == "" {
				continue
			}
			if !strings.HasPrefix(mac, "52:54:00:") || macs[mac] {
				t.Errorf("%s MAC %q is outside the lab's range or repeated", c.Name, mac)
			}
			macs[mac] = true
		}
	}
	if !slices.Equal(cfgs[1].InetAddrs, []string{agentIPv4 + "/15", agentIPv6 + "/64"}) || !slices.Equal(cfgs[1].PrivAddrs, []string{agentPriv + "/24"}) {
		t.Errorf("agent addresses %v and %v, want the fleet's", cfgs[1].InetAddrs, cfgs[1].PrivAddrs)
	}
	if !slices.Equal(cfgs[0].PrivAddrs, []string{controllerPriv + "/24"}) {
		t.Errorf("controller private address %v, want %s/24", cfgs[0].PrivAddrs, controllerPriv)
	}
	if cfgs[0].Provision != TargetScript(o) || cfgs[1].Provision != TargetScript(o) || cfgs[2].Provision != ScannerScript() {
		t.Error("a VM is not provisioned with its role's script")
	}
}

func TestM5UnitApply(t *testing.T) {
	var polls atomic.Int32
	var body atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/hosts/h1/actions/tailscale.prefs.set/v1/plan":
			_, _ = w.Write([]byte(`{"planId":"p1","diff":[],"guards":[{"id":"tailscale.link","ok":true,"message":"","overridable":true,"overridden":false}]}`))
		case "/api/v1/hosts/h1/actions/tailscale.prefs.set/v1/apply":
			var b map[string]any
			_ = json.NewDecoder(r.Body).Decode(&b)
			body.Store(b)
			_, _ = w.Write([]byte(`{"id":"c1","state":"pending"}`))
		case "/api/v1/hosts/h1/actions/tailscale.package.install/v1/plan":
			_, _ = w.Write([]byte(`{"planId":"p2","diff":[],"guards":[]}`))
		case "/api/v1/hosts/h1/actions/tailscale.package.install/v1/apply":
			_, _ = w.Write([]byte(`{"id":"c2","state":"applying"}`))
		case "/api/v1/changes/c1":
			if polls.Add(1) < 3 {
				_, _ = w.Write([]byte(`{"id":"c1","state":"pending"}`))
				return
			}
			_, _ = w.Write([]byte(`{"id":"c1","state":"confirmed","output":{"prefs":{"acceptRoutes":true}}}`))
		case "/api/v1/changes/c2":
			_, _ = w.Write([]byte(`{"id":"c2","state":"completed","output":{"version":"1.104.2"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	v := m5TestNode(t, srv)

	ch := m5Apply(t, v, "tailscale.prefs.set", map[string]any{"acceptRoutes": true})
	if ch.State != core.StateConfirmed || string(ch.Output) != `{"prefs":{"acceptRoutes":true}}` {
		t.Errorf("change = %+v, want confirmed with the output", ch)
	}
	if b, _ := body.Load().(map[string]any); b["planId"] != "p1" || b["confirmNow"] != true {
		t.Errorf("apply carried %v, want planId p1 and confirmNow true", b)
	}
	if polls.Load() < 3 {
		t.Errorf("polled %d times, want it to wait while the change was pending", polls.Load())
	}
	if ch := m5Apply(t, v, "tailscale.package.install", struct{}{}); ch.State != core.StateCompleted || string(ch.Output) != `{"version":"1.104.2"}` {
		t.Errorf("task change = %+v, want completed with the version", ch)
	}
}

func TestM5UnitStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/hosts/h1/resources/tailscale/status" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"version":"1.104.1-tabc","backendState":"NeedsLogin","self":null,"tailnet":"","runSSH":true,"peers":[],"health":[]}`))
	}))
	t.Cleanup(srv.Close)
	st := m5Status(t, m5TestNode(t, srv))
	if st.BackendState != "NeedsLogin" || st.Self != nil || !st.RunSSH || st.Version != "1.104.1-tabc" {
		t.Errorf("m5Status = %+v, want the NeedsLogin node with SSH on", st)
	}
}

func TestM5UnitParsePrefs(t *testing.T) {
	good := `{"ControlURL":"https://controlplane.tailscale.com","RouteAll":true,"ShieldsUp":false,"RunSSH":true,"WantRunning":false,"AdvertiseRoutes":null}`
	got, err := m5xParsePrefs(good)
	if want := (m5Prefs{RouteAll: true, RunSSH: true}); err != nil || got != want {
		t.Errorf("m5xParsePrefs = %+v, %v; want %+v", got, err, want)
	}
	for _, c := range []struct{ name, in, want string }{
		{"not JSON", "Logged out.", "tailscale debug prefs"},
		{"member missing", `{"RouteAll":true,"ShieldsUp":false,"RunSSH":true}`, "WantRunning"},
		{"member not a bool", `{"RouteAll":"yes","ShieldsUp":false,"RunSSH":true,"WantRunning":false}`, "RouteAll"},
	} {
		if _, err := m5xParsePrefs(c.in); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error %v, want one naming %q", c.name, err, c.want)
		}
	}
}

func TestM5UnitPinned(t *testing.T) {
	pins, err := LoadPins(PinsFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range pins.OS {
		if v, err := m5xPinned(o); err != nil || v == "" {
			t.Errorf("m5xPinned(%s) = %q, %v; want the pinned version", o.ID, v, err)
		}
	}
	if _, err := m5xPinned(OS{ID: "x", Packages: []Package{{Name: "docker-ce", Version: "1"}}}); err == nil {
		t.Error("an OS without a tailscale pin: no error")
	}
}

func TestM5UnitLoggedOut(t *testing.T) {
	good := tailscale.Status{Version: "1.104.1-t123", BackendState: "NeedsLogin"}
	if err := m5xLoggedOut(good, "1.104.1"); err != nil {
		t.Errorf("a logged-out node: %v", err)
	}
	if err := m5xLoggedOut(good, ""); err != nil {
		t.Errorf("a logged-out node, any version: %v", err)
	}
	for _, c := range []struct {
		name string
		edit func(*tailscale.Status)
		want string
	}{
		{"running", func(s *tailscale.Status) { s.BackendState = "Running" }, "NeedsLogin"},
		{"self", func(s *tailscale.Status) { s.Self = &tailscale.Node{ID: "n1"} }, "self"},
		{"tailnet", func(s *tailscale.Status) { s.Tailnet = "example.com" }, "tailnet"},
		{"peers", func(s *tailscale.Status) { s.Peers = []tailscale.Peer{{}} }, "1 peers"},
		{"version", func(s *tailscale.Status) { s.Version = "1.90.0" }, "1.90.0"},
		{"no version", func(s *tailscale.Status) { s.Version = "" }, "version"},
	} {
		s := good
		c.edit(&s)
		if err := m5xLoggedOut(s, "1.104.1"); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error %v, want one naming %q", c.name, err, c.want)
		}
	}
}

func TestM5UnitCheckLatency(t *testing.T) {
	m := tslocal.DERPMap{Regions: map[int]*tslocal.DERPRegion{
		1: {RegionID: 1, RegionCode: "nyc", RegionName: "New York City", Nodes: []*tslocal.DERPNode{{Name: "1a", RegionID: 1, HostName: "derp1.example", IPv4: "192.0.2.1"}}},
		2: {RegionID: 2, RegionCode: "tok", RegionName: "Tokyo", Nodes: []*tslocal.DERPNode{{Name: "2a", RegionID: 2, HostName: "derp2.example", IPv4: "192.0.2.2", STUNPort: -1}}},
	}}
	measured := tailscale.Measurement{Available: true, MedianMs: 12.5}
	missing := tailscale.Measurement{Reason: "no STUN endpoint"}
	good := func() tailscale.Latency {
		return tailscale.Latency{Regions: []tailscale.RegionLatency{
			{RegionID: 1, Code: "nyc", Name: "New York City", STUN: measured, HTTPS: measured},
			{RegionID: 2, Code: "tok", Name: "Tokyo", STUN: missing, HTTPS: measured},
		}}
	}
	if err := m5xCheckLatency(good(), m); err != nil {
		t.Errorf("a consistent answer: %v", err)
	}
	if err := m5xCheckLatency(tailscale.Latency{}, tslocal.DERPMap{}); err != nil {
		t.Errorf("no regions for an empty map: %v", err)
	}
	for _, c := range []struct {
		name string
		edit func(*tailscale.Latency)
		want string
	}{
		{"region missing", func(l *tailscale.Latency) { l.Regions = l.Regions[:1] }, "lists regions [1]"},
		{"region added", func(l *tailscale.Latency) { l.Regions = append(l.Regions, tailscale.RegionLatency{RegionID: 3}) }, "lists regions [1 2 3]"},
		{"unsorted", func(l *tailscale.Latency) { slices.Reverse(l.Regions) }, "ascending"},
		{"wrong code", func(l *tailscale.Latency) { l.Regions[0].Code = "sfo" }, `region 1 is "sfo"`},
		{"available without median", func(l *tailscale.Latency) { l.Regions[0].HTTPS.MedianMs = 0 }, "region 1 HTTPS: available"},
		{"available with reason", func(l *tailscale.Latency) { l.Regions[0].STUN.Reason = "x" }, "region 1 STUN: available"},
		{"unavailable without reason", func(l *tailscale.Latency) { l.Regions[1].STUN.Reason = "" }, "region 2 STUN: unavailable"},
		{"unavailable with median", func(l *tailscale.Latency) { l.Regions[1].STUN.MedianMs = 3 }, "region 2 STUN: unavailable"},
		{"STUN without an endpoint", func(l *tailscale.Latency) { l.Regions[1].STUN = measured }, "region 2 reports STUN latency but has no STUN endpoint"},
	} {
		l := good()
		c.edit(&l)
		if err := m5xCheckLatency(l, m); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error %v, want one naming %q", c.name, err, c.want)
		}
	}
	if err := m5xCheckLatency(tailscale.Latency{Regions: []tailscale.RegionLatency{{RegionID: 1}}}, tslocal.DERPMap{}); err == nil {
		t.Error("a region for an empty map: no error")
	}
	if !m5xAnyMeasured(good()) {
		t.Error("m5xAnyMeasured of an answer with measurements = false")
	}
	if m5xAnyMeasured(tailscale.Latency{Regions: []tailscale.RegionLatency{{RegionID: 2, STUN: missing, HTTPS: missing}}}) {
		t.Error("m5xAnyMeasured of an answer with none = true")
	}
}

func TestM5UnitSourceLine(t *testing.T) {
	const want = "deb [signed-by=/usr/share/keyrings/tailscale-archive-keyring.gpg] https://pkgs.tailscale.com/stable/debian bookworm main\n"
	if got := m5xSourceLine("debian", "bookworm"); got != want {
		t.Errorf("m5xSourceLine = %q, want %q", got, want)
	}
}

func TestM5UnitLinkGuard(t *testing.T) {
	g, err := m5xLinkGuard([]core.Guard{{ID: "other", OK: true}, {ID: "tailscale.link", OK: false, Overridable: true, Message: "x"}})
	if err != nil || g.OK || !g.Overridable || g.Message != "x" {
		t.Errorf("m5xLinkGuard = %+v, %v; want the tailscale.link guard", g, err)
	}
	if _, err := m5xLinkGuard([]core.Guard{{ID: "firewall.lockout", OK: true}}); err == nil || !strings.Contains(err.Error(), "tailscale.link") {
		t.Errorf("a plan without the guard: %v, want an error naming tailscale.link", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/hosts/h1/actions/tailscale.prefs.set/v1/plan" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"planId":"p1","diff":[],"guards":[{"id":"tailscale.link","ok":false,"message":"ssh","overridable":true,"overridden":false}]}`))
	}))
	t.Cleanup(srv.Close)
	n := m5Lookup(t, m5TestNode(t, srv))
	g, err = m5PlanLink(context.Background(), n, "tailscale.prefs.set", map[string]any{"shieldsUp": true})
	if err != nil || g.OK || g.Message != "ssh" {
		t.Errorf("m5PlanLink = %+v, %v; want the failing guard", g, err)
	}
}
```

Create `test/harness/m5_test.go`:

```go
//go:build harness

package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/tailscale"
	"github.com/darkraise/darkpanel/internal/platform/tslocal"
	"github.com/darkraise/darkpanel/test/harness/vm"
)

// m5PendingSeconds is the revert timeout of the changes the scenarios revert
// by hand: long enough that the timer never fires first.
const m5PendingSeconds = 120

// TestM5Node is the node half of M5 (spec, Verification: the harness node tests
// that need no tailnet) on one agent whose tailscaled is installed and logged
// out. The workflow selects it with -run, -m2 and -m5.
func TestM5Node(t *testing.T) {
	lab, agent := m5Lab(t)
	t.Logf("lab %s", lab.Slug)
	steps := []struct {
		name string
		gate bool // later steps need this one
		run  func(t *testing.T, v *vm.VM)
	}{
		{"status-logged-out", true, m5NodeStatus},
		{"prefs-patch-and-revert", false, m5NodePrefs},
		{"guard-runssh-refusal", false, m5NodeRunSSH},
		{"latency-against-derp-map", false, m5NodeLatency},
		// The install scenario removes the package the image pinned, so it runs last.
		{"package-install", false, m5NodeInstall},
	}
	for _, s := range steps {
		if !t.Run(s.name, func(t *testing.T) { s.run(t, agent) }) && s.gate {
			return
		}
	}
}

// m5NodeStatus: the logged-out node answers tailscale.status as NeedsLogin
// with no self, tailnet or peers, running the version the image pins.
func m5NodeStatus(t *testing.T, v *vm.VM) {
	n := m5Lookup(t, v)
	pinned, err := m5xPinned(n.os)
	if err != nil {
		t.Fatal(err)
	}
	var st tailscale.Status
	err = eventually(n.ctx, 2*time.Minute, func(ctx context.Context) error {
		var err error
		if st, err = m5ReadStatus(ctx, n); err != nil {
			return err
		}
		return m5xLoggedOut(st, pinned)
	})
	if err != nil {
		t.Fatalf("tailscale.status of a logged-out node: %v", err)
	}
}

// m5NodePrefs: prefs.set changes only the fields it is given. A change left
// unconfirmed and reverted restores the old values; a confirmed one keeps its
// own and leaves the others alone, and the output names exactly the fields set.
func m5NodePrefs(t *testing.T, v *vm.VM) {
	n := m5Lookup(t, v)
	ctx := n.ctx
	before := m5HostPrefs(ctx, t, v)

	ch, err := n.api.m3Apply(ctx, n.hostID, "tailscale.prefs.set", map[string]any{"acceptRoutes": !before.RouteAll, "shieldsUp": !before.ShieldsUp}, m5PendingSeconds)
	m3RequirePending(t, ch, err)
	want := before
	want.RouteAll, want.ShieldsUp = !before.RouteAll, !before.ShieldsUp
	if got := m5HostPrefs(ctx, t, v); got != want {
		t.Errorf("preferences after the unconfirmed change: %+v, want %+v", got, want)
	}
	if _, err := n.api.m3Revert(ctx, ch.ID); err != nil {
		t.Fatal(err)
	}
	m3WaitChange(ctx, t, n.api, ch.ID, core.StateReverted, 2*time.Minute)
	if got := m5HostPrefs(ctx, t, v); got != before {
		t.Errorf("preferences after the revert: %+v, want %+v", got, before)
	}

	done := m5Apply(t, v, "tailscale.prefs.set", map[string]any{"acceptRoutes": !before.RouteAll})
	var res tailscale.PrefsResult
	if err := json.Unmarshal(done.Output, &res); err != nil {
		t.Fatalf("output of the confirmed change: %v", err)
	}
	if got, wantOut := m3JSON(res.Prefs), fmt.Sprintf(`{"acceptRoutes":%t}`, !before.RouteAll); got != wantOut {
		t.Errorf("output prefs %s, want exactly %s", got, wantOut)
	}
	want = before
	want.RouteAll = !before.RouteAll
	if got := m5HostPrefs(ctx, t, v); got != want {
		t.Errorf("preferences after the confirmed change: %+v, want %+v", got, want)
	}
	m5Apply(t, v, "tailscale.prefs.set", map[string]any{"acceptRoutes": before.RouteAll})
	if got := m5HostPrefs(ctx, t, v); got != before {
		t.Errorf("preferences after setting the field back: %+v, want %+v", got, before)
	}
}

// m5NodeRunSSH: while Tailscale SSH is on, the tailscale.link guard refuses a
// change that would block it (shields-up), and an override lets the same plan
// through; once SSH is off again the same plan passes the guard. Tailscale SSH
// is turned on by a change that is left unconfirmed and reverted.
func m5NodeRunSSH(t *testing.T, v *vm.VM) {
	n := m5Lookup(t, v)
	ctx := n.ctx
	shields := map[string]any{"shieldsUp": true}

	ch, err := n.api.m3Apply(ctx, n.hostID, "tailscale.prefs.set", map[string]any{"runSSH": true}, m5PendingSeconds)
	m3RequirePending(t, ch, err)
	reverted := false
	t.Cleanup(func() {
		if !reverted {
			_, _ = n.api.m3Revert(ctx, ch.ID)
		}
	})
	if !m5HostPrefs(ctx, t, v).RunSSH {
		t.Fatal("tailscaled does not hold RunSSH after the change")
	}
	if st := m5Status(t, v); !st.RunSSH {
		t.Error("tailscale.status reports runSSH false while Tailscale SSH is on")
	}

	g, err := m5PlanLink(ctx, n, "tailscale.prefs.set", shields)
	switch {
	case err != nil:
		t.Fatal(err)
	case g.OK:
		t.Errorf("tailscale.link passed shieldsUp with Tailscale SSH on: %q", g.Message)
	case !g.Overridable || g.Message == "":
		t.Errorf("tailscale.link refusal %+v, want an overridable guard with a message", g)
	}
	g, err = m5PlanLink(ctx, n, "tailscale.prefs.set", map[string]any{"shieldsUp": true, "overrides": []string{"tailscale.link"}})
	if err != nil {
		t.Fatal(err)
	}
	if !g.OK || !g.Overridden {
		t.Errorf("tailscale.link with the override listed: %+v, want ok and overridden", g)
	}

	if _, err := n.api.m3Revert(ctx, ch.ID); err != nil {
		t.Fatal(err)
	}
	reverted = true
	m3WaitChange(ctx, t, n.api, ch.ID, core.StateReverted, 2*time.Minute)
	if m5HostPrefs(ctx, t, v).RunSSH {
		t.Error("tailscaled still holds RunSSH after the revert")
	}
	g, err = m5PlanLink(ctx, n, "tailscale.prefs.set", shields)
	if err != nil {
		t.Fatal(err)
	}
	if !g.OK {
		t.Errorf("tailscale.link refused shieldsUp with Tailscale SSH off: %q", g.Message)
	}
}

// m5NodeLatency: tailscale.latency lists the regions of the DERP map tailscaled
// answers, whatever it holds while logged out, with consistent measurements.
func m5NodeLatency(t *testing.T, v *vm.VM) {
	n := m5Lookup(t, v)
	ctx := n.ctx
	out := m3Run(ctx, t, v, "sudo curl -fsS --unix-socket "+tslocal.DefaultSocket+" http://local-tailscaled.sock/localapi/v0/derpmap")
	var m tslocal.DERPMap
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("the DERP map tailscaled answers: %v\n%s", err, out)
	}
	var l tailscale.Latency
	if err := n.api.Resource(ctx, n.hostID, "tailscale.latency", &l); err != nil {
		t.Fatal(err)
	}
	if err := m5xCheckLatency(l, m); err != nil {
		t.Errorf("tailscale.latency against a DERP map of %d regions: %v", len(m.Regions), err)
	}
	if len(l.Regions) > 0 && !m5xAnyMeasured(l) {
		t.Errorf("none of the %d regions has a STUN or HTTPS measurement; the VM reaches the internet, so one should: %s", len(l.Regions), m3JSON(l))
	}
	t.Logf("tailscale.latency: %d regions", len(l.Regions))
}

// m5NodeInstall: package.install refuses while the package is present; once
// the image's pinned package is removed, the resource reports tailscale.unavailable
// and package.install installs the repository's current package, its repository
// files and a running tailscaled that is logged out and not held.
func m5NodeInstall(t *testing.T, v *vm.VM) {
	n := m5Lookup(t, v)
	ctx := n.ctx
	const action = "tailscale.package.install"

	_, err := m4Plan(ctx, n.api, n.hostID, action, struct{}{})
	var ae *APIError
	if !errors.As(err, &ae) || ae.Code != "tailscale.package_present" {
		t.Fatalf("plan of %s with the package installed: %v, want code tailscale.package_present", action, err)
	}

	m3Run(ctx, t, v, "sudo DEBIAN_FRONTEND=noninteractive apt-get -o DPkg::Lock::Timeout=600 -y remove tailscale")
	err = eventually(ctx, time.Minute, func(ctx context.Context) error {
		_, err := m5ReadStatus(ctx, n)
		switch {
		case errors.As(err, &ae) && ae.Code == "tailscale.unavailable":
			return nil
		case err == nil:
			return errors.New("tailscale.status answered, want code tailscale.unavailable")
		}
		return fmt.Errorf("tailscale.status answered %w, want code tailscale.unavailable", err)
	})
	if err != nil {
		t.Fatal(err)
	}
	m3Run(ctx, t, v, "curl -sS --max-time 30 -o /dev/null https://pkgs.tailscale.com/")

	ch := m5Apply(t, v, action, struct{}{})
	var out tailscale.PackageInstalled
	if err := json.Unmarshal(ch.Output, &out); err != nil || out.Version == "" {
		t.Fatalf("output of %s: %s, %v; want the installed version", action, ch.Output, err)
	}
	if got := strings.TrimSpace(m3Run(ctx, t, v, "dpkg-query -W -f '${Version}' tailscale")); got != out.Version {
		t.Errorf("dpkg holds tailscale %q, the change reports %q", got, out.Version)
	}
	if err := eventually(ctx, 2*time.Minute, func(ctx context.Context) error {
		if state := unitState(ctx, v, "tailscaled"); state != "active" {
			return fmt.Errorf("tailscaled is %q", state)
		}
		st, err := m5ReadStatus(ctx, n)
		if err != nil {
			return err
		}
		return m5xLoggedOut(st, "")
	}); err != nil {
		t.Errorf("after the install: %v", err)
	}

	codename := strings.TrimSpace(m3Run(ctx, t, v, ". /etc/os-release && echo $VERSION_CODENAME"))
	if got, want := m3Run(ctx, t, v, "sudo cat /etc/apt/sources.list.d/tailscale.list"), m5xSourceLine(n.os.Family, codename); got != want {
		t.Errorf("tailscale.list is %q, want %q", got, want)
	}
	m3Run(ctx, t, v, "sudo test -s /usr/share/keyrings/tailscale-archive-keyring.gpg")
	if held := m3Run(ctx, t, v, "apt-mark showhold"); strings.Contains(held, "tailscale") {
		t.Errorf("apt-mark showhold lists tailscale after an install: %q", held)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `mkdir -p /root/.cache/w4-tmp/33 && export TMPDIR=/root/.cache/w4-tmp/33 GOTOOLCHAIN=local && timeout 300 go vet -tags harness ./test/harness/`
Expected: FAIL to build, `undefined: m5Lab` first, then the other helpers (`m5Register`, `m5Status`, `m5Configs`, ...).

- [ ] **Step 3: Implement**

Create `test/harness/m5_helpers_test.go` (`//go:build harness`, package `harness`) with these declarations. Every name and every error fragment below is pinned by the tests; the `m5x` functions are pure.

1. Flag and aliases. `var m5Flag = flag.Bool("m5", false, ...)`, a help text saying it runs the M5 node scenarios, needs `-os` and `-m2`, and that each M5 test is a `go test` run of its own. `const m5Deadline = 60 * time.Minute`. `type VM = vm.VM` and `type ChangeView = Change`, each with a doc comment saying they are the names Contracts "Harness M5" gives to `*vm.VM` and to the harness's change type.
2. The node registry. `type m5Node struct { ctx context.Context; api *API; hostID string; os OS }` (a comment on `ctx`: one per lab, for the helpers that take only the VM). `var m5Nodes sync.Map` keyed by `*vm.VM`. `m5Register(t, v, n)` stores `n` under `v` and deletes it in `t.Cleanup`. `m5Lookup(t, v) m5Node` returns it and calls `t.Fatal("harness: the VM is not the agent of an M5 lab; get it from m5Lab")` when absent. The helpers use `v` only as the key, so a test may register a `new(vm.VM)`.
3. `m5Configs(lab *Lab, o OS, targetImg, scannerImg string, key ssh.Signer) []vm.Config` (`ssh` is `golang.org/x/crypto/ssh`, already required by `test/harness/vm`) returns three configs, in this order, for the fleet of M2 on the lab's own links. Controller: `Name: lab.VMName("controller")`, `Image: targetImg`, 2048 MiB, 2 CPUs, `MgmtMAC/InetMAC/PrivMAC` = `lab.MAC(NICMgmt|NICInet|NICPriv, 10)`, `InetAddrs: targetIPv4+"/15", targetIPv6+"/64"`, `PrivAddrs: controllerPriv+"/24"`, `McastPort: lab.McastPort`, `PrivMcastPort: lab.PrivMcastPort`, `Provision: TargetScript(o)`, `Key: key`. Agent: the same with role `agent`, host number 11, `agentIPv4`, `agentIPv6`, `agentPriv`. Scanner: role `scanner`, `Image: scannerImg`, 2048 MiB, 1 CPU, host number 20, no private NIC (`PrivMAC`, `PrivAddrs`, `PrivMcastPort` unset), `scannerIPv4`, `scannerIPv6`, `Provision: ScannerScript()`. `Dir` stays empty: `Lab.Boot` sets it. The addresses are the fleet's because `scenarioInstall` and `serveReleases` use them; the lab's multicast ports keep two labs on separate links.
4. `m5Lab(t) (*Lab, *vm.VM)`: `t.Helper()`; unless `*osFlag`, `*m2Flag` and `*m5Flag` are all set, `t.Skip("no -os, -m2 or -m5 given: the VM tests run in .github/workflows/harness.yml")`. Then: a context with `m5Deadline` whose cancel is registered with `t.Cleanup` before anything boots (so it runs after the VMs stop); `lab := NewLab(t)`; `harnessImages(ctx, t)`; a key from `vm.NewKey()`; `lab.Boot(ctx, t, m5Configs(...))`; `f := &fleet{os: o, controller: vms[0], agent: vms[1], scanner: vms[2]}`; `serveReleases(ctx, t, f)`; `scenarioInstall(ctx, t, f)` (this installs the harness build on the controller and the agent, creates the admin, logs in, creates the host `agent` and enrolls it); `m5Register(t, f.agent, m5Node{ctx: ctx, api: f.api, hostID: f.agentID, os: o})`; return `lab, f.agent`.
5. `m5ReadStatus(ctx, n m5Node) (tailscale.Status, error)` reads the resource `tailscale.status` of `n.hostID` with `n.api.Resource`. `m5Status(t, v)` does it with the node's context and `t.Fatal`s on an error.
6. `m5Terminal(state string) bool` is `m4xTerminal(state)` or `state == core.StateConfirmed`. `m5Settle(ctx, api, id) (Change, error)` is `m4Settle` with `m5Terminal`: it polls `api.m3Change` with `eventually(ctx, m4SettleTimeout, ...)` and returns the change in the first state `m5Terminal` accepts.
7. `m5Apply(t, v, action, input) ChangeView`: plan version 1 of `action` on the node's host with `m4Plan`; `t.Fatal` on a plan error; `t.Fatalf("plan %s: guard %s failed: %s", action, g.ID, g.Message)` for the first guard with `OK` false; POST the apply (`actionPath(host, action+"/v1", "apply")`, `api.do`) with the body `{"planId": <id>, "confirmNow": true}` plus `"confirmation": <the plan's phrase>` when the plan names one, so a revertible change is confirmed by the same request; `m5Settle` the returned change id; `t.Fatalf` unless the settled state is `core.StateCompleted` or `core.StateConfirmed`, quoting the change's error code and message; return the settled change.
8. Preferences. `type m5Prefs struct{ WantRunning, RouteAll, ShieldsUp, RunSSH bool }` (the member names of `tailscale debug prefs`; `RouteAll` is accept-routes). `m5xParsePrefs(out string) (m5Prefs, error)` decodes the output into `map[string]json.RawMessage`, then each of the four members as a bool; an output that is not a JSON object returns an error containing `tailscale debug prefs`; a missing member returns an error containing `tailscale debug prefs has no member <name>`; a member that is not a bool returns an error naming it. `m5HostPrefs(ctx, t, v) m5Prefs` runs `sudo tailscale debug prefs` through `m3Run` and parses it, `t.Fatal`ing on an error.
9. `m5xPinned(o OS) (string, error)` is the `Version` of the package named `tailscale` in `o.Packages`, or an error when there is none.
10. `m5xLoggedOut(st tailscale.Status, version string) error` joins (`errors.Join`) one error per problem: `backend state %q, want NeedsLogin` unless the state is `NeedsLogin`; `self is %+v, want null while logged out` when `Self` is not nil; `tailnet %q, want none`; `%d peers, want none` for a non-empty `Peers`; `version %q, want one beginning %q` when `st.Version` is empty or does not begin with `version` (so `""` accepts any non-empty version). It returns nil when there is no problem.
11. Latency. `m5xHasSTUN(r *tslocal.DERPRegion) bool`: some node is non-nil, has `STUNPort != -1` and an `IPv4` or `IPv6`. `m5xMeasurement(m tailscale.Measurement) error`: an available measurement needs `MedianMs > 0` and an empty `Reason`, else `available with median %v ms and reason %q`; an unavailable one needs `MedianMs == 0` and a non-empty `Reason`, else `unavailable with median %v ms and reason %q`. `m5xCheckLatency(l tailscale.Latency, m tslocal.DERPMap) error`: the region ids of `l` must equal the sorted keys of `m.Regions`, in that ascending order, else the single error `tailscale.latency lists regions %v, want the DERP map's %v in ascending order`; then, joined, per region of `l` (skipping a nil map region): `region %d is %q %q, the DERP map says %q %q` when `Code` or `Name` differ from the map's `RegionCode` or `RegionName`; `region %d STUN: %w` and `region %d HTTPS: %w` wrapping `m5xMeasurement`'s error; `region %d reports STUN latency but has no STUN endpoint` when the STUN measurement is available and `m5xHasSTUN` is false. `m5xAnyMeasured(l) bool` is true when some region has an available STUN or HTTPS measurement.
12. `m5xSourceLine(family, codename string) string` returns `"deb [signed-by=/usr/share/keyrings/tailscale-archive-keyring.gpg] https://pkgs.tailscale.com/stable/" + family + " " + codename + " main\n"`.
13. `m5xLinkGuard(guards []core.Guard) (core.Guard, error)` returns the guard with id `tailscale.link`, or the error `harness: the plan has no tailscale.link guard: <m3JSON(guards)>`. `m5PlanLink(ctx, n m5Node, action string, input any) (core.Guard, error)` plans version 1 of `action` on `n.hostID` with `m4Plan` (nothing is applied) and returns `m5xLinkGuard` of its guards.
14. Imports, doc comments on the exported names, `gofmt` and `goimports` clean, and no `//nolint`.

- [ ] **Step 4: Run test to verify it passes**

Run: `timeout 300 go vet -tags harness ./test/...`
Expected: no output.

Run: `timeout 300 go test -tags harness -count=1 -v -run 'M5' ./test/harness/`
Expected: `--- SKIP: TestM5Node` and `--- PASS` for the ten `TestM5Unit...` tests (`Register`, `Configs`, `Apply`, `Status`, `ParsePrefs`, `Pinned`, `LoggedOut`, `CheckLatency`, `SourceLine`, `LinkGuard`).

Run: `timeout 300 go test -tags harness -race -count=1 -run 'Unit' ./test/harness/...`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add test/harness/m5_helpers_test.go test/harness/m5_test.go test/harness/m5_unit_test.go
git commit -m "test(harness): add TestM5Node node scenarios"
```

**Verify:** `mkdir -p /root/.cache/w4-tmp/33 && export TMPDIR=/root/.cache/w4-tmp/33 GOTOOLCHAIN=local && gofmt -l test/harness && timeout 300 go build ./... && timeout 300 go vet ./... && timeout 300 go vet -tags harness ./test/... && timeout 300 go test -tags harness -count=1 -run 'Unit' ./test/harness/... && timeout 900 go test -count=1 ./... && timeout 900 golangci-lint run --allow-serial-runners ./... && timeout 900 golangci-lint run --allow-serial-runners --build-tags harness ./test/... && timeout 300 go mod tidy -diff`; `gofmt -l` prints nothing; then `rm -rf /root/.cache/w4-tmp/33 && go clean -testcache`. Gate 7 (the `vm-m5` job of Task 35) runs the scenarios on the VMs.

### Task 34: harness: pin rollback with the controller link blocked

**Files:**
- Create: `test/harness/m5_pin_test.go`
- Create: `test/harness/m5_pin_unit_test.go`

**Interfaces:**
- Consumes: from Task 33 (Contracts "Harness M5"): the flag `-m5`, which `TestM5Node`'s file registers under the `harness` build tag; this task finds it by name (`flag.Lookup("m5")`) and so declares no variable for it. Contracts' `m5Lab`, `m5Apply` and `m5Status` are not used: they hand out no controller API client, and this scenario must plan and apply a pin without waiting for it, read the host list, and cut the agent's link to the controller, so it boots the fleet that `TestM4` boots. From the harness as it is on `main`: `bootFleet`, `serveReleases`, `scenarioInstall`, `fleet` (`controller`, `agent *vm.VM`, `api *API`, `localID`, `agentID`), `waitHosts`, `hostsAt`, `versionN`, `osFlag`, `m2Flag`, `eventually`, `unitState`, `m3Run`, `m3Quote`, `m3dxShell`, `m4Plan`, `m4Settle`, `m4ApplyAsync`, `actionPath`, `API.do`, `API.Resource`, `Change`. From the module (Contracts "Tailscale wire shapes", "Tailscale internals"): `tailscale.PackageInstallInput`, `tailscale.PinInput`, `tailscale.Status` from `github.com/darkraise/darkpanel/internal/modules/tailscale`; the registered action `tailscale.package.install/v1` and `tailscale.package.pin/v1`; the resource `tailscale.status`; the agent tables `tailscale_pins` and `tailscale_pin_steps` (columns and phases as Contracts lists them). Also `tslocal.DefaultSocket`, `update.AgentUnit` and `paths.Production().AgentDB()`.
- Produces: `TestM5Pin`; `m5pDeadline`, `m5pStep` and `m5pSteps`, whose table Task 35 extends by one row. Names in Go start with `m5p` only (Contracts "Test prefixes"), except `TestM5Pin`, which Contracts names.

**Items:** 21

**Depends on:** Task 33

- [ ] **Step 1: Write the failing tests**

Create `test/harness/m5_pin_unit_test.go`. None of these tests needs a VM. `TestM5pUnitWrapper` runs the apt-mark wrapper under `sh` with three stand-in programs, each named by absolute path inside the test's temporary directory, so neither `iptables` nor `systemctl` of this host ever runs. `TestM5pUnitFlag` needs Task 33's `-m5` flag, so it fails on a tree without it.

```go
//go:build harness

package harness

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/paths"
)

func TestM5pUnitTarget(t *testing.T) {
	const source = "https://pkgs.tailscale.com/stable/ubuntu jammy/main amd64 Packages"
	line := func(pkg, version string) string { return fmt.Sprintf(" %s | %s | %s\n", pkg, version, source) }
	cases := []struct {
		name, madison, installed string
		want                     string // "" when an error is expected
	}{
		{"the newest other version", line("tailscale", "1.104.1") + line("tailscale", "1.102.0") + line("tailscale", "1.100.0"), "1.104.1", "1.102.0"},
		{"installed is not the newest", line("tailscale", "1.106.0") + line("tailscale", "1.104.1"), "1.104.1", "1.106.0"},
		{"installed is not listed", line("tailscale", "1.106.0"), "1.104.1", "1.106.0"},
		{
			"one version on two architectures",
			line("tailscale", "1.104.1") + strings.Replace(line("tailscale", "1.104.1"), "amd64", "arm64", 1) + line("tailscale", "1.102.0"),
			"1.104.1", "1.102.0",
		},
		{"another package is ignored", line("tailscale-archive-keyring", "1.35.181") + line("tailscale", "1.104.1"), "1.104.1", ""},
		{"a version the pin refuses is skipped", line("tailscale", "1.2 beta") + line("tailscale", "1:1.3") + line("tailscale", "1.102.0"), "1.104.1", "1.102.0"},
		{"only the installed version", line("tailscale", "1.104.1"), "1.104.1", ""},
		{"nothing listed", "", "1.104.1", ""},
	}
	for _, c := range cases {
		got, err := m5pTarget(c.madison, c.installed)
		switch {
		case c.want == "" && (err == nil || !strings.Contains(err.Error(), "no tailscale version other than the installed "+c.installed)):
			t.Errorf("%s: m5pTarget = %q, %v, want an error naming the installed version", c.name, got, err)
		case c.want != "" && (err != nil || got != c.want):
			t.Errorf("%s: m5pTarget = %q, %v, want %q", c.name, got, err, c.want)
		}
	}
}

func TestM5pUnitPinRows(t *testing.T) {
	sha := strings.Repeat("ab", 32)
	out := "01JOTHER|done|1.100.0|1.099.0|0|NeedsLogin|\n" +
		"01JPIN|rolled_back|1.102.0|1.104.1|1|NeedsLogin|" + sha + "\n"
	got, err := m5pParsePin(out, "01JPIN")
	want := m5pPin{Phase: "rolled_back", Version: "1.102.0", PriorVersion: "1.104.1", PriorHeld: true, PriorBackend: "NeedsLogin", StagedSHA256: sha}
	if err != nil || got != want {
		t.Errorf("m5pParsePin = %+v, %v, want %+v", got, err, want)
	}
	got, err = m5pParsePin("01JPIN|held|1.102.0|1.104.1|0|Running|\n", "01JPIN")
	if err != nil || got.Phase != "held" || got.PriorHeld || got.StagedSHA256 != "" {
		t.Errorf("m5pParsePin of an unstaged row = %+v, %v", got, err)
	}

	for name, tc := range map[string]struct{ out, id string }{
		"empty output":         {"", "01JPIN"},
		"a blank line":         {"\n", "01JPIN"},
		"only another change":  {"01JOTHER|done|1.100.0|1.099.0|0|NeedsLogin|\n", "01JPIN"},
		"an id that is a stem": {"01JPINX|done|1.100.0|1.099.0|0|NeedsLogin|\n", "01JPIN"},
	} {
		if _, err := m5pParsePin(tc.out, tc.id); !errors.Is(err, errM5pNoRow) {
			t.Errorf("%s: m5pParsePin = %v, want errM5pNoRow", name, err)
		}
	}
	for name, tc := range map[string]struct{ out, want string }{
		"too few fields":      {"01JPIN|held|1.102.0|1.104.1|0|Running\n", "7 fields"},
		"a bad hold flag":     {"01JPIN|held|1.102.0|1.104.1|2|Running|\n", "prior_held"},
		"an unknown phase":    {"01JPIN|paused|1.102.0|1.104.1|0|Running|\n", "phase"},
		"an empty prior":      {"01JPIN|held|1.102.0||0|Running|\n", "prior_version"},
		"an empty version":    {"01JPIN|held||1.104.1|0|Running|\n", "version"},
		"a malformed sibling": {"broken\n01JPIN|held|1.102.0|1.104.1|0|Running|\n", "7 fields"},
	} {
		if _, err := m5pParsePin(tc.out, "01JPIN"); err == nil || errors.Is(err, errM5pNoRow) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: m5pParsePin = %v, want an error containing %q", name, err, tc.want)
		}
	}
}

func TestM5pUnitStepLines(t *testing.T) {
	out := "01JOTHER|1|unhold|ok\n01JPIN|1|unhold|ok\n01JPIN|2|rollback apt-mark unhold|ok\n\n01JPIN|3|restore hold|failed: exit 100\n"
	want := []string{"1 unhold: ok", "2 rollback apt-mark unhold: ok", "3 restore hold: failed: exit 100"}
	if got := m5pStepLines(out, "01JPIN"); !slices.Equal(got, want) {
		t.Errorf("m5pStepLines = %q, want %q", got, want)
	}
	if got := m5pStepLines(out, "01JNONE"); len(got) != 0 {
		t.Errorf("m5pStepLines of an unknown change = %q, want none", got)
	}
}

func TestM5pUnitSQL(t *testing.T) {
	db := paths.Production().AgentDB()
	for name, tc := range map[string]struct{ got, query string }{
		"pins":  {m5pPinsCommand(), m5pPinsSQL},
		"steps": {m5pStepsCommand(), m5pStepsSQL},
	} {
		want := "sudo sqlite3 -separator '|' " + db + " " + m3Quote(tc.query)
		if tc.got != want {
			t.Errorf("%s command =\n%s\nwant\n%s", name, tc.got, want)
		}
		if strings.Contains(tc.query, "'") {
			t.Errorf("%s query %q holds a quote; the change id is matched in Go, never put in SQL", name, tc.query)
		}
	}
}

func TestM5pUnitSettled(t *testing.T) {
	for _, state := range []string{core.StateFailed, core.StateReverted} {
		if err := m5pSettled(state); err != nil {
			t.Errorf("m5pSettled(%q) = %v, want it accepted", state, err)
		}
	}
	for _, state := range []string{core.StateCompleted, core.StateNeedsRepair, core.StateInterrupted, core.StateApplying, ""} {
		err := m5pSettled(state)
		if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("%q", state)) {
			t.Errorf("m5pSettled(%q) = %v, want an error naming the state", state, err)
		}
	}
}

func TestM5pUnitFlag(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	if registered, on := m5pFlagState(fs); registered || on {
		t.Errorf("an empty set = (%t, %t), want (false, false)", registered, on)
	}
	fs.Bool("m5", false, "")
	if registered, on := m5pFlagState(fs); !registered || on {
		t.Errorf("-m5 not given = (%t, %t), want (true, false)", registered, on)
	}
	if err := fs.Parse([]string{"-m5"}); err != nil {
		t.Fatal(err)
	}
	if registered, on := m5pFlagState(fs); !registered || !on {
		t.Errorf("-m5 given = (%t, %t), want (true, true)", registered, on)
	}
	if registered, _ := m5pFlagState(flag.CommandLine); !registered {
		t.Error("the harness registers no -m5 flag; TestM5Node declares it, and TestM5Pin skipping without it would hide a missing job flag")
	}
}

func TestM5pUnitSteps(t *testing.T) {
	steps := m5pSteps(context.Background(), nil)
	want := []struct {
		name string
		gate bool
	}{{"install", true}, {"package-install", true}, {"pin-rollback", false}}
	if len(steps) < len(want) {
		t.Fatalf("m5pSteps has %d steps, want at least %d", len(steps), len(want))
	}
	seen := map[string]bool{}
	for i, s := range steps {
		if s.run == nil || seen[s.name] {
			t.Errorf("step %d %q has no run function or repeats a name", i, s.name)
		}
		seen[s.name] = true
		if i < len(want) && (s.name != want[i].name || s.gate != want[i].gate) {
			t.Errorf("step %d = %q (gate %t), want %q (gate %t)", i, s.name, s.gate, want[i].name, want[i].gate)
		}
	}
	if m5pDeadline <= 0 {
		t.Errorf("m5pDeadline = %v, want a positive bound", m5pDeadline)
	}
}

func TestM5pUnitCutRule(t *testing.T) {
	for op, want := range map[string]string{
		"-C": "sudo iptables -C OUTPUT -o dppriv -j DROP",
		"-D": "sudo iptables -D OUTPUT -o dppriv -j DROP",
	} {
		if got := m5pIptables(op); got != want {
			t.Errorf("m5pIptables(%q) = %q, want %q", op, got, want)
		}
	}
}

// TestM5pUnitWrapper runs the apt-mark wrapper under sh with stand-ins for
// the real apt-mark, iptables and systemctl, all named by absolute path in a
// temporary directory, so no program of this host runs.
func TestM5pUnitWrapper(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh on this host")
	}
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	stub := func(name string, exit int) string {
		p := filepath.Join(dir, name)
		body := "#!/bin/sh\necho " + m3Quote(name) + ` "$@" >> ` + m3Quote(calls) + "\nexit " + strconv.Itoa(exit) + "\n"
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil { //nolint:gosec // G306: a stand-in program the test runs
			t.Fatal(err)
		}
		return p
	}
	hooks := m5pHooks{Real: stub("real", 0), Flag: filepath.Join(dir, "flag"), Iptables: stub("iptables", 0), Systemctl: stub("systemctl", 0)}
	wrapper := filepath.Join(dir, "wrapper")
	if err := os.WriteFile(wrapper, []byte(m5pWrapper(hooks)), 0o755); err != nil { //nolint:gosec // G306: the script under test
		t.Fatal(err)
	}

	cut := []string{"real hold tailscale", "iptables -I OUTPUT 1 " + m5pCutRule, "systemctl stop tailscaled.service"}
	cases := []struct {
		name     string
		args     []string
		flag     bool
		realExit int
		runs     int
		want     []string
		wantExit int
		flagKept bool
	}{
		{"hold with the flag cuts the link, then stops tailscaled", []string{"hold", "tailscale"}, true, 0, 1, cut, 0, false},
		{"it fires once", []string{"hold", "tailscale"}, true, 0, 2, append(append([]string{}, cut...), "real hold tailscale"), 0, false},
		{"hold without the flag passes through", []string{"hold", "tailscale"}, false, 0, 1, []string{"real hold tailscale"}, 0, false},
		{"unhold leaves the flag", []string{"unhold", "tailscale"}, true, 0, 1, []string{"real unhold tailscale"}, 0, true},
		{"showhold leaves the flag", []string{"showhold"}, true, 0, 1, []string{"real showhold"}, 0, true},
		{"another argument leaves the flag", []string{"hold", "tailscale", "extra"}, true, 0, 1, []string{"real hold tailscale extra"}, 0, true},
		{"a failed hold keeps its status and the flag", []string{"hold", "tailscale"}, true, 3, 1, []string{"real hold tailscale"}, 3, true},
	}
	for _, c := range cases {
		stub("real", c.realExit)
		_ = os.Remove(calls)
		_ = os.Remove(hooks.Flag)
		if c.flag {
			if err := os.WriteFile(hooks.Flag, nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		for i := 0; i < c.runs; i++ {
			cmd := exec.Command(wrapper, c.args...) //nolint:gosec // G204: the script the test wrote
			cmd.Env = []string{"PATH=/usr/bin:/bin"}
			out, err := cmd.CombinedOutput()
			exit := 0
			var ee *exec.ExitError
			switch {
			case errors.As(err, &ee):
				exit = ee.ExitCode()
			case err != nil:
				t.Fatalf("%s: %v", c.name, err)
			}
			if i == 0 && exit != c.wantExit {
				t.Errorf("%s: exit status %d, want %d (output %q)", c.name, exit, c.wantExit, out)
			}
		}
		b, _ := os.ReadFile(calls) //nolint:gosec // G304: a file in the test's temporary directory
		if got := strings.Split(strings.TrimSpace(string(b)), "\n"); !slices.Equal(got, c.want) {
			t.Errorf("%s: calls =\n%q\nwant\n%q", c.name, got, c.want)
		}
		_, err := os.Stat(hooks.Flag)
		if kept := err == nil; kept != c.flagKept {
			t.Errorf("%s: flag kept = %t, want %t", c.name, kept, c.flagKept)
		}
	}
}

func TestM5pUnitHeld(t *testing.T) {
	for name, tc := range map[string]struct {
		out  string
		want bool
	}{
		"held":                   {"tailscale\n", true},
		"held among others":      {"docker-ce\ntailscale\n", true},
		"padded":                 {"  tailscale  \n", true},
		"none":                   {"", false},
		"a package of that stem": {"tailscale-archive-keyring\n", false},
	} {
		if got := m5pHeldIn(tc.out); got != tc.want {
			t.Errorf("%s: m5pHeldIn(%q) = %t, want %t", name, tc.out, got, tc.want)
		}
	}
}

func TestM5pUnitBackend(t *testing.T) {
	if got, err := m5pBackendState(`{"Version":"1.104.1","BackendState":"NeedsLogin","Peer":{}}`); err != nil || got != "NeedsLogin" {
		t.Errorf("m5pBackendState = %q, %v, want NeedsLogin", got, err)
	}
	for name, out := range map[string]string{
		"not JSON":       "Logged out.",
		"no state":       `{"Version":"1.104.1"}`,
		"an empty state": `{"BackendState":""}`,
		"empty":          "",
	} {
		if got, err := m5pBackendState(out); err == nil {
			t.Errorf("%s: m5pBackendState = %q, want an error", name, got)
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
mkdir -p /root/.cache/w4-tmp/34 && export TMPDIR=/root/.cache/w4-tmp/34 GOTOOLCHAIN=local
timeout 300 go vet -tags harness ./test/harness/
```

Expected: FAIL to build, `undefined: m5pTarget` among the first errors (then `m5pParsePin`, `errM5pNoRow`, `m5pStepLines`, `m5pPinsCommand`, `m5pSettled`, `m5pFlagState`, `m5pSteps`, `m5pDeadline`, `m5pIptables`, `m5pHooks`, `m5pWrapper`, `m5pHeldIn`, `m5pBackendState`).

- [ ] **Step 3: Implement**

Create `test/harness/m5_pin_test.go` (first line `//go:build harness`, package `harness`). The pure helpers come first; the VM scenario after them. Nothing runs at package initialisation and nothing reads `f` while the step table is built, because `TestM5pUnitSteps` builds it with a nil fleet.

*Why the scenario is built this way (spec 4.1, the harness requirement of the pin).* A pin has a health check of 120 s after phase 5 (`held`). On a harness host the agent's link runs over the private NIC `dppriv`, not over a tailnet, so the check cannot fail through the link; it fails when tailscaled does not answer. The scenario therefore makes tailscaled stop exactly when `apt-mark hold tailscale` succeeds (phase 5), and at the same instant cuts the agent's link to the controller, so the 120 s check fails and the whole rollback runs with no controller. The hook is a wrapper that replaces `/usr/bin/apt-mark` (the one path the exec allowlist runs) for the test's duration, the way `m3PredictionMismatch` replaces `/usr/sbin/ufw`. The cut is an `iptables` rule at position 1 of the `OUTPUT` chain: UFW accepts established connections before its user rules, so a `ufw deny out` would leave the live session open.

*Helpers, each with the exact behaviour the tests pin.*
- `const m5pDeadline = 35 * time.Minute`: the bound of `TestM5Pin`, with a comment that the workflow's `-timeout` for this test must stay at least 10 minutes above it.
- `type m5pStep struct { name string; gate bool; run func(t *testing.T) }`.
- `m5pSteps(ctx context.Context, f *fleet) []m5pStep` returns, in this order and with closures that capture `ctx` and `f` and read neither before they run: `install` (gate) running `scenarioInstall(ctx, t, f)`, `package-install` (gate) running `m5pPackage(ctx, t, f)`, `pin-rollback` (not a gate) running `m5pRollback(ctx, t, f)`.
- `m5pTarget(madison, installed string) (string, error)` reads `apt-cache madison tailscale` output. For each line, split on `|`; use lines with at least three fields whose first field, trimmed, is exactly `tailscale`; the version is the second field, trimmed. Skip a version equal to `installed` or not matching `^[0-9][0-9A-Za-z.+~-]{0,63}$` (the pin's input pattern, Contracts). Return the first one left (madison lists the newest first). With none left, return the error `harness: apt-cache madison lists no tailscale version other than the installed <installed>` followed by a newline and the madison output.
- `type m5pPin struct { Phase, Version, PriorVersion, PriorBackend, StagedSHA256 string; PriorHeld bool }` and `var errM5pNoRow = errors.New("harness: no tailscale_pins row")`.
- `const m5pPinsSQL = "SELECT change_id, phase, version, prior_version, prior_held, prior_backend, staged_sha256 FROM tailscale_pins ORDER BY updated_at DESC"` and `const m5pStepsSQL = "SELECT change_id, seq, step, result FROM tailscale_pin_steps ORDER BY change_id, seq"`. Neither holds a quote: the change id is matched in Go.
- `m5pPinsCommand()` and `m5pStepsCommand()` return `sudo sqlite3 -separator '|' <paths.Production().AgentDB()> <query>` with the database path and the query passed through `m3dxShell` (which single-quotes the query).
- `m5pParsePin(out, changeID string) (m5pPin, error)` splits `out` into lines, skips blank ones, and splits each on `|`. A line that does not have exactly 7 fields is an error containing `7 fields` and the line, whatever change it belongs to. A line whose first field is not `changeID` is otherwise ignored. For the matching line: field 5 (`prior_held`) must be `0` or `1` (else an error containing `prior_held`); the phase must be one of the ten phases of `tailscale_pins` (Contracts "Tailscale internals": `recorded`, `unheld`, `staged`, `installed`, `held`, `checking`, `done`, `rolling_back`, `rolled_back`, `failed`; else an error containing `phase`); `version` and `prior_version` must not be empty (else an error naming the empty column). With no matching line it returns `errM5pNoRow` itself.
- `m5pStepLines(out, changeID string) []string` splits each line into at most 4 `|` parts and, for the lines whose first part is `changeID`, returns `<seq> <step>: <result>`; other and malformed lines are skipped.
- `m5pSettled(state string) error` is nil for `core.StateFailed` and `core.StateReverted` (a rolled-back pin settles as `failed` through the engine's failure table, or `reverted` through the recovery handler's `rolled_back`, spec 1.3b and 1.3c) and otherwise the error `harness: the change ended "<state>", want failed or reverted`, with the state formatted by `%q`.
- `m5pFlagState(fs *flag.FlagSet) (registered, on bool)`: `registered` is whether `fs` has a flag `m5`; `on` is whether its value is `true`.
- `const m5pCutRule = "-o dppriv -j DROP"` (`dppriv` is the private NIC the vm package names, `provision.go`) and `m5pIptables(op string) string` returning `sudo iptables <op> OUTPUT <m5pCutRule>`.
- `type m5pHooks struct{ Real, Flag, Iptables, Systemctl string }` and `m5pWrapper(h m5pHooks) string`, the text of a POSIX `sh` script (first line `#!/bin/sh`), every path passed through `m3dxShell`: run `h.Real` with all the script's arguments and keep its status; when that status is 0, the arguments joined with a space are exactly `hold tailscale`, and the file `h.Flag` exists, then remove `h.Flag`, run `h.Iptables -I OUTPUT 1 <m5pCutRule>` (the rule's words unquoted, so they are separate arguments), then run `h.Systemctl stop tailscaled.service`; finally exit with the real status. The flag is removed before the effects so the rollback's own `apt-mark hold tailscale` (which restores a prior hold) passes straight through. A production value `m5pProductionHooks` holds `Real` `/usr/bin/apt-mark.distrib`, `Flag` `/run/m5-pin-fail`, `Iptables` `iptables` and `Systemctl` `systemctl` (the agent's runner sets `PATH=/usr/sbin:/usr/bin:/sbin:/bin`).
- `m5pHeldIn(out string) bool`: whether some line of `apt-mark showhold` output is, trimmed, exactly `tailscale`.
- `m5pBackendState(out string) (string, error)`: decodes tailscaled's local API status JSON and returns its `BackendState`; invalid JSON or an empty state is an error naming the output.

*The test and its flag.* `TestM5Pin(t)`: skip with `no -os, -m2 or -m5 given: the VM tests run in .github/workflows/harness.yml` unless `*osFlag` and `*m2Flag` are non-empty and `m5pFlagState(flag.CommandLine)` reports `-m5` on; when `-os` and `-m2` are given and the flag is not registered at all, fail with `harness: the -m5 flag is not registered; TestM5Node declares it`. Then, as `TestM4` does: a context with `m5pDeadline`, `f := bootFleet(ctx, t)`, `serveReleases(ctx, t, f)`, and a loop over `m5pSteps(ctx, f)` that returns when a gate step fails (`if !t.Run(s.name, s.run) && s.gate`).

*Step `package-install`: `m5pPackage(ctx, t, f)`.* The VMs boot with tailscaled from a pinned `.deb`, so the Tailscale repository is not configured; installing through Darkpanel is what writes it, and the pin's staging needs it.
1. Wait up to 3 minutes (`eventually`) until `f.api.Resource(ctx, f.agentID, "tailscale.status", &st)` into a `tailscale.Status` answers with a non-empty `Version` (the module is registered and tailscaled answers).
2. Remove the pre-installed package on the agent VM with `m3Run`: `sudo env DEBIAN_FRONTEND=noninteractive apt-get -o DPkg::Lock::Timeout=600 -y remove tailscale`.
3. `m4ApplyAsync(ctx, t, f.api, f.agentID, "tailscale.package.install", tailscale.PackageInstallInput{})`: plans, applies, waits for the change and fails the test unless it completed. Log first that this step needs `pkgs.tailscale.com`, so a failure names what was unreachable.
4. Wait again, up to 3 minutes, for `tailscale.status` to answer (tailscaled came back with the new package).
5. Hold the package by hand on the agent VM (`sudo apt-mark hold tailscale`, through `m3Run`) so the pin records a prior hold of 1, which the rollback must restore. Require `m5pHeldIn` of `apt-mark showhold` to be true.

*Step `pin-rollback`: `m5pRollback(ctx, t, f)`.*
1. `installed` is `dpkg-query -W -f='${Version}' tailscale` on the agent VM, trimmed (through `m3Run`), and must not be empty. `target` is `m5pTarget` of `apt-cache madison tailscale` (through `m3Run`) and `installed`; an error fails the test with madison's output, which is how a repository that lists a single version shows. Log both.
2. Arm the hook with `m5pArm(ctx, t, f.agent)`, which first registers a `t.Cleanup` that runs, on the agent VM with a context of its own that ends after one minute, `sudo sh -c` of: remove `/run/m5-pin-fail`; delete the cut rule with `iptables -D OUTPUT -o dppriv -j DROP` in a loop until it fails; and, when `/usr/bin/apt-mark.distrib` exists, remove `/usr/bin/apt-mark` and run `dpkg-divert --local --rename --remove /usr/bin/apt-mark`. Then it runs `sudo dpkg-divert --local --rename --add /usr/bin/apt-mark`, writes `m5pWrapper(m5pProductionHooks)` to `/usr/bin/apt-mark` through `RunInput` (`sudo tee /usr/bin/apt-mark >/dev/null && sudo chmod 0755 /usr/bin/apt-mark`), and creates `/run/m5-pin-fail` with `sudo touch`.
3. Register a `t.Cleanup` that, when the test failed, logs the output of `m5pStepsCommand()` and of `sudo journalctl -u <update.AgentUnit> -u 'darkpanel-task-*' --no-pager -n 150` on the agent VM.
4. Start the pin without waiting for it: `m4Plan(ctx, f.api, f.agentID, "tailscale.package.pin", tailscale.PinInput{Version: target})`, then a POST of `{"planId": <id>}` (plus `confirmation` when the plan names one) to `actionPath(f.agentID, "tailscale.package.pin/v1", "apply")` through `f.api.do` into a `Change`. A refused plan or apply fails the test.
5. Wait up to 8 minutes (`eventually`; the check window is 120 s and the rollback follows) until `m5pParsePin` of `m5pPinsCommand()`'s output on the agent VM, for the change's id, shows a phase of `rolled_back`, `failed` or `done`. `errM5pNoRow` and a failing `sqlite3` run mean keep waiting.
6. Require the row: phase `rolled_back` (anything else fails with the phase and `m5pStepLines` of `m5pStepsCommand()`'s output); `Version` equal to `target`; `PriorVersion` equal to `installed`; `PriorHeld` true; `StagedSHA256` matching `^[0-9a-f]{64}$` (the prior package was staged and checked); at least one `m5pStepLines` entry (the rollback was journaled). Log the steps.
7. While the link is still cut, require on the agent VM: `/run/m5-pin-fail` is gone (the hook fired after the real `apt-mark hold`); `m5pIptables("-C")` succeeds; the installed version equals `installed`; `m5pHeldIn` of `apt-mark showhold` is true (the prior hold state is back); `unitState(ctx, f.agent, "tailscaled")` is `active`; and, within 30 s (`eventually`), `m5pBackendState` of `sudo curl -fsS --max-time 10 --unix-socket <tslocal.DefaultSocket> http://local-tailscaled.sock/localapi/v0/status` equals the row's `PriorBackend` (tailscaled answers in its prior backend state).
8. Lift the cut with `m3Run(ctx, t, f.agent, m5pIptables("-D"))`. Wait up to 5 minutes with `waitHosts(ctx, t, f, 5*time.Minute, hostsAt(versionN, f.localID, f.agentID))` (the agent's link is back). `m4Settle(ctx, f.api, change.ID)` must return, and `m5pSettled(ch.State)` must be nil; log `ch.Error` when set.
9. Within 2 minutes (`eventually`), `f.api.Resource(ctx, f.agentID, "tailscale.status", &st)` answers with `st.BackendState` equal to the row's `PriorBackend`.

Do not edit `m5_node_test.go`, `m4_*` files or the workflow. `TestM5Pin` is never run on this host: it needs KVM and the harness images, and runs only in `.github/workflows/harness.yml` (Task 35 adds its job).

- [ ] **Step 4: Run the tests to verify they pass**

```bash
mkdir -p /root/.cache/w4-tmp/34 && export TMPDIR=/root/.cache/w4-tmp/34 GOTOOLCHAIN=local
timeout 300 go vet -tags harness ./test/...
timeout 300 go test -tags harness -count=1 -run 'TestM5p' -v ./test/harness/
timeout 300 go test -tags harness -count=1 -run 'Unit' ./test/harness/...
timeout 600 go test -race -tags harness -count=1 ./test/harness/...
timeout 300 go test -tags harness -count=1 -run '^TestM5Pin$' -v ./test/harness/
timeout 900 golangci-lint run --allow-serial-runners --build-tags harness ./test/...
```

Expected: every `TestM5pUnit*` passes; the `TestM5Pin` run prints `--- SKIP: TestM5Pin` (no `-os` given); the lint run prints `0 issues.`. Then the checks every task runs (`go build ./...`, `go vet ./...`, `go test -count=1 ./...`, `golangci-lint run --allow-serial-runners ./...`, `go mod tidy -diff`), with their real output recorded. `git status` shows only the two new files. Remove `/root/.cache/w4-tmp/34` and run `go clean -testcache`.

- [ ] **Step 5: Commit**

```bash
git add test/harness/m5_pin_test.go test/harness/m5_pin_unit_test.go
git commit -m "test(harness): roll back a pin with the link cut"
```

### Task 35: harness: TestTailscaledLive and the M5 workflow job

**Files:**
- Create: `test/harness/m5_live_test.go`
- Create: `test/harness/m5_live_unit_test.go`
- Modify: `test/harness/m5_pin_test.go` (the `m5pSteps` table: one row after Task 34's last row)
- Modify: `.github/workflows/harness.yml`

**Interfaces:**
- Consumes: from Task 34: `m5pSteps`, `m5pStep` and `m5pDeadline` (`m5_pin_test.go`). From Task 33 (Contracts "Harness M5"): the test names `TestM5Node` and the flag `-m5`, which the new job passes. From the harness as it is on `main`: `fleet` (`controller`, `agent *vm.VM`), `m3Run`, `m3dxShell`, `unitState`, `m3dxExpoSteps`, `m3dxExpoJob`, `m3dxExpoTimeoutRE`, `m3dxExpoLimitRE`, `m4lStep` and `m4lSetup` (`m4_live_test.go`). `tslocal.DefaultSocket`; `TestTailscaledLive` and `DARKPANEL_TAILSCALED_SOCKET` in `internal/platform/tslocal/live_test.go`, unchanged (the package is frozen).
- Produces: the flag `m5lBinaryFlag` (`-tslocal-test`, the path of the compiled tslocal test binary), the step `tailscaled-live` of `TestM5Pin`, the build step `Build the tslocal live test` that writes `bin/harness/tslocal.test`, and the workflow job `vm-m5` that runs `TestM5Node` and `TestM5Pin` for each OS (Contracts "Harness M5"). The `vm` and `vm-m4` jobs are not changed. Names in Go start with `m5l` only.

**Items:** 251

**Depends on:** Task 34

- [ ] **Step 1: Write the failing tests**

Create `test/harness/m5_live_unit_test.go`. `TestM5lUnitWorkflowFile` reads the committed `.github/workflows/harness.yml`, so it fails until the workflow is edited:

```go
//go:build harness

package harness

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/platform/tslocal"
)

func TestM5lUnitCommand(t *testing.T) {
	got := m5lCommand("/var/tmp/tslocal.test", "/var/run/tailscale/tailscaled.sock")
	want := "sudo env DARKPANEL_TAILSCALED_SOCKET=/var/run/tailscale/tailscaled.sock /var/tmp/tslocal.test" +
		" -test.run '^TestTailscaledLive$' -test.v -test.timeout 5m 2>&1"
	if got != want {
		t.Errorf("m5lCommand =\n%s\nwant\n%s", got, want)
	}
	got = m5lCommand("/var/tmp/my test", "/run/a b.sock")
	want = "sudo env DARKPANEL_TAILSCALED_SOCKET='/run/a b.sock' '/var/tmp/my test'" +
		" -test.run '^TestTailscaledLive$' -test.v -test.timeout 5m 2>&1"
	if got != want {
		t.Errorf("m5lCommand with spaces =\n%s\nwant\n%s", got, want)
	}
	if m5lSocket != tslocal.DefaultSocket {
		t.Errorf("m5lSocket = %q, want tailscaled's socket %q", m5lSocket, tslocal.DefaultSocket)
	}
}

func TestM5lUnitVerdict(t *testing.T) {
	const pass = "=== RUN   TestTailscaledLive\n--- PASS: TestTailscaledLive (2.34s)\nPASS\n"
	cases := []struct {
		name, out string
		want      string // "" when accepted, else a substring of the error
	}{
		{"pass", pass, ""},
		{"pass with logs", "    live_test.go:1: status\n" + pass, ""},
		{"skipped, which the binary still reports as PASS", "=== RUN   TestTailscaledLive\n    live_test.go:20: set DARKPANEL_TAILSCALED_SOCKET to drive a real tailscaled\n--- SKIP: TestTailscaledLive (0.00s)\nPASS\n", "--- SKIP"},
		{"failed", "=== RUN   TestTailscaledLive\n    live_test.go:90: boom\n--- FAIL: TestTailscaledLive (1.00s)\nFAIL\n", "--- FAIL"},
		{"failed without the summary line", "FAIL\n", "FAIL"},
		{"panic", "panic: runtime error\n", "panic:"},
		{"empty", "", "no --- PASS"},
		{"another test passed", "--- PASS: TestOther (0.00s)\nPASS\n", "no --- PASS"},
		{"the docker test passed", "--- PASS: TestDockerLive (1.00s)\nPASS\n", "no --- PASS"},
	}
	for _, c := range cases {
		err := m5lVerdict(c.out)
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: %v, want it accepted", c.name, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want) || !strings.Contains(err.Error(), "TestTailscaledLive")):
			t.Errorf("%s: %v, want an error naming TestTailscaledLive and containing %q", c.name, err, c.want)
		}
	}
}

func TestM5lUnitStep(t *testing.T) {
	steps := m5pSteps(context.Background(), nil)
	if len(steps) != 4 {
		t.Fatalf("m5pSteps has %d steps, want install, package-install, pin-rollback, tailscaled-live", len(steps))
	}
	for i, want := range []struct {
		name string
		gate bool
	}{{"install", true}, {"package-install", true}, {"pin-rollback", false}, {"tailscaled-live", false}} {
		if steps[i].name != want.name || steps[i].gate != want.gate || steps[i].run == nil {
			t.Errorf("step %d = %q (gate %t, run set %t), want %q (gate %t)", i, steps[i].name, steps[i].gate, steps[i].run != nil, want.name, want.gate)
		}
	}
}

// m5lWorkflowDoc has the parts of harness.yml that m5lWorkflow reads.
const m5lWorkflowDoc = `jobs:
  build:
    runs-on: ubuntu-24.04
    steps:
      - run: make harness-builds
      - name: Build the dockerapi live test
        run: CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go test -c -o bin/harness/dockerapi.test ./internal/platform/dockerapi
      - name: Build the tslocal live test
        run: CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go test -c -o bin/harness/tslocal.test ./internal/platform/tslocal
      - uses: actions/upload-artifact@x
        with:
          path: bin/harness
  vm:
    needs: build
    timeout-minutes: 280
    steps:
      - name: Run the harness
        run: >-
          go test -tags harness -timeout 100m -v ./test/harness/
  vm-m5:
    needs: build
    runs-on: ubuntu-24.04
    timeout-minutes: 100
    strategy:
      fail-fast: false
      matrix:
        os: [ubuntu-22.04, ubuntu-24.04, debian-12, debian-13]
    steps:
      - name: Enable KVM
        run: test -e /dev/kvm
      - run: sudo apt-get install -y qemu-system-x86
      - uses: actions/checkout@x
      - uses: actions/setup-go@x
      - uses: actions/download-artifact@x
        with:
          name: darkpanel-linux-amd64
      - uses: actions/download-artifact@x
        with:
          name: darkpanel-harness
      - uses: actions/cache@x
      - run: go vet -tags harness ./test/...
      - name: Run the M5 node harness
        run: >-
          go test -tags harness -timeout 70m -v -run '^(TestM5Node|TestM5Pin)$' ./test/harness/
          -args -os ${{ matrix.os }} -m5
          -m2 "$GITHUB_WORKSPACE/bin/harness"
          -tslocal-test "$GITHUB_WORKSPACE/bin/harness/tslocal.test"
          -workdir "$RUNNER_TEMP/harness-m5"
      - if: failure()
        uses: actions/upload-artifact@x
        with:
          name: logs-m5-${{ matrix.os }}
          path: ${{ runner.temp }}/harness-m5/**/*.log
`

func TestM5lUnitWorkflow(t *testing.T) {
	if err := m5lWorkflow(m5lWorkflowDoc); err != nil {
		t.Fatalf("the fixture: %v", err)
	}
	const buildLine = "CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go test -c -o bin/harness/tslocal.test ./internal/platform/tslocal"
	const buildStep = "      - name: Build the tslocal live test\n        run: " + buildLine + "\n"
	for name, tc := range map[string]struct{ from, to, want string }{
		"no build step":      {"Build the tslocal live test", "Build something", "Build the tslocal live test"},
		"dynamic binary":     {buildLine, strings.Replace(buildLine, "CGO_ENABLED=0 ", "", 1), "CGO_ENABLED=0"},
		"wrong architecture": {buildLine, strings.Replace(buildLine, "GOARCH=amd64", "GOARCH=arm64", 1), "GOARCH=amd64"},
		"wrong output":       {"-o bin/harness/tslocal.test", "-o bin/tslocal.test", "-o bin/harness/tslocal.test"},
		"wrong package":      {"./internal/platform/tslocal", "./internal/platform/other", "./internal/platform/tslocal"},
		"built after upload": {buildStep, "", "Build the tslocal live test"},
		"M5 left in vm":      {"go test -tags harness -timeout 100m -v ./test/harness/", "go test -tags harness -timeout 100m -v -run '^TestM5Pin$' ./test/harness/", "vm job"},
		"no vm-m5 job":       {"  vm-m5:\n", "  vm-other:\n", "no vm-m5 job"},
		"no needs":           {"  vm-m5:\n    needs: build\n", "  vm-m5:\n", "needs: build"},
		"fail-fast":          {"fail-fast: false", "fail-fast: true", "fail-fast: false"},
		"no matrix":          {"debian-13]", "debian-12]", "os: ["},
		"no KVM":             {"/dev/kvm", "/dev/null", "/dev/kvm"},
		"no fleet download":  {"name: darkpanel-harness\n", "name: other\n", "name: darkpanel-harness"},
		"no vet":             {"      - run: go vet -tags harness ./test/...\n", "", "go vet -tags harness"},
		"no run step":        {"Run the M5 node harness", "Run M5", "Run the M5 node harness"},
		"wrong tests":        {"TestM5Pin", "TestM5Other", "TestM5Pin"},
		"no flag":            {" -m5\n", "\n", "-m5"},
		"no fleet builds":    {"          -m2 \"$GITHUB_WORKSPACE/bin/harness\"\n", "", "-m2"},
		"no binary":          {"-tslocal-test", "-unknown", "-tslocal-test"},
		"no workdir":         {"          -workdir \"$RUNNER_TEMP/harness-m5\"\n", "", "-workdir"},
		"short timeout":      {"-timeout 70m -v -run", "-timeout 40m -v -run", "-timeout 40m"},
		"timeout over limit": {"-timeout 70m -v -run", "-timeout 80m -v -run", "-timeout 80m"},
		"no logs":            {"${{ runner.temp }}/harness-m5/**/*.log", "", "harness-m5/**/*.log"},
		"wrong artifact":     {"name: logs-m5-", "name: logs-", "logs-m5-"},
		"limit too small":    {"timeout-minutes: 100", "timeout-minutes: 90", "timeout-minutes 90"},
		"limit too large":    {"timeout-minutes: 100", "timeout-minutes: 130", "timeout-minutes 130"},
	} {
		if !strings.Contains(m5lWorkflowDoc, tc.from) {
			t.Fatalf("%s: the fixture lacks %q", name, tc.from)
		}
		doc := strings.Replace(m5lWorkflowDoc, tc.from, tc.to, 1)
		if name == "built after upload" {
			doc = strings.Replace(doc, "  vm:\n", buildStep+"  vm:\n", 1)
		}
		if err := m5lWorkflow(doc); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want an error naming %q", name, err, tc.want)
		}
	}
}

// TestM5lUnitWorkflowFile checks the committed workflow, which is the only
// place TestM5Pin and the live test run.
func TestM5lUnitWorkflowFile(t *testing.T) {
	b, err := os.ReadFile("../../.github/workflows/harness.yml")
	if err != nil {
		t.Fatal(err)
	}
	if err := m5lWorkflow(string(b)); err != nil {
		t.Error(err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
mkdir -p /root/.cache/w4-tmp/35 && export TMPDIR=/root/.cache/w4-tmp/35 GOTOOLCHAIN=local
timeout 300 go vet -tags harness ./test/harness/
```

Expected: FAIL to build, `undefined: m5lCommand` (then `m5lSocket`, `m5lVerdict`, `m5lWorkflow`). After the Go helpers of Step 3 exist and before the workflow and the step table are edited, `go test -tags harness -run '^(TestM5lUnitWorkflowFile|TestM5lUnitStep)$' ./test/harness/` must fail with `the build job has no step named "Build the tslocal live test"` and `m5pSteps has 3 steps`, and `TestM4lWorkflowFile` and `TestM3xExpoWorkflowFile` (the M4 and M3 checks of the same file) must still pass.

- [ ] **Step 3: Implement**

*Budget check before editing.* Read the deadline constant of `TestM5Node` in Task 33's `test/harness/m5_node_test.go`. The job below gives the run step `-timeout 70m` for `TestM5Node` and `TestM5Pin` together; that holds when `TestM5Node`'s deadline plus `m5pDeadline` (35 minutes) plus 10 minutes of cleanup is at most 70. If it is not, do not change the numbers: finish the task and put the sum in the report as a concern.

*`test/harness/m5_live_test.go`* (first line `//go:build harness`, package `harness`):
- `var m5lBinaryFlag = flag.String("tslocal-test", "", "path of the compiled tslocal test binary")`.
- `const m5lSocket = tslocal.DefaultSocket` and `const m5lRemote = "/var/tmp/tslocal.test"`.
- `m5lCommand(binary, socket string) string` returns `sudo env DARKPANEL_TAILSCALED_SOCKET=<socket> <binary> -test.run '^TestTailscaledLive$' -test.v -test.timeout 5m 2>&1`, with `socket` and `binary` each passed through `m3dxShell`.
- `m5lVerdict(out string) error` reads the `-test.v` output line by line: a line starting `--- SKIP`, `--- FAIL`, `FAIL` or `panic:` is an error `harness: TestTailscaledLive did not pass: "<line>"` followed by the output, and the output without a line starting `--- PASS: TestTailscaledLive` is an error containing `no --- PASS: TestTailscaledLive line`. The skip case matters: the test binary prints `--- SKIP` and ends `PASS` when `DARKPANEL_TAILSCALED_SOCKET` is empty.
- `m5lLive(ctx context.Context, t *testing.T, f *fleet)`: fail with `harness: -tslocal-test is required: the path of the tslocal test binary the workflow builds` when the flag is empty (a skip would hide a workflow that lost the flag); then, for `f.controller` and `f.agent` in turn, each as a subtest named `controller` and `agent`: require `unitState(ctx, v, "tailscaled")` to be `active`; copy the binary with `v.CopyFile(ctx, *m5lBinaryFlag, m5lRemote, 0o755)` and remove it in a `t.Cleanup` (a context of its own, one minute); run `m5lCommand(m5lRemote, m5lSocket)` on `v`; log the output with `t.Logf`; fail on a non-zero exit, then on `m5lVerdict`. Both VMs have tailscaled running and logged out, which is what `TestTailscaledLive` needs (provisioning, `provision.go`); on the agent the pin scenario has left the prior package installed and tailscaled running.
- Constants `m5lBuildStep = "Build the tslocal live test"`, `m5lRunStep = "Run the M5 node harness"`, `m5lJob = "vm-m5"`, `m5lJobLimit = 100` and `m5lSetupMinutes = 30` (a harness job is split per milestone past 120 minutes, and this one has a limit of 100 whose last 30 are setup).
- `m5lWorkflow(yml string) error`, which the tests call. It uses `m4lStep` and `m4lSetup`, and requires, in order, each with an error naming what is missing or wrong:
  1. a `build` job (`m3dxExpoJob`) with the step `m5lBuildStep` containing `CGO_ENABLED=0`, `GOOS=linux`, `GOARCH=amd64`, `go test -c`, `-o bin/harness/tslocal.test` and `./internal/platform/tslocal`, placed before the text `path: bin/harness` of the artifact upload (error: `the step "<name>" lacks <text>`, or `the build job does not run the step "<name>" before it uploads path: bin/harness`, or `the build job has no step named "<name>"`);
  2. a `vm` job (error `the workflow has no vm job`) that contains neither `TestM5` nor `harness-m5` (error `the vm job holds <text>, which belongs to the vm-m5 job`);
  3. a `vm-m5` job (error `the workflow has no vm-m5 job`) containing `needs: build`, `runs-on: ubuntu-24.04`, `fail-fast: false`, `os: [ubuntu-22.04, ubuntu-24.04, debian-12, debian-13]` and every `m4lSetup` string (error `the vm-m5 job lacks <text>`);
  4. in that job the step `m5lRunStep` (error `the vm-m5 job has no step named "<name>"`) containing `-run '^(TestM5Node|TestM5Pin)$'`, ` -m5`, `-os ${{ matrix.os }}`, `-m2 "$GITHUB_WORKSPACE/bin/harness"`, `-workdir "$RUNNER_TEMP/harness-m5"` and `-tslocal-test "$GITHUB_WORKSPACE/bin/harness/tslocal.test"` (error `the step "<name>" lacks <text>`), with a `-timeout <n>m` (`m3dxExpoTimeoutRE`) of at least `m5pDeadline` plus 10 minutes (error `the step "<name>" has -timeout <n>m, under TestM5Pin's deadline <d> plus 10 minutes`) and at most `m5lJobLimit - m5lSetupMinutes` (error `the step "<name>" has -timeout <n>m, over <max>m (...)`);
  5. in that job the failure upload: `name: logs-m5-${{ matrix.os }}` and `harness-m5/**/*.log` (error `the vm-m5 job's failure upload lacks <text>`);
  6. in that job a `timeout-minutes` (`m3dxExpoLimitRE`) equal to `m5lJobLimit` (error `the vm-m5 job has timeout-minutes <have>, want 100`; no value at all: `the vm-m5 job has no timeout-minutes`).

*Step table.* In `test/harness/m5_pin_test.go`, after Task 34's last row of `m5pSteps`, add, in the form of the existing rows and not a gate (it runs even when the pin step failed):

```go
		{"tailscaled-live", false, func(t *testing.T) { m5lLive(ctx, t, f) }},
```

*`.github/workflows/harness.yml`* (a configuration value; the two edits and nothing else, no action is bumped, and the `vm` and `vm-m4` jobs stay exactly as they are). Like the dockerapi live test, the tslocal one runs on VMs that have no Go toolchain, and `CGO_ENABLED=0` gives a static binary that runs on every pinned OS.

1. In the `build` job, directly after the step `Build the dockerapi live test` and before `Harness unit tests`, add (the existing artifact step that uploads `bin/harness` carries the file to the new job, and `-m2`, the same directory, ignores the extra file):

```yaml
      # The tslocal live test runs on the harness VMs too, as a static binary.
      - name: Build the tslocal live test
        run: CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go test -c -o bin/harness/tslocal.test ./internal/platform/tslocal
```

2. At the end of the file, after the `vm-m4` job and a blank line, add the job below. It carries the `vm-m4` job's setup steps, copied exactly, then the M5 step and its own failure upload. The comment sits under the job key, not above it, because `m3dxExpoJob` counts a comment line before a key as part of the job above.

```yaml
  vm-m5:
    # The M5 run has a job of its own, as the M4 run has: a job past 120
    # minutes is split per milestone. The limit is the step's -timeout plus
    # 30 minutes of setup.
    needs: build
    runs-on: ubuntu-24.04
    timeout-minutes: 100
    strategy:
      fail-fast: false
      matrix:
        os: [ubuntu-22.04, ubuntu-24.04, debian-12, debian-13]
    steps:
      - name: Enable KVM
        run: |
          if [ ! -e /dev/kvm ]; then
            echo "::error::/dev/kvm is missing on this runner; the harness needs KVM (ADR 0008)"
            exit 1
          fi
          echo 'KERNEL=="kvm", GROUP="kvm", MODE="0666", OPTIONS+="static_node=kvm"' | sudo tee /etc/udev/rules.d/99-kvm4all.rules
          sudo udevadm control --reload-rules
          sudo udevadm trigger --name-match=kvm
      - name: Install QEMU and cloud-image-utils
        run: |
          sudo apt-get update
          sudo apt-get install -y --no-install-recommends qemu-system-x86 qemu-utils cloud-image-utils
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
        with:
          persist-credentials: false
      - uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
        with:
          go-version-file: go.mod
          cache-dependency-path: go.sum
      - uses: actions/download-artifact@9000827ccba6bdab643e8b6fd33ac0654aef8333 # v8.0.2
        with:
          name: darkpanel-linux-amd64
          path: bin
      - uses: actions/download-artifact@9000827ccba6bdab643e8b6fd33ac0654aef8333 # v8.0.2
        with:
          name: darkpanel-harness
          path: bin/harness
      - uses: actions/cache@55cc8345863c7cc4c66a329aec7e433d2d1c52a9 # v6.1.0
        with:
          path: ~/.cache/darkpanel-harness
          key: harness-images-${{ matrix.os }}-${{ hashFiles('test/harness/images.json') }}
      - run: go vet -tags harness ./test/...
      # TestM5Node and TestM5Pin each boot VMs of their own; TestM5Pin also
      # runs the tslocal live test on its two VMs. The -timeout covers both
      # deadlines and the extra 10 minutes leave the VM cleanups time to
      # finish.
      - name: Run the M5 node harness
        run: >-
          go test -tags harness -timeout 70m -v -run '^(TestM5Node|TestM5Pin)$' ./test/harness/
          -args -os ${{ matrix.os }} -m5
          -bin "$GITHUB_WORKSPACE/bin/darkpanel-linux-amd64"
          -m2 "$GITHUB_WORKSPACE/bin/harness"
          -tslocal-test "$GITHUB_WORKSPACE/bin/harness/tslocal.test"
          -commit "$GITHUB_SHA"
          -workdir "$RUNNER_TEMP/harness-m5"
          -cache "$HOME/.cache/darkpanel-harness"
      - if: failure()
        uses: actions/upload-artifact@cf430e030ddbb5b0abf93d22962f4752f3646cd9 # v7.0.2
        with:
          name: logs-m5-${{ matrix.os }}
          path: ${{ runner.temp }}/harness-m5/**/*.log
          retention-days: 7
          if-no-files-found: ignore
```

Pushing the branch and dispatching `harness.yml` on it is the plan's gate 7 (the owner's standing authorization), not a step of this task.

- [ ] **Step 4: Run the tests to verify they pass**

```bash
mkdir -p /root/.cache/w4-tmp/35 && export TMPDIR=/root/.cache/w4-tmp/35 GOTOOLCHAIN=local
timeout 300 go vet -tags harness ./test/...
timeout 300 go test -tags harness -count=1 -run '^(TestM5lUnit|TestM5pUnit|TestM4l|TestM3xExpoWorkflowFile)' -v ./test/harness/
timeout 300 go test -tags harness -count=1 -run 'Unit' ./test/harness/...
timeout 600 go test -race -tags harness -count=1 ./test/harness/...
timeout 900 golangci-lint run --allow-serial-runners --build-tags harness ./test/...
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 timeout 300 go test -c -o "$TMPDIR/tslocal.test" ./internal/platform/tslocal
file "$TMPDIR/tslocal.test"
env -u DARKPANEL_TAILSCALED_SOCKET "$TMPDIR/tslocal.test" -test.run '^TestTailscaledLive$' -test.v
git diff -- .github/workflows/harness.yml
```

Expected: all `TestM5lUnit*`, `TestM5pUnit*`, `TestM4l*` and `TestM3xExpoWorkflowFile` pass; `0 issues.`; `file` prints `statically linked`; the `-test.v` run prints `--- SKIP: TestTailscaledLive` and `PASS`; the diff touches only the new `build` step and the appended `vm-m5` job (the `vm` and `vm-m4` jobs' lines are unchanged). Never set `DARKPANEL_TAILSCALED_SOCKET` on this host: the test would drive a tailscaled socket (Global Constraints, host safety); it runs only on the harness VMs in gate 7. Then the checks every task runs (`go build ./...`, `go vet ./...`, `go test -count=1 ./...`, `golangci-lint run --allow-serial-runners ./...`, `go mod tidy -diff`), with their real output recorded. Remove `/root/.cache/w4-tmp/35` and run `go clean -testcache`.

- [ ] **Step 5: Commit**

```bash
git add test/harness/m5_live_test.go test/harness/m5_live_unit_test.go test/harness/m5_pin_test.go .github/workflows/harness.yml
git commit -m "test(harness): run TestTailscaledLive in the M5 job"
```
