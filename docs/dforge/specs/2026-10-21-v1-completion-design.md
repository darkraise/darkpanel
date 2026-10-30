# v1 completion: firewall, Docker, Tailscale, exposure, hardening and release

**Date:** 2026-10-21
**Status:** design approved by the owner section by section on 2026-10-21;
revision 8 after the wave-0 check of revision 7 (see the review log). Per
the owner's choice the wave 0 plan follows without another spec round.
**Product spec:** docs/dforge/specs/2026-10-17-darkpanel-design.md
**Requirements:** R37–R81; M2 register rows 11, 16 and 35
(docs/dforge/registers/2026-10-19-m2-fleet-lifecycle-core.md); the
controller backup and restore deferred by ADR 0005 to M8; the M5 web
certificate work deferred by ADR 0010
**Execution strategy:** parallel — rolling wave plans. Wave 0 (core
extensions) fixes every shared file, interface, dependency, error code,
extension point and module skeleton. Safety-critical waves run alone (0, 1,
2, 4, 5, 6a, 6b, 7, 8, 10); waves 0b, 3 and 9 run their parts in parallel
worktrees. Each wave's plan is written when the previous wave has merged
(owner standing preference: favour parallel to reach v1 sooner).

## Goal

Build everything v1 still lacks, up to a v1.0.0-rc.1 that is ready to tag with
every gate green, and collect every step only the owner can take into one
checklist (section 12). M9 post-v1 candidates are out of scope.

## Owner decisions (2026-10-21)

- One spec covers every open v1 item. Plans roll: one plan per wave, written
  after the previous wave merges, executed and merged into `main` locally
  without stopping except at owner-only steps.
- Integrations with outside services (Tailscale's cloud API, Cloudflare,
  Let's Encrypt through Nginx Proxy Manager, DERP reachability) are built and
  tested against in-process fake servers and local stand-ins. Live
  demonstrations run once, as a gated step before v1.0, when the owner
  provides credentials.
- DERP runs as a signed derper artifact built by the release workflow from
  upstream `tailscale.com/cmd/derper` at a pinned version and installed by the
  agent as a systemd service.
- Approach: core extensions first and alone, then the firewall alone, then
  modules in waves by dependency, with connectivity, data-loss and exposure
  work alone; hardening and release last.
- During this programme Claude may push wave branches and `main` to origin and
  dispatch `harness.yml`; tags and releases need the owner.
- After round 1 the owner chose: revise every finding, then a second panel
  round before the wave 0 plan.
- **D1 (round 2): container allowances are pinned to their published
  binding.** R45's UFW route rules stay, and Darkpanel's Docker block lets a
  new connection reach them only when its original destination (host address
  and port, as conntrack recorded it before DNAT) is an allowed binding, so a
  container that reuses another container's address inherits nothing (2.6).
- **D2 (round 2): Nginx Proxy Manager's admin port is never published.**
  Port 81 stays on 127.0.0.1. An opt-in, off-by-default action publishes
  NPM's admin UI over HTTPS on a hostname the owner chooses, through an NPM
  proxy host with a Let's Encrypt certificate, forced SSL and HSTS, with a
  warning banner; an audited admin-only action reveals the NPM password
  (6). R65's "localhost and the tailnet only" is read as "the admin port is
  never published; HTTPS exposure only by the owner's explicit action".
- After round 3 the owner chose: revise every finding, then a full fourth
  panel round before the wave 0 plan.
- **Confirmed by the owner on 2026-10-21 (proposed in revision 4).** Two
  resolutions narrow or reword a requirement; the owner confirmed both as
  written:
  - **P1, S3's wording for proxied bindings.** A published port that
    `docker-proxy` serves on a family in which the container has no address
    (IPv6 on an IPv4-only network, Docker's default) never traverses
    `FORWARD`, so no route rule can govern it; Darkpanel governs it with
    UFW `INPUT` rules and requires a deny-by-default incoming policy (2.6).
    S3 ("unreachable unless a Darkpanel route rule allows it") reads
    "unless a Darkpanel UFW rule allows it" (product spec updated).
  - **P2, unsupported Docker network modes for the integration.** Networks
    in Docker's routed gateway modes, and networks with a custom bridge name
    and a non-private subnet for direct-routed (non-DNAT) access, cannot be
    protected from the moment they are created; they are reported
    unprotected and listed in Out of scope (2.6).
- After round 4 the owner split wave 6 into two alone waves (6a, 6b): NPM's
  destructive upgrade and both exposure implementations are exposure and
  data-loss work, which runs alone.
- After the wave-0 checks of revisions 5 and 6 the owner chose to simplify
  the lifecycle core (revision 7): each revision of the effects journal,
  undo, composite repair and recovery added mechanism, and each check found
  about as many problems in it.
- After revision 8 the owner chose to plan wave 0 as three sequential
  plans (`w0a`, `w0b`, `w0c`; see Plans) instead of one, because of its size.
- **Before the wave 1 plan (2026-10-24) the owner ruled on three conflicts
  between this spec and the merged wave 0 code:**
  - **R1, revertible repair actions.** A `RepairedBy` action may be
    compensable, irreversible or revertible, and a revertible repair action
    may name itself (`firewall.state.repair`, `tailscale.prefs.repair`). The
    registry's check, which refused a revertible repair action, is fixed by
    an integrator task of the wave 1 plan (1.3).
  - **R2, the revert timeout is per apply.** R41's "configurable timeout"
    is the apply request's `confirmTimeoutSeconds` (10–900 s, default 60);
    the CLI gains `--confirm-timeout` on every revertible action and the
    change dialog a timeout field. The stored per-host setting is deferred
    (2.4).
  - **R3, the firewall gathers its own live guard facts.** The firewall
    module reads sockets, interfaces and routes through
    `internal/platform/netstat` and finds a local `darkpanel-controller`
    process through its scoped unit client, declaring the external unit
    `{Name: "darkpanel-controller.service", Ops: ["state"]}` (1.5, 1.12).

## Versions

| Component | Version | Note |
|---|---|---|
| Docker Engine API | 1.44 minimum (Docker 25) for the `docker` module | preflight refuses older |
| Docker for the firewall integration and NPM | Docker 28.0 or later, with the `raw`-table localhost protection loaded | earlier releases, or a daemon run with `DOCKER_INSECURE_NO_IPTABLES_RAW=1`, let hosts on the same L2 reach localhost-published ports |
| Docker `FirewallBackend` | `/info` field, API 1.49 (Docker 28.1) or later, object `{Driver, Info}`; `Driver` is `iptables`, `nftables`, `iptables+firewalld` or `nftables+firewalld`; `Info` is not stable and is not read | absent field (Docker 28.0, or a client below 1.49): the daemon uses its iptables backend; the `nftables` drivers exist only from Docker 29.0 (experimental), so 28.x always reports an iptables driver |
| UFW | as shipped by each supported OS (0.36.1 on Ubuntu 22.04 to 0.36.2 on Debian 13) | rule comments need 0.35 |
| Compose plugin | the `docker-compose-plugin` package from Docker's repository, at `/usr/libexec/docker/cli-plugins/docker-compose` | harness pins its version |
| Tailscale | tailscaled 1.82 or later on managed nodes | local API endpoints in 1.8 |
| derper | `release/derper.version` (a `tailscale.com` module version, 1.102 or later) | Tailscale publishes no binaries or images; `-verify-clients` needs tailscaled from the same version on the DERP host (4.3) |
| Nginx Proxy Manager | `jc21/nginx-proxy-manager` 2.13 or later, pinned by digest in `internal/modules/npm/image.go` | 2.13 removed the default admin; `LE_SERVER` (since NPM 2.12) points certbot at another ACME directory |
| cloudflared | `cloudflare/cloudflared` 2025.4.0 or later, pinned by digest in `internal/modules/cloudflare/image.go` | `tunnel run --token-file`; the image runs as UID 65532 |
| Pebble | `ghcr.io/letsencrypt/pebble` and `ghcr.io/letsencrypt/pebble-challtestsrv`, pinned in `test/harness/pins.go` | ACME stand-in and its DNS server for harness certificate tests |

## Design

### 1. Core extensions (waves 0 and 0b)

Wave 0 changes the shared core; wave 0b builds the independent platform
clients. ADR 0014 records every decision in this section; where it refines
ADR 0002, 0003, 0004 or 0009 it says so.

#### 1.1 Action naming

Action names stay `<module>.<noun>.<verb>` and resources `<module>.<noun>`
(`internal/core/registry.go`): module and noun are `[a-z][a-z0-9]*`; only the
verb may contain `-` (`tailnet.device.set-routes`, `npm.admin.reveal-password`).
Every name in this spec follows that grammar. `BreakGlass` stays
controller-side only; an agent action is reachable over the agent socket by
`--local` without any flag, as today, except an `Internal` action (1.7),
which only the controller dispatches. `ActionRef` gains `MarshalText` and
`UnmarshalText` in the `<name>/v<version>` form its `String` already prints,
so step lists and DTOs carry it as one string.

#### 1.2 Effects journal and undo

Four rules shape the lifecycle core: **uncertainty stops automation** (an
effect not proven settled is `unresolved`; its change goes to
`needs_repair`, keeps its locks, and only the operator resolves it);
**undo is a short-window convenience** (only while no later change took
any of its lock keys, and never over an unresolved effect); **repair has
depth 1** (a repair change is never repaired itself); **one owner** (one
mechanism at a time acts on an unfinished change, and no timeout ends
that).

ADR 0004's `compensable` class keeps its meaning (no timer; undone by a
compensating step). Every compensable action declares exactly one of: an
**undo handler** (`Undoer`, below); a compensating action (`CompensatedBy`,
operator-invoked, as today; `core.update.apply` → `core.update.rollback` is
unchanged); or `ForwardOnly: true`, for an action whose effect is
monotonic and never reversed (`panel.listener.revoke`, 8).

**Declarations.** `core.ActionSpec` gains these fields (1.5, 1.7 and 1.14
add `Guards`, `MinScope`, `Available`, `Internal` and `Reveal`):

```go
type ActionSpec struct {
    // existing: Name, Summary, Version, Class, Side, BreakGlass, LockKeys, CompensatedBy
    ForwardOnly  bool          // compensable: never reversed; requires Converges
    Converges    bool          // apply run again with the same input, after a partial or complete apply, ends in the same state and plans an empty diff when the effect is present
    RepairedBy   string        // revertible only, required: an action of the same module (1.3)
    ApplyTimeout time.Duration // one budget for apply and verify; 0 means 30 s; above 45 s requires LongRunning; composites: computed at plan (1.3)
    LongRunning  bool          // apply answers once the intent is persisted (1.3)
    Task         bool          // the host sequences run in transient units (1.3c); set by DefineTask
    Unwind       Unwind        // composite only (1.3)
}
```

**The effects journal.** A change of a mutating action has an effects
journal on its executing side (agent or controller database, table
`core_effects`). An action that journals records each effect **before**
making it; every journal write commits (fsync) before it returns.
`core.Env` gains `Effects EffectJournal`, set whenever `Env.ChangeID` is
set: in Apply, Verify, Revert, Observe, Inverse, Recover and Task. Revert
receives `Env.ChangeID` from wave 0 on (today `runRevert` passes neither).

```go
type EffectKind string // "create" | "modify" | "delete"

type EffectState string

const (
    EffectIntended   EffectState = "intended"
    EffectMade       EffectState = "made"
    EffectSkipped    EffectState = "skipped"
    EffectUnresolved EffectState = "unresolved"
    EffectUndoing    EffectState = "undoing"
    EffectUndone     EffectState = "undone"
    EffectAccepted   EffectState = "accepted"
    EffectAbandoned  EffectState = "abandoned"
)

// StateDoc is the comparable form of a resource's state. State holds the
// fields an action sets and its inverse restores; Version holds the fields
// that change on every write or restart (a start time, an endpoint id, an
// ETag, modified_on) and every State field an inverse cannot restore. It
// never holds a secret. State "null" means the resource does not exist.
// Each module section lists, per effect kind, which fields go where
// (2.5, 3, 4, 6, 7).
type StateDoc struct {
    State   json.RawMessage `json:"state"`
    Version json.RawMessage `json:"version,omitempty"`
}

// RecoveryID is how an observation finds exactly the object a creation made.
type RecoveryID struct {
    Kind  string `json:"kind"`  // "name" | "marker" | "idempotency"
    Value string `json:"value"`
}

// Effect is one row of core_effects.
type Effect struct {
    ChangeID   string
    Seq        int         // 1, 2, ... in the order intended
    Kind       EffectKind
    Resource   string      // "<module>.<noun>:<identity>"; a creation: "<module>.<noun>:?<recovery value>" until made
    Recovery   *RecoveryID // creations only
    Local      bool        // the forward call ends with the process that makes it (below)
    InverseLocal bool      // the same for the inverse call
    CommitWait time.Duration // a call that is not Local: how long the other side may still commit it
    CommitBy   time.Time   // no resolution before this time: the forward call's (or, while undoing, the inverse's) start plus CommitWait
    InverseRecovery *RecoveryID // an inverse that creates (the undo of a deletion): how an observation finds the recreated object
    UndoResource string    // the object the inverse left, as Inverse returned it
    Before     StateDoc    // the state the call expects to find
    After      *StateDoc   // the state the call leaves; nil when it cannot be known before the call
    Post       *StateDoc   // made: the state an undo must still find
    UndoTo     *StateDoc   // undoing: the state the inverse leaves
    Current    *StateDoc   // unresolved and accepted: what was observed
    State      EffectState
    Reason     string      // unresolved: "uncertain" | "ambiguous" | "conflict"
    Resolution string      // the last resolution ("accept" | "reconcile"), with ResolvedBy and ResolvedAt
    ResolvedBy string
    ResolvedAt time.Time
}

// EffectJournal is the handler's view of its change's journal. Every
// method commits before it returns; an illegal transition is an error that
// writes nothing.
type EffectJournal interface {
    // Intend records e as intended and returns its seq. secret, if any, is
    // sealed with associated data "core_effects.secret_ct.<change id>.<seq>".
    Intend(ctx context.Context, e Effect, secret []byte) (seq int, err error)
    // Made records that the call succeeded; resource names the object
    // (a creation's server-assigned id) and post its state now.
    Made(ctx context.Context, seq int, resource string, post StateDoc) error
    // Skipped records that the call was not made, or was refused with an
    // answer that guarantees nothing committed (a Definite error, below).
    Skipped(ctx context.Context, seq int) error
    List(ctx context.Context) ([]Effect, error)
    Secret(ctx context.Context, seq int) ([]byte, error)
}

// Definite marks err as a refusal that guarantees the call committed
// nothing (a provider's 4xx validation answer, a precondition the module
// checked before calling). IsDefinite reports it.
func Definite(err error) error
func IsDefinite(err error) bool
```

**Local and remote calls.** Locality belongs to the operation, not to the
process that issued it, and the action declares it per effect (forward
call `Local`, inverse `InverseLocal`). A call is local only when its whole
effect is done by the time the issuing process is gone: a `FileWriter`
write, or a runner program that does the work itself (`ufw`, the
iptables tools, `apt-get`, `dpkg`, `apt-mark`); its process group is killed
with the agent, as today, or with its task unit (1.3c), so once that
process is gone nothing of the call can still commit. A program that hands
work to a daemon (`docker compose`, which drives the Docker Engine) is not
local, nor are calls to the Docker Engine, the Tailscale local API,
systemd jobs over D-Bus (a restart a task unit queued can run after the
unit exits) and every cloud provider: the other side may still commit
them after Darkpanel stops waiting. For those the action sets
`CommitWait` to the time its section gives (cloud calls 10 min, local
daemons 2 min); `CommitBy` is a waiting period before the operator may
resolve the effect, not proof.

Effect states (the engine writes every transition not marked "handler"):

| From | To | When |
|---|---|---|
| — | `intended` | handler: `Intend`, before the call |
| `intended` | `made` | handler: `Made`, the call succeeded |
| `intended` | `skipped` | handler: `Skipped` |
| `intended` | `made`, `skipped` or `unresolved` | **settle** (below), once the handler has returned or was cut off |
| `made` | `unresolved` (`conflict`) | undo: the observation before the inverse differs from `Post` |
| `made` | `undoing` | undo: immediately before the inverse, `UndoTo` recorded and, for an inverse that is not `InverseLocal`, `CommitBy` reset to now plus `CommitWait` |
| `undoing` | `undone` | the inverse returned no error and the observed State equals `UndoTo`'s |
| `undoing` | `made` | the inverse returned a `Definite` error, or an `InverseLocal` inverse that failed or was cut off is observed State-equal to `Post`: nothing happened |
| `undoing` | `unresolved` | any other outcome of an inverse; an inverse that is not `InverseLocal` and did not return (an error that is not `Definite`, a cut-off) is `unresolved` (`uncertain`) whatever the observation shows |
| `unresolved` | `accepted`, `made` or `skipped` | the operator's resolution (below) |
| any non-terminal | `abandoned` | release (1.3) |

`skipped`, `undone`, `accepted` and `abandoned` are terminal. A sealed
`secret` is cleared in the transaction that makes its effect terminal, and
for a `made` effect when its change can no longer be undone (its undo
window has closed, or the change is a step whose composite reached a
terminal state); the engine's hourly purge applies the second rule. It
exists only for an inverse that needs a value the effect created (a
token's id and secret to delete it).

**Observing.** An action that journals implements `Observer`; `Intend`
fails for an action without one.

```go
type Observation struct {
    Current    StateDoc
    Resource   string      // a creation: the matched object's identity when Matches is 1
    Matches    int         // a creation: objects its RecoveryID finds; 1 for modify and delete
    Candidates []Candidate // a creation with Matches > 1: every match, for the operator
    Exclusive  bool        // the identity tells this object apart from every other (6 says when it does not)
    Allowed    []string    // resolutions the module permits for this effect; nil means both
}

type Candidate struct {
    Resource string   `json:"resource"`
    State    StateDoc `json:"state"`
}

// Observer reads the resource of one effect. It only reads. For an effect
// that is undoing or was undone, it looks for e.UndoResource or, when that
// is empty, the object e.InverseRecovery finds.
type Observer interface {
    Observe(ctx context.Context, env Env, e Effect) (Observation, error)
}

// Resolver is optional. Resolved runs inside the resolution transaction,
// after the effect's new state is written and before the commit, so a
// module can move its own bookkeeping row with the resolution (1.9's
// tailnet_keys). It writes only the module's own tables; an error rolls
// the resolution back.
type Resolver interface {
    Resolved(ctx context.Context, tx *sql.Tx, e Effect, resolution string) error
}
```

Two `StateDoc` values are compared as canonical JSON (object keys sorted,
no insignificant whitespace, numbers as `encoding/json` writes them).
"Equal" means State and Version equal; "State-equal" compares State only.

**Settle** (the engine, for each effect still `intended` after the
handler returned an error or was cut off; for a task action after its unit
is inactive, 1.3c):

- not `Local` → `unresolved` (`uncertain`);
- `Local`, observed: a creation with no match → `skipped`; a creation with
  one match, State-equal to `After` (any State when `After` is nil) →
  `made` with that resource and `Post` the observation; a modify or delete
  equal to `Before` → `skipped`; a modify or delete with `After` set and
  State-equal to it → `made`, `Post` the observation; anything else →
  `unresolved` (`ambiguous`). An observation that fails → `unresolved`.

**Recovery identity (creations).** Recorded with `Intend`, chosen per
endpoint in the action's section: `name` (a fixed name such as
`darkpanel-npm`, checked free at plan and again at apply just before the
call; a change-id-derived name, checked at apply), `marker` (Docker label
`darkpanel.change=<change id>`, DNS record comment `darkpanel:<change id>`,
the Tailscale key namespaces of 1.9) or `idempotency` (a provider key).
Module tables are the authority over this text (Docker: the comparator
table of 3; Nginx Proxy Manager: the tables of 6). Darkpanel never deletes
an object its identity cannot tell apart from others.

**Undo handlers.**

```go
// Undoer is the undo handler of a compensable action. Inverse makes the
// inverse call of one made effect, leaving e.Resource in e.UndoTo, and
// touches no other resource. A Definite error means it changed nothing.
// An inverse that creates (the undo of a deletion) returns the new
// object's identity; the engine records it as e.UndoResource.
type Undoer interface {
    Observer
    Inverse(ctx context.Context, env Env, e Effect) (InverseResult, error)
}

type InverseResult struct {
    Resource string // an inverse that creates: the recreated object; "" otherwise
}
```

An effect whose inverse creates declares `InverseRecovery` at `Intend`
(the same identity kinds as `Recovery`, recorded before the inverse runs),
so an inverse cut off before it returned can still be observed.

`DefineMutating`, `DefineRevertible` and `DefineTask` find `Observer`,
`Resolver`, `Undoer` and `Recoverer` (1.3b) by type assertions on the
handler (the `Uninstaller` idiom) and fill the new `ErasedHandler` fields
`Observe`, `Resolved`, `Inverse` and `Recover` (JSON forms). `UndoTo` is `Before` with `Version`
cleared (a creation: `{"state": null}`); because the check after an
inverse compares State only, a field an inverse cannot restore belongs in
`Version`.

**Undo of a change** (`Engine.Undo(ctx, p, changeID)`).

- **Eligible:** a compensable change of an action with an undo handler, in
  `completed`, `failed` or `interrupted` (its locks were released), or in
  `needs_repair` (it still holds them, and undo is its repair, 1.3); with
  no `unresolved` effect (else `core.needs_repair`, "resolve its effects
  first"); without `recovery_retry` (else `core.needs_repair`, "repair it:
  its recovery handler has not finished", 1.3b); not owned (else
  `core.change_busy`); not a step of a composite unless the caller is that
  composite's unwind or the step is in `needs_repair` and the undo is its
  repair (else `core.change_busy` naming the composite); not a
  repair change (`core.change_busy` naming its target); not while the host
  is uninstalling (`core.uninstalling`, 1.3c). Any other state:
  `change.not_pending`; `reverted`: answered with the change (idempotent).
- **Window.** Table `core_lock_epochs (key, epoch)`. Only the intent
  transaction of a new change (including a repair change) increments the
  epoch of each key it takes and records the new values in
  `core_changes.lock_epochs`; undo, repair and recovery claims take locks
  without incrementing. A change whose locks were released is undoable
  only while every one of its keys still has the epoch it recorded;
  otherwise `core.undo_conflict` ("lock `<key>` was taken by a later
  change"). The transaction that makes a change `reverted` (by undo,
  revert or a recovery handler's `rolled_back`), or `failed` with no
  effect left `made` or `unresolved`, sets each of its keys whose epoch is
  still the recorded one back to that value minus one: the change left
  nothing behind, so the change before it on that key becomes undoable
  again. Consequently an operator can undo the last changes on a key
  newest first, and a composite's unwind, which undoes its steps newest
  first, is never refused because of its own later steps (a step that
  failed with nothing made gives its epoch back too). This replaces any
  per-resource dependency tracking.
- **Claim.** Under the engine's `applyMu` (the same serialization as apply,
  repair, retire and resolve, `internal/core/lifecycle/apply.go`), in one
  write transaction: the checks above, `takeLocks` for the change's keys
  without incrementing their epochs (`lock.held` naming the holder), state
  `undoing`, `recovery_owner = undo`, journal phase `undo`.
- **Run** (in a task unit for a task action, 1.3c; in process otherwise),
  newest effect first, stopping at the first effect that does not end
  `undone` or `skipped`, so an earlier effect is never undone while a later
  one is in doubt or kept. An `accepted` effect stops the run like an
  `unresolved` one; the effects before it stay `made`.
  - an effect found `undoing` (a crash): `InverseLocal` → observe:
    State-equal to `UndoTo` → `undone`; State-equal to `Post` → `made`
    and continue below; anything else → `unresolved`; not `InverseLocal` →
    `unresolved` (`uncertain`);
  - a `made` effect: observe; not State-equal to `Post` → `unresolved`
    (`conflict`); else record `undoing` with `UndoTo`, call `Inverse`;
    no error → observe: State-equal to `UndoTo` → `undone`, else
    `unresolved` (`conflict`); a `Definite` error → `made`; any other
    error: `InverseLocal` and observed State-equal to `Post` → `made`, otherwise
    `unresolved` (`uncertain` when not `InverseLocal`).
- **Settle** (one transaction, owner cleared): every effect `undone` or
  `skipped` → `reverted`, locks released, epochs given back as above; the
  run stopped at an `accepted` effect and every later effect is `undone` or
  `skipped` → **`kept`** (terminal: the accepted state and the effects
  before it stay; never treated as reverted), locks released; otherwise
  `needs_repair`, locks kept. An operator who wants the earlier effects
  undone resolves the effect with `reconcile` instead of `accept`.

Authorization: the action's scope (1.7) for a user. `system:recovery`
(composite unwind and recovery) is accepted by the principal checks for
undo, lookup, retire, revert, confirm and release of the changes it
recovers; for a step of a composite only together with the core-level
`call.parent` naming that composite (1.5), on the controller's session.

**Resolving an unresolved effect.** First a read-only preview,
`darkpanel change effects <id> --observe <seq>`
(`GET /api/v1/changes/{id}/effects/{seq}/observe`, relayed to the agent's
same path, scope `admin`): it calls `Observe` and answers
`{effect, observation, allowed}` (`allowed` is `Observation.Allowed`, or
both resolutions), changing nothing. Then `darkpanel change resolve <id>
--effect <seq> accept|reconcile [--resource <id>]`
(`POST /api/v1/changes/{id}/effects/{seq}/resolve`, relayed to
`POST /agent/v1/changes/{id}/effects/{seq}/resolve`), scope `admin`,
journaled as phase `resolve`. Allowed when the effect is `unresolved`, its
change is `needs_repair` (or is a `failed` repair change whose target is
`needs_repair`, 1.3), and `CommitBy` has passed (else `core.change_busy`,
"the call may still commit until <time>"). Under `applyMu` the engine
claims, with one conditional update, `recovery_owner = resolve` on the
change that holds the locks (the change, or the repair change's target),
which fails with `core.change_busy` when another mechanism owns it; it
then calls `Observe` and, in one transaction that re-checks the effect is
still `unresolved`, records the outcome, calls the module's `Resolved`
when it implements `Resolver`, and clears the owner:

| Resolution | Result |
|---|---|
| `accept` | `accepted`, the observation in `Current`: the current state is kept as the end state |
| `reconcile` | the effect is brought in line with what is there now: observation State-equal to `UndoTo` (computed as above) → `skipped`; a creation with more than one match → refused, `request.invalid_input` ("the identity matches N objects"), unless `resource` names one of `Candidates` and `Allowed` includes `reconcile`; otherwise `made` with `Post` the observation (and the matched or named resource), so the next undo or repair runs the inverse from what is there now |

A resolution not in `Allowed` is refused with `request.invalid_input`. A
resolution never runs an inverse or any write. The change's locks are
held and the change is owned throughout, so neither another Darkpanel
change, a release nor a second resolution acts between the observation
and the record.

**Failure of a compensable or irreversible change** (the engine settles
`intended` effects first):

| Outcome | Effects after settle | Change | Locks |
|---|---|---|---|
| Apply and Verify succeed | — | `completed` | released |
| Apply or Verify returns an error (including `core.deadline_exceeded`), the action has a module recovery handler (1.3b) | none `unresolved` | claimed `recovering`, owner `module`, and the handler runs at once; its outcome decides | kept until then |
| the same, no module recovery handler | none `unresolved` | `failed` | released |
| either | an `unresolved` effect | `needs_repair` (with `recovery_retry` set when the action has a module recovery handler) | kept |
| cut off (startup recovery, 1.3) | as the rows above, with `interrupted` in place of `failed` and the verify message as today | | |

The error stored on a change that enters `needs_repair` is always
`core.needs_repair`; `ChangeError` gains `Cause`, the code of the error
that led there (`cloud.uncertain_outcome`, `change.revert_failed`, a
handler's code), and its message names the unresolved effects. A
`failed` repair change stores `core.needs_repair` with its cause the same
way.

**Failure of a revertible change.** `Reverter.Revert` changes to

```go
// RevertOutcome is what a revert did. The engine, not the handler, writes
// the journal: it applies Effects in the transaction that records the
// revert.
type RevertOutcome struct {
    Effects  []EffectOutcome // one per effect the revert acted on or left
    Override bool            // the connectivity exception (below)
    Detail   json.RawMessage // Override: what was overwritten, shown to the operator
}

type EffectOutcome struct {
    Seq     int
    State   EffectState // "undone" | "unresolved"
    Reason  string      // unresolved: "conflict" (the resource differed from Post) or "uncertain"
    Current *StateDoc   // unresolved: what the revert observed and left in place
}

Revert(ctx context.Context, env Env, snapshot []byte) (RevertOutcome, error)
```

and `ErasedHandler.Revert` to the same result. **One precedence rule**
governs a revert's writes, in this order:

1. An effect already `unresolved` when the revert starts blocks the whole
   revert: `Revert` restores nothing and returns an error, **unless** the
   connectivity exception holds: the action's lockout guard, evaluated on
   the state the change left, fails for a protected flow. Then, and only
   then, `Revert` restores the snapshot whole, reports every effect it
   overwrote as `unresolved` with what it overwrote in `Current`, and
   returns `Override`. This is the only case in Darkpanel where an
   unresolved effect is overwritten; undo, unwind, release, resolution and
   recovery handlers never do it.
2. With no effect unresolved at the start, `Revert` restores each effect;
   an effect whose resource no longer equals its `Post` is left as it is
   and reported `unresolved` (`conflict`), and the others are restored and
   reported `undone`. When any effect was left in place, or a write
   failed, the lockout guard is evaluated again on the state the revert
   leaves; if it fails for a protected flow, the connectivity exception of
   rule 1 applies to that state (whole restore, `Override`).

| Trigger | `Revert` returns | Change | Locks |
|---|---|---|---|
| failed apply or verify, timer, operator revert, startup recovery | no error, every effect `undone` | `reverted`, epochs given back | released |
| the same | no error, an effect `unresolved`, no `Override` | `needs_repair`, cause `change.revert_failed`, the message naming the effects left in place | kept |
| the same | no error, `Override` | `needs_repair`, journal result `override`, `Detail` stored as `core_changes.override_json` | kept |
| the same | an error | `needs_repair`, cause `change.revert_failed` | kept |

**Verify and the revert watchdog.** Verify runs under the change's
deadline (1.3) and is never abandoned: a Verify that does not return keeps
its change `applying` and its locks held. Wave 0 deletes the engine's
`VerifyTimeout` abandonment (`internal/core/lifecycle/guard.go`) and
rewrites the `Mutator.Verify` comment in `contract.go` accordingly. A
revertible change must still be restored on time even when its Apply or
Verify hangs (R41, S1), so the engine arms the change's revert timer in
the intent transaction, at the revert deadline it records there (as
today, intent time plus the confirmation timeout), rather than after
Verify. When the timer fires and the change is `pending`, it reverts as
today. When it fires and the change is still `applying`, the handler has
not returned and cannot be reverted around: the process logs the change
id, writes the journal entry `revert-watchdog` and exits with status 70.
Both service units declare status 70 a successful exit that forces a
restart (`SuccessExitStatus=70`, `RestartForceExitStatus=70`), so systemd
restarts the process 2 s later without failing the unit or starting
`darkpanel-rollback.service`, and startup recovery reverts every
unfinished revertible change (1.3). The restarts still count toward
`StartLimitBurst`, so a binary that exits with 70 at every start is still
rolled back; but each watchdog exit needs a new revertible change whose
deadline is at least `MinConfirmTimeout` (10 s) after its apply, so
watchdog exits alone never make six starts within `StartLimitIntervalSec`
(60 s). Both units also set `RestartMode=direct` (systemd 254 and later;
older versions ignore it), so a crashing service starts
`darkpanel-rollback.service` only once its start limit fails it. The exit
also interrupts every other in-process apply, which startup recovery settles
like any cut-off apply; task units keep running. The controller's
revertible actions follow the same rule.

**Sealed input.** The plan's sealed input (`core_plans.input_ct`) is kept
until the change reaches `completed`, `confirmed`, `reverted`, `failed`,
`interrupted`, `kept`, `repaired` or `abandoned`; recovery handlers and
task units read it while the change is unfinished. Undo never reads it
(it works from the journal alone), so undo after completion needs no
input.

Each action's section states what its undo restores and what it cannot.

#### 1.3 Composite operations

A composite is a controller-side action whose apply runs **steps**: child
changes of other modules' actions on one or more hosts.

- **Declaration.**

  ```go
  const ClassComposite Class = "composite"

  type Unwind string // "reverse" | "forward"

  // Step is one step of a composite.
  type Step struct {
      ID     string          `json:"id"`              // [a-z][a-z0-9]*, unique in the composite
      Host   string          `json:"host"`            // host id; "" for a controller-side action
      Action ActionRef       `json:"action"`          // "<name>/v<version>"
      Input  json.RawMessage `json:"input"`           // the child's input, with references (below)
      After  []string        `json:"after,omitempty"` // steps this one follows
  }

  // Composer is the handler of a composite action.
  type Composer[In, Out any] interface {
      // Steps returns the steps for in. It reads only in and the controller
      // database, never a host.
      Steps(ctx context.Context, env Env, in In) ([]Step, error)
      // Output builds the composite's output from its steps' redacted
      // outputs, keyed by step id; read steps are not among them.
      Output(ctx context.Context, env Env, in In, steps map[string]json.RawMessage) (Out, error)
  }

  // DefineComposite declares a composite: Class composite, Side controller,
  // LockKeys ["composite:<module>"], spec.Unwind set.
  func DefineComposite[In, Out any](spec ActionSpec, h Composer[In, Out]) ActionDef
  ```

  `ErasedHandler` gains `Steps` and `Output`. The engine, not the module,
  plans, applies and recovers a composite. Steps run in list order; `After`
  constrains references and is checked against that order.
- **References.** `${input.<path>}` and `${steps.<id>.output.<path>}` are
  the only references; `<path>` is dot-separated object keys and array
  indexes. A string that is exactly one reference becomes the referenced
  value with its JSON type; a reference inside a longer string is replaced
  by the value's text (strings and numbers only); `$${` is a literal `${`.
  `${input...}` is resolved at plan time (a missing path: `request.invalid_input`);
  `${steps...}` when its step is planned, against the referenced step's
  unredacted output (sealed in the step row), a missing path failing the
  step with `core.child_failed`. A step may reference only steps it follows
  through `After`, directly or not. The resolved input is validated against
  the child action's schema before the child is planned. **Secrets never
  enter a template:** a composite's input type has no `secret:"true"`
  field (registry rule), and at plan the engine refuses (`internal`, a
  module bug) a step whose template, after `${input...}` resolution, holds
  a literal value in a field the child's input schema marks secret; such a
  field may only be a `${steps...}` reference, whose value goes only into
  the sealed resolved input. Templates are therefore stored and previewed
  in clear.
- **Registry rules** (and the engine's check of every `Steps` result at
  plan time; a violation is a module bug, `internal`):
  - a step is never a composite and never `irreversible`; a step may be a
    read action (a read step, below);
  - at most one **revertible** step, and it is the last step;
  - `reverse` (every adding composite: deploys, exposes, joins): every
    compensable step has an undo handler;
  - `forward` (every removal composite: `panel.exposure.remove`,
    `npm.expose.remove`, `npm.admin.unexpose`, `cloudflare.expose.remove`,
    `derp.server.remove`): every non-read step's action has `Converges`,
    because repair repeats it; a forward composite never unwinds, so its
    steps' compensation does not matter to it;
  - the composite holds `composite:<module>` for its whole life; no other
    action declares a `composite:*` key;
  - other rules checked on every action: a compensable action has exactly
    one of an undo handler, `CompensatedBy` or `ForwardOnly`;
    `ForwardOnly` requires `Converges`; an undo handler only on a
    compensable action with at least one lock key (its undo window is
    defined over them); `ApplyTimeout` above 45 s requires `LongRunning`;
    a revertible action has `RepairedBy`, an action of the same module
    whose lock keys are a subset of its own, whose input accepts `{}`
    (the repair request may add fields, below), which is compensable,
    irreversible or revertible (a revertible repair action may name
    itself; owner ruling R1), and which is neither a
    task action nor has a module recovery handler; a task action is
    compensable or irreversible, agent-side, with lock keys and an
    `ApplyTimeout`.
- **Scope.** Every controller mutation needs `admin` (1.7), so every
  composite is `admin`.
- **Preview.** The plan dry-runs every step without an output reference
  (its guards are the composite's; the state hash covers the input and
  their state hashes) and shows each other step as "planned after step
  `<id>`" with its input template. Dry-run plans are stored like any plan, never applied, and expire after 10 minutes.
- **Step log and cursor.** At apply, in the intent transaction, the
  controller writes one row per step to `core_composite_steps` (composite
  change id, step id, ordinal, host, action ref, input template, state
  `pending`, attempt 0, child plan id, child change id, earlier attempts'
  child change ids, sealed resolved input, sealed unredacted output
  `output_ct` with associated data
  `core_composite_steps.output_ct.<composite id>.<step id>`, error) and a
  row to `core_composites` (change id, direction `forward` or `unwind`,
  cursor = the ordinal being worked on, steps deadline). Step states:
  `pending → planning → planned → applying → applied` (`→ confirming →
  confirmed` for the revertible last step), read steps `pending → read`,
  on unwind `undoing → undone`, and `released`. Each boundary is written
  and fsynced before the act it announces: `planning` before the plan
  request (the executing side assigns the plan id, so none is known yet);
  `planned` with the child plan id once the plan answered; `applying`
  before the apply request; `confirming` before the confirm. A plan whose
  answer was lost is never applied, because only the composite applies
  child plans and it applies only a recorded plan id; it expires unused
  after 10 minutes. The cursor advances in the transaction that marks a
  step final. Every committed step state is published as a
  `composite.step` event (1.6).
- **Child results, including secrets.** A child change whose output type
  has secret fields stores its unredacted output sealed in
  `core_changes.private_output_ct` (associated data
  `core_changes.private_output_ct.<change id>`), on its executing side, in
  the transaction that records its result. The composite takes it from the
  apply response or, after a lost response, from the lookup with
  `private=1`; it seals it into the step row, then acknowledges it
  (`POST /agent/v1/changes/{id}/private-output/ack`, or a delete for a
  controller-side child), which clears the column; an unacknowledged
  private output is cleared after 24 hours. A completed step whose private
  output is gone before it was sealed is **output-lost**: the reverse
  composite unwinds (the child is undone, so an auth key is deleted, not
  orphaned) and ends with `core.child_failed`; a composite never continues
  with a redacted value.
- **Read steps.** Executed by the controller's read dispatch (for an
  `Internal` read, 1.7's controller-only path) when the cursor reaches
  them; no plan, no change, no effects. The result is sealed into the step
  row and never appears in the composite's output, the lookup, audit or
  the API. A read step that is not yet `read` is read again when the
  cursor reaches it; unwind skips read steps.
- **Asynchronous apply.** Every composite and every `LongRunning` action
  (section 5 marks them `L`) answers `202` once the change and its intent
  are persisted (state `applying`); progress comes from the change, the
  step view and the SSE events (1.6), to which `--confirm`, the CLI's wait
  and the UI countdown attach. The controller-to-agent apply of such an
  agent action likewise answers once the intent is persisted, and the
  controller follows the lookup and the agent's journal. A synchronous
  action answers within 50 s: whatever is still running then, the API
  answers `202` with the change.
- **Deadlines.** `ApplyTimeout` (0 means 30 s) is one budget for apply and
  verify; the engine runs both under that deadline, and the runner's own
  30 s default applies only to a context without one
  (`internal/platform/exec.go`). Long-running actions declare theirs:
  package installs, updates and pins 15 min; image pulls and Compose
  operations 30 min; ACME issuance 10 min; the NPM bootstrap and upgrade
  20 min; derper install and update 15 min. A passed deadline cancels the
  command (process group killed, as today) and fails the change with
  `core.deadline_exceeded`. Cleanup (settle, revert, the recording
  transaction) runs under `context.WithoutCancel` with the action's
  `ApplyTimeout` for handler calls and 30 s for the transaction, never on
  the expired context. A handler that ignores its context keeps its change
  owned and locked until it returns or the process restarts. A
  composite's steps phase is bounded at plan by the sum of its steps'
  `ApplyTimeout` plus 5 min: then it starts no further step, waits for the
  current child under the child's deadline, and unwinds (reverse) or goes
  `needs_repair` (forward) with `core.deadline_exceeded`; the confirmation
  window (the revertible child's deadline, at most 15 min) is not part of
  it, and unwind and repair have none. Only process shutdown (SIGTERM)
  cancels an in-process apply; a cancelled request never does (`Apply`
  runs under `context.WithoutCancel`). Task units keep running (1.3c).
- **Lookup and retire.** Two agent routes (and the same functions
  in-process for controller-side children), accepted only on the
  controller's session:
  - `GET /agent/v1/plans/{plan id}` returns `PlanLookup` and never changes
    anything;
  - `POST /agent/v1/plans/{plan id}/retire`: under the engine's `applyMu`
    (so it never interleaves with an apply's reads and intent) and in one
    transaction, when no change exists for the plan, marks the plan
    retired, so a later apply of it is refused with `plan.not_found`, and
    answers status `retired`;
    otherwise answers the lookup. The composite retires a child plan whose
    lookup says `absent` before it treats the step as never applied, so a
    delayed apply request can never create the child afterwards.

  ```go
  type PlanLookup struct {
      Status        string          `json:"status"` // "absent" | "retired" | "running" | "recovering" | "terminal"
      ChangeID      string          `json:"changeId,omitempty"`
      State         string          `json:"state,omitempty"`
      Owner         string          `json:"owner,omitempty"`      // recovery_owner
      Deadline      *time.Time      `json:"deadline,omitempty"`   // a pending revertible child's revert deadline
      Made          int             `json:"made"`                 // effects made or undoing
      Unresolved    int             `json:"unresolved"`
      RepairOutcome string          `json:"repairOutcome,omitempty"`
      Output        json.RawMessage `json:"output,omitempty"`     // redacted
      Private       json.RawMessage `json:"private,omitempty"`    // only with ?private=1 while private_output_ct is set
      Error         *ChangeError    `json:"error,omitempty"`
  }
  ```

  `running`: owned by `engine`, `undo` or `repair`. `recovering`: owned by
  `module`. `terminal`: no mechanism owns it (`State` says which state).
- **States.** ADR 0014 adds class `composite` to the shared contracts and
  the change states `awaiting_confirmation`, `undoing`, `needs_repair`,
  `recovering`, `repaired`, `kept`, `abandoned`, `quarantined` and, for
  composites only, `releasing` (`applying` exists already). A composite is
  `applying`, `awaiting_confirmation`, `undoing` (unwinding), `recovering`
  (an agent it waits on is unreachable), `needs_repair`, `releasing`, or
  terminal: `completed`, `reverted` (unwound; error `core.child_failed` or
  `core.deadline_exceeded` naming the step), `failed` (a reverse composite
  whose first non-read step failed with nothing to unwind) or `abandoned`.
  `Dispatcher.Confirm` and `Revert` on a composite act on its revertible
  child. The CLI and UI treat a composite with a revertible step like a
  revertible action and one without like a compensable action.
- **Ownership.** Column `core_changes.recovery_owner`: empty, `engine` (an
  in-process apply, verify or revert), `composite`, `undo`, `module` (a
  recovery handler, 1.3b), `repair` (a repair change runs under this
  change's locks) or `resolve` (an operator's resolution, 1.2). Every
  claim is one conditional update (`UPDATE
  core_changes SET recovery_owner = ?, state = ? WHERE id = ? AND
  recovery_owner = ? AND state = ?`), failing with `core.change_busy` when
  it matches no row. Ownership ends only in the transaction recording the
  owner's outcome, after its handler returned and, for a task unit, after
  the unit was seen inactive (1.3c); a process restart ends in-process
  ownership and startup recovery re-claims the change. The undo, repair,
  release and resolve routes refuse an owned change (`core.change_busy`).
- **Repair of a single change** in `needs_repair`
  (`darkpanel change repair <id>`, `POST /api/v1/changes/{id}/repair`,
  relayed to `POST /agent/v1/changes/{id}/repair`; the agent decides, so
  the repair target never travels as client input). The first row that
  applies decides:

  | The change | Repair | Answer |
  |---|---|---|
  | an `unresolved` effect on it or on a `failed` repair change of it | refused, `core.needs_repair` ("resolve its effects first"); this check comes first, so no mutating recovery runs over an effect in doubt | — |
  | `recovery_retry` set (its recovery handler ended with an error or stopped at an unresolved effect, 1.3b) | runs the recovery handler again, owner `module`; nothing else is admissible until it records an outcome without `recovery_retry` | `202` change |
  | compensable with an undo handler | undo (1.2) | `202` change |
  | revertible | the agent plans its `RepairedBy` action with the request's `input` (default `{}`) and the core-level plan field `repairs: <id>` | `200` plan; the client applies it through the ordinary apply route |
  | compensable with `Converges`, no undo handler, not a task action and without a module recovery handler | the agent plans the same action with its stored input and `repairs: <id>` | `200` plan |
  | anything else | none, `change.not_pending`; release still applies | — |

  `repairs` is set only by this route, from the change id in its path: a
  plan request that supplies it, and a repair request's `input` that
  contains it, are refused with `request.invalid_input`. It is stored in
  `core_plans.repairs`, and the engine derives `Env.Call.RepairOf` (1.5)
  from it. The repair request's optional `input` (an object, validated
  against the `RepairedBy` action's schema) lets the operator choose
  among that action's options (for example `outcome: restore | keep`,
  4.1); the controller's relay fills `guard` and stores `call` for the
  repair plan exactly as `Dispatcher.Plan` does for an ordinary plan, so
  the preview evaluates the lockout guard on the controller's topology
  too. An `interrupted` change of a `Converges` action is finished by
  running the action again with fresh input (8).
- **Repair change P of R** (depth 1). Apply of a plan with `repairs: R`
  runs under `applyMu`; its intent transaction re-checks, and refuses with
  `core.needs_repair` or `core.change_busy` otherwise, that R is
  `needs_repair`, unowned, without `recovery_retry`, and that neither R
  nor any `failed` repair change of R has an `unresolved` effect (a second
  plan made before an earlier attempt failed is caught here). R's lock
  rows then move to P (P's lock keys are a subset of R's; P's intent
  increments their epochs), R gets `recovery_owner = repair` and
  `repaired_by = P`, so the keys are never free in between. The
  `RepairedBy` action's output carries `repairOutcome`: `restored` (the
  host is back in R's before-change state) or `kept` (in R's intended
  state); a `Converges` re-apply always reports `kept`. Both are relative
  to R, because depth is 1. P is never repaired, undone or released
  (`core.change_busy` naming R):

  | P reaches | R becomes | Locks |
  |---|---|---|
  | `completed` or `confirmed` | `repaired` with P's `repairOutcome`, owner cleared | released with P |
  | `failed` or `reverted` with no `unresolved` effect and no override | `needs_repair`, owner cleared | moved back to R in P's terminal transaction |
  | cut off by a restart (startup recovery finds P `applying` or `pending`) | P is settled and reverted or recorded like any cut-off change, then the rows above and below apply, with `interrupted` recorded as `failed` | as above |
  | any outcome that would leave P `needs_repair` (a failed or overriding revert, an `unresolved` effect) | P is recorded `failed` with error `core.needs_repair` instead; R stays `needs_repair`, owner cleared; R's effects view lists P's `made` and `unresolved` effects, which are resolved on P | moved back to R |

  P never has a module recovery handler and is never a task action
  (registry rule), so no recovery responsibility can be left on P. P's
  `made` effects are not undone by any mechanism (P is never undone): for
  a `Converges` re-apply that is harmless; for a revertible P whose revert
  failed, the host state between R's and P's intents is the operator's,
  shown in R's effects view. A further repair of R is a new P, never a
  repair of P.
- **Release** (`darkpanel change release <id>`,
  `POST /api/v1/changes/{id}/release`, body `{"reason": "<1–500
  characters>"}`, scope `admin`, journaled as phase `release`) gives up a
  change in `needs_repair`. It is refused (`core.change_busy`) for a repair
  change (release its target), for a step of a composite (release the
  composite), and while the change or any of its repair changes is owned
  or has a live task unit.
  - A single change: one transaction on its executing side marks every
    non-terminal effect of the change and of its `failed` repair changes
    `abandoned`, moves the change to `abandoned` and releases its locks;
    the answer lists the abandoned effects.
  - A composite: the composite goes `releasing` (owner `composite`, its
    `composite:<module>` lock kept). The runner sends release, as
    `system:recovery` with `call.parent` naming the composite, to every
    child in `needs_repair`, retrying through the watcher until each
    executing side answers `abandoned` or `terminal`; a child answering
    `core.change_busy` (an owner, such as a repair already running, or a
    live task unit) is retried until that owner has recorded its outcome.
    The executing side accepts a child's release only from its parent
    this way. The controller refuses a child repair, and the apply of a
    plan whose `repairs` names a child, while the parent is `releasing`;
    a repair plan made earlier and applied on the agent directly is
    refused there because the child, once released, is no longer
    `needs_repair`. When every child has answered, step rows go `released`
    and the composite `abandoned`, releasing its lock.
  - A repeated release of an `abandoned` change answers it.
- **Locks across restarts and updates.** Startup recovery keeps the locks
  of changes in `applying`, `pending`, `awaiting_confirmation`,
  `undoing`, `needs_repair`, `recovering`, `releasing` and `quarantined`,
  and of every owned change. Update reservations are refused while any
  lock is held (ADR 0012), so a host with such a change refuses
  self-updates; `Reserve` keeps its codes (`update.in_progress` only while
  another operation holds `*`; `update.unconfirmed_changes` while a change
  holds a lock or a revertible change is unconfirmed) and wave 0 extends
  that refusal's detail to name the first held key, its owning change and
  that change's state; the update scheduler shows the detail.
- **Confirmation (R41).** When the last step is revertible and its child is
  `pending`, the composite is `awaiting_confirmation`. Confirm writes the
  step row `confirming` (fsync), then sends the child's confirm. The child
  `confirmed` (or a repeated confirm answered `change.not_pending` while
  the lookup shows `confirmed`) makes the step `confirmed` and the
  composite `completed`. A lost answer is settled by the lookup, never by
  a deadline.
- **Child watcher.** While a composite is `awaiting_confirmation`,
  `recovering`, `releasing`, has a step `confirming` or a child in
  `needs_repair` or `recovering`, the controller follows the child: the
  agent's journal entries of phases `apply`, `interrupt`, `revert`,
  `recover`, `undo`, `resolve` and `release` wake it through the existing
  audit ingester, and it polls the lookup every 15 s. When the agent is
  unreachable it waits until the child's deadline (if any) plus 60 s, then
  marks the composite `recovering` and polls every 30 s; it acts only on a
  state the lookup has shown.
- **Transitions.** Every observation of the cursor step's child, live or
  at recovery, is applied through these tables, chosen by the persisted
  direction. Recovery never creates a new attempt or plans a step; new
  attempts come only from an operator's repair of a forward composite.
  `absent` is always first retired; a retire answering `retired` is read
  as `retired`, any other answer is looked up again in the same table.

  Direction `forward` (executing the steps), cursor step state `applying`
  or `confirming`:

  | Child observed | Reverse composite | Forward composite |
  |---|---|---|
  | `retired` | unwind from the step before (`failed` when there is none) | `needs_repair` at this step |
  | `running` or `recovering` | wait | wait |
  | `completed` (compensable), `confirmed`, or `repaired` with `kept` | step final; advance the cursor; live: plan the next step; at recovery: as "between steps" below; the last step: `completed` | same |
  | `pending` (the revertible last step) | step `applied`; `awaiting_confirmation` (a step `confirming`: resend the confirm) | same |
  | `reverted`, or `repaired` with `restored` | unwind from the step before | `needs_repair` at this step |
  | `failed` or `interrupted`, `Unresolved` 0 | unwind, starting with this step | `needs_repair` at this step |
  | `needs_repair`, or any state with `Unresolved` > 0 | `needs_repair` at this step | same |
  | `kept` or `abandoned` | `needs_repair`; only an operator's step decision (below) moves on | same |
  | `completed`, private output lost | unwind, starting with this step; `core.child_failed` | — (removals have no secret outputs) |

  Direction `unwind` (reverse composites only), cursor step state
  `undoing`:

  | Child observed | Reverse composite |
  |---|---|
  | `retired` | step `undone`; cursor to the step before |
  | `running`, `recovering` or `undoing` | wait |
  | `completed` (an undo not yet claimed) | send the undo again |
  | `pending` | send `Revert` again |
  | `reverted`, or `failed` with `Made` 0 and `Unresolved` 0 | step `undone`; cursor to the step before |
  | `confirmed` (the revertible last step) | stop the unwind; the composite is `completed` (confirmation won) |
  | `needs_repair`, `kept`, `abandoned`, `repaired` (either outcome), or `Unresolved` > 0 | `needs_repair` at this step; only repair or an operator's step decision moves on |

  **Between steps** (recovery only): a cursor step `pending`, `planning`
  or `planned` (no apply was sent; a `planned` plan is retired first), or
  a read step not yet `read`: forward direction → reverse: unwind from the
  step before (`failed` when none was applied); forward composite:
  `needs_repair`; unwind direction → the step counts `undone` (it was
  never applied) and the cursor moves to the step before. Every step final
  in the forward direction and no revertible step → `completed`; the last
  step `applied` with its child `pending` → `awaiting_confirmation`, and
  the watcher resumes.
- **Unwind** (reverse composites only, owner `composite`, direction
  `unwind`). It starts only when every applied step's child can be undone
  cleanly: the lookups show no `unresolved` effect, no child in
  `needs_repair`, `kept` or `abandoned`, and no child owned; otherwise the
  composite goes `needs_repair` before anything is undone. Then, from the
  cursor backwards:
  - read step: nothing;
  - the revertible last step, first: a `pending` child is sent `Revert` and
    the unwind waits for the lookup to show `reverted`; a child found
    `confirmed` stops the unwind and the composite is `completed`
    (confirmation won); an unreachable agent leaves the child to its own
    timer and the composite `recovering`; a child in `needs_repair` stops
    the unwind (`needs_repair`);
  - a compensable step: undone through the undo route as
    `system:recovery` with `call.parent`; the child `reverted` → step
    `undone`; `absent` (retired) → `undone`; any other outcome
    (`needs_repair`, `kept`, `core.undo_conflict`) stops the unwind
    (`needs_repair`). An undo refused with `core.undo_conflict` means a
    change outside the composite took the key since; the operator either
    undoes that change first or decides the step (below).

  When every step is settled the composite is `reverted`. This refines ADR
  0004 for composites only: the controller unwinds automatically because
  every undo acts only on journaled effects and checks them first.
- **Repair of a composite** (`darkpanel change repair <composite id>`,
  scope `admin`, claimed with owner `composite`) depends on its direction:
  - **reverse**: resumes the unwind from the cursor (direction `unwind`),
    with the same start check and the unwind table; it never applies
    anything, and a child it meets in `needs_repair` is repaired on its
    own first (its repair is a single-change repair, refused while the
    composite is `releasing`);
  - **forward**: continues forward from the cursor step. Its child is
    looked up: `completed`, `confirmed` or `repaired` with `kept` → advance;
    `absent` (retired), `reverted`, `repaired` with `restored`, or
    `failed`/`interrupted` with `Unresolved` 0 → a **new attempt**: the
    input is resolved again from the step log, planned afresh by the
    repairing principal and applied as a new child (the step's `Converges`
    action ends in the same state whatever the earlier attempt did); the
    attempt number and the earlier child change id are recorded in the
    step row; `needs_repair`, `Unresolved` > 0, `kept` or `abandoned` →
    refused with `core.needs_repair` naming the child (repair it first, or
    decide the step).
  - **Step decision** for a child in `kept` or `abandoned`, or (reverse,
    met during unwind) a child `repaired` with either outcome or a child
    whose undo was refused with `core.undo_conflict`:
    `darkpanel change repair <composite id> --step <id> --as
    reverted|applied` (admin, audited, the composite unowned). `reverted`
    counts the step as undone (reverse: the operator takes responsibility
    for whatever the child left; the unwind continues with the step
    before) or not done (forward: the next repair makes a new attempt);
    `applied` counts it as done (forward only).

  A stored child plan is never applied after a restart or by another
  principal (plans expire after 10 minutes and are bound to their
  principal); stored plan ids serve only the lookup and retire. A child
  change's actor is the principal that planned its attempt (the
  composite's principal for the first), with `parent_change_id` set.
- **Change recovery at start** (`Engine.Recover`, both sides; ADR 0004's
  pass, extended). The synchronous part calls no handler, so a handler
  that hangs can never keep the process from serving:
  1. Seed ids, as today.
  2. Claims, in one transaction per change (owners left by the previous
     process are void: a restart ends in-process ownership):
     - class `composite`: skipped (composite recovery, 1.3a);
     - a task action in `applying` or `recovering`: owner `module`, state
       `recovering` (1.3c's sequence decides);
     - a revertible change in `applying` or `pending`, whatever its
       deadline (as today: every unconfirmed revertible change is
       reverted at start): owner `engine`, to be reverted;
     - a change in `applying` with `repairs` set, or any other compensable
       or irreversible change in `applying` or `recovering`: owner
       `engine`, to be settled;
     - a change in `undoing`: owner `undo`;
     - a `needs_repair` change with `recovery_retry` set and no
       `unresolved` effect: owner `module`.
  3. The lock sweep deletes only the locks of changes in none of the
     states 1.3 keeps ("Locks across restarts"); reservations (`op:`
     owners) are untouched, as today. Changes in `quarantined` keep their
     locks and are not otherwise touched (10).

  Then **admission** opens: the agent serves its socket and dials the
  controller, so reads, `hello`, the lookup, journal sync and streams
  work, while plan, apply, undo, repair, release and resolve answer
  `core.recovering` until the background pass has started every claimed
  change's work. The **background pass**, one goroutine per claimed
  change: a revertible change is settled and reverted (1.2's revert
  table); a change to be settled has its `intended` effects settled
  (`Observe` for `Local` ones), then follows 1.2's failure table with
  `interrupted` for `failed`, its message from Verify run with a 30 s
  bound (the outcome is already decided and Verify only reads, so a
  Verify that does not return within it is reported "verify did not
  finish" and left running); for an action with a module recovery
  handler, the handler runs only after the settle, and only when no
  effect is `unresolved` (else `needs_repair` with `recovery_retry`); an
  `undoing` change resumes its undo run; a task change follows 1.3c. A
  handler that takes long keeps its change owned and locked, so
  conflicting work is refused with `lock.held` or `core.change_busy`. A
  task unit's health check, which needs the agent's controller session
  (4.1), can therefore complete while its change is still recovering.
- **1.3a Controller start order.** The controller runs `Engine.Recover`,
  then opens its agent listener and serves agent traffic and reads, then
  runs composite recovery in the background (for every non-terminal
  composite, claimed with owner `composite`, by its step log, cursor and
  the transition table); user and token mutations answer `core.recovering`
  until composite recovery has finished or left only `recovering`,
  `awaiting_confirmation` and `releasing` composites, which it keeps
  following. A controller in **restore quarantine** (10) runs
  `Engine.Recover` (whose sweep keeps quarantined records' locks) but
  performs no composite recovery and starts no automatic mutation source
  until the operator releases it.
- **1.3b Recovery handlers.** Some actions keep a durable transaction of
  their own and must finish or roll back after a crash:
  `npm.instance.upgrade` (6), `derp.binary.update` (4.3),
  `tailscale.package.pin` (4.1) and Cloudflare hostname changes (7.3).
  A task action without a module recovery handler is settled by 1.3c's
  sequence alone.

  ```go
  // RecoveryOutcome is a recovery handler's verdict on its change.
  type RecoveryOutcome struct {
      State  string          // "completed" | "failed" | "rolled_back" | "needs_repair"
      Output json.RawMessage // completed: the action's output
      Reason string          // failed, needs_repair: shown to the operator
  }

  // Recoverer finishes or rolls back an action's own transaction after a
  // crash. env.ChangeID is the change; env.Effects its journal.
  type Recoverer interface {
      Recover(ctx context.Context, env Env) (RecoveryOutcome, error)
  }
  ```

  - **When it runs.** At start (1.3, background pass), after a live
    apply or verify failure (1.2's failure table), and on repair while
    `recovery_retry` is set. In every case the engine first settles the
    change's `intended` effects; when any effect is then `unresolved` the
    handler does not run, the change goes `needs_repair` with
    `recovery_retry` set, and repair runs the handler once the operator
    has resolved them. A handler therefore never writes while an earlier
    call may still commit.
  - **Ownership.** The handler runs only on a change claimed with owner
    `module`; it runs as the change's lock owner and takes no lock. For a
    task action it runs inside a task unit with op `recover` (1.3c).
  - **Outcome.** The engine records it and clears the owner in one
    transaction, after settling any `intended` effect the handler left:
    `completed` → `completed` (output stored, private output as above);
    `failed` → `failed`, locks released; `rolled_back` → `reverted`, locks
    released, epochs given back; `needs_repair` → `needs_repair`, locks
    kept. An outcome other than `needs_repair` with an `unresolved` effect
    left is recorded as `needs_repair`. A returned error is recorded as
    `needs_repair` with `recovery_retry` set, locks kept. Nothing but the
    handler (repair runs it again) may act on a change with
    `recovery_retry` set: undo refuses it and release requires it to be
    unowned.
  - **Timeouts.** The handler's context is cancelled at its action's
    `ApplyTimeout`; the engine keeps waiting for it to return (for a task
    unit, for the unit to be inactive) and only then records the outcome.
    Ownership never lapses.
  - It is idempotent, reads only its own transaction table and journal,
    and a failing handler stops neither other handlers nor startup.

  ADR 0014 records this as the one exception to ADR 0004's "never retried
  on its own" for compensable and irreversible actions. Verify stays
  read-only.
- **1.3c Task units.** The host sequences of `tailscale.package.install`,
  `package.update`, `package.pin`, `package.unpin`, `derp.server.install`
  and `derp.binary.update` (apt, dpkg, binary swaps, service restarts) can
  disconnect or restart the agent, so all of their host sequences, forward,
  undo and recovery, run in a **transient systemd unit** outside the
  agent's cgroup, under one protocol.
  - **Declaration.**

    ```go
    // TaskMutator is the handler of a task action. Task runs inside the
    // apply unit; Plan and Verify run in the agent; Observe and Inverse run
    // wherever the settle or undo run that calls them executes (in the
    // agent, or inside an undo unit); a module Recoverer runs inside a
    // recover unit.
    type TaskMutator[In, Out any] interface {
        Plan(ctx context.Context, env Env, in In) (PlanOutcome, error)
        Task(ctx context.Context, env Env, in In) (Out, error)
        Verify(ctx context.Context, env Env, in In, out Out) error
    }

    // DefineTask declares a task action (registry rules, 1.3) and sets
    // Task and LongRunning. A module Recoverer, if the handler has one,
    // is found by type assertion; otherwise the action has no module
    // recovery handler and the engine's sequence below is all there is.
    func DefineTask[In, Out any](spec ActionSpec, h TaskMutator[In, Out]) ActionDef
    ```

    `ErasedHandler` gains `Task`; a task action's `Apply` is nil and the
    registry checks `Task` is set instead. A task action with an undo
    handler implements `Undoer` too.
  - **Unit.** Name `darkpanel-task-<change id>-<op>-<attempt>.service`,
    op `apply`, `undo` or `recover`, attempt 1, 2, ... per (change, op),
    the only names a task unit has (4.1 and 4.3 use them). Started with
    `StartTransientService` over D-Bus (mode `fail`), `ExecStart`
    `{{.Binary}} agent task <change id> <op> <attempt>`, `KillMode` the
    default, no `OnFailure`, `RuntimeMaxSec` the action's `ApplyTimeout`
    and `CollectMode=inactive-or-failed` (wave 0 adds `RuntimeMax` and
    `CollectMode` to `core.TransientService`, and the pending job to
    `core.UnitState`, in `internal/core/units.go`, 1.8).
  - **Results table** `core_task_runs` (change id, op, attempt, state
    `starting | running | succeeded | failed | cancelled`, output_ct, error
    code, error message, PID, started at, finished at), agent side, key
    (change id, op, attempt). A second undo or recover of the same change
    is a new attempt with a new row and unit name, inserted only when the
    previous attempt's row is terminal and its unit inactive; a row is
    never reset, so a `cancelled` row's fence stays closed for good.
  - **Start and fence.** The engine inserts the row `starting` (in the
    intent transaction for `apply`, in the claim transaction for `undo`
    and `recover`), then starts the unit. The `agent task` program's first
    act is the conditional update `starting → running` (with its PID) for
    exactly its (change, op, attempt), which also requires `core_meta`
    to hold no `uninstalling` flag; when it matches no row it exits
    without doing anything. A start call that errors proves nothing: the
    engine treats the unit as never run only when the row is still
    `starting` and the unit is `not-found`, or `inactive` or `failed` with
    no pending job, on two observations 10 s apart, and then applies the
    conditional update `starting → cancelled`, which fences any later
    start of that attempt. When that update matches no row (the program
    claimed the row between the observations), the engine keeps waiting
    as for a `running` row. Until one of these, it keeps the change owned
    and waits.
  - **The `agent task` subcommand** (hidden) opens the agent database (WAL
    with `busy_timeout`, `internal/core/store/db.go`), the agent's sealer
    from its key files, the production runner and exec allowlist, the file
    writer with the registry's owned paths, the constrained unit client
    (1.8), the system clock and a journald logger. It builds
    `Env{HostID, ChangeID, Principal: {Actor: <the change's actor>, Scope:
    admin}, Call, DB, Exec, Files, Units, Sealer, Clock, Log, Effects}`
    (`Agents` nil) and runs the op: `apply`: reads the plan's sealed input
    (kept while the change is unfinished, 1.2) and runs `Task`; `undo`:
    the engine's undo run of 1.2, from the journal alone (no plan input);
    `recover`: reads the input and runs the module `Recoverer`. It records
    the result in its row: `succeeded` with the output sealed (associated
    data `core_task_runs.output_ct.<change id>.<op>.<attempt>`) or `failed`
    with the error's code and client-safe message. It never changes the
    change's state. Its runner programs and file writes are `Local` once
    its unit is seen inactive; its D-Bus jobs and daemon calls are not
    (1.2).
  - **One sequence** decides a task change, live and after a restart. The
    engine (the apply, the undo claimant or the recovery claimant; in the
    agent) reads the row and the unit's state every 2 s; "inactive" means
    `inactive`, `failed` or `not-found` (a collected unit) with no pending
    job:
    1. row `starting` → the start fence above;
    2. row `running`, unit active → wait;
    3. row `succeeded`, unit inactive → `apply`: Verify in the agent, then
       1.2's failure table (`completed` when it passes); `undo`: the undo
       settle; `recover`: the recorded `RecoveryOutcome`;
    4. row `failed`, unit inactive, or row `running` with the unit
       inactive (the error is then `core.deadline_exceeded` when the
       deadline had passed, `internal` "the task ended without recording a
       result" otherwise), or row `cancelled` → settle the change's
       `intended` effects; then for `apply` or `recover`: when the action
       has a module `Recoverer` and no effect is `unresolved`, start a
       `recover` attempt (back to step 1 with op `recover`); otherwise
       1.2's failure table; for `undo`: the undo settle.
  - **Agent restart.** The waiter dies with the agent; the unit keeps
    running. Startup recovery re-claims the change (`recovering` for an
    apply or recover op, `undoing` for an undo op) and resumes the
    sequence on the newest attempt's row and unit; admission lets the
    session run meanwhile.
  - **Liveness gates.** A task change's owner is cleared and its locks
    released only after its unit was seen inactive or the row `cancelled`.
    Every task action has lock keys, so update reservations are refused
    while its change is unfinished.
  - **Uninstall barrier** (1.11). `darkpanel uninstall` first sets
    `core_meta` key `uninstalling` in the agent database, in one
    transaction that refuses with `core.change_busy` (naming them) while
    any change is owned or any `core_task_runs` row is `starting` or
    `running`, unless `--stop-tasks` is given. From then on the engine's
    plan, apply, undo, repair, resolve and recovery claims answer
    `core.uninstalling`, and the `agent task` fence refuses to start. With
    `--stop-tasks`, uninstall stops each active `darkpanel-task-*` unit
    through D-Bus, waits until it is inactive, waits for each owner to
    record its outcome, and then cancels every row still `starting` with
    the conditional update. It proceeds to stop the services only when no
    row is `starting` or `running`, no change is owned, and
    `Units.List("darkpanel-task-*")` shows no active unit and no pending
    job; otherwise it stops with `core.change_busy` naming what remains.
    A failed uninstall clears the flag on its next plan only when the
    operator runs `darkpanel uninstall --abort`.
- **Wire contract.** All routes are non-frozen and use ADR 0006's error
  mapping; an answer carrying a change uses `ChangeView`, which gains
  `ParentChangeID`, `Owner`, `RepairedBy`, `RepairOutcome` and
  `Override json.RawMessage`; `ChangeError` gains `Cause string` (1.2).
  The agent routes below are served only on the controller's session;
  the agent's local socket serves none of them (break-glass works through
  actions, 1.1), so every undo, repair, release and resolution goes
  through the controller and its admission checks.

  | Route | Request | Answer |
  |---|---|---|
  | `POST /api/v1/changes/{id}/undo`, agent `POST /agent/v1/changes/{id}/undo` | empty | `202` change (`undoing`); `200` when already `reverted` |
  | `POST /api/v1/changes/{id}/repair`, agent `POST /agent/v1/changes/{id}/repair` | `{"input": {...}}` (optional, for a `RepairedBy` plan), or for a composite `{"step": "<id>", "as": "reverted" \| "applied"}` | `202` change or `200` `{"plan": PlanView}` (repair table) |
  | `POST /api/v1/changes/{id}/release`, agent `POST /agent/v1/changes/{id}/release` | `{"reason": string}` (the agent route also accepts the parent's release, `call.parent` set, 1.3) | `200` `{"change": ChangeView, "abandoned": [EffectView]}` |
  | `GET /api/v1/changes/{id}/effects`, agent `GET /agent/v1/changes/{id}/effects` | — | `200` `{"effects": [EffectView]}` (with a repaired change's repair changes' effects) |
  | `GET /api/v1/changes/{id}/effects/{seq}/observe`, agent the same path | — | `200` `{"effect": EffectView, "observation": ObservationView, "allowed": [string]}`; changes nothing |
  | `POST /api/v1/changes/{id}/effects/{seq}/resolve`, agent the same path | `{"resolution": "accept" \| "reconcile", "resource": string (optional, one of the candidates)}` | `200` `EffectView` |
  | `GET /api/v1/changes/{id}/steps` | — | `200` `{"steps": [StepView]}` |
  | agent `GET /agent/v1/plans/{plan id}` | query `private=1` | `200` `PlanLookup` |
  | agent `POST /agent/v1/plans/{plan id}/retire` | empty | `200` `PlanLookup` |
  | agent `POST /agent/v1/changes/{id}/private-output/ack` | empty, or `{"final":true}` when the composite reached a terminal state (also clears the step's sealed effect secrets, 1.2) | `204` |

  `EffectView` is `{changeId, seq, kind, resource, local, inverseLocal,
  commitBy, state, reason, before, after, post, undoTo, undoResource,
  current, resolution, resolvedBy, resolvedAt}`, never the secret.
  `ObservationView` is `{current, resource, matches, candidates,
  exclusive}`. `StepView` is `{id, ordinal, host, action, state, attempt,
  childChangeId, earlierChildChangeIds, childDeadline, error}`, never
  inputs or outputs; the composite's step view and the `composite.step`
  events (1.6) are how the UI shows progress after a reload. Scopes: undo
  and repair need the action's scope (composites `admin`); release,
  observe and resolve need `admin`; effects and steps need `read`. The CLI
  gains `darkpanel change undo|repair|release|resolve|effects|steps <id>`
  (`effects --observe <seq>` is the preview); the UI offers Undo on
  undoable changes and Repair, Release and per-effect resolution (preview
  first) on `needs_repair` changes.

  Go additions and changes (the in-process `coretest` fakes implement
  them). The plan and apply calls carry the controller-generated call
  context (1.5): plan through a new `PlanOptions` argument, apply through
  a new `ApplyOptions.Call` field.

  ```go
  // New: options of a plan request. Call is set only by the controller
  // (dispatcher, composite runner, repair relay); the API rejects it.
  type PlanOptions struct {
      Call *CallContext
  }
  // ApplyOptions gains:
  //     Call *CallContext // parent, step and guardRefresh (1.5)

  // AgentClient changes:
  Plan(ctx context.Context, p Principal, a ActionRef, input json.RawMessage, opts PlanOptions) (PlanView, error)
  // AgentClient gains:
  Undo(ctx context.Context, p Principal, changeID string, call *CallContext) (ChangeView, error)
  Repair(ctx context.Context, p Principal, changeID string, req RepairRequest) (RepairResult, error)
  Release(ctx context.Context, p Principal, changeID, reason string, call *CallContext) (ReleaseResult, error)
  Effects(ctx context.Context, p Principal, changeID string) ([]EffectView, error)
  Observe(ctx context.Context, p Principal, changeID string, seq int) (ObserveResult, error)
  Resolve(ctx context.Context, p Principal, changeID string, seq int, req ResolveRequest) (EffectView, error)
  Lookup(ctx context.Context, planID string, private bool) (PlanLookup, error)
  Retire(ctx context.Context, planID string) (PlanLookup, error)
  AckPrivate(ctx context.Context, changeID string) error

  // Dispatcher changes:
  Plan(ctx context.Context, p Principal, hostID string, a ActionRef, input json.RawMessage, opts PlanOptions) (PlanView, error)
  // Dispatcher gains:
  Undo(ctx context.Context, p Principal, changeID string) (ChangeView, error)
  Repair(ctx context.Context, p Principal, changeID string, req RepairRequest) (RepairResult, error)
  Release(ctx context.Context, p Principal, changeID, reason string) (ReleaseResult, error)
  Effects(ctx context.Context, p Principal, changeID string) ([]EffectView, error)
  Observe(ctx context.Context, p Principal, changeID string, seq int) (ObserveResult, error)
  Resolve(ctx context.Context, p Principal, changeID string, seq int, req ResolveRequest) (EffectView, error)
  Steps(ctx context.Context, p Principal, changeID string) ([]StepView, error)

  type RepairRequest struct {
      Input json.RawMessage `json:"input,omitempty"` // RepairedBy plans only; never carries repairs
      Step  string          `json:"step,omitempty"`  // composite step decision
      As    string          `json:"as,omitempty"`
  }
  type RepairResult struct {
      Change *ChangeView `json:"change,omitempty"`
      Plan   *PlanView   `json:"plan,omitempty"`
  }
  type ReleaseResult struct {
      Change    ChangeView   `json:"change"`
      Abandoned []EffectView `json:"abandoned"`
  }
  type ResolveRequest struct {
      Resolution string `json:"resolution"` // "accept" | "reconcile"
      Resource   string `json:"resource,omitempty"`
  }
  type ObserveResult struct {
      Effect      EffectView      `json:"effect"`
      Observation ObservationView `json:"observation"`
      Allowed     []string        `json:"allowed"`
  }
  ```

  The methods 1.4 (enablement), 1.6 (streams), 1.7 (availability,
  internal reads) and 1.14 (reveal) add are declared in those sections in
  the same form.
- **Error codes** of this section: `core.undo_conflict` (the undo window
  has closed), `core.change_busy` (an owned change, a repair change, a
  composite step, a live task unit, a call that may still commit),
  `core.needs_repair` (the error stored on a change entering
  `needs_repair`, with `Cause`, and the refusal of undo or repair while an
  effect is `unresolved` or a recovery handler has not finished),
  `core.child_failed` (a composite that unwound or failed because of a
  step, naming it), `core.recovering` (admission closed at start),
  `core.deadline_exceeded`, `core.uninstalling` (the host is being
  uninstalled, 1.3c); existing: `lock.held`, `change.not_pending`,
  `change.revert_failed`, `plan.not_found` (a retired plan).
- **Tests.** Fault injection at every transition: the controller killed
  around each step's plan (before and after the plan answered), apply and
  confirmation; an apply delayed past its retire, and a retire racing an
  apply; an agent unreachable during unwind and confirmation; a crash
  around each journaled call (`Local` and remote) and each inverse
  (`InverseLocal` and remote, including an inverse that recreates an
  object); undo after a later change took a key; undo of the last two
  changes on a key, newest first; unwind of three compensable steps on one
  key, and of steps after a sibling that failed with nothing made; undo
  stopping at a conflict and at an accepted effect; resolution before and
  after `CommitBy`, racing a release, and with several candidates; a
  revert over an unresolved effect, with and without a failing lockout
  guard, and a revert finding a conflict; an Apply and a Verify of a
  revertible action that never return (the watchdog exit and the revert
  at restart); a failing repair change, one cut off by a restart, and two
  repair plans of one target; composite release with an unreachable agent
  and with a child repair running; a live apply failure of an action with
  a recovery handler, with and without an unresolved effect; a recovery
  handler that ignores its context; a task unit whose start call errors
  after it started, one with a queued job, one outliving an agent restart
  and its deadline, a second undo attempt after a cancelled one, and
  uninstall with a task unit running; quarantined records across a
  restart; and both apply paths across the 60 s write timeout.

#### 1.4 Agent background tasks and module enablement

- **Enablement on the agent.** New, non-frozen agent route
  `PUT /agent/v1/modules/{id}/enabled`, request `{"enabled": bool}`, answer
  `204` (unknown module: `404 not_found`; served on the controller's
  session only, never the local socket). The controller calls it when a
  module is enabled or disabled and after each `hello`. The agent stores the
  flag in its `core_meta` under the key `module_enabled.<id>` (`"1"` or
  `"0"`); an absent key means the manifest's `DefaultEnabled`. The
  controller's `core_module_state` (ADR 0002) stays the authority; the
  agent's key is its pushed copy and is read only by the background-task
  runtime. Actions on the local socket keep their current rule
  (enablement is not applied there). `core.AgentClient` gains
  `SetModuleEnabled(ctx context.Context, moduleID string, enabled bool)
  error`, which the controller's module-enable path and its `hello`
  handler call; the dispatcher has no matching method (enablement stays the
  existing controller API).
- **Interface** (`internal/core`, found on a `Module` by type assertion,
  like `Uninstaller`):

  ```go
  type BackgroundTask struct {
      ID       string        // "firewall.reconcile"; audit actor system:<ID>
      Every    time.Duration // 0: run only on wakes
      LockKeys []string      // the lock keys of every action the task may apply
      // Run is one pass: it returns when the pass is done. It mutates only
      // through tc.Apply.
      Run func(ctx context.Context, tc TaskContext) error
      // Watch, when set, is a long-lived subscription (Docker events) that
      // calls wake for each relevant event; it returns only on error or ctx done.
      Watch func(ctx context.Context, env Env, wake func()) error
  }
  type TaskContext struct {
      Env Env // Principal is system:<ID>, ScopeAdmin; Call is empty (1.5)
      // Apply plans and applies one action of the task's own module through
      // the runtime's lifecycle engine (never a second engine), confirms a
      // revertible change as soon as its verify passes, and marks the change
      // system. A plan with a failed guard returns plan.guard_failed.
      Apply func(ctx context.Context, action string, input any) (ChangeView, error)
  }
  type Background interface{ BackgroundTasks() []BackgroundTask }
  ```

- **Scheduling** (`internal/agent/runtime`, wave 0): one goroutine per task,
  started after startup recovery, only while the module's agent-side enabled
  flag is set and its preflight passes; clearing either cancels the task's
  context, and the runtime waits for `Run` and `Watch` to return before it
  reports the module disabled or closes. A pass runs on start, `Every` after
  the previous pass ended (when `Every` > 0), and after a wake; wakes that
  arrive during a pass coalesce into one further pass. Before each pass the
  runtime checks `core_locks` for every key in `LockKeys`: if any is held,
  the pass is skipped without error and the next wake or tick tries again.
  An error from `Run` is logged and the next pass waits with backoff from
  1 s to 5 min (reset by a successful pass); an error from `Watch` restarts
  it with the same backoff and wakes the task once it is back, so a
  reconnect triggers a full pass.
- A change made through `TaskContext.Apply` has `core_changes.system = 1`
  (new column, `INTEGER NOT NULL DEFAULT 0`, on agent and controller, set
  only by `TaskContext.Apply`), so rollbacks and snapshot retention can tell
  it apart (2.5).
- A task's plans carry no controller guard context; their guards evaluate
  the agent's own live facts, as break-glass plans do (1.5).

#### 1.5 Guard context

Lockout guards (2.3, 4.1) need the paths that must survive a change.

- **Declaration.** `ActionSpec` gains `Guards []GuardSpec`, the guards the
  action's plan may report:

  ```go
  type GuardSpec struct {
      ID          string // "firewall.lockout"; a plan reporting an undeclared id fails with internal
      Overridable bool   // only 2.3's and 4.1's lockout guards
  }
  // Guarded is embedded in the input type of every action with Guards.
  type Guarded struct {
      Guard     *GuardContext `json:"guard,omitempty"`     // filled by the controller
      Overrides []string      `json:"overrides,omitempty"` // ids of Overridable guards
  }
  ```

  The registry refuses an action whose `Guards` is non-empty and whose input
  type does not embed `Guarded`, or the reverse. The schema generator emits
  `guard` and `overrides` like any field, so ADR 0002's rule that the
  controller never sends a field the agent's schema lacks keeps a guarded
  action away from an agent without guard support. Plan refuses an
  `overrides` entry that is not an `Overridable` guard id of the action
  (`request.invalid_input`).
- **Address types in schemas.** The schema generator
  (`internal/core/schema/reflect.go`) runs `invopop/jsonschema` v0.14.0's
  `Reflector` with only `Anonymous` and `AllowAdditionalProperties` set and
  no type mapper. `netip.Addr`, `netip.AddrPort` and `netip.Prefix` are
  structs with unexported fields and no `JSONSchema` method, so (inference
  from the reflector's struct handling, not yet run) it would describe them
  as objects with no properties, and an input schema with
  `additionalProperties: false` would then refuse their JSON form, a
  string. Wave 0 sets `Reflector.Mapper` to map the three types to
  `{"type": "string", "format": "ip-address" | "ip-address-port" |
  "ip-prefix"}` (custom formats registered on the validator's compiler in
  `validate.go`, which already asserts formats, and checked with
  `netip.ParseAddr`, `ParseAddrPort`, `ParsePrefix`),
  and adds a schema test that builds the input schema of a type embedding
  `Guarded` and validates a filled `GuardContext` against it, both IPv4 and
  IPv6.
- **Plan.** The dispatcher fills `guard`; the API and the local socket
  reject a request that supplies `guard` (`request.invalid_input`); the
  local socket's break-glass plans run with `guard` empty.

  ```go
  type GuardContext struct {
      Client      *Peer            // the browser or CLI as the controller's transport saw it
      Via         *ProxyPath       // set when the request came through an exposure (8)
      PanelListen []netip.AddrPort // the controller's web and runtime listeners, on the controller's host
      AgentListen []netip.AddrPort // the controller's agent listeners, on the controller's host
      AgentPeers  []netip.Addr     // live agents' source addresses, on the controller's host
      Complete    bool             // the three lists come from a complete topology with no address dropped; false makes the firewall guard unknown (register row 82)
  }
  type Peer struct {
      Addr netip.Addr
      Port uint16
  }
  type ProxyPath struct {
      ExposureID   string           // the panel exposure (8); what a retirement names
      Kind         string           // npm | tunnel
      ProxyAddr    netip.Addr       // the proxy container's address on the panel network
      ListenerAddr netip.AddrPort   // the runtime listener
      Public       []netip.AddrPort
  }
  ```

- **Call context.** Facts about *why* a change runs come from the
  controller, never from input. `Env` gains `Call CallContext`, set at plan
  and at apply:

  ```go
  type CallContext struct {
      ParentChangeID string        // the composite running this as a step; "" for a top-level change
      ParentAction   string        // that composite's action name
      StepID         string        // the step id within the parent
      RepairOf       string        // the change this one repairs (1.3); "" otherwise; derived by the engine from core_plans.repairs, never sent
      Retired        []string      // exposure ids the parent has retired (below)
      GuardRefresh   *GuardContext // apply only: the controller's topology at apply time
  }
  ```

  On the agent protocol the plan request and the apply request gain an
  optional core-level object `call` with these fields except `RepairOf`
  (JSON names `parentChangeId`, `parentAction`, `stepId`, `retired`,
  `guardRefresh`; a `repairOf` field is refused, `request.invalid_input`),
  accepted only on the controller's mutually authenticated
  session (ADR 0003): the local socket refuses it (`request.invalid_input`)
  and the public API has no such field. The agent stores the plan's `call`
  with the plan (`core_plans.call`, JSON, nullable) and gives the apply the
  stored values plus the apply request's `guardRefresh`; an apply whose
  `call` names a different parent or step than its plan's
  is refused (`plan.state_changed`). Controller-side children get the same
  `Env.Call` from the composite runner directly. Handlers that depend on
  provenance read only `Env.Call`: NPM's and Cloudflare's reserved-resource
  checks (6, 7.3) read `ParentAction`, retirement reads `Retired`.

  In Go the call context travels in the existing interfaces of
  `contract.go`, which wave 0 changes (no compatibility shim; every caller
  is in this repository):

  ```go
  // PlanOptions carries core-level plan request fields.
  type PlanOptions struct {
      Call *CallContext // controller only; nil for a top-level change. RepairOf is ignored.
  }
  // AgentClient.Plan and Dispatcher.Plan gain the trailing argument.
  Plan(ctx context.Context, p Principal, a ActionRef, input json.RawMessage, opts PlanOptions) (PlanView, error)                 // AgentClient
  Plan(ctx context.Context, p Principal, hostID string, a ActionRef, input json.RawMessage, opts PlanOptions) (PlanView, error)  // Dispatcher, ext.Dispatcher

  // ApplyOptions gains Call; its GuardRefresh is set by the dispatcher,
  // never by an API or socket caller (the API decoder has no such field).
  type ApplyOptions struct {
      ConfirmTimeout time.Duration
      Confirmation   string
      ConfirmNow     bool
      Call           *CallContext
  }
  ```

  The public API handlers and the controller socket always pass
  `PlanOptions{}` and leave `ApplyOptions.Call` nil; the dispatcher sets
  `GuardRefresh` itself on every agent apply, and only the composite
  runner sets the other fields. The agent client (`internal/agent/api`)
  encodes `Call` as the JSON `call` object carried in the request header
  `Darkpanel-Call` (base64url, no padding), because the protocol-1 plan
  body is the action's raw input and stays unchanged; the agent refuses
  the header on any session other than the controller's. `AgentClient.Undo` and
  `AgentClient.Release` take a `call *CallContext` too (1.3's wire
  contract): the composite runner passes one whose `parent` names the
  composite when `system:recovery` undoes or releases a step (1.2, 1.3);
  every other caller passes nil.

- **Apply.** The apply request's `call.guardRefresh` (same type, not part
  of the action's input) is the controller's topology at apply time. The
  agent evaluates the union of the plan's `guard`, `guardRefresh`
  and its own live facts (its session's local and remote address, established
  SSH sessions, interface addresses, routes). Apply refuses if a flow that
  passed at plan now fails or a new protected flow (a new SSH session, a new
  agent, a new listener) fails.
- **Local break-glass** plans and applies, and plans made by background
  tasks as `system:*` actors (1.4), have only the agent's own facts. The
  firewall module gathers these facts itself (owner ruling R3), so a
  timer revert, which runs without the request's context, gathers them
  afresh. When a
  `darkpanel-controller` process runs on the agent's host (found in the
  socket table, 1.8, by the unit's main PID through D-Bus), the agent adds
  its listening sockets as protected flows (direct panel, runtime and agent
  listeners) and its established agent sessions' peers, so a `--local`
  change on the controller's host still protects the panel and the agent
  links.
- **Retired paths.** A composite that removes an exposure may, once the
  step revoking its listener has committed, record that exposure as
  retired in its step log; its later children get the exposure's id in
  `Env.Call.Retired`, and their guards then ignore a `Via` whose
  `ExposureID` is retired (protected flow 3, 2.3) and nothing else. Only
  the composite runner sets `Retired`, and only for the exposure its own
  removal revoked (8).
- **Overrides.** `core.Guard` gains the additive fields `Overridable` and
  `Overridden`; an overridden guard reports `OK: true, Overridden: true`, and
  the plan, diff and audit entry record it.

#### 1.6 Streams

- **Declaration** (`internal/core`, found on a `Module` by type assertion):

  ```go
  type StreamDef struct {
      Name       string       // "logs"; the stream is "<module>.<name>"
      ParamsType reflect.Type // validated like action input (query parameters decoded to JSON)
      EventType  reflect.Type // one event; its schema is part of the hash
      // Run emits events until it returns. emit fails once the reader is
      // gone or a limit ended the stream; Run must then return.
      Run func(ctx context.Context, env Env, params json.RawMessage, emit func(event any) error) error
  }
  type Streamer interface{ Streams() []StreamDef }
  // DefineStream builds a StreamDef from typed parameters and events.
  func DefineStream[P, E any](name string, run func(ctx context.Context, env Env, p P, emit func(E) error) error) StreamDef
  ```

  Stream scope is `read`; there is no per-stream scope. A stream's schema
  hash is the existing schema hash (`internal/core/schema`) of
  `{params, event}`.
- **Discovery.** New, non-frozen agent route `GET /agent/v1/streams`,
  answer `{"streams": [{"module": "docker", "name": "logs", "schemaHash":
  "<hex>"}]}`. `hello` is not changed (it is frozen for protocol 1). A 404
  means an agent without streams. The controller refuses to relay a stream
  whose hash differs from its own registry's (`module.unavailable`, reason
  "agent version differs").
- **Transport.** `GET /agent/v1/streams/{module}/{name}?<params>` on a yamux
  stream the controller opens: chunked JSON lines, at most 64 KiB per event,
  closed by either side. Of the 32 streams per agent session
  (`MaxControllerStreams`), actions and confirmations may use up to 16 and
  module streams up to 16.
- **Relay.** `GET /api/v1/hosts/{host}/streams/{module}/{name}` as
  server-sent events, scope `read`. Stream responses use their own deadlines,
  not the 60 s write timeout: a write deadline renewed per event (30 s), an
  idle timeout of 10 minutes, a hard limit of 2 hours. A comment heartbeat is
  sent every 20 s so proxies do not time out a quiet stream. Limits: 4 open
  streams per principal, 16 per host (`core.stream_limit`). Buffering is
  bounded at 256 events; a slow reader is disconnected with a final
  `overflow` event. Streams close when the session or token behind them
  expires or is revoked (checked every 30 s), and when the listener they
  arrived on is revoked (1.10). SSE framing: each module event is
  `event: data` with the event JSON; the stream ends with one `event: end`
  carrying `{"reason": "done" | "overflow" | "expired" | "revoked" |
  "error", "error"?: <problem>}`.
- **Go methods.** `core.AgentClient` gains `Streams(ctx context.Context)
  ([]StreamInfo, error)` (the discovery route; a 404 returns an empty list)
  and `OpenStream(ctx context.Context, p Principal, module, name string,
  params url.Values) (io.ReadCloser, error)` (the JSON-lines body; closing
  it ends the yamux stream), with `StreamInfo{Module, Name, SchemaHash
  string}`. `core.Dispatcher` gains `OpenStream(ctx, p, hostID, module,
  name string, params url.Values) (io.ReadCloser, error)`, which checks
  the hash and the limits and is what the relay handler calls. `ext.Dispatcher`
  does not expose streams.
- **CLI.** `darkpanel <module> <name> --follow` reads the SSE endpoint with
  its own streaming client.
- Streams in v1: `docker.logs`, `docker.stats`, `docker.pull`.
- **Controller events.** The controller has no event endpoint today; wave 0
  builds one, which asynchronous apply (1.3) and the web client use:
  - `internal/controller/events`: `type Bus`, with
    `Publish(eventType string, data any)` (the `ext.Publisher` signature,
    1.12: the bus marshals `data` to JSON, assigns the next sequence number
    and never blocks; a slow subscriber is dropped and then gets a `reset`
    event) and `Subscribe(ctx context.Context) (<-chan Event, lastSeq
    uint64)`; `Event{Seq uint64; Type string; Data json.RawMessage}`. The
    bus draws a random 64-bit **epoch** at process start; the SSE id of an
    event is `<epoch hex>-<seq>`, `seq` increasing from 1 within the epoch.
    A ring buffer keeps the last 1,024 events. A marshal error is logged
    and the event dropped (publishers pass plain structs).
  - Types: `change` `{hostId, changeId, action, state, updatedAt}` on every
    change state the controller records or ingests (controller changes,
    composite parents, agent changes as the audit ingester sees them);
    `composite.step` `{changeId, stepId, state, childChangeId}`; `host`
    `{hostId, online}`; `module` `{hostId, moduleId, enabled, available}`;
    `reset` `{}` (refetch everything).
  - `GET /api/v1/events`, scope `read`, SSE with the same write deadline,
    idle timeout, hard limit and heartbeat as the stream relay, at most 4
    connections per principal (`core.stream_limit`). A `Last-Event-ID`
    whose epoch is the current one and whose sequence is still in the ring
    buffer replays the missed events; any other value (another epoch, so a
    restart; a sequence older than the buffer; a malformed id) gets a
    `reset` first, then live events. Events carry ids and states, never
    action input or output.
  - The web client opens one connection per tab (`web/src/api/events.ts`)
    and refetches the queries an event names; the CLI waits for an
    asynchronous apply by polling `GET /api/v1/changes/{id}` every second.

#### 1.7 Per-action availability, scope and internal actions

`ActionSpec` gains three additive fields:

- `Available func(ctx context.Context, env Env) error`: per-action
  availability; the error text is the reason. For agent-side actions it
  runs on the agent: new, non-frozen route
  `GET /agent/v1/modules/{id}/availability` (controller session only),
  answer `{"module": "docker", "available": bool, "reason": string|null,
  "actions": [{"name": "docker.container.start", "available": bool,
  "reason": string|null}]}`; an action without `Available` is listed as
  available; a module whose preflight fails lists every action unavailable
  with the preflight reason; unknown module: `404 not_found`. The controller
  caches the answer per `hello` and per module enable, re-asks before
  dispatching an action whose cached answer is older than 60 s, and refuses
  an unavailable action with `module.unavailable` and the reason; the UI
  greys it out from the cached answer. Module preflight stays module-wide.
  `core.AgentClient` gains `Availability(ctx context.Context, moduleID
  string) (ModuleAvailability, error)` with `ModuleAvailability{Module
  string; Available bool; Reason string; Actions []ActionAvailability}` and
  `ActionAvailability{Name string; Available bool; Reason string}` (the
  route's body; `Reason` empty for null); the controller's cache sits in
  the dispatcher, which checks it inside `Plan`, `Apply` and `Read` and
  needs no new method. Controller-side actions run `Available` in-process.
- `MinScope`: can only **raise** the scope the existing rules require; it
  never lowers one. Today's rules are kept unchanged: read actions need
  `read`, agent mutations `operate`, controller-side mutations `admin`
  (`core.token.create` therefore stays `admin`). Raised in this spec:
  Compose actions, every `reveal` action, `docker.container.reveal-env`,
  `npm.admin.*` and `panel.*` need `admin`. A composite needs the highest of
  its own and every step's scope (1.3). No action's scope is lowered.
- `Internal`: the action is not exposed through the API or CLI and is
  dispatched only by the controller itself (`tailscale.cert.get`). Internal
  reads use the reveal path end to end (1.14), never `Engine.Read`, the
  public API, the journal or the SSE relay: `ext.Dispatcher.ReadInternal`
  → for an agent action the agent route `POST
  /agent/v1/reveal/{name}/{version}` (controller session only) → the
  executing side's `Engine.Reveal`, which runs `Handler.Reveal` (set by
  `DefineInternalRead`), applies no `Redact`, and writes the audit entry
  from `Revealed.Identity` (actor, action, identity, never the value)
  before returning. `Engine.Reveal` serves an `Internal` action only to a
  call arriving through `ReadInternal` (on the agent: the controller's
  session carrying the internal-read flag below); the public reveal API
  routes refuse it with `action.unknown`. `Internal` actions are the one
  exception to 1.1's rule that every agent action is reachable with
  `--local`: the CLI generates no command for them and the local socket
  answers `action.unknown`. Reveal actions are the other exception (1.14).
- **Internal read audit identity.** `ext.Dispatcher.ReadInternal` takes the
  calling principal (`p core.Principal`, 1.12); the controller sends it in
  the existing `Darkpanel-Principal` header (`internal/agent/api/client.go`),
  which the agent's handler already parses, plus the header
  `Darkpanel-Internal: 1`, which only the controller's session may send
  (the agent accepts an `Internal` action on the reveal route only with
  it, and refuses a public reveal of an `Internal` action without it), and
  the agent's audit entry names that actor. The handler of an `Internal` read returns
  `(Revealed[Out], error)` like a reveal (1.14), built with
  `DefineInternalRead`, so the `Identity` the audit records comes from the
  module ("tailscale certificate for <MagicDNS name>"). Callers and their
  principals: a composite reading a child step's internal output uses the
  composite's principal; the certificate source (1.12) uses
  `core.Principal{Actor: "system:listen.cert", Scope: core.ScopeAdmin}`;
  the Cloudflare connector's `tunnel.token` read uses the principal of the
  `cloudflare.connector.deploy` change that asks for it.

#### 1.8 Host clients (`internal/platform`)

- **Exec allowlist.** The runner's allowlist becomes a list of rules that
  check the **whole argument vector** and set a **fixed environment**;
  `NewRunner(rules []ExecRule)` replaces `NewRunner(paths []string)`, and
  `ProductionRules()` replaces `ProductionAllowlist()`:

  ```go
  // internal/platform
  type ExecRule struct {
      Path  string                 // absolute, clean
      Match func(args []string) error // nil error: the vector is allowed
      Env   []string               // KEY=VALUE pairs appended to the base environment; nothing else is inherited
  }
  ```

  A vector no rule of its path matches fails with an error naming the path
  (never the arguments). The table is the **complete effective allowlist**
  of the agent binary; nothing else may run:

  | Path | Allowed vectors (each argument at most 4,096 bytes, no NUL or control characters) | Environment |
  |---|---|---|
  | `/usr/sbin/useradd` | exactly `--system --no-create-home --shell /usr/sbin/nologin <user>`, `<user>` one of `darkpanel` (installer, today) and `darkpanel-derp` (4.3) | — |
  | `/usr/sbin/userdel` | exactly `<user>`, same set | — |
  | `/usr/sbin/ufw` | `[--force \| --dry-run] <verb> <arg>...`, `<verb>` one of the verbs of 2.2's command grammar (`allow`, `deny`, `reject`, `limit`, `route`, `insert`, `prepend`, `delete`, `default`, `logging`, `enable`, `disable`, `reload`); `ufw` has no `--` separator, so every argument value is also constrained by 2.2's schema patterns before it reaches the runner | — |
  | `/usr/sbin/iptables`, `/usr/sbin/ip6tables` | exactly `--version` | — |
  | `/usr/sbin/{iptables,ip6tables}-save`, `-legacy-save`, `-nft-save` | exactly `-t <table>`, `<table>` one of `filter`, `nat`, `raw`, `mangle`, `security` | — |
  | `/usr/sbin/{iptables,ip6tables}-restore`, `-legacy-restore`, `-nft-restore` | exactly `-n`, input on stdin (the runner's existing `Cmd.Stdin`) | — |
  | `/usr/bin/apt-get` | exactly one of the vectors `APT-1`–`APT-6` below | `DEBIAN_FRONTEND=noninteractive` |
  | `/usr/bin/apt-mark` | exactly `hold tailscale`, `unhold tailscale` or `showhold` | — |
  | `/usr/bin/dpkg` | exactly `--configure -a` | `DEBIAN_FRONTEND=noninteractive` |
  | `/usr/sbin/nft` | exactly `list tables` or `list table <family> <name>`, `<family>` one of `ip`, `ip6`, `inet`, `arp`, `bridge`, `netdev`, `<name>` matching `^[A-Za-z0-9_.-]{1,64}$` (read-only, 2.1) | — |
  | `/usr/bin/docker` | `compose --project-directory <dir> -p <name> (-f <file>)+ (--env-file <f>)* (--profile <p>)* <verb>`, `<verb>` exactly one of `up -d --no-build`, `down`, `pull`, `restart`, `config --format json` (3); `<dir>`, `<file>` and `<f>` absolute paths | `DOCKER_CONFIG=/var/lib/darkpanel/agent/docker-config`, `HOME=/var/lib/darkpanel/agent/docker-config`, `COMPOSE_EXPERIMENTAL_GIT_REMOTE=false`, `COMPOSE_EXPERIMENTAL_OCI_REMOTE=false` |

  **apt-get vectors.** These are the only `apt-get` argument vectors, in
  this order, word for word; 4.1 and 4.3 use them by name and the
  allowlist test runs each one and a reordering of each:

  | Name | Vector | Used for |
  |---|---|---|
  | `APT-1` | `update -o DPkg::Lock::Timeout=120 -o Dir::Etc::sourcelist=/etc/apt/sources.list.d/tailscale.list -o Dir::Etc::sourceparts=- -o APT::Get::List-Cleanup=0` | refresh the Tailscale repository only (no `-y`: `update` asks nothing). `List-Cleanup=0` stops apt from deleting the downloaded lists of every other repository, which it would otherwise treat as unused under the narrowed source list |
  | `APT-2` | `install -o DPkg::Lock::Timeout=120 -y tailscale` | install or update to the repository's current version |
  | `APT-3` | `install -o DPkg::Lock::Timeout=120 -y --allow-downgrades tailscale=<version>` | install an exact version (pin, DERP host) |
  | `APT-4` | `install -o DPkg::Lock::Timeout=120 -y --allow-downgrades --download-only --reinstall tailscale=<version>` | stage a version's `.deb` in `/var/cache/apt/archives/` without installing it |
  | `APT-5` | `install -o DPkg::Lock::Timeout=120 -y --allow-downgrades <deb>` | install a staged file; `<deb>` is an absolute path inside the `tailscale` module's `managed-dir` ending in `.deb` |
  | `APT-6` | `remove -o DPkg::Lock::Timeout=120 -y tailscale` | uninstall |

  `<version>` matches `^[0-9][0-9A-Za-z.+~-]{0,63}$`. The source list is
  always the fixed path `/etc/apt/sources.list.d/tailscale.list` (4.1 writes
  it, or finds an identical one); no other source-list path is accepted.

  Which caller may run a vector is a code rule, not a runner rule: only the
  `tailscale` and `derp` modules' recovery handlers call `dpkg --configure
  -a` (after an interrupted install), and only the installer and the `derp`
  module call `useradd` and `userdel`. The runner keeps its 30 s default
  timeout for a context without a deadline; an action that runs a longer
  program passes a context whose deadline is its `ApplyTimeout` (1.3).
  `systemctl` is not allowed; systemd stays on D-Bus (ADR 0012).
- **Units.** `internal/platform` imports `internal/core`, so the systemd
  contract moves into `internal/core` (new file `internal/core/units.go`)
  and `platform` keeps aliases: `type Systemd = core.Units`,
  `type TransientService = core.TransientService`,
  `type TransientTimer = core.TransientTimer`,
  `type UnitState = core.UnitState`; `platform.DBusSystemd` implements
  `core.Units`. The contract gains a main PID, the pending job, the
  collect mode and a pattern listing:

  ```go
  // internal/core/units.go
  type Units interface {
      StartTransientService(ctx context.Context, name string, svc TransientService) error
      StartTransientTimer(ctx context.Context, name string, t TransientTimer) error
      Start(ctx context.Context, unit string) error
      Stop(ctx context.Context, unit string) error
      Restart(ctx context.Context, unit string) error
      Kill(ctx context.Context, unit string, sig syscall.Signal) error
      ResetFailed(ctx context.Context, unit string) error
      Enable(ctx context.Context, unitFiles ...string) error
      Disable(ctx context.Context, unitFiles ...string) error
      Reload(ctx context.Context) error
      State(ctx context.Context, unit string) (UnitState, error)
      // List returns the loaded units whose names match one glob pattern
      // (systemd's ListUnitsByPatterns), states as State reports them.
      List(ctx context.Context, pattern string) ([]UnitStatus, error)
  }
  type TransientService struct {
      Description string
      ExecStart   []string      // ExecStart[0] is an absolute path
      OnFailure   []string      // unit names
      RuntimeMax  time.Duration // RuntimeMaxSec; zero sets none (1.3c)
      CollectMode string        // CollectMode; "" leaves systemd's default, task units set "inactive-or-failed" (1.3c)
  }
  type TransientTimer struct {
      Description string
      OnActive    time.Duration
      Accuracy    time.Duration
      Service     TransientService
  }
  // UnitState is a unit's state; a unit that does not exist has LoadState
  // "not-found". MainPID is the Service interface's MainPID property (0 for
  // a unit that is not a running service). PendingJob is the unit's Job
  // property: the queued job's type ("start", "stop", ...), "" when none
  // (1.3c's start fence reads it).
  type UnitState struct {
      LoadState, ActiveState, SubState string
      MainPID                          uint32
      PendingJob                       string
  }
  type UnitStatus struct {
      Name string
      UnitState
  }
  ```

  `Env` gains `Units core.Units`. Agent core code (the task runtime of
  1.3c, uninstall and update admission, the guard's search for a
  `darkpanel-controller` process, 1.5) uses the unrestricted client. A
  module gets a **scoped wrapper** that refuses every other unit name with
  an error naming the unit:
  - units it declares as owned paths of kind `unit` (its unit files,
    below): every method;
  - `darkpanel-task-<change id>-<op>-<attempt>.service` units (1.3c): `State` only
    (the task runtime starts and stops them);
  - **external units** it declares in `Manifest.ExternalUnits
    []ExternalUnit{Name string; Ops []string}`, `Ops` a subset of
    `state`, `start`, `stop`, `restart`: v1 declares one,
    `tailscale`'s `{Name: "tailscaled.service", Ops: ["state",
    "restart"]}` (4.1's rollback restarts tailscaled; the DERP module
    restarts it through a `tailscale` action, never directly), and
    `firewall`'s `{Name: "darkpanel-controller.service", Ops: ["state"]}`
    (owner ruling R3: the lockout guard reads the controller's main PID);
  - `List` is refused (core only).

  The `Env` that the `agent task` subcommand builds for a task process
  (1.3c) carries the same scoped wrapper for the task's module, so a
  task (for example `derp.server.install`, 4.3) can enable and start its
  own unit.
- **Docker Engine client** (`internal/platform/dockerapi`, wave 0b): HTTP
  over `/var/run/docker.sock`, API 1.44 or later, typed calls for the
  endpoints sections 2–8 use, streaming for logs, stats, pull and events.
  Hand-written; no Docker SDK.
- **Tailscale local client** (`internal/platform/tslocal`, wave 0b):
  `GET /localapi/v0/status`, `GET` and `PATCH /localapi/v0/prefs` (body
  `ipn.MaskedPrefs`: the prefs plus a `<Field>Set` boolean per field),
  `POST /localapi/v0/start` with `ipn.Options{AuthKey, UpdatePrefs}` for
  login, `POST /localapi/v0/logout`, `GET /localapi/v0/derpmap`, the
  certificate pair endpoint and the DERP-region debug endpoint (4.3). Wire
  types are plain Go structs with only the fields used; a contract test runs
  them against the tailscaled in the harness pins.
- **Socket table and routes** (`internal/platform/netstat`, wave 0b):
  listening and established sockets from `/proc/net/{tcp,tcp6,udp,udp6}`
  joined to owning processes through `/proc/<pid>/fd`; interface addresses
  and route lookups (`RTM_GETROUTE`, including the reverse lookup for an
  incoming flow) through hand-written netlink on `golang.org/x/sys/unix`.
- **Netfilter save parser** (`internal/platform/netfilter`, wave 0b): parses
  `*-save` output for the `filter`, `nat`, `raw`, `mangle` and `security`
  tables into tables, chains and rules; an unknown match or target is kept
  as an opaque rule the evaluator treats as unsupported (2.3). The
  evaluator that walks them is built in wave 1 (2.3), after its golden
  files exist. Fuzzed (R74).
- **File provider additions.** `core.OwnedPath` gains three fields and
  `internal/core` four kind constants; `core.FileWriter` gains the methods
  below (all in `internal/platform/files.go`, each refusing a path, key or
  name the module did not declare with the right kind, with the existing
  `openat2`, symlink and root-ownership checks of ADR 0009 unless stated):

  ```go
  const (
      OwnedRestore    = "restore"     // a regular file a snapshot may rewrite or remove
      OwnedManagedDir = "managed-dir" // a root-owned directory of flat module files
      OwnedSysctl     = "sysctl"      // a kernel parameter under /proc/sys
      OwnedServiceDir = "service-dir" // a directory owned by a module's service user
  )
  type OwnedPath struct {
      Path          string
      Kind          string
      BlockIDs      []string
      CommentPrefix string
      Pattern       string   // sysctl: the value pattern; managed-dir, service-dir: the file-name pattern
      User          string   // service-dir: the owning system user (an OwnedUser of the same module)
      Subdirs       []string // service-dir: the only subdirectories, one level, created with it
  }
  // added to FileWriter
  Restore(ctx context.Context, module string, e RestoreEntry) error
  ReadSysctl(ctx context.Context, module, key string) (string, error)
  WriteSysctl(ctx context.Context, module, key, value string) error
  WriteManaged(ctx context.Context, module, dir, name string, data []byte, mode fs.FileMode, uid, gid int) error
  RemoveManaged(ctx context.Context, module, dir, name string) error
  Rename(ctx context.Context, module, from, to string) error
  EnsureServiceDir(ctx context.Context, module, dir string) error
  RenameIn(ctx context.Context, module, dir, from, to string) error
  RemoveIn(ctx context.Context, module, dir, name string) error
  RemoveServiceDir(ctx context.Context, module, dir string) error
  // Owns reports whether the ownership store records path (any kind) as
  // module's, so a plan can refuse to take over a pre-existing file,
  // unit file or directory.
  Owns(ctx context.Context, module, path string) (bool, error)
  type RestoreEntry struct {
      Path     string
      Present  bool
      Mode     fs.FileMode
      UID, GID int
      Data     []byte
  }
  ```

- **Restore writes.** `Restore` writes the file with the entry's metadata
  (temporary file, fsync, rename, directory fsync) or, when `Present` is
  false, removes it (and fsyncs the directory); kind `restore` only;
  uninstall never removes a `restore` path. `Restore` is for regular files
  only.
- **Kernel parameters.** Kind `sysctl`: `Path` is the key under `/proc/sys`
  (`net/ipv4/ip_forward`) and `Pattern` its value pattern (`^[01]$`). A key
  may contain one `<iface>` segment (`net/ipv6/conf/<iface>/forwarding`),
  which matches `all`, `default`, or a name matching
  `^[A-Za-z0-9_.@-]{1,15}$` other than `.` and `..` that exists as a
  directory beside `all` at the time of the call; an interface that has
  disappeared fails the call with a not-found error the caller can tell
  apart. `ReadSysctl` and `WriteSysctl` open `/proc/sys` once, check with
  `fstatfs` that it is procfs, open the key beneath it with
  `openat2(RESOLVE_BENEATH | RESOLVE_NO_SYMLINKS)`, write the value with a
  single `write` (no temporary file or rename: procfs allows neither), then
  read it back and fail if it differs. Uninstall never touches a `sysctl`
  key. The firewall declares the keys of 2.1.
- **Managed directories.** Kind `managed-dir`: a fixed, root-owned
  directory, created by the agent with mode 0700 on first write, in which
  the module may create and delete **regular files directly inside it** (no
  subdirectories) with names matching `Pattern` (default
  `^[a-z0-9][a-z0-9._-]{0,63}$`), through `WriteManaged` and
  `RemoveManaged`; uninstall removes the directory and its contents. Used
  for tunnel token files (`<tunnel id>.token`, 7.2), NPM backup archives (6)
  and staged `.deb` files (4.1; the apt source list is the fixed path of
  `APT-1`, a kind `file` path). Restore staging is
  not a managed directory: the root restore CLI writes it itself, with the
  services stopped (10).
- **Binaries, unit files and swaps.** `WriteFile` accepts paths of kind
  `file` and, new, kind `unit`: a unit file under `/etc/systemd/system/`
  named `darkpanel-*.service`, after which the module calls
  `Units.Reload`, `Enable` and `Start` (4.3). `Remove` (today kind `file`
  only, `files.go`) also accepts kind `unit`: it unlinks the file without
  following a symlink, fsyncs the directory and forgets the record; a
  missing file is not an error, so a re-run converges. The module calls
  `Units.Stop` and `Disable` before it and `Reload` after it; uninstall
  does the same. A unit file that exists but `Owns` does not report is
  **pre-existing**: the installing action's plan refuses with
  `module.unavailable` and the reason "unit file <path> exists and is not
  Darkpanel's", so Darkpanel never removes a unit it did not create
  (`Owns` reads the ownership store through a new method
  `core.Ownership.Owns(ctx, module, path) (bool, error)`). A binary (`/usr/local/lib/darkpanel/derper`) is a kind `file`
  path written with mode 0755 (temporary file, fsync, rename). `Rename`
  renames one declared `file` path to another declared `file` path **in
  the same directory** (`derper`, `derper.new` and `derper.prev`
  are three declared paths), with `renameat` on the opened parent and a
  directory fsync, so a swap is durable.
- **Service-user directories.** Kind `service-dir`: a directory owned by
  `User` (mode 0700) with its declared `Subdirs`, for a service that runs as
  that user and writes its own files there (derper's key and certificates,
  4.3). `EnsureServiceDir` creates the directory and subdirectories (root
  creates, then `fchown`s to the user's uid and gid looked up at call time)
  and checks an existing one: the parent root-owned and not a symlink, the
  directory and subdirectories owned by `User`, no other entries checked.
  `RenameIn` and `RemoveIn` act on regular files in the directory or one of
  its `Subdirs` (names matching `Pattern`, default
  `^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`, so a certificate named after an
  IPv6 address fits), opened with `openat2(RESOLVE_BENEATH |
  RESOLVE_NO_SYMLINKS)` from the directory's descriptor, refusing a file
  not owned by `User`; a rename is followed by a directory fsync. Darkpanel
  never writes file contents into a `service-dir` (the service does).
  `EnsureServiceDir` records the directory in the ownership store before
  creating it, and refuses a directory that exists but `Owns` does not
  report (pre-existing; the plan refuses first). `RemoveServiceDir` removes
  the directory, used by the module's removing action, its undo, and
  uninstall: opened from its root-owned parent with
  `openat2(RESOLVE_BENEATH | RESOLVE_NO_SYMLINKS)`, it unlinks every
  non-directory entry in each declared subdirectory and in the directory
  itself (a symlink is unlinked, never followed), removes the declared
  subdirectories and then the directory with `unlinkat(AT_REMOVEDIR)`,
  fsyncs the parent and forgets the record. A subdirectory that is not
  declared fails the call with an error naming it, before anything is
  removed, so the operator decides; a missing directory is not an error
  (a re-run converges).
- **Darkpanel-owned Docker objects.** Containers, networks and volumes a
  module creates carry the label `darkpanel.module=<id>`. They are that
  module's resources (ADR 0002), driven through `dockerapi` by the module's
  own actions; generic `docker.*` mutating actions and Compose operations
  refuse them with `docker.owned` (3).

#### 1.9 Cloud plumbing (controller)

- `internal/controller/cloud` (wave 0): one HTTP client with 15 s timeouts. Retries
  (at most 3, backoff, honouring `Retry-After`) apply to `GET` and `DELETE`
  only. Every write has a declared **uncertain-outcome** procedure for a
  request that fails with a 5xx or a transport error after it may have
  reached the provider. Cloud calls are not `Local` (1.2): the write's
  effect becomes `unresolved` (`uncertain`) with `CommitBy` the call's
  start plus 10 minutes, the change goes to `needs_repair` (stored error `core.needs_repair`
  with `Cause` `cloud.uncertain_outcome`, 1.2), and the operator resolves it after `CommitBy`
  (1.2). The procedure below is what the module's `Observe` reads; nothing
  settles an uncertain write automatically, and a write is never re-sent
  blindly:

  | Write | Procedure |
  |---|---|
  | Tailscale auth key create | no immediate retry (the provider gives no guarantee that a request which timed out cannot still commit, so "none found" proves nothing). Every key Darkpanel creates is first recorded in `tailnet_keys` (description, kind `change` or `enroll`, owner id, request time, requested expiry, key id once known, state `requested`, `live`, `uncertain`, `used`, `retired` or `deleted`). Descriptions use two disjoint namespaces: `darkpanel-<change id>` (a 26-character ULID) for keys an action creates, and `darkpanel-enroll-<host id>` for join keys (4.2). On an uncertain create the effect becomes `unresolved` (`uncertain`), the change fails into `needs_repair` (stored error `core.needs_repair` with `Cause` `cloud.uncertain_outcome`, 1.2), and the row becomes `uncertain`. The sweeps below are cleanup of keys Darkpanel owns; they never settle an effect. A sweep lists keys (by id, fetching each when the list omits descriptions, 4.2a) and deletes each whose description is that of an `uncertain` or `retired` row (a key's secret cannot be recovered): every 5 minutes until the request time plus the requested expiry plus 24 hours, then once a day while the account is connected. Independently, a daily orphan sweep deletes a key whose description is in either namespace when it has no row, or its row is `uncertain`, `retired` or `deleted`; a row in `requested` is skipped while it is younger than the request timeout (15 s) plus 5 minutes (its create may still be answered) and is first moved to `uncertain` when older, so the sweep never deletes a key whose create is still in flight. A key committed however late is removed while the account stays connected, and a `live` or `used` key is never mistaken for an orphan. **Resolution moves the row in the same transaction** (the module's `Resolved` hook, 1.2): `Observe` lists keys by the row's description; `accept` of an observed key sets the row `live` with its key id (the operator keeps it; its secret is not recoverable, so the plan says so), `accept` of no key sets it `retired` (a late commit is still swept); `reconcile` recording `made` sets it `live` with the key id (undo then deletes that id), recording `skipped` sets it `retired`. After resolution no sweep treats the key as uncertain. A retry is a new change with a new description |
  | Tailscale policy `POST` (always sent with `If-Match: <ETag read at plan>`) | `Observe` re-`GET`s with HuJSON (State: the normalized text; Version: the ETag). A timed-out write may still commit while the ETag it was sent with is current, so it is never treated as not made before `CommitBy`: after it, an observation equal to the new text lets the operator `reconcile` (recorded `made`) or `accept`; equal to the old text, `reconcile` records `skipped` and the operator's next write uses a fresh ETag; anything else is shown as changed by someone else (`accept` only, or a new policy change). Darkpanel writes no settling change of its own, and no step before it is unwound while the effect is `unresolved` |
  | Tailscale device writes | re-read the device and compare |
  | Cloudflare tunnel create | look up by name `darkpanel-<change id>` |
  | Cloudflare configuration `PUT` | re-`GET` and compare with the body sent; equal → applied. This detects only a configuration that differs afterwards: a concurrent write that Darkpanel's `PUT` overwrote is undetectable (7.1) |
  | Cloudflare DNS record create | list records with the name and the comment `darkpanel:<change id>` |

  The same rule holds for every row: an uncertain write leaves its effect
  `unresolved` and the change in `needs_repair` until the operator
  resolves it.
- **Accounts.** Each module stores credentials in its own table with the
  secret column sealed (ADR 0005). Credentials are write-only through the API
  except through secret delivery (1.14). **Every path that deletes an
  account's credentials row** (`<module>.account.disconnect`, the undo of
  `<module>.account.connect`, a composite's compensation, `change release`
  of a change that created it, and `quarantine resolve`) first checks the
  module's pending cleanup in the same transaction: an `unresolved` cloud
  effect, a `tailnet_keys` row in `requested`, `uncertain` or `retired`, or a
  sweep still in its 5-minute phase. With cleanup pending, disconnect and
  release fail with `cloud.cleanup_pending`; in an undo or compensation the
  `Inverse` deleting the row returns `Definite(cloud.cleanup_pending)`
  without writing (1.2), so the effect stays `made`, the undo settles
  `needs_repair`, and once cleanup has finished the operator repairs the
  change, which runs the inverse again; only
  `--abandon-cleanup` on disconnect (admin, audited, listing what is
  abandoned) deletes the row anyway. The check lives in one function per
  module, `cleanupPending(ctx, tx) ([]string, error)`, which every such path
  calls.
- **Services and actors.** The sweeps (`tailnet.keysweep`) and the
  Cloudflare hostname driver (`cloudflare.hostnames`, 7.3) are controller
  services registered through the extension package (1.12); each writes
  its audit rows through `ext.Deps.Audit` and its changes, as actor
  `system:<service name>`.
- **Capability probes.** Connecting an account probes read capabilities with
  harmless reads and reports them; write capabilities are reported as
  unverified until the first write succeeds, and a 403 on a write is reported
  as `cloud.permission_missing` naming the permission.
- **Fakes.** `internal/controller/cloud/cloudfake` (wave 0) provides shared test
  scaffolding (recorded request log, fault injection: 429, 5xx, lost
  response after commit). Each module's fake server lives in its own package
  (`internal/modules/<id>/fake`), built by the module's wave.

#### 1.10 Trusted proxies and runtime listeners

- **Runtime listeners.** The controller opens and closes listeners recorded
  in `core_listeners` (id, address, port, allowed peer address, client
  header, created by change, state `open | revoked | closed`) without a
  restart, through `core.Listeners` (1.12). They are HTTPS only, with the web certificate authority, and
  accept TCP connections only from the allowed peer.
- **Client address.** Requests on other listeners never read forwarded
  headers. A runtime listener reads exactly one header, set by a proxy
  Darkpanel configured: `X-Darkpanel-Client` for NPM (the proxy host's
  configuration sets it from `$realip_remote_addr`, NPM's own TCP peer, and
  overwrites any incoming value) or `Cf-Connecting-IP` for a tunnel
  (Cloudflare sets it). `X-Forwarded-For` and `X-Real-IP` are ignored. A
  missing or malformed value refuses the request. The audit entry keeps both
  the transport peer and the client address.
- **Public HTTPS.** NPM proxy hosts set `X-Forwarded-Proto $scheme` and force
  SSL with HSTS; tunnels are HTTPS at the edge. A runtime listener refuses a
  request whose forwarded protocol is not `https`.
- **Revocation.** A durable `revoked` state in `core_listeners`, committed
  before anything else, is checked on every request and every stream event;
  then the listener stops accepting, and its tracked active connections and
  streams are closed after the current response is written. A listener found
  `revoked` at start is not opened.
- **TLS.** Runtime listeners share the existing listener code's TLS
  configuration, which already reuses one `tls.Config`; per the constraint in
  `docs/dforge/distilled/constraints.md`, wave 0 adds a
  session-resumption test with shared ticket keys.
- Requests through a runtime listener get `X-Accel-Buffering: no` on SSE
  responses, and the CSRF origin check accepts the exposure's public host
  name (8).

#### 1.11 Uninstall options

`darkpanel uninstall` gains `--module-option <module>.<key>=<value>`
(repeatable). Wave 0 replaces `core.Uninstaller` in `contract.go` (no module
implements it today; only the test module in
`internal/install/uninstall_test.go` does, and wave 0 migrates it):

```go
// UninstallOption is one option a module's uninstall step accepts.
type UninstallOption struct {
    Key     string   // ^[a-z][a-z0-9_]{0,31}$, unique within the module
    Values  []string // the allowed values, at least one
    Default string   // one of Values
    Help    string   // one line, printed in the plan
}

// Uninstaller is the optional agent-side uninstall step of a module, found
// with a type assertion on the Module. Options are the module's declared
// options with every value filled: the --module-option value or the
// default. Uninstall must succeed on a host where it already ran.
type Uninstaller interface {
    UninstallOptions() []UninstallOption
    Uninstall(ctx context.Context, env Env, keepData bool, opts map[string]string) error
}
```

`install.UninstallConfig` gains `ModuleOptions map[string]map[string]string`
(module id → key → value, from the flags). `PlanUninstall` validates it
before anything runs: an option for a module that is not compiled in or has
no `Uninstaller`, an undeclared key or a value outside `Values` is refused
with `request.invalid_input`; every declared option without a flag gets its
`Default`. `Describe` prints, under the `modules` step, one line per module
and option: `<module>.<key>=<value> (default <d>; <help>)`. `runModules`
passes the filled map. The firewall declares `rules` with values `remove`,
`keep`, `baseline` and default `remove` (2.9). Uninstall is refused while a
change is unfinished or a task unit is active, and `--stop-tasks` stops
those units first (liveness gates and the uninstall barrier, 1.3c).
`darkpanel uninstall --abort` clears the `uninstalling` flag the barrier
set (one transaction on `core_meta`, audited as `local:root@<host>`) when
an uninstall stopped before stopping the services, so the agent accepts
changes again; after the services were stopped there is nothing to abort
and it reports that.

#### 1.12 Module skeletons, extension points and shared files

Wave 0 creates every new module package with an empty manifest and adds
its line to `internal/modules/modules.go`: `firewall`, `docker`,
`tailscale`, `tailnet`, `derp`, `npm`, `cloudflare`, `panel`. Every module
is default-enabled; preflight decides whether it is available on a host
(R9). Wave 0 also adds every error code and dependency of section 11, and
the extension points below. An extension point is a **frozen call site**
in a wave 0 file that calls a **named function or interface** which a
later wave implements in a file it owns; wave 0 creates each such file as a
stub (returning nothing), and the owning wave replaces the stub's body.

- **Extension package `internal/core/ext`.** It imports only
  `internal/core`, `internal/core/store` and the standard library. Module
  packages and `internal/controller/server` both import it; no module
  imports `server`, so `server → modules → ext` has no cycle and no
  package-level registry is needed:

  ```go
  package ext

  type Surface int

  const (
      SurfaceWeb    Surface = iota // session or token, CSRF on unsafe methods, the 60 s write timeout
      SurfaceSocket                // the controller socket and its peer-uid check (api/socket.go)
  )

  type Route struct {
      Surface Surface
      Method  string     // http.MethodGet, ...
      Path    string     // "/api/v1/controller/backup"; a Go ServeMux path pattern
      Summary string     // for the OpenAPI document
      Scope   core.Scope // SurfaceWeb only: the minimum scope; ignored on the socket
      Stream  bool       // a 30 s deadline per write instead of the 60 s total
      Handler http.Handler
  }

  type Service struct {
      Name string // "tailnet.keysweep"; its audit actor is system:<Name>
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
  type Publisher interface{ Publish(eventType string, data any) }

  // Controller is a module's optional controller-side contribution, found
  // on a core.Module by type assertion.
  type Controller interface {
      Controller(deps Deps) ([]Route, []Service, error)
  }
  ```

  **Call site** (`internal/controller/server`, frozen): at startup, after
  the API server is built and before listeners accept, the assembly calls
  `Controller(deps)` on every module of `modules.All()` that implements it,
  and `backup.Contribute(deps)` (`internal/controller/backup/contribute.go`,
  same signature, a wave 0 stub that wave 7 fills); it mounts every route
  on its surface's mux (a pattern that clashes with an existing route fails
  startup) and records the route for the OpenAPI document. Routes are
  served during restore quarantine (the quarantine routes are among them);
  the API's quarantine gate refuses mutations. Services start after
  composite recovery and only while `restore_quarantine` is unset (if it is
  set, they start when `quarantine release` clears it); at shutdown their
  contexts are cancelled and the assembly waits for each `Run` to return.
  Users: the auth-key sweeps (`tailnet.keysweep`, 1.9), the Cloudflare
  hostname driver (`cloudflare.hostnames`, 7.3), and wave 7's backup and
  quarantine routes (10).
- **Runtime listeners** (1.10): `Env` gains `Listeners core.Listeners`, set
  for controller-side actions and nil on the agent; `ext.Deps` carries the
  same value:

  ```go
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
  type ListenerSpec struct {
      Addr         netip.AddrPort
      AllowedPeer  netip.Addr
      ClientHeader string // "X-Darkpanel-Client" or "Cf-Connecting-IP"
      PublicHost   string // accepted by the CSRF origin check
      ChangeID     string
  }
  type ListenerInfo struct {
      ID    string
      Spec  ListenerSpec
      State string // open | revoked | closed
  }
  ```

- **Web certificate** (`internal/controller/listen`): a frozen file
  `sources.go` defines

  ```go
  // CertSource supplies the web leaf for a handshake; nil, nil falls
  // through to the next source and finally to the Darkpanel CA leaf.
  type CertSource interface {
      Certificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error)
  }
  type SourceDeps struct {
      Dispatch    ext.Dispatcher
      LocalHostID string
      Log         *slog.Logger
  }
  func certSources(d SourceDeps) []CertSource { return []CertSource{tailscaleCertSource(d)} }
  func extraNames(ctx context.Context, d SourceDeps) ([]string, error) { return tailscaleNames(ctx, d) }
  ```

  `Leaf.GetCertificate` asks the sources in order; `Leaf.Ensure` adds
  `extraNames` to the CA leaf's names. A source's internal reads run as
  `core.Principal{Actor: "system:listen.cert", Scope: core.ScopeAdmin}`
  (1.7), which `SourceDeps` does not carry because it is fixed. Wave 0's stub
  `tailscale_cert.go` defines `tailscaleCertSource` (returns a source that
  always falls through) and `tailscaleNames` (returns none); wave 4 fills
  them (4.1).
- **CLI** (`internal/cli`): a frozen file `modules.go` holds

  ```go
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

  `NewRoot` adds each module's commands under the module's group
  (`darkpanel <module> ...`) and each controller command under
  `controller`; a name clashing with a generated command fails `NewRoot`
  (the existing rule). Wave 0 creates the stubs `internal/cli/<module>.go`
  and `internal/cli/controller_backup.go` (`backupCommands`, which wave 7
  fills with `backup`, `restore` and `quarantine`).
- **Path selection** (`cmd/darkpanel/paths.go`, frozen):

  ```go
  // selectPaths resolves the process's paths; a harness build replaces it.
  var selectPaths = func(cmd *cobra.Command, dev bool) (paths.Paths, error) { return paths.Resolve(dev) }
  // addRootFlags lets a harness build add persistent flags.
  var addRootFlags = func(root *cobra.Command) {}
  ```

  Every `paths.Resolve` call in `cmd/darkpanel` goes through `selectPaths`,
  and `newRoot` calls `addRootFlags`. Wave 9d's
  `cmd/darkpanel/paths_harness.go` (`//go:build harness`, like the
  existing `harness_crash.go`) sets both in `init`: `--state-dir <dir>`
  selects `paths.Under(dir)`.
- **Web** (`web/src`):
  - `modules/types.ts` (frozen) gains, on `ModuleEntry`, optional fields

    ```ts
    export interface ContainerRef {
      hostId: string
      containerId: string
      name: string
      composeService?: string
      ports: { privatePort: number; protocol: "tcp" | "udp"; bindings: { hostIp: string; hostPort: number }[] }[]
    }
    export interface ContainerAction {
      id: string // "firewall.allow"
      label: string
      render: LazyExoticComponent<ComponentType<{ container: ContainerRef; onDone: () => void }>>
    }
    export interface HostCreateExtension {
      id: string // "tailnet.join"
      // Renders extra inputs; value is merged into core.host.create's input.
      render: LazyExoticComponent<ComponentType<{ value: Record<string, unknown>; onChange: (v: Record<string, unknown>) => void }>>
    }
    export interface GlobalPage {
      path: string // "tailnet"; mounted at /<path>/*
      label: string
      routes: LazyExoticComponent<ComponentType>
    }
    export interface ModuleEntry {
      id: string
      label: string
      icon: LucideIcon
      routes?: LazyExoticComponent<ComponentType<ModuleRouteProps>> // host pages; optional now
      containerActions?: ContainerAction[]
      hostCreate?: HostCreateExtension
      global?: GlobalPage
    }
    ```

    A module with only a global page (`tailnet`, `cloudflare`) has no
    `routes`; `enabledModules` and the host page skip such entries.
  - `modules/registry.ts` (frozen) gets one line per new module importing
    `./<id>/routes` (the existing `hostinfo` convention), plus the
    `containerActions`, `hostCreate` and `global` entries that point at
    `./<id>/container-actions`, `./<id>/host-create` and `./<id>/global`;
    wave 0 creates each of these files as a stub component rendering
    nothing, owned afterwards by the module's wave.
  - **Consumers.** `app.tsx` mounts one route `/<global.path>/*` per entry
    with a `global` page, inside the session guard; `shell.tsx` lists host
    pages under each host and the global pages (Tailnet, DERP, Cloudflare)
    in their own sidebar group; `pages/hosts.tsx` gains an "Add host"
    button opening `pages/create-host-dialog.tsx` (new, wave 0), which
    calls `core.host.create` through the change dialog and renders every
    enabled module's `hostCreate` component, merging their values into the
    input; the docker module's container page (wave 3a) renders the
    `containerActions` of every module enabled on the host.
  - `components/change-dialog.tsx`: plan diff, guard results, a per-guard
    override control for guards marked `Overridable`, typed confirmation
    for irreversible actions, the revert countdown, and composite step
    progress (from `composite.step` events, 1.6).
- **Makefile:** a `fuzz` target that runs `scripts/fuzz.sh` (the script
  belongs to wave 9a).
- **Quarantine gate:** the `restore_quarantine` flag in `core_meta`,
  checked before every action apply, user or token mutation (refused with
  `core.quarantined`), composite recovery, the child watcher, module
  recovery handlers, the update scheduler and rollout driver, and service
  start (above); wave 7 only sets the flag (10) and adds the quarantine
  commands.
- **Harness helpers:** per-test VM names, work directories and network
  ranges derived from the test name, a VM concurrency limit, and the
  Compose plugin, Pebble and `pebble-challtestsrv` in
  `test/harness/pins.go` and `provision.go`.

**Frozen files.** After wave 0 merges, no later wave edits these; a wave
that finds a missing code, dependency, allowlist rule or extension point
stops, and the integrator adds it on `main` before the wave continues:
`internal/core/contract.go`, `internal/core/units.go`, `internal/core/define.go`,
`internal/core/registry.go`, `internal/core/ext/**`,
`internal/core/errcode/errcode.go`, `internal/core/lifecycle/**`, the core
migrations under `internal/core/store/migrations/**`,
`internal/platform/exec.go`, `internal/platform/files.go`,
`internal/platform/systemd.go`, `internal/modules/modules.go`,
`internal/cli/modules.go`, `internal/controller/server/**`,
`internal/controller/api/**`, `internal/controller/events/**`,
`internal/controller/listen/sources.go`, `cmd/darkpanel/paths.go`,
`go.mod`, `go.sum`, `web/package.json`, `web/pnpm-lock.yaml`,
`web/src/modules/registry.ts`, `web/src/modules/types.ts`,
`web/src/app.tsx`, `test/harness/pins.go`.

**Handed over.** Stubs wave 0 creates and the named wave then owns: the
module skeleton packages `internal/modules/<id>/**` and their web folders
`web/src/modules/<id>/**` (the module's wave), `internal/cli/<module>.go`
(the module's wave), `internal/cli/controller_backup.go` and
`internal/controller/backup/**` (7), `internal/controller/listen/
tailscale_cert.go` (4).

**Not frozen.** Wave 0 edits these, and later waves may edit them where
their decomposition row says so: `web/src/shell.tsx`,
`web/src/components/change-dialog*`, `web/src/pages/hosts.tsx` and
`create-host-dialog*` (accessibility fixes, 9b), `Makefile` (9c's
`api-docs` target), `internal/install/**` (4, 7), `internal/transport/
token.go` (4), `test/harness/provision.go` and the harness helpers (any
wave's harness test may add a helper in its own file), and
`.github/workflows/*` (as the decomposition says). Wave 10's security
fixes to a frozen file go through the integrator on `main`, each listed in
its plan.

#### 1.13 CLI confirmation and overrides

The existing contract stays: `--yes` answers the "Apply?" question only;
`--confirm` confirms a revertible change (or a composite with a revertible
step) as soon as verify passes; without it the CLI prints the change id and
deadline, and `darkpanel change confirm <id>` confirms. Overridable guards
are overridden with `--override <guard id>` (repeatable), which re-plans with
the ids in `overrides`; every other guard is a hard refusal.

#### 1.14 Secret delivery

A **reveal** action returns one stored secret once, in the response body
only. v1 has two: `npm.admin.reveal-password` (6) and
`docker.container.reveal-env` (3).

- **Declaration.** `ActionSpec` gains `Reveal bool`, allowed only on class
  `read` actions, which then have `MinScope: admin`. The handler is built
  with

  ```go
  // Revealed is a reveal handler's result; Out is the action's output type,
  // whose schema is the output schema of the action's snapshot.
  type Revealed[Out any] struct {
      Identity string // what is revealed, for the audit ("npm admin password, host h1")
      Value    Out    // the output, never redacted
  }
  func DefineReveal[In, Out any](spec ActionSpec, reveal func(ctx context.Context, env Env, in In) (Revealed[Out], error)) ActionDef
  // added to ErasedHandler; set only by DefineReveal, which leaves Read nil
  Reveal func(ctx context.Context, env Env, in json.RawMessage) (identity string, value json.RawMessage, err error)
  ```

  `DefineReveal` sets `InputType` and `OutputType` from `In` and `Out`, so
  the existing schema snapshot (`internal/core/schema/snapshot.go`, which
  needs `OutputType`) covers a reveal action unchanged; secret fields of
  `Out` keep their `secret:"true"` tag, which marks them `x-secret` in the
  schema but is not applied to the value. `Internal` reads (1.7) use the
  same handler field through

  ```go
  func DefineInternalRead[In, Out any](spec ActionSpec, read func(ctx context.Context, env Env, in In) (Revealed[Out], error)) ActionDef
  ```

  **Registry rule** (`registry.go`): a read action has `Handler.Reveal`
  set exactly when `Spec.Reveal` or `Spec.Internal` is true, and then
  `Handler.Read` is nil; `Spec.Reveal` and `Spec.Internal` are never both
  true; a reveal action has `MinScope: admin`; every other read action
  keeps the existing rule (only `Handler.Read` set).

- **Dispatch.** A reveal action is not served by the read route or
  `Engine.Read` (which redact and do not audit): `Engine.Read` refuses it
  (`action.unknown`), and it has its own path end to end. API:
  `POST /api/v1/hosts/{host}/actions/{name}/{version}/reveal` (agent side)
  and `POST /api/v1/actions/{name}/{version}/reveal` (controller side),
  scope `admin`, answer `200 {"value": <Value>}` with `Cache-Control:
  no-store`. Agent protocol: new, non-frozen `POST
  /agent/v1/reveal/{name}/{version}`, controller session only.
  Go methods: `core.AgentClient` gains `Reveal(ctx context.Context, p
  Principal, a ActionRef, input json.RawMessage, internal bool)
  (json.RawMessage, error)` (`internal` sets the `Darkpanel-Internal`
  header, 1.7; only `ReadInternal` passes true); `core.Dispatcher` gains
  `Reveal(ctx context.Context, p Principal, hostID string, a ActionRef,
  input json.RawMessage) (json.RawMessage, error)`, which the API handlers
  call and which refuses an `Internal` action; `ext.Dispatcher.ReadInternal`
  calls `AgentClient.Reveal(..., true)` or the controller engine's `Reveal`.
  `Engine.Reveal(ctx, p, ref, input)` validates the input, writes the audit
  entry (actor, action, `Identity`, never the value: on the controller in
  `core_audit`, on the agent as a journal event the controller ingests)
  **before** returning, then returns `Value`. CLI: `darkpanel <module>
  <action>` prints the value once to standard output. The value is never
  journaled, cached, relayed through SSE, or stored in a change.
- **No `--local`.** Reveal actions are the second exception to 1.1's rule
  that every agent action is reachable with `--local` (the first is
  `Internal`, 1.7): the agent's local socket answers `action.unknown` for
  them and the CLI generates no `--local` form, because a reveal must be
  authorized by an `admin` principal of the controller and audited there.
  A root operator on the host without the controller recovers NPM's
  password with NPM's own reset procedure (the runbook, 9), not through
  Darkpanel.

### 2. Firewall (R37–R49)

Module `firewall`; agent side; lock key `firewall`; preflight:
`/usr/sbin/ufw` present, UFW 0.35 or later, the `*-save` and `*-restore`
tools present.

#### 2.1 Configured state, loaded state and supported configurations

- **Configured state** comes from files, whether UFW is active or not:
  `ENABLED` and `LOGLEVEL` from `/etc/ufw/ufw.conf`; `IPV6`,
  `MANAGE_BUILTINS` and the `DEFAULT_{INPUT,OUTPUT,FORWARD}_POLICY` values
  from `/etc/default/ufw`; rules from the `### tuple ###` lines of
  `/etc/ufw/user.rules` and `user6.rules`; and the framework files
  `before*.rules` and `after*.rules`.
- **Loaded state** comes from `iptables-save` and `ip6tables-save` (tables
  `filter`, `nat`, `raw`, `mangle` and `security`) of the variant UFW uses,
  plus the live values of the supported sysctl keys (below).
- **Foreign netfilter hooks.** The evaluator sees only what it reads, so the
  module also reads the other iptables variant's `*-save` output and, when
  the kernel's `nf_tables` module is loaded (`/sys/module/nf_tables`
  exists), `nft list tables` and `nft list table <family> <name>` for every
  table iptables-nft did not create (`/usr/sbin/nft`, read-only verbs,
  allowlisted in wave 0, 1.8). A rule in the other variant's `filter`,
  `nat`, `raw`, `mangle` or `security` tables, or a native nftables base
  chain that has any rule or a policy other than `accept`, is an
  unsupported configuration (below) naming the table; an empty native base
  chain with policy `accept` is ignored (it decides nothing). With
  `nf_tables` loaded and no `nft` binary, the host is unsupported with the
  reason "install the nftables package so Darkpanel can read every rule".
- **Sysctl.** UFW applies `/etc/ufw/sysctl.conf` (the file `IPT_SYSCTL` in
  `/etc/default/ufw` names) when it starts. The **supported keys** are
  `net.ipv4.ip_forward`, `net.ipv6.conf.{all,default,<iface>}.forwarding`,
  `net.ipv6.conf.{all,default,<iface>}.accept_ra`,
  `net.ipv4.conf.{all,default,<iface>}.rp_filter` and
  `net.ipv4.conf.{all,default,<iface>}.accept_source_route`; the file
  setting any other key that affects forwarding or filtering
  (`net.ipv4.conf.*.forwarding`, `*.route_localnet`,
  `net.bridge.bridge-nf-call-*`) is an unsupported configuration. The module
  predicts the file's effect on the supported keys including the kernel's
  side effects: writing `net.ipv4.ip_forward` resets the IPv4 per-interface
  configuration to the host or router defaults, writing
  `net.ipv6.conf.all.forwarding` sets every interface's `forwarding`, and an
  IPv6 interface with `accept_ra=1` stops accepting router advertisements
  while it forwards. A start that would change any supported value is shown
  in the plan; when the change would cut a protected flow (for example
  `ip_forward=0` under a forwarded panel or Docker path) the guard fails,
  and a change to IPv6 forwarding on a host whose default IPv6 route was
  learned from router advertisements fails the guard as `unknown`.
  Snapshots record the file (a `RestoreEntry`) and every supported key's
  live value, global and per interface. Revert writes the live values back
  with `FileWriter.WriteSysctl` (owned-path kind `sysctl`, 1.8), after UFW's
  own transition has applied the file, global keys first and per-interface
  keys second (so a global write's resets are overwritten), and reads every
  value back; a value that does not read back fails the revert into
  `needs_repair`. The firewall's manifest declares the keys as owned paths
  of kind `sysctl` (1.8): `net/ipv4/ip_forward` (`^[01]$`),
  `net/ipv6/conf/<iface>/forwarding` (`^[01]$`),
  `net/ipv6/conf/<iface>/accept_ra` (`^[012]$`),
  `net/ipv4/conf/<iface>/rp_filter` (`^[012]$`) and
  `net/ipv4/conf/<iface>/accept_source_route` (`^[01]$`). A snapshot
  records per-interface values for the interfaces present when it is
  taken; restore writes `all` and `default` first, then a per-interface
  value only for an interface that still exists (one that has disappeared
  is listed in the revert result and does not fail it); an interface that
  appeared after the snapshot keeps what the kernel gave it from the
  restored `default`.
- `ufw status` output is not parsed for state.
- **Unsupported configurations.** UFW executes `/etc/ufw/before.init` and
  `after.init` at start when they are executable, and with
  `MANAGE_BUILTINS=yes` it flushes built-in chains; neither can be predicted.
  A non-empty chain in the `mangle` or `security` table that the evaluator
  does not support (2.3) is treated the same way. On such a host
  `firewall.status` reports "unsupported configuration" with the reason,
  every guard verdict is `unknown`, and every mutating action is refused
  with `firewall.unsupported_configuration` (a hard refusal, not
  overridable).
- Every parser is fuzzed (R74).

#### 2.2 Rule model and management (R37, R38)

- **Model.** `Rule{ID, Action (allow|deny|limit|reject), Direction
  (in|out|route), Proto (tcp|udp|any|ah|esp|gre|ipv6|igmp), From
  (CIDR|any), FromPort, To
  (CIDR|any), ToPort, InIface, OutIface, Family (v4|v6|both), Log
  (off|log|log-all), Comment, Origin (darkpanel|manual)}`.
- **Grammar** (what UFW can represent; anything else fails the plan with
  `request.invalid_input`):
  - `FromPort` and `ToPort` are a port, a range `a:b`, or a comma list whose
    entries use at most 15 multiport slots in total (a port takes one slot,
    a range two); a range or list requires `Proto` tcp or udp.
  - `Proto` is `tcp`, `udp`, `any`, or one of the port-less protocols UFW
    supports (`ah`, `esp`, `gre`, `ipv6`, `igmp`), which take no ports;
    `ipv6` and `igmp` only with `Family: v4`. The model, the generated JSON
    Schema and the parser use this one enumeration.
  - `From` and `To` are one address or CIDR; their family must agree with
    `Family` (`both` only when both are `any`).
  - `limit` applies only to `Direction: in`.
  - `InIface` only with `in` or `route`; `OutIface` only with `out` or
    `route`.
  - Examples: `{allow, in, tcp, any, -, any, 22}` →
    `ufw allow in proto tcp to any port 22 comment 'darkpanel:<id>'`;
    `{deny, in, udp, 2001:db8::/32, -, any, 5000:5100, Family v6}` →
    `ufw deny in proto udp from 2001:db8::/32 to any port 5000:5100 comment
    'darkpanel:<id>'`; `{allow, route, tcp, any, -, 172.17.0.2, 80,
    OutIface docker0}` → `ufw route allow out on docker0 proto tcp to
    172.17.0.2 port 80 comment 'darkpanel:<id>'`.
- **Schema patterns** (the only protection, since `ufw` has no `--`): ports
  within 1–65535; CIDRs parsed by `netip` and re-rendered; interface names
  `^[a-zA-Z0-9][a-zA-Z0-9_.+-]{0,14}$`; comments `^[A-Za-z0-9 :._/-]{0,64}$`
  after the tag.
- **Tag.** Darkpanel rules carry the comment `darkpanel:<ulid>` optionally
  followed by ` <label>`. Rules without the tag are `manual`; changing or
  deleting one requires `manual: true` in the input and is shown as such in
  the diff, otherwise the plan fails with `firewall.manual_required`.
- **Writing** only through `ufw` with argument vectors.
- **Unrepresentable manual rules.** A manual rule the model cannot express
  (a protocol or option outside the grammar) is shown read-only with its raw
  tuple, kept by every operation, and counted by the evaluator from the
  loaded state.

Resource `firewall.status` (configured and loaded state, rules with origin,
`active`, `inSync` and `supported` flags). Actions are listed in section 5.

#### 2.3 Prediction, evaluation and the lockout guard (R40, S1)

- **Prediction.** The plan computes the configured state after the change
  and the loaded state UFW will produce from it, for the transition the
  action causes (rule change with reload, enable, disable, reload after a
  file change):
  - with UFW active and `IPV6=yes`: the UFW chains (`ufw-before-*`,
    `ufw-user-*`, `ufw-after-*`, logging and policy chains, and the IPv6
    `ufw6-*` equivalents) rendered from the files using UFW's translation,
    with every non-UFW chain kept in its loaded position;
  - with `IPV6=no`: UFW loads no IPv6 chains and, when ip6tables exists,
    restores a plain IPv6 filter table without `-n`: policies `DROP` for
    `INPUT`, `FORWARD` and `OUTPUT`, only `lo` accepted, and every other IPv6
    filter chain removed (Docker's and tailscaled's included). The predicted
    IPv6 state for enable, reload and restore is exactly that table;
  - for disable: UFW flushes its secondary chains but keeps its empty
    primary chains and their built-in jumps (to preserve their positions),
    and sets the built-in policies to `ACCEPT`; a host on which UFW was
    never started has none of these chains. The prediction distinguishes the
    two, and non-UFW chains are untouched (Darkpanel's Docker chains are
    cleaned up separately, 2.6).
  - **`IPV6=no` refusal.** Because the `IPV6=no` start removes every foreign
    IPv6 filter chain, which no snapshot can rebuild, any action whose
    transition starts or reloads UFW with `IPV6=no` is refused
    (`firewall.unsupported_configuration`, not overridable) while the loaded
    IPv6 filter table holds any chain or rule UFW did not create (Docker's,
    tailscaled's or another program's); the plan names them and suggests
    `IPV6=yes`.

  The wave 1 spike records UFW's translation, skeleton and transitions on all
  four OS images as golden files, and the harness re-checks them.
- **In sync.** At plan time the module also predicts the loaded state from
  the **current** configured state and compares it with the actual loaded
  state. A difference marks the host out of sync: every guard verdict is
  `unknown` (a hard refusal) and the plan shows the differing chains. The
  exception is the **repair path**: `firewall.state.repair` (revertible)
  and `firewall.docker.repair` plan the transition from the **actual**
  loaded state to the state the configuration describes, evaluate every
  protected flow against that target (so the guard applies normally and is
  overridable), and are allowed on an out-of-sync host when the difference
  is confined to UFW's and Darkpanel's own chains; a difference in foreign
  chains keeps the hard refusal. Both are reachable through break-glass
  (`sudo darkpanel firewall state repair --local`). Because a repair's
  starting point is the loaded state, not the files, its snapshot also holds
  a **loaded-state before-image**: the `*-save` lines, for both families, of
  every UFW chain (`ufw-*`, `ufw6-*`), every Darkpanel chain
  (`DOCKER-USER`, `darkpanel-*`, `dp-b-*`), and the built-in `INPUT`,
  `FORWARD` and `OUTPUT` chains with their policies. Reverting an
  unconfirmed repair restores the files (unchanged by a repair, so normally
  a no-op) and then loads the before-image with `*-restore -n`, which
  flushes and refills exactly the chains it declares; the built-in chains
  are included only if their current non-UFW, non-Darkpanel lines equal the
  image's (otherwise the revert leaves the built-in chains as they are, so
  no foreign rule is overwritten, and the change becomes `needs_repair`
  naming the difference). Verify then compares the
  loaded chains with the image. Repair is the only action with a
  before-image; every other action starts from an in-sync host. Verify
  compares the predicted state with the loaded state after apply over
  UFW's chains, Darkpanel's chains and the built-in chains of all five
  tables (a rule that before.rules or after.rules appends to a built-in
  chain of the nat, raw, mangle or security table is appended again by
  every start, so such a chain passes while it holds every rule the start
  appends); foreign user chains are not compared, because no start writes
  them. A difference fails verify with `firewall.prediction_mismatch`,
  which reverts.
- **Evaluation** (`internal/modules/firewall/eval`, wave 1). The evaluator
  walks a predicted or loaded state for a packet tuple `(family, in-interface,
  out-interface, source, source port, destination, protocol, destination
  port, conntrack state, original destination and port, mark)` through every
  hook the packet traverses: for incoming packets `raw`, `mangle`, `nat`
  `PREROUTING` (Docker's localhost-protection drops, DNAT setting the
  original destination, mark rules), then `mangle`, `filter` and `security`
  `INPUT` or `FORWARD`, and for forwarded packets `mangle` and `nat`
  `POSTROUTING`; for locally generated packets `raw`, `mangle`, `nat`,
  `filter` and `security` `OUTPUT`, then `POSTROUTING`. It supports the
  matches and targets UFW, Docker and tailscaled generate (`-i`, `-o` with
  `+` wildcards, `-s`, `-d`, `-p`, `--sport`, `--dport`, `--dports`,
  `--sports`, `--tcp-flags`, `!` negation of `-i`, `-o`, `-s`, `-d`,
  `--ctstate` and `--mark`, `-m conntrack --ctstate` including `DNAT`,
  `--ctorigdst`, `--ctorigdstport`, `-m addrtype`, `-m comment`, `-m mark`
  and `-m connmark` with masks, jumps, `RETURN`, `ACCEPT`, `DROP`, `REJECT`,
  `DNAT`, `SNAT`, `MASQUERADE`, `MARK --set-xmark`, `CONNMARK --save-mark`
  and `--restore-mark` with `--nfmask`/`--ctmask`, and `LOG` and `TCPMSS`
  continuing). This list is the set the pinned UFW, Docker 28 and tailscaled
  1.82–1.102 generate (tailscaled's `util/linuxfw` runner: `ts-input`,
  `ts-forward`, `ts-postrouting`, the connmark save and restore rules in
  `mangle`, its DNAT and SNAT rules), and the 0b netfilter parser's contract
  covers the same five tables and the same matches and targets (1.8).
  A packet's mark and connection mark start at 0 for a new connection and
  change only through the `MARK` and `CONNMARK` rules on its path; a match
  on a mark that only a non-netfilter source (a socket option, `tc`) could
  set makes the verdict `unknown`. `limit`, `recent` and `statistic` on a
  rule whose target decides (not `LOG`, which UFW's logging chains and the
  block pair with `limit`), any other match or target, and any non-empty
  chain in a hook it does not model make the verdict `unknown`. Golden fixtures include tailscaled's chains with
  subnet routing, exit-node advertisement and stateful filtering on.
- **tailscaled's chains.** tailscaled inserts `ts-input` and `ts-forward` at
  position 1 of `INPUT` and `FORWARD`, and `ts-input` accepts everything
  arriving on `tailscale0`; the evaluator reproduces that from the loaded
  state, so UFW never governs tailnet traffic.
- **Protected flows.** A flow's ingress is not inferred from a route
  lookup, because routing may be asymmetric even with only the main table,
  and the kernel does not record a TCP socket's ingress interface. The
  evaluator checks the flow on **every eligible in-interface** and the flow
  passes only if it passes on all of them. Every non-loopback interface
  that is up is eligible, except that for IPv4 an interface whose effective
  `rp_filter` (the maximum of `all` and the interface's value) is 1
  (strict) is eligible only when the route back to the source (a lookup of
  the source as destination from the flow's local address, policy rules
  included, read through the 0b routes client) leaves through that
  interface, since
  strict reverse-path filtering drops anything else before `INPUT`. Loose
  mode (2) and 0 exclude nothing. IPv6 has no `rp_filter`, so every up
  interface is eligible. A source in Tailscale's ranges (`100.64.0.0/10`,
  `fd7a:115c:a1e0::/48`) is eligible only on `tailscale0` while that
  interface is up, because tailscaled's `ts-input` drops such a source on
  every other interface. An interface that disappears while the facts are
  read is not eligible. The
  out-interface comes from a forward lookup including policy rules; a
  lookup that cannot be resolved is `unknown`. The flows:
  1. each established TCP connection whose local socket is owned by `sshd`:
     a new connection from its remote address to its local address and port;
  2. when the controller runs on this host and the request came directly:
     from `Client.Addr` to each `PanelListen` address;
  3. when the request came through an exposure: from `Via.ProxyAddr` to
     `Via.ListenerAddr` (in on the panel bridge `dp-panel`); for NPM, from an
     internet address to each `Via.Public` port through `nat` and `FORWARD`
     to the NPM container; for a tunnel, from the cloudflared container's
     address to the internet on UDP 7844, TCP 7844 and TCP 443 through
     `FORWARD`;
  4. when the controller runs on this host: from each `AgentPeers` address to
     each `AgentListen` address;
  5. outbound from this host to the agent's controller address and port;
  6. when tailscaled runs: its own outbound traffic, read from its sockets
     (its WireGuard UDP port to any destination, and TCP 443 to any
     destination for DERP and the control plane).

  Tailscale SSH sessions are not netfilter flows (tailscaled's network stack
  handles them) and are covered by the Tailscale guard (4.1). A flow whose
  path the evaluator cannot determine (an unknown route, an unsupported
  match) is `unknown`.
- **Verdict.** A flow that evaluates to drop, reject or `unknown` fails the
  guard `firewall.lockout` naming the flow (code `plan.guard_failed`). The
  guard is overridable, except on an unsupported configuration (2.1) or an
  out-of-sync host.

#### 2.4 Auto-revert (R41)

Every mutating firewall action is revertible with the agent's revert timer.
The timeout is the apply request's `confirmTimeoutSeconds` (default 60,
range 10–900, owner ruling R2), set with the CLI's `--confirm-timeout` and
the change dialog's timeout field; a stored per-host setting is deferred. The timer is armed in the change's intent transaction,
so it runs even when Apply or Verify hangs: if it fires while the change is
still `applying`, the agent exits through the revert watchdog and startup
recovery reverts the change (1.2). The UI shows a countdown with Confirm and
Revert; the CLI behaves as in 1.13.

#### 2.5 Logical snapshots and revert (R42)

- **Content.** A snapshot holds the firewall's whole logical state: UFW's
  state files (`user.rules`, `user6.rules`, `before.rules`, `before6.rules`,
  `after.rules`, `after6.rules`, `ufw.conf`, `/etc/default/ufw`) as
  `RestoreEntry` values (presence, mode, owner, bytes, size limit 4 MiB
  each), the rows of `firewall_container_rules` (policy by binding, not by
  address), the module's settings, the Docker integration flag, and a hash
  of each file.
- **Store.** `firewall_snapshots` (id, created, change id, actor, `system`
  flag, `pinned` flag, entries, logical state). The engine's per-change
  snapshot is a reference to this row. After every apply the module also
  records the hash of each UFW file as it stands right after the change.
- **Effects.** Every firewall mutation journals its effects (1.2) before
  making them, each with its inverse: a rule added (inverse: delete that
  tuple), a rule deleted (re-add at its position), a default or logging
  level changed (set the old value), UFW enabled or disabled (the opposite,
  with the cleanup or block load of 2.6), a file written (the previous
  entry), a logical row inserted, updated or deleted in
  `firewall_container_rules` or the settings (the previous row), the
  integration flag changed, a sysctl value changed. Each effect that edits
  a file or the loaded state records, before the call, the expected
  before-state and the expected after-state (file hashes from the wave 1
  golden translation; the value or rule tuple for the rest). Every firewall
  effect is `Local` (its calls are runner programs and `FileWriter`
  writes, 1.2), and every firewall action implements `Observer` (1.2), which
  reads the file hash, rule tuple, value or row of one effect; the engine's
  settle step (1.2) then classifies an `intended` effect: equal to the
  before-state → `skipped`; State-equal to the after-state → `made`;
  anything else (partial, or edited since) → `unresolved` (`ambiguous`).
  Every revertible firewall action's `Revert` returns `RevertOutcome`
  (1.2) and reads the change's journal through `env.Effects` and
  `env.ChangeID`. Per effect kind, `State` holds the
  file hash, the rule tuple, the default or logging value, the `ENABLED`
  flag, the logical row or the sysctl value. `Version` holds what changes
  on every write: the hashes of the four files ufw rewrites, and for a
  rule its positions; a file or sysctl effect has none. Settle's `skipped`
  needs State and Version both equal to the before-state, while `made`
  and a revert's conflict check compare State only, so a manual edit
  elsewhere in `user.rules` is no conflict. A mid-apply crash therefore leaves an exact list of what
  was done, or names the one effect that cannot be settled.
- **Revert of a change** (timer, explicit revert, startup recovery,
  failed apply or verify) follows 1.2's single precedence rule, decided in
  this order before anything is written:
  1. **Check.** `Revert` reads the change's journal (`env.Effects`) and
     observes every `made` effect against its `Post`. If any effect is
     already `unresolved`, the revert writes nothing except under the
     connectivity exception (step 4). A `made` effect whose observation
     differs from its `Post` (the change's own rule was edited or removed,
     a manual rule now occupies its position) is a conflict: it is left in
     place and reported in `RevertOutcome.Effects` as `unresolved`
     (`conflict`, with the observation in `Current`), and the revert goes on
     to step 3 for the other effects.
  2. **Whole restore.** Otherwise, if no effect conflicts and every UFW
     file's hash still equals the
     hash recorded right after the change applied (after a mid-apply crash:
     the hash expected after the last settled effect), restore the snapshot
     (below). A missing post-apply hash never by itself authorizes a
     whole-snapshot restore.
  3. **Inverses.** Otherwise (someone edited UFW during the pending window
     without touching the change's own effects), apply the inverses of the
     journaled effects in reverse order, which leaves the other edits in
     place; logical rows are restored in the same transaction as the intent.
     If an inverse fails, the revert stops there.
  4. **Connectivity exception.** Whenever the revert ends without restoring
     the prior state (step 1 found an effect already unresolved, an effect
     was left in place as a conflict, or a step 2 or 3 write failed), the
     lockout guard is evaluated on the loaded state as it now stands. If it fails for a protected flow, the snapshot
     is restored whole even though that overwrites unresolved effects and
     manual edits; this is the single exception to 1.2's rule that an
     `unresolved` effect is never overwritten, it is authorized by the guard
     failure alone (R40: connectivity is kept whoever started the revert),
     and it settles nothing: every `unresolved` effect keeps its state (with
     the overwritten state in its `Current`), and `Revert` returns
     `RevertOutcome{Override: true, Detail}` with `Detail` the emergency
     restore record (time, snapshot id, the diff it overwrote), which the
     engine stores in `core_changes.override_json` and journals with result
     `override` (1.2). If the guard passes, the loaded state is left as it
     is: with an effect already unresolved, or a failed write, `Revert`
     returns an error naming it (cause `change.revert_failed`); with only
     conflicts, it returns no error and reports them in `Effects`, each
     other effect `undone`.
  5. **Outcome.** Steps 2 and 3 completing → `reverted`. Every other ending
     → `needs_repair` (never `failed`; 1.2's revertible failure table), the
     change keeps the `firewall` lock, its snapshot is pinned until the
     repair ends, and every firewall action declares `RepairedBy:
     firewall.state.repair` (1.3): `darkpanel change repair <id>` plans
     that action, which shows the difference, and applies it under the
     change's own lock ownership. The repair request's optional `input`
     (1.3) chooses `outcome: restore` (the default: return the host to the
     snapshot) or `outcome: keep` (bring the host to the state the change
     intended); the action's output `repairOutcome` is `restored` or
     `kept` accordingly, and `kept` is reported only when the loaded state
     verifies equal to the change's intended state (the post-apply hashes
     and rule tuples), else the repair fails.
- **Restore** is crash-safe and complete: one database transaction records
  the restore intent and writes the logical rows; then each file is written
  with `FileWriter.Restore`; then the Docker chains are brought in line with
  the restored integration flag (2.6 cleanup or block load, preserving
  foreign `DOCKER-USER` rules as 2.6 describes); then the UFW transition the
  snapshot requires, predicted with 2.3's transitions before the restore
  starts:
  - snapshot active, UFW active: `ufw reload`;
  - snapshot active, UFW inactive (for example reverting `state.disable`):
    `ufw --force enable` (`--force` skips UFW's interactive SSH prompt);
  - snapshot inactive, UFW active: `ufw disable`;
  - both inactive: none;

  then the snapshot's sysctl values (2.1); then verify that `ENABLED`, the
  loaded UFW and Darkpanel chains and the sysctl values match the
  snapshot's prediction; then the intent is cleared. `ufw reload` on an
  inactive firewall only prints "Firewall not enabled (skipping reload)",
  which is why the transition is chosen, never assumed. Container rules are
  rendered against current container identities, never replayed from old
  addresses. Startup recovery finishes an intent it finds by restoring the
  same snapshot again (idempotent).
- **Retention.** The last 50 unpinned snapshots of non-system changes and
  the last 10 of system changes (the reconcile task's, 2.8, kept separately
  so a busy Docker host does not prune the operator's history), plus any
  referenced by an unconfirmed or `needs_repair` change, plus pinned ones.
  The first mutation on a host pins a `baseline` snapshot taken before it,
  which is never pruned.
- **Actions.** Resource `firewall.snapshots`; `firewall.snapshot.compare`
  (read) diffs two snapshots or one against now; `firewall.snapshot.restore`
  (revertible) restores one, and its diff lists every manual rule it would
  add or remove, which needs `manual: true`.
- **Break-glass.** `sudo darkpanel firewall snapshot restore --local --id <id> --confirm`
  (the generated action command) and
  `sudo darkpanel firewall rollback --local [--change <id>]`, which reverts
  the newest non-system change (or the named one) as above and confirms in
  the same command. These CLI commands live in `internal/cli/firewall*.go`.
- **Reconcile** (2.8) reads only committed state and does not run while the
  `firewall` lock is held.

#### 2.6 Docker integration (R43–R47)

- **Detection (R43).** Resource `firewall.docker`: the daemon's version and
  `FirewallBackend.Driver` (`iptables` when the field is absent); which
  netfilter variant holds Docker's chains (Docker runs the `iptables` on its
  own `PATH`), found by running both the `-legacy-save` and `-nft-save`
  tools and looking for `DOCKER-USER` and `DOCKER-FORWARD`; which variant
  holds UFW's chains; whether Docker's localhost-port protection is loaded in
  the `raw` table; the contents of `DOCKER-USER`; the Docker networks with
  their driver, bridge interface name and subnets; and the block state.
  Supported only when Docker is 28.0 or later, its driver is exactly
  `iptables`, its chains are in the same variant as UFW's, and its `raw`
  protection is loaded. In `nftables` mode Docker creates no `DOCKER-USER`
  chain. Anything else, or inconclusive detection, is unsupported with the
  reason (`firewall.unsupported_integration`).
- **Adoption of `DOCKER-USER`.** Docker reserves `DOCKER-USER` for user
  rules, and the block replaces it (its declaration flushes the chain on
  every UFW start and reload). `firewall.docker.enable` therefore refuses
  (`firewall.docker_user_in_use`, listing the rules) while `DOCKER-USER`
  holds anything other than Docker's default `-j RETURN` or the block's own
  rules. Rules that appear later are treated as an out-of-sync condition:
  health `broken`, and every mutating firewall action, the reconcile task,
  `firewall.docker.repair` and the cleanup refuse with
  `firewall.docker_user_in_use` until the operator either removes them or
  runs `firewall.docker.repair` with `discardForeign: true`, whose diff lists
  each rule it will discard (needs `manual: true`). No automatic operation
  ever discards a foreign rule.
- **Foreign rules and revert.** A revert (timer, explicit, startup
  recovery, break-glass) is never refused for foreign `DOCKER-USER` rules,
  because refusing it could keep a lockout in place. Before its UFW
  transition, the revert reads `DOCKER-USER` and records every foreign rule
  with its position relative to the block's lines in the change. After the
  transition (whose declaration of `DOCKER-USER` flushed the chain), it
  reloads `DOCKER-USER` with `*-restore -n`: the block's lines (or, when the
  restored state has the integration off, `-j RETURN` only) with the
  recorded foreign rules re-inserted where they were, all before the
  block's first line or all after its last. Foreign rules interleaved
  between block lines cannot be placed faithfully: they are re-inserted
  before the block's first line, the change becomes `needs_repair` with the
  original order shown, and health stays `broken` until the operator
  resolves it. The owned-chain cleanup run inside a revert preserves foreign
  rules the same way instead of refusing. The harness adds a foreign rule
  during a confirmation window and during an interrupted cleanup and
  checks that the revert completes and the rule is still loaded.
- **Non-Docker forwarding.** `DOCKER-USER` is reached from `FORWARD` for all
  forwarded traffic, so the block's DNAT gate (below) would also catch other
  port forwards. `firewall.docker.enable` lists every `nat PREROUTING` DNAT
  rule that is not Docker's and refuses until each is covered by the setting
  `forwardExemptions`. An exemption names one foreign forward exactly:
  `{inInterface?, origDestination (address), origPort?, destination
  (address), proto (tcp|udp), port?}`, where `origDestination` and
  `origPort` are the pre-DNAT host address and port the foreign rule
  matches and `destination` and `port` are its `--to-destination` (the
  translated address, which is what `FORWARD` sees). Enable proposes one
  exemption per foreign DNAT rule from its match and target. The block
  renders each as `-A DOCKER-USER [-i <inInterface>] -m conntrack --ctstate
  DNAT --ctorigdst <origDestination> [--ctorigdstport <origPort>] -p <proto>
  -d <destination> [--dport <port>] -j RETURN` ahead of the gate, so it
  returns only connections that the foreign rule itself translated. A
  `destination` inside any Docker network's subnet is refused
  (`firewall.unsupported_integration`, naming the network), and a Docker
  network created later whose subnet contains an exemption's `destination`
  sets health `broken` and is reported unprotected until the exemption is
  removed. Reconcile reports a new foreign DNAT rule as health `degraded`
  naming it.
- **Block (R44).** `firewall.docker.enable` (revertible) writes marked block
  `docker` into `/etc/ufw/after.rules` and `after6.rules` and reloads UFW.
  The block is a self-contained filter section written from Docker's and
  UFW's documented behaviour (no code or text from GPL ufw-docker). UFW loads
  `after.rules` before it creates its user chains (`ufw_start` in
  `ufw-init-functions`), so the block declares `ufw-user-forward`; the
  declaration creates the chain empty at that point, and UFW fills it when it
  loads `user.rules` next. `iptables-restore -n` flushes every chain the
  input declares, so reloads never accumulate rules. IPv4:

  ```
  *filter
  :ufw-user-forward - [0:0]
  :DOCKER-USER - [0:0]
  :darkpanel-docker-bindings - [0:0]
  :darkpanel-docker-deny - [0:0]
  :dp-b-<binding id> - [0:0]
  -A DOCKER-USER -m conntrack --ctstate RELATED,ESTABLISHED -j RETURN
  -A DOCKER-USER -i docker0 -j RETURN
  -A DOCKER-USER -i br-+ -j RETURN
  -A DOCKER-USER -i <each custom bridge name> -j RETURN
  -A DOCKER-USER -s <each privateSources entry> -j RETURN
  -A DOCKER-USER <each forwardExemption> -j RETURN
  -A DOCKER-USER -m conntrack --ctstate DNAT -j darkpanel-docker-bindings
  -A DOCKER-USER -o docker0 -j darkpanel-docker-deny
  -A DOCKER-USER -o br-+ -j darkpanel-docker-deny
  -A DOCKER-USER -o <each custom bridge name> -j darkpanel-docker-deny
  -A DOCKER-USER -d 10.0.0.0/8 -j darkpanel-docker-deny
  -A DOCKER-USER -d 172.16.0.0/12 -j darkpanel-docker-deny
  -A DOCKER-USER -d 192.168.0.0/16 -j darkpanel-docker-deny
  -A DOCKER-USER -j RETURN
  -A darkpanel-docker-bindings -p <proto> -d <container addr> --dport <container port> -m conntrack --ctorigdst <host addr> --ctorigdstport <host port> -j dp-b-<binding id>
  -A darkpanel-docker-bindings -j darkpanel-docker-deny
  -A dp-b-<binding id> -s <each denied source CIDR> -j darkpanel-docker-deny
  -A dp-b-<binding id> -s <each allowed source CIDR, or 0.0.0.0/0> -j ufw-user-forward
  -A dp-b-<binding id> -j darkpanel-docker-deny
  -A darkpanel-docker-deny -m limit --limit 3/min -j LOG --log-prefix "[DARKPANEL DOCKER DENY] "
  -A darkpanel-docker-deny -j DROP
  COMMIT
  ```

  One `darkpanel-docker-bindings` line per allowed binding and container
  address, and one chain `dp-b-<binding id>` per binding (the id is the first
  12 hex digits of a hash of the binding, keeping the name within iptables'
  28-character limit); `--ctorigdst` is omitted for a binding on all host
  addresses. IPv6 is the same in `after6.rules` with `:ufw6-user-forward -
  [0:0]`, `-j ufw6-user-forward`, the IPv6 `privateSources` entries, and
  destination `fc00::/7`. Custom bridge names come from the networks'
  `com.docker.network.bridge.name` option.
- **Private sources (R46).** `privateSources` is a per-host firewall
  setting listing the source ranges whose traffic the block returns
  unexamined (the LAN, the tailnet, a provider's private network). Default:
  `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`, `100.64.0.0/10`,
  `fc00::/7` (which contains Tailscale's `fd7a:115c:a1e0::/48`) and
  `fe80::/10`. Each entry is a CIDR parsed by `netip`, at most 32 entries,
  and no entry may be shorter than /8 (IPv4) or /7 (IPv6), so `0.0.0.0/0`
  and `::/0` are refused. It and `forwardExemptions` are changed with
  `firewall.docker.set-sources` (revertible, lock `firewall`; input either
  list or both), rendered into the block, and recorded in
  snapshots with the module's settings. The destination lines (`-d
  10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`; IPv6 `fc00::/7`) are not
  configurable: they deny new non-DNAT forwards to private container
  addresses. Every source in a `privateSources` range reaches every
  published port whatever the bindings allow; the exposure view shows that
  as its own class (2.7), and the container view warns on each binding that
  a private range reaches only through this exemption.
- **Non-Docker `br-*` interfaces.** `br-+` also matches bridges Docker did
  not create (for example `br-lan`). Detection lists every `br-*` interface
  that is not a Docker network's bridge in `firewall.docker` and sets health
  `degraded` naming it, because traffic entering from it is exempt from the
  gate. The wildcard stays: it is what protects a Docker bridge created
  between two reconciles.
- **How the block decides.**
  - Established traffic returns first.
  - Traffic **entering from a Docker bridge** returns: it is
    container-originated whatever its source address (Docker enables
    `br_netfilter`, so container-to-container traffic on one bridge also
    traverses `FORWARD`), and Docker's own isolation chains still govern
    traffic between networks. Container-to-container and container outbound
    traffic therefore keep working on private, public IPv4 and global IPv6
    networks alike (R46).
  - Traffic from the `privateSources` ranges (the LAN, the tailnet) and the
    declared non-Docker forwards return.
  - A new connection from anywhere else that was DNATed reaches UFW only
    through a binding line matching its original host address and port and
    its translated container address and port, and then only through that
    binding's own chain, which applies **that binding's** source policy
    before jumping to `ufw-user-forward`. Two bindings to the same container
    port therefore never share a source policy: a source allowed on one is
    denied on the other by the other's chain, whatever route rules UFW
    holds. A DNATed connection matching no binding is denied. This gate is
    not limited to known bridges, so published ports on **any** bridge,
    including one created a moment ago with a custom name, are protected
    from creation.
  - A new connection from anywhere else forwarded without DNAT to a Docker
    bridge with a default name (`docker0`, `br-<id>`) or to a private
    destination is denied.
- **Coverage limit.** Direct-routed (non-DNAT) access to a network that has
  both a custom bridge name and a non-private subnet is covered only once
  reconcile writes that name into the block; Docker's routed gateway modes
  (`com.docker.network.bridge.gateway_mode_ipv4|ipv6=routed` or
  `nat-unprotected`) bypass DNAT altogether. Such networks are reported
  `unprotected`, set health `degraded`, and are listed in Out of scope as
  unsupported for the integration (P2, confirmed by the owner on
  2026-10-21, Owner decisions).
- **Address reuse (D1).** A route rule names a container address; the
  binding line also requires the connection's original host address and
  port. A container that later gets the same address inherits the allowance
  only if it also publishes the same host port to the same container port,
  which the host can bind for one container at a time.
- **Manual route rules.** Published-port traffic reaches `ufw-user-forward`
  only through a binding chain, so a manual `ufw route` rule to a container
  takes effect only for a binding Darkpanel allows, and only for the sources
  that binding's chain lets through. `firewall.docker.enable` lists such
  manual route rules in its diff, and the exposure view marks them "no
  effect without a Darkpanel binding".
- **Container policy (R45).** Policy is stored per **published binding**
  `{selector, hostAddr (default all), hostPort, proto}`, where `selector` is a
  container name or a Compose `project/service`, in
  `firewall_container_rules`. Actions (revertible): `firewall.container.allow`
  (an allowance, optional `from` CIDR), `firewall.container.deny` (an explicit
  deny for a source range on an allowed binding), `firewall.container.remove`
  (removes an allowance or deny) and the batch `firewall.containers.apply`
  (a list of those operations in one change). Each allowance is realised as
  one tagged UFW route rule per container address (every replica, every
  network, each family with a container address), its binding line and its
  binding chain's source lines; a deny is a line in the binding's chain
  ahead of its allows, which is where it is enforced, and also a tagged UFW
  route deny (the record R45 asks for). A UFW route rule names the container
  address and port but cannot name the host port, so it would apply to
  every binding of that container port; the route deny is therefore placed
  **after** every Darkpanel route allow for the same container address and
  port. A source a binding's chain lets through always meets its own
  binding's route allow first, so one binding's deny never removes another
  binding's allowance, and a source a binding denies never reaches
  `ufw-user-forward` through that binding. A deny whose `from` lies inside
  a `privateSources` range is refused (it could never match). The
  preview shows the rules and the effective verdict per source partition
  (2.7).
- **Proxied families.** A binding served by `docker-proxy` for a family in
  which the container has no address (IPv6 published to an IPv4-only
  bridge, the default for `-p 8080:80` on an IPv4-only network) never
  traverses `FORWARD`: the proxy accepts it on the host. It is therefore
  governed by UFW's `INPUT` rules like any host service: its allowance is a
  tagged `ufw allow in` rule for that family and host port (with `from`),
  and its denies are tagged `ufw deny in` rules inserted before it. Enabling
  the integration requires `DEFAULT_INPUT_POLICY` to be `DROP` or `REJECT`
  (refused otherwise), so an unallowed proxied binding is closed. This is an
  **invariant while the integration flag is on**, not an enable-time check:
  `firewall.defaults.set`, `firewall.state.enable`, `firewall.state.repair`
  and `firewall.snapshot.restore` refuse (`firewall.unsupported_integration`,
  a hard refusal) when their resulting configured incoming policy is
  `ALLOW`; health (below) checks it; and when the policy is found `ALLOW`
  anyway (an edit of `/etc/default/ufw`), health is `broken` and the
  exposure and container views show every proxied binding as open "by
  default incoming policy". A broad
  manual `INPUT` allow that covers such a port is the operator's own UFW
  rule, and the exposure view and the container view show the binding as
  open "by manual rule <tuple>". This is UFW governing Docker through
  `INPUT` rather than a route rule (P1, S3's wording confirmed by the owner
  on 2026-10-21, Owner decisions).
- **Integration flag and `state.disable`/`state.enable`.** The integration
  flag is configured state and persists across `firewall.state.disable`.
  `state.disable` with the flag on runs the cleanup (below) and leaves the
  flag set; `state.enable` with the flag on writes the block (and binding
  chains) into the files before `ufw enable`, and its prediction includes
  them.
- **Reconcile and health.** See 2.8. Health checks, for both families, that
  the block is present in the file; that the loaded `DOCKER-USER`,
  `darkpanel-docker-bindings` and `darkpanel-docker-deny` chains equal the
  block's rules exactly once and in order; that `FORWARD` jumps to
  `DOCKER-USER` before Docker's own accept rules; that no foreign rule is in
  `DOCKER-USER`; that no foreign DNAT forward lacks an exemption and no
  exemption's destination lies in a Docker subnet; that the configured and
  loaded incoming policy is `DROP` or `REJECT`; that no non-Docker `br-*`
  interface exists; and that every Docker network is covered. Anything else sets
  health `broken` or `degraded` with the reason; the UI offers
  `firewall.docker.repair` (revertible), which rewrites and reloads.
- **Cleanup of loaded chains.** Removing the block from the files does not
  remove its loaded chains. `firewall.docker.disable`, uninstall,
  `firewall.state.disable` while the integration is on, and restoring a
  snapshot taken without the integration run an owned-chain cleanup:
  1. record the cleanup intent (fsync);
  2. write the files without the block (`FileWriter`);
  3. re-read `DOCKER-USER` and refuse (`firewall.docker_user_in_use`) if it
     holds a foreign rule; otherwise load, with `*-restore -n` of the
     variant in use, an input that declares `DOCKER-USER` with only `-j
     RETURN` and flushes and deletes `darkpanel-docker-bindings`,
     `darkpanel-docker-deny` and every `dp-b-*` chain (the wave 2 spike
     confirms the restore syntax on all four images);
  4. `ufw reload` when UFW is active;
  5. verify that no `darkpanel-*` chain is loaded and `DOCKER-USER` holds
     only `-j RETURN`; clear the intent.

  Startup recovery repeats an interrupted cleanup from step 2.
- **`IPV6=no`.** UFW then never reads `after6.rules` and its start removes
  Docker's IPv6 rules (2.3); the integration reports a host with `IPV6=no`
  and any IPv6-enabled Docker network as `degraded` with that reason.

#### 2.7 Exposure view (R48, R49)

- **Rows.** Resource `firewall.exposure`: one row per (protocol, address,
  port, family), merging a listening socket with the Docker published
  binding it belongs to (a `docker-proxy` socket is shown as its container's
  binding).
- **Verdicts by source partition.** For each row the evaluator collects the
  predicates of every rule on the row's path that depend on the client
  (source addresses and CIDRs, in-interfaces, source ports and port ranges,
  and the mark states 2.3 derives), partitions the space of (source address,
  in-interface, source port) by them, and evaluates one representative per
  partition; source ports split into the intervals the rules name and the
  rest. A predicate the evaluator cannot bound (a mark set outside
  netfilter, an unsupported match) makes the affected partitions `unknown`.
  Source classes: `internet` (addresses outside every `privateSources`
  range, arriving on any non-loopback, non-Tailscale interface), `private`
  (one class per `privateSources` range, on non-Tailscale interfaces),
  `tailnet` (100.64.0.0/10 and fd7a:115c:a1e0::/48 on `tailscale0`),
  `localhost`. The `tailnet` verdict is the host firewall's: tailnet policy
  (ACL grants) can block traffic before it reaches the host and is not
  evaluated in v1, so the row says "host firewall; tailnet policy not
  evaluated", except that when the host's tailscaled has shields-up on, the
  `tailnet` class is `closed` with `decidedBy` "Tailscale shields-up". A
  class's verdict is `open`, `closed`,
  `filtered`, `partial` (with the allowed source sets and the rule that
  decides each) or `unknown`; `decidedBy` names the rule (UFW tuple and tag,
  a framework file line, a Docker or tailscaled chain, a block or binding
  line, Docker's raw-table drop, or "bound to loopback").
- **Published ports** follow the real path through `raw`, `nat` and
  `FORWARD` (2.3); there is no shortcut for the integration being off.
- **Harness check (R49).** The scanner probes every row's address and port
  from the internet network for both families, `partial` rows from an
  address in an allowed set and one outside it, and rows whose partitions
  split by source port from a source port in each interval (the scanner's
  fixed-source-port option). TCP must match exactly; for
  UDP an `open` verdict must not be scanned `closed`, and a `closed` or
  `filtered` verdict must not get a UDP response.

#### 2.8 Background task `firewall.reconcile`

Wakes on Docker events (container start, die, destroy, network connect and
disconnect, network create and destroy) and every 5 minutes. It recomputes
route rules, binding lines, binding chains and bridge lines from
`firewall_container_rules`, current container addresses and current
networks, and checks health (2.6). It does nothing while health reports
foreign `DOCKER-USER` rules (2.6).
Changes go through `firewall.containers.apply` or `firewall.docker.repair` as
actor `system:firewall.reconcile` (1.4).

#### 2.9 Uninstall (R30)

The firewall `Uninstaller` declares the option `rules` (1.11), set with
`--module-option firewall.rules=<value>`:

- `remove` (default): delete every Darkpanel-tagged rule; manual rules stay;
- `keep`: leave every rule; tags stay;
- `baseline`: restore the pinned baseline snapshot, after printing which
  manual rules it would add or remove.

In every case the Docker integration's owned-chain cleanup runs (2.6).

#### 2.10 Web pages

`/hosts/:id/firewall`: configured and loaded state (with "out of sync" and
"unsupported configuration" banners), rules table with origin badges, add and
delete through `ChangeDialog`, snapshots (list, compare, restore), Docker
integration (detection, enable, health, repair) and the exposure table with
filters. The Docker integration panel also edits `privateSources` and
`forwardExemptions`. The container action slot gets Allow and Deny.

### 3. Docker management (R50–R55)

Module `docker`; agent side; lock key `docker`; preflight: socket present,
API 1.44 or later. Every undo reads the effects journal (1.2) and acts only
on effects that occurred. Docker Engine calls are not `Local` (1.2):
`CommitBy` is the call's start plus 2 minutes. This table is the **one authoritative comparator
per Docker effect kind**; 1.2's general description defers to it. Each
row names the fields recorded as the post-state right after the action,
the fields compared before an undo (any difference makes the effect
`unresolved` (`conflict`, 1.2), so an older undo never reverses a later
operation), and the expected state after
the undo, recorded before the inverse runs and compared after it
(identities and versions the inverse necessarily renews are excluded):

| Effect kind (actions) | Post-state recorded and compared before undo | Expected after undo (compared) | Excluded after undo |
|---|---|---|---|
| `container.run` (`start`, `stop`, `restart`) | `Id`, `Created`, `State.Running`, `State.StartedAt`, `State.FinishedAt` | `Id`, `Created`, `State.Running` equal to the journaled before-state | `StartedAt`, `FinishedAt` (an undo that starts a container sets new ones) |
| `network.endpoint` (`connect`, `disconnect`) | container `Id`, network `Id`, endpoint present or absent, `EndpointID`, `Aliases`, `IPAMConfig` | present or absent as before; `Aliases` and `IPAMConfig` equal to the journaled settings | `EndpointID` (reconnecting creates a new endpoint) |
| `network.created`, `volume.created` | `Id` (volume: `Name`), `Created` (networks), `Labels`, and no container references it | absent | — |

Normalization: timestamps are compared as instants (RFC 3339 with
nanoseconds, any zone), lists of names as sorted sets, an absent and an
empty list or map as equal, addresses in canonical `netip` form.

| Action | Class | Undo |
|---|---|---|
| `docker.container.start` | compensable | stop the container only if it was stopped before (journaled before-state) |
| `docker.container.stop` | compensable | start the container only if it was running before |
| `docker.container.restart` | compensable | start the container if it is stopped; a restart itself is not reversed |
| `docker.container.remove` | irreversible (typed name) | — |
| `docker.container.inspect`, `docker.container.logs` | read | — |
| `docker.container.reveal-env` | read, admin, secret delivery (1.14) | — |
| `docker.image.pull` | compensable, long-running | nothing: the host is usable either way; a pulled or re-pointed tag is not reverted (documented) |
| `docker.image.remove`, `docker.image.prune` | irreversible | — |
| `docker.network.create`, `docker.volume.create` | compensable | remove the created object if it is unused |
| `docker.network.remove`, `docker.volume.remove` | irreversible | — |
| `docker.network.connect` | compensable | disconnect only if this change attached it |
| `docker.network.disconnect` | compensable | reconnect with the journaled endpoint settings only if this change detached it |
| `docker.compose.up` | irreversible (typed project name), admin | — (Compose may recreate existing containers, removing their writable layers, which nothing can restore) |
| `docker.compose.down` | irreversible (typed project name), admin | — (removes containers and their writable layers) |
| `docker.compose.pull` | compensable, admin, long-running | nothing (documented, as for image pull) |
| `docker.compose.restart` | compensable, admin | start the containers that were running before and are now stopped |

- Resources: `docker.engine`, `docker.containers` (with a stats summary),
  `docker.images`, `docker.networks`, `docker.volumes`, `docker.projects`.
- Streams: `docker.logs` (follow, since, tail), `docker.stats`, `docker.pull`
  (keyed by change id).
- **Secrets in inspect.** `docker.container.inspect` returns environment
  variable names with values replaced by `[redacted]` for every container;
  `docker.container.reveal-env` returns values through secret delivery
  (1.14) and refuses Darkpanel-owned containers. Logs of Darkpanel-owned
  containers are filtered for the secrets their module registered (6, 7.2).
- **Prune (R51).** The plan lists every image to be removed and the bytes
  reclaimed; apply removes exactly that list (re-checked against the state
  hash).
- **In use (R52).** Removing a network or volume used by any container fails
  the plan with `docker.in_use` naming the containers.
- **Ownership.** Mutating actions on objects labelled `darkpanel.module`
  refuse with `docker.owned` (1.8).
- **Compose (R53).**
  - **Trust boundary.** Compose files are operator-authored and trusted like
    any root-run configuration; Darkpanel never writes them. Running them is
    an admin-scope action, and the plan says so ("runs the project's Compose
    file as root"). Every Compose action is long-running (1.3).
  - **Discovery.** Projects come from container labels
    (`com.docker.compose.project`, `.project.working_dir`,
    `.project.config_files`, `.project.environment_file`) and are recorded in
    `docker_projects` (name, directory, files, env files, config hash) when
    first seen, so a project stays listed after `down`.
  - **Profiles.** Active profiles are not discoverable from labels. Each
    project has an operator-set profile list stored in `docker_projects`
    (default none), passed as `--profile` on every run and shown in the plan.
  - **Validation.** The directory and every file must be absolute, exist,
    and be owned by root or the directory's owner, with no symlink in the
    path; otherwise `docker.compose_unavailable` with the reason. A changed
    config hash since the last run is shown in the plan. A project named
    `darkpanel*`, or one whose resolved configuration or current containers
    (read through the Engine API) name an object labelled `darkpanel.module`,
    is refused with `docker.owned`. A resolved configuration containing a
    **provider service** (a service with a `provider` key, which makes
    Compose run a host program) or any other top-level or service key outside
    the set the pinned Compose version documents for containers, networks,
    volumes, configs and secrets is refused with
    `docker.compose_unavailable`; Darkpanel never lets Compose run host
    programs.
  - **Remote configuration and builds.** Compose resolves `include` and
    `extends` from Git and OCI remotes before any validation can see the
    result, and its Git loader runs the `git` program (and through it
    `ssh`); both loaders are on unless disabled (Compose's
    `pkg/remote/git.go` and `oci.go`: `COMPOSE_EXPERIMENTAL_GIT_REMOTE` and
    `COMPOSE_EXPERIMENTAL_OCI_REMOTE`, parsed as booleans, default true).
    Every Compose run, `config` included, therefore sets both to `false`,
    so a remote reference fails the run with Compose's own error, which the
    plan reports as `docker.compose_unavailable`. A service with a `build`
    section could make Compose start a build through the buildx plugin, a
    host program, so `up` always runs with `--no-build` and a resolved
    configuration whose service has `build` but no image present locally is
    refused at plan time. The wave 3a harness test runs a project whose
    `include` names a Git URL and one with a `build` section and checks that
    no `git`, `ssh` or `docker-buildx` process ran.
  - **R54 exception.** Compose has no Engine API, so validation reads the
    resolved configuration from `docker compose config --format json`. This
    is the one place Darkpanel parses `docker` CLI output: Compose's own
    JSON document, not human-readable text, parsed strictly and fuzzed (9).
    ADR 0014 records it, together with the Compose execution exception to
    ADR 0009 (an allowlisted binary that reads operator-authored files and
    starts containers through the daemon).
  - **Compose 5.6.0 schema:**
    - compose refusals — TopLevelKeys lacks jobs → jobs is a top-level property in compose-go v2.16.1 — https://raw.githubusercontent.com/compose-spec/compose-go/v2.16.1/schema/compose-spec.json
    - compose refusals — ServiceKeys lacks pre_start → pre_start is a service property in compose-go v2.16.1 — https://raw.githubusercontent.com/compose-spec/compose-go/v2.16.1/schema/compose-spec.json
    - Basis, checked 2026-10-30: the host runs Compose v5.1.4
      (`docker compose version`), whose `go.mod` requires compose-go v2.11.0
      (https://raw.githubusercontent.com/docker/compose/v5.1.4/go.mod); the
      harness pins Compose v5.6.0 (`test/harness/images.json`), whose `go.mod`
      requires compose-go v2.16.1
      (https://raw.githubusercontent.com/docker/compose/v5.6.0/go.mod). The
      key allowlists of the Compose validation are the property names of the
      host schema (compose-go v2.11.0). Comparing the pinned schema against
      it for the top level and the service, network, volume, secret and config
      definitions (a service's properties collected through its `allOf`
      references), the pinned schema adds the two keys above and removes none.
      The network, volume, secret and config definitions are unchanged.
  - **Running.** `/usr/bin/docker compose --project-directory <dir> -p <name>
    -f <file>... [--env-file <f>]... [--profile <p>]... <verb>` with fixed
    verb flags (`up -d --no-build`, `down`, `pull`, `restart`, and `config
    --format json` for validation). The environment is only `DOCKER_CONFIG`
    and `HOME`, both `/var/lib/darkpanel/agent/docker-config`, plus
    `COMPOSE_EXPERIMENTAL_GIT_REMOTE=false` and
    `COMPOSE_EXPERIMENTAL_OCI_REMOTE=false`; the wave 0 allowlist entry for
    Compose fixes this environment (1.8). `docker-config` is a
    Darkpanel-owned directory whose `config.json` holds no credential
    helpers and which has no plugin directory, so the CLI finds the Compose
    plugin only in the system
    plugin path, which is checked to be root-owned and not writable by
    others. Pulls from private registries through Compose are out of scope in
    v1 (documented). Success is the exit code; the result is read back from
    the Engine API.
- **Exposure in container views (R55).** The container page shows the
  `firewall.exposure` rows of its published bindings and the container
  action slot's buttons; firewall's Allow and Deny plan
  `firewall.container.allow` and `deny` directly on the same host, with the
  firewall's diff, guards and revert countdown. Without an available firewall
  module or a supported integration they are disabled with the reason.
- **Web.** `/hosts/:id/docker` with containers, images, networks, volumes and
  projects tabs; a container page with logs (follow), stats, inspect and
  exposure.

### 4. Tailscale

#### 4.1 Node (R56, R57) — module `tailscale`, agent side, lock key `tailscale`

- **Status (R56).** Resource `tailscale.status`: backend state, self
  (addresses, DNS name, tags), peers (online, addresses, connection `direct
  <endpoint>`, `derp <region>` or `peer-relay`), preferred DERP region, and
  whether Tailscale SSH (`RunSSH`) is on.
- **Latency.** The local API has no netcheck endpoint. Resource
  `tailscale.latency` reports two measurements per region of the DERP map:
  **STUN latency** (labelled as such: STUN binding requests from the agent to
  the region's STUN endpoints, three probes, median; "unavailable" when the
  region has no STUN endpoint or UDP is blocked) and **DERP HTTPS probe
  latency** (HTTPS `GET /derp/latency-check` to the region's nodes, median of
  three; it measures reaching the server, not a relayed peer path;
  "unavailable" on failure). A small hand-written STUN client; no Tailscale
  library.
- **Preferences (R57).** `tailscale.prefs.set` (revertible): `wantRunning`,
  `advertiseRoutes`, `exitNode`, `advertiseExitNode`, `acceptRoutes`,
  `shieldsUp`, `runSSH`. It patches only the fields given (masked prefs); the
  snapshot holds the previous values of exactly those fields, and revert
  patches them back. It declares `RepairedBy: tailscale.prefs.repair`.
- **`tailscale.prefs.repair`** (revertible, `RepairedBy` itself, lock
  `tailscale`, guard `tailscale.link`): the repair action for a
  `prefs.set` whose revert failed (1.3). Input: `outcome: restore | keep`,
  optional with default `restore`, so the plan from `{}` the registry
  requires (1.3) is valid; the operator sets it through the repair
  request's optional `input` (1.3). The repaired change comes from the
  core-level `repairs` field, never from input. `restore` plans a masked
  patch from the current prefs to the repaired change's snapshot values
  (the fields it set, nothing else); `keep` plans a masked patch from the
  current prefs to the values the repaired change set (its input). Both
  leave every other field alone. Its own snapshot is the current values of
  those fields, so an unconfirmed repair reverts like any `prefs.set`.
  Verify reads the prefs back and requires them equal to the target;
  output `repairOutcome` is `restored` or `kept` only after that check, so
  `kept` always means the repaired change's intended prefs hold.
- **Guard `tailscale.link` (overridable).** The agent classifies each
  protected flow of 2.3 by whether it depends on Tailscale: it arrives on or
  leaves through `tailscale0`, or its route uses a subnet route or exit node
  through `tailscale0`. The local API does not list live Tailscale SSH
  sessions, so while `RunSSH` is on the agent assumes one may be the access
  path. The guard fails for:
  - `wantRunning: false`, `tailscale.node.logout`, or `tailscale.node.login`
    with `relogin: true`, whenever a flow depends on Tailscale or `RunSSH` is
    on;
  - `shieldsUp: true` whenever an inbound flow arrives over Tailscale or
    `RunSSH` is on (shields-up blocks Tailscale SSH);
  - `runSSH: false` whenever `RunSSH` is on, unless the request came over a
    path the other flows show does not depend on Tailscale;
  - `acceptRoutes: false`, a different `exitNode`, or a removed
    `advertiseRoutes` entry or exit-node advertisement, whenever a protected
    flow's route uses it.
- **Login and logout.** `tailscale.node.login` (compensable; undo: log out
  only if the node was logged out before, journaled): `authKey` (secret),
  `hostname`, `advertiseTags`; refused on a logged-in node unless `relogin:
  true` (guarded). `tailscale.node.logout` is irreversible (typed hostname)
  and guarded.
- **Package.**
  - `tailscale.package.install` (compensable; undo: remove the package only
    if this change installed it) refuses when the package is present
    (`tailscale.package_present`); available without tailscaled (1.7).
  - `tailscale.package.update` (irreversible, typed hostname) upgrades to the
    repository's current version; refuses a held package
    (`tailscale.package_held`).
  - `tailscale.package.pin` (compensable, long-running; DERP hosts, 4.3)
    installs exactly `tailscale=<version>` and holds it. Changing tailscaled
    can cut the agent's own link to the controller, so the pin is an
    **agent-local transaction that rolls itself back**, like the updater
    (ADR 0007), with a module recovery handler (1.3b). The phases run in a
    root transient systemd unit `darkpanel-task-<change id>-apply-<attempt>.service`
    (the phases and their automatic rollback run in that unit; an undo
    runs in `darkpanel-task-<change id>-undo-<attempt>.service` and recovery in
    `darkpanel-task-<change id>-recover-<attempt>.service`, 1.3c)
    started through D-Bus (the transient-unit runner for long-running agent
    applies, 1.3), so a restart of `darkpanel-agent`, which the new
    tailscaled can provoke by cutting its link, does not kill `dpkg`
    mid-install. Phases, each journaled before it runs, in `tailscale_pins`:
    1. `recorded`: the prior version and the prior hold state (the pin
       requires the package installed and refuses otherwise with
       `tailscale.unavailable`; a DERP host is on the tailnet already);
    2. `unheld`: `apt-mark unhold tailscale` (apt aborts with `-y` on any
       change to a held package, so every apt step below runs unheld);
    3. `staged`: `APT-4` (1.8) with `tailscale=<prior version>` and again
       with the target version, which leave the `.deb` files in
       `/var/cache/apt/archives/`; the prior one is copied with
       `FileWriter.WriteManaged` into the managed directory
       `/var/lib/darkpanel/agent/tailscale-debs/` (flat name
       `tailscale_<version>_<arch>.deb`, its SHA-256 journaled), so rollback
       depends on neither the repository nor the network. If the prior
       version can no longer be downloaded, the pin stops here with
       `tailscale.pin_failed` after restoring the prior hold state; nothing
       was installed;
    4. `installed`: `APT-3` with `tailscale=<version>`;
    5. `held`: `apt-mark hold tailscale`;
    6. `checking`: for up to 120 s, tailscaled answers on its socket, the
       node is back in its prior backend state, and the agent's session to
       the controller is up (for a host whose link runs over the tailnet);
    7. `done`.

    **Rollback** (a failed check, a failed phase 4 or 5, or the recovery
    handler finding the transaction in phases 2–6 with its unit no longer
    running, for example after a reboot), each step journaled:
    `apt-mark unhold tailscale` if it is held; `dpkg --configure -a` when
    phase 4 was interrupted (dpkg refuses further installs until the
    interrupted one is configured); `APT-5` with the staged file after
    checking its SHA-256; restore the prior hold state; restart
    tailscaled; verify the prior version and backend state; record the
    unit's `core_task_runs` row `failed` with the rollback's result (the unit
    never changes the change's state; the agent's engine settles it, 1.3c).
    The controller learns the result through the lookup and the
    journal when the link returns. An agent that starts while the unit is
    still running waits for it rather than rolling back. Undo (when the
    controller asks) performs the same rollback. The harness fails the
    check after phase 5 with the controller link blocked and expects the
    prior version, held state and link back without the controller.
    `tailscale.package.unpin` (compensable, undo: re-hold) restores the
    hold state recorded by the pin it reverses without changing the
    installed version.
  - **Version mapping.** `<version>` for a DERP host is
    `release/derper.version` without its leading `v` (`v1.102.0` →
    `1.102.0`); the DERP spike confirms that Tailscale's apt versions use
    that form on all four OS images.
  - Files: `/usr/share/keyrings/tailscale-archive-keyring.gpg` (fingerprint
    pinned in code) and `/etc/apt/sources.list.d/tailscale.list`. If either
    exists and is not byte-identical to what Darkpanel would write, install
    refuses with `tailscale.repo_conflict`; if identical, it is used but not
    recorded as owned, so uninstall leaves it.
  - `apt-get` runs only as the vectors `APT-1`–`APT-6` of 1.8:
    `package.install` and `package.update` run `APT-1` then `APT-2`; a DERP
    host's install (4.3) runs `APT-1` then `APT-3`; the pin uses `APT-1`,
    `APT-4`, `APT-3` and, for rollback, `APT-5`; uninstall runs `APT-6`.
    The source list `APT-1` names is the file below. The
    repository line uses the OS codename and the `stable` track; unsupported
    OS or architecture refuses. Package installs and updates are
    long-running (1.3).
- **Module preflight** checks only the OS; `tailscale.status` and every action
  except `package.install` report `tailscale.unavailable` through per-action
  availability when the socket does not answer.
- **Web certificate (ADR 0010).** When the controller's host is on a tailnet,
  the controller dispatches the internal action `tailscale.cert.get` (read,
  `Internal`, 1.7) to its own host's agent, which returns the `tailscale cert`
  pair for the node's MagicDNS name. The controller uses it through the
  certificate-source interface (1.12) for its Tailscale listeners and adds
  the MagicDNS name through the extra-names hook, renewing at two thirds of
  the certificate's life and on address changes. Without it the existing web
  root path is unchanged.
- **Web.** `/hosts/:id/tailscale` with status, peers, latency, preferences and
  install or update.

#### 4.2 Tailnet (R58–R60) — module `tailnet`, controller side, lock key `tailnet`

- **Connect (R58).** `tailnet.account.connect` (compensable; undo: after 1.9's cleanup check, delete the
  credentials row this change created): tailnet name, OAuth client id and
  secret (sealed), and the tags the client may assign (`tagsAllowed`).
  Connect obtains a token and probes read capabilities (1.9).
  `tailnet.account.disconnect` is irreversible (typed tailnet name) and has no
  compensation link. One tailnet per controller in v1.
- **4.2a Scopes.** `devices:core`, `devices:routes`, `auth_keys`,
  `policy_file`, and `devices:posture_attributes`, which `policy_file`
  requires (it also requires `devices:core:read`, covered by
  `devices:core`). A client holding `auth_keys` or `devices:core` must have
  tags, and keys it creates must carry those tags or tags they own through
  `tagOwners`. Tokens come from `POST /api/v2/oauth/token` and last one hour;
  the client caches one and renews it at 50 minutes.
  Contract check (3b) result: see the recorded contract below.
- **Recorded contract (Tailscale, checked 2026-10-30).**
  - **Sources:** https://tailscale.com/kb/1623/trust-credentials (scopes,
    last validated Jan 30, 2026), https://tailscale.com/kb/1215/oauth-clients
    (token endpoint, tags), https://api.tailscale.com/api/v2?outputOpenapiSchema=true
    (Tailscale's OpenAPI document, which calls itself unstable; the endpoint
    shapes below). https://tailscale.com/api returns a page rendered by
    script with no API text, so nothing was taken from it. Paths below are
    relative to `https://api.tailscale.com`; `{t}` is the tailnet name or
    ID, or `-` for the token's own tailnet. An item the sources do not state
    is written `not documented`.
  - **Scopes:** connect's read probes: list devices `devices:core:read`,
    list keys `auth_keys:read`, read the policy `policy_file:read`. Write
    groups: authorize, expire and delete a device `devices:core`; routes
    `devices:routes` (its `GET` needs `devices:routes:read`, which it
    includes; no other scope); create and delete an auth key `auth_keys`;
    write the policy `policy_file` (it includes read and validate). Each
    write scope includes its `:read` scope. Dependencies confirmed:
    `policy_file:read` needs `devices:posture_attributes:read` and
    `devices:core:read`; `policy_file` needs `devices:posture_attributes` and
    `devices:core:read`. Tags: a credential created with `devices:core` or
    `auth_keys` must have one or more tags, and auth keys created with it
    must have those exact tags or tags they own through `tagOwners`; tags in
    a token request count only for tokens with `devices:core`, `auth_keys`
    or `all`. Every scope may `GET .../keys/{id}` for the key in use. The
    legacy ids (`devices`, `acl`, `routes`) remain valid only for existing
    credentials.
  - **Token:** `POST /api/v2/oauth/token`, the OAuth 2.0 client credentials
    request format; the page's example sends `client_id` and `client_secret`
    as form data (`curl -d`) and neither `grant_type`, `scope` nor `tags`.
    `scope` and `tags` are optional space-delimited lists the client must be
    permitted to grant. Success: `{"access_token":"tskey-...","token_type":
    "Bearer","expires_in":3600,"scope":"devices"}`. The token lasts one hour
    and "this time cannot be modified". Content type of the request, whether
    `grant_type` is required, and the answer for wrong credentials: not
    documented.
  - **Devices:** `GET /api/v2/tailnet/{t}/devices?fields=all` answers 200
    `{"devices":[Device]}`; `GET /api/v2/device/{id}?fields=all` answers 200
    one `Device` (400 invalid id, 404 unknown). Scope `devices:core:read`.
    Without `fields=all` the default subset omits `enabledRoutes` and
    `advertisedRoutes`. `Device` members at the top level: `nodeId` (the
    preferred id) and `id` (the legacy numeric id, still accepted), `name`
    (MagicDNS name), `hostname`, `os`, `user`, `addresses` (strings), `tags`
    (strings), `authorized` (bool), `lastSeen` and `expires` (date-time
    strings such as `2022-12-01T05:23:30Z`; `lastSeen` is omitted when the
    device never connected or is connected now), `keyExpiryDisabled` (bool),
    `advertisedRoutes` and `enabledRoutes` (strings). `{id}` accepts the node
    id (preferred) and the numeric id.
  - **Routes:** `GET /api/v2/device/{id}/routes` (`devices:routes:read`)
    answers 200 `{"advertisedRoutes":[...],"enabledRoutes":[...]}`, 404
    unknown. `POST` (`devices:routes`) takes `{"routes":[...]}` and replaces
    the enabled list; advertised routes cannot be set through the API;
    it answers 200 with the same two members, 404 unknown. How an exit node
    is expressed (the routes `0.0.0.0/0` and `::/0`): not documented.
  - **Device writes:** authorize `POST /api/v2/device/{id}/authorized`, body
    `{"authorized":true|false}` (required; 400 when missing or not a bool),
    200 without a described body, 402 when the plan or billing refuses, 404
    unknown. Expire `POST /api/v2/device/{id}/expire`, no body, 200 without a
    described body, 404 unknown. Delete `DELETE /api/v2/device/{id}`, 200
    without a described body, 400 invalid id, 501 for a device not owned by
    the tailnet; the answer for an unknown device: not documented. All
    three need `devices:core`.
  - **Keys create:** `POST /api/v2/tailnet/{t}/keys`, scope `auth_keys`.
    Request: `capabilities.devices.create` with `reusable`, `ephemeral`,
    `preauthorized` (bools) and `tags` (strings; required when the caller is
    an OAuth-derived token), `description` (at most 50 alphanumeric
    characters, hyphens and spaces allowed), `expirySeconds` (int64; default
    90 days; limits not documented), `keyType` (default `auth`). The body
    must hold a `capabilities.devices` object, which may be `{}`. Response
    200 `Key`: `id`, `key` (only here), `keyType`, `created`, `expires`,
    `expirySeconds`, `description`, `capabilities`, `userId`. 404 unknown
    tailnet.
  - **Keys list:** `GET /api/v2/tailnet/{t}/keys[?all=true]` answers 200
    `{"keys":[Key]}` of active keys. `Key` has `description`, and the
    document's example list shows `description` on every entry, so entries
    carry it and not only ids. Without `all`, a token derived from an OAuth
    client lists the tailnet's OAuth clients, not its auth keys; with
    `all=true` it lists all auth keys, API access tokens and OAuth clients.
    Reads need `auth_keys:read` for auth keys (`api_access_tokens:read`,
    `oauth_keys:read` and `federated_keys:read` for the other types); the
    answer to `all=true` with only `auth_keys:read`: not documented.
    Timestamps `created`, `expires`, `updated` and `revoked` are date-time
    strings; `revoked` is a timestamp, not a bool. `keyType` is `auth`,
    `client`, `api` or `federated`.
  - **Keys get and delete:** `GET /api/v2/tailnet/{t}/keys/{id}`
    (`auth_keys:read`) answers 200 `Key`; a revoked (deleted) or expired key
    still answers 200 with `invalid: true`; 404 for an unknown tailnet or
    key. `DELETE` (`auth_keys`) answers 200 without a described body, 403 for
    insufficient access, 404 for an unknown tailnet. Deleting a deleted key,
    and an unknown id on `DELETE`: not documented.
  - **Policy read:** `GET /api/v2/tailnet/{t}/acl` with `Accept:
    application/hujson` (`policy_file:read`) answers 200 with the HuJSON text
    as `application/hujson`; `Accept: application/json` answers JSON, and
    any other value or none answers HuJSON. The `ETag` response header is
    documented as present; its exact form is not stated, and the document's
    `If-Match` examples are quoted (`"e0b2816b418"`, `"ts-default"`), so the
    value is sent back as received. 400, 403, 404.
  - **Policy write:** `POST /api/v2/tailnet/{t}/acl` (`policy_file`), request
    `Content-Type: application/hujson` for HuJSON text (JSON is also
    accepted), `If-Match: <ETag as read>` to avoid overwriting a newer
    policy (the value `ts-default` matches only the untouched default).
    200 returns the stored policy in the format of `Accept`; an `ETag` header
    on that answer: not documented. 400 for an incorrectly defined policy or
    one whose `tests` fail; 412 `{"message":"precondition failed, invalid old
    hash"}` for a stale `If-Match`; 403, 404.
  - **Policy validate:** `POST /api/v2/tailnet/{t}/acl/validate`
    (`policy_file:read`), body the policy text as `application/hujson` or
    JSON (a JSON array of tests runs them against the current policy
    instead). It changes nothing. 200 with an empty body means validation
    and tests passed (the document's success example is `{}`); 200 with
    `{"message":"test(s) failed","data":[{"user":...,"errors":[...]}]}` when
    the policy's own `tests` fail; 200 with `{"message":"warning(s)
    found","data":[{"user":...,"warnings":[...]}]}` for warnings; 400
    `{"message":...}` for a policy that does not parse.
  - **Errors:** body `{"message":"..."}`. 403 "Forbidden" with that body;
    whether it names the scope or tag: not documented. 404 `{"message"}`.
    429 `{"message"}`; `Retry-After`: not documented. 401: not documented.
    Also 400, 402, 500, 501, 502 and 504 with `{"message"}`.
  - **Divergences:**
    - Tailnet internals — KeyInfo.Revoked bool → revoked is a date-time timestamp — https://api.tailscale.com/api/v2?outputOpenapiSchema=true
    - Tailnet internals — KeyInfo has no member for a deleted or expired key → GET /tailnet/{t}/keys/{id} answers invalid: true for it — https://api.tailscale.com/api/v2?outputOpenapiSchema=true
    - Tailnet internals — Keys GET /api/v2/tailnet/{t}/keys → needs the query all=true, because without it an OAuth-derived token lists only OAuth clients — https://api.tailscale.com/api/v2?outputOpenapiSchema=true
- **Devices.** Read `tailnet.device.list`, `tailnet.policy.get`.
  Compensable: `tailnet.device.authorize` (undo: de-authorize, if this change
  authorized it); `tailnet.device.set-routes` (approve routes and exit node;
  undo: set the journaled previous list); `tailnet.authkey.create` (undo:
  delete the key; the key is shown once; description `darkpanel-<change
  id>`). Irreversible (typed): `tailnet.device.expire`,
  `tailnet.device.delete`.
- **Auth keys** require `tags`, each within `tagsAllowed`.
- **Policy (R59).** `tailnet.policy.set` (compensable): input is the full
  HuJSON text. Plan fetches `GET /api/v2/tailnet/{t}/acl` with `Accept:
  application/hujson` and its `ETag`, validates the new text with `POST
  .../acl/validate` (a 200 with an empty body or `{}` is valid; a 200 with
  `{message, data}` or a 400 with `{message}` becomes
  `tailnet.policy_invalid`), and renders a line diff. Apply posts with
  `If-Match` on that ETag; a 412 fails with `tailnet.policy_conflict`. The
  effects journal stores the before-text and the after-ETag. Undo posts the
  before-text with `If-Match` on the after-ETag; if the policy changed since,
  undo fails with `tailnet.policy_conflict` and the change needs repair, so
  later edits are never erased.
- **Structural edits.** Other modules edit the policy through
  `tailnet.policy.patch` (compensable, same ETag rules, `Converges`): a list
  of typed edits applied with `github.com/tailscale/hujson`, so comments and
  unrelated content are preserved. Raw JSON Patch is not accepted, because
  positional array operations do not converge (a repeated append adds a
  second entry, a repeated indexed removal removes another element). The
  four edit kinds, each addressed by a JSON Pointer to an object member or
  an array, all converge:
  - `set {path, value}`: set an object member; equal value → no change;
  - `delete {path}`: remove an object member; absent → no change;
  - `addItem {path, value}`: append `value` to the array unless an element
    equal to it (by canonical JSON, comments ignored) is already present;
  - `removeItem {path, value}`: remove every element equal to `value`;
    none → no change.

  A path through an array index, and the JSON Patch kinds `move`, `copy`,
  `replace` on array elements and `test`, are refused at plan with
  `request.invalid_input`. Run again with the same input after a partial
  or complete apply, the patch plans an empty diff when its edits are
  already present, which is what forward composites rely on (1.3, 5).
  The DERP module's region step is `set` and `delete` on
  `/derpMap/Regions/<id>` and on `/derpMap/OmitDefaultRegions`, and
  `addItem`/`removeItem` on `/grants` for the visibility grant (4.3).
- **Join (R60), before enrollment.** A new host reaches the controller's
  agent listener only over loopback or the tailnet by default (ADR 0003), so
  it joins the tailnet first:
  1. "Add host" in the Hosts dialog (through the host-create extension,
     1.12) and `darkpanel host create --tailnet-join --tags <t>` extend
     `core.host.create` with optional `tailnetJoin` and `tags` (additive
     input fields). The controller creates a one-use, pre-authorized,
     ephemeral-false auth key with those tags and an expiry equal to the
     enrollment token's (one hour), description `darkpanel-enroll-<host id>`.
  2. **Bundle v2.** The existing enrollment token (`transport.Token`, five
     fields) is unchanged. A join adds a **versioned bootstrap bundle**: the
     string `dpb2.` followed by base64url JSON `{v: 2, token: <the existing
     token string>, tailnetKey, controllerTailnetAddr}`. The parser accepts
     either a plain token (as today) or a `dpb2.` bundle; any other version is
     refused with an explicit error. The bundle is a one-use bearer secret,
     shown once like the token; the auth key travels in it in clear, because
     no key shared with the not-yet-enrolled host exists, and its exposure is
     bounded by its single use, its tags and its one-hour expiry. The
     controller stores the key sealed for cleanup only.
  3. **Cleanup.** The key's `tailnet_keys` row (kind `enroll`, owner the
     host id) is written before the create call (1.9). When the host
     enrolls, the row becomes `used` and the controller deletes nothing (the
     key is used up). When the token expires unused or the host is deleted
     first, the row becomes `retired` and the sweep deletes the key by
     description; an uncertain key creation is swept as in 1.9.
  4. `install.sh --role agent` and `darkpanel system install --role agent`,
     given a `dpb2.` bundle, install tailscale (the same code as
     `tailscale.package.install`), log in through the local API with the key,
     wait up to 60 s for a tailnet address, and then enroll over the tailnet
     to `controllerTailnetAddr`. A failure leaves the host un-enrolled and
     prints the reason; rerunning with a fresh bundle is the retry.
  5. The host page shows the join result. `tailnet.node.join` (composite,
     `unwind: reverse`: `install` when absent, `key`, `login`) remains as the
     retry path for an already enrolled, reachable host.

  Wave 4 owns these edits: `core.host.create` and the enrollment bundle in
  `internal/controller`, the bundle parser in `internal/transport/token.go`
  (and its fuzz seed), `install.sh.in`, the agent install path in
  `internal/install`, the CLI flag, and the Hosts dialog extension.
- **Web.** `/tailnet` with devices, approvals, auth keys and a policy editor
  with validate and diff.

#### 4.3 DERP (R61–R64) — module `derp`

- **Artifact.** The release workflow builds `tailscale.com/cmd/derper` at
  `release/derper.version` for amd64 and arm64 (`CGO_ENABLED=0`), names them
  `derper-<derper version>-linux-<arch>`, and lists them in a `derper.json`
  manifest (`{darkpanelVersion, derperVersion, artifacts: {arch: sha256}}`)
  that is itself listed in the release's signed `checksums.txt`. The agent
  takes the derper artifact only from the release its own Darkpanel version
  belongs to, verified through that release's signature and manifest. The
  harness builds them with test keys and caches the build keyed by
  `release/derper.version`.
- **Version coupling and its cost.** This spec assumes that `-verify-clients`
  needs derper and tailscaled from the same version; the assumption is not
  yet backed by a cited upstream statement. The DERP spike either cites one
  (Tailscale's custom-DERP documentation or the derper source) and records
  it here, or shows `-verify-clients` working across adjacent versions, in
  which case the pin is relaxed to "tailscaled not older than derper" and
  the hold is dropped (an owner-visible spec change, applied by the
  integrator). Until then, a DERP host's tailscaled is
  therefore pinned and held at the derper version (`tailscale.package.pin`)
  and refuses ordinary updates. Consequence, recorded here, in the threat
  model and in the owner checklist: the DERP host receives a Tailscale
  update only when a Darkpanel release bumps `release/derper.version`, so
  bumping it promptly after a Tailscale security release is part of the
  release routine.
- **Agent half.** `derp.server.install` (compensable; undo from the effects
  journal; lock `derp`; `Available` only when the host is on the tailnet):
  creates system user `darkpanel-derp` (`useradd`, 1.8; owned path kind
  `user`); installs `/usr/local/lib/darkpanel/derper` (kind `file`, mode
  0755); writes `/etc/systemd/system/darkpanel-derper.service` (kind
  `unit`; user `darkpanel-derp`, `AmbientCapabilities=CAP_NET_BIND_SERVICE`,
  `NoNewPrivileges=yes`); creates `/var/lib/darkpanel/derp/` with
  `EnsureServiceDir` (kind `service-dir`, user `darkpanel-derp`, subdir
  `certs`); then reloads, enables and starts the unit through `Env.Units`
  (1.8). Install is long-running (1.3). The DERP spike confirms that `-verify-clients` works
  for a non-root user through tailscaled's socket; if it does not, the unit
  runs as root and the spike records why. `derp.server.uninstall`
  (compensable by `derp.server.install`) removes them; its apply converges
  when run again (an already stopped or absent unit, binary, directory or
  user counts as removed), as forward composites require (1.3).
- **Flags.** `-c /var/lib/darkpanel/derp/derper.key` (0600, written by
  derper itself inside the service directory; never removed by an
  upgrade), `-a :443 -http-port 80 -stun
  -stun-port 3478`, `-verify-clients`, and the certificate mode:
  - `letsencrypt`: `-certmode=letsencrypt -hostname=<name>
    -certdir=/var/lib/darkpanel/derp/certs`, with the hostname's DNS pointing
    at the host; derper renews itself; port 80 serves the ACME challenge;
  - `ip`: `-certmode=manual -hostname=<public IP> -certdir=...`; derper
    creates a self-signed certificate for the address on first start (valid
    one year); clients pin it: the DERP map node gets `HostName: "<ip>"` and
    `CertName: "sha256-raw:<hex SHA-256 of the certificate DER>"`, which
    `derp.server.install` reads back and stores in `derp_servers` with the
    certificate's `NotAfter`. Clients check the expiry of a pinned
    certificate (tailscale `net/tlsdial`, `SetConfigExpectedCertHash`, at
    v1.102.0: the hash comparison is followed by `NotAfter` and `NotBefore`
    checks), and derper generates the self-signed certificate only when its
    files (`<certdir>/<ip>.crt` and `.key`) are absent and reads them only
    at start (`cmd/derper/cert.go` at v1.102.0). Without rotation the
    region therefore stops working one year after install. The DERP page
    and health warn (`degraded`) 30 days before `NotAfter` and report
    `broken` after it. **Rotation** is the composite `derp.cert.rotate`
    (`unwind: reverse`): `cert` (`derp.cert.renew`, agent, compensable,
    lock `derp`: moves the two files in `certs/` to `.prev` names with
    `RenameIn` (1.8), restarts
    derper so it generates a new one-year certificate, reads back the new
    pin; undo moves the `.prev` files back and restarts), then `region`
    (`tailnet.policy.patch`: one `set` of the whole region object at
    `/derpMap/Regions/<id>` with the new node `CertName`, since a path into
    the `Nodes` array is refused, 4.2). Between the
    two steps, clients holding the old pin cannot use the region, so
    rotation has the same precondition as `derp.server.upgrade` (another
    reachable region, below), and its plan states the window.
- **Client verification (R62).** `-verify-clients` checks every client's
  node key against the host's own tailscaled and fails closed when tailscaled
  does not answer; no fail-open flag applies to it. Every client must be
  visible to the DERP host's tailscaled under the tailnet policy, so the
  region step also adds, through `tailnet.policy.patch`, a grant that makes
  all nodes visible to the DERP host's tag (the spike records the minimal
  grant). Turning verification off needs input `verifyClients: false` and
  shows a warning.
- **Ports.** 80/tcp, 443/tcp and 3478/udp; install refuses a host where any
  is already bound (`derp.port_in_use`), for example by Nginx Proxy Manager.
- **Composites** (controller; coordination lock `composite:derp`; no step
  is itself a composite):
  - `derp.server.deploy` (`unwind: reverse`): `tailscaled`
    (`tailscale.package.pin` at the derper version, which rolls itself back
    if it cuts the link, 4.1), `install` (`derp.server.install`), `ports`
    (`firewall.rules.apply` adding the three rules, labelled `derp`; the one
    revertible step, last). It does not publish the region: the owner
    confirms the ports first.
  - `derp.region.set` (`unwind: reverse`, one step: `tailnet.policy.patch`
    on `derpMap` and the visibility grant, the patch computed by the DERP
    module). Refused (`derp.not_ready`) until the server's deploy is
    confirmed and `derp.server.probe` reaches `/derp/probe` and STUN from
    the controller and from at least one agent that is not the DERP host
    (a probe from the DERP host to itself proves nothing); on a fleet with
    no such agent, from the controller alone, with a warning in the plan.
    Region id 900–999, refused if taken
    (`derp.region_taken`); region code and name; the node entry including
    the `ip` mode's pin. `omitDefaultRegions: true` is accepted only when the
    resulting map keeps at least one other reachable custom region, or with
    the typed acknowledgement "nodes will relay only through <region>".
  - `derp.region.remove` (`unwind: reverse`, one step:
    `tailnet.policy.patch`). Removing the last custom region while
    `omitDefaultRegions` is set also clears `omitDefaultRegions`, so nodes
    always keep a relay.
  - `derp.server.upgrade` (`unwind: reverse`): `tailscaled`
    (`tailscale.package.pin` at the new version) and `binary`
    (`derp.binary.update`); each step rolls itself back locally on a failed
    health check, and unwind undoes both. Between the two steps derper and
    tailscaled differ in version, so client verification may refuse
    clients and the region may not relay; the steps run back to back and
    the plan states the window. Because the DERP host's own agent link may
    depend on relaying, the upgrade (and `derp.cert.rotate`) is refused
    with `derp.no_fallback` unless the host has a management path that does
    not use this region: its agent link does not run over the tailnet, or
    the tailnet's DERP map keeps another reachable region (default regions
    not omitted, or another custom region whose probe passes). With
    `omitDefaultRegions` set and this the only region, the owner must first
    add a second region or clear `omitDefaultRegions`.
  - `derp.server.remove` (`unwind: forward`): `region` (`tailnet.policy.patch`
    removing the region, as `derp.region.remove` would), `uninstall`
    (`derp.server.uninstall`), `unpin` (`tailscale.package.unpin`: restores
    the hold state recorded by the deploy's pin, without downgrading
    tailscaled, so the host returns to ordinary updates), then `ports`
    (`firewall.rules.apply` deleting the three rules; revertible, last).
- **Binary update.** `derp.binary.update` (compensable, long-running, lock
  `derp`, module recovery handler 1.3b): stage and verify the new binary,
  journal each phase, keep `derper.prev`, swap with `FileWriter.Rename`
  (`derper` → `derper.prev`, then `derper.new` → `derper`, all
  declared kind `file` paths, 1.8), restart, and
  health-check for up to 60 s (the unit active, TLS `/derp/probe`, STUN, and
  derper's log showing that it reached tailscaled for client verification);
  a failed check or an interrupted swap restores `derper.prev` from the
  journal, locally, without the controller.
- **Probe (R64).** `derp.server.probe` (read): TLS `/derp/probe` and STUN
  from the controller and chosen agents; the local API's DERP-region debug
  endpoint from chosen agents, reported as **connectivity diagnostics**
  only (it uses a throwaway node key and does not relay authenticated
  traffic between two nodes); and per-node visibility through the DERP
  host's `whois`. It distinguishes each node's **preferred** region (status)
  from **observed** use (peers currently connected via the region). No
  Darkpanel check proves an authenticated two-node relay; that is the M5
  live-step demonstration (12): two test-tailnet nodes with direct UDP
  blocked between them, the custom region the only one in the map, and
  `tailscale ping` traffic shown relayed through it.
- Controller table `derp_servers` (host, mode, hostname or address, region
  id, derper version, certificate pin). Web: `/derp`.

### 5. Action catalogue

Classes: R revertible, C compensable (with its undo or compensating
action), I irreversible (typed confirmation), Rd read, X composite with its
unwind policy (`rev` reverse, `fwd` forward). `L` marks a long-running action
(asynchronous apply, 1.3). The task actions of 1.3c
(`tailscale.package.install`, `package.update`, `package.pin`,
`package.unpin`, `derp.server.install`, `derp.binary.update`) are declared
with `DefineTask` and run in
`darkpanel-task-<change id>-<op>-<attempt>.service` (op `apply`, `undo` or
`recover`). Every `ForwardOnly` action and the
action of every non-read step of a forward composite (`derp.server.remove`,
`npm.expose.remove`, `npm.admin.unexpose`, `cloudflare.expose.remove`,
`panel.exposure.remove`; for example `tailnet.policy.patch` (the region
step of `derp.server.remove`, convergent by its edit kinds, 4.2),
`derp.server.uninstall`, `tailscale.package.unpin`, `npm.proxyhost.remove`,
`cloudflare.hostname.remove`, `panel.network.remove` and the
`firewall.rules.apply` that deletes the ports) declares `Converges` (1.2,
1.3): run again with the same input it plans an empty diff when its effect
is already present, and the registry refuses a forward composite whose step
action lacks it. Side: A agent, C controller. Names omit the module
prefix. Composites take `composite:<module>`; children take their own locks.
Scopes follow 1.7 (existing rules kept; raised where noted).

| Module | Action | Class | Side | Locks |
|---|---|---|---|---|
| firewall | `state.enable`, `state.disable`, `state.repair`, `defaults.set`, `logging.set`, `rules.apply`, `rule.add`, `rule.delete`, `snapshot.restore`, `docker.enable`, `docker.disable`, `docker.repair`, `docker.set-sources`, `container.allow`, `container.deny`, `container.remove`, `containers.apply` (each `RepairedBy: state.repair`, 2.5) | R | A | `firewall` |
| firewall | `snapshot.compare` | Rd | A | — |
| docker | the table in 3 (`compose.*` admin and L; `image.pull` L) | — | A | `docker` |
| tailscale | `prefs.set`, `prefs.repair` (each `RepairedBy: prefs.repair`, 4.1) | R | A | `tailscale` |
| tailscale | `node.login` (undo), `package.install` (undo, L), `package.pin` (self-rolling-back, recovery handler, L), `package.unpin` (undo: re-hold, L) | C | A | `tailscale` |
| tailscale | `node.logout`, `package.update` (L) | I | A | `tailscale` |
| tailscale | `cert.get` (`Internal`) | Rd | A | — |
| tailnet | `account.connect` (undo: delete the created row), `device.authorize`, `device.set-routes`, `authkey.create`, `policy.set`, `policy.patch` (undo, 4.2) | C | C | `tailnet` |
| tailnet | `account.disconnect`, `device.expire`, `device.delete` | I | C | `tailnet` |
| tailnet | `device.list`, `policy.get` | Rd | C | — |
| tailnet | `node.join` (install, key, login) | X rev | C | — |
| derp | `server.install` (undo, L), `server.uninstall` (by `server.install`; used only in forward composites and by the operator), `binary.update` (undo, recovery handler, L), `cert.renew` (undo: restore the `.prev` certificate) | C | A | `derp` |
| derp | `server.probe` | Rd | C | — |
| derp | `server.deploy` (tailscaled, install, ports), `server.upgrade` (tailscaled, binary), `cert.rotate` (cert, region), `region.set`, `region.remove` (one `tailnet.policy.patch` step each) | X rev | C | — |
| derp | `server.remove` (region, uninstall, unpin, ports) | X fwd | C | — |
| npm | `instance.deploy` (undo, L), `instance.upgrade` (undo, recovery handler, L), `instance.confirm-public` (undo: clear the mark), `network.attach`, `network.detach` (undo: the opposite, only for the claim this change made; 6), `proxyhost.add`, `proxyhost.update`, `proxyhost.remove`, `accesslist.add`, `accesslist.remove` (undo bound to the id), `certificate.request` (undo: delete the created certificate, L) | C | A | `npm` |
| npm | `instance.remove` (deletes volumes), `certificate.remove` | I | A | `npm` |
| npm | `admin.reveal-password` (admin, secret delivery) | Rd | A | — |
| npm | `instance.deploy-public` (deploy, ports when the integration is on), `expose.container` (network, cert, proxy), `admin.expose` (cert, proxy) | X rev | C | — |
| npm | `expose.remove` (proxy remove, network detach), `admin.unexpose` (proxy remove) | X fwd | C | — |
| cloudflare | `account.connect` (undo: delete the created row), `tunnel.create` (undo), `tunnel.adopt` (undo: restore the stored hash), `hostname.add` (undo only), `hostname.remove` (by `hostname.add` only) | C | C | `cloudflare` |
| cloudflare | `account.disconnect`, `tunnel.delete` | I | C | `cloudflare` |
| cloudflare | `tunnel.list`; `tunnel.token` (`Internal`) | Rd | C | — |
| cloudflare | `connector.run` (undo), `connector.stop` (by `connector.run`), `network.attach`, `network.detach` (claims, as for NPM) | C | A | `cloudflare` |
| cloudflare | `connector.deploy` (tunnel, token, run), `expose.container` (network, hostname) | X rev | C | — |
| cloudflare | `expose.remove` (hostname remove, network detach) | X fwd | C | — |
| panel | `network.create` (undo: remove if unused), `network.remove` (by create) | C | A | `panel` |
| panel | `listener.open` (undo: revoke and close), `listener.revoke` (`BreakGlass`; `ForwardOnly` and `Converges`, 1.2: revocation is monotonic and reopening needs a new `exposure.add`) | C | C | `panel` |
| panel | `exposure.add` (8) | X rev | C | — |
| panel | `exposure.remove` (8) | X fwd | C | — |

Every compensable step of a `rev` composite has an undo handler (`Undoer`);
its read steps have none and its final step may be revertible (1.3). The
wave that builds a
module may add read actions and resources; any other change to this table is
a spec change.

### 6. Exposure: Nginx Proxy Manager (R65–R68) — module `npm`

- **Preconditions.** Docker 28.0 or later with the `raw`-table localhost
  protection loaded (Versions), independent of the firewall integration,
  because port 81 is published on 127.0.0.1; ports 80 and 443 free, and no
  DERP server on the host (`derp.port_in_use`).
- **Deploy (R65).** `npm.instance.deploy` (agent, compensable, undo from the
  effects journal; lock `npm`; input `acmeEmail`, required, an address whose
  domain is a public registrable domain): network `darkpanel-npm`, volumes
  `darkpanel-npm-data` and `darkpanel-npm-letsencrypt`, container
  `darkpanel-npm` from the pinned digest, all labelled
  `darkpanel.module=npm`, with 80 and 443 published on all addresses and 81 on
  127.0.0.1 only.
- **Bootstrap.** Since 2.13 NPM has no default admin, and while no user
  exists `GET /api` reports `setup: false` and `POST /api/users` creates an
  admin without authentication. The image's `INITIAL_ADMIN_*` variables are
  not used (NPM logs that password in plain text). Deploy, before the step
  completes:
  1. requires `setup: false` (a fresh volume);
  2. creates the admin through `POST /api/users` from the agent over
     127.0.0.1:81: email `acmeEmail` (NPM uses the user's email as the ACME
     contact, and Let's Encrypt rejects reserved domains), password 32 random
     bytes, journaled sealed before the request and stored sealed in
     `npm_instances`;
  3. requires `setup: true` and a successful `POST /api/tokens` with those
     credentials.

  A lost response to step 2 cannot be resolved safely (an admin may or may
  not exist with the sealed password), and it does not need to be: the
  admin exists only inside the `darkpanel-npm-data` volume, whose creation
  is already a journaled effect, so the admin request is not an effect of
  its own. Apply fails with `npm.setup_failed`; the network, volume and
  container effects are settled as usual (1.2), so the change ends
  `failed` with them `made` (or `needs_repair` if one of them is itself
  `unresolved`, resolved first as 1.2 describes). The operator sequence
  has nothing specific to NPM: (1) `darkpanel change undo <id>`, which runs
  the inverses newest first and removes the container, both volumes (and
  with them any admin) and the network; (2) a new `npm.instance.deploy`,
  which starts from fresh volumes. Repair never redeploys by itself. The
  password is never
  in the container's environment, the agent's logs or the log stream (logs
  of `darkpanel-npm` are filtered for it).
- **Opening 80 and 443.** NPM's ports are Docker-published bindings.
  `npm.instance.deploy-public` (composite, `unwind: reverse`): `deploy`, then,
  only when the host's firewall integration is enabled and supported,
  `ports` (one `firewall.containers.apply` allowing both bindings of
  `darkpanel-npm`; the revertible step, last, `after: [deploy]`), which the
  user confirms after checking the ports from outside. Without the
  integration the ports are already reachable, the composite has no
  revertible step, and the instance is marked public by
  `npm.instance.confirm-public` (compensable; the owner's explicit
  confirmation, after checking from outside, that 80 and 443 are reachable),
  which the UI offers right after deploy. A **confirmed public instance** is
  one with a confirmed `ports` step or a `confirm-public` mark.
- **Management (R66).** Agent actions call the local API with the stored
  credentials: `npm.proxyhost.add`, `.update`, `.remove`, `npm.accesslist.add`,
  `.remove` (compensable; undo bound to the id and before-state in the
  journal), `npm.certificate.request` (compensable; undo deletes the
  certificate it created; HTTP-01 through NPM's `provider: letsencrypt`),
  `npm.certificate.remove` (irreversible). Read actions list each.
  `npm.certificate.request` sends `meta.letsencrypt_email` = the stored
  `acmeEmail` and `meta.letsencrypt_agree: true` (the 6a spike confirms the
  field names against the pinned image) and is long-running.
- **Recovery identities (1.2).** This section is the authority for NPM;
  1.2 defers to it. NPM assigns ids on creation and has no idempotency key,
  so each create persists, with the effect's intent and before the call,
  the identity recovery looks up (`<seq>` is the effect's sequence number
  in the change's journal, 1.2):

  | Object | Identity | Exclusive because |
  |---|---|---|
  | proxy host | its exact `domain_names` set, plus `created_on` not before the intent's time | plan and apply both check that no proxy host has any of the domains, and NPM refuses a second proxy host for a domain |
  | access list | name `darkpanel-<change id>-<seq>` | the name contains the change id |
  | certificate | `nice_name` `darkpanel-<change id>-<seq>` if the 6a spike shows NPM keeps a given `nice_name` for Let's Encrypt certificates; otherwise its exact `domain_names` set plus `created_on` not before the intent's time | the name (first form); the second form is **not** exclusive |

  **Settlement.** NPM calls are not `Local` (1.2): `CommitBy` is the
  call's start plus 2 minutes (proxy hosts, access lists) or 10 minutes
  (certificates, which wait for the ACME order). An interrupted creation
  therefore becomes `unresolved` (`uncertain`) and nothing settles it
  automatically; the operator resolves it with `change resolve` after
  `CommitBy` (1.2). The module's `Observe` lists the objects of the type
  and reports what the identity finds:

  | Found | `Observe` returns | Resolution |
  |---|---|---|
  | exactly one match by an exclusive identity | `Matches: 1`, the object's id | `reconcile` records it `made`; `accept` keeps it |
  | exactly one match by the non-exclusive certificate identity | `Matches: 1`, the object's id, and the view marks the match "not exclusive" with the candidate shown | the operator decides: `reconcile` records it `made` (the next undo deletes it), `accept` keeps it as is |
  | more than one match | `Matches: N`, every candidate shown | `reconcile` only with `--resource` naming one candidate (`Allowed` includes it, 1.2); otherwise `accept` |
  | none | `Matches: 0`, State `null` | `reconcile` records `skipped`; an object that appears later is shown on the NPM page as "unclaimed Darkpanel object" (its name or domains match a skipped effect) for the operator to remove, never removed automatically |

  Darkpanel never deletes an object it cannot tie to the change by an
  exclusive identity unless the operator has resolved that match with
  `reconcile`. The 6a spike confirms against the pinned image that
  proxy-host domains are enforced unique, that access-list names are kept
  as given, whether a certificate's `nice_name` is kept as given, and that
  `created_on` is returned; its result is recorded in this table.
- **Reserved resources.** Proxy hosts, access lists and certificates
  created by `npm.admin.expose` or `panel.exposure.add` are recorded in
  `npm_reserved` (object id, owning exposure). `npm.proxyhost.add` and
  `.update` refuse (`npm.reserved`) an upstream of `127.0.0.1`, `localhost`
  or the NPM container's own addresses on port 81, and any upstream on the
  `darkpanel-panel` network or the panel gateway address, unless the
  change is a child of `npm.admin.expose` or `panel.exposure.add`
  (`Env.Call.ParentAction`, 1.5, set only by the controller) respectively; `.update`, `.remove`,
  `npm.accesslist.remove` and `npm.certificate.remove` refuse a reserved
  object unless the change is a child of the owning exposure's removal.
  The checks run in the actions' own plan handlers, so no lower-scope path
  around the composites' authorization exists.
- **Attachment claims.** `npm.network.attach` records a claim
  `{network, claimant (exposure or composite id), preexisting}` in
  `npm_attachments`; it attaches only when no claim exists and the
  attachment is not already there (then `preexisting: true`).
  `npm.network.detach` removes its claim and detaches only when no claim is
  left and the attachment was not pre-existing, so removing one exposure
  never breaks another on the same network.
- **Supported targets (6, 7.3).** The proxy reaches the target by its
  container name through Docker's embedded DNS, which exists only on
  user-defined networks (Docker's bridge driver documentation: "On the
  default bridge network, containers can't resolve each other by name").
  A target is supported when it is attached to at least one user-defined
  bridge network; the exposure attaches the proxy to that network (the
  first in name order when there are several, or the one the input names).
  A target only on the default `bridge`, on `host`, `none`, or a
  non-bridge driver is refused in the preview with `npm.unsupported_target`
  (`cloudflare.unsupported_target` for a tunnel exposure) and the reason "connect the container to a user-defined network". The 6a
  spike checks that a container with a Docker-generated name resolves by
  that name on a user-defined network; if it does not, such targets are
  refused the same way.
- **Expose container (R67).** `npm.expose.container` (composite) requires a
  confirmed public instance (`npm.not_public` otherwise). Steps: `network`
  (`npm.network.attach`: attach `darkpanel-npm` to the target's network),
  `cert` (`npm.certificate.request`), `proxy` (`npm.proxyhost.add`
  forwarding to `<container>:<port>` with forced SSL and HSTS), `unwind:
  reverse`. No revertible step. `npm.expose.remove` (`unwind: forward`):
  `proxy` remove, then `network` detach (claim-based).
- **Admin UI (D2).** `npm.admin.expose` (composite, off by default; requires
  a confirmed public instance): `cert` (`npm.certificate.request` for the
  owner's hostname), `proxy` (`npm.proxyhost.add` for that hostname
  forwarding to `127.0.0.1:81` inside the NPM container, forced SSL and HSTS,
  optional access list). The host's NPM page and every Darkpanel page show a
  warning banner while it exists. `npm.admin.unexpose` (`unwind: forward`)
  removes the proxy host; the certificate stays, and deleting it is the
  separate, explicitly confirmed `npm.certificate.remove`.
  `npm.admin.reveal-password` returns the admin password through secret
  delivery (1.14).
- **Client header (1.10, 8).** NPM inserts a proxy host's advanced
  configuration at `server` scope, while its generated `location /` sets
  its own `proxy_set_header` lines, and nginx does not inherit server-level
  `proxy_set_header` into a location that defines any. Darkpanel therefore
  puts its header line inside the effective location, through NPM's own
  mechanism, verified in NPM's v2.13.5 source:
  - The proxy host gets a custom `locations` entry with path `/`; NPM then
    sets `use_default_location` to false (`backend/internal/nginx.js`), so
    no generated `location /` duplicates it.
  - The custom location is rendered from `backend/templates/_location.conf`,
    which already emits `proxy_set_header` for `Host`,
    `X-Forwarded-Scheme`, `X-Forwarded-Proto` (`$scheme`),
    `X-Forwarded-For` and `X-Real-IP`, the `proxy_pass`, and (with
    websockets on) `Upgrade`, `Connection` and `proxy_http_version 1.1`. The
    location's `advanced_config` is rendered as the first line inside that
    block.
  - Darkpanel's `advanced_config` therefore adds only what the template
    lacks: `proxy_set_header X-Darkpanel-Client $realip_remote_addr;` and,
    for the panel, `proxy_read_timeout 3600s;`. It never repeats a header
    the template sets, so no request carries two `Host` headers (which Go's
    server rejects). A client-sent `X-Darkpanel-Client` is replaced, because
    a `proxy_set_header` for a field redefines it.

  The 6a spike renders the configuration from the pinned digest and checks
  the result against this list (a template change in a later image fails
  the spike, and the list is updated here); the harness sends an ordinary
  request that must succeed with exactly one `Host` header, and requests
  with a missing and a forged `X-Darkpanel-Client`, which must arrive
  overwritten.
- **Upgrade (R68).** `npm.instance.upgrade` (agent, compensable, lock `npm`)
  is a durable transaction in `npm_upgrades` (phase per step, fsynced), with
  this recovery table:

  | Interrupted in phase | Recovery |
  |---|---|
  | `recorded` (configuration and digest journaled) | start the original container if it is stopped |
  | `stopped` | start the original container |
  | `archiving` | delete the partial archive; start the original container |
  | `archived` (archive written and its checksum verified) | start the original container |
  | `recreating`, `waiting` | remove the new container; restore the volumes from the verified archive; recreate the original from its journaled configuration |
  | `done` | none |

  These rows are the action's module recovery handler (1.3b); the upgrade
  is long-running (1.3). The new container waits up to 120 s for its API. An archive referenced by
  a non-terminal transaction is never pruned; otherwise the last three
  archives are kept, in the managed directory
  `/var/lib/darkpanel/agent/npm-backups/` (1.8).
- **Web.** `/hosts/:id/npm` (instance, proxy hosts, access lists,
  certificates, admin exposure), and Expose in the container action slot.

### 7. Exposure: Cloudflare Tunnel (R69–R71) — module `cloudflare`

#### 7.1 Account and tunnels (R69, R70)

- **Account.** `cloudflare.account.connect` (controller, compensable; undo, after 1.9's cleanup check:
  delete the credentials row this change created): account id and API token
  (sealed). Token verification shows only that the token is valid, so connect
  probes read capabilities (list tunnels on the account, list zones) and
  reports write capabilities as unverified (1.9). Required permissions:
  "Account: Cloudflare Tunnel: Edit" ("Cloudflare Tunnel Write") on the
  account, "Zone: Zone: Read" ("Zone Read") and "Zone: DNS: Edit" ("DNS
  Write") on the zones used. The 3c contract check settled this; the final
  list is recorded below and belongs in the connect dialog's help text.
  `cloudflare.account.disconnect` is irreversible.
- **Recorded contract (Cloudflare, checked 2026-10-30).**
  - **Sources:** https://raw.githubusercontent.com/cloudflare/api-schemas/main/openapi.json
    (Cloudflare's OpenAPI document; `x-api-token-group` lists the accepted
    permissions per operation),
    https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/get-started/create-remote-tunnel-api/,
    https://developers.cloudflare.com/fundamentals/api/get-started/account-owned-tokens/
    and https://developers.cloudflare.com/api/resources/accounts/subresources/tokens/methods/verify/.
    Paths are relative to `https://api.cloudflare.com/client/v4`; `{a}` is the
    account id. An item the sources do not state is written `not documented`.
  - **Permissions:** token verify: none listed. Tunnel list, get,
    connections list, configuration get: Cloudflare Tunnel Read or Write. Tunnel
    create, delete, tunnel token, configuration put: Cloudflare Tunnel Write.
    Zones list: Zone Read (the document's group is named "Zone Zone Read").
    DNS records list and get: DNS Read or DNS Write; create and delete: DNS
    Write. Cloudflare One Connectors and Cloudflare One Connector:
    cloudflared (Read or Write) are accepted wherever Cloudflare Tunnel is
    listed. Final set to ask for: Account / Cloudflare Tunnel / Edit
    (account), Zone / Zone / Read and Zone / DNS / Edit (the zones used).
    Creating a tunnel and reading its token need nothing beyond Cloudflare
    Tunnel Write: no operation lists Account Settings: Read or any other group.
  - **Token verify:** `GET /accounts/{a}/tokens/verify` for an account-owned
    token (no listed permission); the user-token endpoint is `GET
    /user/tokens/verify` with no account id. The sources do not say which
    applies to a given token; the account endpoint is the one for a token
    given with an account id. 200 `result`: `id`, `status` (`active`,
    `disabled` or `expired`), `expires_on` and `not_before` (date-time,
    optional). Invalid token: a 4XX with `success` false, `errors[]` and
    `result` null; the exact status and code: not documented.
  - **Envelope:** `success`, `errors[]` and `messages[]` (each `code`, an
    integer of at least 1000, `message`, optional `documentation_url` and
    `source.pointer`), `result`, `result_info` (`count`, `page`, `per_page`,
    `total_count`; zones add `total_pages`). Pagination by `page` (default 1)
    and `per_page`: tunnels 1 to 1000, default not documented; zones default
    20, 5 to 50; DNS records default 100, maximum 5000000.
  - **Tunnels:** list `GET /accounts/{a}/cfd_tunnel?is_deleted=false[&name=]`
    (without `is_deleted` deleted tunnels are included); get `GET
    .../cfd_tunnel/{id}`; create `POST .../cfd_tunnel` with `name` (required),
    `config_src` (`cloudflare` or `local`, default `local`) and `tunnel_secret`
    (optional, described for locally managed tunnels; the tutorial omits it);
    its answer also carries the tunnel `token`. Delete `DELETE
    .../cfd_tunnel/{id}`: "must have no active connections"; `DELETE
    .../{id}/connections` cleans connections up (Tunnel Write); the answer for
    a tunnel with connections: not documented. Object: `id`, `name`, `status`
    (`inactive` never run, `degraded`, `healthy`, `down`), `created_at`,
    `deleted_at` (null when live), `connections[]`, `conns_active_at`,
    `conns_inactive_at`, `config_src`, `remote_config`, `account_tag`,
    `tun_type`, `metadata`.
  - **Tunnel token:** `GET .../cfd_tunnel/{id}/token` (Tunnel Write; Read does
    not authorize it): `result` is a string.
  - **Connections:** `GET .../cfd_tunnel/{id}/connections`: `result` is an
    array of connectors (`id`, `version`, `arch`, `run_at`, `config_version`,
    `features`) each with `conns[]`; every `conns` element has `id`, `uuid`,
    `client_id`, `client_version`, `colo_name`, `origin_ip`, `opened_at` (date-time),
    `is_pending_reconnect`. The tunnel object's `connections[]` holds the same
    members, flat.
  - **Configuration:** `GET` and `PUT .../cfd_tunnel/{id}/configurations`; the
    `PUT` body is `{"config":{"ingress":[...]}}` (also `originRequest` and
    `warp-routing.enabled` under `config`); `result`: `tunnel_id`, `version`
    (integer), `config`, `source` (`local` or `cloudflare`), `created_at`
    (`account_id` on the `PUT` answer). Rule: `hostname`, `path`, `service`
    (required: `http://`, `https://`, `unix://`, `tcp://`, `ssh://`, `rdp://`,
    `unix+tls://`, `smb://` or `http_status:<code>`), `originRequest` with
    `noTLSVerify` among others. At least one rule is required and the
    tutorial requires a catch-all rule last (`{"service":"http_status:404"}`);
    the document marks `hostname` required in the `PUT` schema, which that
    catch-all example contradicts. A never-configured tunnel's `GET`, and the
    validation error codes: not documented.
  - **Zones:** `GET /zones?name=<name>&account.id=<id>` (Zone Read); `name`
    also takes operators (`contains`, `starts_with` and others); `result[]`
    holds `id` and `name` among other members; pagination above.
  - **DNS records:** list `GET /zones/{z}/dns_records?name=<name>&comment=<text>`
    (`name` and `comment` are exact, case-insensitive aliases of `name.exact`
    and `comment.exact`; `type`, `content` and `proxied` also filter); get `GET
    .../{id}`; create `POST` with `type`, `name`, `content`, `proxied` (default
    false), `comment`, `ttl` (default 1, automatic); delete `DELETE .../{id}`
    answers 200 `result` `{"id"}`. A record has `id`, `type`, `name`,
    `content`, `proxied`, `comment`, `ttl`, `created_on`, `modified_on`. A CNAME
    `content` must not equal its name; A/AAAA and CNAME cannot share a name.
    Error codes for a duplicate and for an unknown record: not documented.
  - **Errors:** each failure is the envelope with `success` false, `errors[]`
    and `result` null, in a 4XX. The status and first `errors[].code` for an
    invalid token, a missing permission, a missing object, a rate limit and a
    conflicting record: not documented in the sources read.
  - **Divergences:** none
- **Tunnels.** `cloudflare.tunnel.create` (compensable; undo: delete the
  created tunnel; name `darkpanel-<change id>`) creates a remotely managed
  tunnel with `POST /accounts/{a}/cfd_tunnel` and `config_src: cloudflare`;
  its token is read from `GET .../cfd_tunnel/{id}/token`.
  `cloudflare.tunnel.delete` is irreversible (typed name) and refuses while
  hostnames or connectors exist. `cloudflare.tunnel.list` (read) shows
  connector status from `GET .../cfd_tunnel/{id}` (`status`) and
  `.../connections`.
- **Exclusive management.** Cloudflare's tunnel configuration has no
  conflict protection: `PUT .../configurations` replaces the whole
  configuration and carries no version. Darkpanel therefore manages a tunnel
  it created exclusively; editing its configuration in the dashboard or with
  another client is unsupported. Darkpanel stores a hash of the configuration
  it last wrote; a different configuration found before a write is reported
  as drift (`cloud.conflict`) and nothing is written until the owner accepts
  the current configuration with `cloudflare.tunnel.adopt` (compensable;
  re-reads the configuration and stores its hash; undo restores the previous
  hash). Two limits are stated plainly, because the provider offers no
  conflict protection: a write by another client between Darkpanel's read
  and its `PUT` is **overwritten without trace** (the re-read after the
  `PUT`, 1.9, sees only Darkpanel's own configuration); only a write made
  after Darkpanel's last write is detectable, as drift before the next one.
  Exclusive management is therefore a requirement on the operator, not a
  guarantee Darkpanel can enforce.

#### 7.2 Connector (R70)

- `cloudflare.connector.run` (agent, compensable, undo from the journal;
  lock `cloudflare`): container `darkpanel-cloudflared-<tunnel>` from the
  pinned digest, labelled `darkpanel.module=cloudflare`, restart policy
  `unless-stopped`, running `tunnel --no-autoupdate run --token-file
  /run/darkpanel/token`. The token is sealed in the agent database and
  written to `/var/lib/darkpanel/agent/cloudflare-tokens/<tunnel id>.token`
  in that managed directory (1.8; root-owned 0700, flat names): the file is
  owned by UID 65532 (the image's user) with mode 0400 and bind-mounted
  read-only at `/run/darkpanel/token` (dockerd resolves the bind source as
  root, so the directory need not be traversable by UID 65532). The token is never in the
  container's environment or command line. The harness checks that the real
  image reads it and that inspect and logs never show it.
- `cloudflare.connector.deploy` (composite, `unwind: reverse`): `tunnel`
  (optional `cloudflare.tunnel.create`), `token` (`cloudflare.tunnel.token`,
  an `Internal` read whose output is sealed in the step row), `run`.

#### 7.3 Public hostnames (R71)

- `cloudflare.hostname.add` and `cloudflare.hostname.remove` (controller,
  compensable, lock `cloudflare`; `hostname.add` declares an `Undo` and no
  `CompensatedBy`, `hostname.remove` declares `CompensatedBy:
  cloudflare.hostname.add` and no `Undo`, so each has exactly one
  compensation mechanism, 1.2): durable
  two-step operations in `cloudflare_hostnames` (hostname, tunnel, service,
  DNS record id, phase). Add checks drift (7.1), reads the configuration,
  inserts the rule before the catch-all (a catch-all `http_status:404` is
  required last and added if absent), writes it, re-reads it to confirm, then
  creates the proxied CNAME to `<tunnel id>.cfargotunnel.com` with the comment
  `darkpanel:<change id>` and records its id. Remove deletes only the
  recorded record id, after checking that the record still has that content
  and comment, and only the rule it added. These are **read-then-write
  checks, not compare-and-swap**: Cloudflare's DNS record delete takes only
  the record id and no version or condition, and the tunnel configuration
  `PUT` carries none either, so a change another client makes between
  Darkpanel's read and its delete or `PUT` is not detected and may be
  overwritten or removed. The window is one API round trip; the plan and
  the documentation state it, and exclusive management (7.1) is the
  operator's side of the contract. A **reserved hostname** is one whose
  service is the panel gateway (8): `cloudflare.hostname.add` refuses
  (`cloudflare.reserved`) a service naming the panel gateway address unless
  the change is a child of `panel.exposure.add` (`Env.Call.ParentAction`, 1.5), and
  `cloudflare.hostname.remove` refuses a reserved hostname unless it is a
  child of `panel.exposure.remove`. Controller startup recovery
  finishes or undoes an operation found mid-phase through the actions'
  module recovery handler (1.3b). Unrelated ingress rules are carried over
  unchanged. `hostname.add` has an `Undo` (remove the record and rule it
  created, after the post-state checks); `hostname.remove` is compensated by
  `hostname.add` and used only in forward composites and by the operator;
  its apply converges when run again (an ingress rule or DNS record
  already absent counts as removed).
- `cloudflare.expose.container` (composite): `network`
  (`cloudflare.network.attach`, attaching the owned cloudflared container to
  the target's network, under the supported-target rule of 6) and
  `hostname` (service `http://<container>:<port>`).
  `unwind: reverse`. No inbound port opens. `cloudflare.expose.remove`
  (`unwind: forward`): `hostname` remove, then `network` detach.
  `cloudflare.network.attach` and `detach` use attachment claims in
  `cloudflare_attachments`, as for NPM (6).
- Controller tables `cloudflare_accounts`, `cloudflare_tunnels`,
  `cloudflare_hostnames`. Web: `/cloudflare`, and Expose via tunnel in the
  container action slot.

### 8. Exposure: the panel itself (R72) — module `panel`

- **Expose.** `panel.exposure.add` (controller composite, `unwind:
  reverse`, `MinScope: admin`), input
  `via: npm|tunnel`, the public hostname, the controller's host (the only
  host allowed: `panel.wrong_host`), and for `via: tunnel` the `tunnel`
  (required; refused with `cloudflare.connector_missing` unless a
  `darkpanel-cloudflared-<tunnel>` container runs on that host, since a host
  can run several connectors). Refused with `panel.totp_incomplete`
  while any admin is pending TOTP, with `panel.already_exposed` when an
  exposure exists, and for `via: npm` with `npm.not_public` unless a public
  NPM instance is confirmed on that host.
- **Steps.**
  1. `network` (`panel.network.create`): network `darkpanel-panel` labelled
     `darkpanel.module=panel`, bridge name `dp-panel`
     (`com.docker.network.bridge.name`), a free /29 chosen at plan time from
     172.31.255.0/24 downwards, gateway `.1`, proxy address `.2`.
  2. `attach`: attach the proxy container (NPM or cloudflared) with the
     static address `.2` (`npm.network.attach` or
     `cloudflare.network.attach` with a fixed address).
  3. `listen` (`panel.listener.open`): the runtime listener on
     `<gateway>:9443` (setting `exposurePort`, default 9443), allowed peer
     `.2`, client header per 1.10.
  4. `cert` (NPM only, `npm.certificate.request` for the hostname).
  5. `proxy`: the NPM proxy host (upstream `https://<gateway>:9443`, forced
     SSL, HSTS, certificate verification of the internal hop off, and the
     header policy and `proxy_read_timeout 3600s` placed in the effective
     `location /` as section 6 describes) or the tunnel hostname (service
     `https://<gateway>:9443`, `noTLSVerify`).
  6. `ports`: `firewall.rules.apply` adding `allow in on dp-panel from <proxy>
     to <gateway> port 9443 proto tcp`; the one revertible step, last.
- **Guardrails.** Login over the exposure requires TOTP as everywhere; every
  page shows a banner "Panel exposed via <npm|tunnel> at <host>" with a link
  to remove it; the audit log marks proxied requests and keeps both
  addresses (1.10). Harness tests send spoofed `X-Forwarded-For`,
  `X-Real-IP`, `X-Darkpanel-Client` and forwarded-protocol headers from a
  public source, a private source and an attached container, and expect the
  recorded client address to be the true one and plain-HTTP requests to be
  refused.
- **Remove.** `panel.exposure.remove` (composite, `unwind: forward`):
  `revoke` (`panel.listener.revoke`: commits the durable `revoked` state,
  which every request and stream checks, then closes the listener and its
  tracked connections after the response, 1.10), then `proxy` remove (NPM
  proxy host or tunnel hostname), `attach` detach (claim-based), `network`
  remove (`panel.network.remove`, compensated by `panel.network.create`;
  its apply converges when run again: an absent network counts as
  removed), and `ports` (`firewall.rules.apply` deleting the rule; revertible,
  last). Revocation is monotonic: no failure, timeout, revert of the
  `ports` step or restart reopens the listener (the `ports` step's revert
  restores only the firewall rule, behind a revoked listener); reopening
  needs a new `panel.exposure.add`. A step that keeps failing, or a revert
  of the `ports` step by its timer, leaves the composite `needs_repair`
  (1.3) with the panel already unreachable through the proxy, and repair
  re-plans and re-applies forward.
  **Retiring the removed path.** A removal requested through the exposure
  itself carries a guard context (1.5) whose `Via` is the path being
  removed; after `revoke` that path no longer exists, so the final `ports`
  step's lockout guard would refuse it. Once the `revoke` step has
  committed, the composite records the exposure as **retired** in its step
  log, and its later children get it in `Env.Call.Retired` (1.5), so their guards ignore protected
  flow 3 for that exposure only (the parent authorizes this; no other
  change can retire a path). Every other protected flow is still checked.
  The harness removes the exposure through the exposure itself and expects
  the removal to complete. Break-glass: `sudo darkpanel panel listener
  revoke --local` over the controller socket runs `panel.listener.revoke`
  alone (`BreakGlass`); the operator then runs `panel.exposure.remove`,
  whose `revoke` step plans an empty diff (the listener is already
  revoked; the action is `ForwardOnly` and `Converges`, 1.3) and whose
  later steps complete the removal. An interrupted break-glass revoke is
  completed by running the action again (it has `Converges`).
  `panel.not_exposed` refuses a removal when no exposure exists.

### 9. Hardening (R73–R79)

- **Threat model (R73).** `docs/security/threat-model.md`: assets, actors,
  trust boundaries (browser–controller, proxy–controller,
  controller–agent, agent–host, controller–cloud APIs) and, per area
  (authentication, agent protocol, command execution, Compose execution,
  secret handling and secret delivery, firewall, cloud credentials,
  exposure, the DERP host's pinned tailscaled), threats, mitigations and
  residual risk.
- **Security review (R73).** The owner's two-reviewer panel
  (`dforge:judge-fable` and Codex `gpt-6-astra`, one shared brief).
  Findings go into `docs/dforge/registers/<date>-v1-security-review.md`;
  every critical and high finding is fixed and re-reviewed; medium and low
  are fixed or deferred by the owner.
- **Fuzzing (R74).** Wave 9a starts by writing `docs/security/parsers.md`, an
  inventory of every parser of host-tool output, config files and untrusted
  input in the repository, each mapped to its fuzz target. Known members: UFW
  tuple lines, `ufw.conf`, `/etc/default/ufw`, `*-save` output, `iptables
  --version`, `/proc/net/*`, netlink route messages, os-release, cpuinfo,
  meminfo, loadavg, uptime, marked blocks, HuJSON patching, the Compose
  config JSON, the nmap parser in `test/harness/nmap.go`, the release and
  derper manifests and checksum parsers, the backup archive header and
  frames, the enrollment bundle. `scripts/fuzz.sh` (run by `make fuzz` and by
  `.github/workflows/fuzz.yml`) runs each target for 30 s on every push and
  10 minutes nightly; a test fails when a parser in the inventory has no
  target.
- **Upgrade matrix (R75, S5).**
  - Supported upgrade graph: within a major version, from any earlier
    release; agents one minor behind the controller. The updater's refusal of
    cross-major updates stays. Pre-1.0 builds (0.x) are not upgradable to
    1.0; the release notes say so, and the owner's production host is
    reinstalled from 1.0.0-rc.1 (section 12).
  - Harness `TestUpgradeMatrix`, with test keys: a baseline built from the
    commit the wave starts from, labelled `1.0.0`, and the build under test
    labelled `1.1.0`, exercise the upgrade path and an agent one minor behind
    on all four OS; a deliberately broken `1.1.1` checks rollback within 2
    minutes. Once real `v1.*` tags exist, each is added as a source.
    Pre-release ordering (`-rc.N`) is already handled by the updater and gets
    a unit test.
- **Footprint (R76, S6).** Harness `TestFootprint`.
  - Agent: the real agent on an enrolled VM, idle with every module enabled
    and background tasks running; RSS sampled every 10 s for 10 minutes after
    5 minutes of warm-up; the maximum must be at most 40 MB.
  - Controller: 10 enrolled agents. The nine extra agents are harness-only
    processes built with a `harness` build tag that adds `--state-dir`, which
    moves the database, keys, TLS credentials, sockets, lock file and update
    directory under one directory; each runs with only `hostinfo` enabled and
    no background tasks, as its own enrolled host. The controller's RSS is
    sampled the same way while the host list is requested every 15 s; the
    maximum must be at most 150 MB.
- **Accessibility (R77).** `docs/accessibility.md` lists the core flows
  (login with TOTP, the first-admin and lockout recovery documentation pages,
  host list, a firewall change with confirm and revert, container actions,
  expose a container) against WCAG 2.1 AA criteria, each marked automated or
  manual. Automated: `vitest-axe` on every page of those flows,
  keyboard-only tests (focus order, focus visible, dialogs trap and return
  focus). Manual, in a browser through Playwright with screenshots kept as
  evidence: contrast in both themes, 200% zoom and 320 px reflow,
  screen-reader labels on the change dialog.
- **Documentation (R78, R79).** `docs/user/` (install, first admin, every
  module, lockout recovery, uninstall), `docs/dev/module-guide.md`, and
  `docs/api/` generated from `/api/v1/openapi.json` by `make api-docs`, with
  a CI check that the committed copy is current.

### 10. Release (R80, R81)

- **Backup format.** `darkpanel controller backup --out <file>` (over the
  controller socket). The file is:
  - a header: magic `DPBK` (4 bytes), version `1` (1 byte), argon2id `t`
    (uint32 big-endian, 1–10), `m` in KiB (uint32 BE, 64 MiB–1 GiB), `p`
    (uint8, 1–16), a 16-byte salt, a 16-byte nonce prefix;
  - then frames: a uint32 BE ciphertext length followed by the ciphertext
    of one plaintext chunk of at most 64 KiB, sealed with XChaCha20-Poly1305
    under the derived 32-byte key, nonce `prefix || uint64 BE chunk index`,
    associated data `header || 0x00` for every chunk but the last and
    `header || 0x01` for the last; at most 2^32 chunks. A file whose last
    frame does not authenticate with `0x01` is truncated.

  Defaults t=3, m=64 MiB, p=4. The plaintext is a tar of exactly two regular
  files, `controller.db` (an SQLite online backup) and `controller.key`; no
  other member, path, link or device is accepted. Committed test vectors
  cover a short and a multi-chunk archive. The passphrase is read from the
  terminal twice, or from `--passphrase-file` (root-owned, 0600); at most one
  backup runs at a time. Memory stays bounded whatever the database size.
- **Restore.** `darkpanel controller restore --in <file>`, as root, with both
  services stopped (refused otherwise), on a host after
  `system install --role controller`, under an exclusive lock file
  `/run/darkpanel-restore.lock`. It is refused, with "start the controller
  or run `darkpanel controller restore --recover` first", while any
  `restore-*.journal` exists. Every restore has a transaction id `<tx>`
  (a ULID); every file it creates carries it, so recovery acts only on its
  own transaction's files and never on a previous restore's. The restore
  command writes all staging files itself (root, temporary file, fsync,
  rename, directory fsync), not through the agent's file provider; the
  staging directories are declared in the install layout for uninstall only.
  Phases, journaled in `/var/lib/darkpanel/controller/restore-<tx>.journal`
  (each journal write fsynced with its directory):
  1. `staged`: decrypt and authenticate the whole file into
     `controller.db.restore-<tx>` beside the database and
     `controller.key.restore-<tx>` beside the key (each on its destination's
     own filesystem); refuse on any authentication failure or truncation;
  2. `validated`: the database passes `PRAGMA integrity_check`, its schema
     is not newer than the binary's, and the key opens a sealed canary value
     in it;
  3. `quarantined`: the **staged** database is migrated to the binary's
     schema and marked for quarantine (below) in one SQLite transaction,
     opened with `journal_mode=DELETE` so no `-wal` file is left beside the
     staged file, then closed and fsynced. The database is therefore
     quarantined before any original is touched, and no later phase or
     recovery step writes to a database;
  4. `journaled`: the journal records, for each of `controller.db`,
     `controller.db-wal`, `controller.db-shm` and `controller.key`, whether
     it exists now (original presence);
  5. `moved-aside`: each existing original is renamed to `<name>.pre-<tx>`,
     one at a time;
  6. `installed`: each staged file is renamed into place with owner and mode
     (`darkpanel`, 0600), one at a time;
  7. `verified`: the installed `controller.db` is opened read-only and must
     carry `restore_quarantine` with this `<tx>`, have no non-terminal
     record outside state `quarantined`, and no lock row of a kind phase 3
     removes; otherwise the journal stays and the controller does not
     start;
  8. `done`: the journal is removed; `.pre-<tx>` files are kept until the
     next successful restore or a manual cleanup command removes them.

  **Rename intent.** Every rename in phases 5 and 6, and every rename
  recovery makes, is journaled as `{phase, from, to, state: intended}`
  (fsync) **before** it runs and marked `done` after the rename and the
  directory fsync. An `intended` entry is settled by which names exist
  (both names carry `<tx>` or are the fixed originals, and the services are
  stopped, so nothing else creates them): `from` present and `to` absent →
  not done; `to` present and `from` absent → done; both or neither present
  → recovery stops, keeps the journal, and the controller does not start
  (the unit fails with the journal path and the two names in the log, for
  the runbook's manual procedure).

  **Recovery.** The controller unit gains
  `ExecStartPre=+{{.Binary}} controller restore --recover` (the `+` runs it
  as root; `{{.Binary}}` is the swapped binary the units run,
  `internal/install/units/`), so the controller never starts on an
  unfinished restore. `--recover` touches only the controller's files, under
  the restore lock, and is the one restore operation exempt from the
  stopped-services check: the agent does not read those files, so a
  controller restarting under a running agent recovers safely. With no
  journal it exits at once. It processes journals oldest first by `<tx>`;
  for each it completes the transaction when phase 6 has started (renaming
  the remaining staged files, which phase 3 already quarantined) or rolls
  it back otherwise (renaming back exactly the `.pre-<tx>` files its
  journal recorded, and removing originals that did not exist before),
  acting only on files named with that journal's `<tx>`. A completed
  transaction then runs phase 7's read-only verification before its
  journal is removed; a failure keeps the journal and fails the unit, so
  the controller never starts on an unquarantined restored database.
  Recovery is idempotent; it does only renames, removals and one
  read-only open, well inside the units' 30 s `TimeoutStartSec`.
- **Restore quarantine (disaster restore mode).** A backup captures the
  controller as it was, including non-terminal records that are history,
  not instructions. A restored database therefore starts in quarantine:
  - non-terminal composites, single changes, cloud operations (Cloudflare
    hostname phases, uncertain cloud effects and auth-key sweeps) and update
    rollouts are marked `quarantined` and are neither replayed, resumed nor
    unwound;
  - every runtime listener is restored `closed` and every exposure
    `needs_repair` (its proxy container, bridge and gateway belong to the old
    host);
  - **every automatic mutation source is suspended**: composite recovery and
    the child watcher, module recovery handlers on the controller, the
    update scheduler and rollout driver (started today by
    `internal/controller/server/services.go`), the services registered
    through the service hook (1.12: auth-key sweeps, the Cloudflare hostname
    driver); one quarantine flag in `core_meta` is checked by each before it
    starts;
  - **admitted**: logins, sessions and tokens for reads; agent sessions,
    `hello`, audit ingestion and read-only lookups; and on the controller
    socket only, as root: `darkpanel controller quarantine list`,
    `quarantine resolve <record> done|never|repair` (reconciles one record
    against the agents' executing-side state through the read-only lookup
    and against the providers), `quarantine enroll-token <host>` (a
    single-use enrollment token, valid one hour, for a host already in the
    restored database: the only token issuance allowed) and
    `quarantine release`;
  - user and token mutations and every action apply are refused with
    `core.quarantined`.

  **Marking** (phase 3, one transaction on the staged database): set
  `core_meta.restore_quarantine` to `{"tx", "restoredAt",
  "backupCreatedAt"}`; set every non-terminal change, composite, cloud
  operation and rollout to `quarantined`, keeping its prior state in
  `quarantined_from`; clear every `core_changes.recovery_owner` (the
  owners ran in the old process; a record resolved to `needs_repair` starts
  unowned and is claimed afresh, 1.3) and delete the controller database's
  `core_task_runs` rows, if any (task units run only on agents, whose
  databases restore does not touch); set every runtime listener `closed`
  and every exposure `needs_repair`; and dispose of `core_locks` rows:
  - a row owned by an update transaction (an owner that is not a change
    id: ADR 0007's update ids) is **deleted**: that transaction ran on the
    old host, and this host's own update records live under `UpdateDir`,
    which restore does not touch;
  - a row owned by a now-quarantined record is **kept**, owned by that
    record, so nothing new can take its keys until the record is resolved
    (a new change on those keys fails with `lock.held` naming the record);
  - agents' own locks live in their databases, which restore does not
    touch; nothing on an agent is assumed stopped.

  **Resolve outcomes** (`quarantine resolve <record> done|never|repair`),
  each in one transaction with the lock rows:
  - `done`: the record's work is reconciled as complete (the agents'
    read-only lookup, 1.3, shows the executing side terminal and applied,
    or the provider shows the object); the record takes the terminal state
    it would have reached, and its locks are deleted;
  - `never`: reconciled as not applied (the lookup shows `absent` or
    terminal and not applied, and no provider object carries its identity);
    the record becomes `abandoned` with reason "restore: never applied",
    and its locks are deleted;
  - `repair`: the record becomes `needs_repair` with its locks kept, and is
    repaired with `darkpanel change repair` (1.3) after release;
  - `done` and `never` are refused while any lookup answers `running` or
    `recovering`, or an agent the record names is not connected: Darkpanel
    never infers that executing work has stopped.

  `darkpanel controller quarantine release` ends quarantine: it is refused
  while records are unresolved unless `--keep-unresolved`, which leaves
  those records `quarantined` permanently with their locks (terminal:
  never run, shown by `darkpanel change list --state quarantined`, still
  resolvable one by one later); release prints every lock key that stays
  held and by which record. Release clears the flag, then the suspended
  sources start.

  **Routes** (socket surface, root only, through `backup.Contribute`,
  1.12; problems are RFC 9457 as elsewhere):
  - `POST /api/v1/controller/backup`, request `{"passphrase": string,
    "argon2": {"t", "m", "p"}?}`, answer `200` with
    `application/octet-stream` (the file of the format above, streamed);
    `409 core.change_busy` while another backup runs;
  - `GET /api/v1/controller/quarantine`, answer `{"active": bool, "tx",
    "restoredAt", "records": [{"id", "kind": "change" | "composite" |
    "cloud" | "rollout", "action", "hostId", "quarantinedFrom", "locks":
    [string]}]}`;
  - `POST /api/v1/controller/quarantine/{record}/resolve`, request
    `{"outcome": "done" | "never" | "repair"}`, answer the record as in the
    list; `409 core.change_busy` when a lookup shows work running;
  - `POST /api/v1/controller/quarantine/enroll-token`, request
    `{"hostId"}`, answer `{"token", "expiresAt"}` (shown once);
  - `POST /api/v1/controller/quarantine/release`, request
    `{"keepUnresolved": bool}`, answer `{"heldLocks": [{"key", "record"}]}`.

  Restore itself runs only as the root CLI with the services stopped and
  has no route.
- Tests cover wrong passphrase, truncation, tampering, a large database
  within the memory bound, a crash at each phase and on each side of every
  rename and fsync (recovery's own renames included), two restores in a row
  with a crash in the second, a restore refused while a journal exists, a
  crash during recovery, a crash immediately before and during phase 3 (the
  originals untouched, the staged database either unmarked and discarded
  by rollback or fully marked), a controller started after a crash in
  phase 6 (recovery completes the renames and verification passes), a
  hand-made installed database without the quarantine flag (verification
  fails and the controller does not start), lock disposition (an update
  reservation removed, a quarantined change's lock kept and released by
  `resolve done`), a controller restart with the agent running, and a
  backup taken mid-composite restored onto a fresh VM (the composite stays
  quarantined, nothing is unwound and the update scheduler does not run);
  the harness restores a controller backup onto a fresh VM and re-enrolls
  an agent with a quarantine enrollment token. Wave 7 edits the unit
  templates in `internal/install`.
- **Runbook (R81).** `docs/runbook.md`: lockout (firewall break-glass,
  Tailscale prefs revert, panel listener revoke), failed update (rollback,
  recover), lost controller (restore from backup, re-enroll agents).
- **Release candidate.** `CHANGELOG.md` and `docs/versioning.md` updated
  (0.x → 1.0 is a reinstall); `make release-check` green; the commit ready to
  tag `v1.0.0-rc.1`. Tagging is the owner's (section 12).

### 11. Error codes, actors and dependencies

- **Error codes** (wave 0 adds all): `firewall.manual_required`,
  `firewall.unsupported_integration`, `firewall.unsupported_configuration`,
  `firewall.prediction_mismatch`, `firewall.docker_user_in_use`,
  `docker.engine_unavailable`, `docker.in_use`, `docker.owned`,
  `docker.compose_unavailable`, `tailscale.unavailable`,
  `tailscale.repo_conflict`, `tailscale.package_present`,
  `tailscale.package_held`, `tailscale.pin_failed`, `tailnet.policy_conflict`,
  `tailnet.policy_invalid`, `cloud.not_connected`, `cloud.auth_failed`,
  `cloud.permission_missing`, `cloud.rate_limited`, `cloud.unavailable`,
  `cloud.uncertain_outcome`, `cloud.conflict`, `npm.unhealthy`,
  `npm.not_public`, `npm.setup_failed`, `derp.region_taken`,
  `derp.port_in_use`, `derp.verify_failed`, `derp.not_ready`,
  `panel.totp_incomplete`,
  `panel.already_exposed`, `panel.wrong_host`, `panel.not_exposed`,
  `core.child_failed`, `core.needs_repair`, `core.recovering`,
  `core.stream_limit`, `core.undo_conflict`, `core.quarantined`,
  `core.change_busy`, `core.deadline_exceeded`, `core.uninstalling`,
  `cloud.cleanup_pending`, `npm.reserved`, `npm.unsupported_target`, `cloudflare.reserved`,
  `cloudflare.unsupported_target`, `cloudflare.connector_missing`,
  `derp.no_fallback`. Guard
  failures use the existing `plan.guard_failed`; `module.unavailable` and
  `request.invalid_input` already exist (wave 0 checks each name against
  `internal/core/errcode` and adds only the missing ones).
- **Status and title** (the `errcode` registry entry of each new code).
  *Problem* codes answer a request before a change exists, as RFC 9457
  problem details with this status; *action* codes are stored in a
  change's error (and in the audit log) and use the status only when an
  API call returns them synchronously, like the existing
  `change.revert_failed`:

  | Code | Kind | Status | Title |
  |---|---|---|---|
  | `firewall.manual_required` | problem | 409 | Manual action required |
  | `firewall.unsupported_integration` | problem | 409 | Docker integration unsupported |
  | `firewall.unsupported_configuration` | problem | 409 | Firewall configuration unsupported |
  | `firewall.prediction_mismatch` | action | 500 | Firewall state differs from the prediction |
  | `firewall.docker_user_in_use` | problem | 409 | DOCKER-USER holds foreign rules |
  | `docker.engine_unavailable` | problem | 503 | Docker Engine unavailable |
  | `docker.in_use` | problem | 409 | In use |
  | `docker.owned` | problem | 409 | Owned by a Darkpanel module |
  | `docker.compose_unavailable` | problem | 409 | Compose unavailable or project refused |
  | `tailscale.unavailable` | problem | 503 | Tailscale unavailable |
  | `tailscale.repo_conflict` | problem | 409 | Conflicting Tailscale repository |
  | `tailscale.package_present` | problem | 409 | Tailscale already installed |
  | `tailscale.package_held` | problem | 409 | Tailscale package held |
  | `tailscale.pin_failed` | action | 500 | Tailscale pin failed |
  | `tailnet.policy_conflict` | problem | 409 | Policy changed since the plan |
  | `tailnet.policy_invalid` | problem | 422 | Policy invalid |
  | `cloud.not_connected` | problem | 409 | Account not connected |
  | `cloud.auth_failed` | problem | 502 | Provider rejected the credentials |
  | `cloud.permission_missing` | problem | 502 | Provider permission missing |
  | `cloud.rate_limited` | problem | 503 | Provider rate limit |
  | `cloud.unavailable` | problem | 503 | Provider unavailable |
  | `cloud.uncertain_outcome` | action | 504 | Provider outcome uncertain |
  | `cloud.conflict` | problem | 409 | Provider state changed |
  | `cloud.cleanup_pending` | problem | 409 | Cleanup pending on the account |
  | `npm.unhealthy` | problem | 503 | Nginx Proxy Manager unhealthy |
  | `npm.not_public` | problem | 409 | Host not publicly reachable |
  | `npm.setup_failed` | action | 500 | Nginx Proxy Manager setup failed |
  | `npm.reserved` | problem | 409 | Reserved by an exposure |
  | `npm.unsupported_target` | problem | 422 | Unsupported proxy target |
  | `cloudflare.reserved` | problem | 409 | Reserved by an exposure |
  | `cloudflare.unsupported_target` | problem | 422 | Unsupported tunnel target |
  | `cloudflare.connector_missing` | problem | 409 | No connector for the tunnel on this host |
  | `derp.region_taken` | problem | 409 | DERP region id taken |
  | `derp.port_in_use` | problem | 409 | Port in use |
  | `derp.verify_failed` | action | 500 | DERP verification failed |
  | `derp.not_ready` | problem | 409 | DERP server not ready |
  | `derp.no_fallback` | problem | 409 | No other reachable DERP region |
  | `panel.totp_incomplete` | problem | 409 | TOTP not enrolled for every user |
  | `panel.already_exposed` | problem | 409 | Panel already exposed |
  | `panel.wrong_host` | problem | 422 | Not the controller's host |
  | `panel.not_exposed` | problem | 404 | Panel not exposed |
  | `core.child_failed` | action | 500 | A composite step failed |
  | `core.needs_repair` | both (the stored error of a change entering `needs_repair`, and a refusal naming such a change) | 409 | Change needs repair |
  | `core.recovering` | problem | 409 | Recovery in progress |
  | `core.stream_limit` | problem | 429 | Too many streams |
  | `core.undo_conflict` | problem | 409 | Undo window closed |
  | `core.quarantined` | problem | 409 | Controller in restore quarantine |
  | `core.change_busy` | problem | 409 | Change busy |
  | `core.deadline_exceeded` | action | 504 | Action deadline exceeded |
  | `core.uninstalling` | problem | 409 | Host is being uninstalled |

  Any further code sections 1.2–1.3 introduce gets a row in this table.
  **Detail rules:** a problem's `detail` is the module's reason text
  (the guard message, the conflicting rule, the blocking change id, the
  missing permission's name); it never carries secret values or a
  provider's response body, only the provider's HTTP status and error code.
  An action code's message is stored in the change's `ChangeError` under
  the same rules. The API's existing sanitization of `internal` errors is
  unchanged.
- **Actors:** `system:firewall.reconcile`, `system:tailnet.keysweep` and
  `system:cloudflare.hostnames` (1.9), `system:listen.cert` (the
  certificate source's internal reads, 1.7, 1.12), `system:recovery` (startup
  recovery, composite recovery and unwind, resumed undos, recovery
  handlers, the revert watchdog's restart; accepted by the principal
  checks for undo, lookup, retire, revert, confirm and release of changes
  it recovers, for a composite's step only with `call.parent` naming that
  composite, 1.2; it never plans or applies a new attempt and never
  resolves an effect, 1.3).
- **Dependencies** (wave 0 adds all): `github.com/tailscale/hujson`;
  `golang.org/x/crypto` (argon2, chacha20poly1305); `golang.org/x/sys/unix`
  for hand-written netlink (already present); `vitest-axe` and Playwright as
  web development dependencies; no Docker or Tailscale SDK.

### 12. Owner checklist (the end of this programme)

1. M2 row 35: minisign keys and secrets in the release environment, the
   environment limited to `v*` tags, a `v*` tag ruleset, the SSH tag-signing
   key in `release/allowed_signers`, the public key in
   `internal/update/trustedkeys`, `make install-sh` committed.
2. Credentials for the live step: a test tailnet OAuth client with the scopes
   of 4.2a and allowed tags (also the CI secrets of ADR 0008); a Cloudflare
   API token with Cloudflare Tunnel Write, Zone Read and DNS Write on a test
   zone; a domain, a public host for Let's Encrypt and DERP, and a real ACME
   contact address. Claude then runs the live demonstrations: tailnet
   management and the join at install, a DERP region carrying traffic between
   two nodes, a container exposed through NPM with a real certificate and
   through a tunnel, the panel exposed and removed.
3. Tag `v1.0.0-rc.1` (closes M2 rows 11 and 16 once the release run is
   green).
4. Reinstall the production VPS from `v1.0.0-rc.1` (0.x does not upgrade to
   1.0), add one agent, and run them for 14 days with no manual repair (S7).
5. Tag `v1.0.0`; milestones close through `dforge:closing-a-milestone`.
6. Standing: after each Tailscale security release, a Darkpanel release that
   bumps `release/derper.version`, because DERP hosts hold tailscaled at the
   derper version (4.3).

## Contracts

- Every new action follows ADR 0002's compatibility rules and gets a schema
  snapshot under `schemas/`; `guard` and `overrides` (the embedded
  `core.Guarded`, 1.5) are part of each guarded action's schema.
- Agent protocol: `hello` and the frozen surfaces are unchanged. New,
  non-frozen agent routes: `GET /agent/v1/streams`,
  `GET /agent/v1/streams/{module}/{name}`, `POST /agent/v1/changes/{id}/undo`,
  `/repair` (1.3's repair table), `/release`,
  `/effects/{seq}/resolve` and `/private-output/ack`,
  `GET /agent/v1/changes/{id}/effects` and `/effects/{seq}/observe`,
  `GET /agent/v1/plans/{plan id}`
  (`PlanLookup` with `Deadline`; `private=1` over the controller session
  only), `POST /agent/v1/plans/{plan id}/retire`,
  `PUT /agent/v1/modules/{id}/enabled`,
  `GET /agent/v1/modules/{id}/availability`,
  `POST /agent/v1/reveal/{name}/{version}` (1.14; with the request header
  `Darkpanel-Internal: 1` it serves `Internal` reads, 1.7); DTOs in 1.3's wire
  contract and 1.4–1.14. The plan and apply requests gain the core-level
  `call` object in the `Darkpanel-Call` header (1.5; its `guardRefresh` is
  read at apply only), accepted
  only on the controller session; the apply request, for long-running and
  task actions, returns once the intent is persisted. The repair target is
  taken only from the repair route's path (stored in `core_plans.repairs`);
  a plan request that supplies `repairs` is refused. The undo and release
  requests carry `call` (its `parent` names the composite for
  `system:recovery`). During start-up admission (1.3) mutating
  routes answer `core.recovering`.
- API: `POST /api/v1/changes/{id}/undo`, `/repair` (`RepairRequest` with
  the optional `input`), `/release` and `/effects/{seq}/resolve`
  (`ResolveRequest`: `accept` or `reconcile`, optional `resource`),
  `GET /api/v1/changes/{id}/effects`, `/effects/{seq}/observe`
  (`ObserveResult`, read-only) and `/steps` (DTOs, status codes and scopes
  in 1.3's wire contract; `ChangeView` gains `parentChangeId`, `owner`,
  `repairedBy`, `repairOutcome` and `override`; `ChangeError` gains
  `Cause`); apply answers
  `202` for composites, long-running and task actions; a plan request that
  supplies `repairs` is refused; the controller socket's backup and quarantine
  routes (1.12, 10); the SSE
  stream relay and the `GET /api/v1/events` publisher (1.6); the reveal
  routes `POST /api/v1/hosts/{host}/actions/{name}/{version}/reveal` and
  `POST /api/v1/actions/{name}/{version}/reveal` (1.14); asynchronous apply for composites and long-running actions
  (apply answers with the change in `applying`); `core.host.create` gains
  optional `tailnetJoin` and `tags`; enrollment gains the versioned `dpb2.`
  bootstrap bundle beside the unchanged five-field token;
  `darkpanel change undo|repair|release|resolve|effects|steps`, `darkpanel controller quarantine
  list|resolve|enroll-token|release`.
- Shared contracts: class `composite` with `unwind: reverse | forward`;
  the additive change states `awaiting_confirmation`, `undoing`,
  `needs_repair` (composites and single changes), `recovering`, `repaired`,
  `kept`, `abandoned`, `quarantined` and, for composites, `releasing`
  (`applying` exists); owners `engine`, `composite`, `undo`, `module`,
  `repair`, `resolve`; composite step states including `planning` and
  `planned`; `ActionSpec.Available`, `MinScope` (raise only), `Internal`,
  `ForwardOnly`, `Converges`, `RepairedBy`, `ApplyTimeout`, `LongRunning`,
  `Task`, `Unwind`; the handler interfaces `Observer` (`Observation` with
  `Candidates`, `Exclusive`, `Allowed`), the optional `Resolver`
  (`Resolved`), `Undoer` (`Inverse` returning `InverseResult`),
  `Recoverer` (`RecoveryOutcome`), `Composer`, `TaskMutator` with
  `DefineComposite` and `DefineTask` (a task action without a module
  `Recoverer` is recovered by the engine's task sequence, 1.3c), and the
  matching `ErasedHandler` fields `Observe`, `Resolved`, `Inverse`,
  `Recover`, `Task`, `Steps`, `Output`; `Revert` returning `RevertOutcome`
  (with `Effects`, the per-effect `EffectOutcome` the engine journals) and
  the revert watchdog exit (1.2); `Step` and its reference syntax;
  `Env.Effects` with `EffectJournal`, `Effect` (`Local`, `InverseLocal`,
  `CommitBy`, `CommitWait`, `InverseRecovery`, `UndoResource`, `Reason`),
  `StateDoc`, `RecoveryID`, the effect states including
  `EffectUnresolved`, `Definite` and `IsDefinite`; the lock-epoch undo
  window; `PlanLookup` (with `Deadline`), `EffectView`, `ObservationView`,
  `ObserveResult`, `ResolveRequest`, `StepView`, `RepairRequest` (with
  `Input`), `RepairResult`, `ReleaseResult`, `ChangeError.Cause` and the
  `AgentClient` and `Dispatcher` additions (1.3); the repair target taken
  from the repair route's path and the `repairOutcome` output field of
  `RepairedBy` actions; the uninstall barrier (`core_meta` key
  `uninstalling`, `core.uninstalling`, `darkpanel uninstall --abort`,
  1.3c, 1.11);
  `core.TransientService.RuntimeMax` and `CollectMode` (aliased by
  `platform`, 1.8);
  `core.Guard.Overridable`,
  `Overridden`; `ActionSpec.Guards` with `GuardSpec`, the embedded
  `core.Guarded`, `Env.Call` with `CallContext` (1.5); the schema
  reflector's `netip` mapper and formats (1.5); `ActionSpec.Reveal`,
  `Revealed[Out]`, `DefineReveal`, `DefineInternalRead`,
  `ErasedHandler.Reveal` and `Engine.Reveal` (1.7, 1.14);
  `ext.Dispatcher.ReadInternal` with the calling principal (1.7, 1.12),
  served end to end by the reveal path;
  `core.PlanOptions` (the trailing argument of `AgentClient.Plan`,
  `Dispatcher.Plan` and `ext.Dispatcher.Plan`) and `ApplyOptions.Call`
  (1.5); the `AgentClient` additions `SetModuleEnabled` (1.4), `Streams`
  and `OpenStream` with `StreamInfo` (1.6), `Availability` with
  `ModuleAvailability` and `ActionAvailability` (1.7) and `Reveal` (1.14),
  and the `Dispatcher` additions `OpenStream` (1.6) and `Reveal` (1.14);
  `ext.Deps.Audit` (1.12); the replaced `core.Uninstaller` with
  `UninstallOptions() []UninstallOption` and the options map, and
  `install.UninstallConfig.ModuleOptions` (1.11); the convergent edit
  kinds of `tailnet.policy.patch` (`set`, `delete`, `addItem`,
  `removeItem`; 4.2), which every module that edits the policy uses;
  `StreamDef`, `Streamer` and `DefineStream`, the event bus's
  `Publish(eventType, data)` and epoch-qualified SSE ids (1.6); the
  reshaped `BackgroundTask` and `TaskContext` (1.4); the
  `internal/core/ext` package, `core.Listeners` (1.10, 1.12); `core.Units`
  on `Env` with `TransientService` (including `RuntimeMax`),
  `TransientTimer`, `UnitState` (including `MainPID`), `UnitStatus` and
  `List`, moved into `internal/core/units.go` with `platform` aliases, the
  scoped module wrapper and `Manifest.ExternalUnits` (1.8); owned-path
  kinds `restore`, `managed-dir`, `sysctl` and `service-dir`, the writable
  kind `unit`, the `OwnedPath` fields `Pattern`, `User` and `Subdirs`, the
  new `FileWriter` methods including `RemoveServiceDir` and `Owns`,
  `Remove` on kind `unit`, and `core.Ownership.Owns` (1.8); `ExecRule`,
  `ProductionRules` and the `apt-get` vectors `APT-1`–`APT-6` (1.8); the
  route and service registration hooks (1.12); task units
  `darkpanel-task-<change id>-<op>-<attempt>.service` running
  `{{.Binary}} agent task <change id> <op> <attempt>` (1.3c).
- Database: new module tables are prefixed with the module id; core gains
  on `core_changes` the columns `parent_change_id`, `recovery_owner`,
  `repaired_by`, `repair_outcome`, `recovery_retry`, `private_output_ct`,
  `lock_epochs`, `override_json` and `system` (1.4), on `core_plans` the
  columns `repairs` and `call` (1.5); the tables `core_effects` (both
  sides; one row per effect, secret sealed), `core_lock_epochs` (both
  sides), `core_composite_steps` and `core_composites` (controller),
  `core_task_runs` (agent, keyed by change id, op and attempt), the
  agent's `core_meta` key `uninstalling` (1.3c), `core_listeners`,
  the `restore_quarantine`
  marker with its restore transaction id and `quarantined_from` on
  quarantined records (10); all migrations are expand-only.
- Files on disk: every new file, directory, unit, user, kernel parameter
  and block is in its module's owned paths, of the kinds 1.8 lists
  (`file`, `block`, `unit`, `user`, `restore`, `managed-dir`, `sysctl`,
  `service-dir`); UFW state files are `restore` paths. The exec allowlist
  of 1.8 is complete: a program or argument vector it does not list cannot
  run.
- ADR 0008 divergence: ADR 0008 planned a test tailnet in CI from M5; this
  programme runs tailnet-dependent tests in the live step (owner decision),
  and the harness runs the node tests that need no tailnet: package install,
  status of a logged-out node, prefs patch and revert, the guard's
  `RunSSH`-on refusals, and the latency resource against the DERP map. ADR
  0014 records the divergence.
- ADR 0014 records: 1.1–1.14, the composite refinement of ADR 0004, the
  module recovery handler exception to ADR 0004 (1.3b), single-change
  repair under the repaired change's locks and audited release (1.3), the Compose
  exceptions to ADR 0009 and R54 (3), the
  owned-path kinds, module-owned Docker objects, DERP as a signed artifact
  under systemd and `cloudflared` as a container (closing the deferred
  decisions in `docs/adr/README.md`), the full-vector exec rules (1.8),
  the controller-generated call context (1.5), the reveal path (1.14), the
  extension package and its frozen call sites (1.12), and the new packages
  (`core/ext`, `platform/dockerapi`, `tslocal`, `netstat`, `netfilter`,
  `controller/cloud`, `controller/events`, `controller/backup`).

## Parallel decomposition

"Owns" is within a wave; a later wave may edit an earlier wave's files.

| Wave | Part | Owns | Consumes | Runs |
|---|---|---|---|---|
| 0 | Core | `internal/core/**`, `internal/agent/**`, `internal/controller/**` except module code, `internal/transport/**`, `internal/cli/**` except module command files (including `internal/cli/controller.go` and the registration hook), `cmd/darkpanel/**` (the path-selection hook), `internal/install/**`, `internal/platform/` existing files and the runner, `internal/modules/modules.go`, module skeleton packages and every stub of 1.12 (`internal/cli/<module>.go`, `internal/cli/controller_backup.go`, `internal/controller/backup/contribute.go`, `internal/controller/listen/tailscale_cert.go`, `web/src/modules/<id>/{routes,container-actions,host-create,global}.tsx` as each module needs), `web/src/modules/registry.ts`, `web/src/modules/types.ts`, `web/src/app.tsx`, `web/src/pages/hosts.tsx`, `web/src/pages/create-host-dialog*`, `web/src/components/change-dialog*`, `web/src/shell.tsx`, `web/src/api/**` (including `events.ts`), `go.mod`, `go.sum`, `web/package.json`, `web/pnpm-lock.yaml`, `Makefile`, `test/harness/pins.go`, `provision.go`, `images.json` and the harness helpers, `docs/adr/0014-*`, `docs/adr/README.md` | — | alone |
| 0b-1 | Docker Engine client | `internal/platform/dockerapi/**` | 0 | parallel |
| 0b-2 | Tailscale local client | `internal/platform/tslocal/**` | 0 | parallel |
| 0b-3 | Sockets and routes | `internal/platform/netstat/**` | 0 | parallel |
| 0b-4 | Netfilter save parser | `internal/platform/netfilter/**` | 0 | parallel |
| 1 | Firewall: model, UFW, prediction, evaluator, guard, safe apply, snapshots, break-glass | `internal/modules/firewall/**` except `docker*.go`, `exposure*.go`, `reconcile*.go`; `internal/cli/firewall*.go`; `web/src/modules/firewall/**` except docker and exposure pages; `test/harness/m3_firewall_test.go` | 0, 0b | alone |
| 2 | Firewall: Docker integration, reconcile, exposure view | `internal/modules/firewall/docker*.go`, `exposure*.go`, `reconcile*.go`, their web pages, `test/harness/m3_docker_test.go` | 1 | alone |
| 3a | Docker module | `internal/modules/docker/**`, `web/src/modules/docker/**`, `test/harness/m4_test.go` | 2 | parallel |
| 3b | Tailnet (contract check: scopes, policy API) | `internal/modules/tailnet/**` except `join*.go`; `web/src/modules/tailnet/**` except `host-create.tsx` | 0 | parallel |
| 3c | Cloudflare account, tunnels, hostnames (contract check: permissions, configuration API) | `internal/modules/cloudflare/**` except `connector*.go`, `expose*.go`; `web/src/modules/cloudflare/**` except connector and expose pages | 0 | parallel |
| 4 | Tailscale node, guard, package, web certificate, tailnet join at install (spike: local API endpoints) | `internal/modules/tailscale/**`, `web/src/modules/tailscale/**`, `internal/modules/tailnet/join*.go`, `internal/controller/listen/tailscale_cert*.go`, the `core.host.create` and enrollment-bundle files in `internal/controller`, `install.sh.in`, `install.sh`, the agent install path in `internal/install`, the bundle parser in `internal/transport/token.go`, `internal/cli/host*.go`, the Hosts dialog extension `web/src/modules/tailnet/host-create.tsx`, `test/harness/m5_node_test.go` | 1, 3b | alone |
| 5 | DERP (spike: derper flags, IP certificate expiry, non-root verification, the DERP-region debug endpoint as diagnostics, visibility grant) | `internal/modules/derp/**`, `web/src/modules/derp/**`, `release/derper.version`, `.github/workflows/release.yml`, `test/harness/m5_derp_test.go` | 1, 3b, 4 | alone |
| 6a | Nginx Proxy Manager (spike: image digest, `LE_SERVER` and the CA-bundle variable, NPM object identities, 1.2) | `internal/modules/npm/**`, `web/src/modules/npm/**`, `test/harness/m6_npm_test.go` | 2, 3a | alone |
| 6b | Cloudflare connector and expose container | `internal/modules/cloudflare/connector*.go`, `expose*.go`, `web/src/modules/cloudflare/connector*`, `expose*`, `test/harness/m6_cloudflare_test.go` | 3a, 3c, 6a | alone |
| 7 | Controller backup and restore | `internal/controller/backup/**` (its routes through `backup.Contribute`, 1.12; it sets the quarantine flag that wave 0's gate reads), `internal/cli/controller_backup*.go`, `internal/cli/controller_quarantine*.go`, the controller unit template in `internal/install`, `test/harness/restore_test.go` | 0 | alone |
| 8 | Panel exposure | `internal/modules/panel/**`, `web/src/modules/panel/**`, `test/harness/m6_panel_test.go` | 2, 6a, 6b | alone |
| 9a | Fuzz inventory and targets | `docs/security/parsers.md`, `*_fuzz_test.go`, `testdata/fuzz/**`, `scripts/fuzz.sh`, `.github/workflows/fuzz.yml` | 0–8 | parallel |
| 9b | Accessibility | `web/src/**` except the frozen `web/src/app.tsx`, `web/src/modules/registry.ts` and `web/src/modules/types.ts` (1.12; a fix they need goes through the integrator) (fixes and `.a11y.test.tsx`), `docs/accessibility.md` | 0–8 | parallel |
| 9c | Docs and API reference | `docs/user/**`, `docs/dev/**`, `docs/api/**`, the `api-docs` target in `Makefile`, `.github/workflows/ci.yml` | 0–8 | parallel |
| 9d | Upgrade matrix and footprint | `test/harness/upgrade_matrix_test.go`, `footprint_test.go`, the `harness` build-tag files `cmd/darkpanel/paths_harness.go` (the wave 0 path-selection hook, 1.12) and any in `internal/agent/`, `.github/workflows/harness.yml` | 0–8 | parallel |
| 10 | Security review, fixes, runbook, RC readiness | `docs/security/threat-model.md`, `docs/runbook.md`, `CHANGELOG.md`, `docs/versioning.md`; fixes anywhere | 0–9 | alone |

- Wave 0's frozen, handed-over and not-frozen files are listed in 1.12;
  a row above never contradicts that list. Before wave 9, a change to `harness.yml` (a job split or a CI secret) belongs to
  the wave's alone part or, in a parallel wave, to the integrator on `main`.
  Within wave 9 only 9c edits `Makefile`.
- Schema snapshots and the OpenAPI document are regenerated by the integrator
  after each wave merges.
- Every part with a spike or contract check runs it as its first task; a
  result that changes this spec is applied to the spec on `main` by the
  integrator (the spec file has no owner inside a wave) before the part's
  plan continues, and one that breaks a requirement goes to the owner.
- Test isolation: unit tests use `t.TempDir()`, in-process fakes on random
  ports or temporary Unix sockets and no global state; harness tests derive
  VM names, work directories and network ranges from the test name (wave 0
  helper) and respect the VM concurrency limit; a job whose total passes its
  120-minute limit is split per milestone.

## Plans

One plan per wave, named `docs/dforge/plans/<date>-v1-w<n>-<topic>.md`,
written after the previous wave merges. Wave 0 is the exception (owner,
2026-10-21): it is planned as three sequential plans, each written after
the previous one merges and wave 0 still running alone: `w0a` the
lifecycle core (1.1–1.3c), `w0b` platform and surfaces (1.4–1.8, 1.11,
1.13, 1.14) and `w0c` the extension package, cloud plumbing, listeners,
web extension points, harness helpers, error codes and ADR 0014 (1.9,
1.10, 1.12, 11, Contracts), which also builds 1.3's web UI for undo, repair,
release and per-effect resolution, because it owns the change dialog.
Wave 0's file-ownership row covers all three. Every plan ends with the gates in
`docs/dforge/gates.md`, including the harness gate when it touches the
harness, a module's host behaviour or the paths gate 7 lists (waves 0, 4, 7
and 9d touch `test/harness/`, `install.sh.in` or `internal/install/`).

## Verification

| Exit criterion | Evidence |
|---|---|
| M3: S1–S3 on all four OS | harness `TestM3*`: refusal for each protected flow (SSH, direct panel, agent link, outbound controller link, tailscaled egress), an IPv6 SSH session with `IPV6=no` refusing `state.enable`, revert on client disconnect and on reboot, prediction mismatch reverts, a manual `ufw` edit during the pending window survives the revert, an unsupported configuration (executable `before.init`) refused, exposure equals the scan by 2.7's rule for IPv4 and IPv6 (including a `partial` rule and IPv6 published to an IPv4-only bridge), published ports unreachable unless a binding is allowed, a new Docker network protected at creation, containers recreated under continuous scanning (including across an agent restart) never exposing an unallowed binding, foreign `DOCKER-USER` rules refusing enable and, added later, refusing every mutation, disable leaving no Darkpanel chain loaded, tagged and manual `ufw route` rules surviving `ufw reload` and `disable`/`enable` with the block present, two containers on a public IPv4 and on a global IPv6 network reaching each other with the integration on, two bindings to one container port with disjoint allowed sources each refusing the other's source, an unallowed IPv6 binding proxied to an IPv4-only container closed, a non-Docker DNAT forward refusing enable until exempted and working after, `IPV6=no` with Docker IPv6 rules loaded refusing `state.enable`, an asymmetric-route SSH session protected with only the main routing table and loose `rp_filter`, an unconfirmed `state.disable` reverting to an active firewall, an unconfirmed `state.repair` from a deliberately out-of-sync loaded state reverting to that state (timer and reboot), a sysctl change by UFW's `sysctl.conf` reverted with every value read back, a foreign `DOCKER-USER` rule added during a confirmation window and during an interrupted cleanup surviving the revert, an explicit deny on one binding not removing another binding's allowance of the same source to the same container port, `firewall.defaults.set` to allow incoming refused while the integration is on, a forward exemption overlapping a Docker subnet refused, a source in a configured `privateSources` range shown as the `private` class and reaching a published port, a rule restricted by source port scanned from a source port inside and outside its range, and a native nftables base chain with a rule reported as an unsupported configuration |
| M4: every Docker requirement on a managed agent, including allow and deny from a container view | harness `TestM4` on the enrolled agent VM with the Compose plugin (including a project with a Git `include` and one with a `build` section refused without any `git`, `ssh` or buildx process running, and an undo of `container.start` refused as a conflict after the container was restarted by hand); web tests of the container action slot |
| M5: node and tailnet management; a Darkpanel DERP region carries traffic between two nodes | unit tests against fakes (pin rollback in every phase, including an interrupted `dpkg`; `derp.cert.rotate` and its undo; upgrade and rotation refused without a fallback region); harness node tests that need no tailnet (Contracts), including a pin whose check fails after the `held` phase with the controller link blocked; the live step for tailnet, join at install and DERP |
| M6: container exposed through NPM and through a tunnel; panel exposed and reverted | harness: real NPM issuing a certificate from Pebble (`LE_SERVER` pointed at Pebble, `pebble-challtestsrv` as Pebble's DNS resolving the test hostname to the VM, Pebble's root in NPM's CA bundle; set only in harness deployments), an ordinary request through the custom location arriving with one `Host` header, real cloudflared reading its token file (it cannot reach a fake edge: the test checks the container, the file, and that inspect and logs never show the token; connector status from the fake API), a default-bridge-only target refused in preview, `npm.proxyhost.add` to `127.0.0.1:81` and to the panel gateway refused outside their composites, panel exposure through NPM end to end with the spoofed-header tests, and the panel exposure removed through the exposure itself; the live step for real certificates, tunnels and DNS |
| M7: no open critical or high findings; matrix, footprint, accessibility, docs | security register; `TestUpgradeMatrix`, `TestFootprint`; `docs/accessibility.md` with automated and manual evidence; docs CI check |
| M8: v1.0 tagged and signed, production plus one agent, runbook | owner checklist items 1, 3–5; `docs/runbook.md`; harness restore test |

## Rejected alternatives

- One plan for all of v1: about 300 tasks written against code that does not
  exist yet; rejected by the owner for rolling wave plans.
- Milestone-by-milestone order; user-flow slices first (owner chose core
  first with parallel waves).
- Planning every composite child up front (revision 1); several revertible
  children per composite (revision 2): a partial confirmation could not be
  undone.
- Undo from the action's output (revision 2): the output is missing exactly
  when recovery needs it.
- A model of UFW's user rules alone for the guard and exposure view
  (revision 1); one representative source per class (revision 2): wrong for
  source-dependent rules.
- Denying only Docker subnets known when the block is written (revision 1);
  destination private ranges alone (revision 2): public and global subnets
  were unprotected.
- Route rules by container address alone (revision 2): a reused address
  inherited the allowance (owner chose D1). One shared `ufw-user-forward`
  jump for every binding (revision 3): source policy spilled across bindings
  to the same container port.
- Nested composites and a uniform unwind (revision 3): nesting deadlocked on
  the coordination lock, and unwinding a removal would have reopened the
  panel.
- Discovering a child's outcome by re-sending its apply (revision 3): it
  could perform a step that had never run.
- `docker.compose.up` as compensable (revision 3): recreation destroys
  writable layers that no undo restores.
- Container identity alone as the post-state (revision 4): a stop or a
  reconfiguration keeps the identity, so an earlier undo could reverse a
  later operation.
- Settling a timed-out Tailscale policy write by reading the old ETag
  (revision 4): the write could still commit later.
- Settling uncertain writes automatically (revisions 5 and 6: retiring a
  policy ETag with a settling write, classify helpers, supersession
  dependencies, recursive repair): every mechanism added to settle a case
  opened new cases; revision 7 stops automation at uncertainty and leaves
  the decision to the operator (1.2).
- Repairing a single change through a new action that takes the same lock
  (revision 4): the lock the failed change kept could never be acquired.
- Retrying a composite step by applying its stored plan again (revision 4):
  the engine returns the stored failure, and the plan expires and is bound
  to its principal.
- Running waves 6a and 6b in parallel (revision 4): both are exposure work,
  and NPM's upgrade can lose data (owner split them).
- Running a change against a scratch copy of UFW's files: UFW has no
  supported alternate root.
- Writing UFW's `user.rules` directly: UFW owns that file; Darkpanel writes
  through `ufw` and only restores snapshotted bytes.
- Vendor SDKs (Docker, Tailscale): large dependency trees against the S6
  footprint.
- derper built into Darkpanel or built on the host: see Owner decisions.
- Secrets in container environment variables or NPM's `INITIAL_ADMIN_*`
  variables: readable through inspect or logged.
- Publishing NPM's admin port on the tailnet or the public IP (owner chose
  D2).
- Upgrading 0.x hosts to 1.0: the updater refuses cross-major updates by
  design; the production host is reinstalled.

## Out of scope

M9 candidates; Docker's nftables backend (detected, reported unsupported);
Docker before 28.0 for the firewall integration and NPM; non-bridge Docker
networks for the integration (reported unprotected); Docker networks in
routed gateway modes, and direct-routed access to custom-named bridges with
non-private subnets (P2, reported unprotected); UFW with `IPV6=no` while
foreign IPv6 rules are loaded; UFW with executable
`before.init`/`after.init` or `MANAGE_BUILTINS=yes`; hosts with rules in
the other iptables variant or in native nftables base chains (reported as
an unsupported configuration); evaluating tailnet policy (ACL grants) in
the exposure view; private-registry pulls, remote `include`/`extends` and
image builds through Compose; exposing containers that are only on the
default `bridge` network; Headscale; more than one tailnet or Cloudflare account per
controller; editing Darkpanel-managed tunnels outside Darkpanel.

## Review log

- **Round 1 (2026-10-21)**, Fable and Codex gpt-6-astra, one shared brief:
  96 findings, 67 after deduplication (7 critical, 41 high, 13 medium, 6
  low), all accepted. Revision 2 changed action names, added undo handlers,
  composites as durable step logs, background tasks through the lifecycle,
  guard context as a schema field, streams through a new endpoint,
  per-action availability, restore writes and owned Docker objects,
  uncertain cloud outcomes, per-listener proxy trust, uninstall options,
  extension points; the firewall's configured and loaded state, predicted
  chains and an evaluator with real protected paths; logical snapshots with a
  pinned baseline; Docker 28 minimum, variant detection, exact block text and
  destination-range denies; Docker undo meanings, inspect redaction, Compose
  trust; the Tailscale guard, masked prefs, latency, repository conflicts and
  web certificate; tags, ETag-safe undo, HuJSON patches; the DERP manifest
  and durable swap; the NPM bootstrap and durable upgrade; Cloudflare probes,
  durable hostnames and the token file; panel exposure's path and
  local-first removal; the fuzz inventory, upgrade graph, footprint method
  and accessibility matrix; the backup format and staged restore; and a
  reshuffled wave layout.
- **Round 2 (2026-10-21)**, same panel on revision 2 (commit 082b278): Fable
  35 findings (plan-ready once its first 17 were fixed), Codex 57 (not
  ready); 72 after deduplication (7 critical), all accepted; owner decisions
  D1 (bindings) and D2 (NPM admin UI over HTTPS by explicit action).
  Revision 3 changed: an effects journal recorded before each effect, an
  undo route and repair (1.2); serializable steps with state dependencies,
  one revertible step per composite, coordination locks, engine integration,
  a recovery table and the controller start order (1.3); agent-side
  enablement (1.4); `guardRefresh` and per-guard overrides (1.5); stream
  budget and heartbeats (1.6); `MinScope` and `Internal` (1.7); per-path
  argument allowlists, restore entries, managed directories (1.8);
  per-endpoint uncertain outcomes and capability reporting (1.9);
  `X-Darkpanel-Client` and durable revocation (1.10); a certificate-source
  hook and wave 0b (1.12, decomposition); secret delivery (1.14); unsupported
  UFW configurations, the full rule grammar, transition-accurate prediction
  including `IPV6=no`, the out-of-sync check, a `raw`/`nat`/`filter`
  evaluator in wave 1, real flows for cloudflared and tailscaled with route
  lookups (2.1–2.3); drift-safe revert (2.5); `DOCKER-USER` adoption, the
  complete IPv4 and IPv6 blocks with the `ufw-user-forward` declaration,
  binding pins, bridge-interface denies, binding-level policy and a batch
  action, proxied families, owned-chain cleanup (2.6); partitioned exposure
  verdicts (2.7); before-state undo for Docker, controlled `DOCKER_CONFIG`,
  stored profiles, Compose ownership checks (3); the conservative `RunSSH`
  guard, route and relogin guards, `package.pin`, STUN and relay latency
  (4.1); tailnet join at install (4.2); DERP as its own wave, `-c`, a
  non-root service user, the ACL visibility grant, the relay probe and the
  version-coupling cost (4.3); renamed NPM nouns (5); NPM's Docker 28
  precondition, corrected setup flag and bootstrap, `acmeEmail`, the admin UI
  exposure and password reveal, the upgrade recovery table (6); Zone Read,
  exclusive tunnel management, the UID 65532 token file (7); the panel's
  certificate step, client header, timeouts and bridge name (8); the exact
  backup format and a root pre-start restore recovery (10).
- **Round 3 (2026-10-21)**, same panel on revision 3 (commit a16f757): Fable
  23 findings (68 of 72 round-2 rows resolved; blocking set F1, F2, F4–F9),
  Codex 42 (blocking set C1–C10, C18, C40–C42); 52 rows after deduplication
  (one critical), all accepted; two resolutions await the owner (P1, P2).
  Revision 4 changed: existing scopes kept with `MinScope` raising only
  (1.7); recovery identities, post-state checks, attachment claims and a
  durable undo state machine (1.2); no nested composites, `unwind: reverse |
  forward`, asynchronous apply for composites and long-running actions, a
  read-only plan lookup, persisted confirmation intent, a child watcher,
  current-step-first unwind, a full recovery table and module recovery
  handlers (1.3); background-task guard context (1.4); the availability
  route and unredacted internal reads (1.7); flat managed directories and
  the apt vector (1.8); no blind auth-key retry (1.9); command assembly hooks
  (1.12); sysctl and `mangle`/`security` tables, the protocol grammar,
  disable and `IPV6=no` transitions, the repair path, the full packet tuple,
  every plausible ingress (2.1–2.3); journaled inverses for every firewall
  mutation and single-change `needs_repair` (2.5); bridge in-interface
  exemptions, per-binding chains, a global DNAT gate with declared
  exemptions, foreign-rule refusal, proxied bindings under UFW `INPUT`, the
  integration flag across disable (2.6); irreversible `compose.up`, refusal
  of Compose provider services and the R54 exception (3); a self-rolling-back
  `package.pin`, `package.unpin`, the apt sequence, HTTPS probe latency
  (4.1); the versioned `dpb2.` bundle with key expiry and cleanup (4.2); the
  DERP order (ports confirmed before the region), relay safety for
  `omitDefaultRegions`, diagnostics-only probing, unpin on removal (4.3);
  the catalogue with unwind policies and long-running marks (5);
  conditional NPM ports and `confirm-public`, attachment claims, the
  location-level header policy, ACME fields (6); honest limits of tunnel
  read-back, flat token files (7); a forward, monotonic panel removal (8);
  transaction-specific restore files and the restore quarantine (10).
- **Round 4 (2026-10-21)**, same panel on revision 4 (commit d449bf5), full
  round: Fable 20 findings (49 of 52 round-3 rows resolved, 3 partly; wave 0
  blocked by four), Codex 42 (not ready; wave 0 blocked by 15); 53 rows after
  deduplication, all accepted. The owner confirmed P1 and P2 and split wave 6
  into two alone waves. Revision 5 changed: `ForwardOnly` actions, expected
  before and after states with recovery of interrupted modifications,
  ambiguous and conflict resolution, per-effect undo intent, state (not
  identity) post-state checks, supersession, NPM object identities, plan
  and apply checks of exclusive names (1.2); read steps, attempt lineage and
  re-planned repair, execution deadlines, transient task units across agent
  restarts, the lookup's `recovering` state, single-change repair under the
  repaired change's locks with `RepairedBy`, audited release, locks kept
  across restarts, a parent/child transition table, unwind by step class,
  module recovery outcomes with one owner per change (1.3); controller
  listeners protected under break-glass (1.5); `dpkg --configure -a`, the
  `sysctl` path kind and the five-table parser (1.8); the ETag-retiring
  settlement of policy writes, auth-key sweep horizons and disconnect
  refusal (1.9); route, service and quarantine hooks (1.12); rename intent
  in restores, `--recover` admission, refusal while a journal exists, the
  quarantine's admitted commands and suspended mutation sources (10); in
  sections 2–8: a restore that re-enables UFW, the complete sysctl model, a
  loaded-state before-image for repair, foreign rules preserved by revert,
  binding-qualified denies, narrowed DNAT exemptions, the incoming-deny
  invariant behind P1, source-port and mark partitions, every eligible
  ingress interface, other netfilter backends, configured private ranges
  (2); Compose remote loaders disabled (3); the package-pin rollback, the
  DERP upgrade path, certificate rotation and probe sources (4); NPM
  header rendering, default-bridge targets, reserved upstreams, retirement
  of the removed exposure's path and the tunnel input (6–8).
- **Wave-0-scoped check (2026-10-21)** on revision 5 (commit 249446e), at
  the owner's choice ("wave-0 check, then plan"): Fable 27 findings, Codex
  39 (one critical), both "not ready for the wave 0 plan"; 41 rows after
  deduplication, all accepted. Revision 6 changed, outside 1.1–1.3:
  background-task scheduling, lock keys, wake producer and
  `core_changes.system` (1.4); guard declarations (`GuardSpec`,
  `Guarded`) and the controller-generated call context (1.5); the stream
  declaration, discovery body and the new `/api/v1/events` publisher
  (1.6); the availability body and the `Internal` exception to `--local`
  (1.7); the complete full-vector exec allowlist with fixed environments,
  `Env.Units`, and file-provider operations for unit files, binary swaps,
  service-user directories and per-interface sysctl keys (1.8); one
  credential-deletion admission check, disjoint auth-key namespaces with
  ownership records, and service actors (1.9); the dependency-neutral
  extension package with frozen call sites and stubs, runtime listener,
  certificate source, CLI, path-selection and web extension signatures, and
  one exact frozen-file list (1.12); the reveal path (1.14); the sysctl
  declarations (2.1); the emergency-restore precedence (2.5); one Docker
  comparator table (3); one transient unit name (4.1); join-key records
  (4.2); DERP's file layout on the new operations (4.3); NPM's identity and
  settlement tables (6); quarantine marked on the staged database before
  installation and verified before start, lock-row disposition, resolve
  outcomes and route bodies (10); status and title of every new error code
  (11); the decomposition and Plans aligned with 1.12. Inside 1.1–1.3 it
  defined the effects journal API (`EffectJournal`, `Effect`, `StateDoc`,
  effect and undo state tables, resolution, supersession dependencies,
  expected inverse outcomes), the handler interfaces and `Define*` helpers,
  canonical composite states with exhaustive parent/child and recovery
  tables, repair by capability with lineage and lock ownership, recovery
  owners claimed by conditional update and the start-up order with an
  admission phase, task units (1.3c), execution and composite deadlines
  with a fresh cleanup context, sealed private child-result handoff, the
  `Reserve` refusal detail and the wire contract of the change routes. An
  integration pass aligned the cross-references between 1.1–1.3 and the
  rest (Contracts, actors, 2.5, 5, 8, 1.11, forward-composite
  convergence).
- **Final wave-0 check (2026-10-21)** on revision 6 (commit 846488a):
  Fable 18 findings (34 of the 41 rows resolved, 7 partly; three blocking),
  Codex 31 (29 blocking, two for later waves), both "not ready for the wave
  0 plan"; about two thirds were again in the lifecycle core. The owner
  chose to **simplify the lifecycle core** rather than revise it further
  (Owner decisions). Revision 7 outside 1.1–1.3c: the systemd contract
  moved into `internal/core` with platform aliases, `UnitState.MainPID`,
  `Units.List`, a scoped module wrapper with declared external units
  (`tailscaled.service`: state, restart) and the task environment's unit
  client (1.8); unit-file removal through `Remove`, `RemoveServiceDir`,
  `Owns` and refusal of pre-existing unit files and directories (1.8); one
  canonical set of `apt-get` vectors (`APT-1`–`APT-6`, with
  `APT::Get::List-Cleanup=0`) and one source-list path, used by 4.1 (1.8);
  typed reveal output, `ErasedHandler.Reveal`, `DefineInternalRead`, the
  registry rule and reveal's exclusion from `--local` (1.7, 1.14); internal
  reads carry the calling principal for their audit, with
  `system:listen.cert` for the certificate source (1.7, 1.12); the event
  bus implements `ext.Publisher` directly and its SSE ids carry a process
  epoch (1.6); `netip` types in schemas through a reflector mapper and a
  schema test (1.5); `tailscale.prefs.repair` (4.1, 5); one compensation
  mechanism each for the Cloudflare hostname actions (7.3, 5); the auth-key
  orphan sweep skips in-flight creates (1.9); status-table kinds (11); 9b's
  row excludes the frozen web files.
  Inside 1.1–1.3c revision 7 cut supersession, the classify helpers and
  `Reconciler`, the `ambiguous` and `conflict` effect states (one
  `unresolved` state with a reason), automatic settlement of uncertain
  calls (only `Local` effects are settled, through a read-only `Observer`;
  every remote uncertainty goes to the operator after `CommitBy`),
  recursive repair (depth 1), the undo state column and
  `core_composite_attempts`; it added the lock-epoch undo window, the
  `kept` and `releasing` states, the owner `repair`, `RevertOutcome`,
  `Converges`, task units per op (`darkpanel-task-<change id>-<op>.service`)
  with a start fence, the composite cursor table, the plan retire route and
  the step-list route. An integration pass aligned the names across the
  spec: the `call` object no longer carries the repair target; unit names,
  `CollectMode` and `PendingJob` in `internal/core/units.go`; 1.9's
  uncertain writes, 2.5's firewall effects (`Local`, `Observer`,
  `RevertOutcome`), 3's `CommitBy`, 6's NPM settlement through `Observe`
  and operator resolution, `Converges` in 5 and 8, restore clearing
  copied owners (10), the Contracts lists, actors and status table (11).
- **Check of revision 7 (2026-10-21)** (commit 2dc40cb), giving revision 8: Fable (27
  findings) and Codex gpt-6-astra (32 findings), all accepted; per the
  owner's choice the wave 0 plan follows without another spec round, and
  the plan review is the next check. Outside 1.1–1.3c: the firewall revert
  follows 1.2's single precedence rule (an effect already unresolved blocks
  every write, conflicts are left in place and reported while the other
  effects are restored, the connectivity exception decided last on the
  state the revert leaves; 2.5); the replaced `Uninstaller`
  with declared options and defaults (1.11, 2.9); `tailscale.prefs.repair`
  and `firewall.state.repair` take `outcome` through the repair request's
  optional input, defaulting to `restore`, and report `kept` only when
  the repaired change's intended state verifies (2.5, 4.1);
  `tailnet.policy.patch` accepts only the convergent edit kinds `set`,
  `delete`, `addItem` and `removeItem` and declares `Converges` (4.2, 4.3,
  5); resolving an uncertain auth key moves its `tailnet_keys` row so the
  sweeps stop treating it as uncertain (1.9); credential deletion blocked
  by pending cleanup returns `Definite(cloud.cleanup_pending)` from the
  inverse (1.9); NPM bootstrap's lost response is handled by the ordinary
  undo and a new deploy (6); internal reads use the reveal path end to end
  with the `Darkpanel-Internal` header (1.7, 1.14); `ext.Deps.Audit`
  (1.12); `PlanOptions`, `ApplyOptions.Call` and the client-method
  additions for enablement, streams, availability and reveal (1.4–1.7,
  1.14); 4.1's pin unit records its row, never the change state. Inside
  1.1–1.3c: per-operation locality, inverse uncertainty, resolution under
  an owner with a read-only preview and `reconcile` (renamed from
  `retry`), inverse recovery identities, `RevertOutcome.Effects`, the
  revert watchdog, recovery handlers only after settlement, task attempts
  with a closed fence, epochs given back, composite tables by direction,
  parent-driven child release, the uninstall barrier, `ChangeError.Cause`.
  An integration pass reconciled both halves: the optional `Resolver`
  hook run inside the resolution transaction (1.9's key rows), the
  attempt-qualified unit names, `uninstall --abort` (1.11), the Contracts
  lists, and 2.5's revert order with 1.2's rule (plus the connectivity
  re-check after a revert that left effects in place).
