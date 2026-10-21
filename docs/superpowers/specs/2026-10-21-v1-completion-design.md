# v1 completion: firewall, Docker, Tailscale, exposure, hardening and release

**Date:** 2026-10-21
**Status:** design approved by the owner section by section on 2026-10-21;
revision 5 after the fourth (full) Fable and Codex gpt-6-astra panel round
(see the review log). The written spec awaits the owner's review.
**Product spec:** docs/superpowers/specs/2026-10-17-darkpanel-design.md
**Requirements:** R37–R81; M2 register rows 11, 16 and 35
(docs/superpowers/registers/2026-10-19-m2-fleet-lifecycle-core.md); the
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

ADR 0004's `compensable` class keeps its meaning (no timer; undone by a
compensating step). Every compensable action declares exactly one of: an
**undo handler** on the action itself (`Undoer`, below); a compensating
action (`CompensatedBy`, operator-invoked, as today; `core.update.apply` →
`core.update.rollback` is unchanged); or `ForwardOnly: true` for an action
whose effect is monotonic and is never reversed (`panel.listener.revoke`,
8). A `ForwardOnly` action's apply converges when run again after a partial
apply, and its plan reports an empty diff when its effect is already
present (it then applies as a no-op).

**Declarations.** `core.ActionSpec` gains these fields (1.5 and 1.7 add
`Guards`, `MinScope`, `Available` and `Internal`):

```go
type ActionSpec struct {
    // existing: Name, Summary, Version, Class, Side, BreakGlass, LockKeys, CompensatedBy
    ForwardOnly  bool          // compensable only (1.2)
    RepairedBy   string        // revertible only, required: an action of the same module (1.3)
    ApplyTimeout time.Duration // 0 means 30 s; composites: computed at plan (1.3)
    LongRunning  bool          // apply answers once the intent is persisted (1.3)
    Task         bool          // the host sequence runs in a transient unit (1.3c); set by DefineTask
    Unwind       Unwind        // composite only (1.3)
}
```

**The effects journal.** Every change of a mutating action has an effects
journal on its executing side (agent or controller database, table
`core_effects`). An action with an undo handler, and a revertible action
that reconciles (2.5), records each effect **before** making it, and every
journal write commits (fsync) before it returns. `core.Env` gains
`Effects EffectJournal`; the engine sets it whenever it sets `Env.ChangeID`:
in Apply, Verify, Revert, Undo, Reconcile, Recover and Task. Revert also
receives `Env.ChangeID` from wave 0 on (today `runRevert` passes neither).

```go
// EffectKind is what an effect does to its resource.
type EffectKind string

const (
    EffectCreate EffectKind = "create"
    EffectModify EffectKind = "modify"
    EffectDelete EffectKind = "delete"
)

// EffectState is an effect's state (table below).
type EffectState string

const (
    EffectIntended  EffectState = "intended"
    EffectMade      EffectState = "made"
    EffectSkipped   EffectState = "skipped"
    EffectAmbiguous EffectState = "ambiguous"
    EffectUndoing   EffectState = "undoing"
    EffectUndone    EffectState = "undone"
    EffectConflict  EffectState = "conflict"
    EffectAccepted  EffectState = "accepted"
    EffectAbandoned EffectState = "abandoned"
)

// StateDoc is the comparable form of a resource's state. State holds the
// fields an action sets and its inverse restores; Version holds the fields
// that change on every write or restart (a start time, an endpoint id, an
// ETag, modified_on). A StateDoc never holds a secret value; it is shown in
// effect views. State "null" means the resource does not exist. Each module
// section lists, per effect kind, which fields go in State and which in
// Version (2.5, 3, 4, 6, 7).
type StateDoc struct {
    State   json.RawMessage `json:"state"`
    Version json.RawMessage `json:"version,omitempty"`
}

// RecoveryID is how recovery finds exactly the object a creation made.
type RecoveryID struct {
    Kind  string `json:"kind"`  // "name" | "marker" | "idempotency"
    Value string `json:"value"`
}

// Effect is one row of core_effects.
type Effect struct {
    ChangeID    string
    Seq         int         // 1, 2, ... in the order intended
    Kind        EffectKind
    Resource    string      // "<module>.<noun>:<identity>"; for a creation, "<module>.<noun>:?<recovery value>" until Made names the object
    Recovery    *RecoveryID // creations only
    Before      StateDoc    // the state the call expects to find
    After       *StateDoc   // the state the call leaves; nil when it cannot be computed before the call (the action's section says so)
    Post        *StateDoc   // recorded by Made: the state undo must still find
    UndoTo      *StateDoc   // recorded by UndoIntent: the state the inverse leaves
    Current     *StateDoc   // recorded with ambiguous, conflict and accepted: what was found
    State       EffectState
    Resolution  string      // the last resolution applied (below), with ResolvedBy and ResolvedAt
    ResolvedBy  string
    ResolvedAt  time.Time
}

// EffectJournal is a change's effects journal. Every method commits before
// it returns; a method that would make an illegal transition (table below)
// returns an error and writes nothing.
type EffectJournal interface {
    // Intend records an effect in state intended and returns its seq. In the
    // same transaction it records the supersession rows of the effect
    // (below).
    Intend(ctx context.Context, e Effect, secret []byte) (seq int, err error)
    Made(ctx context.Context, seq int, resource string, post StateDoc) error
    Skipped(ctx context.Context, seq int) error
    Ambiguous(ctx context.Context, seq int, current StateDoc) error
    UndoIntent(ctx context.Context, seq int, undoTo StateDoc) error
    Undone(ctx context.Context, seq int) error
    Conflict(ctx context.Context, seq int, current StateDoc) error
    List(ctx context.Context) ([]Effect, error)
    // Secret opens the effect's sealed secret (associated data
    // "core_effects.secret_ct.<change id>.<seq>"), for example a created
    // key's id and value that an undo or cleanup needs.
    Secret(ctx context.Context, seq int) ([]byte, error)
}
```

Effect states:

| From | To | Written by | When |
|---|---|---|---|
| — | `intended` | `Intend` | before the call |
| `intended` | `made` | `Made` | the call succeeded, or recovery found the expected after-state |
| `intended` | `skipped` | `Skipped` | the call changed nothing, or recovery found the before-state |
| `intended` | `ambiguous` | `Ambiguous` | recovery found neither expected state, or a creation's identity matches more than one object |
| `made` | `undoing` | `UndoIntent` | immediately before the inverse |
| `undoing` | `undone` | `Undone` | the resource re-read equals `UndoTo` |
| `undoing` | `undoing` | `UndoIntent` again | the resource still equals `Post` (`ClassifyUndoing` returns `made`): the inverse did not run and runs again |
| `made`, `undoing` | `conflict` | `Conflict` | the resource equals neither `Post` nor `UndoTo` |
| `conflict` | `made` | resolution `retry` | the next undo compares again |
| `conflict`, `ambiguous` | `accepted` | resolution `accept` | the current state is kept as the end state |
| `ambiguous` | `made` | resolution `made` | the operator states the effect happened; the current state is recorded as `Post` |
| `ambiguous` | `skipped` | resolution `skipped` | the operator states nothing happened |
| `intended`, `made`, `undoing`, `ambiguous`, `conflict` | `abandoned` | release (1.3) | the change is given up |

`skipped`, `undone`, `accepted` and `abandoned` are terminal. An effect is
**unresolved** while it is `intended` after recovery has run, `ambiguous`
or `conflict`; a change with an unresolved effect is never treated as if
the effect had not happened (1.3).

- **Comparison.** Two `StateDoc` values are compared as canonical JSON
  (object keys sorted, no insignificant whitespace, numbers as
  `encoding/json` writes them). Core provides the helpers every handler
  uses, so the rules are the same everywhere:

  ```go
  // ClassifyIntended settles an intended effect from the resource's current
  // state. matches is how many objects the recovery identity finds (1 for a
  // modify or delete); uncertain is true when the provider may still commit
  // the call (1.9). It returns EffectSkipped, EffectMade, EffectAmbiguous,
  // or EffectIntended when uncertain is true and nothing was found.
  func ClassifyIntended(e Effect, current StateDoc, matches int, uncertain bool) EffectState
  // CheckPost reports whether current equals e.Post, State and Version.
  func CheckPost(e Effect, current StateDoc) bool
  // ClassifyUndoing settles an undoing effect: State equal to e.UndoTo's →
  // EffectUndone; equal to e.Post (State and Version) → EffectMade; anything
  // else → EffectConflict.
  func ClassifyUndoing(e Effect, current StateDoc) EffectState
  ```

  `ClassifyIntended`: for a creation, no match → `skipped` (or `intended`
  when uncertain), one match whose State equals `After.State` (any State
  when `After` is nil) → `made`, more than one match or one with another
  State → `ambiguous`; for a modify or delete, current equal to `Before`
  (State and Version) → `skipped`, `After` set and current State equal to
  `After.State` → `made`, anything else → `ambiguous`. The check before an
  inverse uses State **and** Version (a manual restart changes a start time
  and so blocks the undo); the check after an inverse uses State only (the
  inverse itself assigns a new start time, endpoint id or ETag).
- **Expected inverse outcome.** Before each inverse the handler computes
  `UndoTo` from `Before` (normally `Before` with Version cleared; for a
  creation `{"state": null}`) and records it with `UndoIntent`, so a crash
  between the inverse and `Undone` is settled by `ClassifyUndoing`.
- **Recovery identity (creations).** Every effect that creates something
  records its `RecoveryID` with `Intend`, before the call, chosen per
  endpoint and named in each action's section:
  - `name`, a deterministic exclusive name: a fixed name (`darkpanel-npm`)
    is checked free at plan and again at apply, immediately before the call;
    a name derived from the change id (`darkpanel-<change id>` for a tunnel)
    cannot exist before the change and is checked at apply, before the call;
  - `marker`, a marker the object carries (Docker label
    `darkpanel.change=<change id>`, DNS record comment
    `darkpanel:<change id>`; the Tailscale key description namespaces of
    1.9);
  - `idempotency`, a provider idempotency key where the provider offers one.

  Each module section that creates objects states its identity per
  endpoint and its settlement when the identity finds more than one object
  or may still find one later (Nginx Proxy Manager: section 6). Darkpanel
  never deletes an object its identity cannot tell apart from others.
- **Precedence: ambiguity is never overwritten by default.** An effect
  that is unresolved is never settled by restoring a whole snapshot, by an
  inverse, by deleting anything, or by releasing locks while work may still
  run. The one exception is a revertible action's **connectivity
  exception** (2.5): its `Revert`, and only its `Revert`, may restore its
  snapshot whole when the action's lockout guard, evaluated at that moment
  on the state the revert would otherwise leave, fails for a protected
  flow. The engine records that revert with journal result `override`; the
  unresolved effects keep their state with the overwritten state recorded
  in `Current`, and the change goes to `needs_repair` all the same, so the
  operator still sees and resolves them. Undo, unwind, release and module
  recovery never take the exception.
- **Supersession.** When `Intend` records an effect on a `Resource` that an
  effect of another change touched and that effect is not `skipped`,
  `undone` or `abandoned`, the journal writes a row
  `core_effect_deps (change_id, seq, later_change_id, later_seq, created_at)`
  for each such earlier effect, in the same transaction. Undo of an earlier
  effect with a dependency row is refused with `core.undo_conflict` naming
  the later change ("undo it first"). A dependency row is deleted when its
  later effect becomes `skipped` or `undone` (the later change left nothing
  behind on the resource). A later effect that ends `accepted` or
  `abandoned` keeps the row, so the earlier effect can then only be
  resolved with `accept` (its undo would reverse a state the operator
  kept). Locks are released when a compensable change completes (ADR
  0004); supersession is what keeps an earlier undo from reversing a later
  operation, and undo reacquires the locks (below) so no Darkpanel
  operation runs between its check and its inverse.
- **Undo handlers.**

  ```go
  // Reconciler settles a change's intended effects after a crash by reading
  // each resource and calling Made, Skipped or Ambiguous through
  // env.Effects (using ClassifyIntended), or leaving an effect intended
  // when the provider may still commit it. It never makes a change.
  type Reconciler interface {
      Reconcile(ctx context.Context, env Env, effects []Effect) error
  }

  // Undoer is the undo handler of a compensable action. Undo receives the
  // change's effects in state made or undoing, newest first. For each it
  // reads the resource; an undoing effect is settled with ClassifyUndoing
  // first; a made effect whose resource fails CheckPost is recorded with
  // Conflict and left alone; otherwise it calls UndoIntent, runs the
  // inverse, re-reads the resource and calls Undone or Conflict. It must
  // touch no resource the journal does not name. A returned error leaves
  // the remaining effects as they are.
  type Undoer interface {
      Reconciler
      Undo(ctx context.Context, env Env, effects []Effect) error
  }
  ```

  `DefineMutating` finds `Undoer` by a type assertion on its handler (the
  `Uninstaller` idiom) and `DefineRevertible` finds `Reconciler`; they fill
  the new `ErasedHandler` fields `Undo` and `Reconcile`.
- **Undo of a change** (`Engine.Undo(ctx, p, changeID)`): allowed on a
  compensable change with an undo handler in `completed`, `failed`,
  `interrupted` or `needs_repair` (undo state not `running`, no repair in
  progress); refused otherwise with `core.change_busy` (a mechanism owns it)
  or `change.not_pending` (another state).
  1. **Claim** (one write transaction): the change is not owned
     (`recovery_owner` empty); every effect not yet terminal has no
     dependency row (else `core.undo_conflict`); the change takes its lock
     keys again with `takeLocks` as their owner (`lock.held` naming the
     holder when another change has one, `update.in_progress` during an
     update reservation); then state `undoing`, `undo_state = running`,
     `recovery_owner = undo`, journal phase `undo`.
  2. **Reconcile**: the `intended` effects go to `Reconcile`.
  3. **Undo**: the `made` and `undoing` effects go to `Undo`.
  4. **Settle** (one write transaction, `recovery_owner` cleared):

     | Effects after steps 2–3 | Undo state | Change state | Locks |
     |---|---|---|---|
     | all `skipped`, `undone` or `accepted` | `done` | `reverted` | released |
     | any `intended`, `ambiguous` or `conflict` | `blocked` | `needs_repair` (error `core.needs_repair`, listing them) | kept |
     | `Undo` or `Reconcile` returned an error with effects still `made` or `undoing` | `failed` | `needs_repair` (the error) | kept |

  An undo request on a `reverted` change returns it (idempotent). An undo
  of a change in `needs_repair` is how a compensable change is repaired
  (1.3). Authorization: the action's scope (1.7) for user requests;
  startup recovery and composite unwind run as `system:recovery`, which the
  principal checks accept for undo, lookup and resolve. The undo state is
  the column `core_changes.undo_state` (`none`, `running`, `done`,
  `blocked`, `failed`).
- **Resolving effects.** `darkpanel change resolve <id> --effect <seq>
  <resolution>` (`POST /api/v1/changes/{id}/effects/{seq}/resolve`, relayed
  to the executing agent as
  `POST /agent/v1/changes/{id}/effects/{seq}/resolve`), scope `admin`,
  journaled as phase `resolve`, allowed only while the change is
  `needs_repair` and not owned:

  | Resolution | From | Evidence recorded | To |
  |---|---|---|---|
  | `retry` | `conflict` | the actor; the operator put the resource back | `made` (the next undo compares again) |
  | `accept` | `conflict`, `ambiguous` | the resource re-read now, into `Current` | `accepted` |
  | `made` | `ambiguous` | the resource re-read now, into `Post` | `made` |
  | `skipped` | `ambiguous` | the actor's statement | `skipped` |

  An `intended` effect left unresolved because the provider may still
  commit it is not resolvable: 1.9 settles it, or release abandons it. A
  resolution never runs an inverse; the next undo or repair does. A
  repeated identical resolution returns the effect unchanged.
- **Undo** reads the journal, not the action's output, so it works after a
  failed apply, a failed verify, a crash mid-apply and a lost response.
- **Failed apply with effects.** When Apply or Verify of an action that
  journals effects fails (including `core.deadline_exceeded`), the engine
  runs `Reconcile` on its `intended` effects under the cleanup context
  (1.3) before recording the result: no unresolved effect → `failed` with
  locks released, as today; an unresolved effect → `needs_repair`, locks
  kept, so no other change touches a resource whose state is unknown.
- **Automatic unwind** of a composite (1.3) uses only undo handlers and
  never an `irreversible` action. Operator-invoked compensation through
  `CompensatedBy` keeps today's rules.
- Each action's section states what its undo restores and what it cannot.

#### 1.3 Composite operations

A composite is a controller-side action whose apply runs **steps**: child
changes of other modules' actions on one or more hosts.

- **Declaration.**

  ```go
  const ClassComposite Class = "composite"

  // Unwind is a composite's failure direction.
  type Unwind string

  const (
      UnwindReverse Unwind = "reverse"
      UnwindForward Unwind = "forward"
  )

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
      // outputs, keyed by step id; a read step's result is not among them.
      Output(ctx context.Context, env Env, in In, steps map[string]json.RawMessage) (Out, error)
  }

  // DefineComposite declares a composite: Class composite, Side controller,
  // LockKeys ["composite:<module>"].
  func DefineComposite[In, Out any](spec ActionSpec, h Composer[In, Out]) ActionDef
  ```

  `ErasedHandler` gains `Steps` and `Output` (JSON forms of the two
  methods). The engine, not the module, plans, applies, verifies and
  recovers a composite.
- **Steps are data.** For example:

  ```json
  [{"id": "net",   "host": "h1", "action": "npm.network.attach/v1",
    "input": {"network": "${input.network}"}},
   {"id": "cert",  "host": "h1", "action": "npm.certificate.request/v1",
    "input": {"domains": ["${input.hostname}"]}, "after": ["net"]},
   {"id": "proxy", "host": "h1", "action": "npm.proxyhost.add/v1",
    "input": {"certificateId": "${steps.cert.output.id}"}, "after": ["cert"]}]
  ```

  **References.** `${input.<path>}` and `${steps.<id>.output.<path>}` are the
  only references; `<path>` is a dot-separated list of object keys and
  array indexes. A JSON string that is exactly one reference is replaced by
  the referenced value with its JSON type; a reference inside a longer
  string is replaced by the value's text, allowed only for strings and
  numbers; `$${` is a literal `${`. An `${input...}` reference is resolved
  at plan time and a missing path fails the plan with
  `request.invalid_input`; a `${steps...}` reference is resolved when its
  step is planned, against the referenced step's unredacted output (the
  sealed step row, below), and a missing path fails the step with
  `core.child_failed`. A step may reference only steps it follows through
  `after`, directly or not. The resolved input is validated against the
  child action's schema before the child is planned. The step list is
  stored, sealed, with the composite's plan.
- **Rules.** Checked by the registry where they concern the composite's
  declaration, and by the engine on every `Steps` result at plan time (a
  violation is a module bug: `internal`):
  - a step is never a composite (no nesting) and never `irreversible`;
  - a step may be a read action (for example `cloudflare.tunnel.token`, an
    `Internal` read); it is not a child change (read steps, below);
  - at most one **revertible** step, and it is the last step;
  - `reverse` (every adding composite: deploys, exposes, joins): every
    compensable step has an undo handler;
  - `forward` (every removal composite: `panel.exposure.remove`,
    `npm.expose.remove`, `npm.admin.unexpose`, `cloudflare.expose.remove`,
    `derp.server.remove`): every compensable step has an undo handler, is
    `ForwardOnly`, or is compensated by an action (`CompensatedBy`) whose
    apply converges when run again; each such action's section says so;
  - step ids are unique, `after` names earlier steps only, and every
    `${steps.<id>...}` reference is reachable through `after`;
  - the composite holds `composite:<module>` for its whole life; children
    take their own keys, and no other action declares a `composite:*` key.
- **Scope.** Composites are controller-side, and under the kept rules every
  controller mutation needs `admin` (1.7), so every composite is `admin`.
- **Preview.** The plan dry-runs every step with no `after` and no output
  reference, and describes each other step as "planned after step `<id>`",
  with its input template. Guards of pre-planned steps are the composite's
  guards. The composite's state hash is the hash of its input and its
  pre-planned steps' state hashes. The dry-run plans are not kept.
- **Durable step log.** Controller table `core_composite_steps` (composite
  change id, step id, ordinal, host, action ref, resolved input with secret
  fields sealed, current attempt, child plan id and child change id of the
  current attempt (both null for a read step), sealed unredacted output
  (`output_ct`, associated data
  `core_composite_steps.output_ct.<composite id>.<step id>`), state,
  error). The row is written and fsynced before the child is planned,
  before it is applied, and before a confirmation is sent. Step states:
  `pending → planned → applying → applied → confirming → confirmed`; on
  unwinding `undoing → undone | undo_failed`; read steps `pending → read`;
  and `released` (below). Table `core_composite_attempts` (composite change
  id, step id, attempt number from 1, child plan id, child change id,
  principal, started at, outcome) keeps every attempt's lineage. Secrets
  produced by a step reach later steps' inputs on the controller only;
  never in preview, audit or API.
- **Child results, including secrets.** A child change whose
  `parent_change_id` is set and whose output type has secret fields stores
  its unredacted output sealed in `core_changes.private_output_ct`
  (associated data `core_changes.private_output_ct.<change id>`), in the
  transaction that records its result, on its executing side. The
  composite obtains it from the apply response or, after a lost response
  or a restart, from the lookup with `private=1` (below); it seals it into
  the step row, then acknowledges it
  (`POST /agent/v1/changes/{id}/private-output/ack`, or the controller's
  own delete for a controller-side child), which clears the column. An
  unacknowledged private output is cleared after 24 hours. A step whose
  child completed but whose private output is gone and was never sealed
  into the step row is **output-lost**: a reverse composite unwinds (the
  child is undone, so an auth key is deleted, not orphaned) and fails with
  `core.child_failed`; the composite is never resumed with a redacted
  value.
- **Read steps.** A read step is executed by the controller's read dispatch
  (for an `Internal` read, the controller-only path of 1.7) once the steps
  it follows are done; it creates no plan and no change. Its result is
  sealed into the step row and is never part of the composite's output, the
  lookup's response, audit or the API. Reads have no effects: at repair a
  read step not yet `read` is read again; a `read` step's sealed result is
  reused by the steps that reference it. Unwind discards it.
- **Asynchronous apply.** Every composite, and every action marked
  `LongRunning` (section 5 marks them `L`: package installs and updates,
  image and Compose pulls, Compose operations, ACME issuance, the NPM
  bootstrap and upgrade, derper install and update), applies
  asynchronously:
  - the API's apply returns `202` as soon as the change and its intent are
    persisted, with the change in state `applying`;
  - progress and the result come from `GET /api/v1/changes/{id}` and the
    change events of the SSE endpoint wave 0 builds (1.6); `--confirm`, the
    CLI's wait and the UI countdown attach to the returned id;
  - the controller-to-agent apply of a long-running agent action likewise
    returns once the agent has persisted the intent; the controller follows
    it through the read-only lookup (below) and the agent's journal events.
  Synchronous actions keep today's behaviour.
- **Execution deadlines.** `ActionSpec.ApplyTimeout` (0 means 30 s, the
  runner's default) bounds an action's apply and verify; the engine runs
  them under a context with that deadline, and the runner keeps its own 30 s
  default only for a context without one (`internal/platform/exec.go`), so
  a long command is no longer cut at 30 s. Long-running actions declare
  theirs: package installs, updates and pins 15 min; image pulls and Compose
  operations 30 min; ACME issuance 10 min; the NPM bootstrap and upgrade
  20 min; derper install and update 15 min. A passed deadline cancels the
  command (process group killed, as today) and fails the change with
  `core.deadline_exceeded`. **Cleanup never runs on the expired context:**
  the revert, reconcile or undo that follows, and the transition that
  records the result, run under a fresh context derived from
  `context.WithoutCancel` with a timeout of the action's `ApplyTimeout`
  (handler calls) and 30 s (the database transaction). A change's locks
  are released only by a transition recorded after every handler call of
  the change has returned and, for a task action, after its unit is
  inactive (1.3c). Undo, revert, reconcile and module recovery are each
  bounded by the action's `ApplyTimeout`.
  **Composite deadlines.** A composite's steps phase is bounded by a
  deadline computed at plan time: the sum of its steps' `ApplyTimeout`
  values plus 5 min, stored with the plan. The parent never cancels a
  child: at the deadline it starts no further step, waits for the current
  child to settle under the child's own deadline, then unwinds (reverse)
  or goes `needs_repair` (forward) with `core.deadline_exceeded`. The
  confirmation window is not part of it: it is the revertible child's own
  deadline (at most 15 min, ADR 0004). Unwind and repair have no parent
  deadline; each child call is bounded by its own action's timeout.
  Tests run a real subprocess longer than 30 s inside a long-running
  action, an HTTP apply longer than the 60 s write timeout, and a deadline
  passing during a revert.
- **Agent shutdown.** On SIGTERM the agent stops accepting requests and
  cancels the contexts of in-process applies: only process shutdown does
  this; a cancelled request still never cancels a change (`Apply` runs
  under `context.WithoutCancel`, `apply.go`). Their changes are settled at
  the next start (recovery, below). Task units (1.3c) keep running.
- **Read-only lookup.** `GET /agent/v1/plans/{plan id}` (and the same
  function in-process for controller-side children) never applies
  anything and returns:

  ```go
  type PlanLookup struct {
      Status         string           `json:"status"` // "absent" | "running" | "recovering" | "terminal"
      ChangeID       string           `json:"changeId,omitempty"`
      State          string           `json:"state,omitempty"`     // the change state
      UndoState      string           `json:"undoState,omitempty"`
      Owner          string           `json:"owner,omitempty"`     // recovery_owner
      Made           int              `json:"made"`                // effects in made, undoing or conflict
      Unresolved     int              `json:"unresolved"`          // effects unresolved (1.2)
      RepairedBy     string           `json:"repairedBy,omitempty"`
      RepairOutcome  string           `json:"repairOutcome,omitempty"`
      Output         json.RawMessage  `json:"output,omitempty"`    // redacted
      Private        json.RawMessage  `json:"private,omitempty"`   // only with ?private=1, below
      Error          *ChangeError     `json:"error,omitempty"`
  }
  ```

  `absent`: no change exists for the plan. `running`: the change is
  `applying` or `undoing` and owned by the engine or undo. `recovering`: a
  module recovery handler owns it. `terminal`: every other state.
  `private=1` is honoured only on a request that arrives over the
  controller's session (never the local socket) and only while
  `private_output_ct` is set.
- **Engine integration.** ADR 0014 adds class `composite` to the shared
  contracts (`read`, `revertible`, `compensable`, `irreversible`,
  `composite`) and the additive change states `awaiting_confirmation`,
  `undoing`, `needs_repair`, `recovering`, `repaired`, `abandoned` and
  `quarantined` (`applying` exists already, `core.StateApplying`). A
  composite's terminal states are the existing `completed` (success),
  `reverted` (unwound, with error `core.child_failed` or
  `core.deadline_exceeded` naming the step) and `failed` (a reverse
  composite whose first non-read step failed with nothing to unwind), plus
  `abandoned`. `Dispatcher.Confirm` and `Revert` on a composite id act on
  its one revertible child. The CLI and UI treat a composite with a
  revertible step like a revertible action and one without like a
  compensable action.
- **Ownership of an unfinished change.** Column
  `core_changes.recovery_owner` names the one mechanism that may act on a
  change: empty (none), `engine` (an in-process apply), `composite` (the
  composite runner, controller), `undo` (an undo in progress) or `module`
  (a module recovery handler, 1.3b). Every claim is one conditional
  update, `UPDATE core_changes SET recovery_owner = ?, state = ? WHERE
  id = ? AND recovery_owner = ? AND state = ?`, and a claim that matches no
  row fails with `core.change_busy`; ownership is cleared in the same
  transaction that records the mechanism's outcome. The undo, repair,
  release and resolve routes refuse an owned change with
  `core.change_busy`.
- **Single changes in `needs_repair`.** A revertible change whose revert
  could not complete (2.5), a compensable change whose undo ended `blocked`
  or `failed`, a compensable change left with unresolved effects by
  recovery, and a change whose module recovery handler failed go to
  `needs_repair` with error `core.needs_repair` (or the handler's error)
  and **keep their lock rows**. **Repair is by capability**, through
  `darkpanel change repair <id>` (`POST /api/v1/changes/{id}/repair`):

  | The change | Repair does | Answer |
  |---|---|---|
  | compensable with an undo handler | undo (1.2), after its `conflict` and `ambiguous` effects are resolved | `202` with the change |
  | failed module recovery (`recovery_retry` set) | runs the module's `Recover` again (agent route `POST /agent/v1/changes/{id}/repair`) | `202` with the change |
  | revertible | plans its `RepairedBy` action with the core-level field `repairs: <id>` | `200` with the plan; the client applies it through the action's ordinary apply route |
  | `ForwardOnly`, `interrupted` | plans the same action with the stored input and `repairs: <id>` | `200` with the plan |

  Any other change has no repair (`change.not_pending`); release (below)
  still applies to it in `needs_repair`. `repairs` is set only by the
  repair route: a plan request that supplies it is refused with
  `request.invalid_input`, and it is stored in `core_plans.repairs`.
  - **Repair under the repaired change's locks.** Apply of a plan whose
    `repairs` names change R accepts R's lock keys as free when R owns
    them, and in the same transaction that persists the repair change P's
    intent it moves R's lock rows to P, sets `R.repaired_by = P`, and so
    the keys are never free in between. While `R.repaired_by` is set, R
    refuses undo, repair and release (`core.change_busy`). The
    `RepairedBy` action's output carries `repairOutcome`: `restored` (the
    host is back in R's before-change state) or `kept` (the host is in R's
    intended state).

    | P reaches | R becomes | Lock rows |
    |---|---|---|
    | `completed` or `confirmed` | `repaired`, with P's `repairOutcome` and a link to P | P's normal release (completion or confirmation) |
    | `failed` or `reverted` (P left nothing behind: P's own revert or undo succeeded) | stays `needs_repair`, `repaired_by` cleared | moved back from P to R in P's terminal transaction |
    | `needs_repair` | stays `needs_repair`, `repaired_by` = P | stay with P; repairing P (P2 with `repairs: P`) continues the lineage |

    When a change becomes `repaired` and it was itself repairing R, R
    becomes `repaired` too, with the outcome of the last link, recursively
    up the lineage. Release of a change that is repairing another is
    refused (`core.change_busy`); releasing the first change of the
    lineage releases every link.
  - **Release.** `darkpanel change release <id>`
    (`POST /api/v1/changes/{id}/release`, body `{"reason": "<1–500
    characters>"}`), scope `admin`, journaled as phase `release`: allowed
    only on a change in `needs_repair` that no mechanism owns and that has
    no live task unit (1.3c); refused otherwise with `core.change_busy`. In
    one transaction it marks every effect that is not terminal
    `abandoned`, moves the change (and every link of its repair lineage) to
    `abandoned` (terminal) and releases its locks. The answer (`200`) lists
    each abandoned effect with its states. On a composite it does the same
    for the composite and its `needs_repair` children; step rows go
    `released`. A repeated release of an `abandoned` change returns it.
  - **Locks across restarts and update reservations.** `Engine.Recover`
    (below) keeps the locks of changes in `applying`, `pending`,
    `awaiting_confirmation`, `undoing`, `needs_repair` and `recovering`.
    Update reservations are refused while any lock is held (ADR 0012), so
    a host with such a change refuses self-updates. `Reserve`
    (`internal/core/lifecycle/reserve.go`) keeps its codes:
    `update.in_progress` only while another operation holds `*`, and
    `update.unconfirmed_changes` while a change holds a lock or a
    revertible change is unconfirmed; wave 0 extends that refusal's detail
    to name the first held key, its owning change and the change's state
    (`lock firewall is held by change <id> (needs_repair)`), and the update
    scheduler shows that detail.
- **Confirmation (R41).** When the last step is revertible and its child
  is `pending`, the composite is `awaiting_confirmation`.
  1. Confirm writes the step row `confirming` (fsync), then sends the
     child's confirm.
  2. On the child's answer `confirmed`, or on a repeated confirm answered
     `change.not_pending` while a lookup shows the child `confirmed`, the
     step becomes `confirmed` and the composite `completed`.
  3. A lost answer is settled by the lookup, never by a deadline: a child
     found `confirmed` completes the composite whatever the time; a child
     found `reverted` is handled by the transition table below.
- **Child watcher.** While a composite is `awaiting_confirmation`, has a
  step `confirming`, has a child in `needs_repair` or `recovering`, or is
  itself `recovering`, the controller watches the child: the agent's
  journal entries of phases `apply`, `interrupt`, `revert`, `recover`,
  `undo`, `resolve` and `release` reach it through the existing audit
  ingester and wake the watcher, and the watcher also polls the lookup
  every 15 s. Each observation goes through the transition table. When the
  agent is unreachable, the watcher waits until the child's deadline (if
  any) plus 60 s, then marks the composite `recovering` and keeps polling
  every 30 s; it acts only on a child state the lookup has shown.
- **Parent and child transitions.** The composite's state is one of
  `applying`, `awaiting_confirmation`, `undoing`, `recovering`,
  `needs_repair` or a terminal state. Every observation of the current
  step's child, live or at recovery, is applied through this table (the
  child's view is the lookup's `Status`, `State`, `Made` and
  `Unresolved`):

  | Child observed | Reverse composite | Forward composite |
  |---|---|---|
  | `absent` (the apply never reached the executing side) | step back to `pending`; live: apply a new attempt (re-planned); at recovery: unwind | step back to `pending`; live: new attempt; at recovery: `needs_repair` at this step |
  | `running` | wait | wait |
  | `recovering` | wait for the module outcome, then this table again | same |
  | `completed` (compensable) | step `applied`; next step, or `completed` when it was the last | same |
  | `pending` (revertible, the last step) | step `applied`; `awaiting_confirmation` | same |
  | `confirmed` | step `confirmed`; `completed` | same |
  | `reverted` (a revertible child by timer, operator or restart; a compensable child undone or rolled back by its module) | unwind the steps before it | `needs_repair` at this step; completed steps stay done; repair makes a new attempt |
  | `failed` or `interrupted`, `Made` 0, `Unresolved` 0 | unwind the steps before it (`failed` when there are none) | `needs_repair` at this step |
  | `failed` or `interrupted`, `Made` > 0, `Unresolved` 0 | unwind, starting with this step | `needs_repair` at this step; repair settles the child by capability (below) |
  | any state with `Unresolved` > 0 | `needs_repair`; nothing is unwound past this step and no new attempt is made until the child's effects are resolved | same |
  | `needs_repair` | `needs_repair`; unwind waits at this step | `needs_repair` at this step |
  | `repaired`, `repairOutcome: restored` | as `reverted` | as `reverted` |
  | `repaired`, `repairOutcome: kept` | as `completed` or `confirmed` (by class) | same |
  | `abandoned` (released on its own) | `needs_repair`; `darkpanel change repair <composite id> --step <id> --as reverted\|applied` (admin, audited) is the only way on | same |
  | completed, but its private output is lost (above) | unwind, starting with this step; `core.child_failed` | — (removals have no secret outputs) |
  | unreachable | wait; after the deadline plus 60 s, `recovering` | same |

  During unwind, a step's undo observed `failed` or `blocked` leaves the
  step `undo_failed` and the composite `needs_repair`. A composite in
  `needs_repair` or `recovering` keeps `composite:<module>`; its children
  keep the locks their own states keep.
- **Unwind** (reverse composites) settles steps from the current one
  backwards, each by its class:
  - read step: nothing;
  - revertible step (always the last): settled **first**, through the
    confirm/revert arbitration: a `pending` child is sent `Revert` and the
    unwind waits until the lookup shows it `reverted`; a child found
    `confirmed` stops the unwind and the composite is `completed`
    (confirmation won); an unreachable agent leaves the child to its own
    timer and the composite `recovering`; a child in `needs_repair` stops
    the unwind. Prerequisites are undone only after the revertible child is
    `reverted`, `repaired` with outcome `restored`, or `absent`;
  - compensable step: undone through the undo route (1.2), including a
    child the agent marked `interrupted`; a step whose lookup says
    `running` or `recovering` is waited for first; a step whose lookup says
    `absent` was never applied; a child with unresolved effects stops the
    unwind (`needs_repair`), since its undo would end `blocked`.

  When every step is settled the composite is `reverted`. `darkpanel change
  repair <composite id>` resumes a stopped unwind once its blocking child is
  settled.
- **Repair of a composite** (`darkpanel change repair <composite id>`,
  scope `admin`; the composite is claimed with owner `composite`). The
  current step's last attempt is settled first through the lookup, by the
  child's capability:
  - `running` or `recovering`: wait;
  - `completed`, `pending`, `confirmed`, or `repaired`: as in the table;
  - `failed` or `interrupted` with `Made` > 0: a child with an undo handler
    is undone (a blocked undo keeps the composite `needs_repair`); a
    `ForwardOnly` or `CompensatedBy` child is not undone: its Verify is run
    (read-only) and, if it passes, the step is `applied`, else a new
    attempt re-applies it (such actions converge, rules above); a child
    with a module recovery handler was settled by that handler before
    repair could claim it;
  - `absent`, or `failed` with `Made` 0: nothing to settle.

  Then a **new attempt**: the step's input is resolved again from the step
  log, the step is planned afresh by the repairing principal and applied
  as a new child change; the attempt is recorded in
  `core_composite_attempts` and the step row points at it. A stored child
  plan is never applied after a restart or by another principal (plans
  expire after 10 minutes and are bound to their principal,
  `internal/core/lifecycle/apply.go`); stored plan ids serve only the
  lookup. A child change's actor is the principal that planned its attempt
  (the composite's principal for the first), with `parent_change_id` set.
  Recovery never makes a new attempt on its own: new attempts come from a
  live run (an `absent` child after a lost apply request) or from an
  operator's repair.
- **Composite recovery** (controller start, 1.3a), for every composite not
  in a terminal state, claimed with owner `composite`, by its step log and
  the lookup:

  | Persisted state | Action |
  |---|---|
  | current step `pending` (child never planned) | reverse: unwind the steps before it (`failed` when none were applied); forward: `needs_repair` |
  | current step `planned` or `applying` | look the child up and apply the transition table "at recovery" |
  | current read step `pending` | reverse: unwind; forward: `needs_repair` |
  | every step `applied` or `read`, no revertible step | `completed` (reverse and forward) |
  | every step `applied`, the last revertible with its child `pending` | `awaiting_confirmation`; resume the watcher |
  | `awaiting_confirmation` | resume the watcher; never unwind on the deadline alone |
  | step `confirming` | look the child up: `confirmed` → `completed`; `pending` → resend confirm; otherwise the table |
  | `undoing` | resume the unwind from the step rows; each undo resumes through the undo route |
  | `recovering` | resume polling the current child |
  | `needs_repair` | leave for the operator; the watcher keeps following children in `needs_repair` or `recovering` |

  This refines ADR 0004 for composites only: the controller unwinds
  automatically because every undo acts only on journaled effects and
  checks post-state first.
- **Change recovery at start** (`Engine.Recover`, both sides; ADR 0004's
  pass, extended). Run once before anything else is served:
  1. Seed ids, as today.
  2. For every change in `applying` or `pending`:
     - class `composite`: skipped; composite recovery owns it (1.3a);
     - the action has a recovery handler (1.3b; every task action has
       one): claim it with state `recovering`, owner `module`; its locks
       stay;
     - revertible: `Reconcile` its `intended` effects if it reconciles,
       then revert from its snapshot as today (`Revert` decides per 2.5);
       a revert that succeeds → `reverted`, locks released; a revert that
       fails → `needs_repair` (no longer `failed`), locks kept;
     - compensable with an undo handler: `Reconcile` its `intended`
       effects; no unresolved effect → `interrupted` with the verify message
       as today, locks released; otherwise `needs_repair`, locks kept;
     - every other compensable or irreversible change: `interrupted`, as
       today.
  3. Every change in `recovering` with owner `module` (a handler cut off by
     the restart) stays claimed; every change in `needs_repair` with
     `recovery_retry` set is claimed again (state `recovering`, owner
     `module`).
  4. The lock sweep deletes the locks of every change not in `applying`,
     `pending`, `awaiting_confirmation`, `undoing`, `needs_repair` or
     `recovering`; reservations (`op:` owners) are untouched, as today.

  Then **admission**: the agent serves its socket and dials the
  controller, so reads, `hello`, the lookup, journal sync and streams work,
  while plan, apply, undo, repair, release and resolve answer
  `core.recovering` until the background pass has resumed every undo in
  `undoing` (owner `undo`) and started every module recovery handler. A
  handler that takes long (a task unit still running) does not hold
  admission closed: its change keeps its locks, so conflicting work is
  refused with `lock.held`. This order lets a task unit's health check,
  which needs the agent's controller session (4.1), complete while its
  change is still recovering. The controller follows the same pattern
  (1.3a).
- **1.3a Controller start order.** The controller runs `Engine.Recover`,
  then opens its agent listener and serves agent traffic and reads, then
  runs composite recovery in the background; user and token mutations are
  refused with `core.recovering` until composite recovery has finished or
  has left only `recovering` and `awaiting_confirmation` composites, which
  it keeps following. A controller that started in **restore quarantine**
  (10) performs no recovery action and starts no automatic mutation source
  until the operator releases it.
- **1.3b Module recovery handlers.** Some actions keep a durable transaction
  of their own and must finish or roll back automatically after a crash:
  `npm.instance.upgrade` (6), `derp.binary.update` (4.3),
  `tailscale.package.pin` (4.1), Cloudflare hostname changes (7.3), and
  every task action (1.3c). Such an action's handler implements

  ```go
  // RecoveryOutcome is a recovery handler's verdict on its change.
  type RecoveryOutcome struct {
      State  string          // "completed" | "failed" | "rolled_back" | "needs_repair"
      Output json.RawMessage // completed: the action's output
      Reason string          // failed, needs_repair: what happened, shown to the operator
  }

  // Recoverer finishes or rolls back an action's own transaction after a
  // crash. env.ChangeID is the change; env.Effects its journal.
  type Recoverer interface {
      Recover(ctx context.Context, env Env) (RecoveryOutcome, error)
  }
  ```

  found by a type assertion in `DefineMutating`, `DefineRevertible` and
  `DefineTask` (`ErasedHandler.Recover`). Contract:
  - **Ownership.** The handler runs only on a change claimed with owner
    `module` (recovery step 2 or 3, or repair); undo recovery skips such
    changes; the routes refuse them with `core.change_busy`; the lookup
    reports `recovering`.
  - **Locks.** The handler runs as the change's lock owner; it takes no
    lock; the change keeps its lock rows until the engine records the
    outcome.
  - **Outcome.** The engine, not the handler, records the transition and
    clears the owner, in one transaction: `completed` → `completed` with
    the output stored (redacted as usual, private output as above);
    `failed` → `failed` (the handler found nothing made; locks released);
    `rolled_back` → `reverted` (every effect `undone` or `skipped`; locks
    released); `needs_repair` → `needs_repair` with the reason, locks kept.
    A returned error, or a handler still running when its `ApplyTimeout`
    passes, is recorded as `needs_repair` with `recovery_retry` set, locks
    kept, and is claimed again at the next start or by `change repair`.
    Before recording `completed` or `failed` the engine checks that no
    effect is unresolved; if one is, it records `needs_repair` instead.
  - It is idempotent; it reads only its own transaction table and effects
    journal; a failing handler does not stop other handlers or startup.

  ADR 0014 records this as the one exception to ADR 0004's "never retried on
  its own" for compensable and irreversible actions. Verify stays
  read-only; no recovery runs inside Verify.
- **1.3c Task units.** The apt, dpkg and binary-swap sequences of
  `tailscale.package.install`, `package.update`, `package.pin`,
  `package.unpin`, `derp.server.install` and `derp.binary.update` must not
  be cut by an agent restart, including one the action itself causes, so
  they run in a **transient systemd unit** outside the agent's cgroup.
  - **Declaration.**

    ```go
    // TaskMutator is the handler of a task action: Task runs inside the
    // unit, in a separate process, and records each phase in the module's
    // transaction table; Plan and Verify run in the agent as for any
    // mutating action.
    type TaskMutator[In, Out any] interface {
        Plan(ctx context.Context, env Env, in In) (PlanOutcome, error)
        Task(ctx context.Context, env Env, in In) (Out, error)
        Verify(ctx context.Context, env Env, in In, out Out) error
    }

    // DefineTask declares a task action: spec.Class compensable or
    // irreversible, Side agent, LockKeys non-empty, ApplyTimeout set; it
    // sets Task and LongRunning. Its recovery handler is the handler's own
    // Recoverer, or core.TaskRecover when it has none.
    func DefineTask[In, Out any](spec ActionSpec, h TaskMutator[In, Out]) ActionDef

    // TaskRecover waits for the change's unit and records its result row
    // as the outcome (completed after a passing Verify; failed when nothing
    // was made; needs_repair otherwise).
    func TaskRecover(ctx context.Context, env Env) (RecoveryOutcome, error)
    ```

    `ErasedHandler` gains `Task` (the JSON form); a task action's
    `ErasedHandler.Apply` is nil and the registry checks `Task` is set
    instead. A task action with an undo handler implements `Undoer` as
    well.
  - **Unit.** Name `darkpanel-task-<change id>.service`, the only name a
    task unit has (4.1 and 4.3 use it). Started with
    `platform.Systemd.StartTransientService` over D-Bus (mode `fail`, so a
    second start of the same name fails), `ExecStart` `{{.Binary}} agent
    task <change id>`, `KillMode` the default, no `OnFailure`, and
    `RuntimeMaxSec` the action's `ApplyTimeout` (wave 0 adds
    `RuntimeMax time.Duration` to `platform.TransientService`).
  - **The `agent task` subcommand** (hidden): opens the agent database
    (WAL with `busy_timeout`, `internal/core/store/db.go`, so it runs
    beside the agent), the agent's sealer from the agent's key files, the
    production runner with the same exec allowlist, the file writer with
    the registry's owned paths, the system clock and a journald logger. It
    reads the change: if it is not `applying` or `recovering`, or its
    `core_task_runs` row is not `starting`, it exits without doing anything.
    It builds `Env{HostID, ChangeID, Principal: {Actor: <the change's
    actor>, Scope: admin}, DB, Exec, Files, Sealer, Clock, Log, Effects}`
    (`Agents` nil), opens the plan's sealed input (still present while the
    change is unfinished), marks the row `running` with its PID, calls
    `Task`, and records the result in the row: `succeeded` with the
    output sealed (associated data `core_task_runs.output_ct.<change id>`)
    or `failed` with the error's code and client-safe message. It never
    changes the change's state; it exits 0 once the row is recorded and
    non-zero only when it could not record it.
  - **Results table** `core_task_runs` (change id, state `starting |
    running | succeeded | failed`, output_ct, error code, error message,
    PID, started at, finished at), agent side.
  - **Apply side.** The intent transaction also inserts the row
    `starting`. The engine then starts the unit; a start error fails the
    change (the row goes `failed`, nothing ran). It then waits, under the
    same rules as any long-running apply: every 2 s it reads the row and
    `Systemd.State` of the unit. A `succeeded` row → Verify → the change
    completes with the stored output. A `failed` row → the effects are
    reconciled → `failed`, or `needs_repair` when an effect is unresolved.
    A unit that is `inactive`, `failed` or `not-found` while the row is
    still `starting` or `running` → the same as `failed`, with
    `internal` "the task ended without recording a result". The unit's
    `RuntimeMaxSec` enforces the deadline; the engine records
    `core.deadline_exceeded` only after it has seen the unit inactive.
  - **Agent restart.** The waiter dies with the agent; the unit keeps
    running. At the next start the change is claimed by its recovery
    handler (recovery step 2), which waits for the unit with the same loop.
    The agent serves its session meanwhile (admission, above).
  - **Liveness gates.** A task change's locks are released only by a
    transition recorded after its unit was seen inactive or not found.
    Every task action declares lock keys, so an update reservation is
    refused while its change is unfinished. Uninstall (1.11) refuses with
    `core.change_busy`, naming the changes, while any change is `applying`,
    `recovering` or `undoing` or any `darkpanel-task-*.service` unit is
    active; `darkpanel uninstall --stop-tasks` first stops each such unit
    through D-Bus, waits until it is inactive, records each change as its
    recovery handler decides, and only then proceeds.
- **Records.** Each non-read step's attempt is an ordinary change with
  `parent_change_id` set.
- **Wire contract.** All routes are non-frozen and use ADR 0006's error
  mapping; every answer that carries a change uses `ChangeView`, which
  gains `ParentChangeID`, `UndoState`, `Owner` and `RepairedBy`.

  | Route | Request | Answer |
  |---|---|---|
  | `POST /api/v1/changes/{id}/undo`, agent `POST /agent/v1/changes/{id}/undo` | empty | `202` change (`undoing`), `200` when already `reverted` |
  | `POST /api/v1/changes/{id}/repair` | `{}`, or for a composite step decision `{"step": "<id>", "as": "reverted" \| "applied"}` | by capability (table above): `202` change or `200` plan |
  | agent `POST /agent/v1/changes/{id}/repair` | empty | `202` change: reruns the module recovery handler |
  | `POST /api/v1/changes/{id}/release`, agent `POST /agent/v1/changes/{id}/release` | `{"reason": string}` | `200` `{"change": ChangeView, "abandoned": [EffectView]}` |
  | `GET /api/v1/changes/{id}/effects`, agent `GET /agent/v1/changes/{id}/effects` | — | `200` `{"effects": [EffectView]}` |
  | `POST /api/v1/changes/{id}/effects/{seq}/resolve`, agent `POST /agent/v1/changes/{id}/effects/{seq}/resolve` | `{"resolution": "retry" \| "accept" \| "made" \| "skipped"}` | `200` EffectView |
  | agent `GET /agent/v1/plans/{plan id}` | query `private=1` (controller session only) | `200` `PlanLookup` |
  | agent `POST /agent/v1/changes/{id}/private-output/ack` | empty | `204` |

  `EffectView` is `{seq, kind, resource, state, before, after, post,
  undoTo, current, resolution, resolvedBy, resolvedAt}`: the `Effect`
  fields without the change id and never the secret. Scopes: undo and
  repair need the action's scope (composites `admin`); release and resolve
  need `admin`; the effects list needs `read`. The CLI gains `darkpanel
  change undo|repair|release|resolve|effects <id>`; the UI offers Undo on
  interrupted, failed and completed compensable changes with an undo
  handler, and Repair, Release and per-effect resolution on
  `needs_repair` changes.
- **Error codes** returned by this section: `core.undo_conflict`
  (supersession), `core.change_busy` (an owned change, a repair in
  progress, a live task unit), `core.needs_repair` (the error recorded on a
  change entering `needs_repair`), `core.child_failed` (the error of a
  composite that unwound or failed because of a step, naming the step),
  `core.recovering` (admission closed at start), `core.deadline_exceeded`.
- **Tests.** Fault injection at every state transition: controller killed
  before and after each step's apply, before and after a confirmation is
  sent and committed, agent unreachable during unwind and confirmation, undo
  failure and conflict, a crash between an inverse and its `undone` record,
  a crash inside each creation and modification effect (before the call,
  after the call before the result is recorded), a forward composite whose
  revertible step reverts, a composite whose every step applied before the
  crash (completed, not unwound), a child in `needs_repair` under a
  composite, a repair whose own revert fails, a released child under a
  composite, repair by a different admin after the plan TTL, a module
  recovery handler racing an undo request, release refused while a task
  unit runs, a lost child response carrying a secret output, a task unit
  outliving an agent restart and its deadline, deadline passing while the
  controller is down, and the long-running path across the 60 s write
  timeout.

#### 1.4 Agent background tasks and module enablement

- **Enablement on the agent.** New, non-frozen agent route
  `PUT /agent/v1/modules/{id}/enabled` with `{enabled: bool}`. The controller
  calls it when a module is enabled or disabled and after each `hello`; the
  agent persists it in `core_meta`. Actions on the local socket keep their
  current rule (enablement is not applied there).
- **Interface.**

  ```go
  type BackgroundTask struct {
      ID    string        // "firewall.reconcile"; audit actor system:<ID>
      Every time.Duration // 0: event-driven only
      Run   func(ctx context.Context, env Env, wake <-chan struct{}) error
  }
  type Background interface{ BackgroundTasks() []BackgroundTask }
  ```

- The runtime starts a module's tasks after startup recovery, only while the
  module's agent-side enabled flag is set and its preflight passes; clearing
  either cancels them. A returned error restarts the task with backoff from
  1 s to 5 min.
- A task mutates only by planning and applying one of its module's actions
  through the lifecycle engine, as actor `system:<ID>`, with the action's
  locks, snapshot and journal. Such a change is confirmed in the same step
  and marked `system`, so rollbacks can skip it (2.5).
- A task skips its run, without error, while any of its lock keys is held,
  and runs again on the next wake.
- A task's plans carry no controller guard context; their guards evaluate
  the agent's own live facts, as break-glass plans do (1.5).
- Event subscriptions (Docker events) reconnect with the same backoff, and a
  reconnect triggers a full run.

#### 1.5 Guard context

Lockout guards (2.3, 4.1) need the paths that must survive a change.

- **Plan.** Every action that declares guards has an input field `guard` of
  type `GuardContext`. The dispatcher fills it; the API rejects a request
  that supplies `guard` (`request.invalid_input`). As an ordinary schema field
  it is covered by ADR 0002's rule that the controller never sends a field
  the agent's schema lacks, so an agent without guard support cannot receive
  a guarded action.

  ```go
  type GuardContext struct {
      Client      *Peer       // the browser or CLI as the controller's transport saw it
      Via         *ProxyPath  // set when the request came through an exposure (8)
      PanelListen []AddrPort  // the controller's web and runtime listeners, on the controller's host
      AgentListen []AddrPort  // the controller's agent listeners, on the controller's host
      AgentPeers  []Addr      // live agents' source addresses, on the controller's host
  }
  type Peer struct{ Addr Addr; Port uint16 }
  type ProxyPath struct {
      Kind         string    // npm | tunnel
      ProxyAddr    Addr      // the proxy container's address on the panel network
      ListenerAddr AddrPort  // the runtime listener
      Public       []AddrPort
  }
  ```

- **Apply.** The apply request carries a core-level `guardRefresh` (same
  type, not part of the action's input): the controller's topology at apply
  time. The agent evaluates the union of the plan's `guard`, `guardRefresh`
  and its own live facts (its session's local and remote address, established
  SSH sessions, interface addresses, routes). Apply refuses if a flow that
  passed at plan now fails or a new protected flow (a new SSH session, a new
  agent, a new listener) fails.
- **Local break-glass** plans and applies, and plans made by background
  tasks as `system:*` actors (1.4), have only the agent's own facts. When a
  `darkpanel-controller` process runs on the agent's host (found in the
  socket table, 1.8, by the unit's main PID through D-Bus), the agent adds
  its listening sockets as protected flows (direct panel, runtime and agent
  listeners) and its established agent sessions' peers, so a `--local`
  change on the controller's host still protects the panel and the agent
  links.
- **Retired paths.** A composite that removes an exposure may, once the
  step revoking its listener has committed, record that exposure as
  retired in its step log; the guard context it passes to its later
  children then omits that exposure's `Via` path (protected flow 3, 2.3)
  and nothing else. Only the parent composite of that removal can retire a
  path (8).
- **Overrides.** Guarded actions have an input field `overrides: []string`
  (guard ids). `core.Guard` gains the additive fields `Overridable` and
  `Overridden`; an overridden guard reports `OK: true, Overridden: true`, and
  the plan, diff and audit entry record it. Only the lockout guards of 2.3
  and 4.1 are overridable.

#### 1.6 Streams

- **Discovery.** New, non-frozen agent route `GET /agent/v1/streams` lists
  the agent's streams and their schema hashes. `hello` is not changed (it is
  frozen for protocol 1). A 404 means an agent without streams.
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
  arrived on is revoked (1.10).
- **CLI.** `darkpanel <module> <name> --follow` reads the SSE endpoint with
  its own streaming client.
- Streams in v1: `docker.logs`, `docker.stats`, `docker.pull`.

#### 1.7 Per-action availability, scope and internal actions

`ActionSpec` gains three additive fields:

- `Available func(ctx, env) error`: per-action availability. For agent-side
  actions it runs on the agent: new, non-frozen route
  `GET /agent/v1/modules/{id}/availability` returns the module's preflight
  result and each action's availability with the reason. The controller
  caches the answer per `hello` and per module enable, re-asks before
  dispatching an action whose cached answer is older than 60 s, and refuses
  an unavailable action with `module.unavailable` and the reason; the UI
  greys it out from the cached answer. Module preflight stays module-wide.
- `MinScope`: can only **raise** the scope the existing rules require; it
  never lowers one. Today's rules are kept unchanged: read actions need
  `read`, agent mutations `operate`, controller-side mutations `admin`
  (`core.token.create` therefore stays `admin`). Raised in this spec:
  Compose actions, every `reveal` action, `docker.container.reveal-env`,
  `npm.admin.*` and `panel.*` need `admin`. A composite needs the highest of
  its own and every step's scope (1.3). No action's scope is lowered.
- `Internal`: the action is not exposed through the API or CLI and is
  dispatched only by the controller itself (`tailscale.cert.get`). Internal
  reads travel through a controller-only dispatch path that returns the
  output unredacted: never through `Engine.Read`, the API, the journal or the
  SSE relay. On the agent, the read handler for an `Internal` action answers
  only the controller's session (never the local socket) and does not apply
  `Redact`; each call is audited like secret delivery (1.14): actor, action
  and the secret's identity, never the value.

#### 1.8 Host clients (`internal/platform`)

- **Exec allowlist** with a per-path first-argument allowlist (new runner
  feature) and a per-path environment allowlist; nothing else is inherited:

  | Path | First arguments | Extra environment |
  |---|---|---|
  | `/usr/sbin/ufw` | any (values constrained by schema patterns, 2.2) | — |
  | `/usr/sbin/iptables`, `/usr/sbin/ip6tables` | `--version` | — |
  | `/usr/sbin/{iptables,ip6tables}-save`, `-legacy-save`, `-nft-save` | `-t` | — |
  | `/usr/sbin/{iptables,ip6tables}-restore`, `-legacy-restore`, `-nft-restore` | `-n` (input on stdin) | — |
  | `/usr/bin/apt-get` | `update`, `install`, `remove` (options follow the verb, which apt-get accepts: `apt-get <verb> -o DPkg::Lock::Timeout=120 -o Dir::Etc::sourcelist=<file> -o Dir::Etc::sourceparts=- -y [--allow-downgrades] [--download-only] [--reinstall] <package>[=<version>]`, or for `install` a `.deb` path inside the module's managed directory, 4.1) | `DEBIAN_FRONTEND=noninteractive` |
  | `/usr/bin/apt-mark` | `hold`, `unhold`, `showhold` | — |
  | `/usr/bin/dpkg` | `--configure` (exactly `--configure -a`; used only by module recovery handlers after an interrupted install) | `DEBIAN_FRONTEND=noninteractive` |
  | `/usr/sbin/nft` | `list` (exactly `list tables` or `list table <family> <name>`; read-only, 2.1) | — |
  | `/usr/bin/docker` | `compose` | `DOCKER_CONFIG`, `HOME`, and always `COMPOSE_EXPERIMENTAL_GIT_REMOTE=false`, `COMPOSE_EXPERIMENTAL_OCI_REMOTE=false` (3) |

  The runner gains stdin input for the restore tools. `ufw` has no `--`
  separator, so every value passed to it is constrained by schema patterns
  alone. `systemctl` is not allowed; systemd stays on D-Bus (ADR 0012).
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
  as an opaque rule the evaluator treats as unsupported (2.3). The evaluator that walks them is built in wave 1 (2.3),
  after its golden files exist. Fuzzed (R74).
- **Restore writes.** `FileWriter.Restore(module string, e RestoreEntry)`
  with `RestoreEntry{Path, Present, Mode, UID, GID, Data}`: writes the file
  with that metadata (temporary file, fsync, rename, directory fsync) or,
  when `Present` is false, removes it (and fsyncs the directory). Allowed
  only on paths the module declares with the owned-path kind `restore`;
  uninstall never removes a `restore` path. `Restore` is for regular files
  only.
- **Kernel parameters.** New owned-path kind `sysctl`: a module declares
  each kernel parameter it may write, as a key (`net/ipv4/ip_forward`) with
  a value pattern (`^[01]$`). `FileWriter.ReadSysctl(module, key)` and
  `FileWriter.WriteSysctl(module, key, value)` refuse an undeclared key or a
  value outside its pattern; they open `/proc/sys` once, check with
  `fstatfs` that it is procfs, open the key beneath it with
  `openat2(RESOLVE_BENEATH | RESOLVE_NO_SYMLINKS)`, write the value with a
  single `write` (no temporary file or rename: procfs allows neither), then
  read it back and fail if it differs. Uninstall never touches a `sysctl`
  key. The firewall declares the keys of 2.1.
- **Managed directories.** New owned-path kind `managed-dir`: a fixed,
  root-owned directory in which the module may create and delete **regular
  files directly inside it** (no subdirectories), with names matching
  `^[a-z0-9][a-z0-9._-]{0,63}$`, through
  `FileWriter.WriteManaged(module, dir, name, data, mode, uid, gid)` and
  `RemoveManaged`; the directory itself is opened with the same symlink and
  ownership checks as every file write (ADR 0009); uninstall removes the
  directory and its contents. Used for tunnel token files (flat names
  `<tunnel id>.token`, 7.2) and NPM backup archives (6). Restore staging is
  not a managed directory: the root restore CLI writes it itself, with the
  services stopped (10).
- **Darkpanel-owned Docker objects.** Containers, networks and volumes a
  module creates carry the label `darkpanel.module=<id>`. They are that
  module's resources (ADR 0002), driven through `dockerapi` by the module's
  own actions; generic `docker.*` mutating actions and Compose operations
  refuse them with `docker.owned` (3).

#### 1.9 Cloud plumbing (controller)

- `internal/controller/cloud`: one HTTP client with 15 s timeouts. Retries
  (at most 3, backoff, honouring `Retry-After`) apply to `GET` and `DELETE`
  only. Every write has a declared **uncertain-outcome** procedure, run when
  the request fails with a 5xx or a transport error after it may have
  reached the provider; a write is never re-sent blindly:

  | Write | Procedure |
  |---|---|
  | Tailscale auth key create | no immediate retry (the provider gives no guarantee that a request which timed out cannot still commit, so "none found" proves nothing). The effect stays `intended`, the step fails with `cloud.uncertain_outcome`, and the uncertain creation is kept in `tailnet_uncertain_keys` (change id, description, request time, requested expiry). A sweep lists keys and deletes each whose description is `darkpanel-<change id>` of a kept row (a key's secret cannot be recovered): every 5 minutes until the request time plus the requested expiry plus 24 hours, then once a day while the account is connected. Independently, a daily orphan sweep deletes any key whose description starts with `darkpanel-` and names a change id with no live key record, so a key committed however late is removed while the account stays connected. A retry is a new change with a new description |
  | Tailscale policy `POST` (always sent with `If-Match: <ETag read at plan>`) | re-`GET` with HuJSON: equal to the new text → `made`; neither the old nor the new text → `cloud.conflict`; equal to the old text and ETag → still uncertain, because the timed-out write may commit later, but only while the old ETag is current. Darkpanel settles it by **retiring the old ETag**: it writes the old text with one trailing comment line `// darkpanel-settled: <change id>` (replacing any earlier such line) with `If-Match: <old ETag>`. Success makes the delayed write impossible (its `If-Match` can no longer match) → `skipped`; a 412 means another write (possibly the delayed one) won → re-`GET` and compare again; a timeout of the settling write is settled the same way. Until settled, the effect stays `intended`, the change fails with `cloud.uncertain_outcome`, and no step before it is unwound |
  | Tailscale device writes | re-read the device and compare |
  | Cloudflare tunnel create | look up by name `darkpanel-<change id>` |
  | Cloudflare configuration `PUT` | re-`GET` and compare with the body sent; equal → applied. This detects only a configuration that differs afterwards: a concurrent write that Darkpanel's `PUT` overwrote is undetectable (7.1) |
  | Cloudflare DNS record create | list records with the name and the comment `darkpanel:<change id>` |

  Unresolvable outcomes fail with `cloud.uncertain_outcome` and leave the
  change for repair.
- **Accounts.** Each module stores credentials in its own table with the
  secret column sealed (ADR 0005). Credentials are write-only through the API
  except through secret delivery (1.14). An account's disconnect is refused
  with `cloud.cleanup_pending` while it has an unsettled uncertain write or
  an auth-key sweep still in its 5-minute phase, because the credentials are
  needed to finish them; `--abandon-cleanup` (admin, audited, listing what
  is abandoned) overrides it.
- **Capability probes.** Connecting an account probes read capabilities with
  harmless reads and reports them; write capabilities are reported as
  unverified until the first write succeeds, and a 403 on a write is reported
  as `cloud.permission_missing` naming the permission.
- **Fakes.** `internal/controller/cloud/cloudfake` provides shared test
  scaffolding (recorded request log, fault injection: 429, 5xx, lost
  response after commit). Each module's fake server lives in its own package
  (`internal/modules/<id>/fake`), built by the module's wave.

#### 1.10 Trusted proxies and runtime listeners

- **Runtime listeners.** The controller opens and closes listeners recorded
  in `core_listeners` (id, address, port, allowed peer address, client
  header, created by change, state `open | revoked | closed`) without a
  restart. They are HTTPS only, with the web certificate authority, and
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
  `docs/superpowers/distilled/constraints.md`, wave 0 adds a
  session-resumption test with shared ticket keys.
- Requests through a runtime listener get `X-Accel-Buffering: no` on SSE
  responses, and the CSRF origin check accepts the exposure's public host
  name (8).

#### 1.11 Uninstall options

`darkpanel uninstall` gains `--module-option <module>.<key>=<value>`
(repeatable), passed to the module's `Uninstaller` as a map; the plan lists
each module's options and their defaults. The firewall uses
`firewall.rules=remove|keep|baseline` (2.9).

#### 1.12 Module skeletons, extension points and shared files

Wave 0 creates every new module package with an empty manifest and registers
it: `firewall`, `docker`, `tailscale`, `tailnet`, `derp`, `npm`,
`cloudflare`, `panel`. Every module is default-enabled; preflight decides
whether it is available on a host (R9). Wave 0 also adds:

- every error code and dependency of section 11;
- `web/src/modules/registry.ts` entries that import each module's
  `web/src/modules/<id>/index.ts` (a stub until its wave), and in
  `web/src/modules/types.ts` a typed **container action slot** that module
  entries may fill (firewall's Allow and Deny, NPM's Expose, Cloudflare's
  Expose via tunnel) and a **host-create extension** for the Hosts dialog
  (used by the tailnet join, 4.2);
- `web/src/components/change-dialog.tsx`: plan diff, guard results, a
  per-guard override control for guards marked `Overridable`, typed
  confirmation for irreversible actions, the revert countdown, and composite
  step progress;
- the sidebar grouping in `web/src/shell.tsx` (host pages under each host,
  global pages Tailnet, DERP, Cloudflare);
- in `internal/controller/listen`, a **certificate source** interface and an
  **extra names** hook for the web leaf certificate, so wave 4 can add the
  `tailscale cert` source and the MagicDNS name in its own file;
- a `fuzz` target in the `Makefile` that runs `scripts/fuzz.sh` (the script
  belongs to wave 9a);
- **command assembly hooks:** a CLI registration hook in `internal/cli`
  (each module's command files register their commands; `internal/cli/
  controller.go` registers the controller subcommands added later, such as
  backup and restore) and a path-selection hook for `paths.Paths` in
  `cmd/darkpanel`, which harness builds use for `--state-dir` (9); later
  waves add command files only;
- **server assembly hooks** in `internal/controller`: a route-registration
  hook, `api.RegisterRoutes(surface api.Surface, fn func(*http.ServeMux))`,
  where `Surface` is `web` (session or token, CSRF, the 60 s write timeout)
  or `socket` (the controller socket's peer-uid check, today's
  `SocketHandler` mux in `internal/controller/api/socket.go`); and a
  service-registration hook, `server.RegisterService(name string, start
  func(ctx context.Context, deps server.Deps) error)`, for long-lived
  controller services, each started after composite recovery and only
  outside restore quarantine (10). Wave 0 registers its own routes and
  services through them; later waves register theirs from their own files.
  Wave 7 uses them for `POST /api/v1/controller/backup` on the socket
  surface (streams the encrypted file as `application/octet-stream`, a 30 s
  deadline per write instead of the 60 s total) and the quarantine routes
  `GET /api/v1/controller/quarantine`,
  `POST /api/v1/controller/quarantine/{record}/resolve`,
  `POST /api/v1/controller/quarantine/enroll-token` and
  `POST /api/v1/controller/quarantine/release` on the socket surface (10);
  modules use the service hook for the auth-key sweeps (1.9) and the
  Cloudflare hostname driver (7.3);
- the **quarantine gate**: the `restore_quarantine` flag in `core_meta`,
  checked before every action apply, user or token mutation (refused with
  `core.quarantined`), composite recovery, the child watcher, module
  recovery handlers, the update scheduler and rollout driver, and every
  service started through the service hook (10); wave 7 only sets the flag
  and adds the quarantine commands;
- harness helpers: per-test VM names, work directories and network ranges
  derived from the test name, a VM concurrency limit, and the Compose
  plugin, Pebble and `pebble-challtestsrv` in `test/harness/pins.go` and
  `provision.go`.

Later waves never edit these files. A wave that finds a missing code,
dependency or extension point stops, and the integrator adds it on `main`
before the wave continues.

#### 1.13 CLI confirmation and overrides

The existing contract stays: `--yes` answers the "Apply?" question only;
`--confirm` confirms a revertible change (or a composite with a revertible
step) as soon as verify passes; without it the CLI prints the change id and
deadline, and `darkpanel change confirm <id>` confirms. Overridable guards
are overridden with `--override <guard id>` (repeatable), which re-plans with
the ids in `overrides`; every other guard is a hard refusal.

#### 1.14 Secret delivery

A `reveal` action (class `read`, `MinScope: admin`) returns one stored
secret once, in the response body only: the audit entry records the actor,
the action and the secret's identity, never the value; the response is never
journaled, cached or relayed to another client. v1 has two:
`npm.admin.reveal-password` (6) and `docker.container.reveal-env` (3).

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
  `needs_repair`.
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
  compares the predicted state relevant to every protected flow (UFW
  chains, the built-in policies and every chain those flows traverse) with
  the loaded state after apply; a difference fails verify with
  `firewall.prediction_mismatch`, which reverts.
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
  interface is eligible. The
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
Setting `revertTimeoutSeconds` (default 60, range 30–600) is the module's
per-host setting. The UI shows a countdown with Confirm and Revert; the CLI
behaves as in 1.13.

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
  golden translation; the value or rule tuple for the rest). Startup
  recovery classifies an `intended` effect by comparing current state with
  both: equal to the before-state → not started; equal to the after-state →
  completed (`made`); anything else (partial, or edited since) →
  **ambiguous**. A mid-apply crash therefore leaves an exact list of what
  was done, or names the one effect that cannot be settled.
- **Revert of a change** (timer, explicit revert, startup recovery):
  1. If every UFW file's hash still equals the hash recorded right after the
     change applied, or, after a mid-apply crash, the hash expected after
     the last settled effect (no effect ambiguous), restore the snapshot
     (below). A missing post-apply hash never by itself authorizes a
     whole-snapshot restore; an ambiguous effect sends the change to step 3
     with that effect named.
  2. Otherwise (someone edited UFW during the pending window), apply the
     inverses of the journaled effects in reverse order, which leaves the
     other edits in place; logical rows are restored in the same transaction
     as the intent.
  3. If an inverse conflicts with a manual edit (the change's own rule was
     edited or removed, a manual rule now occupies the position), the change
     becomes `needs_repair` (1.3 engine integration) and keeps the `firewall`
     lock, its snapshot is pinned until the repair ends, and every firewall
     action declares `RepairedBy: firewall.state.repair` (1.3): `darkpanel
     change repair <id>` plans that action, which shows the difference, and
     applies it under the change's own lock ownership; and **connectivity comes
     first**: the inverses that do not conflict are still applied, and if the
     lockout guard evaluated on the resulting loaded state fails for a
     protected flow, the snapshot is restored whole even though that
     overwrites the manual edit (the diff of what was overwritten is kept in
     the change).
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
- **Break-glass.** `sudo darkpanel firewall snapshot restore --local <id>` and
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
  `firewall.docker.sources.set` (revertible, lock `firewall`; input either
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

The firewall `Uninstaller` takes `firewall.rules`:

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
on effects that occurred. Container identity (id and created time) only
says the object is the same one; the post-state each undo compares (1.2)
also covers what the action changed: for start, stop and restart,
`State.Running` and `State.StartedAt` as they were right after the action
(a container started, stopped or restarted again since is a `conflict`, so
an older undo never reverses a later operation); for network connect and
disconnect, the container's endpoint on that network (present or absent,
and its endpoint id); for a created network or volume, its id plus "no
container uses it".

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
  patches them back.
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
    root transient systemd unit `darkpanel-tailscale-pin-<change id>`
    started through D-Bus (the transient-unit runner for long-running agent
    applies, 1.3), so a restart of `darkpanel-agent`, which the new
    tailscaled can provoke by cutting its link, does not kill `dpkg`
    mid-install. Phases, each journaled before it runs, in `tailscale_pins`:
    1. `recorded`: the prior version and the prior hold state (the pin
       requires the package installed and refuses otherwise with
       `tailscale.unavailable`; a DERP host is on the tailnet already);
    2. `unheld`: `apt-mark unhold tailscale` (apt aborts with `-y` on any
       change to a held package, so every apt step below runs unheld);
    3. `staged`: `apt-get install --download-only --reinstall -o
       DPkg::Lock::Timeout=120 -y tailscale=<prior version>` and the same for
       the target version, which leave the `.deb` files in
       `/var/cache/apt/archives/`; the prior one is copied with
       `FileWriter.WriteManaged` into the managed directory
       `/var/lib/darkpanel/agent/tailscale-debs/` (flat name
       `tailscale_<version>_<arch>.deb`, its SHA-256 journaled), so rollback
       depends on neither the repository nor the network. If the prior
       version can no longer be downloaded, the pin stops here with
       `tailscale.pin_failed` after restoring the prior hold state; nothing
       was installed;
    4. `installed`: `apt-get install -o DPkg::Lock::Timeout=120 -y
       --allow-downgrades tailscale=<version>`;
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
    interrupted one is configured); `apt-get install -o
    DPkg::Lock::Timeout=120 -y --allow-downgrades <staged file>` after
    checking the file's SHA-256; restore the prior hold state; restart
    tailscaled; verify the prior version and backend state; mark the change
    failed. The controller learns the result through the lookup and the
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
  - `apt-get` runs with the verb first and options after it (1.8):
    `update -o DPkg::Lock::Timeout=120 -o Dir::Etc::sourcelist=<tailscale.list>
    -o Dir::Etc::sourceparts=-`, then `install -o DPkg::Lock::Timeout=120 -y
    tailscale` (the pin adds `--allow-downgrades` and the exact version, and
    `--download-only --reinstall` for staging; wave 0's allowlist carries
    these options and `/usr/bin/dpkg --configure -a`, 1.8). The
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

- **Connect (R58).** `tailnet.account.connect` (compensable; undo: delete the
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
  **Contract check (3b, first task)** settles, against Tailscale's API
  documentation and the fake's recorded responses, and writes into this
  section: the scope set and the dependencies stated above (Tailscale's
  OAuth page now defers its scope table to its "trust credentials" topic);
  whether `GET /api/v2/tailnet/{t}/keys` returns each key's description or
  only ids (the auth-key sweep, 1.9, is written to work from ids alone,
  fetching each key with `GET /api/v2/tailnet/{t}/keys/{id}`); and the
  response of `POST .../acl/validate` for a valid policy (an empty 200 body
  is the assumption above).
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
  .../acl/validate` (an empty 200 body is valid; otherwise `{message, data}`
  becomes `tailnet.policy_invalid`), and renders a line diff. Apply posts with
  `If-Match` on that ETag; a 412 fails with `tailnet.policy_conflict`. The
  effects journal stores the before-text and the after-ETag. Undo posts the
  before-text with `If-Match` on the after-ETag; if the policy changed since,
  undo fails with `tailnet.policy_conflict` and the change needs repair, so
  later edits are never erased.
- **Structural edits.** Other modules edit the policy through
  `tailnet.policy.patch` (compensable, same ETag rules): a list of JSON Patch
  operations applied with `github.com/tailscale/hujson`, so comments and
  unrelated content are preserved.
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
  3. **Cleanup.** When the host enrolls, the controller deletes nothing (the
     key is used up). When the token expires unused or the host is deleted
     first, a sweep deletes the key by description; an uncertain key
     creation is swept as in 1.9.
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
  creates system user `darkpanel-derp`; installs
  `/usr/local/lib/darkpanel/derper`; writes
  `/etc/systemd/system/darkpanel-derper.service` (user `darkpanel-derp`,
  `AmbientCapabilities=CAP_NET_BIND_SERVICE`, `NoNewPrivileges=yes`) and
  `/var/lib/darkpanel/derp/` (0700, owned by `darkpanel-derp`) as owned paths;
  and starts the unit. Install is long-running (1.3). The DERP spike confirms that `-verify-clients` works
  for a non-root user through tailscaled's socket; if it does not, the unit
  runs as root and the spike records why. `derp.server.uninstall`
  (compensable by `derp.server.install`) removes them.
- **Flags.** `-c /var/lib/darkpanel/derp/derper.key` (0600; preserved across
  upgrades and declared as an owned file), `-a :443 -http-port 80 -stun
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
    lock `derp`: moves the two files to `.prev` names (owned), restarts
    derper so it generates a new one-year certificate, reads back the new
    pin; undo moves the `.prev` files back and restarts), then `region`
    (`tailnet.policy.patch` replacing the node's `CertName`). Between the
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
  journal each phase, keep `derper.prev` (owned), swap, restart, and
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
(asynchronous apply, 1.3). Side: A agent, C controller. Names omit the module
prefix. Composites take `composite:<module>`; children take their own locks.
Scopes follow 1.7 (existing rules kept; raised where noted).

| Module | Action | Class | Side | Locks |
|---|---|---|---|---|
| firewall | `state.enable`, `state.disable`, `state.repair`, `defaults.set`, `logging.set`, `rules.apply`, `rule.add`, `rule.delete`, `snapshot.restore`, `docker.enable`, `docker.disable`, `docker.repair`, `docker.sources.set`, `container.allow`, `container.deny`, `container.remove`, `containers.apply` (each `RepairedBy: state.repair`, 2.5) | R | A | `firewall` |
| firewall | `snapshot.compare` | Rd | A | — |
| docker | the table in 3 (`compose.*` admin and L; `image.pull` L) | — | A | `docker` |
| tailscale | `prefs.set` | R | A | `tailscale` |
| tailscale | `node.login` (undo), `package.install` (undo, L), `package.pin` (self-rolling-back, recovery handler, L), `package.unpin` (undo: re-hold) | C | A | `tailscale` |
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
| cloudflare | `account.connect` (undo: delete the created row), `tunnel.create` (undo), `tunnel.adopt` (undo: restore the stored hash), `hostname.add` (undo), `hostname.remove` (by `hostname.add`) | C | C | `cloudflare` |
| cloudflare | `account.disconnect`, `tunnel.delete` | I | C | `cloudflare` |
| cloudflare | `tunnel.list`; `tunnel.token` (`Internal`) | Rd | C | — |
| cloudflare | `connector.run` (undo), `connector.stop` (by `connector.run`), `network.attach`, `network.detach` (claims, as for NPM) | C | A | `cloudflare` |
| cloudflare | `connector.deploy` (tunnel, token, run), `expose.container` (network, hostname) | X rev | C | — |
| cloudflare | `expose.remove` (hostname remove, network detach) | X fwd | C | — |
| panel | `network.create` (undo: remove if unused), `network.remove` (by create) | C | A | `panel` |
| panel | `listener.open` (undo: revoke and close), `listener.revoke` (`BreakGlass`; `ForwardOnly`, 1.2: revocation is monotonic and reopening needs a new `exposure.add`) | C | C | `panel` |
| panel | `exposure.add` (8) | X rev | C | — |
| panel | `exposure.remove` (8) | X fwd | C | — |

Every step of a `rev` composite has an `Undo` handler. The wave that builds a
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

  A lost response to step 2 cannot be resolved safely, so the step fails
  with `npm.setup_failed` and the change needs repair; repair removes the
  instance (its volumes are fresh) and deploys again. The password is never
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
- **Recovery identities (1.2).** NPM assigns ids on creation and has no
  idempotency key, so each create persists, before the call, an identity
  recovery can look up:
  - a proxy host: its exact `domain_names` set, which NPM keeps unique
    across proxy hosts (a create naming a domain another host uses is
    refused);
  - an access list: the name `darkpanel-<change id>-<step id>`;
  - a certificate: its exact `domain_names` set plus "created after the
    intent was journaled" (NPM derives a Let's Encrypt certificate's
    `nice_name` from its domains, so no marker can be attached).

  Recovery lists the objects and settles the effect: exactly one match →
  `made` with its id; none → not created; more than one (possible only for
  certificates or for an access list renamed by hand) → the effect stays
  `intended` and the change needs repair with the candidates shown;
  Darkpanel never deletes an object it cannot tie to the change. The 6a
  spike confirms against the pinned image that proxy-host domains are
  enforced unique, that access-list names are kept as given, and how a
  certificate's `nice_name` is set.
- **Reserved resources.** Proxy hosts, access lists and certificates
  created by `npm.admin.expose` or `panel.exposure.add` are recorded in
  `npm_reserved` (object id, owning exposure). `npm.proxyhost.add` and
  `.update` refuse (`npm.reserved`) an upstream of `127.0.0.1`, `localhost`
  or the NPM container's own addresses on port 81, and any upstream on the
  `darkpanel-panel` network or the panel gateway address, unless the
  change is a child of `npm.admin.expose` or `panel.exposure.add`
  (`parent_change_id`, 1.3) respectively; `.update`, `.remove`,
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

- **Account.** `cloudflare.account.connect` (controller, compensable; undo:
  delete the credentials row this change created): account id and API token
  (sealed). Token verification shows only that the token is valid, so connect
  probes read capabilities (list tunnels on the account, list zones) and
  reports write capabilities as unverified (1.9). Required permissions:
  "Cloudflare Tunnel Write" on the account (shown as Cloudflare Tunnel Edit in
  the dashboard), "Zone Read" and "DNS Write" on the zones used (shown as
  "DNS: Edit"). The **3c contract check** (first task) settles against
  Cloudflare's API documentation whether creating a tunnel and reading its
  token need any further permission (for example "Account Settings:
  Read"), and records the final list here and in the connect dialog's
  help text.
  `cloudflare.account.disconnect` is irreversible.
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
  compensable, each other's compensation, lock `cloudflare`): durable
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
  the change is a child of `panel.exposure.add`, and
  `cloudflare.hostname.remove` refuses a reserved hostname unless it is a
  child of `panel.exposure.remove`. Controller startup recovery
  finishes or undoes an operation found mid-phase through the actions'
  module recovery handler (1.3b). Unrelated ingress rules are carried over
  unchanged. `hostname.add` has an `Undo` (remove the record and rule it
  created, after the post-state checks); `hostname.remove` is compensated by
  `hostname.add` and used only in forward composites and by the operator.
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
  remove, and `ports` (`firewall.rules.apply` deleting the rule; revertible,
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
  log, and the guard context passed to its later children omits protected
  flow 3 for that exposure only (the parent authorizes this; no other
  change can retire a path). Every other protected flow is still checked.
  The harness removes the exposure through the exposure itself and expects
  the removal to complete. Break-glass: `sudo darkpanel panel listener
  revoke --local` over the controller socket runs `panel.listener.revoke`
  alone (`BreakGlass`); `darkpanel change repair` completes the rest later.
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
  (`dr-superpowers:judge-fable` and Codex `gpt-6-astra`, one shared brief).
  Findings go into `docs/superpowers/registers/<date>-v1-security-review.md`;
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
  3. `journaled`: the journal records, for each of `controller.db`,
     `controller.db-wal`, `controller.db-shm` and `controller.key`, whether
     it exists now (original presence);
  4. `moved-aside`: each existing original is renamed to `<name>.pre-<tx>`,
     one at a time;
  5. `installed`: each staged file is renamed into place with owner and mode
     (`darkpanel`, 0600), one at a time;
  6. `quarantined`: the restored database is marked `restore_quarantine`
     (below);
  7. `done`: the journal is removed; `.pre-<tx>` files are kept until the
     next successful restore or a manual cleanup command removes them.

  **Rename intent.** Every rename in phases 4 and 5, and every rename
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
  for each it completes the transaction when phase 5 has started (renaming
  the remaining staged files) or rolls it back otherwise (renaming back
  exactly the `.pre-<tx>` files its journal recorded, and removing
  originals that did not exist before), acting only on files named with
  that journal's `<tx>`. Recovery is idempotent; it does only renames and
  removals, well inside the units' 30 s `TimeoutStartSec`.
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

  `darkpanel controller quarantine release` ends quarantine: it is refused
  while records are unresolved unless `--keep-unresolved`, which leaves
  those records `quarantined` permanently (terminal: never run, shown by
  `darkpanel change list --state quarantined`, still resolvable one by one
  later). Release clears the flag, then the suspended sources start.
- Tests cover wrong passphrase, truncation, tampering, a large database
  within the memory bound, a crash at each phase and on each side of every
  rename and fsync (recovery's own renames included), two restores in a row
  with a crash in the second, a restore refused while a journal exists, a
  crash during recovery, a controller restart with the agent running, and a
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
  `core.change_busy`, `core.deadline_exceeded`, `cloud.cleanup_pending`,
  `npm.reserved`, `npm.unsupported_target`, `cloudflare.reserved`,
  `cloudflare.unsupported_target`, `cloudflare.connector_missing`,
  `derp.no_fallback`. Guard
  failures use the existing `plan.guard_failed`; `module.unavailable` and
  `request.invalid_input` already exist (wave 0 checks each name against
  `internal/core/errcode` and adds only the missing ones).
- **Actors:** `system:firewall.reconcile`, `system:recovery` (startup
  recovery, composite recovery and unwind, undo recovery, module recovery
  handlers; accepted by the principal checks for undo, lookup, resolve,
  revert and confirm of changes it recovers; it never plans or applies a
  new attempt, 1.3).
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
5. Tag `v1.0.0`; milestones close through `dr-superpowers:closing-a-milestone`.
6. Standing: after each Tailscale security release, a Darkpanel release that
   bumps `release/derper.version`, because DERP hosts hold tailscaled at the
   derper version (4.3).

## Contracts

- Every new action follows ADR 0002's compatibility rules and gets a schema
  snapshot under `schemas/`; `guard` and `overrides` are part of each guarded
  action's schema.
- Agent protocol: `hello` and the frozen surfaces are unchanged. New,
  non-frozen agent routes: `GET /agent/v1/streams`,
  `GET /agent/v1/streams/{module}/{name}`, `POST /agent/v1/changes/{id}/undo`,
  `/repair` (reruns a module recovery handler), `/release`,
  `/effects/{seq}/resolve` and `/private-output/ack`,
  `GET /agent/v1/changes/{id}/effects`, `GET /agent/v1/plans/{plan id}`
  (`PlanLookup`; `private=1` over the controller session only),
  `PUT /agent/v1/modules/{id}/enabled`,
  `GET /agent/v1/modules/{id}/availability`; DTOs in 1.3's wire contract.
  The apply request gains the core-level `guardRefresh` field and, for
  long-running and task actions, returns once the intent is persisted; a
  plan request gains the core-level `repairs` field, set only by the
  controller's repair route. During start-up admission (1.3) mutating
  routes answer `core.recovering`.
- API: `POST /api/v1/changes/{id}/undo`, `/repair`, `/release` and
  `/effects/{seq}/resolve`, `GET /api/v1/changes/{id}/effects` (DTOs, status
  codes and scopes in 1.3's wire contract; `ChangeView` gains
  `parentChangeId`, `undoState`, `owner` and `repairedBy`); apply answers
  `202` for composites, long-running and task actions; a plan request that
  supplies `repairs` is refused; the controller socket's backup and quarantine
  routes (1.12, 10); the SSE
  stream relay; asynchronous apply for composites and long-running actions
  (apply answers with the change in `applying`); `core.host.create` gains
  optional `tailnetJoin` and `tags`; enrollment gains the versioned `dpb2.`
  bootstrap bundle beside the unchanged five-field token;
  `darkpanel change undo|repair|release|resolve|effects`, `darkpanel controller quarantine
  list|resolve|enroll-token|release`.
- Shared contracts: class `composite` with `unwind: reverse | forward`;
  the additive change states `awaiting_confirmation`, `undoing`,
  `needs_repair` (composites and single changes), `recovering`, `repaired`,
  `abandoned`, `quarantined` (`applying` exists); undo states `none`,
  `running`, `done`, `blocked`, `failed`; owners `engine`, `composite`,
  `undo`, `module`; `ActionSpec.Available`, `MinScope` (raise only),
  `Internal`, `ForwardOnly`, `RepairedBy`, `ApplyTimeout`, `LongRunning`,
  `Task`, `Unwind`; the handler interfaces `Reconciler`, `Undoer`,
  `Recoverer` (`RecoveryOutcome`), `Composer`, `TaskMutator` with
  `DefineComposite`, `DefineTask` and `TaskRecover`, and the matching
  `ErasedHandler` fields `Undo`, `Reconcile`, `Recover`, `Task`, `Steps`,
  `Output`; `Step` and its reference syntax; `Env.Effects` with
  `EffectJournal`, `Effect`, `StateDoc`, `RecoveryID`, effect states and the
  helpers `ClassifyIntended`, `CheckPost`, `ClassifyUndoing`; supersession;
  `PlanLookup`, `EffectView`; the core-level `repairs` input field and the
  `repairOutcome` output field of `RepairedBy` actions;
  `platform.TransientService.RuntimeMax`; `core.Guard.Overridable`,
  `Overridden`; owned-path kinds `restore`, `managed-dir` and `sysctl`; the
  route and service registration hooks (1.12); task units
  `darkpanel-task-<change id>.service` running `{{.Binary}} agent task
  <change id>` (1.3c).
- Database: new module tables are prefixed with the module id; core gains
  on `core_changes` the columns `parent_change_id`, `recovery_owner`,
  `undo_state`, `repaired_by`, `repair_outcome`, `recovery_retry`,
  `private_output_ct` and `system` (1.4), on `core_plans` the columns
  `repairs` and `steps_ct` (a composite's sealed step list) with
  `steps_deadline`; the tables `core_effects` (both sides; one row per
  effect, secret sealed), `core_effect_deps` (both sides),
  `core_composite_steps` and `core_composite_attempts` (controller),
  `core_task_runs` (agent), `core_listeners` and the `restore_quarantine`
  marker; all migrations are expand-only.
- Files on disk: every new file, directory, unit and block is in its module's
  owned paths; UFW state files are `restore` paths.
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
  decisions in `docs/adr/README.md`), and the new packages
  (`platform/dockerapi`, `tslocal`, `netstat`, `netfilter`,
  `controller/cloud`, `controller/backup`).

## Parallel decomposition

"Owns" is within a wave; a later wave may edit an earlier wave's files.

| Wave | Part | Owns | Consumes | Runs |
|---|---|---|---|---|
| 0 | Core | `internal/core/**`, `internal/agent/**`, `internal/controller/**` except module code, `internal/transport/**`, `internal/cli/**` except module command files (including `internal/cli/controller.go` and the registration hook), `cmd/darkpanel/**` (the path-selection hook), `internal/install/**`, `internal/platform/` existing files and the runner, `internal/modules/modules.go`, module skeleton packages, `web/src/modules/registry.ts`, `web/src/modules/types.ts`, `web/src/modules/*/index.ts` stubs, `web/src/components/change-dialog*`, `web/src/shell.tsx`, `web/src/api/**`, `go.mod`, `go.sum`, `web/package.json`, `web/pnpm-lock.yaml`, `Makefile`, `test/harness/pins.go`, `provision.go`, `images.json` and the harness helpers, `docs/adr/0014-*`, `docs/adr/README.md` | — | alone |
| 0b-1 | Docker Engine client | `internal/platform/dockerapi/**` | 0 | parallel |
| 0b-2 | Tailscale local client | `internal/platform/tslocal/**` | 0 | parallel |
| 0b-3 | Sockets and routes | `internal/platform/netstat/**` | 0 | parallel |
| 0b-4 | Netfilter save parser | `internal/platform/netfilter/**` | 0 | parallel |
| 1 | Firewall: model, UFW, prediction, evaluator, guard, safe apply, snapshots, break-glass | `internal/modules/firewall/**` except `docker*.go`, `exposure*.go`, `reconcile*.go`; `internal/cli/firewall*.go`; `web/src/modules/firewall/**` except docker and exposure pages; `test/harness/m3_firewall_test.go` | 0, 0b | alone |
| 2 | Firewall: Docker integration, reconcile, exposure view | `internal/modules/firewall/docker*.go`, `exposure*.go`, `reconcile*.go`, their web pages, `test/harness/m3_docker_test.go` | 1 | alone |
| 3a | Docker module | `internal/modules/docker/**`, `web/src/modules/docker/**`, `test/harness/m4_test.go` | 2 | parallel |
| 3b | Tailnet (contract check: scopes, policy API) | `internal/modules/tailnet/**` except `join*.go`; `web/src/modules/tailnet/**` except the Hosts dialog extension | 0 | parallel |
| 3c | Cloudflare account, tunnels, hostnames (contract check: permissions, configuration API) | `internal/modules/cloudflare/**` except `connector*.go`, `expose*.go`; `web/src/modules/cloudflare/**` except connector and expose pages | 0 | parallel |
| 4 | Tailscale node, guard, package, web certificate, tailnet join at install (spike: local API endpoints) | `internal/modules/tailscale/**`, `web/src/modules/tailscale/**`, `internal/modules/tailnet/join*.go`, `internal/controller/listen/tailscale_cert*.go`, the `core.host.create` and enrollment-bundle files in `internal/controller`, `install.sh.in`, `install.sh`, the agent install path in `internal/install`, the bundle parser in `internal/transport/token.go`, `internal/cli/host*.go`, the Hosts dialog extension in `web/src/modules/tailnet/`, `test/harness/m5_node_test.go` | 1, 3b | alone |
| 5 | DERP (spike: derper flags, IP certificate expiry, non-root verification, the DERP-region debug endpoint as diagnostics, visibility grant) | `internal/modules/derp/**`, `web/src/modules/derp/**`, `release/derper.version`, `.github/workflows/release.yml`, `test/harness/m5_derp_test.go` | 1, 3b, 4 | alone |
| 6a | Nginx Proxy Manager (spike: image digest, `LE_SERVER` and the CA-bundle variable, NPM object identities, 1.2) | `internal/modules/npm/**`, `web/src/modules/npm/**`, `test/harness/m6_npm_test.go` | 2, 3a | alone |
| 6b | Cloudflare connector and expose container | `internal/modules/cloudflare/connector*.go`, `expose*.go`, `web/src/modules/cloudflare/connector*`, `expose*`, `test/harness/m6_cloudflare_test.go` | 3a, 3c, 6a | alone |
| 7 | Controller backup and restore | `internal/controller/backup/**` (registering its socket routes through the 1.12 hooks; it sets the quarantine flag that wave 0's gate reads), `internal/cli/controller_backup*.go`, `internal/cli/controller_quarantine*.go`, the controller unit template in `internal/install`, `test/harness/restore_test.go` | 0 | alone |
| 8 | Panel exposure | `internal/modules/panel/**`, `web/src/modules/panel/**`, `test/harness/m6_panel_test.go` | 2, 6a, 6b | alone |
| 9a | Fuzz inventory and targets | `docs/security/parsers.md`, `*_fuzz_test.go`, `testdata/fuzz/**`, `scripts/fuzz.sh`, `.github/workflows/fuzz.yml` | 0–8 | parallel |
| 9b | Accessibility | `web/src/**` (fixes and `.a11y.test.tsx`), `docs/accessibility.md` | 0–8 | parallel |
| 9c | Docs and API reference | `docs/user/**`, `docs/dev/**`, `docs/api/**`, the `api-docs` target in `Makefile`, `.github/workflows/ci.yml` | 0–8 | parallel |
| 9d | Upgrade matrix and footprint | `test/harness/upgrade_matrix_test.go`, `footprint_test.go`, the `harness` build-tag files in `internal/agent/` and `cmd/darkpanel/` (through the wave 0 path-selection hook), `.github/workflows/harness.yml` | 0–8 | parallel |
| 10 | Security review, fixes, runbook, RC readiness | `docs/security/threat-model.md`, `docs/runbook.md`, `CHANGELOG.md`, `docs/versioning.md`; fixes anywhere | 0–9 | alone |

- Shared files (module list, web registry and types, allowlist, error codes,
  dependencies, lockfiles, harness pins and helpers) belong to wave 0. Before
  wave 9, a change to `harness.yml` (a job split or a CI secret) belongs to
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

One plan per wave, named `docs/superpowers/plans/<date>-v1-w<n>-<topic>.md`,
written after the previous wave merges. Every plan ends with the gates in
`docs/superpowers/gates.md`, including the harness gate when it touches the
harness, a module's host behaviour or the paths gate 7 lists (waves 4 and 7
touch `install.sh.in` or `internal/install/`).

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
