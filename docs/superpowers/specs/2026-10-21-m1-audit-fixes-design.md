# M1 audit fixes

**Source:** the M1 milestone audit of 2026-10-21 at 46b4233 reopened
`docs/superpowers/registers/2026-10-17-m1-platform-core.md` rows 33, 38, 45,
59, 95 and 113-117: they were deferred without a recorded reason. The owner
ruled on 2026-10-21 to fix every item, and to settle each decision below by a
Fable and Codex astra review whose aggregated recommendation is followed.

**Covers:** the items of those ten rows, numbered `<row><letter>` as below.
Code locations were checked against 5761fd8.

## Already resolved (closed with evidence, no work)

- **38c** harness build tag: every file under `test/harness/` carries
  `//go:build harness`, and `harness.yml` runs `go test -tags harness`.
- **95e** `Hosts.mu` held during Hello: fixed in 7b33cbe; per-host locks,
  `hosts.go:47` "never held across I/O", `TestAHungHelloDelaysOnlyItsOwnHost`.
- **95i** unfiltered Changes 404 for a host without a client: fixed in b16cd33;
  `StaticClients.Client` answers `host.offline`, unfiltered `Changes` skips it
  (`changes.go:189-191`, `TestChangesSkipOfflineHosts`).

## Direct fixes (no decision open)

| Item | Defect | Fix |
|---|---|---|
| 33a | `store.DB.Write` rolls back only on panic; `runtime.Goexit` in `fn` leaves the writer inside an open transaction | `defer` a rollback guarded by a `committed` flag; test `TestWriteRollsBackOnGoexit` |
| 33b | no test that `-wal` and `-shm` are 0600 | extend `TestOpenSetsPragmasAndMode` after one write |
| 33c | reader pool untested beyond `query_only` | pragma loop on `db.Read()`; a read during an open `Write` sees the last committed state |
| 38a | `web-deps` reruns `pnpm install` on every web target | stamp file `web/node_modules/.stamp` depending on `web/package.json` and `web/pnpm-lock.yaml` |
| 38b | `verify-build` runs `bin/darkpanel-linux-$(go env GOARCH)` | use `GOHOSTARCH`; skip the run check with a message when the host arch is not in `ARCHES` |
| 45b | no tests for `keep < 1`, the chmod-failure cleanup, a symlink under the root | three tests |
| 59b | no test for an irreversible action with an empty confirmation phrase | fixture handler; assert the error and no `core_plans` row |
| 59c | AEAD tag mismatch never reached with a wrong key | in-package test: key-2 sealer carrying key 1's id; `Open` fails with `secret: open` |
| 59d | zero-value or nil `Sealer` panics in `Seal`/`Open` | return an error `secret: sealer not initialised`; test |
| 95a | `schema.Redact` rebuilds the output schema per call | cache per `reflect.Type` (schema or error) in a `sync.Map` |
| 95c | `audit.Store.insert` stores `Seq 0` as NULL, escaping the dedupe constraint | `Store.ingest` refuses `Seq <= 0` |
| 95f | a failed explicit Hello after a success leaves `greeted` set | clear the host's `greeted` entry on every failure path; test |
| 95h | `Dispatcher.Apply` indexes a returned view without checking host, action and version | refuse a mismatching view before indexing, as `checkChange` does; test |
| 95k | `recoverPanics` drops only `Set-Cookie` from headers set before a panic | snapshot the header map before `next`; on panic restore it; test with a custom header |
| 95l | HEAD on the listeners route untested with credentials | authenticated HEAD case: 200, empty body, `Cache-Control: no-store` |
| 113 | a 401 from a request begun before re-login clears the newer session | session generation counter in `web/src/api/client.ts`, bumped by `setCsrfToken`; the unauthorized handler runs only if the generation is unchanged; test |
| 114a | `ApiError.fields` never shown | the token dialog lists `path: message` for each field error; test |
| 114b | whitespace-only token name gets the generic schema message | client-side check "Enter a name for the token." before planning; test |
| 116a | logout from a page keeps it as the login `next` parameter | navigate to `/login` (replace) before clearing the session; test from `/audit` |
| 117a | no UI-level test that the audit page never renders a secret | `audit.test.tsx` case with a `dpt_` value in a stubbed entry's input, asserted absent (the API level is already covered by `TestEndToEnd` `checkAudit`) |

## Decisions (proposed; the review settles them)

Each lists the options and the proposal.

**33d Existing database file mode.** `Open` sets 0600 only on a file it
creates; an existing 0644 file keeps its mode and passes it to `-wal`/`-shm`.
Options: (a) chmod the database to 0600 on every `Open`, and any existing
`-wal`/`-shm`; (b) refuse to open a file looser than 0600; (c) keep the old
ruling and document it. **Proposal: (a).** The process owns the file; a
refusal would turn a harmless leftover into an outage.

**45a What counts as a backup.** `pruneBackups` deletes any directory whose
name matches the backup pattern. Options: (a) count a directory only when the
database file (`filepath.Base(db.Path())`) is inside it as a regular file;
(b) a marker file written by `Backup`; (c) a subdirectory only Darkpanel uses.
**Proposal: (a).** It recognises backups already on disk, which (b) and (c)
would orphan.

**45c A prune error fails startup.** A failed prune returns an error from
`Backup`, so `Migrate` and startup fail on every restart until an operator
removes the directory (old ruling pc-14-3: failing loudly is intended).
Options: (a) keep it fatal, return the new directory and name the one that
could not be removed; (b) non-fatal, reported through a new
`Result.PruneErr`; (c) non-fatal, logged as a warning. **Proposal: (b)** with
the server logging it at warning level: the backup succeeded, a leftover old
backup costs only disk, and the error stays visible to the caller.

**59a ID ordering on a backwards clock step.** `newID` stamps ULIDs with the
wall clock; `Changes` pages with `id < before`. `Plan` reads the clock three
times. Options: (a) clamp each ID's timestamp to the last one issued in this
process; (b) (a), seeded at engine start from the largest stored plan and
change id; (c) page on a stored sequence column. **Proposal: (b)**, and read
the clock once in `Plan`. (c) is a migration for a rare event.

**95b An abandoned Verify keeps running beside Revert.** When Verify passes
its deadline, `failApply` starts the revert while the Verify goroutine still
runs. Options: (a) accept and document (Verify only reads); (b) wait a
bounded grace period for the Verify goroutine before reverting, then proceed
and log; (c) skip the automatic revert after an abandoned Verify and mark the
change failed and unverified for an operator. **Proposal: (b)**, grace 5 s.

**95d A failing host's ingestion warning every 5 s, no status surface.**
Options: (a) log when a host starts failing and when it recovers; (b) keep
the last ingestion error per host and expose it additively in the host list
(API and UI); (c) both. **Proposal: (a)** now, with (b) recorded as a row
for the M2 fleet status work, where per-host status already lives.

**95g `noChange` treats unknown coded errors after a commit as no change.**
Options: (a) an allowlist of codes known to precede any commit
(plan.not_found, plan.expired, invalid input, confirmation and guard
refusals, module.unavailable, lock.held); (b) look up whether a change exists
for the plan before unmarking it. **Proposal: (a).** Fail safe: an unknown
code keeps the plan marked.

**95j A cancelled request during the login write counts an attempt.** The
code documents this as intended. Options: (a) keep, recording why; (b) do
not count an error caused by `ctx.Err()`. **Proposal: (a)**: with (b) a
client could verify a password guess and cancel before the attempt is
counted, which is the probe the limiter exists to stop.

**95m Test-hygiene gaps from the plan 4 ledger.** The ledger was never
committed; the list cannot be recovered. Options: (a) a fresh hygiene review
of Tasks 2-34's tests; (b) fix the categories known from row 96 (no
`t.Cleanup` on failure paths, HTTP clients without a timeout such as
`http.DefaultClient` in `server/listeners_test.go`) across the controller
tests, and close the rest as unrecoverable. **Proposal: (b).**

**115 No sidebar item highlighted on host pages.** darkraise-ui sets
`activeExact` on sidebar links; "Hosts" links to `/`. Options: (a) in
`AdapterLink`, treat the Hosts item as active on `/` and under `/hosts/`;
(b) move the hosts list to `/hosts` and drop `activeExact` upstream; (c) CSS
override. **Proposal: (a)**, as a per-link `activePrefix` the shell's NAV
declares, so the adapter stays generic.

**116b The user menu shows the role in the email slot.** The kit's user has
only `name` and `email`. Options: (a) leave `email` empty; (b) a label such as
`Role: admin`; (c) ask darkraise-ui for a subtitle field. **Proposal: (a)**:
users have no email, and M1 has a single role.

**117b The unauthenticated session probe prints one 401 console error.**
Options: (a) accept: the browser prints it and script cannot suppress it;
(b) make `GET /api/v1/auth/session` answer 200 with `null` when there is no
session, which adds it to the spec section 12 public list and changes
`specPublic` in `deny_test.go`. **Proposal: (a).** Widening the
deny-by-default public surface to remove a console line is the wrong trade.

## Review outcome (binding)

Reviewed 2026-10-21 by Fable (judge-fable) and Codex gpt-6-astra against
981368a. Where they split, the ruling follows the reviewer whose evidence
addresses the row's own wording. These rulings replace the proposals above.

| Item | Ruling | Raised by |
|---|---|---|
| 33a | Fix stands. Correction: callback errors already roll back (`db.go:109`); only `runtime.Goexit` escapes. Keep the existing error joining and panic propagation; the test calls `Goexit` in a spawned goroutine | both |
| 33c | Hold four reader `sql.Conn` leases at once and check the pragmas, `query_only` included, on each | Codex |
| 33d | (a). Chmod the database and any existing `-wal`/`-shm` to 0600 before `sql.Open`; a chmod failure fails `Open`; update the doc comment at `db.go:41-42` | both |
| 38a | Stamp prerequisites also include `web/pnpm-workspace.yaml` | Codex |
| 38b | The executing checks run with the host's `GOOS`/`GOARCH` (cleared cross-compile overrides), use `GOHOSTARCH`, and skip with a message on a non-Linux host or an arch outside `ARCHES` | both |
| 45a | (a). Keep the `e.IsDir()` check, and `Lstat` the inner database file so a symlink never qualifies | both |
| 45b | The chmod-failure test uses a package-level `chmod = os.Chmod` seam | Fable |
| 45c | (b). `Backup` returns the prune error apart from a copy or chmod failure, which stays fatal; `Result.PruneErr` carries it; the controller server, the agent runtime and the installer each log or print it. Retry: every `Migrate` run attempts the prune even when it takes no backup, so the ADR 0005 "last five kept" rule holds from the next clean start | both; retry rule from Codex's ADR 0005 finding |
| 59a | (b), strengthened. Read the clock once in `Plan`; clamp each ID's timestamp to at least the last issued; seed at engine start at the largest stored plan or change id's timestamp plus 1 ms, so the first new id exceeds the whole stored id; plan expiry stays on wall time. The test needs a clock that can step back. Check the audit store's ULIDs (`audit/audit.go:69-75`, paged by id) and apply the same fix if it shares the defect | both; audit store from Fable |
| 59d | Also `Derive`: a zero-value sealer panics with a clear message (it has no error return) instead of deriving from a nil key | Fable |
| 95b | (a). No grace period. Document in the handler contract (`core/contract.go:152-155`) that Verify must be read-only, honour cancellation and be safe alongside a rollback | both |
| 95d | (c). Per-host ingestion state: warn on the first failure or a changed message, info on recovery, the per-tick repeat at debug; the same reporting for scheduled and dispatcher-triggered ingestion; an additive host-list field for ingestion health ("not yet checked" distinct from healthy) in `core.Host`, its generated schema and snapshot, the web type and the hosts overview | Codex (surface), Fable (logging detail) |
| 95g | (a). The allowlist also includes `action.unknown` and `plan.state_changed`; keep the `claimed` and empty-view conditions | both |
| 95j | (a). Reason: cancellation is client-controlled, which is the "attacker who can provoke faults" case the invariant at `login.go:56-59` covers; a wrong password is counted before any write | both; reason from Fable |
| 95k | Clear the live header map and copy the snapshot back; the test checks a custom header is gone and the security headers survive | both |
| 95l | The authenticated HEAD case runs against a real `httptest.Server`, since a `ResponseRecorder` keeps the body | Codex |
| 95m | A bounded fresh hygiene review of the tests written by Tasks 2-34 of `docs/superpowers/plans/2026-10-18-m1-dispatch-cli-and-assembly.md`, starting from the known categories (missing `t.Cleanup` on failure paths, HTTP clients without a timeout: `server/listeners_test.go:98,101,105`, `server/e2e_test.go:89`, `server/server_test.go:445`, `listen/service_test.go:157`), fixing what it finds | Codex (review), Fable (known sites) |
| 113 | The generation check also guards the session probe's success and rejection callbacks, not only the unauthorized handler; test a delayed probe across logout and re-login | Codex |
| 115 | (a) through a prefix table in `web/src/router/` keyed by `to` (`"/"` also matches `/hosts/`), combined with `NavLink`'s `isActive`, so both the active style and `aria-current` follow; no field on `NAV`, which the kit's closed `NavItem` would not forward | both |
| 116a | Logout is a session transition whose redirect omits `next`; cover a logout answered with `auth.required`; the test records every location and asserts `/audit` then `/login` only | both |
| 116b | (b) `Role: admin`: the kit renders the email slot unconditionally, so an empty value leaves a blank line | Fable |
| 117b | (a). Accepted: the endpoint is authenticated by design (`deny_test.go:23`, ADR 0006) | both |

New items found by the review, outside the ten rows, recorded as register
rows rather than widening this work: the controller's unfiltered `Changes`
merges hosts by id, so its order across hosts stays wall-clock based
(`dispatch/changes.go:163-167`).

## Out of scope

New features beyond the items above. A decision whose outcome is "record a
reason" is resolved by the register note, not by code.

## Verification

Gates 1-7 of `docs/superpowers/gates.md` on the merged main, the M1 browser
check repeated for 113-116, then the M1 audit rerun.
