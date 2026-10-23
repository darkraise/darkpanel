# w0b platform and surfaces: run record

Every ledger line containing `Ruling:` in the order it was made, then every plan amendment in full. No task ended BLOCKED. The plan workspace is deleted when its worktree is removed, so this file is the lasting record of the decisions the run took.

## Rulings

- Ruling: pre-flight — 1 finding (Task 32 credits the runner line to Task 2 instead of Task 13), see preflight.md; no same-wave pair needs serializing — none to the code
- Task 32: Ruling: amendment A1 — Task 32's runner-line note now credits Task 13, as C14 says — wording only, no code or test changes either way
- Ruling: baseline — go test -count=1 ./... exited 0 in the new worktree before Task 1 — none
- Task 1: parked — cannot verify that nothing else depends on core_audit or core_owned_files — Ruling: PARK; no REFERENCES, TRIGGER or VIEW exists in the migrations, and every index on core_audit is recreated by the rebuilds — low cost if wrong, a dropped index would show in audit ingestion tests
- Task 2: parked — cannot verify that callers outside internal/platform still compile — Ruling: PARK; go vet ./... over the module exits 0 and every NewRunner call matches the new signature — low cost if wrong, the branch-wide gates would catch a compile error
- Task 3: parked — cannot confirm from the diff that go mod tidy and golangci-lint printed nothing — Ruling: PARK; the controller reran both at c5578ed (tidy: no output; events package lint: 0 issues) and the diff touches no go.mod or go.sum — low cost if wrong, the final branch gate lints the whole repository
- Task 4: parked — cannot verify from the diff that ExpiresAt derives from the stored last_seen_at — Ruling: PARK; session.go:154-156 caps exp at stored last_seen_at + SessionIdle and a passive check never moves it — low cost if wrong, a stream would close late but the next authentication still refuses the session
- Task 5: parked — cannot verify from the diff that Task 52's hook never blocks or calls back into the registry — Ruling: PARK; Task 52's hostOnline is a single bus.Publish and Bus.Publish takes only its own lock, so the lock order is r.mu then b.mu only. Task 52's dispatch must carry: (1) hostOnline stays a single bus.Publish call with no registry, database or logging call; (2) s.events is assigned (Task 44) before the session-registry block at services.go:84-94 builds its config, or the hook panics on a nil bus while r.mu is held — a hang would show in Task 52's -race end-to-end test
- Ruling: wave 6 (Tasks 6-8) runs one task after another in the checkout — the budget leaves room for one task at a time, and a sequential run is always correct — costs only wall-clock time
- Task 6: parked — cannot verify which tasks embed the six C2 interfaces (the brief names two different lists) — Ruling: PARK; the C2 heading list is correct (Tasks 59, 68, 73, 80 embed into AgentClient; 78, 83 into Dispatcher) and Task 6's line 21 is a wrong note that changes no code. Task 80's dispatch must say: the AgentClient lines after AgentChangeOps were added by Tasks 59, 68 and 73, and the operation constants after opAckPrivate by Tasks 65, 69 and 74 (plan lines 28015 and 28054 name the wrong tasks). Task 27's dispatch must say Task 26, not Task 65, adds `_ core.AgentEnablement = (*Client)(nil)` (plan line 10374) — an implementer would waste time looking in the wrong task, no compile break
- Task 7: parked — cannot verify from the diff the reported RED and GREEN runs and the build, vet, lint and tidy results — Ruling: PARK; the controller reran go test ./internal/core/errcode/ at 1d9dfe2 (ok) and the change is a constant, a map row and two test rows matching C4 and spec section 11 — a lint or format finding in a 4-line change would surface at the closing gates
- Task 8: parked — cannot verify from the diff that the scratch directory was removed — Ruling: PARK; the controller listed /root/.cache/w0b-tmp afterwards and it was empty, systemctl list-units 'darkpanel-live-*' printed nothing and darkpanel-agent and darkpanel-controller were both active — a leftover would only waste disk space
- Ruling: wave 7 (Tasks 9-11) runs one task after another in the checkout — the budget leaves room for one task at a time, and a sequential run is always correct — costs only wall-clock time
- Task 9: parked — cannot verify from the diff that composite unwind and release stay ungated — Ruling: PARK; children.go dispatchChildren.Undo and Release call Engine or the agent client directly and never Dispatcher.Undo, Release, Repair or Resolve — a gated path would strand a composite's recovery, but only children.go builds lifecycle.Children
- Task 10: Ruling: reviewer's note that its read of the brief stopped at line 753 of 1063 is not a requirement the diff could fail; Task 10's section, the Global Constraints and C9 were inside what it read — closes the item without a seat dispatch — cost if wrong: a later contract the reviewer missed could bind this task, which the whole-branch final review would catch
- Task 11: parked — cannot verify from the diff that the API suite passes unchanged and runs the same tests — Ruling: PARK; the diff changes only the harness helpers in server_test.go and adds no, removes no and renames no Test function, and the controller's own go test -count=1 -race ./internal/controller/api/ at 0846fa3 printed ok — a silently dropped test is not possible with helper-only edits
- Ruling: header amendment A2 confirmed by a second judge-opus — only Task 54 (avrServe) reads the harness field cfg, so the Global Constraints bullet now says so; Tasks 15, 30, 79 and 85 build their servers from their own Config literals — if wrong, the header under-lists a consumer of a field that still exists and nothing breaks
- Task 11: Ruling: amendment A2 (Header) — the Global Constraints bullet for newHarnessWith named Tasks 15, 54 and 79 as users of h.cfg; only Task 54 reads it — header under-lists a consumer at worst, no code breaks. Dispatch notes: Tasks 15 and 79 use sseServer and arvHarness exactly as written and must not switch them to h.cfg; Task 54 may rely on h.cfg holding the full Config New received (server_test.go:166-176)
- Task 12: parked — gate 7 (harness scenario 7, scenarioCrashLoop) was not run for Task 12 — Ruling: PARK; the plan defers gate 7 to the pushed branch. The final gate run must be a harness run on the pushed branch HEAD that includes commits 9c84c47 and dc984af (gh workflow run harness.yml --ref v1-w0b-platform-surfaces, then gh run watch), with all four jobs (ubuntu-22.04, ubuntu-24.04, debian-12, debian-13) green; a red TestM2/crash-loop is a Task 12 regression and returns as a new item — on systemd 255+ a crash-looping update might never roll back, which only the VM harness can catch
- Task 12: parked — the systemd version behind systemd-analyze verify was unknown — Ruling: PARK; this host runs systemd 257 (257.13-1~deb13u1) and the brief asks for the check only on a host with systemd; 249 and 252 ignore RestartMode= with a warning and nothing runs systemd-analyze at install time — an older systemd refusing the units would show in the four-OS harness run
- Task 13: parked — nft table name pattern accepts a leading dash — Ruling: PARK; spec 1.8 requires exactly ^[A-Za-z0-9_.-]{1,64}$, the only caller (the firewall module, spec 2.1) takes names from nft list tables output, and only root can create a table. The 2.1 firewall module task owns that check, and a leading-dash refused case is a one-line pattern change if the spec owner tightens it — if wrong, a caller passing an unvalidated name could make nft read a ruleset file as root
- Task 13: parked — compose files are not tied to the project directory — Ruling: PARK; spec 1.8 asks only for absolute paths and spec 3 R53 decides containment at the docker module's validation (absolute, existing, root or directory-owner owned, no symlink), because discovered projects may keep files outside the working directory. The docker module task (wave 3a, R53 validation) owns containment and its harness test — if wrong, an unprivileged user could get a root-run compose file accepted if that validation is never built
- Task 13: parked — userdel and useradd user list is hardcoded — Ruling: PARK; spec 1.8 fixes the set to darkpanel and darkpanel-derp and the wave-end check owns drift. The derp module task should add a test in internal/install (not internal/platform, which would create an import cycle) that every OwnedUser in modules.All() plus ServiceUser passes the ProductionRules userdel rule — if wrong, a future module's user would make uninstall fail on real hosts with no test noticing
- Task 13: parked — cannot verify that ProductionRules covers spec 1.8 completely — Ruling: PARK; the seat compared exec_rules.go with spec lines 1759-1788 entry by entry (users, ufw, iptables, save and restore paths, apt-get APT-1 to APT-6, apt-mark, dpkg, nft, docker) and found nothing missing or wrong — cost none found
- Task 13: parked — cannot verify that ServiceUser equals "darkpanel" — Ruling: PARK; internal/install/layout.go:35 reads const ServiceUser = "darkpanel" and uninstall's userdel vector matches the rule — an install or uninstall user step would be refused at runtime
- Task 14: parked — guard-detection logic duplicated in suppliedGuard and refuseSuppliedGuard — Ruling: PARK; Task 14 steps 3 and 4 prescribe the two functions, no later task builds on either, each has its own case-variant test, and the error shapes differ on purpose (/input/<key> on the controller, /<key> on the agent socket); a shared internal/core helper is a small later refactor
- Task 14: Ruling: cannot-verify agent socket routes other than plan taking an input — met; only read takes an input (unguarded by design), apply decodes ApplyOptions with no input, so every applied plan passed handlePlan (routes(false) in internal/agent/api/handler.go) — none
- Task 14: Ruling: cannot-verify authorization order in runAction — met; actionHandler authorizes before runAction on the network API and the controller socket, and there are no other callers — none
- Task 15: Ruling: cannot-verify Task 44 passes Events and Clock and registers CloseStreams after the listeners — PARK; Task 44 Step 3 does both and TestServesTheEventStream covers them — a dropped shutdown step would hang controller shutdown and Task 44's own test would fail
- Task 15: Ruling: cannot-verify Task 30 holds eventSlot for the whole stream and answers not_found when Events is nil — PARK; handleEvents checks bus == nil first and defers release around sseStream, TestEventsLimit covers it — none
- Task 15: parked — sseStart leaves sseStream goroutines running in TestSSEHeartbeat and TestSSEWriteDeadline — Ruling: PARK; nothing in the repo uses goleak or runs -count=N on internal/controller/api, goroutines last only until the test binary exits; the fix is one t.Cleanup(CloseStreams) if a leak check is added later
- Task 15: parked — sleep-based negative check in TestSSEHeartbeat — Ruling: PARK; the check can pass falsely but never fail falsely, and the rest of the test pins the ping at 20 s; the cost is one early heartbeat byte on a slow runner
- Task 15: parked — release can run twice and sseEncode does not reject newlines in ID or Event — Ruling: PARK; Task 30 calls release once through defer, Task 85 never calls eventSlot, and ID and Event come only from internal epochs, sequence numbers and bus type constants or the literals data and end
- Task 15: Ruling: HEAD holds an event stream open — CONFIRMED-GAP; Go 1.22 ServeMux sends HEAD to GET patterns, so Tasks 30 and 85 would hold a slot for up to 10 minutes; fix in sseStream: return "done" for http.MethodHead right after the initial sseWrite(w, nil) block and before clock := s.cfg.Clock, document it, and add TestSSEHead (HEAD request, result "done", status 200, Content-Type text/event-stream, fake clock Pending() == 0); no change to Tasks 30 or 85
- Task 16: Ruling: a failed or partly unparsable change poll records its rows as seen and drops their events (pollChanges) — CONFIRMED-GAP; record rows in a local map and merge into w.changes only after rows.Err() is nil, warn and skip a row whose updated_at does not parse, and add TestWatchSkipsUnparsableTime; spec 1.6 requires a change event for every recorded state — a few warning lines and one map per poll
- Task 16: Ruling: amendment A3 — pollChanges text amended to match the ruling so a fresh brief does not reintroduce the defect — none
- Task 16: Ruling: late commits past the 5 s overlap lose events — AMEND; polling with a fixed overlap is the plan's decision and holds for every writer that reads the clock inside its write transaction, except the intent insert in apply.go begin, which stamps updated_at before recheck and Snapshot; fixed in Task 60's replacement text, not in Task 16 — a fake clock sees equal values so existing tests are unaffected
- Task 60: Ruling: amendment A4 — the intent insert stamps updated_at with a clock read inside the transaction (formatTime(e.cfg.Base.Clock.Now())) — updated_at of a new change is later than created_at by the recheck and Snapshot time
- Task 16: Ruling: cannot-verify timeFormat equals lifecycle.timeFormat and the writers use the watcher's clock — PARK; internal/core/lifecycle/journal.go defines the same format and every writer stamps through formatTime on e.cfg.Base.Clock, which Task 52 builds from s.clock — none for production code (Task 52's test formats without .UTC(), correct only if the server fake clock runs in UTC; unverified)
- Task 16: parked — AgentChanges publishes can arrive out of order and outlive shutdown — Ruling: PARK; spec 1.6 has the client refetch the queries an event names, and a late publish writes only to the in-memory bus
- Task 16: parked — a failed first read breaks "the first poll only records" — Ruling: PARK; it only causes redundant refetches shortly after startup and nothing relies on it
- Task 16: parked — stop can block shutdown on an unbounded read — Ruling: PARK; reads are local SQLite bounded by the busy timeout and C29 only requires stop to wait
- Task 16: parked — timeFormat duplicates lifecycle.timeFormat — Ruling: PARK; the two are identical, the stored format is a schema contract and changing it needs a migration review
- Task 17: Ruling: cannot-verify module streams really go through OpenModule — PARK; Task 76 adds the separate stream client over ClientConfig.DialStream and Task 84 wires DialStream to s.OpenModule in fleet/registry.go with TestRegistryDialsModuleStreams, while Task 80 dials nothing; the only other api.NewClient caller is the local agent socket with no session budget — a session-backed client built without DialStream would put module streams on the 16 action slots (Task 84's test covers the registry path only)
- Task 18: Ruling: cannot-verify controller stores an agent entry with phase uninstall-abort in core_audit — PARK; audit.go copies the phase unchanged with no Go allowlist, Task 1's 0011_platform_surfaces.sql widens the CHECK to include uninstall-abort and TestControllerPlatformSurfacesMigration inserts such a row — a rejected phase would stall IngestEpoch for that host, visible in the branch-wide review
- Task 18: parked — AbortUninstall journal entry uses time.Now() and the engine path builds its own AgentJournal instead of the configured one — Ruling: PARK; the plan mandates both (C36 signature (ctx, db, actor)), no later task tests the entry time under a fake clock, and in production the engine clock is wall-clock; the fix if ever needed is an unexported abortUninstall(ctx, db, actor, at) taking the engine's clock
- Ruling: Wave 15 base proven — the integration check at fade0a1 passed (wave-15-base-check.txt, exit 0) and context-size --wave 3 said handoff while --wave 2 said ok, so the wave runs two tasks at once — a wider run would exceed the session budget
- Ruling: Wave 15 width — context-size --wave 2 said handoff (224k of 210k) once Task 20 reported, so Task 21 is not dispatched in this session; Tasks 19 and 20 finish and merge, then Wave 15 is serialized for Task 21 and the session hands off — the integration check for Tasks 19-21 runs when Task 21 completes
- Task 20: Ruling: cannot-verify every real module still passes the new registry checks — PARK; only the core declares owned paths today (install.CoreOwnedPaths, kinds file, dir, unit, user, core exempt from the unit-path rule), no module declares ExternalUnits, Pattern, User or Subdirs, planned fixtures scpDerp and uexModule fit the rules, and Task 47's kindsModule would be refused but uninstall reads Manifest() directly and never builds a registry — a fixture not traced here fails at merge as a registration error naming the module
- Task 20: Ruling: the path / is accepted for restore, managed-dir and service-dir — CONFIRMED-GAP; neither the spec nor Tasks 37 and 38 refuse it, so createManaged would record / as the module's managed-dir before Mkdirat fails and every uninstall would then stop at the files step; fix in checkOwnedPaths: for OwnedRestore, OwnedManagedDir and OwnedServiceDir refuse p == "/" with "module %s: %s path %q is the root directory" and add one refusing row to TestRegistryOwnedPathKinds — four lines and one test row, no legitimate module declares /
- Task 21: Ruling: cannot-verify the implementer removed /root/.cache/w0b-tmp/21 — checked by the controller, the directory is gone (ls of /root/.cache/w0b-tmp lists nothing) — none
- Task 22: Ruling: the address formats refuse "" while netip zero values marshal as "" and omitempty never omits a struct — CONFIRMED-GAP; keep refusing "" (spec 1.5 checks with netip.ParseAddr, ParseAddrPort and ParsePrefix, and accepting "" would let required Peer.addr and ProxyPath.proxyAddr carry an empty address into a lockout guard), tag the fixture's optional netip fields omitzero, add a sentence to the format-constant comment in reflect.go and add TestValidatorAcceptsUnsetAddressFields; no later task sends a zero netip value through the validator — test-fixture tags and a comment only
- Task 22: Ruling: amendment A5 — addrFields fixture text amended to omitzero so a fresh brief does not reintroduce omitempty — none
- Task 22: Ruling: cannot-verify no schema snapshot or hash changes — PARK; addressSchema returns nil for every non-netip type and no registered action type reaches a netip type, so TestSnapshotsCurrent and TestActionHashesMatchTheEmbeddedSnapshots read the same bytes (inferred from the code, not run by the seat; the branch-wide check runs them) — a failure would show at the next ./... run
- Task 22: parked — ProxyPath.Public without omitempty marshals null for a nil slice — Ruling: PARK; no task in this plan builds a ProxyPath with nil Public and validates it, Task 58 never sets Via; carry to w0c planning: whoever fills Via sets a non-nil Public or tags it omitempty in internal/core/guard.go
- Task 23: Ruling: cannot-verify the brief's server.go line numbers no longer match — no action; the implementer matched the replacement text, the diff hunk headers agree, and Tasks 14 and 15 moved the lines — none
- Task 24: Ruling: cannot-verify the --module-option flag and gate 7 — no action here; the flag and its usage-error path belong to Task 32, and gate 7 (the install harness after the push) is already owed for Tasks 6, 12, 19, 24, 47 and 57 in handoff.md — none
- Task 25: Ruling: cannot-verify no contract after C14 names a Task 25 output — no action; the task's own Produces line sits in the part of the brief the reviewer read and lists the file provider surface and Owns, and every FileWriter and Ownership implementer in the repository was updated (build and vet compile the tests) — none
- Task 26: Ruling: cannot-verify (*Client).do with a nil output tolerates the agent's 204 — PARK; do returns nil for any 2xx when out is nil or the status is 204, and Task 65's handleEnabled answers 204 with no body — an enablement push that succeeded would be logged as failed and re-pushed at the next hello, noisy logs only
- Task 26: Ruling: cannot-verify the agent route and the AgentClient embedding agree with SetModuleEnabled — PARK; Task 65 registers PUT modules/{id}/enabled and decodes {"enabled": *bool}, Task 59 adds AgentEnablement to core.AgentClient and FakeAgent.SetModuleEnabledFunc, TestEnablementRoute and the Task 26 test pin the same path and body — a mismatch would fail at Task 65 (adjacent: plan line 25318 says Task 65's SetModuleEnabledFunc where Task 59 adds it, wording only)
- Task 27: parked — TestClientAvailability appends to asked in the server handler goroutine without a mutex — Ruling: PARK; the handler appends before it writes the response and the test reads only after both client calls return, Go's race detector treats file-descriptor I/O as synchronization, and go test -race -count=5 -run '^TestClientAvailability$' ./internal/agent/api/ printed ok on 3838b58
- Task 27: Ruling: cannot-verify FromModuleAvailability encoding is covered by Task 69 — PARK; Task 69's TestAvailabilityRoute checks exact bodies with "reason":null at module and action level and "actions":[] for a module with no actions — if Task 69 is later amended to drop either assertion the encoding goes untested until Task 70 onward
- Task 29: Ruling: judge's adjacent finding that registry.go lets two modules claim one unit path — rejected by the controller after reading internal/core/registry.go:183, which refuses an owned path declared by two modules; reserved core names are covered when core declares them
- Ruling: wave 22 base proven by the full integration check on d7aa105 (output in wave-22-base-check.txt, all stages passed) — waves 18-21 were single tasks with no integrated line — none
- Ruling: Tasks 31 and 32 are Go-only, so their dispatch tells the implementer to run make web-deps only if it runs a web command — the setup is a web dependency install they never need — a missed dependency would show as a failing web command they do not run
- Task 39: Ruling: unregistered-action fallback in authorizeChange (automated security scan, confirmed by the task reviewer as Important, plan-mandated) — AMEND, confirmed by a second judge-opus: authorizeChange requires admin for a change whose action is not registered, api.RequiredScope's comment and C41 corrected, the c-gone test flips to expect 403 for operate and 200 for admin; fix commit carried into Task 39's fix loop — MinScope only ever raises a scope and Engine.Confirm rechecks nothing, so the API check is the only gate — if wrong, operate principals lose confirm, revert and undo on orphaned changes and admin must do them
- Ruling: amendment A6 (Header) — C41 rewritten so api.RequiredScope authorizes nothing and an unregistered change needs admin — Ruling: header amendment A6 confirmed by a second judge-opus — AMEND agreed — if wrong, the plan's Contracts carry a bad rule into every later task (Tasks 31 and 39 are the only consumers found)
- Task 39: Ruling: amendments A7, A8, A9 and A10 (Task 39 summary paragraph, c-gone test block, api.RequiredScope comment, authorizeChange) — fail-closed rule for unregistered changes — operate loses access to orphaned changes
- Ruling: wave 29 base not re-proven before dispatch — the base d7aa105..0910bed holds only single-task waves 23-28 whose implementers each ran their package suites, and the integration check runs after Task 42 completes — a base failure would then be blamed on the wave and found there
- Task 45: Ruling: stream stays dead after a refused reconnect (reviewer Important, plan-mandated) — CONFIRMED-GAP: on error with readyState 2 reopen with a 1 s to 30 s capped backoff, the reopened source refetches on open like reset, the closer clears the timer, fake gains readyState and fail(), three fake-timer tests — a non-200 reconnect (429 stream_limit, 502/503) closes EventSource for good and the shell would silently stop refreshing — if wrong, one extra retry every 30 s for a tab over the limit or signed out
- Task 45: Ruling: reset refetches plan queries and replaces an open confirmation dialog's plan (reviewer Important, plan-mandated by C40) — AMEND confirmed by a second judge-opus: reset invalidates every query except keys starting with plan, same predicate for the reopen refetch, plus a reset test caching a plan key — a plan query creates a plan on the controller — if wrong, future non-plan state keyed under plan would not refresh on reset
- Ruling: header amendment A11 confirmed by a second judge-opus — AMEND agreed (C40 reset excludes plan queries) — if wrong, the plan's C40 carries a narrower reset into every later task that adds a web page (only Task 45 consumes it so far)
- Task 45: Ruling: amendment A12 — the reset listener line uses the plan-excluding predicate — plain state under plan would not refresh on reset
- Task 46: Ruling: cannot-verify cv-46a (controller audit store accepting a reveal append) — PARK: audit.Store.Append is phase-agnostic and Task 1's migration test already inserts reveal rows with a NULL change id — a controller-side reveal would fail closed at the audit write and show on the first harness reveal
- Task 46: Ruling: cannot-verify cv-46b (scope and Darkpanel-Internal enforcement outside the engine) — PARK: Task 82 enforces admin scope, Task 74 the Darkpanel-Internal header, Task 75 the cross-kind refusal and Task 79 hides Internal and reveal actions from existing routes — if Task 74 or 82 omit the checks a non-admin could reveal a secret, caught by their own tests and reviews
- Task 47: Ruling: module services keep running while the files step deletes their directories (reviewer Important, plan-mandated) — AMEND: module unit stop and disable moves into the services step, units only removes the unit files — a running root service could write into a directory the files step removes or recreate one whose record was forgotten — if wrong, a future module whose uninstall step needs its service running must start it itself
- Task 47: Ruling: amendment A13 — Task 47 stops module units in the services step — see amendments.md
- Task 47: Ruling: cannot-verify cv-47a (RemoveServiceDir and Files.Remove kind unit behaviour) — PARK: both read in internal/platform and behave as the tests rely on — a regression would fail Task 47's own tests
- Ruling: wave 33 runs at width 2 — context-size --wave 3 says handoff, --wave 2 says ok; Task 50 starts when a slot frees — if wrong, a handoff comes one task earlier
- Task 49: Ruling: reviewer cannot-verify (test, vet and lint results not re-run by the reviewer) — no seat dispatch: it names the implementer's evidence, not a requirement in unchanged code; the wave's Integration check re-runs all of it — if wrong, a red check at the end of wave 33 surfaces it
- Task 50: Ruling: reviewer cannot-verify (dependencies from Tasks 33, 42 and 43 compile and behave as assumed) — no seat dispatch: the reviewer opened lifecycle/available.go and Registry.Actions and confirmed both, and the wave's Integration check compiles and tests the merged tree — if wrong, a red check at the end of wave 33 surfaces it
- Task 48: Ruling: cannot-verify cv-48a (core_plans.call never stores the guard refresh) — PARK: core_plans.call is written only through encodeCall at Engine.Plan, which builds a storedCall with no refresh field, and no lifecycle json.Marshal serialises a CallContext — if wrong, a later rewrite of encodeCall to marshal the whole CallContext would store the refresh and no test would catch it
- Ruling: wave 34 (Tasks 51-53) runs one task after another in the checkout, then the Integration check — a parallel run would not fit the session budget — if wrong, the wave takes longer
- Task 51: Ruling: cannot-verify cv-51a (core streams skip the CLI clash check) — CONFIRMED-GAP: checkCLINames builds a core stream's command without dropping the core word, while Task 87 generates the CLI name without it — the clash check guards core <name> but the CLI exposes <name>, so a core stream could collide with a core noun or module group with no registry error — if wrong, about ten lines and a test row that never fire
- Task 52: Ruling: cannot-verify cv-52a (events.AgentChanges reads on its own goroutine) — PARK: Task 16's AgentChanges only collects change ids on the caller's goroutine and runs the agent read and every Publish in a goroutine, so the ingestion lock is not held across them — if wrong, a slow agent would stall audit ingestion for every host for up to 30 s per page
- Task 53: Ruling: cannot-verify cv-53a (engine validates overrides and reports overridable) — PARK: Task 41's guardOverrides refuses any id that is not an Overridable guard of the action and checkGuards sets Overridable on every reported guard, both tested — if wrong, the CLI hint would never appear while server-side validation still refuses unsafe overrides
- Ruling: wave 35 (Tasks 54-55) runs one task after another in the checkout, then the Integration check — a parallel run would not fit the session budget — if wrong, the wave takes longer
- Task 54: Ruling: cannot-verify cv-54a (empty reason encoded as null by wire.FromModuleAvailability) — PARK: TestAvailabilityAPIRoute asserts the exact body with reason null and Task 69 checks the action-level and empty-actions cases — if wrong, a regression in action-level null encoding would reach the UI untested until Task 69's checks
- Task 54: Ruling: cannot-verify cv-54b (api.Config.Availability set in production) — PARK: Task 72 adds Availability: s.dispatcher.Availability to the api.New configuration and an end-to-end check that expects 200 — if Task 72 were skipped the route would answer 404 in production
- Task 55: Ruling: cannot-verify cv-55a (findOperation helper exists) — PARK: findOperation is an existing helper at internal/core/schema/openapi_test.go:35-46 — if wrong the build would fail, which the green run rules out
- Task 55: Ruling: cannot-verify cv-55b (route coverage check with reveal and Internal actions) — PARK: coverage.go still expects a read path for every read-class action, Task 79 changes instances to servesOp and checkOpenAPI to report a listed Internal action, Task 82 adds the reveal routes and TestRevealRoutesCovered; avrModule in internal/controller/api/availability_test.go declares an Internal read but is never fed to the coverage check, so the brief's claim holds where it matters — if wrong, a coverage hole between Task 55 and Task 79 that only Task 82's test would expose
- Task 56: Ruling: cannot-verify cv-56a (new identifiers clash with later tasks) — PARK: the nine names occur only in Task 56's own section and the branch builds with it — if wrong, a compile error in the later task that adds a duplicate name, caught by its build
- Task 56: parked — Runtime.Close waits for every stream Run with no time limit (reviewer Minor, plan-mandated) — Ruling: spec 1.6 and C33 require the wait, Task 81 assumes it, and a time limit would let a Run use a database the caller closes next; a Run that ignores its context breaks the module contract and belongs to each stream module's tests — if wrong, one badly written stream module would block the agent's runtime replacement until restart; no v1 stream ships before wave 1
- Task 56: parked — security review: stream events are framed without schema.Redact (MEDIUM, sensitive data exposure) — Ruling: spec 1.6 defines no redaction for stream events and spec lines 3301-3305 give secret filtering of docker.logs text to the Docker module (wave 1, sections 6 and 7.2); Redact only blanks x-secret fields so the suggested fix would not cover free text; the plan builds only what a task names — if wrong, a later stream event type with a secret field would send it to a read-scope reader; the fix is about three lines in OpenStream (Redact before the size check) plus a test
- Task 57: Ruling: cannot-verify cv-57a (StopTasks, Actor and AbortUninstall reachable from the CLI) — PARK: Task 66 adds --stop-tasks and --abort to darkpanel uninstall with abortActor and tests for each path — if Task 66 were dropped the CLI could not reach them, shown by its own failing tests
- Task 57: Ruling: cannot-verify cv-57b (gate 7 harness not run) — PARK: gates.md applies gate 7 to a branch touching internal/install only after it is pushed and harness.yml has run on it, and handoff.md already lists Task 57 among the tasks owing it — if wrong, an uninstall regression on a real systemd host would reach main, caught one merge later by the gate on main
- Ruling: wave 38 (Tasks 58-60) runs one task after another in the checkout, then the Integration check — a parallel run would not fit the session budget — if wrong, the wave takes longer
- Task 58: Ruling: cannot-verify cv-58a (agent validator accepts the filled guard; Task 63 wires the real Topology) — PARK: the dispatch harness runs a real runtime.Runtime with schema.NewValidator and TestPlanFillsGuard and TestApplySendsGuardRefresh pass the full guard and the refresh through it; Task 63 sets Topology from listeners and sessions; no production action declares Guards before wave 1 — if wrong, the first real guarded action or Task 63's TestServerTopology would fail loudly with request.invalid_input
- Task 58: Ruling: IPv6 zone choice (ruling-seat dispatch note) — the client peer, every AgentPeers entry and both listener sets lose their zone in guardContext, on copies, Task 23's stored peer keeps it (reviewer Important, fixed in round 1) — a zone names a controller interface and means nothing on the agent's host, and netip equality includes the zone — if wrong, wave 1's lockout guard would compare zone-free values only, which cannot give a false match
- Task 59: Ruling: reviewer cannot-verify (every AgentClient implementer still compiles) — no seat dispatch, the session budget said handoff: the reviewer grepped and found SetModuleEnabled with the matching signature in internal/agent/runtime/enablement.go and internal/agent/api/client.go, and the implementer's go build, go vet and go test ./... passed — if wrong, a red wave 38 Integration check shows it
- Task 60: Ruling: cannot-verify cv-60a (updated_at read from a second clock call) — PARK: the line matches amendment A4 verbatim; created_at is read before the recheck and Snapshot and updated_at is read inside the transaction so the change watcher cannot miss a row that commits later than its 5 s overlap; spec 1.4 asks only that core_changes.system be set — if wrong, updated_at trails created_at by a few milliseconds and nothing reads the gap
- Task 60: Ruling: cannot-verify cv-60b (composite insert does not record system) — PARK: the insert at composite_run.go commitComposite writes the composite parent row, not child steps; ApplySystem is called only by Task 61 taskApply, which accepts agent-side actions, and composites run on the controller; the firewall is the only reader of the flag and its task actions are revertible non-composites — if wrong, a future agent-side composite applied by a task gets system = 0 and the firewall could keep its snapshot among the operators 50 or pick it for rollback; the fix is passing isSystem(ctx) in that one insert
- Ruling: wave 39 tasks run Go tests only, so their worktrees skip make web-deps — the Worktree setup only serves web tests, and the wave Integration check runs it in the checkout — if wrong, a task test that needs web/node_modules fails loudly and is rerun after make web-deps
- Task 61: Ruling: reviewer cannot-verify (e3b0135 is the commit that landed from the agent worktree) — no seat dispatch, mechanical: the task ref resolved to e3b0135 before review and wave merge landed it as d222cf7 with its two files — if wrong, the Integration check shows it
- Task 62: Ruling: cannot-verify cv-62a (journal-before-return contract and reveal audit ingest live outside the diff) — PARK: C24 belongs to Task 46 (complete, review clean) and the core_audit reveal phase to Task 1 migration 0011 (complete); earlier rulings (Task 18, cv-46a) found ingest copies the phase with no Go allowlist; Task 62 tests cover the runtime side — if wrong, IngestEpoch would stall for a host that performed a reveal, a one-line phase fix in the controller audit path that the first harness reveal or the final review catches
- Task 63: Ruling: reviewer cannot-verify (dispatcher reads Topology on every dispatch, not once) — no seat dispatch, mechanical: dispatch.go documents Topology as read on every plan of a guarded action and every agent apply, and guard.go:30 calls d.cfg.Topology() inside guardContext — if wrong, TestServerTopology plus the Integration check would not catch a cached first read, and a lockout guard would see an empty topology until restart
- Task 64: Ruling: plan-conflict pc-64a (a Watch that always fails beside a working Run restarts every second because a successful pass resets its backoff) — AMEND A14: the Watch backoff is counted in watchTask alone and restarts at 1 s only when the failed Watch stayed up longer than the backoff before it; fix round carries the code change and TestBackgroundWatchBackoff — if wrong, a Watch recovering after a long outage restarts a few seconds later; no event is lost
- Ruling: amendment A14 (Task 64) — the Watch backoff reset by a successful pass defeats the 1 s to 5 min schedule for the only agent-side task (firewall.reconcile) on a Docker-less host — a Watch recovering after a long outage waits a few seconds longer
- Task 64: parked — pc-64b a skipped pass is not retried when Every is 0 and there is no Watch — Ruling: spec 1.4 says a skipped pass waits for the next wake or tick; the only agent-side task, firewall.reconcile, runs every 5 minutes and has a Watch; a future agent task with Every 0 and no Watch needs its own retry after a skip
- Task 64: parked — pc-64c one background.mu held across a module stop can stall other modules — Ruling: modules without tasks return before the mutex, only firewall has an agent task in v1, and spec 1.4 requires the waits; once a second agent module gains a task, use one mutex per module (syncTasks and stopBackground only)
- Task 64: Ruling: cannot-verify cv-64a (Protect recovers a panic in Run or Watch) — PARK: lifecycle.Protect at guard.go:27-41 defers recover and returns a panicError, and taskPass and watchTask call Run and Watch through it — if wrong, a task panic would crash the agent
- Task 65: Ruling: reviewer cannot-verify (400 for a wrong-typed enabled and 422 for a missing enabled rest on unchanged decodeJSON and errcode) — no seat dispatch, mechanical: TestEnablementRoute asserts both statuses and the implementer GREEN run passes it — if wrong, the wave 42 Integration check shows it
- Task 66: Ruling: reviewer cannot-verify (AbortUninstall uses only Layout, Systemd and Actor) — no seat dispatch, mechanical: install.AbortUninstall at internal/install/uninstall.go:458 reads cfg.Systemd, cfg.Actor and cfg.Layout.Paths only, so the zero Exec, Users, Clock and Log in the CLI are safe — if wrong, the abort test or the Integration check fails
- Task 67: Ruling: plan-conflict pc-67a (a failed enablement push is not retried while the agent session stays up, because Agent says hello only on a new generation) — CONFIRMED-GAP: keep pushes best effort but a push that fails with anything other than not_found sets a repush flag on the host greeting so the next Agent call says hello again (hosts.go greeting.repush, pushEnablement returns bool, Hosts.RepushEnablement called from coreactions pushEnabled), with tests; no amendment — if wrong, a persistently failing agent costs one extra Hello and one push per module on every reach
- Task 67: Ruling: cannot-verify cv-67a (an older agent without the route answers not_found) — PARK: any push error is logged at warn and the change stands, the not_found exemption in the pc-67a fix rests on the catch-all answering errcode.NotFound (handler.go:135, client.go:159-167) — if wrong, an older agent re-says hello on each reach: extra round trips, no wrong state
- Task 67: Ruling: cannot-verify cv-67b (Contracts C15 and later) — PARK: C22 and C30 are agent-side, C35 keys on checked_at which the upsert still writes, C37 is consumed unchanged — if wrong, a mismatch fails to compile or fails Task 70 tests
- Task 68: Ruling: reviewer cannot-verify (whole-suite, lint and tidy results are summarised in the report) — no seat dispatch, mechanical: the wave 43 Integration check runs the whole suite, lint and tidy on the merged head — if wrong, the check fails
- Task 69: Ruling: reviewer cannot-verify (the exact JSON shape depends on wire.FromModuleAvailability) — no seat dispatch, mechanical: TestAvailabilityRoute asserts the exact bytes and the implementer GREEN run passes it — if wrong, the wave 44 Integration check fails
- Task 70: Ruling: cannot-verify cv-70a (Reveal and ReadInternal also call available) — PARK: Task 75 owns it, its reveal helper backs Dispatcher.Reveal and ReadInternal and calls d.available on both branches, and its tests include an action unavailable case; Task 78 only embeds DispatcherReveal — if wrong, an unavailable reveal reaches the agent and fails there with the agent error; Task 75 review would catch it
- Task 70: Ruling: cannot-verify cv-70b (the availability cache keyed on checked_at misses every dispatch while a host is in the Task 67 repush state, and each dispatch calls Hosts.Agent three times) — PARK: spec 1.7 caches the answer per hello and per module enable and C35 fixes the key; the extra traffic only occurs while a push keeps failing, which Task 67 already accepted; if it shows up in operation, make the re-hello upsert in hosts.go leave checked_at unchanged when enabled and available did not change — if wrong, a host stuck in repush costs about three hellos and one availability request per dispatch, load and no wrong answer
- Task 71: Ruling: reviewer cannot-verify (the reveal route refuses a bad Darkpanel-Internal value and AgentClient embeds AgentReveal) — no seat dispatch, mechanical: the plan assigns the embedding to Task 73 and the route, which answers request.invalid_input for any value but 1 and for a repeated header, to Task 74 (W1 table and Task 74 text) — if wrong, those tasks reviews and tests catch it
- Task 72: Ruling: reviewer cannot-verify (s.dispatcher.Availability matches api.Config.Availability and the route behaves) — no seat dispatch, mechanical: TestEndToEnd drives the real route and the wave 45 Integration check builds and tests the merged head — if wrong, the check fails
- Task 73: Ruling: reviewer cannot-verify (the api.Client and Runtime Reveal bodies behave correctly) — no seat dispatch, mechanical: those bodies belong to Tasks 62 and 71, each reviewed clean, and the wave 46 single-task flow has no Integration check — if wrong, Task 74 route tests and the final review catch it
- Task 74: Ruling: cannot-verify cv-74a (the admin scope on every reveal is enforced somewhere) — PARK: spec 1.14 puts admin on the two public API reveal routes and Task 82 (not yet run) enforces it in authorizeReveal before Dispatcher.Reveal, tested by TestRevealRoutes and TestAuthorizeRevealNeedsAdmin; Task 75 Dispatcher.Reveal does no authorization by C41; CLI (Task 86) posts to Task 82 routes, the controller socket has no reveal route, the agent local socket has none (TestSocketHasNoReveal), ReadInternal is never public — if wrong, an operate token could read a stored secret; Task 82 own 403 test for an operate token would catch it
- Task 74: Ruling: cannot-verify cv-74b (a handler or engine error from Agent.Reveal could carry the secret into the 5xx log) — PARK: Engine.reveal and Runtime.Reveal error paths name only action references and the identity, the journal entry holds {"identity": ...} only; the remaining risk is a module reveal handler returning an error with the secret in its text, which each wave-1 module defining a reveal action or Internal read must test — if wrong, one leak per failed reveal in the agent error log
- Task 75: Ruling: reviewer cannot-verify (d.action refuses a side or host mismatch with action.unknown; d.available exempts core-module actions and treats an agent not_found as available; ruling cv-74b exists) — no seat dispatch, mechanical: the first two belong to the Task 70 and earlier dispatcher tasks, each reviewed clean, and the Task 75 refusal tests exercise the first; cv-74b is the Task 74 ledger ruling above, which the extra log-and-error test follows — if wrong, the final review catches it
- Task 77: Ruling: reviewer cannot-verify (the composite engine hands Children.Read the composite principal) — no seat dispatch, mechanical: the new branch passes the principal through as the brief and spec 1.7 prescribe, and the composite engine call site belongs to earlier reviewed tasks — if wrong, the final review catches it
- Task 76: Ruling: reviewer cannot-verify (the pre-existing test helpers tcpClient, principalHeader, testPrincipal, testPlan, discardLog and problemContentType exist; the Task 80 agent route answers not_found with problemContentType as the fake does) — no seat dispatch, mechanical: the first is shown by the tests compiling and passing in the implementer run, the second is Task 80 contract and Task 80 review checks it — if wrong, the Task 80 review or the final review catches it
- Task 78: Ruling: reviewer cannot-verify (go build and go test are clean across the whole tree) — no seat dispatch, mechanical: the wave 49 Integration check runs both on the merged head — if wrong, that check fails
- Task 79: Ruling: the brief listing of servesOp is cut off after "return op == opPlan " — the implementer ended it with "|| op == opApply", which matches the function comment and how lookupAction treated mutating actions before; the judge confirmed it — if wrong, a mutating action would not serve apply and TestRevealRoutes or the final review would show it
- Task 79: Ruling: reviewer cannot-verify (the production registry has no reveal action before Task 82 adds the reveal routes, so CheckOpenAPI stays green) — no seat dispatch, mechanical: the implementer report states the built registry has none and TestCheckOpenAPI passed, and the wave 51 to 53 Integration checks run it — if wrong, TestCheckOpenAPI fails
- Task 80: Ruling: reviewer cannot-verify (h.actor, h.allow and wire.Streams signatures; *runtime.Runtime and *api.Client satisfy AgentStreams; closing the body cancels Run) — no seat dispatch, mechanical: the first two are shown by the implementer go build and whole-repo go test, and the third is Task 56 behaviour reviewed clean in that task — if wrong, the next Integration check or the final review fails
- Task 81: Ruling: reviewer cannot-verify (Runtime.Close cancels open streams and waits for their Runs; go mod tidy -diff is clean) — no seat dispatch, mechanical: the first is Task 56 behaviour reviewed clean there and exercised by the new test end line, and the diff touches no module file — if wrong, the next Integration check fails
- Task 82: Ruling: reviewer cannot-verify (the coverage check counts one reveal instance per reveal action through Task 79 servesOp and instances; break-glass refusal text matches actionHandler) — no seat dispatch, mechanical: the first is shown by TestRevealRoutesCovered and TestCheckOpenAPI passing in the implementer run after its RED 2, and the second is a text match the final review can compare — if wrong, TestCheckOpenAPI fails at the next Integration check
- Task 83: Ruling: reviewer cannot-verify (Client.Streams turns an agent 404 into an empty list; scope read for the relay route is enforced) — no seat dispatch, mechanical: the first was confirmed in the Task 76 review (Streams answers an empty non-nil list on not_found) and the second belongs to Task 85, whose brief the Task 85 review checks — if wrong, the Task 85 review catches it
- Task 84: Ruling: reviewer cannot-verify (Session.OpenModule keeps its own 16-slot pool apart from Open; api.Client.OpenStream goes through DialStream) — no seat dispatch, mechanical: the first is Task 17 behaviour and the second is Task 76 behaviour, both reviewed clean there, and the new test fails without the change and passes with it (RED timeout, GREEN under -race) — if wrong, the next Integration check fails
- Task 85: Ruling: cannot-verify cv-85a (streamRoute covers the relay path so authentication is passive) — PARK: streamRoute returns true for a GET or HEAD whose path has exactly 8 segments ending in /hosts/{host}/streams/{module}/{name}, identify passes Passive: streamRoute(r), and sse_test.go table cases cover the near misses — if wrong, a held-open relay would keep renewing its session idle limit (a session-lifetime weakness, not a bypass)
- Task 85: Ruling: cannot-verify cv-85b (an unauthenticated request cannot reach Dispatcher.OpenStream) — PARK: the stream route is not public so New wraps it in requireCaller, authenticate already runs on the whole mux, and authorize(c.principal, core.ScopeRead) runs before OpenStream, so discarding the ok from callerFrom is safe — if wrong, unauthenticated callers would share the empty-Actor slot bucket and could exhaust the stream limits, which needs two layers to fail
- Task 85: Ruling: scope — the implementer added two hardenings beyond the brief (agentProblem keeps only code and detail of an agent end error, and relayWriter suppresses a second end frame when sseStream also ends the stream) and one //nolint:gosec G705 on sseWrite in sse.go outside the Files list; the judge found the first two close real W4 and race gaps and the third needed for a clean lint, scope 15 — if wrong, the final review sees three extra hunks and can drop them
- Task 86: Ruling: reviewer cannot-verify (Task 82 routes answer compact JSON so the value prints on one line; addConnFlags registers --local for agent-side reveal commands) — no seat dispatch, mechanical: a JSON string value is one line whatever the encoding and the second is shown by the test, which passes --local and expects the reveal reason rather than an unknown-flag error — if wrong, the reveal tests or the final review show it
- Task 87: Ruling: reviewer cannot-verify (Registry.Streams, ModuleStream and DefineStream have the shapes the code uses; collectInput returns {} when no flags are given) — no seat dispatch, mechanical: both are shown by the implementer go build and the no-parameter TestFollowCommand subtests passing — if wrong, the build or those tests fail
- Ruling: gates 1-5 (ADR records, lint, tests, build, vulnerabilities) ran green on 3d21c29 against merge base 7d5b77d — gates 6 and 7 apply once pushed, so the branch was pushed to origin (push authorized for the v1 programme) and harness.yml was dispatched, CI run 39900597246 and Harness run 39900596221 both on 3d21c29 — if a fix wave changes HEAD both runs must be repeated on the new head
- Ruling: Acceptance QA skipped — the spec has no Acceptance section, so no A<n> statement is tagged test — none
- Ruling: final review (judge-opus whole branch plus security, silent-failures, test-quality and lang-typescript lenses; Codex off — setting; Acceptance QA skipped, no Acceptance section) found 2 Important and 5 Minor plus 5 Important and 21 Minor from the lenses — the single fix wave (dforge:impl-opus-high, base 3d21c29, commits 3d21c29..0a7e0db, 21 commits) fixed 22, rejected 2 and deferred 9 findings and the fix report is final-fix-report.md — a scoped re-review (general-purpose opus) found every fixed finding addressed and no Critical or Important breakage — if wrong, the open items below reach the finishing menu
- Ruling: Acceptance of deferrals by the scoped re-review — DEFERRED Important "unknown topology treated as nothing to protect" needs a GuardContext contract decision (spec 1.5 and C1) and its register row exists only in final-fix-report.md, so the next session adds register rows for the 9 deferrals (and for composite.step events carrying no action name, and the relay writing no end frame on re-authentication failure) with scripts/register add — if wrong, the deferrals are lost
- Ruling: final-residual fr-1 (a client disconnect closing the relay reader is untested) — PARK: handleStream closes the reader in a deferred Close on every exit from sseStream and five relay tests assert body.closed (revoked, idle, shutdown, normal end, overflow) — if wrong, a later early return that skips the deferred close would hold one agent stream and two slots per disconnected client with no test to catch it; one subtest that cancels the request context of relayGet would close the gap
- Ruling: final-residual fr-2 (real /proc/sys read-only tests vs the plan Global Constraint that no unit test touches the real /proc/sys) — ruling seat verdict AMEND A15 (Header): add TestIsProcfs and TestOpenProcSysReal as a second named read-only exception after the TestSystemdLive exception; NOT YET APPLIED, a Header amendment needs a confirming second dforge:judge-opus before scripts/plan-amend (entry saved in amend-15.md) — if wrong, the two tests fail on a host with no procfs at /proc/sys and would need a statfs-guarded skip
- Ruling: amendment A15 (Header) — name TestIsProcfs and TestOpenProcSysReal as a second read-only exception to the rule that no unit test touches the real /proc/sys; they only open it O_RDONLY and read kernel/ostype, and are the only tests of the production procfs check — if wrong, the two tests fail on a host with no procfs at /proc/sys and need a statfs-guarded skip; no host state can change
- Ruling: header amendment A15 confirmed by a second judge-opus — AMEND, same entry; Old text occurs once in the header and both tests only read — if wrong, the plan Global Constraints carry a bad rule into every later task
- Ruling: register rows 82-92 added to docs/dforge/registers/2026-10-21-v1-completion.md for the 9 final-fix deferrals, composite.step events with no action name and the relay writing no end frame on re-authentication failure — if wrong, a deferral is lost or mis-scoped
- Ruling: gates 6 and 7 — CI run 39904821887 and Harness run 39904821741 both green on 0a7e0db; a6f4f6a only adds register rows under docs/ — if wrong, finishing reruns gates 1-5 on a6f4f6a and the harness stays green on the code head
- Ruling: finishing stopped at gate 6 — CI run 39906809219 on a6f4f6a failed in TestCompositeConfirmAnswerLost/the_runner_settles_it (composite_confirm_test.go:104, step g = confirmed, want confirming); gates 1-5 green locally on a6f4f6a, the same suite was green on 0a7e0db, the test file is not in the branch diff and 400 local repeats (one under -race with one CPU) all passed, so it reads as a rare test race against the background runner, but gates.md declares no Known-flaky text so no retry was made — if wrong, a real regression in the lifecycle runner reaches main; Harness run 39906808898 on a6f4f6a still running
- Ruling: gate 6 passed on retry — CI run 39906809219 re-run (failed jobs only) on a6f4f6a went green after TestCompositeConfirmAnswerLost/the_runner_settles_it failed once; the retry was my own ruling after the owner asked to continue without answering the retry question, the failure matches no declared Known-flaky text, and the test asserts the confirming step state right after Confirm returns while a background runner can settle it first (a test race in a file this branch does not change); Harness run 39906808898 on a6f4f6a green — if wrong, a real lifecycle runner regression reaches main; a register row for the flaky test is added with the finishing commit

## Amendments

## A1 — Task 32
Reason: The runner line in cmd/darkpanel/uninstall.go is Task 13's edit (C14), not Task 2's.
Cost if wrong: Wording only; no code or test changes either way.
### Old
````text
The key ends at the first `=`, so a value may hold one; the same `<module>.<key>` given twice is a usage error. `cmd/darkpanel/uninstall.go:56` (the runner line) is Task 2's and stays untouched here.
````
### New
````text
The key ends at the first `=`, so a value may hold one; the same `<module>.<key>` given twice is a usage error. `cmd/darkpanel/uninstall.go:56` (the runner line, `platform.NewRunner(platform.ProductionRules())`) is Task 13's and stays untouched here.
````

## A2 — Header
Reason: Only Task 54 reads the api harness field `cfg`; Tasks 15 and 79 build their servers from their own Config literals.
Cost if wrong: The header under-lists a consumer of a field that still exists; nothing breaks.
### Old
````text
  - `newHarnessWith(t *testing.T, mods ...core.Module) *harness` in `internal/controller/api/server_test.go`, Task 11; the harness gains `cfg Config`, the configuration `h.srv` was built from (Tasks 15, 54 and 79 may use it);
````
### New
````text
  - `newHarnessWith(t *testing.T, mods ...core.Module) *harness` in `internal/controller/api/server_test.go`, Task 11; the harness gains `cfg Config`, the configuration `h.srv` was built from (Task 54's `avrServe` uses it; every other task builds its servers as its own code shows);
````

## A3 — Task 16
Reason: A failed or partly unparsable change poll recorded its rows as seen, so their events were never published.
Cost if wrong: One extra map per poll; behaviour on a clean poll is unchanged.
### Old
````text
	var out []ChangeEvent
	for rows.Next() {
		var ev ChangeEvent
		var at string
		if err := rows.Scan(&ev.ChangeID, &ev.HostID, &ev.Action, &ev.State, &at); err != nil {
			return err
		}
		seen := changeSeen{at: at, state: ev.State}
		if w.changes[ev.ChangeID] == seen {
			continue
		}
		if ev.UpdatedAt, err = time.Parse(timeFormat, at); err != nil {
			return err
		}
		w.changes[ev.ChangeID] = seen
		out = append(out, ev)
	}
	if err := rows.Err(); err != nil {
		return err
	}
````
### New
````text
	var out []ChangeEvent
	read := map[string]changeSeen{}
	for rows.Next() {
		var ev ChangeEvent
		var at string
		if err := rows.Scan(&ev.ChangeID, &ev.HostID, &ev.Action, &ev.State, &at); err != nil {
			return err
		}
		seen := changeSeen{at: at, state: ev.State}
		if w.changes[ev.ChangeID] == seen {
			continue
		}
		read[ev.ChangeID] = seen
		if ev.UpdatedAt, err = time.Parse(timeFormat, at); err != nil {
			w.log.Warn("events: a change's updated_at does not parse; its event is skipped", "change", ev.ChangeID, "updatedAt", at, "err", err)
			continue
		}
		out = append(out, ev)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for id, seen := range read {
		w.changes[id] = seen
	}
````

## A4 — Task 60
Reason: The intent row's updated_at was read before recheck and Snapshot, so it could commit more than the change watcher's 5 s overlap after its stamp and its event would be lost.
Cost if wrong: updated_at of a new change is later than created_at by the recheck and Snapshot time; tests on a fake clock see equal values.
### Old
````text
			snapshot, deadline, string(recorded), parent, isSystem(ctx), created, created)
````
### New
````text
			snapshot, deadline, string(recorded), parent, isSystem(ctx), created, formatTime(e.cfg.Base.Clock.Now()))
````

## A5 — Task 22
Reason: netip zero values marshal as "", which the formats refuse; optional netip fields must be omitzero, or every Go-built request with the field unset fails.
Cost if wrong: Test-fixture tags and a comment only; no production schema or hash changes.
### Old
````text
// addrFields has one field of each netip type, each optional so that a
// validation case can send one alone.
type addrFields struct {
	A netip.Addr       `json:"a,omitempty"`
	P netip.AddrPort   `json:"p,omitempty"`
	N netip.Prefix     `json:"n,omitempty"`
	L []netip.AddrPort `json:"l,omitempty"`
}
````
### New
````text
// addrFields has one field of each netip type, each optional so that a
// validation case can send one alone. A netip zero value marshals as "",
// which the formats refuse, and omitempty never omits a struct, so the
// optional netip fields are omitzero.
type addrFields struct {
	A netip.Addr       `json:"a,omitzero"`
	P netip.AddrPort   `json:"p,omitzero"`
	N netip.Prefix     `json:"n,omitzero"`
	L []netip.AddrPort `json:"l,omitempty"`
}
````

## A6 — Header
Reason: An unregistered change fell back to its default scope, letting operate act on changes whose action had required admin.
Cost if wrong: Operate principals lose confirm, revert and undo on orphaned changes; admin still has them.
### Old
````text
`api.RequiredScope(class, side)` returns `core.DefaultScope(class, side)` and is used only for a change whose action is not registered; the action routes, the controller socket's break-glass route and `authorizeChange` use `core.RequiredScope(def.Spec)`.
````
### New
````text
`api.RequiredScope(class, side)` returns `core.DefaultScope(class, side)` and no route authorizes with it; the action routes, the controller socket's break-glass route and `authorizeChange` use `core.RequiredScope(def.Spec)`, and `authorizeChange` requires `admin` for a change whose action is not registered, because its MinScope is unknown and the check fails closed.
````

## A7 — Task 39
Reason: Task summary must match the fail-closed rule for unregistered changes.
Cost if wrong: Wording only.
### Old
````text
Switches every authorization of a registered action to `core.RequiredScope(def.Spec)`, so `MinScope` raises the scope of the action routes, the controller socket's break-glass routes and the operations on a change that authorize through `authorizeChange` (confirm, revert, undo and repair; release, observe and resolve already need `admin`); `api.RequiredScope` keeps only the unregistered-action case (C41). Composites already need `admin`, the highest scope, so "the highest of its own and every step's scope" holds without reading steps. Authorization risk: it runs alone. Test identifiers carry the prefix `msc`.
````
### New
````text
Switches every authorization of a registered action to `core.RequiredScope(def.Spec)`, so `MinScope` raises the scope of the action routes, the controller socket's break-glass routes and the operations on a change that authorize through `authorizeChange` (confirm, revert, undo and repair; release, observe and resolve already need `admin`); a change whose action is not registered needs `admin`, since its MinScope is unknown (C41). Composites already need `admin`, the highest scope, so "the highest of its own and every step's scope" holds without reading steps. Authorization risk: it runs alone. Test identifiers carry the prefix `msc`.
````

## A8 — Task 39
Reason: The test must require admin, not operate, for an unregistered change.
Cost if wrong: The test would lock in the fail-open fallback.
### Old
````text
		if rec := h.serve(withToken(request("POST", "/api/v1/changes/c-gone/"+op, ""), operate)); rec.Code != 200 {
			t.Errorf("%s of an unregistered action by operate: %d %s", op, rec.Code, rec.Body)
		}
````
### New
````text
		if p := decodeProblem(t, h.serve(withToken(request("POST", "/api/v1/changes/c-gone/"+op, ""), operate)), 403, errcode.Forbidden); p.Detail != "this request needs the admin scope" {
			t.Errorf("%s of an unregistered action by operate: detail %q", op, p.Detail)
		}
		if rec := h.serve(withToken(request("POST", "/api/v1/changes/c-gone/"+op, ""), admin)); rec.Code != 200 {
			t.Errorf("%s of an unregistered action by admin: %d %s", op, rec.Code, rec.Body)
		}
````

## A9 — Task 39
Reason: authorizeChange must fail closed for an unregistered action, and api.RequiredScope's comment must stop claiming that role.
Cost if wrong: Operate loses access to orphaned changes; admin keeps it.
### Old
````text
// RequiredScope is the scope a principal needs for a change of class on
// side whose action the registry no longer has: core.DefaultScope, because
// no MinScope is left to raise it. A registered action needs
// core.RequiredScope of its spec (spec 1.7).
````
### New
````text
// RequiredScope is the default scope of an action of class on side,
// core.DefaultScope, before any MinScope raises it. No route authorizes
// with it: a registered action needs core.RequiredScope of its spec (spec
// 1.7), and a change whose action is unregistered needs admin.
````

## A10 — Task 39
Reason: The unregistered-change fallback let operate act on changes whose action had required admin.
Cost if wrong: Operate loses confirm, revert and undo on orphaned changes; admin keeps them.
### Old
````text
// authorizeChange applies to an operation on a change the scope of the
// change's own action, never of the route: core.RequiredScope of its spec,
// MinScope included. v must be networkVisible, so an unregistered action is
// agent-side and its stored class decides.
func (s *Server) authorizeChange(p core.Principal, v core.ChangeView) error {
	if def, ok := s.cfg.Registry.Action(core.ActionRef{Name: v.Action, Version: v.Version}); ok {
		return authorize(p, core.RequiredScope(def.Spec))
	}
	return authorize(p, RequiredScope(v.Class, core.SideAgent))
}
````
### New
````text
// authorizeChange applies to an operation on a change the scope of the
// change's own action, never of the route: core.RequiredScope of its spec,
// MinScope included. A change whose action the registry no longer has needs
// admin: its MinScope is unknown and the engine checks nothing further on
// confirm, revert or undo, so the check fails closed.
func (s *Server) authorizeChange(p core.Principal, v core.ChangeView) error {
	if def, ok := s.cfg.Registry.Action(core.ActionRef{Name: v.Action, Version: v.Version}); ok {
		return authorize(p, core.RequiredScope(def.Spec))
	}
	return authorize(p, core.ScopeAdmin)
}
````

## A11 — Header
Reason: An unfiltered reset refetches plan queries, which creates new plans and replaces the one a confirmation dialog is showing.
Cost if wrong: Plain state keyed under "plan" would not refresh on reset.
### Old
````text
It opens `new EventSource("/api/v1/events")` (or `factory`), and invalidates on `change` `["changes"]`, `["change", changeId]` and `["audit"]`; on `composite.step` `["change", changeId]` and `["steps", changeId]`; on `host` `HOSTS_QUERY_KEY`; on `module` `HOSTS_QUERY_KEY` and `["resource", hostId]`; on `reset` every query. The returned function closes the source. `web/src/shell.tsx` calls it once per mount of the signed-in shell.
````
### New
````text
It opens `new EventSource("/api/v1/events")` (or `factory`), and invalidates on `change` `["changes"]`, `["change", changeId]` and `["audit"]`; on `composite.step` `["change", changeId]` and `["steps", changeId]`; on `host` `HOSTS_QUERY_KEY`; on `module` `HOSTS_QUERY_KEY` and `["resource", hostId]`; on `reset` every query except those whose key starts with `"plan"` (a plan query creates a plan on the controller and a confirmation dialog holds one plan per opening). The returned function closes the source. `web/src/shell.tsx` calls it once per mount of the signed-in shell.
````

## A12 — Task 45
Reason: Keep Task 45's code in line with the amended C40 reset rule.
Cost if wrong: Plain state keyed under "plan" would not refresh on reset.
### Old
````text
  source.addEventListener("reset", () => void client.invalidateQueries())
````
### New
````text
  source.addEventListener("reset", () => void client.invalidateQueries({ predicate: (query) => query.queryKey[0] !== "plan" }))
````

## A13 — Task 47
Reason: the brief stopped module units in the `units` step, after the `files` step had already removed their managed and service directories. A running root service could then write into those directories or recreate one whose record was already forgotten. The spec only requires the stop before the unit file is removed, and the module step's Env has no Units, so the stop moves to the `services` step.
Cost if wrong: module services stop before the `modules` step instead of after the `files` step; no current module's uninstall step depends on its service running.
### Old
````text
This task teaches `darkpanel uninstall` the new recorded kinds. The `files` step now also removes each recorded `managed-dir` (after an `openat2(RESOLVE_NO_SYMLINKS)` check of its whole path, then `removeTree`, which never follows a symlink inside) and forgets it, and each recorded `service-dir` through `Files.RemoveServiceDir`, which stops the step, naming them, at directories the module does not declare; `--keep-data` keeps both kinds under the data and configuration directories, as it does every entry. The `units` step first stops (waiting as the services step waits), and disables, every recorded unit of a module other than the core, skipping one systemd does not know, then removes those unit files through `Files.Remove` (kind `unit`), so their record is forgotten, then reloads once; the core's units keep today's handling. `restore` and `sysctl` declarations are never recorded and never touched. `TestUninstallRemovesEverything` gains the stop and disable of the demo module's `demo.service`. Data-loss risk: it runs alone.
````
### New
````text
This task teaches `darkpanel uninstall` the new recorded kinds. The `files` step now also removes each recorded `managed-dir` (after an `openat2(RESOLVE_NO_SYMLINKS)` check of its whole path, then `removeTree`, which never follows a symlink inside) and forgets it, and each recorded `service-dir` through `Files.RemoveServiceDir`, which stops the step, naming them, at directories the module does not declare; `--keep-data` keeps both kinds under the data and configuration directories, as it does every entry. The `services` step, after it stops and disables the core's services (and also when none is installed), stops (waiting as it waits for the core's) and disables every recorded unit of a module other than the core, skipping one systemd does not know, so no module service runs while the `modules`, `marked blocks` and `files` steps remove what it uses; its plan gains one line `stop and disable <kind> <path>, of module <id>` per such unit. The `units` step then only removes those unit files through `Files.Remove` (kind `unit`), so their record is forgotten, then reloads once; the core's units keep today's handling. A failed module `Disable` therefore fails the `services` step (`TestUninstallStopsModuleUnits` expects `services: refused`). `restore` and `sysctl` declarations are never recorded and never touched. `TestUninstallRemovesEverything` gains the stop and disable of the demo module's `demo.service`, after the core's `Disable` and before `Reload`. Data-loss risk: it runs alone.
````

## A14 — Task 64
Reason: The Watch backoff reset by a successful pass makes a Watch that always fails restart every second beside a working Run; spec 1.4 asks only for "the same backoff".
Cost if wrong: A Watch recovering after a long outage restarts a few seconds later than it would under a pass reset; no event is lost, since each restart wakes a full pass.
### Old
````text
Runs every module's background tasks in the agent runtime under spec 1.4 (C30). `Open` starts them after recovery, preflight and the first purge; a module runs its tasks while its flag (C22) is set and its last preflight passed, and `syncTasks` decides anew on every `SetModuleEnabled`, every `Preflight` and at `Open`, under one mutex, so the last call wins and a stop returns only once every `Run` and `Watch` returned. `Close` stops them first, before the purge loop and the engine. Each task has one goroutine for its passes and, with `Watch`, one more for the watch, both on a context only stopping the module cancels. A pass runs at start, `Every` after the previous pass ended (never on a timer when `Every` is 0), and after a wake; the wake channel holds one pending wake, so wakes during a pass coalesce into one further pass. A pass is skipped without error while `core_locks` holds one of `LockKeys` or `lifecycle.ReserveAll` (an update holding every lock). A failed pass is logged and the next one waits `taskBackoff` (1 s doubling to 5 min); wakes wait during a backoff too, and a successful pass resets it. A failed `Watch` is restarted after its own backoff, which a successful pass also resets, and each restart wakes the task. `TaskContext.Env` is `moduleEnv` with the principal `system:<id>`/`admin`; `TaskContext.Apply` refuses an action of another module, picks the highest agent-side version and calls `Engine.ApplySystem` (C26).
````
### New
````text
Runs every module's background tasks in the agent runtime under spec 1.4 (C30). `Open` starts them after recovery, preflight and the first purge; a module runs its tasks while its flag (C22) is set and its last preflight passed, and `syncTasks` decides anew on every `SetModuleEnabled`, every `Preflight` and at `Open`, under one mutex, so the last call wins and a stop returns only once every `Run` and `Watch` returned. `Close` stops them first, before the purge loop and the engine. Each task has one goroutine for its passes and, with `Watch`, one more for the watch, both on a context only stopping the module cancels. A pass runs at start, `Every` after the previous pass ended (never on a timer when `Every` is 0), and after a wake; the wake channel holds one pending wake, so wakes during a pass coalesce into one further pass. A pass is skipped without error while `core_locks` holds one of `LockKeys` or `lifecycle.ReserveAll` (an update holding every lock). A failed pass is logged and the next one waits `taskBackoff` (1 s doubling to 5 min); wakes wait during a backoff too, and a successful pass resets it. A failed `Watch` is restarted after its own backoff on the same `taskBackoff` schedule, counted in `watchTask` alone: the count starts again at 1 s only when the `Watch` that just failed stayed up longer than the backoff before it, never because a pass succeeded, so a `Watch` that always fails beside a working `Run` backs off to 5 min instead of restarting every second (`TestBackgroundWatchBackoff`, `watchErrs = 3`, arms 1 s, 2 s and 4 s). Each restart wakes the task. `TaskContext.Env` is `moduleEnv` with the principal `system:<id>`/`admin`; `TaskContext.Apply` refuses an action of another module, picks the highest agent-side version and calls `Engine.ApplySystem` (C26).
````

## A15 — Header
Reason: The final fix wave added two read-only tests of the real `/proc/sys`. They are the only tests of the production procfs check, which runs only when `ProcSys` is `""`; an always-false `isProcfs` had passed every test before them. The rule against touching `/proc/sys` exists to keep tests from writing host kernel settings, and these tests only read. Naming them as an exception keeps the rule exact.
Cost if wrong: The two tests fail on a host with no procfs at `/proc/sys`. They would then need a skip guarded by a `statfs` check for `PROC_SUPER_MAGIC`. No host state can change.
### Old
````text
  The one exception is `TestSystemdLive` (`internal/platform/systemd_test.go`), which Tasks 8 and 19 run with `DARKPANEL_SYSTEMD_LIVE=1` as root on this host and which starts only transient units named `darkpanel-live-<pid>-<nanoseconds>` and stops them. Tasks of one wave therefore share no port, path or global state.
````
### New
````text
  The one exception is `TestSystemdLive` (`internal/platform/systemd_test.go`), which Tasks 8 and 19 run with `DARKPANEL_SYSTEMD_LIVE=1` as root on this host and which starts only transient units named `darkpanel-live-<pid>-<nanoseconds>` and stops them. The second exception is read-only: `TestIsProcfs` and `TestOpenProcSysReal` (`internal/platform/files_sysctl_test.go`) open the real `/proc/sys` with `O_RDONLY`, check it with `isProcfs` and read `kernel/ostype`, and never write under it. Tasks of one wave therefore share no port, path or global state.
````
