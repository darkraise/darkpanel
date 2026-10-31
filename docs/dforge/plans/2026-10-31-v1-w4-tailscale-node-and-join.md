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
- Tasks that share a Go package declare no top-level identifier, test helpers included, that another task declares. Every new test identifier carries its task's prefix from Contracts "Test prefixes", except the shared helpers of Contracts "Tailscale test host", which only Task 8 declares.
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

Each task's **Depends on** line is exactly this: 1, 2, 3, 4, 5, 6, 7: none; 8: Task 1; 9: Task 2, Task 7; 10, 11: Task 7; 12, 13: Task 5; 14, 16, 20: Task 8; 15: Task 3, Task 8; 17: Task 4, Task 8; 18: Task 10; 19: Task 12, Task 13; 21, 22: Task 17; 23, 24, 25, 26: Task 20; 27: Task 21; 28, 29: Task 24; 30: Task 2, Task 26; 31: Task 14, Task 15, Task 16, Task 20, Task 21, Task 22, Task 23, Task 24, Task 25, Task 27, Task 28, Task 29; 32: Task 10, Task 31; 33: Task 31; 34: Task 33; 35: Task 34.

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

Package `internal/modules/tailnet`. Task 10 writes `join_enroll.go`: `module` implements `core.EnrollJoiner` (`func (m module) PlanJoin(...)`, `func (m module) PrepareJoin(...)`), through `func (t tn) planJoin(...)` and `func (t tn) prepareJoin(...)`. PrepareJoin inserts the `tailnet_keys` row `{description: "darkpanel-enroll-" + HostID, kind: "enroll", owner_id: HostID, state: "requested", expiry_seconds: 3600, tags_json}` in its own write transaction, then calls `api.CreateKey(KeyRequest{Description, Tags, Reusable: false, Ephemeral: false, Preauthorized: true, ExpirySeconds: 3600})`, then settles the row `live` with the key id (`settleKey`); a provider failure leaves the row for the sweep (spec 1.9's uncertain-create rule) and returns the error. Task 18 writes `join_sweep.go`: `func settleEnrollKeys(ctx context.Context, db *store.DB, now time.Time) error` moves each `enroll` row in `live` or `uncertain`: to `used` when `core_hosts` holds its owner in state `enrolled`; to `retired` when the owner row is gone, or its enrollment token (`core_enroll_tokens` for the owner) expired unconsumed, or the row is older than its expiry plus 15 minutes; and `keysweep.go`'s `sweep` calls it first in every pass.

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

### Tailscale installer join (from Task 26)

Package `internal/modules/tailscale`, file `bootstrap.go`, the installer's entry point (spec 4.2 step 4):

```go
// JoinDeps is what Join uses on the host being installed.
type JoinDeps struct {
	Exec   core.Runner     // the production allowlisted runner
	Files  core.FileWriter // the production file provider over the new agent.db
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

Package `internal/controller/listen`, file `tailscale_cert.go` (the stub's two functions keep their signatures; `sources.go` is frozen): `tailscaleCertSource(d SourceDeps) CertSource` returns a source that reads `tailscale.cert.get/v1` with `d.Dispatch.ReadInternal(ctx, core.Principal{Actor: "system:listen.cert", Scope: core.ScopeAdmin}, d.LocalHostID, ..., {})` (bounded by 10 s), caches the `CertPair` (JSON shape of Contracts "Tailscale wire shapes"), answers a handshake whose `ServerName` equals the pair's name (case-insensitive, trailing dot ignored) and falls through otherwise, and refreshes in the background once two thirds of the pair's life has passed; `tailscaleNames` returns the cached pair's name, refreshed with the same bounded read, lower-cased, without a trailing dot, and dropped when it is empty, an IP literal, non-ASCII or not a valid DNS name. Any read error falls through and is logged at debug level once per distinct error.

### Web: tailscale (Tasks 5, 12, 13, 19)

`web/src/modules/tailscale/types.ts` (Task 5) exports `TAILSCALE_MODULE = "tailscale"`, `TAILSCALE_ACTION_VERSION = 1`, the TypeScript mirrors `TailscaleStatus`, `TailscaleNode`, `TailscalePeer`, `TailscaleLatency`, `RegionLatency`, `Measurement`, `PrefsFields` of Contracts "Tailscale wire shapes" (same JSON names), and the guards `isTailscaleStatus(v: unknown): v is TailscaleStatus` and `isTailscaleLatency(v: unknown): v is TailscaleLatency`; `fixtures.ts` exports `statusRunning`, `statusLoggedOut`, `latencyFixture` (three regions, one with STUN unavailable); `tailscale-change.tsx` exports `TailscaleChange({hostId, action, title, input, onClose})`, the docker module's `DockerChange` shape with `TAILSCALE_*` constants and, for `tailscale.prefs.set`, the change dialog's revert-timeout field. Task 12 writes `status-panel.tsx` (`StatusPanel({hostId})`), `peers-table.tsx` (`PeersTable({peers})`) and `latency-panel.tsx` (`LatencyPanel({hostId})`). Task 13 writes `prefs-form.tsx` (`PrefsForm({hostId, status, prefs})`), `node-controls.tsx` (`NodeControls({hostId, status})`: login with auth key, logout) and `package-panel.tsx` (`PackagePanel({hostId, available})`: install when the module reports tailscaled unavailable, update otherwise). Task 19 writes `tailscale-page.tsx` (`TailscalePage({hostId})`, including a "Join tailnet" button applying `tailnet.node.join` through the change dialog when the host is not on a tailnet) and replaces `routes.tsx`. The resources are read with the existing resource query of `web/src/api/client.ts`.

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
